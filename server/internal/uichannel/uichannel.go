// Package uichannel is the game UI's own connection to the server: how a class of ours in one of the game's UI movies
// (client_ui/) asks the server for something and hears back, without going through any of the game's channels.
//
// The class opens an ordinary TCP connection with flash.net.Socket, which this client's Scaleform player implements
// (docs/development/ui-channel.md; the movie's side is client_ui/common/opendawngate/ServerChannel.as).
// On a tunnelled server the connection crosses the tunnel like the game's own (Start-Client listens on this port, one of
// the game's fixed ones).
//
// The wire: one JSON object per line, at most MaxLine bytes.
//
//	client -> server   {"call":"<name>","args":{...}}     a call, UTF-8; args may be left out
//	server -> client   {"event":"<name>","data":{...}}    an event, pure ASCII (anything else as \uXXXX); data may be left out
//
// The server's lines are ASCII because of the client's reads (its readBytes crashes the client, so it reads text, and a
// chunk may end anywhere: no character can be cut in two this way).
//
// A connection starts with the call "hello" {"protocol":1,"name":"<the player's name>"}, answered by the event "hello"
// {"protocol":1} or by "refused" {"reason":"..."} and the connection's end. Calls have no replies of their own: a call
// changes something on the server, and the server tells whoever cares with an event. An unknown call is logged and
// ignored, so a newer client still works on an older server.
//
// Anyone can connect, and a client's text is hostile: what a line says goes into the log only cut short and only a few
// times per connection, a user's calls share one budget whatever its connections, and a connection belongs to the login
// that Identify named it by, so it ends with that login even when the persona has another one elsewhere (Recheck).
//
// A new feature is one Handle call here and one on() in its class: docs/development/ui-channel.md.
package uichannel

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"dawngate/server/internal/logging"
)

const (
	// Port is the channel's TCP port, also the setup's in the movies' dgchannel.json.
	Port = 10039
	// Protocol is the wire's version, told in the hello both ways.
	Protocol = 1
	// MaxLine is the longest line either side may send, its newline included.
	MaxLine = 64 << 10
	// MaxName is the longest name a hello may give, in bytes; no player's name comes near it.
	MaxName = 64

	// CallHello and EventHello are the handshake, EventRefused its failure.
	CallHello    = "hello"
	EventHello   = "hello"
	EventRefused = "refused"

	who          = "uichannel"
	helloTimeout = 10 * time.Second
	writeTimeout = 10 * time.Second
	queueSize    = 64 // events waiting for one connection; a client that far behind is dropped
	maxPerUser   = 8  // a user's connections; a further one ends the oldest
	callsPerSec  = 10 // a user's sustained calls per second, over all its connections
	callBurst    = 20 // and its burst
	maxFaults    = 5  // lines a connection's faults may add to the log: the client decides how often they happen
	logClip      = 80 // how much of a client's text a log line holds
)

// Identify names the user behind a connection; name is the hello's. False refuses the connection.
type Identify func(c net.Conn, name string) (user int64, ok bool)

// Handler serves one call from user. A returned error is logged, cut short; the connection stays.
type Handler func(user int64, args json.RawMessage) error

// Server serves the channel. Set Log and Identify, register the calls with Handle, then Serve.
type Server struct {
	Log      *logging.Log
	Identify Identify
	// OnDisconnect is told when user's last connection ended.
	OnDisconnect func(user int64)

	mu       sync.Mutex
	handlers map[string]Handler
	users    map[int64][]*conn // a user's connections, oldest first (each movie's class may hold its own)
	budgets  map[int64]*rate   // a user's calls, over all its connections
	all      map[*conn]bool
	closed   bool
}

// conn is one client connection.
type conn struct {
	nc     net.Conn
	user   int64
	name   string // the hello's, for Identify again (Recheck)
	out    chan []byte
	once   sync.Once
	gone   chan struct{}
	faults int // log lines of this connection's faults so far (the read loop's)
}

// rate is a token bucket for a user's calls.
type rate struct {
	tokens float64
	last   time.Time
}

// full reports whether the bucket has refilled by now: it is then as good as a new one.
func (r *rate) full(now time.Time) bool {
	return r.tokens+now.Sub(r.last).Seconds()*callsPerSec >= callBurst
}

func (r *rate) take(now time.Time) bool {
	r.tokens += now.Sub(r.last).Seconds() * callsPerSec
	r.last = now
	if r.tokens > callBurst {
		r.tokens = callBurst
	}
	if r.tokens < 1 {
		return false
	}
	r.tokens--
	return true
}

func (c *conn) close() {
	c.once.Do(func() {
		close(c.gone)
		c.nc.Close()
	})
}

// Handle registers the handler of a call.
func (s *Server) Handle(call string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handlers == nil {
		s.handlers = map[string]Handler{}
	}
	s.handlers[call] = h
}

// Serve accepts connections until ln closes.
func (s *Server) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.ServeConn(c)
	}
}

