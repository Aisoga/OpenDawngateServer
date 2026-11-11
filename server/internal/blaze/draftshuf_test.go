package blaze

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// shuffleDraftSeats over many seeds: each team's seats are a permutation of its drafters, the bans stay with the
// lobby's top player (Banner is its seat), both teams start in a run of seeds and the pick orders change; without the
// shuffle the lobby's order stays, team 1 starts and the generator isn't touched (the timeouts' random picks as before).
func TestDraftShuffleSeats(t *testing.T) {
	lobby := [2][]int64{{11, 12, 13, 14, 15}, {21, 22, 23}}
	var starts [2]int
	moved := 0
	for seed := uint64(1); seed <= 200; seed++ {
		seats := [2][]int64{slices.Clone(lobby[0]), slices.Clone(lobby[1])}
		o, banners, how := shuffleDraftSeats(&seats, draftRNG(seed), true)
		if how != "shuffled" || banners != "user 11 (team 1) and user 21 (team 2)" {
			t.Fatalf("seed %d: %q, %q", seed, how, banners)
		}
		for team := range seats {
			if got := slices.Sorted(slices.Values(seats[team])); !slices.Equal(got, lobby[team]) {
				t.Fatalf("seed %d: team %d's seats %v aren't its drafters", seed, team+1, seats[team])
			}
			if seats[team][o.Banner[team]] != lobby[team][0] {
				t.Fatalf("seed %d: team %d bans from seat %d (%v)", seed, team+1, o.Banner[team], seats[team])
			}
			if !slices.Equal(seats[team], lobby[team]) {
				moved++
			}
		}
		starts[o.First]++
	}
	if starts[0] < 60 || starts[1] < 60 || moved < 300 {
		t.Fatalf("over 200 seeds: team 1 starts %d, team 2 %d; %d teams' orders changed", starts[0], starts[1], moved)
	}

	seats := [2][]int64{slices.Clone(lobby[0]), slices.Clone(lobby[1])}
	r := draftRNG(7)
	o, banners, how := shuffleDraftSeats(&seats, r, false)
	if o.First != 0 || o.Banner != [2]int{} || !reflect.DeepEqual(seats, lobby) || how != "the lobby's" ||
		banners != "user 11 (team 1) and user 21 (team 2)" {
		t.Fatalf("without the shuffle: %+v %v %q %q", o, seats, how, banners)
	}
	if r.Uint64() != draftRNG(7).Uint64() {
		t.Fatal("without the shuffle the generator was used")
	}
	// a team without drafters: nothing to shuffle, no banner
	none := [2][]int64{{11}, nil}
	if o, banners, _ := shuffleDraftSeats(&none, draftRNG(1), true); o.Banner != [2]int{} || banners != "user 11 (team 1)" {
		t.Fatalf("1 v 0: %+v %q", o, banners)
	}
}

// spiritFirstSeed is a seed whose 2 v 2 shuffle swaps team 1's two drafters, keeps team 2's and lets team 2 start.
func spiritFirstSeed(t *testing.T) uint64 {
	for seed := uint64(1); seed < 1000; seed++ {
		seats := [2][]int64{{1, 2}, {3, 4}}
		if o, _, _ := shuffleDraftSeats(&seats, draftRNG(seed), true); o.First == 1 && seats[0][0] == 2 && seats[1][0] == 3 {
			return seed
		}
	}
	t.Fatal("no seed")
	return 0
}

