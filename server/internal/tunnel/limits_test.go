package tunnel

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/limits"
	"dawngate/server/internal/logging"
)

// the gateway limits by each player's real address (the source of its TCP connections and UDP datagrams),
// with dgserver's limiter. The test players: A's tunnel on tunIP dialling from peerIP (as in the other tests),
// B's on tunIPB dialling from peerIPB, so the gateway sees two real addresses.
const (
	tunIPB  = "127.0.9.24"
	peerIPB = "127.0.9.23"
	tunIPA2 = "127.0.9.25" // a second tunnel from A's real address
)

// offLimits is the public profile with every rate and count off (zero = unlimited), for one rule at a time.
func offLimits() limits.Config {
	c := limits.Default()
	c.Accept, c.Packets, c.Bytes, c.Logins, c.Requests, c.Games, c.Queues = limits.Rate{}, limits.Rate{}, limits.Rate{},
		limits.Rate{}, limits.Rate{}, limits.Rate{}, limits.Rate{}
	c.TunnelPackets, c.TunnelBytes = limits.Rate{}, limits.Rate{}
	c.OpenPerAddr, c.GamesAtOnce, c.TunnelSessions = 0, 0, 0
	return c
}

// startLimitedGateway is startGateway with lim and a session cap, set before Start.
func startLimitedGateway(t *testing.T, logw io.Writer, lim *limits.Limiter, maxSessions int) (*Gateway, *udpEcho) {
	t.Helper()
	id, _, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	echo := newUDPEcho(t, gwIP)
	g := &Gateway{Log: logging.New(logw), Identity: id, BindIP: net.ParseIP(gwIP), MaxSessionsPerPeer: maxSessions,
		Forward: func(port int) *net.UDPAddr { return echo.addr() }}
	if lim != nil {
		g.Limits = lim
	}
	ln := g.Listener(portTCP)
	g.ForwardUDP(portHost)
	if err := g.Start(net.JoinHostPort(gwIP, "0")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				b, _ := io.ReadAll(c)
				fmt.Fprintf(c, "%s|%s", b, c.RemoteAddr().(*Addr).Peer)
			}()
		}
	}()
	return g, echo
}

// startClientFrom is startClient with the player's tunnel address and real (dial) address.
func startClientFrom(t *testing.T, g *Gateway, tun, peer string, logw io.Writer) (*Client, error) {
	t.Helper()
	c := &Client{Log: logging.New(logw), Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tun), DialIP: net.ParseIP(peer)}
	if err := c.Start(); err != nil {
		return nil, err
	}
	t.Cleanup(c.Close)
	return c, nil
}

// streamFrom opens a stream through the tunnel on tun to portTCP and returns the echo ("" when the stream failed).
func streamFrom(t *testing.T, tun, msg string) string {
	t.Helper()
	conn, err := net.DialTCP("tcp4", &net.TCPAddr{IP: net.ParseIP(tun)}, &net.TCPAddr{IP: net.ParseIP(tun), Port: portTCP})
	if err != nil {
		return ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte(msg))
	conn.CloseWrite()
	b, _ := io.ReadAll(conn)
	return string(b)
}

// Connections: every TCP connection to the gateway (control connections and streams) passes the address's new-
// connection rate before its TLS handshake; a flood from one real address is refused, another player's streams go on,
// and the in-process service sees each player's own real address.
func TestTunnelLimitConnections(t *testing.T) {
	cfg := offLimits()
	cfg.Accept = limits.Rate{PerSec: 0.001, Burst: 6}
	lim := limits.New(cfg, nil)
	buf := &syncBuf{}
	g, _ := startLimitedGateway(t, buf, lim, 0)
	if _, err := startClientFrom(t, g, tunIP, peerIP, buf); err != nil {
		t.Fatal(err)
	}
	ok, refused := 0, 0
	for i := 0; i < 12; i++ {
		if got := streamFrom(t, tunIP, "a"); got == "a|"+peerIP {
			ok++
		} else {
			refused++
		}
	}
	if ok == 0 || refused == 0 || ok > 6 {
		t.Fatalf("A's streams: %d served, %d refused; want a few served, then refused", ok, refused)
	}
	// B, another real address, has a bucket of its own
	if _, err := startClientFrom(t, g, tunIPB, peerIPB, buf); err != nil {
		t.Fatal(err)
	}
	if got := streamFrom(t, tunIPB, "b"); got != "b|"+peerIPB {
		t.Errorf("B's stream after A's flood: %q, want b|%s", got, peerIPB)
	}
	// a raw connection from A's address is reset before any TLS: no handshake answer
	raw, err := (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(peerIP)}}).Dial("tcp4", g.Addr())
	if err == nil {
		raw.SetDeadline(time.Now().Add(3 * time.Second))
		raw.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x05, 1, 0, 0, 1, 0})
		if n, _ := raw.Read(make([]byte, 64)); n > 0 {
			t.Errorf("a refused connection got %d bytes back (a handshake answer?)", n)
		}
		raw.Close()
	}
	log := buf.String()
	if !strings.Contains(log, "limits: tunnel connection from "+peerIP+" refused: new connections from "+peerIP+" over") {
		t.Errorf("log lacks the refusal:\n%s", log)
	}
	if strings.Contains(log, "connection from "+peerIPB+" refused") || strings.Contains(log, "connection from "+gwIP+" refused") {
		t.Errorf("another address was refused:\n%s", log)
	}
	if r := lim.TakeReport(); !strings.HasPrefix(r, "refused: accept ") || !strings.HasSuffix(r, "(1 addresses)") {
		t.Errorf("report %q", r)
	}
}

