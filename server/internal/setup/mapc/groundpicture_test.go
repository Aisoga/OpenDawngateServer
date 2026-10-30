package mapc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapc/bake"
)

// writeTiles writes the bake's tiles as `mapc bake --full` does (tile_<i>_<j>.png under the package's ground/) and
// returns the picture's list and the tiles' pixels.
func writeTiles(t *testing.T, m *Map, cm *Compiled, read ShippedFiles) ([]string, [][]byte) {
	t.Helper()
	b, err := cm.Bake(read)
	if err != nil {
		t.Fatal(err)
	}
	tiles, err := PictureTiles(b.Spec)
	if err != nil {
		t.Fatal(err)
	}
	var list []string
	n := b.Spec.Tiles
	for k, px := range tiles {
		f := "ground/" + PictureTileFile(k%n, k/n)
		p, err := bake.EncodePNG(b.Spec.Texels, b.Spec.Texels, px)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(m.Dir, filepath.FromSlash(f)), p, 0o644); err != nil {
			t.Fatal(err)
		}
		list = append(list, f)
	}
	return list, tiles
}

// pictured is m with the picture as its ground: no layers, no stamps (they are in the picture), the cliff kept.
func pictured(m *Map, list []string) *Map {
	m2 := *m
	m2.Ground.GroundBake = m.Ground.GroundBake
	m2.Ground.Layers, m2.Ground.Stamps, m2.Ground.Picture = nil, nil, list
	return &m2
}

func groundFilesOf(t *testing.T, m *Map, read ShippedFiles) map[string][]byte {
	t.Helper()
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	files, _, err := cm.GroundFiles(read)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range files {
		out[f.Name] = f.Data
	}
	return out
}

