package accounts

import (
	"fmt"
	"time"

	"dawngate/server/internal/store"
)

// The player's self-service on the website: every action names the account by its login name and is
// confirmed by its password (the site keeps no login session), with the same check as a game login: argon2id against
// the stored hash, a dummy check for an unknown name (no enumeration by timing), and only an active account may act
// (a disabled or banned one can't delete itself out of the operator's reach, nor read its data around the ban).

// Authenticate checks a login name and password for the website's self-service: the account's credentials, or a
// refusal (Code CodeInvalidUser/CodeInvalidPassword for a wrong pair, CodeUserInactive/CodeBanned/CodeDeactivated
// for an account that isn't active, CodeTooLong, CodeSystem). An account without a password (made by an earlier build) can't
// act here: it gets one at its next game login under Open, or from the operator.
func Authenticate(st *store.Store, name, password string) (store.Credentials, Result) {
	name = NormalizeName(name)
	if len(name) > MaxInput || len(password) > MaxPassword {
		return store.Credentials{}, refuse(CodeTooLong, "login name or password too long")
	}
	cred, found, err := st.Credentials(name)
	if err != nil {
		return cred, refuse(CodeSystem, "account store: %v", err)
	}
	if !found || cred.Hash == "" {
		Verify(dummyHash(), password)
		if !found {
			return cred, refuse(CodeInvalidUser, "no account %q", name)
		}
		return cred, refuse(CodeInvalidPassword, "account %d has no password yet", cred.UserID)
	}
	if !Verify(cred.Hash, password) {
		return cred, refuse(CodeInvalidPassword, "account %d: wrong password", cred.UserID)
	}
	switch cred.Status {
	case store.StatusActive:
		return cred, Result{}
	case store.StatusDisabled:
		return cred, refuse(CodeUserInactive, "account %d is disabled", cred.UserID)
	case store.StatusBanned:
		return cred, refuse(CodeBanned, "account %d is banned", cred.UserID)
	}
	return cred, refuse(CodeDeactivated, "account %d is deleted (%s)", cred.UserID, cred.Status)
}

// ChangePassword gives the account a new password chosen by its player (the website): the old one must be right and
// the new one keep the rules. The old one stops working at once.
func ChangePassword(st *store.Store, name, old, password string) Result {
	cred, r := Authenticate(st, name, old)
	if r.Code != 0 {
		return r
	}
	if c := CheckPassword(password); c != 0 {
		return refuseField(FieldNew, c, "account %d: the new password breaks the rules (%d characters)", cred.UserID, len([]rune(password)))
	}
	h, err := Hash(password)
	if err == nil {
		err = st.SetPassword(cred.UserID, h)
	}
	if err != nil {
		return refuse(CodeSystem, "account %d: storing the new password: %v", cred.UserID, err)
	}
	return Result{Note: fmt.Sprintf("account %d changed its password", cred.UserID)}
}

// DeleteAccount deletes the account by its player's wish (the website, confirmed by the password): its personal data
// goes, its match history stays without the name (store.DeleteAccount).
func DeleteAccount(st *store.Store, name, password string) Result {
	cred, r := Authenticate(st, name, password)
	if r.Code != 0 {
		return r
	}
	if err := st.DeleteAccount(cred.UserID); err != nil {
		return refuse(CodeSystem, "account %d: deleting: %v", cred.UserID, err)
	}
	// Account carries only the user id (the website ends the account's game sessions with it)
	return Result{Account: store.Account{UserID: cred.UserID},
		Note: fmt.Sprintf("account %d deleted by its player (personal data removed, match history kept without the name)", cred.UserID)}
}

// Export is what the website's "download your data" hands the player (JSON): everything the store keeps of the
// account except the password hash.
type Export struct {
	Exported time.Time     `json:"exported"`
	Server   string        `json:"server,omitempty"`
	Status   string        `json:"status"`
	Account  store.Account `json:"account"`
}

// ExportAccount returns the account's data for its player (confirmed by the password).
func ExportAccount(st *store.Store, name, password string, now time.Time) (Export, Result) {
	cred, r := Authenticate(st, name, password)
	if r.Code != 0 {
		return Export{}, r
	}
	a, ok := st.Account(cred.UserID)
	if !ok {
		return Export{}, refuse(CodeSystem, "account %d: not readable", cred.UserID)
	}
	return Export{Exported: now.UTC(), Status: cred.Status.String(), Account: a},
		Result{Account: a, Note: fmt.Sprintf("account %d downloaded its data", cred.UserID)}
}
