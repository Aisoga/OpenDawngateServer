package launcher

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/tunnel"
)

// The host-machine tests' addresses: a main server and a host machine (another machine's each). No lane uses them.
const (
	regionMain = "127.0.9.60"
	regionHost = "127.0.9.61"
)

const testPin = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func TestReadRegions(t *testing.T) {
	root := t.TempDir()
	writeKey := func(name string) {
		if err := writeMachineKey(agentKeyPath(root, name), gmshost.NewAgentKey()); err != nil {
			t.Fatal(err)
		}
	}
	writeKey("na1")
	cases := []struct {
		cfg    Config
		tunnel bool
		flags  []string
		err    string
	}{
		{Config{}, true, nil, ""},
		{Config{"region": "eu"}, true, []string{"--region", "eu"}, ""},
		{Config{"region": "eu", "host_machines": " na1=na "}, true,
			[]string{"--region", "eu", "--host-machine", "na1=na:" + agentKeyPath(root, "na1")}, ""},
		{Config{"region": "EU"}, true, nil, "2-8 lower-case letters"},
		{Config{"host_machines": "na1=na"}, true, nil, "host_machines needs region"},
		{Config{"region": "eu", "host_machines": "na1=na"}, false, nil, "needs the tunnel"},
		{Config{"region": "eu", "host_machines": "na1"}, true, nil, "want name=region"},
		{Config{"region": "eu", "host_machines": "Na1=na"}, true, nil, "a machine's name"},
		{Config{"region": "eu", "host_machines": "na1=north"}, true, nil, ""}, // 5 letters: a region
		{Config{"region": "eu", "host_machines": "na1=n"}, true, nil, "a region is 2-8"},
		{Config{"region": "eu", "host_machines": "na1=na, na1=na"}, true, nil, "na1 twice"},
		{Config{"region": "eu", "host_machines": "na2=na"}, true, nil, "--new-host-machine na2 na makes it"},
	}
	for _, c := range cases {
		flags, note, err := RegionFlags(c.cfg, root, c.tunnel)
		switch {
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%v: error %v, want %q", c.cfg, err, c.err)
		case c.err == "" && err != nil:
			t.Errorf("%v: %v", c.cfg, err)
		case c.err == "" && c.flags != nil && !reflect.DeepEqual(flags, c.flags):
			t.Errorf("%v: flags %q, want %q", c.cfg, flags, c.flags)
		case c.err == "" && c.cfg["host_machines"] != "" && !strings.Contains(note, "na1 ("):
			t.Errorf("%v: note %q", c.cfg, note)
		}
	}
}

func hostMachineConfig(key string) Config {
	return Config{"role": "host-machine", "main_server": "203.0.113.7:42128", "main_pin": testPin, "machine_name": "na1",
		"machine_key": key, "address": "198.51.100.9"}
}

