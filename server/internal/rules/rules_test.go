package rules

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStandardAndPresets(t *testing.T) {
	s := Standard()
	if s != (Combination{Pick: "blind", ShaperPool: "all", Shop: "normal", ItemPool: "all", Victory: "guardian"}) ||
		!s.IsDefault() {
		t.Fatalf("standard %+v", s)
	}
	if !(Combination{}).IsDefault() || (Combination{}).Normal() != s {
		t.Error("an empty combination is not the standard game")
	}
	aram, ok := FindPreset("ARAM")
	if !ok || aram.Options != (Combination{Pick: "random", ShaperPool: "all", Shop: "onDeath", ItemPool: "all",
		Victory: "guardian"}) {
		t.Fatalf("aram %+v %v", aram, ok)
	}
	if p, ok := PresetOf(aram.Options); !ok || p.ID != "aram" {
		t.Errorf("PresetOf(aram) %+v %v", p, ok)
	}
	for _, p := range Presets() {
		if err := p.Options.Validate(); err != nil {
			t.Errorf("preset %s: %v", p.ID, err)
		}
	}
	for _, c := range Categories() {
		if _, ok := option(c.ID, c.Default); !ok {
			t.Errorf("%s: default %q not an option", c.ID, c.Default)
		}
		if o, _ := option(c.ID, c.Default); !o.Bots {
			t.Errorf("%s: the default doesn't support bots", c.ID)
		}
	}
}

func TestDescribeAndPrefix(t *testing.T) {
	cases := []struct {
		c            Combination
		text, prefix string
	}{
		{Standard(), "Standard", ""},
		{with(Standard(), Pick, "random", Shop, "onDeath"), "All Random, Shop on Death", "[ARAM] "},
		{with(Standard(), Pick, "random"), "All Random", "[All Random] "},
		{with(Standard(), ItemPool, "basics"), "Basic items only", CustomPrefix},
		{with(Standard(), ItemPool, "lightning"), "Lightning items only", CustomPrefix}, // LIGHTNING1
		{with(Standard(), Pick, "draft", ShaperPool, "melee", Shop, "onDeath", ItemPool, "noLegendary"),
			"Draft, Melee shapers, Shop on Death, No legendary items", CustomPrefix},
		// WINLOBBY: the win condition's name last, its prefix in a map's words after the rest's
		{with(Standard(), Victory, "bind1"), "First Binding", "[Bind 1] "},
		{with(Standard(), Victory, "race20"), "Kill race 20", "[Race 20] "},
		{with(Standard(), Victory, "time15"), "Most kills after 15 min", "[15 min] "},
		{with(Standard(), Victory, "race20time20"), "Race 20 or 20 min", "[Race 20/20m] "},
		{with(Standard(), Pick, "draft", Victory, "bind3"), "Draft, First to 3 Bindings", "[Draft] [Bind 3] "},
		{with(Standard(), Shop, "onDeath", Victory, "race10"), "Shop on Death, Kill race 10", CustomPrefix + "[Race 10] "},
		{with(Standard(), Victory, "guardian"), "Standard", ""},
		{Combination{Pick: "random"}, "All Random", "[All Random] "}, // empty categories are their defaults
	}
	for _, c := range cases {
		if got := Describe(c.c); got != c.text {
			t.Errorf("Describe(%+v) = %q, want %q", c.c, got, c.text)
		}
		if got := Prefix(c.c); got != c.prefix {
			t.Errorf("Prefix(%+v) = %q, want %q", c.c, got, c.prefix)
		}
	}
}

