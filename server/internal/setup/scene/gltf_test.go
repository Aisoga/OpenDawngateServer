package scene

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"

	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/dds"
)

func madeUpModel() *bmdl.Model {
	return &bmdl.Model{
		Name: "made_up",
		Materials: []bmdl.Material{
			{Shader: "labsVertLit", Textures: []bmdl.Texture{{Slot: "diffuseMap", Path: "Env/Tex/Dirt_D.dds"}},
				Params: map[string][]float32{"DiffuseTint": {0.5, 1, 1}}},
			{Shader: "labsUnlitMaskedScrollAdditive", Textures: []bmdl.Texture{{Slot: "diffuseMap", Path: "env/tex/none.dds"}}},
		},
		Meshes: []bmdl.Mesh{{
			Name:      "quad",
			Positions: [][3]float32{{0, 0, 0}, {2, 0, 0}, {2, 3, 0}, {0, 3, 1}},
			Normals:   [][3]float32{{0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 0, 2}},
			UVs:       [][2]float32{{0, 0}, {1, 0}, {1, 1}, {0, 1}},
			Colors:    [][4]uint8{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {9, 9, 9, 9}},
			Indices:   []uint16{0, 1, 2, 0, 2, 3},
		}},
		Parts: []bmdl.Part{{Mesh: 0, Submeshes: []bmdl.Submesh{{Start: 0, Count: 3, Material: 0}, {Start: 3, Count: 3, Material: 1}}}},
	}
}

func TestGenerated(t *testing.T) {
	dir := t.TempDir()
	px := make([]byte, 16*16*4)
	for i := range px {
		px[i] = byte(i)
	}
	tex, err := dds.Encode(16, 16, px, "dxt1", 1)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	x := &Exporter{Dir: dir, MaxTexture: 8, Read: func(p string) ([]byte, error) {
		reads++
		if p == "Env/Tex/Dirt_D.dds" {
			return tex, nil
		}
		return nil, fs.ErrNotExist
	}}
	info, err := x.Generated("made_up", "tag1", madeUpModel())
	if err != nil {
		t.Fatal(err)
	}
	if info.File != "made_up.gltf" || info.Meshes != 1 || info.Triangles != 2 || len(info.Textures) != 1 ||
		len(info.Missing) != 1 || info.Missing[0] != "env/tex/none.dds" || info.Max != [3]float32{2, 3, 1} {
		t.Fatalf("info %+v", info)
	}
	var doc gltfDoc
	b, err := os.ReadFile(filepath.Join(dir, "made_up.gltf"))
	if err != nil || json.Unmarshal(b, &doc) != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if doc.Asset.Version != "2.0" || len(doc.Meshes) != 1 || len(doc.Meshes[0].Primitives) != 2 ||
		len(doc.Materials) != 2 || doc.ExtensionsUsed[0] != "KHR_materials_unlit" {
		t.Fatalf("doc %s", b)
	}
	m0, m1 := doc.Materials[0], doc.Materials[1]
	if m0.Extras.Draw != "opaque" || !m0.Extras.VertColor || m0.PBR.BaseColorTexture == nil ||
		m0.PBR.BaseColorFactor[0] != 0.5 || m1.Extras.Draw != "add" || !m1.Extras.Unlit || m1.AlphaMode != "BLEND" {
		t.Errorf("materials %+v %+v", m0, m1)
	}
	// the positions as glTF's Y up: game (x, y, z) -> (x, z, -y); the normals unit length
	bin, err := os.ReadFile(filepath.Join(dir, doc.Buffers[0].URI))
	if err != nil || len(bin) != doc.Buffers[0].ByteLength {
		t.Fatalf("bin %d bytes, %v", len(bin), err)
	}
	f := func(view, k int) float32 {
		v := doc.BufferViews[view]
		return math.Float32frombits(binary.LittleEndian.Uint32(bin[v.ByteOffset+4*k:]))
	}
	pos := doc.Accessors[doc.Meshes[0].Primitives[0].Attributes["POSITION"]]
	if f(pos.BufferView, 9) != 0 || f(pos.BufferView, 10) != 1 || f(pos.BufferView, 11) != -3 ||
		pos.Max[1] != 1 || pos.Min[2] != -3 {
		t.Errorf("position 3 (%v, %v, %v), bounds %v %v", f(pos.BufferView, 9), f(pos.BufferView, 10),
			f(pos.BufferView, 11), pos.Min, pos.Max)
	}
	nrm := doc.Accessors[doc.Meshes[0].Primitives[0].Attributes["NORMAL"]]
	if f(nrm.BufferView, 10) != 1 {
		t.Errorf("normal 3's y %v, want 1", f(nrm.BufferView, 10))
	}
	second := doc.Accessors[doc.Meshes[0].Primitives[1].Indices]
	if second.ByteOffset != 6 || second.Count != 3 || second.ComponentType != glUnsignedShort {
		t.Errorf("the second submesh's indices %+v", second)
	}
	// the texture: halved to MaxTexture, a PNG under textures/
	png, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(info.Textures[0])))
	if err != nil || string(png[1:4]) != "PNG" || binary.BigEndian.Uint32(png[16:]) != 8 {
		t.Errorf("texture %s: %v", info.Textures[0], err)
	}
	// the same content again: kept; another tag: written again
	n := reads
	if again, err := x.Generated("made_up", "tag1", madeUpModel()); err != nil || !again.Cached || reads != n {
		t.Errorf("not kept: %+v %v (%d reads)", again, err, reads-n)
	}
	if again, err := x.Generated("made_up", "tag2", madeUpModel()); err != nil || again.Cached {
		t.Errorf("a changed model was kept: %+v %v", again, err)
	}
	// nothing to draw
	if _, err := x.Generated("empty", "t", &bmdl.Model{}); err == nil {
		t.Error("an empty model was written")
	}
	if _, err := x.Model("missing.bmdl"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing model: %v", err)
	}
}

func TestDrawModeAndNames(t *testing.T) {
	for _, c := range []struct {
		shader, draw string
		unlit, vc    bool
	}{
		{"labsVertLit", "opaque", false, true},
		{"labsHero", "opaque", false, false},
		{"labsFlora2", "mask", false, false},
		{"labsFloraVertColor", "mask", false, false},
		{"labsUnlitBlend", "blend", true, false},
		{"labsUnlitMaskedScroll", "blend", true, false},
		{"labsUnlitEnvLayeredScroll", "add", true, false},
		{"labsSphereMaskedScrollAdditive", "add", true, false},
		{"labsGenericVertColors", "opaque", false, true},
		{"labsChromeVertColor", "opaque", false, false},
	} {
		d, u, vc := DrawMode(c.shader)
		if d != c.draw || u != c.unlit || vc != c.vc {
			t.Errorf("%s: %s %v %v", c.shader, d, u, vc)
		}
	}
	if n := FileName(`Titans\Titan01\models\titan01.bmdl`, ".gltf"); n != "titans_titan01_models_titan01.gltf" {
		t.Errorf("FileName %s", n)
	}
	if n := FileName("Env/a b+c.dds", ""); n != "env_a_b_c" {
		t.Errorf("FileName %s", n)
	}
}
