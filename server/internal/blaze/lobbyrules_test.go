package blaze

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/rules"
	"dawngate/server/internal/store"
)

// rulesHostServer is a server with match hosts that take rules (gmshost.FeatureRules in their hello), the UI channel
// and setup applied; rules changes without the gap. Shaper select is on (All Random and the pools need it, RULES3),
// without content: the roster lines carry no lists.
func rulesHostServer(t *testing.T, features []string, setup func(s *Server)) (*tcpServer, *customHost, string) {
	t.Helper()
	ts, host := newCustomHostServerWith(t, features, func(s *Server) {
		s.ShaperSelect.On = true
		if setup != nil {
			setup(s)
		}
	})
	addr := attachUI(t, ts)
	ts.s.rulesGap = 0
	return ts, host, addr
}

// rulesPart is lobby.state's rules part as the server writes it.
func rulesPart(c rules.Combination, bots, canEdit, host bool, findings ...rules.Finding) string {
	lr := LobbyRules{Options: c.Normal(), Text: rules.Describe(c), Bots: bots, Findings: findings, CanEdit: canEdit, Host: host}
	if lr.Findings == nil {
		lr.Findings = []rules.Finding{}
	}
	if p, ok := rules.PresetOf(c); ok {
		lr.Preset = p.ID
	}
	b, _ := json.Marshal(lr)
	return string(b)
}

// stateWithRules is the lobby.state line with these players (lobbyStateLine's form) and the rules part.
func stateWithRules(rulesJSON string, players ...string) string {
	line := lobbyStateLine(players...)
	return strings.TrimSuffix(line, "]}}") + `],"rules":` + rulesJSON + "}}"
}

func catalogLine(t *testing.T) string {
	b, err := json.Marshal(rules.View())
	if err != nil {
		t.Fatal(err)
	}
	return `{"event":"lobby.rulesCatalog","data":` + string(b) + "}"
}

