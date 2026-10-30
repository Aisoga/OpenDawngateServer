package mapc

import (
	"math"
	"sort"
)

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
// Player graph and outside the Guardians' holes that is walkable, or island ground that no well keeps in the NPC graph.
func (cm *Compiled) droppedBlocks() []bool {
	ly, cell := cm.Layers, cm.Cell
	out := make([]bool, len(cm.Player.On))
	for by := 0; by < cm.Player.H; by++ {
		for bx := 0; bx < cm.Player.W; bx++ {
			if cm.Player.On[by*cm.Player.W+bx] {
				continue
			}
			centre := ly.CellCentre(bx*cell+cell/2, by*cell+cell/2)
			inHole := false
			for _, g := range cm.Guardians() {
				if math.Hypot(centre[0]-g.X, centre[1]-g.Y) < HoleRadius+ly.Size/Grid*float64(cell)*2 {
					inHole = true
				}
			}
			if inHole {
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
	drop := cm.droppedBlocks()
	w, h, cell := cm.Player.W, cm.Player.H, cm.Cell
	ly := cm.Layers
	half := ly.Size / Grid * float64(cell) / 2 * math.Sqrt2
	seen := make([]bool, len(drop))
	var parts []DroppedPart
	for k := range drop {
		if !drop[k] || seen[k] {
			continue
		}
		var members []Point
		st := []int{k}
		seen[k] = true
		for len(st) > 0 {
			q := st[len(st)-1]
			st = st[:len(st)-1]
			x, y := q%w, q/w
			members = append(members, ly.CellCentre(x*cell+cell/2, y*cell+cell/2))
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := x+dx, y+dy
					if nx < 0 || ny < 0 || nx >= w || ny >= h {
						continue
					}
					if i := ny*w + nx; drop[i] && !seen[i] {
						seen[i] = true
						st = append(st, i)
					}
				}
			}
		}
		var c Point
		for _, p := range members {
			c[0] += p[0] / float64(len(members))
			c[1] += p[1] / float64(len(members))
		}
		if c[0]+c[1] > 0.5 {
			continue
		}
		p := DroppedPart{Centre: c, Cells: len(members) * cell * cell}
		bestD := math.Inf(1)
		for _, m := range members {
			d := math.Hypot(m[0]-c[0], m[1]-c[1])
			if d < bestD {
				p.At, bestD = m, d
			}
			p.R = math.Max(p.R, d+half)
		}
		p.At = Point{math.Round(p.At[0]*10) / 10, math.Round(p.At[1]*10) / 10}
		p.Centre = Point{math.Round(c[0]*10) / 10, math.Round(c[1]*10) / 10}
		p.R = math.Round(p.R*10) / 10
		parts = append(parts, p)
	}
	sort.SliceStable(parts, func(a, b int) bool { return parts[a].Cells > parts[b].Cells })
	return parts
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
