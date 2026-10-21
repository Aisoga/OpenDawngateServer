package rankedlogs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
)

// logFile builds a dgserver log in the format of v0.5 to v0.9 (the lines this package reads, among others it skips).
type logFile struct {
	b strings.Builder
}

func (l *logFile) add(clock, who, format string, args ...any) {
	fmt.Fprintf(&l.b, "%s.000123 [%s] %s\n", clock, who, fmt.Sprintf(format, args...))
}

func (l *logFile) start(clock, version string) {
	l.add(clock, "blaze", "broadcasts: none; sent to 0 logged-in connections")
	if version != "" {
		l.add(clock, "stub", "version %s (a player's Start-Client needs the same one; the tunnel's info hello and /dawngate/version.txt answer it)", version)
	}
	l.add(clock, "stub", "accounts in /srv/dawngate/Dawngate-Data/data/accounts.db (3 accounts, schema 4)")
}

// game logs one game: createGame (mode "" = none), then per result the stored line ("won"/"lost"), "spectator" or
// "failed" (the not-stored line before the stored one); finished false = no finishGame (the host died).
func (l *logFile) game(clock string, id int64, mode string, descriptors int, finished bool, results map[int64]string,
	ranked bool) {
	if mode != "" {
		l.add(clock, "host", `match %d -> {"t":"createGame","gameId":%d,"level":"SM_Map_v5","mode":%q,"seed":7919,"roster":[{"user":2000000001,"name":"a","index":0,"team":1,"noun":"","bot":false}],"descriptors":%d}`,
			id, id, mode, descriptors)
	}
	l.add(clock, "host", `match %d <- {"t":"playerFinish","user":2000000001,"stats":{"won":1}}`, id)
	if !finished {
		l.add(clock, "blaze", "   game %d: match host aborted (the host's process ended)", id)
		return
	}
	l.add(clock, "blaze", "   game %d finished: winner team 1, 60.0 s, guardian", id)
	for acct := int64(1000000001); acct <= 1000000009; acct++ {
		r, ok := results[acct]
		if !ok {
			continue
		}
		switch r {
		case "spectator":
			l.add(clock, "blaze", "   account %d: game %d not stored (a spectator)", acct, id)
			continue
		case "failed":
			l.add(clock, "blaze", "   (account %d: game %d not stored: disk I/O error)", acct, id)
			r = "won"
		}
		l.add(clock, "blaze", "   account %d: game %d %s, +102 XP (level 2), 1 wins; rewards wait for the karma vote (playerKarma)", acct, id, r)
		l.add(clock, "blaze", "   account %d: game %d history: shaper alpha (0x105169cd), role 1 (gladiator)", acct, id)
		if ranked {
			l.add(clock, "blaze", "   account %d: game %d leaderboard: counted (pvp game); 1 games, 1 wins", acct, id)
		}
		l.add(clock, "blaze", "   account %d: game %d rewards after the karma vote: bag \"Bronze\", 22 Destiny", acct, id)
	}
}

func (l *logFile) write(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(l.b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) // before every line: no silence note
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	return p
}

const (
	acct1 = int64(1000000001)
	acct2 = int64(1000000002)
	acct3 = int64(1000000003)
)

