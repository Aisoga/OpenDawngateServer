package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// M32 (MITEMS1): a database of version 11 migrates to version 12: its seats get an empty inventory and purchases
// (none), and a new match's seats keep both, read by Match only (the lists' pages leave them out, as sources).
func TestMigrateFromVersion11(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied TEXT NOT NULL, note TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:11] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_version VALUES (?, '', ?)`, m.version, m.note); err != nil {
			t.Fatal(err)
		}
	}
	tx, _ := db.Begin()
	insertOldAccount(t, tx)
	if _, err := tx.Exec(`INSERT INTO matches (id, game_id, kind, mode, level, winner, reason, started, ended,
		duration_ms) VALUES (1, 7, 'pvp', 'pvp', 'MM_Map_v3', 1, 'guardian', '2026-11-16T10:00:00Z',
		'2026-11-16T10:20:00Z', 1200000)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO match_seats (match_id, seat, team, persona_id, user_id, sources) VALUES (1, 0, 1, ?, ?,
		'[{"kind":"basic","id":0,"damage":5,"heal":0}]')`, FirstPersonaID, FirstUserID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openT(t, path)
	if want := fmt.Sprintf("1 accounts, schema 11 -> %d", SchemaVersion()); s.Info() != want || SchemaVersion() < 12 {
		t.Errorf("info %q, want %q", s.Info(), want)
	}
	old, found, err := s.Match(1)
	if err != nil || !found || len(old.Seats) != 1 || old.Seats[0].Inventory != nil || old.Seats[0].Purchases != nil ||
		len(old.Seats[0].Sources) == 0 {
		t.Fatalf("the old match: %+v %v %v", old, found, err)
	}

	inv := json.RawMessage(`[75803296,0,0,0,0,0,2011516473,0,0,295762247,0]`)
	buys := json.RawMessage(`[{"ms":4210,"kind":"buy","id":75803296},{"ms":90300,"kind":"sell","id":75803296}]`)
	bot := MatchSeat{Seat: 5, Team: 2, Bot: true, Inventory: json.RawMessage(`[2033400596,0,0,0,0,0,0,0,0,0,0]`),
		Purchases: json.RawMessage(`[{"ms":3000,"kind":"buy","id":2033400596}]`)}
	human := old.Seats[0]
	human.Inventory, human.Purchases = inv, buys
	id, err := s.InsertMatch(Match{GameID: 8, Kind: "pvp", Mode: "pvp", Level: "MM_Map_v3", Winner: 2, Reason: "guardian",
		Started: time.Unix(1700000000, 0), Ended: time.Unix(1700001200, 0), Seats: []MatchSeat{human, bot}})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Match(id)
	if err != nil || len(got.Seats) != 2 {
		t.Fatalf("Match: %+v %v", got, err)
	}
	if h, b := got.Seats[0], got.Seats[1]; string(h.Inventory) != string(inv) || string(h.Purchases) != string(buys) ||
		string(b.Inventory) != string(bot.Inventory) || string(b.Purchases) != string(bot.Purchases) || !b.Bot {
		t.Errorf("seats read back: %+v", got.Seats)
	}
	for name, read := range map[string]func() ([]Match, error){
		"RecentMatches": func() ([]Match, error) { return s.RecentMatches(0, 10) },
		"MatchesOf":     func() ([]Match, error) { return s.MatchesOf(FirstPersonaID, 0, 10) },
		"MatchesAfter":  func() ([]Match, error) { return s.MatchesAfter(0, 10) },
	} {
		ms, err := read()
		if err != nil || len(ms) != 2 {
			t.Fatalf("%s: %+v %v", name, ms, err)
		}
		for _, m := range ms {
			for _, st := range m.Seats {
				if st.Inventory != nil || st.Purchases != nil || st.Sources != nil {
					t.Errorf("%s: match %d seat %d carries the detail: %+v", name, m.ID, st.Seat, st)
				}
			}
		}
	}
}
