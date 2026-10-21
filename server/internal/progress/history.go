package progress

import (
	"math"
	"sort"
	"time"

	"dawngate/server/internal/store"
)

// The profile's shaper and role history (roadmap B7d; docs/ACCOUNT_CONTENT.md §11). Besides PlayerStats the
// profile asks for three keyed Stats groups (StatsManager_RequestPlayerStats 0x00AA6C60), each read by its own
// handler that takes the number after '=' in every key and the values by position:
//
//	ShaperStats         key <scope>=<shaper catalogID>, one row of 2 values: games, wins (all time); the
//	                    client keeps the 5 most played (StatsManager_OnShaperStatsResult 0x00AA5B80)
//	ShaperHistoryStats  same keys, rows of 8 values: games, wins, kills, deaths, assists, creep score, gold
//	                    ("Vim"), seconds played, one row per day (period offset); the client sums the rows,
//	                    divides the last six by the games and keeps the 4 most played (0x00AA5E90)
//	RoleHistoryStats    key <scope>=<role 1-4>, rows of 2 values: games, wins (0x00AA6590)
//
// The two history groups are asked for by day, the last 7 days (PTYP 3 = STAT_PERIOD_DAILY, PCTR 7;
// session g2_run2), ShaperStats for all time (PTYP 0, PCTR 1).

// The keyed stat groups.
const (
	ShaperStats        = "ShaperStats"
	ShaperHistoryStats = "ShaperHistoryStats"
	RoleHistoryStats   = "RoleHistoryStats"
)

// playerFinish.stats keys the history reads, besides won, kills, deaths, assists and durationMs.
const (
	// StatNoun is the asset id of the player's Noun (labsPlayerGlobalData.mpNounDef: the skin Noun, e.g.
	// alpha.Noun); content.ShaperOf turns it into the shaper's catalogID.
	StatNoun = "noun"
	// StatRole is the role the player played, as the RoleHistoryStats key: 1 + labsPlayerGlobalData.mRole
	// (Role*). 0 or missing: no role, the game isn't counted in the role history.
	StatRole   = "role"
	StatCreeps = "creeps" // creep score
	StatGold   = "gold"   // gold earned ("Vim")
)

// Roles, the RoleHistoryStats keys: 1 + the AssetData role enum (gladiator 0, tactician 1, hunter 2,
// predator 3; .rdata 0x0142CB10, as labsPlayerGlobalData.mRole and shaper select carry it). The profile's
// RoleStatsView draws the four in this order (gladiator, tactician, hunter, predator bars), and the client
// files key n under slot n-1 and refuses any other number (0x00AA6590).
const (
	RoleGladiator = 1
	RoleTactician = 2
	RoleHunter    = 3
	RolePredator  = 4
)

// RoleNames names the roles by key (RoleNames[0] = no role).
var RoleNames = [...]string{"none", "gladiator", "tactician", "hunter", "predator"}

// HistoryDays is how many UTC days of history an account keeps: what the client asks the history groups
// for (7 daily periods).
const HistoryDays = 7

// dayLayout is a HistoryDay's day.
const dayLayout = "2006-01-02"

