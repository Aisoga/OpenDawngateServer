package blaze

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/rules"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// tunedLevel is a folder map with every host rule and its balance changed (as dgserver's FolderLevel makes it from a
// package whose map.json has a rules block).
var tunedLevel = PlayableLevel{Name: "CM_Tuned", Tag: "tuned", Label: "[Tuned] ", Archive: "zz_CM_Tuned.snap",
	Display: "Tuned Test", MapRules: MapRules{Kills: 20, Minutes: 15, StartLevel: 6, Tuned: true,
		Summary: "First to 20 kills or most kills after 15 minutes wins; everyone starts at level 6; respawn 8 s.",
		Short:   "Race 20 · 15 min · Lv 6 · Tuned"}}

// balanceLevel only changes balance values: rated as before, no prefix, no host part.
var balanceLevel = PlayableLevel{Name: "CM_Balance", Tag: "balance", Archive: "zz_CM_Balance.snap", Display: "Balance",
	MapRules: MapRules{Tuned: true, Summary: "Respawn 8 s; XP x1.5.", Short: "Tuned"}}

// The map's rules in words: the browser's prefix, createGame's field, the host's parts, the stored record.
func TestMapRulesWords(t *testing.T) {
	for _, c := range []struct {
		r      MapRules
		prefix string
		wire   *gmshost.MapRules
		host   string
	}{
		{MapRules{}, "", nil, ""},
		{MapRules{Tuned: true}, "", nil, ""},
		{MapRules{StartLevel: 6}, "", &gmshost.MapRules{StartLevel: 6}, "start level"},
		{MapRules{Kills: 20}, "[Race 20] ", &gmshost.MapRules{Kills: 20}, "kill race"},
		{MapRules{Minutes: 15}, "[15 min] ", &gmshost.MapRules{Minutes: 15}, "time limit"},
		{MapRules{Kills: 20, Minutes: 15, StartLevel: 6}, "[Race 20/15m] ", &gmshost.MapRules{Kills: 20, Minutes: 15,
			StartLevel: 6}, "kill race, time limit and start level"},
	} {
		if got := c.r.Prefix(); got != c.prefix {
			t.Errorf("%+v: prefix %q, want %q", c.r, got, c.prefix)
		}
		if got := c.r.Wire(); !reflect.DeepEqual(got, c.wire) {
			t.Errorf("%+v: wire %+v, want %+v", c.r, got, c.wire)
		}
		if got := c.r.hostWords(); got != c.host {
			t.Errorf("%+v: host words %q, want %q", c.r, got, c.host)
		}
	}
	tr := tunedLevel.MapRules
	if got := tr.Record(true); got != (store.MapRules{Kills: 20, Minutes: 15, StartLevel: 6, Tuned: true, From: store.RulesFromMap}) {
		t.Errorf("played: %+v", got)
	}
	if got := tr.Record(false); got != (store.MapRules{Tuned: true}) { // a host without the feature played only the balance
		t.Errorf("not played: %+v", got)
	}
	if got := PlayedRulesWords(tr.Record(true)); got != tr.Short {
		t.Errorf("the history's words %q, want the short text %q", got, tr.Short)
	}
	if got := tr.mapUnratedReason(); got != "The map's kill race and time limit make this game unrated." {
		t.Errorf("reason %q", got)
	}
	if balanceLevel.MapRules.mapUnratedReason() != "" || balanceLevel.MapRules.mapHostFinding(false) != "" {
		t.Error("a balance-only map is unrated or a finding")
	}
}

