package tunnel

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"dawngate/server/internal/logging"
	build "dawngate/server/internal/version"
)

// RefusedError is a gateway's refusal of a session (its status: StatusVersion, StatusRelease, StatusAddress, ...).
type RefusedError struct {
	Gateway string
	Status  byte
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("the tunnel gateway at %s refused the session: %s", e.Gateway, StatusText(e.Status))
}

// ReleaseRefused reports whether err is a gateway's refusal of this Start-Client's release: another release than the
// server's (StatusRelease), or a session hello the gateway doesn't take (StatusVersion: a gateway of a release before
// the release check, or this hello's version unknown to it).
func ReleaseRefused(err error) bool {
	var r *RefusedError
	return errors.As(err, &r) && (r.Status == StatusRelease || r.Status == StatusVersion)
}

// RedirectorPort is the redirector's port, compiled into the client: the one TCP port whose TLS 1.1 the client side
// of the tunnel ends itself (Client.RedirectorTLS).
const RedirectorPort = 42127

// DefaultKeepalive is how often the client sends a keepalive datagram (NAT mappings of home routers last 30 s and
// more for UDP).
const DefaultKeepalive = 15 * time.Second

// Client is the player's side of the tunnel (Start-Client.exe; dgserver --tunnel-client for the test lanes): it
// listens on TunnelIP at every port the gateway names and carries the game's connections and datagrams to the
// gateway.
type Client struct {
	Log *logging.Log // nil: no log
	// Gateway is the gateway's address (host:port, TCP and UDP).
	Gateway string
	// Pin is the gateway key's pin; a gateway with another key is refused.
	Pin Pin
	// TunnelIP is the loopback address the player copy is patched for (the local profile: 127.0.0.1).
	TunnelIP net.IP
	// DialIP is the local address of the tunnel's own sockets to the gateway (nil: the system's choice; the test
	// lanes name their own loopback address).
	DialIP net.IP
	// RedirectorTLS ends the game's TLS on RedirectorPort here (the redirector's certificate set of this PC, CN =
	// TunnelIP); nil passes the bytes through (then the server's redirector speaks TLS, which only works when its
	// certificate is made for TunnelIP).
	RedirectorTLS *tls.Config
	// RedirectorPort is the port whose TLS ends here (0: RedirectorPort, the client's compiled-in 42127).
	RedirectorPort int
	// External leaves the game's sockets to the caller (Start-Client's front, which keeps them while it switches
	// between servers): Start opens the session and the UDP channel but listens nowhere; the caller opens streams
	// with OpenStream and sends datagrams with SendDatagram, and Deliver gets the gateway's datagrams.
	External bool
	// Deliver takes a datagram from the gateway in External mode: port is the server port it comes from, dst the
	// game socket on this PC it is for (always a loopback address).
	Deliver func(port int, dst *net.UDPAddr, payload []byte)
	// Release is the release name the session hello names ("" at Start: version.String(), this build's). The
	// gateway lets in only its own release (unless it allows any); Start-Client's front names the server's own for
	// This PC, which is never refused.
	Release string

	HeartbeatEvery, HeartbeatTimeout, Keepalive, DialTimeout time.Duration

	tlsCfg *tls.Config
	gwUDP  *net.UDPAddr
	udp    *net.UDPConn
	tcpLns map[int]net.Listener
	udpLns map[int]*net.UDPConn
	tcp    []int
	udpP   []int
	done   chan struct{}
	wg     sync.WaitGroup

	mu        sync.Mutex
	active    map[net.Conn]struct{} // the streams' sockets, closed by Close
	sess      *clientSession
	sessReady chan struct{} // closed while a session is up
	closed    bool

	udpEcho                                            chan struct{}
	echoOnce                                           sync.Once
	upN, upBytes, downN, downBytes, streams            atomic.Uint64
	dropBig, dropAuth, dropPort, dropNotLocal, trimmed atomic.Uint64
	sessions                                           atomic.Uint64
	streamFails                                        atomic.Uint64
	firstEcho                                          atomic.Int64
	started                                            time.Time
	lastSessionID                                      atomic.Uint64
	resumes                                            atomic.Uint64
}

