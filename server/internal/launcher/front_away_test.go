package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/tunnel"
)

// The away test's addresses: the front (the player's PC), the main server's gateway and its match host, and two match
// gateways of a host machine with the host of match 7 behind them.
const (
	awayFront  = "127.0.9.140"
	awayMain   = "127.0.9.141"
	awayGwA    = "127.0.9.142"
	awayHostA  = "127.0.9.143"
	awayGwB    = "127.0.9.144"
	awayMatch  = 23510 // the match's GMS port, the same number on both machines
	awayQoS    = 23511 // the main server's in-process port (the test's way to the session's Addr)
	awayOther  = 23512 // further match ports of the main server
	awayPorts7 = 23516
)

// prefixEcho is a match host that answers every datagram with prefix + the datagram, and counts them.
type prefixEcho struct {
	conn *net.UDPConn
	mu   sync.Mutex
	n    int
	from netip.AddrPort // the last datagram's source: a gateway's flow
}

func newPrefixEcho(t *testing.T, addr, prefix string) *prefixEcho {
	t.Helper()
	ua, _ := net.ResolveUDPAddr("udp4", addr)
	conn, err := net.ListenUDP("udp4", ua)
	if err != nil {
		t.Fatal(err)
	}
	e := &prefixEcho{conn: conn}
	go func() {
		buf := make([]byte, 65536)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			e.mu.Lock()
			e.n++
			e.from = from.AddrPort()
			e.mu.Unlock()
			conn.WriteToUDP(append([]byte(prefix), buf[:n]...), from)
		}
	}()
	t.Cleanup(func() { conn.Close() })
	return e
}

func (e *prefixEcho) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.n
}

func (e *prefixEcho) source() netip.AddrPort {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.from
}

func (e *prefixEcho) addr() *net.UDPAddr { return e.conn.LocalAddr().(*net.UDPAddr) }

