package dgserver

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Addresses (roadmap A5, ADR-2 decision 8). dgserver keeps two addresses apart:
//
//	--bind         where every listener binds (TCP and UDP: redirector, Blaze, QoS, HTTP, telemetry, the
//	               logged GMS port) and what the match hosts bind (DGGMS_BIND). Default 127.0.0.1.
//	--public-host  what the server hands to clients: the redirector's getServerInstance reply (HOST, IP),
//	               preAuth's QoS ping sites (QOSS BWPS/LTPS PSA), postAuth's and getTelemetryServer's
//	               telemetry server (ADRS), the QoS firewall reply (ips) and NotifyGameSetup's GMS address
//	               (HNET EXIP and INIP). Default 127.0.0.1. While it is loopback, the client's own
//	               addresses that NotifyGameSetup echoes (PNET) are forced to loopback too, as since B8;
//	               with a public host they go back as the client sent them (blaze.Server.ClientAddr).
//
// --host (the only address flag before A5) sets both. The defaults keep everything on this machine; a
// LAN setup is an explicit opt-in with both flags (docs/RUNNING_THE_CLIENT.md "Playing across machines
// (A5)"). The match hosts' control channel binds --gms-control-bind (default 127.0.0.1); a LAN address
// there is only for agents on other host machines and needs --agent-key (roadmap H2, checkControlBind).

// lookupIP resolves a host name (net.Resolver.LookupIP's signature, so tests can replace it).
type lookupIP func(ctx context.Context, network, host string) ([]net.IP, error)

// checkBind validates --bind: one IPv4 address. The wildcard is refused: it would also expose the
// ports on every other interface, and the match host can't bind it (gms/engine/gms_host.cpp needs
// a non-zero DGGMS_BIND address).
func checkBind(bind string) error {
	ip := net.ParseIP(bind).To4()
	if ip == nil {
		return fmt.Errorf("--bind must be an IPv4 address: %q", bind)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("--bind %s: bind one address of this machine (127.0.0.1, or its LAN address for clients on other machines), not the wildcard", bind)
	}
	return nil
}

// resolvePublic turns --public-host (an IPv4 address or a host name) into the dotted IPv4 address the
// server hands out. A name is resolved once, at startup, to its first IPv4 address: the redirector reply
// and NotifyGameSetup carry a numeric address, so every reply names the same one.
func resolvePublic(host string, lookup lookupIP) (string, error) {
	if host == "" {
		return "", fmt.Errorf("--public-host is empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		ip4 := ip.To4()
		if ip4 == nil {
			return "", fmt.Errorf("--public-host must be IPv4 (the client's address fields are IPv4): %q", host)
		}
		if !usable(ip4) {
			return "", fmt.Errorf("--public-host %s is not an address a client can connect to", host)
		}
		return ip4.String(), nil
	}
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIP
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ips, err := lookup(ctx, "ip4", host)
	if err != nil {
		return "", fmt.Errorf("--public-host %s: %w", host, err)
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil && usable(ip4) {
			return ip4.String(), nil
		}
	}
	return "", fmt.Errorf("--public-host %s has no usable IPv4 address", host)
}

// usable: an IPv4 address a client can connect to.
func usable(ip4 net.IP) bool {
	return !ip4.IsUnspecified() && !ip4.IsMulticast() && !ip4.Equal(net.IPv4bcast)
}

// addressNotes are the startup log lines about a bind/public pair that differ (none in the default setup).
func addressNotes(bind, public, name string) []string {
	if bind == public && name == "" {
		return nil
	}
	shown := public
	if name != "" {
		shown = fmt.Sprintf("%s (%s)", public, name)
	}
	notes := []string{fmt.Sprintf("announcing %s to clients (redirector reply, QoS ping site, telemetry server, GMS address); listening on %s", shown, bind)}
	bl, pl := net.ParseIP(bind).IsLoopback(), net.ParseIP(public).IsLoopback()
	switch {
	case bl && !pl:
		notes = append(notes, fmt.Sprintf("note: the announced %s is not loopback, but every port listens on %s only, so clients connecting to %s find nothing unless something forwards to us (add --bind)", public, bind, public))
	case !bl && pl:
		notes = append(notes, fmt.Sprintf("note: listening on %s, but announcing loopback %s: clients reaching the redirector at %s are sent to %s, where nothing listens (add --public-host)", bind, public, bind, public))
	}
	return notes
}

// applyHostFlag gives --host's value to --bind and --public-host unless those were set themselves
// (set: the names of the flags on the command line).
func applyHostFlag(cfg *config, host string, set map[string]bool) {
	if !set["host"] {
		return
	}
	if !set["bind"] {
		cfg.bind = host
	}
	if !set["public-host"] {
		cfg.publicHost = host
	}
}
