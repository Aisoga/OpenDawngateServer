package mapc

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// RingFillSHA256 pins the fills of fillRing (their placements as JSON): the same on every platform.
const RingFillSHA256 = "3f120f0e41918c3afa0c402b3d17fa607bca2384aa318eaae3dcd54a63014141"

// fillRing is CM_Ring with scatters added (each checked: the map must pass).
func fillRing(t *testing.T, ss ...Scatter) *Compiled {
	t.Helper()
	m := ring(t)
	m.Scatter = ss
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	return cm
}

// the test regions: deep in CM_Ring's walls between the lanes, and over the base's open ground and its walls
var (
	wallRegion = pts(-130, -90, -80, -90, -80, -40, -130, -40)
	baseRegion = pts(-135, -135, -85, -135, -85, -85, -135, -85)
)

// TestSplitmixVector pins the scatter's random numbers (the page's estimate has its own SplitMix64 and checks these).
func TestSplitmixVector(t *testing.T) {
	r := &splitmix{s: 7}
	want := []uint64{0x63cbe1e459320dd7, 0x044c3cd7f43c661c, 0xe6984080bab12a02}
	for i, w := range want {
		if got := r.next(); got != w {
			t.Errorf("next %d: %#x, want %#x", i, got, w)
		}
	}
	if got := (&splitmix{s: 1<<40 + 5}).next(); got != 0x486e851c593533ba { // a seed over 32 bits
		t.Errorf("seed 2^40+5: %#x", got)
	}
}

// TestFillWalls: a fill on walls places its props on wall cells only (deep in the wall too), at the ground model's
// height, apart by the spacing (the mirrors too), mirrored; the same for a seed, another for another; at most max.
func TestFillWalls(t *testing.T) {
	s := Scatter{Nouns: []string{"a", "b"}, Seed: 7, Along: "area", On: FillOnWalls, Polygon: wallRegion, Spacing: 5}
	cm := fillRing(t, s)
	got := cm.Scattered(0)
	again := fillRing(t, s).Scattered(0)
	if len(got) < 60 || len(got) != len(again) {
		t.Fatalf("%d and %d props", len(got), len(again))
	}
	high := 0
	for i, p := range got {
		if p != again[i] {
			t.Fatalf("prop %d: %+v, then %+v", i, p, again[i])
		}
		q := Point{p.Pos[0], p.Pos[1]}
		if i%2 == 0 {
			m := got[i+1]
			if p.Team != 1 || m.Team != 2 || m.Pos != [3]float64{-p.Pos[0], -p.Pos[1], p.Pos[2]} || m.Noun != p.Noun {
				t.Errorf("prop %d %+v, its mirror %+v", i, p, m)
			}
			if !inPolygon(q, wallRegion) && dist(q, Point{-105, -65}) > 40 {
				t.Errorf("prop %d at %v: off its region", i, q)
			}
		}
		if k := cm.Layers.KindAt(q); k != Wall {
			t.Errorf("prop %d at %v on kind %d", i, q, k)
		}
		if p.Pos[2] > 4 {
			high++
		}
		if p.Blocks || p.Footprint != nil {
			t.Errorf("prop %d blocks", i)
		}
		for j := 0; j < i; j++ {
			if d := dist(q, Point{got[j].Pos[0], got[j].Pos[1]}); d < s.Spacing {
				t.Fatalf("props %d and %d %.2f m apart", j, i, d)
			}
		}
	}
	if high < len(got)/2 {
		t.Errorf("%d of %d props on the plateau (z > 4)", high, len(got))
	}
	s.Seed = 8
	other := fillRing(t, s).Scattered(0)
	same := 0
	for i := range other {
		if i < len(got) && other[i].Pos == got[i].Pos {
			same++
		}
	}
	if same > len(got)/10 {
		t.Errorf("seed 8: %d of %d props the same", same, len(got))
	}
	s.Max = 5
	if n := len(fillRing(t, s).Scattered(0)); n != 10 {
		t.Errorf("max 5: %d props", n)
	}
	s.Max, s.Density = 0, 0.3
	if n := len(fillRing(t, s).Scattered(0)); !(n > len(got)/6 && n < len(got)/2) {
		t.Errorf("density 0.3: %d props of %d", n, len(got))
	}
}

