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

	// Rules are a custom game's match rules as JSON (package rules' Combination), "" for the standard game. Since M30
	// they choose the game's rating type (internal/rating's ModeOf): standard, draft or custom.
	Rules string
	// Unrated: the custom game's admin made it unrated (M30's Rated/Unrated in the lobby); such a match is never rated.
	Unrated bool
	// MapRules are the map's own rules the match played (MAPRULES5) as JSON (MapRules: its kill race, time limit and
	// start level as the match host played them, and whether the map's balance changed), "" for a map without rules.
	// Not Unrated: a game on such a map stays in the history's lists; rating's Judge rates no game whose map victory
	// was played (MapRules.Victory).
	MapRules string
}

// MapRules is Match.MapRules read (ParseMapRules): what of a map's own rules the match played. Kills, Minutes and
// StartLevel are only those the match host played (a host without the feature played none of them); Tuned: the map
// changes balance values (compiled into its archive, so always played).
type MapRules struct {
	Kills      int  `json:"kills,omitempty"`      // a kill race to this many kills
	Minutes    int  `json:"minutes,omitempty"`    // a time limit in minutes
	StartLevel int  `json:"startLevel,omitempty"` // everyone's start level
	Bindings   int  `json:"bindings,omitempty"`   // WINBIND-B: a Binding race to this many destroyed enemy Bindings
	Tuned      bool `json:"tuned,omitempty"`      // the map's own balance values
	// From is where a played victory came from: RulesFromMap (the map's own rules) or RulesFromLobby (the custom game
	// lobby's win condition, WINLOBBY); "" for none, and in a record from before WINBIND-B (the map's).
	From string `json:"from,omitempty"`
	// Destroyed are each team's destructions of enemy Bindings when a Binding race ran (finishGame's bindings: team
	// 1's, team 2's), the score of "won the Binding race 3–1".
	Destroyed *[2]int `json:"destroyed,omitempty"`
}

// Where a match's victory came from (MapRules.From).
const (
	RulesFromMap   = "map"
	RulesFromLobby = "lobby"
)

// Victory reports whether the match was won by its own rule set: a kill race, a Binding race or a time limit.
func (r MapRules) Victory() bool { return r.Kills > 0 || r.Minutes > 0 || r.Bindings > 0 }

// VictoryFrom is MapRules.From of the match's played victory ("" without one; "map" for a record from before
// WINBIND-B and a column this build can't read).
func (m Match) VictoryFrom() string {
	r, err := ParseMapRules(m.MapRules)
	if err != nil || (r.Victory() && r.From == "") {
		return RulesFromMap
	}
	if !r.Victory() {
		return ""
	}
	return r.From
}

// Record is the column's text: "" for nothing played, else the JSON.
func (r MapRules) Record() string {
	if r == (MapRules{}) {
		return ""
	}
	b, _ := json.Marshal(r) // a struct of ints, a bool, a string and an array always marshals
	return string(b)
}

// ParseMapRules reads Match.MapRules ("" = none).
func ParseMapRules(s string) (MapRules, error) {
	var r MapRules
	if s == "" {
		return r, nil
	}
	err := json.Unmarshal([]byte(s), &r)
	return r, err
}

