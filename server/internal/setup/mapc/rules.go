package mapc

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	lobbyrules "dawngate/server/internal/rules"
)

// The rules section (optional): the map's own match rules. Every member is optional, and an absent one keeps the
// tuning level's value (Map.Tuning: SM_Map_v5's or MM_Map_v3's); a map without the section builds as before, byte for
// byte. The balance members (Respawn, Minions, Gold, XP, Bindings, Wells, Surrender) are compiled into the map's own
// tuning assets (its LevelGameplayTuning, RewardsTuning, LevelUpTuning: GameplayChanged, RewardsChanged,
// LevelUpChanged); Victory and StartLevel are the match host's (HostRules), sent by the server when it creates the
// game. Seconds everywhere but Victory.Minutes and Surrender.After (minutes). The fields are pointers: 0 differs from
// absent. DescribeRules gives the one text players read about them.

// Rules is the map's rules section.
type Rules struct {
	Victory    *VictoryRules   `json:"victory,omitempty"`    // a kill race, a Binding race, a time limit (the host's)
	StartLevel *int            `json:"startLevel,omitempty"` // every seat, bots too, starts at this level (the host's)
	Respawn    *RespawnRules   `json:"respawn,omitempty"`    // the death timer
	Minions    *MinionRules    `json:"minions,omitempty"`    // the minion waves
	Gold       *GoldRules      `json:"gold,omitempty"`       // starting and passive gold, the kill gold's scale
	XP         *XPRules        `json:"xp,omitempty"`         // the levelling speed
	Bindings   *BindingRules   `json:"bindings,omitempty"`   // a destroyed Binding comes back
	Wells      *WellRules      `json:"wells,omitempty"`      // the gold wells' lock times and gold
	Surrender  *SurrenderRules `json:"surrender,omitempty"`  // when a team may surrender
	// LobbyPreset is the lobby setup the map suggests for a custom game on it (RULES6): a preset's id ("draft", or one
	// a server's operator defines) or the rules' short form ("pick=random,shop=onDeath,victory=bind1"). Not compiled:
	// the server reads it from map.json and starts such a game's lobby with it; the lobby's admin may change it.
	LobbyPreset string `json:"lobbyPreset,omitempty"`
}

// VictoryRules end a match without a Guardian's death; they add up, whichever is met first ends the match (the
// Guardian still wins at any time): the first team to Kills player kills wins (a kill race); the first team to destroy
// Bindings enemy Bindings wins (a Binding race: destructions are counted, so a Binding that came back counts again;
// not Rules.Bindings, which brings them back); after Minutes minutes the team with more kills wins, with a Binding race
// the team that destroyed more Bindings, then the one with more kills; a tie goes to sudden death (the next kill, with
// a Binding race the next Binding or kill, wins).
type VictoryRules struct {
	Kills    *int `json:"kills,omitempty"`
	Minutes  *int `json:"minutes,omitempty"`
	Bindings *int `json:"bindings,omitempty"`
}

// RespawnRules are the death timer: Base + PerLevel x the level + PerMinute x the match's minutes, in seconds
// (LevelGameplayTuning's deathTimeBase, deathTimePerLevel, deathTimePerMinute).
type RespawnRules struct {
	Base      *float64 `json:"base,omitempty"`
	PerLevel  *float64 `json:"perLevel,omitempty"`
	PerMinute *float64 `json:"perMinute,omitempty"`
}

// MinionRules are the waves: the first at FirstWave seconds, then one every Every seconds (minionSpawnDelay1,
// minionSpawnInterval1).
type MinionRules struct {
	FirstWave *float64 `json:"firstWave,omitempty"`
	Every     *float64 `json:"every,omitempty"`
}

// GoldRules are RewardsTuning's gold: Start (startingGold), PerMinute (the passive gold a minute) from From seconds on
// (its start delay), and Kills, a factor on the kill, streak, level-difference and first-blood gold.
type GoldRules struct {
	Start     *float64 `json:"start,omitempty"`
	PerMinute *float64 `json:"perMinute,omitempty"`
	From      *float64 `json:"from,omitempty"`
	Kills     *float64 `json:"kills,omitempty"`
}

