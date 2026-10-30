package mapc

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/mapmm/ground"
)

// Props: the shipped models a map places (MAPED-DRESS), by the nouns' names only, so a package still holds no game
// bytes. Three kinds, all authored on team 1's half and mirrored like the shapes (a prop within CentreTolerance of
// the centre stands once; a prop with "mirror": false stands once wherever it is, so a group can take a level's
// dressing of both halves as the level has it):
//   - props[]: one noun at a place, turned (rot, degrees about z, counter-clockwise from east) and scaled; with
//     "blocks" its footprint (the noun's box, or the author's "footprint") becomes ground that blocks walking and
//     sight while the ground model stays flat under it (the Solid kind); with "shadow" a shadow card lies under it.
//     Instead of a noun a prop can name a group: every dressing marker of a shipped level's markersets within r of a
//     point or inside a polygon, moved, turned and scaled as one. A group's holes leave members out, so some can be
//     taken out of it as single props, each with its height, turn, scale and tilt (a noun's "tilt", about x and y).
//   - scatter[]: props spread by a seed along the walls' edge (inside the wall), each with a random noun of a list,
//     a random turn and scale; deterministic.
// The compiler writes them into the map's own markerset <name>_props (only when the map has props or scatter, so a
// map without them compiles as before); each marker copies a shipped marker of the noun (its flags and components),
// a group's member its own.

// The props' limits and defaults.
const (
	MinPropScale     = 0.05
	MaxPropScale     = 20.0
	MaxFootprint     = 100.0 // a footprint's width or depth (m)
	MaxGroupRadius   = 80.0
	MaxScatter       = 1000 // a scatter's props on team 1's half
	DefaultScatter   = 200
	DefaultSpacing   = 6.0
	MinSpacing       = 1.0
	DefaultInset     = 1.0
	MaxInset         = 10.0
	CentreTolerance  = 0.5 // a prop this close to the centre is its own mirror: it stands once
	ShadowNoun       = "ao_blobshadow_01.Noun"
	shadowSpread     = 1.4 // a shadow card's width over its prop's
	defaultScaleLow  = 0.8
	defaultScaleHigh = 1.2
)

// PropTypes are the noun types a prop may have: the dressing (SM_Map_v5's dressing sets hold only these). Gameplay
// nouns (spawn points, towers, creatures, interactables) are refused: the host would count them.
var PropTypes = []string{"obstacle", "terrain", "Water", "Placeable Effect", "animated", "point light",
	"spot light", "line light", "Decal"}

// Prop is a placed shipped model (noun) or a group of a shipped level's dressing (group): exactly one of them.
type Prop struct {
	Noun      string      `json:"noun,omitempty"`  // a shipped noun, with or without ".Noun"
	Group     *Group      `json:"group,omitempty"` // or a group of a shipped level's markers
	Pos       [3]float64  `json:"pos"`             // x, y and the height (z, default 0: the floor)
	Rot       float64     `json:"rot"`             // degrees about z, counter-clockwise
	Scale     float64     `json:"scale"`           // 0 or omitted: 1
	Blocks    bool        `json:"blocks,omitempty"`
	Footprint *[2]float64 `json:"footprint,omitempty"` // the blocking ellipse's width and depth (m, along the turned x and y)
	Shadow    bool        `json:"shadow,omitempty"`    // a shadow card under it

	// Tilt turns a noun about x and y first (degrees, the marker's rotDegrees x and y; rot is its z), as a group's
	// member keeps its own tilt: a member taken out of a group as a single prop stands as it stood in the group.
	Tilt *[2]float64 `json:"tilt,omitempty"`

	// Mirror false places the prop once, exactly where it is authored, anywhere on the map (no mirror for team 2): a
	// group then takes its members as the level has them on both halves (mapc import --dress writes a level's
	// dressing so, since hand-placed dressing is only roughly point-symmetric), a noun stands once. Omitted or true:
	// the prop is authored on team 1's half and mirrored.
	Mirror *bool `json:"mirror,omitempty"`
}

// Once reports whether the prop stands once, unmirrored ("mirror": false).
func (p Prop) Once() bool { return p.Mirror != nil && !*p.Mirror }

// MaxTilt bounds a prop's tilt about x and y (degrees).
const MaxTilt = 180.0

// A single noun whose box's top lies within FlushTolerance of the floor (z 0) is flush with the compiled ground and
// fights it in the depth buffer (seen on CM_Pass: darius_tower_binding, a plinth whose top face is its model's z 0,
// at pos z 0 drew torn); props.flush proposes a lift so its top lies FlushLift above the floor.
const (
	FlushTolerance = 0.02
	FlushLift      = 0.05
)

// Group is a part of a shipped level's dressing: the markers of its markersets (by suffix, "D_Rocks" for
// <level>_D_Rocks) within a region of the level's coordinates: within r of at, or inside polygon (even-odd; any size,
// so a whole half of a level fits: mapc import --dress). The prop's pos is where at lands; its rot and scale turn and
// scale the group about it. The members keep their own height, tilt and components.
type Group struct {
	Level   string   `json:"level"`
	Sets    []string `json:"sets"`
	At      Point    `json:"at"`
	R       float64  `json:"r,omitempty"`
	Polygon []Point  `json:"polygon,omitempty"`

	// Holes leave out the markers inside any of these polygons (the level's coordinates, even-odd): the members
	// taken out of the group as single props.
	Holes [][]Point `json:"holes,omitempty"`
}

// MaxGroupCorner bounds a group polygon's corners (the level's coordinates, metres from its centre).
const MaxGroupCorner = MaxSize

// takes reports whether a marker at p (the level's coordinates) lies in the group's region and in none of its holes.
func (g *Group) takes(p Point) bool {
	for _, h := range g.Holes {
		if inPolygon(p, h) {
			return false
		}
	}
	if g.Polygon != nil {
		return inPolygon(p, g.Polygon)
	}
	return math.Hypot(p[0]-g.At[0], p[1]-g.At[1]) <= g.R
}

