package mapctool

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/mapc"
)

// import writes a package from a shipped level of the copy (MAPED-IMPORT):
//
//	mapc import [--json] [--force] [--dress] --copy <copy> [--name N] [--display D] [--tag T] [--prefix P]
//	            [--tolerance cells] <level> <package dir>
//
// With --dress the level's markersets that hold dressing nouns (its environment excepted) become props groups that
// stand once ("mirror": false) over the whole level (a polygon region reaching past its edges, for its backdrop), so
// both halves wear the level's own dressing; the report lists the markers they leave out (a mostly gameplay set's
// other markers), the sets it didn't take, and how far a mirror of team 1's half would differ from team 2's.
//
// It reads the level's Level, markersets and line-of-sight map from the copy (game\Data\AssetData.snap, images.snap),
// and its Player navigation graph from the level's own archive when the copy has one (game\Data\<level>.snap; the
// see-through ground off the graph becomes chasm), writes <package dir>\map.json (format 1: team 1's half, the compiler mirrors team 2) and checks it as check --copy
// does, with the package's siblings as the other installed maps. The name defaults to the folder's name, the display
// name to the name without "CM_" (13 characters), the tag to the display name's letters and digits in lower case, the
// prefix to "[<display>] ". An existing map.json is kept unless --force. The report says how far the level's own team 2
// lies from team 1's mirror, how many line-of-sight cells the package gets different from the level's, and what the
// import changed (mapc.ImportReport). Exit 0: written and passes; 1: written but fails a rule, or the import failed;
// 2: wrong arguments or unreadable input.
var importUsage = `usage:
  mapc import [--json] [--force] [--dress] --copy <copy> [--name N] [--display D] [--tag T] [--prefix P]
              [--tolerance cells] <level> <package dir>`

func runImport(argv []string, stdout, stderr io.Writer) int {
	var o opts
	var force bool
	io2 := mapc.ImportOptions{}
	val := map[string]*string{"--name": &io2.Name, "--display": &io2.Display, "--tag": &io2.Tag, "--prefix": &io2.Prefix}
	tol := ""
	for i := 0; i < len(argv); i++ {
		a := strings.Replace(argv[i], "--", "-", 1)
		a = "-" + a // -x -> --x
		switch a {
		case "--json":
			o.json = true
		case "--force":
			force = true
		case "--dress":
			io2.Dress = true
		case "--copy", "--tolerance", "--name", "--display", "--tag", "--prefix":
			if i+1 == len(argv) {
				fmt.Fprintf(stderr, "mapc: %s needs a value\n%s\n", a, importUsage)
				return 2
			}
			i++
			switch a {
			case "--copy":
				o.copyDir = argv[i]
			case "--tolerance":
				tol = argv[i]
			default:
				*val[a] = argv[i]
			}
		default:
			if strings.HasPrefix(argv[i], "-") {
				fmt.Fprintf(stderr, "mapc: unknown option %s\n%s\n", argv[i], importUsage)
				return 2
			}
			o.args = append(o.args, argv[i])
		}
	}
	if len(o.args) != 2 || o.copyDir == "" {
		fmt.Fprintf(stderr, "mapc: import takes a level and a package folder, and --copy\n%s\n", importUsage)
		return 2
	}
	if tol != "" {
		f, err := strconv.ParseFloat(tol, 64)
		if err != nil || f <= 0 || f > 4 {
			fmt.Fprintf(stderr, "mapc: --tolerance %q: a number of cells above 0, at most 4\n", tol)
			return 2
		}
		io2.Tolerance = f
	}
	t := &tool{o: o, out: stdout, errw: stderr}
	return t.importLevel(o.args[0], o.args[1], io2, force)
}

var notTagChar = regexp.MustCompile(`[^a-z0-9]+`)

// importDefaults fills the names the command line left out.
func importDefaults(pkg string, o *mapc.ImportOptions) {
	if o.Name == "" {
		o.Name = filepath.Base(filepath.Clean(pkg))
	}
	if o.Display == "" {
		d := strings.ReplaceAll(strings.TrimPrefix(o.Name, "CM_"), "_", " ")
		if len(d) > 13 {
			d = strings.TrimSpace(d[:13])
		}
		o.Display = d
	}
	if o.Tag == "" {
		o.Tag = notTagChar.ReplaceAllString(strings.ToLower(o.Display), "")
	}
	if o.Prefix == "" {
		o.Prefix = "[" + o.Display + "] "
		if len(o.Prefix) > 16 {
			o.Prefix = o.Prefix[:16]
		}
	}
}