// The match's System lines: the lobby's rules, the map's summary, what an older host doesn't play, (Unrated); a
// summary too long for one line (MAPRULES9) keeps the victory and the start level whole on the first line and puts
// the balance on "Map balance: " lines, each within MatchNoteMaxRunes.
func TestMatchNoteText(t *testing.T) {
	tr := tunedLevel.MapRules
	for _, c := range []struct {
		lobby     string
		m         MapRules
		hostPlays bool
		unrated   bool
		want      string
	}{
		{"All Random, Shop on Death", MapRules{}, false, false, "Rules: All Random, Shop on Death"},
		{"Standard", MapRules{}, false, true, "Rules: Standard (Unrated)"},
		{"Draft", tr, true, true, "Rules: Draft · Map: First to 20 kills or most kills after 15 minutes wins; everyone " +
			"starts at level 6; respawn 8 s (Unrated)"},
		{"", tr, true, false, "Map: First to 20 kills or most kills after 15 minutes wins; everyone starts at level 6; " +
			"respawn 8 s"},
		{"", tr, false, false, "Map: First to 20 kills or most kills after 15 minutes wins; everyone starts at level 6; " +
			"respawn 8 s (not played: the match host is too old)"},
		{"", balanceLevel.MapRules, false, false, "Map: Respawn 8 s; XP x1.5"},
	} {
		if got := matchNoteLines(c.lobby, c.m, c.hostPlays, c.unrated); len(got) != 1 || got[0] != c.want {
			t.Errorf("matchNoteLines(%q, %q, %v, %v)\n got %q\nwant %q", c.lobby, c.m.Short, c.hostPlays, c.unrated, got, c.want)
		}
	}
	vectors := describeVectors(t)
	// every member set (MAPRULES1: about 370 characters): the victory and the start level first, whole, the balance
	// on two more lines
	every := vectors["every member"]
	want := []string{
		"Rules: Custom rules, Shop on Death · Map: First to 20 kills or most kills after 15 minutes wins; everyone starts " +
			"at level 6 (Unrated)",
		"Map balance: Respawn 8 s + 0.5 s a level + 0.5 s a minute; minions at 0:30, then every 25 s; 1,500 starting " +
			"gold, 200 gold a minute from 0:30, kill gold x1.5; XP x1.5",
		"Map balance: Bindings return 5 min after they fall; wells open at 5:00, relock 2 min after a capture, well gold " +
			"x1.5; surrender from minute 10",
	}
	if got := matchNoteLines("Custom rules, Shop on Death", every, true, true); !reflect.DeepEqual(got, want) {
		t.Errorf("every member:\n got %q\nwant %q", got, want)
	}
	// a map with only balance values that don't fit: the first line filled with whole parts, the rest after it
	balance := every
	balance.Kills, balance.Minutes, balance.StartLevel = 0, 0, 0
	balance.Summary = strings.SplitN(every.Summary, "; ", 3)[2]
	balance.Summary = strings.ToUpper(balance.Summary[:1]) + balance.Summary[1:]
	want = []string{
		"Map: Respawn 8 s + 0.5 s a level + 0.5 s a minute; minions at 0:30, then every 25 s; 1,500 starting gold, 200 " +
			"gold a minute from 0:30, kill gold x1.5; XP x1.5; Bindings return 5 min after they fall",
		"Map balance: Wells open at 5:00, relock 2 min after a capture, well gold x1.5; surrender from minute 10",
	}
	if got := matchNoteLines("", balance, true, false); !reflect.DeepEqual(got, want) {
		t.Errorf("balance only:\n got %q\nwant %q", got, want)
	}
	// every vector of mapc.DescribeRules in each line a match can get (the lobby's rules and (Unrated) come only with
	// a host that plays the map's rules: a custom game on an older host can't start, a practice or PvP game has no
	// admin): nothing is cut, every part comes once and in order, the victory and the start level on the first line,
	// every line within MatchNoteMaxRunes, each line after the first a "Map balance: " line
	for name, m := range vectors {
		for _, c := range []struct {
			lobby              string
			hostPlays, unrated bool
		}{{"", true, false}, {"", true, true}, {"", false, false}, {"Custom rules, Shop on Death", true, true}} {
			checkNoteLines(t, name, m, matchNoteLines(c.lobby, m, c.hostPlays, c.unrated))
		}
	}
	// the first line is never cut, even past MatchNoteMaxRunes (a long lobby's rules on an older host, which no game
	// gets: the client shows it whole, the chat wraps it)
	widest := vectors["the widest short text"]
	long := "Draft, All Random, Shop on Death, Lightning only, a pool of twelve shapers"
	if got := matchNoteLines(long, widest, false, true); len(got) != 2 || runes(got[0]) <= MatchNoteMaxRunes ||
		!strings.Contains(got[0], "First to 200 kills or most kills after 90 minutes wins; everyone starts at level 20 (") {
		t.Errorf("the widest first line: %q", got)
	}
	// a summary with more parts than MatchNoteMaxLines lines hold (none of mapc's): the last line ends "; …"
	many := MapRules{Tuned: true, Summary: strings.Repeat(strings.Repeat("x", 90)+"; ", 12) + "end."}
	if got := matchNoteLines("", many, true, false); len(got) != MatchNoteMaxLines || !strings.HasSuffix(got[len(got)-1], "; …") {
		t.Errorf("too many parts: %d lines %q", len(got), got)
	}
}

