package mapc

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// num32 is a member's value as a float64 by its float32's shortest text.
func num32(o *assetdata.Object, k string) float64 { v, _ := o.Get(k); return shortFloat(v) }

// list32 is an array member's numbers.
func list32(o *assetdata.Object, k string) []float64 {
	v, _ := o.Get(k)
	var out []float64
	for _, e := range v.([]any) {
		out = append(out, shortFloat(e))
	}
	return out
}

// rulesAssets compiles CM_Ring with a rules section from the made-up AssetData.
func rulesAssets(t *testing.T, rules string) (*Map, *MapAssets) {
	t.Helper()
	m := ring(t)
	m.Rules = rulesOf(rules)
	r, cm := Check(m, nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	as, err := cm.Assets(madeUpAssetData(t), madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	checkAssets(t, m, as) // every asset read back exactly (ParseExact) and written again to the same bytes
	return m, as
}

// TestRulesAssets: a section with every member compiles the three tuning assets, named after the map, listed last,
// named by the Level and the catalog source; each holds the rules' values and keeps the rest (the capture durations,
// the towers' augmentation and lane-agent adjustment, the level-difference multipliers, the ability points).
func TestRulesAssets(t *testing.T) {
	m, as := rulesAssets(t, everyMember)
	want := []string{"CM_Ring.LevelGameplayTuning", "CM_Ring.RewardsTuning", "CM_Ring.LevelUpTuning"}
	if got := as.Names[len(as.Names)-3:]; !reflect.DeepEqual(got, want) || len(as.Names) != len(AssetNames(m.Name))+3 {
		t.Fatalf("assets %v", as.Names)
	}
	lv := as.Values["CM_Ring.Level"]
	for k, v := range map[string]string{"gameplayTuning": want[0], "rewardsTuning": want[1], "levelUpTuning": want[2],
		"rolesTuning": "Made_Up.RolesTuning", "activeItems": "Made_Up.ItemSet"} {
		if got := text(lv, k); got != v {
			t.Errorf("Level %s %q, %q expected", k, got, v)
		}
	}
	src := Catalog(m)
	for i, n := range want {
		e := src.Entries[len(src.Entries)-3+i]
		if e.Name != n || e.Source != "Levels/CM_Ring/"+n || e.Base != "" || len(e.Tags) != 0 {
			t.Errorf("catalog entry %+v", e)
		}
	}
	g := as.Values[want[0]]
	for k, v := range map[string]float64{"deathTimeBase": 8, "deathTimePerLevel": 0.5, "deathTimePerMinute": 0.5,
		"minionSpawnDelay1": 30, "minionSpawnInterval1": 25, "goldMineLockedDurationFirstInSeconds": 300,
		"goldMineLockedDurationInSeconds": 120, "goldMineCaptureAwardGoldPerPlayer": 75, "goldMinedPerMinute": 0,
		"goldMinedPerMinutePerFollower": 2.25, "goldMineBountyPercentage": 0.5, "surrenderEnableOn_MatchTimeInMinutes": 10,
		"surrenderEnableOn_Economy": 20000, "surrenderEnableOn_Kills": 19,
		"surrenderEnableOn_DisconnectionTimeInSeconds": 300, "surrenderVoteSuccessThreshold": 0.666,
		"nubRespawnTime": 480} {
		if got := num32(g, k); got != v {
			t.Errorf("LevelGameplayTuning %s %v, %v expected", k, got, v)
		}
	}
	if d := list32(g, "goldMineCaptureDurationInSeconds"); !reflect.DeepEqual(d, []float64{16, 14, 12}) {
		t.Errorf("the capture durations changed: %v", d)
	}
	if on, _ := g.Get("towerRespawnEnabled"); on != true {
		t.Errorf("towerRespawnEnabled %v", on)
	}
	towers := mustGet(g, "tower").([]any)
	for i, adjust := range []int64{-1, 0} {
		tw := towers[i].(*assetdata.Object)
		aug := mustGet(tw, "augmentation").([]any)
		if num32(tw, "respawnTime") != 300 || mustGet(tw, "laneAgentRespawnCounterAdjustment") != adjust ||
			len(aug) != 1 || text(aug[0].(*assetdata.Object), "name") != "made_up_boost" {
			t.Errorf("tower[%d] %s", i, assetdata.AppendJSON(nil, tw))
		}
	}
	rw := as.Values[want[1]]
	for k, v := range map[string]float64{"startingGold": 1500, "passiveGold": 200, "passiveStartDelayInSeconds": 30,
		"firstBloodGoldReward": 150} {
		if got := num32(rw, k); got != v {
			t.Errorf("RewardsTuning %s %v, %v expected", k, got, v)
		}
	}
	for k, v := range map[string][]float64{"playerKillStreakGold": {450, 525}, "playerDeathStreakGold": {450, 405},
		"playerXPLevelGold": {0}, "playerLowerLevelGoldMultiplier": {1, 0.9}} {
		if got := list32(rw, k); !reflect.DeepEqual(got, v) {
			t.Errorf("RewardsTuning %s %v, %v expected", k, got, v)
		}
	}
	levels := mustGet(as.Values[want[2]], "Level").([]any)
	for i, xp := range []float32{0, 0, 173, 407} { // 260 and 610 / 1.5, rounded
		l := levels[i].(*assetdata.Object)
		if got := mustGet(l, "xpRequired").(float32); got != xp || mustGet(l, "abilityPointsGranted") != uint64(min(i, 1)) {
			t.Errorf("Level[%d] %s, xpRequired %v expected", i, assetdata.AppendJSON(nil, l), xp)
		}
	}
	if num32(as.Values[want[2]], "maxAbilityRank") != 5 {
		t.Errorf("maxAbilityRank changed")
	}

	// a group alone: only its asset; the Level names the tuning level's other two
	m, as = rulesAssets(t, `{"xp": {"speed": 2}}`)
	if got := as.Names[len(as.Names)-1]; got != "CM_Ring.LevelUpTuning" || len(as.Names) != len(AssetNames(m.Name))+1 {
		t.Errorf("assets %v", as.Names)
	}
	lv = as.Values["CM_Ring.Level"]
	if text(lv, "levelUpTuning") != "CM_Ring.LevelUpTuning" || text(lv, "gameplayTuning") != "Made_Up.LevelGameplayTuning" ||
		text(lv, "rewardsTuning") != "Made_Up.RewardsTuning" {
		t.Errorf("Level tunings %s, %s, %s", text(lv, "gameplayTuning"), text(lv, "rewardsTuning"), text(lv, "levelUpTuning"))
	}
	if n := len(Catalog(m).Entries); n != len(AssetNames(m.Name))+1 {
		t.Errorf("%d catalog entries", n)
	}

	// no surrender: the time, economy and kill unlocks out of reach, the disconnect one kept
	_, as = rulesAssets(t, `{"surrender": {"after": 10, "allowed": false}}`)
	g = as.Values["CM_Ring.LevelGameplayTuning"]
	for k, v := range map[string]float64{"surrenderEnableOn_MatchTimeInMinutes": SurrenderNeverMinutes,
		"surrenderEnableOn_Economy": SurrenderNeverEconomy, "surrenderEnableOn_Kills": SurrenderNeverKills,
		"surrenderEnableOn_DisconnectionTimeInSeconds": 300, "deathTimeBase": 15} {
		if got := num32(g, k); got != v {
			t.Errorf("no surrender: %s %v, %v expected", k, got, v)
		}
	}
	if on, _ := g.Get("towerRespawnEnabled"); on != false {
		t.Errorf("towerRespawnEnabled without bindings: %v", on)
	}
	// a fraction of a minute: rounded, with a warning
	m = ring(t)
	m.Rules = rulesOf(`{"surrender": {"after": 7.5}}`)
	r, cm := Check(m, nil)
	if fs := rulesFindings(r); !r.OK || len(fs) != 1 || fs[0].Severity != Warning || fs[0].Path != "rules.surrender.after" ||
		!strings.Contains(fs[0].Message, "minute 7.5 becomes 8") {
		t.Errorf("%+v", r.Findings)
	}
	as, err := cm.Assets(madeUpAssetData(t), madeUpBounds)
	if err != nil || num32(as.Values["CM_Ring.LevelGameplayTuning"], "surrenderEnableOn_MatchTimeInMinutes") != 8 {
		t.Errorf("after 7.5: %v", err)
	}
}

// TestRulesNoBalanceSame: a section with only victory and startLevel (or an empty one, or empty members) compiles no
// tuning asset: the map's assets, its catalog source and its level files are byte for byte those without it.
func TestRulesNoBalanceSame(t *testing.T) {
	_, plain := madeUpAssets(t)
	base := Catalog(ring(t))
	for _, s := range []string{`{"victory": {"kills": 20, "minutes": 15}, "startLevel": 6}`, `{}`,
		`{"respawn": {}, "gold": {}, "xp": {}, "surrender": {}}`} {
		m, as := rulesAssets(t, s)
		if !reflect.DeepEqual(as.Names, plain.Names) || !reflect.DeepEqual(Catalog(m), base) {
			t.Errorf("%s: assets %v", s, as.Names)
		}
		for _, n := range as.Names {
			if !bytes.Equal(as.Files[n], plain.Files[n]) {
				t.Errorf("%s: %s differs", s, n)
			}
		}
	}
}

// TestLevelTuning: the made-up tuning level's defaults as a full section, the effective rules, the constants (every
// member, a changed one with its value, an array element by index, an empty array as []).
func TestLevelTuning(t *testing.T) {
	if _, err := ReadLevelTuning(madeUpAssetData(t), "XX_Map", nil); err == nil {
		t.Errorf("an unknown level was read")
	}
	lt, err := ReadLevelTuning(madeUpAssetData(t), "SM_Map_v5", rulesOf(`{"respawn": {"base": 8}, "bindings": {"respawn": 300}, "xp": {"speed": 2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if lt.Assets["LevelGameplayTuning"] != "Made_Up.LevelGameplayTuning" {
		t.Errorf("%+v", lt.Assets)
	}
	// the made-up values are SM_Map_v5's for the balance members
	if d := lt.Defaults(); d != ShippedTuning["SM_Map_v5"] {
		t.Errorf("defaults %+v", d)
	}
	def, _ := json.Marshal(lt.Defaults().Rules())
	wantDef := `{"startLevel":1,"respawn":{"base":15,"perLevel":0.5,"perMinute":1.25},"minions":{"firstWave":60,"every":30},` +
		`"gold":{"start":500,"perMinute":100,"from":60,"kills":1},"xp":{"speed":1},"bindings":{"respawn":0},` +
		`"wells":{"opensAt":900,"relock":240,"gold":1},"surrender":{"after":20,"allowed":true}}`
	if string(def) != wantDef {
		t.Errorf("defaults\n%s\nnot\n%s", def, wantDef)
	}
	eff, _ := json.Marshal(EffectiveRules(lt.Defaults().Rules(), rulesOf(`{"victory": {"kills": 20, "bindings": 3},
		"startLevel": 6, "respawn": {"base": 8}, "gold": {"kills": 1.5}, "surrender": {"allowed": false}}`)))
	wantEff := `{"victory":{"kills":20,"bindings":3},"startLevel":6,` +
		`"respawn":{"base":8,"perLevel":0.5,"perMinute":1.25},` +
		`"minions":{"firstWave":60,"every":30},"gold":{"start":500,"perMinute":100,"from":60,"kills":1.5},"xp":{"speed":1},` +
		`"bindings":{"respawn":0},"wells":{"opensAt":900,"relock":240,"gold":1},"surrender":{"after":20,"allowed":false}}`
	if string(eff) != wantEff {
		t.Errorf("effective\n%s\nnot\n%s", eff, wantEff)
	}
	if e, _ := json.Marshal(EffectiveRules(lt.Defaults().Rules(), nil)); string(e) != wantDef {
		t.Errorf("effective without rules %s", e)
	}
	cs := lt.Constants()
	byKey := map[string]Constant{}
	for _, c := range cs {
		byKey[c.Asset+" "+c.Member] = c
	}
	for k, want := range map[string]Constant{
		"LevelGameplayTuning deathTimeBase":                 {Default: 15.0, Value: 8.0},
		"LevelGameplayTuning deathTimePerLevel":             {Default: 0.5},
		"LevelGameplayTuning tower[1].respawnTime":          {Default: 600.0, Value: 300.0},
		"LevelGameplayTuning towerRespawnEnabled":           {Default: false, Value: true},
		"LevelGameplayTuning striderMortal":                 {Default: []any{}},
		"LevelGameplayTuning surrenderVoteSuccessThreshold": {Default: 0.666},
		"LevelGameplayTuning surrenderEnableOn_Kills":       {Default: int64(19)},
		"LevelGameplayTuning tower[0].augmentation[0].name": {Default: "made_up_boost"},
		"RewardsTuning startingGold":                        {Default: 500.0},
		"LevelUpTuning Level[2].xpRequired":                 {Default: 260.0, Value: 130.0},
		"LevelUpTuning maxAbilityRank":                      {Default: uint64(5)},
	} {
		got, ok := byKey[k]
		if !ok || !reflect.DeepEqual(got.Default, want.Default) || !reflect.DeepEqual(got.Value, want.Value) {
			t.Errorf("%s: %+v, %+v expected", k, got, want)
		}
	}
	if cs[0].Asset != "LevelGameplayTuning" || cs[0].Member != "deathTimeBase" || cs[len(cs)-1].Asset != "LevelUpTuning" {
		t.Errorf("order: %+v ... %+v", cs[0], cs[len(cs)-1])
	}
	for _, c := range cs {
		if strings.Contains(c.Member, "_unk") {
			t.Errorf("%+v", c)
		}
	}
}

// TestRulesGameBuild builds CM_Ring from build 854's files: with only victory and startLevel the archive is the
// pinned one (Ring854SHA256), so its hash (the map guard's) is unchanged; with balance rules it holds the three
// tuning assets besides and its hash differs.
func TestRulesGameBuild(t *testing.T) {
	in, err := ReadInputs(testgame.Dir(t))
	if err != nil {
		t.Fatal(err)
	}
	m := ring(t)
	m.Rules = rulesOf(`{"victory": {"kills": 20, "minutes": 15}, "startLevel": 6}`)
	b, r, err := Build(m, in, SharedMesh{}, nil)
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if got := digest(b); got != Ring854SHA256 {
		t.Errorf("victory only: SHA-256 %s, pinned %s", got, Ring854SHA256)
	}
	m.Rules = rulesOf(everyMember)
	if b, r, err = Build(m, in, SharedMesh{}, nil); err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if got := digest(b); got == Ring854SHA256 {
		t.Errorf("every rule: the pinned archive")
	}
	a, err := snap.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, e := range a.Entries {
		found[e.Name] = true
	}
	for _, n := range TuningAssetNames(m) {
		if !found[AssetEntry(n)] {
			t.Errorf("the archive lacks %s", AssetEntry(n))
		}
	}
}
