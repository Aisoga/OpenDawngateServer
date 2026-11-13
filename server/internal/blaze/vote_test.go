package blaze

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/rules"
	"dawngate/server/internal/tdf"
)

// voteLobbyN is draftLobbyN for Army or Clone Pick (pick): a host that names rules and rules2, the vote 10 s and its
// result 10 s (the tests end the steps with endStep unless setup shortens them).
func voteLobbyN(t *testing.T, pick string, setup func(s *Server), players []string, spectators ...string) (*tcpServer,
	*customHost, []*tcpClient, []*uiClient, []byte) {
	t.Helper()
	c, err := rules.Parse("pick=" + pick)
	if err != nil {
		t.Fatal(err)
	}
	ts, host, addr := rulesHostServer(t, []string{gmshost.FeatureRules, gmshost.FeatureRules2}, func(s *Server) {
		s.TestRules = &c
		s.VoteTime, s.VoteShowTime = 10*time.Second, 10*time.Second
		s.RollSeed = func() uint64 { return 5 }
		if setup != nil {
			setup(s)
		}
	})
	g := golden.Load(t)
	var cs []*tcpClient
	for i, p := range append(append([]string(nil), players...), spectators...) {
		cl := ts.client(p + "@example.com")
		switch {
		case i == 0:
			wantReply(t, "create", cl.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
			<-host.created
			cl.note(ComponentGameManager, NotifyGameSetupID)
		case i < len(players):
			wantReply(t, p+" joins", cl.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
			cl.settle()
		default:
			wantReply(t, p+" spectates", cl.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
			cl.settle()
		}
		cs = append(cs, cl)
	}
	var us []*uiClient
	for _, cl := range cs {
		u := uiConnect(t, addr, cl)
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
	return ts, host, cs, us, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))
}

// seatsOf orders cs and us by the vote's teams (the first board's Teams): the index in cs of team (1 or 2), seat.
func seatsOf(t *testing.T, cs []*tcpClient, teams [2][]string) func(team, seat int) int {
	return func(team, seat int) int {
		t.Helper()
		for i, c := range cs {
			if c.name == teams[team-1][seat] {
				return i
			}
		}
		t.Fatalf("no %s", teams[team-1][seat])
		return -1
	}
}

// noStartGame expects no startGame at the host for wait (other messages dropped).
func noStartGame(t *testing.T, h *customHost, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case m := <-h.msgs:
			if sg, ok := m.(gmshost.StartGame); ok {
				t.Fatalf("the game started: %+v", sg)
			}
		case <-deadline:
			return
		}
	}
}

// leaveGame is player c's leave of game 1.
func leaveGame(t *testing.T, c *tcpClient) {
	t.Helper()
	leave := tdf.NewWriter().Int("CNTX", 0).Int("GID", 1).Int("PID", c.id).Int("REAS", 6).Bytes()
	wantReply(t, c.name+" leaves", c.gm(CmdGameManagerRemovePlayer, leave))
}

