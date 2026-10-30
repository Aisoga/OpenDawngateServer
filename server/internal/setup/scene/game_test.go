package scene

import (
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/setup/bmdl"
	"dawngate/server/internal/setup/depot"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

// TestGame reads the player's copy (skips without one): every shipped model decodes; SM_Map_v5's scene and a
// package's (CM_Ring's) scene and ground export.
func TestGame(t *testing.T) {
	d, err := depot.Open(testgame.Dir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	models, tris := 0, 0
	for _, p := range d.Paths("") {
		if !strings.HasSuffix(p, ".bmdl") {
			continue
		}
		raw, err := d.Read(p)
		if err != nil {
			t.Fatal(err)
		}
		m, err := bmdl.Decode(raw)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		models++
		for _, me := range m.Meshes {
			tris += len(me.Indices) / 3
		}
	}
	t.Logf("%d models decode, %d triangles", models, tris)
	if models != 1019 {
		t.Errorf("%d models decode, want build 854's 1,019", models)
	}

	as, err := NewAssets(d)
	if err != nil {
		t.Fatal(err)
	}
	sm, err := as.LevelScene("SM_Map_v5")
	if err != nil {
		t.Fatal(err)
	}
	drawn := 0
	for _, o := range sm.Objects {
		if len(o.Parts) > 0 {
			drawn++
		}
	}
	if len(sm.Objects) != 7404 || drawn < 6700 || sm.Size < 500 {
		t.Errorf("SM_Map_v5: %d objects, %d with a model, size %v", len(sm.Objects), drawn, sm.Size)
	}
	if n := as.Noun("GuardianMortal_v5.Noun"); n == nil || n.GraphicsScale != 2.1 || !strings.Contains(n.Model, "titan01") {
		t.Errorf("GuardianMortal_v5: %+v", n)
	}

	m, err := mapc.Load(testmaps.Pkg("CM_Ring"))
	if err != nil {
		t.Fatal(err)
	}
	r, cm := mapc.Check(m, nil)
	if cm == nil {
		t.Fatalf("CM_Ring: %+v", r)
	}
	sc, err := as.PackageScene(cm)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, o := range sc.Objects {
		if len(o.Parts) > 0 {
			kinds[o.Kind+"/"+string(rune('0'+o.Team))]++
		}
	}
	for _, k := range []string{"guardian", "binding", "core", "spawns", "shop", "spawner"} {
		if kinds[k+"/1"] == 0 || kinds[k+"/2"] != kinds[k+"/1"] {
			t.Errorf("%s: team 1 %d, team 2 %d objects with models", k, kinds[k+"/1"], kinds[k+"/2"])
		}
	}
	if len(sc.Missing) > 0 {
		t.Errorf("missing: %v", sc.Missing)
	}
	x := &Exporter{Dir: t.TempDir(), MaxTexture: 64, Read: d.Read}
	t0 := time.Now()
	sc.ExportModels(x)
	g, err := ExportGround(cm, d, x)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CM_Ring: %d models, ground %d triangles, in %v", len(sc.Models), g.Triangles, time.Since(t0))
	if len(sc.Models) < 12 || g.Triangles != 2*320*320 || len(g.Textures) != 5 { // the baked ground: 2 x 2 tiles, the cliff
		t.Errorf("CM_Ring: %d models, ground %+v", len(sc.Models), g)
	}
}
