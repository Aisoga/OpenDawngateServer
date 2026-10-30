package navgraph

import "math"

// Cells marks the cells of an n x n grid over a square level of the given size (metres, centred on (0, 0); cell
// (i, j) centred on ((i - n/2) * size/n, (j - n/2) * size/n), rows from the south, index j*n + i: the line-of-sight
// map's grid) whose centre lies inside one of the graph's areas. The areas are convex and counter-clockwise; a centre
// on an edge counts as inside. A shipped level's Player graph read this way is its walkable ground (less the rim of
// the graph's radius along every wall and drop).
func Cells(g *Graph, size float64, n int) []bool {
	out := make([]bool, n*n)
	if g == nil || n <= 0 || size <= 0 {
		return out
	}
	cell := size / float64(n)
	idx := func(v float64) float64 { return v/cell + float64(n)/2 }
	for _, a := range g.Areas {
		if len(a.Edges) < 3 {
			continue
		}
		x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, e := range a.Edges {
			x, y := float64(e.Pos[0]), float64(e.Pos[1])
			x0, y0, x1, y1 = math.Min(x0, x), math.Min(y0, y), math.Max(x1, x), math.Max(y1, y)
		}
		i0, i1 := int(math.Ceil(idx(x0)-1e-9)), int(math.Floor(idx(x1)+1e-9))
		j0, j1 := int(math.Ceil(idx(y0)-1e-9)), int(math.Floor(idx(y1)+1e-9))
		for j := max(j0, 0); j <= min(j1, n-1); j++ {
			py := (float64(j) - float64(n)/2) * cell
			for i := max(i0, 0); i <= min(i1, n-1); i++ {
				px := (float64(i) - float64(n)/2) * cell
				if inConvex(a.Edges, px, py) {
					out[j*n+i] = true
				}
			}
		}
	}
	return out
}

// inConvex: the point lies left of (or on) every edge of a counter-clockwise convex polygon.
func inConvex(es []Edge, px, py float64) bool {
	for k := range es {
		a, b := es[k].Pos, es[(k+1)%len(es)].Pos
		ax, ay := float64(a[0]), float64(a[1])
		if (float64(b[0])-ax)*(py-ay)-(float64(b[1])-ay)*(px-ax) < -1e-6 {
			return false
		}
	}
	return true
}
