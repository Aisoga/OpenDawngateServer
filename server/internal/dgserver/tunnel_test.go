package dgserver

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gms"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

func TestTunnelWanted(t *testing.T) {
	for _, c := range []struct {
		mode, public string
		want         bool
	}{
		{"off", "203.0.113.5", false}, {"", "203.0.113.5", false}, {"on", "127.0.0.1", true}, {"on", "192.168.1.2", true},
		{"auto", "127.0.0.1", false}, {"auto", "192.168.20.40", false}, {"auto", "10.1.2.3", false},
		{"auto", "203.0.113.5", true}, {"auto", "93.184.216.34", true},
	} {
		if got, err := tunnelWanted(c.mode, c.public); err != nil || got != c.want {
			t.Errorf("tunnelWanted(%q, %q) = %v, %v; want %v", c.mode, c.public, got, err, c.want)
		}
	}
	if _, err := tunnelWanted("yes", "127.0.0.1"); err == nil {
		t.Error("--tunnel yes accepted")
	}
	c := config{redirectorPort: 1, blazePort: 2, qosPort: 3, httpPort: 4, telemetryPort: 5, gmsControlPort: 6, gatewayPort: 7,
		tunnel: TunnelOn, gmsDir: "x"}
	if got := fmt.Sprint(c.printPorts()); got != "[1 2 3 4 5 6 7]" {
		t.Errorf("--print-ports with the tunnel: %s", got)
	}
	c.tunnelClient = "127.0.0.9:42128"
	if got := fmt.Sprint(c.printPorts()); got != "[1 2 3 4 5]" {
		t.Errorf("--print-ports of --tunnel-client: %s", got)
	}
}

// TestTunnelThroughServer runs the whole server with --tunnel on (on 127.0.9.30, as the server's machine) and the
// client side of the tunnel on 127.0.9.31 (the player's loopback address), then does what the game does through it:
// the redirector over TLS 1.1 (ended in the client), Blaze's preAuth, the QoS ping site's latency probe and NAT test,
// and a GMS datagram. Every address the server hands out is the tunnel address; the NAT type is OPEN. The second run
// is Start-Server's with the tunnel: --plain off (no listener of the services' own: the same through the gateway alone)
// and --gms-bind (the GMS datagrams go to, and come from, the hosts' own address).
func TestTunnelThroughServer(t *testing.T) {
	for _, plainOff := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "tunnel only"}[plainOff], func(t *testing.T) { tunnelThroughServer(t, plainOff) })
	}
}

