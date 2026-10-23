package blaze

import (
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/tdf/tdfcheck"
)

func TestOnlineCountText(t *testing.T) {
	for _, c := range []struct {
		online, inMatch int
		want            string
	}{
		{1, 0, "1 player online"},
		{2, 0, "2 players online"},
		{14, 3, "14 players online, 3 in a match"},
		{1, 1, "1 player online, 1 in a match"},
		{0, 0, "0 players online"},
	} {
		if got := OnlineCountText(c.online, c.inMatch); got != c.want {
			t.Errorf("OnlineCountText(%d, %d) = %q, want %q", c.online, c.inMatch, got, c.want)
		}
	}
}

// The count's NotifyMessage matches tdf_golden.py's independent writer and the client's member table.
func TestOnlineCountGolden(t *testing.T) {
	got := SystemMessageNotification(4, OnlineCountMessage(OnlineCountText(2, 1)), testTime)
	tdfcheck.Require(t, "Blaze::Messaging::ServerMessage", got)
	if h, want := hex.EncodeToString(got), golden.Load(t).WriterCase(t, "notifymessage_onlinecount_notification").Hex; h != want {
		t.Errorf("got %s\nwant %s", h, want)
	}
}

// fakeClock is a settable s.Now for the throttle.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// wantCount waits for a system message and checks it is the count's TAG 2 with text.
func (tc *tcpClient) wantCount(text string) {
	tc.t.Helper()
	tag, attrs := tc.systemNote()
	if tag != SystemTagBroadcast || attrs[BroadcastAttrID] != "100" || attrs[BroadcastAttrFlags] != "4" ||
		attrs[BroadcastAttrSeconds] != "604800" || attrs[BroadcastAttrText] != text {
		tc.t.Errorf("%s: want the count %q, got tag %d %v", tc.name, text, tag, attrs)
	}
}

// wantList waits for TAG 1 and a TAG 2 per text (the operator's list, ids 1, 2, ...).
func (tc *tcpClient) wantList(texts ...string) {
	tc.t.Helper()
	if tag, _ := tc.systemNote(); tag != SystemTagHideBroadcasts {
		tc.t.Errorf("%s: tag %d, want hide all first", tc.name, tag)
	}
	for i, text := range texts {
		tag, attrs := tc.systemNote()
		if tag != SystemTagBroadcast || attrs[BroadcastAttrID] != string(rune('1'+i)) || attrs[BroadcastAttrText] != text {
			tc.t.Errorf("%s: broadcast %d: tag %d %v, want %q", tc.name, i+1, tag, attrs, text)
		}
	}
}

