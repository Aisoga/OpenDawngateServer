package mapc

import (
	"fmt"
	"math"
	"strings"

	"dawngate/server/internal/setup/mapmm/ground"
)

// Fill: a scatter with "along": "area" spreads its props over the whole region instead of along the walls' edge
// (EDUX10). The region's cells of the kinds "on" names are the candidates, shuffled by the seed; each draws a noun, a
// turn, a scale and a jitter as the walls' scatter does, and is kept when it lies spacing from every kept prop and
// every mirror. On walkable ground:
//   - a decor noun (DecorNoun: grass, flowers, leaves, ferns, mushrooms, plants; by its name, so the rule needs no
//     copy) may stand anywhere and never blocks;
//   - any other noun (rocks, trees, ...) stands there only when the scatter blocks: its footprint (a circle of
//     footprint metres at scale 1, times its scale) becomes Solid ground, out of both graphs and blocking sight, as a
//     blocking single prop's (stampProps); without blocks such a noun is placed only off walkable ground;
//   - nothing stands, and no footprint reaches, within the keep-out of the lanes' corridors and the fixed set (the
//     Guardian's hole, the wells' and camps' rings, every other object, the ward spots): fillBarred.
//
// Every scatter is placed on the layers as the shapes and the single props leave them, before any fill stamps its
// footprints, so the scatters never depend on each other and the walls' scatter stays as it was. A fill's prop stands
// at the ground model's height (a forest on a plateau stands on it); the walls' scatter keeps z 0.

// The fill's options and limits.
const (
	FillOnAny       = "any"        // every kind of cell (the default)
	FillOnWalls     = "walls"      // walls only
	FillOnOffGround = "off-ground" // walls and chasms: nothing walks there
	FillOnGround    = "ground"     // walkable ground (open and islands) only

	DefaultFillFootprint = 2.0  // a blocking fill prop's footprint diameter at scale 1 (m): a trunk
	MaxFillFootprint     = 20.0 // m

	// The keep-out on walkable ground (m from the cell's centre; the lanes: past the corridor's half width).
	FillLaneMargin = 2.0
	FillGuardian   = HoleRadius + 8 // the hole and a margin
	FillWell       = 24.0           // the worker spots stand about 22 m round a well (a mine's 15.5-18.2 m)
	FillCamp       = 12.0           // a camp's creatures stand up to about 9 m round it
	FillObject     = 8.0            // the Locus, the shop, the spawns, the Cores, the Bindings, the minion spawners
	FillWard       = 4.0

	fillEdge = 2.0 // no prop within this of the level's edge (size.bounds' margin)
)

// FillOns are the values of a fill's "on".
var FillOns = []string{FillOnAny, FillOnWalls, FillOnOffGround, FillOnGround}

// DecorPrefixes are the beginnings of the decor nouns' names (lower case): low plants a unit may walk through, which a
// fill places on walkable ground without blocking. Build 854's dressing nouns of these names are its grass, flowers,
// ferns, bushes, leaves, mushrooms and water plants.
var DecorPrefixes = []string{"grass", "flowers_", "foliage_", "forestbush_", "forestleaves_", "fungus_mushroom",
	"leaves_", "lowshrub_", "plants_", "pumpkin_leaves"}

