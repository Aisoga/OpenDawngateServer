package launcher

import (
	"io"
	"runtime"
	"strings"

	"dawngate/server/internal/setup"
)

// Start-Server on Linux: the match hosts (Dawngate.exe with dgsim.dll, a 32-bit Windows program) run under Wine
// (dgserver --gms-runner), and the release has no Start-Client (the game is a Windows program), so a Linux server is a
// dedicated one: only the match-host copy is set up. What differs from Windows:
//   - the setup checks Wine with its 32-bit part and makes the hosts' Wine prefix, Dawngate-Data/wine (setup.Wine), or
//     uses WINEPREFIX from the environment; without Wine it says what to install and starts nothing;
//   - the hosts listen on 127.0.0.1 (serverHostsAddress), the one address Wine binds a UDP socket to exactly: asked
//     for a loopback alias such as 127.0.1.10 a host listens on 0.0.0.0 (every interface; its replies leave from
//     127.0.0.1 and the gateway's flow drops them), asked for an interface's own address on 0.0.0.0 tied to that
//     interface (gmshost's TestWineHostBind measures it);
//   - the setup folder is made private to the operator (0700): it holds the accounts, the keys and the logs;
//   - the texts name start-server instead of Start-Server.exe, and Linux's tools.

// hostsUnderWine says that this Start-Server runs its hosts under Wine (every OS but Windows). The tests of the
// Windows behaviour turn it off elsewhere (main_other_test.go).
var hostsUnderWine = runtime.GOOS != "windows"

// prepareWine is the Wine step (setup.Wine; the tests replace it).
var prepareWine = func(s *setup.Setup, o setup.WineOptions) (setup.WineEnv, error) { return s.Wine(o) }

// serverHostsAddress is where Start-Server's match hosts listen with the tunnel: HostsAddress (a loopback address
// of their own beside a Start-Client on the same PC), but 127.0.0.1 under Wine, the one loopback address Wine binds
// exactly; a Linux server runs no Start-Client beside it.
func serverHostsAddress(bind string) string {
	if hostsUnderWine {
		return setup.Local
	}
	return HostsAddress(bind)
}

// linuxText rewords Start-Server's console texts for Linux.
var linuxText = strings.NewReplacer(
	"Start-Server.exe", "start-server",
	"Start-Server window", "start-server",
	"dgserver.exe", "dgserver",
	"ipconfig shows it as IPv4 Address", "ip -4 addr shows it",
	"release zip", "release archive",
	"Stop the server with Ctrl+C or by closing this window (it ends running matches).",
	"Stop the server with Ctrl+C or SIGTERM (systemctl stop, docker stop); it ends running matches.",
	"only this PC: you play here with Start-Client.exe", "only this machine: a test, nobody else reaches it",
	"(this PC only)", "(this machine only)",
)

// linuxWriter writes through linuxText (each Fprint call is one Write, so a text is never split).
type linuxWriter struct{ w io.Writer }

func (l linuxWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(l.w, linuxText.Replace(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// wineConfig is the config file's key for the Wine program, only in the Linux text.
const wineConfig = `; Linux: the Wine program that runs the match hosts (the game is a 32-bit Windows program), with its 32-bit part.
; Empty: wine from the PATH. The hosts share the Wine prefix Dawngate-Data/wine (or WINEPREFIX from the environment).
wine =

`

// serverConfigText is the config file Start-Server writes on its first start: ServerConfigText, on Linux with its
// paths and program names and the wine key.
func serverConfigText() string {
	if !hostsUnderWine {
		return ServerConfigText
	}
	t := strings.NewReplacer(
		`D:\Games\Dawngate-Data`, "/srv/dawngate/Dawngate-Data",
		`Dawngate-Data\data\site`, "Dawngate-Data/data/site",
		"this PC only: you play here with Start-Client.exe (the default)", "this machine only: a test, nobody else reaches it (the default)",
	).Replace(linuxText.Replace(ServerConfigText))
	return strings.Replace(t, "; More server options", wineConfig+"; More server options", 1)
}
