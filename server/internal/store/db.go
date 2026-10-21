package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // the "sqlite" database/sql driver (pure Go, ADR-4)
)

// FileName is the database's name in dgserver's data folder.
const FileName = "accounts.db"

// LegacyFile is the JSON store of B7a-B13b that Open imports once, and ImportedSuffix what it is renamed to then.
const (
	LegacyFile     = "accounts.json"
	ImportedSuffix = ".imported"
)

// Store holds the accounts.
type Store struct {
	mu   sync.Mutex
	path string // the database file; "" = memory only
	db   *sql.DB
	info string // what Open found and did (Info)
}

// timeFormat is how times are stored: encoding/json's layout, so imported and new values read back alike.
const timeFormat = time.RFC3339Nano

// list names in list_members (the JSON keys of Lists)
const (
	listFriends = "friends"
	listRecent  = "recentPlayers"
	listBlocked = "blocked"
)

// New returns an empty store that lives in memory only (an SQLite database in memory, one connection).
func New() *Store {
	s, err := open("", ":memory:", time.Now())
	if err != nil {
		panic("store: in-memory database: " + err.Error()) // only a broken build gets here
	}
	return s
}

// Open returns the store kept in the SQLite database at path (dgserver: <data>\accounts.db), creating the database
// and its folder if needed and bringing its schema up to date (migrations). When the database holds no account yet and
// a JSON store of the earlier builds (LegacyFile) lies in the same folder, its accounts are imported and the file is
// renamed to accounts.json.imported (Info says so). A file that isn't such a database, a database from a newer build,
// or a JSON store that doesn't parse stops Open; none of them is changed.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: Open needs a path (New keeps the accounts in memory)")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return open(path, path, time.Now())
}

func open(path, name string, now time.Time) (*Store, error) {
	// one connection: every statement of the store goes through it (one writer; the store's lock serializes them anyway),
	// and an in-memory database lives exactly as long as that connection
	dsn := name + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if path != "" {
		dsn += "&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	fail := func(err error) (*Store, error) {
		db.Close()
		if path == "" {
			return nil, err
		}
		return nil, fmt.Errorf("store: %s: %v", path, err)
	}
	if err := db.Ping(); err != nil {
		return fail(err)
	}
	from, err := migrate(db, now)
	if err != nil {
		return fail(err)
	}
	s := &Store{path: path, db: db}
	if path == "" {
		return s, nil
	}
	n, err := s.Count()
	if err != nil {
		return fail(err)
	}
	switch {
	case from == 0:
		s.info = fmt.Sprintf("new database (schema %d)", SchemaVersion())
	case from < SchemaVersion():
		s.info = fmt.Sprintf("%d accounts, schema %d -> %d", n, from, SchemaVersion())
	default:
		s.info = fmt.Sprintf("%d accounts, schema %d", n, from)
	}
	legacy := filepath.Join(filepath.Dir(path), LegacyFile)
	if _, err := os.Stat(legacy); err == nil && !strings.EqualFold(filepath.Clean(legacy), filepath.Clean(path)) {
		if n > 0 {
			s.info += fmt.Sprintf("; %s beside it NOT imported (the database has accounts already)", LegacyFile)
			return s, nil
		}
		imported, renamed, err := s.importLegacy(legacy)
		if err != nil {
			db.Close()
			return nil, err
		}
		s.info = fmt.Sprintf("%d accounts imported from %s (%s), schema %d", imported, LegacyFile, renamed, SchemaVersion())
	}
	return s, nil
}

// importLegacy moves the accounts of the JSON store at legacy into the (empty) database in one transaction, then
// renames the file (importedName). It returns the number of accounts and what became of the file. A failed rename
// doesn't fail the import: the next start finds a database with accounts and leaves the file alone.
func (s *Store) importLegacy(legacy string) (int, string, error) {
	f, err := readLegacy(legacy)
	if err != nil {
		return 0, "", err
	}
	n, err := s.insertLegacy(legacy, f)
	if err != nil {
		return 0, "", err
	}
	renamed := importedName(legacy)
	if err := os.Rename(legacy, renamed); err != nil {
		return n, fmt.Sprintf("NOT renamed: %v", err), nil
	}
	return n, "renamed " + filepath.Base(renamed), nil
}

