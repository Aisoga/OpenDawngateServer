package dgserver

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/store"
)

// The test aid --test-rating <login name>=<rating>[:<type>][,<login name>=<rating>[:<type>]...] (RATING2): at the
// start, after the rating's seed from the stored matches, each named account (made when it doesn't exist, and given
// the password dawngate1 when it has none, as --test-history-seed does) gets that skill rating, with an RD of 100 and 10
// rated games, so a session can look at what a rating far from the start value changes (the custom lobby's BALANCE,
// which balances by DMAP[0x00070035]) without playing rated games first. The type (MMR2) is the game type whose rating
// it sets, one of standard (the default), draft and custom (store.RatingModes), so the Leaderboard's tabs can show three
// rankings; an account may be named once per type. A test aid only: it writes over the stored rating at every start.

// testRatingRD and testRatingGames are what --test-rating stores beside the rating.
const (
	testRatingRD    = 100
	testRatingGames = 10
)

// testRating is one --test-rating entry.
type testRating struct {
	login string
	value float64
	mode  string // the game type (store.RatingModes)
}

// parseTestRating reads <login name>=<rating>[:<type>][,...] (a rating from 0 to 5000, a type of store.RatingModes;
// standard when left out).
func parseTestRating(spec string) ([]testRating, error) {
	var out []testRating
	for _, part := range strings.Split(spec, ",") {
		login, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		login = accounts.NormalizeName(login)
		val, mode, typed := strings.Cut(val, ":")
		mode = strings.TrimSpace(mode)
		if !typed {
			mode = store.ModeStandard
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if !ok || login == "" || err != nil || v < 0 || v > 5000 || !slices.Contains(store.RatingModes, mode) {
			return nil, fmt.Errorf("%q: want <login name>=<rating>[:<type>][,...] with a rating from 0 to 5000 and a "+
				"type of %s", part, strings.Join(store.RatingModes, ", "))
		}
		out = append(out, testRating{login, v, mode})
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
		_, acct, err := seedPersona(st, r.login, historySeedPassword, now)
		if err != nil {
			return "", err
		}
		if _, err := st.Update(acct.UserID, func(a *store.Account) {
			a.History.SetRatingIn(r.mode, store.Rating{Value: r.value, RD: testRatingRD, Vol: store.StartRating.Vol,
				Games: testRatingGames, Wins: testRatingGames / 2})
		}); err != nil {
			return "", err
		}
		what := fmt.Sprintf("%s (account %d) %g", r.login, acct.UserID, r.value)
		if r.mode != store.ModeStandard {
			what += " " + r.mode
		}
		done = append(done, what)
	}
	return "ratings set: " + strings.Join(done, ", "), nil
}
