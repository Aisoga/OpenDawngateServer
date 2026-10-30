package mapc

import (
	"fmt"
	"math"

	"dawngate/server/internal/setup/mapmm/fogmap"
	"dawngate/server/internal/setup/mapmm/grassmap"
	"dawngate/server/internal/setup/mapmm/los"
)

// Grid is the layers' size: every shipped line-of-sight, grass and tag map is 512 x 512.
const Grid = 512

// The ground kinds of a cell.
const (
	Wall  byte = 0 // blocks sight and walking
	Open  byte = 1 // walked and seen through
	Chasm byte = 2 // seen through, not walked
	Solid byte = 3 // a blocking prop's footprint: blocks sight and walking, the ground model stays flat (props.go)
	// Island is ground only NPCs walk (MAPED-ISLAND, MM_Map_v3's mines): seen through, in the NPC graph, out of the
	// Player graph; the ground model is flat on it as on open ground.
	Island byte = 4
)

// Walkable reports whether NPCs walk a cell of kind k (open ground and islands).
func Walkable(k byte) bool { return k == Open || k == Island }

// Bush is one bush patch: its outline's name (its grass id is FNV-1 of it), its fog outline (counter-clockwise) and
// the shape it comes from (index into terrain.shapes; Mirrored for team 2's copy).
type Bush struct {
	Name     string
	ID       uint32
	Outline  []Point
	Shape    int
	Mirrored bool
	Cells    []int // j*Grid+i, j from the bottom row
}

// Layers are the rasterised layout: Grid x Grid cells, rows from the bottom (y's minimum), cell (i, j) centred on
// ((i - Grid/2) * size/Grid, (j - Grid/2) * size/Grid) as the graphs' grid places it.
type Layers struct {
	Size   float64
	Kind   []byte // Wall, Open, Chasm, Solid, Island
	Bushes []Bush
}

// CellCentre is cell (i, j)'s centre in world units.
func (ly *Layers) CellCentre(i, j int) Point {
	c := ly.Size / Grid
	return Point{float64(i-Grid/2) * c, float64(j-Grid/2) * c}
}

// CellOf is the cell holding world point p (clamped to the grid).
func (ly *Layers) CellOf(p Point) (i, j int) {
	c := ly.Size / Grid
	i = int(math.Floor(p[0]/c + Grid/2 + 0.5))
	j = int(math.Floor(p[1]/c + Grid/2 + 0.5))
	return min(max(i, 0), Grid-1), min(max(j, 0), Grid-1)
}

// LoS is the line-of-sight map: 0 open (open and chasm cells), 128 blocked (walls and blocking props), rows from the
// bottom.
func (ly *Layers) LoS() los.Image {
	pix := make([]byte, Grid*Grid)
	for k, v := range ly.Kind {
		if v == Wall || v == Solid {
			pix[k] = 128
		}
	}
	return los.Image{W: Grid, H: Grid, Bpp: 8, Pix: pix}
}

// Walk is the ground players walk in the line-of-sight map's form (0 open, 128 blocked): the Player graph's input.
func (ly *Layers) Walk() los.Image {
	pix := make([]byte, Grid*Grid)
	for k, v := range ly.Kind {
		if v != Open {
			pix[k] = 128
		}
	}
	return los.Image{W: Grid, H: Grid, Bpp: 8, Pix: pix}
}

// NPCWalk is the ground NPCs walk in Walk's form (open ground and islands): the NPC graph's input.
func (ly *Layers) NPCWalk() los.Image {
	pix := make([]byte, Grid*Grid)
	for k, v := range ly.Kind {
		if !Walkable(v) {
			pix[k] = 128
		}
	}
	return los.Image{W: Grid, H: Grid, Bpp: 8, Pix: pix}
}

// GroundWalk is the ground model's input in Walk's form: the ground NPCs walk with the blocking props' footprints
// counted as walkable, so the ground stays flat under a prop and on an island (it rises only past walls).
func (ly *Layers) GroundWalk() los.Image {
	img := ly.NPCWalk()
	for k, v := range ly.Kind {
		if v == Solid {
			img.Pix[k] = 0
		}
	}
	return img
}

// KindAt is the ground kind at world point p.
func (ly *Layers) KindAt(p Point) byte {
	i, j := ly.CellOf(p)
	return ly.Kind[j*Grid+i]
}

// shapeGeom is a shape's geometry with the lane corridors looked up: a test whether a point lies in it.
type shapeGeom struct {
	circle  *Point
	r       float64
	polygon []Point
	path    []Point
	half    float64 // the path's half width
}

func (g shapeGeom) mirrored() shapeGeom {
	out := g
	if g.circle != nil {
		c := Mirror(*g.circle)
		out.circle = &c
	}
	out.polygon = mirrorAll(g.polygon)
	out.path = mirrorAll(g.path)
	return out
}

