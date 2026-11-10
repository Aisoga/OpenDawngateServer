package tunnel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// The control connection's messages after the session reply (gateway -> client; the client sends heartbeats only),
// each a type byte and its fields (big-endian):
//
//	0 heartbeat: nothing more (every HeartbeatEvery, both ways)
//	1 away:      port uint16 (a match host's GMS port), the match gateway's IPv4 [4] and port uint16, its key's pin [32],
//	             ticket length uint16 + the ticket: send this port's datagrams through a session of their own to that
//	             gateway (a host machine in another region), with that ticket
//	2 home:      port uint16: this port's datagrams go through this session again
//
// A gateway sends them only to a session whose hello was awayHelloVersion or later; a client of an earlier release
// ignores every byte it reads there, so a message it never asked for costs it nothing.
const (
	ctrlHeartbeat byte = 0
	ctrlAway      byte = 1
	ctrlHome      byte = 2

	// awayHelloVersion is the session hello from which a client reads away and home (and may carry a ticket)
	awayHelloVersion = 4

	awayFixed = 1 + 2 + 4 + 2 + 32 + 2 // an away message's bytes before the ticket
)

// Away is where a match port's datagrams go instead of the main session: a host machine's match gateway, its pin and the
// ticket that lets this player into the match there.
type Away struct {
	Gateway netip.AddrPort // an IPv4 address and port
	Pin     Pin
	Ticket  []byte
}

// encodeAway is an away message.
func encodeAway(port int, a Away) ([]byte, error) {
	if !a.Gateway.Addr().Is4() || a.Gateway.Port() == 0 {
		return nil, fmt.Errorf("away: the gateway %v is no IPv4 address and port", a.Gateway)
	}
	if len(a.Ticket) == 0 || len(a.Ticket) > MaxTicketLen {
		return nil, fmt.Errorf("away: a ticket of %d bytes", len(a.Ticket))
	}
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("away: port %d", port)
	}
	b := []byte{ctrlAway}
	b = binary.BigEndian.AppendUint16(b, uint16(port))
	ip := a.Gateway.Addr().As4()
	b = append(b, ip[:]...)
	b = binary.BigEndian.AppendUint16(b, a.Gateway.Port())
	b = append(b, a.Pin[:]...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(a.Ticket)))
	return append(b, a.Ticket...), nil
}

// encodeHome is a home message.
func encodeHome(port int) []byte {
	return binary.BigEndian.AppendUint16([]byte{ctrlHome}, uint16(port))
}

var errControl = errors.New("tunnel: unknown control message")

// parseControl reads the first message of b: its length (0: incomplete), and for away and home the message. An unknown
// type, or a ticket over MaxTicketLen, is an error (the stream can't be read further).
func parseControl(b []byte) (n int, typ byte, port int, a Away, err error) {
	if len(b) == 0 {
		return 0, 0, 0, a, nil
	}
	switch b[0] {
	case ctrlHeartbeat:
		return 1, ctrlHeartbeat, 0, a, nil
	case ctrlHome:
		if len(b) < 3 {
			return 0, 0, 0, a, nil
		}
		return 3, ctrlHome, int(binary.BigEndian.Uint16(b[1:])), a, nil
	case ctrlAway:
		if len(b) < awayFixed {
			return 0, 0, 0, a, nil
		}
		tl := int(binary.BigEndian.Uint16(b[awayFixed-2:]))
		if tl == 0 || tl > MaxTicketLen {
			return 0, 0, 0, a, fmt.Errorf("%w: an away message with a ticket of %d bytes", errControl, tl)
		}
		if len(b) < awayFixed+tl {
			return 0, 0, 0, a, nil
		}
		port = int(binary.BigEndian.Uint16(b[1:]))
		a.Gateway = netip.AddrPortFrom(netip.AddrFrom4([4]byte(b[3:7])), binary.BigEndian.Uint16(b[7:]))
		copy(a.Pin[:], b[9:41])
		a.Ticket = append([]byte(nil), b[awayFixed:awayFixed+tl]...)
		return awayFixed + tl, ctrlAway, port, a, nil
	}
	return 0, 0, 0, a, fmt.Errorf("%w: type %d", errControl, b[0])
}

