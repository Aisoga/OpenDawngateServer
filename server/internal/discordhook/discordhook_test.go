package discordhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const token = "s3cretT0ken-abcdefghijklmnopqrstuvwxyz_0123456789"

// receiver is a fake Discord webhook endpoint: it records the requests and answers each by its handler.
type receiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []request
	answer func(n int, r request, w http.ResponseWriter) // nil: 200 with the next message id
}

type request struct {
	Method, Path, Query string
	Body                map[string]any
	Raw                 string
}

func newReceiver(t *testing.T) *receiver {
	rc := &receiver{}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		req := request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Raw: string(b)}
		json.Unmarshal(b, &req.Body)
		rc.mu.Lock()
		rc.reqs = append(rc.reqs, req)
		n := len(rc.reqs)
		answer := rc.answer
		rc.mu.Unlock()
		if answer != nil {
			answer(n, req, w)
			return
		}
		fmt.Fprintf(w, `{"id":"%d","content":"x"}`, 1000+n)
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func (rc *receiver) url() string { return rc.srv.URL + "/api/webhooks/123456789012345678/" + token }

func (rc *receiver) requests() []request {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]request(nil), rc.reqs...)
}

// logs collects a hook's log lines.
type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, a...))
}

func (l *logs) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// sleeps records the waits instead of waiting.
type sleeps struct {
	mu sync.Mutex
	ds []time.Duration
}

func (s *sleeps) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.ds = append(s.ds, d)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *sleeps) list() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.ds...)
}

func wait(t *testing.T, ref *Ref) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ref.Wait(ctx)
}

func TestEncodeGolden(t *testing.T) {
	for _, c := range []struct {
		name string
		m    Message
		want string
	}{
		{"plain", Message{Content: "Server online (version 1.2.3)"},
			`{"content":"Server online (version 1.2.3)","allowed_mentions":{"parse":[]}}`},
		{"ping", Message{Content: RoleMention("987654321098765432") + " Ann is queuing for PvP", PingRoles: []string{"987654321098765432", "@everyone"}},
			`{"content":"\u003c@\u0026987654321098765432\u003e Ann is queuing for PvP","allowed_mentions":{"parse":[],"roles":["987654321098765432"]}}`},
		{"embed", Message{Embeds: []Embed{{Title: "PvP: Team 1 wins", Description: "Duration 31:05", Color: 0x2ecc71,
			Fields:    []Field{{Name: "Team 1", Value: "Ann (Fenmore) 5/1/7\n+ 3 bots", Inline: true}, {Name: "Team 2", Value: "", Inline: true}},
			Footer:    &Footer{Text: "OpenDawngate"},
			Timestamp: "2026-10-21T12:00:00Z"}}},
			`{"content":"","embeds":[{"title":"PvP: Team 1 wins","description":"Duration 31:05","color":3066993,"fields":[` +
				`{"name":"Team 1","value":"Ann (Fenmore) 5/1/7\n+ 3 bots","inline":true},{"name":"Team 2","value":"` + "\u200b" + `","inline":true}],` +
				`"footer":{"text":"OpenDawngate"},"timestamp":"2026-10-21T12:00:00Z"}],"allowed_mentions":{"parse":[]}}`},
	} {
		b, err := Encode(c.m)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, b, c.want)
		}
	}
}

func TestEncodeLimits(t *testing.T) {
	e := Embed{Title: strings.Repeat("t", 300)}
	for i := 0; i < 30; i++ {
		e.Fields = append(e.Fields, Field{Name: "n", Value: strings.Repeat("v", 2000)})
	}
	b, err := Encode(Message{Content: strings.Repeat("ä", 2500), Embeds: []Embed{e}})
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Content string
		Embeds  []Embed
	}
	json.Unmarshal(b, &p)
	if n := len([]rune(p.Content)); n != maxContent || !strings.HasSuffix(p.Content, "…") {
		t.Errorf("content %d runes", n)
	}
	if n := len([]rune(p.Embeds[0].Title)); n != maxTitle {
		t.Errorf("title %d runes", n)
	}
	if len(p.Embeds[0].Fields) != maxFields || len([]rune(p.Embeds[0].Fields[0].Value)) != maxFieldValue {
		t.Errorf("fields %d, value %d", len(p.Embeds[0].Fields), len([]rune(p.Embeds[0].Fields[0].Value)))
	}
}

