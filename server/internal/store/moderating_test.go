package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The draft and custom ratings (MMR1) are read back exactly, beside the standard one: the accounts' entries and the
// days' parts; a type cleared (the zero Rating, an empty day part) leaves no row; Clone copies the maps.
func TestModeRatingsReadBack(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), FileName))
	a, _ := login(t, s, "a@example.com", time.Unix(0, 0))
	std := Rating{Value: 1520, RD: 200, Vol: 0.06, Games: 3, Wins: 2}
	draft := Rating{Value: 1611.25, RD: 180.5, Vol: 0.0599, Games: 4, Wins: 3}
	custom := Rating{Value: 1402, RD: 250, Vol: 0.06, Games: 1}
	if _, err := s.Update(a.UserID, func(a *Account) {
		a.History.Rating = std
		a.History.SetRatingIn(ModeDraft, draft)
		a.History.SetRatingIn(ModeCustom, custom)
		a.History.Days = []HistoryDay{{Day: "2026-11-11", Rated: GamesWon{1, 1}, RatingBefore: 1500},
			{Day: "2026-11-12"}}
		a.History.Days[0].SetRatedIn(ModeDraft, ModeDay{GamesWon{2, 1}, 1580})
		a.History.Days[1].SetRatedIn(ModeCustom, ModeDay{GamesWon{1, 0}, 1500})
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openT(t, s.Path())
	got, _ := s.Account(a.UserID)
	h := got.History
	if h.Rating != std || h.RatingIn(ModeStandard) != std || h.RatingIn(ModeDraft) != draft ||
		h.RatingIn(ModeCustom) != custom || len(h.Days) != 2 ||
		h.Days[0].RatedIn(ModeDraft) != (ModeDay{GamesWon{2, 1}, 1580}) ||
		h.Days[0].RatedIn(ModeStandard) != (ModeDay{GamesWon{1, 1}, 1500}) ||
		h.Days[1].RatedIn(ModeCustom) != (ModeDay{GamesWon{1, 0}, 1500}) || h.Days[1].RatedIn(ModeDraft) != (ModeDay{}) {
		t.Fatalf("read back %+v", h)
	}
	c := h.Clone()
	c.ModeRatings[ModeDraft] = Rating{}
	c.Days[0].ModeRated[ModeDraft] = ModeDay{}
	if h.RatingIn(ModeDraft) != draft || h.Days[0].RatedIn(ModeDraft).Rated.Games != 2 {
		t.Error("Clone shares the maps")
	}
	// clearing a type removes its rows
	if _, err := s.Update(a.UserID, func(a *Account) {
		a.History.SetRatingIn(ModeCustom, Rating{})
		a.History.Days[1].SetRatedIn(ModeCustom, ModeDay{})
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM mode_ratings WHERE mode = 'custom') +
		(SELECT COUNT(*) FROM mode_rating_days WHERE mode = 'custom')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("custom rows left: %d %v", n, err)
	}
	if got, _ := s.Account(a.UserID); got.History.ModeRatings[ModeCustom] != (Rating{}) || got.History.Days[1].ModeRated != nil ||
		got.History.RatingIn(ModeDraft) != draft {
		t.Errorf("after clearing custom: %+v", got.History)
	}
}

// Each type's ranking reads only its own ratings and days (MMR1): the draft tab lists only the accounts with a draft
// rating, by it, with its games, wins and the ranking before a day from the draft days; the standard tab is unchanged
// by them; an unknown type is an error.
func TestRatingLeaderboardModes(t *testing.T) {
	s := New()
	const today = "2026-11-12"
	type acct struct {
		std, draft, custom Rating
		draftDays          []ModeDay // today's draft part (at most one)
	}
	accts := []acct{
		{std: Rating{Value: 1700, RD: 80, Vol: 0.06, Games: 30, Wins: 20}}, // 1: standard only
		{std: Rating{Value: 1400, RD: 80, Vol: 0.06, Games: 12, Wins: 4},
			draft: Rating{Value: 1650.4, RD: 90, Vol: 0.06, Games: 11, Wins: 8}}, // 2
		{draft: Rating{Value: 1600, RD: 90, Vol: 0.06, Games: 14, Wins: 9},
			draftDays: []ModeDay{{GamesWon{3, 3}, 1520}}}, // 3: 1520 before today
		{draft: Rating{Value: 1700, RD: 200, Vol: 0.06, Games: 2, Wins: 2},
			custom: Rating{Value: 1550, RD: 100, Vol: 0.06, Games: 10, Wins: 6}}, // 4: 2 draft games
	}
	ids := make([]int64, len(accts))
	for i, c := range accts {
		a, _ := login(t, s, fmt.Sprintf("p%d@example.com", i+1), time.Unix(0, 0))
		ids[i] = a.Personas[0].ID
		if _, err := s.Update(a.UserID, func(a *Account) {
			a.History.Rating = c.std
			a.History.SetRatingIn(ModeDraft, c.draft)
			a.History.SetRatingIn(ModeCustom, c.custom)
			for _, md := range c.draftDays {
				d := HistoryDay{Day: today}
				d.SetRatedIn(ModeDraft, md)
				a.History.Days = append(a.History.Days, d)
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	row := func(i int, r Rating, games, wins, skill uint32) LeaderRow {
		return LeaderRow{PersonaID: ids[i], Name: fmt.Sprintf("p%d", i+1), Games: games, Wins: wins, Skill: skill,
			Rating: skill, RatedGames: r.Games}
	}
	rows, err := s.RatingLeaderboard(ModeDraft, "", 1, 200)
	if want := []LeaderRow{row(3, accts[3].draft, 2, 2, 1700), row(1, accts[1].draft, 11, 8, 1650),
		row(2, accts[2].draft, 14, 9, 1600)}; err != nil || !reflect.DeepEqual(rows, want) {
		t.Errorf("draft:\n got %+v %v\nwant %+v", rows, err, want)
	}
	if rows, err = s.RatingLeaderboard(ModeDraft, "", 10, 200); err != nil || len(rows) != 2 || rows[0].PersonaID != ids[1] {
		t.Errorf("draft, at least 10: %+v %v", rows, err)
	}
	// before today: account 3 back at 1520 with 11 games, 9 - 3 = 6 wins
	if rows, err = s.RatingLeaderboard(ModeDraft, today, 10, 200); err != nil || !reflect.DeepEqual(rows,
		[]LeaderRow{row(1, accts[1].draft, 11, 8, 1650), row(2, accts[2].draft, 11, 6, 1520)}) {
		t.Errorf("draft before today: %+v %v", rows, err)
	}
	if rows, err = s.RatingLeaderboard(ModeCustom, "", 10, 200); err != nil || !reflect.DeepEqual(rows,
		[]LeaderRow{row(3, accts[3].custom, 10, 6, 1550)}) {
		t.Errorf("custom: %+v %v", rows, err)
	}
	// the standard tab: History.Rating only (accounts 3 and 4 have no standard game: below the minimum)
	if rows, err = s.RatingLeaderboard(ModeStandard, "", 10, 200); err != nil || !reflect.DeepEqual(rows,
		[]LeaderRow{row(0, accts[0].std, 30, 20, 1700), row(1, accts[1].std, 12, 4, 1400)}) {
		t.Errorf("standard: %+v %v", rows, err)
	}
	if _, err := s.RatingLeaderboard("aram", "", 1, 200); err == nil {
		t.Error("an unknown type ranked")
	}
}

// A database of version 9 migrates to version 10: its matches are rated (unrated 0) and keep their rules, its accounts
// have no draft or custom rating and keep their standard one; a match marked unrated and the new types read back.
func TestMigrateFromVersion9(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied TEXT NOT NULL, note TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:9] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_version VALUES (?, '', ?)`, m.version, m.note); err != nil {
			t.Fatal(err)
		}
	}
	tx, _ := db.Begin()
	insertOldAccount(t, tx)
	if _, err := tx.Exec(`UPDATE accounts SET rating = 1620.5, rating_rd = 120, rated_games = 12, rated_wins = 7`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO matches (id, game_id, kind, mode, level, winner, reason, started, ended,
		duration_ms, rules) VALUES (1, 7, 'custom', 'custom', 'MM_Map_v3', 1, 'guardian', '2026-11-11T10:00:00Z',
		'2026-11-11T10:20:00Z', 1200000, '{"pick":"draft"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO match_seats (match_id, seat, team, persona_id, user_id) VALUES (1, 0, 1, ?, ?)`,
		FirstPersonaID, FirstUserID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openT(t, path)
	if want := fmt.Sprintf("1 accounts, schema 9 -> %d", SchemaVersion()); s.Info() != want || SchemaVersion() < 10 {
		t.Errorf("info %q, want %q", s.Info(), want)
	}
	a, ok := s.Account(FirstUserID)
	if !ok || a.History.Rating != (Rating{Value: 1620.5, RD: 120, Vol: 0.06, Games: 12, Wins: 7}) || a.History.ModeRatings != nil {
		t.Fatalf("after the migration: %v %+v", ok, a.History)
	}
	m, found, err := s.Match(1)
	if err != nil || !found || m.Unrated || m.Rules != `{"pick":"draft"}` || len(m.Seats) != 1 {
		t.Fatalf("the old match: %+v %v %v", m, found, err)
	}
	id, err := s.InsertMatch(Match{GameID: 8, Kind: "custom", Mode: "custom", Winner: 2, Unrated: true,
		Started: time.Unix(1700000000, 0), Ended: time.Unix(1700001200, 0), Seats: m.Seats})
	if err != nil {
		t.Fatal(err)
	}
	if ms, err := s.MatchesAfter(0, 10); err != nil || len(ms) != 2 || ms[0].Unrated || !ms[1].Unrated || ms[1].ID != id {
		t.Errorf("MatchesAfter: %+v %v", ms, err)
	}
	draft := Rating{Value: 1555, RD: 300, Vol: 0.06, Games: 1, Wins: 1}
	if _, err := s.Update(FirstUserID, func(a *Account) { a.History.SetRatingIn(ModeDraft, draft) }); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Account(FirstUserID); a.History.RatingIn(ModeDraft) != draft || a.History.Rating.Value != 1620.5 {
		t.Errorf("read back: %+v", a.History)
	}
}
