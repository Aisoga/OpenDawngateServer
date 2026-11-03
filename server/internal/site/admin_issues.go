package site

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"dawngate/server/internal/issues"
	"dawngate/server/internal/store"
)

// The admin page's part of the issue tracker (Config.Issues with Config.Admin): the issues with filters, one issue
// with every claim (hidden ones too, with the account behind each), hiding and showing a claim, a verdict with its
// note (confirmed, disproven, closed) and reopening; and the API's tokens (make one, shown once; revoke). Every action
// passes adminAction (the session, CSRF, the form rate) and writes the audit log.
//
// Routes: GET /admin/issues?filter=, GET /admin/issues/{id}, POST /admin/issues/verdict, POST /admin/issues/reopen,
// POST /admin/issues/claim, GET /admin/tokens, POST /admin/tokens, POST /admin/tokens/revoke.

func (s *Site) adminIssuesRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /admin/issues", s.adminPage(s.adminIssues))
	m.HandleFunc("GET /admin/issues/{id}", s.adminPage(s.adminIssue))
	m.HandleFunc("POST /admin/issues/verdict", s.adminAction(s.adminVerdict))
	m.HandleFunc("POST /admin/issues/reopen", s.adminAction(s.adminReopen))
	m.HandleFunc("POST /admin/issues/claim", s.adminAction(s.adminHideClaim))
	m.HandleFunc("GET /admin/tokens", s.adminPage(s.adminTokens))
	m.HandleFunc("POST /admin/tokens", s.adminAction(s.adminMakeToken))
	m.HandleFunc("POST /admin/tokens/revoke", s.adminAction(s.adminRevokeToken))
}

// adminIssueFilter is one of the admin list's filters.
type adminIssueFilter struct {
	ID, Label string
	match     func(store.IssueRow) bool
}

func listedWith(f func(issues.Status) bool) func(store.IssueRow) bool {
	return func(r store.IssueRow) bool { return r.InDoc && f(issues.StatusOf(r)) }
}

// adminIssueFilters are the admin list's filters; the first is the default.
var adminIssueFilters = []adminIssueFilter{
	{"open", "Open", listedWith(issues.Status.Open)},
	{"leaning", "Leaning", listedWith(func(st issues.Status) bool { return st == issues.LeaningCorrect || st == issues.LeaningWrong })},
	{"disputed", "Disputed", listedWith(func(st issues.Status) bool { return st == issues.Disputed })},
	{"needs-video", "Needs video proof", listedWith(func(st issues.Status) bool { return st == issues.NeedsVideo })},
	{"needs-verification", "Needs verification", listedWith(func(st issues.Status) bool { return st == issues.NeedsVerification })},
	{"closed", "With a verdict", func(r store.IssueRow) bool { return r.State.Verdict() }},
	{"hidden", "With hidden claims", func(r store.IssueRow) bool { return r.Hidden > 0 }},
	{"withdrawn", "No longer listed", func(r store.IssueRow) bool { return !r.InDoc }},
	{"all", "All", func(store.IssueRow) bool { return true }},
}

func adminFilterOf(id string) adminIssueFilter {
	for _, f := range adminIssueFilters {
		if f.ID == id {
			return f
		}
	}
	return adminIssueFilters[0]
}

// adminIssueRow is one row of the admin list.
type adminIssueRow struct {
	ID, Title, Area string
	Status          issues.Status
	Listed          bool
	Tally           issues.Tally
	CorrectClaims   int
	WrongClaims     int
	Updated         string
}

// adminIssueView is the admin's issue page.
type adminIssueView struct {
	adminIssueRow
	Assumed, LookFor string
	Open             bool // no verdict: the verdict form; else the reopen form
	State            string
	VerdictNote      string
	ClosedAt         string
	ClosedBy         string
	Claims           []adminClaimView
	// Verdict and Note are the verdict form as sent (after a refusal)
	Verdict, Note string
}

type adminClaimView struct {
	ID          int64
	UserID      int64
	Persona     string
	StanceLabel string
	Note        string
	Videos      []videoView
	Weight      int
	Created     string
	Edited      string
	Hidden      bool
}

type tokenRow struct {
	store.APIToken
	CreatedAt, Used string
}

func adminRowOf(r store.IssueRow) adminIssueRow {
	return adminIssueRow{ID: r.ID, Title: r.Title, Area: r.Area, Status: issues.StatusOf(r), Listed: r.InDoc,
		Tally: issues.TallyOf(r), CorrectClaims: r.Correct.Claims(), WrongClaims: r.Wrong.Claims(), Updated: day(r.Updated)}
}

