package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// This file holds the issue tracker's data (schema version 8): the tracked guesses about the original game (issues),
// the players' claims on them with their YouTube videos, the verdicts that close them, and the API's tokens. The
// rules (which links count, what a claim weighs, an issue's computed status, the list the server carries) are
// internal/issues'; the pages and the API are internal/site's. The list of issues is bounded (one row per tracked
// entry, about 130), so it is read whole, without paging.

// IssueState is an issue's stored state: open, or the verdict that closed it.
type IssueState string

// The states. Every state but IssueOpen is a verdict: the issue is read-only then, until an admin reopens it.
const (
	IssueOpen      IssueState = "open"
	IssueConfirmed IssueState = "confirmed" // the guess matches the original
	IssueDisproven IssueState = "disproven" // the guess is wrong; a fix is planned
	IssueClosed    IssueState = "closed"    // can't be verified, or withdrawn
)

// Verdict reports whether the state is a verdict (confirmed, disproven or closed).
func (st IssueState) Verdict() bool {
	return st == IssueConfirmed || st == IssueDisproven || st == IssueClosed
}

// Stance is what a claim says about the guess.
type Stance int

// The stances (issue_claims.stance). 0 is no claim (IssueRow.Own of a player without one).
const (
	StanceCorrect Stance = 1 // looks correct
	StanceWrong   Stance = 2 // looks wrong
)

func (s Stance) String() string {
	switch s {
	case StanceCorrect:
		return "correct"
	case StanceWrong:
		return "wrong"
	case 0:
		return "none"
	}
	return fmt.Sprintf("stance%d", int(s))
}

// MarshalText writes the stance as "correct" or "wrong" (the account's data download, the API).
func (s Stance) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText reads "correct" or "wrong".
func (s *Stance) UnmarshalText(b []byte) error {
	switch string(b) {
	case "correct":
		*s = StanceCorrect
	case "wrong":
		*s = StanceWrong
	default:
		return fmt.Errorf("store: unknown stance %q", b)
	}
	return nil
}

// Limits the store enforces whatever the caller checked before.
const (
	MaxClaimVideos  = 3    // videos per claim
	MaxClaimNote    = 1000 // characters of a claim's note
	MaxVerdictNote  = 2000 // characters of a verdict's note
	MaxVideoSeconds = 100 * 3600
)

// Errors of the tracker's actions.
var (
	ErrNoIssue        = errors.New("store: no such issue")
	ErrIssueClosed    = errors.New("store: the issue is closed (a verdict was given)")
	ErrIssueOpen      = errors.New("store: the issue is open")
	ErrIssueWithdrawn = errors.New("store: the issue is no longer listed")
	ErrNoClaim        = errors.New("store: no such claim")
	ErrNoToken        = errors.New("store: no such API token")
)

// IssueText is an issue's player-facing text as the server's list carries it (issues.Entry): no private field of the
// notes ever comes here.
type IssueText struct {
	ID      string
	Area    string
	Title   string
	Assumed string // what we assumed (one sentence)
	LookFor string // what to look for in a video of the original
	Top     bool   // one of the most important
	Order   int    // the list's order
}

// Issue is one issue as stored.
type Issue struct {
	IssueText
	InDoc       bool // still in the server's list (SyncIssues sets false for an id no longer listed)
	State       IssueState
	VerdictNote string
	ClosedAt    time.Time // zero while open
	ClosedBy    string    // who gave the verdict ("admin <name> (account <id>)", "api:<token name>")
	Updated     time.Time // the last change of the issue, its claims or its verdict (the API's since=)
}

// Evidence is what a claim brings: a note only, a video, or a video with a timestamp.
type Evidence int

// The kinds of evidence, weakest first.
const (
	EvidenceNote Evidence = iota
	EvidenceVideo
	EvidenceTimed
)

// evidenceOf is the evidence of a claim with this many videos, timed of them with a timestamp.
func evidenceOf(videos, timed int) Evidence {
	switch {
	case timed > 0:
		return EvidenceTimed
	case videos > 0:
		return EvidenceVideo
	}
	return EvidenceNote
}

