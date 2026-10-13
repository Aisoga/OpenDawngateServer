package launcher

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/certs"
	"dawngate/server/internal/tunnel"
)

// the encrypted tunnel. Start-Server runs the gateway (tunnel = auto: on for a public address);
// Start-Client tunnels to it (tunnel = auto: for a public server address): the player copy is patched for the tunnel
// address (127.0.0.1, the local profile) with a certificate set of this PC's own (the game's TLS to the redirector
// ends in Start-Client), and the gateway's key is trusted on first use like a server's CA.

// The tunnel's modes in Start-Server.ini and Start-Client.ini (and --tunnel).
const (
	TunnelAuto = "auto"
	TunnelOn   = "on"
	TunnelOff  = "off"
)

// DefaultTunnelAddress is where the client side of the tunnel listens and the player copy connects.
const DefaultTunnelAddress = "127.0.0.1"

// GatewayPinFile is the trusted gateway key's file in Dawngate-Data\servers\<address>\.
const GatewayPinFile = "gateway.pin"

// tunnelMode checks a tunnel = value ("" = auto).
func tunnelMode(v string) (string, error) {
	switch v = strings.ToLower(strings.TrimSpace(v)); v {
	case "":
		return TunnelAuto, nil
	case TunnelAuto, TunnelOn, TunnelOff:
		return v, nil
	case "yes", "true":
		return TunnelOn, nil
	case "no", "false":
		return TunnelOff, nil
	}
	return "", fmt.Errorf("tunnel = %s: use auto, on or off", v)
}

// tunnelFor says whether a server address takes the tunnel in a mode: on always, off never, auto for a public
// address (required there: its other ports aren't forwarded), not on loopback or a LAN.
func tunnelFor(mode, address string) bool {
	return mode == TunnelOn || (mode == TunnelAuto && IsPublicAddress(address))
}

// gatewayPort reads a gateway_port = value ("" = tunnel.DefaultPort).
func gatewayPort(v string) (int, error) {
	if strings.TrimSpace(v) == "" {
		return tunnel.DefaultPort, nil
	}
	p, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("gateway_port = %s: a port number 1-65535", v)
	}
	return p, nil
}

// TrustedGateway is where Start-Client keeps a server's gateway key pin once the player confirmed it.
func TrustedGateway(l setup.Layout, server string) string {
	return filepath.Join(l.Root, "servers", server, GatewayPinFile)
}

// TunnelCerts is the folder of this PC's own redirector certificate set for the tunnel address (CN = that address):
// the game's TLS to the redirector ends in Start-Client with it, so its CA is the one the player copy carries.
func TunnelCerts(l setup.Layout, tunnelIP string) string {
	return filepath.Join(l.Root, "certs", "tunnel-"+tunnelIP)
}

// TrustGateway decides whether to trust the gateway key a server presented (trust on first use, as TrustCA; the
// operator's Start-Server window shows the fingerprint).
func TrustGateway(server string, served tunnel.Pin, o TrustOptions) (Trust, error) {
	return trustOnFirstUse(server, []byte(served.String()+"\n"), func(b []byte) string {
		p, err := tunnel.ParsePin(string(b))
		if err != nil {
			return "(unreadable)"
		}
		return p.Fingerprint()
	}, firstUse{noun: "tunnel key fingerprint", changed: "another tunnel key", short: "tunnel key"}, o)
}