// ---- the list ----

func (s *Site) adminIssues(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	v.Title = "Issues"
	rows, err := s.cfg.Store.ListIssues(store.IssueFilter{Withdrawn: true})
	status := http.StatusOK
	if err != nil {
		v.Error, status = "The store failed: "+err.Error(), http.StatusInternalServerError
	}
	f := adminFilterOf(r.URL.Query().Get("filter"))
	v.Admin.Filter = f.ID
	for _, x := range adminIssueFilters {
		n := 0
		for _, row := range rows {
			if x.match(row) {
				n++
			}
		}
		v.Admin.IssueFilters = append(v.Admin.IssueFilters, filterLink{ID: x.ID, Label: x.Label, Count: n, On: x.ID == f.ID})
	}
	for _, row := range displayOrder(rows) {
		if f.match(row) {
			v.Admin.IssueRows = append(v.Admin.IssueRows, adminRowOf(row))
		}
	}
	s.adminRender(w, r, status, "admin_issues", v)
}

// ---- one issue ----

func (s *Site) adminIssue(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	s.showIssue(w, r, v, http.StatusOK, r.PathValue("id"))
}

// showIssue renders the admin's issue page (an unknown id: the list with an error).
func (s *Site) showIssue(w http.ResponseWriter, r *http.Request, v *view, status int, id string) {
	d, found, err := s.cfg.Store.IssueDetail(id, 0)
	if err != nil || !found {
		v.Title = "Issues"
		v.Error, status = "There is no such issue.", http.StatusNotFound
		if err != nil {
			v.Error, status = "The store failed: "+err.Error(), http.StatusInternalServerError
		}
		s.adminRender(w, r, status, "admin_issues", v)
		return
	}
	v.Title = d.ID + ": " + d.Title
	iv := &adminIssueView{adminIssueRow: adminRowOf(d.IssueRow), Assumed: d.Assumed, LookFor: d.LookFor,
		Open: d.State == store.IssueOpen, State: string(d.State), VerdictNote: d.VerdictNote, ClosedBy: d.ClosedBy}
	if !d.ClosedAt.IsZero() {
		iv.ClosedAt = day(d.ClosedAt) + " UTC"
	}
	if old := v.Admin.Issue; old != nil { // the verdict form as sent
		iv.Verdict, iv.Note = old.Verdict, old.Note
	}
	for _, c := range issues.BestFirst(d.Claims) {
		cv := adminClaimView{ID: c.ID, UserID: c.UserID, Persona: c.Persona, StanceLabel: stanceLabel(c.Stance),
			Note: c.Note, Weight: issues.ClaimWeight(c), Created: day(c.Created), Hidden: c.Hidden}
		if !c.Updated.Equal(c.Created) {
			cv.Edited = day(c.Updated)
		}
		for _, vid := range issues.Videos(c) {
			vv := videoView{URL: vid.URL(), Label: "Watch", Short: "youtube.com/watch?v=" + vid.ID}
			if ts := vid.Timestamp(); ts != "" {
				vv.Label = "Watch at " + ts
			}
			cv.Videos = append(cv.Videos, vv)
		}
		iv.Claims = append(iv.Claims, cv)
	}
	v.Admin.Issue = iv
	s.adminRender(w, r, status, "admin_issue", v)
}

func (s *Site) adminVerdict(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	id := strings.TrimSpace(r.PostFormValue("issue"))
	verdict, note := r.PostFormValue("verdict"), r.PostFormValue("note")
	if verdict == "reopen" { // only the reopen form reopens
		verdict = ""
	}
	s.verdictAction(w, r, sess, v, id, verdict, note)
}

func (s *Site) adminReopen(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	s.verdictAction(w, r, sess, v, strings.TrimSpace(r.PostFormValue("issue")), "reopen", r.PostFormValue("note"))
}

func (s *Site) verdictAction(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view, id, verdict, note string) {
	done, err := s.giveVerdict(w, r, actor(sess, r), id, verdict, note, nil)
	if err != nil {
		status, text := http.StatusInternalServerError, "Not done: the store failed: "+err.Error()
		var ve *verdictError
		if errors.As(err, &ve) {
			status, text = ve.status, ve.text
		}
		noteFor(w, "refused: %s", text)
		v.Error = text
		if verdict != "reopen" {
			v.Admin.Issue = &adminIssueView{Verdict: verdict, Note: note}
		}
		s.showIssue(w, r, v, status, id)
		return
	}
	v.Done = done
	s.showIssue(w, r, v, http.StatusOK, id)
}

