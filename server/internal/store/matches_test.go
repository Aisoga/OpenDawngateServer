package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// testMatch is a finished match with the given humans (persona ids, team 1 then team 2 alternating) and one bot.
func testMatch(gameID int64, at time.Time, personas ...int64) Match {
	m := Match{GameID: gameID, Kind: "pvp", Mode: "pvp", Level: "SM_Map_v5", Winner: 1, Reason: "guardian",
		Started: at.Add(-20 * time.Minute), Ended: at, DurationMs: 1200000}
	for i, p := range personas {
		m.Seats = append(m.Seats, MatchSeat{Seat: i, PersonaID: p, UserID: p - FirstPersonaID + FirstUserID, Team: 1 + i%2,
			Won: i%2 == 0, Kills: int64(i), Stats: map[string]int64{"kills": int64(i)}})
	}
	m.Seats = append(m.Seats, MatchSeat{Seat: 9, Team: 2, Bot: true, Noun: 284823512, Role: 1, Kills: 2, Deaths: 3,
		Assists: 4, CS: 88, Gold: 7420, Level: 11, Stats: map[string]int64{"damageDealt": 10422}})
	return m
}

func matchIDs(ms []Match) []int64 {
	var out []int64
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

// A match is stored whole and reads back as it went in (the names from personas, the bot without one); pages go
// newest first by the store's own id, and a persona's list holds only its matches.
func TestMatches(t *testing.T) {
	s := New()
	t0 := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	a, _ := login(t, s, "a@x", t0)
	b, _ := login(t, s, "b@x", t0)
	pa, pb := a.Personas[0].ID, b.Personas[0].ID

	if ms, err := s.RecentMatches(0, 10); err != nil || len(ms) != 0 {
		t.Errorf("an empty history: %v %v", ms, err)
	}
	if _, found, err := s.Match(1); found || err != nil {
		t.Errorf("match 1 before any: %v %v", found, err)
	}
	// match 1: a and b with sources and a timeline; matches 2-5: a alone (game ids restart: game 1 again)
	m1 := testMatch(1, t0, pa, pb)
	m1.Timeline = json.RawMessage(`{"stepSec":60,"sec":[60]}`)
	m1.Seats[0].Sources = json.RawMessage(`[{"kind":"basic","id":0,"damage":5,"heal":0}]`)
	m1.Seats[0].Left = true
	id1, err := s.InsertMatch(m1)
	if err != nil || id1 != 1 {
		t.Fatalf("InsertMatch: %d %v", id1, err)
	}
	for i := range 4 {
		if id, err := s.InsertMatch(testMatch(int64(1+i%2), t0.Add(time.Duration(i+1)*time.Hour), pa)); err != nil || id != int64(i+2) {
			t.Fatalf("InsertMatch %d: %d %v", i, id, err)
		}
	}

	got, found, err := s.Match(id1)
	if err != nil || !found {
		t.Fatalf("Match: %v %v", found, err)
	}
	want := m1
	want.ID = 1
	want.Seats = append([]MatchSeat(nil), m1.Seats...)
	want.Seats[0].Name, want.Seats[1].Name = a.Personas[0].Name, b.Personas[0].Name
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Match(1)\n got %+v\nwant %+v", got, want)
	}

	// pages: newest first, keyset by id; a short page is the last; the limit is clamped
	for _, c := range []struct {
		before int64
		limit  int
		want   []int64
	}{
		{0, 2, []int64{5, 4}}, {4, 2, []int64{3, 2}}, {2, 2, []int64{1}}, {1, 2, nil}, {0, 0, []int64{5, 4, 3, 2, 1}},
		{-1, 1000, []int64{5, 4, 3, 2, 1}}, {99, 1, []int64{5}},
	} {
		ms, err := s.RecentMatches(c.before, c.limit)
		if err != nil || !reflect.DeepEqual(matchIDs(ms), c.want) {
			t.Errorf("RecentMatches(%d, %d) = %v %v, want %v", c.before, c.limit, matchIDs(ms), err, c.want)
		}
	}
	ms, _ := s.RecentMatches(3, 1)
	if len(ms) != 1 || len(ms[0].Seats) != 2 || !ms[0].Seats[1].Bot || ms[0].Seats[1].Stats["damageDealt"] != 10422 ||
		!ms[0].Ended.Equal(t0.Add(time.Hour)) {
		t.Errorf("a page's match %+v", ms)
	}
	// a page leaves the timeline and the sources out (Match has them)
	if ms, _ := s.RecentMatches(2, 1); len(ms) != 1 || ms[0].Timeline != nil || ms[0].Seats[0].Sources != nil ||
		!ms[0].Seats[0].Left {
		t.Errorf("match 1 on a page %+v", ms)
	}
	for _, c := range []struct {
		persona, before int64
		limit           int
		want            []int64
	}{
		{pa, 0, 3, []int64{5, 4, 3}}, {pa, 3, 3, []int64{2, 1}}, {pb, 0, 3, []int64{1}}, {pb, 1, 3, nil}, {pb + 100, 0, 3, nil},
	} {
		ms, err := s.MatchesOf(c.persona, c.before, c.limit)
		if err != nil || !reflect.DeepEqual(matchIDs(ms), c.want) {
			t.Errorf("MatchesOf(%d, %d, %d) = %v %v, want %v", c.persona, c.before, c.limit, matchIDs(ms), err, c.want)
		}
	}
	// a page of a persona carries every seat of its matches (the other players and the bot too)
	if ms, _ := s.MatchesOf(pb, 0, 1); len(ms) != 1 || len(ms[0].Seats) != 3 || ms[0].Seats[0].PersonaID != pa {
		t.Errorf("b's match %+v", ms)
	}
}

