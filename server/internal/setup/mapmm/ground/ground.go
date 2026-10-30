// Package ground builds MM_Map_v3's ground: the terrain model its terrain noun names (no build ships one, so the
// client draws no ground) and its two textures, from the map's line-of-sight image. It matches the Python
// reference byte for byte.
//
// The model is a heightfield over the map: the open cells (where units walk) a flat floor at z 0, the blocked
// cells a plateau 5 m up, joined by a cliff that rises from 1 m to 4.5 m past the line-of-sight edge; low hills
// roll on the plateau and a shallow dip marks the water plane at the map's centre. It is 25 tiles of 64 x 64 cells
// (each a mesh and a part), the triangles split by material: a dirt floor and cliff, a grass plateau. Its tree,
// its vertex layout (32 bytes: position, normal, tangent, UV, colour) and its two one-texture vertex-lit
// materials come from a shipped terrain model, the template. The vertex colours shade the cliff and the plateau
// and tint the two bases. The dirt texture is procedural (periodic value noise), the grass texture the shipped
// grass texture's pixels re-encoded. All three are DXT1 with the shipped mip chain.
//
// The float arithmetic follows the reference's evaluation order; products are rounded before they are summed
// (the explicit float64 conversions keep the compiler from fusing them).
package ground

import (
	"encoding/binary"
	"fmt"
	"math"

	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/mapmm/los"
)

// The archive entries the build writes, and where its game inputs ship.
const (
	ModelEntry = "Environments/prototype/mm_v3_terrain.bmdl" // the terrain noun's model path
	DirtEntry  = "environments/prototype/textures/mm_v3_dirt_D.dds"
	GrassEntry = "environments/prototype/textures/mm_v3_grass_D.dds"

	TemplateArchive = "anshavar4.snap"
	TemplateEntry   = "environments/anshavar/sm_v5_terrain.bmdl"
	GrassArchive    = "anshavar3.snap"
	GrassSource     = "environments/anshavar/textures/grass_seamless_01_D.dds"
)

// The shape (metres) and the mesh layout.
const (
	Plateau  = 5.0 // the blocked cells' height
	cliffLo  = 1.0 // the cliff rises from this far past the line-of-sight edge
	cliffHi  = 4.5 // to this far
	roll     = 0.6 // the plateau's low hills
	Grid     = 320 // vertices span Grid cells of two line-of-sight cells
	Tile     = 64  // cells per mesh side
	uvCells  = 8   // a texture repeat spans 8 cells, mirrored
	DirtSize = 512
)

var dirtRGB = [3]float64{124, 96, 64}

// The two bases' tints: the Mortal base blue, the Spirit base red.
var baseTints = [2][3]float64{{0.84, 0.91, 1.0}, {1.0, 0.86, 0.82}}

// Spec is what differs between maps: the level's size (metres), the two base ends (their tint fades from 70 to
// 100 m), whether the floor dips at MM_Map_v3's water plane, and Down, the ground that drops instead of rising past
// the edge of the open cells (a chasm; nil: none). Dips are another map's fords: the floor dips under each of them as
// MM_Map_v3's dips under its water plane (Ford stays MM's own, so its ground keeps its bytes). Cut > 0 leaves out
// every triangle whose three corners all lie more than Cut metres past the walkable edge (the ground only where units
// walk, the void around it; 0: the whole square, as shipped). The client clears no colour buffer, so a cut ground
// needs a Backdrop under it, or the void shows the earlier frames.
type Spec struct {
	Size     float64
	Bases    [2][2]float64
	Ford     bool
	Down     func(x, y float64) bool
	Dips     []Dip
	Cut      float64
	Backdrop *Backdrop
	Mist     *Backdrop // a second plane, drawn after the Backdrop (a see-through layer above it)
}

// Backdrop goes with Cut (void.go): a flat plane at height Z (negative: below the floor) from
// -Half to Half on both axes, one more mesh on a third material (a copy of the dirt's with Texture), its UVs
// mirrored with a texture repeat of RepeatX x RepeatY metres, its vertex colour white. Flags, when set, replaces the
// material's flags (the dirt's are 0x106; a test of which bit makes it receive the sun's shadow). A Material
// without textures (labsFogFace) is copied as it is and Texture is not used.
type Backdrop struct {
	Z, Half, RepeatX, RepeatY float64
	Texture                   string
	Flags                     *uint32
	Material                  *bmdl.Node // copied instead of the dirt's material when set (its first texture set to Texture)
}

