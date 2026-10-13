// Package accounts is the login check and the sign-up rules: passwords as argon2id hashes
// (golang.org/x/crypto/argon2, ADR-4), the login-name and password rules, the sign-up modes (open, invite, closed),
// invite codes, the cap on accounts made per source address and day, and the operator's account commands. The store
// (internal/store) keeps the values; Blaze's Authentication::login (internal/blaze) calls Policy.Login, and the website
// will call Policy.Register.
package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"dawngate/server/internal/store"
)

// Mode is the sign-up mode (Start-Server.ini signup, dgserver --signup).
type Mode int

// Sign-up modes.
const (
	// Open: the first login with a new login name creates the account, with the password typed (as before, now
	// with the rules); an account of the earlier builds without a password gets the one of its next login.
	Open Mode = iota
	// Invite: new accounts only with an invite code from the operator, on the website; the game's login never
	// creates one (its form has no field for a code).
	Invite
	// Closed: only the operator creates accounts (dgserver --create-account).
	Closed
)

var modeNames = [...]string{"open", "invite", "closed"}

func (m Mode) String() string {
	if int(m) < len(modeNames) {
		return modeNames[m]
	}
	return fmt.Sprintf("mode%d", int(m))
}

// ParseMode reads a sign-up mode's name.
func ParseMode(s string) (Mode, error) {
	for i, n := range modeNames {
		if strings.EqualFold(strings.TrimSpace(s), n) {
			return Mode(i), nil
		}
	}
	return 0, fmt.Errorf("%q is not a sign-up mode (open, invite, closed)", s)
}

// DefaultSignupsPerAddress is how many accounts one public source address may make per UTC day by default.
const DefaultSignupsPerAddress = 3

// Policy is the server's sign-up policy. The zero value is open sign-up without a cap.
type Policy struct {
	Mode Mode
	// Live, when set, is the sign-up mode as the running server has it now (the admin page changes it); it
	// overrides Mode. Policy is copied by value (Blaze, the website), Live is shared.
	Live *ModeSetting
	// SignupsPerAddress caps the accounts made per source address and UTC day, for public addresses only (loopback and
	// private LAN addresses are never counted: tests and home play make many); 0 = no cap.
	SignupsPerAddress int
}

// CurrentMode is the sign-up mode in force: Live's when set, else Mode.
func (p Policy) CurrentMode() Mode {
	if p.Live != nil {
		return p.Live.Get()
	}
	return p.Mode
}

// ModeSetting is a sign-up mode that a running server changes (the admin page) while logins read it.
type ModeSetting struct{ v atomic.Int32 }

// NewModeSetting returns a setting holding m.
func NewModeSetting(m Mode) *ModeSetting {
	s := &ModeSetting{}
	s.Set(m)
	return s
}

// Get returns the mode.
func (s *ModeSetting) Get() Mode { return Mode(s.v.Load()) }

// Set changes the mode.
func (s *ModeSetting) Set(m Mode) { s.v.Store(int32(m)) }

// Result is the outcome of a login or a registration.
type Result struct {
	Account store.Account
	First   bool   // the account was made by this call
	Code    uint16 // 0 = done; else the Blaze error code to refuse it with (the Code* constants)
	Note    string // what happened, for the server log (never the password)
	// Field names the input a refusal is about, for the website's form: FieldName, FieldDisplay,
	// FieldPassword, FieldCode, or "" (none in particular: the mode, the address's cap, the store).
	Field string
}

// The inputs a Result.Field names.
const (
	FieldName     = "name"
	FieldDisplay  = "display"
	FieldPassword = "password"
	FieldCode     = "code"
	FieldNew      = "new" // the new password (ChangePassword)
)

func refuse(code uint16, format string, a ...any) Result {
	return Result{Code: code, Note: fmt.Sprintf(format, a...)}
}

// refuseField is refuse for one input.
func refuseField(field string, code uint16, format string, a ...any) Result {
	r := refuse(code, format, a...)
	r.Field = field
	return r
}

// Counted reports whether sign-ups from addr count against Policy.SignupsPerAddress: a public address (not loopback,
// private, link-local or unspecified). An invalid address (unknown source) counts as "unknown".
func Counted(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = addr.Unmap()
	return !(addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsUnspecified())
}

func addrKey(addr netip.Addr) string {
	if !addr.IsValid() {
		return "unknown"
	}
	return addr.Unmap().String()
}

