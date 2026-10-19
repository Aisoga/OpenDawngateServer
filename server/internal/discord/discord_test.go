package discord

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeApp answers like the Discord app on the server ends of net.Pipe: READY to a handshake (or CLOSE when refuse
// is set), the command back with its nonce, PONG to a PING. Every frame it gets goes to frames.
type fakeApp struct {
	t      *testing.T
	refuse bool
	frames chan Frame
	mu     sync.Mutex
	conns  []net.Conn
	down   int // dials refused before the first one that connects (the app not running)
	dials  int
}

func newFakeApp(t *testing.T) *fakeApp { return &fakeApp{t: t, frames: make(chan Frame, 100)} }

func (a *fakeApp) dial() (Conn, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dials++
	if a.dials <= a.down {
		return nil, "", errors.New("the pipe doesn't exist")
	}
	client, server := net.Pipe()
	a.conns = append(a.conns, server)
	go a.serve(server)
	return client, fmt.Sprintf("fake%d", len(a.conns)), nil
}

// quit closes the app's end of every connection (the app quits).
func (a *fakeApp) quit() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.conns {
		c.Close()
	}
}

func (a *fakeApp) serve(c net.Conn) {
	defer c.Close()
	for {
		f, err := readFrame(c, time.Hour, time.Second)
		if err != nil {
			return
		}
		a.frames <- f
		switch f.Op {
		case OpHandshake:
			if a.refuse {
				WriteFrame(c, OpClose, map[string]any{"code": 4000, "message": "Invalid Client ID"})
				return
			}
			WriteFrame(c, OpFrame, map[string]any{"cmd": "DISPATCH", "evt": "READY", "data": map[string]any{"v": 1}, "nonce": nil})
		case OpFrame:
			var m struct {
				Cmd   string          `json:"cmd"`
				Args  json.RawMessage `json:"args"`
				Nonce string          `json:"nonce"`
			}
			json.Unmarshal(f.JSON, &m)
			WriteFrame(c, OpFrame, map[string]any{"cmd": m.Cmd, "data": m.Args, "evt": nil, "nonce": m.Nonce})
		case OpPing:
			writeRaw(c, OpPong, f.JSON)
		}
	}
}

// next is the app's next frame of op (others skipped).
func (a *fakeApp) next(op uint32, within time.Duration) (Frame, bool) {
	deadline := time.After(within)
	for {
		select {
		case f := <-a.frames:
			if f.Op == op {
				return f, true
			}
		case <-deadline:
			return Frame{}, false
		}
	}
}

// setActivity is a SET_ACTIVITY frame's args.
type setActivity struct {
	Cmd  string `json:"cmd"`
	Args struct {
		Pid      int             `json:"pid"`
		Activity json.RawMessage `json:"activity"`
	} `json:"args"`
	Nonce string `json:"nonce"`
}

func decodeSet(t *testing.T, f Frame) setActivity {
	t.Helper()
	var s setActivity
	if err := json.Unmarshal(f.JSON, &s); err != nil || s.Cmd != "SET_ACTIVITY" {
		t.Fatalf("not a SET_ACTIVITY: %s (%v)", f.JSON, err)
	}
	return s
}

type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, a ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, a...))
	l.mu.Unlock()
}

func (l *logLines) has(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, x := range l.lines {
		if strings.Contains(x, s) {
			return true
		}
	}
	return false
}

func fastOptions(a *fakeApp, l *logLines) Options {
	return Options{AppID: "1234567890", Pid: 4242, Dial: a.dial, Logf: l.logf, Retry: 50 * time.Millisecond,
		MinGap: 10 * time.Millisecond, ReplyWait: time.Second, Poll: 20 * time.Millisecond}
}

