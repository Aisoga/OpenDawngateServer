package ground

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"testing"
	"time"

	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/testgame"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func floats(v ...[]float64) []byte {
	var out []byte
	for _, x := range v {
		for _, f := range x {
			out = binary.LittleEndian.AppendUint64(out, math.Float64bits(f))
		}
	}
	return out
}

func triples(v [][3]float64) []byte {
	var out []byte
	for _, t := range v {
		out = append(out, floats(t[:])...)
	}
	return out
}

// The made-up inputs; the Python reference's pin script builds the same.

// pattern: RGBA rows top to bottom, flat opaque blocks, flat half-transparent grey blocks, gradients with alpha.
func pattern(w, h int) []byte {
	out := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			bx, by := x/4, y/4
			var px [4]int
			switch {
			case (bx+2*by)%5 == 0:
				px = [4]int{200, 40, 90, 255}
			case (bx+by)%5 == 1:
				px = [4]int{17, 17, 17, 128}
			default:
				a := 255
				if bx%2 != 0 {
					a = (x*13 + y*5) % 256
				}
				px = [4]int{x * 255 / max(1, w-1), y * 255 / max(1, h-1), (x*37 + y*91) % 256, a}
			}
			o := (y*w + x) * 4
			for c := 0; c < 4; c++ {
				out[o+c] = byte(px[c])
			}
		}
	}
	return out
}

func lcg(n int, seed uint32) []byte {
	out := make([]byte, n)
	s := seed
	for i := range out {
		s = (s*1103515245 + 12345) & 0x7FFFFFFF
		out[i] = byte(s >> 16)
	}
	return out
}

// losCells: 0 open, 128 blocked; a diagonal band, a disc at the centre, a corner square, a few blocked specks.
func losCells(w, h int) []byte {
	out := make([]byte, w*h)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			di, dj := i-w/2, j-h/2
			open := abs(i-j) <= 5 || di*di+dj*dj <= 100 || (i < 12 && j < 12)
			if open && (i*5+j*3)%17 == 0 {
				open = false
			}
			if !open {
				out[j*w+i] = 128
			}
		}
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func nd(typ string, kv ...any) *node {
	n := &node{typ: typ, f: map[string]any{}}
	for i := 0; i < len(kv); i += 2 {
		n.f[kv[i].(string)] = kv[i+1]
	}
	return n
}

// madeUpTemplate: a version-5 model with three materials (two one-texture vertex-lit ones), a 24-byte and a
// 32-byte mesh, two parts and a one-bone skeleton.
func madeUpTemplate() *tree {
	el := func(stream, off uint16, typ, usage byte) []byte {
		b := binary.LittleEndian.AppendUint16(nil, stream)
		b = binary.LittleEndian.AppendUint16(b, off)
		return append(b, typ, 0, usage, 0)
	}
	cat := func(p ...[]byte) []byte { return bytes.Join(p, nil) }
	decl32 := cat(el(0, 0, 2, 0), el(0, 12, 8, 1), el(0, 16, 8, 2), el(0, 20, 1, 4), el(0, 28, 8, 5), el(0xFF, 0, 17, 0))
	decl24 := cat(el(0, 0, 2, 0), el(0, 12, 2, 1), el(0xFF, 0, 17, 0))
	u := func(v int) uint32 { return uint32(v) }
	tex := func(slot, path string) *node {
		return nd("Texture", "slot", slot, "slot_hash", fnv1(slot), "path", path, "path_hash", fnv1(path))
	}
	mat := func(i int, shader string, texs ...*node) *node {
		return nd("Material", "shader", shader, "shader_hash", fnv1(shader), "technique", "opaque",
			"technique_hash", fnv1("opaque"), "flags", u(1+i), "param_count", u(0),
			"params", []*node{nd("Param", "name", "tint", "name_hash", fnv1("tint"), "offset", u(0), "size", u(4))},
			"value_count", u(0), "values", []float32{1.0, 0.5, 0.25, float32(1 + i)}, "texture_count", u(0),
			"textures", texs, "stream_count", u(0), "streams", []*node{})
	}
	bounds := func(k int) []float32 { return []float32{0.5 * float32(k), 0.25, -1.0, 2.0, 1.0, 1.5, 0.75, 4.0} }
	mesh := func(i int, decl []byte, stride int) *node {
		name := fmt.Sprintf("madeup_mesh_%d", i)
		return nd("Mesh", "bounds", bounds(i), "name", name, "name_hash", fnv1(name), "material", u(i),
			"decl_count", u(len(decl)/8), "decl", decl, "vertices", lcg(3*stride, uint32(7+i)),
			"indices", []byte{0, 0, 1, 0, 2, 0}, "vertex_count", u(3), "index_count", u(3), "target_count", u(0),
			"targets", []*node{})
	}
	part := func(i int) *node {
		pal := make([]uint32, 16)
		for k := range pal {
			pal[k] = uint32(i + k)
		}
		return nd("Part", "bounds", bounds(10+i), "mesh", u(i), "submesh_count", u(0),
			"submeshes", []*node{nd("Submesh", "bounds", bounds(20+i), "index_start", u(0), "index_count", u(3),
				"material", u(i), "palette", pal)})
	}
	model := nd("Model", "bounds", bounds(30), "name", "madeup_model", "name_hash", fnv1("madeup_model"),
		"material_count", u(0), "materials", []*node{
			mat(0, "labsVertLit", tex("diffuseMap", "madeup/a_D.dds")),
			mat(1, "labsVertLit", tex("diffuseMap", "madeup/b_D.dds")),
			mat(2, "labsLit", tex("diffuseMap", "madeup/c_D.dds"), tex("normalMap", "madeup/c_N.dds"))},
		"mesh_count", u(0), "meshes", []*node{mesh(0, decl24, 24), mesh(1, decl32, 32)},
		"part_count", u(0), "parts", []*node{part(0), part(1)}, "unk_40", u(7))
	bone := nd("Bone", "name", "root", "name_hash", fnv1("root"), "parent", int32(-1), "flags", u(0),
		"inverse_bind", []float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1},
		"rotation", []float32{0, 0, 0, 1}, "translation", []float32{0, 0, 0, 1}, "scale", []float32{1, 1, 1, 1})
	skel := nd("Skeleton", "bone_count", u(0), "count2", u(1), "bones", []*node{bone})
	return &tree{tag: "bmdl", version: 5, align: 16,
		root: nd("Root", "model", model, "skeleton", skel, "anim_count", u(0), "anims", []*node{})}
}