// controlConn is a client's control connection as heartbeat reads it: every byte read goes through the message parser,
// which calls the client's OnAway and OnHome.
type controlConn struct {
	net.Conn
	c   *Client
	buf []byte
}

func (cc *controlConn) Read(p []byte) (int, error) {
	n, err := cc.Conn.Read(p)
	if n > 0 {
		cc.buf = append(cc.buf, p[:n]...)
		for len(cc.buf) > 0 {
			m, typ, port, a, perr := parseControl(cc.buf)
			if perr != nil {
				cc.buf = nil
				return n, perr
			}
			if m == 0 {
				break
			}
			cc.buf = cc.buf[m:]
			cc.c.controlMessage(typ, port, a)
		}
		if len(cc.buf) == 0 {
			cc.buf = nil
		}
	}
	return n, err
}

// controlMessage hands an away or home message to the client's callbacks (logged; the ticket never).
func (c *Client) controlMessage(typ byte, port int, a Away) {
	switch typ {
	case ctrlAway:
		c.logf("away: port %d to the match gateway %s (key %s)", port, a.Gateway, a.Pin.Fingerprint())
		if c.OnAway != nil {
			c.OnAway(port, a)
		}
	case ctrlHome:
		c.logf("home: port %d through this session again", port)
		if c.OnHome != nil {
			c.OnHome(port)
		}
	}
}

// GatewayIP is the gateway's address as Start resolved it (the invalid Addr before Start).
func (c *Client) GatewayIP() netip.Addr {
	if c.gwUDP == nil {
		return netip.Addr{}
	}
	ip, _ := netip.AddrFromSlice(c.gwUDP.IP)
	return ip.Unmap()
}

// TakesAway says whether the session's Start-Client reads away and home (its session hello was awayHelloVersion or
// later); false for an address without a session.
func (a *Addr) TakesAway() bool {
	if a == nil || a.sess == nil {
		return false
	}
	a.sess.mu.Lock()
	defer a.sess.mu.Unlock()
	return a.sess.helloVer >= awayHelloVersion
}

// RouteAway sends the session's Start-Client to a match gateway for port (an away message on its control connection):
// it then carries that port's datagrams through a session of its own to that gateway, with the ticket. The session's
// own route for port goes (its datagrams to port are dropped here meanwhile); a later Route for port sends home. It
// says whether the message went out: false for a Start-Client that doesn't read it (TakesAway), a session whose control
// connection is down (being made again) or a bad Away; the message is written before RouteAway returns.
func (a *Addr) RouteAway(port int, away Away) bool {
	if a == nil || a.sess == nil || a.sess.g == nil {
		return false
	}
	msg, err := encodeAway(port, away)
	if err != nil {
		return false
	}
	s := a.sess
	s.mu.Lock()
	ctrl := s.control
	ok := ctrl != nil && s.helloVer >= awayHelloVersion && !s.ended
	if ok {
		delete(s.routes, port)
		if s.aways == nil {
			s.aways = map[int]bool{}
		}
		s.aways[port] = true
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	if err := s.writeControl(ctrl, msg); err != nil {
		s.g.logf("session %d: the away message for port %d didn't go out: %v", s.id, port, err)
		return false
	}
	s.g.logf("session %d: port %d away to the match gateway %s (key %s)", s.id, port, away.Gateway, away.Pin.Fingerprint())
	return true
}

// homeIfAway sends home for port when the session was sent away for it (Route's hook).
func (s *session) homeIfAway(port int) {
	s.mu.Lock()
	ctrl, was := s.control, s.aways[port]
	delete(s.aways, port)
	s.mu.Unlock()
	if !was || ctrl == nil {
		return
	}
	if err := s.writeControl(ctrl, encodeHome(port)); err != nil {
		s.g.logf("session %d: the home message for port %d didn't go out: %v", s.id, port, err)
		return
	}
	s.g.logf("session %d: port %d home", s.id, port)
}

// writeControl writes one message on the control connection (tls.Conn serializes it with the heartbeats).
func (s *session) writeControl(ctrl net.Conn, msg []byte) error {
	timeout := DefaultHeartbeatTimeout
	if s.g.HeartbeatTimeout > 0 {
		timeout = s.g.HeartbeatTimeout
	}
	ctrl.SetWriteDeadline(time.Now().Add(timeout))
	_, err := ctrl.Write(msg)
	return err
}
