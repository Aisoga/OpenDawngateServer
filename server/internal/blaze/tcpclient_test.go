package blaze

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// tcpServer is a Blaze server on an ephemeral loopback port (B10a: several fake clients at once).
type tcpServer struct {
	t   *testing.T
	s   *Server
	ln  net.Listener
	log *lockedBuf
}

func newTCPServer(t *testing.T, setup func(s *Server)) *tcpServer {
	ts := &tcpServer{t: t, log: &lockedBuf{}}
	ts.s = NewServer(logging.New(ts.log), "127.0.0.1", 17502)
	ts.s.TelemetryPort = 9988
	ts.s.NewKey = func() string { return testKey }
	if setup != nil {
		setup(ts.s)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ts.ln = ln
	go ts.s.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	return ts
}

// tcpClient is one fake client: it reads every frame into a queue so a test can wait for the frame it
// expects while notifications for other things arrive in between.
type tcpClient struct {
	t      *testing.T
	c      net.Conn
	name   string
	id     int64 // persona id after login
	nextID uint32

	mu     sync.Mutex
	frames []fire.Frame
	signal chan struct{}
	err    error
}

func (ts *tcpServer) client(email string) *tcpClient {
	ts.t.Helper()
	c, err := net.Dial("tcp4", ts.ln.Addr().String())
	if err != nil {
		ts.t.Fatal(err)
	}
	tc := &tcpClient{t: ts.t, c: c, name: store.PersonaName(email), signal: make(chan struct{}, 1)}
	ts.t.Cleanup(func() { c.Close() })
	go func() {
		r := fire.NewReader(c)
		for {
			f, err := r.Next()
			tc.mu.Lock()
			if err != nil {
				tc.err = err
			} else {
				tc.frames = append(tc.frames, f)
			}
			tc.mu.Unlock()
			select {
			case tc.signal <- struct{}{}:
			default:
			}
			if err != nil {
				return
			}
		}
	}()
	tc.login(email)
	return tc
}

func (tc *tcpClient) send(comp, cmd uint16, payload []byte) uint32 {
	tc.t.Helper()
	id := tc.nextID
	tc.nextID++
	b, err := fire.Encode(fire.Frame{Component: comp, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
	if err != nil {
		tc.t.Fatal(err)
	}
	if _, err := tc.c.Write(b); err != nil {
		tc.t.Fatal(err)
	}
	return id
}

// wait returns the first queued frame that matches (and removes it), waiting up to 5 s.
func (tc *tcpClient) wait(what string, match func(f fire.Frame) bool) fire.Frame {
	tc.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		tc.mu.Lock()
		for i, f := range tc.frames {
			if match(f) {
				tc.frames = append(tc.frames[:i], tc.frames[i+1:]...)
				tc.mu.Unlock()
				return f
			}
		}
		err := tc.err
		tc.mu.Unlock()
		if err != nil {
			tc.t.Fatalf("%s: %s: connection ended: %v", tc.name, what, err)
		}
		select {
		case <-tc.signal:
		case <-deadline:
			tc.t.Fatalf("%s: no %s (queued: %s)", tc.name, what, tc.queued())
		}
	}
}

// reply waits for the reply (or error reply) to request id.
func (tc *tcpClient) reply(comp, cmd uint16, id uint32) fire.Frame {
	tc.t.Helper()
	return tc.wait(fmt.Sprintf("reply %#x/%#x id %d", comp, cmd, id), func(f fire.Frame) bool {
		return f.Component == comp && f.Command == cmd && f.ID == id && (f.Type == fire.Reply || f.Type == fire.ErrorReply)
	})
}

// note waits for a notification.
func (tc *tcpClient) note(comp, id uint16) fire.Frame {
	tc.t.Helper()
	return tc.wait(fmt.Sprintf("notification %#x/%#x", comp, id), func(f fire.Frame) bool {
		return f.Component == comp && f.Command == id && f.Type == fire.Notification
	})
}

// none checks that no notification comp/id is queued after a short wait.
func (tc *tcpClient) none(comp, id uint16, within time.Duration) {
	tc.t.Helper()
	time.Sleep(within)
	tc.mu.Lock()
	defer tc.mu.Unlock()
	for _, f := range tc.frames {
		if f.Component == comp && f.Command == id && f.Type == fire.Notification {
			tc.t.Errorf("%s: unexpected notification %#x/%#x", tc.name, comp, id)
		}
	}
}

func (tc *tcpClient) queued() string {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	s := ""
	for _, f := range tc.frames {
		s += fmt.Sprintf(" %#x/%#x(%s id %d)", f.Component, f.Command, f.Type, f.ID)
	}
	return s
}

func (tc *tcpClient) login(email string) {
	tc.t.Helper()
	id := tc.send(1, 0x28, tdf.NewWriter().String("MAIL", email).String("PASS", "x").String("TOKN", "").Int("TYPE", 0).Bytes())
	tc.reply(1, 0x28, id)
	id = tc.send(1, 0x6E, tdf.NewWriter().String("PNAM", tc.name).Bytes())
	f := tc.reply(1, 0x6E, id)
	st, err := tdf.Decode(f.Payload)
	if err != nil {
		tc.t.Fatal(err)
	}
	tc.id, _ = st.Int("BUID")
	tc.note(0x7802, 8)
	tc.note(0x7802, 1)
}

// decode returns a frame's payload, decoded.
func decode(t *testing.T, f fire.Frame) *tdf.Struct {
	t.Helper()
	st, err := tdf.Decode(f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
