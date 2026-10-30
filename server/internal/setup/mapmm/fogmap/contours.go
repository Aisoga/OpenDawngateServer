package fogmap

// Contours are the loops of a field's level t over the whole map in cell coordinates (cell centre (i, j) at (i, j)),
// by the same marching squares as the outlines but in one piece: a region (the cells above t) gives a
// counter-clockwise loop round it and a clockwise loop round each hole in it. Each loop is simplified by
// Douglas-Peucker to tol cells, the tolerance halved until the loop stays simple (mapc's import traces a shipped
// level's layout with it).
func Contours(f Field, w, h int, t, tol float64) ([][]Point, error) {
	loops, err := tileLoops(f, w, h, t, 0, w-1, 0, h-1)
	if err != nil {
		return nil, err
	}
	out := make([][]Point, 0, len(loops))
	for _, lp := range loops {
		s := tol
		sl := simplifyLoop(lp, s)
		for !isSimple(sl) && s > 0 {
			if s > 0.02 {
				s /= 2
			} else {
				s = 0
			}
			sl = simplifyLoop(lp, s)
		}
		out = append(out, sl)
	}
	return out, nil
}

// Area is a loop's signed area (positive counter-clockwise).
func Area(pts []Point) float64 { return area(pts) }
