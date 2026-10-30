package bake

import (
	"fmt"
	"math"
	"runtime"
	"sync"
)

// Grid is a scalar field over the ground (a mask, 0..1): N x N values over the square [-Half, Half]^2, value (i, j)
// at the centre of its cell, rows from the south.
type Grid struct {
	N    int
	Half float64
	V    []float32
}

// NewGrid evaluates f at every cell centre (rows in parallel; f must be safe for that).
func NewGrid(n int, half float64, f func(x, y float64) float64) *Grid {
	g := &Grid{N: n, Half: half, V: make([]float32, n*n)}
	rows(n, func(j int) {
		y := g.coord(j)
		for i := 0; i < n; i++ {
			g.V[j*n+i] = float32(f(g.coord(i), y))
		}
	})
	return g
}

func (g *Grid) coord(i int) float64 {
	return -g.Half + float64(float64(float64(i)+0.5)*(2*g.Half)/float64(g.N))
}

// At is bilinear between cell centres, the edge cells continuing outside.
func (g *Grid) At(x, y float64) float64 {
	n := float64(g.N)
	fx := float64((x+g.Half)*n)/(2*g.Half) - 0.5
	fy := float64((y+g.Half)*n)/(2*g.Half) - 0.5
	fx = math.Min(math.Max(fx, 0), n-1)
	fy = math.Min(math.Max(fy, 0), n-1)
	i0, j0 := min(int(fx), g.N-2), min(int(fy), g.N-2)
	tx, ty := fx-float64(i0), fy-float64(j0)
	v := func(i, j int) float64 { return float64(g.V[j*g.N+i]) }
	top := lerp(v(i0, j0), v(i0+1, j0), tx)
	bot := lerp(v(i0, j0+1), v(i0+1, j0+1), tx)
	return lerp(top, bot, ty)
}

// Layer is one tileable texture drawn through a mask: Scale metres per repeat, turned by Rot (radians), its colour
// times Tint and varied in brightness by Vary (0..1, over 23-m noise), at Opacity where the mask is 1. The first
// layer of a bake is the ground under all others (its mask is not read).
type Layer struct {
	Tex     *Image
	Scale   float64
	Rot     float64
	Tint    [3]float64
	Vary    float64
	Opacity float64
	Mask    *Grid // nil: everywhere
	Seed    int
	Break   float64 // 0..1: how much a second, larger and turned copy breaks up the repeats
}

// The second copy's turn (35 degrees) for Layer.Break.
const (
	breakCos = 0.8191520442889918
	breakSin = 0.573576436351046
)

// Stamp is a picture laid onto the ground: its centre, its size in metres (along its own x and y), turned by Rot
// (radians, counter-clockwise; the picture's top towards +y when 0), its alpha times Opacity, its colour times Tint;
// Soft metres of its border fade out (Round: the ellipse inside the rectangle instead of the rectangle).
type Stamp struct {
	Img     *Image
	Centre  [2]float64
	Size    [2]float64
	Rot     float64
	Opacity float64
	Tint    [3]float64
	Soft    float64
	Round   bool
}

// Spec is a bake: the ground's square [-Half, Half]^2 cut into Tiles x Tiles tiles of Texels x Texels texels, each
// with Margin texels of the neighbours' ground around its own (so the tiles meet without a seam whatever the
// sampler does at their edges), and what is painted.
type Spec struct {
	Half          float64
	Tiles, Texels int
	Margin        int
	Layers        []Layer
	Stamps        []Stamp

	// Base (optional) is a painted ground under the layers: one Texels x Texels picture per tile, k = tj*Tiles + ti
	// (west to east, then south to north), each laid out as Tile writes it (margins included; JoinMargins makes
	// them agree). With a Base every layer, the first too, is drawn over it through its mask.
	Base []*Image
}

// MarginFor is the margin a tile of texels gets: 1/256 of it, at least 2 (8 texels of 2048).
func MarginFor(texels int) int { return max(2, texels/256) }

