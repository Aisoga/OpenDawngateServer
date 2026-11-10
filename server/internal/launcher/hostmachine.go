package launcher

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/hostcopy"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/version"
)

// Regions for custom games: a main server (the one players log in to) and host machines in other regions, which run
// only match hosts for it. Start-Server.ini's role picks the program's part:
//   - the main server (role = server, the default) names its own region (region) and the machines that may register
//     (host_machines = na1=na, ...), each with its own key in Dawngate-Data/agents/<name>.key, which
//     Start-Server --new-host-machine NAME REGION writes together with that machine's Start-Server.ini;
//   - a host machine (role = host-machine) sets up only the match-host copy (and Wine's prefix on Linux) and runs
//     dgserver in agent mode: it registers with the main server's tunnel gateway over TLS (the main server's key
//     pinned, its own key proving its name), and its match gateway takes the players' match traffic. Nothing of
//     accounts, sign-up, the website, Discord, the client package or announcements.
//
// HostMachineFlags is the one place where a host machine's ini meets dgserver's flags.

// RoleKey is Start-Server.ini's key for the program's part: server (the default) or host-machine.
const RoleKey = "role"

// RoleHostMachine is role's value for a host machine.
const RoleHostMachine = "host-machine"

// NewHostMachineFlag is Start-Server's command that makes a host machine's key and ini on the main server.
const NewHostMachineFlag = "new-host-machine"

// The main server's keys for host machines.
const (
	RegionKey       = "region"
	HostMachinesKey = "host_machines"
)

func init() { serverKeys = append(serverKeys, RoleKey, RegionKey, HostMachinesKey) }

// hostMachineKeys are the keys a host machine's Start-Server.ini takes.
var hostMachineKeys = []string{RoleKey, "main_server", "main_pin", "machine_name", "machine_key", "address", "bind",
	"gateway_port", "gms_ports", "log_days", "wine", "folder", "zip", GameSourcesKey, "server_flags"}

// hostMachineControlPort is the host machine's relay port for its hosts' control connections (on 127.0.0.1); the
// tests take 0 (any free port) so that they keep off a server on this PC.
var hostMachineControlPort = gmshost.DefaultControlPort

var (
	machineNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	regionNameRE  = regexp.MustCompile(`^[a-z]{2,8}$`)
	hex64RE       = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

// isHostMachine reads role: false for a main server (server or empty), true for host-machine.
func isHostMachine(cfg Config) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(cfg[RoleKey])) {
	case "", "server":
		return false, nil
	case RoleHostMachine:
		return true, nil
	}
	return false, fmt.Errorf("%s = %s: server (the default) or %s", RoleKey, cfg[RoleKey], RoleHostMachine)
}

// agentKeyPath is a host machine's key file in a setup folder: <root>/agents/<name>.key (on the main server the key
// of each machine of host_machines, on a host machine its own when machine_key holds the digits).
func agentKeyPath(root, name string) string { return filepath.Join(root, "agents", name+".key") }

// hostMachineIniPath is the host machine's Start-Server.ini that --new-host-machine writes on the main server.
func hostMachineIniPath(root, name string) string {
	return filepath.Join(root, "agents", name+"-"+ServerConfigName)
}

// RegionMachine is one entry of the main server's host_machines.
type RegionMachine struct{ Name, Region string }

// ReadRegions reads the main server's region and host_machines (name=region, separated by commas).
func ReadRegions(cfg Config) (region string, machines []RegionMachine, err error) {
	region = strings.TrimSpace(cfg[RegionKey])
	if region != "" && !regionNameRE.MatchString(region) {
		return "", nil, fmt.Errorf("%s = %s: 2-8 lower-case letters, e.g. eu", RegionKey, region)
	}
	seen := map[string]bool{}
	for _, e := range strings.Split(cfg[HostMachinesKey], ",") {
		if e = strings.TrimSpace(e); e == "" {
			continue
		}
		name, r, ok := strings.Cut(e, "=")
		name, r = strings.TrimSpace(name), strings.TrimSpace(r)
		switch {
		case !ok:
			return "", nil, fmt.Errorf("%s: %q: want name=region, e.g. na1=na", HostMachinesKey, e)
		case !machineNameRE.MatchString(name):
			return "", nil, fmt.Errorf("%s: %q: a machine's name is 1-32 of a-z 0-9 - (starting with a letter or digit)", HostMachinesKey, name)
		case !regionNameRE.MatchString(r):
			return "", nil, fmt.Errorf("%s: %s=%s: a region is 2-8 lower-case letters", HostMachinesKey, name, r)
		case seen[name]:
			return "", nil, fmt.Errorf("%s: %s twice", HostMachinesKey, name)
		}
		seen[name] = true
		machines = append(machines, RegionMachine{name, r})
	}
	return region, machines, nil
}

