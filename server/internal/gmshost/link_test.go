package gmshost

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/tunnel"
)

// REGION1: host machines on the TLS link through the main server's tunnel gateway (a real tunnel.Gateway on an
// ephemeral loopback port, its Agents hook = Manager.ServeAgent), each with its own key, and the agent's relay for
// its hosts' control lines. The hosts are fake (goroutines); nothing binds a GMS port.

var (
	keyNA   = []byte("na1-0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab")
	keyNA2  = []byte("na2-0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab")
	linkPin = strings.Repeat("ab", 32)
	ringSHA = strings.Repeat("0f", 32)
)

const linkVersion = "v0.17-test"

var linkStarted = time.Date(2026, 11, 9, 18, 30, 5, 0, time.UTC)

type linkMain struct {
	m   *Manager
	log *syncBuf
	g   *tunnel.Gateway
}

// newLinkMain runs a manager that knows na1 (region na) and na2 (region na) with their own keys, and a gateway
// that hands agent links to it. tweak may change the config first.
func newLinkMain(t *testing.T, local *FakeLauncher, tweak func(*Config)) *linkMain {
	t.Helper()
	log := &syncBuf{}
	cfg := Config{Log: logging.New(log),
		HelloTimeout: 2 * time.Second, ReadyTimeout: 2 * time.Second, HeartbeatTimeout: 2 * time.Second,
		ExitTimeout: 500 * time.Millisecond, AgentTimeout: 2 * time.Second, LaunchTimeout: time.Second,
		Link: LinkConfig{Machines: map[string]HostMachine{"na1": {Region: "na", Key: keyNA}, "na2": {Region: "na", Key: keyNA2}},
			Version: linkVersion, Ports: "21040-21049", Started: linkStarted,
			Options: "net_limits=1;net_limit_shared_ips=10.0.0.1;match_record=1"}}
	if local != nil {
		cfg.Launcher, cfg.Pool = local, NewPool(20042, 20042)
	}
	if tweak != nil {
		tweak(&cfg)
	}
	m := NewManager(cfg)
	if err := m.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	id, _, err := tunnel.LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := &tunnel.Gateway{Log: logging.New(log), Identity: id, Agents: m.ServeAgent}
	if err := g.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return &linkMain{m: m, log: log, g: g}
}

