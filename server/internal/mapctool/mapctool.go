// Package mapctool is the map compiler's command line, mapc: it checks, compiles and previews a map package (a
// folder with map.json, format 1). Its output is meant for people and for programs alike: every command takes --json
// and then prints one JSON object on stdout. Two programs run it: the developers' tools/mapc and the release's
// Start-Client.exe --mapc <args>; both hand their arguments to Run, so their output is the same byte for byte.
//
// The interface's version (MAPED-REL1): `mapc version --json` prints {"release", "format", "api"}: the release
// (Start-Client's --version), the map format this compiler reads (mapc.Format) and API, the version of this command
// line for the programs that run mapc and parse its output. **Bump API when a subcommand's arguments, flags, exit
// codes or JSON change incompatibly** (a removed or renamed flag or field, a changed meaning); a new subcommand,
// flag or JSON field that old callers can ignore keeps it.
//
// Run is meant to run once per process: icon's --size is a package variable (iconSize), written by every props
// command, so two Runs at once in one process would race on it. The programs that run mapc start it as a child
// process (one Run each), which also keeps a panic or a long bake out of their own process and lets them stop it
// (mapc has no cancellation).
//
// Usage (from server\; Start-Client.exe --mapc takes the same arguments):
//
//	go run ./tools/mapc version [--json]
//	go run ./tools/mapc check   [--json] [--copy <copy>] [--maps <dir>] <package>
//	go run ./tools/mapc build   [--json] [--no-mesh] [--maps <dir>] <package> <copy> <out>
//	go run ./tools/mapc preview [--json] [--copy <copy>] [--maps <dir>] <package> <dir>
//	go run ./tools/mapc bake    [--json] [--full] [--maps <dir>] <package> <copy> <dir>  (bake.go: a baked ground's pictures)
//	go run ./tools/mapc cook-shared [--json] <physcook.exe> <PhysXCooking.dll>
//	go run ./tools/mapc model | ground | scene ...  (export.go: models, the ground and scene lists as glTF)
//	go run ./tools/mapc props | nouns ...  (props.go: what a map's props place, the nouns a prop may name)
//	go run ./tools/mapc textures | texture ...  (textures.go: the copy's textures a ground can name, their pictures)
//	go run ./tools/mapc import --copy <copy> [--dress] [--name ...] <level> <package>  (import.go: a shipped level as a package)
//
// check runs the rule checks: --json prints the report ({"map", "ok", "findings": [{"rule", "severity", "path",
// "at", "message"}], "stats"}); each finding names the place in map.json it concerns ("base.spawns[2]",
// "terrain.shapes[4]") and, where it has one, a world position. --copy adds the rules that need the game's catalog
// (the nouns it names) and stamps the blocking props by their nouns' boxes, as build does. The other installed maps are the package's sibling folders (their names and tags must
// differ); --maps names another folder of installed maps instead (for checking a draft kept outside it), where a
// package in a folder of the package's own folder name counts as the package itself.
//
// build makes <out> (a file, or a folder that gets zz_<name>.snap) from the copy's files (the folder with game\);
// a map that fails a rule isn't built. The PhysX mesh is the shared floor every map uses (shipped with the compiler);
// --no-mesh leaves it out: such an archive is for inspection only, the game can't load the level without it. The
// archive holds the level files and the map's assets (its Level, its
// markersets and its terrain noun); --json adds "catalog" (the entries the copy's one catalog must list for them:
// [{"name", "source", "tags"}]) and "label" (what offers the map: its tag, prefix, display name, the map list's line
// and the lobby's locale line).
//
// preview writes the derived layers as PNG pictures (north up) and preview.json, which lists the pictures, every
// derived object of both teams, the lanes, the bushes, the check's report and the pixel mapping (and the props
// placed without the copy: single props and scatter; --copy as for check), the chasms' water planes and "bots": the
// points the match host's bots derive from the map (each team's rally point for the parasite and Hunter route, by the
// host's rules). A reach.dropped finding has "at" (the largest cut-off part) and "parts" (each part of team 1's half:
// "at", "centre", "r", "cells").
//
// cook-shared (developers) cooks the shared mesh again with PhysX's cooker (the physcook tool and the game's
// PhysXCooking.dll) and checks that it gives the shipped bytes.
//
// Exit 0: done (check: no error); 1: the map fails a rule or the step failed; 2: wrong arguments or unreadable input.
package mapctool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dawngate/server/internal/setup/mapc"
)

