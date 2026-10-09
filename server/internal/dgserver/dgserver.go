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
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/content"
	"dawngate/server/internal/gms"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/setup/gamecontent"
	"dawngate/server/internal/store"
	"dawngate/server/internal/telemetry"
	"dawngate/server/internal/web"
)

type config struct {
	// A5 (addr.go): bind is where the listeners bind (--bind; --host sets it too), publicHost what the
	// clients are told (--public-host; "" = bind). lookup resolves a public host name (nil = DNS).
	bind, publicHost                                            string
	lookup                                                      lookupIP
	redirectorPort, blazePort, qosPort, httpPort, telemetryPort int
	gmsPort                                                     int  // UDP only
	gmsExternal                                                 bool // announce gmsPort without binding it (B9)
	// C1: a match host per game (gmshost) when gmsDir is set; otherwise gmsPort is announced (B8/B9).
	gmsDir, gmsArgs, gmsPorts string
	// C1b: the hosts' data folders (--gms-data: <dir>\<match>, DGGMS_DATA) and DGGMS_OPTIONS (--gms-options)
	gmsData, gmsOptions string
	// H1-live: idle hosts kept ready (--gms-warm; 0 = a host per game, started on demand)
	gmsWarm        int
	gmsControlPort int
	gmsTimeouts    [3]time.Duration // hello, gameReady, heartbeat (0 = gmshost's defaults)
	// H2: the control listener's address (--gms-control-bind; "" = 127.0.0.1), the agent key file
	// (--agent-key: a main server takes agents with it, an agent proves it), agent mode (--agent, the
	// main server's control address) and the agent's name (--agent-name; "" = the computer name).
	gmsControlBind, agentKey, agent, agentName string
	www, certs, logDir                         string
	// dataDir holds accounts.json ("" = accounts in memory only); contentPath is the content file
	// ("" = <dataDir>\content.json; no content without a data folder).
	dataDir, contentPath string
	policy               blaze.Policy
	mm                   matchmaker.Config  // B10a: the PvP matchmaker's rules
	mmRefuse             bool               // B10b test aid: refuse every startMatchmaking
	mmBots               bool               // B10d: bots in a matchmade game's open seats (--mm-bots, on by default)
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
		if st, err = store.Open(filepath.Join(cfg.dataDir, "accounts.json")); err != nil {
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

// hostsOn: this configuration runs the match-host manager (local hosts, agents or both).
func (c config) hostsOn() bool { return c.gmsDir != "" || c.agentKey != "" }

// printPorts are the TCP ports --print-ports lists: ports(), plus the control port with --gms-dir or
// --agent-key; none in agent mode (it listens on nothing).
func (c config) printPorts() []int {
	switch {
	case c.agent != "":
		return nil
	case c.hostsOn():
		return append(c.ports(), c.gmsControlPort)
	}
	return c.ports()
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
	mc := gmshost.Config{Log: log, BindHost: cfg.bind,
		HelloTimeout: cfg.gmsTimeouts[0], ReadyTimeout: cfg.gmsTimeouts[1], HeartbeatTimeout: cfg.gmsTimeouts[2]}
	var notes []string
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
			DataDir: cfg.gmsData, Options: cfg.gmsOptions}
		notes = append(notes, fmt.Sprintf("match hosts %s %s, ports %d-%d", exe, cfg.gmsArgs, first, last))
		if cfg.gmsData != "" {
			notes = append(notes, `host data `+cfg.gmsData+`\<match>`)
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
	return m, strings.Join(notes, ", ") + ", control " + m.Addr(), nil
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
			DataDir: cfg.gmsData, Options: cfg.gmsOptions}}
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
}

