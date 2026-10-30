package mapc

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/mapc/bake"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// TestVoidRules: a good void passes; each broken one fails its rule (CM_Ring with a void).
func TestVoidRules(t *testing.T) {
	good := func() *Void {
		return &Void{Cut: 1.5, Backdrop: &VoidBackdrop{Z: -50, Texture: TextureRef{RGB: &[3]float64{0, 0, 0}}},
			Mist: &VoidMist{Z: -0.2, Range: 10, Color: [3]float64{0.467, 0.824, 0.859}},
			Fog:  &VoidFog{Start: 10, End: -225, Color: &[3]float64{0.3, 0.34, 0.45}}}
	}
	m := ring(t)
	m.Void = good()
	if r, _ := Check(m, nil); !r.OK || len(r.Findings) != 0 {
		t.Fatalf("the good void: %+v", r.Findings)
	}
	m.Void = &Void{} // an empty section: nothing to check, nothing changes
	if r, _ := Check(m, nil); !r.OK || len(r.Findings) != 0 {
		t.Fatalf("an empty void: %+v", r.Findings)
	}
	for _, c := range []struct {
		rule, sev string
		edit      func(v *Void, m *Map)
	}{
		{"void.cut", Error, func(v *Void, m *Map) { v.Cut = -1 }},
		{"void.cut", Error, func(v *Void, m *Map) { v.Cut = 0.2 }},
		{"void.cut", Error, func(v *Void, m *Map) { v.Cut = 80 }},
		{"void.backdrop", Error, func(v *Void, m *Map) { v.Backdrop = nil; v.Mist = nil }}, // stale frames
		{"void.backdrop", Error, func(v *Void, m *Map) { v.Backdrop.Z = 0 }},
		{"void.backdrop", Error, func(v *Void, m *Map) { v.Backdrop.Z = -400 }},
		{"void.backdrop", Error, func(v *Void, m *Map) { v.Backdrop.Scale = 0.5 }},
		{"ground.texture", Error, func(v *Void, m *Map) { v.Backdrop.Texture = TextureRef{} }},
		{"ground.texture", Error, func(v *Void, m *Map) { v.Backdrop.Texture = TextureRef{RGB: &[3]float64{0, 0, 300}} }},
		{"ground.file", Error, func(v *Void, m *Map) { v.Backdrop.Texture = TextureRef{File: "../void.png"} }},
		{"ground.file", Error, func(v *Void, m *Map) { v.Backdrop.Texture = TextureRef{File: "ground/none.png"} }},
		{"void.mist-floor", Error, func(v *Void, m *Map) { v.Mist.Z = 0 }}, // over the walkable floor
		{"void.mist-floor", Error, func(v *Void, m *Map) { v.Mist.Z = 1.5 }},
		{"void.mist", Error, func(v *Void, m *Map) { v.Mist.Z = -60 }}, // under the backdrop
		{"void.mist", Error, func(v *Void, m *Map) { v.Mist.Range = 0 }},
		{"void.mist", Error, func(v *Void, m *Map) { v.Mist.Color[1] = 3 }},
		{"void.mist-unlit", Warning, func(v *Void, m *Map) { v.Backdrop.Unlit = true }},
		{"void.mist-ford", Warning, func(v *Void, m *Map) { // a ford's water 0.22 m down, the mist 0.1 m down, range 1
			m.fords = []ground.Dip{{X: -5, Y: -5, Cos: 1, HalfW: 10, HalfL: 10, Depth: -0.55}}
			v.Mist.Z, v.Mist.Range = -0.1, 1
		}},
		{"void.fog", Error, func(v *Void, m *Map) { v.Fog.Start, v.Fog.End = -10, 5 }},
		{"void.fog", Error, func(v *Void, m *Map) { v.Fog.End = -5000 }},
		{"void.fog", Error, func(v *Void, m *Map) { (*v.Fog.Color)[0] = 1.5 }},
	} {
		m := ring(t)
		m.Void = good()
		c.edit(m.Void, m)
		r, _ := Check(m, nil)
		if !has(r, c.rule, c.sev) {
			t.Errorf("%s (%s): not found in %+v", c.rule, c.sev, r.Findings)
		}
		if c.sev == Warning && !r.OK {
			t.Errorf("%s: a warning failed the map: %+v", c.rule, r.Findings)
		}
	}
	// the mist just above a ford's water (CM_Pass: 0.02 m) tints it too little for a warning
	m = ring(t)
	m.Void = good()
	m.fords = []ground.Dip{{X: -5, Y: -5, Cos: 1, HalfW: 10, HalfL: 10, Depth: -0.55}}
	if r, _ := Check(m, nil); len(r.Findings) != 0 {
		t.Errorf("the ford under the good mist: %+v", r.Findings)
	}
}

