package rules

import (
	"errors"
	"testing"
)

// The draft's order (DRAFTSHUF): the starting team has the first ban and the first pick, the bans alternate from it and
// the picks run 1-2-2-1-1-2-2-1-1-2 with "1" the starting team; each team bans from its Banner seat; the zero order is
// the order before (team 1 starts, the first seats ban).
func TestDraftOrder(t *testing.T) {
	pool := make([]string, 20)
	for i := range pool {
		pool[i] = string(rune('A' + i))
	}
	teamsOf := func(d *Draft, kind string) []int {
		var out []int
		for _, tn := range d.Turns {
			if tn.Kind == kind {
				out = append(out, tn.Team)
			}
		}
		return out
	}
	same := func(a, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	for _, c := range []struct {
		first      int
		bans, pick []int
	}{
		{0, []int{0, 1, 0, 1}, []int{0, 1, 1, 0, 0, 1, 1, 0, 0, 1}},
		{1, []int{1, 0, 1, 0}, []int{1, 0, 0, 1, 1, 0, 0, 1, 1, 0}},
	} {
		d, err := NewDraftOrder(pool, [2]int{5, 5}, DraftOrder{First: c.first, Banner: [2]int{3, 1}})
		if err != nil {
			t.Fatal(err)
		}
		if !same(teamsOf(d, DraftBan), c.bans) || !same(teamsOf(d, DraftPick), c.pick) {
			t.Fatalf("first %d: bans %v picks %v", c.first, teamsOf(d, DraftBan), teamsOf(d, DraftPick))
		}
		var next [2]int
		for _, tn := range d.Turns {
			switch {
			case tn.Kind == DraftBan && tn.Seat != [2]int{3, 1}[tn.Team]:
				t.Fatalf("first %d: team %d bans from seat %d", c.first, tn.Team, tn.Seat)
			case tn.Kind == DraftPick && tn.Seat != next[tn.Team]:
				t.Fatalf("first %d: team %d's pick %d by seat %d", c.first, tn.Team, next[tn.Team], tn.Seat)
			case tn.Kind == DraftPick:
				next[tn.Team]++
			}
		}
		// the windows stay 1-2-2-2-2-1 after the four bans
		if d.Turns[4].Window != 4 || d.Turns[5].Window != 5 || d.Turns[6].Window != 5 || d.Turns[13].Window != 9 {
			t.Fatalf("first %d: windows %+v", c.first, d.Turns)
		}
		// the starting team's banner acts first, and only it
		if err := d.Act(1-c.first, [2]int{3, 1}[1-c.first], DraftBan, "A"); !errors.Is(err, ErrNotYourTurn) {
			t.Fatalf("first %d: the other team's ban: %v", c.first, err)
		}
		if err := d.Act(c.first, 0, DraftBan, "A"); !errors.Is(err, ErrNotYourTurn) {
			t.Fatalf("first %d: a seat that doesn't ban: %v", c.first, err)
		}
		if err := d.Act(c.first, [2]int{3, 1}[c.first], DraftBan, "A"); err != nil {
			t.Fatalf("first %d: the banner's ban: %v", c.first, err)
		}
	}
	// the zero order is NewDraft's, the order before
	a, _ := NewDraft(pool, [2]int{3, 2})
	b, _ := NewDraftOrder(pool, [2]int{3, 2}, DraftOrder{})
	if len(a.Turns) != len(b.Turns) {
		t.Fatal("NewDraft isn't the zero order")
	}
	for i := range a.Turns {
		if a.Turns[i] != b.Turns[i] {
			t.Fatalf("turn %d: %+v vs %+v", i, a.Turns[i], b.Turns[i])
		}
	}
	// Spirit starts in a 2 v 1: its one pick first, Mortal's two in the double window; a team without humans skips
	d, _ := NewDraftOrder(pool, [2]int{2, 1}, DraftOrder{First: 1})
	if !same(teamsOf(d, DraftBan), []int{1, 0, 1, 0}) || !same(teamsOf(d, DraftPick), []int{1, 0, 0}) ||
		d.Turns[5].Window != d.Turns[6].Window {
		t.Fatalf("2 v 1, Spirit first: %+v", d.Turns)
	}
	d, _ = NewDraftOrder(pool, [2]int{0, 2}, DraftOrder{First: 0})
	if !same(teamsOf(d, DraftBan), []int{1, 1}) || !same(teamsOf(d, DraftPick), []int{1, 1}) {
		t.Fatalf("0 v 2: %+v", d.Turns)
	}
	// bad orders
	for _, o := range []DraftOrder{{First: 2}, {First: -1}, {Banner: [2]int{2, 0}}, {Banner: [2]int{0, -1}}} {
		if _, err := NewDraftOrder(pool, [2]int{2, 2}, o); err == nil {
			t.Errorf("order %+v accepted", o)
		}
	}
}
