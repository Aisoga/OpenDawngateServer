package tunnel

import (
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"
)

// A match gateway is a Gateway with Admit set: the gateway of a host machine in another region, which carries only
// match datagrams. A session there needs a ticket (ticket.go) in its session hello: the ticket names a match, and the
// admitted session gets one route, that match's GMS port to its host on this machine. A second ticket for the same
// match (a second login behind the same Start-Client) adds its user to the session through a ticket hello:
//
//	client -> gateway: "DGTN", version uint8 (1), kind uint8 (kindTicket), session id uint64, token [16],
//	                   ticket length uint16 + the ticket
//	gateway -> client: status uint8 (StatusOK: added; StatusUnavailable: refused), then the gateway closes
//
// A match gateway serves no TCP port, answers no datagram in process and has no Forward (Start refuses those), so an
// admitted session reaches its match's port and nothing else.
const kindTicket byte = 5

// Admission is what a match gateway's Admit makes of a valid ticket.
type Admission struct {
	Match int64
	User  int64
	// Port is the match's GMS port (the port the game sends to, the same number on every machine); Target is where
	// its datagrams go (the match host on this machine)
	Port   int
	Target *net.UDPAddr
}

// MaxTicketUsers bounds the users of one match session (the logins behind one Start-Client in one match); Start-Client
// hands an away session no more tickets than that
const MaxTicketUsers = 8

// admission is an admitted session's match and the users of its tickets.
type admission struct {
	match  int64
	port   int
	target *net.UDPAddr
	users  []int64
}

// TicketAdmit is the Admit of a host machine: guard checks the ticket (TicketGuard.Check: MAC, machine, time, one use),
// running gives the match's host address on this machine (false: not running here, ErrTicketMatch).
func TicketAdmit(guard *TicketGuard, running func(match int64) (*net.UDPAddr, bool)) func([]byte, netip.Addr) (Admission, error) {
	return func(ticket []byte, _ netip.Addr) (Admission, error) {
		t, err := guard.Check(ticket)
		if err != nil {
			return Admission{}, err
		}
		target, ok := running(t.Match)
		if !ok || target == nil {
			return Admission{}, ErrTicketMatch
		}
		return Admission{Match: t.Match, User: t.User, Port: target.Port, Target: target}, nil
	}
}

// checkAdmit refuses a match gateway that would reach more than its tickets' matches.
func (g *Gateway) checkAdmit() error {
	if g.Admit == nil {
		return nil
	}
	if g.Forward != nil || len(g.handlers) > 0 || len(g.listeners) > 0 {
		return errors.New("tunnel: a match gateway (Admit) serves no TCP port and no in-process datagrams and has no Forward")
	}
	if g.Running == nil {
		return errors.New("tunnel: a match gateway (Admit) needs Running (where its matches run now)")
	}
	return nil
}

// matchGone reports whether an admitted session's match no longer runs at its route (s.mu may be held: Running takes
// only the caller's own lock).
func (g *Gateway) matchGone(a *admission) bool {
	if g.Running == nil {
		return false
	}
	t, ok := g.Running(a.match)
	return !ok || t == nil || t.Port != a.port || !t.IP.Equal(a.target.IP)
}

// endGoneMatches ends the admitted sessions whose match no longer runs at their route (the janitor): a Start-Client
// that keeps such a session up with heartbeats can't reach the next match on that port.
func (g *Gateway) endGoneMatches(all []*session) {
	if g.Admit == nil {
		return
	}
	for _, s := range all {
		s.mu.Lock()
		gone := s.adm != nil && !s.ended && g.matchGone(s.adm)
		match := int64(0)
		if s.adm != nil {
			match = s.adm.match
		}
		s.mu.Unlock()
		if !gone {
			continue
		}
		g.mu.Lock()
		if g.sessions[s.id] == s {
			delete(g.sessions, s.id)
		}
		g.mu.Unlock()
		s.end(fmt.Sprintf("its match %d no longer runs here", match))
	}
}

// mayOpen refuses a session hello that would be refused after its ticket was checked (the gateway closing, or a new
// session over MaxSessionsPerPeer), so a refused session never spends its one-use ticket. The same checks run again
// when the session is made (a race between two hellos can still spend one).
func (g *Gateway) mayOpen(c *tls.Conn, peer string, h sessionHello) bool {
	if g.Admit == nil {
		return true
	}
	peerIP := PeerOf(c.RemoteAddr())
	g.mu.Lock()
	closed := g.closed
	old := g.sessions[h.resumeID]
	resume := h.resumeID != 0 && old != nil && subtle.ConstantTimeCompare(old.token[:], h.resumeToken[:]) == 1
	n := g.sessionsOf(peerIP)
	g.mu.Unlock()
	switch {
	case closed:
		c.Write([]byte{StatusUnavailable})
		return false
	case !resume && g.MaxSessionsPerPeer > 0 && n >= g.MaxSessionsPerPeer:
		g.sessionsCut.Add(1)
		c.Write([]byte{StatusUnavailable})
		if g.Limits == nil || g.Limits.ShouldLog("tunnel sessions", peerIP.String()) {
			g.logf("limits: session from %s refused: %d sessions of that address already (at most %d; further refusals "+
				"counted in the stats lines)", peer, n, g.MaxSessionsPerPeer)
		}
		return false
	}
	return true
}

