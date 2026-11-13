package blaze

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/rules"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// PROFILE3 (the user's rules of 2026-11-12/13): who leaves a game before its end lost it. In a game that still finishes
// by the Guardian's death or a surrender, the leaver's result is a loss (Profile, history, Leaderboard counts, its
// match-history seat) even when its team won; the rating keeps RATING1's team result. Before PROFILE3 bravo's
// PlayerStats said 1 win.
func TestLeaverOfFinishedGameLoses(t *testing.T) {
	hg, buf := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{
		{name: "alpha", team: 0, stats: kda("ember.Noun", 1, 4, 2)},
		{name: "bravo", team: 1, stats: kda("ember.Noun", 3, 0, 1)},
	})
	hg.s.Content = loadFixtureContent(t)
	hg.inGameAt = resultAt.Add(-20 * time.Minute)
	alpha, bravo := hg.players[0], hg.players[1]
	alpha.atStart, bravo.atStart = true, true
	bravo.removed = true // left during the match; its team (2) wins without it
	f := gmshost.FinishGame{WinnerTeam: 2, DurationMs: 1200000, Reason: "guardian"}
	hg.persist(nil, f)
	for _, hp := range []*hostedPlayer{alpha, bravo} {
		a, _ := hg.s.Store.Account(hp.userID)
		ps := a.Stats[progress.PlayerStats]
		if ps["PLAYED"] != "1" || ps["WINS"] != "0" || a.History.Ranked.Games != 1 || a.History.Ranked.Wins != 0 {
			t.Errorf("%s: PlayerStats %v, ranked %+v", hp.p.Name, ps, a.History.Ranked)
		}
		if g := totalShaperGames(a.History.Shapers); g.Games != 1 || g.Wins != 0 {
			t.Errorf("%s: shaper history %+v", hp.p.Name, a.History.Shapers)
		}
	}
	if !strings.Contains(buf.String(), "account 1000000002: game 7 left before its end: counted as a loss\n") ||
		strings.Contains(buf.String(), "account 1000000001: game 7 left") {
		t.Errorf("log:\n%s", buf)
	}
	rec := hg.historyMatch(f)
	if len(rec.Seats) != 2 || rec.Seats[1].PersonaID != bravo.p.ID || !rec.Seats[1].Left || rec.Seats[1].Won ||
		rec.Seats[0].Won || rec.Seats[0].Left {
		t.Fatalf("seats %+v", rec.Seats)
	}
	// the rating scores the team's result (RATING1's leaver rule): bravo is rated as a winner
	hg.rate(rec)
	b, _ := hg.s.Store.Account(bravo.userID)
	if r := b.History.Rating; r.Games != 1 || r.Wins != 1 || r.Value <= 1500 {
		t.Errorf("bravo's rating %+v", r)
	}
}

// A finished game that ended neither by the Guardian nor by a surrender and ran under LeftLossAfter leaves its leaver
// the team's result (as before PROFILE3); one that ran LeftLossAfter makes it a loss. A kicked player isn't a leaver.
func TestLeaverOfFinishedGameRules(t *testing.T) {
	for _, c := range []struct {
		name   string
		f      gmshost.FinishGame
		kicked bool
		wins   string // bravo's WINS after (its team won)
	}{
		{"short, another reason", gmshost.FinishGame{WinnerTeam: 2, DurationMs: 600000, Reason: "other"}, false, "1"},
		{"15 minutes, another reason", gmshost.FinishGame{WinnerTeam: 2, DurationMs: 900000, Reason: "other"}, false, "0"},
		{"surrender, short", gmshost.FinishGame{WinnerTeam: 2, DurationMs: 60000, Reason: "surrender"}, false, "0"},
		{"kicked", gmshost.FinishGame{WinnerTeam: 2, DurationMs: 1200000, Reason: "guardian"}, true, "1"},
	} {
		hg, _ := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{{name: "alpha", team: 0}, {name: "bravo", team: 1}})
		hg.s.Content = loadFixtureContent(t)
		bravo := hg.players[1]
		bravo.removed = true
		if c.kicked {
			hg.custom.kicked = map[int64]bool{bravo.p.ID: true}
		}
		hg.persist(nil, c.f)
		b, _ := hg.s.Store.Account(bravo.userID)
		rec := hg.historyMatch(c.f)
		if w := b.Stats[progress.PlayerStats]["WINS"]; w != c.wins || rec.Seats[1].Won != (c.wins == "1") {
			t.Errorf("%s: bravo's WINS %s, seat %+v", c.name, w, rec.Seats[1])
		}
	}
}

// totalShaperGames sums a history's games and wins over its shapers.
func totalShaperGames(m map[uint32]store.GamesWon) (out store.GamesWon) {
	for _, g := range m {
		out.Games += g.Games
		out.Wins += g.Wins
	}
	return out
}

