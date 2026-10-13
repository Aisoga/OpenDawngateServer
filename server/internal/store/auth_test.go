package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// a database of version 1 (with accounts) migrates to the current version; its accounts have no password
// yet and are active.
func TestMigrateFromVersion1(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0].sql); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied TEXT NOT NULL, note TEXT NOT NULL);
		INSERT INTO schema_version VALUES (1, '', 'v1')`); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	a := &Account{UserID: FirstUserID, Email: "Old@Example.com", Level: 1, Personas: []Persona{{ID: FirstPersonaID, Name: "Old"}}}
	if err := insertAccount(tx, a); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openT(t, path)
	if want := fmt.Sprintf("1 accounts, schema 1 -> %d", SchemaVersion()); s.Info() != want {
		t.Errorf("info %q, want %q", s.Info(), want)
	}
	c, found, err := s.Credentials(" old@example.COM ")
	if err != nil || !found || c.UserID != FirstUserID || c.Hash != "" || c.Status != StatusActive || c.Name != "Old@Example.com" {
		t.Errorf("credentials %+v %v %v", c, found, err)
	}
	if _, found, _ := s.Credentials("nobody"); found {
		t.Error("an unknown name found")
	}
}

func TestPasswordAndStatus(t *testing.T) {
	s := New()
	a, _ := login(t, s, "a@x.io", time.Unix(1, 0))
	if err := s.SetPassword(a.UserID, "$argon2id$x"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(a.UserID, StatusBanned); err != nil {
		t.Fatal(err)
	}
	c, _, _ := s.Credentials("A@X.IO")
	if c.Hash != "$argon2id$x" || c.Status != StatusBanned {
		t.Errorf("%+v", c)
	}
	if err := s.SetPassword(999, "h"); !errors.Is(err, ErrNoAccount) {
		t.Errorf("unknown account: %v", err)
	}
	if err := s.SetStatus(a.UserID, Status(9)); err == nil {
		t.Error("status 9 taken")
	}
	got, err := s.LoginUser(a.UserID, time.Unix(50, 0))
	if err != nil || !got.LastLogin.Equal(time.Unix(1, 0)) {
		t.Errorf("LoginUser: last login %v (%v), want the previous one", got.LastLogin, err)
	}
	if got, _ := s.Account(a.UserID); !got.LastLogin.Equal(time.Unix(50, 0)) {
		t.Errorf("last login not stored: %v", got.LastLogin)
	}
	for _, n := range []string{"active", "Disabled", " banned", "deleted"} {
		if _, err := ParseStatus(n); err != nil {
			t.Error(err)
		}
	}
	if _, err := ParseStatus("gone"); err == nil {
		t.Error("gone parsed")
	}
}

func TestCreate(t *testing.T) {
	s := New()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	a, err := s.Create(NewAccount{Name: " Alice ", Hash: "h1"}, now)
	if err != nil || a.Email != "Alice" || len(a.Personas) != 1 || a.Personas[0].Name != "Alice" || !a.Created.Equal(now) {
		t.Fatalf("%+v %v", a, err)
	}
	if c, _, _ := s.Credentials("alice"); c.Hash != "h1" {
		t.Errorf("hash %q", c.Hash)
	}
	if _, err := s.Create(NewAccount{Name: "ALICE", Hash: "h"}, now); !errors.Is(err, ErrExists) {
		t.Errorf("a taken name: %v", err)
	}
	// the cap per address and day; another address and the next day are free; a refusal stores nothing
	for i, name := range []string{"b1", "b2"} {
		if _, err := s.Create(NewAccount{Name: name, From: "203.0.113.5", Limit: 2}, now); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if _, err := s.Create(NewAccount{Name: "b3", From: "203.0.113.5", Limit: 2}, now); !errors.Is(err, ErrSignupLimit) {
		t.Errorf("third: %v", err)
	}
	if _, found, _ := s.Credentials("b3"); found {
		t.Error("a refused sign-up was stored")
	}
	if _, err := s.Create(NewAccount{Name: "c1", From: "203.0.113.6", Limit: 2}, now); err != nil {
		t.Errorf("another address: %v", err)
	}
	if _, err := s.Create(NewAccount{Name: "b3", From: "203.0.113.5", Limit: 2}, now.Add(24*time.Hour)); err != nil {
		t.Errorf("the next day: %v", err)
	}
	var rows int
	s.db.QueryRow(`SELECT COUNT(*) FROM signups`).Scan(&rows)
	if rows != 1 {
		t.Errorf("signups keeps %d rows, want only today's one", rows)
	}
}

func TestInvites(t *testing.T) {
	s := New()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	two, err := s.AddInvite("hash2", 2, time.Time{}, "for the club", now)
	if err != nil || two.ID == 0 || two.Left(now) != 2 {
		t.Fatalf("%+v %v", two, err)
	}
	soon, _ := s.AddInvite("hashSoon", 5, now.Add(time.Hour), "", now)
	revoked, _ := s.AddInvite("hashRevoked", 5, time.Time{}, "", now)
	if err := s.RevokeInvite(revoked.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeInvite(999); err == nil {
		t.Error("revoked an unknown code")
	}
	if _, err := s.AddInvite("hash0", 0, time.Time{}, "", now); err == nil {
		t.Error("an invite of 0 uses")
	}
	if _, err := s.AddInvite("hash2", 1, time.Time{}, "", now); err == nil {
		t.Error("the same code twice")
	}
	create := func(name, code string, at time.Time) error {
		_, err := s.Create(NewAccount{Name: name, InviteHash: code}, at)
		return err
	}
	for _, c := range []struct {
		name, code string
		at         time.Time
		want       string // "" = created
	}{
		{"u1", "hash2", now, ""},
		{"u2", "hash2", now, ""},
		{"u3", "hash2", now, "store: invite code: used up"},
		{"u4", "nohash", now, "store: invite code: unknown"},
		{"u5", "hashRevoked", now, "store: invite code: revoked"},
		{"u6", "hashSoon", now.Add(30 * time.Minute), ""},
		{"u7", "hashSoon", now.Add(time.Hour), "store: invite code: expired"},
	} {
		err := create(c.name, c.code, c.at)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || err.Error() != c.want || !errors.Is(err, ErrInvite)):
			t.Errorf("%s: %v, want %s", c.name, err, c.want)
		}
		if _, found, _ := s.Credentials(c.name); found != (c.want == "") {
			t.Errorf("%s: stored %v", c.name, found)
		}
	}
	all, err := s.Invites()
	if err != nil || len(all) != 3 {
		t.Fatalf("%d invites (%v)", len(all), err)
	}
	if all[0].Used != 2 || all[0].Left(now) != 0 || all[0].Note != "for the club" || !all[0].Created.Equal(now) {
		t.Errorf("first: %+v", all[0])
	}
	if all[1].Used != 1 || !all[1].Expires.Equal(soon.Expires) || all[1].Left(now) != 4 || all[1].Left(now.Add(time.Hour)) != 0 {
		t.Errorf("second: %+v", all[1])
	}
	if !all[2].Revoked || all[2].Left(now) != 0 {
		t.Errorf("third: %+v", all[2])
	}
}
