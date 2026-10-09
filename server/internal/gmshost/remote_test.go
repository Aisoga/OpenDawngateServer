package gmshost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// H2: a main server (a Manager on an ephemeral loopback port) and agents (in-process Agents whose
// launchers start fake hosts: goroutines speaking the control protocol, as C1a's tests do). Nothing
// binds a GMS port; the agents' bind and public hosts are only strings here.

var testKey = []byte("0123456789abcdef0123456789abcdef")

// newMain runs a manager that takes agents with testKey; local, if not nil, also runs hosts here
// (ports 20042..20042+localPorts-1). tweak may change the config first.
func newMain(t *testing.T, local *FakeLauncher, localPorts int, tweak func(*Config)) (*Manager, *syncBuf) {
	t.Helper()
	log := &syncBuf{}
	cfg := Config{Log: logging.New(log), AgentKey: testKey,
		HelloTimeout: 2 * time.Second, ReadyTimeout: 2 * time.Second, HeartbeatTimeout: 2 * time.Second,
		ExitTimeout: 500 * time.Millisecond, AgentTimeout: 2 * time.Second, LaunchTimeout: time.Second}
	if local != nil {
		cfg.Launcher, cfg.Pool = local, NewPool(20042, 20042+localPorts-1)
	}
	if tweak != nil {
		tweak(&cfg)
	}
	m := NewManager(cfg)
	if err := m.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, log
}

type testAgent struct {
	*Agent
	l      *FakeLauncher
	log    *syncBuf
	cancel context.CancelFunc
	done   chan struct{}
}

// runAgent runs an agent named name against m with fake hosts (run) and waits until it is
// registered. Bind is 10.1.2.3 (nothing binds it), the public host public, the ports ports.
func runAgent(t *testing.T, m *Manager, name, public, ports string, run func(env Env, p *FakeProcess)) *testAgent {
	t.Helper()
	return runAgentWith(t, m, name, public, ports, &FakeLauncher{Run: run})
}

