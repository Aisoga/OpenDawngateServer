package navgraph

import "math"

// Point is a world position in the plane (metres).
type Point struct{ X, Y float64 }

// Hole is a disc no area may reach into: centre and radius (a Guardian's platform).
type Hole struct{ X, Y, R float64 }

// WalkableMask marks the open cells (byte 0) of a w x h line-of-sight map that are at least erode 4-neighbour
// steps from every blocking cell and from outside the map.
func WalkableMask(cells []byte, w, h, erode int) []bool {
	const inf = 1 << 30
	dist := make([]int, w*h)
	queue := make([]int, 0, w*h)
	for k := range dist {
		dist[k] = inf
		if cells[k] != 0 {
			dist[k] = 0
			queue = append(queue, k)
		}
	}
	for q := 0; q < len(queue); q++ {
		k := queue[q]
		i, j := k%w, k/w
		for _, n := range [4][2]int{{i + 1, j}, {i - 1, j}, {i, j + 1}, {i, j - 1}} {
			ii, jj := n[0], n[1]
			if ii >= 0 && ii < w && jj >= 0 && jj < h && dist[jj*w+ii] == inf {
				dist[jj*w+ii] = dist[k] + 1
				queue = append(queue, jj*w+ii)
			}
		}
	}
	mask := make([]bool, w*h)
	for k := range mask {
		i, j := k%w, k/w
		border := min(i, w-1-i, j, h-1-j) + 1
		mask[k] = dist[k] >= erode && border >= erode
	}
	return mask
}

// square is the world rectangle (x0, y0, x1, y1) of grid block (bx, by).
func square(bx, by, cell, w, h int, cw, ch float64) [4]float64 {
	return [4]float64{
		(float64(bx*cell) - 0.5 - float64(w)/2) * cw, (float64(by*cell) - 0.5 - float64(h)/2) * ch,
		(float64((bx+1)*cell) - 0.5 - float64(w)/2) * cw, (float64((by+1)*cell) - 0.5 - float64(h)/2) * ch,
	}
}

// inHole reports whether any point of the rectangle lies closer than a hole's radius to its centre.
func inHole(r [4]float64, holes []Hole) bool {
	for _, ho := range holes {
		dx := pyMax(pyMax(r[0]-ho.X, 0.0), ho.X-r[2])
		dy := pyMax(pyMax(r[1]-ho.Y, 0.0), ho.Y-r[3])
		if float64(dx*dx)+float64(dy*dy) < float64(ho.R*ho.R) {
			return true
		}
	}
	return false
}

// GridPolygons returns the squares of cell x cell map cells that are all walkable, as counter-clockwise
// polygons at height z in world units, row by row. A map cell (i, j) is centred on
// ((i - w/2) sizeX/w, (j - h/2) sizeY/h). Every shared edge is a whole edge of both squares (no T-junction).
// No square comes closer to a hole's centre than its radius. With keep points only the 4-connected parts of
// blocks that hold one stay: each point picks the part of the nearest block (within 5 blocks; the first such
// block in discovery order on a tie), a keep point inside a hole keeps nothing.
func GridPolygons(mask []bool, w, h int, sizeX, sizeY float64, cell int, z float64, keep []Point,
	holes []Hole) []Polygon {
	cw, ch := sizeX/float64(w), sizeY/float64(h)
	blk, bw, bh := KeptBlocks(mask, w, h, sizeX, sizeY, cell, keep, holes)
	var polys []Polygon
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			if blk[by*bw+bx] {
				r := square(bx, by, cell, w, h, cw, ch)
				polys = append(polys, Polygon{{r[0], r[1], z}, {r[2], r[1], z}, {r[2], r[3], z}, {r[0], r[3], z}})
			}
		}
	}
	return polys
}

// BlockOf is the grid block (bx, by) holding world point p, for a w x h map spanning sizeX x sizeY in blocks of
// cell x cell cells (it may lie outside the grid).
func BlockOf(p Point, w, h int, sizeX, sizeY float64, cell int) (bx, by int) {
	cw, ch := sizeX/float64(w), sizeY/float64(h)
	return int(pyFloorDiv(p.X/cw+float64(w)/2+0.5, float64(cell))), int(pyFloorDiv(p.Y/ch+float64(h)/2+0.5, float64(cell)))
}

