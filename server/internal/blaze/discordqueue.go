package blaze

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/discordhook"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/matchmaker"
)

// The live PvP queue message in Discord (DISCORD2b; for operators: docs/hosting.md *Discord channel posts*). Our own feature: the
// original had nothing like it.
//
// The first player into an empty PvP queue posts one message to the queue webhook ("**Alice** is queuing for PvP —
// 1/10 · starts <t:…:R>, bots fill the open seats"), pinging the configured role when its cooldown has passed and the
// match is at least PingMinWait away. Each join, leave or new start time edits that message (at most one edit every
// queueEditEvery; the start time is a Discord timestamp, so it counts down in every reader's Discord without edits).
// A match that forms edits it into "PvP match started: 3 players + 7 bots", an emptied queue into "PvP queue closed";
// the next player then starts a new message. Only the matchmaker's PvP queue posts: custom games and practice never
// pass through it. With QueueEdit off every change is a new post instead (the ping only on a queue's first).

// queueEditEvery is the least time between two sends of the queue message (Discord's webhook limit is 5 requests
// per 2 s; a burst of joins becomes one edit).
const queueEditEvery = 2 * time.Second

// queueHook is what DiscordQueue needs of a webhook (*discordhook.Hook; a fake in the tests).
type queueHook interface {
	Post(discordhook.Message) *discordhook.Ref
	Edit(*discordhook.Ref, discordhook.Message)
}

// QueueView is the queue as the message shows it: the matchmaker's snapshot and whether open seats get bots.
type QueueView struct {
	State matchmaker.QueueState
	Bots  bool
}

// MatchStarted is a match the matchmaker formed and the server started: its humans, its bots and the seats left
// empty (no bots: --mm-bots=false or no match hosts).
type MatchStarted struct {
	Players, Bots, Open int
}

// DiscordQueue keeps the queue message in step with the queue. Its methods are safe for concurrent use; a nil
// *DiscordQueue does nothing.
type DiscordQueue struct {
	hook  queueHook
	set   discordhook.Settings
	now   func() time.Time
	after func(time.Duration, func()) // runs a function later (time.AfterFunc; the tests' fake clock)
	// resync shows the queue as it is now; called by the throttle's timer (the server's: through its matchmaking lock)
	resync func()
	logf   func(format string, args ...any)

	mu       sync.Mutex
	open     bool // a message shows a live queue
	ref      *discordhook.Ref
	mention  string // the role mention the open message starts with ("" = none)
	shown    string // the live text last sent
	lastSend time.Time
	lastPing time.Time
	armed    bool // the throttle's timer is pending
}

// NewDiscordQueue returns the queue message's keeper for the webhook h (nil: none, and nil for no webhook).
func NewDiscordQueue(h *discordhook.Hook, set discordhook.Settings) *DiscordQueue {
	if h == nil {
		return nil
	}
	return newDiscordQueue(h, set, time.Now)
}

func newDiscordQueue(h queueHook, set discordhook.Settings, now func() time.Time) *DiscordQueue {
	return &DiscordQueue{hook: h, set: set, now: now,
		after: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		logf:  func(string, ...any) {}}
}

// Update shows the queue v after the matches started (in the order they formed): each started match ends the open
// message ("PvP match started"; a new post when none was open), then a queue with players gets a message (a new one
// with the ping rules, or an edit of the open one when its text changed, throttled), an empty one closes the open
// message.
func (q *DiscordQueue) Update(v QueueView, started []MatchStarted) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	for _, m := range started {
		q.final(matchStartedText(m))
	}
	if v.State.Players == 0 {
		if q.open {
			q.final(queueClosedText)
		}
		return
	}
	text := queueText(v, now, q.set.Names)
	if !q.open {
		msg := discordhook.Message{Content: text}
		why := "no role ping"
		if q.set.PingRole != "" {
			why = q.pingRule(v.State.StartsAt, now)
			if why == "" {
				q.mention = discordhook.RoleMention(q.set.PingRole) + " "
				msg.Content = q.mention + text
				msg.PingRoles = []string{q.set.PingRole}
				q.lastPing = now
				why = "role pinged"
			}
		}
		q.ref = q.hook.Post(msg)
		q.open, q.shown, q.lastSend = true, text, now
		q.logf("discord queue message posted: %d/%d players (%s)", v.State.Players, v.State.Capacity, why)
		return
	}
	if text == q.shown {
		return
	}
	if wait := q.lastSend.Add(queueEditEvery).Sub(now); wait > 0 {
		if !q.armed {
			q.armed = true
			q.after(wait, func() {
				q.mu.Lock()
				q.armed = false
				q.mu.Unlock()
				if q.resync != nil {
					q.resync()
				}
			})
		}
		return
	}
	if q.set.QueueEdit {
		q.hook.Edit(q.ref, discordhook.Message{Content: q.mention + text})
	} else {
		q.ref = q.hook.Post(discordhook.Message{Content: text})
	}
	q.shown, q.lastSend = text, now
}

