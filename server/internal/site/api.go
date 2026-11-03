package site

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/issues"
	"dawngate/server/internal/limits"
	"dawngate/server/internal/store"
)

// The issue tracker's API (Config.Issues): JSON for a reviewer (the AI that judges the claims, or a script), which
// reads the issues with their tallies and claims and gives verdicts that close them. Every request carries
// "Authorization: Bearer <token>" (a token made on /admin/tokens, looked up by its SHA-256: store.APITokenByHash); the
// API reads no cookie, so a browser's session can't be used against it and it needs no CSRF token. A missing,
// malformed, unknown or revoked token gets the same 401 (no hint whether a token existed); each presented token that
// isn't valid is a failed login of its address (limits.API(), always on: 5 free, then a lockout), and each valid token
// has a request rate (DefaultAPIRate). Bodies are JSON (Content-Type application/json) within the site's 8 KB.
//
// The API shows what the players' pages show (the claims' display names, notes, links and dates), plus each claim's
// id and hidden flag; never a login name, an account id or an e-mail address. A verdict is audited as
// "api:<token name>".
//
// Routes: GET /api/issues?state=&since=&withdrawn=, GET /api/issues/{id}, POST /api/issues/{id}/verdict.

// DefaultAPIRate is each token's requests: 60 at once, then one a second. A review reads the list once (every
// issue with its claims) and gives a handful of verdicts.
var DefaultAPIRate = Rate{PerMinute: 60, Burst: 60}

// maxVerdictClaims bounds the claims a verdict may name.
const maxVerdictClaims = 50

type apiState struct {
	limits Limiter
	rate   *buckets // per token id
}

func newAPIState(cfg Config) *apiState {
	a := &apiState{limits: cfg.APILimits}
	if a.limits == nil {
		a.limits = limits.New(limits.API(), cfg.Now)
	}
	rate := cfg.APIRate
	if rate.PerMinute <= 0 {
		rate = DefaultAPIRate
	}
	a.rate = newBuckets(rate)
	return a
}

func (s *Site) apiRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/issues", s.apiHandler(s.apiList))
	m.HandleFunc("GET /api/issues/{id}", s.apiHandler(s.apiIssue))
	m.HandleFunc("POST /api/issues/{id}/verdict", s.apiHandler(s.apiVerdict))
	m.HandleFunc("/api/", s.apiHandler(func(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
		apiError(w, http.StatusNotFound, "no such API request: GET /api/issues, GET /api/issues/{id}, "+
			"POST /api/issues/{id}/verdict")
	}))
}

// ---- the JSON ----

// APIIssue is one issue in the API's answers.
type APIIssue struct {
	ID      string `json:"id"`
	Area    string `json:"area"`
	Title   string `json:"title"`
	Assumed string `json:"assumed"`
	LookFor string `json:"lookFor"`
	Top     bool   `json:"top"`
	Order   int    `json:"order"`
	Listed  bool   `json:"listed"` // still on the server's list (false: withdrawn, read-only)
	// State is the store's: open, confirmed, disproven, closed; Status the page's (issues.Status: computed while open)
	State       string      `json:"state"`
	Status      string      `json:"status"`
	StatusLabel string      `json:"statusLabel"`
	Verdict     *APIVerdict `json:"verdict,omitempty"`
	Updated     string      `json:"updated"` // RFC 3339; the list's since= takes it
	Tally       APITally    `json:"tally"`
	Claims      []APIClaim  `json:"claims"` // best evidence first (issues.BestFirst), hidden ones last
}

// APIVerdict is a closed issue's verdict.
type APIVerdict struct {
	Note string `json:"note"`
	At   string `json:"at"`
	By   string `json:"by"` // "api:<token name>", or "admin" (an admin's name isn't shown)
}

// APITally sums up the visible claims (issues.TallyOf).
type APITally struct {
	CorrectWeight int `json:"correctWeight"`
	WrongWeight   int `json:"wrongWeight"`
	CorrectClaims int `json:"correctClaims"`
	WrongClaims   int `json:"wrongClaims"`
	// CorrectTimed and WrongTimed count the claims with a timestamped video (weight 3)
	CorrectTimed int `json:"correctTimed"`
	WrongTimed   int `json:"wrongTimed"`
	Videos       int `json:"videos"`
	Hidden       int `json:"hidden"`
}

// APIClaim is one claim.
type APIClaim struct {
	ID      int64      `json:"id"`
	Persona string     `json:"persona"` // the player's display name
	Stance  string     `json:"stance"`  // correct or wrong
	Note    string     `json:"note"`
	Videos  []APIVideo `json:"videos"`
	Weight  int        `json:"weight"` // 1 a note only, 2 a video, 3 a video with a timestamp
	Created string     `json:"created"`
	Updated string     `json:"updated"`
	Hidden  bool       `json:"hidden"` // hidden by an admin: not counted, not shown to other players
}

