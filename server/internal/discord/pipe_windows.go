//go:build windows

package discord

import (
	"io"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procPeekNamedPipe = kernel32.NewProc("PeekNamedPipe")
)

func pipePath(name string) string { return `\\.\pipe\` + name }

// pipeConn is the client end of a named pipe. os.OpenFile opens it for synchronous I/O, where Windows runs one
// operation at a time per handle: a ReadFile waiting for data would hold every WriteFile back. So Read never waits
// inside ReadFile: it asks PeekNamedPipe how many bytes are there and reads only those, polling until the deadline,
// which also gives the deadline the pipe itself doesn't have. PeekNamedPipe fails once the app has closed its end.
type pipeConn struct {
	f *os.File
	h syscall.Handle

	mu       sync.Mutex
	deadline time.Time
	closed   bool
}

// pipePollEvery is how often Read looks for data while it waits.
const pipePollEvery = 10 * time.Millisecond

func dialPipe(path string) (Conn, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return &pipeConn{f: f, h: syscall.Handle(f.Fd())}, nil
}

func (c *pipeConn) available() (int, error) {
	var n uint32
	r, _, err := procPeekNamedPipe.Call(uintptr(c.h), 0, 0, 0, uintptr(unsafe.Pointer(&n)), 0)
	if r == 0 {
		if err == syscall.ERROR_BROKEN_PIPE {
			return 0, io.EOF
		}
		return 0, err
	}
	return int(n), nil
}

func (c *pipeConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	for {
		c.mu.Lock()
		closed, deadline := c.closed, c.deadline
		c.mu.Unlock()
		if closed {
			return 0, os.ErrClosed
		}
		n, err := c.available()
		if err != nil {
			return 0, err
		}
		if n > 0 {
			return c.f.Read(b[:min(n, len(b))])
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return 0, os.ErrDeadlineExceeded
		}
		time.Sleep(pipePollEvery)
	}
}

func (c *pipeConn) Write(b []byte) (int, error) { return c.f.Write(b) }

func (c *pipeConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}

func (c *pipeConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	return c.f.Close()
}
