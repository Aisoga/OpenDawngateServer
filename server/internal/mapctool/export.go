package mapctool

// The export commands: the game's models, a map package's ground and a scene list, as glTF 2.0 and PNG for a
// browser or a 3-D program. They read the player's copy and write into a folder the caller names; the output is
// game content (keep it out of the repository).
//
//	mapc model  [--json] [--max-texture N] <copy> <model.bmdl | Name.Noun>... <out>
//	mapc ground [--json] [--max-texture N] [--maps <dir>] <package> <copy> <out>
//	mapc scene  [--json] [--max-texture N] [--maps <dir>] [--no-models] <copy> <package | level> <out>
//
// model exports models of the copy (by path, or a noun's model) as <out>/<path with folders joined by _>.gltf with
// its .bin and the colour textures as <out>/textures/*.png (the longest side halved down to --max-texture, 1024 by
// default). An export of the same model already in <out> is kept.
//
// ground exports the ground model the compiler makes for a package (<out>/<name>_ground.gltf), with its textures.
//
// scene writes <out>/<name>.scene.json: for a package (a folder with map.json) the markers of its compiled Level (what
// `mapc build` writes; export_compiled.go), each drawn as a part of the derived object it stands at (as preview.json
// keys them) or on its own, the Guardians spawned at play time, and the ground; for a level of the copy (its name)
// every marker with its noun's model. The models are exported beside it unless --no-models.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/depot"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/scene"
)

const exportUsage = `  mapc model  [--json] [--max-texture N] <copy> <model.bmdl | Name.Noun>... <out>
  mapc ground [--json] [--max-texture N] [--maps <dir>] <package> <copy> <out>
  mapc scene  [--json] [--max-texture N] [--maps <dir>] [--no-models] <copy> <package | level> <out>`

// exportCommands are the commands this file adds; Run hands them their arguments before its own parsing.
var exportCommands = map[string]func(t *exportTool, args []string) int{
	"model":  (*exportTool).exportModel,
	"ground": (*exportTool).exportGround,
	"scene":  (*exportTool).exportScene,
}

// exportTool is the tool with the export options.
type exportTool struct {
	*tool
	x exportOpts
}

type exportOpts struct {
	maxTexture int
	noModels   bool
}

// runExport parses the export options (and the shared --json and --maps) and runs the command.
func runExport(cmd string, argv []string, stdout, stderr io.Writer) int {
	t := &exportTool{tool: &tool{out: stdout, errw: stderr}}
	var rest []string
	for i := 0; i < len(argv); i++ {
		switch a := argv[i]; a {
		case "--max-texture", "-max-texture":
			if i+1 == len(argv) {
				return t.usage("--max-texture needs a number")
			}
			i++
			n, err := strconv.Atoi(argv[i])
			if err != nil || n < 4 {
				return t.usage("--max-texture needs a number of at least 4")
			}
			t.x.maxTexture = n
		case "--no-models", "-no-models":
			t.x.noModels = true
		default:
			rest = append(rest, a)
		}
	}
	o, err := parse(rest)
	if err != nil {
		return t.usage(err.Error())
	}
	t.o = o
	return exportCommands[cmd](t, o.args)
}

func (t *exportTool) usage(msg string) int {
	fmt.Fprintf(t.errw, "mapc: %s\n%s\n%s\n", msg, usage, exportUsage)
	return 2
}

func (t *exportTool) depot(copyDir string) (*depot.Depot, int) {
	d, err := depot.Open(copyDir)
	if err != nil {
		return nil, t.fail(2, "the copy: %v", err)
	}
	return d, 0
}

func (t *exportTool) exporter(d *depot.Depot, out string) *scene.Exporter {
	return &scene.Exporter{Dir: out, MaxTexture: t.x.maxTexture, Read: d.Read}
}

