package mapc

import (
	"strings"
	"testing"

	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

// groveDir is the second test map: CM_Ring's lanes with a jungle (camps, the parasite, four wells, bushes).
var groveDir = testmaps.Pkg("CM_Grove")

func grove(t *testing.T) *Map {
	t.Helper()
	m, err := Load(groveDir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestGrove: CM_Grove passes every rule without a warning; four wells, four camps a side and the one parasite.
func TestGrove(t *testing.T) {
	m := grove(t)
	r, cm := Check(m, []Other{{"CM_Ring", "ring"}})
	if !r.OK || len(r.Findings) != 0 {
		t.Fatalf("%+v", r.Findings)
	}
	if r.Stats.Wells != 4 || r.Stats.Camps != 9 || r.Stats.Bushes < 8 {
		t.Errorf("stats %+v", r.Stats)
	}
	if n1, n2 := len(cm.Layout.Teams[0].Camps), len(cm.Layout.Teams[1].Camps); n1 != 5 || n2 != 4 {
		t.Errorf("camps %d and %d (5 with the parasite and 4 expected)", n1, n2)
	}
}

// TestGameGrove builds CM_Grove's assets from build 854's AssetData: they read back with sound references; every camp
// is SM_Map_v5's camp of its noun with all its creature spots on open ground, the parasite once, a lane-agent spawner
// per team and lane, 18 worker spots per well; no build warning.
func TestGameGrove(t *testing.T) {
	dir := testgame.Dir(t)
	in, err := ReadInputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := grove(t)
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	as, err := cm.Assets(in.AssetData, madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	checkAssets(t, m, as)
	if len(as.Findings) != 0 {
		t.Errorf("%+v", as.Findings)
	}
	counts := as.MarkerCount()
	t.Logf("%v", counts)
	if n := counts["CM_Grove_followers.Markerset"]; n != 4*37 {
		t.Errorf("%d well markers, %d expected (a well, 18 spots and their 18 targets, four wells)", n, 4*37)
	}
	camps, spots, parasites, agents := 0, 0, 0, 0
	for _, mk := range markers(as.Values["CM_Grove_titans.Markerset"]) {
		n := text(mk, "nounDef")
		switch {
		case strings.HasPrefix(n, "SpawnPoint_v5_"):
			camps++
		case n == "SpawnPoint_Objective_Titan.Noun":
			parasites++
		case n == "SpawnPoint_CreepMarker.Noun":
			spots++
		}
	}
	for _, s := range []string{"minion_blue", "minion_red"} {
		for _, mk := range markers(as.Values["CM_Grove_"+s+".Markerset"]) {
			if strings.HasSuffix(text(mk, "nounDef"), "_Strider.Noun") {
				agents++
			}
		}
	}
	// Buff_Fungus 3, Buff_Fish 3, CreepsUgger 2, CreepsFungus 4 spots a side; the parasite's 1
	if camps != 8 || parasites != 1 || spots != 2*(3+3+2+4)+1 || agents != 4 {
		t.Errorf("camps %d, parasites %d, creature spots %d, lane-agent spawners %d", camps, parasites, spots, agents)
	}
}
