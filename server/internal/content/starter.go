package content

import "fmt"

// The starter Spiritstone page: the perk page a new account starts with.
//
// Its parts are the game data's Starter_ perk shapes, which nothing else uses: two Red Spiritstones of Fervor
// (Starter_HastePower_R1, shape triangle4_starter: the bottom-right half of a 2 x 2 square, cut along the
// diagonal from its top-right to its bottom-left corner; one red socket), the Green Spiritstone of Bravery (Starter_ArmorHealth_G2, square8: 2 x 2, two green sockets) and the
// Blue Spiritstone of Vigor (Starter_HealthPower_B2, rectangle16: 2 wide, 4 high, four blue sockets), each socket
// holding the plain Spark of its colour (FlatHealth, FlatPower, FlatHaste). The areas 2 + 2 + 4 + 8 fill the 4 x 4
// board; the page gives MaxHealth 108, Power 9, Haste 4 and armor (PhysicalDamageReduction) 4.
//
// The placement, as the perk screen draws a page (Perks.gfx: _OnCurrentLoadoutDataChanged puts each shape's clip at
// mGridPoints[ROT][GIDX] and turns it ROT x 90 degrees clockwise about that point; the shape's convexShape is in
// cells with x the column and y the row): the point of cell GIDX = row x 4 + column is its top-left corner for ROT
// 0, top-right for 1, bottom-right for 2 and bottom-left for 3, so GIDX is the board cell the shape's own cell
// (0, 0) lands on.
//   - Vigor at cell 0, unturned: columns 0-1, rows 0-3, the left half.
//   - Bravery at cell 2, unturned: columns 2-3, rows 0-1, the top-right square.
//   - one Fervor at cell 10 (row 2, column 2), unturned: the bottom-right half of the bottom-right square;
//   - the other at cell 15, turned twice: its 2 x 2 box ends at the board's bottom-right corner, and the half
//     turned with it is the square's top-left half.
//
// The sockets turn with their shape: a gem names its socket by its index in PerkShape.socket whatever the
// rotation (SIDX), so every socket index of each shape gets its Spark.
var StarterPage = []StarterShape{
	{Shape: "Starter_HealthPower_B2", Cell: 0, Rotation: 0, Gem: "FlatHaste"},
	{Shape: "Starter_ArmorHealth_G2", Cell: 2, Rotation: 0, Gem: "FlatPower"},
	{Shape: "Starter_HastePower_R1", Cell: 10, Rotation: 0, Gem: "FlatHealth"},
	{Shape: "Starter_HastePower_R1", Cell: 15, Rotation: 2, Gem: "FlatHealth"},
}

// StarterPageName is the starter page's name on the perk screen.
const StarterPageName = "Starter"

// StarterShape is one shape of the starter page: the PerkShape and the Gem by their asset names, its board cell
// (row x 4 + column) and quarter turns, and the gem that fills every one of its sockets.
type StarterShape struct {
	Shape          string
	Cell, Rotation uint8
	Gem            string
}

// StarterPart is a StarterShape resolved against the content: the shape's and the gem's catalogIDs and the
// shape's socket count.
type StarterPart struct {
	StarterShape
	ShapeID, GemID uint32
	Sockets        int
}

// StarterParts resolves the starter page against the content. It fails when a shape or gem is missing, isn't a
// sold item of its kind, has no socket data (a content file of the B7a format), or a socket doesn't take the gem's
// colour, so a page built from it is complete in every shape.
func (c *Content) StarterParts() ([]StarterPart, error) {
	if c.Empty() || c.Old() {
		return nil, fmt.Errorf("content: no content with sockets")
	}
	parts := make([]StarterPart, 0, len(StarterPage))
	for _, s := range StarterPage {
		shape, err := c.perkItem(s.Shape, KindShape, ItemPerkShape)
		if err != nil {
			return nil, err
		}
		gem, err := c.perkItem(s.Gem, KindGem, ItemPerkGem)
		if err != nil {
			return nil, err
		}
		if len(shape.Sockets) == 0 {
			return nil, fmt.Errorf("content: %s has no sockets", s.Shape)
		}
		for i, sock := range shape.Sockets {
			if sock != SocketAny && sock != gem.Category {
				return nil, fmt.Errorf("content: %s socket %d is %s, %s is %s", s.Shape, i, sock, s.Gem, gem.Category)
			}
		}
		parts = append(parts, StarterPart{StarterShape: s, ShapeID: shape.CatalogID, GemID: gem.CatalogID, Sockets: len(shape.Sockets)})
	}
	return parts, nil
}

// StarterComponents is how many of each item the starter page uses (catalogID -> count): 2 Fervors, 1 Bravery,
// 1 Vigor, 2 + 2 + 4 Sparks. Empty when the content can't build the page (StarterParts).
func (c *Content) StarterComponents() map[uint32]uint32 {
	parts, err := c.StarterParts()
	if err != nil {
		return nil
	}
	n := map[uint32]uint32{}
	for _, p := range parts {
		n[p.ShapeID]++
		n[p.GemID] += uint32(p.Sockets)
	}
	return n
}

// perkItem returns the perk of this kind with this asset name, which must also be an inventory item of this type.
func (c *Content) perkItem(name, kind string, itemType int) (Perk, error) {
	for _, p := range c.Perks {
		if p.Name != name {
			continue
		}
		if p.Kind != kind || p.CatalogID == 0 {
			return Perk{}, fmt.Errorf("content: %s is not a %s item", name, kind)
		}
		if it, ok := c.Item(p.CatalogID); !ok || it.ItemType != itemType {
			return Perk{}, fmt.Errorf("content: %s (%#x) is not an inventory item", name, p.CatalogID)
		}
		return p, nil
	}
	return Perk{}, fmt.Errorf("content: no %s %s", kind, name)
}
