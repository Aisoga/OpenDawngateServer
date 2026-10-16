package launcher

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/certs"
	"dawngate/server/internal/setup/exepatch"
	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/telemetry"
	"dawngate/server/internal/tunnel"
)

// The front tests' addresses: two servers (another machine's each), the player's tunnel address, an address with
// nothing on it, one with a server that runs no tunnel, one with a bare gateway. No lane uses them.
const (
	frontServerA   = "127.0.9.50"
	frontServerB   = "127.0.9.51"
	frontLocal     = "127.0.9.52"
	frontNothing   = "127.0.9.53"
	frontNoGateway = "127.0.9.54"
	frontGateway   = "127.0.9.55"
)

// serveTunnelLogged runs dgserver with the tunnel's gateway on address (and the flags extra) until the test ends; it
// returns the gateway's port and the log folder.
func serveTunnelLogged(t *testing.T, address string, extra ...string) (int, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "certs")
	if _, err := certs.Make(dir, address); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	stop, started, done := make(chan struct{}), make(chan dgserver.Info, 1), make(chan int, 1)
	go func() {
		done <- dgserver.Run(append([]string{"--bind", address, "--certs", dir, "--log", logDir, "--data", "", "--broadcast-file", "",
			"--tunnel", "on", "--gateway-port", "0"}, extra...), dgserver.Hooks{Stop: stop,
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
	return info.GatewayPort, logDir
}

// readList is the server list's text in a player copy's list archive.
func readList(t *testing.T, copyDir string) string {
	t.Helper()
	a, err := snap.Open(filepath.Join(copyDir, filepath.FromSlash(ListArchive)))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Entries) != 1 || a.Entries[0].Name != ListPath {
		t.Fatalf("the list archive: %+v", a.Entries)
	}
	b, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// readLogs is the text of every log file in dir.
func readLogs(t *testing.T, dir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	var sb strings.Builder
	for _, f := range files {
		b, _ := os.ReadFile(f)
		sb.Write(b)
	}
	return strings.ReplaceAll(sb.String(), "\r\n", "\n")
}

// waitLog waits until the logs in dir hold want n times (n = 0: at least once); it fails the test otherwise.
func waitLog(t *testing.T, dir, want string, n int) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		text := readLogs(t, dir)
		c := strings.Count(text, want)
		if (n == 0 && c > 0) || (n > 0 && c == n) {
			return text
		}
		if time.Now().After(deadline) {
			t.Fatalf("the log in %s holds %q %d times, want %d:\n%s", dir, want, c, n, text)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// fakeGame is the game's side of the front: its connections from the tunnel address (ip; "" = frontLocal).
type fakeGame struct {
	t  *testing.T
	ip string
}

func (g fakeGame) local() string {
	if g.ip != "" {
		return g.ip
	}
	return frontLocal
}

func (g fakeGame) dialer() *net.Dialer {
	return &net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(g.local())}}
}

func (g fakeGame) dial(port int) net.Conn {
	g.t.Helper()
	c, err := g.dialer().Dial("tcp4", net.JoinHostPort(g.local(), strconv.Itoa(port)))
	if err != nil {
		g.t.Fatal(err)
	}
	return c
}

// redirect asks the front's redirector for the Blaze address, as the game does at its start and after a logout.
func (g fakeGame) redirect() (string, int) {
	g.t.Helper()
	tc, err := tls.DialWithDialer(g.dialer(), "tcp4", net.JoinHostPort(g.local(), strconv.Itoa(RedirectorPort)), &tls.Config{
		InsecureSkipVerify: true, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11, CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_256_CBC_SHA}})
	if err != nil {
		g.t.Fatal(err)
	}
	defer tc.Close()
	f := g.call(tc, 5, 1, 0, tdf.NewWriter().String("NAME", "battlegrounds-1-pc").Bytes())
	st, _ := tdf.Decode(f.Payload)
	u, _ := st.Get("ADDR")
	v := u.Value.(*tdf.Union).Field.Value.(*tdf.Struct)
	host, _ := v.Str("HOST")
	port, _ := v.Int("PORT")
	return host, int(port)
}