// A game whose players all left never finishes; after LeftLossAfter (15 minutes) in game, and when they didn't all
// leave within a minute, the host's end counts it as a loss for each human who was in it when it went IN_GAME and
// joined the GMS: PlayerStats and the Leaderboard's counts, no rewards and no XP; the match is stored
// with Winner 0 and every human seat Left and lost. A spectator, a player who left the lobby before the start and one
// who never joined get nothing. Before PROFILE3 nothing at all was stored (MHIST1's "an aborted match stores
// nothing"), which is how the user's own games left early never reached the Profile.
func TestLeftGameCountsAsLoss(t *testing.T) {
	hg, buf := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{
		{name: "alpha", team: 0}, {name: "bravo", team: 1}, {name: "carl", team: SpectatorTeam}, {name: "dora", team: 1},
	})
	hg.s.Content = loadFixtureContent(t)
	hg.s.Now = func() time.Time { return resultAt }
	hg.inGameAt = resultAt.Add(-20 * time.Minute)
	alpha, bravo, carl, dora := hg.players[0], hg.players[1], hg.players[2], hg.players[3]
	alpha.atStart, bravo.atStart, carl.atStart = true, true, true
	for _, hp := range hg.players {
		hp.removed, hp.leftAt = true, resultAt
	}
	alpha.leftAt = resultAt.Add(-3 * time.Minute) // alpha left first, bravo 3 minutes later: no restart
	hg.abandoned = true
	hg.countLeftGame(gmshost.Outcome{Ready: true, Reason: "abortGame: no players left"})
	for _, hp := range []*hostedPlayer{alpha, bravo} {
		a, _ := hg.s.Store.Account(hp.userID)
		ps := a.Stats[progress.PlayerStats]
		if ps["PLAYED"] != "1" || ps["WINS"] != "0" || a.XP != 0 || a.EC != 0 || a.History.Ranked.Games != 1 {
			t.Errorf("%s: PlayerStats %v, XP %d, EC %d, ranked %+v", hp.p.Name, ps, a.XP, a.EC, a.History.Ranked)
		}
	}
	for _, hp := range []*hostedPlayer{carl, dora} {
		a, _ := hg.s.Store.Account(hp.userID)
		if len(a.Stats[progress.PlayerStats]) != 0 || a.History.Ranked.Games != 0 {
			t.Errorf("%s counted: %v", hp.p.Name, a.Stats)
		}
	}
	ms, err := hg.s.Store.RecentMatches(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("stored %+v", ms)
	}
	m := ms[0]
	if m.Winner != 0 || m.Reason != LeftGameReason || m.DurationMs != 1200000 || m.Kind != "custom" || len(m.Seats) != 2 {
		t.Fatalf("match %+v", m)
	}
	for _, st := range m.Seats {
		if st.Won || !st.Left || (st.PersonaID != alpha.p.ID && st.PersonaID != bravo.p.ID) {
			t.Errorf("seat %+v", st)
		}
	}
	for _, line := range []string{
		"game 7: its players left it before its end (after 1200.0 s in game): a loss for 2 player(s)\n",
		"account 1000000001: game 7 left before its end: lost, no rewards, no XP; 0 wins\n",
		"account 1000000002: game 7 history: no shaper (no noun in playerFinish), no role\n",
		"account 1000000002: game 7 leaderboard: counted (custom game); 1 games, 0 wins\n",
		"game 7: stored as match 1 (2 players, 0 bots; seatStats from the host 0, timeline false)\n",
	} {
		if !strings.Contains(buf.String(), line) {
			t.Errorf("log lacks %q:\n%s", line, buf)
		}
	}
	// the Profile's game types count it as All Modes does (PROFILE2's numbers from the stored matches)
	acct, _ := hg.s.Store.Account(alpha.userID)
	pm, games, err := hg.s.profileIn(alpha.p.ID, acct, "standard")
	if err != nil || games != 1 || pm.Stats[progress.PlayerStats]["PLAYED"] != "1" || pm.Stats[progress.PlayerStats]["WINS"] != "0" {
		t.Errorf("standard tab: %d games, %v, %v", games, pm.Stats[progress.PlayerStats], err)
	}
}

