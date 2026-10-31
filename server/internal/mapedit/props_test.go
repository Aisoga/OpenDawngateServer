package mapedit

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPropsOff(t *testing.T) {
	e := newEnv(t, false)
	var res map[string]any
	if code := e.call("GET", "/api/nouns", nil, &res); code != http.StatusConflict ||
		!strings.Contains(res["error"].(string), "no game copy") {
		t.Errorf("nouns without a copy: %d %v", code, res)
	}
	if code := e.call("POST", "/api/models", map[string]any{"nouns": []string{"x"}}, &res); code != http.StatusConflict {
		t.Errorf("models without a copy: %d %v", code, res)
	}
	// a check of a map with props but no copy: nothing placed (the page draws map.json's own entries)
	doc := map[string]any{}
	json.Unmarshal([]byte(e.text(t)), &doc)
	doc["props"] = []any{map[string]any{"noun": "lichenrock_02", "pos": []any{-130.0, -120.0}, "rot": 0.0, "scale": 1.0}}
	var chk map[string]json.RawMessage
	if code := e.call("POST", "/api/check/CM_Ring", doc, &chk); code != 200 || chk["placed"] != nil {
		t.Errorf("check without a copy: %d, placed %s", code, chk["placed"])
	}
}

func TestMergeFindings(t *testing.T) {
	rep := json.RawMessage(`{"ok":true,"findings":[{"rule":"props.group","path":"props[1].group","message":"m"}]}`)
	got := mergeFindings(rep, []map[string]any{
		{"rule": "props.group", "path": "props[1].group", "message": "m", "severity": "warning"},
		{"rule": "props.type", "path": "props[2].noun", "message": "a creature", "severity": "error"},
	})
	var r struct {
		OK       bool             `json:"ok"`
		Findings []map[string]any `json:"findings"`
	}
	if err := json.Unmarshal(got, &r); err != nil || r.OK || len(r.Findings) != 2 || r.Findings[1]["rule"] != "props.type" {
		t.Errorf("merged: %s", got)
	}
}

// TestProps runs the palette, the models and a check of props through the API (needs the game files; skips without):
// the nouns with their icons, a noun's model, and the check's `placed` (both teams, groups expanded) with the
// placing's findings in the report.
func TestProps(t *testing.T) {
	if gameCopy() == "" {
		t.Skip("no game files")
	}
	e := newEnv(t, true)
	var ns struct {
		OK    bool          `json:"ok"`
		Nouns []paletteNoun `json:"nouns"`
		Icons int           `json:"icons"`
	}
	if code := e.call("GET", "/api/nouns", nil, &ns); code != 200 || !ns.OK || len(ns.Nouns) < 300 || ns.Icons < 200 {
		t.Fatalf("nouns: %d ok %v, %d nouns, %d icons", code, ns.OK, len(ns.Nouns), ns.Icons)
	}
	get := func(path string) []byte {
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
	var rock *paletteNoun
	for i, n := range ns.Nouns {
		if nounKey(n.Name) == "rock_pastoral_large_01" {
			rock = &ns.Nouns[i]
		}
	}
	if rock == nil || rock.Icon == nil || rock.GraphicsScale != 1 || !(rock.Icon.PerPixel > 0) {
		t.Fatalf("the rock: %+v", rock)
	}
	if b := get(rock.Icon.URL); string(b[1:4]) != "PNG" {
		t.Errorf("%s is no PNG", rock.Icon.URL)
	}
	var ns2 json.RawMessage // the second call is the cached reply
	e.call("GET", "/api/nouns", nil, &ns2)
	if !strings.Contains(string(ns2), rock.Icon.URL) {
		t.Errorf("the cached palette lacks the rock")
	}
	var ms struct {
		OK     bool              `json:"ok"`
		Models map[string]string `json:"models"`
	}
	if code := e.call("POST", "/api/models", map[string]any{"nouns": []string{"Rock_Pastoral_Large_01.Noun", "lichenrock_02",
		"no_such_noun"}}, &ms); code != 200 || !ms.OK || ms.Models["rock_pastoral_large_01"] == "" ||
		ms.Models["lichenrock_02"] == "" || ms.Models["no_such_noun"] != "" {
		t.Fatalf("models: %d %+v", code, ms)
	}
	if b := get(ms.Models["rock_pastoral_large_01"]); !strings.Contains(string(b), `"buffers"`) {
		t.Errorf("the rock's glTF: %.200s", b)
	}
	// a check with a blocking rock, a group of SM_Map_v5 and a creature (refused by the placing)
	doc := map[string]any{}
	json.Unmarshal([]byte(e.text(t)), &doc)
	doc["props"] = []any{
		map[string]any{"noun": "Rock_Pastoral_Large_01", "pos": []any{-130.0, -120.0}, "rot": 30.0, "scale": 0.7,
			"blocks": true, "footprint": []any{7.4, 9.7}, "shadow": true},
		map[string]any{"group": map[string]any{"level": "SM_Map_v5", "sets": []any{"D_Rocks", "D_Trees", "D_Foliage"},
			"at": []any{-87.5, -52.6}, "r": 12.0}, "pos": []any{-140.0, -110.0}, "rot": 0.0, "scale": 1.0},
	}
	var chk struct {
		Exit   int `json:"exit"`
		Placed []struct {
			Noun      string     `json:"noun"`
			Pos       [3]float64 `json:"pos"`
			Team      int        `json:"team"`
			Path      string     `json:"path"`
			Card      bool       `json:"card"`
			Footprint *struct {
				Axes [2]float64 `json:"axes"`
			} `json:"footprint"`
		} `json:"placed"`
		Report struct {
			OK bool `json:"ok"`
		} `json:"report"`
	}
	if code := e.call("POST", "/api/check/CM_Ring", doc, &chk); code != 200 || chk.Exit != 0 || !chk.Report.OK {
		t.Fatalf("check: %d %+v", code, chk)
	}
	count := map[string]int{}
	for _, p := range chk.Placed {
		k := p.Path
		if p.Card {
			k += "/card"
		}
		count[k]++
		if p.Path == "props[0]" && !p.Card && p.Team == 2 && (p.Pos[0] != 130 || p.Pos[1] != 120 || p.Footprint == nil ||
			p.Footprint.Axes != [2]float64{3.7, 4.85}) {
			t.Errorf("the rock's mirror: %+v", p)
		}
	}
	if count["props[0]"] != 2 || count["props[0]/card"] != 2 || count["props[1]"] < 10 {
		t.Errorf("placed: %v", count)
	}
	doc["props"] = append(doc["props"].([]any), map[string]any{"noun": "itemshoppe_archon", "pos": []any{-150.0, -170.0}})
	var bad struct {
		Report struct {
			OK       bool `json:"ok"`
			Findings []struct {
				Rule string `json:"rule"`
				Path string `json:"path"`
			} `json:"findings"`
		} `json:"report"`
	}
	e.call("POST", "/api/check/CM_Ring", doc, &bad)
	found := false
	for _, f := range bad.Report.Findings {
		found = found || (f.Rule == "props.type" && f.Path == "props[2].noun")
	}
	if bad.Report.OK || !found {
		t.Errorf("a creature as a prop: %+v", bad.Report)
	}
}
