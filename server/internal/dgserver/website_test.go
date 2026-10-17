package dgserver

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/limits"
	"dawngate/server/internal/site"
	"dawngate/server/internal/sitelinks"
	"dawngate/server/internal/web"
)

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// startWeb starts cfg with its website on a free port (cfg.web.port). A port taken between freePort and the start
// (another test's connection may get it) is tried again with another one and a fresh log folder.
func startWeb(t *testing.T, cfg *config) *instance {
	t.Helper()
	for try := 0; ; try++ {
		cfg.web.port = freePort(t)
		in, err := start(*cfg)
		if err == nil {
			return in
		}
		if try == 4 || !strings.Contains(err.Error(), "the website's port: ") {
			t.Fatal(err)
		}
		if cfg.logDir != "" {
			cfg.logDir = t.TempDir()
		}
	}
}

// TestWebsite runs dgserver with its website and a self-signed certificate: a browser that trusts it registers
// an account in invite mode, the account logs in as the game's login does, and the HTTP port sends the client's
// login-screen links there.
func TestWebsite(t *testing.T) {
	data := t.TempDir()
	cfg := config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), dataDir: data,
		accounts: accounts.Policy{Mode: accounts.Invite}}
	in := startWeb(t, &cfg)
	defer in.stop()
	port := cfg.web.port
	info := in.info(cfg)
	base := "https://127.0.0.1:" + strconv.Itoa(port) + "/"
	if info.WebURL != base || len(info.WebFingerprint) != 95 || info.TCPPorts[len(info.TCPPorts)-1] != port {
		t.Fatalf("info: %+v", info)
	}
	// a browser that accepted the self-signed certificate (the PEM in <data>\site)
	pemBytes, err := os.ReadFile(filepath.Join(data, "site", site.SelfSignedFile))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatal("no certificate in the PEM")
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(u string) (int, string, http.Header) {
		res, err := c.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Header
	}
	code, page, _ := get(base + "signup")
	if code != 200 || !strings.Contains(page, `name="code"`) {
		t.Fatalf("/signup: %d\n%s", code, page)
	}
	invite, _, err := accounts.NewInvite(in.store, 1, 0, "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tok := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page)[1]
	res, err := c.PostForm(base+"signup", url.Values{"csrf": {tok}, "name": {"webuser"}, "display": {"WebUser"},
		"password": {"dawngate1"}, "password2": {"dawngate1"}, "code": {invite}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "Account made") {
		t.Fatalf("register: %d\n%s", res.StatusCode, body)
	}
	if r := cfg.accounts.Login(in.store, "WebUser", "dawngate1", netip.MustParseAddr("127.0.0.1"), time.Now()); r.Code != 0 {
		t.Fatalf("login: %+v", r)
	}
	if code, js, _ := get(base + "status.json"); code != 200 || !strings.Contains(js, `"playersOnline":0`) ||
		!strings.Contains(js, `"signup":"invite"`) {
		t.Errorf("status.json: %d %s", code, js)
	}
	// the HTTP port's redirects: every fallback page's path (the SitePages, at the setups' links) goes to the
	// website's page of the same name, which answers (one list of names, sitelinks.Pages)
	hp := in.ports[3]
	fallbacks := web.SitePages(cfg.accounts.Mode)
	if len(fallbacks) != len(sitelinks.Pages) {
		t.Fatalf("%d fallback pages", len(fallbacks))
	}
	for i, f := range fallbacks {
		p := sitelinks.Pages[i]
		res, err := c.Get("http://127.0.0.1:" + strconv.Itoa(hp) + "/" + f.Path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 302 || res.Header.Get("Location") != base+p {
			t.Errorf("/%s: %d %s", f.Path, res.StatusCode, res.Header.Get("Location"))
			continue
		}
		if code, _, _ := get(res.Header.Get("Location")); code != 200 {
			t.Errorf("the website's %q: %d", p, code)
		}
	}
	in.stop()
	in = &instance{log: in.log} // stopped; the deferred stop has nothing left to close
	b, _ := os.ReadFile(in.log.Path)
	l := string(b)
	for _, want := range []string{"website (PH4) on " + base, "self-signed certificate for localhost, 127.0.0.1",
		"[web] 127.0.0.1 POST /signup -> 200: account", "the HTTP port's /dawngate/<page> redirect there"} {
		if !strings.Contains(l, want) {
			t.Errorf("log lacks %q", want)
		}
	}
	if strings.Contains(l, "dawngate1") {
		t.Error("a password in the log")
	}
	// the next start keeps the certificate
	in2, err := start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer in2.stop()
	if fp := in2.info(cfg).WebFingerprint; fp != info.WebFingerprint {
		t.Errorf("a new certificate at the restart: %s", fp)
	}
}

// TestWebsiteLimits: with --limits on, the website's password forms take the game login's backoff per account:
// after five wrong passwords the account is locked for 30 s, also for the right one.
func TestWebsiteLimits(t *testing.T) {
	cfg := config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), dataDir: t.TempDir(), limitsMode: limits.On}
	in := startWeb(t, &cfg)
	defer in.stop()
	port := cfg.web.port
	if r := cfg.accounts.Register(in.store, "locked", "", "dawngate1", "", netip.Addr{}, time.Now()); r.Code != 0 {
		t.Fatal(r.Note)
	}
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // the certificate isn't this test's point
	base := "https://127.0.0.1:" + strconv.Itoa(port) + "/account/data"
	try := func(pw string) int {
		res, err := c.Get(base)
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(res.Body)
		res.Body.Close()
		tok := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(string(page))[1]
		res, err = c.PostForm(base, url.Values{"csrf": {tok}, "name": {"locked"}, "password": {pw}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for i := 0; i < 6; i++ {
		if code := try("wrong-pass"); code != 401 {
			t.Fatalf("wrong %d: %d", i, code)
		}
	}
	if code := try("dawngate1"); code != 429 {
		t.Errorf("locked account: %d", code)
	}
}

func TestWebsiteFlags(t *testing.T) {
	for _, args := range [][]string{{"--web-domain", "not a domain", "--web-port", "443"}, {"--web-domain", "1.2.3.4", "--web-port", "443"},
		{"--web-domain", "play.example.org"}, {"--web-port", "70000"}} {
		if code := Main(append(args, "--print-ports")); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if d, err := checkWebDomain(" Play.Example.ORG "); err != nil || d != "play.example.org" {
		t.Errorf("%q %v", d, err)
	}
	cfg := config{redirectorPort: 1, blazePort: 2, qosPort: 3, httpPort: 4, telemetryPort: 5, web: websiteConfig{port: 443}}
	if ps := cfg.printPorts(); ps[len(ps)-1] != 443 {
		t.Errorf("print-ports: %v", ps)
	}
	// a taken website port fails the start (nothing half started)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	held := ln.Addr().(*net.TCPAddr).Port
	if _, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), web: websiteConfig{port: held}}); err == nil ||
		!strings.Contains(err.Error(), "website's port") {
		t.Errorf("a held port: %v", err)
	}
}

// a domain's website is https://<domain>/ whatever --web-port is (Let's Encrypt and the players reach it on 443,
// which the router forwards), the URL the setups' links use (sitelinks.Base), and the HTTP port redirects there. The
// ACME directory is a closed loopback port: nothing leaves the PC.
func TestWebsiteDomainURL(t *testing.T) {
	data := t.TempDir()
	cfg := config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), dataDir: data,
		web: websiteConfig{domain: "play.example.org", acmeURL: "http://127.0.0.1:1/directory"}}
	in := startWeb(t, &cfg)
	defer in.stop()
	link, _ := sitelinks.Base("203.0.113.7", sitelinks.HTTPPort, "play.example.org")
	if info := in.info(cfg); info.WebURL != link || info.WebFingerprint != "" {
		t.Errorf("info: %+v, want %s", info, link)
	}
	if to := in.website.redirects()["/dawngate/signup"]; to != link+"signup" {
		t.Errorf("redirect %q", to)
	}
}