// send writes a request.
func (g fakeGame) send(c net.Conn, comp, cmd uint16, id uint32, payload []byte) {
	g.t.Helper()
	out, _ := fire.Encode(fire.Frame{Component: comp, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
	if _, err := c.Write(out); err != nil {
		g.t.Fatal(err)
	}
}

// call sends a request and returns its reply or error reply (notifications are skipped).
func (g fakeGame) call(c net.Conn, comp, cmd uint16, id uint32, payload []byte) fire.Frame {
	g.t.Helper()
	g.send(c, comp, cmd, id, payload)
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	r := bufio.NewReader(c)
	for {
		_, f, err := readFrame(r)
		if err != nil {
			g.t.Fatalf("waiting for the reply to %d/%#x id %d: %v", comp, cmd, id, err)
		}
		if f.ID == id && f.Component == comp && f.Command == cmd && (f.Type == fire.Reply || f.Type == fire.ErrorReply) {
			if r.Buffered() > 0 {
				g.t.Fatalf("frames after the reply to %d/%#x were read too: %d bytes", comp, cmd, r.Buffered())
			}
			return f
		}
	}
}

// start is the client's start on a Blaze connection: preAuth (id 0) and ping (id 1), answered by the front.
func (g fakeGame) start(c net.Conn) {
	g.t.Helper()
	pre := g.call(c, 9, 7, 0, tdf.NewWriter().Struct("CDAT", func(w *tdf.Writer) { w.String("SVCN", "battlegrounds-1-pc") }).Bytes())
	st, _ := tdf.Decode(pre.Payload)
	qoss, _ := st.Sub("QOSS")
	bw, _ := qoss.Sub("BWPS")
	if psa, _ := bw.Str("PSA"); psa != g.local() || pre.Type != fire.Reply {
		g.t.Fatalf("the front's preAuth: ping site %q, type %v", psa, pre.Type)
	}
	if psp, _ := bw.Int("PSP"); psp != int64(DefaultFrontPorts.QoS) {
		g.t.Fatalf("the front's preAuth: QoS port %d", psp)
	}
	if p := g.call(c, 9, 2, 1, nil); p.Type != fire.Reply {
		g.t.Fatalf("the front's ping: %v", p.Type)
	}
}

func loginBody(mail string) []byte {
	return tdf.NewWriter().String("MAIL", mail).String("PASS", "dawngate1").String("TOKN", "").Int("TYPE", 0).Bytes()
}

// telemetryPacket is an '&lgr' packet with a body the sink logs.
func telemetryPacket(body string) []byte {
	p := make([]byte, telemetry.HeaderSize, telemetry.HeaderSize+len(body)+1)
	binary.BigEndian.PutUint32(p[0:], telemetry.KindLogin)
	p = append(p, body...)
	p = append(p, 0)
	binary.BigEndian.PutUint32(p[8:], uint32(len(p)))
	return p
}

// latencyProbe sends a QoS latency probe from the game's QoS socket and waits for the echo.
func latencyProbe(t *testing.T, game *net.UDPConn) {
	t.Helper()
	game.WriteToUDP([]byte{0, 0, 0, 2, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 42}, &net.UDPAddr{IP: net.ParseIP(frontLocal), Port: DefaultFrontPorts.QoS})
	game.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	if n, _, err := game.ReadFromUDP(buf); err != nil || n != 30 {
		t.Fatalf("latency echo: %d bytes, %v", n, err)
	}
}

// The server switch: the front answers the game until a login, then passes it to the picked server through the
// tunnel; a logout and a login with another tag reach the other server; a lost connection's resumeSession goes to
// the current one; an unknown tag, a server that doesn't answer, one without a tunnel and a changed key are the
// front's error replies; telemetry and QoS follow the session; the login screen's list shows each server's state;
// the picker's add (<name>#+<address>[=<name>]) and remove (<name>#-<tag>) go through the login.
func TestFrontServerSwitch(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.50-55's ports")
	}
	portA, logA := serveTunnelLogged(t, frontServerA)
	portB, logB := serveTunnelLogged(t, frontServerB)
	// a server without the tunnel: something at the redirector's port only
	plain, err := net.Listen("tcp4", net.JoinHostPort(frontNoGateway, strconv.Itoa(RedirectorPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	go func() {
		for {
			c, err := plain.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	l := setup.Layout{Root: t.TempDir()}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []Bookmark{{Name: "Server A", Address: frontServerA, GatewayPort: portA}, {Address: frontServerB, GatewayPort: portB},
		{Tag: "down", Address: frontNothing}, {Tag: "nogw", Address: frontNoGateway}} {
		if _, err := bm.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(bm.sortedTags(), ","); got != "a,b,down,nogw" {
		t.Fatalf("tags %s", got)
	}
	_, tlsCfg, err := tunnelCertSet(l, frontLocal, false)
	if err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	lf, err := os.Create(filepath.Join(logDir, "front.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	flog := logging.New(lf)
	f := &Front{Log: flog, Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(frontLocal), RedirectorTLS: tlsCfg, ConnectTimeout: 3 * time.Second}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g := fakeGame{t: t}

	// before a login: the redirector, preAuth, ping, a keep-alive ping, telemetry and QoS are the front's
	if host, port := g.redirect(); host != frontLocal || port != DefaultFrontPorts.Blaze {
		t.Fatalf("the front's redirector names %s:%d", host, port)
	}
	tel := g.dial(DefaultFrontPorts.Telemetry)
	defer tel.Close()
	tel.Write(telemetryPacket("T=first"))
	waitLog(t, logDir, "[telemetry] ('"+frontLocal, 0)
	qosGame, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(frontLocal)})
	if err != nil {
		t.Fatal(err)
	}
	defer qosGame.Close()
	latencyProbe(t, qosGame)
	waitLog(t, logDir, "[qos] udp ('"+frontLocal, 0)

	c1 := g.dial(DefaultFrontPorts.Blaze)
	g.start(c1)
	if p := g.call(c1, 9, 2, 2, nil); p.Type != fire.Reply {
		t.Fatal("keep-alive ping")
	}
	// login to A: its key is new, so the first login stops (confirm once) and the list shows the key; nothing is
	// trusted and nothing reaches A's Blaze yet
	if rep := g.call(c1, 1, 0x28, 3, loginBody("alex#a")); rep.Type != fire.ErrorReply || rep.Error != FrontErrNewKey {
		t.Fatalf("the first login to A: %v error %#x, want error %#x\n%s", rep.Type, rep.Error, FrontErrNewKey, readLogs(t, logDir))
	}
	if _, err := os.Stat(TrustedGateway(l, frontServerA)); err == nil {
		t.Error("A's key trusted at the first login")
	}
	if strings.Contains(readLogs(t, logA), "MAIL:") {
		t.Error("the refused first login reached A")
	}
	confirmCopy := t.TempDir()
	os.MkdirAll(filepath.Join(confirmCopy, "game", "Data"), 0o755)
	if err := f.WriteServerList(confirmCopy); err != nil {
		t.Fatal(err)
	}
	if list := readList(t, confirmCopy); !strings.Contains(list, "\nserver\ta\tServer A\t"+frontServerA+"\t"+strconv.Itoa(portA)+"\tnew\t") {
		t.Errorf("the list after the first login doesn't show A's new key:\n%s", list)
	}
	// the second login to A trusts the key it showed: the tag gone from MAIL, preAuth and ping replayed first
	rep := g.call(c1, 1, 0x28, 4, loginBody("alex#a"))
	if rep.Type != fire.Reply {
		t.Fatalf("login to A: %v error %#x\n%s", rep.Type, rep.Error, readLogs(t, logDir))
	}
	textA := waitLog(t, logA, "    MAIL: 'alex'\n", 1)
	if strings.Contains(textA, "alex#") || !strings.Contains(textA, "cmd 0x0007 request id 0") || !strings.Contains(textA, "cmd 0x0002 request id 1") {
		t.Errorf("server A's log:\n%s", textA)
	}
	if b, ok := f.Current(); !ok || b.Tag != "a" {
		t.Fatalf("current %v %v", b, ok)
	}
	if b, _ := bm.Find("a"); b.LastUsed == "" {
		t.Error("A's last use not recorded")
	}
	if pin, err := os.ReadFile(TrustedGateway(l, frontServerA)); err != nil || len(strings.TrimSpace(string(pin))) != 64 {
		t.Errorf("A's key not trusted at the second login: %q %v", pin, err)
	}
	// telemetry and QoS now go to A: the connection's first packet first, then the new one
	tel.Write(telemetryPacket("T=second"))
	waitLog(t, logA, "<- &lgr, ", 2)
	latencyProbe(t, qosGame)
	waitLog(t, logA, "[qos] udp ('"+frontLocal, 0)

	// logout: answered by A; the client closes the connection and goes through the redirector again (the front's)
	if lo := g.call(c1, 1, 0x46, 5, nil); lo.Type != fire.Reply {
		t.Fatalf("logout: %v", lo.Type)
	}
	waitLog(t, logA, "logged out; its session ends", 1)
	c1.Close()
	if host, port := g.redirect(); host != frontLocal || port != DefaultFrontPorts.Blaze {
		t.Fatalf("the redirector after the logout names %s:%d", host, port)
	}

	// login to B on a new connection: B's key handed out by its operator (Start-Client --gateway-pin), no confirmation
	bPin, err := tunnel.ProbePin(net.JoinHostPort(frontServerB, strconv.Itoa(portB)), 3*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(TrustedGateway(l, frontServerB)), 0o755)
	os.WriteFile(TrustedGateway(l, frontServerB), []byte(bPin.String()+"\n"), 0o644)
	c2 := g.dial(DefaultFrontPorts.Blaze)
	g.start(c2)
	if rep := g.call(c2, 1, 0x28, 2, loginBody("alex#B")); rep.Type != fire.Reply {
		t.Fatalf("login to B: %v error %#x\n%s", rep.Type, rep.Error, readLogs(t, logDir))
	}
	waitLog(t, logB, "    MAIL: 'alex'\n", 1)
	if b, _ := f.Current(); b.Tag != "b" {
		t.Fatalf("current %v after the login to B", b)
	}
	tel.Write(telemetryPacket("T=third"))
	waitLog(t, logB, "<- &lgr, ", 2) // the first packet again, then this one
	if strings.Contains(readLogs(t, logA), "T=third") {
		t.Error("telemetry after the switch reached A")
	}

	// a lost connection: the client sends resumeSession straight to the Blaze port; it goes to B
	c2.Close()
	c3 := g.dial(DefaultFrontPorts.Blaze)
	g.send(c3, 0x7802, 0x23, 0, tdf.NewWriter().String("SKEY", "x").Bytes())
	waitLog(t, logB, "UserSessions(0x7802) cmd 0x0023 request", 1)
	c3.Close()

	// the front's refusals, all on one connection (the client keeps it after a refused login)
	c4 := g.dial(DefaultFrontPorts.Blaze)
	defer c4.Close()
	g.start(c4)
	for i, c := range []struct {
		mail string
		code uint16
	}{{"alex#zz", FrontErrUnknownServer}, {"alex#down", FrontErrUnreachable}, {"alex#nogw", FrontErrNoGateway}} {
		rep := g.call(c4, 1, 0x28, uint32(2+i), loginBody(c.mail))
		if rep.Type != fire.ErrorReply || rep.Error != c.code {
			t.Errorf("login %s: %v error %#x, want error %#x", c.mail, rep.Type, rep.Error, c.code)
		}
	}
	// A with another key than the trusted one
	other := t.TempDir()
	tunnel.WriteIdentity(other)
	otherPin, _ := tunnel.PinFromCertFile(filepath.Join(other, tunnel.CertFile))
	os.WriteFile(TrustedGateway(l, frontServerA), []byte(otherPin.String()+"\n"), 0o644)
	if rep := g.call(c4, 1, 0x28, 5, loginBody("alex#a")); rep.Type != fire.ErrorReply || rep.Error != FrontErrKeyChanged {
		t.Errorf("login to A with a changed key: %v error %#x", rep.Type, rep.Error)
	}
	if b, _ := f.Current(); b.Tag != "b" {
		t.Errorf("a refused switch changed the current server to %v", b)
	}
	// the same connection then logs in to B (no tag: the current server)
	if rep := g.call(c4, 1, 0x28, 6, loginBody("alex")); rep.Type != fire.Reply {
		t.Fatalf("login after the refusals: %v error %#x", rep.Type, rep.Error)
	}
	waitLog(t, logB, "    MAIL: 'alex'\n", 2)
	front := waitLog(t, logDir, "login refused (error 0x00e2)", 1)
	for _, want := range []string{"login refused (error 0x00e0): no server \"zz\" in the list", "login refused (error 0x00e1)",
		"login refused (error 0x00e3)", "the replayed preAuth (id 0) answered by a", "trusting " + frontServerA + "'s tunnel key",
		"login refused (error 0x00e7): " + net.JoinHostPort(frontServerA, strconv.Itoa(portA)) + "'s tunnel key", "(confirmed: the second login to it;"} {
		if !strings.Contains(front, want) {
			t.Errorf("the front's log lacks %q", want)
		}
	}
	if strings.Contains(front, "dawngate1") || strings.Contains(readLogs(t, logA), "dawngate1") {
		t.Error("a log holds the password")
	}

	// the login screen's list: every server probed, the preselected and last-used ones
	bare := &tunnel.Gateway{}
	if bare.Identity, _, err = tunnel.LoadOrCreateIdentity(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := bare.Start(net.JoinHostPort(frontGateway, "0")); err != nil {
		t.Fatal(err)
	}
	defer bare.Close()
	copyDir := t.TempDir()
	os.MkdirAll(filepath.Join(copyDir, "game", "Data"), 0o755)
	if err := f.WriteServerList(copyDir); err != nil {
		t.Fatal(err)
	}
	list := readList(t, copyDir)
	if !strings.Contains(list, "\nlast\ta\n") && !strings.Contains(list, "\nlast\tb\n") { // both used within a second
		t.Errorf("the server list's last server isn't a or b:\n%s", list)
	}
	for _, want := range []string{"dawngate-servers\t3\nversion\t" + f.Version + "\n", "\npreselect\t\n",
		"\nserver\ta\tServer A\t" + frontServerA + "\t" + strconv.Itoa(portA) + "\tchanged\t",
		"\nserver\tb\t" + frontServerB + "\t" + frontServerB + "\t" + strconv.Itoa(portB) + "\tok\t",
		"\nserver\tdown\t" + frontNothing + "\t" + frontNothing + "\t42128\tunreachable\t0\t\t\t\t\t\t\t2\n",
		"\nserver\tnogw\t" + frontNoGateway + "\t" + frontNoGateway + "\t42128\tno_gateway\t0\t\t\t\t\t\t\t3\n"} {
		if !strings.Contains(list, want) {
			t.Errorf("the server list lacks %q:\n%s", want, list)
		}
	}
	if !strings.HasSuffix(strings.TrimRight(list, "\n"), "\nend") {
		t.Errorf("the server list doesn't end with its end line:\n%s", list)
	}
	// the fingerprints: A's served key and the other one trusted for it; B's served key is its trusted one; both run
	// this Start-Client's version (the same build) and open sign-up; their link slots are the list's first two
	for _, line := range strings.Split(list, "\n") {
		fs := strings.Split(line, "\t")
		if len(fs) < 2 || fs[0] != "server" || (fs[1] != "a" && fs[1] != "b") {
			continue
		}
		if len(fs) != 14 || len(fs[7]) != 16 || len(fs[8]) != 16 || (fs[1] == "a") != (fs[7] != fs[8]) || fs[9] != "" ||
			fs[10] != f.Version || fs[11] != versionSame || fs[12] != signupOpen || fs[13] != map[string]string{"a": "0", "b": "1"}[fs[1]] {
			t.Errorf("server %s's fingerprints: %q", fs[1], line)
		}
	}

	// the picker's Add and Remove through the login (on the same connection)
	for i, c := range []struct {
		mail string
		code uint16
	}{{"alex#+127.0.9.56", FrontErrUnreachable}, {"alex#+bad address", FrontErrBadAddress}, {"alex#+", FrontErrBadAddress},
		{"alex#-zz", FrontErrUnknownServer}} {
		rep := g.call(c4, 1, 0x28, uint32(10+i), loginBody(c.mail))
		if rep.Type != fire.ErrorReply || rep.Error != c.code {
			t.Errorf("login %s: %v error %#x, want error %#x", c.mail, rep.Type, rep.Error, c.code)
		}
	}
	if len(bm.List()) != 4 {
		t.Fatalf("a refused add changed the list: %+v", bm.List())
	}
	// a bare gateway answers: added (with its name), its new key shown (confirm once); the same add again is a login
	// to it, which trusts the key, but it has no Blaze
	third := "alex#+" + net.JoinHostPort(frontGateway, strconv.Itoa(bare.Port())) + "=Third #1"
	if rep := g.call(c4, 1, 0x28, 20, loginBody(third)); rep.Type != fire.ErrorReply || rep.Error != FrontErrNewKey {
		t.Errorf("login %s: %v error %#x", third, rep.Type, rep.Error)
	}
	if c, ok := bm.Find(frontGateway); !ok || c.Tag != "c" || c.Name != "Third #1" || c.GatewayPort != bare.Port() {
		t.Errorf("the added server: %+v", c)
	}
	if _, err := os.Stat(TrustedGateway(l, frontGateway)); err == nil {
		t.Error("the added server's key trusted at its first login")
	}
	if list := readList(t, copyDir); !strings.Contains(list, "\nserver\tc\tThird #1\t"+frontGateway+"\t"+strconv.Itoa(bare.Port())+"\tnew\t") {
		t.Errorf("the list wasn't written again after the add, with the new key:\n%s", list)
	}
	if rep := g.call(c4, 1, 0x28, 21, loginBody(third)); rep.Type != fire.ErrorReply || rep.Error != FrontErrServer {
		t.Errorf("login %s again: %v error %#x", third, rep.Type, rep.Error)
	}
	if _, err := os.Stat(TrustedGateway(l, frontGateway)); err != nil {
		t.Errorf("the added server's key isn't trusted after the second login: %v", err)
	}
	// a key that changes between the two logins isn't trusted: the second login shows the new one instead
	other2 := t.TempDir()
	tunnel.WriteIdentity(other2)
	otherPin2, _ := tunnel.PinFromCertFile(filepath.Join(other2, tunnel.CertFile))
	if err := f.trustKey(Bookmark{Address: "127.0.9.57", GatewayPort: 42128}, otherPin); asFrontError(err).code != FrontErrNewKey {
		t.Errorf("a new key: %v", err)
	}
	if err := f.trustKey(Bookmark{Address: "127.0.9.57", GatewayPort: 42128}, otherPin2); asFrontError(err).code != FrontErrNewKey {
		t.Errorf("another key at the second login: %v", err)
	}
	if _, err := os.Stat(TrustedGateway(l, "127.0.9.57")); err == nil {
		t.Error("a key that changed between the logins was trusted")
	}
	if err := f.trustKey(Bookmark{Address: "127.0.9.57", GatewayPort: 42128}, otherPin2); err != nil {
		t.Errorf("the same key at the next login: %v", err)
	}
	// an add of a server in the list already is a login to it
	if rep := g.call(c4, 1, 0x28, 22, loginBody("alex#+"+frontServerB+"=whatever")); rep.Type != fire.Reply {
		t.Errorf("add B again: %v error %#x", rep.Type, rep.Error)
	}
	waitLog(t, logB, "    MAIL: 'alex'\n", 3)
	if len(bm.List()) != 5 {
		t.Errorf("B added twice: %+v", bm.List())
	}
	// remove, on a new connection (the last one is B's now)
	c5 := g.dial(DefaultFrontPorts.Blaze)
	defer c5.Close()
	g.start(c5)
	if rep := g.call(c5, 1, 0x28, 2, loginBody("alex#-c")); rep.Type != fire.ErrorReply || rep.Error != FrontErrRemoved {
		t.Errorf("remove c: %v error %#x", rep.Type, rep.Error)
	}
	if rep := g.call(c5, 1, 0x28, 3, loginBody("alex#-c")); rep.Type != fire.ErrorReply || rep.Error != FrontErrUnknownServer {
		t.Errorf("remove c again: %v error %#x", rep.Type, rep.Error)
	}
	if _, ok := bm.Find("c"); ok || strings.Contains(readList(t, copyDir), "\tThird #1\t") {
		t.Error("c is still in the list")
	}
	// the old list endpoint's path is no longer the front's: it goes to the current server like any other; the
	// login screen's pages are the front's: the plain sign-up link lists the servers, a server's own link (its slot)
	// reaches it, through the current session (B), and A's changed key is refused there too; a legal page goes to the
	// current server
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get("http://" + net.JoinHostPort(frontLocal, strconv.Itoa(DefaultFrontPorts.HTTP)) + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(body)
	}
	get("/dawngate-picker/servers")
	waitLog(t, logB, "GET /dawngate-picker/servers", 1)
	if code, body := get("/dawngate/signup"); code != 200 || !strings.Contains(body, "pick the server") ||
		!strings.Contains(body, `href="/dawngate/pick/1/signup"`) || !strings.Contains(body, "open: your first login") {
		t.Errorf("the plain sign-up link: %d\n%s", code, body)
	}
	if strings.Contains(readLogs(t, logB), "GET /dawngate/signup") {
		t.Error("the plain sign-up link reached B")
	}
	if code, body := get("/dawngate/pick/1/signup"); code != 200 || !strings.Contains(body, "an account is made at its first login") {
		t.Errorf("B's sign-up page: %d\n%s", code, body)
	}
	waitLog(t, logB, "GET /dawngate/signup", 1)
	if code, body := get("/dawngate/pick/0/password"); code != 403 || !strings.Contains(body, "KEY CHANGED") {
		t.Errorf("A's page with a changed key: %d\n%s", code, body)
	}
	if strings.Contains(readLogs(t, logA), "GET /dawngate/password") {
		t.Error("A's page was asked despite its changed key")
	}
	get("/dawngate/terms")
	waitLog(t, logB, "GET /dawngate/terms", 1)

	// the file as written
	raw, _ := os.ReadFile(BookmarksPath(l))
	again, err := LoadBookmarks(BookmarksPath(l))
	if err != nil || len(again.List()) != 4 || !strings.Contains(string(raw), `"gateway_port": `+strconv.Itoa(portA)) {
		t.Errorf("bookmarks.json reread: %v\n%s", err, raw)
	}
}

// TestFrontPages: the login screen's links before any login. The picker's ADD (<name>#*<address>) adds a server
// without a login (FrontErrAdded); the list shows its sign-up mode and link slot; its own sign-up link shows its new
// key, which only the page's confirmation (this run's token, from the front's own page) trusts; the page then comes
// from the server through a short tunnel session of its own; the plain link lists the servers; a login with an unknown
// name there gets the server's own refusal (0x000B, whose text points to Sign up now!).
func TestFrontPages(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.58's and 127.0.9.59's ports")
	}
	const server, local = "127.0.9.58", "127.0.9.59"
	port, logS := serveTunnelLogged(t, server, "--signup", "invite")
	l := setup.Layout{Root: t.TempDir()}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	_, tlsCfg, err := tunnelCertSet(l, local, false)
	if err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	lf, err := os.Create(filepath.Join(logDir, "front.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	f := &Front{Log: logging.New(lf), Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(local), RedirectorTLS: tlsCfg,
		ConnectTimeout: 3 * time.Second, ListRefresh: -1}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	copyDir := t.TempDir()
	os.MkdirAll(filepath.Join(copyDir, "game", "Data"), 0o755)
	if err := f.WriteServerList(copyDir); err != nil {
		t.Fatal(err)
	}
	g := fakeGame{t: t, ip: local}
	g.redirect()
	c := g.dial(DefaultFrontPorts.Blaze)
	defer c.Close()
	g.start(c)
	// the picker's ADD: added, no login (nothing reaches the server's Blaze), the list written again with its mode
	add := "player#*" + net.JoinHostPort(server, strconv.Itoa(port)) + "=Invited"
	if rep := g.call(c, 1, 0x28, 2, loginBody(add)); rep.Type != fire.ErrorReply || rep.Error != FrontErrAdded {
		t.Fatalf("ADD: %v error %#x\n%s", rep.Type, rep.Error, readLogs(t, logDir))
	}
	b, ok := bm.Find(server)
	if !ok || b.Name != "Invited" {
		t.Fatalf("not added: %+v", bm.List())
	}
	if rep := g.call(c, 1, 0x28, 3, loginBody("player#*"+server)); rep.Type != fire.ErrorReply || rep.Error != FrontErrAdded || len(bm.List()) != 1 {
		t.Errorf("ADD again: %v error %#x, %d servers", rep.Type, rep.Error, len(bm.List()))
	}
	if rep := g.call(c, 1, 0x28, 4, loginBody("player#*127.0.9.53")); rep.Type != fire.ErrorReply || rep.Error != FrontErrUnreachable || len(bm.List()) != 1 {
		t.Errorf("ADD of nothing: %v error %#x, %d servers", rep.Type, rep.Error, len(bm.List()))
	}
	if strings.Contains(readLogs(t, logS), "MAIL:") {
		t.Error("an ADD reached the server's Blaze")
	}
	if list := readList(t, copyDir); !strings.Contains(list, "\tnew\t") || !strings.Contains(list, "\tsame\tinvite\t0\n") {
		t.Errorf("the list after the ADD:\n%s", list)
	}
	base := "http://" + net.JoinHostPort(local, strconv.Itoa(DefaultFrontPorts.HTTP))
	do := func(method, path, host string, form url.Values, hdr map[string]string) (int, string, http.Header) {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, _ := http.NewRequest(method, base+path, body)
		if host != "" {
			req.Host = host
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(raw), resp.Header
	}
	// its sign-up link before any login: the new key shown, nothing trusted, nothing asked of the server yet
	code, page, phdr := do("GET", "/dawngate/pick/0/signup", "", nil, nil)
	pin, _ := tunnel.ProbePin(b.Gateway(), 3*time.Second, nil)
	if rp := phdr.Get("Referrer-Policy"); rp != "same-origin" { // no-referrer made browsers send Origin: null
		t.Errorf("the page's Referrer-Policy: %q", rp)
	}
	if code != 200 || !strings.Contains(page, "New server key") || !strings.Contains(page, pin.Fingerprint()) || !strings.Contains(page, `name="token"`) {
		t.Fatalf("the sign-up link of a new server: %d\n%s", code, page)
	}
	if _, err := os.Stat(TrustedGateway(l, server)); err == nil {
		t.Fatal("the key was trusted by showing the page")
	}
	// confirmations that aren't the page's: no token, another site, another Host (DNS rebinding)
	good := url.Values{"token": {f.pageToken}, "key": {pin.String()}}
	for _, bad := range []struct {
		host string
		form url.Values
		hdr  map[string]string
	}{
		{"", url.Values{"key": {pin.String()}}, nil},
		{"", url.Values{"token": {"0" + f.pageToken[1:]}, "key": {pin.String()}}, nil},
		{"", good, map[string]string{"Origin": "http://evil.example"}},
		{"", good, map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"", good, map[string]string{"Origin": "null"}},
		{"", good, map[string]string{"Origin": "null", "Sec-Fetch-Site": "cross-site"}},
		{"evil.example:18080", good, nil},
	} {
		if code, page, _ := do("POST", "/dawngate/pick/0/signup", bad.host, bad.form, bad.hdr); code != 403 {
			t.Errorf("a confirmation %+v: %d\n%s", bad, code, page)
		}
	}
	if _, err := os.Stat(TrustedGateway(l, server)); err == nil {
		t.Fatal("a bad confirmation trusted the key")
	}
	// the confirmation as Chrome and Edge sent it under the page's earlier Referrer-Policy no-referrer: Origin null
	// beside Sec-Fetch-Site same-origin (a page's own Referrer-Policy is same-origin now, so they name the origin)
	if code, _, hdr := do("POST", "/dawngate/pick/0/signup", "", good, map[string]string{"Origin": "null", "Sec-Fetch-Site": "same-origin"}); code != 303 ||
		hdr.Get("Location") != "/dawngate/pick/0/signup" {
		t.Fatalf("the confirmation: %d %v\n%s", code, hdr, readLogs(t, logDir))
	}
	if raw, err := os.ReadFile(TrustedGateway(l, server)); err != nil || strings.TrimSpace(string(raw)) != pin.String() {
		t.Fatalf("the confirmed key: %q %v", raw, err)
	}
	// now the server's own page, through a short session of its own (no login)
	if code, page, _ := do("GET", "/dawngate/pick/0/signup", "", nil, nil); code != 200 || !strings.Contains(page, "invite code") {
		t.Errorf("the server's sign-up page: %d\n%s", code, page)
	}
	waitLog(t, logS, "GET /dawngate/signup", 1)
	if _, ok := f.Current(); ok {
		t.Error("the page opened a current session")
	}
	// the plain link: the list of servers with their modes; a page of a host other than the front's is refused
	if code, page, _ := do("GET", "/dawngate/signup", "", nil, nil); code != 200 || !strings.Contains(page, "Invited") ||
		!strings.Contains(page, "invite only") || !strings.Contains(page, `href="/dawngate/pick/0/signup"`) {
		t.Errorf("the plain sign-up link: %d\n%s", code, page)
	}
	if code, _, _ := do("GET", "/dawngate/pick/0/signup", "rebound.example:18080", nil, nil); code != 403 {
		t.Errorf("another Host: %d", code)
	}
	if code, page, _ := do("GET", "/dawngate/pick/9/password", "", nil, nil); code != 200 || !strings.Contains(page, "no longer in your list") {
		t.Errorf("a slot of no server: %d\n%s", code, page)
	}
	// a login with an unknown name: the server's refusal for a sign-up first (the key is trusted now)
	if rep := g.call(c, 1, 0x28, 5, loginBody("newbie#a")); rep.Type != fire.ErrorReply || rep.Error != 0x000B {
		t.Errorf("an unknown name on an invite server: %v error %#x\n%s", rep.Type, rep.Error, readLogs(t, logDir))
	}
	front := readLogs(t, logDir)
	for _, want := range []string{"login refused (error 0x00ec)", "is in the server list (the picker's ADD; no login)",
		"isn't trusted yet: shown for confirmation", "a confirmation without this run's token refused",
		"trusting " + server + "'s tunnel key " + pin.Fingerprint() + " from now on (confirmed on its sign-up page"} {
		if !strings.Contains(front, want) {
			t.Errorf("the front's log lacks %q", want)
		}
	}
}

// Start-Client in picker mode: Start-Client.ini's server becomes the list's first, the address given is added and
// preselected, its --gateway-pin trusted; the copy is patched for the tunnel address; the picker needs the tunnel.
func TestStartClientPicker(t *testing.T) {
	dir := release(t)
	os.WriteFile(filepath.Join(dir, ClientConfigName), []byte("server = 203.0.113.9\r\npicker = on\r\n"), 0o644)
	pin := strings.Repeat("ab", 32)
	code, out := run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal, "--gateway-pin", pin, "198.51.100.7")
	if code != 0 || !strings.Contains(out, "203.0.113.9:42128 added to the server list as a (Start-Client.ini's server)") ||
		!strings.Contains(out, "198.51.100.7:42128 added to the server list as b (the address given)") || strings.Contains(out, "  server   ") {
		t.Fatalf("picker setup: exit %d\n%s", code, out)
	}
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil || len(bm.List()) != 2 {
		t.Fatalf("bookmarks %v %v", bm, err)
	}
	if raw, _ := os.ReadFile(TrustedGateway(l, "198.51.100.7")); strings.TrimSpace(string(raw)) != pin {
		t.Errorf("--gateway-pin not stored: %q", raw)
	}
	exe, _ := os.ReadFile(filepath.Join(l.ClientCopy(), "game", "Dawngate.exe"))
	if d, _ := exepatch.Describe(exe); d["redirector"] != frontLocal || d["bind"] != frontLocal {
		t.Errorf("client exe %v", d)
	}
	// the login screen's picker is installed (moviepatch); its list is written only before the game starts
	if _, err := os.Stat(filepath.Join(l.ClientCopy(), filepath.FromSlash(moviepatch.Rel))); err != nil || !strings.Contains(out, "server picker") {
		t.Errorf("the picker movie: %v\n%s", err, out)
	}
	// again: nothing added; with the second server's address it is preselected
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal, "198.51.100.7")
	if code != 0 || strings.Contains(out, "added") || !strings.Contains(out, "#b      198.51.100.7:42128") {
		t.Errorf("picker again: exit %d\n%s", code, out)
	}
	// the address first and the flags after it, as the messages and the usage line write it: every flag still counts
	// (--setup-only read: no game started; the flag package alone would have stopped at the address)
	code, out = run(t, RunClient, dir, "198.51.100.7", "--non-interactive", "--setup-only", "--tunnel-address", frontLocal)
	if code != 0 || !strings.Contains(out, "#b      198.51.100.7:42128") || strings.Contains(out, "The game starts") {
		t.Errorf("the address before the flags: exit %d\n%s", code, out)
	}
	if code, out = run(t, RunClient, dir, "198.51.100.7", "--non-interactive", "203.0.113.9"); code != 2 || !strings.Contains(out, "one server address") {
		t.Errorf("two addresses: exit %d\n%s", code, out)
	}
	// picker = on but tunnel = off: Start-Client says so and plays on one server directly (here a loopback address
	// without a server: refused as before the picker)
	if code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel", "off", "127.0.9.61"); code != 1 ||
		!strings.Contains(out, "picker   not used for 127.0.9.61 (tunnel = off: the picker needs the tunnel)") ||
		!strings.Contains(out, "your list's 2 server(s) aren't shown") || !strings.Contains(out, "No server is set up on this PC for 127.0.9.61") {
		t.Errorf("picker without the tunnel: exit %d\n%s", code, out)
	}
	if code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--gateway-pin", pin); code != 1 || !strings.Contains(out, "needs the server's address") {
		t.Errorf("--gateway-pin alone: exit %d\n%s", code, out)
	}
	if code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--picker", "maybe"); code != 1 {
		t.Errorf("--picker maybe: exit %d\n%s", code, out)
	}
	if d, _ := exepatch.Describe(exe); d["appdata"] != "DawngateData" {
		t.Errorf("a player's copy has per-user folders of its own: %v", d)
	}
	// the test lanes' hook: the copy's per-user folders get the lane's letter (a game beside the other lanes' clients)
	if code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal, "--test-lane", "d"); code != 0 {
		t.Fatalf("--test-lane d: exit %d\n%s", code, out)
	}
	exe, _ = os.ReadFile(filepath.Join(l.ClientCopy(), "game", "Dawngate.exe"))
	if d, _ := exepatch.Describe(exe); d["appdata"] != "DawngateDataD" || d["documents"] != "DawngateD" || d["bind"] != frontLocal {
		t.Errorf("--test-lane d: client exe %v", d)
	}
	for _, bad := range []string{"dd", "4", "-"} {
		if code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--test-lane", bad); code != 1 || !strings.Contains(out, "one letter") {
			t.Errorf("--test-lane %s: exit %d\n%s", bad, code, out)
		}
	}
}

// Start-Client's default, picker = auto: the picker whenever the tunnel is used (a public address, tunnel = on, a
// server of the list, the list without a server named); a server without the tunnel is played on directly, and
// Start-Client says that the login screen shows no list.
func TestStartClientPickerDefault(t *testing.T) {
	dir := release(t)
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	picked := func(out string) bool {
		return strings.Contains(out, "servers  ") && !strings.Contains(out, "  server   ")
	}
	// tunnel = on (as a public address takes it with tunnel = auto, TestTunnelModes): the picker; the address added and
	// preselected (nothing is probed before the login)
	code, out := run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal, "--tunnel", "on", "127.0.9.62")
	if code != 0 || !picked(out) || !strings.Contains(out, "127.0.9.62:42128 added to the server list as a (the address given)") {
		t.Fatalf("tunnel = on: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(l.ClientCopy(), filepath.FromSlash(moviepatch.Rel))); err != nil {
		t.Errorf("no picker movie: %v", err)
	}
	// no server named, the list has one: the picker with tunnel = auto (the ini has no server)
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal)
	if code != 0 || !picked(out) || strings.Contains(out, "added") || !strings.Contains(out, "#a      127.0.9.62:42128") {
		t.Errorf("the list without a server named: exit %d\n%s", code, out)
	}
	// a tag of the list (--server a) and its address: the picker (the list's servers have their tunnel), nothing added
	for _, s := range []string{"a", "127.0.9.62"} {
		if code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal, "--server", s); code != 0 ||
			!picked(out) || strings.Contains(out, "added") {
			t.Errorf("--server %s: exit %d\n%s", s, code, out)
		}
	}
	if code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal, "--tunnel", "on", "127.0.9.63"); code != 0 ||
		!picked(out) || !strings.Contains(out, "127.0.9.63:42128 added to the server list as b (the address given)") {
		t.Errorf("a second server: exit %d\n%s", code, out)
	}
	// tunnel = auto takes the tunnel for every server, a loopback or home-network one too: the picker
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal, "127.0.9.61")
	if code != 0 || !picked(out) || !strings.Contains(out, "127.0.9.61:42128 added to the server list as c (the address given)") {
		t.Errorf("a loopback server with tunnel = auto: exit %d\n%s", code, out)
	}
	// tunnel = off (the developers' option): played on directly, as before the picker; Start-Client says why there is
	// no list (here no server is set up on this PC for it)
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel", "off", "127.0.9.64")
	if code != 1 || !strings.Contains(out, "picker   not used for 127.0.9.64 (tunnel = off: the picker needs the tunnel)") ||
		!strings.Contains(out, "your list's 3 server(s) aren't shown") || !strings.Contains(out, "No server is set up on this PC for 127.0.9.64") {
		t.Errorf("a server without the tunnel: exit %d\n%s", code, out)
	}
	// Start-Client.ini's tunnel = off wins over the list too
	os.WriteFile(filepath.Join(dir, ClientConfigName), []byte("server = 127.0.9.64\r\ntunnel = off\r\n"), 0o644)
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only")
	if code != 1 || !strings.Contains(out, "picker   not used for 127.0.9.64 (tunnel = off") {
		t.Errorf("the ini's server without the tunnel: exit %d\n%s", code, out)
	}
	// picker = off: one server, no note
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--picker", "off")
	if code != 1 || strings.Contains(out, "picker   ") {
		t.Errorf("picker = off: exit %d\n%s", code, out)
	}
	for in, want := range map[string]string{"": PickerAuto, " On ": PickerOn, "yes": PickerOn, "off": PickerOff, "false": PickerOff} {
		if got, err := pickerSetting(in); err != nil || got != want {
			t.Errorf("pickerSetting(%q) = %q, %v", in, got, err)
		}
	}
	// this PC's own server behind a router: the list dials its bind address and shows the public one as its name
	scfg := Config{"address": "203.0.113.7", "bind": "192.168.1.20"}
	if b := ownBookmark("203.0.113.7", 42128, scfg); b.Address != "192.168.1.20" || b.Name != "203.0.113.7" {
		t.Errorf("own server behind a router: %+v", b)
	}
	if b := ownBookmark("203.0.113.9", 42128, scfg); b.Address != "203.0.113.9" || b.Name != "" {
		t.Errorf("another server: %+v", b)
	}
}