// API is the version of mapc's command line for the programs that run it (`mapc version --json`'s "api"). Bump it
// when a subcommand's arguments, flags, exit codes or JSON change incompatibly (the package comment has the rule).
const API = 1

const usage = `usage:
  mapc version [--json]
  mapc check   [--json] [--copy <copy>] [--maps <dir>] <package>
  mapc build   [--json] [--no-mesh] [--maps <dir>] <package> <copy> <out>
  mapc preview [--json] [--copy <copy>] [--maps <dir>] <package> <dir>
  mapc bake    [--json] [--full] [--maps <dir>] <package> <copy> <dir>
  mapc cook-shared [--json] <physcook.exe> <PhysXCooking.dll>
  mapc import [--json] [--force] [--dress] --copy <copy> [--name N] [--display D] [--tag T] [--prefix P] <level> <package>`

type opts struct {
	json, noMesh bool
	full         bool // bake: every tile at its own size too
	copyDir      string
	mapsDir      string
	args         []string
}

func parse(argv []string) (opts, error) {
	var o opts
	for i := 0; i < len(argv); i++ {
		switch a := argv[i]; a {
		case "--json", "-json":
			o.json = true
		case "--no-mesh", "-no-mesh":
			o.noMesh = true
		case "--full", "-full":
			o.full = true
		case "--copy", "-copy":
			if i+1 == len(argv) {
				return o, fmt.Errorf("--copy needs a folder")
			}
			i++
			o.copyDir = argv[i]
		case "--maps", "-maps":
			if i+1 == len(argv) {
				return o, fmt.Errorf("--maps needs a folder")
			}
			i++
			o.mapsDir = argv[i]
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("unknown option %s", a)
			}
			o.args = append(o.args, a)
		}
	}
	return o, nil
}

// Run runs one mapc command (argv without the program's name: "check", "--json", <package>) with its output on
// stdout and its errors on stderr, and returns the exit code (0 done, 1 failed, 2 wrong arguments). Once per process:
// the package comment says why.
func Run(argv []string, stdout, stderr io.Writer) int {
	if len(argv) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	cmd := argv[0]
	if cmd == "version" {
		return runVersion(argv[1:], stdout, stderr)
	}
	if _, ok := propCommands[cmd]; ok { // props.go
		return runProps(cmd, argv[1:], stdout, stderr)
	}
	if _, ok := exportCommands[cmd]; ok { // export.go
		return runExport(cmd, argv[1:], stdout, stderr)
	}
	if textureCommands[cmd] { // textures.go
		return runTextures(cmd, argv[1:], stdout, stderr)
	}
	if cmd == "import" { // import.go
		return runImport(argv[1:], stdout, stderr)
	}
	o, err := parse(argv[1:])
	want := map[string]int{"check": 1, "build": 3, "preview": 2, "bake": 3, "cook-shared": 2}
	n, known := want[cmd]
	if err == nil && (!known || len(o.args) != n) {
		err = fmt.Errorf("%s takes %d arguments", cmd, n)
		if !known {
			err = fmt.Errorf("unknown command %q", cmd)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "mapc: %v\n%s\n", err, usage)
		return 2
	}
	t := &tool{o: o, out: stdout, errw: stderr}
	switch cmd {
	case "check":
		return t.check()
	case "build":
		return t.build()
	case "preview":
		return t.preview()
	case "bake":
		return t.bakePictures() // bake.go
	default:
		return t.cookShared()
	}
}