// gatewayAddress is where the tunnel connects for a server: its address, or, when this PC's own Start-Server serves
// that address from another one (a public address behind a router, bind =), the bind address (a router may not take
// its own public address from inside).
func gatewayAddress(server string, port int, scfg Config) string {
	host := server
	if scfg != nil && scfg["address"] == server && scfg["bind"] != "" {
		host = scfg["bind"]
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// trustedGatewayPin probes the gateway and returns its key once trusted: the one stored before, a pin given with
// --gateway-pin (no question), or one the player confirms.
func trustedGatewayPin(o IO, in *bufio.Reader, l setup.Layout, server, gw, pinFlag string, trust bool) (tunnel.Pin, error) {
	served, err := tunnel.ProbePin(gw, 5*time.Second, nil)
	if err != nil {
		return served, fmt.Errorf("no Dawngate tunnel answers at %s: %v\nIs Start-Server.exe running there (its window says "+
			"\"The server runs\" and names the tunnel), and does its router forward TCP and UDP %s to it?", gw, err, portOf(gw))
	}
	if pinFlag != "" {
		want, err := tunnel.ParsePin(pinFlag)
		if err != nil {
			return served, err
		}
		if !tunnel.SamePin(want, served) {
			return served, fmt.Errorf("the server at %s presents the tunnel key %s, not the one given with --gateway-pin (%s)",
				gw, served.Fingerprint(), want.Fingerprint())
		}
		trust = true
	}
	var ask func(string) (bool, error)
	if o.Interactive {
		ask = func(q string) (bool, error) {
			a, err := Prompt(in, o.Out, "\n  "+q)
			return strings.EqualFold(a, "y") || strings.EqualFold(a, "yes"), err
		}
	}
	t, err := TrustGateway(server, served, TrustOptions{Store: TrustedGateway(l, server), Accept: trust, Ask: ask})
	if err != nil {
		return served, err
	}
	switch t {
	case TrustNew:
		fmt.Fprintf(o.Out, "  trusting the server's tunnel key %s from now on (%s)\n", served.Fingerprint(), TrustedGateway(l, server))
	case TrustChanged:
		fmt.Fprintf(o.Out, "  trusting the server's new tunnel key %s\n", served.Fingerprint())
	}
	return served, nil
}

func portOf(hostport string) string {
	_, p, _ := net.SplitHostPort(hostport)
	return p
}

// tunnelCertSet returns this PC's certificate set for the tunnel address (made on first use): the CA modulus the
// player copy gets and the TLS configuration that ends the game's redirector TLS.
func tunnelCertSet(l setup.Layout, tunnelIP string, check bool) ([]byte, *tls.Config, error) {
	dir := TunnelCerts(l, tunnelIP)
	if _, err := certs.Check(dir, tunnelIP); err != nil {
		if check {
			return nil, nil, fmt.Errorf("the tunnel's certificates (%s): %v", dir, err)
		}
		if !errors.Is(err, certs.ErrNoSet) {
			if err := os.RemoveAll(dir); err != nil { // our own set for our own PC: made anew, nobody pinned it
				return nil, nil, err
			}
		}
		if _, err := certs.Make(dir, tunnelIP); err != nil {
			return nil, nil, fmt.Errorf("making the tunnel's certificates in %s: %w", dir, err)
		}
	}
	ca, err := os.ReadFile(filepath.Join(dir, certs.CAModulusFile))
	if err != nil {
		return nil, nil, err
	}
	cfg, err := redirector.TLSConfig(dir)
	if err != nil {
		return nil, nil, err
	}
	return ca, cfg, nil
}

// startTunnel runs the client side of the tunnel for the game: its log in Dawngate-Data\logs\tunnel-<time>.log.
func startTunnel(o IO, l setup.Layout, gw string, pin tunnel.Pin, tunnelIP string, tlsCfg *tls.Config) (*tunnel.Client, func(), error) {
	var log *logging.Log
	var f *os.File
	if err := os.MkdirAll(l.Logs(), 0o755); err == nil {
		if f, err = os.Create(filepath.Join(l.Logs(), "tunnel-"+time.Now().Format("20060102-150405")+".log")); err == nil {
			log = logging.New(f)
		}
	}
	c := &tunnel.Client{Log: log, Gateway: gw, Pin: pin, TunnelIP: net.ParseIP(tunnelIP), RedirectorTLS: tlsCfg}
	closeAll := func() {
		c.Close()
		if f != nil {
			f.Close()
		}
	}
	if err := c.Start(); err != nil {
		if f != nil {
			f.Close()
		}
		return nil, nil, err
	}
	if c.WaitUDP(3 * time.Second) {
		fmt.Fprintf(o.Out, "  tunnel   to %s (key %s): ready, UDP works (%s)\n", gw, pin.Fingerprint(), c.UDPEchoAfter().Round(time.Millisecond))
	} else {
		fmt.Fprintf(o.Out, "  WARNING: the tunnel to %s works for TCP, but no UDP answer came back within 3 s: the matches\n"+
			"  would fail. The server's router must forward UDP %s too (not only TCP).\n", gw, portOf(gw))
	}
	return c, closeAll, nil
}