// describeVectors are the MapRules of server/testdata/maprules/describe.json's cases with a summary (MAPRULES1's
// vectors, as FolderLevel makes them), by name.
func describeVectors(t *testing.T) map[string]MapRules {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "maprules", "describe.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name    string      `json:"name"`
			Rules   *mapc.Rules `json:"rules"`
			Summary string      `json:"summary"`
			Short   string      `json:"short"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	out := map[string]MapRules{}
	for _, c := range file.Cases {
		if c.Summary == "" {
			continue
		}
		h := c.Rules.HostRules()
		out[c.Name] = MapRules{Kills: h.Kills, Minutes: h.Minutes, StartLevel: h.StartLevel, Bindings: h.Bindings,
			Tuned: c.Rules.BalanceChanged(), Summary: c.Summary, Short: c.Short}
	}
	if _, ok := out["every member"]; !ok || len(out) < 10 {
		t.Fatalf("%d vectors, every member %v", len(out), ok)
	}
	return out
}

// checkNoteLines checks a match's System lines against the map's summary: every part once, in order, none cut, the
// victory and the start level on the first line, each line within MatchNoteMaxRunes.
func checkNoteLines(t *testing.T, name string, m MapRules, lines []string) {
	t.Helper()
	lower := func(s string) string { return strings.ToLower(s[:1]) + s[1:] }
	var got []string
	for i, line := range lines {
		if n := utf8.RuneCountInString(line); n > MatchNoteMaxRunes {
			t.Errorf("%s: line %d has %d characters: %q", name, i+1, n, line)
		}
		text, ok := "", false
		if i == 0 {
			_, text, ok = strings.Cut(line, "Map: ")
			if i := strings.Index(text, " played: the match host is too old)"); i >= 0 { // the unplayed tail
				text = text[:strings.LastIndex(text[:i], " (")]
			}
			text = strings.TrimSuffix(text, " (Unrated)")
		} else {
			text, ok = strings.CutPrefix(line, matchNoteBalance)
		}
		if !ok || text == "" || strings.Contains(line, "…") {
			t.Errorf("%s: line %d %q", name, i+1, line)
			return
		}
		for _, p := range strings.Split(text, "; ") {
			got = append(got, lower(p))
		}
	}
	var want []string
	for _, p := range strings.Split(strings.TrimSuffix(m.Summary, "."), "; ") {
		want = append(want, lower(p))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: the parts %q, want %q", name, got, want)
	}
	host := 0
	if m.Victory() {
		host++
	}
	if m.StartLevel > 0 {
		host++
	}
	for _, p := range want[:host] {
		if !strings.Contains(strings.ToLower(lines[0]), strings.ToLower(p)) {
			t.Errorf("%s: the first line %q lacks %q", name, lines[0], p)
		}
	}
}

// A map that sets every member: the players get its three lines in order, each its own record (RulesNoteID,
// RulesNoteID+1, +2), the log names them; the next match's same lines get a number each.
func TestMatchNoteLinesSent(t *testing.T) {
	ar := aram(t)
	ts, host, _ := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) { s.TestRules = &ar })
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	hg.mapRules, hg.mapHost = describeVectors(t)["every member"], true
	lines := hg.noteLinesLocked()
	hg.mu.Unlock()
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "Rules: All Random, Shop on Death · Map: First to 20 kills") {
		t.Fatalf("the lines %q", lines)
	}
	record := func(i int, text string) []byte {
		w := tdf.NewWriter()
		writeClientMessage(w, RulesNoteMessage(i, text))
		return w.Bytes()
	}
	for round, suffix := range []string{"", " (2)"} {
		if round > 0 {
			hg.mu.Lock()
			hg.noteSent = false
			hg.mu.Unlock()
		}
		hg.announceRules()
		for i, line := range lines {
			if f := a.note(ComponentMessaging, NotifyMessageID); !bytes.Contains(f.Payload, record(i, line+suffix)) {
				t.Errorf("round %d, line %d: %q, want record %d %q", round+1, i+1, f.Payload, RulesNoteID+i, line+suffix)
			}
		}
		a.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	}
	for i, line := range lines {
		want := fmt.Sprintf(`game 1: the match's rules: %q to 1 player(s) as a System chat line (line %d of 3)`, line, i+1)
		if !strings.Contains(ts.log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, ts.log.String())
		}
	}
	if RulesNoteID+MatchNoteMaxLines > NoticeFirstID || RulesNoteID+MatchNoteMaxLines > 200 {
		t.Errorf("the rules' ids reach the notices'")
	}
}

