package gmshost

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/version"
)

// The agent link through the main server's tunnel gateway (REGION1; the decision: docs/development/decisions/
// 0007-region-host-machines.md), the main server's side.
// A host machine's agent dials the gateway with TLS 1.3 and the main server's key pinned, says hello kind 4
// (tunnel.DialAgent), and the gateway hands the connection to ServeAgent. The registration is H2's (remote.go) with
// three changes:
//
//   - a key per machine (LinkConfig.Machines: name -> region and key) instead of one shared key; an unknown name
//     gets a challenge with a proof under a random key, so it looks like a wrong key from outside;
//   - both proofs end with the TLS session's exporter value (tunnel.AgentExporter), so a proof is good only on the
//     TLS session it was made on;
//   - the offer adds the machine's match gateway, its pin and its map archives, agentHello.build must equal this
//     server's version (else agentRefused), and agentReady adds the server's clock and start.
//
// Both ends then derive the link's ticket key (MachineInfo.TicketKey, Agent.TicketKey; REGION2's tickets), new at
// every registration and never sent. The hosts on the machine dial the agent's relay (relay.go), which carries each
// host's control lines in the link as hostLine/hostClose; here each host's channel is an in-memory connection
// (net.Pipe) handed to hostHello, whose RemoteAddr is the link's. A host of such a machine says hello only through
// its machine's link (relayedRight).

// LinkConfig configures the host machines on the TLS link (Config.Link).
type LinkConfig struct {
	// Machines are the host machines that may register, by name: each with its region (from this configuration,
	// never from the machine) and its own key (NewAgentKey, ReadAgentKey). Empty: none.
	Machines map[string]HostMachine
	// Version is the release both ends must run ("" = version.String()).
	Version string
	// Ports is this server's GMS port range (--gms-ports): an agent's ports must lie inside it, since Start-Client
	// listens only on the UDP ports the main session lists ("" = not checked).
	Ports string
	// Options are the host options (DGGMS_OPTIONS) sent with every launchHost, so both regions play by one set of
	// rules; the agent replaces only its own net_limit_shared_ips.
	Options string
	// Started is this server's start (agentReady.started names the agent's match folders; zero = the process's).
	Started time.Time

	// Levels turns a machine's maps offer into the levels it can host (REGION5, region_link.go; nil: every level).
	Levels func(offer map[string]string) []string
}

// HostMachine is a host machine the main server knows.
type HostMachine struct {
	Region string
	Key    []byte
}

// MachineInfo is a registered host machine on the TLS link (Manager.Machine).
type MachineInfo struct {
	Name, Region string
	Addr         string            // the link's source address
	Gateway, Pin string            // its match gateway ("IPv4:port") and that gateway's pin (hex); "" = none offered
	Maps         map[string]string // its host copy's map archives (lower-case name -> SHA-256 hex)
	TicketKey    []byte            // the link's ticket key (32 bytes): REGION2's tickets for this machine
}

const (
	proofTickets = "dgserver agent: tickets" // the ticket key's role (never a proof on the wire)
	// linkMaxLine is the longest line on the agent link: a host's longest line (MaxLine) inside a hostLine
	linkMaxLine = MaxLine + 4096
	// relayQueue is how many of a host channel's lines (and of the link's relayed lines) may wait: a host or link
	// that falls further behind closes that host's channel, not the link
	relayQueue = 256
	// StartedLayout is agentReady.started's layout (UTC)
	StartedLayout  = "20060102-150405"
	maxOfferedMaps = 64
)

var processStart = time.Now()

var (
	archiveRE = regexp.MustCompile(`^[a-z0-9_][a-z0-9._-]{0,63}$`)
	sha256RE  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	startedRE = regexp.MustCompile(`^[0-9A-Za-z-]{1,32}$`)
)

// HostLine carries one control line of a host in the agent link (either way); Line is the line itself (a JSON
// object, embedded as it is).
type HostLine struct {
	Match int64           `json:"match"`
	Line  json.RawMessage `json:"line"`
}