func aram(t *testing.T) rules.Combination {
	c, err := rules.Parse("aram")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The lobby's rules over the UI channel (dgserver --test-rules aram, a host that takes rules): the catalog and the rules
// at lobby.enter, canEdit for the admin only; a non-admin's call, an unknown option and a malformed call change nothing;
// the admin's change resets every ready mark and its findings refuse the start; the start then carries the rules and the
// bots setting; after the start the rules are locked.
func TestCustomGameRulesLobby(t *testing.T) {
	ar := aram(t)
	ts, host, addr := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) { s.TestRules = &ar })
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	if cg := <-host.created; cg.FillTeams != 0 {
		t.Errorf("createGame fillTeams %d", cg.FillTeams)
	}
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	b.settle()
	log := ts.log.String()
	if !strings.Contains(log, `"[ARAM] `) || !strings.Contains(log, "game 1: rules All Random, Shop on Death (--test-rules, a test aid)") {
		t.Errorf("the create's log lacks the prefix or the rules:\n%s", log)
	}
	ua, ub := uiConnect(t, addr, a), uiConnect(t, addr, b)
	ua.call(CallLobbyEnter, "")
	ua.want(catalogLine(t))
	ua.want(stateWithRules(rulesPart(ar, false, true, true), a.name, b.name))
	ub.call(CallLobbyEnter, "")
	ub.want(catalogLine(t))
	ua.want(stateWithRules(rulesPart(ar, false, true, true), a.name, b.name+" 0"))
	ub.want(stateWithRules(rulesPart(ar, false, false, true), a.name, b.name+" 0"))

	// B isn't the admin: refused, B hears the state again, A nothing
	ub.call(CallLobbySetRules, `{"preset":"standard"}`)
	ub.want(stateWithRules(rulesPart(ar, false, false, true), a.name, b.name+" 0"))
	ua.quiet()
	// a hostile admin's calls: an unknown option, an unknown field, nothing to set: no change, no event
	for _, bad := range []string{`{"rules":{"pick":"chaos"}}`, `{"preset":"aram","speed":9}`, `{}`, `{"rules":{"pick":"random"},"preset":"aram"}`} {
		ua.call(CallLobbySetRules, bad)
	}
	ua.quiet()

	// B ready; the admin turns the bots on: B's mark is reset, and Shop on Death can't be played with bots
	ub.call(CallLobbySetReady, `{"ready":true}`)
	ua.want(stateWithRules(rulesPart(ar, false, true, true), a.name, b.name+" 1"))
	ub.want(stateWithRules(rulesPart(ar, false, false, true), a.name, b.name+" 1"))
	ua.call(CallLobbySetRules, `{"bots":true}`)
	shop := rules.Finding{Category: rules.Shop, Message: "Shop on Death can't be played with bots yet: turn the bots off or choose another option."}
	ua.want(stateWithRules(rulesPart(ar, true, true, true, shop), a.name, b.name+" 0"))
	ub.want(stateWithRules(rulesPart(ar, true, false, true, shop), a.name, b.name+" 0"))
	if !strings.Contains(ts.log.String(), "game 1: rules set by admin "+itoa(a.id)+": All Random, Shop on Death, bots true; every ready mark reset; findings: shop: ") {
		t.Errorf("log lacks the change:\n%s", ts.log.String())
	}
	ub.call(CallLobbySetReady, `{"ready":true}`)
	ua.want(stateWithRules(rulesPart(ar, true, true, true, shop), a.name, b.name+" 1"))
	ub.want(stateWithRules(rulesPart(ar, true, false, true, shop), a.name, b.name+" 1"))

	// the start with the finding: refused, the admin told why
	for _, x := range []*tcpClient{a, b} {
		x.takeAll(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 50*time.Millisecond)
	}
	start := golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))
	wantError(t, "A starts with a finding", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	f, _ := json.Marshal([]rules.Finding{shop})
	ua.want(`{"event":"lobby.startRefused","data":{"findings":` + string(f) + `}}`)
	ua.want(stateWithRules(rulesPart(ar, true, true, true, shop), a.name, b.name+" 1"))
	ub.want(stateWithRules(rulesPart(ar, true, false, true, shop), a.name, b.name+" 1"))

	// bots off again: no finding; B ready again; the start carries the rules and fillTeams 0
	ua.call(CallLobbySetRules, `{"bots":false}`)
	ua.want(stateWithRules(rulesPart(ar, false, true, true), a.name, b.name+" 0"))
	ub.want(stateWithRules(rulesPart(ar, false, false, true), a.name, b.name+" 0"))
	ub.call(CallLobbySetReady, `{"ready":true}`)
	ua.want(stateWithRules(rulesPart(ar, false, true, true), a.name, b.name+" 1"))
	ub.want(stateWithRules(rulesPart(ar, false, false, true), a.name, b.name+" 1"))
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start))
	sg := hostNext[gmshost.StartGame](t, host)
	if sg.Rules == nil || *sg.Rules != (gmshost.Rules{Pick: "random", ShaperPool: "all", Shop: "onDeath", ItemPool: "all"}) ||
		sg.FillTeams == nil || *sg.FillTeams != 0 {
		t.Errorf("startGame rules %+v, fillTeams %v", sg.Rules, sg.FillTeams)
	}
	if !strings.Contains(ts.log.String(), "; rules All Random, Shop on Death, fillTeams 0") {
		t.Errorf("the start's log lacks the rules")
	}
	// started: the rules are locked (the call isn't even in a lobby any more)
	ua.call(CallLobbySetRules, `{"preset":"standard"}`)
	ua.quiet()
}

