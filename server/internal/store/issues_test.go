package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var issueT0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func issueText(id string, order int) IssueText {
	return IssueText{ID: id, Area: "Area " + id[:2], Title: "Title " + id, Assumed: "We assumed " + id,
		LookFor: "Look for " + id, Order: order}
}

// newIssueStore is a store with three issues (AA1, AA2, BB1, in that order) and three accounts.
func newIssueStore(t *testing.T) (*Store, []int64) {
	t.Helper()
	s := New()
	t.Cleanup(func() { s.Close() })
	if _, err := s.SyncIssues([]IssueText{issueText("AA1", 1), issueText("AA2", 2), issueText("BB1", 3)}, issueT0); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i, name := range []string{"alice", "bob", "carol"} {
		a, err := s.Create(NewAccount{Name: name, Hash: "$h"}, issueT0.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.UserID)
	}
	return s, ids
}

// a database of version 7 migrates to version 8: the tracker's tables are there and empty, and take issues, claims
// and tokens.
func TestMigrateFromVersion7(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied TEXT NOT NULL, note TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:7] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_version VALUES (?, '', ?)`, m.version, m.note); err != nil {
			t.Fatal(err)
		}
	}
	tx, _ := db.Begin()
	insertOldAccount(t, tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := openT(t, path)
	if want := fmt.Sprintf("1 accounts, schema 7 -> %d", SchemaVersion()); s.Info() != want || SchemaVersion() < 8 {
		t.Errorf("info %q, want %q", s.Info(), want)
	}
	if rows, err := s.ListIssues(IssueFilter{Withdrawn: true}); err != nil || len(rows) != 0 {
		t.Errorf("issues after the migration: %v %v", rows, err)
	}
	if toks, err := s.APITokens(); err != nil || len(toks) != 0 {
		t.Errorf("tokens after the migration: %v %v", toks, err)
	}
	if _, err := s.SyncIssues([]IssueText{issueText("AA1", 1)}, issueT0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutClaim("AA1", FirstUserID, StanceWrong, "old account", nil, issueT0); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openT(t, path)
	if cs, err := s.ClaimsOf(FirstUserID); err != nil || len(cs) != 1 || cs[0].Note != "old account" {
		t.Errorf("read back: %+v %v", cs, err)
	}
}

// SyncIssues inserts new issues open, updates a listed one's text, withdraws an id no longer listed (and lists it
// again as it was), and never touches a state, a verdict or a claim.
func TestSyncIssues(t *testing.T) {
	s, users := newIssueStore(t)
	rows, err := s.ListIssues(IssueFilter{})
	if err != nil || len(rows) != 3 || rows[0].ID != "AA1" || rows[2].ID != "BB1" || rows[0].State != IssueOpen ||
		!rows[0].InDoc || rows[0].Assumed != "We assumed AA1" || !rows[0].Updated.Equal(issueT0) {
		t.Fatalf("rows %+v %v", rows, err)
	}
	if _, err := s.PutClaim("AA2", users[0], StanceCorrect, "", nil, issueT0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVerdict("BB1", IssueConfirmed, "matches the video", "api:review", issueT0); err != nil {
		t.Fatal(err)
	}

	t1 := issueT0.Add(time.Hour)
	aa1 := issueText("AA1", 1)
	aa1.Title, aa1.Top = "A new title", true
	res, err := s.SyncIssues([]IssueText{aa1, issueText("BB1", 3), issueText("CC1", 4)}, t1)
	if err != nil || res != (SyncResult{Added: 1, Changed: 1, Unchanged: 1, Withdrawn: 1}) {
		t.Fatalf("sync %+v %v", res, err)
	}
	if res.String() != "1 added, 1 changed, 1 unchanged, 1 withdrawn" {
		t.Errorf("%q", res)
	}
	rows, _ = s.ListIssues(IssueFilter{})
	if len(rows) != 3 || rows[0].Title != "A new title" || !rows[0].Top || !rows[0].Updated.Equal(t1) ||
		rows[1].ID != "BB1" || rows[1].State != IssueConfirmed || !rows[1].Updated.Equal(issueT0) || rows[2].ID != "CC1" {
		t.Fatalf("after the sync %+v", rows)
	}
	all, _ := s.ListIssues(IssueFilter{Withdrawn: true})
	if len(all) != 4 || all[1].ID != "AA2" || all[1].InDoc || all[1].Correct.Claims() != 1 {
		t.Fatalf("with the withdrawn %+v", all)
	}
	// a withdrawn issue takes no claim
	if _, err := s.PutClaim("AA2", users[1], StanceWrong, "", nil, t1); !errors.Is(err, ErrIssueWithdrawn) {
		t.Errorf("claim on a withdrawn issue: %v", err)
	}
	// listed again: back with its claim
	if res, _ := s.SyncIssues([]IssueText{aa1, issueText("AA2", 2), issueText("BB1", 3), issueText("CC1", 4)}, t1); res.Changed != 1 {
		t.Errorf("relisted %+v", res)
	}
	if d, found, _ := s.IssueDetail("AA2", 0); !found || !d.InDoc || len(d.Claims) != 1 || d.State != IssueOpen {
		t.Errorf("relisted %+v", d)
	}
	if v, _, _ := s.IssueDetail("BB1", 0); v.VerdictNote != "matches the video" || v.ClosedBy != "api:review" {
		t.Errorf("verdict touched %+v", v)
	}

	for _, bad := range [][]IssueText{{{ID: ""}}, {issueText("AA1", 1), issueText("AA1", 2)}} {
		if _, err := s.SyncIssues(bad, t1); err == nil {
			t.Errorf("sync of %+v taken", bad)
		}
	}
	if changed, err := s.UpsertIssue(aa1, t1); err != nil || changed {
		t.Errorf("upsert unchanged: %v %v", changed, err)
	}
	if changed, err := s.UpsertIssue(issueText("DD1", 9), t1); err != nil || !changed {
		t.Errorf("upsert new: %v %v", changed, err)
	}
}

func TestIssueClaims(t *testing.T) {
	s, users := newIssueStore(t)
	alice, bob, carol := users[0], users[1], users[2]
	if _, err := s.Update(bob, func(a *Account) { a.Personas[0].Name = "Bobby" }); err != nil {
		t.Fatal(err)
	}
	v := func(id string, sec int) ClaimVideo { return ClaimVideo{VideoID: id, Seconds: sec} }
	id1, id2 := "dQw4w9WgXcQ", "abc_DEF-123"

	// refusals
	for name, err := range map[string]error{
		"stance": func() error { _, err := s.PutClaim("AA1", alice, 3, "", nil, issueT0); return err }(),
		"note": func() error {
			_, err := s.PutClaim("AA1", alice, 1, strings.Repeat("é", 1001), nil, issueT0)
			return err
		}(),
		"utf8":     func() error { _, err := s.PutClaim("AA1", alice, 1, "\xff", nil, issueT0); return err }(),
		"4 videos": func() error { _, err := s.PutClaim("AA1", alice, 1, "", make([]ClaimVideo, 4), issueT0); return err }(),
		"video id": func() error {
			_, err := s.PutClaim("AA1", alice, 1, "", []ClaimVideo{v("short", -1)}, issueT0)
			return err
		}(),
		"seconds": func() error { _, err := s.PutClaim("AA1", alice, 1, "", []ClaimVideo{v(id1, -2)}, issueT0); return err }(),
	} {
		if err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	if _, err := s.PutClaim("ZZ9", alice, 1, "", nil, issueT0); !errors.Is(err, ErrNoIssue) {
		t.Errorf("unknown issue: %v", err)
	}
	if _, err := s.PutClaim("AA1", 42, 1, "", nil, issueT0); !errors.Is(err, ErrNoAccount) {
		t.Errorf("unknown account: %v", err)
	}
	if _, err := s.PutClaim("AA1", alice, 1, strings.Repeat("é", 1000), nil, issueT0); err != nil {
		t.Errorf("1000 characters: %v", err)
	}
	if err := s.DeleteClaim("AA1", alice, issueT0); err != nil {
		t.Fatal(err)
	}

	// alice: correct with a timed video; bob: wrong with an untimed one; carol: correct, note only
	t1 := issueT0.Add(time.Minute)
	c, err := s.PutClaim("AA1", alice, StanceCorrect, "at the start", []ClaimVideo{v(id1, 83), v(id2, -1)}, t1)
	if err != nil || c.Persona != "alice" || c.IssueTitle != "Title AA1" || len(c.Videos) != 2 || c.Videos[0] != v(id1, 83) ||
		!c.Created.Equal(t1) || c.Evidence() != EvidenceTimed {
		t.Fatalf("claim %+v %v", c, err)
	}
	if _, err := s.PutClaim("AA1", bob, StanceWrong, "", []ClaimVideo{v(id2, -1)}, t1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutClaim("AA1", carol, StanceCorrect, "", nil, t1); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListIssues(IssueFilter{Viewer: bob})
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r.Correct.ByEvidence != [3]int{1, 0, 1} || r.Correct.Videos != 2 || r.Wrong.ByEvidence != [3]int{0, 1, 0} ||
		r.Wrong.Videos != 1 || r.Own != StanceWrong || rows[1].Own != 0 || rows[1].Correct.Claims() != 0 || !r.Updated.Equal(t1) {
		t.Errorf("row %+v", r)
	}

	// replace alice's: same id and creation time, the videos replaced
	t2 := t1.Add(time.Minute)
	c2, err := s.PutClaim("AA1", alice, StanceWrong, "changed my mind", []ClaimVideo{v(id2, 5)}, t2)
	if err != nil || c2.ID != c.ID || !c2.Created.Equal(t1) || !c2.Updated.Equal(t2) || c2.Stance != StanceWrong ||
		len(c2.Videos) != 1 || c2.Videos[0] != v(id2, 5) {
		t.Fatalf("replaced %+v %v", c2, err)
	}
	d, found, err := s.IssueDetail("AA1", alice)
	if err != nil || !found || len(d.Claims) != 3 || d.Own != StanceWrong || d.Claims[1].Persona != "Bobby" ||
		d.Wrong.ByEvidence != [3]int{0, 1, 1} || d.Correct.ByEvidence != [3]int{1, 0, 0} {
		t.Fatalf("detail %+v %v %v", d, found, err)
	}
	if own, ok := d.ClaimOf(alice); !ok || own.Note != "changed my mind" {
		t.Errorf("own %+v", own)
	}
	if _, found, _ := s.IssueDetail("ZZ9", 0); found {
		t.Error("an unknown issue found")
	}

	// hide bob's: not counted, still listed (marked), still his own stance
	hb, err := s.SetClaimHidden(d.Claims[1].ID, true, t2)
	if err != nil || !hb.Hidden {
		t.Fatalf("hide %+v %v", hb, err)
	}
	rows, _ = s.ListIssues(IssueFilter{Viewer: bob})
	if rows[0].Wrong.ByEvidence != [3]int{0, 0, 1} || rows[0].Hidden != 1 || rows[0].Own != StanceWrong {
		t.Errorf("hidden: %+v", rows[0])
	}
	if hb, err := s.SetClaimHidden(hb.ID, false, t2); err != nil || hb.Hidden {
		t.Errorf("unhide %+v %v", hb, err)
	}
	if _, err := s.SetClaimHidden(9999, true, t2); !errors.Is(err, ErrNoClaim) {
		t.Errorf("hide unknown: %v", err)
	}
	if got, found, err := s.Claim(hb.ID); err != nil || !found || got.Persona != "Bobby" || len(got.Videos) != 1 {
		t.Errorf("claim by id %+v %v %v", got, found, err)
	}

	// carol deletes hers; again: none
	if err := s.DeleteClaim("AA1", carol, t2); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteClaim("AA1", carol, t2); !errors.Is(err, ErrNoClaim) {
		t.Errorf("deleted twice: %v", err)
	}

	// a verdict: the issue is read-only; a second verdict is refused; reopened, it takes claims again
	t3 := t2.Add(time.Hour)
	if err := s.SetVerdict("AA1", IssueOpen, "", "x", t3); err == nil {
		t.Error("open taken as a verdict")
	}
	if err := s.SetVerdict("AA1", IssueDisproven, strings.Repeat("x", MaxVerdictNote+1), "x", t3); err == nil {
		t.Error("a long verdict note taken")
	}
	if err := s.SetVerdict("AA1", IssueDisproven, "the video shows 5 s", "admin boss (account 1)", t3); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVerdict("AA1", IssueClosed, "", "x", t3); !errors.Is(err, ErrIssueClosed) {
		t.Errorf("second verdict: %v", err)
	}
	if _, err := s.PutClaim("AA1", carol, StanceCorrect, "", nil, t3); !errors.Is(err, ErrIssueClosed) {
		t.Errorf("claim on a closed issue: %v", err)
	}
	if err := s.DeleteClaim("AA1", alice, t3); !errors.Is(err, ErrIssueClosed) {
		t.Errorf("delete on a closed issue: %v", err)
	}
	if _, err := s.SetClaimHidden(hb.ID, true, t3); err != nil {
		t.Errorf("hide on a closed issue: %v", err)
	}
	d, _, _ = s.IssueDetail("AA1", 0)
	if d.State != IssueDisproven || d.VerdictNote != "the video shows 5 s" || !d.ClosedAt.Equal(t3) ||
		d.ClosedBy != "admin boss (account 1)" {
		t.Errorf("verdict %+v", d.Issue)
	}
	if rows, _ := s.ListIssues(IssueFilter{State: IssueDisproven}); len(rows) != 1 || rows[0].ID != "AA1" {
		t.Errorf("by state %+v", rows)
	}
	if rows, _ := s.ListIssues(IssueFilter{Since: t3}); len(rows) != 1 || rows[0].ID != "AA1" {
		t.Errorf("since %+v", rows)
	}
	if rows, _ := s.ListIssues(IssueFilter{Since: t3.Add(time.Nanosecond)}); len(rows) != 0 {
		t.Errorf("since later %+v", rows)
	}
	if err := s.ReopenIssue("AA1", t3); err != nil {
		t.Fatal(err)
	}
	if err := s.ReopenIssue("AA1", t3); !errors.Is(err, ErrIssueOpen) {
		t.Errorf("reopened twice: %v", err)
	}
	if err := s.ReopenIssue("ZZ9", t3); !errors.Is(err, ErrNoIssue) {
		t.Errorf("reopen unknown: %v", err)
	}
	d, _, _ = s.IssueDetail("AA1", 0)
	if d.State != IssueOpen || d.VerdictNote != "" || !d.ClosedAt.IsZero() || d.ClosedBy != "" || len(d.Claims) != 2 {
		t.Errorf("reopened %+v", d)
	}
	if _, err := s.PutClaim("AA1", carol, StanceCorrect, "", nil, t3); err != nil {
		t.Errorf("claim after the reopening: %v", err)
	}
}

// an account's deletion removes its claims and their videos; the others' stay, and the issue counts as changed.
func TestDeleteAccountRemovesClaims(t *testing.T) {
	s, users := newIssueStore(t)
	vid := []ClaimVideo{{VideoID: "dQw4w9WgXcQ", Seconds: 3}}
	for _, is := range []string{"AA1", "BB1"} {
		if _, err := s.PutClaim(is, users[0], StanceCorrect, "mine", vid, issueT0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PutClaim("AA1", users[1], StanceWrong, "", vid, issueT0); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if err := s.DeleteAccount(users[0]); err != nil {
		t.Fatal(err)
	}
	if cs, err := s.ClaimsOf(users[0]); err != nil || len(cs) != 0 {
		t.Errorf("claims left %+v %v", cs, err)
	}
	var videos int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM issue_videos`).Scan(&videos); err != nil || videos != 1 {
		t.Errorf("%d videos left (%v)", videos, err)
	}
	d, _, _ := s.IssueDetail("AA1", 0)
	if len(d.Claims) != 1 || d.Claims[0].UserID != users[1] || d.Updated.Before(before) {
		t.Errorf("after the deletion %+v", d)
	}
	if d, _, _ := s.IssueDetail("AA2", 0); !d.Updated.Equal(issueT0) {
		t.Errorf("an issue without its claims changed: %v", d.Updated)
	}
}

