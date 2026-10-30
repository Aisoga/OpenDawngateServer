package ground

import "testing"

// TestDips: another map's fords (Spec.Dips) dip the walkable floor as MM_Map_v3's ford does: a dip shaped and placed
// as MM's gives MM's heights, the deepest of two overlapping dips counts, and nothing changes off the walkable ground
// or outside the rectangles.
func TestDips(t *testing.T) {
	mm := Spec{Ford: true}
	as := Spec{Dips: []Dip{{X: fordX, Y: fordY, Cos: cos45, Sin: sin45, HalfW: fordW / 2, HalfL: fordL / 2,
		Depth: fordDepth}}}
	for _, p := range [][3]float64{{-0.9, 0.9, -5}, {5, 5, -0.7}, {-10, 12, -3}, {14, 14, -9}, {30, -30, -4},
		{-0.9, 0.9, 2}} {
		if a, b := height(p[2], p[0], p[1], &as), height(p[2], p[0], p[1], &mm); a != b {
			t.Errorf("at %v: %v, MM's ford %v", p, a, b)
		}
	}
	if z := height(-5, -0.9, 0.9, &as); z != fordDepth {
		t.Errorf("the ford's middle: %v", z)
	}
	two := Spec{Dips: []Dip{{X: 0, Y: 0, Cos: 1, HalfW: 20, HalfL: 20, Depth: -0.5},
		{X: 10, Y: 0, Cos: 1, HalfW: 20, HalfL: 20, Depth: -0.8}}}
	if z := height(-5, 5, 0, &two); z != -0.8 {
		t.Errorf("two dips: %v, the deeper -0.8", z)
	}
	if z := height(-5, -10, 0, &two); z != -0.5 {
		t.Errorf("one dip: %v", z)
	}
	if z := height(-5, 40, 0, &two); z != 0 {
		t.Errorf("outside: %v", z)
	}
	if z := height(1, 5, 0, &two); z != height(1, 5, 0, &Spec{}) {
		t.Errorf("past the walkable edge: %v", z)
	}
}
