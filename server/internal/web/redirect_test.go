package web

import (
	"strings"
	"testing"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/sitelinks"
)

// the client's login-screen links (/dawngate/<page>) go to the website when dgserver runs one.
func TestRedirects(t *testing.T) {
	h := &HTTP{Redirects: map[string]string{"/dawngate/signup": "https://play.example.org/signup",
		"/dawngate/": "https://play.example.org/"},
		Extra: []Builtin{{Path: "dawngate/signup", ContentType: "text/html", Body: []byte("fallback")}}}
	resp, log := roundTrip(t, h, "GET /dawngate/signup?x=1 HTTP/1.1\r\nHost: 127.0.0.1:18080\r\n\r\n")
	body := "See https://play.example.org/signup\n"
	want := "HTTP/1.1 302 Found\r\nLocation: https://play.example.org/signup\r\nContent-Type: text/plain\r\n" +
		"Content-Length: 36\r\nConnection: close\r\n\r\n" + body
	if resp != want {
		t.Errorf("response:\n%q", resp)
	}
	if !strings.Contains(log, "-> 302 Found, to https://play.example.org/signup") {
		t.Errorf("log:\n%s", log)
	}
	if resp, _ := roundTrip(t, h, "HEAD /dawngate/ HTTP/1.1\r\n\r\n"); !strings.HasSuffix(resp, "Connection: close\r\n\r\n") ||
		!strings.Contains(resp, "Location: https://play.example.org/\r\n") {
		t.Errorf("HEAD:\n%q", resp)
	}
	// other paths are served as before
	if resp, _ := roundTrip(t, &HTTP{Extra: h.Extra}, "GET /dawngate/signup HTTP/1.1\r\n\r\n"); !strings.HasSuffix(resp, "fallback") {
		t.Errorf("without redirects:\n%q", resp)
	}
}

// with a website every fallback page of the login screen's links (SitePages, sitelinks.Pages) answers with the
// redirect to the website's page of the same name; without one, each is the fallback page.
func TestRedirectsOverSitePages(t *testing.T) {
	extra := SitePages(accounts.Invite)
	redirects := map[string]string{}
	for _, p := range sitelinks.Pages {
		redirects["/"+sitelinks.FallbackPrefix+p] = "https://play.example.org/" + p
	}
	if len(extra) != len(sitelinks.Pages) {
		t.Fatalf("%d fallback pages for %d links", len(extra), len(sitelinks.Pages))
	}
	for i, b := range extra {
		to, ok := redirects["/"+b.Path]
		if !ok || to != "https://play.example.org/"+sitelinks.Pages[i] {
			t.Errorf("fallback page %q has no redirect (%q)", b.Path, to)
		}
		req := "GET /" + b.Path + " HTTP/1.1\r\nHost: 127.0.0.1:18080\r\n\r\n"
		if resp, _ := roundTrip(t, &HTTP{Redirects: redirects, Extra: extra}, req); !strings.HasPrefix(resp, "HTTP/1.1 302 Found\r\nLocation: "+to+"\r\n") {
			t.Errorf("%s with a website:\n%q", b.Path, resp)
		}
		if resp, _ := roundTrip(t, &HTTP{Extra: extra}, req); !strings.HasPrefix(resp, "HTTP/1.1 200 OK\r\n") {
			t.Errorf("%s without a website:\n%q", b.Path, resp)
		}
	}
}
