package gmshost

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/tunnel"
)

// The agent's side of the TLS link (REGION1; the main server's side and the protocol: link.go). With Agent.Link.Pin
// set the agent dials Agent.Server, the main server's tunnel gateway, with that pin, registers with this machine's
// own key (Agent.Key) and its offer (bind, public, ports, the match gateway and its pin, the maps), and starts its
// hosts with DGGMS_CONTROL = its relay (relay.go), which carries their lines in the link.

// AgentLink configures the TLS link of an Agent.
type AgentLink struct {
	// Pin is the main server's gateway key; zero = H2's plain link to the control port.
	Pin tunnel.Pin
	// Relay is where the hosts' control port listens on this machine, "127.0.0.1:10040" (loopback only).
	Relay string
	// Gateway and GatewayPin are this machine's match gateway ("IPv4:port") and its pin (64 hex), offered to the
	// main server for its players' second session (REGION2); both "" = none.
	Gateway, GatewayPin string
	// Maps are this machine's host copy's map archives (lower-case archive name -> SHA-256 hex).
	Maps map[string]string
	// SharedIPs replaces net_limit_shared_ips in the main server's host options: the address this machine's hosts
	// see every player at ("" = the main server's value stays).
	SharedIPs string
	// RetrySlow is the wait after a refusal, a wrong pin or a failed key check (0 = 60 s).
	RetrySlow time.Duration
	// Annotate, when set, may rewrite a host's verifyHello line before the relay sends it (REGION2/5: the flow
	// annotation); a host's own line with a "flow" member never gets this far.
	Annotate func(match int64, line []byte) []byte
	// Linked, when set, gets each registration's ticket key and the main server's clock at agentReady (REGION5: the
	// match gateway's TicketGuard.SetKey), and a nil key when the link ends.
	Linked func(ticketKey []byte, mainNow time.Time)
}

// linkedState is what the current registration gave the agent (under Agent.mu).
type linkedState struct {
	ticketKey []byte
	offset    time.Duration // the main server's clock minus this one's, at agentReady
	started   string
}

func (a *Agent) linkOn() bool { return !a.Link.Pin.IsZero() }

// OfferMaps is AgentLink.Maps for a host copy's map archives (lower-case archive name -> SHA-256): the names an
// offer can carry, at most 64 (by name).
func OfferMaps(versions map[string][32]byte) map[string]string {
	names := make([]string, 0, len(versions))
	for name := range versions {
		if archiveRE.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > maxOfferedMaps {
		names = names[:maxOfferedMaps]
	}
	out := map[string]string{}
	for _, name := range names {
		sum := versions[name]
		out[name] = hex.EncodeToString(sum[:])
	}
	return out
}

// checkLink validates the TLS link's configuration (Check).
func (a *Agent) checkLink() error {
	if !a.linkOn() {
		return nil
	}
	host, port, err := net.SplitHostPort(a.Link.Relay)
	if err != nil {
		return fmt.Errorf("gmshost: agent: bad relay address %q: %v", a.Link.Relay, err)
	}
	if ip := net.ParseIP(host).To4(); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("gmshost: agent: the relay %q must be a loopback IPv4 address (the hosts run on this machine)", a.Link.Relay)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("gmshost: agent: bad relay port in %q", a.Link.Relay)
	}
	if err := checkLinkOffer(AgentAuth{Gateway: a.Link.Gateway, Pin: a.Link.GatewayPin, Maps: a.Link.Maps}, ""); err != nil {
		return fmt.Errorf("gmshost: agent: %v", err)
	}
	return nil
}

// TicketKey is the current link's ticket key (32 bytes; nil while not registered): REGION2 checks tickets with it.
func (a *Agent) TicketKey() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]byte(nil), a.linked.ticketKey...)
}

// ServerNow is this machine's clock corrected to the main server's (agentReady.now; uncorrected before it).
func (a *Agent) ServerNow() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Now().Add(a.linked.offset)
}

// HostPort is the GMS port of a match whose host runs here (REGION2: a ticket's match routes only there).
func (a *Agent) HostPort(match int64) (int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if h := a.running[match]; h != nil {
		return h.port, true
	}
	return 0, false
}

// hostOptions are a host's DGGMS_OPTIONS from launchHost's: the main server's, with this machine's own
// net_limit_shared_ips ("" = the launcher's own options, H2).
func (a *Agent) hostOptions(main string) string {
	if main == "" || a.Link.SharedIPs == "" {
		return main
	}
	return replaceOption(main, "net_limit_shared_ips", a.Link.SharedIPs)
}

// replaceOption sets key in "k=v;k=v" options, replacing any value it had (keys compare without case).
func replaceOption(options, key, value string) string {
	var out []string
	for _, kv := range strings.Split(options, ";") {
		k, _, _ := strings.Cut(kv, "=")
		if strings.TrimSpace(kv) == "" || strings.EqualFold(strings.TrimSpace(k), key) {
			continue
		}
		out = append(out, kv)
	}
	return strings.Join(append(out, key+"="+value), ";")
}