// Scatter spreads props along the walls of team 1's half (and mirrors them): each on a wall cell next to walkable
// ground, Inset metres into the wall, at least Spacing from every other (and from every mirrored one), with a noun
// of Nouns, a turn and a scale in Scale drawn from Seed. Circle or Polygon limit it to a region.
type Scatter struct {
	Nouns   []string   `json:"nouns"`
	Seed    uint64     `json:"seed"`
	Along   string     `json:"along,omitempty"` // "walls" (the default)
	Circle  *Point     `json:"circle,omitempty"`
	R       float64    `json:"r,omitempty"`
	Polygon []Point    `json:"polygon,omitempty"`
	Spacing float64    `json:"spacing,omitempty"` // default DefaultSpacing
	Inset   float64    `json:"inset,omitempty"`   // default DefaultInset
	Scale   [2]float64 `json:"scale,omitempty"`   // [min, max], default [0.8, 1.2]
	Max     int        `json:"max,omitempty"`     // at most this many on team 1's half, default DefaultScatter
	Shadow  bool       `json:"shadow,omitempty"`
}

// NounBounds is a noun's box (its bbox times its graphicsScale, in the model's frame).
type NounBounds struct {
	Min, Max [3]float64
}

// Footprint is a blocking prop's ellipse on the ground: its centre, its half axes along the prop's turned x and y,
// its turn (degrees).
type Footprint struct {
	Centre Point      `json:"centre"`
	Axes   [2]float64 `json:"axes"`
	Yaw    float64    `json:"yaw"`
}

// Placed is one marker the props place, in the markerset's order.
type Placed struct {
	Noun      string     `json:"noun"`
	Pos       [3]float64 `json:"pos"`
	Rot       [3]float64 `json:"rot"` // the marker's rotDegrees
	Scale     float64    `json:"scale"`
	Team      int        `json:"team"`            // 1 authored, 2 its mirror, 0 once (at the centre, or water overlapping its mirror)
	Path      string     `json:"path"`            // the entry of map.json it comes from: props[3], scatter[0]
	Group     string     `json:"group,omitempty"` // Placements only: a group's level (Build expands it)
	Blocks    bool       `json:"blocks,omitempty"`
	Footprint *Footprint `json:"footprint,omitempty"`
	Shadow    bool       `json:"shadow,omitempty"` // a shadow card follows it
	Card      bool       `json:"card,omitempty"`   // this is a shadow card (under the prop before it)

	key  string            // the marker id's key
	tmpl *assetdata.Object // a group member's own marker
	box  *NounBounds       // the noun's box when known (for the shadow)
}

// HasProps reports whether the map places props (then its archive has the props markerset).
func HasProps(m *Map) bool { return len(m.Props) > 0 || len(m.Scatter) > 0 }

// PropsMarkerset is the name (without ".Markerset") of the markerset that holds a map's props.
func PropsMarkerset(name string) string { return name + "_" + setProps }

func nounKey(n string) string { return strings.ToLower(strings.TrimSuffix(n, ".Noun")) }

func propScale(s float64) float64 {
	if s == 0 {
		return 1
	}
	return s
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

// normDeg is an angle in (-180, 180].
func normDeg(a float64) float64 {
	a = math.Mod(a, 360)
	if a > 180 {
		a -= 360
	} else if a <= -180 {
		a += 360
	}
	return a
}

// rotate turns v by deg degrees counter-clockwise.
func rotate(v Point, deg float64) Point {
	s, c := math.Sincos(deg * math.Pi / 180)
	return Point{float64(v[0]*c) - float64(v[1]*s), float64(v[0]*s) + float64(v[1]*c)}
}

func atCentre(p Point) bool { return math.Hypot(p[0], p[1]) <= CentreTolerance }

func (f Footprint) contains(p Point) bool {
	q := rotate(Point{p[0] - f.Centre[0], p[1] - f.Centre[1]}, -f.Yaw)
	x, y := q[0]/f.Axes[0], q[1]/f.Axes[1]
	return float64(x*x)+float64(y*y) <= 1
}

func (f Footprint) mirrored() Footprint {
	return Footprint{Centre: Mirror(f.Centre), Axes: f.Axes, Yaw: normDeg(f.Yaw + 180)}
}

// propFootprint is a single prop's footprint (team 1's): the author's, or the noun's box scaled and turned; false
// when neither is known (the box comes from the copy: ResolveProps).
func propFootprint(m *Map, p Prop) (Footprint, bool) {
	at := Point{p.Pos[0], p.Pos[1]}
	if p.Footprint != nil {
		return Footprint{Centre: at, Axes: [2]float64{p.Footprint[0] / 2, p.Footprint[1] / 2}, Yaw: p.Rot}, true
	}
	b, ok := m.nounBounds[nounKey(p.Noun)]
	if !ok {
		return Footprint{}, false
	}
	s := propScale(p.Scale)
	off := rotate(Point{(b.Min[0] + b.Max[0]) / 2 * s, (b.Min[1] + b.Max[1]) / 2 * s}, p.Rot)
	return Footprint{Centre: Point{at[0] + off[0], at[1] + off[1]},
		Axes: [2]float64{(b.Max[0] - b.Min[0]) / 2 * s, (b.Max[1] - b.Min[1]) / 2 * s}, Yaw: p.Rot}, true
}

// stampProps marks the blocking props' footprints (and their mirrors) on open ground as Solid: they block walking and
// sight, the ground model stays as it is. A footprint smaller than a cell still takes the cell under its centre.
func (ly *Layers) stampProps(m *Map) {
	for _, p := range m.Props {
		if !p.Blocks || p.Noun == "" {
			continue
		}
		f, ok := propFootprint(m, p)
		if !ok || !(f.Axes[0] > 0 && f.Axes[1] > 0) {
			continue
		}
		ly.stamp(f)
		if !p.Once() && !atCentre(Point{p.Pos[0], p.Pos[1]}) {
			ly.stamp(f.mirrored())
		}
	}
}

func (ly *Layers) stamp(f Footprint) {
	r := math.Max(f.Axes[0], f.Axes[1])
	i0, j0 := ly.CellOf(Point{f.Centre[0] - r, f.Centre[1] - r})
	i1, j1 := ly.CellOf(Point{f.Centre[0] + r, f.Centre[1] + r})
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			if k := j*Grid + i; Walkable(ly.Kind[k]) && f.contains(ly.CellCentre(i, j)) {
				ly.Kind[k] = Solid
			}
		}
	}
	if i, j := ly.CellOf(f.Centre); Walkable(ly.Kind[j*Grid+i]) {
		ly.Kind[j*Grid+i] = Solid
	}
}