type tool struct {
	o         opts
	out, errw io.Writer
}

func (t *tool) fail(code int, format string, args ...any) int {
	msg := fmt.Sprintf(format, args...)
	if t.o.json {
		t.emit(map[string]any{"ok": false, "error": msg})
	} else {
		fmt.Fprintln(t.errw, "mapc: "+msg)
	}
	return code
}

func (t *tool) emit(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	t.out.Write(append(b, '\n'))
}

// others are the other installed packages' names and tags: the package's siblings, or with mapsDir that folder's
// packages but the one in a folder of the package's own folder name.
func others(pkg, mapsDir string) []mapc.Other {
	abs, err := filepath.Abs(pkg)
	if err != nil {
		return nil
	}
	parent := filepath.Dir(abs)
	if mapsDir != "" {
		if parent, err = filepath.Abs(mapsDir); err != nil {
			return nil
		}
	}
	ents, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}
	var out []mapc.Other
	for _, e := range ents {
		dir := filepath.Join(parent, e.Name())
		if !e.IsDir() || strings.EqualFold(dir, abs) || (mapsDir != "" && strings.EqualFold(e.Name(), filepath.Base(abs))) {
			continue
		}
		if m, err := mapc.Load(dir); err == nil {
			out = append(out, mapc.Other{Name: m.Name, Tag: m.Tag})
		}
	}
	return out
}

func (t *tool) load(pkg string) (*mapc.Map, int) {
	m, err := mapc.Load(pkg)
	if err != nil {
		if t.o.json {
			t.emit(&mapc.Report{Findings: []mapc.Finding{{Rule: "format.json", Severity: mapc.Error,
				Message: err.Error()}}})
			return nil, 1
		}
		fmt.Fprintln(t.errw, "mapc: "+err.Error())
		return nil, 1
	}
	return m, 0
}

func (t *tool) printReport(r *mapc.Report) {
	for _, f := range r.Findings {
		where := f.Path
		if f.At != nil {
			where += fmt.Sprintf(" at (%.1f, %.1f)", f.At[0], f.At[1])
		}
		fmt.Fprintf(t.out, "%s %s: %s: %s\n", f.Severity, f.Rule, strings.TrimSpace(where), f.Message)
	}
	if s := r.Stats; s != nil {
		fmt.Fprintf(t.out, "%s: graphs of %d (Player) and %d (NPC) squares of %d cells (%.2f m each), lanes %v m, "+
			"%d bushes, %d wells\n", r.Map, s.PlayerAreas, s.NPCAreas, s.Cell, s.CellSize, s.LaneLengths, s.Bushes,
			s.Wells)
	}
	if r.OK {
		fmt.Fprintf(t.out, "%s: passes\n", r.Map)
	} else {
		fmt.Fprintf(t.out, "%s: fails\n", r.Map)
	}
}

func (t *tool) check() int {
	pkg := t.o.args[0]
	m, code := t.load(pkg)
	if m == nil {
		return code
	}
	if code := t.resolveProps(m); code != 0 {
		return code
	}
	r, _ := mapc.Check(m, others(pkg, t.o.mapsDir))
	if t.o.copyDir != "" {
		s, err := mapc.ReadShipped(t.o.copyDir)
		if err != nil {
			return t.fail(2, "the copy: %v", err)
		}
		mapc.CheckShipped(m, s, r)
	}
	if t.o.json {
		t.emit(r)
	} else {
		t.printReport(r)
	}
	if !r.OK {
		return 1
	}
	return 0
}

