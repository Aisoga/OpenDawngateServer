// Package fogmap makes MM_Map_v3's fog-of-war outlines (levels/MM_Map_v3/MM_Map_v3.out) and its minimap picture
// from the line-of-sight map, byte for byte as the Python reference does.
//
// The outlines are the contour of the blocked cells (byte != 0) Inset cells inside the cells' edge, found by
// marching squares over the signed distance field at the cell centres (diagonal saddles joined, so diagonal walls
// stay closed), in tiles of Tile cells that are split in four while a tile's blocked part has a hole: every
// outline is a simple counter-clockwise polygon without holes (the seams lie inside blocked ground), simplified by
// Douglas-Peucker to Tolerance cells. They are written in the client's .out format (see Encode), terrain outlines
// only.
package fogmap

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"dawngate/server/internal/setup/mapmm/los"
)

// The tracer's parameters: the contour's depth inside the blocked cells, the tile size and the simplification
// tolerance, all in cells.
const (
	Inset     = 0.45
	Tile      = 64
	Tolerance = 0.3
)

// Point is a point in cell coordinates (Trace's loops) or world units (the outlines).
type Point struct{ X, Y float64 }

// Outline is one polygon of an outline file: its grass id (0 for terrain) and its points (closed, the last point
// not repeated, counter-clockwise with the blocked side on the left).
type Outline struct {
	Grass  uint32
	Points []Point
}

// Outlines is the .out file of an 8-bit line-of-sight map whose cells span sx x sy world units.
func Outlines(img los.Image, sx, sy float64) ([]byte, error) {
	f, err := NewField(img)
	if err != nil {
		return nil, err
	}
	return OutlinesFrom(img, f, sx, sy)
}

// OutlinesFrom is Outlines with the map's field already computed.
func OutlinesFrom(img los.Image, f Field, sx, sy float64) ([]byte, error) {
	loops, err := Trace(img, f, sx, sy)
	if err != nil {
		return nil, err
	}
	outs := make([]Outline, len(loops))
	for k, lp := range loops {
		outs[k] = Outline{Points: lp}
	}
	return Encode(outs)
}

