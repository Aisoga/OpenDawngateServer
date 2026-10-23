package tunnel

import (
	"net"
	"net/netip"
	"strconv"
)

// Addr is an address on the player's side of a tunnel, as the gateway's virtual connections and datagrams report
// it: a stream's LocalAddr is the tunnel address and the server port the game connected to (what the server tells
// that client to use), its RemoteAddr the game socket's own address on the player's PC (127.0.0.1:<port>, what the
// local profile's server sees). Session tells two players apart whose PCs both use 127.0.0.1. Peer is the player's
// real address as the gateway sees it (the stream's TLS connection's source, a datagram's sender): what a limit per
// source address counts (the sign-ups per address), since IP is a loopback address for every player.
type Addr struct {
	IP      net.IP
	Port    int
	Session uint64
	Peer    netip.Addr
	sess    *session
}

// Network is "tunnel".
func (a *Addr) Network() string { return "tunnel" }

// String is "ip:port", as a TCP address's (net.SplitHostPort works on it).
func (a *Addr) String() string { return net.JoinHostPort(a.IP.String(), strconv.Itoa(a.Port)) }

// TunnelSession returns the tunnel session id (an interface other packages test for without importing this one).
func (a *Addr) TunnelSession() uint64 { return a.Session }

// Release is the release name the session's Start-Client gave in its hello ("" when it named none or the address
// has no session), and same whether that is the gateway's own release. Without the gateway's AllowAnyClient every
// session is of the same release; with it, an older or another Start-Client is let in and same is false. The services
// take same as the capability of a Start-Client of this release: its setup makes the files every map the release
// offers needs (the Blaze map check, blaze.Server.MapTrust).
func (a *Addr) Release() (name string, same bool) {
	if a == nil || a.sess == nil {
		return "", false
	}
	a.sess.mu.Lock()
	defer a.sess.mu.Unlock()
	return a.sess.release, a.sess.sameRelease
}

// FakeSessionAddr is an address of a session that exists only for a service's tests: its Start-Client named release,
// same as the gateway's own or not (Release); it routes nothing.
func FakeSessionAddr(ip net.IP, port int, release string, same bool) *Addr {
	return &Addr{IP: ip, Port: port, Session: 1, sess: &session{release: release, sameRelease: same,
		routes: map[int]*net.UDPAddr{}}}
}

// IPPort returns the address and port.
func (a *Addr) IPPort() (net.IP, int) { return a.IP, a.Port }

// Route tells the gateway where the session's datagrams to port go (a match host's GMS address, e.g. an agent
// machine's, H2): Blaze calls it when it hands the client the tunnel address with that port in HNET. Without a route
// the gateway forwards a port of its UDP list to the server's own bind address.
func (a *Addr) Route(port int, target *net.UDPAddr) {
	if a.sess != nil && target != nil {
		a.sess.route(port, target)
	}
}

// PeerOf is a TCP or UDP address's IP (IPv4-mapped IPv6 unmapped); the zero Addr for any other address.
func PeerOf(a net.Addr) netip.Addr {
	var ip net.IP
	switch t := a.(type) {
	case *net.TCPAddr:
		ip = t.IP
	case *net.UDPAddr:
		ip = t.IP
	default:
		return netip.Addr{}
	}
	p, _ := netip.AddrFromSlice(ip)
	return p.Unmap()
}

// Local returns the tunnel address of a connection the gateway handed out (its LocalAddr), or nil for any other
// connection.
func Local(c net.Conn) *Addr {
	if c == nil {
		return nil
	}
	a, _ := c.LocalAddr().(*Addr)
	return a
}

// streamConn is a stream as a service sees it: the TLS connection with the player-side addresses.
type streamConn struct {
	net.Conn
	local, remote *Addr
}

func (c *streamConn) LocalAddr() net.Addr  { return c.local }
func (c *streamConn) RemoteAddr() net.Addr { return c.remote }

// listener is a virtual listener: the gateway pushes the streams of one port into it.
type listener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
}

func newListener(addr net.Addr) *listener {
	return &listener{addr: addr, ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *listener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *listener) Addr() net.Addr { return l.addr }

// push hands a stream to the listener's Accept; false when the listener is closed.
func (l *listener) push(c net.Conn) bool {
	select {
	case l.ch <- c:
		return true
	case <-l.done:
		return false
	}
}
