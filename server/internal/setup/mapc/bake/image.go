// Package bake paints a map's ground into top-down textures: layers of tileable textures, each drawn through a mask,
// and stamps (pictures placed, turned and scaled) over them, baked into square tiles that the ground model maps from
// above, as the first map's own ground is painted. It also writes that model: the heightfield of the ground package
// with one material per tile and, optionally, a tiled material from the side for the steep faces (the cliffs).
//
// Everything here is plain arithmetic on the inputs: the same inputs give the same bytes on every platform (products
// are rounded before they are summed, so no compiler fuses them).
package bake

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"

	"dawngate/server/internal/setup/dds"
)

// Image is an RGBA picture (rows top to bottom) with its box-filtered mip chain down to 1 x 1.
type Image struct {
	W, H   int
	levels [][]byte
	sizes  [][2]int
}

// NewImage takes w x h RGBA pixels (rows top to bottom).
func NewImage(w, h int, rgba []byte) (*Image, error) {
	if w <= 0 || h <= 0 || len(rgba) != 4*w*h {
		return nil, fmt.Errorf("bake: a %d x %d picture with %d bytes", w, h, len(rgba))
	}
	im := &Image{W: w, H: h}
	cw, ch, cur := w, h, rgba
	for {
		im.levels = append(im.levels, cur)
		im.sizes = append(im.sizes, [2]int{cw, ch})
		if cw == 1 && ch == 1 {
			break
		}
		cw, ch, cur = dds.Downsample(cw, ch, cur)
	}
	return im, nil
}

// Flat is a 4 x 4 picture of one colour (opaque).
func Flat(rgb [3]float64) *Image {
	px := make([]byte, 4*16)
	for k := 0; k < 16; k++ {
		for c := 0; c < 3; c++ {
			px[4*k+c] = byte(clampInt(roundInt(rgb[c]), 0, 255))
		}
		px[4*k+3] = 255
	}
	im, _ := NewImage(4, 4, px)
	return im
}

// Levels is the number of mip levels.
func (im *Image) Levels() int { return len(im.levels) }

// Top is the top level's pixels.
func (im *Image) Top() []byte { return im.levels[0] }

// Cut is the pixel rectangle (x, y, w, h) of the top level as a picture of its own.
func (im *Image) Cut(x, y, w, h int) (*Image, error) {
	if w <= 0 || h <= 0 || x < 0 || y < 0 || x+w > im.W || y+h > im.H {
		return nil, fmt.Errorf("the rectangle %d, %d, %d x %d is not inside the %d x %d picture", x, y, w, h, im.W, im.H)
	}
	px := make([]byte, 4*w*h)
	for r := 0; r < h; r++ {
		copy(px[4*r*w:4*(r+1)*w], im.levels[0][4*((y+r)*im.W+x):4*((y+r)*im.W+x+w)])
	}
	return NewImage(w, h, px)
}

// Repeat is the top level laid out nx x ny times, first halved until each copy is at most max pixels wide and high.
func (im *Image) Repeat(nx, ny, max int) (w, h int, rgba []byte) {
	l := 0
	for l+1 < len(im.levels) && (im.sizes[l][0] > max || im.sizes[l][1] > max) {
		l++
	}
	sw, sh, src := im.sizes[l][0], im.sizes[l][1], im.levels[l]
	w, h = sw*nx, sh*ny
	rgba = make([]byte, 4*w*h)
	for r := 0; r < h; r++ {
		for k := 0; k < nx; k++ {
			copy(rgba[4*(r*w+k*sw):4*(r*w+(k+1)*sw)], src[4*(r%sh)*sw:4*(r%sh+1)*sw])
		}
	}
	return w, h, rgba
}

// at is level l's pixel (x, y), wrapped or clamped.
func (im *Image) at(l, x, y int, wrap bool) [4]float64 {
	w, h := im.sizes[l][0], im.sizes[l][1]
	if wrap {
		x, y = imod(x, w), imod(y, h)
	} else {
		x, y = clampInt(x, 0, w-1), clampInt(y, 0, h-1)
	}
	p := im.levels[l][4*(y*w+x):]
	return [4]float64{float64(p[0]), float64(p[1]), float64(p[2]), float64(p[3])}
}

// bilinear samples level l at (u, v) (0..1 over the picture, v down).
func (im *Image) bilinear(l int, u, v float64, wrap bool) [4]float64 {
	w, h := float64(im.sizes[l][0]), float64(im.sizes[l][1])
	x, y := float64(u*w)-0.5, float64(v*h)-0.5
	fx, fy := math.Floor(x), math.Floor(y)
	tx, ty := x-fx, y-fy
	ix, iy := int(fx), int(fy)
	a, b := im.at(l, ix, iy, wrap), im.at(l, ix+1, iy, wrap)
	c, d := im.at(l, ix, iy+1, wrap), im.at(l, ix+1, iy+1, wrap)
	var out [4]float64
	for k := range out {
		top := lerp(a[k], b[k], tx)
		bot := lerp(c[k], d[k], tx)
		out[k] = lerp(top, bot, ty)
	}
	return out
}

