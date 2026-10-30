package mapc

import (
	"math"
	"testing"
)

// TestClipHalf: the import's cut at x + y = cut keeps the corners inside, puts each crossing on the line and drops a
// loop wholly past it; a cell centre on the diagonal (x + y = 0) of a polygon reaching past the line stays inside.
func TestClipHalf(t *testing.T) {
	sq := []Point{{-2, -2}, {2, -2}, {2, 2}, {-2, 2}} // counter-clockwise, across the diagonal
	got := clipHalf(sq, 0.3)
	for _, p := range got {
		if p[0]+p[1] > 0.3+1e-9 {
			t.Errorf("a corner past the cut: %v", p)
		}
	}
	// the square's 16 m² less the corner past the line, a right triangle with legs of 3.7 m
	if len(got) != 5 || math.Abs(signedArea(got)-(16-3.7*3.7/2)) > 1e-9 {
		t.Errorf("clipped square %v (area %.3f)", got, signedArea(got))
	}
	if !inPolygon(Point{0, 0}, got) || !inPolygon(Point{1.5, -1.5}, got) || inPolygon(Point{0.5, 0.5}, got) {
		t.Errorf("the clipped square holds the wrong points: %v", got)
	}
	if g := clipHalf([]Point{{1, 1}, {3, 1}, {3, 3}}, 0.3); len(g) != 0 {
		t.Errorf("a loop past the cut: %v", g)
	}
	// a U open towards the line, crossing it twice: both arms clipped, the cell between them stays out
	u := []Point{{-10, -10}, {10, -10}, {10, 4}, {6, 4}, {6, -6}, {-6, -6}, {-6, 10}, {-10, 10}}
	g := clipHalf(u, 0.3)
	if !inPolygon(Point{-8, 8}, g) || !inPolygon(Point{8, -8}, g) || inPolygon(Point{0, 0}, g) ||
		inPolygon(Point{8, 2}, g) {
		t.Errorf("the clipped U %v", g)
	}
}
