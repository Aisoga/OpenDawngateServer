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
//	            --gms-options its DGGMS_OPTIONS (run_session.ps1 -GmsHost). Off Windows the hosts run
//	            under Wine (--gms-runner, default auto: "wine"; H4a).
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
//	    [--ftue-required] [--perk-pages 10] [--print-ports]
//	    [--gms-dir DIR [--gms-args "-multipleInstances"] [--gms-ports 10042-10051] [--gms-data DIR]
//	    [--gms-options "key=value;..."] [--gms-warm N] [--gms-runner auto|none|CMD]]
//	    [--agent-key FILE] [--gms-control-bind 127.0.0.1] [--gms-control-port 10040]
//	    [--gms-hello-timeout 30s] [--gms-ready-timeout 2m] [--gms-heartbeat-timeout 30s]
//	    [--mm-team-size 5] [--mm-min-players 1] [--mm-fill-after 15s] [--mm-max-wait 0] [--mm-bots=false] [--mm-refuse]
//	    [--custom-bots] [--leaderboard-min-games 50]
//	    [--kill-switches WalletTopUp] [--broadcast TEXT] [--broadcast-file FILE] [--broadcast-flags 4]
//	bin\dgserver.exe --agent <server IP>:10040 --agent-key FILE --gms-dir DIR --host <this machine's IP>
//	    [--public-host ADDR] [--gms-args ...] [--gms-ports 10042-10051] [--agent-name NAME] [--log DIR] [--seconds N]
//	bin\dgserver.exe --make-agent-key FILE
//	bin\dgserver.exe --make-content <AssetData dump dir | game folder> [--content FILE]
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
// the SQLite database <data>\accounts.db (--data "" keeps them in memory only); the first start imports
// a <data>\accounts.json of the earlier builds and renames it accounts.json.imported. The game catalog comes from the content
// file (--content, default <data>\content.json), which --make-content builds once from the user's own
// AssetData dump (re/scripts/assetdata.py dump, docs/ASSETDATA.md) or straight from the game
// folder (client\ia_Dawngate\Dawngate: its game\Data\AssetData.snap and Locale\en-us\Locale.package; the
// same bytes) and exits; without it the server
// runs as before B7a (no items, empty store). New accounts get the inventory of --owned, the
// currencies --start-ec/--start-pc and the lowest-catalogID avatar; every account has the
// "first-time-user experience done" flag, so PLAY opens the normal play screen; --ftue-required
// restores the original's Practice-only first screen.
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
	"os"

	"dawngate/server/internal/dgserver"
	_ "dawngate/server/internal/launcher" // --tunnel-front: Start-Client's front (dgserver.RunFront)
)

// The server's code is in internal/dgserver, so that Start-Server.exe runs the same server.
func main() { os.Exit(dgserver.Main(os.Args[1:])) }
