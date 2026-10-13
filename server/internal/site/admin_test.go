package site

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/store"
)

// adminFixture is a site with the admin page: an admin "boss" (its password in pw), a player "player1", a clock the
// test moves, a live sign-up mode and two fake matches.
type adminFixture struct {
	*fixture
	pw      string
	bossID  int64
	player  store.Account
	live    *accounts.ModeSetting
	mu      sync.Mutex
	now     time.Time
	ended   []string // EndMatch's calls: "<game> <reason>"
	matches []Match
}

func newAdminFixture(t *testing.T, mut func(*Config)) *adminFixture {
	t.Helper()
	af := &adminFixture{now: t0, live: accounts.NewModeSetting(accounts.Open)}
	af.matches = []Match{{GameID: 7, Name: "player's Game", Mode: "practice", State: "in game", Started: t0.Add(-125 * time.Second),
		Host: 3, Pid: 4242, Players: []MatchPlayer{{Name: "Player1", UserID: 1000000002, PersonaID: 2000000002, Joined: true}}}}
	af.fixture = newFixture(t, accounts.Open, func(c *Config) {
		c.Now = af.clock
		c.Policy.Live = af.live
		c.Admin = &AdminConfig{
			Matches: func() []Match {
				af.mu.Lock()
				defer af.mu.Unlock()
				return append([]Match(nil), af.matches...)
			},
			EndMatch: func(id int64, reason string) error {
				af.mu.Lock()
				defer af.mu.Unlock()
				for i, m := range af.matches {
					if m.GameID == id {
						af.ended = append(af.ended, fmt.Sprintf("%d %s", id, reason))
						af.matches = append(af.matches[:i], af.matches[i+1:]...)
						return nil
					}
				}
				return errors.New("no running game with this id")
			},
		}
		if mut != nil {
			mut(c)
		}
	})
	boss, pw, err := accounts.CreateAccount(af.st, "boss", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetAdmin(af.st, "boss", true); err != nil {
		t.Fatal(err)
	}
	af.pw, af.bossID = pw, boss.UserID
	af.player, _, _ = accounts.CreateAccount(af.st, "player1", t0)
	return af
}

func (af *adminFixture) clock() time.Time {
	af.mu.Lock()
	defer af.mu.Unlock()
	return af.now
}

func (af *adminFixture) advance(d time.Duration) {
	af.mu.Lock()
	af.now = af.now.Add(d)
	af.mu.Unlock()
}

// login logs c in as boss.
func (af *adminFixture) login(c *http.Client) {
	af.t.Helper()
	code, body, _ := af.adminPost(c, "/admin/login", map[string]string{"name": "boss", "password": af.pw})
	if code != 200 || !strings.Contains(body, "Logged in.") {
		af.t.Fatalf("admin login: %d\n%s", code, body)
	}
}

// adminPost posts the fields with the CSRF token of the admin page (any page of it carries one).
func (af *adminFixture) adminPost(c *http.Client, path string, fields map[string]string) (int, string, http.Header) {
	af.t.Helper()
	_, page, _ := af.get(c, "/admin")
	m := tokenRE.FindStringSubmatch(page)
	if m == nil {
		af.t.Fatalf("no CSRF token on /admin:\n%s", page)
	}
	v := url.Values{CSRFField: {m[1]}}
	for k, x := range fields {
		v.Set(k, x)
	}
	return af.post(c, path, v, nil)
}

func (af *adminFixture) audit() []store.AuditEntry {
	af.t.Helper()
	log, err := af.st.AuditLog(100, 0)
	if err != nil {
		af.t.Fatal(err)
	}
	return log
}

func (af *adminFixture) lastAudit() store.AuditEntry {
	af.t.Helper()
	log := af.audit()
	if len(log) == 0 {
		af.t.Fatal("the audit log is empty")
	}
	return log[0]
}

// The login: the form, refusals (no admin, a wrong password) with one text, the session cookie's attributes, the
// pages behind it, the logout, and every step in the audit log.
func TestAdminLoginAndSession(t *testing.T) {
	af := newAdminFixture(t, nil)
	code, body, _ := af.get(af.c, "/admin")
	if code != 200 || !strings.Contains(body, `action="/admin/login"`) || strings.Contains(body, "/admin/accounts") {
		t.Fatalf("/admin without a session: %d\n%s", code, body)
	}
	for _, p := range []string{"/admin/accounts", "/admin/invites", "/admin/signup", "/admin/matches", "/admin/audit"} {
		res, err := af.c.Get(af.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin" {
			t.Errorf("%s without a session: %d %s", p, res.StatusCode, res.Header.Get("Location"))
		}
	}
	// a player with its right password, the admin with a wrong one, an unknown name: one text, 401
	_, ppw, _ := accounts.ResetPassword(af.st, "player1")
	for _, f := range []map[string]string{{"name": "player1", "password": ppw}, {"name": "boss", "password": "wrongpass1"},
		{"name": "nobody", "password": "wrongpass1"}} {
		code, body, h := af.adminPost(af.c, "/admin/login", f)
		if code != 401 || !strings.Contains(body, "Wrong login name or password, or this account is no admin.") ||
			strings.Contains(h.Get("Set-Cookie"), AdminCookie) {
			t.Errorf("%v: %d %s\n%s", f["name"], code, h.Get("Set-Cookie"), body)
		}
	}
	log := af.audit()
	if len(log) != 3 || log[2].Action != "admin login refused" || log[2].Detail != "account 1000000002 is no admin" ||
		log[1].Detail != "account 1000000001: wrong password" || log[0].Target != `"nobody"` ||
		!strings.HasPrefix(log[0].Source, "web 127.0.0.1") || log[0].Actor != "operator" && log[0].Actor != "" {
		t.Errorf("audit of the refusals: %+v", log)
	}
	// the admin's login
	code, body, h := af.adminPost(af.c, "/admin/login", map[string]string{"name": "boss", "password": af.pw})
	if code != 200 || !strings.Contains(body, "Logged in.") || !strings.Contains(body, "Log out boss") {
		t.Fatalf("login: %d\n%s", code, body)
	}
	sc := strings.Join(h.Values("Set-Cookie"), "\n")
	for _, want := range []string{AdminCookie + "=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(sc, want) {
			t.Errorf("Set-Cookie %q lacks %s", sc, want)
		}
	}
	if e := af.lastAudit(); e.Action != "admin login" || e.Actor != "admin boss (account 1000000001)" {
		t.Errorf("audit %+v", e)
	}
	for _, p := range []string{"/admin", "/admin/accounts", "/admin/invites", "/admin/signup", "/admin/matches", "/admin/audit"} {
		code, body, h := af.get(af.c, p)
		if code != 200 || strings.Contains(body, "<script") || !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") ||
			h.Get("Cache-Control") != "no-store" || !strings.Contains(body, "Log out boss") {
			t.Errorf("%s: %d\n%s", p, code, body)
		}
	}
	// another browser has no session
	other := af.newClient()
	if code, body, _ := af.get(other, "/admin"); code != 200 || !strings.Contains(body, `action="/admin/login"`) {
		t.Errorf("another browser: %d", code)
	}
	// idle: 29 minutes keep it, 31 more end it
	af.advance(29 * time.Minute)
	if _, body, _ := af.get(af.c, "/admin/audit"); !strings.Contains(body, "Log out boss") {
		t.Error("the session ended before 30 idle minutes")
	}
	af.advance(31 * time.Minute)
	if res, _ := af.c.Get(af.srv.URL + "/admin/audit"); res.StatusCode != http.StatusSeeOther {
		t.Errorf("after 31 idle minutes: %d", res.StatusCode)
	}
	// log in again, then log out
	af.login(af.c)
	code, body, _ = af.adminPost(af.c, "/admin/logout", nil)
	if code != 200 || !strings.Contains(body, "Logged out.") {
		t.Errorf("logout: %d\n%s", code, body)
	}
	if res, _ := af.c.Get(af.srv.URL + "/admin/accounts"); res.StatusCode != http.StatusSeeOther {
		t.Errorf("after the logout: %d", res.StatusCode)
	}
	if e := af.lastAudit(); e.Action != "admin logout" {
		t.Errorf("audit %+v", e)
	}
	// the role taken (--remove-admin) or the account disabled ends a session at its next request
	af.login(af.c)
	accounts.SetAdmin(af.st, "boss", false)
	if res, _ := af.c.Get(af.srv.URL + "/admin/accounts"); res.StatusCode != http.StatusSeeOther {
		t.Errorf("after --remove-admin: %d", res.StatusCode)
	}
	if l := af.logText(); strings.Contains(l, af.pw) || strings.Contains(l, ppw) {
		t.Errorf("a password in the log:\n%s", l)
	}
	for _, e := range af.audit() {
		if strings.Contains(e.Detail+e.Target, af.pw) || strings.Contains(e.Detail+e.Target, "wrongpass1") {
			t.Errorf("a password in the audit log: %+v", e)
		}
	}
}

