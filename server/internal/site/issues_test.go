package site

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/issues"
	"dawngate/server/internal/store"
)

// testEntries are the tracker's fixture list: two areas, two of the most important, in the list's order.
var testEntries = []issues.Entry{
	{ID: "MN1", Area: "Minions", Title: "Who a minion attacks", Assumed: "Minions attack minions first.",
		LookFor: "Whether minions hit you while you stand among them.", Top: true, Order: 1},
	{ID: "MN2", Area: "Minions", Title: "Minion speed", Assumed: "Minions walk at 325.", LookFor: "How fast they walk.", Order: 2},
	{ID: "MN3", Area: "Minions", Title: "Wave timer", Assumed: "A wave every 30 s.", LookFor: "The time between waves.", Order: 3},
	{ID: "TW1", Area: "Towers", Title: "Tower range", Assumed: "Towers reach 800.", LookFor: "Where a tower starts shooting.", Order: 4},
	{ID: "TW2", Area: "Towers", Title: "Tower aggro", Assumed: "A tower turns on a shaper who hits one.",
		LookFor: "Whether the tower switches to you.", Top: true, Order: 5},
}

// issuesFixture is a site with the tracker on, the fixture list synced, two players and a clock the test moves.
type issuesFixture struct {
	*fixture
	mu       sync.Mutex
	now      time.Time
	p1, p2   store.Account
	pw1, pw2 string
}

func newIssuesFixture(t *testing.T) *issuesFixture {
	t.Helper()
	xf := &issuesFixture{now: t0}
	xf.fixture = newFixture(t, accounts.Open, func(c *Config) {
		c.Issues = true
		c.Now = xf.clock
	})
	if _, err := issues.Sync(xf.st, testEntries, t0); err != nil {
		t.Fatal(err)
	}
	var err error
	if xf.p1, xf.pw1, err = accounts.CreateAccount(xf.st, "player1", t0); err != nil {
		t.Fatal(err)
	}
	if xf.p2, xf.pw2, err = accounts.CreateAccount(xf.st, "player2", t0); err != nil {
		t.Fatal(err)
	}
	return xf
}

func (xf *issuesFixture) clock() time.Time {
	xf.mu.Lock()
	defer xf.mu.Unlock()
	return xf.now
}

func (xf *issuesFixture) advance(d time.Duration) {
	xf.mu.Lock()
	xf.now = xf.now.Add(d)
	xf.mu.Unlock()
}

// login logs a new browser in as the player and returns it.
func (xf *issuesFixture) login(name, pw string) *http.Client {
	xf.t.Helper()
	c := xf.newClient()
	code, body, h := xf.submit(c, "/issues/login", map[string]string{"name": name, "password": pw, "next": "/issues"})
	if code != http.StatusSeeOther || h.Get("Location") != "/issues" {
		xf.t.Fatalf("login %s: %d %q\n%s", name, code, h.Get("Location"), body)
	}
	return c
}

// postFrom gets page (for its CSRF token) and posts the fields to path.
func (xf *issuesFixture) postFrom(c *http.Client, page, path string, fields map[string]string) (int, string, http.Header) {
	xf.t.Helper()
	_, body, _ := xf.get(c, page)
	m := tokenRE.FindStringSubmatch(body)
	if m == nil {
		xf.t.Fatalf("%s: no CSRF token:\n%s", page, body)
	}
	v := url.Values{CSRFField: {m[1]}}
	for k, x := range fields {
		v.Set(k, x)
	}
	return xf.post(c, path, v, nil)
}

func (xf *issuesFixture) claim(c *http.Client, id string, fields map[string]string) (int, string, http.Header) {
	xf.t.Helper()
	return xf.postFrom(c, "/issues/"+id, "/issues/"+id+"/claim", fields)
}