func (s *Site) adminHideClaim(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	id := strings.TrimSpace(r.PostFormValue("issue"))
	claim, ok := formID(r, "claim")
	hide := r.PostFormValue("hidden")
	if !ok || (hide != "1" && hide != "0") {
		v.Error = "Choose a claim to hide or show."
		noteFor(w, "refused: %s", v.Error)
		s.showIssue(w, r, v, http.StatusBadRequest, id)
		return
	}
	c, err := s.cfg.Store.SetClaimHidden(claim, hide == "1", s.cfg.Now())
	if err != nil {
		v.Error = "Not changed: " + err.Error() + "."
		noteFor(w, "refused: %s", v.Error)
		s.showIssue(w, r, v, http.StatusBadRequest, id)
		return
	}
	action, what := "claim show", "shown again: it counts again"
	if c.Hidden {
		action, what = "claim hide", "hidden: it no longer counts, and only its player sees it"
	}
	s.audit(w, r, actor(sess, r), action, fmt.Sprintf("claim %d on %s", c.ID, c.IssueID),
		fmt.Sprintf("account %d (%s), %s", c.UserID, stanceID(c.Stance), plural(len(c.Videos), "video")))
	v.Done = fmt.Sprintf("Claim %d is %s.", c.ID, what)
	s.showIssue(w, r, v, http.StatusOK, c.IssueID)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// ---- the API's tokens ----

func (s *Site) adminTokens(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	s.showTokens(w, r, v, http.StatusOK)
}

func (s *Site) showTokens(w http.ResponseWriter, r *http.Request, v *view, status int) {
	v.Title = "API tokens"
	all, err := s.cfg.Store.APITokens()
	if err != nil {
		v.Error, status = "The store failed: "+err.Error(), http.StatusInternalServerError
	}
	for _, t := range all {
		v.Admin.Tokens = append(v.Admin.Tokens, tokenRow{APIToken: t, CreatedAt: day(t.Created), Used: day(t.LastUsed)})
	}
	s.adminRender(w, r, status, "admin_tokens", v)
}

// maxTokenName bounds a token's name (it names the reviewer in the audit log: "api:<name>").
const maxTokenName = 40

func (s *Site) adminMakeToken(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	show := func(status int) { s.showTokens(w, r, v, status) }
	name := cleanText(r.PostFormValue("name"), maxTokenName+1)
	if name == "" || len([]rune(name)) > maxTokenName {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, fmt.Sprintf("Give the token a name of 1 to %d "+
			"characters (whom it is for: it names them in the audit log).", maxTokenName))
		return
	}
	all, err := s.cfg.Store.APITokens()
	if err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusInternalServerError, "Not made: "+err.Error()+".")
		return
	}
	for _, t := range all {
		if strings.EqualFold(t.Name, name) {
			s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, fmt.Sprintf("Token %d already has the name %q "+
				"(also when revoked: the audit log names tokens by name). Choose another.", t.ID, t.Name))
			return
		}
	}
	token, hash, err := issues.NewToken()
	if err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusInternalServerError, "Not made: "+err.Error()+".")
		return
	}
	t, err := s.cfg.Store.CreateAPIToken(name, hash, actor(sess, r).String(), s.cfg.Now())
	if err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusInternalServerError, "Not made: "+err.Error()+".")
		return
	}
	s.audit(w, r, actor(sess, r), "api token create", fmt.Sprintf("token %d %q", t.ID, t.Name), "")
	v.Admin.Secret, v.Admin.SecretWhat = token, fmt.Sprintf("API token %d %q", t.ID, t.Name)
	v.Done = "Hand the token to its reviewer; it is shown only now (the server keeps only its SHA-256). It is sent as " +
		"Authorization: Bearer <token>."
	show(http.StatusOK)
}

func (s *Site) adminRevokeToken(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	show := func(status int) { s.showTokens(w, r, v, status) }
	id, ok := formID(r, "id")
	if !ok {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Choose a token.")
		return
	}
	name := ""
	if all, err := s.cfg.Store.APITokens(); err == nil {
		for _, t := range all {
			if t.ID == id {
				name = t.Name
			}
		}
	}
	if err := s.cfg.Store.RevokeAPIToken(id); err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Not revoked: "+err.Error()+".")
		return
	}
	s.audit(w, r, actor(sess, r), "api token revoke", fmt.Sprintf("token %d %q", id, name), "")
	v.Done = fmt.Sprintf("Token %d %q is revoked: its next request is refused.", id, name)
	show(http.StatusOK)
}
