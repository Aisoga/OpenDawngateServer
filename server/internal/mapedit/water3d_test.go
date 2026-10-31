package mapedit

import (
	"math"
	"path/filepath"
	"testing"
)

// TestWaterPreview checks what the 3-D view draws its water quads from: the check reply's preview.water for
// CM_Water (three planes: the centre's once at z -1.5, the slab's and its mirror's at -2), and that an edit of a
// chasm's water reaches the next check's planes (no game files needed).
func TestWaterPreview(t *testing.T) {
	const pkg = "CM_Water"
	e := newEnvWith(t, false, filepath.Join(testMaps, pkg))
	doc, _, _ := e.open(pkg)
	type plane struct {
		Shape, Team int
		Pos         [3]float64
		Rot         float64
		Size        [2]float64
	}
	water := func() []plane {
		var r struct {
			Exit    int
			Preview *struct{ Water []plane }
		}
		if code := e.call("POST", "/api/check/"+pkg, doc, &r); code != 200 || r.Exit != 0 || r.Preview == nil {
			t.Fatalf("check: %d exit %d", code, r.Exit)
		}
		return r.Preview.Water
	}
	ws := water()
	if len(ws) != 3 {
		t.Fatalf("planes: %+v", ws)
	}
	slab := -1
	for _, w := range ws {
		switch {
		case w.Team == 0:
			if w.Pos != [3]float64{0, 0, -1.5} || w.Size[0] < 80 || w.Size[1] < 80 {
				t.Errorf("the centre's plane: %+v", w)
			}
		case w.Pos[2] != -2 || w.Size[0] < 60 || w.Size[1] < 18 || math.Abs(w.Rot-60) > 0.1 ||
			math.Abs(math.Abs(w.Pos[0])-105.5) > 0.5:
			t.Errorf("a slab plane: %+v", w)
		case w.Team == 1:
			slab = w.Shape
		}
	}
	if slab < 0 {
		t.Fatalf("no slab plane of team 1: %+v", ws)
	}
	doc["terrain"].(map[string]any)["shapes"].([]any)[slab].(map[string]any)["water"] = map[string]any{"z": -3}
	n := 0
	for _, w := range water() {
		if w.Shape == slab {
			n++
			if w.Pos[2] != -3 {
				t.Errorf("the edited slab's plane: %+v", w)
			}
		}
	}
	if n != 2 {
		t.Errorf("the edited slab: %d planes, want 2", n)
	}
}