func (s *Spec) check() error {
	if s.Tiles < 1 || s.Texels < 16 || s.Texels%4 != 0 || s.Margin < 0 || 2*s.Margin >= s.Texels || !(s.Half > 0) {
		return fmt.Errorf("bake: %d x %d tiles of %d texels, margin %d, half %g", s.Tiles, s.Tiles, s.Texels, s.Margin, s.Half)
	}
	if len(s.Layers) == 0 && s.Base == nil {
		return fmt.Errorf("bake: no layer")
	}
	if s.Base != nil {
		if len(s.Base) != s.Tiles*s.Tiles {
			return fmt.Errorf("bake: %d base pictures for %d x %d tiles", len(s.Base), s.Tiles, s.Tiles)
		}
		for k, im := range s.Base {
			if im == nil || im.W != s.Texels || im.H != s.Texels {
				return fmt.Errorf("bake: base picture %d is not %d x %d", k, s.Texels, s.Texels)
			}
		}
	}
	return nil
}

// TileSize is a tile's side in metres; TexelSize a texel's.
func (s *Spec) TileSize() float64 { return 2 * s.Half / float64(s.Tiles) }

// TexelSize is one texel's side in metres.
func (s *Spec) TexelSize() float64 { return s.TileSize() / float64(s.Texels-2*s.Margin) }

// TileOrigin is tile (ti, tj)'s west and north edges (ti from the west, tj from the south).
func (s *Spec) TileOrigin(ti, tj int) (x0, y1 float64) {
	w := s.TileSize()
	return -s.Half + float64(float64(ti)*w), -s.Half + float64(float64(tj+1)*w)
}

// UV is world point (x, y)'s texture coordinate in tile (ti, tj) (D3D's: u east, v south, 0..1 over the texture).
func (s *Spec) UV(ti, tj int, x, y float64) (u, v float64) {
	x0, y1 := s.TileOrigin(ti, tj)
	inner, t := float64(s.Texels-2*s.Margin), float64(s.Texels)
	w := s.TileSize()
	u = (float64(s.Margin) + float64((x-x0)*inner)/w) / t
	v = (float64(s.Margin) + float64((y1-y)*inner)/w) / t
	return u, v
}

// Colour is the painted ground at (x, y) for a sample texel metres wide (the textures' mip level follows it): the
// first layer, the others over it by mask and opacity, then the stamps by alpha. RGB 0..255.
// With a Base the ground under the layers is the base picture's.
func (s *Spec) Colour(x, y, texel float64) [3]float64 {
	var c [3]float64
	if s.Base != nil {
		c = s.baseAt(x, y, texel)
	}
	return s.over(c, x, y, texel)
}

// baseAt is the base pictures' colour at (x, y): the tile holding the point, sampled trilinearly at the footprint.
func (s *Spec) baseAt(x, y, texel float64) [3]float64 {
	w := s.TileSize()
	ti := clampInt(int(math.Floor((x+s.Half)/w)), 0, s.Tiles-1)
	tj := clampInt(int(math.Floor((y+s.Half)/w)), 0, s.Tiles-1)
	u, v := s.UV(ti, tj, x, y)
	p := s.Base[tj*s.Tiles+ti].Sample(u, v, texel/s.TexelSize(), false)
	return [3]float64{p[0], p[1], p[2]}
}

// over draws the layers and the stamps over c (without a Base the first layer is the ground and c is not read).
func (s *Spec) over(c [3]float64, x, y, texel float64) [3]float64 {
	base := s.Base == nil // the first layer is the ground
	for k := range s.Layers {
		l := &s.Layers[k]
		a := 1.0
		if k > 0 || !base {
			a = l.Opacity
			if l.Mask != nil {
				a = float64(a * l.Mask.At(x, y))
			}
			if a <= 0 {
				continue
			}
		}
		t := l.sample(x, y, texel)
		if k == 0 && base {
			c = t
			continue
		}
		for q := range c {
			c[q] = lerp(c[q], t[q], a)
		}
	}
	for k := range s.Stamps {
		st := &s.Stamps[k]
		p, a := st.sample(x, y, texel)
		if a <= 0 {
			continue
		}
		for q := range c {
			c[q] = lerp(c[q], p[q], a)
		}
	}
	return c
}

