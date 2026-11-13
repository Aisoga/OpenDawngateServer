package mapc

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"dawngate/server/internal/testmaps"
)

func marsh(t *testing.T) *Map {
	t.Helper()
	m, err := Load(testmaps.Pkg("CM_Marsh"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// spur adds a dead-end open path from a to b of width w, with a small camp three quarters along it (in the wall
// between CM_Marsh's jungle path at y -100 and lane 2, or off lane 2 to the map's south edge), and returns the camp.
func spur(m *Map, a, b Point, w float64) Point {
	m.Terrain.Shapes = append(m.Terrain.Shapes, Shape{Type: "open", Path: []Point{a, b}, Width: w})
	cp := Point{(a[0] + 3*b[0]) / 4, (a[1] + 3*b[1]) / 4}
	m.Camps = append(m.Camps, Camp{Kind: CampCamp, Noun: "SpawnPoint_v5_CreepsUgger.Noun", Pos: cp})
	return cp
}

func rules(r *Report) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Rule)
	}
	return out
}

// TestNarrowPath: a 6-m dead-end path holds no square of CM_Marsh's 3.5-m grid, so the check reports it (reach.narrow,
// one part on team 1's half, at the path) and says why the camp on it is off the NPC graph, naming the path and the
// width it needs (8 m along the axes, 11 m at an angle); at 9 m both are gone. The player's map of 2026-11-12 had 18
// camps on such paths, and only the camps were reported.
func TestNarrowPath(t *testing.T) {
	m := marsh(t)
	cp := spur(m, Point{20, -104}, Point{20, -145}, 6)
	k := len(m.Terrain.Shapes) - 1
	r, _ := Check(m, nil)
	if r.OK || len(r.Findings) != 2 {
		t.Fatalf("%v: %+v", rules(r), r.Findings)
	}
	npc, narrow := r.Findings[0], r.Findings[1]
	want := fmt.Sprintf("off the NPC graph: terrain.shapes[%d], a path 6 m wide, is too narrow there; it needs 8 m", k)
	if npc.Rule != "reach.npc" || !strings.HasSuffix(npc.Message, want) || npc.Shape != fmt.Sprintf("terrain.shapes[%d]", k) {
		t.Errorf("%+v", npc)
	}
	if !strings.Contains(narrow.Message, "widen them to 8 m (11 m at an angle)") {
		t.Errorf("%+v", narrow)
	}
	if narrow.Rule != "reach.narrow" || narrow.Severity != Warning || narrow.At == nil || len(narrow.Parts) != 1 {
		t.Fatalf("%+v", narrow)
	}
	if p := narrow.Parts[0]; math.Abs(p.At[0]-20) > 4 || p.At[1] > -110 || p.At[1] < -150 || p.Cells < MinNarrowCells {
		t.Errorf("part %+v, the spur runs (20, -104) to (20, -145), the camp at %v", p, cp)
	}
	if r.Stats.NarrowCells < narrow.Parts[0].Cells || len(r.Stats.PathWidth) != 2 || r.Stats.PathWidth[0] != 8 {
		t.Errorf("stats %+v", r.Stats)
	}

	m = marsh(t)
	spur(m, Point{-108, 52}, Point{-140, 20}, 6) // at 45 degrees
	r, _ = Check(m, nil)
	want = fmt.Sprintf("terrain.shapes[%d], a path 6 m wide, is too narrow there; at its angle it needs 11 m", k)
	if len(r.Findings) != 2 || !strings.HasSuffix(r.Findings[0].Message, want) {
		t.Errorf("at an angle: %v %+v", rules(r), r.Findings)
	}

	m = marsh(t)
	spur(m, Point{20, -104}, Point{20, -145}, 9)
	if r, _ = Check(m, nil); !r.OK || len(r.Findings) != 0 || r.Stats.NarrowCells != 0 {
		t.Errorf("9 m: %v %+v", rules(r), r.Findings)
	}
}

