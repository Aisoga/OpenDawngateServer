package rules

import (
	"math/rand/v2"
	"testing"
)

// hovers is a Timeout hover func over a fixed table: {team, seat} → shaper.
func hovers(m map[[2]int]string) func(team, seat int) string {
	return func(team, seat int) string { return m[[2]int{team, seat}] }
}

// DRAFT1b: a ban turn that times out with the seat's hover on a free shaper bans it (DraftHover); a hover on a shaper
// already banned, outside the pool, or no hover, skips the ban as before; only the seat on turn's hover counts.
func TestDraftTimeoutHoverBan(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	d, _ := NewDraft(draftPool(16), [2]int{2, 2})
	h := hovers(map[[2]int]string{{0, 0}: "PC_04", {0, 1}: "PC_05", {1, 0}: "PC_06"})
	if ended := d.Timeout(r, h); len(ended) != 1 || d.Turns[0].Shaper != "PC_04" || d.Turns[0].How != DraftHover {
		t.Fatalf("team 1's ban by hover: %v %+v", ended, d.Turns[0])
	}
	if d.Free("PC_04") != ErrShaperBanned || d.CurWindow() != 1 {
		t.Fatalf("PC_04 %v, window %d", d.Free("PC_04"), d.CurWindow())
	}
	// team 2's first player hovers PC_04, banned meanwhile: skipped
	if d.Timeout(r, hovers(map[[2]int]string{{1, 0}: "PC_04"})); d.Turns[1].Shaper != "" || d.Turns[1].How != DraftTimeout {
		t.Fatalf("a hover on a banned shaper: %+v", d.Turns[1])
	}
	// a hover outside the pool, then none: skipped
	d.Timeout(r, hovers(map[[2]int]string{{0, 0}: "PC_Nope"}))
	d.Timeout(r, nil)
	for _, i := range []int{2, 3} {
		if d.Turns[i].Shaper != "" || d.Turns[i].How != DraftTimeout {
			t.Fatalf("turn %d: %+v", i, d.Turns[i])
		}
	}
	// the first pick (team 1, seat 0): the enemy's and the teammate's hovers don't steer it
	d.Timeout(r, hovers(map[[2]int]string{{1, 0}: "PC_07", {0, 1}: "PC_08"}))
	if p := d.Turns[4]; p.Kind != DraftPick || p.Team != 0 || p.Seat != 0 || p.How != DraftTimeout || p.Shaper == "" {
		t.Fatalf("someone else's hover: %+v", p)
	}
}

// DRAFT1b: a pick that times out with a free hover picks it; in a double window a hover taken by the teammate's lock
// falls back to a random pick, and two seats hovering the same shaper: the first in order gets it, the other a random.
func TestDraftTimeoutHoverPick(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	d, _ := NewDraft(draftPool(16), [2]int{5, 5})
	pastBans(t, d)
	if d.Timeout(r, hovers(map[[2]int]string{{0, 0}: "PC_10"})); d.Turns[4].Shaper != "PC_10" || d.Turns[4].How != DraftHover {
		t.Fatalf("the pick by hover: %+v", d.Turns[4])
	}
	// team 2's double window: seat 0 locks PC_11, seat 1 hovered it too
	if err := d.Act(1, 0, DraftPick, "PC_11"); err != nil {
		t.Fatal(err)
	}
	ended := d.Timeout(r, hovers(map[[2]int]string{{1, 0}: "PC_12", {1, 1}: "PC_11"}))
	if len(ended) != 1 || ended[0] != 6 || d.Turns[6].How != DraftTimeout || d.Turns[6].Shaper == "" || d.Turns[6].Shaper == "PC_11" {
		t.Fatalf("a hover taken by the teammate: %v %+v", ended, d.Turns[6])
	}
	if d.Turns[5].Shaper != "PC_11" || d.Turns[5].How != DraftChosen {
		t.Fatalf("the lock stays: %+v", d.Turns[5])
	}
	// team 1's double window: both seats hover PC_13, neither locked
	ended = d.Timeout(r, hovers(map[[2]int]string{{0, 1}: "PC_13", {0, 2}: "PC_13"}))
	if len(ended) != 2 || d.Turns[7].Shaper != "PC_13" || d.Turns[7].How != DraftHover ||
		d.Turns[8].How != DraftTimeout || d.Turns[8].Shaper == "" || d.Turns[8].Shaper == "PC_13" {
		t.Fatalf("one shaper hovered twice: %+v", d.Turns[7:9])
	}
	if d.CurWindow() != 7 || d.Last != 8 {
		t.Fatalf("after the window: window %d, last %d", d.CurWindow(), d.Last)
	}
	// a seat that hovers in a double window where the other seat has none: each its own result
	ended = d.Timeout(r, hovers(map[[2]int]string{{1, 3}: "PC_14"}))
	if len(ended) != 2 || d.Turns[9].How != DraftTimeout || d.Turns[10].Shaper != "PC_14" || d.Turns[10].How != DraftHover {
		t.Fatalf("one hover of two: %+v", d.Turns[9:11])
	}
}