// insertLegacy adds the JSON store's accounts and id counters in one transaction.
func (s *Store) insertLegacy(legacy string, f *legacyFile) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	nextUser, nextPersona := max(f.NextUserID, FirstUserID), max(f.NextPersonaID, FirstPersonaID)
	for i := range f.Accounts {
		a := &f.Accounts[i]
		if err := insertAccount(tx, a); err != nil {
			return 0, fmt.Errorf("store: importing %s: account %d (%s): %v", legacy, a.UserID, a.Email, err)
		}
		nextUser = max(nextUser, a.UserID+1)
		for _, p := range a.Personas {
			nextPersona = max(nextPersona, p.ID+1)
		}
	}
	if err := setCounters(tx, nextUser, nextPersona); err != nil {
		return 0, err
	}
	return len(f.Accounts), tx.Commit()
}

// Path returns the database file the store is kept in ("" = memory only).
func (s *Store) Path() string { return s.path }

// Info says what Open found and did: the number of accounts and the schema version, a new database, or the import of
// accounts.json ("" for a memory store).
func (s *Store) Info() string { return s.info }

// Close closes the database. The store must not be used afterwards.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

// Count returns the number of accounts.
func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&n)
	return n, err
}

func emailKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func nameKey(name string) string { return strings.ToLower(name) }

// Login returns the account for email (compared case-insensitively), creating it with one persona if
// it doesn't exist. The password is not checked (dgserver's logins go through internal/accounts,
// which checks it and uses Credentials, LoginUser and Create; Login stays for tests and tools). first
// reports whether the account was just created.
// The returned Account is a copy, as it was before this login (LastLogin = the previous login). An
// error means the database couldn't be read or written; nothing of the login is stored then.
func (s *Store) Login(email string, now time.Time) (acct Account, first bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Account{}, false, err
	}
	defer tx.Rollback()
	var userID int64
	err = tx.QueryRow(`SELECT user_id FROM accounts WHERE email_key = ?`, emailKey(email)).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		if userID, err = createAccount(tx, email, "", "", now); err != nil {
			return Account{}, false, err
		}
		first = true
	} else if err != nil {
		return Account{}, false, err
	}
	a, err := load(tx, userID)
	if err != nil {
		return Account{}, false, err
	}
	if _, err := tx.Exec(`UPDATE accounts SET last_login = ? WHERE user_id = ?`, now.Format(timeFormat), userID); err != nil {
		return Account{}, false, err
	}
	return *a, first, tx.Commit()
}

// Persona returns the account's persona with this name (case-insensitive).
func (a Account) Persona(name string) (Persona, bool) {
	for _, p := range a.Personas {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Persona{}, false
}

// PersonaByID returns the persona with this id and its account.
func (s *Store) PersonaByID(id int64) (Account, Persona, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.personaBy(`SELECT user_id, persona_id FROM personas WHERE persona_id = ?`, id)
}

// PersonaByName returns the persona with this display name (compared case-insensitively; names are unique,
// see uniqueName; the lowest persona id wins should an imported store hold one twice) and its account (A5b:
// UserSessions::lookupUser, the PvP lobby's invite by name).
func (s *Store) PersonaByName(name string) (Account, Persona, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// never an account its player deleted (its persona is anonymous, DeleteAccount)
	return s.personaBy(`SELECT user_id, persona_id FROM personas WHERE name_key = ? AND user_id NOT IN
		(SELECT user_id FROM accounts WHERE status = ?) ORDER BY persona_id LIMIT 1`, nameKey(strings.TrimSpace(name)),
		int(StatusDeleted))
}

func (s *Store) personaBy(query string, args ...any) (Account, Persona, bool) {
	var userID, personaID int64
	if err := s.db.QueryRow(query, args...).Scan(&userID, &personaID); err != nil {
		return Account{}, Persona{}, false
	}
	a, err := load(s.db, userID)
	if err != nil {
		return Account{}, Persona{}, false
	}
	for _, p := range a.Personas {
		if p.ID == personaID {
			return *a, p, true
		}
	}
	return Account{}, Persona{}, false
}

// Account returns the account with this user id.
func (s *Store) Account(userID int64) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := load(s.db, userID)
	if err != nil {
		return Account{}, false
	}
	return *a, true
}

