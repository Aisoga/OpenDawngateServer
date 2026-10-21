package matchmaker

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                   { return &clock{t: time.Unix(1791405059, 0)} }
func newMM(c *clock, team, min int, fill time.Duration) *Matchmaker {
	return New(Config{TeamSize: team, MinPlayers: min, FillAfter: fill, Now: c.now})
}

var nextID int64

// ticket makes a ticket with n players (ids from 100*id) in mode and region.
func ticket(mode, region string, n int) *Ticket {
	nextID++
	t := &Ticket{ID: nextID, Mode: mode, Region: region}
	for i := 0; i < n; i++ {
		t.Players = append(t.Players, Player{ID: nextID*100 + int64(i), Name: fmt.Sprintf("p%d_%d", nextID, i)})
	}
	return t
}

func mustAdd(t *testing.T, m *Matchmaker, ts ...*Ticket) {
	t.Helper()
	for _, tk := range ts {
		if err := m.Add(tk); err != nil {
			t.Fatal(err)
		}
	}
}

// teams renders a match as "[a b|c d]" by ticket id.
func teams(mt Match) string {
	var s [2][]string
	for i := range mt.Teams {
		for _, t := range mt.Teams[i] {
			s[i] = append(s[i], fmt.Sprint(t.ID))
		}
	}
	return "[" + strings.Join(s[0], " ") + "|" + strings.Join(s[1], " ") + "]"
}

func TestFullMatchStartsAtOnce(t *testing.T) {
	c := newClock()
	m := newMM(c, 5, 10, time.Hour) // no early start
	nextID = 0
	for i := 0; i < 9; i++ {
		mustAdd(t, m, ticket("pvp", "na", 1))
	}
	if ms, _ := m.Tick(); len(ms) != 0 {
		t.Fatalf("9 players: %d matches", len(ms))
	}
	mustAdd(t, m, ticket("pvp", "na", 1))
	ms, ex := m.Tick()
	if len(ms) != 1 || len(ex) != 0 {
		t.Fatalf("got %d matches, %d expired", len(ms), len(ex))
	}
	if got := teams(ms[0]); got != "[1 3 5 7 9|2 4 6 8 10]" || !ms[0].Full || ms[0].Size(0) != 5 || ms[0].Size(1) != 5 {
		t.Errorf("teams %s full %v", got, ms[0].Full)
	}
	if m.Len() != 0 {
		t.Errorf("%d tickets left", m.Len())
	}
}

// 1v1 up to 5v5: a full match needs 2*TeamSize players.
func TestTeamSizes(t *testing.T) {
	for size := 1; size <= MaxTeamSize; size++ {
		c := newClock()
		m := newMM(c, size, 2*size, 0)
		for i := 0; i < 2*size+1; i++ {
			mustAdd(t, m, ticket("pvp", "", 1))
		}
		ms, _ := m.Tick()
		if len(ms) != 1 || ms[0].Size(0) != size || ms[0].Size(1) != size || m.Len() != 1 {
			t.Errorf("%dv%d: %d matches, left %d", size, size, len(ms), m.Len())
		}
	}
	if m := New(Config{TeamSize: 9, MinPlayers: 99}); m.Config().TeamSize != 5 || m.Config().MinPlayers != 10 {
		t.Errorf("clamped config %+v", m.Config())
	}
	if m := New(Config{}); m.Config().TeamSize != 1 || m.Config().MinPlayers != 1 {
		t.Errorf("zero config %+v", m.Config())
	}
}