func TestParse(t *testing.T) {
	good := []struct {
		spec string
		want Combination
	}{
		{"aram", with(Standard(), Pick, "random", Shop, "onDeath")},
		{" Standard ", Standard()},
		{`{"pick":"random"}`, with(Standard(), Pick, "random")},
		{`{}`, Standard()},
		{`{"pick":"draft","shaperPool":"ranged","shop":"normal","itemPool":"basics"}`,
			Combination{Pick: "draft", ShaperPool: "ranged", Shop: "normal", ItemPool: "basics", Victory: "guardian"}},
		{`{"victory":"bind1"}`, with(Standard(), Victory, "bind1")},                                   // WINLOBBY
		{"pick=draft,victory=race20time20", with(Standard(), Pick, "draft", Victory, "race20time20")}, // the short form
		{"pick=random, shaperPool=melee", with(Standard(), Pick, "random", ShaperPool, "melee")},      // RULES3's short form
		{"shaperPool=tank", with(Standard(), ShaperPool, "tank")},
	}
	for _, g := range good {
		got, err := Parse(g.spec)
		if err != nil || got != g.want {
			t.Errorf("Parse(%s) = %+v, %v; want %+v", g.spec, got, err, g.want)
		}
	}
	bad := []string{
		"nonsense",
		`{"pick":"chaos"}`,     // an unknown option
		`{"speed":"fast"}`,     // an unknown category
		`{"pick":1}`,           // not a string
		`{"pick":null}`,        // not a string either
		`[]`,                   // not an object
		`null`,                 // nothing
		`{"pick":"random"} {}`, // a second value
		`{"pick":"random"`,     // cut off
		`{"pick":"` + strings.Repeat("x", MaxJSON) + `"}`, // too long
		"pick=chaos",           // the short form: an unknown option
		"speed=fast",           // an unknown category
		"pick=random,melee",    // no category=option
		`{"victory":"race99"}`, // WINLOBBY: only the catalog's win conditions
		"victory=guardian=1",
	}
	for _, b := range bad {
		if c, err := Parse(b); err == nil {
			t.Errorf("Parse(%.40s) = %+v, want an error", b, c)
		}
	}
	if _, err := Parse(`{"pick":"chaos"}`); !errors.Is(err, ErrUnknown) {
		t.Errorf("an unknown option's error %v doesn't wrap ErrUnknown", err)
	}
}

// The JSON the wire carries: the five categories in catalog order (a combination without a win condition, from
// before WINLOBBY, writes four and reads as "guardian").
func TestJSON(t *testing.T) {
	b, err := json.Marshal(with(Standard(), Pick, "random", Shop, "onDeath"))
	if err != nil || string(b) !=
		`{"pick":"random","shaperPool":"all","shop":"onDeath","itemPool":"all","victory":"guardian"}` {
		t.Errorf("JSON %s %v", b, err)
	}
	old := []byte(`{"pick":"random","shaperPool":"all","shop":"onDeath","itemPool":"all"}`)
	if c, err := ParseJSON(old); err != nil || c != with(Standard(), Pick, "random", Shop, "onDeath") {
		t.Errorf("a record from before WINLOBBY: %+v %v", c, err)
	}
	if b, _ := json.Marshal(Combination{Pick: "blind", ShaperPool: "all", Shop: "normal", ItemPool: "all"}); string(b) !=
		`{"pick":"blind","shaperPool":"all","shop":"normal","itemPool":"all"}` {
		t.Errorf("an empty win condition is left out: %s", b)
	}
	c, err := ParseJSON(b)
	if err != nil || Describe(c) != "All Random, Shop on Death" {
		t.Errorf("round trip %+v %v", c, err)
	}
	v, _ := json.Marshal(View())
	if !strings.Contains(string(v), `{"id":"pick","name":"Shaper pick","default":"blind","options":[{"id":"blind","name":"Blind pick","bots":true}`) ||
		!strings.Contains(string(v), `{"id":"aram","name":"ARAM","options":{"pick":"random"`) {
		t.Errorf("catalog view %s", v)
	}
	// LIGHTNING1: the pool is in the catalog; the host feature it needs stays off the wire
	if !strings.Contains(string(v), `{"id":"lightning","name":"Lightning items only","bots":false}`) ||
		strings.Contains(string(v), HostRules2) {
		t.Errorf("catalog view %s", v)
	}
}

type fakeCatalog map[string][2]int

func (f fakeCatalog) PoolSize(p string) (int, int, bool) {
	v, ok := f[p]
	return v[0], v[1], ok
}

