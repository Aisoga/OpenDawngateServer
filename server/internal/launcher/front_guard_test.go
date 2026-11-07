package launcher

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/tunnel"
)

// guardFront is a front in a session with a real dgserver, limited by guard (nil: no limit).
func guardFront(t *testing.T, guard func(f *Front) *gameGuard, extra ...string) (*Front, string) {
	t.Helper()
	port, logA := serveTunnelLogged(t, frontServerA, extra...)
	l := setup.Layout{Root: t.TempDir()}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bm.Add(Bookmark{Name: "A", Address: frontServerA, GatewayPort: port}); err != nil {
		t.Fatal(err)
	}
	_, tlsCfg, err := tunnelCertSet(l, frontLocal, false)
	if err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	lf, err := os.Create(filepath.Join(logDir, "front.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lf.Close() })
	f := &Front{Log: logging.New(lf), Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(frontLocal), RedirectorTLS: tlsCfg, ConnectTimeout: 3 * time.Second}
	f.Guard = guard(f)
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	b, _ := bm.Find("a")
	f.slotOf("a")
	if _, err := f.session(b); err == nil {
		t.Fatal("no confirm once")
	}
	if _, err := f.session(b); err != nil {
		t.Fatal(err)
	}
	return f, logA
}

// frontHTTP sends one raw request to the front's HTTP port from this program and reads the answer.
func frontHTTP(t *testing.T, head string) (*http.Response, string) {
	t.Helper()
	c := fakeGame{t: t}.dial(DefaultFrontPorts.HTTP)
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(c, head+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	r, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("%q: %v", head, err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r, string(b)
}

// A hostile server's page answers with a script, a cookie and a header of its own, and the browser's request carries the
// cookies it holds for 127.0.0.1: none of those crosses the front.
func TestForwardStripsWhatAPageShouldNotHave(t *testing.T) {
	up, srv := net.Pipe()
	defer up.Close()
	got := make(chan *http.Request, 1)
	go func() {
		defer srv.Close()
		r, err := http.ReadRequest(bufio.NewReader(srv))
		if err != nil {
			return
		}
		got <- r
		fmt.Fprint(srv, "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nSet-Cookie: x=1\r\nClear-Site-Data: \"*\"\r\n"+
			"Access-Control-Allow-Origin: *\r\nContent-Security-Policy: default-src *\r\nContent-Length: 28\r\n\r\n<script>alert(1)</script>hi\n")
	}()
	browser, c := net.Pipe()
	defer browser.Close()
	defer c.Close()
	f := &Front{Log: logging.New(io.Discard)}
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader("GET /dawngate/pick/0/signup?x=1 HTTP/1.1\r\nHost: 127.0.0.1:18080\r\n" +
		"Cookie: session=secret\r\nAuthorization: Basic abc\r\nReferer: http://127.0.0.1:18080/dawngate/signup\r\nUser-Agent: b\r\n\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	go f.forwardPage(c, req, Bookmark{Tag: "evil"}, "signup", func() (net.Conn, func(), error) { return up, func() {}, nil })
	browser.SetDeadline(time.Now().Add(5 * time.Second))
	r, err := http.ReadResponse(bufio.NewReader(browser), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	out := <-got
	if out.URL.Path != "/dawngate/signup" || out.URL.RawQuery != "" {
		t.Errorf("the server was asked for %s", out.URL)
	}
	for _, h := range []string{"Cookie", "Authorization", "Referer"} {
		if v := out.Header.Get(h); v != "" {
			t.Errorf("the server got %s: %s", h, v)
		}
	}
	if out.UserAgent() != "b" {
		t.Errorf("the server got User-Agent %q", out.UserAgent())
	}
	for _, h := range []string{"Set-Cookie", "Clear-Site-Data", "Access-Control-Allow-Origin"} {
		if v := r.Header.Get(h); v != "" {
			t.Errorf("the browser got %s: %s", h, v)
		}
	}
	if p := r.Header.Values("Content-Security-Policy"); len(p) != 1 || p[0] != pagePolicy {
		t.Errorf("the browser got the policy %q", p)
	}
	if b, _ := io.ReadAll(r.Body); !strings.HasSuffix(string(b), "hi\n") {
		t.Errorf("the page's body: %q", b)
	}
}

// A session's UDP ports leave with it: a server the player tried doesn't keep its listeners on this PC, the next server's
// open, and the game's own QoS port stays.
func TestFrontClosesAServersUDPPorts(t *testing.T) {
	ip := net.ParseIP(frontLocal).To4()
	f := &Front{Log: logging.New(io.Discard), TunnelIP: ip, Ports: DefaultFrontPorts, done: make(chan struct{}),
		tcpLns: map[int]net.Listener{}, udpLns: map[int]*net.UDPConn{}, conns: map[net.Conn]struct{}{}}
	defer f.Close()
	if err := f.listenUDP(f.Ports.QoS); err != nil {
		t.Fatal(err)
	}
	session := func(port int) *frontSession {
		g := portsGateway(t, func(g *tunnel.Gateway) { g.ForwardUDP(port) })
		s := &frontSession{b: Bookmark{Tag: "x", Address: "127.0.9.60"}}
		s.c = &tunnel.Client{Log: logging.New(io.Discard), Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: ip, External: true, DialTimeout: 5 * time.Second}
		s.c.Deliver = f.deliverFor(s)
		if err := s.c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.c.Close)
		return s
	}
	f.ensureListeners(session(20777))
	f.ensureListeners(session(20888))
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.udpLns[20777] != nil {
		t.Error("the first server's UDP port stays open after the second session")
	}
	if f.udpLns[20888] == nil || f.udpLns[f.Ports.QoS] == nil {
		t.Errorf("the second server's port or the game's QoS port is gone: %v", f.udpLns)
	}
}
