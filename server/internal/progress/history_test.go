package progress

import (
	"reflect"
	"testing"
	"time"

	"dawngate/server/internal/store"
)

// day0 is a fixed UTC noon; the tests play on day0 and the days after it.
var day0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func at(days int) time.Time { return day0.AddDate(0, 0, days) }

// Games add up by shaper (all time and per day) and by role (per day); the input isn't changed; a game
// without shaper and role changes nothing; days older than HistoryDays - 1 days are dropped.
func TestApplyHistory(t *testing.T) {
	const x, y = 0x10000001, 0x10000002
	game := map[string]int64{StatKills: 3, StatDeaths: 1, StatAssists: 2, StatCreeps: 40, StatGold: 5000}
	var h store.History
	h = ApplyHistory(h, at(0), x, RoleGladiator, true, game, 720.9)
	before := h.Clone()
	h2 := ApplyHistory(h, at(2), x, RoleGladiator, false, map[string]int64{StatKills: -5, StatDurationMs: 61500}, 900)
	if !reflect.DeepEqual(h, before) {
		t.Fatal("ApplyHistory changed its input")
	}
	h = ApplyHistory(h2, at(2), y, 0, true, nil, 60) // no role: only by shaper
	h = ApplyHistory(h, at(2), 0, RoleHunter, false, nil, 60)
	if got := ApplyHistory(h, at(2), 0, 5, true, game, 60); !reflect.DeepEqual(got, h) {
		t.Error("a game without shaper and role changed the history")
	}
	want := store.History{
		Shapers: map[uint32]store.GamesWon{x: {Games: 2, Wins: 1}, y: {Games: 1, Wins: 1}},
		Days: []store.HistoryDay{
			{Day: "2026-10-01", Shapers: map[uint32]store.ShaperDay{x: {Games: 1, Wins: 1, Kills: 3, Deaths: 1, Assists: 2,
				Creeps: 40, Gold: 5000, Seconds: 720}}, Roles: map[uint8]store.GamesWon{RoleGladiator: {Games: 1, Wins: 1}}},
			{Day: "2026-10-03", Shapers: map[uint32]store.ShaperDay{x: {Games: 1, Seconds: 61}, y: {Games: 1, Wins: 1, Seconds: 60}},
				Roles: map[uint8]store.GamesWon{RoleGladiator: {Games: 1}, RoleHunter: {Games: 1}}},
		},
	}
	if !reflect.DeepEqual(h, want) {
		t.Fatalf("history\n got %+v\nwant %+v", h, want)
	}
	// six days after day0's game it is still kept (offset 6); seven days after, it is dropped
	if got := ApplyHistory(h, at(6), y, 0, false, nil, 60); len(got.Days) != 3 || got.Days[0].Day != "2026-10-01" {
		t.Errorf("day 6: %+v", got.Days)
	}
	got := ApplyHistory(h, at(7), y, 0, false, nil, 60)
	if len(got.Days) != 2 || got.Days[0].Day != "2026-10-03" || got.Days[1].Day != "2026-10-08" || got.Shapers[x].Games != 2 {
		t.Errorf("day 7: %+v", got)
	}
	// a game on an earlier day than the last one (the clock went back) goes in order
	got = ApplyHistory(h, at(1), y, 0, false, nil, 60)
	if len(got.Days) != 3 || got.Days[1].Day != "2026-10-02" {
		t.Errorf("earlier day: %+v", got.Days)
	}
}

// The Leaderboard's counts add up all time and per day, beside the day's shaper and role games; the input isn't
// changed; a game on a day without other games adds the day; old days are dropped as ApplyHistory drops them.
func TestApplyRanked(t *testing.T) {
	const x = 0x10000001
	h := ApplyHistory(store.History{}, at(0), x, RoleGladiator, true, nil, 60)
	before := h.Clone()
	h2 := ApplyRanked(h, at(0), true)
	if !reflect.DeepEqual(h, before) {
		t.Fatal("ApplyRanked changed its input")
	}
	h2 = ApplyRanked(ApplyRanked(h2, at(2), false), at(2), true)
	want := store.History{
		Shapers: map[uint32]store.GamesWon{x: {Games: 1, Wins: 1}},
		Days: []store.HistoryDay{
			{Day: "2026-10-01", Shapers: map[uint32]store.ShaperDay{x: {Games: 1, Wins: 1, Seconds: 60}},
				Roles: map[uint8]store.GamesWon{RoleGladiator: {Games: 1, Wins: 1}}, Ranked: store.GamesWon{Games: 1, Wins: 1}},
			{Day: "2026-10-03", Ranked: store.GamesWon{Games: 2, Wins: 1}},
		},
		Ranked: store.GamesWon{Games: 3, Wins: 2},
	}
	if !reflect.DeepEqual(h2, want) {
		t.Fatalf("history\n got %+v\nwant %+v", h2, want)
	}
	if got := ApplyRanked(h2, at(7), false); len(got.Days) != 2 || got.Days[0].Day != "2026-10-03" ||
		got.Ranked != (store.GamesWon{Games: 4, Wins: 2}) {
		t.Errorf("day 7: %+v", got)
	}
}