type clientSession struct {
	id    uint64
	token [16]byte
	up    *Sealer
	down  *Opener
	ctrl  *tls.Conn
}

func (c *Client) logf(format string, a ...any) {
	if c.Log != nil {
		c.Log.Logf(who, format, a...)
	}
}

// Start opens the control session, the listeners on TunnelIP and the UDP channel, and checks that UDP reaches the
// gateway (Start does not fail without the echo; UDPWorks says whether it came).
func (c *Client) Start() error {
	if c.TunnelIP == nil {
		c.TunnelIP = net.IPv4(127, 0, 0, 1)
	}
	c.TunnelIP = c.TunnelIP.To4()
	if c.TunnelIP == nil || !c.TunnelIP.IsLoopback() {
		return fmt.Errorf("the tunnel address must be an IPv4 loopback address")
	}
	if c.HeartbeatEvery == 0 {
		c.HeartbeatEvery = DefaultHeartbeatEvery
	}
	if c.HeartbeatTimeout == 0 {
		c.HeartbeatTimeout = DefaultHeartbeatTimeout
	}
	if c.Keepalive == 0 {
		c.Keepalive = DefaultKeepalive
	}
	if c.RedirectorPort == 0 {
		c.RedirectorPort = RedirectorPort
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = 10 * time.Second
	}
	if c.Release == "" {
		c.Release = build.String()
	}
	c.started = time.Now()
	c.tlsCfg = ClientTLS(c.Pin, tls.NewLRUClientSessionCache(64))
	c.done, c.udpEcho, c.sessReady = make(chan struct{}), make(chan struct{}), make(chan struct{})
	ua, err := net.ResolveUDPAddr("udp4", c.Gateway)
	if err != nil {
		return err
	}
	c.gwUDP = ua
	if c.DialIP == nil && ua.IP.IsLoopback() {
		// a gateway on this PC (the test lanes): leave from the tunnel's own address, not 127.0.0.1, which Windows
		// picks for an unbound socket to any loopback address (and which is lane a's)
		c.DialIP = c.TunnelIP
	}
	s, rep, err := c.connect(0, [16]byte{})
	if err != nil {
		return err
	}
	for _, p := range rep.tcp {
		c.tcp = append(c.tcp, int(p))
	}
	for _, p := range rep.udp {
		c.udpP = append(c.udpP, int(p))
	}
	var laddr *net.UDPAddr
	if c.DialIP != nil {
		laddr = &net.UDPAddr{IP: c.DialIP}
	}
	if c.udp, err = net.ListenUDP("udp4", laddr); err != nil {
		s.ctrl.Close()
		return err
	}
	if c.External {
		c.tcpLns, c.udpLns = map[int]net.Listener{}, map[int]*net.UDPConn{}
	} else if err := c.listen(); err != nil {
		c.closeSockets()
		s.ctrl.Close()
		return err
	}
	c.setSession(s)
	where := "listening on " + c.TunnelIP.String()
	if c.External {
		where = "for the game's sockets on " + c.TunnelIP.String()
	}
	c.logf("session %d to %s (key %s): %s, TCP %s, UDP %s", s.id, c.Gateway, c.Pin.Fingerprint(), where,
		intList(c.tcp), intList(c.udpP))
	c.wg.Add(2)
	go c.controlLoop(s)
	go c.gatewayLoop()
	for p, ln := range c.tcpLns {
		c.wg.Add(1)
		go c.acceptLoop(p, ln)
	}
	for p, ln := range c.udpLns {
		c.wg.Add(1)
		go c.udpListenerLoop(p, ln)
	}
	c.wg.Add(1)
	go c.keepaliveLoop()
	return nil
}

