package mapc

import (
	"fmt"
	"math"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/botprofiles"
	"dawngate/server/internal/setup/snap"
)

// The map's AssetData side: its Level, its markersets and its terrain noun, written with the assetdata writer. Every
// gameplay marker is a copy of the first map's (SM_Map_v5's) marker of the same noun, moved to the map's place, so
// it keeps that marker's components (the minion spawners' compositions, the island markers' layers, the grass and
// camera data); the copy's AssetData.snap is read at build time and no game bytes ship. The Level is SM_Map_v5's with
// the map's paths, size and markersets, the environment level's music and the tuning level's tunings.

// TemplateLevel is the shipped level whose markers and Level the map's are copied from.
const TemplateLevel = "SM_Map_v5"

// templateSets are TemplateLevel's markersets the gameplay markers come from (its <level>_<set>.Markerset).
var templateSets = []string{"design", "generated_grass", "environment", "followers", "minion_blue", "minion_red",
	"titans", "towers", "nubs", "cameraMortal", "cameraSpirit", templateJungle}

const (
	// EnergyLines is the energy-line tuning every map's Level names: the first map's old one, which draws no line but
	// lets the client show a Binding's death (TowerGraphics_Init needs a loaded tuning).
	EnergyLines = "SM_Map_v4_EnergyLines.EnergyLineTuning"
	// templateTerrainNoun is the terrain noun the map's is copied from (its model is the ground's template too).
	templateTerrainNoun = "sm_v5_terrain.Noun"
	assetDir            = "AssetData/Binary/"
)

// globalLightNouns are the environment level's markers the map takes: its parallel (global) lights and the graphics
// control (ambient light, fog, bloom, shadows). Their places don't matter; the point and spot lights stand in the
// environment level's world, so they are left out.
var globalLightNouns = []string{"ParallelLight.Noun", "GraphicsControl.Noun"}

// The map's markersets, in the Level's order (each <name>_<suffix>.Markerset).
const (
	setEnvironment = "environment"
	setLights      = "lights"
	setGrass       = "generated_grass"
	setDesign      = "design"
	setFollowers   = "followers"
	setMinionBlue  = "minion_blue"
	setMinionRed   = "minion_red"
	setTitans      = "titans"
	setTowers      = "towers"
	setNubs        = "nubs"
	setCamMortal   = "cameraMortal"
	setCamSpirit   = "cameraSpirit"
	setProps       = "props" // only when the map has props (props.go)
)

var mapSets = []string{setEnvironment, setLights, setGrass, setDesign, setFollowers, setMinionBlue, setMinionRed,
	setTitans, setTowers, setNubs, setCamMortal, setCamSpirit}

// AssetNames are the assets a map adds, by full name (<name>.<Class>): its Level, its markersets and its terrain noun.
func AssetNames(name string) []string {
	out := []string{name + ".Level"}
	for _, s := range mapSets {
		out = append(out, name+"_"+s+".Markerset")
	}
	return append(out, TerrainNoun(name))
}

// TerrainNoun is the map's terrain noun: the ground model at the origin.
func TerrainNoun(name string) string { return name + "_terrain.Noun" }

// AssetEntry is an asset's file in the archive.
func AssetEntry(asset string) string { return assetDir + asset + ".bin" }

// Catalog is the map's catalog source: its assets for the copy's one catalog (botprofiles.Build), each written anew
// with its own source file name; the terrain noun carries the shipped terrain nouns' tags.
func Catalog(m *Map) botprofiles.CatalogSource {
	src := botprofiles.CatalogSource{Name: m.Name}
	lv := "Levels/" + m.Name + "/"
	src.Entries = append(src.Entries, botprofiles.CatalogEntry{Name: m.Name + ".Level", Source: lv + m.Name + ".Level"})
	for _, s := range setsOf(m) {
		n := m.Name + "_" + s
		src.Entries = append(src.Entries, botprofiles.CatalogEntry{Name: n + ".Markerset", Source: lv + n + ".Markerset"})
	}
	e := EntriesFor(m.Name)
	src.Entries = append(src.Entries, botprofiles.CatalogEntry{Name: TerrainNoun(m.Name),
		Source: strings.TrimSuffix(e.Model, ".bmdl") + ".Noun", Tags: []string{"placeable", "terrain"}})
	return src
}

// ---------------------------------------------------------------- reading the copy's assets

// assetSource is the copy's AssetData.snap with the parsed values the compiler copies from.
type assetSource struct {
	ts     *assetdata.Types
	arch   *snap.Archive
	byName map[string]snap.Entry
}

