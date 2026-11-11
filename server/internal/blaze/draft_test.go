package blaze

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/rules"
	"dawngate/server/internal/tdf"
)

// draftState reads u's events until a draft.state and returns its data (other events skipped).
func (u *uiClient) draftState() DraftState {
	u.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		u.nc.SetReadDeadline(deadline)
		line, err := u.read()
		if err != nil {
			u.t.Fatalf("no draft.state: %v", err)
		}
		var ev struct {
			Event string     `json:"event"`
			Data  DraftState `json:"data"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Event == EventDraftState {
			return ev.Data
		}
	}
}

// quietDraft expects no draft.state for a moment (other events skipped).
func (u *uiClient) quietDraft() {
	u.t.Helper()
	deadline := time.Now().Add(80 * time.Millisecond)
	for {
		u.nc.SetReadDeadline(deadline)
		line, err := u.read()
		if err != nil {
			return
		}
		if strings.HasPrefix(line, `{"event":"`+EventDraftState+`"`) {
			u.t.Errorf("unexpected draft.state %s", line)
			return
		}
	}
}

// event reads u's events until one named name and returns its line.
func (u *uiClient) event(name string) string {
	u.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		u.nc.SetReadDeadline(deadline)
		line, err := u.read()
		if err != nil {
			u.t.Fatalf("no %s: %v", name, err)
		}
		if strings.HasPrefix(line, `{"event":"`+name+`"`) {
			return line
		}
	}
}

// drain reads u's events for a moment and drops them.
func (u *uiClient) drain() {
	for {
		u.nc.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		if _, err := u.read(); err != nil {
			return
		}
	}
}

// draftLobbyN is a custom game with the draft rule (dgserver --test-rules pick=draft) and a host that takes rules: the
// first player creates it (the admin), the others join a team by the browser and are ready, the spectators join as
// spectators; everyone entered the lobby with the class. Every draft time is 10 s; setup may change the server.
func draftLobbyN(t *testing.T, setup func(s *Server), players []string, spectators ...string) (*tcpServer, *customHost,
	[]*tcpClient, []*uiClient, []byte, string) {
	t.Helper()
	dr, err := rules.Parse("pick=draft")
	if err != nil {
		t.Fatal(err)
	}
	ts, host, addr := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) {
		s.TestRules = &dr
		s.DraftTurn, s.DraftRoleTime, s.DraftTradeTime = 10*time.Second, 10*time.Second, 10*time.Second
		s.RollSeed = func() uint64 { return 5 }
		s.DraftFixedOrder = true // the tests before DRAFTSHUF expect the lobby's order; draftshuf_test.go turns it off
		if setup != nil {
			setup(s)
		}
	})
	g := golden.Load(t)
	var cs []*tcpClient
	for i, p := range append(append([]string(nil), players...), spectators...) {
		c := ts.client(p + "@example.com")
		switch {
		case i == 0:
			wantReply(t, "create", c.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
			<-host.created
			c.note(ComponentGameManager, NotifyGameSetupID)
		case i < len(players):
			wantReply(t, p+" joins", c.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
			c.settle()
		default:
			wantReply(t, p+" spectates", c.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
			c.settle()
		}
		cs = append(cs, c)
	}
	var us []*uiClient
	for _, c := range cs {
		u := uiConnect(t, addr, c)
		u.call(CallLobbyEnter, "")
		us = append(us, u)
	}
	for _, u := range us[1:len(players)] {
		u.call(CallLobbySetReady, `{"ready":true}`)
	}
	time.Sleep(50 * time.Millisecond)
	for _, u := range us {
		u.drain()
	}
	return ts, host, cs, us, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request")), addr
}

// draftLobby is draftLobbyN with A (team 1, the admin) and B (team 2).
func draftLobby(t *testing.T, setup func(s *Server)) (*tcpServer, *customHost, *tcpClient, *tcpClient, *uiClient, *uiClient, []byte, string) {
	t.Helper()
	ts, host, cs, us, start, addr := draftLobbyN(t, setup, []string{"alpha", "bravo"})
	return ts, host, cs[0], cs[1], us[0], us[1], start, addr
}

// endStep ends game 1's current draft step (the role phase, a window, the trade phase) as its timer would.
func endStep(t *testing.T, ts *tcpServer) {
	t.Helper()
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	d := hg.custom.draft
	gen := 0
	if d != nil {
		gen = d.gen
	}
	hg.mu.Unlock()
	if d == nil {
		t.Fatal("no draft runs")
	}
	hg.draftTimedOut(d, gen)
}

// The draft driven by calls (1 v 1): START holds the start and begins the role phase; a ban needs the board's phase and
// a role; only the player on turn acts, only with a free shaper of the pool; START again changes nothing; without a team
// of two there is no trade phase, and after the last pick the server starts the game itself with the rules' pick
// "draft".
func TestDraftByCalls(t *testing.T) {
	ts, host, a, b, ua, ub, start, _ := draftLobby(t, nil)
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	sa, sb := ua.draftState(), ub.draftState()
	if sa.Phase != "role" || sa.Turn != -1 || sa.Window != -1 || sa.YourTurn || sa.You == nil || sa.You.Team != 1 ||
		sa.You.Seat != 0 || sa.Own == nil || !reflect.DeepEqual(sa.Own.Roles, []string{""}) || sa.TurnMs != 10000 {
		t.Fatalf("A's first board %+v", sa)
	}
	if sb.YourTurn || sb.You == nil || sb.You.Team != 2 || sb.Left <= 0 || sb.Left > 10000 {
		t.Fatalf("B's first board %+v", sb)
	}
	if len(sa.Turns) != 6 || sa.Turns[4].Kind != "pick" || sa.Turns[4].Player != a.name || sa.Turns[5].Player != b.name ||
		sa.Turns[5].Window != 5 || sa.Teams[0][0] != a.name || sa.Teams[1][0] != b.name {
		t.Fatalf("turns %+v teams %v", sa.Turns, sa.Teams)
	}
	if len(sa.Pool) != 35 || sa.Pool[0] != (DraftShaper{ID: "PC_Alpha", Name: "Alpha"}) {
		t.Fatalf("pool %d: %+v", len(sa.Pool), sa.Pool[:1])
	}
	log := ts.log.String()
	if !strings.Contains(log, "game 1: the draft begins (DRAFT1; seed 5, 35 shapers in the pool, 6 turns in 6 windows of 10s; "+
		"the role phase 10s, the trade phase up to 10s)") ||
		!strings.Contains(log, "advanceGameState IN_GAME refused: the draft runs first") {
		t.Fatalf("the log lacks the draft's begin:\n%s", log)
	}
	// the roster stays locked: the server set RosterLockedKey "1" (the test's START didn't)
	if f := b.note(ComponentGameManager, NotifyGameAttribChangeID); !strings.Contains(string(f.Payload), "RosterLockedKey") {
		t.Errorf("B's attribute change %q", f.Payload)
	}

	// the role phase: no ban yet; A's role goes to its team alone
	ua.call(CallDraftBan, `{"shaper":"PC_Alpha"}`)
	if s := ua.draftState(); s.Phase != "role" {
		t.Fatalf("after A's ban in the role phase %+v", s)
	}
	ua.call(CallDraftRole, `{"role":"gladiator"}`)
	if s := ua.draftState(); s.Own.Roles[0] != "gladiator" {
		t.Fatalf("A's role %+v", s.Own)
	}
	ub.quietDraft()
	endStep(t, ts)
	sa, sb = ua.draftState(), ub.draftState()
	if sa.Phase != "ban" || sa.Turn != 0 || sa.Window != 0 || !sa.YourTurn || sb.YourTurn || sb.Own.Roles[0] != "" {
		t.Fatalf("the bans: %+v / %+v", sa, sb)
	}
	if !strings.Contains(ts.log.String(), "the draft's role phase is over: user "+itoa(a.id)+" gladiator, user "+itoa(b.id)+" none") {
		t.Errorf("the log lacks the role phase's end")
	}

	// B without a role, B not on turn, a pick in a ban turn, a shaper outside the pool, a malformed call: refused, the
	// caller hears the board again, the other nothing
	ub.call(CallDraftBan, `{"shaper":"PC_Alpha"}`)
	if s := ub.draftState(); s.Turn != 0 {
		t.Fatalf("after B's refused ban %+v", s)
	}
	if !strings.Contains(ts.log.String(), "Choose your role first.") {
		t.Errorf("the log lacks the role gate")
	}
	ub.call(CallDraftRole, `{"role":"hunter"}`)
	ub.draftState()
	ua.quietDraft()
	ub.call(CallDraftBan, `{"shaper":"PC_Alpha"}`)
	if s := ub.draftState(); s.Turn != 0 {
		t.Fatalf("after B's ban out of turn %+v", s)
	}
	for _, bad := range []struct{ call, args string }{{CallDraftPick, `{"shaper":"PC_Alpha"}`}, {CallDraftBan, `{"shaper":"PC_Nobody"}`}} {
		ua.call(bad.call, bad.args)
		if s := ua.draftState(); s.Turn != 0 {
			t.Fatalf("after %s %s: %+v", bad.call, bad.args, s)
		}
	}
	ua.call(CallDraftBan, `{"shaper":"PC_Alpha","x":1}`)
	ua.call(CallDraftBan, `{}`)
	ua.quiet()
	ub.quiet()
	// START again: refused, nothing changes
	wantError(t, "A starts again", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	if !strings.Contains(ts.log.String(), "advanceGameState IN_GAME refused: a draft runs (the roster stays locked)") {
		t.Errorf("the log lacks the second START")
	}
	// the rules are locked
	ua.call(CallLobbySetRules, `{"preset":"standard"}`)
	if s := ua.event(EventLobbyState); !strings.Contains(s, `"canEdit":false`) {
		t.Errorf("the rules during the draft: %s", s)
	}

	ua.call(CallDraftBan, `{"shaper":"PC_Alpha"}`)
	sa, sb = ua.draftState(), ub.draftState()
	if sa.Turn != 1 || sa.YourTurn || !sb.YourTurn || sa.Turns[0].Shaper != "PC_Alpha" || sa.Turns[0].How != "chosen" {
		t.Fatalf("after A's ban: %+v / %+v", sa, sb)
	}
	ub.call(CallDraftBan, `{"shaper":"PC_Alpha"}`) // banned already
	if s := ub.draftState(); s.Turn != 1 {
		t.Fatalf("B's second ban of PC_Alpha moved the board: %+v", s)
	}
	bans := []struct {
		u      *uiClient
		shaper string
	}{{ub, "PC_Artist"}, {ua, "PC_BloodMage"}, {ub, "PC_Creature"}}
	for _, x := range bans {
		x.u.call(CallDraftBan, `{"shaper":"`+x.shaper+`"}`)
		ua.draftState()
		ub.draftState()
	}
	ua.call(CallDraftPick, `{"shaper":"PC_Artificer"}`)
	ua.draftState()
	if s := ub.draftState(); s.Phase != "pick" || s.Turn != 5 || s.Window != 5 || !s.YourTurn {
		t.Fatalf("B's pick turn %+v", s)
	}
	ub.call(CallDraftPick, `{"shaper":"PC_Artificer"}`) // the other team's pick
	if s := ub.draftState(); s.Turn != 5 {
		t.Fatalf("B took A's pick: %+v", s)
	}
	ub.call(CallDraftPick, `{"shaper":"PC_Warden"}`)
	if s := ua.draftState(); s.Phase != "done" || s.Turn != -1 || s.Window != -1 || s.Turns[5].Shaper != "PC_Warden" ||
		s.Own.Roles[0] != "gladiator" {
		t.Fatalf("the last board %+v", s)
	}
	sg := hostNext[gmshost.StartGame](t, host)
	if sg.Rules == nil || sg.Rules.Pick != "draft" || sg.FillTeams == nil || *sg.FillTeams != 0 {
		t.Fatalf("startGame %+v", sg)
	}
	for _, x := range []*tcpClient{a, b} {
		x.note(ComponentGameManager, NotifyGameStateChangeID)
	}
	log = ts.log.String()
	for _, want := range []string{
		"draft: user " + itoa(a.id) + " (team 1) declares the role gladiator",
		"draft turn 1/6: user " + itoa(a.id) + " (team 1) bans PC_Alpha",
		"draft turn 6/6: user " + itoa(b.id) + " (team 2) picks PC_Warden",
		"the picks are over; no team has two drafters: no trade phase",
		"game 1: the draft is done: user " + itoa(a.id) + " (team 1) PC_Artificer, user " + itoa(b.id) + " (team 2) PC_Warden",
		"; roles user " + itoa(a.id) + " gladiator, user " + itoa(b.id) + " hunter",
		"game 1 starts (the draft ended): roster",
		"; rules Draft, fillTeams 0",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
	// after the start: no lobby, no draft
	ua.call(CallDraftPick, `{"shaper":"PC_Golem"}`)
	ua.quiet()
}

// Two teams of two and a spectator: roles, hovers, requests and ready marks reach the own team only (never the enemy or
// the spectator); a pick-order swap (validated, declined, expired, accepted); a double window locked seat by seat, its
// timeout a random pick for the open seat; the role gate in a pick window; the trade phase with a trade and READY ending
// it early; the start with the traded shapers.
func TestDraftTeams(t *testing.T) {
	ts, host, cs, us, start, _ := draftLobbyN(t, nil, []string{"alpha", "bravo", "charlie", "delta"}, "echo")
	wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	first := make([]DraftState, len(us))
	for i, u := range us {
		first[i] = u.draftState()
	}
	teams := first[0].Teams
	if len(teams[0]) != 2 || len(teams[1]) != 2 {
		t.Fatalf("teams %v", teams)
	}
	at := func(team, seat int) int { // the index in cs/us of the player at team (1 or 2), seat
		for i, c := range cs {
			if c.name == teams[team-1][seat] {
				return i
			}
		}
		t.Fatalf("no %s", teams[team-1][seat])
		return -1
	}
	t1a, t1b, t2a, t2b, spec := us[at(1, 0)], us[at(1, 1)], us[at(2, 0)], us[at(2, 1)], us[4]
	team1, team2 := []*uiClient{t1a, t1b}, []*uiClient{t2a, t2b}
	all := []*uiClient{t1a, t1b, t2a, t2b, spec}
	for _, team := range []int{1, 2} {
		for seat := 0; seat < 2; seat++ {
			s := first[at(team, seat)]
			if s.Phase != "role" || s.You == nil || *s.You != (DraftYou{team, seat}) || s.Own == nil ||
				!reflect.DeepEqual(s.Own.Roles, []string{"", ""}) || len(s.Own.Swaps) != 0 {
				t.Fatalf("%d.%d's first board %+v", team, seat, s)
			}
		}
	}
	if s := first[4]; s.You != nil || s.Own != nil || s.YourTurn {
		t.Fatalf("the spectator's board %+v", s)
	}
	hear := func(who []*uiClient) []DraftState {
		t.Helper()
		var out []DraftState
		for _, u := range who {
			out = append(out, u.draftState())
		}
		for _, u := range all {
			found := false
			for _, w := range who {
				found = found || u == w
			}
			if !found {
				u.quietDraft()
			}
		}
		return out
	}

	// the role phase: a hover is refused; the roles reach the own team only (a role id is lower case)
	t1a.call(CallDraftHover, `{"shaper":"PC_Golem"}`)
	hear([]*uiClient{t1a})
	t1a.call(CallDraftRole, `{"role":"gladiator"}`)
	hear(team1)
	t1b.call(CallDraftRole, `{"role":"Hunter"}`) // not an id: refused by its arguments, no answer
	hear(nil)
	t1b.call(CallDraftRole, `{"role":"hunter"}`)
	if s := hear(team1); !reflect.DeepEqual(s[0].Own.Roles, []string{"gladiator", "hunter"}) {
		t.Fatalf("team 1's roles %+v", s[0].Own)
	}
	t2a.call(CallDraftRole, `{"role":"tactician"}`)
	if s := hear(team2); !reflect.DeepEqual(s[1].Own.Roles, []string{"tactician", ""}) {
		t.Fatalf("team 2's roles %+v", s[1].Own)
	}

	// a pick-order swap in team 1: with itself, with a seat beyond the team, a second request and an answer by the asker
	// are refused; accepted, team 1's pick turns change places for everyone
	t1a.call(CallDraftSwap, `{"seat":0}`)
	hear([]*uiClient{t1a})
	t1a.call(CallDraftSwap, `{"seat":3}`)
	hear([]*uiClient{t1a})
	t1a.call(CallDraftSwap, `{"seat":1}`)
	if s := hear(team1); !reflect.DeepEqual(s[1].Own.Swaps, []DraftAsk{{From: 0, To: 1}}) {
		t.Fatalf("the request %+v", s[1].Own)
	}
	t1b.call(CallDraftSwap, `{"seat":0}`)
	hear([]*uiClient{t1b})
	t1a.call(CallDraftSwapAnswer, `{"accept":true}`)
	hear([]*uiClient{t1a})
	t1b.call(CallDraftSwapAnswer, `{"accept":true}`)
	s := hear(all)
	if s[0].Turns[4].Player != teams[0][1] || s[0].Turns[7].Player != teams[0][0] || len(s[0].Own.Swaps) != 0 || s[4].Own != nil {
		t.Fatalf("after the swap %+v", s[0])
	}
	// team 2: a declined request isn't asked again
	t2a.call(CallDraftSwap, `{"seat":1}`)
	hear(team2)
	t2b.call(CallDraftSwapAnswer, `{"accept":false}`)
	if s := hear(team2); len(s[0].Own.Swaps) != 0 {
		t.Fatalf("after the decline %+v", s[0].Own)
	}
	t2a.call(CallDraftSwap, `{"seat":1}`)
	hear([]*uiClient{t2a})
	// team 1 asks again; the request expires when one of the two is on turn
	t1a.call(CallDraftSwap, `{"seat":1}`)
	hear(team1)

	// the bans, by each team's first player
	endStep(t, ts)
	if s := hear(all); s[0].Phase != "ban" || !s[0].YourTurn || s[2].YourTurn {
		t.Fatalf("the first ban %+v", s[0])
	}
	for i, shaper := range []string{"PC_Alpha", "PC_Artist", "PC_BloodMage", "PC_Creature"} {
		[]*uiClient{t1a, t2a}[i%2].call(CallDraftBan, `{"shaper":"`+shaper+`"}`)
		s = hear(all)
	}
	if s[0].Phase != "pick" || s[0].Window != 4 || s[0].YourTurn || !s[1].YourTurn || len(s[0].Own.Swaps) != 0 {
		t.Fatalf("team 1's first pick (seat 1 after the swap) %+v", s[1])
	}
	if !strings.Contains(ts.log.String(), "draft: user "+itoa(cs[at(1, 0)].id)+"'s swap request to user "+itoa(cs[at(1, 1)].id)+" expired") {
		t.Errorf("the log lacks the expiry")
	}

	// hovers: team 1 only; a banned shaper refused; a hover on a shaper that gets picked is dropped
	t1a.call(CallDraftHover, `{"shaper":"PC_Alpha"}`)
	hear([]*uiClient{t1a})
	t1a.call(CallDraftHover, `{"shaper":"PC_Golem"}`)
	if s := hear(team1); !reflect.DeepEqual(s[1].Own.Hovers, []string{"PC_Golem", ""}) {
		t.Fatalf("the hover %+v", s[1].Own)
	}
	t1b.call(CallDraftPick, `{"shaper":"PC_Golem"}`)
	s = hear(all)
	if !reflect.DeepEqual(s[0].Own.Hovers, []string{"", ""}) || s[0].Turns[4].Shaper != "PC_Golem" || s[0].Window != 5 ||
		!s[2].YourTurn || !s[3].YourTurn || s[0].YourTurn || s[2].Turn != 5 {
		t.Fatalf("team 2's double window %+v / %+v", s[0], s[2])
	}
	if !reflect.DeepEqual(s[2].Own.Roles, []string{"tactician", ""}) {
		t.Fatalf("team 2 sees %+v", s[2].Own)
	}
	t1b.call(CallDraftHover, `{"shaper":"PC_Druid"}`) // after its own pick
	hear([]*uiClient{t1b})

	// the double window: the role gate, then seat 1 locks first, the window stays; seat 0 times out to a random pick
	t2b.call(CallDraftPick, `{"shaper":"PC_Warden"}`)
	hear([]*uiClient{t2b})
	t2b.call(CallDraftRole, `{"role":"predator"}`)
	hear(team2)
	t2b.call(CallDraftPick, `{"shaper":"PC_Warden"}`)
	s = hear(all)
	if s[2].Window != 5 || !s[2].YourTurn || s[3].YourTurn || s[0].Turns[6].Shaper != "PC_Warden" || s[0].Turns[5].How != "" {
		t.Fatalf("after one lock %+v / %+v", s[2], s[3])
	}
	t2b.call(CallDraftPick, `{"shaper":"PC_Druid"}`)
	hear([]*uiClient{t2b})
	endStep(t, ts)
	s = hear(all)
	if s[0].Turns[5].How != "timeout" || s[0].Turns[5].Shaper == "" || s[0].Window != 6 || !s[0].YourTurn {
		t.Fatalf("after the window's timeout %+v", s[0])
	}
	t1a.call(CallDraftPick, `{"shaper":"PC_Otter"}`)
	s = hear(all)
	if s[0].Phase != "trade" || s[0].Turn != -1 || s[0].Window != -1 || s[0].TurnMs != 10000 || s[0].Left <= 0 || s[0].YourTurn {
		t.Fatalf("the trade phase %+v", s[0])
	}

	// the trade phase: no swap any more; a trade asked and accepted; READY from everyone ends it early
	t2a.call(CallDraftSwap, `{"seat":1}`)
	hear([]*uiClient{t2a})
	t1a.call(CallDraftTrade, `{"seat":1}`)
	if s := hear(team1); !reflect.DeepEqual(s[0].Own.Trades, []DraftAsk{{From: 0, To: 1}}) {
		t.Fatalf("the trade request %+v", s[0].Own)
	}
	t1b.call(CallDraftTradeAnswer, `{"accept":true}`)
	s = hear(all)
	if s[0].Turns[4].Shaper != "PC_Otter" || s[0].Turns[7].Shaper != "PC_Golem" || len(s[0].Own.Trades) != 0 {
		t.Fatalf("after the trade %+v", s[0].Turns)
	}
	for i, u := range []*uiClient{t1a, t1b, t2a} {
		u.call(CallDraftReady, "")
		mates := team1
		if i == 2 {
			mates = team2
		}
		hear(mates)
	}
	t2b.call(CallDraftReady, `{}`)
	if s := hear(all); s[0].Phase != "done" || !reflect.DeepEqual(s[0].Own.Ready, []bool{true, true}) {
		t.Fatalf("the end %+v", s[0])
	}
	hostNext[gmshost.StartGame](t, host)
	log := ts.log.String()
	for _, want := range []string{
		"draft: user " + itoa(cs[at(1, 1)].id) + " accepts user " + itoa(cs[at(1, 0)].id) + "'s swap: their pick turns changed places",
		"draft: user " + itoa(cs[at(2, 1)].id) + " declines user " + itoa(cs[at(2, 0)].id) + "'s swap",
		"the picks are over; the trade phase begins (up to 10s)",
		"draft: user " + itoa(cs[at(1, 1)].id) + " accepts user " + itoa(cs[at(1, 0)].id) + "'s trade: user " +
			itoa(cs[at(1, 0)].id) + " plays PC_Golem, user " + itoa(cs[at(1, 1)].id) + " PC_Otter",
		"every drafter is ready: the trade phase ends early",
		"user " + itoa(cs[at(1, 0)].id) + " (team 1) PC_Golem, user " + itoa(cs[at(1, 1)].id) + " (team 1) PC_Otter",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

// A drafter's leave cancels the draft: the board says why, the roster is unlocked, every ready mark reset; the lobby
// stays and can start again (a new draft, the empty team without turns).
func TestDraftLeaverCancels(t *testing.T) {
	ts, _, a, b, ua, ub, start, _ := draftLobby(t, nil)
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	ua.draftState()
	ub.draftState()
	ua.call(CallDraftRole, `{"role":"hunter"}`)
	ua.draftState()
	endStep(t, ts)
	ua.draftState()
	ua.call(CallDraftBan, `{"shaper":"PC_Alpha"}`)
	ua.draftState()
	a.takeAll(ComponentGameManager, NotifyGameAttribChangeID, 50*time.Millisecond)
	leave := tdf.NewWriter().Int("CNTX", 0).Int("GID", 1).Int("PID", b.id).Int("REAS", 6).Bytes()
	wantReply(t, "B leaves", b.gm(CmdGameManagerRemovePlayer, leave))
	s := ua.draftState()
	if s.Phase != "cancelled" || s.Reason != b.name+" left the game." || s.Turn != -1 || s.Turns[0].Shaper != "PC_Alpha" {
		t.Fatalf("the cancelled board %+v", s)
	}
	if f := a.note(ComponentGameManager, NotifyGameAttribChangeID); !strings.Contains(string(f.Payload), "RosterLockedKey") {
		t.Errorf("A's unlock %q", f.Payload)
	}
	ua.event(EventLobbyStartRefused)
	if !strings.Contains(ts.log.String(), "game 1: the draft is cancelled: "+b.name+" left the game.; the roster unlocked") {
		t.Errorf("the log lacks the cancel:\n%s", ts.log.String())
	}
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	locked, draft := attrValue(hg.game.Attrs, AttrRosterLocked), hg.custom.draft
	hg.mu.Unlock()
	if locked != "0" || draft != nil {
		t.Fatalf("after the cancel: RosterLockedKey %q, draft %v", locked, draft)
	}
	// the lobby stays: START again begins a new draft, A alone (two bans and one pick), with the role phase
	ua.drain()
	wantError(t, "A starts again", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	if s := ua.draftState(); s.Phase != "role" || len(s.Turns) != rules.DraftBansPerTeam+1 || len(s.Teams[1]) != 0 ||
		s.Own.Roles[0] != "" {
		t.Fatalf("the second draft %+v", s)
	}
}

// A leaver cancels in the role phase and in the trade phase too.
func TestDraftLeaverAnyPhase(t *testing.T) {
	for _, phase := range []string{"role", "trade"} {
		t.Run(phase, func(t *testing.T) {
			ts, _, cs, us, start, _ := draftLobbyN(t, func(s *Server) {
				if phase == "trade" {
					s.DraftRoleTime, s.DraftTurn = 5*time.Millisecond, 5*time.Millisecond
				}
			}, []string{"alpha", "bravo", "charlie", "delta"})
			wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
			for s := us[0].draftState(); s.Phase != phase; s = us[0].draftState() {
			}
			leave := tdf.NewWriter().Int("CNTX", 0).Int("GID", 1).Int("PID", cs[1].id).Int("REAS", 6).Bytes()
			wantReply(t, "B leaves", cs[1].gm(CmdGameManagerRemovePlayer, leave))
			if s := us[0].draftState(); s.Phase != "cancelled" || s.Reason != cs[1].name+" left the game." {
				t.Fatalf("the cancelled board %+v", s)
			}
			if !strings.Contains(ts.log.String(), "the draft is cancelled: "+cs[1].name+" left the game.") {
				t.Errorf("the log lacks the cancel")
			}
		})
	}
}

// Every step timed out (1 v 1): no roles, the bans skipped, the picks random, no trade phase, and the game starts.
func TestDraftTimeouts(t *testing.T) {
	ts, host, a, _, ua, _, start, _ := draftLobby(t, func(s *Server) {
		s.DraftTurn, s.DraftRoleTime = 20*time.Millisecond, 20*time.Millisecond
	})
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	sg := hostNext[gmshost.StartGame](t, host)
	if sg.Rules == nil || sg.Rules.Pick != "draft" {
		t.Fatalf("startGame %+v", sg)
	}
	var last DraftState
	for last.Phase != "done" {
		last = ua.draftState()
	}
	for i, turn := range last.Turns {
		if turn.How != "timeout" || (turn.Kind == "ban") != (turn.Shaper == "") {
			t.Errorf("turn %d %+v", i, turn)
		}
	}
	if last.Turns[4].Shaper == last.Turns[5].Shaper {
		t.Errorf("both picks %s", last.Turns[4].Shaper)
	}
	log := ts.log.String()
	for _, want := range []string{"the draft's role phase is over: user " + itoa(a.id) + " none",
		"draft turn 1/6 (ban, team 1, user " + itoa(a.id) + ") timed out: skipped",
		"draft turn 5/6 (pick, team 1, user " + itoa(a.id) + ") timed out: a random pick: PC_",
		"; roles user " + itoa(a.id) + " none"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
}

// Two teams of two, every step timed out: the double windows' random picks, the trade phase ends by its time.
func TestDraftTradeEndsByTime(t *testing.T) {
	ts, host, cs, us, start, _ := draftLobbyN(t, func(s *Server) {
		s.DraftTurn, s.DraftRoleTime, s.DraftTradeTime = 10*time.Millisecond, 10*time.Millisecond, 40*time.Millisecond
	}, []string{"alpha", "bravo", "charlie", "delta"})
	wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	hostNext[gmshost.StartGame](t, host)
	var last DraftState
	sawTrade := false
	for last.Phase != "done" {
		last = us[0].draftState()
		sawTrade = sawTrade || last.Phase == "trade"
	}
	seen := map[string]bool{}
	for _, turn := range last.Turns {
		if turn.Kind == "pick" && (turn.Shaper == "" || seen[turn.Shaper]) {
			t.Errorf("picks %+v", last.Turns)
		}
		seen[turn.Shaper] = true
	}
	if !sawTrade || !strings.Contains(ts.log.String(), "the draft's trade phase is over (its time)") {
		t.Errorf("no trade phase seen (%v):\n%s", sawTrade, ts.log.String())
	}
}

// --test-draft (1 v 1): the server declares the roles, acts for the player on turn (and tries once out of turn,
// refused), so a draft runs without the board.
func TestDraftTestAid(t *testing.T) {
	ts, host, a, b, ua, _, start, _ := draftLobby(t, func(s *Server) {
		s.TestDraft, s.DraftRoleTime = 5*time.Millisecond, 30*time.Millisecond
	})
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	hostNext[gmshost.StartGame](t, host)
	var last DraftState
	for last.Phase != "done" {
		last = ua.draftState()
	}
	// bans from the pool's end, picks from its start
	if last.Turns[0].Shaper != "PC_WarlockKnight" || last.Turns[4].Shaper != "PC_Alpha" || last.Turns[5].Shaper != "PC_Artificer" {
		t.Errorf("the aid's board %+v", last.Turns)
	}
	log := ts.log.String()
	for _, want := range []string{
		"draft (--test-draft, a test aid): user " + itoa(a.id) + " declares the role gladiator (ok)",
		"draft (--test-draft, a test aid): user " + itoa(b.id) + "'s ban out of turn: ban \"PC_WarlockKnight\": not this player's turn",
		"; roles user " + itoa(a.id) + " gladiator, user " + itoa(b.id) + " gladiator",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
}

// --test-draft with two teams of two: one swap and one trade, both accepted, every seat of a double window, everyone
// ready (the trade phase ends early).
func TestDraftTestAidTeams(t *testing.T) {
	ts, host, cs, us, start, _ := draftLobbyN(t, func(s *Server) {
		s.TestDraft, s.DraftRoleTime = 5*time.Millisecond, 30*time.Millisecond
	}, []string{"alpha", "bravo", "charlie", "delta"})
	wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	hostNext[gmshost.StartGame](t, host)
	var last DraftState
	for last.Phase != "done" {
		last = us[0].draftState()
	}
	for _, turn := range last.Turns {
		if turn.How != "chosen" && turn.Kind == "pick" {
			t.Errorf("a pick not by the aid %+v", turn)
		}
	}
	log := ts.log.String()
	for _, want := range []string{"for a swap (ok)", "for a trade (ok)", "accepts (ok)", "'s swap: their pick turns changed places",
		"every drafter is ready: the trade phase ends early"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "a test aid): refused") || strings.Contains(log, "ready refused") {
		t.Errorf("the aid was refused:\n%s", log)
	}
}

// While the draft runs: a newcomer may only spectate (and sees the board, without a seat or the teams' private parts),
// a team change is refused.
func TestDraftSeatsFixed(t *testing.T) {
	ts, _, a, b, ua, _, start, addr := draftLobby(t, nil)
	g := golden.Load(t)
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	ua.draftState()
	c := ts.client("charlie@example.com")
	wantError(t, "C joins a team", c.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))), ErrGameInProgress)
	wantReply(t, "C spectates", c.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
	c.settle()
	uc := uiConnect(t, addr, c)
	uc.call(CallLobbyEnter, "")
	if s := uc.draftState(); s.You != nil || s.Own != nil || s.YourTurn || s.Phase != "role" {
		t.Fatalf("the spectator's board %+v", s)
	}
	for _, call := range []struct{ name, args string }{{CallDraftBan, `{"shaper":"PC_Alpha"}`},
		{CallDraftRole, `{"role":"hunter"}`}, {CallDraftSwap, `{"seat":0}`}} {
		uc.call(call.name, call.args)
		if s := uc.draftState(); s.Own != nil {
			t.Fatalf("the spectator's %s %+v", call.name, s)
		}
	}
	ua.quietDraft()
	swap := tdf.NewWriter().Int("GID", 1).List("LGAM", tdf.TypeStruct, []*tdf.Writer{
		tdf.NewWriter().Int("PID", b.id).Int("TIDX", 0)}).Bytes()
	wantError(t, "A moves B", a.gm(CmdGameManagerSwapPlayersTeam, swap), ErrInvalidGameStateAction)
}

// The draft's times: a turn 45 s (30 s + 50 %), the role phase 20 s, the trade phase 30 s.
func TestDraftDefaults(t *testing.T) {
	s := &Server{}
	if s.draftTurn() != 45*time.Second || s.draftRoleTime() != 20*time.Second || s.draftTradeTime() != 30*time.Second {
		t.Fatalf("defaults %v %v %v", s.draftTurn(), s.draftRoleTime(), s.draftTradeTime())
	}
}

// The event's wire: field names and order (the documented example: team 1's Ash in team 1's last pick window of a
// 2 v 1, after Ash and Elm swapped their pick turns; the pool shortened).
func TestDraftStateJSON(t *testing.T) {
	st := DraftState{Phase: "pick", Turn: 6, Window: 6, Left: 31350, TurnMs: 45000, Teams: [2][]string{{"Ash", "Elm"}, {"Birch"}},
		Pool: []DraftShaper{{ID: "PC_Artificer", Name: "Petrus", CatalogID: 273770958,
			Portrait: "Heroes/Artificer/default/ui/Textures/artificer_portrait.dds",
			Card:     "Heroes/Artificer/default/ui/Textures/artificer_purchase.dds"}, {ID: "PC_Old", Name: "Old"}},
		Turns: []DraftStateTurn{{Kind: "ban", Team: 1, Player: "Ash", Shaper: "PC_Alpha", How: "chosen", Window: 0},
			{Kind: "ban", Team: 2, Player: "Birch", How: "timeout", Window: 1},
			{Kind: "ban", Team: 1, Player: "Ash", Shaper: "PC_Golem", How: "chosen", Window: 2},
			{Kind: "ban", Team: 2, Player: "Birch", How: "timeout", Window: 3},
			{Kind: "pick", Team: 1, Player: "Elm", Shaper: "PC_Old", How: "chosen", Window: 4},
			{Kind: "pick", Team: 2, Player: "Birch", Shaper: "PC_Warden", How: "timeout", Window: 5},
			{Kind: "pick", Team: 1, Player: "Ash", Window: 6}},
		You: &DraftYou{Team: 1, Seat: 0}, YourTurn: true,
		Own: &DraftOwn{Roles: []string{"gladiator", "hunter"}, Hovers: []string{"PC_Artificer", ""}, Ready: []bool{false, false},
			Swaps: []DraftAsk{}, Trades: []DraftAsk{}}}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"phase":"pick","turn":6,"window":6,"left":31350,"turnMs":45000,"teams":[["Ash","Elm"],["Birch"]],` +
		`"pool":[{"id":"PC_Artificer","name":"Petrus","catalogID":273770958,` +
		`"portrait":"Heroes/Artificer/default/ui/Textures/artificer_portrait.dds",` +
		`"card":"Heroes/Artificer/default/ui/Textures/artificer_purchase.dds"},{"id":"PC_Old","name":"Old","catalogID":0}],` +
		`"turns":[{"kind":"ban","team":1,"player":"Ash","shaper":"PC_Alpha","how":"chosen","window":0},` +
		`{"kind":"ban","team":2,"player":"Birch","shaper":"","how":"timeout","window":1},` +
		`{"kind":"ban","team":1,"player":"Ash","shaper":"PC_Golem","how":"chosen","window":2},` +
		`{"kind":"ban","team":2,"player":"Birch","shaper":"","how":"timeout","window":3},` +
		`{"kind":"pick","team":1,"player":"Elm","shaper":"PC_Old","how":"chosen","window":4},` +
		`{"kind":"pick","team":2,"player":"Birch","shaper":"PC_Warden","how":"timeout","window":5},` +
		`{"kind":"pick","team":1,"player":"Ash","shaper":"","how":"","window":6}],` +
		`"you":{"team":1,"seat":0},"yourTurn":true,` +
		`"own":{"roles":["gladiator","hunter"],"hovers":["PC_Artificer",""],"ready":[false,false],"swaps":[],"trades":[]}}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

// The draft's arguments: strict, each call's fields only.
func TestParseDraftArgs(t *testing.T) {
	if s, err := parseDraftArgs(json.RawMessage(`{"shaper":"PC_Alpha"}`)); err != nil || s != "PC_Alpha" {
		t.Fatalf("%q %v", s, err)
	}
	for _, bad := range []string{`{}`, `{"shaper":""}`, `{"shaper":1}`, `{"shaper":"a","b":2}`, `{"shaper":"a"} {}`, `[]`,
		`{"shaper":"` + strings.Repeat("x", 300) + `"}`} {
		if _, err := parseDraftArgs(json.RawMessage(bad)); err == nil {
			t.Errorf("%s taken", bad)
		}
	}
	if s, err := parseDraftHover(json.RawMessage(`{"shaper":""}`)); err != nil || s != "" {
		t.Errorf("hover clear %q %v", s, err)
	}
	if _, err := parseDraftHover(json.RawMessage(`{}`)); err == nil {
		t.Error("hover {} taken")
	}
	for _, r := range rules.DraftRoles {
		if got, err := parseDraftRole(json.RawMessage(`{"role":"` + r.ID + `"}`)); err != nil || got != r.ID {
			t.Errorf("role %s: %q %v", r.ID, got, err)
		}
	}
	for _, bad := range []string{`{"role":"Gladiator"}`, `{"role":""}`, `{"role":"mage"}`, `{"role":1}`, `{}`} {
		if _, err := parseDraftRole(json.RawMessage(bad)); err == nil {
			t.Errorf("role %s taken", bad)
		}
	}
	if n, err := parseDraftSeat(json.RawMessage(`{"seat":4}`)); err != nil || n != 4 {
		t.Errorf("seat 4: %d %v", n, err)
	}
	for _, bad := range []string{`{"seat":5}`, `{"seat":-1}`, `{"seat":1.5}`, `{"seat":"1"}`, `{}`, `{"seat":1,"x":0}`} {
		if _, err := parseDraftSeat(json.RawMessage(bad)); err == nil {
			t.Errorf("seat %s taken", bad)
		}
	}
	if ok, err := parseDraftAnswer(json.RawMessage(`{"accept":false}`)); err != nil || ok {
		t.Errorf("answer false: %v %v", ok, err)
	}
	for _, bad := range []string{`{}`, `{"accept":1}`, `{"accept":true,"seat":1}`} {
		if _, err := parseDraftAnswer(json.RawMessage(bad)); err == nil {
			t.Errorf("answer %s taken", bad)
		}
	}
	for _, good := range []string{``, `{}`} {
		if err := parseDraftNone(json.RawMessage(good)); err != nil {
			t.Errorf("ready %q: %v", good, err)
		}
	}
	if err := parseDraftNone(json.RawMessage(`{"ready":true}`)); err == nil {
		t.Error("ready with a field taken")
	}
}

// The pool's entries carry the content's titles and pictures (the board's cards and slots); a shaper without them has
// none, so the board draws its name only.
func TestDraftPoolPictures(t *testing.T) {
	alpha := content.Shaper{Name: "PC_Alpha", CatalogID: 1, Title: "Fenmore",
		Portrait: "Heroes/Alpha/default/ui/Textures/alpha_portrait.dds", Card: "Heroes/Alpha/default/ui/Textures/alpha_purchase.dds"}
	c := &content.Content{Items: []content.Item{
		{CatalogID: 1, Name: "alpha", Type: "shaper", ItemType: content.ItemShaper, Asset: "alpha.Noun", Title: "Fenmore", Shaper: "PC_Alpha"},
		{CatalogID: 2, Name: "golem", Type: "shaper", ItemType: content.ItemShaper, Asset: "golem.Noun", Title: "Faris", Shaper: "PC_Golem"}},
		Shapers: []content.Shaper{alpha, {Name: "PC_Golem", CatalogID: 2, Title: "Faris"}}}
	s := &Server{Content: c, Policy: DefaultPolicy()}
	want := []DraftShaper{{ID: "PC_Alpha", Name: "Fenmore", CatalogID: 1, Portrait: alpha.Portrait, Card: alpha.Card},
		{ID: "PC_Golem", Name: "Faris", CatalogID: 2}}
	if pool := s.draftPool("all"); !reflect.DeepEqual(pool, want) {
		t.Fatalf("pool %+v", pool)
	}
}