// ---------------------------------------------------------------- the rules

// propRules are the props' structural rules (Check): props.kind, props.scale, props.footprint, props.blocks,
// props.group, scatter.*, and size.bounds and symmetry.half for their places.
func (c *checker) propRules() {
	m := c.m
	lim := m.Size/2 - edgeMargin
	place := func(path string, p Point, once bool) {
		if math.Abs(p[0]) > lim || math.Abs(p[1]) > lim {
			c.errorf("size.bounds", path, pt(p), "(%g, %g) lies outside the level (+-%g m, %g m from its edge)",
				p[0], p[1], m.Size/2, edgeMargin)
		}
		if !once && p[0]+p[1] > symTolerance {
			c.errorf("symmetry.half", path, pt(p), "(%g, %g) lies in team 2's half (x + y > 0); author team 1's "+
				"half only, team 2's is mirrored", p[0], p[1])
		}
	}
	for k, p := range m.Props {
		path := fmt.Sprintf("props[%d]", k)
		at := Point{p.Pos[0], p.Pos[1]}
		if (p.Noun == "") == (p.Group == nil) {
			c.errorf("props.kind", path, pt(at), "a prop names exactly one of noun or group")
			continue
		}
		place(path+".pos", at, p.Once())
		if s := propScale(p.Scale); !(s >= MinPropScale && s <= MaxPropScale) {
			c.errorf("props.scale", path+".scale", pt(at), "scale %g: %g to %g", p.Scale, MinPropScale, MaxPropScale)
		}
		if f := p.Footprint; f != nil && !(f[0] > 0 && f[0] <= MaxFootprint && f[1] > 0 && f[1] <= MaxFootprint) {
			c.errorf("props.footprint", path+".footprint", pt(at), "footprint %g x %g: each more than 0 and at "+
				"most %g m", f[0], f[1], MaxFootprint)
		}
		if g := p.Group; g != nil {
			if p.Blocks || p.Footprint != nil || p.Shadow {
				c.errorf("props.blocks", path, pt(at), "a group neither blocks nor gets a shadow (its members bring "+
					"their own); draw a wall shape where it should block")
			}
			if g.Level == "" || len(g.Sets) == 0 {
				c.errorf("props.group", path+".group", pt(at), "a group names a level and at least one of its "+
					"markersets")
			}
			for i, s := range g.Sets {
				if s == "" {
					c.errorf("props.group", fmt.Sprintf("%s.group.sets[%d]", path, i), pt(at), "an empty set name")
				}
			}
			switch {
			case g.Polygon != nil && g.R != 0:
				c.errorf("props.group", path+".group", pt(at), "a group's region is r or a polygon, not both")
			case g.Polygon != nil:
				c.groupPolygon(path+".group.polygon", at, g.Polygon)
			case !(g.R > 0 && g.R <= MaxGroupRadius):
				c.errorf("props.group", path+".group.r", pt(at), "r %g: more than 0 and at most %g (or a polygon)",
					g.R, MaxGroupRadius)
			}
			for i, h := range g.Holes {
				c.groupPolygon(fmt.Sprintf("%s.group.holes[%d]", path, i), at, h)
			}
			if p.Tilt != nil {
				c.errorf("props.tilt", path+".tilt", pt(at), "a group's members keep their own tilt; tilt is a "+
					"single prop's")
			}
			continue
		}
		if t := p.Tilt; t != nil && !(math.Abs(t[0]) <= MaxTilt && math.Abs(t[1]) <= MaxTilt) {
			c.errorf("props.tilt", path+".tilt", pt(at), "tilt %g, %g: each -%g to %g degrees", t[0], t[1],
				MaxTilt, MaxTilt)
		}
		// a noun whose top face lies at the floor's height fights the ground in the depth buffer (known with the
		// copy's boxes: check --copy, build)
		if b, ok := m.nounBounds[nounKey(p.Noun)]; ok && p.Tilt == nil && b.Min[2] < b.Max[2] { // not an effect's point box

			if top := p.Pos[2] + propScale(p.Scale)*b.Max[2]; math.Abs(top) < FlushTolerance {
				c.warnf("props.flush", path+".pos", pt(at), "the top of %s lies at z %.2f, on the floor (z 0): the two "+
					"surfaces fight and it shows torn; lift it with pos's z (%.2f m: a top at z %.2f)", p.Noun, top,
					FlushLift-top+p.Pos[2], FlushLift)
			}
		}
		if p.Blocks {
			if _, ok := propFootprint(m, p); !ok {
				c.warnf("props.footprint", path, pt(at), "the footprint of %s comes from the copy (check --copy, "+
					"build) or the prop's footprint; without either it doesn't block here", p.Noun)
			}
		}
	}
	for k, s := range m.Scatter {
		path := fmt.Sprintf("scatter[%d]", k)
		if len(s.Nouns) == 0 {
			c.errorf("scatter.nouns", path+".nouns", nil, "a scatter names at least one noun")
		}
		for i, n := range s.Nouns {
			if n == "" {
				c.errorf("scatter.nouns", fmt.Sprintf("%s.nouns[%d]", path, i), nil, "an empty noun")
			}
		}
		if s.Along != "" && s.Along != "walls" {
			c.errorf("scatter.along", path+".along", nil, "along %q: \"walls\"", s.Along)
		}
		if s.Spacing != 0 && !(s.Spacing >= MinSpacing) {
			c.errorf("scatter.spacing", path+".spacing", nil, "spacing %g: at least %g m", s.Spacing, MinSpacing)
		}
		if !(s.Inset >= 0 && s.Inset <= MaxInset) {
			c.errorf("scatter.inset", path+".inset", nil, "inset %g: 0 to %g m", s.Inset, MaxInset)
		}
		if lo, hi := scatterScale(s); !(lo >= MinPropScale && hi <= MaxPropScale && lo <= hi) {
			c.errorf("scatter.scale", path+".scale", nil, "scale [%g, %g]: %g <= min <= max <= %g", s.Scale[0],
				s.Scale[1], MinPropScale, MaxPropScale)
		}
		if !(s.Max >= 0 && s.Max <= MaxScatter) {
			c.errorf("scatter.max", path+".max", nil, "max %d: 0 (the default %d) to %d", s.Max, DefaultScatter,
				MaxScatter)
		}
		switch {
		case s.Circle != nil && s.Polygon != nil:
			c.errorf("scatter.region", path, nil, "a scatter's region is a circle or a polygon, not both")
		case s.Circle != nil && !(s.R > 0):
			c.errorf("scatter.region", path+".r", pt(*s.Circle), "a circle needs r > 0")
		case s.Polygon != nil && len(s.Polygon) < 3:
			c.errorf("scatter.region", path+".polygon", nil, "a polygon needs at least 3 corners")
		}
	}
}