func newAssetSource(assetData []byte) (*assetSource, error) {
	ts, err := assetdata.Embedded()
	if err != nil {
		return nil, err
	}
	a, err := snap.Parse(assetData)
	if err != nil {
		return nil, fmt.Errorf("AssetData.snap: %v", err)
	}
	s := &assetSource{ts: ts, arch: a, byName: map[string]snap.Entry{}}
	for _, e := range a.Entries {
		s.byName[strings.ToLower(e.Name)] = e
	}
	return s, nil
}

// object parses an asset ("SM_Map_v5.Level") exactly.
func (s *assetSource) object(asset string) (*assetdata.Object, error) {
	e, ok := s.byName[strings.ToLower(AssetEntry(asset))]
	if !ok {
		return nil, fmt.Errorf("AssetData.snap has no %s", asset)
	}
	b, err := s.arch.Read(e)
	if err != nil {
		return nil, err
	}
	t := s.ts.Type(classOfAsset(asset))
	if t == nil {
		return nil, fmt.Errorf("%s: no type table", asset)
	}
	o, err := s.ts.ParseExact(t, b)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", asset, err)
	}
	return o, nil
}

func classOfAsset(asset string) string {
	_, c, _ := strings.Cut(asset, ".")
	return c
}

// levelSets are a level's markersets (names without .Markerset), in its order.
func levelSets(level *assetdata.Object) ([]string, error) {
	v, _ := level.Get("markersets")
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("the level has no markerset list")
	}
	var out []string
	for _, e := range list {
		o, ok := e.(*assetdata.Object)
		if !ok {
			return nil, fmt.Errorf("a markerset entry isn't a struct")
		}
		a, _ := o.Get("markersetAsset")
		if s, ok := a.(string); ok && s != "" {
			out = append(out, strings.TrimSuffix(s, ".Markerset"))
		}
	}
	return out, nil
}

// markers are a markerset's markers.
func markers(set *assetdata.Object) []*assetdata.Object {
	v, _ := set.Get("markers")
	list, _ := v.([]any)
	out := make([]*assetdata.Object, 0, len(list))
	for _, e := range list {
		if o, ok := e.(*assetdata.Object); ok {
			out = append(out, o)
		}
	}
	return out
}

// templates are the template level's markers by noun and id.
type templates struct {
	set    *assetdata.Object // a markerset of the template level: the group, weight and condition every set copies
	byNoun map[string][]*assetdata.Object
	byID   map[uint64]*assetdata.Object
	bySet  map[string][]*assetdata.Object
}

func readTemplates(src *assetSource) (*templates, error) {
	return readLevelTemplates(src, TemplateLevel, templateSets)
}

// MineLevel is the shipped level a well of kind mine is copied from: its gold mine (gold_<colour>_side_MM, 36 worker
// slots) and the mine's worker spots, in its followers markerset (MAPMINES).
const MineLevel = "MM_Map_v3"

// readLevelTemplates reads a level's markersets (<level>_<set>.Markerset) as templates.
func readLevelTemplates(src *assetSource, level string, sets []string) (*templates, error) {
	t := &templates{byNoun: map[string][]*assetdata.Object{}, byID: map[uint64]*assetdata.Object{},
		bySet: map[string][]*assetdata.Object{}}
	for _, s := range sets {
		o, err := src.object(level + "_" + s + ".Markerset")
		if err != nil {
			return nil, err
		}
		if t.set == nil {
			t.set = o
		}
		for _, m := range markers(o) {
			n := strings.ToLower(text(m, "nounDef"))
			t.byNoun[n] = append(t.byNoun[n], m)
			t.byID[uval(m, "markerId")] = m
			t.bySet[s] = append(t.bySet[s], m)
		}
	}
	return t, nil
}

// first is the first template marker of a noun.
func (t *templates) first(noun string) (*assetdata.Object, error) {
	ms := t.byNoun[strings.ToLower(noun)]
	if len(ms) == 0 {
		return nil, fmt.Errorf("%s has no %s marker", TemplateLevel, noun)
	}
	return ms[0], nil
}

// ---------------------------------------------------------------- value helpers

