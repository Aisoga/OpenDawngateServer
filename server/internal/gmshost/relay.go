package gmshost

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"dawngate/server/internal/logging"
)

// The relay (REGION1): on a host machine with the TLS link, the agent listens on the hosts' control port
// (AgentLink.Relay, loopback) and its hosts dial it as they would dial a main server (they are unchanged). The relay
// checks a host's first line (its hello: a match running here, with that match's token) and then carries every line
// in the link: host -> server hostLine {match, line}, server -> host hostLine, either side hostClose {match, why}.
// The main server feeds a match's lines to hostHello and the match as if the host had dialled it (link.go).
//
// A host's verifyHello that carries a "flow" member is dropped (only the relay may add one: AgentLink.Annotate). A
// channel whose lines can't keep up (relayQueue) is closed, not the link. The link's end closes every channel, and
// the agent kills its hosts.

// relayHelloTimeout is how long a host connection may take to say hello.
const relayHelloTimeout = 10 * time.Second

type relay struct {
	a  *Agent
	ln net.Listener

	mu    sync.Mutex
	link  *agentLink          // nil while not registered
	chans map[int64]*hostChan // the hosts' channels by match
	wg    sync.WaitGroup
}

// startRelay listens on AgentLink.Relay (TLS link only) and serves it until stopRelay.
func (a *Agent) startRelay() error {
	if !a.linkOn() {
		return nil
	}
	ln, err := net.Listen("tcp4", a.Link.Relay)
	if err != nil {
		return fmt.Errorf("gmshost: agent: the hosts' control port: %w", err)
	}
	r := &relay{a: a, ln: ln, chans: map[int64]*hostChan{}}
	a.mu.Lock()
	a.relay = r
	a.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				r.serveHost(c)
			}()
		}
	}()
	a.Log.Logf(agentWho, "the hosts' control port %s (the relay)", ln.Addr())
	return nil
}

// stopRelay closes the relay's listener and its channels.
func (a *Agent) stopRelay() {
	r := a.relay
	if r == nil {
		return
	}
	r.ln.Close()
	a.detachRelay()
	r.wg.Wait()
}

// relayAddr is the address the hosts dial (DGGMS_CONTROL), numeric.
func (a *Agent) relayAddr() string { return a.relay.ln.Addr().String() }

// attachRelay connects the relay to a registered link.
func (a *Agent) attachRelay(l *agentLink) {
	r := a.relay
	r.mu.Lock()
	r.link = l
	r.mu.Unlock()
}

// detachRelay disconnects the relay from its link and closes every channel (the link ended).
func (a *Agent) detachRelay() {
	r := a.relay
	r.mu.Lock()
	r.link = nil
	chans := r.chans
	r.chans = map[int64]*hostChan{}
	r.mu.Unlock()
	for _, ch := range chans {
		ch.end("the link to the main server ended", false)
	}
}

// hostChan is one host's control connection on the relay.
type hostChan struct {
	r     *relay
	l     *agentLink
	match int64
	c     net.Conn
	out   chan []byte // lines for the host; a nil line ends the channel after the ones before it
	done  chan struct{}
	once  sync.Once
}

// serveHost checks a host connection's hello and then relays it.
func (r *relay) serveHost(c net.Conn) {
	peer := logging.Peer(c.RemoteAddr())
	defer r.a.Log.Recover(agentWho, peer, c)
	refuse := func(why string) {
		r.a.Log.Logf(agentWho, "relay: host connection from %s refused: %s", peer, why)
		c.Close()
	}
	c.SetReadDeadline(time.Now().Add(relayHelloTimeout))
	lr := NewLineReader(c)
	line, err := lr.Next()
	if err != nil {
		refuse("no hello: " + err.Error())
		return
	}
	msg, _ := Decode(line)
	h, ok := msg.(Hello)
	if !ok {
		refuse("the first line is not hello")
		return
	}
	r.a.mu.Lock()
	host := r.a.running[h.Match]
	tokenOK := host != nil && subtle.ConstantTimeCompare([]byte(h.Token), []byte(host.token)) == 1
	r.a.mu.Unlock()
	if !tokenOK {
		refuse(fmt.Sprintf("match %d doesn't run here, or the token is wrong", h.Match))
		return
	}
	r.mu.Lock()
	l := r.link
	switch {
	case l == nil:
		r.mu.Unlock()
		refuse("no link to the main server")
		return
	case r.chans[h.Match] != nil:
		r.mu.Unlock()
		refuse(fmt.Sprintf("match %d's host is connected already", h.Match))
		return
	}
	ch := &hostChan{r: r, l: l, match: h.Match, c: c, out: make(chan []byte, relayQueue), done: make(chan struct{})}
	r.chans[h.Match] = ch
	r.mu.Unlock()
	c.SetReadDeadline(time.Time{})
	r.a.Log.Logf(agentWho, "match %d: the host's control connection relayed (pid %d)", h.Match, h.PID)
	go ch.writer()
	if !l.relay(HostLine{Match: h.Match, Line: json.RawMessage(line)}) {
		ch.end("the link's queue is full", false)
		return
	}
	for {
		line, err := lr.Next()
		if err != nil {
			ch.end("the host closed its control connection", true)
			return
		}
		if line = r.filter(h.Match, line); line == nil {
			continue
		}
		if !l.relay(HostLine{Match: h.Match, Line: json.RawMessage(line)}) {
			ch.end("the link's queue is full", true)
			return
		}
	}
}

