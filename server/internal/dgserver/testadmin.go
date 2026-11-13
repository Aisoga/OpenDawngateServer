package dgserver

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/store"
)

// --test-admin (SYSCHAT's test aid): a test session's admin for the website's /admin page without a command run
// before it (--create-account prints a random password, --make-admin needs the account first). The account is made
// with the given password when missing (an existing password stays, as --test-history-seed does), then gets the admin
// role, with an audit log entry. Refused while the limits are on, i.e. on a public address: a known password must never
// make an admin there.

// applyTestAdmin applies spec (<login name>[|<password>], default historySeedPassword) and returns the log line's text.
func applyTestAdmin(st *store.Store, spec string, limitsOn bool, now time.Time) (string, error) {
	if st == nil {
		return "", errors.New("no account store")
	}
	if limitsOn {
		return "", errors.New("refused: the limits are on (a public address); make an admin with --make-admin")
	}
	login, password, withPassword := strings.Cut(strings.TrimSpace(spec), "|")
	login = accounts.NormalizeName(login)
	if login == "" || (withPassword && password == "") {
		return "", fmt.Errorf("%q: want <login name>[|<password>]", spec)
	}
	if !withPassword {
		password = historySeedPassword
	}
	if _, _, err := seedPersona(st, login, password, now); err != nil {
		return "", err
	}
	cr, err := accounts.SetAdmin(st, login, true)
	if err != nil {
		return "", err
	}
	target := accounts.AccountTarget(cr.UserID, cr.Name)
	if err := accounts.Audit(st, accounts.Actor{Source: "test aid"}, "account role", target,
		fmt.Sprintf("%s -> %s (--test-admin)", cr.Role, store.RoleAdmin), now); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s is an admin (was %s)", target, cr.Role), nil
}
