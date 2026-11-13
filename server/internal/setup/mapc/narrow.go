package mapc

import (
	"fmt"
	"math"
	"sort"

	"dawngate/server/internal/setup/mapmm/navgraph"
)

// Walkable ground too narrow for the graphs (EDNARROW, the check's reach.narrow). A graph square needs all its
// Cell x Cell cells in the walkable mask, which Erode shrinks by a cell along every edge, and the squares sit on a
// fixed grid: a path narrower than about two squares holds none, so nobody walks it in game. Without this rule only
// the objects on such ground said so (reach.npc, lanes.graph); reach.dropped counts only squares that fit and are cut
// off, and a path too narrow for any square has none. Found on a player's map of 2026-11-12: 18 camps on paths of
// 5-6 m, at CM_Marsh's 3.5-m squares.

// MinNarrowCells is the smallest part of narrow ground the check reports: about two squares of 4 cells. A rim along a
// wall is never narrow (it lies within Cell + Erode cells of a square), but the tip of a sharp wedge of open ground
// may be; parts this small are left out.
const MinNarrowCells = 32

// angledCells is how many cells across a path at an angle to the map's axes must be to hold a 4-connected run of
// squares wherever it lies, per square size. Measured on CM_Marsh (EDNARROW, 2026-11-12: a dead-end path off lane 2
// with a camp near its end, eight offsets across a square, widths in steps of 0.25 m, angles 0, 15, 30, 45 degrees):
// every offset passes from 5.97 cells (squares of 2 cells), 8.53 (3) and 11.38 (4), the worst angle 30 degrees (45:
// 5.69, 8.53, 11.09; 15: 5.69, 8.25, 10.52); the table rounds up to whole cells. A path along an axis needs 2*Cell+1
// cells (after the erosion's cell on each side, 2*Cell-1 cells hold an aligned run of Cell), which the sweep matched:
// 5.12, 6.83, 9.10. TestPathWidths keeps both.
var angledCells = map[int]float64{2: 6, 3: 9, 4: 12}

// PathWidth is how wide open ground must be, in metres, to hold the graphs' squares of cell cells wherever it lies:
// across a path along the map's axes (north-south or east-west) and across one at an angle to them, each rounded up
// to half a metre.
func PathWidth(cell int, cellSize float64) (straight, angled float64) {
	up := func(v float64) float64 { return math.Ceil(v*2-1e-9) / 2 }
	a, ok := angledCells[cell]
	if !ok { // not a square size the compiler picks or allows (MinCell to MaxCell): a rule of thumb, unmeasured
		a = 3 * float64(cell)
	}
	return up(float64(2*cell+1) * cellSize), up(a * cellSize)
}

// PathWidth is the compiled map's PathWidth.
func (cm *Compiled) PathWidth() (straight, angled float64) {
	return PathWidth(cm.Cell, cm.Layers.Size/Grid)
}

// fitBlocks are the squares that fit in the Player graph's mask (npc false) or the NPC graph's, before the keep
// filter drops the parts that hold no keep point: KeptBlocks' grid, Grid/Cell squares a side.
func (cm *Compiled) fitBlocks(npc bool) []bool {
	i, mask := 0, cm.Mask
	if npc {
		i, mask = 1, cm.NPCMask
	}
	if cm.fit[i] == nil {
		cm.fit[i], _, _ = navgraph.KeptBlocks(mask, Grid, Grid, cm.Map.Size, cm.Map.Size, cm.Cell, nil, cm.holes())
	}
	return cm.fit[i]
}

// nearHole reports whether world point p lies by a hole (a Guardian's platform, a Binding, a Core), where no square
// may reach (droppedBlocks' margin).
func (cm *Compiled) nearHole(p Point) bool {
	return cm.nearAnyHole(p, cm.Layers.Size/Grid*float64(cm.Cell)*2)
}

// narrowAll marks every walkable cell more than Cell + Erode cells (8-neighbour steps) from the nearest cell a fitting
// square covers: open ground against the Player graph's squares, island ground against the NPC graph's. The Guardians'
// platforms are left out.
func (cm *Compiled) narrowAll() []bool {
	ly, cell := cm.Layers, cm.Cell
	out := make([]bool, Grid*Grid)
	limit := int32(cell + Erode)
	bw := Grid / cell
	dist := make([]int32, Grid*Grid)
	queue := make([]int32, 0, Grid*Grid)
	for g, kind := range []byte{Open, Island} {
		fit := cm.fitBlocks(g == 1)
		queue = queue[:0]
		for k := range dist {
			dist[k] = math.MaxInt32
		}
		for b, on := range fit {
			if !on {
				continue
			}
			bx, by := b%bw, b/bw
			for dj := 0; dj < cell; dj++ {
				for di := 0; di < cell; di++ {
					k := (by*cell+dj)*Grid + bx*cell + di
					dist[k] = 0
					queue = append(queue, int32(k))
				}
			}
		}
		for q := 0; q < len(queue); q++ {
			k := int(queue[q])
			i, j := k%Grid, k/Grid
			if dist[k] > limit {
				break // the queue runs in distance order: nothing nearer is left
			}
			for dj := -1; dj <= 1; dj++ {
				for di := -1; di <= 1; di++ {
					ii, jj := i+di, j+dj
					if ii < 0 || jj < 0 || ii >= Grid || jj >= Grid {
						continue
					}
					if n := jj*Grid + ii; dist[n] == math.MaxInt32 {
						dist[n] = dist[k] + 1
						queue = append(queue, int32(n))
					}
				}
			}
		}
		for k, v := range ly.Kind {
			if v == kind && dist[k] > limit && !cm.nearHole(ly.CellCentre(k%Grid, k/Grid)) {
				out[k] = true
			}
		}
	}
	return out
}

