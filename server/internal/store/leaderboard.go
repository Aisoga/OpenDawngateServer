package store

import (
	"database/sql"
	"math"
)

// This file holds the rankings the main menu's Leaderboard shows (Stats::getLeaderboard): by skill rating
// (RatingLeaderboard: History.Rating, the balanced PvP and custom games) or by wins (Leaderboard: History.Ranked, PvP
// and custom games only, never practice games).

// LeaderRow is one account in the ranking.
type LeaderRow struct {
	PersonaID int64  // the account's first persona
	Name      string // its display name
	Games     uint32 // the ranking's games (minus the excluded days'): History.Ranked's or History.Rating's
	Wins      uint32 // the ranking's wins (minus the excluded days'), at most Games
	Shaper    uint32 // the catalogID of the shaper with the most games of any kind (History.Shapers); 0 = none recorded
	// Skill is the reply's first STAT value, which the client keeps but never shows: the wins in the wins ranking, the
	// rating rounded to a whole number (at least 0) in the rating ranking.
	Skill uint32
	// Rating is the account's skill rating rounded to a whole number (at least 0) in both rankings, and RatedGames its
	// rated games (History.Rating.Games, all time): what the Leaderboard's Rating column shows, when players see
	// ratings (blaze.Server.ShownRating's rule). The rating ranking's Rating is its Skill; the wins ranking's the
	// rating now, whatever the day the ranking is for.
	Rating     uint32
	RatedGames uint32
}

// Leaderboard returns the active accounts with at least minGames games (at least 1), ranked by wins (most first), then
// games (fewest first, so the better win ratio leads), then persona id; at most limit rows. Games and wins are the
// account's Leaderboard counts (History.Ranked: PvP and custom games; the profile's PlayerStats, which count practice
// games too, aren't read). excludeFrom ("2006-01-02", a HistoryDay's Day) takes the games and wins of that day and
// every later one out of the counts, as far as the account's history keeps them (HistoryDays days): the ranking as it
// stood before that day ("" = nothing taken out). Disabled, banned and deleted accounts are left out.
func (s *Store) Leaderboard(excludeFrom string, minGames, limit int) ([]LeaderRow, error) {
	return s.leaderboard(`SELECT persona_id, name, games, MIN(wins, games) AS wins, shaper, MIN(wins, games), rating, rated FROM (
		SELECT p.persona_id, p.name, COALESCE(a.rating, 1500) AS rating, a.rated_games AS rated,
			MAX(a.ranked_games - COALESCE(d.games, 0), 0) AS games,
			MAX(a.ranked_wins - COALESCE(d.wins, 0), 0) AS wins,
			`+favouriteShaper+` AS shaper
		FROM accounts a
		JOIN personas p ON p.user_id = a.user_id AND p.pos = 0
		LEFT JOIN (SELECT user_id, SUM(ranked_games) AS games, SUM(ranked_wins) AS wins FROM history_days
			WHERE day >= ?1 GROUP BY user_id) d ON d.user_id = a.user_id
		WHERE a.status = ?2)
	WHERE games >= ?3 ORDER BY MIN(wins, games) DESC, games ASC, persona_id LIMIT ?4`, excludeFrom, minGames, limit, false)
}

// RatingLeaderboard returns the active accounts with at least minGames rated games (at least 1), ranked by their skill
// rating (highest first), then rated games (most first), then persona id; at most limit rows. Games and wins are the
// rated ones (History.Rating), Skill the rating rounded. excludeFrom ("2006-01-02") gives the ranking as it stood
// before that day: the rated games and wins of that day and later taken out, and the rating the one before the first
// rated game of those days (HistoryDay.RatingBefore), as far as the account's history keeps them (HistoryDays days; ""
// = now). Disabled, banned and deleted accounts are left out.
func (s *Store) RatingLeaderboard(excludeFrom string, minGames, limit int) ([]LeaderRow, error) {
	return s.leaderboard(`SELECT persona_id, name, games, MIN(wins, games) AS wins, shaper, rating, rating, rated FROM (
		SELECT p.persona_id, p.name, a.rated_games AS rated,
			MAX(a.rated_games - COALESCE(d.games, 0), 0) AS games,
			MAX(a.rated_wins - COALESCE(d.wins, 0), 0) AS wins,
			COALESCE((SELECT rating_before FROM history_days WHERE user_id = a.user_id AND day >= ?1 AND rated_games > 0
				ORDER BY day LIMIT 1), a.rating) AS rating,
			`+favouriteShaper+` AS shaper
		FROM accounts a
		JOIN personas p ON p.user_id = a.user_id AND p.pos = 0
		LEFT JOIN (SELECT user_id, SUM(rated_games) AS games, SUM(rated_wins) AS wins FROM history_days
			WHERE day >= ?1 GROUP BY user_id) d ON d.user_id = a.user_id
		WHERE a.status = ?2)
	WHERE games >= ?3 ORDER BY rating DESC, games DESC, persona_id LIMIT ?4`, excludeFrom, minGames, limit, true)
}

// favouriteShaper is the account's shaper with the most games of any kind (a correlated subquery on a.user_id).
const favouriteShaper = `COALESCE((SELECT catalog_id FROM history_shapers WHERE user_id = a.user_id
				ORDER BY games DESC, catalog_id LIMIT 1), 0)`

// leaderboard runs a ranking query whose columns are persona id, name, games, wins, shaper, the skill (the wins, or
// the rating when rating is set), the rating and the rated games, and whose parameters are excludeFrom, the active status, minGames and limit.
func (s *Store) leaderboard(query, excludeFrom string, minGames, limit int, rating bool) ([]LeaderRow, error) {
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
		var skill, value float64
		if err := r.Scan(&row.PersonaID, &row.Name, &row.Games, &row.Wins, &row.Shaper, &skill, &value,
			&row.RatedGames); err != nil {
			return err
		}
		if rating {
			skill = math.Round(skill)
		}
		row.Skill = uint32(min(max(skill, 0), math.MaxUint32))
		row.Rating = uint32(min(max(math.Round(value), 0), math.MaxUint32))
		out = append(out, row)
		return nil
	}, query, excludeFrom, int(StatusActive), max(minGames, 1), limit)
	return out, err
}
