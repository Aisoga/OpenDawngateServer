package site

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeNotice is AdminConfig.Notice for the tests: refuses what the server's check would (a stand-in for
// blaze.NoticeText), and every call after the first while tooSoon is set.
type fakeNotice struct {
	mu      sync.Mutex
	calls   []string // "<from>: <text>"
	tooSoon bool
}

func (f *fakeNotice) send(text, from string) (int, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.ContainsAny(text, "<>&~") {
		return 0, "", errors.New(`the game's chat reads '<' as markup; leave out the characters < > & ~`)
	}
	if f.tooSoon && len(f.calls) > 0 {
		return 0, "", NoticeTooSoon("the last notice went out less than 10s ago; send this one in 7 s")
	}
	f.calls = append(f.calls, from+": "+text)
	return 3, strings.TrimSpace(text), nil
}

func (f *fakeNotice) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// SYSCHAT: the message to all players: the page behind the admin session, the CSRF check, the confirmation, the
// refusals (empty, too long, the server's check, too soon: 429), the send with the admin's name, and its audit entry.
func TestAdminNotice(t *testing.T) {
	fn := &fakeNotice{tooSoon: true}
	af := newAdminFixture(t, func(c *Config) {
		c.Admin.Notice, c.Admin.NoticeMax = fn.send, 200
		c.Status = func() Status { return Status{PlayersOnline: 3} }
	})
	// without a session: the page redirects, the action is refused with the login form
	res, err := af.c.Get(af.srv.URL + "/admin/notice")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 {
		t.Errorf("the page without a session: %d", res.StatusCode)
	}
	if code, body, _ := af.adminPost(af.c, "/admin/notice", map[string]string{"text": "hello", "confirm": "yes"}); code != 403 ||
		!strings.Contains(body, "Your admin session has ended") {
		t.Errorf("the action without a session: %d\n%s", code, body)
	}
	af.login(af.c)
	code, body, _ := af.get(af.c, "/admin/notice")
	for _, want := range []string{`action="/admin/notice"`, `<textarea name="text" maxlength="200"`, "(3 now)",
		`name="confirm" value="yes"`, `name="csrf" value="`} {
		if code != 200 || !strings.Contains(body, want) {
			t.Errorf("the page: %d, lacks %q", code, want)
		}
	}
	if _, home, _ := af.get(af.c, "/admin"); !strings.Contains(home, `href="/admin/notice"`) {
		t.Error("the overview has no link to the message page")
	}
	// the CSRF check: a post without the token (or with another) is refused and sends nothing
	for _, tok := range []string{"", "AAAA"} {
		v := url.Values{"text": {"hello"}, "confirm": {"yes"}}
		if tok != "" {
			v.Set(CSRFField, tok)
		}
		if code, _, _ := af.post(af.c, "/admin/notice", v, nil); code != 403 {
			t.Errorf("token %q: %d", tok, code)
		}
	}
	// refusals
	for _, c := range []struct {
		fields map[string]string
		code   int
		text   string
	}{
		{map[string]string{"text": "  \r\n ", "confirm": "yes"}, 400, "Write the message first."},
		{map[string]string{"text": "Server update at 20:00 UTC"}, 400, "Tick the box beside Send to confirm."},
		{map[string]string{"text": strings.Repeat("x", 2001), "confirm": "yes"}, 400, "Not sent: the text is far too long."},
		{map[string]string{"text": "<b>hi</b>", "confirm": "yes"}, 400, "Not sent: the game&#39;s chat reads &#39;&lt;&#39; as markup"},
	} {
		code, body, _ := af.adminPost(af.c, "/admin/notice", c.fields)
		if code != c.code || !strings.Contains(body, c.text) {
			t.Errorf("%q: %d, want %d with %q\n%s", c.fields["text"], code, c.code, c.text, body)
		}
	}
	// the refused text comes back in the box, escaped (never as markup on the page)
	if _, body, _ := af.adminPost(af.c, "/admin/notice", map[string]string{"text": "<b>hi</b>", "confirm": "yes"}); !strings.Contains(body,
		">&lt;b&gt;hi&lt;/b&gt;</textarea>") || strings.Contains(body, "<b>hi</b>") {
		t.Errorf("the refused text isn't escaped in the box:\n%s", body)
	}
	if got := fn.sent(); len(got) != 0 {
		t.Fatalf("sent despite the refusals: %v", got)
	}
	// the send
	code, body, _ = af.adminPost(af.c, "/admin/notice", map[string]string{"text": "Server update at 20:00 UTC", "confirm": "yes"})
	if code != 200 || !strings.Contains(body, "Sent to 3 logged-in connection(s): &#34;Server update at 20:00 UTC&#34;") ||
		strings.Contains(body, ">Server update at 20:00 UTC</textarea>") {
		t.Errorf("send: %d\n%s", code, body)
	}
	if got := fn.sent(); len(got) != 1 || got[0] != "admin boss (account 1000000001): Server update at 20:00 UTC" {
		t.Errorf("Notice calls %v", got)
	}
	if e := af.lastAudit(); e.Action != "player notice" || e.Actor != "admin boss (account 1000000001)" ||
		e.Detail != `"Server update at 20:00 UTC" to 3 logged-in connection(s)` {
		t.Errorf("audit %+v", e)
	}
	// too soon: 429 with the server's words, nothing sent
	code, body, _ = af.adminPost(af.c, "/admin/notice", map[string]string{"text": "Again", "confirm": "yes"})
	if code != 429 || !strings.Contains(body, "send this one in 7 s") {
		t.Errorf("too soon: %d\n%s", code, body)
	}
	if got := fn.sent(); len(got) != 1 {
		t.Errorf("Notice calls %v", got)
	}
}

// A site without notices (AdminConfig.Notice nil) refuses the action with 409.
func TestAdminNoticeOff(t *testing.T) {
	af := newAdminFixture(t, nil)
	af.login(af.c)
	if code, body, _ := af.adminPost(af.c, "/admin/notice", map[string]string{"text": "hello", "confirm": "yes"}); code != 409 ||
		!strings.Contains(body, "can&#39;t send messages to the players") {
		t.Errorf("%d\n%s", code, body)
	}
}
