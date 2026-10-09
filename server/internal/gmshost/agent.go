package gmshost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"dawngate/server/internal/logging"
)

// Agent is a host machine's side of H2 (dgserver --agent; remote.go has the protocol). It keeps one
// connection to the main server's control port, registers with the shared key and starts a host
// (Launcher, usually an ExecLauncher for the machine's host copy) for every launchHost, with
// DGGMS_CONTROL = the address it dialed (numeric, as the host's C++ side wants it) and DGGMS_BIND =
// BindHost:<the port the server picked from Ports>. The hosts then talk to the main server directly.
//
// When the connection ends the agent kills every host it started (the server ends their matches as
// aborted at the same time) and dials again after Retry. Run returns when its context ends, after
// killing its hosts.
type Agent struct {
	Log        *logging.Log
	Server     string // the main server's control address, "host:port"
	Key        []byte // the shared agent key (ReadAgentKey)
	Name       string // this machine's name in the server's log and Match.Site
	BindHost   string // the address the hosts' GMS ports bind: this machine's (LAN) address
	PublicHost string // the address clients are told (HNET); "" = BindHost
	Ports      string // the GMS port range of this machine, "10042-10051"
	Build      string // free-form, logged by the server
	Launcher   Launcher
	Retry      time.Duration // between connection attempts (0 = 5 s)
	Heartbeat  time.Duration // agentStatus interval (0 = 5 s)

	mu      sync.Mutex
	running map[int64]*agentHost
}

type agentHost struct {
	port   int
	proc   Process // nil while launching
	exited chan struct{}
}

const agentWho = "agent"

// Check validates the configuration (Run does it first).
func (a *Agent) Check() error {
	if len(a.Key) < MinAgentKey {
		return fmt.Errorf("gmshost: agent key shorter than %d characters", MinAgentKey)
	}
	if !agentNameRE.MatchString(a.Name) || a.Name == LocalSite {
		return fmt.Errorf("gmshost: bad agent name %q (1-64 of A-Z a-z 0-9 . _ -, not %q)", a.Name, LocalSite)
	}
	host, port, err := net.SplitHostPort(a.Server)
	if err != nil {
		return fmt.Errorf("gmshost: bad server control address %q: %v", a.Server, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("gmshost: bad server control address %q", a.Server)
	}
	if _, _, err := checkAgentOffer(AgentAuth{Bind: a.BindHost, Public: a.public(), Ports: a.Ports}); err != nil {
		return fmt.Errorf("gmshost: agent: %v", err)
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() && net.ParseIP(a.public()).IsLoopback() {
		return fmt.Errorf("gmshost: agent: the main server %s is on another machine, but the GMS host announced to clients is loopback %s (set --public-host)", a.Server, a.public())
	}
	if a.Launcher == nil {
		return errors.New("gmshost: agent without a launcher")
	}
	return nil
}

func (a *Agent) public() string {
	if a.PublicHost != "" {
		return a.PublicHost
	}
	return a.BindHost
}

func dur(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Run serves the main server until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.Check(); err != nil {
		return err
	}
	if a.Log == nil {
		a.Log = logging.New(nopWriter{})
	}
	a.mu.Lock()
	a.running = map[int64]*agentHost{}
	a.mu.Unlock()
	retry := dur(a.Retry, 5*time.Second)
	for {
		err := a.serve(ctx)
		a.killAll("the link to the main server ended")
		if ctx.Err() != nil {
			a.Log.Log(agentWho, "stopped")
			return nil
		}
		a.Log.Logf(agentWho, "link to %s: %v; next attempt in %v", a.Server, err, retry)
		select {
		case <-ctx.Done():
			a.Log.Log(agentWho, "stopped")
			return nil
		case <-time.After(retry):
		}
	}
}

// agentLink is one connection to the main server.
type agentLink struct {
	c  net.Conn
	mu sync.Mutex
}

func (l *agentLink) send(msg Message) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := writeLine(l.c, msg)
	return err
}

// serve runs one connection: dial, register, then launchHost and killHost until it ends.
func (a *Agent) serve(ctx context.Context) error {
	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(ctx, "tcp4", a.Server)
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	l := &agentLink{c: c}
	r := NewLineReader(c)
	public := a.public()

	nonce := newToken()
	if err := l.send(AgentHello{Name: a.Name, Nonce: nonce, Build: a.Build}); err != nil {
		return err
	}
	read := func(what string) (Message, error) {
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		line, err := r.Next()
		if err != nil {
			return nil, fmt.Errorf("no %s: %v (a different --agent-key, or the server takes no agents: see its log)", what, err)
		}
		return Decode(line)
	}
	msg, err := read(TAgentChallenge)
	if err != nil {
		return err
	}
	ch, ok := msg.(AgentChallenge)
	if !ok || !nonceRE.MatchString(ch.Nonce) {
		return fmt.Errorf("expected agentChallenge, got %v", msg)
	}
	if !proofOK(ch.Proof, agentProof(a.Key, proofServer, nonce, ch.Nonce)) {
		return errors.New("the server failed the key check (a different --agent-key?); not registering")
	}
	if err := l.send(AgentAuth{Proof: agentProof(a.Key, proofAgent, ch.Nonce, nonce, a.Name, a.BindHost, public, a.Ports),
		Bind: a.BindHost, Public: public, Ports: a.Ports}); err != nil {
		return err
	}
	if msg, err = read(TAgentReady); err != nil {
		return err
	}
	ready, ok := msg.(AgentReady)
	if !ok {
		return fmt.Errorf("expected agentReady, got %v", msg)
	}
	c.SetReadDeadline(time.Time{})
	control := c.RemoteAddr().String() // numeric: the hosts' DGGMS_CONTROL
	a.Log.Logf(agentWho, "registered with %s as %q (seen from %s): GMS host %s, bind %s, ports %s", control, a.Name,
		ready.Addr, public, a.BindHost, a.Ports)

	done := make(chan struct{})
	defer close(done)
	go func() { // heartbeat
		t := time.NewTicker(dur(a.Heartbeat, 5*time.Second))
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if l.send(AgentStatus{Hosts: a.hosts()}) != nil {
					c.Close()
					return
				}
			}
		}
	}()
	for {
		line, err := r.Next()
		if err != nil {
			return fmt.Errorf("connection ended: %v", err)
		}
		msg, err := Decode(line)
		if err != nil {
			a.Log.Logf(agentWho, "(line ignored: %v)", err)
			continue
		}
		a.Log.Logf(agentWho, "<- %s", logged(msg, line))
		switch v := msg.(type) {
		case LaunchHost:
			a.launch(l, control, v)
		case KillHost:
			a.kill(v.Match)
		default:
			a.Log.Logf(agentWho, "(%s from the server, ignored)", msg.Type())
		}
	}
}