func (l *Layer) sample(x, y, texel float64) [3]float64 {
	cs, sn := math.Cos(l.Rot), math.Sin(l.Rot)
	rx := float64(x*cs) + float64(y*sn)
	ry := float64(-x*sn) + float64(y*cs)
	u, v := rx/l.Scale, -ry/l.Scale
	p := l.Tex.Sample(u, v, float64(texel*float64(l.Tex.W))/l.Scale, true)
	if l.Break > 0 {
		// a second copy, larger and turned, shows through where slow noise says, so the repeats don't line up
		w := float64(l.Break * Smooth((Noise(x, y, float64(l.Scale*2.3), 57+l.Seed)-0.3)/0.4))
		if w > 0 {
			s2 := float64(l.Scale * 1.37)
			bx := float64(rx*breakCos) + float64(ry*breakSin)
			by := float64(-rx*breakSin) + float64(ry*breakCos)
			q := l.Tex.Sample(bx/s2+0.37, -by/s2+0.61, float64(texel*float64(l.Tex.W))/s2, true)
			for k := range p {
				p[k] = lerp(p[k], q[k], w)
			}
		}
	}
	f := 1.0
	if l.Vary > 0 {
		f = 1 + float64(l.Vary*(float64(2*Noise(x, y, 23, 31+l.Seed))-1))
	}
	return [3]float64{float64(p[0]*l.Tint[0]) * f, float64(p[1]*l.Tint[1]) * f, float64(p[2]*l.Tint[2]) * f}
}

// sample is the stamp's colour and alpha at (x, y) (alpha 0 outside it).
func (st *Stamp) sample(x, y, texel float64) ([3]float64, float64) {
	dx, dy := x-st.Centre[0], y-st.Centre[1]
	if math.Abs(dx) > st.Size[0]+st.Size[1] || math.Abs(dy) > st.Size[0]+st.Size[1] {
		return [3]float64{}, 0
	}
	cs, sn := math.Cos(st.Rot), math.Sin(st.Rot)
	lx := float64(dx*cs) + float64(dy*sn)
	ly := float64(-dx*sn) + float64(dy*cs)
	s, t := lx/st.Size[0]+0.5, 0.5-ly/st.Size[1]
	if s < 0 || s > 1 || t < 0 || t > 1 {
		return [3]float64{}, 0
	}
	edge := 1.0
	if st.Round {
		ex, ey := float64(2*s)-1, float64(2*t)-1
		r := math.Sqrt(float64(ex*ex) + float64(ey*ey))
		if r >= 1 {
			return [3]float64{}, 0
		}
		if st.Soft > 0 {
			edge = Smooth(float64((1-r)*math.Min(st.Size[0], st.Size[1])/2) / st.Soft)
		}
	} else if st.Soft > 0 {
		d := math.Min(math.Min(float64(s*st.Size[0]), float64((1-s)*st.Size[0])),
			math.Min(float64(t*st.Size[1]), float64((1-t)*st.Size[1])))
		edge = Smooth(d / st.Soft)
	}
	p := st.Img.Sample(s, t, float64(texel*float64(st.Img.W))/st.Size[0], false)
	a := float64(float64(p[3]/255)*st.Opacity) * edge
	return [3]float64{float64(p[0] * st.Tint[0]), float64(p[1] * st.Tint[1]), float64(p[2] * st.Tint[2])}, a
}

// Tile bakes tile (ti, tj): Texels x Texels RGBA (rows top to bottom, north up, alpha 255).
func (s *Spec) Tile(ti, tj int) ([]byte, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	if ti < 0 || tj < 0 || ti >= s.Tiles || tj >= s.Tiles {
		return nil, fmt.Errorf("bake: no tile %d, %d", ti, tj)
	}
	n := s.Texels
	x0, y1 := s.TileOrigin(ti, tj)
	texel := s.TexelSize()
	out := make([]byte, 4*n*n)
	var base []byte
	if s.Base != nil {
		base = s.Base[tj*s.Tiles+ti].Top()
	}
	rows(n, func(r int) {
		y := y1 - float64(float64(float64(r)+0.5-float64(s.Margin))*texel)
		for c := 0; c < n; c++ {
			x := x0 + float64(float64(float64(c)+0.5-float64(s.Margin))*texel)
			o := 4 * (r*n + c)
			if base != nil { // the base picture's own texel, exactly: an exported tile imported unchanged bakes the same
				put(out[o:], s.over([3]float64{float64(base[o]), float64(base[o+1]), float64(base[o+2])}, x, y, texel))
			} else {
				put(out[o:], s.Colour(x, y, texel))
			}
		}
	})
	return out, nil
}

