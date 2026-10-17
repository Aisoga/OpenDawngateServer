package site

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
)

func TestForwardedAddr(t *testing.T) {
	for _, c := range []struct {
		xff  []string
		real string
		want string // "" = none
	}{
		{nil, "", ""},
		{[]string{"203.0.113.9"}, "", "203.0.113.9"},
		// a browser's own X-Forwarded-For comes first; the proxy appends the address it saw: only that one counts
		{[]string{"10.0.0.1, 6.6.6.6, 203.0.113.9"}, "", "203.0.113.9"},
		{[]string{"6.6.6.6", "203.0.113.9"}, "", "203.0.113.9"}, // several header lines: the last one's
		{[]string{" 203.0.113.9:51000 "}, "", "203.0.113.9"},
		{[]string{"[2001:db8::7]:51000"}, "", "2001:db8::7"},
		{[]string{"2001:db8::7"}, "", "2001:db8::7"},
		{[]string{"::ffff:203.0.113.9"}, "", "203.0.113.9"},
		{nil, "198.51.100.4", "198.51.100.4"},
		{[]string{"6.6.6.6, unknown"}, "198.51.100.4", "198.51.100.4"}, // no address last: X-Real-IP
		{[]string{"6.6.6.6, unknown"}, "", ""},
		{[]string{"203.0.113.9"}, "198.51.100.4", "203.0.113.9"},
		{nil, "not an address", ""},
		{[]string{"fe80::1%eth0"}, "", ""},
	} {
		h := http.Header{}
		for _, v := range c.xff {
			h.Add("X-Forwarded-For", v)
		}
		if c.real != "" {
			h.Set("X-Real-IP", c.real)
		}
		a, ok := forwardedAddr(h)
		got := ""
		if ok {
			got = a.String()
		}
		if got != c.want {
			t.Errorf("%q / %q: %q, want %q", c.xff, c.real, got, c.want)
		}
	}
}

// proxyFixture is a site behind a reverse proxy: plain HTTP on loopback, the test's client playing the proxy (it
// sets the forwarding headers itself).
type proxyFixture struct {
	t     *testing.T
	site  *Site
	srv   *httptest.Server
	log   []string
	logMu sync.Mutex
}

func newProxyFixture(t *testing.T, mut func(*Config)) *proxyFixture {
	t.Helper()
	f := &proxyFixture{t: t}
	ff := newFixture(t, accounts.Open, func(c *Config) {
		c.Proxy, c.PublicURL, c.HSTS = true, "https://play.example.org/", true
		c.Log = func(format string, a ...any) {
			f.logMu.Lock()
			f.log = append(f.log, fmt.Sprintf(format, a...))
			f.logMu.Unlock()
		}
		if mut != nil {
			mut(c)
		}
	})
	f.site = ff.site
	f.srv = httptest.NewServer(f.site) // plain HTTP, as behind the proxy
	t.Cleanup(f.srv.Close)
	return f
}

func (f *proxyFixture) logText() string {
	f.logMu.Lock()
	defer f.logMu.Unlock()
	return strings.Join(f.log, "\n")
}