// ClaimVideo is one YouTube video of a claim: only its id and where to start (issues.ParseLink makes one).
type ClaimVideo struct {
	VideoID string `json:"video"`
	Seconds int    `json:"seconds"` // -1 = no timestamp
}

// Claim is one player's claim on an issue.
type Claim struct {
	ID         int64        `json:"id"`
	IssueID    string       `json:"issue"`
	IssueTitle string       `json:"issueTitle,omitempty"` // only ClaimsOf fills it
	UserID     int64        `json:"-"`
	Persona    string       `json:"persona,omitempty"` // the account's display name (its first persona)
	Stance     Stance       `json:"stance"`
	Note       string       `json:"note,omitempty"`
	Videos     []ClaimVideo `json:"videos,omitempty"`
	Created    time.Time    `json:"created"`
	Updated    time.Time    `json:"updated"`
	Hidden     bool         `json:"hidden,omitempty"` // hidden by an admin: not shown, not counted
}

// Evidence is what the claim brings.
func (c Claim) Evidence() Evidence {
	timed := 0
	for _, v := range c.Videos {
		if v.Seconds >= 0 {
			timed++
		}
	}
	return evidenceOf(len(c.Videos), timed)
}

// StanceCount counts an issue's visible claims of one stance.
type StanceCount struct {
	ByEvidence [3]int // claims by Evidence
	Videos     int    // their video links
}

// Claims is the number of claims.
func (c StanceCount) Claims() int { return c.ByEvidence[0] + c.ByEvidence[1] + c.ByEvidence[2] }

// IssueRow is an issue with its claims counted (hidden claims apart).
type IssueRow struct {
	Issue
	Correct StanceCount // visible claims "looks correct"
	Wrong   StanceCount // visible claims "looks wrong"
	Hidden  int         // claims hidden by an admin
	Own     Stance      // the viewer's own claim's stance (0 = none; also when it is hidden)
}

// count adds one claim to the row.
func (r *IssueRow) count(userID, viewer int64, stance Stance, hidden bool, videos, timed int) {
	if viewer != 0 && userID == viewer {
		r.Own = stance
	}
	if hidden {
		r.Hidden++
		return
	}
	c := &r.Correct
	if stance == StanceWrong {
		c = &r.Wrong
	}
	c.ByEvidence[evidenceOf(videos, timed)]++
	c.Videos += videos
}

// IssueDetail is one issue with its claims (hidden ones too, marked) and their videos, oldest claim first.
type IssueDetail struct {
	IssueRow
	Claims []Claim
}

// ClaimOf returns this account's claim among the issue's.
func (d IssueDetail) ClaimOf(userID int64) (Claim, bool) {
	for _, c := range d.Claims {
		if c.UserID == userID {
			return c, true
		}
	}
	return Claim{}, false
}

// IssueFilter selects the issues ListIssues returns.
type IssueFilter struct {
	State     IssueState // "" = any
	Since     time.Time  // zero = any; else only issues updated at or after it
	Withdrawn bool       // also the issues no longer in the list
	Viewer    int64      // the account whose own stance each row carries (0 = none)
}

// SyncResult says what SyncIssues changed.
type SyncResult struct {
	Added, Changed, Unchanged, Withdrawn int
}

func (r SyncResult) String() string {
	return fmt.Sprintf("%d added, %d changed, %d unchanged, %d withdrawn", r.Added, r.Changed, r.Unchanged, r.Withdrawn)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeFormat)
}

func parseOptTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := parseTime(s)
	return t
}

