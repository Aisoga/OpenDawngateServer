package site

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"dawngate/server/internal/issues"
	"dawngate/server/internal/store"
)

// /admin/issues: the filters with their counts, the rows; one issue with every claim and its account; hide and show
// a claim (audited; the players' tally and the API follow); a verdict on the page (refusals keep the form; the
// players' page read-only; the API shows "admin"), reopen; without a session: the login.
func TestAdminIssues(t *testing.T) {
	tf := newTrackerFixture(t, nil)
	ca := tf.claim("MN1", tf.alice.UserID, store.StanceCorrect, "timed", store.ClaimVideo{VideoID: "dQw4w9WgXcQ", Seconds: 83})
	cb := tf.claim("MN1", tf.bob.UserID, store.StanceWrong, "Bob <script>")
	tf.claim("TW1", tf.bob.UserID, store.StanceCorrect, "note only")

	anon := tf.newClient()
	if code, _, h := tf.get(anon, "/admin/issues"); code != http.StatusSeeOther || h.Get("Location") != "/admin" {
		t.Errorf("without a session: %d %q", code, h.Get("Location"))
	}
	if code, body, _ := tf.adminPost(anon, "/admin/issues/verdict", map[string]string{"issue": "MN1", "verdict": "closed",
		"note": "x"}); code != http.StatusForbidden || !strings.Contains(body, "session has ended") {
		t.Errorf("a verdict without a session: %d", code)
	}
	c := tf.newClient()
	tf.login(c)
	_, home, _ := tf.get(c, "/admin")
	for _, s := range []string{`href="/admin/issues"`, `href="/admin/tokens"`} {
		if !strings.Contains(home, s) {
			t.Errorf("the admin page has no %s", s)
		}
	}
	code, body, _ := tf.get(c, "/admin/issues")
	for _, s := range []string{"<strong>Open (5)</strong>", ">Leaning (1)<", ">Needs video proof (1)<", ">Needs verification (3)<",
		">With a verdict (0)<", `href="/admin/issues/MN1">MN1</a> Who a minion attacks`, "1 (weight 3)", "1 (weight 1)",
		`href="/admin/issues/TW2"`} {
		if !strings.Contains(body, s) {
			t.Errorf("/admin/issues has no %q", s)
		}
	}
	if code != 200 || strings.Count(body, `<tr><td><a href="/admin/issues/`) != 5 {
		t.Errorf("/admin/issues: %d\n%s", code, body)
	}
	_, body, _ = tf.get(c, "/admin/issues?filter=leaning")
	if strings.Count(body, `<tr><td><a href="/admin/issues/`) != 1 || !strings.Contains(body, "/admin/issues/MN1") {
		t.Errorf("leaning:\n%s", body)
	}
	code, body, _ = tf.get(c, "/admin/issues/MN1")
	for _, s := range []string{"Who a minion attacks", "Minions attack minions first.", "Alice", "Bob &lt;script&gt;",
		"account " + itoa(tf.alice.UserID), `href="https://www.youtube.com/watch?v=dQw4w9WgXcQ&amp;t=83s"`, "Watch at 1:23",
		`action="/admin/issues/verdict"`, `name="claim" value="` + itoa(ca.ID) + `"`} {
		if !strings.Contains(body, s) {
			t.Errorf("/admin/issues/MN1 has no %q", s)
		}
	}
	if code != 200 || strings.Contains(body, "<script>") || strings.Index(body, "Alice") > strings.Index(body, "Bob &lt;") {
		t.Errorf("/admin/issues/MN1: %d", code)
	}
	if code, body, _ := tf.get(c, "/admin/issues/XX9"); code != 404 || !strings.Contains(body, "no such issue") {
		t.Errorf("unknown issue: %d", code)
	}

	// hide Bob's claim: audited, no longer counted, the API marks it
	code, body, _ = tf.adminPost(c, "/admin/issues/claim", map[string]string{"issue": "MN1", "claim": itoa(cb.ID), "hidden": "1"})
	if code != 200 || !strings.Contains(body, "Claim "+itoa(cb.ID)+" is hidden") || !strings.Contains(body, "<b>hidden</b>") {
		t.Errorf("hide: %d\n%s", code, body)
	}
	if e := tf.lastAudit(); e.Action != "claim hide" || e.Target != "claim "+itoa(cb.ID)+" on MN1" ||
		e.Detail != "account "+itoa(tf.bob.UserID)+" (wrong), 0 videos" || !strings.HasPrefix(e.Actor, "admin boss") {
		t.Errorf("hide audit: %+v", e)
	}
	_, js, _ := tf.api("GET", "/api/issues/MN1", tf.token, "", "")
	if is := decode[APIIssue](t, js); is.Tally.WrongClaims != 0 || is.Tally.Hidden != 1 || !is.Claims[1].Hidden {
		t.Errorf("the API after the hide: %s", js)
	}
	code, body, _ = tf.adminPost(c, "/admin/issues/claim", map[string]string{"issue": "MN1", "claim": itoa(cb.ID), "hidden": "0"})
	if code != 200 || !strings.Contains(body, "shown again") || tf.lastAudit().Action != "claim show" {
		t.Errorf("show: %d", code)
	}
	for _, f := range []map[string]string{{"issue": "MN1", "claim": "x", "hidden": "1"}, {"issue": "MN1", "claim": itoa(cb.ID), "hidden": "2"},
		{"issue": "MN1", "claim": "999999", "hidden": "1"}} {
		if code, _, _ := tf.adminPost(c, "/admin/issues/claim", f); code != 400 {
			t.Errorf("hide %v: %d", f, code)
		}
	}

	// a verdict: refused without a note (the form keeps the choice), then given
	code, body, _ = tf.adminPost(c, "/admin/issues/verdict", map[string]string{"issue": "MN1", "verdict": "disproven", "note": " "})
	if code != 400 || !strings.Contains(body, "needs a note") || !strings.Contains(body, `value="disproven" checked`) {
		t.Errorf("no note: %d\n%s", code, body)
	}
	if code, _, _ := tf.adminPost(c, "/admin/issues/verdict", map[string]string{"issue": "MN1", "verdict": "reopen", "note": "x"}); code != 400 {
		t.Errorf("reopen through the verdict form: %d", code)
	}
	code, body, _ = tf.adminPost(c, "/admin/issues/verdict", map[string]string{"issue": "MN1", "verdict": "disproven",
		"note": "The original's minions hit shapers first."})
	if code != 200 || !strings.Contains(body, "Issue MN1 is disproven now") || !strings.Contains(body, "By admin boss") ||
		!strings.Contains(body, `action="/admin/issues/reopen"`) {
		t.Errorf("verdict: %d\n%s", code, body)
	}
	if e := tf.lastAudit(); e.Action != "issue verdict" || e.Detail != "disproven: The original's minions hit shapers first." {
		t.Errorf("verdict audit: %+v", e)
	}
	_, js, _ = tf.api("GET", "/api/issues/MN1", tf.token, "", "")
	if is := decode[APIIssue](t, js); is.State != "disproven" || is.Verdict == nil || is.Verdict.By != "admin" || strings.Contains(js, "boss") {
		t.Errorf("the API after the admin's verdict: %s", js)
	}
	if code, body, _ := tf.adminPost(c, "/admin/issues/verdict", map[string]string{"issue": "MN1", "verdict": "closed",
		"note": "x"}); code != 409 || !strings.Contains(body, "reopen it first") {
		t.Errorf("second verdict: %d", code)
	}
	_, body, _ = tf.get(c, "/admin/issues?filter=closed")
	if !strings.Contains(body, "<strong>With a verdict (1)</strong>") || !strings.Contains(body, ">Open (4)<") {
		t.Errorf("closed filter:\n%s", body)
	}
	// hiding works on a closed issue too
	if code, _, _ := tf.adminPost(c, "/admin/issues/claim", map[string]string{"issue": "MN1", "claim": itoa(ca.ID), "hidden": "1"}); code != 200 {
		t.Errorf("hide on a closed issue: %d", code)
	}
	code, body, _ = tf.adminPost(c, "/admin/issues/reopen", map[string]string{"issue": "MN1"})
	if code != 200 || !strings.Contains(body, "open again") || !strings.Contains(body, `action="/admin/issues/verdict"`) {
		t.Errorf("reopen: %d", code)
	}
	if e := tf.lastAudit(); e.Action != "issue reopen" || e.Detail != "was disproven by admin boss (account "+itoa(tf.bossID)+
		"): The original's minions hit shapers first." {
		t.Errorf("reopen audit: %+v", e)
	}
	if code, body, _ := tf.adminPost(c, "/admin/issues/reopen", map[string]string{"issue": "MN1"}); code != 409 || !strings.Contains(body, "open already") {
		t.Errorf("reopen an open issue: %d", code)
	}
	// withdrawn issues have their own filter
	if _, err := issues.Sync(tf.st, testEntries[:4], tf.clock()); err != nil {
		t.Fatal(err)
	}
	_, body, _ = tf.get(c, "/admin/issues?filter=withdrawn")
	if !strings.Contains(body, "<strong>No longer listed (1)</strong>") || !strings.Contains(body, "no longer listed</span>") {
		t.Errorf("withdrawn:\n%s", body)
	}
}

