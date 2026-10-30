package mapc

import (
	"fmt"

	"dawngate/server/internal/setup/mapmm/navgraph"
)

// The navigation graphs' grid, as MM_Map_v3's (MAPNAV2b): open cells at least Erode steps from a blocked one, squares
// of Cell x Cell cells at z GraphZ, the Guardians' NPC islands octagons at IslandZ. Cell is the smallest from
// MinCell to MaxCell that keeps both graphs under AreaBudget squares (MAPNAV2-live: about 7,000 keep the host's
// frame time).
const (
	Erode      = 2
	GraphZ     = 0.7
	IslandZ    = 1.1
	MinCell    = 2
	MaxCell    = 4
	AreaBudget = 7000
)

// Blocks is a graph's grid of squares: On[by*W+bx] where the graph has one (navgraph.KeptBlocks).
type Blocks struct {
	On    []bool
	W, H  int
	Areas int
}

// Compiled is a map with everything derived: both teams' objects, the layers and the graphs' grids.
type Compiled struct {
	Map         *Map
	Layout      *Layout
	Layers      *Layers
	Cell        int
	Player, NPC Blocks // the NPC graph's Areas count its Guardian islands too
	Mask        []bool // the walkable cells the Player graph is cut from (navgraph.WalkableMask)
	NPCMask     []bool // the NPC graph's: Mask with the islands (MAPED-ISLAND)
}

// walkableBlock reports whether every cell of block (bx, by) is in the walkable mask.
func (c *Compiled) walkableBlock(bx, by int) bool { return c.maskBlock(c.Mask, bx, by) }

func (c *Compiled) maskBlock(mask []bool, bx, by int) bool {
	for dj := 0; dj < c.Cell; dj++ {
		for di := 0; di < c.Cell; di++ {
			if !mask[(by*c.Cell+dj)*Grid+bx*c.Cell+di] {
				return false
			}
		}
	}
	return true
}

// Compile derives a map that passed the structural checks.
func Compile(m *Map) (*Compiled, error) {
	ly, err := Rasterise(m)
	if err != nil {
		return nil, err
	}
	c := &Compiled{Map: m, Layout: Resolve(m), Layers: ly}
	mask := navgraph.WalkableMask(ly.Walk().Pix, Grid, Grid, Erode)
	c.Mask = mask
	c.NPCMask = navgraph.WalkableMask(ly.NPCWalk().Pix, Grid, Grid, Erode)
	lo, hi := MinCell, MaxCell
	if set := m.graphCell(); set >= MinCell && set <= MaxCell { // graph.go: the map sets it
		lo, hi = set, set
	}
	for cell := lo; cell <= hi; cell++ {
		c.Cell = cell
		c.Player = c.blocks(mask, c.PlayerKeep())
		c.NPC = c.blocks(c.NPCMask, c.NPCKeep())
		c.NPC.Areas += len(c.Guardians())
		if c.Player.Areas < AreaBudget && c.NPC.Areas < AreaBudget {
			break
		}
	}
	return c, nil
}

func (c *Compiled) blocks(mask []bool, keep []navgraph.Point) Blocks {
	on, w, h := navgraph.KeptBlocks(mask, Grid, Grid, c.Map.Size, c.Map.Size, c.Cell, keep, c.holes())
	n := 0
	for _, v := range on {
		if v {
			n++
		}
	}
	return Blocks{On: on, W: w, H: h, Areas: n}
}

func navPoint(p Point) navgraph.Point { return navgraph.Point{X: p[0], Y: p[1]} }

// Guardians are both Guardians' places.
func (c *Compiled) Guardians() []navgraph.Point {
	return []navgraph.Point{navPoint(c.Layout.Teams[0].Guardian), navPoint(c.Layout.Teams[1].Guardian)}
}

func (c *Compiled) holes() []navgraph.Hole {
	var hs []navgraph.Hole
	for _, g := range c.Guardians() {
		hs = append(hs, navgraph.Hole{X: g.X, Y: g.Y, R: HoleRadius})
	}
	return hs
}

