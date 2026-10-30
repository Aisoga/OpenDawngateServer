package navgraph

import (
	"fmt"

	"dawngate/server/internal/setup/mapmm/los"
)

// Spec says how a map's two graphs are made from its line-of-sight map.
type Spec struct {
	SizeX, SizeY float64 // the level's size (metres), which the map's cells span
	Cell         int     // map cells per square side
	Erode        int     // steps an open cell must keep from blocking ones
	Z            float64 // the squares' height
	Keep         []Point // the Player graph's island markers (the parts that hold one stay)
	NPCKeep      []Point // the NPC graph's
	Guardians    []Point // the Guardians' platforms: a hole in both graphs, an island in the NPC graph
	HoleRadius   float64
	IslandRadius float64
	IslandZ      float64
	IslandSides  int
}

// MM is MM_Map_v3's spec: squares of 2 x 2 cells at z 0.7; the Player marker in the middle; the NPC markers there,
// on both Guardians and on the two corner islands; holes of 10 m at the Guardians, NPC islands of 3.7 m (octagons
// at z 1.1) in them.
var MM = Spec{
	SizeX: los.Size, SizeY: los.Size, Cell: 2, Erode: 2, Z: 0.7,
	Keep:         []Point{{-8.5, 9.7}},
	NPCKeep:      []Point{{-8.5, 9.7}, {-166.3, -166.3}, {165.0, 165.4}, {260.6, 261.1}, {-260.8, -260.1}},
	Guardians:    []Point{{-166.3, -166.3}, {165.0, 165.4}},
	HoleRadius:   10.0,
	IslandRadius: 3.7, IslandZ: 1.1, IslandSides: 8,
}

// Build makes the two graphs of spec from an 8-bit line-of-sight map, the header values taken from the two
// template graphs (the Player and the NPC one).
func Build(img los.Image, spec Spec, playerTemplate, npcTemplate []byte) (player, npc []byte, err error) {
	return BuildTwo(img, img, spec, playerTemplate, npcTemplate)
}

// BuildTwo is Build with the NPC graph cut from its own walkable map of the same size: ground only NPCs walk is open
// in npcImg and blocked in img.
func BuildTwo(img, npcImg los.Image, spec Spec, playerTemplate, npcTemplate []byte) (player, npc []byte, err error) {
	for _, im := range []los.Image{img, npcImg} {
		if im.Bpp != 8 || len(im.Pix) < im.W*im.H {
			return nil, nil, fmt.Errorf("navgraph: an 8-bit line-of-sight map is expected (%d bits, %d of %d bytes)",
				im.Bpp, len(im.Pix), im.W*im.H)
		}
	}
	if npcImg.W != img.W || npcImg.H != img.H {
		return nil, nil, fmt.Errorf("navgraph: the NPC map is %d x %d, the Player map %d x %d", npcImg.W, npcImg.H,
			img.W, img.H)
	}
	pt, err := Parse(playerTemplate)
	if err != nil {
		return nil, nil, fmt.Errorf("navgraph: the Player template: %w", err)
	}
	nt, err := Parse(npcTemplate)
	if err != nil {
		return nil, nil, fmt.Errorf("navgraph: the NPC template: %w", err)
	}
	if len(pt.Graphs) == 0 || len(nt.Graphs) == 0 {
		return nil, nil, fmt.Errorf("navgraph: a template holds no graph")
	}
	holes := make([]Hole, len(spec.Guardians))
	for i, g := range spec.Guardians {
		holes[i] = Hole{g.X, g.Y, spec.HoleRadius}
	}
	mask := WalkableMask(img.Pix, img.W, img.H, spec.Erode)
	polys := GridPolygons(mask, img.W, img.H, spec.SizeX, spec.SizeY, spec.Cell, spec.Z, spec.Keep, holes)
	player = Write(FromPolygons(polys, pt))
	mask = WalkableMask(npcImg.Pix, img.W, img.H, spec.Erode)
	polys = GridPolygons(mask, img.W, img.H, spec.SizeX, spec.SizeY, spec.Cell, spec.Z, spec.NPCKeep, holes)
	for _, ho := range holes {
		polys = append(polys, IslandPolygon(ho.X, ho.Y, spec.IslandRadius, spec.IslandZ, spec.IslandSides))
	}
	npc = Write(FromPolygons(polys, nt))
	return player, npc, nil
}

// BuildMM makes MM_Map_v3_Player.bfx and MM_Map_v3_NPC.bfx from the map's line-of-sight map and SM_Map_v5's
// two shipped graphs (the templates).
func BuildMM(img los.Image, playerTemplate, npcTemplate []byte) (player, npc []byte, err error) {
	return Build(img, MM, playerTemplate, npcTemplate)
}