func intList(ps []int) string {
	u := make([]uint16, len(ps))
	for i, p := range ps {
		u[i] = uint16(p)
	}
	return portList(u)
}

// Ports returns the TCP and UDP ports the client listens on.
func (c *Client) Ports() (tcp, udp []int) { return c.tcp, c.udpP }

// WaitUDP waits up to d for the gateway's echo of a keepalive and says whether it came: whether UDP reaches the
// gateway (a router that forwards only the gateway's TCP port fails here; the matches would then fail).
func (c *Client) WaitUDP(d time.Duration) bool {
	select {
	case <-c.udpEcho:
		return true
	case <-time.After(d):
		return false
	}
}

// UDPEchoAfter is how long after Start the first keepalive echo came (0: none yet).
func (c *Client) UDPEchoAfter() time.Duration {
	t := c.firstEcho.Load()
	if t == 0 {
		return 0
	}
	return time.Duration(t)
}

func (c *Client) dial(network, addr string) (*tls.Conn, error) {
	d := &net.Dialer{Timeout: c.DialTimeout}
	if c.DialIP != nil {
		d.LocalAddr = &net.TCPAddr{IP: c.DialIP}
	}
	return tls.DialWithDialer(d, network, addr, c.tlsCfg)
}

// connect opens a control session (resume: the last one's id and token).
func (c *Client) connect(resumeID uint64, resumeToken [16]byte) (*clientSession, sessionReply, error) {
	var rep sessionReply
	conn, err := c.dial("tcp4", c.Gateway)
	if err != nil {
		return nil, rep, fmt.Errorf("the tunnel gateway at %s: %w", c.Gateway, err)
	}
	conn.SetDeadline(time.Now().Add(c.DialTimeout))
	h := sessionHello{tunnelIP: ip4(c.TunnelIP), resumeID: resumeID, resumeToken: resumeToken, release: c.Release}
	if _, err := conn.Write(h.encode()); err != nil {
		conn.Close()
		return nil, rep, err
	}
	if rep, err = readSessionReply(conn); err != nil {
		conn.Close()
		return nil, rep, fmt.Errorf("the tunnel gateway at %s: %w", c.Gateway, err)
	}
	if rep.status != StatusOK {
		conn.Close()
		return nil, rep, &RefusedError{Gateway: c.Gateway, Status: rep.status}
	}
	conn.SetDeadline(time.Time{})
	keys, err := DeriveKeys(conn.ConnectionState(), rep.session)
	if err != nil {
		conn.Close()
		return nil, rep, err
	}
	up, err := NewSealer(keys.Up, rep.session)
	if err != nil {
		conn.Close()
		return nil, rep, err
	}
	down, err := NewOpener(keys.Down, rep.session)
	if err != nil {
		conn.Close()
		return nil, rep, err
	}
	c.sessions.Add(1)
	if rep.resumed {
		c.resumes.Add(1)
	}
	c.lastSessionID.Store(rep.session)
	return &clientSession{id: rep.session, token: rep.token, up: up, down: down, ctrl: conn}, rep, nil
}

func (c *Client) listen() error {
	c.tcpLns, c.udpLns = map[int]net.Listener{}, map[int]*net.UDPConn{}
	for _, p := range c.tcp {
		ln, err := net.Listen("tcp4", net.JoinHostPort(c.TunnelIP.String(), strconv.Itoa(p)))
		if err != nil {
			return fmt.Errorf("TCP port %d on %s is in use (a Dawngate server or another tunnel on this PC?): %w", p, c.TunnelIP, err)
		}
		c.tcpLns[p] = ln
	}
	for _, p := range c.udpP {
		ln, err := net.ListenUDP("udp4", &net.UDPAddr{IP: c.TunnelIP, Port: p})
		if err != nil {
			return fmt.Errorf("UDP port %d on %s is in use (a Dawngate server or another tunnel on this PC?): %w", p, c.TunnelIP, err)
		}
		c.udpLns[p] = ln
	}
	return nil
}