// PlayerKeep are the Player graph's island markers: each team's first spawn point (the parts of the walkable ground
// holding one stay; every other open pocket is dropped).
func (c *Compiled) PlayerKeep() []navgraph.Point {
	return []navgraph.Point{navPoint(c.Layout.Teams[0].Spawns[0]), navPoint(c.Layout.Teams[1].Spawns[0])}
}

// NPCKeep are the NPC graph's: the Player graph's, the Guardians and the wells on islands (so an island holding a
// well stays in the NPC graph though nothing joins it to a base, as MM_Map_v3's mines).
func (c *Compiled) NPCKeep() []navgraph.Point {
	keep := append(c.PlayerKeep(), c.Guardians()...)
	for t := range 2 {
		for _, w := range c.IslandWells(t) {
			keep = append(keep, navPoint(c.Layout.Teams[t].Wells[w]))
		}
	}
	return keep
}

// IslandWells are the indexes of team t's wells (0 or 1) that stand on island ground: out of the players' reach,
// each gets an NPC-only island marker (assets.go design), which the match host reads to leave the well out of the
// bots' well missions (MAPMINES).
func (c *Compiled) IslandWells(t int) []int {
	var ws []int
	for k, w := range c.Layout.Teams[t].Wells {
		if c.Layers.KindAt(w) == Island {
			ws = append(ws, k)
		}
	}
	return ws
}

// NavSpec is the navgraph spec the archive's graphs are built with.
func (c *Compiled) NavSpec() navgraph.Spec {
	return navgraph.Spec{
		SizeX: c.Map.Size, SizeY: c.Map.Size, Cell: c.Cell, Erode: Erode, Z: GraphZ,
		Keep: c.PlayerKeep(), NPCKeep: c.NPCKeep(), Guardians: c.Guardians(),
		HoleRadius: HoleRadius, IslandRadius: IslandRadius, IslandZ: IslandZ, IslandSides: 8,
	}
}

// BlockAt is the block (bx, by) holding world point p.
func (c *Compiled) BlockAt(p Point) (int, int) {
	return navgraph.BlockOf(navPoint(p), Grid, Grid, c.Map.Size, c.Map.Size, c.Cell)
}

// Near is the index of the graph's square at block (bx, by) or, with slack > 0, of the nearest within slack blocks
// (-1 if none).
func (b *Blocks) Near(bx, by, slack int) int {
	best, bestD := -1, 1<<30
	for dy := -slack; dy <= slack; dy++ {
		for dx := -slack; dx <= slack; dx++ {
			x, y := bx+dx, by+dy
			if x < 0 || y < 0 || x >= b.W || y >= b.H || !b.On[y*b.W+x] {
				continue
			}
			if d := dx*dx + dy*dy; d < bestD {
				best, bestD = y*b.W+x, d
			}
		}
	}
	return best
}

// Components numbers the graph's 4-connected parts (-1 off the graph).
func (b *Blocks) Components() []int {
	comp := make([]int, len(b.On))
	for i := range comp {
		comp[i] = -1
	}
	n := 0
	for k, on := range b.On {
		if !on || comp[k] >= 0 {
			continue
		}
		st := []int{k}
		comp[k] = n
		for len(st) > 0 {
			q := st[len(st)-1]
			st = st[:len(st)-1]
			x, y := q%b.W, q/b.W
			for _, nb := range [4][2]int{{x + 1, y}, {x - 1, y}, {x, y + 1}, {x, y - 1}} {
				if nb[0] < 0 || nb[1] < 0 || nb[0] >= b.W || nb[1] >= b.H {
					continue
				}
				if i := nb[1]*b.W + nb[0]; b.On[i] && comp[i] < 0 {
					comp[i] = n
					st = append(st, i)
				}
			}
		}
		n++
	}
	return comp
}

func (c *Compiled) String() string {
	return fmt.Sprintf("%s: cell %d, Player %d areas, NPC %d", c.Map.Name, c.Cell, c.Player.Areas, c.NPC.Areas)
}
