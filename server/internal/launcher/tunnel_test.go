package launcher

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/certs"
	"dawngate/server/internal/setup/exepatch"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

// The tunnel tests' addresses: the server (as another machine's) and the player's tunnel address; no lane uses them.
const (
	tunnelServer = "127.0.9.40"
	tunnelLocal  = "127.0.9.41"
)

// serveTunnelOn runs dgserver with the tunnel's gateway on address until the test ends; it returns the gateway's
// port and key fingerprint.
func serveTunnelOn(t *testing.T, address string) (int, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "certs")
	if _, err := certs.Make(dir, address); err != nil {
		t.Fatal(err)
	}
	stop, started, done := make(chan struct{}), make(chan dgserver.Info, 1), make(chan int, 1)
	go func() {
		done <- dgserver.Run([]string{"--bind", address, "--certs", dir, "--log", t.TempDir(), "--data", "", "--broadcast-file", "",
			"--tunnel", "on", "--gateway-port", "0"}, dgserver.Hooks{Stop: stop,
			Started: func(i dgserver.Info) { started <- i }, StartFailed: func(error) { started <- dgserver.Info{} }})
	}()
	info := <-started
	if info.GatewayPort == 0 {
		t.Fatal("dgserver didn't start with a gateway")
	}
	t.Cleanup(func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("dgserver didn't stop")
		}
	})
	return info.GatewayPort, info.GatewayFingerprint
}

// Start-Client with the tunnel: no gateway, then a key refused until trusted, the player's y, a known key, a changed
// key, --gateway-pin; the copy patched for the tunnel address with this PC's own tunnel CA; then the tunnel itself
// carries the game's redirector request (TLS 1.1 ended in Start-Client) to the server, whose reply names the tunnel
// address.
func TestStartClientTunnel(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.40's server ports")
	}
	dir := release(t)
	args := func(port int, extra ...string) []string {
		return append([]string{"--non-interactive", "--setup-only", "--tunnel", "on", "--tunnel-address", tunnelLocal,
			"--gateway-port", strconv.Itoa(port)}, append(extra, tunnelServer)...)
	}
	code, out := run(t, RunClient, dir, args(tunnel.DefaultPort)...)
	if code != 1 || !strings.Contains(out, "no Dawngate tunnel answers at "+tunnelServer) {
		t.Fatalf("no gateway: exit %d\n%s", code, out)
	}
	port, fp := serveTunnelOn(t, tunnelServer)
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	store := TrustedGateway(l, tunnelServer)
	code, out = run(t, RunClient, dir, args(port)...)
	if code != 1 || !strings.Contains(out, "tunnel key (fingerprint "+fp+") isn't trusted yet") || exists(store) {
		t.Fatalf("an unconfirmed key: exit %d\n%s", code, out)
	}
	var buf bytes.Buffer
	a := args(port)
	code = RunClient(a[1:], IO{In: strings.NewReader("y\n"), Out: &buf, ExeDir: dir, Interactive: true})
	out = buf.String()
	if code != 0 || !strings.Contains(out, "Its tunnel key fingerprint is\n\n      "+fp) ||
		!strings.Contains(out, "trusting the server's tunnel key "+fp) {
		t.Fatalf("confirmed: exit %d\n%s", code, out)
	}
	exe, _ := os.ReadFile(filepath.Join(l.ClientCopy(), "game", "Dawngate.exe"))
	ca, _ := os.ReadFile(filepath.Join(TunnelCerts(l, tunnelLocal), certs.CAModulusFile))
	if d, _ := exepatch.Describe(exe); d["ca_sha256"] != certs.Fingerprint(ca) || d["redirector"] != tunnelLocal || d["bind"] != tunnelLocal {
		t.Errorf("client exe %v (the tunnel CA %s)", d, certs.Fingerprint(ca))
	}
	code, out = run(t, RunClient, dir, args(port)...)
	if code != 0 || strings.Contains(out, "trusting") {
		t.Errorf("a known key: exit %d\n%s", code, out)
	}
	// the server set up anew: another key is refused until confirmed; --gateway-pin with the right key trusts it
	other := t.TempDir()
	tunnel.WriteIdentity(other)
	otherPin, _ := tunnel.PinFromCertFile(filepath.Join(other, tunnel.CertFile))
	os.WriteFile(store, []byte(otherPin.String()+"\n"), 0o644)
	code, out = run(t, RunClient, dir, args(port)...)
	if code != 1 || !strings.Contains(out, "has another tunnel key ("+fp+") than the one trusted before ("+otherPin.Fingerprint()+")") {
		t.Fatalf("a changed key: exit %d\n%s", code, out)
	}
	code, out = run(t, RunClient, dir, args(port, "--gateway-pin", otherPin.String())...)
	if code != 1 || !strings.Contains(out, "not the one given with --gateway-pin") {
		t.Fatalf("a wrong --gateway-pin: exit %d\n%s", code, out)
	}
	served, err := tunnel.ProbePin(net.JoinHostPort(tunnelServer, strconv.Itoa(port)), 5*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, out = run(t, RunClient, dir, args(port, "--gateway-pin", served.String())...)
	if code != 0 || !strings.Contains(out, "trusting the server's new tunnel key "+fp) {
		t.Fatalf("--gateway-pin: exit %d\n%s", code, out)
	}

	// the tunnel as play() runs it: the redirector request through it
	_, tlsCfg, err := tunnelCertSet(l, tunnelLocal, false)
	if err != nil {
		t.Fatal(err)
	}
	var tb bytes.Buffer
	_, stop, err := startTunnel(IO{Out: &tb}, l, net.JoinHostPort(tunnelServer, strconv.Itoa(port)), served, tunnelLocal, tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if !strings.Contains(tb.String(), "ready, UDP works") {
		t.Errorf("tunnel start: %s", tb.String())
	}
	tc, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(tunnelLocal)}}, "tcp4",
		net.JoinHostPort(tunnelLocal, strconv.Itoa(RedirectorPort)), &tls.Config{InsecureSkipVerify: true,
			MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11, CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_256_CBC_SHA}})
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	if err := certs.VerifyServerCert(tc.ConnectionState().PeerCertificates[0], ca, tunnelLocal); err != nil {
		t.Errorf("the tunnel's redirector certificate doesn't verify as the game checks it: %v", err)
	}
	req, _ := fire.Encode(fire.Frame{Component: 5, Command: 1, Type: fire.Request, Payload: tdf.NewWriter().String("NAME", "battlegrounds-1-pc").Bytes()})
	tc.Write(req)
	tc.SetReadDeadline(time.Now().Add(5 * time.Second))
	f, err := fire.NewReader(tc).Next()
	if err != nil {
		t.Fatal(err)
	}
	st, _ := tdf.Decode(f.Payload)
	u, _ := st.Get("ADDR")
	if host, _ := u.Value.(*tdf.Union).Field.Value.(*tdf.Struct).Str("HOST"); host != tunnelLocal {
		t.Errorf("getServerInstance through the tunnel names %q, want %s", host, tunnelLocal)
	}
	io.Copy(io.Discard, io.LimitReader(tc, 0))
}