// TestOffGraphCut: a camp in an open pocket out of reach (squares fit there, but nothing joins them to the bases) is
// off the NPC graph because its ground is cut off, which the message says (beside reach.dropped's warning).
func TestOffGraphCut(t *testing.T) {
	m := grove(t)
	m.Terrain.Shapes = append(m.Terrain.Shapes, Shape{Type: "open", Circle: &Point{-130, 20}, R: 9})
	m.Camps = append(m.Camps, Camp{Kind: CampCamp, Noun: "SpawnPoint_v5_CreepsUgger.Noun", Pos: Point{-130, 20}})
	r, _ := Check(m, nil)
	var npc *Finding
	for i, f := range r.Findings {
		if f.Rule == "reach.npc" {
			npc = &r.Findings[i]
		}
	}
	if npc == nil || !strings.Contains(npc.Message, "cut off from the bases") || r.Stats.NarrowCells != 0 {
		t.Fatalf("%v: %+v", rules(r), r.Findings)
	}
}

// TestPathWidth: the widths for the messages and the page, at the three square sizes of CM_Marsh's cells (0.88 m).
func TestPathWidth(t *testing.T) {
	cs := 450.0 / Grid
	for cell, want := range map[int][2]float64{2: {4.5, 5.5}, 3: {6.5, 8}, 4: {8, 11}} {
		if s, d := PathWidth(cell, cs); s != want[0] || d != want[1] {
			t.Errorf("cell %d: %g, %g; want %v", cell, s, d, want)
		}
	}
}

// spurHolds tells whether a dead-end path off CM_Marsh's lane 2 toward the map's south edge, at angle theta (degrees
// from straight south, turning east) and shifted east by off metres, passes the check's graph rules at squares of
// cell cells: no narrow ground, nothing cut off, and a camp 3 m before its end on the NPC graph.
func spurHolds(t *testing.T, cell int, theta, w, off float64) bool {
	m := marsh(t)
	m.Graph = &Graph{Cell: cell}
	a := Point{-80 + off, -172}
	b := Point{a[0] + 38*math.Tan(theta*math.Pi/180), -210}
	m.Terrain.Shapes = append(m.Terrain.Shapes, Shape{Type: "open", Path: []Point{a, b}, Width: w})
	l := dist(a, b)
	cp := Point{b[0] - (b[0]-a[0])*3/l, b[1] - (b[1]-a[1])*3/l}
	cm, err := Compile(m)
	if err != nil {
		t.Fatal(err)
	}
	st := cm.stats()
	bx, by := cm.BlockAt(cp)
	return st.NarrowCells == 0 && st.DroppedCells == 0 && cm.NPC.Near(bx, by, 1) >= 0
}

// TestPathWidths: open ground as wide as PathWidth holds the squares wherever it lies (offsets across a square;
// straight, and at 30 and 45 degrees, the worst angles of angledCells' measurement), and a path of 6 m at the usual
// 4-cell squares holds none (it is reported).
func TestPathWidths(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles CM_Marsh 26 times")
	}
	cs := 450.0 / Grid
	for _, cell := range []int{4, 3} {
		straight, angled := PathWidth(cell, cs)
		for k := 0; k < 4; k++ {
			off := float64(k) * float64(cell) * cs / 4
			if !spurHolds(t, cell, 0, straight, off) {
				t.Errorf("cell %d: a straight path of %g m at offset %.2f m doesn't hold", cell, straight, off)
			}
			for _, theta := range []float64{30, 45} {
				if !spurHolds(t, cell, theta, angled, off) {
					t.Errorf("cell %d: a path of %g m at %g degrees, offset %.2f m, doesn't hold", cell, angled, theta,
						off)
				}
			}
		}
	}
	for _, theta := range []float64{0, 45} {
		if spurHolds(t, 4, theta, 6, 0) {
			t.Errorf("a 6-m path at %g degrees holds the 3.5-m squares", theta)
		}
	}
}