// XPRules: Speed divides every level's required XP (LevelUpTuning), so 2 levels twice as fast.
type XPRules struct {
	Speed *float64 `json:"speed,omitempty"`
}

// BindingRules: a destroyed Binding comes back Respawn seconds after it fell (towerRespawnEnabled and every
// tower[].respawnTime).
type BindingRules struct {
	Respawn *float64 `json:"respawn,omitempty"`
}

// WellRules are the gold wells: locked until OpensAt seconds into the match, locked for Relock seconds after a capture
// (goldMineLockedDurationFirstInSeconds, goldMineLockedDurationInSeconds), Gold a factor on the gold they pay.
type WellRules struct {
	OpensAt *float64 `json:"opensAt,omitempty"`
	Relock  *float64 `json:"relock,omitempty"`
	Gold    *float64 `json:"gold,omitempty"`
}

// SurrenderRules: a vote may be called from After minutes on (surrenderEnableOn_MatchTimeInMinutes); Allowed false
// puts the time, economy and kill unlocks out of reach and keeps only the disconnect one (a player gone for 5 min).
type SurrenderRules struct {
	After   *float64 `json:"after,omitempty"`
	Allowed *bool    `json:"allowed,omitempty"`
}

// The rules' limits (rules.*).
const (
	MinRaceKills, MaxRaceKills   = 1, 200
	MinRaceBindings              = 1
	MaxRaceBindings              = 12 // SM_Map_v5's 6 a team twice over (with Bindings coming back)
	MinLimitMinutes, MaxMinutes  = 3, 90
	MinStartLevel, MaxStartLevel = 1, 20
	MaxRespawnBase               = 120.0
	MaxRespawnGrowth             = 10.0 // perLevel, perMinute
	MaxFirstWave                 = 600.0
	MinWaveEvery, MaxWaveEvery   = 10.0, 300.0
	CrowdedWaveEvery             = 15.0 // waves closer than this crowd the lanes: a warning
	MaxStartGold                 = 20000.0
	MaxGoldPerMinute             = 5000.0
	MaxGoldFactor                = 10.0 // gold.kills, wells.gold
	MaxGoldFrom                  = 3600.0
	MinXPSpeed, MaxXPSpeed       = 0.25, 5.0
	MinBindingRespawn            = 30.0
	MaxBindingRespawn            = 3600.0
	MaxWellsOpenAt               = 3600.0
	MinWellsRelock               = 30.0
	MaxWellsRelock               = 3600.0
	MinSurrenderAfter            = 5.0
	MaxSurrenderAfter            = 60.0
	ShortRace                    = 5 // a kill race under this ends in the first fights: a warning
)

// TuningNames are the shipped tuning assets' names (<name>.LevelGameplayTuning and the others): a map that compiles
// its own tuning assets (<map>.LevelGameplayTuning) can't take one, or its assets would replace the shipped ones in the
// catalog (names.tuning).
var TuningNames = []string{"Tuning", "Tuning_MM_V3"}

// TuningDefaults are a tuning level's shipped values for the balance members: the cross-warnings compare with them
// when the section leaves a member out, and `mapc tuning` prints them without a copy (TuningDefaults.Rules). From
// the shipped LevelGameplayTuning assets Tuning (SM_Map_v5) and Tuning_MM_V3 (MM_Map_v3) and the one RewardsTuning
// both name; TestGameAssets compares them with build 854's (tuning.go's tuningDefaultsOf).
type TuningDefaults struct {
	RespawnBase, RespawnPerLevel, RespawnPerMinute float64 // deathTimeBase, deathTimePerLevel, deathTimePerMinute
	FirstWave                                      float64 // minionSpawnDelay1, seconds
	WaveEvery                                      float64 // minionSpawnInterval1, seconds
	StartGold, GoldPerMinute, GoldFrom             float64 // startingGold, passiveGold, passiveStartDelayInSeconds
	BindingRespawn                                 float64 // tower[0].respawnTime when towerRespawnEnabled, else 0
	WellsOpenAt                                    float64 // goldMineLockedDurationFirstInSeconds
	WellsRelock                                    float64 // goldMineLockedDurationInSeconds
	SurrenderAfter                                 float64 // surrenderEnableOn_MatchTimeInMinutes
}

