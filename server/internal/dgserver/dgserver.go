// Package dgserver is the Dawngate restoration server, dgserver (cmd/dgserver documents the services, flags and
// modes): moved here from cmd/dgserver so that Start-Server.exe (cmd/start-server) runs the same
// server in its own process, with every flag, default and log line unchanged. Main(args) is dgserver's command line;
// Run adds the hooks Start-Server uses.
package dgserver

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/blaze"
	"dawngate/server/internal/clientpkg"
	"dawngate/server/internal/content"
	"dawngate/server/internal/discordhook"
	"dawngate/server/internal/gms"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/limits"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/setup/exepatch"
	"dawngate/server/internal/setup/gamecontent"
	"dawngate/server/internal/site"
	"dawngate/server/internal/store"
	"dawngate/server/internal/telemetry"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/version"
	"dawngate/server/internal/web"
)

type config struct {
	// A5 (addr.go): bind is where the listeners bind (--bind; --host sets it too), publicHost what the
	// clients are told (--public-host; "" = bind). lookup resolves a public host name (nil = DNS).
	bind, publicHost string
	lookup           lookupIP
	// plain is --plain: on (the default) binds the services' own listeners on --bind; off serves them only through
	// the tunnel gateway's in-process listeners (needs --tunnel on). gmsBind is --gms-bind: where the match hosts' GMS
	// ports bind and the gateway's flows go ("" = --bind; when set, also the address HNET names).
	plain, gmsBind                                              string
	redirectorPort, blazePort, qosPort, httpPort, telemetryPort int
	gmsPort                                                     int  // UDP only
	gmsExternal                                                 bool // announce gmsPort without binding it (B9)
	// C1: a match host per game (gmshost) when gmsDir is set; otherwise gmsPort is announced (B8/B9).
	gmsDir, gmsArgs, gmsPorts string
	// C1b: the hosts' data folders (--gms-data: <dir>\<match>, DGGMS_DATA) and DGGMS_OPTIONS (--gms-options)
	gmsData, gmsOptions string
	// H4a: what runs the hosts' Dawngate.exe (--gms-runner: auto = the exe itself on Windows, wine elsewhere for a .exe)
	gmsRunner string
	// H1-live: idle hosts kept ready (--gms-warm; 0 = a host per game, started on demand)
	gmsWarm        int
	gmsControlPort int
	gmsTimeouts    [3]time.Duration // hello, gameReady, heartbeat (0 = gmshost's defaults)
	// H2: the control listener's address (--gms-control-bind; "" = 127.0.0.1), the agent key file
	// (--agent-key: a main server takes agents with it, an agent proves it), agent mode (--agent, the
	// main server's control address) and the agent's name (--agent-name; "" = the computer name).
	gmsControlBind, agentKey, agent, agentName string
	www, certs, logDir                         string
	// dataDir holds the account database accounts.db ("" = accounts in memory only); contentPath is the content file
	// ("" = <dataDir>\content.json; no content without a data folder).
	dataDir, contentPath string
	policy               blaze.Policy
	mm                   matchmaker.Config  // B10a: the PvP matchmaker's rules
	mmRefuse             bool               // B10b test aid: refuse every startMatchmaking
	testLoginErrors      []uint16           // test aid: the n-th login's error reply (--test-login-errors)
	mmBots               bool               // B10d: bots in a matchmade game's open seats (--mm-bots, on by default)
	leaderboardMinGames  int                // LBFIX: the fewest games for the Leaderboard (--leaderboard-min-games)
	customBots           bool               // B15: bots in a custom game's open seats (--custom-bots)
	shaperSelect         blaze.ShaperSelect // D3-live step 4: --shaper-select, --free-rotation, --shaper-timers
	// B14: the kill switches every login gets (--kill-switches), a broadcast message from the command line
	// (--broadcast), the operator's broadcast file ("" = none; main makes <data>\broadcast.txt the default) and the
	// broadcasts' default display flags (--broadcast-flags)
	killSwitches             []int64
	broadcast, broadcastFile string
	broadcastFlags           int64
	// serve <certs>\ca_modulus.bin on the HTTP port (--serve-ca) for Start-Client.exe on other machines
	serveCA bool
	// the sign-up policy (--signup, --signups-per-address) and the log retention in days (--log-days; 0 = keep)
	accounts accounts.Policy
	logDays  int
	// how often the running server re-reads the logged-in accounts' status (--status-check; 0 = never) and what
	// a client hears before its session is ended (--session-end)
	statusCheck time.Duration
	sessionEnd  blaze.SessionEndNotice
	// the tunnel (tunnel.go): --tunnel off|on|auto, the gateway's port and key folder ("" = --certs); the
	// test lanes' client side (--tunnel-client <gateway host:port>, --tunnel-address, --tunnel-pin or
	// --tunnel-pin-cert)
	tunnel, gatewayKey                                    string
	gatewayPort                                           int
	allowAnyClient                                        bool // --allow-any-client: the gateway lets in any release
	tunnelClient, tunnelAddress, tunnelPin, tunnelPinCert string
	// --tunnel-front <folder> (with --tunnel-client): Start-Client's picker front instead of one tunnel, its server
	// list and trusted keys in <folder>, the login screen's list written into --front-copy
	tunnelFront, frontCopy string
	// --limits (auto: on for a public bind or announced address) and the limits themselves (nil = Default())
	limitsMode limits.Mode
	limitsCfg  *limits.Config
	// the website (website.go): --web-port, --web-domain, --web-email, --web-dir, --web-acme-url, --web-proxy,
	// --web-bind
	web websiteConfig
	// the client package (clientpkg.go): --client-package, --client-package-name, --client-package-source
	clientPackage clientPackageConfig
	// quiet writes the log to its file only, not to the console (Hooks.Quiet: Start-Client's server for This PC)
	quiet bool
	// the Discord webhooks (secrets: never logged) and the posts' options (--discord-...)
	discord discordhook.Settings
}

// broadcastEvery is how often the operator's broadcast file is read (B14).
const broadcastEvery = 2 * time.Second

// contentFile returns the content file of this configuration ("" = none).
func (c config) contentFile() string {
	if c.contentPath != "" {
		return c.contentPath
	}
	if c.dataDir != "" {
		return filepath.Join(c.dataDir, "content.json")
	}
	return ""
}

// openData opens the account store and loads the content file. A missing content file is not an
// error (the server runs without content); a broken one is.
func openData(cfg config) (*store.Store, *content.Content, string, error) {
	st := store.New()
	if cfg.dataDir != "" {
		var err error
		// the SQLite store; its first Open imports an accounts.json of the earlier builds from the same folder
		if st, err = store.Open(filepath.Join(cfg.dataDir, store.FileName)); err != nil {
			return nil, nil, "", err
		}
	}
	file := cfg.contentFile()
	if file == "" {
		return st, nil, "no content", nil
	}
	c, err := content.Load(file)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil, fmt.Sprintf("no content file %s (new accounts get no items; build it with --make-content)", file), nil
	}
	if err != nil {
		st.Close()
		return nil, nil, "", err
	}
	info := fmt.Sprintf("content %s: %d items, %d shapers", file, len(c.Items), len(c.Shapers))
	if c.Old() { // B7a's format: no gem sockets, loot drops or loot tuning (B7c)
		info += fmt.Sprintf(" (format %s: no gem sockets or loot drops; rebuild it with --make-content)", c.Format)
	} else if !c.HasPerkStats() { // D3-perks: a file from before it has no perk stats (shaper select's C0 02)
		info += " (no perk stats: perk pages give no stats in a match; rebuild it with --make-content)"
	}
	return st, c, info, nil
}

// makeContent builds the content file (--make-content) from an AssetData dump folder, or from a
// game folder (one with game\Data\AssetData.snap, gamecontent.DataDir), which gives the same bytes without
// Python or a dump.
func makeContent(dir, file string) error {
	if file == "" {
		return errors.New("--make-content needs --content or --data")
	}
	build := content.Build
	if gamecontent.IsGameDir(dir) {
		build = gamecontent.Build
	}
	c, err := build(dir)
	if err != nil {
		return err
	}
	if err := c.Write(file); err != nil {
		return err
	}
	items, _, _ := c.Grant(content.OwnAll)
	fmt.Printf("wrote %s: %d items (%d ownable), %d shapers, %d perks, %d match items, %d loot bags\n",
		file, len(c.Items), len(items), len(c.Shapers), len(c.Perks), len(c.MatchItems), len(c.LootBags))
	return nil
}

func (c config) ports() []int {
	return []int{c.redirectorPort, c.blazePort, c.qosPort, c.httpPort, c.telemetryPort}
}

// defaultServicePorts are ports()' defaults: a service reached only through the tunnel (--plain off) takes them for a
// port given as 0, since its listener is the gateway's and binds nothing.
var defaultServicePorts = []int{redirector.Port, 10041, 17502, 18080, 9988}

