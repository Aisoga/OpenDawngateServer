package gmshost

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Remote host machines (roadmap H2, ADR-2 decision 8), the main server's side. An agent (dgserver
// --agent on another machine; agent.go) dials the control port and registers:
//
//	agent  -> server  agentHello {name, nonce A, build}
//	server -> agent   agentChallenge {nonce S, proof = HMAC(key, "server", A, S)}
//	agent  -> server  agentAuth {proof = HMAC(key, "agent", S, A, name, bind, public, ports), bind, public, ports}
//	server -> agent   agentReady {addr}
//
// Both sides prove they know the shared agent key (dgserver --agent-key) without sending it, the
// fresh nonces make an old proof useless (a replayed agentAuth is refused), and the agent's proof
// covers what it offers. From then on the agent is a site: Start may pick it, and then sends
//
//	server -> agent   launchHost {match, token, port}      -> hostStarted {match, pid} | hostFailed {match, error}
//	server -> agent   killHost {match}
//	agent  -> server  hostExited {match, code}, agentStatus {hosts} (heartbeat, every 5 s)
//
// The host the agent starts dials this control port itself with its token, like a local one; its
// hello must come from the agent's address (Manager.helloFrom). The agent connection is the liveness
// of everything the agent started: when it ends (closed, silent for AgentTimeout, replaced by a new
// registration under the same name), the agent's running matches end as aborted ("agent <name>
// lost"), and the agent kills its hosts (agent.go). Messages after agentReady are not authenticated:
// the channel is for a trusted LAN (docs/HOST_CONTROL.md *Security model*).

// MinAgentKey is the shortest agent key accepted (bytes); NewAgentKey makes 64 hex digits.
const MinAgentKey = 16

// NewAgentKey returns a new random agent key: 64 hex digits (dgserver --make-agent-key).
func NewAgentKey() string { return newToken() + newToken() }

// ReadAgentKey reads an agent key file: its content without surrounding white space, at least
// MinAgentKey bytes.
func ReadAgentKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("gmshost: agent key: %w", err)
	}
	key := []byte(strings.TrimSpace(string(b)))
	if len(key) < MinAgentKey {
		return nil, fmt.Errorf("gmshost: agent key in %s is shorter than %d characters (make one with --make-agent-key)", path, MinAgentKey)
	}
	return key, nil
}

// Proof roles: which side computes it, so one side's proof never passes as the other's.
const (
	proofServer = "dgserver agent: server"
	proofAgent  = "dgserver agent: agent"
)