// pins: the Python reference's values for the made-up inputs.
var pins = map[string]string{
	"template":       "7a2b0ac42ce3b774b41f6113ad6812cc3fb89423de9178459d0d5527f31cc551",
	"field":          "77316bd901c96eaeef37bd0163bba969d40c31353f10b012a8fb7207fecb4ffb",
	"ground_d":       "76c8b216cced2106322c6547b3084220b1c02d08c40f0ff8073ab02ce389e5e7",
	"ground_z":       "45b4c98fdfd5493d527810a2f53063ef9f2acddd4a2f2f13952db852f594f68f",
	"ground_normal":  "b2684fea435f6e6a5a8231a2f5ad244e0af95163ef885f11c5185d7e02ea30c8",
	"ground_rgb":     "212b6e8ff76faa1e725994dac64a3e554498f5e3c7123be1ac71549ee2268f0a",
	"model":          "6c20e788e9d506a615d840c4b8741cfa7af80a494c1aa167575f3a3f786103eb",
	"grass":          "46521d637b723fba5e9b4465702ae350cbe852fada7cf481cc78cd15e39ffb13",
	"dirt_pixels_64": "901cebd45923d8e8926a4cde6d3770dc12fd6752699109e84dbaee34f0aa296a",
	"dirt_64":        "817b0565cdaaf746b292cac84639e4088fd3c63770ccd0f8d942edd73e828603",
	"dirt":           "6d4119412b91fcb7fa456be8e582271fab6157705809c891d15f63b69990f46b",
}

// The reference build's outputs from the game's files.
const (
	refModel = "db13c73c769d89f93a7302170f3c498be4c93f46f553b84689d902d10cdd2d12"
	refDirt  = "6d4119412b91fcb7fa456be8e582271fab6157705809c891d15f63b69990f46b"
	refGrass = "1133332bab305c24d82acbcb5f76b25b20a4aeae28255e221aa93e80e8c98fd9"
)

func check(t *testing.T, name string, b []byte) {
	t.Helper()
	if got, want := sum(b), pins[name]; got != want {
		t.Errorf("%s: sha256 %s, the Python reference %s", name, got, want)
	}
}