// DecorNoun reports whether a noun is decor (DecorPrefixes), by its name with or without ".Noun".
func DecorNoun(n string) bool {
	k := nounKey(n)
	for _, p := range DecorPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

// IsFill reports whether the scatter fills its region ("along": "area").
func (s Scatter) IsFill() bool { return s.Along == "area" }

// on is the fill's "on", defaulted.
func (s Scatter) on() string {
	if s.On == "" {
		return FillOnAny
	}
	return s.On
}

func (s Scatter) footprint() float64 {
	if s.Footprint == 0 {
		return DefaultFillFootprint
	}
	return s.Footprint
}

func (s Scatter) density() float64 {
	if s.Density == 0 {
		return 1
	}
	return s.Density
}

// takes reports whether a fill on "on" may use a cell of kind k.
func fillTakes(on string, k byte) bool {
	switch on {
	case FillOnWalls:
		return k == Wall
	case FillOnOffGround:
		return k == Wall || k == Chasm
	case FillOnGround:
		return Walkable(k)
	}
	return k != Solid
}

// placeScatters places every scatter on the layers as the shapes and the single props leave them, then stamps the
// blocking fills' footprints (Compile, before the graphs are cut).
func (cm *Compiled) placeScatters() error {
	m := cm.Map
	cm.scattered = make([][]Placed, len(m.Scatter))
	fills := false
	for _, s := range m.Scatter {
		fills = fills || s.IsFill()
	}
	if fills {
		if err := cm.fillGround(); err != nil {
			return err
		}
	}
	for k, s := range m.Scatter {
		if s.IsFill() {
			cm.scattered[k] = cm.filled(k)
		} else {
			cm.scattered[k] = cm.alongWalls(k)
		}
	}
	if !fills {
		return nil
	}
	ly := cm.Layers
	cm.fillSolid = make([]bool, Grid*Grid)
	for _, pls := range cm.scattered {
		for _, p := range pls {
			if p.Footprint == nil {
				continue
			}
			for _, c := range ly.stampCells(*p.Footprint, func(k int) bool { return !cm.barred[k] }) {
				ly.Kind[c] = Solid
				cm.fillSolid[c] = true
			}
		}
	}
	return nil
}

// fillGround makes what the fills read: the cells they never use (fillBarred) and the ground model's field for the
// props' heights.
func (cm *Compiled) fillGround() error {
	if cm.barred != nil {
		return nil
	}
	walk := cm.Layers.GroundWalk()
	field, err := ground.NewField(walk.W, walk.H, walk.Pix, cm.Map.Size)
	if err != nil {
		return err
	}
	cm.field = field
	cm.barred = cm.fillBarred()
	return nil
}

// FillBarred are the cells (j*Grid+i) a fill never uses: past the level's edge margin, past the void's cut (no ground
// there), and the walkable cells within the keep-out of the lanes and the fixed set. Made before the fills stamp.
func (cm *Compiled) FillBarred() ([]bool, error) {
	if err := cm.fillGround(); err != nil {
		return nil, err
	}
	return cm.barred, nil
}

// FillSolid are the cells the blocking fills stamped Solid (walkable before; nil without a fill).
func (cm *Compiled) FillSolid() []bool { return cm.fillSolid }

func (cm *Compiled) fillBarred() []bool {
	m, ly := cm.Map, cm.Layers
	out := make([]bool, Grid*Grid)
	type disc struct {
		p Point
		r float64
	}
	var discs []disc
	add := func(p Point, r float64) { discs = append(discs, disc{p, r}, disc{Mirror(p), r}) }
	t := &cm.Layout.Teams[0]
	add(t.Guardian, FillGuardian)
	for _, p := range append([]Point{t.Locus, t.Shop}, append(append([]Point{}, t.Spawns...), t.Cores...)...) {
		add(p, FillObject)
	}
	for _, id := range cm.Layout.LaneIDs {
		for _, p := range t.Bindings[id] {
			add(p, FillObject)
		}
		add(t.Spawners[id], FillObject)
	}
	for _, p := range t.Wells {
		add(p, FillWell)
	}
	for _, c := range t.Camps {
		add(c.Pos, FillCamp)
	}
	for _, w := range m.Wards {
		add(w.Pos, FillWard)
	}
	type band struct {
		knots []Point
		half  float64
	}
	var bands []band
	for tt := range cm.Layout.Teams {
		for _, id := range cm.Layout.LaneIDs {
			bands = append(bands, band{cm.Layout.Teams[tt].Lanes[id], cm.laneWidth(id)/2 + FillLaneMargin})
		}
	}
	lim := m.Size/2 - fillEdge
	cut := m.voidCut()
	for j := 0; j < Grid; j++ {
		for i := 0; i < Grid; i++ {
			k := j*Grid + i
			c := ly.CellCentre(i, j)
			if math.Abs(c[0]) > lim || math.Abs(c[1]) > lim || (cut > 0 && cm.field.D[k] > cut-1) {
				out[k] = true
				continue
			}
			if !Walkable(ly.Kind[k]) {
				continue
			}
			for _, d := range discs {
				if dist(c, d.p) <= d.r {
					out[k] = true
					break
				}
			}
			if out[k] {
				continue
			}
			for _, b := range bands {
				if lineDist(c, b.knots) <= b.half {
					out[k] = true
					break
				}
			}
		}
	}
	return out
}

// lineDist is p's distance to the polyline.
func lineDist(p Point, pl []Point) float64 {
	if len(pl) == 1 {
		return dist(p, pl[0])
	}
	best := math.Inf(1)
	for i := 0; i+1 < len(pl); i++ {
		best = math.Min(best, segDist(p, pl[i], pl[i+1]))
	}
	return best
}

// stampCells are the walkable cells (and ok) a footprint covers: those whose centre lies in it, and the one under its
// centre.
func (ly *Layers) stampCells(f Footprint, ok func(k int) bool) []int {
	var out []int
	r := math.Max(f.Axes[0], f.Axes[1])
	i0, j0 := ly.CellOf(Point{f.Centre[0] - r, f.Centre[1] - r})
	i1, j1 := ly.CellOf(Point{f.Centre[0] + r, f.Centre[1] + r})
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			if k := j*Grid + i; Walkable(ly.Kind[k]) && ok(k) && f.contains(ly.CellCentre(i, j)) {
				out = append(out, k)
			}
		}
	}
	if i, j := ly.CellOf(f.Centre); Walkable(ly.Kind[j*Grid+i]) && ok(j*Grid+i) {
		out = append(out, j*Grid+i)
	}
	return out
}

