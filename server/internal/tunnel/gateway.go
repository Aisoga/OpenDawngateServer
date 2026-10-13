package tunnel

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dawngate/server/internal/logging"
)

// Defaults of the gateway's and the client's timings.
const (
	DefaultPort             = 42128
	DefaultHeartbeatEvery   = 10 * time.Second
	DefaultHeartbeatTimeout = 35 * time.Second
	DefaultGrace            = 60 * time.Second
	DefaultFlowIdle         = 120 * time.Second
	DefaultHandshakeTimeout = 10 * time.Second
	DefaultMaxFlows         = 64
	DefaultStatsEvery       = time.Minute
)

const who = "tunnel"

// Limits are per-address rate and count limits the gateway applies by each player's real address: dgserver's
// *limits.Limiter satisfies it. Listener wraps the gateway's TCP listener, so every connection (a session's
// control connection and every stream) passes the address's new- and open-connection limits before its TLS handshake
// (a refused one is reset and costs no handshake) and releases its slot when it closes; AllowTunnelPacket takes every
// UDP datagram before the gateway looks at it (no session lookup, no AES work for a refused one); ShouldLog keeps a
// flood's refusals to one log line per address and kind a minute. The services behind the gateway see the same real
// address as Addr.Peer (Blaze's login limits, the sign-up cap), so nothing is keyed on the gateway's own address.
type Limits interface {
	Listener(ln net.Listener, name string, logf func(format string, a ...any)) net.Listener
	AllowTunnelPacket(addr string, n int) bool
	ShouldLog(kind, key string) bool
}

// PacketHandler answers a datagram in process (the QoS ping site): from is the game socket's address on the
// player's PC, reply sends a datagram back to it from the port it went to.
type PacketHandler func(payload []byte, from *Addr, reply func([]byte) error)

// Gateway is the server side of the tunnel (dgserver --tunnel): one TCP and one UDP port. Register the ports with
// Listener, HandleUDP and ForwardUDP before Start.
type Gateway struct {
	Log      *logging.Log
	Identity *Identity
	// BindIP is the address forwarded datagrams leave from (the server's --bind; nil = the system's choice).
	BindIP net.IP
	// Forward is where a forwarded port's datagrams go when the session has no route for it (Addr.Route); nil
	// result = dropped. dgserver: the server's bind address with the same port (its match hosts).
	Forward func(port int) *net.UDPAddr

	HeartbeatEvery, HeartbeatTimeout, Grace, FlowIdle, HandshakeTimeout time.Duration
	MaxFlows                                                            int // per session
	// StatsEvery logs each live session's counts this often (0: DefaultStatsEvery; negative: never)
	StatsEvery time.Duration
	// Limits (nil = none) limit connections and datagrams by the sender's real address; MaxSessionsPerPeer
	// caps the sessions (live or in their grace) one real address may hold (0 = no cap; a session taken up again
	// doesn't count as a new one).
	Limits             Limits
	MaxSessionsPerPeer int

	listeners map[int]*listener
	handlers  map[int]PacketHandler
	forwarded map[int]bool

	tln  net.Listener
	udp  *net.UDPConn
	done chan struct{}
	wg   sync.WaitGroup

	mu       sync.Mutex
	sessions map[uint64]*session
	closed   bool

	unknown, junk atomic.Uint64 // datagrams of no session, datagrams that are no tunnel datagram
	limited       atomic.Uint64 // datagrams refused by Limits
	sessionsCut   atomic.Uint64 // sessions refused by MaxSessionsPerPeer
}

// Listener returns the virtual listener of a server port: the streams clients open to it. The service serves it
// beside its own socket (e.g. blaze.Server.Serve).
func (g *Gateway) Listener(port int) net.Listener {
	if g.listeners == nil {
		g.listeners = map[int]*listener{}
	}
	if l := g.listeners[port]; l != nil {
		return l
	}
	l := newListener(&Addr{IP: net.IPv4(127, 0, 0, 1).To4(), Port: port})
	g.listeners[port] = l
	return l
}

// HandleUDP answers a UDP port's datagrams in process.
func (g *Gateway) HandleUDP(port int, h PacketHandler) {
	if g.handlers == nil {
		g.handlers = map[int]PacketHandler{}
	}
	g.handlers[port] = h
}