func TestReadHostMachine(t *testing.T) {
	key := gmshost.NewAgentKey()
	h, err := ReadHostMachine(hostMachineConfig(key), t.TempDir())
	want := HostMachine{Main: "203.0.113.7:42128", Pin: testPin, Name: "na1", Key: key, Address: "198.51.100.9", GatewayPort: 42128,
		GMSPorts: "10042-10051", LogDays: 30}
	if err != nil || h != want {
		t.Fatalf("defaults: %+v %v", h, err)
	}
	if hm, err := isHostMachine(hostMachineConfig(key)); !hm || err != nil {
		t.Errorf("role: %v %v", hm, err)
	}
	for _, role := range []string{"", "server", "Server"} {
		if hm, err := isHostMachine(Config{"role": role}); hm || err != nil {
			t.Errorf("role %q: %v %v", role, hm, err)
		}
	}
	if _, err := isHostMachine(Config{"role": "agent"}); err == nil {
		t.Error("role = agent taken")
	}
	// a name without a port gets the default gateway port; a key file next to the program; bind, ports, days
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "na1.key"), []byte(key+"\n"), 0o600)
	c := hostMachineConfig("na1.key")
	c["main_server"], c["bind"], c["gateway_port"], c["gms_ports"], c["log_days"] = "dawngate.example.org", "10.0.0.5", "42138", "10042-10045", "0"
	h, err = ReadHostMachine(c, dir)
	if err != nil || h.Main != "dawngate.example.org:42128" || h.KeyFile != filepath.Join(dir, "na1.key") || h.Key != "" ||
		h.bind() != "10.0.0.5" || h.GatewayPort != 42138 || h.GMSPorts != "10042-10045" || h.LogDays != 0 {
		t.Errorf("the other keys: %+v %v", h, err)
	}
	for k, v := range map[string]string{"main_server": "", "main_pin": "abc", "machine_name": "NA 1", "machine_key": "",
		"address": "", "bind": "a.example", "gateway_port": "0", "gms_ports": "x", "log_days": "-1"} {
		c := hostMachineConfig(key)
		c[k] = v
		if _, err := ReadHostMachine(c, dir); err == nil {
			t.Errorf("%s = %q taken", k, v)
		}
	}
	c = hostMachineConfig("missing.key")
	if _, err := ReadHostMachine(c, dir); err == nil || !strings.Contains(err.Error(), "neither 64 hex digits nor a key file") {
		t.Errorf("a missing key file: %v", err)
	}
	c = hostMachineConfig(key)
	c["address"] = "dawngate.example.org"
	if _, err := ReadHostMachine(c, dir); err == nil || !strings.Contains(err.Error(), "IPv4") {
		t.Errorf("a name as the address: %v", err)
	}
}

