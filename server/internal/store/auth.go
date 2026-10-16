package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// This file holds what the password check and sign-up need from the database (schema versions 2 and 3). The rules
// themselves (the hash, the login-name rules, the sign-up modes) are internal/accounts'; the store only keeps the
// values and makes each step one transaction.

// Status is an account's state (accounts.status).
type Status int

// Account states. A login of an account that isn't active is refused with the matching Blaze error
// (internal/accounts).
const (
	StatusActive   Status = 0
	StatusDisabled Status = 1 // by the operator (AUTH_ERR_USER_INACTIVE)
	StatusBanned   Status = 2 // by the operator (AUTH_ERR_BANNED)
	StatusDeleted  Status = 3 // deleted by its player (AUTH_ERR_DEACTIVATED)
)

var statusNames = map[Status]string{StatusActive: "active", StatusDisabled: "disabled", StatusBanned: "banned", StatusDeleted: "deleted"}

func (s Status) String() string {
	if n, ok := statusNames[s]; ok {
		return n
	}
	return fmt.Sprintf("status%d", int(s))
}

// ParseStatus reads a status name (active, disabled, banned, deleted).
func ParseStatus(name string) (Status, error) {
	for s, n := range statusNames {
		if strings.EqualFold(strings.TrimSpace(name), n) {
			return s, nil
		}
	}
	return 0, fmt.Errorf("%q is not an account status (active, disabled, banned, deleted)", name)
}

// LocalTrustHash is the password column of an account that This PC's server made without a password (accounts.Policy's
// LocalTrust). The earlier builds' accounts without one have "", and no PHC string looks like it.
const LocalTrustHash = "local-trust"

// Credentials are what a login checks: the account's id, its login name as stored, its password hash ("" = none yet,
// LocalTrustHash = made by This PC without one),
// its status and its role.
type Credentials struct {
	UserID int64
	Name   string
	Hash   string
	Status Status
	Role   Role
}

// Credentials returns the login data of the account with this login name (compared as Login compares it: trimmed,
// case-insensitively). found is false when there is none.
func (s *Store) Credentials(name string) (c Credentials, found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.db.QueryRow(`SELECT user_id, email, password, status, role FROM accounts WHERE email_key = ?`, emailKey(name)).
		Scan(&c.UserID, &c.Name, &c.Hash, &c.Status, &c.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, false, nil
	}
	return c, err == nil, err
}

// LoginUser is a login of an existing account that has been checked: it returns the account as it was before this
// login (LastLogin = the previous one) and stores now as its last login.
func (s *Store) LoginUser(userID int64, now time.Time) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	a, err := load(tx, userID)
	if err != nil {
		return Account{}, err
	}
	if _, err := tx.Exec(`UPDATE accounts SET last_login = ? WHERE user_id = ?`, now.Format(timeFormat), userID); err != nil {
		return Account{}, err
	}
	return *a, tx.Commit()
}

// SetPassword stores a new password hash for the account ("" removes it: the account then has none).
func (s *Store) SetPassword(userID int64, hash string) error {
	return s.exec1(`UPDATE accounts SET password = ? WHERE user_id = ?`, hash, userID)
}

// SetStatus changes the account's status.
func (s *Store) SetStatus(userID int64, st Status) error {
	if _, ok := statusNames[st]; !ok {
		return fmt.Errorf("store: unknown status %d", int(st))
	}
	return s.exec1(`UPDATE accounts SET status = ? WHERE user_id = ?`, int(st), userID)
}

// exec1 runs an UPDATE that must change exactly one account (ErrNoAccount otherwise).
func (s *Store) exec1(query string, args ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrNoAccount
	}
	return nil
}

// Sign-up refusals of Create.
var (
	ErrExists      = errors.New("store: the login name is taken")
	ErrSignupLimit = errors.New("store: this address has made its accounts for today")
	ErrInvite      = errors.New("store: invite code") // wrapped with the reason: unknown, used up, expired, revoked
	// ErrPersonaTaken is a chosen display name (NewAccount.Persona) that another persona has.
	ErrPersonaTaken = errors.New("store: the display name is taken")
)

// NewAccount is an account to create (Create).
type NewAccount struct {
	Name string // the login name (Account.Email), already checked against the login-name rules
	Hash string // its password hash ("" = none)
	// From is the source address the sign-up counts against ("" = not counted) and Limit the most accounts it may make
	// per UTC day (0 = no limit).
	From  string
	Limit int
	// InviteHash is the SHA-256 of the invite code the sign-up uses ("" = none); one use of it is taken.
	InviteHash string
	// Persona is the display name the player chose (the website's registration; already checked against
	// accounts.CheckDisplayName): it must not be another persona's (ErrPersonaTaken). "" = made from the login name
	// (PersonaName, numbered when taken), as a login's sign-up makes it.
	Persona string
}