// groupPolygon checks a group's polygon region: at least 3 corners, each within MaxGroupCorner of the level's centre,
// enclosing some area.
func (c *checker) groupPolygon(path string, at Point, poly []Point) {
	if len(poly) < 3 {
		c.errorf("props.group", path, pt(at), "a group's polygon needs at least 3 corners")
		return
	}
	area := 0.0
	for i, p := range poly {
		if !(math.Abs(p[0]) <= MaxGroupCorner && math.Abs(p[1]) <= MaxGroupCorner) {
			c.errorf("props.group", fmt.Sprintf("%s[%d]", path, i), pt(at), "corner (%g, %g): at most %g m from "+
				"the level's centre", p[0], p[1], MaxGroupCorner)
			return
		}
		q := poly[(i+1)%len(poly)]
		area += float64(p[0]*q[1]) - float64(q[0]*p[1])
	}
	if !(math.Abs(area)/2 >= 1) {
		c.errorf("props.group", path, pt(at), "the group's polygon encloses no area")
	}
}

func scatterScale(s Scatter) (lo, hi float64) {
	if s.Scale == [2]float64{} {
		return defaultScaleLow, defaultScaleHigh
	}
	return s.Scale[0], s.Scale[1]
}

// checkShippedProps runs the props' rules that need the copy: every noun exists (nouns.exist), a group's level and
// markersets exist (props.group).
func (c *checker) checkShippedProps(s *Shipped) {
	m := c.m
	noun := func(n string) bool { return s.Nouns[nounKey(n)] }
	for k, p := range m.Props {
		at := pt(Point{p.Pos[0], p.Pos[1]})
		if p.Noun != "" && !noun(p.Noun) {
			c.errorf("nouns.exist", fmt.Sprintf("props[%d].noun", k), at, "the copy has no noun %s", p.Noun)
		}
		if g := p.Group; g != nil && g.Level != "" {
			if !s.Levels[strings.ToLower(g.Level)] {
				c.errorf("props.group", fmt.Sprintf("props[%d].group.level", k), at, "the copy has no level %s",
					g.Level)
				continue
			}
			for i, set := range g.Sets {
				if s.Markersets != nil && !s.Markersets[strings.ToLower(g.Level+"_"+set)] {
					c.errorf("props.group", fmt.Sprintf("props[%d].group.sets[%d]", k, i), at, "the copy has no "+
						"markerset %s_%s", g.Level, set)
				}
			}
		}
	}
	for k, sc := range m.Scatter {
		for i, n := range sc.Nouns {
			if n != "" && !noun(n) {
				c.errorf("nouns.exist", fmt.Sprintf("scatter[%d].nouns[%d]", k, i), nil, "the copy has no noun %s", n)
			}
		}
	}
}

// ---------------------------------------------------------------- the nouns' boxes

// propNouns are the nouns whose boxes the props need: every single prop's and scatter noun, and the shadow card.
func propNouns(m *Map) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if k := nounKey(n); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, n)
		}
	}
	for _, p := range m.Props {
		add(p.Noun)
	}
	for _, s := range m.Scatter {
		for _, n := range s.Nouns {
			add(n)
		}
	}
	if len(out) > 0 {
		add(ShadowNoun)
	}
	return out
}

// ResolveProps reads the boxes of the nouns the map's props name from the copy's AssetData.snap, so that Check and
// Compile stamp the blocking props' footprints and Build sizes the shadow cards. A noun the copy lacks is left out
// (CheckShipped's nouns.exist names it). Build calls it; a map without props needs nothing.
func ResolveProps(m *Map, assetData []byte) error {
	m.fords = nil
	if !HasProps(m) {
		return nil
	}
	src, err := newAssetSource(assetData)
	if err != nil {
		return err
	}
	ps := newPropSource(src)
	m.nounBounds = map[string]NounBounds{}
	for _, n := range propNouns(m) {
		if info := ps.noun(n); info != nil && info.box != nil {
			m.nounBounds[nounKey(n)] = *info.box
		}
	}
	// the fords: the water planes the props place (groups and single nouns; a group or noun the copy lacks is
	// left to the placing's findings)
	cm := &Compiled{Map: m}
	var water []Placed
	for k, p := range m.Props {
		if p.Group != nil {
			if pls, _, err := ps.group(cm, k); err == nil {
				water = append(water, pls...)
			}
			continue
		}
		if info := ps.noun(p.Noun); isWater(info) {
			pls := cm.single(k)
			for i := range pls {
				pls[i].Noun = info.name
			}
			if pls, err = ps.waterOnce(pls); err != nil {
				return err
			}
			water = append(water, pls...)
		}
	}
	m.fords, err = ps.fords(water)
	return err
}

// ---------------------------------------------------------------- the props' water: fords, planes standing once

