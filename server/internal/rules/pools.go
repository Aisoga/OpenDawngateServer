package rules

import (
	"math/rand/v2"
	"sort"
)

// The shaper pools (RULES3). A pool is a preset: the shapers whose game data matches it. Build 854's PlayerClass data
// has for each shaper its attack style (ShaperStyle_AttackStyle: melee, ranged, hybrid) and its archetype
// (CharacterArchetype: tank, bruiser, carry, mage, support, assassin). Its four role ratings (roleRatingGladiator, ...)
// are 1 for every shaper but the High Huntress, so no role pool can come from the data; the archetypes are the pools by
// what a shaper does instead. A hybrid attacker (Faris) is in both the melee and the ranged pool. The assassins (4 in
// build 854) are no pool: a pick needs 5 (PoolForPick).
//
// The table below is that data for the 35 shapers a player can be offered (the titled, not testing-only shapers:
// content.Listed, SHAPERPOOL). The server's content file has the archetype but not the attack style, so the table holds
// both rather than make every content file be regenerated; TestPoolsAgreeWithContent compares it with a content file.
// Bot marks the shapers the match host's bots can play (gms/core/bots: the composition's role lists, the Hunter and
// Gladiator classes and the profiles, by base class); keep it with them.

// ShaperFacts is one shaper's data for the pools.
type ShaperFacts struct {
	Class     string // its PlayerClass ("PC_Alpha")
	Attack    string // ShaperStyle_AttackStyle: "melee", "ranged" or "hybrid"
	Archetype string // CharacterArchetype
	Bot       bool   // the bots can play it
}

var shaperFacts = []ShaperFacts{
	{"PC_Alpha", "ranged", "mage", false},
	{"PC_Artificer", "melee", "bruiser", true},
	{"PC_Artist", "ranged", "support", true},
	{"PC_BloodMage", "ranged", "support", false},
	{"PC_Creature", "ranged", "carry", true},
	{"PC_Dancer", "ranged", "mage", false},
	{"PC_DeepOne", "melee", "assassin", false},
	{"PC_Desecrator", "melee", "tank", true},
	{"PC_Doll", "ranged", "support", false},
	{"PC_Dreamer", "ranged", "support", false},
	{"PC_Druid", "melee", "bruiser", false},
	{"PC_Enforcer", "melee", "bruiser", false},
	{"PC_Golem", "hybrid", "assassin", true},
	{"PC_Guardian", "melee", "tank", true},
	{"PC_Gunman", "ranged", "carry", false},
	{"PC_HighHuntress", "ranged", "carry", true},
	{"PC_HuTank", "melee", "tank", false},
	{"PC_KingOfMasks", "ranged", "mage", false},
	{"PC_LoreMaster", "ranged", "mage", false},
	{"PC_MerchantPrincess", "ranged", "carry", true},
	{"PC_Otter", "melee", "bruiser", true},
	{"PC_Outlaw", "ranged", "carry", false},
	{"PC_Penitent", "melee", "bruiser", false},
	{"PC_Planeswalker", "ranged", "mage", false},
	{"PC_Raider", "melee", "tank", false},
	{"PC_Savage", "ranged", "carry", false},
	{"PC_Sculptor", "melee", "support", true},
	{"PC_ShadowAssassin", "melee", "assassin", false},
	{"PC_Shepherd", "melee", "tank", false},
	{"PC_SkyCaptain", "ranged", "carry", false},
	{"PC_SnowQueen", "ranged", "mage", false},
	{"PC_Survivor", "melee", "assassin", true},
	{"PC_TidalQueen", "ranged", "mage", true},
	{"PC_Warden", "melee", "bruiser", true},
	{"PC_WarlockKnight", "melee", "bruiser", false},
}

// Facts returns the pools' table (a copy).
func Facts() []ShaperFacts { return append([]ShaperFacts(nil), shaperFacts...) }

// InPool reports whether a shaper of these facts belongs to pool id ("all" and "" hold every shaper).
func InPool(pool string, f ShaperFacts) bool {
	switch pool {
	case "", "all":
		return true
	case "melee":
		return f.Attack == "melee" || f.Attack == "hybrid"
	case "ranged":
		return f.Attack == "ranged" || f.Attack == "hybrid"
	case "tank", "bruiser", "carry", "mage", "support":
		return f.Archetype == pool
	}
	return false
}

// Pools is the pools' catalog on a server: the table's shapers the server offers (Offered: the PlayerClasses its
// content lists, SHAPERPOOL; nil = every shaper of the table). It is Check's Catalog.
type Pools struct {
	Offered map[string]bool
}

// offered reports whether the server offers the shaper.
func (p Pools) offered(class string) bool { return p.Offered == nil || p.Offered[class] }

// PoolSize counts the pool's shapers the server offers, and those of them the bots can play.
func (p Pools) PoolSize(pool string) (int, int, bool) {
	if _, ok := option(ShaperPool, pool); !ok && pool != "" {
		return 0, 0, false
	}
	n, bots := 0, 0
	for _, f := range shaperFacts {
		if p.offered(f.Class) && InPool(pool, f) {
			n++
			if f.Bot {
				bots++
			}
		}
	}
	return n, bots, true
}

// Members returns the pool's PlayerClasses the server offers, sorted; nil for "all" (no pool: every shaper).
func (p Pools) Members(pool string) []string {
	if pool == "" || pool == "all" {
		return nil
	}
	var out []string
	for _, f := range shaperFacts {
		if p.offered(f.Class) && InPool(pool, f) {
			out = append(out, f.Class)
		}
	}
	sort.Strings(out)
	return out
}

// Shapers returns the pool's PlayerClasses the server offers, sorted; for "all" every offered shaper (the draft's pool,
// RULES5b).
func (p Pools) Shapers(pool string) []string {
	var out []string
	for _, f := range shaperFacts {
		if p.offered(f.Class) && InPool(pool, f) {
			out = append(out, f.Class)
		}
	}
	sort.Strings(out)
	return out
}

// Roll is All Random's draw: for each team (key) a shaper of `pool` for each of its seats, none twice within a team (the
// client greys out a teammate's shaper; a shaper is unique within a team only, so the teams draw on their own). The
// result has one entry per seat, in seat order: "" where the pool ran out. The teams draw in key order from one
// generator seeded with `seed` (the server's, never a client's), so a seed gives the same rolls.
func Roll(pool []string, seats map[int]int, seed uint64) map[int][]string {
	r := rand.New(rand.NewPCG(seed, seed^0x52554C4553330000)) // "RULES3"
	sorted := append([]string(nil), pool...)
	sort.Strings(sorted)
	teams := make([]int, 0, len(seats))
	for t := range seats {
		teams = append(teams, t)
	}
	sort.Ints(teams)
	out := map[int][]string{}
	for _, t := range teams {
		order := append([]string(nil), sorted...)
		r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		got := make([]string, seats[t])
		for i := range got {
			if i < len(order) {
				got[i] = order[i]
			}
		}
		out[t] = got
	}
	return out
}