// fakeLimiter records the game login's limiter calls (dgserver's limits on).
type fakeLimiter struct {
	mu    sync.Mutex
	calls []string
}

func (l *fakeLimiter) Login(addr, account string) (time.Duration, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "login "+account)
	return 0, ""
}

func (l *fakeLimiter) LoginDone(addr, account string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fmt.Sprintf("done %s %v", account, ok))
}

// The admin login's limits: stricter than the game's (limits.Admin, always on): the 4th wrong password for a name
// locks it, an attempt then is refused with 429 before the password is checked; the game login's limiter is asked too.
func TestAdminLoginLimits(t *testing.T) {
	game := &fakeLimiter{}
	af := newAdminFixture(t, func(c *Config) { c.Limits = game })
	for i := range 4 {
		if code, _, _ := af.adminPost(af.c, "/admin/login", map[string]string{"name": "boss", "password": "wrongpass1"}); code != 401 {
			t.Fatalf("attempt %d: %d", i+1, code)
		}
		af.advance(31 * time.Second) // the address's rate: one every 30 s after 5
	}
	code, body, h := af.adminPost(af.c, "/admin/login", map[string]string{"name": "boss", "password": af.pw})
	if code != 429 || h.Get("Retry-After") == "" || !strings.Contains(body, "Too many attempts") {
		t.Fatalf("locked: %d %s\n%s", code, h.Get("Retry-After"), body)
	}
	af.advance(time.Minute)
	af.login(af.c)
	game.mu.Lock()
	defer game.mu.Unlock()
	if len(game.calls) != 10 || game.calls[0] != "login boss" || game.calls[1] != "done boss false" ||
		game.calls[9] != "done boss true" {
		t.Errorf("the game limiter's calls: %v", game.calls)
	}
	if l := af.logText(); !strings.Contains(l, "admin login limits: 4 failed logins for \"boss\"") {
		t.Errorf("log:\n%s", l)
	}
}