var secretRE = regexp.MustCompile(`<p class="secret">([^<]+)</p>`)

// /admin/tokens: a token made (shown once, only its hash stored, never in the log or the audit log), a name taken
// refused, the list, revoking (the API refuses it at once).
func TestAdminTokens(t *testing.T) {
	tf := newTrackerFixture(t, nil)
	c := tf.newClient()
	tf.login(c)
	code, body, _ := tf.get(c, "/admin/tokens")
	if code != 200 || !strings.Contains(body, "<td>reviewer</td>") || !strings.Contains(body, "Make the token") {
		t.Fatalf("/admin/tokens: %d\n%s", code, body)
	}
	for _, name := range []string{"", "   ", strings.Repeat("n", 41), "Reviewer"} {
		if code, _, _ := tf.adminPost(c, "/admin/tokens", map[string]string{"name": name}); code != 400 {
			t.Errorf("name %q: %d", name, code)
		}
	}
	code, body, _ = tf.adminPost(c, "/admin/tokens", map[string]string{"name": "claude"})
	m := secretRE.FindStringSubmatch(body)
	if code != 200 || m == nil || len(m[1]) != 43 || !strings.Contains(body, "shown only now") {
		t.Fatalf("make: %d\n%s", code, body)
	}
	tok := m[1]
	at, found, _ := tf.st.APITokenByHash(issues.TokenHash(tok))
	if !found || at.Name != "claude" || !strings.HasPrefix(at.CreatedBy, "admin boss") {
		t.Fatalf("stored: %+v %v", at, found)
	}
	e := tf.lastAudit()
	if e.Action != "api token create" || e.Target != "token "+itoa(at.ID)+` "claude"` {
		t.Errorf("audit: %+v", e)
	}
	for _, x := range append(tf.audit(), store.AuditEntry{Detail: tf.logText()}) {
		if strings.Contains(x.Target+x.Detail+x.Actor, tok) {
			t.Fatalf("the token is in the log or the audit log: %+v", x)
		}
	}
	if _, body, _ := tf.get(c, "/admin/tokens"); strings.Contains(body, tok) || !strings.Contains(body, "<td>claude</td>") {
		t.Error("the list shows the token, or not its name")
	}
	if code, _, _ := tf.api("GET", "/api/issues", tok, "", ""); code != 200 {
		t.Errorf("the new token: %d", code)
	}
	code, body, _ = tf.adminPost(c, "/admin/tokens/revoke", map[string]string{"id": itoa(at.ID)})
	if code != 200 || !strings.Contains(body, "is revoked") || !strings.Contains(body, "<td>revoked</td>") {
		t.Errorf("revoke: %d\n%s", code, body)
	}
	if e := tf.lastAudit(); e.Action != "api token revoke" || e.Target != "token "+itoa(at.ID)+` "claude"` {
		t.Errorf("revoke audit: %+v", e)
	}
	if code, _, _ := tf.api("GET", "/api/issues", tok, "", ""); code != 401 {
		t.Errorf("the revoked token: %d", code)
	}
	if code, _, _ := tf.adminPost(c, "/admin/tokens/revoke", map[string]string{"id": "999"}); code != 400 {
		t.Errorf("revoke an unknown token: %d", code)
	}
	if code, _, _ := tf.adminPost(c, "/admin/tokens", map[string]string{"name": "claude"}); code != 400 {
		t.Errorf("a revoked token's name again: %d", code)
	}
}