// narrowCells are narrowAll's cells in parts of MinNarrowCells or more: the ground reach.narrow reports and the graph
// pictures paint red.
func (cm *Compiled) narrowCells() []bool {
	if cm.narrow == nil {
		all := cm.narrowAll()
		cm.narrow = make([]bool, len(all))
		for _, members := range components(all, Grid, Grid) {
			if len(members) < MinNarrowCells {
				continue
			}
			for _, k := range members {
				cm.narrow[k] = true
			}
		}
	}
	return cm.narrow
}

// NarrowParts are the narrow ground's parts on team 1's half, the largest first (team 2's are their mirror).
func (cm *Compiled) NarrowParts() []DroppedPart {
	cs := cm.Layers.Size / Grid
	return halfParts(cm.narrowCells(), Grid, Grid, func(x, y int) Point { return cm.Layers.CellCentre(x, y) }, 1,
		cs/2*math.Sqrt2)
}

// narrowed is the reach.narrow warning: the whole count, the finding at the largest part of team 1's half, every
// part in Parts.
func (c *checker) narrowed(cm *Compiled, st *Stats) {
	parts := cm.NarrowParts()
	straight, angled := cm.PathWidth()
	msg := "%d walkable cells (%.0f m^2) are too narrow for the graphs, so nobody walks there: widen them to %g m " +
		"(%g m at an angle) or close them with a wall shape"
	args := []any{st.NarrowCells, float64(st.NarrowCells) * st.CellSize * st.CellSize, straight, angled}
	var at *Point
	if len(parts) > 0 {
		at = pt(parts[0].At)
		msg += ". %d part(s) on team 1's half (team 2's are their mirror), the largest %d cells at (%.1f, %.1f)"
		args = append(args, len(parts), parts[0].Cells, parts[0].At[0], parts[0].At[1])
	}
	c.warnf("reach.narrow", "terrain", at, msg, args...)
	c.fs[len(c.fs)-1].Parts = parts
}

// offGraphWhy says why world point p has no square of a graph near it (compAt's one square of slack), for the
// messages of reach.npc and lanes.graph: squares fit there but are cut off from the bases, or the ground holds none.
// For the latter it names the terrain shape that made the ground and the width it needs, shape being its path in
// map.json ("terrain.shapes[k]", the finding's Shape: the editor selects it), "" when the default ground is. Both are
// "" on ground that isn't walkable (fixed.open and the like say so).
func (cm *Compiled) offGraphWhy(p Point, npc bool) (why, shape string) {
	if why := cm.holeWhy(p); why != "" {
		return why, ""
	}
	fb := Blocks{On: cm.fitBlocks(npc), W: Grid / cm.Cell, H: Grid / cm.Cell}
	bx, by := cm.BlockAt(p)
	if fb.Near(bx, by, 1) >= 0 {
		return ": its ground is cut off from the bases (reach.dropped)", ""
	}
	if !Walkable(cm.Layers.KindAt(p)) {
		return "", ""
	}
	straight, angled := cm.PathWidth()
	k, g := cm.groundShape(p)
	if k < 0 {
		return fmt.Sprintf(": the ground there is too narrow; it needs %g m (%g m at an angle)", straight, angled), ""
	}
	shape = fmt.Sprintf("terrain.shapes[%d]", k)
	s := cm.Map.Terrain.Shapes[k]
	switch {
	case g.path != nil:
		need, at := straight, ""
		if !alongAxes(g.path, p) {
			need, at = angled, "at its angle "
		}
		what := fmt.Sprintf("a path %g m wide", 2*g.half)
		if s.Lane != 0 {
			what = fmt.Sprintf("lane %d's corridor, %g m wide", s.Lane, 2*g.half)
		}
		return fmt.Sprintf(": %s, %s, is too narrow there; %sit needs %g m", shape, what, at, need), shape
	case g.circle != nil:
		return fmt.Sprintf(": %s, a circle %g m across, is too small; it needs %g m", shape, 2*g.r, angled), shape
	}
	return fmt.Sprintf(": %s (a polygon) is too narrow there; it needs %g m (%g m at an angle)", shape, straight,
		angled), shape
}

