package mapc

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapmm/navgraph"
)

// TestStructureHoles: every Binding and Core of both teams stands in a hole of StructureRadius in both graphs (no
// square reaches into it, so no unit walks through one), the bases' part of the Player graph comes to its rim, the
// check finds nothing new, and the Guardians keep their platforms and islands.
func TestStructureHoles(t *testing.T) {
	cm, err := Compile(ring(t))
	if err != nil {
		t.Fatal(err)
	}
	st := cm.Structures()
	if len(st) != 22 { // CM_Ring: 2 x 5 Cores, 2 x 2 lanes x 3 Bindings
		t.Fatalf("%d structures, 22", len(st))
	}
	side := cm.Layers.Size / Grid * float64(cm.Cell)
	home := cm.Player.Components()[func() int {
		bx, by := cm.BlockAt(cm.Layout.Teams[0].Spawns[0])
		return cm.Player.Near(bx, by, 1)
	}()]
	plComp := cm.Player.Components()
	for _, s := range st {
		nearest := math.Inf(1)
		for _, g := range []*Blocks{&cm.Player, &cm.NPC} {
			for by := 0; by < g.H; by++ {
				for bx := 0; bx < g.W; bx++ {
					if !g.On[by*g.W+bx] {
						continue
					}
					r := navgraph.BlockRect(bx, by, cm.Cell, Grid, Grid, cm.Map.Size, cm.Map.Size)
					dx := math.Max(math.Max(r[0]-s.X, 0), s.X-r[2])
					dy := math.Max(math.Max(r[1]-s.Y, 0), s.Y-r[3])
					d := math.Hypot(dx, dy)
					if d < StructureRadius {
						t.Fatalf("a square %.2f m from the structure at (%.1f, %.1f)", d, s.X, s.Y)
					}
					if g == &cm.Player && plComp[by*g.W+bx] == home {
						nearest = math.Min(nearest, d)
					}
				}
			}
		}
		if nearest > StructureRadius+side {
			t.Errorf("the bases' part of the Player graph comes %.2f m to the structure at (%.1f, %.1f)", nearest,
				s.X, s.Y)
		}
	}
	r, _ := Check(ring(t), nil)
	for _, f := range r.Findings {
		if f.Rule == "reach.player" || f.Rule == "reach.dropped" || f.Rule == "reach.narrow" {
			t.Errorf("%s: %s", f.Rule, f.Message)
		}
	}
	if !r.OK {
		t.Errorf("CM_Ring fails the check: %+v", r.Findings)
	}
	if len(cm.holes()) != len(st)+2 {
		t.Errorf("%d holes, %d", len(cm.holes()), len(st)+2)
	}
}

// TestBindingOnLaneLine: a Binding put on its lane's line cuts the leg out of the NPC graph (its hole), and
// lanes.graph names that Binding instead of calling the ground cut off from the bases.
func TestBindingOnLaneLine(t *testing.T) {
	m := ring(t)
	ly := Resolve(m)
	m.Base.Bindings = Bindings{Lanes: map[string][]Point{}}
	for _, id := range ly.LaneIDs {
		m.Base.Bindings.Lanes[strconv.Itoa(id)] = append([]Point(nil), ly.Teams[0].Bindings[id]...)
	}
	knots := m.Lanes[0].Knots
	s, _ := project(knots, ly.Teams[0].Bindings[m.Lanes[0].ID][1])
	on, _ := along(knots, s)
	m.Base.Bindings.Lanes[strconv.Itoa(m.Lanes[0].ID)][1] = on
	r, _ := Check(m, nil)
	var msgs []string
	for _, f := range r.Findings {
		if f.Rule == "lanes.graph" {
			msgs = append(msgs, f.Message)
		}
	}
	want := fmt.Sprintf("team 1's lane %d Middle Binding stands at (%.1f, %.1f)", m.Lanes[0].ID, on[0], on[1])
	if len(msgs) == 0 || !strings.Contains(strings.Join(msgs, "\n"), want) {
		t.Fatalf("lanes.graph %q, want one naming %q", msgs, want)
	}
	for _, msg := range msgs {
		if strings.Contains(msg, "cut off") {
			t.Errorf("lanes.graph still says cut off: %s", msg)
		}
	}
}
