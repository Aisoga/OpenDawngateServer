package mapc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/mapmm/navgraph"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

var mapsDir = testmaps.Dir()

// TestFormatMapRoundTrip: every package of maps/ written by FormatMap reads back as the same map.
func TestFormatMapRoundTrip(t *testing.T) {
	ents, err := os.ReadDir(mapsDir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		m, err := Load(filepath.Join(mapsDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text, err := FormatMap(m)
		if err != nil {
			t.Fatal(err)
		}
		back, err := Parse(text)
		if err != nil {
			t.Fatalf("%s: %v\n%s", e.Name(), err, text)
		}
		a, _ := json.Marshal(m)
		b, _ := json.Marshal(back)
		if !bytes.Equal(a, b) {
			k := 0
			for k < len(a) && k < len(b) && a[k] == b[k] {
				k++
			}
			lo := max(0, k-60)
			t.Errorf("%s: the written map reads back differently at %d:\n%s\n%s", e.Name(), k, a[lo:min(len(a), k+60)],
				b[lo:min(len(b), k+60)])
		}
		for _, line := range strings.Split(string(text), "\n") {
			if len(line) > wrapLimit+40 && strings.Contains(line, "], [") && !strings.Contains(line, `"ground"`) {
				t.Errorf("%s: a long line: %s", e.Name(), line)
				break
			}
		}
		n++
	}
	if n < 8 {
		t.Errorf("%d packages", n)
	}
}

// TestOneLaneBindings: lanes that are one path (CM_PushB's, 34 m apart) may give the Bindings on one lane only (MM_Map_v3's
// two a team, MAPED-IMPORT); team 2's are the mirror on the other lane, the tag map still finds both Inner lines;
// separate lanes (CM_Ring's) still need both.
func TestOneLaneBindings(t *testing.T) {
	m := pushMap(t, "CM_PushB")
	delete(m.Base.Bindings.Lanes, "2")
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	for _, f := range r.Findings {
		if strings.HasPrefix(f.Rule, "fixed.") {
			t.Errorf("%+v", f)
		}
	}
	t1, t2 := cm.Layout.Teams[0], cm.Layout.Teams[1]
	if len(t1.Bindings[1]) != 2 || len(t1.Bindings[2]) != 0 || len(t2.Bindings[2]) != 2 || len(t2.Bindings[1]) != 0 {
		t.Errorf("team 1 %v, team 2 %v", t1.Bindings, t2.Bindings)
	}
	s := cm.TagSpec()
	if len(s.Bindings) != 4 || s.Inner[0].X != t1.Bindings[1][1][0] || s.Inner[1].X != t2.Bindings[2][1][0] {
		t.Errorf("tag spec %+v", s)
	}
	if !LanesShareOnePath(m.Lanes) {
		t.Error("CM_PushB's lanes aren't one path")
	}
	rm := ring(t)
	rm.Base.Bindings = Bindings{Lanes: map[string][]Point{}}
	for _, ln := range rm.Lanes {
		rm.Base.Bindings.Lanes[string(rune('0'+ln.ID))] = AutoBindings(ln.ID, ln.Knots)
	}
	delete(rm.Base.Bindings.Lanes, "2")
	if LanesShareOnePath(rm.Lanes) {
		t.Error("CM_Ring's lanes are one path")
	}
	r, _ = Check(rm, nil)
	if !has(r, "fixed.bindings", Error) {
		t.Errorf("CM_Ring without lane 2's Bindings: %+v", r.Findings)
	}
}

func importInputs(t *testing.T) ImportInputs {
	t.Helper()
	dir := testgame.Dir(t)
	var in ImportInputs
	for k, rel := range ImportSources {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if k == 0 {
			in.AssetData = b
		} else {
			in.Images = b
		}
	}
	return in
}

// diagonalGaps counts the cells on the diagonal (i + j = Grid, x + y = 0) that the level has open and the compiled
// layers have as wall while both their neighbours across it (team 1's (i-1, j) and team 2's (i+1, j)) are open in
// the layers: a strip the two halves' outlines both miss.
func diagonalGaps(img los.Image, ly *Layers) int {
	n := 0
	for i := 1; i < Grid-1; i++ {
		j := Grid - i
		if j <= 0 || j >= Grid {
			continue
		}
		k := j*Grid + i
		if img.Pix[k] == 0 && ly.Kind[k] == Wall && ly.Kind[k-1] != Wall && ly.Kind[k+1] != Wall {
			n++
		}
	}
	return n
}

// passStatues are the two Binding plinths added to CM_Pass by hand after its import (mirrored for team 2), lifted
// 5 cm off the floor: their top face is the model's z 0, flush with the ground at z 0 they draw torn (props.flush).
func passStatues() []Prop {
	return []Prop{{Noun: "darius_tower_binding", Pos: [3]float64{-40.1, -46.2, 0.05}, Rot: 116.4, Scale: 1},
		{Noun: "darius_tower_binding", Pos: [3]float64{-86.5, -108.8, 0.05}, Rot: 116.4, Scale: 1}}
}

// passVoid is CM_Pass's void: the look tuned live on MM_Map_v3 (the ground cut 1.5 m past the walkable edge, the lit
// black backdrop at z -50, the mist 0.2 m under the floor with FogRange 10 in a pale cyan).
func passVoid() *Void {
	return &Void{Cut: 1.5, Backdrop: &VoidBackdrop{Z: -50, Texture: TextureRef{RGB: &[3]float64{0, 0, 0}}},
		Mist: &VoidMist{Z: -0.2, Range: 10, Color: [3]float64{0.467, 0.824, 0.859}}}
}

// TestGameImportMM: MM_Map_v3 from build 854's files with what was added by hand (passStatues, passVoid) gives
// the fixture CM_Pass (server/testdata/maps) byte for byte, which passes every rule, as the import alone does; the traced half differs from the
// level's line-of-sight map in under 1 % of its open cells, the whole map in under 6 % (the mirror and the mines'
// clearings); the level's own team 2 lies within 1.5 m of team 1's mirror; its two Bindings a team go on one lane; its
// mine gets a clearing and a path; the archive builds.
func TestGameImportMM(t *testing.T) {
	in := importInputs(t)
	m, rep, err := Import("MM_Map_v3", in, ImportOptions{Name: "CM_Pass", Display: "Pass (edit)", Tag: "pass",
		Prefix: "[Pass] ", Dress: true})
	if err != nil {
		t.Fatal(err)
	}
	// the package is the import plus what was added to it by hand: the two Binding statues on team 1's lane (the
	// user's) and MM_Map_v3's void look (MAPED-VOID)
	pkg := *m
	pkg.Props = append(append([]Prop{}, m.Props...), passStatues()...)
	pkg.Void = passVoid()
	text, err := FormatMap(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(mapsDir, "CM_Pass", FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(text, bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) { // a Windows checkout's CRLF
		t.Errorf("the import differs from the fixture CM_Pass/map.json:\n%s", text)
	}
	if p, err := Parse(text); err != nil {
		t.Fatal(err)
	} else if r, _ := Check(p, nil); !r.OK || len(r.Findings) != 0 {
		t.Errorf("the package: %+v", r.Findings)
	}
	// the import itself
	imported, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(imported)
	if err != nil {
		t.Fatal(err)
	}
	r, cm := Check(back, nil)
	if !r.OK || len(r.Findings) != 0 {
		t.Errorf("%+v", r.Findings)
	}
	l := rep.LoS
	if l.LevelOpen != 37202 || float64(l.Team1Half) > 0.01*float64(l.LevelOpen) || l.Share > 0.06 || l.Changed == 0 {
		t.Errorf("line of sight %+v", l)
	}
	for _, d := range rep.Mirror {
		if !d.Spline && (d.Max < 0 || d.Max > 1.5 || d.Note != "") {
			t.Errorf("mirror %+v", d)
		}
	}
	if len(m.Base.Bindings.Lanes) != 1 || len(m.Base.Cores.Points) != 3 || len(m.Wells) != 1 || len(m.Lanes) != 2 ||
		len(m.Lanes[0].Knots) > MaxKnots || m.Environment != "MM_Map_v3" || m.Tuning != "MM_Map_v3" {
		t.Errorf("%+v", m)
	}
	joined := false
	for _, c := range rep.Changes {
		joined = joined || strings.Contains(c, "not joined to the base") && strings.Contains(c, "an island")
	}
	if !joined {
		t.Errorf("changes %v", rep.Changes)
	}
	// MAPED-ISLAND: the mines stay on islands only NPCs walk, as on the level
	if a, b := cm.IslandWells(0), cm.IslandWells(1); len(a) != 1 || len(b) != 1 || r.Stats.IslandCells == 0 {
		t.Errorf("island wells %v, %v; stats %+v", a, b, *r.Stats)
	}
	for tm := range 2 {
		w := cm.Layout.Teams[tm].Wells[0]
		d := cm.PlayerDistance(w)
		if d < IslandWorkerReach {
			t.Errorf("team %d's mine: the Player graph %.1f m from it", tm+1, d)
		}
		t.Logf("team %d's mine at (%.1f, %.1f): the Player graph %.1f m from it", tm+1, w[0], w[1], d)
	}
	bushes := 0
	for _, s := range m.Terrain.Shapes {
		if s.Type == "bush" {
			bushes++
		}
	}
	if bushes != 2 || len(cm.Layers.Bushes) != 4 {
		t.Errorf("%d bushes authored", bushes)
	}
	// the compiled layout against the level's own line-of-sight map
	raw, err := los.FromArchive(in.Images, LoSEntry("MM_Map_v3"))
	if err != nil {
		t.Fatal(err)
	}
	img, _ := los.Read(raw)
	diff := 0
	for k, v := range cm.Layers.LoS().Pix {
		if (v == 0) != (img.Pix[k] == 0) {
			diff++
		}
	}
	if diff != l.Differ {
		t.Errorf("%d cells differ, the report says %d", diff, l.Differ)
	}
	// MAPED-IMPORT-FIT: the graphs keep the level's squares of 2 cells (the budget would pick 3), and no cell on the
	// diagonal that the level and both its neighbours have open is wall (the lane crossing's strip, before the clip)
	if m.Graph == nil || m.Graph.Cell != navgraph.MM.Cell || cm.Cell != navgraph.MM.Cell {
		t.Errorf("graph %+v, compiled with squares of %d cells", m.Graph, cm.Cell)
	}
	if n := diagonalGaps(img, cm.Layers); n != 0 {
		t.Errorf("%d cells on the diagonal are wall between open ground", n)
	}
	bin, err := ReadInputs(testgame.Dir(t))
	if err != nil {
		t.Fatal(err)
	}
	as, err := cm.Assets(bin.AssetData, madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	checkAssets(t, back, as)
	// each mine's NPC-only island marker, as MM_Map_v3's (the match host leaves such a well out of the bots' getter)
	at, players := islandMarkers(as)
	for tm := range 2 {
		w, found := cm.Layout.Teams[tm].Wells[0], false
		for k, p := range at {
			found = found || dist(p, w) < 0.01 && !players[k]
		}
		if !found {
			t.Errorf("team %d's mine has no NPC-only island marker: %v %v", tm+1, at, players)
		}
	}
	// MAPED-MINE: the mines are MM_Map_v3's (kind mine), each with its 36 worker spots 15.5 to 18.2 m round it
	if len(m.Wells) != 1 || !m.Wells[0].IsMine() {
		t.Errorf("wells %+v", m.Wells)
	}
	checkMines(t, as, back.Name, "gold_%s_side_MM.Noun", 36, 15.4, 18.3)
	for _, f := range as.Findings {
		if f.Rule == "wells.spots" {
			t.Errorf("placing finding %+v", f)
		}
	}
	checkDress(t, rep, cm, bin.AssetData)
}

// checkDress: MM_Map_v3's eight sets with dressing come as groups that stand once over the whole level (its
// environment is reported, not taken; the towers' Bindings and Locus and the lights' global lights are left out), and
// they place the level's own 640 dressing markers, both halves as the level has them (sameDressing), among them the
// Guardian's platform (darius_titan_bath) at both Guardians and the towers' four range rings a side.
func checkDress(t *testing.T, rep *ImportReport, cm *Compiled, assetData []byte) {
	t.Helper()
	d := rep.Dress
	if d == nil {
		t.Fatal("no dressing report")
	}
	var taken, skipped []string
	for _, s := range d.Taken {
		taken = append(taken, s.Set)
	}
	for _, s := range d.Skipped {
		skipped = append(skipped, s.Set)
	}
	if strings.Join(taken, ",") != "towers,lights,objects,rocks,trees,brush,foliage,VFX" ||
		strings.Join(skipped, ",") != "environment" || d.Members != 640 || d.unmatched() != 161 ||
		len(d.Taken[0].LeftOut) != 8 ||
		strings.Join(d.Taken[1].LeftOut, ",") != "GraphicsControl x1,ParallelLight x2" {
		t.Errorf("dressing %+v", d)
	}
	pls, fs, err := cm.PlacedProps(assetData)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].Rule != "props.group" || fs[0].Path != "props[0].group" || fs[1].Path != "props[1].group" {
		t.Errorf("placing findings %+v", fs)
	}
	teams := map[int]int{}
	bath := map[int]bool{}
	rings := map[int]int{}
	for _, p := range pls {
		teams[p.Team]++
		side := 1
		if p.Pos[0]+p.Pos[1] > 0 {
			side = 2
		}
		if strings.EqualFold(p.Noun, "editor_grid_50meter_radius.Noun") {
			rings[side]++
		}
		for tm, g := range []Point{{-166.3, -166.2}, {166.3, 166.2}} {
			if strings.EqualFold(p.Noun, "darius_titan_bath.Noun") && dist(Point{p.Pos[0], p.Pos[1]}, g) < 1 {
				bath[tm+1] = true
			}
		}
	}
	// every marker once (team 0), as on the level: the two water planes (the centre's ford, the diagonal's) too; the
	// ford (z -0.22, no mask) is the one ford, the floor dipping 0.55 m under it as on MM_Map_v3
	if teams[0] != 640 || len(teams) != 1 || !bath[1] || !bath[2] || rings[1] != 4 || rings[2] != 4 {
		t.Errorf("placed %v, the Guardian's platform %v, range rings %v", teams, bath, rings)
	}
	sameDressing(t, cm, assetData, pls)
	if err := ResolveProps(cm.Map, assetData); err != nil {
		t.Fatal(err)
	}
	if f := cm.Fords(); len(f) != 1 || f[0].X != -0.881 || f[0].Y != 0.881 || f[0].Depth != -0.55 ||
		f[0].HalfW != 15 || f[0].HalfL != 30 {
		t.Errorf("fords %+v", f)
	}
}

// TestGameImportSM: SM_Map_v5 with its own archive (its Player graph: the walkable ground) gives the fixture CM_Stand byte
// for byte, which passes with one warning (reach.dropped: the pockets the level reaches through necks narrower than
// the graphs' squares; no terrain.edge, the border is wall); its see-through ground off the level's graph is chasm,
// never on the level's walkable cells or blocked ones, and brings the compiled walkable ground closer to the level's;
// without the archive the same import has no chasm (the line-of-sight map can't tell one).
func TestGameImportSM(t *testing.T) {
	in := importInputs(t)
	in.Level = testgame.Read(t, filepath.Base(LevelSource("SM_Map_v5")))
	o := ImportOptions{Name: "CM_Stand", Display: "Stand (edit)", Tag: "stand", Prefix: "[Stand] ", Dress: true}
	m, rep, err := Import("SM_Map_v5", in, o)
	if err != nil {
		t.Fatal(err)
	}
	text, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(mapsDir, "CM_Stand", FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(text, bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) {
		t.Errorf("the import differs from the fixture CM_Stand/map.json:\n%s", text)
	}
	r, cm := Check(m, nil)
	if !r.OK || len(r.Findings) != 1 || r.Findings[0].Rule != "reach.dropped" {
		t.Errorf("%+v", r.Findings)
	}
	if len(m.Camps) != 12 || len(m.Wells) != 2 || len(m.Base.Cores.Points) != 5 || rep.SizeNote == "" {
		t.Errorf("camps %d, wells %d, %+v", len(m.Camps), len(m.Wells), rep)
	}
	c := rep.Chasm
	if c == nil || c.Walkable != 98554 || c.Parts < 10 || c.Cells < 1000 || c.Kept == 0 || c.Share < 0.78 {
		t.Fatalf("chasm %+v", c)
	}
	walk, err := levelWalk("SM_Map_v5", in.Level, m.Size)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := los.FromArchive(in.Images, LoSEntry("SM_Map_v5"))
	if err != nil {
		t.Fatal(err)
	}
	img, _ := los.Read(raw)
	chasm, onWalk, onWall := 0, 0, 0
	for k, v := range cm.Layers.Kind {
		i, j := k%Grid, k/Grid
		if v != Chasm || i+j > Grid {
			continue
		}
		chasm++
		if walk[k] {
			onWalk++
		}
		if img.Pix[k] != 0 {
			onWall++
		}
	}
	// the traced outlines (1.5 cells) cut the small parts and may cross a few blocked cells
	if chasm < c.Cells*4/5 || onWalk != 0 || onWall > chasm/20 {
		t.Errorf("chasm cells in team 1's half %d (traced from %d): %d on the level's walkable ground, %d blocked",
			chasm, c.Cells, onWalk, onWall)
	}
	if n := diagonalGaps(img, cm.Layers); n != 0 || m.Graph != nil { // MAPED-IMPORT-FIT: SM's graphs aren't a grid
		t.Errorf("%d cells on the diagonal are wall between open ground; graph %+v", n, m.Graph)
	}
	checkDressSM(t, rep, cm, in.AssetData)
	in.Level = nil
	m0, rep0, err := Import("SM_Map_v5", in, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range m0.Terrain.Shapes {
		if s.Type == "chasm" {
			t.Fatal("a chasm without the level's graph")
		}
	}
	cm0, err := Compile(m0)
	if err != nil {
		t.Fatal(err)
	}
	both0, pk0 := 0, 0
	for k, w := range walk {
		if cm0.Mask[k] {
			pk0++
			if w {
				both0++
			}
		}
	}
	share0 := float64(both0) / float64(c.Walkable+pk0-both0)
	if rep0.Chasm != nil || share0 >= c.Share {
		t.Errorf("without chasms the walkable ground's share is %.4f, with them %.4f", share0, c.Share)
	}
	t.Logf("walkable ground's share: %.4f without chasms, %.4f with; chasm %+v", share0, c.Share, *c)
}

// checkDressSM (MAPED-IMPORT-SM-DRESS, MAPED-IMPORT-ONCE): SM_Map_v5's fourteen sets with dressing come as groups
// that stand once over the whole level (the environment alone isn't taken; the towers set gives its twelve Binding
// platforms, its Bindings, Locus and respawn markers left out): the level's own 7,021 dressing markers (sameDressing),
// among them the 99 of the waterfall scene behind team 1's base (team 2 has none on the level) and 575 past the
// level's edges; five fords.
func checkDressSM(t *testing.T, rep *ImportReport, cm *Compiled, assetData []byte) {
	t.Helper()
	d := rep.Dress
	if d == nil {
		t.Fatal("no dressing report")
	}
	var taken, skipped []string
	for _, s := range d.Taken {
		taken = append(taken, s.Set)
	}
	for _, s := range d.Skipped {
		skipped = append(skipped, s.Set)
	}
	if len(taken) != 14 || taken[2] != "towers" || taken[13] != "BaseWaterfallScene" || d.Taken[13].Taken != 99 ||
		d.Taken[13].Team2 != 0 || strings.Join(skipped, ",") != "environment" || d.Members != 7021 ||
		d.Taken[2].Taken != 12 || d.unmatched() != 2252 {
		t.Errorf("dressing %v, skipped %v, %d members; towers %+v", taken, skipped, d.Members, d.Taken[2])
	}
	pls, fs, err := cm.PlacedProps(assetData)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Rule != "props.group" || fs[0].Path != "props[2].group" {
		t.Errorf("placing findings %+v", fs)
	}
	h := cm.Map.Size / 2
	teams, past, platforms := map[int]int{}, map[int]int{}, map[int]int{}
	for _, p := range pls {
		teams[p.Team]++
		if math.Abs(p.Pos[0]) > h || math.Abs(p.Pos[1]) > h {
			past[p.Team]++
		}
		if strings.EqualFold(p.Noun, "darius_tower_binding.Noun") {
			platforms[p.Team]++
		}
	}
	if teams[0] != 7021 || len(teams) != 1 || past[0] != 575 || platforms[0] != 12 {
		t.Errorf("placed %v, past the edges %v, Binding platforms %v", teams, past, platforms)
	}
	sameDressing(t, cm, assetData, pls)
	if err := ResolveProps(cm.Map, assetData); err != nil {
		t.Fatal(err)
	}
	// the level's own fords: the river's south of each well, each spawn dais's pool and the centre's pool (team 2's
	// side of the centre, so the mirrored form had none)
	centre := 0
	for _, f := range cm.Fords() {
		if math.Hypot(f.X, f.Y) < 1 {
			centre++
		}
	}
	if f := cm.Fords(); len(f) != 5 || centre != 1 {
		t.Errorf("fords %+v", f)
	}
}

// sameDressing: the groups that stand once place their level's dressing marker for marker, as the level has it: per
// group (in the props' order) each dressing marker of its sets in the set's order with the same noun, its place
// within 0.01 m, its turn within 0.01 degrees and its scale within 0.001; every dressing marker of the sets is placed.
func sameDressing(t *testing.T, cm *Compiled, assetData []byte, pls []Placed) {
	t.Helper()
	src, err := newAssetSource(assetData)
	if err != nil {
		t.Fatal(err)
	}
	ps := newPropSource(src)
	var want []Placed
	for k, p := range cm.Map.Props {
		if p.Group == nil || !p.Once() {
			t.Fatalf("props[%d] isn't a group that stands once", k)
		}
		for _, set := range p.Group.Sets {
			o, err := src.object(p.Group.Level + "_" + set + ".Markerset")
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range markers(o) {
				info := ps.noun(text(m, "nounDef"))
				if info == nil || !isPropType(info.typ) {
					continue
				}
				r := vec(m, "rotDegrees")
				if len(r) != 3 {
					r = []float64{0, 0, 0}
				}
				v := vec(m, "pos")
				want = append(want, Placed{Noun: info.name, Pos: [3]float64{v[0], v[1], v[2]},
					Rot: [3]float64{r[0], r[1], r[2]}, Scale: fval(mustGet(m, "scale")), Path: fmt.Sprintf("props[%d]", k)})
			}
		}
	}
	if len(pls) != len(want) {
		t.Fatalf("%d markers placed, the level has %d", len(pls), len(want))
	}
	bad := 0
	for i, w := range want {
		p := pls[i]
		near := p.Noun == w.Noun && p.Path == w.Path && p.Team == 0 && math.Abs(p.Scale-w.Scale) <= 0.001 &&
			math.Abs(normDeg(p.Rot[2]-w.Rot[2])) <= 0.01 && p.Rot[0] == w.Rot[0] && p.Rot[1] == w.Rot[1]
		for a := range 3 {
			near = near && math.Abs(p.Pos[a]-w.Pos[a]) <= 0.01
		}
		if !near {
			if bad++; bad <= 5 {
				t.Errorf("marker %d: placed %s at %v rot %v scale %g (%s), the level's %s at %v rot %v scale %g", i,
					p.Noun, p.Pos, p.Rot, p.Scale, p.Path, w.Noun, w.Pos, w.Rot, w.Scale)
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d markers differ from the level's", bad, len(want))
	}
}
