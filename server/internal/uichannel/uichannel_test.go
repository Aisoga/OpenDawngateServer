package uichannel

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// client is a test's end of a channel connection.
type client struct {
	t  *testing.T
	nc net.Conn
	r  *bufio.Reader
}

func (c *client) write(line string) {
	c.t.Helper()
	c.nc.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.nc.Write([]byte(line + "\n")); err != nil {
		c.t.Fatalf("write %q: %v", line, err)
	}
}

// line reads the next line as the wire has it (without its newline).
func (c *client) line() string {
	c.t.Helper()
	c.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	s, err := c.r.ReadString('\n')
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	return strings.TrimSuffix(s, "\n")
}

// closed expects the server to end the connection.
func (c *client) closed() {
	c.t.Helper()
	c.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, err := c.r.ReadByte(); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				c.t.Fatal("the connection stayed open")
			}
			return
		}
	}
}

type fixture struct {
	t          *testing.T
	s          *Server
	log        *syncWriter
	identified atomic.Int64 // Identify's calls
	alphaGone  atomic.Bool  // alpha's login ended
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	log := &syncWriter{}
	s := &Server{Log: logging.New(log)}
	f := &fixture{t: t, s: s, log: log}
	s.Identify = func(_ net.Conn, name string) (int64, bool) {
		f.identified.Add(1)
		if name == "alpha" && f.alphaGone.Load() {
			return 0, false
		}
		switch name {
		case "alpha":
			return 1, true
		case "bravo":
			return 2, true
		}
		return 0, false
	}
	t.Cleanup(s.Close)
	return f
}

// syncWriter is a log both the server's goroutines and the test use.
type syncWriter struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func (f *fixture) connect() *client {
	f.t.Helper()
	a, b := net.Pipe()
	go f.s.ServeConn(b)
	f.t.Cleanup(func() { a.Close() })
	return &client{t: f.t, nc: a, r: bufio.NewReader(a)}
}

func (f *fixture) hello(name string) *client {
	f.t.Helper()
	c := f.connect()
	c.write(`{"call":"hello","args":{"protocol":1,"name":"` + name + `"}}`)
	if got, want := c.line(), `{"event":"hello","data":{"protocol":1}}`; got != want {
		f.t.Fatalf("hello: %s, want %s", got, want)
	}
	return c
}

// The wire's bytes both ways: the golden lines of docs/development/ui-channel.md.
func TestLines(t *testing.T) {
	f := newFixture(t)
	got := make(chan string, 1)
	f.s.Handle("lobby.setReady", func(user int64, args json.RawMessage) error {
		got <- string(args)
		return nil
	})
	c := f.hello("alpha")
	c.write(`{"call":"lobby.setReady","args":{"ready":true}}`)
	if s := <-got; s != `{"ready":true}` {
		t.Errorf("args %s", s)
	}
	if !f.s.Send(1, "lobby.state", map[string]any{"players": []map[string]any{{"name": "Grüße ✓", "ready": false}}}) {
		t.Fatal("Send: false")
	}
	if got, want := c.line(), `{"event":"lobby.state","data":{"players":[{"name":"Gr\u00fc\u00dfe \u2713","ready":false}]}}`; got != want {
		t.Errorf("event %s, want %s", got, want)
	}
	// above the 16-bit range: a surrogate pair; and a call may be UTF-8 as it is
	if !f.s.Send(1, "e", "😀") {
		t.Fatal("Send: false")
	}
	if got, want := c.line(), `{"event":"e","data":"\ud83d\ude00"}`; got != want {
		t.Errorf("event %s, want %s", got, want)
	}
	c.write(`{"call":"lobby.setReady","args":{"name":"Grüße ✓"}}`)
	if s := <-got; s != `{"name":"Grüße ✓"}` {
		t.Errorf("args %s", s)
	}
	if !f.s.Send(1, "lobby.startRefused", nil) {
		t.Fatal("Send: false")
	}
	if got, want := c.line(), `{"event":"lobby.startRefused"}`; got != want {
		t.Errorf("event %s, want %s", got, want)
	}
}

