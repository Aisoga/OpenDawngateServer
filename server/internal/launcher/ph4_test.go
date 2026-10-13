package launcher

import (
	"path/filepath"
	"strings"
	"testing"
)

// Start-Server.ini's website keys become dgserver flags; an ini of an earlier release (no keys) gets the website
// on 443 with a self-signed certificate.
func TestReadWebsite(t *testing.T) {
	for _, c := range []struct {
		cfg  Config
		want string
	}{
		{Config{}, "--web-port 443"},
		{Config{"web_port": "0"}, "--web-port 0"},
		{Config{"web_port": "8443", "web_domain": " Play.Example.org "}, "--web-port 8443 --web-domain play.example.org"},
		{Config{"web_domain": "play.example.org", "web_email": "op@example.org"},
			"--web-port 443 --web-domain play.example.org --web-email op@example.org"},
		{Config{"web_email": "op@example.org"}, "--web-port 443"},
	} {
		w, err := ReadWebsite(c.cfg)
		if err != nil || strings.Join(w.Flags(), " ") != c.want {
			t.Errorf("%v: %v %v, want %s", c.cfg, w.Flags(), err, c.want)
		}
	}
	for _, bad := range []Config{{"web_port": "https"}, {"web_port": "70000"}, {"web_port": "0", "web_domain": "a.org"},
		{"web_domain": "1.2.3.4"}, {"web_domain": "localhost"}, {"web_domain": "https://a.org/"},
		{"web_domain": "a.org:8443"}, {"web_domain": "-a.org"}, {"web_domain": "dä.org"}} {
		if _, err := ReadWebsite(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// Start-Client takes Start-Server.ini's web_domain only for this PC's own server's copy.
func TestOwnServerDomain(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "release")
	root := filepath.Join(exe, DefaultFolder)
	scfg := Config{"web_domain": "Play.Example.org"}
	for _, c := range []struct {
		scfg         Config
		root, server string
		want         string
	}{
		{scfg, root, "127.0.0.1", "play.example.org"},
		{nil, root, "127.0.0.1", ""},
		{scfg, root, "192.168.1.20", ""},                             // another server, the same folder
		{scfg, filepath.Join(t.TempDir(), "other"), "127.0.0.1", ""}, // another folder
		{Config{"web_domain": "play.example.org", "address": "192.168.1.20"}, root, "192.168.1.20", "play.example.org"},
		{Config{"web_domain": "play.example.org", "folder": "elsewhere"}, root, "127.0.0.1", ""},
		{Config{"web_domain": "play.example.org", "web_port": "0"}, root, "127.0.0.1", ""}, // an invalid ini
		{Config{}, root, "127.0.0.1", ""},
	} {
		if got := ownServerDomain(c.scfg, exe, c.root, c.server); got != c.want {
			t.Errorf("%v %s %s: %q, want %q", c.scfg, c.root, c.server, got, c.want)
		}
	}
}