func TestIssuesOff(t *testing.T) {
	f := newFixture(t, accounts.Open, nil)
	for _, p := range []string{"/issues", "/issues/login", "/issues/MN1"} {
		if code, _, _ := f.get(f.c, p); code != http.StatusNotFound {
			t.Errorf("%s without the tracker: %d", p, code)
		}
	}
	if _, body, _ := f.get(f.c, "/"); strings.Contains(body, "Check the rules") {
		t.Error("the nav links the tracker while it is off")
	}
	if _, body, _ := f.get(f.c, "/privacy"); strings.Contains(body, "issue tracker") {
		t.Error("the privacy text names the tracker while it is off")
	}
}

func TestIssuesLogin(t *testing.T) {
	xf := newIssuesFixture(t)
	c := xf.newClient()
	// everything needs a login
	for _, p := range []string{"/issues", "/issues/MN2?filter=all"} {
		code, _, h := xf.get(c, p)
		if code != http.StatusSeeOther || h.Get("Location") != "/issues/login?next="+url.QueryEscape(p) {
			t.Errorf("%s without a session: %d %q", p, code, h.Get("Location"))
		}
	}
	if _, body, _ := xf.get(c, "/"); !strings.Contains(body, `<a href="/issues">Check the rules</a>`) {
		t.Error("no nav link to the tracker")
	}
	if _, body, _ := xf.get(c, "/privacy"); !strings.Contains(body, "Your claims on the issue tracker") {
		t.Error("the privacy text doesn't name the claims")
	}
	// refused: a wrong password, an unknown name, a disabled account; one text for the first two
	for _, x := range []struct {
		name, pw string
		code     int
		text     string
	}{
		{"player1", "wrong-password", 401, "Wrong login name or password."},
		{"nobody", "whatever-password", 401, "Wrong login name or password."},
	} {
		code, body, h := xf.submit(c, "/issues/login", map[string]string{"name": x.name, "password": x.pw})
		if code != x.code || !strings.Contains(body, x.text) || strings.Contains(h.Get("Set-Cookie"), PlayerCookie) {
			t.Errorf("login %s: %d %q\n%s", x.name, code, h.Get("Set-Cookie"), body)
		}
	}
	xf.st.SetStatus(xf.p2.UserID, store.StatusDisabled)
	if code, body, _ := xf.submit(c, "/issues/login", map[string]string{"name": "player2", "password": xf.pw2}); code != 403 ||
		!strings.Contains(body, "This account is disabled.") {
		t.Errorf("a disabled account: %d\n%s", code, body)
	}
	// accepted: the cookie, then back to where the player came from (only a tracker page)
	code, _, h := xf.submit(c, "/issues/login", map[string]string{"name": "Player1", "password": xf.pw1, "next": "/issues/MN2?filter=all"})
	if code != http.StatusSeeOther || h.Get("Location") != "/issues/MN2?filter=all" {
		t.Fatalf("login: %d %q", code, h.Get("Location"))
	}
	ck := h.Get("Set-Cookie")
	for _, want := range []string{PlayerCookie + "=", "Path=/", "Max-Age=2592000", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(ck, want) {
			t.Errorf("the cookie %q lacks %s", ck, want)
		}
	}
	if strings.Contains(ck, "Domain") {
		t.Errorf("the cookie has a domain: %q", ck)
	}
	if code, body, _ := xf.get(c, "/issues"); code != 200 || !strings.Contains(body, "Logged in as "+xf.p1.Personas[0].Name) {
		t.Errorf("/issues logged in: %d\n%s", code, body)
	}
	if code, _, h := xf.get(c, "/issues/login"); code != http.StatusSeeOther || h.Get("Location") != "/issues" {
		t.Errorf("the login form while logged in: %d %q", code, h.Get("Location"))
	}
	if !strings.Contains(xf.logText(), "logged into the issue tracker") || strings.Contains(xf.logText(), xf.pw1) {
		t.Errorf("the log:\n%s", xf.logText())
	}
	// never off-site afterwards
	for _, next := range []string{"https://evil.example/", "//evil.example/issues", "/issues/../admin", "/admin"} {
		c2 := xf.newClient()
		_, _, h := xf.submit(c2, "/issues/login", map[string]string{"name": "player1", "password": xf.pw1, "next": next})
		if h.Get("Location") != "/issues" {
			t.Errorf("next %q: %q", next, h.Get("Location"))
		}
	}
	// logout
	code, body, _ := xf.postFrom(c, "/issues", "/issues/logout", nil)
	if code != 200 || !strings.Contains(body, "Logged out.") {
		t.Errorf("logout: %d\n%s", code, body)
	}
	if code, _, _ := xf.get(c, "/issues"); code != http.StatusSeeOther {
		t.Errorf("after the logout: %d", code)
	}
}

