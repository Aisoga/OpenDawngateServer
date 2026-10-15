// Command start-server is Start-Server.exe of the release: double-click it next
// to Dawngate.zip. Its first start writes Start-Server.ini (the options), finds Dawngate.zip (next to it, in
// Downloads, or asks for it) and sets up the folder Dawngate-Data next to it: the match-host copy of the game,
// patched, certificates and the tunnel's key made for this install, dgsim.dll from the release's host folder, the
// player content (Start-Client.exe makes the player copy). Every start checks (and repairs) that setup, then runs
// the server (dgserver, in this process) behind its encrypted tunnel, the players' only port, until Ctrl+C or the
// window is closed. Then double-click Start-Client.exe.
//
//	Start-Server.exe [--config <file>] [--setup-only] [--check] [--non-interactive] [--zip <Dawngate.zip>]
//	    [--host-files <dir>] [--seconds N]
//
// Build: go build -o Start-Server.exe ./cmd/start-server (re/scripts/make_release.ps1 builds the release).
package main

import (
	"os"
	"slices"

	"dawngate/server/internal/launcher"
)

func main() {
	args := os.Args[1:]
	os.Exit(launcher.RunServer(args, launcher.StdIO(slices.Contains(args, "--non-interactive") ||
		slices.Contains(args, "-non-interactive"))))
}