// Create makes a new account with one persona (named after the login name, as Login names it), in one transaction
// with its checks: ErrExists when the name is taken, ErrSignupLimit when From has made Limit accounts today, ErrInvite
// (wrapped) when the invite code is unknown, used up, expired or revoked. Nothing is stored on a refusal. It returns
// the new account.
func (s *Store) Create(n NewAccount, now time.Time) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	var one int
	switch err := tx.QueryRow(`SELECT 1 FROM accounts WHERE email_key = ?`, emailKey(n.Name)).Scan(&one); {
	case err == nil:
		return Account{}, ErrExists
	case !errors.Is(err, sql.ErrNoRows):
		return Account{}, err
	}
	if n.Persona != "" {
		if taken, err := personaTaken(tx, n.Persona); err != nil {
			return Account{}, err
		} else if taken {
			return Account{}, ErrPersonaTaken
		}
	}
	day := now.UTC().Format("2006-01-02")
	if n.From != "" {
		if _, err := tx.Exec(`DELETE FROM signups WHERE day <> ?`, day); err != nil {
			return Account{}, err
		}
		var count int
		if err := tx.QueryRow(`SELECT count FROM signups WHERE day = ? AND addr = ?`, day, n.From).Scan(&count); err != nil &&
			!errors.Is(err, sql.ErrNoRows) {
			return Account{}, err
		}
		if n.Limit > 0 && count >= n.Limit {
			return Account{}, ErrSignupLimit
		}
		if _, err := tx.Exec(`INSERT INTO signups (day, addr, count) VALUES (?, ?, 1)
			ON CONFLICT (day, addr) DO UPDATE SET count = count + 1`, day, n.From); err != nil {
			return Account{}, err
		}
	}
	if n.InviteHash != "" {
		if err := useInvite(tx, n.InviteHash, now); err != nil {
			return Account{}, err
		}
	}
	userID, err := createAccount(tx, n.Name, n.Persona, n.Hash, now)
	if err != nil {
		return Account{}, err
	}
	a, err := load(tx, userID)
	if err != nil {
		return Account{}, err
	}
	return *a, tx.Commit()
}

// createAccount inserts a new account named name (trimmed) with one persona and the password hash, and advances the
// id counters. It returns the user id. The persona is display when given (the caller checked that it is free), else
// PersonaName(name), made unique.
func createAccount(tx *sql.Tx, name, display, hash string, now time.Time) (int64, error) {
	nextUser, nextPersona, err := counters(tx)
	if err != nil {
		return 0, err
	}
	persona := display
	if persona == "" {
		if persona, err = uniqueName(tx, PersonaName(name)); err != nil {
			return 0, err
		}
	}
	a := &Account{UserID: nextUser, Email: strings.TrimSpace(name), Created: now, Level: 1,
		Personas: []Persona{{ID: nextPersona, Name: persona}}}
	if err := insertAccount(tx, a); err != nil {
		return 0, err
	}
	if hash != "" {
		if _, err := tx.Exec(`UPDATE accounts SET password = ? WHERE user_id = ?`, hash, a.UserID); err != nil {
			return 0, err
		}
	}
	if err := setCounters(tx, nextUser+1, nextPersona+1); err != nil {
		return 0, err
	}
	return a.UserID, nil
}

// Invite is one invite code as the store keeps it (the code itself isn't kept, only its SHA-256).
type Invite struct {
	ID      int64
	Uses    int       // how many accounts it may make
	Used    int       // how many it made
	Created time.Time //
	Expires time.Time // zero = never
	Revoked bool
	Note    string
}

// Left is how many more accounts the code may make now (0 when expired or revoked).
func (i Invite) Left(now time.Time) int {
	if i.Revoked || (!i.Expires.IsZero() && !now.Before(i.Expires)) {
		return 0
	}
	return max(i.Uses-i.Used, 0)
}

// AddInvite stores a new invite code by its SHA-256 (codeHash), good for uses accounts until expires (zero = never).
func (s *Store) AddInvite(codeHash string, uses int, expires time.Time, note string, now time.Time) (Invite, error) {
	if uses < 1 {
		return Invite{}, errors.New("store: an invite code needs at least one use")
	}
	exp := ""
	if !expires.IsZero() {
		exp = expires.UTC().Format(timeFormat)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`INSERT INTO invites (code_hash, uses, created, expires, note) VALUES (?, ?, ?, ?, ?)`,
		codeHash, uses, now.UTC().Format(timeFormat), exp, note)
	if err != nil {
		return Invite{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Invite{}, err
	}
	return Invite{ID: id, Uses: uses, Created: now.UTC(), Expires: expires, Note: note}, nil
}

// Invites returns every invite code, oldest first.
func (s *Store) Invites() ([]Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, uses, used, created, expires, revoked, note FROM invites ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		var i Invite
		var created, expires string
		var revoked int
		if err := rows.Scan(&i.ID, &i.Uses, &i.Used, &created, &expires, &revoked, &i.Note); err != nil {
			return nil, err
		}
		if i.Created, err = time.Parse(timeFormat, created); err != nil {
			return nil, err
		}
		if expires != "" {
			if i.Expires, err = time.Parse(timeFormat, expires); err != nil {
				return nil, err
			}
		}
		i.Revoked = revoked != 0
		out = append(out, i)
	}
	return out, rows.Err()
}

// RevokeInvite makes the invite code with this id unusable.
func (s *Store) RevokeInvite(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE invites SET revoked = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("store: no invite code %d", id)
	}
	return nil
}

// useInvite takes one use of the invite code with this hash, or says why it can't.
func useInvite(tx *sql.Tx, codeHash string, now time.Time) error {
	var i Invite
	var expires string
	var revoked int
	err := tx.QueryRow(`SELECT id, uses, used, expires, revoked FROM invites WHERE code_hash = ?`, codeHash).
		Scan(&i.ID, &i.Uses, &i.Used, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: unknown", ErrInvite)
	}
	if err != nil {
		return err
	}
	if expires != "" {
		if i.Expires, err = time.Parse(timeFormat, expires); err != nil {
			return err
		}
	}
	i.Revoked = revoked != 0
	switch {
	case i.Revoked:
		return fmt.Errorf("%w: revoked", ErrInvite)
	case !i.Expires.IsZero() && !now.Before(i.Expires):
		return fmt.Errorf("%w: expired", ErrInvite)
	case i.Used >= i.Uses:
		return fmt.Errorf("%w: used up", ErrInvite)
	}
	_, err = tx.Exec(`UPDATE invites SET used = used + 1 WHERE id = ?`, i.ID)
	return err
}