// Open connections: a player's long-lived streams hold its address's slots until they close.
func TestTunnelLimitOpenConnections(t *testing.T) {
	cfg := offLimits()
	cfg.OpenPerAddr = 3 // the control connection and two streams
	lim := limits.New(cfg, nil)
	buf := &syncBuf{}
	g, _ := startLimitedGateway(t, buf, lim, 0)
	if _, err := startClientFrom(t, g, tunIP, peerIP, buf); err != nil {
		t.Fatal(err)
	}
	var held []net.Conn
	for i := 0; i < 2; i++ { // two streams kept open (the echo answers at EOF only)
		c, err := net.DialTCP("tcp4", &net.TCPAddr{IP: net.ParseIP(tunIP)}, &net.TCPAddr{IP: net.ParseIP(tunIP), Port: portTCP})
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("x"))
		held = append(held, c)
	}
	deadline := time.Now().Add(5 * time.Second)
	for lim.Open(peerIP) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := lim.Open(peerIP); n != 3 {
		t.Fatalf("open connections of %s: %d, want 3", peerIP, n)
	}
	if got := streamFrom(t, tunIP, "third"); got != "" {
		t.Errorf("a third stream was served: %q", got)
	}
	held[0].Close()
	deadline = time.Now().Add(5 * time.Second)
	for lim.Open(peerIP) > 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := streamFrom(t, tunIP, "again"); got != "again|"+peerIP {
		t.Errorf("after a stream closed: %q", got)
	}
	held[1].Close()
}

// Datagrams: the channel's datagrams pass the sender's datagram rate before the session lookup and the decryption; a
// flood from one real address is cut to the rate, another player's datagrams all pass.
func TestTunnelLimitDatagrams(t *testing.T) {
	cfg := offLimits()
	cfg.TunnelPackets = limits.Rate{PerSec: 0.001, Burst: 30}
	lim := limits.New(cfg, nil)
	buf := &syncBuf{}
	g, echo := startLimitedGateway(t, buf, lim, 0)
	a, err := startClientFrom(t, g, tunIP, peerIP, buf)
	if err != nil {
		t.Fatal(err)
	}
	if !a.WaitUDP(3 * time.Second) {
		t.Fatal("A: no UDP echo")
	}
	ga := gameUDP(t)
	for i := 0; i < 100; i++ {
		ga.WriteToUDP([]byte(fmt.Sprintf("a%03d", i)), &net.UDPAddr{IP: net.ParseIP(tunIP), Port: portHost})
	}
	time.Sleep(300 * time.Millisecond)
	gotA, _ := echo.counts()
	if gotA == 0 || gotA >= 30 {
		t.Errorf("the host got %d of A's 100 datagrams; want fewer than the burst of 30 (keepalives take some)", gotA)
	}
	if g.limited.Load() == 0 {
		t.Error("no datagram counted as limited")
	}
	b, err := startClientFrom(t, g, tunIPB, peerIPB, buf)
	if err != nil {
		t.Fatal(err)
	}
	if !b.WaitUDP(3 * time.Second) {
		t.Fatal("B: no UDP echo after A's flood")
	}
	gb, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(tunIPB)})
	if err != nil {
		t.Fatal(err)
	}
	defer gb.Close()
	for i := 0; i < 10; i++ {
		gb.WriteToUDP([]byte(fmt.Sprintf("b%03d", i)), &net.UDPAddr{IP: net.ParseIP(tunIPB), Port: portHost})
	}
	time.Sleep(300 * time.Millisecond)
	if got, _ := echo.counts(); got-gotA != 10 {
		t.Errorf("the host got %d of B's 10 datagrams", got-gotA)
	}
	log := buf.String()
	if !strings.Contains(log, "limits: udp from "+peerIP+" over its rate; dropped") || strings.Contains(log, "udp from "+peerIPB) {
		t.Errorf("log:\n%s", log)
	}
	if rep := strings.Join(g.Report(), "\n"); !strings.Contains(rep, "refused by the limits (PH6b): datagrams ") {
		t.Errorf("report:\n%s", rep)
	}
}

// Sessions: one real address holds at most MaxSessionsPerPeer sessions; another address isn't affected, and a session
// taken up again (the resume after a lost control connection) is no new one.
func TestTunnelLimitSessions(t *testing.T) {
	buf := &syncBuf{}
	g, _ := startLimitedGateway(t, buf, nil, 1)
	a, err := startClientFrom(t, g, tunIP, peerIP, buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := startClientFrom(t, g, tunIPA2, peerIP, buf); err == nil {
		t.Error("a second session from A's address was accepted")
	} else if !strings.Contains(err.Error(), StatusText(StatusUnavailable)) {
		t.Errorf("second session: %v", err)
	}
	if _, err := startClientFrom(t, g, tunIPB, peerIPB, buf); err != nil {
		t.Errorf("B's session: %v", err)
	}
	if got := streamFrom(t, tunIP, "a"); got != "a|"+peerIP {
		t.Errorf("A's stream: %q", got)
	}
	s := a.session(time.Second)
	if s == nil {
		t.Fatal("A has no session")
	}
	g.mu.Lock()
	gs := g.sessions[s.id]
	g.mu.Unlock()
	gs.mu.Lock()
	ctl := gs.control
	gs.mu.Unlock()
	ctl.Close() // the control connection lost: A connects again and takes its session up
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(buf.String(), "(taken up again") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(buf.String(), "(taken up again") {
		t.Errorf("A's session wasn't taken up again:\n%s", buf.String())
	}
	if g.sessionsCut.Load() != 1 {
		t.Errorf("sessions refused %d, want 1", g.sessionsCut.Load())
	}
	if !strings.Contains(buf.String(), "limits: session from ('"+peerIP+"'") {
		t.Errorf("log lacks the session refusal:\n%s", buf.String())
	}
}