// A prop's water plane (a Water noun: a group's member, or a single prop placed as the noun's first shipped marker)
// a little under the floor, as MM_Map_v3's ford in its centre (z -0.22, 30 x 60 m, no mask), is a ford: the ground
// model's walkable floor dips under its rectangle (ground.Dip) to FordWater below the water, so the water shows over
// the floor as on MM_Map_v3 (whose floor dips 0.55 m there, mapmm/ground's Spec.Ford). A deeper plane (a chasm's
// water; MM's diagonal water at z -2.9 under its walls) dips nothing: it would make a pit of walkable ground. A masked
// plane isn't a ford: its mask cuts the water where the dip would show bare. A Water prop whose plane overlaps its
// mirror's (MM's ford, 2.5 m from it) stands once, where team 1's stands (team 0): the two would be drawn over each
// other. Scatter places no water.
const (
	FordMinZ  = -1.0 // a plane at least this high (and below the floor, z 0) is a ford
	FordWater = 0.33 // the water over the dipped floor (MM_Map_v3: its 0.55 m dip under its plane at -0.22)
)

// isWater reports whether a noun is a water plane.
func isWater(info *nounInfo) bool { return info != nil && strings.EqualFold(info.typ, "Water") }

// waterPlane is a placed water prop's plane (its template's waterData size times its scale) and mask; false when the
// prop isn't water or its template has no size.
func (ps *propSource) waterPlane(p Placed) (WaterPlane, string, bool, error) {
	if p.Card || !isWater(ps.noun(p.Noun)) {
		return WaterPlane{}, "", false, nil
	}
	tm := p.tmpl
	if tm == nil {
		var err error
		if tm, err = ps.template(p.Noun); err != nil || tm == nil {
			return WaterPlane{}, "", false, err
		}
	}
	wd := sub(tm, "waterData")
	if wd == nil {
		return WaterPlane{}, "", false, nil
	}
	sz := vec(wd, "size")
	if len(sz) != 2 || !(sz[0] > 0 && sz[1] > 0) {
		return WaterPlane{}, "", false, nil
	}
	return WaterPlane{Team: p.Team, Pos: p.Pos, Rot: p.Rot[2], Size: [2]float64{sz[0] * p.Scale, sz[1] * p.Scale},
		Path: p.Path}, text(wd, "mask"), true, nil
}

// overlapsMirror reports whether a team 1 water prop's plane overlaps its mirror's.
func (ps *propSource) overlapsMirror(p Placed) (bool, error) {
	w, _, ok, err := ps.waterPlane(p)
	if !ok || err != nil {
		return false, err
	}
	m := w
	m.Pos = [3]float64{-w.Pos[0], -w.Pos[1], w.Pos[2]}
	m.Rot = w.Rot + 180
	return rectsOverlap(w, m), nil
}

// waterOnce keeps a placement and its mirror (sides) as one prop at team 1's place (team 0) when it is water whose
// plane overlaps its mirror's.
func (ps *propSource) waterOnce(pls []Placed) ([]Placed, error) {
	if len(pls) != 2 {
		return pls, nil
	}
	once, err := ps.overlapsMirror(pls[0])
	if err != nil || !once {
		return pls, err
	}
	p := pls[0]
	p.Team = 0
	return []Placed{p}, nil
}

// fords are the dips under the placed water props that are fords.
func (ps *propSource) fords(pls []Placed) ([]ground.Dip, error) {
	var out []ground.Dip
	for _, p := range pls {
		w, mask, ok, err := ps.waterPlane(p)
		if err != nil {
			return nil, err
		}
		if z := w.Pos[2]; !ok || mask != "" || z < FordMinZ || z >= 0 {
			continue
		}
		r := w.Rot * math.Pi / 180
		out = append(out, ground.Dip{X: w.Pos[0], Y: w.Pos[1], Cos: math.Cos(r), Sin: math.Sin(r),
			HalfW: w.Size[0] / 2, HalfL: w.Size[1] / 2, Depth: round3(w.Pos[2] - FordWater)})
	}
	return out, nil
}

// Fords are the dips the ground model makes under the map's fords (ResolveProps reads them from the copy; nil before
// or without one).
func (cm *Compiled) Fords() []ground.Dip { return cm.Map.fords }

// propSource is the copy's assets with what the props look up: the nouns, the template markers.
type propSource struct {
	*assetSource
	nouns   map[string]*nounInfo
	byNoun  map[string]*assetdata.Object // the first shipped marker of each noun, from the sets read so far
	pending []string                     // the markersets not read yet, in search order
	read    map[string]bool
}

type nounInfo struct {
	name  string // as the copy spells it, with .Noun
	typ   string
	model string
	scale float64 // graphicsScale
	box   *NounBounds
}

func newPropSource(src *assetSource) *propSource {
	return &propSource{assetSource: src, nouns: map[string]*nounInfo{}, byNoun: map[string]*assetdata.Object{},
		read: map[string]bool{}}
}

// noun reads a noun's name, type and box (nil when the copy has none).
func (ps *propSource) noun(n string) *nounInfo {
	k := nounKey(n)
	if info, ok := ps.nouns[k]; ok {
		return info
	}
	var info *nounInfo
	if e, ok := ps.byName[strings.ToLower(AssetEntry(k+".Noun"))]; ok {
		name := strings.TrimSuffix(strings.TrimPrefix(e.Name, assetDir), ".bin")
		if o, err := ps.object(name); err == nil {
			info = &nounInfo{name: name, model: text(o, "modelPath")}
			if v, ok := o.Get("nounType"); ok {
				info.typ = fmt.Sprint(v)
			}
			gs := 1.0
			if v, ok := o.Get("graphicsScale"); ok && fval(v) > 0 {
				gs = fval(v)
			}
			info.scale = gs
			if bb := sub(o, "bbox"); bb != nil {
				lo, hi := vec(bb, "min"), vec(bb, "max")
				if len(lo) == 3 && len(hi) == 3 {
					b := &NounBounds{}
					for i := range 3 {
						b.Min[i], b.Max[i] = lo[i]*gs, hi[i]*gs
					}
					info.box = b
				}
			}
		}
	}
	ps.nouns[k] = info
	return info
}

func isPropType(t string) bool { return inList(t, PropTypes, false) }

