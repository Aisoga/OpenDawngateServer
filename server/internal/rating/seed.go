package rating

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
)

// SeedKey is the server setting that marks the seed as done (server_settings, as leaderboard_import marks the
// Leaderboard's import from old logs): its value says what the seed found.
const SeedKey = "rating_seed"

// SeedModesKey marks the seed of the draft and custom ratings as done (M30, MMR1; SeedModes), apart from SeedKey so
// that a server whose standard ratings were seeded before gets the new types once, without touching the standard.
const SeedModesKey = "rating_seed_modes"

// seedPage is how many stored matches the seed reads at a time.
const seedPage = store.MaxMatchPage

// SeedReport is what Seed or SeedModes did.
type SeedReport struct {
	Already  bool           // the mark was there: nothing done
	Mark     string         // the mark's value (the earlier seed's, when Already)
	Matches  int            // stored matches replayed
	Rated    int            // of them rated (in the seed's types)
	ByMode   map[string]int // the rated ones by game type
	Accounts int            // accounts given a rating
	Missing  int            // accounts the rated matches name that no longer exist (left out)
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
	mode string
	at   time.Time
	next store.Rating
	won  bool
}

// Seed gives every account its standard rating from the stored match history once (the schema's rating columns start
// everyone at 1500): it replays every stored match in the order it was stored (the match id: the order the matches
// ended) by the same rule and update as a live game (Rate), then writes each named account's rating, rated games and
// wins and the history days' rated parts (progress.ApplyRated, the days of the last HistoryDays only) and the mark
// SeedKey, all in one transaction. With the mark already set it does nothing. An account a match names but the store
// no longer has is still rated in the replay (its rating counts in its opponents' composite) but not written. Only the
// Standard type's matches are rated here (a match with rules was never rated before M30: the same result); the draft
// and custom ratings are SeedModes'.
func Seed(st *store.Store, now time.Time) (SeedReport, error) {
	return seed(st, now, SeedKey, []string{Standard})
}

// SeedModes gives every account its draft and custom ratings from the stored match history once (M30's decision 7):
// Seed's replay over the Draft and Custom matches only, oldest first, with the mark SeedModesKey. The standard rating,
// rated games and days are never read or written (rating_seed stays as it was).
func SeedModes(st *store.Store, now time.Time) (SeedReport, error) {
	return seed(st, now, SeedModesKey, []string{Draft, Custom})
}

// seed replays the stored matches of the game types modes and writes those types' ratings, with the mark key.
func seed(st *store.Store, now time.Time, key string, modes []string) (SeedReport, error) {
	if v, _, found, err := st.Setting(key); err != nil {
		return SeedReport{}, err
	} else if found {
		return SeedReport{Already: true, Mark: v}, nil
	}
	rep := SeedReport{ByMode: map[string]int{}}
	cur := map[string]map[int64]store.Rating{} // by type, by account
	for _, mode := range modes {
		cur[mode] = map[int64]store.Rating{}
	}
	events := map[int64][]seedEvent{}
	current := func(id int64, mode string) store.Rating {
		if r, ok := cur[mode][id]; ok {
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
			if !slices.Contains(modes, ModeOf(m)) {
				continue
			}
			v, changes := Rate(m, current)
			if !v.Rated {
				continue
			}
			rep.Rated++
			rep.ByMode[v.Mode]++
			for _, c := range changes {
				cur[v.Mode][c.UserID] = c.After
				events[c.UserID] = append(events[c.UserID], seedEvent{mode: v.Mode, at: m.Ended, next: c.After, won: c.Won})
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
	rated := fmt.Sprintf("%d rated", rep.Rated)
	if len(modes) > 1 || modes[0] != Standard {
		var parts []string
		for _, mode := range modes {
			parts = append(parts, fmt.Sprintf("%s %d", mode, rep.ByMode[mode]))
		}
		rated += " (" + strings.Join(parts, ", ") + ")"
	}
	rep.Mark = fmt.Sprintf("%d stored matches replayed, %s, %d accounts rated", rep.Matches, rated, rep.Accounts)
	if rep.Missing > 0 {
		rep.Mark += fmt.Sprintf(", %d no longer exist", rep.Missing)
	}
	rep.Mark += ", at " + now.UTC().Format(time.RFC3339)
	err := st.UpdateMany(ids, func(a *store.Account) {
		for _, mode := range modes {
			a.History.SetRatingIn(mode, store.Rating{})
			for i := range a.History.Days {
				a.History.Days[i].SetRatedIn(mode, store.ModeDay{})
			}
		}
		for _, e := range events[a.UserID] {
			a.History = progress.ApplyRated(a.History, e.mode, now, e.at, e.next, e.won)
		}
	}, key, rep.Mark, now)
	if err != nil {
		return SeedReport{}, err
	}
	return rep, nil
}