// plainOn says whether the services bind their own listeners (--plain on, the default).
func (c config) plainOn() bool { return c.plain != PlainOff }

// hostsBind is the match hosts' GMS address (--gms-bind, else --bind): DGGMS_BIND's host, the GMS logger's, and
// where the tunnel gateway's flows go and leave from.
func (c config) hostsBind() string {
	if c.gmsBind != "" {
		return c.gmsBind
	}
	return c.bind
}

// hostsOn: this configuration runs the match-host manager (local hosts, agents or both).
func (c config) hostsOn() bool { return c.gmsDir != "" || c.agentKey != "" }

// printPorts are the TCP ports --print-ports lists: ports(), plus the control port with --gms-dir or
// --agent-key; none in agent mode (it listens on nothing).
func (c config) printPorts() []int {
	ps := c.ports()
	if !c.plainOn() && c.tunnelClient == "" { // the services listen only inside the gateway
		ps = nil
	}
	switch {
	case c.agent != "":
		return nil
	case c.tunnelClient != "": // the tunnel's client side listens on the server's ports (as the gateway names them)
		return ps
	case c.hostsOn():
		ps = append(ps, c.gmsControlPort)
	}
	if c.tunnel == TunnelOn { // and the gateway's (auto: only when the public host turns out public)
		ps = append(ps, c.gatewayPort)
	}
	if c.web.port > 0 { // the website
		ps = append(ps, c.web.port)
	}
	return ps
}

// prepareHosts runs once before the first host is started here (gmshost.ExecLauncher.Prepare): the
// launcher event the game needs. Tests replace it.
var prepareHosts = gmshost.LauncherEvent

// checkControlBind validates --gms-control-bind (H2): one IPv4 address, not the wildcard; anything but
// loopback exposes the control port to the network, which only agents need, so it takes --agent-key.
func checkControlBind(bind string, agents bool) error {
	ip := net.ParseIP(bind).To4()
	if ip == nil || ip.IsUnspecified() {
		return fmt.Errorf("--gms-control-bind must be one IPv4 address of this machine: %q", bind)
	}
	if !ip.IsLoopback() && !agents {
		return fmt.Errorf("--gms-control-bind %s puts the control port on the network, which only agents need: add --agent-key (docs/HOST_CONTROL.md, H2)", bind)
	}
	return nil
}

// startHosts starts the match-host manager (C1, --gms-dir; H2, --agent-key): the control listener on
// --gms-control-bind, the launcher for <gms-dir>'s Dawngate.exe (which needs the launcher event,
// gmshost.LauncherEvent) and the agent key.
func startHosts(cfg config, log *logging.Log) (*gmshost.Manager, string, error) {
	if cfg.gmsExternal {
		return nil, "", errors.New("--gms-dir/--agent-key and --gms-external exclude each other")
	}
	controlBind := cfg.gmsControlBind
	if controlBind == "" {
		controlBind = "127.0.0.1"
	}
	if err := checkControlBind(controlBind, cfg.agentKey != ""); err != nil {
		return nil, "", err
	}
	mc := gmshost.Config{Log: log, BindHost: cfg.hostsBind(), GMSHost: cfg.gmsBind,
		HelloTimeout: cfg.gmsTimeouts[0], ReadyTimeout: cfg.gmsTimeouts[1], HeartbeatTimeout: cfg.gmsTimeouts[2]}
	var notes []string
	runner := ""
	if cfg.gmsDir != "" {
		exe, err := gmshost.ResolveExe(cfg.gmsDir)
		if err != nil {
			return nil, "", err
		}
		first, last, err := gmshost.ParsePorts(cfg.gmsPorts)
		if err != nil {
			return nil, "", err
		}
		mc.Pool = gmshost.NewPool(first, last)
		mc.Launcher = &gmshost.ExecLauncher{Path: exe, Args: strings.Fields(cfg.gmsArgs), Prepare: prepareHosts,
			DataDir: cfg.gmsData, Options: cfg.gmsOptions, Runner: gmshost.RunnerFor(cfg.gmsRunner, exe)}
		notes = append(notes, fmt.Sprintf("match hosts %s %s, ports %d-%d", exe, cfg.gmsArgs, first, last))
		if r := gmshost.RunnerFor(cfg.gmsRunner, exe); len(r) > 0 {
			runner = ", hosts run by " + strings.Join(r, " ") // H4a: under Wine off Windows
		}
		if cfg.gmsData != "" {
			notes = append(notes, "host data "+filepath.Join(cfg.gmsData, "<match>"))
		}
		if cfg.gmsOptions != "" {
			notes = append(notes, "host options "+cfg.gmsOptions)
		}
		if cfg.gmsWarm > 0 {
			if cfg.gmsWarm >= last-first+1 {
				return nil, "", fmt.Errorf("--gms-warm %d leaves no port of --gms-ports %s for a game started on demand", cfg.gmsWarm, cfg.gmsPorts)
			}
			mc.Warm = cfg.gmsWarm
			notes = append(notes, fmt.Sprintf("warm pool %d", cfg.gmsWarm))
		}
	}
	if cfg.agentKey != "" {
		key, err := gmshost.ReadAgentKey(cfg.agentKey)
		if err != nil {
			return nil, "", err
		}
		mc.AgentKey = key
		notes = append(notes, "agents with the key in "+cfg.agentKey)
	}
	m := gmshost.NewManager(mc)
	if err := m.Listen(net.JoinHostPort(controlBind, strconv.Itoa(cfg.gmsControlPort))); err != nil {
		return nil, "", err
	}
	m.FillWarm() // H1-live: start the warm hosts (--gms-warm)
	return m, strings.Join(notes, ", ") + ", control " + m.Addr() + runner, nil
}

// agentInstance is dgserver in agent mode (H2): only the agent runs.
type agentInstance struct {
	log    *logging.Log
	agent  *gmshost.Agent
	cancel context.CancelFunc
	done   chan struct{}
}

// startAgent starts agent mode (--agent): it registers with the main server's control port and starts
// hosts from --gms-dir on this machine when asked.
func startAgent(cfg config) (*agentInstance, error) {
	if err := checkBind(cfg.bind); err != nil {
		return nil, err
	}
	if cfg.publicHost == "" {
		cfg.publicHost = cfg.bind
	}
	public, err := resolvePublic(cfg.publicHost, cfg.lookup)
	if err != nil {
		return nil, err
	}
	if cfg.agentKey == "" {
		return nil, errors.New("--agent needs --agent-key (the main server's key file)")
	}
	key, err := gmshost.ReadAgentKey(cfg.agentKey)
	if err != nil {
		return nil, err
	}
	if cfg.gmsDir == "" {
		return nil, errors.New("--agent needs --gms-dir (this machine's host copy)")
	}
	exe, err := gmshost.ResolveExe(cfg.gmsDir)
	if err != nil {
		return nil, err
	}
	name := cfg.agentName
	if name == "" {
		if name, err = os.Hostname(); err != nil {
			return nil, err
		}
	}
	ag := &gmshost.Agent{Server: cfg.agent, Key: key, Name: name, BindHost: cfg.bind, PublicHost: public, Ports: cfg.gmsPorts,
		Build: "dgserver", Launcher: &gmshost.ExecLauncher{Path: exe, Args: strings.Fields(cfg.gmsArgs), Prepare: prepareHosts,
			DataDir: cfg.gmsData, Options: cfg.gmsOptions, Runner: gmshost.RunnerFor(cfg.gmsRunner, exe)}}
	if err := ag.Check(); err != nil {
		return nil, err
	}
	log, err := logging.Open(cfg.logDir)
	if err != nil {
		return nil, err
	}
	ag.Log = log
	ctx, cancel := context.WithCancel(context.Background())
	in := &agentInstance{log: log, agent: ag, cancel: cancel, done: make(chan struct{})}
	log.Logf("stub", "agent %q for %s: hosts %s %s, ports %s bound on %s, GMS host %s; log %s", name, cfg.agent, exe,
		cfg.gmsArgs, cfg.gmsPorts, cfg.bind, public, log.Path)
	go func() {
		if err := ag.Run(ctx); err != nil {
			log.Logf("stub", "agent: %v", err)
		}
		close(in.done)
	}()
	return in, nil
}

// stop ends the agent (it kills its hosts) and closes the log.
func (in *agentInstance) stop() {
	in.cancel()
	<-in.done
	in.log.Log("stub", "stopped")
	in.log.Close()
}