// runAgentWith is runAgent with a launcher of the test's own.
func runAgentWith(t *testing.T, m *Manager, name, public, ports string, l *FakeLauncher) *testAgent {
	t.Helper()
	ta := &testAgent{l: l, log: &syncBuf{}, done: make(chan struct{})}
	ta.Agent = &Agent{Log: logging.New(ta.log), Server: m.Addr(), Key: testKey, Name: name, BindHost: "10.1.2.3",
		PublicHost: public, Ports: ports, Build: "test", Launcher: ta.l, Retry: 100 * time.Millisecond,
		Heartbeat: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	ta.cancel = cancel
	go func() {
		if err := ta.Run(ctx); err != nil {
			t.Errorf("agent %s: %v", name, err)
		}
		close(ta.done)
	}()
	t.Cleanup(ta.stop)
	waitSite(t, m, name, true)
	return ta
}

func (ta *testAgent) stop() {
	ta.cancel()
	<-ta.done
}

// waitSite waits until the agent name is registered (or gone).
func waitSite(t *testing.T, m *Manager, name string, present bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; {
		found := false
		for _, s := range m.Sites() {
			found = found || s.Name == name
		}
		if found == present {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent %s registered %v, want %v; sites %+v", name, found, present, m.Sites())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitLog waits until log contains want (n times).
func waitLog(t *testing.T, log *syncBuf, want string) { t.Helper(); waitLogN(t, log, want, 1) }

func waitLogN(t *testing.T, log *syncBuf, want string, n int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); strings.Count(log.String(), want) < n; {
		if time.Now().After(deadline) {
			t.Fatalf("log lacks %q (%d times):\n%s", want, n, log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeFile(path, s string) error { return os.WriteFile(path, []byte(s), 0o600) }

// dropAgent closes a registered agent's connection from the server's side: a link that breaks while
// the agent runs.
func (m *Manager) dropAgent(name, reason string) bool {
	m.mu.Lock()
	var a *agentConn
	for _, s := range m.sites {
		if s.agent != nil && s.name == name {
			a = s.agent
		}
	}
	m.mu.Unlock()
	if a == nil {
		return false
	}
	a.close(reason)
	return true
}

func hasAll(t *testing.T, what, log string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(log, want) {
			t.Errorf("%s log lacks %q:\n%s", what, want, log)
		}
	}
}

// fullMatch plays a whole match: a player, the in-game phase, a result, finishGame.
func fullMatch(env Env, p *FakeProcess) {
	PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
		for _, m := range []Message{AddPlayer{User: 2000000001}, GMSUpdate{Phase: PhaseInGame},
			PlayerFinish{User: 2000000001, Stats: map[string]int64{"kills": 3}},
			FinishGame{WinnerTeam: 1, DurationMs: 60000, Reason: "guardian"}} {
			if err := h.Send(m); err != nil {
				return err
			}
		}
		return nil
	})
}

// untilKilled is a host that answers gameReady and then waits to be killed.
func untilKilled(env Env, p *FakeProcess) {
	PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
		<-p.Killed()
		return errors.New("killed")
	})
}

// A match on an agent: the agent starts the host on its machine with the server's control address,
// the match, the token and DGGMS_BIND on its own bind address; the host dials the server itself and
// plays the whole match; the agent reports the exit. The match names the agent and its GMS host.
func TestAgentMatch(t *testing.T) {
	m, mlog := newMain(t, nil, 0, nil)
	ta := runAgent(t, m, "hostA", "192.168.1.20", "21042-21042", fullMatch)
	if got := m.Sites(); len(got) != 1 || got[0] != (SiteInfo{Name: "hostA", Addr: "127.0.0.1", BindHost: "10.1.2.3",
		GMSHost: "192.168.1.20", Ports: "21042-21042", Free: 1}) {
		t.Errorf("sites %+v", got)
	}
	rec := newRecorder()
	mt, err := m.Start(practice, rec.Events())
	if err != nil {
		t.Fatal(err)
	}
	if mt.Site != "hostA" || mt.GMSHost != "192.168.1.20" || mt.Bind != "10.1.2.3:21042" || mt.Port != 21042 {
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
	if env.Control != m.Addr() || env.Match != mt.ID || env.Bind != "10.1.2.3:21042" || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(env.Token) {
		t.Errorf("host environment %+v (server %s)", env, m.Addr())
	}
	waitLog(t, ta.log, "match 1: pid ")
	if s := m.Sites(); len(s) != 1 || s[0].Free != 1 || s[0].Running != 0 {
		t.Errorf("sites after the match %+v", s)
	}
	pid := mt.Pid()
	hasAll(t, "server", mlog.String(),
		`[host] agent "hostA" from ('127.0.0.1', `, `) registered (test): GMS host 192.168.1.20, bind 10.1.2.3, ports 21042-21042`,
		fmt.Sprintf("[host] match 1: started pid %d on agent hostA for game 1 (practice, SM_Map_v5, 1 players), GMS 10.1.2.3:21042", pid),
		`[host] agent hostA -> {"t":"launchHost","match":1,"token":"...","port":21042}`,
		fmt.Sprintf(`[host] agent hostA <- {"t":"hostStarted","match":1,"pid":%d}`, pid),
		fmt.Sprintf("[host] match 1: hello from pid %d (fake)", pid),
		`[host] agent hostA <- {"t":"hostExited","match":1,"code":0}`,
		": finished (winner team 1): guardian; exit code 0, killed false; port 21042 free")
	hasAll(t, "agent", ta.log.String(),
		fmt.Sprintf(`[agent] registered with %s as "hostA" (seen from 127.0.0.1): GMS host 192.168.1.20, bind 10.1.2.3, ports 21042-21042`, m.Addr()),
		fmt.Sprintf("[agent] match 1: started pid %d, DGGMS_BIND 10.1.2.3:21042, DGGMS_CONTROL %s", pid, m.Addr()),
		fmt.Sprintf("[agent] match 1: pid %d exited with code 0", pid))
	for what, log := range map[string]string{"server": mlog.String(), "agent": ta.log.String()} {
		if strings.Contains(log, env.Token) {
			t.Errorf("the %s log shows the token", what)
		}
	}
}

// Two agents and local hosts: each match goes to the machine with the most free ports, ties to the
// earlier one (local, then the agents in registration order), and each match names its machine.
func TestAgentsAndLocalHosts(t *testing.T) {
	finish := make(chan struct{})
	host := func(env Env, p *FakeProcess) {
		PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
			<-finish
			return h.Send(FinishGame{WinnerTeam: 2, Reason: env.Bind})
		})
	}
	m, _ := newMain(t, &FakeLauncher{Run: host}, 1, func(c *Config) { c.HeartbeatTimeout = 10 * time.Second })
	runAgent(t, m, "hostA", "192.168.1.20", "21042-21043", host)
	runAgent(t, m, "hostB", "192.168.1.21", "22042-22042", host)
	var recs []*recorder
	var got []string
	for range 4 {
		rec := newRecorder()
		mt, err := m.Start(practice, rec.Events())
		if err != nil {
			t.Fatal(err)
		}
		<-rec.ready
		recs = append(recs, rec)
		got = append(got, fmt.Sprintf("%s %s %q", mt.Site, mt.Bind, mt.GMSHost))
	}
	want := []string{`hostA 10.1.2.3:21042 "192.168.1.20"`, `local 127.0.0.1:20042 ""`, `hostA 10.1.2.3:21043 "192.168.1.20"`,
		`hostB 10.1.2.3:22042 "192.168.1.21"`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("matches\n got %v\nwant %v", got, want)
	}
	if _, err := m.Start(practice, Events{}); !errors.Is(err, ErrNoPort) {
		t.Errorf("fifth match: %v", err)
	}
	close(finish)
	for _, rec := range recs {
		if o := rec.wait(t); !o.Finished || o.ExitCode != 0 {
			t.Errorf("outcome %+v", o)
		}
	}
	for _, s := range m.Sites() {
		if s.Running != 0 || (s.Name == "hostA") != (s.Free == 2) {
			t.Errorf("site %+v", s)
		}
	}
}

// A server that takes no agents has no machine to start a host on.
func TestNoSite(t *testing.T) {
	m, _ := newMain(t, nil, 0, nil)
	if _, err := m.Start(practice, Events{}); !errors.Is(err, ErrNoSite) {
		t.Errorf("start without hosts: %v", err)
	}
}

// --- an agent played by hand: the handshake's refusals and agents that misbehave ---

type rawAgent struct {
	t *testing.T
	c net.Conn
	r *LineReader
}

func dialRaw(t *testing.T, addr string) *rawAgent {
	t.Helper()
	c, err := net.Dial("tcp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &rawAgent{t: t, c: c, r: NewLineReader(c)}
}

func (ra *rawAgent) send(m Message) {
	ra.t.Helper()
	if _, err := writeLine(ra.c, m); err != nil {
		ra.t.Fatal(err)
	}
}

func (ra *rawAgent) recv(d time.Duration) (Message, error) {
	ra.c.SetReadDeadline(time.Now().Add(d))
	line, err := ra.r.Next()
	if err != nil {
		return nil, err
	}
	return Decode(line)
}

// closed checks the server closed the connection (EOF, not a timeout).
func (ra *rawAgent) closed() {
	ra.t.Helper()
	if m, err := ra.recv(3 * time.Second); err == nil {
		ra.t.Errorf("got %#v, want the connection closed", m)
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		ra.t.Error("the connection is still open")
	}
}

var offer = AgentAuth{Bind: "10.1.2.3", Public: "192.168.1.20", Ports: "21042-21042"}

// register runs the handshake with key; it returns the agentAuth it sent (after the challenge).
func (ra *rawAgent) register(key []byte, name, nonce string, o AgentAuth) AgentAuth {
	ra.t.Helper()
	ra.send(AgentHello{Name: name, Nonce: nonce, Build: "raw"})
	m, err := ra.recv(3 * time.Second)
	ch, ok := m.(AgentChallenge)
	if err != nil || !ok {
		ra.t.Fatalf("challenge: %v %v", m, err)
	}
	if !proofOK(ch.Proof, agentProof(testKey, proofServer, nonce, ch.Nonce)) {
		ra.t.Error("the server's proof doesn't check out")
	}
	o.Proof = agentProof(key, proofAgent, ch.Nonce, nonce, name, o.Bind, o.Public, o.Ports)
	ra.send(o)
	return o
}

const nonceA = "00112233445566778899aabbccddeeff"

// The handshake: a registration with the key works; a wrong key, a replayed agentAuth, a server
// without a key, a bad name, nonce or offer are refused (the connection closed, a log line). An
// agent with the wrong key refuses the server first.
func TestAgentAuth(t *testing.T) {
	m, log := newMain(t, nil, 0, func(c *Config) { c.AgentTimeout = 10 * time.Second })
	good := dialRaw(t, m.Addr())
	auth := good.register(testKey, "raw1", nonceA, offer)
	if msg, err := good.recv(3 * time.Second); err != nil || msg != (AgentReady{Addr: "127.0.0.1"}) {
		t.Fatalf("ready: %#v %v", msg, err)
	}
	waitSite(t, m, "raw1", true)

	// the same agentAuth again, on a new connection with the same nonce: the server's nonce is new
	replay := dialRaw(t, m.Addr())
	replay.send(AgentHello{Name: "raw1", Nonce: nonceA, Build: "raw"})
	if _, err := replay.recv(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	replay.send(auth)
	replay.closed()

	wrong := dialRaw(t, m.Addr())
	wrong.register([]byte("another key, 32 characters long."), "raw2", nonceA, offer)
	wrong.closed()

	for _, bad := range []struct {
		hello AgentHello
		o     AgentAuth
	}{
		{AgentHello{Name: "local", Nonce: nonceA}, offer},
		{AgentHello{Name: "a b", Nonce: nonceA}, offer},
		{AgentHello{Name: "raw3", Nonce: "short"}, offer},
		{AgentHello{Name: "raw3", Nonce: nonceA}, AgentAuth{Bind: "0.0.0.0", Public: "192.168.1.20", Ports: "1-2"}},
		{AgentHello{Name: "raw3", Nonce: nonceA}, AgentAuth{Bind: "10.1.2.3", Public: "224.0.0.1", Ports: "1-2"}},
		{AgentHello{Name: "raw3", Nonce: nonceA}, AgentAuth{Bind: "10.1.2.3", Public: "192.168.1.20", Ports: "x"}},
	} {
		ra := dialRaw(t, m.Addr())
		if bad.hello.Name == "raw3" && bad.hello.Nonce == nonceA {
			ra.register(testKey, "raw3", nonceA, bad.o)
		} else {
			ra.send(bad.hello)
		}
		ra.closed()
	}
	if s := m.Sites(); len(s) != 1 || s[0].Name != "raw1" {
		t.Errorf("sites %+v", s)
	}
	hasAll(t, "server", log.String(),
		`agent "raw1" from ('127.0.0.1', `, `) registered (raw): GMS host 192.168.1.20, bind 10.1.2.3, ports 21042-21042`,
		`) rejected: wrong proof (a different key, or a replayed agentAuth)`,
		`agent "local" from `, `) rejected: bad name (1-64 of A-Z a-z 0-9 . _ -, not "local")`,
		`agent "a b" from `, `agent "raw3" from `, `) rejected: bad nonce`,
		`) rejected: bind "0.0.0.0" is not one IPv4 address`,
		`) rejected: public host "224.0.0.1" is not an IPv4 address clients can connect to`,
		`) rejected: gmshost: bad port range "x"`)
	if n := strings.Count(log.String(), "rejected: wrong proof"); n != 2 {
		t.Errorf("%d wrong proofs, want 2 (the replay and the wrong key):\n%s", n, log.String())
	}

	// an agent with another key: it checks the server's proof first and doesn't register
	other := &syncBuf{}
	ag := &Agent{Log: logging.New(other), Server: m.Addr(), Key: []byte("another key, 32 characters long."), Name: "hostX",
		BindHost: "10.1.2.3", Ports: "21042-21042", Launcher: &FakeLauncher{}, Retry: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ag.Run(ctx); close(done) }()
	waitLog(t, other, "the server failed the key check (a different --agent-key?); not registering")
	cancel()
	<-done
	waitLog(t, log, `agent "hostX" from `)

	// a server without a key takes no agent
	m2, log2 := newMain(t, nil, 0, func(c *Config) { c.AgentKey = nil })
	ra := dialRaw(t, m2.Addr())
	ra.send(AgentHello{Name: "raw1", Nonce: nonceA})
	ra.closed()
	hasAll(t, "server", log2.String(), `) rejected: this server takes no agents (no --agent-key)`)
}

// fakeFirst makes the first accepted connection look as if it came from ip (a host machine on the
// LAN, without binding anything but loopback).
type fakeFirst struct {
	net.Listener
	ip net.IP
	n  atomic.Int32
}

type fakeAddrConn struct {
	net.Conn
	addr net.Addr
}

func (c fakeAddrConn) RemoteAddr() net.Addr { return c.addr }

func (l *fakeFirst) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil && l.n.Add(1) == 1 {
		return fakeAddrConn{c, &net.TCPAddr{IP: l.ip, Port: 40000}}, nil
	}
	return c, err
}

// newMainFaked is newMain whose first control connection appears to come from 10.9.9.9.
func newMainFaked(t *testing.T, local *FakeLauncher) (*Manager, *syncBuf) {
	t.Helper()
	log := &syncBuf{}
	cfg := Config{Log: logging.New(log), AgentKey: testKey, HelloTimeout: time.Second, ReadyTimeout: time.Second,
		HeartbeatTimeout: time.Second, ExitTimeout: 300 * time.Millisecond, AgentTimeout: 2 * time.Second, LaunchTimeout: time.Second}
	if local != nil {
		cfg.Launcher, cfg.Pool = local, NewPool(20042, 20042)
	}
	m := NewManager(cfg)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m.Serve(&fakeFirst{Listener: ln, ip: net.IPv4(10, 9, 9, 9)})
	t.Cleanup(m.Close)
	return m, log
}

// Host tokens: one hello per token (a replay is refused while the match runs and after it ended),
// and the hello must come from the machine the host was started on.
func TestHostTokens(t *testing.T) {
	t.Run("replay", func(t *testing.T) {
		m, log := newMain(t, nil, 0, func(c *Config) { c.HeartbeatTimeout = 10 * time.Second })
		finish := make(chan struct{})
		ta := runAgent(t, m, "hostA", "192.168.1.20", "21042-21042", func(env Env, p *FakeProcess) {
			PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
				<-finish
				return h.Send(FinishGame{Reason: "done"})
			})
		})
		rec := newRecorder()
		if _, err := m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		<-rec.ready
		env := ta.l.Envs()[0]
		replay := func() {
			h, err := DialHost(env, 1, "replay")
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			if msg, err := h.Recv(3 * time.Second); err == nil {
				t.Errorf("replayed hello got %#v", msg)
			}
		}
		replay()
		close(finish)
		if o := rec.wait(t); !o.Finished {
			t.Errorf("outcome %+v", o)
		}
		replay()
		hasAll(t, "server", log.String(), "rejected: match 1's token was used already (a replay, or a second host)",
			"rejected: unknown match 1 or wrong token")
	})
	t.Run("agent's host from elsewhere", func(t *testing.T) {
		m, log := newMainFaked(t, nil) // the agent's connection comes "from 10.9.9.9", its host from 127.0.0.1
		runAgent(t, m, "hostA", "192.168.1.20", "21042-21042", fullMatch)
		rec := newRecorder()
		if _, err := m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		if o := rec.wait(t); o.Ready || o.Finished {
			t.Errorf("outcome %+v", o)
		}
		hasAll(t, "server", log.String(), `agent "hostA" from ('10.9.9.9', 40000) registered`,
			"rejected: hello for match 1 from 127.0.0.1, but its host runs on hostA (10.9.9.9)")
	})
	t.Run("local host from elsewhere", func(t *testing.T) {
		m, log := newMainFaked(t, &FakeLauncher{Run: fullMatch}) // the host's connection comes "from 10.9.9.9"
		rec := newRecorder()
		if _, err := m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		if o := rec.wait(t); o.Ready || o.Finished {
			t.Errorf("outcome %+v", o)
		}
		hasAll(t, "server", log.String(), "rejected: hello for match 1 from 10.9.9.9, but its host runs on local (loopback or 127.0.0.1:")
	})
}

