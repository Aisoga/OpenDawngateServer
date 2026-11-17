package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// MAPRULES5: the map's own rules a match played, as the column's JSON; a column this build can't read counts as a
// played victory (never rated by mistake).
func TestMapRulesRecord(t *testing.T) {
	cases := []struct {
		r       MapRules
		record  string
		victory bool
	}{
		{MapRules{}, "", false},
		{MapRules{Tuned: true}, `{"tuned":true}`, false},
		{MapRules{StartLevel: 6}, `{"startLevel":6}`, false},
		{MapRules{Kills: 20, Minutes: 15, StartLevel: 6, Tuned: true}, `{"kills":20,"minutes":15,"startLevel":6,"tuned":true}`, true},
		{MapRules{Minutes: 3}, `{"minutes":3}`, true},
	}
	for _, c := range cases {
		if got := c.r.Record(); got != c.record {
			t.Errorf("%+v: record %q, want %q", c.r, got, c.record)
		}
		back, err := ParseMapRules(c.record)
		if err != nil || back != c.r {
			t.Errorf("%q read back as %+v (%v)", c.record, back, err)
		}
		if got := (Match{MapRules: c.record}).MapVictory(); got != c.victory || c.r.Victory() != c.victory {
			t.Errorf("%q: victory %v, want %v", c.record, got, c.victory)
		}
	}
	if !(Match{MapRules: "{broken"}).MapVictory() {
		t.Error("an unreadable column isn't counted as a played victory")
	}
}

// A database of version 10 migrates to version 11: its matches get an empty map_rules (no map rules), and a new match's
// map_rules reads back from every read path (Match, MatchesOf, MatchesAfter, RecentMatches).
func TestMigrateFromVersion10(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied TEXT NOT NULL, note TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:10] {
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
		duration_ms, rules, unrated) VALUES (1, 7, 'custom', 'custom', 'CM_Ring', 1, 'guardian', '2026-11-11T10:00:00Z',
		'2026-11-11T10:20:00Z', 1200000, '{"pick":"draft"}', 1)`); err != nil {
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
	if want := fmt.Sprintf("1 accounts, schema 10 -> %d", SchemaVersion()); s.Info() != want || SchemaVersion() < 11 {
		t.Errorf("info %q, want %q", s.Info(), want)
	}
	m, found, err := s.Match(1)
	if err != nil || !found || !m.Unrated || m.Rules != `{"pick":"draft"}` || m.MapRules != "" || m.MapVictory() {
		t.Fatalf("the old match: %+v %v %v", m, found, err)
	}
	rec := MapRules{Kills: 3, StartLevel: 6, Tuned: true}.Record()
	id, err := s.InsertMatch(Match{GameID: 8, Kind: "custom", Mode: "custom", Level: "CM_Tuned", Winner: 2, Reason: "kills",
		Started: time.Unix(1700000000, 0), Ended: time.Unix(1700001200, 0), MapRules: rec, Seats: m.Seats})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Match(id)
	if err != nil || got.MapRules != rec || !got.MapVictory() || got.Unrated {
		t.Errorf("Match: %+v %v", got, err)
	}
	if ms, err := s.MatchesAfter(0, 10); err != nil || len(ms) != 2 || ms[0].MapRules != "" || ms[1].MapRules != rec {
		t.Errorf("MatchesAfter: %+v %v", ms, err)
	}
	if ms, err := s.MatchesOf(FirstPersonaID, 0, 10); err != nil || len(ms) != 2 || ms[0].MapRules != rec {
		t.Errorf("MatchesOf: %+v %v", ms, err)
	}
	if ms, err := s.RecentMatches(0, 10); err != nil || len(ms) != 2 || ms[0].MapRules != rec || ms[1].MapRules != "" {
		t.Errorf("RecentMatches: %+v %v", ms, err)
	}
}