// ForwardUDP lets datagrams to these ports through to Forward's address (or a session's route).
func (g *Gateway) ForwardUDP(ports ...int) {
	if g.forwarded == nil {
		g.forwarded = map[int]bool{}
	}
	for _, p := range ports {
		g.forwarded[p] = true
	}
}

func (g *Gateway) defaults() {
	if g.HeartbeatEvery == 0 {
		g.HeartbeatEvery = DefaultHeartbeatEvery
	}
	if g.HeartbeatTimeout == 0 {
		g.HeartbeatTimeout = DefaultHeartbeatTimeout
	}
	if g.Grace == 0 {
		g.Grace = DefaultGrace
	}
	if g.FlowIdle == 0 {
		g.FlowIdle = DefaultFlowIdle
	}
	if g.HandshakeTimeout == 0 {
		g.HandshakeTimeout = DefaultHandshakeTimeout
	}
	if g.MaxFlows == 0 {
		g.MaxFlows = DefaultMaxFlows
	}
	if g.StatsEvery == 0 {
		g.StatsEvery = DefaultStatsEvery
	}
}

// Start listens on addr (host:port) with TCP and UDP and serves both until Close.
func (g *Gateway) Start(addr string) error {
	if g.Identity == nil {
		return errors.New("tunnel: the gateway has no key")
	}
	g.defaults()
	ta, err := net.ResolveTCPAddr("tcp4", addr)
	if err != nil {
		return err
	}
	tln, err := net.ListenTCP("tcp4", ta)
	if err != nil {
		return err
	}
	ua := &net.UDPAddr{IP: ta.IP, Port: tln.Addr().(*net.TCPAddr).Port}
	udp, err := net.ListenUDP("udp4", ua)
	if err != nil {
		tln.Close()
		return err
	}
	var raw net.Listener = tln
	if g.Limits != nil { // per real address, before the TLS handshake
		raw = g.Limits.Listener(tln, "tunnel", g.logf)
	}
	g.tln, g.udp, g.done = tls.NewListener(raw, ServerTLS(g.Identity)), udp, make(chan struct{})
	g.mu.Lock()
	g.sessions = map[uint64]*session{}
	g.mu.Unlock()
	g.wg.Add(3)
	go g.acceptLoop()
	go g.udpLoop()
	go g.janitor()
	return nil
}

// Addr is the gateway's address (host:port; the same port for TCP and UDP).
func (g *Gateway) Addr() string {
	if g.tln == nil {
		return ""
	}
	return g.tln.Addr().String()
}

// Port is the gateway's port.
func (g *Gateway) Port() int {
	if g.tln == nil {
		return 0
	}
	return g.tln.Addr().(*net.TCPAddr).Port
}

// Describe is the gateway's start-up note for the log.
func (g *Gateway) Describe() string {
	tcp, udp := g.ports()
	return fmt.Sprintf("tunnel gateway on %s (TLS 1.3 and UDP), key fingerprint %s; streams to TCP %s, datagrams to UDP %s",
		g.Addr(), g.Identity.Pin.Fingerprint(), portList(tcp), portList(udp))
}

func portList(ps []uint16) string {
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = strconv.Itoa(int(p))
	}
	return strings.Join(s, ",")
}

func (g *Gateway) ports() (tcp, udp []uint16) {
	for p := range g.listeners {
		tcp = append(tcp, uint16(p))
	}
	for p := range g.handlers {
		udp = append(udp, uint16(p))
	}
	for p := range g.forwarded {
		if g.handlers[p] == nil {
			udp = append(udp, uint16(p))
		}
	}
	slices.Sort(tcp)
	slices.Sort(udp)
	return tcp, udp
}

// Sessions returns the number of live sessions.
func (g *Gateway) Sessions() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sessions)
}

// Close stops the gateway: its sockets, every session and flow, and the virtual listeners.
func (g *Gateway) Close() {
	g.mu.Lock()
	if g.closed || g.done == nil {
		g.closed = true
		g.mu.Unlock()
		return
	}
	g.closed = true
	close(g.done)
	ss := make([]*session, 0, len(g.sessions))
	for _, s := range g.sessions {
		ss = append(ss, s)
	}
	g.sessions = map[uint64]*session{}
	g.mu.Unlock()
	g.tln.Close()
	g.udp.Close()
	for _, l := range g.listeners {
		l.Close()
	}
	for _, s := range ss {
		s.end("gateway closed")
	}
	g.wg.Wait()
}

func (g *Gateway) logf(format string, a ...any) {
	if g.Log != nil {
		g.Log.Logf(who, format, a...)
	}
}

