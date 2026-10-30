package main

// The props commands: what a map's props place, and which shipped nouns a prop may name. Both read the player's
// copy (the nouns' boxes, a group's markers); their output names shipped assets only.
//
//	mapc props [--json] [--maps <dir>] <package> <copy>
//	mapc nouns [--json] <copy>
//	mapc icon  [--json] [--size N] <copy> <Name.Noun>... <out>
//
// props lists every marker the package's props markerset gets, in its order: the noun, the place (x, y, z), the
// turn (the marker's rotDegrees), the scale, the team (1, 2 the mirror, 0 a prop at the centre), the entry of
// map.json it comes from ("props[3]", "scatter[0]"), a blocking prop's footprint and the shadow cards; then the
// placing's findings. --json prints {"ok", "map", "props": [...], "findings": [...]}.
//
// nouns lists the copy's nouns a prop may name (the dressing: obstacles, terrain pieces, water, effects, lights,
// decals) with their type, model, graphicsScale (the model is drawn at it times the marker's scale) and box (the bbox
// times the graphicsScale): {"ok", "nouns": [{"name", "type", "model", "graphicsScale", "box": {"min", "max"}}]}.
//
// icon bakes each noun's model seen from straight above (north up, transparent background, --size pixels square,
// 128 by default) into <out>/<noun>.png, with <out>/<noun>.json saying where it lies on the ground: {"noun", "file",
// "size", "centre" (the model-space point at the picture's centre), "metresPerPixel"} at a marker scale of 1. An icon
// of the same size already in <out> is kept. The pictures are game content: keep them out of the repository.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/depot"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/scene"
)

const propsUsage = `  mapc props [--json] [--maps <dir>] <package> <copy>
  mapc nouns [--json] <copy>
  mapc icon  [--json] [--size N] <copy> <Name.Noun>... <out>`

// propCommands are the commands this file adds; run hands them their arguments before its own parsing.
var propCommands = map[string]func(t *tool, args []string) int{
	"props": (*tool).props,
	"nouns": (*tool).nouns,
	"icon":  (*tool).icon,
}

// iconSize is icon's --size (pixels, default 128).
var iconSize = 128

func runProps(cmd string, argv []string, stdout, stderr io.Writer) int {
	t := &tool{out: stdout, errw: stderr}
	iconSize = 128
	var rest []string
	for i := 0; i < len(argv); i++ {
		if a := argv[i]; a == "--size" || a == "-size" {
			n := 0
			if i+1 < len(argv) {
				n, _ = strconv.Atoi(argv[i+1])
			}
			if n < 16 || n > 2048 {
				fmt.Fprintf(stderr, "mapc: --size needs 16 to 2048 pixels\n%s\n", propsUsage)
				return 2
			}
			iconSize = n
			i++
			continue
		}
		rest = append(rest, argv[i])
	}
	argv = rest
	o, err := parse(argv)
	if err != nil {
		fmt.Fprintf(stderr, "mapc: %v\n%s\n%s\n", err, usage, propsUsage)
		return 2
	}
	t.o = o
	return propCommands[cmd](t, o.args)
}

func readAssetData(copyDir string) ([]byte, error) {
	return os.ReadFile(filepath.Join(copyDir, filepath.FromSlash(mapc.AssetDataSource)))
}

// resolveProps reads the props' nouns' boxes from --copy (when given and the map has props), so check and preview
// stamp the blocking props' footprints as build does.
func (t *tool) resolveProps(m *mapc.Map) int {
	return t.resolvePropsFrom(m, t.o.copyDir)
}

func (t *tool) resolvePropsFrom(m *mapc.Map, copyDir string) int {
	if copyDir == "" || !mapc.HasProps(m) {
		return 0
	}
	ad, err := readAssetData(copyDir)
	if err == nil {
		err = mapc.ResolveProps(m, ad)
	}
	if err != nil {
		return t.fail(2, "the copy: %v", err)
	}
	return 0
}

