package tunnel

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// waitCount waits until f gives at least want; false when it doesn't within 3 s.
func waitCount(f func() uint64, want uint64) bool {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		if f() >= want {
			return true
		}
	}
	return false
}

// gatewaySession is the gateway's only session.
func gatewaySession(t *testing.T, g *Gateway) *session {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.sessions) != 1 {
		t.Fatalf("%d sessions", len(g.sessions))
	}
	for _, s := range g.sessions {
		return s
	}
	return nil
}

// A hostile server names a local socket of another program as a datagram's destination: nothing reaches it until that
// socket has itself sent to that port through the tunnel. In both of the client's modes, since the front's goes through
// Deliver and a plain tunnel's through its own listener.
func TestClientDropsUnspokenDestination(t *testing.T) {
	for _, tc := range []struct {
		name     string
		external bool
	}{{"listeners", false}, {"external", true}} {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := startGateway(t, io.Discard)
			delivered := make(chan string, 4)
			c := &Client{Log: logging.New(io.Discard), Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tunIP),
				DialIP: net.ParseIP(peerIP), External: tc.external}
			if tc.external {
				c.Deliver = func(port int, dst *net.UDPAddr, payload []byte) { delivered <- dst.String() + " " + string(payload) }
			}
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(c.Close)
			if !c.WaitUDP(3 * time.Second) { // the keepalive isn't gated
				t.Fatal("no keepalive echo")
			}
			s := gatewaySession(t, g)

			svc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()
			addr := svc.LocalAddr().(*net.UDPAddr)
			hostile := Inner{Kind: KindData, Port: portQoS, IP: [4]byte{127, 0, 0, 1}, ClientPort: uint16(addr.Port)}

			if err := s.send(hostile, []byte("query")); err != nil {
				t.Fatal(err)
			}
			if !waitCount(c.dropNotSpoke.Load, 1) {
				t.Fatal("the datagram wasn't dropped")
			}
			svc.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			if n, from, err := svc.ReadFromUDP(make([]byte, 64)); err == nil {
				t.Errorf("the local service got %d bytes from %s", n, from)
			}
			if len(delivered) != 0 {
				t.Errorf("delivered: %s", <-delivered)
			}
			if c.downN.Load() != 0 {
				t.Errorf("%d datagrams counted as delivered", c.downN.Load())
			}
			if !strings.Contains(c.Counts(), "not spoken 1") {
				t.Errorf("counts: %s", c.Counts())
			}

			// what reaches the socket next, in either mode
			next := func() string {
				t.Helper()
				if tc.external {
					select {
					case d := <-delivered:
						return d
					case <-time.After(3 * time.Second):
						t.Fatal("nothing delivered")
					}
				}
				svc.SetReadDeadline(time.Now().Add(3 * time.Second))
				b := make([]byte, 256)
				n, _, err := svc.ReadFromUDP(b)
				if err != nil {
					t.Fatal(err)
				}
				return string(b[:n])
			}

			// a socket that has spoken to a port hears that port's answer (the stand-in match host's echo), and still
			// counts as not having spoken to any other port
			if !c.SendDatagram(portHost, addr, []byte("hi")) {
				t.Fatal("SendDatagram")
			}
			if got := next(); !strings.HasSuffix(got, "hi") {
				t.Errorf("the host's echo: %q", got)
			}
			if err := s.send(hostile, []byte("query")); err != nil {
				t.Fatal(err)
			}
			if !waitCount(c.dropNotSpoke.Load, 2) {
				t.Fatal("a socket that spoke to another port was reached")
			}

			// and once it has spoken to this one, this one's: what the game's sockets do
			if !c.SendDatagram(portQoS, addr, []byte("probe")) {
				t.Fatal("SendDatagram")
			}
			if got := next(); !strings.Contains(got, "answer:probe") {
				t.Errorf("the answer: %q", got)
			}
		})
	}
}

// The record of who spoke is bounded, and a socket still in use is back with its next datagram.
func TestSpokeBounded(t *testing.T) {
	c := &Client{}
	first := Inner{Port: 1, IP: [4]byte{127, 0, 0, 1}, ClientPort: 1}
	c.noteSpoke(first)
	for i := 2; i <= maxSpoke; i++ {
		c.noteSpoke(Inner{Port: 1, IP: [4]byte{127, 0, 0, 1}, ClientPort: uint16(i)})
	}
	if !c.hasSpoken(first) || len(c.spoke) != maxSpoke {
		t.Fatalf("full: %d entries, first there: %v", len(c.spoke), c.hasSpoken(first))
	}
	c.noteSpoke(first) // already there: nothing changes
	if len(c.spoke) != maxSpoke {
		t.Errorf("%d entries after a known socket", len(c.spoke))
	}
	c.noteSpoke(Inner{Port: 2, IP: [4]byte{127, 0, 0, 1}, ClientPort: 1})
	if len(c.spoke) != 1 || c.hasSpoken(first) {
		t.Errorf("past the bound: %d entries, first there: %v", len(c.spoke), c.hasSpoken(first))
	}
	c.noteSpoke(first)
	if !c.hasSpoken(first) {
		t.Error("a socket in use isn't back with its next datagram")
	}
}