func (t *tool) build() int {
	pkg, copyDir, out := t.o.args[0], t.o.args[1], t.o.args[2]
	m, code := t.load(pkg)
	if m == nil {
		return code
	}
	in, err := mapc.ReadInputs(copyDir)
	if err != nil {
		return t.fail(2, "the copy: %v", err)
	}
	var mesh mapc.MeshSource = mapc.SharedMesh{}
	if t.o.noMesh {
		mesh = nil
	}
	b, r, err := mapc.Build(m, in, mesh, others(pkg, t.o.mapsDir))
	if err != nil {
		if t.o.json {
			t.emit(map[string]any{"ok": false, "error": err.Error(), "report": r})
		} else {
			if r != nil {
				t.printReport(r)
			}
			fmt.Fprintln(t.errw, "mapc: "+err.Error())
		}
		return 1
	}
	if st, err := os.Stat(out); err == nil && st.IsDir() {
		out = filepath.Join(out, mapc.File(m.Name))
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return t.fail(1, "%v", err)
	}
	sum := mapc.SHA256(b)
	if t.o.json {
		t.emit(map[string]any{"ok": true, "map": m.Name, "archive": out, "bytes": len(b), "sha256": sum,
			"mesh": !t.o.noMesh, "report": r, "catalog": catalogJSON(m), "label": mapc.LabelOf(m)})
	} else {
		t.printReport(r)
		fmt.Fprintf(t.out, "%s: %d bytes, sha256 %s\n", out, len(b), sum)
		l := mapc.LabelOf(m)
		fmt.Fprintf(t.out, "%s: %d assets for the catalog; offered as %q (tag %s, prefix %q)\n", m.Name,
			len(mapc.MapAssetNames(m)), l.Display, l.Tag, l.Prefix)
	}
	return 0
}

// catalogJSON is the map's catalog entries as build --json prints them: [{"name", "source", "tags"}].
func catalogJSON(m *mapc.Map) []map[string]any {
	var out []map[string]any
	for _, e := range mapc.Catalog(m).Entries {
		tags := e.Tags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, map[string]any{"name": e.Name, "source": e.Source, "tags": tags})
	}
	return out
}

func (t *tool) preview() int {
	pkg, dir := t.o.args[0], t.o.args[1]
	m, code := t.load(pkg)
	if m == nil {
		return code
	}
	if code := t.resolveProps(m); code != 0 {
		return code
	}
	r, cm := mapc.Check(m, others(pkg, t.o.mapsDir))
	if cm == nil {
		if t.o.json {
			t.emit(map[string]any{"ok": false, "error": "the map fails a structural rule", "report": r})
		} else {
			t.printReport(r)
		}
		return 1
	}
	files, err := cm.Preview(r)
	if err != nil {
		return t.fail(1, "%v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return t.fail(1, "%v", err)
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), files[n], 0o644); err != nil {
			return t.fail(1, "%v", err)
		}
	}
	if t.o.json {
		t.emit(map[string]any{"ok": r.OK, "dir": dir, "files": names, "report": r})
	} else {
		t.printReport(r)
		fmt.Fprintf(t.out, "%s: %s\n", dir, strings.Join(names, ", "))
	}
	return 0
}

// cookShared cooks the shared PhysX mesh's input with PhysX's cooker (the developers' physcook tool and the game's
// PhysXCooking.dll) and compares the result with the shipped mesh (exit 1 when it differs).
func (t *tool) cookShared() int {
	cooker, dll := t.o.args[0], t.o.args[1]
	cooked, _, err := mapc.CookShared(cooker, dll)
	if err != nil {
		return t.fail(1, "%v", err)
	}
	sum := mapc.SHA256(cooked)
	same := sum == mapc.SharedCookedSHA256
	if t.o.json {
		t.emit(map[string]any{"ok": same, "cookedBytes": len(cooked), "cookedSHA256": sum,
			"inputSHA256": mapc.SHA256(mapc.SharedMeshInput()), "shippedSHA256": mapc.SharedCookedSHA256})
	} else {
		fmt.Fprintf(t.out, "the shared mesh: %d cooked bytes, sha256 %s (shipped %s)\n", len(cooked), sum,
			mapc.SharedCookedSHA256)
	}
	if !same {
		return 1
	}
	return 0
}
