package limits

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time                 { return c.t }
func (c *clock) add(d time.Duration)            { c.t = c.t.Add(d) }
func newClock() *clock                          { return &clock{t: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)} }
func testLimiter(cfg Config) (*Limiter, *clock) { c := newClock(); return New(cfg, c.now), c }

func TestParseModeAndActive(t *testing.T) {
	public := func(a string) bool { return a == "203.0.113.5" }
	for _, c := range []struct {
		in   string
		mode Mode
	}{{"", Auto}, {"auto", Auto}, {"ON", On}, {"off", Off}, {"0", Off}} {
		m, err := ParseMode(c.in)
		if err != nil || m != c.mode {
			t.Errorf("ParseMode(%q) = %v, %v", c.in, m, err)
		}
	}
	if _, err := ParseMode("sometimes"); err == nil {
		t.Error("ParseMode accepted sometimes")
	}
	if Auto.Active(public, "127.0.0.1", "192.168.1.5") || !Auto.Active(public, "192.168.1.5", "203.0.113.5") {
		t.Error("auto: on only with a public address")
	}
	if !On.Active(public, "127.0.0.4") || Off.Active(public, "203.0.113.5") {
		t.Error("on/off ignore the addresses")
	}
}

func TestNilLimiterAllowsEverything(t *testing.T) {
	var l *Limiter
	if rel, ok, _ := l.Admit("x"); !ok {
		t.Fatal("nil Admit refused")
	} else {
		rel()
	}
	if !l.AllowPacket("x", 1<<20) || l.StartGame(1, 99) != nil || l.JoinQueue(1) != nil {
		t.Fatal("nil limiter refused")
	}
	if w, _ := l.Login("x", "a"); w != 0 {
		t.Fatal("nil Login waits")
	}
	g := l.RequestGate()
	for i := 0; i < 10000; i++ {
		if w, ok := g.Next(); w != 0 || !ok {
			t.Fatal("nil gate waits")
		}
	}
	if l.TakeReport() != "" {
		t.Fatal("nil report")
	}
}

// New connections per address: the burst, then the rate; another address has its own bucket.
func TestAcceptRate(t *testing.T) {
	cfg := Default()
	cfg.Accept = Rate{PerSec: 1, Burst: 3}
	l, c := testLimiter(cfg)
	for i := 0; i < 3; i++ {
		rel, ok, _ := l.Admit("10.0.0.1")
		if !ok {
			t.Fatalf("connection %d refused", i)
		}
		rel()
	}
	if _, ok, why := l.Admit("10.0.0.1"); ok || !strings.Contains(why, "new connections") {
		t.Fatalf("4th connection: ok %v, %q", ok, why)
	}
	if _, ok, _ := l.Admit("10.0.0.2"); !ok {
		t.Fatal("another address refused")
	}
	c.add(time.Second)
	if _, ok, _ := l.Admit("10.0.0.1"); !ok {
		t.Fatal("a second later: refused")
	}
	if r := l.TakeReport(); r != "refused: accept 1 (1 addresses)" {
		t.Fatalf("report %q", r)
	}
	if r := l.TakeReport(); r != "" {
		t.Fatalf("second report %q", r)
	}
}

// Open connections per address: the cap, and a release frees a slot (once, however often it is called).
func TestOpenPerAddress(t *testing.T) {
	cfg := Default()
	cfg.OpenPerAddr = 2
	l, _ := testLimiter(cfg)
	r1, _, _ := l.Admit("a")
	_, ok, _ := l.Admit("a")
	if !ok {
		t.Fatal("2nd refused")
	}
	if _, ok, why := l.Admit("a"); ok || !strings.Contains(why, "2 connections open") {
		t.Fatalf("3rd: %v %q", ok, why)
	}
	r1()
	r1()
	if l.Open("a") != 1 {
		t.Fatalf("open %d after a double release", l.Open("a"))
	}
	if _, ok, _ := l.Admit("a"); !ok {
		t.Fatal("after a release: refused")
	}
}

// UDP datagrams and bytes per address.
func TestPackets(t *testing.T) {
	cfg := Default()
	cfg.Packets = Rate{PerSec: 10, Burst: 5}
	cfg.Bytes = Rate{PerSec: 1000, Burst: 2000}
	l, c := testLimiter(cfg)
	n := 0
	for i := 0; i < 20; i++ {
		if l.AllowPacket("a", 100) {
			n++
		}
	}
	if n != 5 {
		t.Fatalf("burst let %d through", n)
	}
	c.add(time.Second)
	if !l.AllowPacket("a", 1500) || l.AllowPacket("a", 1500) {
		t.Fatal("the byte bucket (1,000 B/s, 2,000 burst) should take one 1,500-byte datagram, not two")
	}
	if !l.AllowPacket("b", 100) {
		t.Fatal("another address refused")
	}
}