// With MinPlayers 1 a lone player gets a game once FillAfter has passed (bots fill the rest later, M8).
func TestFillAfter(t *testing.T) {
	c := newClock()
	m := newMM(c, 5, 1, 10*time.Second)
	nextID = 0
	mustAdd(t, m, ticket("pvp", "na", 1))
	if ms, _ := m.Tick(); len(ms) != 0 {
		t.Fatal("started before FillAfter")
	}
	if d := m.NextDeadline(); !d.Equal(c.t.Add(10 * time.Second)) {
		t.Errorf("next deadline %v", d)
	}
	c.advance(5 * time.Second)
	mustAdd(t, m, ticket("pvp", "na", 1))
	c.advance(5 * time.Second)
	ms, _ := m.Tick()
	if len(ms) != 1 || teams(ms[0]) != "[1|2]" || ms[0].Full {
		t.Fatalf("matches %v", ms)
	}
	// FillAfter 0 and MinPlayers 1: at once
	m = newMM(c, 5, 1, 0)
	mustAdd(t, m, ticket("pvp", "", 1))
	if ms, _ := m.Tick(); len(ms) != 1 || ms[0].Size(0) != 1 || ms[0].Size(1) != 0 {
		t.Fatalf("instant match %v", ms)
	}
}

// MinPlayers holds a match back even after FillAfter.
func TestMinPlayers(t *testing.T) {
	c := newClock()
	m := newMM(c, 5, 4, time.Second)
	for i := 0; i < 3; i++ {
		mustAdd(t, m, ticket("pvp", "", 1))
	}
	c.advance(time.Minute)
	if ms, _ := m.Tick(); len(ms) != 0 {
		t.Fatal("started with 3 of 4")
	}
	mustAdd(t, m, ticket("pvp", "", 1)) // the new one hasn't waited, but the anchor has
	if ms, _ := m.Tick(); len(ms) != 1 || ms[0].Size(0) != 2 || ms[0].Size(1) != 2 {
		t.Fatalf("matches %v", ms)
	}
}

// Groups stay on one team; a group that fits nowhere waits for the next match.
func TestGroups(t *testing.T) {
	c := newClock()
	m := newMM(c, 5, 10, time.Hour)
	nextID = 0
	mustAdd(t, m, ticket("pvp", "", 3), ticket("pvp", "", 3), ticket("pvp", "", 3), ticket("pvp", "", 2), ticket("pvp", "", 2))
	ms, _ := m.Tick()
	if len(ms) != 1 || teams(ms[0]) != "[1 4|2 5]" {
		t.Fatalf("matches %v", ms)
	}
	if left, ok := m.Find(300); !ok || left.ID != 3 || m.Len() != 1 {
		t.Errorf("left %v %v", left, ok)
	}
	if err := m.Add(ticket("pvp", "", 6)); !errors.Is(err, ErrTooBig) {
		t.Errorf("6-player group: %v", err)
	}
	if err := m.Add(&Ticket{ID: 99}); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty ticket: %v", err)
	}
}

// Modes never meet; regions meet only if equal or one side is "any".
func TestModesAndRegions(t *testing.T) {
	c := newClock()
	m := newMM(c, 1, 2, time.Hour)
	nextID = 0
	mustAdd(t, m, ticket("pvp", "na", 1), ticket("pvp", "eu", 1), ticket("other", "na", 1))
	if ms, _ := m.Tick(); len(ms) != 0 {
		t.Fatalf("matched across region or mode: %v", teams(ms[0]))
	}
	mustAdd(t, m, ticket("pvp", "", 1)) // any region: joins the oldest compatible (na)
	ms, _ := m.Tick()
	if len(ms) != 1 || teams(ms[0]) != "[1|4]" || ms[0].Region != "na" {
		t.Fatalf("matches %v", ms)
	}
	mustAdd(t, m, ticket("pvp", "eu", 1))
	ms, _ = m.Tick()
	if len(ms) != 1 || teams(ms[0]) != "[2|5]" || ms[0].Region != "eu" {
		t.Fatalf("eu match %v", ms)
	}
	// an "any" anchor takes its region from the first ticket that has one
	m = newMM(c, 1, 2, 0)
	mustAdd(t, m, ticket("pvp", "", 1), ticket("pvp", "eu", 1))
	if ms, _ := m.Tick(); len(ms) != 1 || ms[0].Region != "eu" {
		t.Fatalf("any + eu: %v", ms)
	}
}