// Army Pick, two teams of two and a spectator: START holds the start and begins the vote; a vote reaches the voter's
// team only (never the enemy or the spectator); the same vote again tells nobody; a vote outside the pool, the draft's
// calls and a lock without a vote are refused; a lock is final; once everyone has locked the vote ends at once, each
// team gets one of its own votes, the result is shown, and the game starts with pick "army".
func TestArmyVote(t *testing.T) {
	ts, host, cs, us, start := voteLobbyN(t, rules.PickArmy, nil, []string{"alpha", "bravo", "charlie", "delta"}, "echo")
	wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	first := make([]DraftState, len(us))
	for i, u := range us {
		first[i] = u.draftState()
	}
	teams := first[0].Teams
	if len(teams[0]) != 2 || len(teams[1]) != 2 {
		t.Fatalf("teams %v", teams)
	}
	at := seatsOf(t, cs, teams)
	t1a, t1b, t2a, t2b, spec := us[at(1, 0)], us[at(1, 1)], us[at(2, 0)], us[at(2, 1)], us[4]
	for _, team := range []int{1, 2} {
		for seat := 0; seat < 2; seat++ {
			s := first[at(team, seat)]
			if s.Phase != "vote" || s.Turn != -1 || s.Window != -1 || len(s.Turns) != 0 || !s.YourTurn || s.TurnMs != 10000 ||
				s.Left <= 0 || s.You == nil || *s.You != (DraftYou{team, seat}) || s.Own == nil ||
				!reflect.DeepEqual(s.Own.Votes, []string{"", ""}) || !reflect.DeepEqual(s.Own.Locked, []bool{false, false}) ||
				s.Vote == nil || s.Vote.Kind != "army" || s.Vote.Votes != nil || s.Vote.Locked != nil ||
				s.Vote.Drawn != [2]string{} || len(s.Pool) != 35 {
				t.Fatalf("%d.%d's first board %+v (vote %+v)", team, seat, s, s.Vote)
			}
		}
	}
	if s := first[4]; s.Phase != "vote" || s.You != nil || s.Own != nil || s.YourTurn || s.Vote == nil || s.Vote.Votes != nil {
		t.Fatalf("the spectator's first board %+v", s)
	}
	if log := ts.log.String(); !strings.Contains(log, "game 1: the vote begins (ARMY1; Army Pick, seed 5, 35 shapers in the "+
		"pool, 10s, the drawn shaper shown 10s): team 1 [") || !strings.Contains(log, "the roster stays locked (RosterLockedKey") {
		t.Fatalf("the log lacks the vote's begin:\n%s", log)
	}

	// a vote: the team hears it, the enemy and the spectator don't
	t1a.call(CallDraftVote, `{"shaper":"PC_Gunman"}`)
	for _, u := range []*uiClient{t1a, t1b} {
		if s := u.draftState(); !reflect.DeepEqual(s.Own.Votes, []string{"PC_Gunman", ""}) || s.Vote.Votes != nil {
			t.Fatalf("team 1 after the vote: %+v", s.Own)
		}
	}
	for _, u := range []*uiClient{t2a, t2b, spec} {
		u.quietDraft()
	}
	// the same vote again: nobody hears anything
	t1a.call(CallDraftVote, `{"shaper":"PC_Gunman"}`)
	for _, u := range []*uiClient{t1a, t1b, t2a, t2b, spec} {
		u.quietDraft()
	}
	// refused: the caller hears the board again, nothing changed; the others hear nothing
	for _, bad := range []struct{ call, args string }{{CallDraftVote, `{"shaper":"PC_Nobody"}`},
		{CallDraftBan, `{"shaper":"PC_Alpha"}`}, {CallDraftPick, `{"shaper":"PC_Alpha"}`}, {CallDraftRole, `{"role":"hunter"}`},
		{CallDraftHover, `{"shaper":"PC_Alpha"}`}, {CallDraftSwap, `{"seat":1}`}, {CallDraftSwapAnswer, `{"accept":true}`},
		{CallDraftTrade, `{"seat":1}`}, {CallDraftReady, `{}`}} {
		t1a.call(bad.call, bad.args)
		if s := t1a.draftState(); s.Phase != "vote" || !reflect.DeepEqual(s.Own.Votes, []string{"PC_Gunman", ""}) ||
			s.Own.Roles[0] != "" {
			t.Fatalf("after %s %s: %+v", bad.call, bad.args, s)
		}
		t1b.quietDraft()
	}
	// malformed: no answer at all
	t1a.call(CallDraftVote, `{"shaper":"PC_Alpha","x":1}`)
	t1a.call(CallDraftVote, `{}`)
	t1a.call(CallDraftLock, `{"now":true}`)
	t1a.quietDraft()
	// a lock without a vote is refused; a vote can change until the lock
	t1b.call(CallDraftLock, "")
	if s := t1b.draftState(); s.Own.Locked[1] {
		t.Fatalf("t1b locked without a vote: %+v", s.Own)
	}
	t1b.call(CallDraftVote, `{"shaper":"PC_Warden"}`)
	t1a.draftState()
	t1b.draftState()
	t1b.call(CallDraftVote, `{"shaper":"PC_Alpha"}`)
	t1a.draftState()
	t1b.draftState()
	t1a.call(CallDraftLock, `{}`)
	for _, u := range []*uiClient{t1a, t1b} {
		s := u.draftState()
		if !reflect.DeepEqual(s.Own.Locked, []bool{true, false}) || !reflect.DeepEqual(s.Own.Votes, []string{"PC_Gunman", "PC_Alpha"}) {
			t.Fatalf("team 1 after t1a's lock: %+v", s.Own)
		}
		if u == t1a && s.YourTurn || u == t1b && !s.YourTurn {
			t.Errorf("yourTurn after the lock: %+v", s)
		}
	}
	t2a.quietDraft()
	// locked: the vote can't change; a second lock tells nobody
	t1a.call(CallDraftVote, `{"shaper":"PC_Warden"}`)
	if s := t1a.draftState(); s.Own.Votes[0] != "PC_Gunman" {
		t.Fatalf("t1a's vote changed after its lock: %+v", s.Own)
	}
	t1a.call(CallDraftLock, "")
	t1a.quietDraft()
	t1b.quietDraft()
	// START again: refused, nothing changes
	wantError(t, "A starts again", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)

	// everyone else votes and locks: team 2 both PC_Warden (both teams may hold a shaper the other votes for)
	t1b.call(CallDraftLock, "")
	t1a.draftState()
	t1b.draftState()
	for _, u := range []*uiClient{t2a, t2b} {
		u.call(CallDraftVote, `{"shaper":"PC_Warden"}`)
		t2a.draftState()
		t2b.draftState()
	}
	t2a.call(CallDraftLock, "")
	t2a.draftState()
	t2b.draftState()
	t2b.call(CallDraftLock, "")
	var done []DraftState
	for _, u := range []*uiClient{t1a, t1b, t2a, t2b, spec} {
		done = append(done, u.draftState())
	}
	for i, s := range done {
		if s.Phase != "done" || s.YourTurn || s.Vote == nil || s.Vote.Drawn[1] != "PC_Warden" ||
			(s.Vote.Drawn[0] != "PC_Gunman" && s.Vote.Drawn[0] != "PC_Alpha") || s.Vote.How != [2]string{"vote", "vote"} ||
			s.TurnMs != 10000 || s.Left <= 0 {
			t.Fatalf("%d's result %+v (vote %+v)", i, s, s.Vote)
		}
	}
	if done[4].Vote.Votes != nil || done[4].Own != nil {
		t.Errorf("the spectator saw Army's votes: %+v", done[4])
	}
	drawn := done[0].Vote.Drawn
	// the result is shown; the game starts when its step ends
	noStartGame(t, host, 50*time.Millisecond)
	endStep(t, ts)
	sg := hostNext[gmshost.StartGame](t, host)
	if sg.Rules == nil || sg.Rules.Pick != "army" {
		t.Fatalf("startGame %+v", sg)
	}
	if s := t1a.draftState(); s.Phase != "done" || s.Vote.Drawn != drawn || s.Left != 0 {
		t.Fatalf("the last board %+v", s)
	}
	log := ts.log.String()
	for _, want := range []string{
		"vote: user " + itoa(cs[at(1, 0)].id) + " (team 1) locks PC_Gunman (1/4)",
		"vote: user " + itoa(cs[at(2, 1)].id) + " (team 2) locks PC_Warden (4/4)",
		"the vote is over (every voter has locked: the vote ends early): team 1 draws " + drawn[0] +
			" (votes PC_Alpha 1, PC_Gunman 1); team 2 draws PC_Warden (votes PC_Warden 2); shown 10s, then the game starts",
		"game 1: the vote is done: user " + itoa(cs[at(1, 0)].id) + " (team 1) " + drawn[0],
		"user " + itoa(cs[at(2, 1)].id) + " (team 2) PC_Warden",
		"game 1 starts (the vote ended): roster",
		"Vote for a shaper first.",
		"role: no roles in a vote",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// Army Pick by its time: a team without votes draws at random from the pool, the other team one of its votes; the
// result's own time ends in the start.
func TestArmyVoteTimeout(t *testing.T) {
	ts, host, cs, us, start := voteLobbyN(t, rules.PickArmy, func(s *Server) {
		s.VoteTime, s.VoteShowTime = 60*time.Millisecond, 30*time.Millisecond
	}, []string{"alpha", "bravo"})
	wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	us[0].draftState()
	us[0].call(CallDraftVote, `{"shaper":"PC_Otter"}`)
	if s := us[0].draftState(); s.Own.Votes[0] != "PC_Otter" {
		t.Fatalf("A's vote %+v", s.Own)
	}
	sg := hostNext[gmshost.StartGame](t, host)
	if sg.Rules == nil || sg.Rules.Pick != "army" {
		t.Fatalf("startGame %+v", sg)
	}
	var res DraftState
	for res.Phase != "done" {
		res = us[1].draftState()
	}
	if res.Vote.Drawn[0] != "PC_Otter" || res.Vote.Drawn[1] == "" || res.Vote.How != [2]string{"vote", "random"} {
		t.Fatalf("the result %+v", res.Vote)
	}
	if log := ts.log.String(); !strings.Contains(log, "the vote is over (its time): team 1 draws PC_Otter (votes PC_Otter 1); "+
		"team 2 draws "+res.Vote.Drawn[1]+" at random from the pool (no votes)") {
		t.Errorf("the log lacks the draw:\n%s", log)
	}
}

// Clone Pick: every vote and lock reaches everyone (the spectator too, in Vote.Votes); the vote ends by its time, early
// once everyone has locked, or by a leaver (in the vote and while the result is shown); one draw for everyone.
func TestCloneVote(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		ts, host, cs, us, start := voteLobbyN(t, rules.PickClone, nil, []string{"alpha", "bravo", "charlie"}, "echo")
		wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
		var first DraftState
		for _, u := range us {
			first = u.draftState()
		}
		if first.Vote == nil || first.Vote.Kind != "clone" || first.Vote.Votes == nil || first.Own != nil ||
			len(first.Vote.Votes[0])+len(first.Vote.Votes[1]) != 3 {
			t.Fatalf("the spectator's first board %+v", first)
		}
		at := seatsOf(t, cs, first.Teams)
		us[0].call(CallDraftVote, `{"shaper":"PC_Gunman"}`)
		us[1].call(CallDraftVote, `{"shaper":"PC_Warden"}`)
		team, seat := 1, 0
		if at(1, 0) != 0 {
			team, seat = 2, 0
		}
		var last DraftState
		for _, u := range us {
			for i := 0; i < 2; i++ {
				last = u.draftState()
			}
		}
		if (*last.Vote.Votes)[team-1][seat] != "PC_Gunman" {
			t.Fatalf("the spectator's view of the votes %+v", *last.Vote.Votes)
		}
		us[0].call(CallDraftLock, "")
		for _, u := range us {
			last = u.draftState()
		}
		if !(*last.Vote.Locked)[team-1][seat] {
			t.Fatalf("the spectator's view of the locks %+v", *last.Vote.Locked)
		}
		endStep(t, ts)
		var res []DraftState
		for _, u := range us {
			res = append(res, u.draftState())
		}
		d := res[0].Vote.Drawn[0]
		if d != "PC_Gunman" && d != "PC_Warden" {
			t.Fatalf("drew %q", d)
		}
		for i, s := range res {
			if s.Phase != "done" || s.Vote.Drawn != [2]string{d, d} || s.Vote.How != [2]string{"vote", "vote"} {
				t.Fatalf("%d's result %+v", i, s.Vote)
			}
		}
		endStep(t, ts)
		sg := hostNext[gmshost.StartGame](t, host)
		if sg.Rules == nil || sg.Rules.Pick != "clone" {
			t.Fatalf("startGame %+v", sg)
		}
		log := ts.log.String()
		for _, want := range []string{"the vote begins (ARMY1; Clone Pick, seed 5",
			"the vote is over (its time): everyone draws " + d + " (votes PC_Gunman 1, PC_Warden 1)",
			"game 1 starts (the vote ended)"} {
			if !strings.Contains(log, want) {
				t.Errorf("log lacks %q:\n%s", want, log)
			}
		}
		for _, c := range cs[:3] {
			if !strings.Contains(log, "user "+itoa(c.id)+" (team ") {
				t.Errorf("the vote's end lacks user %d", c.id)
			}
		}
	})
	t.Run("no votes", func(t *testing.T) {
		ts, _, cs, us, start := voteLobbyN(t, rules.PickClone, nil, []string{"alpha", "bravo"})
		wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
		us[0].draftState()
		endStep(t, ts)
		s := us[0].draftState()
		if s.Vote.Drawn[0] == "" || s.Vote.Drawn[0] != s.Vote.Drawn[1] || s.Vote.How != [2]string{"random", "random"} {
			t.Fatalf("the result %+v", s.Vote)
		}
	})
	t.Run("locks end it early", func(t *testing.T) {
		ts, host, cs, us, start := voteLobbyN(t, rules.PickClone, func(s *Server) { s.VoteShowTime = 30 * time.Millisecond },
			[]string{"alpha", "bravo"})
		wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
		us[0].draftState()
		us[1].draftState()
		for _, u := range us {
			u.call(CallDraftVote, `{"shaper":"PC_Artificer"}`)
			u.call(CallDraftLock, "")
		}
		var res DraftState
		for res.Phase != "done" {
			res = us[1].draftState()
		}
		if res.Vote.Drawn != [2]string{"PC_Artificer", "PC_Artificer"} || res.Left <= 0 {
			t.Fatalf("the result %+v", res)
		}
		sg := hostNext[gmshost.StartGame](t, host)
		if sg.Rules == nil || sg.Rules.Pick != "clone" {
			t.Fatalf("startGame %+v", sg)
		}
		if !strings.Contains(ts.log.String(), "every voter has locked: the vote ends early): everyone draws PC_Artificer "+
			"(votes PC_Artificer 2)") {
			t.Errorf("the log lacks the early end:\n%s", ts.log.String())
		}
	})
	for _, stage := range []string{"vote", "result"} {
		t.Run("a leaver cancels in the "+stage, func(t *testing.T) {
			ts, host, cs, us, start := voteLobbyN(t, rules.PickClone, nil, []string{"alpha", "bravo"})
			wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
			us[0].draftState()
			us[0].call(CallDraftVote, `{"shaper":"PC_Gunman"}`)
			us[0].draftState()
			if stage == "result" {
				endStep(t, ts)
				if s := us[0].draftState(); s.Phase != "done" {
					t.Fatalf("no result %+v", s)
				}
			}
			cs[0].takeAll(ComponentGameManager, NotifyGameAttribChangeID, 50*time.Millisecond)
			leaveGame(t, cs[1])
			s := us[0].draftState()
			if s.Phase != "cancelled" || s.Reason != cs[1].name+" left the game." || s.YourTurn {
				t.Fatalf("the cancelled board %+v", s)
			}
			if f := cs[0].note(ComponentGameManager, NotifyGameAttribChangeID); !strings.Contains(string(f.Payload), "RosterLockedKey") {
				t.Errorf("A's unlock %q", f.Payload)
			}
			hg := ts.s.hosted.get(1)
			hg.mu.Lock()
			locked, draft := attrValue(hg.game.Attrs, AttrRosterLocked), hg.custom.draft
			hg.mu.Unlock()
			if locked != "0" || draft != nil {
				t.Fatalf("after the cancel: RosterLockedKey %q, draft %v", locked, draft)
			}
			noStartGame(t, host, 50*time.Millisecond)
			// the lobby stays: START again begins a new vote, without the old votes
			us[0].drain()
			wantError(t, "A starts again", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
			if s := us[0].draftState(); s.Phase != "vote" || s.Own.Votes[0] != "" || len(s.Teams[1]) != 0 {
				t.Fatalf("the second vote %+v", s)
			}
		})
	}
}

// Army Pick with a leaver: the vote is cancelled in Army Pick too, the team's votes gone.
func TestArmyVoteLeaverCancels(t *testing.T) {
	ts, _, cs, us, start := voteLobbyN(t, rules.PickArmy, nil, []string{"alpha", "bravo"})
	wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	us[0].draftState()
	us[1].draftState()
	leaveGame(t, cs[0])
	if s := us[1].draftState(); s.Phase != "cancelled" || s.Reason != cs[0].name+" left the game." {
		t.Fatalf("the cancelled board %+v", s)
	}
	if !strings.Contains(ts.log.String(), "the draft is cancelled: "+cs[0].name+" left the game.") {
		t.Errorf("the log lacks the cancel")
	}
}

// --test-draft in a vote: every voter votes and locks through the calls, the vote ends early, the game starts.
func TestVoteTestAid(t *testing.T) {
	for _, pick := range []string{rules.PickArmy, rules.PickClone} {
		t.Run(pick, func(t *testing.T) {
			ts, host, cs, us, start := voteLobbyN(t, pick, func(s *Server) {
				s.TestDraft, s.VoteShowTime = 5*time.Millisecond, 20*time.Millisecond
			}, []string{"alpha", "bravo"})
			wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
			sg := hostNext[gmshost.StartGame](t, host)
			if sg.Rules == nil || sg.Rules.Pick != pick {
				t.Fatalf("startGame %+v", sg)
			}
			var last DraftState
			for last.Phase != "done" {
				last = us[0].draftState()
			}
			want := [2]string{"PC_Alpha", "PC_Artificer"} // seat 0 of team 1 the pool's first, of team 2 its second
			if pick == rules.PickClone && (last.Vote.Drawn[0] != last.Vote.Drawn[1] ||
				last.Vote.Drawn[0] != want[0] && last.Vote.Drawn[0] != want[1]) || pick == rules.PickArmy && last.Vote.Drawn != want {
				t.Fatalf("drawn %v", last.Vote.Drawn)
			}
			log := ts.log.String()
			for _, w := range []string{"vote (--test-draft, a test aid): user " + itoa(cs[0].id) + " votes PC_Alpha (ok) and locks (ok)",
				"every voter has locked: the vote ends early"} {
				if !strings.Contains(log, w) {
					t.Errorf("log lacks %q:\n%s", w, log)
				}
			}
		})
	}
}

// Army and Clone Pick need the host feature rules2: a host naming only rules gets the finding, and START is refused.
func TestVoteNeedsRules2(t *testing.T) {
	for _, pick := range []string{rules.PickArmy, rules.PickClone} {
		t.Run(pick, func(t *testing.T) {
			c, _ := rules.Parse("pick=" + pick)
			ts, host, _ := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) { s.TestRules = &c })
			g := golden.Load(t)
			a := ts.client("alpha@example.com")
			wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
			<-host.created
			a.note(ComponentGameManager, NotifyGameSetupID)
			hg := ts.s.hosted.get(1)
			hg.mu.Lock()
			fs := hg.rulesFindingsLocked()
			hg.mu.Unlock()
			name := map[string]string{rules.PickArmy: "Army Pick", rules.PickClone: "Clone Pick"}[pick]
			want := []rules.Finding{{Category: rules.Pick,
				Message: name + " needs a newer match host than this game's: choose another option."}}
			if !reflect.DeepEqual(fs, want) {
				t.Errorf("findings %+v, want %+v", fs, want)
			}
			wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))),
				ErrInvalidGameStateAction)
			hg.mu.Lock()
			d := hg.custom.draft
			hg.mu.Unlock()
			if d != nil {
				t.Errorf("a vote began on a host without rules2")
			}
		})
	}
}