// Accounts: the search, disable (the game's login then refused with 0x0065), enable, ban, a new password (shown once,
// works), delete (confirmed); the own account is protected; each action in the audit log, never a password.
func TestAdminAccounts(t *testing.T) {
	af := newAdminFixture(t, nil)
	af.login(af.c)
	_, body, _ := af.get(af.c, "/admin/accounts?q=PLAYER")
	if !strings.Contains(body, "<td>player1") || strings.Contains(body, "<td>boss") {
		t.Errorf("search:\n%s", body)
	}
	_, body, _ = af.get(af.c, "/admin/accounts")
	if !strings.Contains(body, "<td>boss") || !strings.Contains(body, "(you)") || !strings.Contains(body, "<b>admin</b>") {
		t.Errorf("all:\n%s", body)
	}
	id := fmt.Sprint(af.player.UserID)
	// a new password: shown once, the login works with it
	code, body, _ := af.adminPost(af.c, "/admin/accounts/password", map[string]string{"id": id})
	m := regexp.MustCompile(`<p class="secret">([^<]+)</p>`).FindStringSubmatch(body)
	if code != 200 || m == nil {
		t.Fatalf("new password: %d\n%s", code, body)
	}
	pw := m[1]
	login := func() uint16 {
		return accounts.Policy{}.Login(af.st, "player1", pw, netip.MustParseAddr("127.0.0.1"), t0).Code
	}
	if c := login(); c != 0 {
		t.Fatalf("login with the new password: %#x", c)
	}
	if e := af.lastAudit(); e.Action != "reset password" || e.Target != `account 1000000002 "player1"` || strings.Contains(e.Detail, pw) {
		t.Errorf("audit %+v", e)
	}
	// disable: refused with the client's 0x0065; enable; ban: 0x002B
	for _, c := range []struct {
		status string
		login  uint16
		detail string
	}{{"disabled", accounts.CodeUserInactive, "active -> disabled"}, {"active", 0, "disabled -> active"},
		{"banned", accounts.CodeBanned, "active -> banned"}, {"active", 0, "banned -> active"}} {
		code, body, _ := af.adminPost(af.c, "/admin/accounts/status", map[string]string{"id": id, "status": c.status, "q": "player"})
		if code != 200 || !strings.Contains(body, "is "+c.status+" now") || !strings.Contains(body, "<td>player1") {
			t.Errorf("%s: %d\n%s", c.status, code, body)
		}
		if got := login(); got != c.login {
			t.Errorf("%s: login %#x, want %#x", c.status, got, c.login)
		}
		if e := af.lastAudit(); e.Action != "account status" || e.Detail != c.detail {
			t.Errorf("%s: audit %+v", c.status, e)
		}
	}
	// refusals: the own account, a bad status, an unknown account
	for _, f := range []map[string]string{{"id": fmt.Sprint(af.bossID), "status": "disabled"}, {"id": id, "status": "deleted"},
		{"id": id, "status": "sleepy"}, {"id": "1", "status": "disabled"}, {"id": "x", "status": "disabled"}} {
		if code, _, _ := af.adminPost(af.c, "/admin/accounts/status", f); code != 400 {
			t.Errorf("%v: %d", f, code)
		}
	}
	if code, body, _ := af.adminPost(af.c, "/admin/accounts/delete", map[string]string{"id": fmt.Sprint(af.bossID), "confirm": "yes"}); code != 400 ||
		!strings.Contains(body, "lock you out") {
		t.Errorf("own delete: %d\n%s", code, body)
	}
	// delete: confirmed only
	if code, body, _ := af.adminPost(af.c, "/admin/accounts/delete", map[string]string{"id": id}); code != 400 ||
		!strings.Contains(body, "Tick the box") {
		t.Errorf("unconfirmed delete: %d\n%s", code, body)
	}
	if _, found, _ := af.st.Credentials("player1"); !found {
		t.Fatal("deleted without confirmation")
	}
	code, body, _ = af.adminPost(af.c, "/admin/accounts/delete", map[string]string{"id": id, "confirm": "yes"})
	if code != 200 || !strings.Contains(body, "is deleted") {
		t.Errorf("delete: %d\n%s", code, body)
	}
	if _, found, _ := af.st.Credentials("player1"); found { // the name is free again (the rule)
		t.Error("the deleted account's name still taken")
	}
	if e := af.lastAudit(); e.Action != "delete account" || e.Target != `account 1000000002 "player1"` {
		t.Errorf("audit %+v", e)
	}
	if code, _, _ := af.adminPost(af.c, "/admin/accounts/password", map[string]string{"id": id}); code != 400 {
		t.Errorf("a new password for a deleted account: %d", code)
	}
	// without the CSRF token
	if code, _, _ := af.post(af.c, "/admin/accounts/status", url.Values{"id": {id}, "status": {"disabled"}}, nil); code != 403 {
		t.Errorf("no token: %d", code)
	}
	// without a session (another browser with a token of its own)
	other := af.newClient()
	if code, body, _ := af.adminPost(other, "/admin/accounts/status", map[string]string{"id": id, "status": "disabled"}); code != 403 ||
		!strings.Contains(body, "session has ended") {
		t.Errorf("no session: %d\n%s", code, body)
	}
	if l := af.logText(); strings.Contains(l, pw) {
		t.Errorf("the new password in the log:\n%s", l)
	}
}

