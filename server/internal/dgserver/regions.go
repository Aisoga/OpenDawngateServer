package dgserver

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/limits"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup/exepatch"
	"dawngate/server/internal/tunnel"
)

// REGION5: regions for custom games, joined. The main server (--region, --host-machine) turns the
// host manager's regions on, records each machine's maps as the levels it can host, and hands Blaze RegionRoute: a
// tunnelled player of a game on a host machine with a match gateway gets that gateway, its pin and a ticket for the
// match. A host machine (--agent-tls with --tunnel on) runs that match gateway: tickets checked with the link's ticket
// key (each registration's, AgentLink.Linked), one route per admitted session to its match's host on this machine,
// and the relay's verifyHello annotation from the gateway's sessions (gmshost.FlowAnnotator).

// wireRegions turns the regions on (--region) with --host-machine's machines, gives them their names (--region-name,
// PREF1; also LocalRegion's without --region) and sets Blaze's RegionRoute.
func wireRegions(cfg config, hosts *gmshost.Manager, bz *blaze.Server, log *logging.Log) {
	r := cfg.regions
	if hosts == nil {
		return
	}
	if names, err := r.regionNames(); err == nil { // linkConfig has checked them
		hosts.SetRegionNames(names)
	}
	if r.region == "" {
		return
	}
	machines := map[string]string{}
	for _, s := range r.machines {
		if name, region, _, err := parseHostMachine(s); err == nil { // linkConfig has checked them
			machines[name] = region
		}
	}
	hosts.SetRegions(r.region, machines)
	bz.RegionRoute = regionRoute(hosts, time.Now)
	var named []string
	for _, id := range hosts.Regions() {
		named = append(named, fmt.Sprintf("%s (%s)", id, hosts.RegionName(id)))
	}
	log.Logf("stub", "regions: home %s, games may run in %s (each player's Preferred Region: a custom game its creator's, "+
		"practice its player's, a practice group and PvP by vote; a region that can't take the game falls back home)",
		r.region, strings.Join(named, ", "))
}

// regionRoute is Blaze's RegionRoute: for a game on a TLS-linked machine that offered a match gateway, the gateway, its
// pin and a ticket for (the game's match, user) under the link's ticket key.
func regionRoute(hosts *gmshost.Manager, now func() time.Time) func(blaze.Game, int64) (tunnel.Away, bool) {
	return func(g blaze.Game, user int64) (tunnel.Away, bool) {
		info, ok := hosts.Machine(g.Site)
		if !ok || info.Gateway == "" || len(info.TicketKey) == 0 {
			return tunnel.Away{}, false
		}
		gw, err := netip.ParseAddrPort(info.Gateway)
		if err != nil {
			return tunnel.Away{}, false
		}
		pin, err := tunnel.ParsePin(info.Pin)
		if err != nil {
			return tunnel.Away{}, false
		}
		ticket, err := tunnel.MintTicket(info.TicketKey, tunnel.Ticket{Machine: info.Name, Match: g.Match, User: user, Issued: now()})
		if err != nil {
			return tunnel.Away{}, false
		}
		return tunnel.Away{Gateway: gw, Pin: pin, Ticket: ticket}, true
	}
}

// machineLevels is LinkConfig.Levels: a machine hosts SM_Map_v5 (it ships whole), every level whose archive this
// server has no version of (MAPGUARD's release rule, as for players), and every map of the maps folder whose archive
// it offers in this server's version (MAPED-GUARD); a map it lacks, or has in another version, falls back home ("map").
func machineLevels(versions map[string][32]byte) func(map[string]string) []string {
	return func(offer map[string]string) []string {
		var out []string
		for _, l := range blaze.PlayableLevels {
			arch := strings.ToLower(l.Archive)
			v, versioned := versions[arch]
			if arch == "" || !versioned || strings.EqualFold(offer[arch], hex.EncodeToString(v[:])) {
				out = append(out, l.Name)
			}
		}
		return out
	}
}

