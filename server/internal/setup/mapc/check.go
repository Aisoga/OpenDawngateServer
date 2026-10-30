package mapc

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"dawngate/server/internal/setup/mapmm/navgraph"
)

// Finding is one failed rule: its id, error (the build refuses the map) or warning, where in map.json it is (a path
// such as "base.spawns[2]" or "terrain.shapes[4]"; empty for the whole map), a world position when it has one, and
// the message.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	At       *Point `json:"at,omitempty"`
	Message  string `json:"message"`

	Parts []DroppedPart `json:"parts,omitempty"` // reach.dropped: the cut-off parts on team 1's half (dropped.go)
}

// Severities.
const (
	Error   = "error"
	Warning = "warning"
)

// The rules' limits.
const (
	MaxNameLen    = 80 // ParseLevel's
	MaxDisplayLen = 13 // the create dialog's map list shows about this many characters
	MaxPrefixLen  = 16
	MaxSize       = 600.0 // the host's level-size check gives an unknown level +-300 m
	MinLaneWidth  = 6.0
	MaxLaneWidth  = 80.0
	// MaxKnots: the match host fills the server's spline table with at most 16 knots a lane; a longer lane's
	// minions get no spline, call their lane done at the spawn and walk straight at the enemy base (MAPED5)
	MaxKnots     = 16
	MaxWells     = 8
	BindingRange = 31.0 // the radius of a Binding's tag (MAPTAGS' fit on SM_Map_v5); its lane must lie inside it
	LaneEndReach = 25.0 // a lane's last knot from an enemy Core or Guardian (SM_Map_v5's: 16.2 and 16.6 m)
	MinBushCells = 6
	EdgeCells    = 4 // the border of cells that should be wall
	edgeMargin   = 2.0
	symTolerance = 0.5
)

// SharedPath is how far apart two lanes may run and still be one path: the match host's tolerance
// (gms/core/bots/lanes.h LanesShareOnePath: every knot of each lane within it of the other lane).
const SharedPath = 60.0

// LanesShareOnePath tells whether a map's two lanes are one path as the match host decides it; then the host takes
// every Binding as standing in every lane, and one lane's Bindings are enough (fixed.bindings).
func LanesShareOnePath(ls []Lane) bool {
	if len(ls) != 2 || len(ls[0].Knots) < 2 || len(ls[1].Knots) < 2 {
		return false
	}
	return laneGap(ls[0].Knots, ls[1].Knots) <= SharedPath && laneGap(ls[1].Knots, ls[0].Knots) <= SharedPath
}

// ShippedLevels are the game's Level assets: a map can't take one's name.
var ShippedLevels = []string{"DGTut_Map_v1", "LootBagPresentation_Map_Defeat", "LootBagPresentation_Map_Victory",
	"MM_Map_v3", "SM_Map_v4", "SM_Map_v5", "SS_Map_v1", "Shaper_Gallery", "TA_Map_v1", "showroomCharacters",
	"showroomObjects", "test_VFX_arena"}

// ReservedTags are the map tags the server's own maps use (SM_Map_v5's and MM_Map_v3's).
var ReservedTags = []string{"sm", "mm"}

// SharedLevels are the shipped levels a map may take its environment and its tuning from.
var SharedLevels = []string{"SM_Map_v5", "MM_Map_v3"}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	tagRe  = regexp.MustCompile(`^[a-z0-9]+$`)
)

// Report is Check's result.
type Report struct {
	Map      string    `json:"map"`
	OK       bool      `json:"ok"` // no error
	Findings []Finding `json:"findings"`
	Stats    *Stats    `json:"stats,omitempty"`
}

// Stats are the compiled map's numbers.
type Stats struct {
	Cell         int     `json:"cell"`
	PlayerAreas  int     `json:"playerAreas"`
	NPCAreas     int     `json:"npcAreas"`
	OpenCells    int     `json:"openCells"`
	ChasmCells   int     `json:"chasmCells"`
	IslandCells  int     `json:"islandCells,omitempty"` // ground only NPCs walk (MAPED-ISLAND)
	Bushes       int     `json:"bushes"`
	Wells        int     `json:"wells"`
	Camps        int     `json:"camps"` // on the map: team 1's mirrored, the parasite once (MAPED3)
	LaneLengths  []int   `json:"laneLengths"`
	DroppedCells int     `json:"droppedCells"` // walkable cells the Player graph leaves out (unreachable pockets)
	CellSize     float64 `json:"cellSize"`
	WaterPlanes  int     `json:"waterPlanes,omitempty"` // the chasms' water planes (water.go)
}