const backdropCells = 64 // the backdrop's grid (65 x 65 vertices)

// Dip is a ford: a rectangle (its centre, the cosine and sine of its turn, its half sides along the turned x and y)
// where the walkable floor dips by Depth (negative, metres), fading in over DipEdge metres inside its edges and over
// 2 m from the walkable ground's edge, as MM_Map_v3's floor under its water plane. Where dips overlap, the deepest
// counts.
type Dip struct {
	X, Y, Cos, Sin, HalfW, HalfL, Depth float64
}

// DipEdge is a dip's fade inside its rectangle (MM_Map_v3's ford's).
const DipEdge = fordEdge

func (p Dip) at(x, y float64) float64 {
	dx, dy := x-p.X, y-p.Y
	lx := float64(dx*p.Cos) + float64(dy*p.Sin)
	ly := float64(-dx*p.Sin) + float64(dy*p.Cos)
	e := pmin(p.HalfW-math.Abs(lx), p.HalfL-math.Abs(ly))
	if e <= 0 {
		return 0
	}
	return float64(p.Depth * smooth(e/DipEdge))
}

// dipAt is the deepest dip at (x, y) (0 outside them all).
func dipAt(dips []Dip, x, y float64) float64 {
	z := 0.0
	for _, p := range dips {
		z = pmin(z, p.at(x, y))
	}
	return z
}

// MM is MM_Map_v3's spec.
var MM = Spec{Size: los.Size, Bases: [2][2]float64{{-206.0, -206.0}, {206.0, 206.0}}, Ford: true}

const baseIn, baseOut = 70.0, 100.0 // full tint inside, none past

// The water plane: a 30 x 60 m rectangle at (-0.88, 0.88) turned 45 degrees; the floor dips 0.55 m inside it.
const (
	fordX, fordY = -0.880542, 0.880542
	fordW, fordL = 30.0, 60.0
	fordDepth    = -0.55
	fordEdge     = 4.0
	// cos and sin of 45 degrees as the reference's C library returns them
	cos45 = 0x1.6a09e667f3bcdp-1
	sin45 = 0x1.6a09e667f3bcdp-1
)

// ---------------------------------------------------------------- the distance field

// edt1d is Felzenszwalb and Huttenlocher's squared distance transform of one row (f: 0 at sources).
func edt1d(f []float64) []float64 {
	n := len(f)
	d := make([]float64, n)
	v := make([]int, n)
	z := make([]float64, n+1)
	k := 0
	z[0], z[1] = math.Inf(-1), math.Inf(1)
	for q := 1; q < n; q++ {
		p := v[k]
		s := ((f[q] + float64(q*q)) - (f[p] + float64(p*p))) / (2.0 * float64(q-p))
		for s <= z[k] {
			k--
			p = v[k]
			s = ((f[q] + float64(q*q)) - (f[p] + float64(p*p))) / (2.0 * float64(q-p))
		}
		k++
		v[k] = q
		z[k] = s
		z[k+1] = math.Inf(1)
	}
	k = 0
	for q := 0; q < n; q++ {
		for z[k+1] < float64(q) {
			k++
		}
		p := v[k]
		d[q] = float64((q-p)*(q-p)) + f[p]
	}
	return d
}

// edt is the Euclidean distance (cells) from every cell to the nearest cell where mask is true.
func edt(mask []bool, w, h int) []float64 {
	const big = 1e20
	cols := make([][]float64, w)
	col := make([]float64, h)
	for i := 0; i < w; i++ {
		for j := 0; j < h; j++ {
			col[j] = big
			if mask[j*w+i] {
				col[j] = 0
			}
		}
		cols[i] = edt1d(col)
	}
	out := make([]float64, w*h)
	row := make([]float64, w)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			row[i] = cols[i][j]
		}
		r := edt1d(row)
		for i := 0; i < w; i++ {
			if r[i] < big {
				out[j*w+i] = math.Sqrt(r[i])
			} else {
				out[j*w+i] = 1e9
			}
		}
	}
	return out
}

// Field is the signed distance (m) to the line-of-sight edge: negative on open cells, positive on blocked ones.
// Cell (i, j) holds x = (i - w/2) / Scale, y = (j - h/2) / Scale at its centre; row 0 is y's minimum.
type Field struct {
	W, H               int
	Size, Scale, CellM float64
	D                  []float64
	Blocked            []bool
}

