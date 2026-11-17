package mapc

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/assetdata"
)

// The map's own tuning assets (MAPRULES2): the rules section's balance members compiled into copies of the tuning
// level's LevelGameplayTuning, RewardsTuning and LevelUpTuning (Map.Tuning's Level names them: gameplayTuning,
// rewardsTuning, levelUpTuning). Each is written only when its group changes (GameplayChanged, RewardsChanged,
// LevelUpChanged), as <map>.LevelGameplayTuning etc.; the map's Level then names it instead of the shipped one, and the
// map's archive and catalog source list it (MapAssetNames, Catalog). A member the rules leave out keeps the tuning
// level's value, so a map without balance members builds byte for byte as before. The mapping:
//
//   - respawn: deathTimeBase, deathTimePerLevel, deathTimePerMinute;
//   - minions: minionSpawnDelay1 (firstWave), minionSpawnInterval1 (every);
//   - bindings: towerRespawnEnabled true and every tower[].respawnTime (augmentation and the lane-agent adjustment
//     kept);
//   - wells: goldMineLockedDurationFirstInSeconds (opensAt), goldMineLockedDurationInSeconds (relock), and gold as a
//     factor on goldMineCaptureAwardGoldPerPlayer, goldMinedPerMinute and goldMinedPerMinutePerFollower (the capture
//     durations stay: at most 5 are read and each must be above 0.1, gms/core/wells/wells.h; the bounty is a share of
//     the well's gold, so it follows);
//   - surrender: surrenderEnableOn_MatchTimeInMinutes (after, rounded to a whole minute: the member is an int64);
//     allowed false puts the time, economy and kill unlocks out of reach (SurrenderNever*) and keeps the disconnect one;
//   - gold: startingGold, passiveGold (a minute), passiveStartDelayInSeconds; kills as a factor on
//     playerKillStreakGold, playerDeathStreakGold (the kill's base gold), playerXPLevelGold and firstBloodGoldReward
//     (the level-difference multipliers stay: they multiply the scaled base);
//   - xp.speed: every Level[].xpRequired divided by it, rounded to whole XP (the shipped values are whole).

// TuningKind is one of the tuning assets a map may own: its class and the Level member that names it.
type TuningKind struct{ Class, LevelMember string }

// TuningKinds are the three, in the order a map's assets list them.
var TuningKinds = []TuningKind{{"LevelGameplayTuning", "gameplayTuning"}, {"RewardsTuning", "rewardsTuning"},
	{"LevelUpTuning", "levelUpTuning"}}

// The values that put a surrender unlock out of reach (surrender.allowed false): about 69 days, a billion gold, a
// hundred thousand kills (a kill race ends at MaxRaceKills).
const (
	SurrenderNeverMinutes = 100000
	SurrenderNeverEconomy = 1e9
	SurrenderNeverKills   = 100000
)

// changes tells whether the section compiles a tuning asset of the class.
func (r *Rules) changes(class string) bool {
	switch class {
	case "LevelGameplayTuning":
		return r.GameplayChanged()
	case "RewardsTuning":
		return r.RewardsChanged()
	case "LevelUpTuning":
		return r.LevelUpChanged()
	}
	return false
}

// TuningAsset is a map's own tuning asset of a class: <name>.<class>.
func TuningAsset(name, class string) string { return name + "." + class }

// TuningAssetNames are the map's own tuning assets, those its rules change (TuningKinds' order); none for a map
// without balance members.
func TuningAssetNames(m *Map) []string {
	var out []string
	for _, k := range TuningKinds {
		if m.Rules.changes(k.Class) {
			out = append(out, TuningAsset(m.Name, k.Class))
		}
	}
	return out
}

// compileTuning reads the tuning asset of a kind the tuning level (its Level lv) names and returns it and a copy with
// the rules applied.
func (s *assetSource) compileTuning(lv *assetdata.Object, k TuningKind, r *Rules) (shipped, compiled *assetdata.Object,
	err error) {
	name := text(lv, k.LevelMember)
	if name == "" {
		return nil, nil, fmt.Errorf("the tuning level names no %s", k.Class)
	}
	if !strings.EqualFold(classOfAsset(name), k.Class) {
		return nil, nil, fmt.Errorf("the tuning level's %s %s isn't a %s", k.LevelMember, name, k.Class)
	}
	shipped, err = s.object(name)
	if err != nil {
		return nil, nil, err
	}
	compiled = cloneObj(shipped)
	if err := applyRules(s.ts.Type(k.Class), compiled, r); err != nil {
		return nil, nil, fmt.Errorf("%s: %v", name, err)
	}
	return shipped, compiled, nil
}