func (g shapeGeom) contains(p Point) bool {
	switch {
	case g.circle != nil:
		return dist(p, *g.circle) <= g.r
	case g.polygon != nil:
		return inPolygon(p, g.polygon)
	case g.path != nil:
		for i := 0; i+1 < len(g.path); i++ {
			if segDist(p, g.path[i], g.path[i+1]) <= g.half {
				return true
			}
		}
		return len(g.path) == 1 && dist(p, g.path[0]) <= g.half
	}
	return false
}

// bounds is the geometry's bounding box (x0, y0, x1, y1).
func (g shapeGeom) bounds() [4]float64 {
	b := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	add := func(p Point, r float64) {
		b[0], b[1] = math.Min(b[0], p[0]-r), math.Min(b[1], p[1]-r)
		b[2], b[3] = math.Max(b[2], p[0]+r), math.Max(b[3], p[1]+r)
	}
	switch {
	case g.circle != nil:
		add(*g.circle, g.r)
	case g.polygon != nil:
		for _, p := range g.polygon {
			add(p, 0)
		}
	default:
		for _, p := range g.path {
			add(p, g.half)
		}
	}
	return b
}

// inPolygon is the even-odd rule.
func inPolygon(p Point, poly []Point) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a[1] > p[1]) != (b[1] > p[1]) && p[0] < (b[0]-a[0])*(p[1]-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

// geom is shape k's geometry (lane corridors from the map's lanes).
func geom(m *Map, s Shape) (shapeGeom, error) {
	n := 0
	var g shapeGeom
	if s.Lane != 0 {
		n++
		for _, ln := range m.Lanes {
			if ln.ID == s.Lane {
				g.path, g.half = ln.Knots, ln.Width/2
			}
		}
		if g.path == nil {
			return g, fmt.Errorf("names lane %d, which the map doesn't have", s.Lane)
		}
	}
	if s.Circle != nil {
		n++
		c := *s.Circle
		g.circle, g.r = &c, s.R
		if !(s.R > 0) {
			return g, fmt.Errorf("a circle needs r > 0")
		}
	}
	if s.Polygon != nil {
		n++
		g.polygon = s.Polygon
		if len(s.Polygon) < 3 {
			return g, fmt.Errorf("a polygon needs at least 3 corners")
		}
	}
	if s.Path != nil {
		n++
		g.path, g.half = s.Path, s.Width/2
		if len(s.Path) < 2 || !(s.Width > 0) {
			return g, fmt.Errorf("a path needs at least 2 points and a width > 0")
		}
	}
	if n != 1 {
		return g, fmt.Errorf("a shape needs exactly one of lane, circle, polygon or path (it has %d)", n)
	}
	return g, nil
}

// kinds are terrain.default's values; shapeKinds a shape's (bush aside).
var (
	kinds      = map[string]byte{"wall": Wall, "open": Open, "chasm": Chasm}
	shapeKinds = map[string]byte{"wall": Wall, "open": Open, "chasm": Chasm, "island": Island}
)

// Rasterise draws the terrain: the default everywhere, then each shape and its mirror in order; a bush shape adds a
// bush patch on each side and keeps the ground. The map must have passed the structural checks.
func Rasterise(m *Map) (*Layers, error) {
	def, ok := kinds[m.Terrain.Default]
	if !ok {
		return nil, fmt.Errorf("terrain.default %q is not wall, open or chasm", m.Terrain.Default)
	}
	ly := &Layers{Size: m.Size, Kind: make([]byte, Grid*Grid)}
	for k := range ly.Kind {
		ly.Kind[k] = def
	}
	for k, s := range m.Terrain.Shapes {
		g, err := geom(m, s)
		if err != nil {
			return nil, fmt.Errorf("terrain.shapes[%d]: %v", k, err)
		}
		for side, gg := range []shapeGeom{g, g.mirrored()} {
			cells := ly.cellsIn(gg)
			if s.Type == "bush" {
				b, err := ly.bush(m, k, side == 1, gg, cells)
				if err != nil {
					return nil, fmt.Errorf("terrain.shapes[%d]: %v", k, err)
				}
				ly.Bushes = append(ly.Bushes, b)
				continue
			}
			v, ok := shapeKinds[s.Type]
			if !ok {
				return nil, fmt.Errorf("terrain.shapes[%d]: type %q is not open, wall, chasm, island or bush", k,
					s.Type)
			}
			for _, c := range cells {
				ly.Kind[c] = v
			}
		}
	}
	ly.stampProps(m)
	return ly, nil
}

// cellsIn are the cells whose centre lies in g, row by row.
func (ly *Layers) cellsIn(g shapeGeom) []int {
	b := g.bounds()
	i0, j0 := ly.CellOf(Point{b[0], b[1]})
	i1, j1 := ly.CellOf(Point{b[2], b[3]})
	var out []int
	for j := max(j0-1, 0); j <= min(j1+1, Grid-1); j++ {
		for i := max(i0-1, 0); i <= min(i1+1, Grid-1); i++ {
			if g.contains(ly.CellCentre(i, j)) {
				out = append(out, j*Grid+i)
			}
		}
	}
	return out
}

// bush makes a bush patch: its name <map>_grass_<shape>[m] (a trailing x added until the id's top byte, the grass
// map's alpha, is non-zero), its outline (a circle as a 24-gon, a polygon as drawn, counter-clockwise).
func (ly *Layers) bush(m *Map, k int, mirrored bool, g shapeGeom, cells []int) (Bush, error) {
	var pts []Point
	switch {
	case g.circle != nil:
		for a := 0; a < 24; a++ {
			t := 2 * math.Pi * float64(a) / 24
			pts = append(pts, Point{g.circle[0] + g.r*math.Cos(t), g.circle[1] + g.r*math.Sin(t)})
		}
	case g.polygon != nil:
		pts = append(pts, g.polygon...)
		if signedArea(pts) < 0 {
			pts = reversed(pts)
		}
	default:
		return Bush{}, fmt.Errorf("a bush is a circle or a polygon")
	}
	name := fmt.Sprintf("%s_grass_%02d", m.Name, k)
	if mirrored {
		name += "m"
	}
	id, err := grassmap.ID(name)
	for err == nil && id>>24 == 0 {
		name += "x"
		id, err = grassmap.ID(name)
	}
	if err != nil {
		return Bush{}, err
	}
	return Bush{Name: name, ID: id, Outline: pts, Shape: k, Mirrored: mirrored, Cells: cells}, nil
}

func signedArea(ps []Point) float64 {
	var a float64
	for i := range ps {
		j := (i + 1) % len(ps)
		a += ps[i][0]*ps[j][1] - ps[j][0]*ps[i][1]
	}
	return a / 2
}

// GrassOutlines are the bushes' fog outlines.
func (ly *Layers) GrassOutlines() []fogmap.Outline {
	out := make([]fogmap.Outline, len(ly.Bushes))
	for k, b := range ly.Bushes {
		pts := make([]fogmap.Point, len(b.Outline))
		for i, p := range b.Outline {
			pts[i] = fogmap.Point{X: p[0], Y: p[1]}
		}
		out[k] = fogmap.Outline{Grass: b.ID, Points: pts}
	}
	return out
}

// GrassCells are the grass map's non-zero cells as the game reads them (MM_Map_v3's rule, grassmap): each bush's
// cells hold its id (an earlier bush keeps a shared cell), then a one-cell ring round each bush, still empty cells,
// holds the id's low 24 bits (alpha 0).
func (ly *Layers) GrassCells() map[int]uint32 {
	vals := map[int]uint32{}
	for _, b := range ly.Bushes {
		for _, c := range b.Cells {
			if _, ok := vals[c]; !ok {
				vals[c] = b.ID
			}
		}
	}
	for _, b := range ly.Bushes {
		for _, c := range b.Cells {
			i, j := c%Grid, c/Grid
			for dj := -1; dj <= 1; dj++ {
				for di := -1; di <= 1; di++ {
					ii, jj := i+di, j+dj
					if ii < 0 || ii >= Grid || jj < 0 || jj >= Grid {
						continue
					}
					if _, ok := vals[jj*Grid+ii]; !ok {
						vals[jj*Grid+ii] = b.ID & 0x00FFFFFF
					}
				}
			}
		}
	}
	return vals
}

// GrassTGA is the grass map: a 32-bit TGA laid out as the first map's (18-byte header, 8 alpha bits, rows from the
// bottom, B, G, R, A).
func (ly *Layers) GrassTGA() []byte {
	out := make([]byte, 18+4*Grid*Grid)
	out[2] = 2
	out[12], out[13], out[14], out[15] = Grid&0xFF, Grid>>8, Grid&0xFF, Grid>>8
	out[16], out[17] = 32, 8
	for k, v := range ly.GrassCells() {
		o := 18 + 4*k
		out[o], out[o+1], out[o+2], out[o+3] = byte(v>>16), byte(v>>8), byte(v), byte(v>>24)
	}
	return out
}

// LoSTGA is the line-of-sight map's file: an 8-bit grey TGA, rows from the bottom.
func LoSTGA(img los.Image) []byte {
	out := make([]byte, 18, 18+len(img.Pix))
	out[2] = 3
	out[12], out[13], out[14], out[15] = byte(img.W), byte(img.W>>8), byte(img.H), byte(img.H>>8)
	out[16] = 8
	return append(out, img.Pix...)
}
