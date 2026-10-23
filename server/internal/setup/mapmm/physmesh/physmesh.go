// Package physmesh holds MM_Map_v3's PhysX triangle mesh (levels/MM_Map_v3/MM_Map_v3.bin): the mesh made from its
// line-of-sight map byte for byte as the Python reference does (Build), its cooking input (DGMI) and the cooked mesh
// itself, shipped as data (Cooked, ForMap): cooking needs PhysX's own cooker, so the developers cook it once with
// their physcook tool (gms/physcook) and the setup checks the player's map against the cooking input.
//
// The mesh is a floor over the whole map (top at FloorZ, in tiles of FloorTile x FloorTile cells, also under the
// walls) and walls over every blocked cell: their tops at WallTop merged into rectangles (row runs merged with the
// same run of the rows above), their side faces towards open cells from WallBottom to WallTop merged along the
// grid lines, normals outwards. The map's outer edge has no side faces.
package physmesh

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"dawngate/server/internal/setup/mapmm/los"
)

// The mesh's heights (metres) and the floor's tile size (cells): the shipped SM_Map_v5.bin's floor top and wall
// tops.
const (
	FloorZ     = 0.088
	FloorTile  = 32
	WallBottom = 0.0
	WallTop    = 3.05
)

// Mesh is a triangle mesh: vertices (x, y, z in world units, the map centred on the origin, z up) and triangles
// (counter-clockwise seen from the front).
type Mesh struct {
	Verts [][3]float64
	Tris  [][3]uint32
}

type rect struct{ i0, j0, i1, j1 int } // cells i0..i1-1 x j0..j1-1

// rectangles returns the cells whose value is set as rectangles, each row's runs merged with the same run of the
// rows above, ordered by their first row and then their first column.
func rectangles(cls []bool, w, h int) []rect {
	type run struct{ a, b int }
	var out []rect
	active := map[run]int{}
	for j := 0; j <= h; j++ {
		runs := map[run]bool{}
		if j < h {
			for i := 0; i < w; {
				if cls[j*w+i] {
					k := i
					for k < w && cls[j*w+k] {
						k++
					}
					runs[run{i, k}] = true
					i = k
				} else {
					i++
				}
			}
		}
		next := map[run]int{}
		for r, j0 := range active {
			if runs[r] {
				next[r] = j0
			} else {
				out = append(out, rect{r.a, j0, r.b, j})
			}
		}
		for r := range runs {
			if _, ok := next[r]; !ok {
				next[r] = j
			}
		}
		active = next
	}
	// (first row, first column) is unique: two runs that start in one row are disjoint
	sort.Slice(out, func(a, b int) bool {
		if out[a].j0 != out[b].j0 {
			return out[a].j0 < out[b].j0
		}
		return out[a].i0 < out[b].i0
	})
	return out
}

// segment is a side face: on the vertical grid line g between cell columns g-1 and g over rows a0..a1-1 (x), or
// on the horizontal line between rows g-1 and g over columns a0..a1-1 (y), facing +1 (the open cell on the high
// side) or -1.
type segment struct {
	y         bool
	g, a0, a1 int
	face      int
}

func wallSegments(blocked []bool, w, h int) []segment {
	var segs []segment
	faceOf := func(low bool) int {
		if low {
			return 1
		}
		return -1
	}
	for gx := 1; gx < w; gx++ {
		for j := 0; j < h; {
			left, right := blocked[j*w+gx-1], blocked[j*w+gx]
			if left == right {
				j++
				continue
			}
			face := faceOf(left)
			k := j
			for k < h && blocked[k*w+gx-1] != blocked[k*w+gx] && faceOf(blocked[k*w+gx-1]) == face {
				k++
			}
			segs = append(segs, segment{false, gx, j, k, face})
			j = k
		}
	}
	for gy := 1; gy < h; gy++ {
		for i := 0; i < w; {
			low, high := blocked[(gy-1)*w+i], blocked[gy*w+i]
			if low == high {
				i++
				continue
			}
			face := faceOf(low)
			k := i
			for k < w && blocked[(gy-1)*w+k] != blocked[gy*w+k] && faceOf(blocked[(gy-1)*w+k]) == face {
				k++
			}
			segs = append(segs, segment{true, gy, i, k, face})
			i = k
		}
	}
	return segs
}

type builder struct {
	w, h   int
	cx, cy float64
	mesh   Mesh
	index  map[vkey]uint32
}

type vkey struct {
	g, k int
	z    float64
}