func text(o *assetdata.Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

func uval(o *assetdata.Object, k string) uint64 {
	v, _ := o.Get(k)
	switch x := v.(type) {
	case uint64:
		return x
	case int64:
		return uint64(x)
	}
	return 0
}

func fval(v any) float64 {
	switch x := v.(type) {
	case float32:
		return float64(x)
	case float64:
		return x
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	}
	return 0
}

// vec is a vector member's components.
func vec(o *assetdata.Object, k string) []float64 {
	v, _ := o.Get(k)
	list, _ := v.([]any)
	out := make([]float64, len(list))
	for i, e := range list {
		out[i] = fval(e)
	}
	return out
}

func f32s(xs ...float64) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = float32(x)
	}
	return out
}

func sub(o *assetdata.Object, k string) *assetdata.Object {
	v, _ := o.Get(k)
	s, _ := v.(*assetdata.Object)
	return s
}

// clone is a deep copy of a parsed value.
func clone(v any) any {
	switch x := v.(type) {
	case *assetdata.Object:
		o := assetdata.NewObject()
		for _, k := range x.Keys() {
			e, _ := x.Get(k)
			o.Set(k, clone(e))
		}
		return o
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = clone(e)
		}
		return out
	}
	return v
}

func cloneObj(o *assetdata.Object) *assetdata.Object { return clone(o).(*assetdata.Object) }

// ---------------------------------------------------------------- the markers

// assetBuild is one map's AssetData build.
type assetBuild struct {
	cm    *Compiled
	src   *assetSource
	tm    *templates
	ids   map[uint32]string // the marker ids taken, by key
	names map[string]int    // the marker names taken, by noun
	sets  map[string][]any  // the map's markersets' markers, by suffix
}

// markerID is a marker's id: FNV-1 of "<name>/<key>" (a key names the object: "lane/1/1/3"), the next free one
// when taken or zero.
func (b *assetBuild) markerID(key string) uint32 {
	k := b.cm.Map.Name + "/" + key
	for {
		id := fnv1Lower(k)
		if prev, taken := b.ids[id]; id != 0 && (!taken || prev == key) {
			b.ids[id] = key
			return id
		}
		k += "+"
	}
}

func fnv1Lower(s string) uint32 {
	h := uint32(0x811C9DC5)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		h *= 0x01000193
		h ^= uint32(c)
	}
	return h
}

// place copies a template marker into a set: its id from key, its name the noun's with a running number (as the
// shipped markers are named), at (x, y) with the template's height and rotation; no target and no children.
func (b *assetBuild) place(set string, tmpl *assetdata.Object, key string, p Point) *assetdata.Object {
	m := cloneObj(tmpl)
	noun := text(m, "nounDef")
	n := b.names[strings.ToLower(noun)]
	b.names[strings.ToLower(noun)] = n + 1
	name := noun
	if n > 0 {
		name = fmt.Sprintf("%s-%d", noun, n)
	}
	m.Set("markerName", name)
	m.Set("markerId", uint64(b.markerID(key)))
	pos := vec(m, "pos")
	m.Set("pos", f32s(p[0], p[1], pos[2]))
	m.Set("targetMarkerId", uint64(0))
	m.Set("childMarkerIds", []any{})
	b.sets[set] = append(b.sets[set], m)
	return m
}

func setChildren(m *assetdata.Object, ids ...uint32) {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = uint64(id)
	}
	m.Set("childMarkerIds", out)
}

var teamColour = [2]string{"blue", "red"}
var teamName = [2]string{"Mortal", "Spirit"}
var laneName = map[int]string{1: "top", 2: "bottom"}
var laneNoun = map[int]string{1: "Top", 2: "Bottom"}

// smLane is the lane a template marker belongs to: Top (1) on the diagonal's north-west side.
func smLane(m *assetdata.Object) int {
	p := vec(m, "pos")
	if p[1]-p[0] >= 0 {
		return 1
	}
	return 2
}

// spline is a template spline's knot.
func (t *templates) knot(spline string, k int) (*assetdata.Object, error) {
	for _, m := range t.byNoun["splinepathnode.noun"] {
		d := sub(m, "splinePathNodeData")
		if d != nil && text(d, "name") == spline && int(fval(mustGet(d, "knot"))) == k {
			return m, nil
		}
	}
	return nil, fmt.Errorf("%s has no knot %d of the spline %s", TemplateLevel, k, spline)
}

func mustGet(o *assetdata.Object, k string) any {
	v, _ := o.Get(k)
	return v
}