// BlockRect is the world rectangle (x0, y0, x1, y1) of grid block (bx, by).
func BlockRect(bx, by, cell, w, h int, sizeX, sizeY float64) [4]float64 {
	return square(bx, by, cell, w, h, sizeX/float64(w), sizeY/float64(h))
}

// KeptBlocks are GridPolygons' squares as a bw x bh grid of blocks (row by, column bx at by*bw+bx): true where the
// graph has a square.
func KeptBlocks(mask []bool, w, h int, sizeX, sizeY float64, cell int, keep []Point, holes []Hole) (blk []bool,
	bw, bh int) {
	cw, ch := sizeX/float64(w), sizeY/float64(h)
	bw, bh = w/cell, h/cell
	blk = make([]bool, bw*bh)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			ok := true
			for di := 0; di < cell && ok; di++ {
				for dj := 0; dj < cell && ok; dj++ {
					ok = mask[(by*cell+dj)*w+bx*cell+di]
				}
			}
			blk[by*bw+bx] = ok && !(len(holes) > 0 && inHole(square(bx, by, cell, w, h, cw, ch), holes))
		}
	}
	if len(keep) > 0 {
		var kept []Point
		for _, p := range keep {
			in := false
			for _, ho := range holes {
				if norm(p.X-ho.X, p.Y-ho.Y) < ho.R {
					in = true
					break
				}
			}
			if !in {
				kept = append(kept, p)
			}
		}
		// The 4-connected parts, numbered by a depth-first search from each unnumbered block in row order;
		// order lists the blocks as they were numbered.
		comp := make([]int, bw*bh)
		for i := range comp {
			comp[i] = -1
		}
		var order []int
		n := 0
		for by := 0; by < bh; by++ {
			for bx := 0; bx < bw; bx++ {
				if !blk[by*bw+bx] || comp[by*bw+bx] >= 0 {
					continue
				}
				st := []int{by*bw + bx}
				comp[by*bw+bx] = n
				order = append(order, by*bw+bx)
				for len(st) > 0 {
					k := st[len(st)-1]
					st = st[:len(st)-1]
					x, y := k%bw, k/bw
					for _, nb := range [4][2]int{{x + 1, y}, {x - 1, y}, {x, y + 1}, {x, y - 1}} {
						xx, yy := nb[0], nb[1]
						if xx >= 0 && xx < bw && yy >= 0 && yy < bh && blk[yy*bw+xx] && comp[yy*bw+xx] < 0 {
							comp[yy*bw+xx] = n
							order = append(order, yy*bw+xx)
							st = append(st, yy*bw+xx)
						}
					}
				}
				n++
			}
		}
		wanted := make([]bool, n)
		for _, p := range kept {
			bx := int(pyFloorDiv(p.X/cw+float64(w)/2+0.5, float64(cell)))
			by := int(pyFloorDiv(p.Y/ch+float64(h)/2+0.5, float64(cell)))
			best, bc := -1, -1
			for _, k := range order {
				dx, dy := k%bw-bx, k/bw-by
				if d := dx*dx + dy*dy; best < 0 || d < best {
					best, bc = d, comp[k]
				}
			}
			if best >= 0 && best <= 25 {
				wanted[bc] = true
			}
		}
		for k := range blk {
			blk[k] = blk[k] && wanted[comp[k]]
		}
	}
	return blk, bw, bh
}

// IslandPolygon is a regular n-gon (counter-clockwise) of circumradius r at (x, y), height z: a Guardian's own
// island in the NPC graph.
func IslandPolygon(x, y, r, z float64, n int) Polygon {
	pi := math.Pi
	twoPi := 2 * pi
	p := make(Polygon, n)
	for k := 0; k < n; k++ {
		a := twoPi * float64(k) / float64(n)
		p[k] = [3]float64{x + float64(r*math.Cos(a)), y + float64(r*math.Sin(a)), z}
	}
	return p
}
