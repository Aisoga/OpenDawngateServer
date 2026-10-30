package bake

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// TestBase: tiles baked from a Base and nothing else are the Base's pixels exactly; a Base made of a bake's own tiles
// gives those tiles again; a layer over the Base draws only through its mask.
func TestBase(t *testing.T) {
	s := madeUpSpec(t, madeUpGround(t))
	var tiles [][]byte
	for tj := 0; tj < 2; tj++ {
		for ti := 0; ti < 2; ti++ {
			px, err := s.Tile(ti, tj)
			if err != nil {
				t.Fatal(err)
			}
			tiles = append(tiles, px)
		}
	}
	joined := make([][]byte, 4)
	for k := range tiles {
		joined[k] = append([]byte(nil), tiles[k]...)
	}
	if err := JoinMargins(2, 64, s.Margin, joined); err != nil {
		t.Fatal(err)
	}
	b := &Spec{Half: s.Half, Tiles: 2, Texels: 64, Margin: s.Margin}
	for k := range joined {
		if !bytes.Equal(joined[k], tiles[k]) {
			t.Errorf("tile %d: the bake's margins differ from the neighbours' squares", k)
		}
		im, err := NewImage(64, 64, joined[k])
		if err != nil {
			t.Fatal(err)
		}
		b.Base = append(b.Base, im)
	}
	for k := range tiles {
		px, err := b.Tile(k%2, k/2)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(px, tiles[k]) {
			t.Errorf("tile %d baked from its own picture differs", k)
		}
	}
	// the picture is the base's colour at each place (the whole ground, at about a texel a pixel)
	n := 2 * (64 - 2*s.Margin)
	pa, pb := s.Picture(n, -s.Half, -s.Half, 2*s.Half), b.Picture(n, -s.Half, -s.Half, 2*s.Half)
	diff := 0
	for i := range pa {
		d := int(pa[i]) - int(pb[i])
		diff = max(diff, d, -d)
	}
	if diff > 2 {
		t.Errorf("the base's picture differs from the bake's by up to %d", diff)
	}
	// a layer over it: only where its mask is
	mask := NewGrid(32, s.Half, func(x, y float64) float64 {
		if x > 0 && y > 0 {
			return 1
		}
		return 0
	})
	b.Layers = []Layer{{Tex: Flat([3]float64{0, 255, 0}), Scale: 4, Tint: [3]float64{1, 1, 1}, Opacity: 1, Mask: mask}}
	if c := b.Colour(s.Half/2, s.Half/2, b.TexelSize()); c != [3]float64{0, 255, 0} {
		t.Errorf("the layer over the base: %v", c)
	}
	if c, w := b.Colour(-s.Half/2, -s.Half/2, b.TexelSize()), s.Colour(-s.Half/2, -s.Half/2, b.TexelSize()); c[0]-w[0] > 3 || w[0]-c[0] > 3 {
		t.Errorf("outside the mask: %v, the bake %v", c, w)
	}
	b.Base = b.Base[:3]
	if _, err := b.Tile(0, 0); err == nil {
		t.Error("3 base pictures for 2 x 2 tiles baked")
	}
}

// TestJoinMargins: on made-up noise, every margin texel inside the ground is its owner's texel at the same place, and
// the outer border's margins and every own square stay.
func TestJoinMargins(t *testing.T) {
	const tiles, texels, margin = 3, 32, 3
	inner := texels - 2*margin
	px := make([][]byte, tiles*tiles)
	before := make([][]byte, len(px))
	seed := uint32(7)
	for k := range px {
		px[k] = make([]byte, 4*texels*texels)
		for o := range px[k] {
			seed = seed*1664525 + 1013904223
			px[k][o] = byte(seed >> 24)
		}
		before[k] = append([]byte(nil), px[k]...)
	}
	if err := JoinMargins(tiles, texels, margin, px); err != nil {
		t.Fatal(err)
	}
	// the texel at global (gx from the west, gy from the north) in tile (ti, row from the north)
	for tj := 0; tj < tiles; tj++ {
		row := tiles - 1 - tj
		for r := 0; r < texels; r++ {
			for c := 0; c < texels; c++ {
				gy := row*inner + r - margin
				for ti := 0; ti < tiles; ti++ {
					gx := ti*inner + c - margin
					o := 4 * (r*texels + c)
					got := px[tj*tiles+ti][o : o+4]
					own := r >= margin && r < texels-margin && c >= margin && c < texels-margin
					outside := gx < 0 || gy < 0 || gx >= tiles*inner || gy >= tiles*inner
					var want []byte
					if own || outside {
						want = before[tj*tiles+ti][o : o+4]
					} else {
						oc, orow := gx/inner, gy/inner
						lo := 4 * ((gy%inner+margin)*texels + gx%inner + margin)
						want = before[(tiles-1-orow)*tiles+oc][lo : lo+4]
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("tile (%d, %d) texel (%d, %d): %v, want %v", ti, tj, c, r, got, want)
					}
				}
			}
		}
	}
	if JoinMargins(2, 32, 3, px[:3]) == nil || JoinMargins(1, 16, 3, [][]byte{make([]byte, 10)}) == nil {
		t.Error("wrong counts or sizes joined")
	}
}

// TestDecodeFast: the fast copies give the general conversion's bytes (8-bit RGB, RGBA, and an RGBA with a
// transparent pixel, which takes the general path).
func TestDecodeFast(t *testing.T) {
	enc := func(m image.Image) []byte {
		var b bytes.Buffer
		if err := png.Encode(&b, m); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	slow := func(m image.Image) []byte {
		r := m.Bounds()
		out := make([]byte, 0, 4*r.Dx()*r.Dy())
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				c := color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
				out = append(out, c.R, c.G, c.B, c.A)
			}
		}
		return out
	}
	for _, alpha := range []bool{false, true} {
		m := image.NewNRGBA(image.Rect(0, 0, 13, 9))
		copy(m.Pix, pattern(13, 9))
		if !alpha {
			for o := 3; o < len(m.Pix); o += 4 {
				m.Pix[o] = 255
			}
		}
		b := enc(m)
		dec, _ := png.Decode(bytes.NewReader(b))
		w, h, px, err := DecodePNGPixels(b, 64)
		if err != nil || w != 13 || h != 9 || !bytes.Equal(px, slow(dec)) {
			t.Errorf("alpha %v (%T): %v", alpha, dec, err)
		}
		if fastRGBA(dec) == nil {
			t.Errorf("alpha %v: %T took the general path", alpha, dec)
		}
	}
	rgba := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for o := range rgba.Pix {
		rgba.Pix[o] = 255
	}
	rgba.Pix[3], rgba.Pix[0] = 128, 100
	if fastRGBA(rgba) != nil {
		t.Error("an RGBA that isn't opaque took the fast path")
	}
}
