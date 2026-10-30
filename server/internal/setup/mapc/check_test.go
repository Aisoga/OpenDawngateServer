package mapc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/testmaps"
)

// ringDir is the repository's first test map.
var ringDir = testmaps.Pkg("CM_Ring")

func ring(t *testing.T) *Map {
	t.Helper()
	m, err := Load(ringDir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func has(r *Report, rule, sev string) bool {
	for _, f := range r.Findings {
		if f.Rule == rule && f.Severity == sev {
			return true
		}
	}
	return false
}

func TestRingPasses(t *testing.T) {
	r, cm := Check(ring(t), nil)
	if !r.OK || len(r.Findings) != 0 {
		t.Fatalf("CM_Ring: %+v", r.Findings)
	}
	if cm.Player.Areas >= AreaBudget || cm.Cell < MinCell || cm.Cell > MaxCell {
		t.Errorf("%v", cm)
	}
	t.Logf("%v; stats %+v", cm, *r.Stats)
}

// bakeLayer makes the map's ground baked with the earth under one more layer.
func bakeLayer(m *Map, l GroundLayer) {
	m.Ground.Style = "baked"
	m.Ground.Layers = []GroundLayer{{Texture: TextureRef{Procedural: "dirt"}}, l}
}

func pts(ps ...float64) []Point {
	var out []Point
	for i := 0; i+1 < len(ps); i += 2 {
		out = append(out, Point{ps[i], ps[i+1]})
	}
	return out
}

// TestRules breaks CM_Ring once per rule (one made-up broken map each) and expects the rule's finding.
func TestRules(t *testing.T) {
	autoBindings := func(m *Map) {
		m.Base.Bindings = Bindings{Lanes: map[string][]Point{}}
		for _, ln := range m.Lanes {
			m.Base.Bindings.Lanes[string(rune('0'+ln.ID))] = AutoBindings(ln.ID, ln.Knots)
		}
	}
	cases := []struct {
		rule, sev string
		others    []Other
		edit      func(m *Map)
	}{
		{"format.version", Error, nil, func(m *Map) { m.Format = 2 }},
		{"names.name", Error, nil, func(m *Map) { m.Name = "CM Ring" }},
		{"names.name", Error, nil, func(m *Map) { m.Name = strings.Repeat("A", 81) }},
		{"names.shipped", Error, nil, func(m *Map) { m.Name = "sm_map_v5" }},
		{"names.unique", Error, []Other{{"cm_ring", "other"}}, func(m *Map) {}},
		{"names.tag", Error, nil, func(m *Map) { m.Tag = "Ring" }},
		{"names.tag-unique", Error, nil, func(m *Map) { m.Tag = "mm" }},
		{"names.tag-unique", Error, []Other{{"CM_Other", "ring"}}, func(m *Map) {}},
		{"names.display", Error, nil, func(m *Map) { m.Display = "A Test Ring Map" }},
		{"names.display", Error, nil, func(m *Map) { m.Display = "" }},
		{"names.prefix", Error, nil, func(m *Map) { m.Prefix = "[A very long prefix] " }},
		{"size.max", Error, nil, func(m *Map) { m.Size = 640 }},
		{"size.bounds", Error, nil, func(m *Map) { m.Base.Locus = Point{-219, -196} }},
		{"symmetry.kind", Error, nil, func(m *Map) { m.Symmetry = "mirror" }},
		{"symmetry.half", Error, nil, func(m *Map) { m.Wells[0].Pos = Point{60, 138} }},
		{"symmetry.half", Error, nil, func(m *Map) { m.Terrain.Shapes[4].Circle = &Point{60, 138} }},
		{"symmetry.base", Error, nil, func(m *Map) { m.Base.Guardian = Point{-142, 10} }},
		{"symmetry.lanes", Error, nil, func(m *Map) { m.Lanes[1].Knots[3] = Point{40, -174} }},
		{"symmetry.lanes", Error, nil, func(m *Map) { m.Lanes[1].Width = 20 }},
		{"refs.environment", Error, nil, func(m *Map) { m.Environment = "TA_Map_v1" }},
		{"refs.tuning", Error, nil, func(m *Map) { m.Tuning = "" }},
		{"ground.style", Error, nil, func(m *Map) { m.Ground.Style = "painted" }},
		{"ground.baked", Error, nil, func(m *Map) { m.Ground.Style, m.Ground.Texels = "tiled", 2048 }},
		{"ground.texels", Error, nil, func(m *Map) { m.Ground.Style, m.Ground.Texels = "baked", 3000 }},
		{"ground.tiles", Error, nil, func(m *Map) { m.Ground.Style, m.Ground.Tiles = "baked", 3 }},
		{"ground.minimap", Error, nil, func(m *Map) { m.Ground.Style, m.Ground.Minimap = "baked", "paint" }},
		{"ground.texture", Error, nil, func(m *Map) { bakeLayer(m, GroundLayer{Auto: "high"}) }},
		{"ground.texture", Error, nil, func(m *Map) {
			bakeLayer(m, GroundLayer{Texture: TextureRef{Game: "x_D.dds", Archive: "zz_CM_Ring.snap"}, Auto: "high"})
		}},
		{"ground.texture", Error, nil, func(m *Map) {
			bakeLayer(m, GroundLayer{Texture: TextureRef{Procedural: "lava"}, Auto: "high"})
		}},
		{"ground.file", Error, nil, func(m *Map) {
			bakeLayer(m, GroundLayer{Texture: TextureRef{File: "../stolen.png"}, Auto: "high"})
		}},
		{"ground.file", Error, nil, func(m *Map) {
			bakeLayer(m, GroundLayer{Texture: TextureRef{File: "ground/missing.png"}, Auto: "high"})
		}},
		{"ground.mask", Error, nil, func(m *Map) { bakeLayer(m, GroundLayer{Texture: TextureRef{Procedural: "dirt"}}) }},
		{"ground.auto", Error, nil, func(m *Map) {
			bakeLayer(m, GroundLayer{Texture: TextureRef{Procedural: "dirt"}, Auto: "lanes"})
		}},
		{"ground.shape", Error, nil, func(m *Map) {
			bakeLayer(m, GroundLayer{Texture: TextureRef{Procedural: "dirt"}, Shapes: []MaskShape{{Lane: 3}}})
		}},
		{"ground.scale", Error, nil, func(m *Map) {
			bakeLayer(m, GroundLayer{Texture: TextureRef{Procedural: "dirt"}, Auto: "high", Scale: 0.1})
		}},
		{"ground.tint", Error, nil, func(m *Map) {
			bakeLayer(m, GroundLayer{Texture: TextureRef{Procedural: "dirt"}, Auto: "high", Tint: &[3]float64{1, 9, 1}})
		}},
		{"ground.base-mask", Warning, nil, func(m *Map) {
			m.Ground.Style = "baked"
			m.Ground.Layers = []GroundLayer{{Texture: TextureRef{Procedural: "dirt"}, Auto: "floor"}}
		}},
		{"ground.size", Error, nil, func(m *Map) {
			m.Ground.Style = "baked"
			m.Ground.Stamps = []GroundStamp{{Image: TextureRef{Game: "a_D.dds", Archive: "anshavar3.snap"}, Size: [2]float64{0, 4}}}
		}},
		{"ground.texture", Error, nil, func(m *Map) {
			m.Ground.Style = "baked"
			m.Ground.Stamps = []GroundStamp{{Image: TextureRef{Procedural: "dirt"}, Size: [2]float64{4, 4}}}
		}},
		{"ground.cliff", Error, nil, func(m *Map) {
			m.Ground.Style = "baked"
			m.Ground.Cliff = &GroundCliff{Texture: TextureRef{Procedural: "dirt"}, Scale: 3}
		}},
		{"ground.decal", Error, nil, func(m *Map) {
			m.Ground.Decals = []GroundDecal{{Diffuse: "letters.tga", Size: [3]float64{2, 2, 2}}}
		}},
		{"ground.decal", Error, nil, func(m *Map) {
			m.Ground.Decals = []GroundDecal{{Diffuse: "letters.dds", Size: [3]float64{2, 0, 2}}}
		}},
		{"lanes.count", Error, nil, func(m *Map) { m.Lanes = append(m.Lanes, m.Lanes[0]) }},
		{"lanes.count", Error, nil, func(m *Map) { m.Lanes = nil }},
		{"lanes.ids", Error, nil, func(m *Map) { m.Lanes[1].ID = 3 }},
		{"lanes.ids", Error, nil, func(m *Map) { m.Lanes[1].ID = 1 }},
		{"lanes.width", Error, nil, func(m *Map) { m.Lanes[0].Width, m.Lanes[1].Width = 4, 4 }},
		{"lanes.knots", Error, nil, func(m *Map) { m.Lanes[0].Knots = m.Lanes[0].Knots[:1] }},
		{"lanes.knots", Error, nil, func(m *Map) { m.Lanes[0].Knots[1] = m.Lanes[0].Knots[0] }},
		{"lanes.knots", Error, nil, func(m *Map) {
			for i := range m.Lanes {
				ks := m.Lanes[i].Knots
				var more []Point
				for k := 0; k+1 < len(ks); k++ {
					more = append(more, ks[k], Point{(ks[k][0] + ks[k+1][0]) / 2, (ks[k][1] + ks[k+1][1]) / 2})
				}
				m.Lanes[i].Knots = append(more, ks[len(ks)-1])
			}
		}},
		{"fixed.spawns", Error, nil, func(m *Map) { m.Base.Spawns = m.Base.Spawns[:4] }},
		{"fixed.cores", Error, nil, func(m *Map) { m.Base.Cores = Cores{Points: pts(-110, -110, -160, -100)} }},
		{"fixed.bindings", Error, nil, func(m *Map) {
			autoBindings(m)
			m.Base.Bindings.Lanes["2"] = m.Base.Bindings.Lanes["2"][:2]
		}},
		{"fixed.bindings", Error, nil, func(m *Map) {
			autoBindings(m)
			m.Base.Bindings.Lanes["3"] = m.Base.Bindings.Lanes["2"]
		}},
		{"fixed.bindings", Error, nil, func(m *Map) { autoBindings(m); delete(m.Base.Bindings.Lanes, "1") }},
		{"budget.wells", Error, nil, func(m *Map) {
			for len(m.Wells) < 5 {
				m.Wells = append(m.Wells, m.Wells[0])
			}
		}},
		{"budget.wells", Error, nil, func(m *Map) { m.Wells = nil }},
		{"camps.kind", Error, nil, func(m *Map) { m.Camps = []Camp{{Kind: "boss", Noun: "x.Noun", Pos: Point{-60, -138}}} }},
		{"camps.noun", Error, nil, func(m *Map) { m.Camps = []Camp{{Kind: "camp", Pos: Point{-60, -138}}} }},
		{"camps.parasite", Error, nil, func(m *Map) {
			p := Camp{Kind: CampParasite, Noun: "SpawnPoint_Objective_Titan.Noun", Pos: Point{-60, -130}}
			m.Camps = []Camp{p, p}
		}},
		{"camps.parasite", Warning, nil, func(m *Map) {
			m.Camps = []Camp{{Kind: CampParasite, Noun: "SpawnPoint_Objective_Titan.Noun", Pos: Point{-60, -130}}}
		}},
		{"camps.role", Warning, nil, func(m *Map) {
			m.Camps = []Camp{{Kind: CampBuff, Noun: "SpawnPoint_v5_CreepsUgger.Noun", Pos: Point{-60, -130}}}
		}},
		{"camps.role", Warning, nil, func(m *Map) {
			m.Camps = []Camp{{Kind: CampCamp, Noun: "SpawnPoint_v5_CreepsBoar.Noun", Pos: Point{-60, -130}}}
		}},
		{"terrain.default", Error, nil, func(m *Map) { m.Terrain.Default = "lava" }},
		{"terrain.shape", Error, nil, func(m *Map) { m.Terrain.Shapes[2].Type = "water" }},
		{"terrain.shape", Error, nil, func(m *Map) { m.Terrain.Shapes[2].Polygon = pts(0, 0, 1, 0, 1, 1) }},
		{"terrain.shape", Error, nil, func(m *Map) { m.Terrain.Shapes[2].R = 0 }},
		{"terrain.shape", Error, nil, func(m *Map) { m.Terrain.Shapes[0].Lane = 3 }},
		{"terrain.shape", Error, nil, func(m *Map) { m.Terrain.Shapes[7] = Shape{Type: "bush", Lane: 1} }},
		{"fixed.open", Error, nil, func(m *Map) { m.Base.Shop = Point{-215, -150} }},
		{"fixed.hole", Error, nil, func(m *Map) { m.Base.Spawns[0] = Point{-137, -137} }},
		{"fixed.range", Error, nil, func(m *Map) {
			autoBindings(m)
			m.Base.Bindings.Lanes["1"][0] = Point{-140, 100}
		}},
		{"fixed.tiers", Error, nil, func(m *Map) {
			autoBindings(m)
			b := m.Base.Bindings.Lanes["1"]
			b[0], b[2] = b[2], b[0]
		}},
		{"fixed.tiers", Error, nil, func(m *Map) {
			autoBindings(m)
			b := m.Base.Bindings.Lanes["1"]
			b[1], b[2] = b[2], b[1]
		}},
		{"lanes.graph", Error, nil, func(m *Map) { m.add(Shape{Type: "wall", Circle: &Point{-174, 0}, R: 30}) }},
		{"reach.islands", Error, nil, func(m *Map) { m.add(Shape{Type: "wall", Circle: &Point{-174, 0}, R: 30}) }},
		{"lanes.start", Error, nil, func(m *Map) { m.add(Shape{Type: "wall", Circle: &Point{-171, -166}, R: 6}) }},
		{"lanes.end", Error, nil, func(m *Map) {
			m.Lanes[0].Knots = m.Lanes[0].Knots[:8]
			m.Lanes[1].Knots = m.Lanes[1].Knots[2:]
		}},
		{"reach.player", Error, nil, func(m *Map) { m.Terrain.Shapes[5].Type = "wall" }},
		{"reach.dropped", Warning, nil, func(m *Map) { m.Terrain.Shapes[5].Type = "wall" }},
		{"reach.npc", Error, nil, func(m *Map) { m.Wells[0].Pos = Point{-60, -110} }},
		{"budget.areas", Error, nil, func(m *Map) { m.Terrain.Default = "open" }},
		{"budget.bush", Error, nil, func(m *Map) { m.Terrain.Shapes[7].Polygon = pts(-182, -10, -181, -10, -181, -9) }},
		{"budget.bush", Warning, nil, func(m *Map) { m.Terrain.Shapes[7].Polygon = pts(-200, -10, -190, -10, -190, 0) }},
		{"terrain.edge", Warning, nil, func(m *Map) { m.Terrain.Shapes[3].R = 30 }},
	}
	for _, tc := range cases {
		m := ring(t)
		tc.edit(m)
		r, _ := Check(m, tc.others)
		if !has(r, tc.rule, tc.sev) {
			t.Errorf("%s (%s): not found in %+v", tc.rule, tc.sev, r.Findings)
		}
		if tc.sev == Error && r.OK {
			t.Errorf("%s: the report says ok", tc.rule)
		}
	}
}

func (m *Map) add(s Shape) { m.Terrain.Shapes = append(m.Terrain.Shapes, s) }

func TestCheckShipped(t *testing.T) {
	m := ring(t)
	m.Camps = []Camp{{Kind: "camp", Noun: "Missing.Noun", Pos: Point{-60, -138}}}
	m.Props = []Prop{{Noun: "rock_a.Noun"}}
	m.Name = "CM_Shipped"
	m.Environment = "SM_Map_v5"
	r, _ := Check(m, nil)
	s := &Shipped{Nouns: map[string]bool{"rock_a": true}, Levels: map[string]bool{"cm_shipped": true}}
	CheckShipped(m, s, r)
	if !has(r, "nouns.exist", Error) || !has(r, "names.shipped", Error) || !has(r, "refs.environment", Error) {
		t.Fatalf("%+v", r.Findings)
	}
	n := 0
	for _, f := range r.Findings {
		if f.Rule == "nouns.exist" {
			n++
		}
	}
	if n != 1 || r.OK {
		t.Errorf("%d nouns.exist findings (1: the camp's), ok %v", n, r.OK)
	}
}

func TestParse(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(ringDir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(b)
	if err != nil || !m.Base.Cores.Auto || !m.Base.Bindings.Auto {
		t.Fatalf("%v %+v", err, m.Base)
	}
	for _, bad := range []string{
		strings.Replace(string(b), `"tag"`, `"tags"`, 1),                    // an unknown member
		strings.Replace(string(b), `"cores": "auto"`, `"cores": "some"`, 1), // neither auto nor points
		strings.Replace(string(b), `"bindings": "auto"`, `"bindings": "x"`, 1),
		string(b) + "{}", // two values
		`{"format": 1, "size": "big"}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted: %.60q", bad)
		}
	}
	explicit := strings.Replace(string(b), `"cores": "auto"`, `"cores": [[-110, -110], [-159, -99], [-99, -159], `+
		`[-189, -138], [-138, -189]]`, 1)
	explicit = strings.Replace(explicit, `"bindings": "auto"`, `"bindings": {"1": [[-176, 47], [-166, -34], `+
		`[-178, -77]], "2": [[47, -176], [-34, -166], [-77, -178]]}`, 1)
	m, err = Parse([]byte(explicit))
	if err != nil || m.Base.Cores.Auto || len(m.Base.Cores.Points) != 5 || len(m.Base.Bindings.Lanes["2"]) != 3 {
		t.Fatalf("%v %+v", err, m.Base)
	}
	if r, _ := Check(m, nil); !r.OK {
		t.Errorf("explicit Cores and Bindings: %+v", r.Findings)
	}
}

// TestResolve: the mirror, the derived places at SM_Map_v5's numbers.
func TestResolve(t *testing.T) {
	m := ring(t)
	l := Resolve(m)
	t1, t2 := &l.Teams[0], &l.Teams[1]
	near := func(a, b Point) bool { return dist(a, b) < 1e-9 }
	if !near(t2.Guardian, Point{142, 142}) || !near(t1.Cores[0], Point{-142 + 32.4622, -142 + 32.4622}) ||
		!near(t2.Cores[4], Mirror(t1.Cores[4])) {
		t.Errorf("guardian %v, cores %v %v", t2.Guardian, t1.Cores, t2.Cores)
	}
	// lane 1's spawner: 21 m behind (-170, -145) towards (-170, -100)
	if !near(t1.Spawners[1], Point{-170, -166}) || !near(t2.Spawners[1], Point{145 + 21, 170}) {
		t.Errorf("spawners %v %v", t1.Spawners, t2.Spawners)
	}
	// team 2's lane 1 is team 1's walked back; its Bindings are team 1's lane 2 Bindings mirrored
	if !near(t2.Lanes[1][0], Point{145, 170}) || !near(t2.Bindings[1][2], Mirror(t1.Bindings[2][2])) {
		t.Errorf("team 2's lane 1 %v, bindings %v", t2.Lanes[1][:1], t2.Bindings[1])
	}
	// the tiers lie at SM's fractions of the lane
	total := length(t1.Lanes[1])
	for k, p := range t1.Bindings[1] {
		s, d := project(t1.Lanes[1], p)
		if f := s / total; f < smBindingFractions[k]-0.01 || f > smBindingFractions[k]+0.01 || d > 10 {
			t.Errorf("lane 1 %s: fraction %.3f, %.1f m off", Tiers[k], f, d)
		}
	}
}

func TestMeshHex(t *testing.T) {
	cooked := append([]byte("NXS\x01MESH"), make([]byte, 70)...)
	dgmi := []byte("DGMI made up")
	text := WriteMeshHex(dgmi, cooked)
	sha, got, err := ReadMeshHex(text)
	if err != nil || sha != digest(dgmi) || string(got) != string(cooked) {
		t.Fatalf("%v %s", err, sha)
	}
	for _, bad := range [][]byte{text[1:], []byte("# dgmi-sha256 " + sha + "\nzz\n"),
		[]byte("# dgmi-sha256 " + sha + "\n0011\n")} {
		if _, _, err := ReadMeshHex(bad); err == nil {
			t.Errorf("accepted %.40q", bad)
		}
	}
}
