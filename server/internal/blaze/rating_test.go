package blaze

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/rating"
	"dawngate/server/internal/rules"
	"dawngate/server/internal/store"
)

// ratingGame is resultGame's game on a server with a store, each player's account made (alpha@example.com, ...).
func ratingGame(t *testing.T, mode, flags string, custom bool, players []resultSeat) (*hostedGame, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	s := NewServer(logging.New(&buf), "127.0.0.1", 17502)
	s.Now = func() time.Time { return resultAt }
	hg := resultGame(mode, flags, 0, custom, players)
	hg.s = s
	for _, hp := range hg.players {
		a, _, err := s.Store.Login(hp.p.Name+"@example.com", resultAt)
		if err != nil {
			t.Fatal(err)
		}
		hp.userID, hp.p.ID = a.UserID, a.Personas[0].ID
	}
	return hg, &buf
}

// A finished custom game's rating: the humans at the start are the history's seats (a player who left the lobby before
// the start has none; one who left during the match keeps its seat and its team's win), 1 v 1 here, so both are rated
// against each other in one transaction, with a log line each; the next game starts from the stored ratings.
func TestRateHostedGame(t *testing.T) {
	hg, buf := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{
		{name: "alpha", team: 0}, {name: "bravo", team: 1}, {name: "carl", team: 1},
	})
	hg.inGameAt = resultAt.Add(-20 * time.Minute)
	alpha, bravo, carl := hg.players[0], hg.players[1], hg.players[2]
	alpha.atStart, bravo.atStart = true, true
	bravo.removed = true // left during the match
	carl.removed = true  // left the lobby before the start (atStart false)
	f := gmshost.FinishGame{WinnerTeam: 2, DurationMs: 1200000, Reason: "guardian"}
	rec := hg.historyMatch(f)
	if len(rec.Seats) != 2 || rec.Seats[1].PersonaID != bravo.p.ID || !rec.Seats[1].Left {
		t.Fatalf("seats %+v", rec.Seats)
	}
	hg.rate(rec)
	a, _ := hg.s.Store.Account(alpha.userID)
	b, _ := hg.s.Store.Account(bravo.userID)
	c, _ := hg.s.Store.Account(carl.userID)
	_, want := rating.Rate(rec.Match, func(int64, string) store.Rating { return store.Rating{} })
	if ra := a.History.Rating; ra.Value != want[0].After.Value || ra.Games != 1 || ra.Wins != 0 || ra.Value >= 1500 {
		t.Errorf("alpha (lost): %+v", ra)
	}
	if rb := b.History.Rating; rb.Value != want[1].After.Value || rb.Games != 1 || rb.Wins != 1 || rb.Value <= 1500 {
		t.Errorf("bravo (won, left during the match): %+v", rb)
	}
	if c.History.Rating != (store.Rating{}) {
		t.Errorf("carl (left the lobby) rated: %+v", c.History.Rating)
	}
	if d := b.History.Days; len(d) != 1 || d[0].Rated != (store.GamesWon{Games: 1, Wins: 1}) || d[0].RatingBefore != 1500 {
		t.Errorf("bravo's day: %+v", d)
	}
	for _, line := range []string{
		"account 1000000001: game 7 rating: 1500 -> 1338 (rated: 1 v 1 humans)\n",
		"account 1000000002: game 7 rating: 1500 -> 1662 (rated: 1 v 1 humans)\n",
	} {
		if !strings.Contains(buf.String(), line) {
			t.Errorf("log lacks %q:\n%s", line, buf)
		}
	}
	if strings.Contains(buf.String(), "account 1000000003") {
		t.Errorf("carl in the log:\n%s", buf)
	}
	// the next game from the stored ratings: alpha against bravo again, alpha wins
	hg.game.ID = 8
	hg.rate(historyRecord{Match: store.Match{Kind: "custom", Winner: 1, Seats: []store.MatchSeat{
		{Seat: 0, Team: 1, UserID: alpha.userID, PersonaID: alpha.p.ID}, {Seat: 5, Team: 2, UserID: bravo.userID, PersonaID: bravo.p.ID}}}})
	a2, _ := hg.s.Store.Account(alpha.userID)
	if a2.History.Rating.Games != 2 || a2.History.Rating.Value <= a.History.Rating.Value ||
		!strings.Contains(buf.String(), "account 1000000001: game 8 rating: 1338 -> ") {
		t.Errorf("the second game: %+v\n%s", a2.History.Rating, buf)
	}
}

