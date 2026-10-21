package launcher

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/version"
)

// The version tests' addresses: a server (dgserver with the tunnel), the player's tunnel address, a gateway of a
// release before the version check, an HTTP server without the version page. No lane uses them.
const (
	verServer = "127.0.9.80"
	verLocal  = "127.0.9.81"
	verOld    = "127.0.9.82"
	verPlain  = "127.0.9.83"
)

// oldGateway is a gateway of a release before the info hello: it reads the hello's head and closes the connection at
// the unknown kind. It returns its address and key.
func oldGateway(t *testing.T, ip string) (string, tunnel.Pin) {
	t.Helper()
	id, _, err := tunnel.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp4", ip+":0", tunnel.ServerTLS(id))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				head := make([]byte, 6)
				io.ReadFull(c, head)
			}()
		}
	}()
	return ln.Addr().String(), id.Pin
}

// TestFrontVersion: the list shows each server's version and its state against Start-Client's own; a login to a
// server of another version and to one that names none (an older release) is refused with FrontErrVersion before
// anything reaches the server, and goes through with the same version.
func TestFrontVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.80-82's ports")
	}
	port, logS := serveTunnelLogged(t, verServer)
	oldAddr, oldPin := oldGateway(t, verOld)
	_, oldPortS, _ := net.SplitHostPort(oldAddr)
	oldPort, _ := strconv.Atoi(oldPortS)
	l := setup.Layout{Root: t.TempDir()}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []Bookmark{{Tag: "s", Address: verServer, GatewayPort: port}, {Tag: "o", Address: verOld, GatewayPort: oldPort}} {
		if _, err := bm.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	sPin, err := tunnel.ProbePin(net.JoinHostPort(verServer, strconv.Itoa(port)), 3*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	for addr, pin := range map[string]tunnel.Pin{verServer: sPin, verOld: oldPin} { // keys trusted: only the version counts
		if _, err := TrustGateway(addr, pin, TrustOptions{Store: TrustedGateway(l, addr), Accept: true}); err != nil {
			t.Fatal(err)
		}
	}
	_, tlsCfg, err := tunnelCertSet(l, verLocal, false)
	if err != nil {
		t.Fatal(err)
	}
	own := version.String()
	for _, c := range []struct {
		version string // the front's own
		state   string // the server s's state in the list
		code    uint16 // the login to s (0: through)
	}{
		{"OpenDawngateServer-20261016-another", versionOther, FrontErrVersion},
		{"", versionSame, 0}, // this build's own version: the server's
	} {
		logDir := t.TempDir()
		lf, err := os.Create(filepath.Join(logDir, "front.log"))
		if err != nil {
			t.Fatal(err)
		}
		f := &Front{Log: logging.New(lf), Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(verLocal), RedirectorTLS: tlsCfg,
			ConnectTimeout: 3 * time.Second, ListRefresh: -1, Version: c.version}
		if err := f.Start(); err != nil {
			t.Fatal(err)
		}
		list := string(f.ServerList())
		for _, want := range []string{"dawngate-servers\t3\nversion\t" + f.Version + "\n",
			"\tok\t", "\t\t" + own + "\t" + c.state + "\topen\t", // s: its version, the state and its sign-up mode
			"\nserver\to\t" + verOld + "\t" + verOld + "\t" + oldPortS + "\tok\t", "\t\t\t" + versionUnknown + "\t\t"} {
			if !strings.Contains(list, want) {
				t.Errorf("front %q: the list lacks %q:\n%s", f.Version, want, list)
			}
		}
		g := fakeGame{t: t, ip: verLocal}
		g.redirect()
		conn := g.dial(DefaultFrontPorts.Blaze)
		g.start(conn)
		// the older release first: refused, nothing reaches it
		if rep := g.call(conn, 1, 0x28, 2, loginBody("vera#o")); rep.Type != fire.ErrorReply || rep.Error != FrontErrVersion {
			t.Errorf("front %q, login to an older release: %v %#x", f.Version, rep.Type, rep.Error)
		}
		rep := g.call(conn, 1, 0x28, 3, loginBody("vera#s"))
		switch {
		case c.code != 0 && (rep.Type != fire.ErrorReply || rep.Error != c.code):
			t.Errorf("front %q, login to s: %v %#x, want error %#x", f.Version, rep.Type, rep.Error, c.code)
		case c.code == 0 && rep.Type != fire.Reply:
			t.Errorf("front %q, login to s: %v %#x, want the server's reply\n%s", f.Version, rep.Type, rep.Error, readLogs(t, logDir))
		}
		conn.Close()
		f.Close()
		lf.Close()
		text := readLogs(t, logDir)
		if !strings.Contains(text, "doesn't tell its version") || !strings.Contains(text, "("+verOld) {
			t.Errorf("front %q: the older release's refusal isn't logged:\n%s", f.Version, text)
		}
		if c.code != 0 && !strings.Contains(text, "runs version "+own+", this Start-Client is "+c.version) {
			t.Errorf("front %q: the refusal doesn't name both versions:\n%s", f.Version, text)
		}
		if c.code != 0 && strings.Count(readLogs(t, logS), "MAIL:") != 0 {
			t.Errorf("front %q: the refused login reached the server", f.Version)
		}
	}
	if n := strings.Count(readLogs(t, logS), "MAIL: 'vera'"); n != 1 {
		t.Errorf("the server got %d logins, want 1 (the same version's)", n)
	}
}

// TestFetchVersion: a server without the tunnel answers its version on the HTTP port; one of an older release
// answers 404, and the check refuses both another version and none.
func TestFetchVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.80 and .83's ports")
	}
	serveTunnelLogged(t, verServer) // its plain HTTP port too (dgserver's default --plain on)
	if v, err := FetchVersion(verServer, HTTPPort, 3*time.Second); err != nil || v != version.String() {
		t.Errorf("FetchVersion: %q, %v (want %q)", v, err, version.String())
	}
	ln, err := net.Listen("tcp4", verPlain+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go http.Serve(ln, http.NotFoundHandler())
	p := ln.Addr().(*net.TCPAddr).Port
	v, verr := FetchVersion(verPlain, p, 3*time.Second)
	if verr == nil || v != "" || !strings.Contains(verr.Error(), "404") {
		t.Errorf("FetchVersion of an older server: %q, %v", v, verr)
	}
	if err := versionMismatch(verPlain, v, verr); err == nil || !strings.Contains(err.Error(), "doesn't tell its version") {
		t.Errorf("versionMismatch of none: %v", err)
	}
	if err := versionMismatch(verServer, "Release-X", nil); err == nil ||
		!strings.Contains(err.Error(), "runs version Release-X, this Start-Client is version "+version.String()) {
		t.Errorf("versionMismatch of another: %v", err)
	}
	if err := versionMismatch(verServer, version.String(), nil); err != nil {
		t.Errorf("versionMismatch of the same: %v", err)
	}
}
