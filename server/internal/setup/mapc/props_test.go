package mapc

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

// dressDir is the repository's props test map: CM_Ring's layout with a pocket, props, a group and a scatter.
var dressDir = testmaps.Pkg("CM_Dress")

// Dress854SHA256 pins CM_Dress built from build 854's files with the shared mesh.
const Dress854SHA256 = "7250890f146dfceb91f7ef49e2bb425f1e801dc22e71126fc00cce9fcf49cd41"

func dress(t *testing.T) *Map {
	t.Helper()
	m, err := Load(dressDir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func fp(w, d float64) *[2]float64 { return &[2]float64{w, d} }

// TestPropRules breaks CM_Ring once per props rule and expects the rule's finding.
func TestPropRules(t *testing.T) {
	rock := func(p Prop) func(m *Map) { return func(m *Map) { m.Props = []Prop{p} } }
	scatter := func(s Scatter) func(m *Map) { return func(m *Map) { m.Scatter = []Scatter{s} } }
	group := &Group{Level: "SM_Map_v5", Sets: []string{"D_Rocks"}, At: Point{0, 0}, R: 10}
	ok := Scatter{Nouns: []string{"lichenrock_01"}}
	cases := []struct {
		rule, sev string
		edit      func(m *Map)
	}{
		{"props.kind", Error, rock(Prop{Pos: [3]float64{-150, -150}})},
		{"props.kind", Error, rock(Prop{Noun: "rock", Group: group, Pos: [3]float64{-150, -150}})},
		{"props.scale", Error, rock(Prop{Noun: "rock", Pos: [3]float64{-150, -150}, Scale: 30})},
		{"props.footprint", Error, rock(Prop{Noun: "rock", Pos: [3]float64{-150, -150}, Blocks: true,
			Footprint: fp(0, 3)})},
		{"props.footprint", Warning, rock(Prop{Noun: "rock", Pos: [3]float64{-150, -150}, Blocks: true})},
		{"props.blocks", Error, rock(Prop{Group: group, Pos: [3]float64{-150, -150}, Blocks: true})},
		{"props.group", Error, rock(Prop{Group: &Group{Sets: []string{"D_Rocks"}, R: 10}, Pos: [3]float64{-150, -150}})},
		{"props.group", Error, rock(Prop{Group: &Group{Level: "SM_Map_v5", Sets: []string{""}, R: 10},
			Pos: [3]float64{-150, -150}})},
		{"props.group", Error, rock(Prop{Group: &Group{Level: "SM_Map_v5", Sets: []string{"D_Rocks"}},
			Pos: [3]float64{-150, -150}})},
		// a polygon region: not with r, at least 3 corners, within the bounds, some area
		{"props.group", Error, rock(Prop{Group: &Group{Level: "SM_Map_v5", Sets: []string{"D_Rocks"}, R: 10,
			Polygon: pts(0, 0, 10, 0, 0, 10)}, Pos: [3]float64{-150, -150}})},
		{"props.group", Error, rock(Prop{Group: &Group{Level: "SM_Map_v5", Sets: []string{"D_Rocks"},
			Polygon: pts(0, 0, 10, 0)}, Pos: [3]float64{-150, -150}})},
		{"props.group", Error, rock(Prop{Group: &Group{Level: "SM_Map_v5", Sets: []string{"D_Rocks"},
			Polygon: pts(0, 0, 700, 0, 0, 10)}, Pos: [3]float64{-150, -150}})},
		{"props.group", Error, rock(Prop{Group: &Group{Level: "SM_Map_v5", Sets: []string{"D_Rocks"},
			Polygon: pts(0, 0, 10, 0, 20, 0)}, Pos: [3]float64{-150, -150}})},
		{"size.bounds", Error, rock(Prop{Noun: "rock", Pos: [3]float64{-219, -100}})},
		{"symmetry.half", Error, rock(Prop{Noun: "rock", Pos: [3]float64{10, 5}})},
		{"scatter.nouns", Error, scatter(Scatter{})},
		{"scatter.nouns", Error, scatter(Scatter{Nouns: []string{""}})},
		{"scatter.along", Error, scatter(Scatter{Nouns: ok.Nouns, Along: "chasms"})},
		{"scatter.spacing", Error, scatter(Scatter{Nouns: ok.Nouns, Spacing: 0.5})},
		{"scatter.inset", Error, scatter(Scatter{Nouns: ok.Nouns, Inset: 11})},
		{"scatter.scale", Error, scatter(Scatter{Nouns: ok.Nouns, Scale: [2]float64{1.5, 1}})},
		{"scatter.max", Error, scatter(Scatter{Nouns: ok.Nouns, Max: 5000})},
		{"scatter.region", Error, scatter(Scatter{Nouns: ok.Nouns, Circle: &Point{-150, -150}, R: 5,
			Polygon: pts(0, 0, 1, 0, 1, 1)})},
		{"scatter.region", Error, scatter(Scatter{Nouns: ok.Nouns, Circle: &Point{-150, -150}})},
		{"scatter.region", Error, scatter(Scatter{Nouns: ok.Nouns, Polygon: pts(0, 0, 1, 0)})},
		// a blocking prop is ground that blocks: on an object, across a lane
		{"fixed.open", Error, rock(Prop{Noun: "rock", Pos: [3]float64{-204, -204}, Blocks: true, Footprint: fp(6, 6)})},
		{"lanes.graph", Error, rock(Prop{Noun: "rock", Pos: [3]float64{-172, -60}, Blocks: true, Footprint: fp(40, 40)})},
	}
	for _, tc := range cases {
		m := ring(t)
		tc.edit(m)
		r, _ := Check(m, nil)
		if !has(r, tc.rule, tc.sev) {
			t.Errorf("%s (%s): not found in %+v", tc.rule, tc.sev, r.Findings)
		}
		if tc.sev == Error && r.OK {
			t.Errorf("%s: the report says ok", tc.rule)
		}
	}
	// the copy's rules: the nouns, a group's level and markersets
	m := ring(t)
	m.Props = []Prop{{Group: &Group{Level: "SM_Map_v5", Sets: []string{"D_Rocks", "D_Nothing"}, R: 5},
		Pos: [3]float64{-150, -150}}, {Group: &Group{Level: "XX_Map", Sets: []string{"D_Rocks"}, R: 5},
		Pos: [3]float64{-150, -150}}}
	m.Scatter = []Scatter{{Nouns: []string{"rock_a", "rock_b"}}}
	r, _ := Check(m, nil)
	s := &Shipped{Nouns: map[string]bool{"rock_a": true}, Levels: map[string]bool{"sm_map_v5": true},
		Markersets: map[string]bool{"sm_map_v5_d_rocks": true}}
	CheckShipped(m, s, r)
	n := map[string]int{}
	for _, f := range r.Findings {
		n[f.Rule]++
	}
	if n["nouns.exist"] != 1 || n["props.group"] != 2 || r.OK {
		t.Errorf("%+v", r.Findings)
	}
}

// TestPropsStamp: a blocking prop's footprint and its mirror become Solid cells (blocking sight and walking, the
// ground flat), a prop in a wall stamps nothing, a prop at the centre stamps once; the map without props is CM_Ring.
func TestPropsStamp(t *testing.T) {
	base, err := Rasterise(ring(t))
	if err != nil {
		t.Fatal(err)
	}
	m := ring(t)
	m.Props = []Prop{
		{Noun: "rock", Pos: [3]float64{-125, -125}, Rot: 30, Blocks: true, Footprint: fp(8, 4)},
		{Noun: "rock", Pos: [3]float64{-210, -60}, Blocks: true, Footprint: fp(8, 8)}, // in a wall
		{Noun: "rock", Pos: [3]float64{-125, -110}},                                   // doesn't block
	}
	ly, err := Rasterise(m)
	if err != nil {
		t.Fatal(err)
	}
	cell := ly.Size / Grid
	solid := 0
	for k, v := range ly.Kind {
		i, j := k%Grid, k/Grid
		if v == Solid {
			solid++
			if mk := (Grid-j)*Grid + (Grid - i); mk < len(ly.Kind) && ly.Kind[mk] != Solid {
				t.Errorf("cell (%d, %d) is Solid, its mirror isn't", i, j)
			}
			if base.Kind[k] != Open {
				t.Errorf("cell (%d, %d): a footprint on ground that wasn't open", i, j)
			}
		} else if v != base.Kind[k] {
			t.Errorf("cell (%d, %d) changed from %d to %d", i, j, base.Kind[k], v)
		}
	}
	// an ellipse of 8 x 4 m, twice
	if want := 2 * math.Pi * 4 * 2 / (cell * cell); math.Abs(float64(solid)-want) > 0.15*want {
		t.Errorf("%d Solid cells, about %.0f expected", solid, want)
	}
	// turned by 30 degrees: (-125, -125) + 3.5 m along the turned x is in it, 3.5 m along the turned y isn't
	along := rotate(Point{3.5, 0}, 30)
	across := rotate(Point{0, 3.5}, 30)
	if ly.KindAt(Point{-125 + along[0], -125 + along[1]}) != Solid || ly.KindAt(Point{-125 + across[0],
		-125 + across[1]}) != Open || ly.KindAt(Point{125 - along[0], 125 - along[1]}) != Solid {
		t.Errorf("the footprint isn't the turned ellipse")
	}
	losImg, walk, gw := ly.LoS(), ly.Walk(), ly.GroundWalk()
	bw := base.Walk()
	for k, v := range ly.Kind {
		if v == Solid && (losImg.Pix[k] == 0 || walk.Pix[k] == 0) {
			t.Fatalf("a Solid cell lets sight or walking through")
		}
		if gw.Pix[k] != bw.Pix[k] {
			t.Fatalf("the ground's input changed under the prop")
		}
	}
	// at the centre: once (on the chasm nothing; on open ground the cells round the centre)
	m = ring(t)
	m.Terrain.Shapes[6].Type = "open"
	m.Props = []Prop{{Noun: "rock", Pos: [3]float64{0.2, 0.1}, Blocks: true, Footprint: fp(4, 4)}}
	if ly, err = Rasterise(m); err != nil {
		t.Fatal(err)
	}
	solid = 0
	for _, v := range ly.Kind {
		if v == Solid {
			solid++
		}
	}
	if want := math.Pi * 4 / (cell * cell); math.Abs(float64(solid)-want) > 0.25*want {
		t.Errorf("a prop at the centre: %d Solid cells, about %.0f expected (once)", solid, want)
	}
	// the footprint from the noun's box (ResolveProps' result): offset and turned with the prop
	m = ring(t)
	m.Props = []Prop{{Noun: "box", Pos: [3]float64{-125, -125}, Rot: 90, Scale: 2, Blocks: true}}
	m.nounBounds = map[string]NounBounds{"box": {Min: [3]float64{0, -1, 0}, Max: [3]float64{4, 1, 3}}}
	f, ok := propFootprint(m, m.Props[0])
	if !ok || math.Abs(f.Centre[0]+125) > 1e-9 || math.Abs(f.Centre[1]+121) > 1e-9 || f.Axes != [2]float64{4, 2} {
		t.Errorf("the box's footprint %+v %v", f, ok)
	}
	if r, _ := Check(m, nil); !r.OK || len(r.Findings) != 0 {
		t.Errorf("%+v", r.Findings)
	}
}

func scatterRing(t *testing.T, s Scatter) *Compiled {
	t.Helper()
	m := ring(t)
	m.Scatter = []Scatter{s}
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	return cm
}

// TestScatter: the scatter is the same for a seed and another for another seed; its props stand in the walls along
// walkable ground, apart by the spacing (the mirrors too), with nouns, turns and scales from the lists and ranges.
func TestScatter(t *testing.T) {
	s := Scatter{Nouns: []string{"a", "b", "c"}, Seed: 7, Spacing: 7, Max: 120, Scale: [2]float64{0.5, 1.5}}
	cm := scatterRing(t, s)
	got := cm.Scattered(0)
	again := scatterRing(t, s).Scattered(0)
	if len(got) != 240 || len(again) != len(got) {
		t.Fatalf("%d and %d props, 240 expected", len(got), len(again))
	}
	nouns := map[string]int{}
	inWall := 0
	for i, p := range got {
		if p != again[i] {
			t.Fatalf("prop %d: %+v, then %+v", i, p, again[i])
		}
		q := Point{p.Pos[0], p.Pos[1]}
		if i%2 == 0 {
			if p.Team != 1 || q[0]+q[1] > 1 {
				t.Errorf("prop %d: team %d at %v", i, p.Team, q)
			}
			m := got[i+1]
			if m.Team != 2 || m.Pos[0] != -p.Pos[0] || m.Pos[1] != -p.Pos[1] || m.Noun != p.Noun ||
				math.Abs(normDeg(m.Rot[2]-p.Rot[2]-180)) > 1e-9 {
				t.Errorf("prop %d's mirror %+v", i, m)
			}
		}
		for j := 0; j < i; j++ {
			if d := dist(q, Point{got[j].Pos[0], got[j].Pos[1]}); d < s.Spacing {
				t.Fatalf("props %d and %d %.2f m apart", j, i, d)
			}
		}
		nouns[p.Noun]++
		if p.Scale < 0.5 || p.Scale > 1.5 || p.Rot[2] < -180 || p.Rot[2] > 180 {
			t.Errorf("prop %d: scale %g, turn %g", i, p.Scale, p.Rot[2])
		}
		if cm.Layers.KindAt(q) == Wall {
			inWall++
		}
		// walkable ground within 3 m
		near := false
		for a := 0.0; a < 2*math.Pi && !near; a += math.Pi / 8 {
			for r := 0.0; r <= 3; r += 0.5 {
				if cm.Layers.KindAt(Point{q[0] + r*math.Cos(a), q[1] + r*math.Sin(a)}) == Open {
					near = true
				}
			}
		}
		if !near {
			t.Errorf("prop %d at %v: no walkable ground within 3 m", i, q)
		}
	}
	if inWall < len(got)*9/10 || len(nouns) != 3 {
		t.Errorf("%d of %d in walls; nouns %v", inWall, len(got), nouns)
	}
	s.Seed = 8
	other := scatterRing(t, s).Scattered(0)
	same := 0
	for i := range other {
		if i < len(got) && other[i].Pos == got[i].Pos {
			same++
		}
	}
	if same > len(got)/10 {
		t.Errorf("seed 8 places %d props as seed 7 does", same)
	}
	// a region: only inside it
	s.Circle, s.R, s.Max = &Point{-60, -130}, 40, 0
	for _, p := range scatterRing(t, s).Scattered(0) {
		if p.Team == 1 && dist(Point{p.Pos[0], p.Pos[1]}, Point{-60, -130}) > 40+DefaultInset+1 {
			t.Errorf("a prop outside the region: %v", p.Pos)
		}
	}
}

// propsAssetData is the made-up AssetData with a dressing markerset (two rocks, a shadow card and a creature for a
// group), the nouns it names and a creature noun.
func propsAssetData(t *testing.T) []byte {
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
	write := func(asset string, o *assetdata.Object) {
		b, err := ts.Write(ts.Type(classOfAsset(asset)), o, assetdata.WriteOptions{})
		if err != nil {
			t.Fatalf("%s: %v", asset, err)
		}
		files = append(files, snap.File{Name: AssetEntry(asset), Data: b})
	}
	box := func(x0, y0, z0, x1, y1, z1 float32) *assetdata.Object {
		return obj("min", []any{x0, y0, z0}, "max", []any{x1, y1, z1})
	}
	write("Made_Rock.Noun", obj("nounType", "obstacle", "graphicsScale", float32(1), "modelPath",
		"Environments/made/made_rock.bmdl", "bbox", box(-2, -1, 0, 2, 1, 2)))
	write(ShadowNoun, obj("nounType", "obstacle", "graphicsScale", float32(1), "modelPath",
		"Environments/made/made_shadow.bmdl", "bbox", box(-19, -12, 0, 19, 12, 0)))
	write("Made_Beast.Noun", obj("nounType", "creature", "graphicsScale", float32(1)))
	write(TemplateLevel+"_D_Rocks.Markerset", obj("markers", []any{
		madeUpMarker("Made_Rock.Noun", 1201, 10, 10, -0.5, "createWithCollision", true),
		madeUpMarker("Made_Rock.Noun", 1202, 14, 10, 0),
		madeUpMarker(ShadowNoun, 1203, 30, 30, 0.1),
		madeUpMarker("Made_Beast.Noun", 1204, 12, 12, 0),
		madeUpMarker("Made_Rock.Noun", 1205, 100, 100, 0),
	}, "group", "none", "weight", float32(1)))
	return snap.Pack(files)
}

func propsRing(t *testing.T) *Map {
	m := ring(t)
	m.Props = []Prop{
		{Noun: "made_rock", Pos: [3]float64{-125, -125}, Rot: 30, Scale: 2, Blocks: true, Shadow: true},
		{Group: &Group{Level: TemplateLevel, Sets: []string{"D_Rocks"}, At: Point{12, 10}, R: 5},
			Pos: [3]float64{-120, -160, 1}, Rot: 90},
		{Noun: "Made_Rock.Noun", Pos: [3]float64{0.2, 0.1, 0}},
	}
	m.Scatter = []Scatter{{Nouns: []string{"made_rock"}, Seed: 3, Max: 3}}
	return m
}

// TestPropsMadeUp: CM_Ring with props from made-up assets: the props markerset (last in the Level, in the catalog)
// with each placement, read back as written; the blocking prop's footprint from the noun's box; the group's members
// moved and turned, its creature left out; the centre prop once; a creature noun refused.
func TestPropsMadeUp(t *testing.T) {
	ad := propsAssetData(t)
	m := propsRing(t)
	if err := ResolveProps(m, ad); err != nil {
		t.Fatal(err)
	}
	r, cm := Check(m, nil)
	if !r.OK || len(r.Findings) != 0 {
		t.Fatalf("%+v", r.Findings)
	}
	if cm.Layers.KindAt(Point{-125, -125}) != Solid || cm.Layers.KindAt(Point{125, 125}) != Solid {
		t.Errorf("the rock's footprint (its box) isn't stamped")
	}
	as, err := cm.Assets(ad, madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	checkAssets(t, m, as)
	if len(as.Findings) != 1 || as.Findings[0].Rule != "props.group" {
		t.Errorf("findings %+v", as.Findings)
	}
	set := m.Name + "_props.Markerset"
	if as.Names[len(as.Names)-2] != set {
		t.Errorf("assets %v", as.Names)
	}
	ms := markers(as.Values[set])
	// the rock and its card, twice; the group's two rocks, twice; the centre rock; three scattered, twice
	if len(ms) != 4+4+1+6 {
		t.Fatalf("%d props markers", len(ms))
	}
	want := []struct {
		noun        string
		x, y, z, rz float64
		scale       float64
	}{
		{"Made_Rock.Noun", -125, -125, 0, 30, 2}, {ShadowNoun, -125, -125, 0.1, 30, 0.295},
		{"Made_Rock.Noun", 125, 125, 0, -150, 2}, {ShadowNoun, 125, 125, 0.1, -150, 0.295},
		// (10, 10) and (14, 10) round (12, 10), turned by 90 degrees to (-120, -162) and (-120, -158)
		{"Made_Rock.Noun", -120, -162, 0.5, normDeg(1201%360 + 90), 1}, {"Made_Rock.Noun", 120, 162, 0.5, normDeg(1201%360 + 270), 1},
		{"Made_Rock.Noun", -120, -158, 1, normDeg(1202%360 + 90), 1}, {"Made_Rock.Noun", 120, 158, 1, normDeg(1202%360 + 270), 1},
		{"Made_Rock.Noun", 0.2, 0.1, 0, 0, 1},
	}
	for i, w := range want {
		mk := ms[i]
		p, rot := vec(mk, "pos"), vec(mk, "rotDegrees")
		near := func(a, b float64) bool { return math.Abs(a-b) < 1e-3 }
		if text(mk, "nounDef") != w.noun || !near(p[0], w.x) || !near(p[1], w.y) || !near(p[2], w.z) ||
			!near(rot[2], w.rz) || !near(fval(mustGet(mk, "scale")), w.scale) {
			t.Errorf("marker %d: %s at %v turned %v scale %v; want %+v", i, text(mk, "nounDef"), p, rot,
				mustGet(mk, "scale"), w)
		}
	}
	if v, _ := ms[0].Get("createWithCollision"); v != true {
		t.Errorf("the single rock didn't copy the shipped marker's flags")
	}
	src := Catalog(m)
	if src.Entries[len(src.Entries)-2].Name != set || !strings.HasSuffix(src.Entries[len(src.Entries)-2].Source,
		"/"+set) {
		t.Errorf("catalog %+v", src.Entries)
	}
	// the same map with a creature as a prop isn't built
	m = propsRing(t)
	m.Props = append(m.Props, Prop{Noun: "made_beast", Pos: [3]float64{-150, -150}})
	_, cm = Check(m, nil)
	pls, fs, err := cm.PlacedProps(ad)
	if err != nil || len(fs) != 2 || !has(&Report{Findings: fs}, "props.type", Error) {
		t.Errorf("%v %+v", err, fs)
	}
	for _, p := range pls {
		if strings.EqualFold(p.Noun, "Made_Beast.Noun") {
			t.Errorf("the creature was placed")
		}
	}
	// a polygon region: the members inside it (the two rocks and the creature, not the card at (30, 30) or the rock at
	// (100, 100)), placed unmoved at pos == at and mirrored; written and read back
	m = ring(t)
	m.Props = []Prop{{Group: &Group{Level: TemplateLevel, Sets: []string{"D_Rocks"}, At: Point{-50, -50},
		Polygon: pts(0, 0, 20, 0, 20, 20, 0, 20)}, Pos: [3]float64{-50, -50}, Scale: 1}}
	js, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(js)
	if err != nil || len(back.Props) != 1 || len(back.Props[0].Group.Polygon) != 4 || back.Props[0].Group.R != 0 {
		t.Fatalf("%v %s", err, js)
	}
	r, cm = Check(back, nil)
	if !r.OK || len(r.Findings) != 0 {
		t.Fatalf("%+v", r.Findings)
	}
	pls, fs, err = cm.PlacedProps(ad)
	if err != nil || len(fs) != 1 || fs[0].Rule != "props.group" || len(pls) != 4 {
		t.Fatalf("%v %+v %+v", err, fs, pls)
	}
	for i, w := range []Point{{10, 10}, {-10, -10}, {14, 10}, {-14, -10}} {
		if p := pls[i].Pos; math.Abs(p[0]-w[0]) > 1e-3 || math.Abs(p[1]-w[1]) > 1e-3 || pls[i].Team != 1+i%2 {
			t.Errorf("member %d at %v team %d; want %v", i, p, pls[i].Team, w)
		}
	}
	// no props: CM_Ring's assets as before
	if names := MapAssetNames(ring(t)); strings.Join(names, ",") != strings.Join(AssetNames("CM_Ring"), ",") {
		t.Errorf("%v", names)
	}
}

// TestGameProps builds CM_Dress from build 854's files (pinned; twice the same bytes): its props markerset holds
// every placement, each a shipped noun; the blocking rock blocks the line of sight; the nouns a prop may name.
func TestGameProps(t *testing.T) {
	dir := testgame.Dir(t)
	in, err := ReadInputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, r, err := Build(dress(t), in, SharedMesh{}, nil)
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if len(r.Findings) != 0 {
		t.Errorf("findings %+v", r.Findings)
	}
	if got := digest(b); got != Dress854SHA256 {
		t.Errorf("SHA-256 %s, pinned %s", got, Dress854SHA256)
	}
	again, _, err := Build(dress(t), in, SharedMesh{}, nil)
	if err != nil || !bytes.Equal(again, b) {
		t.Errorf("a second build gives other bytes (%v)", err)
	}
	m := dress(t)
	if err := ResolveProps(m, in.AssetData); err != nil {
		t.Fatal(err)
	}
	_, cm := Check(m, nil)
	pls, fs, err := cm.PlacedProps(in.AssetData)
	if err != nil || len(fs) != 0 {
		t.Fatalf("%v %+v", err, fs)
	}
	a, err := snap.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := assetdata.Embedded()
	s, err := ReadShipped(dir)
	if err != nil {
		t.Fatal(err)
	}
	var set *assetdata.Object
	for _, e := range a.Entries {
		if e.Name == AssetEntry("CM_Dress_props.Markerset") {
			raw, _ := a.Read(e)
			if set, err = ts.ParseExact(ts.Type("Markerset"), raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	if set == nil || len(markers(set)) != len(pls) {
		t.Fatalf("the props markerset: %d markers for %d placements", len(markers(set)), len(pls))
	}
	for _, mk := range markers(set) {
		if !s.Nouns[nounKey(text(mk, "nounDef"))] {
			t.Errorf("%s isn't a shipped noun", text(mk, "nounDef"))
		}
	}
	if cm.Layers.KindAt(Point{-125, -125}) != Solid || cm.Layers.LoS().Pix[func() int {
		i, j := cm.Layers.CellOf(Point{125, 125})
		return j*Grid + i
	}()] != 128 {
		t.Errorf("the blocking rock doesn't block")
	}
	counts := map[string]int{}
	for _, p := range pls {
		counts[p.Path]++
	}
	t.Logf("CM_Dress: %d props markers %v", len(pls), counts)
	ns, err := PropNouns(in.AssetData)
	if err != nil || len(ns) < 300 {
		t.Fatalf("%v: %d prop nouns", err, len(ns))
	}
	for _, n := range ns {
		if !isPropType(n.Type) {
			t.Errorf("%s: %s", n.Name, n.Type)
		}
	}
}