// MapVictory reports whether the match played the map's own victory (MapRules.Victory); a column this build can't
// read counts as one, so such a match is never rated by mistake.
func (m Match) MapVictory() bool {
	r, err := ParseMapRules(m.MapRules)
	return err != nil || r.Victory()
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
	// Inventory and Purchases (M32) are seatStats.inventory and .purchases as JSON (a list of 11 item asset ids by
	// slot; a list of {ms, kind, id}); nil = none. Read by Match only.
	Inventory, Purchases json.RawMessage
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
	res, err := tx.Exec(`INSERT INTO matches (game_id, kind, mode, level, winner, reason, started, ended, duration_ms, timeline, rules,
		unrated, map_rules) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.GameID, m.Kind, m.Mode, m.Level, m.Winner, m.Reason,
		m.Started.UTC().Format(timeFormat), m.Ended.UTC().Format(timeFormat), m.DurationMs, string(m.Timeline), m.Rules,
		m.Unrated, m.MapRules)
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
			left_match, kills, deaths, assists, cs, gold, level, stats, sources, inventory, purchases)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, st.Seat, nullID(st.PersonaID), nullID(st.UserID), st.Team, st.Bot, int64(st.Noun), st.Role, st.Won, st.Left,
			st.Kills, st.Deaths, st.Assists, st.CS, st.Gold, st.Level, string(stats), string(st.Sources),
			string(st.Inventory), string(st.Purchases)); err != nil {
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

// MatchesWhere is RecentMatches (personaID 0) or MatchesOf (a persona) with a filter: only the matches keep accepts,
// paged as those (newest first, older than before, at most limit; a page shorter than limit is the last). keep sees
// a match's head without seats (ID, Kind, Mode, Rules, Unrated and the rest of matchColumns), so a filter by game type
// (the match history's type=, PROFILE1) pages right whatever the share of matches it keeps. The heads are read newest
// first until limit are kept (one pass over the index; a match is a small row), then the kept matches are loaded.
func (s *Store) MatchesWhere(personaID int64, before int64, limit int, keep func(Match) bool) ([]Match, error) {
	before, limit = page(before, limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	query, args := `SELECT `+matchColumns+` FROM matches WHERE id < ? ORDER BY id DESC`, []any{before}
	if personaID != 0 {
		query, args = `SELECT `+matchColumns+` FROM matches WHERE id IN (SELECT match_id FROM match_seats
			WHERE persona_id = ? AND match_id < ?) ORDER BY id DESC`, []any{personaID, before}
	}
	var out []Match
	err := each(s.db, func(r *sql.Rows) error {
		var m Match
		if err := scanMatch(r, &m); err != nil {
			return err
		}
		if keep == nil || keep(m) {
			out = append(out, m)
		}
		if len(out) == limit {
			return errPageFull
		}
		return nil
	}, query, args...)
	if err != nil && !errors.Is(err, errPageFull) {
		return nil, err
	}
	return out, s.loadSeats(out, false)
}

// errPageFull ends MatchesWhere's scan once its page is full.
var errPageFull = errors.New("page full")

// MatchesAfter returns the matches after the match after (0 = from the first), oldest first, at most limit (paged as
// RecentMatches), for a replay of the whole history in the order it was played (the rating's seed).
func (s *Store) MatchesAfter(after int64, limit int) ([]Match, error) {
	_, limit = page(0, limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.matches(`SELECT `+matchColumns+` FROM matches WHERE id > ? ORDER BY id LIMIT ?`, max(after, 0), limit)
}

// PlayedMatches returns every match the persona has a seat in, oldest first, each with that seat only (its Name left
// empty; no Timeline, no Sources): the profile's numbers by game type (PROFILE2) are made from them. keep (nil = all)
// sees a match's head without its seat, as MatchesWhere's does, and decides which are read. One query: a persona's
// seats through the match_seats_persona index, joined with their matches.
func (s *Store) PlayedMatches(personaID int64, keep func(Match) bool) ([]Match, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Match
	err := each(s.db, func(r *sql.Rows) error {
		var m Match
		var st MatchSeat
		var persona, user sql.NullInt64
		var noun int64
		var stats string
		if err := scanMatch(r, &m, &st.Seat, &persona, &user, &st.Team, &st.Bot, &noun, &st.Role, &st.Won, &st.Left,
			&st.Kills, &st.Deaths, &st.Assists, &st.CS, &st.Gold, &st.Level, &stats); err != nil {
			return err
		}
		if keep != nil && !keep(m) {
			return nil
		}
		st.PersonaID, st.UserID, st.Noun = persona.Int64, user.Int64, uint32(noun)
		if stats != "" && stats != "{}" {
			if err := json.Unmarshal([]byte(stats), &st.Stats); err != nil {
				return fmt.Errorf("match %d seat %d: stats: %v", m.ID, st.Seat, err)
			}
		}
		m.Seats = []MatchSeat{st}
		out = append(out, m)
		return nil
	}, `SELECT m.id, m.game_id, m.kind, m.mode, m.level, m.winner, m.reason, m.started, m.ended, m.duration_ms, m.rules,
		m.unrated, m.map_rules, ms.seat, ms.persona_id, ms.user_id, ms.team, ms.bot, ms.noun, ms.role, ms.won, ms.left_match, ms.kills,
		ms.deaths, ms.assists, ms.cs, ms.gold, ms.level, ms.stats
		FROM match_seats ms JOIN matches m ON m.id = ms.match_id WHERE ms.persona_id = ? ORDER BY m.id`, personaID)
	return out, err
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

const matchColumns = `id, game_id, kind, mode, level, winner, reason, started, ended, duration_ms, rules, unrated, map_rules`

// scanMatch reads matchColumns (and the extra columns after them) into m.
func scanMatch(r interface{ Scan(...any) error }, m *Match, extra ...any) error {
	var started, ended string
	if err := r.Scan(append([]any{&m.ID, &m.GameID, &m.Kind, &m.Mode, &m.Level, &m.Winner, &m.Reason, &started, &ended,
		&m.DurationMs, &m.Rules, &m.Unrated, &m.MapRules}, extra...)...); err != nil {
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

// loadSeats reads the seats of ms (by seat; the names from personas as they are now), with their sources, inventory
// and purchases when sources is set (s.mu held).
func (s *Store) loadSeats(ms []Match, sources bool) error {
	if len(ms) == 0 {
		return nil
	}
	at := map[int64]int{}
	ids := make([]any, len(ms))
	for i, m := range ms {
		at[m.ID], ids[i] = i, m.ID
	}
	src := `'', '', ''`
	if sources {
		src = `ms.sources, ms.inventory, ms.purchases`
	}
	return each(s.db, func(r *sql.Rows) error {
		var st MatchSeat
		var matchID int64
		var persona, user sql.NullInt64
		var noun int64
		var stats, srcJSON, invJSON, buyJSON string
		if err := r.Scan(&matchID, &st.Seat, &persona, &user, &st.Name, &st.Team, &st.Bot, &noun, &st.Role, &st.Won, &st.Left,
			&st.Kills, &st.Deaths, &st.Assists, &st.CS, &st.Gold, &st.Level, &stats, &srcJSON, &invJSON, &buyJSON); err != nil {
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
		if invJSON != "" {
			st.Inventory = json.RawMessage(invJSON)
		}
		if buyJSON != "" {
			st.Purchases = json.RawMessage(buyJSON)
		}
		m := &ms[at[matchID]]
		m.Seats = append(m.Seats, st)
		return nil
	}, `SELECT ms.match_id, ms.seat, ms.persona_id, ms.user_id, COALESCE(p.name, ''), ms.team, ms.bot, ms.noun, ms.role, ms.won,
		ms.left_match, ms.kills, ms.deaths, ms.assists, ms.cs, ms.gold, ms.level, ms.stats, `+src+`
		FROM match_seats ms LEFT JOIN personas p ON p.persona_id = ms.persona_id
		WHERE ms.match_id IN (?`+strings.Repeat(`, ?`, len(ids)-1)+`) ORDER BY ms.match_id, ms.seat`, ids...)
}