// setNum sets a scalar member to v in its type's Go value (float32, int64, uint64, bool; ParseExact's).
func setNum(t *assetdata.Type, o *assetdata.Object, k string, v float64) error {
	typ := ""
	for _, m := range t.Members {
		if m.Name == k {
			typ = m.Type
		}
	}
	switch {
	case typ == "float":
		o.Set(k, float32(v))
	case typ == "bool":
		o.Set(k, v != 0)
	case strings.HasPrefix(typ, "uint"):
		o.Set(k, uint64(math.Round(v)))
	case strings.HasPrefix(typ, "int"):
		o.Set(k, int64(math.Round(v)))
	default:
		return fmt.Errorf("%s.%s: no number member (%q)", t.Name, k, typ)
	}
	return nil
}

// applyRules sets a tuning asset's members (a copy of the tuning level's) from the rules; every member they leave out
// keeps its value. The class is t's.
func applyRules(t *assetdata.Type, o *assetdata.Object, r *Rules) error {
	if t == nil || r == nil {
		return nil
	}
	var err error
	set := func(k string, v *float64) {
		if v != nil && err == nil {
			err = setNum(t, o, k, *v)
		}
	}
	scale := func(k string, f float64) {
		if err != nil {
			return
		}
		v, ok := o.Get(k)
		if !ok {
			return // a made-up asset without it: nothing to scale
		}
		switch x := v.(type) {
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = float32(fval(e) * f)
			}
			o.Set(k, out)
		default:
			err = setNum(t, o, k, fval(x)*f)
		}
	}
	switch t.Name {
	case "LevelGameplayTuning":
		if v := r.Respawn; v != nil {
			set("deathTimeBase", v.Base)
			set("deathTimePerLevel", v.PerLevel)
			set("deathTimePerMinute", v.PerMinute)
		}
		if v := r.Minions; v != nil {
			set("minionSpawnDelay1", v.FirstWave)
			set("minionSpawnInterval1", v.Every)
		}
		if v := r.Bindings; v != nil && v.Respawn != nil {
			o.Set("towerRespawnEnabled", true)
			list, _ := mustGet(o, "tower").([]any)
			out := make([]any, len(list))
			for i, e := range list {
				tw, ok := e.(*assetdata.Object)
				if !ok {
					return fmt.Errorf("tower[%d] isn't a struct", i)
				}
				tw = cloneObj(tw)
				tw.Set("respawnTime", float32(*v.Respawn))
				out[i] = tw
			}
			o.Set("tower", out)
		}
		if v := r.Wells; v != nil {
			set("goldMineLockedDurationFirstInSeconds", v.OpensAt)
			set("goldMineLockedDurationInSeconds", v.Relock)
			if v.Gold != nil {
				for _, k := range []string{"goldMineCaptureAwardGoldPerPlayer", "goldMinedPerMinute",
					"goldMinedPerMinutePerFollower"} {
					scale(k, *v.Gold)
				}
			}
		}
		if v := r.Surrender; v != nil {
			if v.Allowed != nil && !*v.Allowed {
				never := []struct {
					k string
					v float64
				}{{"surrenderEnableOn_MatchTimeInMinutes", SurrenderNeverMinutes},
					{"surrenderEnableOn_Economy", SurrenderNeverEconomy}, {"surrenderEnableOn_Kills", SurrenderNeverKills}}
				for _, n := range never {
					set(n.k, &n.v)
				}
			} else if v.After != nil {
				after := math.Round(*v.After)
				set("surrenderEnableOn_MatchTimeInMinutes", &after)
			}
		}
	case "RewardsTuning":
		if v := r.Gold; v != nil {
			set("startingGold", v.Start)
			set("passiveGold", v.PerMinute)
			set("passiveStartDelayInSeconds", v.From)
			if v.Kills != nil {
				for _, k := range []string{"playerKillStreakGold", "playerDeathStreakGold", "playerXPLevelGold",
					"firstBloodGoldReward"} {
					scale(k, *v.Kills)
				}
			}
		}
	case "LevelUpTuning":
		if v := r.XP; v != nil && v.Speed != nil && *v.Speed > 0 {
			list, _ := mustGet(o, "Level").([]any)
			out := make([]any, len(list))
			for i, e := range list {
				lv, ok := e.(*assetdata.Object)
				if !ok {
					return fmt.Errorf("Level[%d] isn't a struct", i)
				}
				lv = cloneObj(lv)
				x, _ := lv.Get("xpRequired")
				lv.Set("xpRequired", float32(math.Round(fval(x)/(*v.Speed))))
				out[i] = lv
			}
			o.Set("Level", out)
		}
	}
	return err
}