// RegionFlags are the main server's dgserver flags for regions: --region and one --host-machine NAME=REGION:KEYFILE
// per machine of host_machines (its key in the setup folder). host_machines needs region and the tunnel; a machine
// without its key file is an error that names the command making it. note is the banner's line ("" without machines).
func RegionFlags(cfg Config, root string, tunnelOn bool) (flags []string, note string, err error) {
	region, machines, err := ReadRegions(cfg)
	if err != nil {
		return nil, "", err
	}
	if region != "" {
		flags = append(flags, "--region", region)
	}
	if len(machines) == 0 {
		return flags, "", nil
	}
	if region == "" {
		return nil, "", fmt.Errorf("%s needs %s: this server's own region (e.g. eu), the home of players who chose none", HostMachinesKey, RegionKey)
	}
	if !tunnelOn {
		return nil, "", fmt.Errorf("%s needs the tunnel (tunnel = auto or on): host machines register through its gateway", HostMachinesKey)
	}
	var names []string
	for _, m := range machines {
		key := agentKeyPath(root, m.Name)
		if _, err := gmshost.ReadAgentKey(key); err != nil {
			return nil, "", fmt.Errorf("%s: the host machine %s has no key in %s: Start-Server.exe --%s %s %s makes it",
				HostMachinesKey, m.Name, key, NewHostMachineFlag, m.Name, m.Region)
		}
		flags = append(flags, "--host-machine", m.Name+"="+m.Region+":"+key)
		names = append(names, m.Name+" ("+m.Region+")")
	}
	return flags, fmt.Sprintf("This server's region is %s; host machines that may register: %s.", region, strings.Join(names, ", ")), nil
}

// HostMachine is a host machine's Start-Server.ini, read and checked.
type HostMachine struct {
	Main        string // main_server: the main server's tunnel gateway, host:port (a name or an address)
	Pin         string // main_pin: its key's pin, 64 lower-case hex digits
	Name        string // machine_name
	Key         string // machine_key's 64 hex digits; "" when KeyFile names the key's file
	KeyFile     string // machine_key as a file, resolved
	Address     string // address: this machine's IPv4 address that players dial
	Bind        string // bind: the address the match gateway listens on ("" = Address)
	GatewayPort int    // gateway_port: the match gateway's TCP and UDP port
	GMSPorts    string // gms_ports: the match hosts' UDP ports
	LogDays     int    // log_days
}

