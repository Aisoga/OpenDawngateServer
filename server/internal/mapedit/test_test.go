package mapedit

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/mapdist"
	"dawngate/server/internal/testmaps"
)

// fakeGame is a Game that records its requests, runs the Check at once and says it runs until Stop.
type fakeGame struct {
	mu     sync.Mutex
	reqs   []TestRequest
	report []byte
	check  error
	st     *TestStatus
	forced bool
}

func (g *fakeGame) Test(req TestRequest) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.st != nil && g.st.Running {
		return errors.New("a Test is running; Stop it first")
	}
	g.reqs = append(g.reqs, req)
	g.report, g.check = req.Check()
	g.st = &TestStatus{Name: "fake_" + req.Map, Pkg: req.Pkg, Map: req.Map, Phase: "game", Running: true,
		Started: time.Now(), Lines: []string{"checked", "the game starts"}}
	if g.check != nil {
		g.st.Phase, g.st.Running, g.st.Error = "done", false, "check: "+g.check.Error()
	}
	return nil
}

func (g *fakeGame) Stop(force bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.st == nil || !g.st.Running {
		return ErrNoTest
	}
	g.st.Running, g.st.Stopped, g.st.Phase, g.forced = false, true, "done", force
	return nil
}

func (g *fakeGame) Status() *TestStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.st == nil {
		return nil
	}
	cp := *g.st
	return &cp
}

// TestTestGame: Test hands the saved package to the Game (its folder, the map's name, the page's options as they
// came, a Check that runs mapc check on the saved file), and Stop and the state reach the page.
func TestTestGame(t *testing.T) {
	e := newEnv(t, false)
	g := &fakeGame{}
	c := e.c
	c.Game = g
	e = serve(t, c, e.maps)

	var st struct {
		Test struct {
			Enabled bool
			Reason  string
		}
	}
	if code := e.call("GET", "/api/state", nil, &st); code != 200 || !st.Test.Enabled || st.Test.Reason != "" {
		t.Fatalf("state: %d %+v", code, st)
	}
	var none struct{ Test *TestStatus }
	if e.call("GET", "/api/test", nil, &none); none.Test != nil {
		t.Errorf("before a Test: %+v", none.Test)
	}
	if code := e.call("POST", "/api/test/CM_Ring", `[1, 2]`, nil); code != 400 {
		t.Errorf("options not an object: %d, want 400", code)
	}
	if code := e.call("POST", "/api/test-stop", nil, nil); code != 409 {
		t.Errorf("stop before a Test: %d, want 409", code)
	}
	var started struct {
		OK   bool
		Test TestStatus
	}
	if code := e.call("POST", "/api/test/CM_Ring", `{"seconds": 30, "x": [1]}`, &started); code != 200 || !started.OK ||
		started.Test.Map != "CM_Ring" || !started.Test.Running {
		t.Fatalf("test: %d %+v", code, started)
	}
	if code := e.call("POST", "/api/test/CM_Ring", nil, nil); code != 409 {
		t.Errorf("a second Test: %d, want 409", code)
	}
	r := g.reqs[0]
	if r.Pkg != "CM_Ring" || r.Map != "CM_Ring" || r.Dir != filepath.Join(e.maps, "CM_Ring") ||
		string(r.Options) != `{"seconds": 30, "x": [1]}` || g.check != nil || !json.Valid(g.report) {
		t.Errorf("the request %+v, check %v %s", r, g.check, g.report)
	}
	var ts struct {
		Test TestStatus
		Tail []string
	}
	if e.call("GET", "/api/test", nil, &ts); ts.Test.Phase != "game" || len(ts.Tail) != 2 {
		t.Errorf("status: %+v", ts)
	}
	if code := e.call("POST", "/api/test-stop?force=1", nil, nil); code != 200 || !g.forced {
		t.Errorf("stop: %d forced %v", code, g.forced)
	}
	if e.call("GET", "/api/test", nil, &ts); ts.Test.Running || !ts.Test.Stopped {
		t.Errorf("after Stop: %+v", ts)
	}

	// a saved package that fails a rule: the Check says which, and the Test doesn't start
	p := filepath.Join(e.maps, "CM_Ring", "map.json")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), `"tag": "ring"`, `"tag": "Ring!"`, 1)), 0o644)
	if code := e.call("POST", "/api/test/CM_Ring", nil, nil); code != 200 {
		t.Fatalf("test: %d", code)
	}
	if g.check == nil || !strings.Contains(g.check.Error(), "fails mapc check (exit 1): names.") {
		t.Errorf("the check of a broken package: %v", g.check)
	}
	os.WriteFile(p, []byte(`{"format": 1}`), 0o644)
	if code := e.call("POST", "/api/test/CM_Ring", nil, nil); code != 409 {
		t.Errorf("no map name: %d, want 409", code)
	}
}