func categories(fs []Finding) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Category)
	}
	return strings.Join(out, ",")
}

// Each option on its own, and each listed interaction (not every combination).
func TestCheck(t *testing.T) {
	host := Context{HostRules: true}
	bots := Context{HostRules: true, Bots: true, BotSeats: 4}
	rules2 := Context{HostRules: true, HostFeatures: []string{"rules", HostRules2}}
	mr2 := Context{HostRules: true, HostFeatures: []string{"rules", HostRules2, "maprules", HostMapRules2}}
	small := fakeCatalog{"all": {24, 10}, "melee": {4, 4}, "ranged": {16, 3}}
	cases := []struct {
		name string
		c    Combination
		ctx  Context
		want string // the findings' categories
	}{
		{"standard, old host", Standard(), Context{}, ""},
		{"standard with bots, old host", Standard(), Context{Bots: true, BotSeats: 5}, ""},
		{"rules, old host", with(Standard(), Pick, "random"), Context{}, FindingHost},
		{"all random", with(Standard(), Pick, "random"), host, ""},
		{"all random with bots", with(Standard(), Pick, "random"), bots, ""},
		{"draft", with(Standard(), Pick, "draft"), host, ""},
		{"draft with bots", with(Standard(), Pick, "draft"), bots, Pick},
		{"melee pool", with(Standard(), ShaperPool, "melee"), host, ""},
		{"melee pool, bots", with(Standard(), ShaperPool, "melee"), bots, ""},
		{"shop on death", with(Standard(), Shop, "onDeath"), host, ""},
		{"shop on death with bots", with(Standard(), Shop, "onDeath"), bots, Shop},
		{"item pool", with(Standard(), ItemPool, "basics"), host, ""},
		{"item pool with bots", with(Standard(), ItemPool, "noLegendary"), bots, ItemPool},
		{"aram with bots", with(Standard(), Pick, "random", Shop, "onDeath"), bots, Shop},
		{"item pool and shop on death", with(Standard(), Shop, "onDeath", ItemPool, "basics"), host, ""},
		// the pool against the pick
		{"a small pool", with(Standard(), ShaperPool, "melee"), Context{HostRules: true, Catalog: small}, ShaperPool},
		{"a draft in a pool of 16", with(Standard(), Pick, "draft", ShaperPool, "ranged"),
			Context{HostRules: true, Catalog: small}, ""},
		{"a draft in all 24 of a smaller catalog", with(Standard(), Pick, "draft", ShaperPool, "all"),
			Context{HostRules: true, Catalog: fakeCatalog{"all": {12, 12}}}, ShaperPool},
		// the pool against the bots
		{"too few bot shapers", with(Standard(), ShaperPool, "ranged"),
			Context{HostRules: true, Bots: true, BotSeats: 4, Catalog: small}, FindingBots},
		{"enough bot shapers", with(Standard(), ShaperPool, "ranged"),
			Context{HostRules: true, Bots: true, BotSeats: 3, Catalog: small}, ""},
		{"no such pool in the catalog", with(Standard(), ShaperPool, "ranged"),
			Context{HostRules: true, Catalog: fakeCatalog{"all": {24, 10}}}, ShaperPool},
		{"unknown ids", Combination{Pick: "chaos", Shop: "always"}, host, Pick + "," + Shop},
		// M30 (LIGHTNING1): the pool "lightning" needs a host that names rules2; without it the rules host would take
		// the pool as "all items"
		{"lightning, rules2 host", with(Standard(), ItemPool, "lightning"), rules2, ""},
		{"lightning, rules host without rules2", with(Standard(), ItemPool, "lightning"), host, ItemPool},
		{"lightning, a host naming other features", with(Standard(), ItemPool, "lightning"),
			Context{HostRules: true, HostFeatures: []string{"rules", "x"}}, ItemPool},
		{"lightning, old host", with(Standard(), ItemPool, "lightning"), Context{}, FindingHost},
		{"lightning with bots", with(Standard(), ItemPool, "lightning"),
			Context{HostRules: true, HostFeatures: []string{HostRules2}, Bots: true, BotSeats: 4}, ItemPool},
		{"lightning with bots, no rules2", with(Standard(), ItemPool, "lightning"), bots, ItemPool + "," + ItemPool},
		{"lightning and shop on death", with(Standard(), Shop, "onDeath", ItemPool, "lightning"), rules2, ""},
		{"rules2 host, standard options", with(Standard(), ItemPool, "basics"), rules2, ""},
		// WINLOBBY: a win condition needs a host that names maprules2; bots play every one; the map's own victory
		// leaves only Guardian only
		{"first binding, maprules2 host", with(Standard(), Victory, "bind1"), mr2, ""},
		{"first binding with bots", with(Standard(), Victory, "bind1"),
			Context{HostRules: true, HostFeatures: []string{HostMapRules2}, Bots: true, BotSeats: 5}, ""},
		{"kill race, host without maprules2", with(Standard(), Victory, "race20"), rules2, Victory},
		{"kill race, old host", with(Standard(), Victory, "race20"), Context{}, FindingHost},
		{"kill race on a map with its own victory", with(Standard(), Victory, "race20"),
			Context{HostRules: true, HostFeatures: []string{HostMapRules2}, MapVictory: true}, Victory},
		{"guardian on a map with its own victory", Standard(), Context{HostRules: true, MapVictory: true}, ""},
		{"draft and race 20 or 20 min", with(Standard(), Pick, "draft", Victory, "race20time20"), mr2, ""},
	}
	for _, c := range cases {
		got := Check(c.c, c.ctx)
		if categories(got) != c.want {
			t.Errorf("%s: findings %q, want %q (%s)", c.name, categories(got), c.want, DescribeFindings(got))
		}
	}
}