// pingRule says why the role isn't pinged ("" = ping): its cooldown, or a match too soon to be worth it.
func (q *DiscordQueue) pingRule(startsAt, now time.Time) string {
	if !q.lastPing.IsZero() && now.Sub(q.lastPing) < q.set.PingCooldown {
		return fmt.Sprintf("no role ping: last one %s ago, cooldown %s", now.Sub(q.lastPing).Round(time.Second), q.set.PingCooldown)
	}
	if !startsAt.IsZero() && startsAt.Sub(now) < q.set.PingMinWait {
		return fmt.Sprintf("no role ping: the match starts in %s, under %s", max(startsAt.Sub(now), 0).Round(time.Second), q.set.PingMinWait)
	}
	return ""
}

// final ends the open message with text (an edit, or a new post with QueueEdit off or when none is open); it goes out
// at once, past the throttle (the sender coalesces an edit not sent yet).
func (q *DiscordQueue) final(text string) {
	if q.open && q.set.QueueEdit {
		q.hook.Edit(q.ref, discordhook.Message{Content: text})
	} else {
		q.hook.Post(discordhook.Message{Content: text})
	}
	q.logf("discord queue message: %s", strings.ReplaceAll(text, "**", ""))
	q.open, q.ref, q.mention, q.shown = false, nil, "", ""
	q.lastSend = q.now()
}

const queueClosedText = "**PvP queue closed**"

// queueText is the live message: who is queuing, the count, when the match starts and what fills the open seats.
func queueText(v QueueView, now time.Time, names bool) string {
	st := v.State
	var who string
	if names {
		parts := make([]string, len(st.Tickets))
		for i, t := range st.Tickets {
			parts[i] = "**" + discordhook.Escape(t.Players[0].Name) + "**"
			if n := len(t.Players) - 1; n > 0 {
				parts[i] += fmt.Sprintf(" + %d", n)
			}
		}
		who = joinAnd(parts)
		if len(parts) == 1 {
			who += " is"
		} else {
			who += " are"
		}
	} else if st.Players == 1 {
		who = "A player is"
	} else {
		who = fmt.Sprintf("%d players are", st.Players)
	}
	s := fmt.Sprintf("%s queuing for PvP — %d/%d · ", who, st.Players, st.Capacity)
	switch {
	case st.StartsAt.IsZero():
		return s + "waiting for more players"
	case !st.StartsAt.After(now):
		s += "starting now"
	default:
		s += "starts " + discordhook.Timestamp(st.StartsAt, "R")
	}
	if st.Players >= st.Capacity {
		return s
	}
	if v.Bots {
		return s + ", bots fill the open seats"
	}
	return s + ", open seats stay empty"
}

// matchStartedText is the message's end when its match starts.
func matchStartedText(m MatchStarted) string {
	s := "**PvP match started**: " + plural(m.Players, "player")
	if m.Bots > 0 {
		s += " + " + plural(m.Bots, "bot")
	}
	if m.Open > 0 {
		s += ", " + plural(m.Open, "open seat")
	}
	return s
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func joinAnd(parts []string) string {
	if len(parts) <= 1 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// SetDiscordQueue turns the queue message on (q from NewDiscordQueue; nil = off). dgserver calls it once the
// webhooks are open.
func (s *Server) SetDiscordQueue(q *DiscordQueue) {
	if q != nil {
		q.resync = func() { s.discordQueueSync(nil, false) }
		q.logf = func(f string, a ...any) { s.Log.Logf("blaze", "   "+f, a...) }
	}
	s.discordQueue.Store(q)
}

// discordQueueSync shows the PvP queue as it is now. A tick that formed matches (formed) has counted itself in
// mm.forming before it started them; until its own sync, others skip, so no "queue closed" can overtake its "match
// started" (the queue no longer holds the match's tickets meanwhile).
func (s *Server) discordQueueSync(started []MatchStarted, formed bool) {
	s.mm.discordMu.Lock()
	defer s.mm.discordMu.Unlock()
	if formed {
		s.mm.forming--
	} else if s.mm.forming > 0 {
		return
	}
	v := QueueView{State: s.MM.Snapshot(gmshost.ModePvP), Bots: s.Hosts != nil && s.MMBots}
	s.roomQueueNews(v, started) // ROOMS2: the same news in the queue chat room (rooms.go)
	s.discordQueue.Load().Update(v, started)
}
