package tunnel

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"
)

// v1SessionHello is a session hello as Start-Client wrote it before the release check: version 1, the 28 bytes, no
// release name.
func v1SessionHello(h sessionHello) []byte {
	b := h.encode()[:6+28]
	b[4] = 1
	return b
}

// gatewayAddr is an address of the gateway's session id, as a service's stream or datagram has it.
func gatewayAddr(g *Gateway, id uint64) *Addr {
	g.mu.Lock()
	defer g.mu.Unlock()
	return &Addr{Session: id, sess: g.sessions[id]}
}

// TestSessionHelloWire pins the session hello's bytes: version 2, the 28 bytes of version 1, the release's length and
// name; a name over version.MaxLen bytes is cut, and the gateway's reader cleans it.
func TestSessionHelloWire(t *testing.T) {
	h := sessionHello{tunnelIP: [4]byte{127, 0, 0, 1}, resumeID: 0x0102030405060708, release: "v1.2"}
	h.resumeToken[0], h.resumeToken[15] = 0xAA, 0xBB
	want := "DGTN\x02\x01\x7f\x00\x00\x01\x01\x02\x03\x04\x05\x06\x07\x08\xaa" + strings.Repeat("\x00", 14) + "\xbb\x04v1.2"
	if got := h.encode(); string(got) != want {
		t.Errorf("encode = %q\nwant     %q", got, want)
	}
	if got := v1SessionHello(h); len(got) != 34 || got[4] != 1 {
		t.Errorf("version 1 hello = %q", got)
	}
	r := bytes.NewReader(h.encode())
	ver, kind, err := readHello(r)
	if err != nil || ver != sessionVersion || kind != kindSession {
		t.Fatalf("readHello = %d, %d, %v", ver, kind, err)
	}
	back, err := readSessionHello(r, ver)
	if err != nil || back != h {
		t.Errorf("readSessionHello = %+v, %v; want %+v", back, err, h)
	}
	// version 1: the 28 bytes only, no release
	r = bytes.NewReader(v1SessionHello(h))
	ver, _, _ = readHello(r)
	back, err = readSessionHello(r, ver)
	if err != nil || ver != 1 || back.release != "" || back.resumeID != h.resumeID || r.Len() != 0 {
		t.Errorf("version 1: %d, %+v, %v (%d bytes left)", ver, back, err, r.Len())
	}
	long := sessionHello{release: strings.Repeat("x", 300)}.encode()
	if n := int(long[6+28]); n != 64 || len(long) != 6+28+1+64 {
		t.Errorf("a long name: length byte %d, %d bytes", n, len(long))
	}
	r = bytes.NewReader(sessionHello{release: "a b\x00c"}.encode())
	ver, _, _ = readHello(r)
	if back, _ := readSessionHello(r, ver); back.release != "a-b-c" {
		t.Errorf("an odd name read as %q", back.release)
	}
	if _, _, err := readHello(bytes.NewReader([]byte("DGTN\x03\x01"))); err != errVersion {
		t.Errorf("version 3: %v", err)
	}
}