// APIVideo is one of a claim's YouTube videos: the canonical link and the start.
type APIVideo struct {
	URL       string `json:"url"`
	VideoID   string `json:"video"`
	Seconds   int    `json:"seconds"`             // -1 = no timestamp
	Timestamp string `json:"timestamp,omitempty"` // as the page shows it (1:23)
}

// APIList is GET /api/issues's answer.
type APIList struct {
	Now    string     `json:"now"` // the server's time: the next since=
	Count  int        `json:"count"`
	Issues []APIIssue `json:"issues"`
}

// APIVerdictRequest is POST /api/issues/{id}/verdict's body.
type APIVerdictRequest struct {
	Verdict string  `json:"verdict"` // confirmed, disproven, closed, or reopen
	Note    string  `json:"note"`
	Claims  []int64 `json:"claims"` // the claims the verdict rests on (the issue's own; audited)
}

// APIVerdictAnswer is the verdict's answer: what was done and the issue as it is now.
type APIVerdictAnswer struct {
	Done  string   `json:"done"`
	Issue APIIssue `json:"issue"`
}

func apiTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func apiIssueOf(d store.IssueDetail) APIIssue {
	st := issues.StatusOf(d.IssueRow)
	t := issues.TallyOf(d.IssueRow)
	a := APIIssue{ID: d.ID, Area: d.Area, Title: d.Title, Assumed: d.Assumed, LookFor: d.LookFor, Top: d.Top,
		Order: d.Order, Listed: d.InDoc, State: string(d.State), Status: string(st), StatusLabel: st.Label(),
		Updated: apiTime(d.Updated), Claims: []APIClaim{},
		Tally: APITally{CorrectWeight: t.Correct, WrongWeight: t.Wrong, CorrectClaims: d.Correct.Claims(),
			WrongClaims: d.Wrong.Claims(), CorrectTimed: d.Correct.ByEvidence[store.EvidenceTimed],
			WrongTimed: d.Wrong.ByEvidence[store.EvidenceTimed], Videos: t.Videos, Hidden: t.Hidden}}
	if d.State.Verdict() {
		by := "admin"
		if strings.HasPrefix(d.ClosedBy, "api:") {
			by = d.ClosedBy
		}
		a.Verdict = &APIVerdict{Note: d.VerdictNote, At: apiTime(d.ClosedAt), By: by}
	}
	for _, c := range issues.BestFirst(d.Claims) {
		ac := APIClaim{ID: c.ID, Persona: c.Persona, Stance: stanceID(c.Stance), Note: c.Note, Videos: []APIVideo{},
			Weight: issues.ClaimWeight(c), Created: apiTime(c.Created), Updated: apiTime(c.Updated), Hidden: c.Hidden}
		if ac.Persona == "" {
			ac.Persona = "A player"
		}
		for _, v := range issues.Videos(c) {
			ac.Videos = append(ac.Videos, APIVideo{URL: v.URL(), VideoID: v.ID, Seconds: v.Seconds, Timestamp: v.Timestamp()})
		}
		a.Claims = append(a.Claims, ac)
	}
	return a
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // JSON with nosniff: a note's <b> stays as typed
	enc.Encode(v)
}

// APIError is every refusal's body.
type APIError struct {
	Error string `json:"error"`
}

func apiError(w http.ResponseWriter, status int, text string) {
	noteFor(w, "refused: %s", text)
	writeJSON(w, status, APIError{Error: text})
}

// ---- the token ----

// apiUnauthorized is the one answer to a request without a valid token.
const apiUnauthorized = "a valid API token is needed (Authorization: Bearer <token>)"

// bearer reads the token from an Authorization header ("" = none or not a Bearer token of the right form).
func bearer(h string) (token string, presented bool) {
	if h == "" {
		return "", false
	}
	scheme, tok, ok := strings.Cut(strings.TrimSpace(h), " ")
	tok = strings.TrimSpace(tok)
	if !ok || !strings.EqualFold(scheme, "Bearer") || len(tok) != 43 {
		return "", true
	}
	for i := 0; i < len(tok); i++ {
		if c := tok[i]; !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", true
		}
	}
	return tok, true
}

