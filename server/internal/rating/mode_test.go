package rating

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/store"
)

// withRules is match(kind, ...) with the rules column set.
func withRules(m store.Match, rules string) store.Match {
	m.Rules = rules
	return m
}

// A game's type comes from its rules (M30's default): PvP and a custom game without rules (or with every default) are
// Standard, exactly the draft preset is Draft, any other combination (ARAM, a draft with another option, rules this
// build can't read) Custom.
func TestModeOf(t *testing.T) {
	for _, c := range []struct {
		kind, rules, want string
	}{
		{"pvp", "", Standard},
		{"pvp", `{"pick":"random"}`, Standard}, // matchmaking has no rules; a PvP game is the standard game
		{"custom", "", Standard},
		{"custom", `{"pick":"blind","shaperPool":"all","shop":"normal","itemPool":"all"}`, Standard},
		{"custom", `{"pick":"draft","shaperPool":"all","shop":"normal","itemPool":"all"}`, Draft},
		{"custom", `{"pick":"draft"}`, Draft},
		{"custom", `{"pick":"draft","shop":"onDeath"}`, Custom},
		{"custom", `{"pick":"random","shaperPool":"all","shop":"onDeath","itemPool":"all"}`, Custom}, // ARAM
		{"custom", `{"pick":"random"}`, Custom},                                                      // All Random
		{"custom", `{"shaperPool":"melee"}`, Custom},
		{"custom", `{"pick":"someday"}`, Custom}, // unreadable: never the standard's
		{"custom", `not json`, Custom},
		{"practice", "", Standard},
	} {
		if got := ModeOf(withRules(match(c.kind, 1, 1, 1), c.rules)); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.kind, c.rules, got, c.want)
		}
	}
	if !reflect.DeepEqual(Modes(), []string{"standard", "draft", "custom"}) {
		t.Errorf("Modes %v", Modes())
	}
}

// Since M30 a game with rules is rated in its type, and a custom game the admin made unrated never is (MMR1).
func TestJudgeModes(t *testing.T) {
	for _, c := range []struct {
		name  string
		m     store.Match
		rated bool
		mode  string
		why   string
	}{
		{"ARAM, 1 v 1", withRules(match("custom", 1, 1, 1), `{"pick":"random","shop":"onDeath"}`), true, Custom, "1 v 1 humans"},
		{"draft, 2 v 2", withRules(match("custom", 2, 2, 2), `{"pick":"draft"}`), true, Draft, "2 v 2 humans"},
		{"draft, 2 v 3", withRules(match("custom", 2, 2, 3), `{"pick":"draft"}`), false, Draft, "2 v 3 humans"},
		{"standard custom", match("custom", 1, 3, 3), true, Standard, "3 v 3 humans"},
		{"draft, no winner", withRules(match("custom", 0, 1, 1), `{"pick":"draft"}`), false, Draft, "no winner"},
	} {
		v := Judge(c.m)
		if v.Rated != c.rated || v.Mode != c.mode || v.Why != c.why {
			t.Errorf("%s: %+v, want rated %v %s (%s)", c.name, v, c.rated, c.mode, c.why)
		}
	}
	for _, rules := range []string{"", `{"pick":"draft"}`, `{"pick":"random"}`} {
		m := withRules(match("custom", 1, 2, 2), rules)
		m.Unrated = true
		if v := Judge(m); v.Rated || v.Why != "unrated by the admin" || v.DescribeNotRated() != "not rated (unrated by the admin)" {
			t.Errorf("unrated %q: %+v", rules, v)
		}
		if v, cs := Rate(m, func(int64, string) store.Rating { return store.Rating{} }); v.Rated || cs != nil {
			t.Errorf("unrated %q rated: %+v %+v", rules, v, cs)
		}
	}
}

