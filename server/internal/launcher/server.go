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
	"strconv"
	"strings"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/hostcopy"
	"dawngate/server/internal/sitelinks"
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
;                  start Start-Client.exe with that address (README.txt, "Playing over your home network").
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
;   auto  on for the public profile (players on the internet reach the server only through it: forward
;         gateway_port, TCP and UDP, on your router, and 443 for the website), off on this PC and on a LAN
;   on    also on a LAN address (players there use it with Start-Client.exe <address> --tunnel on)
;   off   never (not allowed with the public profile)
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

; The folder for the game copies (about 3 GB), the certificates, the accounts and the logs: relative to this
; program's folder, or a full path such as D:\Games\Dawngate-Data.
folder = Dawngate-Data

; Dawngate.zip from archive.org, only needed for the first setup and for repairs. Empty: next to this program,
; then in your Downloads folder; if it isn't found there, Start-Server.exe asks for it.
zip =

; More server options, separated by blanks, e.g.  --owned starter  or  --mm-team-size 1 --mm-fill-after 5s
; (README.txt lists a few; "dgserver.exe -h" in the developers' build lists all). Empty: the defaults.
server_flags =
`

// manifest is the build the setup expects (nil: build 854); the tests set a synthetic one.
var manifest *archive.Manifest

var serverKeys = []string{"address", "bind", RiskAckKey, "profile", "tunnel", "gateway_port", "shaper_select", "signup",
	"signups_per_address", "log_days", "web_port", "web_domain", "web_email", "folder", "zip", "server_flags"}

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

// zipFinder is the setup's FindZip: the configured or usual places, then (interactively) the question.
func zipFinder(o IO, in *bufio.Reader, configured string) func() (string, error) {
	return func() (string, error) {
		cands := ZipCandidates(o.ExeDir, configured)
		if p := FirstFile(cands); p != "" {
			return p, nil
		}
		if !o.Interactive {
			return "", fmt.Errorf("%w (looked in %s); put it next to Start-Server.exe or name it in the config file (zip =)",
				setup.ErrNoZip, strings.Join(cands, ", "))
		}
		home, _ := os.UserHomeDir()
		return AskZip(in, o.Out, func() (string, error) { return PickZip(filepath.Join(home, "Downloads")) })
	}
}

// RunServer is Start-Server.exe (cmd/start-server): its first start writes Start-Server.ini, finds Dawngate.zip and
// sets everything up in the folder next to it (setup.Server), every start checks (and repairs) that setup, then runs
// dgserver in this process with the match-host copy until Ctrl+C or the window is closed. It returns the exit code.
func RunServer(args []string, o IO) int {
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
	riskFlag := fs.Bool(RiskAckFlag, false, "accept the risk of a server on a public address (needed to start one)")
	configure := fs.Bool("configure", false, "ask the first start's questions again (who plays: this PC, a home network, the internet)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	in := bufio.NewReader(o.In)
	fmt.Fprintln(o.Out, "Dawngate Restoration: Start-Server")
	path := *configPath
	if path == "" {
		path = filepath.Join(o.ExeDir, ServerConfigName)
	}
	cfg, created, err := LoadOrCreate(path, ServerConfigText)
	if err != nil {
		return o.fail("The config file %s can't be read or written: %v", path, err)
	}
	if created {
		fmt.Fprintf(o.Out, "  wrote %s with the default options (edit it to change them)\n", path)
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
	folder := cfg["folder"]
	if folder == "" {
		folder = DefaultFolder
	}
	root := Resolve(o.ExeDir, folder)
	zipConf := cfg["zip"]
	if *zipFlag != "" {
		zipConf = *zipFlag
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
	if public {
		logDays, _ := strconv.Atoi(firstNonEmpty(cfg["log_days"], "30"))
		ServerRiskNotice(o.Out, RiskInfo{Address: address, Bind: cfg["bind"], GatewayPort: gwPort, Signup: loginFlags[1],
			WebDomain: web.Domain, LogDays: logDays})
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

	if !*check && !*setupOnly { // before the setup: a second Start-Server must not touch the copies of a running one
		held := HeldPorts(bindAddr, ServerPorts[:len(ServerPorts)-1])
		held = append(held, HeldPorts(ControlBind(address), ServerPorts[len(ServerPorts)-1:])...)
		if tunnelOn {
			held = append(held, HeldPorts(bindAddr, []Port{{gwPort, false, "the tunnel"}, {gwPort, true, "the tunnel"}})...)
		}
		if len(held) > 0 {
			return o.fail("Another program holds the server's ports on %s:\n  %s\nIs a server already running (another "+
				"Start-Server window, or dgserver.exe)? Close it, then start Start-Server.exe again.", address,
				strings.Join(held, "\n  "))
		}
		if web.Port > 0 { // the website's port (443 is often taken by another program on a home PC)
			if h := HeldPorts(bindAddr, []Port{{web.Port, false, "the website"}}); len(h) > 0 {
				if web.Domain != "" {
					return o.fail("Another program holds the website's port on %s:\n  %s\nweb_domain = %s needs it (Let's "+
						"Encrypt checks the domain on it). Close that program, or set web_port to another port and "+
						"forward TCP 443 on your router to it.", bindAddr, h[0], web.Domain)
				}
				fmt.Fprintf(o.Out, "  note: another program holds the website's port %d (%s): the server starts without its "+
					"website this time (set web_port = 8443, for example, in %s)\n", web.Port, h[0], path)
				web.Port = 0
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
	fmt.Fprintf(o.Out, "  folder   %s\n  address  %s (%s)\n\n", root, address, where)
	if *check {
		fmt.Fprintln(o.Out, "Checking the setup (nothing is changed):")
	} else {
		fmt.Fprintln(o.Out, "Checking the setup (the first start sets everything up: about a minute and 3 GB):")
	}
	l := setup.Layout{Root: root}
	// with a web domain the client copy's login links go to https://<domain>/<page> (sitelinks.Base)
	s := &setup.Setup{Layout: l, Out: o.Out, Live: o.Live, Check: *check, FindZip: zipFinder(o, in, zipConf), HostFiles: files,
		Manifest: manifest, WebDomain: web.Domain}
	var release func()
	if !*check {
		if release, err = setup.Lock(root); err != nil {
			return o.fail("%v", err)
		}
	}
	err = s.Server(setup.ServerOptions{Address: address})
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

	own := append(loginFlags, web.Flags()...)
	if publicP { // the limits on also for a public profile on a LAN address (dgserver's auto covers a public one)
		own = append(own, "--limits", "on")
	}
	flags := ServerFlags(l, address, shaperSelect, append(own, SplitFlags(cfg["server_flags"])...))
	if bindAddr != address {
		flags = append(flags, "--bind", bindAddr) // later wins: listen on this PC's address, announce the public one
	}
	if tunnelOn { // the gateway beside the plain ports (which a public server's router doesn't forward)
		flags = append(flags, "--tunnel", "on", "--gateway-port", strconv.Itoa(gwPort))
	}
	if *seconds > 0 {
		flags = append(flags, fmt.Sprintf("--seconds=%d", *seconds))
	}
	fmt.Fprintln(o.Out, "\nStarting the server ...")
	code := dgserver.Run(flags, dgserver.Hooks{Started: func(info dgserver.Info) {
		fmt.Fprintln(o.Out, "")
		fmt.Fprintf(o.Out, "==== The server runs on %s. ====\n", address)
		if info.GatewayPort > 0 {
			fmt.Fprintf(o.Out, "  The encrypted tunnel listens on %s port %d (TCP and UDP).\n", bindAddr, info.GatewayPort)
			if public {
				fmt.Fprintf(o.Out, "  Forward TCP and UDP %d and TCP 443 (the website) on your router to this PC, and no other port.\n", info.GatewayPort)
				if bindAddr == address {
					fmt.Fprintf(o.Out, "  This PC holds the public address itself: let its firewall in only TCP and UDP %d and TCP 443.\n", info.GatewayPort)
				}
				fmt.Fprintf(o.Out, "  Players start Start-Client.exe %s.\n", address)
			} else {
				fmt.Fprintf(o.Out, "  Players start Start-Client.exe %s --tunnel on.\n", address)
			}
			fmt.Fprintf(o.Out, "  The first time, they are shown the server's tunnel key fingerprint: it must be %s.\n", info.GatewayFingerprint)
			if !public && info.CAFingerprint != "" && !net.ParseIP(address).IsLoopback() {
				fmt.Fprintf(o.Out, "  (Without the tunnel they are shown the certificate fingerprint %s instead.)\n", info.CAFingerprint)
			}
		} else if net.ParseIP(address).IsLoopback() {
			fmt.Fprintln(o.Out, "  Now double-click Start-Client.exe to play.")
		} else {
			fmt.Fprintf(o.Out, "  Players start Start-Client.exe %s (you too, on this PC: just Start-Client.exe).\n", address)
			fmt.Fprintf(o.Out, "  The first time, they are shown the server's fingerprint: it must be %s.\n", info.CAFingerprint)
			if public {
				fmt.Fprintln(o.Out, "  If Windows asks whether the server may use the network, allow it, and forward the ports listed")
				fmt.Fprintln(o.Out, "  above on your router. Your use is at your own risk.")
			} else {
				fmt.Fprintln(o.Out, "  If Windows asks whether the server may use the network, allow it for private networks.")
			}
		}
		if publicP {
			fmt.Fprintln(o.Out, "  The public profile: players only through the tunnel, the limits on (connections, logins, games, packets).")
		}
		fmt.Fprintf(o.Out, "  %s\n", loginHint(cfg["signup"]))
		if info.WebURL != "" {
			fmt.Fprintf(o.Out, "  The website: %s (registration, password change, account deletion, status).\n", info.WebURL)
			fmt.Fprintf(o.Out, "  The admin page: %sadmin (for accounts made admin with Start-Server.exe --make-admin <login name>).\n", info.WebURL)
			if info.WebFingerprint != "" {
				fmt.Fprintf(o.Out, "  Its certificate is self-signed: browsers warn once; its SHA-256 fingerprint is\n    %s\n", info.WebFingerprint)
			} else if public {
				fmt.Fprintln(o.Out, "  Its certificate comes from Let's Encrypt (it checks the domain on TCP 443, which your router forwards).")
			}
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
// 0 = none), web_domain (a DNS name: Let's Encrypt; empty: self-signed), web_email.
type Website struct {
	Port          int
	Domain, Email string
}

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
	if w.Domain != "" && w.Port == 0 {
		return w, fmt.Errorf("web_domain = %s needs a web_port (443)", w.Domain)
	}
	return w, nil
}

// Flags are dgserver's flags for the website (before server_flags, which may override them).
func (w Website) Flags() []string {
	f := []string{"--web-port", strconv.Itoa(w.Port)}
	if w.Domain != "" {
		f = append(f, "--web-domain", w.Domain)
		if w.Email != "" {
			f = append(f, "--web-email", w.Email)
		}
	}
	return f
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
// --account-status, --list-invites, --revoke-invite and --make-invite's options) out of Start-Server's arguments, with
// their values. A non-empty acct means: run them on the setup folder's database (dgserver --data) and exit.
func SplitAccountArgs(args []string) (own, acct []string) {
	isAccount := map[string]bool{}
	for _, f := range dgserver.AccountCommandFlags {
		isAccount[f] = true
	}
	boolFlags := map[string]bool{"make-invite": true, "list-invites": true}
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
		if !strings.HasPrefix(name, "invite-") {
			command = true
		}
	}
	if !command { // --invite-uses alone isn't a command: let the flag parser complain
		return args, nil
	}
	return own, acct
}

// ServerFlags are dgserver's arguments for a setup folder (start_server.ps1's: the host copy, a host per game,
// headless; plus the certificates, data and logs of the folder and --serve-ca for players on other machines).
func ServerFlags(l setup.Layout, address string, shaperSelect bool, extra []string) []string {
	f := []string{"--bind", address, "--public-host", address, "--certs", l.Certs(address), "--data", l.Data(),
		"--log", l.Logs(), "--gms-dir", l.HostCopy(), "--gms-args=-multipleInstances", "--gms-data",
		filepath.Join(l.Logs(), "hosts"), "--gms-control-bind", ControlBind(address), "--serve-ca"}
	if shaperSelect {
		f = append(f, "--shaper-select")
	}
	return append(f, extra...)
}

// ControlBind is where the match hosts' control port binds: the server's own address when it is a loopback one (so a
// server on another loopback address, e.g. a test on 127.0.9.1, keeps off 127.0.0.1:10040), else 127.0.0.1 (a LAN
// address there would expose it, which dgserver allows only with agents, OPERATORS.md).
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
			return "Download Dawngate.zip from https://archive.org/details/Dawngate, put it next to Start-Server.exe " +
				"and start Start-Server.exe again."
		}
		if strings.Contains(l.Detail, "disk space") || l.Step == "disk space" {
			return "Free some disk space (or set folder = to another drive in " + ServerConfigName + "), then start again."
		}
	}
	return "Start Start-Server.exe again: the setup continues where it stopped. If the same step fails again, delete " +
		"the folder named in it (inside " + root + ") so that it is made anew, and report the message if that doesn't help."
}