// An agent lost while its match runs: the match ends without a result ("agent hostA lost") and the
// agent kills its host. When the agent stops, its machine is gone; when only the link breaks, the
// agent registers again and takes the next match.
func TestAgentLost(t *testing.T) {
	t.Run("agent stops", func(t *testing.T) {
		m, log := newMain(t, nil, 0, func(c *Config) { c.HeartbeatTimeout = 10 * time.Second })
		ta := runAgent(t, m, "hostA", "192.168.1.20", "21042-21042", untilKilled)
		rec := newRecorder()
		if _, err := m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		<-rec.ready
		ta.stop()
		o := rec.wait(t)
		if !o.Ready || o.Finished || o.Reason != "agent hostA lost" || o.ExitCode != -1 {
			t.Errorf("outcome %+v", o)
		}
		waitSite(t, m, "hostA", false)
		if _, err := m.Start(practice, Events{}); !errors.Is(err, ErrNoSite) {
			t.Errorf("start without the agent: %v", err)
		}
		hasAll(t, "agent", ta.log.String(), "[agent] the link to the main server ended: killing 1 hosts", "exited with code 1", "[agent] stopped")
		hasAll(t, "server", log.String(), `[host] agent "hostA" (127.0.0.1) gone: connection closed by the agent; 1 running hosts end with it`,
			"[host] match 1: ended after ", ": aborted: agent hostA lost; exit code -1, killed false; port 21042 free")
	})
	t.Run("link breaks", func(t *testing.T) {
		m, log := newMain(t, nil, 0, func(c *Config) { c.HeartbeatTimeout = 10 * time.Second })
		first := make(chan struct{}, 1)
		first <- struct{}{}
		ta := runAgent(t, m, "hostA", "192.168.1.20", "21042-21042", func(env Env, p *FakeProcess) {
			select {
			case <-first:
				untilKilled(env, p)
			default:
				fullMatch(env, p)
			}
		})
		rec := newRecorder()
		if _, err := m.Start(practice, rec.Events()); err != nil {
			t.Fatal(err)
		}
		<-rec.ready
		if !m.dropAgent("hostA", "test: link cut") {
			t.Fatal("no agent hostA")
		}
		if o := rec.wait(t); o.Finished || o.Reason != "agent hostA lost" {
			t.Errorf("outcome %+v", o)
		}
		waitLogN(t, log, "registered (test)", 2)
		waitSite(t, m, "hostA", true)
		rec2 := newRecorder()
		if _, err := m.Start(practice, rec2.Events()); err != nil {
			t.Fatal(err)
		}
		if o := rec2.wait(t); !o.Finished || o.ExitCode != 0 {
			t.Errorf("second match %+v", o)
		}
		hasAll(t, "agent", ta.log.String(), "[agent] the link to the main server ended: killing 1 hosts", "; next attempt in 100ms")
		hasAll(t, "server", log.String(), `gone: test: link cut; 1 running hosts end with it`)
	})
}

