package ground

// Authored heights: a map's wall shapes may set their plateau's height and its chasm shapes their depth (the map
// compiler's terrain.shapes[k].height). Without them every wall is a Plateau-high plateau and every chasm drops
// Plateau metres, and the ground is byte for byte the ground made before heights existed (Spec.Relief nil).
//
// The relief at a point d metres past the walkable ground's edge (Field's signed distance; d <= 0 is the walkable
// floor, always z 0 but for the fords' dips), with h the blended height there (Relief.Top) and hi = CliffEnd(h):
//
//	z = h * smooth((d - CliffLo) / (hi - CliffLo))    the cliff from CliffLo past the edge up to the plateau
//	    + roll hills past hi + RollFrom                walls only; as before heights
//	a chasm's point (Spec.Down): -(the same cliff term), no hills
//
// smooth is the smoothstep 3t^2 - 2t^3 on t clamped to [0, 1]. The cliff band widens with the height, so a taller
// wall's cliff keeps the 5-m wall's slope (at most about 65 degrees) instead of turning into a vertical face the
// tile grid (two line-of-sight cells a vertex) can't draw: CliffEnd(h) = CliffLo + (CliffHi - CliffLo) * max(1,
// h / Plateau). A lower wall keeps the 3.5-m band (a gentler slope).
//
// The blend: Relief.Top is, per kind (wall or chasm), the mean of that kind's authored heights over the square of
// cells within HeightBlend metres of each cell (cells of the other kinds don't count; none of the kind there:
// Plateau), sampled bilinearly between the cell centres as Field.At samples the distance. So where two wall shapes
// of different heights meet the plateau ramps over about 2 * HeightBlend metres instead of stepping, and the walkable
// ground beside a wall never lowers it.

import "math"

// The authored heights' limits and the relief's constants (metres).
const (
	CliffLo     = cliffLo // the cliff rises from this far past the walkable edge, whatever the height
	CliffHi     = cliffHi // to this far for a wall of Plateau or lower (CliffEnd)
	RollFrom    = 1.5     // the plateau's hills start this far past the cliff's end (6 m for a 5-m wall)
	MinHeight   = 1.0     // a wall's plateau or a chasm's depth: at least this
	MaxHeight   = 40.0    // and at most this
	HeightBlend = 4.0     // heights of one kind blend over the cells within this distance (a square)
)

// CliffEnd is where a cliff of height h (positive: a wall's plateau or a chasm's depth) ends, past the walkable edge.
func CliffEnd(h float64) float64 {
	return cliffLo + float64((cliffHi-cliffLo)*math.Max(1, h/Plateau))
}

// Relief is the authored heights, blended (the package comment above): Wall and Chasm hold, at each of the W x H
// cell centres (laid out as Field's), the wall plateau's height and the chasm's depth there (both positive metres).
type Relief struct {
	W, H        int
	Scale       float64 // cells per metre
	Wall, Chasm []float64
}

// NewRelief blends the w x h cells' authored tops spanning size metres: top > 0 a wall cell's plateau height, < 0 a
// chasm cell's depth, 0 any other cell (walkable ground, a blocking prop's footprint).
func NewRelief(w, h int, size float64, top []float64) *Relief {
	r := &Relief{W: w, H: h, Scale: float64(w) / size}
	rad := max(0, int(math.Round(HeightBlend*r.Scale)))
	r.Wall = blendKind(w, h, rad, top, 1)
	r.Chasm = blendKind(w, h, rad, top, -1)
	return r
}

// blendKind is the mean of sign * top over the cells where sign * top > 0 within rad cells (a square), Plateau where
// there are none: a row pass, then a column pass, each summing its window directly (so a uniform height stays exact).
func blendKind(w, h, rad int, top []float64, sign float64) []float64 {
	sum, cnt := make([]float64, w*h), make([]float64, w*h)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			var s, c float64
			for q := max(0, i-rad); q <= min(w-1, i+rad); q++ {
				if v := sign * top[j*w+q]; v > 0 {
					s += v
					c++
				}
			}
			sum[j*w+i], cnt[j*w+i] = s, c
		}
	}
	out := make([]float64, w*h)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			var s, c float64
			for q := max(0, j-rad); q <= min(h-1, j+rad); q++ {
				s += sum[q*w+i]
				c += cnt[q*w+i]
			}
			out[j*w+i] = Plateau
			if c > 0 {
				out[j*w+i] = s / c
			}
		}
	}
	return out
}

// Top is the blended plateau height (down false) or chasm depth (down true) at (x, y): bilinear between the cell
// centres, clamped to the grid.
func (r *Relief) Top(x, y float64, down bool) float64 {
	v := r.Wall
	if down {
		v = r.Chasm
	}
	fi := pmin(pmax(float64(r.W)/2+float64(x*r.Scale), 0.0), float64(r.W)-1.0)
	fj := pmin(pmax(float64(r.H)/2+float64(y*r.Scale), 0.0), float64(r.H)-1.0)
	i0, j0 := min(int(fi), r.W-2), min(int(fj), r.H-2)
	tx, ty := fi-float64(i0), fj-float64(j0)
	top := float64(v[j0*r.W+i0]*(1-tx)) + float64(v[j0*r.W+i0+1]*tx)
	bot := float64(v[(j0+1)*r.W+i0]*(1-tx)) + float64(v[(j0+1)*r.W+i0+1]*tx)
	return float64(top*(1-ty)) + float64(bot*ty)
}

// cliff is the relief at (x, y), d metres past the walkable edge: the height h (positive) of the plateau or the
// chasm there, where its cliff ends (hi) and whether the ground drops (a chasm). Without authored heights h is
// Plateau and hi CliffHi everywhere.
func cliff(d, x, y float64, s *Spec) (h, hi float64, down bool) {
	h, hi = Plateau, cliffHi
	down = d > 0 && s.Down != nil && s.Down(x, y)
	if d > 0 && s.Relief != nil {
		h = s.Relief.Top(x, y, down)
		hi = CliffEnd(h)
	}
	return h, hi, down
}

// CliffBand is cliff's h and hi for another ground writer (the baked ground's "high" rule).
func CliffBand(d, x, y float64, s *Spec) (h, hi float64) {
	h, hi, _ = cliff(d, x, y, s)
	return h, hi
}