// lanes places each team's splines (one per lane id, its knots the team's walk) and minion spawners (each pointing at
// its spline's head knot). The lane id and team are on every knot (the host reads the head knot's).
func (b *assetBuild) lanes() (spawners [2][]uint32, err error) {
	head, err := b.tm.knot("blue_top", 0)
	if err != nil {
		return spawners, err
	}
	rest, err := b.tm.knot("blue_top", 2)
	if err != nil {
		return spawners, err
	}
	for t, set := range []string{setMinionBlue, setMinionRed} {
		team := &b.cm.Layout.Teams[t]
		for _, id := range b.cm.Layout.LaneIDs {
			name := teamColour[t] + "_" + laneName[id]
			var headID uint32
			for k, p := range team.Lanes[id] {
				tm := rest
				if k == 0 {
					tm = head
				}
				m := b.place(set, tm, fmt.Sprintf("lane/%d/%d/%d", t+1, id, k), p)
				d := cloneObj(sub(tm, "splinePathNodeData"))
				d.Set("name", name)
				d.Set("knot", int64(k))
				d.Set("lane", int64(id))
				d.Set("team", int64(t+1))
				m.Set("splinePathNodeData", d)
				if k == 0 {
					headID = uint32(uval(m, "markerId"))
				}
			}
			tm, err := b.tm.first(fmt.Sprintf("SpawnPoint_v5_%s_%s.Noun", teamName[t], laneNoun[id]))
			if err != nil {
				return spawners, err
			}
			m := b.place(set, tm, fmt.Sprintf("spawner/%d/%d", t+1, id), team.Spawners[id])
			m.Set("targetMarkerId", uint64(headID))
			spawners[t] = append(spawners[t], uint32(uval(m, "markerId")))
		}
	}
	return spawners, nil
}

// structures places each team's Bindings (per lane, Outer, Middle and Inner, or MM_Map_v3's Middle and Inner, each
// pointing at the next as its child, as the shipped ones do; the keys count from the outermost), its Locus and its
// Cores (A to E, or A to C).
func (b *assetBuild) structures() error {
	for t := range 2 {
		team := &b.cm.Layout.Teams[t]
		for _, id := range b.cm.Layout.LaneIDs {
			if len(team.Bindings[id]) == 0 { // a lane of a one-path map without its own (fixed.bindings)
				continue
			}
			ms := make([]*assetdata.Object, len(team.Bindings[id]))
			for k, tier := range TierNames(len(ms)) {
				noun := fmt.Sprintf("Binding_%s_%s.Noun", teamName[t], tier)
				cands := b.tm.byNoun[strings.ToLower(noun)]
				if len(cands) == 0 {
					return fmt.Errorf("%s has no %s marker", TemplateLevel, noun)
				}
				tm := cands[0]
				for _, c := range cands {
					if smLane(c) == id {
						tm = c
						break
					}
				}
				ms[k] = b.place(setTowers, tm, fmt.Sprintf("binding/%d/%d/%d", t+1, id, k), team.Bindings[id][k])
			}
			for k := 0; k+1 < len(ms); k++ {
				setChildren(ms[k], uint32(uval(ms[k+1], "markerId")))
			}
		}
		tm, err := b.tm.first(fmt.Sprintf("DEST_prefab_%s_tower_v5.Noun", teamColour[t]))
		if err != nil {
			return err
		}
		b.place(setTowers, tm, fmt.Sprintf("locus/%d", t+1), team.Locus)
		for k, p := range team.Cores {
			tm, err := b.tm.first(fmt.Sprintf("nub_%s_v5_%c.Noun", strings.ToLower(teamName[t]), 'A'+k))
			if err != nil {
				return err
			}
			b.place(setNubs, tm, fmt.Sprintf("core/%d/%d", t+1, k), p)
		}
	}
	return nil
}

// titans places each team's Guardian: its titan node (the noun names the Guardian) pointing at its spawn point, its
// children the team's minion spawners and, for team 1, team 2's node (as the shipped nodes have them).
func (b *assetBuild) titans(spawners [2][]uint32) error {
	spots := b.tm.byNoun["titanspawnpoint.noun"]
	for t := range 2 {
		g := b.cm.Layout.Teams[t].Guardian
		var tm *assetdata.Object
		for _, s := range spots {
			if p := vec(s, "pos"); (p[0]+p[1] < 0) == (t == 0) {
				tm = s
				break
			}
		}
		if tm == nil {
			return fmt.Errorf("%s has no TitanSpawnPoint marker of team %d", TemplateLevel, t+1)
		}
		spot := b.place(setTitans, tm, fmt.Sprintf("guardian/%d/spot", t+1), g)
		nt, err := b.tm.first(fmt.Sprintf("titan_node_%s_base_v5.Noun", teamColour[t]))
		if err != nil {
			return err
		}
		node := b.place(setTitans, nt, fmt.Sprintf("guardian/%d/node", t+1), g)
		node.Set("targetMarkerId", uval(spot, "markerId"))
		kids := append([]uint32(nil), spawners[t]...)
		if t == 0 {
			kids = append(kids, b.markerID("guardian/2/node"))
		}
		setChildren(node, kids...)
	}
	return nil
}

