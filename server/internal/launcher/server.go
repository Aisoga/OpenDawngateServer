package launcher

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/gamedl"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/hostcopy"
	"dawngate/server/internal/sitelinks"
	"dawngate/server/internal/version"
)

// ServerConfigName is Start-Server.exe's config file, next to it.
const ServerConfigName = "Start-Server.ini"

// DefaultFolder is the setup folder's default name, next to the programs.
const DefaultFolder = "Dawngate-Data"

// ServerConfigText is the config file Start-Server.exe writes on its first start.
const ServerConfigText = `; Start-Server.ini: the options of Start-Server.exe. Start-Server.exe wrote this file on its first start;
; edit it, then start Start-Server.exe again. Lines that start with ; are comments.

; Start-Server.exe asks who will play on this server at its first start (and with --configure) and writes the
; answers here.

; The address the server listens on and tells the players.
;   127.0.0.1      this PC only: you play here with Start-Client.exe (the default)
;   a LAN address  this PC's address in your home network (e.g. 192.168.1.20): players on other machines
;                  add that address to their server list (README.txt, "Playing over your home network").
;   a public one   your internet address, for players anywhere: the public profile below, at your own risk
;                  (Start-Server prints what that means and starts only with the acknowledgement below; you
;                  forward the tunnel's port and 443 on your router yourself).
address = 127.0.0.1

; Only for a public address behind a router: the address of this PC that the server listens on (its LAN address),
; while "address" is the public one players use. Empty: the server listens on "address" itself.
bind =

; A public address needs your explicit acknowledgement that you have read the risk notice Start-Server prints:
; yes, or the flag  Start-Server.exe --i-accept-the-risk
i_accept_the_risk = no

; The profile:
;   auto    public for a public address, home otherwise (the default)
;   home    a server for this PC or your home network: the options below as they are
;   public  a server for strangers: the encrypted tunnel is required (tunnel = off is refused) and the limits are on
;           (connections, logins, games and packets per address); choose signup = invite or closed and set up the
;           website (web_domain). A public address always takes it; on a LAN address it tests a public setup.
profile = auto

; The encrypted tunnel for the players' connections (TLS 1.3 and AES-GCM on one port, TCP and UDP):
;   auto  on (the default), for every address: players reach the server only through it, on gateway_port. On the
;         internet forward gateway_port, TCP and UDP, on your router, and 443 for the website.
;   on    the same
;   off   the developers' option: no tunnel, the server's plain ports instead (players need Start-Client.exe
;         --tunnel off and confirm the server's certificate fingerprint); not allowed with the public profile
tunnel = auto

; The tunnel's port, TCP and UDP.
gateway_port = 42128

; Shaper select before every match: on, or off (then everyone plays Fenmore).
shaper_select = on

; Who can make an account. Players log in with a login name (3-32 of A-Z a-z 0-9 . _ - @) and a password
; (8-63 characters), which the server checks.
;   open    the first login with a new login name makes the account, with the password typed then (the default;
;           a public server should take invite or closed)
;   invite  new accounts only with an invite code you make on the website's admin page or with
;           Start-Server.exe --make-invite   (players register with it on the server's website, below)
;   closed  only you make accounts:   Start-Server.exe --create-account <login name>   (prints the password)
; A player forgot the password:   Start-Server.exe --reset-password <login name>   prints a new one.
; The website's admin page (/admin) changes the mode while the server runs and keeps it across restarts, until you
; change it here: the most recent change wins. Give an account the admin role with
;   Start-Server.exe --make-admin <login name>
signup = open

; Under signup = open: the most accounts one internet address may make per day (addresses of this PC and of
; your home network never count). 0: no limit.
signups_per_address = 3

; Delete the server's logs after this many days (they hold login names and addresses). 0: keep them.
log_days = 30

; The server's website: players make their account there (under signup = invite with your code), change their
; password, delete their account or download their data, and see the server's status. HTTPS on this port; 0: none.
; If another program holds the port, the server starts without the website (and says so), unless web_domain is set.
web_port = 443

; Your domain name for the website, e.g. play.example.org, whose DNS A record points at your public address: the
; server then gets a free certificate for it from Let's Encrypt by itself, which needs TCP 443 forwarded to this PC
; (to web_port). Empty: a self-signed certificate, which browsers warn about once (compare its fingerprint with the
; one Start-Server prints). The site's folder Dawngate-Data\data\site also takes your privacy.html, terms.html and
; about.html (HTML fragments shown on the site's pages).
web_domain =

; With web_domain: an e-mail address Let's Encrypt may write to about the certificate (optional).
web_email =

; Behind a reverse proxy: when a web server on this machine (nginx, for example) already serves HTTPS on port 443
; for other sites, it can serve the Dawngate website too.
;   off  the server serves its website itself on web_port, as above (the default)
;   on   the website runs as plain HTTP on 127.0.0.1:web_port only, for your proxy; set web_port to a free port
;        above 1023 (e.g. 8095). Your proxy holds web_domain's certificate and forwards https://<web_domain>/ to
;        http://127.0.0.1:<web_port> (the hosting guide, "The website behind nginx"). Needs web_domain; web_email
;        isn't used.
web_proxy = off

; A ready game client for your players (needs the tunnel):
;   off  none (the default)
;   on   the server hands out the release of its own version for Windows with a Start-Client.ini that preselects this
;        server and trusts its key, at /dawngate/download/OpenDawngate-<name>.zip on the website (its Download page).
;        The server downloads that release from github.com once (its only outgoing connection besides the website's
;        certificate) and keeps it in Dawngate-Data\data\client-package.
client_package = off

; The folder for the game copies (about 3 GB), the certificates, the accounts and the logs: relative to this
; program's folder, or a full path such as D:\Games\Dawngate-Data.
folder = Dawngate-Data

; Dawngate.zip from archive.org, only needed for the first setup and for repairs. Empty: next to this program,
; then in your Downloads folder; if it isn't found there, Start-Server.exe offers to download it (in a console) or
; asks for it.
zip =

; Mirrors of Dawngate.zip, tried in turn after archive.org when the zip is downloaded, separated by commas, e.g.
; https://mirror.example.org/Dawngate.zip. Every download is checked against the original build before it is used.
game_sources =

; A dedicated server, one that only hosts other players:
;   no   the default. Start-Server.exe makes no player copy anyway while the tunnel is on (Start-Client.exe makes
;        its own); with tunnel = off it also sets up the player copy (Dawngate-Data\client) for this server.
;   yes  only the match-host copy (Dawngate-Data\host) is set up and run, never a player copy, whatever the tunnel:
;        about 1.5 GB less, and a complete folder (host, certs, data) starts without Dawngate.zip. A player copy
;        already in the folder is left alone. Or the flag  Start-Server.exe --dedicated
dedicated = no

; Discord channel posts by webhook (the hosting guide, "Discord channel posts"). Whoever has a webhook address
; (https://discord.com/api/webhooks/...) can post to its channel: keep this file private. In Discord: Server
; Settings, Integrations, Webhooks, New Webhook, choose the channel, Copy Webhook URL. Empty: no posts.
;   discord_webhook         the queue message and the match results, unless they have their own below
;   discord_webhook_queue   the queue message's own channel
;   discord_webhook_result  the match results' own channel
;   discord_webhook_server  the server's status (online, going down, back after a crash): only this one, never
;                           discord_webhook
discord_webhook =
discord_webhook_queue =
discord_webhook_result =
discord_webhook_server =

; The queue message: the id of a role it pings (empty: none), at most every discord_ping_cooldown and not when the
; match starts within discord_ping_min_wait anyway; discord_queue_edit on: one message edited live, off: a new post
; per change. Player names in the posts: on, or off ("a player"). The modes whose results are posted: pvp, custom,
; practice (comma-separated), or none.
discord_queue_ping =
discord_ping_cooldown = 15m
discord_ping_min_wait = 30s
discord_queue_edit = on
discord_names = on
discord_result_modes = pvp,custom

; More server options, separated by blanks, e.g.  --owned starter  or  --mm-team-size 1 --mm-fill-after 5s
; (README.txt lists a few; "dgserver.exe -h" in the developers' build lists all). Empty: the defaults.
server_flags =
`