func (t *tool) importLevel(level, pkg string, o mapc.ImportOptions, force bool) int {
	importDefaults(pkg, &o)
	file := filepath.Join(pkg, mapc.FileName)
	if _, err := os.Stat(file); err == nil && !force {
		return t.fail(2, "%s exists (--force replaces it)", file)
	}
	var in mapc.ImportInputs
	for k, rel := range mapc.ImportSources {
		b, err := os.ReadFile(filepath.Join(t.o.copyDir, filepath.FromSlash(rel)))
		if err != nil {
			return t.fail(2, "the copy: %v", err)
		}
		if k == 0 {
			in.AssetData = b
		} else {
			in.Images = b
		}
	}
	// the level's own archive, when the copy has one (its Player graph gives the chasms)
	if b, err := os.ReadFile(filepath.Join(t.o.copyDir, filepath.FromSlash(mapc.LevelSource(level)))); err == nil {
		in.Level = b
	} else if !os.IsNotExist(err) {
		return t.fail(2, "the copy: %v", err)
	}
	m, rep, err := mapc.Import(level, in, o)
	if err != nil {
		return t.fail(1, "import %s: %v", level, err)
	}
	text, err := mapc.FormatMap(m)
	if err != nil {
		return t.fail(1, "import %s: %v", level, err)
	}
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		return t.fail(2, "%v", err)
	}
	if err := os.WriteFile(file, text, 0o644); err != nil {
		return t.fail(2, "%v", err)
	}
	// check what was written, as check --copy does
	m2, code := t.load(pkg)
	if m2 == nil {
		return code
	}
	r, _ := mapc.Check(m2, others(pkg, ""))
	s, err := mapc.ReadShipped(t.o.copyDir)
	if err != nil {
		return t.fail(2, "the copy: %v", err)
	}
	mapc.CheckShipped(m2, s, r)
	if t.o.json {
		t.emit(map[string]any{"ok": r.OK, "file": file, "import": rep, "check": r})
	} else {
		printImport(t.out, file, rep)
		t.printReport(r)
	}
	if !r.OK {
		return 1
	}
	return 0
}

func printImport(w io.Writer, file string, r *mapc.ImportReport) {
	fmt.Fprintf(w, "%s: %s imported, size %g m", file, r.Level, r.Size)
	if r.SizeNote != "" {
		fmt.Fprintf(w, " (%s)", r.SizeNote)
	}
	fmt.Fprintf(w, "; terrain %d shapes, %d corners\n", r.Shapes, r.Vertices)
	for _, d := range r.Mirror {
		note := ""
		if d.Note != "" {
			note = " (" + d.Note + ")"
		}
		if d.Spline {
			fmt.Fprintf(w, "lanes: %s: %d knots, at most %.2f m apart\n", d.What, d.Count, d.Max)
			continue
		}
		if d.Max < 0 {
			fmt.Fprintf(w, "mirror: %s: %d, none of team 2's to compare%s\n", d.What, d.Count, note)
			continue
		}
		fmt.Fprintf(w, "mirror: %s: %d, team 2 at most %.2f m from team 1's mirror%s\n", d.What, d.Count, d.Max, note)
	}
	l := r.LoS
	fmt.Fprintf(w, "line of sight: the level has %d open cells; %d cells differ from their point mirror, %d from their "+
		"reflection across x + y = 0\n", l.LevelOpen, l.PointMirror, l.Reflection)
	fmt.Fprintf(w, "line of sight: the package differs from the level in %d cells (%.2f %% of its open cells): %d in "+
		"team 1's half (the tracing), %d in team 2's (the mirror), %d where the import changed the layout\n",
		l.Differ, 100*l.Share, l.Team1Half, l.Team2Half, l.Changed)
	if c := r.Chasm; c != nil {
		fmt.Fprintf(w, "walkable: the level's Player graph covers %d cells; the package's %d, %d of them the level's "+
			"(%.2f of the union); chasm %d cells in %d parts\n", c.Walkable, c.Package, c.Both, c.Share, c.Cells, c.Parts)
	}
	for _, c := range r.Changes {
		fmt.Fprintf(w, "changed: %s\n", c)
	}
	if d := r.Dress; d != nil {
		for _, s := range d.Taken {
			fmt.Fprintf(w, "dressing: %s: %d of its %d markers placed once as the level has them, %d of them on "+
				"team 2's side (%d without a mirrored twin on team 1's)", s.Set, s.Taken, s.Markers, s.Team2, s.Unmatched)
			if s.Outside > 0 {
				fmt.Fprintf(w, "; %d past the region, left out", s.Outside)
			}
			if len(s.LeftOut) > 0 {
				fmt.Fprintf(w, "; not dressing, left out: %s", strings.Join(s.LeftOut, ", "))
			}
			fmt.Fprintln(w)
		}
		for _, s := range d.Skipped {
			fmt.Fprintf(w, "dressing: %s not taken (%s): %d dressing markers of %d\n", s.Set, s.Reason, s.Dressing,
				s.Markers)
		}
	}
}