// the tunnel gateway's channel datagrams have buckets and rates of their own (a tunnelled player's match
// traffic), apart from the QoS port's; the report names them.
func TestTunnelPackets(t *testing.T) {
	cfg := Default()
	cfg.Packets = Rate{PerSec: 1, Burst: 2}
	cfg.TunnelPackets = Rate{PerSec: 100, Burst: 50}
	cfg.TunnelBytes = Rate{PerSec: 10000, Burst: 4000}
	l, c := testLimiter(cfg)
	n := 0
	for i := 0; i < 80; i++ {
		if l.AllowTunnelPacket("a", 10) {
			n++
		}
	}
	if n != 50 {
		t.Fatalf("tunnel burst let %d through, want 50", n)
	}
	if !l.AllowPacket("a", 10) || !l.AllowPacket("a", 10) || l.AllowPacket("a", 10) {
		t.Fatal("the QoS bucket (burst 2) should be untouched by the tunnel's datagrams")
	}
	c.add(time.Second)
	if !l.AllowTunnelPacket("a", 3000) || l.AllowTunnelPacket("a", 3000) {
		t.Fatal("the tunnel byte bucket (4,000 burst) should take one 3,000-byte datagram, not two")
	}
	if !l.AllowTunnelPacket("b", 10) {
		t.Fatal("another address refused")
	}
	if r := l.TakeReport(); r != "refused: udp 1 (1 addresses), tunnel-udp 31 (1 addresses)" {
		t.Errorf("report %q", r)
	}
	if !(*Limiter)(nil).AllowTunnelPacket("x", 1<<20) {
		t.Error("nil limiter refused")
	}
}

// Logins: the rate per address, the account's backoff after its free failures (doubling, capped), the address's,
// and a success clearing both.
func TestLoginBackoff(t *testing.T) {
	cfg := Default()
	cfg.Logins = Rate{} // off here: the backoff alone
	cfg.AccountFail = Backoff{Free: 3, Base: 10 * time.Second, Max: 40 * time.Second, Forget: time.Hour}
	cfg.AddrFail = Backoff{Free: 100, Base: time.Minute, Max: time.Hour, Forget: time.Hour}
	l, c := testLimiter(cfg)
	fail := func() {
		t.Helper()
		if w, why := l.Login("1.2.3.4", "victim"); w != 0 {
			t.Fatalf("refused early: %s", why)
		}
		l.LoginDone("1.2.3.4", "victim", false)
	}
	for i := 0; i < 3; i++ {
		fail()
	}
	if w, _ := l.Login("1.2.3.4", "victim"); w != 0 {
		t.Fatal("3 failures are free")
	}
	l.LoginDone("1.2.3.4", "victim", false) // the 4th: 10 s
	if w, why := l.Login("5.6.7.8", "victim"); w != 10*time.Second || !strings.Contains(why, `4 failed logins for "victim"`) {
		t.Fatalf("after 4: wait %s (%s)", w, why)
	}
	if w, _ := l.Login("5.6.7.8", "other"); w != 0 {
		t.Fatal("another account is locked too")
	}
	c.add(10 * time.Second)
	fail() // the 5th: 20 s
	c.add(19 * time.Second)
	if w, _ := l.Login("1.2.3.4", "victim"); w != time.Second {
		t.Fatalf("after 5: wait %s, want 1s", w)
	}
	c.add(time.Second)
	fail() // 6th: 40 s
	if w, _ := l.Login("1.2.3.4", "victim"); w != 40*time.Second {
		t.Fatalf("after 6: wait %s, want 40s", w)
	}
	if got := cfg.AccountFail.lock(20); got != 40*time.Second {
		t.Fatalf("the cap: %s", got)
	}
	c.add(time.Minute)
	if w, _ := l.Login("1.2.3.4", "victim"); w != 0 {
		t.Fatal("still locked after the cap")
	}
	l.LoginDone("1.2.3.4", "victim", true)
	l.LoginDone("1.2.3.4", "victim", false)
	if w, _ := l.Login("1.2.3.4", "victim"); w != 0 {
		t.Fatal("a success didn't clear the failures")
	}
	// an hour of quiet forgets them too
	for i := 0; i < 6; i++ {
		l.LoginDone("9.9.9.9", "quiet", false)
	}
	c.add(2 * time.Hour)
	if w, _ := l.Login("9.9.9.9", "quiet"); w != 0 {
		t.Fatal("old failures not forgotten")
	}
}