func (t *tool) props(args []string) int {
	if len(args) != 2 {
		fmt.Fprintf(t.errw, "mapc: props takes <package> <copy>\n%s\n", propsUsage)
		return 2
	}
	m, code := t.load(args[0])
	if m == nil {
		return code
	}
	ad, err := readAssetData(args[1])
	if err != nil {
		return t.fail(2, "the copy: %v", err)
	}
	if err := mapc.ResolveProps(m, ad); err != nil {
		return t.fail(2, "the copy: %v", err)
	}
	r, cm := mapc.Check(m, others(args[0], t.o.mapsDir))
	if cm == nil {
		if t.o.json {
			t.emit(map[string]any{"ok": false, "error": "the map fails a structural rule", "report": r})
		} else {
			t.printReport(r)
		}
		return 1
	}
	pls, fs, err := cm.PlacedProps(ad)
	if err != nil {
		return t.fail(1, "%v", err)
	}
	if fs == nil {
		fs = []mapc.Finding{}
	}
	if pls == nil {
		pls = []mapc.Placed{}
	}
	ok := true
	for _, f := range fs {
		ok = ok && f.Severity != mapc.Error
	}
	if t.o.json {
		t.emit(map[string]any{"ok": ok, "map": m.Name, "props": pls, "findings": fs})
	} else {
		for _, p := range pls {
			extra := ""
			if f := p.Footprint; f != nil {
				extra = fmt.Sprintf(", blocks %.1f x %.1f m at (%.1f, %.1f)", 2*f.Axes[0], 2*f.Axes[1], f.Centre[0],
					f.Centre[1])
			}
			fmt.Fprintf(t.out, "%-11s team %d %s at (%.2f, %.2f, %.2f) turned %.1f scale %.2f%s\n", p.Path, p.Team,
				p.Noun, p.Pos[0], p.Pos[1], p.Pos[2], p.Rot[2], p.Scale, extra)
		}
		t.printReport(&mapc.Report{Map: m.Name, OK: ok, Findings: fs})
		fmt.Fprintf(t.out, "%s: %d props\n", m.Name, len(pls))
	}
	if !ok {
		return 1
	}
	return 0
}

// icon bakes top-down icons of nouns' models into <out>/<noun>.png with <out>/<noun>.json (scene.IconInfo: where
// the picture lies on the ground); an icon of the same size already there is kept.
func (t *tool) icon(args []string) int {
	if len(args) < 3 {
		fmt.Fprintf(t.errw, "mapc: icon takes <copy>, at least one noun and <out>\n%s\n", propsUsage)
		return 2
	}
	d, err := depot.Open(args[0])
	if err != nil {
		return t.fail(2, "the copy: %v", err)
	}
	defer d.Close()
	as, err := scene.NewAssets(d)
	if err != nil {
		return t.fail(1, "%v", err)
	}
	out := args[len(args)-1]
	if err := os.MkdirAll(out, 0o755); err != nil {
		return t.fail(1, "%v", err)
	}
	var infos []scene.IconInfo
	failed := 0
	for _, noun := range args[1 : len(args)-1] {
		base := scene.FileName(strings.TrimSuffix(noun, ".Noun")+".Noun", "")
		pngFile, jsonFile := filepath.Join(out, base+".png"), filepath.Join(out, base+".json")
		var info scene.IconInfo
		if b, err := os.ReadFile(jsonFile); err == nil && json.Unmarshal(b, &info) == nil && info.Size == iconSize {
			if _, err := os.Stat(pngFile); err == nil {
				info.Cached = true
				infos = append(infos, info)
				continue
			}
		}
		img, info, err := as.Icon(noun, iconSize)
		if err != nil {
			fmt.Fprintf(t.errw, "mapc: %s: %v\n", noun, err)
			failed++
			continue
		}
		info.File = base + ".png"
		b, _ := json.Marshal(info)
		if err := os.WriteFile(pngFile, img, 0o644); err != nil {
			return t.fail(1, "%v", err)
		}
		if err := os.WriteFile(jsonFile, b, 0o644); err != nil {
			return t.fail(1, "%v", err)
		}
		infos = append(infos, info)
	}
	if t.o.json {
		t.emit(map[string]any{"ok": failed == 0, "dir": out, "icons": infos, "failed": failed})
	} else {
		for _, i := range infos {
			fmt.Fprintf(t.out, "%s: %s, %d px, %.3f m a pixel%s\n", i.Noun, filepath.Join(out, i.File), i.Size,
				i.PerPixel, cachedNote(i.Cached))
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func (t *tool) nouns(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(t.errw, "mapc: nouns takes <copy>\n%s\n", propsUsage)
		return 2
	}
	ad, err := readAssetData(args[0])
	if err != nil {
		return t.fail(2, "the copy: %v", err)
	}
	ns, err := mapc.PropNouns(ad)
	if err != nil {
		return t.fail(1, "%v", err)
	}
	if t.o.json {
		t.emit(map[string]any{"ok": true, "nouns": ns})
		return 0
	}
	for _, n := range ns {
		b := n.Box
		fmt.Fprintf(t.out, "%-48s %-16s %5.1f x %5.1f x %5.1f m  %s\n", n.Name, n.Type, b.Max[0]-b.Min[0],
			b.Max[1]-b.Min[1], b.Max[2]-b.Min[2], n.Model)
	}
	fmt.Fprintf(t.out, "%d nouns\n", len(ns))
	return 0
}