// day returns t's UTC day at midnight.
func day(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// dayOffset is how many days before today day is (0 = today; negative for a day after it); ok false for a
// day that doesn't parse.
func dayOffset(today time.Time, d string) (int, bool) {
	t, err := time.Parse(dayLayout, d)
	if err != nil {
		return 0, false
	}
	return int(day(today).Sub(t).Hours() / 24), true
}

// u32 clamps a stat to uint32.
func u32(v int64) uint32 {
	return uint32(min(max(v, 0), math.MaxUint32))
}

// ApplyHistory adds one finished game to an account's history and returns the new history (h is not
// changed). shaper is the shaper's catalogID (0: unknown, the game isn't counted by shaper), role the
// RoleHistoryStats key (outside 1-4: not counted by role), game the host's playerFinish.stats, seconds the
// match length (finishGame.durationMs; the player's own durationMs wins if the host sent one). The game
// goes to the day of now (UTC); days more than HistoryDays - 1 days before it are dropped.
func ApplyHistory(h store.History, now time.Time, shaper uint32, role int64, won bool, game map[string]int64,
	seconds float64) store.History {
	out := h.Clone()
	if shaper == 0 && (role < RoleGladiator || role > RolePredator) {
		return out
	}
	win := uint32(0)
	if won {
		win = 1
	}
	secs := u32(int64(max(seconds, 0)))
	if ms := game[StatDurationMs]; ms > 0 {
		secs = u32(ms / 1000)
	}
	d := &out.Days[dayIndex(&out, now)]
	if shaper != 0 {
		if out.Shapers == nil {
			out.Shapers = map[uint32]store.GamesWon{}
		}
		all := out.Shapers[shaper]
		out.Shapers[shaper] = store.GamesWon{Games: all.Games + 1, Wins: all.Wins + win}
		if d.Shapers == nil {
			d.Shapers = map[uint32]store.ShaperDay{}
		}
		s := d.Shapers[shaper]
		s.Games++
		s.Wins += win
		s.Kills += u32(game[StatKills])
		s.Deaths += u32(game[StatDeaths])
		s.Assists += u32(game[StatAssists])
		s.Creeps += u32(game[StatCreeps])
		s.Gold += u32(game[StatGold])
		s.Seconds += secs
		d.Shapers[shaper] = s
	}
	if role >= RoleGladiator && role <= RolePredator {
		if d.Roles == nil {
			d.Roles = map[uint8]store.GamesWon{}
		}
		r := d.Roles[uint8(role)]
		d.Roles[uint8(role)] = store.GamesWon{Games: r.Games + 1, Wins: r.Wins + win}
	}
	pruneDays(&out, now)
	return out
}

// ApplyRanked adds one finished game to an account's Leaderboard counts (store.History.Ranked, all time, and the day
// of now's part of them, for the ranking a day ago) and returns the new history (h is not changed). Only a game that
// counts for the Leaderboard comes here (a PvP or custom game: blaze's hostedGame.ranked). Days more than
// HistoryDays - 1 days before now are dropped, as ApplyHistory drops them.
func ApplyRanked(h store.History, now time.Time, won bool) store.History {
	out := h.Clone()
	win := uint32(0)
	if won {
		win = 1
	}
	out.Ranked = store.GamesWon{Games: out.Ranked.Games + 1, Wins: out.Ranked.Wins + win}
	d := &out.Days[dayIndex(&out, now)]
	d.Ranked = store.GamesWon{Games: d.Ranked.Games + 1, Wins: d.Ranked.Wins + win}
	pruneDays(&out, now)
	return out
}

// ImportRanked adds one game played at the time at, before the Leaderboard had its own counts, to them (the operator's
// import from the server's old logs): to the all-time count always, and to at's UTC day (the day ApplyRanked would have
// chosen then) only while that day is still one of the HistoryDays days before now that an account keeps; days older
// than that are dropped, as ApplyRanked drops them. It returns the new history (h is not changed).
func ImportRanked(h store.History, now, at time.Time, won bool) store.History {
	out := h.Clone()
	win := uint32(0)
	if won {
		win = 1
	}
	out.Ranked = store.GamesWon{Games: out.Ranked.Games + 1, Wins: out.Ranked.Wins + win}
	if off, _ := dayOffset(now, day(at).Format(dayLayout)); off >= 0 && off < HistoryDays {
		d := &out.Days[dayIndex(&out, at)]
		d.Ranked = store.GamesWon{Games: d.Ranked.Games + 1, Wins: d.Ranked.Wins + win}
	}
	pruneDays(&out, now)
	return out
}

// dayIndex returns the index of now's UTC day in h.Days, adding the day in order if it has none.
func dayIndex(h *store.History, now time.Time) int {
	today := day(now).Format(dayLayout)
	i := sort.Search(len(h.Days), func(i int) bool { return h.Days[i].Day >= today })
	if i == len(h.Days) || h.Days[i].Day != today {
		h.Days = append(h.Days, store.HistoryDay{})
		copy(h.Days[i+1:], h.Days[i:])
		h.Days[i] = store.HistoryDay{Day: today}
	}
	return i
}

// pruneDays drops the days more than HistoryDays - 1 days before now (a day that doesn't parse stays).
func pruneDays(h *store.History, now time.Time) {
	kept := h.Days[:0]
	for _, dd := range h.Days {
		if off, ok := dayOffset(now, dd.Day); !ok || off < HistoryDays {
			kept = append(kept, dd)
		}
	}
	h.Days = kept
}

// Row is one EntityStats of a keyed stat group: its period offset (POFF: days before today for the daily
// groups, 0 for ShaperStats) and its values (STAT, in the group's stat order).
type Row struct {
	Offset int
	Values []uint32
}

// Keyed is one key of a keyed stat group: the number after '=' in its KEY (a shaper's catalogID, a role)
// and its rows.
type Keyed struct {
	ID   uint32
	Rows []Row
}

// ShaperStatsOf is the group ShaperStats of a history: per shaper (by catalogID) one row {games, wins} of
// all time. A shaper without games has no key: the client parses every key's number and looks it up as
// a catalogID.
func ShaperStatsOf(h store.History) []Keyed {
	var out []Keyed
	for _, id := range sortedKeys(h.Shapers) {
		if g := h.Shapers[id]; g.Games > 0 && id != 0 {
			out = append(out, Keyed{ID: id, Rows: []Row{{Values: []uint32{g.Games, g.Wins}}}})
		}
	}
	return out
}

// periods returns the history's days that fall into the request's periods (offset .. offset + count - 1
// days before today; count < 1 counts as 1, Blaze's default PCTR), oldest first, with their offsets.
func periods(h store.History, today time.Time, offset, count int) ([]store.HistoryDay, []int) {
	var days []store.HistoryDay
	var offs []int
	for _, d := range h.Days {
		off, ok := dayOffset(today, d.Day)
		if ok && off >= max(offset, 0) && off < offset+max(count, 1) {
			days, offs = append(days, d), append(offs, off)
		}
	}
	return days, offs
}

// ShaperHistoryOf is the group ShaperHistoryStats of a history for the request's periods: per shaper one
// row per day with games, {games, wins, kills, deaths, assists, creeps, gold, seconds}. A key never has
// zero games in all: the client divides by their sum (an integer division for the seconds).
func ShaperHistoryOf(h store.History, today time.Time, offset, count int) []Keyed {
	days, offs := periods(h, today, offset, count)
	rows := map[uint32][]Row{}
	for i, d := range days {
		for id, s := range d.Shapers {
			if s.Games > 0 && id != 0 {
				rows[id] = append(rows[id], Row{Offset: offs[i], Values: []uint32{s.Games, s.Wins, s.Kills, s.Deaths,
					s.Assists, s.Creeps, s.Gold, s.Seconds}})
			}
		}
	}
	return keyed(rows)
}

// RoleHistoryOf is the group RoleHistoryStats of a history for the request's periods: per role one row
// per day with games, {games, wins}.
func RoleHistoryOf(h store.History, today time.Time, offset, count int) []Keyed {
	days, offs := periods(h, today, offset, count)
	rows := map[uint32][]Row{}
	for i, d := range days {
		for role, g := range d.Roles {
			if g.Games > 0 && role >= RoleGladiator && role <= RolePredator {
				rows[uint32(role)] = append(rows[uint32(role)], Row{Offset: offs[i], Values: []uint32{g.Games, g.Wins}})
			}
		}
	}
	return keyed(rows)
}

// keyed orders rows by key, and each key's rows by offset.
func keyed(rows map[uint32][]Row) []Keyed {
	var out []Keyed
	for _, id := range sortedKeys(rows) {
		r := rows[id]
		sort.Slice(r, func(i, j int) bool { return r[i].Offset < r[j].Offset })
		out = append(out, Keyed{ID: id, Rows: r})
	}
	return out
}

func sortedKeys[V any](m map[uint32]V) []uint32 {
	keys := make([]uint32, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