// ShippedTuning are SM_Map_v5's and MM_Map_v3's values (MM_Map_v3's death timer is 10 + 0.5 a level + 0.5 a
// minute; neither level has a Binding respawn).
var ShippedTuning = map[string]TuningDefaults{
	"SM_Map_v5": {RespawnBase: 15, RespawnPerLevel: 0.5, RespawnPerMinute: 1.25, FirstWave: 60, WaveEvery: 30,
		StartGold: 500, GoldPerMinute: 100, GoldFrom: 60, WellsOpenAt: 900, WellsRelock: 240, SurrenderAfter: 20},
	"MM_Map_v3": {RespawnBase: 10, RespawnPerLevel: 0.5, RespawnPerMinute: 0.5, FirstWave: 60, WaveEvery: 30,
		StartGold: 500, GoldPerMinute: 100, GoldFrom: 60, WellsOpenAt: 900, WellsRelock: 240, SurrenderAfter: 20},
}

// HostRules are the rules the match host plays (createGame's mapRules): 0 is "not set" (the Guardian's victory, no
// time limit, level 1). Comparable.
type HostRules struct {
	Kills      int // a kill race to this many kills
	Minutes    int // a time limit in minutes
	StartLevel int // everyone's starting level

	Bindings int // a Binding race to this many destroyed enemy Bindings (not Rules.Bindings, their return)
}

// IsZero tells whether the host plays the usual match.
func (h HostRules) IsZero() bool { return h == HostRules{} }

// HostRules are the section's victory and start level (zero for a nil section).
func (r *Rules) HostRules() HostRules {
	var h HostRules
	if r == nil {
		return h
	}
	if v := r.Victory; v != nil {
		h.Kills = intOr(v.Kills)
		h.Minutes = intOr(v.Minutes)
		h.Bindings = intOr(v.Bindings)
	}
	h.StartLevel = intOr(r.StartLevel)
	return h
}

// VictoryChanged tells whether the map changes how a match is won (a kill race, a Binding race or a time limit): the
// server then rates no such game.
func (r *Rules) VictoryChanged() bool {
	return r != nil && r.Victory != nil &&
		(r.Victory.Kills != nil || r.Victory.Minutes != nil || r.Victory.Bindings != nil)
}

// BindingsPerTeam is how many Bindings each team has (the enemy's as many, mirrored): "auto" places 3 on every lane,
// else the places per lane (a one-path map may give one lane none: fixed.bindings). What a Binding race can reach
// when no Binding comes back.
func (m *Map) BindingsPerTeam() int {
	if m.Base.Bindings.Auto {
		return 3 * len(m.Lanes)
	}
	n := 0
	for _, ln := range m.Lanes {
		n += len(m.Base.Bindings.Lanes[strconv.Itoa(ln.ID)])
	}
	return n
}

// GameplayChanged tells whether a member compiled into the map's LevelGameplayTuning is set (respawn, minions,
// bindings, wells, surrender).
func (r *Rules) GameplayChanged() bool {
	if r == nil {
		return false
	}
	return (r.Respawn != nil && (r.Respawn.Base != nil || r.Respawn.PerLevel != nil || r.Respawn.PerMinute != nil)) ||
		(r.Minions != nil && (r.Minions.FirstWave != nil || r.Minions.Every != nil)) ||
		(r.Bindings != nil && r.Bindings.Respawn != nil) ||
		(r.Wells != nil && (r.Wells.OpensAt != nil || r.Wells.Relock != nil || r.Wells.Gold != nil)) ||
		(r.Surrender != nil && (r.Surrender.After != nil || r.Surrender.Allowed != nil))
}