// Send sends an event to each of user's connections; false when none took it (no connection, or queues full, which
// ends those connections).
func (s *Server) Send(user int64, event string, data any) bool {
	line, err := encodeEvent(event, data)
	if err != nil {
		s.logf("event %s for %d: %v", event, user, err)
		return false
	}
	s.mu.Lock()
	conns := slices.Clone(s.users[user])
	s.mu.Unlock()
	sent := false
	for _, c := range conns {
		sent = c.send(s, line) || sent
	}
	return sent
}

// Connected reports whether user has a connection.
func (s *Server) Connected(user int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users[user]) > 0
}

// Recheck asks Identify again about each of user's connections and ends those it no longer names as user. Call it when
// one of user's logins ends: a connection belongs to the login it came by, not to a persona that may have another.
func (s *Server) Recheck(user int64) {
	s.mu.Lock()
	conns := slices.Clone(s.users[user])
	s.mu.Unlock()
	for _, c := range conns {
		if u, ok := s.Identify(c.nc, c.name); !ok || u != user {
			s.logf("%s: the login behind user %d's connection ended; closing", logging.Peer(c.nc.RemoteAddr()), user)
			c.close()
		}
	}
}

// Close ends every connection; connections that arrive later are closed at once.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	all := make([]*conn, 0, len(s.all))
	for c := range s.all {
		all = append(all, c)
	}
	s.mu.Unlock()
	for _, c := range all {
		c.close()
	}
}

func (s *Server) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log.Logf(who, format, a...)
	}
}

// fault logs a client's fault, at most maxFaults times per connection.
func (s *Server) fault(c *conn, peer, text string) {
	c.faults++
	switch {
	case c.faults < maxFaults:
		s.logf("%s (user %d): %s", peer, c.user, plain(text))
	case c.faults == maxFaults:
		s.logf("%s (user %d): %s; further faults of this connection aren't logged", peer, c.user, plain(text))
	}
}

// clip is a client's text as far as a log line or a refusal takes it.
func clip(s string) string {
	if len(s) <= logClip {
		return s
	}
	return s[:logClip] + "..."
}

// plain is a text that may hold a client's words as one short log line: no control characters, cut short.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7F {
			return '?'
		}
		return r
	}, clip(s))
}

// allow takes a call from user's budget.
func (s *Server) allow(user int64, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.budgets[user]
	return b != nil && b.take(now)
}

// encodeEvent is an event's line.
func encodeEvent(event string, data any) ([]byte, error) {
	line, err := json.Marshal(struct {
		Event string `json:"event"`
		Data  any    `json:"data,omitempty"`
	}{event, data})
	if err != nil {
		return nil, err
	}
	line = asciiJSON(line)
	if len(line) >= MaxLine {
		return nil, fmt.Errorf("%d bytes: over the line limit", len(line))
	}
	return append(line, '\n'), nil
}

// asciiJSON escapes every non-ASCII character of a JSON text as \uXXXX (above the 16-bit range as a surrogate pair, as
// JSON has it). Outside strings JSON is ASCII anyway.
func asciiJSON(b []byte) []byte {
	ascii := true
	for _, c := range b {
		if c >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return b
	}
	var out bytes.Buffer
	for _, r := range string(b) {
		switch {
		case r < utf8.RuneSelf:
			out.WriteByte(byte(r))
		case r > 0xFFFF:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&out, "\\u%04x\\u%04x", r1, r2)
		default:
			fmt.Fprintf(&out, "\\u%04x", r)
		}
	}
	return out.Bytes()
}

// send queues a line; a client too far behind loses its connection.
func (c *conn) send(s *Server, line []byte) bool {
	select {
	case <-c.gone:
		return false
	case c.out <- line:
		return true
	default:
		s.logf("%s: %d events behind; closing", logging.Peer(c.nc.RemoteAddr()), queueSize)
		c.close()
		return false
	}
}

func (c *conn) writeLoop() {
	for {
		select {
		case <-c.gone:
			return
		case line := <-c.out:
			c.nc.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := c.nc.Write(line); err != nil {
				c.close()
				return
			}
		}
	}
}

// ServeConn serves one connection until it ends.
func (s *Server) ServeConn(nc net.Conn) {
	peer := logging.Peer(nc.RemoteAddr())
	if s.Log != nil {
		defer s.Log.Recover(who, peer, nc) // a panic ends this connection only
	}
	c := &conn{nc: nc, out: make(chan []byte, queueSize), gone: make(chan struct{})}
	defer c.close()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.all == nil {
		s.all = map[*conn]bool{}
	}
	s.all[c] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.all, c)
		s.mu.Unlock()
	}()
	r := bufio.NewReaderSize(nc, MaxLine)
	nc.SetReadDeadline(time.Now().Add(helloTimeout))
	if !s.hello(c, r, peer) {
		return
	}
	defer s.forget(c)
	nc.SetReadDeadline(time.Time{})
	go c.writeLoop()
	for {
		call, args, err := readCall(r)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				s.logf("%s (user %d): %s", peer, c.user, plain(err.Error()))
			}
			return
		}
		if !s.allow(c.user, time.Now()) {
			s.logf("%s (user %d): over %d calls a second; closing", peer, c.user, callsPerSec)
			return
		}
		s.mu.Lock()
		h := s.handlers[call]
		s.mu.Unlock()
		if h == nil {
			s.fault(c, peer, fmt.Sprintf("unknown call %q; ignored", clip(call)))
			continue
		}
		if err := h(c.user, args); err != nil {
			s.fault(c, peer, call+": "+err.Error())
		}
	}
}