// testLogs writes the logs folder of the tests: two old releases' files (game ids 1 in both), a newer release's, a
// developer build's, and things that aren't dgserver logs.
func testLogs(t *testing.T) (dir, devFile string) {
	dir = t.TempDir()
	var a logFile // v0.6, started 2026-10-17 17:50:03
	a.start("17:50:03", "v0.6")
	a.game("18:00:00", 1, "pvp", 1, true, map[int64]string{acct1: "won", acct2: "lost"}, false)
	a.game("18:30:00", 2, "custom", 2, true, map[int64]string{acct1: "lost", acct2: "won", acct3: "spectator"}, false)
	a.game("19:00:00", 3, "custom", 0x412, true, map[int64]string{acct1: "won"}, false) // a practice group's game
	a.game("19:30:00", 4, "practice", 0x50C, true, map[int64]string{acct2: "won"}, false)
	a.game("20:00:00", 5, "pvp", 1, false, nil, false) // never finished
	a.game("21:00:00", 6, "pvp", 1, true, map[int64]string{acct2: "failed"}, false)
	a.game("21:30:00", 8, "", 0, true, map[int64]string{acct2: "won"}, false) // no createGame line
	a.add("23:59:58", "blaze", "a line before midnight")
	a.game("00:10:00", 7, "custom", 2, true, map[int64]string{acct1: "won"}, false) // the next day: 2026-10-18
	a.write(t, dir, "20261017-175003.log")

	var b logFile // v0.8, game ids start again
	b.start("20:26:34", "v0.8")
	b.game("20:30:00", 1, "pvp", 1, true, map[int64]string{acct1: "won"}, false)
	b.write(t, dir, "20261018-202634.log")

	var c logFile // v0.9.1: counts the games itself
	c.start("19:45:00", "v0.9.1")
	c.game("19:50:00", 1, "pvp", 1, true, map[int64]string{acct1: "won"}, true)
	c.write(t, dir, "20261021-194500.log")

	var d logFile // a developer build
	d.start("10:00:00", "dev-1c3fc5f0f7+dirty")
	d.game("10:30:00", 1, "pvp", 1, true, map[int64]string{acct2: "won"}, false)
	devFile = d.write(t, dir, "20261020-100000.log")

	var e logFile // no version line, a developer build after the counts (its leaderboard lines)
	e.start("11:00:00", "")
	e.game("11:00:00", 1, "pvp", 1, true, map[int64]string{acct2: "won"}, true)
	e.write(t, dir, "20261020-110000.log")

	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("account 1000000001: game 1 won, x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "hosts", "match1"), 0o755)
	return dir, devFile
}

// testStore makes the three accounts, account 1 with live counts since the update and the profile's totals.
func testStore(t *testing.T, now time.Time) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for i, email := range []string{"alice@example.com", "bob@example.com", "carol@example.com"} {
		a, _, err := st.Login(email, now)
		if err != nil || a.UserID != acct1+int64(i) {
			t.Fatalf("account %d: %d %v", i, a.UserID, err)
		}
	}
	if _, err := st.Update(acct1, func(a *store.Account) {
		a.History = progress.ApplyRanked(progress.ApplyRanked(a.History, now, true), now, false) // 2 live games
		a.Stats = map[string]map[string]string{progress.PlayerStats: {"PLAYED": "9", "WINS": "6"}}
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func TestRead(t *testing.T) {
	dir, dev := testLogs(t)
	files, err := Read([]string{dir}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, fmt.Sprintf("%s %s %v", f.Name, f.Version, f.Import))
	}
	want := []string{"20261017-175003.log v0.6 true", "20261018-202634.log v0.8 true", "20261020-100000.log dev-1c3fc5f0f7+dirty false",
		"20261020-110000.log  false", "20261021-194500.log v0.9.1 false"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("files\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	a := files[0]
	byID := map[int64]*Game{}
	for _, g := range a.Games {
		byID[g.ID] = g
	}
	for id, want := range map[int64]string{1: "true pvp game", 2: "true custom game", 3: "false practice group's game",
		4: "false practice game", 7: "true custom game", 8: "false no createGame line, mode unknown"} {
		r, kind := byID[id].Ranked()
		if got := fmt.Sprint(r, " ", kind); got != want {
			t.Errorf("game %d: %s, want %s", id, got, want)
		}
	}
	if g := byID[5]; g.Finished || len(g.Results) != 0 {
		t.Errorf("game 5 (no finish): %+v", g)
	}
	if g := byID[2]; g.Spectators != 1 || len(g.Results) != 2 {
		t.Errorf("game 2 (a spectator): %+v", g)
	}
	if g := byID[6]; len(g.Results) != 1 || !g.Results[0].Failed {
		t.Errorf("game 6 (not stored): %+v", g)
	}
	if at := byID[7].Results[0].At; !at.Equal(time.Date(2026, 10, 18, 0, 10, 0, 123000, time.UTC)) {
		t.Errorf("game 7 after midnight: %v", at)
	}
	if len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "game 8: no createGame line") {
		t.Errorf("notes: %q", a.Notes)
	}
	// the developer build's file is read when it is named itself; a newer release's or one with leaderboard lines never
	files, err = Read([]string{dir, dev, filepath.Join(dir, "20261021-194500.log"), filepath.Join(dir, "20261020-110000.log")}, time.UTC)
	if err != nil || len(files) != 5 || !files[2].Import || !files[2].Named || files[3].Import || files[4].Import {
		t.Fatalf("named: %v %+v", err, files)
	}
	if !strings.Contains(files[3].Reason, "its \"leaderboard:\" lines") || !strings.Contains(files[4].Reason, "v0.9.1 counts") {
		t.Errorf("reasons: %q, %q", files[3].Reason, files[4].Reason)
	}
	if _, err := Read([]string{filepath.Join(dir, "notes.txt")}, time.UTC); err == nil {
		t.Error("a named file without a log's name was read")
	}
}

// A log written in another zone: the lines' local times are converted, the day is the UTC day.
func TestReadZone(t *testing.T) {
	dir := t.TempDir()
	var l logFile
	l.start("01:20:00", "v0.7")
	l.game("01:30:00", 1, "pvp", 1, true, map[int64]string{acct1: "won"}, false)
	l.write(t, dir, "20261019-012000.log")
	berlin := time.FixedZone("CEST", 2*3600)
	files, err := Read([]string{dir}, berlin)
	if err != nil {
		t.Fatal(err)
	}
	if at := files[0].Games[0].Results[0].At.UTC(); at.Format("2006-01-02 15:04") != "2026-10-18 23:30" {
		t.Errorf("UTC time %v", at)
	}
	if len(files[0].Notes) != 0 {
		t.Errorf("notes: %q", files[0].Notes)
	}
	// written more than a day after its last line: a silence of a day may hide a date change, said in a note
	later := time.Date(2026, 10, 21, 3, 0, 0, 0, berlin)
	os.Chtimes(filepath.Join(dir, "20261019-012000.log"), later, later)
	if files, err = Read([]string{dir}, berlin); err != nil || len(files[0].Notes) != 1 ||
		!strings.Contains(files[0].Notes[0], "last written 2026-10-21 03:00, more than a day after its last line's time 2026-10-19 01:30") {
		t.Errorf("silence: %v %q", err, files[0].Notes)
	}
}

func TestDryRunApplyAndAgain(t *testing.T) {
	dir, _ := testLogs(t)
	st := testStore(t, now)
	before, _ := st.Account(acct1)
	var out bytes.Buffer
	sum, err := Run(st, Options{Paths: []string{dir}, Loc: time.UTC, Now: now}, &out)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"dry run: nothing is written",
		"20261017-175003.log: version v0.6; 7 games finished, 3 PvP or custom games with players, 8 player results",
		"note: game 8: no createGame line",
		"20261021-194500.log: skipped: version v0.9.1 counts",
		"20261020-100000.log: skipped: version dev-1c3fc5f0f7+dirty is a developer build",
		`account 1000000001 "alice" (login "alice@example.com"):`,
		"in the imported logs: 5 games, 4 wins; PvP or custom 4 games, 3 wins (custom game 2, pvp game 2); other (practice) 1",
		"per UTC day (games/wins): 2026-10-17 2/1, 2026-10-18 2/2",
		"in the skipped logs: 1 games, 1 wins",
		"! the profile counts 3 games and 1 wins more than these logs show",
		"Leaderboard: 2 games, 1 wins now -> 6 games, 4 wins",
		"in the imported logs: 4 games, 3 wins; PvP or custom 2 games, 1 wins (custom game 1, pvp game 1); other (practice) 2",
		"Total: 2 of 5 log files imported; 2 accounts gain 6 PvP or custom games (4 wins).",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the dry run lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "1000000003") {
		t.Errorf("the spectator is listed:\n%s", text)
	}
	if sum.Applied || sum.Games != 6 {
		t.Errorf("summary %+v", sum)
	}
	if after, _ := st.Account(acct1); after.History.Ranked != before.History.Ranked {
		t.Fatal("the dry run wrote")
	}
	if _, _, found, _ := st.Setting(SettingKey); found {
		t.Fatal("the dry run set the mark")
	}

	out.Reset()
	if sum, err = Run(st, Options{Paths: []string{dir}, Loc: time.UTC, Now: now, Apply: true}, &out); err != nil || !sum.Applied {
		t.Fatalf("apply: %v %+v", err, sum)
	}
	a, _ := st.Account(acct1)
	if a.History.Ranked != (store.GamesWon{Games: 6, Wins: 4}) {
		t.Errorf("account 1: %+v", a.History.Ranked)
	}
	days := map[string]store.GamesWon{}
	for _, d := range a.History.Days {
		days[d.Day] = d.Ranked
	}
	if len(days) != 3 || days["2026-10-17"] != (store.GamesWon{Games: 2, Wins: 1}) ||
		days["2026-10-18"] != (store.GamesWon{Games: 2, Wins: 2}) || days["2026-10-22"] != (store.GamesWon{Games: 2, Wins: 1}) {
		t.Errorf("account 1's days: %+v", a.History.Days)
	}
	if b, _ := st.Account(acct2); b.History.Ranked != (store.GamesWon{Games: 2, Wins: 1}) {
		t.Errorf("account 2: %+v", b.History.Ranked)
	}
	if c, _ := st.Account(acct3); c.History.Ranked != (store.GamesWon{}) {
		t.Errorf("account 3 (a spectator): %+v", c.History.Ranked)
	}
	mark, _, found, _ := st.Setting(SettingKey)
	if !found || !strings.Contains(mark, "6 PvP or custom games (4 wins) of 2 accounts from 2 log files: 20261017-175003.log, 20261018-202634.log") {
		t.Errorf("mark %q", mark)
	}

	// a second --apply is refused and changes nothing; the dry run says so
	out.Reset()
	if _, err := Run(st, Options{Paths: []string{dir}, Loc: time.UTC, Now: now, Apply: true}, &out); !errors.Is(err, ErrImportedBefore) {
		t.Fatalf("second apply: %v", err)
	}
	if again, _ := st.Account(acct1); again.History.Ranked != a.History.Ranked {
		t.Error("the refused apply wrote")
	}
	out.Reset()
	Run(st, Options{Paths: []string{dir}, Loc: time.UTC, Now: now}, &out)
	if !strings.Contains(out.String(), "NOTE: the import ran before") || !strings.Contains(out.String(), "--apply would be refused") {
		t.Errorf("dry run after the import:\n%s", out.String())
	}
	// --force imports again
	if _, err := Run(st, Options{Paths: []string{dir}, Loc: time.UTC, Now: now, Apply: true, Force: true}, &out); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.Account(acct1); again.History.Ranked != (store.GamesWon{Games: 10, Wins: 7}) {
		t.Errorf("forced: %+v", again.History.Ranked)
	}
}

// An account the logs name but the database no longer has is skipped; the others are written.
func TestMissingAccount(t *testing.T) {
	dir := t.TempDir()
	var l logFile
	l.start("11:00:00", "v0.8")
	l.game("12:00:00", 1, "pvp", 1, true, map[int64]string{acct1: "won", 1000000009: "won"}, false)
	l.write(t, dir, "20261020-110000.log")
	st := testStore(t, now)
	var out bytes.Buffer
	sum, err := Run(st, Options{Paths: []string{dir}, Loc: time.UTC, Now: now, Apply: true}, &out)
	if err != nil || sum.Missing != 1 || sum.Accounts != 1 {
		t.Fatalf("%v %+v\n%s", err, sum, out.String())
	}
	if !strings.Contains(out.String(), "account 1000000009: not in the database") {
		t.Error(out.String())
	}
	if a, _ := st.Account(acct1); a.History.Ranked != (store.GamesWon{Games: 3, Wins: 2}) {
		t.Errorf("%+v", a.History.Ranked)
	}
}