// The names are read when a match is read: a rename shows, a deleted account's persona is "Deleted player", and a
// persona whose account row is gone leaves its seat without a name (no foreign key: the match stays).
func TestMatchNames(t *testing.T) {
	s := New()
	t0 := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	var ps []int64
	var users []int64
	for _, e := range []string{"a@x", "b@x", "c@x"} {
		acct, _ := login(t, s, e, t0)
		ps, users = append(ps, acct.Personas[0].ID), append(users, acct.UserID)
	}
	id, err := s.InsertMatch(testMatch(1, t0, ps...))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(users[0], func(a *Account) { a.Personas[0].Name = "Renamed" }); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(users[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM accounts WHERE user_id = ?`, users[2]); err != nil {
		t.Fatal(err)
	}
	m, found, err := s.Match(id)
	if err != nil || !found {
		t.Fatalf("Match: %v %v", found, err)
	}
	var names []string
	for _, st := range m.Seats {
		names = append(names, st.Name)
	}
	if want := []string{"Renamed", DeletedName, "", ""}; !reflect.DeepEqual(names, want) || m.Seats[2].PersonaID != ps[2] {
		t.Errorf("names %q, want %q (seats %+v)", names, want, m.Seats)
	}
}

// A match that can't be stored whole stores nothing: a seat twice, no seat at all.
func TestInsertMatchRefused(t *testing.T) {
	s := New()
	m := testMatch(1, time.Unix(0, 0), FirstPersonaID)
	m.Seats = append(m.Seats, MatchSeat{Seat: 9, Bot: true, Team: 2})
	if _, err := s.InsertMatch(m); err == nil {
		t.Error("a seat twice was stored")
	}
	if _, err := s.InsertMatch(Match{GameID: 1}); err != ErrNoSeats {
		t.Errorf("no seats: %v", err)
	}
	var n, seats int
	s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM matches), (SELECT COUNT(*) FROM match_seats)`).Scan(&n, &seats)
	if n != 0 || seats != 0 {
		t.Errorf("%d matches, %d seats stored", n, seats)
	}
	// the next one gets id 1 (the failed transaction left nothing behind)
	if id, err := s.InsertMatch(testMatch(1, time.Unix(0, 0))); err != nil || id != 1 {
		t.Errorf("after the refusals: %d %v", id, err)
	}
}

// a database of version 5 migrates to version 6: the history is empty, the accounts are unchanged, and the next match
// is stored.
func TestMigrateFromVersion5(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied TEXT NOT NULL, note TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:5] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_version VALUES (?, '', ?)`, m.version, m.note); err != nil {
			t.Fatal(err)
		}
	}
	tx, _ := db.Begin()
	insertOldAccount(t, tx)
	if _, err := tx.Exec(`UPDATE accounts SET ranked_games = 3, ranked_wins = 2`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openT(t, path)
	if want := fmt.Sprintf("1 accounts, schema 5 -> %d", SchemaVersion()); s.Info() != want {
		t.Errorf("info %q, want %q", s.Info(), want)
	}
	if a, ok := s.Account(FirstUserID); !ok || a.History.Ranked != (GamesWon{3, 2}) {
		t.Errorf("account %v, Leaderboard counts %+v", ok, a.History.Ranked)
	}
	if ms, err := s.RecentMatches(0, 10); err != nil || len(ms) != 0 {
		t.Errorf("history after the migration: %v %v", ms, err)
	}
	id, err := s.InsertMatch(testMatch(1, time.Unix(0, 0), FirstPersonaID))
	if err != nil {
		t.Fatal(err)
	}
	if ms, err := s.MatchesOf(FirstPersonaID, 0, 10); err != nil || len(ms) != 1 || ms[0].ID != id || ms[0].Seats[0].Name != "Old" {
		t.Errorf("the old account's match: %+v %v", ms, err)
	}
}