// writeAgentKey writes a new agent key to path (--make-agent-key); an existing file is left alone.
func writeAgentKey(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(gmshost.NewAgentKey() + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// instance is a running server.
type instance struct {
	log       *logging.Log
	listeners []net.Listener
	qosUDP    *net.UDPConn     // QoS probes, on the QoS listener's port number
	gmsUDP    *net.UDPConn     // the GMS address announced in NotifyGameSetup (logged only)
	hosts     *gmshost.Manager // the match hosts (--gms-dir)
	ports     []int            // actual ports, in config order
	// B14: stops the broadcast file's watcher (nil without --broadcast-file)
	stopWatch func()
	// the served CA modulus's fingerprint (--serve-ca; "" without)
	caFingerprint string
	// the account store, closed after the listeners
	store *store.Store
	// stops the log pruning (--log-days; nil without)
	stopPrune func()
	// the tunnel's gateway (--tunnel on; nil without)
	gateway *tunnel.Gateway
	// the limits (nil = off) and the stop of their minute report. The tunnel gateway takes connections and
	// datagrams through the same limiter by the player's real address: Listener's Admit per accepted
	// connection, before the TLS handshake, AllowTunnelPacket per datagram, before the decryption.
	limits     *limits.Limiter
	stopLimits func()
	// stops the status check of the logged-in accounts (--status-check; nil without)
	stopStatus func()
	// the Discord webhooks (--discord-webhook...; no hook and no goroutine without a URL)
	discord *discordhook.Hooks
	// matches counts the running games with a match host (the "going down" post)
	matches func() int
	// the website (--web-port; nil without)
	website *website
	// the client package (--client-package; nil without)
	clientPackage *clientpkg.Package
	// the services' own listeners are bound (--plain on); false: their ports are only the gateway's (--plain off)
	plain bool
}

// start opens the log, binds every port and starts serving.
func start(cfg config) (*instance, error) {
	if err := checkBind(cfg.bind); err != nil {
		return nil, err
	}
	if cfg.gmsBind != "" {
		if err := checkBind(cfg.gmsBind); err != nil {
			return nil, fmt.Errorf("--gms-bind: %w", err)
		}
	}
	if cfg.publicHost == "" {
		cfg.publicHost = cfg.bind
	}
	public, err := resolvePublic(cfg.publicHost, cfg.lookup)
	if err != nil {
		return nil, err
	}
	if cfg.accounts.LocalTrust && !(net.ParseIP(cfg.bind).IsLoopback() && net.ParseIP(public).IsLoopback()) {
		return nil, fmt.Errorf("--local-trust (no passwords) is only for a server that only this PC reaches: --bind and --public-host must be loopback addresses, not %s and %s", cfg.bind, public)
	}
	cfg.accounts.ThisMachine = thisMachine
	tunnelOn, err := tunnelWanted(cfg.tunnel, public)
	if err != nil {
		return nil, err
	}
	if !cfg.plainOn() && !tunnelOn {
		return nil, errors.New("--plain off needs the tunnel (--tunnel on): without plain listeners the services are reachable only through its gateway")
	}
	publicName := "" // a name given as --public-host, for the log
	if net.ParseIP(cfg.publicHost) == nil {
		publicName = cfg.publicHost
	}
	if cfg.gmsExternal && cfg.gmsPort == 0 {
		return nil, fmt.Errorf("--gms-external needs a fixed --gms-port")
	}
	www := ""
	if cfg.www != "" {
		var err error
		if www, err = filepath.Abs(cfg.www); err != nil {
			return nil, err
		}
	}
	tlsCfg, err := redirector.TLSConfig(cfg.certs)
	if err != nil {
		return nil, err
	}
	var extra []web.Builtin
	caNote := ""
	if cfg.serveCA { // the CA's public modulus for Start-Client.exe on other machines (nothing secret)
		modulus, err := os.ReadFile(filepath.Join(cfg.certs, "ca_modulus.bin"))
		if err != nil {
			return nil, fmt.Errorf("--serve-ca: %w", err)
		}
		if len(modulus) != 128 {
			return nil, fmt.Errorf("--serve-ca: %s has %d bytes, not a CA modulus's 128", filepath.Join(cfg.certs, "ca_modulus.bin"), len(modulus))
		}
		extra = append(extra, web.Builtin{Path: web.CAModulusPath, ContentType: "application/octet-stream", Body: modulus})
		caNote = web.Fingerprint(modulus)
	}
	var pkgLog *logging.Log // the package's lines, once the log is open
	pkg, err := newClientPackage(cfg, tunnelOn, &pkgLog)
	if err != nil {
		return nil, err
	}
	extra = append(extra, web.SitePagesFor(cfg.accounts.Mode, pkg != nil)...) // the login screen's links (sitelinks)
	extra = append(extra, web.VersionPage(version.String()))                  // Start-Client's check of a server without the tunnel
	st, cont, contentInfo, err := openData(cfg)
	if err != nil {
		return nil, err
	}
	// the sign-up mode in force: --signup, or the admin page's choice if --signup is as at the last start; the
	// admin page changes it while the server runs (Policy.Live, shared by Blaze and the website)
	mode, modeNote, err := accounts.StartMode(st, cfg.accounts.Mode, time.Now())
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("the sign-up mode's setting: %w", err)
	}
	cfg.accounts.Live = accounts.NewModeSetting(mode)
	// the log last, so a setup error leaves no open log file behind (the listener errors below close it)
	log, err := logging.Open(cfg.logDir)
	if err != nil {
		st.Close()
		return nil, err
	}
	if cfg.quiet {
		log.SetConsole(nil)
	}
	pkgLog = log
	in := &instance{log: log, caFingerprint: caNote, store: st, plain: cfg.plainOn(), clientPackage: pkg}
	limitsNote := startLimits(&cfg, in, public)
	for i, p := range cfg.ports() {
		if !in.plain { // only the gateway's listener (startGateway): nothing to bind, the port is a number
			if p == 0 {
				p = defaultServicePorts[i]
			}
			in.ports = append(in.ports, p)
			continue
		}
		var ln net.Listener
		if i == qosListener {
			// QoS serves TCP and UDP on one port
			ln, in.qosUDP, err = listenTCPAndUDP(cfg.bind, p)
		} else {
			ln, err = net.Listen("tcp4", net.JoinHostPort(cfg.bind, strconv.Itoa(p)))
		}
		if err != nil {
			in.close()
			log.Close()
			return nil, err
		}
		name := listenerNames[i]
		ln = in.limits.Listener(ln, name, func(f string, a ...any) { log.Logf(name, f, a...) }) // the limits (nil: ln itself)
		in.listeners = append(in.listeners, ln)
		in.ports = append(in.ports, ln.Addr().(*net.TCPAddr).Port)
	}
	rp, bp, qp, hp, tp := in.ports[0], in.ports[1], in.ports[2], in.ports[3], in.ports[4]
	gp, gmsNote := cfg.gmsPort, "UDP, logged"
	if cfg.hostsOn() {
		if in.hosts, gmsNote, err = startHosts(cfg, log); err != nil {
			in.close()
			log.Close()
			return nil, err
		}
	} else if cfg.gmsExternal {
		gmsNote = "external, announced only"
	} else {
		if in.gmsUDP, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(cfg.hostsBind()), Port: gp}); err != nil {
			in.close()
			log.Close()
			return nil, err
		}
		gp = in.gmsUDP.LocalAddr().(*net.UDPAddr).Port
	}
	rd := &redirector.Server{Log: log, Host: public, BlazePort: bp}
	if in.plain {
		go rd.Serve(tls.NewListener(in.listeners[0], tlsCfg))
	}
	bz := blaze.NewServer(log, public, qp)
	bz.TelemetryPort = tp
	bz.GMSPort = gp
	bz.GMSHost = cfg.gmsBind // "" = the public host
	bz.Store, bz.Content, bz.Policy = st, cont, cfg.policy
	bz.Hosts = in.hosts // nil without --gms-dir: B8's announced GMS port
	bz.MM = matchmaker.New(cfg.mm)
	in.matches = func() int { return len(bz.HostedMatches()) }
	bz.MMRefuse = cfg.mmRefuse
	bz.TestLoginErrors = cfg.testLoginErrors
	bz.Accounts = cfg.accounts
	bz.MMBots = cfg.mmBots
	bz.LeaderboardMinGames = cfg.leaderboardMinGames
	bz.CustomBots = cfg.customBots
	bz.ShaperSelect = cfg.shaperSelect
	bz.KillSwitches = cfg.killSwitches
	bz.Limits = in.limits
	bz.SessionEnd = cfg.sessionEnd
	if cfg.statusCheck > 0 && st.Path() != "" { // another process can change the database only on disk
		in.stopStatus = bz.WatchAccountStatus(cfg.statusCheck)
	}
	broadcastNote := startBroadcasts(cfg, bz, in)
	qos := web.NewQoS(log, public, in.qosUDP)
	qos.Port = qp // the qosport the replies name without a probe socket (--plain off)
	qos.Limits = in.limits
	if in.plain {
		go bz.Serve(in.listeners[1])
		go qos.Serve(in.listeners[2])
		go qos.ServeUDP()
	}
	httpSrv, tel := &web.HTTP{Log: log, WWW: www, Extra: extra}, &telemetry.Sink{Log: log}
	if pkg != nil {
		httpSrv.Download, cfg.web.download = webDownload(pkg), siteDownload(pkg)
	}
	if cfg.web.port > 0 { // the website, before the HTTP port serves (its redirects)
		if in.website, err = startWebsite(cfg, log, st, in.limits, websiteStatus(bz, in.hosts), websiteAdmin(bz),
			bz.EndAccountSessions, public,
			publicName); err != nil {
			in.close()
			log.Close()
			return nil, err
		}
		httpSrv.Redirects = in.website.redirects()
		if pkg != nil { // the HTTP port's Download page goes to the website's (the file itself is served on both)
			httpSrv.Redirects["/"+web.DownloadPagePath] = in.website.url + site.PageDownload
		}
	}
	if in.plain {
		go httpSrv.Serve(in.listeners[3])
		go tel.Serve(in.listeners[4])
	}
	if in.gmsUDP != nil {
		go (&gms.Logger{Log: log, Conn: in.gmsUDP}).Serve()
	}
	tunnelNote := ""
	if tunnelOn {
		if in.gateway, tunnelNote, err = startGateway(cfg, log, in, services{rd: rd, bz: bz, qos: qos, web: httpSrv, tel: tel}, gp); err != nil {
			in.close()
			log.Close()
			return nil, err
		}
	}

	serving := ""
	if www != "" {
		serving = " serving " + www
	}
	if caNote != "" {
		serving += fmt.Sprintf(" and the CA modulus at /%s (fingerprint %s, --serve-ca)", web.CAModulusPath, caNote)
	}
	h := cfg.bind
	if in.plain {
		log.Logf("stub", "redirector on %s:%d (TLS), Blaze on %s:%d, QoS on %s:%d (HTTP, UDP), HTTP (CDN/crash) on %s:%d%s, "+
			"telemetry on %s:%d, GMS (%s) on %s:%d; log %s", h, rp, h, bp, h, qp, h, hp, serving, h, tp, gmsNote, cfg.hostsBind(), gp, log.Path)
	} else {
		log.Logf("stub", "no plain listeners (--plain off): redirector %d, Blaze %d, QoS %d (HTTP, UDP), HTTP (CDN/crash) %d%s and "+
			"telemetry %d only through the tunnel's gateway; GMS (%s) on %s:%d; log %s", rp, bp, qp, hp, serving, tp, gmsNote,
			cfg.hostsBind(), gp, log.Path)
	}
	for _, note := range addressNotes(cfg.bind, public, publicName) { // A5; none in the default setup
		log.Log("stub", note)
	}
	if in.gateway != nil {
		log.Log("stub", in.gateway.Describe()+tunnelNote)
	}
	log.Logf("stub", "version %s (a player's Start-Client needs the same one; the tunnel's info hello and /%s answer it)",
		version.String(), web.VersionPath)
	if in.website != nil {
		log.Log("stub", in.website.note)
	}
	if pkg != nil && in.gateway != nil {
		log.Log("stub", startClientPackage(pkg, cfg, in.gateway))
	}
	accounts := "accounts in memory only"
	if st.Path() != "" {
		accounts = fmt.Sprintf("accounts in %s (%s)", st.Path(), st.Info())
	}
	p := cfg.policy
	testNote := ""
	if p.SpentElsewhere {
		testNote = "; TEST: every purchase finds its balance spent elsewhere (--test-spent-elsewhere)"
	}
	log.Logf("stub", "%s; %s; new accounts: %s owned, EC %d, PC %d; FTUE done for all: %v; perk pages: %d%s", accounts, contentInfo,
		p.Owned, p.StartEC, p.StartPC, p.FTUEDone, p.PerkPages, testNote)
	mmNotes := ""
	if cfg.hostsOn() { // B10d: only a match host can fill the seats
		if cfg.mmBots {
			mmNotes = "; bots in the open seats"
		} else {
			mmNotes = "; open seats stay empty (--mm-bots=false)"
		}
	}
	if cfg.mmRefuse {
		mmNotes += "; every request refused (--mm-refuse, a test aid)"
	}
	if len(cfg.testLoginErrors) > 0 {
		log.Logf("stub", "TEST: the first %d logins answered with the errors %s (--test-login-errors, a test aid)",
			len(cfg.testLoginErrors), describeLoginErrors(cfg.testLoginErrors))
	}
	log.Logf("stub", "PvP matchmaking: %s%s", bz.MM.Config(), mmNotes)
	if cfg.hostsOn() || cfg.shaperSelect.On {
		log.Logf("stub", "shaper select (D3-live): %s", cfg.shaperSelect.Describe())
	}
	log.Logf("stub", "system messages (B14): kill switches %s; %s", blaze.DescribeKillSwitches(cfg.killSwitches), broadcastNote)
	log.Logf("stub", "logins (PH2): %s%s; %s; %s", describeSignup(cfg.accounts), modeNote, startPruning(cfg, in),
		describeSessionEnd(cfg, in.stopStatus != nil))
	log.Logf("stub", "limits (PH6): %s", limitsNote)
	if in.discord, err = discordhook.Open(cfg.discord, discordhook.Options{Logf: func(f string, a ...any) { log.Logf("stub", f, a...) },
		UserAgent: "OpenDawngate-Server/" + version.String()}); err != nil {
		in.close()
		log.Close()
		return nil, err
	}
	log.Log("stub", in.discord.Describe())
	bz.SetResultPosts(blaze.ResultPosts{Hook: in.discord.Hook(discordhook.Result), Settings: cfg.discord})
	bz.SetDiscordQueue(blaze.NewDiscordQueue(in.discord.Hook(discordhook.Queue), cfg.discord)) // DISCORD2b
	return in, nil
}

