package mapc

import (
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapmm/navgraph"
)

// islandRing is CM_Ring with its well on an island (ground only NPCs walk): the well's clearing and path replaced by
// an island circle in the wall between the base, the lanes and the centre, the well moved onto it.
func islandRing(t *testing.T) *Map {
	t.Helper()
	m := ring(t)
	c := Point{-70, -70}
	m.Wells = []Well{{Pos: c}}
	var shapes []Shape
	for k, s := range m.Terrain.Shapes {
		switch k {
		case 4: // the well's clearing
			shapes = append(shapes, Shape{Type: "island", Circle: &c, R: 24})
		case 5: // its path
		default:
			shapes = append(shapes, s)
		}
	}
	m.Terrain.Shapes = shapes
	return m
}

// islandMarkers are the assets' island markers: their places and whether they allow players.
func islandMarkers(as *MapAssets) (at []Point, players []bool) {
	for _, n := range as.Names {
		if !strings.HasSuffix(n, ".Markerset") {
			continue
		}
		for _, mk := range markers(as.Values[n]) {
			d := sub(mk, "navmeshMarkerData")
			if d == nil {
				continue
			}
			p := vec(mk, "pos")
			pl, _ := d.Get("allowPlayersHere")
			at = append(at, Point{p[0], p[1]})
			players = append(players, pl == true)
		}
	}
	return at, players
}

// TestIslandWell: a well on an island passes the check; the island is in the NPC graph and out of the Player graph,
// both graphs as the archive gets them; the well gets an NPC-only island marker (the match host's MAPMINES rule) and
// the minimap's island colour.
func TestIslandWell(t *testing.T) {
	m := islandRing(t)
	r, cm := Check(m, nil)
	if !r.OK || len(r.Findings) != 0 {
		t.Fatalf("%+v", r.Findings)
	}
	if a, b := cm.IslandWells(0), cm.IslandWells(1); len(a) != 1 || len(b) != 1 || a[0] != 0 || b[0] != 0 {
		t.Fatalf("island wells %v, %v", a, b)
	}
	for tm := range 2 {
		w := cm.Layout.Teams[tm].Wells[0]
		bx, by := cm.BlockAt(w)
		if cm.NPC.Near(bx, by, 0) < 0 || cm.Player.Near(bx, by, 0) >= 0 {
			t.Errorf("team %d's well: on the NPC graph %v, on the Player graph %v", tm+1,
				cm.NPC.Near(bx, by, 0) >= 0, cm.Player.Near(bx, by, 0) >= 0)
		}
	}
	// the Player graph, the Guardians and the two wells
	if r.Stats.IslandCells == 0 || r.Stats.DroppedCells != 0 || len(cm.NPCKeep()) != 6 {
		t.Errorf("stats %+v, NPC keep %d", *r.Stats, len(cm.NPCKeep()))
	}
	// the graphs as built: the island's squares only in the NPC graph
	ref, _ := Check(ring(t), nil)
	player, npc, err := navgraph.BuildTwo(cm.Layers.Walk(), cm.Layers.NPCWalk(), cm.NavSpec(), madeUpTemplate(1),
		madeUpTemplate(2))
	if err != nil {
		t.Fatal(err)
	}
	areas := func(b []byte) int {
		img, err := navgraph.Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		return len(img.Graphs[0].Areas)
	}
	if p, n := areas(player), areas(npc); p != cm.Player.Areas || n != cm.NPC.Areas || n-p < 100 {
		t.Errorf("graphs: Player %d (%d), NPC %d (%d)", p, cm.Player.Areas, n, cm.NPC.Areas)
	}
	t.Logf("%v (CM_Ring: %v); stats %+v", cm, ref.Stats, *r.Stats)
	// the markers: an NPC-only one on each well, beside the Guardians' and the bases'
	as, err := cm.Assets(madeUpAssetData(t), madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	checkAssets(t, m, as)
	at, players := islandMarkers(as)
	if len(at) != 6 {
		t.Errorf("%d island markers, 6 expected", len(at))
	}
	for tm := range 2 {
		w, found := cm.Layout.Teams[tm].Wells[0], false
		for k, p := range at {
			if dist(p, w) < 0.01 && !players[k] {
				found = true
			}
		}
		if !found {
			t.Errorf("team %d's well has no NPC-only island marker: %v %v", tm+1, at, players)
		}
	}
	if spec := cm.MinimapSpec(); len(spec.Islands) != 2 {
		t.Errorf("minimap islands %v", spec.Islands)
	}
	// CM_Ring's own well stays in reach: no island marker, no island
	_, rcm := Check(ring(t), nil)
	if len(rcm.IslandWells(0)) != 0 || len(rcm.NPCKeep()) != 4 || len(rcm.MinimapSpec().Islands) != 0 {
		t.Errorf("CM_Ring: island wells %v", rcm.IslandWells(0))
	}
}

// TestIslandRules: the rules an island is checked by.
func TestIslandRules(t *testing.T) {
	for _, tc := range []struct {
		name, rule, sev string
		edit            func(m *Map)
	}{
		{"the Player graph inside the capture ring", "reach.island", Error, func(m *Map) {
			// CM_Ring's well clearing as the island, its path open up to 12 m from the well
			m.Terrain.Shapes[4].Type = "island"
			m.Terrain.Shapes[5].Path = pts(-60, -142, -60, -172)
		}},
		{"the Player graph within the workers' reach", "reach.island", Warning, func(m *Map) {
			m.Terrain.Shapes[4].Type = "island"
			m.Terrain.Shapes = append(m.Terrain.Shapes[:5], m.Terrain.Shapes[6:]...)
		}},
		{"an island lane", "terrain.shape", Error, func(m *Map) { m.add(Shape{Type: "island", Lane: 1}) }},
		{"the Locus on an island", "fixed.open", Error, func(m *Map) {
			c := m.Base.Locus
			m.add(Shape{Type: "island", Circle: &c, R: 3})
		}},
		{"an island no well keeps", "reach.dropped", Warning, func(m *Map) {
			c := Point{-100, -40}
			m.add(Shape{Type: "island", Circle: &c, R: 10})
		}},
		{"an unknown type", "terrain.shape", Error, func(m *Map) { m.Terrain.Shapes[4].Type = "isle" }},
	} {
		m := islandRing(t)
		if strings.HasPrefix(tc.name, "the Player graph") {
			m = ring(t)
		}
		tc.edit(m)
		r, _ := Check(m, nil)
		if !has(r, tc.rule, tc.sev) {
			t.Errorf("%s: no %s %s in %+v", tc.name, tc.rule, tc.sev, r.Findings)
		}
		if tc.sev == Warning && !r.OK {
			t.Errorf("%s: %+v", tc.name, r.Findings)
		}
	}
	// terrain.default can't be island
	m := islandRing(t)
	m.Terrain.Default = "island"
	if r, _ := Check(m, nil); !has(r, "terrain.default", Error) {
		t.Errorf("default island: %+v", r.Findings)
	}
}