func TestLoginAddressBackoffAndRate(t *testing.T) {
	cfg := Default()
	cfg.AddrFail = Backoff{Free: 2, Base: time.Minute, Max: time.Hour, Forget: time.Hour}
	cfg.AccountFail = Backoff{}
	cfg.Logins = Rate{PerSec: 1.0 / 6, Burst: 4}
	l, c := testLimiter(cfg)
	for i, acct := range []string{"a", "b", "c"} { // a spray over accounts from one address
		if w, why := l.Login("6.6.6.6", acct); w != 0 {
			t.Fatalf("login %d refused: %s", i, why)
		}
		l.LoginDone("6.6.6.6", acct, false)
	}
	if w, why := l.Login("6.6.6.6", "d"); w != time.Minute || !strings.Contains(why, "3 failed logins from 6.6.6.6") {
		t.Fatalf("address backoff: %s %q", w, why)
	}
	c.add(time.Minute)
	// the rate: 4 in the burst were taken by 3 attempts (refused ones take none); one more, then 6 s apart
	if w, _ := l.Login("6.6.6.6", "d"); w != 0 {
		t.Fatal("4th attempt refused")
	}
	l.LoginDone("6.6.6.6", "d", true)
	c.add(time.Second) // the burst (4) refilled, less the attempt just made: three more, then the rate
	for i := 0; i < 3; i++ {
		if w, _ := l.Login("6.6.6.6", "d"); w != 0 {
			t.Fatalf("burst attempt %d refused", i)
		}
	}
	w, why := l.Login("6.6.6.6", "d")
	if w <= 0 || w > 6*time.Second || !strings.Contains(why, "logins from 6.6.6.6 over") {
		t.Fatalf("over the rate: %s %q", w, why)
	}
	if r := l.TakeReport(); !strings.Contains(r, "login 2 (1 addresses)") {
		t.Fatalf("report %q", r)
	}
}

// Blaze requests per connection: the burst passes at once, then requests wait 1/rate each; a client that slows down
// is let off, one whose every request has to wait for longer than RequestHeld is to be closed.
func TestRequestGate(t *testing.T) {
	cfg := Default()
	cfg.Requests = Rate{PerSec: 10, Burst: 5}
	cfg.RequestHeld = time.Second
	l, c := testLimiter(cfg)
	g := l.RequestGate()
	for i := 0; i < 5; i++ {
		if w, ok := g.Next(); w != 0 || !ok {
			t.Fatalf("burst request %d: %s %v", i, w, ok)
		}
	}
	if w, ok := g.Next(); w != 100*time.Millisecond || !ok {
		t.Fatalf("6th: %s %v", w, ok)
	}
	// a client that keeps coming at once: each request waits 100 ms (the caller sleeps), for a second, then closed
	ok := true
	n := 0
	for ; ok && n < 100; n++ {
		var w time.Duration
		c.add(100 * time.Millisecond)
		if w, ok = g.Next(); ok && w != 100*time.Millisecond {
			t.Fatalf("request %d waits %s", n, w)
		}
	}
	if ok || n != 11 {
		t.Fatalf("a flood: closed %v after %d more requests", !ok, n)
	}
	if r := l.TakeReport(); r != "refused: requests 1" {
		t.Fatalf("report %q", r)
	}
	// a client that pauses is let off
	g = l.RequestGate()
	for i := 0; i < 6; i++ {
		g.Next()
	}
	c.add(900 * time.Millisecond)
	for i := 0; i < 5; i++ {
		c.add(150 * time.Millisecond)
		if _, ok := g.Next(); !ok {
			t.Fatal("a client slower than the rate was closed")
		}
	}
	if w, ok := l.RequestGate().Next(); w != 0 || !ok {
		t.Fatal("a new connection's gate isn't fresh")
	}
}

func TestGamesAndQueues(t *testing.T) {
	cfg := Default()
	cfg.Games = Rate{PerSec: 1.0 / 20, Burst: 2}
	cfg.GamesAtOnce = 2
	cfg.Queues = Rate{PerSec: 1.0 / 10, Burst: 2}
	l, c := testLimiter(cfg)
	if l.StartGame(7, 0) != nil || l.StartGame(7, 1) != nil {
		t.Fatal("two games refused")
	}
	if err := l.StartGame(7, 2); !errors.Is(err, ErrGamesAtOnce) {
		t.Fatalf("a third running game: %v", err)
	}
	if err := l.StartGame(7, 0); !errors.Is(err, ErrGameRate) {
		t.Fatalf("over the rate: %v", err)
	}
	if l.StartGame(8, 0) != nil {
		t.Fatal("another user refused")
	}
	c.add(20 * time.Second)
	if l.StartGame(7, 0) != nil {
		t.Fatal("20 s later refused")
	}
	if l.JoinQueue(7) != nil || l.JoinQueue(7) != nil {
		t.Fatal("queues refused")
	}
	if err := l.JoinQueue(7); !errors.Is(err, ErrQueueRate) {
		t.Fatalf("3rd queue: %v", err)
	}
	if r := l.TakeReport(); r != "refused: game 2 (1 users), queue 1 (1 users)" {
		t.Fatalf("report %q", r)
	}
}