// DefaultStatusCheck is --status-check's default.
const DefaultStatusCheck = 5 * time.Second

// describeSessionEnd is the part of the logins' start-up line.
func describeSessionEnd(cfg config, checking bool) string {
	s := "a disabled, banned or deleted account's game session ends at once on the admin page and the website"
	if checking {
		s += fmt.Sprintf(", within %s after --account-status (status check)", cfg.statusCheck)
	} else {
		s += ", after --account-status only at its next login (no status check)"
	}
	if cfg.sessionEnd != blaze.SessionEndDisconnected {
		s += fmt.Sprintf(" (TEST: --session-end %s)", cfg.sessionEnd)
	}
	return s
}

// thisMachine reports whether a login's source address is this machine's own: a loopback address or one of its
// interfaces' (accounts.Policy.ThisMachine: only this machine gives an account This PC made without a password its
// first password).
func thisMachine(a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	a = a.Unmap()
	if a.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, ia := range addrs {
		if n, ok := ia.(*net.IPNet); ok {
			if own, ok := netip.AddrFromSlice(n.IP); ok && own.Unmap() == a {
				return true
			}
		}
	}
	return false
}

// describeSignup is the sign-up policy for the start-up line.
func describeSignup(p accounts.Policy) string {
	if p.LocalTrust {
		return "no passwords (--local-trust, a server only this PC reaches): any login name logs in with any password, none is checked or stored"
	}
	s := "passwords checked (argon2id hashes); sign-up " + p.CurrentMode().String()
	switch p.CurrentMode() {
	case accounts.Open:
		s += ": a new login name makes an account at its first login"
		if p.SignupsPerAddress > 0 {
			s += fmt.Sprintf(", at most %d a day per public address", p.SignupsPerAddress)
		}
		s += "; an account without a password takes its next login's"
	case accounts.Invite:
		s += ": new accounts only with an invite code (--make-invite), not from the game's login"
	case accounts.Closed:
		s += ": only the operator makes accounts (--create-account)"
	}
	return s
}

// startPruning applies --log-days now and every pruneEvery until the instance closes; it returns the log's note.
func startPruning(cfg config, in *instance) string {
	if cfg.logDays <= 0 {
		return "logs kept (no --log-days)"
	}
	note := fmt.Sprintf("logs older than %d days deleted (--log-days; now: %s)", cfg.logDays,
		describeRemoved(pruneLogs(cfg.logDir, cfg.gmsData, cfg.logDays, time.Now())))
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(pruneEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if removed := pruneLogs(cfg.logDir, cfg.gmsData, cfg.logDays, time.Now()); len(removed) > 0 {
					in.log.Logf("stub", "--log-days %d: %s", cfg.logDays, describeRemoved(removed))
				}
			}
		}
	}()
	in.stopPrune = func() { close(stop); <-done }
	return note
}

// listenerNames are cfg.ports()'s listeners in order (the log tags of their limits lines).
var listenerNames = []string{"redirector", "blaze", "qos", "http", "telemetry"}

// qosListener is QoS's index in config.ports and listenerNames.
const qosListener = 2