// HostClose ends a host's channel in the agent link (either way).
type HostClose struct {
	Match int64  `json:"match"`
	Why   string `json:"why"`
}

// AgentRefused answers a correct agentAuth the server still refuses (another release), before it closes.
type AgentRefused struct {
	Reason string `json:"reason"`
}

func (HostLine) Type() string     { return "hostLine" }
func (HostClose) Type() string    { return "hostClose" }
func (AgentRefused) Type() string { return "agentRefused" }

func init() {
	for _, m := range []Message{HostLine{}, HostClose{}, AgentRefused{}} {
		messageTypes[m.Type()] = m
	}
}

// newLinkReader reads the agent link's lines (up to linkMaxLine).
func newLinkReader(c net.Conn) *LineReader {
	r := NewLineReader(c)
	r.s.Buffer(make([]byte, 4096), linkMaxLine+2)
	r.max = linkMaxLine
	return r
}

// mapsPart is the offer's maps as one proof part: "name=sha" sorted by name, joined by ",".
func mapsPart(maps map[string]string) string {
	parts := make([]string, 0, len(maps))
	for k, v := range maps {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// linkProofs are the TLS link's proofs: H2's parts, then the offer's new fields and the exporter value.
func serverLinkProof(key []byte, agentNonce, serverNonce, exporter string) string {
	return agentProof(key, proofServer, agentNonce, serverNonce, exporter)
}

func agentLinkProof(key []byte, serverNonce, agentNonce, name string, o AgentAuth, exporter string) string {
	return agentProof(key, proofAgent, serverNonce, agentNonce, name, o.Bind, o.Public, o.Ports, o.Gateway, o.Pin,
		mapsPart(o.Maps), exporter)
}

func ticketKey(key []byte, serverNonce, agentNonce, exporter string) []byte {
	b, _ := hex.DecodeString(agentProof(key, proofTickets, serverNonce, agentNonce, exporter))
	return b
}

// checkLinkOffer checks the TLS link's additions to an offer: the match gateway an IPv4 address and port with a
// pin (or neither), the maps' names and hashes, and the ports inside mainPorts (if set).
func checkLinkOffer(a AgentAuth, mainPorts string) error {
	if a.Gateway != "" || a.Pin != "" {
		host, port, err := net.SplitHostPort(a.Gateway)
		ip := net.ParseIP(host).To4()
		n, perr := strconv.Atoi(port)
		if err != nil || ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) || perr != nil || n < 1 || n > 65535 {
			return fmt.Errorf("match gateway %q is not an IPv4 address and port players can connect to", a.Gateway)
		}
		if !sha256RE.MatchString(a.Pin) {
			return fmt.Errorf("match gateway pin %q is not 64 hex digits", a.Pin)
		}
	}
	if len(a.Maps) > maxOfferedMaps {
		return fmt.Errorf("%d maps offered (at most %d)", len(a.Maps), maxOfferedMaps)
	}
	for k, v := range a.Maps {
		if !archiveRE.MatchString(k) || !sha256RE.MatchString(v) {
			return fmt.Errorf("map %q %q is not a lower-case archive name and a SHA-256", k, v)
		}
	}
	if mainPorts != "" {
		mf, ml, err := ParsePorts(mainPorts)
		af, al, aerr := ParsePorts(a.Ports)
		if err == nil && aerr == nil && (af < mf || al > ml) {
			return fmt.Errorf("ports %s are not inside this server's GMS ports %s", a.Ports, mainPorts)
		}
	}
	return nil
}

// checkLinkAddrs is the TLS link's rule on the addresses a machine offers, its GMS host (Public) and its match gateway,
// by the link's source src: an IPv4 address on the internet; a loopback one only over a link from this machine (the
// test lanes' host machines on 127.0.2.x); one in a private network or the carrier-grade NAT range 100.64.0.0/10 only
// over a link from such an address or from this machine (a LAN); never link-local, 0.0.0.0/8, multicast, broadcast or
// reserved. So a host machine elsewhere can't point the main server's players at its own match hosts (127.0.1.x) or
// into its network.
func checkLinkAddrs(a AgentAuth, src net.IP) error {
	from, ok := netip.AddrFromSlice(src.To4())
	if !ok {
		return fmt.Errorf("the link comes from %v, no IPv4 address", src)
	}
	check := func(what, s string) error {
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return fmt.Errorf("%s %q is no IPv4 address", what, s)
		}
		addr = addr.Unmap()
		local := from.IsLoopback()
		inNet := local || from.IsPrivate() || sharedNet.Contains(from)
		switch {
		case !addr.Is4():
			return fmt.Errorf("%s %v is no IPv4 address", what, addr)
		case addr.IsLoopback():
			if !local {
				return fmt.Errorf("%s %v is a loopback address, and the machine's link comes from %v", what, addr, from)
			}
		case addr.IsPrivate() || sharedNet.Contains(addr):
			if !inNet {
				return fmt.Errorf("%s %v is in a private network, and the machine's link comes from %v", what, addr, from)
			}
		case thisNet.Contains(addr) || reservedNet.Contains(addr) || !addr.IsGlobalUnicast():
			return fmt.Errorf("%s %v is no address players can reach", what, addr)
		}
		return nil
	}
	if err := check("public host", a.Public); err != nil {
		return err
	}
	if a.Gateway != "" {
		host, _, err := net.SplitHostPort(a.Gateway)
		if err != nil {
			return fmt.Errorf("match gateway %q is not an IPv4 address and port", a.Gateway)
		}
		return check("match gateway", host)
	}
	return nil
}

