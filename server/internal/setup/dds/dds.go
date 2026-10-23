// Package dds encodes and decodes the game's DirectDraw Surface textures: DXT1 and DXT5 block compression (and
// uncompressed A8R8G8B8) with the header the shipped textures carry.
//
// The encoder matches the Python reference byte for byte. A colour block's two RGB565 endpoints lie on the
// block's principal axis (four power iterations on the colour covariance, the projections' extremes), each texel
// takes the nearest of the four palette colours; a DXT5 alpha block's endpoints are the block's maximum and
// minimum with the eight-value ramp. Mip levels are box-filtered halvings, by default down to 8 x 8 (the shipped
// chain: a 1024 x 1024 texture has 8 levels). The header copies the shipped quirks: flags 0xA1027, pitch the top
// level's byte size, the pixel format's bit count 4 (DXT1) or 8 (DXT5) with the RGBA masks set, caps 0x1008.
//
// The float arithmetic is written as the reference evaluates it (left to right, every product rounded before
// the sum: the explicit float64 conversions keep the compiler from fusing them).
package dds

import (
	"encoding/binary"
	"fmt"
	"math"
)

// The header constants.
const (
	Flags  = 0xA1027
	Caps   = 0x1008
	Header = 128
)

// Info is what a header says.
type Info struct {
	W, H, Mips int
	FourCC     string
	Format     string // "dxt1", "dxt5" or "argb"
}

// ReadInfo reads a header.
func ReadInfo(raw []byte) (Info, error) {
	if len(raw) < Header || string(raw[:4]) != "DDS " {
		return Info{}, fmt.Errorf("dds: not a DDS file")
	}
	le := binary.LittleEndian
	in := Info{H: int(le.Uint32(raw[12:])), W: int(le.Uint32(raw[16:])), Mips: int(le.Uint32(raw[28:])),
		FourCC: string(raw[84:88])}
	switch {
	case le.Uint32(raw[80:])&0x40 != 0 && in.FourCC == "\x00\x00\x00\x00":
		in.Format = "argb"
	case in.FourCC == "DXT1":
		in.Format = "dxt1"
	default:
		in.Format = "dxt5"
	}
	return in, nil
}

func to565(r, g, b int) int {
	return (r*31+127)/255<<11 | (g*63+127)/255<<5 | (b*31+127)/255
}

func from565(c int) [3]int {
	return [3]int{(c >> 11 & 31) * 255 / 31, (c >> 5 & 63) * 255 / 63, (c & 31) * 255 / 31}
}

// roundInt is the reference's int(round(x)): half to even.
func roundInt(x float64) int { return int(math.RoundToEven(x)) }

