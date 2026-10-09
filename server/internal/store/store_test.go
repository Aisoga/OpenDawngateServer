package store

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// login is Store.Login for tests: a save error fails the test.
func login(t *testing.T, s *Store, email string, now time.Time) (Account, bool) {
	t.Helper()
	a, first, err := s.Login(email, now)
	if err != nil {
		t.Fatal(err)
	}
	return a, first
}

func TestLoginCreatesAccountOnTheFly(t *testing.T) {
	s := New()
	t0 := time.Unix(1791405059, 0)
	a, first := login(t, s, "Player@Example.com", t0)
	if !first || a.UserID != FirstUserID || a.Email != "Player@Example.com" || !a.LastLogin.IsZero() || a.Level != 1 {
		t.Fatalf("first login: %+v first=%v", a, first)
	}
	if len(a.Personas) != 1 || a.Personas[0] != (Persona{ID: FirstPersonaID, Name: "Player"}) {
		t.Fatalf("personas: %+v", a.Personas)
	}
	// same account again, case-insensitively; LastLogin is the previous login
	t1 := t0.Add(time.Minute)
	b, first := login(t, s, " player@example.COM ", t1)
	if first || b.UserID != a.UserID || !b.LastLogin.Equal(t0) {
		t.Fatalf("second login: %+v first=%v", b, first)
	}
	// a second account gets the next ids and a unique persona name
	c, first := login(t, s, "player@other.example", t1)
	if !first || c.UserID != FirstUserID+1 || c.Personas[0] != (Persona{ID: FirstPersonaID + 1, Name: "player2"}) {
		t.Fatalf("other account: %+v", c)
	}
	if p, ok := c.Persona("PLAYER2"); !ok || p.ID != FirstPersonaID+1 {
		t.Errorf("Persona lookup: %+v %v", p, ok)
	}
	if _, ok := c.Persona("player"); ok {
		t.Errorf("Persona found another account's name")
	}
	if acct, p, ok := s.PersonaByID(FirstPersonaID + 1); !ok || acct.UserID != c.UserID || p.Name != "player2" {
		t.Errorf("PersonaByID: %+v %+v %v", acct, p, ok)
	}
	if _, _, ok := s.PersonaByID(42); ok {
		t.Errorf("PersonaByID found an unknown id")
	}
	if acct, ok := s.Account(c.UserID); !ok || acct.Email != "player@other.example" {
		t.Errorf("Account: %+v %v", acct, ok)
	}
	if _, ok := s.Account(42); ok {
		t.Errorf("Account found an unknown id")
	}
}

