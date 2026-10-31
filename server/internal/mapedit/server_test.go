package mapedit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/mapctool"
	"dawngate/server/internal/mapedit/textjson"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

// The tests run the real compiler, as a release does: the test binary is its own mapc (TestMain: `<test binary>
// --mapc <args>` runs mapctool, as `Start-Client.exe --mapc` does), and the servers' Config names it with
// MapcArgs ["--mapc"].
var mapcExe string

// mapcArgs is the Config's MapcArgs for mapcExe.
var mapcArgs = []string{"--mapc"}

// testMaps is the test maps' fixture folder (server/testdata/maps): the repository's maps/ holds no package in git.
var testMaps = testmaps.Dir()

var ringDir = testmaps.Pkg("CM_Ring")

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--mapc" {
		os.Exit(mapctool.Run(os.Args[2:], os.Stdout, os.Stderr))
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "no test binary (%v): the server tests skip\n", err)
	}
	mapcExe = exe
	os.Exit(m.Run())
}

// mapcCmd is mapc with args, as the editor runs it.
func mapcCmd(args ...string) *exec.Cmd {
	return exec.Command(mapcExe, append(append([]string(nil), mapcArgs...), args...)...)
}

// gameCopy is the game files to build from, or "" (DAWNGATE_GAME, else the checkout's pristine extract: the
// setup's testgame rule). They are only read.
func gameCopy() string { return testgame.Find() }

// testPages makes a page folder (a release's editor\ layout: index.html, editor.json with api, templates\ with
// CM_Grove) and returns it.
func testPages(t *testing.T, api int) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>Map editor</title>test page\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ManifestFile), []byte(fmt.Sprintf(`{"version": "0.1.0-test", "api": %d}`, api)), 0o644)
	copyPackage(t, testmaps.Pkg("CM_Grove"), filepath.Join(dir, "templates", "CM_Grove"))
	return dir
}

// copyPackage copies a package's files (map.json and the rest; no sub-folders) into dst.
func copyPackage(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type env struct {
	t    *testing.T
	srv  *httptest.Server
	maps string
	c    Config
}

// newEnv serves a copy of CM_Ring's package from a scratch maps folder.
func newEnv(t *testing.T, withCopy bool) *env { return newEnvWith(t, withCopy, ringDir) }

// newEnvWith serves a copy of the package pkgDir (its map.json) from a scratch maps folder.
func newEnvWith(t *testing.T, withCopy bool, pkgDir string) *env {
	t.Helper()
	if mapcExe == "" {
		t.Skip("no mapc")
	}
	maps := t.TempDir()
	copyPackage(t, pkgDir, filepath.Join(maps, filepath.Base(pkgDir)))
	c := Config{Maps: maps, Mapc: mapcExe, MapcArgs: mapcArgs, Out: filepath.Join(t.TempDir(), "out"),
		Work: t.TempDir(), Cache: filepath.Join(t.TempDir(), "3d"), Pages: testPages(t, API)}
	if withCopy {
		c.Copy = gameCopy()
	}
	return serve(t, c, maps)
}

// serve starts a Server of c on a test listener.
func serve(t *testing.T, c Config, maps string) *env {
	t.Helper()
	s, err := NewServer(c)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, maps: maps, c: c}
}

func (e *env) call(method, path string, body any, out any) int {
	e.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		r = bytes.NewReader(b)
	case string:
		r = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		r = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
	return res.StatusCode
}

type finding struct {
	Rule, Severity, Path, Message string
	At                            []float64
}

type checkReply struct {
	Rev    int
	Exit   int
	Text   string
	Images string
	Error  string
	Report struct {
		OK       bool
		Findings []finding
	}
	Preview *struct {
		Files   []struct{ File string }
		Objects []struct {
			Kind, Name string
			Team, Lane int
			Pos        []float64
		}
	}
	CopyChecked bool
	// Placed: the props' markers (with the copy and props only)
	Placed []struct {
		Path string
		Team int
		Pos  []float64
	}
}