func TestDuplicatesCancelAndTimeout(t *testing.T) {
	c := newClock()
	m := newMM(c, 5, 10, time.Hour)
	nextID = 0
	a := ticket("pvp", "", 2)
	mustAdd(t, m, a)
	if err := m.Add(&Ticket{ID: 50, Players: []Player{{ID: a.Players[1].ID}}}); !errors.Is(err, ErrAlreadyQueued) {
		t.Errorf("player twice: %v", err)
	}
	if err := m.Add(&Ticket{ID: a.ID, Players: []Player{{ID: 7}}}); !errors.Is(err, ErrDuplicateID) {
		t.Errorf("id twice: %v", err)
	}
	if got, ok := m.Remove(a.ID); !ok || got != a {
		t.Error("remove failed")
	}
	if _, ok := m.Remove(a.ID); ok {
		t.Error("removed twice")
	}
	b := ticket("pvp", "", 1)
	b.Timeout = 30 * time.Second
	mustAdd(t, m, b, ticket("pvp", "", 1))
	if d := m.NextDeadline(); !d.Equal(c.t.Add(30 * time.Second)) {
		t.Errorf("next deadline %v", d)
	}
	c.advance(30 * time.Second)
	ms, ex := m.Tick()
	if len(ms) != 0 || len(ex) != 1 || ex[0] != b || m.Len() != 1 {
		t.Errorf("matches %d expired %v left %d", len(ms), ex, m.Len())
	}
}

// MaxWait (B10b, dgserver --mm-max-wait) caps a ticket's own timeout and gives one to a ticket without.
func TestMaxWait(t *testing.T) {
	c := newClock()
	m := New(Config{TeamSize: 5, MinPlayers: 10, FillAfter: time.Hour, MaxWait: 20 * time.Second, Now: c.now})
	nextID = 0
	long, none, short := ticket("pvp", "", 1), ticket("pvp", "", 1), ticket("pvp", "", 1)
	long.Timeout = 3 * time.Hour // the client's DUR
	short.Timeout = 10 * time.Second
	mustAdd(t, m, long, none, short)
	if long.Timeout != 20*time.Second || none.Timeout != 20*time.Second || short.Timeout != 10*time.Second {
		t.Errorf("timeouts %v %v %v", long.Timeout, none.Timeout, short.Timeout)
	}
	if s := m.Config().String(); !strings.HasSuffix(s, ", wait at most 20s") {
		t.Errorf("config %q", s)
	}
	c.advance(20 * time.Second)
	if _, ex := m.Tick(); len(ex) != 3 || m.Len() != 0 {
		t.Errorf("expired %d, left %d", len(ex), m.Len())
	}
}

// Several matches form in one tick, oldest anchors first.
func TestSeveralMatchesPerTick(t *testing.T) {
	c := newClock()
	m := newMM(c, 1, 2, time.Hour)
	nextID = 0
	for i := 0; i < 5; i++ {
		mustAdd(t, m, ticket("pvp", "", 1))
	}
	ms, _ := m.Tick()
	if len(ms) != 2 || teams(ms[0]) != "[1|2]" || teams(ms[1]) != "[3|4]" || m.Len() != 1 {
		t.Fatalf("matches %d left %d", len(ms), m.Len())
	}
}

