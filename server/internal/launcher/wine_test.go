package launcher

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dawngate/server/internal/setup"
)

// linuxServer turns on the Linux server's path (wine.go) for one test, with a stand-in for the Wine step that reports
// fail's line (or [ok] with a fake wine) and records its options.
func linuxServer(t *testing.T, fail *setup.Line) *[]setup.WineOptions {
	t.Helper()
	var calls []setup.WineOptions
	orig, origWine := hostsUnderWine, prepareWine
	hostsUnderWine = true
	prepareWine = func(s *setup.Setup, o setup.WineOptions) (setup.WineEnv, error) {
		calls = append(calls, o)
		env := setup.WineEnv{Wine: "/usr/bin/wine", Prefix: o.Prefix, Version: "wine-8.0 (test)"}
		if env.Prefix == "" {
			env.Prefix = s.WinePrefix()
		}
		if fail != nil {
			s.Report(fail.Status, fail.Step, fail.Detail)
			return env, setup.ErrIncomplete
		}
		return env, nil
	}
	t.Cleanup(func() { hostsUnderWine, prepareWine = orig, origWine })
	t.Setenv("WINEPREFIX", "")
	os.Unsetenv("WINEPREFIX")
	return &calls
}

func TestServerHostsAddress(t *testing.T) {
	linuxServer(t, nil)
	for _, in := range []string{"127.0.0.1", "127.0.9.1", "192.168.1.20", "203.0.113.7"} {
		if got := serverHostsAddress(in); got != "127.0.0.1" {
			t.Errorf("under Wine serverHostsAddress(%s) = %s, want 127.0.0.1", in, got)
		}
	}
	hostsUnderWine = false
	if got := serverHostsAddress("192.168.1.20"); got != HostsAddress("192.168.1.20") {
		t.Errorf("on Windows serverHostsAddress = %s", got)
	}
}