// Who gets which line: a practice game on the map (never rated: no "(Unrated)"), a PvP game whose host plays the
// victory (unrated), the same on a host without the feature (rated as usual, the line says what isn't played), a
// custom game with its rules, a practice group's game (never rated).
func TestMatchNoteForEveryGame(t *testing.T) {
	tr := tunedLevel.MapRules
	summary := "Map: First to 20 kills or most kills after 15 minutes wins; everyone starts at level 6; respawn 8 s"
	for _, c := range []struct {
		name    string
		mode    string
		flags   string
		custom  bool
		mapHost bool
		want    string
	}{
		{"practice", gmshost.ModePractice, "1292", false, true, summary},
		{"PvP", gmshost.ModePvP, "1", false, true, summary + " (Unrated)"},
		{"PvP, older host", gmshost.ModePvP, "1", false, false, summary +
			" (not played: the match host is too old)"},
		{"custom draft", gmshost.ModeCustom, "2", true, true, "Rules: Draft · " + summary + " (Unrated)"},
		{"practice group", gmshost.ModeCustom, "1042", true, true, summary},
	} {
		hg := resultGame(c.mode, c.flags, 0, c.custom, nil)
		hg.mapRules, hg.mapHost = tr, c.mapHost
		if c.name == "custom draft" {
			hg.custom.rules, _ = rules.Parse("draft")
		}
		if !hg.rulesNoteDueLocked() {
			t.Errorf("%s: no line due", c.name)
		}
		if got := hg.noteLinesLocked(); len(got) != 1 || got[0] != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	if hg := resultGame(gmshost.ModePvP, "1", 0, false, nil); hg.rulesNoteDueLocked() {
		t.Error("a PvP game on a map without rules has a line")
	}
}

// A custom game on a map with a kill race and a time limit, with a match host that plays them: createGame carries
// them, the browser's name has the prefix after the map's label, lobby.state the map's words and Rated forced off
// with the reason, the start goes ahead, the System line says it, and the admin's Rated can't turn it on.
func TestCustomGameMapRules(t *testing.T) {
	withLevels(t, tunedLevel)
	ts, host, addr := rulesHostServer(t, []string{gmshost.FeatureRules, gmshost.FeatureMapRules}, func(s *Server) {
		s.Level, s.MapTrust = tunedLevel.Name, MapTrust{All: true}
	})
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	cg := <-host.created
	if cg.Level != tunedLevel.Name || cg.MapRules == nil || *cg.MapRules != (gmshost.MapRules{Kills: 20, Minutes: 15, StartLevel: 6}) {
		t.Fatalf("createGame level %s, mapRules %+v", cg.Level, cg.MapRules)
	}
	a.note(ComponentGameManager, NotifyGameSetupID)
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	name, mapHost := hg.game.Name, hg.mapHost
	hg.mu.Unlock()
	if !strings.HasPrefix(name, "[Tuned] [Race 20/15m] ") || !mapHost {
		t.Errorf("name %q (the label, then the map's prefix), host plays them %v", name, mapHost)
	}
	ua := uiConnect(t, addr, a)
	ua.call(CallLobbyEnter, "")
	ua.want(catalogLine(t))
	std := rules.Standard()
	ua.want(stateWithRules(mapRulesPart(t, std, tunedLevel.MapRules, true), a.name))
	// the admin's Rated changes the admin's choice, never the map's: lobby.state stays unrated
	ua.call(CallLobbySetRules, `{"rated":true}`)
	ua.want(stateWithRules(mapRulesPart(t, std, tunedLevel.MapRules, true), a.name))
	if got := mapRulesPart(t, std, tunedLevel.MapRules, true); !strings.Contains(got, `"rated":false,`) ||
		!strings.Contains(got, strings.ReplaceAll(`"map":"Race 20 DOT 15 min DOT Lv 6 DOT Tuned","mapText":"First to 20 `+
			`kills or most kills after 15 minutes wins; everyone starts at level 6; respawn 8 s.","mapUnrated":true,`+
			`"mapReason":"The map's kill race and time limit make this game unrated.","fixed":[{"category":"victory",`+
			`"message":"This map has its own win condition."}]}`, "DOT", "\\"+"u00b7")) {
		t.Errorf("the wire: %s", got)
	}
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	hostNext[gmshost.StartGame](t, host)
	hg.announceRules()
	want := "Map: First to 20 kills or most kills after 15 minutes wins; everyone starts at level 6; respawn 8 s (Unrated)"
	if f := a.note(ComponentMessaging, NotifyMessageID); !strings.Contains(string(f.Payload), want) {
		t.Errorf("the line: %q, want %q", f.Payload, want)
	}
	for _, line := range []string{"game 1: the map's rules (kills 20, minutes 15, start level 6): the match host takes them",
		`game 1: the match's rules: "` + want + `" to 1 player(s) as a System chat line`} {
		if !strings.Contains(ts.log.String(), line) {
			t.Errorf("log lacks %q:\n%s", line, ts.log.String())
		}
	}
}

// mapRulesPart is lobby.state's rules part of a game (the admin's, rated by the admin) on a map with rules m.
func mapRulesPart(t *testing.T, c rules.Combination, m MapRules, canEdit bool, findings ...rules.Finding) string {
	t.Helper()
	var lr LobbyRules
	if err := json.Unmarshal([]byte(rulesPart(c, false, canEdit, true, findings...)), &lr); err != nil {
		t.Fatal(err)
	}
	lr.Map, lr.MapText = m.Short, m.Summary
	if why := m.mapUnratedReason(); why != "" {
		lr.Rated, lr.MapUnrated, lr.MapReason = false, true, why
	}
	if m.Victory() { // WINLOBBY: the map decides the win condition
		lr.Fixed = []rules.Finding{{Category: rules.Victory, Message: rules.MapVictoryReason}}
	}
	if lr.Findings == nil {
		lr.Findings = []rules.Finding{}
	}
	b, _ := json.Marshal(lr)
	return strings.ReplaceAll(string(b), "·", "\\"+"u00b7") // the channel writes ASCII JSON
}

// The same map on a match host without the feature: the manager left the rules out of createGame, lobby.state has the
// finding, and START is refused (the game would be played without its kill race, time limit and start level).
func TestCustomGameMapRulesOldHost(t *testing.T) {
	withLevels(t, tunedLevel)
	ts, host, addr := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) {
		s.Level, s.MapTrust = tunedLevel.Name, MapTrust{All: true}
	})
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	if cg := <-host.created; cg.MapRules != nil {
		t.Errorf("an older host got mapRules %+v", cg.MapRules)
	}
	a.note(ComponentGameManager, NotifyGameSetupID)
	finding := rules.Finding{Category: rules.FindingHost, Message: "This game's match host is an older build that can't " +
		"play the map's kill race, time limit and start level."}
	ua := uiConnect(t, addr, a)
	ua.call(CallLobbyEnter, "")
	ua.want(catalogLine(t))
	ua.want(stateWithRules(mapRulesPart(t, rules.Standard(), tunedLevel.MapRules, true, finding), a.name))
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))),
		ErrInvalidGameStateAction)
	for _, line := range []string{"the map's rules (kills 20, minutes 15, start level 6) left out of createGame",
		"game 1: the map's rules (kills 20, minutes 15, start level 6): the match host lacks maprules: they aren't played"} {
		if !strings.Contains(ts.log.String(), line) {
			t.Errorf("log lacks %q:\n%s", line, ts.log.String())
		}
	}
}