func tunnelThroughServer(t *testing.T, plainOff bool) {
	const srvIP, tunIP, hostsIP = "127.0.9.30", "127.0.9.31", "127.0.9.32"
	logDir := t.TempDir()
	certs := testCerts(t)
	cfg := config{bind: srvIP, certs: certs, logDir: logDir, redirectorPort: redirector.Port, tunnel: TunnelOn}
	gmsFrom := srvIP
	if plainOff {
		cfg.plain, cfg.gmsBind, gmsFrom = PlainOff, hostsIP, hostsIP
	}
	in, err := start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer in.stop()
	if plainOff {
		// nothing of the services' own on the server's address: every port free there, TCP and QoS's UDP
		if len(in.listeners) != 0 || in.qosUDP != nil {
			t.Errorf("--plain off bound listeners: %d, QoS UDP %v", len(in.listeners), in.qosUDP != nil)
		}
		if got := fmt.Sprint(in.ports); got != "[42127 10041 17502 18080 9988]" {
			t.Errorf("the gateway's service ports: %s", got)
		}
		if held := heldOn(srvIP, in.ports); len(held) > 0 {
			t.Errorf("--plain off: ports held on %s: %v", srvIP, held)
		}
		if info := in.info(cfg); fmt.Sprint(info.TCPPorts) != fmt.Sprint([]int{in.gateway.Port()}) {
			t.Errorf("--plain off: Info.TCPPorts %v, want only the gateway's", info.TCPPorts)
		}
		if a := in.gmsUDP.LocalAddr().(*net.UDPAddr); !a.IP.Equal(net.ParseIP(hostsIP)) {
			t.Errorf("the GMS port binds %v, not --gms-bind", a)
		}
	}
	if in.gateway == nil {
		t.Fatal("no gateway")
	}
	if _, err := os.Stat(filepath.Join(certs, tunnel.KeyFile)); err != nil {
		t.Errorf("the gateway key wasn't written next to the certificates: %v", err)
	}
	info := in.info(config{bind: srvIP})
	if info.GatewayPort != in.gateway.Port() || len(info.GatewayFingerprint) != 16 {
		t.Errorf("info %+v", info)
	}
	tlsCfg, err := redirector.TLSConfig(certs)
	if err != nil {
		t.Fatal(err)
	}
	c := &tunnel.Client{Gateway: in.gateway.Addr(), Pin: in.gateway.Identity.Pin, TunnelIP: net.ParseIP(tunIP), RedirectorTLS: tlsCfg}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.WaitUDP(3 * time.Second) {
		t.Fatal("no UDP echo")
	}
	at := func(port int) string { return net.JoinHostPort(tunIP, strconv.Itoa(port)) }
	dial := func(port int) net.Conn {
		conn, err := (&net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(tunIP)}}).Dial("tcp4", at(port)) // as the game: bound to its address
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}

	// the redirector: TLS 1.1 as the game speaks it, to the tunnel; the reply names the tunnel address
	tc, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(tunIP)}}, "tcp4", at(redirector.Port), &tls.Config{InsecureSkipVerify: true,
		MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11, CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_256_CBC_SHA}})
	if err != nil {
		t.Fatal(err)
	}
	r := call(t, tc, 5, 1, 0, tdf.NewWriter().String("NAME", "battlegrounds-1-pc").Bytes())
	tc.Close()
	u, _ := r.Get("ADDR")
	valu := u.Value.(*tdf.Union).Field.Value.(*tdf.Struct)
	if host, _ := valu.Str("HOST"); host != tunIP {
		t.Errorf("redirector: Blaze host %q, want the tunnel address %s", host, tunIP)
	}
	if port, _ := valu.Int("PORT"); int(port) != in.ports[1] {
		t.Errorf("redirector: Blaze port %d, want %d", port, in.ports[1])
	}

	// Blaze: the ping site is the tunnel address
	bc := dial(in.ports[1])
	pre := call(t, bc, 9, 7, 0, nil)
	bc.Close()
	qoss, _ := pre.Sub("QOSS")
	bwps, _ := qoss.Sub("BWPS")
	if psa, _ := bwps.Str("PSA"); psa != tunIP {
		t.Errorf("preAuth: ping site %q, want %s", psa, tunIP)
	}

	// QoS latency probe: the echo names the game socket's own address, as for a local client
	game, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(tunIP)})
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	qosAt := &net.UDPAddr{IP: net.ParseIP(tunIP), Port: in.ports[2]}
	game.WriteToUDP([]byte{0, 0, 0, 2, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 42}, qosAt)
	game.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, from, err := game.ReadFromUDP(buf)
	if err != nil || n != 30 {
		t.Fatalf("latency echo: %d bytes, %v", n, err)
	}
	me := game.LocalAddr().(*net.UDPAddr)
	if !net.IP(buf[20:24]).Equal(me.IP) || int(binary.BigEndian.Uint16(buf[24:])) != me.Port || from.Port != in.ports[2] {
		t.Errorf("latency echo names %v:%d (from %v), want %v", net.IP(buf[20:24]), binary.BigEndian.Uint16(buf[24:]), from, me)
	}

	// the NAT test: the firewall reply names the tunnel address; the packet comes through; OPEN
	qc := dial(in.ports[2])
	fmt.Fprint(qc, "GET /qos/firewall?vers=1&nint=2 HTTP/1.1\r\nHost: x\r\n\r\n")
	fw, _ := io.ReadAll(qc)
	qc.Close()
	tip := net.ParseIP(tunIP).To4()
	if want := fmt.Sprintf("<ips><ips>%d</ips></ips>", binary.BigEndian.Uint32(tip)); !strings.Contains(string(fw), want) {
		t.Fatalf("firewall reply lacks %q:\n%s", want, fw)
	}
	id := between(string(fw), "<requestid>", "</requestid>")
	secret := between(string(fw), "<reqsecret>", "</reqsecret>")
	qc = dial(in.ports[2])
	fmt.Fprintf(qc, "GET /qos/firetype?vers=1&rqid=%s&rqsc=%s&inip=%d&inpt=%d HTTP/1.1\r\nHost: x\r\n\r\n", id, secret,
		binary.BigEndian.Uint32(tip), me.Port)
	idn, _ := strconv.ParseUint(id, 10, 32)
	sec, _ := strconv.ParseInt(secret, 10, 64)
	pkt := binary.BigEndian.AppendUint32(nil, uint32(idn))
	pkt = binary.BigEndian.AppendUint32(pkt, uint32(sec))
	time.Sleep(100 * time.Millisecond)
	game.WriteToUDP(pkt, qosAt)
	ft, _ := io.ReadAll(bufio.NewReader(qc))
	qc.Close()
	if !strings.Contains(string(ft), "<firetype><firetype>1</firetype></firetype>") {
		t.Errorf("firetype through the tunnel isn't OPEN:\n%s", ft)
	}

	// a GMS datagram reaches the GMS port (B8's logger here; a match host otherwise) from the server's own address
	gmsPort := in.gmsUDP.LocalAddr().(*net.UDPAddr).Port
	_, gmsListed := c.Ports()
	if !containsInt(gmsListed, gmsPort) {
		t.Errorf("the client doesn't listen on the GMS port %d: %v", gmsPort, gmsListed)
	}
	game.WriteToUDP(append(append([]byte{0x05}, gms.OfflineMagic...), 6, 0, 0, 0), &net.UDPAddr{IP: net.ParseIP(tunIP), Port: gmsPort})
	time.Sleep(300 * time.Millisecond)

	text := logText(t, logDir)
	for _, want := range []string{
		"tunnel gateway on " + srvIP + ":",
		"; a new gateway key in ",
		"tunnel address " + tunIP,
		"[redirector] connection from ('" + tunIP + "', ",
		" through the tunnel (its TLS ended in the player's tunnel)",
		fmt.Sprintf("[redirector] -> getServerInstance reply: %s:%d, SECU=0", tunIP, in.ports[1]),
		"[qos] udp ('" + tunIP + "', ",
		"[gms] udp ('" + gmsFrom + "', ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("log lacks %q:\n%s", want, text)
		}
	}
	if plainOff && !strings.Contains(text, "no plain listeners (--plain off): redirector 42127, Blaze 10041, QoS 17502") {
		t.Errorf("the start-up line doesn't say the services are only the gateway's:\n%s", text)
	}
}