// upsertIssue inserts the issue or updates its text (and lists it again); state, verdict and claims stay.
func upsertIssue(q querier, t IssueText, now time.Time) (added, changed bool, err error) {
	if t.ID == "" {
		return false, false, errors.New("store: an issue without an id")
	}
	var old IssueText
	var top, inDoc int
	err = q.QueryRow(`SELECT area, title, assumed, look_for, top, ord, in_doc FROM issues WHERE id = ?`, t.ID).
		Scan(&old.Area, &old.Title, &old.Assumed, &old.LookFor, &top, &old.Order, &inDoc)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = q.Exec(`INSERT INTO issues (id, area, title, assumed, look_for, top, ord, in_doc, state, updated)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`, t.ID, t.Area, t.Title, t.Assumed, t.LookFor, boolInt(t.Top), t.Order,
			string(IssueOpen), formatTime(now))
		return err == nil, false, err
	case err != nil:
		return false, false, err
	}
	old.ID, old.Top = t.ID, top != 0
	if old == t && inDoc == 1 {
		return false, false, nil
	}
	_, err = q.Exec(`UPDATE issues SET area = ?, title = ?, assumed = ?, look_for = ?, top = ?, ord = ?, in_doc = 1,
		updated = ? WHERE id = ?`, t.Area, t.Title, t.Assumed, t.LookFor, boolInt(t.Top), t.Order, formatTime(now), t.ID)
	return false, err == nil, err
}

// UpsertIssue inserts the issue (open) or updates its text; it never changes the state, the verdict or the claims.
// changed is false when the stored issue already had this text and was listed.
func (s *Store) UpsertIssue(t IssueText, now time.Time) (changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	added, ch, err := upsertIssue(s.db, t, now)
	return added || ch, err
}

