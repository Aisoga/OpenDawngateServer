package bake

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"runtime"
	"testing"

	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/mapmm/ground"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// pattern is a made-up RGBA picture: gradients and a checker, alpha varying.
func pattern(w, h int) []byte {
	px := make([]byte, 4*w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := 4 * (y*w + x)
			px[o] = byte(x * 255 / max(1, w-1))
			px[o+1] = byte(y * 255 / max(1, h-1))
			px[o+2] = byte(((x/4 + y/4) % 2) * 200)
			px[o+3] = byte(255 - (x+y)%64)
		}
	}
	return px
}

func img(t *testing.T, w, h int) *Image {
	t.Helper()
	im, err := NewImage(w, h, pattern(w, h))
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func nd(typ string, kv ...any) *bmdl.Node {
	n := &bmdl.Node{Type: typ, F: map[string]any{}}
	for i := 0; i < len(kv); i += 2 {
		n.F[kv[i].(string)] = kv[i+1]
	}
	return n
}

// madeUpTemplate is a version-5 model with a one-texture vertex-lit material, a 24-byte and a 32-byte mesh and two
// parts (the ground package's tests' kind).
func madeUpTemplate() []byte {
	el := func(stream, off uint16, typ, usage byte) []byte {
		b := binary.LittleEndian.AppendUint16(nil, stream)
		b = binary.LittleEndian.AppendUint16(b, off)
		return append(b, typ, 0, usage, 0)
	}
	cat := func(p ...[]byte) []byte { return bytes.Join(p, nil) }
	decl32 := cat(el(0, 0, 2, 0), el(0, 12, 8, 1), el(0, 16, 8, 2), el(0, 20, 1, 4), el(0, 28, 8, 5), el(0xFF, 0, 17, 0))
	decl24 := cat(el(0, 0, 2, 0), el(0, 12, 2, 1), el(0xFF, 0, 17, 0))
	u := func(v int) uint32 { return uint32(v) }
	tex := nd("Texture", "slot", "diffuseMap", "slot_hash", bmdl.FNV1("diffuseMap"), "path", "madeup/a_D.dds",
		"path_hash", bmdl.FNV1("madeup/a_D.dds"))
	mat := nd("Material", "shader", "labsVertLit", "shader_hash", bmdl.FNV1("labsVertLit"), "technique", "opaque",
		"technique_hash", bmdl.FNV1("opaque"), "flags", u(0x106), "param_count", u(0),
		"params", []*bmdl.Node{nd("Param", "name", "tint", "name_hash", bmdl.FNV1("tint"), "offset", u(0), "size", u(4))},
		"value_count", u(0), "values", []float32{1.0, 0.5, 0.25, 1}, "texture_count", u(0),
		"textures", []*bmdl.Node{tex}, "stream_count", u(0), "streams", []*bmdl.Node{})
	bounds := func(k int) []float32 { return []float32{0.5 * float32(k), 0.25, -1.0, 2.0, 1.0, 1.5, 0.75, 4.0} }
	mesh := func(i int, decl []byte, stride int) *bmdl.Node {
		name := fmt.Sprintf("madeup_mesh_%d", i)
		return nd("Mesh", "bounds", bounds(i), "name", name, "name_hash", bmdl.FNV1(name), "material", u(0),
			"decl_count", u(len(decl)/8), "decl", decl, "vertices", make([]byte, 3*stride),
			"indices", []byte{0, 0, 1, 0, 2, 0}, "vertex_count", u(3), "index_count", u(3), "target_count", u(0),
			"targets", []*bmdl.Node{})
	}
	part := func(i int) *bmdl.Node {
		pal := make([]uint32, 16)
		return nd("Part", "bounds", bounds(10+i), "mesh", u(i), "submesh_count", u(0),
			"submeshes", []*bmdl.Node{nd("Submesh", "bounds", bounds(20+i), "index_start", u(0), "index_count", u(3),
				"material", u(0), "palette", pal)})
	}
	model := nd("Model", "bounds", bounds(30), "name", "madeup_model", "name_hash", bmdl.FNV1("madeup_model"),
		"material_count", u(0), "materials", []*bmdl.Node{mat},
		"mesh_count", u(0), "meshes", []*bmdl.Node{mesh(0, decl24, 24), mesh(1, decl32, 32)},
		"part_count", u(0), "parts", []*bmdl.Node{part(0), part(1)}, "unk_40", u(7))
	b, err := bmdl.Write(&bmdl.Tree{Tag: "bmdl", Version: 5, Align: 16,
		Root: nd("Root", "model", model, "skeleton", nil, "anim_count", u(0), "anims", []*bmdl.Node{})})
	if err != nil {
		panic(err)
	}
	return b
}

// madeUpGround is a 64-cell heightfield over 80 m: an open cross in walls (a cliff around it).
func madeUpGround(t *testing.T) *ground.Ground {
	t.Helper()
	const n = 64
	cells := make([]byte, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			if !(i > 26 && i < 38 || j > 22 && j < 34) {
				cells[j*n+i] = 128
			}
		}
	}
	f, err := ground.NewField(n, n, cells, 80)
	if err != nil {
		t.Fatal(err)
	}
	return ground.NewGroundSpec(f, 32, 0, ground.Spec{Size: 80, Bases: [2][2]float64{{-30, -30}, {30, 30}}})
}

