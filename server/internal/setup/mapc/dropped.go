package mapc

import "math"

// The walkable ground cut off from the bases (the check's reach.dropped): which squares of the graphs' grid are
// dropped, and their parts, so a finding can say where they are. A pocket the Player graph drops (an open circle out
// of reach, a shore ring of a chasm too narrow for the graphs' erosion) is easy to miss on a map; the finding's "at"
// is the largest part and "parts" lists every part of team 1's half (team 2's are their mirror).

// DroppedPart is one 8-connected part of the dropped squares.
type DroppedPart struct {
	At     Point   `json:"at"`     // the part's square nearest its centre (on the part, also for a ring)
	Centre Point   `json:"centre"` // the mean of its squares' centres
	R      float64 `json:"r"`      // the farthest square's corner from the centre
	Cells  int     `json:"cells"`  // the line-of-sight map's cells it covers
}

// droppedBlocks marks, per square of the Player graph's grid, walkable ground left out of the graphs: a square off the
// Player graph and outside the holes (the Guardians' platforms, the Bindings and Cores) that is walkable, or island
// ground that no well keeps in the NPC graph.
func (cm *Compiled) droppedBlocks() []bool {
	ly, cell := cm.Layers, cm.Cell
	out := make([]bool, len(cm.Player.On))
	for by := 0; by < cm.Player.H; by++ {
		for bx := 0; bx < cm.Player.W; bx++ {
			if cm.Player.On[by*cm.Player.W+bx] {
				continue
			}
			centre := ly.CellCentre(bx*cell+cell/2, by*cell+cell/2)
			if cm.nearAnyHole(centre, ly.Size/Grid*float64(cell)*2) {
				continue
			}
			if cm.walkableBlock(bx, by) {
				out[by*cm.Player.W+bx] = true
			} else if k := by*cm.NPC.W + bx; cm.maskBlock(cm.NPCMask, bx, by) && !cm.NPC.On[k] {
				out[by*cm.Player.W+bx] = true // island ground no well keeps in the NPC graph
			}
		}
	}
	return out
}

// DroppedParts are the dropped squares' parts whose centre lies on team 1's half (x + y <= 0.5), the largest first.
func (cm *Compiled) DroppedParts() []DroppedPart {
	ly, cell := cm.Layers, cm.Cell
	return halfParts(cm.droppedBlocks(), cm.Player.W, cm.Player.H, func(x, y int) Point {
		return ly.CellCentre(x*cell+cell/2, y*cell+cell/2)
	}, cell*cell, ly.Size/Grid*float64(cell)/2*math.Sqrt2) // narrow.go
}

// dropped is the reach.dropped warning: the whole count, the finding at the largest part of team 1's half, every
// part in Parts.
func (c *checker) dropped(cm *Compiled, st *Stats) {
	parts := cm.DroppedParts()
	msg := "%d walkable cells (%.0f m^2) are cut off from the bases and left out of the graphs"
	args := []any{st.DroppedCells, float64(st.DroppedCells) * st.CellSize * st.CellSize}
	var at *Point
	if len(parts) > 0 {
		at = pt(parts[0].At)
		msg += ": %d part(s) on team 1's half (team 2's are their mirror), the largest %d cells at (%.1f, %.1f)"
		args = append(args, len(parts), parts[0].Cells, parts[0].At[0], parts[0].At[1])
	}
	c.warnf("reach.dropped", "terrain", at, msg, args...)
	c.fs[len(c.fs)-1].Parts = parts
}
