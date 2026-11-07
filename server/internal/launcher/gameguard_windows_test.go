package launcher

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func guardOn(t *testing.T) {
	t.Helper()
	checkGameOwner = true
	t.Cleanup(func() { checkGameOwner = false })
}

// The test binary isn't under the copy's folder, so it stands for any other program on this PC. On a port with no use
// outside the game it gets nothing and no stream reaches the server; on the HTTP port it gets only the pages. With the
// copy's folder around the test binary, the same connections pass.
func TestGameGuard(t *testing.T) {
	guardOn(t)
	host := frontLocal + ":" + strconv.Itoa(DefaultFrontPorts.HTTP)
	f, logA := guardFront(t, func(f *Front) *gameGuard { return newGameGuard(t.TempDir(), f.logf) })

	for _, port := range []int{DefaultFrontPorts.UI, DefaultFrontPorts.Blaze, DefaultFrontPorts.QoS, DefaultFrontPorts.Telemetry, DefaultFrontPorts.Redirector} {
		c := fakeGame{t: t}.dial(port)
		c.SetDeadline(time.Now().Add(5 * time.Second))
		c.Write([]byte("{\"call\":\"hello\"}\n"))
		// closed with what it sent unread, which Windows reports as a reset; a timeout would mean it stayed open
		if b, err := io.ReadAll(c); len(b) != 0 || errors.Is(err, os.ErrDeadlineExceeded) {
			t.Errorf("port %d: %q, %v, want the connection closed with no byte", port, b, err)
		}
		c.Close()
	}
	r, body := frontHTTP(t, "GET /battlegrounds/flood HTTP/1.1\r\nHost: "+host)
	if r.StatusCode != http.StatusForbidden || !strings.Contains(body, "serves the game") {
		t.Errorf("another program's request for the game's content: %d %s", r.StatusCode, body)
	}
	r, _ = frontHTTP(t, "POST /crash/x HTTP/1.1\r\nHost: "+host+"\r\nContent-Length: 1\r\n\r\nx")
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("another program's crash report: %d", r.StatusCode)
	}
	if text := readLogs(t, logA); strings.Contains(text, "/battlegrounds/flood") || strings.Contains(text, "[uichannel]") && strings.Contains(text, "connected") {
		t.Errorf("a refused connection reached the server:\n%s", text)
	}
	// the player's browser still gets the pages, from a link in the game (no site) and from each other (the same site)
	for _, h := range []string{"", "\r\nSec-Fetch-Site: none", "\r\nSec-Fetch-Site: same-origin"} {
		if r, body := frontHTTP(t, "GET /dawngate/signup HTTP/1.1\r\nHost: "+host+h); r.StatusCode != http.StatusOK || !strings.Contains(body, "A") {
			t.Errorf("the chooser with %q: %d", h, r.StatusCode)
		}
	}
	// another site's page gets none of them, and a page under another name neither (DNS rebinding)
	for _, h := range []string{"Host: " + host + "\r\nSec-Fetch-Site: cross-site", "Host: " + host + "\r\nSec-Fetch-Site: same-site", "Host: evil.example:18080"} {
		if r, _ := frontHTTP(t, "GET /dawngate/signup HTTP/1.1\r\n"+h); r.StatusCode != http.StatusForbidden {
			t.Errorf("a page with %q: %d", h, r.StatusCode)
		}
		if r, _ := frontHTTP(t, "GET /dawngate/pick/0/signup HTTP/1.1\r\n"+h); r.StatusCode != http.StatusForbidden {
			t.Errorf("a server's page with %q: %d", h, r.StatusCode)
		}
	}
	_ = f

	// the same program, as the copy's: the game's content reaches the server again
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f.Guard = newGameGuard(filepath.Dir(exe), f.logf)
	frontHTTP(t, "GET /x/common/home.json?fromgame=1 HTTP/1.1\r\nHost: "+host)
	waitLog(t, logA, "fromgame", 0)
}

// A person's click on a server page's Download link passes (the same site, a GET); a page of another site, or a walk away
// from the path, doesn't.
func TestGameGuardDownloadLink(t *testing.T) {
	guardOn(t)
	host := frontLocal + ":" + strconv.Itoa(DefaultFrontPorts.HTTP)
	guardFront(t, func(f *Front) *gameGuard { return newGameGuard(t.TempDir(), f.logf) })
	same := "\r\nHost: " + host + "\r\nSec-Fetch-Site: same-origin"
	if r, _ := frontHTTP(t, "GET /dawngate/download HTTP/1.1"+same); r.StatusCode == http.StatusForbidden {
		t.Errorf("the download's page from a server's page: %d", r.StatusCode)
	}
	for _, line := range []string{
		"GET /dawngate/download HTTP/1.1\r\nHost: " + host,
		"GET /dawngate/download HTTP/1.1\r\nHost: " + host + "\r\nSec-Fetch-Site: cross-site",
		"GET /dawngate/download HTTP/1.1\r\nHost: evil.example\r\nSec-Fetch-Site: same-origin",
		"GET /dawngate/download/../../x HTTP/1.1" + same,
		"GET /dawngate/downloads HTTP/1.1" + same,
		"POST /dawngate/download HTTP/1.1" + same + "\r\nContent-Length: 0",
	} {
		if r, _ := frontHTTP(t, line); r.StatusCode != http.StatusForbidden {
			t.Errorf("%q: %d", line, r.StatusCode)
		}
	}
}

// The system's table holds the far end of a connection, and the program's file is this one.
func TestPeerProcess(t *testing.T) {
	ln, err := net.Listen("tcp4", frontLocal+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pid, image, err := peerProcess(s)
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	if int(pid) != os.Getpid() || !sameFile(image, exe) {
		t.Errorf("pid %d %s, want %d %s", pid, image, os.Getpid(), exe)
	}
	if !underFolder(image, filepath.Dir(exe)) {
		t.Error("the program isn't under its own folder")
	}
	if underFolder(image, t.TempDir()) {
		t.Error("the program is under a folder it isn't in")
	}
}

func sameFile(a, b string) bool {
	x, err1 := os.Stat(a)
	y, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(x, y)
}
