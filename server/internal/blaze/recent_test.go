package blaze

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
)

// The home page's results: the shapers by their titles, per team in roster order ("" without the host's result), the
// bots; no name kept, not even in memory.
func TestRecentResultOf(t *testing.T) {
	c := loadFixtureContent(t)
	hg := resultGame(gmshost.ModeCustom, "2", 5, true, []resultSeat{
		{name: "NameSecretA", team: 0, stats: kda("ember.Noun", 1, 2, 3)},
		{name: "NameSecretB", team: 1},                                       // no result from the host
		{name: "NameSecretC", team: 1, away: true, stats: kda("x", 0, 0, 0)}, // never joined: not a player
		{name: "NameSecretD", team: 2},                                       // a spectator
		{name: "NameSecretE", team: 0, stats: kda("unknown.Noun", 0, 0, 0)},
	})
	x := recentResultOf(hg.resultLocked(gmshost.FinishGame{WinnerTeam: 2, DurationMs: 61500, Reason: "surrender"}, resultAt), c)
	got := fmt.Sprintf("%+v", x)
	want := "{GameID:7 Mode:custom Kind:Custom game Level:SM_Map_v5 WinnerTeam:2 Reason:surrender Duration:1m1.5s " +
		"At:" + resultAt.String() + " Teams:[{Shapers:[Ember ] Bots:3} {Shapers:[] Bots:4}]}"
	if got != want || fmt.Sprintf("%q %q", x.Teams[0].Shapers, x.Teams[1].Shapers) != `["Ember" ""] [""]` {
		t.Errorf("RecentResult\n got %s\nwant %s", got, want)
	}
	if strings.Contains(got, "Secret") {
		t.Errorf("a name kept: %s", got)
	}
}

// The ring keeps the last RecentResultsKept, newest first; a caller's copy doesn't change it; safe for concurrent use.
func TestRecentResultsRing(t *testing.T) {
	s := &Server{}
	if got := s.RecentResults(); len(got) != 0 {
		t.Fatalf("empty: %+v", got)
	}
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); s.recent.add(RecentResult{GameID: int64(100 + i)}); s.RecentResults() }()
	}
	wg.Wait()
	for i := 1; i <= RecentResultsKept+2; i++ {
		s.recent.add(RecentResult{GameID: int64(i), At: time.Unix(int64(i), 0),
			Teams: [2]RecentTeam{{Shapers: []string{"Ember"}}, {Shapers: []string{}}}})
	}
	got := s.RecentResults()
	if len(got) != RecentResultsKept || got[0].GameID != RecentResultsKept+2 || got[len(got)-1].GameID != 3 {
		t.Fatalf("ring: %d results, first %d, last %d", len(got), got[0].GameID, got[len(got)-1].GameID)
	}
	got[0].Teams[0].Shapers[0] = "changed"
	if s.RecentResults()[0].Teams[0].Shapers[0] != "Ember" {
		t.Error("a caller's copy changed the ring")
	}
}