// Invite codes: made (shown once, works on the sign-up page under invite), listed, revoked (then refused).
func TestAdminInvites(t *testing.T) {
	af := newAdminFixture(t, nil)
	af.live.Set(accounts.Invite)
	af.login(af.c)
	code, body, _ := af.adminPost(af.c, "/admin/invites", map[string]string{"uses": "2", "days": "7", "note": "for <Ann>\x07"})
	m := regexp.MustCompile(`<p class="secret">([0-9A-Z-]{19})</p>`).FindStringSubmatch(body)
	if code != 200 || m == nil || !strings.Contains(body, "for &lt;Ann&gt;") || !strings.Contains(body, "2 left") {
		t.Fatalf("make: %d\n%s", code, body)
	}
	invite := m[1]
	e := af.lastAudit()
	if e.Action != "invite create" || e.Target != "invite 1" || strings.Contains(e.Detail, invite) ||
		e.Detail != `2 use(s), valid until 2026-10-20 12:00 UTC, note "for <Ann>"` {
		t.Errorf("audit %+v", e)
	}
	player := af.newClient()
	if code, body, _ := af.submit(player, "/signup", register("invited1", "", "dawngate1", invite)); code != 200 {
		t.Fatalf("sign-up with the code: %d\n%s", code, body)
	}
	_, body, _ = af.get(af.c, "/admin/invites")
	if !strings.Contains(body, "1 of 2") || !strings.Contains(body, "1 left") || strings.Contains(body, invite) {
		t.Errorf("list:\n%s", body)
	}
	for _, f := range []map[string]string{{"uses": "0", "days": "1"}, {"uses": "1", "days": "-1"}, {"uses": "x", "days": "1"}} {
		if code, _, _ := af.adminPost(af.c, "/admin/invites", f); code != 400 {
			t.Errorf("%v: %d", f, code)
		}
	}
	code, body, _ = af.adminPost(af.c, "/admin/invites/revoke", map[string]string{"id": "1"})
	if code != 200 || !strings.Contains(body, "revoked") {
		t.Errorf("revoke: %d\n%s", code, body)
	}
	if e := af.lastAudit(); e.Action != "invite revoke" || e.Target != "invite 1" {
		t.Errorf("audit %+v", e)
	}
	if code, body, _ := af.submit(player, "/signup", register("invited2", "", "dawngate1", invite)); code != 400 ||
		!strings.Contains(body, "unknown, expired or revoked") {
		t.Errorf("a revoked code: %d\n%s", code, body)
	}
	if code, _, _ := af.adminPost(af.c, "/admin/invites/revoke", map[string]string{"id": "99"}); code != 400 {
		t.Errorf("an unknown code: %d", code)
	}
}