// oldServerTunnelComments are the tunnel key's comments that earlier releases wrote into Start-Server.ini, when auto
// meant on only for a public address; a start replaces them with ServerConfigText's (the value stays: an explicit off
// is kept, auto now means on).
var oldServerTunnelComments = [][]string{
	{"; The encrypted tunnel for the players' connections (TLS 1.3 and AES-GCM on one port, TCP and UDP):",
		";   auto  on for a public address (players on the internet reach the server only through it: forward",
		";         gateway_port, TCP and UDP, on your router, and nothing else), off on this PC and on a LAN (the default)",
		";   on    also on a LAN address (players there use it with Start-Client.exe --tunnel on)",
		";   off   never (a public server then needs all its ports forwarded, unencrypted)"},
	{"; The encrypted tunnel for the players' connections (TLS 1.3 and AES-GCM on one port, TCP and UDP):",
		";   auto  on for the public profile (players on the internet reach the server only through it: forward",
		";         gateway_port, TCP and UDP, on your router, and 443 for the website), off on this PC and on a LAN",
		";   on    also on a LAN address (players there use it with Start-Client.exe <address> --tunnel on)",
		";   off   never (not allowed with the public profile)"},
}

// manifest is the build the setup expects (nil: build 854); the tests set a synthetic one.
var manifest *archive.Manifest

var serverKeys = []string{"address", "bind", RiskAckKey, "profile", "tunnel", "gateway_port", "shaper_select", "dedicated", "signup",
	"signups_per_address", "log_days", "web_port", "web_domain", "web_email", "web_proxy", "folder", "zip", "server_flags",
	"wine", ClientPackageKey, "discord_webhook", "discord_webhook_queue", "discord_webhook_result", "discord_webhook_server", "discord_queue_ping",
	"discord_ping_cooldown", "discord_ping_min_wait", "discord_names", "discord_queue_edit", "discord_result_modes"}

// IO is where a launcher talks: the console normally, buffers in tests.
type IO struct {
	In          io.Reader
	Out         io.Writer
	Interactive bool // ask questions (a console, not --non-interactive)
	Live        bool // Out is a console (progress in place)
	OwnConsole  bool // the window closes with the program: wait for Enter after an error
	ExeDir      string
}

// StdIO is the console's IO for the program in exeDir.
func StdIO(nonInteractive bool) IO {
	return IO{In: os.Stdin, Out: os.Stdout, Interactive: !nonInteractive && IsConsole(os.Stdin),
		Live: IsConsole(os.Stdout), OwnConsole: OwnConsole(), ExeDir: ExeDir()}
}

func (o IO) fail(format string, a ...any) int {
	fmt.Fprintf(o.Out, "\n"+format+"\n", a...)
	o.pause()
	return 1
}