// The startGame line with the rules as the shared example has it (docs/data/host_control_examples.jsonl), and the
// --custom-bots game whose admin turned the bots off: fillTeams 0, so the host plans none.
func TestCustomGameRulesStartLine(t *testing.T) {
	ar := aram(t)
	ts, host, _ := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) { s.TestRules = &ar })
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	sg := hostNext[gmshost.StartGame](t, host)
	sg.Roster[0].Name = "alpha" // the example's name (the test's persona is the login's)
	wantLine(t, sg, `{"t":"startGame","roster":[{"user":2000000001,"name":"alpha","index":0,"team":1,"noun":"","bot":false}],`+
		`"rules":{"pick":"random","shaperPool":"all","shop":"onDeath","itemPool":"all"},"fillTeams":0}`)
	// the match begins (the host's first inGame): one System chat line with the rules, once
	hg := ts.s.hosted.get(1)
	hg.announceRules()
	f := a.note(ComponentMessaging, NotifyMessageID)
	if !strings.Contains(string(f.Payload), "Rules: All Random, Shop on Death") {
		t.Errorf("the rules line: %q", f.Payload)
	}
	hg.announceRules()
	a.none(ComponentMessaging, NotifyMessageID, 0)
	if !strings.Contains(ts.log.String(), `game 1: the match's rules: "Rules: All Random, Shop on Death" to 1 player(s) as a System chat line`) {
		t.Errorf("log lacks the announcement")
	}
	// the same text again on this connection (the next game) gets a number: the client shows a record's text once
	hg.mu.Lock()
	hg.custom.announced = false
	hg.mu.Unlock()
	hg.announceRules()
	if f := a.note(ComponentMessaging, NotifyMessageID); !strings.Contains(string(f.Payload), "Rules: All Random, Shop on Death (2)") {
		t.Errorf("the repeated line: %q", f.Payload)
	}
}

// RULES1b: the line goes out RulesNoteDelay after the host's first inGame, not at it (the client drops a System line
// that comes before its in-match chat window is up), and a second inGame starts no second timer.
func TestCustomGameRulesLineWaits(t *testing.T) {
	ar := aram(t)
	ts, host, _ := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) {
		s.TestRules = &ar
		s.RulesNoteDelay = 300 * time.Millisecond
	})
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	hostNext[gmshost.StartGame](t, host)
	hg := ts.s.hosted.get(1)
	hg.announceRulesLater()
	hg.announceRulesLater()
	a.none(ComponentMessaging, NotifyMessageID, 100*time.Millisecond)
	if f := a.note(ComponentMessaging, NotifyMessageID); !strings.Contains(string(f.Payload), "Rules: All Random, Shop on Death") {
		t.Errorf("the rules line: %q", f.Payload)
	}
	a.none(ComponentMessaging, NotifyMessageID, 500*time.Millisecond)
	if !strings.Contains(ts.log.String(), "game 1: the match begins; its rules go out as a System chat line in 300ms") {
		t.Errorf("log lacks the timer's line")
	}
	if n := strings.Count(ts.log.String(), "the match's rules: "); n != 1 {
		t.Errorf("%d rules lines, want 1", n)
	}
}

// A --custom-bots game (createGame fillTeams 5) whose admin turns the bots off in the lobby: the start's fillTeams is 0,
// and the browser's name loses [Bots].
func TestCustomGameRulesBotsOff(t *testing.T) {
	ts, host, addr := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) { s.CustomBots = true })
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	if cg := <-host.created; cg.FillTeams != 5 {
		t.Fatalf("createGame fillTeams %d", cg.FillTeams)
	}
	a.note(ComponentGameManager, NotifyGameSetupID)
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	before := hg.game.Name
	hg.mu.Unlock()
	if !strings.HasPrefix(before, BotFillPrefix) {
		t.Errorf("name %q without %q", before, BotFillPrefix)
	}
	ua := uiConnect(t, addr, a)
	ua.call(CallLobbyEnter, "")
	ua.want(catalogLine(t))
	ua.want(stateWithRules(rulesPart(rules.Standard(), true, true, true), a.name))
	ua.call(CallLobbySetRules, `{"bots":false}`)
	ua.want(stateWithRules(rulesPart(rules.Standard(), false, true, true), a.name))
	hg.mu.Lock()
	after := hg.game.Name
	hg.mu.Unlock()
	if after != strings.TrimPrefix(before, BotFillPrefix) {
		t.Errorf("name %q after the bots went off (was %q)", after, before)
	}
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	sg := hostNext[gmshost.StartGame](t, host)
	if sg.Rules != nil || sg.FillTeams == nil || *sg.FillTeams != 0 {
		t.Errorf("startGame rules %+v, fillTeams %v", sg.Rules, sg.FillTeams)
	}
	hg.mu.Lock()
	fill := hg.fillTeams
	hg.mu.Unlock()
	if fill != 0 {
		t.Errorf("the game's fillTeams %d after the start", fill)
	}
}

