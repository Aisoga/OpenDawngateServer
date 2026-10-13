package limits

import (
	"net"
	"sync"
)

// Listener wraps ln so that every accepted connection passes Admit: a refused one is closed at once (the client sees
// its connection reset) and Accept goes on to the next; an admitted one releases its slot when it is closed. logf
// gets one line per refused address and kind a minute (nil = none). For the redirector, wrap the TCP listener before
// tls.NewListener, so a refused connection costs no handshake. A nil Limiter returns ln itself.
func (l *Limiter) Listener(ln net.Listener, name string, logf func(format string, a ...any)) net.Listener {
	if l == nil {
		return ln
	}
	return &listener{Listener: ln, l: l, name: name, logf: logf}
}

type listener struct {
	net.Listener
	l    *Limiter
	name string
	logf func(format string, a ...any)
}

func (ln *listener) Accept() (net.Conn, error) {
	for {
		c, err := ln.Listener.Accept()
		if err != nil {
			return nil, err
		}
		addr := HostOf(c.RemoteAddr())
		release, ok, why := ln.l.Admit(addr)
		if !ok {
			if tc, isTCP := c.(*net.TCPConn); isTCP {
				tc.SetLinger(0) // a reset, not a FIN: nothing is kept for the address
			}
			c.Close()
			if ln.logf != nil && ln.l.ShouldLog("conn "+ln.name, addr) {
				ln.logf("limits: %s connection from %s refused: %s (further refusals of this address are counted in the "+
					"minute's limits line)", ln.name, addr, why)
			}
			continue
		}
		return &conn{Conn: c, release: release}, nil
	}
}

// conn releases its Admit slot once, when closed.
type conn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *conn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
