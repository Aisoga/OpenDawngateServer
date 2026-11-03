package dgserver

import (
	"fmt"
	"time"

	"dawngate/server/internal/issues"
	"dawngate/server/internal/store"
)

// syncIssues brings the store's issues up to the server's list of tracked guesses (issues.Entries, issues.Sync: new
// ones added, changed texts updated, those no longer listed withdrawn; claims and verdicts untouched) when the
// tracker is on (--issues on), and says so for the start-up line. Off, the store is left as it is.
func syncIssues(cfg config, st *store.Store, now time.Time) string {
	if !cfg.issues {
		return "off (--issues on: the players' pages at <website>/issues)"
	}
	entries, err := issues.Entries()
	if err != nil {
		return fmt.Sprintf("on, but the list of tracked guesses can't be read (the pages show the issues as stored): %v", err)
	}
	res, err := issues.Sync(st, entries, now)
	if err != nil {
		return fmt.Sprintf("on, but its issues couldn't be synced with the list of %d tracked guesses (the pages show the "+
			"issues as stored): %v", len(entries), err)
	}
	top := 0
	for _, e := range entries {
		if e.Top {
			top++
		}
	}
	where := "the pages under /issues on the website"
	if cfg.web.port == 0 {
		where = "no pages without the website (--web-port)"
	}
	return fmt.Sprintf("on, %d tracked guesses (%d most important) synced: %s; %s", len(entries), top, res, where)
}
