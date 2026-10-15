package launcher

import (
	"bytes"
	"net"
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
	"dawngate/server/internal/tunnel"
)

// samePC is the loopback address of the same-PC test: a Start-Server on it and a Start-Client's front on it, as both
// use 127.0.0.1 on a player's PC (no test lane uses 127.0.0.7).
const samePC = "127.0.0.7"

func TestHostsAddress(t *testing.T) {
	for in, want := range map[string]string{"127.0.0.1": "127.0.1.10", "127.0.0.2": "127.0.2.10", "127.0.0.7": "127.0.7.10",
		"127.0.2.9": "127.0.2.9", "127.0.9.1": "127.0.9.1", "192.168.1.20": "127.0.1.10", "203.0.113.7": "127.0.1.10"} {
		if got := HostsAddress(in); got != want {
			t.Errorf("HostsAddress(%s) = %s, want %s", in, got, want)
		}
	}
}

// A Start-Server with the tunnel and a Start-Client's front on one PC and one address: the server binds no plain
// listener there (dgserver --plain off), only its gateway, and its match hosts' GMS port on HostsAddress, so the front
// takes every game port of the same address; a login goes through the gateway to the services in process (the key
// confirmed once), and a datagram to the GMS port reaches the hosts' address through the gateway.
func TestSamePCServerAndFront(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.0.7's and 127.0.7.10's ports")
	}
	hosts := HostsAddress(samePC)
	dir := filepath.Join(t.TempDir(), "certs")
	if _, err := certs.Make(dir, samePC); err != nil {
		t.Fatal(err)
	}
	logSrv := t.TempDir()
	stop, started, done := make(chan struct{}), make(chan dgserver.Info, 1), make(chan int, 1)
	go func() {
		done <- dgserver.Run([]string{"--bind", samePC, "--public-host", samePC, "--certs", dir, "--log", logSrv, "--data", "",
			"--broadcast-file", "", "--tunnel", "on", "--gateway-port", "0", "--plain", "off", "--gms-bind", hosts,
			"--gms-port", "10042"}, dgserver.Hooks{Stop: stop,
			Started: func(i dgserver.Info) { started <- i }, StartFailed: func(error) { started <- dgserver.Info{} }})
	}()
	info := <-started
	if info.GatewayPort == 0 {
		t.Fatal("dgserver didn't start with a gateway")
	}
	defer func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("dgserver didn't stop")
		}
	}()
	if len(info.TCPPorts) != 1 || info.TCPPorts[0] != info.GatewayPort {
		t.Errorf("the server listens on TCP %v, want only its gateway %d", info.TCPPorts, info.GatewayPort)
	}
	// every game port of the address is free for the front
	gamePorts := []Port{{42127, false, ""}, {10041, false, ""}, {17502, false, ""}, {17502, true, ""}, {18080, false, ""},
		{9988, false, ""}, {10042, true, ""}}
	if held := HeldPorts(samePC, gamePorts); len(held) > 0 {
		t.Fatalf("the server holds game ports of %s: %v", samePC, held)
	}

	l := setup.Layout{Root: t.TempDir()}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bm.Add(Bookmark{Address: samePC, GatewayPort: info.GatewayPort}); err != nil {
		t.Fatal(err)
	}
	_, tlsCfg, err := tunnelCertSet(l, samePC, false)
	if err != nil {
		t.Fatal(err)
	}
	logFront := t.TempDir()
	lf, err := os.Create(filepath.Join(logFront, "front.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	f := &Front{Log: logging.New(lf), Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(samePC), RedirectorTLS: tlsCfg,
		ConnectTimeout: 3 * time.Second}
	if err := f.Start(); err != nil {
		t.Fatalf("the front beside the server on %s: %v", samePC, err)
	}
	defer f.Close()
	g := fakeGame{t: t, ip: samePC}
	g.redirect()
	c := g.dial(DefaultFrontPorts.Blaze)
	defer c.Close()
	g.start(c)
	// confirm once: the first login shows the key, the second trusts it and goes through
	if rep := g.call(c, 1, 0x28, 2, loginBody("tun1#a")); rep.Type != fire.ErrorReply || rep.Error != FrontErrNewKey {
		t.Fatalf("the first login: %v error %#x, want %#x\n%s", rep.Type, rep.Error, FrontErrNewKey, readLogs(t, logFront))
	}
	if rep := g.call(c, 1, 0x28, 3, loginBody("tun1#a")); rep.Type != fire.Reply {
		t.Fatalf("the second login: %v error %#x\n%s", rep.Type, rep.Error, readLogs(t, logFront))
	}
	waitLog(t, logSrv, "    MAIL: 'tun1'\n", 1)
	// a GMS datagram from the game to the front's port 10042 on the same address reaches the hosts' address
	game, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(samePC)})
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	for deadline := time.Now().Add(5 * time.Second); ; {
		game.WriteToUDP([]byte{0x05, 0, 0, 0, 0}, &net.UDPAddr{IP: net.ParseIP(samePC), Port: 10042})
		if strings.Contains(readLogs(t, logSrv), "[gms] udp ('"+hosts+"', ") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no GMS datagram from %s in the server's log:\n%s\nfront:\n%s", hosts, readLogs(t, logSrv), readLogs(t, logFront))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if front := readLogs(t, logFront); strings.Contains(front, "in use") {
		t.Errorf("a front port was in use:\n%s", front)
	}
	if srv := readLogs(t, logSrv); !strings.Contains(srv, "no plain listeners (--plain off)") || !strings.Contains(srv, "GMS (UDP, logged) on "+hosts+":10042") {
		t.Errorf("the server's start-up lines:\n%s", srv)
	}
}

