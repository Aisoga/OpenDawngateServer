package tunnel

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// The test addresses: the gateway on 127.0.9.20 (as the server's machine), the tunnel on 127.0.9.21 (the player's
// loopback address), the impairing relay on 127.0.9.22 (the network between them). Ports 23001-23003 are the
// "server's" ports.
const (
	gwIP     = "127.0.9.20"
	tunIP    = "127.0.9.21"
	peerIP   = "127.0.9.22" // the test client's own sockets to the gateway: the "player's real address"
	relayIP  = "127.0.9.22"
	portTCP  = 23001
	portQoS  = 23002
	portHost = 23003
)

type testEnv struct {
	t      *testing.T
	g      *Gateway
	c      *Client
	echo   *udpEcho
	logBuf *syncBuf
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// udpEcho is a stand-in match host: it echoes every datagram and counts what it got.
type udpEcho struct {
	conn *net.UDPConn
	mu   sync.Mutex
	got  map[string]int
	from map[string]bool
}

func newUDPEcho(t *testing.T, ip string) *udpEcho {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(ip)})
	if err != nil {
		t.Fatal(err)
	}
	e := &udpEcho{conn: conn, got: map[string]int{}, from: map[string]bool{}}
	go func() {
		buf := make([]byte, 65536)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			e.mu.Lock()
			e.got[string(buf[:n])]++
			e.from[from.String()] = true
			e.mu.Unlock()
			conn.WriteToUDP(buf[:n], from)
		}
	}()
	t.Cleanup(func() { conn.Close() })
	return e
}

func (e *udpEcho) addr() *net.UDPAddr { return e.conn.LocalAddr().(*net.UDPAddr) }

func (e *udpEcho) counts() (distinct, dups int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, n := range e.got {
		distinct++
		dups += n - 1
	}
	return
}

// startGateway runs a gateway with an echo stream service on portTCP, an in-process datagram answer on portQoS and
// portHost forwarded to an echo socket.
func startGateway(t *testing.T, logw io.Writer) (*Gateway, *udpEcho) {
	t.Helper()
	return startGatewayWith(t, logw, nil)
}

// startGatewayWith is startGateway with more settings made before the start (nil: none).
func startGatewayWith(t *testing.T, logw io.Writer, set func(*Gateway)) (*Gateway, *udpEcho) {
	t.Helper()
	id, _, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	echo := newUDPEcho(t, gwIP)
	g := &Gateway{Log: logging.New(logw), Identity: id, BindIP: net.ParseIP(gwIP),
		Forward: func(port int) *net.UDPAddr { return echo.addr() }}
	ln := g.Listener(portTCP)
	g.HandleUDP(portQoS, func(p []byte, from *Addr, reply func([]byte) error) {
		reply([]byte(fmt.Sprintf("answer:%s|%s|%d|%s", p, from, from.TunnelSession(), from.Peer)))
	})
	g.ForwardUDP(portHost)
	rl := g.Listener(RedirectorPort)
	if set != nil {
		set(g)
	}
	if err := g.Start(net.JoinHostPort(gwIP, "0")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	for _, l := range []net.Listener{ln, rl} {
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go func() { // echo: everything until EOF, then the addresses the service saw
					defer c.Close()
					b, _ := io.ReadAll(c)
					la, ra := c.LocalAddr().(*Addr), c.RemoteAddr().(*Addr)
					fmt.Fprintf(c, "%s|%s|%s|%d|%s|%s", b, la, ra, la.Session, la.Peer, ra.Peer)
				}()
			}
		}()
	}
	return g, echo
}

func startClient(t *testing.T, gateway string, pin Pin, logw io.Writer) (*Client, error) {
	t.Helper()
	c := &Client{Log: logging.New(logw), Gateway: gateway, Pin: pin, TunnelIP: net.ParseIP(tunIP), DialIP: net.ParseIP(peerIP),
		RedirectorTLS: &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}}}
	if err := c.Start(); err != nil {
		return nil, err
	}
	t.Cleanup(c.Close)
	return c, nil
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	buf := &syncBuf{}
	g, echo := startGateway(t, buf)
	c, err := startClient(t, g.Addr(), g.Identity.Pin, buf)
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{t: t, g: g, c: c, echo: echo, logBuf: buf}
}

