package dgserver

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gms"
	"dawngate/server/internal/tdf"
)

// testPassword is the test logins' password (a new account's password must keep the rules).
const testPassword = "dawngate1"

func testCerts(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "server_cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(filepath.Join(dir, "server_key.pem"), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600)
	return dir
}

func call(t *testing.T, c net.Conn, comp, cmd uint16, id uint32, payload []byte) *tdf.Struct {
	t.Helper()
	req, _ := fire.Encode(fire.Frame{Component: comp, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	f, err := fire.NewReader(c).Next()
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != fire.Reply || f.ID != id || f.Component != comp || f.Command != cmd {
		t.Fatalf("reply header %+v", f)
	}
	s, err := tdf.Decode(f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The whole server on ephemeral ports: one pass through every listener, then the log file.
func TestServer(t *testing.T) {
	logDir := t.TempDir()
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: logDir})
	if err != nil {
		t.Fatal(err)
	}
	addr := func(i int) string { return fmt.Sprintf("127.0.0.1:%d", in.ports[i]) }

	// redirector -> our Blaze port
	tc, err := tls.Dial("tcp", addr(0), &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10,
		MaxVersion: tls.VersionTLS11, CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_256_CBC_SHA}})
	if err != nil {
		t.Fatal(err)
	}
	r := call(t, tc, 5, 1, 0, tdf.NewWriter().String("NAME", "battlegrounds-1-pc").Bytes())
	tc.Close()
	u, _ := r.Get("ADDR")
	valu := u.Value.(*tdf.Union).Field.Value.(*tdf.Struct)
	if port, _ := valu.Int("PORT"); int(port) != in.ports[1] {
		t.Errorf("redirector announced port %d, Blaze is on %d", port, in.ports[1])
	}

	// Blaze: preAuth announces our QoS port, ping answers
	bc, err := net.Dial("tcp", addr(1))
	if err != nil {
		t.Fatal(err)
	}
	pre := call(t, bc, 9, 7, 0, nil)
	qoss, _ := pre.Sub("QOSS")
	bwps, _ := qoss.Sub("BWPS")
	if psp, _ := bwps.Int("PSP"); int(psp) != in.ports[2] {
		t.Errorf("preAuth announced QoS port %d, QoS is on %d", psp, in.ports[2])
	}
	if stim, _ := call(t, bc, 9, 2, 1, nil).Int("STIM"); stim < time.Now().Unix()-60 {
		t.Errorf("STIM %d", stim)
	}
	bc.Close()

	// HTTP built-in rules
	hc, err := net.Dial("tcp", addr(3))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(hc, "GET /a/telemetry_rules.xml HTTP/1.1\r\nHost: x\r\n\r\n")
	status, _ := bufio.NewReader(hc).ReadString('\n')
	hc.Close()
	if status != "HTTP/1.1 200 OK\r\n" {
		t.Errorf("HTTP status %q", status)
	}

	// QoS: the latency test over HTTP names our UDP port, which echoes a probe
	qc, err := net.Dial("tcp", addr(2))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(qc, "GET /qos/qos?vers=1&qtyp=1&prpt=3659 HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n")
	qrep, _ := io.ReadAll(qc)
	qc.Close()
	if want := fmt.Sprintf("<qosport>%d</qosport><requestid>1</requestid>", in.ports[2]); !strings.Contains(string(qrep), want) {
		t.Errorf("QoS reply lacks %q:\n%s", want, qrep)
	}
	uc, err := net.Dial("udp4", addr(2))
	if err != nil {
		t.Fatal(err)
	}
	uc.Write([]byte{0, 0, 0, 2, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 42})
	uc.SetReadDeadline(time.Now().Add(5 * time.Second))
	echo := make([]byte, 64)
	if n, err := uc.Read(echo); err != nil || n != 30 || echo[19] != 42 {
		t.Errorf("QoS probe echo: %d bytes % x, %v", n, echo[:n], err)
	}
	uc.Close()

	// GMS logger (B8): a RakNet open-connection request is logged, not answered
	gmsPort := in.gmsUDP.LocalAddr().(*net.UDPAddr).Port
	gc, err := net.Dial("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(gmsPort)))
	if err != nil {
		t.Fatal(err)
	}
	gc.Write(append(append([]byte{0x05}, gms.OfflineMagic...), 6, 0, 0, 0))
	gc.Close()

	// telemetry logger
	c, err := net.Dial("tcp", addr(4))
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("&lgr"))
	c.(*net.TCPConn).CloseWrite()
	io.ReadAll(c)
	c.Close()
	time.Sleep(200 * time.Millisecond) // let the handlers log the close
	in.stop()

	files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
	if len(files) != 1 {
		t.Fatalf("log files: %v", files)
	}
	raw, _ := os.ReadFile(files[0])
	text := string(raw)
	if runtime.GOOS == "windows" {
		if strings.Count(text, "\n") != strings.Count(text, "\r\n") {
			t.Error("log file lines don't all end in CRLF")
		}
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	h := "127.0.0.1"
	for _, want := range []string{
		fmt.Sprintf("[stub] redirector on %s:%d (TLS), Blaze on %s:%d, QoS on %s:%d (HTTP, UDP), HTTP (CDN/crash) on %s:%d, telemetry on %s:%d, GMS (UDP, logged) on %s:%d; log %s\n",
			h, in.ports[0], h, in.ports[1], h, in.ports[2], h, in.ports[3], h, in.ports[4], h, gmsPort, files[0]),
		" 21 bytes: 0x05 ID_OPEN_CONNECTION_REQUEST_1, offline magic, protocol version 6, padded to 21 bytes (MTU probe)\n",
		"[gms]    first of its kind:\n    0000  05 00 ff ff 00 fe fe fe fe fd fd fd fd 12 34 56 ",
		"), TLSv1.1 AES256-SHA\n",
		fmt.Sprintf("[redirector] -> getServerInstance reply: 127.0.0.1:%d, SECU=0\n", in.ports[1]),
		"[blaze] -> preAuth reply, ",
		"[blaze] -> ping reply, 9 bytes:\n",
		"[http] -> 200 OK, 7119 bytes (built-in)\n",
		") GET /qos/qos?vers=1&qtyp=1&prpt=3659 HTTP/1.1\n",
		"[qos] -> 200 OK, <qos><numprobes>0</numprobes>",
		"[qos] udp ('127.0.0.1', ",
		" latency probe: request 2, requestid 1, flags 1 -> echo\n",
		" 4 bytes left over, not a full packet:\n    0000  26 6c 67 72 ",
		" closed after 4 bytes, 0 packets, 0 events\n",
		"[stub] stopped\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("log lacks %q:\n%s", want, text)
		}
	}
}

