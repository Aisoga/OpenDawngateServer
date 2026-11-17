package gmshost

import "fmt"

// FeatureMapRules is the host feature for a map's own rules: createGame's MapRules (MAPRULES4). A host without it
// would play such a map without its kill race, time limit or start level, so the manager strips the field for it and
// logs that (gameFor).
const FeatureMapRules = "maprules"

// FeatureMapRules2 is the host feature for the Binding race and the lobby's win condition (WINBIND-B): createGame's
// MapRules.Bindings, StartGame.Victory, FinishGame.Bindings and the reason "bindings". The manager leaves Bindings out
// of createGame for a host without it (and logs that); dgserver refuses a custom game's START that needs it.
const FeatureMapRules2 = "maprules2"

// MapRules are a map's own victory rules and start level, which dgserver reads from the map's map.json (its rules
// block's victory and startLevel) and the host runs (gms/engine/maprules.cpp): a kill race (the first team to Kills
// wins), a time limit (when Minutes are over the team with more kills wins; a tie goes to sudden death, the next kill
// wins) and every seat's start level, bots included. Bindings (WINBIND-B, only to a host with FeatureMapRules2) is a
// Binding race: the first team to destroy that many enemy Bindings wins; with a time limit the team with more Bindings
// wins at the time, then the one with more kills, then sudden death (the next Binding or kill). Each 0 = not set
// (omitted); the host clamps the values again.
type MapRules struct {
	Kills      int `json:"kills,omitempty"`
	Minutes    int `json:"minutes,omitempty"`
	StartLevel int `json:"startLevel,omitempty"`
	Bindings   int `json:"bindings,omitempty"`
}

// Empty reports whether r sets nothing (a nil r too).
func (r *MapRules) Empty() bool { return r == nil || *r == MapRules{} }

// gameFor is the createGame the match's host gets: its MapRules only for a host that names FeatureMapRules (else
// left out, with a log line), their Bindings only for one that names FeatureMapRules2 (WINBIND-B; else left out, with
// a log line), and none when they set nothing.
func (m *Manager) gameFor(mt *Match, who string) CreateGame {
	g := mt.Game
	if g.MapRules.Empty() {
		g.MapRules = nil
		return g
	}
	if !mt.Supports(FeatureMapRules) {
		bind := ""
		if g.MapRules.Bindings != 0 {
			bind = fmt.Sprintf(", bindings %d", g.MapRules.Bindings)
		}
		m.log.Logf(who, "match %d: the map's rules (kills %d, minutes %d, start level %d%s) left out of createGame: the "+
			"host's hello lacks %q", mt.ID, g.MapRules.Kills, g.MapRules.Minutes, g.MapRules.StartLevel, bind,
			FeatureMapRules)
		g.MapRules = nil
		return g
	}
	r := *g.MapRules
	if r.Bindings != 0 && !mt.Supports(FeatureMapRules2) {
		m.log.Logf(who, "match %d: the map's Binding race (bindings %d) left out of createGame: the host's hello lacks %q",
			mt.ID, r.Bindings, FeatureMapRules2)
		r.Bindings = 0
		if r.Empty() {
			g.MapRules = nil
			return g
		}
	}
	g.MapRules = &r
	return g
}

// Victory is the lobby's win condition in numbers (StartGame.Victory, WINBIND-B; only to a host with
// FeatureMapRules2): a kill race of Kills, a time limit of Minutes, a Binding race of Bindings, as MapRules' members;
// each 0 = not set (omitted). The host sets the victory from it and keeps createGame's start level.
type Victory struct {
	Kills    int `json:"kills,omitempty"`
	Minutes  int `json:"minutes,omitempty"`
	Bindings int `json:"bindings,omitempty"`
}

// Empty reports whether v sets nothing (a nil v too): Guardian only.
func (v *Victory) Empty() bool { return v == nil || *v == Victory{} }