// ErrNoAccount is returned by Update for an unknown user id.
var ErrNoAccount = errors.New("store: no such account")

// Update changes the account with this user id through fn and stores it, in one transaction. fn gets the
// stored account (maps allocated) and must not keep it; its UserID can't be changed. It returns a copy of the
// changed account. An error other than ErrNoAccount means the change couldn't be stored (the database keeps
// the account as it was; the returned copy is what fn made).
func (s *Store) Update(userID int64, fn func(a *Account)) (Account, error) {
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
	before := copyAccount(a)
	fn(a)
	a.UserID = userID
	if err := save(tx, &before, a); err != nil {
		return copyAccount(a), err
	}
	return copyAccount(a), tx.Commit()
}

// UpdatePair changes two accounts through fn in one transaction (B13b: a friendship is added to or
// removed from both accounts at once). fn gets the stored accounts (maps allocated, as Update's; the same pointer
// twice if the ids are equal) and must not keep them. It returns copies of both; ErrNoAccount if either is unknown.
func (s *Store) UpdatePair(userA, userB int64, fn func(a, b *Account)) (Account, Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Account{}, Account{}, err
	}
	defer tx.Rollback()
	a, err := load(tx, userA)
	if err != nil {
		return Account{}, Account{}, err
	}
	b := a
	if userB != userA {
		if b, err = load(tx, userB); err != nil {
			return Account{}, Account{}, err
		}
	}
	beforeA, beforeB := copyAccount(a), copyAccount(b)
	fn(a, b)
	a.UserID, b.UserID = userA, userB
	err = save(tx, &beforeA, a)
	if err == nil && b != a {
		err = save(tx, &beforeB, b)
	}
	if err == nil {
		err = tx.Commit()
	}
	return copyAccount(a), copyAccount(b), err
}

func copyAccount(a *Account) Account {
	c := *a
	c.Personas = append([]Persona(nil), a.Personas...)
	c.Items = maps.Clone(a.Items)
	c.Settings = maps.Clone(a.Settings)
	c.PerkPages = ClonePages(a.PerkPages)
	c.History = a.History.Clone()
	c.Lists = a.Lists.Clone()
	if a.Stats != nil {
		c.Stats = make(map[string]map[string]string, len(a.Stats))
		for g, m := range a.Stats {
			c.Stats[g] = maps.Clone(m)
		}
	}
	return c
}

// uniqueName appends a number to name while another account's persona already has it.
func uniqueName(q querier, name string) (string, error) {
	taken := func(n string) (bool, error) {
		var one int
		err := q.QueryRow(`SELECT 1 FROM personas WHERE name_key = ? LIMIT 1`, nameKey(n)).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	}
	if t, err := taken(name); err != nil || !t {
		return name, err
	}
	for i := 2; ; i++ {
		suffix := strconv.Itoa(i)
		base := name
		if len(base)+len(suffix) > MaxPersonaName {
			base = base[:MaxPersonaName-len(suffix)]
		}
		if t, err := taken(base + suffix); err != nil || !t {
			return base + suffix, err
		}
	}
}