// ReadHostMachine reads a host machine's keys (relative files from exeDir).
func ReadHostMachine(cfg Config, exeDir string) (HostMachine, error) {
	var h HostMachine
	main := strings.TrimSpace(cfg["main_server"])
	if main == "" {
		return h, errors.New("main_server is empty: the main server's address (or name) and its gateway_port, e.g. 203.0.113.7:42128")
	}
	if _, _, err := net.SplitHostPort(main); err != nil {
		main = net.JoinHostPort(main, strconv.Itoa(tunnel.DefaultPort))
	}
	host, port, err := net.SplitHostPort(main)
	if n, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
		return h, fmt.Errorf("main_server = %s: the main server's address (or name) and port, e.g. 203.0.113.7:42128", cfg["main_server"])
	}
	h.Main = main
	pin, err := tunnel.ParsePin(strings.TrimSpace(cfg["main_pin"]))
	if err != nil || pin.IsZero() {
		return h, fmt.Errorf("main_pin = %s: the main server's key pin, 64 hex digits (Start-Server.exe --%s on the main server prints it)",
			cfg["main_pin"], NewHostMachineFlag)
	}
	h.Pin = pin.String()
	h.Name = strings.TrimSpace(cfg["machine_name"])
	if !machineNameRE.MatchString(h.Name) {
		return h, fmt.Errorf("machine_name = %s: this machine's name in the main server's host_machines, 1-32 of a-z 0-9 -", h.Name)
	}
	switch key := strings.TrimSpace(cfg["machine_key"]); {
	case key == "":
		return h, errors.New("machine_key is empty: this machine's key, 64 hex digits (or a file that holds them), from the main server's " +
			"Start-Server.exe --" + NewHostMachineFlag)
	case hex64RE.MatchString(key):
		h.Key = strings.ToLower(key)
	default:
		h.KeyFile = Resolve(exeDir, key)
		if _, err := gmshost.ReadAgentKey(h.KeyFile); err != nil {
			return h, fmt.Errorf("machine_key = %s: neither 64 hex digits nor a key file (%v)", key, err)
		}
	}
	h.Address = strings.TrimSpace(cfg["address"])
	if err := hostMachineIPv4(h.Address); err != nil {
		return h, fmt.Errorf("address = %s: this machine's IPv4 address that players connect to (%v)", h.Address, err)
	}
	if h.Bind = strings.TrimSpace(cfg["bind"]); h.Bind != "" {
		if err := hostMachineIPv4(h.Bind); err != nil {
			return h, fmt.Errorf("bind = %s: an IPv4 address of this machine (%v)", h.Bind, err)
		}
	}
	if h.GatewayPort, err = gatewayPort(cfg["gateway_port"]); err != nil {
		return h, err
	}
	h.GMSPorts = firstNonEmpty(strings.TrimSpace(cfg["gms_ports"]), gmshost.DefaultPorts)
	if _, _, err := gmshost.ParsePorts(h.GMSPorts); err != nil {
		return h, fmt.Errorf("gms_ports = %s: a range such as %s (%v)", h.GMSPorts, gmshost.DefaultPorts, err)
	}
	if h.LogDays, err = strconv.Atoi(firstNonEmpty(strings.TrimSpace(cfg["log_days"]), "30")); err != nil || h.LogDays < 0 {
		return h, fmt.Errorf("log_days = %s: a number of days, 0 to keep the logs", cfg["log_days"])
	}
	return h, nil
}

func hostMachineIPv4(s string) error {
	ip := net.ParseIP(s)
	if s == "" || ip == nil || ip.To4() == nil {
		return errors.New("not an IPv4 address")
	}
	if ip.IsUnspecified() || ip.IsMulticast() {
		return errors.New("not a unicast address")
	}
	return nil
}

// bind is where the match gateway listens: bind, else the address itself.
func (h HostMachine) bind() string { return firstNonEmpty(h.Bind, h.Address) }

// HostMachineFlags are dgserver's arguments for a host machine (role = host-machine): agent mode over the TLS link to
// the main server's gateway (--agent-tls, --agent-pin, --agent-key, --agent-name), the hosts from the setup folder's
// match-host copy on hostsAddr (--gms-bind; under Wine 127.0.0.1) with the relay on 127.0.0.1:controlPort, and the
// match gateway: the tunnel gateway on --bind:--gateway-port, its key in --certs (made at its first start), announced
// as --public-host, with no plain listener. extra (server_flags and the Wine runner) comes last and wins.
func HostMachineFlags(h HostMachine, l setup.Layout, keyFile, hostsAddr string, controlPort int, extra []string) []string {
	f := []string{"--agent-tls", h.Main, "--agent-pin", h.Pin, "--agent-key", keyFile, "--agent-name", h.Name,
		"--gms-dir", l.HostCopy(), "--gms-args=-multipleInstances", "--gms-data", filepath.Join(l.Logs(), "hosts"),
		"--gms-ports", h.GMSPorts, "--gms-bind", hostsAddr, "--gms-control-port", strconv.Itoa(controlPort),
		"--log", l.Logs(), "--log-days", strconv.Itoa(h.LogDays), "--certs", l.Certs(h.Address),
		"--bind", h.bind(), "--public-host", h.Address,
		"--tunnel", "on", "--gateway-port", strconv.Itoa(h.GatewayPort), "--plain", "off"}
	return append(f, extra...)
}

