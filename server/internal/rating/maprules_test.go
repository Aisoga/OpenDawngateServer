package rating

import (
	"testing"

	"dawngate/server/internal/store"
)

// MAPRULES5 (the user's rule of 2026-11-13): a game whose map's kill race or time limit was played is never rated,
// in any type; a map with only its own balance values (or a start level) rates in the game's type as before; a column
// this build can't read counts as a played victory.
func TestJudgeMapRules(t *testing.T) {
	onMap := func(kind, rules, mapRules string) store.Match {
		m := match(kind, 1, 1, 1)
		m.Rules, m.MapRules = rules, mapRules
		return m
	}
	const victory = "the map's own victory: a kill race, a Binding race or a time limit"
	for _, c := range []struct {
		name  string
		m     store.Match
		rated bool
		why   string
		mode  string
	}{
		{"a kill race (PvP)", onMap("pvp", "", `{"kills":20}`), false, victory, Standard},
		{"a time limit (custom draft)", onMap("custom", `{"pick":"draft"}`, `{"minutes":15,"tuned":true}`), false, victory, Draft},
		{"both", onMap("custom", "", `{"kills":20,"minutes":15,"startLevel":6}`), false, victory, Standard},
		{"balance only (draft)", onMap("custom", `{"pick":"draft"}`, `{"tuned":true}`), true, "1 v 1 humans", Draft},
		{"a start level only", onMap("pvp", "", `{"startLevel":6}`), true, "1 v 1 humans", Standard},
		{"unreadable", onMap("pvp", "", `{"kills":`), false, victory, Standard},
		{"the admin's Unrated first", func() store.Match { m := onMap("custom", "", `{"kills":3}`); m.Unrated = true; return m }(),
			false, "unrated by the admin", Standard},
		{"a practice game says practice", onMap("practice", "", `{"kills":3}`), false, "a practice game", Standard},
	} {
		v := Judge(c.m)
		if v.Rated != c.rated || v.Why != c.why || v.Mode != c.mode {
			t.Errorf("%s: %+v, want rated %v (%s) in %s", c.name, v, c.rated, c.why, c.mode)
		}
	}
	// the replay of the stored matches agrees: a played kill race changes nobody's rating
	_, out := Rate(onMap("pvp", "", `{"kills":20}`), func(int64, string) store.Rating { return store.Rating{} })
	if len(out) != 0 {
		t.Errorf("a kill race rated: %+v", out)
	}
}
