package mapc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/mapc/bake"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/snap"
)

// RingBakedMadeUpSHA256 pins CM_Ring with a made-up baked ground (madeUpBaked): its ground files and its minimap,
// from made-up textures and a made-up template, so the public tests on every platform pin the bake.
const RingBakedMadeUpSHA256 = "cb87af6542464556e1f07ee6caf70568423d066a57c2b270c1aeebec91d0763f"

// bakeTemplate is a made-up version-5 template model: a one-texture vertex-lit material, a 24-byte and a 32-byte
// mesh and two parts.
func bakeTemplate(t *testing.T) []byte {
	t.Helper()
	nd := func(typ string, kv ...any) *bmdl.Node {
		n := &bmdl.Node{Type: typ, F: map[string]any{}}
		for i := 0; i < len(kv); i += 2 {
			n.F[kv[i].(string)] = kv[i+1]
		}
		return n
	}
	el := func(stream, off uint16, typ, usage byte) []byte {
		b := binary.LittleEndian.AppendUint16(nil, stream)
		b = binary.LittleEndian.AppendUint16(b, off)
		return append(b, typ, 0, usage, 0)
	}
	cat := func(p ...[]byte) []byte { return bytes.Join(p, nil) }
	decl32 := cat(el(0, 0, 2, 0), el(0, 12, 8, 1), el(0, 16, 8, 2), el(0, 20, 1, 4), el(0, 28, 8, 5), el(0xFF, 0, 17, 0))
	decl24 := cat(el(0, 0, 2, 0), el(0, 12, 2, 1), el(0xFF, 0, 17, 0))
	u := func(v int) uint32 { return uint32(v) }
	tex := nd("Texture", "slot", "diffuseMap", "slot_hash", bmdl.FNV1("diffuseMap"), "path", "made/a_D.dds",
		"path_hash", bmdl.FNV1("made/a_D.dds"))
	mat := nd("Material", "shader", "labsVertLit", "shader_hash", bmdl.FNV1("labsVertLit"), "technique", "opaque",
		"technique_hash", bmdl.FNV1("opaque"), "flags", u(0x106), "param_count", u(0), "params", []*bmdl.Node{},
		"value_count", u(0), "values", []float32{}, "texture_count", u(0), "textures", []*bmdl.Node{tex},
		"stream_count", u(0), "streams", []*bmdl.Node{})
	bounds := []float32{0, 0, 0, 1, 1, 1, 1, 1}
	mesh := func(i int, decl []byte, stride int) *bmdl.Node {
		name := fmt.Sprintf("made_mesh_%d", i)
		return nd("Mesh", "bounds", bounds, "name", name, "name_hash", bmdl.FNV1(name), "material", u(0),
			"decl_count", u(len(decl)/8), "decl", decl, "vertices", make([]byte, 3*stride),
			"indices", []byte{0, 0, 1, 0, 2, 0}, "vertex_count", u(3), "index_count", u(3), "target_count", u(0),
			"targets", []*bmdl.Node{})
	}
	part := func(i int) *bmdl.Node {
		return nd("Part", "bounds", bounds, "mesh", u(i), "submesh_count", u(0), "submeshes", []*bmdl.Node{
			nd("Submesh", "bounds", bounds, "index_start", u(0), "index_count", u(3), "material", u(0),
				"palette", make([]uint32, 16))})
	}
	model := nd("Model", "bounds", bounds, "name", "made_model", "name_hash", bmdl.FNV1("made_model"),
		"material_count", u(0), "materials", []*bmdl.Node{mat}, "mesh_count", u(0),
		"meshes", []*bmdl.Node{mesh(0, decl24, 24), mesh(1, decl32, 32)}, "part_count", u(0),
		"parts", []*bmdl.Node{part(0), part(1)}, "unk_40", u(7))
	b, err := bmdl.Write(&bmdl.Tree{Tag: "bmdl", Version: 5, Align: 16,
		Root: nd("Root", "model", model, "skeleton", nil, "anim_count", u(0), "anims", []*bmdl.Node{})})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// stripes is a made-up RGBA picture.
func stripes(w, h int, alpha bool) []byte {
	px := make([]byte, 4*w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := 4 * (y*w + x)
			px[o], px[o+1], px[o+2], px[o+3] = byte(40+x*7), byte(90+(x+y)%40), byte(200-y*5), 255
			if alpha {
				px[o+3] = byte(min(255, 16*(x+y)))
			}
		}
	}
	return px
}