func (c *Client) closeSockets() {
	for _, ln := range c.tcpLns {
		ln.Close()
	}
	for _, ln := range c.udpLns {
		ln.Close()
	}
	if c.udp != nil {
		c.udp.Close()
	}
}

func (c *Client) setSession(s *clientSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sess = s
	if s != nil {
		select {
		case <-c.sessReady:
		default:
			close(c.sessReady)
		}
	} else {
		select {
		case <-c.sessReady:
			c.sessReady = make(chan struct{})
		default:
		}
	}
}

// session returns the current session, waiting up to d for one while the control connection is being made again.
func (c *Client) session(d time.Duration) *clientSession {
	c.mu.Lock()
	s, ready := c.sess, c.sessReady
	c.mu.Unlock()
	if s != nil {
		return s
	}
	select {
	case <-ready:
	case <-time.After(d):
	case <-c.done:
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess
}

// Close stops the tunnel: the listeners, the control session and every stream's sockets; it logs the counts.
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed || c.done == nil {
		c.closed = true
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.done)
	s := c.sess
	c.mu.Unlock()
	c.closeSockets()
	if s != nil {
		s.ctrl.Close()
	}
	c.mu.Lock()
	for conn := range c.active {
		conn.Close()
	}
	c.mu.Unlock()
	c.wg.Wait()
	c.logf("closed after %s: %s", time.Since(c.started).Round(time.Second), c.Counts())
}

// Counts describes the client's traffic so far.
func (c *Client) Counts() string {
	return fmt.Sprintf("up %d datagrams (%d bytes), down %d (%d bytes), %d streams (%d failed), %d session(s) (%d taken up again); RakNet MTU probes cut to 1200's %d; dropped: too big %d, auth %d, unknown port %d, not loopback %d",
		c.upN.Load(), c.upBytes.Load(), c.downN.Load(), c.downBytes.Load(), c.streams.Load(), c.streamFails.Load(),
		c.sessions.Load(), c.resumes.Load(), c.trimmed.Load(), c.dropBig.Load(), c.dropAuth.Load(), c.dropPort.Load(), c.dropNotLocal.Load())
}

// controlLoop keeps the control connection: heartbeats, and when it fails a new one that takes the session up again.
func (c *Client) controlLoop(s *clientSession) {
	defer c.wg.Done()
	for {
		err := heartbeat(s.ctrl, c.HeartbeatEvery, c.HeartbeatTimeout, c.done)
		s.ctrl.Close()
		select {
		case <-c.done:
			return
		default:
		}
		c.logf("session %d: control connection lost (%v); connecting again", s.id, err)
		c.setSession(nil)
		wait := time.Second
		for {
			ns, _, err := c.connect(s.id, s.token)
			if err == nil {
				what := "taken up again"
				if ns.id != s.id {
					what = fmt.Sprintf("a new session (%d ended on the gateway)", s.id)
				}
				c.logf("session %d: %s", ns.id, what)
				s = ns
				c.setSession(s)
				c.sendKeepalive()
				break
			}
			c.logf("connecting again: %v (next try in %s)", err, wait)
			select {
			case <-c.done:
				return
			case <-time.After(wait):
			}
			wait = min(2*wait, 10*time.Second)
		}
	}
}

func (c *Client) keepaliveLoop() {
	defer c.wg.Done()
	c.sendKeepalive()
	t := time.NewTicker(c.Keepalive)
	defer t.Stop()
	fast := time.NewTicker(500 * time.Millisecond) // until the first echo
	defer fast.Stop()
	stats := time.NewTicker(DefaultStatsEvery)
	defer stats.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-stats.C:
			c.logf("stats: %s", c.Counts())
		case <-t.C:
			c.sendKeepalive()
		case <-fast.C:
			select {
			case <-c.udpEcho:
				fast.Stop()
			default:
				if time.Since(c.started) < 5*time.Second {
					c.sendKeepalive()
				}
			}
		}
	}
}