// The config file and the texts on Linux: start-server, Linux's paths and tools, the wine key; the Windows text
// unchanged.
func TestLinuxTexts(t *testing.T) {
	if serverConfigText() != ServerConfigText && !hostsUnderWine {
		t.Error("the Windows config text changed")
	}
	linuxServer(t, nil)
	text := serverConfigText()
	for _, want := range []string{"start-server --make-invite", "/srv/dawngate/Dawngate-Data", "Dawngate-Data/data/site",
		"\nwine =\n", "this machine only: a test", "\"dgserver -h\""} {
		if !strings.Contains(text, want) {
			t.Errorf("the Linux config text lacks %q", want)
		}
	}
	for _, not := range []string{"Start-Server.exe", `D:\`, "dgserver.exe"} {
		if strings.Contains(text, not) {
			t.Errorf("the Linux config text has %q", not)
		}
	}
	cfg := Config{}
	for _, l := range strings.Split(text, "\n") { // the same keys as on Windows, plus wine
		if k, _, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(strings.TrimSpace(l), ";") {
			cfg[strings.TrimSpace(k)] = ""
		}
	}
	if u := cfg.Unknown(serverKeys...); len(u) != 0 {
		t.Errorf("unknown keys %v", u)
	}
	if _, ok := cfg["wine"]; !ok {
		t.Error("no wine key")
	}
	var b strings.Builder
	w := linuxWriter{&b}
	in := "Start-Server.exe --check (ipconfig shows it as IPv4 Address)"
	n, err := w.Write([]byte(in))
	if err != nil || n != len(in) || b.String() != "start-server --check (ip -4 addr shows it)" {
		t.Errorf("linuxWriter: %d %v %q", n, err, b.String())
	}
}

// Start-Server's Linux path on a release folder: the Wine step first, a dedicated setup (no player copy, also with
// tunnel = off), the setup folder private, the hosts under the checked Wine (--gms-runner) on 127.0.0.1 in its
// prefix (WINEPREFIX), the texts for Linux; without Wine nothing is set up and the message says what to install.
func TestStartServerLinux(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.1's server ports and 127.0.0.1's hosts ports")
	}
	missing := setup.Line{Status: setup.Missing, Step: "wine", Detail: "not installed (no wine on the PATH)"}
	calls := linuxServer(t, &missing)
	dir := release(t)
	ini := filepath.Join(dir, ServerConfigName)
	code, out := run(t, RunServer, dir, "--non-interactive", "--setup-only")
	if code != 1 || !strings.Contains(out, "Wine isn't ready for the match hosts") || !strings.Contains(out, "sudo apt install") ||
		!strings.Contains(out, "wine32") || strings.Contains(out, "[done]     host copy") {
		t.Fatalf("without Wine: exit %d\n%s", code, out)
	}
	if raw, _ := os.ReadFile(ini); !strings.Contains(string(raw), "\nwine =") || strings.Contains(string(raw), "Start-Server.exe") {
		t.Errorf("the config file isn't Linux's:\n%s", raw)
	}
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	if exists(l.HostCopy()) {
		t.Error("a host copy without Wine")
	}

	calls = linuxServer(t, nil)
	SetKey(ini, "address", testAddress)
	SetKey(ini, "wine", "/opt/wine-stable/bin/wine")
	code, out = run(t, RunServer, dir, "--non-interactive", "--seconds", "1")
	if code != 0 || !strings.Contains(out, "==== The server runs on "+testAddress+". ====") || !strings.Contains(out, "Server stopped.") {
		t.Fatalf("Start-Server on Linux: exit %d\n%s", code, out)
	}
	if len(*calls) != 1 || (*calls)[0].Command != "/opt/wine-stable/bin/wine" || (*calls)[0].Prefix != "" {
		t.Errorf("the Wine step: %+v", *calls)
	}
	for _, want := range []string{"mode     dedicated server, the match hosts under Wine", "[skipped]  client copy    a Linux server",
		"[done]     host copy", "[done]     host DLL", "only this machine reaches it", "Stop the server with Ctrl+C or SIGTERM",
		"The match hosts run under wine-8.0 (test) (/usr/bin/wine) on 127.0.0.1, in the Wine prefix " + l.WinePrefix()} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Start-Server.exe") || strings.Contains(out, "double-click") {
		t.Errorf("Windows texts in:\n%s", out)
	}
	if exists(filepath.Dir(l.ClientCopy())) {
		t.Error("a player copy on Linux")
	}
	if os.Getenv("WINEPREFIX") != l.WinePrefix() {
		t.Errorf("WINEPREFIX %q", os.Getenv("WINEPREFIX"))
	}
	if st, err := os.Stat(l.Root); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o700) {
		t.Errorf("the setup folder: %v %v", st.Mode(), err)
	}
	logs, _ := filepath.Glob(filepath.Join(l.Logs(), "*.log"))
	if len(logs) != 1 {
		t.Fatalf("logs %v", logs)
	}
	raw, _ := os.ReadFile(logs[0])
	for _, want := range []string{"hosts run by /usr/bin/wine", "tunnel gateway on " + testAddress + ":42128"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("server log lacks %q:\n%s", want, raw)
		}
	}

	// tunnel = off: still no player copy; a WINEPREFIX of the environment is the hosts' prefix
	SetKey(ini, "tunnel", TunnelOff)
	t.Setenv("WINEPREFIX", filepath.Join(t.TempDir(), "prefix"))
	*calls = nil
	if code, out = run(t, RunServer, dir, "--non-interactive", "--setup-only"); code != 0 || strings.Contains(out, "client exe") {
		t.Errorf("tunnel = off: exit %d\n%s", code, out)
	}
	if len(*calls) != 1 || (*calls)[0].Prefix != os.Getenv("WINEPREFIX") {
		t.Errorf("the Wine step with WINEPREFIX: %+v", *calls)
	}
	if exists(filepath.Dir(l.ClientCopy())) {
		t.Error("a player copy on Linux with tunnel = off")
	}
	// --check reports the Wine step's result with the rest, and fails with it
	fail := setup.Line{Status: setup.Failed, Step: "wine 32-bit", Detail: "Wine can't run 32-bit programs"}
	linuxServer(t, &fail)
	if code, out = run(t, RunServer, dir, "--non-interactive", "--check"); code != 1 || !strings.Contains(out, "[FAILED]   wine 32-bit") ||
		!strings.Contains(out, "[ok]       host copy") {
		t.Errorf("--check without Wine's 32-bit part: exit %d\n%s", code, out)
	}
}
