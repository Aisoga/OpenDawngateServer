package store

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// legacyGolden is a JSON store in the shape the last JSON build (B13b) wrote: every field of Account, nil and
// empty collections, times with and without an offset, the zero time, uint64 values with the top bit set.
const legacyGolden = "../../testdata/accounts_b13b.json"

// normalized returns a's content in the shape load returns it: Items, Settings and Stats allocated, a stat group
// without values dropped (it has no rows), every other empty collection nil.
func normalized(a Account) Account {
	c := copyAccount(&a)
	if c.Items == nil {
		c.Items = map[uint32]uint32{}
	}
	if c.Settings == nil {
		c.Settings = map[string]string{}
	}
	if c.Stats == nil {
		c.Stats = map[string]map[string]string{}
	}
	maps.DeleteFunc(c.Stats, func(_ string, m map[string]string) bool { return len(m) == 0 })
	if len(c.Personas) == 0 {
		c.Personas = nil
	}
	if len(c.PerkPages) == 0 {
		c.PerkPages = nil
	}
	for i := range c.PerkPages {
		if len(c.PerkPages[i].Shapes) == 0 {
			c.PerkPages[i].Shapes = nil
		}
		for j := range c.PerkPages[i].Shapes {
			if len(c.PerkPages[i].Shapes[j].Gems) == 0 {
				c.PerkPages[i].Shapes[j].Gems = nil
			}
		}
	}
	if len(c.History.Shapers) == 0 {
		c.History.Shapers = nil
	}
	if len(c.History.Days) == 0 {
		c.History.Days = nil
	}
	for i := range c.History.Days {
		if len(c.History.Days[i].Shapers) == 0 {
			c.History.Days[i].Shapers = nil
		}
		if len(c.History.Days[i].Roles) == 0 {
			c.History.Days[i].Roles = nil
		}
	}
	for _, l := range []*[]ListMember{&c.Lists.Friends, &c.Lists.Recent, &c.Lists.Blocked} {
		if len(*l) == 0 {
			*l = nil
		}
	}
	return c
}