// TestVoidFormat: the void writes a member a line and reads back; a map without one writes none.
func TestVoidFormat(t *testing.T) {
	m := ring(t)
	plain, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte(`"void"`)) {
		t.Errorf("a map without a void writes one")
	}
	m.Void = passVoid()
	m.Void.Fog = &VoidFog{Start: 10, End: -225}
	text, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `  "void": {
    "cut": 1.5,
    "backdrop": {"z": -50, "texture": {"rgb": [0, 0, 0]}},
    "mist": {"z": -0.2, "range": 10, "color": [0.467, 0.824, 0.859]},
    "fog": {"start": 10, "end": -225}
  },`
	if !strings.Contains(string(text), want) {
		t.Errorf("the void written:\n%s", text)
	}
	back, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Void, m.Void) {
		t.Errorf("read back %+v", back.Void)
	}
}

// modelOf parses an archive's ground model.
func modelOf(t *testing.T, archive []byte, name string) (*bmdl.Node, map[string][]byte) {
	t.Helper()
	a, err := snap.Parse(archive)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, e := range a.Entries {
		if files[e.Name], err = a.Read(e); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := bmdl.Parse(files[EntriesFor(name).Model])
	if err != nil {
		t.Fatal(err)
	}
	return tr.Root.F["model"].(*bmdl.Node), files
}

func triangles(m *bmdl.Node) int {
	n := 0
	for _, me := range m.F["meshes"].([]*bmdl.Node) {
		n += int(bmdl.U32(me.F["index_count"])) / 3
	}
	return n
}

// TestGameVoid builds CM_Pass from build 854's files with its void and without: the void adds the backdrop texture
// (black) and two planes on two materials of their own (the lit backdrop at z -50, the mist on the fog face's
// material with its values), and the cut leaves out most of the ground's triangles; the planes and their materials
// are MM_Map_v3's own under the same values (the experiment the look was tuned on), but the texture path. The
// environment's lights keep their height fog unless the void sets one. CM_Ring's baked ground takes a void too.
func TestGameVoid(t *testing.T) {
	dir := testgame.Dir(t)
	in, err := ReadInputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	pass, err := Load(filepath.Join(mapsDir, "CM_Pass"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pass.Void, passVoid()) {
		t.Fatalf("CM_Pass's void %+v", pass.Void)
	}
	withVoid, r, err := Build(pass, in, SharedMesh{}, nil)
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	// the plinths lifted 5 cm raise no props.flush; at z 0 (as first placed) each does
	flush := func(r *Report) int {
		n := 0
		for _, f := range r.Findings {
			if f.Rule == "props.flush" && f.Severity == Warning {
				n++
			}
		}
		return n
	}
	flat, err := Load(filepath.Join(mapsDir, "CM_Pass"))
	if err != nil {
		t.Fatal(err)
	}
	for k := range flat.Props {
		if flat.Props[k].Noun != "" {
			flat.Props[k].Pos[2] = 0
		}
	}
	if err := ResolveProps(flat, in.AssetData); err != nil {
		t.Fatal(err)
	}
	if rf, _ := Check(flat, nil); flush(r) != 0 || flush(rf) != 2 || !rf.OK {
		t.Errorf("props.flush: %d on the package, %d with the plinths at z 0", flush(r), flush(rf))
	}
	bare := *pass
	bare.Void = nil
	without, r, err := Build(&bare, in, SharedMesh{}, nil)
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	t.Logf("CM_Pass: %d bytes with its void (%s), %d without", len(withVoid), digest(withVoid)[:16], len(without))
	mv, fv := modelOf(t, withVoid, "CM_Pass")
	mb, fb := modelOf(t, without, "CM_Pass")
	if _, err := ground.CheckModel(fv[EntriesFor("CM_Pass").Model]); err != nil {
		t.Fatal(err)
	}
	mats := mv.F["materials"].([]*bmdl.Node)
	meshes := mv.F["meshes"].([]*bmdl.Node)
	nb := len(mb.F["meshes"].([]*bmdl.Node))
	tv, tb := triangles(mv), triangles(mb)
	planes := 2 * 2 * ground.BackdropCells * ground.BackdropCells
	if len(mats) != 4 || len(meshes) != nb+2 || tv-planes >= tb/2 {
		t.Errorf("with the void: %d materials, %d meshes, %d triangles; without: %d meshes, %d triangles", len(mats),
			len(meshes), tv, nb, tb)
	}
	t.Logf("the ground's triangles: %d cut to %d", tb, tv-planes)
	tex := fv[BackdropEntry("CM_Pass")]
	if black, err := ground.FlatDDS([3]byte{}); err != nil || !bytes.Equal(tex, black) {
		t.Errorf("the backdrop's texture: %d bytes (%v)", len(tex), err)
	}
	if _, ok := fb[BackdropEntry("CM_Pass")]; ok || len(fv) != len(fb)+1 {
		t.Errorf("entries: %d with the void, %d without", len(fv), len(fb))
	}
	for name, b := range fb {
		if strings.HasSuffix(name, "_lights.Markerset.bin") && !bytes.Equal(b, fv[name]) {
			t.Errorf("%s differs though the void sets no fog", name)
		}
	}

	// the same planes as MM_Map_v3's ground under mmvoid's values
	images, err := ArchiveFromCopy(dir)(los.Archive)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := los.FromArchive(images, los.Entry)
	if err != nil {
		t.Fatal(err)
	}
	img, err := los.Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	read := in.Shipped()
	tmpl, err := read(ground.TemplateArchive, ground.TemplateEntry)
	if err != nil {
		t.Fatal(err)
	}
	grass, err := read(ground.GrassArchive, ground.GrassSource)
	if err != nil {
		t.Fatal(err)
	}
	fogFace, err := read(ground.FogFaceArchive, ground.FogFaceEntry)
	if err != nil {
		t.Fatal(err)
	}
	mist, err := ground.FogFaceMaterial(fogFace, [3]float64{0.467, 0.824, 0.859}, 10)
	if err != nil {
		t.Fatal(err)
	}
	spec := ground.MM
	spec.Cut = 1.5
	const mmTex = "environments/prototype/textures/mm_v3_backdrop_D.dds"
	spec.Backdrop = &ground.Backdrop{Z: -50, Half: ground.BackdropHalf, RepeatX: ground.BackdropRepeat,
		RepeatY: ground.BackdropRepeat, Texture: mmTex}
	spec.Mist = &ground.Backdrop{Z: -0.2, Half: ground.BackdropHalf, RepeatX: ground.BackdropRepeat,
		RepeatY: ground.BackdropRepeat, Material: mist}
	mmModel, _, _, err := ground.BuildSpec(img, spec, tmpl, grass, [2]string{ground.DirtEntry, ground.GrassEntry})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := bmdl.Parse(mmModel)
	if err != nil {
		t.Fatal(err)
	}
	mm := tr.Root.F["model"].(*bmdl.Node)
	mmMats, mmMeshes := mm.F["materials"].([]*bmdl.Node), mm.F["meshes"].([]*bmdl.Node)
	for k := 1; k <= 2; k++ {
		a, b := meshes[len(meshes)-k], mmMeshes[len(mmMeshes)-k]
		for _, f := range []string{"vertices", "indices", "bounds", "material"} {
			if !reflect.DeepEqual(a.F[f], b.F[f]) {
				t.Errorf("plane %d: %s differs from MM_Map_v3's", 3-k, f)
			}
		}
	}
	ours := mats[2].Clone()
	ours.F["textures"].([]*bmdl.Node)[0].F["path"] = mmTex
	ours.F["textures"].([]*bmdl.Node)[0].F["path_hash"] = bmdl.FNV1(mmTex)
	if !reflect.DeepEqual(ours, mmMats[2]) || !reflect.DeepEqual(mats[3], mmMats[3]) {
		t.Errorf("the planes' materials differ from MM_Map_v3's")
	}

	// a height fog of the map's own: the lights' GraphicsControl carries it
	fogged := *pass
	fogged.Void = passVoid()
	fogged.Void.Fog = &VoidFog{Start: -1, End: -30, Color: &[3]float64{0.3, 0.34, 0.45}}
	if err := ResolveProps(&fogged, in.AssetData); err != nil {
		t.Fatal(err)
	}
	_, cm := Check(&fogged, nil)
	if cm == nil {
		t.Fatal("CM_Pass with a fog fails")
	}
	as, err := cm.Assets(in.AssetData, madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	var got []any
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case *assetdata.Object:
			if s, ok := x.Get("fog_ground_start"); ok {
				e, _ := x.Get("fog_ground_end")
				c, _ := x.Get("fog_color")
				got = append(got, s, e, c)
			}
			for _, k := range x.Keys() {
				e, _ := x.Get(k)
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(as.Values["CM_Pass_lights.Markerset"])
	want := []any{float32(-1), float32(-30), []any{float32(0.3), float32(0.34), float32(0.45)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the lights' fog %v", got)
	}

	// CM_Ring's baked ground with a void: the planes after the bake's materials, the cut
	rm := ring(t)
	rm.Void = &Void{Cut: 3, Backdrop: &VoidBackdrop{Z: -40, Texture: TextureRef{RGB: &[3]float64{20, 20, 30}}, Unlit: true}}
	b, r, err := Build(rm, in, SharedMesh{}, nil)
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	rv, rf := modelOf(t, b, "CM_Ring")
	info, err := bake.CheckModel(rf[EntriesFor("CM_Ring").Model])
	if err != nil {
		t.Fatal(err)
	}
	rmats, rmeshes := rv.F["materials"].([]*bmdl.Node), rv.F["meshes"].([]*bmdl.Node)
	last := rmeshes[len(rmeshes)-1]
	if info.Materials != 6 || rmats[5].F["shader"] != "labsUnlit" || last.F["name"] != "cm_ring_backdrop" {
		t.Errorf("CM_Ring with a void: %+v, the last material %v, mesh %v", info, rmats[5].F["shader"], last.F["name"])
	}
	plain, _, err := Build(ring(t), in, SharedMesh{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pm, _ := modelOf(t, plain, "CM_Ring")
	if triangles(rv)-2*ground.BackdropCells*ground.BackdropCells >= triangles(pm) {
		t.Errorf("CM_Ring's cut: %d triangles, %d without", triangles(rv), triangles(pm))
	}
}