// ImportRanked adds an old game to the all-time count, and to its own day only while that day is still kept (the
// HistoryDays days up to now); a live day's count is added to, never replaced.
func TestImportRanked(t *testing.T) {
	const x = 0x10000001
	now := at(8) // 2026-10-09: the days kept are 2026-10-03 .. 2026-10-09
	h := ApplyRanked(ApplyHistory(store.History{}, now, x, RoleGladiator, true, nil, 60), now, true)
	before := h.Clone()
	got := ImportRanked(h, now, at(0), true) // 2026-10-01: too old for a day
	if !reflect.DeepEqual(h, before) {
		t.Fatal("ImportRanked changed its input")
	}
	got = ImportRanked(ImportRanked(got, now, at(3), false), now, at(3), true) // 2026-10-04
	got = ImportRanked(got, now, at(8), false)                                 // today, beside the live game
	want := store.History{
		Shapers: map[uint32]store.GamesWon{x: {Games: 1, Wins: 1}},
		Days: []store.HistoryDay{
			{Day: "2026-10-04", Ranked: store.GamesWon{Games: 2, Wins: 1}},
			{Day: "2026-10-09", Shapers: map[uint32]store.ShaperDay{x: {Games: 1, Wins: 1, Seconds: 60}},
				Roles: map[uint8]store.GamesWon{RoleGladiator: {Games: 1, Wins: 1}}, Ranked: store.GamesWon{Games: 2, Wins: 1}},
		},
		Ranked: store.GamesWon{Games: 5, Wins: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history\n got %+v\nwant %+v", got, want)
	}
}

// The groups' keys and rows: ShaperStats all time by catalogID; the history groups per day within the
// requested periods (offset, count), each key's rows by offset; days after today, keys without games and
// roles outside 1-4 are left out.
func TestHistoryGroups(t *testing.T) {
	h := store.History{
		Shapers: map[uint32]store.GamesWon{9: {Games: 3, Wins: 2}, 4: {Games: 1}, 5: {}},
		Days: []store.HistoryDay{
			{Day: "2026-10-01", Shapers: map[uint32]store.ShaperDay{9: {Games: 2, Wins: 2, Kills: 7, Seconds: 1000}},
				Roles: map[uint8]store.GamesWon{2: {Games: 2, Wins: 2}, 7: {Games: 1}}},
			{Day: "2026-10-03", Shapers: map[uint32]store.ShaperDay{9: {Games: 1, Deaths: 4}, 4: {Games: 1, Gold: 10}, 6: {}},
				Roles: map[uint8]store.GamesWon{2: {Games: 1}, 4: {Games: 1}}},
			{Day: "2026-10-05", Shapers: map[uint32]store.ShaperDay{9: {Games: 1}}}, // after today
			{Day: "bad", Shapers: map[uint32]store.ShaperDay{9: {Games: 1}}},
		},
	}
	today := at(2)
	if got, want := ShaperStatsOf(h), []Keyed{{4, []Row{{0, []uint32{1, 0}}}}, {9, []Row{{0, []uint32{3, 2}}}}}; !reflect.DeepEqual(got, want) {
		t.Errorf("ShaperStatsOf %+v", got)
	}
	want := []Keyed{
		{4, []Row{{0, []uint32{1, 0, 0, 0, 0, 0, 10, 0}}}},
		{9, []Row{{0, []uint32{1, 0, 0, 4, 0, 0, 0, 0}}, {2, []uint32{2, 2, 7, 0, 0, 0, 0, 1000}}}},
	}
	if got := ShaperHistoryOf(h, today, 0, 7); !reflect.DeepEqual(got, want) {
		t.Errorf("ShaperHistoryOf\n got %+v\nwant %+v", got, want)
	}
	if got := ShaperHistoryOf(h, today, 1, 2); !reflect.DeepEqual(got, []Keyed{{9, []Row{{2, []uint32{2, 2, 7, 0, 0, 0, 0, 1000}}}}}) {
		t.Errorf("ShaperHistoryOf(1, 2) %+v", got)
	}
	if got := ShaperHistoryOf(h, today, 0, 0); len(got) != 2 || len(got[1].Rows) != 1 { // PCTR 0 counts as 1: today only
		t.Errorf("ShaperHistoryOf(0, 0) %+v", got)
	}
	wantRoles := []Keyed{{2, []Row{{0, []uint32{1, 0}}, {2, []uint32{2, 2}}}}, {4, []Row{{0, []uint32{1, 0}}}}}
	if got := RoleHistoryOf(h, today, 0, 7); !reflect.DeepEqual(got, wantRoles) {
		t.Errorf("RoleHistoryOf\n got %+v\nwant %+v", got, wantRoles)
	}
	if got := RoleHistoryOf(store.History{}, today, 0, 7); got != nil {
		t.Errorf("RoleHistoryOf(empty) %+v", got)
	}
}
