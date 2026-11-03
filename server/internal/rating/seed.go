package rating

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
)

// SeedKey is the server setting that marks the seed as done (server_settings, as leaderboard_import marks the
// Leaderboard's import from old logs): its value says what the seed found.
const SeedKey = "rating_seed"

// seedPage is how many stored matches the seed reads at a time.
const seedPage = store.MaxMatchPage

// SeedReport is what Seed did.
type SeedReport struct {
	Already  bool   // the mark was there: nothing done
	Mark     string // the mark's value (the earlier seed's, when Already)
	Matches  int    // stored matches replayed
	Rated    int    // of them rated
	Accounts int    // accounts given a rating
	Missing  int    // accounts the rated matches name that no longer exist (left out)
}

// String is the report for the server's log.
func (r SeedReport) String() string {
	if r.Already {
		return "seeded before (" + r.Mark + ")"
	}
	return r.Mark
}

// seedEvent is one rated game of one account in the replay.
type seedEvent struct {
	at   time.Time
	next store.Rating
	won  bool
}

// Seed gives every account its rating from the stored match history once (the schema's rating columns start everyone
// at 1500): it replays every stored match in the order it was stored (the match id: the order the matches ended) by the
// same rule and update as a live game (Rate), then writes each named account's rating, rated games and wins and the
// history days' rated parts (progress.ApplyRated, the days of the last HistoryDays only) and the mark SeedKey, all in
// one transaction. With the mark already set it does nothing. An account a match names but the store no longer has is
// still rated in the replay (its rating counts in its opponents' composite) but not written.
func Seed(st *store.Store, now time.Time) (SeedReport, error) {
	if v, _, found, err := st.Setting(SeedKey); err != nil {
		return SeedReport{}, err
	} else if found {
		return SeedReport{Already: true, Mark: v}, nil
	}
	var rep SeedReport
	cur := map[int64]store.Rating{}
	events := map[int64][]seedEvent{}
	current := func(id int64) store.Rating {
		if r, ok := cur[id]; ok {
			return r
		}
		return store.StartRating
	}
	for after := int64(0); ; {
		ms, err := st.MatchesAfter(after, seedPage)
		if err != nil {
			return SeedReport{}, err
		}
		for _, m := range ms {
			after = m.ID
			rep.Matches++
			v, changes := Rate(m, current)
			if !v.Rated {
				continue
			}
			rep.Rated++
			for _, c := range changes {
				cur[c.UserID] = c.After
				events[c.UserID] = append(events[c.UserID], seedEvent{at: m.Ended, next: c.After, won: c.Won})
			}
		}
		if len(ms) < seedPage {
			break
		}
	}
	var ids []int64
	for _, id := range slices.Sorted(maps.Keys(events)) {
		if _, ok := st.Account(id); ok {
			ids = append(ids, id)
		} else {
			rep.Missing++
		}
	}
	rep.Accounts = len(ids)
	rep.Mark = fmt.Sprintf("%d stored matches replayed, %d rated, %d accounts rated", rep.Matches, rep.Rated, rep.Accounts)
	if rep.Missing > 0 {
		rep.Mark += fmt.Sprintf(", %d no longer exist", rep.Missing)
	}
	rep.Mark += ", at " + now.UTC().Format(time.RFC3339)
	err := st.UpdateMany(ids, func(a *store.Account) {
		a.History.Rating = store.Rating{}
		for i := range a.History.Days {
			a.History.Days[i].Rated, a.History.Days[i].RatingBefore = store.GamesWon{}, 0
		}
		for _, e := range events[a.UserID] {
			a.History = progress.ApplyRated(a.History, now, e.at, e.next, e.won)
		}
	}, SeedKey, rep.Mark, now)
	if err != nil {
		return SeedReport{}, err
	}
	return rep, nil
}