// admitHello checks a session hello's ticket on a match gateway before the session is made: ok false = refused (the
// status written and logged). A hello that takes up a live session again (its id and token) needs no new ticket (nil,
// true); on a gateway without Admit a hello with a ticket is refused, one without passes (nil, true).
func (g *Gateway) admitHello(c *tls.Conn, peer string, h sessionHello) (*Admission, bool) {
	if g.Admit == nil {
		if len(h.ticket) == 0 {
			return nil, true
		}
		c.Write([]byte{StatusUnavailable})
		g.logf("%s: session refused: its hello carries a ticket, and this gateway is no match gateway", peer)
		return nil, false
	}
	if h.resumeID != 0 {
		g.mu.Lock()
		old := g.sessions[h.resumeID]
		g.mu.Unlock()
		if old != nil && subtle.ConstantTimeCompare(old.token[:], h.resumeToken[:]) == 1 {
			return nil, true
		}
	}
	addr := PeerOf(c.RemoteAddr())
	if len(h.ticket) == 0 {
		c.Write([]byte{StatusUnavailable})
		g.refusedTicket(addr, "%s: session refused: no ticket (a match gateway admits only the main server's players)", peer)
		return nil, false
	}
	a, err := g.Admit(h.ticket, addr)
	if err == nil && (a.Target == nil || a.Port <= 0 || a.Port > 65535) {
		err = ErrTicketMatch
	}
	if err != nil {
		c.Write([]byte{StatusUnavailable})
		g.refusedTicket(addr, "%s: session refused: %v", peer, err)
		return nil, false
	}
	return &a, true
}

// admitted gives a session its hello's version and, on a match gateway, its admission and its one route; false: a
// match gateway's new session without one (the session it meant to take up again ended meanwhile), which is dropped.
func (g *Gateway) admitted(s *session, ver byte, a *Admission, resumed bool) bool {
	s.mu.Lock()
	s.helloVer = ver
	if a != nil && s.adm == nil {
		s.adm = &admission{match: a.Match, port: a.Port, target: a.Target, users: []int64{a.User}}
		s.routes[a.Port] = a.Target
	}
	missing := g.Admit != nil && s.adm == nil
	s.mu.Unlock()
	if missing && !resumed {
		g.mu.Lock()
		if g.sessions[s.id] == s {
			delete(g.sessions, s.id)
		}
		g.mu.Unlock()
		s.end("no ticket")
		return false
	}
	if a != nil {
		g.logf("session %d: admitted by its ticket: match %d, user %d; its one route: port %d to %s", s.id, a.Match, a.User,
			a.Port, a.Target)
	}
	return true
}

// serveTicket adds a ticket's user to a match session (the ticket hello).
func (g *Gateway) serveTicket(c *tls.Conn, peer string) {
	defer c.Close()
	var b [8 + 16 + 2]byte
	if _, err := io.ReadFull(c, b[:]); err != nil {
		g.logf("%s: ticket hello: %v", peer, err)
		return
	}
	n := int(binary.BigEndian.Uint16(b[24:]))
	if n > MaxTicketLen {
		g.logf("%s: ticket hello: a ticket of %d bytes (at most %d)", peer, n, MaxTicketLen)
		return
	}
	ticket := make([]byte, n)
	if _, err := io.ReadFull(c, ticket); err != nil {
		g.logf("%s: ticket hello: %v", peer, err)
		return
	}
	id := binary.BigEndian.Uint64(b[:])
	g.mu.Lock()
	s := g.sessions[id]
	g.mu.Unlock()
	if s == nil || subtle.ConstantTimeCompare(s.token[:], b[8:24]) != 1 {
		c.Write([]byte{StatusNoSession})
		g.logf("%s: ticket refused: unknown session", peer)
		return
	}
	addr := PeerOf(c.RemoteAddr())
	if g.Admit == nil {
		c.Write([]byte{StatusUnavailable})
		g.logf("session %d: ticket refused: this gateway is no match gateway", id)
		return
	}
	a, err := g.Admit(ticket, addr)
	s.mu.Lock()
	adm := s.adm
	if err == nil && (adm == nil || a.Match != adm.match) {
		err = fmt.Errorf("ticket: for match %d, and the session is another match's (a ticket for another match opens another session)", a.Match)
	}
	if err == nil && !slices.Contains(adm.users, a.User) {
		if len(adm.users) >= MaxTicketUsers {
			err = fmt.Errorf("ticket: the session has %d users already", len(adm.users))
		} else {
			adm.users = append(adm.users, a.User)
		}
	}
	s.mu.Unlock()
	if err != nil {
		c.Write([]byte{StatusUnavailable})
		g.refusedTicket(addr, "session %d: ticket refused: %v", id, err)
		return
	}
	c.Write([]byte{StatusOK})
	g.logf("session %d: ticket added: match %d, user %d", id, a.Match, a.User)
}

