// Package progress computes what a finished game gives a player (roadmap B7c): the post-game rewards
// (loot bag, Destiny, items, crafting currency for duplicates) with the server Lua's loot-bag formulas,
// the PlayerStats update, and account XP and level. It is engine-free and has no state: the Blaze side
// (server/internal/blaze, gamehost.go) feeds it a finished game and stores what it returns.
//
// Sources (docs/MATCH_FLOW.md *Match end and post-game (D8-static)*, docs/ACCOUNT_CONTENT.md *B7c*):
//   - nGameModeHandlers (ServerData Lua/GameModeHandlers): GetLootBagBaseRoll, GetLootBagGameOutcomePoints
//     and GetLootBagECAward. Nothing in the client calls them (R10); the server does, here. The Lua runs
//     with a 4-byte float lua_Number, so the formulas are evaluated in float32 like the original.
//   - AssetData LootBag (tiers, RequiredBagScore, drop tables), LootBagRarity, LootBagTuning (FTUE bags and
//     loot, first-win points), all through server/internal/content.
//   - The rest is ours and marked [assumed]: which bag a game gets, the drop pools, the crafting currency of
//     a duplicate, the stats' meanings and the account XP curve (no client reader of ALEV/ALXP was found).
//
// re/scripts/postgame.py practice_rewards is the reference for practice games; TestPracticeRewardsMatchPostgame
// pins its golden example.
package progress

import (
	"math"
	"strconv"
	"strings"

	"dawngate/server/internal/content"
)

// Game descriptors: Lua GlobalDefinitions nGameDescriptors, the Blaze game attribute GameFlags (B8's
// practice game has 0x50C = Solo | SoloPractice | FTUE | BotsFillTeams; B10a's matchmade game 1).
const (
	DescMatchmade     = 1 << 0
	DescCustom        = 1 << 1
	DescSolo          = 1 << 2
	DescSoloPractice  = 1 << 3
	DescGroupPractice = 1 << 4
	DescRanked        = 1 << 5
	DescFTUE          = 1 << 8
)

// MaxLootItems is how many items the post-game's C1 04 carries (its readers loop over 4 entries).
const MaxLootItems = 4

// Rand is the random source (math/rand's *Rand has it); tests use a fixed one.
type Rand interface{ Float64() float64 }

// Game is one player's finished game.
type Game struct {
	Descriptors uint32  // GameFlags
	Won         bool    // the player's team won
	Seconds     float64 // match duration
	FirstWin    bool    // a win that is the account's first of the UTC day (the caller decides)
	// Karma (D8c) is the karma votes the player got in the post-game's karma vote (the host's playerKarma); nil =
	// none counted. It counts only in a game with a karma vote (KarmaVote).
	Karma *Karma
}

// KarmaVote reports a game whose post-game has the karma vote: the client plays the matchmade end cinematic
// (GameOverState_OnGameOver 0x00543560: no practice bit, the matchmade bit) and the host's PostGame runs the
// vote (gms/core/postgame ModeOf). Its rewards wait for the host's playerKarma (D8c).
func (g Game) KarmaVote() bool { return !g.Practice() && g.Descriptors&DescMatchmade != 0 }

// KarmaPointsPerVote is the bag points one karma vote is worth (C1 04 +0x18 = votes x this) [assumed: no data or
// script has a karma rule; LootBagTuning has only CheatPoints_Karma 0]. The host's fallback uses the same value
// (gms/core/postgame Options::karmaPointsPerVote).
const KarmaPointsPerVote = 10

// Karma is the karma votes a player got, by the voter's relation (C1 04 +0x1C party, +0x20 team, +0x24 enemy).
type Karma struct {
	Party, Team, Enemy int
}

// Votes is the number of votes in all.
func (k Karma) Votes() int { return k.Party + k.Team + k.Enemy }

// Points is the karma's bag points (C1 04 +0x18).
func (k Karma) Points() int { return k.Votes() * KarmaPointsPerVote }