// listenTCPAndUDP binds TCP and UDP on one port. With port 0 (tests) the system picks the TCP port, and Windows
// may have reserved that number for UDP (excluded port ranges, e.g. on CI runners): then another port is tried.
func listenTCPAndUDP(bind string, port int) (net.Listener, *net.UDPConn, error) {
	for attempt := 0; ; attempt++ {
		ln, err := net.Listen("tcp4", net.JoinHostPort(bind, strconv.Itoa(port)))
		if err != nil {
			return nil, nil, err
		}
		p := ln.Addr().(*net.TCPAddr).Port
		udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(bind), Port: p})
		if err == nil {
			return ln, udp, nil
		}
		ln.Close()
		if port != 0 || attempt == 19 {
			return nil, nil, err
		}
	}
}

// limitsReportEvery is how often the limits' refusals are summed up in the log.
const limitsReportEvery = time.Minute

// startLimits makes the limiter when --limits says so (auto = a public bind or announced address), asks the match
// hosts for theirs ([host] net_limits=1, after the operator's --gms-options, which win) and starts the minute report.
// It returns the start-up line's note.
func startLimits(cfg *config, in *instance, public string) string {
	if !cfg.limitsMode.Active(exepatch.IsPublic, cfg.bind, public) {
		if cfg.limitsMode == limits.Off {
			return "off (--limits off)"
		}
		return "off (loopback and LAN addresses; --limits on forces them)"
	}
	lc := limits.Default()
	if cfg.limitsCfg != nil {
		lc = *cfg.limitsCfg
	}
	in.limits = limits.New(lc, nil)
	// no address of dgserver's own is exempt (an earlier build had exempted --bind as the tunnel gateway's source). A
	// tunnelled player's streams and datagrams reach the services in process with the player's real address
	// (tunnel.Addr.Peer, Blaze's Conn.Addr), and the gateway limits them by it before the TLS handshake and before a
	// datagram is decrypted (startGateway). The match hosts do see every tunnelled player at --bind (the gateway's flow
	// sockets), so they keep their per-IP rules off for it (net_limit_shared_ips).
	if cfg.hostsOn() {
		cfg.gmsOptions = withOption(cfg.gmsOptions, "net_limits", "1")
		cfg.gmsOptions = withOption(cfg.gmsOptions, "net_limit_shared_ips", cfg.hostsBind())
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(limitsReportEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if r := in.limits.TakeReport(); r != "" {
					in.log.Logf("stub", "limits: last minute %s", r)
				}
			}
		}
	}()
	var once sync.Once
	in.stopLimits = func() { once.Do(func() { close(stop); <-done }) }
	why := "--limits on"
	if cfg.limitsMode == limits.Auto {
		why = "a public address"
	}
	hostNote := ""
	if cfg.hostsOn() {
		hostNote = "; match hosts net_limits=1, net_limit_shared_ips=" + cfg.hostsBind() + " (the tunnel gateway's flows)"
	}
	return fmt.Sprintf("on (%s): %s; tunnelled players by their real address (the gateway's connections, datagrams "+
		"and sessions, Blaze's logins)%s", why, lc.Describe(), hostNote)
}

// withOption appends key=value to a "key=value;..." option string unless the key is there already (the host takes the
// first of a key, so the operator's own value would win anyway; this keeps the start-up line honest).
func withOption(options, key, value string) string {
	for _, kv := range strings.Split(options, ";") {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(strings.TrimSpace(k), key) {
			return options
		}
	}
	if strings.TrimSpace(options) == "" {
		return key + "=" + value
	}
	return strings.TrimRight(options, "; ") + ";" + key + "=" + value
}

// startBroadcasts sets up the broadcast messages (B14): the one from --broadcast and, with --broadcast-file, the
// operator's file, read now and every broadcastEvery by a watcher that in.close stops. It returns the log's note.
func startBroadcasts(cfg config, bz *blaze.Server, in *instance) string {
	if cfg.broadcastFlags == 0 {
		cfg.broadcastFlags = blaze.DefaultBroadcastFlags
	}
	static, notes := blaze.ParseBroadcasts(cfg.broadcast, cfg.broadcastFlags)
	for _, n := range notes {
		bz.Log.Logf("stub", "--broadcast: %s", n)
	}
	switch {
	case cfg.broadcastFile != "":
		in.stopWatch = bz.WatchBroadcasts(cfg.broadcastFile, static, cfg.broadcastFlags, broadcastEvery)
		return fmt.Sprintf("broadcasts: %d from --broadcast, then the lines of %s (read every %s; edit it to announce, delete it to stop)",
			len(static), cfg.broadcastFile, broadcastEvery)
	case len(static) > 0:
		bz.SetBroadcasts(static)
		return fmt.Sprintf("broadcasts: %d from --broadcast", len(static))
	}
	return "no broadcasts"
}

func (in *instance) close() {
	if in.stopWatch != nil {
		in.stopWatch()
	}
	if in.gateway != nil {
		in.gateway.Close()
	}
	if in.website != nil {
		in.website.close()
	}
	if in.stopPrune != nil {
		in.stopPrune()
	}
	if in.stopLimits != nil {
		in.stopLimits()
	}
	if in.stopStatus != nil {
		in.stopStatus()
	}
	in.discord.Close(0) // the "going down" post had its flush before (Run)
	if in.hosts != nil {
		in.hosts.Close() // kills running hosts
	}
	for _, ln := range in.listeners {
		ln.Close()
	}
	if in.qosUDP != nil {
		in.qosUDP.Close()
	}
	if in.gmsUDP != nil {
		in.gmsUDP.Close()
	}
	if in.store != nil {
		in.store.Close() // a request still running gets an error from the store; nothing half-written (one transaction each)
	}
}

// stop closes the listeners and the log.
func (in *instance) stop() {
	in.close()
	in.log.Log("stub", "stopped")
	in.log.Close()
}

// besideBin returns server\<name> for a binary in server\bin.
func besideBin(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	return filepath.Join(filepath.Dir(exe), "..", name)
}

// Main runs dgserver with its command-line arguments (without the program name) and returns the exit code.
func Main(args []string) int { return Run(args, Hooks{}) }

