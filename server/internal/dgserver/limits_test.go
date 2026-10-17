package dgserver

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"dawngate/server/internal/limits"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/tunnel"
)

func serverLog(t *testing.T, logDir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
	if len(files) == 0 {
		t.Fatal("no log file")
	}
	raw, _ := os.ReadFile(files[0])
	return string(raw)
}

// --limits on wraps every listener (open connections per address over all of them together), asks the match
// hosts for net_limits=1 and says so at the start; auto on loopback leaves everything as it was.
func TestLimitsOn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dawngate.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	lc := limits.Default()
	lc.OpenPerAddr = 2
	logDir := t.TempDir()
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: logDir, gmsDir: dir, gmsPorts: "20044-20045",
		gmsOptions: "fog=0", limitsMode: limits.On, limitsCfg: &lc})
	if err != nil {
		t.Fatal(err)
	}
	if in.limits == nil || in.stopLimits == nil {
		t.Fatal("no limiter")
	}
	dial := func(i int) (net.Conn, error) {
		// from 127.0.9.1, an address of the test's own (no address is exempt; the server's own included)
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 9, 1)}}
		c, err := d.Dial("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(in.ports[i])))
		if err == nil {
			t.Cleanup(func() { c.Close() })
		}
		return c, err
	}
	for _, i := range []int{0, 4} { // the redirector (no TLS handshake yet) and telemetry hold the address's two slots
		if _, err := dial(i); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for in.limits.Open("127.0.9.1") < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// Linux may deliver the server's close as a reset before Dial returns: refused as well
	if b, err := dial(1); err == nil {
		b.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := b.Read(make([]byte, 1)); err == nil {
			t.Error("a third connection from the address was served")
		} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Error("a third connection from the address wasn't closed")
		}
	} else if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatal(err)
	}
	in.stop()
	log := serverLog(t, logDir)
	for _, want := range []string{
		"host options fog=0;net_limits=1;net_limit_shared_ips=127.0.0.1",
		"; tunnelled players by their real address (the gateway's connections, datagrams and sessions, Blaze's logins)",
		"; tunnel per address 16 sessions, 400/s (burst 1200) datagrams and 256/s (burst 1024) KB/s",
		"limits (PH6): on (--limits on): per address 1/s (burst 30) new and 2 open TCP connections",
		"; match hosts net_limits=1",
		"[blaze] limits: blaze connection from 127.0.9.1 refused: 2 connections open from 127.0.9.1",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}

	logDir = t.TempDir()
	in, err = start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: logDir})
	if err != nil {
		t.Fatal(err)
	}
	if in.limits != nil || in.stopLimits != nil {
		t.Error("limits on loopback with auto")
	}
	in.stop()
	if log := serverLog(t, logDir); !strings.Contains(log, "limits (PH6): off (loopback and LAN addresses; --limits on forces them)") {
		t.Errorf("log:\n%s", log)
	}
	if code := Run([]string{"--limits", "sometimes"}, Hooks{}); code != 2 {
		t.Errorf("--limits sometimes: exit %d", code)
	}
}

func TestWithOption(t *testing.T) {
	for _, c := range [][2]string{
		{"", "net_limits=1"},
		{"fog=0", "fog=0;net_limits=1"},
		{"fog=0; ", "fog=0;net_limits=1"},
		{"fog=0; NET_LIMITS = 0", "fog=0; NET_LIMITS = 0"},
	} {
		if got := withOption(c[0], "net_limits", "1"); got != c[1] {
			t.Errorf("withOption(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

// with the tunnel on, the gateway takes its connections and datagrams through the server's limiter by the
// player's real address; a raw flood of the gateway port from one address is reset, a player from another address
// still gets its session; the server's own --bind is no longer exempt.
func TestLimitsTunnelGateway(t *testing.T) {
	const srvIP, floodIP, playerIP, tunIP = "127.0.9.40", "127.0.9.41", "127.0.9.42", "127.0.9.43"
	lc := limits.Default()
	lc.Accept = limits.Rate{PerSec: 0.001, Burst: 3}
	logDir := t.TempDir()
	in, err := start(config{bind: srvIP, certs: testCerts(t), logDir: logDir, tunnel: TunnelOn, limitsMode: limits.On,
		limitsCfg: &lc})
	if err != nil {
		t.Fatal(err)
	}
	if in.gateway == nil || in.gateway.Limits == nil || in.gateway.MaxSessionsPerPeer != lc.TunnelSessions {
		t.Fatalf("gateway %+v", in.gateway)
	}
	refused := 0
	for i := 0; i < 8; i++ {
		c, err := (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(floodIP)}}).Dial("tcp4", in.gateway.Addr())
		if err != nil {
			refused++
			continue
		}
		c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if _, err := c.Read(make([]byte, 1)); err != nil {
			if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
				refused++ // reset at once; an admitted one waits for its TLS hello (the deadline)
			}
		}
		c.Close()
	}
	if refused < 5 {
		t.Errorf("%d of 8 flood connections refused, want at least 5 past the burst of 3", refused)
	}
	tlsCfg, err := redirector.TLSConfig(testCerts(t))
	if err != nil {
		t.Fatal(err)
	}
	c := &tunnel.Client{Gateway: in.gateway.Addr(), Pin: in.gateway.Identity.Pin, TunnelIP: net.ParseIP(tunIP),
		DialIP: net.ParseIP(playerIP), RedirectorTLS: tlsCfg}
	if err := c.Start(); err != nil {
		t.Fatalf("a player from another address after the flood: %v", err)
	}
	c.Close()
	if in.limits.Open(srvIP) != 0 {
		t.Error("connections counted for the server's own address")
	}
	in.stop()
	log := serverLog(t, logDir)
	for _, want := range []string{
		"[tunnel] limits: tunnel connection from " + floodIP + " refused: new connections from " + floodIP + " over",
		"tunnelled players by their real address",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "connection from "+playerIP+" refused") {
		t.Errorf("the player was refused:\n%s", log)
	}
}
