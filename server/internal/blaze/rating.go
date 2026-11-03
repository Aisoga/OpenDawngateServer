package blaze

import (
	"dawngate/server/internal/progress"
	"dawngate/server/internal/rating"
	"dawngate/server/internal/store"
)

// The skill rating (RATING1, internal/rating): at the host's finishGame, after the match history's record is made, a
// rated game (a PvP or custom game with a winner whose teams had as many humans each, at least one) updates every
// human's Glicko-2 rating against the other team as one composite opponent, all of them in one transaction
// (store.UpdateGroup: each new rating depends on the others' old ones). The Leaderboard ranks by it when
// Server.RatingLeaderboard is on; the ratings are kept in both modes. Each human gets one log line:
// "account <id>: game <n> rating: 1500 -> 1516 (rated: 2 v 2 humans)" or "... rating: not rated (2 v 3 humans)".

// rate applies the finished match's rating update (rating.Rate on the history's seats, so a live game and the seed
// from the stored matches follow the same rule).
func (hg *hostedGame) rate(rec historyRecord) {
	s := hg.s
	if s.Store == nil {
		return
	}
	m := rec.Match
	v := rating.Judge(m)
	var ids []int64
	for _, st := range m.Seats {
		if !st.Bot && st.UserID != 0 && st.PersonaID != 0 && st.Team >= 1 && st.Team <= 2 {
			ids = append(ids, st.UserID)
		}
	}
	if !v.Rated {
		for _, id := range ids {
			s.Log.Logf("blaze", "   account %d: game %d rating: %s", id, hg.game.ID, v.DescribeNotRated())
		}
		return
	}
	now := s.Now()
	var changes []rating.Change
	_, err := s.Store.UpdateGroup(ids, func(as []*store.Account) {
		byID := map[int64]*store.Account{}
		for _, a := range as {
			byID[a.UserID] = a
		}
		_, changes = rating.Rate(m, func(id int64) store.Rating {
			if a := byID[id]; a != nil {
				return a.History.Rating
			}
			return store.StartRating // an account deleted during the match: rated as new, not stored
		})
		for _, c := range changes {
			if a := byID[c.UserID]; a != nil {
				a.History = progress.ApplyRated(a.History, now, now, c.After, c.Won)
			}
		}
	})
	if err != nil {
		s.Log.Logf("blaze", "   (game %d: the ratings not stored: %v)", hg.game.ID, err)
		return
	}
	for _, c := range changes {
		s.Log.Logf("blaze", "   account %d: game %d rating: %s", c.UserID, hg.game.ID, c.Describe(v))
	}
}
