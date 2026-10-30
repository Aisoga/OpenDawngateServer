package main

// The texture commands: which of the copy's textures a baked ground can name, and pictures of them. Both read the
// player's copy; the pictures are game content (keep them out of the repository).
//
//	mapc textures [--json] [--all] [--min N] <copy>
//	mapc texture  [--json] [--max-texture N] <copy> <path.dds>... <out>
//
// textures lists the .dds files of the copy's shipped archives (not the zz_ ones a setup adds) that suit a ground
// layer, a stamp or the cliff: a format the bake reads (DXT1, DXT5, A8R8G8B8), both sides at least --min pixels (256
// by default), at most twice as long as wide, a colour map by its name (not a normal, specular, gloss, glow or mask
// map: the last part of the name after "_"), and not the interface's (UI/). --all lists every .dds with what it is. Each path once, with the
// last shipped archive holding it: what a texture reference's "game" and "archive" take. Only the files' headers are
// read. --json prints {"ok", "count", "textures": [{"path", "archive", "w", "h", "format", "alpha", "kind",
// "ground"}]}.
//
// texture writes each named texture's top level, halved down to --max-texture pixels (1024 by default), as
// <out>/textures/<name>_<hash>.png (kept when present) and prints {"ok", "textures": [{"path", "file"}]}.

import (
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/depot"
)

const texturesUsage = `  mapc textures [--json] [--all] [--min N] <copy>
  mapc texture  [--json] [--max-texture N] <copy> <path.dds>... <out>`

// textureCommands are the commands this file adds; run hands them their arguments before its own parsing.
var textureCommands = map[string]bool{"textures": true, "texture": true}

