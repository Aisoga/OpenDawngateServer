package blaze

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

// The players-online count in the main menu's marquee (ONLINECOUNT; for operators: docs/hosting.md *Announcements*).
// Our own feature: one generated broadcast (TAG 2, display
// flag 4 only: the marquee and shaper select, never the chat's System line or the notification panel, which would
// post a line per change) beside the operator's (SetBroadcasts), with its own message id, so the operator's lines
// keep theirs: "1 player online", "14 players online, 3 in a match".
//
// What the client does with it (GameUI_AddBroadcastMessage 0x004B7310, GameUI_UpdateBroadcastMessages 0x004B5DE0):
// a TAG 2 for a known id with the same flags and the same text only renews the record's expiry and marks nothing
// dirty, so the marquee isn't rebuilt (message 0xCB796E00 is posted only when the dirty byte GameUI +0x9608 is set);
// a changed text marks it dirty and the marquee is rebuilt once with every flag-4 record in the order the ids first
// arrived. TAG 1 (GameUI_HideBroadcastMessages 0x004ACC00) clears every record's flags and marks it dirty, and the
// TAG 2s after it set the flags again: a whole list rebuilds the marquee too. So a change of the count alone sends
// only the count's TAG 2 (the operator's records stay untouched), and only to the connections whose last count text
// differs; whether a rebuild restarts the marquee's scroll is the movie's (the live check).

// OnlineCountID is the count's broadcast id [our choice]: past the operator's ids 1..MaxBroadcasts.
const OnlineCountID = 100

// DefaultOnlineCountEvery is the least time between two pushes of a changed count to the logged-in clients
// (dgserver --online-count-every): a burst of logins and logouts becomes at most one push now and one after it.
const DefaultOnlineCountEvery = 30 * time.Second

// onlineCountPoll is how often the count is looked at while it is on (a change is pushed within this time when the
// last push is older than the interval).
const onlineCountPoll = time.Second

// onlineCount is the count's state, under systemMessages.mu (so a login, a list push and a count push stay in order).
type onlineCount struct {
	on       bool
	every    time.Duration
	sent     map[*Conn]string // the count text each logged-in connection got last (from its login on)
	lastPush time.Time        // the last push to the clients (logins don't count)
}

// OnlineCountText is the count's text [assumed wording]: "1 player online", "2 players online", with players in a
// running match "14 players online, 3 in a match". ASCII only: the client keeps the text as a narrow string whose
// encoding in the movie isn't known.
func OnlineCountText(online, inMatch int) string {
	s := plural(online, "player") + " online"
	if inMatch > 0 {
		s += fmt.Sprintf(", %d in a match", inMatch)
	}
	return s
}

// OnlineCountMessage is the count's broadcast (TAG 2, id OnlineCountID, the marquee only).
func OnlineCountMessage(text string) ClientMessage {
	return BroadcastMessage(Broadcast{ID: OnlineCountID, Flags: BroadcastMarquee, Text: text})
}

// PlayersInMatches is how many players are in a running match now: the distinct personas still in a game with a
// match host (from its creation over the loading and shaper select to its end; a player who left or lost its Blaze
// session isn't), a custom game only once its admin started it (its lobby isn't a match). Spectators of a started
// custom game count.
func (s *Server) PlayersInMatches() int {
	s.hosted.mu.Lock()
	games := make([]*hostedGame, 0, len(s.hosted.byID))
	for _, hg := range s.hosted.byID {
		games = append(games, hg)
	}
	s.hosted.mu.Unlock()
	var ids []int64
	for _, hg := range games {
		hg.mu.Lock()
		if !hg.ended && (hg.custom == nil || hg.custom.started) {
			for _, hp := range hg.players {
				if !hp.removed && !slices.Contains(ids, hp.p.ID) {
					ids = append(ids, hp.p.ID)
				}
			}
		}
		hg.mu.Unlock()
	}
	return len(ids)
}

// onlineCountTextNow is the count's text for this moment: the users logged in (OnlineUsers, the website's number:
// one per persona with a loginPersona'd Blaze connection, so neither a match host nor a connection that never
// logged in counts) and the players in a running match.
func (s *Server) onlineCountTextNow() string {
	return OnlineCountText(s.OnlineUsers(), s.PlayersInMatches())
}

// StartOnlineCount turns the players-online count on: every login from now on gets it after the operator's
// broadcasts (broadcasts count as on: the login also gets TAG 1), a list change re-sends it with the list, and a
// changed count goes to every logged-in client whose text differs, at most once per every (0 =
// DefaultOnlineCountEvery). A goroutine looks every onlineCountPoll until the returned stop function is called (which
// waits for it).
func (s *Server) StartOnlineCount(every time.Duration) (stop func()) {
	every = s.enableOnlineCount(every)
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(min(onlineCountPoll, every))
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
				s.onlineCountTick()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(quit) })
		<-done
	}
}

// enableOnlineCount turns the count on without its goroutine (the tests drive onlineCountTick) and returns the
// interval used.
func (s *Server) enableOnlineCount(every time.Duration) time.Duration {
	if every <= 0 {
		every = DefaultOnlineCountEvery
	}
	sm := &s.sys
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.count.on, sm.count.every = true, every
	if sm.count.sent == nil {
		sm.count.sent = map[*Conn]string{}
	}
	return every
}

// onlineCountTick pushes the count's TAG 2 to every logged-in connection that got its login messages and holds
// another text, unless the last push was less than the interval ago (then a later tick does it). It reports how many
// connections it sent to.
func (s *Server) onlineCountTick() int {
	sm := &s.sys
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.count.on {
		return 0
	}
	text := s.onlineCountTextNow()
	var to []*Conn
	for c, got := range sm.count.sent {
		if got != text {
			to = append(to, c)
		}
	}
	if len(to) == 0 {
		return 0
	}
	now := s.Now()
	if !sm.count.lastPush.IsZero() && now.Sub(sm.count.lastPush) < sm.count.every {
		return 0
	}
	sm.count.lastPush = now
	s.Log.Logf("blaze", "online count: %q; sent to %d logged-in connections", text, len(to))
	for _, c := range to {
		sm.count.sent[c] = text
		s.pushSystemLocked(c, []ClientMessage{OnlineCountMessage(text)})
	}
	return len(to)
}

// countMessagesLocked is the count's TAG 2 for c with the current text (nil when the count is off), and records
// that c got it (s.sys.mu held). The caller registers forgetCount once it has let go of the lock.
func (s *Server) countMessagesLocked(c *Conn) []ClientMessage {
	sm := &s.sys
	if !sm.count.on {
		return nil
	}
	text := s.onlineCountTextNow()
	sm.count.sent[c] = text
	return []ClientMessage{OnlineCountMessage(text)}
}

// forgetCount drops c from the count's connections when its session ends (a logout or the connection's close; at
// once when it has ended already). Called without s.sys.mu held: OnSessionEnd runs fn at once on a closed connection.
func (s *Server) forgetCount(c *Conn) {
	c.OnSessionEnd(func() {
		s.sys.mu.Lock()
		delete(s.sys.count.sent, c)
		s.sys.mu.Unlock()
	})
}