// gx is grid line g (0..w) in world x: cell i spans lines i and i+1, its centre at (i - w/2) * cx.
func (b *builder) gx(g int) float64 { return (float64(g) - 0.5 - float64(b.w)/2) * b.cx }
func (b *builder) gy(g int) float64 { return (float64(g) - 0.5 - float64(b.h)/2) * b.cy }

func (b *builder) v(g, k int, z float64) uint32 {
	key := vkey{g, k, z}
	if n, ok := b.index[key]; ok {
		return n
	}
	n := uint32(len(b.mesh.Verts))
	b.index[key] = n
	b.mesh.Verts = append(b.mesh.Verts, [3]float64{b.gx(g), b.gy(k), z})
	return n
}

func (b *builder) quad(p, q, r, s uint32) { // counter-clockwise seen from the front
	b.mesh.Tris = append(b.mesh.Tris, [3]uint32{p, q, r}, [3]uint32{p, r, s})
}

func (b *builder) top(r rect, z float64) {
	b.quad(b.v(r.i0, r.j0, z), b.v(r.i1, r.j0, z), b.v(r.i1, r.j1, z), b.v(r.i0, r.j1, z))
}

func (b *builder) wall(s segment, zb, zt float64) {
	var p, q, r, t uint32
	if !s.y { // the line x = gx(g) from row a0 to a1
		p, q, r, t = b.v(s.g, s.a0, zb), b.v(s.g, s.a1, zb), b.v(s.g, s.a1, zt), b.v(s.g, s.a0, zt)
	} else { // the line y = gy(g) from column a0 to a1; +1 faces +y with the corners from a1 back to a0
		p, q, r, t = b.v(s.a1, s.g, zb), b.v(s.a0, s.g, zb), b.v(s.a0, s.g, zt), b.v(s.a1, s.g, zt)
	}
	if s.face > 0 {
		b.quad(p, q, r, t)
	} else {
		b.quad(q, p, t, r)
	}
}

func floorTiles(w, h, tile int) []rect {
	var out []rect
	for j := 0; j < h; j += tile {
		for i := 0; i < w; i += tile {
			out = append(out, rect{i, j, min(i+tile, w), min(j+tile, h)})
		}
	}
	return out
}

// Build makes the floor and walls of an 8-bit line-of-sight map whose cells span sx x sy world units (vertices in
// the order of first use: the floor tiles, the wall tops, the side faces).
func Build(img los.Image, sx, sy float64) (Mesh, error) {
	w, h := img.W, img.H
	if w < 1 || h < 1 || len(img.Pix) < w*h {
		return Mesh{}, fmt.Errorf("physmesh: a %d x %d map needs %d bytes, it has %d", w, h, w*h, len(img.Pix))
	}
	blocked := make([]bool, w*h)
	for k := range blocked {
		blocked[k] = img.Pix[k] != 0
	}
	b := &builder{w: w, h: h, cx: sx / float64(w), cy: sy / float64(h), index: map[vkey]uint32{}}
	for _, r := range floorTiles(w, h, FloorTile) {
		b.top(r, FloorZ)
	}
	for _, r := range rectangles(blocked, w, h) {
		b.top(r, WallTop)
	}
	for _, s := range wallSegments(blocked, w, h) {
		b.wall(s, WallBottom, WallTop)
	}
	return b.mesh, nil
}

// ConvexEdgeThreshold is the cooking input's NxTriangleMeshDesc::convexEdgeThreshold.
const ConvexEdgeThreshold = 0.001

// DGMI is the mesh as physcook.exe's input (little-endian): "DGMI", u32 version 1, u32 vertex count, u32 triangle
// count, u32 flags (1: per-triangle materials follow), f32 convex edge threshold; the vertices (3 x f32), the
// triangles (3 x u32 indices) and the materials (u16 per triangle, all 0 here).
func (m Mesh) DGMI() []byte {
	le := binary.LittleEndian
	out := make([]byte, 0, 24+12*len(m.Verts)+14*len(m.Tris))
	out = append(out, "DGMI"...)
	out = le.AppendUint32(out, 1)
	out = le.AppendUint32(out, uint32(len(m.Verts)))
	out = le.AppendUint32(out, uint32(len(m.Tris)))
	out = le.AppendUint32(out, 1)
	out = le.AppendUint32(out, math.Float32bits(ConvexEdgeThreshold))
	for _, v := range m.Verts {
		for _, c := range v {
			out = le.AppendUint32(out, math.Float32bits(float32(c)))
		}
	}
	for _, t := range m.Tris {
		for _, c := range t {
			out = le.AppendUint32(out, c)
		}
	}
	for range m.Tris {
		out = le.AppendUint16(out, 0)
	}
	return out
}