// The vote's wire: the fields' names and order (the documented examples: Army's voter of team 1 in a 2 v 1, Clone's
// spectator after the draw).
func TestVoteStateJSON(t *testing.T) {
	army := DraftState{Phase: "vote", Turn: -1, Window: -1, Left: 21500, TurnMs: 30000, Teams: [2][]string{{"Ash", "Elm"}, {"Birch"}},
		Pool: []DraftShaper{{ID: "PC_Old", Name: "Old"}}, Turns: []DraftStateTurn{}, You: &DraftYou{Team: 1, Seat: 0},
		YourTurn: true, Own: &DraftOwn{Roles: []string{"", ""}, Hovers: []string{"", ""}, Ready: []bool{false, false},
			Swaps: []DraftAsk{}, Trades: []DraftAsk{}, Votes: []string{"PC_Old", ""}, Locked: []bool{false, false}},
		Vote: &DraftVote{Kind: "army"}}
	votes, locked := [2][]string{{"PC_Old", ""}, {"PC_Old"}}, [2][]bool{{true, false}, {true}}
	clone := DraftState{Phase: "done", Turn: -1, Window: -1, Left: 2950, TurnMs: 3000, Teams: [2][]string{{"Ash", "Elm"}, {"Birch"}},
		Pool: []DraftShaper{{ID: "PC_Old", Name: "Old"}}, Turns: []DraftStateTurn{},
		Vote: &DraftVote{Kind: "clone", Votes: &votes, Locked: &locked, Drawn: [2]string{"PC_Old", "PC_Old"}, How: [2]string{"vote", "vote"}}}
	for _, c := range []struct {
		st   DraftState
		want string
	}{
		{army, `{"phase":"vote","turn":-1,"window":-1,"left":21500,"turnMs":30000,"teams":[["Ash","Elm"],["Birch"]],` +
			`"pool":[{"id":"PC_Old","name":"Old","catalogID":0}],"turns":[],"you":{"team":1,"seat":0},"yourTurn":true,` +
			`"own":{"roles":["",""],"hovers":["",""],"ready":[false,false],"swaps":[],"trades":[],"votes":["PC_Old",""],` +
			`"locked":[false,false]},"vote":{"kind":"army","drawn":["",""],"how":["",""]}}`},
		{clone, `{"phase":"done","turn":-1,"window":-1,"left":2950,"turnMs":3000,"teams":[["Ash","Elm"],["Birch"]],` +
			`"pool":[{"id":"PC_Old","name":"Old","catalogID":0}],"turns":[],"yourTurn":false,` +
			`"vote":{"kind":"clone","votes":[["PC_Old",""],["PC_Old"]],"locked":[[true,false],[true]],` +
			`"drawn":["PC_Old","PC_Old"],"how":["vote","vote"]}}`},
	} {
		b, err := json.Marshal(c.st)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.want {
			t.Errorf("got  %s\nwant %s", b, c.want)
		}
	}
}