// wells places each well: the template well of its team's noun with its worker spots (its children) and each spot's
// target, all moved by the well's offset from the template's. A well of kind mine takes MineLevel's mine and spots
// instead of TemplateLevel's well (read only when the map has one, so a map without keeps its bytes). It reports a
// well with a spot on blocked ground.
func (b *assetBuild) wells() ([]Finding, error) {
	var fs []Finding
	var mine *templates
	for _, w := range b.cm.Map.Wells {
		if w.IsMine() && mine == nil {
			var err error
			if mine, err = readLevelTemplates(b.src, MineLevel, []string{setFollowers}); err != nil {
				return nil, err
			}
		}
	}
	for t := range 2 {
		for w, p := range b.cm.Layout.Teams[t].Wells {
			src, level, noun, ring := b.tm, TemplateLevel, fmt.Sprintf("gold_%s_side.Noun", teamColour[t]), "about 22"
			if b.cm.Map.Wells[w].IsMine() {
				src, level, noun, ring = mine, MineLevel, fmt.Sprintf("gold_%s_side_MM.Noun", teamColour[t]),
					"15.5 to 18.2"
			}
			ms := src.byNoun[strings.ToLower(noun)]
			if len(ms) == 0 {
				return nil, fmt.Errorf("%s has no %s marker", level, noun)
			}
			tm := ms[0]
			tp := vec(tm, "pos")
			dx, dy := p[0]-tp[0], p[1]-tp[1]
			moved := func(m *assetdata.Object) Point {
				q := vec(m, "pos")
				return Point{q[0] + dx, q[1] + dy}
			}
			well := b.place(setFollowers, tm, fmt.Sprintf("well/%d/%d", t+1, w), p)
			var kids []uint32
			blocked := 0
			for k, c := range vec(tm, "childMarkerIds") {
				spot, ok := src.byID[uint64(c)]
				if !ok {
					return nil, fmt.Errorf("%s: the well's worker spot %d is missing", level, uint64(c))
				}
				sp := moved(spot)
				m := b.place(setFollowers, spot, fmt.Sprintf("well/%d/%d/spot/%d", t+1, w, k), sp)
				if look, ok := src.byID[uval(spot, "targetMarkerId")]; ok {
					l := b.place(setFollowers, look, fmt.Sprintf("well/%d/%d/look/%d", t+1, w, k), moved(look))
					m.Set("targetMarkerId", uval(l, "markerId"))
				}
				kids = append(kids, uint32(uval(m, "markerId")))
				if !Walkable(b.cm.Layers.KindAt(sp)) {
					blocked++
				}
			}
			setChildren(well, kids...)
			if t == 0 && blocked > 0 {
				fs = append(fs, Finding{Rule: "wells.spots", Severity: Warning, Path: fmt.Sprintf("wells[%d].pos", w),
					At: &Point{p[0], p[1]}, Message: fmt.Sprintf("%d of the well's %d worker spots stand on blocked "+
						"ground (they stand %s m round the well; open that ground)", blocked, len(kids), ring)})
			}
		}
	}
	return fs, nil
}

// centre is the mean of markers' positions.
func centre(ms []*assetdata.Object) Point {
	var c Point
	for _, m := range ms {
		p := vec(m, "pos")
		c[0] += p[0] / float64(len(ms))
		c[1] += p[1] / float64(len(ms))
	}
	return c
}

