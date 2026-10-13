package sitelinks

import "testing"

// the website's own domain (dgserver --web-domain, Start-Server.ini's web_domain) is CheckDomain's rule without a
// port and without an IP address.
func TestWebsiteDomain(t *testing.T) {
	for in, want := range map[string]string{"": "", " Play.Example.ORG ": "play.example.org", "a.b": "a.b",
		"dawngate.example-1.org": "dawngate.example-1.org"} {
		if got, err := WebsiteDomain(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"a.b:8443", "1.2.3.4", "localhost", "-a.b", "a-.b", "a..b", "https://a.b/", "a.b/c",
		"dä.de", "a b.c", "a.b:"} {
		if got, err := WebsiteDomain(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
	// the links' rule keeps a port (a website on another port, e.g. a test)
	if d, err := CheckDomain("a.b:8443"); err != nil || d != "a.b:8443" {
		t.Errorf("CheckDomain a.b:8443: %q %v", d, err)
	}
	if b, err := Base("127.0.0.1", HTTPPort, " "); err == nil {
		t.Errorf("Base with a blank domain: %q", b)
	}
}

// Every page has its link, in Pages' order; the fallback base is dgserver's HTTP port.
func TestBase(t *testing.T) {
	if b, err := Base("192.168.1.20", HTTPPort, ""); err != nil || b != "http://192.168.1.20:18080/dawngate/" {
		t.Errorf("fallback: %q %v", b, err)
	}
	if b, err := Base("192.168.1.20", HTTPPort, "Dawngate.Example.org"); err != nil || b != "https://dawngate.example.org/" {
		t.Errorf("domain: %q %v", b, err)
	}
	if len(Pages) != 5 || Pages[0] != Index {
		t.Errorf("pages %q", Pages)
	}
}
