package navgraph

import (
	"math"
	"math/big"
	"sort"
	"strconv"
)

// Constants of the derived values.
const (
	// KDPad moves a kd split plane outwards from its subtree's extreme coordinate (as every shipped node).
	KDPad = 0.0002
	// ClusterMax is the largest cluster (the shipped graphs' largest).
	ClusterMax = 45
	// ClusterEdge is edge flags1 bit 15: the edge joins two clusters.
	ClusterEdge = 0x8000
	// The new areas' flags: island 0x3FFFF in flags1, flags2's low bits as shipped.
	areaFlags1 = 0x01FFFF80
	areaFlags2 = 0x00110000
	edgeFlags1 = 0xFFFF0000
)

// Polygon is a convex polygon, counter-clockwise seen from +z, of [x, y, z] vertices.
type Polygon [][3]float64

// f32 rounds to single precision (the file's floats), returning the value as a float64.
func f32(x float64) float64 { return float64(float32(x)) }

// The Python reference computes in IEEE doubles; the helpers below reproduce its library functions exactly.
// Products are wrapped in float64() so that no compiler fuses them into an FMA.

// norm is the correctly rounded Euclidean norm sqrt(sum v[i]^2) (the reference's hypot and dist are correctly
// rounded for these inputs): the sum of squares exactly, then the double whose rounding interval holds its root.
func norm(v ...float64) float64 {
	nz, last := 0, 0.0
	for _, x := range v {
		if x != 0 {
			nz++
			last = x
		}
	}
	switch nz {
	case 0:
		return 0
	case 1:
		return math.Abs(last)
	}
	const prec = 1200
	var s, t big.Float
	s.SetPrec(prec)
	t.SetPrec(prec)
	for _, x := range v {
		t.SetFloat64(x)
		t.Mul(&t, &t)
		s.Add(&s, &t)
	}
	approx, _ := s.Float64()
	c := math.Sqrt(approx)
	for i := 0; i < 8; i++ {
		up := math.Nextafter(c, math.Inf(1))
		if k := midSqCmp(c, up, &s); k < 0 || (k == 0 && odd(c)) {
			c = up
			continue
		}
		dn := math.Nextafter(c, 0)
		if k := midSqCmp(c, dn, &s); k > 0 || (k == 0 && odd(c)) {
			c = dn
			continue
		}
		break
	}
	return c
}

func odd(x float64) bool { return math.Float64bits(x)&1 == 1 }

// midSqCmp compares ((a+b)/2)^2 with s exactly.
func midSqCmp(a, b float64, s *big.Float) int {
	var m, x big.Float
	m.SetPrec(64).SetFloat64(a)
	x.SetPrec(64).SetFloat64(b)
	m.Add(&m, &x)
	m.Quo(&m, big.NewFloat(2))
	var sq big.Float
	sq.SetPrec(160).Mul(&m, &m)
	return sq.Cmp(s)
}

// round3 is the reference's round(x, 3): the correctly rounded 3-decimal value (ties to even), as a double;
// the zero's sign dropped (the reference's keys compare -0.0 equal to 0.0).
func round3(x float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 3, 64), 64)
	if v == 0 {
		return 0
	}
	return v
}

// pyFloorDiv is the reference's float floor division a // b (its fmod-based rule, not floor(a / b)).
func pyFloorDiv(vx, wx float64) float64 {
	mod := math.Mod(vx, wx)
	div := (vx - mod) / wx
	if mod != 0 {
		if (wx < 0) != (mod < 0) {
			div -= 1.0
		}
	}
	if div != 0 {
		fd := math.Floor(div)
		if div-fd > 0.5 {
			fd += 1.0
		}
		return fd
	}
	return math.Copysign(0, vx/wx)
}

// pySum is the reference's sum() of doubles from the integer start 0: Neumaier's compensated summation.
func pySum(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	f := 0 + xs[0]
	if f == 0 {
		f = 0 // the integer start turns -0.0 into 0.0
	}
	c := 0.0
	for _, x := range xs[1:] {
		t := f + x
		if math.Abs(f) >= math.Abs(x) {
			c += (f - t) + x
		} else {
			c += (x - t) + f
		}
		f = t
	}
	if c != 0 && !math.IsInf(c, 0) && !math.IsNaN(c) {
		f += c
	}
	return f
}

func verts(ar *Area) [][3]float64 {
	vs := make([][3]float64, len(ar.Edges))
	for i, e := range ar.Edges {
		vs[i] = [3]float64{float64(e.Pos[0]), float64(e.Pos[1]), float64(e.Pos[2])}
	}
	return vs
}

func meanZ(vs [][3]float64) float64 {
	zs := make([]float64, len(vs))
	for i, v := range vs {
		zs[i] = v[2]
	}
	return pySum(zs) / float64(len(vs))
}

