package tunnel

import (
	"bytes"
	"crypto/sha256"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// TestSessionHelloMaps pins the version 3 session hello: version 2's bytes, then the map count and per map its
// name's length, the name and the SHA-256; the reader cleans the names, and a hello naming too many maps is refused.
func TestSessionHelloMaps(t *testing.T) {
	var sum [32]byte
	for i := range sum {
		sum[i] = byte(i)
	}
	h := sessionHello{tunnelIP: [4]byte{127, 0, 0, 1}, release: "v1", maps: []InstalledMap{{Archive: "zz_A.snap", SHA256: sum}}}
	want := "DGTN\x03\x01\x7f\x00\x00\x01" + strings.Repeat("\x00", 8+16) + "\x02v1" + "\x01" + "\x09zz_A.snap" + string(sum[:])
	if got := h.encode(); string(got) != want {
		t.Errorf("encode = %q\nwant     %q", got, want)
	}
	r := bytes.NewReader(h.encode())
	ver, kind, err := readHello(r)
	if err != nil || ver != sessionVersion || kind != kindSession {
		t.Fatalf("readHello = %d, %d, %v", ver, kind, err)
	}
	back, err := readSessionHello(r, ver)
	h.ver = sessionVersion
	if err != nil || !reflect.DeepEqual(back, h) || r.Len() != 0 {
		t.Errorf("readSessionHello = %+v, %v; want %+v", back, err, h)
	}
	// an empty list is named (not nil); an odd name is cleaned
	r = bytes.NewReader(sessionHello{maps: []InstalledMap{{Archive: "zz_a b.snap"}}}.encode())
	ver, _, _ = readHello(r)
	if back, _ := readSessionHello(r, ver); len(back.maps) != 1 || back.maps[0].Archive != "zz_a-b.snap" {
		t.Errorf("an odd name: %+v", back.maps)
	}
	r = bytes.NewReader(sessionHello{}.encode())
	ver, _, _ = readHello(r)
	if back, _ := readSessionHello(r, ver); back.maps == nil || len(back.maps) != 0 {
		t.Errorf("no maps: %#v", back.maps)
	}
	// a version 2 hello names none (nil)
	r = bytes.NewReader(sessionHello{ver: releaseVersion, release: "v1"}.encode())
	ver, _, _ = readHello(r)
	if back, _ := readSessionHello(r, ver); back.maps != nil || back.release != "v1" {
		t.Errorf("version 2: %+v", back)
	}
	many := make([]InstalledMap, MaxHelloMaps+5)
	if b := (sessionHello{maps: many}).encode(); int(b[6+28+1]) != MaxHelloMaps {
		t.Errorf("too many maps written: count %d", b[6+28+1])
	}
	raw := sessionHello{}.encode()
	raw[len(raw)-1] = MaxHelloMaps + 1
	r = bytes.NewReader(raw)
	ver, _, _ = readHello(r)
	if _, err := readSessionHello(r, ver); err == nil {
		t.Error("a hello naming too many maps was read")
	}
}

// TestReadInstalledMaps: the archives the copy has, hashed, in the asked order; missing ones and paths left out.
func TestReadInstalledMaps(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "zz_B.snap"), []byte("bee"), 0o644)
	os.WriteFile(filepath.Join(dir, "zz_A.snap"), []byte("a"), 0o644)
	os.Mkdir(filepath.Join(dir, "zz_D.snap"), 0o755)
	got := ReadInstalledMaps(dir, "zz_B.snap", "zz_C.snap", "zz_A.snap", "zz_D.snap", "zz_b.snap", `..\zz_A.snap`, "")
	if len(got) != 2 || got[0].Archive != "zz_B.snap" || got[1].Archive != "zz_A.snap" ||
		got[0].SHA256 != sha256.Sum256([]byte("bee")) || got[1].SHA256 != sha256.Sum256([]byte("a")) {
		t.Errorf("ReadInstalledMaps = %+v", got)
	}
	if w := MapsWords(got); w != "zz_B.snap "+got[0].Short()+", zz_A.snap "+got[1].Short() || len(got[0].Short()) != 8 {
		t.Errorf("MapsWords = %q", w)
	}
	if MapsWords(nil) != "none" {
		t.Error("MapsWords(nil)")
	}
}

