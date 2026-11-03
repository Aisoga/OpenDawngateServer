package site

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/issues"
	"dawngate/server/internal/store"
)

// The issue tracker's pages for players (Config.Issues): our guesses about the original game that a player can
// check against videos of it. A player logs in with the game account (issues_session.go), reads the list (filtered
// by status, the most important first, then by area), opens an issue (what we assumed, what to look for, the
// claims, best evidence first) and makes, changes or deletes the own claim: looks correct or looks wrong, an
// optional plain-text note and up to three YouTube links, each with an optional timestamp (issues.ParseLink keeps
// only the video's id and start; the page shows the canonical link, never an embed). A closed issue (a verdict) is
// read-only. Every POST passes guard (the CSRF token and the form rate); the login also the game login's limiter
// (limitLogin, loginDone) when dgserver's limits are on.
//
// Routes: GET/POST /issues/login, POST /issues/logout, GET /issues, GET /issues/{id}, POST /issues/{id}/claim,
// POST /issues/{id}/claim/delete.

// maxClaimBody bounds a claim's form: a note of MaxNote characters may take 4 bytes each, 12 once %-escaped.
const maxClaimBody = 16 << 10

func (s *Site) issuesRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /issues/login", s.issuesLoginForm)
	m.HandleFunc("POST /issues/login", s.issuesLogin)
	m.HandleFunc("POST /issues/logout", s.issuesLogout)
	m.HandleFunc("GET /issues", s.issuesList)
	m.HandleFunc("GET /issues/{id}", s.issuePage)
	m.HandleFunc("POST /issues/{id}/claim", s.issueClaim)
	m.HandleFunc("POST /issues/{id}/claim/delete", s.issueClaimDelete)
}

// ---- the filters ----

// issueFilter is one of the list's filters.
type issueFilter struct {
	ID, Label string
	match     func(issues.Status) bool
}

// issueFilters are the list's filters in the page's order; the first is the default.
var issueFilters = []issueFilter{
	{"needs-verification", "Needs verification", func(st issues.Status) bool { return st == issues.NeedsVerification }},
	{"needs-video", "Needs video proof", func(st issues.Status) bool { return st == issues.NeedsVideo }},
	{"disputed", "Disputed", func(st issues.Status) bool { return st == issues.Disputed }},
	{"leaning", "Leaning", func(st issues.Status) bool { return st == issues.LeaningCorrect || st == issues.LeaningWrong }},
	{"closed", "Closed", func(st issues.Status) bool { return !st.Open() }},
	{"all", "All", func(issues.Status) bool { return true }},
}

// filterOf is the request's filter (?filter=; an unknown or missing one is the default).
func filterOf(id string) issueFilter {
	for _, f := range issueFilters {
		if f.ID == id {
			return f
		}
	}
	return issueFilters[0]
}

// ---- the views ----

// issuesView is the tracker pages' data (view.Tracker).
type issuesView struct {
	Player  string // who is logged in (the display name; the login name when it has none)
	Next    string // the login form: where to go afterwards
	Filter  string // the filter's id
	Label   string // the filter's label
	Filters []filterLink
	Top     []issueRowView
	Areas   []areaView
	Shown   int // issues the filter shows
	Issue   *issueDetailView
}

type filterLink struct {
	ID, Label string
	Count     int
	On        bool
}

type areaView struct {
	Name string
	Rows []issueRowView
}

type issueRowView struct {
	ID, Title, Area string
	Status          issues.Status
	Videos          int
	Own             string // the player's own stance ("correct", "wrong", "" = none)
	Top             bool
}

// issueRows is the list template's "issuerows" argument: rows and the filter their links keep.
type issueRows struct {
	Rows   []issueRowView
	Filter string
}

func rowsArg(rows []issueRowView, filter string) issueRows { return issueRows{rows, filter} }

type issueDetailView struct {
	issueRowView
	Assumed, LookFor string
	Open             bool // takes claims (open and still listed)
	Withdrawn        bool // no longer on the list
	VerdictNote      string
	ClosedAt         string
	Correct, Wrong   int // the visible claims' weights
	CorrectPct       int // the weight bar's share of "looks correct", in steps of 5 (0..100)
	WrongPct         int
	Claims           []claimView
	HasOwn           bool
	Form             claimForm
	Prev, Next       *issueRowView // within the filter
}

type claimView struct {
	Stance, StanceLabel string
	Persona             string
	Date, Edited        string
	Note                string
	Videos              []videoView
	Weight              int
	Own, Hidden         bool
}