func (g *Gateway) acceptLoop() {
	defer g.wg.Done()
	for {
		c, err := g.tln.Accept()
		if err != nil {
			select {
			case <-g.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go g.serveConn(c.(*tls.Conn))
	}
}

func (g *Gateway) serveConn(c *tls.Conn) {
	peer := logging.Peer(c.RemoteAddr())
	c.SetDeadline(time.Now().Add(g.HandshakeTimeout))
	if err := c.Handshake(); err != nil {
		g.logf("%s: TLS handshake failed: %v", peer, err)
		c.Close()
		return
	}
	kind, err := readHello(c)
	switch {
	case errors.Is(err, errVersion):
		c.Write([]byte{StatusVersion})
		g.logf("%s: tunnel version not supported", peer)
		c.Close()
		return
	case err != nil:
		if errors.Is(err, io.EOF) {
			g.logf("%s: closed after the TLS handshake (a key probe, Start-Client's first contact)", peer)
		} else {
			g.logf("%s: no tunnel hello: %v", peer, err)
		}
		c.Close()
		return
	}
	switch kind {
	case kindSession:
		g.serveSession(c, peer)
	case kindStream:
		g.serveStream(c, peer)
	default:
		g.logf("%s: unknown hello kind %d", peer, kind)
		c.Close()
	}
}

// sessionsOf counts the sessions whose control connection came from peer (g.mu held).
func (g *Gateway) sessionsOf(peer netip.Addr) int {
	n := 0
	for _, s := range g.sessions {
		if s.peerIP == peer {
			n++
		}
	}
	return n
}

func randomID() (uint64, [16]byte) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	var tok [16]byte
	copy(tok[:], b[8:])
	return binary.BigEndian.Uint64(b[:8]), tok
}

// serveSession runs a control connection: a new session or one taken up again, its keys, then the heartbeats.
func (g *Gateway) serveSession(c *tls.Conn, peer string) {
	defer c.Close()
	h, err := readSessionHello(c)
	if err != nil {
		g.logf("%s: session hello: %v", peer, err)
		return
	}
	ip := ipOf(h.tunnelIP)
	if !ip.IsLoopback() {
		c.Write([]byte{StatusAddress})
		g.logf("%s: session refused: tunnel address %s is not a loopback address", peer, ip)
		return
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		c.Write([]byte{StatusUnavailable})
		return
	}
	var s *session
	resumed := false
	peerIP := PeerOf(c.RemoteAddr())
	if old := g.sessions[h.resumeID]; h.resumeID != 0 && old != nil && subtle.ConstantTimeCompare(old.token[:], h.resumeToken[:]) == 1 {
		s, resumed = old, true
	} else if n := g.sessionsOf(peerIP); g.MaxSessionsPerPeer > 0 && n >= g.MaxSessionsPerPeer {
		g.mu.Unlock()
		g.sessionsCut.Add(1)
		c.Write([]byte{StatusUnavailable})
		if g.Limits == nil || g.Limits.ShouldLog("tunnel sessions", peerIP.String()) {
			g.logf("limits: session from %s refused: %d sessions of that address already (at most %d; further refusals "+
				"counted in the stats lines)", peer, n, g.MaxSessionsPerPeer)
		}
		return
	} else {
		var id uint64
		var tok [16]byte
		for id, tok = randomID(); id == 0 || g.sessions[id] != nil; id, tok = randomID() {
		}
		s = &session{g: g, id: id, token: tok, peer: peer, peerIP: peerIP, created: time.Now(),
			flows: map[flowKey]*flow{}, routes: map[int]*net.UDPAddr{}}
		g.sessions[id] = s
	}
	g.mu.Unlock()
	keys, err := DeriveKeys(c.ConnectionState(), s.id)
	if err == nil {
		err = s.attach(c, keys, ip)
	}
	if err != nil {
		g.logf("%s: session %d: %v", peer, s.id, err)
		return
	}
	tcp, udp := g.ports()
	rep := sessionReply{status: StatusOK, session: s.id, token: s.token, resumed: resumed, maxPayload: MaxPayload, tcp: tcp, udp: udp}
	if _, err := c.Write(rep.encode()); err != nil {
		s.detach(c, err)
		return
	}
	what := "new"
	if resumed {
		what = "taken up again, new UDP keys"
	}
	g.logf("session %d from %s (%s): tunnel address %s", s.id, peer, what, ip)
	err = heartbeat(c, g.HeartbeatEvery, g.HeartbeatTimeout, g.done)
	s.detach(c, err)
}

// heartbeat sends one byte every every and reads the other side's; it returns when a read fails or times out, or
// when stop is closed.
func heartbeat(c net.Conn, every, timeout time.Duration, stop <-chan struct{}) error {
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.SetWriteDeadline(time.Now().Add(timeout))
				if _, err := c.Write([]byte{0}); err != nil {
					c.Close()
					return
				}
			case <-quit:
				return
			case <-stop:
				c.Close()
				return
			}
		}
	}()
	var b [64]byte
	for {
		c.SetReadDeadline(time.Now().Add(timeout))
		if _, err := c.Read(b[:]); err != nil {
			return err
		}
	}
}