// polygonCentroid is the area centroid of the polygon in the plane and the vertices' mean height (the vertex
// mean for a degenerate polygon).
func polygonCentroid(vs [][3]float64) [3]float64 {
	var a, cx, cy float64
	n := len(vs)
	for i := 0; i < n; i++ {
		x0, y0 := vs[i][0], vs[i][1]
		x1, y1 := vs[(i+1)%n][0], vs[(i+1)%n][1]
		c := float64(x0*y1) - float64(x1*y0)
		a += c
		cx += float64((x0 + x1) * c)
		cy += float64((y0 + y1) * c)
	}
	if math.Abs(a) < 1e-12 {
		xs, ys := make([]float64, n), make([]float64, n)
		for i, v := range vs {
			xs[i], ys[i] = v[0], v[1]
		}
		return [3]float64{pySum(xs) / float64(n), pySum(ys) / float64(n), meanZ(vs)}
	}
	a *= 0.5
	return [3]float64{cx / float64(6*a), cy / float64(6*a), meanZ(vs)}
}

func tri2(a, b, c [3]float64) float64 {
	return math.Abs(float64((b[0]-a[0])*(c[1]-a[1])) - float64((b[1]-a[1])*(c[0]-a[0])))
}

// basisVertex is flags2's bits 24-31: the vertex k >= 2 spanning the largest triangle with vertices 0 and 1
// (the first one on a tie).
func basisVertex(vs [][3]float64) int {
	best, bv := 2, tri2(vs[0], vs[1], vs[2])
	for k := 3; k < len(vs); k++ {
		if v := tri2(vs[0], vs[1], vs[k]); v > bv {
			best, bv = k, v
		}
	}
	return best
}

func edgeCost(a, b *Area) uint32 {
	d := norm(float64(a.Pos[0])-float64(b.Pos[0]), float64(a.Pos[1])-float64(b.Pos[1]),
		float64(a.Pos[2])-float64(b.Pos[2]))
	return uint32(math.Ceil(d * 500))
}

// Clusters groups the areas into connected clusters of at most limit areas, breadth first from the first
// unassigned area; it returns each area's cluster number.
func Clusters(g *Graph, limit int) []int {
	idx := areaIndex(g)
	cid := make([]int, len(g.Areas))
	for i := range cid {
		cid[i] = -1
	}
	n := 0
	for a := range g.Areas {
		if cid[a] >= 0 {
			continue
		}
		queue := []int{a}
		cid[a] = n
		size := 1
		for i := 0; i < len(queue) && size < limit; i++ {
			for _, e := range g.Areas[queue[i]].Edges {
				if e.Adj == 0 {
					continue
				}
				if j := idx[e.Adj]; cid[j] < 0 && size < limit {
					cid[j] = n
					queue = append(queue, j)
					size++
				}
			}
		}
		n++
	}
	return cid
}

func areaIndex(g *Graph) map[uint32]int {
	idx := make(map[uint32]int, len(g.Areas))
	for i := range g.Areas {
		idx[g.Areas[i].Offset] = i
	}
	return idx
}

type vkey struct{ x, y float64 }
type ekey struct{ a, b vkey }

// Relink derives everything else from the areas' polygons: the offsets, the adjacency (an edge whose end points
// are another area's edge reversed, compared at 3 decimals), the centres, radii and basis vertices, the edge
// costs, the cluster edges, the bounding box and the kd tree.
func Relink(g *Graph) {
	o := uint32(GraphHeaderSize)
	for i := range g.Areas {
		g.Areas[i].Offset = o
		o += AreaSize + EdgeSize*uint32(len(g.Areas[i].Edges))
	}
	all := make([][][3]float64, len(g.Areas))
	keys := make([][]vkey, len(g.Areas))
	owner := make(map[ekey]int)
	for i := range g.Areas {
		vs := verts(&g.Areas[i])
		all[i] = vs
		ks := make([]vkey, len(vs))
		for k, v := range vs {
			ks[k] = vkey{round3(v[0]), round3(v[1])}
		}
		keys[i] = ks
		for k := range ks {
			owner[ekey{ks[k], ks[(k+1)%len(ks)]}] = i
		}
	}
	for i := range g.Areas {
		ar := &g.Areas[i]
		vs, ks := all[i], keys[i]
		for k := range ar.Edges {
			if other, ok := owner[ekey{ks[(k+1)%len(ks)], ks[k]}]; ok {
				ar.Edges[k].Adj = g.Areas[other].Offset
			} else {
				ar.Edges[k].Adj = 0
			}
		}
		pos := polygonCentroid(vs)
		for c := 0; c < 3; c++ {
			ar.Pos[c] = float32(pos[c])
		}
		px, py := float64(ar.Pos[0]), float64(ar.Pos[1])
		r := math.Inf(-1)
		for _, v := range vs {
			if d := norm(v[0]-px, v[1]-py); d > r {
				r = d
			}
		}
		ar.Radius = float32(r)
		ar.Flags[1] = ar.Flags[1]&0x00FFFFFF | uint32(basisVertex(vs))<<24
	}
	idx := areaIndex(g)
	cid := Clusters(g, ClusterMax)
	for i := range g.Areas {
		ar := &g.Areas[i]
		for k := range ar.Edges {
			e := &ar.Edges[k]
			e.Flags1 &^= ClusterEdge
			if e.Adj == 0 {
				e.Flags2 = 0
				continue
			}
			j := idx[e.Adj]
			e.Flags2 = edgeCost(ar, &g.Areas[j])
			if cid[i] != cid[j] {
				e.Flags1 |= ClusterEdge
			}
		}
	}
	g.BBox = bboxOf(all)
	g.KDBBox = g.BBox
	g.KD = BuildKD(g)
}