func TestShouldLogOncePerMinute(t *testing.T) {
	l, c := testLimiter(Default())
	if !l.ShouldLog("x", "a") || l.ShouldLog("x", "a") || !l.ShouldLog("x", "b") || !l.ShouldLog("y", "a") {
		t.Fatal("first per key and kind")
	}
	c.add(61 * time.Second)
	if !l.ShouldLog("x", "a") {
		t.Fatal("a minute later")
	}
}

// A flood from many addresses: the tables stay bounded.
func TestPruneBounded(t *testing.T) {
	cfg := Default()
	cfg.MaxKeys = 100
	l, c := testLimiter(cfg)
	for i := 0; i < 1000; i++ {
		l.AllowPacket(net.IPv4(10, 0, byte(i>>8), byte(i)).String(), 10)
		c.add(time.Millisecond)
	}
	if len(l.packets) > 101 {
		t.Fatalf("%d packet buckets", len(l.packets))
	}
	c.add(time.Hour)
	l.AllowPacket("z", 1)
	if len(l.packets) != 1 || len(l.bytes) != 1 {
		t.Fatalf("idle buckets kept: %d/%d", len(l.packets), len(l.bytes))
	}
}

// The listener: a refused connection is closed by the server at once, an admitted one's Close frees its slot.
func TestListener(t *testing.T) {
	cfg := Default()
	cfg.OpenPerAddr = 1
	l, _ := testLimiter(cfg)
	inner, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	ln := l.Listener(inner, "blaze", func(f string, a ...any) { lines = append(lines, f) })
	defer ln.Close()
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	c1, err := net.Dial("tcp4", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	s1 := <-accepted
	c2, err := net.Dial("tcp4", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	c2.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c2.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("the second connection wasn't closed by the server: %v", err)
	}
	s1.Close()
	c3, err := net.Dial("tcp4", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	select {
	case s3 := <-accepted:
		s3.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("after the first closed, the third wasn't accepted")
	}
	if len(lines) != 1 {
		t.Fatalf("log lines %v", lines)
	}
	if l.Open("127.0.0.1") != 0 {
		t.Fatalf("open %d", l.Open("127.0.0.1"))
	}
	var nl *Limiter
	if nl.Listener(inner, "x", nil) != inner {
		t.Fatal("a nil limiter wraps")
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// The server's own address (the tunnel gateway's source) is exempt from the per-address limits, not from an
// account's backoff.
func TestExempt(t *testing.T) {
	cfg := Default()
	cfg.OpenPerAddr, cfg.Accept = 1, Rate{PerSec: 1, Burst: 1}
	cfg.Packets = Rate{PerSec: 1, Burst: 1}
	cfg.Logins = Rate{PerSec: 1.0 / 60, Burst: 1}
	cfg.AccountFail = Backoff{Free: 1, Base: time.Minute, Max: time.Minute}
	cfg.AddrFail = Backoff{Free: 1, Base: time.Minute, Max: time.Minute}
	l, _ := testLimiter(cfg)
	l.Exempt("10.1.1.1", "")
	for i := 0; i < 5; i++ {
		if _, ok, _ := l.Admit("10.1.1.1"); !ok || !l.AllowPacket("10.1.1.1", 100) {
			t.Fatal("an exempt address was limited")
		}
		if w, why := l.Login("10.1.1.1", "x"+string(rune('a'+i))); w != 0 {
			t.Fatalf("exempt login %d: %s", i, why)
		}
		l.LoginDone("10.1.1.1", "x"+string(rune('a'+i)), false)
	}
	l.LoginDone("10.1.1.1", "victim", false)
	l.LoginDone("10.1.1.1", "victim", false)
	if w, _ := l.Login("10.1.1.1", "victim"); w == 0 {
		t.Fatal("an account's backoff doesn't apply through an exempt address")
	}
	if _, ok, _ := l.Admit("10.1.1.2"); !ok {
		t.Fatal("first")
	}
	if _, ok, _ := l.Admit("10.1.1.2"); ok {
		t.Fatal("another address isn't exempt")
	}
	var nl *Limiter
	nl.Exempt("x")
}
