package mapc

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"dawngate/server/internal/setup/mapmm/fogmap"
	"dawngate/server/internal/setup/mapmm/grassmap"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/mapmm/navgraph"
	"dawngate/server/internal/setup/snap"
)

// The import (MAPED-IMPORT): a shipped level as a format-1 package, so a map author can start from it in the editor.
//
// Import reads the level from the player's copy (its Level, markersets and line-of-sight map; nothing of it is kept but
// coordinates and traced shapes) and writes team 1's half as format 1 has it: the size from the Level's levelSize; the
// lanes from team 1's splines (lane 1 its Top spline, lane 2 the mirror walked back, at most MaxKnots knots); the fixed
// set (spawns, Guardian, Locus, the base zone as the shop, the Cores, the Bindings) from its markers; the wells, camps
// and bushes from theirs; the terrain traced from the line-of-sight map's team-1 half (x + y <= 0) into open polygons
// and the walls inside them. Team 2 is the compiler's mirror, so ImportReport says how far the level's own team 2 lies
// from that mirror, and how many line-of-sight cells the compiled map gets different from the level's.
//
// The line-of-sight map can't tell a chasm from open ground; the level's own Player navigation graph can, where the
// copy has it (ImportInputs.Level, SM_Map_v5's archive): the see-through ground well off the walkable graph becomes
// chasm, but for the platforms the graph leaves out under the level's objects (chasms). The border of the level stays
// wall (terrain.edge).
//
// With ImportOptions.Dress the level's dressing sets become props groups (importdress.go). A well on ground not
// joined to the base (MM_Map_v3's mines on their NPC-only islands) keeps it as an island shape (joinWells;
// ImportReport.Changes lists it).

// ImportOptions name the package; a zero value takes the defaults below.
type ImportOptions struct {
	Name, Display, Tag, Prefix string
	// Tolerance is the terrain tracer's Douglas-Peucker tolerance in cells (default ImportTolerance).
	Tolerance float64
	// Dress writes the level's dressing sets as props groups that stand once, both halves as the level has them
	// (importdress.go).
	Dress bool
}

const (
	// ImportTolerance is the default simplification of the traced terrain, in cells (a cell is size/512).
	ImportTolerance = 1.5
	// importCut is how far past the diagonal x + y = 0 the traced team-1 half may reach (metres): its own diagonal
	// cells (x + y = 0) stay inside, the next row (x + y = one cell) outside, and symmetry.half (0.5) holds.
	importCut = 0.3
	// importOver is how many cells past the diagonal the traced field reaches before its loops are clipped at
	// importCut: the tracer's simplification (up to the tolerance, 1.5 cells) then can't pull an outline back over the
	// diagonal's own cells, which the mirror's outline doesn't reach either (MAPED-IMPORT-FIT: CM_Pass's lane crossing
	// lost a strip of the centre that way).
	importOver = 4
	// importEdge is the border of cells the import keeps wall: terrain.edge's EdgeCells, one more for the mirror (cell
	// i's mirror is 512 - i) and one for the tracer's simplification.
	importEdge = EdgeCells + 2
	// WellClearing is the open circle an imported well gets when its place isn't joined to its base (metres; CM_Ring's
	// well circles are 26 m, a well's worker spots stand about 22 m round it).
	WellClearing = 24.0
	// WellPathWidth is the path that joins such a well to its base.
	WellPathWidth = 12.0

	// ImportRim: open ground within this many cells of the level's walkable graph counts as walkable (the graph keeps
	// its radius, 1.59 m, about 1.6 cells, off every wall and drop), farther it is a chasm (MAPED-IMPORT-SM).
	ImportRim = 3.0
	// ImportMinChasm: a part of chasm under this many cells stays open (the slivers in narrow wall pockets).
	ImportMinChasm = 16
	// ImportKeepClear and ImportLaneClear: a part within this many metres of a fixed object, well, camp or lane knot,
	// or of a lane's leg, stays open (the platforms the level's graph leaves out).
	ImportKeepClear = 10.0
	ImportLaneClear = 3.0
)

// ImportReport is what the import found and changed.
type ImportReport struct {
	Level    string  `json:"level"`
	Size     float64 `json:"size"`
	SizeNote string  `json:"sizeNote,omitempty"`
	// Mirror: per object kind how far the level's own team 2 lies from team 1's mirror (metres).
	Mirror []MirrorDiff `json:"mirror"`
	// LoS compares the level's line-of-sight map with itself mirrored and with the compiled package's.
	LoS ImportLoS `json:"los"`
	// Shapes and Vertices are the traced terrain's.
	Shapes   int `json:"shapes"`
	Vertices int `json:"vertices"`
	// Changes lists what the import made differently from the level (and why).
	Changes []string `json:"changes"`
	// Dress is the dressing's report (ImportOptions.Dress only).
	Dress *ImportDress `json:"dress,omitempty"`

	// Chasm is what the level's walkable graph gave (nil: the copy has none for the level).
	Chasm *ImportChasm `json:"chasm,omitempty"`
}

// ImportChasm counts cells (512 x 512) of the level's walkable ground and the chasms traced from it.
type ImportChasm struct {
	Walkable int `json:"walkable"` // the level's Player graph's cells (both halves)
	// Cells and Parts: the chasm traced in team 1's half; Kept (KeptCells) the parts left open under objects, Small
	// those under ImportMinChasm cells.
	Cells     int `json:"cells"`
	Parts     int `json:"parts"`
	Kept      int `json:"kept"`
	KeptCells int `json:"keptCells"`
	Small     int `json:"small"`
	// Package and Both: the compiled Player graph's cells, and those also in the level's; Share is Both over the
	// union (the walkable ground's IoU).
	Package int     `json:"package"`
	Both    int     `json:"both"`
	Share   float64 `json:"share"`
}

// MirrorDiff is one object kind's distance between the level's team 2 and team 1 mirrored.
type MirrorDiff struct {
	What  string  `json:"what"`
	Count int     `json:"count"`
	Max   float64 `json:"max"`  // metres; -1 when team 2 has none to compare
	Note  string  `json:"note"` // e.g. how the counts differ
	// Spline: a lane, Max the farthest either polyline's knots lie from the other (the level's spline, the package's
	// lane in that team's direction).
	Spline bool `json:"spline,omitempty"`
}

