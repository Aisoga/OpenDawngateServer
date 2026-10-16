package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file holds what the admin page needs from the database (schema version 4): the admin role, the
// account search, the audit log and the server's settings. The rules (who may do what, what an action writes into the
// audit log) are internal/accounts' and internal/site's.

// Role is an account's role (accounts.role).
type Role int

// Roles. An admin logs into the website's /admin pages with its login name and password.
const (
	RolePlayer Role = 0
	RoleAdmin  Role = 1 // dgserver --make-admin
)

func (r Role) String() string {
	switch r {
	case RolePlayer:
		return "player"
	case RoleAdmin:
		return "admin"
	}
	return fmt.Sprintf("role%d", int(r))
}

// CredentialsByID returns the login data of the account with this user id (found false when there is none).
func (s *Store) CredentialsByID(userID int64) (c Credentials, found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.db.QueryRow(`SELECT user_id, email, password, status, role FROM accounts WHERE user_id = ?`, userID).
		Scan(&c.UserID, &c.Name, &c.Hash, &c.Status, &c.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, false, nil
	}
	return c, err == nil, err
}

// Statuses returns the status of each of these accounts (the running server's check of the logged-in accounts,
// since the operator's --account-status runs in another process). An id without an account is missing from the map.
func (s *Store) Statuses(userIDs []int64) (map[int64]Status, error) {
	out := make(map[int64]Status, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	q := `SELECT user_id, status FROM accounts WHERE user_id IN (?` + strings.Repeat(`, ?`, len(userIDs)-1) + `)`
	s.mu.Lock()
	defer s.mu.Unlock()
	err := each(s.db, func(r *sql.Rows) error {
		var id int64
		var st Status
		if err := r.Scan(&id, &st); err != nil {
			return err
		}
		out[id] = st
		return nil
	}, q, args...)
	return out, err
}

// SetRole changes the account's role.
func (s *Store) SetRole(userID int64, r Role) error {
	if r != RolePlayer && r != RoleAdmin {
		return fmt.Errorf("store: unknown role %d", int(r))
	}
	return s.exec1(`UPDATE accounts SET role = ? WHERE user_id = ?`, int(r), userID)
}

// Admins returns the accounts with the admin role (their credentials without the hash), by user id.
func (s *Store) Admins() ([]Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Credentials
	err := each(s.db, func(r *sql.Rows) error {
		var c Credentials
		if err := r.Scan(&c.UserID, &c.Name, &c.Status, &c.Role); err != nil {
			return err
		}
		out = append(out, c)
		return nil
	}, `SELECT user_id, email, status, role FROM accounts WHERE role = ? ORDER BY user_id`, int(RoleAdmin))
	return out, err
}

// AccountSummary is one account as the admin page lists it.
type AccountSummary struct {
	UserID    int64
	Name      string   // the login name ("#deleted-u<id>" for a deleted account)
	Personas  []string // the display names
	Status    Status
	Role      Role
	HasHash   bool // a password is set (an account of the earlier builds may have none yet)
	Level     int64
	Created   time.Time
	LastLogin time.Time
}

// FindAccounts searches the accounts: an all-digit query also matches the user id or a persona id exactly; otherwise
// (and also then) the login names and display names that contain the query (case-insensitively). An empty query
// lists the newest accounts. At most limit rows, newest first.
func (s *Store) FindAccounts(query string, limit int) ([]AccountSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	q := strings.ToLower(strings.TrimSpace(query))
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []int64
	collect := func(r *sql.Rows) error {
		var id int64
		if err := r.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	}
	var err error
	if q == "" {
		err = each(s.db, collect, `SELECT user_id FROM accounts ORDER BY user_id DESC LIMIT ?`, limit)
	} else {
		id, _ := strconv.ParseInt(q, 10, 64) // 0 when q isn't a number: no id is 0
		// instr instead of LIKE: no wildcard in the query needs escaping
		err = each(s.db, collect, `SELECT user_id FROM accounts WHERE user_id = ? OR instr(email_key, ?) > 0
			OR user_id IN (SELECT user_id FROM personas WHERE persona_id = ? OR instr(name_key, ?) > 0)
			ORDER BY user_id DESC LIMIT ?`, id, q, id, q, limit)
	}
	if err != nil {
		return nil, err
	}
	out := make([]AccountSummary, 0, len(ids))
	for _, id := range ids {
		a, err := summary(s.db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Summary returns one account as the admin page shows it.
func (s *Store) Summary(userID int64) (AccountSummary, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := summary(s.db, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountSummary{}, false, nil
	}
	return a, err == nil, err
}

func summary(q querier, userID int64) (AccountSummary, error) {
	a := AccountSummary{UserID: userID}
	var hash, created, last string
	if err := q.QueryRow(`SELECT email, password, status, role, level, created, last_login FROM accounts WHERE user_id = ?`,
		userID).Scan(&a.Name, &hash, &a.Status, &a.Role, &a.Level, &created, &last); err != nil {
		return a, err
	}
	a.HasHash = hash != "" && hash != LocalTrustHash
	a.Created, _ = parseTime(created)
	a.LastLogin, _ = parseTime(last)
	err := each(q, func(r *sql.Rows) error {
		var n string
		if err := r.Scan(&n); err != nil {
			return err
		}
		a.Personas = append(a.Personas, n)
		return nil
	}, `SELECT name FROM personas WHERE user_id = ? ORDER BY pos`, userID)
	return a, err
}

// AuditEntry is one line of the audit log: who did what to whom, from where. It never holds a password or an invite
// code.
type AuditEntry struct {
	ID     int64
	Time   time.Time
	Actor  string // "admin <login name> (account <id>)", "operator (command line)", ...
	Source string // "web <address>", "command line", "server start"
	Action string // e.g. "account status", "reset password", "invite create", "end match", "admin login"
	Target string // the account, invite or match acted on ("" = none)
	Detail string
}

// maxAuditField bounds what one field keeps (a typed login name in a refused admin login is anyone's input).
const maxAuditField = 512

func clip(s string) string {
	if len(s) > maxAuditField {
		return s[:maxAuditField] + "…"
	}
	return s
}

// Audit appends an entry to the audit log (its Time; zero = now).
func (s *Store) Audit(e AuditEntry) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO audit (time, actor, source, action, target, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		e.Time.UTC().Format(timeFormat), clip(e.Actor), clip(e.Source), clip(e.Action), clip(e.Target), clip(e.Detail))
	return err
}

// AuditLog returns the newest entries of the audit log, newest first: at most limit of them, older than the entry
// before (0 = from the newest).
func (s *Store) AuditLog(limit int, before int64) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	if before <= 0 {
		before = 1 << 62
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AuditEntry
	err := each(s.db, func(r *sql.Rows) error {
		var e AuditEntry
		var t string
		if err := r.Scan(&e.ID, &t, &e.Actor, &e.Source, &e.Action, &e.Target, &e.Detail); err != nil {
			return err
		}
		e.Time, _ = parseTime(t)
		out = append(out, e)
		return nil
	}, `SELECT id, time, actor, source, action, target, detail FROM audit WHERE id < ? ORDER BY id DESC LIMIT ?`, before, limit)
	return out, err
}

// Setting returns a server setting (found false when it was never set).
func (s *Store) Setting(key string) (value string, changed time.Time, found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c string
	err = s.db.QueryRow(`SELECT value, changed FROM server_settings WHERE key = ?`, key).Scan(&value, &c)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	changed, _ = parseTime(c)
	return value, changed, true, nil
}

// SetSetting stores a server setting.
func (s *Store) SetSetting(key, value string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO server_settings (key, value, changed) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, changed = excluded.changed`, key, value,
		now.UTC().Format(timeFormat))
	return err
}