// Who may connect: only a hello first, of this protocol, from someone Identify knows.
func TestHelloRefusals(t *testing.T) {
	for _, tc := range []struct{ name, line, reason string }{
		{"another call first", `{"call":"lobby.setReady","args":{"ready":true}}`, `the first call is \"lobby.setReady\", not hello`},
		{"another protocol", `{"call":"hello","args":{"protocol":2,"name":"alpha"}}`, "protocol 2; this server speaks 1"},
		{"no login", `{"call":"hello","args":{"protocol":1,"name":"nobody"}}`, `no login for \"nobody\" from here`},
		{"malformed args", `{"call":"hello","args":{"protocol":"x"}}`, "a malformed hello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			c := f.connect()
			c.write(tc.line)
			if got, want := c.line(), `{"event":"refused","data":{"reason":"`+tc.reason+`"}}`; got != want {
				t.Errorf("%s, want %s", got, want)
			}
			c.closed()
			if f.s.Connected(1) {
				t.Error("connected after a refusal")
			}
		})
	}
	t.Run("not JSON", func(t *testing.T) {
		f := newFixture(t)
		c := f.connect()
		c.write("~#rdy 1~")
		c.closed()
	})
}

// A call runs as the identified user, an unknown one is ignored, a handler's error keeps the connection.
func TestCalls(t *testing.T) {
	f := newFixture(t)
	got := make(chan int64, 4)
	f.s.Handle("x", func(user int64, _ json.RawMessage) error {
		got <- user
		return errors.New("a handler's error")
	})
	a, b := f.hello("alpha"), f.hello("bravo")
	a.write(`{"call":"no such call"}`)
	a.write(`{"call":"x"}`)
	if u := <-got; u != 1 {
		t.Errorf("a's call ran as user %d", u)
	}
	b.write(`{"call":"x"}`)
	if u := <-got; u != 2 {
		t.Errorf("b's call ran as user %d", u)
	}
	a.write(`{"call":"x"}`)
	if u := <-got; u != 1 {
		t.Errorf("a's second call ran as user %d", u)
	}
	if log := f.log.String(); !strings.Contains(log, `unknown call "no such call"; ignored`) || !strings.Contains(log, "x: a handler's error") {
		t.Errorf("log: %s", log)
	}
}

// Greet's events reach each new connection right after its hello, the user's own, before anything else; an event that
// can't be encoded is logged and left out.
func TestGreet(t *testing.T) {
	f := newFixture(t)
	f.s.Greet = func(user int64) []Greeting {
		return []Greeting{{Event: "region.state", Data: map[string]any{"user": user}}, {Event: "bad", Data: func() {}},
			{Event: "second"}}
	}
	a := f.hello("alpha")
	if got, want := a.line(), `{"event":"region.state","data":{"user":1}}`; got != want {
		t.Errorf("greeting %s, want %s", got, want)
	}
	if got, want := a.line(), `{"event":"second"}`; got != want {
		t.Errorf("second greeting %s, want %s", got, want)
	}
	b := f.hello("bravo")
	if got, want := b.line(), `{"event":"region.state","data":{"user":2}}`; got != want {
		t.Errorf("bravo's greeting %s, want %s", got, want)
	}
	if log := f.log.String(); !strings.Contains(log, "greeting bad for 1:") {
		t.Errorf("log: %s", log)
	}
}

// A line over the limit, a malformed one, one without a call and too many calls end the connection, and the end is told.
func TestBadClients(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(c *client)
	}{
		{"malformed", func(c *client) { c.write("{") }},
		{"no call", func(c *client) { c.write(`{"args":{}}`) }},
		{"too long", func(c *client) {
			go c.nc.Write([]byte(strings.Repeat("x", MaxLine+1)))
		}},
		{"too many calls", func(c *client) {
			go func() {
				for i := 0; i < callBurst*4; i++ {
					if _, err := c.nc.Write([]byte(`{"call":"x"}` + "\n")); err != nil {
						return
					}
				}
			}()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ended := make(chan int64, 1)
			f.s.OnDisconnect = func(user int64) { ended <- user }
			f.s.Handle("x", func(int64, json.RawMessage) error { return nil })
			c := f.hello("alpha")
			tc.write(c)
			select {
			case u := <-ended:
				if u != 1 {
					t.Errorf("OnDisconnect names %d", u)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the connection stayed open")
			}
			if f.s.Connected(1) {
				t.Error("still connected")
			}
		})
	}
}