// FlowAdmission names the match and the ticket users of the match session behind a flow's own socket (FlowSession):
// what the agent's relay adds to a host's verifyHello question. False: no flow of an admitted session has that address.
func (g *Gateway) FlowAdmission(addr netip.AddrPort) (match int64, users []int64, ok bool) {
	id, ok := g.FlowSession(addr)
	if !ok {
		return 0, nil, false
	}
	g.mu.Lock()
	s := g.sessions[id]
	g.mu.Unlock()
	if s == nil {
		return 0, nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.adm == nil {
		return 0, nil, false
	}
	return s.adm.match, slices.Clone(s.adm.users), true
}

// refusedTicket logs a refusal at most once a minute per address (Limits.ShouldLog when the gateway has limits).
func (g *Gateway) refusedTicket(addr netip.Addr, format string, a ...any) {
	if g.Limits != nil {
		if g.Limits.ShouldLog("tunnel tickets", addr.String()) {
			g.logf(format, a...)
		}
		return
	}
	if g.ticketLog.should(addr, time.Now()) {
		g.logf(format, a...)
	}
}

// onceAMinute remembers when a line was last logged per address (bounded: it forgets everything past 4,096 addresses).
type onceAMinute struct {
	mu   sync.Mutex
	last map[netip.Addr]time.Time
}

func (o *onceAMinute) should(a netip.Addr, now time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if t, ok := o.last[a]; ok && now.Sub(t) < time.Minute {
		return false
	}
	if o.last == nil || len(o.last) >= 4096 {
		o.last = map[netip.Addr]time.Time{}
	}
	o.last[a] = now
	return true
}

// mapsNotNamed is a version 4 session hello's maps count for no list (a nil Client.Maps: the server keeps its release
// rule, as for a version 2 hello).
const mapsNotNamed = 0xFF

// appendTicket writes a session hello's ticket: its length uint16 and its bytes (cut to MaxTicketLen).
func appendTicket(b, ticket []byte) []byte {
	if len(ticket) > MaxTicketLen {
		ticket = ticket[:MaxTicketLen]
	}
	b = binary.BigEndian.AppendUint16(b, uint16(len(ticket)))
	return append(b, ticket...)
}

// readTicket reads a session hello's ticket (nil: none).
func readTicket(r io.Reader) ([]byte, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(l[:]))
	if n == 0 {
		return nil, nil
	}
	if n > MaxTicketLen {
		return nil, fmt.Errorf("the session hello's ticket has %d bytes (at most %d)", n, MaxTicketLen)
	}
	t := make([]byte, n)
	if _, err := io.ReadFull(r, t); err != nil {
		return nil, err
	}
	return t, nil
}

// ticketHello is a ticket hello's bytes.
func ticketHello(session uint64, token [16]byte, ticket []byte) []byte {
	b := append([]byte(magic), version, kindTicket)
	b = binary.BigEndian.AppendUint64(b, session)
	b = append(b, token[:]...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(ticket)))
	return append(b, ticket...)
}

// AddTicket hands the match gateway a second ticket for this session's match (another login behind this Start-Client):
// its user joins the session's users. An error: the gateway refused it, or the session is down.
func (c *Client) AddTicket(ticket []byte) error {
	if len(ticket) == 0 || len(ticket) > MaxTicketLen {
		return fmt.Errorf("a ticket of %d bytes", len(ticket))
	}
	s := c.session(10 * time.Second)
	if s == nil {
		return errors.New("no tunnel session")
	}
	gc, err := c.dial("tcp4", c.Gateway)
	if err != nil {
		return err
	}
	defer gc.Close()
	defer c.whileContext(gc)()
	gc.SetDeadline(time.Now().Add(c.DialTimeout))
	var st [1]byte
	if _, err = gc.Write(ticketHello(s.id, s.token, ticket)); err == nil {
		_, err = io.ReadFull(gc, st[:])
	}
	if err != nil {
		return err
	}
	if st[0] != StatusOK {
		return fmt.Errorf("the gateway refused the ticket: %s", StatusText(st[0]))
	}
	return nil
}