// dataName names a host's folder: "<the main server's start>_<match>" on the TLS link (the main server's match ids
// start at 1 again after its restart), the match id on H2's.
func (l *agentLink) dataName(match int64) string {
	if l.started == "" {
		return ""
	}
	return l.started + "_" + strconv.FormatInt(match, 10)
}

// relay queues a relayed line (hostLine, hostClose) for the link; false when the queue is full or the link gone.
func (l *agentLink) relay(msg Message) bool {
	line, err := Encode(msg)
	if err != nil {
		return false
	}
	select {
	case <-l.gone:
		return false
	default:
	}
	select {
	case l.out <- line:
		return true
	default:
		return false
	}
}

// writer writes the relayed lines in order until the link ends; a failed write ends the link.
func (l *agentLink) writer() {
	for {
		select {
		case line := <-l.out:
			l.mu.Lock()
			l.c.SetWriteDeadline(time.Now().Add(writeTimeout))
			_, err := l.c.Write(line)
			l.mu.Unlock()
			if err != nil {
				l.c.Close()
				return
			}
		case <-l.gone:
			return
		}
	}
}

// serveLink runs one TLS link: dial the gateway, register, then launchHost, killHost and the hosts' lines.
func (a *Agent) serveLink(ctx context.Context) error {
	c, err := tunnel.DialAgent(ctx, a.Server, a.Link.Pin, 10*time.Second)
	if err != nil {
		if errors.Is(err, tunnel.ErrPinMismatch) {
			return slow(fmt.Errorf("the main server's key is not the pinned one (%v)", err))
		}
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	exp, err := tunnel.AgentExporter(c)
	if err != nil {
		return err
	}
	exporter := hex.EncodeToString(exp)
	l := &agentLink{c: c, out: make(chan []byte, relayQueue), gone: make(chan struct{})}
	defer close(l.gone)
	r := newLinkReader(c)
	public := a.public()

	nonce := newToken()
	if err := l.send(AgentHello{Name: a.Name, Nonce: nonce, Build: a.Build}); err != nil {
		return err
	}
	read := func(what string) (Message, error) {
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		line, err := r.Next()
		if err != nil {
			return nil, fmt.Errorf("no %s: %v", what, err)
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
	if !proofOK(ch.Proof, serverLinkProof(a.Key, nonce, ch.Nonce, exporter)) {
		return slow(fmt.Errorf("the main server failed the key check (it has another key for %q, or doesn't know the name); not registering", a.Name))
	}
	offer := AgentAuth{Bind: a.BindHost, Public: public, Ports: a.Ports, Gateway: a.Link.Gateway, Pin: a.Link.GatewayPin, Maps: a.Link.Maps}
	offer.Proof = agentLinkProof(a.Key, ch.Nonce, nonce, a.Name, offer, exporter)
	if err := l.send(offer); err != nil {
		return err
	}
	if msg, err = read(TAgentReady); err != nil {
		return fmt.Errorf("%v (the main server refused the offer or the key: see its log)", err)
	}
	if ref, ok := msg.(AgentRefused); ok {
		return slow(fmt.Errorf("refused by the main server: %s (this machine %s)", ref.Reason, a.Build))
	}
	ready, ok := msg.(AgentReady)
	if !ok {
		return fmt.Errorf("expected agentReady, got %v", msg)
	}
	c.SetReadDeadline(time.Time{})
	if !startedRE.MatchString(ready.Started) {
		ready.Started = ""
	}
	l.started = ready.Started
	a.mu.Lock()
	a.linked = linkedState{ticketKey: ticketKey(a.Key, ch.Nonce, nonce, exporter), started: ready.Started}
	if ready.Now > 0 {
		a.linked.offset = time.UnixMilli(ready.Now).Sub(time.Now())
	}
	a.mu.Unlock()
	a.linkedHook(true) // REGION5: region_link.go
	defer func() {
		a.mu.Lock()
		a.linked = linkedState{}
		a.mu.Unlock()
		a.linkedHook(false)
	}()
	relayAddr := a.relayAddr()
	a.Log.Logf(agentWho, "registered with %s over the TLS link as %q (seen from %s; main server started %s): GMS host %s, bind %s, ports %s, relay %s",
		a.Server, a.Name, ready.Addr, ready.Started, public, a.BindHost, a.Ports, relayAddr)
	a.attachRelay(l)
	defer a.detachRelay()
	go l.writer()

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
		switch v := msg.(type) {
		case HostLine:
			a.toHost(v)
			continue
		case HostClose:
			a.closeFromServer(v)
			continue
		}
		a.Log.Logf(agentWho, "<- %s", logged(msg, line))
		switch v := msg.(type) {
		case LaunchHost:
			a.launch(l, relayAddr, v)
		case KillHost:
			a.kill(v.Match)
		default:
			a.Log.Logf(agentWho, "(%s from the server, ignored)", msg.Type())
		}
	}
}