// --gms-external (B9): the GMS port is announced but left free for another process (raknet_probe_p5 --listen).
func TestGMSExternal(t *testing.T) {
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}) // stands in for the external GMS
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	port := probe.LocalAddr().(*net.UDPAddr).Port
	logDir := t.TempDir()
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: logDir, gmsPort: port, gmsExternal: true})
	if err != nil {
		t.Fatalf("start with the GMS port held by another socket: %v", err)
	}
	if in.gmsUDP != nil {
		t.Error("--gms-external bound the GMS port")
	}
	in.stop()
	files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
	if len(files) != 1 {
		t.Fatalf("log files: %v", files)
	}
	raw, _ := os.ReadFile(files[0])
	if want := fmt.Sprintf("GMS (external, announced only) on 127.0.0.1:%d;", port); !strings.Contains(string(raw), want) {
		t.Errorf("log lacks %q:\n%s", want, raw)
	}
	if _, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), gmsExternal: true}); err == nil {
		t.Error("no error for --gms-external without a fixed --gms-port")
	}
}

// --gms-dir (C1): the control listener runs on loopback, nothing binds the GMS ports, --print-ports
// lists the control port; bad settings stop the start.
func TestGMSHosts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dawngate.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: logDir, gmsDir: dir, gmsPorts: "20042-20043",
		gmsArgs: "-multipleInstances"})
	if err != nil {
		t.Fatal(err)
	}
	if in.gmsUDP != nil || in.hosts == nil || !strings.HasPrefix(in.hosts.Addr(), "127.0.0.1:") {
		t.Errorf("gmsUDP %v, hosts %v", in.gmsUDP, in.hosts)
	}
	in.stop()
	files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
	raw, _ := os.ReadFile(files[0])
	if want := "GMS (match hosts " + filepath.Join(dir, "Dawngate.exe") + " -multipleInstances, ports 20042-20043, control 127.0.0.1:"; !strings.Contains(string(raw), want) {
		t.Errorf("log lacks %q:\n%s", want, raw)
	}
	for _, bad := range []config{
		{gmsDir: dir, gmsPorts: "20042-20043", gmsExternal: true, gmsPort: 20042},
		{gmsDir: t.TempDir(), gmsPorts: "20042-20043"},
		{gmsDir: dir, gmsPorts: "x"},
		{gmsDir: dir, gmsPorts: "20042-20043", gmsWarm: 2}, // H1-live: the warm pool must leave a port for a cold start
	} {
		bad.bind, bad.certs, bad.logDir = "127.0.0.1", testCerts(t), t.TempDir()
		if in, err := start(bad); err == nil {
			in.stop()
			t.Errorf("no error for %+v", bad)
		}
	}
	c := config{redirectorPort: 1, blazePort: 2, qosPort: 3, httpPort: 4, telemetryPort: 5, gmsControlPort: 6}
	if got := fmt.Sprint(c.printPorts()); got != "[1 2 3 4 5]" {
		t.Errorf("print ports without --gms-dir: %s", got)
	}
	c.gmsDir = dir
	if got := fmt.Sprint(c.printPorts()); got != "[1 2 3 4 5 6]" {
		t.Errorf("print ports with --gms-dir: %s", got)
	}
}

func TestStartRejectsHostName(t *testing.T) {
	if _, err := start(config{bind: "localhost", logDir: t.TempDir()}); err == nil {
		t.Error("no error for --host localhost")
	}
}

