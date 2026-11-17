package blaze

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/rules"
)

// WINLOBBY: the lobby's win condition (the rules' fifth category, "victory").

// allHostFeatures is a host of this build: every rules and map rules feature.
var allHostFeatures = []string{gmshost.FeatureRules, gmshost.FeatureRules2, gmshost.FeatureMapRules,
	gmshost.FeatureMapRules2}

// lobbyVictoryPart is lobby.state's rules part (the admin's) of a game whose lobby chose a win condition: unrated, the
// greyed Rated with the lobby's reason.
func lobbyVictoryPart(t *testing.T, c rules.Combination, canEdit bool, findings ...rules.Finding) string {
	t.Helper()
	var lr LobbyRules
	if err := json.Unmarshal([]byte(rulesPartRated(c, false, false, canEdit, true, findings...)), &lr); err != nil {
		t.Fatal(err)
	}
	lr.MapUnrated, lr.MapReason = true, LobbyVictoryReason
	b, _ := json.Marshal(lr)
	return string(b)
}

// The feature names agree, and the victory's browser prefix is a map's in the same words.
func TestLobbyVictoryNames(t *testing.T) {
	if rules.HostMapRules2 != gmshost.FeatureMapRules2 {
		t.Fatalf("rules.HostMapRules2 %q, gmshost.FeatureMapRules2 %q", rules.HostMapRules2, gmshost.FeatureMapRules2)
	}
	for _, id := range []string{"guardian", "race10", "race20", "race30", "time15", "race20time20", "bind1", "bind3"} {
		g := rules.Combination{Victory: id}.Goal()
		if got, want := g.Prefix(), (MapRules{Kills: g.Kills, Minutes: g.Minutes, Bindings: g.Bindings}).Prefix(); got != want {
			t.Errorf("%s: prefix %q, a map's %q", id, got, want)
		}
	}
	c, _ := rules.Parse("pick=draft,victory=bind3")
	if got := lobbyName(nil, "", true, false, c, "alpha's Game"); got != "[Bots] [Draft] [Bind 3] alpha's Game" {
		t.Errorf("name %q", got)
	}
}

// The admin chooses First Binding on the built-in map: lobby.state has it with Rated greyed by the lobby's reason, the
// browser's name its prefix; START sends the rules with its id and startGame's victory in numbers.
func TestCustomGameLobbyVictory(t *testing.T) {
	ts, host, addr := rulesHostServer(t, allHostFeatures, nil)
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	ua := uiConnect(t, addr, a)
	ua.call(CallLobbyEnter, "")
	ua.want(catalogLine(t))
	ua.want(stateWithRules(rulesPart(rules.Standard(), false, true, true), a.name))
	ua.call(CallLobbySetRules, `{"rules":{"pick":"blind","shaperPool":"all","shop":"normal","itemPool":"all","victory":"bind1"}}`)
	bind1, _ := rules.Parse("victory=bind1")
	got := lobbyVictoryPart(t, bind1, true)
	ua.want(stateWithRules(got, a.name))
	if !strings.Contains(got, `"text":"First Binding"`) || !strings.Contains(got, `"rated":false`) ||
		!strings.Contains(got, `"mapReason":"The lobby's win condition makes this game unrated."`) {
		t.Errorf("the wire: %s", got)
	}
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	name := hg.game.Name
	hg.mu.Unlock()
	if !strings.HasPrefix(name, "[Bind 1] ") {
		t.Errorf("the browser's name %q", name)
	}
	// the admin's Rated can't make it rated: the state stays unrated
	ua.call(CallLobbySetRules, `{"rated":true}`)
	ua.want(stateWithRules(got, a.name))
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	sg := hostNext[gmshost.StartGame](t, host)
	if sg.Rules == nil || *sg.Rules != (gmshost.Rules{Pick: "blind", ShaperPool: "all", Shop: "normal", ItemPool: "all",
		Victory: "bind1"}) || sg.Victory == nil || *sg.Victory != (gmshost.Victory{Bindings: 1}) {
		t.Errorf("startGame rules %+v, victory %+v", sg.Rules, sg.Victory)
	}
	for _, line := range []string{"; rules First Binding, fillTeams 0",
		`game 1: startGame's victory: kills 0, minutes 0, bindings 1 (the lobby's win condition "bind1")`} {
		if !strings.Contains(ts.log.String(), line) {
			t.Errorf("log lacks %q:\n%s", line, ts.log.String())
		}
	}
	hg.mu.Lock()
	lines := hg.noteLinesLocked()
	hg.mu.Unlock()
	if !reflect.DeepEqual(lines, []string{"Rules: First Binding (Unrated)"}) {
		t.Errorf("the System lines %q", lines)
	}
}