// Practice reports a practice game (solo or group practice), whose Destiny follows the practice
// formula and whose bag is fixed (below).
func (g Game) Practice() bool { return g.Descriptors&(DescSoloPractice|DescGroupPractice) != 0 }

// FTUE reports a first-time-user game: C1 04's FTUE byte, FLAG bit 0.
func (g Game) FTUE() bool { return g.Descriptors&DescFTUE != 0 }

// Item is one reward item as C1 04 carries it: {catalogID, isDuplicate, duplicationAlternate}.
type Item struct {
	CatalogID uint32
	Duplicate bool   // the player already owns as many as one can: Alternate crafting currency instead
	Alternate uint32 // crafting currency for a duplicate
}

// Rewards is what one player gets for a game: the fields of the post-game's C1 04 (MATCH_FLOW.md) plus
// the bag's name.
type Rewards struct {
	Bag            string // loot bag name ("Platinum"); "" = no bag known (no content)
	LootBag        uint32 // its AssetCatalog id (C1 04 +0x00)
	Destiny        uint32 // earned currency (EC) awarded (+0x04)
	BaseRoll       int    // bag points (+0x08)
	WinPoints      int    // +0x0C
	WinAltPoints   int    // +0x10 (the match-time half of GetLootBagGameOutcomePoints)
	FirstWinPoints int    // +0x14
	KarmaPoints    int    // +0x18 (D8c: Karma.Points())
	Karma          *Karma // +0x1C..+0x24, the votes counted (D8c); nil = no karma counted
	Items          []Item // at most MaxLootItems (+0x28, count +0x68)
	CC             uint32 // crafting currency: the sum of the duplicates' Alternate
	FTUE           bool   // +0x74
}

// Score is the bag score: base roll + game outcome points (both halves) + first win + karma (D8c: the karma
// votes of a matchmade game's post-game; 0 in any other game) [assumed: the reward calculation view shows exactly
// these rows, bagPointsKarma among them].
func (r Rewards) Score() int {
	return r.BaseRoll + r.WinPoints + r.WinAltPoints + r.FirstWinPoints + r.KarmaPoints
}

// BaseRoll is GetLootBagBaseRoll: Round((100 * math.random() + 125)^3.5 / 325446.44), 67...525.
func BaseRoll(rand01 float64) int {
	x := float32(100)*float32(rand01) + 125
	v := float32(math.Pow(float64(x), 3.5)) / float32(325446.44)
	return int(math.Floor(float64(v) + 0.5)) // nUtil.Round [assumed: half up]
}

// OutcomePoints is GetLootBagGameOutcomePoints(won, matchSeconds): 200 for a win, and 0.91 per match
// minute up to 55 (truncated to an integer for C1 04's i32).
func OutcomePoints(won bool, seconds float64) (win, alt int) {
	if won {
		win = 200
	}
	m := min(float32(seconds)/60, 55)
	return win, int(m * float32(0.91))
}

// ECAward is GetLootBagECAward(lootBag, ?, matchSeconds, practiceAllowed, descriptors): the Destiny of a
// game, piecewise linear in the match minutes per bag tier (Bronze...Diamond by name, capped at 55
// minutes), or the practice formula (capped at 50) for a practice game. An unknown bag gives 0.
func ECAward(bag string, seconds float64, practiceAllowed bool, descriptors uint32) float32 {
	m := float32(seconds) / 60
	lim := float32(55)
	if descriptors&(DescSoloPractice|DescGroupPractice) != 0 {
		if !practiceAllowed {
			return 0
		}
		lim = 50
		if m <= 30 {
			return 5 + float32(0.63)*min(m, lim)
		}
		return 17 + float32(0.22)*min(m, lim)
	}
	type tier struct{ a, b, c, mid, late float32 }
	tiers := map[string]tier{
		"bronze":   {22.4, 15.2, 28.8, 0.6, 0.22},
		"silver":   {32, 25, 40, 0.6, 0.19},
		"gold":     {40, 33.6, 47.2, 0.61, 0.225},
		"platinum": {49.6, 44, 60, 0.62, 0.21},
		"diamond":  {64, 55.2, 76, 0.75, 0.21},
	}
	t, ok := tiers[strings.ToLower(bag)]
	if !ok {
		return 0
	}
	switch {
	case m <= 18:
		return t.a + float32(0.225)*min(m, lim)
	case m <= 37:
		return t.b + t.mid*min(m, lim)
	default:
		return t.c + t.late*min(m, lim)
	}
}