// A practice game on a map with rules: createGame asks for them in every mode (the manager leaves them out for a host
// without the feature, as here, and says so).
func TestPracticeGameMapRules(t *testing.T) {
	withLevels(t, tunedLevel)
	hs := newHostedSessionFull(t, "", "", nil, func(s *Server) { s.Level, s.MapTrust = tunedLevel.Name, MapTrust{All: true} },
		func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error { return receive(hs, h) })
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	if cg := hs.hostGot().(gmshost.CreateGame); cg.Level != tunedLevel.Name || cg.MapRules != nil {
		t.Errorf("createGame level %s, mapRules %+v (the host has no maprules)", cg.Level, cg.MapRules)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(hs.log.String(), "the map's rules (kills 20, minutes 15, start level 6) left out of createGame") {
		if time.Now().After(deadline) {
			t.Fatalf("log lacks the manager's line:\n%s", hs.log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	hs.close()
}

// The rating and the Leaderboard: a game whose map victory was played is stored with its map rules (in every list:
// not Unrated), counts for neither the wins nor any rating; on a host without the feature (not played) it is rated as
// usual; a balance-only map stays rated in its type.
func TestMapRulesRating(t *testing.T) {
	for _, c := range []struct {
		name    string
		m       MapRules
		mapHost bool
		record  string
		rated   bool
	}{
		{"a played kill race", tunedLevel.MapRules, true, `{"kills":20,"minutes":15,"startLevel":6,"tuned":true,"from":"map"}`, false},
		{"not played (an older host)", tunedLevel.MapRules, false, `{"tuned":true}`, true},
		{"balance only", balanceLevel.MapRules, true, `{"tuned":true}`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			hg, buf := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{{name: "alpha", team: 0}, {name: "bravo", team: 1}})
			hg.mapRules, hg.mapHost = c.m, c.mapHost
			hg.inGameAt = resultAt.Add(-20 * time.Minute)
			for _, hp := range hg.players {
				hp.atStart = true
			}
			if hg.ranked() != c.rated {
				t.Errorf("ranked %v", hg.ranked())
			}
			f := gmshost.FinishGame{WinnerTeam: 1, DurationMs: 1200000, Reason: ReasonKills}
			hg.persist(nil, f)
			rec := hg.historyMatch(f)
			if rec.MapRules != c.record || rec.Unrated {
				t.Errorf("history map rules %q, unrated %v", rec.MapRules, rec.Unrated)
			}
			hg.rate(rec)
			for _, hp := range hg.players {
				a, _ := hg.s.Store.Account(hp.userID)
				if rated := a.History.Rating.Games == 1; rated != c.rated || (a.History.Ranked.Games == 1) != c.rated {
					t.Errorf("%s: rating %+v, wins count %+v", hp.p.Name, a.History.Rating, a.History.Ranked)
				}
			}
			if !c.rated {
				for _, line := range []string{"leaderboard: not counted (the map's own victory)",
					"not rated (the map's own victory: a kill race, a Binding race or a time limit)"} {
					if n := strings.Count(buf.String(), line); n != 2 {
						t.Errorf("%d lines %q:\n%s", n, line, buf)
					}
				}
			}
		})
	}
}

// PROFILE3's leaver rule with a map's own victory: the kill race, the time limit and sudden death end the game the
// others finished, so whoever left before lost; another reason under 15 minutes doesn't.
func TestLeaverLosesMapReasons(t *testing.T) {
	s := &Server{}
	for _, c := range []struct {
		reason string
		want   bool
	}{{ReasonKills, true}, {ReasonTime, true}, {ReasonSuddenDeath, true}, {"guardian", true}, {"surrender", true},
		{"aborted", false}, {"", false}} {
		if got := s.leaverLoses(gmshost.FinishGame{WinnerTeam: 1, DurationMs: 60000, Reason: c.reason}); got != c.want {
			t.Errorf("%q: %v, want %v", c.reason, got, c.want)
		}
	}
}

// The result's words for a map's own victory: the score from the host's seatStats (bots too), the winner's first; the
// humans' playerFinish kills without seatStats when no bot played; none when bots played without seatStats.
func TestMapRulesResultWords(t *testing.T) {
	for _, c := range []struct {
		reason string
		known  bool
		want   string
	}{
		{ReasonKills, true, "Team 2 won the kill race 20–17"},
		{ReasonKills, false, "Team 2 won the kill race"},
		{ReasonTime, true, "Team 2 won on time 20–17"},
		{ReasonSuddenDeath, true, "Team 2 won in sudden death 20–17"},
		{"guardian", true, "Team 1's Guardian fell"},
		{"surrender", true, "Team 1 surrendered"},
		{"odd", true, "the match ended"},
	} {
		if got := resultHow(c.reason, 2, [2]int64{17, 20}, c.known, nil); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.reason, c.known, got, c.want)
		}
	}
	hg := resultGame(gmshost.ModeCustom, "2", 0, true, []resultSeat{{"Ann", 0, false, kda("", 3, 0, 0)},
		{"Bob", 1, false, kda("", 2, 0, 0)}})
	if k, ok := hg.teamKillsLocked(); !ok || k != [2]int64{3, 2} {
		t.Errorf("from playerFinish: %v %v", k, ok)
	}
	hg.seatStats = map[int]gmshost.SeatStats{0: {Index: 0, User: 2000000001, Team: 1, Stats: map[string]int64{progress.StatKills: 3}},
		5: {Index: 5, User: 2000000002, Team: 2, Stats: map[string]int64{progress.StatKills: 2}}, 6: {Index: 6, Team: 2, Bot: true,
			Stats: map[string]int64{progress.StatKills: 18}}}
	if k, ok := hg.teamKillsLocked(); !ok || k != [2]int64{3, 20} {
		t.Errorf("from seatStats: %v %v", k, ok)
	}
	r := hg.resultLocked(gmshost.FinishGame{WinnerTeam: 2, DurationMs: 805546, Reason: ReasonKills}, resultAt)
	c := loadFixtureContent(t)
	msg := resultMessage(r, true, c)
	if d := msg.Embeds[0].Description; d != "Team 2 won the kill race 20–3 after 13:25" {
		t.Errorf("the post: %q", d)
	}
	if x := recentResultOf(r, c); x.Kills == nil || *x.Kills != [2]int64{3, 20} {
		t.Errorf("the home page's score: %+v", x.Kills)
	}
	// MAPRULES8 (maprules8_custom1g: "won the kill race 0–0"): the live host's seatStats of a human has no kills (only the
	// server's extras); a bot's has them. The human's kills come from its playerFinish, as in the match history.
	live := resultGame(gmshost.ModeCustom, "2", 4, true, []resultSeat{{"Ann", 0, false, kda("", 2, 0, 0)}})
	live.seatStats = map[int]gmshost.SeatStats{
		0: {Index: 0, User: 2000000001, Team: 1, Stats: map[string]int64{"damageDealt": 1906, "goldTotal": 2550}},
		1: {Index: 1, Bot: true, Team: 1, Stats: map[string]int64{progress.StatKills: 0}},
		5: {Index: 5, Bot: true, Team: 2, Stats: map[string]int64{progress.StatKills: 0, progress.StatDeaths: 1}},
		6: {Index: 6, Bot: true, Team: 2, Stats: map[string]int64{progress.StatKills: 0, progress.StatDeaths: 1}}}
	if k, ok := live.teamKillsLocked(); !ok || k != [2]int64{2, 0} {
		t.Errorf("a human's kills from playerFinish beside seatStats: %v %v", k, ok)
	}
	lr := live.resultLocked(gmshost.FinishGame{WinnerTeam: 1, DurationMs: 26000, Reason: ReasonKills}, resultAt)
	if d := resultMessage(lr, true, c).Embeds[0].Description; d != "Team 1 won the kill race 2–0 after 0:26" {
		t.Errorf("the live post: %q", d)
	}
	// a human with a playerFinish but no seatStats line still counts
	delete(live.seatStats, 0)
	if k, _ := live.teamKillsLocked(); k != [2]int64{2, 0} {
		t.Errorf("a human without seatStats: %v", k)
	}
	bots := resultGame(gmshost.ModeCustom, "2", 5, true, []resultSeat{{"Ann", 0, false, kda("", 3, 0, 0)}})
	if _, ok := bots.teamKillsLocked(); ok {
		t.Error("bots without seatStats: a score")
	}
}