// serveStream hands a stream to its port's listener.
func (g *Gateway) serveStream(c *tls.Conn, peer string) {
	h, err := readStreamHello(c)
	if err != nil {
		g.logf("%s: stream hello: %v", peer, err)
		c.Close()
		return
	}
	g.mu.Lock()
	s := g.sessions[h.session]
	g.mu.Unlock()
	if s == nil || subtle.ConstantTimeCompare(s.token[:], h.token[:]) != 1 {
		c.Write([]byte{StatusNoSession})
		g.logf("%s: stream refused: unknown session", peer)
		c.Close()
		return
	}
	port := int(h.port)
	l := g.listeners[port]
	if l == nil {
		c.Write([]byte{StatusPort})
		g.logf("session %d: stream to port %d refused (not served)", s.id, port)
		c.Close()
		return
	}
	if _, err := c.Write([]byte{StatusOK}); err != nil {
		c.Close()
		return
	}
	c.SetDeadline(time.Time{})
	s.mu.Lock()
	tip := s.tunnelIP
	s.mu.Unlock()
	s.streams.Add(1)
	peerIP := PeerOf(c.RemoteAddr()) // the player's real address (the sign-up cap, the limits)
	sc := &streamConn{Conn: c,
		local:  &Addr{IP: tip, Port: port, Session: s.id, Peer: peerIP, sess: s},
		remote: &Addr{IP: ipOf(h.clientIP), Port: int(h.clientPort), Session: s.id, Peer: peerIP, sess: s}}
	if !l.push(sc) {
		c.Close()
	}
}

func (g *Gateway) udpLoop() {
	defer g.wg.Done()
	buf := make([]byte, 65536)
	for {
		n, from, err := g.udp.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-g.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue // Windows: an ICMP port unreachable for an earlier send (WSAECONNRESET); the socket stays usable
		}
		if g.Limits != nil { // the sender's real address, before the session lookup and the decryption
			if ip := from.IP.String(); !g.Limits.AllowTunnelPacket(ip, n) {
				g.limited.Add(1)
				if g.Limits.ShouldLog("udp tunnel", ip) {
					g.logf("limits: udp from %s over its rate; dropped (further drops counted in the minute's limits line)", ip)
				}
				continue
			}
		}
		id, ok := SessionOf(buf[:n])
		if !ok {
			g.junk.Add(1)
			continue
		}
		g.mu.Lock()
		s := g.sessions[id]
		g.mu.Unlock()
		if s == nil {
			g.unknown.Add(1)
			continue
		}
		s.receive(append([]byte(nil), buf[:n]...), from)
	}
}

func (g *Gateway) janitor() {
	defer g.wg.Done()
	t := time.NewTicker(min(g.Grace, g.FlowIdle) / 4)
	defer t.Stop()
	var stats <-chan time.Time
	if g.StatsEvery > 0 {
		st := time.NewTicker(g.StatsEvery)
		defer st.Stop()
		stats = st.C
	}
	for {
		select {
		case <-g.done:
			return
		case <-stats:
			for _, line := range g.Report() {
				g.logf("stats %s", line)
			}
		case now := <-t.C:
			g.mu.Lock()
			var ended []*session
			for id, s := range g.sessions {
				s.mu.Lock()
				lost := s.control == nil && !s.lost.IsZero() && now.Sub(s.lost) > g.Grace
				s.mu.Unlock()
				if lost {
					delete(g.sessions, id)
					ended = append(ended, s)
				}
			}
			all := make([]*session, 0, len(g.sessions))
			for _, s := range g.sessions {
				all = append(all, s)
			}
			g.mu.Unlock()
			for _, s := range ended {
				s.end(fmt.Sprintf("no control connection for %s", g.Grace))
			}
			for _, s := range all {
				s.closeIdleFlows(now)
			}
		}
	}
}

