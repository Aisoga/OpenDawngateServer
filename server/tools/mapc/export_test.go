package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/testgame"
)

func TestExportArgs(t *testing.T) {
	for _, args := range [][]string{
		{"model"}, {"model", "copy", "out"}, {"ground", "pkg"}, {"scene", "a", "b"},
		{"scene", "--max-texture"}, {"scene", "--max-texture", "2", "a", "b", "c"}, {"model", "--bogus", "a", "b", "c"},
	} {
		if code, _, errw := call(args...); code != 2 || !strings.Contains(errw, "mapc scene") {
			t.Errorf("%v: exit %d, %s", args, code, errw)
		}
	}
	if code, _, _ := call("scene", "--json", t.TempDir(), "SM_Map_v5", t.TempDir()); code != 2 {
		t.Errorf("a folder without game files: exit %d", code)
	}
}

// TestExportGame runs the three export commands on the player's copy (skips without one).
func TestExportGame(t *testing.T) {
	game := testgame.Dir(t)
	out := t.TempDir()
	code, stdout, errw := call("scene", "--json", "--max-texture", "64", game, ring, out)
	var res struct {
		OK      bool     `json:"ok"`
		Scene   string   `json:"scene"`
		Objects int      `json:"objects"`
		Models  int      `json:"models"`
		Ground  string   `json:"ground"`
		Missing []string `json:"missing"`
	}
	// the compiled Level's models: the Guardians, Bindings, Cores and archons
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || code != 0 || !res.OK || res.Models < 5 ||
		res.Ground != "CM_Ring_ground.gltf" || len(res.Missing) > 0 {
		t.Fatalf("scene: exit %d, %v: %s %s", code, err, stdout, errw)
	}
	b, err := os.ReadFile(res.Scene)
	var sc struct {
		Map     string `json:"map"`
		Source  string `json:"source"`
		Objects []struct {
			Kind  string `json:"kind"`
			Parts []struct {
				Model string `json:"model"`
			} `json:"parts"`
		} `json:"objects"`
		Models map[string]struct {
			File string `json:"file"`
		} `json:"models"`
	}
	if err != nil || json.Unmarshal(b, &sc) != nil || sc.Map != "CM_Ring" || sc.Source != "package" ||
		len(sc.Objects) != res.Objects {
		t.Fatalf("%s: %v", res.Scene, err)
	}
	for _, o := range sc.Objects {
		for _, p := range o.Parts {
			m, ok := sc.Models[p.Model]
			if _, err := os.Stat(filepath.Join(out, m.File)); !ok || err != nil {
				t.Errorf("%s's model %s: not exported (%v)", o.Kind, p.Model, err)
			}
		}
	}
	// a noun's model, by path and by noun; the second is kept
	code, stdout, errw = call("model", game, "Binding_Mortal_Outer.Noun", "Environments/Shared/tower_blue_rebuild.bmdl", out)
	if code != 0 || strings.Count(stdout, "(kept)") != 2 {
		t.Errorf("model: exit %d: %s %s", code, stdout, errw)
	}
	if code, _, _ := call("model", game, "NoSuchThing.Noun", out); code != 1 {
		t.Errorf("model of a missing noun: exit %d", code)
	}
	code, stdout, errw = call("ground", ring, game, filepath.Join(out, "g"))
	if code != 0 || !strings.Contains(stdout, "204800 triangles") {
		t.Errorf("ground: exit %d: %s %s", code, stdout, errw)
	}
}