// start opens the log, binds every port and starts serving.
func start(cfg config) (*instance, error) {
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
	st, cont, contentInfo, err := openData(cfg)
	if err != nil {
		return nil, err
	}
	// the log last, so a setup error leaves no open log file behind (the listener errors below close it)
	log, err := logging.Open(cfg.logDir)
	if err != nil {
		return nil, err
	}
	in := &instance{log: log, caFingerprint: caNote}
	for _, p := range cfg.ports() {
		ln, err := net.Listen("tcp4", net.JoinHostPort(cfg.bind, strconv.Itoa(p)))
		if err != nil {
			in.close()
			log.Close()
			return nil, err
		}
		in.listeners = append(in.listeners, ln)
		in.ports = append(in.ports, ln.Addr().(*net.TCPAddr).Port)
	}
	rp, bp, qp, hp, tp := in.ports[0], in.ports[1], in.ports[2], in.ports[3], in.ports[4]
	if in.qosUDP, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(cfg.bind), Port: qp}); err != nil {
		in.close()
		log.Close()
		return nil, err
	}
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
		if in.gmsUDP, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(cfg.bind), Port: gp}); err != nil {
			in.close()
			log.Close()
			return nil, err
		}
		gp = in.gmsUDP.LocalAddr().(*net.UDPAddr).Port
	}
	rd := &redirector.Server{Log: log, Host: public, BlazePort: bp}
	go rd.Serve(tls.NewListener(in.listeners[0], tlsCfg))
	bz := blaze.NewServer(log, public, qp)
	bz.TelemetryPort = tp
	bz.GMSPort = gp
	bz.Store, bz.Content, bz.Policy = st, cont, cfg.policy
	bz.Hosts = in.hosts // nil without --gms-dir: B8's announced GMS port
	bz.MM = matchmaker.New(cfg.mm)
	bz.MMRefuse = cfg.mmRefuse
	bz.MMBots = cfg.mmBots
	bz.CustomBots = cfg.customBots
	bz.ShaperSelect = cfg.shaperSelect
	bz.KillSwitches = cfg.killSwitches
	broadcastNote := startBroadcasts(cfg, bz, in)
	go bz.Serve(in.listeners[1])
	qos := web.NewQoS(log, public, in.qosUDP)
	go qos.Serve(in.listeners[2])
	go qos.ServeUDP()
	go (&web.HTTP{Log: log, WWW: www, Extra: extra}).Serve(in.listeners[3])
	go (&telemetry.Sink{Log: log}).Serve(in.listeners[4])
	if in.gmsUDP != nil {
		go (&gms.Logger{Log: log, Conn: in.gmsUDP}).Serve()
	}

	serving := ""
	if www != "" {
		serving = " serving " + www
	}
	if caNote != "" {
		serving += fmt.Sprintf(" and the CA modulus at /%s (fingerprint %s, --serve-ca)", web.CAModulusPath, caNote)
	}
	h := cfg.bind
	log.Logf("stub", "redirector on %s:%d (TLS), Blaze on %s:%d, QoS on %s:%d (HTTP, UDP), HTTP (CDN/crash) on %s:%d%s, "+
		"telemetry on %s:%d, GMS (%s) on %s:%d; log %s", h, rp, h, bp, h, qp, h, hp, serving, h, tp, gmsNote, h, gp, log.Path)
	for _, note := range addressNotes(cfg.bind, public, publicName) { // A5; none in the default setup
		log.Log("stub", note)
	}
	accounts := "accounts in memory only"
	if st.Path() != "" {
		accounts = "accounts in " + st.Path()
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
	log.Logf("stub", "PvP matchmaking: %s%s", bz.MM.Config(), mmNotes)
	if cfg.hostsOn() || cfg.shaperSelect.On {
		log.Logf("stub", "shaper select (D3-live): %s", cfg.shaperSelect.Describe())
	}
	log.Logf("stub", "system messages (B14): kill switches %s; %s", blaze.DescribeKillSwitches(cfg.killSwitches), broadcastNote)
	return in, nil
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
	fs.StringVar(&cfg.dataDir, "data", besideBin("data"), `data folder: accounts.json (accounts, player content) and content.json; "" = accounts in memory only`)
	fs.StringVar(&cfg.contentPath, "content", "", `content file (default <data>\content.json)`)
	makeContentDir := fs.String("make-content", "", `build the content file from this AssetData dump folder (re/scripts/assetdata.py dump) or game folder (the one with game\Data\AssetData.snap), then exit`)
	writeWWW := fs.String("write-www", "", "write the built-in web answers under this folder at the paths the client requests (for --www), then exit")
	cfg.policy = blaze.DefaultPolicy()
	fs.StringVar(&cfg.policy.Owned, "owned", cfg.policy.Owned, `inventory of new accounts: "all" or "starter"`)
	startEC := fs.Uint("start-ec", uint(cfg.policy.StartEC), "earned currency (Destiny) of new accounts")
	startPC := fs.Uint("start-pc", uint(cfg.policy.StartPC), "paid currency (Waypoints) of new accounts")
	fs.BoolVar(&cfg.policy.FTUEDone, "ftue-done", false, "every account is past the first-time-user (Practice-only) gate")
	perkPages := fs.Uint("perk-pages", uint(cfg.policy.PerkPages), "the most perk pages an account can have (the loadout page's STCK; B7c)")
	fs.BoolVar(&cfg.policy.SpentElsewhere, "test-spent-elsewhere", false, "test aid (B7c-live): every purchase finds its currency's balance spent elsewhere (set to 0) and is refused for insufficient funds")
	cfg.mm = blaze.DefaultMatchmaking()
	fs.IntVar(&cfg.mm.TeamSize, "mm-team-size", cfg.mm.TeamSize, "PvP matchmaking: players per team (1 = 1v1 ... 5 = 5v5)")
	fs.IntVar(&cfg.mm.MinPlayers, "mm-min-players", cfg.mm.MinPlayers, "PvP matchmaking: fewest players a match starts with once --mm-fill-after has passed (the empty slots get bots, --mm-bots)")
	fs.DurationVar(&cfg.mm.FillAfter, "mm-fill-after", cfg.mm.FillAfter, "PvP matchmaking: how long the oldest player waits for full teams before a smaller match starts")
	fs.DurationVar(&cfg.mm.MaxWait, "mm-max-wait", 0, "PvP matchmaking: the longest a player waits in the queue before it gives up (SESSION_TIMED_OUT; 0 = the client's 3 h)")
	fs.BoolVar(&cfg.mmRefuse, "mm-refuse", false, "PvP matchmaking test aid: refuse every startMatchmaking (GAMEMANAGER_ERR_INVALID_MATCHMAKING_CRITERIA)")
	fs.BoolVar(&cfg.mmBots, "mm-bots", true, "PvP matchmaking with match hosts: a game that starts with open seats (after --mm-fill-after) gets bots in them, up to --mm-team-size a team (B10d); --mm-bots=false leaves them empty")
	fs.BoolVar(&cfg.customBots, "custom-bots", false, "custom games with match hosts: bots fill the open seats when the game starts (B15; off: only the lobby's players)")
	fs.BoolVar(&cfg.shaperSelect.On, "shaper-select", false, "match hosts run shaper select with each player's shapers, skins, wards and announcers from its account (D3-live)")
	freeRotation := fs.String("free-rotation", "", `shaper select: shapers in the free rotation, comma-separated ("gunman,PC_Otter,Fenmore")`)
	shaperTimers := fs.String("shaper-timers", "", "shaper select: <pick ms>,<role/perk ms>,<skin ms>,<reveal ms> (default: the host's; practice 300000,300000,15500,16)")
	killSwitches := fs.String("kill-switches", blaze.DefaultKillSwitches, `client features switched off at every login (B14), comma-separated: Matchmaking, WalletTopUp (the add-funds button), EUGameServers, PracticeSoloGames, FTUESoloGames, PurchaseWithWaypoints, PracticeGroupGames, CustomGames, ForwardRenderer; "none" = all allowed`)
	fs.StringVar(&cfg.broadcast, "broadcast", "", `a broadcast message every login gets (B14; "@<flags> " in front sets its display flags)`)
	fs.StringVar(&cfg.broadcastFile, "broadcast-file", "", `the operator's broadcast messages, one per line, read every 2 s and sent to everyone logged in when they change (B14; default <data>\broadcast.txt, "" = none)`)
	fs.Int64Var(&cfg.broadcastFlags, "broadcast-flags", blaze.DefaultBroadcastFlags, "display flags of the broadcast messages: 4 the main menu's marquee and shaper select, 2 a System chat line (also in a match), 1 the main menu chat's notifications; added up")
	seconds := fs.Int("seconds", 0, "stop after N seconds (default: run until Ctrl+C)")
	printPorts := fs.Bool("print-ports", false, "print the TCP ports this configuration listens on, then exit")
	fs.BoolVar(&cfg.serveCA, "serve-ca", false, "serve the redirector CA's public modulus (<certs>\\ca_modulus.bin) on the HTTP port at /"+web.CAModulusPath+", for Start-Client.exe on other machines (trust on first use, the player compares its fingerprint)")
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

	if *makeAgentKey != "" {
		if err := writeAgentKey(*makeAgentKey); err != nil {
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			return 1
		}
		fmt.Println("wrote a new agent key to", *makeAgentKey, "(copy it to every host machine; keep it private)")
		return 0
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
	if h.Started != nil {
		h.Started(in.info(cfg))
	}
	waitForStop(*seconds, h.Stop)
	in.stop()
	return 0
}

// Hooks let a caller of Run (Start-Server.exe) follow the server: all optional.
type Hooks struct {
	Started     func(Info)      // the server listens (after its startup lines)
	StartFailed func(error)     // the server didn't start (the error is also printed as dgserver prints it)
	Stop        <-chan struct{} // closing it stops the server as Ctrl+C does
}

// Info describes a started server.
type Info struct {
	Bind, PublicHost string
	TCPPorts         []int  // redirector, Blaze, QoS, HTTP, telemetry (and the control port with match hosts)
	Log              string // the log file
	CAFingerprint    string // the CA modulus's fingerprint when --serve-ca serves it, else ""
}

func (in *instance) info(cfg config) Info {
	ports := append([]int(nil), in.ports...)
	if in.hosts != nil {
		ports = append(ports, cfg.gmsControlPort)
	}
	public := cfg.publicHost
	if public == "" {
		public = cfg.bind
	}
	return Info{Bind: cfg.bind, PublicHost: public, TCPPorts: ports, Log: in.log.Path, CAFingerprint: in.caFingerprint}
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