func clamp255(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// colorBlock: 8 bytes, two 565 endpoints along the block's main axis and 2-bit indices.
func colorBlock(px *[16][3]int, out []byte) {
	sr, sg, sb := 0, 0, 0
	for _, p := range px {
		sr += p[0]
		sg += p[1]
		sb += p[2]
	}
	mr, mg, mb := float64(sr)/16, float64(sg)/16, float64(sb)/16
	var cxx, cxy, cxz, cyy, cyz, czz float64
	for _, p := range px {
		x, y, z := float64(p[0])-mr, float64(p[1])-mg, float64(p[2])-mb
		cxx += float64(x * x)
		cxy += float64(x * y)
		cxz += float64(x * z)
		cyy += float64(y * y)
		cyz += float64(y * z)
		czz += float64(z * z)
	}
	ax := [3]float64{1, 1, 1}
	for it := 0; it < 4; it++ {
		ax = [3]float64{
			float64(cxx*ax[0]) + float64(cxy*ax[1]) + float64(cxz*ax[2]),
			float64(cxy*ax[0]) + float64(cyy*ax[1]) + float64(cyz*ax[2]),
			float64(cxz*ax[0]) + float64(cyz*ax[1]) + float64(czz*ax[2]),
		}
		m := math.Max(math.Max(math.Abs(ax[0]), math.Abs(ax[1])), math.Abs(ax[2]))
		if m < 1e-9 {
			ax = [3]float64{1, 1, 1}
			break
		}
		ax = [3]float64{ax[0] / m, ax[1] / m, ax[2] / m}
	}
	var lo, hi float64
	for k, p := range px {
		v := float64((float64(p[0])-mr)*ax[0]) + float64((float64(p[1])-mg)*ax[1]) + float64((float64(p[2])-mb)*ax[2])
		if k == 0 || v < lo {
			lo = v
		}
		if k == 0 || v > hi {
			hi = v
		}
	}
	ln := float64(ax[0]*ax[0]) + float64(ax[1]*ax[1]) + float64(ax[2]*ax[2])
	mean := [3]float64{mr, mg, mb}
	pt := func(t float64) [3]int {
		var c [3]int
		for k := 0; k < 3; k++ {
			c[k] = clamp255(roundInt(mean[k] + float64(t*ax[k])/ln))
		}
		return c
	}
	ph, pl := pt(hi), pt(lo)
	c0, c1 := to565(ph[0], ph[1], ph[2]), to565(pl[0], pl[1], pl[2])
	if c0 < c1 { // the four-colour mode needs c0 > c1 (DXT5 ignores the order)
		c0, c1 = c1, c0
	}
	binary.LittleEndian.PutUint16(out[0:], uint16(c0))
	binary.LittleEndian.PutUint16(out[2:], uint16(c1))
	if c0 == c1 {
		binary.LittleEndian.PutUint32(out[4:], 0)
		return
	}
	e0, e1 := from565(c0), from565(c1)
	var pal [4][3]int
	pal[0], pal[1] = e0, e1
	for k := 0; k < 3; k++ {
		pal[2][k] = (2*e0[k] + e1[k]) / 3
		pal[3][k] = (e0[k] + 2*e1[k]) / 3
	}
	var bits uint32
	for k, p := range px {
		best, bi := 1<<30, 0
		for i, c := range pal {
			d := (p[0]-c[0])*(p[0]-c[0]) + (p[1]-c[1])*(p[1]-c[1]) + (p[2]-c[2])*(p[2]-c[2])
			if d < best {
				best, bi = d, i
			}
		}
		bits |= uint32(bi) << (2 * k)
	}
	binary.LittleEndian.PutUint32(out[4:], bits)
}

// alphaBlock: 8 bytes, the maximum and minimum and 3-bit indices into the eight-value ramp.
func alphaBlock(al *[16]int, out []byte) {
	a0, a1 := al[0], al[0]
	for _, a := range al {
		a0, a1 = max(a0, a), min(a1, a)
	}
	out[0], out[1] = byte(a0), byte(a1)
	for k := 2; k < 8; k++ {
		out[k] = 0
	}
	if a0 == a1 {
		return
	}
	pal := [8]int{a0, a1}
	for k := 1; k < 7; k++ {
		pal[k+1] = ((7-k)*a0 + k*a1) / 7
	}
	var bits uint64
	for k, a := range al {
		bi, bd := 0, -1
		for i, p := range pal {
			d := p - a
			if d < 0 {
				d = -d
			}
			if bd < 0 || d < bd {
				bi, bd = i, d
			}
		}
		bits |= uint64(bi) << (3 * k)
	}
	for k := 0; k < 6; k++ {
		out[2+k] = byte(bits >> (8 * k))
	}
}

// EncodeLevel encodes one mip level (format "dxt1", "dxt5" or "argb"). Blocks past the edge repeat the last
// row and column.
func EncodeLevel(w, h int, rgba []byte, format string) ([]byte, error) {
	if len(rgba) < w*h*4 {
		return nil, fmt.Errorf("dds: %d x %d needs %d RGBA bytes, got %d", w, h, w*h*4, len(rgba))
	}
	switch format {
	case "argb":
		out := make([]byte, w*h*4)
		for q := 0; q < w*h*4; q += 4 {
			out[q], out[q+1], out[q+2], out[q+3] = rgba[q+2], rgba[q+1], rgba[q], rgba[q+3]
		}
		return out, nil
	case "dxt1", "dxt5":
	default:
		return nil, fmt.Errorf("dds: unknown format %q", format)
	}
	dxt1 := format == "dxt1"
	block := 16
	if dxt1 {
		block = 8
	}
	out := make([]byte, 0, ((w+3)/4)*((h+3)/4)*block)
	var px [16][3]int
	var al [16]int
	var tmp [8]byte
	for by := 0; by < h; by += 4 {
		for bx := 0; bx < w; bx += 4 {
			for k := 0; k < 16; k++ {
				x, y := min(bx+(k&3), w-1), min(by+(k>>2), h-1)
				q := (y*w + x) * 4
				px[k] = [3]int{int(rgba[q]), int(rgba[q+1]), int(rgba[q+2])}
				al[k] = int(rgba[q+3])
			}
			if !dxt1 {
				alphaBlock(&al, tmp[:])
				out = append(out, tmp[:]...)
			}
			colorBlock(&px, tmp[:])
			out = append(out, tmp[:]...)
		}
	}
	return out, nil
}

// Downsample halves an RGBA image (2 x 2 box, rounded; an odd edge repeats its last row or column).
func Downsample(w, h int, rgba []byte) (int, int, []byte) {
	nw, nh := max(1, w/2), max(1, h/2)
	out := make([]byte, nw*nh*4)
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			for c := 0; c < 4; c++ {
				s := 0
				for dy := 0; dy < 2; dy++ {
					for dx := 0; dx < 2; dx++ {
						s += int(rgba[(min(2*y+dy, h-1)*w+min(2*x+dx, w-1))*4+c])
					}
				}
				out[(y*nw+x)*4+c] = byte((s + 2) / 4)
			}
		}
	}
	return nw, nh, out
}