// TestTunnelMaps: the session hello's maps reach the services (Addr.Maps), a session taken up again takes the new
// list, a version 2 hello names none; a gateway of a release before the list (StatusVersion for version 3) gets a
// version 2 hello from the same client, which then has its session without maps.
func TestTunnelMaps(t *testing.T) {
	buf := &syncBuf{}
	g, _ := startGatewayWith(t, buf, func(g *Gateway) { g.Version = "rel-A" })
	ring := InstalledMap{Archive: "zz_CM_Ring.snap", SHA256: sha256.Sum256([]byte("ring"))}
	c := &Client{Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tunIP), DialIP: net.ParseIP(peerIP),
		External: true, Release: "rel-A", Maps: []InstalledMap{ring}}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	s := c.session(time.Second)
	if maps, named := gatewayAddr(g, s.id).Maps(); !named || !reflect.DeepEqual(maps, []InstalledMap{ring}) {
		t.Errorf("Maps() = %+v, %v", maps, named)
	}
	other := InstalledMap{Archive: "zz_CM_Ring.snap", SHA256: sha256.Sum256([]byte("other"))}
	lo := [4]byte{127, 0, 0, 1}
	if st := rawHello(t, g, sessionHello{tunnelIP: lo, resumeID: s.id, resumeToken: s.token, release: "rel-A",
		maps: []InstalledMap{other}}.encode()); st != StatusOK {
		t.Fatalf("taken up again: %s", StatusText(st))
	}
	if maps, _ := gatewayAddr(g, s.id).Maps(); len(maps) != 1 || maps[0] != other {
		t.Errorf("taken up again: Maps() = %+v", maps)
	}
	c.Close()
	// a client without maps names an empty list; one with nil Maps none (a version 2 hello)
	empty := &Client{Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tunIP), DialIP: net.ParseIP(peerIP),
		External: true, Release: "rel-A", Maps: ReadInstalledMaps(t.TempDir(), "zz_CM_Ring.snap")}
	if err := empty.Start(); err != nil {
		t.Fatal(err)
	}
	if maps, named := gatewayAddr(g, empty.session(time.Second).id).Maps(); !named || len(maps) != 0 {
		t.Errorf("no maps: Maps() = %+v, %v", maps, named)
	}
	empty.Close()
	none := &Client{Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tunIP), DialIP: net.ParseIP(peerIP),
		External: true, Release: "rel-A"}
	if err := none.Start(); err != nil {
		t.Fatal(err)
	}
	if _, named := gatewayAddr(g, none.session(time.Second).id).Maps(); named {
		t.Error("a client with nil Maps named a list")
	}
	none.Close()
	if !strings.Contains(buf.String(), "release rel-A, maps zz_CM_Ring.snap "+ring.Short()) ||
		!strings.Contains(buf.String(), "release rel-A, maps none") {
		t.Errorf("the gateway's log:\n%s", buf.String())
	}
	if _, named := (&Addr{}).Maps(); named {
		t.Error("an address without a session names maps")
	}
	if maps, named := FakeSessionMapsAddr(net.ParseIP(tunIP), 1, "x", true, []InstalledMap{ring}).Maps(); !named || len(maps) != 1 {
		t.Error("FakeSessionMapsAddr")
	}
	if _, named := FakeSessionAddr(net.ParseIP(tunIP), 1, "x", true).Maps(); named {
		t.Error("FakeSessionAddr names maps")
	}

	// a gateway of a release before the map list
	buf2 := &syncBuf{}
	old, _ := startGatewayWith(t, buf2, func(g *Gateway) { g.Version = "rel-A"; g.maxSessionHello = releaseVersion })
	cbuf := &syncBuf{}
	oc := &Client{Log: logging.New(cbuf), Gateway: old.Addr(), Pin: old.Identity.Pin, TunnelIP: net.ParseIP(tunIP),
		DialIP: net.ParseIP(peerIP), External: true, Release: "rel-A", Maps: []InstalledMap{ring}}
	if err := oc.Start(); err != nil {
		t.Fatalf("an older gateway: %v", err)
	}
	defer oc.Close()
	if name, same := gatewayAddr(old, oc.session(time.Second).id).Release(); name != "rel-A" || !same {
		t.Errorf("an older gateway: Release() = %q, %v", name, same)
	}
	if _, named := gatewayAddr(old, oc.session(time.Second).id).Maps(); named {
		t.Error("an older gateway's session names maps")
	}
	if !strings.Contains(cbuf.String(), "takes no map list (a release before it): session hello version 2") ||
		!strings.Contains(buf2.String(), "tunnel version 3 not supported") ||
		!strings.Contains(buf2.String(), "maps not named (a Start-Client from before the map check per version)") {
		t.Errorf("the logs:\nclient %s\ngateway %s", cbuf.String(), buf2.String())
	}
}
