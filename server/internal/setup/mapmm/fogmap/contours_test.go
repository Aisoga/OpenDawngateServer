package fogmap

import "testing"

// TestContours: a 10 x 10 region with a 2 x 2 hole gives one counter-clockwise loop round it and one clockwise loop
// round the hole, each through the midpoints between the inside and the outside cells; a second region apart gives
// its own loop.
func TestContours(t *testing.T) {
	const w, h = 24, 16
	f := make(Field, w*h)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			in := (i >= 2 && i <= 11 && j >= 2 && j <= 11 && !(i >= 6 && i <= 7 && j >= 6 && j <= 7)) ||
				(i >= 15 && i <= 20 && j >= 3 && j <= 5)
			if in {
				f[j*w+i] = 1
			} else {
				f[j*w+i] = -1
			}
		}
	}
	loops, err := Contours(f, w, h, 0, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	if len(loops) != 3 {
		t.Fatalf("%d loops", len(loops))
	}
	var ccw, cw []float64
	for _, lp := range loops {
		if a := Area(lp); a > 0 {
			ccw = append(ccw, a)
		} else {
			cw = append(cw, -a)
		}
		for _, p := range lp {
			if p.X < 1.5 || p.X > 20.5 || p.Y < 1.5 || p.Y > 11.5 {
				t.Errorf("a corner outside the regions' cells: %+v", p)
			}
		}
	}
	if len(ccw) != 2 || len(cw) != 1 {
		t.Fatalf("outer %v, holes %v", ccw, cw)
	}
	// the hole's loop runs through the midpoints round its 2 x 2 cells (corners cut: an octagon of area 3.5 or the
	// square of 4 when the corners are kept); the region's round its 10 x 10
	if cw[0] < 3 || cw[0] > 4 {
		t.Errorf("hole area %.2f", cw[0])
	}
	big := max(ccw[0], ccw[1])
	if big < 99 || big > 100 {
		t.Errorf("region area %.2f", big)
	}
}
