package launcher

import (
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/tunnel"
)

// verSwitched is the release test's gateway address (no lane uses it).
const verSwitched = "127.0.9.84"

// refusingGateway answers the info hello with version v and every session hello with status st (a gateway that
// refuses this Start-Client's release: a server restarted with another release between the probe and the session,
// or a gateway of a release before the release check, StatusVersion). It records the release names the session hellos
// carried.
func refusingGateway(t *testing.T, ip, v string, st byte) (addr string, pin tunnel.Pin, names func() []string) {
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
	var mu sync.Mutex
	var got []string
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
				if _, err := io.ReadFull(c, head); err != nil {
					return
				}
				switch head[5] {
				case 3: // info: status, the version, no facts
					c.Write(append(append([]byte{0, byte(len(v))}, v...), 0))
				case 1: // session: 28 bytes, then (version 2) the release
					b := make([]byte, 29)
					if head[4] != 2 || func() bool { _, err := io.ReadFull(c, b); return err != nil }() {
						return
					}
					name := make([]byte, b[28])
					io.ReadFull(c, name)
					mu.Lock()
					got = append(got, string(name))
					mu.Unlock()
					c.Write([]byte{st})
				}
			}()
		}
	}()
	return ln.Addr().String(), id.Pin, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// TestFrontRelease: the session hello names this Start-Client's release; a gateway that refuses it (StatusRelease,
// or StatusVersion from a gateway that takes no version 2 hello) makes the login's error FrontErrVersion (0x00EB),
// not FrontErrUnreachable, logged with the release and the server's version. This PC names the server's own version.
func TestFrontRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.81 and 127.0.9.84's ports")
	}
	const rel = "v9.8.7"
	_, tlsCfg, err := tunnelCertSet(setup.Layout{Root: t.TempDir()}, verLocal, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []byte{tunnel.StatusRelease, tunnel.StatusVersion} {
		addr, pin, names := refusingGateway(t, verSwitched, rel, st)
		_, portS, _ := net.SplitHostPort(addr)
		port, _ := strconv.Atoi(portS)
		l := setup.Layout{Root: t.TempDir()}
		bm, err := LoadBookmarks(BookmarksPath(l))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bm.Add(Bookmark{Tag: "r", Address: verSwitched, GatewayPort: port}); err != nil {
			t.Fatal(err)
		}
		if _, err := TrustGateway(verSwitched, pin, TrustOptions{Store: TrustedGateway(l, verSwitched), Accept: true}); err != nil {
			t.Fatal(err)
		}
		logDir := t.TempDir()
		lf, err := os.Create(filepath.Join(logDir, "front.log"))
		if err != nil {
			t.Fatal(err)
		}
		f := &Front{Log: logging.New(lf), Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(verLocal), RedirectorTLS: tlsCfg,
			ConnectTimeout: 3 * time.Second, ListRefresh: -1, Version: rel}
		if err := f.Start(); err != nil {
			t.Fatal(err)
		}
		g := fakeGame{t: t, ip: verLocal}
		g.redirect()
		conn := g.dial(DefaultFrontPorts.Blaze)
		g.start(conn)
		rep := g.call(conn, 1, 0x28, 2, loginBody("vera#r"))
		conn.Close()
		f.Close()
		lf.Close()
		text := readLogs(t, logDir)
		if rep.Type != fire.ErrorReply || rep.Error != FrontErrVersion {
			t.Errorf("status %d: the login's answer %v %#x, want error %#x\n%s", st, rep.Type, rep.Error, FrontErrVersion, text)
		}
		if want := "r (" + addr + ") refused this Start-Client's release " + rel + ": the tunnel gateway at " + addr +
			" refused the session: " + tunnel.StatusText(st); !strings.Contains(text, want) {
			t.Errorf("status %d: the log lacks %q:\n%s", st, want, text)
		}
		if n := names(); len(n) != 1 || n[0] != rel {
			t.Errorf("status %d: the session hellos named %q, want [%s]", st, n, rel)
		}
	}

	// the release a session names: this Start-Client's, and for This PC the version its server told
	f := &Front{Version: "B", ThisPC: &thisPC{}}
	pc, other := Bookmark{Tag: ThisPCTag}, Bookmark{Tag: "s"}
	if got := f.releaseFor(pc, "A"); got != "A" {
		t.Errorf("This PC of version A: the hello names %q", got)
	}
	if got := f.releaseFor(pc, ""); got != "B" {
		t.Errorf("This PC of no version: the hello names %q", got)
	}
	if got := f.releaseFor(other, "A"); got != "B" {
		t.Errorf("a server of version A: the hello names %q", got)
	}
	if got := (&Front{Version: "B"}).releaseFor(pc, "A"); got != "B" {
		t.Errorf("a pc tag without This PC: the hello names %q", got)
	}
}