func (e *env) open(pkg string) (map[string]any, string, string) {
	e.t.Helper()
	var m struct{ Text, Sha256 string }
	if code := e.call("GET", "/api/map/"+pkg, nil, &m); code != 200 {
		e.t.Fatalf("open %s: %d", pkg, code)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(m.Text), &doc); err != nil {
		e.t.Fatal(err)
	}
	return doc, m.Text, m.Sha256
}

func (e *env) check(pkg string, doc any) checkReply {
	e.t.Helper()
	var r checkReply
	if code := e.call("POST", "/api/check/"+pkg, doc, &r); code != 200 {
		e.t.Fatalf("check: %d %s", code, r.Error)
	}
	return r
}

func TestOpenCheckSaveUnchanged(t *testing.T) {
	e := newEnv(t, false)
	doc, text, sha := e.open("CM_Ring")
	r := e.check("CM_Ring", doc)
	if !r.Report.OK || r.Exit != 0 || r.Preview == nil || len(r.Preview.Files) != 9 || r.Text != text {
		t.Fatalf("check of the unchanged map: %+v", r.Report)
	}
	res, err := http.Get(e.srv.URL + r.Images + "layout.png")
	if err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("the layout picture: %v %v", err, res)
	}
	res.Body.Close()
	// open and save unchanged: the same bytes, the file isn't even written
	var s struct {
		OK, Written bool
		Sha256      string
	}
	if code := e.call("POST", "/api/save/CM_Ring?base="+sha, doc, &s); code != 200 || !s.OK || s.Written || s.Sha256 != sha {
		t.Fatalf("save unchanged: %d %+v", code, s)
	}
	b, _ := os.ReadFile(filepath.Join(e.maps, "CM_Ring", "map.json"))
	orig, _ := os.ReadFile(filepath.Join(ringDir, "map.json"))
	if !bytes.Equal(b, orig) {
		t.Error("the file changed")
	}
}

// edited sets the parsed map's members as the page does: an "auto" Binding made explicit and moved, a bush added,
// a wall added and moved.
func edited(t *testing.T, doc map[string]any, r checkReply) {
	base := doc["base"].(map[string]any)
	bind := map[string]any{}
	for _, o := range r.Preview.Objects {
		if o.Kind != "binding" || o.Team != 1 {
			continue
		}
		key := fmt.Sprint(o.Lane)
		if bind[key] == nil {
			bind[key] = []any{nil, nil, nil}
		}
		tier := map[string]int{"Outer": 0, "Middle": 1, "Inner": 2}[o.Name[strings.LastIndex(o.Name, " ")+1:]]
		p := []any{round(o.Pos[0]), round(o.Pos[1])}
		if key == "1" && tier == 0 {
			p = []any{round(o.Pos[0] + 2), round(o.Pos[1] - 3)}
		}
		bind[key].([]any)[tier] = p
	}
	base["bindings"] = bind
	ter := doc["terrain"].(map[string]any)
	ter["shapes"] = append(ter["shapes"].([]any),
		map[string]any{"type": "bush", "circle": []any{-165, -70}, "r": 4},
		map[string]any{"type": "wall", "circle": []any{-128, -118}, "r": 3})
}

func round(v float64) float64 { return float64(int64(v*10+0.5*sign(v))) / 10 }

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