// matchGateway is a host machine's match gateway (--agent-tls with --tunnel on): TCP+UDP on --bind:--gateway-port with
// its own key (--gateway-key, default --certs), carrying only the datagrams of tickets' matches to the hosts here
// (--gms-bind, else --bind). It sets the agent's offer (gateway, pin), the ticket key's hook and the relay's annotation.
// nil without --tunnel on.
func matchGateway(cfg config, ag *gmshost.Agent, log *logging.Log, public string) (*tunnel.Gateway, string, error) {
	on, err := tunnelWanted(cfg.tunnel, public)
	if err != nil || !on {
		return nil, "", err
	}
	if cfg.regions.agentTLS == "" {
		return nil, "", errors.New("--tunnel on in agent mode needs --agent-tls (the match gateway's tickets come over the TLS link)")
	}
	pub := net.ParseIP(public).To4()
	hosts := net.ParseIP(cfg.hostsBind()).To4()
	if pub == nil || hosts == nil {
		return nil, "", fmt.Errorf("the match gateway needs IPv4 addresses: --public-host %q, the hosts' %q", public, cfg.hostsBind())
	}
	first, last, err := gmshost.ParsePorts(cfg.gmsPorts)
	if err != nil {
		return nil, "", err
	}
	keyDir := cfg.gatewayKey
	if keyDir == "" {
		keyDir = cfg.certs
	}
	id, created, err := tunnel.LoadOrCreateIdentity(keyDir)
	if err != nil {
		return nil, "", err
	}
	guard := &tunnel.TicketGuard{Machine: ag.Name}
	running := func(match int64) (*net.UDPAddr, bool) {
		port, ok := ag.HostPort(match)
		if !ok || port == 0 {
			return nil, false
		}
		return &net.UDPAddr{IP: hosts, Port: port}, true
	}
	g := &tunnel.Gateway{Log: log, Identity: id, BindIP: hosts, Admit: tunnel.TicketAdmit(guard, running), Running: running}
	limitsNote := "off (loopback and LAN addresses)"
	if cfg.limitsMode.Active(exepatch.IsPublic, cfg.bind, public) {
		lc := limits.Default()
		if cfg.limitsCfg != nil {
			lc = *cfg.limitsCfg
		}
		l := limits.New(lc, nil)
		g.Limits, g.MaxSessionsPerPeer = l, lc.TunnelSessions
		limitsNote = "on: " + lc.Describe()
	}
	for p := first; p <= last; p++ {
		g.ForwardUDP(p)
	}
	if err := g.Start(net.JoinHostPort(cfg.bind, strconv.Itoa(cfg.gatewayPort))); err != nil {
		return nil, "", fmt.Errorf("the match gateway: %w", err)
	}
	_, port, _ := net.SplitHostPort(g.Addr())
	ag.Link.Gateway = net.JoinHostPort(pub.String(), port)
	ag.Link.GatewayPin = id.Pin.String()
	ag.Link.Linked = func(key []byte, mainNow time.Time) { guard.SetKey(key, mainNow) }
	ag.Link.Annotate = gmshost.FlowAnnotator(g.FlowAdmission)
	note := fmt.Sprintf("match gateway on %s (players dial %s, key %s), ports %d-%d to the hosts on %s; limits %s",
		g.Addr(), ag.Link.Gateway, id.Pin.Fingerprint(), first, last, cfg.hostsBind(), limitsNote)
	if created {
		note += "; a new gateway key in " + keyDir
	}
	return g, note, nil
}

// agentPruning is --log-days in agent mode (a host machine): this dgserver's log files and its hosts' folders
// (--gms-data) older than that, at the start and every pruneEvery until stop. The start's note for the log.
func (in *agentInstance) agentPruning(cfg config) string {
	if cfg.logDays <= 0 {
		return ""
	}
	note := fmt.Sprintf("logs older than %d days deleted (--log-days; now: %s)", cfg.logDays,
		describeRemoved(pruneLogs(cfg.logDir, cfg.gmsData, cfg.logDays, time.Now())))
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(pruneEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if removed := pruneLogs(cfg.logDir, cfg.gmsData, cfg.logDays, time.Now()); len(removed) > 0 {
					in.log.Logf("stub", "--log-days %d: %s", cfg.logDays, describeRemoved(removed))
				}
			}
		}
	}()
	in.stopPrune = func() { close(stop); <-done }
	return note
}

// info is Hooks.Started's Info for agent mode: its address, the relay's port and its match gateway's (when it runs
// one), the log.
func (in *agentInstance) info(cfg config) Info {
	inf := Info{Bind: cfg.bind, PublicHost: in.agent.PublicHost, Log: in.log.Path, Version: in.agent.Build}
	if cfg.regions.agentTLS != "" {
		inf.TCPPorts = []int{cfg.gmsControlPort}
	}
	if g := in.gateway; g != nil {
		inf.GatewayPort, inf.GatewayFingerprint, inf.GatewayPin = g.Port(), g.Identity.Pin.Fingerprint(), g.Identity.Pin.String()
		inf.TCPPorts = append(inf.TCPPorts, inf.GatewayPort)
	}
	return inf
}