// TestFillGround: on walkable ground a fill without blocks places only decor; with blocks its rocks stamp their
// footprints Solid (out of both graphs, in the line-of-sight map), never within the keep-out; decor never blocks; the
// walls' scatter of the same map is the same with and without the fill.
func TestFillGround(t *testing.T) {
	plants := Scatter{Nouns: []string{"grassblades01_forest", "rock_forest_small_02"}, Seed: 3, Along: "area",
		On: FillOnGround, Polygon: baseRegion, Spacing: 4}
	walls := Scatter{Nouns: []string{"rock"}, Seed: 7, Spacing: 7}
	base := fillRing(t, walls)
	cm := fillRing(t, walls, plants)
	got := cm.Scattered(1)
	if len(got) < 20 {
		t.Fatalf("%d props", len(got))
	}
	for i, p := range got {
		q := Point{p.Pos[0], p.Pos[1]}
		if !DecorNoun(p.Noun) || p.Blocks {
			t.Errorf("prop %d: %s, blocks %v", i, p.Noun, p.Blocks)
		}
		if k := cm.Layers.KindAt(q); !Walkable(k) {
			t.Errorf("prop %d at %v on kind %d", i, q, k)
		}
		if i, j := cm.Layers.CellOf(q); p.Team == 1 && cm.barred[j*Grid+i] {
			t.Errorf("prop %d at %v in the keep-out", i, q)
		}
		if d := dist(q, cm.Layout.Teams[0].Guardian); p.Team == 1 && d < FillGuardian-1 {
			t.Errorf("prop %d %.1f m from the Guardian", i, d)
		}
	}
	if cm.FillSolid() == nil || countKind(cm.Layers, Solid) != 0 {
		t.Errorf("a fill without blocks stamped %d cells", countKind(cm.Layers, Solid))
	}
	if !samePlaced(base.Scattered(0), cm.Scattered(0)) {
		t.Error("the walls' scatter changed with the fill")
	}

	// with blocks: the rocks stand on the walkable ground too and block it
	plants.Blocks, plants.Footprint = true, 3
	cm = fillRing(t, walls, plants)
	got = cm.Scattered(1)
	rocks, stamped := 0, 0
	for i, p := range got {
		q := Point{p.Pos[0], p.Pos[1]}
		if DecorNoun(p.Noun) {
			if p.Blocks || p.Footprint != nil {
				t.Errorf("prop %d: decor blocks", i)
			}
			continue
		}
		rocks++
		if !p.Blocks || p.Footprint == nil || math.Abs(p.Footprint.Axes[0]-1.5*p.Scale) > 1e-9 {
			t.Fatalf("prop %d: %+v", i, p)
		}
		if k := cm.Layers.KindAt(q); k != Solid {
			t.Errorf("rock %d at %v on kind %d", i, q, k)
		}
	}
	for k, on := range cm.FillSolid() {
		if on {
			stamped++
			if cm.barred[k] || cm.Layers.Kind[k] != Solid || cm.Mask[k] || cm.NPCMask[k] || cm.Layers.LoS().Pix[k] == 0 {
				t.Fatalf("stamped cell %d: barred %v, kind %d", k, cm.barred[k], cm.Layers.Kind[k])
			}
			i, j := k%Grid, k/Grid
			c := cm.Layers.CellCentre(i, j)
			mi, mj := cm.Layers.CellOf(Mirror(c))
			if !cm.FillSolid()[mj*Grid+mi] {
				t.Fatalf("stamped cell %v without its mirror", c)
			}
		}
	}
	if rocks < 5 || stamped < rocks*2 || countKind(cm.Layers, Solid) != stamped {
		t.Errorf("%d rocks, %d stamped cells, %d Solid", rocks, stamped, countKind(cm.Layers, Solid))
	}
	if !samePlaced(base.Scattered(0), cm.Scattered(0)) {
		t.Error("the walls' scatter changed with the blocking fill")
	}
	// hidden derived art: no props, the footprints stay
	cm.NoDerived = true
	if len(cm.Scattered(1)) != 0 || countKind(cm.Layers, Solid) != stamped {
		t.Error("NoDerived")
	}
}

// TestFillKeepOut: the keep-out covers the lanes' corridors, the Guardian's hole, the wells' rings and the spawns on
// walkable ground; walls stay free; the level's edge is barred.
func TestFillKeepOut(t *testing.T) {
	cm := fillRing(t)
	barred, err := cm.FillBarred()
	if err != nil {
		t.Fatal(err)
	}
	at := func(p Point) bool { i, j := cm.Layers.CellOf(p); return barred[j*Grid+i] }
	for _, p := range []Point{{-170, -100}, {-100, -170}, {170, 100}, {-142, -142}, {-130, -142}, {-60, -130},
		{-60, -110}, {-188.9, -183.9}, {-219, -150}} {
		if !at(p) {
			t.Errorf("%v not barred", p)
		}
	}
	for _, p := range []Point{{-105, -65}, {-130, -100}, {-150, -95}} {
		if at(p) {
			t.Errorf("%v barred (kind %d)", p, cm.Layers.KindAt(p))
		}
	}
}