func TestEscape(t *testing.T) {
	for in, want := range map[string]string{
		"Ann":                   "Ann",
		"@everyone":             "\\@\u200beveryone",
		"@here":                 "\\@\u200bhere",
		"<@123456>":             "\\<\\@\u200b123456\\>",
		"<@&42>":                "\\<\\@\u200b&42\\>",
		"**bold**":              "\\*\\*bold\\*\\*",
		"__x__ ~~y~~ ||z||":     "\\_\\_x\\_\\_ \\~\\~y\\~\\~ \\|\\|z\\|\\|",
		"`code`":                "\\`code\\`",
		"[x](https://e.org)":    "\\[x\\]\\(https\\://e\\.org\\)",
		"# head":                "\\# head",
		"line\nbreak":           "line break",
		"zero\u200bwidth":       "zerowidth",
		"   ":                   "?",
		"<t:1:R>":               "\\<t\\:1\\:R\\>",
		strings.Repeat("a", 40): strings.Repeat("a", 31) + "…",
	} {
		if got := Escape(in); got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckURL(t *testing.T) {
	ok := []string{
		"https://discord.com/api/webhooks/123456789012345678/" + token,
		"https://discordapp.com/api/webhooks/123/abc",
		"https://discord.com/api/v10/webhooks/123/abc",
		"  https://DISCORD.com/api/webhooks/123/abc  ",
	}
	for _, u := range ok {
		if err := CheckURL(u, false); err != nil {
			t.Errorf("%s refused: %v", u, err)
		}
	}
	bad := []string{
		"", "discord.com/api/webhooks/1/a", "http://discord.com/api/webhooks/1/a", "https://evil.com/api/webhooks/1/a",
		"https://discord.com.evil.com/api/webhooks/1/a", "https://discord.com:8443/api/webhooks/1/a",
		"https://discord.com/api/webhooks/1/a?wait=true", "https://discord.com/api/webhooks/x/a",
		"https://discord.com/api/webhooks/1/", "https://discord.com/api/webhooks/1/a/b", "https://u:p@discord.com/api/webhooks/1/a",
		"http://127.0.0.1:8080/api/webhooks/1/a", "https://discord.com/api/channels/1/a",
	}
	for _, u := range bad {
		if err := CheckURL(u, false); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
	if err := CheckURL("http://127.0.0.1:8080/api/webhooks/1/"+token, true); err != nil {
		t.Errorf("local receiver refused with allowLocal: %v", err)
	}
	for _, u := range []string{"http://localhost:8080/api/webhooks/1/a", "http://192.168.1.2/api/webhooks/1/a",
		"https://127.0.0.1/api/webhooks/1/a"} {
		if err := CheckURL(u, true); err == nil {
			t.Errorf("%q accepted with allowLocal", u)
		}
	}
	err := CheckURL("https://discord.com/api/webhooks/1/"+token+"?x=1", false)
	if err == nil || strings.Contains(err.Error(), token) {
		t.Errorf("error %v", err)
	}
}

func TestMask(t *testing.T) {
	if got := Mask("https://discord.com/api/webhooks/123456789012345678/" + token); got != "webhook id …5678" {
		t.Errorf("Mask = %q", got)
	}
	if got := Mask("http://127.0.0.1:8099/api/webhooks/42/" + token); got != "local test receiver 127.0.0.1:8099, webhook id 42" {
		t.Errorf("Mask local = %q", got)
	}
}

func TestURLsFor(t *testing.T) {
	d, q, r, s := "D", "Q", "R", "S"
	for _, c := range []struct {
		u                  URLs
		queue, result, srv string
	}{
		{URLs{}, "", "", ""},
		{URLs{Default: d}, d, d, ""}, // the server's status never takes the default
		{URLs{Default: d, Queue: q}, q, d, ""},
		{URLs{Default: d, Result: r, Server: s}, d, r, s},
		{URLs{Queue: q}, q, "", ""},
		{URLs{Server: s}, "", "", s},
	} {
		if c.u.For(Queue) != c.queue || c.u.For(Result) != c.result || c.u.For(Server) != c.srv {
			t.Errorf("%+v: %q %q %q", c.u, c.u.For(Queue), c.u.For(Result), c.u.For(Server))
		}
	}
}

func TestOpenSharesAndDescribes(t *testing.T) {
	a := "https://discord.com/api/webhooks/111111/" + token
	b := "https://discord.com/api/webhooks/222222/" + token + "b"
	s := DefaultSettings()
	s.URLs = URLs{Default: a, Server: b}
	s.PingRole = "333"
	hs, err := Open(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer hs.Close(0)
	if hs.Hook(Queue) == nil || hs.Hook(Queue) != hs.Hook(Result) || hs.Hook(Server) == hs.Hook(Queue) || len(hs.all) != 2 {
		t.Errorf("hooks: %v %v %v (%d)", hs.Hook(Queue), hs.Hook(Result), hs.Hook(Server), len(hs.all))
	}
	d := hs.Describe()
	want := "discord webhooks: queue on (webhook id …1111), result on (webhook id …1111), server on (webhook id …2222, " +
		"only its own webhook); ping role 333 (at most every 15m0s, not within 30s of the start); player names; " +
		"the queue message edited live; results of pvp,custom"
	if d != want {
		t.Errorf("Describe:\n got %s\nwant %s", d, want)
	}
	if strings.Contains(d, token) {
		t.Error("the token is in the log line")
	}

	none, err := Open(DefaultSettings(), Options{})
	if err != nil || none.Any() || none.Hook(Queue) != nil {
		t.Fatalf("no URL: %v %v", none, err)
	}
	if got := none.Describe(); got != "discord webhooks: off (queue off, result off, server off)" {
		t.Errorf("off: %s", got)
	}
	var nilHooks *Hooks
	if nilHooks.Hook(Server).Post(Message{Content: "x"}) != nil || nilHooks.Any() {
		t.Error("a nil Hooks posts")
	}
	nilHooks.Close(time.Second)

	bad := DefaultSettings()
	bad.URLs.Result = "https://example.com/api/webhooks/1/" + token
	if _, err := Open(bad, Options{}); err == nil || !strings.Contains(err.Error(), "discord-webhook-result") || strings.Contains(err.Error(), token) {
		t.Errorf("bad URL: %v", err)
	}
	bad = DefaultSettings()
	bad.PingRole = "@everyone"
	if _, err := Open(bad, Options{}); err == nil {
		t.Error("role @everyone accepted")
	}
}

func TestParseSettings(t *testing.T) {
	if m, err := ParseModes(" PvP, custom,practice,pvp "); err != nil || strings.Join(m, ",") != "pvp,custom,practice" {
		t.Errorf("modes %v %v", m, err)
	}
	if m, err := ParseModes("none"); err != nil || len(m) != 0 {
		t.Errorf("none: %v %v", m, err)
	}
	if _, err := ParseModes("pvp,ranked"); err == nil {
		t.Error("ranked accepted")
	}
	for in, want := range map[string]bool{"on": true, "OFF": false, "yes": true, "0": false} {
		if got, err := ParseOnOff(in); err != nil || got != want {
			t.Errorf("ParseOnOff(%q) = %v %v", in, got, err)
		}
	}
	if _, err := ParseOnOff("maybe"); err == nil {
		t.Error("maybe accepted")
	}
}

func TestPostAndEdit(t *testing.T) {
	rc := newReceiver(t)
	h, err := New(rc.url(), Options{AllowLocal: true})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(time.Second)
	ref := h.Post(Message{Content: "first"})
	id, err := wait(t, ref)
	if err != nil || id != "1001" {
		t.Fatalf("post: %q %v", id, err)
	}
	h.Edit(ref, Message{Content: "second"})
	h.Close(5 * time.Second)
	rs := rc.requests()
	if len(rs) != 2 {
		t.Fatalf("%d requests", len(rs))
	}
	if rs[0].Method != "POST" || rs[0].Query != "wait=true" || rs[0].Path != "/api/webhooks/123456789012345678/"+token {
		t.Errorf("post: %+v", rs[0])
	}
	if rs[1].Method != "PATCH" || rs[1].Path != "/api/webhooks/123456789012345678/"+token+"/messages/1001" || rs[1].Body["content"] != "second" {
		t.Errorf("edit: %+v", rs[1])
	}
	if am, _ := rs[1].Body["allowed_mentions"].(map[string]any); am == nil {
		t.Error("an edit without allowed_mentions")
	}
}

func TestEditsCoalesce(t *testing.T) {
	rc := newReceiver(t)
	release := make(chan struct{})
	rc.answer = func(n int, r request, w http.ResponseWriter) {
		if n == 1 {
			<-release // the post is in flight while the edits queue up
		}
		fmt.Fprintf(w, `{"id":"%d"}`, 1000+n)
	}
	h, _ := New(rc.url(), Options{AllowLocal: true})
	ref := h.Post(Message{Content: "post"})
	for i := 1; i <= 5; i++ {
		h.Edit(ref, Message{Content: fmt.Sprintf("edit %d", i)})
	}
	close(release)
	h.Close(5 * time.Second)
	rs := rc.requests()
	if len(rs) != 2 || rs[1].Method != "PATCH" || rs[1].Body["content"] != "edit 5" || !strings.HasSuffix(rs[1].Path, "/messages/1001") {
		t.Errorf("requests: %+v", rs)
	}
}

func TestEditRepostsWhenGone(t *testing.T) {
	rc := newReceiver(t)
	rc.answer = func(n int, r request, w http.ResponseWriter) {
		if r.Method == "PATCH" {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Unknown Message","code":10008}`)
			return
		}
		fmt.Fprintf(w, `{"id":"%d"}`, 1000+n)
	}
	var l logs
	h, _ := New(rc.url(), Options{AllowLocal: true, Logf: l.logf, sleep: (&sleeps{}).sleep})
	ref := h.Post(Message{Content: "post"})
	wait(t, ref)
	h.Edit(ref, Message{Content: "again"})
	h.Close(5 * time.Second)
	rs := rc.requests()
	if len(rs) != 3 || rs[2].Method != "POST" || rs[2].Body["content"] != "again" || ref.ID() != "1003" {
		t.Errorf("requests %+v, id %s", rs, ref.ID())
	}
	if !strings.Contains(l.all(), "is gone") {
		t.Errorf("log: %s", l.all())
	}
}

func TestRateLimit(t *testing.T) {
	rc := newReceiver(t)
	rc.answer = func(n int, r request, w http.ResponseWriter) {
		switch n {
		case 1:
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"message":"You are being rate limited.","retry_after":1.5,"global":false}`)
		case 2:
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset-After", "2.25")
			fmt.Fprintf(w, `{"id":"%d"}`, 1000+n)
		default:
			fmt.Fprintf(w, `{"id":"%d"}`, 1000+n)
		}
	}
	sl := &sleeps{}
	h, _ := New(rc.url(), Options{AllowLocal: true, sleep: sl.sleep})
	id, err := wait(t, h.Post(Message{Content: "a"}))
	if err != nil || id != "1002" {
		t.Fatalf("post after a 429: %q %v", id, err)
	}
	if id, err := wait(t, h.Post(Message{Content: "b"})); err != nil || id != "1003" {
		t.Fatalf("second post: %q %v", id, err)
	}
	h.Close(time.Second)
	ds := sl.list()
	// the body's retry_after (1.5 s) wins over the header; then the empty bucket's reset (2.25 s)
	if len(ds) != 2 || ds[0] < 1400*time.Millisecond || ds[0] > 1500*time.Millisecond || ds[1] < 2100*time.Millisecond || ds[1] > 2250*time.Millisecond {
		t.Errorf("waits %v", ds)
	}
}

func TestBackoff(t *testing.T) {
	rc := newReceiver(t)
	rc.answer = func(n int, r request, w http.ResponseWriter) {
		if n <= 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, `{"id":"%d"}`, 1000+n)
	}
	sl := &sleeps{}
	var l logs
	h, _ := New(rc.url(), Options{AllowLocal: true, sleep: sl.sleep, Logf: l.logf})
	if _, err := wait(t, h.Post(Message{Content: "a"})); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("three 502s: %v", err)
	}
	if id, err := wait(t, h.Post(Message{Content: "b"})); err != nil || id != "1004" {
		t.Fatalf("after the failures: %q %v", id, err)
	}
	h.Close(time.Second)
	ds := sl.list()
	if len(ds) != 3 || ds[0] > time.Second || ds[0] < 900*time.Millisecond || ds[1] < 1900*time.Millisecond || ds[2] < 3900*time.Millisecond {
		t.Errorf("back-off waits %v", ds)
	}
	if len(rc.requests()) != 4 || !strings.Contains(l.all(), "works again after 3 failed") {
		t.Errorf("%d requests; log %s", len(rc.requests()), l.all())
	}

	// a 4xx is final: no retry
	rc2 := newReceiver(t)
	rc2.answer = func(n int, r request, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"message":"Cannot send an empty message","code":50006}`)
	}
	h2, _ := New(rc2.url(), Options{AllowLocal: true, sleep: (&sleeps{}).sleep})
	if _, err := wait(t, h2.Post(Message{})); err == nil || !strings.Contains(err.Error(), "400: Cannot send an empty message") {
		t.Errorf("400: %v", err)
	}
	h2.Close(time.Second)
	if len(rc2.requests()) != 1 {
		t.Errorf("400 retried: %d requests", len(rc2.requests()))
	}
}

func TestQueueFullDrops(t *testing.T) {
	rc := newReceiver(t)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	rc.answer = func(n int, r request, w http.ResponseWriter) {
		if n == 1 {
			started <- struct{}{}
			<-release
		}
		fmt.Fprintf(w, `{"id":"%d"}`, 1000+n)
	}
	var l logs
	h, _ := New(rc.url(), Options{AllowLocal: true, QueueSize: 2, Logf: l.logf})
	refs := []*Ref{h.Post(Message{Content: "0"})}
	<-started // the first is in flight; two fit the queue, the rest drop
	for i := 1; i <= 5; i++ {
		refs = append(refs, h.Post(Message{Content: fmt.Sprint(i)}))
	}
	for i := 3; i <= 5; i++ {
		if _, err := wait(t, refs[i]); !errors.Is(err, ErrDropped) {
			t.Errorf("post %d: %v", i, err)
		}
	}
	close(release)
	for i := 0; i <= 2; i++ {
		if _, err := wait(t, refs[i]); err != nil {
			t.Errorf("post %d: %v", i, err)
		}
	}
	h.Close(time.Second)
	if n := len(rc.requests()); n != 3 {
		t.Errorf("%d requests", n)
	}
	log := l.all()
	if strings.Count(log, "queue is full") != 1 || !strings.Contains(log, "3 post(s) or edit(s) dropped") {
		t.Errorf("log:\n%s", log)
	}
}

func TestCloseFlushLimit(t *testing.T) {
	rc := newReceiver(t)
	block := make(chan struct{})
	defer close(block)
	rc.answer = func(n int, r request, w http.ResponseWriter) {
		<-block
	}
	h, _ := New(rc.url(), Options{AllowLocal: true})
	a, b := h.Post(Message{Content: "a"}), h.Post(Message{Content: "b"})
	t0 := time.Now()
	h.Close(200 * time.Millisecond)
	if d := time.Since(t0); d > 3*time.Second {
		t.Errorf("Close took %s", d)
	}
	for _, r := range []*Ref{a, b} {
		if _, err := wait(t, r); !errors.Is(err, ErrClosed) {
			t.Errorf("after the flush time: %v", err)
		}
	}
	if _, err := wait(t, h.Post(Message{Content: "late"})); !errors.Is(err, ErrClosed) {
		t.Errorf("post after Close: %v", err)
	}
	h.Close(time.Second) // twice is fine
}

func TestErrorsHideToken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens: connection refused
	var l logs
	h, err := New("http://"+addr+"/api/webhooks/42/"+token, Options{AllowLocal: true, Logf: l.logf, sleep: (&sleeps{}).sleep})
	if err != nil {
		t.Fatal(err)
	}
	_, perr := wait(t, h.Post(Message{Content: "x"}))
	h.Close(time.Second)
	if perr == nil || strings.Contains(perr.Error(), token) || strings.Contains(l.all(), token) {
		t.Errorf("error %v; log %s", perr, l.all())
	}
	if !strings.Contains(l.all(), "a post failed") {
		t.Errorf("log: %s", l.all())
	}
}

func TestNilHook(t *testing.T) {
	var h *Hook
	ref := h.Post(Message{Content: "x"})
	h.Edit(ref, Message{Content: "y"})
	h.Close(time.Second)
	if _, err := ref.Wait(context.Background()); !errors.Is(err, ErrOff) || ref.ID() != "" || h.Label() != "off" {
		t.Errorf("nil hook: %v", err)
	}
}