// NewField takes the w x h cells (0 open, anything else blocked) spanning size metres.
func NewField(w, h int, cells []byte, size float64) (*Field, error) {
	if w < 2 || h < 2 || len(cells) < w*h {
		return nil, fmt.Errorf("ground: a %d x %d line-of-sight map with %d cells", w, h, len(cells))
	}
	f := &Field{W: w, H: h, Size: size, Scale: float64(w) / size, CellM: size / float64(w)}
	blocked := make([]bool, w*h)
	open := make([]bool, w*h)
	nb := 0
	for k := range blocked {
		blocked[k] = cells[k] != 0
		open[k] = !blocked[k]
		if blocked[k] {
			nb++
		}
	}
	if nb == 0 || nb == w*h {
		return nil, fmt.Errorf("ground: the line-of-sight map needs open and blocked cells")
	}
	toOpen, toBlocked := edt(open, w, h), edt(blocked, w, h)
	half := 0.5 * f.CellM
	f.D = make([]float64, w*h)
	for k := range f.D {
		if blocked[k] {
			f.D[k] = float64(toOpen[k]*f.CellM) - half
		} else {
			f.D[k] = -(float64(toBlocked[k]*f.CellM) - half)
		}
	}
	f.Blocked = blocked
	return f, nil
}

// At is bilinear between cell centres; outside the image the nearest edge cell plus the distance beyond it.
func (f *Field) At(x, y float64) float64 {
	fi := float64(f.W)/2 + float64(x*f.Scale)
	fj := float64(f.H)/2 + float64(y*f.Scale)
	ci := pmin(pmax(fi, 0.0), float64(f.W)-1.0)
	cj := pmin(pmax(fj, 0.0), float64(f.H)-1.0)
	i0, j0 := min(int(ci), f.W-2), min(int(cj), f.H-2)
	tx, ty := ci-float64(i0), cj-float64(j0)
	d, w := f.D, f.W
	top := float64(d[j0*w+i0]*(1-tx)) + float64(d[j0*w+i0+1]*tx)
	bot := float64(d[(j0+1)*w+i0]*(1-tx)) + float64(d[(j0+1)*w+i0+1]*tx)
	v := float64(top*(1-ty)) + float64(bot*ty)
	beyond := float64(hypot(fi-ci, fj-cj) * f.CellM)
	if v > 0 {
		return v + beyond
	}
	return v
}

// ---------------------------------------------------------------- the heightfield

func smooth(t float64) float64 {
	t = pmin(pmax(t, 0.0), 1.0)
	return float64(t*t) * (3 - float64(2*t))
}

// lattice hashes an integer lattice point to [0, 1).
func lattice(ix, iy, seed int) float64 {
	n := uint32(int64(ix)*374761393 + int64(iy)*668265263 + int64(seed)*2147483647)
	n = (n ^ n>>13) * 1274126177
	return float64((n^n>>16)&0xFFFF) / 65536.0
}

// valueNoise is smooth value noise with lattice spacing period; wrap > 0 lattice points make it periodic.
func valueNoise(x, y, period float64, seed, wrap int) float64 {
	gx, gy := x/period, y/period
	fix, fiy := math.Floor(gx), math.Floor(gy)
	ix, iy := int(fix), int(fiy)
	fx, fy := smooth(gx-fix), smooth(gy-fiy)
	g := func(a, b int) float64 {
		if wrap != 0 {
			a, b = imod(a, wrap), imod(b, wrap)
		}
		return lattice(a, b, seed)
	}
	top := float64(g(ix, iy)*(1-fx)) + float64(g(ix+1, iy)*fx)
	bot := float64(g(ix, iy+1)*(1-fx)) + float64(g(ix+1, iy+1)*fx)
	return float64(top*(1-fy)) + float64(bot*fy)
}

func fordMask(x, y float64) float64 {
	dx, dy := x-fordX, y-fordY
	lx := float64(dx*cos45) + float64(dy*sin45)
	ly := float64(-dx*sin45) + float64(dy*cos45)
	ex := fordW/2 - math.Abs(lx)
	ey := fordL/2 - math.Abs(ly)
	return smooth(pmin(ex, ey) / fordEdge)
}

func height(d, x, y float64, s *Spec) float64 {
	z := float64(Plateau * smooth((d-cliffLo)/(cliffHi-cliffLo)))
	if d > 0 && s.Down != nil && s.Down(x, y) {
		return -z
	}
	if d > 6.0 {
		z += float64(float64(roll*(float64(2*valueNoise(x, y, 23.0, 7, 0))-1)) * smooth((d-6.0)/8.0))
	}
	if d < 0 && s.Ford {
		z += float64(float64(fordDepth*fordMask(x, y)) * smooth(-d/2.0))
	}
	if d < 0 && len(s.Dips) > 0 {
		z += float64(dipAt(s.Dips, x, y) * smooth(-d/2.0))
	}
	return z
}