func TestIssuesSessionEnds(t *testing.T) {
	xf := newIssuesFixture(t)
	// a ban ends the session at the next request
	c := xf.login("player1", xf.pw1)
	if err := xf.st.SetStatus(xf.p1.UserID, store.StatusBanned); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := xf.get(c, "/issues"); code != http.StatusSeeOther {
		t.Errorf("banned: %d", code)
	}
	xf.st.SetStatus(xf.p1.UserID, store.StatusActive)
	if code, _, _ := xf.get(c, "/issues"); code != http.StatusSeeOther {
		t.Errorf("the ban's session came back: %d", code)
	}
	// a POST without a session: the login form, 403
	code, body, _ := xf.postFrom(c, "/issues/login", "/issues/MN2/claim", map[string]string{"stance": "correct"})
	if code != 403 || !strings.Contains(body, "Your session has ended") {
		t.Errorf("a claim without a session: %d\n%s", code, body)
	}
	// idle: 7 days; at most 30 days
	c = xf.login("player1", xf.pw1)
	for i := 0; i < 4; i++ {
		xf.advance(6 * 24 * time.Hour)
		if code, _, _ := xf.get(c, "/issues"); code != 200 {
			t.Fatalf("day %d: %d", 6*(i+1), code)
		}
	}
	xf.advance(6*24*time.Hour + time.Minute) // past day 30 since the login
	if code, _, _ := xf.get(c, "/issues"); code != http.StatusSeeOther {
		t.Errorf("after 30 days: %d", code)
	}
	c = xf.login("player1", xf.pw1)
	xf.advance(7*24*time.Hour + time.Minute)
	if code, _, _ := xf.get(c, "/issues"); code != http.StatusSeeOther {
		t.Errorf("after 7 idle days: %d", code)
	}
	// the cap: the least recently used session goes
	xf.site.players.cap = 2
	a := xf.login("player1", xf.pw1)
	xf.advance(time.Minute)
	b := xf.login("player2", xf.pw2)
	xf.advance(time.Minute)
	xf.get(a, "/issues")
	xf.advance(time.Minute)
	xf.login("player2", xf.pw2)
	if code, _, _ := xf.get(b, "/issues"); code != http.StatusSeeOther {
		t.Errorf("the least recently used session stayed: %d", code)
	}
	if code, _, _ := xf.get(a, "/issues"); code != 200 {
		t.Errorf("a recently used session went: %d", code)
	}
}