// session is one client's tunnel on the gateway.
type session struct {
	g       *Gateway
	id      uint64
	token   [16]byte
	peer    string
	peerIP  netip.Addr // the control connection's real source address (MaxSessionsPerPeer)
	created time.Time

	mu       sync.Mutex
	tunnelIP net.IP
	up       *Opener
	down     *Sealer
	endpoint *net.UDPAddr
	control  net.Conn
	lost     time.Time
	flows    map[flowKey]*flow
	routes   map[int]*net.UDPAddr
	ended    bool

	upN, upBytes, downN, downBytes, streams       atomic.Uint64
	dropAuth, dropReplay, dropBig, dropRoute, eps atomic.Uint64
}

type flowKey struct {
	ip         [4]byte
	clientPort uint16
	port       uint16
}

// flow is one game socket's datagrams to one forwarded port: a socket of the server machine's own.
type flow struct {
	key    flowKey
	target *net.UDPAddr
	conn   *net.UDPConn
	last   atomic.Int64 // unix nanoseconds of the last datagram either way
}

func (s *session) attach(c net.Conn, k Keys, ip net.IP) error {
	up, err := NewOpener(k.Up, s.id)
	if err != nil {
		return err
	}
	down, err := NewSealer(k.Down, s.id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	old := s.control
	s.control, s.lost, s.up, s.down, s.tunnelIP = c, time.Time{}, up, down, ip
	s.mu.Unlock()
	if old != nil && old != c {
		old.Close()
	}
	return nil
}

func (s *session) detach(c net.Conn, err error) {
	s.mu.Lock()
	current := s.control == c
	if current {
		s.control, s.lost = nil, time.Now()
	}
	ended := s.ended
	s.mu.Unlock()
	if current && !ended {
		reason := "closed"
		if err != nil && !errors.Is(err, io.EOF) {
			reason = err.Error()
		}
		s.g.logf("session %d: control connection lost (%s); kept for %s", s.id, reason, s.g.Grace)
	}
}

func (s *session) route(port int, target *net.UDPAddr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[port] = target
}

// end closes the session's flows and logs its counts.
func (s *session) end(reason string) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	ctrl := s.control
	flows := s.flows
	s.flows = map[flowKey]*flow{}
	s.mu.Unlock()
	if ctrl != nil {
		ctrl.Close()
	}
	for _, f := range flows {
		f.conn.Close()
	}
	s.g.logf("session %d ended (%s) after %s: %s", s.id, reason, time.Since(s.created).Round(time.Second), s.counts())
}

func (s *session) counts() string {
	return fmt.Sprintf("up %d datagrams (%d bytes), down %d (%d bytes), %d streams, %d UDP address(es); dropped: auth %d, replay %d, too big %d, no route %d",
		s.upN.Load(), s.upBytes.Load(), s.downN.Load(), s.downBytes.Load(), s.streams.Load(), s.eps.Load(),
		s.dropAuth.Load(), s.dropReplay.Load(), s.dropBig.Load(), s.dropRoute.Load())
}

func (s *session) receive(pkt []byte, from *net.UDPAddr) {
	s.mu.Lock()
	up := s.up
	s.mu.Unlock()
	if up == nil {
		return
	}
	in, payload, err := up.Open(pkt)
	switch {
	case errors.Is(err, ErrReplay):
		s.dropReplay.Add(1)
		return
	case err != nil:
		s.dropAuth.Add(1)
		return
	}
	s.mu.Lock()
	if s.endpoint == nil || !s.endpoint.IP.Equal(from.IP) || s.endpoint.Port != from.Port { // a NAT may map it anew
		s.endpoint = &net.UDPAddr{IP: append(net.IP(nil), from.IP...), Port: from.Port}
		s.eps.Add(1)
		if s.eps.Load() <= 4 {
			s.g.logf("session %d: UDP from %s", s.id, logging.Peer(from))
		}
	}
	s.mu.Unlock()
	if in.Kind == KindKeepalive {
		s.send(Inner{Kind: KindKeepalive}, nil)
		return
	}
	s.upN.Add(1)
	s.upBytes.Add(uint64(len(payload)))
	s.dispatch(in, payload, PeerOf(from))
}