// colour is the vertex colour (RGB 0..1): the shade by zone, times the base tint.
func colour(d, z, x, y float64, s *Spec) [3]float64 {
	var c float64
	switch {
	case d <= cliffLo: // the floor, shaded towards the cliff's foot
		c = 1.0 - float64(0.28*smooth((d+4.0)/(4.0+cliffLo)))
	case d < cliffHi:
		c = 0.72 - float64(0.30*pmin(pmax(z/Plateau, 0.0), 1.0))
	default:
		c = 0.60 * (0.88 + float64(0.12*valueNoise(x, y, 9.0, 3, 0)))
	}
	rgb := [3]float64{c, c, c}
	for t, b := range s.Bases {
		f := 1.0 - smooth((hypot(x-b[0], y-b[1])-baseIn)/(baseOut-baseIn))
		if f > 0 {
			for k := range rgb {
				rgb[k] = rgb[k] * ((1 - f) + float64(f*baseTints[t][k]))
			}
		}
	}
	return rgb
}

// triWave: 0 -> 1 -> 0 over two units, mirrored texture repeats with every UV in [0, 1].
func triWave(t float64) float64 {
	t = fmod(t, 2.0)
	if t <= 1.0 {
		return t
	}
	return 2.0 - t
}

// Ground is the vertex grid: positions, normals and colours of (Grid + 1)^2 vertices.
type Ground struct {
	Grid, N int
	Cell    float64
	Cut     float64     // Spec.Cut
	Backs   []*Backdrop // Spec.Backdrop and Spec.Mist, the ones set, each on its own material after the ground's two
	X       []float64   // the vertex coordinates along both axes
	D, Z    []float64
	Normal  [][3]float64
	RGB     [][3]float64
}

// NewGround samples the field on grid x grid cells of cell metres (cell <= 0: two line-of-sight cells), centred
// on the origin.
func NewGround(f *Field, grid int, cell float64) *Ground {
	return NewGroundSpec(f, grid, cell, MM)
}

// NewGroundSpec is NewGround for another map's spec.
func NewGroundSpec(f *Field, grid int, cell float64, spec Spec) *Ground {
	if cell <= 0 {
		cell = 2 * f.CellM
	}
	half, n := grid/2, grid+1
	g := &Ground{Grid: grid, N: n, Cell: cell, Cut: spec.Cut, X: make([]float64, n), D: make([]float64, n*n),
		Z: make([]float64, n*n), Normal: make([][3]float64, n*n), RGB: make([][3]float64, n*n)}
	for _, b := range []*Backdrop{spec.Backdrop, spec.Mist} {
		if b != nil {
			g.Backs = append(g.Backs, b)
		}
	}
	for i := range g.X {
		g.X[i] = float64(i-half) * cell
	}
	for j := 0; j < n; j++ {
		y := g.X[j]
		for i := 0; i < n; i++ {
			x := g.X[i]
			d := f.At(x, y)
			g.D[j*n+i] = d
			g.Z[j*n+i] = height(d, x, y, &spec)
		}
	}
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			k := j*n + i
			zx := (g.Z[j*n+min(i+1, n-1)] - g.Z[j*n+max(i-1, 0)]) / float64(float64(min(i+1, n-1)-max(i-1, 0))*cell)
			zy := (g.Z[min(j+1, n-1)*n+i] - g.Z[max(j-1, 0)*n+i]) / float64(float64(min(j+1, n-1)-max(j-1, 0))*cell)
			ln := math.Sqrt(float64(zx*zx) + float64(zy*zy) + 1.0)
			g.Normal[k] = [3]float64{-zx / ln, -zy / ln, 1.0 / ln}
			g.RGB[k] = colour(g.D[k], g.Z[k], g.X[i], g.X[j], &spec)
		}
	}
	return g
}

func (g *Ground) uv(i, j int) [2]float64 {
	return [2]float64{triWave(float64(i) / uvCells), triWave(float64(j) / uvCells)}
}