// The vote's times: 30 s, the result 3 s.
func TestVoteDefaults(t *testing.T) {
	s := &Server{}
	if s.voteTime() != 30*time.Second || s.voteShowTime() != 3*time.Second {
		t.Fatalf("defaults %v %v", s.voteTime(), s.voteShowTime())
	}
}

// A vote's drawn shapers go into startGame as the draft's: every human's list is its team's drawn shaper alone, also
// several humans of one team the same (Army, Clone), marked available; no role.
func TestApplyPickRulesArmy(t *testing.T) {
	var log strings.Builder
	c := pickContent()
	s := &Server{Log: logging.New(&log), Content: c}
	sg := gmshost.StartGame{Roster: pickRoster(c, [][2]int{{0, 1}, {5, 1}, {6, 2}}), Rules: &gmshost.Rules{Pick: "army",
		ShaperPool: "all", Shop: "normal", ItemPool: "all"}}
	s.applyPickRules(1, &sg, map[int64]draftedSeat{2000000001: {Shaper: "PC_Warden"}, 2000000002: {Shaper: "PC_Warden"},
		2000000003: {Shaper: "PC_Artificer"}})
	for _, want := range []struct {
		i    int
		noun string
	}{{0, "Warden.Noun"}, {1, "Warden.Noun"}, {2, "Artificer.Noun"}} {
		e := sg.Roster[want.i]
		if len(e.Shapers) != 1 || e.Shapers[0].Noun != want.noun || e.Shapers[0].Flags&gmshost.ShaperAvailable == 0 || e.Role != "" {
			t.Errorf("user %d's list %+v (role %q)", e.User, e.Shapers, e.Role)
		}
	}
	if !strings.Contains(log.String(), "user 2000000002 (index 5, team 1) -> Warden.Noun") {
		t.Errorf("log %s", log.String())
	}
}
