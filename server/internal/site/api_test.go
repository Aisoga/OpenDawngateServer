package site

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/issues"
	"dawngate/server/internal/store"
)

// trackerFixture is the admin fixture with the issue tracker on: the fixture list synced, two players with e-mail
// login names and display names of their own (alice@example.com "Alice", bob@example.com "Bob"), and an API token
// "reviewer" made in the store.
type trackerFixture struct {
	*adminFixture
	alice, bob store.Account
	token      string
	tokenID    int64
}

func newTrackerFixture(t *testing.T, mut func(*Config)) *trackerFixture {
	t.Helper()
	tf := &trackerFixture{}
	tf.adminFixture = newAdminFixture(t, func(c *Config) {
		c.Issues = true
		if mut != nil {
			mut(c)
		}
	})
	if _, err := issues.Sync(tf.st, testEntries, t0); err != nil {
		t.Fatal(err)
	}
	var err error
	if tf.alice, err = tf.st.Create(store.NewAccount{Name: "alice@example.com", Persona: "Alice"}, t0); err != nil {
		t.Fatal(err)
	}
	if tf.bob, err = tf.st.Create(store.NewAccount{Name: "bob@example.com", Persona: "Bob"}, t0); err != nil {
		t.Fatal(err)
	}
	tok, hash, err := issues.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	at, err := tf.st.CreateAPIToken("reviewer", hash, "admin boss (account 1)", t0)
	if err != nil {
		t.Fatal(err)
	}
	tf.token, tf.tokenID = tok, at.ID
	return tf
}

// api sends an API request with the token ("" = no Authorization header; a value with a space is sent as it is).
func (tf *trackerFixture) api(method, path, token, contentType, body string) (int, string, http.Header) {
	tf.t.Helper()
	req, _ := http.NewRequest(method, tf.srv.URL+path, strings.NewReader(body))
	switch {
	case token == "":
	case strings.Contains(token, " "):
		req.Header.Set("Authorization", token)
	default:
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := tf.srv.Client().Do(req)
	if err != nil {
		tf.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

func (tf *trackerFixture) claim(issue string, user int64, stance store.Stance, note string, videos ...store.ClaimVideo) store.Claim {
	tf.t.Helper()
	c, err := tf.st.PutClaim(issue, user, stance, note, videos, tf.clock())
	if err != nil {
		tf.t.Fatal(err)
	}
	return c
}

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, body)
	}
	return v
}

