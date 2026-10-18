package dgserver

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/telemetry"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/web"
)

// The tunnel's modes: --tunnel off (the default), on, or auto (on for a public --public-host,
// Start-Server's rule: required on a public address, off on loopback and LAN).
const (
	TunnelOff  = "off"
	TunnelOn   = "on"
	TunnelAuto = "auto"
)

// --plain's values: the services' own listeners on (the default) or off (only through the tunnel's gateway).
const (
	PlainOn  = "on"
	PlainOff = "off"
)

// tunnelWanted resolves --tunnel for the public host.
func tunnelWanted(mode, public string) (bool, error) {
	switch mode {
	case TunnelOff, "":
		return false, nil
	case TunnelOn:
		return true, nil
	case TunnelAuto:
		ip := net.ParseIP(public).To4()
		return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast(), nil
	}
	return false, fmt.Errorf("--tunnel must be %s, %s or %s, not %q", TunnelOff, TunnelOn, TunnelAuto, mode)
}

// services are the in-process servers the gateway hands its streams and datagrams to.
type services struct {
	rd  *redirector.Server
	bz  *blaze.Server
	qos *web.QoS
	web *web.HTTP
	tel *telemetry.Sink
}

// startGateway runs the tunnel's gateway (--tunnel on): TCP and UDP on --bind:--gateway-port, its key in
// --gateway-key (made at the first start). Streams go to the services in process, QoS datagrams too, and datagrams to
// the GMS ports go to the match hosts on --gms-bind (default --bind; or where Blaze routed the session, an agent's
// machine). With --plain off the gateway's listeners are the services' only ones.
func startGateway(cfg config, log *logging.Log, in *instance, sv services, gmsPort int) (*tunnel.Gateway, string, error) {
	keyDir := cfg.gatewayKey
	if keyDir == "" {
		keyDir = cfg.certs
	}
	id, created, err := tunnel.LoadOrCreateIdentity(keyDir)
	if err != nil {
		return nil, "", err
	}
	// the match hosts' address (--gms-bind, else --bind): the flows to them go there and leave from there
	bind := net.ParseIP(cfg.hostsBind()).To4()
	gmsPorts := map[int]bool{}
	if cfg.gmsDir != "" || cfg.agentKey != "" {
		first, last, err := gmshost.ParsePorts(cfg.gmsPorts)
		if err != nil {
			return nil, "", err
		}
		for p := first; p <= last; p++ {
			gmsPorts[p] = true
		}
	} else if gmsPort > 0 {
		gmsPorts[gmsPort] = true // B8's logger or an external GMS (--gms-external)
	}
	g := &tunnel.Gateway{Log: log, Identity: id, BindIP: bind, AllowAnyClient: cfg.allowAnyClient,
		Forward: func(port int) *net.UDPAddr {
			if gmsPorts[port] {
				return &net.UDPAddr{IP: bind, Port: port}
			}
			return nil
		}}
	// the facts an info hello answers with besides the version: the sign-up mode as it is now (the admin page may
	// change it), which Start-Client's server list shows
	policy := cfg.accounts
	g.Facts = func() map[string]string { return map[string]string{tunnel.FactSignup: policy.SignupFact()} }
	if in.limits != nil { // connections, datagrams and sessions per real address (a nil *Limiter isn't nil here)
		g.Limits, g.MaxSessionsPerPeer = in.limits, in.limits.Config().TunnelSessions
	}
	rp, bp, qp, hp, tp := in.ports[0], in.ports[1], in.ports[2], in.ports[3], in.ports[4]
	go sv.rd.Serve(g.Listener(rp)) // plain: the game's TLS ends in the player's tunnel
	go sv.bz.Serve(g.Listener(bp))
	go sv.qos.Serve(g.Listener(qp))
	go sv.web.Serve(g.Listener(hp))
	go sv.tel.Serve(g.Listener(tp))
	g.HandleUDP(qp, sv.qos.TunnelPacket)
	for p := range gmsPorts {
		g.ForwardUDP(p)
	}
	if err := g.Start(net.JoinHostPort(cfg.bind, strconv.Itoa(cfg.gatewayPort))); err != nil {
		return nil, "", fmt.Errorf("the tunnel gateway: %w", err)
	}
	note := ""
	if created {
		note = fmt.Sprintf("; a new gateway key in %s (players confirm its fingerprint at their first start)", keyDir)
	}
	return g, note, nil
}