// gameUDP is a game socket on the tunnel address.
func gameUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(tunIP)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func roundTrip(t *testing.T, c *net.UDPConn, port int, msg []byte, wait time.Duration) ([]byte, *net.UDPAddr, error) {
	t.Helper()
	if _, err := c.WriteToUDP(msg, &net.UDPAddr{IP: net.ParseIP(tunIP), Port: port}); err != nil {
		return nil, nil, err
	}
	c.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 65536)
	n, from, err := c.ReadFromUDP(buf)
	if err != nil {
		return nil, nil, err
	}
	return buf[:n], from, nil
}

func streamRoundTrip(t *testing.T, port int, msg string) string {
	t.Helper()
	conn, err := net.DialTCP("tcp4", &net.TCPAddr{IP: net.ParseIP(tunIP)}, &net.TCPAddr{IP: net.ParseIP(tunIP), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte(msg))
	conn.CloseWrite()
	b, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTunnelStreamsAndDatagrams(t *testing.T) {
	e := newEnv(t)
	if !e.c.WaitUDP(2 * time.Second) {
		t.Fatal("no keepalive echo from the gateway")
	}
	tcp, udp := e.c.Ports()
	if !slices.Equal(tcp, []int{portTCP, RedirectorPort}) || !slices.Equal(udp, []int{portQoS, portHost}) {
		t.Errorf("ports TCP %v UDP %v", tcp, udp)
	}

	// a stream: the service sees the tunnel address and the game socket's own address, the client gets the answer
	got := streamRoundTrip(t, portTCP, "preAuth")
	parts := strings.Split(got, "|")
	if len(parts) != 6 || parts[0] != "preAuth" || parts[1] != net.JoinHostPort(tunIP, strconv.Itoa(portTCP)) ||
		!strings.HasPrefix(parts[2], tunIP+":") || parts[3] == "0" || parts[4] != peerIP || parts[5] != peerIP {
		t.Errorf("stream answer %q", got)
	}

	// an in-process datagram answer: from the port it went to, with the game socket's address
	game := gameUDP(t)
	b, from, err := roundTrip(t, game, portQoS, []byte("probe"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("answer:probe|%s|", game.LocalAddr()); !strings.HasPrefix(string(b), want) ||
		!strings.HasSuffix(string(b), "|"+peerIP) {
		t.Errorf("QoS answer %q, want %q...|%s", b, want, peerIP)
	}
	if from.String() != net.JoinHostPort(tunIP, strconv.Itoa(portQoS)) {
		t.Errorf("answer from %s", from)
	}

	// a forwarded datagram: the match host's echo comes back from the host port; the host sees the server's own address
	b, from, err = roundTrip(t, game, portHost, []byte("HelloReq"), 2*time.Second)
	if err != nil || string(b) != "HelloReq" || from.Port != portHost {
		t.Fatalf("host echo %q from %v: %v", b, from, err)
	}
	e.echo.mu.Lock()
	for a := range e.echo.from {
		if !strings.HasPrefix(a, gwIP+":") {
			t.Errorf("the host saw %s, not the gateway's address", a)
		}
	}
	e.echo.mu.Unlock()

	// the largest payload passes, one byte more is dropped (RakNet then takes a smaller MTU)
	big := bytes.Repeat([]byte{7}, MaxPayload)
	if b, _, err := roundTrip(t, game, portHost, big, 2*time.Second); err != nil || len(b) != MaxPayload {
		t.Errorf("MaxPayload datagram: %d bytes, %v", len(b), err)
	}
	if _, _, err := roundTrip(t, game, portHost, append(big, 7), 300*time.Millisecond); err == nil {
		t.Error("a datagram above MaxPayload came through")
	}
	if e.c.dropBig.Load() != 1 {
		t.Errorf("client dropBig %d", e.c.dropBig.Load())
	}
	// RakNet's 1492 MTU probe (1464 bytes) arrives cut to 1200's (1172): the host answers MTU 1200 at once
	probe := make([]byte, 1492-28)
	probe[0] = 0x05
	copy(probe[1:], rakNetOfflineMagic)
	probe[17] = 6
	if b, _, err := roundTrip(t, game, portHost, probe, 2*time.Second); err != nil || len(b) != TrimmedProbe || !bytes.Equal(b, probe[:TrimmedProbe]) {
		t.Errorf("MTU probe: %d bytes back, %v", len(b), err)
	}
	if e.c.trimmed.Load() != 1 {
		t.Errorf("client trimmed %d", e.c.trimmed.Load())
	}

	// the redirector's port: the game's TLS ends in the client, the service gets the plain bytes
	tc, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(tunIP)}}, "tcp4",
		net.JoinHostPort(tunIP, strconv.Itoa(RedirectorPort)), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	tc.Write([]byte("getServerInstance"))
	tc.CloseWrite()
	rb, _ := io.ReadAll(tc)
	tc.Close()
	if !strings.HasPrefix(string(rb), "getServerInstance|"+net.JoinHostPort(tunIP, strconv.Itoa(RedirectorPort))) {
		t.Errorf("redirector stream answer %q", rb)
	}
	if e.g.Sessions() != 1 {
		t.Errorf("%d sessions", e.g.Sessions())
	}
}

func TestTunnelWrongPin(t *testing.T) {
	buf := &syncBuf{}
	g, _ := startGateway(t, buf)
	other := g.Identity.Pin
	other[0] ^= 1
	if _, err := startClient(t, g.Addr(), other, buf); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Errorf("a client with another pin: %v", err)
	}
	if p, err := ProbePin(g.Addr(), 2*time.Second, nil); err != nil || p != g.Identity.Pin {
		t.Errorf("ProbePin: %v, %v", p == g.Identity.Pin, err)
	}
	// nothing that isn't a gateway passes as one: a plain TLS 1.3 server without the ALPN
	ln, err := tls.Listen("tcp4", gwIP+":0", &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()
	if _, err := ProbePin(ln.Addr().String(), 2*time.Second, nil); err == nil {
		t.Error("ProbePin accepted a TLS server that is no gateway")
	}
}

// TestProbeInfo: the info hello answers the gateway's version and opens no session; a gateway of a release before
// it (closing the connection at the unknown kind, as this one did) still gives its key, with no version; something
// that is no gateway fails as with ProbePin.
func TestProbeInfo(t *testing.T) {
	buf := &syncBuf{}
	g, _ := startGateway(t, buf)
	if g.Version == "" {
		t.Error("the gateway has no version after Start")
	}
	info, err := ProbeInfo(g.Addr(), 2*time.Second, nil)
	if err != nil || info.Pin != g.Identity.Pin || info.Version != g.Version || info.VersionErr != nil {
		t.Errorf("ProbeInfo: %+v, %v (the gateway's version %q)", info, err, g.Version)
	}
	// the gateway logs after its reply
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(buf.String(), "version asked (") && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if g.Sessions() != 0 || !strings.Contains(buf.String(), "version asked ("+g.Version) {
		t.Errorf("%d sessions after the info hello; log:\n%s", g.Sessions(), buf.String())
	}
	if v, facts, err := readInfoReply(bytes.NewReader(infoReply("Dawngate-Restoration-test\t2", map[string]string{"signup": "in vite"}))); err != nil ||
		v != "Dawngate-Restoration-test-2" || len(facts) != 1 || facts[FactSignup] != "in-vite" {
		t.Errorf("an info reply read back: %q, %v, %v (cleaned)", v, facts, err)
	}
	if info.Facts == nil || len(info.Facts) != 0 {
		t.Errorf("a gateway without facts: %v (want an empty set)", info.Facts)
	}
	// the server's facts: asked at each hello (a change while it runs shows)
	mode := "invite"
	var modeMu sync.Mutex
	gf, _ := startGatewayWith(t, io.Discard, func(g *Gateway) {
		g.Facts = func() map[string]string {
			modeMu.Lock()
			defer modeMu.Unlock()
			return map[string]string{FactSignup: mode}
		}
	})
	for _, want := range []string{"invite", "closed"} {
		modeMu.Lock()
		mode = want
		modeMu.Unlock()
		if info, err := ProbeInfo(gf.Addr(), 2*time.Second, nil); err != nil || info.Facts[FactSignup] != want || info.Version != gf.Version {
			t.Errorf("ProbeInfo with the facts: %+v, %v (want signup %s)", info, err, want)
		}
	}

	// an older gateway: the TLS handshake, the hello's head read, the connection closed (unknown hello kind)
	old, err := tls.Listen("tcp4", gwIP+":0", ServerTLS(g.Identity))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	go func() {
		for {
			c, err := old.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if _, kind, err := readHello(c); err == nil && kind != kindSession && kind != kindStream {
					return // "unknown hello kind": closed
				}
			}()
		}
	}()
	info, err = ProbeInfo(old.Addr().String(), 2*time.Second, nil)
	if err != nil || info.Pin != g.Identity.Pin || info.Version != "" || info.VersionErr == nil ||
		!strings.Contains(info.VersionErr.Error(), "before it") {
		t.Errorf("ProbeInfo of an older gateway: %+v, %v", info, err)
	}

	// no gateway: a plain TLS 1.3 server without the ALPN
	ln, err := tls.Listen("tcp4", gwIP+":0", &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()
	if _, err := ProbeInfo(ln.Addr().String(), 2*time.Second, nil); err == nil {
		t.Error("ProbeInfo accepted a TLS server that is no gateway")
	}
	// the wire: "DGTN", 1, 3; the reply status 0, the length, the text
	if got := infoHello(); string(got) != "DGTN\x01\x03" {
		t.Errorf("infoHello = %q", got)
	}
	// the facts: a count, then each key and value with a length byte; a reply of a release before them ends after
	// the version and reads without facts
	if got := infoReply("r1", nil); string(got) != "\x00\x02r1\x00" {
		t.Errorf("infoReply = %q", got)
	}
	if got := infoReply("r1", map[string]string{"signup": "open", "": "x"}); string(got) != "\x00\x02r1\x01\x06signup\x04open" {
		t.Errorf("infoReply with a fact = %q", got)
	}
	if v, facts, err := readInfoReply(bytes.NewReader([]byte("\x00\x02r1"))); err != nil || v != "r1" || facts != nil {
		t.Errorf("a reply without facts: %q, %v, %v", v, facts, err)
	}
	if _, _, err := readInfoReply(bytes.NewReader([]byte{StatusVersion})); err == nil {
		t.Error("readInfoReply took a refusal")
	}
}

// rawHello opens a TLS connection to the gateway and sends a hello by hand.
func rawHello(t *testing.T, g *Gateway, hello []byte) byte {
	t.Helper()
	c, err := tls.Dial("tcp4", g.Addr(), ClientTLS(g.Identity.Pin, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write(hello)
	var st [1]byte
	if _, err := io.ReadFull(c, st[:]); err != nil {
		t.Fatal(err)
	}
	return st[0]
}

func TestTunnelRefusals(t *testing.T) {
	e := newEnv(t)
	s := e.c.session(time.Second)
	if st := rawHello(t, e.g, streamHello{session: s.id, port: portTCP}.encode()); st != StatusNoSession {
		t.Errorf("stream with a wrong token: %s", StatusText(st))
	}
	if st := rawHello(t, e.g, streamHello{session: s.id + 1, token: s.token, port: portTCP}.encode()); st != StatusNoSession {
		t.Errorf("stream of an unknown session: %s", StatusText(st))
	}
	if st := rawHello(t, e.g, streamHello{session: s.id, token: s.token, port: 10040}.encode()); st != StatusPort {
		t.Errorf("stream to the control port: %s", StatusText(st))
	}
	if st := rawHello(t, e.g, sessionHello{tunnelIP: [4]byte{192, 168, 1, 2}, release: e.g.Version}.encode()); st != StatusAddress {
		t.Errorf("session for a LAN tunnel address: %s", StatusText(st))
	}
	h := sessionHello{tunnelIP: [4]byte{127, 0, 0, 1}}.encode()
	h[4] = 9
	if st := rawHello(t, e.g, h); st != StatusVersion {
		t.Errorf("version 9: %s", StatusText(st))
	}
	// a datagram sealed with another session's keys, or garbage, reaches nothing
	fake, _ := NewSealer(testKeys(9).Up, s.id)
	fake.next.Store(1 << 40) // ahead of the window: the gateway has to try the key
	pkt, _ := fake.Seal(InnerFor(KindData, portHost, &net.UDPAddr{IP: net.ParseIP(tunIP), Port: 1}), []byte("forged"))
	u, _ := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(gwIP), Port: e.g.Port()})
	u.Write(pkt)
	u.Write([]byte("not a tunnel datagram at all, long enough to be read"))
	u.Close()
	time.Sleep(200 * time.Millisecond)
	if d, _ := e.echo.counts(); d != 0 {
		t.Errorf("the host got %d datagrams", d)
	}
	gs := e.g.sessions[s.id]
	if gs.dropAuth.Load() != 1 {
		t.Errorf("gateway dropAuth %d", gs.dropAuth.Load())
	}
}

// relay is the network between client and gateway: TCP passes, UDP from the client goes through mangle.
type relay struct {
	tln    net.Listener
	udp    *net.UDPConn
	gw     *net.UDPConn
	mu     sync.Mutex
	client *net.UDPAddr
	mangle func(pkt []byte) [][]byte
}

func newRelay(t *testing.T, gateway string, mangle func([]byte) [][]byte) *relay {
	t.Helper()
	tln, err := net.Listen("tcp4", relayIP+":0")
	if err != nil {
		t.Fatal(err)
	}
	port := tln.Addr().(*net.TCPAddr).Port
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(relayIP), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	ga, _ := net.ResolveUDPAddr("udp4", gateway)
	gw, err := net.DialUDP("udp4", &net.UDPAddr{IP: net.ParseIP(relayIP)}, ga)
	if err != nil {
		t.Fatal(err)
	}
	r := &relay{tln: tln, udp: udp, gw: gw, mangle: mangle}
	t.Cleanup(func() { tln.Close(); udp.Close(); gw.Close() })
	go func() {
		for {
			c, err := tln.Accept()
			if err != nil {
				return
			}
			go func() {
				g, err := net.Dial("tcp4", gateway)
				if err != nil {
					c.Close()
					return
				}
				Pipe(c, g)
			}()
		}
	}()
	go func() {
		buf := make([]byte, 65536)
		for {
			n, from, err := udp.ReadFromUDP(buf)
			if err != nil {
				return
			}
			r.mu.Lock()
			r.client = from
			r.mu.Unlock()
			for _, p := range r.mangle(append([]byte(nil), buf[:n]...)) {
				gw.Write(p)
			}
		}
	}()
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := gw.Read(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			r.mu.Lock()
			to := r.client
			r.mu.Unlock()
			if to != nil {
				udp.WriteToUDP(buf[:n], to)
			}
		}
	}()
	return r
}

func (r *relay) addr() string { return r.tln.Addr().String() }

// TestTunnelImpairedPath: on the way to the gateway every datagram is sent twice (the second copy a replay), each
// pair of datagrams swapped (reordering), every fifth lost and every seventh changed on the way (a forgery or a
// broken datagram). The host gets each datagram at most once, every unchanged one that wasn't lost, and nothing else.
func TestTunnelImpairedPath(t *testing.T) {
	buf := &syncBuf{}
	g, echo := startGateway(t, buf)
	var mu sync.Mutex
	n, held := 0, []byte(nil)
	var lost, changed int
	r := newRelay(t, g.Addr(), func(p []byte) [][]byte {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n <= 8 { // the start-up keepalives pass untouched
			return [][]byte{p}
		}
		switch {
		case n%5 == 0:
			lost++
			return nil
		case n%7 == 0:
			changed++
			p[len(p)-3] ^= 0x10
		}
		if held == nil { // hold it back: it goes out after the next one
			held = p
			return nil
		}
		out := [][]byte{p, held, p, held} // swapped, then both again
		held = nil
		return out
	})
	c, err := startClient(t, r.addr(), g.Identity.Pin, buf)
	if err != nil {
		t.Fatal(err)
	}
	if !c.WaitUDP(3 * time.Second) {
		t.Fatal("no keepalive echo through the relay")
	}
	time.Sleep(600 * time.Millisecond) // the start-up keepalives are over
	game := gameUDP(t)
	to := &net.UDPAddr{IP: net.ParseIP(tunIP), Port: portHost}
	const total = 400
	for i := range total {
		game.WriteToUDP([]byte(fmt.Sprintf("datagram %03d", i)), to)
		time.Sleep(200 * time.Microsecond)
	}
	time.Sleep(500 * time.Millisecond)
	distinct, dups := echo.counts()
	mu.Lock()
	l, ch := lost, changed
	mu.Unlock()
	echo.mu.Lock()
	for msg := range echo.got {
		if !strings.HasPrefix(msg, "datagram ") {
			t.Errorf("the host got %q", msg)
		}
	}
	echo.mu.Unlock()
	if dups != 0 {
		t.Errorf("the host got %d duplicates", dups)
	}
	if distinct == 0 || distinct > total-l-ch+1 { // +1: the last one may still be held back
		t.Errorf("the host got %d distinct datagrams of %d (lost %d, changed %d)", distinct, total, l, ch)
	}
	var s *session
	g.mu.Lock()
	for _, x := range g.sessions {
		s = x
	}
	g.mu.Unlock()
	t.Logf("relay: %d sent, %d lost, %d changed; host got %d; gateway: %s", total, l, ch, distinct, s.counts())
	if s.dropReplay.Load() == 0 || s.dropAuth.Load() != uint64(2*ch) { // each changed one was sent twice
		t.Errorf("gateway drops: replay %d (want > 0), auth %d (want %d)", s.dropReplay.Load(), s.dropAuth.Load(), 2*ch)
	}
	if distinct < total-l-ch-2 {
		t.Errorf("only %d of %d datagrams arrived (lost %d, changed %d)", distinct, total, l, ch)
	}
}

// TestTunnelResume: the control connection breaks; the client takes the session up again with new keys and the
// datagrams flow on.
func TestTunnelResume(t *testing.T) {
	e := newEnv(t)
	game := gameUDP(t)
	if _, _, err := roundTrip(t, game, portHost, []byte("before"), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	first := e.c.session(time.Second)
	e.g.mu.Lock()
	gs := e.g.sessions[first.id]
	e.g.mu.Unlock()
	gs.mu.Lock()
	gs.control.Close()
	gs.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if s := e.c.session(0); s != nil && s != first {
			if s.id != first.id {
				t.Errorf("session %d after the break, want %d taken up again", s.id, first.id)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the client didn't connect again")
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _, err := roundTrip(t, game, portHost, []byte("after"), 2*time.Second)
	if err != nil || string(b) != "after" {
		t.Fatalf("after the break: %q %v", b, err)
	}
	if e.c.resumes.Load() != 1 || !strings.Contains(e.logBuf.String(), "taken up again") {
		t.Errorf("resumes %d; log:\n%s", e.c.resumes.Load(), e.logBuf.String())
	}
}

// TestTunnelRoute: Blaze routes a port to another host (an agent machine's match); the gateway forwards there.
func TestTunnelRoute(t *testing.T) {
	buf := &syncBuf{}
	g, _ := startGateway(t, buf)
	other := newUDPEcho(t, gwIP)
	ln := g.listeners[portTCP]
	_ = ln
	c, err := startClient(t, g.Addr(), g.Identity.Pin, buf)
	if err != nil {
		t.Fatal(err)
	}
	s := c.session(time.Second)
	g.mu.Lock()
	gs := g.sessions[s.id]
	g.mu.Unlock()
	(&Addr{sess: gs}).Route(portHost, other.addr())
	game := gameUDP(t)
	if b, _, err := roundTrip(t, game, portHost, []byte("to the agent"), 2*time.Second); err != nil || string(b) != "to the agent" {
		t.Fatalf("%q %v", b, err)
	}
	if d, _ := other.counts(); d != 1 {
		t.Errorf("the routed host got %d datagrams", d)
	}
}

func median(d []time.Duration) time.Duration {
	slices.Sort(d)
	return d[len(d)/2]
}

// TestTunnelLatency measures what the tunnel adds on this machine (loopback): a datagram's round trip to a match
// host's echo directly and through the tunnel, and a stream's connect-and-answer time. The numbers go to the test
// log (go test -v -run Latency); has them.
func TestTunnelLatency(t *testing.T) {
	e := newEnv(t)
	game := gameUDP(t)
	// the clock steps 0.5-1 ms on Windows, so each figure is a batch's mean
	const n, batch = 20, 25
	direct, tunnelled := make([]time.Duration, 0, n), make([]time.Duration, 0, n)
	buf := make([]byte, 2048)
	msg := bytes.Repeat([]byte{1}, 200)
	for range n {
		t0 := time.Now()
		for range batch {
			game.WriteToUDP(msg, e.echo.addr())
			game.SetReadDeadline(time.Now().Add(time.Second))
			if _, _, err := game.ReadFromUDP(buf); err != nil {
				t.Fatal(err)
			}
		}
		direct = append(direct, time.Since(t0)/batch)
		t0 = time.Now()
		for range batch {
			if _, _, err := roundTrip(t, game, portHost, msg, time.Second); err != nil {
				t.Fatal(err)
			}
		}
		tunnelled = append(tunnelled, time.Since(t0)/batch)
	}
	t0 := time.Now()
	const streams = 40
	for range streams {
		streamRoundTrip(t, portTCP, "x")
	}
	st := time.Since(t0) / streams
	dm, tm := median(direct), median(tunnelled)
	t.Logf("UDP round trip (200 bytes), median of %d batches of %d: direct %v, through the tunnel %v (added %v); a new stream's connect and answer %v",
		n, batch, dm, tm, tm-dm, st)
	if tm > 50*time.Millisecond {
		t.Errorf("tunnel round trip %v", tm)
	}
}
