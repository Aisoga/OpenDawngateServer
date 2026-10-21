package blaze

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/discordhook"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/tdf"
)

// fakeQueueHook records the queue message's posts and edits as "post: <content> [ping <roles>]" and "edit: ...".
type fakeQueueHook struct {
	mu  sync.Mutex
	ops []string
}

func (f *fakeQueueHook) Post(m discordhook.Message) *discordhook.Ref {
	f.add("post", m)
	return nil
}

func (f *fakeQueueHook) Edit(_ *discordhook.Ref, m discordhook.Message) { f.add("edit", m) }

func (f *fakeQueueHook) add(op string, m discordhook.Message) {
	s := op + ": " + m.Content
	if len(m.PingRoles) > 0 {
		s += fmt.Sprintf(" [ping %v]", m.PingRoles)
	}
	f.mu.Lock()
	f.ops = append(f.ops, s)
	f.mu.Unlock()
}

// take returns the operations since the last take.
func (f *fakeQueueHook) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ops := f.ops
	f.ops = nil
	return ops
}

// queueRig drives a DiscordQueue with a fake clock and a fake throttle timer.
type queueRig struct {
	t       *testing.T
	hook    *fakeQueueHook
	q       *DiscordQueue
	now     time.Time
	pending []func()
	view    QueueView
}

const testRole = "123456789012345678"

func newQueueRig(t *testing.T, set discordhook.Settings) *queueRig {
	r := &queueRig{t: t, hook: &fakeQueueHook{}, now: time.Unix(1792600000, 0)}
	r.q = newDiscordQueue(r.hook, set, func() time.Time { return r.now })
	r.q.after = func(_ time.Duration, f func()) { r.pending = append(r.pending, f) }
	r.q.resync = func() { r.q.Update(r.view, nil) }
	r.view.Bots = true
	return r
}

func defaultQueueSettings() discordhook.Settings {
	s := discordhook.DefaultSettings()
	s.PingRole = testRole
	return s
}

// show sets the queue: tickets as "Name" or "Name+2" (a party of 3), the match start in sec seconds (<0: unknown).
func (r *queueRig) show(sec int, started []MatchStarted, tickets ...string) {
	st := matchmaker.QueueState{Mode: "pvp", Capacity: 10}
	for i, tk := range tickets {
		name, extra := tk, 0
		if j := strings.IndexByte(tk, '+'); j >= 0 {
			name = tk[:j]
			fmt.Sscan(tk[j+1:], &extra)
		}
		ts := matchmaker.TicketState{ID: int64(i + 1), Players: []matchmaker.Player{{ID: int64(i*10 + 1), Name: name}}}
		for k := 0; k < extra; k++ {
			ts.Players = append(ts.Players, matchmaker.Player{ID: int64(i*10 + 2 + k), Name: fmt.Sprintf("%s_m%d", name, k)})
		}
		st.Tickets = append(st.Tickets, ts)
		st.Players += len(ts.Players)
	}
	if sec >= 0 && len(tickets) > 0 {
		st.StartsAt = r.now.Add(time.Duration(sec) * time.Second)
	}
	r.view.State = st
	r.q.Update(r.view, started)
}

func (r *queueRig) advance(d time.Duration) { r.now = r.now.Add(d) }

// fire runs the throttle's pending timers.
func (r *queueRig) fire() {
	p := r.pending
	r.pending = nil
	for _, f := range p {
		f()
	}
}

