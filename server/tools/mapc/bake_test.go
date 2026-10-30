package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// TestBakeFullRoundTrip (the player's copy; skips without one): CM_Ring's ground at 2 x 1024, `bake --full` writes
// its four tiles, and the package with those tiles as its ground's picture (its layers and stamps dropped: they are
// in the picture) builds an archive whose tile textures are byte for byte the first build's.
func TestBakeFullRoundTrip(t *testing.T) {
	game := testgame.Dir(t)
	pkg := filepath.Join(t.TempDir(), "CM_Ring")
	if err := os.MkdirAll(filepath.Join(pkg, "ground"), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(ring, "map.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(src, &doc); err != nil {
		t.Fatal(err)
	}
	g := doc["ground"].(map[string]any)
	g["texels"] = 1024
	put := func() {
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "map.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put()
	out := t.TempDir()
	code, stdout, errw := call("bake", "--json", "--full", pkg, game, out)
	var res struct {
		OK     bool     `json:"ok"`
		Margin int      `json:"margin"`
		Tiles  []string `json:"tiles_png"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || code != 0 || !res.OK || res.Margin != 4 ||
		len(res.Tiles) != 4 || res.Tiles[1] != "tile_1_0.png" {
		t.Fatalf("bake --full: exit %d, %v: %s %s", code, err, stdout, errw)
	}
	var picture []any
	for _, f := range res.Tiles {
		b, err := os.ReadFile(filepath.Join(out, f))
		if err != nil {
			t.Fatal(err)
		}
		if c, err := png.DecodeConfig(bytes.NewReader(b)); err != nil || c.Width != 1024 || c.Height != 1024 {
			t.Fatalf("%s: %+v %v", f, c, err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "ground", f), b, 0o644); err != nil {
			t.Fatal(err)
		}
		picture = append(picture, "ground/"+f)
	}
	build := func(dir string) *snap.Archive {
		t.Helper()
		if code, stdout, errw := call("build", "--json", pkg, game, dir); code != 0 {
			t.Fatalf("build: exit %d: %s %s", code, stdout, errw)
		}
		b, err := os.ReadFile(filepath.Join(dir, "zz_CM_Ring.snap"))
		if err != nil {
			t.Fatal(err)
		}
		a, err := snap.Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	entry := func(a *snap.Archive, name string) []byte {
		t.Helper()
		for _, e := range a.Entries {
			if e.Name == name {
				b, err := a.Read(e)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
		}
		t.Fatalf("no %s", name)
		return nil
	}
	first := build(t.TempDir())
	delete(g, "layers")
	delete(g, "stamps")
	g["picture"] = picture
	put()
	second := build(t.TempDir())
	for tj := 0; tj < 2; tj++ {
		for ti := 0; ti < 2; ti++ {
			name := mapc.BakeTileEntry("CM_Ring", ti, tj)
			if !bytes.Equal(entry(first, name), entry(second, name)) {
				t.Errorf("%s differs after the round trip", name)
			}
		}
	}
	// a missing tile: the check names it
	if err := os.Remove(filepath.Join(pkg, "ground", "tile_1_1.png")); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = call("check", "--json", pkg)
	if code != 1 || !bytes.Contains([]byte(stdout), []byte("ground/tile_1_1.png is missing from the package")) {
		t.Errorf("check without a tile: exit %d: %s", code, stdout)
	}
}