// MMWAIT: a smaller match waits FillAfter after its anchor, FillExtend more for each session that joins;
// a full match starts at once. Nine solo players joining just in time keep a 5v5 waiting 90 + 8 × 30 s.
func TestFillExtend(t *testing.T) {
	c := newClock()
	start := c.t
	m := New(Config{TeamSize: 5, MinPlayers: 1, FillAfter: 90 * time.Second, FillExtend: 30 * time.Second, Now: c.now})
	nextID = 0
	mustAdd(t, m, ticket("pvp", "", 1))
	if d := m.NextDeadline(); !d.Equal(start.Add(90 * time.Second)) {
		t.Fatalf("next deadline %v", d.Sub(start))
	}
	for i := 0; i < 8; i++ { // each joins 1 s before the deadline so far
		c.t = m.NextDeadline().Add(-time.Second)
		if ms, _ := m.Tick(); len(ms) != 0 {
			t.Fatalf("join %d: started early", i)
		}
		mustAdd(t, m, ticket("pvp", "", 1))
	}
	if d := m.NextDeadline().Sub(start); d != 330*time.Second {
		t.Fatalf("deadline after 8 joins %v", d)
	}
	c.t = start.Add(330*time.Second - time.Millisecond)
	if ms, _ := m.Tick(); len(ms) != 0 {
		t.Fatal("started before 330 s")
	}
	c.t = start.Add(330 * time.Second)
	if ms, _ := m.Tick(); len(ms) != 1 || ms[0].Full || teams(ms[0]) != "[1 3 5 7 9|2 4 6 8]" {
		t.Fatalf("matches %v", ms)
	}

	// the tenth player fills the match: at once
	c.t = start
	nextID = 0
	for i := 0; i < 10; i++ {
		mustAdd(t, m, ticket("pvp", "", 1))
		c.advance(time.Second)
	}
	if ms, _ := m.Tick(); len(ms) != 1 || !ms[0].Full {
		t.Fatalf("full match %v", ms)
	}

	// a party is one join: +30 s once
	c.t = start
	mustAdd(t, m, ticket("pvp", "", 1))
	c.advance(10 * time.Second)
	mustAdd(t, m, ticket("pvp", "", 3))
	if d := m.NextDeadline().Sub(start); d != 120*time.Second {
		t.Fatalf("deadline with a party %v", d)
	}
	// a cancel takes its extension back
	m.Remove(nextID)
	if d := m.NextDeadline().Sub(start); d != 90*time.Second {
		t.Fatalf("deadline after the cancel %v", d)
	}
}

// DISCORD2b: Snapshot gives one mode's tickets, its player count and the earliest match start (MMWAIT's rule).
func TestSnapshot(t *testing.T) {
	c := newClock()
	start := c.t
	m := New(Config{TeamSize: 5, MinPlayers: 1, FillAfter: 90 * time.Second, FillExtend: 30 * time.Second, Now: c.now})
	nextID = 0
	if st := m.Snapshot("pvp"); len(st.Tickets) != 0 || st.Players != 0 || !st.StartsAt.IsZero() || st.Capacity != 10 {
		t.Fatalf("empty queue %+v", st)
	}
	mustAdd(t, m, ticket("pvp", "", 1))
	c.advance(10 * time.Second)
	mustAdd(t, m, ticket("pvp", "", 3), ticket("custom", "", 1))
	st := m.Snapshot("pvp")
	if len(st.Tickets) != 2 || st.Players != 4 || st.Tickets[1].Players[0].Name != "p2_0" || !st.Tickets[0].Queued.Equal(start) {
		t.Fatalf("snapshot %+v", st)
	}
	if d := st.StartsAt.Sub(start); d != 120*time.Second { // 90 s after the first, +30 s for the party
		t.Fatalf("starts after %v", d)
	}
	if st := m.Snapshot("custom"); st.Players != 1 || st.StartsAt.Sub(start) != 100*time.Second {
		t.Fatalf("custom %+v", st)
	}
	// a full match starts now
	mustAdd(t, m, ticket("pvp", "", 5), ticket("pvp", "", 1))
	if st := m.Snapshot("pvp"); st.Players != 10 || !st.StartsAt.Equal(c.t) {
		t.Fatalf("full: %+v", st)
	}
	// fewer than MinPlayers: no start time
	m2 := New(Config{TeamSize: 5, MinPlayers: 2, FillAfter: time.Minute, Now: c.now})
	mustAdd(t, m2, ticket("pvp", "", 1))
	if st := m2.Snapshot("pvp"); st.Players != 1 || !st.StartsAt.IsZero() {
		t.Fatalf("below MinPlayers %+v", st)
	}
}