// Aborted games nobody left by choice count for nobody: the admin page ended it, the game never went IN_GAME (a
// lobby, loading, shaper select), the host gave up, it was never ready, or the abort wasn't the players' leave.
func TestLeftGameNotCounted(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(hg *hostedGame, o *gmshost.Outcome)
	}{
		{"the admin ended it", func(hg *hostedGame, o *gmshost.Outcome) { hg.endedByAdmin = true }},
		{"never IN_GAME", func(hg *hostedGame, o *gmshost.Outcome) { hg.inGameAt = time.Time{} }},
		{"the host gave up", func(hg *hostedGame, o *gmshost.Outcome) { o.Error = &gmshost.GMSError{Code: 1} }},
		{"never ready", func(hg *hostedGame, o *gmshost.Outcome) { o.Ready = false }},
		{"not left", func(hg *hostedGame, o *gmshost.Outcome) { hg.abandoned = false }},
	} {
		hg, _ := ratingGame(t, gmshost.ModePractice, "1292", false, []resultSeat{{name: "alpha", team: 0}})
		hg.inGameAt = resultAt.Add(-20 * time.Minute)
		hg.players[0].atStart, hg.players[0].removed, hg.abandoned = true, true, true
		o := gmshost.Outcome{Ready: true}
		c.set(hg, &o)
		hg.countLeftGame(o)
		a, _ := hg.s.Store.Account(hg.players[0].userID)
		ms, _ := hg.s.Store.RecentMatches(0, 10)
		if len(a.Stats[progress.PlayerStats]) != 0 || len(ms) != 0 {
			t.Errorf("%s: counted %v, stored %d", c.name, a.Stats, len(ms))
		}
	}
}

// A left custom game its admin made Unrated is stored (as Unrated, so the lists and the Profile's tabs leave it out)
// but adds nothing to the Profile, as a finished one (PROFILE2's inProfile); a practice game left counts in the
// Profile but not in the Leaderboard's counts, and sets no FTUE flag.
func TestLeftGameUnratedAndPractice(t *testing.T) {
	hg, _ := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{{name: "alpha", team: 0}})
	hg.custom.rules, hg.custom.unrated = rules.Standard(), true
	hg.inGameAt = resultAt.Add(-20 * time.Minute)
	hg.players[0].atStart, hg.players[0].removed, hg.abandoned = true, true, true
	hg.countLeftGame(gmshost.Outcome{Ready: true})
	a, _ := hg.s.Store.Account(hg.players[0].userID)
	ms, _ := hg.s.Store.RecentMatches(0, 10)
	if len(a.Stats[progress.PlayerStats]) != 0 || a.History.Ranked.Games != 0 || len(ms) != 1 || !ms[0].Unrated {
		t.Errorf("unrated: %v, ranked %+v, stored %+v", a.Stats, a.History.Ranked, ms)
	}

	hg, _ = ratingGame(t, gmshost.ModePractice, "1292", false, []resultSeat{{name: "alpha", team: 0}})
	hg.inGameAt = resultAt.Add(-20 * time.Minute)
	hg.players[0].atStart, hg.players[0].removed, hg.abandoned = true, true, true
	hg.countLeftGame(gmshost.Outcome{Ready: true})
	a, _ = hg.s.Store.Account(hg.players[0].userID)
	ms, _ = hg.s.Store.RecentMatches(0, 10)
	if a.Stats[progress.PlayerStats]["PLAYED"] != "1" || a.History.Ranked.Games != 0 || a.Flags != 0 || len(ms) != 1 ||
		ms[0].Kind != "practice" {
		t.Errorf("practice: %v, ranked %+v, flags %d, stored %+v", a.Stats, a.History.Ranked, a.Flags, ms)
	}
}