// material: 0 dirt (floor and cliff), 1 grass (the plateau's top).
func (g *Ground) material(ks [3]int) int {
	m := g.Z[ks[0]]
	for _, k := range ks[1:] {
		if g.Z[k] < m {
			m = g.Z[k]
		}
	}
	if m >= 0.75*Plateau {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- the model

func q8(v float64) byte {
	return byte(clampInt(roundInt((v+1.0)*127), 0, 254))
}

// encodeVertex: the 32-byte layout, FLOAT3 position, UBYTE4N normal and tangent, FLOAT2 UV, UBYTE4N colour.
func encodeVertex(out []byte, pos [3]float64, nrm [3]float64, uv [2]float64, rgb [3]float64) []byte {
	tx, tz := 1.0, 0.0 // +x along the surface
	if nrm[2] != 0 {
		tz = -nrm[0] / nrm[2]
	}
	ln := math.Sqrt(float64(tx*tx) + float64(tz*tz))
	tx, tz = tx/ln, tz/ln
	le := binary.LittleEndian
	for _, p := range pos {
		out = le.AppendUint32(out, math.Float32bits(float32(p)))
	}
	out = append(out, q8(nrm[0]), q8(nrm[1]), q8(nrm[2]), 0, q8(tx), 127, q8(tz), 0)
	for _, u := range uv {
		out = le.AppendUint32(out, math.Float32bits(float32(u)))
	}
	for _, c := range rgb {
		out = append(out, byte(clampInt(roundInt(c*255), 0, 255)))
	}
	return append(out, 0xFF)
}

type sub struct{ mat, start, count int }

// tileMesh is one tile's vertex buffer, positions, submeshes (sorted by material) and indices.
func tileMesh(g *Ground, ti, tj int) ([]byte, [][3]float64, []sub, []int) {
	n := g.N
	i0, j0 := ti*Tile, tj*Tile
	i1, j1 := min(i0+Tile, g.Grid), min(j0+Tile, g.Grid)
	rowLen := i1 - i0 + 1
	local := func(k int) int { return (k/n-j0)*rowLen + (k%n - i0) }
	vb := make([]byte, 0, 32*rowLen*(j1-j0+1))
	var pts [][3]float64
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			k := j*n + i
			p := [3]float64{g.X[i], g.X[j], g.Z[k]}
			pts = append(pts, p)
			vb = encodeVertex(vb, p, g.Normal[k], g.uv(i, j), g.RGB[k])
		}
	}
	var byMat [2][][3]int
	for j := j0; j < j1; j++ {
		for i := i0; i < i1; i++ {
			a, b, c, d := j*n+i, j*n+i+1, (j+1)*n+i+1, (j+1)*n+i
			// split along the (+1, +1) diagonal, parallel to the lane; counter-clockwise seen from +z
			for _, t := range [2][3]int{{a, b, c}, {a, c, d}} {
				if g.Cuts(t) {
					continue
				}
				m := g.material(t)
				byMat[m] = append(byMat[m], [3]int{local(t[0]), local(t[1]), local(t[2])})
			}
		}
	}
	if len(byMat[0])+len(byMat[1]) == 0 {
		byMat[0] = [][3]int{{0, 0, 0}} // a tile Cut emptied: one degenerate triangle keeps its mesh (draws nothing)
	}
	var idx []int
	var subs []sub
	for m := 0; m < 2; m++ {
		if len(byMat[m]) > 0 {
			subs = append(subs, sub{m, len(idx), 3 * len(byMat[m])})
			for _, t := range byMat[m] {
				idx = append(idx, t[0], t[1], t[2])
			}
		}
	}
	return vb, pts, subs, idx
}

// backdropMesh is the Backdrop's vertex buffer, positions and indices.
func backdropMesh(b *Backdrop) ([]byte, [][3]float64, []int) {
	n := backdropCells + 1
	step := 2 * b.Half / backdropCells
	var vb []byte
	var pts [][3]float64
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			x, y := -b.Half+float64(i)*step, -b.Half+float64(j)*step
			p := [3]float64{x, y, b.Z}
			pts = append(pts, p)
			uv := [2]float64{triWave((x + b.Half) / b.RepeatX), triWave((y + b.Half) / b.RepeatY)}
			vb = encodeVertex(vb, p, [3]float64{0, 0, 1}, uv, [3]float64{1, 1, 1})
		}
	}
	var idx []int
	for j := 0; j < backdropCells; j++ {
		for i := 0; i < backdropCells; i++ {
			a, b2, c, d := j*n+i, j*n+i+1, (j+1)*n+i+1, (j+1)*n+i
			idx = append(idx, a, b2, c, a, c, d)
		}
	}
	return vb, pts, idx
}

