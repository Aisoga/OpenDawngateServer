package rating

import (
	"math"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/store"
)

func near(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

// Glickman's worked example ("Example of the Glicko-2 system", 2013, steps 1-8): a player rated 1500, RD 200,
// volatility 0.06, tau 0.5, beats a 1400 (RD 30) and loses to a 1550 (RD 100) and a 1700 (RD 300) in one period: his
// published result is 1464.06, RD 151.52, volatility 0.05999.
func TestGlickmanExample(t *testing.T) {
	got := Update(store.Rating{Value: 1500, RD: 200, Vol: 0.06, Games: 7, Wins: 3},
		[]Result{{1400, 30, 1}, {1550, 100, 0}, {1700, 300, 0}})
	if !near(got.Value, 1464.06, 0.01) || !near(got.RD, 151.52, 0.01) || !near(got.Vol, 0.05999, 0.00001) {
		t.Errorf("got %.4f, RD %.4f, volatility %.6f; Glickman's 1464.06, 151.52, 0.05999", got.Value, got.RD, got.Vol)
	}
	if got.Games != 7 || got.Wins != 3 {
		t.Errorf("the games and wins changed: %+v", got)
	}
	// no game: only the deviation grows (step 6), never past the start's
	idle := Update(store.Rating{Value: 1500, RD: 200, Vol: 0.06}, nil)
	if idle.Value != 1500 || !near(idle.RD, math.Sqrt(200*200+math.Pow(0.06*glickoScale, 2)), 1e-9) {
		t.Errorf("a period without games: %+v", idle)
	}
	if capped := Update(store.StartRating, nil); capped.RD != MaxRD {
		t.Errorf("the start's RD grew: %+v", capped)
	}
	// an account without a rating starts at 1500, RD 350
	if fresh := Update(store.Rating{}, []Result{{1500, 350, 1}}); fresh.Value <= 1500 || fresh.RD >= 350 {
		t.Errorf("a new player's win: %+v", fresh)
	}
}

// The other team as one opponent: its mean rating and the root mean square of its RDs; an account without a rating
// counts as 1500, RD 350.
func TestComposite(t *testing.T) {
	got := Composite([]store.Rating{{Value: 1600, RD: 50, Vol: 0.06}, {Value: 1400, RD: 150, Vol: 0.06}, {}})
	if !near(got.Rating, 1500, 1e-9) || !near(got.RD, math.Sqrt((50*50+150*150+350*350)/3.0), 1e-9) {
		t.Errorf("composite %+v", got)
	}
	if one := Composite([]store.Rating{{Value: 1720, RD: 80, Vol: 0.06}}); one.Rating != 1720 || one.RD != 80 {
		t.Errorf("one player: %+v", one)
	}
	if none := Composite(nil); none.Rating != 1500 || none.RD != 350 {
		t.Errorf("no player: %+v", none)
	}
}

// match makes a stored match: humans per team (accounts 1.. on team 1, 101.. on team 2), bots to five a team.
func match(kind string, winner, team1, team2 int) store.Match {
	m := store.Match{Kind: kind, Mode: kind, Winner: winner}
	seat := 0
	add := func(team, humans int, base int64) {
		for i := 0; i < 5; i++ {
			st := store.MatchSeat{Seat: seat, Team: team, Bot: i >= humans}
			if !st.Bot {
				st.UserID, st.PersonaID = base+int64(i), 2000+base+int64(i)
			}
			m.Seats = append(m.Seats, st)
			seat++
		}
	}
	add(1, team1, 1)
	add(2, team2, 101)
	return m
}

// The user's rule: a PvP or custom game with a winner whose teams had as many humans, at least one each; bots don't
// count. The user's examples: 2 v 2 humans with bots filling rated, 1 v 1 with bots rated, 2 v 3 with bots not, humans
// against bots only not; and never a practice game, a practice group's or one without a winner.
func TestJudge(t *testing.T) {
	for _, c := range []struct {
		name  string
		m     store.Match
		rated bool
		why   string
	}{
		{"2 v 2 humans, bots filling", match("pvp", 1, 2, 2), true, "2 v 2 humans"},
		{"1 v 1 with bots", match("custom", 2, 1, 1), true, "1 v 1 humans"},
		{"5 v 5 humans", match("pvp", 2, 5, 5), true, "5 v 5 humans"},
		{"2 v 3 with bots", match("pvp", 1, 2, 3), false, "2 v 3 humans"},
		{"humans against bots only", match("custom", 1, 2, 0), false, "2 v 0 humans"},
		{"bots only", match("custom", 1, 0, 0), false, "0 v 0 humans"},
		{"a practice game", match("practice", 1, 1, 0), false, "a practice game"},
		{"a practice group's game", match("practiceGroup", 1, 2, 0), false, "a practice group's game"},
		{"no winner", match("pvp", 0, 1, 1), false, "no winner"},
	} {
		v := Judge(c.m)
		if v.Rated != c.rated || v.Why != c.why {
			t.Errorf("%s: %+v, want rated %v (%s)", c.name, v, c.rated, c.why)
		}
	}
	// a spectator has no seat; a seat without an account or on another team isn't a human
	m := match("custom", 1, 1, 1)
	m.Seats = append(m.Seats, store.MatchSeat{Seat: 10, Team: 2}, store.MatchSeat{Seat: 11, Team: 3, UserID: 9, PersonaID: 9})
	if v := Judge(m); !v.Rated || v.Humans != [2]int{1, 1} {
		t.Errorf("odd seats counted: %+v", v)
	}
	if got := Judge(match("pvp", 1, 2, 3)).DescribeNotRated(); got != "not rated (2 v 3 humans)" {
		t.Errorf("the log text: %q", got)
	}
}

// One rated match: every human against the other team's humans as one composite opponent, with the team's result (a
// human who left keeps it); equal ratings move by the same amount up and down; a match that isn't rated changes no one.
func TestRate(t *testing.T) {
	ratings := map[int64]store.Rating{
		1: {Value: 1600, RD: 80, Vol: 0.06}, 2: {Value: 1400, RD: 120, Vol: 0.06},
		101: {Value: 1550, RD: 60, Vol: 0.06}, // 102 has none: 1500, RD 350
	}
	current := func(id int64) store.Rating { return ratings[id] }
	m := match("pvp", 2, 2, 2)
	m.Seats[0].Left = true // account 1 left during the match: it still loses with its team
	m.Seats[0].Won = true  // even if its seat says otherwise
	v, changes := Rate(m, current)
	if !v.Rated || len(changes) != 4 {
		t.Fatalf("%+v, %d changes", v, len(changes))
	}
	opp1 := Composite([]store.Rating{ratings[101], {}})
	opp2 := Composite([]store.Rating{ratings[1], ratings[2]})
	for _, c := range changes {
		var want store.Rating
		switch c.Team {
		case 1:
			want = Update(current(c.UserID), []Result{{opp1.Rating, opp1.RD, 0}})
		case 2:
			want = Update(current(c.UserID), []Result{{opp2.Rating, opp2.RD, 1}})
		}
		if c.Won != (c.Team == 2) || c.After != want || c.Before != current(c.UserID).OrStart() {
			t.Errorf("account %d: %+v, want after %+v", c.UserID, c, want)
		}
		if (c.Team == 2) != (c.After.Value > c.Before.Value) {
			t.Errorf("account %d (team %d) moved the wrong way: %+v", c.UserID, c.Team, c)
		}
	}
	if !strings.HasSuffix(changes[0].Describe(v), " (rated: 2 v 2 humans)") ||
		!strings.HasPrefix(changes[0].Describe(v), "1600 -> ") {
		t.Errorf("the log text: %q", changes[0].Describe(v))
	}
	// two new players, 1 v 1: the winner gains what the loser loses
	_, cs := Rate(match("custom", 1, 1, 1), func(int64) store.Rating { return store.Rating{} })
	if len(cs) != 2 || !near(cs[0].After.Value-1500, 1500-cs[1].After.Value, 1e-9) || cs[0].After.Value <= 1500 ||
		cs[0].After.RD != cs[1].After.RD {
		t.Errorf("1 v 1 of new players: %+v", cs)
	}
	if v, cs := Rate(match("pvp", 1, 2, 3), current); v.Rated || cs != nil {
		t.Errorf("2 v 3 rated: %+v %+v", v, cs)
	}
}

// The seed replays the stored matches once, oldest first, by Rate's rule: the accounts get the replay's ratings, rated
// games and wins, the kept days their rated parts; the mark is set and a second seed does nothing; an account a match
// names that no longer exists is rated in the replay only.
func TestSeed(t *testing.T) {
	st := store.New()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	var users [3]int64
	var personas [3]int64
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
	ghost := store.MatchSeat{Seat: 2, Team: 2, UserID: 999, PersonaID: 3999} // a deleted account
	matches := []store.Match{
		{Kind: "pvp", Winner: 1, Ended: now.AddDate(0, 0, -20), Seats: []store.MatchSeat{seat(0, 1), seat(1, 2)}},
		{Kind: "practice", Winner: 1, Ended: now.AddDate(0, 0, -19), Seats: []store.MatchSeat{seat(0, 1)}},
		{Kind: "custom", Winner: 2, Ended: now.AddDate(0, 0, -1), Seats: []store.MatchSeat{seat(0, 1), seat(1, 2)}},
		{Kind: "pvp", Winner: 2, Ended: now.Add(-2 * time.Hour), Seats: []store.MatchSeat{seat(0, 1), ghost}},
		{Kind: "pvp", Winner: 1, Ended: now.Add(-time.Hour), Seats: []store.MatchSeat{seat(0, 1), seat(1, 1), seat(2, 2)}},
	}
	for _, m := range matches {
		m.Mode, m.Started = m.Kind, m.Ended.Add(-20*time.Minute)
		if _, err := st.InsertMatch(m); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Seed(st, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Already || rep.Matches != 5 || rep.Rated != 3 || rep.Accounts != 2 || rep.Missing != 1 ||
		!strings.HasPrefix(rep.String(), "5 stored matches replayed, 3 rated, 2 accounts rated, 1 no longer exist, at ") {
		t.Errorf("report %+v: %s", rep, rep)
	}
	// the replay by hand
	cur := map[int64]store.Rating{}
	get := func(id int64) store.Rating { return cur[id] }
	for _, i := range []int{0, 2, 3} {
		_, cs := Rate(matches[i], get)
		for _, c := range cs {
			cur[c.UserID] = c.After
		}
	}
	a, _ := st.Account(users[0])
	b, _ := st.Account(users[1])
	c, _ := st.Account(users[2])
	ra, rb := a.History.Rating, b.History.Rating
	if !near(ra.Value, cur[users[0]].Value, 1e-9) || !near(ra.RD, cur[users[0]].RD, 1e-9) || ra.Games != 3 || ra.Wins != 1 {
		t.Errorf("a: %+v, want %+v after 3 games, 1 win", ra, cur[users[0]])
	}
	if !near(rb.Value, cur[users[1]].Value, 1e-9) || rb.Games != 2 || rb.Wins != 1 {
		t.Errorf("b: %+v, want %+v after 2 games, 1 win", rb, cur[users[1]])
	}
	if c.History.Rating != (store.Rating{}) {
		t.Errorf("c played no rated game (1 v 2): %+v", c.History.Rating)
	}
	// the days kept: yesterday's custom game and today's ghost game for a (the game 20 days ago is out of them)
	if len(a.History.Days) != 2 || a.History.Days[0].Rated != (store.GamesWon{Games: 1}) ||
		a.History.Days[1].Rated != (store.GamesWon{Games: 1}) {
		t.Fatalf("a's days: %+v", a.History.Days)
	}
	_, first := Rate(matches[0], func(int64) store.Rating { return store.Rating{} })
	if !near(a.History.Days[0].RatingBefore, first[0].After.Value, 1e-9) {
		t.Errorf("a's rating before yesterday: %v, want %v", a.History.Days[0].RatingBefore, first[0].After.Value)
	}
	if v, _, found, _ := st.Setting(SeedKey); !found || v != rep.Mark {
		t.Errorf("the mark: %q %v", v, found)
	}
	// once only: a new match and a second seed change nothing
	if _, err := st.InsertMatch(store.Match{Kind: "pvp", Mode: "pvp", Winner: 2, Started: now, Ended: now,
		Seats: []store.MatchSeat{seat(0, 1), seat(1, 2)}}); err != nil {
		t.Fatal(err)
	}
	again, err := Seed(st, now.Add(time.Hour))
	if err != nil || !again.Already || again.Mark != rep.Mark || !strings.HasPrefix(again.String(), "seeded before (5 stored") {
		t.Errorf("second seed: %+v %v", again, err)
	}
	if a2, _ := st.Account(users[0]); a2.History.Rating != ra {
		t.Errorf("the second seed changed a: %+v", a2.History.Rating)
	}
}

// An empty history: the mark is set, nobody changes.
func TestSeedEmpty(t *testing.T) {
	st := store.New()
	rep, err := Seed(st, time.Unix(1700000000, 0))
	if err != nil || rep.Matches != 0 || rep.Accounts != 0 ||
		!strings.HasPrefix(rep.Mark, "0 stored matches replayed, 0 rated, 0 accounts rated, at ") {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, _, found, _ := st.Setting(SeedKey); !found {
		t.Error("no mark")
	}
}