// End to end (the user's games of 2026-11-12 on the live server): a practice game alone, the match running (the host's
// phase inGame), the player leaves. Before PROFILE3 the host's abort stored nothing and the Profile stayed as it was;
// now the game is a loss in PlayerStats and a stored match, and still no FTUE flag.
func TestHostedGameLeftCountsAsLoss(t *testing.T) {
	var later atomic.Int64
	hs := newHostedSessionFull(t, "", "", nil, func(s *Server) {
		// the session's clock, moved on by later (the goldens before the match carry its times)
		now := s.Now
		s.Now = func() time.Time { return now().Add(time.Duration(later.Load())) }
		s.LeftLossAfter = time.Minute
	},
		func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
			h.Send(gmshost.AddPlayer{User: store.FirstPersonaID})
			h.Send(gmshost.GMSUpdate{Phase: gmshost.PhaseInGame})
			return receive(hs, h)
		})
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	hs.hostGot()
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
	hs.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(1, GameStateInGame))
	later.Store(int64(2 * time.Minute)) // the player leaves two minutes into the match
	hs.send(4, 0x0B, 3, tdf.NewWriter().Int("GID", 1).Int("PID", store.FirstPersonaID).Int("REAS", 6).Bytes())
	hs.expect(4, 0x0B, fire.Reply, 3, "")
	hs.expect(4, NotifyPlayerRemovedID, fire.Notification, 0, "notifyplayerremoved_playerleft_notification")
	hs.hostGot() // removePlayer
	if m := hs.hostGot(); m != (gmshost.AbortGame{Reason: "no players left"}) {
		t.Errorf("host got %#v", m)
	}
	hs.waitIdle()
	acct, _ := hs.s.Store.Account(store.FirstUserID)
	if ps := acct.Stats[progress.PlayerStats]; ps["PLAYED"] != "1" || ps["WINS"] != "0" || acct.Flags&store.FlagFTUEDone != 0 ||
		acct.XP != 0 {
		t.Errorf("account after the left game: %v, flags %d, XP %d", ps, acct.Flags, acct.XP)
	}
	ms, err := hs.s.Store.RecentMatches(0, 10)
	if err != nil || len(ms) != 1 || ms[0].Kind != "practice" || ms[0].Winner != 0 || ms[0].Reason != LeftGameReason ||
		len(ms[0].Seats) != 1 || !ms[0].Seats[0].Left || ms[0].Seats[0].Won {
		t.Errorf("stored %+v, %v", ms, err)
	}
	hs.close()
	for _, want := range []string{
		"[blaze]    game 1: its players left it before its end (after ",
		"[blaze]    account 1000000001: game 1 left before its end: lost, no rewards, no XP; 0 wins",
		"[blaze]    account 1000000001: game 1 leaderboard: not counted (a practice game); 0 games, 0 wins",
		"[blaze]    game 1: stored as match 1 (1 players, 0 bots; seatStats from the host 0, timeline false)",
	} {
		if log := hs.log.String(); !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// Left before LeftLossAfter, or all within a minute of each other (a restart): counted for nobody, nothing stored, a log
// line that says why. A solo game left after LeftLossAfter counts (TestLeftGameUnratedAndPractice).
func TestLeftGameShortOrRestart(t *testing.T) {
	for _, c := range []struct {
		name    string
		inGame  time.Duration
		spread  time.Duration // the second player's leave after the first's
		players int
		log     string
		counted bool
	}{
		{"14 minutes alone", 14 * time.Minute, 0, 1, "game 7: its players left it after 840.0 s in game, under 15m0s: counted for nobody (PROFILE3)\n", false},
		{"a restart after 20 minutes", 20 * time.Minute, 50 * time.Second, 2, "game 7: its 2 players all left within 50.0 s of each other (a restart): counted for nobody (PROFILE3)\n", false},
		{"two leaves 61 s apart", 20 * time.Minute, 61 * time.Second, 2, "a loss for 2 player(s)", true},
		{"15 minutes alone", 15 * time.Minute, 0, 1, "a loss for 1 player(s)", true},
	} {
		seats := []resultSeat{{name: "alpha", team: 0}, {name: "bravo", team: 1}}[:c.players]
		hg, buf := ratingGame(t, gmshost.ModeCustom, "2", true, seats)
		hg.inGameAt = resultAt.Add(-c.inGame)
		for i, hp := range hg.players {
			hp.atStart, hp.removed, hp.leftAt = true, true, resultAt.Add(-c.spread+time.Duration(i)*c.spread)
		}
		hg.abandoned = true
		hg.countLeftGame(gmshost.Outcome{Ready: true})
		a, _ := hg.s.Store.Account(hg.players[0].userID)
		ms, _ := hg.s.Store.RecentMatches(0, 10)
		if counted := a.Stats[progress.PlayerStats]["PLAYED"] == "1" && len(ms) == 1; counted != c.counted ||
			!strings.Contains(buf.String(), c.log) {
			t.Errorf("%s: counted %v (%v, %d stored), log:\n%s", c.name, counted, a.Stats, len(ms), buf)
		}
	}
}

// The end to end case with the default 15 minutes: the player leaves at once, so nothing counts (as before PROFILE3).
func TestHostedGameLeftEarlyNotCounted(t *testing.T) {
	hs := newHostedSession(t, nil, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		h.Send(gmshost.AddPlayer{User: store.FirstPersonaID})
		h.Send(gmshost.GMSUpdate{Phase: gmshost.PhaseInGame})
		return receive(hs, h)
	})
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	hs.hostGot()
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
	hs.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(1, GameStateInGame))
	hs.send(4, 0x0B, 3, tdf.NewWriter().Int("GID", 1).Int("PID", store.FirstPersonaID).Int("REAS", 6).Bytes())
	hs.expect(4, 0x0B, fire.Reply, 3, "")
	hs.expect(4, NotifyPlayerRemovedID, fire.Notification, 0, "notifyplayerremoved_playerleft_notification")
	hs.hostGot() // removePlayer
	hs.hostGot() // abortGame
	hs.waitIdle()
	acct, _ := hs.s.Store.Account(store.FirstUserID)
	ms, _ := hs.s.Store.RecentMatches(0, 10)
	hs.close()
	if len(acct.Stats[progress.PlayerStats]) != 0 || len(ms) != 0 ||
		!strings.Contains(hs.log.String(), "under 15m0s: counted for nobody (PROFILE3)") {
		t.Errorf("counted %v, stored %d, log:\n%s", acct.Stats, len(ms), hs.log.String())
	}
}