// spacedSet is the kept props' places (and their mirrors) in buckets of the spacing, for the spacing test.
type spacedSet struct {
	cell float64
	b    map[[2]int][]Point
}

func newSpaced(spacing float64) *spacedSet { return &spacedSet{cell: spacing, b: map[[2]int][]Point{}} }

func (s *spacedSet) key(p Point) [2]int {
	return [2]int{int(math.Floor(p[0] / s.cell)), int(math.Floor(p[1] / s.cell))}
}

// near reports whether a kept place lies less than the spacing from p (squared distances: the page's estimate
// computes the same test bit for bit).
func (s *spacedSet) near(p Point) bool {
	k := s.key(p)
	lim := float64(s.cell * s.cell)
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			for _, q := range s.b[[2]int{k[0] + dx, k[1] + dy}] {
				ex, ey := p[0]-q[0], p[1]-q[1]
				if float64(ex*ex)+float64(ey*ey) < lim {
					return true
				}
			}
		}
	}
	return false
}

func (s *spacedSet) add(p Point) { k := s.key(p); s.b[k] = append(s.b[k], p) }

// filled are fill scatter[k]'s props on both sides, in the order they were drawn.
func (cm *Compiled) filled(k int) []Placed {
	s := cm.Map.Scatter[k]
	ly := cm.Layers
	spacing := s.Spacing
	if spacing == 0 {
		spacing = DefaultSpacing
	}
	most := s.Max
	if most == 0 {
		most = DefaultScatter
	}
	lo, hi := scatterScale(s)
	on, density, fp := s.on(), s.density(), s.footprint()
	in := func(Point) bool { return true }
	switch {
	case s.Circle != nil:
		c, r := *s.Circle, s.R
		in = func(p Point) bool {
			dx, dy := p[0]-c[0], p[1]-c[1]
			return float64(dx*dx)+float64(dy*dy) <= float64(r*r)
		}
	case s.Polygon != nil:
		in = func(p Point) bool { return inPolygon(p, s.Polygon) }
	}
	// the candidates: team 1's cells in the region of the kinds on takes, not barred, row by row
	var cands []int
	for j := 0; j < Grid; j++ {
		for i := 0; i < Grid; i++ {
			kk := j*Grid + i
			c := ly.CellCentre(i, j)
			if c[0]+c[1] > 0 || cm.barred[kk] || !fillTakes(on, ly.Kind[kk]) || !in(c) {
				continue
			}
			cands = append(cands, kk)
		}
	}
	rng := &splitmix{s: s.Seed}
	for i := len(cands) - 1; i > 0; i-- {
		j := int(rng.next() % uint64(i+1))
		cands[i], cands[j] = cands[j], cands[i]
	}
	spec := cm.GroundSpec()
	cell := ly.Size / Grid
	taken := newSpaced(spacing)
	lim := float64(spacing * spacing)
	var out []Placed
	n := 0
	for _, kk := range cands {
		if n >= most {
			break
		}
		c := ly.CellCentre(kk%Grid, kk/Grid)
		jx, jy := (rng.float()-0.5)*cell, (rng.float()-0.5)*cell
		noun := s.Nouns[rng.next()%uint64(len(s.Nouns))]
		yaw := round3(rng.float()*360 - 180)
		scale := round3(lo + rng.float()*(hi-lo))
		decor := DecorNoun(noun)
		if Walkable(ly.Kind[kk]) && !decor && !s.Blocks {
			continue // a rock or a tree on walkable ground only when the scatter blocks
		}
		p := Point{round3(c[0] + jx), round3(c[1] + jy)}
		if 4*(float64(p[0]*p[0])+float64(p[1]*p[1])) < lim || taken.near(p) { // its mirror, the kept ones
			continue
		}
		taken.add(p)
		taken.add(Mirror(p))
		if density < 1 && rng.float() >= density {
			continue // its place stays taken: the density thins the filled region evenly
		}
		z := round3(ground.Height(cm.field.At(p[0], p[1]), p[0], p[1], &spec))
		pl := Placed{Noun: noun, Pos: [3]float64{p[0], p[1], z}, Rot: [3]float64{0, 0, yaw}, Scale: scale,
			Path: fmt.Sprintf("scatter[%d]", k), Shadow: s.Shadow, key: fmt.Sprintf("scatter/%d/%d", k, n)}
		if s.Blocks && !decor {
			r := fp / 2 * scale
			pl.Blocks = true
			pl.Footprint = &Footprint{Centre: p, Axes: [2]float64{r, r}, Yaw: yaw}
		}
		if b, ok := cm.Map.nounBounds[nounKey(noun)]; ok {
			pl.box = &b
		}
		out = append(out, sides(pl)...)
		n++
	}
	return out
}