// Login checks a login of the game (Blaze Authentication::login's MAIL and PASS) from the source address from, at now:
//
//   - a name or password longer than the rules allow is refused at once (CodeTooLong), an empty password with
//     CodeInvalidPassword (the client sends an empty one when LOGIN is pressed again after a refusal);
//   - a known name: its password is checked against the hash (CodeInvalidPassword when wrong), then its status
//     (disabled, banned, deleted). An account without a hash (made by an earlier build) takes the password of this login as its
//     own under Open, if that keeps the password rules; otherwise it is refused (CodeInvalidPassword) until the
//     operator sets one (--reset-password);
//   - an unknown name is CodeInvalidUser under Invite and Closed (the same text as a wrong password), and under Open
//     becomes a new account when the name and the password keep the rules and the address hasn't made its accounts
//     for today (CodeBusy).
//
// A login that verified a hash made with older parameters stores a new one. Unknown names take as long as a known one
// (a dummy check), so the time doesn't tell which names exist.
func (p Policy) Login(st *store.Store, name, password string, from netip.Addr, now time.Time) Result {
	name = NormalizeName(name)
	if len(name) > MaxInput {
		return refuse(CodeTooLong, "login name of %d bytes", len(name))
	}
	if len(password) > MaxPassword {
		return refuse(CodeTooLong, "password longer than %d bytes", MaxPassword)
	}
	if password == "" {
		return refuse(CodeInvalidPassword, "empty password")
	}
	cred, found, err := st.Credentials(name)
	if err != nil {
		return refuse(CodeSystem, "account store: %v", err)
	}
	if !found {
		Verify(dummyHash(), password)
		if mode := p.CurrentMode(); mode != Open {
			return refuse(CodeInvalidUser, "no account %q (sign-up %s: new accounts don't come from the game's login)", name, mode)
		}
		return p.create(st, name, "", password, from, "", now)
	}
	note := ""
	switch {
	case cred.Hash != "":
		if !Verify(cred.Hash, password) {
			return refuse(CodeInvalidPassword, "account %d: wrong password", cred.UserID)
		}
		if cred.Status == store.StatusActive && NeedsRehash(cred.Hash) {
			if h, err := Hash(password); err == nil && st.SetPassword(cred.UserID, h) == nil {
				note = "; its password hash renewed with the current parameters"
			}
		}
	case cred.Status != store.StatusActive:
		// no password to check: the status below says no anyway
	case p.CurrentMode() != Open:
		return refuse(CodeInvalidPassword, "account %d has no password yet (made by an earlier build); under sign-up %s the operator sets one (--reset-password)", cred.UserID, p.CurrentMode())
	default:
		if code := CheckPassword(password); code != 0 {
			return refuse(code, "account %d has no password yet and this one breaks the rules (%d characters)", cred.UserID, len([]rune(password)))
		}
		h, err := Hash(password)
		if err == nil {
			err = st.SetPassword(cred.UserID, h)
		}
		if err != nil {
			return refuse(CodeSystem, "account %d: storing its first password: %v", cred.UserID, err)
		}
		note = "; it had no password (made by an earlier build): this login's is now its password"
	}
	switch cred.Status {
	case store.StatusActive:
	case store.StatusDisabled:
		return refuse(CodeUserInactive, "account %d is disabled", cred.UserID)
	case store.StatusBanned:
		return refuse(CodeBanned, "account %d is banned", cred.UserID)
	default:
		return refuse(CodeDeactivated, "account %d is deleted (%s)", cred.UserID, cred.Status)
	}
	acct, err := st.LoginUser(cred.UserID, now)
	if err != nil {
		return refuse(CodeSystem, "account %d: %v", cred.UserID, err)
	}
	return Result{Account: acct, Note: fmt.Sprintf("account %d logged in%s", cred.UserID, note)}
}

// Register makes an account from the website: the login name, the display name ("" = made from the login name,
// as a login's sign-up makes it; else it must keep CheckDisplayName's rules and be free), the password (typed twice
// there) and, under Invite, an invite code. Under Open the code is optional (a given one must be valid and is used),
// under Closed registration is refused. The same rules and the same per-address cap as a login's sign-up apply.
func (p Policy) Register(st *store.Store, name, display, password, code string, from netip.Addr, now time.Time) Result {
	name = NormalizeName(name)
	display = strings.TrimSpace(display)
	mode := p.CurrentMode()
	if mode == Closed {
		return refuse(CodeInvalidUser, "sign-up is closed")
	}
	code = NormalizeCode(code)
	if mode == Invite && code == "" {
		return refuseField(FieldCode, CodeInvalidCode, "sign-up needs an invite code")
	}
	return p.create(st, name, display, password, from, code, now)
}