// A host that crashes on an agent: the agent reports the exit code; the match is aborted.
func TestAgentHostCrash(t *testing.T) {
	m, _ := newMain(t, nil, 0, nil)
	runAgent(t, m, "hostA", "192.168.1.20", "21042-21042", func(env Env, p *FakeProcess) {
		PlayHost(env, p, func(h *HostConn, cg CreateGame) error {
			h.Send(AddPlayer{User: 2000000001})
			h.Close()
			p.Exit(3)
			return errors.New("crashed")
		})
	})
	rec := newRecorder()
	if _, err := m.Start(practice, rec.Events()); err != nil {
		t.Fatal(err)
	}
	o := rec.wait(t)
	if !o.Ready || o.Finished || o.Killed || o.ExitCode != 3 || o.Reason != "exited with code 3 without finishGame" {
		t.Errorf("outcome %+v", o)
	}
	if got := rec.list(); !reflect.DeepEqual(got, []string{"ready 21042", "add 2000000001/0", "end"}) {
		t.Errorf("events %v", got)
	}
}

// Launch failures: the agent can't start the host (hostFailed), or doesn't answer in time (the
// server gives up and sends killHost, and again for a late hostStarted). The port is free again.
func TestAgentLaunchFails(t *testing.T) {
	m, log := newMain(t, nil, 0, nil)
	ta := runAgentWith(t, m, "hostA", "192.168.1.20", "21042-21042", &FakeLauncher{Err: errors.New("no Dawngate.exe")})
	if _, err := m.Start(practice, Events{}); err == nil || err.Error() != "agent hostA: no Dawngate.exe" {
		t.Errorf("start: %v", err)
	}
	if s := m.Sites(); s[0].Free != 1 {
		t.Errorf("sites %+v", s)
	}
	hasAll(t, "server", log.String(), `agent hostA <- {"t":"hostFailed","match":1,"error":"no Dawngate.exe"}`,
		"match 1 (game 1): launch failed: agent hostA: no Dawngate.exe")
	hasAll(t, "agent", ta.log.String(), "[agent] match 1: not started: no Dawngate.exe")

	m2, log2 := newMain(t, nil, 0, nil)
	ra := dialRaw(t, m2.Addr())
	ra.register(testKey, "slow", nonceA, offer)
	ra.recv(3 * time.Second) // agentReady
	waitSite(t, m2, "slow", true)
	if _, err := m2.Start(practice, Events{}); err == nil || err.Error() != "agent slow: no answer to launchHost within 1s" {
		t.Errorf("start: %v", err)
	}
	for _, want := range []Message{LaunchHost{Match: 1, Port: 21042}, KillHost{Match: 1}} {
		msg, err := ra.recv(3 * time.Second)
		if l, ok := msg.(LaunchHost); ok {
			l.Token = ""
			msg = l
		}
		if err != nil || msg != want {
			t.Errorf("agent got %#v %v, want %#v", msg, err, want)
		}
	}
	ra.send(HostStarted{Match: 1, PID: 4242}) // too late
	if msg, err := ra.recv(3 * time.Second); err != nil || msg != (KillHost{Match: 1}) {
		t.Errorf("after a late hostStarted: %#v %v", msg, err)
	}
	hasAll(t, "server", log2.String(), "(hostStarted for match 1, which no launch waits for: killing it)")
	if s := m2.Sites(); s[0].Free != 1 {
		t.Errorf("sites %+v", s)
	}
}