// The window before the game is announced (its host's hello and gameReady; the live check's first try hit it with a
// script): lobby.enter and lobby.setRules are refused as "no announced lobby" and change nothing, so no call is judged
// against a host that hasn't spoken; once announced, the same calls work.
func TestCustomGameRulesBeforeAnnounced(t *testing.T) {
	ts, host, addr := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) { s.CustomBots = true })
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	hg.custom.ready = false // as between the create and the host's gameReady
	hg.mu.Unlock()
	ua := uiConnect(t, addr, a)
	ua.call(CallLobbyEnter, "")
	ua.call(CallLobbySetRules, `{"bots":false}`)
	ua.quiet()
	hg.mu.Lock()
	bots := hg.custom.bots
	hg.custom.ready = true
	hg.mu.Unlock()
	if !bots {
		t.Fatal("a call before the announcement changed the bots")
	}
	if !strings.Contains(ts.log.String(), "lobby.setRules: no announced lobby (none, or its host is starting)") {
		t.Errorf("log lacks the refusal")
	}
	ua.call(CallLobbyEnter, "")
	ua.want(catalogLine(t))
	ua.want(stateWithRules(rulesPart(rules.Standard(), true, true, true), a.name))
	ua.call(CallLobbySetRules, `{"bots":false}`)
	ua.want(stateWithRules(rulesPart(rules.Standard(), false, true, true), a.name))
}

// A host without the feature (an older build): the lobby says so; rules and a bots change are refused, rules set anyway
// (--test-rules) are a finding that refuses the start; back to the standard game, the start is today's startGame
// (no rules, no fillTeams).
func TestCustomGameRulesOldHost(t *testing.T) {
	ar := aram(t)
	ts, host, addr := rulesHostServer(t, nil, func(s *Server) { s.TestRules = &ar })
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	old := rules.Finding{Category: rules.FindingHost, Message: "This game's match host is an older build that can't play rules: only the standard game is possible."}
	ua := uiConnect(t, addr, a)
	ua.call(CallLobbyEnter, "")
	ua.want(catalogLine(t))
	ua.want(stateWithRules(rulesPart(ar, false, false, false, old), a.name))
	start := golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))
	a.takeAll(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 50*time.Millisecond)
	wantError(t, "A starts with rules on an old host", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	f, _ := json.Marshal([]rules.Finding{old})
	ua.want(`{"event":"lobby.startRefused","data":{"findings":` + string(f) + `}}`)
	ua.want(stateWithRules(rulesPart(ar, false, false, false, old), a.name))
	// rules and bots refused; the standard game allowed
	ua.call(CallLobbySetRules, `{"preset":"allRandom"}`)
	ua.want(stateWithRules(rulesPart(ar, false, false, false, old), a.name))
	ua.call(CallLobbySetRules, `{"preset":"standard","bots":true}`)
	ua.want(stateWithRules(rulesPart(ar, false, false, false, old), a.name))
	ua.call(CallLobbySetRules, `{"preset":"standard"}`)
	ua.want(stateWithRules(rulesPart(rules.Standard(), false, false, false), a.name))
	if !strings.Contains(ts.log.String(), "lobby.setRules: the match host is an older build without rules") {
		t.Errorf("log lacks the refusal")
	}
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start))
	sg := hostNext[gmshost.StartGame](t, host)
	if sg.Rules != nil || sg.FillTeams != nil {
		t.Errorf("an old host's startGame with rules %+v, fillTeams %v", sg.Rules, sg.FillTeams)
	}
}