// madeUpBaked is CM_Ring with a made-up baked ground: every texture kind, every mask part, a stamp from the package
// and one cut from a made-up shipped texture, a cliff; the package's pictures in a fresh folder. The shipped files
// come from made-up archives (made.snap, the template's).
func madeUpBaked(t *testing.T) (*Map, ShippedFiles) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ground"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, w, h int, alpha bool) {
		b, err := bake.EncodePNG(w, h, stripes(w, h, alpha))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ground/stone.png", 24, 16, false)
	write("ground/mask.png", 32, 32, true)
	write("ground/rune.png", 12, 12, true)
	shipped, err := dds.Encode(16, 16, stripes(16, 16, true), "dxt5", 0)
	if err != nil {
		t.Fatal(err)
	}
	made := snap.Pack([]snap.File{{Name: "made/tex_D.dds", Data: shipped}})
	tmpl := snap.Pack([]snap.File{{Name: ground.TemplateEntry, Data: bakeTemplate(t)}})
	in := Inputs{Anshavar4: tmpl, Archive: func(name string) ([]byte, error) {
		if name == "made.snap" {
			return made, nil
		}
		return nil, fmt.Errorf("no %s", name)
	}}
	m := ring(t)
	m.Dir = dir
	soft, half := 3.0, 0.5
	m.Ground = Ground{Style: "baked", GroundBake: GroundBake{Texels: 1024, Tiles: 2,
		Layers: []GroundLayer{
			{Texture: TextureRef{Procedural: "dirt"}, Scale: 18, Vary: 0.1, Break: 0.5},
			{Texture: TextureRef{RGB: &[3]float64{40, 120, 50}}, Auto: "high", Soft: &soft, Noise: 2},
			{Texture: TextureRef{File: "ground/stone.png"}, Scale: 6, Rot: 20, Shapes: []MaskShape{{Lane: 1, Width: 8},
				{Circle: &Point{-150, -150}, R: 30}, {Circle: &Point{-150, -150}, R: 10, Erase: true}}, Noise: 1},
			{Texture: TextureRef{Game: "made/tex_D.dds", Archive: "made.snap"}, Scale: 9, Image: "ground/mask.png",
				Opacity: &half, Tint: &[3]float64{1.2, 1, 0.8}},
			{Texture: TextureRef{RGB: &[3]float64{60, 50, 40}}, Auto: "slope", Slope: 25},
			{Texture: TextureRef{RGB: &[3]float64{20, 20, 30}}, Auto: "chasm"},
			{Texture: TextureRef{RGB: &[3]float64{30, 90, 30}}, Auto: "bushes", Mirror: new(bool)},
			{Texture: TextureRef{RGB: &[3]float64{90, 80, 70}}, Auto: "floor", Opacity: &half},
		},
		Stamps: []GroundStamp{
			{Image: TextureRef{File: "ground/rune.png"}, Pos: Point{-60, -130}, Size: [2]float64{14, 14}, Rot: 15, Round: true, Soft: 2},
			{Image: TextureRef{Game: "made/tex_D.dds", Archive: "made.snap"}, Rect: &[4]int{4, 4, 8, 8}, Pos: Point{-170, 0},
				Size: [2]float64{10, 6}, Soft: 1, Mirror: new(bool)},
		},
		Cliff: &GroundCliff{Texture: TextureRef{File: "ground/stone.png"}, Scale: 8, Slope: 45},
	}}
	return m, in.Shipped()
}

func TestBakeMadeUp(t *testing.T) {
	run := func() ([]snap.File, []byte) {
		m, read := madeUpBaked(t)
		r, cm := Check(m, nil)
		if !r.OK {
			t.Fatalf("%+v", r.Findings)
		}
		files, mm, err := cm.GroundFiles(read)
		if err != nil {
			t.Fatal(err)
		}
		return files, mm
	}
	files, mm := run()
	var all bytes.Buffer
	for _, f := range files {
		t.Logf("%-62s %8d %s", f.Name, len(f.Data), digest(f.Data)[:16])
		all.WriteString(digest(f.Data))
	}
	t.Logf("minimap %d %s", len(mm), digest(mm)[:16])
	all.WriteString(digest(mm))
	if got := digest(all.Bytes()); got != RingBakedMadeUpSHA256 {
		t.Errorf("SHA-256 %s, pinned %s", got, RingBakedMadeUpSHA256)
	}
	again, mm2 := run()
	for i := range files {
		if !bytes.Equal(files[i].Data, again[i].Data) {
			t.Errorf("%s differs between two runs", files[i].Name)
		}
	}
	if !bytes.Equal(mm, mm2) {
		t.Error("the minimap differs between two runs")
	}
	// the model, 4 tiles and the cliff
	want := []string{EntriesFor("CM_Ring").Model, BakeTileEntry("CM_Ring", 0, 0), BakeTileEntry("CM_Ring", 1, 0),
		BakeTileEntry("CM_Ring", 0, 1), BakeTileEntry("CM_Ring", 1, 1), CliffEntry("CM_Ring")}
	if len(files) != len(want) {
		t.Fatalf("%d files", len(files))
	}
	for i, f := range files {
		if f.Name != want[i] {
			t.Errorf("file %d %s, %s expected", i, f.Name, want[i])
		}
	}
	info, err := bake.CheckModel(files[0].Data)
	if err != nil || info.Materials != 5 {
		t.Errorf("%+v %v", info, err)
	}
	if in, err := dds.ReadInfo(files[1].Data); err != nil || in.W != 1024 {
		t.Errorf("tile %+v %v", in, err)
	}
	if in, err := dds.ReadInfo(files[5].Data); err != nil || in.W != 48 || in.H != 32 {
		t.Errorf("cliff %+v %v", in, err)
	}
}