// A registered agent that goes silent is dropped; a second registration under the same name
// replaces the first.
func TestAgentSilentOrReplaced(t *testing.T) {
	m, log := newMain(t, nil, 0, func(c *Config) { c.AgentTimeout = 300 * time.Millisecond })
	ra := dialRaw(t, m.Addr())
	ra.register(testKey, "quiet", nonceA, offer)
	waitSite(t, m, "quiet", true)
	waitSite(t, m, "quiet", false)
	hasAll(t, "server", log.String(), `agent "quiet" (127.0.0.1) gone: silent for 300ms; 0 running hosts end with it`)

	m2, log2 := newMain(t, nil, 0, nil)
	one := dialRaw(t, m2.Addr())
	one.register(testKey, "twin", nonceA, offer)
	one.recv(3 * time.Second)
	two := dialRaw(t, m2.Addr())
	two.register(testKey, "twin", "ffeeddccbbaa99887766554433221100", AgentAuth{Bind: "10.1.2.4", Public: "192.168.1.22", Ports: "23042-23042"})
	two.recv(3 * time.Second)
	one.closed()
	if s := m2.Sites(); len(s) != 1 || s[0].BindHost != "10.1.2.4" {
		t.Errorf("sites %+v", s)
	}
	hasAll(t, "server", log2.String(), `agent "twin" (127.0.0.1) gone: replaced by a new registration from ('127.0.0.1', `)
}

