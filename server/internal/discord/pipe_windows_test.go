//go:build windows

package discord

import (
	"encoding/json"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var (
	procCreateNamedPipeW    = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe    = kernel32.NewProc("ConnectNamedPipe")
	procDisconnectNamedPipe = kernel32.NewProc("DisconnectNamedPipe")
)

// pipeServer is the app's end of a real named pipe, read and written synchronously from one goroutine.
type pipeServer struct{ h syscall.Handle }

func newPipeServer(t *testing.T, name string) *pipeServer {
	t.Helper()
	p, _ := syscall.UTF16PtrFromString(pipePath(name))
	const (
		pipeAccessDuplex = 0x3
		pipeTypeByte     = 0x0
		pipeWait         = 0x0
	)
	h, _, err := procCreateNamedPipeW.Call(uintptr(unsafe.Pointer(p)), pipeAccessDuplex, pipeTypeByte|pipeWait, 1, 65536, 65536, 0, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		t.Fatalf("CreateNamedPipeW: %v", err)
	}
	return &pipeServer{h: syscall.Handle(h)}
}

func (s *pipeServer) accept() error {
	r, _, err := procConnectNamedPipe.Call(uintptr(s.h), 0)
	if r == 0 && err != syscall.Errno(535) { // ERROR_PIPE_CONNECTED: the client came first
		return err
	}
	return nil
}

func (s *pipeServer) Read(b []byte) (int, error) {
	var n uint32
	err := syscall.ReadFile(s.h, b, &n, nil)
	return int(n), err
}

func (s *pipeServer) Write(b []byte) (int, error) {
	var n uint32
	err := syscall.WriteFile(s.h, b, &n, nil)
	return int(n), err
}

func (s *pipeServer) readFrame() (Frame, error) {
	var hdr [8]byte
	if _, err := readFull(s, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := int(hdr[4]) | int(hdr[5])<<8 | int(hdr[6])<<16 | int(hdr[7])<<24
	body := make([]byte, n)
	_, err := readFull(s, body)
	return Frame{Op: uint32(hdr[0]), JSON: body}, err
}

func readFull(s *pipeServer, b []byte) (int, error) {
	got := 0
	for got < len(b) {
		n, err := s.Read(b[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

func (s *pipeServer) close() {
	procDisconnectNamedPipe.Call(uintptr(s.h))
	syscall.CloseHandle(s.h)
}

// TestPipeConn runs a Presence through a real named pipe (a name of the test's own, never the app's): the
// handshake, an activity, a PING answered between updates while nothing is read, the clear at Close; then the
// server's end closed: the client's read ends.
func TestPipeConn(t *testing.T) {
	name := fmt.Sprintf("dawngate-test-discord-gotest-%d", os.Getpid())
	srv := newPipeServer(t, name)
	defer srv.close()
	var l logLines
	p := New(Options{AppID: "42", Pid: 7, Dial: DialPipes(PipeNames(name)), Logf: l.logf, Retry: 50 * time.Millisecond,
		MinGap: 10 * time.Millisecond, ReplyWait: 2 * time.Second, Poll: 20 * time.Millisecond})
	p.Set(&Activity{Details: "In the menu"})
	if err := srv.accept(); err != nil {
		t.Fatalf("ConnectNamedPipe: %v", err)
	}
	f, err := srv.readFrame()
	if err != nil || f.Op != OpHandshake || string(f.JSON) != `{"client_id":"42","v":1}` {
		t.Fatalf("handshake: %v op %d %s", err, f.Op, f.JSON)
	}
	WriteFrame(srv, OpFrame, map[string]any{"cmd": "DISPATCH", "evt": "READY", "nonce": nil})
	f, err = srv.readFrame()
	if err != nil || f.Op != OpFrame {
		t.Fatalf("SET_ACTIVITY: %v %+v", err, f)
	}
	var s setActivity
	json.Unmarshal(f.JSON, &s)
	if s.Args.Pid != 7 || string(s.Args.Activity) != `{"details":"In the menu"}` {
		t.Errorf("%s", f.JSON)
	}
	WriteFrame(srv, OpFrame, map[string]any{"cmd": "SET_ACTIVITY", "evt": nil, "nonce": s.Nonce})
	time.Sleep(100 * time.Millisecond)
	writeRaw(srv, OpPing, []byte(`{"n":1}`))
	if f, err = srv.readFrame(); err != nil || f.Op != OpPong || string(f.JSON) != `{"n":1}` {
		t.Fatalf("PONG: %v op %d %s", err, f.Op, f.JSON)
	}
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	f, err = srv.readFrame()
	if err != nil || f.Op != OpFrame {
		t.Fatalf("the clear: %v %+v", err, f)
	}
	json.Unmarshal(f.JSON, &s)
	if string(s.Args.Activity) != "null" {
		t.Errorf("the clear: %s", f.JSON)
	}
	WriteFrame(srv, OpFrame, map[string]any{"cmd": "SET_ACTIVITY", "evt": nil, "nonce": s.Nonce})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung")
	}
	if !l.has("connected to the Discord app") || !l.has("activity cleared (the game ended)") {
		t.Errorf("log %q", l.lines)
	}
}

// TestPipeConnServerGone: the app's end closed under a connected Presence: the poll notices and the Presence
// tries again.
func TestPipeConnServerGone(t *testing.T) {
	name := fmt.Sprintf("dawngate-test-discord-gotest2-%d", os.Getpid())
	srv := newPipeServer(t, name)
	var l logLines
	p := New(Options{AppID: "42", Pid: 7, Dial: DialPipes(PipeNames(name)), Logf: l.logf, Retry: 50 * time.Millisecond,
		MinGap: 10 * time.Millisecond, ReplyWait: 2 * time.Second, Poll: 20 * time.Millisecond})
	defer p.Close()
	p.Set(&Activity{Details: "In the menu"})
	if err := srv.accept(); err != nil {
		t.Fatal(err)
	}
	srv.readFrame()
	WriteFrame(srv, OpFrame, map[string]any{"cmd": "DISPATCH", "evt": "READY", "nonce": nil})
	f, _ := srv.readFrame()
	var s setActivity
	json.Unmarshal(f.JSON, &s)
	WriteFrame(srv, OpFrame, map[string]any{"cmd": "SET_ACTIVITY", "evt": nil, "nonce": s.Nonce})
	time.Sleep(100 * time.Millisecond)
	srv.close() // the app quits
	deadline := time.Now().Add(3 * time.Second)
	for !l.has("connection to the Discord app ended") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !l.has("connection to the Discord app ended") {
		t.Errorf("log %q", l.lines)
	}
}

// TestAgainstFakeApp runs a Presence against a fake Discord app that serves the pipe DAWNGATE_DISCORD_FAKE_PIPE names
// (skipped otherwise): an activity, a change, the clear at Close.
func TestAgainstFakeApp(t *testing.T) {
	name := os.Getenv("DAWNGATE_DISCORD_FAKE_PIPE")
	if name == "" {
		t.Skip("DAWNGATE_DISCORD_FAKE_PIPE not set (start a fake Discord app on a pipe of that name first)")
	}
	if err := CheckTestPipe(name); err != nil {
		t.Fatal(err)
	}
	var l logLines
	p := New(Options{AppID: "1", Pid: os.Getpid(), Dial: DialPipes(PipeNames(name)), Logf: l.logf, MinGap: 100 * time.Millisecond})
	p.Set(&Activity{Details: "In the menu", LargeImage: "logo", LargeText: "Dawngate"})
	time.Sleep(500 * time.Millisecond)
	p.Set(&Activity{Details: "In match · Practice vs bots", Start: time.Now()})
	time.Sleep(500 * time.Millisecond)
	p.Close()
	for _, s := range l.lines {
		t.Log(s)
	}
	if !l.has("activity In match") || !l.has("activity cleared (the game ended)") {
		t.Errorf("log %q", l.lines)
	}
}
