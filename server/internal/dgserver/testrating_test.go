package dgserver

import (
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/store"
)

func TestParseTestRating(t *testing.T) {
	rs, err := parseTestRating(" r2a@x.io=2400, r2b@x.io = 600.5 ")
	if err != nil || len(rs) != 2 || rs[0] != (testRating{"r2a@x.io", 2400}) || rs[1] != (testRating{"r2b@x.io", 600.5}) {
		t.Errorf("parsed %+v, %v", rs, err)
	}
	for _, bad := range []string{"", "r2a@x.io", "=1500", "r2a@x.io=high", "r2a@x.io=-1", "r2a@x.io=5001", "a=1,b"} {
		if _, err := parseTestRating(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// RATING2's test aid: the named accounts (one made without a password) get their ratings at the start, after the
// seed, and again at the next start; a bad value is only logged.
func TestTestRatingAtStart(t *testing.T) {
	data := t.TempDir()
	log := runBriefly(t, data, "--test-rating", "r2a@x.io=2400,r2b@x.io=600")
	if !strings.Contains(log, "TEST: skill rating: ratings set: r2a@x.io (account ") ||
		!strings.Contains(log, ") 600 (--test-rating, a test aid)") {
		t.Fatalf("start:\n%s", log)
	}
	st, err := store.Open(filepath.Join(data, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]float64{"r2a": 2400, "r2b": 600} {
		a, _, ok := st.PersonaByName(name)
		if r := a.History.Rating; !ok || r.Value != want || r.RD != testRatingRD || r.Games != testRatingGames {
			t.Errorf("%s: %v %+v, want %g", name, ok, r, want)
		}
	}
	st.Close()
	if log := runBriefly(t, data, "--test-rating", "r2a@x.io=lots"); !strings.Contains(log, "TEST: --test-rating r2a@x.io=lots: ") {
		t.Errorf("a bad value:\n%s", log)
	}
}