func TestTunnelModes(t *testing.T) {
	for _, c := range []struct {
		mode, address string
		want          bool
	}{
		{"auto", "127.0.0.1", false}, {"auto", "192.168.1.20", false}, {"auto", "8.8.8.8", true},
		{"on", "127.0.0.1", true}, {"on", "192.168.1.20", true}, {"off", "8.8.8.8", false},
	} {
		if got := tunnelFor(c.mode, c.address); got != c.want {
			t.Errorf("tunnelFor(%s, %s) = %v", c.mode, c.address, got)
		}
	}
	for in, want := range map[string]string{"": "auto", " On ": "on", "yes": "on", "off": "off", "no": "off"} {
		if got, err := tunnelMode(in); err != nil || got != want {
			t.Errorf("tunnelMode(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := tunnelMode("maybe"); err == nil {
		t.Error("tunnel = maybe accepted")
	}
	if p, err := gatewayPort(""); err != nil || p != 42128 {
		t.Errorf("gatewayPort default %d %v", p, err)
	}
	if _, err := gatewayPort("70000"); err == nil {
		t.Error("gateway_port 70000 accepted")
	}
	scfg := Config{"address": "203.0.113.7", "bind": "192.168.1.20"}
	if got := gatewayAddress("203.0.113.7", 42128, scfg); got != "192.168.1.20:42128" {
		t.Errorf("own server behind a router: %s", got)
	}
	if got := gatewayAddress("203.0.113.9", 42128, scfg); got != "203.0.113.9:42128" {
		t.Errorf("another server: %s", got)
	}
}