// Run is Main with hooks (Start-Server.exe's).
func Run(args []string, h Hooks) int {
	fs := flag.NewFlagSet("dgserver", flag.ContinueOnError)
	cfg := config{redirectorPort: redirector.Port}
	fs.StringVar(&cfg.bind, "bind", "127.0.0.1", "IPv4 address every listener binds (A5: a LAN address for clients on other machines)")
	fs.StringVar(&cfg.publicHost, "public-host", "127.0.0.1", "IPv4 address or name handed to clients: redirector reply, QoS ping site, telemetry, GMS address (A5)")
	host := fs.String("host", "", "shorthand for --bind and --public-host with the same address")
	fs.IntVar(&cfg.blazePort, "blaze-port", 10041, "Blaze port (plain TCP)")
	fs.IntVar(&cfg.qosPort, "qos-port", 17502, "QoS ping-site port announced in preAuth")
	fs.IntVar(&cfg.httpPort, "http-port", 18080, "HTTP port; must match patch_config.py --http-port")
	fs.IntVar(&cfg.telemetryPort, "telemetry-port", 9988, "telemetry port; must match patch_config.py --telemetry-port")
	fs.IntVar(&cfg.gmsPort, "gms-port", blaze.GMSPort, "UDP port of the GMS address announced in NotifyGameSetup (logged only)")
	fs.BoolVar(&cfg.gmsExternal, "gms-external", false, "announce --gms-port without binding it; another process serves the GMS")
	fs.StringVar(&cfg.gmsDir, "gms-dir", "", `start a match host per game from this host copy's Dawngate.exe (C1; e.g. client\run_854_server); default: announce --gms-port`)
	fs.StringVar(&cfg.gmsArgs, "gms-args", "-multipleInstances", "command line of the match hosts (-multipleInstances: no single-instance mutex, so a host runs beside a client)")
	fs.StringVar(&cfg.gmsPorts, "gms-ports", gmshost.DefaultPorts, "UDP port pool of the match hosts (one per running match)")
	fs.StringVar(&cfg.gmsData, "gms-data", "", `give each match host its own data folder <dir>\<match id> (DGGMS_DATA: its dgsim.log, AppData, Documents; its stdout in stdout.txt there); default: the DLL's <game dir>\host\<match id>`)
	fs.IntVar(&cfg.gmsWarm, "gms-warm", 0, "keep this many idle match hosts ready on this machine (H1-live warm pool; each holds a port of --gms-ports); 0 = start a host per game")
	fs.StringVar(&cfg.gmsRunner, "gms-runner", "auto", `what runs the match hosts' Dawngate.exe: "auto" (the exe itself on Windows, "wine" elsewhere for a .exe), "none", or a command line such as "/opt/wine-stable/bin/wine"`)
	fs.StringVar(&cfg.gmsOptions, "gms-options", "", `DGGMS_OPTIONS for the match hosts: "key=value;..." over the host copy's dgsim.ini [host] keys (development runs)`)
	fs.IntVar(&cfg.gmsControlPort, "gms-control-port", gmshost.DefaultControlPort, "TCP port of the control channel the match hosts (and agents) dial (JSON Lines)")
	fs.StringVar(&cfg.gmsControlBind, "gms-control-bind", "127.0.0.1", "IPv4 address the control port binds; a LAN address only for agents on other machines (needs --agent-key; H2)")
	fs.StringVar(&cfg.agentKey, "agent-key", "", "agent key file (H2): a main server takes agents that know it; in agent mode, the key to register with")
	fs.StringVar(&cfg.agent, "agent", "", "agent mode (H2): register with the main server's control port at this address (IP:port) and start match hosts from --gms-dir here")
	fs.StringVar(&cfg.agentName, "agent-name", "", "agent mode: this host machine's name in the main server's log (default: the computer name)")
	makeAgentKey := fs.String("make-agent-key", "", "write a new random agent key to this file (not overwritten if it exists), then exit")
	fs.DurationVar(&cfg.gmsTimeouts[0], "gms-hello-timeout", gmshost.DefaultHelloTimeout, "kill a match host without hello after this")
	fs.DurationVar(&cfg.gmsTimeouts[1], "gms-ready-timeout", gmshost.DefaultReadyTimeout, "kill a match host without gameReady this long after createGame")
	fs.DurationVar(&cfg.gmsTimeouts[2], "gms-heartbeat-timeout", gmshost.DefaultHeartbeatTimeout, "kill a ready match host silent for this long")
	fs.StringVar(&cfg.www, "www", "", "serve files from this directory on the HTTP port (default: built-in answers, else 404)")
	fs.StringVar(&cfg.certs, "certs", besideBin("certs"), "folder with server_cert.pem and server_key.pem")
	fs.StringVar(&cfg.logDir, "log", besideBin("logs"), "log folder")
	fs.StringVar(&cfg.dataDir, "data", besideBin("data"), `data folder: accounts.db (accounts, player content; an accounts.json of earlier builds is imported at the first start) and content.json; "" = accounts in memory only`)
	fs.StringVar(&cfg.contentPath, "content", "", `content file (default <data>\content.json)`)
	makeContentDir := fs.String("make-content", "", `build the content file from this AssetData dump folder (re/scripts/assetdata.py dump) or game folder (the one with game\Data\AssetData.snap), then exit`)
	writeWWW := fs.String("write-www", "", "write the built-in web answers under this folder at the paths the client requests (for --www), then exit")
	cfg.policy = blaze.DefaultPolicy()
	fs.StringVar(&cfg.policy.Owned, "owned", cfg.policy.Owned, `inventory of new accounts: "all" or "starter"`)
	startEC := fs.Uint("start-ec", uint(cfg.policy.StartEC), "earned currency (Destiny) of new accounts")
	startPC := fs.Uint("start-pc", uint(cfg.policy.StartPC), "paid currency (Waypoints) of new accounts")
	fs.BoolVar(&cfg.policy.FTUEDone, "ftue-done", cfg.policy.FTUEDone, "every account is past the first-time-user (Practice-only) gate (the default; kept for old command lines)")
	ftueRequired := fs.Bool("ftue-required", false, "the original's first-time-user gate: a new account sees the Practice-only screen until its FTUE flag is set (off by default)")
	perkPages := fs.Uint("perk-pages", uint(cfg.policy.PerkPages), "the most perk pages an account can have (the loadout page's STCK; B7c)")
	fs.BoolVar(&cfg.policy.SpentElsewhere, "test-spent-elsewhere", false, "test aid (B7c-live): every purchase finds its currency's balance spent elsewhere (set to 0) and is refused for insufficient funds")
	cfg.mm = blaze.DefaultMatchmaking()
	fs.IntVar(&cfg.mm.TeamSize, "mm-team-size", cfg.mm.TeamSize, "PvP matchmaking: players per team (1 = 1v1 ... 5 = 5v5)")
	fs.IntVar(&cfg.mm.MinPlayers, "mm-min-players", cfg.mm.MinPlayers, "PvP matchmaking: fewest players a match starts with once --mm-fill-after has passed (the empty slots get bots, --mm-bots)")
	fs.DurationVar(&cfg.mm.FillAfter, "mm-fill-after", cfg.mm.FillAfter, "PvP matchmaking: how long the oldest player waits for full teams before a smaller match starts")
	fs.DurationVar(&cfg.mm.FillExtend, "mm-fill-extend", cfg.mm.FillExtend, "PvP matchmaking: how much longer a smaller match waits for each player (session) who joins it (0 = no extension)")
	fs.DurationVar(&cfg.mm.MaxWait, "mm-max-wait", 0, "PvP matchmaking: the longest a player waits in the queue before it gives up (SESSION_TIMED_OUT; 0 = the client's 3 h)")
	testLoginErrors := fs.String("test-login-errors", "", "test aid: comma-separated 16-bit error codes (hex 0x.. or decimal); the n-th login gets the n-th as its error reply (0 = answered as usual), later logins are answered as usual; e.g. 0x0C,0x4072,0")
	fs.BoolVar(&cfg.mmRefuse, "mm-refuse", false, "PvP matchmaking test aid: refuse every startMatchmaking (GAMEMANAGER_ERR_INVALID_MATCHMAKING_CRITERIA)")
	fs.BoolVar(&cfg.mmBots, "mm-bots", true, "PvP matchmaking with match hosts: a game that starts with open seats (after --mm-fill-after) gets bots in them, up to --mm-team-size a team (B10d); --mm-bots=false leaves them empty")
	discordOpts := addDiscordFlags(fs)
	fs.IntVar(&cfg.leaderboardMinGames, "leaderboard-min-games", blaze.LeaderboardMinGames, "the main menu's Leaderboard lists the accounts with at least this many PvP and custom games (the screen says 10)")
	fs.BoolVar(&cfg.customBots, "custom-bots", false, "custom games with match hosts: bots fill the open seats when the game starts (B15; off: only the lobby's players)")
	fs.BoolVar(&cfg.shaperSelect.On, "shaper-select", false, "match hosts run shaper select with each player's shapers, skins, wards and announcers from its account (D3-live)")
	freeRotation := fs.String("free-rotation", "", `shaper select: shapers in the free rotation, comma-separated ("gunman,PC_Otter,Fenmore")`)
	shaperTimers := fs.String("shaper-timers", "", "shaper select: <pick ms>,<role/perk ms>,<skin ms>,<reveal ms> (default: the host's; practice 300000,300000,15500,16)")
	killSwitches := fs.String("kill-switches", blaze.DefaultKillSwitches, `client features switched off at every login (B14), comma-separated: Matchmaking, WalletTopUp (the add-funds button), EUGameServers, PracticeSoloGames, FTUESoloGames, PurchaseWithWaypoints, PracticeGroupGames, CustomGames, ForwardRenderer; "none" = all allowed`)
	fs.StringVar(&cfg.broadcast, "broadcast", "", `a broadcast message every login gets (B14; "@<flags> " in front sets its display flags)`)
	fs.StringVar(&cfg.broadcastFile, "broadcast-file", "", `the operator's broadcast messages, one per line, read every 2 s and sent to everyone logged in when they change (B14; default <data>\broadcast.txt, "" = none)`)
	fs.Int64Var(&cfg.broadcastFlags, "broadcast-flags", blaze.DefaultBroadcastFlags, "display flags of the broadcast messages: 4 the main menu's marquee and shaper select, 2 a System chat line (also in a match), 1 the main menu chat's notifications; added up")
	signup := fs.String("signup", "open", "sign-up mode: open (a new login name makes an account at its first login, with the password typed), invite (new accounts only with an invite code: --make-invite; the website), closed (only the operator: --create-account)")
	fs.IntVar(&cfg.accounts.SignupsPerAddress, "signups-per-address", accounts.DefaultSignupsPerAddress, "the most accounts one public source address may make per UTC day (loopback and LAN addresses are never counted); 0 = no cap")
	fs.BoolVar(&cfg.accounts.LocalTrust, "local-trust", false, "no passwords, for a server only this PC reaches (Start-Client's own server for This PC): any login name logs in with any password, and none is checked or stored; needs a loopback --bind")
	fs.DurationVar(&cfg.statusCheck, "status-check", DefaultStatusCheck, "re-read the status of the logged-in accounts this often and end the game session of each one that isn't active any more (the operator's --account-status in another process); the admin page and the website end it at once; 0 = never")
	sessionEnd := fs.String("session-end", blaze.SessionEndDisconnected.String(), "a test aid: what the client hears before its session is ended: disconnected (UserSessions::UserSessionDisconnected), unauthenticated (UserUnauthenticated), close (the connection closed without a word)")
	fs.IntVar(&cfg.logDays, "log-days", 0, "delete dgserver's log files and the match hosts' folders (--gms-data) older than this many days, at the start and every 6 hours (the logs hold login names and addresses); 0 = keep everything")
	var ac accountCommands
	fs.BoolVar(&ac.makeInvite, "make-invite", false, "make an invite code (printed once), then exit; with --invite-uses, --invite-days, --invite-note")
	fs.IntVar(&ac.inviteUses, "invite-uses", 1, "--make-invite: how many accounts the code may make")
	fs.Float64Var(&ac.inviteDays, "invite-days", 14, "--make-invite: days until the code expires (0 = never)")
	fs.StringVar(&ac.inviteNote, "invite-note", "", "--make-invite: a note kept with the code (whom it is for)")
	fs.BoolVar(&ac.listInvites, "list-invites", false, "list the invite codes (id, uses, expiry), then exit")
	fs.Int64Var(&ac.revokeInvite, "revoke-invite", 0, "revoke the invite code with this id, then exit")
	fs.StringVar(&ac.createAccount, "create-account", "", "make an account with this login name and a random password (printed once), then exit (any sign-up mode)")
	fs.StringVar(&ac.resetPassword, "reset-password", "", "give the account with this login name a new random password (printed once), then exit")
	fs.StringVar(&ac.accountStatus, "account-status", "", "set an account's status, then exit: <login name>=active|disabled|banned|deleted")
	fs.StringVar(&ac.makeAdmin, "make-admin", "", "give the account with this login name the admin role (the website's /admin page), then exit")
	fs.StringVar(&ac.removeAdmin, "remove-admin", "", "take the admin role from the account with this login name, then exit")
	fs.Var(&ac.importLogs, "import-ranked-logs", "after updating from v0.8 or older, with the server stopped: count the "+
		"Leaderboard's PvP and custom games of the older versions from their log files (a logs folder, or a log file named "+
		"itself; may be given more than once), then exit; prints what it finds (a dry run) unless --apply")
	fs.BoolVar(&ac.apply, "apply", false, "--import-ranked-logs: write the games into the database (once; again only with --force)")
	fs.BoolVar(&ac.force, "force", false, "--import-ranked-logs --apply: import again although an import ran before (the same games count twice)")
	seconds := fs.Int("seconds", 0, "stop after N seconds (default: run until Ctrl+C)")
	printPorts := fs.Bool("print-ports", false, "print the TCP ports this configuration listens on, then exit")
	limitsFlag := fs.String("limits", "auto", "rate and count limits on every listener, the logins, Blaze requests, games and queues, and the match hosts' packets (net_limits): auto = on for a public --bind or --public-host address, off on loopback and LAN; on forces them (the lanes' tests); off")
	fs.BoolVar(&cfg.serveCA, "serve-ca", false, "serve the redirector CA's public modulus (<certs>\\ca_modulus.bin) on the HTTP port at /"+web.CAModulusPath+", for Start-Client.exe on other machines (trust on first use, the player compares its fingerprint)")
	fs.StringVar(&cfg.plain, "plain", PlainOn, `the services' own listeners on --bind (redirector, Blaze, QoS TCP and UDP, HTTP, telemetry): "on", or "off" = reachable only through the tunnel's gateway (needs --tunnel on; Start-Server with the tunnel)`)
	fs.StringVar(&cfg.gmsBind, "gms-bind", "", "IPv4 address the match hosts' GMS ports bind (DGGMS_BIND) and HNET names, where the tunnel gateway sends their datagrams (default --bind; Start-Server with the tunnel: a loopback address of its own)")
	fs.StringVar(&cfg.tunnel, "tunnel", TunnelOff, `the encrypted tunnel's gateway: "on" serves it on --bind:--gateway-port (TCP and UDP) beside the plain ports, "auto" = on for a public --public-host, "off"`)
	fs.IntVar(&cfg.gatewayPort, "gateway-port", tunnel.DefaultPort, "the tunnel gateway's port, TCP and UDP (the only port a public server forwards, with the website's)")
	fs.StringVar(&cfg.gatewayKey, "gateway-key", "", `folder of the gateway's key (`+tunnel.CertFile+`, `+tunnel.KeyFile+`; made at the first start); default --certs`)
	fs.BoolVar(&cfg.allowAnyClient, "allow-any-client", false, "the tunnel gateway lets in a Start-Client of any release (for developers); without it only Start-Client of this server's release logs in, and an older or other one is refused")
	fs.StringVar(&cfg.tunnelClient, "tunnel-client", "", "test aid: run only the player's side of the tunnel to this gateway (host:port), listening on --tunnel-address; the game's TLS to the redirector ends here with --certs (CN = --tunnel-address)")
	fs.StringVar(&cfg.tunnelAddress, "tunnel-address", "127.0.0.1", "--tunnel-client: the loopback address the player copy is patched for")
	fs.StringVar(&cfg.tunnelPin, "tunnel-pin", "", "--tunnel-client: the gateway key's pin (64 hex digits)")
	fs.StringVar(&cfg.tunnelPinCert, "tunnel-pin-cert", "", "--tunnel-client: pin the key of this gateway certificate file (the server's "+tunnel.CertFile+")")
	fs.StringVar(&cfg.tunnelFront, "tunnel-front", "", "--tunnel-client: test aid: run Start-Client's server picker front instead of one tunnel, with the server list and trusted keys in this folder (servers\\bookmarks.json; the --tunnel-client gateway is added, its key trusted, and preselected)")
	fs.StringVar(&cfg.frontCopy, "front-copy", "", "--tunnel-front: the player copy (the folder with game\\Data) the login screen's server list is written into")
	fs.IntVar(&cfg.web.port, "web-port", 0, "the website's HTTPS port (registration, password change, account deletion, data download, status, privacy); 0 = no website. Start-Server.ini's web_port: 443")
	webDomain := fs.String("web-domain", "", "the website's domain name (e.g. play.example.org, its A record at this server): a Let's Encrypt certificate by TLS-ALPN-01, so the CA must reach <domain>:443 (forwarded to --web-port); empty = a self-signed certificate for the server's addresses")
	fs.StringVar(&cfg.web.email, "web-email", "", "--web-domain: a contact address for the CA's expiry mail (optional)")
	fs.StringVar(&cfg.web.dir, "web-dir", "", `the website's folder: its certificates and the operator's privacy.html, terms.html, about.html (default <data>\site)`)
	fs.StringVar(&cfg.web.acmeURL, "web-acme-url", "", "--web-domain: the ACME directory (default Let's Encrypt; "+site.LetsEncryptStagingURL+" for a first try)")
	fs.BoolVar(&cfg.web.proxy, "web-proxy", false, "serve the website behind a reverse proxy (nginx, for example) that holds --web-domain's HTTPS certificate: plain HTTP on --web-bind:--web-port only, no certificate of its own; the links and redirects point at https://<web-domain>/, and the players' addresses come from the proxy's X-Forwarded-For or X-Real-IP (trusted from a loopback peer only)")
	fs.StringVar(&cfg.web.bind, "web-bind", "", "--web-proxy: the loopback address the website listens on (default "+DefaultWebProxyBind+"; never a network address)")
	fs.BoolVar(&cfg.clientPackage.on, "client-package", false, "hand out the game client for this server at /dawngate/download/OpenDawngate-<name>.zip "+
		"(the HTTP port and the website, with a Download page): the Windows release of this server's version, fetched from "+
		"GitHub once into <data>\\client-package, with a Start-Client.ini that preselects this server (--public-host) and "+
		"trusts its tunnel key; needs --tunnel on and --data; a developer build has none")
	fs.StringVar(&cfg.clientPackage.name, "client-package-name", "", "--client-package: the <name> in the file's name (default --web-domain, else --public-host)")
	fs.StringVar(&cfg.clientPackage.source, "client-package-source", "", "--client-package: test aid: a fake of GitHub's API and downloads at "+
		"http://<loopback address>:<port> instead of GitHub")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Dawngate restoration server: redirector (TLS :%d), Blaze, QoS, HTTP and telemetry.\n"+
			"Usage: dgserver [flags]\n", redirector.Port)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil { // the flag package has printed the error and the usage
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	applyHostFlag(&cfg, *host, set)
	cfg.policy.StartEC, cfg.policy.StartPC = uint32(min(*startEC, math.MaxUint32)), uint32(min(*startPC, math.MaxUint32))
	if cfg.policy.Owned != content.OwnAll && cfg.policy.Owned != content.OwnStarter {
		fmt.Fprintf(os.Stderr, "dgserver: --owned must be %q or %q\n", content.OwnAll, content.OwnStarter)
		return 2
	}
	if *perkPages < 1 || *perkPages > 255 {
		fmt.Fprintln(os.Stderr, "dgserver: --perk-pages must be 1 to 255")
		return 2
	}
	cfg.policy.PerkPages = uint32(*perkPages)
	if *ftueRequired {
		cfg.policy.FTUEDone = false
	}
	for _, n := range strings.Split(*freeRotation, ",") {
		if n = strings.TrimSpace(n); n != "" {
			cfg.shaperSelect.FreeRotation = append(cfg.shaperSelect.FreeRotation, n)
		}
	}
	if *shaperTimers != "" {
		t, err := blaze.ParseShaperTimers(*shaperTimers)
		if err != nil {
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			return 2
		}
		cfg.shaperSelect.Timers = t
	}
	if err := applySystemFlags(&cfg, *killSwitches, set); err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		return 2
	}
	if m, err := accounts.ParseMode(*signup); err != nil {
		fmt.Fprintln(os.Stderr, "dgserver: --signup:", err)
		return 2
	} else {
		cfg.accounts.Mode = m
	}
	if cfg.accounts.SignupsPerAddress < 0 || cfg.logDays < 0 || cfg.statusCheck < 0 {
		fmt.Fprintln(os.Stderr, "dgserver: --signups-per-address, --log-days and --status-check must be 0 or more")
		return 2
	}
	if n, err := blaze.ParseSessionEndNotice(*sessionEnd); err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		return 2
	} else {
		cfg.sessionEnd = n
	}
	if cfg.plain != PlainOn && cfg.plain != PlainOff {
		fmt.Fprintf(os.Stderr, "dgserver: --plain must be %s or %s, not %q\n", PlainOn, PlainOff, cfg.plain)
		return 2
	}
	if m, err := limits.ParseMode(*limitsFlag); err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		return 2
	} else {
		cfg.limitsMode = m
	}
	if d, err := checkWebDomain(*webDomain); err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		return 2
	} else {
		cfg.web.domain = d
	}
	if cfg.web.port < 0 || cfg.web.port > 65535 || (cfg.web.domain != "" && cfg.web.port == 0) {
		fmt.Fprintln(os.Stderr, "dgserver: --web-port must be 1-65535 for a website (--web-domain needs one), 0 for none")
		return 2
	}
	if err := checkWebProxy(&cfg.web); err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		return 2
	}
	if d, err := discordOpts.settings(); err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		return 2
	} else {
		cfg.discord = d
	}
	if codes, err := parseLoginErrors(*testLoginErrors); err != nil {
		fmt.Fprintln(os.Stderr, "dgserver: --test-login-errors:", err)
		return 2
	} else {
		cfg.testLoginErrors = codes
	}

	if *makeAgentKey != "" {
		if err := writeAgentKey(*makeAgentKey); err != nil {
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			return 1
		}
		fmt.Println("wrote a new agent key to", *makeAgentKey, "(copy it to every host machine; keep it private)")
		return 0
	}
	if ac.any() {
		return ac.run(cfg.dataDir, os.Stdout, os.Stderr, time.Now())
	}
	if *makeContentDir != "" {
		if err := makeContent(*makeContentDir, cfg.contentFile()); err != nil {
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			return 1
		}
		return 0
	}
	if *writeWWW != "" {
		files, err := web.WriteBuiltins(*writeWWW)
		for _, f := range files {
			fmt.Println("wrote", f)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			return 1
		}
		return 0
	}
	if *printPorts {
		var ps []string
		for _, p := range cfg.printPorts() {
			ps = append(ps, strconv.Itoa(p))
		}
		fmt.Println(strings.Join(ps, " "))
		return 0
	}

	cfg.quiet = h.Quiet
	if cfg.tunnelClient != "" {
		return runTunnelClient(cfg, *seconds, h)
	}
	if cfg.agent != "" {
		ag, err := startAgent(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			return 1
		}
		waitForStop(*seconds, h.Stop)
		ag.stop()
		return 0
	}
	in, err := start(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		if h.StartFailed != nil {
			h.StartFailed(err)
		}
		return 1
	}
	status := startStatus(cfg.dataDir, in.discord.Hook(discordhook.Server), in.log, version.String(), time.Now)
	if h.Started != nil {
		h.Started(in.info(cfg))
	}
	status.online()
	waitForStop(*seconds, h.Stop)
	status.down(in.discord, in.matches(), discordFlush)
	in.stop()
	return 0
}

