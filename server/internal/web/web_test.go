package web

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/logging"
)

func TestTelemetryRulesMatchPythonStub(t *testing.T) {
	if !bytes.Equal(TelemetryRules(), golden.File(t, "telemetry_rules.xml")) {
		t.Error("TelemetryRules() differs from testdata/telemetry_rules.xml (the Python stub's rules)")
	}
}

// roundTrip sends raw request bytes to an HTTP server over loopback TCP, half-closes, and returns the
// response and the log with the client's address replaced by PEER.
func roundTrip(t *testing.T, h *HTTP, req string) (string, string) {
	t.Helper()
	var logBuf bytes.Buffer
	h.Log = logging.New(&logBuf)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		if c, err := ln.Accept(); err == nil {
			h.ServeConn(c)
		}
		close(done)
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte(req))
	c.(*net.TCPConn).CloseWrite()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, _ := io.ReadAll(c)
	<-done
	return string(resp), strings.ReplaceAll(logBuf.String(), logging.Peer(c.LocalAddr()), "PEER")
}

func TestBuiltinTelemetryRules(t *testing.T) {
	resp, log := roundTrip(t, &HTTP{}, "GET /battlegrounds_test/live/builds/manifests/telemetry_rules.xml HTTP/1.1\r\n"+
		"User-Agent: BG-Telemetry\r\nHost: 127.0.0.1:18080\r\n\r\n")
	rules := string(TelemetryRules())
	if want := "HTTP/1.1 200 OK\r\nContent-Type: text/xml\r\nContent-Length: 7119\r\nConnection: close\r\n\r\n" + rules; resp != want {
		t.Errorf("response:\n%q", resp)
	}
	for _, want := range []string{
		"[http] PEER GET /battlegrounds_test/live/builds/manifests/telemetry_rules.xml HTTP/1.1\n    user-agent: BG-Telemetry\n    host: 127.0.0.1:18080\n",
		"[http] -> 200 OK, 7119 bytes (built-in)\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestNotFoundAndHead(t *testing.T) {
	resp, log := roundTrip(t, &HTTP{}, "GET /qos/qos?vers=1 HTTP/1.1\r\n\r\n")
	if resp != "HTTP/1.1 404 Not Found\r\nContent-Type: text/plain\r\nContent-Length: 0\r\nConnection: close\r\n\r\n" {
		t.Errorf("404 response %q", resp)
	}
	if !strings.Contains(log, "[http] -> 404 Not Found\n") {
		t.Errorf("log:\n%s", log)
	}
	resp, _ = roundTrip(t, &HTTP{}, "HEAD /x/telemetry_rules.xml HTTP/1.1\r\n\r\n")
	if !strings.HasSuffix(resp, "Content-Length: 7119\r\nConnection: close\r\n\r\n") {
		t.Errorf("HEAD response %q", resp)
	}
}

func TestWWW(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "client", "v1"), 0o755)
	os.WriteFile(filepath.Join(root, "client", "v1", "lore page.html"), []byte("<p>hi</p>"), 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(root), "secret.txt"), []byte("no"), 0o644)
	h := &HTTP{WWW: root}

	resp, log := roundTrip(t, h, "GET http://127.0.0.1:18080/client/v1/lore%20page.html?x=1 HTTP/1.1\r\n\r\n")
	if !strings.HasPrefix(resp, "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: 9\r\n") || !strings.HasSuffix(resp, "<p>hi</p>") {
		t.Errorf("file response %q", resp)
	}
	if !strings.Contains(log, "-> 200 OK, 9 bytes from "+filepath.Join(root, "client", "v1", "lore page.html")) {
		t.Errorf("log:\n%s", log)
	}
	if resp, _ := roundTrip(t, h, "GET /../secret.txt HTTP/1.1\r\n\r\n"); !strings.HasPrefix(resp, "HTTP/1.1 404") {
		t.Errorf("path outside the web root served: %q", resp)
	}
	if resp, _ := roundTrip(t, h, "GET /client HTTP/1.1\r\n\r\n"); !strings.HasPrefix(resp, "HTTP/1.1 404") {
		t.Errorf("directory served: %q", resp)
	}
}

func TestBodiesAreLogged(t *testing.T) {
	_, log := roundTrip(t, &HTTP{}, "POST /crash HTTP/1.1\r\nContent-Length: 3\r\n\r\nabc")
	if !strings.Contains(log, "    content-length: 3\n    body 3 bytes:\n    0000  61 62 63 ") {
		t.Errorf("log:\n%s", log)
	}
	_, log = roundTrip(t, &HTTP{}, "POST /t HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nab\r\n1;x=y\r\nc\r\n0\r\n\r\n")
	if !strings.Contains(log, "body 3 bytes:\n    0000  61 62 63 ") {
		t.Errorf("chunked log:\n%s", log)
	}
	_, log = roundTrip(t, &HTTP{}, "GET / HTTP/1.1\r\nHost: x")
	if !strings.Contains(log, `[http] PEER ValueError: truncated request head: b'GET / HTTP/1.1\r\nHost: x'`+"\n") {
		t.Errorf("truncated log:\n%s", log)
	}
}

func TestURLHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"/a/b?c=d":             "/a/b",
		"http://h:1/x/y#z":     "/x/y",
		"http://h:1":           "",
		"/p?u=http://x/y":      "/p",
		"/telemetry_rules.xml": "/telemetry_rules.xml",
	} {
		if got := urlPath(in); got != want {
			t.Errorf("urlPath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := unquote("/a%20b%zz%4"); got != "/a b%zz%4" {
		t.Errorf("unquote = %q", got)
	}
}