// TestPagesLayout: one rule finds the page, its templates and its manifest. A release's editor\ is the page itself
// (templates\ and editor.json inside it); a checkout of the page's repository has web\index.html and keeps
// templates\ and editor.json beside web\.
func TestPagesLayout(t *testing.T) {
	rel := testPages(t, API)
	p, err := LoadPages(rel)
	if err != nil || p.Web != rel || p.Templates != filepath.Join(rel, "templates") || p.Manifest.Version != "0.1.0-test" {
		t.Errorf("a release's editor folder: %+v %v", p, err)
	}
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "web"), 0o755)
	os.WriteFile(filepath.Join(src, "web", "index.html"), []byte("checkout page"), 0o644)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(`{"version": "1.2.3", "api": 1}`), 0o644)
	copyPackage(t, testmaps.Pkg("CM_Marsh"), filepath.Join(src, "templates", "CM_Marsh"))
	p, err = LoadPages(src)
	if err != nil || p.Web != filepath.Join(src, "web") || p.Templates != filepath.Join(src, "templates") ||
		p.Manifest.Version != "1.2.3" {
		t.Errorf("a checkout: %+v %v", p, err)
	}
	// the checkout's web\ given directly (DEVELOPING.md's --editor-pages <checkout>\web): the rest beside it
	p, err = LoadPages(filepath.Join(src, "web"))
	if err != nil || p.Web != filepath.Join(src, "web") || p.Templates != filepath.Join(src, "templates") ||
		p.Manifest.Version != "1.2.3" {
		t.Errorf("a checkout's web: %+v %v", p, err)
	}
	// a release's folder with templates of its own: those, not a parent's
	os.MkdirAll(filepath.Join(src, "web", "templates"), 0o755)
	if p, err = LoadPages(filepath.Join(src, "web")); err != nil || p.Templates != filepath.Join(src, "web", "templates") {
		t.Errorf("templates inside: %+v %v", p, err)
	}
	os.Remove(filepath.Join(src, "web", "templates"))
	e := newEnv(t, false)
	c := e.c
	c.Pages = src
	e = serve(t, c, e.maps)
	res, err := http.Get(e.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(b) != "checkout page" {
		t.Errorf("GET /: %d %q", res.StatusCode, b)
	}
	for _, bad := range []struct{ dir, want string }{
		{"", "no folder"},
		{t.TempDir(), "no index.html"},
		{filepath.Join(src, "templates"), "no index.html"},
	} {
		if _, err := LoadPages(bad.dir); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("LoadPages(%q): %v, want %q", bad.dir, err, bad.want)
		}
	}
	os.Remove(filepath.Join(src, ManifestFile))
	if _, err := LoadPages(src); err == nil || !strings.Contains(err.Error(), "editor.json") {
		t.Errorf("no manifest: %v", err)
	}
}

// TestPageVersion: a page made for another API is refused, naming both versions: PageError for the program that
// serves it (the launcher's console line), a short error page at /, and /api/state's pageError.
func TestPageVersion(t *testing.T) {
	e := newEnv(t, false)
	c := e.c
	c.Pages = testPages(t, API+1)
	s, err := NewServer(c)
	if err != nil {
		t.Fatal(err)
	}
	var ve *VersionError
	if err := s.PageError(); !errors.As(err, &ve) || ve.Page.API != API+1 || ve.Backend != API ||
		!strings.Contains(err.Error(), "version 0.1.0-test") || !strings.Contains(err.Error(), "interface 2") ||
		!strings.Contains(err.Error(), "interface 1") {
		t.Fatalf("PageError: %v", err)
	}
	e = serve(t, c, e.maps)
	res, err := http.Get(e.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(b), "can't start") ||
		!strings.Contains(string(b), "interface 2") || strings.Contains(string(b), "test page") {
		t.Errorf("GET /: %d %s", res.StatusCode, b)
	}
	var st struct{ PageError string }
	if e.call("GET", "/api/state", nil, &st); !strings.Contains(st.PageError, "interface 2") {
		t.Errorf("state's pageError %q", st.PageError)
	}
	c.Pages = ""
	if s, err := NewServer(c); err != nil || s.PageError() == nil {
		t.Errorf("no page folder: %v %v", err, s.PageError())
	}
}

