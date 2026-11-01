package mapc

import (
	"bytes"
	"testing"
)

// RingDerivedMadeUpSHA256 pins madeUpBaked with the two rules that follow the gameplay shapes added (a lanes band
// and an edge band along the chasms), so every platform bakes them alike.
const RingDerivedMadeUpSHA256 = "ad02fa92b95ee4c3be0459a5d292a665b55d5198d2263eb585e3661810972f9e"

// openCentre is CM_Ring with open ground round its central chasm (r 40 in r 70), so the chasm has an edge on
// walkable ground: (0, -44) is 4 m from the chasm and 26 m from the walls, (0, -67) 3 m from the walls and 27 m from
// the chasm.
func openCentre(t *testing.T) (*Map, ShippedFiles) {
	t.Helper()
	m, read := madeUpBaked(t)
	for k, s := range m.Terrain.Shapes {
		if s.Type == "chasm" {
			c := Point{0, 0}
			open := Shape{Type: "open", Circle: &c, R: 70}
			m.Terrain.Shapes = append(m.Terrain.Shapes[:k], append([]Shape{open}, m.Terrain.Shapes[k:]...)...)
			return m, read
		}
	}
	t.Fatal("CM_Ring has no chasm")
	return nil, nil
}

// derivedMask bakes m and returns layer l's mask (as the second layer).
func derivedMask(t *testing.T, m *Map, read ShippedFiles, l GroundLayer, noDerived bool) func(x, y float64) float64 {
	t.Helper()
	r, cm := Check(m, nil)
	if cm == nil || !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	cm.NoDerived = noDerived
	b, err := cm.Bake(read)
	if err != nil {
		t.Fatal(err)
	}
	f, err := cm.layerMask(1, l, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestAutoLanes: "lanes" covers both lanes' corridors at their width or the layer's, follows a moved lane, and
// feathers by the layer's soft.
func TestAutoLanes(t *testing.T) {
	m, read := madeUpBaked(t)
	zero, four := 0.0, 4.0
	l := GroundLayer{Texture: TextureRef{RGB: &[3]float64{1, 2, 3}}, Auto: "lanes", Soft: &zero}
	f := derivedMask(t, m, read, l, false)
	for _, c := range []struct {
		x, y, want float64
	}{{-174, 0, 1}, {-164, 0, 1}, {174, 0, 1}, {0, -174, 1}, {-174 + 13, 0, 0}, {-100, -100, 0}, {0, 0, 0}} {
		if got := f(c.x, c.y); got != c.want {
			t.Errorf("lanes at (%g, %g): %g, want %g", c.x, c.y, got, c.want)
		}
	}
	l.Width = 8
	f = derivedMask(t, m, read, l, false)
	if f(-174, 0) != 1 || f(-169, 0) != 0 || f(169, 0) != 0 || f(172, 0) != 1 {
		t.Errorf("width 8: %g %g %g %g", f(-174, 0), f(-169, 0), f(169, 0), f(172, 0))
	}
	l.Soft = &four // the feather: half drawn at the band's edge, full 2 m inside, none 2 m out
	f = derivedMask(t, m, read, l, false)
	if v := f(-170, 0); !(v > 0.45 && v < 0.55) || f(-172, 0) != 1 || f(-168, 0) != 0 {
		t.Errorf("soft 4: %g at the edge, %g, %g", v, f(-172, 0), f(-168, 0))
	}
	// the lane moved (lane 1's middle legs to x = -150, lane 2's mirror with it): the band follows
	l.Soft = &zero
	m.Lanes[0].Knots[2], m.Lanes[0].Knots[3] = Point{-150, -30}, Point{-150, 30}
	m.Lanes[1].Knots[6], m.Lanes[1].Knots[7] = Point{150, -30}, Point{150, 30}
	f = derivedMask(t, m, read, l, false)
	if f(-150, 0) != 1 || f(-174, 0) != 0 || f(150, 0) != 1 || f(174, 0) != 0 {
		t.Errorf("a moved lane: %g %g %g %g", f(-150, 0), f(-174, 0), f(150, 0), f(174, 0))
	}
	// hidden derived art: the rule leaves the mask, a shape stays
	l.Shapes = []MaskShape{{Circle: &Point{-100, -100}, R: 5}}
	f = derivedMask(t, m, read, l, true)
	if f(-150, 0) != 0 || f(150, 0) != 0 || f(-100, -100) != 1 || f(100, 100) != 1 {
		t.Errorf("no derived: %g %g, the shape %g %g", f(-150, 0), f(150, 0), f(-100, -100), f(100, 100))
	}
}

// TestAutoEdge: "edge" is a band of the walkable ground along its edge, against the walls, the chasms or both, a
// negative start reaching onto the blocked side.
func TestAutoEdge(t *testing.T) {
	m, read := openCentre(t)
	zero := 0.0
	l := GroundLayer{Texture: TextureRef{RGB: &[3]float64{1, 2, 3}}, Auto: "edge", Soft: &zero}
	cases := []struct {
		along string
		rng   *[2]float64
		x, y  float64
		want  float64
	}{
		{"", nil, 0, -44, 1}, {"", nil, 0, -67, 1}, {"", nil, 0, -55, 0}, {"", nil, 0, -30, 0}, {"", nil, 0, 44, 1},
		{"chasms", nil, 0, -44, 1}, {"chasms", nil, 0, -67, 0}, {"walls", nil, 0, -44, 0}, {"walls", nil, 0, -67, 1},
		{"chasms", &[2]float64{-4, 0}, 0, -38, 1}, {"walls", &[2]float64{-4, 0}, 0, -38, 0},
		{"walls", &[2]float64{-4, 0}, 0, -72, 1}, {"", &[2]float64{2, 3}, 0, -44, 0},
	}
	for _, c := range cases {
		l.Along, l.Range = c.along, c.rng
		f := derivedMask(t, m, read, l, false)
		if got := f(c.x, c.y); got != c.want {
			t.Errorf("edge along %q range %v at (%g, %g): %g, want %g", c.along, c.rng, c.x, c.y, got, c.want)
		}
	}
	// CM_Ring as it is has no chasm edge on walkable ground, and a map without chasms no chasm band at all
	m, read = madeUpBaked(t)
	l.Along, l.Range = "chasms", nil
	f := derivedMask(t, m, read, l, false)
	if f(0, -44) != 0 || f(0, -38) != 0 {
		t.Errorf("CM_Ring's walled chasm: %g %g", f(0, -44), f(0, -38))
	}
	var shapes []Shape
	for _, s := range m.Terrain.Shapes {
		if s.Type != "chasm" {
			shapes = append(shapes, s)
		}
	}
	m.Terrain.Shapes = shapes
	f = derivedMask(t, m, read, l, false)
	for _, p := range []Point{{0, 0}, {-174, 0}, {-150, -150}} {
		if v := f(p[0], p[1]); v != 0 {
			t.Errorf("no chasms: %g at %v", v, p)
		}
	}
	m, read = openCentre(t)
	l.Along = ""
	if f = derivedMask(t, m, read, l, true); f(0, -44) != 0 || f(0, -67) != 0 {
		t.Error("no derived: the edge band stays")
	}
}

// TestDerivedRulesChecked: the new members' ranges, and a warning when a layer of another rule has one.
func TestDerivedRulesChecked(t *testing.T) {
	for _, c := range []struct {
		l    GroundLayer
		rule string
		sev  string
	}{
		{GroundLayer{Auto: "lanes", Width: 1}, "ground.width", Error},
		{GroundLayer{Auto: "lanes", Width: 121}, "ground.width", Error},
		{GroundLayer{Auto: "high", Width: 10}, "ground.width", Warning},
		{GroundLayer{Auto: "edge", Range: &[2]float64{4, 2}}, "ground.range", Error},
		{GroundLayer{Auto: "edge", Range: &[2]float64{-21, 2}}, "ground.range", Error},
		{GroundLayer{Auto: "edge", Range: &[2]float64{0, 61}}, "ground.range", Error},
		{GroundLayer{Auto: "slope", Range: &[2]float64{0, 6}}, "ground.range", Warning},
		{GroundLayer{Auto: "edge", Along: "wall"}, "ground.along", Error},
		{GroundLayer{Auto: "lanes", Along: "walls"}, "ground.along", Warning},
		{GroundLayer{Auto: "lane"}, "ground.auto", Error},
	} {
		m, _ := madeUpBaked(t)
		c.l.Texture = TextureRef{RGB: &[3]float64{1, 2, 3}}
		m.Ground.Layers = append(m.Ground.Layers, c.l)
		if r, _ := Check(m, nil); !has(r, c.rule, c.sev) {
			t.Errorf("%+v: no %s %s: %+v", c.l, c.sev, c.rule, r.Findings)
		}
	}
	m, _ := madeUpBaked(t)
	m.Ground.Layers = append(m.Ground.Layers,
		GroundLayer{Texture: TextureRef{RGB: &[3]float64{1, 2, 3}}, Auto: "lanes", Width: 10},
		GroundLayer{Texture: TextureRef{RGB: &[3]float64{1, 2, 3}}, Auto: "edge", Range: &[2]float64{-2, 5}, Along: "walls"},
		GroundLayer{Texture: TextureRef{RGB: &[3]float64{1, 2, 3}}, Auto: "edge", Along: "chasms"})
	r, _ := Check(m, nil)
	for _, f := range r.Findings {
		if f.Rule == "ground.width" || f.Rule == "ground.range" || f.Rule == "ground.along" || f.Rule == "ground.auto" {
			t.Errorf("a good layer: %+v", f)
		}
	}
	if !DerivedRule("lanes") || !DerivedRule("edge") || DerivedRule("high") || DerivedRule("all") || DerivedRule("") {
		t.Error("DerivedRule")
	}
}

// TestBakeDerivedMadeUp pins a bake with the new rules (and stays the same between two runs); the scatter is gone
// with NoDerived.
func TestBakeDerivedMadeUp(t *testing.T) {
	run := func() []byte {
		m, read := openCentre(t)
		soft := 3.0
		m.Ground.Layers = append(m.Ground.Layers,
			GroundLayer{Texture: TextureRef{RGB: &[3]float64{200, 180, 140}}, Auto: "lanes", Width: 10, Soft: &soft, Noise: 1},
			GroundLayer{Texture: TextureRef{RGB: &[3]float64{70, 60, 50}}, Auto: "edge", Range: &[2]float64{-1, 4},
				Along: "walls", Noise: 1.5},
			GroundLayer{Texture: TextureRef{RGB: &[3]float64{40, 40, 60}}, Auto: "edge", Along: "chasms"})
		r, cm := Check(m, nil)
		if !r.OK {
			t.Fatalf("%+v", r.Findings)
		}
		files, mm, err := cm.GroundFiles(read)
		if err != nil {
			t.Fatal(err)
		}
		var all bytes.Buffer
		for _, f := range files {
			all.WriteString(digest(f.Data))
		}
		all.WriteString(digest(mm))
		return all.Bytes()
	}
	a := run()
	if got := digest(a); got != RingDerivedMadeUpSHA256 {
		t.Errorf("SHA-256 %s, pinned %s", got, RingDerivedMadeUpSHA256)
	}
	if !bytes.Equal(a, run()) {
		t.Error("two runs differ")
	}
	m, _ := madeUpBaked(t)
	m.Scatter = []Scatter{{Nouns: []string{"rock"}, Seed: 7}}
	_, cm := Check(m, nil)
	if len(cm.Scattered(0)) == 0 {
		t.Fatal("no scatter")
	}
	cm.NoDerived = true
	if n := len(cm.Scattered(0)); n != 0 {
		t.Errorf("no derived: %d scattered", n)
	}
}