// The token: none, malformed, unknown and revoked get the same 401; a valid one works and is touched; the admin's
// session cookie counts for nothing; failed tokens lock the address out (also for a valid token) until the lockout
// ends; another address isn't affected (the limiter's own test); the tracker off: no API.
func TestAPIAuth(t *testing.T) {
	tf := newTrackerFixture(t, nil)
	revokedTok, revokedHash, _ := issues.NewToken()
	rt, _ := tf.st.CreateAPIToken("old", revokedHash, "admin boss (account 1)", t0)
	if err := tf.st.RevokeAPIToken(rt.ID); err != nil {
		t.Fatal(err)
	}
	unknown, _, _ := issues.NewToken()
	want := `{"error":"` + apiUnauthorized + `"}` + "\n"
	for _, tc := range []struct{ name, token string }{
		{"no header", ""},
		{"basic", "Basic Ym9zczpwdw=="},
		{"short", "Bearer abc"},
		{"unknown", unknown},
		{"revoked", revokedTok},
	} {
		code, body, h := tf.api("GET", "/api/issues", tc.token, "", "")
		if code != 401 || body != want || h.Get("WWW-Authenticate") != `Bearer realm="issues"` ||
			h.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Errorf("%s: %d %q %q", tc.name, code, body, h)
		}
	}
	for _, s := range []string{"refused: api: no token", "refused: api: unknown token", "refused: api: revoked token"} {
		if !strings.Contains(tf.logText(), s) {
			t.Errorf("the log has no %q:\n%s", s, tf.logText())
		}
	}
	// the admin's cookie isn't a token
	c := tf.newClient()
	tf.login(c)
	req, _ := http.NewRequest("GET", tf.srv.URL+"/api/issues", nil)
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Errorf("with the admin's session: %d", res.StatusCode)
	}
	// valid (it clears the 4 failures so far: basic, short, unknown, revoked; a missing header is none)
	code, body, _ := tf.api("GET", "/api/issues", tf.token, "", "")
	if code != 200 || !strings.Contains(body, `"count":5`) {
		t.Fatalf("valid token: %d\n%s", code, body)
	}
	if at, _, _ := tf.st.APITokenByHash(issues.TokenHash(tf.token)); !at.LastUsed.Equal(t0) {
		t.Errorf("last used %v", at.LastUsed)
	}
	for i := range 6 {
		if code, _, _ := tf.api("GET", "/api/issues", unknown, "", ""); code != 401 {
			t.Fatalf("bad token %d: %d", i+1, code)
		}
	}
	code, body, h := tf.api("GET", "/api/issues", tf.token, "", "")
	if code != 429 || h.Get("Retry-After") == "" || !strings.Contains(body, "isn't valid") {
		t.Errorf("locked address with a valid token: %d %q %q", code, h.Get("Retry-After"), body)
	}
	tf.advance(6 * time.Minute)
	if code, _, _ := tf.api("GET", "/api/issues", tf.token, "", ""); code != 200 {
		t.Errorf("after the lockout: %d", code)
	}
	// unknown API paths answer JSON (after the token)
	if code, body, _ := tf.api("GET", "/api/nothing", tf.token, "", ""); code != 404 || !strings.Contains(body, `"error"`) {
		t.Errorf("/api/nothing: %d %s", code, body)
	}
	if code, body, _ := tf.api("DELETE", "/api/issues/MN1", tf.token, "", ""); code != 404 || !strings.Contains(body, `"error"`) {
		t.Errorf("DELETE: %d %s", code, body)
	}

	// the tracker off: no API, no admin pages for it
	off := newAdminFixture(t, nil)
	if code, body, _ := off.get(off.c, "/api/issues"); code != 404 || strings.Contains(body, `"error"`) {
		t.Errorf("tracker off: %d %s", code, body)
	}
	oc := off.newClient()
	off.login(oc)
	if code, _, _ := off.get(oc, "/admin/issues"); code != 404 {
		t.Errorf("tracker off: /admin/issues %d", code)
	}
	if _, body, _ := off.get(oc, "/admin"); strings.Contains(body, "/admin/tokens") {
		t.Error("tracker off: the admin page links the tokens")
	}
}

// Each token's rate: the burst, then 429 with Retry-After; refilled with time.
func TestAPIRate(t *testing.T) {
	tf := newTrackerFixture(t, func(c *Config) { c.APIRate = Rate{PerMinute: 60, Burst: 3} })
	for i := range 3 {
		if code, _, _ := tf.api("GET", "/api/issues/MN1", tf.token, "", ""); code != 200 {
			t.Fatalf("request %d: %d", i+1, code)
		}
	}
	code, body, h := tf.api("GET", "/api/issues/MN1", tf.token, "", "")
	if code != 429 || h.Get("Retry-After") != "1" || !strings.Contains(body, "this token") {
		t.Errorf("over the rate: %d %q %s", code, h.Get("Retry-After"), body)
	}
	tf.advance(2 * time.Second)
	if code, _, _ := tf.api("GET", "/api/issues/MN1", tf.token, "", ""); code != 200 {
		t.Errorf("refilled: %d", code)
	}
}