// dispatch hands a datagram to its port's handler or forwards it; peer is the sender's real address.
func (s *session) dispatch(in Inner, payload []byte, peer netip.Addr) {
	port := int(in.Port)
	if h := s.g.handlers[port]; h != nil {
		from := &Addr{IP: ipOf(in.IP), Port: int(in.ClientPort), Session: s.id, Peer: peer, sess: s}
		h(payload, from, func(b []byte) error {
			return s.send(Inner{Kind: KindData, Port: in.Port, IP: in.IP, ClientPort: in.ClientPort}, b)
		})
		return
	}
	f := s.flowFor(flowKey{ip: in.IP, clientPort: in.ClientPort, port: in.Port})
	if f == nil {
		s.dropRoute.Add(1)
		return
	}
	f.last.Store(time.Now().UnixNano())
	f.conn.Write(payload)
}

// flowFor returns the flow of key, made when needed; nil when the port has no target or the session has too many.
func (s *session) flowFor(key flowKey) *flow {
	port := int(key.port)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return nil
	}
	target := s.routes[port]
	if target == nil && s.g.forwarded[port] && s.g.Forward != nil {
		target = s.g.Forward(port)
	}
	if target == nil {
		return nil
	}
	if f := s.flows[key]; f != nil {
		if f.target.IP.Equal(target.IP) && f.target.Port == target.Port {
			return f
		}
		f.conn.Close() // the port's match moved (a new route)
		delete(s.flows, key)
	}
	if len(s.flows) >= s.g.MaxFlows {
		return nil
	}
	var laddr *net.UDPAddr
	if s.g.BindIP != nil {
		laddr = &net.UDPAddr{IP: s.g.BindIP}
	}
	conn, err := net.DialUDP("udp4", laddr, target)
	if err != nil {
		s.g.logf("session %d: no socket to %s: %v", s.id, target, err)
		return nil
	}
	f := &flow{key: key, target: target, conn: conn}
	f.last.Store(time.Now().UnixNano())
	s.flows[key] = f
	go s.readFlow(f)
	return f
}

func (s *session) readFlow(f *flow) {
	buf := make([]byte, 65536)
	for {
		n, err := f.conn.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue // WSAECONNRESET after an ICMP port unreachable: the host isn't (yet) there
		}
		f.last.Store(time.Now().UnixNano())
		s.send(Inner{Kind: KindData, Port: f.key.port, IP: f.key.ip, ClientPort: f.key.clientPort}, buf[:n])
	}
}

func (s *session) closeIdleFlows(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, f := range s.flows {
		if now.Sub(time.Unix(0, f.last.Load())) > s.g.FlowIdle {
			f.conn.Close()
			delete(s.flows, k)
		}
	}
}

// send seals a datagram to the client's last UDP address.
func (s *session) send(in Inner, payload []byte) error {
	s.mu.Lock()
	down, ep := s.down, s.endpoint
	s.mu.Unlock()
	if down == nil || ep == nil {
		return errors.New("tunnel: no UDP address of the client yet")
	}
	pkt, err := down.Seal(in, payload)
	if err != nil {
		s.dropBig.Add(1)
		return err
	}
	if _, err := s.g.udp.WriteToUDP(pkt, ep); err != nil {
		return err
	}
	if in.Kind == KindData {
		s.downN.Add(1)
		s.downBytes.Add(uint64(len(payload)))
	}
	return nil
}

// Report is a line per live session (counts), for a status page or the log.
func (g *Gateway) Report() []string {
	g.mu.Lock()
	ss := make([]*session, 0, len(g.sessions))
	for _, s := range g.sessions {
		ss = append(ss, s)
	}
	g.mu.Unlock()
	sort.Slice(ss, func(i, j int) bool { return ss[i].created.Before(ss[j].created) })
	var out []string
	for _, s := range ss {
		out = append(out, fmt.Sprintf("session %d (%s): %s", s.id, s.peer, s.counts()))
	}
	if n, j := g.unknown.Load(), g.junk.Load(); n+j > 0 {
		out = append(out, fmt.Sprintf("datagrams of no session %d, not tunnel datagrams %d", n, j))
	}
	if l, c := g.limited.Load(), g.sessionsCut.Load(); l+c > 0 {
		out = append(out, fmt.Sprintf("refused by the limits (PH6b): datagrams %d, sessions %d (connections: the limits line)", l, c))
	}
	return out
}