// apiHandler checks the request's token before the API's handler runs: the address's lockout first (failed tokens),
// then the token (by its SHA-256; unknown or revoked: 401, a failure of the address), then the token's rate.
func (s *Site) apiHandler(h func(w http.ResponseWriter, r *http.Request, tok store.APIToken)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := clientAddr(r)
		if wait, why := s.api.limits.Login(addr, ""); wait > 0 {
			noteFor(w, "refused: api limits: %s", why)
			w.Header().Set("Retry-After", fmt.Sprint(int(math.Ceil(wait.Seconds()))))
			writeJSON(w, http.StatusTooManyRequests, APIError{Error: "too many requests with a token that isn't valid " +
				"from your address: wait and try again"})
			return
		}
		raw, presented := bearer(r.Header.Get("Authorization"))
		var tok store.APIToken
		found := false
		if raw != "" {
			var err error
			tok, found, err = s.cfg.Store.APITokenByHash(issues.TokenHash(raw))
			if err != nil {
				noteFor(w, "the store: %v", err)
				writeJSON(w, http.StatusInternalServerError, APIError{Error: "the server could not check the token just now"})
				return
			}
		}
		if !found || tok.Revoked {
			why := "no token"
			switch {
			case found:
				why = fmt.Sprintf("revoked token %d", tok.ID)
			case raw != "":
				why = "unknown token"
			case presented:
				why = "malformed Authorization header"
			}
			if presented {
				s.api.limits.LoginDone(addr, "", false)
			}
			noteFor(w, "refused: api: %s", why)
			w.Header().Set("WWW-Authenticate", `Bearer realm="issues"`)
			writeJSON(w, http.StatusUnauthorized, APIError{Error: apiUnauthorized})
			return
		}
		s.api.limits.LoginDone(addr, "", true)
		now := s.cfg.Now()
		if wait := s.api.rate.take(strconv.FormatInt(tok.ID, 10), now); wait > 0 {
			noteFor(w, "refused: api:%s over its rate", tok.Name)
			w.Header().Set("Retry-After", fmt.Sprint(int(math.Ceil(wait.Seconds()))))
			writeJSON(w, http.StatusTooManyRequests, APIError{Error: "too many requests with this token: wait and try again"})
			return
		}
		if err := s.cfg.Store.TouchAPIToken(tok.ID, now); err != nil && s.cfg.Log != nil {
			s.cfg.Log("%s %s: the API token's last use: %v", addr, r.URL.Path, err)
		}
		h(w, r, tok)
	}
}

// ---- the requests ----

func (s *Site) apiList(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	q := r.URL.Query()
	f := store.IssueFilter{State: store.IssueState(q.Get("state"))}
	switch f.State {
	case "", store.IssueOpen, store.IssueConfirmed, store.IssueDisproven, store.IssueClosed:
	default:
		apiError(w, http.StatusBadRequest, "state: open, confirmed, disproven or closed (or none: every state)")
		return
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			apiError(w, http.StatusBadRequest, "since: a time in RFC 3339 (2026-11-03T12:00:00Z)")
			return
		}
		f.Since = t
	}
	switch q.Get("withdrawn") {
	case "", "0", "false":
	case "1", "true":
		f.Withdrawn = true
	default:
		apiError(w, http.StatusBadRequest, "withdrawn: 1 or 0")
		return
	}
	now := s.cfg.Now()
	rows, err := s.cfg.Store.ListIssues(f)
	if err != nil {
		s.apiStoreFailed(w, err)
		return
	}
	out := APIList{Now: apiTime(now), Issues: []APIIssue{}}
	for _, row := range rows {
		d, found, err := s.cfg.Store.IssueDetail(row.ID, 0)
		if err != nil {
			s.apiStoreFailed(w, err)
			return
		}
		if found {
			out.Issues = append(out.Issues, apiIssueOf(d))
		}
	}
	out.Count = len(out.Issues)
	noteFor(w, "api:%s: %d issue(s)", tok.Name, out.Count)
	writeJSON(w, http.StatusOK, out)
}

func (s *Site) apiStoreFailed(w http.ResponseWriter, err error) {
	noteFor(w, "the store: %v", err)
	writeJSON(w, http.StatusInternalServerError, APIError{Error: "the server could not read the issues just now"})
}

func (s *Site) apiIssue(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	d, found, err := s.cfg.Store.IssueDetail(r.PathValue("id"), 0)
	if err != nil {
		s.apiStoreFailed(w, err)
		return
	}
	if !found {
		apiError(w, http.StatusNotFound, "no such issue")
		return
	}
	noteFor(w, "api:%s: issue %s", tok.Name, d.ID)
	writeJSON(w, http.StatusOK, apiIssueOf(d))
}

