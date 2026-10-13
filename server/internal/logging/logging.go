// Package logging writes the server log: one timestamped line per event, "HH:MM:SS.ffffff [who] msg",
// to stdout and to <dir>/<YYYYMMDD-HHMMSS>.log. The format is the Python stub's (blaze_stub.py, before
// S1), which re/scripts/run_session.ps1 parses for its summary; keep the two in step.
package logging

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// MaxDump is how much of an HTTP body or a telemetry chunk is hexdumped.
const MaxDump = 2048

// Log is safe for concurrent use; each message is written as one block.
type Log struct {
	Path string // the log file

	mu  sync.Mutex
	f   io.WriteCloser
	out io.Writer
	now func() time.Time
}

// Open creates dir if needed and a new log file named after the current time in it.
func Open(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+".log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Log{Path: path, f: f, out: os.Stdout, now: time.Now}, nil
}

// New returns a Log that writes only to w (tests).
func New(w io.Writer) *Log { return &Log{out: w, now: time.Now} }

// newline matches the Python stub, whose text-mode log file had CRLF line ends on Windows.
var newline = map[bool]string{true: "\r\n", false: "\n"}[runtime.GOOS == "windows"]

// Log writes one message; msg may span several lines.
func (l *Log) Log(who, msg string) {
	line := l.now().Format("15:04:05.000000") + " [" + who + "] " + msg
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.out != nil {
		fmt.Fprintln(l.out, line)
	}
	if l.f != nil {
		io.WriteString(l.f, strings.ReplaceAll(line, "\n", newline)+newline)
	}
}

// Logf formats a message.
func (l *Log) Logf(who, format string, args ...any) { l.Log(who, fmt.Sprintf(format, args...)) }

// Close closes the log file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Hexdump renders b as indented lines of 16 bytes: offset, hex, ASCII.
func Hexdump(b []byte) string {
	const width = 16
	var sb strings.Builder
	for i := 0; i < len(b); i += width {
		if i > 0 {
			sb.WriteByte('\n')
		}
		row := b[i:min(i+width, len(b))]
		hexs := make([]string, len(row))
		ascii := make([]byte, len(row))
		for j, c := range row {
			hexs[j] = fmt.Sprintf("%02x", c)
			if c >= 32 && c < 127 {
				ascii[j] = c
			} else {
				ascii[j] = '.'
			}
		}
		fmt.Fprintf(&sb, "    %04x  %-*s %s", i, width*3, strings.Join(hexs, " "), ascii)
	}
	return sb.String()
}

// Peer formats a remote address the way the Python stub did: ('127.0.0.1', 51641).
func Peer(a net.Addr) string {
	switch t := a.(type) {
	case *net.TCPAddr:
		return fmt.Sprintf("('%s', %d)", t.IP.String(), t.Port)
	case *net.UDPAddr:
		return fmt.Sprintf("('%s', %d)", t.IP.String(), t.Port)
	case interface {
		IPPort() (net.IP, int)
		TunnelSession() uint64
	}: // a game socket behind the tunnel (tunnel.Addr)
		ip, port := t.IPPort()
		return fmt.Sprintf("('%s', %d, tunnel %d)", ip.String(), port, t.TunnelSession())
	}
	return fmt.Sprintf("('%s')", a.String())
}

// ErrName gives an error a short class name for log lines, in the style of the Python stub
// ("TimeoutError: ...", "ConnectionError: ...").
func ErrName(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "TimeoutError"
	}
	return "ConnectionError"
}