func (o IO) pause() {
	if o.OwnConsole && o.Interactive {
		fmt.Fprint(o.Out, "\nPress Enter to close this window.")
		bufio.NewReader(o.In).ReadString('\n')
	}
}

// zipFinder is the setup's FindZip: the configured or usual places, then the download (gamezip.go: in a console after
// a question, without one only from sources named on the command line; never when zip = names a file), then
// (interactively) the question for the file.
func zipFinder(o IO, in *bufio.Reader, configured string, gs gameSources) func() (string, error) {
	return func() (string, error) {
		cands := zipPlaces(o.ExeDir, configured, gs)
		if p := FirstFile(cands); p != "" {
			return p, nil
		}
		if configured == "" {
			p, err := offerGameDownload(o, in, gs)
			switch {
			case p != "":
				return p, nil
			case errors.Is(err, gamedl.ErrNoSpace):
				return "", err
			case err != nil && !errors.Is(err, errDeclined):
				if !o.Interactive {
					return "", fmt.Errorf("%w: %v; put it next to the program, or name another source (--%s)", setup.ErrNoZip,
						err, GameSourceFlag)
				}
				fmt.Fprintf(o.Out, "  %v\n", err)
			}
		}
		if !o.Interactive {
			return "", fmt.Errorf("%w (looked in %s); put it next to the program or name it in the config file (zip =), "+
				"or start the program in a console to download it", setup.ErrNoZip, strings.Join(cands, ", "))
		}
		var pick func() (string, error) // Windows' file dialog; elsewhere only a typed path
		if runtime.GOOS == "windows" {
			home, _ := os.UserHomeDir()
			pick = func() (string, error) { return PickZip(filepath.Join(home, "Downloads")) }
		}
		return AskZip(in, o.Out, pick)
	}
}