// The IPv4 ranges checkLinkAddrs treats on their own (RFC 6598, RFC 1122, RFC 1112).
var (
	sharedNet   = netip.MustParsePrefix("100.64.0.0/10")
	thisNet     = netip.MustParsePrefix("0.0.0.0/8")
	reservedNet = netip.MustParsePrefix("240.0.0.0/4")
)

func (lc LinkConfig) version() string {
	if lc.Version != "" {
		return lc.Version
	}
	return version.String()
}

// ServeAgent serves a host machine's agent link (tunnel.Gateway.Agents): the registration, then the agent's lines
// until the link ends.
func (m *Manager) ServeAgent(c *tls.Conn, peer string) {
	defer m.log.Recover(who, peer, c) // a panic ends this link only
	exp, err := tunnel.AgentExporter(c)
	if err != nil {
		m.log.Logf(who, "agent link from %s: %v", peer, err)
		c.Close()
		return
	}
	c.SetReadDeadline(time.Now().Add(m.cfg.HelloTimeout))
	r := newLinkReader(c)
	line, err := r.Next()
	if err != nil {
		m.log.Logf(who, "agent link from %s closed before agentHello: %v", peer, err)
		c.Close()
		return
	}
	msg, _ := Decode(line)
	h, ok := msg.(AgentHello)
	if !ok {
		m.log.Logf(who, "agent link from %s rejected: first line is not agentHello: %.200s", peer, line)
		c.Close()
		return
	}
	m.linkHello(c, r, h, peer, hex.EncodeToString(exp))
}

