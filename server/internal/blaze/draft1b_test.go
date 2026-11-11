package blaze

import (
	"strings"
	"testing"
)

// DRAFT1b: a turn that times out takes its player's hover (how "hover"): a ban and a pick by hover; the enemy's, a
// teammate's and a spectator's hovers never steer a turn; a cleared hover skips the ban as before; in a double window a
// hover on the shaper the teammate locked meanwhile falls back to a random pick.
func TestDraftTimeoutTakesHover(t *testing.T) {
	ts, _, cs, us, start, _ := draftLobbyN(t, nil, []string{"alpha", "bravo", "charlie", "delta"}, "echo")
	wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	first := make([]DraftState, len(us))
	for i, u := range us {
		first[i] = u.draftState()
	}
	teams := first[0].Teams
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
	all := []*uiClient{t1a, t1b, t2a, t2b, spec}
	settle := func() {
		for _, u := range all {
			u.drain()
		}
	}
	// do makes u's call once every queue is empty, so the first board u hears is its answer (a team's or everyone's
	// board, or the caller's own after a refusal): the call is done before the test goes on.
	do := func(u *uiClient, call, args string) DraftState {
		t.Helper()
		settle()
		u.call(call, args)
		return u.draftState()
	}
	board := func() DraftState { // the public board after a window's timeout, as the spectator sees it
		t.Helper()
		settle()
		endStep(t, ts)
		return spec.draftState()
	}
	for i, u := range []*uiClient{t1a, t1b, t2a, t2b} {
		do(u, CallDraftRole, `{"role":"`+[]string{"gladiator", "hunter", "tactician", "predator"}[i]+`"}`)
	}
	board() // the role phase ends

	// ban 1 (team 1's first player): its own hover is banned; its teammate's, the enemy's and the spectator's don't count
	do(t1b, CallDraftHover, `{"shaper":"PC_Druid"}`)
	do(t2a, CallDraftHover, `{"shaper":"PC_Artist"}`)
	if s := do(spec, CallDraftHover, `{"shaper":"PC_Otter"}`); s.Own != nil { // refused: not a drafter
		t.Fatalf("the spectator's board %+v", s)
	}
	if s := do(t1a, CallDraftHover, `{"shaper":"PC_Alpha"}`); s.Own.Hovers[0] != "PC_Alpha" || s.Own.Hovers[1] != "PC_Druid" {
		t.Fatalf("team 1's hovers %+v", s.Own)
	}
	if s := board(); s.Turns[0].Shaper != "PC_Alpha" || s.Turns[0].How != "hover" || s.Window != 1 {
		t.Fatalf("ban 1 by hover %+v", s.Turns[0])
	}
	// ban 2 (team 2): its first player's hover, not team 1's
	do(t1a, CallDraftHover, `{"shaper":"PC_BloodMage"}`)
	if s := board(); s.Turns[1].Shaper != "PC_Artist" || s.Turns[1].How != "hover" {
		t.Fatalf("ban 2 by the enemy's hover %+v", s.Turns[1])
	}
	// ban 3 (team 1): the hover cleared, so the ban is skipped; ban 4 (team 2): its hover dropped when banned: skipped
	do(t1a, CallDraftHover, `{"shaper":""}`)
	for _, i := range []int{2, 3} {
		if s := board(); s.Turns[i].Shaper != "" || s.Turns[i].How != "timeout" {
			t.Fatalf("ban %d without a hover %+v", i+1, s.Turns[i])
		}
	}
	// pick 1 (team 1, seat 0): its hover
	do(t1a, CallDraftHover, `{"shaper":"PC_Golem"}`)
	if s := board(); s.Turns[4].Shaper != "PC_Golem" || s.Turns[4].How != "hover" {
		t.Fatalf("pick 1 by hover %+v", s.Turns[4])
	}
	// team 2's double window: both hover PC_Warden, seat 0 locks it; seat 1 falls back to a random pick
	do(t2a, CallDraftHover, `{"shaper":"PC_Warden"}`)
	do(t2b, CallDraftHover, `{"shaper":"PC_Warden"}`)
	do(t2a, CallDraftPick, `{"shaper":"PC_Warden"}`)
	s := board()
	if s.Turns[5].Shaper != "PC_Warden" || s.Turns[5].How != "chosen" || s.Turns[6].How != "timeout" ||
		s.Turns[6].Shaper == "" || s.Turns[6].Shaper == "PC_Warden" {
		t.Fatalf("the double window %+v", s.Turns[5:7])
	}
	// pick 4 (team 1, seat 1): the hover it made during the bans still stands
	if s := board(); s.Turns[7].Shaper != "PC_Druid" || s.Turns[7].How != "hover" || s.Phase != "trade" {
		t.Fatalf("pick 4 by hover %+v, phase %s", s.Turns[7], s.Phase)
	}
	log := ts.log.String()
	for _, want := range []string{
		"draft turn 1/8 (ban, team 1, user " + itoa(cs[at(1, 0)].id) + ") timed out: its hover bans PC_Alpha",
		"draft turn 2/8 (ban, team 2, user " + itoa(cs[at(2, 0)].id) + ") timed out: its hover bans PC_Artist",
		"draft turn 3/8 (ban, team 1, user " + itoa(cs[at(1, 0)].id) + ") timed out: skipped",
		"draft turn 5/8 (pick, team 1, user " + itoa(cs[at(1, 0)].id) + ") timed out: its hover picks PC_Golem",
		"draft turn 7/8 (pick, team 2, user " + itoa(cs[at(2, 1)].id) + ") timed out: a random pick: ",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q", want)
		}
	}
}
