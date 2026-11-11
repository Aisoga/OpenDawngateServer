package launcher

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/tunnel"
)

// The front's away sessions: a match on a host machine in another region. The main server (the current session's)
// sends an away message for a match port (tunnel.Addr.RouteAway): that machine's match gateway, its pin and a ticket.
// The front checks the address (awayAddrAllowed) and the port (one of the current session's UDP ports) and opens a
// second session there (a tunnel.Client in External mode with the ticket); that port's datagrams then go through it, and
// its answers reach the game from the same local port. A second ticket for the same match and gateway (another login
// behind this front) joins the open session (a ticket hello). The away session ends with the main server's home message
// for its port, when the current session is no longer the one that sent it (a switch, Front.EndSession, Close), or after
// AwayIdle without a datagram either way.
//
// What a hostile main server can make Start-Client do with away messages is bounded: at most maxAway sessions at a
// time (one per port); one connect at a time per port (a later message for the port cancels the earlier connect, and
// its own waits until that one has ended); a session's joins one after the other on its own goroutine, at most
// tunnel.MaxTicketUsers tickets per session (the gateway takes no more); and the away messages at most awayBurst at
// once and awayRate a second after that, the rest dropped before anything is dialed or logged. So it gets at most
// maxAway TLS connects in flight, a few a second, to addresses of its choice, and none into the player's own network.
const maxAway = 4

// The rate of a server's away messages: a burst (a match's logins behind one front, a rejoin) and a steady rate.
const (
	awayBurst = 16
	awayRate  = 2 // messages a second
)

// tokenBucket is a rate's bucket (the caller locks).
type tokenBucket struct {
	tokens float64
	last   time.Time
}