// checkImport imports the JSON store src into a fresh database beside a copy of it and checks that every account
// reads back equal, that the id counters continue and that the file was renamed.
func checkImport(t *testing.T, src string) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	want, err := readLegacy(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	legacy := filepath.Join(dir, LegacyFile)
	if err := os.WriteFile(legacy, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	s := openT(t, filepath.Join(dir, FileName))
	if n, _ := s.Count(); n != len(want.Accounts) {
		t.Fatalf("%d accounts imported, want %d", n, len(want.Accounts))
	}
	if !strings.Contains(s.Info(), "imported from accounts.json (renamed accounts.json.imported)") {
		t.Errorf("info %q", s.Info())
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("%s still there", legacy)
	}
	if got, _ := os.ReadFile(legacy + ImportedSuffix); string(got) != string(raw) {
		t.Errorf("the imported file wasn't kept as it was")
	}
	nextUser, nextPersona := want.NextUserID, want.NextPersonaID
	for _, w := range want.Accounts {
		got, ok := s.Account(w.UserID)
		if !ok {
			t.Errorf("account %d (%s) missing", w.UserID, w.Email)
			continue
		}
		if g, n := normalized(got), normalized(w); !reflect.DeepEqual(g, n) {
			t.Errorf("account %d (%s) differs:\n got %+v\nwant %+v", w.UserID, w.Email, g, n)
		}
		for _, p := range w.Personas {
			if a, q, ok := s.PersonaByID(p.ID); !ok || a.UserID != w.UserID || q != p {
				t.Errorf("PersonaByID(%d): %v %+v", p.ID, ok, q)
			}
			nextPersona = max(nextPersona, p.ID+1)
		}
		nextUser = max(nextUser, w.UserID+1)
	}
	// the counters go on where the file's ended
	a, first, err := s.Login("brand-new-account@example.com", time.Unix(1791405059, 0))
	if err != nil || !first || a.UserID != max(nextUser, FirstUserID) || a.Personas[0].ID != max(nextPersona, FirstPersonaID) {
		t.Errorf("a new account after the import: %+v first=%v err=%v (want user %d, persona %d)", a, first, err, nextUser, nextPersona)
	}
}

// The import of the JSON store gives equal accounts (the done-when), here for a store in the last JSON build's shape.
func TestImportLegacy(t *testing.T) {
	checkImport(t, legacyGolden)
}

// DGSTORE_IMPORT_CHECK names real accounts.json files (a copy of a lane's, separated by ';') to import the same way;
// the test is skipped without it. It ran on copies of the test lanes' files.
func TestImportRealFiles(t *testing.T) {
	files := os.Getenv("DGSTORE_IMPORT_CHECK")
	if files == "" {
		t.Skip("DGSTORE_IMPORT_CHECK not set")
	}
	for _, f := range strings.Split(files, ";") {
		t.Run(filepath.Base(filepath.Dir(f)), func(t *testing.T) { checkImport(t, f) })
	}
}

// Imported accounts behave as before: Login finds them case-insensitively, Update changes and keeps them, the
// persona names stay taken.
func TestImportedAccountsLive(t *testing.T) {
	dir := t.TempDir()
	raw, _ := os.ReadFile(legacyGolden)
	os.WriteFile(filepath.Join(dir, LegacyFile), raw, 0o644)
	s := openT(t, filepath.Join(dir, FileName))
	t1 := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	a, first := login(t, s, "player@EXAMPLE.com", t1)
	if first || a.UserID != 1000000001 || a.EC != 8025 || !a.LastLogin.Equal(time.Date(2026, 10, 12, 16, 49, 1, 5e8, time.UTC)) {
		t.Fatalf("login of an imported account: %+v first=%v", a, first)
	}
	if _, err := s.Update(a.UserID, func(a *Account) { a.EC += 5; a.Items[1] = 1; a.PerkPages[1].Name = "renamed" }); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Account(a.UserID)
	if b.EC != 8030 || b.Items[1] != 1 || b.Items[281832551] != 12 || b.PerkPages[1].Name != "renamed" ||
		len(b.PerkPages[0].Shapes[0].Gems) != 2 || !b.LastLogin.Equal(t1) || len(b.Lists.Friends) != 2 {
		t.Errorf("after an update: %+v", b)
	}
	c, _ := login(t, s, "second_name-16ch@elsewhere", t1)
	if c.Personas[0].Name != "second_name-16c2" {
		t.Errorf("persona name %q (an imported second persona's name is taken)", c.Personas[0].Name)
	}
	if acct, p, ok := s.PersonaByName("SECOND_NAME-16CH"); !ok || acct.UserID != 1000000004 || p.ID != 2000000005 {
		t.Errorf("PersonaByName: %+v %v", p, ok)
	}
}

// A JSON store that doesn't parse, has another format or an email twice stops Open and stays as it is; the next
// start tries again (the database stays empty).
func TestImportRefusesBadFiles(t *testing.T) {
	for name, body := range map[string]string{
		"broken": `{"format": "dawngate-accounts-1", "accounts": [`,
		"format": `{"format": "other", "accounts": []}`,
		"dup": `{"format": "dawngate-accounts-1", "accounts": [{"userID": 1, "email": "a@x"},
			{"userID": 2, "email": "A@x"}]}`,
		"dupid": `{"format": "dawngate-accounts-1", "accounts": [{"userID": 1, "email": "a@x"},
			{"userID": 1, "email": "b@x"}]}`,
	} {
		dir := t.TempDir()
		legacy := filepath.Join(dir, LegacyFile)
		os.WriteFile(legacy, []byte(body), 0o644)
		if s, err := Open(filepath.Join(dir, FileName)); err == nil {
			s.Close()
			t.Errorf("%s: Open accepted it", name)
		} else if !strings.Contains(err.Error(), legacy) {
			t.Errorf("%s: the error doesn't name the file: %v", name, err)
		}
		if raw, _ := os.ReadFile(legacy); string(raw) != body {
			t.Errorf("%s: file changed", name)
		}
		// fixed, it imports
		os.WriteFile(legacy, []byte(`{"format": "dawngate-accounts-1", "accounts": [{"userID": 1000000007, "email": "a@x",
			"personas": [{"id": 2000000003, "name": "a"}]}]}`), 0o644)
		s := openT(t, filepath.Join(dir, FileName))
		if a, first := login(t, s, "A@X", time.Unix(0, 0)); first || a.UserID != 1000000007 {
			t.Errorf("%s: after fixing: %+v", name, a)
		}
		if a, _ := login(t, s, "new@x", time.Unix(0, 0)); a.UserID != 1000000008 || a.Personas[0].ID != 2000000004 {
			t.Errorf("%s: a new account: %+v", name, a)
		}
	}
}

// accounts.json beside a database that has accounts is left alone (no second import, no merge); a second file to
// import while accounts.json.imported exists gets the next free name.
func TestImportOnlyIntoAnEmptyDatabase(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, FileName)
	legacy := filepath.Join(dir, LegacyFile)
	os.WriteFile(legacy+ImportedSuffix, []byte("an earlier import"), 0o644)
	raw, _ := os.ReadFile(legacyGolden)
	os.WriteFile(legacy, raw, 0o644)
	s := openT(t, db)
	if !strings.Contains(s.Info(), "(renamed accounts.json.imported.2)") {
		t.Errorf("info %q", s.Info())
	}
	if got, _ := os.ReadFile(legacy + ImportedSuffix); string(got) != "an earlier import" {
		t.Errorf("an earlier import's file was overwritten")
	}
	s.Close()
	os.WriteFile(legacy, []byte(`{"format": "dawngate-accounts-1", "accounts": [{"userID": 1, "email": "z@x"}]}`), 0o644)
	r := openT(t, db)
	if n, _ := r.Count(); n != 3 || !strings.Contains(r.Info(), "NOT imported") {
		t.Errorf("%d accounts, info %q", n, r.Info())
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("accounts.json was moved: %v", err)
	}
}