// TextureInfo is one listed texture.
type TextureInfo struct {
	Path    string `json:"path"`
	Archive string `json:"archive"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	Format  string `json:"format"` // DXT1, DXT5, ARGB (A8R8G8B8) or what else the header says
	Alpha   bool   `json:"alpha"`  // the format carries alpha (DXT5, ARGB)
	Kind    string `json:"kind"`   // colour, normal or other (by the name)
	Ground  bool   `json:"ground"` // a ground layer, stamp or cliff can name it
}

func runTextures(cmd string, argv []string, stdout, stderr io.Writer) int {
	t := &tool{out: stdout, errw: stderr}
	all, minSide, maxTexture := false, 256, 0
	var rest []string
	bad := func(msg string) int {
		fmt.Fprintf(stderr, "mapc: %s\n%s\n", msg, texturesUsage)
		return 2
	}
	for i := 0; i < len(argv); i++ {
		switch a := argv[i]; a {
		case "--all", "-all":
			all = true
		case "--min", "-min", "--max-texture", "-max-texture":
			n := 0
			if i+1 < len(argv) {
				n, _ = strconv.Atoi(argv[i+1])
			}
			if n < 4 || n > 16384 {
				return bad(a + " needs a number of pixels (4 to 16384)")
			}
			i++
			if strings.HasSuffix(a, "min") {
				minSide = n
			} else {
				maxTexture = n
			}
		default:
			rest = append(rest, a)
		}
	}
	o, err := parse(rest)
	if err != nil {
		return bad(err.Error())
	}
	t.o = o
	if cmd == "texture" {
		if len(o.args) < 3 {
			return bad("texture takes <copy>, at least one .dds path and <out>")
		}
		return t.texturePictures(o.args[0], o.args[1:len(o.args)-1], o.args[len(o.args)-1], maxTexture)
	}
	if len(o.args) != 1 {
		return bad("textures takes <copy>")
	}
	return t.listTextures(o.args[0], all, minSide)
}

// listTextures reads every shipped .dds's header.
func (t *tool) listTextures(copyDir string, all bool, minSide int) int {
	d, err := depot.Open(copyDir)
	if err != nil {
		return t.fail(2, "the copy: %v", err)
	}
	defer d.Close()
	last := map[string]depot.File{} // the last shipped archive's entry per path
	var order []string
	for _, f := range d.Files() {
		if !strings.EqualFold(path.Ext(f.Name), ".dds") || strings.HasPrefix(strings.ToLower(f.Archive), "zz_") ||
			!archiveName(f.Archive) {
			continue
		}
		k := depot.Key(f.Name)
		if _, ok := last[k]; !ok {
			order = append(order, k)
		}
		last[k] = f
	}
	sort.Strings(order)
	list := []TextureInfo{}
	for _, k := range order {
		f := last[k]
		head, err := d.Head(f, 128)
		if err != nil {
			return t.fail(1, "%v", err)
		}
		ti := TextureInfo{Path: strings.ReplaceAll(f.Name, `\`, "/"), Archive: f.Archive, Kind: textureKind(f.Name)}
		ti.W, ti.H, ti.Format = ddsHeader(head)
		ti.Alpha = ti.Format == "DXT5" || ti.Format == "ARGB"
		readable := ti.Format == "DXT1" || ti.Format == "DXT5" || ti.Format == "ARGB"
		ti.Ground = readable && ti.Kind == "colour" && !strings.HasPrefix(k, "ui/") && min(ti.W, ti.H) >= minSide &&
			max(ti.W, ti.H) <= 2*min(ti.W, ti.H)
		if all || ti.Ground {
			list = append(list, ti)
		}
	}
	if t.o.json {
		t.emit(map[string]any{"ok": true, "count": len(list), "textures": list})
		return 0
	}
	for _, ti := range list {
		mark := ""
		if all && !ti.Ground {
			mark = "  (" + ti.Kind + ")"
		}
		fmt.Fprintf(t.out, "%-16s %-80s %4d x %-4d %s%s\n", ti.Archive, ti.Path, ti.W, ti.H, ti.Format, mark)
	}
	fmt.Fprintf(t.out, "%d textures\n", len(list))
	return 0
}

// archiveName is a name a texture reference's archive may take (mapc's ground.texture rule).
func archiveName(n string) bool {
	if !strings.HasSuffix(n, ".snap") || len(n) == len(".snap") {
		return false
	}
	for _, c := range strings.TrimSuffix(n, ".snap") {
		if !(c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// ddsHeader reads a .dds header's size and pixel format ("" when it is none).
func ddsHeader(b []byte) (w, h int, format string) {
	if len(b) < 128 || string(b[:4]) != "DDS " {
		return 0, 0, ""
	}
	le := binary.LittleEndian
	h, w = int(le.Uint32(b[12:])), int(le.Uint32(b[16:]))
	pf, bits := le.Uint32(b[0x50:]), le.Uint32(b[0x58:])
	switch fourcc := string(b[0x54:0x58]); {
	case pf&0x4 != 0:
		format = strings.TrimRight(fourcc, "\x00 ")
	case pf&0x40 != 0 && bits == 32 && pf&0x1 != 0:
		format = "ARGB"
	case pf&0x40 != 0:
		format = "RGB" + strconv.Itoa(int(bits))
	default:
		format = "other"
	}
	return w, h, format
}

// textureKind tells a colour map from the others by the last part of its name ("rock_01_D.dds": D).
func textureKind(p string) string {
	base := strings.ToLower(strings.TrimSuffix(path.Base(strings.ReplaceAll(p, `\`, "/")), path.Ext(p)))
	suffix := base
	if i := strings.LastIndexByte(base, '_'); i >= 0 {
		suffix = base[i+1:]
	}
	switch suffix {
	case "n", "nm", "nrm", "norm", "normal", "normals", "bump":
		return "normal"
	case "s", "sp", "spec", "specular", "g", "gloss", "e", "em", "emissive", "glow", "m", "mask", "a", "alpha",
		"ao", "h", "height", "o", "opacity", "lm", "lightmap", "r", "rough", "i", "illum":
		return "other"
	}
	return "colour"
}

// texturePictures exports textures as PNG through the model export's code (scene.Exporter.Texture).
func (t *tool) texturePictures(copyDir string, paths []string, out string, maxTexture int) int {
	d, err := depot.Open(copyDir)
	if err != nil {
		return t.fail(2, "the copy: %v", err)
	}
	defer d.Close()
	x := (&exportTool{tool: t, x: exportOpts{maxTexture: maxTexture}}).exporter(d, out)
	type pic struct {
		Path  string `json:"path"`
		File  string `json:"file,omitempty"`
		Error string `json:"error,omitempty"`
	}
	var pics []pic
	failed := 0
	for _, p := range paths {
		file, err := x.Texture(p)
		if err != nil {
			pics = append(pics, pic{Path: p, Error: err.Error()})
			failed++
			continue
		}
		pics = append(pics, pic{Path: p, File: file})
	}
	if t.o.json {
		t.emit(map[string]any{"ok": failed == 0, "textures": pics})
	} else {
		for _, p := range pics {
			if p.Error != "" {
				fmt.Fprintf(t.errw, "mapc: %s: %s\n", p.Path, p.Error)
			} else {
				fmt.Fprintf(t.out, "%s -> %s\n", p.Path, p.File)
			}
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}