// TestGroundPictureRoundTrip: the bake's tiles written as `mapc bake --full` writes them and read back as the
// ground's picture give the same archive files, byte for byte (the tiles' DDS, the model, the cliff).
func TestGroundPictureRoundTrip(t *testing.T) {
	m, read := madeUpBaked(t)
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	want := groundFilesOf(t, m, read)
	list, tiles := writeTiles(t, m, cm, read)
	// the tiles' margins already agree with their neighbours' squares: joining changes nothing
	cp := make([][]byte, len(tiles))
	for k := range tiles {
		cp[k] = append([]byte(nil), tiles[k]...)
	}
	if err := bake.JoinMargins(2, 1024, bake.MarginFor(1024), cp); err != nil {
		t.Fatal(err)
	}
	for k := range cp {
		if !bytes.Equal(cp[k], tiles[k]) {
			t.Errorf("tile %d: its margins differ from its neighbours' squares", k)
		}
	}
	m2 := pictured(m, list)
	got := groundFilesOf(t, m2, read)
	if len(got) != len(want) {
		t.Fatalf("%d files, %d before", len(got), len(want))
	}
	for name, data := range want {
		if !bytes.Equal(got[name], data) {
			t.Errorf("%s differs after the round trip", name)
		}
	}

	// a painted margin over a neighbour's square is that neighbour's: the tiles stay the same
	mark := func(k, x, y int) {
		px := append([]byte(nil), tiles[k]...)
		o := 4 * (y*1024 + x)
		px[o], px[o+1], px[o+2] = 255, 0, 255
		p, err := bake.EncodePNG(1024, 1024, px)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(m.Dir, filepath.FromSlash(list[k])), p, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mark(0, 1022, 500) // tile (0, 0)'s east margin: tile (1, 0)'s ground
	if got := groundFilesOf(t, m2, read); !bytes.Equal(got[BakeTileEntry("CM_Ring", 0, 0)], want[BakeTileEntry("CM_Ring", 0, 0)]) {
		t.Error("a margin over the neighbour's square changed the tile")
	}
	mark(0, 1, 500) // tile (0, 0)'s west margin: outside the ground, its own
	if got := groundFilesOf(t, m2, read); bytes.Equal(got[BakeTileEntry("CM_Ring", 0, 0)], want[BakeTileEntry("CM_Ring", 0, 0)]) {
		t.Error("the outer margin's own pixel was not kept")
	}
	writeTiles(t, m, cm, read) // the export's pixels again

	// a layer over the picture draws through its mask only
	m3 := pictured(m, list)
	m3.Ground.Layers = []GroundLayer{{Texture: TextureRef{RGB: &[3]float64{250, 0, 250}},
		Shapes: []MaskShape{{Circle: &Point{-150, -150}, R: 20}}, Mirror: new(bool)}}
	b3 := bakeOf(t, m3, read)
	b0 := bakeOf(t, m2, read)
	if c := b3.Spec.Colour(-150, -150, 0.2); !(c[0] > 240 && c[1] < 10) {
		t.Errorf("the layer over the picture at its circle: %v", c)
	}
	if c, d := b3.Spec.Colour(150, 150, 0.2), b0.Spec.Colour(150, 150, 0.2); c != d {
		t.Errorf("the layer drew at the mirror (mirror off): %v, %v", c, d)
	}
	orig := bakeOf(t, m, read)
	for _, p := range [][2]float64{{-100, 40}, {-150, -150}, {170, -20}, {0.3, 0.2}} {
		c, w := b0.Spec.Colour(p[0], p[1], 0.3), orig.Spec.Colour(p[0], p[1], 0.3)
		for q := range c {
			if d := c[q] - w[q]; d > 24 || d < -24 {
				t.Errorf("the picture at %v: %v, the bake %v", p, c, w)
				break
			}
		}
	}
}

func bakeOf(t *testing.T, m *Map, read ShippedFiles) *Baked {
	t.Helper()
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	b, err := cm.Bake(read)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestGroundPictureRules: the count, the order by name, a missing or wrongly sized file, a layer without a mask over
// the picture, the picture without the baked style.
func TestGroundPictureRules(t *testing.T) {
	m, read := madeUpBaked(t)
	_, cm := Check(m, nil)
	list, _ := writeTiles(t, m, cm, read)
	has := func(m *Map, rule, text string) {
		t.Helper()
		r, _ := Check(m, nil)
		for _, f := range r.Findings {
			if f.Rule == rule && strings.Contains(f.Message, text) {
				return
			}
		}
		t.Errorf("no %s %q in %+v", rule, text, r.Findings)
	}
	has(pictured(m, list[:3]), "ground.picture", "3 pictures for 2 x 2 tiles")
	has(pictured(m, []string{list[1], list[0], list[2], list[3]}), "ground.picture", "named for tile (1, 0) but listed for tile (0, 0)")
	has(pictured(m, []string{list[0], list[0], list[2], list[3]}), "ground.picture", "listed twice")
	has(pictured(m, []string{"ground/none.png", list[1], list[2], list[3]}), "ground.picture", "ground/none.png is missing from the package")
	has(pictured(m, []string{"../x.png", list[1], list[2], list[3]}), "ground.file", "inside the package")
	has(pictured(m, []string{"ground/stone.png", list[1], list[2], list[3]}), "ground.picture", "24 x 16 pixels, the tile is 1024 x 1024")
	unmasked := pictured(m, list)
	unmasked.Ground.Layers = []GroundLayer{{Texture: TextureRef{Procedural: "dirt"}}}
	has(unmasked, "ground.mask", "over the ground's picture needs a mask")
	tiled := pictured(m, list)
	tiled.Ground = Ground{Style: "tiled", GroundBake: GroundBake{Picture: list}}
	has(tiled, "ground.baked", "picture go with the style")
	// a missing file stops the bake with its name too (the check without the folder doesn't read files)
	m2 := pictured(m, list)
	m2.Ground.Picture = []string{list[0], list[1], list[2], "ground/tile_1_1_gone.png"}
	dir := m2.Dir
	m2.Dir = ""
	r, cm2 := Check(m2, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	m2.Dir = dir
	if _, err := cm2.Bake(read); err == nil || !strings.Contains(err.Error(), "ground/tile_1_1_gone.png is missing") {
		t.Errorf("the bake with a missing tile: %v", err)
	}
}