// querier is what *sql.DB and *sql.Tx share.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func counters(q querier) (nextUser, nextPersona int64, err error) {
	nextUser, nextPersona = FirstUserID, FirstPersonaID
	rows, err := q.Query(`SELECT name, value FROM counters`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var v int64
		if err := rows.Scan(&name, &v); err != nil {
			return 0, 0, err
		}
		switch name {
		case "next_user_id":
			nextUser = max(nextUser, v)
		case "next_persona_id":
			nextPersona = max(nextPersona, v)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	// never below what the tables hold (a counter row lost or edited by hand)
	var mu, mp sql.NullInt64
	if err := q.QueryRow(`SELECT MAX(user_id) FROM accounts`).Scan(&mu); err != nil {
		return 0, 0, err
	}
	if err := q.QueryRow(`SELECT MAX(persona_id) FROM personas`).Scan(&mp); err != nil {
		return 0, 0, err
	}
	if mu.Valid {
		nextUser = max(nextUser, mu.Int64+1)
	}
	if mp.Valid {
		nextPersona = max(nextPersona, mp.Int64+1)
	}
	return nextUser, nextPersona, nil
}

func setCounters(q querier, nextUser, nextPersona int64) error {
	for name, v := range map[string]int64{"next_user_id": nextUser, "next_persona_id": nextPersona} {
		if _, err := q.Exec(`INSERT INTO counters (name, value) VALUES (?, ?)
			ON CONFLICT (name) DO UPDATE SET value = excluded.value`, name, v); err != nil {
			return err
		}
	}
	return nil
}

// ---- reading an account ----

// load reads the account with this user id (ErrNoAccount if there is none). Items, Settings and Stats are always
// allocated (as Update hands them to its fn); every other collection is nil when empty, as encoding/json's omitempty
// left it in accounts.json.
func load(q querier, userID int64) (*Account, error) {
	a := &Account{UserID: userID}
	var created, lastLogin string
	var granted int64
	var level, xp, avatar, ec, pc, cc, spentEC, spentPC, countEC, countPC uint32
	var flags, userInfo int64
	err := q.QueryRow(`SELECT email, created, last_login, granted, level, xp, flags, avatar, ec, pc, cc, user_info,
		first_win_day, spent_ec, spent_pc, count_ec, count_pc, ranked_games, ranked_wins FROM accounts WHERE user_id = ?`,
		userID).Scan(
		&a.Email, &created, &lastLogin, &granted, &level, &xp, &flags, &avatar, &ec, &pc, &cc, &userInfo,
		&a.FirstWinDay, &spentEC, &spentPC, &countEC, &countPC, &a.History.Ranked.Games, &a.History.Ranked.Wins)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoAccount
	}
	if err != nil {
		return nil, err
	}
	if a.Created, err = parseTime(created); err != nil {
		return nil, err
	}
	if a.LastLogin, err = parseTime(lastLogin); err != nil {
		return nil, err
	}
	a.Granted = granted != 0
	a.Level, a.XP, a.Flags, a.Avatar = level, xp, uint64(flags), avatar
	a.EC, a.PC, a.CC, a.UserInfo = ec, pc, cc, uint64(userInfo)
	a.Commerce = Commerce{SpentEC: spentEC, SpentPC: spentPC, CountEC: countEC, CountPC: countPC}
	a.Items, a.Settings, a.Stats = map[uint32]uint32{}, map[string]string{}, map[string]map[string]string{}

	err = each(q, func(r *sql.Rows) error {
		var p Persona
		err := r.Scan(&p.ID, &p.Name)
		a.Personas = append(a.Personas, p)
		return err
	}, `SELECT persona_id, name FROM personas WHERE user_id = ? ORDER BY pos`, userID)
	if err == nil {
		err = each(q, func(r *sql.Rows) error {
			var id, n uint32
			err := r.Scan(&id, &n)
			a.Items[id] = n
			return err
		}, `SELECT catalog_id, count FROM items WHERE user_id = ?`, userID)
	}
	if err == nil {
		err = each(q, func(r *sql.Rows) error {
			var k, v string
			err := r.Scan(&k, &v)
			a.Settings[k] = v
			return err
		}, `SELECT key, value FROM settings WHERE user_id = ?`, userID)
	}
	if err == nil {
		err = each(q, func(r *sql.Rows) error {
			var g, k, v string
			err := r.Scan(&g, &k, &v)
			if a.Stats[g] == nil {
				a.Stats[g] = map[string]string{}
			}
			a.Stats[g][k] = v
			return err
		}, `SELECT grp, name, value FROM stats WHERE user_id = ?`, userID)
	}
	if err == nil {
		err = each(q, func(r *sql.Rows) error {
			var p PerkPage
			var shapes string
			if err := r.Scan(&p.ID, &p.Name, &p.Version, &p.Status, &shapes); err != nil {
				return err
			}
			if shapes != "" {
				if err := json.Unmarshal([]byte(shapes), &p.Shapes); err != nil {
					return fmt.Errorf("perk page %d: %v", p.ID, err)
				}
			}
			a.PerkPages = append(a.PerkPages, p)
			return nil
		}, `SELECT page_id, name, version, status, shapes FROM perk_pages WHERE user_id = ? ORDER BY pos`, userID)
	}
	if err == nil {
		err = loadHistory(q, a)
	}
	if err == nil {
		err = each(q, func(r *sql.Rows) error {
			var list, added string
			var m ListMember
			if err := r.Scan(&list, &m.ID, &m.Name, &added); err != nil {
				return err
			}
			t, err := parseTime(added)
			if err != nil {
				return err
			}
			m.Added = t
			switch list {
			case listFriends:
				a.Lists.Friends = append(a.Lists.Friends, m)
			case listRecent:
				a.Lists.Recent = append(a.Lists.Recent, m)
			case listBlocked:
				a.Lists.Blocked = append(a.Lists.Blocked, m)
			}
			return nil
		}, `SELECT list, persona_id, name, added FROM list_members WHERE user_id = ? ORDER BY list, pos`, userID)
	}
	if err != nil {
		return nil, fmt.Errorf("store: account %d: %v", userID, err)
	}
	return a, nil
}

