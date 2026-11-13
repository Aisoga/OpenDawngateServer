package blaze

import (
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/rules"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// seedModeRatings gives email's account these ratings per game type (rating.Modes; a zero Rating: none).
func seedModeRatings(t *testing.T, s *Server, email string, rs map[string]store.Rating) {
	t.Helper()
	a, _, err := s.Store.Login(email, time.Unix(testTime, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Update(a.UserID, func(a *store.Account) {
		for mode, r := range rs {
			a.History.SetRatingIn(mode, r)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// lbStateLine is leaderboard.state's line with the rating Leaderboard on.
func lbStateLine(board string) string {
	return `{"event":"leaderboard.state","data":{"board":"` + board + `","boards":["standard","draft","custom"]}}`
}

// getLeaderboard sends the client's two Leaderboard requests (the golden 'Skill' and 'SkillDaily') and returns both
// replies.
func (tc *tcpClient) getLeaderboard(g *golden.Data) (skill, daily fire.Frame) {
	tc.t.Helper()
	skill = tc.reply(ComponentStats, CmdStatsGetLeaderboard,
		tc.send(ComponentStats, CmdStatsGetLeaderboard, golden.Bytes(tc.t, decodeCase(tc.t, g, "getleaderboard_request"))))
	daily = tc.reply(ComponentStats, CmdStatsGetLeaderboard,
		tc.send(ComponentStats, CmdStatsGetLeaderboard, golden.Bytes(tc.t, decodeCase(tc.t, g, "getleaderboard_daily_request"))))
	return skill, daily
}

// MMR2: leaderboard.select picks the board a player's Leaderboard requests are answered from. Every connection's
// greeting names the board (standard at first) and the boards; a new board is kept per persona and every connection of
// the player hears it, the other players keep theirs; 'Skill' and 'SkillDaily' then rank by that type's rating (its
// rows, its rated games for the 10-game minimum and the marks); an unknown board, a malformed call and a change within
// the gap are refused with the caller's state; the same board again answers the state; the end of the last login
// forgets the board.
func TestLeaderboardSelect(t *testing.T) {
	r := func(v float64, games uint32) store.Rating {
		return store.Rating{Value: v, RD: 90, Vol: 0.06, Games: games, Wins: games / 2}
	}
	ts := newTCPServer(t, func(s *Server) {
		seedModeRatings(t, s, "alpha@example.com", map[string]store.Rating{
			store.ModeStandard: r(1611.6, 12), store.ModeDraft: r(1800, 10)})
		seedModeRatings(t, s, "bravo@example.com", map[string]store.Rating{
			store.ModeStandard: r(1388.2, 10), store.ModeDraft: r(1700, 9), store.ModeCustom: r(1450, 11)})
		seedModeRatings(t, s, "charlie@example.com", map[string]store.Rating{store.ModeCustom: r(1520, 20)})
	})
	addr := attachUI(t, ts)
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	ua, ua2, ub := uiConnect(t, addr, a), uiConnect(t, addr, a), uiConnect(t, addr, b)
	for _, u := range []*uiClient{ua, ua2, ub} {
		if u.lbGreeting != lbStateLine("standard") {
			t.Errorf("greeting %s", u.lbGreeting)
		}
	}
	rows := func(f fire.Frame) string {
		t.Helper()
		var out []string
		l, ok := decode(t, f).Get("LDLS")
		if !ok {
			t.Fatal("no LDLS")
		}
		for _, row := range l.Value.(*tdf.List).Items {
			name, _ := row.(*tdf.Struct).Str("ENAM")
			out = append(out, name)
		}
		return strings.Join(out, " ")
	}
	mark := func(name string, v int64) string { return name + RatingMark(v) }
	wantBoard := func(tc *tcpClient, skill, daily string) {
		t.Helper()
		s, d := tc.getLeaderboard(g)
		if got := rows(s); got != skill {
			t.Errorf("%s's 'Skill': %q, want %q", tc.name, got, skill)
		}
		if got := rows(d); got != daily {
			t.Errorf("%s's 'SkillDaily': %q, want %q", tc.name, got, daily)
		}
	}

	// standard at first: alpha 1612 (12 games), bravo 1388 (10); charlie has no standard games
	wantBoard(a, mark(a.name, 1612)+" "+mark(b.name, 1388), a.name+" "+b.name)

	// alpha picks draft: both of alpha's connections hear it, bravo nothing; alpha's requests get the draft ranking
	// (bravo's 9 draft games are under the minimum), bravo's still the standard
	ua.call(CallLeaderboardSelect, `{"board":"draft"}`)
	ua.want(lbStateLine("draft"))
	ua2.want(lbStateLine("draft"))
	ub.quiet()
	wantBoard(a, mark(a.name, 1800), a.name)
	wantBoard(b, mark(a.name, 1612)+" "+mark(b.name, 1388), a.name+" "+b.name)
	if log := ts.log.String(); !strings.Contains(log, "leaderboard: persona "+itoa(a.id)+"'s board draft (was standard)") ||
		!strings.Contains(log, `leaderboard "Skill": 1 row(s) from rank 1 (by draft rating), 1 with the rating shown`) {
		t.Errorf("log:\n%s", log)
	}

	// bravo picks custom: charlie 1520 (20), bravo 1450 (11)
	ub.call(CallLeaderboardSelect, `{"board":"custom"}`)
	ub.want(lbStateLine("custom"))
	wantBoard(b, mark("charlie", 1520)+" "+mark(b.name, 1450), "charlie "+b.name)

	// hostile and early calls: refused with the player's state (to each of its connections), the board kept
	ts.s.lbGap = time.Hour
	for _, bad := range []string{`{"board":"aram"}`, `{"board":"Draft"}`, `{"board":7}`, `{}`, `"custom"`, `{"board":"standard"}`} {
		ua.call(CallLeaderboardSelect, bad)
		ua.want(lbStateLine("draft"))
		ua2.want(lbStateLine("draft"))
	}
	// the same board again: the state, no change
	ua.call(CallLeaderboardSelect, `{"board":"draft"}`)
	ua.want(lbStateLine("draft"))
	ua2.want(lbStateLine("draft"))
	ub.quiet()
	ts.s.lbGap = 0
	ua.call(CallLeaderboardSelect, `{"board":"standard"}`)
	ua.want(lbStateLine("standard"))
	ua2.want(lbStateLine("standard"))
	wantBoard(a, mark(a.name, 1612)+" "+mark(b.name, 1388), a.name+" "+b.name)
	if ts.s.leaderboardBoard(b.id) != store.ModeCustom {
		t.Errorf("bravo's board %q", ts.s.leaderboardBoard(b.id))
	}

	// bravo's login ends: the board is forgotten; the next login starts on standard
	b.c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for ts.s.leaderboardBoard(b.id) != store.ModeStandard && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := ts.s.leaderboardBoard(b.id); got != store.ModeStandard {
		t.Errorf("after the logout bravo's board %q", got)
	}
	b2 := ts.client("bravo@example.com")
	if u := uiConnect(t, addr, b2); u.lbGreeting != lbStateLine("standard") {
		t.Errorf("the new login's greeting %s", u.lbGreeting)
	}
}

// With the wins ranking (--rating-leaderboard off) the state names no boards (the class draws no tabs) and every
// leaderboard.select is refused with it.
func TestLeaderboardSelectWinsRanking(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) { s.RatingLeaderboard = false })
	addr := attachUI(t, ts)
	a := ts.client("alpha@example.com")
	ua := uiConnect(t, addr, a)
	off := `{"event":"leaderboard.state","data":{"board":"standard"}}`
	if ua.lbGreeting != off {
		t.Errorf("greeting %s", ua.lbGreeting)
	}
	ua.call(CallLeaderboardSelect, `{"board":"draft"}`)
	ua.want(off)
	if ts.s.leaderboardBoard(a.id) != store.ModeStandard {
		t.Error("a board kept")
	}
}

// MMR2: the custom lobby's tiles (RATINGSHOW3) show the rating of the lobby's current type, read again when the
// admin's rules change the type: standard, then the draft preset, then ARAM (custom), then standard again; a player
// without enough rated games of a type shows none.
func TestLobbyTilesFollowType(t *testing.T) {
	r := func(v float64, games uint32) store.Rating {
		return store.Rating{Value: v, RD: 90, Vol: 0.06, Games: games}
	}
	ts, _, addr := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) {
		seedModeRatings(t, s, "alpha@example.com", map[string]store.Rating{
			store.ModeStandard: r(1611.6, 12), store.ModeDraft: r(1800, 10)})
		seedModeRatings(t, s, "bravo@example.com", map[string]store.Rating{
			store.ModeStandard: r(1388.2, 10), store.ModeCustom: r(1450, 11)})
	})
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	b.settle()
	ua := uiConnect(t, addr, a)
	ua.call(CallLobbyEnter, "")
	ua.want(catalogLine(t))
	std := rules.Standard()
	ua.want(stateWithRules(rulesPart(std, false, true, true), a.name+" - 1612", b.name+" - 1388"))

	draft, err := rules.Parse("draft")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		preset string
		rules  rules.Combination
		a, b   string
	}{
		{"draft", draft, a.name + " - 1800", b.name},  // bravo has no draft games
		{"aram", aram(t), a.name, b.name + " - 1450"}, // ARAM is the custom type; alpha has none
		{"standard", std, a.name + " - 1612", b.name + " - 1388"},
	} {
		ua.call(CallLobbySetRules, `{"preset":"`+c.preset+`"}`)
		if got := ua.event(EventLobbyState); got != stateWithRules(rulesPart(c.rules, false, true, true), c.a, c.b) {
			t.Errorf("%s: %s", c.preset, got)
		}
	}
}
