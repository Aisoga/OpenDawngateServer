package tunnel

import (
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// matchEnv is a host machine's match gateway with two running matches (7 and 8), each an echo host on this machine.
type matchEnv struct {
	g         *Gateway
	guard     *TicketGuard
	m7, m8    *udpEcho
	log       *syncBuf
	nextNonce byte
}

func newMatchEnv(t *testing.T) *matchEnv {
	t.Helper()
	e := &matchEnv{log: &syncBuf{}, guard: &TicketGuard{Machine: "na1"}}
	e.guard.SetKey(testTicketKey, time.Now())
	e.m7, e.m8 = newUDPEcho(t, gwIP), newUDPEcho(t, gwIP)
	running := func(m int64) (*net.UDPAddr, bool) {
		switch m {
		case 7:
			return e.m7.addr(), true
		case 8:
			return e.m8.addr(), true
		}
		return nil, false
	}
	id, _, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.g = &Gateway{Log: logging.New(e.log), Identity: id, BindIP: net.ParseIP(gwIP), Admit: TicketAdmit(e.guard, running),
		Running: running}
	e.g.ForwardUDP(e.m7.addr().Port, e.m8.addr().Port)
	if err := e.g.Start(net.JoinHostPort(gwIP, "0")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.g.Close)
	return e
}

// ticket mints a fresh ticket of the link's key.
func (e *matchEnv) ticket(t *testing.T, machine string, match, user int64) []byte {
	t.Helper()
	e.nextNonce++
	tk := Ticket{Machine: machine, Match: match, User: user, Issued: time.Now()}
	tk.Nonce[0] = e.nextNonce
	b, err := MintTicket(testTicketKey, tk)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// client opens a session to the match gateway with ticket (nil: none).
func (e *matchEnv) client(t *testing.T, ticket []byte) (*Client, error) {
	t.Helper()
	c := &Client{Log: logging.New(e.log), Gateway: e.g.Addr(), Pin: e.g.Identity.Pin, TunnelIP: net.ParseIP(tunIP),
		DialIP: net.ParseIP(peerIP), Ticket: ticket, DialTimeout: 3 * time.Second}
	if err := c.Start(); err != nil {
		return nil, err
	}
	t.Cleanup(c.Close)
	return c, nil
}

func refusedWith(err error, status byte) bool {
	var r *RefusedError
	return errors.As(err, &r) && r.Status == status
}

// TestMatchGatewayAdmission: a session needs a valid ticket; an admitted one reaches its match's port and no other;
// a second ticket for the same match adds its user (FlowAdmission), one for another match or a used one is refused.
func TestMatchGatewayAdmission(t *testing.T) {
	e := newMatchEnv(t)
	if _, err := e.client(t, nil); !refusedWith(err, StatusUnavailable) {
		t.Errorf("a session without a ticket: %v", err)
	}
	if _, err := e.client(t, e.ticket(t, "eu1", 7, 1)); !refusedWith(err, StatusUnavailable) {
		t.Errorf("a ticket for another machine: %v", err)
	}
	if _, err := e.client(t, e.ticket(t, "na1", 9, 1)); !refusedWith(err, StatusUnavailable) {
		t.Errorf("a ticket for a match not running here: %v", err)
	}
	first := e.ticket(t, "na1", 7, 2000000001)
	c, err := e.client(t, first)
	if err != nil {
		t.Fatalf("a good ticket: %v", err)
	}
	if _, err := e.client(t, first); !refusedWith(err, StatusUnavailable) {
		t.Errorf("the same ticket again: %v", err)
	}
	// the refusals of one address: the first a minute is logged
	if !e.log.waitFor(2*time.Second, "session refused: no ticket", "admitted by its ticket: match 7, user 2000000001") ||
		strings.Contains(e.log.String(), "ticket: for another host machine") {
		t.Errorf("the log:\n%s", e.log.String())
	}
	if mac := first[len(first)-32:]; strings.Contains(e.log.String(), string(mac)) || strings.Contains(e.log.String(), hex.EncodeToString(mac)) {
		t.Error("the log holds a ticket's bytes")
	}
	game := gameUDP(t)
	if b, _, err := roundTrip(t, game, e.m7.addr().Port, []byte("to match 7"), 2*time.Second); err != nil || string(b) != "to match 7" {
		t.Fatalf("the match's port: %q %v", b, err)
	}
	if b, _, err := roundTrip(t, game, e.m8.addr().Port, []byte("to match 8"), 300*time.Millisecond); err == nil {
		t.Errorf("another match's port answered: %q", b)
	}
	if d, _ := e.m8.counts(); d != 0 {
		t.Errorf("match 8's host got %d datagrams from a match 7 session", d)
	}
	s := c.session(time.Second)
	gs := gatewaySession(t, e.g)
	gs.mu.Lock()
	var local netip.AddrPort
	for _, f := range gs.flows {
		local = f.local
	}
	gs.mu.Unlock()
	if m, users, ok := e.g.FlowAdmission(local); !ok || m != 7 || !slices.Equal(users, []int64{2000000001}) {
		t.Errorf("FlowAdmission = %d %v %v", m, users, ok)
	}
	// a second login behind the same Start-Client: its ticket joins the session
	if err := c.AddTicket(e.ticket(t, "na1", 7, 2000000002)); err != nil {
		t.Errorf("a second ticket for match 7: %v", err)
	}
	if err := c.AddTicket(e.ticket(t, "na1", 8, 2000000003)); err == nil {
		t.Error("a ticket for match 8 joined a match 7 session")
	}
	if err := c.AddTicket(first); err == nil {
		t.Error("a used ticket joined")
	}
	if m, users, ok := e.g.FlowAdmission(local); !ok || m != 7 || !slices.Equal(users, []int64{2000000001, 2000000002}) {
		t.Errorf("FlowAdmission after the second ticket = %d %v %v", m, users, ok)
	}
	if _, _, ok := e.g.FlowAdmission(netip.AddrPortFrom(local.Addr(), local.Port()+1)); ok {
		t.Error("FlowAdmission of no flow")
	}
	// taken up again within the grace: no new ticket needed (the first is used)
	gs.mu.Lock()
	gs.control.Close()
	gs.mu.Unlock()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if ns := c.session(0); ns != nil && ns != s {
			if ns.id != s.id {
				t.Fatalf("a new session %d after the break, want %d taken up again", ns.id, s.id)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the client didn't connect again; log:\n%s", e.log.String())
		}
	}
	if b, _, err := roundTrip(t, game, e.m7.addr().Port, []byte("after"), 2*time.Second); err != nil || string(b) != "after" {
		t.Errorf("after the break: %q %v", b, err)
	}
}

// TestMatchGatewayConfig: a match gateway with a service, an in-process port or Forward doesn't start; a gateway without
// Admit refuses a session hello with a ticket, and a ticket hello.
func TestMatchGatewayConfig(t *testing.T) {
	id, _, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	admit := func([]byte, netip.Addr) (Admission, error) { return Admission{}, nil }
	for name, g := range map[string]*Gateway{
		"Forward":   {Identity: id, Admit: admit, Forward: func(int) *net.UDPAddr { return nil }},
		"a service": {Identity: id, Admit: admit, listeners: map[int]*listener{1: nil}},
		"HandleUDP": {Identity: id, Admit: admit, handlers: map[int]PacketHandler{5000: nil}},
	} {
		if err := g.Start(net.JoinHostPort(gwIP, "0")); err == nil || !strings.Contains(err.Error(), "match gateway") {
			g.Close()
			t.Errorf("%s: %v", name, err)
		}
	}
	e := newEnv(t)
	c := &Client{Gateway: e.g.Addr(), Pin: e.g.Identity.Pin, TunnelIP: net.ParseIP(tunIP), DialIP: net.ParseIP(peerIP),
		Ticket: []byte("a ticket"), DialTimeout: 3 * time.Second}
	if err := c.Start(); !refusedWith(err, StatusUnavailable) {
		c.Close()
		t.Errorf("a ticket to the main gateway: %v", err)
	}
	if err := e.c.AddTicket([]byte("a ticket")); err == nil {
		t.Error("the main gateway took a ticket hello")
	}
	if !e.logBuf.waitFor(2*time.Second, "this gateway is no match gateway") {
		t.Errorf("the log:\n%s", e.logBuf.String())
	}
	// the ticket hello's bytes
	var tok [16]byte
	tok[0] = 0xAA
	want := "DGTN\x01\x05\x00\x00\x00\x00\x00\x00\x00\x2a\xaa" + strings.Repeat("\x00", 15) + "\x00\x03abc"
	if got := ticketHello(42, tok, []byte("abc")); string(got) != want {
		t.Errorf("ticketHello = %q\nwant         %q", got, want)
	}
}