// TestNewFromTemplates: New from template takes the page's templates as well as the maps folder's packages.
func TestNewFromTemplates(t *testing.T) {
	e := newEnv(t, false)
	var res struct {
		OK    bool
		Pkg   string
		Error string
	}
	q := map[string]string{"template": "CM_Grove", "source": "templates", "name": "CM_Mine", "display": "Mine", "tag": "mine"}
	if code := e.call("POST", "/api/new", q, &res); code != 200 || !res.OK {
		t.Fatalf("new from the templates: %d %+v", code, res)
	}
	doc, _, _ := e.open("CM_Mine")
	if doc["name"] != "CM_Mine" || doc["tag"] != "mine" {
		t.Errorf("the copy: %v %v", doc["name"], doc["tag"])
	}
	if r := e.check("CM_Mine", doc); !r.Report.OK {
		t.Errorf("the copy fails: %+v", r.Report.Findings)
	}
	q = map[string]string{"template": "CM_Grove", "name": "CM_Mine2"} // not in the maps folder
	if code := e.call("POST", "/api/new", q, nil); code != 404 {
		t.Errorf("a template from the maps folder that isn't there: %d", code)
	}
	q["source"] = "elsewhere"
	if code := e.call("POST", "/api/new", q, nil); code != 400 {
		t.Errorf("another source: %d", code)
	}
}

// TestDeliveredReadOnly: a package map delivery put into the maps folder (delivered.txt) opens read-only, saying
// where it came from; Save refuses it; Save as my own copy (newMap with it as the template, keepAdded) makes a
// package without the marker that saves, with the pictures added to the delivered one in this run.
func TestDeliveredReadOnly(t *testing.T) {
	e := newEnv(t, false)
	dir := filepath.Join(e.maps, "CM_Ring")
	marker := "This map package was downloaded by Start-Client from the server Test Server (127.0.0.1:42128) on 2026-10-31 12:00 UTC.\nmap CM_Ring\n"
	if err := os.WriteFile(filepath.Join(dir, mapdist.MarkerFile), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Text, Sha256 string
		Delivered    *struct{ Server, Why string }
	}
	if code := e.call("GET", "/api/map/CM_Ring", nil, &m); code != 200 || m.Delivered == nil ||
		m.Delivered.Server != "Test Server (127.0.0.1:42128)" || !strings.Contains(m.Delivered.Why, "Save as my own copy") {
		t.Fatalf("open: %d %+v", code, m.Delivered)
	}
	var st struct{ Maps []mapInfo }
	if e.call("GET", "/api/state", nil, &st); len(st.Maps) != 1 || !st.Maps[0].Delivered {
		t.Errorf("state: %+v", st.Maps)
	}
	orig, _ := os.ReadFile(filepath.Join(dir, "map.json"))
	var doc map[string]any
	json.Unmarshal([]byte(m.Text), &doc)
	doc["display"] = "Edited"
	var sv struct {
		OK        bool
		Delivered bool
		Error     string
	}
	if code := e.call("POST", "/api/save/CM_Ring?force=1", doc, &sv); code != http.StatusForbidden || sv.OK ||
		!sv.Delivered || !strings.Contains(sv.Error, "map delivery") {
		t.Errorf("save: %d %+v", code, sv)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "map.json")); string(b) != string(orig) {
		t.Error("the delivered package was written")
	}
	// a picture added to it in this run goes with Save as my own copy
	var a struct{ File string }
	if e.call("POST", "/api/asset/CM_Ring?name=ground/moss.png", grey(t, 32, 100), &a); a.File != "ground/moss.png" {
		t.Fatalf("added %q", a.File)
	}
	q := map[string]any{"template": "CM_Ring", "name": "CM_MyRing", "display": "My Ring", "tag": "myring", "keepAdded": true}
	if code := e.call("POST", "/api/new", q, nil); code != 200 {
		t.Fatalf("save as my own copy: %d", code)
	}
	if _, err := os.Stat(filepath.Join(e.maps, "CM_MyRing", mapdist.MarkerFile)); err == nil {
		t.Error("the copy has the marker")
	}
	cdoc, _, sha := e.open("CM_MyRing")
	for _, k := range []string{"name", "display", "tag", "prefix"} {
		if v, ok := cdoc[k]; ok {
			doc[k] = v
		}
	}
	doc["display"] = "Edited"
	gr, _ := doc["ground"].(map[string]any)
	if gr == nil {
		gr = map[string]any{}
		doc["ground"] = gr
	}
	gr["picture"] = []any{"ground/moss.png"}
	if code := e.call("POST", "/api/save/CM_MyRing?base="+sha, doc, &sv); code != 200 || !sv.OK {
		t.Fatalf("save the copy: %d %+v", code, sv)
	}
	if _, err := os.Stat(filepath.Join(e.maps, "CM_MyRing", "ground", "moss.png")); err != nil {
		t.Errorf("the added picture didn't go with the copy: %v", err)
	}
}