// ImportLoS counts line-of-sight cells (512 x 512).
type ImportLoS struct {
	LevelOpen int `json:"levelOpen"` // the level's open cells
	// PointMirror and Reflection: the level's cells that differ from their point mirror (x, y) -> (-x, -y) and from
	// their reflection across the diagonal x + y = 0 ((x, y) -> (-y, -x)).
	PointMirror int `json:"pointMirror"`
	Reflection  int `json:"reflection"`
	// Differ: the compiled package's cells that differ from the level's, Team1Half / Team2Half where (x + y <= 0 or
	// > 0), and Changed those inside the import's own changes (Changes).
	Differ    int `json:"differ"`
	Team1Half int `json:"team1Half"`
	Team2Half int `json:"team2Half"`
	Changed   int `json:"changed"`
	// Share is Differ over the level's open cells.
	Share float64 `json:"share"`
}

// importMarker is one marker of the level: its noun (lower case, without .noun), position and the parsed object.
type importMarker struct {
	set, name, noun string
	pos             Point
	spline          *splineKnot
}

type splineKnot struct {
	name             string
	knot, lane, team int
}

// ImportInputs are the copy's archives the import reads.
type ImportInputs struct {
	AssetData, Images []byte

	// Level is the level's own archive (LevelSource), optional: its Player graph is the level's walkable ground, which
	// tells the chasms (see-through ground that isn't walked) from open ground. Without it the import has no chasms.
	Level []byte
}

// ImportSources are those archives' paths in a copy.
var ImportSources = []string{AssetDataSource, "game/Data/" + los.Archive}

// LoSEntry is a level's line-of-sight map in images.snap.
func LoSEntry(level string) string { return "images/8bit/" + level + "_LineOfSight.tga" }

// LevelSource is a level's own archive in a copy (SM_Map_v5's holds its graphs; MM_Map_v3 has none in the game).
func LevelSource(level string) string { return "game/Data/" + level + ".snap" }

// PlayerGraphEntry is a level's Player navigation graph in its archive.
func PlayerGraphEntry(level string) string { return "levels/" + level + "/" + level + "_Player.bfx" }

// levelWalk reads the level's walkable cells from its Player graph (nil without the archive or the graph).
func levelWalk(level string, archive []byte, size float64) ([]bool, error) {
	if archive == nil {
		return nil, nil
	}
	a, err := snap.Parse(archive)
	if err != nil {
		return nil, err
	}
	for _, e := range a.Entries {
		if strings.EqualFold(e.Name, PlayerGraphEntry(level)) {
			raw, err := a.Read(e)
			if err != nil {
				return nil, err
			}
			img, err := navgraph.Parse(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", PlayerGraphEntry(level), err)
			}
			if len(img.Graphs) == 0 {
				return nil, fmt.Errorf("%s holds no graph", PlayerGraphEntry(level))
			}
			return navgraph.Cells(img.Graphs[0], size, Grid), nil
		}
	}
	return nil, nil
}