// section returns the list page's part under the heading that starts with h (up to the next heading).
func section(body, h string) string {
	i := strings.Index(body, "<h2>"+h)
	if i < 0 {
		return ""
	}
	rest := body[i+4:]
	if j := strings.Index(rest, "<h2>"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

var rowIDRE = regexp.MustCompile(`<span class="id">([A-Z0-9]+)</span>`)

func rowIDs(s string) string {
	var ids []string
	for _, m := range rowIDRE.FindAllStringSubmatch(s, -1) {
		ids = append(ids, m[1])
	}
	return strings.Join(ids, " ")
}

func TestIssuesListFilters(t *testing.T) {
	xf := newIssuesFixture(t)
	now := t0
	put := func(id string, user int64, stance store.Stance, videos ...store.ClaimVideo) {
		t.Helper()
		if _, err := xf.st.PutClaim(id, user, stance, "", videos, now); err != nil {
			t.Fatal(err)
		}
	}
	vid := store.ClaimVideo{VideoID: "dQw4w9WgXcQ", Seconds: 83}
	put("MN2", xf.p2.UserID, store.StanceCorrect)                                                           // needs video proof
	put("MN3", xf.p2.UserID, store.StanceCorrect, vid)                                                      // leaning correct
	put("TW1", xf.p2.UserID, store.StanceCorrect, vid)                                                      // disputed: 3 against 3
	put("TW1", xf.p1.UserID, store.StanceWrong, vid, store.ClaimVideo{VideoID: "abcdefghijk", Seconds: -1}) // the viewer's own
	if err := xf.st.SetVerdict("TW2", store.IssueConfirmed, "Seen in a 2014 video.", "admin boss", now); err != nil {
		t.Fatal(err)
	}
	c := xf.login("player1", xf.pw1)
	for _, x := range []struct {
		filter, top, minions, towers string
	}{
		{"", "MN1", "", ""}, // the default: needs verification
		{"needs-verification", "MN1", "", ""},
		{"needs-video", "", "MN2", ""},
		{"disputed", "", "", "TW1"},
		{"leaning", "", "MN3", ""},
		{"closed", "TW2", "", ""},
		{"all", "MN1 TW2", "MN2 MN3", "TW1"},
		{"bogus", "MN1", "", ""},
	} {
		p := "/issues"
		if x.filter != "" {
			p += "?filter=" + x.filter
		}
		code, body, _ := xf.get(c, p)
		if code != 200 {
			t.Fatalf("%s: %d", p, code)
		}
		if got := rowIDs(section(body, "Most important")); got != x.top {
			t.Errorf("%s: most important %q, want %q", p, got, x.top)
		}
		if got := rowIDs(section(body, "Minions")); got != x.minions {
			t.Errorf("%s: minions %q, want %q", p, got, x.minions)
		}
		if got := rowIDs(section(body, "Towers")); got != x.towers {
			t.Errorf("%s: towers %q, want %q", p, got, x.towers)
		}
		if strings.Contains(body, "<script") {
			t.Errorf("%s: a script", p)
		}
	}
	_, body, _ := xf.get(c, "/issues?filter=all")
	// the order: How this works, the filters with their counts, the most important, then the areas with counts
	order := []string{"How this works", `<a href="/issues?filter=needs-verification">Needs verification (1)</a>`,
		`<a href="/issues?filter=needs-video">Needs video proof (1)</a>`, `<a href="/issues?filter=disputed">Disputed (1)</a>`,
		`<a href="/issues?filter=leaning">Leaning (1)</a>`, `<a href="/issues?filter=closed">Closed (1)</a>`, `<strong>All (5)</strong>`,
		"<h2>Most important (2)</h2>", "<h2>Minions (2)</h2>", "<h2>Towers (1)</h2>"}
	at := 0
	for _, s := range order {
		i := strings.Index(body[at:], s)
		if i < 0 {
			t.Fatalf("%q missing or out of order:\n%s", s, body)
		}
		at += i
	}
	// the rows: chip, videos, the own mark, links keeping the filter
	for _, want := range []string{
		`<span class="chip chip-needs-verification">Needs verification</span>`,
		`<span class="chip chip-confirmed">Confirmed</span>`,
		`<span class="chip chip-disputed">Disputed</span> <span class="muted">3 videos</span> <span class="own own-wrong">you: looks wrong</span>`,
		`<span class="chip chip-leaning-correct">Leaning correct</span> <span class="muted">1 video</span></li>`,
		`<a href="/issues/TW1?filter=all">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the list lacks %s", want)
		}
	}
	if _, body, _ = xf.get(c, "/issues"); !strings.Contains(body, `<a href="/issues/MN1">`) {
		t.Error("the default filter's links carry a filter")
	}
	// previous and next within the filter, in the list's order (the most important first)
	_, body, _ = xf.get(c, "/issues/MN2?filter=all")
	if !strings.Contains(body, `<a href="/issues/TW2?filter=all" rel="prev">`) || !strings.Contains(body, `<a href="/issues/MN3?filter=all" rel="next">`) {
		t.Errorf("MN2's neighbours under all:\n%s", body)
	}
	_, body, _ = xf.get(c, "/issues/MN2?filter=leaning") // not in the filter: its nearest that are
	if strings.Contains(body, `rel="prev"`) || !strings.Contains(body, `<a href="/issues/MN3?filter=leaning" rel="next">`) {
		t.Errorf("MN2's neighbours under leaning:\n%s", body)
	}
	if code, _, _ := xf.get(c, "/issues/XX9"); code != 404 {
		t.Errorf("an unknown issue: %d", code)
	}
}

func TestIssuesClaim(t *testing.T) {
	xf := newIssuesFixture(t)
	c := xf.login("player1", xf.pw1)
	code, body, _ := xf.get(c, "/issues/MN2")
	for _, want := range []string{`<span class="chip chip-needs-verification">`, "What we assumed", "Minions walk at 325.",
		`<div class="lookfor">`, "How fast they walk.", "No claims yet.", `action="/issues/MN2/claim"`, "Make a claim",
		`<span class="side-correct p0"></span><span class="side-wrong p0"></span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the new issue's page lacks %s", want)
		}
	}
	if code != 200 || strings.Contains(body, "Delete my claim") {
		t.Errorf("%d, a delete button without a claim", code)
	}
	// create: a timed video and a note with markup, which stays text
	note := "At 1:23 the minions <b>ignore</b> me. See https://evil.example/x"
	code, _, h := xf.claim(c, "MN2", map[string]string{"stance": "correct", "note": note,
		"link1": "https://youtu.be/dQw4w9WgXcQ?si=abc", "time1": "1:23", "filter": "needs-verification"})
	if code != http.StatusSeeOther || h.Get("Location") != "/issues/MN2?done=saved" {
		t.Fatalf("claim: %d %q", code, h.Get("Location"))
	}
	first, _, _ := xf.st.IssueDetail("MN2", 0)
	if len(first.Claims) != 1 || first.Claims[0].Videos[0] != (store.ClaimVideo{VideoID: "dQw4w9WgXcQ", Seconds: 83}) {
		t.Fatalf("stored: %+v", first.Claims)
	}
	_, body, _ = xf.get(c, "/issues/MN2?done=saved")
	for _, want := range []string{"Your claim is saved.", `<span class="chip chip-leaning-correct">`,
		`<a href="https://www.youtube.com/watch?v=dQw4w9WgXcQ&amp;t=83s" rel="nofollow noopener noreferrer ugc">Watch at 1:23</a>`,
		html.EscapeString(note), xf.p1.Personas[0].Name + " (you)", "weight 3", `<span class="side-correct p100"></span><span class="side-wrong p0"></span>`,
		`value="correct" checked`, "Save my changes", "Delete my claim",
		`value="https://www.youtube.com/watch?v=dQw4w9WgXcQ&amp;t=83s"`, `name="time1" value="1:23"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page after the claim lacks %s", want)
		}
	}
	if strings.Contains(body, "<b>ignore") || strings.Contains(body, `href="https://evil.example`) || strings.Contains(body, "<iframe") ||
		strings.Contains(body, "<img") {
		t.Error("the note isn't plain text, or a video is embedded")
	}
	if !strings.Contains(xf.logText(), "claim on MN2 saved (correct, 1 video(s)") || strings.Contains(xf.logText(), "ignore") {
		t.Errorf("the log:\n%s", xf.logText())
	}
	// a second player against it: 1 against 3 still leans
	c2 := xf.login("player2", xf.pw2)
	if code, _, _ := xf.claim(c2, "MN2", map[string]string{"stance": "wrong", "note": "Not in my video."}); code != http.StatusSeeOther {
		t.Fatalf("player2's claim: %d", code)
	}
	_, body, _ = xf.get(c, "/issues/MN2")
	if !strings.Contains(body, `<span class="chip chip-leaning-correct">`) || !strings.Contains(body, "side-correct p75") ||
		strings.Index(body, xf.p1.Personas[0].Name+" (you)") > strings.Index(body, "Not in my video.") {
		t.Errorf("two claims: the chip, the bar or best evidence first:\n%s", body)
	}
	// edit: the same claim, now wrong, two untimed videos (a filter kept)
	xf.advance(time.Hour)
	code, _, h = xf.claim(c, "MN2", map[string]string{"stance": "wrong", "note": "", "link1": "youtube.com/shorts/abcdefghijk",
		"link3": "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "filter": "all"})
	if code != http.StatusSeeOther || h.Get("Location") != "/issues/MN2?done=saved&filter=all" {
		t.Fatalf("edit: %d %q", code, h.Get("Location"))
	}
	d, _, _ := xf.st.IssueDetail("MN2", 0)
	own, _ := d.ClaimOf(xf.p1.UserID)
	if own.ID != first.Claims[0].ID || own.Stance != store.StanceWrong || len(own.Videos) != 2 || own.Note != "" || !own.Created.Equal(t0) {
		t.Errorf("edited: %+v", own)
	}
	_, body, _ = xf.get(c, "/issues/MN2")
	if !strings.Contains(body, `<span class="chip chip-leaning-wrong">`) || !strings.Contains(body, ">Watch</a>") ||
		!strings.Contains(body, "edited 2026-10-13") {
		t.Errorf("after the edit:\n%s", body)
	}
	if _, list, _ := xf.get(c, "/issues?filter=leaning"); !strings.Contains(list, "you: looks wrong") {
		t.Error("the list lacks the own mark")
	}
	// delete
	code, _, h = xf.postFrom(c, "/issues/MN2", "/issues/MN2/claim/delete", map[string]string{"filter": "needs-verification"})
	if code != http.StatusSeeOther || h.Get("Location") != "/issues/MN2?done=deleted" {
		t.Fatalf("delete: %d %q", code, h.Get("Location"))
	}
	_, body, _ = xf.get(c, "/issues/MN2?done=deleted")
	if !strings.Contains(body, "Your claim is deleted.") || strings.Contains(body, xf.p1.Personas[0].Name+" (you)") ||
		!strings.Contains(body, `<span class="chip chip-needs-video">`) {
		t.Errorf("after the delete:\n%s", body)
	}
	// a hidden claim: only its own player sees it, marked
	d, _, _ = xf.st.IssueDetail("MN2", 0)
	xf.st.SetClaimHidden(d.Claims[0].ID, true, t0)
	if _, body, _ = xf.get(c, "/issues/MN2"); strings.Contains(body, "Not in my video.") {
		t.Error("another player's hidden claim is shown")
	}
	if _, body, _ = xf.get(c2, "/issues/MN2"); !strings.Contains(body, "hidden by an admin: only you see it") {
		t.Error("the own hidden claim isn't shown marked")
	}
}

func TestIssuesClaimRefused(t *testing.T) {
	xf := newIssuesFixture(t)
	c := xf.login("player1", xf.pw1)
	// a link that isn't YouTube's: refused, the form kept as typed
	code, body, _ := xf.claim(c, "MN3", map[string]string{"stance": "wrong", "note": "My note stays.",
		"link1": "https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ", "time1": "0:42", "link2": "https://youtu.be/dQw4w9WgXcQ"})
	for _, want := range []string{"Your claim isn&#39;t saved", "Only YouTube links are taken", "My note stays.", `value="wrong" checked`,
		`value="https://youtube.com.evil.example/watch?v=dQw4w9WgXcQ"`, `name="time1" value="0:42"`, `value="https://youtu.be/dQw4w9WgXcQ"`} {
		if !strings.Contains(body, want) {
			t.Errorf("a refused link: the page lacks %s", want)
		}
	}
	if code != 400 {
		t.Errorf("a refused link: %d", code)
	}
	for _, x := range []struct {
		fields map[string]string
		text   string
	}{
		{map[string]string{"note": "no stance"}, "Choose: looks correct, or looks wrong."},
		{map[string]string{"stance": "correct", "time2": "1:00"}, "A time needs a link."},
		{map[string]string{"stance": "correct", "link1": "https://youtu.be/dQw4w9WgXcQ", "time1": "soon"}, "The time isn"},
		{map[string]string{"stance": "correct", "note": strings.Repeat("ä", 1001)}, "The note is longer than 1000 characters."},
		{map[string]string{"stance": "correct", "note": "a‮b"}, "The note holds characters"},
		{map[string]string{"stance": "correct", "link1": "javascript:alert(1)"}, "Only YouTube links are taken"},
	} {
		if code, body, _ := xf.claim(c, "MN3", x.fields); code != 400 || !strings.Contains(body, html.EscapeString(x.text)) {
			t.Errorf("%v: %d, no %q", x.fields, code, x.text)
		}
	}
	// a note of 1,000 four-byte characters fits the form's body limit
	if code, _, _ := xf.claim(c, "MN3", map[string]string{"stance": "correct", "note": strings.Repeat("\U0001F600", 1000)}); code != http.StatusSeeOther {
		t.Errorf("a long note: %d", code)
	}
	// CSRF: no token, a wrong one, another site's origin; the form stays
	for _, x := range []struct {
		v   url.Values
		hdr map[string]string
	}{
		{url.Values{"stance": {"wrong"}, "note": {"Kept after CSRF."}}, nil},
		{url.Values{"stance": {"wrong"}, "note": {"Kept after CSRF."}, CSRFField: {"forged"}}, nil},
		{url.Values{"stance": {"wrong"}, "note": {"Kept after CSRF."}}, map[string]string{"Sec-Fetch-Site": "cross-site"}},
	} {
		code, body, _ := xf.post(c, "/issues/MN3/claim", x.v, x.hdr)
		if code != 403 || !strings.Contains(body, "The form had expired") || !strings.Contains(body, "Kept after CSRF.") {
			t.Errorf("CSRF %v: %d\n%s", x.v, code, body)
		}
	}
	if code, _, _ := xf.post(c, "/issues/MN3/claim/delete", url.Values{}, nil); code != 403 {
		t.Errorf("a delete without a token: %d", code)
	}
	d, _, _ := xf.st.IssueDetail("MN3", 0)
	if own, _ := d.ClaimOf(xf.p1.UserID); own.Stance != store.StanceCorrect {
		t.Errorf("a refused form changed the claim: %+v", own)
	}
	// an unknown issue
	if code, _, _ := xf.postFrom(c, "/issues/MN3", "/issues/XX9/claim", map[string]string{"stance": "correct"}); code != 404 {
		t.Errorf("a claim on an unknown issue: %d", code)
	}
}

func TestIssuesClosedReadOnly(t *testing.T) {
	xf := newIssuesFixture(t)
	c := xf.login("player1", xf.pw1)
	if code, _, _ := xf.claim(c, "TW1", map[string]string{"stance": "correct", "link1": "https://youtu.be/dQw4w9WgXcQ?t=95"}); code != http.StatusSeeOther {
		t.Fatalf("claim: %d", code)
	}
	_, form, _ := xf.get(c, "/issues/TW1") // a form opened before the verdict
	token := tokenRE.FindStringSubmatch(form)[1]
	if err := xf.st.SetVerdict("TW1", store.IssueDisproven, "Towers reach 700 in the original.\nFixed in the next release.", "api:ai", t0); err != nil {
		t.Fatal(err)
	}
	_, body, _ := xf.get(c, "/issues/TW1")
	for _, want := range []string{`<span class="chip chip-disproven">Disproven</span>`, "Towers reach 700 in the original.\nFixed",
		"This issue is closed: it is read-only.", "Watch at 1:35"} {
		if !strings.Contains(body, want) {
			t.Errorf("the closed issue's page lacks %q", want)
		}
	}
	if strings.Contains(body, `action="/issues/TW1/claim"`) || strings.Contains(body, "Delete my claim") || strings.Contains(body, "api:ai") {
		t.Errorf("a closed issue has a form, or names who closed it:\n%s", body)
	}
	for _, p := range []string{"/issues/TW1/claim", "/issues/TW1/claim/delete"} {
		code, body, _ := xf.post(c, p, url.Values{CSRFField: {token}, "stance": {"wrong"}}, nil)
		if code != http.StatusConflict || !strings.Contains(body, "This issue is closed") {
			t.Errorf("%s on a closed issue: %d", p, code)
		}
	}
	d, _, _ := xf.st.IssueDetail("TW1", 0)
	if len(d.Claims) != 1 || d.Claims[0].Stance != store.StanceCorrect {
		t.Errorf("the closed issue's claims changed: %+v", d.Claims)
	}
	// a withdrawn issue (no longer on the list): shown, read-only
	if _, err := issues.Sync(xf.st, testEntries[:4], t0); err != nil {
		t.Fatal(err)
	}
	_, body, _ = xf.get(c, "/issues/TW2")
	if !strings.Contains(body, "no longer on our list") || strings.Contains(body, `action="/issues/TW2/claim"`) {
		t.Errorf("a withdrawn issue:\n%s", body)
	}
	if _, list, _ := xf.get(c, "/issues?filter=all"); strings.Contains(list, ">TW2<") {
		t.Error("a withdrawn issue is listed")
	}
}

func TestIssuesExportAndDeletion(t *testing.T) {
	xf := newIssuesFixture(t)
	c := xf.login("player1", xf.pw1)
	if code, _, _ := xf.claim(c, "MN1", map[string]string{"stance": "wrong", "note": "Seen otherwise.", "link1": "https://youtu.be/dQw4w9WgXcQ", "time1": "2:00"}); code != http.StatusSeeOther {
		t.Fatalf("claim: %d", code)
	}
	code, body, _ := xf.submit(c, "/account/data", map[string]string{"name": "player1", "password": xf.pw1})
	for _, want := range []string{`"issueClaims": [`, `"issue": "MN1"`, `"issueTitle": "Who a minion attacks"`, `"stance": "wrong"`,
		`"note": "Seen otherwise."`, `"video": "dQw4w9WgXcQ"`, `"seconds": 120`} {
		if !strings.Contains(body, want) {
			t.Errorf("the export lacks %s", want)
		}
	}
	if code != 200 {
		t.Errorf("export: %d", code)
	}
	if _, body, _ := xf.get(c, "/account/data"); !strings.Contains(body, "your claims on the issue tracker") {
		t.Error("the data page doesn't name the claims")
	}
	code, _, _ = xf.submit(c, "/account/delete", map[string]string{"name": "player1", "password": xf.pw1, "confirm": "yes"})
	if code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _, _ := xf.get(c, "/issues"); code != http.StatusSeeOther {
		t.Errorf("the deleted account's session: %d", code)
	}
	c2 := xf.login("player2", xf.pw2)
	if _, body, _ := xf.get(c2, "/issues/MN1"); strings.Contains(body, "Seen otherwise.") || !strings.Contains(body, "No claims yet.") {
		t.Error("the deleted account's claim is still shown")
	}
}