func bboxOf(all [][][3]float64) [6]float32 {
	var mn, mx [3]float64
	first := true
	for _, vs := range all {
		for _, v := range vs {
			for c := 0; c < 3; c++ {
				if first || v[c] < mn[c] {
					mn[c] = v[c]
				}
				if first || v[c] > mx[c] {
					mx[c] = v[c]
				}
			}
			first = false
		}
	}
	return [6]float32{float32(mn[0]), float32(mn[1]), float32(mn[2]), float32(mx[0]), float32(mx[1]), float32(mx[2])}
}

type kdBox struct {
	off uint32
	b   [4]float64 // min x, min y, max x, max y
}

// BuildKD builds the kd tree over the areas' 2-D boxes, one area per leaf: each node splits its areas, sorted
// (stably) by their box centres on the axis where the centres spread wider (x on a tie), into the first
// floor(n/2) and the rest; the planes are the left part's largest and the right part's smallest coordinate,
// padded outwards by KDPad.
func BuildKD(g *Graph) *KDNode {
	boxes := make([]kdBox, len(g.Areas))
	for i := range g.Areas {
		vs := verts(&g.Areas[i])
		b := [4]float64{vs[0][0], vs[0][1], vs[0][0], vs[0][1]}
		for _, v := range vs[1:] {
			b[0], b[1] = pyMin(b[0], v[0]), pyMin(b[1], v[1])
			b[2], b[3] = pyMax(b[2], v[0]), pyMax(b[3], v[1])
		}
		boxes[i] = kdBox{g.Areas[i].Offset, b}
	}
	return kdRec(boxes)
}

func kdRec(items []kdBox) *KDNode {
	if len(items) == 1 {
		return &KDNode{Leaf: true, Area: items[0].off}
	}
	spread := func(axis int) float64 {
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, it := range items {
			c := (it.b[axis] + it.b[2+axis]) / 2
			lo, hi = pyMin(lo, c), pyMax(hi, c)
		}
		return hi - lo
	}
	axis := 1
	if spread(0) >= spread(1) {
		axis = 0
	}
	s := make([]kdBox, len(items))
	copy(s, items)
	sort.SliceStable(s, func(i, j int) bool {
		return (s[i].b[axis]+s[i].b[2+axis])/2 < (s[j].b[axis]+s[j].b[2+axis])/2
	})
	h := len(s) / 2
	left, right := s[:h], s[h:]
	lmax, rmin := math.Inf(-1), math.Inf(1)
	for _, it := range left {
		lmax = pyMax(lmax, it.b[2+axis])
	}
	for _, it := range right {
		rmin = pyMin(rmin, it.b[axis])
	}
	return &KDNode{Axis: uint32(axis), LMax: float32(lmax + KDPad), RMin: float32(rmin - KDPad),
		Left: kdRec(left), Right: kdRec(right)}
}

// FromPolygons makes a one-graph image from convex polygons. The header values (version, layer, buildScale,
// voxSize, radius, step, height, up axis, the reserved and constant bytes) and the navset version come from the
// template's first graph, or the shipped defaults when template is nil; everything else is derived (Relink).
func FromPolygons(polys []Polygon, template *Image) *Image {
	img := NewImage()
	g := NewGraph()
	if template != nil && len(template.Graphs) > 0 {
		t := template.Graphs[0]
		g.Version, g.Layer = t.Version, t.Layer
		g.BuildScale, g.VoxSize, g.Radius, g.Step, g.Height = t.BuildScale, t.VoxSize, t.Radius, t.Step, t.Height
		g.UpAxis, g.Reserved, g.Constant = t.UpAxis, t.Reserved, t.Constant
		img.NavsetVersion = template.NavsetVersion
	}
	g.Areas = make([]Area, len(polys))
	for i, p := range polys {
		ar := &g.Areas[i]
		ar.SearchCost = 0xFFFFFFFF
		ar.Flags = [4]uint32{areaFlags1, areaFlags2, 0, 0}
		ar.Edges = make([]Edge, len(p))
		for k, v := range p {
			ar.Edges[k] = Edge{Pos: [3]float32{float32(v[0]), float32(v[1]), float32(v[2])}, Flags1: edgeFlags1}
		}
	}
	Relink(g)
	img.Graphs = append(img.Graphs, g)
	return img
}

// pyMin and pyMax keep the first of equal values (the reference's min and max), unlike math.Min and math.Max
// with signed zeros.
func pyMin(a, b float64) float64 {
	if b < a {
		return b
	}
	return a
}

func pyMax(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}
