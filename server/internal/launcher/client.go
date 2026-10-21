package launcher

import (
	"bufio"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/certs"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/update"
	"dawngate/server/internal/version"
)

// ClientConfigName is Start-Client.exe's config file, next to it (written only when a player saves a server address).
const ClientConfigName = "Start-Client.ini"

// ClientConfigText is the head of Start-Client.ini when Start-Client.exe writes it.
const ClientConfigText = `; Start-Client.ini: the options of Start-Client.exe. Lines that start with ; are comments.

; The server to play on: its address, e.g. 192.168.1.20 (with the server picker below it goes into a new list).
; Empty: the server picker's list, with "This PC" first (the server on this PC: Start-Client runs it itself, or uses
; Start-Server.exe's when that runs on the same folder). "Start-Client.exe <address>" adds and preselects a server.
server =

; The folder for the game copy (1.5 GB): relative to this program's folder, or a full path. Empty: Start-Server.ini's
; folder, else Dawngate-Data.
folder =

; The encrypted tunnel to the server: auto (the default) and on use it for every server; off never (the developers'
; option, for a server whose Start-Server.ini has tunnel = off: the game then plays on that one server directly).
tunnel = auto

; The server's tunnel port (TCP and UDP), when its operator changed it. Empty: 42128.
gateway_port =

; The tunnel key of the server above (64 hex digits), when its operator handed it out: the first login to that server
; then trusts it without stopping at the key's fingerprint. A key that differs from one you trusted before is not taken
; (the login screen shows KEY CHANGED). Empty: compare the fingerprint at the first login.
gateway_pin =

; The server picker on the game's login screen: SERVER above the login lists your servers (the list is
; Dawngate-Data\servers\bookmarks.json), and you add or remove servers there. auto (the default) and on use it
; whenever the tunnel is used; off never: the game then plays on one server.
; "Start-Client.exe <address>" adds a server to the list and preselects it. At the first login to a server the list
; shows its key's fingerprint and the login stops; compare it with the one its operator gives you, then log in again
; to trust it.
picker = auto

; This PC without passwords: on (the default) lets any login name in on the server Start-Client runs for This PC, with
; any password (none is checked or stored; only this PC reaches that server). off checks passwords there as any server
; does. Not used while this folder's Start-Server.exe serves This PC: it always checks passwords.
local_trust = on

; Updates: on (the default) asks github.com once at each start whether a newer release is out and, if so, whether to
; install it (a server plays only with Start-Client of its own release). off never asks.
update_check = on

; Discord status: on (the default) shows your friends on Discord what you do in Dawngate (the menu, the queue, shaper
; select, a match and its time, your party's size; no names) while the game runs. It needs the Discord app on this PC.
; off never.
discord = on

; Join from Discord: on (the default) lets your Discord friends click Join on your profile while you are in a party,
; a practice group or a custom lobby that has room and no password; Start-Client then sends them the game's invite in
; the leader's name. Your own Join clicks arrive the same way: Discord starts Start-Client when it doesn't run (it is
; registered for that at each start). Needs discord = on. off never.
discord_join = on

; The Discord application the status is shown as (its id, a number). Empty: the one built into this release.
discord_app_id =
`

// oldClientComments are the tunnel and picker keys' comments that earlier releases wrote into Start-Client.ini (when
// tunnel = auto meant the tunnel only for a public address); a start replaces them with ClientConfigText's.
var oldClientComments = map[string][][]string{
	"tunnel": {{"; The encrypted tunnel to the server: auto (the default) uses it for a server on a public internet address, which",
		"; needs it; on uses it for any server that runs it (Start-Server.ini tunnel = on); off never."}},
	"picker": {{"; The server picker on the game's login screen: SERVER above the login lists your servers (the list is",
		"; Dawngate-Data\\servers\\bookmarks.json), and you add or remove servers there. auto (the default) uses it whenever",
		"; the tunnel is used: for a server on the internet, with tunnel = on, or when the list has servers and no server",
		"; is set above. on always (it needs the tunnel), off never: the game then plays on one server.",
		"; \"Start-Client.exe <address>\" adds a server to the list and preselects it. A server without the tunnel (on a home",
		"; network) can't be picked: Start-Client then plays on it directly."}},
}