// DuplicateCC is the crafting currency a duplicate item gives, by BGEcommerce item type [assumed: the
// originals came from EA's backend; one tenth of our EC prices for perk shapes and gems].
var DuplicateCC = map[int]uint32{content.ItemPerkShape: 50, content.ItemPerkGem: 15}

// DefaultDuplicateCC is the crafting currency of a duplicate of any other item type [assumed].
const DefaultDuplicateCC = 25

// Compute decides one player's rewards. owned gives the account's current count of an item.
//
//   - Practice game (MATCH_FLOW.md's D8 proposal): a won FTUE game gets LootBagTuning's FTUE bag
//     (Platinum) and the FTUE loot (a perk shape and two gems); any other practice game the default bag
//     (Bronze) and no items. Destiny from the practice formula, rounded down.
//   - Other games [assumed]: the bag is the highest tier whose RequiredBagScore the bag score reaches
//     (the lowest tier otherwise), its items come from its drop table (Drops), Destiny from the tier's
//     formula, rounded down. In a game with a karma vote (KarmaVote) the score includes the karma points of
//     g.Karma (D8c).
//   - Every item the player already owns as many of as one can (counting the items before it in this
//     reward) is a duplicate worth DuplicateCC crafting currency instead.
func Compute(c *content.Content, owned func(id uint32) uint32, g Game, rnd Rand) Rewards {
	if c == nil {
		c = &content.Content{}
	}
	r := Rewards{FTUE: g.FTUE(), BaseRoll: BaseRoll(rnd.Float64())}
	r.WinPoints, r.WinAltPoints = OutcomePoints(g.Won, g.Seconds)
	if g.FirstWin && g.Won {
		r.FirstWinPoints = c.LootTuning.FirstWinPoints
		if r.FirstWinPoints == 0 {
			r.FirstWinPoints = 100 // LootBagTuning.Points_FirstWinOfTheDay of build 854
		}
	}
	r.AddKarma(g)
	var bag content.LootBag
	var ok bool
	var items []uint32
	if g.Practice() {
		if g.FTUE() && g.Won {
			bag, ok = c.Bag(c.LootTuning.FTUEBag)
			items = append(items, c.FTUELoot...)
		} else {
			bag, ok = c.Bag(c.LootTuning.DefaultBag)
		}
	} else if c != nil && len(c.LootBags) > 0 {
		bag, ok = c.LootBags[0], true
		for _, b := range c.LootBags { // ordered by RequiredBagScore
			if r.Score() >= b.RequiredBagScore {
				bag = b
			}
		}
		items = Drops(c, bag, rnd)
	}
	if ok {
		r.Bag, r.LootBag = bag.Name, bag.ID
	}
	if d := ECAward(r.Bag, g.Seconds, true, g.Descriptors); d > 0 {
		r.Destiny = uint32(d)
	}
	r.Items, r.CC = Resolve(c, owned, items)
	return r
}

// AddKarma puts g's karma votes and points into r (D8c), in a game with a karma vote only: a practice game's
// rewards stay as they were.
func (r *Rewards) AddKarma(g Game) {
	if g.Karma == nil || !g.KarmaVote() {
		return
	}
	k := *g.Karma
	r.Karma, r.KarmaPoints = &k, k.Points()
}