// Close kills the hosts on agents too (killHost; the agent reports the exit) and closes the links.
func TestCloseWithAgent(t *testing.T) {
	m, log := newMain(t, nil, 0, func(c *Config) { c.HeartbeatTimeout = 10 * time.Second })
	ta := runAgent(t, m, "hostA", "192.168.1.20", "21042-21042", untilKilled)
	rec := newRecorder()
	if _, err := m.Start(practice, rec.Events()); err != nil {
		t.Fatal(err)
	}
	<-rec.ready
	m.Close()
	if o := rec.wait(t); !o.Killed || o.Reason != "server shutting down" || o.ExitCode != 1 {
		t.Errorf("outcome %+v", o)
	}
	hasAll(t, "server", log.String(), `agent hostA -> {"t":"killHost","match":1}`, `gone: server shutting down; 0 running hosts end with it`)
	waitLog(t, ta.log, "[agent] match 1: killing pid ")
}

func TestAgentCheck(t *testing.T) {
	ok := func() *Agent {
		return &Agent{Server: "192.168.1.10:10040", Key: testKey, Name: "hostA", BindHost: "192.168.1.20", Ports: "10042-10051",
			Launcher: &FakeLauncher{}}
	}
	if err := ok().Check(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		change func(a *Agent)
		want   string
	}{
		{func(a *Agent) { a.Key = []byte("short") }, "agent key shorter than 16 characters"},
		{func(a *Agent) { a.Name = "local" }, `bad agent name "local"`},
		{func(a *Agent) { a.Name = "" }, `bad agent name ""`},
		{func(a *Agent) { a.Server = "192.168.1.10" }, "bad server control address"},
		{func(a *Agent) { a.Server = "192.168.1.10:0" }, "bad server control address"},
		{func(a *Agent) { a.BindHost = "0.0.0.0" }, `bind "0.0.0.0" is not one IPv4 address`},
		{func(a *Agent) { a.Ports = "9-1" }, "bad port range"},
		{func(a *Agent) { a.BindHost, a.PublicHost = "127.0.0.1", "" }, "the GMS host announced to clients is loopback 127.0.0.1"},
		{func(a *Agent) { a.Launcher = nil }, "without a launcher"},
	} {
		a := ok()
		c.change(a)
		if err := a.Check(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", a.Name, err, c.want)
		}
	}
	// all on one machine (the tests' setup): loopback everywhere is fine
	local := ok()
	local.Server, local.BindHost = "127.0.0.1:10040", "127.0.0.1"
	if err := local.Check(); err != nil {
		t.Error(err)
	}
}