// Hooks let a caller of Run (Start-Server.exe) follow the server: all optional.
type Hooks struct {
	Started     func(Info)      // the server listens (after its startup lines)
	StartFailed func(error)     // the server didn't start (the error is also printed as dgserver prints it)
	Stop        <-chan struct{} // closing it stops the server as Ctrl+C does
	// Quiet writes the server's log only to its file (Start-Client runs the server for This PC beside the game: its
	// console keeps Start-Client's own lines)
	Quiet bool
}

// Info describes a started server.
type Info struct {
	Bind, PublicHost string
	TCPPorts         []int  // redirector, Blaze, QoS, HTTP, telemetry (and the control port with match hosts)
	Log              string // the log file
	CAFingerprint    string // the CA modulus's fingerprint when --serve-ca serves it, else ""
	// the tunnel gateway's port (TCP and UDP) and its key's fingerprint (0 and "" without --tunnel)
	GatewayPort        int
	GatewayFingerprint string
	GatewayPin         string // the whole key pin (64 hex digits), what Start-Client --gateway-pin takes
	// the website's address and its self-signed certificate's SHA-256 fingerprint ("" without a website; the
	// fingerprint "" with a domain's certificate)
	WebURL, WebFingerprint string
	// WebProxy is where the website listens behind a reverse proxy (<loopback address>:<port>; "" otherwise)
	WebProxy string
	// Version is the build's version (version.String): a player's Start-Client needs the same one
	Version string
	// ClientPackage is the client package's URL path (--client-package; "" without) and why there is none ("" when
	// there is one: a developer build has none)
	ClientPackage, ClientPackageMissing string
}