func loadHistory(q querier, a *Account) error {
	err := each(q, func(r *sql.Rows) error {
		var id uint32
		var g GamesWon
		if err := r.Scan(&id, &g.Games, &g.Wins); err != nil {
			return err
		}
		if a.History.Shapers == nil {
			a.History.Shapers = map[uint32]GamesWon{}
		}
		a.History.Shapers[id] = g
		return nil
	}, `SELECT catalog_id, games, wins FROM history_shapers WHERE user_id = ?`, a.UserID)
	if err != nil {
		return err
	}
	byPos := map[int]int{} // pos -> index into Days
	err = each(q, func(r *sql.Rows) error {
		var pos int
		var d HistoryDay
		err := r.Scan(&pos, &d.Day, &d.Ranked.Games, &d.Ranked.Wins)
		byPos[pos] = len(a.History.Days)
		a.History.Days = append(a.History.Days, d)
		return err
	}, `SELECT pos, day, ranked_games, ranked_wins FROM history_days WHERE user_id = ? ORDER BY pos`, a.UserID)
	if err != nil || len(a.History.Days) == 0 {
		return err
	}
	err = each(q, func(r *sql.Rows) error {
		var pos int
		var id uint32
		var d ShaperDay
		if err := r.Scan(&pos, &id, &d.Games, &d.Wins, &d.Kills, &d.Deaths, &d.Assists, &d.Creeps, &d.Gold, &d.Seconds); err != nil {
			return err
		}
		i, ok := byPos[pos]
		if !ok {
			return fmt.Errorf("history day %d's shapers without the day", pos)
		}
		if a.History.Days[i].Shapers == nil {
			a.History.Days[i].Shapers = map[uint32]ShaperDay{}
		}
		a.History.Days[i].Shapers[id] = d
		return nil
	}, `SELECT pos, catalog_id, games, wins, kills, deaths, assists, creeps, gold, seconds FROM history_day_shapers
		WHERE user_id = ?`, a.UserID)
	if err != nil {
		return err
	}
	return each(q, func(r *sql.Rows) error {
		var pos int
		var role uint8
		var g GamesWon
		if err := r.Scan(&pos, &role, &g.Games, &g.Wins); err != nil {
			return err
		}
		i, ok := byPos[pos]
		if !ok {
			return fmt.Errorf("history day %d's roles without the day", pos)
		}
		if a.History.Days[i].Roles == nil {
			a.History.Days[i].Roles = map[uint8]GamesWon{}
		}
		a.History.Days[i].Roles[role] = g
		return nil
	}, `SELECT pos, role, games, wins FROM history_day_roles WHERE user_id = ?`, a.UserID)
}