// hostMachineConfigText is a host machine's Start-Server.ini, as --new-host-machine writes it on the main server.
func hostMachineConfigText(mainServer, pin, name, key string) string {
	t := `; Start-Server.ini of the host machine ` + name + `: it runs the matches of the main server named below, in its region.
; Start-Server.exe --` + NewHostMachineFlag + ` wrote it on the main server. Copy it next to Start-Server.exe on the host machine as
; Start-Server.ini, set address below, then start Start-Server.exe there. It holds the machine's key: keep it private.
; Lines that start with ; are comments.

; This program's part: host-machine runs match hosts for the main server below and nothing else (no accounts, no
; sign-up, no website, no game client for players). server (or no role line) is a main server.
role = host-machine

; The main server: its address or domain name and its gateway_port, and the pin of its key (the main server's
; Start-Server prints both). The host machine connects to it there (TCP), and refuses a server with another key.
main_server = ` + mainServer + `
main_pin = ` + pin + `

; This machine's name in the main server's host_machines and its own key (64 hex digits, or a file that holds them).
; A key registers only its own name; the main server revokes it when its line in host_machines goes.
machine_name = ` + name + `
machine_key = ` + key + `

; This machine's public IPv4 address: players' games connect to its match gateway there.
address =

; Only behind a router or a cloud's NAT: the address of this machine the match gateway listens on (its LAN address),
; while address is the public one. Empty: the gateway listens on address itself.
bind =

; The match gateway's port, TCP and UDP: let it in on the firewall (and SSH), nothing else.
gateway_port = 42128

; The match hosts' UDP ports, one per running match (on 127.0.0.1, reached only through the gateway). They must lie
; inside the main server's (by default both are 10042-10051).
gms_ports = 10042-10051

; Delete the matches' logs and recordings after this many days (they hold the players' addresses). 0: keep them.
log_days = 30

; The folder for the match-host copy, its certificates and the logs: relative to this program's folder, or a full
; path.
folder = Dawngate-Data

; Dawngate.zip from archive.org, only needed for the first setup and for repairs. Empty: next to this program, then
; in your Downloads folder.
zip =

; More options for dgserver, separated by blanks. Empty: the defaults.
server_flags =
`
	if hostsUnderWine {
		t = strings.Replace(linuxText.Replace(t), "\nserver_flags =", "\n"+wineConfig+"server_flags =", 1)
	}
	return t
}

// hostMachineRun is what RunServer hands its host-machine role: the flags it parsed.
type hostMachineRun struct {
	in                *bufio.Reader
	path              string
	check, setupOnly  bool
	seconds           int
	hostDir, zip, src string
	accountArgs       []string
}

