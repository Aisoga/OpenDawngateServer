package blaze

import (
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/limits"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

// testLimits is the public profile with every per-test limit off (zero rate = unlimited), for one limit at a time.
func testLimits() limits.Config {
	c := limits.Default()
	c.Logins, c.Requests, c.Games, c.Queues = limits.Rate{}, limits.Rate{}, limits.Rate{}, limits.Rate{}
	c.AccountFail, c.AddrFail = limits.Backoff{}, limits.Backoff{}
	c.GamesAtOnce = 0
	return c
}

// loginFrame logs in with a password long enough for the check (8+ characters).
func loginFrame(tc *tcpClient, name string) fire.Frame {
	tc.t.Helper()
	id := tc.send(1, 0x28, tdf.NewWriter().String("MAIL", name).String("PASS", "dawngate1").String("TOKN", "").Int("TYPE", 0).Bytes())
	return tc.reply(1, 0x28, id)
}

// logins over the address's rate get ERR_SERVER_BUSY (0x4072, the client's LOGIN ERROR panel, the connection
// kept), before the login handler runs.
func TestLimitsLoginRate(t *testing.T) {
	cfg := testLimits()
	cfg.Logins = limits.Rate{PerSec: 1.0 / 60, Burst: 2}
	ts := newTCPServer(t, func(s *Server) { s.Limits = limits.New(cfg, nil) })
	a := ts.dial("a")
	for i := 0; i < 2; i++ {
		if f := loginFrame(a, "flood"); f.Type != fire.Reply {
			t.Fatalf("login %d: %s %#x", i, f.Type, f.Error)
		}
	}
	f := loginFrame(a, "flood")
	if f.Type != fire.ErrorReply || f.Error != 0x4072 || len(f.Payload) != 0 {
		t.Fatalf("3rd login: %s error %#x", f.Type, f.Error)
	}
	// same address, another connection: the same bucket
	if f := loginFrame(ts.dial("b"), "other"); f.Error != 0x4072 {
		t.Fatalf("another connection from the address: %s %#x", f.Type, f.Error)
	}
	if f := loginFrame(a, "flood"); f.Error != 0x4072 { // the connection stays usable
		t.Fatalf("4th: %#x", f.Error)
	}
	log := ts.log.String()
	for _, want := range []string{"limits: login refused (logins from 127.0.0.1 over 1/min (burst 2); try again in ",
		"-> login error reply (error 0x4072), 0 bytes"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if r := ts.s.Limits.TakeReport(); r != "refused: login 3 (1 addresses)" {
		t.Errorf("report %q", r)
	}
}

// failed logins (here the test aid's error replies; the wrong passwords alike) start the account's
// backoff; a refused attempt doesn't reach the login handler (its test counter doesn't move); a success clears it.
func TestLimitsLoginBackoff(t *testing.T) {
	cfg := testLimits()
	cfg.AccountFail = limits.Backoff{Free: 2, Base: time.Hour, Max: time.Hour}
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	ts := newTCPServer(t, func(s *Server) {
		s.TestLoginErrors = []uint16{0x0C, 0x0C, 0x0C}
		s.Limits = limits.New(cfg, func() time.Time { return now })
	})
	a := ts.dial("a")
	for i := 0; i < 3; i++ {
		if f := loginFrame(a, "Victim"); f.Error != 0x0C {
			t.Fatalf("login %d: %#x", i, f.Error)
		}
	}
	if f := loginFrame(a, " victim "); f.Error != 0x4072 { // the same account (trimmed, any case)
		t.Fatalf("after 3 failures: %s %#x", f.Type, f.Error)
	}
	if f := loginFrame(a, "someone"); f.Type != fire.Reply { // the 4th login reaching the handler: past the test list
		t.Fatalf("another account: %s %#x", f.Type, f.Error)
	}
	now = now.Add(time.Hour)
	if f := loginFrame(a, "victim"); f.Type != fire.Reply {
		t.Fatalf("an hour later: %s %#x", f.Type, f.Error)
	}
	if !strings.Contains(ts.log.String(), `limits: login refused (3 failed logins for "victim" in a row; locked for 1h0m0s more`) {
		t.Errorf("log:\n%s", ts.log.String())
	}
}

// a connection whose requests come faster than the rate is slowed down (every request is still answered), and
// one held back for longer than RequestHeld is closed.
func TestLimitsRequestRate(t *testing.T) {
	cfg := testLimits()
	// slow enough that a loaded machine's handling of a ping takes well under a token's 40 ms (a whole token back
	// lets the client off)
	cfg.Requests = limits.Rate{PerSec: 25, Burst: 5}
	cfg.RequestHeld = 250 * time.Millisecond
	ts := newTCPServer(t, func(s *Server) { s.Limits = limits.New(cfg, nil) })
	a := ts.dial("a")
	start := time.Now()
	ids := make([]uint32, 8)
	for i := range ids {
		ids[i] = a.send(ComponentUtil, CmdUtilPing, nil)
	}
	for _, id := range ids {
		if f := a.reply(ComponentUtil, CmdUtilPing, id); f.Type != fire.Reply {
			t.Fatalf("ping %d: %s", id, f.Type)
		}
	}
	if d := time.Since(start); d < 100*time.Millisecond { // 3 past the burst at 25/s: 120 ms
		t.Errorf("8 pings answered in %s; want them slowed down", d)
	}
	b := ts.dial("b")
	for i := 0; i < 200; i++ {
		b.send(ComponentUtil, CmdUtilPing, nil)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b.mu.Lock()
		err := b.err
		b.mu.Unlock()
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the flooding connection wasn't closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(ts.log.String(), "limits: connection from 127.0.0.1 closed: requests held back to 25/s (burst 5) for more than 250ms") {
		t.Errorf("log:\n%s", ts.log.String())
	}
}

// a user's matchmaking starts over its rate get GAMEMANAGER_ERR_MATCHMAKING_EXCEEDED_MAX_REQUESTS.
func TestLimitsQueue(t *testing.T) {
	cfg := testLimits()
	cfg.Queues = limits.Rate{PerSec: 1.0 / 600, Burst: 1}
	ts := newTCPServer(t, func(s *Server) { s.Limits = limits.New(cfg, nil) })
	a := ts.client("queue@example.com")
	msid := a.queue(0)
	id := a.send(ComponentGameManager, CmdGameManagerCancelMatchmaking, tdf.NewWriter().Int("MSID", msid).Bytes())
	a.reply(ComponentGameManager, CmdGameManagerCancelMatchmaking, id)
	f := a.gm(CmdGameManagerStartMatchmaking, startMatchmakingRequest(0))
	wantError(t, "second queue", f, ErrMatchmakingExceededLimit)
	if !strings.Contains(ts.log.String(), "limits: matchmaking of account ") {
		t.Errorf("log:\n%s", ts.log.String())
	}
}

// a user in GamesAtOnce running hosted games can't start another (resetDedicatedServer's error reply for no
// host, ErrNoHostCapacity); nor beyond the rate.
func TestLimitsGamesAtOnce(t *testing.T) {
	cfg := testLimits()
	cfg.GamesAtOnce = 1
	hs := newHostedSessionFull(t, "", "", nil, func(s *Server) { s.Limits = limits.New(cfg, nil) },
		func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error { return receive(hs, h) })
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	hs.hostGot()
	hs.send(4, 0x19, 3, hs.request("creategame_request"))
	for {
		f := hs.next("the second game's answer")
		if f.Type == fire.Notification {
			continue
		}
		if f.Command != 0x19 || f.Type != fire.ErrorReply || f.ID != 3 || f.Error != ErrNoHostCapacity {
			t.Fatalf("second game: %+v", f)
		}
		break
	}
	if got := hs.s.hosted.runningFor(store.FirstUserID); got != 1 {
		t.Errorf("runningFor = %d", got)
	}
	hs.close()
	if log := hs.log.String(); !strings.Contains(log, "limits: game start of account ") ||
		!strings.Contains(log, "the user is in too many running games (1 running, at most 1)") {
		t.Errorf("log:\n%s", log)
	}
}

// a tunnelled login counts with the player's real address (tunnel.Addr.Peer), not the loopback address every
// tunnelled game socket has: two players through the tunnel aren't lumped together, and one player's flood doesn't
// lock the other out.
func TestLimitsLoginTunnelledRealAddress(t *testing.T) {
	cfg := testLimits()
	cfg.Logins = limits.Rate{PerSec: 1.0 / 60, Burst: 1}
	s := &Server{Log: newTestLog(t), Store: store.New(), Limits: limits.New(cfg, nil)}
	tunnelled := func(peer string, session uint64) *Conn {
		return &Conn{tun: &tunnel.Addr{IP: net.ParseIP("127.0.0.1").To4(), Port: 10041, Session: session,
			Peer: netip.MustParseAddr(peer)}}
	}
	a, b := tunnelled("203.0.113.7", 1), tunnelled("198.51.100.4", 2)
	if a.remoteIP() != "203.0.113.7" || b.remoteIP() != "198.51.100.4" {
		t.Fatalf("limit keys %q, %q", a.remoteIP(), b.remoteIP())
	}
	if (&Conn{}).remoteIP() != "" || (*Conn)(nil).remoteIP() != "" {
		t.Error("a conn without an address has a limit key")
	}
	handled := 0
	login := limitLogin(func(r *Request) []byte { handled++; return []byte{1} })
	try := func(c *Conn, name string) uint16 {
		body, err := tdf.Decode(tdf.NewWriter().String("MAIL", name).Bytes())
		if err != nil {
			t.Fatal(err)
		}
		r := &Request{Server: s, Conn: c, Body: body}
		login(r)
		return r.errCode
	}
	if code := try(a, "first@example.com"); code != 0 {
		t.Fatalf("A's first login: %#x", code)
	}
	if code := try(a, "first@example.com"); code != errLimitServerBusy {
		t.Errorf("A's second login: %#x, want ERR_SERVER_BUSY", code)
	}
	if code := try(b, "second@example.com"); code != 0 {
		t.Errorf("B's login after A's: %#x (lumped with A?)", code)
	}
	if handled != 2 {
		t.Errorf("the login handler ran %d times, want 2", handled)
	}
}
