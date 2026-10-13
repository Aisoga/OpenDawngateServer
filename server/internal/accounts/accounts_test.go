package accounts

import (
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/store"
)

var (
	t0       = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	loopback = netip.MustParseAddr("127.0.0.1")
	lan      = netip.MustParseAddr("192.168.1.20")
	public1  = netip.MustParseAddr("203.0.113.5")
	public2  = netip.MustParseAddr("198.51.100.7")
)

func TestHash(t *testing.T) {
	h, err := Hash("dawngate1")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`).MatchString(h) {
		t.Errorf("not a PHC argon2id string: %s", h)
	}
	if !Verify(h, "dawngate1") || Verify(h, "dawngate2") || Verify(h, "") {
		t.Error("Verify")
	}
	if h2, _ := Hash("dawngate1"); h2 == h {
		t.Error("the same salt twice")
	}
	if NeedsRehash(h) {
		t.Error("a current hash needs a rehash")
	}
	old, err := hashWith("dawngate1", Params{Memory: 64, Time: 1, Threads: 1})
	if err != nil || !Verify(old, "dawngate1") || !NeedsRehash(old) {
		t.Errorf("a hash with other parameters: %s %v", old, err)
	}
	// the reference implementation's test vector (RFC 9106 differs in its secret/AD; this is phc-winner-argon2's):
	// argon2id, m=65536, t=2, p=1, password "password", salt "somesalt"
	ref := "$argon2id$v=19$m=65536,t=2,p=1$c29tZXNhbHQ$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc"
	if !Verify(ref, "password") || Verify(ref, "Password") {
		t.Error("the reference vector doesn't verify")
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=65536,t=2,p=1$c29tZXNhbHQ$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc",
		"$argon2id$v=19$m=65536,t=2$c29tZXNhbHQ$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc",
		"$argon2id$v=19$m=999999999,t=2,p=1$c29tZXNhbHQ$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc",
		"$argon2id$v=19$m=65536,t=2,p=1$!!$CTFhFdXPJO1aFaMaO6Mm5c8y7cJHAph8ArZWb2GRPPc"} {
		if Verify(bad, "password") {
			t.Errorf("%q verified", bad)
		}
		if !NeedsRehash(bad) {
			t.Errorf("%q needs no rehash", bad)
		}
	}
}

func TestRules(t *testing.T) {
	for name, want := range map[string]uint16{
		"abc": 0, "player@example.com": 0, "A.b_c-d@e": 0, strings.Repeat("x", 32): 0,
		"ab": CodeTooShort, "": CodeTooShort, strings.Repeat("x", 33): CodeTooLong,
		"Zoë": CodeInvalidName, "two words": CodeInvalidName, "a+b@x.io": CodeInvalidName, "semi;colon": CodeInvalidName,
	} {
		if got := CheckName(name); got != want {
			t.Errorf("CheckName(%q) = %#x, want %#x", name, got, want)
		}
	}
	for pw, want := range map[string]uint16{
		"dawngate1": 0, "12345678": 0, "secret 1": 0, "ëëëëëëëë": 0, strings.Repeat("p", 63): 0,
		"": CodeInvalidPassword, "secret1": CodeTooShort, "ëëëëëëë": CodeTooShort,
		strings.Repeat("p", 64): CodeTooLong, strings.Repeat("ë", 32): CodeTooLong, // 64 bytes
	} {
		if got := CheckPassword(pw); got != want {
			t.Errorf("CheckPassword(%q) = %#x, want %#x", pw, got, want)
		}
	}
	for s, want := range map[string]Mode{"open": Open, " Invite": Invite, "CLOSED": Closed} {
		if m, err := ParseMode(s); err != nil || m != want {
			t.Errorf("ParseMode(%q) = %v %v", s, m, err)
		}
	}
	if _, err := ParseMode("public"); err == nil {
		t.Error("ParseMode(public)")
	}
	for a, want := range map[netip.Addr]bool{loopback: false, lan: false, netip.MustParseAddr("10.1.2.3"): false,
		netip.MustParseAddr("169.254.1.1"): false, netip.MustParseAddr("::1"): false, netip.MustParseAddr("fd00::1"): false,
		netip.MustParseAddr("::ffff:127.0.0.1"): false, public1: true, netip.MustParseAddr("2001:db8::1"): true, {}: true} {
		if Counted(a) != want {
			t.Errorf("Counted(%v) = %v", a, !want)
		}
	}
}

// check runs one login and compares its error code (0 = logged in).
func check(t *testing.T, p Policy, st *store.Store, name, pw string, from netip.Addr, want uint16) Result {
	t.Helper()
	r := p.Login(st, name, pw, from, t0)
	if r.Code != want {
		t.Errorf("login %q/%q from %v: code %#04x (%s), want %#04x", name, pw, from, r.Code, r.Note, want)
	}
	if strings.Contains(r.Note, pw) && pw != "" {
		t.Errorf("the note holds the password: %s", r.Note)
	}
	return r
}

func TestLoginOpen(t *testing.T) {
	st := store.New()
	p := Policy{Mode: Open}
	r := check(t, p, st, " Player@Example.com ", "dawngate1", loopback, 0)
	if !r.First || r.Account.Email != "Player@Example.com" || r.Account.Personas[0].Name != "Player" {
		t.Errorf("first login: %+v", r)
	}
	if c, _, _ := st.Credentials("player@example.com"); !Verify(c.Hash, "dawngate1") {
		t.Error("no hash stored")
	}
	r = check(t, p, st, "PLAYER@example.com", "dawngate1", loopback, 0)
	if r.First || r.Account.UserID != store.FirstUserID {
		t.Errorf("second login: %+v", r)
	}
	check(t, p, st, "player@example.com", "dawngate2", loopback, CodeInvalidPassword)
	check(t, p, st, "player@example.com", "", loopback, CodeInvalidPassword) // the client's retry
	check(t, p, st, "player@example.com", strings.Repeat("p", 64), loopback, CodeTooLong)
	// new names: the rules
	check(t, p, st, "ab", "dawngate1", loopback, CodeTooShort)
	check(t, p, st, strings.Repeat("n", 33), "dawngate1", loopback, CodeTooLong)
	check(t, p, st, strings.Repeat("n", MaxInput+1), "dawngate1", loopback, CodeTooLong)
	check(t, p, st, "Zoë Smith", "dawngate1", loopback, CodeInvalidName)
	check(t, p, st, "newname", "short", loopback, CodeTooShort)
	check(t, p, st, "newname", "", loopback, CodeInvalidPassword)
	if n, _ := st.Count(); n != 1 {
		t.Errorf("%d accounts after the refusals, want 1", n)
	}
}

// Accounts made by an earlier build (no hash): under open the next login's password becomes theirs; under invite and closed
// they wait for the operator. A name that breaks today's rules still logs in.
func TestLoginLegacyAccounts(t *testing.T) {
	st := store.New()
	st.Login("Zoë Smith", t0)
	st.Login("old@example.com", t0)
	st.Login("closed@example.com", t0)
	check(t, Policy{Mode: Invite}, st, "old@example.com", "dawngate1", loopback, CodeInvalidPassword)
	check(t, Policy{Mode: Closed}, st, "old@example.com", "dawngate1", loopback, CodeInvalidPassword)
	open := Policy{Mode: Open}
	check(t, open, st, "old@example.com", "short", loopback, CodeTooShort)
	r := check(t, open, st, "old@example.com", "dawngate1", loopback, 0)
	if r.First || !strings.Contains(r.Note, "had no password") {
		t.Errorf("%+v", r)
	}
	check(t, open, st, "old@example.com", "something", loopback, CodeInvalidPassword)
	check(t, Policy{Mode: Closed}, st, "old@example.com", "dawngate1", loopback, 0) // now it has one
	check(t, open, st, "zoë smith", "secret 12", loopback, 0)
	check(t, open, st, "Zoë Smith", "secret 12", loopback, 0)
	// the operator's reset gives a closed server's legacy account its password
	c, pw, err := ResetPassword(st, "closed@example.com")
	if err != nil || c.UserID == 0 || len(pw) != 16 {
		t.Fatalf("%+v %q %v", c, pw, err)
	}
	check(t, Policy{Mode: Closed}, st, "closed@example.com", pw, loopback, 0)
}

func TestLoginInviteAndClosed(t *testing.T) {
	for _, m := range []Mode{Invite, Closed} {
		st := store.New()
		p := Policy{Mode: m}
		check(t, p, st, "newplayer", "dawngate1", public1, CodeInvalidUser)
		check(t, p, st, "x", "dawngate1", public1, CodeInvalidUser) // no hint about the rules either
		if n, _ := st.Count(); n != 0 {
			t.Errorf("%v: %d accounts made", m, n)
		}
		a, pw, err := CreateAccount(st, "Made.By-Op", t0)
		if err != nil || a.Email != "Made.By-Op" {
			t.Fatalf("%v: %+v %v", m, a, err)
		}
		check(t, p, st, "made.by-op", pw, public1, 0)
		check(t, p, st, "made.by-op", pw+"x", public1, CodeInvalidPassword)
		if _, _, err := CreateAccount(st, "MADE.BY-OP", t0); err == nil {
			t.Errorf("%v: created a taken name", m)
		}
		if _, _, err := CreateAccount(st, "bad name", t0); err == nil {
			t.Errorf("%v: created a name against the rules", m)
		}
	}
}

func TestLoginStatus(t *testing.T) {
	st := store.New()
	p := Policy{}
	check(t, p, st, "player1", "dawngate1", loopback, 0)
	for s, code := range map[store.Status]uint16{store.StatusDisabled: CodeUserInactive, store.StatusBanned: CodeBanned,
		store.StatusDeleted: CodeDeactivated, store.StatusActive: 0} {
		if _, err := SetStatus(st, "PLAYER1", s); err != nil {
			t.Fatal(err)
		}
		check(t, p, st, "player1", "dawngate1", loopback, code)
		if s != store.StatusActive {
			check(t, p, st, "player1", "wrongpass", loopback, CodeInvalidPassword) // the status only after the password
		}
	}
	if _, err := SetStatus(st, "nobody", store.StatusBanned); err == nil {
		t.Error("an unknown account's status set")
	}
	// a legacy account without a password that is banned stays banned (no password to set)
	st.Login("legacy", t0)
	SetStatus(st, "legacy", store.StatusBanned)
	check(t, p, st, "legacy", "dawngate1", loopback, CodeBanned)
	if c, _, _ := st.Credentials("legacy"); c.Hash != "" {
		t.Error("a banned account got a password")
	}
}

func TestSignupCap(t *testing.T) {
	st := store.New()
	p := Policy{Mode: Open, SignupsPerAddress: 2}
	check(t, p, st, "pub1", "dawngate1", public1, 0)
	check(t, p, st, "pub2", "dawngate1", public1, 0)
	check(t, p, st, "pub3", "dawngate1", public1, CodeBusy)
	check(t, p, st, "pub1", "dawngate1", public1, 0) // logins of existing accounts aren't capped
	check(t, p, st, "pub3", "dawngate1", public2, 0)
	for i := range 5 { // loopback and LAN aren't counted
		check(t, p, st, "lo"+string(rune('a'+i))+"xx", "dawngate1", loopback, 0)
		check(t, p, st, "lan"+string(rune('a'+i))+"x", "dawngate1", lan, 0)
	}
	r := p.Login(st, "pub4", "dawngate1", public1, t0.Add(24*time.Hour)) // the next UTC day
	if r.Code != 0 {
		t.Errorf("the next day: %#x %s", r.Code, r.Note)
	}
}

func TestRehash(t *testing.T) {
	st := store.New()
	a, _, _ := st.Login("old", t0)
	old, _ := hashWith("dawngate1", Params{Memory: 64, Time: 1, Threads: 1})
	st.SetPassword(a.UserID, old)
	r := check(t, Policy{}, st, "old", "dawngate1", loopback, 0)
	c, _, _ := st.Credentials("old")
	if c.Hash == old || NeedsRehash(c.Hash) || !Verify(c.Hash, "dawngate1") || !strings.Contains(r.Note, "renewed") {
		t.Errorf("not rehashed: %s (%s)", c.Hash, r.Note)
	}
}

func TestRegisterAndInvites(t *testing.T) {
	st := store.New()
	code, inv, err := NewInvite(st, 2, 48*time.Hour, "club", t0)
	if err != nil || inv.Uses != 2 || !inv.Expires.Equal(t0.Add(48*time.Hour)) {
		t.Fatalf("%q %+v %v", code, inv, err)
	}
	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$`).MatchString(code) {
		t.Errorf("code %q", code)
	}
	p := Policy{Mode: Invite}
	reg := func(name, code string, want uint16) {
		t.Helper()
		r := p.Register(st, name, "", "dawngate1", code, public1, t0)
		if r.Code != want {
			t.Errorf("register %q with %q: %#x (%s), want %#x", name, code, r.Code, r.Note, want)
		}
	}
	reg("reg1", "", CodeInvalidCode)
	reg("reg1", "AAAA-BBBB-CCCC-DDDD", CodeInvalidCode)
	reg("reg1", strings.ToLower(strings.ReplaceAll(code, "-", " ")), 0) // typed loosely
	reg("reg1", code, CodeExists)                                       // the name is taken (no use spent)
	reg("bad name", code, CodeInvalidName)
	reg("reg2", code, 0)
	reg("reg3", code, CodeCodeUsed)
	check(t, p, st, "reg1", "dawngate1", public1, 0)
	if all, _ := st.Invites(); all[0].Used != 2 {
		t.Errorf("used %d", all[0].Used)
	}
	if r := (Policy{Mode: Closed}).Register(st, "reg4", "", "dawngate1", code, public1, t0); r.Code != CodeInvalidUser {
		t.Errorf("closed: %+v", r)
	}
	if r := (Policy{Mode: Open}).Register(st, "reg4", "", "dawngate1", "", public1, t0); r.Code != 0 || !r.First {
		t.Errorf("open without a code: %+v", r)
	}
	// expired
	code2, _, _ := NewInvite(st, 1, time.Hour, "", t0)
	if r := p.Register(st, "reg5", "", "dawngate1", code2, public1, t0.Add(2*time.Hour)); r.Code != CodeInvalidCode {
		t.Errorf("expired: %+v", r)
	}
	if NormalizeCode("ab-cd oi l") != "ABCD011" {
		t.Errorf("NormalizeCode: %s", NormalizeCode("ab-cd oi l"))
	}
}

func TestNewPassword(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		pw := NewPassword()
		if len(pw) != 16 || CheckPassword(pw) != 0 || strings.ContainsAny(pw, "0O1lI") || seen[pw] {
			t.Fatalf("%q", pw)
		}
		seen[pw] = true
	}
}
