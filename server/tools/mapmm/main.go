// Command mapmm makes (or checks) a game copy's added archive game\Data\zz_MM_Map_v3.snap, the second map
// MM_Map_v3's level files, with the release setup's own code (setup.MapMM): the developers' setups run it for their
// copies, so they hold the same bytes as a release install.
//
// Usage (from server\):
//
//	go run ./tools/mapmm [--check] <copy>
//
// <copy> is the folder with game\ (e.g. client\run_854\Dawngate).
// Exit 0: current or written; 1: not current (--check) or the step failed; 2: wrong arguments.
package main

import (
	"flag"
	"fmt"
	"os"

	"dawngate/server/internal/setup"
)

func main() {
	check := flag.Bool("check", false, "only report; write nothing")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: mapmm [--check] <copy with game\\>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	s := &setup.Setup{Out: os.Stdout, Check: *check}
	if err := s.MapMM("MM_Map_v3", flag.Arg(0)); err != nil {
		os.Exit(1)
	}
}