func madeUpSpec(t *testing.T, g *ground.Ground) *Spec {
	half := float64(g.Grid/2) * g.Cell
	mask := NewGrid(64, half, func(x, y float64) float64 { return Smooth(0.5 - (math.Hypot(x-5, y+3)-12)/6) })
	return &Spec{Half: half, Tiles: 2, Texels: 64, Margin: MarginFor(64),
		Layers: []Layer{
			{Tex: img(t, 32, 32), Scale: 7, Tint: [3]float64{1, 1, 1}, Opacity: 1},
			{Tex: img(t, 16, 8), Scale: 3, Rot: 0.4, Tint: [3]float64{0.8, 1.1, 0.9}, Vary: 0.2, Opacity: 0.9, Mask: mask,
				Seed: 1, Break: 0.7},
		},
		Stamps: []Stamp{{Img: img(t, 20, 12), Centre: [2]float64{-8, 6}, Size: [2]float64{14, 9}, Rot: 0.7, Opacity: 0.8,
			Tint: [3]float64{1, 1, 1}, Soft: 2, Round: true},
			{Img: Flat([3]float64{200, 30, 30}), Centre: [2]float64{10, -10}, Size: [2]float64{6, 6}, Opacity: 1,
				Tint: [3]float64{1, 1, 1}, Soft: 1}}}
}

// pins of the made-up bake (any platform must give these bytes).
var pins = map[string]string{
	"tile00":  "1be690b54547d9579962e43961e3fe00f79529c049a0185ff6b5d5748989d31c",
	"tile11":  "72dadd53dc6234852cc26ab49e2fe40948d3be8b09e572af07b5f4cfb398b572",
	"picture": "5164e975371f0c29860481eb4090d3662f611b9d7b36be9fb17e45089560c4ac",
	"model":   "8015a7a09b705785d97fc6744e5ca5b3148a5c0e525a07642a2d021f7528153d",
}

func check(t *testing.T, name string, b []byte) {
	t.Helper()
	got := sum(b)
	if want := pins[name]; got != want {
		t.Errorf("%s: sha256 %s, pinned %s", name, got, want)
	}
}

