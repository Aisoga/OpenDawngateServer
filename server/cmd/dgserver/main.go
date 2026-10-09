// Command dgserver is the Dawngate restoration server (ADR-1): the services the patched client talks
// to, ported from the Python stub re/stubs/blaze_stub.py in roadmap item S1 with the same flags, ports
// and log format.
//
//	redirector  TLS on <host>:42127 (TLS 1.0/1.1, RSA key exchange with AES-SHA; certificate from
//	            server/tools/make_certs.py). Answers Redirector::getServerInstance (component 5,
//	            command 1) with our own Blaze address.
//	blaze       Plain TCP on <host>:<blaze-port>. Answers the requests the blaze package has handlers
//	            for (Util::preAuth, Util::ping, the login of roadmap item B5, ...) and logs every frame;
//	            unhandled requests get no answer, so the next unknown step stays visible.
//	qos         The ping site announced in the preAuth reply (roadmap item B2): HTTP on
//	            <host>:<qos-port> answers DirtySDK QosApi's /qos/qos, /qos/firewall and /qos/firetype,
//	            and UDP on the same port number answers its latency, bandwidth and firewall probes.
//	http        HTTP on <host>:<http-port>, which replaces EA's CDN (telemetry_rules.xml, web content),
//	            the crash-report and the debug-telemetry URLs. Logs each request (line, headers, body).
//	            Serves files from --www if the path exists there, else a built-in answer (our own
//	            telemetry_rules.xml and, since B6, dynamic_strings.xml, dynamic_lore.snap and the
//	            main menu's home.html; docs/WEB_CONTENT.md), else 404. --write-www DIR writes the
//	            built-in answers under DIR at the paths the client requests and exits.
//	telemetry   Plain TCP on <host>:<telemetry-port> (NewTelemetryServerIP/Port). Decodes what arrives
//	            (DirtySDK TelemetryApi "&lgr" packets: the anonymous login, one log line per LABS_*
//	            event; docs/TELEMETRY.md) and never answers (the client expects no reply).
//	gms         UDP on <host>:<gms-port>, the match server address GameManager::NotifyGameSetup hands
//	            the client (roadmap item B8). Logs the client's RakNet datagrams and never answers.
//	            With --gms-external the port is only announced, not bound, so another process can
//	            serve it (B9: raknet_probe_p5 --listen, started by run_session.ps1 -GmsProbe).
//	hosts       With --gms-dir (roadmap C1, ADR-2): instead of announcing --gms-port, every
//	            resetDedicatedServer starts a match host (<gms-dir>'s Dawngate.exe with --gms-args and
//	            the DGGMS_* environment) on a port from --gms-ports; the hosts dial the control
//	            listener on <gms-control-bind>:<gms-control-port> (JSON Lines, internal/gmshost,
//	            docs/HOST_CONTROL.md), and NotifyGameSetup goes out once the host reports gameReady.
//	            --gms-dir may also name gms_script.exe (C1b: the scripted GMS speaks the channel in host
//	            mode; --gms-args "--script-a"). --gms-data gives each host a data folder of its own,
//	            --gms-options its DGGMS_OPTIONS (run_session.ps1 -GmsHost).
//	agents      With --agent-key (roadmap H2): host machines running dgserver --agent register on the
//	            control port with the shared key, and games may start their hosts there; HNET then
//	            names that machine. --gms-control-bind puts the control port on a LAN address for them.
//
// Agent mode (H2, on a host machine): dgserver --agent <main server's control address> runs only the
// agent (gmshost.Agent): it registers with the main server and starts hosts from --gms-dir there, on
// ports from --gms-ports bound to --bind; clients are told --public-host. No Blaze, no listener.
//
// Build and run (from server/):
//
//	go build -o bin\dgserver.exe .\cmd\dgserver
//	bin\dgserver.exe [--bind 127.0.0.1] [--public-host 127.0.0.1] [--host ADDR (both)]
//	    [--blaze-port 10041] [--qos-port 17502] [--http-port 18080]
//	    [--telemetry-port 9988] [--gms-port 10042] [--gms-external] [--www DIR] [--certs DIR] [--log DIR]
//	    [--seconds N] [--data DIR] [--content FILE] [--owned all|starter] [--start-ec N] [--start-pc N]
//	    [--ftue-done] [--perk-pages 10] [--print-ports]
//	    [--gms-dir DIR [--gms-args "-multipleInstances"] [--gms-ports 10042-10051] [--gms-data DIR]
//	    [--gms-options "key=value;..."] [--gms-warm N]]
//	    [--agent-key FILE] [--gms-control-bind 127.0.0.1] [--gms-control-port 10040]
//	    [--gms-hello-timeout 30s] [--gms-ready-timeout 2m] [--gms-heartbeat-timeout 30s]
//	    [--mm-team-size 5] [--mm-min-players 1] [--mm-fill-after 15s] [--mm-max-wait 0] [--mm-bots=false] [--mm-refuse]
//	    [--custom-bots]
//	    [--kill-switches WalletTopUp] [--broadcast TEXT] [--broadcast-file FILE] [--broadcast-flags 4]
//	bin\dgserver.exe --agent <server IP>:10040 --agent-key FILE --gms-dir DIR --host <this machine's IP>
//	    [--public-host ADDR] [--gms-args ...] [--gms-ports 10042-10051] [--agent-name NAME] [--log DIR] [--seconds N]
//	bin\dgserver.exe --make-agent-key FILE
//	bin\dgserver.exe --make-content <AssetData dump dir> [--content FILE]
//	bin\dgserver.exe --write-www <dir>
//
// PvP matchmaking (roadmap B10a, internal/matchmaker, docs/BLAZE.md *PvP matchmaking (B10a)*):
// GameManager::startMatchmaking queues the player (or the PvP party it leads) per region; a game forms
// when both teams of --mm-team-size are full, or once the oldest session has waited --mm-fill-after with
// at least --mm-min-players. The game is announced like a practice game: the --gms-port address, or a
// match host per game with --gms-dir, which since B10d fills the empty slots with bots up to
// --mm-team-size a team (--mm-bots=false leaves them empty). --mm-max-wait caps a session's wait (the
// client asks for 3 h; B10b); --mm-refuse (a test aid) refuses every request.
//
// Custom games (roadmap B15, docs/BLAZE.md *Custom games and spectators (B15)*): PLAY > CUSTOM's CREATE GAME makes a
// lobby game (its host waits in mode custom until the admin starts it), BROWSE GAMES lists them; --custom-bots fills a
// custom game's open seats with bots when it starts.
//
// Addresses (roadmap A5, addr.go): the listeners bind --bind, and clients are told --public-host (an
// IPv4 address, or a name resolved once at startup); both default to 127.0.0.1, which keeps everything on
// this machine. <host> above is --bind for the listeners and --public-host in what is announced.
//
// --certs, --log and --data default to certs\, logs\ and data\ next to bin\ (server\certs, server\logs,
// server\data). The log file is <log>\<YYYYMMDD-HHMMSS>.log, and every line also goes to stdout.
// --print-ports prints the TCP ports this configuration listens on and exits; re/scripts/run_session.ps1
// uses it (with --gms-dir the control port is the sixth). The UDP ports are the QoS port's number and
// the GMS port (with --gms-dir: the hosts bind the pool's ports themselves).
//
// Player content and persistence (roadmap B7a): accounts, personas and player content are kept in
// <data>\accounts.json (--data "" keeps them in memory only). The game catalog comes from the content
// file (--content, default <data>\content.json), which --make-content builds once from the user's own
// AssetData dump (re/scripts/assetdata.py dump, docs/ASSETDATA.md) and exits; without it the server
// runs as before B7a (no items, empty store). New accounts get the inventory of --owned, the
// currencies --start-ec/--start-pc and the lowest-catalogID avatar; --ftue-done gives every account the
// "first-time-user experience done" flag, so PLAY opens the normal play screen (docs/BLAZE.md, B7a).
// Since B7c accounts keep perk pages (at most --perk-pages), purchases and their commerce profile, and a
// finished hosted game stores PlayerStats, XP, level, wins and its rewards (docs/BLAZE.md *Progression
// (B7c)*).
//
// System messages (roadmap B14, docs/BLAZE.md *System messages and other users' wins (B14)*): every login gets the
// kill switches of --kill-switches (default WalletTopUp: the main menu's add-funds button, whose top-up page nothing
// serves) and the broadcast messages: --broadcast and the lines of --broadcast-file (default <data>\broadcast.txt,
// read every 2 s; a change goes to everyone logged in at once), shown in the main menu's marquee and shaper select
// (--broadcast-flags 4; 2 adds a System chat line, also in a match).
package main

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
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/content"
	"dawngate/server/internal/gms"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/redirector"
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