func TestPersonaName(t *testing.T) {
	for in, want := range map[string]string{
		"player@example.com":              "player",
		"Mr.Smith+tag@example.com":        "MrSmithtag",
		"averyveryverylongname@x":         "averyveryverylon",
		"@example.com":                    "Player",
		"äöü@example.com":                 "Player",
		"under_score-dash@x":              "under_score-dash",
		"  spaced@example.com  ":          "spaced",
		"no-at-sign":                      "no-at-sign",
		"xäy@example.com":                 "xy",
		"1234567890123456789@example.com": "1234567890123456",
	} {
		if got := PersonaName(in); got != want {
			t.Errorf("PersonaName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueNameKeepsLength(t *testing.T) {
	s := New()
	now := time.Unix(0, 0)
	login(t, s, "averyveryverylongname@a", now)
	a, _ := login(t, s, "averyveryverylongname@b", now)
	if got := a.Personas[0].Name; got != "averyveryverylo2" {
		t.Errorf("got %q", got)
	}
}

// Update changes the stored account and returns copies the caller can't change it through.
func TestUpdateAndCopies(t *testing.T) {
	s := New()
	a, _ := login(t, s, "player@example.com", time.Unix(0, 0))
	got, err := s.Update(a.UserID, func(a *Account) {
		a.Items[0x10000001] = 1
		a.Settings["k"] = "v"
		a.Stats["PlayerStats"] = map[string]string{"WINS": "3"}
		a.EC = 500
		a.AddPage()
		a.Page(1).Shapes = []PerkShape{{ID: 6, Gems: []PerkGem{{ID: 7}}}}
		a.History = History{Shapers: map[uint32]GamesWon{1: {Games: 1}},
			Days: []HistoryDay{{Day: "2026-10-08", Shapers: map[uint32]ShaperDay{1: {Games: 1}}, Roles: map[uint8]GamesWon{1: {Games: 1}}}}}
		a.Lists = Lists{Friends: []ListMember{{ID: 5, Name: "five"}}, Recent: []ListMember{{ID: 6}}, Blocked: []ListMember{{ID: 7}}}
	})
	if err != nil {
		t.Fatal(err)
	}
	got.Lists.Friends[0].Name = "changed"
	got.Lists.Recent[0].ID = 99
	got.Lists.Blocked[0].ID = 99
	if again, _ := s.Account(a.UserID); again.Lists.Friends[0].Name != "five" || again.Lists.Recent[0].ID != 6 ||
		again.Lists.Blocked[0].ID != 7 || IndexOf(again.Lists.Friends, 5) != 0 || IndexOf(again.Lists.Friends, 6) != -1 {
		t.Errorf("stored lists changed through a copy: %+v", again.Lists)
	}
	got.History.Shapers[1] = GamesWon{Games: 99}
	got.History.Days[0].Shapers[1] = ShaperDay{Games: 99}
	got.History.Days[0].Roles[1] = GamesWon{Games: 99}
	got.History.Days[0].Day = "changed"
	if again, _ := s.Account(a.UserID); again.History.Shapers[1].Games != 1 || again.History.Days[0].Shapers[1].Games != 1 ||
		again.History.Days[0].Roles[1].Games != 1 || again.History.Days[0].Day != "2026-10-08" {
		t.Errorf("stored history changed through a copy: %+v", again.History)
	}
	got.Items[0x10000001] = 99
	got.Settings["k"] = "changed"
	got.Stats["PlayerStats"]["WINS"] = "99"
	got.Personas[0].Name = "changed"
	got.PerkPages[0].Shapes[0].Gems[0].ID = 99
	got.PerkPages[0].Name = "changed"
	again, _ := s.Account(a.UserID)
	if again.Items[0x10000001] != 1 || again.Settings["k"] != "v" || again.Stats["PlayerStats"]["WINS"] != "3" ||
		again.Personas[0].Name != "player" || again.EC != 500 || again.PerkPages[0].Name != "Page 1" ||
		again.PerkPages[0].Shapes[0].Gems[0].ID != 7 {
		t.Errorf("stored account changed through a copy: %+v", again)
	}
	if _, err := s.Update(42, func(*Account) {}); err != ErrNoAccount {
		t.Errorf("Update of an unknown id: %v", err)
	}
}

// UpdatePair changes two accounts at once (B13b: a friendship on both sides) and saves once.
func TestUpdatePair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	a, _ := login(t, s, "a@example.com", t0)
	b, _ := login(t, s, "b@example.com", t0)
	ga, gb, err := s.UpdatePair(a.UserID, b.UserID, func(x, y *Account) {
		x.Lists.Friends = append(x.Lists.Friends, ListMember{ID: y.Personas[0].ID, Name: y.Personas[0].Name, Added: t0})
		y.Lists.Friends = append(y.Lists.Friends, ListMember{ID: x.Personas[0].ID, Name: x.Personas[0].Name, Added: t0})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ga.Lists.Friends) != 1 || ga.Lists.Friends[0].Name != "b" || len(gb.Lists.Friends) != 1 || gb.Lists.Friends[0].Name != "a" {
		t.Fatalf("pair: %+v / %+v", ga.Lists, gb.Lists)
	}
	ga.Lists.Friends[0].Name = "changed"
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := r.Account(a.UserID); len(again.Lists.Friends) != 1 || again.Lists.Friends[0].Name != "b" ||
		!again.Lists.Friends[0].Added.Equal(t0) {
		t.Errorf("after reopening: %+v", again.Lists)
	}
	if _, _, err := s.UpdatePair(a.UserID, 42, func(x, y *Account) { t.Error("fn ran for an unknown id") }); err != ErrNoAccount {
		t.Errorf("UpdatePair with an unknown id: %v", err)
	}
	if x, y, err := s.UpdatePair(a.UserID, a.UserID, func(x, y *Account) {
		if x != y {
			t.Error("the same id gave two accounts")
		}
	}); err != nil || x.UserID != y.UserID {
		t.Errorf("UpdatePair with the same id: %v", err)
	}
}

// Everything survives a restart: accounts, personas, id counters and player content.
func TestPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "accounts.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	a, _ := login(t, s, "player@example.com", t0)
	login(t, s, "other@example.com", t0)
	login(t, s, "player@example.com", t0.Add(time.Hour)) // LastLogin
	want, err := s.Update(a.UserID, func(a *Account) {
		a.Granted = true
		a.Flags |= FlagFTUEDone
		a.Avatar = 0x1000000A
		a.EC, a.PC, a.CC = 10000, 1000, 7
		a.Items[0x10000001] = 1
		a.Items[0x10000006] = 2
		a.Settings["Audio.Master"] = "0.5"
		a.UserInfo = 1 << 62
		a.Stats["PlayerStats"] = map[string]string{"WINS": "1"}
		// B7c: perk pages, the commerce profile, the first win of the day
		a.AddPage()
		if p := a.AddPage(); p.ID != 2 {
			t.Errorf("second page id %d", p.ID)
		}
		a.PerkPages[1].Shapes = []PerkShape{{ID: 0x10000006, Cell: 5, Rotation: 1,
			Gems: []PerkGem{{ID: 0x10000007, Socket: 0}}}}
		a.PerkPages[1].Version = 3
		a.Commerce = Commerce{SpentEC: 650, SpentPC: 300, CountEC: 2, CountPC: 1}
		a.FirstWinDay = "2026-10-08"
		// B7d: the shaper and role history
		a.History = History{Shapers: map[uint32]GamesWon{0x10000001: {Games: 2, Wins: 1}},
			Days: []HistoryDay{{Day: "2026-10-06", Shapers: map[uint32]ShaperDay{0x10000001: {Games: 1, Wins: 1, Kills: 3,
				Deaths: 1, Assists: 2, Creeps: 40, Gold: 5000, Seconds: 720}}, Roles: map[uint8]GamesWon{1: {Games: 1, Wins: 1}}},
				{Day: "2026-10-08", Roles: map[uint8]GamesWon{3: {Games: 1}}}}}
		// B13b: the association lists
		a.Lists = Lists{Friends: []ListMember{{ID: FirstPersonaID + 1, Name: "other", Added: t0}},
			Recent:  []ListMember{{ID: FirstPersonaID + 1, Name: "other", Added: t0.Add(time.Minute)}},
			Blocked: []ListMember{{ID: 42, Name: "pest", Added: t0}}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(want.PerkPages) != 2 || want.PerkPages[0].Name != "Page 1" || want.PerkPages[1].ID != 2 ||
		want.PerkPages[1].Name != "Page 2" || want.PerkPages[1].Status != 1 {
		t.Errorf("pages: %+v", want.PerkPages)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no file after a change: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := r.Account(a.UserID)
	if !ok {
		t.Fatal("account lost")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after reopening:\n got %+v\nwant %+v", got, want)
	}
	if !got.LastLogin.Equal(t0.Add(time.Hour)) {
		t.Errorf("LastLogin %v", got.LastLogin)
	}
	// the id counters continue, and the persona names stay taken
	c, first := login(t, r, "third@example.com", t0)
	if !first || c.UserID != FirstUserID+2 || c.Personas[0].ID != FirstPersonaID+2 {
		t.Errorf("new account after reopening: %+v", c)
	}
	d, _ := login(t, r, "player@elsewhere.example", t0)
	if d.Personas[0].Name != "player2" {
		t.Errorf("persona name %q", d.Personas[0].Name)
	}
	if b, first := login(t, r, "OTHER@example.com", t0); first || b.UserID != FirstUserID+1 {
		t.Errorf("existing account after reopening: %+v first=%v", b, first)
	}
}

// A file that doesn't parse stops Open and stays as it is.
func TestOpenRefusesBadFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"broken": `{"format": "dawngate-accounts-1", "accounts": [`,
		"format": `{"format": "other", "accounts": []}`,
		"dup": `{"format": "dawngate-accounts-1", "accounts": [{"userID": 1, "email": "a@x"},
			{"userID": 2, "email": "A@x"}]}`,
	} {
		path := filepath.Join(dir, name+".json")
		os.WriteFile(path, []byte(body), 0o644)
		if _, err := Open(path); err == nil {
			t.Errorf("%s: Open accepted it", name)
		}
		if raw, _ := os.ReadFile(path); string(raw) != body {
			t.Errorf("%s: file changed", name)
		}
	}
}

// Many connections at once: every login and update lands, and the file holds them all.
func TestConcurrentUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, _, err := s.Login(fmt.Sprintf("p%d@example.com", i), time.Unix(int64(i), 0))
			if err != nil {
				errs <- err
				return
			}
			for j := 0; j < 5; j++ {
				if _, err := s.Update(a.UserID, func(a *Account) { a.EC += 10 }); err != nil {
					errs <- err
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for i := 0; i < n; i++ {
		a, first, _ := r.Login(fmt.Sprintf("P%d@example.com", i), time.Unix(0, 0))
		if first || a.EC != 50 || ids[a.UserID] {
			t.Errorf("account %d after reopening: %+v first=%v", i, a, first)
		}
		ids[a.UserID] = true
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"format": "dawngate-accounts-1"`) {
		t.Errorf("file:\n%s", raw)
	}
}

// A store without a file never writes one.
func TestMemoryStoreWritesNothing(t *testing.T) {
	s := New()
	if s.Path() != "" {
		t.Errorf("path %q", s.Path())
	}
	if _, _, err := s.Login("player@example.com", time.Unix(0, 0)); err != nil {
		t.Error(err)
	}
}