// SyncIssues makes the stored issues the server's list, in one transaction: each listed issue is inserted (open) or
// its text updated (UpsertIssue), and an issue no longer listed gets InDoc false (its claims and verdict stay; listed
// again, it comes back as it was). Ids must be unique and non-empty.
func (s *Store) SyncIssues(list []IssueText, now time.Time) (SyncResult, error) {
	var res SyncResult
	seen := make(map[string]bool, len(list))
	for _, t := range list {
		if t.ID == "" {
			return res, errors.New("store: an issue without an id")
		}
		if seen[t.ID] {
			return res, fmt.Errorf("store: issue %q listed twice", t.ID)
		}
		seen[t.ID] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	for _, t := range list {
		added, changed, err := upsertIssue(tx, t, now)
		if err != nil {
			return SyncResult{}, fmt.Errorf("issue %q: %v", t.ID, err)
		}
		switch {
		case added:
			res.Added++
		case changed:
			res.Changed++
		default:
			res.Unchanged++
		}
	}
	var gone []string
	if err := each(tx, func(r *sql.Rows) error {
		var id string
		if err := r.Scan(&id); err != nil {
			return err
		}
		if !seen[id] {
			gone = append(gone, id)
		}
		return nil
	}, `SELECT id FROM issues WHERE in_doc = 1`); err != nil {
		return SyncResult{}, err
	}
	for _, id := range gone {
		if _, err := tx.Exec(`UPDATE issues SET in_doc = 0, updated = ? WHERE id = ?`, formatTime(now), id); err != nil {
			return SyncResult{}, err
		}
	}
	res.Withdrawn = len(gone)
	return res, tx.Commit()
}

const issueColumns = `id, area, title, assumed, look_for, top, ord, in_doc, state, verdict_note, closed_at, closed_by, updated`

func scanIssue(r interface{ Scan(...any) error }, is *Issue) error {
	var top, inDoc int
	var state, closedAt, updated string
	if err := r.Scan(&is.ID, &is.Area, &is.Title, &is.Assumed, &is.LookFor, &top, &is.Order, &inDoc, &state,
		&is.VerdictNote, &closedAt, &is.ClosedBy, &updated); err != nil {
		return err
	}
	is.Top, is.InDoc, is.State = top != 0, inDoc != 0, IssueState(state)
	is.ClosedAt, is.Updated = parseOptTime(closedAt), parseOptTime(updated)
	return nil
}

// ListIssues returns the issues the filter selects, in the list's order, with their claims counted.
func (s *Store) ListIssues(f IssueFilter) ([]IssueRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []IssueRow
	byID := map[string]int{}
	err := each(s.db, func(r *sql.Rows) error {
		var row IssueRow
		if err := scanIssue(r, &row.Issue); err != nil {
			return err
		}
		// Since in Go: RFC 3339 text with nanoseconds doesn't sort as its times do ("…:00.5Z" < "…:00Z")
		if !f.Since.IsZero() && row.Updated.Before(f.Since) {
			return nil
		}
		byID[row.ID] = len(rows)
		rows = append(rows, row)
		return nil
	}, `SELECT `+issueColumns+` FROM issues WHERE (? = '' OR state = ?) AND (? = 1 OR in_doc = 1) ORDER BY ord, id`,
		string(f.State), string(f.State), boolInt(f.Withdrawn))
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	err = each(s.db, func(r *sql.Rows) error {
		var issue string
		var user int64
		var stance Stance
		var hidden bool
		var videos, timed int
		if err := r.Scan(&issue, &user, &stance, &hidden, &videos, &timed); err != nil {
			return err
		}
		if i, ok := byID[issue]; ok {
			rows[i].count(user, f.Viewer, stance, hidden, videos, timed)
		}
		return nil
	}, `SELECT c.issue_id, c.user_id, c.stance, c.hidden, COUNT(v.claim_id), COALESCE(SUM(v.seconds >= 0), 0)
		FROM issue_claims c LEFT JOIN issue_videos v ON v.claim_id = c.id GROUP BY c.id`)
	return rows, err
}

// claimColumns reads a claim with its account's display name (c is issue_claims, i issues).
const claimColumns = `c.id, c.issue_id, i.title, c.user_id,
	COALESCE((SELECT name FROM personas p WHERE p.user_id = c.user_id ORDER BY pos LIMIT 1), ''),
	c.stance, c.note, c.created, c.updated, c.hidden`

func scanClaim(r interface{ Scan(...any) error }, c *Claim) error {
	var created, updated string
	if err := r.Scan(&c.ID, &c.IssueID, &c.IssueTitle, &c.UserID, &c.Persona, &c.Stance, &c.Note, &created, &updated,
		&c.Hidden); err != nil {
		return err
	}
	c.Created, c.Updated = parseOptTime(created), parseOptTime(updated)
	return nil
}

// claims reads the claims of the query (over claimColumns) with their videos.
func claims(q querier, query string, args ...any) ([]Claim, error) {
	var out []Claim
	byID := map[int64]int{}
	if err := each(q, func(r *sql.Rows) error {
		var c Claim
		if err := scanClaim(r, &c); err != nil {
			return err
		}
		byID[c.ID] = len(out)
		out = append(out, c)
		return nil
	}, query, args...); err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]any, 0, len(out))
	for _, c := range out {
		ids = append(ids, c.ID)
	}
	err := each(q, func(r *sql.Rows) error {
		var id int64
		var v ClaimVideo
		if err := r.Scan(&id, &v.VideoID, &v.Seconds); err != nil {
			return err
		}
		if i, ok := byID[id]; ok {
			out[i].Videos = append(out[i].Videos, v)
		}
		return nil
	}, `SELECT claim_id, video_id, seconds FROM issue_videos WHERE claim_id IN (?`+strings.Repeat(`, ?`, len(ids)-1)+
		`) ORDER BY claim_id, pos`, ids...)
	return out, err
}

// IssueDetail returns one issue with its claims and their videos (found false when there is no such issue). viewer
// is the account whose own stance the row carries (0 = none).
func (s *Store) IssueDetail(id string, viewer int64) (IssueDetail, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var d IssueDetail
	err := scanIssue(s.db.QueryRow(`SELECT `+issueColumns+` FROM issues WHERE id = ?`, id), &d.Issue)
	if errors.Is(err, sql.ErrNoRows) {
		return IssueDetail{}, false, nil
	}
	if err != nil {
		return IssueDetail{}, false, err
	}
	d.Claims, err = claims(s.db, `SELECT `+claimColumns+` FROM issue_claims c JOIN issues i ON i.id = c.issue_id
		WHERE c.issue_id = ? ORDER BY c.created, c.id`, id)
	if err != nil {
		return IssueDetail{}, false, err
	}
	for _, c := range d.Claims {
		timed := 0
		for _, v := range c.Videos {
			if v.Seconds >= 0 {
				timed++
			}
		}
		d.count(c.UserID, viewer, c.Stance, c.Hidden, len(c.Videos), timed)
	}
	return d, true, nil
}