// fillRules are a scatter's structural rules for the fill's members (propRules): scatter.on, scatter.footprint,
// scatter.density, scatter.decor; each fill member on a walls scatter (and inset on a fill) is a warning: not read.
func (c *checker) fillRules(path string, s Scatter) {
	if !s.IsFill() {
		for _, x := range []struct {
			set  bool
			name string
		}{{s.On != "", "on"}, {s.Blocks, "blocks"}, {s.Footprint != 0, "footprint"}, {s.Density != 0, "density"}} {
			if x.set {
				c.warnf("scatter."+x.name, path+"."+x.name, nil, "%s is read only when the scatter fills its area "+
					"(\"along\": \"area\")", x.name)
			}
		}
		return
	}
	if s.Inset != 0 {
		c.warnf("scatter.inset", path+".inset", nil, "inset is read only along the walls")
	}
	if !inList(s.on(), FillOns, false) {
		c.errorf("scatter.on", path+".on", nil, "on %q: %s", s.On, strings.Join(FillOns, ", "))
	}
	if s.Footprint != 0 && !(s.Footprint > 0 && s.Footprint <= MaxFillFootprint) {
		c.errorf("scatter.footprint", path+".footprint", nil, "footprint %g: more than 0, at most %g m", s.Footprint,
			MaxFillFootprint)
	} else if s.Footprint != 0 && !s.Blocks {
		c.warnf("scatter.footprint", path+".footprint", nil, "footprint is read only when the scatter blocks")
	}
	if s.Density != 0 && !(s.Density > 0 && s.Density <= 1) {
		c.errorf("scatter.density", path+".density", nil, "density %g: more than 0, at most 1", s.Density)
	}
	if s.on() == FillOnGround && !s.Blocks {
		decor := false
		for _, n := range s.Nouns {
			decor = decor || DecorNoun(n)
		}
		if !decor && len(s.Nouns) > 0 {
			c.warnf("scatter.decor", path+".nouns", nil, "places nothing: rocks and trees stand on walkable ground "+
				"only when the scatter blocks; without blocks only decor does (the nouns beginning %s)",
				strings.Join(DecorPrefixes, ", "))
		}
	}
}

// scatterGeometry warns of a scatter that places nothing (scatter.empty): the reason a drawn region shows no props.
func (c *checker) scatterGeometry(cm *Compiled) {
	for k, s := range cm.Map.Scatter {
		if len(cm.scattered[k]) > 0 {
			continue
		}
		why := "no wall cell next to walkable ground lies in its region"
		if s.IsFill() {
			why = fmt.Sprintf("no cell of its region is free for it (on %s; the lanes and the fixed set keep their "+
				"ground clear)", s.on())
		}
		c.warnf("scatter.empty", fmt.Sprintf("scatter[%d]", k), nil, "the scatter places nothing: %s", why)
	}
}
