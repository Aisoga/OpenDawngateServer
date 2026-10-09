package dgserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/tdf"
)

func TestCheckBind(t *testing.T) {
	for _, ok := range []string{"127.0.0.1", "127.0.0.2", "192.168.1.20"} {
		if err := checkBind(ok); err != nil {
			t.Errorf("checkBind(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "0.0.0.0", "localhost", "::1", "::", "192.168.1"} {
		if err := checkBind(bad); err == nil {
			t.Errorf("checkBind(%q): no error", bad)
		}
	}
}

func TestResolvePublic(t *testing.T) {
	var asked []string
	lookup := func(_ context.Context, network, host string) ([]net.IP, error) {
		asked = append(asked, network+" "+host)
		switch host {
		case "dgserver.test":
			return []net.IP{net.IPv4zero, net.ParseIP("10.1.2.3"), net.ParseIP("10.1.2.4")}, nil
		case "v6only.test":
			return []net.IP{net.ParseIP("fe80::1")}, nil
		}
		return nil, errors.New("no such host")
	}
	for in, want := range map[string]string{"127.0.0.1": "127.0.0.1", "192.168.1.20": "192.168.1.20",
		"::ffff:192.168.1.20": "192.168.1.20", "dgserver.test": "10.1.2.3"} {
		if got, err := resolvePublic(in, lookup); err != nil || got != want {
			t.Errorf("resolvePublic(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if len(asked) != 1 || asked[0] != "ip4 dgserver.test" {
		t.Errorf("lookups %q (want one, for IPv4, and none for addresses)", asked)
	}
	for _, bad := range []string{"", "0.0.0.0", "224.0.0.251", "255.255.255.255", "::1", "unknown.test", "v6only.test"} {
		if got, err := resolvePublic(bad, lookup); err == nil {
			t.Errorf("resolvePublic(%q) = %q, no error", bad, got)
		}
	}
}

func TestAddressNotes(t *testing.T) {
	for _, c := range []struct {
		bind, public, name string
		want               []string // substrings, one per line
	}{
		{"127.0.0.1", "127.0.0.1", "", nil}, // the default: nothing new in the log
		{"192.168.1.20", "192.168.1.20", "", nil},
		{"192.168.1.20", "192.168.1.20", "dgserver.lan", []string{"announcing 192.168.1.20 (dgserver.lan) to clients"}},
		{"192.168.1.20", "203.0.113.5", "", []string{"announcing 203.0.113.5 to clients (redirector reply, QoS ping site, telemetry server, GMS address); listening on 192.168.1.20"}},
		{"127.0.0.1", "192.168.1.20", "", []string{"announcing 192.168.1.20", "note: the announced 192.168.1.20 is not loopback, but every port listens on 127.0.0.1 only"}},
		{"192.168.1.20", "127.0.0.1", "", []string{"announcing 127.0.0.1", "note: listening on 192.168.1.20, but announcing loopback 127.0.0.1"}},
	} {
		got := addressNotes(c.bind, c.public, c.name)
		if len(got) != len(c.want) {
			t.Errorf("%s/%s: %q", c.bind, c.public, got)
			continue
		}
		for i, w := range c.want {
			if !strings.Contains(got[i], w) {
				t.Errorf("%s/%s line %d: %q lacks %q", c.bind, c.public, i, got[i], w)
			}
		}
	}
}

func TestApplyHostFlag(t *testing.T) {
	def := config{bind: "127.0.0.1", publicHost: "127.0.0.1"}
	for _, c := range []struct {
		set          []string
		bind, public string
	}{
		{nil, "127.0.0.1", "127.0.0.1"},
		{[]string{"host"}, "10.0.0.9", "10.0.0.9"},
		{[]string{"host", "bind"}, "127.0.0.1", "10.0.0.9"},
		{[]string{"host", "public-host"}, "10.0.0.9", "127.0.0.1"},
		{[]string{"bind"}, "127.0.0.1", "127.0.0.1"}, // --host's default "" is ignored when it isn't given
	} {
		cfg, set := def, map[string]bool{}
		for _, s := range c.set {
			set[s] = true
		}
		applyHostFlag(&cfg, "10.0.0.9", set)
		if cfg.bind != c.bind || cfg.publicHost != c.public {
			t.Errorf("set %v: bind %q public %q, want %q %q", c.set, cfg.bind, cfg.publicHost, c.bind, c.public)
		}
	}
}

// The whole server with a public host (A5) on ephemeral loopback ports: it binds --bind only, and the
// redirector reply, preAuth's ping site and the QoS firewall reply name the public host. A name is
// resolved once, at startup. (The GameManager and telemetry replies are covered in internal/blaze.)
func TestServerPublicHost(t *testing.T) {
	for _, c := range []struct{ public, want, logLine string }{
		{"192.168.1.20", "192.168.1.20", "[stub] announcing 192.168.1.20 to clients (redirector reply, QoS ping site, telemetry server, GMS address); listening on 127.0.0.1\n"},
		{"dgserver.test", "10.1.2.3", "[stub] announcing 10.1.2.3 (dgserver.test) to clients"},
	} {
		logDir := t.TempDir()
		in, err := start(config{bind: "127.0.0.1", publicHost: c.public, certs: testCerts(t), logDir: logDir,
			lookup: func(_ context.Context, _, host string) ([]net.IP, error) {
				if host == "dgserver.test" {
					return []net.IP{net.ParseIP("10.1.2.3")}, nil
				}
				return nil, errors.New("no such host")
			}})
		if err != nil {
			t.Fatal(err)
		}
		addr := func(i int) string { return fmt.Sprintf("127.0.0.1:%d", in.ports[i]) }
		for i, ln := range in.listeners {
			if h, _, _ := net.SplitHostPort(ln.Addr().String()); h != "127.0.0.1" {
				t.Errorf("listener %d on %s", i, ln.Addr())
			}
		}
		if h := in.qosUDP.LocalAddr().(*net.UDPAddr).IP.String(); h != "127.0.0.1" {
			t.Errorf("QoS UDP on %s", h)
		}

		// redirector: HOST and IP of our Blaze server are the public host, the port is the one we listen on
		tc, err := tls.Dial("tcp", addr(0), &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10,
			MaxVersion: tls.VersionTLS11, CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_256_CBC_SHA}})
		if err != nil {
			t.Fatal(err)
		}
		r := call(t, tc, 5, 1, 0, tdf.NewWriter().String("NAME", "battlegrounds-1-pc").Bytes())
		tc.Close()
		u, _ := r.Get("ADDR")
		valu := u.Value.(*tdf.Union).Field.Value.(*tdf.Struct)
		host, _ := valu.Str("HOST")
		ip, _ := valu.Int("IP")
		port, _ := valu.Int("PORT")
		if host != c.want || uint32(ip) != ipv4(c.want) || int(port) != in.ports[1] {
			t.Errorf("%s: redirector announced %s (%#x):%d", c.public, host, ip, port)
		}

		// preAuth: both ping sites are the public host
		bc, err := net.Dial("tcp", addr(1))
		if err != nil {
			t.Fatal(err)
		}
		pre := call(t, bc, 9, 7, 0, nil)
		bc.Close()
		qoss, _ := pre.Sub("QOSS")
		bwps, _ := qoss.Sub("BWPS")
		if psa, _ := bwps.Str("PSA"); psa != c.want {
			t.Errorf("%s: preAuth BWPS PSA %q", c.public, psa)
		}

		// QoS firewall reply: the client's firewall packets go to the public host
		qc, err := net.Dial("tcp", addr(2))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(qc, "GET /qos/firewall?vers=1&nint=2 HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n")
		qrep, _ := io.ReadAll(qc)
		qc.Close()
		if want := fmt.Sprintf("<ips><ips>%d</ips></ips>", ipv4(c.want)); !strings.Contains(string(qrep), want) {
			t.Errorf("%s: firewall reply lacks %q:\n%s", c.public, want, qrep)
		}
		in.stop()

		files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
		raw, _ := os.ReadFile(files[0])
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		for _, want := range []string{"[stub] redirector on 127.0.0.1:", c.logLine,
			"[stub] note: the announced " + c.want + " is not loopback, but every port listens on 127.0.0.1 only",
			fmt.Sprintf("[redirector] -> getServerInstance reply: %s:%d, SECU=0\n", c.want, in.ports[1])} {
			if !strings.Contains(text, want) {
				t.Errorf("log lacks %q:\n%s", want, text)
			}
		}
	}
	// a bad public host or bind address stops the start
	for _, bad := range []config{{bind: "127.0.0.1", publicHost: "0.0.0.0"}, {bind: "0.0.0.0"},
		{bind: "127.0.0.1", publicHost: "unknown.test", lookup: func(context.Context, string, string) ([]net.IP, error) {
			return nil, errors.New("no such host")
		}}} {
		bad.certs, bad.logDir = testCerts(t), t.TempDir()
		if in, err := start(bad); err == nil {
			in.stop()
			t.Errorf("no error for bind %q public %q", bad.bind, bad.publicHost)
		}
	}
}

// The default setup logs no address notes: the startup log is as before A5.
func TestServerDefaultAddressesUnchanged(t *testing.T) {
	logDir := t.TempDir()
	in, err := start(config{bind: "127.0.0.1", publicHost: "127.0.0.1", certs: testCerts(t), logDir: logDir})
	if err != nil {
		t.Fatal(err)
	}
	in.stop()
	files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
	raw, _ := os.ReadFile(files[0])
	if strings.Contains(string(raw), "announcing") || strings.Contains(string(raw), "note:") {
		t.Errorf("default setup logged address notes:\n%s", raw)
	}
}

func ipv4(s string) uint32 {
	ip := net.ParseIP(s).To4()
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}
