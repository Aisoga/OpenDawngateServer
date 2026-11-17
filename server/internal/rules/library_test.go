package rules

import (
	"strings"
	"testing"
)

const presetFile = `# the operator's presets
bindBrawl | Bind Brawl | pick=random,victory=bind1
shopless  | No Shop    | {"shop":"onDeath","itemPool":"all"}

aram2     | ARAM again | pick=random,shop=onDeath
draft     | My Draft   | pick=draft,victory=race20
BINDBRAWL | Twice      | pick=random,victory=bind3
x | a | b | c
9lives    | Nine       | pick=random,shop=onDeath,itemPool=lightning
bad       | [Bad]      | pick=random
weird     | Weird      | pick=fast
noform    | No Form    | aram
same      | Same       | victory=bind1,pick=random
`

// The operator's file: good lines become presets, every bad line is a problem with its number and the rest still read.
func TestParsePresets(t *testing.T) {
	l, problems := ParsePresets([]byte(presetFile))
	var ids []string
	for _, p := range l.Own() {
		ids = append(ids, p.ID+"="+p.Name)
	}
	if got := strings.Join(ids, ","); got != "bindBrawl=Bind Brawl,shopless=No Shop" {
		t.Errorf("presets %s", got)
	}
	want := []string{
		`line 5: aram2: the same rules as the preset aram (ARAM)`,
		`line 6: draft: the id is a built-in preset's`,
		`line 7: BINDBRAWL: the id is used before (Bind Brawl)`,
		`line 8: not "id | Name | rules" (4 fields)`,
		`line 9: the id "9lives" isn't 1 to 24 letters, digits, - and _ starting with a letter`,
		`line 10: bad: the name has [, ] or |`,
		`line 11: weird: rules: unknown option "fast" for pick`,
		`line 12: noform: the rules "aram" are no category=option list (e.g. pick=random,shop=onDeath)`,
		`line 13: same: the same rules as the preset bindBrawl (Bind Brawl)`,
	}
	if strings.Join(problems, "\n") != strings.Join(want, "\n") {
		t.Errorf("problems\n%s\nwant\n%s", strings.Join(problems, "\n"), strings.Join(want, "\n"))
	}
	if _, p := ParsePresets(make([]byte, MaxPresetFile+1)); len(p) != 1 || !strings.Contains(p[0], "no presets read") {
		t.Errorf("a big file: %v", p)
	}
	var many strings.Builder
	pools := []string{"all", "melee", "ranged"}
	n := 0
	for _, pool := range pools {
		for _, shop := range []string{"normal", "onDeath"} {
			for _, item := range []string{"all", "lightning"} {
				for _, v := range []string{"guardian", "race10", "race20", "race30", "time15", "bind1", "bind3"} {
					n++
					many.WriteString("p" + strings.Repeat("x", n%3) + string(rune('a'+n%26)) + string(rune('a'+n/26)) +
						" | P | shaperPool=" + pool + ",shop=" + shop + ",itemPool=" + item + ",victory=" + v + ",pick=army\n")
				}
			}
		}
	}
	l, p := ParsePresets([]byte(many.String()))
	if len(l.Own()) != MaxOwnPresets || len(p) != n-MaxOwnPresets || !strings.Contains(p[0], "more than 50 presets") {
		t.Errorf("%d presets, problems %d: %v", len(l.Own()), len(p), p[:min(len(p), 2)])
	}
}

// Find, Of, Resolve and Prefix with the operator's presets; a nil Library has the built-in ones.
func TestLibrary(t *testing.T) {
	l, _ := ParsePresets([]byte(presetFile))
	if p, ok := l.Find("BINDbrawl"); !ok || p.Name != "Bind Brawl" {
		t.Errorf("Find: %+v %v", p, ok)
	}
	if p, ok := l.Find("draft"); !ok || p.Name != "Draft" {
		t.Errorf("Find a built-in: %+v %v", p, ok)
	}
	var none *Library
	if _, ok := none.Find("bindBrawl"); ok {
		t.Error("a nil Library found an operator's preset")
	}
	for _, c := range []struct{ spec, want string }{
		{"bindBrawl", "[Bind Brawl] [Bind 1] "},
		{"pick=random,victory=bind3", "[All Random] [Bind 3] "}, // a built-in without its win condition
		{"shop=onDeath", "[No Shop] "},                          // the operator's preset with the Guardian's
		{"shop=onDeath,victory=race20", "[No Shop] [Race 20] "}, // the same without its win condition
		{"pick=draft", "[Draft] "},                              // built-ins first
		{"pick=random,shop=onDeath,itemPool=lightning", CustomPrefix},
		{"standard", ""},
		{"victory=bind1", "[Bind 1] "},
	} {
		comb, err := l.Resolve(c.spec)
		if err != nil {
			t.Fatalf("%s: %v", c.spec, err)
		}
		if got := l.Prefix(comb); got != c.want {
			t.Errorf("%s: prefix %q, want %q", c.spec, got, c.want)
		}
		if none.Prefix(comb) != Prefix(comb) {
			t.Errorf("%s: a nil Library's prefix %q, Prefix's %q", c.spec, none.Prefix(comb), Prefix(comb))
		}
	}
	if p, ok := l.Of(with(Standard(), Pick, "random", Victory, "bind1")); !ok || p.ID != "bindBrawl" {
		t.Errorf("Of: %+v %v", p, ok)
	}
	if _, err := l.Resolve("brawl"); err == nil || !strings.Contains(err.Error(), "unknown preset") {
		t.Errorf("an unknown id: %v", err)
	}
	if _, err := none.Resolve("bindBrawl"); err == nil {
		t.Error("a nil Library resolved an operator's preset")
	}
}

// A map's suggestion: ids and rules, an unknown id a warning, a win condition on a victory map an error, a combination
// the lobby refuses a warning.
func TestCheckSuggestion(t *testing.T) {
	for _, c := range []struct {
		spec       string
		mapVictory bool
		errs, warn string
	}{
		{"", false, "", ""},
		{"draft", false, "", ""},
		{"pick=random,shop=onDeath,victory=bind1", false, "", ""},
		{`{"pick":"random"}`, false, "", ""},
		{"brawl", false, "", `no built-in preset is called "brawl": only servers that define this preset use it`},
		{"pick=fast", false, `unknown option "fast" for pick`, ""},
		{"pick random", false, `"pick random" is neither a preset's id nor rules`, ""},
		{"victory=bind1", true, "the map has its own win condition", ""},
		{"pick=random,victory=guardian", true, "", ""},
		{"pick=draft,shaperPool=tank", false, "", "Draft needs at least 14 shapers in the pool"},
	} {
		errs, warns := CheckSuggestion(c.spec, c.mapVictory)
		e, w := strings.Join(errs, "; "), strings.Join(warns, "; ")
		if (c.errs == "") != (e == "") || !strings.Contains(e, c.errs) || (c.warn == "") != (w == "") ||
			!strings.Contains(w, c.warn) {
			t.Errorf("%q (map victory %v): errors %q, warnings %q", c.spec, c.mapVictory, e, w)
		}
	}
}

func TestValidPresetID(t *testing.T) {
	for id, ok := range map[string]bool{"a": true, "bind-brawl_2": true, "": false, "2a": false, "a b": false,
		"a=b": false, strings.Repeat("a", MaxPresetID): true, strings.Repeat("a", MaxPresetID+1): false, "é": false} {
		if ValidPresetID(id) != ok {
			t.Errorf("%q: %v", id, !ok)
		}
	}
}