// DefaultMips is the shipped chain's level count: down to 8 x 8 (1024 -> 8 levels).
func DefaultMips(w, h int) int {
	n, s := 1, min(w, h)
	for s > 8 {
		s /= 2
		n++
	}
	return n
}

// FullMips is the level count down to 1 x 1.
func FullMips(w, h int) int {
	n, s := 1, max(w, h)
	for s > 1 {
		s /= 2
		n++
	}
	return n
}

// Encode writes a .dds of the RGBA pixels (rows top to bottom) in format "dxt1", "dxt5" or "argb" with mips
// levels (0: DefaultMips). The sides must be multiples of 4.
func Encode(w, h int, rgba []byte, format string, mips int) ([]byte, error) {
	if w <= 0 || h <= 0 || w%4 != 0 || h%4 != 0 {
		return nil, fmt.Errorf("dds: width and height must be positive multiples of 4, got %d x %d", w, h)
	}
	if mips <= 0 {
		mips = DefaultMips(w, h)
	}
	var body []byte
	cw, ch, cur := w, h, rgba
	for i := 0; i < mips; i++ {
		lv, err := EncodeLevel(cw, ch, cur, format)
		if err != nil {
			return nil, err
		}
		body = append(body, lv...)
		if i+1 < mips {
			cw, ch, cur = Downsample(cw, ch, cur)
		}
	}
	block := 16
	if format == "dxt1" {
		block = 8
	}
	linear := max(1, (w+3)/4) * max(1, (h+3)/4) * block
	fourcc := "DXT5"
	if format == "dxt1" {
		fourcc = "DXT1"
	}
	pfFlags, bitCount := 4, 8
	if format == "dxt1" {
		bitCount = 4
	}
	if format == "argb" {
		// pitch one row, pixel format RGB | alpha, 32 bits
		linear, fourcc, pfFlags, bitCount = w*4, "\x00\x00\x00\x00", 0x41, 32
	}
	le := binary.LittleEndian
	head := make([]byte, Header, Header+len(body))
	copy(head, "DDS ")
	for k, v := range []int{124, Flags, h, w, linear, 1, mips} {
		le.PutUint32(head[4+4*k:], uint32(v))
	}
	le.PutUint32(head[76:], 32)
	le.PutUint32(head[80:], uint32(pfFlags))
	copy(head[84:], fourcc)
	for k, v := range []uint32{uint32(bitCount), 0xFF0000, 0xFF00, 0xFF, 0xFF000000} {
		le.PutUint32(head[88+4*k:], v)
	}
	le.PutUint32(head[108:], Caps)
	return append(head, body...), nil
}