func TestAPITokens(t *testing.T) {
	s := New()
	defer s.Close()
	hash := strings.Repeat("ab", 32)
	for _, bad := range []string{"", strings.Repeat("AB", 32), strings.Repeat("a", 63), strings.Repeat("g", 64)} {
		if _, err := s.CreateAPIToken("review", bad, "admin boss", issueT0); err == nil {
			t.Errorf("hash %q taken", bad)
		}
	}
	if _, err := s.CreateAPIToken(" ", hash, "admin boss", issueT0); err == nil {
		t.Error("a token without a name")
	}
	tok, err := s.CreateAPIToken("review", hash, "admin boss (account 1)", issueT0)
	if err != nil || tok.ID == 0 || tok.Name != "review" || tok.CreatedBy != "admin boss (account 1)" ||
		!tok.Created.Equal(issueT0) || !tok.LastUsed.IsZero() || tok.Revoked {
		t.Fatalf("token %+v %v", tok, err)
	}
	if _, err := s.CreateAPIToken("again", hash, "", issueT0); err == nil {
		t.Error("the same hash twice")
	}
	if err := s.TouchAPIToken(tok.ID, issueT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.APITokenByHash(hash)
	if err != nil || !found || got.ID != tok.ID || !got.LastUsed.Equal(issueT0.Add(time.Hour)) {
		t.Errorf("by hash %+v %v %v", got, found, err)
	}
	if _, found, err := s.APITokenByHash(strings.Repeat("cd", 32)); found || err != nil {
		t.Errorf("unknown hash found (%v)", err)
	}
	if err := s.RevokeAPIToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.APITokenByHash(hash); !got.Revoked {
		t.Error("not revoked")
	}
	if err := s.RevokeAPIToken(99); !errors.Is(err, ErrNoToken) {
		t.Errorf("revoke unknown: %v", err)
	}
	if err := s.TouchAPIToken(99, issueT0); !errors.Is(err, ErrNoToken) {
		t.Errorf("touch unknown: %v", err)
	}
	s.CreateAPIToken("second", strings.Repeat("cd", 32), "", issueT0)
	if all, err := s.APITokens(); err != nil || len(all) != 2 || all[0].Name != "second" {
		t.Errorf("all %+v %v", all, err)
	}
}

func TestStanceText(t *testing.T) {
	for _, s := range []Stance{StanceCorrect, StanceWrong} {
		b, _ := s.MarshalText()
		var back Stance
		if err := back.UnmarshalText(b); err != nil || back != s {
			t.Errorf("%v: %q %v %v", s, b, back, err)
		}
	}
	var x Stance
	if x.UnmarshalText([]byte("maybe")) == nil || Stance(0).String() != "none" || Stance(7).String() != "stance7" {
		t.Error("unknown stances")
	}
	if !IssueClosed.Verdict() || IssueOpen.Verdict() {
		t.Error("Verdict")
	}
}
