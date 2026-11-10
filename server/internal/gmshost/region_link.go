package gmshost

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"time"
)

// REGION5: the region pick (region.go) joined to the TLS link (link.go, agent_link.go, relay.go). On the main server a
// machine's registration clears an earlier refusal and records its maps (as levels, LinkConfig.Levels), and a refusal
// for another release is remembered (FellBackVersion). On the host machine each registration hands its ticket key and
// the main server's clock to the match gateway (AgentLink.Linked), and the relay annotates a host's verifyHello with
// the match gateway's flow (FlowAnnotator).

// HelloFlow is a verifyHello's relay annotation: the match gateway's session behind the address holds tickets for
// Match, for Users.
type HelloFlow struct {
	Match int64   `json:"match"`
	Users []int64 `json:"users"`
}

// machineRegistered is a TLS-linked machine's good registration: no refusal any more, and the levels it offers.
func (m *Manager) machineRegistered(name string, offer map[string]string) {
	m.MachineRefused(name, "")
	var levels []string // nil: every level
	if f := m.cfg.Link.Levels; f != nil {
		if levels = f(offer); levels == nil {
			levels = []string{}
		}
	}
	m.SetMachineMaps(name, levels)
}

// linkedHook tells AgentLink.Linked about a registration (on) or the link's end.
func (a *Agent) linkedHook(on bool) {
	if a.Link.Linked == nil {
		return
	}
	if !on {
		a.Link.Linked(nil, time.Now())
		return
	}
	a.mu.Lock()
	key := append([]byte(nil), a.linked.ticketKey...)
	now := time.Now().Add(a.linked.offset)
	a.mu.Unlock()
	a.Link.Linked(key, now)
}

// FlowAnnotator is AgentLink.Annotate for a host machine with a match gateway: flow names the session behind a host's
// GMS peer address (tunnel.Gateway.FlowAdmission). A verifyHello whose address is such a flow gets a "flow" member
// with the session's match and users; any other line, and an address that is no flow, goes through as it is (the
// main server then refuses the claim: no annotation).
func FlowAnnotator(flow func(netip.AddrPort) (match int64, users []int64, ok bool)) func(int64, []byte) []byte {
	return func(_ int64, line []byte) []byte {
		var v VerifyHello
		if json.Unmarshal(line, &v) != nil {
			return line
		}
		ap, err := netip.ParseAddrPort(v.Addr)
		if err != nil {
			return line
		}
		match, users, ok := flow(netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
		if !ok {
			return line
		}
		if users == nil {
			users = []int64{}
		}
		note, err := json.Marshal(HelloFlow{Match: match, Users: users})
		body := bytes.TrimRight(line, " \t\r\n")
		if err != nil || len(body) < 2 || body[len(body)-1] != '}' {
			return line
		}
		out := append([]byte(nil), body[:len(body)-1]...)
		if len(bytes.TrimSpace(out)) > 1 { // not "{"
			out = append(out, ',')
		}
		out = append(out, `"flow":`...)
		out = append(out, note...)
		return append(out, '}')
	}
}
