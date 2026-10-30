package mapc

// The graph section (optional): the navigation graphs' square size. Without it the compiler picks the smallest square
// from MinCell to MaxCell that keeps both graphs under AreaBudget (compile.go); with it the author sets the square,
// as an imported level's own graphs have it (MM_Map_v3's are squares of 2 cells, navgraph.MM). A smaller square
// follows the walls more closely (the walkable ground reaches nearer to them: a square is only kept when all its cells
// are walkable, so the graph's rim lies up to Cell - 1 cells inside the eroded edge) and costs more areas. A map
// without it builds as before, byte for byte.

// Graph is the map's graph section.
type Graph struct {
	Cell int `json:"cell,omitempty"` // map cells a square's side (MinCell to MaxCell; 0: the budget picks it)
}

// MaxAreas is the most areas a graph of a set square size may have: MM_Map_v3 plays with 8,089 Player and 8,459 NPC
// areas (squares of 2 cells), and SM_Map_v5's 29,961-square grid cost the match host 5 ms a tick more than its
// shipped graph (MAPNAV2-live); 10,000 keeps a set size near the proven count.
const MaxAreas = 10000

// cell is the square size the map sets (0: none).
func (m *Map) graphCell() int {
	if m.Graph == nil {
		return 0
	}
	return m.Graph.Cell
}

// graphRules checks the section's values (structure).
func (c *checker) graphRules() {
	g := c.m.Graph
	if g == nil {
		return
	}
	if g.Cell != 0 && (g.Cell < MinCell || g.Cell > MaxCell) {
		c.errorf("graph.cell", "graph.cell", nil, "cell %d: %d to %d map cells, or 0 (the budget picks it)", g.Cell,
			MinCell, MaxCell)
	}
}

// graphGeometry checks a set square size's area counts (after the compile).
func (c *checker) graphGeometry(cm *Compiled) {
	if c.m.graphCell() == 0 {
		return
	}
	for _, g := range []struct {
		name  string
		areas int
	}{{"Player", cm.Player.Areas}, {"NPC", cm.NPC.Areas}} {
		if g.areas > MaxAreas {
			c.errorf("graph.areas", "graph.cell", nil, "the %s graph has %d areas with squares of %d cells, more than "+
				"%d: a larger cell, or none (the budget picks it)", g.name, g.areas, cm.Cell, MaxAreas)
		}
	}
}
