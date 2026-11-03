package issues

import (
	"testing"
	"time"

	"dawngate/server/internal/store"
)

func claim(id int64, stance store.Stance, videos ...int) store.Claim {
	c := store.Claim{ID: id, Stance: stance, Created: time.Unix(id, 0)}
	for _, s := range videos {
		c.Videos = append(c.Videos, store.ClaimVideo{VideoID: vid, Seconds: s})
	}
	return c
}

func TestWeight(t *testing.T) {
	for _, c := range []struct {
		c    store.Claim
		want int
	}{
		{claim(1, store.StanceCorrect), 1},
		{claim(1, store.StanceCorrect, -1), 2},
		{claim(1, store.StanceCorrect, -1, -1, -1), 2},
		{claim(1, store.StanceWrong, 0), 3},
		{claim(1, store.StanceWrong, -1, 83), 3},
		{claim(1, store.StanceWrong, 1, 2, 3), 3}, // at most 3 per claim
	} {
		if got := ClaimWeight(c.c); got != c.want {
			t.Errorf("%+v: %d, want %d", c.c.Videos, got, c.want)
		}
	}
}

// row builds an issue row from claims, as the store counts them.
func row(state store.IssueState, cs ...store.Claim) store.IssueRow {
	r := store.IssueRow{Issue: store.Issue{State: state}}
	for _, c := range cs {
		if c.Hidden {
			r.Hidden++
			continue
		}
		sc := &r.Correct
		if c.Stance == store.StanceWrong {
			sc = &r.Wrong
		}
		sc.ByEvidence[c.Evidence()]++
		sc.Videos += len(c.Videos)
	}
	return r
}

func hidden(c store.Claim) store.Claim { c.Hidden = true; return c }

func TestStatus(t *testing.T) {
	ok, wrong := store.StanceCorrect, store.StanceWrong
	for _, c := range []struct {
		name string
		r    store.IssueRow
		want Status
	}{
		{"no claim", row(store.IssueOpen), NeedsVerification},
		{"only a hidden claim", row(store.IssueOpen, hidden(claim(1, ok, 5))), NeedsVerification},
		{"notes only", row(store.IssueOpen, claim(1, ok), claim(2, ok), claim(3, ok), claim(4, ok)), NeedsVideo},
		{"a hidden video doesn't count", row(store.IssueOpen, claim(1, ok), hidden(claim(2, ok, 5))), NeedsVideo},
		{"one timed video: 3", row(store.IssueOpen, claim(1, ok, 5)), LeaningCorrect},
		{"one untimed video leans", row(store.IssueOpen, claim(1, ok, -1)), LeaningCorrect},
		{"one untimed video against nothing: wrong", row(store.IssueOpen, claim(1, wrong, -1)), LeaningWrong},
		{"a video against a note: 2 against 1", row(store.IssueOpen, claim(1, ok, -1), claim(2, wrong)), LeaningCorrect},
		{"a timed video against a video: 3 against 2", row(store.IssueOpen, claim(1, ok, 5), claim(2, wrong, -1)), Disputed},
		{"untimed video and a note", row(store.IssueOpen, claim(1, ok, -1), claim(2, ok)), LeaningCorrect},
		{"3 against 1: three times", row(store.IssueOpen, claim(1, wrong, 5), claim(2, ok)), LeaningWrong},
		{"4 against 2: exactly twice", row(store.IssueOpen, claim(1, ok, 5), claim(2, ok), claim(3, wrong, -1)), LeaningCorrect},
		{"5 against 3: under twice", row(store.IssueOpen, claim(1, ok, 5), claim(2, ok, -1), claim(3, wrong, 5)), Disputed},
		{"3 against 3", row(store.IssueOpen, claim(1, ok, 5), claim(2, wrong, 5)), Disputed},
		{"6 against 3", row(store.IssueOpen, claim(1, wrong, 5), claim(2, wrong, 7), claim(3, ok, 1)), LeaningWrong},
		{"a hidden counter-claim", row(store.IssueOpen, claim(1, ok, 5), hidden(claim(2, wrong, 5))), LeaningCorrect},
		{"confirmed", row(store.IssueConfirmed), Confirmed},
		{"disproven over claims", row(store.IssueDisproven, claim(1, ok, 5)), Disproven},
		{"closed", row(store.IssueClosed, claim(1, wrong, 5)), Closed},
	} {
		if got := StatusOf(c.r); got != c.want {
			t.Errorf("%s: %s (%+v), want %s", c.name, got, TallyOf(c.r), c.want)
		}
	}
	tl := TallyOf(row(store.IssueOpen, claim(1, ok, 5, -1), claim(2, wrong), hidden(claim(3, wrong, 5))))
	if tl != (Tally{Correct: 3, Wrong: 1, Claims: 2, Videos: 2, Hidden: 1}) {
		t.Errorf("tally %+v", tl)
	}
	for _, s := range Statuses {
		if p, ok := ParseStatus(string(s)); !ok || p != s || s.Label() == string(s) {
			t.Errorf("%q: %q %v %q", s, p, ok, s.Label())
		}
		if s.Open() == (s == Confirmed || s == Disproven || s == Closed) {
			t.Errorf("%q open %v", s, s.Open())
		}
	}
	if _, ok := ParseStatus("leaning"); ok {
		t.Error("an unknown status parsed")
	}
}

func TestBestFirst(t *testing.T) {
	cs := []store.Claim{claim(1, store.StanceCorrect), hidden(claim(2, store.StanceWrong, 5)), claim(3, store.StanceWrong, -1),
		claim(4, store.StanceCorrect, 9), claim(5, store.StanceWrong, 1)}
	got := BestFirst(cs)
	var ids []int64
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if want := []int64{4, 5, 3, 1, 2}; len(ids) != 5 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] ||
		ids[3] != want[3] || ids[4] != want[4] {
		t.Errorf("order %v, want %v", ids, want)
	}
	if cs[0].ID != 1 {
		t.Error("the input reordered")
	}
}