// do sends a request as the proxy would forward it: the player's address and the scheme in the headers.
func (f *proxyFixture) do(c *http.Client, method, path, from string, body url.Values) (int, string, http.Header) {
	f.t.Helper()
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(body.Encode())
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://play.example.org") // the browser's, at the public address
	}
	if from != "" {
		req.Header.Set("X-Forwarded-For", "6.6.6.6, "+from) // a spoofed entry first, the proxy's last
		req.Header.Set("X-Real-IP", from)
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	res, err := c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

// browser is a client whose cookies the test carries itself: the site's cookies are Secure, which a cookie jar
// never sends over the test's plain HTTP (the proxy's side); the browser sees HTTPS.
type browser struct {
	c      *http.Client
	cookie string
}

func newBrowser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{c: &http.Client{Jar: jar, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (f *proxyFixture) submit(b *browser, path, from string, fields map[string]string) (int, string) {
	f.t.Helper()
	req, _ := http.NewRequest("GET", f.srv.URL+path, nil)
	req.Header.Set("X-Forwarded-For", from)
	res, err := b.c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == CSRFCookie {
			if !c.Secure {
				f.t.Error("the CSRF cookie isn't Secure behind the proxy")
			}
			b.cookie = c.Value
		}
	}
	m := tokenRE.FindStringSubmatch(string(page))
	if m == nil {
		f.t.Fatalf("%s: no CSRF token:\n%s", path, page)
	}
	v := url.Values{CSRFField: {m[1]}}
	for k, x := range fields {
		v.Set(k, x)
	}
	req, _ = http.NewRequest("POST", f.srv.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://play.example.org")
	req.Header.Set("X-Forwarded-For", "10.9.9.9, "+from)
	req.AddCookie(&http.Cookie{Name: CSRFCookie, Value: b.cookie})
	res, err = b.c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(body)
}

// Behind the proxy the player's address is the one the proxy appended: the log, the per-address sign-up cap and
// the form rate count it, never the proxy's loopback address or what the browser wrote itself.
func TestProxyClientAddress(t *testing.T) {
	f := newProxyFixture(t, func(c *Config) { c.Policy.SignupsPerAddress = 2 })
	b := newBrowser()
	for i, want := range []int{200, 200, 429} { // the third account of one address a day is refused
		code, page := f.submit(b, "/signup", "203.0.113.9", register(fmt.Sprintf("proxied%d", i), "", "dawngate1", ""))
		if code != want {
			t.Fatalf("signup %d: %d, want %d\n%s", i, code, want, page)
		}
	}
	// another player behind the same proxy is another address
	if code, page := f.submit(b, "/signup", "198.51.100.4", register("proxied9", "", "dawngate1", "")); code != 200 {
		t.Fatalf("another address: %d\n%s", code, page)
	}
	l := f.logText()
	for _, want := range []string{"203.0.113.9 POST /signup -> 200: account", "198.51.100.4 POST /signup -> 200: account"} {
		if !strings.Contains(l, want) {
			t.Errorf("log lacks %q:\n%s", want, l)
		}
	}
	if strings.Contains(l, "127.0.0.1 ") || strings.Contains(l, "6.6.6.6") || strings.Contains(l, "10.9.9.9") {
		t.Errorf("the proxy's or a spoofed address in the log:\n%s", l)
	}
}

// The forwarding headers count only from a loopback peer and only behind a proxy: another peer, or the site on the
// network itself, keeps the TCP peer's address.
func TestProxyHeadersTrust(t *testing.T) {
	st := newFixture(t, accounts.Open, nil).st
	for _, c := range []struct {
		proxy bool
		peer  string
		want  string
	}{
		{true, "127.0.0.1:40000", "203.0.113.9"},
		{true, "127.0.0.5:40000", "203.0.113.9"},
		{true, "[::1]:40000", "203.0.113.9"},
		{true, "192.0.2.50:40000", "192.0.2.50"}, // not the proxy: its headers are anyone's
		{true, "10.0.0.2:40000", "10.0.0.2"},
		{false, "127.0.0.1:40000", "127.0.0.1"}, // the site itself on the network: no proxy to trust
		{false, "192.0.2.50:40000", "192.0.2.50"},
	} {
		var lines []string
		s, err := New(Config{Store: st, Proxy: c.proxy,
			PublicURL: map[bool]string{true: "https://play.example.org/"}[c.proxy],
			Log:       func(format string, a ...any) { lines = append(lines, fmt.Sprintf(format, a...)) }})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("GET", "/status", nil)
		req.RemoteAddr = c.peer
		req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
		req.Header.Set("X-Real-IP", "203.0.113.9")
		req.Header.Set("X-Forwarded-Proto", "http") // only a trusted proxy's redirects
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if len(lines) != 1 || !strings.HasPrefix(lines[0], c.want+" GET /status -> ") {
			t.Errorf("proxy %v, peer %s: %q, want %s", c.proxy, c.peer, lines, c.want)
		}
		trusted := c.proxy && c.want == "203.0.113.9"
		if (w.Code == http.StatusPermanentRedirect) != trusted {
			t.Errorf("proxy %v, peer %s: %d", c.proxy, c.peer, w.Code)
		}
		if req.RemoteAddr != c.peer {
			t.Errorf("the server's request changed: %s", req.RemoteAddr)
		}
	}
}

// A request the proxy forwarded over plain HTTP goes to the same path at the public HTTPS address; without the
// header the proxy terminated HTTPS. The pages keep their HTTPS headers (HSTS) behind the proxy.
func TestProxyRedirectsToHTTPS(t *testing.T) {
	f := newProxyFixture(t, nil)
	c := newBrowser().c
	req, _ := http.NewRequest("GET", f.srv.URL+"/signup?x=1", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusPermanentRedirect || res.Header.Get("Location") != "https://play.example.org/signup?x=1" {
		t.Errorf("plain HTTP: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	code, _, h := f.do(c, "GET", "/signup", "203.0.113.9", nil)
	if code != 200 || h.Get("Strict-Transport-Security") == "" {
		t.Errorf("https: %d %v", code, h)
	}
	if code, _, _ := f.do(c, "GET", "/", "", nil); code != 200 { // no headers at all: served
		t.Errorf("no headers: %d", code)
	}
	if !strings.Contains(f.logText(), "203.0.113.9 GET /signup -> 308: plain HTTP at the proxy: redirected to HTTPS") {
		t.Errorf("log:\n%s", f.logText())
	}
}

// The browser's Origin is the public address while the Host the proxy passes may be its upstream's: a form posted
// from the public site is taken, one from another site isn't.
func TestProxyOrigin(t *testing.T) {
	f := newProxyFixture(t, nil)
	b := newBrowser()
	if code, page := f.submit(b, "/signup", "203.0.113.9", register("origin1", "", "dawngate1", "")); code != 200 {
		t.Fatalf("from the public site: %d\n%s", code, page)
	}
	req, _ := http.NewRequest("POST", f.srv.URL+"/signup", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.AddCookie(&http.Cookie{Name: CSRFCookie, Value: b.cookie})
	res, err := b.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(f.logText(), "refused: Origin https://evil.example.com") {
		t.Errorf("another site: %d\n%s", res.StatusCode, f.logText())
	}
}

type fakeConns struct {
	mu    sync.Mutex
	asked []string
	open  map[string]int
	max   int
}

func (c *fakeConns) Admit(addr string) (func(), bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, addr)
	if c.open[addr] >= c.max {
		return nil, false, fmt.Sprintf("%d connections open from %s", c.open[addr], addr)
	}
	c.open[addr]++
	return func() { c.mu.Lock(); c.open[addr]--; c.mu.Unlock() }, true, ""
}

// The per-address connection rule is asked per request with the player's address, and released after it.
func TestProxyConnLimits(t *testing.T) {
	conns := &fakeConns{open: map[string]int{}, max: 1}
	f := newProxyFixture(t, func(c *Config) { c.Conns = conns })
	c := newBrowser().c
	for i := 0; i < 3; i++ {
		if code, _, _ := f.do(c, "GET", "/status", "203.0.113.9", nil); code != 200 {
			t.Errorf("request %d: %d", i, code)
		}
	}
	conns.mu.Lock()
	if strings.Join(conns.asked, " ") != "203.0.113.9 203.0.113.9 203.0.113.9" || conns.open["203.0.113.9"] != 0 {
		t.Errorf("asked %v, open %v", conns.asked, conns.open)
	}
	conns.open["198.51.100.4"] = 1 // that address holds its share
	conns.mu.Unlock()
	code, _, h := f.do(c, "GET", "/status", "198.51.100.4", nil)
	if code != http.StatusTooManyRequests || h.Get("Retry-After") == "" {
		t.Errorf("over the limit: %d %v", code, h)
	}
	if !strings.Contains(f.logText(), "198.51.100.4 GET /status -> 429: refused: limits: 1 connections open from 198.51.100.4") {
		t.Errorf("log:\n%s", f.logText())
	}
}