// Decode reads a .dds's top level as RGBA (rows top to bottom): DXT1, DXT5 or uncompressed A8R8G8B8. A DXT1
// block in its three-colour mode decodes index 3 as opaque black.
func Decode(raw []byte) (w, h int, rgba []byte, err error) {
	if len(raw) < Header || string(raw[:4]) != "DDS " {
		return 0, 0, nil, fmt.Errorf("dds: not a DDS file")
	}
	le := binary.LittleEndian
	h, w = int(le.Uint32(raw[12:])), int(le.Uint32(raw[16:]))
	fourcc := string(raw[0x54:0x58])
	pfFlags, bitCount := le.Uint32(raw[0x50:]), le.Uint32(raw[0x58:])
	if w <= 0 || h <= 0 || w > 1<<14 || h > 1<<14 {
		return 0, 0, nil, fmt.Errorf("dds: bad size %d x %d", w, h)
	}
	if pfFlags&0x40 != 0 && bitCount == 32 {
		if len(raw) < Header+w*h*4 {
			return 0, 0, nil, fmt.Errorf("dds: the pixels are cut")
		}
		px := make([]byte, w*h*4)
		src := raw[Header:]
		for q := 0; q < w*h*4; q += 4 {
			px[q], px[q+1], px[q+2], px[q+3] = src[q+2], src[q+1], src[q], src[q+3]
		}
		return w, h, px, nil
	}
	if fourcc != "DXT1" && fourcc != "DXT5" {
		return 0, 0, nil, fmt.Errorf("dds: unsupported format %q", fourcc)
	}
	block := 16
	if fourcc == "DXT1" {
		block = 8
	}
	if len(raw) < Header+((w+3)/4)*((h+3)/4)*block {
		return 0, 0, nil, fmt.Errorf("dds: the top level is cut")
	}
	px := make([]byte, w*h*4)
	p := Header
	var alpha [16]int
	for by := 0; by < h; by += 4 {
		for bx := 0; bx < w; bx += 4 {
			for k := range alpha {
				alpha[k] = 255
			}
			if block == 16 {
				a0, a1 := int(raw[p]), int(raw[p+1])
				var bits uint64
				for k := 0; k < 6; k++ {
					bits |= uint64(raw[p+2+k]) << (8 * k)
				}
				var pal [8]int
				pal[0], pal[1] = a0, a1
				if a0 > a1 {
					for k := 1; k < 7; k++ {
						pal[k+1] = ((7-k)*a0 + k*a1) / 7
					}
				} else {
					for k := 1; k < 5; k++ {
						pal[k+1] = ((5-k)*a0 + k*a1) / 5
					}
					pal[6], pal[7] = 0, 255
				}
				for k := 0; k < 16; k++ {
					alpha[k] = pal[bits>>(3*k)&7]
				}
				p += 8
			}
			c0, c1 := int(le.Uint16(raw[p:])), int(le.Uint16(raw[p+2:]))
			bits := le.Uint32(raw[p+4:])
			p += 8
			r0, r1 := from565(c0), from565(c1)
			var cols [4][3]int
			cols[0], cols[1] = r0, r1
			if c0 > c1 || block == 16 {
				for k := 0; k < 3; k++ {
					cols[2][k] = (2*r0[k] + r1[k]) / 3
					cols[3][k] = (r0[k] + 2*r1[k]) / 3
				}
			} else {
				for k := 0; k < 3; k++ {
					cols[2][k] = (r0[k] + r1[k]) / 2
				}
			}
			for k := 0; k < 16; k++ {
				x, y := bx+(k&3), by+(k>>2)
				if x < w && y < h {
					q := (y*w + x) * 4
					c := cols[bits>>(2*k)&3]
					px[q], px[q+1], px[q+2], px[q+3] = byte(c[0]), byte(c[1]), byte(c[2]), byte(alpha[k])
				}
			}
		}
	}
	return w, h, px, nil
}
