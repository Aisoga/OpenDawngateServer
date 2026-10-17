package launcher

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/exepatch"
	"dawngate/server/internal/setup/synthzip"
	"dawngate/server/internal/tunnel"
)

// testAddress is the loopback address the end-to-end test runs the server on: no test lane uses it
// (the test lanes use 127.0.0.1-127.0.0.4), and the control port binds it too (ControlBind).
const testAddress = "127.0.9.1"

func writeZip(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	p := filepath.Join(dir, "Dawngate.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, data := range files {
		ew, _ := w.Create(name)
		ew.Write([]byte(data))
	}
	w.Close()
	f.Close()
	return p
}

// release makes a release folder in a temp dir: the host folder (a fake dgsim.dll, two Lua probes), the synthetic
// Dawngate.zip next to it, and the synthetic manifest for the setup.
func release(t *testing.T) string {
	t.Helper()
	// a test lane's game on this PC holds the client mutex; the tests' copies are synthetic and never collide with it
	orig := clientRunning
	clientRunning = func() bool { return false }
	t.Cleanup(func() { clientRunning = orig })
	dir := filepath.Join(t.TempDir(), "release")
	if err := os.MkdirAll(filepath.Join(dir, "host", "lua"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "host", "dgsim.dll"), []byte("MZ fake dgsim.dll loaded"), 0o644)
	os.WriteFile(filepath.Join(dir, "host", "lua", "dgsim_bots.lua"), []byte("-- bots\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "host", "lua", "dgsim_probe.lua"), []byte("-- probe\n"), 0o644)
	_, m := synthzip.Zip(t, dir)
	manifest = m
	t.Cleanup(func() { manifest = nil })
	return dir
}

func run(t *testing.T, f func([]string, IO) int, exeDir string, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := f(args, IO{In: strings.NewReader(""), Out: &out, ExeDir: exeDir})
	return code, out.String()
}

// Start-Server's whole path on a release folder: the config file written, the setup done from the zip next to it,
// the server started on 127.0.9.1 and stopped; then Start-Client's check and setup against it.
func TestStartServerAndClient(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.1's server ports")
	}
	dir := release(t)
	// --check on a fresh folder: the config file is written, nothing else
	code, out := run(t, RunServer, dir, "--check", "--non-interactive")
	if code != 1 || !strings.Contains(out, "wrote "+filepath.Join(dir, ServerConfigName)) {
		t.Fatalf("--check: exit %d\n%s", code, out)
	}
	if exists(filepath.Join(dir, DefaultFolder)) {
		t.Error("--check made the setup folder")
	}
	if err := SetKey(filepath.Join(dir, ServerConfigName), "address", testAddress); err != nil {
		t.Fatal(err)
	}
	code, out = run(t, RunServer, dir, "--non-interactive", "--seconds", "1")
	if code != 0 || !strings.Contains(out, "==== The server runs on "+testAddress+". ====") ||
		!strings.Contains(out, "Server stopped.") {
		t.Fatalf("Start-Server: exit %d\n%s", code, out)
	}
	// the website on the default port 443 of the test address, self-signed; a Linux run without root (CI) can't bind a
	// port below 1024: then the server starts without it and says so
	if strings.Contains(out, "listen tcp4 "+testAddress+":443: bind: permission denied") {
		t.Log("port 443 needs root here: the website's check is skipped")
	} else if !strings.Contains(out, "The website: https://"+testAddress+"/") || !strings.Contains(out, "self-signed") {
		t.Errorf("no website in the banner:\n%s", out)
	}
	// with the tunnel (the default) Start-Server leaves the player copy to Start-Client
	for _, step := range []string{"certificates", "host copy", "host exe", "host config", "host DLL", "content"} {
		if !strings.Contains(out, "[done]     "+step) {
			t.Errorf("no [done] %s", step)
		}
	}
	if !strings.Contains(out, "[skipped]  client copy") {
		t.Errorf("the client copy not left to Start-Client:\n%s", out)
	}
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	if exists(l.ClientCopy()) {
		t.Error("Start-Server made a player copy")
	}
	logs, _ := filepath.Glob(filepath.Join(l.Logs(), "*.log"))
	if len(logs) != 1 {
		t.Fatalf("logs %v", logs)
	}
	raw, _ := os.ReadFile(logs[0])
	for _, want := range []string{"no plain listeners (--plain off)", "tunnel gateway on " + testAddress + ":42128",
		"match hosts " + filepath.Join(l.HostCopy(), "game", "Dawngate.exe")} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("server log lacks %q:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), "ca_modulus.bin") {
		t.Errorf("the CA served with the tunnel:\n%s", raw)
	}
	// a second start finds everything current
	code, out = run(t, RunServer, dir, "--non-interactive", "--setup-only")
	if code != 0 || strings.Contains(out, "[done]") {
		t.Errorf("second start: exit %d\n%s", code, out)
	}
	// Start-Client in the same folder: nothing added to the list, This PC is the folder's server (Start-Client's own
	// while the Start-Server doesn't run); the player copy patched for the tunnel address, its links through the front
	ownPin, err := tunnel.PinFromCertFile(filepath.Join(l.Certs(testAddress), tunnel.CertFile))
	if err != nil {
		t.Fatal(err)
	}
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", "127.0.9.2")
	if code != 0 || strings.Contains(out, "added to the server list") ||
		!strings.Contains(out, "#pc     This PC: the server on this PC, run by Start-Client on 127.0.9.11 (it starts when you log in to it; without passwords (local_trust): any login name logs in)") ||
		!strings.Contains(out, "links at http://127.0.9.2:18080/dawngate/") {
		t.Errorf("Start-Client --setup-only: exit %d\n%s", code, out)
	}
	exe, _ := os.ReadFile(filepath.Join(l.ClientCopy(), "game", "Dawngate.exe"))
	if d, _ := exepatch.Describe(exe); d["redirector"] != "127.0.9.2" {
		t.Errorf("client exe %v", d)
	}
	// while the folder's Start-Server runs (its run lock, here held for another live process), This PC is that server,
	// its key trusted from the lock (no question)
	holdRunLock(t, l, setup.RunLock{Program: "Start-Server", Address: testAddress, GatewayPort: 42128, State: setup.RunRunning,
		Pin: ownPin.String()})
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", "127.0.9.2")
	if code != 0 || !strings.Contains(out, "#pc     This PC: this folder's Start-Server ("+testAddress+":42128, running)") ||
		!strings.Contains(out, "its key "+ownPin.Fingerprint()+" trusted (the server of this folder)") {
		t.Errorf("Start-Client beside the folder's running Start-Server: exit %d\n%s", code, out)
	}
	if raw, _ := os.ReadFile(TrustedGateway(l, testAddress)); strings.TrimSpace(string(raw)) != ownPin.String() {
		t.Errorf("the own key: %q", raw)
	}
	// a second Start-Server on the folder refuses while that one runs
	if code, out = run(t, RunServer, dir, "--non-interactive", "--setup-only"); code != 1 ||
		!strings.Contains(out, "This folder's server already runs in Start-Server") {
		t.Errorf("Start-Server beside the folder's running server: exit %d\n%s", code, out)
	}
	os.Remove(filepath.Join(l.Root, setup.RunLockFile))
	// web_domain doesn't touch the player copy: its links go through the front to the server's HTTP port, which
	// redirects to the website; Start-Server's next start leaves the copy alone
	if err := SetKey(filepath.Join(dir, ServerConfigName), "web_domain", "Play.Example.org"); err != nil {
		t.Fatal(err)
	}
	code, out = run(t, RunServer, dir, "--non-interactive", "--setup-only")
	if code != 0 || strings.Contains(out, "[done]") || strings.Contains(out, "client texts") {
		t.Errorf("Start-Server with web_domain: exit %d\n%s", code, out)
	}
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--tunnel-address", "127.0.9.2")
	if code != 0 || !strings.Contains(out, "[ok]       client texts") || strings.Contains(out, "trusted") {
		t.Errorf("Start-Client after web_domain: exit %d\n%s", code, out)
	}
	// tunnel = off (developers): Start-Server sets up the direct player copy and serves its CA
	SetKey(filepath.Join(dir, ServerConfigName), "web_domain", "")
	SetKey(filepath.Join(dir, ServerConfigName), "tunnel", TunnelOff)
	code, out = run(t, RunServer, dir, "--non-interactive", "--seconds", "1")
	if code != 0 || !strings.Contains(out, "[done]     client exe") || !strings.Contains(out, "Now double-click Start-Client.exe to play (without the tunnel") {
		t.Errorf("Start-Server with tunnel = off: exit %d\n%s", code, out)
	}
	logs, _ = filepath.Glob(filepath.Join(l.Logs(), "*.log"))
	all := ""
	for _, f := range logs {
		b, _ := os.ReadFile(f)
		all += string(b)
	}
	if !strings.Contains(all, "the CA modulus at /dawngate/ca_modulus.bin") {
		t.Errorf("tunnel = off: no CA served:\n%s", all)
	}
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only")
	if code != 0 || !strings.Contains(out, "tunnel   off") || !strings.Contains(out, "server   "+testAddress) {
		t.Errorf("Start-Client beside tunnel = off: exit %d\n%s", code, out)
	}
	code, out = run(t, RunClient, dir, "--non-interactive", "--check", "--tunnel", "off", "127.0.9.2")
	if code != 1 || !strings.Contains(out, "No server is set up on this PC for 127.0.9.2") {
		t.Errorf("Start-Client for an unknown local server: exit %d\n%s", code, out)
	}
}

func TestStartServerRefusals(t *testing.T) {
	dir := release(t)
	cfg := filepath.Join(dir, ServerConfigName)
	if _, _, err := LoadOrCreate(cfg, ServerConfigText); err != nil {
		t.Fatal(err)
	}
	SetKey(cfg, "address", "8.8.8.8") // public is allowed, but only with the acknowledgement
	if code, out := run(t, RunServer, dir, "--non-interactive"); code != 1 || !strings.Contains(out, "A PUBLIC SERVER: 8.8.8.8") ||
		!strings.Contains(out, "risk is not accepted") || !strings.Contains(out, "only TCP and UDP 42128 (the tunnel) and TCP 443") {
		t.Errorf("a public address without the acknowledgement: exit %d\n%s", code, out)
	}
	if code, out := run(t, RunServer, dir, "--non-interactive", "--i-accept-the-risk"); code != 1 ||
		strings.Contains(out, "risk is not accepted") || !strings.Contains(out, "is not an address of this PC") {
		t.Errorf("a public address with the flag (this PC can't bind it): exit %d\n%s", code, out)
	}
	SetKey(cfg, "i_accept_the_risk", "yes")
	if code, out := run(t, RunServer, dir, "--non-interactive"); code != 1 || strings.Contains(out, "risk is not accepted") {
		t.Errorf("a public address with the ini key: exit %d\n%s", code, out)
	}
	SetKey(cfg, "i_accept_the_risk", "no")
	SetKey(cfg, "address", "224.0.0.1")
	if code, out := run(t, RunServer, dir, "--non-interactive"); code != 1 || !strings.Contains(out, "address = 224.0.0.1") {
		t.Errorf("a multicast address: exit %d\n%s", code, out)
	}
	SetKey(cfg, "address", testAddress)
	SetKey(cfg, "shaper_select", "maybe")
	if code, out := run(t, RunServer, dir, "--non-interactive"); code != 1 || !strings.Contains(out, "use on or off") {
		t.Errorf("a bad on/off: exit %d\n%s", code, out)
	}
	SetKey(cfg, "shaper_select", "on")
	os.Remove(filepath.Join(dir, "Dawngate.zip"))
	SetKey(cfg, "zip", "nowhere.zip")
	code, out := run(t, RunServer, dir, "--non-interactive", "--setup-only")
	if code != 1 || !strings.Contains(out, "Dawngate.zip not found") || !strings.Contains(out, "archive.org") {
		t.Errorf("no zip: exit %d\n%s", code, out)
	}
	os.RemoveAll(filepath.Join(dir, "host"))
	if code, out := run(t, RunServer, dir, "--non-interactive"); code != 1 || !strings.Contains(out, "has no dgsim.dll") {
		t.Errorf("no host folder: exit %d\n%s", code, out)
	}
}

func TestServerFlags(t *testing.T) {
	l := setup.Layout{Root: `D:\x`}
	got := strings.Join(ServerFlags(l, "192.168.1.20", true, []string{"--owned", "starter"}), " ")
	// the paths are joined with the running system's separator (a Linux build joins D:\x/certs/...)
	for _, want := range []string{`--bind 192.168.1.20 --public-host 192.168.1.20`, `--certs ` + filepath.Join(l.Root, "certs", "192.168.1.20"),
		`--gms-dir ` + filepath.Join(l.Root, "host", "Dawngate"), `--gms-args=-multipleInstances`, `--gms-control-bind 127.0.0.1`,
		`--shaper-select --owned starter`} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %s", want, got)
		}
	}
	if got := strings.Join(ServerFlags(l, "127.0.9.1", false, nil), " "); !strings.Contains(got, "--gms-control-bind 127.0.9.1") ||
		strings.Contains(got, "--shaper-select") {
		t.Errorf("loopback: %s", got)
	}
}