// heldOn lists the TCP ports of ps and UDP port ps[2] (QoS) something holds on address.
func heldOn(address string, ps []int) []string {
	var held []string
	for i, p := range ps {
		if ln, err := net.Listen("tcp4", net.JoinHostPort(address, strconv.Itoa(p))); err != nil {
			held = append(held, fmt.Sprintf("TCP %d", p))
		} else {
			ln.Close()
		}
		if i == qosListener {
			if c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(address), Port: p}); err != nil {
				held = append(held, fmt.Sprintf("UDP %d", p))
			} else {
				c.Close()
			}
		}
	}
	return held
}

// --plain off needs the tunnel, and --plain and --gms-bind take only what they should; --print-ports leaves the
// services' ports out without plain listeners.
func TestPlainOff(t *testing.T) {
	if _, err := start(config{bind: "127.0.9.33", certs: testCerts(t), logDir: t.TempDir(), plain: PlainOff, tunnel: TunnelOff}); err == nil ||
		!strings.Contains(err.Error(), "--plain off needs the tunnel") {
		t.Errorf("--plain off without the tunnel: %v", err)
	}
	if _, err := start(config{bind: "127.0.9.33", certs: testCerts(t), logDir: t.TempDir(), gmsBind: "0.0.0.0", tunnel: TunnelOn}); err == nil ||
		!strings.Contains(err.Error(), "--gms-bind") {
		t.Errorf("--gms-bind 0.0.0.0: %v", err)
	}
	if code := Main([]string{"--plain", "maybe", "--print-ports"}); code != 2 {
		t.Errorf("--plain maybe: exit %d", code)
	}
	c := config{redirectorPort: 1, blazePort: 2, qosPort: 3, httpPort: 4, telemetryPort: 5, gmsControlPort: 6, gatewayPort: 7,
		tunnel: TunnelOn, gmsDir: "x", plain: PlainOff}
	if got := fmt.Sprint(c.printPorts()); got != "[6 7]" {
		t.Errorf("--print-ports with --plain off: %s", got)
	}
	if c.hostsBind() != "" || (config{bind: "127.0.9.33"}).hostsBind() != "127.0.9.33" || (config{bind: "a", gmsBind: "b"}).hostsBind() != "b" {
		t.Error("hostsBind")
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	if j := strings.Index(s, b); j >= 0 {
		return s[:j]
	}
	return ""
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func logText(t *testing.T, dir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	if len(files) != 1 {
		t.Fatalf("log files: %v", files)
	}
	raw, _ := os.ReadFile(files[0])
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}