// A user may have several connections (each movie's class its own): every one gets the events, one's end isn't a
// disconnect, the last one's is, and a connection over the limit ends the oldest.
func TestSeveralConnections(t *testing.T) {
	f := newFixture(t)
	ended := make(chan int64, 2)
	f.s.OnDisconnect = func(user int64) { ended <- user }
	first := f.hello("alpha")
	second := f.hello("alpha")
	if !f.s.Send(1, "e", nil) {
		t.Fatal("Send: false")
	}
	for _, c := range []*client{first, second} {
		if got := c.line(); got != `{"event":"e"}` {
			t.Errorf("got %s", got)
		}
	}
	first.nc.Close()
	select {
	case <-ended:
		t.Fatal("OnDisconnect while another connection is up")
	case <-time.After(50 * time.Millisecond):
	}
	if !f.s.Connected(1) {
		t.Error("not connected any more")
	}
	if !f.s.Send(1, "e", nil) {
		t.Fatal("Send: false")
	}
	if got := second.line(); got != `{"event":"e"}` {
		t.Errorf("the second connection got %s", got)
	}
	// the last one's end comes after a limit's: one over ends the oldest
	var more []*client
	for i := 1; i < maxPerUser; i++ {
		more = append(more, f.hello("alpha"))
	}
	f.hello("alpha")
	second.closed()
	f.alphaGone.Store(true)
	f.s.Recheck(1)
	for _, c := range more {
		c.closed()
	}
	select {
	case u := <-ended:
		if u != 1 {
			t.Errorf("OnDisconnect names %d", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no OnDisconnect")
	}
	if f.s.Send(1, "e", nil) {
		t.Error("Send after the end: true")
	}
}

// Nobody who doesn't say hello stays; a client that stops reading is dropped rather than holding the server up.
func TestSlowClient(t *testing.T) {
	f := newFixture(t)
	c := f.hello("alpha")
	sent := 0
	for i := 0; i < queueSize*4 && f.s.Send(1, "e", map[string]string{"pad": strings.Repeat("x", 1024)}); i++ {
		sent++
	}
	if sent >= queueSize*4 {
		t.Fatalf("%d events queued for a client that doesn't read", sent)
	}
	c.closed()
	if !strings.Contains(f.log.String(), "events behind; closing") {
		t.Errorf("log: %s", f.log.String())
	}
}

// A client's text in the log is short and on one line, and a connection's faults add only so many lines: a client must
// not be able to fill the server's disk.
func TestLogBounded(t *testing.T) {
	f := newFixture(t)
	c := f.hello("alpha")
	long := strings.Repeat("\x7f", MaxLine/2)
	line, err := json.Marshal(map[string]string{"call": long})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < callBurst; i++ {
		c.write(string(line))
	}
	c.nc.Close()
	deadline := time.Now().Add(2 * time.Second)
	for f.s.Connected(1) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	log := f.log.String()
	if n := strings.Count(log, "unknown call"); n != maxFaults {
		t.Errorf("%d lines of unknown calls, want %d", n, maxFaults)
	}
	if !strings.Contains(log, "further faults of this connection aren't logged") {
		t.Errorf("log without the end of its faults: %s", log)
	}
	if len(log) > 8<<10 {
		t.Errorf("%d bytes of log from one connection", len(log))
	}

	// the hello's own texts: refused, and short in the log
	f = newFixture(t)
	bad := f.connect()
	bad.write(`{"call":"` + strings.Repeat("x", MaxLine/2) + `"}`)
	if got := bad.line(); len(got) > 2*logClip {
		t.Errorf("a refusal of %d bytes", len(got))
	}
	if log := f.log.String(); len(log) > 1<<10 {
		t.Errorf("%d bytes of log for one refused hello", len(log))
	}
}

// plain keeps a line one line.
func TestPlain(t *testing.T) {
	if got := plain("a\r\nb\x7f\x00c"); got != "a??b??c" {
		t.Errorf("got %q", got)
	}
	if got := plain(strings.Repeat("x", logClip+1)); len(got) != logClip+len("...") {
		t.Errorf("%d bytes", len(got))
	}
}

// A name too long for anyone is refused before anything looks it up.
func TestHelloLongName(t *testing.T) {
	f := newFixture(t)
	c := f.connect()
	c.write(`{"call":"hello","args":{"protocol":1,"name":"` + strings.Repeat("x", MaxName+1) + `"}}`)
	if got := c.line(); !strings.Contains(got, `"event":"refused"`) {
		t.Errorf("got %s", got)
	}
	if n := f.identified.Load(); n != 0 {
		t.Errorf("Identify asked %d times", n)
	}
}

// A user's calls share one budget, however many connections it opens.
func TestCallBudgetPerUser(t *testing.T) {
	f := newFixture(t)
	calls := make(chan struct{}, 2*maxPerUser*callBurst)
	f.s.Handle("x", func(int64, json.RawMessage) error {
		calls <- struct{}{}
		return nil
	})
	var conns []*client
	for i := 0; i < maxPerUser; i++ {
		conns = append(conns, f.hello("alpha"))
	}
	for _, c := range conns {
		go func(c *client) {
			for i := 0; i < callBurst; i++ {
				if _, err := c.nc.Write([]byte(`{"call":"x"}` + "\n")); err != nil {
					return
				}
			}
		}(c)
	}
	time.Sleep(300 * time.Millisecond)
	// the burst plus what the 300 ms refilled, with room for a slow machine
	if n, most := len(calls), callBurst+callsPerSec*2; n > most {
		t.Errorf("%d calls got through (at most %d)", n, most)
	}
	if !strings.Contains(f.log.String(), "calls a second; closing") {
		t.Errorf("log: %s", f.log.String())
	}
}

// A reconnect is no way round a user's budget: it outlives the user's last connection until it has refilled.
func TestCallBudgetAcrossReconnects(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int64
	f.s.Handle("x", func(int64, json.RawMessage) error {
		calls.Add(1)
		return nil
	})
	start := time.Now()
	for i := 0; i < 10; i++ {
		c := f.hello("alpha")
		c.nc.SetWriteDeadline(time.Now().Add(2 * time.Second))
		for j := 0; j < callBurst*2; j++ {
			if _, err := c.nc.Write([]byte(`{"call":"x"}` + "\n")); err != nil {
				break // over the budget: the server has ended this connection
			}
		}
		c.nc.Close()
		for deadline := time.Now().Add(2 * time.Second); f.s.Connected(1) && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
	}
	// the burst, what the time refilled, and room for a slow machine; without the rule it is 10 bursts
	if n, most := calls.Load(), int64(callBurst+time.Since(start).Seconds()*callsPerSec+callsPerSec); n > most {
		t.Errorf("%d calls over 10 connections in %v (at most %d)", n, time.Since(start), most)
	}
}

// A budget goes once it has refilled, and a connected user keeps its own.
func TestBudgetForgottenOnceRefilled(t *testing.T) {
	f := newFixture(t)
	f.s.Handle("x", func(int64, json.RawMessage) error { return nil })
	stays := f.hello("bravo")
	c := f.hello("alpha")
	c.nc.SetWriteDeadline(time.Now().Add(2 * time.Second))
	for j := 0; j < callBurst*2; j++ {
		if _, err := c.nc.Write([]byte(`{"call":"x"}` + "\n")); err != nil {
			break
		}
	}
	c.nc.Close()
	for deadline := time.Now().Add(2 * time.Second); f.s.Connected(1) && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	has := func(user int64) bool {
		f.s.mu.Lock()
		defer f.s.mu.Unlock()
		return f.s.budgets[user] != nil
	}
	if !has(1) {
		t.Fatal("an empty budget went with its last connection")
	}
	f.s.mu.Lock()
	f.s.forgetBudgets(time.Now().Add(time.Duration(callBurst/callsPerSec+1) * time.Second))
	f.s.mu.Unlock()
	if has(1) {
		t.Error("a refilled budget of a user without a connection stayed")
	}
	if !has(2) {
		t.Error("a connected user lost its budget")
	}
	stays.nc.Close()
}

// A login that ends between the two looks at a hello leaves no connection behind (Recheck found nothing to end).
func TestHelloLoginEndsMeanwhile(t *testing.T) {
	f := newFixture(t)
	first := true
	f.s.Identify = func(net.Conn, string) (int64, bool) {
		if first {
			first = false
			return 1, true
		}
		return 0, false
	}
	c := f.connect()
	c.write(`{"call":"hello","args":{"protocol":1,"name":"alpha"}}`)
	if got := c.line(); !strings.Contains(got, `"event":"refused"`) {
		t.Errorf("got %s", got)
	}
	if f.s.Connected(1) {
		t.Error("a connection without a login stayed")
	}
}

// Recheck ends only the connections Identify no longer names.
func TestRecheck(t *testing.T) {
	f := newFixture(t)
	ended := make(chan int64, 2)
	f.s.OnDisconnect = func(user int64) { ended <- user }
	alpha := f.hello("alpha")
	bravo := f.hello("bravo")
	f.s.Recheck(1) // still logged in: nothing happens
	if !f.s.Send(1, "e", nil) {
		t.Fatal("Send: false")
	}
	if got := alpha.line(); got != `{"event":"e"}` {
		t.Errorf("got %s", got)
	}
	f.alphaGone.Store(true)
	f.s.Recheck(1)
	alpha.closed()
	select {
	case u := <-ended:
		if u != 1 {
			t.Errorf("OnDisconnect names %d", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no OnDisconnect")
	}
	if !f.s.Send(2, "e", nil) {
		t.Fatal("bravo's connection ended too")
	}
	if got := bravo.line(); got != `{"event":"e"}` {
		t.Errorf("got %s", got)
	}
}

func TestEventTooLong(t *testing.T) {
	f := newFixture(t)
	f.hello("alpha")
	if f.s.Send(1, "e", strings.Repeat("x", MaxLine)) {
		t.Error("an event over the limit was sent")
	}
	if !f.s.Connected(1) {
		t.Error("the connection ended")
	}
}