func TestHandshakeAndActivity(t *testing.T) {
	app := newFakeApp(t)
	var l logLines
	p := New(fastOptions(app, &l))
	defer p.Close()
	start := time.Unix(1760000000, 0)
	p.Set(&Activity{Details: "In match · PvP", State: "In a party", Start: start, PartySize: 3, PartyMax: 5,
		LargeImage: "logo", LargeText: "Dawngate"})
	h, ok := app.next(OpHandshake, 2*time.Second)
	if !ok {
		t.Fatal("no handshake")
	}
	if got := string(h.JSON); got != `{"client_id":"1234567890","v":1}` {
		t.Errorf("handshake %s", got)
	}
	f, ok := app.next(OpFrame, 2*time.Second)
	if !ok {
		t.Fatal("no SET_ACTIVITY")
	}
	s := decodeSet(t, f)
	if s.Args.Pid != 4242 || s.Nonce == "" {
		t.Errorf("pid %d nonce %q", s.Args.Pid, s.Nonce)
	}
	want := `{"details":"In match · PvP","state":"In a party","timestamps":{"start":1760000000},` +
		`"assets":{"large_image":"logo","large_text":"Dawngate"},"party":{"size":[3,5]}}`
	if string(s.Args.Activity) != want {
		t.Errorf("activity\n got %s\nwant %s", s.Args.Activity, want)
	}
	time.Sleep(50 * time.Millisecond)
	if !l.has("discord: connected to the Discord app (fake1)") || !l.has("discord: activity In match · PvP / In a party (3 of 5)") {
		t.Errorf("log %q", l.lines)
	}
	// an equal activity sends nothing
	p.Set(&Activity{Details: "In match · PvP", State: "In a party", Start: start, PartySize: 3, PartyMax: 5,
		LargeImage: "logo", LargeText: "Dawngate"})
	if f, ok := app.next(OpFrame, 200*time.Millisecond); ok {
		t.Errorf("an equal activity was sent again: %s", f.JSON)
	}
}

func TestMinimalActivity(t *testing.T) {
	a := &Activity{Details: "In the menu"}
	b, _ := json.Marshal(a.JSON())
	if string(b) != `{"details":"In the menu"}` {
		t.Errorf("%s", b)
	}
	if b, _ := json.Marshal((*Activity)(nil).JSON()); string(b) != "null" {
		t.Errorf("nil: %s", b)
	}
}

func TestPingPong(t *testing.T) {
	app := newFakeApp(t)
	var l logLines
	client, server := net.Pipe()
	o := fastOptions(app, &l)
	dialed := make(chan struct{})
	o.Dial = func() (Conn, string, error) { close(dialed); return client, "pinged", nil }
	p := New(o)
	defer p.Close()
	p.Set(&Activity{Details: "In the menu"})
	<-dialed
	if f, err := readFrame(server, time.Second, time.Second); err != nil || f.Op != OpHandshake {
		t.Fatalf("handshake: %v %+v", err, f)
	}
	WriteFrame(server, OpFrame, map[string]any{"cmd": "DISPATCH", "evt": "READY", "nonce": nil})
	f, err := readFrame(server, time.Second, time.Second)
	if err != nil || f.Op != OpFrame {
		t.Fatalf("SET_ACTIVITY: %v %+v", err, f)
	}
	WriteFrame(server, OpFrame, map[string]any{"cmd": "SET_ACTIVITY", "evt": nil, "nonce": decodeSet(t, f).Nonce})
	// the app pings between updates: the poll answers
	writeRaw(server, OpPing, []byte(`{"ping":7}`))
	f, err = readFrame(server, time.Second, time.Second)
	if err != nil || f.Op != OpPong || string(f.JSON) != `{"ping":7}` {
		t.Fatalf("PONG: %v op %d %s", err, f.Op, f.JSON)
	}
	go io.Copy(io.Discard, server) // the clear at Close
}

func TestClearOnClose(t *testing.T) {
	app := newFakeApp(t)
	var l logLines
	p := New(fastOptions(app, &l))
	p.Set(&Activity{Details: "In the menu"})
	if _, ok := app.next(OpFrame, 2*time.Second); !ok {
		t.Fatal("no SET_ACTIVITY")
	}
	time.Sleep(30 * time.Millisecond)
	p.Close()
	f, ok := app.next(OpFrame, time.Second)
	if !ok {
		t.Fatal("no clear at Close")
	}
	if s := decodeSet(t, f); string(s.Args.Activity) != "null" {
		t.Errorf("the clear: %s", f.JSON)
	}
	if !l.has("activity cleared (the game ended)") {
		t.Errorf("log %q", l.lines)
	}
	p.Close() // twice: nothing
}