// matchGateway is a host machine's match gateway on ip whose match 7 runs at host (its port is the match's port).
func matchGateway(t *testing.T, ip string, guard *tunnel.TicketGuard, host *net.UDPAddr) *tunnel.Gateway {
	t.Helper()
	id, _, err := tunnel.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := &tunnel.Gateway{Log: logging.New(io.Discard), Identity: id, BindIP: net.ParseIP(ip),
		Admit:   tunnel.TicketAdmit(guard, func(m int64) (*net.UDPAddr, bool) { return host, m == 7 }),
		Running: func(m int64) (*net.UDPAddr, bool) { return host, m == 7 }}
	g.ForwardUDP(awayMatch)
	if err := g.Start(net.JoinHostPort(ip, "0")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return g
}

// TestFrontAway: the main server sends the match port to a host machine's match gateway; the front opens a session
// there and carries that port's datagrams through it (answers delivered, the main server's host sees none), a second
// login's ticket joins it, a wrong key, a port not listed, a private or loopback address from a public server and a
// fifth session are refused, and home, idleness and the main session's end close it.
func TestFrontAway(t *testing.T) {
	key := make([]byte, 32)
	key[0] = 7
	guard := &tunnel.TicketGuard{Machine: "na1"}
	guard.SetKey(key, time.Now())
	nonce := byte(0)
	ticket := func(user int64) []byte {
		nonce++
		tk := tunnel.Ticket{Machine: "na1", Match: 7, User: user, Issued: time.Now()}
		tk.Nonce[0] = nonce
		b, err := tunnel.MintTicket(key, tk)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	mainHost := newPrefixEcho(t, awayMain+":0", "main:")
	hostA := newPrefixEcho(t, net.JoinHostPort(awayHostA, "23510"), "A:")
	gwA := matchGateway(t, awayGwA, guard, hostA.addr())
	gwB := matchGateway(t, awayGwB, guard, hostA.addr()) // a gateway with another key

	// the main server's gateway: the match ports routed by Blaze only, and an in-process port that hands the test the
	// session's address (what Blaze's ForClient has)
	id, _, err := tunnel.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	addrs := make(chan *tunnel.Addr, 8)
	main := &tunnel.Gateway{Log: logging.New(io.Discard), Identity: id, BindIP: net.ParseIP(awayMain)}
	for p := awayMatch; p <= awayPorts7; p++ {
		if p != awayQoS {
			main.ForwardUDP(p)
		}
	}
	main.HandleUDP(awayQoS, func(_ []byte, from *tunnel.Addr, reply func([]byte) error) {
		addrs <- from
		reply([]byte("qos"))
	})
	if err := main.Start(net.JoinHostPort(awayMain, "0")); err != nil {
		t.Fatal(err)
	}
	defer main.Close()

	logBuf := &syncBuffer{}
	ip := net.ParseIP(awayFront).To4()
	f := &Front{Log: logging.New(logBuf), TunnelIP: ip, Ports: DefaultFrontPorts, done: make(chan struct{}),
		tcpLns: map[int]net.Listener{}, udpLns: map[int]*net.UDPConn{}, conns: map[net.Conn]struct{}{},
		ConnectTimeout: 3 * time.Second, AwayIdle: time.Second}
	s := &frontSession{b: Bookmark{Tag: "main", Address: awayMain}}
	s.c = &tunnel.Client{Log: f.Log, Gateway: main.Addr(), Pin: main.Identity.Pin, TunnelIP: ip, External: true,
		DialTimeout: 3 * time.Second}
	s.c.Deliver = f.deliverFor(s)
	f.hookAway(s)
	if err := s.c.Start(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.cur = s
	f.mu.Unlock()
	f.ensureListeners(s)
	defer f.Close()

	game, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	send := func(port int, msg string, wait time.Duration) (string, error) {
		t.Helper()
		game.WriteToUDP([]byte(msg), &net.UDPAddr{IP: ip, Port: port})
		game.SetReadDeadline(time.Now().Add(wait))
		buf := make([]byte, 2048)
		n, _, err := game.ReadFromUDP(buf)
		return string(buf[:n]), err
	}
	if got, err := send(awayQoS, "who", 2*time.Second); err != nil || got != "qos" {
		t.Fatalf("the main session's in-process port: %q %v", got, err)
	}
	addr := <-addrs
	if !addr.TakesAway() {
		t.Fatal("the front's session doesn't take away messages")
	}
	addr.Route(awayMatch, mainHost.addr())
	if got, err := send(awayMatch, "m1", 2*time.Second); err != nil || got != "main:m1" {
		t.Fatalf("the match on the main server: %q %v", got, err)
	}
	waitAway := func(want []int) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !slices.Equal(s.awayPorts(), want); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("away ports %v, want %v; log:\n%s", s.awayPorts(), want, logBuf.String())
			}
		}
	}
	waitLog := func(wants ...string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			text, all := logBuf.String(), true
			for _, w := range wants {
				all = all && strings.Contains(text, w)
			}
			if all {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the log lacks %q:\n%s", wants, text)
			}
		}
	}
	gwAAddr := netip.MustParseAddrPort(gwA.Addr())

	// away: the match port's datagrams go to the host machine, the answers come back from the same port
	if !addr.RouteAway(awayMatch, tunnel.Away{Gateway: gwAAddr, Pin: gwA.Identity.Pin, Ticket: ticket(2000000001)}) {
		t.Fatal("RouteAway didn't send")
	}
	waitAway([]int{awayMatch})
	before := mainHost.count()
	for i := range 3 {
		msg := "a" + string(rune('0'+i))
		got, err := send(awayMatch, msg, 2*time.Second)
		if err != nil || got != "A:"+msg {
			t.Fatalf("through the away session: %q %v", got, err)
		}
	}
	if mainHost.count() != before {
		t.Errorf("the main server's host got %d datagrams while the port was away", mainHost.count()-before)
	}
	if got, err := send(awayQoS, "who", 2*time.Second); err != nil || got != "qos" {
		t.Errorf("the main session's other port while away: %q %v", got, err)
	}
	<-addrs

	// a second login's ticket for the same match joins the session
	if !addr.RouteAway(awayMatch, tunnel.Away{Gateway: gwAAddr, Pin: gwA.Identity.Pin, Ticket: ticket(2000000002)}) {
		t.Fatal("the second RouteAway didn't send")
	}
	waitLog("another login's ticket joined the session to " + gwA.Addr())
	var flowUsers []int64
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && len(flowUsers) < 2; time.Sleep(10 * time.Millisecond) {
		_, flowUsers, _ = gwA.FlowAdmission(hostA.source()) // what the agent's relay asks for a host's verifyHello
	}
	if !slices.Equal(flowUsers, []int64{2000000001, 2000000002}) {
		t.Errorf("the match gateway's users %v", flowUsers)
	}

	// refusals: a wrong key, a port the main session doesn't list, addresses into the player's network, a fifth session
	addr.RouteAway(awayOther, tunnel.Away{Gateway: netip.MustParseAddrPort(gwB.Addr()), Pin: gwA.Identity.Pin, Ticket: ticket(2000000001)})
	waitLog("away: port 23512 to " + gwB.Addr() + " ended: didn't start: the gateway's key is not the one the server named")
	public := netip.MustParseAddr("203.0.113.5")
	f.takeAway(s, public, awayOther, tunnel.Away{Gateway: netip.MustParseAddrPort("192.168.1.5:42128"), Ticket: ticket(1)})
	f.takeAway(s, public, awayOther, tunnel.Away{Gateway: netip.MustParseAddrPort("127.0.0.1:42128"), Ticket: ticket(1)})
	f.takeAway(s, s.c.GatewayIP(), 23999, tunnel.Away{Gateway: gwAAddr, Ticket: ticket(1)})
	waitLog("away: port 23512 to 192.168.1.5:42128 refused: 192.168.1.5 is in a private network, and the server isn't",
		"away: port 23512 to 127.0.0.1:42128 refused: 127.0.0.1 is a loopback address, and the server isn't on this PC",
		"away: port 23999 to "+gwA.Addr()+" refused: the server's session doesn't list that port")
	s.awayMu.Lock()
	for p := awayOther; p < awayOther+3; p++ {
		s.away[p] = &awaySession{port: p, done: make(chan struct{})}
	}
	s.awayMu.Unlock()
	f.takeAway(s, s.c.GatewayIP(), awayPorts7, tunnel.Away{Gateway: gwAAddr, Pin: gwA.Identity.Pin, Ticket: ticket(1)})
	waitLog("away: port 23516 to " + gwA.Addr() + " refused: 4 away sessions are open already (at most 4)")
	s.awayMu.Lock()
	for p := awayOther; p < awayOther+3; p++ {
		delete(s.away, p)
	}
	s.awayMu.Unlock()
	waitAway([]int{awayMatch})

	// home: the port goes through the main session again
	addr.Route(awayMatch, mainHost.addr())
	waitLog("away: port 23510 to " + gwA.Addr() + " ended: the server sent it home")
	waitAway(nil)
	if got, err := send(awayMatch, "m2", 2*time.Second); err != nil || got != "main:m2" {
		t.Errorf("after home: %q %v", got, err)
	}

	// idle: no datagram for AwayIdle
	addr.RouteAway(awayMatch, tunnel.Away{Gateway: gwAAddr, Pin: gwA.Identity.Pin, Ticket: ticket(2000000003)})
	waitAway([]int{awayMatch})
	waitLog("ended: no datagram for 1s")
	waitAway(nil)

	// the main session ends (a switch, EndSession): its away sessions with it
	addr.RouteAway(awayMatch, tunnel.Away{Gateway: gwAAddr, Pin: gwA.Identity.Pin, Ticket: ticket(2000000004)})
	waitAway([]int{awayMatch})
	if !f.EndSession("main") {
		t.Fatal("EndSession")
	}
	waitLog("ended: the server's session ended")
	waitAway(nil)
}