// TestTunnelRelease: the gateway lets in only a Start-Client of its own release. A version 1 session hello (a
// Start-Client from before the check) gets StatusVersion, another release or an empty name StatusRelease, both logged
// with the names; a session taken up again is checked the same way; the same release gets its session. With
// AllowAnyClient every one is let in and the difference logged.
func TestTunnelRelease(t *testing.T) {
	buf := &syncBuf{}
	g, _ := startGatewayWith(t, buf, func(g *Gateway) { g.Version = "rel-A" })
	if d := g.Describe(); !strings.Contains(d, "sessions only from Start-Client release rel-A") {
		t.Errorf("Describe: %s", d)
	}
	lo := [4]byte{127, 0, 0, 1}
	if st := rawHello(t, g, v1SessionHello(sessionHello{tunnelIP: lo})); st != StatusVersion {
		t.Errorf("a version 1 session hello: %s", StatusText(st))
	}
	if st := rawHello(t, g, sessionHello{tunnelIP: lo, release: "rel-B"}.encode()); st != StatusRelease {
		t.Errorf("another release: %s", StatusText(st))
	}
	if st := rawHello(t, g, sessionHello{tunnelIP: lo}.encode()); st != StatusRelease {
		t.Errorf("an empty release name: %s", StatusText(st))
	}
	// the tunnel client: another release is refused with an error ReleaseRefused knows, the same one let in
	other := &Client{Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tunIP), DialIP: net.ParseIP(peerIP),
		External: true, Release: "rel-B"}
	err := other.Start()
	if err == nil {
		other.Close()
		t.Fatal("a Start-Client of another release got a session")
	}
	if !ReleaseRefused(err) || !strings.Contains(err.Error(), "refused the session: another release than the server's") {
		t.Errorf("the refusal: %v (ReleaseRefused %v)", err, ReleaseRefused(err))
	}
	same := &Client{Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tunIP), DialIP: net.ParseIP(peerIP),
		External: true, Release: "rel-A"}
	if err := same.Start(); err != nil {
		t.Fatalf("the same release: %v", err)
	}
	defer same.Close()
	s := same.session(time.Second)
	if name, ok := gatewayAddr(g, s.id).Release(); name != "rel-A" || !ok {
		t.Errorf("the same release's session: Release() = %q, %v", name, ok)
	}
	// a session taken up again names its release too
	if st := rawHello(t, g, sessionHello{tunnelIP: lo, resumeID: s.id, resumeToken: s.token, release: "rel-B"}.encode()); st != StatusRelease {
		t.Errorf("a session taken up again by another release: %s", StatusText(st))
	}
	if st := rawHello(t, g, v1SessionHello(sessionHello{tunnelIP: lo, resumeID: s.id, resumeToken: s.token})); st != StatusVersion {
		t.Errorf("a session taken up again with a version 1 hello: %s", StatusText(st))
	}
	if ReleaseRefused(&RefusedError{Status: StatusAddress}) || !ReleaseRefused(&RefusedError{Status: StatusVersion}) || ReleaseRefused(nil) {
		t.Error("ReleaseRefused takes the wrong statuses")
	}
	log := buf.String()
	for _, want := range []string{
		"session refused: its Start-Client names no release (session hello version 1: a Start-Client from before the release check); this server is release rel-A",
		"session refused: its Start-Client is release rel-B, this server is release rel-A: both need the same release",
		"session refused: its Start-Client is release empty, this server is release rel-A",
		"(new): tunnel address " + tunIP + ", release rel-A",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
	if g.Sessions() != 1 {
		t.Errorf("%d sessions, want the same release's one", g.Sessions())
	}

	// --allow-any-client: every release and a version 1 hello let in, the difference logged
	buf2 := &syncBuf{}
	open, _ := startGatewayWith(t, buf2, func(g *Gateway) { g.Version = "rel-A"; g.AllowAnyClient = true })
	if d := open.Describe(); !strings.Contains(d, "any release (--allow-any-client)") {
		t.Errorf("Describe: %s", d)
	}
	if st := rawHello(t, open, v1SessionHello(sessionHello{tunnelIP: lo})); st != StatusOK {
		t.Errorf("--allow-any-client, a version 1 hello: %s", StatusText(st))
	}
	other = &Client{Gateway: open.Addr(), Pin: open.Identity.Pin, TunnelIP: net.ParseIP(tunIP), DialIP: net.ParseIP(peerIP),
		External: true, Release: "rel-B"}
	if err := other.Start(); err != nil {
		t.Fatalf("--allow-any-client, another release: %v", err)
	}
	// let in, but not of the server's release: the services see that (the Blaze map check)
	if name, ok := gatewayAddr(open, other.session(time.Second).id).Release(); name != "rel-B" || ok {
		t.Errorf("another release let in: Release() = %q, %v", name, ok)
	}
	other.Close()
	if name, ok := (&Addr{}).Release(); name != "" || ok {
		t.Errorf("an address without a session: Release() = %q, %v", name, ok)
	}
	var none *Addr
	if _, ok := none.Release(); ok {
		t.Error("a nil address has the release")
	}
	log = buf2.String()
	for _, want := range []string{
		"its Start-Client's release is not named (a Start-Client from before the release check), this server's rel-A: let in (--allow-any-client)",
		"its Start-Client's release is rel-B, this server's rel-A: let in (--allow-any-client)",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
}