func TestCloseWithoutApp(t *testing.T) {
	app := newFakeApp(t)
	app.down = 1000
	var l logLines
	p := New(fastOptions(app, &l))
	p.Set(&Activity{Details: "In the menu"})
	time.Sleep(200 * time.Millisecond)
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung")
	}
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, "no Discord app") {
			n++
		}
	}
	if n != 1 || app.dials < 3 {
		t.Errorf("%d failure lines (want 1) after %d dials: %q", n, app.dials, l.lines)
	}
}

func TestReconnect(t *testing.T) {
	app := newFakeApp(t)
	app.down = 2 // the app isn't running for the first two tries
	var l logLines
	p := New(fastOptions(app, &l))
	defer p.Close()
	p.Set(&Activity{Details: "In queue · PvP"})
	f, ok := app.next(OpFrame, 2*time.Second)
	if !ok {
		t.Fatal("no SET_ACTIVITY after the app came up")
	}
	if s := decodeSet(t, f); !strings.Contains(string(s.Args.Activity), "In queue") {
		t.Errorf("%s", f.JSON)
	}
	if app.dials != 3 {
		t.Errorf("dials %d, want 3", app.dials)
	}
	// the app quits and comes back: the activity again on the new connection
	time.Sleep(30 * time.Millisecond)
	app.quit()
	if _, ok := app.next(OpHandshake, 2*time.Second); !ok {
		t.Fatal("no new handshake after the app quit")
	}
	f, ok = app.next(OpFrame, 2*time.Second)
	if !ok {
		t.Fatal("the activity wasn't sent again")
	}
	if s := decodeSet(t, f); !strings.Contains(string(s.Args.Activity), "In queue") {
		t.Errorf("%s", f.JSON)
	}
	if !l.has("connection to the Discord app ended") {
		t.Errorf("log %q", l.lines)
	}
}

func TestRefusedHandshake(t *testing.T) {
	app := newFakeApp(t)
	app.refuse = true
	var l logLines
	p := New(fastOptions(app, &l))
	p.Set(&Activity{Details: "In the menu"})
	time.Sleep(150 * time.Millisecond)
	p.Close()
	if !l.has("refused the handshake (4000 Invalid Client ID)") {
		t.Errorf("log %q", l.lines)
	}
}

func TestThrottle(t *testing.T) {
	app := newFakeApp(t)
	var l logLines
	o := fastOptions(app, &l)
	o.MinGap = 400 * time.Millisecond
	p := New(o)
	defer p.Close()
	// connected and idle first
	p.Set(&Activity{Details: "In the menu"})
	if _, ok := app.next(OpFrame, 2*time.Second); !ok {
		t.Fatal("no first SET_ACTIVITY")
	}
	time.Sleep(500 * time.Millisecond) // past the gap
	for i := 1; i <= 5; i++ {
		p.Set(&Activity{Details: fmt.Sprintf("update %d", i)})
	}
	var got []string
	deadline := time.After(1500 * time.Millisecond)
loop:
	for {
		select {
		case f := <-app.frames:
			if f.Op == OpFrame {
				got = append(got, string(decodeSet(t, f).Args.Activity))
			}
		case <-deadline:
			break loop
		}
	}
	if len(got) == 0 || len(got) > 2 {
		t.Fatalf("%d updates sent for 5 quick ones (want 1 or 2): %q", len(got), got)
	}
	if !strings.Contains(got[len(got)-1], "update 5") {
		t.Errorf("the last one sent isn't the last one set: %q", got)
	}
}

