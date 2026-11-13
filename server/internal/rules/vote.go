package rules

import (
	"math/rand/v2"
	"slices"
)

// Army Pick and Clone Pick (ARMY1, the user's decisions of 2026-11-12): instead of a draft, every human votes for one
// shaper of the pool in the lobby; then Army Pick draws one of each team's votes for the whole team (both teams may
// get the same shaper), Clone Pick one of all the votes for everyone. The lobby's side is in package blaze (draft.go,
// vote.go); the match host lets a team hold one shaper several times for both picks.
const (
	PickArmy  = "army"
	PickClone = "clone"
)

// IsVotePick reports whether the pick rule pick is a vote (Army Pick or Clone Pick).
func IsVotePick(pick string) bool { return pick == PickArmy || pick == PickClone }

// PoolForVote is the shapers a vote needs in the pool: one is enough, since a team (Army) or everyone (Clone) plays the
// drawn shaper and the host lets a team hold it several times.
const PoolForVote = 1

// DrawVote draws a vote's shaper with rng (the server's generator, from a seed of its own; never a client's): one of
// votes, each with the same chance, so a shaper's odds are its share of the votes (three votes, three times one vote's
// odds). A vote that is "" or not in pool doesn't count. Without a vote that counts it draws a shaper of pool, each
// with the same chance. fromVote reports which of the two it was; "" with an empty pool and no vote.
func DrawVote(votes, pool []string, rng *rand.Rand) (shaper string, fromVote bool) {
	var counted []string
	for _, v := range votes {
		if v != "" && slices.Contains(pool, v) {
			counted = append(counted, v)
		}
	}
	if len(counted) > 0 {
		return counted[rng.IntN(len(counted))], true
	}
	if len(pool) == 0 {
		return "", false
	}
	return pool[rng.IntN(len(pool))], false
}