// runHostMachine is Start-Server with role = host-machine: the match-host copy (with Wine's prefix on Linux) and
// dgserver in agent mode with its match gateway.
func runHostMachine(o IO, cfg Config, r hostMachineRun) int {
	path := r.path
	var mainKeys []string
	for _, k := range cfg.Unknown(hostMachineKeys...) {
		if slices.Contains(serverKeys, k) {
			mainKeys = append(mainKeys, k)
		} else {
			fmt.Fprintf(o.Out, "  note: %s: unknown option %q (ignored)\n", path, k)
		}
	}
	if len(mainKeys) > 0 {
		fmt.Fprintf(o.Out, "  note: %s: a host machine doesn't use a main server's options (%s; ignored)\n", path, strings.Join(mainKeys, ", "))
	}
	if len(r.accountArgs) > 0 {
		return o.fail("A host machine keeps no accounts: run the account commands on the main server.")
	}
	h, err := ReadHostMachine(cfg, o.ExeDir)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	if h.Key != "" { // the machine's key in clear in the ini: readable by its owner only (Linux)
		if changed, err := keepPrivate("", path); err != nil {
			fmt.Fprintf(o.Out, "  note: %s holds this machine's key, and its mode can't be set to 0600: %v\n", path, err)
		} else if changed {
			fmt.Fprintf(o.Out, "  note: %s holds this machine's key: its mode is now 0600 (only its owner reads it)\n", path)
		}
	}
	folder := firstNonEmpty(cfg["folder"], DefaultFolder)
	root := Resolve(o.ExeDir, folder)
	l := setup.Layout{Root: root}
	zipConf := firstNonEmpty(r.zip, cfg["zip"])
	sources, err := newGameSources(r.src, cfg[GameSourcesKey], gameHostCopySpace)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	bindAddr := h.bind()
	if !r.check {
		if err := thisMachine(bindAddr); err != nil {
			return o.fail("%s: %s is not an address of this machine (%v): set address to this machine's public address, or, behind "+
				"a router or a cloud's NAT, bind to the address the machine holds (ipconfig shows it as IPv4 Address).", path, bindAddr, err)
		}
	}
	hostFiles := firstNonEmpty(r.hostDir, filepath.Join(o.ExeDir, "host"))
	if _, err := os.Stat(filepath.Join(hostFiles, "dgsim.dll")); err != nil {
		return o.fail("The match host's files are missing: %s has no dgsim.dll. Unpack the whole release zip, "+
			"with its host folder, next to Start-Server.exe.", hostFiles)
	}
	files := hostcopy.Files{FS: os.DirFS(hostFiles), DLL: "dgsim.dll", Lua: "lua"}
	hostsAddr := serverHostsAddress(bindAddr)
	if !r.check && !r.setupOnly { // before the setup: a second Start-Server must not touch the copies of a running one
		held := HeldPorts(bindAddr, []Port{{h.GatewayPort, false, "the match gateway"}, {h.GatewayPort, true, "the match gateway"}})
		first, last, _ := gmshost.ParsePorts(h.GMSPorts)
		var gms []Port
		for p := first; p <= last; p++ {
			gms = append(gms, Port{p, true, "a match host"})
		}
		held = append(held, HeldPorts(hostsAddr, gms)...)
		if hostMachineControlPort > 0 {
			held = append(held, HeldPorts(setup.Local, []Port{{hostMachineControlPort, false, "the match hosts' control channel"}})...)
		}
		if len(held) > 0 {
			return o.fail("Another program holds the host machine's ports:\n  %s\nIs it already running (another Start-Server, "+
				"or dgserver.exe)? Close it, then start Start-Server.exe again.", strings.Join(held, "\n  "))
		}
	}
	var runLock *setup.RunLockHandle
	if !r.check {
		lock, held, err := setup.TakeRunLock(root, setup.RunLock{Program: "Start-Server", Address: h.Address, Dial: bindAddr,
			GatewayPort: h.GatewayPort, State: setup.RunSetup})
		if errors.Is(err, setup.ErrServerRuns) {
			return o.fail("This folder's server already runs in %s (pid %d): close it first, or give this Start-Server a folder "+
				"of its own (folder = in %s).", held.Program, held.Pid, path)
		}
		if err != nil {
			return o.fail("The folder's server lock: %v", err)
		}
		runLock = lock
		defer lock.Release()
		if hostsUnderWine { // the machine's key and the logs: the operator's alone
			if err := os.Chmod(root, 0o700); err != nil {
				fmt.Fprintf(o.Out, "  note: %s can't be made private (%v)\n", root, err)
			}
		}
	}
	fmt.Fprintf(o.Out, "  folder   %s\n", root)
	fmt.Fprintf(o.Out, "  role     host machine %s of the main server %s: match hosts only (no accounts, no website)\n", h.Name, h.Main)
	fmt.Fprintf(o.Out, "  address  %s (players' games connect to its match gateway, port %d)\n", h.Address, h.GatewayPort)
	if hostsUnderWine {
		fmt.Fprintln(o.Out, "  mode     the match hosts under Wine (the match-host copy only)")
	}
	fmt.Fprintln(o.Out)
	if r.check {
		fmt.Fprintln(o.Out, "Checking the setup (nothing is changed):")
	} else {
		size := "1.5 GB: the match-host copy only"
		if hostsUnderWine {
			size = "3 GB: the match-host copy and the hosts' Wine prefix"
		}
		fmt.Fprintf(o.Out, "Checking the setup (the first start sets everything up: about a minute and %s):\n", size)
	}
	s := &setup.Setup{Layout: l, Out: o.Out, Live: o.Live, Check: r.check, FindZip: zipFinder(o, r.in, zipConf, sources), HostFiles: files,
		Manifest: manifest, Maps: MapsDir(o.ExeDir)}
	var release func()
	if !r.check {
		if release, err = setup.Lock(root); err != nil {
			return o.fail("%v", err)
		}
	}
	var wine setup.WineEnv
	if hostsUnderWine { // first Wine (and its prefix): without it no host can start, so nothing else is set up
		wine, err = prepareWine(s, setup.WineOptions{Command: strings.TrimSpace(cfg["wine"]), Prefix: os.Getenv("WINEPREFIX")})
		if err != nil && !r.check {
			if release != nil {
				release()
			}
			advice := "Start again; if the same step fails again, delete the Wine prefix " + wine.Prefix + " so that it is made anew."
			for _, b := range s.Bad() {
				if b.Step == "wine" || b.Step == "wine 32-bit" {
					advice = setup.WineInstallHint
				}
			}
			return o.fail("Wine isn't ready for the match hosts:\n%s\n\n%s", lines(s.Bad()), advice)
		}
	}
	keyFile := h.KeyFile
	if keyFile == "" && !r.check {
		keyFile = agentKeyPath(root, h.Name)
		if err := writeMachineKey(keyFile, h.Key); err != nil {
			if release != nil {
				release()
			}
			return o.fail("This machine's key can't be written to %s: %v", keyFile, err)
		}
	}
	err = s.Server(setup.ServerOptions{Address: h.Address, NoClient: true, NoContent: true,
		NoClientNote: "a host machine: the match-host copy only", NoContentNote: "a host machine keeps no accounts"})
	if release != nil {
		release()
	}
	if r.check {
		if err != nil {
			fmt.Fprintf(o.Out, "\n%d item(s) not current; start Start-Server.exe without --check to set them up.\n", len(s.Bad()))
			return 1
		}
		fmt.Fprintln(o.Out, "\nEverything is set up.")
		return 0
	}
	if err != nil {
		return o.fail("The setup is not complete:\n%s\n\n%s", lines(s.Bad()), setupAdvice(s.Bad(), root))
	}
	fmt.Fprintln(o.Out, "  the setup is complete")
	if r.setupOnly {
		return 0
	}
	var extra []string
	if hostsUnderWine { // the hosts under the Wine the setup checked, in its prefix
		extra = append(extra, "--gms-runner", wine.Wine)
		os.Setenv("WINEPREFIX", wine.Prefix)
	}
	extra = append(append(extra, MapsFlags(o.ExeDir)...), SplitFlags(cfg["server_flags"])...)
	if r.seconds > 0 {
		extra = append(extra, fmt.Sprintf("--seconds=%d", r.seconds))
	}
	flags := HostMachineFlags(h, l, keyFile, hostsAddr, hostMachineControlPort, extra)
	fmt.Fprintln(o.Out, "\nStarting the host machine ...")
	if code := dgserver.Run(flags, dgserver.Hooks{Started: func(info dgserver.Info) {
		if err := runLock.Set(setup.RunRunning, info.GatewayPin); err != nil {
			fmt.Fprintf(o.Out, "  note: the folder's server lock: %v\n", err)
		}
		hostMachineBanner(o.Out, h, l, wine, info)
	}}); code != 0 {
		return o.fail("The host machine stopped with an error (see above).")
	}
	fmt.Fprintln(o.Out, "Host machine stopped.")
	return 0
}

