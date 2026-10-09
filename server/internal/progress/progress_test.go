package progress

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/golden"
)

// seq is a Rand that returns its values in turn (then repeats the last).
type seq struct {
	v []float64
	i int
}

func (s *seq) Float64() float64 {
	x := s.v[min(s.i, len(s.v)-1)]
	s.i++
	return x
}

func fixture(t *testing.T) *content.Content {
	t.Helper()
	c, err := content.Build(filepath.Join(golden.Dir(), "assetdata"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// build854Loot is a content file with build 854's loot data that postgame.py uses (bag names and scores,
// the FTUE bags and loot: Resonance perk shape 0x10A4638F, FlatPower gem 0x10A46383; made-up max counts).
const build854Loot = `{"format": "dawngate-content-2", "items": [
 {"catalogID": 279208835, "name": "FlatPower", "type": "gem", "itemType": 3, "status": "none", "maxOwnable": 16},
 {"catalogID": 279208847, "name": "Resonance", "type": "perk", "itemType": 4, "status": "none", "maxOwnable": 1}],
 "lootBags": [{"name": "Bronze", "requiredBagScore": 25, "id": 1325899411}, {"name": "Silver", "requiredBagScore": 468, "id": 1677840772},
  {"name": "Gold", "requiredBagScore": 697, "id": 3272128905}, {"name": "Platinum", "requiredBagScore": 830, "id": 272547319},
  {"name": "Diamond", "requiredBagScore": 945, "id": 3237931193}],
 "ftueLoot": [279208847, 279208835, 279208835],
 "lootTuning": {"ftueBag": "Platinum", "defaultBag": "Bronze", "firstWinPoints": 100, "wonGamePoints": 100}}`

func load(t *testing.T, raw string) *content.Content {
	t.Helper()
	path := filepath.Join(t.TempDir(), "content.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := content.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func none(uint32) uint32 { return 0 }

// The formulas against re/scripts/postgame.py (and its documented ranges).
func TestFormulas(t *testing.T) {
	for _, c := range []struct {
		r    float64
		want int
	}{{0, 67}, {0.5, 218}, {1, 525}} {
		if got := BaseRoll(c.r); got != c.want {
			t.Errorf("BaseRoll(%v) = %d, want %d", c.r, got, c.want)
		}
	}
	if w, a := OutcomePoints(true, 720); w != 200 || a != 10 {
		t.Errorf("OutcomePoints(won, 12 min) = %d, %d", w, a)
	}
	if w, a := OutcomePoints(false, 3600*2); w != 0 || a != 50 { // capped at 55 minutes: 50.05
		t.Errorf("OutcomePoints(lost, 2 h) = %d, %d", w, a)
	}
	for _, c := range []struct {
		bag     string
		minutes float64
		desc    uint32
		want    float32
	}{
		{"Bronze", 12, 0x50C, 12.56}, // practice: 5 + 0.63 m
		{"Bronze", 40, 0x50C, 25.8},  // practice after 30 min: 17 + 0.22 m
		{"Bronze", 60, 0x50C, 28},    // capped at 50 min
		{"Bronze", 10, 1, 24.65},     // matchmade Bronze: 22.4 + 0.225 m
		{"Silver", 20, 1, 37},        // 25 + 0.6 m
		{"Gold", 40, 1, 56.2},        // 47.2 + 0.225 m
		{"Platinum", 30, 1, 62.6},    // 44 + 0.62 m
		{"Diamond", 100, 1, 87.55},   // 76 + 0.21 * 55
		{"Copper", 10, 1, 0},         // not a tier the Lua knows
	} {
		got := ECAward(c.bag, c.minutes*60, true, c.desc)
		if d := got - c.want; d > 0.01 || d < -0.01 {
			t.Errorf("ECAward(%s, %v min, %#x) = %v, want %v", c.bag, c.minutes, c.desc, got, c.want)
		}
	}
	if got := ECAward("Bronze", 600, false, 0x50C); got != 0 {
		t.Errorf("practice not allowed: %v", got)
	}
}

// A won 12-minute FTUE practice game with roll 0.5 is postgame.py's golden C1 04 (MATCH_FLOW.md):
// Platinum, 12 Destiny, points 218/200/10, the three FTUE items, the FTUE flag.
func TestPracticeRewardsMatchPostgame(t *testing.T) {
	c := load(t, build854Loot)
	r := Compute(c, none, Game{Descriptors: 0x50C, Won: true, Seconds: 720}, &seq{v: []float64{0.5}})
	want := Rewards{Bag: "Platinum", LootBag: 0x103EBDF7, Destiny: 12, BaseRoll: 0xda, WinPoints: 0xc8, WinAltPoints: 0x0a,
		Items: []Item{{CatalogID: 0x10A4638F}, {CatalogID: 0x10A46383}, {CatalogID: 0x10A46383}}, FTUE: true}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("won FTUE practice:\n got %+v\nwant %+v", r, want)
	}
	// lost: the default bag, no items, still the FTUE flag
	r = Compute(c, none, Game{Descriptors: 0x50C, Seconds: 300}, &seq{v: []float64{0}})
	if r.Bag != "Bronze" || r.LootBag != 0x4F079E93 || len(r.Items) != 0 || !r.FTUE || r.Destiny != 8 || r.WinPoints != 0 {
		t.Errorf("lost practice: %+v", r)
	}
	// the perk shape already owned (max 1): a duplicate worth crafting currency; the gems still count
	owned := func(id uint32) uint32 {
		if id == 0x10A4638F {
			return 1
		}
		return 15 // one gem below the max of 16: the first counts, the second is a duplicate
	}
	r = Compute(c, owned, Game{Descriptors: 0x50C, Won: true, Seconds: 720}, &seq{v: []float64{0.5}})
	if want := []Item{{0x10A4638F, true, 50}, {0x10A46383, false, 0}, {0x10A46383, true, 15}}; !reflect.DeepEqual(r.Items, want) || r.CC != 65 {
		t.Errorf("duplicates: %+v CC %d", r.Items, r.CC)
	}
}

// Matchmade games: the bag by score, the first win of the day, the drop table.
func TestMatchmadeRewards(t *testing.T) {
	c := load(t, build854Loot)
	// roll 1 (525) + win 200 + 10 + first win 100 = 835: Platinum (830)
	r := Compute(c, none, Game{Descriptors: DescMatchmade, Won: true, Seconds: 720, FirstWin: true}, &seq{v: []float64{1}})
	if r.Bag != "Platinum" || r.Score() != 835 || r.FirstWinPoints != 100 || r.FTUE || r.Destiny != 52 {
		t.Errorf("won matchmade: %+v score %d", r, r.Score())
	}
	// the first win counts only for a win; roll 0 (67) + 10: Bronze
	r = Compute(c, none, Game{Descriptors: DescMatchmade, Seconds: 720, FirstWin: true}, &seq{v: []float64{0}})
	if r.Bag != "Bronze" || r.FirstWinPoints != 0 || r.Score() != 77 {
		t.Errorf("lost matchmade: %+v", r)
	}

	// the fixture's drop table: Silverish drops two Shiny items (2_MAX_SHAPES 0.75: Spark; GEMS 0.25: RedGem)
	f := fixture(t)
	bag, _ := f.Bag("Silverish")
	if got := Drops(f, bag, &seq{v: []float64{0.1, 0, 0.9, 0}}); !reflect.DeepEqual(got, []uint32{0x10000006, 0x10000007}) {
		t.Errorf("drops %x", got)
	}
	if got := PoolItems(f, "16_MAX_SHAPES"); len(got) != 0 {
		t.Errorf("16_MAX_SHAPES: %x", got)
	}
	if got := PoolItems(f, "GEMS"); !reflect.DeepEqual(got, []uint32{0x10000007}) { // BlueGem has no item
		t.Errorf("GEMS: %x", got)
	}
	// a score of 400 reaches Silverish; Spark owned twice (its max) is a duplicate
	owned := func(id uint32) uint32 { return map[uint32]uint32{0x10000006: 2}[id] }
	r = Compute(f, owned, Game{Descriptors: DescMatchmade, Won: true, Seconds: 600}, &seq{v: []float64{0.5, 0.1, 0, 0.1, 0}})
	if r.Bag != "Silverish" || len(r.Items) != 2 || !r.Items[0].Duplicate || !r.Items[1].Duplicate || r.CC != 100 {
		t.Errorf("fixture matchmade: %+v", r)
	}
	// no content: no bag, no items; practice Destiny all the same
	if r := Compute(nil, none, Game{Descriptors: 0x50C, Won: true, Seconds: 600}, &seq{v: []float64{0.5}}); r.Bag != "" || r.Destiny != 11 || !r.FTUE { // the practice formula needs no bag
		t.Errorf("no content: %+v", r)
	}
}

// D8c: a matchmade game's karma votes count in the bag score (10 points per vote [assumed]); a practice game
// ignores them.
func TestKarma(t *testing.T) {
	if k := (Karma{Party: 1, Team: 1, Enemy: 2}); k.Votes() != 4 || k.Points() != 40 { // postgame.py karma's example
		t.Errorf("karma %+v: %d votes, %d points", k, k.Votes(), k.Points())
	}
	for d, want := range map[uint32]bool{DescMatchmade: true, DescMatchmade | DescRanked: true, 0x50C: false,
		DescMatchmade | DescSoloPractice: false, DescCustom: false, 0: false} {
		if got := (Game{Descriptors: d}).KarmaVote(); got != want {
			t.Errorf("KarmaVote(%#x) = %v", d, got)
		}
	}
	c := load(t, build854Loot)
	// a lost 10-minute matchmade game, roll 0.9: 448 + 9 = 457, Bronze; with a team and an enemy vote 477, Silver
	g := Game{Descriptors: DescMatchmade, Seconds: 600}
	r := Compute(c, none, g, &seq{v: []float64{0.9}})
	if r.Bag != "Bronze" || r.Score() != 457 || r.Karma != nil || r.KarmaPoints != 0 || r.Destiny != 24 {
		t.Errorf("without karma: %+v score %d", r, r.Score())
	}
	g.Karma = &Karma{Team: 1, Enemy: 1}
	r = Compute(c, none, g, &seq{v: []float64{0.9}})
	if r.Bag != "Silver" || r.Score() != 477 || r.KarmaPoints != 20 || r.Karma == nil || *r.Karma != *g.Karma || r.Destiny != 34 {
		t.Errorf("with karma: %+v score %d", r, r.Score())
	}
	g.Karma.Enemy = 5 // r keeps its own copy
	if r.Karma.Enemy != 1 {
		t.Errorf("karma not copied: %+v", r.Karma)
	}
	// counted but no votes: karma present, 0 points
	r = Compute(c, none, Game{Descriptors: DescMatchmade, Seconds: 600, Karma: &Karma{}}, &seq{v: []float64{0.9}})
	if r.Bag != "Bronze" || r.Karma == nil || r.KarmaPoints != 0 {
		t.Errorf("no votes: %+v", r)
	}
	// a practice game has no karma vote: the votes are ignored and the rewards stay as they were
	p := Compute(c, none, Game{Descriptors: 0x50C, Seconds: 600, Karma: &Karma{Enemy: 3}}, &seq{v: []float64{0.9}})
	if want := Compute(c, none, Game{Descriptors: 0x50C, Seconds: 600}, &seq{v: []float64{0.9}}); !reflect.DeepEqual(p, want) ||
		p.Karma != nil {
		t.Errorf("practice with karma: %+v, want %+v", p, want)
	}
}

func TestPlayerStats(t *testing.T) {
	s := ApplyPlayerStats(nil, true, map[string]int64{StatKills: 3, StatAssists: 2, StatDeaths: 1, StatBestStreak: 3, StatDoubleKills: 1})
	s = ApplyPlayerStats(s, false, map[string]int64{StatKills: 1, StatAssists: 5, StatTripleKills: 1, StatOmniKills: -4})
	want := map[string]string{"PLAYED": "2", "WINS": "1", "KILLS": "4", "ASSISTS": "7", "KILLSINGAME": "3",
		"ASSISTSINGAME": "5", "STREAKINGAME": "3", "DOUBLEKILLS": "1", "TRIPLEKILLS": "1", "TETRAKILLS": "0", "OMNIKILLS": "0"}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("stats:\n got %v\nwant %v", s, want)
	}
	if Wins(s) != 1 || Wins(nil) != 0 {
		t.Errorf("wins %d", Wins(s))
	}
}

func TestXP(t *testing.T) {
	if got := GameXP(true, 720); got != 124 {
		t.Errorf("GameXP(won, 12 min) = %d", got)
	}
	if got := GameXP(false, 4*3600); got != 160 {
		t.Errorf("GameXP(lost, 4 h) = %d", got)
	}
	for xp, want := range map[uint32]uint32{0: 1, 99: 1, 100: 2, 299: 2, 300: 3, 600: 4, 1 << 31: MaxLevel} {
		if got := LevelFor(xp); got != want {
			t.Errorf("LevelFor(%d) = %d, want %d", xp, got, want)
		}
	}
}