func (s *Site) apiVerdict(w http.ResponseWriter, r *http.Request, tok store.APIToken) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		apiError(w, http.StatusUnsupportedMediaType, "the body must be JSON (Content-Type: application/json)")
		return
	}
	var req APIVerdictRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&req)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = errors.New("more than one JSON value")
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			apiError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the body is larger than %d bytes", tooBig.Limit))
			return
		}
		apiError(w, http.StatusBadRequest, `the body: {"verdict": "confirmed|disproven|closed|reopen", "note": "...", `+
			`"claims": [ids]}: `+err.Error())
		return
	}
	who := accounts.Actor{API: tok.Name, Source: "api " + clientAddr(r)}
	done, err := s.giveVerdict(w, r, who, r.PathValue("id"), req.Verdict, req.Note, req.Claims)
	if err != nil {
		var ve *verdictError
		if errors.As(err, &ve) {
			apiError(w, ve.status, ve.text)
			return
		}
		s.apiStoreFailed(w, err)
		return
	}
	d, _, err := s.cfg.Store.IssueDetail(r.PathValue("id"), 0)
	if err != nil {
		s.apiStoreFailed(w, err)
		return
	}
	writeJSON(w, http.StatusOK, APIVerdictAnswer{Done: done, Issue: apiIssueOf(d)})
}

// ---- verdicts (the API's and the admin page's) ----

// verdictError is a refused verdict: its status and its text (shown on the admin page, the API's error).
type verdictError struct {
	status int
	text   string
}

func (e *verdictError) Error() string { return e.text }

// verdictStates are the verdicts by their names.
var verdictStates = map[string]store.IssueState{
	"confirmed": store.IssueConfirmed,
	"disproven": store.IssueDisproven,
	"closed":    store.IssueClosed,
}

// giveVerdict closes an open issue with a verdict (confirmed, disproven, closed: a note is needed) or reopens one with
// a verdict ("reopen": the note is optional), names the claims it rests on (the issue's own), and writes the audit
// log entry. It returns the success line; a refusal is a *verdictError.
func (s *Site) giveVerdict(w http.ResponseWriter, r *http.Request, who accounts.Actor, id, verdict, note string,
	claimIDs []int64) (string, error) {
	state, isVerdict := verdictStates[verdict]
	if !isVerdict && verdict != "reopen" {
		return "", &verdictError{http.StatusBadRequest, "verdict: confirmed, disproven, closed or reopen"}
	}
	note, err := issues.CheckVerdictNote(note)
	if err != nil {
		return "", &verdictError{http.StatusBadRequest, upperFirst(err.Error()) + "."}
	}
	if isVerdict && note == "" {
		return "", &verdictError{http.StatusBadRequest, "A verdict needs a note: what the evidence shows (players read it " +
			"on the closed issue)."}
	}
	if len(claimIDs) > maxVerdictClaims {
		return "", &verdictError{http.StatusBadRequest, fmt.Sprintf("At most %d claims.", maxVerdictClaims)}
	}
	d, found, err := s.cfg.Store.IssueDetail(id, 0)
	if err != nil {
		return "", err
	}
	if !found {
		return "", &verdictError{http.StatusNotFound, "no such issue"}
	}
	var named []string
	seen := map[int64]bool{}
	for _, c := range claimIDs {
		if seen[c] {
			continue
		}
		seen[c] = true
		if !slices.ContainsFunc(d.Claims, func(x store.Claim) bool { return x.ID == c }) {
			return "", &verdictError{http.StatusBadRequest, fmt.Sprintf("Claim %d isn't one of issue %s's.", c, d.ID)}
		}
		named = append(named, strconv.FormatInt(c, 10))
	}
	claims := ""
	if len(named) > 0 {
		claims = "; claims " + strings.Join(named, ", ")
	}
	now := s.cfg.Now()
	target := "issue " + d.ID
	if !isVerdict {
		if err := s.cfg.Store.ReopenIssue(d.ID, now); errors.Is(err, store.ErrIssueOpen) {
			return "", &verdictError{http.StatusConflict, fmt.Sprintf("Issue %s is open already.", d.ID)}
		} else if err != nil {
			return "", err
		}
		detail := fmt.Sprintf("was %s by %s: %s", d.State, d.ClosedBy, d.VerdictNote)
		if note != "" {
			detail += "; why: " + note
		}
		s.audit(w, r, who, "issue reopen", target, detail+claims)
		return fmt.Sprintf("Issue %s is open again (it was %s); players can make claims on it.", d.ID, d.State), nil
	}
	if err := s.cfg.Store.SetVerdict(d.ID, state, note, who.String(), now); errors.Is(err, store.ErrIssueClosed) {
		return "", &verdictError{http.StatusConflict, fmt.Sprintf("Issue %s has a verdict already (%s): reopen it first.",
			d.ID, d.State)}
	} else if err != nil {
		return "", err
	}
	s.audit(w, r, who, "issue verdict", target, fmt.Sprintf("%s: %s%s", state, note, claims))
	return fmt.Sprintf("Issue %s is %s now and read-only; players see the note on its page.", d.ID, state), nil
}