// Trace returns the terrain outlines of a line-of-sight map in world units (x right, y up, the map centred on
// the origin), each simple and counter-clockwise, every coordinate a float32 value.
func Trace(img los.Image, f Field, sx, sy float64) ([][]Point, error) {
	w, h := img.W, img.H
	if len(f) != w*h {
		return nil, fmt.Errorf("fogmap: the field has %d values, the map %d x %d cells", len(f), w, h)
	}
	var loops [][]Point
	var tileRec func(i0, j0, size int) error
	tileRec = func(i0, j0, size int) error {
		i1, j1 := min(i0+size, w-1), min(j0+size, h-1)
		anyIn := false
		for j := j0; j <= j1 && !anyIn; j++ {
			for i := i0; i <= i1; i++ {
				if f[j*w+i] > Inset {
					anyIn = true
					break
				}
			}
		}
		if !anyIn {
			return nil
		}
		ls, err := tileLoops(f, w, h, Inset, i0, i1, j0, j1)
		if err != nil {
			return err
		}
		hole := false
		for _, lp := range ls {
			if area(lp) < 0 {
				hole = true
				break
			}
		}
		if hole && size > 2 {
			half := size / 2
			for _, dj := range [2]int{0, half} {
				for _, di := range [2]int{0, half} {
					if i0+di < i1 && j0+dj < j1 {
						if err := tileRec(i0+di, j0+dj, half); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}
		for _, lp := range ls {
			s := Tolerance
			out := simplifyLoop(lp, s)
			for !isSimple(out) && s > 0 {
				if s > 0.02 {
					s = s / 2
				} else {
					s = 0
				}
				out = simplifyLoop(lp, s)
			}
			loops = append(loops, out)
		}
		return nil
	}
	for j0 := 0; j0 < h-1; j0 += Tile {
		for i0 := 0; i0 < w-1; i0 += Tile {
			if err := tileRec(i0, j0, Tile); err != nil {
				return nil, err
			}
		}
	}
	hx, hy := sx/2, sy/2
	fw, fh := float64(w), float64(h)
	var world [][]Point
	for _, lp := range loops {
		pts := make([]Point, len(lp))
		for k, p := range lp {
			x := maxF(-hx, minF(hx, (p.X-fw/2)*sx/fw))
			y := maxF(-hy, minF(hy, (p.Y-fh/2)*sy/fh))
			pts[k] = Point{float64(float32(x)), float64(float32(y))}
		}
		pts = dedup(pts)
		if len(pts) >= 3 {
			world = append(world, pts)
		}
	}
	return world, nil
}

// dedup drops every point equal to the one before it (the first compared with the last).
func dedup(pts []Point) []Point {
	var out []Point
	for k, p := range pts {
		prev := pts[(k-1+len(pts))%len(pts)]
		if p != prev {
			out = append(out, p)
		}
	}
	return out
}

func area(pts []Point) float64 {
	a := 0.0
	for k := range pts {
		p1, p2 := pts[(k-1+len(pts))%len(pts)], pts[k]
		a += float64(p1.X*p2.Y) - float64(p2.X*p1.Y)
	}
	return a / 2
}

type corner [2]int

// edge is a sample grid edge, its corners in order.
type edge struct{ a, b corner }

func cornerLess(a, b corner) bool { return a[0] < b[0] || (a[0] == b[0] && a[1] < b[1]) }

func mkEdge(p, q corner) edge {
	if cornerLess(q, p) {
		return edge{q, p}
	}
	return edge{p, q}
}

// starts holds the contour segments by the edge they start on, in insertion order (popLast takes the newest).
type starts struct {
	items []startItem
	idx   map[edge]int
	alive int
	top   int
}

type startItem struct {
	key, next edge
	p         Point
	dead      bool
}

func (s *starts) add(key, next edge, p Point) error {
	if _, ok := s.idx[key]; ok {
		return errors.New("fogmap: two contour segments start on one edge")
	}
	s.idx[key] = len(s.items)
	s.items = append(s.items, startItem{key: key, next: next, p: p})
	s.alive++
	s.top = len(s.items)
	return nil
}

func (s *starts) popLast() startItem {
	for s.items[s.top-1].dead {
		s.top--
	}
	s.top--
	it := s.items[s.top]
	s.items[s.top].dead = true
	delete(s.idx, it.key)
	s.alive--
	return it
}

func (s *starts) pop(key edge) (startItem, error) {
	k, ok := s.idx[key]
	if !ok {
		return startItem{}, errors.New("fogmap: a contour does not close")
	}
	it := s.items[k]
	s.items[k].dead = true
	delete(s.idx, key)
	s.alive--
	return it, nil
}

// tileLoops returns the directed contour loops (blocked side on the left) of the samples (i0..i1) x (j0..j1),
// inclusive, with open ground all round, in cell coordinates.
func tileLoops(f Field, w, h int, t float64, i0, i1, j0, j1 int) ([][]Point, error) {
	val := func(c corner) (float64, bool) {
		if i0 <= c[0] && c[0] <= i1 && j0 <= c[1] && c[1] <= j1 {
			return f[c[1]*w+c[0]], true
		}
		return 0, false
	}
	crossing := func(e edge) Point {
		a, b := e.a, e.b
		fa, oka := val(a)
		fb, okb := val(b)
		if !oka || fa <= t {
			a, b, fa, fb, okb = b, a, fb, fa, oka
		}
		if !okb { // the tile's edge; past the map's last cell, the map's edge (cell coordinate w or h)
			x, y := float64(a[0]), float64(a[1])
			if b[0] >= w {
				x = float64(min(max(b[0], a[0]), w))
			}
			if b[1] >= h {
				y = float64(min(max(b[1], a[1]), h))
			}
			return Point{x, y}
		}
		u := (fa - t) / (fa - fb)
		return Point{float64(a[0]) + float64(float64(b[0]-a[0])*u), float64(a[1]) + float64(float64(b[1]-a[1])*u)}
	}
	st := &starts{idx: map[edge]int{}}
	for b := j0 - 1; b < j1+1; b++ {
		for a := i0 - 1; a < i1+1; a++ {
			cs := [4]corner{{a, b}, {a + 1, b}, {a + 1, b + 1}, {a, b + 1}}
			var ins [4]bool
			nin := 0
			for k, c := range cs {
				v, ok := val(c)
				ins[k] = ok && v > t
				if ins[k] {
					nin++
				}
			}
			if nin == 0 || nin == 4 {
				continue
			}
			// edge k joins corner k and k + 1 (counter-clockwise); exit = inside -> outside, entry = outside -> inside
			var exits, entries []int
			for k := 0; k < 4; k++ {
				if ins[k] && !ins[(k+1)%4] {
					exits = append(exits, k)
				}
				if !ins[k] && ins[(k+1)%4] {
					entries = append(entries, k)
				}
			}
			var pairs [][2]int
			if len(exits) == 1 {
				pairs = [][2]int{{exits[0], entries[0]}}
			} else { // a saddle: join the two blocked corners (the exit pairs with the next entry counter-clockwise)
				for _, x := range exits {
					best := entries[0]
					for _, n := range entries[1:] {
						if (n-x+4)%4 < (best-x+4)%4 {
							best = n
						}
					}
					pairs = append(pairs, [2]int{x, best})
				}
			}
			for _, pr := range pairs {
				x, n := pr[0], pr[1]
				kx := mkEdge(cs[x], cs[(x+1)%4])
				kn := mkEdge(cs[n], cs[(n+1)%4])
				if err := st.add(kx, kn, crossing(kx)); err != nil {
					return nil, err
				}
			}
		}
	}
	var loops [][]Point
	for st.alive > 0 {
		first := st.popLast()
		loop := []Point{first.p}
		kn := first.next
		for kn != first.key {
			it, err := st.pop(kn)
			if err != nil {
				return nil, err
			}
			kn = it.next
			loop = append(loop, it.p)
		}
		d := dedup(loop)
		if len(d) >= 3 && math.Abs(area(d)) > 1e-9 {
			loops = append(loops, d)
		}
	}
	return loops, nil
}

func segDist(p, a, b Point) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := float64(dx*dx) + float64(dy*dy)
	if l == 0 {
		return hypot(p.X-a.X, p.Y-a.Y)
	}
	u := maxF(0.0, minF(1.0, (float64((p.X-a.X)*dx)+float64((p.Y-a.Y)*dy))/l))
	return hypot(p.X-a.X-float64(u*dx), p.Y-a.Y-float64(u*dy))
}

// dp is Douglas-Peucker on an open polyline (its end points kept).
func dp(pts []Point, tol float64) []Point {
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	stack := [][2]int{{0, len(pts) - 1}}
	for len(stack) > 0 {
		a, b := stack[len(stack)-1][0], stack[len(stack)-1][1]
		stack = stack[:len(stack)-1]
		best, bi := -1.0, -1
		for k := a + 1; k < b; k++ {
			if d := segDist(pts[k], pts[a], pts[b]); d > best {
				best, bi = d, k
			}
		}
		if bi >= 0 && best > tol {
			keep[bi] = true
			stack = append(stack, [2]int{a, bi}, [2]int{bi, b})
		}
	}
	var out []Point
	for k, p := range pts {
		if keep[k] {
			out = append(out, p)
		}
	}
	return out
}

// simplifyLoop is Douglas-Peucker on a closed loop, split at its first point and the point farthest from it.
func simplifyLoop(loop []Point, tol float64) []Point {
	if tol <= 0 || len(loop) <= 4 {
		return append([]Point(nil), loop...)
	}
	far, farD := 0, -1.0
	for k, p := range loop {
		dx, dy := p.X-loop[0].X, p.Y-loop[0].Y
		if d := float64(dx*dx) + float64(dy*dy); k == 0 || d > farD {
			far, farD = k, d
		}
	}
	a := dp(loop[:far+1], tol)
	rest := append(append([]Point(nil), loop[far:]...), loop[0])
	b := dp(rest, tol)
	out := append(append([]Point(nil), a[:len(a)-1]...), b[:len(b)-1]...)
	if len(out) >= 3 {
		return out
	}
	return append([]Point(nil), loop...)
}

func orient(px, py, qx, qy, rx, ry float64) float64 {
	return float64((qx-px)*(ry-py)) - float64((qy-py)*(rx-px))
}

func segsCross(ax, ay, bx, by, cx, cy, dx, dy float64) bool {
	d1 := orient(ax, ay, bx, by, cx, cy)
	d2 := orient(ax, ay, bx, by, dx, dy)
	d3 := orient(cx, cy, dx, dy, ax, ay)
	d4 := orient(cx, cy, dx, dy, bx, by)
	return (d1 > 0) != (d2 > 0) && (d3 > 0) != (d4 > 0)
}

func isSimple(pts []Point) bool {
	n := len(pts)
	for a := 0; a < n; a++ {
		p1, p2 := pts[a], pts[(a+1)%n]
		for b := a + 2; b < n; b++ {
			if a == 0 && b == n-1 {
				continue
			}
			q1, q2 := pts[b], pts[(b+1)%n]
			if segsCross(p1.X, p1.Y, p2.X, p2.Y, q1.X, q1.Y, q2.X, q2.Y) {
				return false
			}
		}
	}
	return true
}

// Encode writes an outline file (little-endian): the header "ltuo" (read as the u32 'outl'), u32 terrain outline
// count, u16 version 0x0300, u16 grass outline count, u32 0; then per outline [grass outlines only: u32 grass
// id], 8 f32 from the points' bounding box b0..b1 (centre c = (b0 + b1) / 2 as x, y, 0; radius r = sqrt(r2);
// half-extents e = (b1 - b0) / 2 as x, y, 0; r2 = ex*ex + ey*ey), u32 vertex count and the vertices (f32 x, y, z):
// a triangle strip of the closed polygon p0..p(n-1): p0 twice at z 0, then (p, z 0), (p, z 1) for p0..p(n-1), p0,
// and p0 once more when n is even (the count, a multiple of 4), then p0 twice at z 0. Terrain outlines (grass id
// 0) must come first.
func Encode(outlines []Outline) ([]byte, error) {
	nterr := 0
	for nterr < len(outlines) && outlines[nterr].Grass == 0 {
		nterr++
	}
	for _, o := range outlines[nterr:] {
		if o.Grass == 0 {
			return nil, errors.New("fogmap: terrain outlines (grass id 0) must come before the grass outlines")
		}
	}
	if len(outlines)-nterr > 0xFFFF {
		return nil, errors.New("fogmap: too many grass outlines")
	}
	le := binary.LittleEndian
	out := []byte("ltuo")
	out = le.AppendUint32(out, uint32(nterr))
	out = le.AppendUint16(out, 0x0300)
	out = le.AppendUint16(out, uint16(len(outlines)-nterr))
	out = le.AppendUint32(out, 0)
	var err error
	for _, o := range outlines {
		if o.Grass != 0 {
			out = le.AppendUint32(out, o.Grass)
		}
		if out, err = encodeOutline(out, o.Points); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func encodeOutline(out []byte, in []Point) ([]byte, error) {
	n := len(in)
	pts := make([]Point, n)
	for k, p := range in {
		pts[k] = Point{float64(float32(p.X)), float64(float32(p.Y))}
	}
	if n < 3 {
		return nil, errors.New("fogmap: an outline needs three or more points and no repeated neighbours")
	}
	for k := range pts {
		if pts[k] == pts[(k-1+n)%n] {
			return nil, errors.New("fogmap: an outline needs three or more points and no repeated neighbours")
		}
	}
	bx0, bx1, by0, by1 := pts[0].X, pts[0].X, pts[0].Y, pts[0].Y
	for _, p := range pts[1:] {
		bx0, bx1 = minF(bx0, p.X), maxF(bx1, p.X)
		by0, by1 = minF(by0, p.Y), maxF(by1, p.Y)
	}
	f32 := func(v float64) float64 { return float64(float32(v)) }
	cx, cy := f32(f32(bx0+bx1)*0.5), f32(f32(by0+by1)*0.5)
	ex, ey := f32(f32(bx1-bx0)*0.5), f32(f32(by1-by0)*0.5)
	r2 := f32(float64(ex*ex) + float64(ey*ey))
	r := f32(math.Sqrt(r2))
	le := binary.LittleEndian
	put := func(v float64) { out = le.AppendUint32(out, math.Float32bits(float32(v))) }
	for _, v := range [8]float64{cx, cy, 0, r, ex, ey, 0, r2} {
		put(v)
	}
	ring := append(append([]Point(nil), pts...), pts[0])
	if len(ring)%2 == 1 {
		ring = append(ring, pts[0])
	}
	p0 := pts[0]
	out = le.AppendUint32(out, uint32(4+2*len(ring)))
	vert := func(p Point, z float64) { put(p.X); put(p.Y); put(z) }
	vert(p0, 0)
	vert(p0, 0)
	for _, p := range ring {
		vert(p, 0)
		vert(p, 1)
	}
	vert(p0, 0)
	vert(p0, 0)
	return out, nil
}
