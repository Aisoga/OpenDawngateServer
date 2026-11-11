package blaze

import (
	"strings"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/rules"
)

// pickContent is a content with every shaper of the pools' table (as "<class>.Noun", titled) plus an untitled one
// (offered to nobody, SHAPERPOOL).
func pickContent() *content.Content {
	c := &content.Content{}
	for i, f := range rules.Facts() {
		c.Items = append(c.Items, content.Item{CatalogID: uint32(0x1000 + i), Name: f.Class, ItemType: content.ItemShaper,
			Asset: strings.TrimPrefix(f.Class, "PC_") + ".Noun", Shaper: f.Class, Title: f.Class})
	}
	c.Items = append(c.Items, content.Item{CatalogID: 0x2000, Name: "weaver", ItemType: content.ItemShaper,
		Asset: "Weaver.Noun", Shaper: "PC_Weaver"})
	return c
}

// A roster as lobbyContent leaves it: every human's list holds every offered shaper, the first owned.
func pickRoster(c *content.Content, humans [][2]int) []gmshost.RosterEntry {
	var out []gmshost.RosterEntry
	for i, h := range humans {
		e := gmshost.RosterEntry{User: int64(2000000001 + i), Index: h[0], Team: h[1]}
		for k, it := range c.Items {
			if it.Title == "" {
				continue
			}
			o := gmshost.ShaperOffer{Noun: it.Asset}
			if k == 0 {
				o.Flags = gmshost.ShaperOwned
			}
			e.Shapers = append(e.Shapers, o)
		}
		out = append(out, e)
	}
	out = append(out, gmshost.RosterEntry{User: 2000000099, Index: 10, Spectator: true})
	return out
}

func classOfNoun(noun string) string { return "PC_" + strings.TrimSuffix(noun, ".Noun") }

// All Random: each human's list is one shaper (available), never twice within a team, from the server's seed; a pool
// keeps only its shapers (available) and names them for the bots; the standard pick in the pool "all" changes nothing.
func TestApplyPickRules(t *testing.T) {
	var log strings.Builder
	c := pickContent()
	seed := uint64(41)
	s := &Server{Log: logging.New(&log), Content: c, RollSeed: func() uint64 { seed++; return seed }}
	humans := [][2]int{{0, 1}, {1, 1}, {2, 1}, {3, 1}, {4, 1}, {5, 2}, {6, 2}}

	// the standard pick in the pool "all": nothing changes
	sg := gmshost.StartGame{Roster: pickRoster(c, humans), Rules: &gmshost.Rules{Pick: "blind", ShaperPool: "all",
		Shop: "onDeath", ItemPool: "all"}}
	s.applyPickRules(1, &sg, nil)
	if len(sg.Roster[0].Shapers) != 35 || sg.PoolClasses != nil || sg.Roster[0].Shapers[1].Flags != 0 {
		t.Fatalf("Shop on Death alone changed the lists: %d shapers, pool %v", len(sg.Roster[0].Shapers), sg.PoolClasses)
	}

	for round := 0; round < 50; round++ {
		sg := gmshost.StartGame{Roster: pickRoster(c, humans), Rules: &gmshost.Rules{Pick: "random", ShaperPool: "all",
			Shop: "normal", ItemPool: "all"}}
		s.applyPickRules(1, &sg, nil)
		if sg.PoolClasses != nil {
			t.Fatalf("the pool all sent classes %v", sg.PoolClasses)
		}
		seen := map[int]map[string]bool{1: {}, 2: {}}
		for _, e := range sg.Roster {
			if e.Spectator {
				if len(e.Shapers) != 0 {
					t.Fatalf("the spectator got a list")
				}
				continue
			}
			if len(e.Shapers) != 1 || e.Shapers[0].Flags&gmshost.ShaperAvailable == 0 {
				t.Fatalf("round %d: user %d's list %+v", round, e.User, e.Shapers)
			}
			n := e.Shapers[0].Noun
			if n == "Weaver.Noun" || seen[e.Team][n] {
				t.Fatalf("round %d: %s (team %d) rolled twice or unoffered", round, n, e.Team)
			}
			seen[e.Team][n] = true
		}
	}
	if !strings.Contains(log.String(), "game 1: All Random (seed 42, 35 shapers to roll from): user 2000000001 (index 0, team 1) -> ") {
		t.Errorf("the roll's log line:\n%s", log.String())
	}

	// the melee pool, blind pick: only melee and hybrid shapers, all available; the bots get the classes
	sg = gmshost.StartGame{Roster: pickRoster(c, humans), Rules: &gmshost.Rules{Pick: "blind", ShaperPool: "melee",
		Shop: "normal", ItemPool: "all"}}
	s.applyPickRules(2, &sg, nil)
	if len(sg.PoolClasses) != 17 || sg.PoolClasses[0] != "PC_Artificer" {
		t.Errorf("poolClasses %v", sg.PoolClasses)
	}
	for _, e := range sg.Roster[:len(humans)] {
		if len(e.Shapers) != 17 {
			t.Fatalf("user %d: %d shapers in the melee pool", e.User, len(e.Shapers))
		}
		for _, o := range e.Shapers {
			cls := classOfNoun(o.Noun)
			ok := false
			for _, f := range rules.Facts() {
				ok = ok || (f.Class == cls && rules.InPool("melee", f))
			}
			if !ok || o.Flags&gmshost.ShaperAvailable == 0 {
				t.Fatalf("user %d: %+v in the melee pool", e.User, o)
			}
		}
	}

	// All Random in the support pool (5 shapers): the full team of five gets all five, each once
	sg = gmshost.StartGame{Roster: pickRoster(c, humans), Rules: &gmshost.Rules{Pick: "random", ShaperPool: "support",
		Shop: "normal", ItemPool: "all"}}
	s.applyPickRules(3, &sg, nil)
	got := map[string]bool{}
	for _, e := range sg.Roster[:5] {
		got[classOfNoun(e.Shapers[0].Noun)] = true
	}
	if len(got) != 5 || !got["PC_Sculptor"] || !got["PC_Artist"] {
		t.Errorf("team 1 in the support pool rolled %v", got)
	}
	if len(sg.PoolClasses) != 5 {
		t.Errorf("poolClasses %v", sg.PoolClasses)
	}

	// no rules (an old host, the standard game): nothing
	sg = gmshost.StartGame{Roster: pickRoster(c, humans)}
	s.applyPickRules(4, &sg, nil)
	if len(sg.Roster[0].Shapers) != 35 {
		t.Errorf("no rules changed the list")
	}
}

