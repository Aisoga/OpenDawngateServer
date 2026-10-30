package mapc

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

var waterDir = testmaps.Pkg("CM_Water")

// Water854SHA256 pins CM_Water built from build 854's files with the shared mesh.
const Water854SHA256 = "5648ef76256b6c185029cf4cd14cee0db8b0c0762a9047782145f48ef74ae5f2"

func waterMap(t *testing.T) *Map {
	t.Helper()
	m, err := Load(waterDir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// slab is the polygon chasm CM_Water's water test uses: 60 x 18 m, its long side turned 60 degrees.
var slab = []Point{{-97.79, 5.48}, {-127.79, -46.48}, {-112.21, -55.48}, {-82.21, -3.52}}

// TestWaterFormat: "water" reads true, false and the object, and writes them back so; an unknown member is an error.
func TestWaterFormat(t *testing.T) {
	z := -3.0
	for _, c := range []struct {
		in   string
		want Water
	}{{`true`, Water{}}, {`false`, Water{Off: true}}, {`{"z": -3, "mask": "circle"}`, Water{Z: &z, Mask: "circle"}},
		{`{"look": "chasm"}`, Water{Look: "chasm"}},
		{`{}`, Water{}}} {
		var w Water
		if err := json.Unmarshal([]byte(c.in), &w); err != nil || !reflect.DeepEqual(w, c.want) {
			t.Errorf("%s: %+v %v", c.in, w, err)
		}
		out, err := json.Marshal(w)
		back := strings.ReplaceAll(c.in, " ", "")
		if back == "{}" {
			back = "true"
		}
		if err != nil || string(out) != back {
			t.Errorf("%s written as %s (%v)", c.in, out, err)
		}
	}
	var w Water
	if err := json.Unmarshal([]byte(`{"depth": 2}`), &w); err == nil {
		t.Errorf("an unknown member read")
	}
	var s Shape
	if err := json.Unmarshal([]byte(`{"type": "chasm", "circle": [0, 0], "r": 9}`), &s); err != nil || s.Water != nil ||
		s.watered() {
		t.Errorf("no water: %+v %v", s, err)
	}
	if b, _ := json.Marshal(s); bytes.Contains(b, []byte("water")) {
		t.Errorf("a shape without water writes %s", b)
	}
	for name, ok := range map[string]bool{"": true, "none": true, "Circle": true, "chasm_wm_3": true, "Fish_wm_6": true,
		"Spirit_Lake_gobo": true, "Chasm_wm_9": false, "x.dds": false} {
		mask, outline, got := (&Water{Mask: name}).MaskFiles()
		if got != ok {
			t.Errorf("mask %q: %v", name, got)
		}
		if name == "chasm_wm_3" && (mask != `Environments\anshavar\textures\Chasm_wm_3.dds` ||
			outline != `Environments\anshavar\textures\Chasm_wm_3.out`) {
			t.Errorf("%s %s", mask, outline)
		}
	}
}

// TestWaterPlanes: CM_Water's planes: the centre chasm's once at the origin, at its height and with its mask; the
// turned slab's along its long side, mirrored; each covers every cell whose ground lies below its water and lies
// over higher ground at its edges; the same twice.
func TestWaterPlanes(t *testing.T) {
	r, cm := Check(waterMap(t), nil)
	if !r.OK || len(r.Findings) != 0 || r.Stats.WaterPlanes != 3 {
		t.Fatalf("%+v %+v", r.Findings, r.Stats)
	}
	ps, fs, err := cm.WaterPlanes()
	if err != nil || len(fs) != 0 || len(ps) != 3 {
		t.Fatalf("%v %+v %+v", err, fs, ps)
	}
	again, _, _ := cm.WaterPlanes()
	if !reflect.DeepEqual(ps, again) {
		t.Errorf("not the same twice")
	}
	c := ps[0]
	if c.Team != 0 || c.Pos != [3]float64{0, 0, -1.5} || !strings.HasSuffix(c.Mask, `\Circle_wm.dds`) || c.Look != "chasm" ||
		!strings.HasSuffix(c.Outline, `\Circle_wm.out`) || c.Size[0] < 80 || c.Size[0] > 90 || c.Size[1] < 80 ||
		c.Size[1] > 90 || c.Path != "terrain.shapes[11].water" {
		t.Errorf("the centre's water %+v", c)
	}
	a, b := ps[1], ps[2]
	if a.Team != 1 || b.Team != 2 || math.Abs(a.Rot-60) > 1.5 || a.Size[0] < 60 || a.Size[0] > 72 ||
		a.Size[1] < 18 || a.Size[1] > 30 || a.Mask != "" || a.Outline != "" || a.Look != "river" || a.Pos[2] != DefaultWaterZ ||
		math.Hypot(a.Pos[0]+105, a.Pos[1]+25) > 2 || b.Pos != [3]float64{-a.Pos[0], -a.Pos[1], a.Pos[2]} ||
		b.Rot != a.Rot || b.Size != a.Size {
		t.Errorf("the slab's water %+v %+v", a, b)
	}
	// every cell below a plane's water lies in a plane; every plane's edge lies over ground above its water
	walk := cm.Layers.GroundWalk()
	f, err := ground.NewField(walk.W, walk.H, walk.Pix, cm.Map.Size)
	if err != nil {
		t.Fatal(err)
	}
	spec := cm.GroundSpec()
	z := func(p Point) float64 { return ground.Height(f.At(p[0], p[1]), p[0], p[1], &spec) }
	inside := func(p WaterPlane, q Point) bool {
		r := p.Rot * math.Pi / 180
		dx, dy := q[0]-p.Pos[0], q[1]-p.Pos[1]
		return math.Abs(dx*math.Cos(r)+dy*math.Sin(r)) <= p.Size[0]/2 &&
			math.Abs(-dx*math.Sin(r)+dy*math.Cos(r)) <= p.Size[1]/2
	}
	for k, kind := range cm.Layers.Kind {
		q := cm.Layers.CellCentre(k%Grid, k/Grid)
		if kind != Chasm || z(q) >= DefaultWaterZ-wetBelow {
			continue
		}
		if !inside(ps[0], q) && !inside(ps[1], q) && !inside(ps[2], q) {
			t.Fatalf("the wet cell at %v is in no plane", q)
		}
	}
	for _, p := range ps {
		cs := p.corners()
		for i := range cs {
			for s := 0.0; s <= 1; s += 0.01 {
				q := Point{cs[i][0] + (cs[(i+1)%4][0]-cs[i][0])*s, cs[i][1] + (cs[(i+1)%4][1]-cs[i][1])*s}
				if z(q) < p.Pos[2] {
					t.Fatalf("%s: its edge at %v lies over ground at %.2f, below the water", p.Path, q, z(q))
				}
			}
		}
	}
	if HasWater(ring(t)) {
		t.Errorf("CM_Ring has water")
	}
	if ps, fs, err := mustCompile(t, ring(t)).WaterPlanes(); ps != nil || fs != nil || err != nil {
		t.Errorf("a map without water: %v %v %v", ps, fs, err)
	}
}

func mustCompile(t *testing.T, m *Map) *Compiled {
	t.Helper()
	r, cm := Check(m, nil)
	if cm == nil {
		t.Fatalf("%+v", r.Findings)
	}
	return cm
}

// TestWaterRules breaks each water rule once on CM_Water.
func TestWaterRules(t *testing.T) {
	z := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		name, rule, sev string
		edit            func(m *Map)
	}{
		{"on an open shape", "terrain.water", Error, func(m *Map) { m.Terrain.Shapes[9].Water = &Water{} }},
		{"too low", "terrain.water", Error, func(m *Map) { m.Terrain.Shapes[12].Water = &Water{Z: z(-5)} }},
		{"above the floor", "terrain.water", Error, func(m *Map) { m.Terrain.Shapes[12].Water = &Water{Z: z(0)} }},
		{"an unknown mask", "terrain.water", Error, func(m *Map) { m.Terrain.Shapes[12].Water = &Water{Mask: "blob"} }},
		{"an unknown look", "terrain.water", Error, func(m *Map) { m.Terrain.Shapes[12].Water = &Water{Look: "lava"} }},
		{"a narrow chasm", "terrain.water", Warning, func(m *Map) {
			m.Terrain.Shapes = append(m.Terrain.Shapes, Shape{Type: "chasm", Path: []Point{{-75, -125}, {-70, -115}},
				Width: 3, Water: &Water{}})
		}},
		{"two overlapping", "terrain.water-overlap", Warning, func(m *Map) {
			m.Terrain.Shapes = append(m.Terrain.Shapes, Shape{Type: "chasm", Circle: &Point{-120.6, -16}, R: 8,
				Water: &Water{}})
		}},
	} {
		m := waterMap(t)
		c.edit(m)
		r, _ := Check(m, nil)
		if !has(r, c.rule, c.sev) {
			t.Errorf("%s: %+v", c.name, r.Findings)
		}
	}
	// false: no water, no plane
	m := waterMap(t)
	m.Terrain.Shapes[12].Water = &Water{Off: true}
	if r, _ := Check(m, nil); !r.OK || r.Stats.WaterPlanes != 1 {
		t.Errorf("water false: %+v %+v", r.Findings, r.Stats)
	}
}

// waterAssetData is the made-up AssetData with a made-up water markerset (a lake, then a chasm water marker).
func waterAssetData(t *testing.T) []byte {
	t.Helper()
	ts, err := assetdata.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	a, err := snap.Parse(madeUpAssetData(t))
	if err != nil {
		t.Fatal(err)
	}
	var files []snap.File
	for _, e := range a.Entries {
		b, err := a.Read(e)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, snap.File{Name: e.Name, Data: b})
	}
	wd := func(mask string, tint float32) *assetdata.Object {
		return obj("mWaterType", "Water", "size", []any{float32(57), float32(57)}, "mask", mask, "outline",
			strings.TrimSuffix(mask, ".dds")+".out", "diffuseTint", []any{tint, float32(0.45), float32(0.45)},
			"tile", []any{float32(-0.5), float32(-0.5)},
			"depth_fog", float32(0.01), "soft_edges", true, "enable", true)
	}
	set := obj("markers", []any{
		madeUpMarker("Water.Noun", 1301, 5, 5, 0.5, "waterData", wd(`Environments\anshavar\textures\Fish_wm_1.dds`, 0.2)),
		madeUpMarker("Water.Noun", 1302, 6, 6, -1.85, "scale", float32(0.88),
			"waterData", wd(`Environments\anshavar\textures\Chasm_wm_2.dds`, 0.4)),
	}, "group", "none", "weight", float32(1))
	b, err := ts.Write(ts.Type("Markerset"), set, assetdata.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, snap.File{Name: AssetEntry(WaterTemplateSet), Data: b})
	return snap.Pack(files)
}