// A login the server refuses after a switch (a wrong password) leaves the login screen showing, so the list goes
// on refreshing; the next login that goes through stops it.
func TestFrontRefusedLoginKeepsRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.50's and 127.0.9.52's ports")
	}
	portA, _ := serveTunnelLogged(t, frontServerA)
	l := setup.Layout{Root: t.TempDir()}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []Bookmark{{Tag: "down", Address: frontNothing}, {Tag: "a", Address: frontServerA, GatewayPort: portA}} {
		if _, err := bm.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	_, tlsCfg, err := tunnelCertSet(l, frontLocal, false)
	if err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	lf, err := os.Create(filepath.Join(logDir, "front.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	f := &Front{Log: logging.New(lf), Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(frontLocal), RedirectorTLS: tlsCfg,
		ConnectTimeout: 3 * time.Second, ProbeTimeout: time.Second}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	copyDir := t.TempDir()
	os.MkdirAll(filepath.Join(copyDir, "game", "Data"), 0o755)
	if err := f.WriteServerList(copyDir); err != nil {
		t.Fatal(err)
	}
	if list := readList(t, copyDir); !strings.Contains(list, "\nlast\t\n") {
		t.Fatalf("the list before a login:\n%s", list)
	}
	g := fakeGame{t: t}
	atLogin := func() bool {
		f.listMu.Lock()
		defer f.listMu.Unlock()
		return f.atLogin
	}
	login := func(c net.Conn, id uint32, pass string) fire.Frame {
		return g.call(c, 1, 0x28, id, tdf.NewWriter().String("MAIL", "alex#a").String("PASS", pass).String("TOKN", "").Int("TYPE", 0).Bytes())
	}
	g.redirect()
	c1 := g.dial(DefaultFrontPorts.Blaze)
	g.start(c1)
	// confirm once: the first login shows A's new key and the login screen stays
	if rep := login(c1, 2, "dawngate1"); rep.Type != fire.ErrorReply || rep.Error != FrontErrNewKey || !atLogin() {
		t.Fatalf("the first login to a new key: %v %#x, at the login screen %v", rep.Type, rep.Error, atLogin())
	}
	if rep := login(c1, 3, "dawngate1"); rep.Type != fire.Reply || atLogin() {
		t.Fatalf("the second login (the account made): %v %#x, at the login screen %v", rep.Type, rep.Error, atLogin())
	}
	// the list file names A as the last server at once, away from the login screen: the login screen after the
	// next logout loads it and preselects A
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(readList(t, copyDir), "\nlast\ta\n"); {
		if time.Now().After(deadline) {
			t.Fatalf("the list's last server after the login to A:\n%s", readList(t, copyDir))
		}
		time.Sleep(50 * time.Millisecond)
	}
	c1.Close()
	g.redirect() // the client after a logout
	if !atLogin() {
		t.Fatal("not at the login screen after the redirector")
	}
	c2 := g.dial(DefaultFrontPorts.Blaze)
	defer c2.Close()
	g.start(c2)
	if rep := login(c2, 2, "wrongpassword"); rep.Type != fire.ErrorReply || !atLogin() {
		t.Fatalf("a wrong password: %v %#x, at the login screen %v\n%s", rep.Type, rep.Error, atLogin(), readLogs(t, logDir))
	}
	waitLog(t, logDir, "refused the login (error", 0)
	if rep := login(c2, 3, "dawngate1"); rep.Type != fire.Reply || atLogin() {
		t.Fatalf("the right password: %v %#x, at the login screen %v", rep.Type, rep.Error, atLogin())
	}
}

func TestSplitTagAndCleanLogin(t *testing.T) {
	for _, c := range []struct{ in, name, tag string }{
		{"alex#b", "alex", "b"}, {"alex", "alex", ""}, {"a#b#c", "a", "b#c"}, {"alex#", "alex", ""}, {"Zoë Smith#Home", "Zoë Smith", "Home"},
		{"alex#+play.example.org=Friends #2", "alex", "+play.example.org=Friends #2"}, {"alex#-b", "alex", "-b"},
	} {
		if name, tag, _ := splitTag(c.in); name != c.name || tag != c.tag {
			t.Errorf("splitTag(%q) = %q, %q", c.in, name, tag)
		}
	}
	raw, _ := fire.Encode(fire.Frame{Component: 1, Command: 0x28, Type: fire.Request, ID: 7, Payload: loginBody("alex#b")})
	raw[9] |= 0x30 // header bits the frame type doesn't use stay as sent
	mail, name, tag, out, err := cleanLogin(raw)
	if err != nil || mail != "alex#b" || name != "alex" || tag != "b" {
		t.Fatalf("cleanLogin: %q %q %q %v", mail, name, tag, err)
	}
	want, _ := fire.Encode(fire.Frame{Component: 1, Command: 0x28, Type: fire.Request, ID: 7, Payload: loginBody("alex")})
	want[9] |= 0x30
	if string(out) != string(want) {
		t.Errorf("cleanLogin:\n got %x\nwant %x", out, want)
	}
	if _, _, _, same, _ := cleanLogin(want); string(same) != string(want) {
		t.Error("a login without a tag was changed")
	}
	// the golden login request (testdata/tdf_golden.json, frames login_request) with a tag typed: the decoder and
	// encoder give its bytes again, so the cleaned request differs only in MAIL
	golden, _ := hex.DecodeString("003200010028000000000002b61a6c0113706c61796572406578616d706c652e636f6d00c21cf3010a6461776e676174653100d2faee010100d39c250000")
	st, _ := tdf.Decode(golden[fire.HeaderLen:])
	st.Fields[0].Value = "player@example.com#home"
	tagged := append(append([]byte(nil), golden[:fire.HeaderLen]...), tdf.Encode(st)...)
	binary.BigEndian.PutUint16(tagged[0:], uint16(len(tagged)-fire.HeaderLen))
	if _, name, tag, out, err := cleanLogin(tagged); err != nil || name != "player@example.com" || tag != "home" || string(out) != string(golden) {
		t.Errorf("the golden login with a tag: %q %q %v\n got %x\nwant %x", name, tag, err, out, golden)
	}
	// the real client's login request as it sent it, typed alex#a (captured live): it encodes again to its own
	// bytes, so the front's rewrite is exact; the cleaned request is the same frame with MAIL 'alex'
	real, _ := hex.DecodeString("002600010028000000000003b61a6c0107616c6578236100c21cf3010a6461776e676174653100d2faee010100d39c250000")
	wantReal, _ := hex.DecodeString("002400010028000000000003b61a6c0105616c657800c21cf3010a6461776e676174653100d2faee010100d39c250000")
	if _, name, tag, out, err := cleanLogin(real); err != nil || name != "alex" || tag != "a" || string(out) != string(wantReal) {
		t.Errorf("the real client's login: %q %q %v\n got %x\nwant %x", name, tag, err, out, wantReal)
	}
}

func TestBookmarks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers", BookmarksFile)
	b, err := LoadBookmarks(path)
	if err != nil || len(b.List()) != 0 {
		t.Fatal(err)
	}
	if _, ok := b.Last(); ok {
		t.Error("an empty list has a last server")
	}
	x, err := b.Add(Bookmark{Address: "203.0.113.5"})
	if err != nil || x.Tag != "a" || x.Name != "203.0.113.5" || x.GatewayPort != tunnel.DefaultPort {
		t.Fatalf("add: %+v %v", x, err)
	}
	y, _ := b.Add(Bookmark{Address: "play.example.org", Name: "Friends", Tag: "Home"})
	if y.Tag != "home" {
		t.Errorf("tag %q", y.Tag)
	}
	z, _ := b.Add(Bookmark{Address: "198.51.100.7", GatewayPort: 5000})
	if z.Tag != "b" {
		t.Errorf("the next free tag: %q", z.Tag)
	}
	for _, bad := range []Bookmark{{Address: "203.0.113.5"}, {Address: "198.51.100.8", Tag: "home"}, {Address: "bad address"},
		{Address: "::1"}, {Address: "198.51.100.9", Tag: "no#tag"}, {Address: "198.51.100.9", GatewayPort: 70000}} {
		if _, err := b.Add(bad); err == nil {
			t.Errorf("added %+v", bad)
		}
	}
	for key, tag := range map[string]string{"HOME": "home", "198.51.100.7": "b", "friends": "home", "a": "a"} {
		if got, ok := b.Find(key); !ok || got.Tag != tag {
			t.Errorf("Find(%q) = %+v", key, got)
		}
	}
	if last, _ := b.Last(); last.Tag != "a" {
		t.Errorf("last before any use: %s", last.Tag)
	}
	b.Used("b", time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
	b.Used("home", time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC))
	if last, _ := b.Last(); last.Tag != "b" {
		t.Errorf("last: %s", last.Tag)
	}
	if _, err := b.Remove("home"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Remove("home"); err != ErrNoBookmark {
		t.Errorf("remove twice: %v", err)
	}
	again, err := LoadBookmarks(path)
	if err != nil || len(again.List()) != 2 || again.List()[1].LastUsed != "2026-10-15T12:00:00Z" || again.List()[1].GatewayPort != 5000 {
		t.Errorf("reread: %+v %v", again.List(), err)
	}
	os.WriteFile(path, []byte(`{"servers": [{"tag": "a", "address": "x y"}]}`), 0o644)
	if _, err := LoadBookmarks(path); err == nil {
		t.Error("a bad file was read")
	}
}

// The list is written again while the login screen shows: a server that stops answering shows as unreachable (with
// the key trusted for it), each server's key fingerprints, the end line; a list with only new pings isn't logged.
func TestFrontListRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.58-59's ports")
	}
	const local, gwAddr = "127.0.9.58", "127.0.9.59"
	gw := &tunnel.Gateway{}
	var err error
	if gw.Identity, _, err = tunnel.LoadOrCreateIdentity(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(net.JoinHostPort(gwAddr, "0")); err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	l := setup.Layout{Root: t.TempDir()}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bm.Add(Bookmark{Tag: "a", Address: gwAddr, GatewayPort: gw.Port()}); err != nil {
		t.Fatal(err)
	}
	if _, err := TrustGateway(gwAddr, gw.Identity.Pin, TrustOptions{Store: TrustedGateway(l, gwAddr), Accept: true}); err != nil {
		t.Fatal(err)
	}
	_, tlsCfg, err := tunnelCertSet(l, local, false)
	if err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	lf, err := os.Create(filepath.Join(logDir, "front.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	f := &Front{Log: logging.New(lf), Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(local), RedirectorTLS: tlsCfg,
		ConnectTimeout: 2 * time.Second, ProbeTimeout: time.Second, ListRefresh: 200 * time.Millisecond}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	copyDir := t.TempDir()
	os.MkdirAll(filepath.Join(copyDir, "game", "Data"), 0o755)
	if err := f.WriteServerList(copyDir); err != nil {
		t.Fatal(err)
	}
	fp := gw.Identity.Pin.Fingerprint()
	ok := "\nserver\ta\t" + gwAddr + "\t" + gwAddr + "\t" + strconv.Itoa(gw.Port()) + "\tok\t"
	if list := readList(t, copyDir); !strings.Contains(list, ok) || !strings.Contains(list, "\t"+fp+"\t"+fp+"\t\t"+gw.Version+"\tsame\t\t0\nend\n") {
		t.Fatalf("the list before:\n%s", list)
	}
	gw.Close()
	down := "\nserver\ta\t" + gwAddr + "\t" + gwAddr + "\t" + strconv.Itoa(gw.Port()) + "\tunreachable\t0\t\t" + fp + "\t\t\t\t\t0\nend\n"
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(readList(t, copyDir), down) {
		if time.Now().After(deadline) {
			t.Fatalf("the list wasn't written again with the server down:\n%s", readList(t, copyDir))
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitLog(t, logDir, "(1 servers: a unreachable)", 0)
	// away from the login screen nothing is probed; back at it (the redirector) the list is written at once
	f.setAtLogin(false)
	time.Sleep(300 * time.Millisecond)
	f.setAtLogin(true)
	time.Sleep(300 * time.Millisecond)
	f.Close()
	logText := readLogs(t, logDir)
	if n := strings.Count(logText, "the server list for the login screen"); n != 2 {
		t.Errorf("the list was logged %d times, want 2 (the start, the server down):\n%s", n, logText)
	}
}