func f32(x float64) float64 { return float64(float32(x)) }

// boundsOf: centre, radius, half extents, radius squared, in the build tool's float32 steps.
func boundsOf(pts [][3]float64, withOrigin bool) []float32 {
	if withOrigin {
		pts = append(pts[:len(pts):len(pts)], [3]float64{})
	}
	if len(pts) == 0 {
		return make([]float32, 8)
	}
	lo, hi := pts[0], pts[0]
	for _, p := range pts[1:] {
		for k := 0; k < 3; k++ {
			if p[k] < lo[k] {
				lo[k] = p[k]
			}
			if p[k] > hi[k] {
				hi[k] = p[k]
			}
		}
	}
	var h, c [3]float64
	for k := 0; k < 3; k++ {
		h[k] = f32(f32(hi[k]-lo[k]) * 0.5)
		c[k] = f32(lo[k] + h[k])
	}
	r2 := f32(f32(float64(h[0]*h[0])+float64(h[1]*h[1])) + float64(h[2]*h[2]))
	return []float32{float32(c[0]), float32(c[1]), float32(c[2]), float32(math.Sqrt(r2)),
		float32(h[0]), float32(h[1]), float32(h[2]), float32(r2)}
}

func child(n *bmdl.Node, name string) (*bmdl.Node, error) {
	c, ok := n.F[name].(*bmdl.Node)
	if !ok {
		return nil, fmt.Errorf("ground: the template has no %s", name)
	}
	return c, nil
}

func children(n *bmdl.Node, name string, want int) ([]*bmdl.Node, error) {
	c, ok := n.F[name].([]*bmdl.Node)
	if !ok || len(c) < want {
		return nil, fmt.Errorf("ground: the template has fewer than %d %s", want, name)
	}
	return c, nil
}

// BuildModel is the .bmdl: the template's tree with two of its vertex-lit materials (their texture paths set to
// textures) and one mesh and part per tile of the ground.
func BuildModel(template []byte, g *Ground, textures [2]string) ([]byte, error) {
	tr, err := bmdl.Parse(template)
	if err != nil {
		return nil, err
	}
	tm, err := child(tr.Root, "model")
	if err != nil {
		return nil, err
	}
	tmats, err := children(tm, "materials", 2)
	if err != nil {
		return nil, err
	}
	tmeshes, err := children(tm, "meshes", 2)
	if err != nil {
		return nil, err
	}
	tparts, err := children(tm, "parts", 2)
	if err != nil {
		return nil, err
	}
	tmesh, tpart := tmeshes[1], tparts[1]
	if decl, ok := tmesh.F["decl"].([]byte); !ok || bmdl.DeclStride(decl) != 32 {
		return nil, fmt.Errorf("ground: template mesh 1 is not the 32-byte layout")
	}
	tsubs, err := children(tpart, "submeshes", 1)
	if err != nil {
		return nil, err
	}
	var mats []*bmdl.Node
	for k, path := range textures {
		mt := tmats[k].Clone()
		texs, ok := mt.F["textures"].([]*bmdl.Node)
		if sh, _ := mt.F["shader"].(string); sh != "labsVertLit" || !ok || len(texs) != 1 {
			return nil, fmt.Errorf("ground: template material is not a one-texture labsVertLit")
		}
		texs[0].F["path"], texs[0].F["path_hash"] = path, bmdl.FNV1(path)
		mats = append(mats, mt)
	}
	for _, b := range g.Backs {
		mats = append(mats, BackdropMaterial(b, mats[0])) // void.go
	}
	var meshes, parts []*bmdl.Node
	var every [][3]float64
	tiles := (g.Grid + Tile - 1) / Tile
	for tj := 0; tj < tiles; tj++ {
		for ti := 0; ti < tiles; ti++ {
			vb, pts, subs, idx := tileMesh(g, ti, tj)
			if len(pts) > 65535 {
				return nil, fmt.Errorf("ground: tile with %d vertices", len(pts))
			}
			if len(subs) == 0 {
				return nil, fmt.Errorf("ground: an empty tile")
			}
			me := tmesh.Clone()
			name := fmt.Sprintf("mm_v3_ground_%d_%d", ti, tj)
			ib := make([]byte, 2*len(idx))
			for q, v := range idx {
				binary.LittleEndian.PutUint16(ib[2*q:], uint16(v))
			}
			me.F["name"], me.F["name_hash"] = name, bmdl.FNV1(name)
			me.F["material"] = uint32(subs[0].mat)
			me.F["vertices"], me.F["indices"] = vb, ib
			me.F["vertex_count"], me.F["index_count"] = uint32(len(pts)), uint32(len(idx))
			me.F["bounds"] = boundsOf(pts, false)
			me.F["target_count"], me.F["targets"] = uint32(0), []*bmdl.Node{}
			part := tpart.Clone()
			part.F["mesh"] = uint32(len(meshes))
			part.F["bounds"] = boundsOf(pts, true)
			var sl []*bmdl.Node
			for _, s := range subs {
				sm := tsubs[0].Clone()
				sm.F["index_start"], sm.F["index_count"], sm.F["material"] = uint32(s.start), uint32(s.count), uint32(s.mat)
				sp := make([][3]float64, s.count)
				for q := range sp {
					sp[q] = pts[idx[s.start+q]]
				}
				sm.F["bounds"] = boundsOf(sp, false)
				sl = append(sl, sm)
			}
			part.F["submeshes"] = sl
			meshes = append(meshes, me)
			parts = append(parts, part)
			every = append(every, pts...)
		}
	}
	for k, b := range g.Backs {
		name := "mm_v3_backdrop"
		if k > 0 {
			name = fmt.Sprintf("mm_v3_backdrop_%d", k)
		}
		me, part, pts := BackdropPart(b, name, uint32(2+k), uint32(len(meshes)), tmesh, tpart, tsubs[0]) // void.go
		meshes = append(meshes, me)
		parts = append(parts, part)
		every = append(every, pts...)
	}
	model := tm.Clone()
	model.F["name"], model.F["name_hash"] = "mm_v3_terrain", bmdl.FNV1("mm_v3_terrain")
	model.F["materials"], model.F["meshes"], model.F["parts"] = mats, meshes, parts
	model.F["bounds"] = boundsOf(every, false)
	root := &bmdl.Node{Type: "Root", F: map[string]any{}}
	for k, v := range tr.Root.F {
		root.F[k] = v
	}
	root.F["model"], root.F["skeleton"], root.F["anims"], root.F["anim_count"] = model, nil, []*bmdl.Node{}, uint32(0)
	return bmdl.Write(&bmdl.Tree{Tag: tr.Tag, Version: tr.Version, Align: tr.Align, Root: root})
}