// RunServer is Start-Server.exe (cmd/start-server): its first start writes Start-Server.ini, finds Dawngate.zip and
// sets everything up in the folder next to it (setup.Server), every start checks (and repairs) that setup, then runs
// dgserver in this process with the match-host copy until Ctrl+C or the window is closed. It returns the exit code.
func RunServer(args []string, o IO) int {
	if hostsUnderWine { // Linux: start-server and Linux's tools in the texts (wine.go)
		o.Out = linuxWriter{o.Out}
	}
	args, accountArgs := SplitAccountArgs(args)
	fs := flag.NewFlagSet("Start-Server", flag.ContinueOnError)
	fs.SetOutput(o.Out)
	configPath := fs.String("config", "", "the config file (default: "+ServerConfigName+" next to this program)")
	setupOnly := fs.Bool("setup-only", false, "set up or repair everything, then exit without starting the server")
	check := fs.Bool("check", false, "only report what is missing or stale; change nothing")
	fs.Bool("non-interactive", false, "never ask anything (fail when Dawngate.zip isn't found); for scripts and tests")
	seconds := fs.Int("seconds", 0, "stop the server after N seconds (tests)")
	hostDir := fs.String("host-files", "", `the folder with dgsim.dll and lua\ (default: host\ next to this program)`)
	zipFlag := fs.String("zip", "", "Dawngate.zip (instead of the config file's zip)")
	gameSourceFlag := fs.String(GameSourceFlag, "", "where to download Dawngate.zip from when it isn't found (addresses separated by commas; "+
		"only these, without asking)")
	riskFlag := fs.Bool(RiskAckFlag, false, "accept the risk of a server on a public address (needed to start one)")
	dedicatedFlag := fs.Bool("dedicated", false, "a dedicated server: set up and run only the match-host copy, never a player copy (the config file's dedicated = yes)")
	configure := fs.Bool("configure", false, "ask the first start's questions again (who plays: this PC, a home network, the internet)")
	showVersion := fs.Bool("version", false, "print this release's version and exit (players need a Start-Client.exe of the same version)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(o.Out, version.String())
		return 0
	}
	in := bufio.NewReader(o.In)
	fmt.Fprintf(o.Out, "OpenDawngateServer: Start-Server, version %s\n", version.String())
	path := *configPath
	if path == "" {
		path = filepath.Join(o.ExeDir, ServerConfigName)
	}
	cfg, created, err := LoadOrCreate(path, serverConfigText())
	if err != nil {
		return o.fail("The config file %s can't be read or written: %v", path, err)
	}
	if created {
		fmt.Fprintf(o.Out, "  wrote %s with the default options (edit it to change them)\n", path)
	} else if !*check {
		// an earlier release's file: tunnel = auto meant on only for a public address, now for every server
		if changed, err := migrateComment(path, oldServerTunnelComments, commentOf(serverConfigText(), "tunnel")); err != nil {
			fmt.Fprintf(o.Out, "  note: %s can't be updated: %v\n", path, err)
		} else if changed {
			fmt.Fprintf(o.Out, "  note: %s: the encrypted tunnel is now on for every server (tunnel = auto; the comment above it updated)\n", path)
		}
	}
	for _, k := range cfg.Unknown(serverKeys...) {
		fmt.Fprintf(o.Out, "  note: %s: unknown option %q (ignored)\n", path, k)
	}
	if len(accountArgs) > 0 { // an account command on the setup folder's database, then exit
		folder := cfg["folder"]
		if folder == "" {
			folder = DefaultFolder
		}
		l := setup.Layout{Root: Resolve(o.ExeDir, folder)}
		if code := dgserver.Main(append([]string{"--data", l.Data()}, accountArgs...)); code != 0 {
			return o.fail("The account command failed (see above).")
		}
		return 0
	}
	if (created || *configure) && !*check { // the first start asks who will play (the public profile's questions)
		if !o.Interactive {
			if *configure {
				return o.fail("--configure asks questions: start it in a console without --non-interactive.")
			}
		} else {
			folder := cfg["folder"]
			if folder == "" {
				folder = DefaultFolder
			}
			if _, err := AskServerProfile(in, o.Out, path, setup.Layout{Root: Resolve(o.ExeDir, folder)}.Data()); err != nil {
				return o.fail("%s: %v (start Start-Server.exe --configure to answer again, or edit the file)", path, err)
			}
			if cfg, err = ReadConfig(path); err != nil {
				return o.fail("The config file %s can't be read: %v", path, err)
			}
			fmt.Fprintln(o.Out, "")
		}
	}
	loginFlags, err := LoginFlags(cfg)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	web, err := ReadWebsite(cfg)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	address := cfg["address"]
	if address == "" {
		address = setup.Local
	}
	bindAddr := cfg["bind"]
	if bindAddr == "" {
		bindAddr = address
	} else if _, err := setup.ClientOptions(bindAddr); err != nil {
		return o.fail("%s: bind = %s: %v", path, bindAddr, err)
	}
	riskOK, err := cfg.Bool(RiskAckKey, false)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	riskOK = riskOK || *riskFlag
	shaperSelect, err := cfg.Bool("shaper_select", true)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	dedicated, err := cfg.Bool("dedicated", false)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	dedicated = dedicated || *dedicatedFlag || hostsUnderWine // Linux: no Start-Client, so never a player copy
	clientPackage, err := cfg.Bool(ClientPackageKey, false)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	folder := cfg["folder"]
	if folder == "" {
		folder = DefaultFolder
	}
	root := Resolve(o.ExeDir, folder)
	zipConf := cfg["zip"]
	if *zipFlag != "" {
		zipConf = *zipFlag
	}
	need := int64(gameCopiesSpace)
	if dedicated {
		need = gameHostCopySpace
	}
	sources, err := newGameSources(*gameSourceFlag, cfg[GameSourcesKey], need)
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	if _, err := setup.ClientOptions(address); err != nil {
		return o.fail("%s: address = %s: %v", path, address, err)
	}
	public := IsPublicAddress(address)
	mode, err := tunnelMode(cfg["tunnel"])
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	gwPort, err := gatewayPort(cfg["gateway_port"])
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	profile, err := profileMode(cfg["profile"])
	if err != nil {
		return o.fail("%s: %v", path, err)
	}
	if err := checkProfile(profile, address, mode); err != nil {
		return o.fail("%s: %v", path, err)
	}
	publicP := publicProfile(profile, address) // the tunnel required, the limits on
	tunnelOn := tunnelFor(mode, address) || publicP
	if clientPackage && !tunnelOn {
		return o.fail("%s: %s = on needs the tunnel (tunnel = auto or on): the package's Start-Client.ini trusts its key", path, ClientPackageKey)
	}
	if public {
		logDays, _ := strconv.Atoi(firstNonEmpty(cfg["log_days"], "30"))
		ServerRiskNotice(o.Out, RiskInfo{Address: address, Bind: cfg["bind"], GatewayPort: gwPort, Signup: loginFlags[1],
			WebDomain: web.Domain, WebProxy: web.Proxy && web.Port > 0, LogDays: logDays})
		if !riskOK && !*check && !*setupOnly {
			ServerRiskRefusal(o.Out, path)
			return o.fail("%s: address = %s is a public address and the risk is not accepted.", path, address)
		}
	}
	if !*check {
		if err := thisMachine(bindAddr); err != nil {
			return o.fail("%s: %s is not an address of this PC (%v): use 127.0.0.1, or this PC's LAN address "+
				"(ipconfig shows it as IPv4 Address). Behind a router with a public address, set address to the "+
				"public one and bind to this PC's LAN address.", path, bindAddr, err)
		}
	}
	hostFiles := *hostDir
	if hostFiles == "" {
		hostFiles = filepath.Join(o.ExeDir, "host")
	}
	files := hostcopy.Files{FS: os.DirFS(hostFiles), DLL: "dgsim.dll", Lua: "lua"}
	if _, err := os.Stat(filepath.Join(hostFiles, "dgsim.dll")); err != nil {
		return o.fail("The match host's files are missing: %s has no dgsim.dll. Unpack the whole release zip, "+
			"with its host folder, next to Start-Server.exe.", hostFiles)
	}

	hostsAddr := bindAddr // the match hosts' GMS ports: with the tunnel on a loopback address of their own
	if tunnelOn {
		hostsAddr = serverHostsAddress(bindAddr) // under Wine 127.0.0.1 (wine.go)
	}
	if !*check && !*setupOnly { // before the setup: a second Start-Server must not touch the copies of a running one
		var held []string
		if tunnelOn { // the gateway is the only port for players; the services listen inside it (dgserver --plain off)
			held = HeldPorts(bindAddr, []Port{{gwPort, false, "the tunnel"}, {gwPort, true, "the tunnel"}})
			held = append(held, HeldPorts(hostsAddr, HostPorts)...)
		} else {
			held = HeldPorts(bindAddr, ServerPorts[:len(ServerPorts)-1])
		}
		held = append(held, HeldPorts(ControlBind(address), ServerPorts[len(ServerPorts)-1:])...)
		if len(held) > 0 {
			return o.fail("Another program holds the server's ports on %s:\n  %s\nIs a server already running (another "+
				"Start-Server window, or dgserver.exe)? Close it, then start Start-Server.exe again.", address,
				strings.Join(held, "\n  "))
		}
		if web.Port > 0 && web.Proxy { // behind a reverse proxy: the proxy forwards to this port, so it must be ours
			if h := HeldPorts(ProxyBind, []Port{{web.Port, false, "the website"}}); len(h) > 0 {
				return o.fail("Another program holds the website's port on %s:\n  %s\nweb_proxy = on: your reverse proxy "+
					"forwards https://%s/ to http://%s. Close that program, or set web_port to another free port (and the "+
					"proxy's proxy_pass to it).", ProxyBind, h[0], web.Domain, web.ProxyAddress())
			}
		} else if web.Port > 0 { // the website's port (443 is often taken by another program on a home PC)
			if h := HeldPorts(bindAddr, []Port{{web.Port, false, "the website"}}); len(h) > 0 {
				if web.Domain != "" {
					return o.fail("Another program holds the website's port on %s:\n  %s\nweb_domain = %s needs it (Let's "+
						"Encrypt checks the domain on it). Close that program, or set web_port to another port and "+
						"forward TCP 443 on your router to it.", bindAddr, h[0], web.Domain)
				}
				if hostsUnderWine && web.Port < 1024 && strings.Contains(h[0], "permission denied") {
					fmt.Fprintf(o.Out, "  note: port %d needs root or the capability CAP_NET_BIND_SERVICE (%s): the server starts "+
						"without its website this time (the systemd unit in hosting.md gives the capability)\n", web.Port, h[0])
				} else {
					fmt.Fprintf(o.Out, "  note: another program holds the website's port %d (%s): the server starts without its "+
						"website this time (set web_port = 8443, for example, in %s)\n", web.Port, h[0], path)
				}
				web.Port = 0
			}
		}
	}
	var runLock *setup.RunLockHandle
	if !*check { // one server per setup folder: also not beside Start-Client's own server for This PC on it
		gw := 0
		if tunnelOn {
			gw = gwPort
		}
		h, held, err := setup.TakeRunLock(root, setup.RunLock{Program: "Start-Server", Address: address, Dial: bindAddr,
			GatewayPort: gw, State: setup.RunSetup})
		if errors.Is(err, setup.ErrServerRuns) {
			if held.Program == "Start-Client" {
				return o.fail("This folder's server already runs inside Start-Client.exe (This PC in the game's server list, "+
					"pid %d). A setup folder has one server: close the game (its server stops with Start-Client), then start "+
					"Start-Server.exe again, or give Start-Server a folder of its own (folder = in %s).", held.Pid, path)
			}
			return o.fail("This folder's server already runs in %s (pid %d): close it first (a setup folder has one "+
				"server), or give this Start-Server a folder of its own (folder = in %s).", held.Program, held.Pid, path)
		}
		if err != nil {
			return o.fail("The folder's server lock: %v", err)
		}
		runLock = h
		defer h.Release()
		if hostsUnderWine { // the accounts, the keys and the logs: the operator's alone
			if err := os.Chmod(root, 0o700); err != nil {
				fmt.Fprintf(o.Out, "  note: %s can't be made private (%v)\n", root, err)
			}
		}
	}
	where := "this PC only"
	if !net.ParseIP(address).IsLoopback() {
		where = "players on your network connect to it"
		if public {
			where = "PUBLIC: players on the internet connect to it, at your own risk"
		}
	}
	if publicP {
		where += "; the public profile: the tunnel, the limits, sign-up " + loginFlags[1]
	}
	fmt.Fprintf(o.Out, "  folder   %s\n  address  %s (%s)\n", root, address, where)
	switch {
	case hostsUnderWine:
		fmt.Fprintln(o.Out, "  mode     dedicated server, the match hosts under Wine (the match-host copy only)")
	case dedicated:
		fmt.Fprintln(o.Out, "  mode     dedicated server (the match-host copy only, no player copy)")
	}
	if web.Proxy && web.Port > 0 {
		fmt.Fprintf(o.Out, "  website  %s\n", proxyWebsiteLine("https://"+web.Domain+"/", web.ProxyAddress()))
	}
	fmt.Fprintln(o.Out)
	if *check {
		fmt.Fprintln(o.Out, "Checking the setup (nothing is changed):")
	} else {
		size := "3 GB"
		switch {
		case hostsUnderWine:
			size = "3 GB: the match-host copy and the hosts' Wine prefix"
		case dedicated:
			size = "1.5 GB: the match-host copy only"
		case tunnelOn:
			size = "1.5 GB; Start-Client.exe makes the player copy"
		}
		fmt.Fprintf(o.Out, "Checking the setup (the first start sets everything up: about a minute and %s):\n", size)
	}
	l := setup.Layout{Root: root}
	// with a web domain the client copy's login links go to https://<domain>/<page> (sitelinks.Base)
	s := &setup.Setup{Layout: l, Out: o.Out, Live: o.Live, Check: *check, FindZip: zipFinder(o, in, zipConf, sources), HostFiles: files,
		Manifest: manifest, WebDomain: web.Domain}
	var release func()
	if !*check {
		if release, err = setup.Lock(root); err != nil {
			return o.fail("%v", err)
		}
	}
	var wine setup.WineEnv
	if hostsUnderWine { // first Wine (and its prefix): without it no host can start, so nothing else is set up
		wine, err = prepareWine(s, setup.WineOptions{Command: strings.TrimSpace(cfg["wine"]), Prefix: os.Getenv("WINEPREFIX")})
		if err != nil && !*check {
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
	// with the tunnel Start-Client makes the player copy; dedicated = yes: never one, also with tunnel = off
	so := setup.ServerOptions{Address: address, NoClient: tunnelOn}
	if dedicated {
		so.NoClient = true
		so.NoClientNote = "a dedicated server: the match-host copy only (Start-Client.exe makes its own player copy)"
		if hostsUnderWine {
			so.NoClientNote = "a Linux server: the match-host copy only (players play with Start-Client.exe on Windows)"
		}
	}
	err = s.Server(so)
	if release != nil {
		release()
	}
	if *check {
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
	if *setupOnly {
		return 0
	}

	own := append(append(loginFlags, web.Flags()...), DiscordFlags(cfg)...)
	if clientPackage {
		own = append(own, "--client-package")
	}
	if publicP { // the limits on also for a public profile on a LAN address (dgserver's auto covers a public one)
		own = append(own, "--limits", "on")
	}
	if hostsUnderWine { // the hosts under the Wine the setup checked, in its prefix
		own = append(own, "--gms-runner", wine.Wine)
		os.Setenv("WINEPREFIX", wine.Prefix)
	}
	flags := ServerFlags(l, address, shaperSelect, append(own, SplitFlags(cfg["server_flags"])...))
	if bindAddr != address {
		flags = append(flags, "--bind", bindAddr) // later wins: listen on this PC's address, announce the public one
	}
	if tunnelOn { // the gateway is the players' only port: no plain listener, the match hosts on a loopback address
		flags = append(flags, "--tunnel", "on", "--gateway-port", strconv.Itoa(gwPort), "--plain", "off", "--gms-bind", hostsAddr)
	} else { // tunnel = off (developers): the plain ports, and the CA for Start-Client --tunnel off on other machines
		flags = append(flags, "--serve-ca")
	}
	if *seconds > 0 {
		flags = append(flags, fmt.Sprintf("--seconds=%d", *seconds))
	}
	fmt.Fprintln(o.Out, "\nStarting the server ...")
	code := dgserver.Run(flags, dgserver.Hooks{Started: func(info dgserver.Info) {
		if err := runLock.Set(setup.RunRunning, info.GatewayPin); err != nil { // Start-Client's This PC finds it running
			fmt.Fprintf(o.Out, "  note: the folder's server lock: %v\n", err)
		}
		fmt.Fprintln(o.Out, "")
		fmt.Fprintf(o.Out, "==== The server runs on %s. ====\n", address)
		fmt.Fprintf(o.Out, "  Version %s: players need Start-Client.exe of the same release (it refuses a login to any other).\n", info.Version)
		if info.GatewayPort > 0 {
			fmt.Fprintf(o.Out, "  Players connect through the encrypted tunnel on %s port %d (TCP and UDP), its only port for players.\n",
				bindAddr, info.GatewayPort)
			fmt.Fprintf(o.Out, "  The server's key fingerprint is %s.\n", info.GatewayFingerprint)
			switch {
			case net.ParseIP(address).IsLoopback() && hostsUnderWine:
				fmt.Fprintf(o.Out, "  It listens on %s, so only this machine reaches it: for players set address in %s to this\n"+
					"  machine's address (start-server --configure asks for it).\n", address, ServerConfigName)
			case net.ParseIP(address).IsLoopback():
				fmt.Fprintln(o.Out, "  Now double-click Start-Client.exe to play: This PC in its server list is this server (the same folder).")
			default:
				site443 := "the website"
				if info.WebProxy != "" {
					site443 = "the website, through your reverse proxy"
				}
				if public {
					fmt.Fprintf(o.Out, "  Forward TCP and UDP %d and TCP 443 (%s) on your router to this PC, and no other port.\n", info.GatewayPort, site443)
					if bindAddr == address {
						fmt.Fprintf(o.Out, "  This PC holds the public address itself: let its firewall in only TCP and UDP %d and TCP 443.\n", info.GatewayPort)
					}
				} else if hostsUnderWine {
					fmt.Fprintf(o.Out, "  If this machine has a firewall, let in TCP and UDP %d (and TCP 443 for %s).\n", info.GatewayPort, site443)
				} else {
					fmt.Fprintln(o.Out, "  If Windows asks whether the server may use the network, allow it for private networks.")
				}
				fmt.Fprintf(o.Out, "  Players add %s to the server list on the game's login screen (or start Start-Client.exe %s);\n", address, address)
				if hostsUnderWine {
					fmt.Fprintf(o.Out, "  at a player's first login the list shows the key\n")
				} else {
					fmt.Fprintf(o.Out, "  you play here with just Start-Client.exe. At a player's first login the list shows the key\n")
				}
				fmt.Fprintf(o.Out, "  fingerprint, which must be %s; logging in again trusts it. Or hand out the whole key, so\n", info.GatewayFingerprint)
				fmt.Fprintf(o.Out, "  that nobody needs to compare:  Start-Client.exe %s --gateway-pin %s\n", address, info.GatewayPin)
			}
		} else if net.ParseIP(address).IsLoopback() { // tunnel = off: the developers' direct path
			fmt.Fprintln(o.Out, "  Now double-click Start-Client.exe to play (without the tunnel: tunnel = off here, which it follows).")
		} else {
			fmt.Fprintf(o.Out, "  tunnel = off: players start Start-Client.exe %s --tunnel off (you too, on this PC: just\n", address)
			fmt.Fprintln(o.Out, "  Start-Client.exe, which follows this folder's tunnel = off).")
			fmt.Fprintf(o.Out, "  The first time, they are shown the server's certificate fingerprint: it must be %s.\n", info.CAFingerprint)
			fmt.Fprintln(o.Out, "  If Windows asks whether the server may use the network, allow it for private networks.")
		}
		if publicP {
			fmt.Fprintln(o.Out, "  The public profile: players only through the tunnel, the limits on (connections, logins, games, packets).")
		}
		fmt.Fprintf(o.Out, "  %s\n", loginHint(cfg["signup"]))
		websiteBanner(o.Out, info, public)
		packageBanner(o.Out, info)
		if hostsUnderWine {
			fmt.Fprintf(o.Out, "  The match hosts run under %s (%s) on 127.0.0.1, in the Wine prefix %s.\n", wine.Version, wine.Wine, wine.Prefix)
		}
		fmt.Fprintf(o.Out, "  Logs: %s\n", l.Logs())
		fmt.Fprintln(o.Out, "  Stop the server with Ctrl+C or by closing this window (it ends running matches).")
		fmt.Fprintln(o.Out, "")
	}})
	if code != 0 {
		return o.fail("The server stopped with an error (see above).")
	}
	fmt.Fprintln(o.Out, "Server stopped.")
	return 0
}

// LoginFlags are dgserver's flags for Start-Server.ini's login keys: signup (open, invite, closed; default
// open), signups_per_address (default 3) and log_days (default 30). They come before server_flags, which can override
// them.
func LoginFlags(cfg Config) ([]string, error) {
	mode := cfg["signup"]
	if mode == "" {
		mode = "open"
	}
	m, err := accounts.ParseMode(mode)
	if err != nil {
		return nil, fmt.Errorf("signup = %s: use open, invite or closed", mode)
	}
	f := []string{"--signup", m.String()}
	for _, k := range []struct{ key, flag, def string }{
		{"signups_per_address", "--signups-per-address", "3"}, {"log_days", "--log-days", "30"}} {
		v := cfg[k.key]
		if v == "" {
			v = k.def
		}
		if n, err := strconv.Atoi(v); err != nil || n < 0 {
			return nil, fmt.Errorf("%s = %s: use a whole number, 0 or more", k.key, v)
		}
		f = append(f, k.flag, v)
	}
	return f, nil
}

// Website is Start-Server.ini's website keys: web_port (default 443, also for an ini of an earlier release;
// 0 = none), web_domain (a DNS name: Let's Encrypt; empty: self-signed), web_email, web_proxy (on: plain HTTP on
// ProxyBind:web_port for a reverse proxy that serves web_domain's HTTPS).
type Website struct {
	Port          int
	Domain, Email string
	Proxy         bool
}

// ProxyBind is where the website listens behind a reverse proxy (dgserver's default --web-bind).
const ProxyBind = dgserver.DefaultWebProxyBind

// ProxyAddress is the address the reverse proxy forwards to (web_proxy = on), e.g. 127.0.0.1:8095.
func (w Website) ProxyAddress() string { return net.JoinHostPort(ProxyBind, strconv.Itoa(w.Port)) }

// ReadWebsite reads the website keys.
func ReadWebsite(cfg Config) (Website, error) {
	w := Website{Port: 443, Email: strings.TrimSpace(cfg["web_email"])}
	d, err := sitelinks.WebsiteDomain(cfg["web_domain"]) // dgserver's --web-domain rule
	if err != nil {
		return w, fmt.Errorf("web_domain = %s: use a domain name such as play.example.org (or leave it empty)",
			strings.TrimSpace(cfg["web_domain"]))
	}
	w.Domain = d
	if v := strings.TrimSpace(cfg["web_port"]); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 65535 {
			return w, fmt.Errorf("web_port = %s: use a port number (443), or 0 for no website", v)
		}
		w.Port = n
	}
	proxy, err := cfg.Bool("web_proxy", false)
	if err != nil {
		return w, err
	}
	w.Proxy = proxy
	if w.Proxy && w.Port != 0 { // web_port = 0 turns the website off, the proxy mode with it
		if w.Domain == "" {
			return w, fmt.Errorf("web_proxy = on needs web_domain: the domain name your reverse proxy serves the website on")
		}
		if w.Port < 1024 {
			return w, fmt.Errorf("web_proxy = on: web_port = %d is the proxy's side; set web_port to a free port above "+
				"1023 (e.g. 8095), where the website listens on %s for your proxy", w.Port, ProxyBind)
		}
	}
	if w.Domain != "" && w.Port == 0 {
		return w, fmt.Errorf("web_domain = %s needs a web_port (443)", w.Domain)
	}
	return w, nil
}

// Flags are dgserver's flags for the website (before server_flags, which may override them).
func (w Website) Flags() []string {
	f := []string{"--web-port", strconv.Itoa(w.Port)}
	if w.Proxy && w.Port != 0 { // the proxy holds the certificate: no e-mail for Let's Encrypt
		return append(f, "--web-domain", w.Domain, "--web-proxy")
	}
	if w.Domain != "" {
		f = append(f, "--web-domain", w.Domain)
		if w.Email != "" {
			f = append(f, "--web-email", w.Email)
		}
	}
	return f
}

// websiteBanner is the banner's part on the website (nothing without one).
func websiteBanner(w io.Writer, info dgserver.Info, public bool) {
	if info.WebURL == "" {
		return
	}
	if info.WebProxy != "" {
		fmt.Fprintf(w, "  The website: %s.\n", proxyWebsiteLine(info.WebURL, info.WebProxy))
		fmt.Fprintln(w, "  Registration, password change, account deletion and status are there; your proxy holds its certificate.")
	} else {
		fmt.Fprintf(w, "  The website: %s (registration, password change, account deletion, status).\n", info.WebURL)
	}
	fmt.Fprintf(w, "  The admin page: %sadmin (for accounts made admin with Start-Server.exe --make-admin <login name>).\n", info.WebURL)
	switch {
	case info.WebFingerprint != "":
		fmt.Fprintf(w, "  Its certificate is self-signed: browsers warn once; its SHA-256 fingerprint is\n    %s\n", info.WebFingerprint)
	case info.WebProxy == "" && public:
		fmt.Fprintln(w, "  Its certificate comes from Let's Encrypt (it checks the domain on TCP 443, which your router forwards).")
	}
}

// proxyWebsiteLine names the website behind a reverse proxy: its public address and where the proxy forwards to.
func proxyWebsiteLine(url, listen string) string {
	return fmt.Sprintf("%s (behind your reverse proxy on %s)", url, listen)
}

// loginHint is the banner's line on logging in, by sign-up mode.
func loginHint(mode string) string {
	m, err := accounts.ParseMode(mode)
	switch {
	case err != nil || m == accounts.Open:
		return "Log in with a login name and a password (8 or more characters): the first login with a new name creates the account."
	case m == accounts.Invite:
		return "New accounts need an invite code (Start-Server.exe --make-invite), entered on the website's sign-up page; or make one with --create-account <login name>."
	}
	return "Only you make accounts: Start-Server.exe --create-account <login name> prints the password to hand out."
}

// SplitAccountArgs takes dgserver's one-shot account commands (--make-invite, --create-account, --reset-password,
// --account-status, --list-invites, --revoke-invite and --make-invite's options; --import-ranked-logs with --apply and
// --force) out of Start-Server's arguments, with
// their values. A non-empty acct means: run them on the setup folder's database (dgserver --data) and exit.
func SplitAccountArgs(args []string) (own, acct []string) {
	isAccount := map[string]bool{}
	for _, f := range dgserver.AccountCommandFlags {
		isAccount[f] = true
	}
	boolFlags := map[string]bool{"make-invite": true, "list-invites": true, "apply": true, "force": true}
	options := map[string]bool{"apply": true, "force": true} // --import-ranked-logs' options, not commands of their own
	command := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || !isAccount[name] {
			own = append(own, a)
			continue
		}
		acct = append(acct, a)
		if !hasValue && !boolFlags[name] && i+1 < len(args) {
			i++
			acct = append(acct, args[i])
		}
		if !strings.HasPrefix(name, "invite-") && !options[name] {
			command = true
		}
	}
	if !command { // --invite-uses or --apply alone isn't a command: let the flag parser complain
		return args, nil
	}
	return own, acct
}

// ServerFlags are dgserver's arguments for a setup folder (start_server.ps1's: the host copy, a host per game,
// headless; plus the certificates, data and logs of the folder). RunServer adds the tunnel's (or, with tunnel = off,
// --serve-ca for players on other machines).
func ServerFlags(l setup.Layout, address string, shaperSelect bool, extra []string) []string {
	f := []string{"--bind", address, "--public-host", address, "--certs", l.Certs(address), "--data", l.Data(),
		"--log", l.Logs(), "--gms-dir", l.HostCopy(), "--gms-args=-multipleInstances", "--gms-data",
		filepath.Join(l.Logs(), "hosts"), "--gms-control-bind", ControlBind(address)}
	if shaperSelect {
		f = append(f, "--shaper-select")
	}
	return append(f, extra...)
}

// ControlBind is where the match hosts' control port binds: the server's own address when it is a loopback one (so a
// server on another loopback address, e.g. a test on 127.0.9.1, keeps off 127.0.0.1:10040), else 127.0.0.1 (a LAN
// address there would expose it, which dgserver allows only with agents, docs/reference.md *Ports*).
func ControlBind(address string) string {
	if ip := net.ParseIP(address); ip != nil && ip.IsLoopback() {
		return address
	}
	return setup.Local
}

// thisMachine checks that address can be bound here.
func thisMachine(address string) error {
	ln, err := net.Listen("tcp4", net.JoinHostPort(address, "0"))
	if err != nil {
		return err
	}
	return ln.Close()
}

func lines(ls []setup.Line) string {
	var b strings.Builder
	for i, l := range ls {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(l.String())
	}
	return b.String()
}

// setupAdvice says what to do about the steps left undone.
func setupAdvice(bad []setup.Line, root string) string {
	for _, l := range bad {
		if strings.Contains(l.Detail, "Dawngate.zip not found") || strings.Contains(l.Detail, "no Dawngate.zip") {
			return "Start the program again in a console window: it offers to download Dawngate.zip (an interrupted " +
				"download continues), or put Dawngate.zip from https://archive.org/details/Dawngate next to it first."
		}
		if strings.Contains(l.Detail, "disk space") || l.Step == "disk space" {
			return "Free some disk space (or set folder = to another drive in " + ServerConfigName + "), then start again."
		}
	}
	return "Start Start-Server.exe again: the setup continues where it stopped. If the same step fails again, delete " +
		"the folder named in it (inside " + root + ") so that it is made anew, and report the message if that doesn't help."
}