// JoinMargins makes tiles x tiles pictures of texels x texels RGBA (k = tj*tiles + ti, rows top to bottom, as Tile
// writes them) agree where they overlap: each margin texel that lies inside a neighbour's own square is copied from
// that neighbour's texel at the same place (the tiles' texel grids line up), so a picture painted tile by tile has
// no seam; margin texels outside the ground (the outer border) keep their own. The pictures are changed in place.
func JoinMargins(tiles, texels, margin int, rgba [][]byte) error {
	if len(rgba) != tiles*tiles {
		return fmt.Errorf("bake: %d pictures for %d x %d tiles", len(rgba), tiles, tiles)
	}
	for k, p := range rgba {
		if len(p) != 4*texels*texels {
			return fmt.Errorf("bake: picture %d has %d bytes, not %d x %d RGBA", k, len(p), texels, texels)
		}
	}
	inner := texels - 2*margin
	if margin <= 0 || inner <= 0 {
		return nil
	}
	// owner says which tile's own square holds global texel g (counted from the west, or from the north) and where
	// in that tile's picture it is; ok is false outside the ground.
	owner := func(g int) (t, local int, ok bool) {
		if g < 0 || g >= tiles*inner {
			return 0, 0, false
		}
		return g / inner, g%inner + margin, true
	}
	// only margin texels are written and only own-square texels read, so the order doesn't matter
	for tj := 0; tj < tiles; tj++ {
		for ti := 0; ti < tiles; ti++ {
			dst := rgba[tj*tiles+ti]
			row := tiles - 1 - tj // the tile's row counted from the north
			for r := 0; r < texels; r++ {
				inR := r >= margin && r < texels-margin
				for c := 0; c < texels; c++ {
					if inR && c >= margin && c < texels-margin {
						c = texels - margin - 1 // the own square: skip to its east margin
						continue
					}
					oc, lc, okc := owner(ti*inner + c - margin)
					or, lr, okr := owner(row*inner + r - margin)
					if !okc || !okr || (oc == ti && or == row) {
						continue
					}
					from := rgba[(tiles-1-or)*tiles+oc]
					copy(dst[4*(r*texels+c):4*(r*texels+c)+4], from[4*(lr*texels+lc):4*(lr*texels+lc)+4])
				}
			}
		}
	}
	return nil
}

// Picture is the painted ground over the square [x0, x0 + side] x [y0, y0 + side] as n x n RGBA (north up).
func (s *Spec) Picture(n int, x0, y0, side float64) []byte {
	texel := side / float64(n)
	out := make([]byte, 4*n*n)
	rows(n, func(r int) {
		y := y0 + side - float64(float64(float64(r)+0.5)*texel)
		for c := 0; c < n; c++ {
			x := x0 + float64(float64(float64(c)+0.5)*texel)
			put(out[4*(r*n+c):], s.Colour(x, y, texel))
		}
	})
	return out
}

func put(p []byte, c [3]float64) {
	for q := 0; q < 3; q++ {
		p[q] = byte(clampInt(roundInt(c[q]), 0, 255))
	}
	p[3] = 255
}

// rows runs f for every row 0..n-1, spread over the processors (each row is written by one call only, so the result
// doesn't depend on the order).
func rows(n int, f func(r int)) {
	workers := min(runtime.GOMAXPROCS(0), n)
	if workers <= 1 {
		for r := 0; r < n; r++ {
			f(r)
		}
		return
	}
	var wg sync.WaitGroup
	next := make(chan int, n)
	for r := 0; r < n; r++ {
		next <- r
	}
	close(next)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range next {
				f(r)
			}
		}()
	}
	wg.Wait()
}
