package gmshost

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// HostConn is the host side of the control channel in Go: what gms/core/control does in C++. The
// tests' fake hosts use it.
type HostConn struct {
	c  net.Conn
	r  *LineReader
	mu sync.Mutex
}

// DialHost dials env.Control and sends hello with env's match and token.
func DialHost(env Env, pid int, build string) (*HostConn, error) {
	c, err := net.DialTimeout("tcp4", env.Control, 5*time.Second)
	if err != nil {
		return nil, err
	}
	h := &HostConn{c: c, r: NewLineReader(c)}
	if err := h.Send(Hello{Match: env.Match, Token: env.Token, PID: pid, Build: build}); err != nil {
		c.Close()
		return nil, err
	}
	return h, nil
}

// Send writes one message.
func (h *HostConn) Send(msg Message) error {
	line, err := Encode(msg)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err = h.c.Write(line)
	return err
}

// Recv reads the next message (timeout 0 = none).
func (h *HostConn) Recv(timeout time.Duration) (Message, error) {
	if timeout > 0 {
		h.c.SetReadDeadline(time.Now().Add(timeout))
	} else {
		h.c.SetReadDeadline(time.Time{})
	}
	line, err := h.r.Next()
	if err != nil {
		return nil, err
	}
	return Decode(line)
}

// Close closes the connection.
func (h *HostConn) Close() error { return h.c.Close() }

// FakeProcess is a Process played by a goroutine (tests, fake hosts).
type FakeProcess struct {
	pid    int
	killed chan struct{}
	done   chan struct{}
	once   sync.Once
	kill   sync.Once
	code   int
}

var fakePIDs atomic.Int64

// NewFakeProcess returns a running fake process with a made-up pid.
func NewFakeProcess() *FakeProcess {
	return &FakeProcess{pid: int(90000 + fakePIDs.Add(1)), killed: make(chan struct{}), done: make(chan struct{})}
}

func (p *FakeProcess) Pid() int { return p.pid }

// Kill marks the process killed and ends it with exit code 1 (like TerminateProcess(h, 1)).
func (p *FakeProcess) Kill() error {
	p.kill.Do(func() { close(p.killed) })
	p.Exit(1)
	return nil
}

// Killed is closed once Kill was called.
func (p *FakeProcess) Killed() <-chan struct{} { return p.killed }

// Exit ends the process with code (the first call wins).
func (p *FakeProcess) Exit(code int) {
	p.once.Do(func() {
		p.code = code
		close(p.done)
	})
}

// Done is closed when the process has ended.
func (p *FakeProcess) Done() <-chan struct{} { return p.done }

func (p *FakeProcess) Wait() (int, error) {
	<-p.done
	return p.code, nil
}

// FakeLauncher starts fake hosts: Run plays the host in a goroutine with the environment the real
// host would get. Err makes every launch fail.
type FakeLauncher struct {
	Run func(env Env, p *FakeProcess)
	Err error

	mu   sync.Mutex
	envs []Env
}

// Launch starts Run in a goroutine.
func (l *FakeLauncher) Launch(env Env) (Process, error) {
	if l.Err != nil {
		return nil, l.Err
	}
	l.mu.Lock()
	l.envs = append(l.envs, env)
	l.mu.Unlock()
	p := NewFakeProcess()
	go l.Run(env, p)
	return p, nil
}

// Envs returns the environments of every launch so far.
func (l *FakeLauncher) Envs() []Env {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Env(nil), l.envs...)
}

// PlayHost is a scripted fake host: it dials, says hello, waits for createGame (a warm host up to a
// minute, else 10 s), answers gameReady
// with the port from env.Bind, then runs script (which may be nil) and, when script returns, sends
// logoutGMS, closes and exits 0. It returns early (exit 1) if the connection fails, and stops when
// the process is killed.
func PlayHost(env Env, p *FakeProcess, script func(h *HostConn, cg CreateGame) error) {
	h, err := DialHost(env, p.Pid(), "fake")
	if err != nil {
		p.Exit(1)
		return
	}
	go func() { <-p.Done(); h.Close() }() // a kill drops the connection, as a dying process does
	wait := 10 * time.Second
	if env.Warm { // H1-live: a warm host waits for its game
		wait = time.Minute
	}
	msg, err := h.Recv(wait)
	cg, ok := msg.(CreateGame)
	if err != nil || !ok {
		h.Close()
		p.Exit(1)
		return
	}
	_, port, _ := net.SplitHostPort(env.Bind)
	var n int
	fmt.Sscan(port, &n)
	if err := h.Send(GameReady{Port: n}); err != nil {
		p.Exit(1)
		return
	}
	if script != nil {
		if err := script(h, cg); err != nil {
			h.Close()
			p.Exit(2)
			return
		}
	}
	h.Send(LogoutGMS{})
	h.Close()
	p.Exit(0)
}
