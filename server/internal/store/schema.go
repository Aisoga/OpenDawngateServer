package store

import (
	"database/sql"
	"fmt"
	"time"
)

// A migration is one step of the database's schema (ADR-4). Migrations are applied in order, each in its own
// transaction together with its row in schema_version, so a database is always at one known version. A later package
// appends a step (e.g. the password hashes: ALTER TABLE accounts ADD COLUMN ...); a step is never changed once it
// has shipped, because databases out there already hold it.
type migration struct {
	version int
	note    string
	sql     string
}

// migrations is the schema's history. Version 1 holds everything accounts.json held (B5-B13b); versions 2 and 3 are
// the password hashes, account status and sign-up tables.
//
// Layout rules: one row per account in accounts (its scalar fields), one table per collection of an account, every
// child row keyed by user_id and removed with the account (ON DELETE CASCADE). Lists keep their order in a pos column.
// Times are RFC 3339 text with nanoseconds (as encoding/json wrote them), the uint64 bit fields are stored as their
// int64 bit patterns. A perk page's shapes are JSON in the store's own layout (PerkShape), since they are only ever
// read and written as a whole.
var migrations = []migration{
	{1, "accounts, personas and player content (PH1: everything accounts.json held)", `
CREATE TABLE counters (
	name  TEXT PRIMARY KEY,
	value INTEGER NOT NULL
);
CREATE TABLE accounts (
	user_id       INTEGER PRIMARY KEY,
	email         TEXT    NOT NULL,
	email_key     TEXT    NOT NULL UNIQUE,
	created       TEXT    NOT NULL,
	last_login    TEXT    NOT NULL,
	granted       INTEGER NOT NULL DEFAULT 0,
	level         INTEGER NOT NULL DEFAULT 1,
	xp            INTEGER NOT NULL DEFAULT 0,
	flags         INTEGER NOT NULL DEFAULT 0,
	avatar        INTEGER NOT NULL DEFAULT 0,
	ec            INTEGER NOT NULL DEFAULT 0,
	pc            INTEGER NOT NULL DEFAULT 0,
	cc            INTEGER NOT NULL DEFAULT 0,
	user_info     INTEGER NOT NULL DEFAULT 0,
	first_win_day TEXT    NOT NULL DEFAULT '',
	spent_ec      INTEGER NOT NULL DEFAULT 0,
	spent_pc      INTEGER NOT NULL DEFAULT 0,
	count_ec      INTEGER NOT NULL DEFAULT 0,
	count_pc      INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE personas (
	persona_id INTEGER PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES accounts(user_id) ON DELETE CASCADE,
	pos        INTEGER NOT NULL,
	name       TEXT    NOT NULL,
	name_key   TEXT    NOT NULL
);
CREATE INDEX personas_user ON personas(user_id);
CREATE INDEX personas_name ON personas(name_key);
CREATE TABLE items (
	user_id    INTEGER NOT NULL REFERENCES accounts(user_id) ON DELETE CASCADE,
	catalog_id INTEGER NOT NULL,
	count      INTEGER NOT NULL,
	PRIMARY KEY (user_id, catalog_id)
) WITHOUT ROWID;
CREATE TABLE settings (
	user_id INTEGER NOT NULL REFERENCES accounts(user_id) ON DELETE CASCADE,
	key     TEXT    NOT NULL,
	value   TEXT    NOT NULL,
	PRIMARY KEY (user_id, key)
) WITHOUT ROWID;
CREATE TABLE stats (
	user_id INTEGER NOT NULL REFERENCES accounts(user_id) ON DELETE CASCADE,
	grp     TEXT    NOT NULL,
	name    TEXT    NOT NULL,
	value   TEXT    NOT NULL,
	PRIMARY KEY (user_id, grp, name)
) WITHOUT ROWID;
CREATE TABLE perk_pages (
	user_id INTEGER NOT NULL REFERENCES accounts(user_id) ON DELETE CASCADE,
	pos     INTEGER NOT NULL,
	page_id INTEGER NOT NULL,
	name    TEXT    NOT NULL,
	version INTEGER NOT NULL,
	status  INTEGER NOT NULL,
	shapes  TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (user_id, pos)
) WITHOUT ROWID;
CREATE TABLE history_shapers (
	user_id    INTEGER NOT NULL REFERENCES accounts(user_id) ON DELETE CASCADE,
	catalog_id INTEGER NOT NULL,
	games      INTEGER NOT NULL,
	wins       INTEGER NOT NULL,
	PRIMARY KEY (user_id, catalog_id)
) WITHOUT ROWID;
CREATE TABLE history_days (
	user_id INTEGER NOT NULL REFERENCES accounts(user_id) ON DELETE CASCADE,
	pos     INTEGER NOT NULL,
	day     TEXT    NOT NULL,
	PRIMARY KEY (user_id, pos)
) WITHOUT ROWID;
CREATE TABLE history_day_shapers (
	user_id    INTEGER NOT NULL REFERENCES accounts(user_id) ON DELETE CASCADE,
	pos        INTEGER NOT NULL,
	catalog_id INTEGER NOT NULL,
	games      INTEGER NOT NULL,
	wins       INTEGER NOT NULL,
	kills      INTEGER NOT NULL,
	deaths     INTEGER NOT NULL,
	assists    INTEGER NOT NULL,
	creeps     INTEGER NOT NULL,
	gold       INTEGER NOT NULL,
	seconds    INTEGER NOT NULL,
	PRIMARY KEY (user_id, pos, catalog_id)
) WITHOUT ROWID;
CREATE TABLE history_day_roles (
	user_id INTEGER NOT NULL REFERENCES accounts(user_id) ON DELETE CASCADE,
	pos     INTEGER NOT NULL,
	role    INTEGER NOT NULL,
	games   INTEGER NOT NULL,
	wins    INTEGER NOT NULL,
	PRIMARY KEY (user_id, pos, role)
) WITHOUT ROWID;
CREATE TABLE list_members (
	user_id    INTEGER NOT NULL REFERENCES accounts(user_id) ON DELETE CASCADE,
	list       TEXT    NOT NULL,
	pos        INTEGER NOT NULL,
	persona_id INTEGER NOT NULL,
	name       TEXT    NOT NULL,
	added      TEXT    NOT NULL,
	PRIMARY KEY (user_id, list, pos)
) WITHOUT ROWID;
`},
	// the password check. password is the argon2id hash in the PHC string format (internal/accounts; "" = none
	// yet: an account of the earlier builds, which gets one at its next login under signup = open, otherwise from the
	// operator); status is Status (0 active, 1 disabled, 2 banned, 3 deleted).
	{2, "passwords and account status (PH2)", `
ALTER TABLE accounts ADD COLUMN password TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN status INTEGER NOT NULL DEFAULT 0;
`},
	// sign-up. invites are the operator's invite codes (only the SHA-256 of a code is kept: the code itself is
	// shown once, when it is made); signups counts the accounts made per source address and UTC day, for the cap on
	// public addresses, and holds only the current day (older rows are dropped at the next sign-up).
	{3, "invite codes and sign-ups per address (PH2)", `
CREATE TABLE invites (
	id        INTEGER PRIMARY KEY,
	code_hash TEXT    NOT NULL UNIQUE,
	uses      INTEGER NOT NULL,
	used      INTEGER NOT NULL DEFAULT 0,
	created   TEXT    NOT NULL,
	expires   TEXT    NOT NULL DEFAULT '',
	revoked   INTEGER NOT NULL DEFAULT 0,
	note      TEXT    NOT NULL DEFAULT ''
);
CREATE TABLE signups (
	day   TEXT    NOT NULL,
	addr  TEXT    NOT NULL,
	count INTEGER NOT NULL,
	PRIMARY KEY (day, addr)
) WITHOUT ROWID;
`},
	// the admin page. role is Role (0 player, 1 admin: dgserver --make-admin); audit is the log of every admin
	// action and of the operator's account commands (never a password or an invite code); server_settings holds what
	// a running server changes and keeps across restarts (the sign-up mode set on the admin page).
	{4, "admin role, audit log and server settings (PH5)", `
ALTER TABLE accounts ADD COLUMN role INTEGER NOT NULL DEFAULT 0;
CREATE TABLE audit (
	id     INTEGER PRIMARY KEY,
	time   TEXT    NOT NULL,
	actor  TEXT    NOT NULL,
	source TEXT    NOT NULL,
	action TEXT    NOT NULL,
	target TEXT    NOT NULL DEFAULT '',
	detail TEXT    NOT NULL DEFAULT ''
);
CREATE TABLE server_settings (
	key     TEXT PRIMARY KEY,
	value   TEXT NOT NULL,
	changed TEXT NOT NULL
) WITHOUT ROWID;
`},
	// the Leaderboard's own counts (History.Ranked): the PvP and custom games and their wins, all time on the account
	// and per day of the history (the daily ranking). The profile's PlayerStats count every game, practice included,
	// and the history can't tell a practice game from another, so an older account starts at 0 here.
	{5, "the Leaderboard's games and wins (LBMIN)", `
ALTER TABLE accounts ADD COLUMN ranked_games INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN ranked_wins INTEGER NOT NULL DEFAULT 0;
ALTER TABLE history_days ADD COLUMN ranked_games INTEGER NOT NULL DEFAULT 0;
ALTER TABLE history_days ADD COLUMN ranked_wins INTEGER NOT NULL DEFAULT 0;
`},
	// every finished match (M18, matches.go): its own id (Blaze game ids restart with each server start), how it ended,
	// the host's timeline as JSON ('' = none), and a row per seat. A seat's persona_id and user_id are NULL for a bot and
	// carry no foreign key: save() rewrites personas (DELETE + INSERT), and a match stays when an account goes. Names are
	// read from personas when a match is read. stats is a JSON object of integers, sources the host's JSON list ('' =
	// none).
	{6, "match history: every finished match and its seats (MHIST1)", `
CREATE TABLE matches (
	id          INTEGER PRIMARY KEY,
	game_id     INTEGER NOT NULL,
	kind        TEXT    NOT NULL,
	mode        TEXT    NOT NULL,
	level       TEXT    NOT NULL DEFAULT '',
	winner      INTEGER NOT NULL DEFAULT 0,
	reason      TEXT    NOT NULL DEFAULT '',
	started     TEXT    NOT NULL,
	ended       TEXT    NOT NULL,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	timeline    TEXT    NOT NULL DEFAULT ''
);
CREATE TABLE match_seats (
	match_id   INTEGER NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
	seat       INTEGER NOT NULL,
	persona_id INTEGER,
	user_id    INTEGER,
	team       INTEGER NOT NULL,
	bot        INTEGER NOT NULL DEFAULT 0,
	noun       INTEGER NOT NULL DEFAULT 0,
	role       INTEGER NOT NULL DEFAULT 0,
	won        INTEGER NOT NULL DEFAULT 0,
	left_match INTEGER NOT NULL DEFAULT 0,
	kills      INTEGER NOT NULL DEFAULT 0,
	deaths     INTEGER NOT NULL DEFAULT 0,
	assists    INTEGER NOT NULL DEFAULT 0,
	cs         INTEGER NOT NULL DEFAULT 0,
	gold       INTEGER NOT NULL DEFAULT 0,
	level      INTEGER NOT NULL DEFAULT 0,
	stats      TEXT    NOT NULL DEFAULT '{}',
	sources    TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (match_id, seat)
) WITHOUT ROWID;
CREATE INDEX match_seats_persona ON match_seats (persona_id, match_id DESC);
`},
}

// SchemaVersion is the schema version this build writes (the last migration's).
func SchemaVersion() int { return migrations[len(migrations)-1].version }

// migrate brings db up to SchemaVersion. A database from a newer build (a version this one doesn't know) is refused,
// never changed.
func migrate(db *sql.DB, now time.Time) (from int, err error) {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER PRIMARY KEY,
	applied TEXT    NOT NULL,
	note    TEXT    NOT NULL
)`); err != nil {
		return 0, err
	}
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&from); err != nil {
		return 0, err
	}
	if from > SchemaVersion() {
		return from, fmt.Errorf("schema version %d is newer than this dgserver's %d (made by a newer build; use that one)", from, SchemaVersion())
	}
	for _, m := range migrations {
		if m.version <= from {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return from, err
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return from, fmt.Errorf("migration %d: %v", m.version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version, applied, note) VALUES (?, ?, ?)`,
			m.version, now.UTC().Format(time.RFC3339), m.note); err != nil {
			tx.Rollback()
			return from, err
		}
		if err := tx.Commit(); err != nil {
			return from, err
		}
	}
	return from, nil
}
