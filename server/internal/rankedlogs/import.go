package rankedlogs

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
)

// SettingKey is the server setting (store.SetSetting; the server_settings table every database since schema 4 has, which
// the server reads only by its own keys) that records that the import ran: what it added and when. A second --apply
// is refused while it is there, unless forced.
const SettingKey = "leaderboard_import"

// Options are one run's.
type Options struct {
	Paths []string       // log folders and files (Read)
	Loc   *time.Location // the zone the server wrote its log times in (its local time)
	Apply bool           // write; otherwise only print what would be written (a dry run)
	Force bool           // apply even though the import ran before (its games would count twice)
	Now   time.Time
}

// Summary is what a run found (and, with Apply, wrote).
type Summary struct {
	Files, Imported    int // files read, files imported
	Accounts           int // accounts given games
	Games, Wins        int // ranked player games added, and their wins
	Applied            bool
	Missing            int // accounts in the logs that the database doesn't have
	PreviouslyImported string
}

// ErrImportedBefore refuses a second --apply without --force.
var ErrImportedBefore = errors.New("the Leaderboard's games were imported from the logs before")

// played is one ranked game of an account.
type played struct {
	at  time.Time
	won bool
}

// tally is one account's games in the logs.
type tally struct {
	id                int64
	ranked            []played
	other             int // its other stored games in the imported files (practice, practice groups, unknown mode)
	all, allWins      int // every stored game in the imported files
	skipped, skipWins int // its stored games in the skipped files (for the comparison with the profile only)
	days              map[string]store.GamesWon
	rankedWins        int
	rankedByKind      map[string]int
}