// Rate takes each human's rating in the match's type: a draft game reads and moves the draft ratings only.
func TestRateInMode(t *testing.T) {
	asked := map[string]int{}
	ratings := map[string]store.Rating{Standard: {Value: 1900, RD: 50, Vol: 0.06}, Draft: {Value: 1500, RD: 350, Vol: 0.06}}
	m := withRules(match("custom", 1, 1, 1), `{"pick":"draft"}`)
	v, cs := Rate(m, func(_ int64, mode string) store.Rating { asked[mode]++; return ratings[mode] })
	if !v.Rated || v.Mode != Draft || len(cs) != 2 || !reflect.DeepEqual(asked, map[string]int{Draft: 2}) ||
		cs[0].Before != ratings[Draft] || cs[0].After.Value <= 1500 {
		t.Errorf("%+v %+v asked %v", v, cs, asked)
	}
}

// SeedModes replays only the draft and custom matches, oldest first, each type against its own ratings, and leaves the
// standard ratings and days exactly as Seed made them; a match the admin made unrated stays out; once only (its own
// mark), and Seed's mark is untouched.
func TestSeedModes(t *testing.T) {
	st := store.New()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	var users, personas [3]int64
	for i, name := range []string{"a", "b", "c"} {
		a, err := st.Create(store.NewAccount{Name: name + "@example.com"}, now.AddDate(0, 0, -30))
		if err != nil {
			t.Fatal(err)
		}
		users[i], personas[i] = a.UserID, a.Personas[0].ID
	}
	seat := func(i, team int) store.MatchSeat {
		return store.MatchSeat{Seat: i, Team: team, UserID: users[i], PersonaID: personas[i]}
	}
	const draft, aram = `{"pick":"draft"}`, `{"pick":"random","shop":"onDeath"}`
	matches := []store.Match{
		{Kind: "pvp", Winner: 1, Ended: now.AddDate(0, 0, -10), Seats: []store.MatchSeat{seat(0, 1), seat(1, 2)}},
		{Kind: "custom", Rules: draft, Winner: 2, Ended: now.AddDate(0, 0, -9), Seats: []store.MatchSeat{seat(0, 1), seat(1, 2)}},
		{Kind: "custom", Rules: aram, Winner: 1, Ended: now.AddDate(0, 0, -2), Seats: []store.MatchSeat{seat(1, 1), seat(2, 2)}},
		{Kind: "custom", Winner: 2, Ended: now.AddDate(0, 0, -1), Seats: []store.MatchSeat{seat(0, 1), seat(2, 2)}},
		{Kind: "custom", Rules: draft, Winner: 1, Ended: now.Add(-3 * time.Hour), Seats: []store.MatchSeat{seat(0, 1), seat(2, 2)}},
		{Kind: "custom", Rules: draft, Winner: 1, Unrated: true, Ended: now.Add(-2 * time.Hour),
			Seats: []store.MatchSeat{seat(1, 1), seat(2, 2)}},
		{Kind: "custom", Rules: draft, Winner: 2, Ended: now.Add(-time.Hour), Seats: []store.MatchSeat{seat(0, 1), seat(1, 2)}},
	}
	for _, m := range matches {
		m.Mode, m.Started = m.Kind, m.Ended.Add(-20*time.Minute)
		if _, err := st.InsertMatch(m); err != nil {
			t.Fatal(err)
		}
	}
	// the standard seed first (a server seeded before M30), then a live standard day for c
	std, err := Seed(st, now)
	if err != nil || std.Rated != 2 {
		t.Fatalf("Seed: %+v %v", std, err)
	}
	before := map[int64]store.History{}
	for _, id := range users {
		a, _ := st.Account(id)
		before[id] = a.History
	}
	rep, err := SeedModes(st, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Already || rep.Matches != 7 || rep.Rated != 4 || rep.ByMode[Draft] != 3 || rep.ByMode[Custom] != 1 ||
		rep.Accounts != 3 || !strings.HasPrefix(rep.String(),
		"7 stored matches replayed, 4 rated (draft 3, custom 1), 3 accounts rated, at ") {
		t.Errorf("report %+v: %s", rep, rep)
	}
	// the replay by hand, per type
	cur := map[string]map[int64]store.Rating{Draft: {}, Custom: {}}
	for _, i := range []int{1, 2, 4, 6} {
		v, cs := Rate(matches[i], func(id int64, mode string) store.Rating { return cur[mode][id] })
		for _, c := range cs {
			cur[v.Mode][c.UserID] = c.After
		}
	}
	for i, id := range users {
		a, _ := st.Account(id)
		h, b := a.History, before[id]
		if h.Rating != b.Rating {
			t.Errorf("%d: the standard rating changed: %+v, was %+v", i, h.Rating, b.Rating)
		}
		if len(h.Days) < len(b.Days) {
			t.Fatalf("%d: days lost: %+v", i, h.Days)
		}
		for _, d := range h.Days {
			for _, bd := range b.Days {
				if bd.Day == d.Day && (bd.Rated != d.Rated || bd.RatingBefore != d.RatingBefore || bd.Ranked != d.Ranked) {
					t.Errorf("%d: the standard day %s changed: %+v, was %+v", i, d.Day, d, bd)
				}
			}
		}
		for _, mode := range []string{Draft, Custom} {
			if got, want := h.RatingIn(mode), cur[mode][id]; !near(got.Value, want.Value, 1e-9) || !near(got.RD, want.RD, 1e-9) {
				t.Errorf("%d %s: %+v, want %+v", i, mode, got, want)
			}
		}
	}
	a, _ := st.Account(users[0])
	if r := a.History.RatingIn(Draft); r.Games != 3 || r.Wins != 1 || a.History.RatingIn(Custom) != (store.Rating{}) {
		t.Errorf("a's draft %+v, custom %+v", r, a.History.RatingIn(Custom))
	}
	// a's days: today's two draft games (the one 9 days ago is out of the kept days)
	if d := a.History.Days[len(a.History.Days)-1]; d.Day != "2026-11-12" ||
		d.RatedIn(Draft).Rated != (store.GamesWon{Games: 2, Wins: 1}) {
		t.Errorf("a's today: %+v", d)
	}
	if v, _, found, _ := st.Setting(SeedModesKey); !found || v != rep.Mark {
		t.Errorf("the mark: %q %v", v, found)
	}
	if v, _, _, _ := st.Setting(SeedKey); v != std.Mark {
		t.Errorf("Seed's mark changed: %q", v)
	}
	again, err := SeedModes(st, now.Add(time.Hour))
	if err != nil || !again.Already || again.Mark != rep.Mark {
		t.Errorf("second seed: %+v %v", again, err)
	}
	if b, _ := st.Account(users[0]); !reflect.DeepEqual(b.History, a.History) {
		t.Errorf("the second seed changed a: %+v", b.History)
	}
}

// On a fresh server both seeds run in a row: the standard one rates only the Standard matches (as before M30, when a
// game with rules was never rated), the second only the others.
func TestSeedStandardOnly(t *testing.T) {
	st := store.New()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	var seats []store.MatchSeat
	for i, name := range []string{"a", "b"} {
		a, _ := st.Create(store.NewAccount{Name: name + "@example.com"}, now.AddDate(0, 0, -30))
		seats = append(seats, store.MatchSeat{Seat: i, Team: i + 1, UserID: a.UserID, PersonaID: a.Personas[0].ID})
	}
	for _, rules := range []string{`{"pick":"draft"}`, `{"pick":"random"}`} {
		if _, err := st.InsertMatch(store.Match{Kind: "custom", Mode: "custom", Rules: rules, Winner: 1, Started: now,
			Ended: now, Seats: seats}); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Seed(st, now)
	if err != nil || rep.Rated != 0 || rep.Accounts != 0 {
		t.Errorf("Seed rated games with rules: %+v %v", rep, err)
	}
	rep, err = SeedModes(st, now)
	if err != nil || rep.Rated != 2 || rep.Accounts != 2 {
		t.Errorf("SeedModes: %+v %v", rep, err)
	}
	a, _ := st.Account(seats[0].UserID)
	if a.History.Rating != (store.Rating{}) || a.History.RatingIn(Draft).Games != 1 || a.History.RatingIn(Custom).Games != 1 {
		t.Errorf("a: %+v", a.History)
	}
}
