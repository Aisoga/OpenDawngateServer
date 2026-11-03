package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The rating ranking: active accounts with enough rated games (History.Rating; the Leaderboard's counts aren't read),
// by rating, then more rated games, then persona id; STAT[0]'s Skill the rating rounded (never below 0); the ranking
// before a day: that day's and later rated games out, the rating the one before the first of them (RatingBefore).
func TestRatingLeaderboard(t *testing.T) {
	s := New()
	const today = "2026-11-02"
	type acct struct {
		rating Rating
		ranked GamesWon
		days   []HistoryDay
		status Status
	}
	accts := []acct{
		{rating: Rating{Value: 1612.4, RD: 80, Vol: 0.06, Games: 12, Wins: 8}, ranked: GamesWon{30, 1}}, // 1
		{rating: Rating{Value: 1700.6, RD: 70, Vol: 0.06, Games: 14, Wins: 9},
			days: []HistoryDay{{Day: today, Rated: GamesWon{Games: 3, Wins: 3}, RatingBefore: 1580.2}}}, // 2: 1580 before today
		{rating: Rating{Value: 1612.4, RD: 90, Vol: 0.06, Games: 20, Wins: 9}},                         // 3: ties 1, more games: ahead
		{rating: Rating{Value: -12, RD: 300, Vol: 0.06, Games: 10, Wins: 0}},                           // 4: below 0, STAT 0
		{rating: Rating{Value: 1900, RD: 60, Vol: 0.06, Games: 40, Wins: 30}, status: StatusDisabled},  // 5
		{rating: Rating{Value: 1650, RD: 200, Vol: 0.06, Games: 2, Wins: 2}, ranked: GamesWon{50, 50}}, // 6: 2 rated
		{}, // 7: no rated game (1500, the start)
		{rating: Rating{Value: 1550, RD: 100, Vol: 0.06, Games: 4, Wins: 3},
			days: []HistoryDay{{Day: "2026-11-01", Rated: GamesWon{Games: 1, Wins: 1}, RatingBefore: 1530},
				{Day: today, Rated: GamesWon{Games: 3, Wins: 2}, RatingBefore: 1545}}}, // 8: all but one today
	}
	ids := make([]int64, len(accts))
	for i, c := range accts {
		a, _ := login(t, s, fmt.Sprintf("p%d@example.com", i+1), time.Unix(0, 0))
		ids[i] = a.Personas[0].ID
		if _, err := s.Update(a.UserID, func(a *Account) {
			a.History.Rating, a.History.Ranked, a.History.Days = c.rating, c.ranked, c.days
		}); err != nil {
			t.Fatal(err)
		}
		if c.status != StatusActive {
			if err := s.SetStatus(a.UserID, c.status); err != nil {
				t.Fatal(err)
			}
		}
	}
	row := func(i int, games, wins, skill uint32) LeaderRow {
		return LeaderRow{PersonaID: ids[i], Name: fmt.Sprintf("p%d", i+1), Games: games, Wins: wins, Skill: skill, Rating: skill,
			RatedGames: accts[i].rating.Games}
	}
	rows, err := s.RatingLeaderboard("", 1, 200)
	if err != nil {
		t.Fatal(err)
	}
	want := []LeaderRow{row(1, 14, 9, 1701), row(5, 2, 2, 1650), row(2, 20, 9, 1612), row(0, 12, 8, 1612),
		row(7, 4, 3, 1550), row(3, 10, 0, 0)}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("now:\n got %+v\nwant %+v", rows, want)
	}
	if rows, err = s.RatingLeaderboard("", 10, 200); err != nil || !reflect.DeepEqual(rows,
		[]LeaderRow{row(1, 14, 9, 1701), row(2, 20, 9, 1612), row(0, 12, 8, 1612), row(3, 10, 0, 0)}) {
		t.Errorf("at least 10 rated games: %+v %v", rows, err)
	}
	// before today: 2 back at 1580 with 11 games, 8 at 1545 with 1 game
	if rows, err = s.RatingLeaderboard(today, 1, 200); err != nil || !reflect.DeepEqual(rows,
		[]LeaderRow{row(5, 2, 2, 1650), row(2, 20, 9, 1612), row(0, 12, 8, 1612), row(1, 11, 6, 1580),
			row(7, 1, 1, 1545), row(3, 10, 0, 0)}) {
		t.Errorf("before today: %+v %v", rows, err)
	}
	// before yesterday: 8 has no rated game left
	if rows, err = s.RatingLeaderboard("2026-11-01", 1, 200); err != nil || len(rows) != 5 || rows[4].PersonaID != ids[3] {
		t.Errorf("before yesterday: %+v %v", rows, err)
	}
	if rows, err = s.RatingLeaderboard("", 1, 2); err != nil || len(rows) != 2 || rows[1].PersonaID != ids[5] {
		t.Errorf("limit 2: %+v %v", rows, err)
	}
	// the wins ranking is unchanged: its Skill is the wins; its Rating the rating now
	if rows, err = s.Leaderboard("", 1, 1); err != nil || len(rows) != 1 || rows[0] != (LeaderRow{PersonaID: ids[5],
		Name: "p6", Games: 50, Wins: 50, Skill: 50, Rating: 1650, RatedGames: 2}) {
		t.Errorf("the wins ranking: %+v %v", rows, err)
	}
}