// design places the base: the player spawn points, the base zone (the item shop's reach; a spawn-point noun with a
// team and the catalog tag baseMarker), the shop archons (moved with the spawn points' centre, as MM_Map_v3's are),
// and the island markers: an NPC-only one on each Guardian's platform and on each well on an island, one for players
// and NPCs at each team's first spawn point.
func (b *assetBuild) design() error {
	zones := [2]string{"Base_Health_Regen_Defenders.Noun", "Base_Health_Regen_Attackers.Noun"}
	var npcOnly, both *assetdata.Object
	for _, m := range b.tm.byNoun["navmeshmarker_valid.noun"] {
		d := sub(m, "navmeshMarkerData")
		if d == nil {
			continue
		}
		pl, _ := d.Get("allowPlayersHere")
		if pl == true && both == nil {
			both = m
		} else if pl == false && npcOnly == nil {
			npcOnly = m
		}
	}
	if npcOnly == nil || both == nil {
		return fmt.Errorf("%s lacks its island markers", TemplateLevel)
	}
	for t := range 2 {
		team := &b.cm.Layout.Teams[t]
		spawnNoun := fmt.Sprintf("SpawnPoint_Team%d.Noun", t+1)
		tms := b.tm.byNoun[strings.ToLower(spawnNoun)]
		if len(tms) == 0 {
			return fmt.Errorf("%s has no %s marker", TemplateLevel, spawnNoun)
		}
		for k, p := range team.Spawns {
			b.place(setDesign, tms[k%len(tms)], fmt.Sprintf("spawn/%d/%d", t+1, k), p)
		}
		zt, err := b.tm.first(zones[t])
		if err != nil {
			return err
		}
		b.place(setDesign, zt, fmt.Sprintf("shop/%d", t+1), team.Shop)
		archons := b.tm.byNoun[strings.ToLower(fmt.Sprintf("itemshoppe_archon_%s.Noun", teamName[t]))]
		sc, ac := team.SpawnCentre(), centre(tms)
		for k, a := range archons {
			p := vec(a, "pos")
			b.place(setDesign, a, fmt.Sprintf("archon/%d/%d", t+1, k), Point{p[0] + sc[0] - ac[0], p[1] + sc[1] - ac[1]})
		}
		b.place(setDesign, npcOnly, fmt.Sprintf("island/%d/guardian", t+1), team.Guardian)
		b.place(setDesign, both, fmt.Sprintf("island/%d/base", t+1), team.Spawns[0])
		// MAPED-ISLAND: an NPC-only marker on each well that stands on an island, as MM_Map_v3's on its mines (0.6 m
		// from them); the match host leaves such a well out of the bots' well missions (MAPMINES)
		for _, w := range b.cm.IslandWells(t) {
			b.place(setDesign, npcOnly, fmt.Sprintf("island/%d/well/%d", t+1, w), team.Wells[w])
		}
	}
	return nil
}

// cameras places the Guardians' death cameras (the end cinematics' cameraMortal and cameraSpirit, each a camera and
// its node), moved by the Guardian's offset from the template level's.
func (b *assetBuild) cameras() error {
	for t, set := range []string{setCamMortal, setCamSpirit} {
		ms := b.tm.bySet[set]
		nodes := b.tm.byNoun[fmt.Sprintf("titan_node_%s_base_v5.noun", teamColour[t])]
		if len(ms) == 0 || len(nodes) == 0 {
			return fmt.Errorf("%s_%s has no markers or no Guardian", TemplateLevel, set)
		}
		gp, g := vec(nodes[0], "pos"), b.cm.Layout.Teams[t].Guardian
		for k, m := range ms {
			p := vec(m, "pos")
			c := b.place(set, m, fmt.Sprintf("camera/%d/%d", t+1, k), Point{p[0] + g[0] - gp[0], p[1] + g[1] - gp[1]})
			c.Set("markerName", text(m, "markerName"))
		}
	}
	return nil
}

// grass places one Grass marker per bush: the template's grass data with the map's grass map as the cut, the bush's
// name as the outline (its grass id is FNV-1 of it) and its bounding box as the rectangle (u, v = x, y / size + 0.5),
// standing at the box's centre.
func (b *assetBuild) grass() error {
	tm, err := b.tm.first("Grass.Noun")
	if err != nil {
		return err
	}
	size := b.cm.Map.Size
	e := EntriesFor(b.cm.Map.Name)
	for k, bush := range b.cm.Layers.Bushes {
		x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, p := range bush.Outline {
			x0, y0, x1, y1 = math.Min(x0, p[0]), math.Min(y0, p[1]), math.Max(x1, p[0]), math.Max(y1, p[1])
		}
		m := b.place(setGrass, tm, fmt.Sprintf("bush/%d", k), Point{(x0 + x1) / 2, (y0 + y1) / 2})
		m.Set("markerName", "Grass_"+bush.Name+".Noun")
		g := cloneObj(sub(tm, "grassData"))
		g.Set("cut", e.Grass)
		g.Set("outline", bush.Name)
		g.Set("cutTopLeft", f32s(x0/size+0.5, y0/size+0.5))
		g.Set("cutBottomRight", f32s(x1/size+0.5, y1/size+0.5))
		sz := vec(g, "size")
		g.Set("size", f32s(x1-x0, y1-y0, sz[2]))
		g.Set("seed", int64(bush.ID&0xFFFF))
		m.Set("grassData", g)
	}
	return nil
}

