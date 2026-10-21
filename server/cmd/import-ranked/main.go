// Command import-ranked counts the Leaderboard's PvP and custom games of a server's older versions (v0.8 and before)
// from its log files into its database: the same import as dgserver's and Start-Server's --import-ranked-logs, as a
// program of its own for a server whose version has no such command (it changes no schema: a server of the version
// that made the database opens it afterwards as before). Stop the server first.
//
//	import-ranked --data <data folder> [--apply [--force]] <logs folder or log file>...
//
// Without --apply it only prints what it finds (a dry run). Build it for Linux on any system (the SQLite driver is pure
// Go): CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o import-ranked ./cmd/import-ranked
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/rankedlogs"
	"dawngate/server/internal/store"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("import-ranked", flag.ContinueOnError)
	data := fs.String("data", "", "the server's data folder (the one with "+store.FileName+"; Start-Server: <folder>/data)")
	apply := fs.Bool("apply", false, "write the games into the database (once; again only with --force)")
	force := fs.Bool("force", false, "--apply although an import ran before (the same games count twice)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: import-ranked --data <data folder> [--apply [--force]] <logs folder or log file>...")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *data == "" || fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	path := filepath.Join(*data, store.FileName)
	if _, err := os.Stat(path); err != nil { // never make a new, empty database by a typo
		fmt.Fprintln(os.Stderr, "import-ranked:", err)
		return 1
	}
	st, err := store.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "import-ranked:", err)
		return 1
	}
	defer st.Close()
	now := time.Now()
	sum, err := rankedlogs.Run(st, rankedlogs.Options{Paths: fs.Args(), Loc: time.Local, Apply: *apply, Force: *force, Now: now},
		os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "import-ranked:", err)
		return 1
	}
	if sum.Applied {
		if err := accounts.Audit(st, accounts.CommandLine, "import ranked games", "", fmt.Sprintf(
			"%d PvP or custom games (%d wins) of %d accounts from %d log files", sum.Games, sum.Wins, sum.Accounts,
			sum.Imported), now); err != nil {
			fmt.Fprintln(os.Stderr, "import-ranked: the audit log:", err)
		}
	}
	return 0
}