// UpdateGroup changes several accounts in one transaction: each once, an unknown one left out, the copies in order.
func TestUpdateGroup(t *testing.T) {
	s := New()
	a, _ := login(t, s, "a@example.com", time.Unix(0, 0))
	b, _ := login(t, s, "b@example.com", time.Unix(0, 0))
	var seen []int64
	out, err := s.UpdateGroup([]int64{b.UserID, 999, a.UserID, b.UserID}, func(as []*Account) {
		for _, x := range as {
			seen = append(seen, x.UserID)
			x.History.Rating = Rating{Value: float64(x.UserID % 1000), RD: 100, Vol: 0.06, Games: 1}
		}
	})
	if err != nil || !reflect.DeepEqual(seen, []int64{b.UserID, a.UserID}) || len(out) != 2 || out[0].UserID != b.UserID {
		t.Fatalf("%v %v %+v", seen, err, out)
	}
	if got, _ := s.Account(a.UserID); got.History.Rating != (Rating{Value: 1, RD: 100, Vol: 0.06, Games: 1}) {
		t.Errorf("a: %+v", got.History.Rating)
	}
}

// A database of version 6 migrates to version 7: every account at the start rating (read back as the zero value, no
// rated game), the history days without rated games; a stored rating and day read back exactly.
func TestMigrateFromVersion6(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied TEXT NOT NULL, note TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:6] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_version VALUES (?, '', ?)`, m.version, m.note); err != nil {
			t.Fatal(err)
		}
	}
	tx, _ := db.Begin()
	insertOldAccount(t, tx)
	if _, err := tx.Exec(`INSERT INTO history_days (user_id, pos, day, ranked_games, ranked_wins) VALUES (?, 0, '2026-11-01', 2, 1)`,
		FirstUserID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openT(t, path)
	if want := fmt.Sprintf("1 accounts, schema 6 -> %d", SchemaVersion()); s.Info() != want || SchemaVersion() < 7 {
		t.Errorf("info %q, want %q", s.Info(), want)
	}
	a, ok := s.Account(FirstUserID)
	if !ok || a.History.Rating != (Rating{}) || a.History.Rating.OrStart() != StartRating || len(a.History.Days) != 1 ||
		a.History.Days[0].Ranked != (GamesWon{2, 1}) || a.History.Days[0].Rated != (GamesWon{}) {
		t.Fatalf("after the migration: %v %+v", ok, a.History)
	}
	r := Rating{Value: 1523.25, RD: 211.5, Vol: 0.0599991, Games: 3, Wins: 2}
	if _, err := s.Update(FirstUserID, func(a *Account) {
		a.History.Rating = r
		a.History.Days[0].Rated, a.History.Days[0].RatingBefore = GamesWon{1, 1}, 1488.5
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openT(t, path)
	if a, _ := s.Account(FirstUserID); a.History.Rating != r || a.History.Days[0].Rated != (GamesWon{1, 1}) ||
		a.History.Days[0].RatingBefore != 1488.5 {
		t.Errorf("read back: %+v", a.History)
	}
}