// TestWaterMadeUp: CM_Ring with water in its chasm (the chasm look) and in a turned slab (the river look), from
// made-up assets: the environment markerset ends with the planes, each a copy of its look's marker (its colours kept;
// the river's ripples tiled by the size) at its place, turned (the
// mirror by a further 180 degrees), scale 1, with its size, mask and outline; the rest of the map as before.
func TestWaterMadeUp(t *testing.T) {
	ad := waterAssetData(t)
	m := ring(t)
	plain, err := mustCompile(t, m).Assets(ad, madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	k := -1
	for i, s := range m.Terrain.Shapes {
		if s.Type == "chasm" {
			k = i
		}
	}
	if k < 0 {
		t.Fatal("CM_Ring has no chasm")
	}
	m.Terrain.Shapes[k].Water = &Water{Mask: "Chasm_wm_7", Look: "chasm"}
	m.Terrain.Shapes = append(m.Terrain.Shapes, Shape{Type: "chasm", Polygon: slab, Water: &Water{}})
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	ps, _, err := cm.WaterPlanes()
	if err != nil || len(ps) != 3 {
		t.Fatalf("%v %+v", err, ps)
	}
	as, err := cm.Assets(ad, madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	checkAssets(t, m, as)
	env := m.Name + "_" + setEnvironment + ".Markerset"
	ms, before := markers(as.Values[env]), markers(plain.Values[env])
	if len(ms) != len(before)+3 {
		t.Fatalf("%d environment markers, %d before", len(ms), len(before))
	}
	for i, p := range ps {
		mk := ms[len(before)+i]
		wd := sub(mk, "waterData")
		pos, rot, size := vec(mk, "pos"), vec(mk, "rotDegrees"), vec(wd, "size")
		near := func(a, b float64) bool { return math.Abs(a-b) < 1e-3 }
		wantRot := p.Rot
		if p.Team == 2 {
			wantRot += 180
		}
		if text(mk, "nounDef") != "Water.Noun" || !near(pos[0], p.Pos[0]) || !near(pos[1], p.Pos[1]) ||
			!near(pos[2], p.Pos[2]) || !near(rot[2], wantRot) || fval(mustGet(mk, "scale")) != 1 ||
			!near(size[0], p.Size[0]) || !near(size[1], p.Size[1]) || text(wd, "mask") != p.Mask ||
			text(wd, "outline") != p.Outline || !near(vec(wd, "diffuseTint")[0], map[string]float64{"river": 0.2,
			"chasm": 0.4}[p.Look]) || !near(vec(wd, "tile")[0], map[string]float64{"river": round3(p.Size[0] / riverTileM),
			"chasm": -0.5}[p.Look]) {
			t.Errorf("plane %d: %s at %v turned %v, size %v, mask %q %q; want %+v", i, text(mk, "nounDef"), pos, rot,
				size, text(wd, "mask"), text(wd, "outline"), p)
		}
	}
	if !strings.HasSuffix(ps[0].Mask, `\Chasm_wm_7.dds`) || ps[0].Team != 0 || ps[1].Mask != "" {
		t.Errorf("%+v", ps)
	}
	for _, name := range plain.Names {
		if name != env && !reflect.DeepEqual(as.Values[name], plain.Values[name]) {
			t.Errorf("%s changed", name)
		}
	}
}

// TestGameWater builds CM_Water from build 854's files (pinned; twice the same bytes): its environment markerset
// holds the three planes, each a copy of SM_Map_v5's first chasm water marker.
func TestGameWater(t *testing.T) {
	dir := testgame.Dir(t)
	in, err := ReadInputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, r, err := Build(waterMap(t), in, SharedMesh{}, nil)
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if len(r.Findings) != 0 {
		t.Errorf("findings %+v", r.Findings)
	}
	if got := digest(b); got != Water854SHA256 {
		t.Errorf("SHA-256 %s, pinned %s (%d bytes)", got, Water854SHA256, len(b))
	}
	again, _, err := Build(waterMap(t), in, SharedMesh{}, nil)
	if err != nil || !bytes.Equal(again, b) {
		t.Errorf("a second build gives other bytes (%v)", err)
	}
	a, err := snap.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := assetdata.Embedded()
	var set *assetdata.Object
	for _, e := range a.Entries {
		if e.Name == AssetEntry("CM_Water_environment.Markerset") {
			raw, _ := a.Read(e)
			if set, err = ts.ParseExact(ts.Type("Markerset"), raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	if set == nil {
		t.Fatal("no environment markerset")
	}
	var water []*assetdata.Object
	for _, mk := range markers(set) {
		if sub(mk, "waterData") != nil {
			water = append(water, mk)
		}
	}
	if len(water) != 3 {
		t.Fatalf("%d water markers", len(water))
	}
	for _, mk := range water {
		wd := sub(mk, "waterData")
		t.Logf("%s at %v turned %v: size %v, mask %q, tint %v, depth fog %v", text(mk, "markerName"), vec(mk, "pos"),
			vec(mk, "rotDegrees"), vec(wd, "size"), text(wd, "mask"), vec(wd, "diffuseTint"), mustGet(wd, "depth_fog"))
	}
}