// environment places the ground (the map's terrain noun at the origin) and the environment level's global lights.
func (b *assetBuild) environment(envLevel *assetdata.Object) error {
	var tm *assetdata.Object
	for _, m := range b.tm.bySet["environment"] {
		if strings.EqualFold(text(m, "nounDef"), templateTerrainNoun) {
			tm = m
		}
	}
	if tm == nil {
		return fmt.Errorf("%s_environment has no %s marker", TemplateLevel, templateTerrainNoun)
	}
	m := cloneObj(tm)
	m.Set("nounDef", TerrainNoun(b.cm.Map.Name))
	b.place(setEnvironment, m, "terrain", Point{0, 0})
	sets, err := levelSets(envLevel)
	if err != nil {
		return err
	}
	n := 0
	for _, s := range sets {
		o, err := b.src.object(s + ".Markerset")
		if err != nil {
			return err
		}
		for _, lm := range markers(o) {
			for _, want := range globalLightNouns {
				if strings.EqualFold(text(lm, "nounDef"), want) {
					p := vec(lm, "pos")
					c := b.place(setLights, lm, fmt.Sprintf("light/%d", n), Point{p[0], p[1]})
					c.Set("markerName", text(lm, "markerName"))
					if v := b.cm.Map.Void; v != nil && v.Fog != nil { // void.go: the map's own height fog
						SetHeightFog(c, v.Fog.Start, v.Fog.End, v.Fog.Color)
					}
					n++
				}
			}
		}
	}
	if n == 0 {
		return fmt.Errorf("the environment level has no global light")
	}
	return nil
}

// sharedLevel is the shipped level's own name (SharedLevels' spelling) for a map's environment or tuning.
func sharedLevel(name string) string {
	for _, l := range SharedLevels {
		if strings.EqualFold(l, name) {
			return l
		}
	}
	return name
}

// level is the map's Level: the template level's with the map's markersets, files (EntriesFor's paths; the Keys
// name the level folder as their group, as the shipped ones do), size and the energy-line tuning; the music from the
// environment level; the item set and the tunings from the tuning level. The end cinematics stay the template's.
func (b *assetBuild) level(tmpl, env, tuning *assetdata.Object) *assetdata.Object {
	n, e := b.cm.Map.Name, EntriesFor(b.cm.Map.Name)
	lv := cloneObj(tmpl)
	var sets []any
	for _, s := range setsOf(b.cm.Map) {
		o := assetdata.NewObject()
		o.Set("markersetAsset", n+"_"+s+".Markerset")
		sets = append(sets, o)
	}
	lv.Set("markersets", sets)
	lv.Set("music", clone(mustGet(env, "music")))
	key := func(path string) string { return n + "!" + path[strings.LastIndex(path, "/")+1:] }
	lv.Set("navMeshPlayer", key(e.Player))
	lv.Set("navMeshPathPlayer", e.Player)
	lv.Set("navMeshNPC", key(e.NPC))
	lv.Set("navMeshPathNPC", e.NPC)
	lv.Set("physicsMesh", key(e.Mesh))
	lv.Set("physicsMeshPath", e.Mesh)
	lv.Set("outlinePath", e.Outlines)
	lv.Set("lineOfSightMap", e.LoS)
	lv.Set("grassMap", e.Grass)
	lv.Set("miniMap", e.Minimap)
	lv.Set("tagMap", e.TagImage)
	lv.Set("levelSize", f32s(b.cm.Map.Size, b.cm.Map.Size))
	for _, k := range []string{"activeItems", "rolesTuning", "levelUpTuning", "rewardsTuning", "gameplayTuning"} {
		lv.Set(k, clone(mustGet(tuning, k)))
	}
	lv.Set("energyLineTuning", EnergyLines)
	return lv
}

