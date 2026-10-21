package store

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// The ranking: active accounts with enough Leaderboard games (History.Ranked; the profile's PlayerStats, practice games
// included, aren't read), by wins, then fewer games, then persona id; the favourite shaper by games of any kind; a day's
// Leaderboard games taken out for the ranking before that day; disabled, banned and deleted accounts and accounts
// without Leaderboard games left out; the limit.
func TestLeaderboard(t *testing.T) {
	s := New()
	const today = "2026-10-17"
	type acct struct {
		ranked  GamesWon
		shapers map[uint32]GamesWon
		day     *GamesWon // today's Leaderboard games (and one lost game the day before)
		status  Status
	}
	accts := []acct{
		{ranked: GamesWon{10, 4}, shapers: map[uint32]GamesWon{7: {Games: 6, Wins: 3}, 9: {Games: 4, Wins: 1}}}, // 1
		{ranked: GamesWon{8, 4}, shapers: map[uint32]GamesWon{9: {Games: 8, Wins: 4}},
			day: &GamesWon{3, 3}}, // 2: ahead of 1 (fewer games); before today 5 games, 1 win
		{ranked: GamesWon{3, 6}}, // 3: wins above games (a count out of step): counted as 3
		{},                       // 4: no Leaderboard games (its PlayerStats say 50)
		{ranked: GamesWon{20, 20}, status: StatusBanned}, // 5
		{ranked: GamesWon{2, 2}, day: &GamesWon{2, 2}},   // 6: all games today
	}
	ids := make([]int64, len(accts))
	for i, c := range accts {
		a, _ := login(t, s, fmt.Sprintf("p%d@example.com", i+1), time.Unix(0, 0))
		ids[i] = a.Personas[0].ID
		if _, err := s.Update(a.UserID, func(a *Account) {
			a.Stats["PlayerStats"] = map[string]string{"PLAYED": "50", "WINS": "50"} // every game, practice included
			a.History.Ranked = c.ranked
			a.History.Shapers = c.shapers
			if c.day != nil {
				a.History.Days = []HistoryDay{{Day: "2026-10-16", Shapers: map[uint32]ShaperDay{1: {Games: 4}},
					Ranked: GamesWon{Games: 1}}, {Day: today, Ranked: *c.day}}
			}
		}); err != nil {
			t.Fatal(err)
		}
		if c.status != StatusActive {
			if err := s.SetStatus(a.UserID, c.status); err != nil {
				t.Fatal(err)
			}
		}
	}
	name := func(i int) string { return fmt.Sprintf("p%d", i+1) }
	rows, err := s.Leaderboard("", 1, 200)
	if err != nil {
		t.Fatal(err)
	}
	want := []LeaderRow{
		{ids[1], name(1), 8, 4, 9},
		{ids[0], name(0), 10, 4, 7},
		{ids[2], name(2), 3, 3, 0},
		{ids[5], name(5), 2, 2, 0},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("all time:\n got %+v\nwant %+v", rows, want)
	}
	rows, err = s.Leaderboard(today, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	want = []LeaderRow{
		{ids[0], name(0), 10, 4, 7},
		{ids[2], name(2), 3, 3, 0},
		{ids[1], name(1), 5, 1, 9},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("before today:\n got %+v\nwant %+v", rows, want)
	}
	// from the day before: that day's games too (one each for 2 and 6)
	if rows, err = s.Leaderboard("2026-10-16", 1, 200); err != nil {
		t.Fatal(err)
	}
	want = []LeaderRow{
		{ids[0], name(0), 10, 4, 7},
		{ids[2], name(2), 3, 3, 0},
		{ids[1], name(1), 4, 1, 9},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("before yesterday:\n got %+v\nwant %+v", rows, want)
	}
	if rows, err = s.Leaderboard("", 1, 2); err != nil || len(rows) != 2 || rows[1].PersonaID != ids[0] {
		t.Errorf("limit 2: %+v, %v", rows, err)
	}
	if rows, err = s.Leaderboard("", 8, 200); err != nil || len(rows) != 2 || rows[0].PersonaID != ids[1] ||
		rows[1].PersonaID != ids[0] {
		t.Errorf("at least 8 games: %+v, %v", rows, err)
	}
	if rows, err = s.Leaderboard("", 1, 0); err != nil || rows != nil {
		t.Errorf("limit 0: %+v, %v", rows, err)
	}
}
