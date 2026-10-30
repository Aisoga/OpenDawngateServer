package bmdl

import (
	"encoding/binary"
	"math"
	"testing"
)

func nd(typ string, kv ...any) *Node {
	n := &Node{Type: typ, F: map[string]any{}}
	for i := 0; i+1 < len(kv); i += 2 {
		n.F[kv[i].(string)] = kv[i+1]
	}
	return n
}

// declElem is one D3DVERTEXELEMENT9: stream 0, offset, type, method 0, usage, usage index 0.
func declElem(off int, typ, usage byte) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint16(b[2:], uint16(off))
	b[4], b[6] = typ, usage
	return b
}

// madeUp is a one-mesh, one-part model in the vertex-lit layout: FLOAT3 position, UBYTE4N normal, FLOAT2 UV,
// UBYTE4N colour (32 bytes with the tangent at 16), two triangles split into two submeshes.
func madeUp() *Tree {
	u := func(v int) uint32 { return uint32(v) }
	var decl []byte
	decl = append(decl, declElem(0, 2, 0)...)
	decl = append(decl, declElem(12, 8, 1)...)
	decl = append(decl, declElem(16, 8, 2)...)
	decl = append(decl, declElem(20, 1, 4)...)
	decl = append(decl, declElem(28, 8, 5)...)
	end := make([]byte, 8)
	binary.LittleEndian.PutUint16(end, 0xFF)
	decl = append(decl, end...)
	var vb []byte
	le := binary.LittleEndian
	pts := [][3]float32{{0, 0, 0}, {2, 0, 0}, {2, 3, 0}, {0, 3, 1}}
	for k, p := range pts {
		for _, c := range p {
			vb = le.AppendUint32(vb, math.Float32bits(c))
		}
		vb = append(vb, 127, 127, 254, 0, 254, 127, 127, 0) // normal (0, 0, 1), tangent (1, 0, 0)
		vb = le.AppendUint32(vb, math.Float32bits(float32(k)/4))
		vb = le.AppendUint32(vb, math.Float32bits(0.5))
		vb = append(vb, byte(10*k), 20, 30, 255)
	}
	ib := make([]byte, 12)
	for q, v := range []uint16{0, 1, 2, 0, 2, 3} {
		le.PutUint16(ib[2*q:], v)
	}
	bounds := make([]float32, 8)
	tex := nd("Texture", "slot", "diffuseMap", "slot_hash", FNV1("diffuseMap"), "path", "env/t/a_D.dds", "path_hash",
		FNV1("env/t/a_D.dds"))
	mat := func(shader string) *Node {
		return nd("Material", "shader", shader, "shader_hash", FNV1(shader), "technique", "hlslShader1",
			"technique_hash", FNV1("hlslShader1"), "flags", u(0), "param_count", u(0),
			"params", []*Node{nd("Param", "name", "DiffuseTint", "name_hash", FNV1("DiffuseTint"), "offset", u(4),
				"size", u(12))}, "value_count", u(0), "values", []float32{9, 0.5, 0.25, 1},
			"texture_count", u(0), "textures", []*Node{tex}, "stream_count", u(0), "streams", []*Node{})
	}
	mesh := nd("Mesh", "bounds", bounds, "name", "quad", "name_hash", FNV1("quad"), "material", u(0), "decl_count", u(5),
		"decl", decl, "vertices", vb, "indices", ib, "vertex_count", u(4), "index_count", u(6))
	sub := func(start, mat int) *Node {
		return nd("Submesh", "bounds", bounds, "index_start", u(start), "index_count", u(3), "material", u(mat),
			"palette", make([]uint32, 16))
	}
	part := nd("Part", "bounds", bounds, "mesh", u(0), "submesh_count", u(0), "submeshes", []*Node{sub(0, 0), sub(3, 1)})
	model := nd("Model", "bounds", bounds, "name", "made_up", "name_hash", FNV1("made_up"), "material_count", u(0),
		"materials", []*Node{mat("labsVertLit"), mat("labsFlora2")}, "mesh_count", u(0), "meshes", []*Node{mesh},
		"part_count", u(0), "parts", []*Node{part}, "unk_40", u(0))
	return &Tree{Tag: "bmdl", Version: 4, Align: 4,
		Root: nd("Root", "model", model, "skeleton", nil, "anim_count", u(0), "anims", []*Node{})}
}

func TestDecode(t *testing.T) {
	raw, err := Write(madeUp())
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "made_up" || len(m.Materials) != 2 || len(m.Meshes) != 1 || len(m.Parts) != 1 || m.Bones != 0 {
		t.Fatalf("decoded %+v", m)
	}
	mt := m.Materials[0]
	if mt.Shader != "labsVertLit" || len(mt.Textures) != 1 || mt.Textures[0] != (Texture{"diffuseMap", "env/t/a_D.dds"}) {
		t.Errorf("material %+v", mt)
	}
	if tint := mt.Params["DiffuseTint"]; len(tint) != 3 || tint[0] != 0.5 || tint[1] != 0.25 || tint[2] != 1 {
		t.Errorf("DiffuseTint %v (byte offset 4, 12 bytes)", tint)
	}
	me := m.Meshes[0]
	if len(me.Positions) != 4 || me.Positions[3] != [3]float32{0, 3, 1} {
		t.Errorf("positions %v", me.Positions)
	}
	if n := me.Normals[0]; math.Abs(float64(n[2]-1)) > 1e-6 || n[0] != 0 || n[1] != 0 {
		t.Errorf("normal %v", n)
	}
	if me.UVs[2] != [2]float32{0.5, 0.5} || me.Colors[3] != [4]uint8{30, 20, 30, 255} {
		t.Errorf("uv %v colour %v", me.UVs[2], me.Colors[3])
	}
	if len(me.Indices) != 6 || me.Indices[5] != 3 {
		t.Errorf("indices %v", me.Indices)
	}
	if s := m.Parts[0].Submeshes; len(s) != 2 || s[1] != (Submesh{Start: 3, Count: 3, Material: 1}) {
		t.Errorf("submeshes %v", s)
	}
	// a broken index is refused
	bad := madeUp()
	ib := bad.Root.F["model"].(*Node).F["meshes"].([]*Node)[0].F["indices"].([]byte)
	binary.LittleEndian.PutUint16(ib[10:], 4)
	raw, _ = Write(bad)
	if _, err := Decode(raw); err == nil {
		t.Error("an index past the vertices was accepted")
	}
	if _, err := Decode(raw[:40]); err == nil {
		t.Error("a cut file was accepted")
	}
}

func TestElementTypes(t *testing.T) {
	b := []byte{0xFF, 0x7F, 0x01, 0x80}
	if v := element(b, 9); v[0] != 1 || v[1] != -32767.0/32767 {
		t.Errorf("SHORT2N %v", v)
	}
	if v := element([]byte{0x00, 0x3C, 0x00, 0xC0}, 15); v[0] != 1 || v[1] != -2 {
		t.Errorf("FLOAT16_2 %v", v)
	}
	if element(b, 13) != nil {
		t.Error("UDEC3 is not read")
	}
}