// create makes a new account after the rules (the name, the display name, the password, the per-address cap, the
// invite code).
func (p Policy) create(st *store.Store, name, display, password string, from netip.Addr, code string, now time.Time) Result {
	if c := CheckName(name); c != 0 {
		return refuseField(FieldName, c, "new login name %q breaks the rules", name)
	}
	if display != "" {
		if c := CheckDisplayName(display); c != 0 {
			return refuseField(FieldDisplay, c, "new account %q: the display name %q breaks the rules", name, display)
		}
	}
	if c := CheckPassword(password); c != 0 {
		return refuseField(FieldPassword, c, "new account %q: the password breaks the rules (%d characters)", name, len([]rune(password)))
	}
	h, err := Hash(password)
	if err != nil {
		return refuse(CodeSystem, "hashing: %v", err)
	}
	n := store.NewAccount{Name: name, Hash: h, Persona: display}
	if p.SignupsPerAddress > 0 && Counted(from) {
		n.From, n.Limit = addrKey(from), p.SignupsPerAddress
	}
	if code != "" {
		n.InviteHash = CodeHash(code)
	}
	acct, err := st.Create(n, now)
	switch {
	case errors.Is(err, store.ErrExists): // the website's registration, or a login's sign-up racing another
		return refuseField(FieldName, CodeExists, "login name %q is taken", name)
	case errors.Is(err, store.ErrPersonaTaken):
		return refuseField(FieldDisplay, CodeExists, "new account %q: the display name %q is taken", name, display)
	case errors.Is(err, store.ErrSignupLimit):
		return refuse(CodeBusy, "new account %q: %s has made %d accounts today", name, addrKey(from), p.SignupsPerAddress)
	case errors.Is(err, store.ErrInvite):
		c := CodeInvalidCode
		if strings.HasSuffix(err.Error(), "used up") {
			c = CodeCodeUsed
		}
		return refuseField(FieldCode, c, "new account %q: %v", name, err)
	case err != nil:
		return refuse(CodeSystem, "new account %q: %v", name, err)
	}
	how := "open sign-up"
	if code != "" {
		how = "invite code"
	}
	return Result{Account: acct, First: true, Note: fmt.Sprintf("account %d created for %q (%s)", acct.UserID, name, how)}
}

// ---- the operator's commands (dgserver --create-account, --reset-password, --account-status, --make-invite) ----

// passwordAlphabet has no look-alike characters (no 0/O, 1/l/I), so a password read out loud or copied by hand works.
const passwordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewPassword returns a random password of 16 characters (about 93 bits) for the operator to hand to a player.
func NewPassword() string { return randomString(passwordAlphabet, 16) }

func randomString(alphabet string, n int) string {
	b := make([]byte, n)
	// rand.Read never fails on supported platforms (Go 1.24); a short alphabet keeps the modulo bias negligible
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// CreateAccount makes an account for the operator (any sign-up mode): the name must keep the rules; the password is a
// new random one (NewPassword), returned to be handed to the player.
func CreateAccount(st *store.Store, name string, now time.Time) (store.Account, string, error) {
	name = NormalizeName(name)
	if c := CheckName(name); c != 0 {
		return store.Account{}, "", fmt.Errorf("%q is no valid login name: %d to %d of A-Z a-z 0-9 . _ - @", name, MinName, MaxName)
	}
	pw := NewPassword()
	h, err := Hash(pw)
	if err != nil {
		return store.Account{}, "", err
	}
	a, err := st.Create(store.NewAccount{Name: name, Hash: h}, now)
	if errors.Is(err, store.ErrExists) {
		return store.Account{}, "", fmt.Errorf("an account %q exists already (--reset-password gives it a new password)", name)
	}
	return a, pw, err
}

// ResetPassword gives the account with this login name a new random password and returns it (the operator's reset of a
// forgotten password: the server never sends mail). The old one stops working at once.
func ResetPassword(st *store.Store, name string) (store.Credentials, string, error) {
	c, err := lookup(st, name)
	if err != nil {
		return c, "", err
	}
	pw := NewPassword()
	h, err := Hash(pw)
	if err != nil {
		return c, "", err
	}
	return c, pw, st.SetPassword(c.UserID, h)
}

// SetStatus sets the account's status (active, disabled, banned, deleted).
func SetStatus(st *store.Store, name string, s store.Status) (store.Credentials, error) {
	c, err := lookup(st, name)
	if err != nil {
		return c, err
	}
	return c, st.SetStatus(c.UserID, s)
}

func lookup(st *store.Store, name string) (store.Credentials, error) {
	c, found, err := st.Credentials(NormalizeName(name))
	if err != nil {
		return c, err
	}
	if !found {
		return c, fmt.Errorf("no account %q", NormalizeName(name))
	}
	return c, nil
}

// codeAlphabet is Crockford's base32 (no I, L, O, U): codes are read and typed by people.
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewInvite makes an invite code good for uses accounts for valid (0 = never expires), stores its hash and returns the
// code (shown once: the store keeps only its SHA-256) as XXXX-XXXX-XXXX-XXXX (80 bits).
func NewInvite(st *store.Store, uses int, valid time.Duration, note string, now time.Time) (string, store.Invite, error) {
	raw := randomString(codeAlphabet, 16)
	code := raw[0:4] + "-" + raw[4:8] + "-" + raw[8:12] + "-" + raw[12:16]
	var expires time.Time
	if valid > 0 {
		expires = now.Add(valid).UTC()
	}
	inv, err := st.AddInvite(CodeHash(code), uses, expires, note, now)
	return code, inv, err
}

// NormalizeCode is an invite code as it is hashed: upper case, without blanks and dashes, with Crockford's read-alikes
// (O as 0, I and L as 1).
func NormalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch {
		case r == '-' || r == ' ' || r == '\t':
		case r == 'O':
			b.WriteByte('0')
		case r == 'I' || r == 'L':
			b.WriteByte('1')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CodeHash is the SHA-256 (hex) the store keeps of an invite code.
func CodeHash(code string) string {
	sum := sha256.Sum256([]byte(NormalizeCode(code)))
	return hex.EncodeToString(sum[:])
}
