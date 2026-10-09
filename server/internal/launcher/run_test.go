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
	for _, step := range []string{"certificates", "client copy", "client exe", "client config", "host copy", "host exe",
		"host config", "host DLL", "content"} {
		if !strings.Contains(out, "[done]     "+step) {
			t.Errorf("no [done] %s", step)
		}
	}
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	exe, _ := os.ReadFile(filepath.Join(l.ClientCopy(), "game", "Dawngate.exe"))
	if d, _ := exepatch.Describe(exe); d["redirector"] != testAddress {
		t.Errorf("client exe %v", d)
	}
	logs, _ := filepath.Glob(filepath.Join(l.Logs(), "*.log"))
	if len(logs) != 1 {
		t.Fatalf("logs %v", logs)
	}
	raw, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(raw), "the CA modulus at /dawngate/ca_modulus.bin") ||
		!strings.Contains(string(raw), "match hosts "+filepath.Join(l.HostCopy(), "game", "Dawngate.exe")) {
		t.Errorf("server log:\n%s", raw)
	}
	// a second start finds everything current
	code, out = run(t, RunServer, dir, "--non-interactive", "--setup-only")
	if code != 0 || strings.Contains(out, "[done]") {
		t.Errorf("second start: exit %d\n%s", code, out)
	}
	// Start-Client: the server address from Start-Server.ini, this PC's CA; no server running now
	code, out = run(t, RunClient, dir, "--non-interactive")
	if code != 1 || !strings.Contains(out, "No server is running on "+testAddress) {
		t.Errorf("Start-Client without a server: exit %d\n%s", code, out)
	}
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only")
	if code != 0 || !strings.Contains(out, "[ok]       client exe") || !strings.Contains(out, "server   "+testAddress) {
		t.Errorf("Start-Client --setup-only: exit %d\n%s", code, out)
	}
	code, out = run(t, RunClient, dir, "--non-interactive", "--check", "127.0.9.2")
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
	SetKey(cfg, "address", "8.8.8.8")
	if code, out := run(t, RunServer, dir, "--non-interactive"); code != 1 || !strings.Contains(out, "address = 8.8.8.8") {
		t.Errorf("a public address: exit %d\n%s", code, out)
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
	for _, want := range []string{`--bind 192.168.1.20 --public-host 192.168.1.20`, `--certs D:\x\certs\192.168.1.20`,
		`--gms-dir D:\x\host\Dawngate`, `--gms-args=-multipleInstances`, `--gms-control-bind 127.0.0.1`, `--serve-ca`,
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