func TestAgentKey(t *testing.T) {
	k := NewAgentKey()
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(k) || k == NewAgentKey() {
		t.Errorf("key %q", k)
	}
	dir := t.TempDir()
	path := dir + "/key.txt"
	for _, c := range []struct {
		content, want string
	}{
		{" " + k + "\r\n", k},
		{"0123456789abcdef", "0123456789abcdef"},
		{"0123456789abcde\n", ""},
	} {
		if err := writeFile(path, c.content); err != nil {
			t.Fatal(err)
		}
		got, err := ReadAgentKey(path)
		if string(got) != c.want || (err != nil) != (c.want == "") {
			t.Errorf("%q: %q %v", c.content, got, err)
		}
	}
	if _, err := ReadAgentKey(dir + "/missing"); err == nil {
		t.Error("no error for a missing file")
	}
	// the proofs: each role and every part count
	p := agentProof(testKey, proofAgent, "a", "b")
	for _, other := range []string{agentProof(testKey, proofServer, "a", "b"), agentProof(testKey, proofAgent, "ab"),
		agentProof(testKey, proofAgent, "a", "b", ""), agentProof([]byte("x"), proofAgent, "a", "b")} {
		if proofOK(other, p) {
			t.Errorf("proof %s matches %s", other, p)
		}
	}
}
