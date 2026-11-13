package store

import (
	"database/sql"
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// The game types a rating is kept for (M30, MMR1; internal/rating's ModeOf decides a match's type): the standard game
// (PvP matchmaking, a custom game with the default rules), the draft (exactly the rules preset "draft") and any other
// custom game. Each type has its own Glicko-2 rating per account and its own Leaderboard ranking (RatingLeaderboard).
const (
	ModeStandard = "standard"
	ModeDraft    = "draft"
	ModeCustom   = "custom"
)

// RatingModes are the game types in the Leaderboard's order.
var RatingModes = []string{ModeStandard, ModeDraft, ModeCustom}

// IsRatingMode reports whether mode is one of RatingModes.
func IsRatingMode(mode string) bool { return slices.Contains(RatingModes, mode) }

// ModeDay is one history day's rated games of one game type other than the standard (HistoryDay.ModeRated): the
// day's rated games and wins and the type's rating before its first rated game that day.
type ModeDay struct {
	Rated        GamesWon `json:"rated"`
	RatingBefore float64  `json:"ratingBefore"`
}

// RatingIn is the account's rating in game type mode (History.Rating for the standard game; the zero Rating for a type
// without a rated game: use OrStart).
func (h History) RatingIn(mode string) Rating {
	if mode == ModeStandard || mode == "" {
		return h.Rating
	}
	return h.ModeRatings[mode]
}

// SetRatingIn sets the account's rating in game type mode (mode one of RatingModes); the zero Rating removes another
// type's entry (no rated game of it).
func (h *History) SetRatingIn(mode string, r Rating) {
	if mode == ModeStandard || mode == "" {
		h.Rating = r
		return
	}
	if r == (Rating{}) {
		delete(h.ModeRatings, mode)
		if len(h.ModeRatings) == 0 {
			h.ModeRatings = nil
		}
		return
	}
	if h.ModeRatings == nil {
		h.ModeRatings = map[string]Rating{}
	}
	h.ModeRatings[mode] = r
}

// RatedIn is the day's rated games and wins in game type mode and the rating before its first one (valid when
// games > 0).
func (d HistoryDay) RatedIn(mode string) ModeDay {
	if mode == ModeStandard || mode == "" {
		return ModeDay{Rated: d.Rated, RatingBefore: d.RatingBefore}
	}
	return d.ModeRated[mode]
}

// SetRatedIn sets the day's rated part in game type mode; an empty one (no games) removes the type's entry.
func (d *HistoryDay) SetRatedIn(mode string, md ModeDay) {
	if mode == ModeStandard || mode == "" {
		d.Rated, d.RatingBefore = md.Rated, md.RatingBefore
		return
	}
	if md.Rated.Games == 0 {
		delete(d.ModeRated, mode)
		if len(d.ModeRated) == 0 {
			d.ModeRated = nil
		}
		return
	}
	if d.ModeRated == nil {
		d.ModeRated = map[string]ModeDay{}
	}
	d.ModeRated[mode] = md
}

// loadModeRatings reads the account's draft and custom ratings and its history days' rated parts of them (after
// loadHistory). A day row whose day the history no longer keeps is left out.
func loadModeRatings(q querier, a *Account) error {
	err := each(q, func(r *sql.Rows) error {
		var mode string
		var rt Rating
		if err := r.Scan(&mode, &rt.Value, &rt.RD, &rt.Vol, &rt.Games, &rt.Wins); err != nil {
			return err
		}
		a.History.SetRatingIn(mode, rt)
		return nil
	}, `SELECT mode, rating, rd, vol, games, wins FROM mode_ratings WHERE user_id = ? AND mode <> ?`, a.UserID,
		ModeStandard)
	if err != nil || len(a.History.Days) == 0 {
		return err
	}
	byDay := map[string]int{}
	for i, d := range a.History.Days {
		byDay[d.Day] = i
	}
	return each(q, func(r *sql.Rows) error {
		var day, mode string
		var md ModeDay
		if err := r.Scan(&day, &mode, &md.Rated.Games, &md.Rated.Wins, &md.RatingBefore); err != nil {
			return err
		}
		if i, ok := byDay[day]; ok && mode != ModeStandard {
			a.History.Days[i].SetRatedIn(mode, md)
		}
		return nil
	}, `SELECT day, mode, rated_games, rated_wins, rating_before FROM mode_rating_days WHERE user_id = ?`, a.UserID)
}

// saveModeRatings writes the account's draft and custom ratings and its days' rated parts of them when they changed
// (save's history block).
func saveModeRatings(w *writer, before, a *Account) {
	if !maps.Equal(before.History.ModeRatings, a.History.ModeRatings) {
		modes := slices.Sorted(maps.Keys(a.History.ModeRatings))
		w.replace("mode_ratings", `INSERT INTO mode_ratings (user_id, mode, rating, rd, vol, games, wins)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, len(modes), func(i int) []any {
			rt := a.History.ModeRatings[modes[i]].OrStart()
			return []any{a.UserID, modes[i], rt.Value, rt.RD, rt.Vol, rt.Games, rt.Wins}
		})
	}
	if !reflect.DeepEqual(modeDays(before.History), modeDays(a.History)) {
		rows := modeDays(a.History)
		w.replace("mode_rating_days", `INSERT INTO mode_rating_days (user_id, day, mode, rated_games, rated_wins,
			rating_before) VALUES (?, ?, ?, ?, ?, ?)`, len(rows), func(i int) []any {
			return append([]any{a.UserID}, rows[i]...)
		})
	}
}

// modeDays are h's days' rated parts of the other types as rows (day, mode, games, wins, rating before), in order.
func modeDays(h History) [][]any {
	var rows [][]any
	for _, d := range h.Days {
		for _, mode := range slices.Sorted(maps.Keys(d.ModeRated)) {
			md := d.ModeRated[mode]
			if md.Rated.Games == 0 {
				continue
			}
			rows = append(rows, []any{d.Day, mode, md.Rated.Games, md.Rated.Wins, md.RatingBefore})
		}
	}
	return rows
}

// checkMode refuses a game type that isn't one of RatingModes.
func checkMode(mode string) error {
	if !IsRatingMode(mode) {
		return fmt.Errorf("store: no rating type %q", mode)
	}
	return nil
}
