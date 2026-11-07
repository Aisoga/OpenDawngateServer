package blaze

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

// Who may reach a match host and who may claim a seat there: a real gateway, real tunnel clients and fake match hosts
// that really receive the forwarded datagrams and ask the server about each claim as the C++ host does. A datagram
// "claim <id>" to a GMS port stands for a HelloReq: the host answers "ok <id>" or "no <id>", and a session that reaches
// no host gets no answer at all. (The RakNet part, and what the host does with the verdict, is gms_tests' test_seatauth.)
type seatRig struct {
	t      *testing.T
	ts     *tcpServer
	g      *tunnel.Gateway
	gms    []int
	blaze  int
	nextIP int
}

const (
	seatGwIP, seatHostsIP = "127.0.9.60", "127.0.9.61"
	seatBlazePort         = 21041
	seatPortFirst         = 21042
	seatPortLast          = 21044
)

// a seat host: every "claim <id>" datagram is asked about with verifyHello, and the verdict goes back to the socket that
// sent it.
func playSeatHost(t *testing.T, env gmshost.Env, p *gmshost.FakeProcess) {
	gmshost.PlayHost(env, p, func(h *gmshost.HostConn, _ gmshost.CreateGame) error {
		addr, err := net.ResolveUDPAddr("udp4", env.Bind)
		if err != nil {
			return err
		}
		sock, err := net.ListenUDP("udp4", addr)
		if err != nil {
			return err
		}
		defer sock.Close()
		var mu sync.Mutex
		from := map[string]*net.UDPAddr{}
		go func() {
			buf := make([]byte, 2048)
			for {
				n, a, err := sock.ReadFromUDP(buf)
				if err != nil {
					return
				}
				var id int64
				if _, err := fmt.Sscanf(string(buf[:n]), "claim %d", &id); err != nil {
					continue
				}
				mu.Lock()
				from[a.String()] = a
				mu.Unlock()
				h.Send(gmshost.VerifyHello{Addr: a.String(), User: id})
			}
		}()
		for {
			m, err := h.Recv(0)
			if err != nil {
				return err
			}
			switch v := m.(type) {
			case gmshost.AbortGame:
				return nil
			case gmshost.HelloVerdict:
				mu.Lock()
				a := from[v.Addr]
				mu.Unlock()
				word := "no"
				if v.OK {
					word = "ok"
				}
				sock.WriteToUDP([]byte(fmt.Sprintf("%s %d", word, v.User)), a)
			}
		}
	})
}

func newSeatRig(t *testing.T) *seatRig {
	t.Helper()
	r := &seatRig{t: t, blaze: seatBlazePort, nextIP: 70}
	id, _, err := tunnel.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var hosts *gmshost.Manager
	r.ts = newTCPServer(t, func(s *Server) {
		l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) { playSeatHost(t, env, p) }}
		hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(seatPortFirst, seatPortLast),
			BindHost: seatHostsIP, GMSHost: seatHostsIP, ExitTimeout: time.Second})
		if err := hosts.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		s.Hosts = hosts
		s.GMSHost = seatHostsIP
	})
	t.Cleanup(hosts.Close)
	// as dgserver with match hosts: the GMS ports forwarded, and no Forward: only a session's route leads anywhere
	r.g = &tunnel.Gateway{Log: logging.New(r.ts.log), Identity: id, BindIP: net.ParseIP(seatHostsIP)}
	r.ts.s.TunnelFlow = r.g.FlowSession
	for p := seatPortFirst; p <= seatPortLast; p++ {
		r.g.ForwardUDP(p)
	}
	go r.ts.s.Serve(r.g.Listener(r.blaze))
	if err := r.g.Start(net.JoinHostPort(seatGwIP, "0")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.g.Close)
	return r
}

// a tunnel session: its own tunnel address, no login yet.
func (r *seatRig) session() string {
	r.t.Helper()
	r.nextIP++
	ip := fmt.Sprintf("127.0.9.%d", r.nextIP)
	c := &tunnel.Client{Log: logging.New(r.ts.log), Gateway: r.g.Addr(), Pin: r.g.Identity.Pin, TunnelIP: net.ParseIP(ip),
		DialIP: net.ParseIP(ip)}
	if err := c.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(c.Close)
	if !c.WaitUDP(3 * time.Second) {
		r.t.Fatal("no UDP through the tunnel")
	}
	return ip
}