// B14: --kill-switches, --broadcast-flags and the default --broadcast-file; a login on a started server gets the kill
// switches and the operator file's broadcast after loginPersona.
// ONLINECOUNT: --online-count on/off and --online-count-every's floor.
func TestOnlineCountFlags(t *testing.T) {
	for _, c := range []struct {
		on    string
		every time.Duration
		want  bool
		bad   bool
	}{{"on", 30 * time.Second, true, false}, {"OFF", time.Second, false, false}, {"maybe", 30 * time.Second, false, true},
		{"on", 500 * time.Millisecond, true, true}} {
		cfg := config{onlineCountEvery: c.every}
		err := applyOnlineCount(&cfg, c.on)
		if (err != nil) != c.bad || (!c.bad && cfg.onlineCount != c.want) {
			t.Errorf("%+v: on %v, err %v", c, cfg.onlineCount, err)
		}
	}
}

func TestSystemMessages(t *testing.T) {
	cfg := config{dataDir: "data", broadcastFlags: 4}
	if err := applySystemFlags(&cfg, "WalletTopUp,CustomGames", map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(cfg.killSwitches) != "[2 8]" || cfg.broadcastFile != filepath.Join("data", "broadcast.txt") {
		t.Errorf("kill switches %v, broadcast file %q", cfg.killSwitches, cfg.broadcastFile)
	}
	off := config{dataDir: "data", broadcastFlags: 4}
	if err := applySystemFlags(&off, "none", map[string]bool{"broadcast-file": true}); err != nil || off.killSwitches != nil ||
		off.broadcastFile != "" {
		t.Errorf("none and --broadcast-file \"\": %v %v %q", err, off.killSwitches, off.broadcastFile)
	}
	for _, bad := range []struct {
		switches string
		flags    int64
	}{{"Wallet", 4}, {"", 0}, {"", 8}} {
		c := config{broadcastFlags: bad.flags}
		if err := applySystemFlags(&c, bad.switches, map[string]bool{}); err == nil {
			t.Errorf("no error for %+v", bad)
		}
	}

	file := filepath.Join(t.TempDir(), "broadcast.txt")
	if err := os.WriteFile(file, []byte("# maintenance\n@6 Server restart at 20:00 UTC\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: logDir, killSwitches: cfg.killSwitches,
		broadcastFile: file, broadcastFlags: 4, onlineCount: true, onlineCountEvery: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	bc, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", in.ports[1]))
	if err != nil {
		t.Fatal(err)
	}
	r := fire.NewReader(bc)
	send := func(cmd uint16, id uint32, payload []byte) {
		b, _ := fire.Encode(fire.Frame{Component: 1, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
		if _, err := bc.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	send(0x28, 1, tdf.NewWriter().String("MAIL", "player@example.com").String("PASS", testPassword).String("TOKN", "").Int("TYPE", 0).Bytes())
	send(0x6E, 2, tdf.NewWriter().String("PNAM", "player").Bytes())
	var tags []string
	for len(tags) < 4 {
		bc.SetReadDeadline(time.Now().Add(5 * time.Second))
		f, err := r.Next()
		if err != nil {
			t.Fatalf("after %v: %v", tags, err)
		}
		if f.Component != 0x0F || f.Command != 1 || f.Type != fire.Notification {
			continue // the replies, UserAuthenticated, UserSessionExtendedDataUpdate
		}
		st, err := tdf.Decode(f.Payload)
		if err != nil {
			t.Fatal(err)
		}
		pyld, _ := st.Sub("PYLD")
		typ, _ := pyld.Int("TYPE")
		tag, _ := pyld.Int("TAG")
		attr, _ := pyld.Get("ATTR")
		var kv []string
		for _, e := range attr.Value.(*tdf.Map).Entries {
			kv = append(kv, fmt.Sprintf("%#x=%v", e.Key, e.Value))
		}
		tags = append(tags, fmt.Sprintf("%d/%d %s", typ, tag, strings.Join(kv, " ")))
	}
	bc.Close()
	want := []string{"300/0 0x2=1 0x8=1", "300/1 ", "300/2 0xb0000=1 0xb0001=6 0xb0002=604800 0xb0003=Server restart at 20:00 UTC",
		"300/2 0xb0000=100 0xb0001=4 0xb0002=604800 0xb0003=1 player online"} // ONLINECOUNT: the count after the operator's lines
	if fmt.Sprint(tags) != fmt.Sprint(want) {
		t.Errorf("system messages\n got %q\nwant %q", tags, want)
	}
	time.Sleep(100 * time.Millisecond) // let the handler log the close
	in.stop()
	files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
	raw, _ := os.ReadFile(files[0])
	for _, w := range []string{
		"[stub] system messages (B14): kill switches 2 WalletTopUp, 8 CustomGames; broadcasts: 0 from --broadcast, then the lines of " + file,
		`broadcasts: 1 "Server restart at 20:00 UTC" (flags 6); sent to 0 logged-in connections`,
		"(read every 2s; edit it to announce, delete it to stop); the players-online count in the marquee (a change pushed at most every 30s)",
		`broadcasts 1; online count "1 player online"`,
	} {
		if !strings.Contains(string(raw), w) {
			t.Errorf("log lacks %q:\n%s", w, raw)
		}
	}
}