func (r *queueRig) expect(what string, want ...string) {
	r.t.Helper()
	got := r.hook.take()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		r.t.Errorf("%s:\ngot\n  %s\nwant\n  %s", what, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func (r *queueRig) ts(sec int) string {
	return discordhook.Timestamp(r.now.Add(time.Duration(sec)*time.Second), "R")
}

// DISCORD2b: the sequence of posts and edits for joins, a leave, a party, the throttle, a match start, a timeout,
// the role ping's cooldown and its least wait.
func TestDiscordQueueSequence(t *testing.T) {
	r := newQueueRig(t, defaultQueueSettings())
	ping := "<@&" + testRole + "> "

	// the first player: a post with the role ping
	r.show(90, nil, "Alice")
	r.expect("first join", "post: "+ping+"**Alice** is queuing for PvP — 1/10 · starts "+r.ts(90)+", bots fill the open seats [ping ["+testRole+"]]")
	start := r.now

	// a party joins 1 s later: throttled, then one edit with the new deadline (+30 s); the mention stays, no ping
	r.advance(time.Second)
	r.show(119, nil, "Alice", "Bob+2")
	r.expect("throttled join")
	if len(r.pending) != 1 {
		t.Fatalf("%d timers armed", len(r.pending))
	}
	r.advance(time.Second)
	r.fire()
	at := start.Add(120 * time.Second)
	live := "**Alice** and **Bob** + 2 are queuing for PvP — 4/10 · starts " + discordhook.Timestamp(at, "R") + ", bots fill the open seats"
	r.expect("edit after 2 s", "edit: "+ping+live)

	// nothing changed: no edit
	r.advance(5 * time.Second)
	r.q.Update(r.view, nil)
	r.expect("unchanged")

	// Alice leaves (more than 2 s later: at once)
	r.show(int(at.Sub(r.now)/time.Second), nil, "Bob+2")
	r.expect("leave", "edit: "+ping+"**Bob** + 2 is queuing for PvP — 3/10 · starts "+discordhook.Timestamp(at, "R")+", bots fill the open seats")

	// the match starts: the message ends; the queue is empty, nothing more
	r.advance(10 * time.Second)
	r.show(-1, []MatchStarted{{Players: 3, Bots: 7}})
	r.expect("match start", "edit: **PvP match started**: 3 players + 7 bots")

	// a new player within the cooldown: a new message, no ping
	r.advance(time.Minute)
	r.show(90, nil, "Carol")
	r.expect("cooldown", "post: **Carol** is queuing for PvP — 1/10 · starts "+r.ts(90)+", bots fill the open seats")

	// Carol's session times out (or is canceled, or disconnects): the queue closes
	r.advance(90 * time.Second)
	r.show(-1, nil)
	r.expect("timeout", "edit: **PvP queue closed**")
	r.show(-1, nil)
	r.expect("closed stays closed")

	// after the cooldown, but the match starts in 20 s (under the 30 s least wait): no ping
	r.advance(15 * time.Minute)
	r.show(20, nil, "Dave")
	r.expect("least wait", "post: **Dave** is queuing for PvP — 1/10 · starts "+r.ts(20)+", bots fill the open seats")

	// a match starts while others wait: the old message ends, a new one for the rest (cooldown passed: ping)
	r.advance(time.Second)
	r.show(90, []MatchStarted{{Players: 1, Bots: 9}}, "Eve")
	r.expect("match and a new queue",
		"edit: **PvP match started**: 1 player + 9 bots",
		"post: "+ping+"**Eve** is queuing for PvP — 1/10 · starts "+r.ts(90)+", bots fill the open seats [ping ["+testRole+"]]")

	// a timer armed before the message ended finds nothing to do
	r.advance(time.Second)
	r.show(91, nil, "Eve", "Finn")
	r.show(-1, []MatchStarted{{Players: 2, Open: 8}})
	r.expect("match before the throttled edit", "edit: **PvP match started**: 2 players, 8 open seats")
	r.fire()
	r.expect("stale timer")
}

// DISCORD2b: a match that forms with no message open (it filled or started at once) is posted; a full queue says
// "starting now"; fewer than MinPlayers "waiting for more players"; no bots "open seats stay empty".
func TestDiscordQueueTexts(t *testing.T) {
	r := newQueueRig(t, discordhook.DefaultSettings()) // no role
	r.show(-1, []MatchStarted{{Players: 10}})
	r.expect("instant match", "post: **PvP match started**: 10 players")

	names := []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J"}
	r.show(0, nil, names...)
	r.expect("full", "post: **A**, **B**, **C**, **D**, **E**, **F**, **G**, **H**, **I** and **J** are queuing for PvP — 10/10 · starting now")
	r.show(-1, []MatchStarted{{Players: 10}})
	r.hook.take()

	r.view.Bots = false
	r.show(-1, nil, "Solo")
	r.expect("below MinPlayers", "post: **Solo** is queuing for PvP — 1/10 · waiting for more players")
	r.advance(3 * time.Second)
	r.show(60, nil, "Solo", "Duo")
	r.expect("no bots", "edit: **Solo** and **Duo** are queuing for PvP — 2/10 · starts "+r.ts(60)+", open seats stay empty")
}

// DISCORD2b: names off ("A player", "N players"), the queue message posted anew per change (discord_queue_edit off),
// and a name that tries to ping is escaped and pings nobody.
func TestDiscordQueueNamesOffAndPosts(t *testing.T) {
	set := defaultQueueSettings()
	set.Names, set.QueueEdit = false, false
	r := newQueueRig(t, set)
	r.show(90, nil, "Alice")
	ping := "<@&" + testRole + "> "
	r.expect("first", "post: "+ping+"A player is queuing for PvP — 1/10 · starts "+r.ts(90)+", bots fill the open seats [ping ["+testRole+"]]")
	r.advance(3 * time.Second)
	r.show(117, nil, "Alice", "Bob+2")
	r.expect("second: a new post, no ping", "post: 4 players are queuing for PvP — 4/10 · starts "+r.ts(117)+", bots fill the open seats")
	r.advance(3 * time.Second)
	r.show(-1, nil)
	r.expect("closed: a new post", "post: **PvP queue closed**")

	r = newQueueRig(t, discordhook.DefaultSettings())
	r.show(90, nil, "@everyone")
	got := r.hook.take()
	if len(got) != 1 || strings.Contains(got[0], "[ping") || strings.Contains(got[0], "@everyone") ||
		!strings.Contains(got[0], `**\@`) {
		t.Errorf("escaped name: %q", got)
	}
}

// DISCORD2b: through the server: QUEUE SOLO posts the message, the cancel closes it, a timeout too, and a match that
// forms at once is posted as started. Custom and practice games never reach the matchmaker, so never post.
func TestDiscordQueueServer(t *testing.T) {
	hook := &fakeQueueHook{}
	ts := mmServer(t, 5, 1, time.Hour)
	ts.s.SetDiscordQueue(newDiscordQueue(hook, discordhook.DefaultSettings(), time.Now))
	a := ts.client("alpha@example.com")
	msid := a.queue(0)
	waitOps := func(n int) []string {
		t.Helper()
		var ops []string
		for deadline := time.Now().Add(5 * time.Second); len(ops) < n; time.Sleep(10 * time.Millisecond) {
			hook.mu.Lock()
			ops = append([]string(nil), hook.ops...)
			hook.mu.Unlock()
			if time.Now().After(deadline) {
				t.Fatalf("ops %q, want %d", ops, n)
			}
		}
		hook.take()
		return ops
	}
	ops := waitOps(1)
	if !strings.HasPrefix(ops[0], "post: **alpha** is queuing for PvP — 1/10 · starts <t:") ||
		!strings.HasSuffix(ops[0], ":R>, open seats stay empty") {
		t.Errorf("queued: %q", ops)
	}
	a.send(ComponentGameManager, CmdGameManagerCancelMatchmaking, tdf.NewWriter().Int("MSID", msid).Bytes())
	a.note(ComponentGameManager, NotifyMatchmakingFailedID)
	if ops := waitOps(1); ops[0] != "edit: **PvP queue closed**" {
		t.Errorf("canceled: %q", ops)
	}
	b := ts.client("bravo@example.com")
	b.queue(100) // DUR 100 ms: times out
	b.note(ComponentGameManager, NotifyMatchmakingFailedID)
	if ops := waitOps(2); !strings.HasPrefix(ops[0], "post: **bravo** is queuing") || ops[1] != "edit: **PvP queue closed**" {
		t.Errorf("timed out: %q", ops)
	}
	if log := ts.log.String(); !strings.Contains(log, "discord queue message posted: 1/10 players (no role ping)") ||
		!strings.Contains(log, "discord queue message: PvP queue closed") {
		t.Errorf("log:\n%s", log)
	}

	ts2 := mmServer(t, 5, 1, 0)
	hook2 := &fakeQueueHook{}
	ts2.s.SetDiscordQueue(newDiscordQueue(hook2, discordhook.DefaultSettings(), time.Now))
	c := ts2.client("charlie@example.com")
	c.queue(0)
	c.gameSetup()
	hook = hook2
	if ops := waitOps(1); ops[0] != "post: **PvP match started**: 1 player, 9 open seats" {
		t.Errorf("instant match: %q", ops)
	}
}