// take takes one token at now; false: none left.
func (b *tokenBucket) take(now time.Time, rate, burst float64) bool {
	if b.last.IsZero() {
		b.tokens = burst
	} else if d := now.Sub(b.last).Seconds(); d > 0 {
		b.tokens = min(burst, b.tokens+d*rate)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// awaySession is one match port's session to a host machine's match gateway.
type awaySession struct {
	port  int // the server port (a match host's GMS port; the game's port is the same number)
	gw    netip.AddrPort
	pin   tunnel.Pin
	match int64 // the ticket's (tunnel.ParseTicket)

	c       *tunnel.Client // nil while it connects
	tickets int            // the tickets handed to it so far, the first one's included (at most tunnel.MaxTicketUsers)
	joins   chan []byte    // the further tickets, joined one after the other by runAway
	last    atomic.Int64   // unix nanoseconds of the last datagram either way

	ctx    context.Context // done with done: ends its connect's and its joins' dials
	cancel context.CancelFunc
	prev   *awaySession  // the port's earlier session, whose runAway ends before this one dials
	exited chan struct{} // closed when its runAway has returned
	done   chan struct{}
	once   sync.Once
}

// newAwaySession is a session for port to gw for its first ticket's match.
func newAwaySession(port int, gw netip.AddrPort, pin tunnel.Pin, match int64) *awaySession {
	as := &awaySession{port: port, gw: gw, pin: pin, match: match, tickets: 1, joins: make(chan []byte, tunnel.MaxTicketUsers),
		exited: make(chan struct{}), done: make(chan struct{})}
	as.ctx, as.cancel = context.WithCancel(context.Background())
	as.last.Store(time.Now().UnixNano())
	return as
}

func (a *awaySession) stop() {
	a.once.Do(func() {
		close(a.done)
		if a.cancel != nil {
			a.cancel()
		}
	})
}

// awayIdle is AwayIdle or the gateway's flow idle time.
func (f *Front) awayIdle() time.Duration {
	if f.AwayIdle > 0 {
		return f.AwayIdle
	}
	return tunnel.DefaultFlowIdle
}

// hookAway lets a session's main server send match ports away (connect's hook).
func (f *Front) hookAway(s *frontSession) {
	s.c.OnAway = func(port int, a tunnel.Away) { f.takeAway(s, s.c.GatewayIP(), port, a) }
	s.c.OnHome = func(port int) { f.endAway(s, port, nil, "the server sent it home") }
}

// awayAddrAllowed is the rule on a match gateway's address: an IPv4 unicast address on the internet; a loopback one
// only from a main server on this PC, one in a private network only from a main server in a private network (the test
// lanes, a LAN), one in the carrier-grade NAT range 100.64.0.0/10 only from a main server in that range; 0.0.0.0/8 and
// the reserved 240.0.0.0/4 never. So a server can't make Start-Client dial into the player's own network.
func awayAddrAllowed(main, away netip.Addr) error {
	away = away.Unmap()
	main = main.Unmap()
	switch {
	case !away.Is4():
		return fmt.Errorf("%v is no IPv4 address", away)
	case away.IsLoopback():
		if !main.IsLoopback() {
			return fmt.Errorf("%v is a loopback address, and the server isn't on this PC", away)
		}
	case away.IsPrivate():
		if !main.IsPrivate() {
			return fmt.Errorf("%v is in a private network, and the server isn't", away)
		}
	case sharedNet.Contains(away):
		if !sharedNet.Contains(main) {
			return fmt.Errorf("%v is in the carrier-grade NAT range %v, and the server isn't", away, sharedNet)
		}
	case thisNet.Contains(away) || reservedNet.Contains(away) || !away.IsGlobalUnicast():
		return fmt.Errorf("%v is no unicast address", away)
	}
	return nil
}

// The IPv4 ranges awayAddrAllowed treats on their own (RFC 6598, RFC 1122, RFC 1112).
var (
	sharedNet   = netip.MustParsePrefix("100.64.0.0/10")
	thisNet     = netip.MustParsePrefix("0.0.0.0/8")
	reservedNet = netip.MustParsePrefix("240.0.0.0/4")
)

// takeAway takes an away message of the current session's main server (mainIP: its gateway's address): its rate
// first, then the message, then a session to the match gateway opened in the background (the control connection's
// reader isn't held up).
func (f *Front) takeAway(s *frontSession, mainIP netip.Addr, port int, a tunnel.Away) {
	if f.current() != s {
		return
	}
	s.awayMu.Lock()
	if !s.awayRate.take(time.Now(), awayRate, awayBurst) {
		s.awayDropped++
		first := s.awayDropped == 1
		s.awayMu.Unlock()
		if first {
			f.logf("away: the server's away messages come faster than %d a second; dropping them (counted)", awayRate)
		}
		return
	}
	dropped := s.awayDropped
	s.awayDropped = 0
	s.awayMu.Unlock()
	if dropped > 0 {
		f.logf("away: %d away messages of the server dropped (too fast)", dropped)
	}
	refuse := func(format string, args ...any) {
		f.logf("away: port %d to %s refused: %s", port, a.Gateway, fmt.Sprintf(format, args...))
	}
	if _, udp := s.c.Ports(); !slices.Contains(udp, port) {
		refuse("the server's session doesn't list that port")
		return
	}
	if a.Gateway.Port() == 0 {
		refuse("no gateway port")
		return
	}
	if err := awayAddrAllowed(mainIP, a.Gateway.Addr()); err != nil {
		refuse("%v", err)
		return
	}
	t, err := tunnel.ParseTicket(a.Ticket)
	if err != nil {
		refuse("%v", err)
		return
	}
	s.awayMu.Lock()
	if s.away == nil {
		s.away = map[int]*awaySession{}
	}
	if old := s.away[port]; old != nil && old.gw == a.Gateway && old.match == t.Match && tunnel.SamePin(old.pin, a.Pin) {
		n := old.tickets
		full := n >= tunnel.MaxTicketUsers || old.joins == nil
		if !full {
			old.tickets++
			old.joins <- a.Ticket // never blocks: room for every ticket after the first
		}
		s.awayMu.Unlock()
		if full {
			refuse("the session to that match has %d tickets already (at most %d)", n, tunnel.MaxTicketUsers)
		}
		return
	}
	others := 0
	for p := range s.away {
		if p != port {
			others++
		}
	}
	if others >= maxAway {
		s.awayMu.Unlock()
		refuse("%d away sessions are open already (at most %d)", others, maxAway)
		return
	}
	old := s.away[port]
	as := newAwaySession(port, a.Gateway, a.Pin, t.Match)
	as.prev = old
	s.away[port] = as
	s.awayMu.Unlock()
	if old != nil {
		old.stop() // the port's earlier match: its connect is cancelled, its loop closes it
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		as.stop()
		return
	}
	f.wg.Add(1)
	f.mu.Unlock()
	go f.runAway(s, as, a.Ticket)
}

// joinAway hands an open away session another login's ticket.
func (f *Front) joinAway(as *awaySession, c *tunnel.Client, ticket []byte) {
	if err := c.AddTicket(ticket); err != nil {
		f.logf("away: port %d: the second ticket for %s wasn't taken: %v", as.port, as.gw, err)
		return
	}
	f.logf("away: port %d: another login's ticket joined the session to %s", as.port, as.gw)
}

// runAway connects an away session and keeps it until it ends. The port's earlier session ends first (stop cancelled
// its connect), so a port has one connect at a time; the session's joins run here, one after the other.
func (f *Front) runAway(s *frontSession, as *awaySession, ticket []byte) {
	defer f.wg.Done()
	defer close(as.exited)
	if p := as.prev; p != nil {
		if p.exited != nil {
			<-p.exited
		}
		as.prev = nil
	}
	select {
	case <-as.done: // replaced while it waited
		f.endAway(s, as.port, as, "")
		return
	case <-f.done:
		f.endAway(s, as.port, as, "Start-Client closes")
		return
	default:
	}
	start := time.Now()
	c := &tunnel.Client{Log: f.Log, Gateway: as.gw.String(), Pin: as.pin, TunnelIP: f.TunnelIP, External: true,
		DialTimeout: f.ConnectTimeout, Release: s.c.Release, Ticket: ticket, Context: as.ctx}
	c.Deliver = f.deliverAway(s, as)
	failed, why := false, ""
	if err := c.Start(); err != nil {
		failed, why = true, fmt.Sprintf("didn't start: %v", err)
		if errors.Is(err, tunnel.ErrPinMismatch) {
			why = "didn't start: the gateway's key is not the one the server named"
		}
		if as.ctx.Err() != nil {
			why = "" // replaced or ended while it connected: no line
		}
	} else if _, udp := c.Ports(); !slices.Contains(udp, as.port) {
		c.Close()
		failed, why = true, "didn't start: the match gateway doesn't list the port"
	}
	if failed {
		f.endAway(s, as.port, as, why)
		return
	}
	s.awayMu.Lock()
	current := s.away[as.port] == as
	if current {
		as.c = c
	}
	s.awayMu.Unlock()
	if !current {
		c.Close()
		return
	}
	f.logf("away: port %d through the match gateway %s (key %s, match %d) in %s", as.port, as.gw, as.pin.Fingerprint(),
		as.match, time.Since(start).Round(time.Millisecond))
	idle := f.awayIdle()
	tick := time.NewTicker(min(max(idle/4, 10*time.Millisecond), time.Second))
	defer tick.Stop()
	for {
		select {
		case <-f.done:
			f.endAway(s, as.port, as, "Start-Client closes")
			return
		case <-as.done:
			f.endAway(s, as.port, as, "")
			return
		case t := <-as.joins:
			f.joinAway(as, c, t)
		case <-tick.C:
			if f.current() != s {
				f.endAway(s, as.port, as, "the server's session ended")
				return
			}
			if time.Since(time.Unix(0, as.last.Load())) > idle {
				f.endAway(s, as.port, as, fmt.Sprintf("no datagram for %s", idle))
				return
			}
		}
	}
}

// endAway ends the away session of port (only as, when given): it leaves the map, closes and is logged with why ("":
// no line, it was replaced or already ended).
func (f *Front) endAway(s *frontSession, port int, as *awaySession, why string) {
	s.awayMu.Lock()
	cur := s.away[port]
	if cur != nil && (as == nil || cur == as) {
		delete(s.away, port)
	} else if as == nil {
		s.awayMu.Unlock()
		return
	}
	if as == nil {
		as = cur
	}
	c := as.c
	as.c = nil
	s.awayMu.Unlock()
	as.stop()
	if c != nil {
		c.Close()
	}
	if why != "" {
		f.logf("away: port %d to %s ended: %s", port, as.gw, why)
	}
}

// sendAway sends a datagram of the game's port through that port's away session; false: the port has none (the main
// session carries it). A port whose away session is still connecting carries nothing meanwhile.
func (f *Front) sendAway(s *frontSession, port int, src *net.UDPAddr, data []byte) bool {
	sp := s.serverPort(f, port)
	s.awayMu.Lock()
	as := s.away[sp]
	var c *tunnel.Client
	if as != nil {
		c = as.c
	}
	s.awayMu.Unlock()
	if as == nil {
		return false
	}
	if c != nil {
		as.last.Store(time.Now().UnixNano())
		c.SendDatagram(sp, src, data)
	}
	return true
}

// deliverAway sends an away session's datagrams from its match gateway to the game, from the game's port (only the
// away session's own port, and only while it and its main session are current).
func (f *Front) deliverAway(s *frontSession, as *awaySession) func(port int, dst *net.UDPAddr, payload []byte) {
	return func(port int, dst *net.UDPAddr, payload []byte) {
		if port != as.port {
			return
		}
		s.awayMu.Lock()
		mine := s.away[port] == as
		s.awayMu.Unlock()
		f.mu.Lock()
		ln, cur, write := f.udpLns[s.localPort(f, port)], f.cur, f.writeGame
		f.mu.Unlock()
		if cur != s || !mine {
			return
		}
		switch {
		case write != nil:
			as.last.Store(time.Now().UnixNano())
			write(port, dst, payload)
		case ln != nil:
			as.last.Store(time.Now().UnixNano())
			ln.WriteToUDP(payload, dst)
		}
	}
}

// awayWithoutFront gives Start-Client's path without the front (startTunnel, picker = off) the front's away sessions
// on its one tunnel.Client c, which listens on the game's ports itself (PREF5): a Front of that one session, which never
// switches and has no listeners of its own. c's listeners hand an away port's datagrams to sendAway (Divert), the
// answers go out from c's listener of the port (WriteLocal); every rule of takeAway holds as with the front. Call it
// before c.Start, since OnAway makes c say the version 4 hello (tunnel.Client.firstHello). Close ends the away sessions
// and c.
func awayWithoutFront(log *logging.Log, c *tunnel.Client, connectTimeout time.Duration) *Front {
	f := &Front{Log: log, TunnelIP: c.TunnelIP, ConnectTimeout: connectTimeout, done: make(chan struct{}),
		tcpLns: map[int]net.Listener{}, udpLns: map[int]*net.UDPConn{}, conns: map[net.Conn]struct{}{},
		logWho: "tunnel"}
	s := &frontSession{c: c}
	f.cur = s
	f.writeGame = func(port int, dst *net.UDPAddr, payload []byte) { c.WriteLocal(port, dst, payload) }
	f.hookAway(s)
	c.Divert = func(port int, src *net.UDPAddr, data []byte) bool { return f.sendAway(s, port, src, data) }
	return f
}

// cancelAwayConnects cancels the session's away connects and joins when it stops being current (a switch, EndSession,
// Close): a connect then ends at once; an open away session ends at its loop's next tick, as before.
func (s *frontSession) cancelAwayConnects() {
	s.awayMu.Lock()
	defer s.awayMu.Unlock()
	for _, as := range s.away {
		if as.cancel != nil {
			as.cancel()
		}
	}
}

// awayPorts lists the ports with an open away session (tests, the log).
func (s *frontSession) awayPorts() []int {
	s.awayMu.Lock()
	defer s.awayMu.Unlock()
	var out []int
	for p, as := range s.away {
		if as.c != nil {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}
