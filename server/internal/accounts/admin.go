package accounts

import (
	"errors"
	"fmt"
	"time"

	"dawngate/server/internal/store"
)

// The admin's side: the admin role, the actions of the admin page by account id, the sign-up mode a
// running server changes and keeps, and the audit log, which every admin action and every one of the operator's
// account commands writes (never a password or an invite code).

// Actor is who does an admin action, for the audit log.
type Actor struct {
	Name   string // the admin's login name ("" = the operator on the command line)
	UserID int64  // the admin's account (0 = none)
	Source string // where from: "web <address>", "command line", "server start"
}

// CommandLine is the operator's account commands (dgserver / Start-Server.exe).
var CommandLine = Actor{Source: "command line"}

func (a Actor) String() string {
	if a.UserID == 0 {
		return "operator"
	}
	return fmt.Sprintf("admin %s (account %d)", a.Name, a.UserID)
}

// Audit writes one entry of the audit log at now. A failure to write is returned (the caller logs it; the action
// stays done).
func Audit(st *store.Store, who Actor, action, target, detail string, now time.Time) error {
	return st.Audit(store.AuditEntry{Time: now, Actor: who.String(), Source: who.Source, Action: action, Target: target,
		Detail: detail})
}

// AccountTarget names an account in the audit log.
func AccountTarget(id int64, name string) string { return fmt.Sprintf("account %d %q", id, name) }

// SetAdmin gives the account with this login name the admin role (admin true) or takes it (dgserver --make-admin,
// --remove-admin). Only an active account can become an admin.
func SetAdmin(st *store.Store, name string, admin bool) (store.Credentials, error) {
	c, err := lookup(st, name)
	if err != nil {
		return c, err
	}
	role := store.RolePlayer
	if admin {
		role = store.RoleAdmin
		if c.Status != store.StatusActive {
			return c, fmt.Errorf("account %d %q is %s: enable it first (--account-status %s=active)", c.UserID, c.Name, c.Status, c.Name)
		}
	}
	return c, st.SetRole(c.UserID, role)
}

// ErrSelf refuses an admin's action on its own account that would lock it out (disable, ban, delete).
var ErrSelf = errors.New("not on your own account (it would lock you out; ask another admin or use the command line)")

// ErrDeleted refuses changing a deleted account (its personal data is gone; it can't come back).
var ErrDeleted = errors.New("the account is deleted")

// byID returns the account's credentials for an admin action.
func byID(st *store.Store, id int64) (store.Credentials, error) {
	c, found, err := st.CredentialsByID(id)
	if err != nil {
		return c, err
	}
	if !found {
		return c, fmt.Errorf("no account %d", id)
	}
	return c, nil
}

// SetStatusByID is the admin page's disable/enable/ban: who (an admin) may not change its own account, and a deleted
// account stays deleted (DeleteByID is the way to delete). It returns the account as it was.
func SetStatusByID(st *store.Store, who Actor, id int64, s store.Status) (store.Credentials, error) {
	c, err := byID(st, id)
	if err != nil {
		return c, err
	}
	switch {
	case c.Status == store.StatusDeleted:
		return c, ErrDeleted
	case s == store.StatusDeleted:
		return c, errors.New("delete an account with the delete action")
	case id == who.UserID && s != store.StatusActive:
		return c, ErrSelf
	}
	// an admin that isn't active can't log into the admin page (Authenticate); its role stays, so enabling it again
	// restores it
	return c, st.SetStatus(id, s)
}

// ResetPasswordByID gives the account a new random password (shown once to the admin) and returns it.
func ResetPasswordByID(st *store.Store, id int64) (store.Credentials, string, error) {
	c, err := byID(st, id)
	if err != nil {
		return c, "", err
	}
	if c.Status == store.StatusDeleted {
		return c, "", ErrDeleted
	}
	pw := NewPassword()
	h, err := Hash(pw)
	if err != nil {
		return c, "", err
	}
	return c, pw, st.SetPassword(id, h)
}

// DeleteByID deletes the account as its player's own deletion does (store.DeleteAccount: the personal data goes,
// the match history stays without the name). An admin can't delete its own account here.
func DeleteByID(st *store.Store, who Actor, id int64) (store.Credentials, error) {
	c, err := byID(st, id)
	if err != nil {
		return c, err
	}
	if c.Status == store.StatusDeleted {
		return c, ErrDeleted
	}
	if id == who.UserID {
		return c, ErrSelf
	}
	return c, st.DeleteAccount(id)
}

// The server settings that keep the sign-up mode (store.Setting).
const (
	// SettingSignup is the sign-up mode in force (set at the start or on the admin page).
	SettingSignup = "signup_mode"
	// SettingSignupFlag is the mode the server was started with last (--signup, Start-Server.ini's signup).
	SettingSignupFlag = "signup_mode_flag"
)

// StartMode decides the sign-up mode at a server's start: the most recent change wins. When the operator's setting
// (flag: --signup, which Start-Server passes from its ini's signup) differs from the one of the last start, it is a
// change and wins; otherwise a mode set on the admin page since then stays. It stores both and returns the mode and
// a note for the start-up line; a change from the flag is written into the audit log.
func StartMode(st *store.Store, flag Mode, now time.Time) (Mode, string, error) {
	lastFlag, _, hadFlag, err := st.Setting(SettingSignupFlag)
	if err != nil {
		return flag, "", err
	}
	cur, changed, hadCur, err := st.Setting(SettingSignup)
	if err != nil {
		return flag, "", err
	}
	if hadFlag && lastFlag == flag.String() && hadCur {
		if m, err := ParseMode(cur); err == nil {
			if m == flag {
				return m, "", nil
			}
			return m, fmt.Sprintf(" (set on the admin page %s; --signup/Start-Server.ini still says %s)",
				changed.UTC().Format("2006-01-02 15:04 UTC"), flag), nil
		}
	}
	if err := st.SetSetting(SettingSignupFlag, flag.String(), now); err != nil {
		return flag, "", err
	}
	if err := st.SetSetting(SettingSignup, flag.String(), now); err != nil {
		return flag, "", err
	}
	if hadCur && cur != flag.String() {
		Audit(st, Actor{Source: "server start"}, "sign-up mode", "", fmt.Sprintf("%s -> %s (--signup / Start-Server.ini)", cur, flag), now)
	}
	return flag, "", nil
}

// SetMode is the admin page's change of the sign-up mode: in force at once (live) and kept across restarts. It
// returns the mode before.
func SetMode(st *store.Store, live *ModeSetting, m Mode, now time.Time) (Mode, error) {
	before := live.Get()
	if err := st.SetSetting(SettingSignup, m.String(), now); err != nil {
		return before, err
	}
	live.Set(m)
	return before, nil
}
