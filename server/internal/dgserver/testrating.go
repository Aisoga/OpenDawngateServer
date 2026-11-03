package dgserver

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/store"
)

// The test aid --test-rating <login name>=<rating>[,<login name>=<rating>...] (RATING2): at the start, after the
// rating's seed from the stored matches, each named account (made without a password when it doesn't exist, as
// --test-history-seed does, so the first login sets one) gets that skill rating, with an RD of 100 and 10 rated
// games, so a session can look at what a rating far from the start value changes (the custom lobby's BALANCE, which
// balances by DMAP[0x00070035]) without playing rated games first. A test aid only: it writes over the stored rating
// at every start.

// testRatingRD and testRatingGames are what --test-rating stores beside the rating.
const (
	testRatingRD    = 100
	testRatingGames = 10
)

// testRating is one --test-rating entry.
type testRating struct {
	login string
	value float64
}

// parseTestRating reads <login name>=<rating>[,...] (a rating from 0 to 5000).
func parseTestRating(spec string) ([]testRating, error) {
	var out []testRating
	for _, part := range strings.Split(spec, ",") {
		login, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		login = accounts.NormalizeName(login)
		v, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if !ok || login == "" || err != nil || v < 0 || v > 5000 {
			return nil, fmt.Errorf("%q: want <login name>=<rating>[,...] with a rating from 0 to 5000", part)
		}
		out = append(out, testRating{login, v})
	}
	return out, nil
}

// applyTestRating stores the ratings of spec and returns the log line's text.
func applyTestRating(st *store.Store, spec string, now time.Time) (string, error) {
	rs, err := parseTestRating(spec)
	if err != nil {
		return "", err
	}
	var done []string
	for _, r := range rs {
		_, acct, err := seedPersona(st, r.login, now)
		if err != nil {
			return "", err
		}
		if _, err := st.Update(acct.UserID, func(a *store.Account) {
			a.History.Rating = store.Rating{Value: r.value, RD: testRatingRD, Vol: store.StartRating.Vol,
				Games: testRatingGames, Wins: testRatingGames / 2}
		}); err != nil {
			return "", err
		}
		done = append(done, fmt.Sprintf("%s (account %d) %g", r.login, acct.UserID, r.value))
	}
	return "ratings set: " + strings.Join(done, ", "), nil
}