// The checks see the server's shaper select and its content's pools: without --shaper-select All Random is refused, and
// a server whose content offers few shapers can't play a pool.
func TestRulesContextPools(t *testing.T) {
	s := &Server{Content: pickContent()}
	if n, b, _ := s.rulesPools().PoolSize("all"); n != 35 || b != 13 {
		t.Errorf("the content's pool all: %d, %d", n, b)
	}
	if s.shaperSelectRuns() {
		t.Error("shaper select runs without --shaper-select")
	}
	fs := rules.Check(rules.Combination{Pick: "random"}, rules.Context{HostRules: true, NoShaperSelect: !s.shaperSelectRuns(),
		Catalog: s.rulesPools()})
	if len(fs) != 1 || fs[0].Category != rules.Pick {
		t.Errorf("findings %s", rules.DescribeFindings(fs))
	}
	s.Content = &content.Content{Items: pickContent().Items[:3]}
	fs = rules.Check(rules.Combination{ShaperPool: "melee"}, rules.Context{HostRules: true, Catalog: s.rulesPools()})
	if len(fs) != 1 || fs[0].Category != rules.ShaperPool {
		t.Errorf("findings %s", rules.DescribeFindings(fs))
	}
}

// The draft (RULES5b): each human's list is its drafted shaper alone (available), the pool's filter first; a human
// without a drafted shaper keeps its list; the spectator none.
func TestApplyPickRulesDraft(t *testing.T) {
	var log strings.Builder
	c := pickContent()
	s := &Server{Log: logging.New(&log), Content: c}
	sg := gmshost.StartGame{Roster: pickRoster(c, [][2]int{{0, 1}, {5, 2}, {6, 2}}), Rules: &gmshost.Rules{Pick: "draft",
		ShaperPool: "melee", Shop: "normal", ItemPool: "all"}}
	s.applyPickRules(1, &sg, map[int64]draftedSeat{2000000001: {Shaper: "PC_Warden"}, 2000000002: {Shaper: "PC_Artificer", Role: "hunter"}})
	if len(sg.PoolClasses) != 17 {
		t.Errorf("poolClasses %v", sg.PoolClasses)
	}
	for _, want := range []struct {
		i    int
		noun string
		n    int
	}{{0, "Warden.Noun", 1}, {1, "Artificer.Noun", 1}, {2, "", 17}} {
		e := sg.Roster[want.i]
		if len(e.Shapers) != want.n || want.noun != "" && (e.Shapers[0].Noun != want.noun || e.Shapers[0].Flags&gmshost.ShaperAvailable == 0) {
			t.Errorf("user %d's list %+v", e.User, e.Shapers)
		}
	}
	if len(sg.Roster[3].Shapers) != 0 {
		t.Errorf("the spectator got a list")
	}
	for _, want := range []string{"game 1: the draft's shapers: user 2000000001 (index 0, team 1) -> Warden.Noun",
		"user 2000000003 (team 2): no drafted shaper in its list (\"\"), keeps its list",
		"game 1: the draft's roles: user 2000000002 (index 5) hunter; the host presets each in shaper select"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
	// DRAFT3: the declared role goes into the roster for the host's preset; none stays none
	for i, want := range []string{"", "hunter", "", ""} {
		if sg.Roster[i].Role != want {
			t.Errorf("roster %d: role %q, want %q", i, sg.Roster[i].Role, want)
		}
	}
}

// DRAFT3: the draft's role ids (rules.DraftRoles, the wire's) are the roster's (gmshost.Roles), in the record's order.
func TestDraftRolesAreTheRosterRoles(t *testing.T) {
	if len(rules.DraftRoles) != len(gmshost.Roles) {
		t.Fatalf("%d draft roles, %d roster roles", len(rules.DraftRoles), len(gmshost.Roles))
	}
	for i, r := range rules.DraftRoles {
		if gmshost.RoleIndex(r.ID) != i {
			t.Errorf("draft role %q is roster role %d, want %d", r.ID, gmshost.RoleIndex(r.ID), i)
		}
	}
}