// WINBIND-A2/WINBIND-B: line 1 with every win condition and a start level stays within MatchNoteMaxRunes on any host
// (the short "not played" tails); a host without maprules2 plays all but the Binding race, and the tail says so.
func TestMatchNoteBindingRace(t *testing.T) {
	every := describeVectors(t)["every win condition with a start level"]
	if every.Bindings != 3 || every.Kills != 20 || every.Minutes != 20 || every.StartLevel != 6 {
		t.Fatalf("vector %+v", every)
	}
	for _, u := range []MapRules{{}, every.hostOnly(), {Bindings: 3}} {
		lines := matchNoteLinesOf("", every, u, true)
		if runes(lines[0]) > MatchNoteMaxRunes || !strings.Contains(lines[0], "everyone starts at level 6") {
			t.Errorf("unplayed %+v: line 1 has %d characters: %q", u, runes(lines[0]), lines[0])
		}
	}
	got := matchNoteLinesOf("", every, every.unplayed(true, false), false)[0]
	if !strings.HasSuffix(got, "; everyone starts at level 6 (the Binding race isn't played: the match host is too old)") {
		t.Errorf("a MAPRULES4 host: %q", got)
	}
	if got := every.hostFinding(every.unplayed(true, false)); got != "This game's match host is an older build that "+
		"can't play the map's Binding race." {
		t.Errorf("finding %q", got)
	}
	if every.hostFinding(every.unplayed(true, true)) != "" {
		t.Error("a host with maprules2 has a finding")
	}
	if p := every.Prefix(); p != "[Race 20/Bind 3/20m] " {
		t.Errorf("prefix %q", p)
	}
	for _, c := range []struct {
		m    MapRules
		want string
	}{{MapRules{Bindings: 3}, "[Bind 3] "}, {MapRules{Bindings: 1, Minutes: 15}, "[Bind 1/15m] "},
		{MapRules{Kills: 20, Bindings: 3}, "[Race 20/Bind 3] "}, {MapRules{Minutes: 15}, "[15 min] "}} {
		if got := c.m.Prefix(); got != c.want {
			t.Errorf("%+v: prefix %q, want %q", c.m, got, c.want)
		}
	}
	if got := (MapRules{Kills: 20, Bindings: 3}).mapUnratedReason(); got != "The map's kill race and Binding race make "+
		"this game unrated." {
		t.Errorf("reason %q", got)
	}
	// the record: what was played, where the victory came from, the Binding race's score
	rec := every.RecordOf(true, true, []int{3, 1})
	if rec.Bindings != 3 || rec.From != "map" || rec.Destroyed == nil || *rec.Destroyed != [2]int{3, 1} {
		t.Errorf("record %+v", rec)
	}
	if got := rec.Record(); got != `{"kills":20,"minutes":20,"startLevel":6,"bindings":3,"tuned":true,"from":"map",`+
		`"destroyed":[3,1]}` {
		t.Errorf("record %s", got)
	}
	if rec := every.RecordOf(true, false, []int{0, 0}); rec.Bindings != 0 || rec.Destroyed != nil || rec.Kills != 20 {
		t.Errorf("a MAPRULES4 host's record %+v", rec)
	}
	if got := PlayedRulesWords(rec); got != "Race 20 · Bind 3 · 20 min · Lv 6 · Tuned" {
		t.Errorf("words %q", got)
	}
}