// The front-less test's addresses (TestTunnelPathAway): the player's PC, the main server's gateway and its match host,
// a host machine's match gateway and its host of match 7.
const (
	tpaPC    = "127.0.9.160"
	tpaMain  = "127.0.9.161"
	tpaGw    = "127.0.9.162"
	tpaHost  = "127.0.9.163"
	tpaMatch = 23520
	tpaQoS   = 23521
)

// TestTunnelPathAway (PREF5): Start-Client's path without the front (startTunnel, picker = off) says the version 4
// hello and takes the main server's away message like the front: the match port's datagrams go through a session to the
// host machine's match gateway, the answers come back from the game's own port, the main server's host sees none, the
// session's other ports stay on the main session; home brings the port back, and closing ends an open away session.
func TestTunnelPathAway(t *testing.T) {
	key := make([]byte, 32)
	key[0] = 9
	guard := &tunnel.TicketGuard{Machine: "na1"}
	guard.SetKey(key, time.Now())
	nonce := byte(0)
	ticket := func(user int64) []byte {
		nonce++
		tk := tunnel.Ticket{Machine: "na1", Match: 7, User: user, Issued: time.Now()}
		tk.Nonce[0] = nonce
		b, err := tunnel.MintTicket(key, tk)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	mainHost := newPrefixEcho(t, tpaMain+":0", "main:")
	host := newPrefixEcho(t, net.JoinHostPort(tpaHost, strconv.Itoa(tpaMatch)), "A:")
	id, _, err := tunnel.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gwID, _, err := tunnel.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gw := &tunnel.Gateway{Log: logging.New(io.Discard), Identity: gwID, BindIP: net.ParseIP(tpaGw),
		Admit:   tunnel.TicketAdmit(guard, func(m int64) (*net.UDPAddr, bool) { return host.addr(), m == 7 }),
		Running: func(m int64) (*net.UDPAddr, bool) { return host.addr(), m == 7 }}
	gw.ForwardUDP(tpaMatch)
	if err := gw.Start(net.JoinHostPort(tpaGw, "0")); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	addrs := make(chan *tunnel.Addr, 8)
	main := &tunnel.Gateway{Log: logging.New(io.Discard), Identity: id, BindIP: net.ParseIP(tpaMain)}
	main.ForwardUDP(tpaMatch)
	main.HandleUDP(tpaQoS, func(_ []byte, from *tunnel.Addr, reply func([]byte) error) {
		addrs <- from
		reply([]byte("qos"))
	})
	if err := main.Start(net.JoinHostPort(tpaMain, "0")); err != nil {
		t.Fatal(err)
	}
	defer main.Close()

	l := setup.Layout{Root: t.TempDir()}
	var out bytes.Buffer
	_, stop, err := startTunnel(IO{Out: &out, ExeDir: t.TempDir()}, l, main.Addr(), main.Identity.Pin, tpaPC, nil)
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stop()
		}
	}()
	logText := func() string {
		names, _ := filepath.Glob(filepath.Join(l.Logs(), "tunnel-*.log"))
		var b strings.Builder
		for _, n := range names {
			raw, _ := os.ReadFile(n)
			b.Write(raw)
		}
		return b.String()
	}
	waitLog := func(want string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !strings.Contains(logText(), want); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the tunnel log lacks %q:\n%s", want, logText())
			}
		}
	}
	ip := net.ParseIP(tpaPC).To4()
	game, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	send := func(port int, msg string) (string, error) {
		t.Helper()
		game.WriteToUDP([]byte(msg), &net.UDPAddr{IP: ip, Port: port})
		game.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 2048)
		n, from, err := game.ReadFromUDP(buf)
		if err == nil && from.Port != port {
			return "", fmt.Errorf("the answer came from port %d, not %d", from.Port, port)
		}
		return string(buf[:n]), err
	}
	if got, err := send(tpaQoS, "who"); err != nil || got != "qos" {
		t.Fatalf("the main session's in-process port: %q %v", got, err)
	}
	addr := <-addrs
	if !addr.TakesAway() {
		t.Fatal("the front-less path's session doesn't say the version 4 hello")
	}
	addr.Route(tpaMatch, mainHost.addr())
	if got, err := send(tpaMatch, "m1"); err != nil || got != "main:m1" {
		t.Fatalf("the match on the main server: %q %v", got, err)
	}

	// away: the match port's datagrams go to the host machine, the answers come back from the same port
	if !addr.RouteAway(tpaMatch, tunnel.Away{Gateway: netip.MustParseAddrPort(gw.Addr()), Pin: gw.Identity.Pin, Ticket: ticket(2000000001)}) {
		t.Fatal("RouteAway didn't send")
	}
	waitLog(fmt.Sprintf("away: port %d through the match gateway %s", tpaMatch, gw.Addr()))
	before := mainHost.count()
	for i := range 3 {
		msg := "a" + string(rune('0'+i))
		if got, err := send(tpaMatch, msg); err != nil || got != "A:"+msg {
			t.Fatalf("through the away session: %q %v", got, err)
		}
	}
	if mainHost.count() != before {
		t.Errorf("the main server's host got %d datagrams while the port was away", mainHost.count()-before)
	}
	if got, err := send(tpaQoS, "who"); err != nil || got != "qos" {
		t.Errorf("the main session's other port while away: %q %v", got, err)
	}
	<-addrs

	// home: the port goes through the main session again
	addr.Route(tpaMatch, mainHost.addr())
	waitLog(fmt.Sprintf("away: port %d to %s ended: the server sent it home", tpaMatch, gw.Addr()))
	if got, err := send(tpaMatch, "m2"); err != nil || got != "main:m2" {
		t.Errorf("after home: %q %v", got, err)
	}

	// away again, then Start-Client closes: the away session ends with it
	addr.RouteAway(tpaMatch, tunnel.Away{Gateway: netip.MustParseAddrPort(gw.Addr()), Pin: gw.Identity.Pin, Ticket: ticket(2000000002)})
	for deadline := time.Now().Add(5 * time.Second); strings.Count(logText(), "through the match gateway") < 2; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the second away session didn't open:\n%s", logText())
		}
	}
	stop()
	stopped = true
	waitLog(fmt.Sprintf("away: port %d to %s ended: Start-Client closes", tpaMatch, gw.Addr()))
}

