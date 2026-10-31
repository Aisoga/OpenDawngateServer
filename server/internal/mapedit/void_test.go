package mapedit

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// voidPick is the section the void panel writes when it is switched on (web/void.js PICK: the user's MM look).
func voidPick() map[string]any {
	return map[string]any{
		"cut":      1.5,
		"backdrop": map[string]any{"z": -50.0, "texture": map[string]any{"rgb": []any{0.0, 0.0, 0.0}}},
		"mist":     map[string]any{"z": -0.2, "range": 10.0, "color": []any{0.467, 0.824, 0.859}},
	}
}

func hasFinding(r checkReply, rule, path string) bool {
	for _, f := range r.Report.Findings {
		if f.Rule == rule && f.Path == path {
			return true
		}
	}
	return false
}

// TestVoidEdit: CM_Ring (no void) gets the void panel's section, edited as the panel does: the pick passes; a mist
// above the floor, a cut without its backdrop and a fog whose end lies above its start fail at the paths the panel
// shows them at; an unlit backdrop under the mist warns. The edited void (a height fog with its colour added) saves,
// reads back the same, passes mapc check, builds as mapc build builds the saved package (and not as without the void),
// and the 3-D view's ground (mapc ground of the draft) holds the backdrop and the mist plane.
func TestVoidEdit(t *testing.T) {
	e := newEnv(t, true)
	doc, text, sha := e.open("CM_Ring")
	if _, ok := doc["void"]; ok {
		t.Fatal("CM_Ring has a void already")
	}
	doc["void"] = voidPick()
	r := e.check("CM_Ring", doc)
	if !r.Report.OK {
		t.Fatalf("the pick fails: %+v", r.Report.Findings)
	}
	mist := func() map[string]any { return doc["void"].(map[string]any)["mist"].(map[string]any) }
	broken := []struct {
		name, rule, path string
		edit, undo       func()
		warning          bool
	}{
		{"mist above the floor", "void.mist-floor", "void.mist.z",
			func() { mist()["z"] = 0.1 }, func() { mist()["z"] = -0.2 }, false},
		{"a cut without a backdrop", "void.backdrop", "void",
			func() { delete(doc["void"].(map[string]any), "backdrop") },
			func() { doc["void"].(map[string]any)["backdrop"] = voidPick()["backdrop"] }, false},
		{"the fog's end above its start", "void.fog", "void.fog",
			func() { doc["void"].(map[string]any)["fog"] = map[string]any{"start": 10.0, "end": 20.0} },
			func() { delete(doc["void"].(map[string]any), "fog") }, false},
		{"an unlit backdrop under the mist", "void.mist-unlit", "void.mist",
			func() { doc["void"].(map[string]any)["backdrop"].(map[string]any)["unlit"] = true },
			func() { delete(doc["void"].(map[string]any)["backdrop"].(map[string]any), "unlit") }, true},
	}
	for _, b := range broken {
		b.edit()
		r := e.check("CM_Ring", doc)
		if r.Report.OK == !b.warning || !hasFinding(r, b.rule, b.path) {
			t.Errorf("%s: ok %v, %+v", b.name, r.Report.OK, r.Report.Findings)
		}
		b.undo()
	}
	// the edit the panel makes: the height fog switched on with its own colour, the mist's range changed
	doc["void"].(map[string]any)["fog"] = map[string]any{"start": 5.0, "end": -40.0, "color": []any{0.302, 0.341, 0.451}}
	mist()["range"] = 25.0
	if r := e.check("CM_Ring", doc); !r.Report.OK {
		t.Fatalf("the edited void fails: %+v", r.Report.Findings)
	}
	var s struct {
		OK, Written bool
		Text        string
	}
	if code := e.call("POST", "/api/save/CM_Ring?base="+sha, doc, &s); code != 200 || !s.Written {
		t.Fatalf("save: %d %+v", code, s)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(s.Text), &back); err != nil || !reflect.DeepEqual(back["void"], doc["void"]) {
		t.Fatalf("the saved void: %v %v", err, back["void"])
	}
	if n := changedLines(text, s.Text); n > 8 {
		t.Errorf("%d lines changed:\n%s", n, s.Text)
	}
	t.Logf("the saved void:\n%s", s.Text[strings.Index(s.Text, `"void"`)-2:])
	saved := filepath.Join(e.maps, "CM_Ring")
	if out, err := mapcCmd("check", saved).CombinedOutput(); err != nil {
		t.Errorf("mapc check of the saved package: %v %s", err, out)
	}
	if e.c.Copy == "" {
		return
	}
	var b struct {
		Exit   int
		Result struct {
			OK     bool
			Sha256 string
		}
	}
	if code := e.call("POST", "/api/build/CM_Ring", nil, &b); code != 200 || b.Exit != 0 || !b.Result.OK {
		t.Fatalf("build: %d %+v", code, b)
	}
	direct := func(pkg string) string {
		out, err := mapcCmd("build", "--json", pkg, e.c.Copy, filepath.Join(t.TempDir(), "x.snap")).Output()
		var d struct{ Sha256 string }
		if err != nil || json.Unmarshal(out, &d) != nil {
			t.Fatalf("mapc build %s: %v %s", pkg, err, out)
		}
		return d.Sha256
	}
	if with, without := direct(saved), direct(ringDir); with != b.Result.Sha256 || without == with {
		t.Errorf("the editor's build %s, mapc build of the saved package %s, of CM_Ring without the void %s", b.Result.Sha256, with, without)
	}
	// the 3-D view's ground: the cut ground, the backdrop and the mist plane (on the fog face's material)
	var g struct {
		OK        bool   `json:"ok"`
		URL       string `json:"url"`
		Triangles int    `json:"triangles"`
	}
	if e.call("POST", "/api/ground/CM_Ring", nil, &g); !g.OK || g.Triangles >= 204800 {
		t.Fatalf("ground: %+v", g)
	}
	gltf := string(getBody(t, e, g.URL))
	for _, want := range []string{"labsFogFace", "_backdrop"} {
		if !strings.Contains(strings.ToLower(gltf), strings.ToLower(want)) {
			t.Errorf("the ground's glTF names no %s", want)
		}
	}
	t.Logf("the ground with the void: %d triangles", g.Triangles)
}

// getBody is the body of a GET of the editor's path (200 only).
func getBody(t *testing.T, e *env, path string) []byte {
	t.Helper()
	r, err := http.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 {
		t.Fatalf("%s: %d", path, r.StatusCode)
	}
	return b
}

// TestPassVoidShown: CM_Pass opens with its void (the pick: what the panel shows) and passes the check with the copy.
func TestPassVoidShown(t *testing.T) {
	e := newEnvWith(t, true, filepath.Join(testMaps, "CM_Pass"))
	doc, _, _ := e.open("CM_Pass")
	if !reflect.DeepEqual(doc["void"], voidPick()) {
		t.Errorf("CM_Pass's void %v, the panel's pick %v", doc["void"], voidPick())
	}
	r := e.check("CM_Pass", doc)
	if !r.Report.OK {
		t.Errorf("CM_Pass: %+v", r.Report.Findings)
	}
	for _, f := range r.Report.Findings { // its two props.group warnings (members that aren't dressing) are the import's
		if strings.HasPrefix(f.Rule, "void.") || f.Rule == "props.flush" {
			t.Errorf("CM_Pass: %+v", f)
		}
	}
}