// The sign-up mode: changed on the page, in force at once for the website (and, through Policy.Live, the game's
// login), kept in the store, in the audit log.
func TestAdminSignupMode(t *testing.T) {
	af := newAdminFixture(t, nil)
	af.login(af.c)
	_, body, _ := af.get(af.c, "/admin/signup")
	if !strings.Contains(body, "Sign-up is <b>open</b>") || !strings.Contains(body, `<option value="open" selected>`) {
		t.Errorf("page:\n%s", body)
	}
	code, body, _ := af.adminPost(af.c, "/admin/signup", map[string]string{"mode": "closed"})
	if code != 200 || !strings.Contains(body, "Sign-up is closed now (was open)") {
		t.Fatalf("set: %d\n%s", code, body)
	}
	if af.live.Get() != accounts.Closed {
		t.Errorf("live mode %v", af.live.Get())
	}
	if _, page, _ := af.get(af.newClient(), "/signup"); strings.Contains(page, "<form") {
		t.Error("the sign-up form under closed")
	}
	if v, _, _, _ := af.st.Setting(accounts.SettingSignup); v != "closed" {
		t.Errorf("stored %q", v)
	}
	if e := af.lastAudit(); e.Action != "sign-up mode" || e.Detail != "open -> closed" {
		t.Errorf("audit %+v", e)
	}
	if code, _, _ := af.adminPost(af.c, "/admin/signup", map[string]string{"mode": "maybe"}); code != 400 {
		t.Errorf("a bad mode: %d", code)
	}
	// a site without a live mode can't change it
	af2 := newAdminFixture(t, func(c *Config) { c.Policy.Live = nil })
	af2.login(af2.c)
	if code, _, _ := af2.adminPost(af2.c, "/admin/signup", map[string]string{"mode": "closed"}); code != 409 {
		t.Errorf("no live mode: %d", code)
	}
}

// Matches: listed with players and duration; ended (confirmed) with the admin in the reason; an ended or unknown one
// refused.
func TestAdminMatches(t *testing.T) {
	af := newAdminFixture(t, nil)
	af.login(af.c)
	_, body, _ := af.get(af.c, "/admin/matches")
	for _, want := range []string{"<td>7<br>", "host 3, pid 4242", "practice<br>in game", "2 min 05 s",
		"Player1 (account 1000000002, team 1, in the match)"} {
		if !strings.Contains(body, want) {
			t.Errorf("matches lack %q:\n%s", want, body)
		}
	}
	if code, _, _ := af.adminPost(af.c, "/admin/matches/end", map[string]string{"game": "7"}); code != 400 {
		t.Errorf("unconfirmed: %d", code)
	}
	code, body, _ := af.adminPost(af.c, "/admin/matches/end", map[string]string{"game": "7", "confirm": "yes", "reason": "test end"})
	if code != 200 || !strings.Contains(body, "Match 7 is ending") || !strings.Contains(body, "No match is running.") {
		t.Fatalf("end: %d\n%s", code, body)
	}
	if len(af.ended) != 1 || af.ended[0] != "7 ended by admin boss (account 1000000001) on the admin page: test end" {
		t.Errorf("EndMatch calls %v", af.ended)
	}
	if e := af.lastAudit(); e.Action != "end match" || e.Target != "game 7" {
		t.Errorf("audit %+v", e)
	}
	if code, body, _ := af.adminPost(af.c, "/admin/matches/end", map[string]string{"game": "7", "confirm": "yes"}); code != 409 ||
		!strings.Contains(body, "not ended") {
		t.Errorf("again: %d\n%s", code, body)
	}
	_, body, _ = af.get(af.c, "/admin/audit")
	for _, want := range []string{"admin login", "end match", "game 7"} {
		if !strings.Contains(body, want) {
			t.Errorf("the audit page lacks %q", want)
		}
	}
}