func TestMadeUp(t *testing.T) {
	tmpl, err := writeTree(madeUpTemplate())
	if err != nil {
		t.Fatal(err)
	}
	check(t, "template", tmpl)
	tr, err := parseTree(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := writeTree(tr); err != nil || !bytes.Equal(again, tmpl) {
		t.Fatalf("the made-up template does not round-trip (%v)", err)
	}
	field, err := NewField(64, 64, losCells(64, 64), 300.0)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "field", floats(field.D))
	g := NewGround(field, 100, 4.0)
	check(t, "ground_d", floats(g.D))
	check(t, "ground_z", floats(g.Z))
	check(t, "ground_normal", triples(g.Normal))
	check(t, "ground_rgb", triples(g.RGB))
	model, err := BuildModel(tmpl, g, [2]string{DirtEntry, GrassEntry})
	if err != nil {
		t.Fatal(err)
	}
	info, err := CheckModel(model)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "model", model)
	if info.Meshes != 4 || info.Triangles != 20000 {
		t.Errorf("%d meshes, %d triangles; the Python reference 4, 20000", info.Meshes, info.Triangles)
	}
	src, err := dds.Encode(32, 32, pattern(32, 32), "dxt5", 0)
	if err != nil {
		t.Fatal(err)
	}
	grass, err := GrassDDS(src)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "grass", grass)
	check(t, "dirt_pixels_64", DirtPixels(64))
	small, err := DirtDDS(64)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "dirt_64", small)
	start := time.Now()
	dirt, err := DirtDDS(DirtSize)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "dirt", dirt)
	t.Logf("the %d x %d dirt texture in %v", DirtSize, DirtSize, time.Since(start))
}

func TestTemplateChecks(t *testing.T) {
	tr := madeUpTemplate()
	tr.root.f["model"].(*node).f["materials"].([]*node)[1].f["shader"] = "labsLit"
	tmpl, err := writeTree(tr)
	if err != nil {
		t.Fatal(err)
	}
	field, err := NewField(64, 64, losCells(64, 64), 300.0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildModel(tmpl, NewGround(field, 8, 4.0), [2]string{DirtEntry, GrassEntry}); err == nil {
		t.Error("a template without two vertex-lit materials was taken")
	}
	if _, err := NewField(4, 4, make([]byte, 16), 10); err == nil {
		t.Error("an all-open map was taken")
	}
	if _, err := parseTree(tmpl[:len(tmpl)-1]); err == nil {
		t.Error("a cut model was parsed")
	}
}

func TestHelpers(t *testing.T) {
	if fmod(-0.5, 2.0) != 1.5 || imod(-3, 4) != 1 || roundInt(2.5) != 2 || roundInt(3.5) != 4 {
		t.Error("the Python arithmetic helpers")
	}
	if fnv1("ABC") != fnv1("abc") {
		t.Error("fnv1 ignores case")
	}
	if got := hypot(3, 4); got != 5 {
		t.Errorf("hypot(3, 4) = %v", got)
	}
	// the Python reference's math.hypot of made-up pairs
	for _, c := range [][3]float64{
		{-0x1.c88340f06f0f0p+8, 0x1.96c3a11e134ccp+7, 0x1.f3c4a0837365dp+8},
		{0x1.43b61da2f3fe0p+3, -0x1.22e8b4b64df7cp+8, 0x1.2315b807acd54p+8},
		{-0x1.9f51dc6b5f74bp+8, -0x1.4a4e4cb92e519p+8, 0x1.0953915285d9bp+9},
		{0x1.dff3f05466b36p+8, 0x1.574e3a7b9d240p+5, 0x1.e1de165bfceb9p+8},
		{0x1.5f4aec2e41eacp+8, -0x1.7585c7bd222b8p+5, 0x1.6261c3830454ep+8},
		{0x1.13c1db281ce26p+8, 0x1.55ee4cbed9a30p+8, 0x1.b7454e7a83b58p+8},
		{0x1.88a823b524e08p+6, 0x1.7da9b5fb5213ap+8, 0x1.8a15b35b7698dp+8},
		{-0x1.cc7a8e39097fcp+7, 0x1.b6aa5295ec260p+6, 0x1.fe0bea77db567p+7},
	} {
		if got := hypot(c[0], c[1]); got != c[2] {
			t.Errorf("hypot(%x, %x) = %x, the Python reference %x", c[0], c[1], got, c[2])
		}
	}
}

// TestGame builds the ground from the game's archives and compares it with the reference build's output.
func TestGame(t *testing.T) {
	images := testgame.Read(t, los.Archive)
	tmplArchive := testgame.Read(t, TemplateArchive)
	grassArchive := testgame.Read(t, GrassArchive)
	start := time.Now()
	model, dirt, grass, err := FromArchives(images, tmplArchive, grassArchive)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("built in %v: model %d bytes, dirt %d, grass %d", time.Since(start), len(model), len(dirt), len(grass))
	for _, c := range []struct {
		name string
		b    []byte
		want string
	}{{ModelEntry, model, refModel}, {DirtEntry, dirt, refDirt}, {GrassEntry, grass, refGrass}} {
		if got := sum(c.b); got != c.want {
			t.Errorf("%s: sha256 %s, the reference build %s", c.name, got, c.want)
		}
	}
}