// each runs fn for every row of the query.
func each(q querier, fn func(*sql.Rows) error, query string, args ...any) error {
	rows, err := q.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func parseTime(s string) (time.Time, error) { return time.Parse(timeFormat, s) }

// ---- writing an account ----

// insertAccount adds a whole new account (Login, the import).
func insertAccount(q querier, a *Account) error {
	if _, err := q.Exec(`INSERT INTO accounts (user_id, email, email_key, created, last_login) VALUES (?, ?, ?, ?, ?)`,
		a.UserID, a.Email, emailKey(a.Email), a.Created.Format(timeFormat), a.LastLogin.Format(timeFormat)); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("the email %q (or the user id %d) twice", a.Email, a.UserID)
		}
		return err
	}
	return save(q, nil, a)
}

// save writes what changed between before and a (before nil: everything but the account's key columns, which
// insertAccount wrote). The account's row is always written; each collection only when it differs.
func save(q querier, before, a *Account) error {
	if _, err := q.Exec(`UPDATE accounts SET email = ?, email_key = ?, created = ?, last_login = ?, granted = ?, level = ?,
		xp = ?, flags = ?, avatar = ?, ec = ?, pc = ?, cc = ?, user_info = ?, first_win_day = ?, spent_ec = ?, spent_pc = ?,
		count_ec = ?, count_pc = ?, ranked_games = ?, ranked_wins = ? WHERE user_id = ?`,
		a.Email, emailKey(a.Email), a.Created.Format(timeFormat), a.LastLogin.Format(timeFormat), a.Granted, a.Level,
		a.XP, int64(a.Flags), a.Avatar, a.EC, a.PC, a.CC, int64(a.UserInfo), a.FirstWinDay, a.Commerce.SpentEC,
		a.Commerce.SpentPC, a.Commerce.CountEC, a.Commerce.CountPC, a.History.Ranked.Games, a.History.Ranked.Wins,
		a.UserID); err != nil {
		return err
	}
	if before == nil {
		before = &Account{}
	}
	w := writer{q: q, user: a.UserID}
	if !slices.Equal(before.Personas, a.Personas) {
		w.replace("personas", `INSERT INTO personas (persona_id, user_id, pos, name, name_key) VALUES (?, ?, ?, ?, ?)`,
			len(a.Personas), func(i int) []any {
				p := a.Personas[i]
				return []any{p.ID, a.UserID, i, p.Name, nameKey(p.Name)}
			})
	}
	if !maps.Equal(before.Items, a.Items) {
		ids := slices.Sorted(maps.Keys(a.Items))
		w.replace("items", `INSERT INTO items (user_id, catalog_id, count) VALUES (?, ?, ?)`, len(ids), func(i int) []any {
			return []any{a.UserID, ids[i], a.Items[ids[i]]}
		})
	}
	if !maps.Equal(before.Settings, a.Settings) {
		keys := slices.Sorted(maps.Keys(a.Settings))
		w.replace("settings", `INSERT INTO settings (user_id, key, value) VALUES (?, ?, ?)`, len(keys), func(i int) []any {
			return []any{a.UserID, keys[i], a.Settings[keys[i]]}
		})
	}
	if !reflect.DeepEqual(before.Stats, a.Stats) {
		var rows [][]any
		for _, g := range slices.Sorted(maps.Keys(a.Stats)) {
			for _, k := range slices.Sorted(maps.Keys(a.Stats[g])) {
				rows = append(rows, []any{a.UserID, g, k, a.Stats[g][k]})
			}
		}
		w.replace("stats", `INSERT INTO stats (user_id, grp, name, value) VALUES (?, ?, ?, ?)`, len(rows),
			func(i int) []any { return rows[i] })
	}
	if !reflect.DeepEqual(before.PerkPages, a.PerkPages) {
		w.replace("perk_pages", `INSERT INTO perk_pages (user_id, pos, page_id, name, version, status, shapes)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, len(a.PerkPages), func(i int) []any {
			p := a.PerkPages[i]
			shapes := ""
			if len(p.Shapes) > 0 {
				raw, err := json.Marshal(p.Shapes)
				if err != nil && w.err == nil {
					w.err = err
				}
				shapes = string(raw)
			}
			return []any{a.UserID, i, p.ID, p.Name, p.Version, p.Status, shapes}
		})
	}
	if !reflect.DeepEqual(before.History, a.History) {
		h := a.History
		ids := slices.Sorted(maps.Keys(h.Shapers))
		w.replace("history_shapers", `INSERT INTO history_shapers (user_id, catalog_id, games, wins) VALUES (?, ?, ?, ?)`,
			len(ids), func(i int) []any { g := h.Shapers[ids[i]]; return []any{a.UserID, ids[i], g.Games, g.Wins} })
		w.replace("history_days", `INSERT INTO history_days (user_id, pos, day, ranked_games, ranked_wins) VALUES (?, ?, ?, ?, ?)`,
			len(h.Days),
			func(i int) []any { d := h.Days[i]; return []any{a.UserID, i, d.Day, d.Ranked.Games, d.Ranked.Wins} })
		var shapers, roles [][]any
		for pos, d := range h.Days {
			for _, id := range slices.Sorted(maps.Keys(d.Shapers)) {
				s := d.Shapers[id]
				shapers = append(shapers, []any{a.UserID, pos, id, s.Games, s.Wins, s.Kills, s.Deaths, s.Assists, s.Creeps, s.Gold, s.Seconds})
			}
			for _, r := range slices.Sorted(maps.Keys(d.Roles)) {
				g := d.Roles[r]
				roles = append(roles, []any{a.UserID, pos, r, g.Games, g.Wins})
			}
		}
		w.replace("history_day_shapers", `INSERT INTO history_day_shapers (user_id, pos, catalog_id, games, wins, kills,
			deaths, assists, creeps, gold, seconds) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, len(shapers),
			func(i int) []any { return shapers[i] })
		w.replace("history_day_roles", `INSERT INTO history_day_roles (user_id, pos, role, games, wins) VALUES (?, ?, ?, ?, ?)`,
			len(roles), func(i int) []any { return roles[i] })
	}
	if !reflect.DeepEqual(before.Lists, a.Lists) {
		var rows [][]any
		for _, l := range []struct {
			name    string
			members []ListMember
		}{{listFriends, a.Lists.Friends}, {listRecent, a.Lists.Recent}, {listBlocked, a.Lists.Blocked}} {
			for pos, m := range l.members {
				rows = append(rows, []any{a.UserID, l.name, pos, m.ID, m.Name, m.Added.Format(timeFormat)})
			}
		}
		w.replace("list_members", `INSERT INTO list_members (user_id, list, pos, persona_id, name, added) VALUES (?, ?, ?, ?, ?, ?)`,
			len(rows), func(i int) []any { return rows[i] })
	}
	return w.err
}

// writer replaces an account's rows of one table at a time; the first error sticks.
type writer struct {
	q    querier
	user int64
	err  error
}

func (w *writer) replace(table, insert string, n int, row func(i int) []any) {
	if w.err != nil {
		return
	}
	if _, w.err = w.q.Exec(`DELETE FROM `+table+` WHERE user_id = ?`, w.user); w.err != nil {
		w.err = fmt.Errorf("%s: %v", table, w.err)
		return
	}
	for i := 0; i < n && w.err == nil; i++ {
		if _, err := w.q.Exec(insert, row(i)...); err != nil {
			w.err = fmt.Errorf("%s: %v", table, err)
		}
	}
}
