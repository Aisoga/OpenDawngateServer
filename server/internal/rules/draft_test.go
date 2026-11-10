package rules

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

func draftPool(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("PC_%02d", i))
	}
	return out
}

// order is the board's turns as text: "b0 b1 ... p0.0 p1.0 ..." (kind, team, seat for picks).
func order(d *Draft) string {
	var parts []string
	for _, t := range d.Turns {
		if t.Kind == DraftBan {
			parts = append(parts, fmt.Sprintf("b%d", t.Team))
		} else {
			parts = append(parts, fmt.Sprintf("p%d.%d", t.Team, t.Seat))
		}
	}
	return strings.Join(parts, " ")
}

// The full board: two bans a team alternating, then 1-2-2-1-1-2-2-1-1-2 to the seats in order.
func TestDraftTurnOrder(t *testing.T) {
	d, err := NewDraft(draftPool(14), [2]int{5, 5})
	if err != nil {
		t.Fatal(err)
	}
	want := "b0 b1 b0 b1 p0.0 p1.0 p1.1 p0.1 p0.2 p1.2 p1.3 p0.3 p0.4 p1.4"
	if got := order(d); got != want {
		t.Fatalf("turns %q, want %q", got, want)
	}
	if PoolForDraft != 14 {
		t.Fatalf("PoolForDraft %d", PoolForDraft)
	}
}

// A team with fewer humans skips its turns beyond its size; a team without humans bans nothing.
func TestDraftSmallTeams(t *testing.T) {
	for _, c := range []struct {
		seats [2]int
		want  string
	}{
		{[2]int{1, 1}, "b0 b1 b0 b1 p0.0 p1.0"},
		{[2]int{2, 1}, "b0 b1 b0 b1 p0.0 p1.0 p0.1"},
		{[2]int{1, 3}, "b0 b1 b0 b1 p0.0 p1.0 p1.1 p1.2"},
		{[2]int{2, 0}, "b0 b0 p0.0 p0.1"},
		{[2]int{0, 0}, ""},
	} {
		d, err := NewDraft(draftPool(16), c.seats)
		if err != nil {
			t.Fatal(err)
		}
		if got := order(d); got != c.want {
			t.Errorf("%v: turns %q, want %q", c.seats, got, c.want)
		}
		if c.want == "" && !d.Done() {
			t.Errorf("%v: an empty draft isn't done", c.seats)
		}
	}
	if _, err := NewDraft(nil, [2]int{6, 0}); err == nil {
		t.Error("six seats taken")
	}
}

// Only the player on turn, only its turn's kind, only a free shaper of the pool; a refusal changes nothing.
func TestDraftActChecks(t *testing.T) {
	d, _ := NewDraft(draftPool(16), [2]int{2, 2})
	if err := d.Act(1, 0, DraftBan, "PC_00"); !errors.Is(err, ErrNotYourTurn) {
		t.Fatalf("team 2 on team 1's ban: %v", err)
	}
	if err := d.Act(0, 1, DraftBan, "PC_00"); !errors.Is(err, ErrNotYourTurn) {
		t.Fatalf("team 1's second player bans: %v", err)
	}
	if err := d.Act(0, 0, DraftPick, "PC_00"); !errors.Is(err, ErrWrongKind) {
		t.Fatalf("a pick in a ban turn: %v", err)
	}
	if err := d.Act(0, 0, DraftBan, "PC_99"); !errors.Is(err, ErrNotInPool) {
		t.Fatalf("outside the pool: %v", err)
	}
	if d.Cur != 0 {
		t.Fatal("a refusal moved the board")
	}
	if err := d.Act(0, 0, DraftBan, "PC_00"); err != nil {
		t.Fatal(err)
	}
	if err := d.Act(1, 0, DraftBan, "PC_00"); !errors.Is(err, ErrShaperBanned) {
		t.Fatalf("banned twice: %v", err)
	}
	for i, s := range []string{"PC_01", "PC_02", "PC_03"} {
		team := (i + 1) % 2
		if err := d.Act(team, 0, DraftBan, s); err != nil {
			t.Fatalf("ban %d: %v", i+1, err)
		}
	}
	if d.Phase() != DraftPick {
		t.Fatalf("phase %q after four bans", d.Phase())
	}
	if err := d.Act(0, 0, DraftPick, "PC_03"); !errors.Is(err, ErrShaperBanned) {
		t.Fatalf("a banned pick: %v", err)
	}
	if err := d.Act(0, 0, DraftPick, "PC_10"); err != nil {
		t.Fatal(err)
	}
	if err := d.Act(1, 0, DraftPick, "PC_10"); !errors.Is(err, ErrShaperPicked) {
		t.Fatalf("the other team's pick: %v", err)
	}
	for _, a := range []struct {
		team, seat int
		s          string
	}{{1, 0, "PC_11"}, {1, 1, "PC_12"}, {0, 1, "PC_13"}} {
		if err := d.Act(a.team, a.seat, DraftPick, a.s); err != nil {
			t.Fatal(err)
		}
	}
	if !d.Done() || d.Phase() != "done" {
		t.Fatalf("not done: %d of %d", d.Cur, len(d.Turns))
	}
	if err := d.Act(0, 0, DraftPick, "PC_14"); !errors.Is(err, ErrDraftOver) {
		t.Fatalf("after the end: %v", err)
	}
	p := d.Picks()
	if p[0][0] != "PC_10" || p[1][0] != "PC_11" || p[1][1] != "PC_12" || p[0][1] != "PC_13" || p[0][2] != "" {
		t.Fatalf("picks %v", p)
	}
	if b := d.Bans(); len(b) != 2*DraftBansPerTeam || b[0] != "PC_00" {
		t.Fatalf("bans %v", b)
	}
}

