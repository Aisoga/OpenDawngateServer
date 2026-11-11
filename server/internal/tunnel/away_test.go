package tunnel

import (
	"bytes"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

func testAway() Away {
	a := Away{Gateway: netip.MustParseAddrPort("203.0.113.7:42128"), Ticket: []byte("tk")}
	for i := range a.Pin {
		a.Pin[i] = byte(i)
	}
	return a
}

// TestControlMessagesWire pins the away and home messages' bytes and reads them from a stream cut anywhere.
func TestControlMessagesWire(t *testing.T) {
	away, err := encodeAway(10042, testAway())
	const want = "01" + "273a" + "cb007107" + "a490" +
		"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" + "0002" + "746b"
	if err != nil || hex.EncodeToString(away) != want {
		t.Fatalf("encodeAway = %x, %v\nwant         %s", away, err, want)
	}
	if h := hex.EncodeToString(encodeHome(10042)); h != "02273a" {
		t.Errorf("encodeHome = %s", h)
	}
	for _, bad := range []Away{
		{Gateway: netip.MustParseAddrPort("[2001:db8::1]:42128"), Ticket: []byte("t")},
		{Gateway: netip.MustParseAddrPort("203.0.113.7:0"), Ticket: []byte("t")},
		{Gateway: netip.MustParseAddrPort("203.0.113.7:42128")},
		{Gateway: netip.MustParseAddrPort("203.0.113.7:42128"), Ticket: make([]byte, MaxTicketLen+1)},
	} {
		if _, err := encodeAway(10042, bad); err == nil {
			t.Errorf("encodeAway(%v) wrote it", bad)
		}
	}
	// a heartbeat, an away and a home, read through a connection that hands them out a byte at a time
	stream := append(append([]byte{0}, away...), encodeHome(10042)...)
	type got struct {
		home bool
		port int
		a    Away
	}
	var mu sync.Mutex
	var seen []got
	c := &Client{OnAway: func(p int, a Away) { mu.Lock(); seen = append(seen, got{port: p, a: a}); mu.Unlock() },
		OnHome: func(p int) { mu.Lock(); seen = append(seen, got{home: true, port: p}); mu.Unlock() }}
	cc := &controlConn{Conn: &byteConn{r: bytes.NewReader(stream)}, c: c}
	buf := make([]byte, 64)
	for {
		if _, err := cc.Read(buf); err != nil {
			break
		}
	}
	if want := []got{{port: 10042, a: testAway()}, {home: true, port: 10042}}; !reflect.DeepEqual(seen, want) {
		t.Errorf("read %+v, want %+v", seen, want)
	}
	for _, bad := range [][]byte{{3}, {1, 0, 1, 1, 2, 3, 4, 0, 1}} {
		bad = append(bad, make([]byte, 64)...)
		if _, _, _, _, err := parseControl(bad); !errors.Is(err, errControl) {
			t.Errorf("parseControl(%x) = %v", bad[:9], err)
		}
	}
}

// byteConn is a connection that reads one byte per Read.
type byteConn struct {
	net.Conn
	r *bytes.Reader
}

func (b *byteConn) Read(p []byte) (int, error) { return b.r.Read(p[:1]) }

// TestSessionHelloAway pins the version 4 session hello: no map list (0xFF) and a ticket, and maps with no ticket.
func TestSessionHelloAway(t *testing.T) {
	h := sessionHello{tunnelIP: [4]byte{127, 0, 0, 1}, release: "v1", ticket: []byte("tk")}
	want := "4447544e" + "0401" + "7f000001" + "0000000000000000" + "00000000000000000000000000000000" + "027631" + "ff" + "0002746b"
	if got := hex.EncodeToString(h.encode()); got != want {
		t.Errorf("encode = %s\nwant     %s", got, want)
	}
	for _, h := range []sessionHello{h, {release: "v1", maps: []InstalledMap{{Archive: "zz_A.snap"}}}, {release: "v1", maps: []InstalledMap{}}} {
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
	}
}

// TestAwayAndHome: Blaze's RouteAway reaches the client's OnAway on the control connection, its Route for the same port
// OnHome; the session's own route for the port is gone while it is away. A Start-Client whose gateway took only a
// version 3 hello reads no away message, so RouteAway says no.
func TestAwayAndHome(t *testing.T) {
	buf := &syncBuf{}
	g, echo := startGatewayWith(t, buf, func(g *Gateway) { g.Forward = nil })
	type msg struct {
		home bool
		port int
		a    Away
	}
	got := make(chan msg, 4)
	c := &Client{Log: logging.New(buf), Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tunIP), DialIP: net.ParseIP(peerIP),
		External: true, Maps: []InstalledMap{}, OnAway: func(p int, a Away) { got <- msg{port: p, a: a} },
		OnHome: func(p int) { got <- msg{home: true, port: p} }}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	addr := gatewayAddr(g, c.session(time.Second).id)
	if !addr.TakesAway() {
		t.Fatal("a version 4 session doesn't take away")
	}
	addr.Route(portHost, echo.addr())
	if !addr.RouteAway(portHost, testAway()) {
		t.Fatal("RouteAway didn't send")
	}
	select {
	case m := <-got:
		if m.home || m.port != portHost || !reflect.DeepEqual(m.a, testAway()) {
			t.Errorf("OnAway got %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no away message")
	}
	addr.sess.mu.Lock()
	_, routed := addr.sess.routes[portHost]
	addr.sess.mu.Unlock()
	if routed {
		t.Error("the port kept its route while away")
	}
	addr.Route(portHost, echo.addr())
	select {
	case m := <-got:
		if !m.home || m.port != portHost {
			t.Errorf("OnHome got %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no home message")
	}
	addr.Route(portHost, echo.addr()) // a port that isn't away: nothing is sent
	select {
	case m := <-got:
		t.Errorf("a second home: %+v", m)
	case <-time.After(200 * time.Millisecond):
	}
	if !buf.waitFor(2*time.Second, "away: port 23003 to the match gateway 203.0.113.7:42128", "port 23003 away to the match gateway",
		"home: port 23003 through this session again", "port 23003 home") {
		t.Errorf("the log:\n%s", buf.String())
	}

	// a gateway of a release before the away messages: the client steps down to version 3
	buf3 := &syncBuf{}
	old, _ := startGatewayWith(t, buf3, func(g *Gateway) { g.maxSessionHello = mapsVersion })
	oc := &Client{Log: logging.New(buf3), Gateway: old.Addr(), Pin: old.Identity.Pin, TunnelIP: net.ParseIP(tunIP),
		DialIP: net.ParseIP(peerIP), External: true, Maps: []InstalledMap{}, OnAway: func(int, Away) {}}
	if err := oc.Start(); err != nil {
		t.Fatal(err)
	}
	defer oc.Close()
	oaddr := gatewayAddr(old, oc.session(time.Second).id)
	if oaddr.TakesAway() || oaddr.RouteAway(portHost, testAway()) {
		t.Error("a version 3 session was sent away")
	}
	if _, named := oaddr.Maps(); !named {
		t.Error("the version 3 hello lost its maps")
	}
	if !buf3.waitFor(2*time.Second, "takes no away messages (a release before them): session hello version 3") {
		t.Errorf("the log:\n%s", buf3.String())
	}
	if (&Addr{}).TakesAway() || (&Addr{}).RouteAway(1, testAway()) {
		t.Error("an address without a session")
	}
}

// TestHelloSaysAway (PREF4): only a client that reads away messages (OnAway) or carries a ticket says the version 4
// hello; one without (dgserver's --tunnel-client, Start-Client's path without the front) says version 3, so the main
// server never sends its port away (Blaze keeps its players' games home: wantedBy), and version 2 without a map list.
func TestHelloSaysAway(t *testing.T) {
	buf := &syncBuf{}
	g, _ := startGatewayWith(t, buf, func(g *Gateway) { g.Forward = nil })
	for _, c := range []struct {
		what   string
		maps   []InstalledMap
		onAway func(int, Away)
		takes  bool
		named  bool
	}{
		{"with OnAway", []InstalledMap{}, func(int, Away) {}, true, true},
		{"without OnAway", []InstalledMap{}, nil, false, true},
		{"without OnAway and maps", nil, nil, false, false},
	} {
		cl := &Client{Log: logging.New(buf), Gateway: g.Addr(), Pin: g.Identity.Pin, TunnelIP: net.ParseIP(tunIP),
			DialIP: net.ParseIP(peerIP), External: true, Maps: c.maps, OnAway: c.onAway}
		if err := cl.Start(); err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		addr := gatewayAddr(g, cl.session(time.Second).id)
		if addr.TakesAway() != c.takes || addr.RouteAway(portHost, testAway()) != c.takes {
			t.Errorf("%s: TakesAway %v, want %v", c.what, addr.TakesAway(), c.takes)
		}
		if _, named := addr.Maps(); named != c.named {
			t.Errorf("%s: maps named %v, want %v", c.what, named, c.named)
		}
		cl.Close()
	}
	if s := buf.String(); strings.Contains(s, "takes no away messages") || strings.Contains(s, "takes no map list") {
		t.Errorf("a client stepped down:\n%s", s)
	}
}
