package mapc

import (
	"fmt"
	"math"
	"testing"

	"dawngate/server/internal/testmaps"
)

// TestBotsLive: the bots' derived points of CM_Grove and CM_Marsh are the match host's, as its live logs printed them
// ("bots: parasite rally team N: (x, y), d from the parasite camp ..." and "hunterroute: team N's route ..."):
// CM_Marsh's pillar at (-14, -10) turns team 1's rally by 80 degrees (and 9 units nearer) while team 2's stays
// straight; CM_Ring has no parasite camp and no camps: no bots' points (preview.json without "bots").
func TestBotsLive(t *testing.T) {
	type rally struct{ x, y, d, turn float64 }
	for _, tc := range []struct {
		name  string
		rally [2]rally
		route [2][]string
	}{
		{"CM_Grove", [2]rally{{-46.2, -41.6, 62.0, 0}, {41.4, 46.0, 62.0, 0}},
			[2][]string{{"buff (-100.0, -15.0) L1", "small (-105.0, 60.0) L1", "buff (-15.0, -100.0) L1",
				"small (60.0, -105.0) L1"}, {"buff (100.0, 15.0) L1", "small (105.0, -60.0) L1",
				"buff (15.0, 100.0) L1", "small (-60.0, 105.0) L1"}}},
		{"CM_Marsh", [2]rally{{28.0, -39.5, 53.0, 80}, {41.4, 46.0, 62.0, 0}},
			[2][]string{{"buff (-100.0, -15.0) L1", "small (-108.0, 52.0) L1", "buff (-15.0, -100.0) L1",
				"small (60.0, -105.0) L1"}, {"buff (100.0, 15.0) L1", "small (108.0, -52.0) L1",
				"buff (15.0, 100.0) L1", "small (-60.0, 105.0) L1"}}},
	} {
		m, err := Load(testmaps.Pkg(tc.name))
		if err != nil {
			t.Fatal(err)
		}
		cm, err := Compile(m)
		if err != nil {
			t.Fatal(err)
		}
		b := cm.Bots()
		if len(b.Rally) != 2 {
			t.Fatalf("%s: rally %+v", tc.name, b.Rally)
		}
		for k, r := range b.Rally {
			w := tc.rally[k]
			if math.Abs(r.Pos[0]-w.x) > 0.051 || math.Abs(r.Pos[1]-w.y) > 0.051 || math.Abs(r.Dist-w.d) > 0.051 ||
				math.Abs(r.Turn-w.turn) > 0.01 || r.Quality != RallyOpenLine {
				t.Errorf("%s: team %d's rally %+v, the host's (%.1f, %.1f) at %.1f", tc.name, r.Team, r, w.x, w.y, w.d)
			}
		}
		for k, rt := range b.Routes {
			var got []string
			for _, s := range rt.Steps {
				got = append(got, fmt.Sprintf("%s (%.1f, %.1f) L%d", s.Role, s.Pos[0], s.Pos[1], s.MinLevel))
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.route[k]) {
				t.Errorf("%s: team %d's route %q, the host's %q", tc.name, rt.Team, got, tc.route[k])
			}
		}
		if p := b.Routes[0].Steps[0].Path; p != "camps[0].pos" {
			t.Errorf("%s: the first step's path %q", tc.name, p)
		}
	}
	cm, err := Compile(ring(t))
	if err != nil {
		t.Fatal(err)
	}
	if b := cm.Bots(); b != nil {
		t.Errorf("CM_Ring: %+v", b)
	}
}

// TestRallyRule: the rally rule's own cases on made-up ground: open everywhere gives the straight point at 62; a wall
// on the straight point turns it by the first step that clears it (+10 before -10); no open ground at all gives the
// straight point unchecked.
func TestRallyRule(t *testing.T) {
	all := func(float32, float32) bool { return true }
	x, y, q, turn := rallyPoint(0, 0, -100, 0, all)
	if math.Abs(float64(x)+62) > 1e-4 || math.Abs(float64(y)) > 1e-4 || q != RallyOpenLine || turn != 0 {
		t.Errorf("open: (%v, %v) %s %d", x, y, q, turn)
	}
	// a wall band |y| < 8 beyond 40 units out: the point turns to +2 steps (20 degrees: |y| = 62 sin 20 = 21 > 8 + 5)
	band := func(x, y float32) bool { return !(x < -40 && y > -8 && y < 8) }
	x, y, q, turn = rallyPoint(0, 0, -100, 0, band)
	if turn != 2 || q != RallyOpen && q != RallyOpenLine || !band(x, y) {
		t.Errorf("band: (%v, %v) %s %d", x, y, q, turn)
	}
	none := func(float32, float32) bool { return false }
	x, y, q, _ = rallyPoint(0, 0, -100, 0, none)
	if math.Abs(float64(x)+62) > 1e-4 || q != RallyUnchecked {
		t.Errorf("none: (%v, %v) %s", x, y, q)
	}
	if CampRole("SpawnPoint_v5_Buff_Fish.Noun") != "buff" || CampRole("SpawnPoint_v5_CreepsBoar.Noun") != "big" ||
		CampRole("SpawnPoint_v5_CreepsUgger.Noun") != "small" {
		t.Error("camp roles")
	}
}