// ONLINECOUNT: a login gets the operator's lines and then the count with itself counted; a change goes to the
// clients holding another text (only the count's TAG 2), at once when the last push is old enough, else once the
// interval has passed (a burst becomes one push); a client that leaves stops counting; an operator's change re-sends
// the count after its TAG 1; players in a running match are counted.
func TestOnlineCountPushed(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	ts := newTCPServer(t, func(s *Server) {
		s.Now = clock.now
		s.SetBroadcasts([]Broadcast{{Flags: BroadcastMarquee, Text: "Welcome"}})
		s.enableOnlineCount(30 * time.Second)
	})
	s := ts.s
	tick := func(want int) {
		t.Helper()
		if n := s.onlineCountTick(); n != want {
			t.Errorf("tick: sent to %d connections, want %d", n, want)
		}
	}
	a := ts.client("alpha@example.com")
	a.wantList("Welcome")
	a.wantCount("1 player online")
	tick(0) // a holds the current count

	b := ts.client("bravo@example.com")
	b.wantList("Welcome")
	b.wantCount("2 players online")
	tick(1) // a only: the first push goes out at once
	a.wantCount("2 players online")
	b.none(ComponentMessaging, NotifyMessageID, 20*time.Millisecond)

	c := ts.client("charlie@example.com") // a burst: within the interval
	c.wantList("Welcome")
	c.wantCount("3 players online")
	tick(0)
	clock.add(29 * time.Second)
	tick(0)
	clock.add(time.Second)
	tick(2)
	a.wantCount("3 players online")
	b.wantCount("3 players online")

	b.c.Close() // b logs out (its connection ends): a and c see 2 after the interval
	deadline := time.Now().Add(5 * time.Second)
	countConns := func() int { // b's session-end hooks: the relay's online entry first, the count's after it
		s.sys.mu.Lock()
		defer s.sys.mu.Unlock()
		return len(s.sys.count.sent)
	}
	for s.OnlineUsers() != 2 || countConns() != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("online users %d after b left", s.OnlineUsers())
		}
		time.Sleep(5 * time.Millisecond)
	}
	tick(0)
	clock.add(30 * time.Second)
	tick(2)
	a.wantCount("2 players online")
	c.wantCount("2 players online")
	s.sys.mu.Lock()
	if n := len(s.sys.count.sent); n != 2 {
		t.Errorf("count connections %d, want 2 (b forgotten)", n)
	}
	s.sys.mu.Unlock()

	// the operator's change: hide all, the list, then the count again (TAG 1 hid it too)
	s.SetBroadcasts([]Broadcast{{Flags: BroadcastMarquee, Text: "Welcome"}, {Flags: BroadcastMarquee, Text: "Restart at 20:00"}})
	for _, x := range []*tcpClient{a, c} {
		x.wantList("Welcome", "Restart at 20:00")
		x.wantCount("2 players online")
	}

	// in a match: a running hosted game with a (c's custom lobby before its start doesn't count, nor an ended game)
	s.hosted.add(&hostedGame{s: s, game: Game{ID: 7}, players: []*hostedPlayer{{p: Player{ID: a.id}}}})
	s.hosted.add(&hostedGame{s: s, game: Game{ID: 8}, players: []*hostedPlayer{{p: Player{ID: c.id}}}, custom: &customLobby{}})
	s.hosted.add(&hostedGame{s: s, game: Game{ID: 9}, players: []*hostedPlayer{{p: Player{ID: c.id}}}, ended: true})
	clock.add(30 * time.Second)
	tick(2)
	a.wantCount("2 players online, 1 in a match")
	c.wantCount("2 players online, 1 in a match")
	s.hosted.get(8).custom.started = true
	clock.add(30 * time.Second)
	tick(2)
	a.wantCount("2 players online, 2 in a match")
	c.wantCount("2 players online, 2 in a match")
	tick(0)

	log := ts.log.String()
	for _, want := range []string{
		`system messages for ` + strconv.FormatInt(a.id, 10) + ` "alpha": kill switches none; broadcasts 1; online count "1 player online"`,
		`online count: "3 players online"; sent to 2 logged-in connections`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// ONLINECOUNT without operator lines: the login gets TAG 1 (broadcasts count as on) and the count alone; with the
// count off (the blaze default) a server without broadcasts sends nothing.
func TestOnlineCountWithoutOperatorLines(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) { s.enableOnlineCount(0) })
	a := ts.client("alpha@example.com")
	a.wantList()
	a.wantCount("1 player online")
	if ts.s.sys.count.every != DefaultOnlineCountEvery {
		t.Errorf("every %s", ts.s.sys.count.every)
	}

	off := newTCPServer(t, nil)
	x := off.client("alpha@example.com")
	x.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	if n := off.s.onlineCountTick(); n != 0 {
		t.Errorf("count off: sent to %d", n)
	}
}

// StartOnlineCount's goroutine pushes a change by itself and stops.
func TestStartOnlineCount(t *testing.T) {
	ts := newTCPServer(t, nil)
	stop := ts.s.StartOnlineCount(10 * time.Millisecond)
	defer stop()
	a := ts.client("alpha@example.com")
	a.wantList()
	a.wantCount("1 player online")
	ts.client("bravo@example.com")
	a.wantCount("2 players online") // within a poll (10 ms here)
	stop()
	stop() // twice is fine
}