var clientKeys = []string{"server", "folder", "zip", "tunnel", "gateway_port", "picker", "local_trust", "update_check",
	DiscordKey, DiscordAppIDKey, DiscordJoinKey,
	GatewayPinKey}

// TestParkEnv set to 1 is Start-Client's --test-park (the release's test hook; never needed by a player).
const TestParkEnv = "DAWNGATE_TEST_PARK"

// RunClient is Start-Client.exe (cmd/start-client): it finds the server or servers to play on, makes sure the player
// copy is set up and patched for the tunnel address (setup.Client; with tunnel = off for one server, with that
// server's CA, trusted on first use), starts the front (the login screen's server list; or one tunnel with
// picker = off), then starts the game and waits until it exits. It returns the exit code.
func RunClient(args []string, o IO) int {
	fs := flag.NewFlagSet("Start-Client", flag.ContinueOnError)
	fs.SetOutput(o.Out)
	serverFlag := fs.String("server", "", "the server: its address, or the tag or name of a server in the picker's list (also the first "+
		"argument; default: Start-Client.ini, then the picker's list, then this PC's Start-Server)")
	caFile := fs.String("ca", "", "--tunnel off: the server's ca_modulus.bin, when its operator handed it out (instead of fetching it)")
	trust := fs.Bool("trust", false, "trust a new server key without asking, with --picker off or --tunnel off (scripts and tests; players confirm the fingerprint)")
	setupOnly := fs.Bool("setup-only", false, "set up the player copy for the server, then exit without starting the game")
	check := fs.Bool("check", false, "only report what is missing or stale; change nothing")
	nonInteractive := fs.Bool("non-interactive", false, "never ask anything (also no update question); for scripts and tests")
	park := fs.Bool("test-park", os.Getenv(TestParkEnv) == "1", "test hook: keep the game window off the desktop and never activate it, as run_session.ps1 -Background does; also "+TestParkEnv+"=1")
	zipFlag := fs.String("zip", "", "Dawngate.zip, when the player copy must be made or repaired")
	gameSourceFlag := fs.String(GameSourceFlag, "", "where to download Dawngate.zip from when it isn't found (addresses separated by commas; "+
		"only these, without asking)")
	tunnelFlag := fs.String("tunnel", "", "the encrypted tunnel: auto or on (every server; the default), off (a server whose Start-Server.ini has tunnel = off)")
	gwPortFlag := fs.String("gateway-port", "", "the server's tunnel port (default 42128)")
	gwPinFlag := fs.String("gateway-pin", "", "the server's tunnel key (64 hex digits), when its operator handed it out (no question)")
	tunnelAddr := fs.String("tunnel-address", DefaultTunnelAddress, "the loopback address the tunnel listens on and the game connects to (tests)")
	pickerFlag := fs.String("picker", "", "the server picker on the login screen (the list in "+`Dawngate-Data\servers\`+BookmarksFile+
		"): auto (the default) whenever the tunnel is used, on always (needs the tunnel), off plays on one server")
	lane := fs.String("test-lane", "", `test hook: one letter X; the game copy gets per-user folders of its own (%APPDATA%\DawngateDataX, `+
		`Documents\DawngateX) and the game runs with -multipleInstances, beside another client on this PC`)
	showVersion := fs.Bool("version", false, "print this release's version and exit (it plays only on servers of the same version)")
	localTrustFlag := fs.String("local-trust", "", "This PC without passwords: on (the default) lets any login name in on the server Start-Client runs for This PC, "+
		"with any password; off checks passwords there (Start-Client.ini local_trust)")
	discordFlag := fs.String("discord", "", "your Discord status while the game runs: on (the default) or off (Start-Client.ini discord)")
	discordJoinFlag := fs.String("discord-join", "", "Join from Discord: on (the default) or off (Start-Client.ini discord_join)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(o.Out, version.String())
		return 0
	}
	if len(positional) > 1 {
		fmt.Fprintf(o.Out, "Start-Client takes one server address, not %q\n", positional)
		return 2
	}
	*lane = strings.ToUpper(*lane)
	if *lane != "" && (len(*lane) != 1 || (*lane)[0] < 'A' || (*lane)[0] > 'Z') {
		return o.fail("--test-lane %s: one letter", *lane)
	}
	in := bufio.NewReader(o.In)
	fmt.Fprintf(o.Out, "OpenDawngateServer: Start-Client, version %s\n", version.String())
	if *lane == "" && !*check && clientRunning() {
		// a second Start-Client (e.g. Discord starting it for a Join while the game runs) changes nothing: the running
		// one keeps the server list in memory and would write over what this one wrote
		return o.fail("A Dawngate client is already running (only one can run at a time): close it, then start " +
			"Start-Client.exe again. A Join from Discord goes to the running game while its Discord status is on.")
	}

	clientIni := filepath.Join(o.ExeDir, ClientConfigName)
	ccfg, err := ReadConfig(clientIni)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return o.fail("%v", err)
	}
	for _, k := range ccfg.Unknown(clientKeys...) {
		fmt.Fprintf(o.Out, "  note: %s: unknown option %q (ignored)\n", clientIni, k)
	}
	if ccfg != nil && !*check { // an earlier release's file: tunnel = auto meant the tunnel only for a public address
		for _, k := range []string{"tunnel", "picker"} {
			if changed, err := migrateComment(clientIni, oldClientComments[k], commentOf(ClientConfigText, k)); err != nil {
				fmt.Fprintf(o.Out, "  note: %s can't be updated: %v\n", clientIni, err)
				break
			} else if changed && k == "tunnel" {
				fmt.Fprintf(o.Out, "  note: %s: the encrypted tunnel is now used for every server (tunnel = auto; the comments updated)\n", clientIni)
			}
		}
	}
	scfg, _ := ReadConfig(filepath.Join(o.ExeDir, ServerConfigName)) // this PC's server, when there is one
	l := setup.Layout{Root: Resolve(o.ExeDir, clientFolder(ccfg, scfg))}
	for _, f := range update.CleanOld(o.ExeDir) { // what an update left: the old program, cut-short temporary files
		fmt.Fprintf(o.Out, "  update   %s deleted (left by the update)\n", f)
	}
	if code, ran, err := offerUpdate(o, in, updateOptions{args: args, check: *check, setupOnly: *setupOnly,
		nonInteractive: *nonInteractive, lane: *lane, setting: ccfg["update_check"], root: l.Root}); err != nil {
		return o.fail("%v", err)
	} else if ran {
		return code
	}
	pickerSet, err := pickerSetting(firstNonEmpty(*pickerFlag, ccfg["picker"]))
	if err != nil {
		return o.fail("%v", err)
	}
	localTrust, err := localTrustSetting(firstNonEmpty(*localTrustFlag, ccfg["local_trust"]))
	if err != nil {
		return o.fail("%v", err)
	}
	// the Discord status: never the player's from a test run (only the test hook's fake app)
	dc, err := discordSetting(*discordFlag, ccfg, *lane != "" || *park || *nonInteractive, discordEnv)
	if err != nil {
		return o.fail("%v", err)
	}
	joinWhy := ""
	if dc.join, joinWhy, err = discordJoinSetting(*discordJoinFlag, ccfg, dc); err != nil {
		return o.fail("%v", err)
	}
	mode, err := tunnelMode(firstNonEmpty(*tunnelFlag, ccfg["tunnel"]))
	if err != nil {
		return o.fail("%v", err)
	}
	if mode == TunnelAuto && scfg != nil {
		// this folder's Start-Server runs without the tunnel (the developers' tunnel = off): its player copy follows it
		if sm, err := tunnelMode(scfg["tunnel"]); err == nil && sm == TunnelOff {
			mode = TunnelOff
			fmt.Fprintf(o.Out, "  tunnel   off (%s has tunnel = off)\n", ServerConfigName)
		}
	}
	gwPort, err := gatewayPort(firstNonEmpty(*gwPortFlag, ccfg["gateway_port"]))
	if err != nil {
		return o.fail("%v", err)
	}
	if err := checkIniGatewayPin(ccfg); err != nil {
		return o.fail("%v", err)
	}
	named := *serverFlag
	if named == "" && len(positional) > 0 {
		named = positional[0]
	}
	choice, err := chooseServer(o, in, clientIni, l, ccfg, scfg, named, pickerSet, mode, gwPort)
	if err != nil {
		return o.fail("%v", err)
	}
	picker, server := choice.picker, choice.server
	if _, err := setup.ClientOptions(server); err != nil && !picker {
		return o.fail("The server address %q: %v", server, err)
	}
	tunnelled := picker || tunnelFor(mode, server) // in picker mode every server of the list is reached through its tunnel
	if IsPublicAddress(server) {
		ClientRiskNotice(o.Out, server, tunnelled)
	}
	local := !remoteServer(server)
	if !picker {
		fmt.Fprintf(o.Out, "  server   %s\n", server)
	}
	fmt.Fprintf(o.Out, "  folder   %s\n", l.Root)

	var ca []byte
	patchFor := server // the address the player copy is patched for
	var gw string
	var pin tunnel.Pin
	var tunnelTLS *tls.Config
	var bookmarks *Bookmarks
	var pc *thisPC // the list's This PC (picker mode)
	preselect := ""
	if tunnelled {
		// the game talks to the tunnel on this PC; the tunnel's key is pinned (confirmed once), the redirector's
		// certificates are this PC's own (no server CA needed)
		if ip := net.ParseIP(*tunnelAddr).To4(); ip == nil || !ip.IsLoopback() {
			return o.fail("--tunnel-address %s: a loopback address such as 127.0.0.1", *tunnelAddr)
		}
		patchFor = *tunnelAddr
		ownAddrs, ownPin, ownKey := ownServerKey(scfg, o.ExeDir, l) // this PC's Start-Server in the same folder
		if picker {
			bookmarks = choice.bm
			var ini Config // This PC takes the game options of this folder's Start-Server.ini
			if sameFolder(scfg, o.ExeDir, l) {
				ini = scfg
			}
			pc = newThisPC(o, l, patchFor, filepath.Join(o.ExeDir, "host"), ini, bookmarks, *check, localTrust)
			if preselect, err = pickerBookmarks(o, l, choice, *gwPinFlag, *check, pc); err != nil {
				return o.fail("%v", err)
			}
			if ownKey {
				if err := pinOwnServer(o, l, bookmarks, ownAddrs, ownPin, *check); err != nil {
					return o.fail("%v", err)
				}
			}
		} else {
			gw = gatewayAddress(server, gwPort, scfg)
			fmt.Fprintf(o.Out, "  tunnel   %s (the game connects to %s on this PC)\n", gw, patchFor)
			pinFlag, pinFrom := *gwPinFlag, "--gateway-pin"
			if ownKey && pinFlag == "" && len(ownAddrs) > 0 && ownAddrs[0] == server {
				pinFlag = ownPin.String() // this PC's own server: its key is in the folder
			}
			if pinFlag == "" && strings.EqualFold(server, ccfg["server"]) { // the operator's key in Start-Client.ini
				pinFlag, pinFrom = iniGatewayPin(o, l, ccfg, server), iniPinSource
			}
			if !*check {
				if pin, err = trustedGatewayPin(o, in, l, server, gw, pinFlag, pinFrom, *trust); err != nil {
					return o.fail("%v", err)
				}
			}
		}
		if ca, tunnelTLS, err = tunnelCertSet(l, patchFor, *check); err != nil {
			return o.fail("%v", err)
		}
	} else {
		// the server's CA: this PC's own server's, else fetched (or handed out) and trusted on first use
		ownCA := filepath.Join(l.Certs(server), certs.CAModulusFile)
		cert, answering := ServerCert(server, RedirectorPort, 5*time.Second) // the redirector, as the game reaches it
		if b, err := os.ReadFile(ownCA); err == nil {
			ca = b
		} else if local {
			return o.fail("No server is set up on this PC for %s: start Start-Server.exe first (its first start sets "+
				"everything up), then Start-Client.exe.", server)
		}
		if answering != nil && !*setupOnly && !*check {
			if local || ca != nil {
				return o.fail("No server is running on %s: double-click Start-Server.exe first and wait until it says "+
					"\"The server runs\", then start Start-Client.exe again.", server)
			}
			return o.fail("No server answers at %s (port %d: %v).\nWithout the tunnel (tunnel = off) Start-Client needs a "+
				"Start-Server.exe with tunnel = off and address = %s in its Start-Server.ini, whose firewall lets the network "+
				"in. A server with the tunnel (the default) is reached without --tunnel off.", server, RedirectorPort, answering, server)
		}
		if ca == nil {
			if ca, err = remoteCA(o, in, l, server, *caFile, *trust, answering == nil); err != nil {
				return o.fail("%v", err)
			}
		}
		if answering == nil && !*setupOnly && !*check {
			// the server's version must be this Start-Client's: the game talks to it directly, so the check is now
			v, verr := FetchVersion(server, HTTPPort, 5*time.Second)
			if err := versionMismatch(server, v, verr); err != nil {
				return o.fail("%v", err)
			}
		}
		if answering == nil {
			if err := certs.VerifyServerCert(cert, ca, server); err != nil {
				return o.fail("The server at %s doesn't present a certificate the game would accept: %v\n"+
					"The game would fail with \"Unable to establish connection\". If the server was set up anew, delete "+
					"%s and start Start-Client.exe again to confirm its new fingerprint.", server, err, l.TrustedCA(server))
			}
		}
	}

	fmt.Fprintln(o.Out, "\nChecking the game copy (the first start makes it: about half a minute and 1.5 GB):")
	zipConf := firstNonEmpty(*zipFlag, ccfg["zip"], scfg["zip"])
	zipUsed := "" // the archive this start found (This PC's setup takes it again)
	sources, err := newGameSources(*gameSourceFlag, firstNonEmpty(ccfg[GameSourcesKey], scfg[GameSourcesKey]), gameCopiesSpace)
	if err != nil {
		return o.fail("%v", err)
	}
	sources.noDownloadsDir = *lane != ""
	findZip := zipFinder(o, in, zipConf, sources)
	if pc != nil {
		quiet := o
		quiet.Interactive = false // This PC is set up while the game runs: nothing is asked or downloaded then
		later := zipFinder(quiet, nil, zipConf, gameSources{noDownloadsDir: sources.noDownloadsDir})
		pc.findZip = func() (string, error) {
			if zipUsed != "" {
				return zipUsed, nil
			}
			p, err := later()
			if err != nil {
				return "", fmt.Errorf("%v; then log in to This PC again", err)
			}
			return p, nil
		}
	}
	s := &setup.Setup{Layout: l, Out: o.Out, Live: o.Live, Check: *check, FindZip: func() (string, error) {
		p, err := findZip()
		if err == nil {
			zipUsed = p
		}
		return p, err
	},
		Manifest: manifest, WebDomain: ownServerDomain(scfg, o.ExeDir, l.Root, server), DataSuffix: *lane, Picker: picker}
	var release func()
	if !*check {
		if release, err = setup.Lock(l.Root); err != nil {
			return o.fail("%v", err)
		}
	}
	if *lane == "" && clientRunning() && !*check { // a test lane's game runs beside the others (-multipleInstances)
		if release != nil {
			release()
		}
		return o.fail("A Dawngate client is already running (only one can run at a time): close it, then start " +
			"Start-Client.exe again.")
	}
	err = s.Client(patchFor, ca)
	if release != nil {
		release()
	}
	if *check {
		if err != nil {
			fmt.Fprintf(o.Out, "\n%d item(s) not current.\n", len(s.Bad()))
			return 1
		}
		fmt.Fprintln(o.Out, "\nThe game copy is ready.")
		return 0
	}
	if err != nil {
		return o.fail("The game copy is not ready:\n%s\n\n%s", lines(s.Bad()), setupAdvice(s.Bad(), l.Root))
	}
	if *setupOnly {
		return 0
	}
	if !picker {
		if err := RemoveServerList(l.ClientCopy()); err != nil {
			return o.fail("The server list of an earlier start can't be removed: %v (is the game running?)", err)
		}
	}
	var tracker *presenceTracker // the front's view of the game for the Discord status
	var joinFront *Front         // the front that takes a friend's Join from Discord
	presenceLog := func(format string, a ...any) { fmt.Fprintf(o.Out, "  "+format+"\n", a...) }
	if picker {
		if dc.on {
			tracker = newPresenceTracker(nil)
		}
		stop, flog, front, err := startFront(o, l, bookmarks, patchFor, tunnelTLS, preselect, pc, tracker, dc)
		if err != nil {
			return o.fail("The server picker didn't start: %v", err)
		}
		defer stop()
		joinFront = front
		if flog != nil {
			presenceLog = func(format string, a ...any) { flog.Logf(frontWho, format, a...) }
		}
	} else if tunnelled {
		_, stop, err := startTunnel(o, l, gw, pin, patchFor, tunnelTLS)
		if err != nil {
			return o.fail("The tunnel didn't start: %v", err)
		}
		defer stop()
	}
	gameArgs := ""
	if *lane != "" {
		gameArgs = "-multipleInstances"
	}
	fmt.Fprintf(o.Out, "  discord  %s\n", dc.why)
	var onJoin func(string)
	if dc.join && tracker != nil && joinFront != nil {
		fmt.Fprintf(o.Out, "  discord  %s\n", joinWhy)
		onJoin = joinFront.DiscordJoinSecret
		registerLaunch(o, dc)
	} else if joinWhy != "" && dc.join {
		fmt.Fprintf(o.Out, "  discord  Join not used (it needs the server picker)\n")
	} else if joinWhy != "" {
		fmt.Fprintf(o.Out, "  discord  %s\n", joinWhy)
	}
	var presence func(pid int) func()
	if dc.on {
		presence = func(pid int) func() { return startPresence(dc, pid, tracker, presenceLog, onJoin) }
	}
	return play(o, l, *park, gameArgs, presence)
}

// ownServerDomain is the web domain the player copy's login links use: Start-Server.ini's web_domain when the
// copy is this PC's own server's (the same folder) and the game plays on that server, so Start-Server and Start-Client
// make the same added locale package and don't rewrite each other's; "" otherwise. A player copy for a server on
// another machine keeps the links at http://<server>:18080/dawngate/<page> (sitelinks.Base without a domain), which
// that server's HTTP port redirects to its website (through the tunnel too), so a player
// needs to know no domain and an operator who adds or changes one re-patches nobody's copy.
func ownServerDomain(scfg Config, exeDir, root, server string) string {
	if scfg == nil {
		return ""
	}
	folder := scfg["folder"]
	if folder == "" {
		folder = DefaultFolder
	}
	address := scfg["address"]
	if address == "" {
		address = setup.Local
	}
	if !strings.EqualFold(filepath.Clean(Resolve(exeDir, folder)), filepath.Clean(root)) || address != server {
		return ""
	}
	w, err := ReadWebsite(scfg)
	if err != nil || w.Port == 0 {
		return ""
	}
	return w.Domain
}

// remoteServer says whether a server address is another machine's (its CA is fetched and trusted on first use) rather
// than this PC's own (a loopback address: the CA is this PC's setup's). The test of the remote path sets it to use a
// server on a loopback address.
var remoteServer = func(address string) bool { return !net.ParseIP(address).IsLoopback() }

// clientRunning is ClientRunning (the game's Global\Dawngate mutex); the tests replace it, since a test lane's client on
// this PC holds that mutex too.
var clientRunning = ClientRunning

// parseInterspersed parses flags before and after the arguments that aren't flags (Start-Client.exe <address>
// --ca <file>, as the messages tell players to type; the flag package alone stops at the address and leaves every
// later flag unread). "--" ends the flags.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		if len(args) > len(rest) && args[len(args)-len(rest)-1] == "--" {
			return append(positional, rest...), nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// localTrustSetting reads local_trust = (--local-trust): on (the default, also "") or off.
func localTrustSetting(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "on", "yes", "true", "1":
		return true, nil
	case "off", "no", "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("local_trust = %s: use on or off", v)
}

// clientFolder is the setup folder: Start-Client.ini's folder, else Start-Server.ini's, else Dawngate-Data.
func clientFolder(ccfg, scfg Config) string {
	return firstNonEmpty(ccfg["folder"], scfg["folder"], DefaultFolder)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func setClientKey(path, key, value string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(ClientConfigText, "\n", "\r\n")), 0o644); err != nil {
			return err
		}
	}
	return SetKey(path, key, value)
}

// remoteCA is the CA of a server on another machine: the handed-out file (--ca) or the one it serves, trusted on
// first use (TrustCA; the player compares the fingerprint with the operator's Start-Server window).
func remoteCA(o IO, in *bufio.Reader, l setup.Layout, server, caFile string, trust, answering bool) ([]byte, error) {
	var served []byte
	var err error
	switch {
	case caFile != "":
		if served, err = os.ReadFile(caFile); err != nil {
			return nil, err
		}
		if len(served) != 128 {
			return nil, fmt.Errorf("%s has %d bytes: a ca_modulus.bin has 128", caFile, len(served))
		}
		trust = true // the operator handed it out
	case answering:
		if served, err = FetchCA(server, HTTPPort, 5*time.Second); err != nil {
			if known, kerr := os.ReadFile(l.TrustedCA(server)); kerr == nil {
				fmt.Fprintf(o.Out, "  note: %v; using the CA trusted before (%s)\n", err, certs.Fingerprint(known))
				return known, nil
			}
			return nil, fmt.Errorf("the server's certificate authority can't be fetched: %v\nAsk its operator for "+
				"the file ca_modulus.bin and start Start-Client.exe %s --ca <that file>", err, server)
		}
	default:
		if known, err := os.ReadFile(l.TrustedCA(server)); err == nil {
			return known, nil
		}
		return nil, fmt.Errorf("no server answers at %s and its CA isn't known yet: start Start-Client.exe while "+
			"the server runs, or pass --ca <its ca_modulus.bin>", server)
	}
	var ask func(string) (bool, error)
	if o.Interactive {
		ask = func(q string) (bool, error) {
			a, err := Prompt(in, o.Out, "\n  "+q)
			return strings.EqualFold(a, "y") || strings.EqualFold(a, "yes"), err
		}
	}
	t, err := TrustCA(server, served, TrustOptions{Store: l.TrustedCA(server), Accept: trust, Ask: ask})
	if err != nil {
		return nil, err
	}
	switch t {
	case TrustNew:
		fmt.Fprintf(o.Out, "  trusting the server's CA %s from now on (%s)\n", certs.Fingerprint(served), l.TrustedCA(server))
	case TrustChanged:
		fmt.Fprintf(o.Out, "  trusting the server's new CA %s\n", certs.Fingerprint(served))
	}
	return served, nil
}

// play starts the game and waits for it; Ctrl+C or closing the window closes the game. presence (nil: none) starts
// the Discord status for the game's process; the function it returns clears it when the game has ended.
func play(o IO, l setup.Layout, park bool, args string, presence func(pid int) func()) int {
	prev := ForegroundWindow() // the test hook gives the foreground back to it (Park)
	g, err := StartGame(l.ClientCopy(), StartOptions{Args: args, NoActivate: park})
	if err != nil {
		return o.fail("The game didn't start: %v", err)
	}
	defer g.Release()
	if presence != nil {
		defer presence(g.Pid)()
	}
	fmt.Fprintf(o.Out, "\nThe game starts (game pid %d; its window opens in a few seconds).\n", g.Pid)
	fmt.Fprintln(o.Out, "  Log in with your login name and password (with open sign-up the first login makes the account), then PLAY and PRACTICE DAWNGATE (README.txt).")
	fmt.Fprintln(o.Out, "  This window closes when the game ends; Ctrl+C here closes the game.")
	stop := make(chan struct{})
	defer close(stop)
	if park {
		fmt.Fprintln(o.Out, "  test hook: the game window is kept off the desktop, without a taskbar button, and out of the foreground (--test-park)")
		go g.Park(stop, prev, ParkEvents{ForegroundBack: func(n int, ok bool) {
			if n <= 20 {
				res := "given back to the window that had it"
				if !ok {
					res = "Windows refused to give it back"
				}
				fmt.Fprintf(o.Out, "  test hook: the game took the foreground (%d); %s (%s)\n", n, res, time.Now().Format("15:04:05.000"))
			}
		}, Lock: func(locked bool) {
			what := "the user is idle past ForegroundLockTimeout: foreground locked"
			if !locked {
				what = "the user is back: foreground lock released"
			}
			fmt.Fprintf(o.Out, "  test hook: %s (%s)\n", what, time.Now().Format("15:04:05.000"))
		}})
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	done := make(chan uint32, 1)
	go func() {
		code, _ := g.Wait()
		done <- code
	}()
	select {
	case code := <-done:
		fmt.Fprintf(o.Out, "The game exited (code %d).\n", int32(code))
		return 0
	case <-interrupt:
		fmt.Fprintln(o.Out, "Closing the game ...")
		g.Close()
		if !g.Exited(4 * time.Second) { // in a match the game ignores WM_CLOSE; a closed console allows about 5 s
			g.Kill()
		}
		return 0
	}
}
