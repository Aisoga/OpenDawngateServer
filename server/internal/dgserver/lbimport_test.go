package dgserver

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/rankedlogs"
	"dawngate/server/internal/store"
)

// --import-ranked-logs: a dry run by default, --apply writes once (with an audit entry), a second --apply is refused
// unless --force; --apply alone is refused. The import's rules are tested in rankedlogs.
func TestImportRankedLogsCommand(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	st, err := store.Open(filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := accounts.CreateAccount(st, "alice", now)
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	logs := t.TempDir()
	log := "20:00:00.000001 [stub] version v0.8 (a player's Start-Client needs the same one)\n" +
		`20:10:00.000001 [host] match 1 -> {"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"pvp","seed":7919,"descriptors":1}` + "\n" +
		"20:30:00.000001 [blaze]    game 1 finished: winner team 1, 60.0 s, guardian\n" +
		"20:30:00.000002 [blaze]    account " + strconv.FormatInt(a.UserID, 10) + ": game 1 won, +102 XP (level 2), 1 wins; bag\n"
	if err := os.WriteFile(filepath.Join(logs, "20261020-200000.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(c accountCommands) (int, string, string) {
		c.importLoc = time.UTC
		var out, errOut bytes.Buffer
		code := c.run(dir, &out, &errOut, now)
		return code, out.String(), errOut.String()
	}
	ranked := func() store.GamesWon {
		st, err := store.Open(filepath.Join(dir, store.FileName))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		acct, _ := st.Account(a.UserID)
		return acct.History.Ranked
	}
	if code, out, _ := run(accountCommands{importLogs: stringList{logs}}); code != 0 ||
		!strings.Contains(out, "Leaderboard: 0 games, 0 wins now -> 1 games, 1 wins") || ranked() != (store.GamesWon{}) {
		t.Fatalf("dry run: %d %s", code, out)
	}
	if code, out, _ := run(accountCommands{importLogs: stringList{logs}, apply: true}); code != 0 ||
		!strings.Contains(out, "Written.") || ranked() != (store.GamesWon{Games: 1, Wins: 1}) {
		t.Fatalf("apply: %d %s", code, out)
	}
	if code, _, errOut := run(accountCommands{importLogs: stringList{logs}, apply: true}); code != 1 ||
		!strings.Contains(errOut, "imported from the logs before") || ranked() != (store.GamesWon{Games: 1, Wins: 1}) {
		t.Fatalf("second apply: %d %s", code, errOut)
	}
	if code, _, _ := run(accountCommands{importLogs: stringList{logs}, apply: true, force: true}); code != 0 ||
		ranked() != (store.GamesWon{Games: 2, Wins: 2}) {
		t.Fatalf("forced: %d", code)
	}
	if code, _, errOut := run(accountCommands{apply: true}); code != 1 || !strings.Contains(errOut, "go with --import-ranked-logs") {
		t.Fatalf("--apply alone: %d %s", code, errOut)
	}
	st, _ = store.Open(filepath.Join(dir, store.FileName))
	defer st.Close()
	entries, _ := st.AuditLog(10, 1<<62)
	n := 0
	for _, e := range entries {
		if e.Action == "import ranked games" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d audit entries of the import, want 2: %+v", n, entries)
	}
	if _, _, found, _ := st.Setting(rankedlogs.SettingKey); !found {
		t.Error("no import mark")
	}
	// the flag parses (repeatable) and the command needs no running server
	if code := Main([]string{"--data", dir, "--import-ranked-logs", logs, "--import-ranked-logs", logs}); code != 0 {
		t.Errorf("Main: %d", code)
	}
}