// RewardsChanged tells whether a member compiled into the map's RewardsTuning is set (gold).
func (r *Rules) RewardsChanged() bool {
	return r != nil && r.Gold != nil &&
		(r.Gold.Start != nil || r.Gold.PerMinute != nil || r.Gold.From != nil || r.Gold.Kills != nil)
}

// LevelUpChanged tells whether a member compiled into the map's LevelUpTuning is set (xp).
func (r *Rules) LevelUpChanged() bool {
	return r != nil && r.XP != nil && r.XP.Speed != nil
}

// BalanceChanged tells whether any compiled member is set (the map then has its own tuning assets).
func (r *Rules) BalanceChanged() bool {
	return r.GameplayChanged() || r.RewardsChanged() || r.LevelUpChanged()
}

func intOr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// rulesRules checks the section's values (structure): each member's range, then the cross-warnings against the
// tuning level's values for the members left out (ShippedTuning).
func (c *checker) rulesRules() {
	r := c.m.Rules
	if r == nil {
		return
	}
	if r.BalanceChanged() && inList(c.m.Name, TuningNames, true) {
		c.errorf("names.tuning", "name", nil, "name %q is the game's own tuning's: a map with its own respawn, minion, "+
			"gold, XP, Binding, well or surrender rules needs another name", c.m.Name)
	}
	ints := func(rule, path string, v *int, lo, hi int, what string) bool {
		if v != nil && (*v < lo || *v > hi) {
			c.errorf(rule, path, nil, "%s %d: from %d to %d", what, *v, lo, hi)
			return false
		}
		return v != nil
	}
	nums := func(rule, path string, v *float64, lo, hi float64, what string) bool {
		if v != nil && (math.IsNaN(*v) || *v < lo || *v > hi) {
			c.errorf(rule, path, nil, "%s %s: from %s to %s", what, num(*v), num(lo), num(hi))
			return false
		}
		return v != nil
	}
	def, ok := ShippedTuning[c.m.Tuning]
	if !ok {
		def = ShippedTuning["SM_Map_v5"]
	}
	var kills, minutes int
	if v := r.Victory; v != nil {
		if ints("rules.victory", "rules.victory.kills", v.Kills, MinRaceKills, MaxRaceKills,
			"the kill race's target (kills)") {
			kills = *v.Kills
			if kills < ShortRace {
				c.warnf("rules.victory", "rules.victory.kills", nil, "a kill race to %d ends in the first fight or "+
					"two: %d or more kills gives the teams a match", kills, ShortRace)
			}
		}
		if ints("rules.victory", "rules.victory.minutes", v.Minutes, MinLimitMinutes, MaxMinutes,
			"the time limit (minutes)") {
			minutes = *v.Minutes
		}
		if ints("rules.victory", "rules.victory.bindings", v.Bindings, MinRaceBindings, MaxRaceBindings,
			"the Binding race's target (Bindings)") {
			back := def.BindingRespawn > 0 || (r.Bindings != nil && r.Bindings.Respawn != nil)
			if have := c.m.BindingsPerTeam(); !back && have > 0 && *v.Bindings > have {
				c.errorf("rules.victory", "rules.victory.bindings", nil, "a Binding race to %d can never be reached: "+
					"the enemy has %d Bindings and they don't come back (%d or fewer, or let destroyed Bindings come "+
					"back)", *v.Bindings, have, have)
			}
		}
	}
	ints("rules.start-level", "rules.startLevel", r.StartLevel, MinStartLevel, MaxStartLevel, "the starting level")
	if v := r.Respawn; v != nil {
		nums("rules.respawn", "rules.respawn.base", v.Base, 0, MaxRespawnBase, "the respawn time's base (seconds)")
		nums("rules.respawn", "rules.respawn.perLevel", v.PerLevel, 0, MaxRespawnGrowth,
			"the respawn time a level (seconds)")
		nums("rules.respawn", "rules.respawn.perMinute", v.PerMinute, 0, MaxRespawnGrowth,
			"the respawn time a match minute (seconds)")
	}
	every := def.WaveEvery
	if v := r.Minions; v != nil {
		nums("rules.minions", "rules.minions.firstWave", v.FirstWave, 0, MaxFirstWave,
			"the first minion wave's time (seconds)")
		if nums("rules.minions", "rules.minions.every", v.Every, MinWaveEvery, MaxWaveEvery,
			"the time between minion waves (seconds)") {
			every = *v.Every
			if every < CrowdedWaveEvery {
				c.warnf("rules.minions", "rules.minions.every", nil, "a wave every %s s: the waves pile up in the "+
					"lanes faster than they fight (%s s or more keeps them apart)", num(every), num(CrowdedWaveEvery))
			}
		}
	}
	if v := r.Gold; v != nil {
		nums("rules.gold", "rules.gold.start", v.Start, 0, MaxStartGold, "the starting gold")
		nums("rules.gold", "rules.gold.perMinute", v.PerMinute, 0, MaxGoldPerMinute, "the gold a minute")
		nums("rules.gold", "rules.gold.from", v.From, 0, MaxGoldFrom, "the passive gold's start (seconds)")
		nums("rules.gold", "rules.gold.kills", v.Kills, 0, MaxGoldFactor, "the kill gold's factor")
	}
	if v := r.XP; v != nil {
		nums("rules.xp", "rules.xp.speed", v.Speed, MinXPSpeed, MaxXPSpeed, "the XP speed")
	}
	if v := r.Bindings; v != nil {
		if nums("rules.bindings", "rules.bindings.respawn", v.Respawn, MinBindingRespawn, MaxBindingRespawn,
			"the Bindings' return (seconds after they fall)") && *v.Respawn < every {
			c.warnf("rules.bindings", "rules.bindings.respawn", nil, "a Binding back %s after it falls is back "+
				"before the next minion wave (every %s): a push can't follow up on it", duration(*v.Respawn),
				duration(every))
		}
	}
	opensAt := def.WellsOpenAt
	if v := r.Wells; v != nil {
		if nums("rules.wells", "rules.wells.opensAt", v.OpensAt, 0, MaxWellsOpenAt, "the wells' opening (seconds)") {
			opensAt = *v.OpensAt
		}
		nums("rules.wells", "rules.wells.relock", v.Relock, MinWellsRelock, MaxWellsRelock,
			"the wells' lock after a capture (seconds)")
		nums("rules.wells", "rules.wells.gold", v.Gold, 0, MaxGoldFactor, "the wells' gold factor")
	}
	if minutes > 0 && opensAt > float64(minutes)*60 {
		c.warnf("rules.wells", "rules.wells.opensAt", nil, "the wells open at %s, after the time limit of %d "+
			"minutes: they only open in sudden death", clock(opensAt), minutes)
	}
	after, allowed := def.SurrenderAfter, true
	if v := r.Surrender; v != nil {
		if nums("rules.surrender", "rules.surrender.after", v.After, MinSurrenderAfter, MaxSurrenderAfter,
			"the surrender's unlock (minutes)") {
			after = *v.After
		}
		if v.Allowed != nil {
			allowed = *v.Allowed
		}
		if v.After != nil && allowed && after != math.Round(after) {
			c.warnf("rules.surrender", "rules.surrender.after", nil, "the surrender unlocks on a whole minute: "+
				"minute %s becomes %s", num(after), num(math.Round(after)))
		}
	}
	if minutes > 0 && allowed && float64(minutes) <= after {
		c.warnf("rules.surrender", "rules.surrender.after", nil, "the time limit of %d minutes comes at or before "+
			"the surrender's unlock at %s minutes: the surrender never unlocks by time (only by a lead in gold or "+
			"kills, or a player gone)", minutes, num(after))
	}
	// RULES6: the suggested lobby setup, judged by the lobby's own catalog (server/internal/rules)
	errs, warns := lobbyrules.CheckSuggestion(r.LobbyPreset, r.VictoryChanged())
	for _, e := range errs {
		c.errorf("rules.lobby-preset", "rules.lobbyPreset", nil, "the suggested lobby setup: %s", e)
	}
	for _, w := range warns {
		c.warnf("rules.lobby-preset", "rules.lobbyPreset", nil, "the suggested lobby setup: %s", w)
	}
}