// template is the first shipped marker of a noun: the template level's markersets in its order first, then every
// other markerset of the copy by name; nil when no markerset places the noun.
func (ps *propSource) template(noun string) (*assetdata.Object, error) {
	k := nounKey(noun) + ".noun"
	if m, ok := ps.byNoun[k]; ok {
		return m, nil
	}
	if ps.pending == nil {
		lv, err := ps.object(TemplateLevel + ".Level")
		if err != nil {
			return nil, err
		}
		sets, err := levelSets(lv)
		if err != nil {
			return nil, err
		}
		var rest []string
		for _, e := range ps.arch.Entries {
			if n := e.Name; strings.HasPrefix(n, assetDir) && strings.HasSuffix(strings.ToLower(n), ".markerset.bin") {
				rest = append(rest, strings.TrimSuffix(strings.TrimPrefix(n, assetDir), ".Markerset.bin"))
			}
		}
		sort.Slice(rest, func(i, j int) bool { return strings.ToLower(rest[i]) < strings.ToLower(rest[j]) })
		ps.pending = append(append([]string{}, sets...), rest...)
	}
	for len(ps.pending) > 0 {
		s := ps.pending[0]
		ps.pending = ps.pending[1:]
		if ps.read[strings.ToLower(s)] {
			continue
		}
		ps.read[strings.ToLower(s)] = true
		o, err := ps.object(s + ".Markerset")
		if err != nil {
			continue // a name with another case, or not parsable: skip it
		}
		for _, m := range markers(o) {
			if n := strings.ToLower(text(m, "nounDef")); ps.byNoun[n] == nil {
				ps.byNoun[n] = m
			}
		}
		if m, ok := ps.byNoun[k]; ok {
			return m, nil
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------- the placements

// splitmix is the scatter's random numbers (SplitMix64: the same on every platform).
type splitmix struct{ s uint64 }

func (r *splitmix) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (r *splitmix) float() float64 { return float64(r.next()>>11) / (1 << 53) }

// sides are a team-1 placement and its mirror (team 2: x and y negated, turned by 180 degrees), or the placement
// alone at the centre (team 0).
func sides(p Placed) []Placed {
	if atCentre(Point{p.Pos[0], p.Pos[1]}) {
		p.Team = 0
		return []Placed{p}
	}
	p.Team = 1
	q := p
	q.Team = 2
	q.Pos = [3]float64{-p.Pos[0], -p.Pos[1], p.Pos[2]}
	q.Rot = [3]float64{p.Rot[0], p.Rot[1], normDeg(p.Rot[2] + 180)}
	q.key = p.key + "/m"
	if p.Footprint != nil {
		f := p.Footprint.mirrored()
		q.Footprint = &f
	}
	return []Placed{p, q}
}

// single is props[k] (a noun) placed on both sides.
func (cm *Compiled) single(k int) []Placed {
	p := cm.Map.Props[k]
	pl := Placed{Noun: p.Noun, Pos: p.Pos, Rot: [3]float64{0, 0, normDeg(p.Rot)}, Scale: propScale(p.Scale),
		Path: fmt.Sprintf("props[%d]", k), Blocks: p.Blocks, Shadow: p.Shadow, key: fmt.Sprintf("prop/%d", k)}
	if p.Tilt != nil {
		pl.Rot[0], pl.Rot[1] = p.Tilt[0], p.Tilt[1]
	}
	if b, ok := cm.Map.nounBounds[nounKey(p.Noun)]; ok {
		pl.box = &b
	}
	if p.Blocks {
		if f, ok := propFootprint(cm.Map, p); ok {
			pl.Footprint = &f
		}
	}
	return placeSides(p, pl)
}

// placeSides is a placement of prop p on both sides (sides), or once where it is (team 0) when p doesn't mirror.
func placeSides(p Prop, pl Placed) []Placed {
	if p.Once() {
		pl.Team = 0
		return []Placed{pl}
	}
	return sides(pl)
}

// Scattered are scatter[k]'s props on both sides, in the order they were drawn.
func (cm *Compiled) Scattered(k int) []Placed {
	s := cm.Map.Scatter[k]
	ly := cm.Layers
	spacing := s.Spacing
	if spacing == 0 {
		spacing = DefaultSpacing
	}
	inset := s.Inset
	if inset == 0 {
		inset = DefaultInset
	}
	most := s.Max
	if most == 0 {
		most = DefaultScatter
	}
	lo, hi := scatterScale(s)
	in := func(Point) bool { return true }
	switch {
	case s.Circle != nil:
		c, r := *s.Circle, s.R
		in = func(p Point) bool { return dist(p, c) <= r }
	case s.Polygon != nil:
		in = func(p Point) bool { return inPolygon(p, s.Polygon) }
	}
	// the candidates: wall cells of team 1's half next to walkable ground, row by row, with the direction into the wall
	type cand struct {
		at, into Point
	}
	var cands []cand
	cell := ly.Size / Grid
	for j := 0; j < Grid; j++ {
		for i := 0; i < Grid; i++ {
			if ly.Kind[j*Grid+i] != Wall {
				continue
			}
			c := ly.CellCentre(i, j)
			if c[0]+c[1] > 0 || !in(c) {
				continue
			}
			edge := false
			var v Point
			for dj := -2; dj <= 2; dj++ {
				for di := -2; di <= 2; di++ {
					ii, jj := i+di, j+dj
					if ii < 0 || jj < 0 || ii >= Grid || jj >= Grid || !Walkable(ly.Kind[jj*Grid+ii]) {
						continue
					}
					if (di == 0) != (dj == 0) && di*di+dj*dj == 1 {
						edge = true
					}
					v[0] -= float64(di)
					v[1] -= float64(dj)
				}
			}
			if !edge {
				continue
			}
			if n := math.Hypot(v[0], v[1]); n > 0 {
				v = Point{v[0] / n, v[1] / n}
			}
			cands = append(cands, cand{at: c, into: v})
		}
	}
	rng := &splitmix{s: s.Seed}
	for i := len(cands) - 1; i > 0; i-- {
		j := int(rng.next() % uint64(i+1))
		cands[i], cands[j] = cands[j], cands[i]
	}
	var taken []Point // team 1's and their mirrors
	var out []Placed
	n := 0
	for _, c := range cands {
		if n >= most {
			break
		}
		jx, jy := (rng.float()-0.5)*cell, (rng.float()-0.5)*cell
		noun := s.Nouns[rng.next()%uint64(len(s.Nouns))]
		yaw := round3(rng.float()*360 - 180)
		scale := round3(lo + rng.float()*(hi-lo))
		p := Point{round3(c.at[0] + float64(c.into[0]*inset) + jx), round3(c.at[1] + float64(c.into[1]*inset) + jy)}
		if dist(p, Mirror(p)) < spacing {
			continue
		}
		ok := true
		for _, q := range taken {
			if dist(p, q) < spacing {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		taken = append(taken, p, Mirror(p))
		pl := Placed{Noun: noun, Pos: [3]float64{p[0], p[1], 0}, Rot: [3]float64{0, 0, yaw}, Scale: scale,
			Path: fmt.Sprintf("scatter[%d]", k), Shadow: s.Shadow, key: fmt.Sprintf("scatter/%d/%d", k, n)}
		if b, ok := cm.Map.nounBounds[nounKey(noun)]; ok {
			pl.box = &b
		}
		out = append(out, sides(pl)...)
		n++
	}
	return out
}

// Placements are the props the map places without reading the copy: the single props and the scatter, both sides
// (a group stands as one entry at its place, its level as Group; Build expands it).
func (cm *Compiled) Placements() []Placed {
	var out []Placed
	for k, p := range cm.Map.Props {
		if p.Group != nil {
			out = append(out, placeSides(p, Placed{Group: p.Group.Level, Pos: p.Pos, Rot: [3]float64{0, 0, normDeg(p.Rot)},
				Scale: propScale(p.Scale), Path: fmt.Sprintf("props[%d]", k), key: fmt.Sprintf("prop/%d", k)})...)
			continue
		}
		out = append(out, cm.single(k)...)
	}
	for k := range cm.Map.Scatter {
		out = append(out, cm.Scattered(k)...)
	}
	return out
}

// group is props[k]'s group placed on both sides: each member's marker moved, turned and scaled about the group's
// point. Members whose noun isn't dressing (PropTypes) are left out and counted.
func (ps *propSource) group(cm *Compiled, k int) ([]Placed, int, error) {
	p := cm.Map.Props[k]
	g := p.Group
	s := propScale(p.Scale)
	var members []*assetdata.Object
	for _, set := range g.Sets {
		o, err := ps.object(g.Level + "_" + set + ".Markerset")
		if err != nil {
			return nil, 0, fmt.Errorf("props[%d].group: %v", k, err)
		}
		for _, m := range markers(o) {
			if mp := vec(m, "pos"); len(mp) == 3 && g.takes(Point{mp[0], mp[1]}) {
				members = append(members, m)
			}
		}
	}
	var out []Placed
	left := 0
	for i, m := range members {
		info := ps.noun(text(m, "nounDef"))
		if info == nil || !isPropType(info.typ) {
			left++
			continue
		}
		mp, mr := vec(m, "pos"), vec(m, "rotDegrees")
		if len(mr) != 3 {
			mr = []float64{0, 0, 0}
		}
		rel := rotate(Point{(mp[0] - g.At[0]) * s, (mp[1] - g.At[1]) * s}, p.Rot)
		pl := Placed{Noun: info.name, Pos: [3]float64{round3(p.Pos[0] + rel[0]), round3(p.Pos[1] + rel[1]),
			round3(float64(mp[2]*s) + p.Pos[2])}, Rot: [3]float64{mr[0], mr[1], round3(normDeg(mr[2] + p.Rot))},
			Scale: round3(fval(mustGet(m, "scale")) * s), Path: fmt.Sprintf("props[%d]", k),
			key: fmt.Sprintf("prop/%d/%d", k, i), tmpl: m}
		if p.Once() || atCentre(Point{p.Pos[0], p.Pos[1]}) {
			pl.Team = 0
			out = append(out, pl)
			continue
		}
		pls, err := ps.waterOnce(sides(pl))
		if err != nil {
			return nil, 0, err
		}
		out = append(out, pls...)
	}
	return out, left, nil
}

// shadow is the shadow card under a placed prop: the shadow noun at its place, turned with it, as wide as
// shadowSpread times the prop's box; false when either box is unknown.
func (ps *propSource) shadow(p Placed) (Placed, bool) {
	sh := ps.noun(ShadowNoun)
	if p.box == nil || sh == nil || sh.box == nil {
		return Placed{}, false
	}
	w := math.Max(p.box.Max[0]-p.box.Min[0], p.box.Max[1]-p.box.Min[1]) * p.Scale
	sw := math.Max(sh.box.Max[0]-sh.box.Min[0], sh.box.Max[1]-sh.box.Min[1])
	if !(sw > 0) {
		return Placed{}, false
	}
	return Placed{Noun: sh.name, Pos: [3]float64{p.Pos[0], p.Pos[1], 0}, Rot: [3]float64{0, 0, p.Rot[2]},
		Scale: round3(shadowSpread * w / sw), Team: p.Team, Path: p.Path, Card: true, key: p.key + "/shadow"}, true
}

// PlacedProps are every marker the props markerset holds, in its order: per props entry (a noun's both sides, a
// group's members), then each scatter's; each prop with a shadow followed by its card. The findings are the
// placing's warnings (props.group: members left out; props.shadow: no box) and errors (props.type: a noun that
// isn't dressing).
func (cm *Compiled) PlacedProps(assetData []byte) ([]Placed, []Finding, error) {
	src, err := newAssetSource(assetData)
	if err != nil {
		return nil, nil, err
	}
	return cm.placedProps(newPropSource(src))
}

func (cm *Compiled) placedProps(ps *propSource) ([]Placed, []Finding, error) {
	if cm.Map.nounBounds == nil && HasProps(cm.Map) {
		cm.Map.nounBounds = map[string]NounBounds{}
		for _, n := range propNouns(cm.Map) {
			if info := ps.noun(n); info != nil && info.box != nil {
				cm.Map.nounBounds[nounKey(n)] = *info.box
			}
		}
	}
	c := &checker{m: cm.Map}
	var out []Placed
	add := func(ps2 *propSource, pls []Placed) {
		for _, p := range pls {
			out = append(out, p)
			if p.Shadow {
				if sh, ok := ps2.shadow(p); ok {
					out = append(out, sh)
				} else {
					c.warnf("props.shadow", p.Path, pt(Point{p.Pos[0], p.Pos[1]}), "no shadow card under %s: its box "+
						"or the shadow card's is unknown", p.Noun)
				}
			}
		}
	}
	typeOK := func(path, n string, at Point) bool {
		info := ps.noun(n)
		switch {
		case info == nil:
			c.errorf("nouns.exist", path, pt(at), "the copy has no noun %s", n)
		case !isPropType(info.typ):
			c.errorf("props.type", path, pt(at), "%s is a %q noun; a prop is dressing (%s)", info.name, info.typ,
				strings.Join(PropTypes, ", "))
		default:
			return true
		}
		return false
	}
	for k, p := range cm.Map.Props {
		at := Point{p.Pos[0], p.Pos[1]}
		if p.Group != nil {
			pls, left, err := ps.group(cm, k)
			if err != nil {
				return nil, nil, err
			}
			if left > 0 {
				c.warnf("props.group", fmt.Sprintf("props[%d].group", k), pt(at), "%d markers that aren't dressing "+
					"left out of the group", left)
			}
			if len(pls) == 0 {
				c.warnf("props.group", fmt.Sprintf("props[%d].group", k), pt(at), "the group holds no marker")
			}
			add(ps, pls)
			continue
		}
		if !typeOK(fmt.Sprintf("props[%d].noun", k), p.Noun, at) {
			continue
		}
		pls := cm.single(k)
		info := ps.noun(p.Noun)
		for i := range pls {
			pls[i].Noun = info.name
		}
		pls, err := ps.waterOnce(pls)
		if err != nil {
			return nil, nil, err
		}
		add(ps, pls)
	}
	for k, s := range cm.Map.Scatter {
		ok := true
		for i, n := range s.Nouns {
			ok = typeOK(fmt.Sprintf("scatter[%d].nouns[%d]", k, i), n, Point{}) && ok
		}
		if !ok {
			continue
		}
		pls := cm.Scattered(k)
		for i := range pls {
			pls[i].Noun = ps.noun(pls[i].Noun).name
		}
		add(ps, pls)
	}
	return out, c.fs, nil
}

// props writes the props markerset: per placement a copy of its template (a group member's own marker, else the
// first shipped marker of the noun, else the shadow card's with the noun put in) at its place, turn and scale.
func (b *assetBuild) props() ([]Finding, error) {
	if !HasProps(b.cm.Map) {
		return nil, nil
	}
	ps := newPropSource(b.src)
	pls, fs, err := b.cm.placedProps(ps)
	if err != nil {
		return nil, err
	}
	var fallback *assetdata.Object
	for _, p := range pls {
		tm := p.tmpl
		if tm == nil {
			if tm, err = ps.template(p.Noun); err != nil {
				return nil, err
			}
		}
		if tm == nil {
			if fallback == nil {
				if fallback, err = ps.template(ShadowNoun); err != nil || fallback == nil {
					return nil, fmt.Errorf("no shipped marker of %s to place %s with", ShadowNoun, p.Noun)
				}
			}
			tm = fallback
		}
		if !strings.EqualFold(text(tm, "nounDef"), p.Noun) {
			tm = cloneObj(tm)
			tm.Set("nounDef", p.Noun)
		}
		m := b.place(setProps, tm, p.key, Point{p.Pos[0], p.Pos[1]})
		z := p.Pos[2]
		if p.Card {
			z = vec(tm, "pos")[2] // a shadow card lies at its template's height
		}
		m.Set("pos", f32s(p.Pos[0], p.Pos[1], z))
		m.Set("rotDegrees", f32s(p.Rot[0], p.Rot[1], p.Rot[2]))
		m.Set("scale", float32(p.Scale))
	}
	if len(b.sets[setProps]) == 0 {
		b.sets[setProps] = []any{}
	}
	return fs, nil
}

// PropNoun is a noun a prop may name (PropTypes), for a palette: its name, type, model (empty for a noun without
// one: a light, an effect), the graphicsScale the model is drawn at (times the marker's scale) and box (scaled by
// the graphicsScale).
type PropNoun struct {
	Name          string     `json:"name"`
	Type          string     `json:"type"`
	Model         string     `json:"model,omitempty"`
	GraphicsScale float64    `json:"graphicsScale"`
	Box           NounBounds `json:"box"`
}

// MarshalJSON writes the box as {"min": [x, y, z], "max": [...]}.
func (b NounBounds) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`{"min":[%g,%g,%g],"max":[%g,%g,%g]}`, b.Min[0], b.Min[1], b.Min[2], b.Max[0],
		b.Max[1], b.Max[2])), nil
}

// PropNouns are the copy's nouns a prop may name, by name (case-insensitive).
func PropNouns(assetData []byte) ([]PropNoun, error) {
	src, err := newAssetSource(assetData)
	if err != nil {
		return nil, err
	}
	ps := newPropSource(src)
	var out []PropNoun
	for _, e := range src.arch.Entries {
		if !strings.HasPrefix(e.Name, assetDir) || !strings.HasSuffix(strings.ToLower(e.Name), ".noun.bin") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(e.Name, assetDir), ".bin")
		info := ps.noun(name)
		if info == nil || !isPropType(info.typ) {
			continue
		}
		pn := PropNoun{Name: info.name, Type: info.typ, Model: info.model, GraphicsScale: info.scale}
		if info.box != nil {
			pn.Box = *info.box
		}
		out = append(out, pn)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// setsOf are the map's markersets (suffixes) in the Level's order: the props markerset last when it has props.
func setsOf(m *Map) []string {
	if !HasProps(m) {
		return mapSets
	}
	return append(append([]string{}, mapSets...), setProps)
}

// MapAssetNames are the assets a map adds (AssetNames, and its props markerset when it has props).
func MapAssetNames(m *Map) []string {
	out := []string{m.Name + ".Level"}
	for _, s := range setsOf(m) {
		out = append(out, m.Name+"_"+s+".Markerset")
	}
	return append(out, TerrainNoun(m.Name))
}
