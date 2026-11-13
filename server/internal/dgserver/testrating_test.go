package dgserver

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dawngate/server/internal/store"
)

func TestParseTestRating(t *testing.T) {
	rs, err := parseTestRating(" r2a@x.io=2400, r2b@x.io = 600.5 , r2a@x.io=1700:draft,r2b@x.io=1650 : custom,r2c@x.io=1:standard")
	want := []testRating{{"r2a@x.io", 2400, store.ModeStandard}, {"r2b@x.io", 600.5, store.ModeStandard},
		{"r2a@x.io", 1700, store.ModeDraft}, {"r2b@x.io", 1650, store.ModeCustom}, {"r2c@x.io", 1, store.ModeStandard}}
	if err != nil || !slices.Equal(rs, want) {
		t.Errorf("parsed %+v, %v", rs, err)
	}
	for _, bad := range []string{"", "r2a@x.io", "=1500", "r2a@x.io=high", "r2a@x.io=-1", "r2a@x.io=5001", "a=1,b",
		"a=1500:aram", "a=1500:", "a=1500:Draft"} {
		if _, err := parseTestRating(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// RATING2's test aid: the named accounts (one made without a password) get their ratings at the start, after the
// seed, and again at the next start; a bad value is only logged. MMR2: a type sets that type's rating only.
func TestTestRatingAtStart(t *testing.T) {
	data := t.TempDir()
	log := runBriefly(t, data, "--test-rating", "r2a@x.io=2400,r2b@x.io=1700:draft,r2b@x.io=600")
	if !strings.Contains(log, "TEST: skill rating: ratings set: r2a@x.io (account ") ||
		!strings.Contains(log, ") 1700 draft, r2b@x.io (account ") ||
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
	a, _, _ := st.PersonaByName("r2b")
	if r := a.History.RatingIn(store.ModeDraft); r.Value != 1700 || r.Games != testRatingGames || len(a.History.ModeRatings) != 1 {
		t.Errorf("r2b's draft rating %+v (types %v)", r, a.History.ModeRatings)
	}
	if rows, err := st.RatingLeaderboard(store.ModeDraft, "", testRatingGames, 10); err != nil || len(rows) != 1 ||
		rows[0].Rating != 1700 {
		t.Errorf("the draft ranking: %+v, %v", rows, err)
	}
	st.Close()
	if log := runBriefly(t, data, "--test-rating", "r2a@x.io=lots"); !strings.Contains(log, "TEST: --test-rating r2a@x.io=lots: ") {
		t.Errorf("a bad value:\n%s", log)
	}
}