func (c *Client) sendKeepalive() {
	s := c.session(0)
	if s == nil {
		return
	}
	if pkt, err := s.up.Seal(Inner{Kind: KindKeepalive}, nil); err == nil {
		c.udp.WriteToUDP(pkt, c.gwUDP)
	}
}

// gatewayLoop delivers the gateway's datagrams to the game's sockets, from the port each went to.
func (c *Client) gatewayLoop() {
	defer c.wg.Done()
	buf := make([]byte, 65536)
	for {
		n, from, err := c.udp.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-c.done:
				return
			default:
			}
			continue
		}
		if !from.IP.Equal(c.gwUDP.IP) || from.Port != c.gwUDP.Port {
			continue
		}
		s := c.session(0)
		if s == nil {
			continue
		}
		in, payload, err := s.down.Open(buf[:n])
		if err != nil {
			c.dropAuth.Add(1)
			continue
		}
		if in.Kind == KindKeepalive {
			c.echoOnce.Do(func() {
				c.firstEcho.Store(int64(time.Since(c.started)))
				close(c.udpEcho)
			})
			continue
		}
		ln := c.udpLns[int(in.Port)]
		if ln == nil && !(c.External && slices.Contains(c.udpP, int(in.Port))) {
			c.dropPort.Add(1)
			continue
		}
		dst := in.Client()
		if !dst.IP.IsLoopback() { // the server can't make the tunnel send anywhere but to this PC's own sockets
			c.dropNotLocal.Add(1)
			continue
		}
		c.downN.Add(1)
		c.downBytes.Add(uint64(len(payload)))
		if c.External {
			if c.Deliver != nil {
				c.Deliver(int(in.Port), dst, payload)
			}
			continue
		}
		ln.WriteToUDP(payload, dst)
	}
}

// udpListenerLoop carries the game's datagrams to one UDP port through the channel.
func (c *Client) udpListenerLoop(port int, ln *net.UDPConn) {
	defer c.wg.Done()
	buf := make([]byte, 65536)
	for {
		n, src, err := ln.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-c.done:
				return
			default:
			}
			continue
		}
		c.SendDatagram(port, src, buf[:n])
	}
}

// SendDatagram carries one of the game's datagrams to a server port through the channel: src is the game socket
// it came from. A datagram over MaxPayload is dropped, except RakNet's MTU probe, which is cut to 1200's
// (TrimMTUProbe); so is one while the control connection is being made again. It says whether it was sent.
func (c *Client) SendDatagram(port int, src *net.UDPAddr, data []byte) bool {
	if len(data) > MaxPayload {
		var ok bool
		if data, ok = TrimMTUProbe(data); !ok {
			c.dropBig.Add(1)
			return false
		}
		c.trimmed.Add(1) // RakNet's 1492-byte MTU probe, cut to 1200's (raknet.go)
	}
	s := c.session(0)
	if s == nil {
		return false
	}
	pkt, err := s.up.Seal(InnerFor(KindData, port, src), data)
	if err != nil {
		return false
	}
	c.upN.Add(1)
	c.upBytes.Add(uint64(len(data)))
	_, err = c.udp.WriteToUDP(pkt, c.gwUDP)
	return err == nil
}

// Up says whether the client has a session (false while the control connection is being made again, and after
// Close).
func (c *Client) Up() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.sess != nil
}

func (c *Client) acceptLoop(port int, ln net.Listener) {
	defer c.wg.Done()
	for {
		lc, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-c.done:
				return
			default:
			}
			continue
		}
		if !c.track(lc, true) {
			lc.Close()
			return
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			defer c.track(lc, false)
			c.serveStream(port, lc)
		}()
	}
}

