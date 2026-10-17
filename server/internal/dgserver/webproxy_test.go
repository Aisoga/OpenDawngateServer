package dgserver

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/limits"
	"dawngate/server/internal/site"
)

// The website behind a reverse proxy: plain HTTP on a loopback address, https://<domain>/ everywhere, no
// certificate of its own; the players' addresses from the proxy's headers, the limits per player address.
func TestWebsiteProxy(t *testing.T) {
	data := t.TempDir()
	cfg := config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), dataDir: data, limitsMode: limits.On,
		web: websiteConfig{domain: "play.example.org", proxy: true}}
	in := startWeb(t, &cfg)
	defer in.stop()
	port := cfg.web.port
	listen := "127.0.0.1:" + strconv.Itoa(port)
	info := in.info(cfg)
	if info.WebURL != "https://play.example.org/" || info.WebProxy != listen || info.WebFingerprint != "" {
		t.Fatalf("info: %+v", info)
	}
	if _, err := os.Stat(filepath.Join(data, "site", site.SelfSignedFile)); err == nil {
		t.Error("a self-signed certificate behind the proxy")
	}
	c := &http.Client{Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(u, from string) (int, http.Header) {
		t.Helper()
		req, _ := http.NewRequest("GET", u, nil)
		if from != "" {
			req.Header.Set("X-Forwarded-For", "6.6.6.6, "+from)
			req.Header.Set("X-Real-IP", from)
			req.Header.Set("X-Forwarded-Proto", "https")
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res.StatusCode, res.Header
	}
	if code, h := get("http://"+listen+"/status", "203.0.113.9"); code != 200 || h.Get("Strict-Transport-Security") == "" {
		t.Errorf("/status over plain HTTP: %d %v", code, h)
	}
	// no TLS on the port
	if conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", listen,
		&tls.Config{InsecureSkipVerify: true}); err == nil {
		conn.Close()
		t.Error("a TLS handshake behind the proxy")
	}
	// the HTTP port's redirects of the login screen's links name the public address
	hp := in.ports[3]
	if code, h := get("http://127.0.0.1:"+strconv.Itoa(hp)+"/dawngate/signup", ""); code != 302 ||
		h.Get("Location") != "https://play.example.org/signup" {
		t.Errorf("redirect: %d %s", code, h.Get("Location"))
	}
	// the per-address connection rule (30 at once, then one a second) counts each player's address, not the proxy's
	refused := 0
	for i := 0; i < 40; i++ {
		if code, _ := get("http://"+listen+"/status", "198.51.100.4"); code == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Error("no request of one address refused")
	}
	if code, _ := get("http://"+listen+"/status", "192.0.2.77"); code != 200 {
		t.Errorf("another address refused too: %d", code)
	}
	in.stop()
	in = &instance{log: in.log}
	b, _ := os.ReadFile(in.log.Path)
	l := string(b)
	for _, want := range []string{"website on https://play.example.org/ behind a reverse proxy (--web-proxy): plain HTTP on " + listen,
		"[web] 203.0.113.9 GET /status -> 200", "[web] 198.51.100.4 GET /status -> 429: refused: limits:",
		"[web] 192.0.2.77 GET /status -> 200"} {
		if !strings.Contains(l, want) {
			t.Errorf("log lacks %q", want)
		}
	}
	if strings.Contains(l, "[web] 127.0.0.1 GET") || strings.Contains(l, "6.6.6.6") {
		t.Error("the proxy's or a spoofed address in the log")
	}
}

func TestWebsiteProxyFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--web-proxy", "--web-port", "8095"},                                                        // no domain
		{"--web-proxy", "--web-port", "8095", "--web-domain", "a.org", "--web-bind", "0.0.0.0"},      // not loopback
		{"--web-proxy", "--web-port", "8095", "--web-domain", "a.org", "--web-bind", "192.0.2.1"},    // a network address
		{"--web-proxy", "--web-port", "8095", "--web-domain", "a.org", "--web-bind", "::1"},          // IPv4 only
		{"--web-proxy", "--web-port", "8095", "--web-domain", "a.org", "--web-bind", "localhost"},    // an address, not a name
		{"--web-proxy", "--web-port", "8095", "--web-domain", "a.org", "--web-email", "op@a.org"},    // the proxy's certificate
		{"--web-proxy", "--web-port", "8095", "--web-domain", "a.org", "--web-acme-url", "http://x"}, // the same
		{"--web-port", "8095", "--web-bind", "127.0.0.1"},                                            // --web-bind alone
	} {
		if code := Main(append(args, "--print-ports")); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	for _, args := range [][]string{
		{"--web-proxy", "--web-port", "8095", "--web-domain", "a.org"},
		{"--web-proxy", "--web-port", "8095", "--web-domain", "a.org", "--web-bind", "127.0.0.4"},
		{"--web-proxy", "--web-port", "0"}, // no website: nothing to check
	} {
		if code := Main(append(args, "--print-ports")); code != 0 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	wc := websiteConfig{port: 8095, domain: "a.org", proxy: true}
	if err := checkWebProxy(&wc); err != nil || wc.bind != DefaultWebProxyBind {
		t.Errorf("default bind: %q %v", wc.bind, err)
	}
	// start refuses a network address too (a config not made by the flags)
	_, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(),
		web: websiteConfig{port: freePort(t), domain: "a.org", proxy: true, bind: "0.0.0.0"}})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Errorf("start on 0.0.0.0: %v", err)
	}
}
