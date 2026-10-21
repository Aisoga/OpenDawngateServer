package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// a database of version 3 (with a password and a status) migrates to the current version: every account is a player,
// the audit log and the settings are empty, and what version 3 stored is unchanged.
func TestMigrateFromVersion3(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied TEXT NOT NULL, note TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:3] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_version VALUES (?, '', ?)`, m.version, m.note); err != nil {
			t.Fatal(err)
		}
	}
	tx, _ := db.Begin()
	insertOldAccount(t, tx)
	if _, err := tx.Exec(`UPDATE accounts SET password = '$argon2id$old', status = 1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openT(t, path)
	if want := fmt.Sprintf("1 accounts, schema 3 -> %d", SchemaVersion()); s.Info() != want {
		t.Errorf("info %q, want %q", s.Info(), want)
	}
	c, found, err := s.CredentialsByID(FirstUserID)
	if err != nil || !found || c.Hash != "$argon2id$old" || c.Status != StatusDisabled || c.Role != RolePlayer {
		t.Errorf("credentials %+v %v %v", c, found, err)
	}
	// version 5: the Leaderboard's counts start at 0 (the older games can't be told apart from practice games)
	if a, ok := s.Account(FirstUserID); !ok || a.History.Ranked != (GamesWon{}) {
		t.Errorf("account %v, Leaderboard counts %+v", ok, a.History.Ranked)
	}
	if log, err := s.AuditLog(10, 0); err != nil || len(log) != 0 {
		t.Errorf("audit log %v %v", log, err)
	}
	if _, _, found, err := s.Setting("signup_mode"); found || err != nil {
		t.Errorf("a setting found (%v)", err)
	}
}

func TestRolesAndSearch(t *testing.T) {
	s := New()
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	var ids []int64
	for i, name := range []string{"alice", "bob", "carol_admin", "dave"} {
		a, err := s.Create(NewAccount{Name: name, Hash: "$h"}, t0.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.UserID)
	}
	if err := s.SetRole(ids[2], RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRole(ids[2]+100, RoleAdmin); err != ErrNoAccount {
		t.Errorf("an unknown account: %v", err)
	}
	if err := s.SetRole(ids[0], Role(7)); err == nil {
		t.Error("role 7 taken")
	}
	c, _, _ := s.Credentials("CAROL_ADMIN")
	if c.Role != RoleAdmin {
		t.Errorf("credentials %+v", c)
	}
	admins, err := s.Admins()
	if err != nil || len(admins) != 1 || admins[0].Name != "carol_admin" || admins[0].Hash != "" {
		t.Errorf("admins %+v %v", admins, err)
	}

	names := func(q string) string {
		t.Helper()
		got, err := s.FindAccounts(q, 10)
		if err != nil {
			t.Fatal(err)
		}
		var n []string
		for _, a := range got {
			n = append(n, a.Name)
		}
		return strings.Join(n, ",")
	}
	if got := names(""); got != "dave,carol_admin,bob,alice" {
		t.Errorf("all: %s", got)
	}
	if got := names("A"); got != "dave,carol_admin,alice" { // login names, case-insensitively
		t.Errorf("a: %s", got)
	}
	if got := names(fmt.Sprint(ids[1])); got != "bob" {
		t.Errorf("by id: %s", got)
	}
	if got := names("%"); got != "" { // no wildcard
		t.Errorf("%%: %s", got)
	}
	// display names: rename bob's persona
	if _, err := s.Update(ids[1], func(a *Account) { a.Personas[0].Name = "Zorro" }); err != nil {
		t.Fatal(err)
	}
	if got := names("zor"); got != "bob" {
		t.Errorf("by display name: %s", got)
	}
	sum, found, err := s.Summary(ids[1])
	if err != nil || !found || sum.Name != "bob" || len(sum.Personas) != 1 || sum.Personas[0] != "Zorro" || !sum.HasHash ||
		sum.Status != StatusActive || sum.Role != RolePlayer || !sum.Created.Equal(t0.Add(time.Minute)) {
		t.Errorf("summary %+v %v %v", sum, found, err)
	}
	if _, found, _ := s.Summary(1); found {
		t.Error("summary of an unknown account")
	}
	if got, _ := s.FindAccounts("", 2); len(got) != 2 {
		t.Errorf("limit: %d", len(got))
	}

	// a deleted account loses its role
	if err := s.DeleteAccount(ids[2]); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := s.CredentialsByID(ids[2]); c.Role != RolePlayer || c.Status != StatusDeleted {
		t.Errorf("deleted admin %+v", c)
	}
}

func TestAuditAndSettings(t *testing.T) {
	s := New()
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		if err := s.Audit(AuditEntry{Time: t0.Add(time.Duration(i) * time.Second), Actor: "operator", Source: "command line",
			Action: "test", Target: fmt.Sprint(i), Detail: strings.Repeat("x", 600*i)}); err != nil {
			t.Fatal(err)
		}
	}
	log, err := s.AuditLog(3, 0)
	if err != nil || len(log) != 3 || log[0].Target != "4" || log[2].Target != "2" || !log[0].Time.Equal(t0.Add(4*time.Second)) {
		t.Fatalf("log %+v %v", log, err)
	}
	if len(log[0].Detail) > maxAuditField+len("…") {
		t.Errorf("detail of %d bytes kept", len(log[0].Detail))
	}
	older, _ := s.AuditLog(10, log[2].ID)
	if len(older) != 2 || older[0].Target != "1" {
		t.Errorf("older %+v", older)
	}

	if err := s.SetSetting("signup_mode", "invite", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("signup_mode", "closed", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	v, changed, found, err := s.Setting("signup_mode")
	if v != "closed" || !found || err != nil || !changed.Equal(t0.Add(time.Hour)) {
		t.Errorf("setting %q %v %v %v", v, changed, found, err)
	}
}

// the status check reads the statuses of the logged-in accounts in one query; an unknown id is missing.
func TestStatuses(t *testing.T) {
	s := New()
	a, _ := login(t, s, "a@x.io", time.Unix(1, 0))
	b, _ := login(t, s, "b@x.io", time.Unix(1, 0))
	if err := s.SetStatus(b.UserID, StatusBanned); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(a.UserID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Statuses([]int64{a.UserID, b.UserID, 42})
	if err != nil || len(got) != 2 || got[a.UserID] != StatusDeleted || got[b.UserID] != StatusBanned {
		t.Errorf("%v %v", got, err)
	}
	if got, err := s.Statuses(nil); err != nil || len(got) != 0 {
		t.Errorf("none: %v %v", got, err)
	}
}
