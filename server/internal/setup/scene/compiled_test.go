package scene

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/depot"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/mapc/bake"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

// TestCompiledGame reads the player's copy (skips without one): CM_Ring's scene from its compiled Level draws the
// Guardians, the Cores and the Bindings at their objects (as preview.json keys them) and the archons with the spawn
// platform, every marker of the Level is listed once, and the terrain marker is left to the generated ground.
func TestCompiledGame(t *testing.T) {
	dir := testgame.Dir(t)
	d, err := depot.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	as, err := NewAssets(d)
	if err != nil {
		t.Fatal(err)
	}
	m, err := mapc.Load(testmaps.Pkg("CM_Ring"))
	if err != nil {
		t.Fatal(err)
	}
	r, cm := mapc.Check(m, nil)
	if cm == nil {
		t.Fatalf("CM_Ring: %+v", r)
	}
	model, _, err := GroundFiles(cm, d)
	if err != nil {
		t.Fatal(err)
	}
	info, err := bake.CheckModel(model)
	if err != nil {
		t.Fatal(err)
	}
	ad, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(mapc.AssetDataSource)))
	if err != nil {
		t.Fatal(err)
	}
	ma, err := cm.Assets(ad, info.Bounds)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := as.CompiledMarkers(cm, ma)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, n := range ma.MarkerCount() {
		total += n
	}
	if len(ms) != total {
		t.Errorf("%d markers listed, the markersets hold %d", len(ms), total)
	}
	sc, err := as.CompiledScene(cm, ma)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Level != "CM_Ring.Level" || sc.Source != "package" || len(sc.Missing) > 0 {
		t.Errorf("scene: level %q, source %q, missing %v", sc.Level, sc.Source, sc.Missing)
	}
	parts := map[string]int{}
	listed := 0
	for _, o := range sc.Objects {
		key := o.Kind + "/" + string(rune('0'+o.Team))
		for _, p := range o.Parts {
			parts[key]++
			if strings.Contains(strings.ToLower(p.Noun), "terrain") {
				t.Errorf("%s draws the terrain noun %s", o.Name, p.Noun)
			}
			if o.Kind != "spawns" && o.Kind != "marker" && (p.Offset[0]*p.Offset[0]+p.Offset[1]*p.Offset[1] > 1) {
				t.Errorf("%s %s: part %s %v off its object", o.Kind, o.Name, p.Noun, p.Offset)
			}
		}
		if o.Kind == "marker" {
			listed++
		}
	}
	// per team: 1 Guardian, 5 Cores, 6 Bindings (two lanes of three), the 2 archons with the spawn platform
	for k, want := range map[string]int{"guardian": 1, "core": 5, "binding": 6, "spawns": 2} {
		if parts[k+"/1"] != want || parts[k+"/2"] != want {
			t.Errorf("%s: team 1 %d, team 2 %d parts, want %d each", k, parts[k+"/1"], parts[k+"/2"], want)
		}
	}
	attached := 0
	for k, n := range parts {
		if !strings.HasPrefix(k, "marker/") && !strings.HasPrefix(k, "guardian/") {
			attached += n
		}
	}
	if listed+attached != total-1 { // the terrain marker is left out
		t.Errorf("%d markers alone + %d attached, want %d (all but the terrain)", listed, attached, total-1)
	}
	t.Logf("CM_Ring compiled: %d markers, %d alone, %d drawn with objects", total, listed, attached)
	// the game objects stand on the floor: the archons (markers at SM_Map_v5's z of about 5 m) and the Guardians
	for _, o := range sc.Objects {
		for _, p := range o.Parts {
			if n := strings.ToLower(p.Noun); (strings.HasPrefix(n, "itemshoppe_archon") || o.Kind == "guardian" ||
				o.Kind == "binding") && p.Offset[2] != 0 {
				t.Errorf("%s %s: %s drawn at z %g, want the floor", o.Kind, o.Name, p.Noun, p.Offset[2])
			}
		}
	}
}

// TestCompiledProps reads the player's copy (skips without one): CM_Dress's props are markers of their own (the
// props markerset), never parts of an object they stand near, and dressing keeps its marker's height.
func TestCompiledProps(t *testing.T) {
	dir := testgame.Dir(t)
	d, err := depot.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	as, err := NewAssets(d)
	if err != nil {
		t.Fatal(err)
	}
	m, err := mapc.Load(testmaps.Pkg("CM_Dress"))
	if err != nil {
		t.Fatal(err)
	}
	ad, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(mapc.AssetDataSource)))
	if err != nil {
		t.Fatal(err)
	}
	if err := mapc.ResolveProps(m, ad); err != nil {
		t.Fatal(err)
	}
	r, cm := mapc.Check(m, nil)
	if cm == nil {
		t.Fatalf("CM_Dress: %+v", r)
	}
	model, _, err := GroundFiles(cm, d)
	if err != nil {
		t.Fatal(err)
	}
	info, err := bake.CheckModel(model)
	if err != nil {
		t.Fatal(err)
	}
	ma, err := cm.Assets(ad, info.Bounds)
	if err != nil {
		t.Fatal(err)
	}
	pls, _, err := cm.PlacedProps(ad)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := as.CompiledScene(cm, ma)
	if err != nil {
		t.Fatal(err)
	}
	set := mapc.PropsMarkerset("CM_Dress")
	var props []Object
	for _, o := range sc.Objects {
		if o.Markerset == set {
			props = append(props, o)
		}
	}
	if len(props) != len(pls) {
		t.Fatalf("%d markers of %s in the scene, mapc props places %d", len(props), set, len(pls))
	}
	for i, o := range props {
		p := pls[i]
		if o.Kind != "marker" || !strings.EqualFold(o.Noun+".Noun", p.Noun) ||
			dist2(o.Pos, [2]float64{p.Pos[0], p.Pos[1]}) > 0.01 {
			t.Errorf("props marker %d: %s %s at %v, mapc props: %s at %v", i, o.Kind, o.Noun, o.Pos, p.Noun, p.Pos)
		}
		if len(o.Parts) == 1 && !p.Card && o.Parts[0].Offset[2] != float64(float32(p.Pos[2])) {
			t.Errorf("props marker %d (%s): z %g, want %g", i, o.Noun, o.Parts[0].Offset[2], p.Pos[2])
		}
	}
	t.Logf("CM_Dress: %d props markers, all of their own", len(props))
}