// ---------------------------------------------------------------- the defaults and `mapc tuning`

// tuningDefaultsOf are a tuning level's values for the balance members, from its LevelGameplayTuning and
// RewardsTuning (either may be nil: its part stays 0).
func tuningDefaultsOf(g, rw *assetdata.Object) TuningDefaults {
	f := func(o *assetdata.Object, k string) float64 {
		if o == nil {
			return 0
		}
		v, _ := o.Get(k)
		return shortFloat(v)
	}
	d := TuningDefaults{
		RespawnBase: f(g, "deathTimeBase"), RespawnPerLevel: f(g, "deathTimePerLevel"),
		RespawnPerMinute: f(g, "deathTimePerMinute"), FirstWave: f(g, "minionSpawnDelay1"),
		WaveEvery: f(g, "minionSpawnInterval1"), WellsOpenAt: f(g, "goldMineLockedDurationFirstInSeconds"),
		WellsRelock: f(g, "goldMineLockedDurationInSeconds"), SurrenderAfter: f(g, "surrenderEnableOn_MatchTimeInMinutes"),
		StartGold: f(rw, "startingGold"), GoldPerMinute: f(rw, "passiveGold"), GoldFrom: f(rw, "passiveStartDelayInSeconds"),
	}
	if g != nil {
		if on, _ := g.Get("towerRespawnEnabled"); on == true {
			if list, _ := g.Get("tower"); list != nil {
				if ts, _ := list.([]any); len(ts) > 0 {
					if tw, ok := ts[0].(*assetdata.Object); ok {
						d.BindingRespawn = f(tw, "respawnTime")
					}
				}
			}
		}
	}
	return d
}

// shortFloat is a parsed number as a float64, a float32 by its shortest text (0.666, not 0.6660000085830688).
func shortFloat(v any) float64 {
	if f, ok := v.(float32); ok {
		x, _ := strconv.ParseFloat(strconv.FormatFloat(float64(f), 'g', -1, 32), 64)
		return x
	}
	return fval(v)
}

// Rules are the defaults as a full rules section, the shape `mapc tuning` prints: every balance member set (the
// factors 1, the surrender allowed, bindings.respawn 0 when the level has no Binding respawn), startLevel 1, no
// victory.
func (d TuningDefaults) Rules() *Rules {
	p := func(v float64) *float64 { return &v }
	one, lv, yes := 1.0, 1, true
	return &Rules{
		StartLevel: &lv,
		Respawn:    &RespawnRules{Base: p(d.RespawnBase), PerLevel: p(d.RespawnPerLevel), PerMinute: p(d.RespawnPerMinute)},
		Minions:    &MinionRules{FirstWave: p(d.FirstWave), Every: p(d.WaveEvery)},
		Gold:       &GoldRules{Start: p(d.StartGold), PerMinute: p(d.GoldPerMinute), From: p(d.GoldFrom), Kills: p(one)},
		XP:         &XPRules{Speed: p(one)},
		Bindings:   &BindingRules{Respawn: p(d.BindingRespawn)},
		Wells:      &WellRules{OpensAt: p(d.WellsOpenAt), Relock: p(d.WellsRelock), Gold: p(one)},
		Surrender:  &SurrenderRules{After: p(d.SurrenderAfter), Allowed: &yes},
	}
}

// EffectiveRules are the defaults (a full section, TuningDefaults.Rules) with the map's members put in: what the map
// plays. The map's victory and start level are taken over too.
func EffectiveRules(def, r *Rules) *Rules {
	out := &Rules{}
	b, _ := json.Marshal(def)
	_ = json.Unmarshal(b, out)
	if r == nil {
		return out
	}
	put := func(dst **float64, src *float64) {
		if src != nil {
			v := *src
			*dst = &v
		}
	}
	if r.VictoryChanged() {
		v := *r.Victory
		out.Victory = &v
	}
	if r.StartLevel != nil {
		v := *r.StartLevel
		out.StartLevel = &v
	}
	if v := r.Respawn; v != nil && out.Respawn != nil {
		put(&out.Respawn.Base, v.Base)
		put(&out.Respawn.PerLevel, v.PerLevel)
		put(&out.Respawn.PerMinute, v.PerMinute)
	}
	if v := r.Minions; v != nil && out.Minions != nil {
		put(&out.Minions.FirstWave, v.FirstWave)
		put(&out.Minions.Every, v.Every)
	}
	if v := r.Gold; v != nil && out.Gold != nil {
		put(&out.Gold.Start, v.Start)
		put(&out.Gold.PerMinute, v.PerMinute)
		put(&out.Gold.From, v.From)
		put(&out.Gold.Kills, v.Kills)
	}
	if v := r.XP; v != nil && out.XP != nil {
		put(&out.XP.Speed, v.Speed)
	}
	if v := r.Bindings; v != nil && out.Bindings != nil {
		put(&out.Bindings.Respawn, v.Respawn)
	}
	if v := r.Wells; v != nil && out.Wells != nil {
		put(&out.Wells.OpensAt, v.OpensAt)
		put(&out.Wells.Relock, v.Relock)
		put(&out.Wells.Gold, v.Gold)
	}
	if v := r.Surrender; v != nil && out.Surrender != nil {
		put(&out.Surrender.After, v.After)
		if v.Allowed != nil {
			a := *v.Allowed
			out.Surrender.Allowed = &a
		}
	}
	return out
}