type videoView struct {
	URL, Label, Short string
}

type claimForm struct {
	Stance string // "correct", "wrong" or ""
	Note   string
	Links  []linkField
	Errors map[string]string // field -> its error
}

type linkField struct {
	N               int
	Link, Time, Err string
}

func stanceID(s store.Stance) string {
	switch s {
	case store.StanceCorrect:
		return "correct"
	case store.StanceWrong:
		return "wrong"
	}
	return ""
}

func stanceLabel(s store.Stance) string {
	if s == store.StanceWrong {
		return "Looks wrong"
	}
	return "Looks correct"
}

func rowView(r store.IssueRow) issueRowView {
	return issueRowView{ID: r.ID, Title: r.Title, Area: r.Area, Status: issues.StatusOf(r),
		Videos: r.Correct.Videos + r.Wrong.Videos, Own: stanceID(r.Own), Top: r.Top}
}

// displayOrder is the list page's order: the most important first, then the others by area (the areas in the order
// of their first issue), each in the list's order.
func displayOrder(rows []store.IssueRow) []store.IssueRow {
	var out []store.IssueRow
	for _, r := range rows {
		if r.Top {
			out = append(out, r)
		}
	}
	var areas []string
	byArea := map[string][]store.IssueRow{}
	for _, r := range rows {
		if r.Top {
			continue
		}
		if _, ok := byArea[r.Area]; !ok {
			areas = append(areas, r.Area)
		}
		byArea[r.Area] = append(byArea[r.Area], r)
	}
	for _, a := range areas {
		out = append(out, byArea[a]...)
	}
	return out
}

// tracker is the pages' common part: who is logged in, the filters with their counts.
func (s *Site) tracker(sess *playerSession, rows []store.IssueRow, f issueFilter) *issuesView {
	v := &issuesView{Player: sess.persona, Filter: f.ID, Label: f.Label}
	if v.Player == "" {
		v.Player = sess.name
	}
	for _, x := range issueFilters {
		n := 0
		for _, r := range rows {
			if x.match(issues.StatusOf(r)) {
				n++
			}
		}
		v.Filters = append(v.Filters, filterLink{ID: x.ID, Label: x.Label, Count: n, On: x.ID == f.ID})
	}
	return v
}

// ---- the login ----

// nextRE is where the login may send a player afterwards: a tracker page only (never another site).
var nextRE = regexp.MustCompile(`^/issues(/[A-Za-z0-9_.-]{1,64})?(\?filter=[a-z-]{1,32})?$`)

func safeNext(next string) string {
	if nextRE.MatchString(next) {
		return next
	}
	return "/issues"
}

func (s *Site) issuesLoginView(next string) *view {
	return &view{Title: "Check the rules: log in", Tracker: &issuesView{Next: safeNext(next)}}
}

// toLogin sends a GET without a session to the login form, which comes back here afterwards.
func (s *Site) toLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/issues/login?next="+url.QueryEscape(safeNext(r.URL.RequestURI())), http.StatusSeeOther)
}