// makeContent builds the content file from an AssetData dump (--make-content).
func makeContent(dumpDir, file string) error {
	if file == "" {
		return errors.New("--make-content needs --content or --data")
	}
	c, err := content.Build(dumpDir)
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
	st, cont, contentInfo, err := openData(cfg)
	if err != nil {
		return nil, err
	}
	// the log last, so a setup error leaves no open log file behind (the listener errors below close it)
	log, err := logging.Open(cfg.logDir)
	if err != nil {
		return nil, err
	}
	in := &instance{log: log}
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
	go (&web.HTTP{Log: log, WWW: www}).Serve(in.listeners[3])
	go (&telemetry.Sink{Log: log}).Serve(in.listeners[4])
	if in.gmsUDP != nil {
		go (&gms.Logger{Log: log, Conn: in.gmsUDP}).Serve()
	}

	serving := ""
	if www != "" {
		serving = " serving " + www
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

func main() {
	cfg := config{redirectorPort: redirector.Port}
	flag.StringVar(&cfg.bind, "bind", "127.0.0.1", "IPv4 address every listener binds (A5: a LAN address for clients on other machines)")
	flag.StringVar(&cfg.publicHost, "public-host", "127.0.0.1", "IPv4 address or name handed to clients: redirector reply, QoS ping site, telemetry, GMS address (A5)")
	host := flag.String("host", "", "shorthand for --bind and --public-host with the same address")
	flag.IntVar(&cfg.blazePort, "blaze-port", 10041, "Blaze port (plain TCP)")
	flag.IntVar(&cfg.qosPort, "qos-port", 17502, "QoS ping-site port announced in preAuth")
	flag.IntVar(&cfg.httpPort, "http-port", 18080, "HTTP port; must match patch_config.py --http-port")
	flag.IntVar(&cfg.telemetryPort, "telemetry-port", 9988, "telemetry port; must match patch_config.py --telemetry-port")
	flag.IntVar(&cfg.gmsPort, "gms-port", blaze.GMSPort, "UDP port of the GMS address announced in NotifyGameSetup (logged only)")
	flag.BoolVar(&cfg.gmsExternal, "gms-external", false, "announce --gms-port without binding it; another process serves the GMS")
	flag.StringVar(&cfg.gmsDir, "gms-dir", "", `start a match host per game from this host copy's Dawngate.exe (C1; e.g. client\run_854_server); default: announce --gms-port`)
	flag.StringVar(&cfg.gmsArgs, "gms-args", "-multipleInstances", "command line of the match hosts (-multipleInstances: no single-instance mutex, so a host runs beside a client)")
	flag.StringVar(&cfg.gmsPorts, "gms-ports", gmshost.DefaultPorts, "UDP port pool of the match hosts (one per running match)")
	flag.StringVar(&cfg.gmsData, "gms-data", "", `give each match host its own data folder <dir>\<match id> (DGGMS_DATA: its dgsim.log, AppData, Documents; its stdout in stdout.txt there); default: the DLL's <game dir>\host\<match id>`)
	flag.IntVar(&cfg.gmsWarm, "gms-warm", 0, "keep this many idle match hosts ready on this machine (H1-live warm pool; each holds a port of --gms-ports); 0 = start a host per game")
	flag.StringVar(&cfg.gmsOptions, "gms-options", "", `DGGMS_OPTIONS for the match hosts: "key=value;..." over the host copy's dgsim.ini [host] keys (development runs)`)
	flag.IntVar(&cfg.gmsControlPort, "gms-control-port", gmshost.DefaultControlPort, "TCP port of the control channel the match hosts (and agents) dial (JSON Lines)")
	flag.StringVar(&cfg.gmsControlBind, "gms-control-bind", "127.0.0.1", "IPv4 address the control port binds; a LAN address only for agents on other machines (needs --agent-key; H2)")
	flag.StringVar(&cfg.agentKey, "agent-key", "", "agent key file (H2): a main server takes agents that know it; in agent mode, the key to register with")
	flag.StringVar(&cfg.agent, "agent", "", "agent mode (H2): register with the main server's control port at this address (IP:port) and start match hosts from --gms-dir here")
	flag.StringVar(&cfg.agentName, "agent-name", "", "agent mode: this host machine's name in the main server's log (default: the computer name)")
	makeAgentKey := flag.String("make-agent-key", "", "write a new random agent key to this file (not overwritten if it exists), then exit")
	flag.DurationVar(&cfg.gmsTimeouts[0], "gms-hello-timeout", gmshost.DefaultHelloTimeout, "kill a match host without hello after this")
	flag.DurationVar(&cfg.gmsTimeouts[1], "gms-ready-timeout", gmshost.DefaultReadyTimeout, "kill a match host without gameReady this long after createGame")
	flag.DurationVar(&cfg.gmsTimeouts[2], "gms-heartbeat-timeout", gmshost.DefaultHeartbeatTimeout, "kill a ready match host silent for this long")
	flag.StringVar(&cfg.www, "www", "", "serve files from this directory on the HTTP port (default: built-in answers, else 404)")
	flag.StringVar(&cfg.certs, "certs", besideBin("certs"), "folder with server_cert.pem and server_key.pem")
	flag.StringVar(&cfg.logDir, "log", besideBin("logs"), "log folder")
	flag.StringVar(&cfg.dataDir, "data", besideBin("data"), `data folder: accounts.json (accounts, player content) and content.json; "" = accounts in memory only`)
	flag.StringVar(&cfg.contentPath, "content", "", `content file (default <data>\content.json)`)
	makeContentDir := flag.String("make-content", "", "build the content file from this AssetData dump folder (re/scripts/assetdata.py dump), then exit")
	writeWWW := flag.String("write-www", "", "write the built-in web answers under this folder at the paths the client requests (for --www), then exit")
	cfg.policy = blaze.DefaultPolicy()
	flag.StringVar(&cfg.policy.Owned, "owned", cfg.policy.Owned, `inventory of new accounts: "all" or "starter"`)
	startEC := flag.Uint("start-ec", uint(cfg.policy.StartEC), "earned currency (Destiny) of new accounts")
	startPC := flag.Uint("start-pc", uint(cfg.policy.StartPC), "paid currency (Waypoints) of new accounts")
	flag.BoolVar(&cfg.policy.FTUEDone, "ftue-done", false, "every account is past the first-time-user (Practice-only) gate")
	perkPages := flag.Uint("perk-pages", uint(cfg.policy.PerkPages), "the most perk pages an account can have (the loadout page's STCK; B7c)")
	flag.BoolVar(&cfg.policy.SpentElsewhere, "test-spent-elsewhere", false, "test aid (B7c-live): every purchase finds its currency's balance spent elsewhere (set to 0) and is refused for insufficient funds")
	cfg.mm = blaze.DefaultMatchmaking()
	flag.IntVar(&cfg.mm.TeamSize, "mm-team-size", cfg.mm.TeamSize, "PvP matchmaking: players per team (1 = 1v1 ... 5 = 5v5)")
	flag.IntVar(&cfg.mm.MinPlayers, "mm-min-players", cfg.mm.MinPlayers, "PvP matchmaking: fewest players a match starts with once --mm-fill-after has passed (the empty slots get bots, --mm-bots)")
	flag.DurationVar(&cfg.mm.FillAfter, "mm-fill-after", cfg.mm.FillAfter, "PvP matchmaking: how long the oldest player waits for full teams before a smaller match starts")
	flag.DurationVar(&cfg.mm.MaxWait, "mm-max-wait", 0, "PvP matchmaking: the longest a player waits in the queue before it gives up (SESSION_TIMED_OUT; 0 = the client's 3 h)")
	flag.BoolVar(&cfg.mmRefuse, "mm-refuse", false, "PvP matchmaking test aid: refuse every startMatchmaking (GAMEMANAGER_ERR_INVALID_MATCHMAKING_CRITERIA)")
	flag.BoolVar(&cfg.mmBots, "mm-bots", true, "PvP matchmaking with match hosts: a game that starts with open seats (after --mm-fill-after) gets bots in them, up to --mm-team-size a team (B10d); --mm-bots=false leaves them empty")
	flag.BoolVar(&cfg.customBots, "custom-bots", false, "custom games with match hosts: bots fill the open seats when the game starts (B15; off: only the lobby's players)")
	flag.BoolVar(&cfg.shaperSelect.On, "shaper-select", false, "match hosts run shaper select with each player's shapers, skins, wards and announcers from its account (D3-live)")
	freeRotation := flag.String("free-rotation", "", `shaper select: shapers in the free rotation, comma-separated ("gunman,PC_Otter,Fenmore")`)
	shaperTimers := flag.String("shaper-timers", "", "shaper select: <pick ms>,<role/perk ms>,<skin ms>,<reveal ms> (default: the host's; practice 300000,300000,15500,16)")
	killSwitches := flag.String("kill-switches", blaze.DefaultKillSwitches, `client features switched off at every login (B14), comma-separated: Matchmaking, WalletTopUp (the add-funds button), EUGameServers, PracticeSoloGames, FTUESoloGames, PurchaseWithWaypoints, PracticeGroupGames, CustomGames, ForwardRenderer; "none" = all allowed`)
	flag.StringVar(&cfg.broadcast, "broadcast", "", `a broadcast message every login gets (B14; "@<flags> " in front sets its display flags)`)
	flag.StringVar(&cfg.broadcastFile, "broadcast-file", "", `the operator's broadcast messages, one per line, read every 2 s and sent to everyone logged in when they change (B14; default <data>\broadcast.txt, "" = none)`)
	flag.Int64Var(&cfg.broadcastFlags, "broadcast-flags", blaze.DefaultBroadcastFlags, "display flags of the broadcast messages: 4 the main menu's marquee and shaper select, 2 a System chat line (also in a match), 1 the main menu chat's notifications; added up")
	seconds := flag.Int("seconds", 0, "stop after N seconds (default: run until Ctrl+C)")
	printPorts := flag.Bool("print-ports", false, "print the TCP ports this configuration listens on, then exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Dawngate restoration server: redirector (TLS :%d), Blaze, QoS, HTTP and telemetry.\n"+
			"Usage: dgserver [flags]\n", redirector.Port)
		flag.PrintDefaults()
	}
	flag.Parse()
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	applyHostFlag(&cfg, *host, set)
	cfg.policy.StartEC, cfg.policy.StartPC = uint32(min(*startEC, math.MaxUint32)), uint32(min(*startPC, math.MaxUint32))
	if cfg.policy.Owned != content.OwnAll && cfg.policy.Owned != content.OwnStarter {
		fmt.Fprintf(os.Stderr, "dgserver: --owned must be %q or %q\n", content.OwnAll, content.OwnStarter)
		os.Exit(2)
	}
	if *perkPages < 1 || *perkPages > 255 {
		fmt.Fprintln(os.Stderr, "dgserver: --perk-pages must be 1 to 255")
		os.Exit(2)
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
			os.Exit(2)
		}
		cfg.shaperSelect.Timers = t
	}
	if err := applySystemFlags(&cfg, *killSwitches, set); err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		os.Exit(2)
	}

	if *makeAgentKey != "" {
		if err := writeAgentKey(*makeAgentKey); err != nil {
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			os.Exit(1)
		}
		fmt.Println("wrote a new agent key to", *makeAgentKey, "(copy it to every host machine; keep it private)")
		return
	}
	if *makeContentDir != "" {
		if err := makeContent(*makeContentDir, cfg.contentFile()); err != nil {
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			os.Exit(1)
		}
		return
	}
	if *writeWWW != "" {
		files, err := web.WriteBuiltins(*writeWWW)
		for _, f := range files {
			fmt.Println("wrote", f)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			os.Exit(1)
		}
		return
	}
	if *printPorts {
		var ps []string
		for _, p := range cfg.printPorts() {
			ps = append(ps, strconv.Itoa(p))
		}
		fmt.Println(strings.Join(ps, " "))
		return
	}

	if cfg.agent != "" {
		ag, err := startAgent(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "dgserver:", err)
			os.Exit(1)
		}
		waitForStop(*seconds)
		ag.stop()
		return
	}
	in, err := start(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dgserver:", err)
		os.Exit(1)
	}
	waitForStop(*seconds)
	in.stop()
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

// waitForStop returns on Ctrl+C or after seconds (0 = never).
func waitForStop(seconds int) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	var timeout <-chan time.Time
	if seconds > 0 {
		timeout = time.After(time.Duration(seconds) * time.Second)
	}
	select {
	case <-interrupt:
	case <-timeout:
	}
}