// Resolve turns dropped catalogIDs into reward items (at most MaxLootItems): an item the player already
// owns as many of as one can (counting the items before it) is a duplicate worth DuplicateCC crafting
// currency; it also returns the crafting currency in all. A withheld item (content.IsWithheld, e.g. in a host's
// proposal) is dropped.
func Resolve(c *content.Content, owned func(id uint32) uint32, ids []uint32) ([]Item, uint32) {
	var out []Item
	var cc uint32
	got := map[uint32]uint32{}
	for _, id := range ids {
		if len(out) == MaxLootItems {
			break
		}
		if c.IsWithheld(id) {
			continue
		}
		it := Item{CatalogID: id}
		ci, known := c.Item(id)
		limit := uint32(1)
		if known {
			limit = max(ci.MaxOwnable, 1)
		}
		if owned(id)+got[id] >= limit {
			it.Duplicate = true
			it.Alternate = DefaultDuplicateCC
			if v, ok := DuplicateCC[ci.ItemType]; ok {
				it.Alternate = v
			}
			cc += it.Alternate
		} else {
			got[id]++
		}
		out = append(out, it)
	}
	return out, cc
}

// Drops rolls a bag's drop table: an IndependentEvents node runs each child with its own chance, a
// WeightedRandom node one child picked by chance as weight; a node with a rarity drops one item of
// that rarity (Pick). The result is in drop order.
func Drops(c *content.Content, bag content.LootBag, rnd Rand) []uint32 {
	var out []uint32
	var walk func(d content.Drop)
	walk = func(d content.Drop) {
		if d.Rarity != "" {
			if id, ok := Pick(c, d.Rarity, rnd); ok {
				out = append(out, id)
			}
		}
		switch d.Type {
		case "WeightedRandom":
			total := 0.0
			for _, ch := range d.Drops {
				total += ch.Chance
			}
			x := rnd.Float64() * total
			for _, ch := range d.Drops {
				if x < ch.Chance {
					walk(ch)
					return
				}
				x -= ch.Chance
			}
		default: // IndependentEvents
			for _, ch := range d.Drops {
				if ch.Chance >= 1 || rnd.Float64() < ch.Chance {
					walk(ch)
				}
			}
		}
	}
	if bag.Drop != nil {
		walk(*bag.Drop)
	}
	return out
}

// Pick draws one item of a rarity: a pool by its weight, then one of the pool's sold items at random.
// The pools are "GEMS" (every gem the store sells) and "<n>_MAX_SHAPES" (every sold perk shape one can
// own n of) [inferred: those n are exactly build 854's Shape.maxOwnable values]. An empty pool drops
// nothing.
func Pick(c *content.Content, rarity string, rnd Rand) (uint32, bool) {
	ra, ok := c.Rarity(rarity)
	if !ok || len(ra.Pools) == 0 {
		return 0, false
	}
	total := 0.0
	for _, p := range ra.Pools {
		total += p.Chance
	}
	x := rnd.Float64() * total
	pool := ra.Pools[len(ra.Pools)-1].Name
	for _, p := range ra.Pools {
		if x < p.Chance {
			pool = p.Name
			break
		}
		x -= p.Chance
	}
	members := PoolItems(c, pool)
	if len(members) == 0 {
		return 0, false
	}
	return members[int(rnd.Float64()*float64(len(members)))%len(members)], true
}

// PoolItems lists a drop pool's items (catalogIDs, in the content's perk order): sold items only
// (CollectionItem status none, so the store has an offer for them and the perk screen shows them).
func PoolItems(c *content.Content, pool string) []uint32 {
	kind, n := content.KindGem, uint32(0)
	if pool != "GEMS" {
		num, ok := strings.CutSuffix(pool, "_MAX_SHAPES")
		v, err := strconv.ParseUint(num, 10, 32)
		if !ok || err != nil {
			return nil
		}
		kind, n = content.KindShape, uint32(v)
	}
	var out []uint32
	for _, p := range c.Perks {
		it, ok := c.Item(p.CatalogID)
		if p.Kind != kind || !ok || it.Status != content.StatusNone || (n != 0 && p.MaxOwnable != n) {
			continue
		}
		out = append(out, p.CatalogID)
	}
	return out
}