// A host without maprules2 can't play a lobby's win condition: a finding names the option, START is refused.
func TestLobbyVictoryOldHost(t *testing.T) {
	race, _ := rules.Parse("victory=race20")
	ts, host, _ := rulesHostServer(t, []string{gmshost.FeatureRules, gmshost.FeatureRules2, gmshost.FeatureMapRules},
		func(s *Server) { s.TestRules = &race })
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	fs := hg.rulesFindingsLocked()
	hg.mu.Unlock()
	want := []rules.Finding{{Category: rules.Victory,
		Message: "Kill race 20 needs a newer match host than this game's: choose another option."}}
	if !reflect.DeepEqual(fs, want) {
		t.Errorf("findings %+v, want %+v", fs, want)
	}
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))),
		ErrInvalidGameStateAction)
}

// On a map with its own victory the map decides: lobby.state greys the category (fixed), and a win condition chosen
// anyway (a hostile client's call, here the test aid) is a finding that refuses START.
func TestLobbyVictoryOnVictoryMap(t *testing.T) {
	withLevels(t, tunedLevel)
	race, _ := rules.Parse("victory=race10")
	ts, host, addr := rulesHostServer(t, allHostFeatures, func(s *Server) {
		s.Level, s.MapTrust, s.TestRules = tunedLevel.Name, MapTrust{All: true}, &race
	})
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	finding := rules.Finding{Category: rules.Victory, Message: "This map has its own win condition. Choose Guardian only."}
	ua := uiConnect(t, addr, a)
	ua.call(CallLobbyEnter, "")
	ua.want(catalogLine(t))
	got := mapRulesPart(t, race, tunedLevel.MapRules, true, finding)
	ua.want(stateWithRules(got, a.name))
	if !strings.Contains(got, `"fixed":[{"category":"victory","message":"This map has its own win condition."}]`) ||
		!strings.Contains(got, `"mapReason":"The map's kill race and time limit make this game unrated."`) {
		t.Errorf("the wire: %s", got)
	}
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))),
		ErrInvalidGameStateAction)
	f, _ := json.Marshal([]rules.Finding{finding})
	ua.want(`{"event":"lobby.startRefused","data":{"findings":` + string(f) + `}}`)
	ua.want(stateWithRules(got, a.name))
	// Guardian only again: no finding, the start goes ahead without a victory of the lobby's
	ua.call(CallLobbySetRules, `{"preset":"standard"}`)
	ua.want(stateWithRules(mapRulesPart(t, rules.Standard(), tunedLevel.MapRules, true), a.name))
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	if sg := hostNext[gmshost.StartGame](t, host); sg.Rules != nil || sg.Victory != nil {
		t.Errorf("startGame rules %+v, victory %+v", sg.Rules, sg.Victory)
	}
}

// A finished game with the lobby's win condition: not counted, not rated (the lobby's reason), still a history row;
// its map_rules record the victory with from "lobby" (beside a map's balance values), its rules column names it.
func TestLobbyVictoryRecord(t *testing.T) {
	for _, c := range []struct {
		name   string
		m      MapRules
		record string
	}{
		{"the built-in map", MapRules{}, `{"bindings":1,"from":"lobby","destroyed":[1,0]}`},
		{"a balance-only map", balanceLevel.MapRules, `{"bindings":1,"tuned":true,"from":"lobby","destroyed":[1,0]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			hg, buf := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{{name: "alpha", team: 0}, {name: "bravo", team: 1}})
			hg.custom.rules, _ = rules.Parse("victory=bind1")
			hg.mapRules, hg.mapHost, hg.mapHost2 = c.m, true, true
			hg.inGameAt = resultAt.Add(-20 * time.Minute)
			for _, hp := range hg.players {
				hp.atStart = true
			}
			if hg.ranked() {
				t.Error("counted for the Leaderboard")
			}
			f := gmshost.FinishGame{WinnerTeam: 1, DurationMs: 400000, Reason: ReasonBindings, Bindings: []int{1, 0}}
			hg.persist(nil, f)
			rec := hg.historyMatch(f)
			if rec.MapRules != c.record || rec.Unrated ||
				rec.Rules != `{"pick":"blind","shaperPool":"all","shop":"normal","itemPool":"all","victory":"bind1"}` {
				t.Errorf("history map rules %q, rules %q, unrated %v", rec.MapRules, rec.Rules, rec.Unrated)
			}
			hg.rate(rec)
			for _, hp := range hg.players {
				a, _ := hg.s.Store.Account(hp.userID)
				if a.History.Rating.Games != 0 || a.History.Ranked.Games != 0 {
					t.Errorf("%s: rating %+v, wins count %+v", hp.p.Name, a.History.Rating, a.History.Ranked)
				}
			}
			for _, line := range []string{"leaderboard: not counted (the lobby's win condition)",
				"not rated (the lobby's win condition: a kill race, a Binding race or a time limit)"} {
				if n := strings.Count(buf.String(), line); n != 2 {
					t.Errorf("%d lines %q:\n%s", n, line, buf)
				}
			}
		})
	}
	// a host without maprules2 never played it (START refuses it; here the record's guard): nothing recorded
	hg, _ := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{{name: "alpha", team: 0}, {name: "bravo", team: 1}})
	hg.custom.rules, _ = rules.Parse("victory=race20")
	hg.mapHost = true
	if rec := hg.mapRecordLocked(nil); rec != "" {
		t.Errorf("an unplayed lobby victory recorded %q", rec)
	}
}
