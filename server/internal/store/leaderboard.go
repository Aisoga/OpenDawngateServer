package store

import "database/sql"

// This file holds the ranking the main menu's Leaderboard shows (Stats::getLeaderboard), read from the accounts'
// Leaderboard counts (History.Ranked: PvP and custom games only, never practice games).

// LeaderRow is one account in the ranking.
type LeaderRow struct {
	PersonaID int64  // the account's first persona
	Name      string // its display name
	Games     uint32 // History.Ranked's games (minus the excluded days')
	Wins      uint32 // History.Ranked's wins (minus the excluded days'), at most Games
	Shaper    uint32 // the catalogID of the shaper with the most games of any kind (History.Shapers); 0 = none recorded
}

// Leaderboard returns the active accounts with at least minGames games (at least 1), ranked by wins (most first), then
// games (fewest first, so the better win ratio leads), then persona id; at most limit rows. Games and wins are the
// account's Leaderboard counts (History.Ranked: PvP and custom games; the profile's PlayerStats, which count practice
// games too, aren't read). excludeFrom ("2006-01-02", a HistoryDay's Day) takes the games and wins of that day and
// every later one out of the counts, as far as the account's history keeps them (HistoryDays days): the ranking as it
// stood before that day ("" = nothing taken out). Disabled, banned and deleted accounts are left out.
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
			MAX(a.ranked_games - COALESCE(d.games, 0), 0) AS games,
			MAX(a.ranked_wins - COALESCE(d.wins, 0), 0) AS wins,
			COALESCE((SELECT catalog_id FROM history_shapers WHERE user_id = a.user_id
				ORDER BY games DESC, catalog_id LIMIT 1), 0) AS shaper
		FROM accounts a
		JOIN personas p ON p.user_id = a.user_id AND p.pos = 0
		LEFT JOIN (SELECT user_id, SUM(ranked_games) AS games, SUM(ranked_wins) AS wins FROM history_days
			WHERE day >= ? GROUP BY user_id) d ON d.user_id = a.user_id
		WHERE a.status = ?)
	WHERE games >= ? ORDER BY MIN(wins, games) DESC, games ASC, persona_id LIMIT ?`,
		excludeFrom, int(StatusActive), max(minGames, 1), limit)
	return out, err
}