// linkHello registers a host machine on the TLS link and serves it (remote.go's agentHello, per machine).
func (m *Manager) linkHello(c net.Conn, r *LineReader, h AgentHello, peer, exporter string) {
	reject := func(why string) {
		m.log.Logf(who, "host machine %q from %s rejected: %s", h.Name, peer, why)
		c.Close()
	}
	lc := m.cfg.Link
	switch {
	case len(lc.Machines) == 0:
		reject("this server takes no host machines (no --host-machine)")
		return
	case !agentNameRE.MatchString(h.Name) || h.Name == LocalSite:
		reject("bad name (1-64 of A-Z a-z 0-9 . _ -, not \"local\")")
		return
	case !nonceRE.MatchString(h.Nonce):
		reject("bad nonce")
		return
	}
	mach, known := lc.Machines[h.Name]
	key := mach.Key
	if !known || len(key) == 0 { // the same steps as a wrong key: a challenge whose proof no key matches
		var b [32]byte
		rand.Read(b[:])
		key = []byte(hex.EncodeToString(b[:]))
	}
	nonce := newToken()
	if _, err := writeLine(c, AgentChallenge{Nonce: nonce, Proof: serverLinkProof(key, h.Nonce, nonce, exporter)}); err != nil {
		reject("challenge not sent: " + err.Error())
		return
	}
	c.SetReadDeadline(time.Now().Add(m.cfg.HelloTimeout))
	line, err := r.Next()
	switch {
	case !known:
		reject("unknown host machine name")
		return
	case err != nil:
		reject("no agentAuth: " + err.Error() + " (the agent found the challenge's proof wrong: another key?)")
		return
	}
	msg, _ := Decode(line)
	auth, ok := msg.(AgentAuth)
	if !ok {
		reject(fmt.Sprintf("second line is not agentAuth: %.200s", line))
		return
	}
	if !proofOK(auth.Proof, agentLinkProof(key, nonce, h.Nonce, h.Name, auth, exporter)) {
		reject("wrong proof (another key, or an agentAuth from another TLS session)")
		return
	}
	first, last, err := checkAgentOffer(auth)
	if err == nil {
		err = checkLinkOffer(auth, lc.Ports)
	}
	if err != nil {
		reject(err.Error())
		return
	}
	if v := lc.version(); h.Build != v {
		why := fmt.Sprintf("version %s, this server %s", h.Build, v)
		writeLine(c, AgentRefused{Reason: why})
		m.MachineRefused(h.Name, FellBackVersion) // REGION5: its region's games fall back with "version"
		reject(why + " (both machines run one release)")
		return
	}
	ip := remoteIP(c)
	if ip == nil {
		reject("not a TCP connection")
		return
	}
	if err := checkLinkAddrs(auth, ip); err != nil {
		reject(err.Error())
		return
	}
	a := &agentConn{m: m, c: c, r: r, name: h.Name, ip: ip,
		pending: map[int64]chan launchResult{}, procs: map[int64]*remoteProcess{}, gone: make(chan struct{})}
	a.link = &linkState{region: mach.Region, gateway: auth.Gateway, pin: auth.Pin, maps: auth.Maps,
		ticketKey: ticketKey(key, nonce, h.Nonce, exporter), peer: c.RemoteAddr(),
		chans: map[int64]*linkChan{}, out: make(chan []byte, relayQueue)}
	a.site = &site{name: h.Name, launcher: a, pool: NewPool(first, last), bindHost: auth.Bind, gmsHost: auth.Public, agent: a}
	started := lc.Started
	if started.IsZero() {
		started = processStart
	}
	if err := a.send(AgentReady{Addr: ip.String(), Now: time.Now().UnixMilli(), Started: started.UTC().Format(StartedLayout)}); err != nil {
		reject("agentReady not sent: " + err.Error())
		return
	}
	gw := "none"
	if auth.Gateway != "" {
		gw = auth.Gateway + " (pin " + auth.Pin[:16] + ")"
	}
	m.log.Logf(who, "host machine %q (region %s) from %s registered over the TLS link (%s): GMS host %s, bind %s, ports %d-%d, match gateway %s, %d maps",
		h.Name, mach.Region, peer, h.Build, auth.Public, auth.Bind, first, last, gw, len(auth.Maps))
	m.machineRegistered(h.Name, auth.Maps) // REGION5: region_link.go

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
	go a.linkWriter()
	a.readLoop()
}

// Machine is a registered host machine on the TLS link, by name.
func (m *Manager) Machine(name string) (MachineInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sites {
		if a := s.agent; a != nil && a.link != nil && s.name == name {
			l := a.link
			return MachineInfo{Name: name, Region: l.region, Addr: a.ip.String(), Gateway: l.gateway, Pin: l.pin,
				Maps: l.maps, TicketKey: append([]byte(nil), l.ticketKey...)}, true
		}
	}
	return MachineInfo{}, false
}