// ModelInfo is what CheckModel counts.
type ModelInfo struct {
	Meshes, Triangles int
	Bounds            []float32
}

// CheckModel parses a model back: the round trip byte for byte, every index in range, two materials, the
// submeshes covering the indices.
func CheckModel(raw []byte) (ModelInfo, error) {
	tr, err := bmdl.Parse(raw)
	if err != nil {
		return ModelInfo{}, err
	}
	again, err := bmdl.Write(tr)
	if err != nil {
		return ModelInfo{}, err
	}
	if string(again) != string(raw) {
		return ModelInfo{}, fmt.Errorf("ground: the model does not round-trip")
	}
	m, err := child(tr.Root, "model")
	if err != nil {
		return ModelInfo{}, err
	}
	mats, _ := m.F["materials"].([]*bmdl.Node)
	meshes, _ := m.F["meshes"].([]*bmdl.Node)
	parts, _ := m.F["parts"].([]*bmdl.Node)
	if len(mats) < 2 || len(mats) > 4 || len(meshes) != len(parts) {
		return ModelInfo{}, fmt.Errorf("ground: materials or parts wrong")
	}
	tris := 0
	for pi, me := range meshes {
		part := parts[pi]
		nIdx, nVert := int(bmdl.U32(me.F["index_count"])), int(bmdl.U32(me.F["vertex_count"]))
		ib, _ := me.F["indices"].([]byte)
		if nIdx == 0 || len(ib) < 2*nIdx {
			return ModelInfo{}, fmt.Errorf("ground: mesh %d has no indices", pi)
		}
		top := 0
		for q := 0; q < nIdx; q++ {
			top = max(top, int(binary.LittleEndian.Uint16(ib[2*q:])))
		}
		if top >= nVert || int(bmdl.U32(part.F["mesh"])) != pi {
			return ModelInfo{}, fmt.Errorf("ground: mesh %d: index out of range", pi)
		}
		subs, _ := part.F["submeshes"].([]*bmdl.Node)
		cover := 0
		for _, s := range subs {
			cover += int(bmdl.U32(s.F["index_count"]))
		}
		if cover != nIdx {
			return ModelInfo{}, fmt.Errorf("ground: mesh %d: submeshes do not cover the indices", pi)
		}
		tris += nIdx / 3
	}
	b, _ := m.F["bounds"].([]float32)
	return ModelInfo{Meshes: len(meshes), Triangles: tris, Bounds: b}, nil
}

