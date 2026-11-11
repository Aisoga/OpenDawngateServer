package dgserver

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/redirector"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/uichannel"
)

// The UI channel through the real tunnel, with no plain listeners (a public server's setup): the gateway lists its port,
// so a Start-Client's tunnel listens on it; a login's tunnel session is who a channel is, whatever name its hello gives,
// so one player can't speak for another; a session without a login is refused; and the channel ends with its login.
func TestUIChannelThroughTunnel(t *testing.T) {
	const srvIP, hostsIP = "127.0.9.40", "127.0.9.41"
	certs := testCerts(t)
	in, err := start(config{bind: srvIP, certs: certs, logDir: t.TempDir(), redirectorPort: redirector.Port, tunnel: TunnelOn,
		plain: PlainOff, gmsBind: hostsIP})
	if err != nil {
		t.Fatal(err)
	}
	defer in.stop()
	tlsCfg, err := redirector.TLSConfig(certs)
	if err != nil {
		t.Fatal(err)
	}
	blazePort, uiPort := in.ports[1], in.ports[uiListener]
	if uiPort != uichannel.Port {
		t.Errorf("the UI channel's port is %d, not %d", uiPort, uichannel.Port)
	}

	// player is one PC: its Start-Client's tunnel on a loopback address of its own
	player := func(tunIP string) func(port int) net.Conn {
		t.Helper()
		c := &tunnel.Client{Gateway: in.gateway.Addr(), Pin: in.gateway.Identity.Pin, TunnelIP: net.ParseIP(tunIP), RedirectorTLS: tlsCfg}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Close)
		tcp, _ := c.Ports()
		if !containsInt(tcp, uiPort) {
			t.Errorf("the tunnel doesn't listen on the UI channel's port %d: %v", uiPort, tcp)
		}
		return func(port int) net.Conn {
			t.Helper()
			conn, err := net.DialTimeout("tcp4", net.JoinHostPort(tunIP, strconv.Itoa(port)), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			return conn
		}
	}
	login := func(dial func(int) net.Conn, email string) (net.Conn, int64) {
		t.Helper()
		bc := dial(blazePort)
		call(t, bc, 1, 0x28, 0, tdf.NewWriter().String("MAIL", email).String("PASS", testPassword).String("TOKN", "").Bytes())
		name := store.PersonaName(email)
		st := call(t, bc, 1, 0x6E, 1, tdf.NewWriter().String("PNAM", name).Bytes())
		id, _ := st.Int("BUID")
		if id == 0 {
			t.Fatalf("%s: no BUID", email)
		}
		return bc, id
	}
	hello := func(dial func(int) net.Conn, name string) (net.Conn, string) {
		t.Helper()
		c := dial(uiPort)
		fmt.Fprintf(c, `{"call":"hello","args":{"protocol":1,"name":%q}}`+"\n", name)
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		line := readLine(t, c)
		if strings.Contains(line, `"event":"hello"`) { // PREF1: the greeting follows
			if g := readLine(t, c); !strings.HasPrefix(g, `{"event":"region.state","data":{"regions":[{"id":"local","name":"Local"}]`) {
				t.Errorf("greeting %s", g)
			}
		}
		return c, line
	}

	alpha, bravo, nobody := player("127.0.9.42"), player("127.0.9.43"), player("127.0.9.44")
	alphaBlaze, alphaID := login(alpha, "alpha@example.com")
	_, bravoID := login(bravo, "bravo@example.com")

	// a session with no login is nobody, even with a real player's name
	if _, line := hello(nobody, "bravo"); !strings.Contains(line, `"event":"refused"`) {
		t.Errorf("a session without a login: %s", line)
	}
	if in.ui.Connected(bravoID) {
		t.Error("a session without a login connected as bravo")
	}
	// alpha's session says it is bravo: it is alpha
	alphaUI, line := hello(alpha, "bravo")
	if want := `{"event":"hello","data":{"protocol":1}}`; line != want {
		t.Errorf("hello: %s, want %s", line, want)
	}
	if !in.ui.Connected(alphaID) || in.ui.Connected(bravoID) {
		t.Errorf("connected: alpha %v, bravo %v", in.ui.Connected(alphaID), in.ui.Connected(bravoID))
	}
	// the server reaches it through the tunnel
	if !in.ui.Send(alphaID, "test", map[string]string{"text": "Grüße ✓"}) {
		t.Error("Send: false")
	}
	alphaUI.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := bufio.NewReader(alphaUI).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"event":"test","data":{"text":"Gr\u00fc\u00dfe \u2713"}}` + "\n"; got != want {
		t.Errorf("event %q, want %q", got, want)
	}
	// bravo connects as itself, so the two channels are apart
	hello(bravo, "")
	if !in.ui.Connected(bravoID) {
		t.Error("bravo didn't connect")
	}
	// alpha's login ends: its channel ends, bravo's stays
	alphaBlaze.Close()
	deadline := time.Now().Add(5 * time.Second)
	for in.ui.Connected(alphaID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if in.ui.Connected(alphaID) {
		t.Error("alpha's channel outlived its login")
	}
	if !in.ui.Connected(bravoID) {
		t.Error("bravo's channel ended with alpha's login")
	}
}

// readLine reads one line from c a byte at a time (no buffer that could take the next line with it), without its end.
func readLine(t *testing.T, c net.Conn) string {
	t.Helper()
	var b []byte
	one := make([]byte, 1)
	for {
		if _, err := c.Read(one); err != nil {
			t.Fatalf("read: %v (so far %q)", err, b)
		}
		if one[0] == '\n' {
			return strings.TrimSpace(string(b))
		}
		b = append(b, one[0])
	}
}