// TestFillRules breaks each fill rule once.
func TestFillRules(t *testing.T) {
	ok := Scatter{Nouns: []string{"grassblades01_forest"}, Along: "area", Polygon: baseRegion}
	with := func(f func(s *Scatter)) func(m *Map) {
		return func(m *Map) { s := ok; f(&s); m.Scatter = []Scatter{s} }
	}
	cases := []struct {
		rule, sev string
		edit      func(m *Map)
	}{
		{"scatter.along", Error, with(func(s *Scatter) { s.Along = "fill" })},
		{"scatter.on", Error, with(func(s *Scatter) { s.On = "lanes" })},
		{"scatter.footprint", Error, with(func(s *Scatter) { s.Blocks, s.Footprint = true, 25 })},
		{"scatter.footprint", Error, with(func(s *Scatter) { s.Blocks, s.Footprint = true, -1 })},
		{"scatter.footprint", Warning, with(func(s *Scatter) { s.Footprint = 3 })},
		{"scatter.density", Error, with(func(s *Scatter) { s.Density = 1.5 })},
		{"scatter.density", Error, with(func(s *Scatter) { s.Density = -0.1 })},
		{"scatter.inset", Warning, with(func(s *Scatter) { s.Inset = 2 })},
		{"scatter.decor", Warning, with(func(s *Scatter) { s.On, s.Nouns = FillOnGround, []string{"rock"} })},
		{"scatter.on", Warning, with(func(s *Scatter) { s.Along, s.On = "", FillOnWalls })},
		{"scatter.blocks", Warning, with(func(s *Scatter) { s.Along, s.Blocks = "walls", true })},
		{"scatter.density", Warning, with(func(s *Scatter) { s.Along, s.Density = "", 0.5 })},
		{"scatter.empty", Warning, with(func(s *Scatter) { s.On, s.Polygon = FillOnGround, wallRegion })},
		{"scatter.empty", Warning, with(func(s *Scatter) { s.Along, s.Polygon = "", pts(-130, -140, -120, -140, -120, -130) })},
	}
	for _, tc := range cases {
		m := ring(t)
		tc.edit(m)
		r, _ := Check(m, nil)
		if !has(r, tc.rule, tc.sev) {
			t.Errorf("%s (%s): not found in %+v (%+v)", tc.rule, tc.sev, r.Findings, m.Scatter)
		}
	}
	// the good ones pass without a finding of their own
	for _, s := range []Scatter{ok, {Nouns: []string{"rock"}, Along: "area", On: FillOnOffGround, Polygon: wallRegion,
		Density: 0.5}, {Nouns: []string{"rock"}, Along: "area", Blocks: true, Footprint: 4, Polygon: baseRegion}} {
		m := ring(t)
		m.Scatter = []Scatter{s}
		r, _ := Check(m, nil)
		for _, f := range r.Findings {
			if strings.HasPrefix(f.Rule, "scatter.") {
				t.Errorf("%+v: %+v", s, f)
			}
		}
	}
}

// TestFillFormat: the members read and written back; an older reading (an unknown member) is refused.
func TestFillFormat(t *testing.T) {
	m := ring(t)
	m.Scatter = []Scatter{{Nouns: []string{"rock"}, Seed: 4, Along: "area", On: FillOnGround, Blocks: true,
		Footprint: 3, Density: 0.5, Polygon: baseRegion}}
	b, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"along": "area", "polygon"`) && !strings.Contains(string(b), `"on": "ground"`) {
		t.Errorf("written: %s", b)
	}
	back, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if s := back.Scatter[0]; s.On != FillOnGround || !s.Blocks || s.Footprint != 3 || s.Density != 0.5 || !s.IsFill() {
		t.Errorf("read back %+v", s)
	}
	if _, err := Parse([]byte(strings.Replace(string(b), `"density"`, `"thickness"`, 1))); err == nil {
		t.Error("an unknown member read")
	}
}

// TestFillPinned pins a fill's placements (both kinds of ground, blocks, density) as JSON.
func TestFillPinned(t *testing.T) {
	cm := fillRing(t, Scatter{Nouns: []string{"grassblades01_forest", "rock_forest_small_02"}, Seed: 11,
		Along: "area", Blocks: true, Polygon: pts(-135, -135, -80, -135, -80, -40, -135, -40), Spacing: 4,
		Density: 0.8, Scale: [2]float64{0.6, 1.4}})
	b, err := json.Marshal(cm.Scattered(0))
	if err != nil {
		t.Fatal(err)
	}
	if got := digest(b); got != RingFillSHA256 {
		t.Errorf("SHA-256 %s, pinned %s (%d props)", got, RingFillSHA256, len(cm.Scattered(0)))
	}
}

func countKind(ly *Layers, k byte) int {
	n := 0
	for _, v := range ly.Kind {
		if v == k {
			n++
		}
	}
	return n
}

func samePlaced(a, b []Placed) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Pos != b[i].Pos || a[i].Noun != b[i].Noun || a[i].Rot != b[i].Rot || a[i].Scale != b[i].Scale {
			return false
		}
	}
	return true
}