// login is a Blaze login through the tunnel at ip.
func (r *seatRig) login(ip, email string) *tcpClient {
	r.t.Helper()
	d := net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(ip)}}
	c, err := d.Dial("tcp4", net.JoinHostPort(ip, fmt.Sprint(r.blaze)))
	if err != nil {
		r.t.Fatal(err)
	}
	tc := r.ts.clientOn(c, email)
	tc.login(email)
	return tc
}

// claim sends "claim <id>" from a game socket at ip to the GMS port, as the game's RakNet socket would, and returns the
// host's answer, "" when none comes (the session doesn't reach the host).
func (r *seatRig) claim(ip string, port int, id int64) string {
	r.t.Helper()
	sock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(ip)})
	if err != nil {
		r.t.Fatal(err)
	}
	defer sock.Close()
	to := &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
	buf := make([]byte, 256)
	for try := 0; try < 3; try++ { // datagrams: the host binds just after its gameReady
		sock.WriteToUDP([]byte(fmt.Sprintf("claim %d", id)), to)
		sock.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		if n, _, err := sock.ReadFromUDP(buf); err == nil {
			return string(buf[:n])
		}
	}
	return ""
}

func (r *seatRig) reports(what string) bool {
	for _, l := range r.g.Report() {
		if strings.Contains(l, what) {
			return true
		}
	}
	return false
}