// agentProof is HMAC-SHA256(key, role 0 part 0 part ...) in hex (0 = a NUL byte; the parts are
// nonces, a name, addresses and a port range, none of which can hold a NUL).
func agentProof(key []byte, role string, parts ...string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(role))
	for _, p := range parts {
		mac.Write([]byte{0})
		mac.Write([]byte(p))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func proofOK(got, want string) bool { return hmac.Equal([]byte(got), []byte(want)) }

var (
	agentNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	nonceRE     = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// checkAgentOffer checks what an agent offers: bind one IPv4 address of its machine (not the
// wildcard: the host can't bind it), public an IPv4 address clients can connect to, ports a range.
func checkAgentOffer(a AgentAuth) (first, last int, err error) {
	if ip := net.ParseIP(a.Bind).To4(); ip == nil || ip.IsUnspecified() {
		return 0, 0, fmt.Errorf("bind %q is not one IPv4 address", a.Bind)
	}
	if ip := net.ParseIP(a.Public).To4(); ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) {
		return 0, 0, fmt.Errorf("public host %q is not an IPv4 address clients can connect to", a.Public)
	}
	return ParsePorts(a.Ports)
}

// logged is a line as the logs show it: a launchHost's token is left out (tokens stay out of logs).
func logged(msg Message, line []byte) string {
	if v, ok := msg.(LaunchHost); ok {
		v.Token = "..."
		if b, err := Encode(v); err == nil {
			line = b
		}
	}
	return strings.TrimRight(string(line), "\n")
}

// writeLine writes msg as one line on c (the caller serializes the writes) and returns the line.
func writeLine(c net.Conn, msg Message) ([]byte, error) {
	line, err := Encode(msg)
	if err != nil {
		return nil, err
	}
	c.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err = c.Write(line)
	return line, err
}

// agentHello registers an agent (the handshake above) and serves its connection until it ends.
func (m *Manager) agentHello(c net.Conn, r *LineReader, h AgentHello, peer string) {
	reject := func(why string) {
		m.log.Logf(who, "agent %q from %s rejected: %s", h.Name, peer, why)
		c.Close()
	}
	key := m.cfg.AgentKey
	switch {
	case len(key) == 0:
		reject("this server takes no agents (no --agent-key)")
		return
	case !agentNameRE.MatchString(h.Name) || h.Name == LocalSite:
		reject("bad name (1-64 of A-Z a-z 0-9 . _ -, not \"local\")")
		return
	case !nonceRE.MatchString(h.Nonce):
		reject("bad nonce")
		return
	}
	nonce := newToken()
	if _, err := writeLine(c, AgentChallenge{Nonce: nonce, Proof: agentProof(key, proofServer, h.Nonce, nonce)}); err != nil {
		reject("challenge not sent: " + err.Error())
		return
	}
	c.SetReadDeadline(time.Now().Add(m.cfg.HelloTimeout))
	line, err := r.Next()
	if err != nil {
		reject("no agentAuth: " + err.Error())
		return
	}
	msg, _ := Decode(line)
	auth, ok := msg.(AgentAuth)
	if !ok {
		reject(fmt.Sprintf("second line is not agentAuth: %.200s", line))
		return
	}
	if !proofOK(auth.Proof, agentProof(key, proofAgent, nonce, h.Nonce, h.Name, auth.Bind, auth.Public, auth.Ports)) {
		reject("wrong proof (a different key, or a replayed agentAuth)")
		return
	}
	first, last, err := checkAgentOffer(auth)
	if err != nil {
		reject(err.Error())
		return
	}
	ip := remoteIP(c)
	if ip == nil {
		reject("not a TCP connection")
		return
	}
	a := &agentConn{m: m, c: c, r: r, name: h.Name, ip: ip,
		pending: map[int64]chan launchResult{}, procs: map[int64]*remoteProcess{}, gone: make(chan struct{})}
	a.site = &site{name: h.Name, launcher: a, pool: NewPool(first, last), bindHost: auth.Bind, gmsHost: auth.Public, agent: a}
	// agentReady and the log line before the site is published: the agent expects agentReady before
	// any launchHost, and whoever sees the site finds the registration in the log
	if err := a.send(AgentReady{Addr: ip.String()}); err != nil {
		reject("agentReady not sent: " + err.Error())
		return
	}
	m.log.Logf(who, "agent %q from %s registered (%s): GMS host %s, bind %s, ports %d-%d", h.Name, peer, h.Build,
		auth.Public, auth.Bind, first, last)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		reject("server shutting down")
		return
	}
	var old *agentConn
	for i, s := range m.sites {
		if s.agent != nil && s.name == h.Name {
			old = s.agent
			m.sites = append(m.sites[:i:i], m.sites[i+1:]...)
			break
		}
	}
	m.sites = append(m.sites, a.site)
	m.mu.Unlock()
	if old != nil {
		old.close(fmt.Sprintf("replaced by a new registration from %s", peer))
	}
	a.readLoop()
}

// agentConn is a registered agent's connection; it is also its site's Launcher.
type agentConn struct {
	m    *Manager
	c    net.Conn
	r    *LineReader
	name string
	ip   net.IP // the agent's address: its hosts' hellos must come from here
	site *site

	wmu     sync.Mutex // serializes writes
	mu      sync.Mutex
	pending map[int64]chan launchResult // launchHost sent, no answer yet (nil once closed)
	procs   map[int64]*remoteProcess    // started, not exited (nil once closed)
	gone    chan struct{}               // closed when the connection has ended
	once    sync.Once
}

type launchResult struct {
	proc *remoteProcess
	err  error
}

// send writes one line to the agent and logs it.
func (a *agentConn) send(msg Message) error {
	a.wmu.Lock()
	line, err := writeLine(a.c, msg)
	a.wmu.Unlock()
	if err != nil {
		return err
	}
	a.m.log.Logf(who, "agent %s -> %s", a.name, logged(msg, line))
	return nil
}

func (a *agentConn) lost() error { return fmt.Errorf("agent %s lost", a.name) }

// readLoop reads the agent's lines until the connection ends; a silent agent is dropped.
func (a *agentConn) readLoop() {
	for {
		a.c.SetReadDeadline(time.Now().Add(a.m.cfg.AgentTimeout))
		line, err := a.r.Next()
		if err != nil {
			var ne net.Error
			switch {
			case errors.As(err, &ne) && ne.Timeout():
				a.close(fmt.Sprintf("silent for %v", a.m.cfg.AgentTimeout))
			case errors.Is(err, io.EOF):
				a.close("connection closed by the agent")
			default:
				a.close("connection ended: " + err.Error())
			}
			return
		}
		msg, err := Decode(line)
		if err != nil {
			a.m.log.Logf(who, "agent %s: (line ignored: %v)", a.name, err)
			continue
		}
		if _, beat := msg.(AgentStatus); !beat {
			a.m.log.Logf(who, "agent %s <- %s", a.name, line)
		}
		switch v := msg.(type) {
		case AgentStatus:
		case HostStarted:
			a.started(v)
		case HostFailed:
			a.mu.Lock()
			ch := a.pending[v.Match]
			delete(a.pending, v.Match)
			a.mu.Unlock()
			if ch != nil {
				ch <- launchResult{err: fmt.Errorf("agent %s: %s", a.name, v.Error)}
			}
		case HostExited:
			a.mu.Lock()
			p := a.procs[v.Match]
			delete(a.procs, v.Match)
			a.mu.Unlock()
			if p == nil {
				a.m.log.Logf(who, "agent %s: (hostExited for match %d, which it doesn't run for us)", a.name, v.Match)
				continue
			}
			p.end(v.Code, nil)
		default:
			a.m.log.Logf(who, "agent %s: (%s from the agent, ignored)", a.name, msg.Type())
		}
	}
}

