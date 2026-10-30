package main

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

var dressPkg = testmaps.Pkg("CM_Dress")

func TestPropsArgs(t *testing.T) {
	for _, args := range [][]string{{"props"}, {"props", "pkg"}, {"nouns"}, {"nouns", "a", "b"}} {
		if code, _, errw := call(args...); code != 2 || !strings.Contains(errw, "mapc props") {
			t.Errorf("%v: exit %d, %s", args, code, errw)
		}
	}
	if code, _, _ := call("nouns", t.TempDir()); code != 2 {
		t.Errorf("a folder without game files: exit %d", code)
	}
	// without the copy check warns that the blocking rock's footprint is unknown; preview lists the placements
	code, out, _ := call("check", "--json", dressPkg)
	var r mapc.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 0 || len(r.Findings) != 1 ||
		r.Findings[0].Rule != "props.footprint" {
		t.Errorf("check: exit %d, %v: %s", code, err, out)
	}
	dir := t.TempDir()
	if code, _, errw := call("preview", dressPkg, dir); code != 0 {
		t.Fatalf("preview: exit %d: %s", code, errw)
	}
	b, err := os.ReadFile(filepath.Join(dir, "preview.json"))
	var idx mapc.PreviewIndex
	if err == nil {
		err = json.Unmarshal(b, &idx)
	}
	if err != nil || len(idx.Props) < 300 {
		t.Errorf("preview.json: %v, %d props", err, len(idx.Props))
	}
}

// TestPropsGame runs props and nouns on the player's copy, and check --copy (skips without one).
func TestPropsGame(t *testing.T) {
	game := testgame.Dir(t)
	code, out, errw := call("props", "--json", dressPkg, game)
	var res struct {
		OK       bool           `json:"ok"`
		Map      string         `json:"map"`
		Props    []mapc.Placed  `json:"props"`
		Findings []mapc.Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || code != 0 || !res.OK || res.Map != "CM_Dress" ||
		len(res.Props) != 366 || len(res.Findings) != 0 {
		t.Fatalf("props: exit %d, %v: %d props %+v %s", code, err, len(res.Props), res.Findings, errw)
	}
	if p := res.Props[0]; p.Path != "props[0]" || p.Footprint == nil || !p.Blocks {
		t.Errorf("the first prop %+v", p)
	}
	code, out, _ = call("nouns", "--json", game)
	var ns struct {
		OK    bool            `json:"ok"`
		Nouns []mapc.PropNoun `json:"nouns"`
	}
	if err := json.Unmarshal([]byte(out), &ns); err != nil || code != 0 || len(ns.Nouns) < 300 {
		t.Fatalf("nouns: exit %d, %v", code, err)
	}
	for _, n := range ns.Nouns {
		if !(n.GraphicsScale > 0) {
			t.Errorf("noun %s: graphicsScale %g", n.Name, n.GraphicsScale)
		}
	}
	if code, out, _ := call("check", "--json", "--copy", game, dressPkg); code != 0 ||
		!strings.Contains(out, `"findings": []`) {
		t.Errorf("check --copy: exit %d: %s", code, out)
	}
	// icons: a picture with the rock in it and its sidecar; kept on a second run; a noun without a model fails
	dir := t.TempDir()
	for run := range 2 {
		code, out, errw := call("icon", "--json", "--size", "64", game, "Rock_Pastoral_Large_01.Noun", dir)
		var ic struct {
			OK    bool              `json:"ok"`
			Icons []json.RawMessage `json:"icons"`
		}
		if err := json.Unmarshal([]byte(out), &ic); err != nil || code != 0 || !ic.OK || len(ic.Icons) != 1 ||
			strings.Contains(string(ic.Icons[0]), `"cached"`) != (run == 1) {
			t.Fatalf("icon run %d: exit %d, %v: %s %s", run, code, err, out, errw)
		}
	}
	f, err := os.Open(filepath.Join(dir, "rock_pastoral_large_01.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatalf("%v", err)
	}
	opaque := 0
	for y := range 64 {
		for x := range 64 {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
				opaque++
			}
		}
	}
	if opaque < 64*64/3 || opaque == 64*64 {
		t.Errorf("%d of %d pixels drawn", opaque, 64*64)
	}
	if code, _, _ := call("icon", game, "Water.Noun", dir); code != 1 {
		t.Errorf("a noun without a model: exit %d", code)
	}
}