// DescribeRules is the rules' text for players, the one the editor, the lobby and the match's System line show: the
// summary names every member the section sets, in plain words (the tuning level's values for the rest aren't named;
// whether the game is rated is the caller's), and short is the few words for the lobby's map line ("Race 20 · 15 min
// · Lv 6", "Tuned" for the balance alone, at most ShortMax characters). Both are empty for a nil or empty section.
// Deterministic; the vectors are in server/testdata/maprules/describe.json.
func DescribeRules(r *Rules) (summary, short string) {
	if r == nil {
		return "", ""
	}
	var parts, tags []string
	if r.VictoryChanged() {
		parts = append(parts, victoryText(r.Victory))
		if v := r.Victory; v.Kills != nil {
			tags = append(tags, fmt.Sprintf("Race %d", *v.Kills))
		}
		if v := r.Victory; v.Bindings != nil {
			tags = append(tags, fmt.Sprintf("Bind %d", *v.Bindings))
		}
		if v := r.Victory; v.Minutes != nil {
			tags = append(tags, fmt.Sprintf("%d min", *v.Minutes))
		}
	}
	level := ""
	if r.StartLevel != nil {
		parts = append(parts, fmt.Sprintf("everyone starts at level %d", *r.StartLevel))
		level = fmt.Sprintf("Lv %d", *r.StartLevel)
		tags = append(tags, level)
	}
	if v := r.Respawn; v != nil {
		var grow []string
		if v.PerLevel != nil {
			grow = append(grow, num(*v.PerLevel)+" s a level")
		}
		if v.PerMinute != nil {
			grow = append(grow, num(*v.PerMinute)+" s a minute")
		}
		switch {
		case v.Base != nil:
			parts = append(parts, strings.Join(append([]string{"respawn " + num(*v.Base) + " s"}, grow...), " + "))
		case len(grow) > 0:
			parts = append(parts, "respawn time grows "+strings.Join(grow, " and "))
		}
	}
	if v := r.Minions; v != nil {
		switch {
		case v.FirstWave != nil && v.Every != nil:
			parts = append(parts, fmt.Sprintf("minions at %s, then every %s", clock(*v.FirstWave), duration(*v.Every)))
		case v.FirstWave != nil:
			parts = append(parts, "minions at "+clock(*v.FirstWave))
		case v.Every != nil:
			parts = append(parts, "a minion wave every "+duration(*v.Every))
		}
	}
	if v := r.Gold; v != nil {
		var g []string
		if v.Start != nil {
			g = append(g, num(*v.Start)+" starting gold")
		}
		switch {
		case v.PerMinute != nil && v.From != nil:
			g = append(g, fmt.Sprintf("%s gold a minute from %s", num(*v.PerMinute), clock(*v.From)))
		case v.PerMinute != nil:
			g = append(g, num(*v.PerMinute)+" gold a minute")
		case v.From != nil:
			g = append(g, "passive gold from "+clock(*v.From))
		}
		if v.Kills != nil {
			g = append(g, "kill gold x"+num(*v.Kills))
		}
		if len(g) > 0 {
			parts = append(parts, strings.Join(g, ", "))
		}
	}
	if r.LevelUpChanged() {
		parts = append(parts, "XP x"+num(*r.XP.Speed))
	}
	if v := r.Bindings; v != nil && v.Respawn != nil {
		parts = append(parts, "Bindings return "+duration(*v.Respawn)+" after they fall")
	}
	if v := r.Wells; v != nil {
		var w []string
		if v.OpensAt != nil {
			w = append(w, "wells open at "+clock(*v.OpensAt))
		}
		if v.Relock != nil {
			w = append(w, "relock "+duration(*v.Relock)+" after a capture")
		}
		if v.Gold != nil {
			w = append(w, "well gold x"+num(*v.Gold))
		}
		if len(w) > 0 {
			if v.OpensAt == nil && v.Relock != nil {
				w[0] = "wells " + w[0]
			}
			parts = append(parts, strings.Join(w, ", "))
		}
	}
	if v := r.Surrender; v != nil {
		switch {
		case v.Allowed != nil && !*v.Allowed:
			parts = append(parts, "no surrender (only when a player is gone)")
		case v.After != nil:
			parts = append(parts, "surrender from minute "+num(*v.After))
		case v.Allowed != nil:
			parts = append(parts, "surrender allowed")
		}
	}
	if len(parts) == 0 {
		return "", ""
	}
	summary = strings.Join(parts, "; ") + "."
	summary = strings.ToUpper(summary[:1]) + summary[1:]
	if r.BalanceChanged() {
		tags = append(tags, "Tuned")
	}
	// the short text's room: "Tuned" goes first, then the start level (the summary still names both); the victory's
	// tags alone take at most 27 characters ("Race 200 · Bind 12 · 90 min")
	for _, drop := range []string{"Tuned", level} {
		if len(tags) > 1 && utf8.RuneCountInString(strings.Join(tags, shortSep)) > ShortMax {
			tags = remove(tags, drop)
		}
	}
	return summary, strings.Join(tags, shortSep)
}