// linkAgent is an agent on the TLS link to lm as name with key and build (not waited for: see waitSite).
func linkAgent(t *testing.T, lm *linkMain, name string, key []byte, build string, l *FakeLauncher, tweak func(*Agent)) *testAgent {
	t.Helper()
	ta := &testAgent{l: l, log: &syncBuf{}, done: make(chan struct{})}
	ta.Agent = &Agent{Log: logging.New(ta.log), Server: lm.g.Addr(), Key: key, Name: name, BindHost: "127.0.0.1",
		PublicHost: "192.168.1.30", Ports: "21042-21043", Build: build, Launcher: l, Retry: 100 * time.Millisecond,
		Heartbeat: 100 * time.Millisecond,
		Link: AgentLink{Pin: lm.g.Identity.Pin, Relay: "127.0.0.1:0", Gateway: "192.168.1.30:42128", GatewayPin: linkPin,
			Maps: map[string]string{"zz_cm_ring.snap": ringSHA}, SharedIPs: "127.0.0.1", RetrySlow: 300 * time.Millisecond}}
	if tweak != nil {
		tweak(ta.Agent)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ta.cancel = cancel
	go func() {
		if err := ta.Run(ctx); err != nil {
			t.Errorf("agent %s: %v", name, err)
		}
		close(ta.done)
	}()
	t.Cleanup(ta.stop)
	return ta
}

// A match on a host machine over the TLS link: the registration names the machine's region, match gateway, pin and
// maps, both ends derive the same ticket key, the host dials the agent's relay (not the main server), its lines
// travel in the link, the main server's options reach it with the machine's own shared address, and its folder is
// named by the main server's start.
func TestLinkMatch(t *testing.T) {
	lm := newLinkMain(t, nil, nil)
	ta := linkAgent(t, lm, "na1", keyNA, linkVersion, &FakeLauncher{Run: fullMatch}, nil)
	waitSite(t, lm.m, "na1", true)
	info, ok := lm.m.Machine("na1")
	if !ok || info.Region != "na" || info.Gateway != "192.168.1.30:42128" || info.Pin != linkPin || info.Addr != "127.0.0.1" ||
		!reflect.DeepEqual(info.Maps, map[string]string{"zz_cm_ring.snap": ringSHA}) {
		t.Errorf("machine %+v %v", info, ok)
	}
	if len(info.TicketKey) != 32 || hex.EncodeToString(info.TicketKey) != hex.EncodeToString(ta.TicketKey()) {
		t.Errorf("ticket keys: main %x, agent %x", info.TicketKey, ta.TicketKey())
	}
	if d := time.Since(ta.ServerNow()); d > time.Second || d < -time.Second {
		t.Errorf("the agent's server clock is %v off", d)
	}
	rec := newRecorder()
	mt, err := lm.m.Start(practice, rec.Events())
	if err != nil {
		t.Fatal(err)
	}
	if mt.Site != "na1" || mt.GMSHost != "192.168.1.30" || mt.Bind != "127.0.0.1:21042" {
		t.Errorf("match %+v", mt)
	}
	o := rec.wait(t)
	if !o.Ready || !o.Finished || o.Killed || o.ExitCode != 0 || o.Reason != "guardian" {
		t.Errorf("outcome %+v", o)
	}
	want := []string{"ready 21042", "add 2000000001/0", "update inGame", "player 2000000001 map[kills:3]", "finish 1 guardian", "end"}
	if got := rec.list(); !reflect.DeepEqual(got, want) {
		t.Errorf("events\n got %v\nwant %v", got, want)
	}
	env := ta.l.Envs()[0]
	if env.Control != ta.relayAddr() || env.Control == lm.m.Addr() || env.Match != mt.ID || env.Bind != "127.0.0.1:21042" ||
		env.DataName != "20261109-183005_1" || env.Options != "net_limits=1;match_record=1;net_limit_shared_ips=127.0.0.1" {
		t.Errorf("host environment %+v (relay %s)", env, ta.relayAddr())
	}
	waitLog(t, ta.log, "match 1: pid ")
	mlog, alog := lm.log.String(), ta.log.String()
	hasAll(t, "server", mlog,
		`host machine "na1" (region na) from ('127.0.0.1', `,
		`) registered over the TLS link (v0.17-test): GMS host 192.168.1.30, bind 127.0.0.1, ports 21042-21043, match gateway 192.168.1.30:42128 (pin abababababababab), 1 maps`,
		`[host] agent na1 -> {"t":"launchHost","match":1,"token":"...","port":21042,"options":"net_limits=1;net_limit_shared_ips=10.0.0.1;match_record=1"}`,
		fmt.Sprintf("[host] match 1: hello from pid %d (fake)", mt.Pid()),
		`[host] agent na1: match 1: the host's channel closed there: the host closed its control connection`,
		": finished (winner team 1): guardian; exit code 0, killed false; port 21042 free")
	hasAll(t, "agent", alog,
		`registered with `+lm.g.Addr()+` over the TLS link as "na1" (seen from 127.0.0.1; main server started 20261109-183005)`,
		fmt.Sprintf("match 1: the host's control connection relayed (pid %d)", mt.Pid()),
		"match 1: started pid ", "DGGMS_CONTROL "+ta.relayAddr())
	for what, log := range map[string]string{"server": mlog, "agent": alog} {
		if strings.Contains(log, env.Token) {
			t.Errorf("the %s log shows the token", what)
		}
		if strings.Contains(log, `"hostLine"`) {
			t.Errorf("the %s log shows the relayed lines", what)
		}
	}
}

// rawLink is a hand-driven agent on the TLS link.
type rawLink struct {
	t   *testing.T
	c   *tls.Conn
	r   *LineReader
	exp string
}

func dialLink(t *testing.T, lm *linkMain) *rawLink {
	t.Helper()
	c, err := tunnel.DialAgent(context.Background(), lm.g.Addr(), lm.g.Identity.Pin, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	exp, err := tunnel.AgentExporter(c)
	if err != nil {
		t.Fatal(err)
	}
	return &rawLink{t: t, c: c, r: newLinkReader(c), exp: hex.EncodeToString(exp)}
}

func (rl *rawLink) send(m Message) {
	rl.t.Helper()
	if _, err := writeLine(rl.c, m); err != nil {
		rl.t.Fatal(err)
	}
}

func (rl *rawLink) recv(d time.Duration) (Message, error) {
	rl.c.SetReadDeadline(time.Now().Add(d))
	line, err := rl.r.Next()
	if err != nil {
		return nil, err
	}
	return Decode(line)
}

// closed checks the server closed the link (not a timeout).
func (rl *rawLink) closed() {
	rl.t.Helper()
	if m, err := rl.recv(3 * time.Second); err == nil {
		rl.t.Errorf("got %#v, want the link closed", m)
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		rl.t.Error("the link is still open")
	}
}

var linkOffer = AgentAuth{Bind: "127.0.0.1", Public: "192.168.1.31", Ports: "21044-21044"}

// hello sends agentHello and returns the challenge (its proof checked against key on this session).
func (rl *rawLink) hello(key []byte, name, build string) AgentChallenge {
	rl.t.Helper()
	rl.send(AgentHello{Name: name, Nonce: nonceA, Build: build})
	m, err := rl.recv(3 * time.Second)
	ch, ok := m.(AgentChallenge)
	if err != nil || !ok {
		rl.t.Fatalf("challenge: %v %v", m, err)
	}
	if !proofOK(ch.Proof, serverLinkProof(key, nonceA, ch.Nonce, rl.exp)) {
		rl.t.Error("the server's proof doesn't check out on this session")
	}
	return ch
}

// auth sends the agentAuth for o with key, bound to exporter.
func (rl *rawLink) auth(key []byte, name string, ch AgentChallenge, o AgentAuth, exporter string) {
	rl.t.Helper()
	o.Proof = agentLinkProof(key, ch.Nonce, nonceA, name, o, exporter)
	rl.send(o)
}

// The registration's failure table: a wrong key, an unknown name (the same steps as a wrong key),
// an agentAuth made on another TLS session, another release (agentRefused, the agent waits RetrySlow), a wrong pin
// (nothing sent), ports outside the main server's, a bad match gateway or map; a right one registers.
func TestLinkAuth(t *testing.T) {
	lm := newLinkMain(t, nil, func(c *Config) { c.AgentTimeout = 10 * time.Second })

	// the agent refuses a server without its key (another key there, or an unknown name): it checks first
	wrong := linkAgent(t, lm, "na1", []byte("not-the-key-0123456789abcdef"), linkVersion, &FakeLauncher{Run: fullMatch}, nil)
	unknown := linkAgent(t, lm, "eu9", keyNA, linkVersion, &FakeLauncher{Run: fullMatch}, nil)
	waitLog(t, wrong.log, `the main server failed the key check (it has another key for "na1", or doesn't know the name); not registering; next attempt in 300ms`)
	waitLog(t, unknown.log, `the main server failed the key check (it has another key for "eu9", or doesn't know the name); not registering`)
	waitLog(t, lm.log, `host machine "eu9" from `)
	waitLog(t, lm.log, `rejected: unknown host machine name`)
	waitLog(t, lm.log, `host machine "na1" from `)
	waitLog(t, lm.log, `rejected: no agentAuth: EOF (the agent found the challenge's proof wrong: another key?)`)
	wrong.stop()
	unknown.stop()

	// another release: refused with the reason, the agent tells and waits
	old := linkAgent(t, lm, "na2", keyNA2, "v0.16", &FakeLauncher{Run: fullMatch}, nil)
	waitLog(t, old.log, "refused by the main server: version v0.16, this server v0.17-test (this machine v0.16); next attempt in 300ms")
	waitLog(t, lm.log, `host machine "na2" from `)
	waitLog(t, lm.log, `rejected: version v0.16, this server v0.17-test (both machines run one release)`)
	old.stop()

	// a wrong pin: the TLS handshake fails, nothing is sent
	pinned := linkAgent(t, lm, "na2", keyNA2, linkVersion, &FakeLauncher{Run: fullMatch}, func(a *Agent) { a.Link.Pin[3] ^= 0x40 })
	waitLog(t, pinned.log, "the main server's key is not the pinned one (")
	waitLog(t, pinned.log, "next attempt in 300ms")
	pinned.stop()

	// an agentAuth made for another TLS session (a relay that terminates TLS, or a replay) fails the proof
	x := dialLink(t, lm)
	y := dialLink(t, lm)
	ch := y.hello(keyNA2, "na2", linkVersion)
	y.auth(keyNA2, "na2", ch, linkOffer, x.exp)
	y.closed()
	waitLog(t, lm.log, `rejected: wrong proof (another key, or an agentAuth from another TLS session)`)

	// the offer: ports outside the main server's, a bad match gateway, pin or map
	for _, c := range []struct {
		set  func(*AgentAuth)
		want string
	}{
		{func(o *AgentAuth) { o.Ports = "21048-21050" }, "ports 21048-21050 are not inside this server's GMS ports 21040-21049"},
		{func(o *AgentAuth) { o.Gateway, o.Pin = "0.0.0.0:42128", linkPin }, `match gateway "0.0.0.0:42128" is not an IPv4 address and port`},
		{func(o *AgentAuth) { o.Gateway, o.Pin = "192.168.1.31:42128", "abc" }, `match gateway pin "abc" is not 64 hex digits`},
		{func(o *AgentAuth) { o.Maps = map[string]string{"ZZ_X.snap": ringSHA} }, `map "ZZ_X.snap"`},
	} {
		o := linkOffer
		c.set(&o)
		rl := dialLink(t, lm)
		rl.auth(keyNA2, "na2", rl.hello(keyNA2, "na2", linkVersion), o, rl.exp)
		rl.closed()
		waitLog(t, lm.log, c.want)
	}

	// and the right one registers: agentReady with the server's clock and start
	ok := dialLink(t, lm)
	ok.auth(keyNA2, "na2", ok.hello(keyNA2, "na2", linkVersion), linkOffer, ok.exp)
	m, err := ok.recv(3 * time.Second)
	ready, isReady := m.(AgentReady)
	if err != nil || !isReady || ready.Addr != "127.0.0.1" || ready.Started != "20261109-183005" || time.Since(time.UnixMilli(ready.Now)).Abs() > 5*time.Second {
		t.Fatalf("agentReady %#v %v", m, err)
	}
	waitSite(t, lm.m, "na2", true)
	if _, found := lm.m.Machine("na1"); found {
		t.Error("na1 is registered")
	}
}

// The link lost: the machine's running match ends as aborted ("agent na1 lost"), the agent kills its host and its
// relay's channel, and registers again.
func TestLinkLost(t *testing.T) {
	lm := newLinkMain(t, nil, nil)
	ta := linkAgent(t, lm, "na1", keyNA, linkVersion, &FakeLauncher{Run: untilKilled}, nil)
	waitSite(t, lm.m, "na1", true)
	rec := newRecorder()
	if _, err := lm.m.Start(practice, rec.Events()); err != nil {
		t.Fatal(err)
	}
	if port := <-rec.ready; port != 21042 {
		t.Errorf("ready on %d", port)
	}
	if !lm.m.dropAgent("na1", "test: the link broke") {
		t.Fatal("no agent na1")
	}
	o := rec.wait(t)
	if o.Finished || o.ExitCode != -1 || !strings.Contains(o.Reason, "agent na1 lost") {
		t.Errorf("outcome %+v", o)
	}
	waitLog(t, ta.log, "the link to the main server ended: killing 1 hosts")
	waitLog(t, ta.log, "match 1: the host's channel closed: the link to the main server ended")
	waitLogN(t, ta.log, "over the TLS link as \"na1\"", 2)
	waitSite(t, lm.m, "na1", true)
}

// The relay: a host of a linked machine says hello only through that machine's link (not to the control port
// directly, and no hello for another machine's match through it); the relay checks the hello itself; a host's own
// verifyHello with a flow member is dropped, the others pass Annotate; the server's close of a channel closes the
// host's connection.
func TestLinkRelay(t *testing.T) {
	local := &FakeLauncher{Run: func(env Env, p *FakeProcess) { <-p.Killed(); p.Exit(-1) }} // never says hello
	direct := make(chan error, 1)
	verdicts := make(chan Message, 4)
	lm := newLinkMain(t, local, nil)
	host := func(env Env, p *FakeProcess) {
		// first straight to the main server's control port with the right token: refused there
		d := env
		d.Control = lm.m.Addr()
		h, err := DialHost(d, p.Pid(), "fake")
		if err == nil {
			_, err = h.Recv(3 * time.Second)
			h.Close()
		}
		direct <- err
		PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
			// a verifyHello with a flow member of its own never arrives; the next one does (annotated)
			h.mu.Lock()
			h.c.Write([]byte(`{"t":"verifyHello","addr":"127.0.0.1:5000","user":7,"flow":{"match":1,"users":[7]}}` + "\n"))
			h.mu.Unlock()
			if err := h.Send(VerifyHello{Addr: "127.0.0.1:5001", User: 8}); err != nil {
				return err
			}
			for {
				m, err := h.Recv(5 * time.Second)
				if err != nil {
					return err
				}
				verdicts <- m
			}
		})
	}
	ta := linkAgent(t, lm, "na1", keyNA, linkVersion, &FakeLauncher{Run: host}, func(a *Agent) {
		a.Link.Annotate = func(match int64, line []byte) []byte {
			return append(line[:len(line)-1:len(line)-1], []byte(fmt.Sprintf(`,"flow":{"match":%d,"users":[8]}}`, match))...)
		}
	})
	waitSite(t, lm.m, "na1", true)
	asked := make(chan VerifyHello, 4)
	rec := newRecorder()
	ev := rec.Events()
	ev.VerifyHello = func(mt *Match, v VerifyHello) bool { asked <- v; return true }
	// the local site has the most free ports after na1 took one? na1 has two, local one: na1 first
	mt, err := lm.m.Start(practice, ev)
	if err != nil {
		t.Fatal(err)
	}
	if mt.Site != "na1" {
		t.Fatalf("match on %s", mt.Site)
	}
	if err := <-direct; err == nil {
		t.Error("the host's hello straight to the control port was answered")
	}
	waitLog(t, lm.log, "hello for match 1 not through the link of na1, where its host runs")
	select {
	case v := <-asked:
		if v.Addr != "127.0.0.1:5001" || v.User != 8 || v.Flow == nil || v.Flow.Match != 1 || fmt.Sprint(v.Flow.Users) != "[8]" {
			t.Errorf("verifyHello %+v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no verifyHello reached the server")
	}
	select {
	case m := <-verdicts:
		if m != (HelloVerdict{Addr: "127.0.0.1:5001", User: 8, OK: true}) {
			t.Errorf("verdict %#v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no verdict reached the host")
	}
	waitLog(t, ta.log, "match 1: a verifyHello with a flow member of its own dropped")
	waitLog(t, lm.log, `"user":8,"flow":{"match":1,"users":[8]}}`)
	if strings.Contains(lm.log.String(), `"user":7`) {
		t.Error("the host's own flow member reached the server")
	}

	// the relay checks a hello itself: a wrong token, a match that doesn't run here
	for _, hello := range []Hello{{Match: mt.ID, Token: strings.Repeat("0", 32)}, {Match: 99, Token: strings.Repeat("0", 32)}} {
		h, err := DialHost(Env{Control: ta.relayAddr(), Match: hello.Match, Token: hello.Token}, 1, "fake")
		if err != nil {
			t.Fatal(err)
		}
		if m, err := h.Recv(3 * time.Second); err == nil {
			t.Errorf("relay answered %#v", m)
		}
		h.Close()
	}
	waitLogN(t, ta.log, "doesn't run here, or the token is wrong", 2)

	// a hello for another machine's match through this link: a local match's token, sent by a raw linked agent
	// registered as na2: the main server refuses it and closes the channel (hostClose)
	local2 := newRecorder()
	g2 := practice
	g2.GameID = 2
	lmt, err := lm.m.Start(g2, local2.Events())
	if err != nil {
		t.Fatal(err)
	}
	if lmt.Site != LocalSite {
		t.Fatalf("second match on %s", lmt.Site)
	}
	var tok string
	for _, e := range local.Envs() {
		if e.Match == lmt.ID {
			tok = e.Token
		}
	}
	rl := dialLink(t, lm)
	rl.auth(keyNA2, "na2", rl.hello(keyNA2, "na2", linkVersion), linkOffer, rl.exp)
	if m, err := rl.recv(3 * time.Second); err != nil {
		t.Fatalf("na2: %v %v", m, err)
	}
	line, _ := Encode(Hello{Match: lmt.ID, Token: tok, PID: 1, Build: "raw"})
	rl.send(HostLine{Match: lmt.ID, Line: json.RawMessage(line[:len(line)-1])})
	for {
		m, err := rl.recv(3 * time.Second)
		if err != nil {
			t.Fatalf("no hostClose: %v", err)
		}
		if c, ok := m.(HostClose); ok {
			if c.Match != lmt.ID || c.Why != "the match closed the host's connection" {
				t.Errorf("hostClose %+v", c)
			}
			break
		}
	}
	waitLog(t, lm.log, fmt.Sprintf("hello for match %d not through the link of local, where its host runs", lmt.ID))
	// a first line that is no hello closes the channel at once
	rl.send(HostLine{Match: 77, Line: json.RawMessage(`{"t":"gameReady","port":1}`)})
	for {
		m, err := rl.recv(3 * time.Second)
		if err != nil {
			t.Fatalf("no hostClose: %v", err)
		}
		if c, ok := m.(HostClose); ok {
			if c.Match != 77 || c.Why != "the first line is not the host's hello" {
				t.Errorf("hostClose %+v", c)
			}
			break
		}
	}
	if regexp.MustCompile(`"token":"[0-9a-f]{32}"`).MatchString(lm.log.String() + ta.log.String()) {
		t.Error("a log shows a token")
	}
}

// The agent's checks of its link configuration, and the options it gives its hosts.
func TestAgentLinkCheck(t *testing.T) {
	base := func() *Agent {
		return &Agent{Server: "dawngate.example:42128", Key: keyNA, Name: "na1", BindHost: "127.0.0.1", PublicHost: "203.0.113.5",
			Ports: "10042-10051", Launcher: &FakeLauncher{}, Link: AgentLink{Pin: tunnel.Pin{1}, Relay: "127.0.0.1:10040"}}
	}
	if err := base().Check(); err != nil {
		t.Errorf("good agent: %v", err)
	}
	for _, c := range []struct {
		set  func(*Agent)
		want string
	}{
		{func(a *Agent) { a.Link.Relay = "0.0.0.0:10040" }, "must be a loopback IPv4 address"},
		{func(a *Agent) { a.Link.Relay = "127.0.0.1" }, "bad relay address"},
		{func(a *Agent) { a.Link.Gateway = "203.0.113.5:42128" }, "is not 64 hex digits"},
		{func(a *Agent) { a.Link.Maps = map[string]string{"x.snap": "12"} }, `map "x.snap" "12"`},
	} {
		a := base()
		c.set(a)
		if err := a.Check(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.want, err)
		}
	}
	a := base()
	if got := a.hostOptions(""); got != "" {
		t.Errorf("no main options: %q", got)
	}
	if got := a.hostOptions("fog=1"); got != "fog=1" {
		t.Errorf("no shared address of its own: %q", got)
	}
	a.Link.SharedIPs = "127.0.0.1"
	if got := a.hostOptions("Net_Limit_Shared_IPs=1.2.3.4;fog=1;"); got != "fog=1;net_limit_shared_ips=127.0.0.1" {
		t.Errorf("replaced: %q", got)
	}
	if got := mapsPart(map[string]string{"b": "2", "a": "1"}); got != "a=1,b=2" {
		t.Errorf("mapsPart %q", got)
	}
}