// hostMachineBanner says what the host machine does once it starts.
func hostMachineBanner(w io.Writer, h HostMachine, l setup.Layout, wine setup.WineEnv, info dgserver.Info) {
	port := h.GatewayPort
	if info.GatewayPort > 0 {
		port = info.GatewayPort
	}
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "==== The host machine %s registers with the main server %s. ====\n", h.Name, h.Main)
	fmt.Fprintf(w, "  Version %s: the main server must run the same release (it refuses another): update both together.\n",
		firstNonEmpty(info.Version, version.String()))
	fmt.Fprintf(w, "  The main server gives it its region and its matches; players' games reach them through its match gateway on\n"+
		"  %s port %d (TCP and UDP). Let in TCP and UDP %d (and SSH) on this machine's firewall, nothing else.\n",
		h.Address, port, port)
	if info.GatewayFingerprint != "" {
		fmt.Fprintf(w, "  The match gateway's key fingerprint is %s (the main server hands it to the players' Start-Client).\n",
			info.GatewayFingerprint)
	}
	if wine.Wine != "" {
		fmt.Fprintf(w, "  The match hosts run under %s (%s) on 127.0.0.1, in the Wine prefix %s.\n", wine.Version, wine.Wine, wine.Prefix)
	}
	fmt.Fprintf(w, "  The matches' logs (and recordings, if the main server records): %s; deleted after %d days (0: kept).\n",
		filepath.Join(l.Logs(), "hosts"), h.LogDays)
	fmt.Fprintf(w, "  Logs: %s\n", l.Logs())
	fmt.Fprintln(w, "  Stop the server with Ctrl+C or by closing this window (it ends running matches).")
	fmt.Fprintln(w, "")
}

