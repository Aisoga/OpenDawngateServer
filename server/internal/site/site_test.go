package site

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/store"
)

var t0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t     *testing.T
	st    *store.Store
	site  *Site
	srv   *httptest.Server
	c     *http.Client
	log   []string
	logMu sync.Mutex
}

func newFixture(t *testing.T, mode accounts.Mode, mut func(*Config)) *fixture {
	t.Helper()
	f := &fixture{t: t, st: store.New()}
	t.Cleanup(func() { f.st.Close() })
	cfg := Config{Store: f.st, Policy: accounts.Policy{Mode: mode, SignupsPerAddress: 3}, Version: "test",
		Now: func() time.Time { return t0 }, FormRate: Rate{PerMinute: 600, Burst: 600},
		Status: func() Status { return Status{PlayersOnline: 2, MatchesRunning: 1} },
		Log: func(format string, a ...any) {
			f.logMu.Lock()
			f.log = append(f.log, fmt.Sprintf(format, a...))
			f.logMu.Unlock()
		}}
	if mut != nil {
		mut(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.site = s
	f.srv = httptest.NewTLSServer(s)
	t.Cleanup(f.srv.Close)
	f.c = f.newClient()
	return f
}

// newClient is a browser: the test server's certificate trusted, its own cookies, no redirects followed.
func (f *fixture) newClient() *http.Client {
	c := *f.srv.Client() // a copy: the server's client is one shared value
	jar, _ := cookiejar.New(nil)
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

func (f *fixture) get(c *http.Client, path string) (int, string, http.Header) {
	f.t.Helper()
	res, err := c.Get(f.srv.URL + path)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

var tokenRE = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// submit gets the form page (for its token), then posts the fields with it.
func (f *fixture) submit(c *http.Client, path string, fields map[string]string) (int, string, http.Header) {
	f.t.Helper()
	_, page, _ := f.get(c, path)
	m := tokenRE.FindStringSubmatch(page)
	if m == nil {
		f.t.Fatalf("%s: no CSRF token in the form:\n%s", path, page)
	}
	v := url.Values{CSRFField: {m[1]}}
	for k, x := range fields {
		v.Set(k, x)
	}
	return f.post(c, path, v, nil)
}

func (f *fixture) post(c *http.Client, path string, v url.Values, hdr map[string]string) (int, string, http.Header) {
	f.t.Helper()
	req, _ := http.NewRequest("POST", f.srv.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, x := range hdr {
		req.Header.Set(k, x)
	}
	res, err := c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

func (f *fixture) logText() string {
	f.logMu.Lock()
	defer f.logMu.Unlock()
	return strings.Join(f.log, "\n")
}

func register(name, display, pw, code string) map[string]string {
	return map[string]string{"name": name, "display": display, "password": pw, "password2": pw, "code": code}
}

func TestPagesAndHeaders(t *testing.T) {
	f := newFixture(t, accounts.Open, nil)
	for _, p := range []string{"/", "/signup", "/password", "/account/password", "/account/delete", "/account/data",
		"/status", "/privacy", "/terms"} {
		code, body, h := f.get(f.c, p)
		if code != 200 || !strings.Contains(body, "<html") {
			t.Errorf("%s: %d", p, code)
		}
		if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") || h.Get("X-Frame-Options") != "DENY" ||
			h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: headers %v", p, h)
		}
		if strings.Contains(body, "<script") {
			t.Errorf("%s: a script", p)
		}
	}
	if code, _, _ := f.get(f.c, "/nothing"); code != 404 {
		t.Errorf("/nothing: %d", code)
	}
	if code, _, h := f.get(f.c, "/site.css"); code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/css") {
		t.Errorf("/site.css: %d %s", code, h.Get("Content-Type"))
	}
	// the CSRF cookie: __Host-, Secure, HttpOnly, SameSite=Strict, Path=/
	res, err := f.newClient().Get(f.srv.URL + "/signup")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	sc := res.Header.Get("Set-Cookie")
	for _, want := range []string{CSRFCookie + "=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(sc, want) {
			t.Errorf("Set-Cookie %q lacks %s", sc, want)
		}
	}
	if strings.Contains(sc, "Domain=") {
		t.Errorf("Set-Cookie %q has a Domain", sc)
	}
}

func TestStatusNoNames(t *testing.T) {
	f := newFixture(t, accounts.Open, nil)
	f.submit(f.c, "/signup", register("secretname", "SecretDisplay", "dawngate1", ""))
	code, body, _ := f.get(f.c, "/status")
	if code != 200 || !strings.Contains(body, "<td>2</td>") || !strings.Contains(body, "<td>1</td>") ||
		!strings.Contains(body, "test") {
		t.Fatalf("/status: %d\n%s", code, body)
	}
	_, js, h := f.get(f.c, "/status.json")
	var st StatusJSON
	if err := json.Unmarshal([]byte(js), &st); err != nil || h.Get("Content-Type") != "application/json" {
		t.Fatalf("/status.json: %v %s", err, js)
	}
	if st.PlayersOnline != 2 || st.MatchesRunning != 1 || st.Version != "test" || st.Signup != "open" {
		t.Errorf("/status.json: %+v", st)
	}
	for _, p := range []string{body, js} {
		if strings.Contains(strings.ToLower(p), "secret") {
			t.Errorf("the status names a player:\n%s", p)
		}
	}
}

func TestRegisterOpen(t *testing.T) {
	f := newFixture(t, accounts.Open, nil)
	code, body, _ := f.submit(f.c, "/signup", register("WebPlayer", "Webby", "dawngate1", ""))
	if code != 200 || !strings.Contains(body, "Account made") || !strings.Contains(body, "Webby") {
		t.Fatalf("register: %d\n%s", code, body)
	}
	// the account logs in as the game's login does, with that display name
	r := accounts.Policy{Mode: accounts.Invite}.Login(f.st, "webplayer", "dawngate1", netip.MustParseAddr("127.0.0.1"), t0)
	if r.Code != 0 || r.Account.Personas[0].Name != "Webby" {
		t.Fatalf("login: %+v", r)
	}
	// taken login name, taken display name, bad display name, short password, passwords differ
	cases := []struct {
		fields map[string]string
		code   int
		text   string
	}{
		{register("webplayer", "", "dawngate1", ""), 400, "login name is taken"},
		{register("other", "webby", "dawngate1", ""), 400, "display name is taken"},
		{register("other", "no spaces", "dawngate1", ""), 400, "A display name has"},
		{register("x", "", "dawngate1", ""), 400, "A login name has"},
		{register("other", "", "short", ""), 400, "A password has"},
		{register("other", "", "", ""), 400, "A password has"},
		{map[string]string{"name": "other", "password": "dawngate1", "password2": "dawngate2"}, 400, "differ"},
	}
	for _, c := range cases {
		code, body, _ := f.submit(f.c, "/signup", c.fields)
		if code != c.code || !strings.Contains(body, c.text) {
			t.Errorf("%v: %d, want %d with %q:\n%s", c.fields, code, c.code, c.text, body)
		}
		if strings.Contains(body, "dawngate1") || strings.Contains(body, "dawngate2") {
			t.Errorf("%v: the page shows a password", c.fields)
		}
	}
	// the default display name comes from the login name
	if code, body, _ := f.submit(f.c, "/signup", register("plain.name@example.org", "", "dawngate1", "")); code != 200 ||
		!strings.Contains(body, "plainname") {
		t.Errorf("default display name: %d\n%s", code, body)
	}
	if l := f.logText(); strings.Contains(l, "dawngate1") || !strings.Contains(l, "POST /signup -> 200: account") {
		t.Errorf("log:\n%s", l)
	}
}

func TestRegisterInviteAndClosed(t *testing.T) {
	f := newFixture(t, accounts.Invite, nil)
	_, page, _ := f.get(f.c, "/signup")
	if !strings.Contains(page, `name="code"`) {
		t.Fatal("invite mode: no code field")
	}
	if code, body, _ := f.submit(f.c, "/signup", register("invitee", "", "dawngate1", "")); code != 400 ||
		!strings.Contains(body, "invite code is unknown") {
		t.Errorf("no code: %d\n%s", code, body)
	}
	code1, _, err := accounts.NewInvite(f.st, 1, 0, "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if code, body, _ := f.submit(f.c, "/signup", register("invitee", "", "dawngate1", strings.ToLower(code1))); code != 200 {
		t.Fatalf("with the code: %d\n%s", code, body)
	}
	if code, body, _ := f.submit(f.c, "/signup", register("invitee2", "", "dawngate1", code1)); code != 400 ||
		!strings.Contains(body, "used up") {
		t.Errorf("used code: %d\n%s", code, body)
	}

	c := newFixture(t, accounts.Closed, nil)
	_, page, _ = c.get(c.c, "/signup")
	if strings.Contains(page, "<form") || !strings.Contains(page, "Only the server's operator") {
		t.Errorf("closed: %s", page)
	}
	// a forged token is refused before the mode; with a valid one, closed still says no
	_, page, _ = c.get(c.c, "/account/password")
	tok := tokenRE.FindStringSubmatch(page)[1]
	if code, _, _ := c.post(c.c, "/signup", url.Values{CSRFField: {tok}, "name": {"x123"}, "password": {"dawngate1"},
		"password2": {"dawngate1"}}, nil); code != 403 {
		t.Errorf("closed POST: %d", code)
	}
	if n, _ := c.st.Count(); n != 0 {
		t.Errorf("closed: %d accounts", n)
	}
}

func TestCSRF(t *testing.T) {
	f := newFixture(t, accounts.Open, nil)
	fields := url.Values{"name": {"csrfplayer"}, "password": {"dawngate1"}, "password2": {"dawngate1"}}
	// no cookie, no token
	if code, _, _ := f.post(f.newClient(), "/signup", fields, nil); code != 403 {
		t.Errorf("no token: %d", code)
	}
	// a token of another browser (its cookie isn't ours)
	other := f.newClient()
	_, page, _ := f.get(other, "/signup")
	otherTok := tokenRE.FindStringSubmatch(page)[1]
	f.get(f.c, "/signup") // our cookie
	v := url.Values{CSRFField: {otherTok}}
	for k, x := range fields {
		v[k] = x
	}
	if code, _, _ := f.post(f.c, "/signup", v, nil); code != 403 {
		t.Errorf("another browser's token: %d", code)
	}
	// our token, but sent from another site
	_, page, _ = f.get(f.c, "/signup")
	v.Set(CSRFField, tokenRE.FindStringSubmatch(page)[1])
	if code, _, _ := f.post(f.c, "/signup", v, map[string]string{"Sec-Fetch-Site": "cross-site"}); code != 403 {
		t.Errorf("cross-site: %d", code)
	}
	if code, _, _ := f.post(f.c, "/signup", v, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Errorf("foreign Origin: %d", code)
	}
	if n, _ := f.st.Count(); n != 0 {
		t.Fatalf("%d accounts made by refused posts", n)
	}
	// the same with a same-origin browser's headers passes
	u, _ := url.Parse(f.srv.URL)
	if code, body, _ := f.post(f.c, "/signup", v, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://" + u.Host}); code != 200 {
		t.Errorf("same origin: %d\n%s", code, body)
	}
	// a token from before a restart (another key) is refused once, with a fresh form
	f2, err := New(f.site.cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.srv.Config.Handler = f2
	_, page, _ = f.get(f.c, "/account/data") // a token of the other start's key
	f.srv.Config.Handler = f.site
	v2 := url.Values{CSRFField: {tokenRE.FindStringSubmatch(page)[1]}, "name": {"csrfplayer"}, "password": {"dawngate1"}}
	if code, body, _ := f.post(f.c, "/account/data", v2, nil); code != 403 || !strings.Contains(body, "expired") ||
		!tokenRE.MatchString(body) {
		t.Errorf("old key's token: %d\n%s", code, body)
	}
	// an oversized body is refused
	big := url.Values{CSRFField: {"x"}, "name": {strings.Repeat("a", maxBody)}}
	if code, _, _ := f.post(f.c, "/signup", big, nil); code != 413 {
		t.Errorf("big body: %d", code)
	}
}

func TestChangeDeleteData(t *testing.T) {
	f := newFixture(t, accounts.Open, nil)
	if code, _, _ := f.submit(f.c, "/signup", register("selfie", "Selfie", "dawngate1", "")); code != 200 {
		t.Fatal("register")
	}
	friend := accounts.Policy{}.Register(f.st, "friendly", "", "dawngate1", "", netip.Addr{}, t0)
	me, mine, _ := f.st.PersonaByName("Selfie")
	_, theirs, _ := f.st.PersonaByName(friend.Account.Personas[0].Name)
	f.st.UpdatePair(me.UserID, friend.Account.UserID, func(a, b *store.Account) {
		a.Lists.Friends = []store.ListMember{{ID: theirs.ID, Name: theirs.Name, Added: t0}}
		b.Lists.Friends = []store.ListMember{{ID: mine.ID, Name: mine.Name, Added: t0}}
		b.Lists.Recent = []store.ListMember{{ID: mine.ID, Name: mine.Name, Added: t0}}
		a.Items = map[uint32]uint32{7: 1}
		a.Settings = map[string]string{"k": "v"}
		a.History.Shapers = map[uint32]store.GamesWon{11: {Games: 3, Wins: 2}}
	})

	// change the password: wrong old one, new ones differ, short, then right
	pw := func(old, n1, n2 string) map[string]string {
		return map[string]string{"name": "SELFIE", "password": old, "new": n1, "new2": n2}
	}
	if code, body, _ := f.submit(f.c, "/account/password", pw("wrongpass", "dawngate2", "dawngate2")); code != 401 ||
		!strings.Contains(body, "Wrong login name or password") {
		t.Errorf("wrong old: %d", code)
	}
	if code, _, _ := f.submit(f.c, "/account/password", pw("dawngate1", "dawngate2", "dawngate3")); code != 400 {
		t.Errorf("differ: %d", code)
	}
	if code, body, _ := f.submit(f.c, "/account/password", pw("dawngate1", "short", "short")); code != 400 ||
		!strings.Contains(body, "A password has") {
		t.Errorf("short: %d", code)
	}
	if code, _, _ := f.submit(f.c, "/account/password", pw("dawngate1", "dawngate2", "dawngate2")); code != 200 {
		t.Fatalf("change: %d", code)
	}
	if r := (accounts.Policy{}).Login(f.st, "selfie", "dawngate1", netip.Addr{}, t0); r.Code != accounts.CodeInvalidPassword {
		t.Errorf("old password still works: %+v", r)
	}

	// download the data
	code, body, h := f.submit(f.c, "/account/data", map[string]string{"name": "selfie", "password": "dawngate2"})
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "application/json") ||
		!strings.Contains(h.Get("Content-Disposition"), "attachment") {
		t.Fatalf("data: %d %v\n%s", code, h, body)
	}
	var exp accounts.Export
	if err := json.Unmarshal([]byte(body), &exp); err != nil || exp.Account.Email != "selfie" || exp.Status != "active" ||
		exp.Account.Personas[0].Name != "Selfie" || exp.Account.Items[7] != 1 || len(exp.Account.Lists.Friends) != 1 {
		t.Fatalf("data: %v %+v", err, exp)
	}
	if strings.Contains(body, "argon2") {
		t.Fatal("the data holds the password hash")
	}

	// delete: unconfirmed, wrong password, then right
	del := func(pw, confirm string) map[string]string {
		return map[string]string{"name": "selfie", "password": pw, "confirm": confirm}
	}
	if code, _, _ := f.submit(f.c, "/account/delete", del("dawngate2", "")); code != 400 {
		t.Errorf("unconfirmed: %d", code)
	}
	if code, _, _ := f.submit(f.c, "/account/delete", del("dawngate1", "yes")); code != 401 {
		t.Errorf("wrong password: %d", code)
	}
	if code, body, _ := f.submit(f.c, "/account/delete", del("dawngate2", "yes")); code != 200 || !strings.Contains(body, "deleted") {
		t.Fatalf("delete: %d\n%s", code, body)
	}
	// the personal data is gone, the history stays without the name
	a, ok := f.st.Account(me.UserID)
	if !ok || a.Email == "selfie" || a.Personas[0].Name != store.DeletedName || len(a.Items) != 0 || len(a.Settings) != 0 ||
		len(a.Lists.Friends) != 0 || a.History.Shapers[11].Games != 3 {
		t.Fatalf("after the deletion: %+v", a)
	}
	if c, found, _ := f.st.Credentials("selfie"); found {
		t.Errorf("the login name is still taken: %+v", c)
	}
	if _, _, found := f.st.PersonaByName("Selfie"); found {
		t.Error("the display name still finds the account")
	}
	b, _ := f.st.Account(friend.Account.UserID)
	if len(b.Lists.Friends) != 0 || len(b.Lists.Recent) != 1 || b.Lists.Recent[0].Name != store.DeletedName {
		t.Errorf("the friend's lists: %+v", b.Lists)
	}
	// the login doesn't work any more; the name may be registered again
	if r := (accounts.Policy{Mode: accounts.Invite}).Login(f.st, "selfie", "dawngate2", netip.Addr{}, t0); r.Code != accounts.CodeInvalidUser {
		t.Errorf("login after the deletion: %+v", r)
	}
	if code, _, _ := f.submit(f.c, "/account/data", map[string]string{"name": "selfie", "password": "dawngate2"}); code != 401 {
		t.Errorf("data after the deletion: %d", code)
	}
	if code, _, _ := f.submit(f.c, "/signup", register("selfie", "Selfie", "dawngate9", "")); code != 200 {
		t.Errorf("the name again: %d", code)
	}
	// a disabled account can't act on itself
	accounts.SetStatus(f.st, "friendly", store.StatusDisabled)
	if code, body, _ := f.submit(f.c, "/account/delete", map[string]string{"name": "friendly", "password": "dawngate1", "confirm": "yes"}); code != 403 ||
		!strings.Contains(body, "disabled") {
		t.Errorf("disabled: %d", code)
	}
	if l := f.logText(); strings.Contains(l, "dawngate") {
		t.Errorf("a password in the log:\n%s", l)
	}
}

// fakeLimits records the limiter calls and refuses an account after two failures.
type fakeLimits struct {
	mu    sync.Mutex
	calls []string
	fails map[string]int
}

func (l *fakeLimits) Login(addr, account string) (time.Duration, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "login "+addr+" "+account)
	if l.fails[account] >= 2 {
		return 30 * time.Second, "locked"
	}
	return 0, ""
}

func (l *fakeLimits) LoginDone(addr, account string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fmt.Sprintf("done %s %s %v", addr, account, ok))
	if !ok {
		l.fails[account]++
	}
}

func TestLimits(t *testing.T) {
	lim := &fakeLimits{fails: map[string]int{}}
	f := newFixture(t, accounts.Open, func(c *Config) { c.Limits = lim })
	f.submit(f.c, "/signup", register("limited", "", "dawngate1", ""))
	for i := 0; i < 2; i++ {
		if code, _, _ := f.submit(f.c, "/account/data", map[string]string{"name": "Limited", "password": "wrong-one"}); code != 401 {
			t.Fatalf("wrong %d: %d", i, code)
		}
	}
	// locked now, even with the right password
	code, body, h := f.submit(f.c, "/account/data", map[string]string{"name": "limited", "password": "dawngate1"})
	if code != 429 || h.Get("Retry-After") != "30" || !strings.Contains(body, "Too many attempts") {
		t.Fatalf("locked: %d %v", code, h)
	}
	want := []string{"login 127.0.0.1 ", "login 127.0.0.1 limited", "done 127.0.0.1 limited false",
		"login 127.0.0.1 limited", "done 127.0.0.1 limited false", "login 127.0.0.1 limited"}
	if strings.Join(lim.calls, "|") != strings.Join(want, "|") {
		t.Errorf("limiter calls:\n%s\nwant\n%s", strings.Join(lim.calls, "\n"), strings.Join(want, "\n"))
	}

	// the site's own per-address rate, always on
	g := newFixture(t, accounts.Open, func(c *Config) { c.FormRate = Rate{PerMinute: 1, Burst: 2} })
	for i := 0; i < 2; i++ {
		if code, _, _ := g.submit(g.c, "/account/data", map[string]string{"name": "nobody", "password": "dawngate1"}); code != 401 {
			t.Fatalf("post %d: %d", i, code)
		}
	}
	code, _, h = g.submit(g.c, "/account/data", map[string]string{"name": "nobody", "password": "dawngate1"})
	if code != 429 || h.Get("Retry-After") != "60" {
		t.Errorf("third post: %d %v", code, h)
	}
	if !strings.Contains(g.logText(), "refused: the address's form rate") {
		t.Errorf("log:\n%s", g.logText())
	}
}

func TestOperatorPages(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "privacy.html"), []byte("<p>Run by <b>Marc</b>; logs kept 30 days.</p>"), 0o644)
	f := newFixture(t, accounts.Open, func(c *Config) { c.Pages = dir })
	if _, body, _ := f.get(f.c, "/privacy"); !strings.Contains(body, "<p>Run by <b>Marc</b>; logs kept 30 days.</p>") {
		t.Errorf("privacy:\n%s", body)
	}
	if _, body, _ := f.get(f.c, "/terms"); !strings.Contains(body, "terms.html") {
		t.Errorf("terms default:\n%s", body)
	}
}

func TestHSTS(t *testing.T) {
	f := newFixture(t, accounts.Open, func(c *Config) { c.HSTS = true })
	if _, _, h := f.get(f.c, "/"); !strings.HasPrefix(h.Get("Strict-Transport-Security"), "max-age=") {
		t.Errorf("HSTS: %v", h)
	}
	g := newFixture(t, accounts.Open, nil)
	if _, _, h := g.get(g.c, "/"); h.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS on a self-signed site")
	}
}