// runTunnelClient is dgserver --tunnel-client (a test aid for the lanes; players get the same client from
// Start-Client.exe): the player's side of the tunnel on --tunnel-address, until Ctrl+C or --seconds.
func runTunnelClient(cfg config, seconds int, h Hooks) int {
	pin, err := clientPin(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		return 2
	}
	tlsCfg, err := redirector.TLSConfig(cfg.certs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		return 1
	}
	log, err := logging.Open(cfg.logDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		return 1
	}
	defer log.Close()
	if cfg.tunnelFront != "" {
		if RunFront == nil {
			fmt.Fprintln(os.Stderr, "dgserver: --tunnel-front: this build has no front (cmd/dgserver sets it)")
			return 2
		}
		stop, ports, err := RunFront(FrontConfig{Folder: cfg.tunnelFront, Gateway: cfg.tunnelClient, Pin: pin,
			TunnelAddress: cfg.tunnelAddress, RedirectorTLS: tlsCfg, Log: log, Copy: cfg.frontCopy})
		if err != nil {
			log.Logf("front", "the front didn't start: %v", err)
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			return 1
		}
		if h.Started != nil {
			h.Started(Info{Bind: cfg.tunnelAddress, TCPPorts: ports, Log: log.Path})
		}
		waitForStop(seconds, h.Stop)
		stop()
		return 0
	}
	c := &tunnel.Client{Log: log, Gateway: cfg.tunnelClient, Pin: pin, TunnelIP: net.ParseIP(cfg.tunnelAddress), RedirectorTLS: tlsCfg}
	if err := c.Start(); err != nil {
		log.Logf("tunnel", "the tunnel didn't start: %v", err)
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		return 1
	}
	if c.WaitUDP(3 * time.Second) {
		log.Logf("tunnel", "UDP through the gateway works (keepalive echo %s after the start)", c.UDPEchoAfter().Round(time.Millisecond))
	} else {
		log.Logf("tunnel", "WARNING: no keepalive echo from the gateway within 3 s: UDP doesn't reach %s (matches would fail)", cfg.tunnelClient)
	}
	if h.Started != nil {
		tcp, _ := c.Ports()
		h.Started(Info{Bind: cfg.tunnelAddress, TCPPorts: tcp, Log: log.Path})
	}
	waitForStop(seconds, h.Stop)
	c.Close()
	return 0
}

// FrontConfig is what dgserver --tunnel-front hands RunFront.
type FrontConfig struct {
	Folder        string     // the front's server list and trusted keys (a Start-Client Dawngate-Data folder's layout)
	Gateway       string     // the --tunnel-client gateway (host:port): added to the list, trusted with Pin, preselected
	Pin           tunnel.Pin // its key
	TunnelAddress string     // the loopback address the player copy is patched for
	RedirectorTLS *tls.Config
	Log           *logging.Log
	Copy          string // the player copy the login screen's list goes into ("" = none)
}

// RunFront starts Start-Client's front for dgserver --tunnel-front (the launcher sets it: dgserver can't import the
// launcher, which imports dgserver); it returns its stop function and its TCP ports.
var RunFront func(FrontConfig) (stop func(), tcpPorts []int, err error)

// clientPin is --tunnel-pin (64 hex digits) or the pin of --tunnel-pin-cert (the gateway's certificate file).
func clientPin(cfg config) (tunnel.Pin, error) {
	switch {
	case cfg.tunnelPin != "":
		return tunnel.ParsePin(cfg.tunnelPin)
	case cfg.tunnelPinCert != "":
		return tunnel.PinFromCertFile(cfg.tunnelPinCert)
	}
	return tunnel.Pin{}, errors.New("--tunnel-client needs the gateway's key: --tunnel-pin <64 hex digits> or --tunnel-pin-cert <its gateway_cert.pem>")
}