// started takes hostStarted: the waiting Launch gets its process. A late answer (Launch gave up)
// gets a killHost, so no host runs for a match the server forgot.
func (a *agentConn) started(v HostStarted) {
	a.mu.Lock()
	ch := a.pending[v.Match]
	var p *remoteProcess
	if ch != nil {
		delete(a.pending, v.Match)
		p = &remoteProcess{a: a, match: v.Match, pid: v.PID, done: make(chan struct{})}
		a.procs[v.Match] = p
	}
	a.mu.Unlock()
	if ch == nil {
		a.m.log.Logf(who, "agent %s: (hostStarted for match %d, which no launch waits for: killing it)", a.name, v.Match)
		a.send(KillHost{Match: v.Match})
		return
	}
	ch <- launchResult{proc: p}
}

// Launch asks the agent to start a host (Launcher): launchHost with the match, token and port, then
// hostStarted or hostFailed within LaunchTimeout.
func (a *agentConn) Launch(env Env) (Process, error) {
	_, ps, _ := net.SplitHostPort(env.Bind)
	port, _ := strconv.Atoi(ps)
	ch := make(chan launchResult, 1)
	a.mu.Lock()
	if a.pending == nil {
		a.mu.Unlock()
		return nil, a.lost()
	}
	a.pending[env.Match] = ch
	a.mu.Unlock()
	forget := func() {
		a.mu.Lock()
		if a.pending != nil {
			delete(a.pending, env.Match)
		}
		a.mu.Unlock()
	}
	if err := a.send(LaunchHost{Match: env.Match, Token: env.Token, Port: port}); err != nil {
		forget()
		a.close("launchHost not sent: " + err.Error())
		return nil, fmt.Errorf("agent %s: %v", a.name, err)
	}
	t := time.NewTimer(a.m.cfg.LaunchTimeout)
	defer t.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return r.proc, nil
	case <-a.gone:
		return nil, a.lost()
	case <-t.C:
		forget()
		select {
		case r := <-ch: // answered just now
			if r.err != nil {
				return nil, r.err
			}
			return r.proc, nil
		default:
		}
		a.send(KillHost{Match: env.Match}) // in case it starts after all
		return nil, fmt.Errorf("agent %s: no answer to launchHost within %v", a.name, a.m.cfg.LaunchTimeout)
	}
}

// close ends the agent's connection (once): pending launches fail, the site goes, and every process
// it started counts as exited with code -1 ("agent <name> lost"), which ends its match. The log line
// comes first, so whoever sees the site gone or the connection closed finds it in the log.
func (a *agentConn) close(reason string) {
	a.once.Do(func() {
		a.mu.Lock()
		procs := a.procs
		a.procs, a.pending = nil, nil
		close(a.gone)
		a.mu.Unlock()
		m := a.m
		m.log.Logf(who, "agent %q (%s) gone: %s; %d running hosts end with it", a.name, a.ip, reason, len(procs))
		m.mu.Lock()
		for i, s := range m.sites {
			if s == a.site {
				m.sites = append(m.sites[:i:i], m.sites[i+1:]...)
				break
			}
		}
		m.mu.Unlock()
		a.c.Close()
		for _, p := range procs {
			p.end(-1, a.lost())
		}
	})
}

// remoteProcess is a host an agent started (Process).
type remoteProcess struct {
	a     *agentConn
	match int64
	pid   int
	done  chan struct{}
	once  sync.Once
	code  int
	err   error
}

func (p *remoteProcess) Pid() int { return p.pid }

// Kill asks the agent to kill the process; it has ended once Wait returns.
func (p *remoteProcess) Kill() error {
	select {
	case <-p.done:
		return nil
	default:
	}
	return p.a.send(KillHost{Match: p.match})
}

// Wait returns the exit code the agent reported, or -1 and an error if the agent was lost first.
func (p *remoteProcess) Wait() (int, error) {
	<-p.done
	return p.code, p.err
}

func (p *remoteProcess) end(code int, err error) {
	p.once.Do(func() {
		p.code, p.err = code, err
		close(p.done)
	})
}