// writeMachineKey writes a host machine's key from its ini into the setup folder (0600 in a 0700 folder), unless the
// file already holds it.
func writeMachineKey(path, key string) error {
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) == key {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return err
	}
	_, err := keepPrivate(filepath.Dir(path), path)
	return err
}

// keepPrivate sets a folder that holds keys to 0700 and a file with a key to 0600 on Linux (and any other Unix):
// os.MkdirAll and os.WriteFile give those modes only to what they create, so a folder or a file that was there
// already keeps whatever its mode was. dir or file "" is left alone. Windows keeps its folders' access lists.
// It says whether a mode changed.
func keepPrivate(dir, file string) (bool, error) {
	if runtime.GOOS == "windows" {
		return false, nil
	}
	changed := false
	for _, x := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0o700}, {file, 0o600}} {
		if x.path == "" {
			continue
		}
		st, err := os.Stat(x.path)
		if err != nil {
			return changed, err
		}
		if st.Mode().Perm() != x.mode {
			if err := os.Chmod(x.path, x.mode); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	return changed, nil
}

// runNewHostMachine is Start-Server --new-host-machine NAME REGION on the main server: the machine's key in
// <folder>/agents/<name>.key (a new one; an existing key is kept), its entry in host_machines, and the host machine's
// Start-Server.ini in <folder>/agents/<name>-Start-Server.ini with the main server's address and pin, the machine's
// name and key; the lines are printed too.
func runNewHostMachine(o IO, path string, cfg Config, name string, args []string) int {
	if len(args) != 1 {
		return o.fail("Start-Server.exe --%s NAME REGION: the host machine's name (e.g. na1) and its region (e.g. na).", NewHostMachineFlag)
	}
	region := args[0]
	if !machineNameRE.MatchString(name) {
		return o.fail("--%s %s: a machine's name is 1-32 of a-z 0-9 - (starting with a letter or digit), e.g. na1.", NewHostMachineFlag, name)
	}
	if !regionNameRE.MatchString(region) {
		return o.fail("--%s %s %s: a region is 2-8 lower-case letters, e.g. na.", NewHostMachineFlag, name, region)
	}
	if hm, err := isHostMachine(cfg); err != nil {
		return o.fail("%s: %v", path, err)
	} else if hm {
		return o.fail("%s: role = %s: run --%s on the main server, not on a host machine.", path, RoleHostMachine, NewHostMachineFlag)
	}
	own, machines, err := ReadRegions(cfg)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	root := Resolve(o.ExeDir, firstNonEmpty(cfg["folder"], DefaultFolder))
	l := setup.Layout{Root: root}
	keyPath := agentKeyPath(root, name)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return o.fail("%v", err)
	}
	if _, err := keepPrivate(filepath.Dir(keyPath), ""); err != nil {
		return o.fail("%v", err)
	}
	key, made := gmshost.NewAgentKey(), true
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	switch {
	case errors.Is(err, os.ErrExist):
		b, rerr := gmshost.ReadAgentKey(keyPath)
		if rerr != nil {
			return o.fail("%v", rerr)
		}
		key, made = string(b), false
		if _, err := keepPrivate("", keyPath); err != nil {
			return o.fail("%v", err)
		}
	case err != nil:
		return o.fail("The key can't be written: %v", err)
	default:
		_, werr := f.WriteString(key + "\n")
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return o.fail("The key can't be written: %v", werr)
		}
	}
	entries, changed := []string{}, true
	for _, m := range machines {
		if m.Name == name {
			changed = m.Region != region
			continue
		}
		entries = append(entries, m.Name+"="+m.Region)
	}
	entries = append(entries, name+"="+region)
	if changed {
		if err := SetKey(path, HostMachinesKey, strings.Join(entries, ", ")); err != nil {
			return o.fail("%s can't be written: %v", path, err)
		}
	}
	address := firstNonEmpty(cfg["address"], setup.Local)
	gw, err := gatewayPort(cfg["gateway_port"])
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	mainServer := net.JoinHostPort(address, strconv.Itoa(gw))
	pin := "<the pin this server's banner prints: --gateway-pin ...>"
	gotPin := false
	if p, err := tunnel.PinFromCertFile(filepath.Join(l.Certs(address), tunnel.CertFile)); err == nil {
		pin, gotPin = p.String(), true
	}
	iniPath := hostMachineIniPath(root, name)
	if err := os.WriteFile(iniPath, []byte(strings.ReplaceAll(hostMachineConfigText(mainServer, pin, name, key), "\n", iniNewline)), 0o600); err != nil {
		return o.fail("The host machine's Start-Server.ini can't be written: %v", err)
	}
	if _, err := keepPrivate("", iniPath); err != nil {
		return o.fail("The host machine's Start-Server.ini: %v", err)
	}
	state := "a new key"
	if !made {
		state = "its key, kept (delete the file and run this again for a new one: the old key then no longer registers)"
	}
	fmt.Fprintf(o.Out, "Host machine %s in the region %s: %s in %s\n", name, region, state, keyPath)
	fmt.Fprintf(o.Out, "  %s: %s = %s\n", path, HostMachinesKey, strings.Join(entries, ", "))
	if own == "" {
		fmt.Fprintf(o.Out, "  note: set %s in %s to this server's own region (e.g. eu): host machines need it.\n", RegionKey, path)
	}
	if mode, err := tunnelMode(cfg["tunnel"]); err == nil && mode == TunnelOff {
		fmt.Fprintf(o.Out, "  note: tunnel = off in %s: host machines register through the tunnel; turn it on (auto).\n", path)
	}
	if ip := net.ParseIP(address); ip != nil && ip.IsLoopback() {
		fmt.Fprintf(o.Out, "  note: this server's address is %s, which a machine elsewhere can't reach: set main_server in the host\n"+
			"  machine's file to this server's public address or domain name (and %d).\n", address, gw)
	}
	if !gotPin {
		fmt.Fprintf(o.Out, "  note: this server has no tunnel key yet: start it once (its first start makes the key), then run this\n"+
			"  command again; it keeps the machine's key and writes the file with the pin.\n")
	}
	fmt.Fprintf(o.Out, "\nThe host machine's Start-Server.ini is %s (it holds the machine's key: keep it private).\n", iniPath)
	fmt.Fprintln(o.Out, "Copy it to the host machine next to Start-Server.exe as Start-Server.ini and set address there to that")
	fmt.Fprintln(o.Out, "machine's public IPv4 address. Or put these lines into the host machine's Start-Server.ini:")
	fmt.Fprintf(o.Out, "  role = %s\n  main_server = %s\n  main_pin = %s\n  machine_name = %s\n  machine_key = <the 64 hex digits in %s>\n",
		RoleHostMachine, mainServer, pin, name, keyPath)
	fmt.Fprintln(o.Out, "(The key isn't printed, so that it stays out of terminal logs and screenshots.)")
	fmt.Fprintln(o.Out, "Restart this server to take the host machine (it reads host_machines at its start).")
	return 0
}