// Games that aren't rated change no rating and say why, one line per human: uneven teams (2 v 1 with a bot), a
// practice group's game, a game without a winner.
func TestRateHostedGameNotRated(t *testing.T) {
	for _, c := range []struct {
		mode, flags string
		custom      bool
		winner      int
		want        string
	}{
		{gmshost.ModePvP, "1", false, 1, "rating: not rated (2 v 1 humans)\n"},
		{gmshost.ModeCustom, "1042", true, 1, "rating: not rated (a practice group's game)\n"},
		{gmshost.ModePvP, "1", false, 0, "rating: not rated (no winner)\n"},
	} {
		hg, buf := ratingGame(t, c.mode, c.flags, c.custom, []resultSeat{
			{name: "alpha", team: 0}, {name: "bravo", team: 0}, {name: "carl", team: 1}})
		hg.rate(hg.historyMatch(gmshost.FinishGame{WinnerTeam: c.winner, DurationMs: 1000}))
		if n := strings.Count(buf.String(), c.want); n != 3 {
			t.Errorf("%s %s: %d lines %q:\n%s", c.mode, c.flags, n, c.want, buf)
		}
		for _, hp := range hg.players {
			if a, _ := hg.s.Store.Account(hp.userID); a.History.Rating != (store.Rating{}) {
				t.Errorf("%s: %s rated %+v", c.mode, hp.p.Name, a.History.Rating)
			}
		}
	}
}

// A custom game its admin made unrated (M30, MMR1): the history's row says so, no rating of any type moves and the
// Leaderboard doesn't count it, with or without rules; a draft game (exactly the preset) rates in the draft type.
func TestRateHostedGameAdminUnrated(t *testing.T) {
	draft, err := rules.Parse("draft")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		rules   rules.Combination
		unrated bool
		want    string // the log line's end
		mode    string // the type rated ("" none)
	}{
		{rules.Standard(), true, "rating: not rated (unrated by the admin)\n", ""},
		{draft, true, "rating: not rated (unrated by the admin)\n", ""},
		{draft, false, " (rated: 1 v 1 humans)\n", rating.Draft},
	} {
		hg, buf := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{{name: "alpha", team: 0}, {name: "bravo", team: 1}})
		hg.custom.rules, hg.custom.unrated = c.rules, c.unrated
		hg.inGameAt = resultAt.Add(-20 * time.Minute)
		for _, hp := range hg.players {
			hp.atStart = true
		}
		f := gmshost.FinishGame{WinnerTeam: 2, DurationMs: 1200000, Reason: "guardian"}
		hg.persist(nil, f)
		rec := hg.historyMatch(f)
		if rec.Unrated != c.unrated {
			t.Errorf("%v: the history's unrated %v", c.rules, rec.Unrated)
		}
		hg.rate(rec)
		if n := strings.Count(buf.String(), c.want); n != 2 {
			t.Errorf("%v: %d lines %q:\n%s", c.rules, n, c.want, buf)
		}
		if c.unrated && strings.Count(buf.String(), "leaderboard: not counted (a custom game unrated by the admin)") != 2 {
			t.Errorf("%v: the Leaderboard's line:\n%s", c.rules, buf)
		}
		if c.mode == rating.Draft && strings.Count(buf.String(), "game 7 draft rating: 1500 -> ") != 2 {
			t.Errorf("the draft rating's line:\n%s", buf)
		}
		for _, hp := range hg.players {
			a, _ := hg.s.Store.Account(hp.userID)
			for _, mode := range rating.Modes() {
				if r := a.History.RatingIn(mode); (r.Games == 1) != (mode == c.mode) {
					t.Errorf("%v: %s's %s rating %+v", c.rules, hp.p.Name, mode, r)
				}
			}
			if a.History.Ranked.Games != 0 {
				t.Errorf("%v: %s counted on the Leaderboard", c.rules, hp.p.Name)
			}
		}
	}
}

