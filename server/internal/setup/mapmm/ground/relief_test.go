package ground

import (
	"bytes"
	"math"
	"testing"

	"dawngate/server/internal/setup/bmdl"
)

// reliefCells: a 64 x 64 map of 128 m, open (0) but for a wall block in the west half (x < -8 m) and a chasm block
// in the east (x > 24 m, |y| < 20 m).
func reliefCells() (cells []byte, chasm func(x, y float64) bool) {
	const w, size = 64, 128.0
	cells = make([]byte, w*w)
	chasm = func(x, y float64) bool { return x > 24 && math.Abs(y) < 20 }
	for j := 0; j < w; j++ {
		for i := 0; i < w; i++ {
			x, y := float64(i-w/2)*size/w, float64(j-w/2)*size/w
			if x < -8 || chasm(x, y) {
				cells[j*w+i] = 128
			}
		}
	}
	return cells, chasm
}

// tops are the authored tops: the wall Plateau, but h in its south half (y < 0); the chasm -depth.
func tops(cells []byte, chasm func(x, y float64) bool, h, depth float64) []float64 {
	const w, size = 64, 128.0
	out := make([]float64, w*w)
	for k, c := range cells {
		x, y := float64(k%w-w/2)*size/w, float64(k/w-w/2)*size/w
		switch {
		case c == 0:
		case chasm(x, y):
			out[k] = -depth
		case y < 0:
			out[k] = h
		default:
			out[k] = Plateau
		}
	}
	return out
}

// TestReliefPlateau: with every top at Plateau the relief gives the ground made without one bit for bit (heights,
// normals, colours, the model's bytes), so a map whose heights are all 5 m (or that has none) builds as before.
func TestReliefPlateau(t *testing.T) {
	cells, chasm := reliefCells()
	field, err := NewField(64, 64, cells, 128)
	if err != nil {
		t.Fatal(err)
	}
	plain := Spec{Size: 128, Down: chasm}
	with := plain
	with.Relief = NewRelief(64, 64, 128, tops(cells, chasm, Plateau, Plateau))
	a, b := NewGroundSpec(field, 64, 2, plain), NewGroundSpec(field, 64, 2, with)
	if !bytes.Equal(floats(a.Z), floats(b.Z)) || !bytes.Equal(triples(a.Normal), triples(b.Normal)) ||
		!bytes.Equal(triples(a.RGB), triples(b.RGB)) {
		t.Fatal("the all-Plateau relief changes the ground")
	}
	if a.Top != nil || b.Top == nil {
		t.Errorf("Top: %v without a relief, %d with", a.Top != nil, len(b.Top))
	}
	tmpl, err := bmdl.Write(madeUpTemplate())
	if err != nil {
		t.Fatal(err)
	}
	ma, err := BuildModel(tmpl, a, [2]string{DirtEntry, GrassEntry})
	if err != nil {
		t.Fatal(err)
	}
	mb, err := BuildModel(tmpl, b, [2]string{DirtEntry, GrassEntry})
	if err != nil || !bytes.Equal(ma, mb) {
		t.Errorf("the models differ (%v)", err)
	}
	if CliffEnd(Plateau) != cliffHi || CliffEnd(2) != cliffHi || CliffEnd(12) != cliffLo+3.5*12/5.0 {
		t.Errorf("CliffEnd: %g %g %g", CliffEnd(Plateau), CliffEnd(2), CliffEnd(12))
	}
}

// TestReliefHeights: the south half's wall stands h high and the chasm drops its depth, the north half keeps
// Plateau, the walkable floor stays at 0, the plateaus ramp between (no step), and the cliff widens with the height.
func TestReliefHeights(t *testing.T) {
	cells, chasm := reliefCells()
	field, err := NewField(64, 64, cells, 128)
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Size: 128, Down: chasm, Relief: NewRelief(64, 64, 128, tops(cells, chasm, 12, 15))}
	plain := Spec{Size: 128, Down: chasm}
	z := func(x, y float64, s *Spec) float64 { return Height(field.At(x, y), x, y, s) }
	if r := spec.Relief.Top(-50, -40, false); r != 12 {
		t.Errorf("the south wall's top %g, want 12 exactly", r)
	}
	if r := spec.Relief.Top(-50, 40, false); r != Plateau {
		t.Errorf("the north wall's top %g, want %g", r, Plateau)
	}
	if r := spec.Relief.Top(50, 0, true); r != 15 {
		t.Errorf("the chasm's depth %g, want 15", r)
	}
	// deep in the south wall: 12 m give or take the hills (+-0.6)
	if v := z(-50, -40, &spec); math.Abs(v-12) > roll {
		t.Errorf("z(-50, -40) = %g, want 12 +- %g", v, roll)
	}
	if v := z(-50, 40, &spec); v != z(-50, 40, &plain) {
		t.Errorf("the north wall moved: %g, plain %g", v, z(-50, 40, &plain))
	}
	// the chasm's bottom (more than CliffEnd(15) = 11.5 m past its edge)
	if v := z(56, 0, &spec); v != -15 {
		t.Errorf("z(56, 0) = %g, want -15", v)
	}
	// the walkable floor: every open cell at 0, with heights as without
	for k, c := range cells {
		if c != 0 {
			continue
		}
		x, y := float64(k%64-32)*2, float64(k/64-32)*2
		if a, b := Height(field.D[k], x, y, &spec), Height(field.D[k], x, y, &plain); a != 0 || b != 0 {
			t.Fatalf("open cell (%g, %g) at %g (plain %g)", x, y, a, b)
		}
	}
	// the ramp: along x = -50 from y -20 to 20 the plateau never steps more than 2.2 m in 1 m
	prev := z(-50, -20, &spec)
	for y := -19.0; y <= 20; y++ {
		v := z(-50, y, &spec)
		if math.Abs(v-prev) > 2.2 {
			t.Errorf("a step of %g m at y %g", v-prev, y)
		}
		prev = v
	}
	// the cliff: 12 m reaches full height CliffEnd(12) = 9.4 m past the edge (x = -8), not at 4.5 m
	if v := z(-8-4.5, -40, &spec); v > 8 {
		t.Errorf("4.5 m into the 12-m wall z %g: the cliff didn't widen", v)
	}
	if v := z(-8-9.6, -40, &spec); v < 11 {
		t.Errorf("9.6 m into the 12-m wall z %g", v)
	}
}