// Start-Server.ini and Start-Client.ini of earlier releases: the tunnel's (and the picker's) comments replaced by the
// current ones, the values and every other line kept; a current file is left alone.
func TestIniMigration(t *testing.T) {
	dir := t.TempDir()
	cur := commentOf(ServerConfigText, "tunnel")
	if len(cur) != 6 || !strings.HasPrefix(cur[0], "; The encrypted tunnel for the players'") {
		t.Fatalf("the current tunnel comment: %q", cur)
	}
	for i, old := range oldServerTunnelComments {
		text := strings.Replace(ServerConfigText, strings.Join(cur, "\n"), strings.Join(old, "\n"), 1)
		text = strings.Replace(text, "tunnel = auto", "tunnel = off", 1) // an explicit off is kept
		path := filepath.Join(dir, "Start-Server"+strconv.Itoa(i)+".ini")
		os.WriteFile(path, []byte("\xef\xbb\xbf"+strings.ReplaceAll(text, "\n", "\r\n")), 0o644)
		changed, err := migrateComment(path, oldServerTunnelComments, cur)
		if err != nil || !changed {
			t.Fatalf("release %d's ini: changed %v, %v", i, changed, err)
		}
		b, _ := os.ReadFile(path)
		want := "\xef\xbb\xbf" + strings.ReplaceAll(strings.Replace(ServerConfigText, "tunnel = auto", "tunnel = off", 1), "\n", "\r\n")
		if string(b) != want {
			t.Errorf("release %d's ini after the migration:\n%s", i, b)
		}
		if changed, _ := migrateComment(path, oldServerTunnelComments, cur); changed {
			t.Errorf("release %d's ini migrated twice", i)
		}
		if c, err := ReadConfig(path); err != nil || c["tunnel"] != "off" {
			t.Errorf("the tunnel value: %q %v", c["tunnel"], err)
		}
	}
	// Start-Client.ini as SP4's release wrote it
	oldClient := ClientConfigText
	for _, k := range []string{"tunnel", "picker"} {
		oldClient = strings.Replace(oldClient, strings.Join(commentOf(ClientConfigText, k), "\n"), strings.Join(oldClientComments[k][0], "\n"), 1)
	}
	if oldClient == ClientConfigText {
		t.Fatal("no old client text")
	}
	rel := release(t)
	os.WriteFile(filepath.Join(rel, ClientConfigName), []byte(strings.ReplaceAll(oldClient, "\n", "\r\n")), 0o644)
	code, out := run(t, RunClient, rel, "--non-interactive", "--setup-only", "--tunnel-address", frontLocal)
	if code != 0 || !strings.Contains(out, "the encrypted tunnel is now used for every server") {
		t.Errorf("Start-Client with SP4's ini: exit %d\n%s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(rel, ClientConfigName)); string(b) != strings.ReplaceAll(ClientConfigText, "\n", "\r\n") {
		t.Errorf("Start-Client.ini after the migration:\n%s", b)
	}
	// this folder's Start-Server with tunnel = off: Start-Client follows it (the direct path)
	os.WriteFile(filepath.Join(rel, ServerConfigName), []byte("address = 127.0.9.8\r\ntunnel = off\r\n"), 0o644)
	code, out = run(t, RunClient, rel, "--non-interactive", "--setup-only")
	if code != 1 || !strings.Contains(out, "tunnel   off ("+ServerConfigName+" has tunnel = off)") ||
		!strings.Contains(out, "No server is set up on this PC for 127.0.9.8") {
		t.Errorf("Start-Client beside a Start-Server with tunnel = off: exit %d\n%s", code, out)
	}
}

// This PC's Start-Server in the same folder: Start-Client trusts its key without the confirmation (it is in the
// folder), also again after the server was set up anew; a server in another folder isn't.
func TestOwnServerKey(t *testing.T) {
	exeDir := t.TempDir()
	l := setup.Layout{Root: filepath.Join(exeDir, DefaultFolder)}
	scfg := Config{"address": "203.0.113.7", "bind": "192.168.1.20"}
	if _, _, ok := ownServerKey(scfg, exeDir, l); ok {
		t.Error("an own key before the server's first start")
	}
	if err := tunnel.WriteIdentity(l.Certs("203.0.113.7")); err != nil {
		t.Fatal(err)
	}
	addrs, pin, ok := ownServerKey(scfg, exeDir, l)
	if !ok || strings.Join(addrs, ",") != "203.0.113.7,192.168.1.20" {
		t.Fatalf("own key: %v %v", addrs, ok)
	}
	if _, _, ok := ownServerKey(Config{"address": "203.0.113.7", "folder": `D:\elsewhere`}, exeDir, l); ok {
		t.Error("a Start-Server with another folder counted as this PC's")
	}
	bm, _ := LoadBookmarks(BookmarksPath(l))
	bm.Add(ownBookmark("203.0.113.7", 42128, scfg)) // the bind address, the public one as its name
	bm.Add(Bookmark{Address: "198.51.100.3"})
	var out bytes.Buffer
	if err := pinOwnServer(IO{Out: &out}, l, bm, addrs, pin, false); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(TrustedGateway(l, "192.168.1.20")); strings.TrimSpace(string(raw)) != pin.String() ||
		!strings.Contains(out.String(), "#a is this PC's Start-Server: its key "+pin.Fingerprint()+" trusted") {
		t.Errorf("the own key not trusted: %q\n%s", raw, out.String())
	}
	if _, err := os.Stat(TrustedGateway(l, "198.51.100.3")); err == nil {
		t.Error("another server's key trusted")
	}
	out.Reset()
	pinOwnServer(IO{Out: &out}, l, bm, addrs, pin, false)
	if out.Len() != 0 {
		t.Errorf("trusted again: %s", out.String())
	}
	// set up anew: the folder's new key replaces the old one
	os.RemoveAll(l.Certs("203.0.113.7"))
	tunnel.WriteIdentity(l.Certs("203.0.113.7"))
	_, pin2, _ := ownServerKey(scfg, exeDir, l)
	pinOwnServer(IO{Out: &out}, l, bm, addrs, pin2, false)
	if raw, _ := os.ReadFile(TrustedGateway(l, "192.168.1.20")); strings.TrimSpace(string(raw)) != pin2.String() {
		t.Errorf("the own server's new key isn't trusted: %q", raw)
	}
}
