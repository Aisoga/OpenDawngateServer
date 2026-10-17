package store

import "database/sql"

// This file holds the ranking the main menu's Leaderboard shows (Stats::getLeaderboard). No schema change: the
// ranking is read from the stats and history the accounts already keep.

// LeaderRow is one account in the ranking.
type LeaderRow struct {
	PersonaID int64  // the account's first persona
	Name      string // its display name
	Games     uint32 // PlayerStats' PLAYED (minus the excluded days' games)
	Wins      uint32 // PlayerStats' WINS (minus the excluded days' wins), at most Games
	Shaper    uint32 // the catalogID of the shaper with the most games (History.Shapers); 0 = none recorded
}

// Leaderboard returns the active accounts with at least minGames games (at least 1), ranked by wins (most first), then
// games (fewest first, so the better win ratio leads), then persona id; at most limit rows. Games and wins are the
// totals the profile shows (Stats group PlayerStats, PLAYED and WINS). excludeFrom ("2006-01-02", a HistoryDay's Day)
// takes the games and wins of that day and every later one out of the totals, as far as the account's history recorded
// them by shaper (it keeps HistoryDays days): the ranking as it stood before that day ("" = nothing taken out).
// Disabled, banned and deleted accounts are left out.
func (s *Store) Leaderboard(excludeFrom string, minGames, limit int) ([]LeaderRow, error) {
	if excludeFrom == "" {
		excludeFrom = "~" // sorts after every day
	}
	if limit <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []LeaderRow
	err := each(s.db, func(r *sql.Rows) error {
		var row LeaderRow
		if err := r.Scan(&row.PersonaID, &row.Name, &row.Games, &row.Wins, &row.Shaper); err != nil {
			return err
		}
		out = append(out, row)
		return nil
	}, `SELECT persona_id, name, games, MIN(wins, games) AS wins, shaper FROM (
		SELECT p.persona_id, p.name,
			MAX(CAST(COALESCE((SELECT value FROM stats WHERE user_id = a.user_id AND grp = 'PlayerStats'
				AND name = 'PLAYED'), '0') AS INTEGER) - COALESCE(d.games, 0), 0) AS games,
			MAX(CAST(COALESCE((SELECT value FROM stats WHERE user_id = a.user_id AND grp = 'PlayerStats'
				AND name = 'WINS'), '0') AS INTEGER) - COALESCE(d.wins, 0), 0) AS wins,
			COALESCE((SELECT catalog_id FROM history_shapers WHERE user_id = a.user_id
				ORDER BY games DESC, catalog_id LIMIT 1), 0) AS shaper
		FROM accounts a
		JOIN personas p ON p.user_id = a.user_id AND p.pos = 0
		LEFT JOIN (SELECT hd.user_id, SUM(hs.games) AS games, SUM(hs.wins) AS wins FROM history_days hd
			JOIN history_day_shapers hs ON hs.user_id = hd.user_id AND hs.pos = hd.pos
			WHERE hd.day >= ? GROUP BY hd.user_id) d ON d.user_id = a.user_id
		WHERE a.status = ?)
	WHERE games >= ? ORDER BY MIN(wins, games) DESC, games ASC, persona_id LIMIT ?`,
		excludeFrom, int(StatusActive), max(minGames, 1), limit)
	return out, err
}