// filter is a host's line as the relay sends it, or nil (dropped). The line is read as the main server reads it
// (Decode: keys without regard to case, escapes undone), so no spelling slips past: a verifyHello with a flow of its own
// is dropped, any other verifyHello goes on as Encode writes it (only its own fields) through AgentLink.Annotate. Every
// other line goes through as it came.
func (r *relay) filter(match int64, line []byte) []byte {
	msg, err := Decode(line)
	if err != nil {
		return line // the main server can't read it either
	}
	v, ok := msg.(VerifyHello)
	if !ok {
		return line
	}
	if v.Flow != nil {
		r.a.Log.Logf(agentWho, "match %d: a verifyHello with a flow member of its own dropped", match)
		return nil
	}
	clean, err := Encode(v)
	if err != nil {
		return nil
	}
	clean = bytes.TrimRight(clean, "\n")
	if r.a.Link.Annotate != nil {
		return r.a.Link.Annotate(match, clean)
	}
	return clean
}

// toHost writes a server's line to its host (in order, through the channel's queue).
func (a *Agent) toHost(v HostLine) {
	r := a.relay
	r.mu.Lock()
	ch := r.chans[v.Match]
	r.mu.Unlock()
	if ch == nil {
		return // the channel ended here already; its hostClose is on the way
	}
	if !ch.push(v.Line) {
		ch.end("the server's lines came faster than the host read them", true)
	}
}

// closeFromServer ends a host's channel once the lines before have reached the host (hostClose from the server).
func (a *Agent) closeFromServer(v HostClose) {
	r := a.relay
	r.mu.Lock()
	ch := r.chans[v.Match]
	r.mu.Unlock()
	if ch == nil {
		return
	}
	a.Log.Logf(agentWho, "match %d: the main server closed the host's channel: %s", v.Match, v.Why)
	if !ch.push(nil) {
		ch.end("closed by the main server", false)
	}
}

func (ch *hostChan) push(line []byte) bool {
	select {
	case ch.out <- line:
		return true
	case <-ch.done:
		return true
	default:
		return false
	}
}

// writer writes the queued lines to the host.
func (ch *hostChan) writer() {
	for {
		select {
		case line := <-ch.out:
			if line == nil {
				ch.end("closed by the main server", false)
				return
			}
			ch.c.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := ch.c.Write(append(append([]byte(nil), line...), '\n')); err != nil {
				ch.end("a write to the host failed: "+err.Error(), true)
				return
			}
		case <-ch.done:
			return
		}
	}
}

// end closes the channel once; tell sends hostClose to the main server.
func (ch *hostChan) end(why string, tell bool) {
	ch.once.Do(func() {
		r := ch.r
		r.mu.Lock()
		if r.chans[ch.match] == ch {
			delete(r.chans, ch.match)
		}
		r.mu.Unlock()
		close(ch.done)
		ch.c.Close()
		r.a.Log.Logf(agentWho, "match %d: the host's channel closed: %s", ch.match, why)
		if tell && !ch.l.relay(HostClose{Match: ch.match, Why: why}) {
			go ch.l.send(HostClose{Match: ch.match, Why: why}) // the queue is full: at least the close
		}
	})
}