// seedRating gives a pipe session's account its rating, rated games and wins, and today's rated part (the rating before
// today's first rated game).
func seedRating(t *testing.T, s *Server, email string, r store.Rating, today *store.HistoryDay, shaper uint32) {
	t.Helper()
	a, _, err := s.Store.Login(email, time.Unix(testTime, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Update(a.UserID, func(a *store.Account) {
		a.History.Rating = r
		a.History.Ranked = store.GamesWon{Games: 99, Wins: 99} // the wins ranking's counts: not read by the rating's
		if shaper != 0 {
			a.History.Shapers = map[uint32]store.GamesWon{shaper: {Games: r.Games}}
		}
		if today != nil {
			d := *today
			d.Day = time.Unix(testTime, 0).UTC().Format("2006-01-02")
			a.History.Days = []store.HistoryDay{d}
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// The Leaderboard by rating (the default, --rating-leaderboard on): 'Skill' ranks the accounts with at least
// LeaderboardMinGames rated games by rating, STAT the rating rounded, the rated games and wins and the shaper;
// 'SkillDaily' the ranking before today's rated games (second's three wins today were what put it first). With
// show_rating on (the default) the 'Skill' names carry RatingMark (RATINGSHOW2's column), the daily ones don't.
func TestRatingLeaderboardSession(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) {
		if !s.RatingLeaderboard {
			t.Error("NewServer: the rating ranking isn't the default")
		}
		seedRating(t, s, "player@example.com", store.Rating{Value: 1611.6, RD: 90, Vol: 0.06, Games: 12, Wins: 7}, nil, 1002)
		seedRating(t, s, "second@example.com", store.Rating{Value: 1688.2, RD: 80, Vol: 0.06, Games: 13, Wins: 9},
			&store.HistoryDay{Rated: store.GamesWon{Games: 3, Wins: 3}, RatingBefore: 1590.4}, 1001)
		seedRating(t, s, "third@example.com", store.Rating{Value: 1900, RD: 200, Vol: 0.06, Games: 9, Wins: 9}, nil, 0)
	})
	ps.loginAs("player@example.com", 1)
	ps.send(ComponentStats, CmdStatsGetLeaderboard, 10, ps.request("getleaderboard_request"))
	ps.expectPayload(ComponentStats, CmdStatsGetLeaderboard, fire.Reply, 10, LeaderboardReply([]store.LeaderRow{
		{PersonaID: store.FirstPersonaID + 1, Name: "second" + RatingMark(1688), Games: 13, Wins: 9, Shaper: 1001, Skill: 1688},
		{PersonaID: store.FirstPersonaID, Name: "player" + RatingMark(1612), Games: 12, Wins: 7, Shaper: 1002, Skill: 1612}}, 1))
	ps.send(ComponentStats, CmdStatsGetLeaderboard, 11, ps.request("getleaderboard_daily_request"))
	ps.expectPayload(ComponentStats, CmdStatsGetLeaderboard, fire.Reply, 11, LeaderboardReply([]store.LeaderRow{
		{PersonaID: store.FirstPersonaID, Name: "player", Games: 12, Wins: 7, Shaper: 1002, Skill: 1612},
		{PersonaID: store.FirstPersonaID + 1, Name: "second", Games: 10, Wins: 6, Shaper: 1001, Skill: 1590}}, 1))
	log := ps.close()
	for _, want := range []string{
		"[blaze]    leaderboard \"Skill\": 2 row(s) from rank 1 (by rating), 2 with the rating shown\n",
		"[blaze]    leaderboard \"SkillDaily\": 2 row(s) from rank 1 (by rating)\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}