// ---------------------------------------------------------------- the textures

// DirtPixels is the periodic earth texture (RGBA, size x size): an earth brown varied by four octaves of value
// noise, a warm/cool shift, darker grains and lighter pebbles.
func DirtPixels(size int) []byte {
	out := make([]byte, size*size*4)
	s := float64(size)
	octaves := [4]struct {
		period float64
		wrap   int
		amp    float64
	}{{s / 4, 4, 0.10}, {s / 16, 16, 0.08}, {s / 64, 64, 0.06}, {s / 128, 128, 0.05}}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x), float64(y)
			v := 0.0
			for _, o := range octaves {
				v += float64(o.amp * (float64(2*valueNoise(fx, fy, o.period, 11+o.wrap, o.wrap)) - 1))
			}
			warm := float64(0.06 * (float64(2*valueNoise(fx, fy, s/8, 5, 8)) - 1))
			grain := lattice(x, y, 19)
			f := 1.0 + v
			if grain > 0.985 {
				f *= 1.18 // pebbles
			} else if grain < 0.02 {
				f *= 0.80
			}
			px := [3]float64{dirtRGB[0] * (f + warm), dirtRGB[1] * f, dirtRGB[2] * (f - warm)}
			o := (y*size + x) * 4
			for c := 0; c < 3; c++ {
				out[o+c] = byte(clampInt(int(px[c]), 0, 255))
			}
			out[o+3] = 0xFF
		}
	}
	return out
}

// DirtDDS is the dirt texture as a DXT1 .dds.
func DirtDDS(size int) ([]byte, error) {
	return dds.Encode(size, size, DirtPixels(size), "dxt1", 0)
}

// GrassDDS re-encodes a .dds's top level as DXT1 with the default mip chain.
func GrassDDS(src []byte) ([]byte, error) {
	w, h, px, err := dds.Decode(src)
	if err != nil {
		return nil, err
	}
	return dds.Encode(w, h, px, "dxt1", 0)
}

// ---------------------------------------------------------------- build

// Build makes the three entries (ModelEntry, DirtEntry, GrassEntry) from the line-of-sight map, the template model
// (TemplateEntry) and the shipped grass texture (GrassSource).
func Build(img los.Image, template, grassDDS []byte) (model, dirtDDS, grassOut []byte, err error) {
	return BuildSpec(img, MM, template, grassDDS, [2]string{DirtEntry, GrassEntry})
}

// BuildSpec is Build for another map: its spec and the two textures' paths, which the model names.
func BuildSpec(img los.Image, spec Spec, template, grassDDS []byte, textures [2]string) (model, dirtDDS,
	grassOut []byte, err error) {
	if img.Bpp != 8 {
		return nil, nil, nil, fmt.Errorf("ground: an 8-bit line-of-sight map is expected, this one has %d bits", img.Bpp)
	}
	field, err := NewField(img.W, img.H, img.Pix, spec.Size)
	if err != nil {
		return nil, nil, nil, err
	}
	g := NewGroundSpec(field, Grid, 0, spec)
	if dirtDDS, err = DirtDDS(DirtSize); err != nil {
		return nil, nil, nil, err
	}
	if grassOut, err = GrassDDS(grassDDS); err != nil {
		return nil, nil, nil, err
	}
	if model, err = BuildModel(template, g, textures); err != nil {
		return nil, nil, nil, err
	}
	if _, err = CheckModel(model); err != nil {
		return nil, nil, nil, err
	}
	return model, dirtDDS, grassOut, nil
}

// FromArchives runs Build on the game's archives: images.snap (the line-of-sight map), TemplateArchive and
// GrassArchive.
func FromArchives(images, templateArchive, grassArchive []byte) (model, dirtDDS, grassOut []byte, err error) {
	raw, err := los.FromArchive(images, los.Entry)
	if err != nil {
		return nil, nil, nil, err
	}
	img, err := los.Read(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	tmpl, err := los.FromArchive(templateArchive, TemplateEntry)
	if err != nil {
		return nil, nil, nil, err
	}
	grass, err := los.FromArchive(grassArchive, GrassSource)
	if err != nil {
		return nil, nil, nil, err
	}
	return Build(img, tmpl, grass)
}
