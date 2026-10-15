package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup"
)

// Start-Server as a dedicated server with tunnel = off, the case dedicated adds (with the tunnel no player copy is
// made anyway): only the match-host copy is set up (no client\ folder) and the server runs; Start-Client on the same
// folder makes its own player copy, which the next dedicated start leaves alone; a complete folder starts without the
// archive. The flag and the ini key do the same.
func TestStartServerDedicated(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.1's server ports")
	}
	dir := release(t)
	ini := filepath.Join(dir, ServerConfigName)
	run(t, RunServer, dir, "--check", "--non-interactive") // writes the config file
	if raw, _ := os.ReadFile(ini); !strings.Contains(string(raw), "dedicated = no") {
		t.Errorf("the config file has no dedicated option:\n%s", raw)
	}
	SetKey(ini, "address", testAddress)
	SetKey(ini, "tunnel", TunnelOff)
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}

	// the flag
	code, out := run(t, RunServer, dir, "--non-interactive", "--dedicated", "--seconds", "1")
	if code != 0 || !strings.Contains(out, "==== The server runs on "+testAddress+". ====") || !strings.Contains(out, "Server stopped.") {
		t.Fatalf("Start-Server --dedicated: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "mode     dedicated server (the match-host copy only, no player copy)") ||
		!strings.Contains(out, "[skipped]  client copy    a dedicated server") {
		t.Errorf("no dedicated notice in:\n%s", out)
	}
	for _, step := range []string{"certificates", "host copy", "host exe", "host config", "host DLL", "content"} {
		if !strings.Contains(out, "[done]     "+step) {
			t.Errorf("no [done] %s", step)
		}
	}
	for _, step := range []string{"client exe", "client config", "client texts"} {
		if strings.Contains(out, step) {
			t.Errorf("step %q ran for a dedicated server", step)
		}
	}
	if exists(filepath.Dir(l.ClientCopy())) {
		t.Errorf("%s exists", filepath.Dir(l.ClientCopy()))
	}
	logs, _ := filepath.Glob(filepath.Join(l.Logs(), "*.log"))
	if len(logs) != 1 {
		t.Fatalf("logs %v", logs)
	}
	if b, _ := os.ReadFile(logs[0]); !strings.Contains(string(b), "match hosts "+filepath.Join(l.HostCopy(), "game", "Dawngate.exe")) {
		t.Errorf("server log:\n%s", b)
	}

	// Start-Client on the same folder makes its own player copy (it follows tunnel = off); the next dedicated start
	// leaves it alone
	if code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only"); code != 0 || !strings.Contains(out, "[done]     client copy") {
		t.Fatalf("Start-Client --setup-only: exit %d\n%s", code, out)
	}
	exe := filepath.Join(l.ClientCopy(), "game", "Dawngate.exe")
	if err := os.WriteFile(exe, []byte("changed by someone else"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out = run(t, RunServer, dir, "--non-interactive", "--dedicated", "--setup-only"); code != 0 || strings.Contains(out, "[done]") {
		t.Errorf("a second dedicated start: exit %d\n%s", code, out)
	}
	if b, _ := os.ReadFile(exe); string(b) != "changed by someone else" {
		t.Error("the dedicated start touched the player copy")
	}
	if err := os.RemoveAll(filepath.Dir(l.ClientCopy())); err != nil {
		t.Fatal(err)
	}

	// the ini key, and no archive any more: the dedicated folder is complete without it
	if err := os.Remove(filepath.Join(dir, "Dawngate.zip")); err != nil {
		t.Fatal(err)
	}
	SetKey(ini, "dedicated", "yes")
	code, out = run(t, RunServer, dir, "--non-interactive", "--seconds", "1")
	if code != 0 || !strings.Contains(out, "==== The server runs on "+testAddress+". ====") || strings.Contains(out, "[done]") ||
		!strings.Contains(out, "mode     dedicated server") {
		t.Fatalf("second dedicated start without the archive: exit %d\n%s", code, out)
	}
	if code, out = run(t, RunServer, dir, "--non-interactive", "--check"); code != 0 {
		t.Errorf("--check: exit %d\n%s", code, out)
	}

	// without the option, tunnel = off needs the player copy (and the archive is gone); with the tunnel (the default)
	// the same folder is complete: Start-Server makes no player copy then anyway
	SetKey(ini, "dedicated", "no")
	if code, out = run(t, RunServer, dir, "--non-interactive", "--check"); code != 1 || !strings.Contains(out, "[MISSING]  client copy") {
		t.Errorf("--check without dedicated: exit %d\n%s", code, out)
	}
	SetKey(ini, "tunnel", "auto")
	if code, out = run(t, RunServer, dir, "--non-interactive", "--check"); code != 0 || !strings.Contains(out, "[skipped]  client copy") {
		t.Errorf("--check with the tunnel: exit %d\n%s", code, out)
	}

	SetKey(ini, "dedicated", "maybe")
	if code, out = run(t, RunServer, dir, "--non-interactive", "--check"); code != 1 || !strings.Contains(out, `dedicated = "maybe": use on or off`) {
		t.Errorf("a bad dedicated: exit %d\n%s", code, out)
	}
}