func TestEditAndSave(t *testing.T) {
	e := newEnv(t, false)
	doc, text, sha := e.open("CM_Ring")
	r := e.check("CM_Ring", doc)
	edited(t, doc, r)
	r = e.check("CM_Ring", doc)
	if !r.Report.OK {
		t.Fatalf("the edited map fails: %+v", r.Report.Findings)
	}
	var s struct {
		OK, Written bool
		Sha256      string
		Text        string
	}
	if code := e.call("POST", "/api/save/CM_Ring?base="+sha, doc, &s); code != 200 || !s.Written {
		t.Fatalf("save: %d %+v", code, s)
	}
	saved, _ := os.ReadFile(filepath.Join(e.maps, "CM_Ring", "map.json"))
	if string(saved) != s.Text {
		t.Error("the reply's text isn't the file's")
	}
	// only the edited lines differ: the lanes, spawns and the old shapes keep their bytes
	for _, keep := range []string{
		`{"id": 1, "width": 24, "knots": [[-170, -145], [-170, -100], [-174, -30], [-174, 30], [-165, 115],`,
		`"spawns": [[-188.9, -183.9], [-183.3, -189.8], [-178.7, -186.6], [-179.7, -180.2], [-186.3, -178.7]],`,
		`{"type": "bush", "polygon": [[60, -182], [80, -182], [80, -174], [60, -174]]},`,
		`"circle": [-165, -70], "r": 4`,
		`"cores": "auto",`,
	} {
		if !strings.Contains(string(saved), keep) {
			t.Errorf("no %s in\n%s", keep, saved)
		}
	}
	if diff := changedLines(text, string(saved)); diff > 7 { // the bindings (4 lines), the two shapes and the comma after the old last one
		t.Errorf("%d lines changed:\n%s", diff, saved)
	}
	// the saved package passes mapc itself
	out, err := mapcCmd("check", filepath.Join(e.maps, "CM_Ring")).CombinedOutput()
	if err != nil {
		t.Errorf("mapc check of the saved package: %v %s", err, out)
	}
	// a stale base is refused, force writes
	if code := e.call("POST", "/api/save/CM_Ring?base="+sha, doc, nil); code != http.StatusConflict {
		t.Errorf("a stale save: %d", code)
	}
	if code := e.call("POST", "/api/save/CM_Ring?force=1", doc, nil); code != 200 {
		t.Errorf("a forced save: %d", code)
	}
}

// changedLines counts the lines of b that aren't lines of a.
func changedLines(a, b string) int {
	have := map[string]int{}
	for _, l := range strings.Split(a, "\n") {
		have[l]++
	}
	n := 0
	for _, l := range strings.Split(b, "\n") {
		if have[l] > 0 {
			have[l]--
		} else {
			n++
		}
	}
	return n
}

func TestBrokenEdit(t *testing.T) {
	e := newEnv(t, false)
	doc, _, _ := e.open("CM_Ring")
	doc["base"].(map[string]any)["spawns"].([]any)[0] = []any{-100, -20} // into the walled middle
	r := e.check("CM_Ring", doc)
	found := false
	for _, f := range r.Report.Findings {
		found = found || (f.Rule == "fixed.open" && f.Path == "base.spawns[0]" && len(f.At) == 2 && f.At[0] == -100)
	}
	if r.Report.OK || r.Exit != 0 || !found {
		t.Errorf("no fixed.open on base.spawns[0]: %+v", r.Report)
	}
	// a structural failure: the report, no layers
	doc["lanes"] = []any{}
	r = e.check("CM_Ring", doc)
	if r.Report.OK || r.Preview != nil || r.Exit != 1 || len(r.Report.Findings) == 0 {
		t.Errorf("structural: %+v", r)
	}
	// an unknown member: mapc's format.json, the member kept in the draft
	doc, _, _ = e.open("CM_Ring")
	doc["later"] = map[string]any{"x": 1}
	r = e.check("CM_Ring", doc)
	if r.Report.OK || len(r.Report.Findings) != 1 || r.Report.Findings[0].Rule != "format.json" ||
		!strings.Contains(r.Text, `"later": {"x": 1}`) {
		t.Errorf("unknown member: %+v", r)
	}
	// not JSON
	if code := e.call("POST", "/api/check/CM_Ring", "{", nil); code != 400 {
		t.Errorf("bad JSON: %d", code)
	}
}