// Claim returns one claim by its id (found false when there is none).
func (s *Store) Claim(id int64) (Claim, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := claims(s.db, `SELECT `+claimColumns+` FROM issue_claims c JOIN issues i ON i.id = c.issue_id
		WHERE c.id = ?`, id)
	if err != nil || len(cs) == 0 {
		return Claim{}, false, err
	}
	return cs[0], true, nil
}

// ClaimsOf returns the account's claims (the account's data download), by issue.
func (s *Store) ClaimsOf(userID int64) ([]Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return claims(s.db, `SELECT `+claimColumns+` FROM issue_claims c JOIN issues i ON i.id = c.issue_id
		WHERE c.user_id = ? ORDER BY i.ord, c.issue_id`, userID)
}

// validVideoID reports whether id is a YouTube video id (11 of A-Z a-z 0-9 _ -).
func validVideoID(id string) bool {
	if len(id) != 11 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func checkText(what, s string, max int) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("store: the %s isn't UTF-8", what)
	}
	if n := utf8.RuneCountInString(s); n > max {
		return fmt.Errorf("store: the %s has %d characters (at most %d)", what, n, max)
	}
	return nil
}

// openIssue checks that the issue takes claims: it exists (ErrNoIssue), has no verdict (ErrIssueClosed) and, when
// listed is set, is still listed (ErrIssueWithdrawn).
func openIssue(q querier, id string, listed bool) error {
	var state string
	var inDoc int
	err := q.QueryRow(`SELECT state, in_doc FROM issues WHERE id = ?`, id).Scan(&state, &inDoc)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoIssue
	case err != nil:
		return err
	case IssueState(state) != IssueOpen:
		return ErrIssueClosed
	case listed && inDoc == 0:
		return ErrIssueWithdrawn
	}
	return nil
}

func touchIssue(q querier, id string, now time.Time) error {
	_, err := q.Exec(`UPDATE issues SET updated = ? WHERE id = ?`, formatTime(now), id)
	return err
}

// PutClaim creates or replaces the account's claim on the issue: its stance, note and videos (the old videos go). A
// replaced claim keeps its id, its creation time and whether an admin hid it. The issue must be open and listed
// (ErrNoIssue, ErrIssueClosed, ErrIssueWithdrawn), the account must exist (ErrNoAccount); the stance, the note
// (MaxClaimNote characters) and the videos (MaxClaimVideos, valid ids, seconds -1 or 0..MaxVideoSeconds) are checked.
func (s *Store) PutClaim(issueID string, userID int64, stance Stance, note string, videos []ClaimVideo, now time.Time) (Claim, error) {
	if stance != StanceCorrect && stance != StanceWrong {
		return Claim{}, fmt.Errorf("store: unknown stance %d", int(stance))
	}
	if err := checkText("note", note, MaxClaimNote); err != nil {
		return Claim{}, err
	}
	if len(videos) > MaxClaimVideos {
		return Claim{}, fmt.Errorf("store: %d videos (at most %d)", len(videos), MaxClaimVideos)
	}
	for _, v := range videos {
		if !validVideoID(v.VideoID) {
			return Claim{}, fmt.Errorf("store: %q is no video id", v.VideoID)
		}
		if v.Seconds < -1 || v.Seconds > MaxVideoSeconds {
			return Claim{}, fmt.Errorf("store: a video start of %d s", v.Seconds)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Claim{}, err
	}
	defer tx.Rollback()
	if err := openIssue(tx, issueID, true); err != nil {
		return Claim{}, err
	}
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM accounts WHERE user_id = ?`, userID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return Claim{}, ErrNoAccount
	} else if err != nil {
		return Claim{}, err
	}
	t := formatTime(now)
	if _, err := tx.Exec(`INSERT INTO issue_claims (issue_id, user_id, stance, note, created, updated) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (issue_id, user_id) DO UPDATE SET stance = excluded.stance, note = excluded.note,
		updated = excluded.updated`, issueID, userID, int(stance), note, t, t); err != nil {
		return Claim{}, err
	}
	var id int64
	if err := tx.QueryRow(`SELECT id FROM issue_claims WHERE issue_id = ? AND user_id = ?`, issueID, userID).Scan(&id); err != nil {
		return Claim{}, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_videos WHERE claim_id = ?`, id); err != nil {
		return Claim{}, err
	}
	for i, v := range videos {
		if _, err := tx.Exec(`INSERT INTO issue_videos (claim_id, pos, video_id, seconds) VALUES (?, ?, ?, ?)`, id, i,
			v.VideoID, v.Seconds); err != nil {
			return Claim{}, err
		}
	}
	if err := touchIssue(tx, issueID, now); err != nil {
		return Claim{}, err
	}
	cs, err := claims(tx, `SELECT `+claimColumns+` FROM issue_claims c JOIN issues i ON i.id = c.issue_id WHERE c.id = ?`, id)
	if err != nil {
		return Claim{}, err
	}
	if len(cs) != 1 {
		return Claim{}, ErrNoClaim
	}
	return cs[0], tx.Commit()
}