// linkState is an agentConn's part for the TLS link.
type linkState struct {
	region, gateway, pin string
	maps                 map[string]string
	ticketKey            []byte
	peer                 net.Addr            // the link's source: the hosts' channels' RemoteAddr
	out                  chan []byte         // relayed lines waiting for the link (linkWriter)
	mu                   sync.Mutex          // guards chans
	chans                map[int64]*linkChan // the hosts' channels by match; nil once the link ended
}

// hostOptions are launchHost's options: this server's host options on the TLS link, none on H2's.
func (a *agentConn) hostOptions() string {
	if a.link == nil {
		return ""
	}
	return a.m.cfg.Link.Options
}

// linkWriter writes the relayed lines in order, until the link ends; a write that fails ends the link.
func (a *agentConn) linkWriter() {
	for {
		select {
		case line := <-a.link.out:
			a.wmu.Lock()
			a.c.SetWriteDeadline(time.Now().Add(writeTimeout))
			_, err := a.c.Write(line)
			a.wmu.Unlock()
			if err != nil {
				a.close("the link's write failed: " + err.Error())
				return
			}
		case <-a.gone:
			return
		}
	}
}

// relaySend queues a hostLine or hostClose for the link; false when the queue is full or the link gone.
func (a *agentConn) relaySend(msg Message) bool {
	line, err := Encode(msg)
	if err != nil {
		return false
	}
	select {
	case <-a.gone:
		return false
	default:
	}
	select {
	case a.link.out <- line:
		return true
	default:
		return false
	}
}

// relayed takes a hostLine or hostClose (true); other messages are readLoop's. On H2's plain link they are
// dropped: its hosts dial the control port themselves.
func (a *agentConn) relayed(msg Message) bool {
	switch v := msg.(type) {
	case HostLine:
		if a.link == nil {
			a.m.log.Logf(who, "agent %s: (hostLine on a plain link, ignored)", a.name)
		} else {
			a.hostLine(v)
		}
		return true
	case HostClose:
		if a.link == nil {
			a.m.log.Logf(who, "agent %s: (hostClose on a plain link, ignored)", a.name)
		} else {
			a.m.log.Logf(who, "agent %s: match %d: the host's channel closed there: %s", a.name, v.Match, v.Why)
			a.link.mu.Lock()
			ch := a.link.chans[v.Match]
			a.link.mu.Unlock()
			if ch != nil {
				ch.closeAfterQueued()
			}
		}
		return true
	}
	return false
}

// hostLine takes a host's line from the link: the first of a match opens its channel (it must be the host's
// hello, which goes to hostHello with the channel as its connection), the others go to the match in order.
func (a *agentConn) hostLine(v HostLine) {
	l := a.link
	l.mu.Lock()
	if l.chans == nil {
		l.mu.Unlock()
		return
	}
	ch := l.chans[v.Match]
	l.mu.Unlock()
	if ch != nil {
		if !ch.push(v.Line) {
			ch.close("the host's lines came faster than its match read them", true)
		}
		return
	}
	msg, _ := Decode(v.Line)
	h, ok := msg.(Hello)
	if !ok || h.Match != v.Match {
		a.m.log.Logf(who, "agent %s: match %d: a host channel whose first line is no hello for it", a.name, v.Match)
		a.relaySend(HostClose{Match: v.Match, Why: "the first line is not the host's hello"})
		return
	}
	srv, ag := net.Pipe()
	ch = &linkChan{a: a, match: v.Match, srv: &relayConn{Conn: srv, a: a}, ag: ag,
		in: make(chan []byte, relayQueue), done: make(chan struct{})}
	l.mu.Lock()
	if l.chans == nil {
		l.mu.Unlock()
		srv.Close()
		ag.Close()
		return
	}
	l.chans[v.Match] = ch
	l.mu.Unlock()
	go ch.feed()
	go ch.pump()
	peer := logging.Peer(l.peer)
	go func() {
		defer a.m.log.Recover(who, peer, ch.srv)
		a.m.hostHello(ch.srv, NewLineReader(ch.srv), h, peer)
	}()
}