type checker struct {
	m  *Map
	fs []Finding
}

func (c *checker) add(rule, sev, path string, at *Point, format string, args ...any) {
	var p *Point
	if at != nil {
		q := *at
		p = &q
	}
	c.fs = append(c.fs, Finding{Rule: rule, Severity: sev, Path: path, At: p, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) errorf(rule, path string, at *Point, format string, args ...any) {
	c.add(rule, Error, path, at, format, args...)
}

func (c *checker) warnf(rule, path string, at *Point, format string, args ...any) {
	c.add(rule, Warning, path, at, format, args...)
}

func (c *checker) errors() int {
	n := 0
	for _, f := range c.fs {
		if f.Severity == Error {
			n++
		}
	}
	return n
}

// Other is another installed map, for the uniqueness rules.
type Other struct {
	Name, Tag string
}

// Check runs every rule on a map: first the structural ones (each value on its own), then, when they pass, the
// geometric ones on the compiled map (Compile). others are the other installed maps (their names and tags must differ).
func Check(m *Map, others []Other) (*Report, *Compiled) {
	c := &checker{m: m}
	c.structure(others)
	var cm *Compiled
	if c.errors() == 0 {
		var err error
		cm, err = Compile(m)
		if err != nil {
			c.errorf("format.compile", "", nil, "%v", err)
			cm = nil
		} else {
			c.geometry(cm)
			c.graphGeometry(cm) // graph.go
			c.waterGeometry(cm) // water.go
			c.groundFiles()
			c.voidFiles() // void.go
		}
	}
	c.wards(cm) // wards.go
	r := &Report{Map: m.Name, Findings: c.fs}
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	r.OK = c.errors() == 0
	if cm != nil {
		r.Stats = cm.stats()
	}
	return r, cm
}

func inList(s string, list []string, fold bool) bool {
	for _, x := range list {
		if x == s || (fold && strings.EqualFold(x, s)) {
			return true
		}
	}
	return false
}

func pt(p Point) *Point { return &p }

// structure runs the rules that need no rasterising.
func (c *checker) structure(others []Other) {
	m := c.m
	if m.Format != Format {
		c.errorf("format.version", "format", nil, "format %d, this compiler reads format %d", m.Format, Format)
	}
	// names
	switch {
	case !nameRe.MatchString(m.Name):
		c.errorf("names.name", "name", nil, "name %q: only letters, digits and _", m.Name)
	case len(m.Name) > MaxNameLen:
		c.errorf("names.name", "name", nil, "name: %d characters, at most %d", len(m.Name), MaxNameLen)
	case inList(m.Name, ShippedLevels, true):
		c.errorf("names.shipped", "name", nil, "name %q is a shipped level's", m.Name)
	}
	if !tagRe.MatchString(m.Tag) {
		c.errorf("names.tag", "tag", nil, "tag %q: only a-z and 0-9, at least one", m.Tag)
	} else if inList(m.Tag, ReservedTags, false) {
		c.errorf("names.tag-unique", "tag", nil, "tag %q is a built-in map's", m.Tag)
	}
	for _, o := range others {
		if strings.EqualFold(o.Name, m.Name) {
			c.errorf("names.unique", "name", nil, "another installed map is called %s", o.Name)
		}
		if o.Tag == m.Tag {
			c.errorf("names.tag-unique", "tag", nil, "the installed map %s has the tag %q too", o.Name, m.Tag)
		}
	}
	if n := utf8.RuneCountInString(m.Display); n == 0 || n > MaxDisplayLen {
		c.errorf("names.display", "display", nil, "display %q: %d characters, 1 to %d", m.Display, n, MaxDisplayLen)
	}
	if n := utf8.RuneCountInString(m.Prefix); n > MaxPrefixLen {
		c.errorf("names.prefix", "prefix", nil, "prefix: %d characters, at most %d", n, MaxPrefixLen)
	}
	// size, references, symmetry kind, ground
	if !(m.Size > 0 && m.Size <= MaxSize) {
		c.errorf("size.max", "size", nil, "size %g: more than 0 and at most %g", m.Size, MaxSize)
	}
	if m.Symmetry != "point" {
		c.errorf("symmetry.kind", "symmetry", nil, "symmetry %q: format 1 knows \"point\"", m.Symmetry)
	}
	if !inList(m.Environment, SharedLevels, false) {
		c.errorf("refs.environment", "environment", nil, "environment %q: one of %s", m.Environment,
			strings.Join(SharedLevels, ", "))
	}
	if !inList(m.Tuning, SharedLevels, false) {
		c.errorf("refs.tuning", "tuning", nil, "tuning %q: one of %s", m.Tuning, strings.Join(SharedLevels, ", "))
	}
	c.ground()     // groundtex.go
	c.propRules()  // props.go
	c.waterRules() // water.go
	c.voidRules()  // void.go
	c.graphRules() // graph.go
	// lanes
	if n := len(m.Lanes); n < 1 || n > 2 {
		c.errorf("lanes.count", "lanes", nil, "%d lanes, 1 or 2", n)
	}
	seen := map[int]bool{}
	for k, ln := range m.Lanes {
		path := fmt.Sprintf("lanes[%d]", k)
		if ln.ID != 1 && ln.ID != 2 {
			c.errorf("lanes.ids", path+".id", nil, "lane id %d: 1 (Top) or 2 (Bottom)", ln.ID)
		} else if seen[ln.ID] {
			c.errorf("lanes.ids", path+".id", nil, "lane id %d twice", ln.ID)
		}
		seen[ln.ID] = true
		if !(ln.Width >= MinLaneWidth && ln.Width <= MaxLaneWidth) {
			c.errorf("lanes.width", path+".width", nil, "width %g: %g to %g", ln.Width, MinLaneWidth, MaxLaneWidth)
		}
		if len(ln.Knots) < 2 {
			c.errorf("lanes.knots", path+".knots", nil, "%d knots, at least 2", len(ln.Knots))
		} else if len(ln.Knots) > MaxKnots {
			c.errorf("lanes.knots", path+".knots", nil, "%d knots, at most %d (the match host's lane table: the "+
				"minions of a longer lane walk straight at the enemy base)", len(ln.Knots), MaxKnots)
		}
		for i := 0; i+1 < len(ln.Knots); i++ {
			if dist(ln.Knots[i], ln.Knots[i+1]) < 1 {
				c.errorf("lanes.knots", fmt.Sprintf("%s.knots[%d]", path, i+1), pt(ln.Knots[i+1]),
					"knot %d lies on knot %d", i+1, i)
			}
		}
	}
	// the fixed set
	b := m.Base
	if len(b.Spawns) != 5 {
		c.errorf("fixed.spawns", "base.spawns", nil, "%d spawn points, 5", len(b.Spawns))
	}
	if n := len(b.Cores.Points); !b.Cores.Auto && n != CoreCounts[0] && n != CoreCounts[1] {
		c.errorf("fixed.cores", "base.cores", nil, "%d Cores, 5 (A to E), 3 (A to C, MM_Map_v3's set) or \"auto\"", n)
	}
	if !b.Bindings.Auto {
		keys := make([]string, 0, len(b.Bindings.Lanes))
		for k := range b.Bindings.Lanes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		count := -1 // every lane the same tiers
		for _, k := range keys {
			id, err := strconv.Atoi(k)
			n := len(b.Bindings.Lanes[k])
			if err != nil || !seen[id] {
				c.errorf("fixed.bindings", "base.bindings."+k, nil, "Bindings for lane %q, which the map doesn't have", k)
			} else if n != 3 && n != 2 {
				c.errorf("fixed.bindings", "base.bindings."+k, nil, "%d Bindings on lane %s, 3 (Outer, Middle, Inner) "+
					"or 2 (Middle, Inner: MM_Map_v3's tiers)", n, k)
			} else if count >= 0 && n != count {
				c.errorf("fixed.bindings", "base.bindings."+k, nil, "%d Bindings on lane %s, %d on the other: every "+
					"lane has the same tiers", n, k, count)
			} else {
				count = n
			}
		}
		missing := 0
		for _, ln := range m.Lanes {
			if _, ok := b.Bindings.Lanes[strconv.Itoa(ln.ID)]; !ok {
				missing++
			}
		}
		// one lane's Bindings are enough when the lanes are one path (MAPED-IMPORT: MM_Map_v3's two Bindings a team)
		if missing > 0 && (missing == len(m.Lanes) || !LanesShareOnePath(m.Lanes)) {
			for _, ln := range m.Lanes {
				if _, ok := b.Bindings.Lanes[strconv.Itoa(ln.ID)]; !ok {
					c.errorf("fixed.bindings", "base.bindings", nil, "no Bindings for lane %d (only lanes that are "+
						"one path, at most %.0f m apart, may leave one lane without)", ln.ID, SharedPath)
				}
			}
		}
	}
	if n := 2 * len(m.Wells); n < 1 || n > MaxWells {
		c.errorf("budget.wells", "wells", nil, "%d wells on the map (each authored one is mirrored), 1 to %d", n,
			MaxWells)
	}
	for k, w := range m.Wells {
		if w.Kind != "" && !inList(w.Kind, WellKinds, false) {
			c.errorf("wells.kind", fmt.Sprintf("wells[%d].kind", k), pt(w.Pos), "kind %q: %s (or none, a well)",
				w.Kind, strings.Join(WellKinds, " or "))
		}
	}
	for k, cp := range m.Camps {
		path := fmt.Sprintf("camps[%d]", k)
		if !inList(cp.Kind, []string{"camp", "buff", "parasite"}, false) {
			c.errorf("camps.kind", path+".kind", pt(cp.Pos), "kind %q: camp, buff or parasite", cp.Kind)
		}
		if cp.Noun == "" {
			c.errorf("camps.noun", path+".noun", pt(cp.Pos), "a camp names its spawner noun")
		}
	}
	c.campRules() // camps.go
	// terrain
	if _, ok := kinds[m.Terrain.Default]; !ok {
		c.errorf("terrain.default", "terrain.default", nil, "default %q: wall, open or chasm", m.Terrain.Default)
	}
	for k, s := range m.Terrain.Shapes {
		path := fmt.Sprintf("terrain.shapes[%d]", k)
		if _, ok := shapeKinds[s.Type]; !ok && s.Type != "bush" {
			c.errorf("terrain.shape", path+".type", nil, "type %q: open, wall, chasm, island or bush", s.Type)
		}
		g, err := geom(m, s)
		if err != nil {
			c.errorf("terrain.shape", path, nil, "%v", err)
			continue
		}
		if s.Type == "bush" && g.circle == nil && g.polygon == nil {
			c.errorf("terrain.shape", path, nil, "a bush is a circle or a polygon")
		}
		if s.Type == "island" && s.Lane != 0 {
			c.errorf("terrain.shape", path, nil, "an island is a circle, a polygon or a path, not a lane")
		}
	}
	if c.errors() > 0 {
		return
	}
	c.bounds()
	c.symmetry()
}

// authored are the authored points with their paths (team 1's).
func (c *checker) authored() (paths []string, pts []Point) {
	m := c.m
	add := func(path string, p Point) { paths, pts = append(paths, path), append(pts, p) }
	add("base.locus", m.Base.Locus)
	add("base.guardian", m.Base.Guardian)
	add("base.shop", m.Base.Shop)
	for k, p := range m.Base.Spawns {
		add(fmt.Sprintf("base.spawns[%d]", k), p)
	}
	for k, p := range m.Base.Cores.Points {
		add(fmt.Sprintf("base.cores[%d]", k), p)
	}
	keys := make([]string, 0, len(m.Base.Bindings.Lanes))
	for k := range m.Base.Bindings.Lanes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for i, p := range m.Base.Bindings.Lanes[k] {
			add(fmt.Sprintf("base.bindings.%s[%d]", k, i), p)
		}
	}
	for k, w := range m.Wells {
		add(fmt.Sprintf("wells[%d].pos", k), w.Pos)
	}
	for k, cp := range m.Camps {
		add(fmt.Sprintf("camps[%d].pos", k), cp.Pos)
	}
	return
}

func (c *checker) bounds() {
	m := c.m
	lim := m.Size/2 - edgeMargin
	out := func(p Point) bool { return math.Abs(p[0]) > lim || math.Abs(p[1]) > lim }
	paths, pts := c.authored()
	for k, ln := range m.Lanes {
		for i, p := range ln.Knots {
			paths, pts = append(paths, fmt.Sprintf("lanes[%d].knots[%d]", k, i)), append(pts, p)
		}
	}
	for i, p := range pts {
		if out(p) {
			c.errorf("size.bounds", paths[i], pt(p), "(%g, %g) lies outside the level (+-%g m, %g m from its edge)",
				p[0], p[1], m.Size/2, edgeMargin)
		}
	}
}

func (c *checker) symmetry() {
	m := c.m
	paths, pts := c.authored()
	for i, p := range pts {
		if p[0]+p[1] > 0 {
			c.errorf("symmetry.half", paths[i], pt(p), "(%g, %g) lies in team 2's half (x + y > 0); author team 1's "+
				"half only, team 2's is mirrored", p[0], p[1])
		}
	}
	g := m.Base.Guardian
	if !(g[0] < 0 && g[1] < 0) {
		c.errorf("symmetry.base", "base.guardian", pt(g), "team 1's base belongs in the south-west (x < 0, y < 0)")
	}
	for k, s := range m.Terrain.Shapes {
		if s.Lane != 0 {
			continue
		}
		gm, _ := geom(m, s)
		bb := gm.bounds()
		if cx, cy := (bb[0]+bb[2])/2, (bb[1]+bb[3])/2; cx+cy > symTolerance {
			c.errorf("symmetry.half", fmt.Sprintf("terrain.shapes[%d]", k), &Point{cx, cy},
				"the shape's centre (%.1f, %.1f) lies in team 2's half; draw team 1's half and the middle", cx, cy)
		}
	}
	// the lanes: the mirror of each lane, walked backwards, is a lane (the other with two lanes, itself with one)
	for k, ln := range m.Lanes {
		var other *Lane
		for j := range m.Lanes {
			if len(m.Lanes) == 1 || j != k {
				other = &m.Lanes[j]
			}
		}
		want := reversed(mirrorAll(ln.Knots))
		path := fmt.Sprintf("lanes[%d]", k)
		if len(other.Knots) != len(want) {
			c.errorf("symmetry.lanes", path, nil, "lane %d's mirror has %d knots, lane %d %d", ln.ID, len(want),
				other.ID, len(other.Knots))
			continue
		}
		for i, p := range want {
			if dist(p, other.Knots[i]) > symTolerance {
				c.errorf("symmetry.lanes", fmt.Sprintf("lanes[%d].knots[%d]", k, len(want)-1-i),
					pt(ln.Knots[len(want)-1-i]), "lane %d mirrored and reversed gives (%g, %g) as lane %d's knot %d, "+
						"which is (%g, %g)", ln.ID, p[0], p[1], other.ID, i, other.Knots[i][0], other.Knots[i][1])
				break
			}
		}
		if other.Width != ln.Width {
			c.errorf("symmetry.lanes", path+".width", nil, "lane %d is %g wide, its mirror lane %d %g", ln.ID,
				ln.Width, other.ID, other.Width)
		}
	}
}

// object is a resolved object of team 1 to check: its name, its path in map.json (or the derived thing's) and place.
type object struct {
	what, path string
	p          Point
}

func (cm *Compiled) objects() []object {
	t := &cm.Layout.Teams[0]
	m := cm.Map
	obs := []object{{"the Locus", "base.locus", t.Locus}, {"the Guardian", "base.guardian", t.Guardian},
		{"the shop", "base.shop", t.Shop}}
	for k, p := range t.Spawns {
		obs = append(obs, object{fmt.Sprintf("spawn point %d", k), fmt.Sprintf("base.spawns[%d]", k), p})
	}
	for k, p := range t.Cores {
		path := "base.cores"
		if !m.Base.Cores.Auto {
			path = fmt.Sprintf("base.cores[%d]", k)
		}
		obs = append(obs, object{fmt.Sprintf("Core %c", 'A'+k), path, p})
	}
	for _, id := range cm.Layout.LaneIDs {
		for k, p := range t.Bindings[id] {
			path := "base.bindings"
			if !m.Base.Bindings.Auto {
				path = fmt.Sprintf("base.bindings.%d[%d]", id, k)
			}
			obs = append(obs, object{fmt.Sprintf("lane %d's %s Binding", id, TierNames(len(t.Bindings[id]))[k]), path,
				p})
		}
		obs = append(obs, object{fmt.Sprintf("lane %d's minion spawner", id), cm.lanePath(id), t.Spawners[id]})
	}
	for k, p := range t.Wells {
		obs = append(obs, object{fmt.Sprintf("well %d", k), fmt.Sprintf("wells[%d].pos", k), p})
	}
	for k, cp := range t.Camps {
		obs = append(obs, object{fmt.Sprintf("camp %d", k), fmt.Sprintf("camps[%d].pos", k), cp.Pos})
	}
	return obs
}

func (cm *Compiled) lanePath(id int) string {
	for k, ln := range cm.Map.Lanes {
		if ln.ID == id {
			return fmt.Sprintf("lanes[%d]", k)
		}
	}
	return "lanes"
}

func (cm *Compiled) laneWidth(id int) float64 {
	for _, ln := range cm.Map.Lanes {
		if ln.ID == id {
			return ln.Width
		}
	}
	return 0
}

// geometry runs the rules on the compiled map (team 1's side; team 2's is its mirror).
func (c *checker) geometry(cm *Compiled) {
	t := &cm.Layout.Teams[0]
	ly := cm.Layers
	obs := cm.objects()
	// every object on open ground, none in the Guardian's hole
	for _, o := range obs {
		if k := ly.KindAt(o.p); k != Open && !(k == Island && strings.HasPrefix(o.what, "well")) {
			c.errorf("fixed.open", o.path, pt(o.p), "%s stands on %s ground", o.what, kindName(k))
		}
		if o.what == "the Guardian" || strings.HasPrefix(o.what, "Core") {
			continue
		}
		for _, g := range []Point{t.Guardian, cm.Layout.Teams[1].Guardian} {
			if d := dist(o.p, g); d < HoleRadius+ly.Size/Grid*float64(cm.Cell) {
				c.errorf("fixed.hole", o.path, pt(o.p), "%s stands %.1f m from a Guardian, inside its platform's hole",
					o.what, d)
			}
		}
	}
	// the Bindings: in range of their lane, tiers in order, the Outer one on the own half of the lane
	for _, id := range cm.Layout.LaneIDs {
		knots, total := t.Lanes[id], length(t.Lanes[id])
		bs := t.Bindings[id]
		tiers := TierNames(len(bs))
		at := make([]float64, len(bs))
		for k, p := range bs {
			s, d := project(knots, p)
			at[k] = s
			path := cm.objPath(fmt.Sprintf("lane %d's %s Binding", id, tiers[k]), obs)
			if d+cm.laneWidth(id)/2 > BindingRange {
				c.errorf("fixed.range", path, pt(p), "lane %d's %s Binding stands %.1f m from the lane: with the "+
					"corridor's half width %.1f m it reaches past its %g m", id, tiers[k], d, cm.laneWidth(id)/2,
					BindingRange)
			}
		}
		if len(bs) == 0 {
			continue
		}
		last := len(bs) - 1
		for k := 0; k < last; k++ {
			if !(at[k+1] < at[k]) {
				parts := make([]string, len(bs))
				for i := range bs {
					parts[last-i] = fmt.Sprintf("%.0f (%s)", at[i], tiers[i])
				}
				c.errorf("fixed.tiers", cm.objPath(fmt.Sprintf("lane %d's Inner Binding", id), obs), pt(bs[last]),
					"lane %d's Bindings lie at %s m along it: %s from the own base", id, strings.Join(parts, ", "),
					strings.Join(reversedStrings(tiers), ", "))
				break
			}
		}
		if at[0] >= total/2 {
			c.errorf("fixed.tiers", cm.objPath(fmt.Sprintf("lane %d's %s Binding", id, tiers[0]), obs), pt(bs[0]),
				"lane %d's %s Binding lies past the lane's middle (%.0f of %.0f m)", id, tiers[0], at[0], total)
		}
	}
	// the lanes on the NPC graph
	npcComp := cm.NPC.Components()
	plComp := cm.Player.Components()
	compAt := func(b *Blocks, comp []int, p Point) int {
		bx, by := cm.BlockAt(p)
		if i := b.Near(bx, by, 1); i >= 0 {
			return comp[i]
		}
		return -1
	}
	for _, id := range cm.Layout.LaneIDs {
		knots := t.Lanes[id]
		lp := cm.lanePath(id)
		for k, p := range knots {
			if compAt(&cm.NPC, npcComp, p) < 0 {
				c.errorf("lanes.graph", fmt.Sprintf("%s.knots[%d]", lp, k), pt(p), "lane %d's knot %d is off the NPC "+
					"graph", id, k)
			}
		}
		step := ly.Size / Grid
		for k := 0; k+1 < len(knots); k++ {
			l := dist(knots[k], knots[k+1])
			for s := 0.0; s <= l; s += step {
				q := Point{knots[k][0] + (knots[k+1][0]-knots[k][0])*s/l, knots[k][1] + (knots[k+1][1]-knots[k][1])*s/l}
				if compAt(&cm.NPC, npcComp, q) < 0 {
					c.errorf("lanes.graph", fmt.Sprintf("%s.knots[%d]", lp, k+1), pt(q), "lane %d's leg from knot %d to "+
						"%d leaves the NPC graph at (%.1f, %.1f)", id, k, k+1, q[0], q[1])
					break
				}
			}
		}
		sp := t.Spawners[id]
		if a, b := compAt(&cm.NPC, npcComp, sp), compAt(&cm.NPC, npcComp, knots[0]); a < 0 || a != b {
			c.errorf("lanes.start", lp, pt(sp), "lane %d's minion spawner (%.1f, %.1f, %g m behind its first knot) "+
				"can't reach the first knot on the NPC graph", id, sp[0], sp[1], SpawnerBack)
		}
		last := knots[len(knots)-1]
		enemy := &cm.Layout.Teams[1]
		near := dist(last, enemy.Guardian)
		for _, p := range enemy.Cores {
			near = math.Min(near, dist(last, p))
		}
		if near > LaneEndReach {
			c.errorf("lanes.end", fmt.Sprintf("%s.knots[%d]", lp, len(knots)-1), pt(last), "lane %d ends %.1f m from "+
				"the enemy's nearest Core or Guardian; at most %g m, or its minions never turn on the base", id, near,
				LaneEndReach)
		}
	}
	// reachability on the Player graph from both bases
	home := compAt(&cm.Player, plComp, t.Spawns[0])
	if away := compAt(&cm.Player, plComp, cm.Layout.Teams[1].Spawns[0]); home < 0 || home != away {
		c.errorf("reach.islands", "base.spawns[0]", pt(t.Spawns[0]), "the two bases' spawn points aren't connected "+
			"on the Player graph")
	}
	for _, o := range obs {
		if o.what == "the Guardian" || strings.HasPrefix(o.what, "camp") ||
			(strings.HasPrefix(o.what, "well") && ly.KindAt(o.p) == Island) {
			continue
		}
		if compAt(&cm.Player, plComp, o.p) != home || home < 0 {
			c.errorf("reach.player", o.path, pt(o.p), "%s can't be reached from the bases on the Player graph", o.what)
		}
	}
	c.islandWells(cm)
	for k, w := range t.Wells {
		for a := 0; a < 8; a++ {
			q := Point{w[0] + 8*math.Cos(float64(a)*math.Pi/4), w[1] + 8*math.Sin(float64(a)*math.Pi/4)}
			if compAt(&cm.NPC, npcComp, q) < 0 {
				c.errorf("reach.npc", fmt.Sprintf("wells[%d].pos", k), pt(q), "well %d's workers' ring (8 m) leaves the "+
					"NPC graph at (%.1f, %.1f)", k, q[0], q[1])
				break
			}
		}
	}
	for k, cp := range t.Camps {
		if compAt(&cm.NPC, npcComp, cp.Pos) < 0 {
			c.errorf("reach.npc", fmt.Sprintf("camps[%d].pos", k), pt(cp.Pos), "camp %d is off the NPC graph", k)
		}
	}
	if st := cm.stats(); st.DroppedCells > 0 {
		c.dropped(cm, st) // dropped.go: at the largest cut-off part, every part in the finding's parts
	}
	// the level's edge: the ground model and the PhysX mesh end there, so open ground must stop short of it
	edge := 0
	var edgeAt *Point
	for k, v := range ly.Kind {
		i, j := k%Grid, k/Grid
		if v != Wall && min(i, j, Grid-1-i, Grid-1-j) < EdgeCells {
			edge++
			if edgeAt == nil {
				edgeAt = pt(ly.CellCentre(i, j))
			}
		}
	}
	if edge > 0 {
		c.warnf("terrain.edge", "terrain", edgeAt, "%d open or chasm cells lie within %d cells of the level's edge, "+
			"where the ground model runs on flat; keep a wall along the edge", edge, EdgeCells)
	}
	// budgets
	if (cm.Player.Areas >= AreaBudget || cm.NPC.Areas >= AreaBudget) && c.m.graphCell() == 0 { // a set cell: graph.areas
		c.errorf("budget.areas", "terrain", nil, "the graphs have %d (Player) and %d (NPC) areas at the coarsest "+
			"grid (%d cells a square), under %d each", cm.Player.Areas, cm.NPC.Areas, cm.Cell, AreaBudget)
	}
	for _, b := range ly.Bushes {
		path := fmt.Sprintf("terrain.shapes[%d]", b.Shape)
		if b.Mirrored {
			continue
		}
		if len(b.Cells) < MinBushCells {
			c.errorf("budget.bush", path, pt(b.Outline[0]), "the bush covers %d cells, at least %d", len(b.Cells),
				MinBushCells)
		}
		open := 0
		for _, k := range b.Cells {
			if Walkable(ly.Kind[k]) {
				open++
			}
		}
		if 2*open < len(b.Cells) {
			c.warnf("budget.bush", path, pt(b.Outline[0]), "the bush lies mostly off walkable ground (%d of %d cells)",
				open, len(b.Cells))
		}
	}
}

func (cm *Compiled) objPath(what string, obs []object) string {
	for _, o := range obs {
		if o.what == what {
			return o.path
		}
	}
	return ""
}

func kindName(k byte) string {
	switch k {
	case Wall:
		return "wall"
	case Chasm:
		return "chasm"
	case Solid:
		return "blocked (a prop's)"
	case Island:
		return "island"
	}
	return "open"
}

// The reach.island rule's distances (metres): the match host's well capture radius (MAPMINES: a shaper in it
// captures), and the reach a shaper on the Player graph must keep from a well on an island so it can't hit the
// workers either: their spots stand about 22 m round the well, the longest dash is about 17 (MM_Map_v3: the Player
// graph's nearest square is 53 m from each mine).
const (
	IslandCaptureReach = 12.0
	IslandWorkerReach  = 39.0
)

// PlayerDistance is how near the Player graph's squares come to p (metres; +Inf without squares).
func (cm *Compiled) PlayerDistance(p Point) float64 {
	b := &cm.Player
	near := math.Inf(1)
	for by := 0; by < b.H; by++ {
		for bx := 0; bx < b.W; bx++ {
			if !b.On[by*b.W+bx] {
				continue
			}
			r := navgraph.BlockRect(bx, by, cm.Cell, Grid, Grid, cm.Map.Size, cm.Map.Size)
			dx := math.Max(0, math.Max(r[0]-p[0], p[0]-r[2]))
			dy := math.Max(0, math.Max(r[1]-p[1], p[1]-r[3]))
			near = math.Min(near, math.Hypot(dx, dy))
		}
	}
	return near
}

// islandWells checks each well on an island (reach.island): the players must not reach its capture ring (an error:
// a shaper could take the well while the bots, which the match host keeps off it, never contest it), nor come
// within IslandWorkerReach (a warning: a shaper could hit its workers).
func (c *checker) islandWells(cm *Compiled) {
	for _, w := range cm.IslandWells(0) {
		p := cm.Layout.Teams[0].Wells[w]
		near := cm.PlayerDistance(p)
		path := fmt.Sprintf("wells[%d].pos", w)
		switch {
		case near <= IslandCaptureReach:
			c.errorf("reach.island", path, pt(p), "well %d stands on an island, but the Player graph comes %.1f m "+
				"from it, inside its capture ring (%g m): shapers could take it, the bots never would", w, near,
				IslandCaptureReach)
		case near < IslandWorkerReach:
			c.warnf("reach.island", path, pt(p), "well %d stands on an island, but the Player graph comes %.1f m "+
				"from it: shapers could hit its workers (keep %g m clear)", w, near, IslandWorkerReach)
		}
	}
}

func (cm *Compiled) stats() *Stats {
	ly := cm.Layers
	st := &Stats{Cell: cm.Cell, PlayerAreas: cm.Player.Areas, NPCAreas: cm.NPC.Areas, Bushes: len(ly.Bushes),
		Wells: 2 * len(cm.Map.Wells), CellSize: ly.Size / Grid}
	st.Camps = len(cm.Layout.Teams[0].Camps) + len(cm.Layout.Teams[1].Camps)
	if ps, _, err := cm.WaterPlanes(); err == nil {
		st.WaterPlanes = len(ps)
	}
	for _, k := range ly.Kind {
		switch k {
		case Open:
			st.OpenCells++
		case Chasm:
			st.ChasmCells++
		case Island:
			st.IslandCells++
		}
	}
	for _, id := range cm.Layout.LaneIDs {
		st.LaneLengths = append(st.LaneLengths, int(math.Round(length(cm.Layout.Teams[0].Lanes[id]))))
	}
	// walkable cells whose block isn't on the Player graph and isn't in a Guardian's hole: cut off (dropped.go)
	for _, d := range cm.droppedBlocks() {
		if d {
			st.DroppedCells += cm.Cell * cm.Cell
		}
	}
	return st
}

func reversedStrings(ss []string) []string {
	out := make([]string, len(ss))
	for i, v := range ss {
		out[len(ss)-1-i] = v
	}
	return out
}