// WINLOBBY: the lobby's win conditions in numbers, as a map's victory (startGame.victory), and their catalog entry.
func TestGoals(t *testing.T) {
	want := map[string]Goal{"guardian": {}, "race10": {Kills: 10}, "race20": {Kills: 20}, "race30": {Kills: 30},
		"time15": {Minutes: 15}, "race20time20": {Kills: 20, Minutes: 20}, "bind1": {Bindings: 1}, "bind3": {Bindings: 3}}
	cat, ok := category(Victory)
	if !ok || cat.Default != "guardian" || len(cat.Options) != 8 {
		t.Fatalf("the victory category %+v %v", cat, ok)
	}
	for _, o := range cat.Options {
		g, known := want[o.ID]
		if !known {
			t.Errorf("an option %q the user didn't choose", o.ID)
		}
		if got := with(Standard(), Victory, o.ID).Goal(); got != g {
			t.Errorf("%s: goal %+v, want %+v", o.ID, got, g)
		}
		if !o.Bots {
			t.Errorf("%s: bots can't play it", o.ID)
		}
		if (o.Host == HostMapRules2) == (o.ID == "guardian") {
			t.Errorf("%s: host feature %q", o.ID, o.Host)
		}
	}
	if !(Combination{}).Goal().IsZero() || !Standard().Goal().IsZero() {
		t.Error("the standard game has a goal")
	}
	v, _ := json.Marshal(View())
	if !strings.Contains(string(v), `{"id":"victory","name":"Win condition","default":"guardian","options":[{"id":"guardian","name":"Guardian only","bots":true}`) ||
		strings.Contains(string(v), HostMapRules2) {
		t.Errorf("catalog view %s", v)
	}
}

// WINLOBBY: Compact leaves the Guardian's win condition out of the wire's and the record's JSON (as before WINLOBBY).
func TestCompact(t *testing.T) {
	if c := with(Standard(), Pick, "random").Compact(); c.Victory != "" || c.Pick != "random" || c.Shop != "normal" {
		t.Errorf("compact %+v", c)
	}
	if c := (Combination{Victory: "bind1"}).Compact(); c.Victory != "bind1" || c.Pick != "blind" {
		t.Errorf("compact %+v", c)
	}
}
