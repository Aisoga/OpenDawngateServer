// Command start-client is Start-Client.exe of the release: it starts the game
// against the server of Start-Server.exe on this PC, or against a server on another machine of your network
// ("Start-Client.exe 192.168.1.20", or server = in Start-Client.ini). It makes and patches the player copy for that
// server first (Dawngate.zip is needed once), with the server's certificate authority: this PC's own, or the one a
// server on another machine serves, trusted once the player has compared its fingerprint with the operator's
// Start-Server window. It refuses when no server answers, holds the launcher's event while the game runs and waits
// until the game exits.
//
//	Start-Client.exe [<server address>] [--ca <ca_modulus.bin>] [--setup-only] [--check] [--non-interactive]
//	    [--trust] [--zip <Dawngate.zip>] [--test-park]
//
// --test-park (or DAWNGATE_TEST_PARK=1) is the release's test hook: the game is started without activation and its
// window kept off the desktop, as run_session.ps1 -Background keeps it; launch_monitor.ps1 -Attach then drives the
// login and the clicks with posted messages. A player never needs it.
package main

import (
	"os"
	"slices"

	"dawngate/server/internal/launcher"
)

func main() {
	args := os.Args[1:]
	os.Exit(launcher.RunClient(args, launcher.StdIO(slices.Contains(args, "--non-interactive") ||
		slices.Contains(args, "-non-interactive"))))
}
