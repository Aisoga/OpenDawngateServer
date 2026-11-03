package issues

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/store"
)

func TestEntries(t *testing.T) {
	es, err := Entries()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range es {
		if seen[e.ID] || e.Title == "" {
			t.Errorf("entry %+v", e)
		}
		seen[e.ID] = true
	}
	good := `[{"id": "UI1", "area": "The menus", "title": "A guess", "assumed": "We assumed it.", "lookFor": "Look at it.",
		"top": true, "order": 1}]`
	es, err = ParseEntries([]byte(good))
	if err != nil || len(es) != 1 || es[0] != (Entry{"UI1", "The menus", "A guess", "We assumed it.", "Look at it.", true, 1}) {
		t.Errorf("parsed %+v %v", es, err)
	}
	for _, bad := range []string{`{}`, `[{"title": "x"}]`, `[{"id": "A1"}]`, `[{"id": "A1", "title": "x"}, {"id": "A1", "title": "y"}]`} {
		if _, err := ParseEntries([]byte(bad)); err == nil {
			t.Errorf("%s taken", bad)
		}
	}
}

// Sync adds, updates and withdraws by id and never touches a state or a claim.
func TestSync(t *testing.T) {
	st := store.New()
	defer st.Close()
	t0 := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	e := func(id string, order int) Entry {
		return Entry{ID: id, Area: "Area", Title: "Title " + id, Assumed: "We assumed " + id, LookFor: "Look " + id, Order: order}
	}
	res, err := Sync(st, []Entry{e("A1", 1), e("A2", 2), e("A3", 3)}, t0)
	if err != nil || res != (store.SyncResult{Added: 3}) {
		t.Fatalf("first sync %+v %v", res, err)
	}
	acct, err := st.Create(store.NewAccount{Name: "alice", Hash: "$h"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutClaim("A2", acct.UserID, store.StanceCorrect, "", []store.ClaimVideo{{VideoID: vid, Seconds: 9}}, t0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetVerdict("A3", store.IssueConfirmed, "seen in a video", "api:review", t0); err != nil {
		t.Fatal(err)
	}
	a1 := e("A1", 1)
	a1.LookFor, a1.Top = "Something else", true
	res, err = Sync(st, []Entry{a1, e("A3", 2), e("A4", 3)}, t0.Add(time.Hour))
	if err != nil || res != (store.SyncResult{Added: 1, Changed: 2, Withdrawn: 1}) { // A3's order changed
		t.Fatalf("second sync %+v %v", res, err)
	}
	rows, _ := st.ListIssues(store.IssueFilter{Withdrawn: true})
	got := map[string]store.IssueRow{}
	for _, r := range rows {
		got[r.ID] = r
	}
	if r := got["A1"]; r.LookFor != "Something else" || !r.Top || !r.InDoc {
		t.Errorf("A1 %+v", r)
	}
	if r := got["A2"]; r.InDoc || r.Correct.Claims() != 1 || StatusOf(r) != LeaningCorrect {
		t.Errorf("A2 withdrawn %+v", r)
	}
	if r := got["A3"]; r.State != store.IssueConfirmed || r.VerdictNote != "seen in a video" || r.Order != 2 {
		t.Errorf("A3 %+v", r)
	}
	if r := got["A4"]; r.State != store.IssueOpen || StatusOf(r) != NeedsVerification {
		t.Errorf("A4 %+v", r)
	}
	if _, err := Sync(st, []Entry{e("A1", 1), e("A1", 2)}, t0); err == nil {
		t.Error("an id twice synced")
	}
	// the embedded list syncs (the placeholder is empty: everything is withdrawn then)
	es, _ := Entries()
	if res, err := Sync(st, es, t0); err != nil || res.Added+res.Changed+res.Unchanged != len(es) {
		t.Errorf("embedded list %+v %v", res, err)
	}
}

func TestCheckNote(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "",
		"  seen at 1:23 \r\n":          "seen at 1:23",
		"line one\r\nline two\rthree":  "line one\nline two\nthree",
		"tab\there":                    "tab\there",
		"<a href=x>link</a> & ünïcode": "<a href=x>link</a> & ünïcode", // stored as typed; the pages escape it
		"family 👨‍👩‍👧":                 "family 👨‍👩‍👧",                 // a zero-width joiner is text
		strings.Repeat("ü", MaxNote):   strings.Repeat("ü", MaxNote),
	} {
		if got, err := CheckNote(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for in, want := range map[string]error{
		strings.Repeat("a", MaxNote+1): ErrNoteLong,
		"bell\a":                       ErrNoteControl,
		"nul\x00":                      ErrNoteControl,
		"esc\x1b[31m":                  ErrNoteControl,
		"\xff\xfe":                     ErrNoteControl,
		"abc‮dcba":                     ErrNoteControl, // right-to-left override
		"iso⁦late":                     ErrNoteControl,
		"para graph":                   ErrNoteControl,
	} {
		if _, err := CheckNote(in); !errors.Is(err, want) {
			t.Errorf("%q: %v, want %v", in, err, want)
		}
	}
}

func TestToken(t *testing.T) {
	a, ha, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, hb, _ := NewToken()
	if raw, err := base64.RawURLEncoding.DecodeString(a); err != nil || len(raw) != 32 || len(a) != 43 {
		t.Errorf("token %q: %d bytes %v", a, len(raw), err)
	}
	if a == b || ha == hb || ha != TokenHash(a) || len(ha) != 64 || strings.ToLower(ha) != ha {
		t.Errorf("tokens %q %q hashes %q %q", a, b, ha, hb)
	}
	// SHA-256("abc")
	if got := TokenHash("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("hash %s", got)
	}
	st := store.New()
	defer st.Close()
	tok, err := st.CreateAPIToken("review", ha, "admin", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got, found, _ := st.APITokenByHash(TokenHash(a)); !found || got.ID != tok.ID {
		t.Error("the token not found by its hash")
	}
}
