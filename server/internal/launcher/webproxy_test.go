package launcher

import (
	"bytes"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"dawngate/server/internal/dgserver"
)

// web_proxy = on: dgserver's proxy flags, no e-mail for Let's Encrypt; it needs web_domain and a web_port of its own
// above 1023 (the proxy holds 443).
func TestReadWebsiteProxy(t *testing.T) {
	for _, c := range []struct {
		cfg  Config
		want string
	}{
		{Config{"web_proxy": "on", "web_port": "8095", "web_domain": "Play.Example.org"},
			"--web-port 8095 --web-domain play.example.org --web-proxy"},
		{Config{"web_proxy": "on", "web_port": "8095", "web_domain": "play.example.org", "web_email": "op@example.org"},
			"--web-port 8095 --web-domain play.example.org --web-proxy"},
		{Config{"web_proxy": "off", "web_port": "8095", "web_domain": "play.example.org"},
			"--web-port 8095 --web-domain play.example.org"},
		{Config{"web_proxy": "on", "web_port": "0"}, "--web-port 0"}, // no website
	} {
		w, err := ReadWebsite(c.cfg)
		if err != nil || strings.Join(w.Flags(), " ") != c.want {
			t.Errorf("%v: %v %v, want %s", c.cfg, w.Flags(), err, c.want)
		}
	}
	if w, _ := ReadWebsite(Config{"web_proxy": "yes", "web_port": "8095", "web_domain": "a.org"}); w.ProxyAddress() != "127.0.0.1:8095" {
		t.Errorf("proxy address %s", w.ProxyAddress())
	}
	for bad, why := range map[string]Config{
		"needs web_domain": {"web_proxy": "on", "web_port": "8095"},
		"above 1023":       {"web_proxy": "on", "web_domain": "a.org"}, // the default 443
		"use on or off":    {"web_proxy": "maybe", "web_port": "8095", "web_domain": "a.org"},
	} {
		if _, err := ReadWebsite(why); err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("%v: %v", why, err)
		}
	}
}

func TestWebsiteBanner(t *testing.T) {
	var b bytes.Buffer
	websiteBanner(&b, dgserver.Info{WebURL: "https://play.example.org/", WebProxy: "127.0.0.1:8095"}, true)
	out := b.String()
	for _, want := range []string{"The website: https://play.example.org/ (behind your reverse proxy on 127.0.0.1:8095).",
		"The admin page: https://play.example.org/admin", "your proxy holds its certificate"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "Let's Encrypt") || strings.Contains(out, "self-signed") {
		t.Errorf("another certificate named:\n%s", out)
	}
	b.Reset()
	websiteBanner(&b, dgserver.Info{WebURL: "https://play.example.org/"}, true)
	if !strings.Contains(b.String(), "(registration, password change, account deletion, status)") ||
		!strings.Contains(b.String(), "Let's Encrypt") {
		t.Errorf("a domain's website:\n%s", b.String())
	}
	b.Reset()
	websiteBanner(&b, dgserver.Info{}, true)
	if b.Len() != 0 {
		t.Errorf("no website: %q", b.String())
	}
	b.Reset()
	ServerRiskNotice(&b, RiskInfo{Address: "203.0.113.7", Signup: "invite", WebDomain: "play.example.org", WebProxy: true})
	if !strings.Contains(b.String(), "TCP 443 (the website, served by your reverse proxy, which holds its certificate)") {
		t.Errorf("risk notice:\n%s", b.String())
	}
}

// Start-Server with the public profile and the website behind a reverse proxy: --check names it, the banner gives
// the public address and the loopback port, and a held port stops the start.
func TestStartServerBehindProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.1's server ports")
	}
	dir := release(t)
	cfg := filepath.Join(dir, ServerConfigName)
	if code, out := run(t, RunServer, dir, "--check", "--non-interactive"); code != 1 {
		t.Fatalf("--check: exit %d\n%s", code, out)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0") // a free port for the website, held until the last start
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	listen := "127.0.0.1:" + port
	for k, v := range map[string]string{"address": testAddress, "profile": ProfilePublic, "signup": "invite",
		"gateway_port": "42197", "web_proxy": "on", "web_port": port, "web_domain": "play.example.org"} {
		if err := SetKey(cfg, k, v); err != nil {
			t.Fatal(err)
		}
	}
	code, out := run(t, RunServer, dir, "--non-interactive", "--seconds", "1")
	if code != 1 || !strings.Contains(out, "Another program holds the website's port on 127.0.0.1") ||
		!strings.Contains(out, "web_proxy = on: your reverse proxy forwards https://play.example.org/ to http://"+listen) {
		t.Fatalf("a held port: exit %d\n%s", code, out)
	}
	ln.Close()
	if code, out = run(t, RunServer, dir, "--non-interactive", "--setup-only"); code != 0 {
		t.Fatalf("--setup-only: exit %d\n%s", code, out)
	}
	if code, out = run(t, RunServer, dir, "--check", "--non-interactive"); code != 0 ||
		!strings.Contains(out, "  website  https://play.example.org/ (behind your reverse proxy on "+listen+")") {
		t.Errorf("--check: exit %d\n%s", code, out)
	}
	code, out = run(t, RunServer, dir, "--non-interactive", "--seconds", "1")
	if code != 0 {
		t.Fatalf("Start-Server: exit %d\n%s", code, out)
	}
	for _, want := range []string{"The website: https://play.example.org/ (behind your reverse proxy on " + listen + ").",
		"The admin page: https://play.example.org/admin", "the public profile: the tunnel, the limits"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "self-signed") || strings.Contains(out, "note: another program holds the website's port") {
		t.Errorf("the banner:\n%s", out)
	}
}
