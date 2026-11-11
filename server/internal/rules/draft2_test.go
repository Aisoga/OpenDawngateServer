package rules

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// windows is the board's turns grouped by window: "b0 | b1 | ... | p0.0 | p1.0 p1.1 | ...".
func windows(d *Draft) string {
	var parts []string
	for i, t := range d.Turns {
		s := fmt.Sprintf("b%d", t.Team)
		if t.Kind == DraftPick {
			s = fmt.Sprintf("p%d.%d", t.Team, t.Seat)
		}
		if i > 0 && d.Turns[i-1].Window == t.Window {
			parts[len(parts)-1] += " " + s
		} else {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " | ")
}

// The picks run in windows 1-2-2-2-2-1 over the order 1-2-2-1-1-2-2-1-1-2; a double window with one planned turn is a
// single one, and the windows are numbered without gaps.
func TestDraftWindows(t *testing.T) {
	for _, c := range []struct {
		seats [2]int
		want  string
	}{
		{[2]int{5, 5}, "b0 | b1 | b0 | b1 | p0.0 | p1.0 p1.1 | p0.1 p0.2 | p1.2 p1.3 | p0.3 p0.4 | p1.4"},
		{[2]int{1, 1}, "b0 | b1 | b0 | b1 | p0.0 | p1.0"},
		{[2]int{2, 1}, "b0 | b1 | b0 | b1 | p0.0 | p1.0 | p0.1"},
		{[2]int{1, 3}, "b0 | b1 | b0 | b1 | p0.0 | p1.0 p1.1 | p1.2"},
		{[2]int{2, 0}, "b0 | b0 | p0.0 | p0.1"},
		{[2]int{3, 2}, "b0 | b1 | b0 | b1 | p0.0 | p1.0 p1.1 | p0.1 p0.2"},
	} {
		d, err := NewDraft(draftPool(16), c.seats)
		if err != nil {
			t.Fatal(err)
		}
		if got := windows(d); got != c.want {
			t.Errorf("%v: windows %q, want %q", c.seats, got, c.want)
		}
		for i, turn := range d.Turns {
			if i > 0 && turn.Window != d.Turns[i-1].Window && turn.Window != d.Turns[i-1].Window+1 {
				t.Errorf("%v: a gap before turn %d", c.seats, i)
			}
		}
	}
}

// pastBans plays a full board's four bans.
func pastBans(t *testing.T, d *Draft) {
	t.Helper()
	for i, s := range []string{"PC_00", "PC_01", "PC_02", "PC_03"} {
		if err := d.Act(i%2, 0, DraftBan, s); err != nil {
			t.Fatal(err)
		}
	}
}

// A double window: each seat locks on its own, in any order, a locked seat can't act again, the window ends when both
// have locked; its timeout takes a random pick only for the seat still open.
func TestDraftDoubleWindow(t *testing.T) {
	d, _ := NewDraft(draftPool(16), [2]int{5, 5})
	pastBans(t, d)
	if err := d.Act(0, 0, DraftPick, "PC_10"); err != nil {
		t.Fatal(err)
	}
	if got := d.Window(); len(got) != 2 || !d.OnTurn(1, 0) || !d.OnTurn(1, 1) || d.OnTurn(0, 1) {
		t.Fatalf("the double window %v", got)
	}
	if err := d.Act(1, 1, DraftPick, "PC_11"); err != nil { // the second seat first
		t.Fatal(err)
	}
	if d.Last != 6 || d.CurWindow() != 5 || d.OnTurn(1, 1) || !d.OnTurn(1, 0) {
		t.Fatalf("after one lock: last %d, window %d", d.Last, d.CurWindow())
	}
	if err := d.Act(1, 1, DraftPick, "PC_12"); !errors.Is(err, ErrLockedIn) {
		t.Fatalf("locked twice: %v", err)
	}
	if err := d.Act(1, 0, DraftPick, "PC_11"); !errors.Is(err, ErrShaperPicked) {
		t.Fatalf("the teammate's shaper: %v", err)
	}
	if err := d.Act(0, 1, DraftPick, "PC_12"); !errors.Is(err, ErrNotYourTurn) {
		t.Fatalf("the next window early: %v", err)
	}
	if err := d.Act(1, 0, DraftPick, "PC_12"); err != nil {
		t.Fatal(err)
	}
	if d.CurWindow() != 6 || len(d.Open()) != 2 {
		t.Fatalf("the next window %d, open %v", d.CurWindow(), d.Open())
	}
	// team 1's double window: one seat locks, the other times out
	if err := d.Act(0, 2, DraftPick, "PC_13"); err != nil {
		t.Fatal(err)
	}
	ended := d.Timeout(rand.New(rand.NewPCG(1, 2)), nil)
	if len(ended) != 1 || d.Turns[ended[0]].Seat != 1 || d.Turns[ended[0]].How != DraftTimeout || d.Turns[ended[0]].Shaper == "" {
		t.Fatalf("the window's timeout %v %+v", ended, d.Turns[7:9])
	}
	if d.Turns[8].How != DraftChosen || d.CurWindow() != 7 {
		t.Fatalf("after the timeout %+v, window %d", d.Turns[8], d.CurWindow())
	}
}

// A pick-order swap: teammates without a pick whose windows aren't on exchange their pick turns (the bans stay the first
// seat's); a seat that picked, is on turn, has no pick turn, or is itself is refused.
func TestDraftSwapPicks(t *testing.T) {
	d, _ := NewDraft(draftPool(16), [2]int{3, 2})
	if err := d.CheckSwap(0, 0, 0); !errors.Is(err, ErrSameSeat) {
		t.Fatalf("with itself: %v", err)
	}
	if err := d.CheckSwap(1, 0, 2); !errors.Is(err, ErrNoPickTurn) {
		t.Fatalf("a seat without a pick: %v", err)
	}
	if err := d.SwapPicks(0, 0, 2); err != nil { // during the bans
		t.Fatal(err)
	}
	if got := windows(d); got != "b0 | b1 | b0 | b1 | p0.2 | p1.0 p1.1 | p0.1 p0.0" {
		t.Fatalf("after the swap %q", got)
	}
	pastBans(t, d)
	if err := d.CheckSwap(0, 2, 1); !errors.Is(err, ErrWindowOn) {
		t.Fatalf("a seat on turn: %v", err)
	}
	if err := d.Act(0, 0, DraftPick, "PC_10"); !errors.Is(err, ErrNotYourTurn) {
		t.Fatalf("seat 0 picks first: %v", err)
	}
	if err := d.Act(0, 2, DraftPick, "PC_10"); err != nil {
		t.Fatal(err)
	}
	if err := d.CheckSwap(0, 2, 1); !errors.Is(err, ErrAlreadyPicked) {
		t.Fatalf("a seat that picked: %v", err)
	}
	if err := d.CheckSwap(1, 0, 1); !errors.Is(err, ErrWindowOn) {
		t.Fatalf("both on turn: %v", err)
	}
	if err := d.SwapPicks(0, 1, 0); err != nil { // both still to come, in the same window
		t.Fatal(err)
	}
	if d.Picks()[0][2] != "PC_10" {
		t.Fatalf("picks %v", d.Picks())
	}
}

// The trade: only after the picks, only seats that hold a shaper; the shapers (and how they were picked) change places,
// and Picks gives them after the trade.
func TestDraftTrade(t *testing.T) {
	d, _ := NewDraft(draftPool(16), [2]int{2, 1})
	pastBans(t, d)
	if err := d.Act(0, 0, DraftPick, "PC_10"); err != nil {
		t.Fatal(err)
	}
	if err := d.CheckTrade(0, 0, 1); !errors.Is(err, ErrDraftNotOver) {
		t.Fatalf("during the picks: %v", err)
	}
	d.Timeout(rand.New(rand.NewPCG(3, 4)), nil) // team 2's pick
	if err := d.Act(0, 1, DraftPick, "PC_12"); err != nil {
		t.Fatal(err)
	}
	if err := d.CheckTrade(1, 0, 1); !errors.Is(err, ErrNoPickTurn) {
		t.Fatalf("a seat without a pick: %v", err)
	}
	if err := d.Trade(0, 1, 1); !errors.Is(err, ErrSameSeat) {
		t.Fatalf("with itself: %v", err)
	}
	if err := d.Trade(0, 1, 0); err != nil {
		t.Fatal(err)
	}
	if p := d.Picks(); p[0][0] != "PC_12" || p[0][1] != "PC_10" {
		t.Fatalf("picks after the trade %v", p)
	}
	if d.Free("PC_10") != ErrShaperPicked || d.Free("PC_12") != ErrShaperPicked {
		t.Fatal("a traded shaper is free again")
	}
}

// The roles: four lower-case ids in the host's order.
func TestDraftRoles(t *testing.T) {
	var ids []string
	for _, r := range DraftRoles {
		ids = append(ids, r.ID+"="+r.Name)
	}
	if got := strings.Join(ids, " "); got != "gladiator=Gladiator tactician=Tactician hunter=Hunter predator=Predator" {
		t.Fatalf("roles %s", got)
	}
	if !IsDraftRole("hunter") || IsDraftRole("Hunter") || IsDraftRole("") {
		t.Fatal("IsDraftRole")
	}
}
