package blaze

import (
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/store"
)

// SPECSTATS: a finished custom game with spectators. The match is stored with the two players' seats only (no seat, no
// match of its own, no profile stats for a spectator); the spectators who watched it to the end get it as their watched
// match (the review's MATCH STATS tab), the one who left during the match and the one whose host never reported it
// don't, nor do the players. A later match the spectator watches replaces it; an older id never steps back.
func TestSpectatorWatchedMatch(t *testing.T) {
	hg, buf := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{
		{name: "alpha", team: 0}, {name: "bravo", team: 1},
		{name: "watcher", team: SpectatorTeam}, {name: "leaver", team: SpectatorTeam}, {name: "ghost", team: SpectatorTeam, away: true},
	})
	hg.inGameAt = resultAt.Add(-20 * time.Minute)
	for _, hp := range hg.players {
		hp.atStart = true
	}
	alpha, bravo, watcher, leaver, ghost := hg.players[0], hg.players[1], hg.players[2], hg.players[3], hg.players[4]
	leaver.removed = true // left during the match
	s := hg.s
	// The spectator's own earlier match (played): its newest own match before the one it watches.
	own, err := s.Store.InsertMatch(store.Match{Kind: "practice", Mode: gmshost.ModePractice, Started: resultAt.Add(-time.Hour),
		Ended: resultAt.Add(-30 * time.Minute), Seats: []store.MatchSeat{{Seat: 0, PersonaID: watcher.p.ID, UserID: watcher.userID, Team: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	f := gmshost.FinishGame{WinnerTeam: 1, DurationMs: 600000, Reason: "guardian"}
	hg.persist(nil, f)
	id := hg.storeMatch(hg.historyMatch(f))
	if id <= own {
		t.Fatalf("stored id %d after the spectator's own %d", id, own)
	}
	m, ok, _ := s.Store.Match(id)
	if !ok || len(m.Seats) != 2 || m.Seats[0].PersonaID != alpha.p.ID || m.Seats[1].PersonaID != bravo.p.ID {
		t.Fatalf("match %+v", m)
	}
	if got := s.WatchedMatch(watcher.p.ID); got != id {
		t.Errorf("watcher's watched match %d, want %d", got, id)
	}
	for _, hp := range []*hostedPlayer{alpha, bravo, leaver, ghost} {
		if got := s.WatchedMatch(hp.p.ID); got != 0 {
			t.Errorf("%s: watched match %d, want none", hp.p.Name, got)
		}
	}
	// the spectator's own history is unchanged: only its played match
	if ms, _ := s.Store.MatchesOf(watcher.p.ID, 0, 10); len(ms) != 1 || ms[0].ID != own {
		t.Errorf("watcher's own matches %+v", ms)
	}
	if a, _ := s.Store.Account(watcher.userID); len(a.History.Shapers) != 0 || len(a.History.Days) != 0 ||
		a.History.Ranked.Games != 0 || a.History.Rating.Games != 0 {
		t.Errorf("watcher's profile counts the match: %+v", a.History)
	}
	if !strings.Contains(buf.String(), "watched by 1 spectator(s) [") {
		t.Errorf("log:\n%s", buf.String())
	}
	// a newer one replaces it; an older id never steps back
	s.noteWatched([]int64{watcher.p.ID}, id+5)
	s.noteWatched([]int64{watcher.p.ID}, id)
	if got := s.WatchedMatch(watcher.p.ID); got != id+5 {
		t.Errorf("watched match %d, want %d", got, id+5)
	}
	// a game that isn't custom has no spectators
	hg.custom = nil
	if w := hg.historyMatch(f).watchers; len(w) != 0 {
		t.Errorf("non-custom watchers %v", w)
	}
}
