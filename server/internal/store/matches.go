package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// This file holds the match history (M18, schema version 6): every finished match with a row per seat, written once at
// the host's finishGame (blaze's hostedGame.storeMatch) and read newest first with keyset pagination (the match id).

// Match is one finished match. ID is the store's own (Blaze game ids restart with each server start: GameID is only
// the server run's).
type Match struct {
	ID         int64
	GameID     int64
	Kind       string // "pvp", "custom", "practiceGroup", "practice" (blaze's matchKind)
	Mode       string // createGame's mode: "pvp", "custom", "practice"
	Level      string // the level it was played on
	Winner     int    // finishGame's winnerTeam: 1 or 2, anything else = no winner
	Reason     string // finishGame's reason: "guardian", "surrender", ...
	Started    time.Time
	Ended      time.Time
	DurationMs int64
	// Timeline is the host's matchTimeline as JSON (its fields without "t"); nil = the host sent none. Read by Match only.
	Timeline json.RawMessage
	Seats    []MatchSeat // by seat
}

// MatchSeat is one seat of a match: a human (PersonaID set) or a bot (PersonaID 0).
type MatchSeat struct {
	Seat      int   // the gameplay index (0-9)
	PersonaID int64 // the Blaze user (persona) id; 0 = a bot (NULL in the table)
	UserID    int64 // the account; 0 = a bot
	// Name is the persona's name when the match is read (renames show, a deleted account's persona is "Deleted
	// player"); "" for a bot or a persona that no longer exists. InsertMatch ignores it.
	Name   string
	Team   int // 1 or 2
	Bot    bool
	Noun   uint32 // the shaper's Noun asset id (0 = unknown)
	Role   int    // 1 + the role (1 gladiator, 2 tactician, 3 hunter, 4 predator); 0 = none
	Won    bool
	Left   bool // a human who left the match before its end (or never reached it)
	Kills  int64
	Deaths int64
	// Assists, CS (creeps), Gold and Level are the list's other columns (playerFinish's assists, creeps, gold, level).
	Assists, CS, Gold, Level int64
	Stats                    map[string]int64 // every stat the host reported for the seat (nil = none)
	// Sources is seatStats.sources as JSON; nil = none. Read by Match only.
	Sources json.RawMessage
}

// MaxMatchPage is the most matches one page returns (MatchesOf, RecentMatches).
const MaxMatchPage = 100

// ErrNoSeats is returned by InsertMatch for a match without a seat.
var ErrNoSeats = errors.New("store: a match without seats")