// Run reads the logs, prints per account what they hold and what the import adds, and with o.Apply writes it into st
// in one transaction together with the SettingKey mark.
func Run(st *store.Store, o Options, out io.Writer) (Summary, error) {
	var sum Summary
	if o.Loc == nil {
		o.Loc = time.Local
	}
	files, err := Read(o.Paths, o.Loc)
	if err != nil {
		return sum, err
	}
	sum.Files = len(files)
	prev, changed, found, err := st.Setting(SettingKey)
	if err != nil {
		return sum, err
	}
	if found {
		sum.PreviouslyImported = fmt.Sprintf("%s (%s)", changed.UTC().Format("2006-01-02 15:04 UTC"), prev)
		if o.Apply && !o.Force {
			return sum, fmt.Errorf("%w on %s; --force imports them again (and counts the same games twice)",
				ErrImportedBefore, sum.PreviouslyImported)
		}
	}
	mode := "dry run: nothing is written; --apply writes it"
	if o.Apply {
		mode = "--apply: writing it into the database"
	}
	zone, offset := o.Now.In(o.Loc).Zone()
	fmt.Fprintf(out, "The Leaderboard's games from the server's logs (%s).\n", mode)
	fmt.Fprintf(out, "Log times read in this machine's time zone (%s, UTC%s now), where the server wrote them; a game "+
		"counts on its UTC day, as the server counts it.\n", zone, utcOffset(offset))
	if found {
		fmt.Fprintf(out, "NOTE: the import ran before: %s.\n", sum.PreviouslyImported)
	}

	tallies := map[int64]*tally{}
	get := func(id int64) *tally {
		t := tallies[id]
		if t == nil {
			t = &tally{id: id, days: map[string]store.GamesWon{}, rankedByKind: map[string]int{}}
			tallies[id] = t
		}
		return t
	}
	keep := o.Now.UTC().AddDate(0, 0, -(progress.HistoryDays - 1)).Format("2006-01-02")
	fmt.Fprintln(out, "\nLog files:")
	for _, f := range files {
		finished, ranked, results := 0, 0, 0
		for _, g := range f.Games {
			if g.Finished {
				finished++
			}
			isRanked, kind := g.Ranked()
			counted := false
			for _, r := range g.Results {
				if r.Failed {
					continue
				}
				if isRanked && !counted {
					ranked++
					counted = true
				}
				results++
				t := get(r.Account)
				if !f.Import {
					t.skipped++
					if r.Won {
						t.skipWins++
					}
					continue
				}
				t.all++
				if r.Won {
					t.allWins++
				}
				if !isRanked {
					t.other++
					continue
				}
				t.ranked = append(t.ranked, played{r.At, r.Won})
				t.rankedByKind[kind]++
				day := r.At.UTC().Format("2006-01-02")
				dd := t.days[day]
				dd.Games++
				if r.Won {
					dd.Wins++
					t.rankedWins++
				}
				t.days[day] = dd
			}
		}
		if f.Import {
			sum.Imported++
		}
		fmt.Fprintf(out, "  %s: %s; %d games finished, %d PvP or custom games with players, %d player results\n", f.Name, f.Reason,
			finished, ranked, results)
		for _, n := range f.Notes {
			fmt.Fprintf(out, "    note: %s\n", n)
		}
	}

	var ids []int64
	for id, t := range tallies {
		if t.all > 0 || t.skipped > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	fmt.Fprintln(out, "\nAccounts:")
	var write []int64
	for _, id := range ids {
		t := tallies[id]
		a, ok := st.Account(id)
		if !ok {
			if len(t.ranked) > 0 {
				sum.Missing++
				fmt.Fprintf(out, "  account %d: not in the database (deleted?): its %d games are skipped\n", id, len(t.ranked))
			}
			continue
		}
		name := ""
		if len(a.Personas) > 0 {
			name = a.Personas[0].Name
		}
		fmt.Fprintf(out, "  account %d %q (login %q):\n", id, name, a.Email)
		fmt.Fprintf(out, "    in the imported logs: %d games, %d wins; PvP or custom %d games, %d wins%s; other (practice) %d\n",
			t.all, t.allWins, len(t.ranked), t.rankedWins, kinds(t.rankedByKind), t.other)
		if len(t.days) > 0 {
			var days []string
			for d := range t.days {
				days = append(days, d)
			}
			sort.Strings(days)
			var parts []string
			for _, d := range days {
				p := fmt.Sprintf("%s %d/%d", d, t.days[d].Games, t.days[d].Wins)
				if d < keep {
					p += " (too old for the daily ranking: all time only)"
				}
				parts = append(parts, p)
			}
			fmt.Fprintf(out, "    per UTC day (games/wins): %s\n", strings.Join(parts, ", "))
		}
		if t.skipped > 0 {
			fmt.Fprintf(out, "    in the skipped logs: %d games, %d wins (not imported)\n", t.skipped, t.skipWins)
		}
		playedAll := stat(a, "PLAYED")
		winsAll := stat(a, "WINS")
		explained, explainedWins := t.all+t.skipped, t.allWins+t.skipWins
		fmt.Fprintf(out, "    profile (every game, practice included): %d games, %d wins\n", playedAll, winsAll)
		if playedAll > explained || winsAll > explainedWins {
			fmt.Fprintf(out, "    ! the profile counts %d games and %d wins more than these logs show: games from before the "+
				"first log (or from a log not read); their mode is unknown, so they are not added\n",
				max(playedAll-explained, 0), max(winsAll-explainedWins, 0))
		} else if playedAll < explained {
			fmt.Fprintf(out, "    ! the logs show %d games more than the profile counts (an account made again?)\n", explained-playedAll)
		}
		after := a.History.Ranked
		after.Games += uint32(len(t.ranked))
		after.Wins += uint32(t.rankedWins)
		fmt.Fprintf(out, "    Leaderboard: %d games, %d wins now -> %d games, %d wins\n", a.History.Ranked.Games,
			a.History.Ranked.Wins, after.Games, after.Wins)
		if len(t.ranked) > 0 {
			write = append(write, id)
			sum.Accounts++
			sum.Games += len(t.ranked)
			sum.Wins += t.rankedWins
		}
	}
	fmt.Fprintf(out, "\nTotal: %d of %d log files imported; %d accounts gain %d PvP or custom games (%d wins).\n",
		sum.Imported, sum.Files, sum.Accounts, sum.Games, sum.Wins)
	if !o.Apply {
		if found && !o.Force {
			fmt.Fprintln(out, "--apply would be refused: the import ran before (--force imports again, counting the games twice).")
		}
		return sum, nil
	}
	if sum.Imported == 0 {
		fmt.Fprintln(out, "Nothing written: no log file to import.")
		return sum, nil
	}
	var names []string
	for _, f := range files {
		if f.Import {
			names = append(names, f.Name)
		}
	}
	mark := fmt.Sprintf("%d PvP or custom games (%d wins) of %d accounts from %d log files: %s", sum.Games, sum.Wins,
		sum.Accounts, sum.Imported, strings.Join(names, ", "))
	err = st.UpdateMany(write, func(a *store.Account) {
		t := tallies[a.UserID]
		sort.SliceStable(t.ranked, func(i, j int) bool { return t.ranked[i].at.Before(t.ranked[j].at) })
		for _, p := range t.ranked {
			a.History = progress.ImportRanked(a.History, o.Now, p.at, p.won)
		}
	}, SettingKey, mark, o.Now)
	if err != nil {
		return sum, err
	}
	sum.Applied = true
	fmt.Fprintln(out, "Written. The server can be started again.")
	return sum, nil
}

// kinds lists the ranked games by kind, " (pvp game 3, custom game 1)".
func kinds(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var parts []string
	for _, k := range ks {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// stat is a PlayerStats value of the account (0 when missing).
func stat(a store.Account, name string) int {
	v, _ := strconv.Atoi(a.Stats[progress.PlayerStats][name])
	return v
}

// utcOffset formats a zone offset in seconds as +02:00.
func utcOffset(sec int) string {
	sign := "+"
	if sec < 0 {
		sign, sec = "-", -sec
	}
	return fmt.Sprintf("%s%02d:%02d", sign, sec/3600, sec/60%60)
}