func TestSeatAuth(t *testing.T) {
	r := newSeatRig(t)
	g := golden.Load(t)

	// alpha plays: its session has the route to the game's port
	sa := r.session()
	alpha := r.login(sa, "alpha@example.com")
	wantReply(t, "create", alpha.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	setup := decode(t, alpha.note(ComponentGameManager, NotifyGameSetupID))
	game, _ := setup.Sub("GAME")
	if game == nil {
		t.Fatal("no game in NotifyGameSetup")
	}
	port := seatPortFirst
	if got := r.claim(sa, port, alpha.id); got != fmt.Sprintf("ok %d", alpha.id) {
		t.Fatalf("alpha's own id through its own session: %q", got)
	}

	// a tunnel session with no login reaches no GMS port (it needs only the public release name)
	sb := r.session()
	if got := r.claim(sb, port, alpha.id); got != "" {
		t.Fatalf("a session with no login reached the host: %q", got)
	}
	if !r.reports("no route 3") {
		t.Errorf("the gateway counts no dropped datagrams:\n%s", strings.Join(r.g.Report(), "\n"))
	}

	// a login alone doesn't: bravo is in no game
	bravo := r.login(sb, "bravo@example.com")
	if got := r.claim(sb, port, bravo.id); got != "" {
		t.Fatalf("a login with no game reached the host: %q", got)
	}

	// looking at a game doesn't: getGameDataFromId and a browser list rewrite the address, with no route
	ask := tdf.NewWriter().List("GLST", tdf.TypeInt, []int64{1}).Bytes()
	bravo.reply(ComponentGameManager, CmdGameManagerGetGameDataFromID, bravo.send(ComponentGameManager, CmdGameManagerGetGameDataFromID, ask))
	bconn := r.ts.s.connsOf(bravo.id)[0]
	gms, ok := ParseAddr(seatHostsIP, port)
	if !ok {
		t.Fatal("ParseAddr")
	}
	listed := bconn.forClientListed([]ListedGame{{Game: Game{ID: 1, GMS: gms}}})
	if want, _ := ParseAddr(sb, port); listed[0].Game.GMS != want {
		t.Errorf("the list's address %v, want the tunnel address %v", listed[0].Game.GMS, want)
	}
	if got := r.claim(sb, port, bravo.id); got != "" {
		t.Fatalf("a look at a game opened the way to its host: %q", got)
	}

	// the other layer: with a route (a spectator's to the same game) a claim of alpha's id is refused, its own isn't
	if got := bconn.ForClient(Game{ID: 1, GMS: gms}).GMS; got != listed[0].Game.GMS {
		t.Errorf("ForClient's address %v", got)
	}
	if got := r.claim(sb, port, alpha.id); got != fmt.Sprintf("no %d", alpha.id) {
		t.Errorf("a session claiming another persona's id: %q, want refused", got)
	}
	if got := r.claim(sb, port, bravo.id); got != fmt.Sprintf("ok %d", bravo.id) {
		t.Errorf("a session claiming its own login's id: %q", got)
	}
	if !strings.Contains(r.ts.log.String(), fmt.Sprintf("claims player %d, not among its tunnel session's logins [%d]", alpha.id, bravo.id)) {
		t.Errorf("the refusal isn't logged:\n%s", r.ts.log.String())
	}

	// a real reconnect: a new tunnel session with the same login may claim it
	sc := r.session()
	again := r.login(sc, "alpha@example.com")
	if got := r.claim(sc, port, alpha.id); got != "" { // no route until it joins
		t.Errorf("the second login before its join: %q", got)
	}
	r.ts.s.connsOf(again.id)[1].ForClient(Game{ID: 1, GMS: gms})
	if got := r.claim(sc, port, alpha.id); got != fmt.Sprintf("ok %d", alpha.id) {
		t.Errorf("a reconnect from a new session: %q", got)
	}

	// several logins in one tunnel session (the test lanes' two clients): either id, not a third
	charlie := r.login(sa, "charlie@example.com")
	if got := r.claim(sa, port, charlie.id); got != fmt.Sprintf("ok %d", charlie.id) {
		t.Errorf("the session's second login: %q", got)
	}
	if got := r.claim(sa, port, bravo.id); got != fmt.Sprintf("no %d", bravo.id) {
		t.Errorf("another session's login: %q", got)
	}

	// a login that ended counts no more
	charlie.c.Close()
	for deadline := time.Now().Add(3 * time.Second); len(r.ts.s.connsOf(charlie.id)) > 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if got := r.claim(sa, port, charlie.id); got != fmt.Sprintf("no %d", charlie.id) {
		t.Errorf("a login that ended: %q", got)
	}
}

// The direct path (tunnel = off, LAN and home servers): no flow, so the claim must be a direct login from the same IP,
// every loopback address counting as one. Tunnelled logins never pass here.
func TestVerifyHelloDirect(t *testing.T) {
	ts := newTCPServer(t, nil)
	a := ts.clientFrom("alpha@example.com", "127.0.0.5")
	b := ts.client("bravo@example.com")
	mt := &gmshost.Match{ID: 7}
	check := func(what, addr string, user int64, want bool) {
		t.Helper()
		if got := ts.s.verifyHello(mt, gmshost.VerifyHello{Addr: addr, User: user}); got != want {
			t.Errorf("%s: %v, want %v", what, got, want)
		}
	}
	check("the same loopback address", "127.0.0.5:50000", a.id, true)
	check("another loopback address", "127.0.0.8:50000", a.id, true)
	check("from elsewhere", "203.0.113.9:50000", a.id, false)
	check("nobody's login", "127.0.0.5:50000", 2000000099, false)
	check("no address", "nonsense", a.id, false)

	// a persona logged in through the tunnel has no direct login to pass for
	ts.s.connsOf(b.id)[0].tun = tunnel.FakeSessionAddr(net.IPv4(127, 0, 0, 1), 10041, "v", true)
	check("a tunnelled login, directly", "127.0.0.1:50000", b.id, false)

	// --gms-hello-check off
	ts.s.NoHelloCheck = true
	check("no check", "203.0.113.9:50000", a.id, true)
}

// The line both sides write stays what the other reads.
func TestSeatAuthLines(t *testing.T) {
	line, err := gmshost.Encode(gmshost.HelloVerdict{Addr: "127.0.1.10:50123", User: 2000000002})
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(line, &v); err != nil || v["ok"] != false || v["addr"] != "127.0.1.10:50123" {
		t.Errorf("%s: %v", line, err)
	}
}