// WINBIND-B: the Binding race's result words (Discord) and the leaver rule.
func TestBindingRaceResultWords(t *testing.T) {
	k := [2]int64{12, 9}
	for _, c := range []struct {
		reason   string
		winner   int
		bindings []int
		want     string
	}{
		{ReasonBindings, 1, []int{3, 1}, "Team 1 won the Binding race 3–1"},
		{ReasonBindings, 2, []int{0, 1}, "Team 2 won the Binding race 1–0"},
		{ReasonBindings, 2, nil, "Team 2 won the Binding race"},
		{ReasonTime, 1, []int{2, 1}, "Team 1 won on time with more Bindings 2–1"},
		{ReasonTime, 1, []int{1, 1}, "Team 1 won on time 12–9"},
		{ReasonSuddenDeath, 1, []int{1, 1}, "Team 1 won in sudden death 12–9"},
	} {
		if got := resultHow(c.reason, c.winner, k, true, c.bindings); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.reason, c.bindings, got, c.want)
		}
	}
	if !mapEndReason(ReasonBindings) {
		t.Error("bindings isn't a map end")
	}
	s := &Server{}
	if !s.leaverLoses(gmshost.FinishGame{Reason: ReasonBindings, DurationMs: 60000}) {
		t.Error("a Binding race's end doesn't make the leavers lose")
	}
}
