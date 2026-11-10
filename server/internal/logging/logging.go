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
	"regexp"
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

	dir     string // Open's folder
	lim     Limits
	written int64     // bytes in the current file
	tokens  float64   // the rate's bucket
	last    time.Time // when it was filled last
	dropped int64     // lines dropped since the last one written
}

// Limits bound what a log can cost, so that nobody can fill a server's disk through it, however a line comes about (a zero
// field: no limit). Every frame and packet is logged, and a client decides how many and how long, and --log-days prunes only
// by age. A rotated file is named as Open names one, so the pruning finds it.
type Limits struct {
	MaxMessage int     // bytes of one message; the rest is cut
	BytesPerS  float64 // sustained bytes a second, beyond it lines are dropped and counted
	Burst      float64 // the rate's burst, in bytes
	FileBytes  int64   // a file over this starts a new one
	TotalBytes int64   // the oldest files go when the folder's log files hold more
}

// PublicLimits are a public server's (dgserver turns them on with the other limits). A client's session logs about 1 MB, so
// the rate is far above a server's real traffic; a flood at the rate reaches TotalBytes and only then costs old logs.
func PublicLimits() Limits {
	return Limits{MaxMessage: 64 << 10, BytesPerS: 4 << 20, Burst: 64 << 20, FileBytes: 256 << 20, TotalBytes: 8 << 30}
}

// SetLimits sets the limits from now on.
func (l *Log) SetLimits(lim Limits) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lim, l.tokens, l.last = lim, lim.Burst, l.now()
}

// Limits returns the limits set last (none: the zero Limits).
func (l *Log) Limits() Limits {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lim
}

// allowLocked takes n bytes from the rate's bucket.
func (l *Log) allowLocked(n int, now time.Time) bool {
	if l.lim.BytesPerS <= 0 {
		return true
	}
	l.tokens = min(l.lim.Burst, l.tokens+now.Sub(l.last).Seconds()*l.lim.BytesPerS)
	l.last = now
	if l.tokens < float64(n) {
		return false
	}
	l.tokens -= float64(n)
	return true
}

// rotateLocked starts a new file and removes the oldest ones over TotalBytes.
func (l *Log) rotateLocked() {
	f, err := os.OpenFile(filepath.Join(l.dir, l.now().Format("20060102-150405")+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return // the file goes on growing until a new one can be made
	}
	l.f.Close()
	l.f, l.Path, l.written = f, f.Name(), 0
	if l.lim.TotalBytes <= 0 {
		return
	}
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	var total int64
	for i := len(entries) - 1; i >= 0; i-- { // ReadDir sorts by name, and the names are times: newest first from the end
		e := entries[i]
		if e.IsDir() || !logFileName.MatchString(e.Name()) {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		if total += st.Size(); total > l.lim.TotalBytes && filepath.Join(l.dir, e.Name()) != l.Path {
			os.Remove(filepath.Join(l.dir, e.Name()))
		}
	}
}

// logFileName is a log file's name (Open, rotateLocked).
var logFileName = regexp.MustCompile(`^\d{8}-\d{6}\.log$`)

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
	return &Log{Path: path, f: f, out: os.Stdout, now: time.Now, dir: dir}, nil
}

// New returns a Log that writes only to w (tests).
func New(w io.Writer) *Log { return &Log{out: w, now: time.Now} }

// newline matches the Python stub, whose text-mode log file had CRLF line ends on Windows.
var newline = map[bool]string{true: "\r\n", false: "\n"}[runtime.GOOS == "windows"]

// Log writes one message; msg may span several lines.
func (l *Log) Log(who, msg string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if m := l.lim.MaxMessage; m > 0 && len(msg) > m {
		msg = msg[:m] + fmt.Sprintf(" ... (%d bytes cut)", len(msg)-m)
	}
	line := now.Format("15:04:05.000000") + " [" + who + "] " + msg
	if !l.allowLocked(len(line), now) {
		l.dropped++
		return
	}
	if l.dropped > 0 {
		note := now.Format("15:04:05.000000") + " [log] " + fmt.Sprintf("%d lines dropped: more than this server logs a second", l.dropped)
		l.dropped = 0
		l.writeLocked(note)
	}
	l.writeLocked(line)
	if l.lim.FileBytes > 0 && l.written >= l.lim.FileBytes && l.f != nil && l.dir != "" {
		l.rotateLocked()
	}
}

func (l *Log) writeLocked(line string) {
	if l.out != nil {
		fmt.Fprintln(l.out, line)
	}
	if l.f != nil {
		n, _ := io.WriteString(l.f, strings.ReplaceAll(line, "\n", newline)+newline)
		l.written += int64(n)
	}
}

// SetConsole changes where the lines go besides the file (Open: stdout; nil: the file only).
func (l *Log) SetConsole(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out = w
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