// victoryText is the summary's victory part, one part however many conditions it names (the match's System line
// relies on that): "first to 20 kills wins", "first to destroy 3 Bindings or most Bindings after 20 minutes wins
// (then kills)", "first to 20 kills or to destroy 3 Bindings, or most kills ..." .
func victoryText(v *VictoryRules) string {
	var race []string
	if v.Kills != nil {
		race = append(race, "to "+plural(*v.Kills, "kill"))
	}
	if v.Bindings != nil {
		n := plural(*v.Bindings, "Binding")
		if *v.Bindings == 1 {
			n = "a Binding"
		}
		race = append(race, "to destroy "+n)
	}
	first := ""
	if len(race) > 0 {
		first = "first " + strings.Join(race, " or ")
	}
	if v.Minutes == nil {
		return first + " wins"
	}
	most := "most kills after " + plural(*v.Minutes, "minute") + " wins"
	if v.Bindings != nil {
		most = "most Bindings after " + plural(*v.Minutes, "minute") + " wins (then kills)"
	}
	switch {
	case len(race) == 0:
		return most
	case len(race) > 1:
		return first + ", or " + most
	}
	return first + " or " + most
}

// remove returns tags without t (an empty t removes nothing).
func remove(tags []string, t string) []string {
	if t == "" {
		return tags
	}
	out := tags[:0:0]
	for _, x := range tags {
		if x != t {
			out = append(out, x)
		}
	}
	return out
}