// holeWhy names the hole off the graphs that p lies in or beside (two squares: a square goes when any point of it lies
// inside): a Guardian's platform (HoleRadius), a Binding's or a Core's footprint (StructureRadius). "" when there is
// none. A lane's leg through a Binding is the likely one: SM_Map_v5's stand 6-9 m beside their lane's line.
func (cm *Compiled) holeWhy(p Point) string {
	margin := 2 * cm.Layers.Size / Grid * float64(cm.Cell)
	for t := range 2 {
		tm := &cm.Layout.Teams[t]
		if dist(p, tm.Guardian) < HoleRadius+margin {
			return fmt.Sprintf(": team %d's Guardian platform (%g m round it) is off the graphs", t+1, HoleRadius)
		}
		for k, q := range tm.Cores {
			if dist(p, q) < StructureRadius+margin {
				return fmt.Sprintf(": team %d's Core %c stands at (%.1f, %.1f), %g m round it off the graphs", t+1,
					'A'+k, q[0], q[1], StructureRadius)
			}
		}
		for _, id := range cm.Layout.LaneIDs {
			bs := tm.Bindings[id]
			for k, q := range bs {
				if dist(p, q) < StructureRadius+margin {
					return fmt.Sprintf(": team %d's lane %d %s Binding stands at (%.1f, %.1f), %g m round it off the "+
						"graphs; move it off the lane's line (SM_Map_v5's stand 6-9 m beside theirs)", t+1, id,
						TierNames(len(bs))[k], q[0], q[1], StructureRadius)
				}
			}
		}
	}
	return ""
}

// groundShape is the terrain shape that made the walkable ground at p, as Rasterise paints them (the last shape, or
// its mirror, that holds p; a bush keeps the ground), and its geometry on that side; -1 for the default ground.
func (cm *Compiled) groundShape(p Point) (int, shapeGeom) {
	shapes := cm.Map.Terrain.Shapes
	for k := len(shapes) - 1; k >= 0; k-- {
		if shapes[k].Type == "bush" {
			continue
		}
		g, err := geom(cm.Map, shapes[k])
		if err != nil {
			continue
		}
		for _, gg := range []shapeGeom{g, g.mirrored()} {
			if gg.contains(p) {
				if v, ok := shapeKinds[shapes[k].Type]; ok && Walkable(v) {
					return k, gg
				}
				return -1, shapeGeom{}
			}
		}
	}
	return -1, shapeGeom{}
}

// alongAxes reports whether the leg of path nearest p runs within 5 degrees of the map's axes (north-south or
// east-west), where the narrower of PathWidth's widths holds.
func alongAxes(path []Point, p Point) bool {
	best, a, b := math.Inf(1), path[0], path[0]
	for i := 0; i+1 < len(path); i++ {
		if d := segDist(p, path[i], path[i+1]); d < best {
			best, a, b = d, path[i], path[i+1]
		}
	}
	deg := math.Mod(math.Abs(math.Atan2(b[1]-a[1], b[0]-a[0])*180/math.Pi), 90)
	return deg <= 5 || deg >= 85
}

// components are the 8-connected parts of the marked units of a w x h grid, each a list of unit indexes.
func components(marked []bool, w, h int) [][]int {
	seen := make([]bool, len(marked))
	var out [][]int
	for k := range marked {
		if !marked[k] || seen[k] {
			continue
		}
		members := []int{}
		st := []int{k}
		seen[k] = true
		for len(st) > 0 {
			q := st[len(st)-1]
			st = st[:len(st)-1]
			members = append(members, q)
			x, y := q%w, q/w
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := x+dx, y+dy
					if nx < 0 || ny < 0 || nx >= w || ny >= h {
						continue
					}
					if i := ny*w + nx; marked[i] && !seen[i] {
						seen[i] = true
						st = append(st, i)
					}
				}
			}
		}
		out = append(out, members)
	}
	return out
}

// halfParts are the 8-connected parts of the marked units of a w x h grid whose centre lies on team 1's half
// (x + y <= 0.5), the largest first. centre is a unit's world centre, unitCells the line-of-sight cells a unit covers
// and half half its diagonal (for the part's radius).
func halfParts(marked []bool, w, h int, centre func(x, y int) Point, unitCells int, half float64) []DroppedPart {
	var parts []DroppedPart
	for _, members := range components(marked, w, h) {
		pts := make([]Point, len(members))
		var c Point
		for i, k := range members {
			pts[i] = centre(k%w, k/w)
			c[0] += pts[i][0] / float64(len(members))
			c[1] += pts[i][1] / float64(len(members))
		}
		if c[0]+c[1] > 0.5 {
			continue
		}
		p := DroppedPart{Centre: c, Cells: len(members) * unitCells}
		bestD := math.Inf(1)
		for _, m := range pts {
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