// The list and one issue: tallies, claims best first with canonical links and seconds, the hidden flag, display
// names only (no login name, e-mail or account id); the filters state, since and withdrawn; bad parameters.
func TestAPIList(t *testing.T) {
	tf := newTrackerFixture(t, nil)
	tf.claim("MN1", tf.bob.UserID, store.StanceWrong, "Bob's note <b>")
	tf.advance(time.Minute)
	ca := tf.claim("MN1", tf.alice.UserID, store.StanceCorrect, "At 1:23 they hit minions.",
		store.ClaimVideo{VideoID: "dQw4w9WgXcQ", Seconds: 83}, store.ClaimVideo{VideoID: "abcdefghijk", Seconds: -1})
	tf.claim("TW1", tf.alice.UserID, store.StanceCorrect, "", store.ClaimVideo{VideoID: "abcdefghijk", Seconds: -1})
	hidden := tf.claim("TW1", tf.bob.UserID, store.StanceWrong, "spam")
	if _, err := tf.st.SetClaimHidden(hidden.ID, true, tf.clock()); err != nil {
		t.Fatal(err)
	}
	code, body, _ := tf.api("GET", "/api/issues", tf.token, "", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, bad := range []string{"alice@example.com", "bob@example.com", "@", "1000000", "userId", "email"} {
		if strings.Contains(body, bad) {
			t.Errorf("the list holds %q:\n%s", bad, body)
		}
	}
	list := decode[APIList](t, body)
	if list.Count != 5 || len(list.Issues) != 5 || list.Now != apiTime(tf.clock()) {
		t.Fatalf("count %d, now %s", list.Count, list.Now)
	}
	var mn1, tw1 APIIssue
	for _, is := range list.Issues {
		switch is.ID {
		case "MN1":
			mn1 = is
		case "TW1":
			tw1 = is
		}
	}
	if mn1.Title != "Who a minion attacks" || mn1.LookFor == "" || !mn1.Top || !mn1.Listed || mn1.State != "open" ||
		mn1.Status != "leaning-correct" || mn1.StatusLabel != "Leaning correct" || mn1.Verdict != nil {
		t.Errorf("MN1: %+v", mn1)
	}
	if want := (APITally{CorrectWeight: 3, WrongWeight: 1, CorrectClaims: 1, WrongClaims: 1, CorrectTimed: 1, Videos: 2}); mn1.Tally != want {
		t.Errorf("MN1's tally %+v, want %+v", mn1.Tally, want)
	}
	if len(mn1.Claims) != 2 || mn1.Claims[0].ID != ca.ID || mn1.Claims[0].Persona != "Alice" || mn1.Claims[0].Stance != "correct" ||
		mn1.Claims[0].Weight != 3 || mn1.Claims[1].Persona != "Bob" || mn1.Claims[1].Note != "Bob's note <b>" ||
		mn1.Claims[1].Weight != 1 || len(mn1.Claims[1].Videos) != 0 || mn1.Claims[1].Videos == nil {
		t.Errorf("MN1's claims: %+v", mn1.Claims)
	}
	if v := mn1.Claims[0].Videos; len(v) != 2 || v[0] != (APIVideo{URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=83s",
		VideoID: "dQw4w9WgXcQ", Seconds: 83, Timestamp: "1:23"}) || v[1] != (APIVideo{URL: "https://www.youtube.com/watch?v=abcdefghijk",
		VideoID: "abcdefghijk", Seconds: -1}) {
		t.Errorf("videos: %+v", v)
	}
	if tw1.Tally.Hidden != 1 || tw1.Tally.WrongClaims != 0 || len(tw1.Claims) != 2 || !tw1.Claims[1].Hidden ||
		tw1.Claims[0].Hidden || tw1.Status != "leaning-correct" {
		t.Errorf("TW1: %+v", tw1)
	}
	// one issue
	code, body, _ = tf.api("GET", "/api/issues/MN1", tf.token, "", "")
	if one := decode[APIIssue](t, body); code != 200 || one.ID != "MN1" || len(one.Claims) != 2 {
		t.Errorf("one: %d %s", code, body)
	}
	if code, body, _ := tf.api("GET", "/api/issues/XX9", tf.token, "", ""); code != 404 || body != `{"error":"no such issue"}`+"\n" {
		t.Errorf("unknown: %d %s", code, body)
	}
	// since: only what changed at or after it
	tf.advance(time.Hour)
	mark := tf.clock()
	tf.claim("MN3", tf.bob.UserID, store.StanceCorrect, "later")
	_, body, _ = tf.api("GET", "/api/issues?since="+apiTime(mark), tf.token, "", "")
	if l := decode[APIList](t, body); l.Count != 1 || l.Issues[0].ID != "MN3" {
		t.Errorf("since: %s", body)
	}
	// state
	if err := tf.st.SetVerdict("MN2", store.IssueConfirmed, "seen", "admin boss (account 1)", tf.clock()); err != nil {
		t.Fatal(err)
	}
	_, body, _ = tf.api("GET", "/api/issues?state=confirmed", tf.token, "", "")
	if l := decode[APIList](t, body); l.Count != 1 || l.Issues[0].ID != "MN2" || l.Issues[0].Verdict == nil ||
		l.Issues[0].Verdict.By != "admin" || l.Issues[0].Verdict.Note != "seen" || strings.Contains(body, "boss") {
		t.Errorf("state=confirmed: %s", body)
	}
	_, body, _ = tf.api("GET", "/api/issues?state=open", tf.token, "", "")
	if l := decode[APIList](t, body); l.Count != 4 {
		t.Errorf("state=open: %d", l.Count)
	}
	// withdrawn
	if _, err := issues.Sync(tf.st, testEntries[:4], tf.clock()); err != nil {
		t.Fatal(err)
	}
	_, body, _ = tf.api("GET", "/api/issues", tf.token, "", "")
	if l := decode[APIList](t, body); l.Count != 4 {
		t.Errorf("without the withdrawn: %d", l.Count)
	}
	_, body, _ = tf.api("GET", "/api/issues?withdrawn=1", tf.token, "", "")
	if l := decode[APIList](t, body); l.Count != 5 || l.Issues[4].ID != "TW2" || l.Issues[4].Listed {
		t.Errorf("withdrawn=1: %s", body)
	}
	for _, q := range []string{"state=maybe", "since=yesterday", "withdrawn=yes"} {
		if code, body, _ := tf.api("GET", "/api/issues?"+q, tf.token, "", ""); code != 400 || !strings.Contains(body, `"error"`) {
			t.Errorf("%s: %d %s", q, code, body)
		}
	}
}

// A verdict through the API: the refusals, a verdict closing the issue (audited as api:reviewer with its claims), the
// players' page read-only with the note, a second verdict refused, reopening (by the API and on the admin page).
func TestAPIVerdict(t *testing.T) {
	tf := newTrackerFixture(t, nil)
	ca := tf.claim("MN1", tf.alice.UserID, store.StanceCorrect, "seen", store.ClaimVideo{VideoID: "dQw4w9WgXcQ", Seconds: 83})
	cb := tf.claim("MN1", tf.bob.UserID, store.StanceCorrect, "", store.ClaimVideo{VideoID: "abcdefghijk", Seconds: 20})
	other := tf.claim("TW1", tf.bob.UserID, store.StanceWrong, "no")
	const js = "application/json"
	post := func(id, ct, body string) (int, string) {
		code, b, _ := tf.api("POST", "/api/issues/"+id+"/verdict", tf.token, ct, body)
		return code, b
	}
	for _, tc := range []struct {
		name, id, ct, body string
		code               int
		text               string
	}{
		{"form", "MN1", "application/x-www-form-urlencoded", "verdict=confirmed", 415, "must be JSON"},
		{"no type", "MN1", "", `{"verdict":"confirmed","note":"x"}`, 415, "must be JSON"},
		{"broken", "MN1", js, `{"verdict":`, 400, "the body"},
		{"unknown field", "MN1", js, `{"verdict":"confirmed","note":"x","by":"me"}`, 400, "unknown field"},
		{"two values", "MN1", js, `{"verdict":"confirmed","note":"x"}{}`, 400, "more than one"},
		{"verdict", "MN1", js, `{"verdict":"fixed","note":"x"}`, 400, "confirmed, disproven, closed or reopen"},
		{"no note", "MN1", js, `{"verdict":"confirmed","note":"  "}`, 400, "needs a note"},
		{"control", "MN1", js, `{"verdict":"confirmed","note":"a\u202eb"}`, 400, "aren't text"},
		{"long note", "MN1", js, `{"verdict":"confirmed","note":"` + strings.Repeat("x", 2001) + `"}`, 400, "longer than 2000"},
		{"other's claim", "MN1", js, `{"verdict":"confirmed","note":"x","claims":[` + itoa(other.ID) + `]}`, 400, "isn't one of"},
		{"unknown issue", "XX9", js, `{"verdict":"confirmed","note":"x"}`, 404, "no such issue"},
		{"reopen open", "MN1", js, `{"verdict":"reopen"}`, 409, "open already"},
		{"too big", "MN1", js, `{"verdict":"confirmed","note":"` + strings.Repeat("x", 9000) + `"}`, 413, "larger than 8192"},
	} {
		if code, body := post(tc.id, tc.ct, tc.body); code != tc.code || !strings.Contains(body, tc.text) {
			t.Errorf("%s: %d %s", tc.name, code, body)
		}
	}
	if d, _, _ := tf.st.IssueDetail("MN1", 0); d.State != store.IssueOpen {
		t.Fatalf("refusals changed MN1: %s", d.State)
	}
	tf.advance(time.Minute)
	code, body := post("MN1", "application/json; charset=utf-8", `{"verdict":"confirmed","note":"Two timed videos agree.","claims":[`+
		itoa(ca.ID)+`,`+itoa(cb.ID)+`,`+itoa(ca.ID)+`]}`)
	ans := decode[APIVerdictAnswer](t, body)
	if code != 200 || ans.Issue.State != "confirmed" || ans.Issue.Status != "confirmed" || ans.Issue.Verdict == nil ||
		ans.Issue.Verdict.By != "api:reviewer" || ans.Issue.Verdict.Note != "Two timed videos agree." ||
		ans.Issue.Verdict.At != apiTime(tf.clock()) || !strings.Contains(ans.Done, "MN1 is confirmed") {
		t.Fatalf("verdict: %d %s", code, body)
	}
	e := tf.lastAudit()
	if e.Actor != "api:reviewer" || !strings.HasPrefix(e.Source, "api 127.0.0.1") || e.Action != "issue verdict" ||
		e.Target != "issue MN1" || e.Detail != "confirmed: Two timed videos agree.; claims "+itoa(ca.ID)+", "+itoa(cb.ID) {
		t.Errorf("audit: %+v", e)
	}
	if !strings.Contains(tf.logText(), "api:reviewer: issue verdict issue MN1 confirmed") {
		t.Errorf("log:\n%s", tf.logText())
	}
	if d, _, _ := tf.st.IssueDetail("MN1", 0); d.ClosedBy != "api:reviewer" {
		t.Errorf("closed by %q", d.ClosedBy)
	}
	// the players' page: read-only with the note
	pc := tf.newClient()
	if code, body, h := tf.submit(pc, "/issues/login", map[string]string{"name": "player1", "password": playerPassword(t, tf),
		"next": "/issues/MN1"}); code != 303 {
		t.Fatalf("player login: %d %v\n%s", code, h, body)
	}
	_, page, _ := tf.get(pc, "/issues/MN1")
	if !strings.Contains(page, "Two timed videos agree.") || !strings.Contains(page, "read-only") ||
		strings.Contains(page, `action="/issues/MN1/claim"`) || strings.Contains(page, "reviewer") {
		t.Errorf("the closed page:\n%s", page)
	}
	m := tokenRE.FindStringSubmatch(page)
	if code, _, _ := tf.post(pc, "/issues/MN1/claim", map[string][]string{CSRFField: {m[1]}, "stance": {"wrong"}}, nil); code != 409 {
		t.Errorf("a claim on the closed issue: %d", code)
	}
	// a second verdict: refused; reopen through the API, then a verdict again, reopened on the admin page
	if code, body := post("MN1", js, `{"verdict":"disproven","note":"no"}`); code != 409 || !strings.Contains(body, "reopen it first") {
		t.Errorf("second verdict: %d %s", code, body)
	}
	code, body = post("MN1", js, `{"verdict":"reopen","note":"A counter-video came in."}`)
	if ans := decode[APIVerdictAnswer](t, body); code != 200 || ans.Issue.State != "open" || ans.Issue.Verdict != nil {
		t.Errorf("reopen: %d %s", code, body)
	}
	if e := tf.lastAudit(); e.Action != "issue reopen" || e.Actor != "api:reviewer" ||
		e.Detail != "was confirmed by api:reviewer: Two timed videos agree.; why: A counter-video came in." {
		t.Errorf("reopen audit: %+v", e)
	}
	if code, _ := post("MN1", js, `{"verdict":"disproven","note":"The original differs."}`); code != 200 {
		t.Errorf("disproven: %d", code)
	}
	ac := tf.newClient()
	tf.login(ac)
	code, page, _ = tf.adminPost(ac, "/admin/issues/reopen", map[string]string{"issue": "MN1", "note": "the AI was wrong"})
	if code != 200 || !strings.Contains(page, "Issue MN1 is open again (it was disproven)") {
		t.Errorf("admin reopen: %d\n%s", code, page)
	}
	if e := tf.lastAudit(); e.Action != "issue reopen" || !strings.HasPrefix(e.Actor, "admin boss") ||
		!strings.Contains(e.Detail, "was disproven by api:reviewer: The original differs.; why: the AI was wrong") {
		t.Errorf("admin reopen audit: %+v", e)
	}
	_, body, _ = tf.api("GET", "/api/issues/MN1", tf.token, "", "")
	if is := decode[APIIssue](t, body); is.State != "open" || is.Status != "leaning-correct" {
		t.Errorf("after the admin's reopen: %s", body)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// playerPassword gives the fixture's player1 a password and returns it.
func playerPassword(t *testing.T, tf *trackerFixture) string {
	t.Helper()
	_, pw, err := accounts.ResetPasswordByID(tf.st, tf.player.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return pw
}