// A 2 v 2 lobby with the shuffle on and a seed where Spirit starts: the board's teams in the shuffled pick order, team
// 2's top player bans first, team 1's top player (now its second seat) bans for it, team 2 picks first, team 1's two
// picks in the double window; a pick-order swap after the shuffle; the begin's log line names the order and the start.
func TestDraftShuffleLobby(t *testing.T) {
	seed := spiritFirstSeed(t)
	ts, _, cs, us, start, _ := draftLobbyN(t, func(s *Server) {
		s.DraftFixedOrder = false
		s.RollSeed = func() uint64 { return seed }
	}, []string{"alpha", "bravo", "charlie", "delta"})
	wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	states := make([]DraftState, len(us))
	for i, u := range us {
		states[i] = u.draftState()
	}
	// the lobby's order
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	var lobby [2][]string
	for _, e := range lobbyRoster(hg.players, hg.game.Attrs) {
		if !e.Spectator && e.Team >= 1 && e.Team <= 2 {
			lobby[e.Team-1] = append(lobby[e.Team-1], e.Name)
		}
	}
	hg.mu.Unlock()
	teams := states[0].Teams
	if len(lobby[0]) != 2 || len(lobby[1]) != 2 || !slices.Equal(teams[0], []string{lobby[0][1], lobby[0][0]}) ||
		!slices.Equal(teams[1], lobby[1]) {
		t.Fatalf("seed %d: teams %v, the lobby %v", seed, teams, lobby)
	}
	who := func(name string) *uiClient {
		for i, c := range cs {
			if c.name == name {
				return us[i]
			}
		}
		t.Fatalf("no %s", name)
		return nil
	}
	idOf := func(name string) string {
		for _, c := range cs {
			if c.name == name {
				return itoa(c.id)
			}
		}
		return ""
	}
	type turn struct {
		kind   string
		team   int
		player string
	}
	want := []turn{{"ban", 2, lobby[1][0]}, {"ban", 1, lobby[0][0]}, {"ban", 2, lobby[1][0]}, {"ban", 1, lobby[0][0]},
		{"pick", 2, teams[1][0]}, {"pick", 1, teams[0][0]}, {"pick", 1, teams[0][1]}, {"pick", 2, teams[1][1]}}
	check := func(s DraftState, want []turn) {
		t.Helper()
		if len(s.Turns) != len(want) {
			t.Fatalf("turns %+v", s.Turns)
		}
		for i, w := range want {
			if g := s.Turns[i]; g.Kind != w.kind || g.Team != w.team || g.Player != w.player {
				t.Fatalf("turn %d: %+v, want %+v", i, g, w)
			}
		}
		if s.Turns[5].Window != s.Turns[6].Window || s.Turns[4].Window == s.Turns[5].Window {
			t.Fatalf("the windows %+v", s.Turns)
		}
	}
	check(states[0], want)
	if !strings.Contains(ts.log.String(), "(the pick order, shuffled); team 2 starts (a coin flip): the first ban and the "+
		"first pick; the bans by user "+idOf(lobby[0][0])+" (team 1) and user "+idOf(lobby[1][0])+" (team 2); the roster stays locked") {
		t.Fatalf("the begin's line:\n%s", ts.log.String())
	}
	// team 1's lobby top is now its seat 1
	if s := states[slices.IndexFunc(cs, func(c *tcpClient) bool { return c.name == lobby[0][0] })]; s.You == nil ||
		*s.You != (DraftYou{Team: 1, Seat: 1}) {
		t.Fatalf("team 1's top player's place %+v", s.You)
	}

	// a swap after the shuffle: team 1's seats trade their pick turns (the double window's order)
	t1a, t1b := who(teams[0][0]), who(teams[0][1])
	t1a.call(CallDraftSwap, `{"seat":1}`)
	t1a.draftState()
	t1b.draftState()
	t1b.call(CallDraftSwapAnswer, `{"accept":true}`)
	s := t1a.draftState()
	want[5].player, want[6].player = teams[0][1], teams[0][0]
	check(s, want)
	for _, u := range us {
		u.drain()
	}

	// the bans: everyone declares a role; team 2's top player bans first, team 1's other player can't ban for it
	for _, u := range us {
		u.call(CallDraftRole, `{"role":"hunter"}`)
	}
	endStep(t, ts)
	for _, u := range us {
		u.drain()
	}
	who(lobby[0][0]).call(CallDraftBan, `{"shaper":"PC_Alpha"}`)
	if s := who(lobby[0][0]).draftState(); s.Turn != 0 {
		t.Fatalf("team 1 banned first: %+v", s)
	}
	who(lobby[1][0]).call(CallDraftBan, `{"shaper":"PC_Alpha"}`)
	for _, u := range us {
		u.drain()
	}
	who(lobby[0][1]).call(CallDraftBan, `{"shaper":"PC_Artist"}`) // team 1's seat 0, not its banner
	if s := who(lobby[0][1]).draftState(); s.Turn != 1 {
		t.Fatalf("team 1's other player banned: %+v", s)
	}
	who(lobby[0][0]).call(CallDraftBan, `{"shaper":"PC_Artist"}`)
	if s := who(lobby[0][0]).draftState(); s.Turn != 2 || s.Turns[1].Shaper != "PC_Artist" {
		t.Fatalf("team 1's banner's ban: %+v", s)
	}
	for _, want := range []string{
		"draft turn 1/8: user " + idOf(lobby[1][0]) + " (team 2) bans PC_Alpha",
		"draft turn 2/8: user " + idOf(lobby[0][0]) + " (team 1) bans PC_Artist",
	} {
		if !strings.Contains(ts.log.String(), want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

// --draft-shuffle=off (DraftFixedOrder): the lobby's order, team 1 starts, its first player bans; the log says so.
func TestDraftShuffleOff(t *testing.T) {
	ts, _, cs, us, start, _ := draftLobbyN(t, nil, []string{"alpha", "bravo", "charlie", "delta"})
	wantError(t, "A starts", cs[0].gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	s := us[0].draftState()
	if s.Turns[0].Team != 1 || s.Turns[0].Player != s.Teams[0][0] || s.Turns[1].Team != 2 || s.Turns[4].Team != 1 ||
		s.Turns[4].Player != s.Teams[0][0] || s.Turns[5].Player != s.Teams[1][0] || s.Turns[6].Player != s.Teams[1][1] {
		t.Fatalf("turns %+v", s.Turns)
	}
	if !strings.Contains(ts.log.String(), "(the pick order, the lobby's); team 1 starts: the first ban and the first pick") {
		t.Fatalf("the begin's line:\n%s", ts.log.String())
	}
}