// LevelTuning is a tuning level's three tuning assets as shipped and with a rules section compiled into them (the
// same value where the section changes nothing), by class.
type LevelTuning struct {
	Level    string                       // SM_Map_v5 or MM_Map_v3
	Assets   map[string]string            // the shipped assets' names ("Tuning.LevelGameplayTuning")
	Shipped  map[string]*assetdata.Object // as the copy has them
	Compiled map[string]*assetdata.Object // with the rules
}

// ReadLevelTuning reads a tuning level's tuning assets from the copy's AssetData.snap and compiles the rules (nil:
// none) into them.
func ReadLevelTuning(assetData []byte, level string, r *Rules) (*LevelTuning, error) {
	if !inList(level, SharedLevels, false) {
		return nil, fmt.Errorf("tuning level %q: one of %s", level, strings.Join(SharedLevels, ", "))
	}
	src, err := newAssetSource(assetData)
	if err != nil {
		return nil, err
	}
	lv, err := src.object(level + ".Level")
	if err != nil {
		return nil, err
	}
	lt := &LevelTuning{Level: level, Assets: map[string]string{}, Shipped: map[string]*assetdata.Object{},
		Compiled: map[string]*assetdata.Object{}}
	for _, k := range TuningKinds {
		s, c, err := src.compileTuning(lv, k, r)
		if err != nil {
			return nil, err
		}
		lt.Assets[k.Class], lt.Shipped[k.Class], lt.Compiled[k.Class] = text(lv, k.LevelMember), s, c
	}
	return lt, nil
}

// Defaults are the level's shipped values as a full rules section (TuningDefaults.Rules).
func (lt *LevelTuning) Defaults() TuningDefaults {
	return tuningDefaultsOf(lt.Shipped["LevelGameplayTuning"], lt.Shipped["RewardsTuning"])
}

// Constant is one scalar member of a tuning asset (an array element by its index: "tower[0].respawnTime", an empty
// array as []): the tuning level's value and, when the rules change it, the compiled one.
type Constant struct {
	Asset   string `json:"asset"` // the class: LevelGameplayTuning, RewardsTuning, LevelUpTuning
	Member  string `json:"member"`
	Default any    `json:"default"`
	Value   any    `json:"value,omitempty"`
}

// Constants are every member of the three assets, in the assets' order (TuningKinds) and each one's member order.
func (lt *LevelTuning) Constants() []Constant {
	var out []Constant
	for _, k := range TuningKinds {
		def, got := flattenTuning(lt.Shipped[k.Class]), flattenTuning(lt.Compiled[k.Class])
		now := map[string]any{}
		for _, c := range got {
			now[c.Member] = c.Default
		}
		for _, c := range def {
			c.Asset = k.Class
			if v, ok := now[c.Member]; ok && !reflect.DeepEqual(v, c.Default) {
				c.Value = v
			}
			out = append(out, c)
		}
	}
	return out
}

// flattenTuning lists an asset's scalar members (Constant's Member and Default), the unknown bytes (_unk) left out.
func flattenTuning(o *assetdata.Object) []Constant {
	var out []Constant
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case *assetdata.Object:
			for _, k := range x.Keys() {
				if k == "_unk" {
					continue
				}
				e, _ := x.Get(k)
				p := k
				if path != "" {
					p = path + "." + k
				}
				walk(p, e)
			}
		case []any:
			if len(x) == 0 {
				out = append(out, Constant{Member: path, Default: []any{}})
			}
			for i, e := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), e)
			}
		case float32:
			out = append(out, Constant{Member: path, Default: shortFloat(x)})
		default:
			out = append(out, Constant{Member: path, Default: x})
		}
	}
	if o != nil {
		walk("", o)
	}
	return out
}