// terrainNoun is the map's terrain noun: the template terrain noun naming the map's ground model (its Key's group
// the model's folder, as the shipped nouns' are), its box the model's bounds.
func terrainNoun(tmpl *assetdata.Object, model string, bounds []float32) (*assetdata.Object, error) {
	if len(bounds) != 8 {
		return nil, fmt.Errorf("the ground model has no bounds")
	}
	o := cloneObj(tmpl)
	dir := model[:strings.LastIndex(model, "/")]
	o.Set("modelKey", dir[strings.LastIndex(dir, "/")+1:]+"!"+model[len(dir)+1:])
	o.Set("modelPath", model)
	box := assetdata.NewObject()
	c, h := bounds[0:3], bounds[4:7]
	box.Set("min", []any{c[0] - h[0], c[1] - h[1], c[2] - h[2]})
	box.Set("max", []any{c[0] + h[0], c[1] + h[1], c[2] + h[2]})
	o.Set("bbox", box)
	return o, nil
}

// MapAssets are a map's new assets, written: by full name (AssetNames' order) the value and the file's bytes.
type MapAssets struct {
	Names    []string
	Values   map[string]*assetdata.Object
	Files    map[string][]byte
	Findings []Finding // the warnings placing the markers raised (wells.spots)
}

// Assets writes the map's Level, markersets and terrain noun from the copy's AssetData.snap. modelBounds are the
// ground model's (bake.CheckModel's: centre, radius, half extents, radius squared).
func (cm *Compiled) Assets(assetData []byte, modelBounds []float32) (*MapAssets, error) {
	src, err := newAssetSource(assetData)
	if err != nil {
		return nil, err
	}
	tm, err := readTemplates(src)
	if err != nil {
		return nil, err
	}
	b := &assetBuild{cm: cm, src: src, tm: tm, ids: map[uint32]string{}, names: map[string]int{},
		sets: map[string][]any{}}
	tmplLevel, err := src.object(TemplateLevel + ".Level")
	if err != nil {
		return nil, err
	}
	env, err := src.object(sharedLevel(cm.Map.Environment) + ".Level")
	if err != nil {
		return nil, err
	}
	tuning, err := src.object(sharedLevel(cm.Map.Tuning) + ".Level")
	if err != nil {
		return nil, err
	}
	if err := b.environment(env); err != nil {
		return nil, err
	}
	if err := b.grass(); err != nil {
		return nil, err
	}
	if err := b.decals(); err != nil { // decals.go
		return nil, err
	}
	if err := b.water(); err != nil { // water.go
		return nil, err
	}
	if err := b.design(); err != nil {
		return nil, err
	}
	if err := b.wardMarkers(); err != nil { // wards.go
		return nil, err
	}
	fs, err := b.wells()
	if err != nil {
		return nil, err
	}
	spawners, err := b.lanes()
	if err != nil {
		return nil, err
	}
	if err := b.laneAgents(); err != nil { // camps.go
		return nil, err
	}
	if err := b.titans(spawners); err != nil {
		return nil, err
	}
	campFs, err := b.camps() // camps.go
	if err != nil {
		return nil, err
	}
	fs = append(fs, campFs...)
	if err := b.structures(); err != nil {
		return nil, err
	}
	if err := b.cameras(); err != nil {
		return nil, err
	}
	pfs, err := b.props() // props.go
	if err != nil {
		return nil, err
	}
	fs = append(fs, pfs...)
	noun, err := src.object(templateTerrainNoun)
	if err != nil {
		return nil, err
	}
	tn, err := terrainNoun(noun, EntriesFor(cm.Map.Name).Model, modelBounds)
	if err != nil {
		return nil, err
	}

	out := &MapAssets{Names: MapAssetNames(cm.Map), Values: map[string]*assetdata.Object{}, Files: map[string][]byte{},
		Findings: fs}
	n := cm.Map.Name
	out.Values[n+".Level"] = b.level(tmplLevel, env, tuning)
	for _, s := range setsOf(cm.Map) {
		set := assetdata.NewObject()
		set.Set("markers", append([]any{}, b.sets[s]...))
		for _, k := range []string{"group", "weight", "condition"} {
			set.Set(k, clone(mustGet(tm.set, k)))
		}
		out.Values[n+"_"+s+".Markerset"] = set
	}
	out.Values[TerrainNoun(n)] = tn
	for _, name := range out.Names {
		t := src.ts.Type(classOfAsset(name))
		bs, err := src.ts.Write(t, out.Values[name], assetdata.WriteOptions{})
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		out.Files[name] = bs
	}
	return out, nil
}

// MarkerCount is the number of markers in the map's markersets (for a summary).
func (a *MapAssets) MarkerCount() map[string]int {
	out := map[string]int{}
	for _, n := range a.Names {
		if strings.HasSuffix(n, ".Markerset") {
			out[n] = len(markers(a.Values[n]))
		}
	}
	return out
}
