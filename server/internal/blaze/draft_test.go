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

// draftLobby is a custom game with the draft rule (dgserver --test-rules pick=draft) and a host that takes rules: A
// (team 1, the admin) and B (team 2), both in the lobby with the class, B ready. setup may change the server.
func draftLobby(t *testing.T, setup func(s *Server)) (*tcpServer, *customHost, *tcpClient, *tcpClient, *uiClient, *uiClient, []byte, string) {
	t.Helper()
	dr, err := rules.Parse("pick=draft")
	if err != nil {
		t.Fatal(err)
	}
	ts, host, addr := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) {
		s.TestRules = &dr
		s.DraftTurn = 10 * time.Second
		s.RollSeed = func() uint64 { return 5 }
		if setup != nil {
			setup(s)
		}
	})
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	b.settle()
	ua, ub := uiConnect(t, addr, a), uiConnect(t, addr, b)
	ua.call(CallLobbyEnter, "")
	ub.call(CallLobbyEnter, "")
	ub.call(CallLobbySetReady, `{"ready":true}`)
	time.Sleep(50 * time.Millisecond)
	ua.drain()
	ub.drain()
	return ts, host, a, b, ua, ub, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request")), addr
}

// The draft driven by calls: START holds the start and begins it; only the player on turn acts, only with a free shaper
// of the pool; spectators and START again change nothing; after the last pick the server starts the game itself with
// the rules' pick "draft".
func TestDraftByCalls(t *testing.T) {
	ts, host, a, b, ua, ub, start, _ := draftLobby(t, nil)
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	sa, sb := ua.draftState(), ub.draftState()
	if sa.Phase != "ban" || sa.Turn != 0 || !sa.YourTurn || sa.You == nil || sa.You.Team != 1 || sa.You.Seat != 0 {
		t.Fatalf("A's first board %+v", sa)
	}
	if sb.YourTurn || sb.You == nil || sb.You.Team != 2 || sb.Left <= 0 || sb.Left > 10000 || sb.TurnMs != 10000 {
		t.Fatalf("B's first board %+v", sb)
	}
	if len(sa.Turns) != 6 || sa.Turns[4].Kind != "pick" || sa.Turns[4].Player != a.name || sa.Turns[5].Player != b.name ||
		sa.Teams[0][0] != a.name || sa.Teams[1][0] != b.name {
		t.Fatalf("turns %+v teams %v", sa.Turns, sa.Teams)
	}
	if len(sa.Pool) != 35 || sa.Pool[0] != (DraftShaper{ID: "PC_Alpha", Name: "Alpha"}) {
		t.Fatalf("pool %d: %+v", len(sa.Pool), sa.Pool[:1])
	}
	log := ts.log.String()
	if !strings.Contains(log, "game 1: the draft begins (RULES5b; seed 5, 35 shapers in the pool, 6 turns of 10s)") ||
		!strings.Contains(log, "advanceGameState IN_GAME refused: the draft runs first") {
		t.Fatalf("the log lacks the draft's begin:\n%s", log)
	}
	// the roster stays locked: the server set RosterLockedKey "1" (the test's START didn't)
	if f := b.note(ComponentGameManager, NotifyGameAttribChangeID); !strings.Contains(string(f.Payload), "RosterLockedKey") {
		t.Errorf("B's attribute change %q", f.Payload)
	}

	// B isn't on turn, a pick in a ban turn, a shaper outside the pool, a malformed call: refused, the caller hears the
	// board again, the other nothing
	ub.call(CallDraftBan, `{"shaper":"PC_Alpha"}`)
	if s := ub.draftState(); s.Turn != 0 {
		t.Fatalf("after B's refused ban %+v", s)
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
	if s := ub.draftState(); s.Phase != "pick" || s.Turn != 5 || !s.YourTurn {
		t.Fatalf("B's pick turn %+v", s)
	}
	ub.call(CallDraftPick, `{"shaper":"PC_Artificer"}`) // the other team's pick
	if s := ub.draftState(); s.Turn != 5 {
		t.Fatalf("B took A's pick: %+v", s)
	}
	ub.call(CallDraftPick, `{"shaper":"PC_Warden"}`)
	if s := ua.draftState(); s.Phase != "done" || s.Turn != -1 || s.Turns[5].Shaper != "PC_Warden" {
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
		"draft turn 1/6: user " + itoa(a.id) + " (team 1) bans PC_Alpha",
		"draft turn 6/6: user " + itoa(b.id) + " (team 2) picks PC_Warden",
		"game 1: the draft is done: user " + itoa(a.id) + " (team 1) PC_Artificer, user " + itoa(b.id) + " (team 2) PC_Warden",
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

// A drafter's leave cancels the draft: the board says why, the roster is unlocked, every ready mark reset; the lobby
// stays and can start again (a new draft, the empty team without turns).
func TestDraftLeaverCancels(t *testing.T) {
	ts, _, a, b, ua, ub, start, _ := draftLobby(t, nil)
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	ua.draftState()
	ub.draftState()
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
	// the lobby stays: START again begins a new draft, A alone (three bans and one pick)
	ua.drain()
	wantError(t, "A starts again", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	if s := ua.draftState(); s.Phase != "ban" || len(s.Turns) != rules.DraftBansPerTeam+1 || len(s.Teams[1]) != 0 {
		t.Fatalf("the second draft %+v", s)
	}
}

// Every turn timed out: the bans are skipped, the picks random, and the game starts.
func TestDraftTimeouts(t *testing.T) {
	ts, host, a, _, ua, _, start, _ := draftLobby(t, func(s *Server) { s.DraftTurn = 20 * time.Millisecond })
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
	if !strings.Contains(log, "draft turn 1/6 (ban, team 1, user "+itoa(a.id)+") timed out: skipped") ||
		!strings.Contains(log, "draft turn 5/6 (pick, team 1, user "+itoa(a.id)+") timed out: a random pick: PC_") {
		t.Errorf("the log lacks the timeouts:\n%s", log)
	}
}

// --test-draft: the server acts for the player on turn (and tries once out of turn, refused), so a draft runs without
// the board.
func TestDraftTestAid(t *testing.T) {
	ts, host, a, b, ua, _, start, _ := draftLobby(t, func(s *Server) { s.TestDraft = 5 * time.Millisecond })
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
	if !strings.Contains(log, "draft (--test-draft, a test aid): user "+itoa(b.id)+"'s ban out of turn: ban \"PC_WarlockKnight\": not this player's turn") {
		t.Errorf("the log lacks the refused call:\n%s", log)
	}
}

// While the draft runs: a newcomer may only spectate (and sees the board, without a seat), a team change is refused.
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
	if s := uc.draftState(); s.You != nil || s.YourTurn || s.Phase != "ban" {
		t.Fatalf("the spectator's board %+v", s)
	}
	uc.call(CallDraftBan, `{"shaper":"PC_Alpha"}`)
	if s := uc.draftState(); s.Turn != 0 {
		t.Fatalf("the spectator's ban %+v", s)
	}
	swap := tdf.NewWriter().Int("GID", 1).List("LGAM", tdf.TypeStruct, []*tdf.Writer{
		tdf.NewWriter().Int("PID", b.id).Int("TIDX", 0)}).Bytes()
	wantError(t, "A moves B", a.gm(CmdGameManagerSwapPlayersTeam, swap), ErrInvalidGameStateAction)
}

// The event's wire: field names and order (the documented example).
func TestDraftStateJSON(t *testing.T) {
	st := DraftState{Phase: "pick", Turn: 6, Left: 21350, TurnMs: 30000, Teams: [2][]string{{"Ash"}, {"Birch"}},
		Pool: []DraftShaper{{ID: "PC_Artificer", Name: "Petrus", CatalogID: 273770958,
			Portrait: "Heroes/Artificer/default/ui/Textures/artificer_portrait.dds",
			Card:     "Heroes/Artificer/default/ui/Textures/artificer_purchase.dds"}, {ID: "PC_Old", Name: "Old"}},
		Turns: []DraftStateTurn{{Kind: "ban", Team: 1, Player: "Ash", Shaper: "PC_Alpha", How: "chosen"},
			{Kind: "ban", Team: 2, Player: "Birch", How: "timeout"}, {Kind: "pick", Team: 1, Player: "Ash"}},
		You: &DraftYou{Team: 1, Seat: 0}, YourTurn: true}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"phase":"pick","turn":6,"left":21350,"turnMs":30000,"teams":[["Ash"],["Birch"]],` +
		`"pool":[{"id":"PC_Artificer","name":"Petrus","catalogID":273770958,` +
		`"portrait":"Heroes/Artificer/default/ui/Textures/artificer_portrait.dds",` +
		`"card":"Heroes/Artificer/default/ui/Textures/artificer_purchase.dds"},{"id":"PC_Old","name":"Old","catalogID":0}],` +
		`"turns":[{"kind":"ban","team":1,"player":"Ash","shaper":"PC_Alpha","how":"chosen"},` +
		`{"kind":"ban","team":2,"player":"Birch","shaper":"","how":"timeout"},{"kind":"pick","team":1,"player":"Ash","shaper":"","how":""}],` +
		`"you":{"team":1,"seat":0},"yourTurn":true}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

// The draft's arguments: {"shaper": "..."} only.
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
