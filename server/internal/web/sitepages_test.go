package web

import (
	"strings"
	"testing"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/sitelinks"
)

// every page the login screen links to answers on the HTTP port, with the sign-up mode's text.
func TestSitePages(t *testing.T) {
	for _, c := range []struct {
		mode accounts.Mode
		want string
	}{
		{accounts.Open, "an account is made at its first login"},
		{accounts.Invite, "invite code from its operator"},
		{accounts.Closed, "Only this server's operator makes accounts"},
	} {
		h := &HTTP{Extra: SitePages(c.mode)}
		for _, page := range sitelinks.Pages {
			resp, log := roundTrip(t, h, "GET http://127.0.0.2:18080/dawngate/"+page+"?x=1 HTTP/1.1\r\nHost: 127.0.0.2:18080\r\n\r\n")
			if !strings.HasPrefix(resp, "HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\n") ||
				!strings.Contains(resp, "<!doctype html>") || strings.Contains(resp, "<script") || strings.Contains(resp, "<form") {
				t.Errorf("%v /dawngate/%s: %q", c.mode, page, resp)
			}
			if !strings.Contains(log, "(served)") {
				t.Errorf("log:\n%s", log)
			}
			if page == sitelinks.Signup && !strings.Contains(resp, c.want) {
				t.Errorf("%v: the sign-up page lacks %q:\n%s", c.mode, c.want, resp)
			}
		}
	}
	h := &HTTP{Extra: SitePages(accounts.Open)}
	if resp, _ := roundTrip(t, h, "GET /dawngate/other HTTP/1.1\r\n\r\n"); !strings.HasPrefix(resp, "HTTP/1.1 404") {
		t.Errorf("an unknown page: %q", resp)
	}
	resp, _ := roundTrip(t, h, "GET /dawngate/signup HTTP/1.1\r\n\r\n")
	if !strings.Contains(resp, "3 to 32 characters") || !strings.Contains(resp, "8 to 63") {
		t.Errorf("the open sign-up page lacks the rules:\n%s", resp)
	}
}