func (s *Site) issuesLoginForm(w http.ResponseWriter, r *http.Request) {
	if s.playerSession(r) != nil {
		http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	v := s.issuesLoginView(r.URL.Query().Get("next"))
	v.CSRF = s.csrfToken(w, r)
	s.render(w, r, http.StatusOK, "issues_login", v)
}

// issuesLogin checks the game account's login name and password (the game login's limiter first when dgserver's
// limits are on); an active account gets a session and goes on to the page it came from.
func (s *Site) issuesLogin(w http.ResponseWriter, r *http.Request) {
	v := s.issuesLoginView("")
	if !s.guard(w, r, "issues_login", v) {
		return
	}
	v.Tracker.Next = safeNext(r.PostFormValue("next"))
	v.Name = strings.TrimSpace(r.PostFormValue("name"))
	key := accountKey(v.Name)
	if !s.limitLogin(w, r, "issues_login", v, key) {
		return
	}
	cred, res := accounts.Authenticate(s.cfg.Store, v.Name, r.PostFormValue("password"))
	s.loginDone(r, key, res)
	if res.Code != 0 {
		s.refused(w, r, statusOf(res), "issues_login", v, res)
		return
	}
	s.newPlayerSession(w, cred)
	noteFor(w, "account %d logged into the issue tracker", cred.UserID)
	http.Redirect(w, r, v.Tracker.Next, http.StatusSeeOther)
}

func (s *Site) issuesLogout(w http.ResponseWriter, r *http.Request) {
	v := s.issuesLoginView("")
	if !s.guard(w, r, "issues_login", v) {
		return
	}
	if sess := s.playerSession(r); sess != nil {
		noteFor(w, "account %d logged out of the issue tracker", sess.userID)
	}
	s.endPlayerSession(w, r)
	v.Done = "Logged out."
	v.CSRF = s.csrfToken(w, r)
	s.render(w, r, http.StatusOK, "issues_login", v)
}

// ---- the list ----

func (s *Site) issuesList(w http.ResponseWriter, r *http.Request) {
	sess := s.playerSession(r)
	if sess == nil {
		s.toLogin(w, r)
		return
	}
	rows, err := s.cfg.Store.ListIssues(store.IssueFilter{Viewer: sess.userID})
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	f := filterOf(r.URL.Query().Get("filter"))
	t := s.tracker(sess, rows, f)
	areaAt := map[string]int{}
	for _, row := range displayOrder(rows) {
		if !f.match(issues.StatusOf(row)) {
			continue
		}
		t.Shown++
		rv := rowView(row)
		if row.Top {
			t.Top = append(t.Top, rv)
			continue
		}
		i, ok := areaAt[row.Area]
		if !ok {
			i = len(t.Areas)
			areaAt[row.Area] = i
			t.Areas = append(t.Areas, areaView{Name: row.Area})
		}
		t.Areas[i].Rows = append(t.Areas[i].Rows, rv)
	}
	v := &view{Title: "Check the rules", Tracker: t, CSRF: s.csrfToken(w, r)}
	s.render(w, r, http.StatusOK, "issues_list", v)
}

func (s *Site) storeFailed(w http.ResponseWriter, r *http.Request, err error) {
	noteFor(w, "the store: %v", err)
	s.render(w, r, http.StatusInternalServerError, "message", &view{Title: "Not available",
		Text: "The server could not read the issues just now. Please try again later."})
}

// ---- one issue ----

// doneTexts are the issue page's success lines (?done=, after a claim's POST).
var doneTexts = map[string]string{
	"saved":   "Your claim is saved. You can change or delete it as long as the issue is open.",
	"deleted": "Your claim is deleted.",
}

func (s *Site) issuePage(w http.ResponseWriter, r *http.Request) {
	sess := s.playerSession(r)
	if sess == nil {
		s.toLogin(w, r)
		return
	}
	v, ok := s.issueView(w, r, sess, r.PathValue("id"), filterOf(r.URL.Query().Get("filter")))
	if !ok {
		return
	}
	v.Done = doneTexts[r.URL.Query().Get("done")]
	s.render(w, r, http.StatusOK, "issues_issue", v)
}

// issueView builds an issue's page (false: it answered already: no such issue, or the store failed).
func (s *Site) issueView(w http.ResponseWriter, r *http.Request, sess *playerSession, id string, f issueFilter) (*view, bool) {
	d, found, err := s.cfg.Store.IssueDetail(id, sess.userID)
	if err != nil {
		s.storeFailed(w, r, err)
		return nil, false
	}
	if !found {
		noteFor(w, "no issue %q", id)
		s.render(w, r, http.StatusNotFound, "message", &view{Title: "Not found", Text: "There is no such issue."})
		return nil, false
	}
	rows, err := s.cfg.Store.ListIssues(store.IssueFilter{Viewer: sess.userID})
	if err != nil {
		s.storeFailed(w, r, err)
		return nil, false
	}
	t := s.tracker(sess, rows, f)
	dv := &issueDetailView{issueRowView: rowView(d.IssueRow), Assumed: d.Assumed, LookFor: d.LookFor,
		Withdrawn: !d.InDoc, VerdictNote: d.VerdictNote}
	dv.Open = d.State == store.IssueOpen && d.InDoc
	if !d.ClosedAt.IsZero() {
		dv.ClosedAt = d.ClosedAt.UTC().Format("2006-01-02")
	}
	tally := issues.TallyOf(d.IssueRow)
	dv.Correct, dv.Wrong = tally.Correct, tally.Wrong
	if sum := tally.Correct + tally.Wrong; sum > 0 {
		dv.CorrectPct = int(math.Round(float64(tally.Correct)*20/float64(sum))) * 5
		dv.WrongPct = 100 - dv.CorrectPct
	}
	for _, c := range issues.BestFirst(d.Claims) {
		own := c.UserID == sess.userID
		if c.Hidden && !own { // a hidden claim: only its own player sees it (marked)
			continue
		}
		cv := claimView{Stance: stanceID(c.Stance), StanceLabel: stanceLabel(c.Stance), Persona: c.Persona,
			Date: c.Created.UTC().Format("2006-01-02"), Note: c.Note, Weight: issues.ClaimWeight(c), Own: own,
			Hidden: c.Hidden}
		if cv.Persona == "" {
			cv.Persona = "A player"
		}
		if !c.Updated.Equal(c.Created) {
			cv.Edited = c.Updated.UTC().Format("2006-01-02")
		}
		for _, vid := range issues.Videos(c) {
			vv := videoView{URL: vid.URL(), Label: "Watch", Short: "youtube.com/watch?v=" + vid.ID}
			if ts := vid.Timestamp(); ts != "" {
				vv.Label = "Watch at " + ts
			}
			cv.Videos = append(cv.Videos, vv)
		}
		dv.Claims = append(dv.Claims, cv)
	}
	dv.Form = claimForm{Links: emptyLinks()}
	if own, ok := d.ClaimOf(sess.userID); ok {
		dv.HasOwn = true
		dv.Form.Stance, dv.Form.Note = stanceID(own.Stance), own.Note
		for i, vid := range issues.Videos(own) {
			if i < len(dv.Form.Links) {
				dv.Form.Links[i].Link = vid.URL()
				dv.Form.Links[i].Time = vid.Timestamp()
			}
		}
	}
	// previous and next within the filter, in the list page's order (an issue the filter doesn't show: its
	// nearest neighbours that it does)
	order := displayOrder(rows)
	at := -1
	for i, row := range order {
		if row.ID == d.ID {
			at = i
		}
	}
	if at >= 0 {
		for i := at - 1; i >= 0; i-- {
			if f.match(issues.StatusOf(order[i])) {
				p := rowView(order[i])
				dv.Prev = &p
				break
			}
		}
		for i := at + 1; i < len(order); i++ {
			if f.match(issues.StatusOf(order[i])) {
				n := rowView(order[i])
				dv.Next = &n
				break
			}
		}
	}
	t.Issue = dv
	return &view{Title: d.ID + ": " + d.Title, Tracker: t, CSRF: s.csrfToken(w, r)}, true
}

func emptyLinks() []linkField {
	ls := make([]linkField, store.MaxClaimVideos)
	for i := range ls {
		ls[i].N = i + 1
	}
	return ls
}

// issueAction starts a claim's POST: the session (else the login form with 403), the issue's page (it answers a 404
// itself), then guard (CSRF, form rate), whose refusal shows that page with the form as sent (fill).
func (s *Site) issueAction(w http.ResponseWriter, r *http.Request, fill func(*issueDetailView)) (*playerSession, *view, bool) {
	sess := s.playerSession(r)
	if sess == nil {
		v := s.issuesLoginView("/issues/" + r.PathValue("id"))
		v.Error = "Your session has ended: log in again."
		v.CSRF = s.csrfToken(w, r)
		noteFor(w, "refused: no player session")
		s.render(w, r, http.StatusForbidden, "issues_login", v)
		return nil, nil, false
	}
	if err := r.ParseForm(); err != nil { // as guard answers it (a second ParseForm would report no error)
		noteFor(w, "form not read: %v", err)
		s.render(w, r, http.StatusRequestEntityTooLarge, "message", &view{Title: "Form refused",
			Text: "The form could not be read. Go back, reload the page and try again."})
		return nil, nil, false
	}
	v, ok := s.issueView(w, r, sess, r.PathValue("id"), filterOf(r.PostFormValue("filter")))
	if !ok {
		return nil, nil, false
	}
	if fill != nil && v.Tracker.Issue.Open {
		fill(v.Tracker.Issue)
	}
	if !s.guard(w, r, "issues_issue", v) {
		return nil, nil, false
	}
	return sess, v, true
}

// backTo is the issue page after a claim's POST (303: a reload doesn't send the form again).
func backTo(w http.ResponseWriter, r *http.Request, id, filter, done string) {
	q := url.Values{"done": {done}}
	if filter != issueFilters[0].ID {
		q.Set("filter", filter)
	}
	http.Redirect(w, r, "/issues/"+url.PathEscape(id)+"?"+q.Encode(), http.StatusSeeOther)
}

// issueClaim makes or changes the player's claim. A refused field comes back with the form as typed.
func (s *Site) issueClaim(w http.ResponseWriter, r *http.Request) {
	var form claimForm
	var stance store.Stance
	var note string
	var videos []store.ClaimVideo
	read := false
	sess, v, ok := s.issueAction(w, r, func(d *issueDetailView) {
		form, stance, note, videos = readClaim(r)
		read = true
		d.Form = form
	})
	if !ok {
		return
	}
	id := v.Tracker.Issue.ID
	if !read { // closed or withdrawn: read-only
		noteFor(w, "account %d: claim on %s refused: the issue is closed", sess.userID, id)
		v.Error = "This issue is closed: its claims can't be changed any more."
		s.render(w, r, http.StatusConflict, "issues_issue", v)
		return
	}
	if len(form.Errors) > 0 {
		noteFor(w, "account %d: claim on %s refused (%d field(s))", sess.userID, id, len(form.Errors))
		v.Error = "Your claim isn't saved: see the marked fields."
		s.render(w, r, http.StatusBadRequest, "issues_issue", v)
		return
	}
	if _, err := s.cfg.Store.PutClaim(id, sess.userID, stance, note, videos, s.cfg.Now()); err != nil {
		s.claimFailed(w, r, sess, v, err)
		return
	}
	noteFor(w, "account %d: claim on %s saved (%s, %d video(s), a note of %d characters)", sess.userID, id,
		stanceID(stance), len(videos), len([]rune(note)))
	backTo(w, r, id, v.Tracker.Filter, "saved")
}

// readClaim reads a claim's form: the stance, the note (issues.CheckNote) and the links (issues.ParseLink); a
// refused field has its error in form.Errors, and the form keeps what was typed.
func readClaim(r *http.Request) (form claimForm, stance store.Stance, note string, videos []store.ClaimVideo) {
	form = claimForm{Stance: r.PostFormValue("stance"), Note: r.PostFormValue("note"), Links: emptyLinks(),
		Errors: map[string]string{}}
	switch form.Stance {
	case "correct":
		stance = store.StanceCorrect
	case "wrong":
		stance = store.StanceWrong
	default:
		form.Stance = ""
		form.Errors["stance"] = "Choose: looks correct, or looks wrong."
	}
	note, err := issues.CheckNote(form.Note)
	if err != nil {
		form.Errors["note"] = upperFirst(err.Error()) + "."
	}
	for i := range form.Links {
		l := &form.Links[i]
		l.Link = strings.TrimSpace(r.PostFormValue(fmt.Sprintf("link%d", l.N)))
		l.Time = strings.TrimSpace(r.PostFormValue(fmt.Sprintf("time%d", l.N)))
		switch {
		case l.Link == "" && l.Time != "":
			l.Err = "A time needs a link."
		case l.Link == "":
		default:
			vid, err := issues.ParseLink(l.Link, l.Time)
			if err != nil {
				l.Err = upperFirst(err.Error()) + "."
			} else {
				videos = append(videos, vid.Stored())
			}
		}
		if l.Err != "" {
			form.Errors[fmt.Sprintf("link%d", l.N)] = l.Err
		}
	}
	return form, stance, note, videos
}

func (s *Site) issueClaimDelete(w http.ResponseWriter, r *http.Request) {
	sess, v, ok := s.issueAction(w, r, nil)
	if !ok {
		return
	}
	id := v.Tracker.Issue.ID
	err := s.cfg.Store.DeleteClaim(id, sess.userID, s.cfg.Now())
	if err != nil && !errors.Is(err, store.ErrNoClaim) {
		s.claimFailed(w, r, sess, v, err)
		return
	}
	noteFor(w, "account %d: claim on %s deleted", sess.userID, id)
	backTo(w, r, id, v.Tracker.Filter, "deleted")
}

// claimFailed answers a refusal of the store with the issue's page: a closed or withdrawn issue (read-only; closed
// between the page and the store's check), or a failure.
func (s *Site) claimFailed(w http.ResponseWriter, r *http.Request, sess *playerSession, v *view, err error) {
	id := v.Tracker.Issue.ID
	noteFor(w, "account %d: claim on %s refused: %v", sess.userID, id, err)
	status := http.StatusInternalServerError
	v.Error = "The server could not do this just now. Please try again later."
	if errors.Is(err, store.ErrIssueClosed) || errors.Is(err, store.ErrIssueWithdrawn) || errors.Is(err, store.ErrNoIssue) {
		status, v.Error = http.StatusConflict, "This issue is closed: its claims can't be changed any more."
	}
	s.render(w, r, status, "issues_issue", v)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
