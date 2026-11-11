package dgserver

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/blaze"
	"dawngate/server/internal/store"
)

// The test aid --test-preferred-region <login name>[|<password>]=<region>[,...] (PREF3): at the start each named
// account gets that Preferred Region stored, the account setting the UI channel's region.set writes, so a session can
// create a game in another region without the play screen's dropdown (PREF2's class). A missing account is made, with
// the password's hash when one is given (the session's -Login password: a login refuses an account without one); an
// existing one keeps its password. The region isn't checked against the server's: an unknown one counts as the home
// region, as a stale stored one does. A test aid only: it writes over the stored preference at every start.

// testPreferred is one --test-preferred-region entry.
type testPreferred struct{ login, password, region string }

// parseTestPreferred reads <login name>[|<password>]=<region>[,...] (a region alias of 2-8 letters).
func parseTestPreferred(spec string) ([]testPreferred, error) {
	var out []testPreferred
	for _, part := range strings.Split(spec, ",") {
		who, region, ok := strings.Cut(strings.TrimSpace(part), "=")
		login, password, _ := strings.Cut(who, "|")
		login, region = accounts.NormalizeName(login), strings.ToLower(strings.TrimSpace(region))
		if !ok || login == "" || len(region) < 2 || len(region) > 8 || strings.Trim(region, "abcdefghijklmnopqrstuvwxyz") != "" {
			return nil, fmt.Errorf("%q: want <login name>[|<password>]=<region>[,...] with a region alias of 2-8 letters", part)
		}
		out = append(out, testPreferred{login, password, region})
	}
	return out, nil
}

// testPreferredAccount is the entry's account, made with its password's hash when missing.
func testPreferredAccount(st *store.Store, p testPreferred, now time.Time) (store.Account, error) {
	hash := ""
	if p.password != "" {
		h, err := accounts.Hash(p.password)
		if err != nil {
			return store.Account{}, err
		}
		hash = h
	}
	a, err := st.Create(store.NewAccount{Name: p.login, Hash: hash}, now)
	if errors.Is(err, store.ErrExists) {
		acc, _, ok := st.PersonaByName(store.PersonaName(p.login))
		if !ok {
			return store.Account{}, fmt.Errorf("account %s exists, but its persona %q wasn't found", p.login, store.PersonaName(p.login))
		}
		return acc, nil
	}
	return a, err
}

// applyTestPreferred stores the preferences of spec and returns the log line's text.
func applyTestPreferred(st *store.Store, spec string, now time.Time) (string, error) {
	ps, err := parseTestPreferred(spec)
	if err != nil {
		return "", err
	}
	var done []string
	for _, p := range ps {
		acct, err := testPreferredAccount(st, p, now)
		if err != nil {
			return "", err
		}
		if _, err := st.Update(acct.UserID, func(a *store.Account) {
			if a.Settings == nil {
				a.Settings = map[string]string{}
			}
			a.Settings[blaze.SettingPreferredRegion] = p.region
		}); err != nil {
			return "", err
		}
		done = append(done, fmt.Sprintf("%s (account %d) %s", p.login, acct.UserID, p.region))
	}
	return "Preferred Regions set: " + strings.Join(done, ", "), nil
}