// Import makes a package's map from a shipped level. The map isn't checked here (the caller runs Check on it).
func Import(level string, in ImportInputs, o ImportOptions) (*Map, *ImportReport, error) {
	if o.Tolerance <= 0 {
		o.Tolerance = ImportTolerance
	}
	src, err := newAssetSource(in.AssetData)
	if err != nil {
		return nil, nil, err
	}
	lv, err := src.object(level + ".Level")
	if err != nil {
		return nil, nil, err
	}
	rep := &ImportReport{Level: level}
	sz := vec(lv, "levelSize")
	if len(sz) != 2 || sz[0] <= 0 {
		return nil, nil, fmt.Errorf("%s.Level has no levelSize", level)
	}
	size := math.Round(math.Max(sz[0], sz[1])*1000) / 1000
	if math.Abs(sz[0]-sz[1]) > 0.001 {
		rep.SizeNote = fmt.Sprintf("the level is %.3f x %.3f m; format 1 is square: %.3f", sz[0], sz[1], size)
	}
	rep.Size = size
	sets, err := levelSets(lv)
	if err != nil {
		return nil, nil, err
	}
	var ms []importMarker
	var grassRaw []byte
	for _, s := range sets {
		obj, err := src.object(s + ".Markerset")
		if err != nil {
			return nil, nil, err
		}
		for _, m := range markers(obj) {
			p := vec(m, "pos")
			if len(p) < 2 {
				continue
			}
			im := importMarker{set: s, name: text(m, "markerName"),
				noun: strings.ToLower(strings.TrimSuffix(text(m, "nounDef"), ".Noun")), pos: Point{p[0], p[1]}}
			if d := sub(m, "splinePathNodeData"); d != nil {
				im.spline = &splineKnot{name: text(d, "name"), knot: int(fval(mustGet(d, "knot"))),
					lane: int(fval(mustGet(d, "lane"))), team: int(fval(mustGet(d, "team")))}
			}
			ms = append(ms, im)
		}
		if strings.EqualFold(s, level+"_"+setGrass) {
			e, ok := src.byName[strings.ToLower(AssetEntry(s+".Markerset"))]
			if ok {
				if grassRaw, err = src.arch.Read(e); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	sort.SliceStable(ms, func(a, b int) bool { return ms[a].name < ms[b].name })

	m := &Map{Format: Format, Name: o.Name, Display: o.Display, Tag: o.Tag, Prefix: o.Prefix, Size: size,
		Symmetry: "point", Environment: SharedLevels[0], Tuning: SharedLevels[0], Ground: Ground{Style: "tiled"},
		Camps: []Camp{}}
	for _, l := range SharedLevels {
		if strings.EqualFold(l, level) {
			m.Environment, m.Tuning = l, l
		}
	}
	im := &importer{m: m, rep: rep, ms: ms}
	if err := im.lanes(); err != nil {
		return nil, nil, err
	}
	if err := im.base(); err != nil {
		return nil, nil, err
	}
	im.wellsAndCamps()
	if o.Dress {
		if err := im.dress(src, level, sets, size); err != nil {
			return nil, nil, err
		}
	}

	raw, err := los.FromArchive(in.Images, LoSEntry(level))
	if err != nil {
		return nil, nil, err
	}
	img, err := los.Read(raw)
	if err != nil {
		return nil, nil, err
	}
	if img.W != Grid || img.H != Grid {
		return nil, nil, fmt.Errorf("%s: %d x %d cells, %d x %d expected", LoSEntry(level), img.W, img.H, Grid, Grid)
	}
	walk, err := levelWalk(level, in.Level, size)
	if err != nil {
		return nil, nil, err
	}
	if err := im.terrain(img, o.Tolerance, walk); err != nil {
		return nil, nil, err
	}
	im.laneWidth(img)
	if strings.EqualFold(level, los.Level) {
		// MM_Map_v3's graphs are squares of 2 cells (navgraph.MM, 8,089 and 8,459 areas): the package keeps that grid,
		// so its walkable ground reaches as near the walls as the level's (the budget would pick 3 cells)
		m.Graph = &Graph{Cell: navgraph.MM.Cell}
		im.note("the graphs keep the level's squares of %d cells (graph.cell)", navgraph.MM.Cell)
	}
	if grassRaw != nil {
		ps, err := grassmap.Patches(src.ts, grassRaw, "images/32bit/"+level+"_GrassMap.tga")
		if err != nil {
			return nil, nil, err
		}
		im.bushes(ps)
	}
	changed, err := im.joinWells()
	if err != nil {
		return nil, nil, err
	}
	for _, k := range im.edge {
		changed[k] = true
	}
	if err := im.compare(img, changed, walk); err != nil {
		return nil, nil, err
	}
	return m, rep, nil
}

type importer struct {
	m   *Map
	rep *ImportReport
	ms  []importMarker

	edge []int // the level's open cells the import keeps wall along the edge (terrain)
}

func (im *importer) note(format string, args ...any) {
	im.rep.Changes = append(im.rep.Changes, fmt.Sprintf(format, args...))
}

// find are the markers whose noun matches (a prefix when it ends in '*').
func (im *importer) find(noun string) []importMarker {
	var out []importMarker
	pre := strings.HasSuffix(noun, "*")
	noun = strings.TrimSuffix(noun, "*")
	for _, m := range im.ms {
		if m.noun == noun || (pre && strings.HasPrefix(m.noun, noun)) {
			out = append(out, m)
		}
	}
	return out
}

func r1(x float64) float64 { return math.Round(x*10) / 10 }

func r1p(p Point) Point { return Point{r1(p[0]), r1(p[1])} }

func positions(ms []importMarker) []Point {
	out := make([]Point, len(ms))
	for k, m := range ms {
		out[k] = m.pos
	}
	return out
}

// mirrorDiff records the largest distance from each of team 2's points to the nearest mirrored team-1 point.
func (im *importer) mirrorDiff(what string, t1, t2 []Point) {
	d := MirrorDiff{What: what, Count: len(t1), Max: -1}
	if len(t1) != len(t2) {
		d.Note = fmt.Sprintf("team 1 has %d, team 2 %d", len(t1), len(t2))
	}
	for _, q := range t2 {
		best := math.Inf(1)
		for _, p := range t1 {
			best = math.Min(best, dist(Mirror(p), q))
		}
		if !math.IsInf(best, 1) {
			d.Max = math.Max(d.Max, math.Round(best*100)/100)
		}
	}
	im.rep.Mirror = append(im.rep.Mirror, d)
}

// splines are the level's splines by name, knots in order; heads their head knot's data.
func (im *importer) splines() (map[string][]Point, map[string]splineKnot) {
	type kp struct {
		k int
		p Point
	}
	by := map[string][]kp{}
	heads := map[string]splineKnot{}
	for _, m := range im.ms {
		if m.spline == nil || m.spline.name == "" {
			continue
		}
		by[m.spline.name] = append(by[m.spline.name], kp{m.spline.knot, m.pos})
		if m.spline.knot == 0 {
			heads[m.spline.name] = *m.spline
		}
	}
	out := map[string][]Point{}
	for n, ks := range by {
		sort.Slice(ks, func(a, b int) bool { return ks[a].k < ks[b].k })
		for _, k := range ks {
			out[n] = append(out[n], k.p)
		}
	}
	return out, heads
}

// laneOf picks a team's spline for a lane id: the one whose head knot names the team and lane.
func laneOf(heads map[string]splineKnot, team, lane int) string {
	var names []string
	for n, h := range heads {
		if h.team == team && h.lane == lane {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// simplifyKnots drops knots (the one whose removal moves the path least) until at most MaxKnots remain.
func simplifyKnots(ks []Point) []Point {
	out := append([]Point(nil), ks...)
	for len(out) > MaxKnots {
		best, bi := math.Inf(1), -1
		for k := 1; k+1 < len(out); k++ {
			if d := segDist(out[k], out[k-1], out[k+1]); d < best {
				best, bi = d, k
			}
		}
		out = append(out[:bi], out[bi+1:]...)
	}
	return out
}

// laneGap is the largest distance from any of a's knots to the polyline b.
func laneGap(a, b []Point) float64 {
	worst := 0.0
	for _, p := range a {
		_, d := project(b, p)
		worst = math.Max(worst, d)
	}
	return math.Round(worst*100) / 100
}

func (im *importer) lanes() error {
	sp, heads := im.splines()
	l1 := laneOf(heads, 1, 1)
	if l1 == "" {
		return fmt.Errorf("the level has no spline whose head knot is team 1's lane 1")
	}
	// Format 1's lane 2 is lane 1 mirrored and walked back, so lane 1's second half is lane 2's first half mirrored.
	// Each lane keeps the level's own first half (team 1's spline up to the diagonal x + y = 0, where its minions
	// leave their base), and its second half is the other lane's first half mirrored: the level's ends near the enemy
	// base (MM_Map_v3's curl round the Guardian) aren't kept.
	a := sp[l1]
	l2 := laneOf(heads, 1, 2)
	b := a
	if l2 != "" {
		b = sp[l2]
	} else {
		im.note("the level has one lane a team (%s); format 1's lane 2 is lane 1 mirrored", l1)
	}
	half := func(ks []Point) []Point {
		var out []Point
		for _, p := range ks {
			if p[0]+p[1] > 0 {
				break
			}
			out = append(out, r1p(p))
		}
		return out
	}
	ha, hb := half(a), half(b)
	if len(ha) == 0 || len(hb) == 0 {
		return fmt.Errorf("team 1's splines %s and %s don't start in team 1's half (x + y <= 0)", l1, l2)
	}
	tail := reversed(mirrorAll(hb))
	if dist(ha[len(ha)-1], tail[0]) < 1 { // the two meet at the centre: one knot there
		j := r1p(Point{(ha[len(ha)-1][0] + tail[0][0]) / 2, (ha[len(ha)-1][1] + tail[0][1]) / 2})
		ha[len(ha)-1], tail = j, tail[1:]
	}
	k1 := append(ha, tail...)
	if len(k1) > MaxKnots {
		im.note("lane 1 has %d knots; the match host's table takes %d: the knots that bend it least dropped",
			len(k1), MaxKnots)
		k1 = simplifyKnots(k1)
	}
	k2 := reversed(mirrorAll(k1))
	im.m.Lanes = []Lane{{ID: 1, Knots: k1}, {ID: 2, Knots: k2}}
	im.note("lane 1 is %s's %d knots to the centre, then %s's mirrored (%d knots); lane 2 its mirror: the level's "+
		"lanes past the centre aren't kept", l1, len(ha), l2, len(k1))
	// the level's own lanes against the package's
	gap := func(what, n string, want []Point) {
		im.rep.Mirror = append(im.rep.Mirror, MirrorDiff{What: what + " (" + n + ")", Count: len(sp[n]),
			Max: math.Max(laneGap(sp[n], want), laneGap(want, sp[n])), Spline: true})
	}
	gap("team 1's lane 1 against the package's", l1, k1)
	if l2 != "" {
		gap("team 1's lane 2 against the package's", l2, k2)
	}
	for id, want := range map[int][]Point{1: reversed(k1), 2: reversed(k2)} {
		if n := laneOf(heads, 2, id); n != "" {
			gap(fmt.Sprintf("team 2's lane %d against the package's", id), n, want)
		}
	}
	sort.SliceStable(im.rep.Mirror, func(a, b int) bool { return im.rep.Mirror[a].What < im.rep.Mirror[b].What })
	return nil
}

// one is the first marker of a noun pattern, or an error.
func (im *importer) one(noun, what string) (Point, error) {
	f := im.find(noun)
	if len(f) == 0 {
		return Point{}, fmt.Errorf("the level has no %s (%s)", what, noun)
	}
	return f[0].pos, nil
}

func (im *importer) base() error {
	b := &im.m.Base
	pairs := []struct {
		what   string
		t1, t2 string
		dst    *Point
	}{
		{"the Locus", "dest_prefab_blue_tower*", "dest_prefab_red_tower*", &b.Locus},
		{"the Guardian", "titan_node_blue_base*", "titan_node_red_base*", &b.Guardian},
		{"the shop (base zone)", "*regen_defenders", "*regen_attackers", &b.Shop},
	}
	for _, p := range pairs {
		var t1, t2 []importMarker
		for _, m := range im.ms {
			if matchNoun(m.noun, p.t1) {
				t1 = append(t1, m)
			}
			if matchNoun(m.noun, p.t2) {
				t2 = append(t2, m)
			}
		}
		if len(t1) == 0 {
			return fmt.Errorf("the level has no %s (%s)", p.what, p.t1)
		}
		*p.dst = r1p(t1[0].pos)
		im.mirrorDiff(p.what, positions(t1[:1]), positions(t2))
	}
	s1, s2 := im.find("spawnpoint_team1"), im.find("spawnpoint_team2")
	for _, s := range s1 {
		b.Spawns = append(b.Spawns, r1p(s.pos))
	}
	im.mirrorDiff("the spawn points", positions(s1), positions(s2))
	// the Cores, A to E
	var c1, c2 []Point
	for l := 'a'; l <= 'e'; l++ {
		if f := im.find("nub_mortal_v5_" + string(l)); len(f) > 0 {
			c1 = append(c1, f[0].pos)
		}
		if f := im.find("nub_spirit_v5_" + string(l)); len(f) > 0 {
			c2 = append(c2, f[0].pos)
		}
	}
	for _, c := range c1 {
		b.Cores.Points = append(b.Cores.Points, r1p(c))
	}
	im.mirrorDiff("the Cores", c1, c2)
	return im.bindings()
}

// matchNoun matches a noun against a pattern with a '*' at either end.
func matchNoun(noun, pat string) bool {
	switch {
	case strings.HasPrefix(pat, "*"):
		return strings.HasSuffix(noun, pat[1:])
	case strings.HasSuffix(pat, "*"):
		return strings.HasPrefix(noun, pat[:len(pat)-1])
	}
	return noun == pat
}

// bindings: team 1's Bindings by tier, each on its nearest lane; when the lanes are one path (the host's
// LanesShareOnePath) all of them on the lane nearest them all, as the host puts a one-path level's Bindings.
func (im *importer) bindings() error {
	type bd struct {
		tier int
		p    Point
	}
	var t1, t2 []bd
	var p2 []Point
	for k, tier := range Tiers {
		for _, m := range im.find("binding_mortal_" + strings.ToLower(tier)) {
			t1 = append(t1, bd{k, m.pos})
		}
		for _, m := range im.find("binding_spirit_" + strings.ToLower(tier)) {
			t2 = append(t2, bd{k, m.pos})
			p2 = append(p2, m.pos)
		}
	}
	var p1 []Point
	for _, b := range t1 {
		p1 = append(p1, b.p)
	}
	im.mirrorDiff("the Bindings", p1, p2)
	if len(t1) == 0 {
		return fmt.Errorf("the level has no Binding_Mortal_* marker")
	}
	lanes := im.m.Lanes
	shared := LanesShareOnePath(lanes)
	by := map[int][]bd{}
	if shared {
		best, bl := math.Inf(1), 0
		for k, ln := range lanes {
			sum := 0.0
			for _, b := range t1 {
				_, d := project(ln.Knots, b.p)
				sum += d
			}
			if sum < best {
				best, bl = sum, k
			}
		}
		by[lanes[bl].ID] = t1
	} else {
		for _, b := range t1 {
			best, bl := math.Inf(1), 0
			for k, ln := range lanes {
				if _, d := project(ln.Knots, b.p); d < best {
					best, bl = d, k
				}
			}
			by[lanes[bl].ID] = append(by[lanes[bl].ID], b)
		}
	}
	im.m.Base.Bindings = Bindings{Lanes: map[string][]Point{}}
	for id, bs := range by {
		sort.SliceStable(bs, func(a, b int) bool { return bs[a].tier < bs[b].tier })
		var pts []Point
		for _, b := range bs {
			pts = append(pts, r1p(b.p))
		}
		im.m.Base.Bindings.Lanes[fmt.Sprint(id)] = pts
	}
	if shared && len(lanes) == 2 {
		im.note("the lanes are one path (at most %.0f m apart): team 1's %d Bindings all go on the lane nearest them, "+
			"team 2's are their mirror on the other lane id (the match host takes a one-path level's Bindings as "+
			"standing in every lane)", SharedPath, len(t1))
	}
	return nil
}

// laneWidth sets both lanes' width: the open corridor's median width across lane 1's inner knots (from the
// line-of-sight map), narrowed where a Binding would otherwise reach past its range (fixed.range), within 6-80 m.
func (im *importer) laneWidth(img los.Image) {
	ks := im.m.Lanes[0].Knots
	cell := im.m.Size / Grid
	openAt := func(p Point) bool {
		i := int(math.Floor(p[0]/cell + Grid/2 + 0.5))
		j := int(math.Floor(p[1]/cell + Grid/2 + 0.5))
		return i >= 0 && j >= 0 && i < Grid && j < Grid && img.Pix[j*Grid+i] == 0
	}
	var ws []float64
	for k := 1; k+1 < len(ks); k++ {
		d := Point{ks[k+1][0] - ks[k-1][0], ks[k+1][1] - ks[k-1][1]}
		l := math.Hypot(d[0], d[1])
		if l == 0 || !openAt(ks[k]) {
			continue
		}
		n := Point{-d[1] / l, d[0] / l}
		w := 0.0
		for _, sgn := range []float64{1, -1} {
			s := 0.0
			for s < 100 && openAt(Point{ks[k][0] + sgn*n[0]*(s+cell/2), ks[k][1] + sgn*n[1]*(s+cell/2)}) {
				s += cell / 2
			}
			w += s
		}
		ws = append(ws, w)
	}
	width := 20.0
	if len(ws) > 0 {
		sort.Float64s(ws)
		width = math.Round(ws[len(ws)/2])
	}
	measured := width
	for id, pts := range im.m.Base.Bindings.Lanes {
		var knots []Point
		for _, ln := range im.m.Lanes {
			if fmt.Sprint(ln.ID) == id {
				knots = ln.Knots
			}
		}
		for _, p := range pts {
			if _, d := project(knots, p); 2*(BindingRange-d)-1 < width {
				width = math.Floor(2*(BindingRange-d) - 1)
			}
		}
	}
	width = math.Max(6, math.Min(80, width))
	if width != measured {
		im.note("the lanes' width is %.0f m, not the corridor's %.0f m: a Binding's range (%.0f m) must reach across "+
			"its lane's corridor (fixed.range)", width, measured, BindingRange)
	}
	for k := range im.m.Lanes {
		im.m.Lanes[k].Width = width
	}
}

// isCampNoun tells a jungle camp's spawner noun (SM_Map_v5's: SpawnPoint_v5_*, the parasite's
// SpawnPoint_Objective_Titan).
func isCampNoun(n string) string {
	switch {
	case n == "spawnpoint_objective_titan":
		return CampParasite
	case strings.HasPrefix(n, "spawnpoint_v5_creeps") || strings.HasPrefix(n, "spawnpoint_v5_buff"):
		if buffNoun(n) {
			return CampBuff
		}
		return CampCamp
	}
	return ""
}

func (im *importer) wellsAndCamps() {
	w1, w2 := im.find("gold_blue_side*"), im.find("gold_red_side*")
	for _, w := range w1 {
		well := Well{Pos: r1p(w.pos)}
		if w.noun == "gold_blue_side_mm" { // MM_Map_v3's mine (36 worker spots, MAPMINES), placed as itself
			well.Kind = WellMine
		}
		im.m.Wells = append(im.m.Wells, well)
	}
	im.mirrorDiff("the wells", positions(w1), positions(w2))
	var c1, c2 []Point
	for _, m := range im.ms {
		kind := isCampNoun(m.noun)
		if kind == "" {
			continue
		}
		if kind == CampParasite {
			im.m.Camps = append(im.m.Camps, Camp{Kind: kind, Noun: canonicalNoun(m), Pos: r1p(m.pos)})
			continue
		}
		if m.pos[0]+m.pos[1] <= 0 {
			im.m.Camps = append(im.m.Camps, Camp{Kind: kind, Noun: canonicalNoun(m), Pos: r1p(m.pos)})
			c1 = append(c1, m.pos)
		} else {
			c2 = append(c2, m.pos)
		}
	}
	if len(c1)+len(c2) > 0 {
		im.mirrorDiff("the camps", c1, c2)
	}
}

// canonicalNoun is a marker's noun as the template level names it (the asset name with its case).
func canonicalNoun(m importMarker) string {
	for _, n := range []string{"SpawnPoint_Objective_Titan"} {
		if strings.EqualFold(n, m.noun) {
			return n + ".Noun"
		}
	}
	// SpawnPoint_v5_<Creeps|Buff_><Kind>: the marker's own name carries the case (the noun plus "-n")
	name, _, _ := strings.Cut(m.name, "-")
	if strings.EqualFold(strings.TrimSuffix(name, ".Noun"), m.noun) {
		return strings.TrimSuffix(name, ".Noun") + ".Noun"
	}
	return m.noun + ".Noun"
}

// ---------------------------------------------------------------- the terrain

// terrain traces the level's open ground in team 1's half (cells with x + y <= 0) into polygons: a region's outline
// becomes an open shape, a hole in it a wall shape, drawn in that order over a wall default. The cells within
// EdgeCells of the level's edge stay wall (terrain.edge). With the level's walkable ground (walk, from its Player
// graph; nil: none) the open ground far from it becomes chasm (chasms): each chasm's outline is drawn after the open
// region holding it and before that region's walls, a walkable island inside a chasm as open after it.
func (im *importer) terrain(img los.Image, tol float64, walk []bool) error {
	f := make(fogmap.Field, Grid*Grid)
	im.edge = nil
	for j := 0; j < Grid; j++ {
		for i := 0; i < Grid; i++ {
			k := j*Grid + i
			edge := min(i, j, Grid-1-i, Grid-1-j) < importEdge
			if img.Pix[k] == 0 && i+j <= Grid+importOver && !edge {
				f[k] = 1
			} else {
				f[k] = -1
				if img.Pix[k] == 0 && edge {
					im.edge = append(im.edge, k)
				}
			}
		}
	}
	if band := len(im.edge); band > 0 {
		im.note("%d open cells within %d cells of the level's edge (%.1f m) are wall (terrain.edge: the ground model "+
			"and the floor end there)", band, importEdge, importEdge*im.m.Size/Grid)
	}
	ts, err := im.trace(f, tol)
	if err != nil {
		return err
	}
	for k := range ts {
		for l := range ts {
			if k != l && inPolygon(ts[k].pts[0], ts[l].pts) {
				ts[k].depth++
			}
		}
		ts[k].key = float64(ts[k].depth)
		ts[k].typ = "open"
		if ts[k].hole {
			ts[k].typ = "wall"
		}
	}
	if walk != nil {
		cs, err := im.chasms(f, walk, tol)
		if err != nil {
			return err
		}
		for k := range cs {
			// after the open region round it (the LoS loops holding most of its corners, less one: a corner may lie
			// just outside its region's simplified outline), before that region's walls
			in := 0
			for l := range ts {
				n := 0
				for _, p := range cs[k].pts {
					if inPolygon(p, ts[l].pts) {
						n++
					}
				}
				if 2*n > len(cs[k].pts) {
					in++
				}
			}
			cs[k].key = float64(in-1) + 0.5 + 0.01*float64(cs[k].depth)
		}
		ts = append(ts, cs...)
	}
	sort.SliceStable(ts, func(a, b int) bool {
		if ts[a].key != ts[b].key {
			return ts[a].key < ts[b].key
		}
		return ts[a].area > ts[b].area
	})
	im.m.Terrain = Terrain{Default: "wall"}
	for _, t := range ts {
		im.m.Terrain.Shapes = append(im.m.Terrain.Shapes, Shape{Type: t.typ, Polygon: t.pts})
		im.rep.Shapes++
		im.rep.Vertices += len(t.pts)
	}
	return nil
}

// chasms traces the level's see-through but unwalkable ground in team 1's half: the open cells of f (team 1's traced
// half) farther than ImportRim cells from the level's walkable ground (walk; the Player graph's rim along walls and
// drops is ImportRim wide at most), in 4-connected parts of at least ImportMinChasm cells and none within
// ImportKeepClear metres of a fixed object, a well, a camp or a lane knot (nor ImportLaneClear of a lane's leg): the
// shipped graph leaves out the platforms the Guardian, the Cores, the Locus and the spawn points stand on, which must
// stay open here. A part's outline becomes a chasm, a hole in it open.
func (im *importer) chasms(f fogmap.Field, walk []bool, tol float64) ([]traced, error) {
	cell := im.m.Size / Grid
	near := dilate(walk, ImportRim)
	keep := make([]bool, Grid*Grid)
	mark := func(p Point, r float64) {
		n := int(math.Ceil(r / cell))
		ci, cj := int(math.Round(p[0]/cell+Grid/2)), int(math.Round(p[1]/cell+Grid/2))
		for j := max(cj-n, 0); j <= min(cj+n, Grid-1); j++ {
			for i := max(ci-n, 0); i <= min(ci+n, Grid-1); i++ {
				q := Point{(float64(i) - Grid/2) * cell, (float64(j) - Grid/2) * cell}
				if dist(p, q) <= r {
					keep[j*Grid+i] = true
				}
			}
		}
	}
	for _, p := range im.objects() {
		mark(p, ImportKeepClear)
	}
	for _, l := range im.m.Lanes {
		for k := 1; k < len(l.Knots); k++ {
			a, b := l.Knots[k-1], l.Knots[k]
			n := int(math.Ceil(dist(a, b) / cell))
			for s := 0; s <= n; s++ {
				t := float64(s) / float64(max(n, 1))
				mark(Point{a[0] + t*(b[0]-a[0]), a[1] + t*(b[1]-a[1])}, ImportLaneClear)
			}
		}
	}
	cand := func(k int) bool { return f[k] > 0 && !near[k] }
	g := make(fogmap.Field, Grid*Grid)
	for k := range g {
		g[k] = -1
	}
	seen := make([]bool, Grid*Grid)
	var small, kept, keptCells, cells, parts int
	for k0 := range f {
		if seen[k0] || !cand(k0) {
			continue
		}
		seen[k0] = true
		comp := []int{k0}
		cleared := false
		for q := 0; q < len(comp); q++ {
			k := comp[q]
			cleared = cleared || keep[k]
			i, j := k%Grid, k/Grid
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				a, b := i+d[0], j+d[1]
				if a < 0 || b < 0 || a >= Grid || b >= Grid {
					continue
				}
				if n := b*Grid + a; !seen[n] && cand(n) {
					seen[n] = true
					comp = append(comp, n)
				}
			}
		}
		switch {
		case len(comp) < ImportMinChasm:
			small++
		case cleared:
			kept++
			keptCells += len(comp)
		default:
			parts++
			cells += len(comp)
			for _, k := range comp {
				g[k] = 1
			}
		}
	}
	im.rep.Chasm = &ImportChasm{Walkable: count(walk), Cells: cells, Parts: parts, Kept: kept, KeptCells: keptCells,
		Small: small}
	if parts == 0 {
		return nil, nil
	}
	ts, err := im.trace(g, tol)
	if err != nil {
		return nil, err
	}
	for k := range ts {
		for l := range ts {
			if k != l && inPolygon(ts[k].pts[0], ts[l].pts) {
				ts[k].depth++
			}
		}
		ts[k].typ = "chasm"
		if ts[k].hole {
			ts[k].typ = "open"
		}
	}
	im.note("%d parts of see-through ground off the level's walkable graph (%d cells in team 1's half) are chasm; %d "+
		"more under fixed objects, wells, camps or lanes (%d cells: the platforms the level's graph leaves out) and %d "+
		"under %d cells stay open", parts, cells, kept, keptCells, small, ImportMinChasm)
	return ts, nil
}

// objects are the places a chasm must keep clear of: team 1's fixed set, wells, camps, lane knots and minion
// spawners.
func (im *importer) objects() []Point {
	b := im.m.Base
	ps := []Point{b.Locus, b.Guardian, b.Shop}
	ps = append(ps, b.Spawns...)
	ps = append(ps, b.Cores.Points...)
	for _, bs := range b.Bindings.Lanes { // marked, so the order doesn't matter
		ps = append(ps, bs...)
	}
	for _, w := range im.m.Wells {
		ps = append(ps, w.Pos)
	}
	for _, c := range im.m.Camps {
		ps = append(ps, c.Pos)
	}
	for _, l := range im.m.Lanes {
		ps = append(ps, l.Knots...)
		if len(l.Knots) >= 2 {
			ps = append(ps, Spawner(l.Knots))
		}
	}
	return ps
}

// dilate marks the cells within r cells (Euclidean, centre to centre) of a marked one.
func dilate(on []bool, r float64) []bool {
	out := make([]bool, len(on))
	n := int(math.Floor(r))
	for k, v := range on {
		if !v {
			continue
		}
		i, j := k%Grid, k/Grid
		for b := -n; b <= n; b++ {
			for a := -n; a <= n; a++ {
				x, y := i+a, j+b
				if float64(a*a+b*b) <= r*r && x >= 0 && y >= 0 && x < Grid && y < Grid {
					out[y*Grid+x] = true
				}
			}
		}
	}
	return out
}

func count(on []bool) int {
	n := 0
	for _, v := range on {
		if v {
			n++
		}
	}
	return n
}

// traced is one loop of a traced field: its corners in metres, whether it is a hole (clockwise), its nesting depth
// among its own field's loops, its area, the shape type it becomes and its drawing order.
type traced struct {
	pts   []Point
	hole  bool
	depth int
	area  float64
	typ   string
	key   float64
}

// trace runs the marching squares over a field of team 1's half (positive inside) and turns its loops into
// polygons in metres: cut at x + y = importCut, rounded to 0.1 m, loops under a cell's area dropped.
func (im *importer) trace(f fogmap.Field, tol float64) ([]traced, error) {
	loops, err := fogmap.Contours(f, Grid, Grid, 0, tol)
	if err != nil {
		return nil, err
	}
	cell := im.m.Size / Grid
	var ts []traced
	for _, lp := range loops {
		raw := make([]Point, 0, len(lp))
		for _, p := range lp {
			raw = append(raw, Point{(p.X - Grid/2) * cell, (p.Y - Grid/2) * cell})
		}
		if len(raw) > 1 && raw[0] == raw[len(raw)-1] {
			raw = raw[:len(raw)-1]
		}
		var pts []Point
		for _, q := range clipHalf(raw, importCut) {
			q = r1p(q)
			if len(pts) > 0 && pts[len(pts)-1] == q {
				continue
			}
			pts = append(pts, q)
		}
		if len(pts) > 1 && pts[0] == pts[len(pts)-1] {
			pts = pts[:len(pts)-1]
		}
		a := signedArea(pts)
		if len(pts) < 3 || math.Abs(a) < cell*cell {
			continue
		}
		ts = append(ts, traced{pts: pts, hole: fogmap.Area(lp) < 0, area: math.Abs(a)})
	}
	return ts, nil
}

// clipHalf clips a closed polygon to the half-plane x + y <= cut (Sutherland-Hodgman): the corners inside kept, an
// edge that crosses the line gets its crossing point. A loop that crosses the line more than once may come back with
// edges along the line both ways; they enclose nothing (no cell centre lies on the line).
func clipHalf(pts []Point, cut float64) []Point {
	var out []Point
	for k := range pts {
		a, b := pts[k], pts[(k+1)%len(pts)]
		sa, sb := a[0]+a[1]-cut, b[0]+b[1]-cut
		if sa <= 0 {
			out = append(out, a)
		}
		if (sa < 0 && sb > 0) || (sa > 0 && sb < 0) {
			t := sa / (sa - sb)
			out = append(out, Point{a[0] + t*(b[0]-a[0]), a[1] + t*(b[1]-a[1])})
		}
	}
	return out
}

// bushes adds team 1's grass patches (their centre in x + y <= 0) as bush shapes: the patch's ellipse as its fog
// outline has it (a 24-gon), or a circle when it is round.
func (im *importer) bushes(ps []grassmap.Patch) {
	var b1, b2 []Point
	for _, p := range ps {
		x0, y0, x1, y1 := p.Rect(im.m.Size)
		c := Point{(x0 + x1) / 2, (y0 + y1) / 2}
		if c[0]+c[1] > 0 {
			b2 = append(b2, c)
			continue
		}
		b1 = append(b1, c)
		a, b := (x1-x0)/2, (y1-y0)/2
		if math.Abs(a-b) < 0.05 {
			cc := r1p(c)
			im.m.Terrain.Shapes = append(im.m.Terrain.Shapes, Shape{Type: "bush", Circle: &cc, R: r1(a)})
			continue
		}
		var pts []Point
		for k := 0; k < 24; k++ {
			t := float64(k) * math.Pi / 12
			pts = append(pts, r1p(Point{c[0] + a*math.Cos(t), c[1] + b*math.Sin(t)}))
		}
		im.m.Terrain.Shapes = append(im.m.Terrain.Shapes, Shape{Type: "bush", Polygon: pts})
	}
	im.mirrorDiff("the bushes (centres)", b1, b2)
}

// joinWells handles every well that doesn't stand on open ground joined to team 1's first spawn point (MM_Map_v3's
// mines stand on islands only NPCs may use): the traced open polygon round it becomes an island (MAPED-ISLAND), or,
// when no traced polygon holds it apart from the base, it gets a clearing and a path to the nearest joined ground.
// It returns the cells the added shapes cover (for the comparison).
func (im *importer) joinWells() (map[int]bool, error) {
	changed := map[int]bool{}
	if len(im.m.Base.Spawns) == 0 {
		return changed, nil
	}
	ly, err := Rasterise(im.m)
	if err != nil {
		return nil, err
	}
	reach := make([]bool, Grid*Grid)
	si, sj := ly.CellOf(im.m.Base.Spawns[0])
	stack := []int{sj*Grid + si}
	if ly.Kind[stack[0]] != Open {
		return changed, nil
	}
	reach[stack[0]] = true
	for len(stack) > 0 {
		k := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		i, j := k%Grid, k/Grid
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			a, b := i+d[0], j+d[1]
			if a < 0 || b < 0 || a >= Grid || b >= Grid {
				continue
			}
			if n := b*Grid + a; !reach[n] && ly.Kind[n] == Open {
				reach[n] = true
				stack = append(stack, n)
			}
		}
	}
	for w, wl := range im.m.Wells {
		i, j := ly.CellOf(wl.Pos)
		if reach[j*Grid+i] {
			continue
		}
		best, bp := math.Inf(1), Point{}
		for k, on := range reach {
			if !on {
				continue
			}
			c := ly.CellCentre(k%Grid, k/Grid)
			if d := dist(c, wl.Pos); d < best {
				best, bp = d, c
			}
		}
		if math.IsInf(best, 1) {
			continue
		}
		c := wl.Pos
		var shapes []Shape
		island := im.islandShape(wl.Pos)
		if island >= 0 {
			// the traced ground round the well is an island; a clearing of the same kind keeps the worker spots
			// (about 22 m round a well; MM's mine islands are about 23 m round) on it
			im.m.Terrain.Shapes[island].Type = "island"
			shapes = []Shape{{Type: "island", Circle: &c, R: WellClearing}}
		} else {
			// carry the path a few metres into the joined ground, so the graphs' erosion keeps it joined
			dir := Point{(bp[0] - wl.Pos[0]) / best, (bp[1] - wl.Pos[1]) / best}
			end := r1p(Point{bp[0] + dir[0]*WellPathWidth/2, bp[1] + dir[1]*WellPathWidth/2})
			shapes = []Shape{{Type: "open", Circle: &c, R: WellClearing},
				{Type: "open", Path: []Point{wl.Pos, end}, Width: WellPathWidth}}
		}
		// before the walls (the shapes after the open outlines) and the bushes: right after the opens and islands
		at := 0
		for at < len(im.m.Terrain.Shapes) && (im.m.Terrain.Shapes[at].Type == "open" ||
			im.m.Terrain.Shapes[at].Type == "island") {
			at++
		}
		rest := append([]Shape(nil), im.m.Terrain.Shapes[at:]...)
		im.m.Terrain.Shapes = append(append(im.m.Terrain.Shapes[:at], shapes...), rest...)
		for _, s := range shapes {
			g, err := geom(im.m, s)
			if err != nil {
				return nil, err
			}
			for _, gg := range []shapeGeom{g, g.mirrored()} {
				for _, k := range ly.cellsIn(gg) {
					changed[k] = true
				}
			}
		}
		if island >= 0 {
			im.note("well %d at (%.1f, %.1f) stands on ground not joined to the base (%.0f m from it): its traced "+
				"ground (terrain.shapes[%d]) and a clearing of %.0f m round it are an island, walked by NPCs only, so "+
				"the well stays out of the players' reach as on the level", w, wl.Pos[0], wl.Pos[1], best, island,
				WellClearing)
			continue
		}
		im.note("well %d at (%.1f, %.1f) stands on ground not joined to the base (%.0f m from it): given an open "+
			"circle of %.0f m and a path of %.0f m to the base, so shapers can reach it (on the level only its workers "+
			"could)", w, wl.Pos[0], wl.Pos[1], best, WellClearing, WellPathWidth)
	}
	return changed, nil
}

// islandShape is the innermost traced open polygon holding p but not team 1's first spawn point (-1 if none).
func (im *importer) islandShape(p Point) int {
	at := -1
	for k, s := range im.m.Terrain.Shapes {
		if s.Type == "open" && s.Polygon != nil && inPolygon(p, s.Polygon) && !inPolygon(im.m.Base.Spawns[0], s.Polygon) {
			at = k // the traced shapes are ordered outer first
		}
	}
	return at
}

// compare fills the report's LoS counts: the level against its mirrors and against the compiled package; with the
// level's walkable cells also the compiled Player graph's against them.
func (im *importer) compare(img los.Image, changed map[int]bool, walk []bool) error {
	if walk != nil && im.rep.Chasm != nil {
		cm, err := Compile(im.m)
		if err != nil {
			return err
		}
		c := im.rep.Chasm
		for k, w := range walk {
			if cm.Mask[k] {
				c.Package++
				if w {
					c.Both++
				}
			}
		}
		if u := c.Walkable + c.Package - c.Both; u > 0 {
			c.Share = math.Round(float64(c.Both)/float64(u)*10000) / 10000
		}
	}
	r := &im.rep.LoS
	open := func(i, j int) bool {
		if i < 0 || j < 0 || i >= Grid || j >= Grid {
			return false
		}
		return img.Pix[j*Grid+i] == 0
	}
	for j := 0; j < Grid; j++ {
		for i := 0; i < Grid; i++ {
			o := open(i, j)
			if o {
				r.LevelOpen++
			}
			if o != open(Grid-i, Grid-j) {
				r.PointMirror++
			}
			if o != open(Grid-j, Grid-i) {
				r.Reflection++
			}
		}
	}
	ly, err := Rasterise(im.m)
	if err != nil {
		return err
	}
	got := ly.LoS()
	for k := range got.Pix {
		if (got.Pix[k] == 0) == (img.Pix[k] == 0) {
			continue
		}
		r.Differ++
		i, j := k%Grid, k/Grid
		switch {
		case changed[k]:
			r.Changed++
		case i+j <= Grid:
			r.Team1Half++
		default:
			r.Team2Half++
		}
	}
	if r.LevelOpen > 0 {
		r.Share = math.Round(float64(r.Differ)/float64(r.LevelOpen)*10000) / 10000
	}
	return nil
}