// ShortMax is the most characters DescribeRules' short text takes: past it the balance's tag "Tuned" goes, then the
// start level's (the victory's tags take at most 27).
const ShortMax = 30

const shortSep = " · "

// num writes a number as players read it: no trailing zeros, thousands with a comma ("1,500", "0.5").
func num(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) >= 1000 && math.Abs(v) < 1e15 {
		s := strconv.FormatInt(int64(math.Abs(v)), 10)
		var b strings.Builder
		if v < 0 {
			b.WriteByte('-')
		}
		for i, d := range s {
			if i > 0 && (len(s)-i)%3 == 0 {
				b.WriteByte(',')
			}
			b.WriteRune(d)
		}
		return b.String()
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// plural writes "1 kill", "20 kills".
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// duration writes a span of seconds: "25 s", "4 min", "4 min 30 s".
func duration(s float64) string {
	if s < 60 {
		return num(s) + " s"
	}
	m := math.Floor(s / 60)
	if rest := s - 60*m; rest > 0 {
		return fmt.Sprintf("%s min %s s", num(m), num(rest))
	}
	return num(m) + " min"
}

// clock writes a time in the match as its clock shows it: "0:30", "15:00" ("12.5 s" for a fraction of a second).
func clock(s float64) string {
	if s != math.Trunc(s) {
		return num(s) + " s"
	}
	n := int64(s)
	return fmt.Sprintf("%d:%02d", n/60, n%60)
}