// DeleteClaim removes the account's claim on the issue with its videos (a player's own deletion). A closed issue is
// read-only (ErrIssueClosed); ErrNoClaim when the account has none there.
func (s *Store) DeleteClaim(issueID string, userID int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := openIssue(tx, issueID, false); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM issue_videos WHERE claim_id IN
		(SELECT id FROM issue_claims WHERE issue_id = ? AND user_id = ?)`, issueID, userID); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM issue_claims WHERE issue_id = ? AND user_id = ?`, issueID, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrNoClaim
	}
	if err := touchIssue(tx, issueID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// SetClaimHidden hides a claim (an admin's moderation: not shown, not counted) or shows it again, also on a closed
// issue. It returns the claim as it is now (ErrNoClaim when there is none).
func (s *Store) SetClaimHidden(claimID int64, hidden bool, now time.Time) (Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Claim{}, err
	}
	defer tx.Rollback()
	var issue string
	if err := tx.QueryRow(`SELECT issue_id FROM issue_claims WHERE id = ?`, claimID).Scan(&issue); errors.Is(err, sql.ErrNoRows) {
		return Claim{}, ErrNoClaim
	} else if err != nil {
		return Claim{}, err
	}
	if _, err := tx.Exec(`UPDATE issue_claims SET hidden = ? WHERE id = ?`, boolInt(hidden), claimID); err != nil {
		return Claim{}, err
	}
	if err := touchIssue(tx, issue, now); err != nil {
		return Claim{}, err
	}
	cs, err := claims(tx, `SELECT `+claimColumns+` FROM issue_claims c JOIN issues i ON i.id = c.issue_id WHERE c.id = ?`, claimID)
	if err != nil {
		return Claim{}, err
	}
	if len(cs) != 1 {
		return Claim{}, ErrNoClaim
	}
	return cs[0], tx.Commit()
}