func TestBakeErrors(t *testing.T) {
	m, read := madeUpBaked(t)
	m.Ground.Stamps[1].Rect = &[4]int{10, 10, 8, 8}
	_, cm := Check(m, nil)
	if _, _, err := cm.GroundFiles(read); err == nil || !strings.Contains(err.Error(), "rect") {
		t.Errorf("a rectangle past the picture: %v", err)
	}
	m, read = madeUpBaked(t)
	m.Ground.Layers[3].Texture.Archive = "other.snap"
	_, cm = Check(m, nil)
	if _, _, err := cm.GroundFiles(read); err == nil {
		t.Error("an archive the inputs don't have read")
	}
	m, _ = madeUpBaked(t)
	if err := os.WriteFile(filepath.Join(m.Dir, "ground", "mask.png"), []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, _ := Check(m, nil); !has(r, "ground.file", Error) {
		t.Errorf("a broken picture passes: %+v", r.Findings)
	}
	for _, c := range []struct {
		p  string
		ok bool
	}{{"ground/a.png", true}, {"a.PNG", true}, {"../a.png", false}, {"/a.png", false}, {`ground\a.png`, false},
		{"ground/./a.png", false}, {"C:/a.png", false}, {"a.jpg", false}, {"", false}} {
		if err := PackagePath(c.p, ".png"); (err == nil) != c.ok {
			t.Errorf("%q: %v", c.p, err)
		}
	}
}

// TestMaskShapeSoft: a shape's own soft replaces the layer's for its edge (a brush stroke's softness), the rule
// checks its range, and a shape leaves the mask alone outside its reach.
func TestMaskShapeSoft(t *testing.T) {
	m, read := madeUpBaked(t)
	zero, wide, tooWide := 0.0, 8.0, 60.0
	stroke := MaskShape{Path: []Point{{-100, -100}, {-60, -100}}, Width: 10}
	l := GroundLayer{Texture: TextureRef{RGB: &[3]float64{1, 2, 3}}, Soft: &zero, Shapes: []MaskShape{stroke}}
	r, cm := Check(m, nil)
	if cm == nil || !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	b, err := cm.Bake(read)
	if err != nil {
		t.Fatal(err)
	}
	f, err := cm.layerMask(1, l, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f(-80, -97) != 1 || f(-80, -93) != 0 || f(80, 97) != 1 || f(0, 0) != 0 {
		t.Errorf("hard edge: %g %g, mirror %g", f(-80, -97), f(-80, -93), f(80, 97))
	}
	l.Shapes[0].Soft = &wide
	f, _ = cm.layerMask(1, l, b, nil)
	if v := f(-80, -93); !(v > 0.05 && v < 0.5) || f(-80, -90) != 0 || f(-80, -100) != 1 {
		t.Errorf("the stroke's soft 8 m: %g at 2 m out, %g at 5 m out", v, f(-80, -90))
	}
	l.Shapes = append(l.Shapes, MaskShape{Circle: &Point{-80, -100}, R: 3, Erase: true, Soft: &zero})
	f, _ = cm.layerMask(1, l, b, nil)
	if f(-80, -100) != 0 || f(-70, -100) != 1 {
		t.Errorf("an erase stroke: %g, beside it %g", f(-80, -100), f(-70, -100))
	}
	m.Ground.Layers[2].Shapes[0].Soft = &tooWide
	if r, _ := Check(m, nil); !has(r, "ground.soft", Error) {
		t.Errorf("a shape's soft of 60 m passes: %+v", r.Findings)
	}
}

// TestBakedFormat: a baked ground section reads strictly and writes back the same members.
func TestBakedFormat(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(ringDir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(b)
	if err != nil || !m.Baked() || len(m.Ground.Layers) == 0 {
		t.Fatalf("%v", err)
	}
	bad := strings.Replace(string(b), `"style": "baked",`, `"style": "baked", "colour": 1,`, 1)
	if _, err := Parse([]byte(bad)); err == nil {
		t.Error("an unknown ground member read")
	}
	plain := ring(t)
	plain.Ground = Ground{Style: "baked"}
	if n := len(plain.BakeLayers()); n != 2 || plain.BakeTexels() != DefaultTexels || plain.BakeTiles() != DefaultTiles {
		t.Errorf("defaults: %d layers", n)
	}
}
