package web

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// chunked is a chunked body of n chunks of size bytes each (then the last chunk).
func chunked(n, size int) string {
	var b strings.Builder
	chunk := strings.Repeat("x", size)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "%x\r\n%s\r\n", size, chunk)
	}
	b.WriteString("0\r\n\r\n")
	return b.String()
}

// A body's claimed length is checked against the path's limit before anything is read or reserved: over it is
// 413 and the connection closed, a malformed length 400; nothing panics.
func TestBodyLimits(t *testing.T) {
	const tooLarge = "HTTP/1.1 413 Content Too Large\r\n"
	const bad = "HTTP/1.1 400 Bad Request\r\n"
	for _, tc := range []struct {
		name, req, want, log string
	}{
		{"huge content-length", "POST /t HTTP/1.1\r\nContent-Length: 99999999999\r\n\r\n", tooLarge,
			"[http] PEER POST /t HTTP/1.1: TooLarge: body over the limit of 1048576 bytes (Content-Length 99999999999)\n"},
		{"content-length past int64", "POST /t HTTP/1.1\r\nContent-Length: 99999999999999999999999\r\n\r\n", tooLarge, "TooLarge"},
		{"content-length one over", fmt.Sprintf("POST /t HTTP/1.1\r\nContent-Length: %d\r\n\r\n", maxBody+1), tooLarge, "TooLarge"},
		{"negative content-length", "POST /t HTTP/1.1\r\nContent-Length: -1\r\n\r\n", bad, `ValueError: invalid content-length "-1"`},
		{"chunk 7fffffffffffffff", "POST /t HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n7fffffffffffffff\r\nabc", tooLarge,
			"TooLarge: body over the limit of 1048576 bytes (a chunk of 9223372036854775807 bytes after 0)"},
		{"chunk ffffffffffffffff", "POST /t HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\nffffffffffffffff\r\nabc", tooLarge,
			"(a chunk of 18446744073709551615 bytes after 0)"},
		{"chunk past uint64", "POST /t HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n1ffffffffffffffff;x=y\r\n", tooLarge,
			"(a chunk of 0x1ffffffffffffffff bytes)"},
		{"chunk after others", "POST /t HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nab\r\nfffff\r\n", tooLarge,
			"(a chunk of 1048575 bytes after 2)"},
		{"bad chunk size", "POST /t HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n", bad, `ValueError: invalid chunk size "zz\r\n"`},
		{"long chunk line", "POST /t HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n1;" + strings.Repeat("e", 5000) + "\r\nx\r\n0\r\n\r\n", bad,
			"ValueError: chunk size line longer than 4096 bytes"},
		{"many small chunks over", "POST /t HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n" + chunked(17, 64<<10), tooLarge,
			"(a chunk of 65536 bytes after 1048576)"},
		{"another path", "POST /qos/qos HTTP/1.1\r\nContent-Length: 2000000\r\n\r\n", tooLarge, "TooLarge"},
	} {
		resp, log := roundTrip(t, &HTTP{}, tc.req)
		if !strings.HasPrefix(resp, tc.want) || !strings.Contains(resp, "\r\nConnection: close\r\n\r\n") {
			t.Errorf("%s: response %q", tc.name, resp)
		}
		if !strings.Contains(log, tc.log) || !strings.Contains(log, "[http] -> "+strings.TrimSuffix(tc.want[9:], "\r\n")+"\n") {
			t.Errorf("%s: log lacks %q:\n%s", tc.name, tc.log, log)
		}
	}
}

// A body exactly at the limit is read (in pieces; only its first logging.MaxDump bytes are kept) and answered as
// before; the crash reports' path has the larger limit.
func TestBodyAtTheLimit(t *testing.T) {
	body := strings.Repeat("a", maxBody)
	for _, req := range []string{
		fmt.Sprintf("POST /t HTTP/1.1\r\nContent-Length: %d\r\n\r\n%s", maxBody, body),
		"POST /t HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n" + chunked(16, 64<<10),
	} {
		resp, log := roundTrip(t, &HTTP{}, req)
		if !strings.HasPrefix(resp, "HTTP/1.1 404 Not Found\r\n") {
			t.Errorf("response %q", resp[:min(len(resp), 200)])
		}
		if !strings.Contains(log, fmt.Sprintf("    body %d bytes (first %d shown):\n    0000  ", maxBody, logging.MaxDump)) {
			t.Errorf("log:\n%s", log[:min(len(log), 600)])
		}
	}
	resp, log := roundTrip(t, &HTTP{}, fmt.Sprintf("POST /crash/reportCrash/ HTTP/1.1\r\nContent-Length: %d\r\n\r\n%sbb", maxBody+2, body))
	if !strings.HasPrefix(resp, "HTTP/1.1 404 Not Found\r\n") || !strings.Contains(log, fmt.Sprintf("body %d bytes", maxBody+2)) {
		t.Errorf("crash report over maxBody: response %q", resp[:min(len(resp), 200)])
	}
	for target, want := range map[string]int64{
		"/crash/reportCrash/": maxCrashBody, "http://127.0.0.1:18080/crash/reportCrash/?x=1": maxCrashBody,
		"/crash": maxBody, "/t/crash/": maxBody, "/": maxBody,
	} {
		if got := bodyLimit(target); got != want {
			t.Errorf("bodyLimit(%q) = %d, want %d", target, got, want)
		}
	}
}

// A panic while a request is served ends that connection only: it is logged with its stack, and the server
// answers the next request.
func TestHTTPPanicKeepsServing(t *testing.T) {
	var logBuf syncBuffer
	h := &HTTP{Log: logging.New(&logBuf), Home: func() []byte { panic("home broke") }}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go h.Serve(ln)
	ask := func(req string) string {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.Write([]byte(req))
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp, _ := io.ReadAll(c)
		return string(resp)
	}
	if resp := ask("GET /x" + HomeJSONSuffix + " HTTP/1.1\r\n\r\n"); resp != "" {
		t.Errorf("panicking request answered %q", resp)
	}
	if resp := ask("GET /qos HTTP/1.1\r\n\r\n"); !strings.HasPrefix(resp, "HTTP/1.1 404 Not Found\r\n") {
		t.Errorf("next request: %q", resp)
	}
	log := logBuf.String()
	for _, want := range []string{" panic: home broke (that connection is closed; the server goes on)\n", "goroutine ", "serveHome"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// syncBuffer is a bytes.Buffer safe for the server's goroutines and the test.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