// TestAwayAddrAllowed is the address rule on its own.
func TestAwayAddrAllowed(t *testing.T) {
	pub, lan, lo := netip.MustParseAddr("203.0.113.5"), netip.MustParseAddr("192.168.1.2"), netip.MustParseAddr("127.0.0.2")
	for _, c := range []struct {
		main, away netip.Addr
		ok         bool
	}{
		{pub, netip.MustParseAddr("198.51.100.7"), true},
		{pub, netip.MustParseAddr("10.0.0.5"), false},
		{pub, netip.MustParseAddr("127.0.0.1"), false},
		{pub, netip.MustParseAddr("169.254.1.1"), false},
		{pub, netip.MustParseAddr("224.0.0.1"), false},
		{pub, netip.MustParseAddr("255.255.255.255"), false},
		{pub, netip.MustParseAddr("0.0.0.0"), false},
		{pub, netip.MustParseAddr("0.1.2.3"), false},       // 0.0.0.0/8
		{pub, netip.MustParseAddr("100.64.0.1"), false},    // carrier-grade NAT: the player's provider's network
		{pub, netip.MustParseAddr("100.127.255.1"), false}, // its last /16
		{pub, netip.MustParseAddr("100.128.0.1"), true},    // past it
		{pub, netip.MustParseAddr("240.0.0.1"), false},     // reserved
		{netip.MustParseAddr("100.100.1.1"), netip.MustParseAddr("100.64.0.9"), true},
		{pub, netip.MustParseAddr("2001:db8::1"), false},
		{lan, netip.MustParseAddr("192.168.1.9"), true},
		{lan, netip.MustParseAddr("198.51.100.7"), true},
		{lan, netip.MustParseAddr("127.0.0.1"), false},
		{lo, netip.MustParseAddr("127.0.2.20"), true},
		{lo, netip.MustParseAddr("192.168.1.9"), false},
	} {
		if err := awayAddrAllowed(c.main, c.away); (err == nil) != c.ok {
			t.Errorf("main %v, away %v: %v", c.main, c.away, err)
		}
	}
}