func (in *instance) info(cfg config) Info {
	var ports []int
	if in.plain {
		ports = append(ports, in.ports...)
	}
	if in.hosts != nil {
		ports = append(ports, cfg.gmsControlPort)
	}
	public := cfg.publicHost
	if public == "" {
		public = cfg.bind
	}
	info := Info{Bind: cfg.bind, PublicHost: public, TCPPorts: ports, Log: in.log.Path, CAFingerprint: in.caFingerprint,
		Version: version.String()}
	if in.gateway != nil {
		info.GatewayPort, info.GatewayFingerprint = in.gateway.Port(), in.gateway.Identity.Pin.Fingerprint()
		info.GatewayPin = in.gateway.Identity.Pin.String()
		info.TCPPorts = append(info.TCPPorts, info.GatewayPort)
	}
	if in.clientPackage != nil {
		info.ClientPackage, info.ClientPackageMissing = in.clientPackage.Path(), in.clientPackage.Unavailable()
	}
	if in.website != nil {
		info.WebURL, info.WebFingerprint, info.WebProxy = in.website.url, in.website.fingerprint, in.website.proxyAddr
		info.TCPPorts = append(info.TCPPorts, cfg.web.port)
	}
	return info
}

// applySystemFlags reads B14's flags into cfg: --kill-switches, --broadcast-flags (1-7) and the default of
// --broadcast-file (<data>\broadcast.txt unless the flag was given; none without a data folder).
func applySystemFlags(cfg *config, killSwitches string, set map[string]bool) error {
	ids, err := blaze.ParseKillSwitches(killSwitches)
	if err != nil {
		return fmt.Errorf("--kill-switches: %v", err)
	}
	cfg.killSwitches = ids
	if cfg.broadcastFlags < 1 || cfg.broadcastFlags > 7 {
		return fmt.Errorf("--broadcast-flags must be 1 to 7 (4 marquee, 2 chat line, 1 notification, added up)")
	}
	if !set["broadcast-file"] && cfg.dataDir != "" {
		cfg.broadcastFile = filepath.Join(cfg.dataDir, "broadcast.txt")
	}
	return nil
}

// parseLoginErrors reads --test-login-errors (a test aid): comma-separated 16-bit error codes, hex with 0x or
// decimal; "" = none.
func parseLoginErrors(s string) ([]uint16, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var codes []uint16
	for _, f := range strings.Split(s, ",") {
		v, err := strconv.ParseUint(strings.TrimSpace(f), 0, 16)
		if err != nil {
			return nil, fmt.Errorf("%q is not a 16-bit error code", strings.TrimSpace(f))
		}
		codes = append(codes, uint16(v))
	}
	return codes, nil
}

// describeLoginErrors writes --test-login-errors' codes for the start-up line.
func describeLoginErrors(codes []uint16) string {
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = fmt.Sprintf("%#04x", c)
	}
	return strings.Join(parts, ", ")
}

// waitForStop returns on Ctrl+C, when the console window is closed (Windows' CTRL_CLOSE_EVENT arrives as SIGTERM; the
// process then has a few seconds to stop its hosts, which also end with it through their job object, H2b), when stop
// is closed, or after seconds (0 = never).
func waitForStop(seconds int, stop <-chan struct{}) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	var timeout <-chan time.Time
	if seconds > 0 {
		timeout = time.After(time.Duration(seconds) * time.Second)
	}
	select {
	case <-interrupt:
	case <-timeout:
	case <-stop:
	}
}