func TestMadeUp(t *testing.T) {
	g := madeUpGround(t)
	s := madeUpSpec(t, g)
	t00, err := s.Tile(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t11, err := s.Tile(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	pic := s.Picture(48, -s.Half, -s.Half, 2*s.Half)
	model, err := BuildModel(madeUpTemplate(), g, s, "madeup", []string{"t00.dds", "t10.dds", "t01.dds", "t11.dds"},
		&Cliff{Path: "cliff.dds", Scale: 8, CosSlope: math.Cos(30 * math.Pi / 180)})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "tile00", t00)
	check(t, "tile11", t11)
	check(t, "picture", pic)
	check(t, "model", model)
	info, err := CheckModel(model)
	if err != nil {
		t.Fatal(err)
	}
	if info.Materials != 5 || info.Meshes != 4 || info.Triangles != 2*32*32 {
		t.Errorf("%+v", info)
	}
	// one processor gives the same bytes
	prev := runtime.GOMAXPROCS(1)
	again, _ := s.Tile(0, 0)
	runtime.GOMAXPROCS(prev)
	if !bytes.Equal(again, t00) {
		t.Error("the tile differs on one processor")
	}
}

// TestSeams: a tile's margin holds its neighbour's ground, so the texels on both sides of a tile edge agree.
func TestSeams(t *testing.T) {
	g := madeUpGround(t)
	s := madeUpSpec(t, g)
	a, _ := s.Tile(0, 0)
	b, _ := s.Tile(1, 0)
	n, m := s.Texels, s.Margin
	inner := n - 2*m
	worst := 0
	for r := 0; r < n; r++ {
		for c := 0; c < 2*m; c++ { // a's columns from its inner right edge on are b's first columns
			pa := a[4*(r*n+inner+c):]
			pb := b[4*(r*n+c):]
			for q := 0; q < 3; q++ {
				d := int(pa[q]) - int(pb[q])
				worst = max(worst, max(d, -d))
			}
		}
	}
	if worst > 1 {
		t.Errorf("the margins differ by up to %d", worst)
	}
	// the UVs of a tile's edges lie on the margin's inner edge
	x0, y1 := s.TileOrigin(1, 0)
	u, v := s.UV(1, 0, x0, y1)
	if math.Abs(u-float64(m)/float64(n)) > 1e-12 || math.Abs(v-float64(m)/float64(n)) > 1e-12 {
		t.Errorf("uv %g, %g", u, v)
	}
}

func TestModelUVs(t *testing.T) {
	g := madeUpGround(t)
	s := madeUpSpec(t, g)
	model, err := BuildModel(madeUpTemplate(), g, s, "madeup", []string{"a", "b", "c", "d"},
		&Cliff{Path: "cliff", Scale: 8, CosSlope: math.Cos(30 * math.Pi / 180)})
	if err != nil {
		t.Fatal(err)
	}
	m, err := bmdl.Decode(model)
	if err != nil {
		t.Fatal(err)
	}
	cliffs := 0
	for _, p := range m.Parts {
		me := m.Meshes[p.Mesh]
		for _, sm := range p.Submeshes {
			if sm.Material == 4 {
				cliffs += sm.Count / 3
			}
			for q := sm.Start; q < sm.Start+sm.Count; q++ {
				uv := me.UVs[me.Indices[q]]
				if uv[0] < 0 || uv[0] > 1 || uv[1] < 0 || uv[1] > 1 {
					t.Fatalf("material %d: uv %v outside 0..1", sm.Material, uv)
				}
			}
		}
	}
	if cliffs == 0 {
		t.Error("no cliff triangles")
	}
	t.Logf("%d cliff triangles", cliffs)
}

func TestSample(t *testing.T) {
	im := Flat([3]float64{10, 20, 30})
	if p := im.Sample(0.3, 0.9, 3, true); p != [4]float64{10, 20, 30, 255} {
		t.Errorf("%v", p)
	}
	pi := img(t, 8, 8)
	if pi.Levels() != 4 {
		t.Errorf("%d levels", pi.Levels())
	}
	// wrapped: one repeat over gives the same
	a, b := pi.Sample(0.2, 0.7, 1, true), pi.Sample(1.2, -0.3, 1, true)
	for k := range a {
		if math.Abs(a[k]-b[k]) > 1e-9 {
			t.Errorf("%v %v", a, b)
		}
	}
	c, err := pi.Cut(2, 3, 4, 2)
	if err != nil || c.W != 4 || c.H != 2 || !bytes.Equal(c.Top()[:4], pi.Top()[4*(3*8+2):4*(3*8+2)+4]) {
		t.Errorf("cut %v", err)
	}
	if _, err := pi.Cut(6, 0, 4, 2); err == nil {
		t.Error("a rectangle past the edge cut")
	}
	w, h, rep := pi.Repeat(2, 2, 4)
	if w != 8 || h != 8 || !bytes.Equal(rep[:16], rep[16:32]) {
		t.Errorf("repeat %d x %d", w, h)
	}
	gr := NewGrid(4, 2, func(x, y float64) float64 { return x })
	if v := gr.At(0.25, 1); math.Abs(v-0.25) > 1e-6 {
		t.Errorf("grid %g", v)
	}
	if Smooth(-1) != 0 || Smooth(2) != 1 || Smooth(0.5) != 0.5 {
		t.Error("smooth")
	}
}

func TestPNG(t *testing.T) {
	px := pattern(5, 3)
	b, err := EncodePNG(5, 3, px)
	if err != nil {
		t.Fatal(err)
	}
	im, err := DecodePNG(b, 8)
	if err != nil || !bytes.Equal(im.Top(), px) {
		t.Fatalf("%v", err)
	}
	if _, err := DecodePNG(b, 4); err == nil {
		t.Error("a picture over the limit read")
	}
}
