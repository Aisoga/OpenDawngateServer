// Command start-client is Start-Client.exe of the release: it starts the game
// with the server list on the login screen: "This PC" first, the server on this PC (Start-Server.exe's when that
// runs on the same folder, else one Start-Client runs itself: set up and started at the first login to it, ended with
// the game), then servers on other machines ("Start-Client.exe 192.168.1.20" adds one), each reached through its
// encrypted tunnel. It makes and patches the player copy first (Dawngate.zip is needed once) for the tunnel on this
// PC, runs the front that answers the login screen and connects the login to the picked server, whose key is pinned:
// this PC's own server's from the shared folder, one handed out (--gateway-pin), or one the player confirms at the
// first login (the list shows its fingerprint, the second login trusts it). It holds the launcher's event while the
// game runs and waits until the game exits. With --tunnel off (a developers' option) it plays on one server directly,
// with that server's CA.
//
//	Start-Client.exe [<server address or tag>] [--gateway-pin <key>] [--setup-only] [--check] [--non-interactive]
//	    [--zip <Dawngate.zip>] [--tunnel off [--ca <ca_modulus.bin>] [--trust]] [--test-park]
//	Start-Client.exe --mapc <command> [<args>]
//
// --test-park (or DAWNGATE_TEST_PARK=1) is the release's test hook: the game is started without activation and its
// window kept off the desktop, as run_session.ps1 -Background keeps it; launch_monitor.ps1 -Attach then drives the
// login and the clicks with posted messages. A player never needs it.
//
// --mapc, as the first argument, runs the map compiler's command line instead of the game (internal/mapctool: the
// same code and output as the developers' tools/mapc; `--mapc version --json` names its interface's version): the
// way other tools of the release check, build and preview a map package. It touches no game copy but the one its
// arguments name.
package main

import (
	"os"
	"slices"

	"dawngate/server/internal/launcher"
	"dawngate/server/internal/mapctool"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "--mapc" || args[0] == "-mapc") {
		os.Exit(mapctool.Run(args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(launcher.RunClient(args, launcher.StdIO(slices.Contains(args, "--non-interactive") ||
		slices.Contains(args, "-non-interactive"))))
}