// Sample is the picture at (u, v) (0..1 over the picture, v down; wrap repeats it, else the edge continues) for
// footprint picture pixels per sample: trilinear between the two mip levels that bracket it.
func (im *Image) Sample(u, v, footprint float64, wrap bool) [4]float64 {
	lod := 0.0
	if footprint > 1 {
		lod = math.Log2(footprint)
	}
	top := float64(len(im.levels) - 1)
	if lod >= top {
		return im.bilinear(len(im.levels)-1, u, v, wrap)
	}
	l := int(lod)
	f := lod - float64(l)
	a := im.bilinear(l, u, v, wrap)
	if f == 0 {
		return a
	}
	b := im.bilinear(l+1, u, v, wrap)
	for k := range a {
		a[k] = lerp(a[k], b[k], f)
	}
	return a
}

// DecodePNG reads a PNG as RGBA (rows top to bottom, not premultiplied), at most maxSide pixels a side.
func DecodePNG(b []byte, maxSide int) (*Image, error) {
	w, h, px, err := DecodePNGPixels(b, maxSide)
	if err != nil {
		return nil, err
	}
	return NewImage(w, h, px)
}

// DecodePNGPixels reads a PNG as w x h RGBA pixels (rows top to bottom, not premultiplied), at most maxSide a side.
func DecodePNGPixels(b []byte, maxSide int) (w, h int, rgba []byte, err error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0, 0, nil, err
	}
	if cfg.Width > maxSide || cfg.Height > maxSide {
		return 0, 0, nil, fmt.Errorf("a %d x %d picture, at most %d x %d", cfg.Width, cfg.Height, maxSide, maxSide)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, 0, nil, err
	}
	r := img.Bounds()
	w, h = r.Dx(), r.Dy()
	if px := fastRGBA(img); px != nil {
		return w, h, px, nil
	}
	px := make([]byte, 4*w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(r.Min.X+x, r.Min.Y+y)).(color.NRGBA)
			o := 4 * (y*w + x)
			px[o], px[o+1], px[o+2], px[o+3] = c.R, c.G, c.B, c.A
		}
	}
	return w, h, px, nil
}

// fastRGBA copies the pixels of the two layouts a big PNG usually decodes to (8-bit RGB as opaque RGBA, 8-bit RGBA
// as NRGBA) without a call per pixel; the bytes are the ones the general conversion gives. nil for any other layout
// (or an RGBA with a pixel that isn't opaque: its colour would need un-premultiplying).
func fastRGBA(img image.Image) []byte {
	var pix []byte
	var stride int
	var r image.Rectangle
	switch m := img.(type) {
	case *image.NRGBA:
		pix, stride, r = m.Pix, m.Stride, m.Rect
	case *image.RGBA:
		pix, stride, r = m.Pix, m.Stride, m.Rect
		for y := 0; y < r.Dy(); y++ {
			row := pix[y*stride : y*stride+4*r.Dx()]
			for o := 3; o < len(row); o += 4 {
				if row[o] != 255 {
					return nil
				}
			}
		}
	default:
		return nil
	}
	w, h := r.Dx(), r.Dy()
	out := make([]byte, 4*w*h)
	for y := 0; y < h; y++ {
		copy(out[4*y*w:4*(y+1)*w], pix[y*stride:y*stride+4*w])
	}
	return out
}

// DecodeDDS reads a .dds's top level (DXT1, DXT5 or A8R8G8B8).
func DecodeDDS(b []byte) (*Image, error) {
	w, h, px, err := dds.Decode(b)
	if err != nil {
		return nil, err
	}
	return NewImage(w, h, px)
}

// EncodePNG writes RGBA pixels (rows top to bottom) as a PNG.
func EncodePNG(w, h int, rgba []byte) ([]byte, error) {
	img := &image.NRGBA{Pix: rgba, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// ---------------------------------------------------------------- numbers

func lerp(a, b, t float64) float64 { return a + float64(float64(b-a)*t) }

func roundInt(x float64) int { return int(math.RoundToEven(x)) }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func imod(a, n int) int {
	a %= n
	if a < 0 {
		a += n
	}
	return a
}

// Smooth is the smoothstep of t clamped to 0..1.
func Smooth(t float64) float64 {
	t = clamp01(t)
	return float64(t*t) * (3 - float64(2*t))
}

// lattice hashes an integer lattice point to [0, 1).
func lattice(ix, iy, seed int) float64 {
	n := uint32(int64(ix)*374761393 + int64(iy)*668265263 + int64(seed)*2147483647)
	n = (n ^ n>>13) * 1274126177
	return float64((n^n>>16)&0xFFFF) / 65536.0
}

// Noise is smooth value noise in [0, 1) with lattice spacing period (metres).
func Noise(x, y, period float64, seed int) float64 {
	gx, gy := x/period, y/period
	fix, fiy := math.Floor(gx), math.Floor(gy)
	ix, iy := int(fix), int(fiy)
	fx, fy := Smooth(gx-fix), Smooth(gy-fiy)
	top := lerp(lattice(ix, iy, seed), lattice(ix+1, iy, seed), fx)
	bot := lerp(lattice(ix, iy+1, seed), lattice(ix+1, iy+1, seed), fx)
	return lerp(top, bot, fy)
}
