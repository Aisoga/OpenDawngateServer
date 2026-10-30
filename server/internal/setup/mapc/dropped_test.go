package mapc

import (
	"math"
	"testing"
)

// TestDroppedAt: an open pocket out of reach (CM_Grove with an open circle in the wall at (-130, 20)) gives one
// reach.dropped warning at the pocket, with the pocket as its one part of team 1's half (its mirror on team 2's isn't
// listed); a second pocket adds a second part, the larger first.
func TestDroppedAt(t *testing.T) {
	m := grove(t)
	m.Terrain.Shapes = append(m.Terrain.Shapes, Shape{Type: "open", Circle: &Point{-130, 20}, R: 7})
	r, _ := Check(m, nil)
	if !r.OK || len(r.Findings) != 1 || r.Findings[0].Rule != "reach.dropped" {
		t.Fatalf("%+v", r.Findings)
	}
	f := r.Findings[0]
	if f.At == nil || math.Hypot(f.At[0]+130, f.At[1]-20) > 3 || len(f.Parts) != 1 || f.Parts[0].R > 10 {
		t.Fatalf("%+v at %v", f, f.At)
	}
	if f.Parts[0].Cells >= r.Stats.DroppedCells { // the mirror counts too (its cells may differ: the grid's offset)
		t.Errorf("part %d cells, %d dropped on the map (both halves)", f.Parts[0].Cells, r.Stats.DroppedCells)
	}
	m.Terrain.Shapes = append(m.Terrain.Shapes, Shape{Type: "open", Circle: &Point{-145, 60}, R: 9})
	r, _ = Check(m, nil)
	if len(r.Findings) != 1 || len(r.Findings[0].Parts) != 2 {
		t.Fatalf("%+v", r.Findings)
	}
	p := r.Findings[0].Parts
	if math.Hypot(p[0].At[0]+145, p[0].At[1]-60) > 4 || p[0].Cells <= p[1].Cells || *r.Findings[0].At != p[0].At {
		t.Errorf("%+v", p)
	}
}
