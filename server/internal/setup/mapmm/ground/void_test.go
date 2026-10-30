package ground

import (
	"encoding/binary"
	"math"
	"testing"

	"dawngate/server/internal/setup/bmdl"
)

// voidModelSHA256 pins the made-up ground with a cut, a backdrop and a mist plane (TestVoidMadeUp).
const voidModelSHA256 = "73b96b4d15ba649bdf1056c6260eb136e415a53e0dc90cf18d83e5e8bd3098d6"

// madeUpFogFace is a one-material model of the fog sphere's kind: labsFogFace, four values, no texture.
func madeUpFogFace() []byte {
	u := func(v int) uint32 { return uint32(v) }
	mat := nd("Material", "shader", "labsFogFace", "shader_hash", bmdl.FNV1("labsFogFace"), "technique", "blend",
		"technique_hash", bmdl.FNV1("blend"), "flags", u(1), "param_count", u(0),
		"params", []*bmdl.Node{nd("Param", "name", "DiffuseTint", "name_hash", bmdl.FNV1("DiffuseTint"), "offset", u(0), "size", u(3))},
		"value_count", u(0), "values", []float32{1, 1, 1, 110}, "texture_count", u(0), "textures", []*bmdl.Node{},
		"stream_count", u(0), "streams", []*bmdl.Node{})
	tr := madeUpTemplate()
	m := tr.Root.F["model"].(*bmdl.Node)
	m.F["materials"] = []*bmdl.Node{mat}
	b, err := bmdl.Write(tr)
	if err != nil {
		panic(err)
	}
	return b
}

// TestVoidMadeUp builds the made-up ground cut 2 m past the walkable edge with a black backdrop and a mist plane:
// the cut drops the triangles wholly past it (a tile it empties keeps one degenerate triangle), the planes come
// after the tiles on materials 2 and 3, the mist's material is the fog face's with its values set, and the bytes
// are pinned.
func TestVoidMadeUp(t *testing.T) {
	tmpl, err := bmdl.Write(madeUpTemplate())
	if err != nil {
		t.Fatal(err)
	}
	field, err := NewField(64, 64, losCells(64, 64), 300.0)
	if err != nil {
		t.Fatal(err)
	}
	full := NewGround(field, 100, 4.0)
	whole, err := CheckModel(mustModel(t, tmpl, full))
	if err != nil {
		t.Fatal(err)
	}
	mist, err := FogFaceMaterial(madeUpFogFace(), [3]float64{0.467, 0.824, 0.859}, 10)
	if err != nil {
		t.Fatal(err)
	}
	spec := MM
	spec.Cut = 2
	spec.Backdrop = &Backdrop{Z: -50, Half: BackdropHalf, RepeatX: BackdropRepeat, RepeatY: BackdropRepeat,
		Texture: "madeup/backdrop_D.dds"}
	spec.Mist = &Backdrop{Z: -0.2, Half: BackdropHalf, RepeatX: BackdropRepeat, RepeatY: BackdropRepeat, Material: mist}
	g := NewGroundSpec(field, 100, 4.0, spec)
	model := mustModel(t, tmpl, g)
	info, err := CheckModel(model)
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	for k := range g.D {
		if g.D[k] <= spec.Cut {
			kept++
		}
	}
	planes := 2 * 2 * BackdropCells * BackdropCells
	if info.Meshes != whole.Meshes+2 || info.Triangles-planes >= whole.Triangles || kept == 0 {
		t.Errorf("cut: %+v, the whole ground %+v (%d vertices within the cut)", info, whole, kept)
	}
	tr, err := bmdl.Parse(model)
	if err != nil {
		t.Fatal(err)
	}
	m := tr.Root.F["model"].(*bmdl.Node)
	mats := m.F["materials"].([]*bmdl.Node)
	meshes := m.F["meshes"].([]*bmdl.Node)
	if len(mats) != 4 || mats[2].F["shader"] != "labsVertLit" || mats[3].F["shader"] != "labsFogFace" {
		t.Fatalf("materials %d", len(mats))
	}
	if p := mats[2].F["textures"].([]*bmdl.Node)[0].F["path"]; p != "madeup/backdrop_D.dds" {
		t.Errorf("the backdrop's texture %v", p)
	}
	if v := mats[3].F["values"].([]float32); v[0] != float32(0.467) || v[3] != 10 {
		t.Errorf("the mist's values %v", v)
	}
	last := meshes[len(meshes)-1]
	if last.F["name"] != "mm_v3_backdrop_1" || bmdl.U32(last.F["material"]) != 3 {
		t.Errorf("the last mesh %v on material %d", last.F["name"], bmdl.U32(last.F["material"]))
	}
	vb := last.F["vertices"].([]byte)
	if z := f32At(vb, 8); z != float32(-0.2) {
		t.Errorf("the mist's first vertex at z %g", z)
	}
	if got := sum(model); got != voidModelSHA256 {
		t.Errorf("SHA-256 %s, pinned %s", got, voidModelSHA256)
	}
}

func mustModel(t *testing.T, tmpl []byte, g *Ground) []byte {
	t.Helper()
	b, err := BuildModel(tmpl, g, [2]string{DirtEntry, GrassEntry})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func f32At(b []byte, off int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(b[off:]))
}
