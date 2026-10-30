// Command mapsetup makes (or checks) a game copy's map files with the release setup's own code (setup.MapSteps): the
// archive of MM_Map_v3 (zz_MM_Map_v3.snap), the archive of every map in a maps folder (zz_<name>.snap, compiled from
// its package), the bot profiles' archive with those maps' catalog entries (zz_BotProfiles.snap) and, for a player
// copy, the create dialog with its map list (zz_MatchOptions.snap). The developers' setups run it for their copies, so
// they hold the same bytes as a release install.
//
// Usage (from server\):
//
//	go run ./tools/mapsetup [--check] [--maps <dir>] [--client] <copy>
//	go run ./tools/mapsetup --server-list <address> [--maps <dir>] <copy>
//
// <copy> is the folder with game\ (e.g. client\run_854\Dawngate); --maps the maps folder (the repository's maps\, which holds only the packages copied in);
// --client for a player copy (the dialog), else a match-host copy. --server-list only rewrites a player copy's create
// dialog list for the server at <address> played without the tunnel (its /dawngate/maps.txt and
// /dawngate/mapversions.txt on the HTTP port): the copy's maps that the server offers, without one the copy has in
// another version than the server's, as Start-Client.exe's front does at a login through the tunnel. A checkout's
// Start-Client.cmd runs it before the game starts, so the dialog lists only the maps the local server offers.
// Exit 0: current or written; 1: not current (--check) or a step failed; 2: wrong arguments.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"dawngate/server/internal/launcher"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/mapc"
)

func main() {
	check := flag.Bool("check", false, "only report; write nothing")
	maps := flag.String("maps", "", "the maps folder (map packages, a sub-folder with map.json each)")
	client := flag.Bool("client", false, "a player copy: also the create dialog's map list")
	serverList := flag.String("server-list", "", "only rewrite a player copy's create dialog list for the server at this address (no tunnel)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: mapsetup [--check] [--maps <dir>] [--client] <copy with game\\>")
		fmt.Fprintln(os.Stderr, "       mapsetup --server-list <address> [--maps <dir>] <copy with game\\>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if *serverList != "" {
		var folder []mapc.Package
		if *maps != "" {
			folder, _ = mapc.ReadFolder(*maps)
		}
		line, err := launcher.WriteServerMapList(flag.Arg(0), setup.DialogMaps(folder), *serverList, launcher.HTTPPort, 5*time.Second)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mapsetup: the create dialog's maps:", err)
			os.Exit(1)
		}
		fmt.Println(line)
		return
	}
	s := &setup.Setup{Out: os.Stdout, Check: *check, Maps: *maps}
	if err := s.MapSteps(flag.Arg(0), *client); err != nil {
		os.Exit(1)
	}
}