// hosts lists the matches whose hosts run here, in order.
func (a *Agent) hosts() []int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]int64, 0, len(a.running))
	for id := range a.running {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// launch starts a host for launchHost and answers hostStarted (then hostExited once it ends) or
// hostFailed. The port must be in this agent's range and not in use by another of its hosts.
func (a *Agent) launch(l *agentLink, control string, v LaunchHost) {
	fail := func(why string) {
		a.Log.Logf(agentWho, "match %d: not started: %s", v.Match, why)
		l.send(HostFailed{Match: v.Match, Error: why})
	}
	first, last, _ := ParsePorts(a.Ports)
	if v.Port < first || v.Port > last {
		fail(fmt.Sprintf("port %d is outside this agent's ports %s", v.Port, a.Ports))
		return
	}
	h := &agentHost{port: v.Port, exited: make(chan struct{})}
	a.mu.Lock()
	for id, o := range a.running {
		if id == v.Match || o.port == v.Port {
			a.mu.Unlock()
			fail(fmt.Sprintf("match %d or port %d is already in use here", v.Match, v.Port))
			return
		}
	}
	a.running[v.Match] = h
	a.mu.Unlock()
	env := Env{Control: control, Match: v.Match, Token: v.Token, Bind: net.JoinHostPort(a.BindHost, strconv.Itoa(v.Port))}
	proc, err := a.Launcher.Launch(env)
	if err != nil {
		a.mu.Lock()
		delete(a.running, v.Match)
		a.mu.Unlock()
		fail(err.Error())
		return
	}
	a.mu.Lock()
	h.proc = proc
	a.mu.Unlock()
	a.Log.Logf(agentWho, "match %d: started pid %d, DGGMS_BIND %s, DGGMS_CONTROL %s", v.Match, proc.Pid(), env.Bind, control)
	l.send(HostStarted{Match: v.Match, PID: proc.Pid()})
	go func() {
		code, err := proc.Wait()
		a.mu.Lock()
		delete(a.running, v.Match)
		a.mu.Unlock()
		close(h.exited)
		if err != nil {
			a.Log.Logf(agentWho, "match %d: (wait: %v)", v.Match, err)
		}
		a.Log.Logf(agentWho, "match %d: pid %d exited with code %d", v.Match, proc.Pid(), code)
		l.send(HostExited{Match: v.Match, Code: code}) // fails quietly once the link is gone
	}()
}

// kill kills a match's host (killHost).
func (a *Agent) kill(match int64) {
	a.mu.Lock()
	h := a.running[match]
	var proc Process
	if h != nil {
		proc = h.proc
	}
	a.mu.Unlock()
	if proc == nil {
		a.Log.Logf(agentWho, "(killHost for match %d, which has no host here)", match)
		return
	}
	a.Log.Logf(agentWho, "match %d: killing pid %d (the server asked)", match, proc.Pid())
	if err := proc.Kill(); err != nil {
		a.Log.Logf(agentWho, "match %d: (kill: %v)", match, err)
	}
}

// killAll kills every host and waits (up to the reap timeout) until they have exited.
func (a *Agent) killAll(why string) {
	a.mu.Lock()
	var hs []*agentHost
	for _, h := range a.running {
		if h.proc != nil {
			hs = append(hs, h)
		}
	}
	a.mu.Unlock()
	if len(hs) == 0 {
		return
	}
	a.Log.Logf(agentWho, "%s: killing %d hosts", why, len(hs))
	deadline := time.After(reapTimeout)
	for _, h := range hs {
		if err := h.proc.Kill(); err != nil {
			a.Log.Logf(agentWho, "(kill pid %d: %v)", h.proc.Pid(), err)
		}
	}
	for _, h := range hs {
		select {
		case <-h.exited:
		case <-deadline:
			a.Log.Logf(agentWho, "(pid %d did not exit in time)", h.proc.Pid())
			return
		}
	}
}