// A file that isn't an SQLite database, or a database of a newer schema, stops Open and isn't changed.
func TestOpenRefusesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.db")
	body := strings.Repeat("not a database ", 300)
	os.WriteFile(junk, []byte(body), 0o644)
	if s, err := Open(junk); err == nil {
		s.Close()
		t.Error("Open accepted a file that isn't a database")
	}
	if raw, _ := os.ReadFile(junk); string(raw) != body {
		t.Error("the file changed")
	}

	newer := filepath.Join(dir, "newer.db")
	s := openT(t, newer)
	login(t, s, "a@x", time.Unix(0, 0))
	if _, err := s.db.Exec(`INSERT INTO schema_version (version, applied, note) VALUES (?, '', 'from the future')`,
		SchemaVersion()+1); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if r, err := Open(newer); err == nil || !strings.Contains(err.Error(), "newer") {
		if r != nil {
			r.Close()
		}
		t.Errorf("a newer schema: %v", err)
	}
}

// The database is in WAL mode with foreign keys on, and schema_version holds every migration once.
func TestSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s := openT(t, path)
	if s.Info() != fmt.Sprintf("new database (schema %d)", SchemaVersion()) {
		t.Errorf("info %q", s.Info())
	}
	var mode string
	var fk int
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode %q %v", mode, err)
	}
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys %d %v", fk, err)
	}
	s.Close()
	r := openT(t, path) // migrating again does nothing
	var n, v int
	if err := r.db.QueryRow(`SELECT COUNT(*), MAX(version) FROM schema_version`).Scan(&n, &v); err != nil || n != len(migrations) ||
		v != SchemaVersion() {
		t.Errorf("schema_version: %d rows, version %d (%v)", n, v, err)
	}
	// deleting an account removes all its rows (ON DELETE CASCADE: the account deletion relies on it)
	a, _ := login(t, r, "a@x", time.Unix(0, 0))
	r.Update(a.UserID, func(a *Account) {
		a.Items[1], a.Settings["k"], a.Stats["g"] = 1, "v", map[string]string{"n": "1"}
		a.AddPage()
		a.History = History{Shapers: map[uint32]GamesWon{1: {1, 1}}, Days: []HistoryDay{{Day: "d", Roles: map[uint8]GamesWon{1: {1, 0}}}}}
		a.Lists.Friends = []ListMember{{ID: 5}}
	})
	if _, err := r.db.Exec(`DELETE FROM accounts WHERE user_id = ?`, a.UserID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"personas", "items", "settings", "stats", "perk_pages", "history_shapers", "history_days",
		"history_day_shapers", "history_day_roles", "list_members"} {
		var rows int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&rows); err != nil || rows != 0 {
			t.Errorf("%s: %d rows left (%v)", table, rows, err)
		}
	}
}

// A change that can't be stored leaves both accounts as they were (one transaction).
func TestFailedUpdateChangesNothing(t *testing.T) {
	s := New()
	a, _ := login(t, s, "a@x", time.Unix(0, 0))
	b, _ := login(t, s, "b@x", time.Unix(0, 0))
	_, _, err := s.UpdatePair(a.UserID, b.UserID, func(x, y *Account) {
		x.EC = 100
		x.Lists.Friends = []ListMember{{ID: y.Personas[0].ID}}
		y.EC = 200
		y.Email = "A@X" // a's email: refused by the unique key
	})
	if err == nil {
		t.Fatal("UpdatePair stored a duplicate email")
	}
	for _, id := range []int64{a.UserID, b.UserID} {
		if acct, _ := s.Account(id); acct.EC != 0 || len(acct.Lists.Friends) != 0 || acct.Email != map[int64]string{a.UserID: "a@x", b.UserID: "b@x"}[id] {
			t.Errorf("account %d changed: %+v", id, acct)
		}
	}
	// the user id can't be changed through Update
	got, err := s.Update(a.UserID, func(x *Account) { x.UserID = 7; x.Level = 9 })
	if err != nil || got.UserID != a.UserID {
		t.Errorf("Update changed the user id: %+v %v", got, err)
	}
	if acct, ok := s.Account(a.UserID); !ok || acct.Level != 9 {
		t.Errorf("after the update: %+v", acct)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&n)
	if n != 2 {
		t.Errorf("%d accounts", n)
	}
}