func TestPipeNames(t *testing.T) {
	n := PipeNames("")
	if len(n) != 10 || !strings.HasSuffix(n[0], "discord-ipc-0") || !strings.HasSuffix(n[9], "discord-ipc-9") {
		t.Errorf("%q", n)
	}
	if n := PipeNames(`\\.\pipe\dg-test`); len(n) != 1 || !strings.HasSuffix(n[0], "dg-test") || strings.Count(n[0], "pipe") > 1 {
		t.Errorf("%q", n)
	}
	for _, bad := range []string{"discord-ipc-0", `\\.\pipe\Discord-IPC-3`, "", "a/b", `a\b`} {
		if CheckTestPipe(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := CheckTestPipe("dawngate-test-discord-b"); err != nil {
		t.Error(err)
	}
}

func TestJoinActivityJSON(t *testing.T) {
	a := &Activity{Details: "In the menu", State: "In a party", PartySize: 2, PartyMax: 5, PartyID: "p1", JoinSecret: "dg1/x/y/z"}
	b, _ := json.Marshal(a.JSON())
	want := `{"details":"In the menu","state":"In a party","party":{"id":"p1","size":[2,5],"privacy":1},"secrets":{"join":"dg1/x/y/z"}}`
	if string(b) != want {
		t.Errorf("\n got %s\nwant %s", b, want)
	}
	if !strings.HasSuffix(a.String(), ", joinable") {
		t.Errorf("%s", a)
	}
	// no secret without a free seat, without a party id, equal to the party id, or too long: no privacy either
	for _, c := range []Activity{
		{Details: "x", PartySize: 5, PartyMax: 5, PartyID: "p1", JoinSecret: "s1"},
		{Details: "x", PartySize: 2, PartyMax: 5, JoinSecret: "s1"},
		{Details: "x", PartySize: 2, PartyMax: 5, PartyID: "s1", JoinSecret: "s1"},
		{Details: "x", PartySize: 2, PartyMax: 5, PartyID: "p1", JoinSecret: strings.Repeat("a", MaxSecret+1)},
		{Details: "x", PartyID: "p1", JoinSecret: "s1"},
	} {
		b, _ := json.Marshal(c.JSON())
		if strings.Contains(string(b), "secrets") || strings.Contains(string(b), "privacy") {
			t.Errorf("%+v: %s", c, b)
		}
	}
	if (&Activity{PartyID: "a"}).equal(&Activity{PartyID: "b"}) || (&Activity{JoinSecret: "a"}).equal(&Activity{JoinSecret: "b"}) {
		t.Error("equal ignores the join fields")
	}
}

func TestSubscribeAndJoin(t *testing.T) {
	app := newFakeApp(t)
	var l logLines
	got := make(chan string, 2)
	o := fastOptions(app, &l)
	o.OnJoin = func(s string) { got <- s }
	p := New(o)
	defer p.Close()
	p.Set(&Activity{Details: "In the menu"})
	if _, ok := app.next(OpHandshake, 2*time.Second); !ok {
		t.Fatal("no handshake")
	}
	f, ok := app.next(OpFrame, 2*time.Second)
	if !ok {
		t.Fatal("no frame after READY")
	}
	var sub struct {
		Cmd, Evt, Nonce string
	}
	if json.Unmarshal(f.JSON, &sub); sub.Cmd != "SUBSCRIBE" || sub.Evt != "ACTIVITY_JOIN" || sub.Nonce == "" {
		t.Fatalf("first frame %s, want the SUBSCRIBE", f.JSON)
	}
	if f, ok := app.next(OpFrame, 2*time.Second); !ok || decodeSet(t, f).Cmd != "SET_ACTIVITY" {
		t.Fatal("no SET_ACTIVITY after the SUBSCRIBE")
	}
	app.mu.Lock()
	c := app.conns[0]
	app.mu.Unlock()
	go WriteFrame(c, OpFrame, map[string]any{"cmd": "DISPATCH", "evt": "ACTIVITY_JOIN", "data": map[string]any{"secret": "dg1/s"}, "nonce": nil})
	select {
	case s := <-got:
		if s != "dg1/s" {
			t.Errorf("secret %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnJoin not called")
	}
	go WriteFrame(c, OpFrame, map[string]any{"cmd": "DISPATCH", "evt": "ACTIVITY_JOIN", "data": map[string]any{}, "nonce": nil})
	time.Sleep(200 * time.Millisecond)
	if len(got) != 0 || !l.has("subscribed to ACTIVITY_JOIN") || !l.has("without a secret") {
		t.Errorf("log %q", l.lines)
	}
	// without OnJoin: no SUBSCRIBE
	app2 := newFakeApp(t)
	p2 := New(fastOptions(app2, &l))
	defer p2.Close()
	p2.Set(&Activity{Details: "In the menu"})
	if f, ok := app2.next(OpFrame, 2*time.Second); !ok || decodeSet(t, f).Cmd != "SET_ACTIVITY" {
		t.Fatal("without OnJoin the first frame isn't SET_ACTIVITY")
	}
}