func (t *exportTool) exportModel(args []string) int {
	if len(args) < 3 {
		return t.usage("model takes <copy>, at least one model and <out>")
	}
	d, code := t.depot(args[0])
	if d == nil {
		return code
	}
	defer d.Close()
	x := t.exporter(d, args[len(args)-1])
	var as *scene.Assets
	var infos []scene.ModelInfo
	failed := 0
	for _, name := range args[1 : len(args)-1] {
		path := name
		if strings.HasSuffix(strings.ToLower(name), ".noun") {
			if as == nil {
				var err error
				if as, err = scene.NewAssets(d); err != nil {
					return t.fail(1, "%v", err)
				}
			}
			n := as.Noun(name)
			if n == nil || n.Model == "" {
				fmt.Fprintf(t.errw, "mapc: %s: no such noun, or it draws no model\n", name)
				failed++
				continue
			}
			path = n.Model
		}
		info, err := x.Model(path)
		if err != nil {
			fmt.Fprintf(t.errw, "mapc: %s: %v\n", name, err)
			failed++
			continue
		}
		infos = append(infos, info)
		if !t.o.json {
			fmt.Fprintf(t.out, "%s: %s, %d meshes, %d triangles, %d textures%s\n", info.Source, info.File, info.Meshes,
				info.Triangles, len(info.Textures), cachedNote(info.Cached))
		}
	}
	if t.o.json {
		t.emit(map[string]any{"ok": failed == 0, "dir": x.Dir, "models": infos, "failed": failed})
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func cachedNote(c bool) string {
	if c {
		return " (kept)"
	}
	return ""
}

// compiled loads and checks a package (its blocking props' footprints from the copy); nil (and the reply) when it
// fails a structural rule.
func (t *exportTool) compiled(pkg, copyDir string) (*mapc.Compiled, int) {
	m, code := t.load(pkg)
	if m == nil {
		return nil, code
	}
	if code := t.resolvePropsFrom(m, copyDir); code != 0 {
		return nil, code
	}
	r, cm := mapc.Check(m, others(pkg, t.o.mapsDir))
	if cm == nil {
		if t.o.json {
			t.emit(map[string]any{"ok": false, "error": "the map fails a structural rule", "report": r})
		} else {
			t.printReport(r)
		}
		return nil, 1
	}
	return cm, 0
}

func (t *exportTool) exportGround(args []string) int {
	if len(args) != 3 {
		return t.usage("ground takes <package> <copy> <out>")
	}
	cm, code := t.compiled(args[0], args[1])
	if cm == nil {
		return code
	}
	d, code := t.depot(args[1])
	if d == nil {
		return code
	}
	defer d.Close()
	info, err := scene.ExportGround(cm, d, t.exporter(d, args[2]))
	if err != nil {
		return t.fail(1, "%v", err)
	}
	if t.o.json {
		t.emit(map[string]any{"ok": true, "dir": args[2], "ground": info})
	} else {
		fmt.Fprintf(t.out, "%s: %s, %d triangles%s\n", cm.Map.Name, filepath.Join(args[2], info.File), info.Triangles,
			cachedNote(info.Cached))
	}
	return 0
}

func (t *exportTool) exportScene(args []string) int {
	if len(args) != 3 {
		return t.usage("scene takes <copy> <package | level> <out>")
	}
	d, code := t.depot(args[0])
	if d == nil {
		return code
	}
	defer d.Close()
	as, err := scene.NewAssets(d)
	if err != nil {
		return t.fail(1, "%v", err)
	}
	out := args[2]
	x := t.exporter(d, out)
	var sc *scene.Scene
	if st, err := os.Stat(filepath.Join(args[1], mapc.FileName)); err == nil && !st.IsDir() {
		cm, code := t.compiled(args[1], args[0])
		if cm == nil {
			return code
		}
		if sc, err = compiledScene(as, cm, d, args[0]); err != nil {
			return t.fail(1, "%v", err)
		}
		g, err := scene.ExportGround(cm, d, x)
		if err != nil {
			return t.fail(1, "the ground: %v", err)
		}
		sc.Ground = g.File
	} else if sc, err = as.LevelScene(args[1]); err != nil {
		return t.fail(2, "%s is neither a package nor a level of the copy: %v", args[1], err)
	}
	if !t.x.noModels {
		sc.ExportModels(x)
	}
	sort.Strings(sc.Missing)
	b, err := json.MarshalIndent(sc, "", " ")
	if err != nil {
		return t.fail(1, "%v", err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return t.fail(1, "%v", err)
	}
	file := filepath.Join(out, sc.Map+".scene.json")
	if err := os.WriteFile(file, b, 0o644); err != nil {
		return t.fail(1, "%v", err)
	}
	parts := 0
	for _, o := range sc.Objects {
		parts += len(o.Parts)
	}
	if t.o.json {
		t.emit(map[string]any{"ok": true, "scene": file, "objects": len(sc.Objects), "parts": parts,
			"models": len(sc.Models), "ground": sc.Ground, "missing": sc.Missing})
	} else {
		fmt.Fprintf(t.out, "%s: %d objects, %d models drawn %d times; %s\n", sc.Map, len(sc.Objects), len(sc.Models),
			parts, file)
		for _, m := range sc.Missing {
			fmt.Fprintln(t.out, "missing: "+m)
		}
	}
	return 0
}
