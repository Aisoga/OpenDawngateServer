package tunnel

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// runningSet is a host machine's running matches, changed by the test.
type runningSet struct {
	mu sync.Mutex
	m  map[int64]*net.UDPAddr
}

func (r *runningSet) set(m map[int64]*net.UDPAddr) {
	r.mu.Lock()
	r.m = m
	r.mu.Unlock()
}

func (r *runningSet) running(match int64) (*net.UDPAddr, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.m[match]
	return a, ok
}

// secGateway is a match gateway over run's matches with a short grace (its janitor looks every 100 ms) and at most
// perPeer sessions per address (0: no cap).
func secGateway(t *testing.T, run *runningSet, guard *TicketGuard, perPeer int, log *syncBuf, ports ...int) *Gateway {
	t.Helper()
	id, _, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{Log: logging.New(log), Identity: id, BindIP: net.ParseIP(gwIP), Admit: TicketAdmit(guard, run.running),
		Running: run.running, Grace: 400 * time.Millisecond, FlowIdle: 5 * time.Second, MaxSessionsPerPeer: perPeer}
	g.ForwardUDP(ports...)
	if err := g.Start(net.JoinHostPort(gwIP, "0")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return g
}

func secTicket(t *testing.T, nonce byte, match, user int64) []byte {
	t.Helper()
	tk := Ticket{Machine: "na1", Match: match, User: user, Issued: time.Now()}
	tk.Nonce[0], tk.Nonce[1] = nonce, 0xA5
	b, err := MintTicket(testTicketKey, tk)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func secClient(t *testing.T, g *Gateway, log *syncBuf, ticket []byte) (*Client, error) {
	t.Helper()
	c := &Client{Log: logging.New(log), Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tunIP),
		DialIP: net.ParseIP(peerIP), Ticket: ticket, DialTimeout: 3 * time.Second}
	if err := c.Start(); err != nil {
		return nil, err
	}
	t.Cleanup(c.Close)
	return c, nil
}

// TestMatchGatewayMatchEnds: an admitted session reaches its match only while that match runs at its route. When the
// match ends and a later match takes its port, the session's datagrams go nowhere and the janitor ends the session,
// so a Start-Client that keeps it up can't reach the later match. A match gateway without Running doesn't start.
func TestMatchGatewayMatchEnds(t *testing.T) {
	guard := &TicketGuard{Machine: "na1"}
	guard.SetKey(testTicketKey, time.Now())
	host := newUDPEcho(t, gwIP)
	run := &runningSet{}
	run.set(map[int64]*net.UDPAddr{7: host.addr()})
	log := &syncBuf{}
	g := secGateway(t, run, guard, 0, log, host.addr().Port)
	if _, err := secClient(t, g, log, secTicket(t, 1, 7, 2000000001)); err != nil {
		t.Fatal(err)
	}
	game := gameUDP(t)
	if b, _, err := roundTrip(t, game, host.addr().Port, []byte("match 7"), 2*time.Second); err != nil || string(b) != "match 7" {
		t.Fatalf("the match's port: %q %v", b, err)
	}
	before, _ := host.counts()

	run.set(map[int64]*net.UDPAddr{9: host.addr()}) // match 7 ended, match 9 runs on its port
	if b, _, err := roundTrip(t, game, host.addr().Port, []byte("to match 9"), 300*time.Millisecond); err == nil {
		t.Errorf("a match 7 session reached match 9: %q", b)
	}
	if after, _ := host.counts(); after != before {
		t.Errorf("the host got %d datagrams after its match ended", after-before)
	}
	if !log.waitFor(3*time.Second, "ended (its match 7 no longer runs here)") {
		t.Errorf("the session wasn't ended:\n%s", log.String())
	}

	id, _, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bare := &Gateway{Identity: id, Admit: TicketAdmit(guard, run.running)}
	if err := bare.Start(net.JoinHostPort(gwIP, "0")); err == nil || !strings.Contains(err.Error(), "needs Running") {
		if err == nil {
			bare.Close()
		}
		t.Errorf("a match gateway without Running: %v", err)
	}
}

// TestMatchGatewayTicketKept: a session refused for the per-address cap doesn't spend its one-use ticket.
func TestMatchGatewayTicketKept(t *testing.T) {
	guard := &TicketGuard{Machine: "na1"}
	guard.SetKey(testTicketKey, time.Now())
	host := newUDPEcho(t, gwIP)
	run := &runningSet{}
	run.set(map[int64]*net.UDPAddr{7: host.addr()})
	log := &syncBuf{}
	g := secGateway(t, run, guard, 1, log, host.addr().Port)
	if _, err := secClient(t, g, log, secTicket(t, 1, 7, 2000000001)); err != nil {
		t.Fatal(err)
	}
	second := secTicket(t, 2, 7, 2000000002)
	if _, err := secClient(t, g, log, second); !refusedWith(err, StatusUnavailable) {
		t.Fatalf("a second session of the address: %v", err)
	}
	if !log.waitFor(2*time.Second, "sessions of that address already (at most 1") {
		t.Errorf("the log:\n%s", log.String())
	}
	if _, err := guard.Check(second); err != nil {
		t.Errorf("the refused session's ticket was spent: %v", err)
	}
}