// A practice group's game and a lobby without a match host have no rules: lobby.state leaves them out, and the call is
// refused (TestReadyCheck's lines, without a host, show the first).
func TestRulesOffWithoutHost(t *testing.T) {
	ts := newTCPServer(t, nil)
	addr := attachUI(t, ts)
	ts.s.rulesGap = 0
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	ua := uiConnect(t, addr, a)
	ua.call(CallLobbyEnter, "")
	ua.want(lobbyStateLine(a.name))
	ua.call(CallLobbySetRules, `{"preset":"aram"}`)
	ua.want(lobbyStateLine(a.name))
	if !strings.Contains(ts.log.String(), "a game without rules") {
		t.Errorf("log lacks the refusal")
	}
}

// lobby.setRules' arguments.
func TestParseSetRules(t *testing.T) {
	c, bots, err := parseSetRules([]byte(`{"preset":"aram","bots":false}`))
	if err != nil || c == nil || rules.Describe(*c) != "All Random, Shop on Death" || bots == nil || *bots {
		t.Errorf("preset and bots: %v %v %v", c, bots, err)
	}
	c, bots, err = parseSetRules([]byte(`{"rules":{"shop":"onDeath"}}`))
	if err != nil || c == nil || rules.Describe(*c) != "Shop on Death" || bots != nil {
		t.Errorf("rules: %v %v %v", c, bots, err)
	}
	if c, bots, err = parseSetRules([]byte(`{"bots":true}`)); err != nil || c != nil || bots == nil || !*bots {
		t.Errorf("bots: %v %v %v", c, bots, err)
	}
	for _, bad := range []string{`{}`, `{"preset":"nope"}`, `{"rules":{"pick":"chaos"}}`, `{"rules":"aram"}`, `{"bots":"yes"}`,
		`{"x":1}`, `[]`, `{"preset":"aram"} {}`, `{"rules":{"pick":"random"},"preset":"aram"}`, `{"rules":{"pick":"` + strings.Repeat("x", 3000) + `"}}`} {
		if c, bots, err := parseSetRules([]byte(bad)); err == nil {
			t.Errorf("%.40s: %v %v, want an error", bad, c, bots)
		}
	}
}

// A finished custom game with rules: not counted on the Leaderboard, not rated, its history row has the rules.
func TestCustomGameRulesUnrated(t *testing.T) {
	hg, buf := ratingGame(t, gmshost.ModeCustom, "2", true, []resultSeat{{name: "alpha", team: 0}, {name: "bravo", team: 1}})
	hg.custom.rules = aram(t)
	hg.inGameAt = resultAt.Add(-20 * time.Minute)
	for _, hp := range hg.players {
		hp.atStart = true
	}
	if hg.ranked() {
		t.Error("a custom game with rules counts for the Leaderboard")
	}
	f := gmshost.FinishGame{WinnerTeam: 1, DurationMs: 1200000, Reason: "guardian"}
	hg.persist(nil, f)
	rec := hg.historyMatch(f)
	if rec.Rules != `{"pick":"random","shaperPool":"all","shop":"onDeath","itemPool":"all"}` {
		t.Errorf("history rules %q", rec.Rules)
	}
	hg.rate(rec)
	for _, hp := range hg.players {
		a, _ := hg.s.Store.Account(hp.userID)
		if a.History.Rating != (store.Rating{}) || a.History.Ranked.Games != 0 {
			t.Errorf("%s: rating %+v, Leaderboard %+v", hp.p.Name, a.History.Rating, a.History.Ranked)
		}
	}
	for _, line := range []string{"leaderboard: not counted (a custom game with rules)", "rating: not rated (a custom game with rules)"} {
		if n := strings.Count(buf.String(), line); n != 2 {
			t.Errorf("%d lines %q:\n%s", n, line, buf)
		}
	}
	// the standard game counts as before
	hg.custom.rules = rules.Standard()
	if !hg.ranked() || hg.historyMatch(f).Rules != "" {
		t.Error("the standard game: not ranked, or rules in its history")
	}
}
