package dgserver

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/version"
)

// Host machines on the TLS link (REGION1): the main server names the machines that may register through its tunnel
// gateway (--host-machine NAME=REGION:KEYFILE, each with its own key) and its own region (--region); a host machine
// runs dgserver in agent mode with --agent-tls (the main server's gateway) and --agent-pin (its key's pin), its own
// key in --agent-key, and its hosts' control port on loopback (--gms-control-port), which the agent relays.

// regionFlags are the REGION1 flags (config.regions).
type regionFlags struct {
	region   string     // --region: this server's region
	machines stringList // --host-machine NAME=REGION:KEYFILE, repeatable
	agentTLS string     // --agent-tls: agent mode over the main server's tunnel gateway, its address
	agentPin string     // --agent-pin: the main server's gateway pin (64 hex)

	agentPinCert string // REGION5: --agent-pin-cert, the main server's gateway_cert.pem instead of --agent-pin (test lanes)

	names stringList // PREF1: --region-name ALIAS=NAME, repeatable
}

func addRegionFlags(fs *flag.FlagSet, r *regionFlags) {
	fs.StringVar(&r.region, "region", "", "this server's region, e.g. eu (regions for custom games: the home region of --host-machine's machines)")
	fs.Var(&r.machines, "host-machine", "NAME=REGION:KEYFILE: a host machine that may register through the tunnel gateway (needs --tunnel on), "+
		"with its region and its own key file (--make-agent-key); repeatable")
	fs.StringVar(&r.agentTLS, "agent-tls", "", "agent mode over the TLS link: register with the main server's tunnel gateway at this address (host:port) "+
		"and start match hosts from --gms-dir here; their control port is this machine's loopback --gms-control-port")
	fs.StringVar(&r.agentPin, "agent-pin", "", "with --agent-tls: the main server's gateway key pin (64 hex digits, its banner's)")
	fs.StringVar(&r.agentPinCert, "agent-pin-cert", "", "with --agent-tls instead of --agent-pin: the main server's gateway certificate file "+
		"(gateway_cert.pem; a test lane's main server on the same PC)")
	fs.Var(&r.names, "region-name", "ALIAS=NAME: the name players see for a region (--region's or a --host-machine's; local "+
		"without --region), e.g. na=NA (Virginia); 1-32 characters; repeatable. Default: the alias in capitals")
}