func TestNewFromTemplate(t *testing.T) {
	e := newEnv(t, false)
	var res struct {
		OK    bool
		Pkg   string
		Error string
	}
	q := map[string]string{"template": "CM_Ring", "name": "CM_Copy", "display": "Test Copy", "tag": "copy", "prefix": "[Copy] "}
	// a picture of the template's own ground goes with it (MAPED-EDIT5)
	pic := filepath.Join(e.maps, "CM_Ring", "ground", "sub", "t.png")
	if err := os.MkdirAll(filepath.Dir(pic), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pic, []byte("\x89PNG test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := e.call("POST", "/api/new", q, &res); code != 200 || !res.OK || res.Pkg != "CM_Copy" {
		t.Fatalf("new: %d %+v", code, res)
	}
	if b, err := os.ReadFile(filepath.Join(e.maps, "CM_Copy", "ground", "sub", "t.png")); err != nil || string(b) != "\x89PNG test" {
		t.Errorf("the template's picture: %q %v", b, err)
	}
	b, _ := os.ReadFile(filepath.Join(e.maps, "CM_Copy", "map.json"))
	orig, _ := os.ReadFile(filepath.Join(ringDir, "map.json"))
	want := strings.NewReplacer(`"CM_Ring"`, `"CM_Copy"`, `"Test Ring"`, `"Test Copy"`, `"ring"`, `"copy"`,
		`"[Ring] "`, `"[Copy] "`).Replace(string(orig))
	if string(b) != want {
		t.Errorf("the new map.json:\n%s", b)
	}
	doc, _, _ := e.open("CM_Copy")
	if r := e.check("CM_Copy", doc); !r.Report.OK {
		t.Errorf("the copy fails: %+v", r.Report.Findings)
	}
	// the same tag as the template: the check names it (the other installed maps are the maps folder's)
	doc["tag"] = "ring"
	r := e.check("CM_Copy", doc)
	found := false
	for _, f := range r.Report.Findings {
		found = found || f.Rule == "names.tag-unique"
	}
	if !found {
		t.Errorf("no names.tag-unique: %+v", r.Report.Findings)
	}
	if code := e.call("POST", "/api/new", q, nil); code != http.StatusConflict {
		t.Errorf("a second CM_Copy: %d", code)
	}
	// MAPED-EDIT6: a picture added, the package deleted outside the editor and made again: the same name, no _2
	var a struct{ File string }
	if e.call("POST", "/api/asset/CM_Copy?name=ground/moss.png", grey(t, 32, 100), &a); a.File != "ground/moss.png" {
		t.Fatalf("added %q", a.File)
	}
	if err := os.RemoveAll(filepath.Join(e.maps, "CM_Copy")); err != nil {
		t.Fatal(err)
	}
	if code := e.call("POST", "/api/new", q, nil); code != 200 {
		t.Fatalf("made again: %d", code)
	}
	if e.call("POST", "/api/asset/CM_Copy?name=ground/moss.png", grey(t, 32, 160), &a); a.File != "ground/moss.png" {
		t.Errorf("a new picture after the package was made again: %q (the old store kept)", a.File)
	}
	q["name"] = "../x"
	if code := e.call("POST", "/api/new", q, nil); code != 400 {
		t.Errorf("a bad name: %d", code)
	}
}

// TestGuards: ServeHTTP lets only this machine's pages in. The Host must be a loopback name or address (DNS
// rebinding: a foreign name resolving to 127.0.0.1 is refused), and a request that isn't a GET with an Origin must
// come from this server's own (CSRF: another site's page posting to the editor); then the routes' own refusals.
func TestGuards(t *testing.T) {
	e := newEnv(t, false)
	self := strings.TrimPrefix(e.srv.URL, "http://")
	for _, c := range []struct {
		host, origin, method, path string
		want                       int
	}{
		{"evil.example:80", "", "GET", "/api/state", 403},
		{"evil.example", "", "GET", "/", 403},
		{"192.168.1.10:17890", "", "GET", "/api/state", 403},
		{"127.0.0.1.evil.example", "", "GET", "/api/state", 403},
		{"localhost:17890", "", "GET", "/api/state", 200},
		{"127.0.0.4:17893", "", "GET", "/api/state", 200}, // a test lane's loopback address
		{"[::1]:17890", "", "GET", "/api/state", 200},
		{"", "http://evil.example", "POST", "/api/test/CM_Ring", 403},
		{"", "http://evil.example", "POST", "/api/save/CM_Ring", 403},
		{"", "http://evil.example", "POST", "/api/new", 403},
		{"", "null", "POST", "/api/save/CM_Ring", 403},        // a sandboxed or file:// page
		{"", "http://evil.example", "GET", "/api/state", 200}, // a GET changes nothing
		{"", "http://" + self, "POST", "/api/test-stop", 409}, // its own page: past the guard
		{"", "", "GET", "/api/map/CM_Nope", 404},
		{"", "", "GET", "/api/map/a.b", 400},
		{"", "", "GET", "/preview/CM_Ring/1/..%2fmap.json", 404},
		{"", "", "POST", "/api/test/CM_Ring", 409},  // no Game
		{"", "", "POST", "/api/test-stop", 409},     // no Test running
		{"", "", "POST", "/api/build/CM_Ring", 409}, // no copy configured
		{"localhost", "", "GET", "/", 200},
	} {
		req, _ := http.NewRequest(c.method, e.srv.URL+c.path, nil)
		if c.host != "" {
			req.Host = c.host
		}
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != c.want {
			t.Errorf("%s %s (host %q origin %q): %d, %d expected", c.method, c.path, c.host, c.origin, res.StatusCode, c.want)
		}
	}
	var st struct {
		API       int
		Release   string
		Maps      []mapInfo
		Templates []mapInfo
		Page      Manifest
		Test      struct {
			Enabled bool
			Reason  string
		}
	}
	e.call("GET", "/api/state", nil, &st)
	if len(st.Maps) != 1 || st.Maps[0].Name != "CM_Ring" || !st.Maps[0].Mesh || st.Test.Enabled || st.Test.Reason != noGame ||
		st.API != API || st.Release == "" || st.Page.Version != "0.1.0-test" || len(st.Templates) != 1 ||
		st.Templates[0].Pkg != "CM_Grove" {
		t.Errorf("state: %+v", st)
	}
}

// TestBuild builds the opened-and-saved package through the editor and mapc directly: the same archive.
func TestBuild(t *testing.T) {
	e := newEnv(t, true)
	if e.c.Copy == "" {
		t.Skip("no game files (set DAWNGATE_GAME)")
	}
	doc, _, sha := e.open("CM_Ring")
	e.call("POST", "/api/save/CM_Ring?base="+sha, doc, nil)
	r := e.check("CM_Ring", doc)
	if !r.CopyChecked || !r.Report.OK {
		t.Errorf("the check with the copy: %+v", r)
	}
	var b struct {
		Exit   int
		Result struct {
			OK      bool
			Archive string
			Sha256  string
			Error   string
		}
	}
	if code := e.call("POST", "/api/build/CM_Ring", nil, &b); code != 200 || b.Exit != 0 || !b.Result.OK {
		t.Fatalf("build: %d %+v", code, b)
	}
	direct := filepath.Join(t.TempDir(), "direct.snap")
	out, err := mapcCmd("build", "--json", ringDir, e.c.Copy, direct).Output()
	var d struct{ Sha256 string }
	if err != nil || json.Unmarshal(out, &d) != nil || d.Sha256 != b.Result.Sha256 {
		t.Errorf("mapc build directly: %v %s, the editor's %s", err, out, b.Result.Sha256)
	}
	// an edited layout builds as it is (every map gets the shared PhysX floor), and without the mesh
	edited(t, doc, r)
	var s struct{ Sha256 string }
	e.call("POST", "/api/save/CM_Ring?force=1", doc, &s)
	if e.call("POST", "/api/build/CM_Ring", nil, &b); b.Exit != 0 || !b.Result.OK {
		t.Errorf("an edited layout: %+v", b)
	}
	if e.call("POST", "/api/build/CM_Ring?mesh=0", nil, &b); b.Exit != 0 || !b.Result.OK {
		t.Errorf("no mesh: %+v", b)
	}
}

// TestMergeKeepsRing is the round trip through the page's serialisation (JSON.stringify keeps the members' order,
// Go's Marshal sorts them; the layout must survive both).
func TestMergeKeepsRing(t *testing.T) {
	orig, err := os.ReadFile(filepath.Join(ringDir, "map.json"))
	if err != nil {
		t.Skip(err)
	}
	var doc map[string]any
	json.Unmarshal(orig, &doc)
	b, _ := json.Marshal(doc)
	out, err := textjson.Merge(orig, b)
	if err != nil || !bytes.Equal(out, orig) {
		t.Errorf("%v:\n%s", err, out)
	}
}

// TestImportedPackage: the package mapc import wrote from MM_Map_v3 (maps/CM_Pass, MAPED-IMPORT) opens, checks, saves
// unchanged without a write, takes an edit (the Inner Binding moved, a bush and a wall pillar added, the rocks' group
// of the whole level standing once (a polygon region, "mirror": false, mapc import --dress) moved by 2 m) that still
// passes, and builds as it is and edited; with the copy the check places the level's 640 dressing markers once each
// (both halves as MM_Map_v3 has them) and the two statues added by hand on both sides, the moved group's 2 m off,
// still once. The package's void section is kept through the save.
func TestImportedPackage(t *testing.T) {
	const pkg = "CM_Pass"
	e := newEnvWith(t, true, filepath.Join(testMaps, pkg))
	doc, text, sha := e.open(pkg)
	r := e.check(pkg, doc)
	if !r.Report.OK || r.Exit != 0 || r.Preview == nil || r.Text != text {
		t.Fatalf("check of the unchanged map: %+v", r.Report)
	}
	rockAt := func(r checkReply) []float64 { // the rocks group's first marker
		for _, p := range r.Placed {
			if p.Path == "props[3]" && p.Team == 0 {
				return p.Pos
			}
		}
		return nil
	}
	once := func(r checkReply) bool { // the dressing once (team 0); the two statues added by hand mirrored
		teams := map[int]int{}
		for _, p := range r.Placed {
			teams[p.Team]++
		}
		return teams[0] == 640 && teams[1] == 2 && teams[2] == 2 && len(teams) == 3
	}
	rock0 := rockAt(r)
	if e.c.Copy != "" && (len(r.Placed) != 644 || rock0 == nil || !once(r)) {
		t.Errorf("the dressing placed %d markers", len(r.Placed))
	}
	var s struct {
		OK, Written bool
		Sha256      string
	}
	if code := e.call("POST", "/api/save/"+pkg+"?base="+sha, doc, &s); code != 200 || !s.OK || s.Written || s.Sha256 != sha {
		t.Fatalf("save unchanged: %d %+v", code, s)
	}
	build := func(what string) {
		if e.c.Copy == "" {
			return
		}
		var b struct {
			Exit   int
			Result struct{ OK bool }
		}
		if code := e.call("POST", "/api/build/"+pkg, nil, &b); code != 200 || b.Exit != 0 || !b.Result.OK {
			t.Errorf("build %s: %d %+v", what, code, b)
		}
	}
	build("unchanged")
	base := doc["base"].(map[string]any)
	lane := base["bindings"].(map[string]any)["1"].([]any)
	inner := lane[1].([]any)
	lane[1] = []any{round(inner[0].(float64) + 2), round(inner[1].(float64) - 2)}
	ter := doc["terrain"].(map[string]any)
	ter["shapes"] = append(ter["shapes"].([]any),
		map[string]any{"type": "bush", "circle": []any{-100, -100}, "r": 4},
		map[string]any{"type": "wall", "circle": []any{-120, -170}, "r": 3})
	rocks := doc["props"].([]any)[3].(map[string]any)
	if g := rocks["group"].(map[string]any); g["sets"].([]any)[0] != "rocks" || len(g["polygon"].([]any)) != 4 ||
		rocks["mirror"] != false {
		t.Fatalf("props[3]: %v", rocks)
	}
	pos := rocks["pos"].([]any)
	rocks["pos"] = []any{round(pos[0].(float64) - 2), round(pos[1].(float64) - 2), pos[2]}
	r = e.check(pkg, doc)
	if !r.Report.OK {
		t.Fatalf("the edited map fails: %+v", r.Report.Findings)
	}
	if rock1 := rockAt(r); e.c.Copy != "" && (len(r.Placed) != 644 || rock1 == nil || !once(r) ||
		math.Abs(rock1[0]-rock0[0]+2) > 0.01 || math.Abs(rock1[1]-rock0[1]+2) > 0.01) {
		t.Errorf("the moved group: %d markers, the first rock %v, was %v", len(r.Placed), rock1, rock0)
	}
	var s2 struct {
		OK, Written bool
		Text        string
	}
	if code := e.call("POST", "/api/save/"+pkg+"?base="+sha, doc, &s2); code != 200 || !s2.Written {
		t.Fatalf("save: %d %+v", code, s2)
	}
	if diff := changedLines(text, s2.Text); diff > 6 {
		t.Errorf("%d lines changed:\n%s", diff, s2.Text)
	}
	if !strings.Contains(s2.Text, `"mist": {"z": -0.2, "range": 10, "color": [0.467, 0.824, 0.859]}`) {
		t.Errorf("the void didn't survive the save:\n%s", s2.Text)
	}
	if out, err := mapcCmd("check", filepath.Join(e.maps, pkg)).CombinedOutput(); err != nil {
		t.Errorf("mapc check of the saved package: %v %s", err, out)
	}
	build("edited")
}

// TestImportedChasms: the package mapc import wrote from SM_Map_v5 with its chasms (maps/CM_Stand, MAPED-IMPORT-SM)
// opens, checks (its one warning: the pockets the graphs' squares can't reach), with the copy places SM_Map_v5's
// own dressing once each (7,021 markers, both halves as the level has them, mapc import --dress), saves unchanged
// without a write and builds.
func TestImportedChasms(t *testing.T) {
	const pkg = "CM_Stand"
	e := newEnvWith(t, true, filepath.Join(testMaps, pkg))
	doc, text, sha := e.open(pkg)
	chasms := 0
	for _, s := range doc["terrain"].(map[string]any)["shapes"].([]any) {
		if s.(map[string]any)["type"] == "chasm" {
			chasms++
		}
	}
	if chasms == 0 {
		t.Errorf("%s has no chasm", pkg)
	}
	r := e.check(pkg, doc)
	if !r.Report.OK || r.Exit != 0 || r.Preview == nil || r.Text != text {
		t.Fatalf("check of the unchanged map: exit %d %+v", r.Exit, r.Report)
	}
	if e.c.Copy != "" && len(r.Placed) != 7021 {
		t.Errorf("the dressing placed %d markers", len(r.Placed))
	}
	var s struct {
		OK, Written bool
		Sha256      string
	}
	if code := e.call("POST", "/api/save/"+pkg+"?base="+sha, doc, &s); code != 200 || !s.OK || s.Written || s.Sha256 != sha {
		t.Fatalf("save unchanged: %d %+v", code, s)
	}
	if e.c.Copy != "" {
		var b struct {
			Exit   int
			Result struct{ OK bool }
		}
		if code := e.call("POST", "/api/build/"+pkg, nil, &b); code != 200 || b.Exit != 0 || !b.Result.OK {
			t.Errorf("build: %d %+v", code, b)
		}
	}
}