// A timed-out ban is skipped; a timed-out pick takes a random free shaper of the pool, never a banned or picked one.
func TestDraftTimeouts(t *testing.T) {
	for seed := uint64(0); seed < 200; seed++ {
		r := rand.New(rand.NewPCG(seed, 1))
		d, _ := NewDraft(draftPool(14), [2]int{5, 5})
		if err := d.Act(0, 0, DraftBan, "PC_00"); err != nil {
			t.Fatal(err)
		}
		turn, ok := d.Timeout(r)
		if !ok || turn.Kind != DraftBan || turn.Shaper != "" || turn.How != DraftTimeout {
			t.Fatalf("a ban's timeout: %+v", turn)
		}
		for !d.Done() {
			if _, ok := d.Timeout(r); !ok {
				t.Fatal("no turn")
			}
		}
		if _, ok := d.Timeout(r); ok {
			t.Fatal("a timeout after the end")
		}
		seen := map[string]bool{"PC_00": true}
		for _, team := range d.Picks() {
			for _, s := range team {
				if s == "" || seen[s] {
					t.Fatalf("seed %d: picks %v", seed, d.Picks())
				}
				seen[s] = true
			}
		}
	}
}

// Every pick times out in a pool of exactly the picks: each still gets a shaper; one fewer leaves the last without.
func TestDraftTimeoutPoolRunsOut(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 7))
	d, _ := NewDraft(draftPool(9), [2]int{5, 5})
	for !d.Done() {
		d.Timeout(r)
	}
	n := 0
	for _, team := range d.Picks() {
		for _, s := range team {
			if s != "" {
				n++
			}
		}
	}
	if n != 9 {
		t.Fatalf("%d picks from a pool of 9", n)
	}
}

// The checks: a draft needs 16 shapers in the pool, no bots and shaper select.
func TestCheckDraft(t *testing.T) {
	c := Standard()
	c.Pick = "draft"
	if fs := Check(c, Context{HostRules: true}); len(fs) != 0 {
		t.Fatalf("draft in the pool all: %v", fs)
	}
	if fs := Check(c, Context{HostRules: true, Bots: true, BotSeats: 4, TeamSize: 5}); len(fs) == 0 ||
		!strings.Contains(DescribeFindings(fs), "bots") {
		t.Fatalf("draft with bots: %v", fs)
	}
	if fs := Check(c, Context{HostRules: true, NoShaperSelect: true}); len(fs) != 1 || fs[0].Category != Pick {
		t.Fatalf("draft without shaper select: %v", fs)
	}
	c.ShaperPool = "melee" // 17
	if fs := Check(c, Context{HostRules: true}); len(fs) != 0 {
		t.Fatalf("draft in melee: %v", fs)
	}
	c.ShaperPool = "mage" // 7
	if fs := Check(c, Context{HostRules: true}); len(fs) != 1 || !strings.Contains(fs[0].Message, "14") {
		t.Fatalf("draft in mage: %v", fs)
	}
}