// hello runs the handshake; false means the connection ends. Its answers are written directly: the write loop starts
// after it, so an event queued as soon as the user is known can't overtake the hello.
func (s *Server) hello(c *conn, r *bufio.Reader, peer string) bool {
	answer := func(event string, data any) bool {
		line, err := encodeEvent(event, data)
		if err != nil {
			return false
		}
		c.nc.SetWriteDeadline(time.Now().Add(writeTimeout))
		_, err = c.nc.Write(line)
		return err == nil
	}
	refuse := func(reason string) bool {
		s.logf("%s: refused: %s", peer, reason)
		answer(EventRefused, map[string]string{"reason": reason})
		return false
	}
	call, args, err := readCall(r)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			s.logf("%s: no hello: %s", peer, plain(err.Error()))
		}
		return false
	}
	if call != CallHello {
		return refuse(fmt.Sprintf("the first call is %q, not hello", clip(call)))
	}
	var h struct {
		Protocol int    `json:"protocol"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(args, &h); err != nil {
		return refuse("a malformed hello")
	}
	if h.Protocol != Protocol {
		return refuse(fmt.Sprintf("protocol %d; this server speaks %d", h.Protocol, Protocol))
	}
	if len(h.Name) > MaxName {
		return refuse(fmt.Sprintf("a name over %d bytes", MaxName))
	}
	if s.Identify == nil {
		return refuse("nobody can be identified")
	}
	user, ok := s.Identify(c.nc, h.Name)
	if !ok {
		return refuse(fmt.Sprintf("no login for %q from here", h.Name))
	}
	c.user, c.name = user, h.Name
	s.mu.Lock()
	if s.users == nil {
		s.users = map[int64][]*conn{}
		s.budgets = map[int64]*rate{}
	}
	var oldest *conn
	if len(s.users[user]) >= maxPerUser {
		oldest = s.users[user][0]
	}
	s.users[user] = append(s.users[user], c)
	if s.budgets[user] == nil {
		s.budgets[user] = &rate{tokens: callBurst, last: time.Now()}
	}
	s.mu.Unlock()
	// A login that ended since Identify found nothing to end here (Recheck), so look again now that it would.
	if again, ok := s.Identify(c.nc, h.Name); !ok || again != user {
		s.forget(c)
		return refuse(fmt.Sprintf("no login for %q from here", h.Name))
	}
	if oldest != nil {
		s.logf("%s: user %d has %d connections; the oldest ends", peer, user, maxPerUser)
		oldest.close()
	}
	s.logf("%s: user %d connected", peer, user)
	if !answer(EventHello, map[string]int{"protocol": Protocol}) {
		s.forget(c)
		return false
	}
	return true
}

// forget takes an ended connection off its user, and tells OnDisconnect when it was the last one.
func (s *Server) forget(c *conn) {
	s.mu.Lock()
	conns := slices.DeleteFunc(s.users[c.user], func(x *conn) bool { return x == c })
	last := len(conns) == 0
	if last {
		delete(s.users, c.user)
		s.forgetBudgets(time.Now())
	} else {
		s.users[c.user] = conns
	}
	notify := s.OnDisconnect
	s.mu.Unlock()
	if last {
		s.logf("user %d disconnected", c.user)
		if notify != nil {
			notify(c.user)
		}
	}
}

// forgetBudgets drops the budgets of users without a connection that have refilled (s.mu held). A budget outlives its
// user's last connection, or a reconnect would be a way round it, but only for as long as that makes a difference: a
// bucket is full again callBurst/callsPerSec after its last call at most.
func (s *Server) forgetBudgets(now time.Time) {
	for user, b := range s.budgets {
		if len(s.users[user]) == 0 && b.full(now) {
			delete(s.budgets, user)
		}
	}
}

// readCall reads one call line.
func readCall(r *bufio.Reader) (string, json.RawMessage, error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return "", nil, fmt.Errorf("a line over %d bytes", MaxLine)
		}
		return "", nil, err
	}
	var m struct {
		Call string          `json:"call"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return "", nil, fmt.Errorf("a malformed line: %w", err)
	}
	if m.Call == "" {
		return "", nil, errors.New("a line without a call")
	}
	return m.Call, m.Args, nil
}