// regionNames reads --region-name: each alias with its name, checked against the server's regions (--region and the
// --host-machine regions; without --region only gmshost.LocalRegion).
func (r regionFlags) regionNames() (map[string]string, error) {
	known := map[string]bool{}
	if r.region == "" {
		known[gmshost.LocalRegion] = true
	} else {
		known[r.region] = true
		for _, s := range r.machines {
			if _, region, _, err := parseHostMachine(s); err == nil {
				known[region] = true
			}
		}
	}
	names := map[string]string{}
	for _, s := range r.names {
		alias, name, ok := strings.Cut(s, "=")
		alias = strings.TrimSpace(alias)
		switch {
		case !ok || !regionRE.MatchString(alias):
			return nil, fmt.Errorf("--region-name %q: want ALIAS=NAME, the alias 2-8 lower-case letters", s)
		case !known[alias]:
			return nil, fmt.Errorf("--region-name %q: %s is no region of this server (%s)", s, alias, strings.Join(sortedKeys(known), ", "))
		case names[alias] != "":
			return nil, fmt.Errorf("--region-name: %s twice", alias)
		}
		if why := gmshost.CheckRegionName(name); why != "" {
			return nil, fmt.Errorf("--region-name %q: %s", s, why)
		}
		names[alias] = name
	}
	return names, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var regionRE = regexp.MustCompile(`^[a-z]{2,8}$`)

// parseHostMachine reads --host-machine's NAME=REGION:KEYFILE (the key file may hold a drive letter's colon).
func parseHostMachine(s string) (name, region, keyFile string, err error) {
	name, rest, ok := strings.Cut(s, "=")
	region, keyFile, ok2 := strings.Cut(rest, ":")
	if !ok || !ok2 || name == "" || keyFile == "" {
		return "", "", "", fmt.Errorf("--host-machine %q: want NAME=REGION:KEYFILE", s)
	}
	if !regionRE.MatchString(region) {
		return "", "", "", fmt.Errorf("--host-machine %q: the region must be 2-8 lower-case letters", s)
	}
	return name, region, keyFile, nil
}

// linkConfig is the host manager's gmshost.LinkConfig from --host-machine (and the server's own --gms-ports and
// host options), with a note for the start-up line; none configured = the zero config.
func (c config) linkConfig() (gmshost.LinkConfig, string, error) {
	r := c.regions
	if r.region != "" && !regionRE.MatchString(r.region) {
		return gmshost.LinkConfig{}, "", fmt.Errorf("--region %q: 2-8 lower-case letters", r.region)
	}
	if _, err := r.regionNames(); err != nil {
		return gmshost.LinkConfig{}, "", err
	}
	if len(r.machines) == 0 {
		return gmshost.LinkConfig{}, "", nil
	}
	if c.tunnel != TunnelOn {
		return gmshost.LinkConfig{}, "", errors.New("--host-machine needs the tunnel gateway (--tunnel on): host machines register through it")
	}
	lc := gmshost.LinkConfig{Machines: map[string]gmshost.HostMachine{}, Version: version.String(), Ports: c.gmsPorts,
		Options: c.gmsOptions, Started: time.Now()}
	var notes []string
	for _, s := range r.machines {
		name, region, keyFile, err := parseHostMachine(s)
		if err != nil {
			return gmshost.LinkConfig{}, "", err
		}
		if _, dup := lc.Machines[name]; dup {
			return gmshost.LinkConfig{}, "", fmt.Errorf("--host-machine %s twice", name)
		}
		key, err := gmshost.ReadAgentKey(keyFile)
		if err != nil {
			return gmshost.LinkConfig{}, "", err
		}
		lc.Machines[name] = gmshost.HostMachine{Region: region, Key: key}
		notes = append(notes, name+" ("+region+")")
	}
	if r.region == "" {
		return gmshost.LinkConfig{}, "", errors.New("--host-machine needs --region (this server's own region: the home of " +
			"every game not wanted elsewhere, and the fallback)")
	}
	lc.Levels = machineLevels(c.mapVersions) // REGION5: regions.go
	sort.Strings(notes)
	return lc, "host machines " + strings.Join(notes, ", ") + " through the gateway", nil
}

// agentsHook is the gateway's Agents hook: the host manager's ServeAgent when host machines may register, else nil.
func agentsHook(cfg config, hosts *gmshost.Manager) func(*tls.Conn, string) {
	if hosts == nil || len(cfg.regions.machines) == 0 {
		return nil
	}
	return hosts.ServeAgent
}

// agentLink sets an agent up for the TLS link (--agent-tls): the main server's gateway and pin, the relay on this
// machine's loopback control port, the release as its build, the host copy's maps, and the hosts' own address as
// the shared address of the main server's host options. Nothing without --agent-tls.
func (c config) agentLink(ag *gmshost.Agent) error {
	r := c.regions
	if r.agentTLS == "" {
		if r.agentPin != "" || r.agentPinCert != "" {
			return errors.New("--agent-pin needs --agent-tls")
		}
		return nil
	}
	if r.agentPin == "" && r.agentPinCert != "" { // REGION5
		pin, err := tunnel.PinFromCertFile(r.agentPinCert)
		if err != nil {
			return fmt.Errorf("--agent-pin-cert: %w", err)
		}
		r.agentPin = pin.String()
	}
	if c.agent != "" {
		return errors.New("--agent and --agent-tls exclude each other (the plain link or the TLS link)")
	}
	host, port, err := net.SplitHostPort(r.agentTLS)
	if n, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
		return fmt.Errorf("--agent-tls %q: want the main server's gateway as host:port", r.agentTLS)
	}
	pin, err := tunnel.ParsePin(r.agentPin)
	if err != nil || pin.IsZero() {
		return fmt.Errorf("--agent-tls needs --agent-pin, the main server's gateway pin (64 hex digits): %q", r.agentPin)
	}
	ag.Server, ag.Build = r.agentTLS, version.String()
	relayHost := "127.0.0.1" // REGION5: --gms-control-bind when it is a loopback address (a test lane's own)
	if ip := net.ParseIP(c.gmsControlBind).To4(); ip != nil && ip.IsLoopback() {
		relayHost = ip.String()
	}
	ag.Link = gmshost.AgentLink{Pin: pin, Relay: net.JoinHostPort(relayHost, strconv.Itoa(c.gmsControlPort)),
		Maps: gmshost.OfferMaps(c.mapVersions), SharedIPs: c.hostsBind()}
	return nil
}