// InsertMatch stores a finished match and its seats in one transaction and returns its id (m.ID is ignored). A seat
// twice (the same Seat) fails it, and nothing is stored.
func (s *Store) InsertMatch(m Match) (int64, error) {
	if len(m.Seats) == 0 {
		return 0, ErrNoSeats
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO matches (game_id, kind, mode, level, winner, reason, started, ended, duration_ms, timeline)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.GameID, m.Kind, m.Mode, m.Level, m.Winner, m.Reason,
		m.Started.UTC().Format(timeFormat), m.Ended.UTC().Format(timeFormat), m.DurationMs, string(m.Timeline))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, st := range m.Seats {
		stats := []byte("{}")
		if len(st.Stats) > 0 {
			if stats, err = json.Marshal(st.Stats); err != nil {
				return 0, err
			}
		}
		if _, err := tx.Exec(`INSERT INTO match_seats (match_id, seat, persona_id, user_id, team, bot, noun, role, won,
			left_match, kills, deaths, assists, cs, gold, level, stats, sources) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, st.Seat, nullID(st.PersonaID), nullID(st.UserID), st.Team, st.Bot, int64(st.Noun), st.Role, st.Won, st.Left,
			st.Kills, st.Deaths, st.Assists, st.CS, st.Gold, st.Level, string(stats), string(st.Sources)); err != nil {
			return 0, fmt.Errorf("seat %d: %w", st.Seat, err)
		}
	}
	return id, tx.Commit()
}

// nullID is an id for a column that is NULL for a bot (0).
func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// RecentMatches returns the newest matches, newest first: at most limit (1..MaxMatchPage; 0 or less = 20), older than
// the match before (0 = from the newest). Their seats carry everything but Sources; Timeline is left out (Match has
// both). A page shorter than limit is the last.
func (s *Store) RecentMatches(before int64, limit int) ([]Match, error) {
	before, limit = page(before, limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.matches(`SELECT `+matchColumns+` FROM matches WHERE id < ? ORDER BY id DESC LIMIT ?`, before, limit)
}

// MatchesOf returns the matches the persona played a seat in, newest first, paged as RecentMatches.
func (s *Store) MatchesOf(personaID int64, before int64, limit int) ([]Match, error) {
	before, limit = page(before, limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.matches(`SELECT `+matchColumns+` FROM matches WHERE id IN (SELECT match_id FROM match_seats
		WHERE persona_id = ? AND match_id < ? ORDER BY match_id DESC LIMIT ?) ORDER BY id DESC`, personaID, before, limit)
}

// Match returns one match with everything stored for it (found false when there is none).
func (s *Store) Match(id int64) (Match, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var m Match
	var timeline string
	err := scanMatch(s.db.QueryRow(`SELECT `+matchColumns+`, timeline FROM matches WHERE id = ?`, id), &m, &timeline)
	if errors.Is(err, sql.ErrNoRows) {
		return Match{}, false, nil
	}
	if err != nil {
		return Match{}, false, err
	}
	if timeline != "" {
		m.Timeline = json.RawMessage(timeline)
	}
	ms := []Match{m}
	if err := s.loadSeats(ms, true); err != nil {
		return Match{}, false, err
	}
	return ms[0], true, nil
}

// page clamps a page request: before 0 or less = from the newest, limit 1..MaxMatchPage (0 or less = 20).
func page(before int64, limit int) (int64, int) {
	if before <= 0 {
		before = 1 << 62
	}
	if limit <= 0 {
		limit = 20
	}
	return before, min(limit, MaxMatchPage)
}

const matchColumns = `id, game_id, kind, mode, level, winner, reason, started, ended, duration_ms`

// scanMatch reads matchColumns (and the extra columns after them) into m.
func scanMatch(r interface{ Scan(...any) error }, m *Match, extra ...any) error {
	var started, ended string
	if err := r.Scan(append([]any{&m.ID, &m.GameID, &m.Kind, &m.Mode, &m.Level, &m.Winner, &m.Reason, &started, &ended,
		&m.DurationMs}, extra...)...); err != nil {
		return err
	}
	m.Started, _ = parseTime(started)
	m.Ended, _ = parseTime(ended)
	return nil
}

// matches runs a query of matchColumns and loads the seats (s.mu held).
func (s *Store) matches(query string, args ...any) ([]Match, error) {
	var out []Match
	err := each(s.db, func(r *sql.Rows) error {
		var m Match
		if err := scanMatch(r, &m); err != nil {
			return err
		}
		out = append(out, m)
		return nil
	}, query, args...)
	if err != nil {
		return nil, err
	}
	return out, s.loadSeats(out, false)
}

// loadSeats reads the seats of ms (by seat; the names from personas as they are now), with their sources when
// sources is set (s.mu held).
func (s *Store) loadSeats(ms []Match, sources bool) error {
	if len(ms) == 0 {
		return nil
	}
	at := map[int64]int{}
	ids := make([]any, len(ms))
	for i, m := range ms {
		at[m.ID], ids[i] = i, m.ID
	}
	src := `''`
	if sources {
		src = `ms.sources`
	}
	return each(s.db, func(r *sql.Rows) error {
		var st MatchSeat
		var matchID int64
		var persona, user sql.NullInt64
		var noun int64
		var stats, srcJSON string
		if err := r.Scan(&matchID, &st.Seat, &persona, &user, &st.Name, &st.Team, &st.Bot, &noun, &st.Role, &st.Won, &st.Left,
			&st.Kills, &st.Deaths, &st.Assists, &st.CS, &st.Gold, &st.Level, &stats, &srcJSON); err != nil {
			return err
		}
		st.PersonaID, st.UserID, st.Noun = persona.Int64, user.Int64, uint32(noun)
		if stats != "" && stats != "{}" {
			if err := json.Unmarshal([]byte(stats), &st.Stats); err != nil {
				return fmt.Errorf("match %d seat %d: stats: %v", matchID, st.Seat, err)
			}
		}
		if srcJSON != "" {
			st.Sources = json.RawMessage(srcJSON)
		}
		m := &ms[at[matchID]]
		m.Seats = append(m.Seats, st)
		return nil
	}, `SELECT ms.match_id, ms.seat, ms.persona_id, ms.user_id, COALESCE(p.name, ''), ms.team, ms.bot, ms.noun, ms.role, ms.won,
		ms.left_match, ms.kills, ms.deaths, ms.assists, ms.cs, ms.gold, ms.level, ms.stats, `+src+`
		FROM match_seats ms LEFT JOIN personas p ON p.persona_id = ms.persona_id
		WHERE ms.match_id IN (?`+strings.Repeat(`, ?`, len(ids)-1)+`) ORDER BY ms.match_id, ms.seat`, ids...)
}
