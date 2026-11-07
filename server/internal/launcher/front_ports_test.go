package launcher

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/tunnel"
)

// portsGateway is a gateway on a test address that lists what set makes it list.
func portsGateway(t *testing.T, set func(*tunnel.Gateway)) *tunnel.Gateway {
	t.Helper()
	id, _, err := tunnel.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := &tunnel.Gateway{Log: logging.New(io.Discard), Identity: id}
	set(g)
	if err := g.Start("127.0.9.60:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return g
}

// A server names TCP ports of other programs, and match host ports: the front listens on the UDP ones only. Its own fixed
// TCP ports come from Start, which is what the game is told.
func TestFrontIgnoresServerTCPPorts(t *testing.T) {
	g := portsGateway(t, func(g *tunnel.Gateway) {
		g.Listener(5432)
		g.Listener(6379)
		g.ForwardUDP(20777)
	})
	ip := net.ParseIP(frontLocal).To4()
	f := &Front{Log: logging.New(io.Discard), TunnelIP: ip, Ports: DefaultFrontPorts, done: make(chan struct{}),
		tcpLns: map[int]net.Listener{}, udpLns: map[int]*net.UDPConn{}, conns: map[net.Conn]struct{}{}}
	s := &frontSession{b: Bookmark{Tag: "evil", Address: "127.0.9.60"}}
	s.c = &tunnel.Client{Log: logging.New(io.Discard), Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: ip, External: true,
		DialTimeout: 5 * time.Second}
	s.c.Deliver = f.deliverFor(s)
	if err := s.c.Start(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.cur = s
	f.mu.Unlock()
	f.ensureListeners(s)
	defer f.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.tcpLns) != 0 {
		t.Errorf("the front listens on a server's TCP ports: %d of them", len(f.tcpLns))
	}
	if f.udpLns[20777] == nil {
		t.Error("the front doesn't listen on a match host's UDP port")
	}
}

// Start-Client's one tunnel (picker = off) has no port mapping, so a server with a TCP port outside the game's is refused,
// saying what works instead. TestStartClientTunnel has the case that passes: a server with the game's ports.
func TestStartTunnelRefusesOtherTCPPorts(t *testing.T) {
	g := portsGateway(t, func(g *tunnel.Gateway) { g.Listener(20041) })
	var out bytes.Buffer
	_, _, err := startTunnel(IO{Out: &out, ExeDir: t.TempDir()}, setup.Layout{Root: t.TempDir()}, g.Addr(), g.Identity.Pin, frontLocal, nil)
	if !errors.Is(err, tunnel.ErrTCPPort) {
		t.Fatalf("a server with TCP port 20041: %v", err)
	}
	if !strings.Contains(err.Error(), "20041") || !strings.Contains(err.Error(), "picker = auto") {
		t.Errorf("the error doesn't say what to do: %v", err)
	}
}