// SetVerdict closes an open issue with a verdict (IssueConfirmed, IssueDisproven or IssueClosed), its note
// (MaxVerdictNote characters), who gave it and when. An issue that already has a verdict is refused (ErrIssueClosed):
// it is reopened first, so a verdict never silently replaces another.
func (s *Store) SetVerdict(id string, state IssueState, note, by string, now time.Time) error {
	if !state.Verdict() {
		return fmt.Errorf("store: %q is no verdict", state)
	}
	if err := checkText("verdict's note", note, MaxVerdictNote); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := openIssue(tx, id, false); err != nil {
		return err
	}
	t := formatTime(now)
	if _, err := tx.Exec(`UPDATE issues SET state = ?, verdict_note = ?, closed_at = ?, closed_by = ?, updated = ?
		WHERE id = ?`, string(state), note, t, clip(by), t, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ReopenIssue opens an issue with a verdict again (an admin's step): the verdict, its note and who gave it are
// cleared (the audit log keeps them). ErrNoIssue, or ErrIssueOpen when it is open already.
func (s *Store) ReopenIssue(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var state string
	err := s.db.QueryRow(`SELECT state FROM issues WHERE id = ?`, id).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoIssue
	case err != nil:
		return err
	case IssueState(state) == IssueOpen:
		return ErrIssueOpen
	}
	_, err = s.db.Exec(`UPDATE issues SET state = ?, verdict_note = '', closed_at = '', closed_by = '', updated = ?
		WHERE id = ?`, string(IssueOpen), formatTime(now), id)
	return err
}

// deleteClaimsOf removes every claim of the account and their videos (DeleteAccount's transaction); the issues they
// were on count as changed.
func deleteClaimsOf(tx *sql.Tx, userID int64, now time.Time) error {
	if _, err := tx.Exec(`UPDATE issues SET updated = ? WHERE id IN (SELECT issue_id FROM issue_claims WHERE user_id = ?)`,
		formatTime(now), userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM issue_videos WHERE claim_id IN (SELECT id FROM issue_claims WHERE user_id = ?)`,
		userID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM issue_claims WHERE user_id = ?`, userID)
	return err
}

// ---- the API's tokens ----

// APIToken is one token of the tracker's API, without the token itself (only its SHA-256 is stored).
type APIToken struct {
	ID        int64
	Name      string
	Created   time.Time
	CreatedBy string
	LastUsed  time.Time // zero = never
	Revoked   bool
}

// validHash reports whether h is a SHA-256 in lowercase hex.
func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for i := 0; i < len(h); i++ {
		if c := h[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

const tokenColumns = `id, name, created, created_by, last_used, revoked`

func scanToken(r interface{ Scan(...any) error }, t *APIToken) error {
	var created, used string
	if err := r.Scan(&t.ID, &t.Name, &created, &t.CreatedBy, &used, &t.Revoked); err != nil {
		return err
	}
	t.Created, t.LastUsed = parseOptTime(created), parseOptTime(used)
	return nil
}

// CreateAPIToken stores a new token by its SHA-256 (lowercase hex; issues.NewToken makes both), its name and who
// made it.
func (s *Store) CreateAPIToken(name, sha256Hex, createdBy string, now time.Time) (APIToken, error) {
	if !validHash(sha256Hex) {
		return APIToken{}, errors.New("store: a token's hash must be a SHA-256 in lowercase hex")
	}
	if strings.TrimSpace(name) == "" {
		return APIToken{}, errors.New("store: a token needs a name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`INSERT INTO api_tokens (name, sha256, created, created_by) VALUES (?, ?, ?, ?)`, clip(name),
		sha256Hex, formatTime(now), clip(createdBy))
	if err != nil {
		return APIToken{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return APIToken{}, err
	}
	var t APIToken
	return t, scanToken(s.db.QueryRow(`SELECT `+tokenColumns+` FROM api_tokens WHERE id = ?`, id), &t)
}

// APITokenByHash looks a token up by its SHA-256 (found false when there is none). A revoked token is found with
// Revoked set: the caller refuses it.
func (s *Store) APITokenByHash(sha256Hex string) (APIToken, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var t APIToken
	err := scanToken(s.db.QueryRow(`SELECT `+tokenColumns+` FROM api_tokens WHERE sha256 = ?`, sha256Hex), &t)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, false, nil
	}
	return t, err == nil, err
}

// APITokens returns every token, newest first.
func (s *Store) APITokens() ([]APIToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []APIToken
	err := each(s.db, func(r *sql.Rows) error {
		var t APIToken
		if err := scanToken(r, &t); err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}, `SELECT `+tokenColumns+` FROM api_tokens ORDER BY id DESC`)
	return out, err
}

func (s *Store) tokenExec(query string, args ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrNoToken
	}
	return nil
}

// RevokeAPIToken revokes a token for good (ErrNoToken when there is none).
func (s *Store) RevokeAPIToken(id int64) error {
	return s.tokenExec(`UPDATE api_tokens SET revoked = 1 WHERE id = ?`, id)
}

// TouchAPIToken records the token's use.
func (s *Store) TouchAPIToken(id int64, now time.Time) error {
	return s.tokenExec(`UPDATE api_tokens SET last_used = ? WHERE id = ?`, formatTime(now), id)
}
