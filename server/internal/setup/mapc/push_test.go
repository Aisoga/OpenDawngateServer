package mapc

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

// The push test maps (MAPED5): one straight path between the bases (lanes 1 and 2 share it, 34 m apart), CM_PushA
// with SM_Map_v5's fixed set, CM_PushB the same with MM_Map_v3's (3 Cores, the Middle and Inner tiers), CM_PushC
// CM_PushA's base at about CM_Ring's lane length.
func pushMap(t *testing.T, name string) *Map {
	t.Helper()
	m, err := Load(testmaps.Pkg(name))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// polyDist is p's distance to the polyline ks.
func polyDist(ks []Point, p Point) float64 {
	best := math.Inf(1)
	for i := 0; i+1 < len(ks); i++ {
		a, b := ks[i], ks[i+1]
		dx, dy := b[0]-a[0], b[1]-a[1]
		u := ((p[0]-a[0])*dx + (p[1]-a[1])*dy) / (dx*dx + dy*dy)
		u = math.Max(0, math.Min(1, u))
		best = math.Min(best, math.Hypot(a[0]+u*dx-p[0], a[1]+u*dy-p[1]))
	}
	return best
}

// TestPushMaps: each passes every rule without a warning; its two lanes are one shared path for the host (every
// knot within 60 of the other lane, lanes.h's LanesShareOnePath); CM_PushB differs from CM_PushA only in its base
// set; CM_PushC's lane is CM_Ring's length within 6 %.
func TestPushMaps(t *testing.T) {
	names := []string{"CM_PushA", "CM_PushB", "CM_PushC"}
	maps := map[string]*Map{}
	for _, n := range names {
		m := pushMap(t, n)
		maps[n] = m
		var others []Other
		for _, o := range names {
			if o != n {
				others = append(others, Other{o, "push" + strings.ToLower(o[len(o)-1:])})
			}
		}
		r, cm := Check(m, others)
		if !r.OK || len(r.Findings) != 0 {
			t.Fatalf("%s: %+v", n, r.Findings)
		}
		if len(m.Camps) != 0 || len(m.Lanes) != 2 {
			t.Errorf("%s: %d camps, %d lanes", n, len(m.Camps), len(m.Lanes))
		}
		for _, ln := range m.Lanes {
			other := m.Lanes[0].Knots
			if ln.ID == m.Lanes[0].ID {
				other = m.Lanes[1].Knots
			}
			for _, k := range ln.Knots {
				if d := polyDist(other, k); d > 60 {
					t.Errorf("%s: lane %d's knot %v is %.1f from the other lane: not one shared path", n, ln.ID, k, d)
				}
			}
		}
		t1 := cm.Layout.Teams[0]
		wantCores, wantTiers := 5, 3
		if n == "CM_PushB" {
			wantCores, wantTiers = 3, 2
		}
		if len(t1.Cores) != wantCores || len(cm.Layout.Teams[1].Cores) != wantCores {
			t.Errorf("%s: %d Cores", n, len(t1.Cores))
		}
		for _, id := range cm.Layout.LaneIDs {
			for tm := range 2 {
				if got := len(cm.Layout.Teams[tm].Bindings[id]); got != wantTiers {
					t.Errorf("%s: team %d lane %d: %d Bindings", n, tm+1, id, got)
				}
			}
		}
		t.Logf("%s: %v, lanes %.1f m", n, cm, length(t1.Lanes[1]))
	}
	strip := func(m *Map) string {
		c := *m
		c.Name, c.Display, c.Tag, c.Prefix = "", "", "", ""
		c.Base.Cores, c.Base.Bindings = Cores{Auto: true}, Bindings{Auto: true}
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if strip(maps["CM_PushA"]) != strip(maps["CM_PushB"]) {
		t.Errorf("CM_PushB differs from CM_PushA beyond its Cores and Bindings")
	}
	// CM_PushB's Middle and Inner Bindings stand where "auto" puts CM_PushA's
	for _, ln := range maps["CM_PushB"].Lanes {
		auto := AutoBindings(ln.ID, ln.Knots)[1:]
		for k, p := range maps["CM_PushB"].Base.Bindings.Lanes[string(rune('0'+ln.ID))] {
			if dist(p, auto[k]) > 0.1 {
				t.Errorf("CM_PushB lane %d tier %d at %v, auto %v", ln.ID, k, p, auto[k])
			}
		}
	}
	ringLen := length(ring(t).Lanes[0].Knots)
	if l := length(maps["CM_PushC"].Lanes[0].Knots); math.Abs(l-ringLen)/ringLen > 0.06 {
		t.Errorf("CM_PushC's lane %.1f m, CM_Ring's %.1f", l, ringLen)
	}
}

// TestMMBaseSet: a base with MM_Map_v3's set (three Cores, the Middle and Inner Bindings) passes on CM_Ring when the
// Cores stand at SM's A, D and E (CM_Ring's lanes end by D and E), and the rules refuse 4 Cores, lanes with different
// tiers, two tiers in the wrong order and SM's A, B and C (the lanes then end 28 m from the nearest Core).
func TestMMBaseSet(t *testing.T) {
	mm := func(m *Map) {
		g := m.Base.Guardian
		m.Base.Cores = Cores{}
		for _, o := range []Point{smCoreOffsets[0], smCoreOffsets[3], smCoreOffsets[4]} {
			m.Base.Cores.Points = append(m.Base.Cores.Points, Point{g[0] + o[0], g[1] + o[1]})
		}
		m.Base.Bindings = Bindings{Lanes: map[string][]Point{}}
		for _, ln := range m.Lanes {
			m.Base.Bindings.Lanes[string(rune('0'+ln.ID))] = AutoBindings(ln.ID, ln.Knots)[1:]
		}
	}
	m := ring(t)
	mm(m)
	r, cm := Check(m, nil)
	if !r.OK || len(r.Findings) != 0 {
		t.Fatalf("%+v", r.Findings)
	}
	if len(cm.Layout.Teams[1].Cores) != 3 || len(cm.Layout.Teams[1].Bindings[1]) != 2 {
		t.Errorf("team 2: %v", cm.Layout.Teams[1])
	}
	if s := strings.Join(TierNames(2), ","); s != "Middle,Inner" {
		t.Errorf("TierNames(2) %s", s)
	}
	cases := []struct {
		rule string
		edit func(m *Map)
	}{
		{"fixed.cores", func(m *Map) { m.Base.Cores.Points = m.Base.Cores.Points[:2] }},
		{"fixed.cores", func(m *Map) { m.Base.Cores.Points = append(m.Base.Cores.Points, Point{-150, -170}) }},
		{"fixed.bindings", func(m *Map) {
			m.Base.Bindings.Lanes["1"] = AutoBindings(1, m.Lanes[0].Knots)
		}},
		{"fixed.bindings", func(m *Map) { m.Base.Bindings.Lanes["1"] = m.Base.Bindings.Lanes["1"][:1] }},
		{"fixed.tiers", func(m *Map) {
			b := m.Base.Bindings.Lanes["2"]
			b[0], b[1] = b[1], b[0]
		}},
		{"lanes.end", func(m *Map) {
			g := m.Base.Guardian
			for k, o := range smCoreOffsets[:3] {
				m.Base.Cores.Points[k] = Point{g[0] + o[0], g[1] + o[1]}
			}
		}},
	}
	for _, tc := range cases {
		m := ring(t)
		mm(m)
		tc.edit(m)
		r, _ := Check(m, nil)
		if !has(r, tc.rule, Error) || r.OK {
			t.Errorf("%s: not found in %+v", tc.rule, r.Findings)
		}
	}
}

// TestGamePushB builds CM_PushB's assets from build 854's AssetData: per team and lane a Middle Binding whose child
// is the Inner one, no Outer Binding, the Cores A to C, and each lane-agent spawner's child the enemy's Middle
// Binding of its lane; the markers read back with sound references; no build warning.
func TestGamePushB(t *testing.T) {
	dir := testgame.Dir(t)
	in, err := ReadInputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := pushMap(t, "CM_PushB")
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	as, err := cm.Assets(in.AssetData, madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	checkAssets(t, m, as)
	if len(as.Findings) != 0 {
		t.Errorf("%+v", as.Findings)
	}
	byID := map[uint64]string{}
	count := map[string]int{}
	var mids []uint64
	childOf := map[uint64][]float64{}
	for _, set := range []string{"towers", "nubs", "minion_blue", "minion_red"} {
		for _, mk := range markers(as.Values["CM_PushB_"+set+".Markerset"]) {
			n := text(mk, "nounDef")
			id := uval(mk, "markerId")
			byID[id] = n
			count[n]++
			childOf[id] = vec(mk, "childMarkerIds")
			if strings.HasSuffix(n, "_Middle.Noun") {
				mids = append(mids, id)
			}
		}
	}
	for _, n := range []string{"Binding_Mortal_Middle.Noun", "Binding_Mortal_Inner.Noun", "Binding_Spirit_Middle.Noun",
		"Binding_Spirit_Inner.Noun"} {
		if count[n] != 2 {
			t.Errorf("%s: %d markers, 2 expected", n, count[n])
		}
	}
	if count["Binding_Mortal_Outer.Noun"]+count["Binding_Spirit_Outer.Noun"] != 0 {
		t.Errorf("an Outer Binding on CM_PushB")
	}
	for _, c := range []string{"A", "B", "C"} {
		if count["nub_mortal_v5_"+c+".Noun"] != 1 || count["nub_spirit_v5_"+c+".Noun"] != 1 {
			t.Errorf("Core %s: %v", c, count)
		}
	}
	if count["nub_mortal_v5_D.Noun"]+count["nub_spirit_v5_E.Noun"] != 0 {
		t.Errorf("a Core D or E on CM_PushB")
	}
	for _, id := range mids {
		ch := childOf[id]
		if len(ch) != 1 || !strings.HasSuffix(byID[uint64(ch[0])], "_Inner.Noun") {
			t.Errorf("%s %d: children %v", byID[id], id, ch)
		}
	}
	agents := 0
	for id, n := range byID {
		if strings.HasSuffix(n, "_Strider.Noun") {
			agents++
			ch := childOf[id]
			if len(ch) != 1 || !strings.HasSuffix(byID[uint64(ch[0])], "_Middle.Noun") {
				t.Errorf("%s: child %v", n, ch)
			}
		}
	}
	if agents != 4 {
		t.Errorf("%d lane-agent spawners", agents)
	}
}
