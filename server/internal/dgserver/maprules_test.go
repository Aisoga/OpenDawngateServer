package dgserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/store"
	"dawngate/server/internal/web"
)

// tunedPackage writes the fixture CM_Tuned (MAPRULES2's balance-only rules) into a maps folder of its own, with
// victory (a JSON object's text, "" = none) and startLevel (0 = none) added to its rules block.
func tunedPackage(t *testing.T, dir, victory string, startLevel int) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "testdata", "maprules", "CM_Tuned", "map.json"))
	if err != nil {
		t.Fatal(err)
	}
	add := ""
	if victory != "" {
		add += `"victory": ` + victory + ", "
	}
	if startLevel > 0 {
		add += `"startLevel": ` + itoa(uint32(startLevel)) + ", "
	}
	text := strings.Replace(string(src), `"rules": {`, `"rules": {`+add, 1)
	if add != "" && text == string(src) {
		t.Fatal("the fixture has no rules block")
	}
	if err := os.MkdirAll(filepath.Join(dir, "CM_Tuned"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CM_Tuned", "map.json"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// WINBIND-B: FolderLevel takes the map's Binding race (mapc.HostRules().Bindings) into the level, so createGame sends it.
func TestFolderLevelBindingRace(t *testing.T) {
	dir := t.TempDir()
	tunedPackage(t, dir, `{"bindings": 2, "minutes": 3}`, 0)
	pkgs, errs := mapc.ReadFolder(dir)
	if len(pkgs) != 1 || len(errs) != 0 {
		t.Fatalf("%d packages, %v", len(pkgs), errs)
	}
	l := FolderLevel(pkgs[0])
	if l.MapRules.Bindings != 2 || l.MapRules.Minutes != 3 || l.MapRules.Kills != 0 || !l.MapRules.Victory() {
		t.Errorf("FolderLevel rules %+v", l.MapRules)
	}
	if w := l.MapRules.Wire(); w == nil || w.Bindings != 2 || w.Minutes != 3 {
		t.Errorf("wire %+v", w)
	}
	if p := l.MapRules.Prefix(); p != "[Bind 2/3m] " {
		t.Errorf("prefix %q", p)
	}
}

// MAPRULES5: FolderLevel takes the package's rules (the host's parts, the balance, DescribeRules' texts); the start-up
// line names them; the folder read again with changed rules replaces the level (ReplaceLevels compares with ==).
func TestFolderLevelMapRules(t *testing.T) {
	orig := blaze.PlayableLevels
	t.Cleanup(func() { blaze.PlayableLevels = orig })
	dir := t.TempDir()
	tunedPackage(t, dir, `{"kills": 20, "minutes": 15}`, 6)
	pkgs, errs := mapc.ReadFolder(dir)
	if len(pkgs) != 1 || len(errs) != 0 {
		t.Fatalf("%d packages, %v", len(pkgs), errs)
	}
	summary, short := mapc.DescribeRules(pkgs[0].Map.Rules)
	want := blaze.MapRules{Kills: 20, Minutes: 15, StartLevel: 6, Tuned: true, Summary: summary, Short: short}
	if l := FolderLevel(pkgs[0]); l.MapRules != want || l.Name != "CM_Tuned" || !strings.HasPrefix(short, "Race 20 · 15 min · Lv 6") {
		t.Errorf("FolderLevel %+v, want rules %+v", l, want)
	}
	notes, err := addFolderMaps(dir)
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "CM_Tuned (~#tunedring, ") ||
		!strings.Contains(notes[0], ", rules "+short+")") {
		t.Fatalf("%v %q", err, notes)
	}
	// the map editor's Test again after the kill target changed: the level is replaced
	again := t.TempDir()
	tunedPackage(t, again, `{"kills": 3}`, 0)
	if _, err := addFolderMaps(again); err != nil {
		t.Fatal(err)
	}
	l, _ := blaze.FindLevel("CM_Tuned")
	if l.MapRules.Kills != 3 || l.MapRules.Minutes != 0 || l.MapRules.StartLevel != 0 || !l.MapRules.Tuned ||
		l.MapRules.Prefix() != "[Race 3] " {
		t.Errorf("after the change: %+v", l.MapRules)
	}
	// the fixture as it is (balance only): no host part, no prefix
	plain := t.TempDir()
	tunedPackage(t, plain, "", 0)
	pkgs, _ = mapc.ReadFolder(plain)
	if r := FolderLevel(pkgs[0]).MapRules; !r.Tuned || r.HostPart() || r.Prefix() != "" || r.Short != "Tuned" {
		t.Errorf("balance only: %+v", r)
	}
}

// The match history names a map's rules on the row (a badge) and in the match, and whether they kept a game unrated
// that would otherwise rate; the result's reason stays the host's word.
func TestHistoryMapRules(t *testing.T) {
	s := store.New()
	seats := []store.MatchSeat{{Seat: 0, Team: 1, Kills: 3}, {Seat: 5, Team: 2, Bot: true, Kills: 1}}
	for _, m := range []store.Match{
		{Kind: "custom", Mode: "custom", Level: "SM_Map_v5", Winner: 1, Reason: "kills", MapRules: `{"kills":3,"tuned":true}`},
		{Kind: "practice", Mode: "practice", Level: "SM_Map_v5", Winner: 1, Reason: "time", MapRules: `{"minutes":15}`},
		{Kind: "pvp", Mode: "pvp", Level: "SM_Map_v5", Winner: 1, Reason: "guardian", MapRules: `{"tuned":true}`},
	} {
		m.Started, m.Ended, m.Seats = historyNow.Add(-time.Hour), historyNow, seats
		if _, err := s.InsertMatch(m); err != nil {
			t.Fatal(err)
		}
	}
	h := newHistory(s, nil, true, func() time.Time { return historyNow }, func(string, string, ...any) {})
	for _, c := range []struct {
		id   string
		has  string
		lack string
	}{
		{"1", `"reason":"kills"`, ""},
		{"1", `"mapRules":"Race 3 · Tuned","mapUnrated":true`, ""},
		{"2", `"mapRules":"15 min"`, `"mapUnrated"`}, // practice: never rated anyway
		{"3", `"mapRules":"Tuned"`, `"mapUnrated"`},  // balance only: rated
	} {
		code, got := ask(h, web.HistoryGame, "id="+c.id)
		has := strings.ReplaceAll(c.has, `·`, "\\"+"u00b7")
		if code != 200 || !(strings.Contains(got, has) || strings.Contains(got, strings.ReplaceAll(has, "\\"+"u00b7", "·"))) ||
			(c.lack != "" && strings.Contains(got, c.lack)) {
			t.Errorf("match %s: %d %s, want %s without %q", c.id, code, got, c.has, c.lack)
		}
	}
	// every match is in the list (a played kill race isn't Unrated)
	if _, got := ask(h, web.HistoryGames, "n=50"); strings.Count(got, `"mapRules":`) != 3 {
		t.Errorf("the list: %s", got)
	}
}