// closeRelay ends every host channel of the link (the link has ended).
func (a *agentConn) closeRelay() {
	if a.link == nil {
		return
	}
	a.link.mu.Lock()
	chans := a.link.chans
	a.link.chans = nil
	a.link.mu.Unlock()
	for _, ch := range chans {
		ch.close("the link ended", false)
	}
}

// relayConn is a host's channel as hostHello and the match see it: an in-memory connection whose RemoteAddr is
// its agent link's source.
type relayConn struct {
	net.Conn
	a *agentConn
}

func (c *relayConn) RemoteAddr() net.Addr { return c.a.link.peer }

// relayedRight: may a hello for a host on s come over c? A host of a machine on the TLS link only through that
// machine's link; every other host only over a real control connection. Called with m.mu held.
func relayedRight(s *site, c net.Conn) bool {
	rc, relayed := c.(*relayConn)
	if s.agent == nil || s.agent.link == nil {
		return !relayed
	}
	return relayed && rc.a == s.agent
}

// linkChan is one host's channel on the main server: the match reads and writes srv, the link side ag.
type linkChan struct {
	a     *agentConn
	match int64
	srv   *relayConn
	ag    net.Conn
	in    chan []byte // lines for the match; a nil line closes the channel after the ones before it
	done  chan struct{}
	once  sync.Once
}

// push queues a line for the match; false when the queue is full.
func (ch *linkChan) push(line []byte) bool {
	select {
	case ch.in <- line:
		return true
	case <-ch.done:
		return true
	default:
		return false
	}
}

// closeAfterQueued closes the channel once the lines queued before have reached the match (the host's last lines,
// finishGame, come right before its close).
func (ch *linkChan) closeAfterQueued() {
	if !ch.push(nil) {
		ch.close("the host's channel closed there", false)
	}
}

// feed writes the queued lines into the pipe, in order.
func (ch *linkChan) feed() {
	for {
		select {
		case line := <-ch.in:
			if line == nil {
				ch.close("the host's channel closed there", false)
				return
			}
			if _, err := ch.ag.Write(append(append([]byte(nil), line...), '\n')); err != nil {
				ch.close("the match closed the host's connection", true)
				return
			}
		case <-ch.done:
			return
		}
	}
}

// pump carries the match's lines to the host through the link; the match closing the connection closes the
// channel there too.
func (ch *linkChan) pump() {
	r := newLinkReader(ch.ag)
	for {
		line, err := r.Next()
		if err != nil {
			ch.close("the match closed the host's connection", true)
			return
		}
		if !ch.a.relaySend(HostLine{Match: ch.match, Line: json.RawMessage(line)}) {
			ch.close("the link's queue is full", true)
			return
		}
	}
}

// close ends the channel once; tell sends hostClose to the agent.
func (ch *linkChan) close(why string, tell bool) {
	ch.once.Do(func() {
		l := ch.a.link
		l.mu.Lock()
		if l.chans != nil && l.chans[ch.match] == ch {
			delete(l.chans, ch.match)
		}
		l.mu.Unlock()
		close(ch.done)
		ch.ag.Close() // the match reads EOF, as from a host's own closed connection; it closes srv itself
		if tell {
			ch.a.relaySend(HostClose{Match: ch.match, Why: why})
		}
	})
}

// errSlow marks an agent error after which the agent waits RetrySlow before it dials again (a refusal, a wrong
// pin or key: trying again at once would not change it).
type errSlow struct{ err error }

func (e errSlow) Error() string { return e.err.Error() }
func (e errSlow) Unwrap() error { return e.err }

func slow(err error) error { return errSlow{err} }

func isSlow(err error) bool {
	var s errSlow
	return errors.As(err, &s)
}