// HostMachineFlags is the one place the ini meets dgserver: agent mode over the TLS link, the hosts on their own
// address, the match gateway on bind and announced as the address.
func TestHostMachineFlags(t *testing.T) {
	l := setup.Layout{Root: filepath.Join("srv", "Dawngate-Data")}
	h := HostMachine{Main: "203.0.113.7:42128", Pin: testPin, Name: "na1", Address: "198.51.100.9", Bind: "10.0.0.5", GatewayPort: 42128,
		GMSPorts: "10042-10051", LogDays: 30}
	got := HostMachineFlags(h, l, "na1.key", "127.0.0.1", 10040, []string{"--gms-runner", "/usr/bin/wine"})
	want := []string{"--agent-tls", "203.0.113.7:42128", "--agent-pin", testPin, "--agent-key", "na1.key", "--agent-name", "na1",
		"--gms-dir", l.HostCopy(), "--gms-args=-multipleInstances", "--gms-data", filepath.Join(l.Logs(), "hosts"),
		"--gms-ports", "10042-10051", "--gms-bind", "127.0.0.1", "--gms-control-port", "10040",
		"--log", l.Logs(), "--log-days", "30", "--certs", l.Certs("198.51.100.9"),
		"--bind", "10.0.0.5", "--public-host", "198.51.100.9",
		"--tunnel", "on", "--gateway-port", "42128", "--plain", "off", "--gms-runner", "/usr/bin/wine"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flags\n%q\nwant\n%q", got, want)
	}
	h.Bind = ""
	got = HostMachineFlags(h, l, "na1.key", "127.0.0.1", 10040, nil)
	if i := indexOf(got, "--bind"); i < 0 || got[i+1] != "198.51.100.9" {
		t.Errorf("without bind: %q", got)
	}
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// Start-Server --new-host-machine on the main server: the key (new, then kept), host_machines, the host machine's ini
// with the main server's address and pin, the notes for what is missing.
func TestNewHostMachine(t *testing.T) {
	dir := release(t)
	ini := filepath.Join(dir, ServerConfigName)
	if _, _, err := LoadOrCreate(ini, serverConfigText()); err != nil {
		t.Fatal(err)
	}
	SetKey(ini, "address", "203.0.113.7")
	root := filepath.Join(dir, DefaultFolder)
	code, out := run(t, RunServer, dir, "--non-interactive", "--new-host-machine", "na1", "na")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	keyB, err := gmshost.ReadAgentKey(agentKeyPath(root, "na1"))
	if err != nil || !hex64RE.Match(keyB) {
		t.Fatalf("the key: %q %v", keyB, err)
	}
	key := string(keyB)
	if st, _ := os.Stat(agentKeyPath(root, "na1")); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("the key file's mode %v", st.Mode())
	}
	if strings.Contains(out, key) {
		t.Errorf("the key is printed:\n%s", out)
	}
	for _, want := range []string{"Host machine na1 in the region na: a new key", "host_machines = na1=na",
		"note: set region in", "no tunnel key yet", "machine_name = na1", "machine_key = <the 64 hex digits in " + agentKeyPath(root, "na1") + ">",
		"main_server = 203.0.113.7:42128", "role = host-machine", hostMachineIniPath(root, "na1")} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	cfg, _ := ReadConfig(ini)
	if cfg["host_machines"] != "na1=na" {
		t.Errorf("host_machines = %q", cfg["host_machines"])
	}
	// the main server's key made (as at its first start): the second run keeps the machine's key and has the pin
	if err := tunnel.WriteIdentity(setup.Layout{Root: root}.Certs("203.0.113.7")); err != nil {
		t.Fatal(err)
	}
	pin, _ := tunnel.PinFromCertFile(filepath.Join(setup.Layout{Root: root}.Certs("203.0.113.7"), tunnel.CertFile))
	SetKey(ini, "region", "eu")
	code, out = run(t, RunServer, dir, "--non-interactive", "--new-host-machine", "na2", "na")
	if code != 0 || strings.Contains(out, "note: set region") || !strings.Contains(out, "main_pin = "+pin.String()) {
		t.Fatalf("na2: exit %d\n%s", code, out)
	}
	// an operator's copy with looser modes (Linux): the second run tightens the folder, the key and the ini
	if runtime.GOOS != "windows" {
		for p, m := range map[string]os.FileMode{filepath.Dir(agentKeyPath(root, "na1")): 0o755, agentKeyPath(root, "na1"): 0o644,
			hostMachineIniPath(root, "na1"): 0o644} {
			if err := os.Chmod(p, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	code, out = run(t, RunServer, dir, "--non-interactive", "--new-host-machine", "na1", "na")
	if code != 0 || !strings.Contains(out, "its key, kept") || strings.Contains(out, key) {
		t.Fatalf("na1 again: exit %d\n%s", code, out)
	}
	if runtime.GOOS != "windows" {
		for p, m := range map[string]os.FileMode{filepath.Dir(agentKeyPath(root, "na1")): 0o700, agentKeyPath(root, "na1"): 0o600,
			hostMachineIniPath(root, "na1"): 0o600} {
			if st, err := os.Stat(p); err != nil || st.Mode().Perm() != m {
				t.Errorf("%s: stat %v %v, want mode %v", p, st, err, m)
			}
		}
	}
	cfg, _ = ReadConfig(ini)
	if cfg["host_machines"] != "na1=na, na2=na" {
		t.Errorf("host_machines = %q", cfg["host_machines"])
	}
	// the written ini is a host machine's: with an address it reads as one, with this server's pin and the key
	hcfg, err := ReadConfig(hostMachineIniPath(root, "na1"))
	if err != nil {
		t.Fatal(err)
	}
	if u := hcfg.Unknown(hostMachineKeys...); len(u) > 0 {
		t.Errorf("unknown keys in the host machine's ini: %v", u)
	}
	hcfg["address"] = "198.51.100.9"
	if h, err := ReadHostMachine(hcfg, dir); err != nil || h.Pin != pin.String() || h.Key != key || h.Main != "203.0.113.7:42128" {
		t.Errorf("the host machine's ini: %+v %v", h, err)
	}
	// the main server takes both machines now
	tun := true
	if flags, _, err := RegionFlags(cfg, root, tun); err != nil || len(flags) != 6 {
		t.Errorf("RegionFlags: %q %v", flags, err)
	}
	// wrong uses
	for _, args := range [][]string{{"--new-host-machine", "na1"}, {"--new-host-machine", "NA", "na"}, {"--new-host-machine", "na3", "n"}} {
		if code, out := run(t, RunServer, dir, append([]string{"--non-interactive"}, args...)...); code == 0 {
			t.Errorf("%q: exit 0\n%s", args, out)
		}
	}
	SetKey(ini, "role", "host-machine")
	if code, out := run(t, RunServer, dir, "--non-interactive", "--new-host-machine", "na3", "na"); code == 0 ||
		!strings.Contains(out, "on the main server, not on a host machine") {
		t.Errorf("on a host machine: exit %d\n%s", code, out)
	}
}

// The main server with host_machines: refused without a key or without the tunnel, before any setup; with the key
// made by --new-host-machine, dgserver gets --region and --host-machine and the banner names them.
func TestStartServerRegions(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.60's server ports")
	}
	dir := release(t)
	ini := filepath.Join(dir, ServerConfigName)
	if _, _, err := LoadOrCreate(ini, serverConfigText()); err != nil {
		t.Fatal(err)
	}
	SetKey(ini, "address", regionMain)
	SetKey(ini, "region", "eu")
	SetKey(ini, "host_machines", "na1=na")
	code, out := run(t, RunServer, dir, "--non-interactive", "--check")
	if code == 0 || !strings.Contains(out, "--new-host-machine na1 na makes it") || strings.Contains(out, "Checking the setup") {
		t.Errorf("without the key: exit %d\n%s", code, out)
	}
	SetKey(ini, "tunnel", "off")
	if code, out = run(t, RunServer, dir, "--non-interactive", "--check"); code == 0 || !strings.Contains(out, "needs the tunnel") {
		t.Errorf("tunnel = off: exit %d\n%s", code, out)
	}
	cfg, _ := ReadConfig(ini)
	if u := cfg.Unknown(serverKeys...); len(u) > 0 {
		t.Errorf("unknown keys: %v", u)
	}
	SetKey(ini, "tunnel", "auto")
	SetKey(ini, "web_port", "0")
	if code, out = run(t, RunServer, dir, "--non-interactive", "--new-host-machine", "na1", "na"); code != 0 {
		t.Fatalf("--new-host-machine: exit %d\n%s", code, out)
	}
	code, out = run(t, RunServer, dir, "--non-interactive", "--seconds", "1")
	if code != 0 || !strings.Contains(out, "This server's region is eu; host machines that may register: na1 (na).") {
		t.Fatalf("the main server: exit %d\n%s", code, out)
	}
	if !logHas(t, setup.Layout{Root: filepath.Join(dir, DefaultFolder)}.Logs(), "host machines na1 (na) through the gateway") {
		t.Error("the server log doesn't name the host machine")
	}
}

// A host machine's whole path: the setup without the server's parts (no player copy, no content, no website), then
// dgserver in agent mode registering with a main server's gateway over TLS with its own key.
func TestStartServerHostMachine(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.60's and 127.0.9.61's ports")
	}
	orig := hostMachineControlPort
	hostMachineControlPort = 0 // the relay on any free port of 127.0.0.1, off a server on this PC
	t.Cleanup(func() { hostMachineControlPort = orig })
	key := gmshost.NewAgentKey()
	keyFile := filepath.Join(t.TempDir(), "na1.key")
	os.WriteFile(keyFile, []byte(key+"\n"), 0o600)
	// the main server: its gateway takes na1 with that key
	gw, mainLogs := serveTunnelLogged(t, regionMain, "--region", "eu", "--host-machine", "na1=na:"+keyFile,
		"--gms-control-bind", regionMain)
	pin, err := tunnel.ProbePin(regionMain+":"+itoa(gw), 5*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}

	dir := release(t)
	ini := filepath.Join(dir, ServerConfigName)
	os.WriteFile(ini, []byte(hostMachineConfigText(regionMain+":"+itoa(gw), pin.String(), "na1", key)), 0o600)
	SetKey(ini, "address", regionHost)
	SetKey(ini, "signup", "open") // a main server's option: noted, ignored
	code, out := run(t, RunServer, dir, "--non-interactive", "--seconds", "3")
	if code != 0 || !strings.Contains(out, "==== The host machine na1 registers with the main server "+regionMain) ||
		!strings.Contains(out, "Host machine stopped.") {
		t.Fatalf("the host machine: exit %d\n%s", code, out)
	}
	for _, want := range []string{"role     host machine na1", "[skipped]  client copy    a host machine",
		"[skipped]  content        a host machine keeps no accounts", "[done]     host copy", "[done]     host DLL",
		"a host machine doesn't use a main server's options (signup", "Let in TCP and UDP 42128"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	for _, not := range []string{"The website:", "https://", "sign-up", "Players add"} {
		if strings.Contains(out, not) {
			t.Errorf("%q in a host machine's output:\n%s", not, out)
		}
	}
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	if exists(l.Data()) || exists(filepath.Dir(l.ClientCopy())) {
		t.Error("a host machine made a data folder or a player copy")
	}
	if b, err := os.ReadFile(agentKeyPath(l.Root, "na1")); err != nil || strings.TrimSpace(string(b)) != key {
		t.Errorf("the machine's key file: %q %v", b, err)
	}
	if !logHas(t, l.Logs(), `registered with `+regionMain+":"+itoa(gw)+` over the TLS link as "na1"`) {
		t.Error("the host machine never registered")
	}
	// the match gateway runs on the host machine and is in its offer; the host machine prunes its own logs
	if !logHas(t, mainLogs, `host machine "na1" (region na)`) || !logHas(t, mainLogs, "match gateway "+regionHost+":42128 (pin ") {
		t.Error("the main server's log lacks na1's registration with its match gateway")
	}
	if !logHas(t, l.Logs(), "match gateway on "+regionHost+":42128") || !logHas(t, l.Logs(), "logs older than 30 days deleted") {
		t.Error("the host machine's log lacks its match gateway or its log pruning")
	}
	if !strings.Contains(out, "The match gateway's key fingerprint is ") {
		t.Errorf("no gateway fingerprint in the banner:\n%s", out)
	}
	// --check on the set-up folder: current; a key that isn't the main server's is refused there (and logged here). On
	// Linux an ini with the key in clear that others may read is set to 0600.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(ini, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if code, out = run(t, RunServer, dir, "--non-interactive", "--check"); code != 0 || !strings.Contains(out, "Everything is set up.") {
		t.Errorf("--check: exit %d\n%s", code, out)
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(ini); err != nil || st.Mode().Perm() != 0o600 || !strings.Contains(out, "its mode is now 0600") {
			t.Errorf("the ini with the key: stat %v %v\n%s", st, err, out)
		}
	}
	if code, out = run(t, RunServer, dir, "--non-interactive", "--create-account", "x"); code == 0 || !strings.Contains(out, "keeps no accounts") {
		t.Errorf("an account command: exit %d\n%s", code, out)
	}
}

// Under Wine: the Wine step first, the match-host copy only, the Linux texts.
func TestStartServerHostMachineLinux(t *testing.T) {
	calls := linuxServer(t, nil)
	dir := release(t)
	ini := filepath.Join(dir, ServerConfigName)
	os.WriteFile(ini, []byte(hostMachineConfigText("203.0.113.7:42128", testPin, "na1", gmshost.NewAgentKey())), 0o600)
	SetKey(ini, "address", regionHost)
	SetKey(ini, "wine", "/opt/wine-stable/bin/wine")
	code, out := run(t, RunServer, dir, "--non-interactive", "--setup-only")
	if code != 0 || len(*calls) != 1 || (*calls)[0].Command != "/opt/wine-stable/bin/wine" {
		t.Fatalf("exit %d, Wine %+v\n%s", code, *calls, out)
	}
	for _, want := range []string{"mode     the match hosts under Wine", "[done]     host copy", "a host machine keeps no accounts"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Start-Server.exe") {
		t.Errorf("Windows texts in:\n%s", out)
	}
	text := hostMachineConfigText("203.0.113.7:42128", testPin, "na1", "k")
	if !strings.Contains(text, "\nwine =") || strings.Contains(text, "Start-Server.exe") {
		t.Errorf("the host machine's ini on Linux:\n%s", text)
	}
	if st, err := os.Stat(filepath.Join(dir, DefaultFolder)); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o700) {
		t.Errorf("the setup folder: %v", err)
	}
}

// logHas says whether a log file in dir holds s.
func logHas(t *testing.T, dir, s string) bool {
	t.Helper()
	logs, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	for _, p := range logs {
		if b, _ := os.ReadFile(p); strings.Contains(string(b), s) {
			return true
		}
	}
	return false
}