// track adds a stream's socket to the ones Close closes (false: the client is closed), or removes it.
func (c *Client) track(conn net.Conn, add bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !add {
		delete(c.active, conn)
		return true
	}
	if c.closed {
		return false
	}
	if c.active == nil {
		c.active = map[net.Conn]struct{}{}
	}
	c.active[conn] = struct{}{}
	return true
}

// serveStream carries one of the game's TCP connections as a stream.
func (c *Client) serveStream(port int, lc net.Conn) {
	src, _ := lc.RemoteAddr().(*net.TCPAddr)
	gc, err := c.OpenStream(port, src)
	if err != nil {
		lc.Close()
		return
	}
	defer gc.Close()
	var local net.Conn = lc
	if port == c.RedirectorPort && c.RedirectorTLS != nil {
		tl := tls.Server(lc, c.RedirectorTLS)
		tl.SetDeadline(time.Now().Add(60 * time.Second))
		if err := tl.Handshake(); err != nil {
			c.logf("the game's TLS to the redirector failed: %v", err)
			gc.Close()
			lc.Close()
			return
		}
		tl.SetDeadline(time.Time{})
		local = tl
	}
	Pipe(local, gc)
}

// errClosed: the client is closed.
var errClosed = errors.New("the tunnel is closed")

// OpenStream opens a stream to a server port for a game socket (src, its address on this PC), waiting up to 10 s
// for a session while the control connection is being made again; the stream's bytes are the port's. Close closes
// it; so does the client's Close.
func (c *Client) OpenStream(port int, src *net.TCPAddr) (net.Conn, error) {
	s := c.session(10 * time.Second)
	if s == nil || src == nil {
		c.streamFails.Add(1)
		if src == nil {
			return nil, errors.New("no game socket address")
		}
		return nil, errors.New("no tunnel session")
	}
	gc, err := c.dial("tcp4", c.Gateway)
	if err != nil {
		c.logf("stream to port %d: %v", port, err)
		c.streamFails.Add(1)
		return nil, err
	}
	gc.SetDeadline(time.Now().Add(c.DialTimeout))
	h := streamHello{session: s.id, token: s.token, port: uint16(port), clientIP: ip4(src.IP), clientPort: uint16(src.Port)}
	var st [1]byte
	if _, err = gc.Write(h.encode()); err == nil {
		_, err = io.ReadFull(gc, st[:])
	}
	if err != nil || st[0] != StatusOK {
		if err == nil {
			err = errors.New(StatusText(st[0]))
		}
		c.logf("stream to port %d refused: %v", port, err)
		gc.Close()
		c.streamFails.Add(1)
		return nil, err
	}
	gc.SetDeadline(time.Time{})
	if !c.track(gc, true) {
		gc.Close()
		return nil, errClosed
	}
	c.streams.Add(1)
	return &trackedConn{Conn: gc, c: c}, nil
}

// trackedConn is a stream OpenStream handed out: its Close also removes it from the sockets the client's Close
// closes. CloseWrite passes a half close on (Pipe).
type trackedConn struct {
	*tls.Conn
	c    *Client
	once sync.Once
}

func (t *trackedConn) Close() error {
	t.once.Do(func() { t.c.track(t.Conn, false) })
	return t.Conn.Close()
}

// Pipe copies both ways between a and b, passes a half close on (a TCP FIN or a TLS close_notify) and closes both when
// both directions have ended.
func Pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		if _, err := io.Copy(dst, src); err != nil { // a reset or a close from the other side: no half close to pass on
			a.Close()
			b.Close()
			return
		}
		closeWrite(dst)
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	a.Close()
	b.Close()
}

func closeWrite(c net.Conn) {
	if t, ok := c.(interface{ CloseWrite() error }); ok { // *net.TCPConn, *tls.Conn, a stream of OpenStream
		t.CloseWrite()
		return
	}
	c.Close()
}
