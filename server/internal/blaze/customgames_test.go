package blaze

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// testCustomGame is hosted custom game 1 "Custom 1" as tdf_golden.py's B15 cases have it: CREATE GAME's three teams of
// five, GameFlags 2, the map's LevelId, in its lobby.
func testCustomGame() Game {
	return Game{ID: 1, Name: "Custom 1", Settings: 0x505, Capacity: []int64{15, 0}, TeamCap: 5,
		TeamIDs: []int64{1, 2, 3}, Version: "1.0.903.854-RL-Prod-Release", GMS: Addr{IP: 0x7F000001, Port: GMSPort},
		Seed: 7919, PersistedID: PersistedGameID(1), Attrs: mergeAttrs([]tdf.Pair{tdf.KV("ExpectedPlayerCount", "10"),
			tdf.KV("GameFlags", "2"), tdf.KV("GameOwnerId", "77359401"), tdf.KV("GameOwnerName", "player"),
			tdf.KV("GameType", "1"), tdf.KV("LevelId", "2490188286")},
			[]tdf.Pair{tdf.KV(ServerBuildVersionAttr, "1.0.903.854-RL-Prod-Release")})}
}

func testLobbyPlayers() []LobbyPlayer {
	ps := testMatchedPlayers()
	ps[1].Team = 1
	return []LobbyPlayer{{Player: ps[0], Slot: 0, State: PlayerStateActiveConnected},
		{Player: ps[1], Slot: 1, State: PlayerStateActiveConnecting}}
}

func testRosters() TeamRosters {
	return TeamRosters{Author: store.FirstPersonaID, Sequence: 2,
		Teams: [][]int64{{store.FirstPersonaID, 0, 0, 0, 0}, {store.FirstPersonaID + 1, 0, 0, 0, 0}, {0, 0, 0, 0, 0}}}
}

func b15Payloads() []struct {
	name, class string
	got         []byte
} {
	g := testCustomGame()
	ps := testLobbyPlayers()
	creator := ps[0]
	creator.State = PlayerStateActiveConnecting
	admins := []int64{ps[0].ID}
	listed := ListedGame{Game: g, State: GameStatePreGame, Admins: admins, Region: "na", Players: ps}
	rosterAttrs := []tdf.Pair{tdf.KV(AttrRosterLocked, "0"), tdf.KV(AttrSpectators, ""),
		tdf.KV(AttrTeamRosters, testRosters().Encode())}
	return []struct {
		name, class string
		got         []byte
	}{
		{"notifygamesetup_custom_notification", "Blaze::GameManager::NotifyGameSetup",
			LobbyGameSetupNotification(g, admins, []LobbyPlayer{creator}, SetupContextCreateGame)},
		{"notifygamesetup_customjoin_notification", "Blaze::GameManager::NotifyGameSetup",
			LobbyGameSetupNotification(g, admins, ps, SetupContextJoinGame)},
		{"notifyplayerjoining_custom_notification", "Blaze::GameManager::NotifyPlayerJoining",
			LobbyPlayerJoiningNotification(1, ps[1])},
		{"notifyplayerjoincompleted_notification", "Blaze::GameManager::NotifyPlayerJoinCompleted",
			PlayerJoinCompletedNotification(1, ps[1].ID)},
		{"notifygameattribchange_notification", "Blaze::GameManager::NotifyGameAttribChange",
			GameAttribChangeNotification(1, rosterAttrs)},
		{"notifyplayerattribchange_notification", "Blaze::GameManager::NotifyPlayerAttribChange",
			PlayerAttribChangeNotification(1, ps[1].ID, []tdf.Pair{tdf.KV("PrivateAuthToken", "1A2B3C4D")})},
		{"notifygameplayerteamchange_notification", "Blaze::GameManager::NotifyGamePlayerTeamChange",
			GamePlayerTeamChangeNotification(1, ps[1].ID, SpectatorTeam)},
		{"getgamelist_reply", "Blaze::GameManager::GetGameListResponse", GameListReply(1, 1)},
		{"notifygamelistupdate_notification", "Blaze::GameManager::NotifyGameListUpdate",
			GameListUpdateNotification(1, []ListedGame{listed}, nil)},
		{"notifygamelistupdate_removed_notification", "Blaze::GameManager::NotifyGameListUpdate",
			GameListUpdateNotification(1, nil, []int64{1})},
	}
}

// B15's replies and notifications, written independently by tdf_golden.py from the member tables, and against the
// client's tables.
func TestB15RepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range b15Payloads() {
		tdfcheck.Require(t, c.class, c.got)
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// The requests rebuilt for B15 (the game list's from g3b_run10b's log) are their classes' members, and what the
// handlers read from them is there.
func TestB15ClientRequests(t *testing.T) {
	g := golden.Load(t)
	for _, c := range []struct{ name, class string }{
		{"getgamelistsubscription_request", "Blaze::GameManager::GetGameListRequest"},
		{"resetdedicatedserver_custom_request", "Blaze::GameManager::CreateGameRequest"},
		{"joingame_browser_request", "Blaze::GameManager::JoinGameRequest"},
		{"joingame_spectate_request", "Blaze::GameManager::JoinGameRequest"},
		{"joingame_private_request", "Blaze::GameManager::JoinGameRequest"},
		{"setgameattributes_request", "Blaze::GameManager::SetGameAttributesRequest"},
		{"swapplayersteam_request", "Blaze::GameManager::SwapPlayersTeamRequest"},
		{"advancegamestate_request", "Blaze::GameManager::AdvanceGameStateRequest"},
		{"setplayerattributes_request", "Blaze::GameManager::SetPlayerAttributesRequest"},
		{"sendmessage_jointeam_request", "Blaze::Messaging::ClientMessage"},
		{"destroygamelist_request", "Blaze::GameManager::DestroyGameListRequest"},
	} {
		raw := golden.Bytes(t, decodeCase(t, g, c.name))
		tdfcheck.Require(t, c.class, raw)
	}
	body, err := tdf.Decode(golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request")))
	if err != nil {
		t.Fatal(err)
	}
	if f := int64(gameFromRequest(body, 1, Addr{}).descriptors()); !IsLobbyGame(f) {
		t.Errorf("GameFlags %#x isn't a lobby game", f)
	}
	body, _ = tdf.Decode(golden.Bytes(t, decodeCase(t, g, "setgameattributes_request")))
	if r, err := DecodeTeamRosters(attrValue(stringMap(body, "ATTR"), AttrTeamRosters)); err != nil ||
		r.Teams[1][0] != store.FirstPersonaID+1 || r.Sequence != 2 {
		t.Errorf("TeamRostersKey: %+v %v", r, err)
	}
	for _, f := range []int64{0x1, 0x50C, 0x40C, 0x4} {
		if IsLobbyGame(f) {
			t.Errorf("GameFlags %#x taken for a lobby game", f)
		}
	}
	if !IsLobbyGame(0x412) { // a practice group's game takes the custom branch too
		t.Error("PracticeGroup 0x412")
	}
}

// TeamRostersKey round trip, with the client's line breaks (0x00834CC0: a newline after every 76 characters).
func TestTeamRosters(t *testing.T) {
	r := TeamRosters{Author: 7, Sequence: 9, Teams: [][]int64{{1, 2, 3, 4, 0}, {11, 12, 13, 14, 5}, {6, 0, 0, 0, 0}}}
	s := r.Encode()
	if len(s) <= 76 {
		t.Fatalf("too short for a line break: %d", len(s))
	}
	got, err := DecodeTeamRosters(s[:76] + "\n" + s[76:])
	if err != nil || got.Author != 7 || got.Sequence != 9 || got.Teams[0][2] != 3 || got.Teams[1][4] != 5 || got.Teams[2][0] != 6 {
		t.Errorf("%+v %v", got, err)
	}
	for _, bad := range []string{"", "AAAA", "!!"} {
		if _, err := DecodeTeamRosters(bad); err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
}

// The host's roster of a lobby: team blocks 0-4 and 5-9, spectators 10-14 with team 0, in the order of the admin's
// TeamRostersKey where it names the players, else in join order; the players that left are not on it.
func TestLobbyRoster(t *testing.T) {
	mk := func(id, team, slot int64) *hostedPlayer {
		return &hostedPlayer{p: Player{ID: id, Name: "p", Team: team}, slot: slot}
	}
	players := []*hostedPlayer{mk(1, 0, 0), mk(2, 0, 1), mk(3, 1, 2), mk(4, SpectatorTeam, 3), mk(5, 1, 4)}
	players[4].removed = true
	roster := lobbyRoster(players, nil)
	if got := describeRoster(roster); got != "[1@0/team 1, 2@1/team 1, 3@5/team 2, 4@10/spectator]" {
		t.Errorf("join order: %s", got)
	}
	attrs := []tdf.Pair{tdf.KV(AttrTeamRosters, TeamRosters{Author: 1, Sequence: 1,
		Teams: [][]int64{{0, 2, 1, 0, 0}, {3, 0, 0, 0, 0}, {4, 0, 0, 0, 0}}}.Encode())}
	if got := describeRoster(lobbyRoster(players, attrs)); got != "[2@0/team 1, 1@1/team 1, 3@5/team 2, 4@10/spectator]" {
		t.Errorf("packed order: %s", got)
	}
}

// customHost is a fake match host that reports what dgserver sends it.
type customHost struct {
	created chan gmshost.CreateGame
	msgs    chan gmshost.Message
}

func newCustomHostServer(t *testing.T, setup func(s *Server)) (*tcpServer, *customHost) {
	h := &customHost{created: make(chan gmshost.CreateGame, 4), msgs: make(chan gmshost.Message, 64)}
	var hosts *gmshost.Manager
	ts := newTCPServer(t, func(s *Server) {
		l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
			gmshost.PlayHost(env, p, func(c *gmshost.HostConn, cg gmshost.CreateGame) error {
				h.created <- cg
				for {
					m, err := c.Recv(0)
					if err != nil {
						return err
					}
					h.msgs <- m
					if _, ok := m.(gmshost.AbortGame); ok {
						return nil
					}
				}
			})
		}}
		hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+5, GMSPort+6),
			ExitTimeout: time.Second})
		if err := hosts.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		s.Hosts = hosts
		if setup != nil {
			setup(s)
		}
	})
	t.Cleanup(hosts.Close)
	return ts, h
}

// hostNext returns the host's next message of type T (it skips the others).
func hostNext[T gmshost.Message](t *testing.T, h *customHost) T {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-h.msgs:
			if v, ok := m.(T); ok {
				return v
			}
		case <-deadline:
			var zero T
			t.Fatalf("host: no %s", zero.Type())
			return zero
		}
	}
}

func (tc *tcpClient) gm(cmd uint16, payload []byte) fire.Frame {
	tc.t.Helper()
	return tc.reply(ComponentGameManager, cmd, tc.send(ComponentGameManager, cmd, payload))
}

// wantLine checks a control message's line (the example lines in docs/data/host_control_examples.jsonl).
func wantLine(t *testing.T, m gmshost.Message, want string) {
	t.Helper()
	line, err := gmshost.Encode(m)
	if err != nil || string(line) != want+"\n" {
		t.Errorf("control line\n got %s\nwant %s", line, want)
	}
}

func wantReply(t *testing.T, what string, f fire.Frame) {
	t.Helper()
	if f.Type != fire.Reply {
		t.Fatalf("%s: %s error %#x", what, f.Type, f.Error)
	}
}

func wantError(t *testing.T, what string, f fire.Frame, code uint16) {
	t.Helper()
	if f.Type != fire.ErrorReply || f.Error != code {
		t.Fatalf("%s: %s error %#x, want error %#x", what, f.Type, f.Error, code)
	}
}

// listGames is a NotifyGameListUpdate's UPDT game ids (with their player counts) and REMV.
func listGames(t *testing.T, f fire.Frame) (updated map[int64]int, removed []int64) {
	t.Helper()
	st := decode(t, f)
	updated = map[int64]int{}
	if u, ok := st.Get("UPDT"); ok {
		for _, it := range u.Value.(*tdf.List).Items {
			gam, _ := it.(*tdf.Struct).Sub("GAM")
			gid, _ := gam.Int("GID")
			rost, _ := gam.Get("ROST")
			updated[gid] = len(rost.Value.(*tdf.List).Items)
		}
	}
	if r, ok := st.Get("REMV"); ok {
		for _, it := range r.Value.(*tdf.List).Items {
			removed = append(removed, it.(int64))
		}
	}
	return updated, removed
}

// A custom game as two clients play it, with a fake match host: A creates it (resetDedicatedServer with GameFlags 2:
// the host starts in mode custom, NotifyGameSetup with the create context after gameReady), B browses (the game list
// with A's game), joins (team 1, the join context with both players; A gets NotifyPlayerJoining; the host's
// updateRoster), both joins complete with their GMS connections, the admin's roster attributes and team swap reach
// everyone (B's own attempt is refused), a third player joins and is kicked, B's list follows, and A's start sends
// NotifyGameStateChange IN_GAME and the host's startGame. A player can't join the started game, a spectator can.
func TestCustomGameHosted(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")

	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	cg := <-host.created
	if cg.Mode != gmshost.ModeCustom || cg.Descriptors != 2 || cg.FillTeams != 0 || len(cg.Roster) != 1 ||
		cg.Roster[0].User != a.id || cg.Roster[0].Index != 0 || cg.Roster[0].Team != 1 {
		t.Fatalf("createGame %+v", cg)
	}
	wantLine(t, cg, `{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"custom","seed":7919,"roster":[{"user":2000000001,`+
		`"name":"alpha","index":0,"team":1,"noun":"","bot":false}],"descriptors":2}`) // docs/data/host_control_examples.jsonl
	setup := decode(t, a.note(ComponentGameManager, NotifyGameSetupID))
	game, _ := setup.Sub("GAME")
	if tids := intList(game, "TIDS"); len(tids) != 3 {
		t.Errorf("TIDS %v", tids)
	}
	if admins := intList(game, "ADMN"); len(admins) != 1 || admins[0] != a.id {
		t.Errorf("ADMN %v", admins)
	}

	// B browses: the list has A's game, one player
	f := b.gm(CmdGameManagerGetGameListSubscription, golden.Bytes(t, decodeCase(t, g, "getgamelistsubscription_request")))
	wantReply(t, "game list", f)
	tdfcheck.Require(t, "Blaze::GameManager::GetGameListResponse", f.Payload)
	listID, _ := decode(t, f).Int("GLID")
	up := b.note(ComponentGameManager, NotifyGameListUpdateID)
	tdfcheck.Require(t, "Blaze::GameManager::NotifyGameListUpdate", up.Payload)
	if got, _ := listGames(t, up); got[1] != 1 {
		t.Fatalf("list %v", got)
	}

	// B joins any team: team 1 (index 1), the join context with both players
	f = b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request")))
	wantReply(t, "join", f)
	f = b.note(ComponentGameManager, NotifyGameSetupID)
	tdfcheck.Require(t, "Blaze::GameManager::NotifyGameSetup", f.Payload)
	st := decode(t, f)
	pros, _ := st.Get("PROS")
	if n := len(pros.Value.(*tdf.List).Items); n != 2 {
		t.Errorf("PROS %d players", n)
	}
	rf, _ := st.Get("REAS")
	if dctx, _ := rf.Value.(*tdf.Union).Field.Value.(*tdf.Struct).Int("DCTX"); dctx != SetupContextJoinGame {
		t.Errorf("setup context %d", dctx)
	}
	pj := decode(t, a.note(ComponentGameManager, NotifyPlayerJoiningID))
	pdat, _ := pj.Sub("PDAT")
	if team, _ := pdat.Int("TIDX"); team != 1 {
		t.Errorf("B's team %d", team)
	}
	wantLine(t, hostNext[gmshost.UpdateRoster](t, host), `{"t":"updateRoster","roster":[{"user":2000000001,"name":"alpha",`+
		`"index":0,"team":1,"noun":"","bot":false},{"user":2000000002,"name":"bravo","index":5,"team":2,"noun":"","bot":false}]}`)
	// the update after the join (the host's ready may still have pushed the game as it was before)
	b.wait("the list after the join", func(f fire.Frame) bool {
		if f.Component != ComponentGameManager || f.Command != NotifyGameListUpdateID || f.Type != fire.Notification {
			return false
		}
		got, _ := listGames(t, f)
		return got[1] == 2
	})

	// both connect to the GMS: NotifyPlayerJoinCompleted to both, once each
	mesh := func(c *tcpClient) {
		w := tdf.NewWriter().Int("GID", 1).List("TARG", tdf.TypeStruct, []*tdf.Writer{
			tdf.NewWriter().Int("FLGS", 0).Int("PID", DedicatedServerID).Int("STAT", 2)})
		wantReply(t, "mesh", c.gm(CmdGameManagerUpdateMeshConnection, w.Bytes()))
	}
	mesh(a)
	mesh(b)
	mesh(a) // again: no second notification
	for _, c := range []*tcpClient{a, b} {
		done := map[int64]bool{}
		for range 2 {
			jc := decode(t, c.note(ComponentGameManager, NotifyPlayerJoinCompletedID))
			pid, _ := jc.Int("PID")
			done[pid] = true
		}
		if !done[a.id] || !done[b.id] {
			t.Errorf("%s: joins completed %v", c.name, done)
		}
		c.none(ComponentGameManager, NotifyPlayerJoinCompletedID, 50*time.Millisecond)
	}

	// the admin's roster attributes reach everyone; B may not set them
	attrs := golden.Bytes(t, decodeCase(t, g, "setgameattributes_request"))
	wantReply(t, "attributes", a.gm(CmdGameManagerSetGameAttributes, attrs))
	for _, c := range []*tcpClient{a, b} {
		f := c.note(ComponentGameManager, NotifyGameAttribChangeID)
		tdfcheck.Require(t, "Blaze::GameManager::NotifyGameAttribChange", f.Payload)
	}
	if got := describeRoster(hostNext[gmshost.UpdateRoster](t, host).Roster); got != "[2000000001@0/team 1, 2000000002@5/team 2]" {
		t.Errorf("updateRoster after the attributes %s", got) // the packed rosters order the slots
	}
	wantError(t, "B's attributes", b.gm(CmdGameManagerSetGameAttributes, attrs), ErrPermissionDenied)
	// B's own player attribute
	wantReply(t, "player attributes", b.gm(CmdGameManagerSetPlayerAttributes, golden.Bytes(t, decodeCase(t, g, "setplayerattributes_request"))))
	a.note(ComponentGameManager, NotifyPlayerAttribChangeID)

	// the admin moves B to the spectators: everyone hears, the host's roster has B at 10
	wantReply(t, "swap", a.gm(CmdGameManagerSwapPlayersTeam, golden.Bytes(t, decodeCase(t, g, "swapplayersteam_request"))))
	for _, c := range []*tcpClient{a, b} {
		tc := decode(t, c.note(ComponentGameManager, NotifyGamePlayerTeamChangeID))
		if pid, _ := tc.Int("PID"); pid != b.id {
			t.Errorf("%s: team change of %d", c.name, pid)
		}
	}
	if got := describeRoster(hostNext[gmshost.UpdateRoster](t, host).Roster); !strings.Contains(got, "2000000002@10/spectator") {
		t.Errorf("updateRoster after the swap %s", got)
	}

	// a third player joins and is kicked by the admin
	c := ts.client("charlie@example.com")
	wantReply(t, "C's join", c.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	c.note(ComponentGameManager, NotifyGameSetupID)
	hostNext[gmshost.UpdateRoster](t, host)
	kick := tdf.NewWriter().Int("CNTX", 0).Int("GID", 1).Int("PID", c.id).Int("REAS", RemovedPlayerKicked).Bytes()
	wantError(t, "B kicks", b.gm(CmdGameManagerRemovePlayer, kick), ErrPermissionDenied)
	wantReply(t, "A kicks", a.gm(CmdGameManagerRemovePlayer, kick))
	for _, x := range []*tcpClient{a, b, c} {
		pr := decode(t, x.note(ComponentGameManager, NotifyPlayerRemovedID))
		if pid, _ := pr.Int("PID"); pid != c.id {
			t.Errorf("%s: removed %d", x.name, pid)
		}
	}
	if rp := hostNext[gmshost.RemovePlayer](t, host); rp.User != c.id || rp.Reason != "PLAYER_KICKED" {
		t.Errorf("host removePlayer %+v", rp)
	}

	// the start: IN_GAME to both, startGame to the host
	wantError(t, "B starts", b.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))), ErrPermissionDenied)
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	for _, x := range []*tcpClient{a, b} {
		if st, _ := decode(t, x.note(ComponentGameManager, NotifyGameStateChangeID)).Int("GSTA"); st != GameStateInGame {
			t.Errorf("%s: state %#x", x.name, st)
		}
	}
	wantLine(t, hostNext[gmshost.StartGame](t, host), `{"t":"startGame","roster":[{"user":2000000001,"name":"alpha","index":0,`+
		`"team":1,"noun":"","bot":false},{"user":2000000002,"name":"bravo","index":10,"team":0,"noun":"","bot":false,"spectator":true}]}`)
	// HOME1: started, it is a match for the home page, no longer an open lobby (still in the browser's list)
	if ls := ts.s.CustomLobbies(); len(ls) != 1 || !ls[0].Started || ls[0].Players != 1 || ls[0].Spectators != 1 {
		t.Errorf("CustomLobbies %+v", ls)
	}
	if ms := ts.s.HostedMatches(); len(ms) != 1 || ms[0].Lobby || ms[0].InGameAt.IsZero() || ms[0].Mode != gmshost.ModeCustom {
		t.Errorf("HostedMatches %+v", ms)
	}
	// now only a spectator may join
	d := ts.client("delta@example.com")
	wantError(t, "D joins the started game", d.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))), ErrGameInProgress)
	wantReply(t, "D spectates", d.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
	d.note(ComponentGameManager, NotifyGameSetupID)
	// B15b: the host hears of the spectator of the running game (it keeps startGame's players and takes the spectators)
	wantLine(t, hostNext[gmshost.UpdateRoster](t, host), `{"t":"updateRoster","roster":[{"user":2000000001,"name":"alpha",`+
		`"index":0,"team":1,"noun":"","bot":false},{"user":2000000002,"name":"bravo","index":10,"team":0,"noun":"",`+
		`"bot":false,"spectator":true},{"user":2000000004,"name":"delta","index":11,"team":0,"noun":"","bot":false,`+
		`"spectator":true}]}`) // docs/data/host_control_examples.jsonl

	// B's list is gone once destroyed: no more updates
	wantReply(t, "destroy list", b.gm(CmdGameManagerDestroyGameList, tdf.NewWriter().Int("GLID", listID).Bytes()))
	log := ts.log.String()
	for _, want := range []string{`game 1 "Custom 1": a custom game (GameFlags 0x2, 3 teams of 5, private false)`,
		"game 1: player 2000000002 joins (team 1, slot 1, 2 players now; started false)",
		"game 1: player 2000000003 removed by admin 2000000001 (PLAYER_KICKED)",
		"game 1 starts (its admin's advanceGameState IN_GAME): roster [2000000001@0/team 1, 2000000002@10/spectator]",
		"game list 1 destroyed"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

// The admin leaving its lobby ends the game: the host is aborted, the other player gets GAME_DESTROYED, the browser
// drops the game.
func TestCustomGameAdminLeaves(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	g := golden.Load(t)
	a, b, v := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("victor@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "list", v.gm(CmdGameManagerGetGameListSubscription, golden.Bytes(t, decodeCase(t, g, "getgamelistsubscription_request"))))
	v.note(ComponentGameManager, NotifyGameListUpdateID)
	wantReply(t, "join", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	b.note(ComponentGameManager, NotifyGameSetupID)
	v.note(ComponentGameManager, NotifyGameListUpdateID)
	leave := tdf.NewWriter().Int("CNTX", 0).Int("GID", 1).Int("PID", a.id).Int("REAS", 6).Bytes()
	wantReply(t, "A leaves", a.gm(CmdGameManagerRemovePlayer, leave))
	if ab := hostNext[gmshost.AbortGame](t, host); ab.Reason != "its admin left the lobby" {
		t.Errorf("abortGame %q", ab.Reason)
	}
	reasons := map[int64]bool{} // A's leave (PLAYER_LEFT, 0.5 s later) and the game's end (GAME_DESTROYED), in any order
	for range 2 {
		reas, _ := decode(t, b.note(ComponentGameManager, NotifyPlayerRemovedID)).Int("REAS")
		reasons[reas] = true
	}
	if !reasons[6] || !reasons[RemovedGameDestroyed] {
		t.Errorf("B: removals %v", reasons)
	}
	for {
		_, removed := listGames(t, v.note(ComponentGameManager, NotifyGameListUpdateID))
		if len(removed) == 1 && removed[0] == 1 {
			break
		}
	}
}

// Without a match host the lobby still works on the Blaze side: NotifyGameSetup names --gms-port at once, joins,
// the team full error, the start; the game ends with its last player.
func TestCustomGameNoHost(t *testing.T) {
	ts := newTCPServer(t, nil)
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	f := a.note(ComponentGameManager, NotifyGameSetupID)
	tdfcheck.Require(t, "Blaze::GameManager::NotifyGameSetup", f.Payload)
	// five more fill team 1 and team 2 (any team: the smaller), then the 11th player finds both full
	var clients []*tcpClient
	for i := 0; i < 9; i++ {
		c := ts.client(string(rune('b'+i)) + "@example.com")
		wantReply(t, "join", c.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
		clients = append(clients, c)
	}
	late := ts.client("late@example.com")
	wantError(t, "a full game", late.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))), ErrGameFull)
	wantReply(t, "spectate", late.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
	wantReply(t, "start", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	wantError(t, "start again", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))), ErrInvalidGameStateAction)
	for _, c := range append(clients, a, late) {
		c.c.Close()
	}
	deadline := time.Now().Add(5 * time.Second)
	for ts.s.hosted.get(1) != nil {
		if time.Now().After(deadline) {
			t.Fatal("game 1 still there after its last player")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if log := ts.log.String(); !strings.Contains(log, "game 1 ends: no players left") {
		t.Errorf("log:\n%s", log)
	}
}

// dgserver --shaper-select: the host replaces its roster with updateRoster's and startGame's, so a joiner's entry
// carries its own content (shapers, wards, announcers) there as the creator's does in createGame; a spectator's none.
func TestCustomGameShaperSelect(t *testing.T) {
	ts, host := newCustomHostServer(t, func(s *Server) {
		s.Content = loadFixtureContent(t)
		s.ShaperSelect = ShaperSelect{On: true}
	})
	g := golden.Load(t)
	a, b, c := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	if cg := <-host.created; !cg.ShaperSelect || len(cg.Roster) != 1 || len(cg.Roster[0].Shapers) == 0 {
		t.Fatalf("createGame %+v", cg)
	}
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	ur := hostNext[gmshost.UpdateRoster](t, host)
	if len(ur.Roster) != 2 || len(ur.Roster[0].Shapers) == 0 || len(ur.Roster[1].Shapers) == 0 || len(ur.Roster[1].Wards) == 0 {
		t.Fatalf("updateRoster %s: the joiner without content", describeRoster(ur.Roster))
	}
	wantReply(t, "C spectates", c.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
	hostNext[gmshost.UpdateRoster](t, host)
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	sg := hostNext[gmshost.StartGame](t, host)
	if got := describeRoster(sg.Roster); got != "[2000000001@0/team 1, 2000000002@5/team 2, 2000000003@10/spectator]" {
		t.Fatalf("startGame %s", got)
	}
	if len(sg.Roster[0].Shapers) == 0 || len(sg.Roster[1].Shapers) == 0 || len(sg.Roster[2].Shapers) != 0 {
		t.Errorf("startGame content: %d, %d, %d shapers", len(sg.Roster[0].Shapers), len(sg.Roster[1].Shapers),
			len(sg.Roster[2].Shapers))
	}
	if line, err := gmshost.Encode(sg); err != nil || len(line) > gmshost.MaxLine {
		t.Errorf("startGame line: %d bytes (%v)", len(line), err)
	}
}

// B15b: a custom game's spectator joins the GMS like a player (the host's addPlayer, as the host DLL reports a served
// spectator) but plays nothing: at finishGame its account stores no game and the host gets no playerRewards for it,
// while the player's game is stored and rewarded as before.
func TestCustomGameSpectatorNotStored(t *testing.T) {
	got := make(chan gmshost.Message, 16)
	var hosts *gmshost.Manager
	ts := newTCPServer(t, func(s *Server) {
		s.Content = loadFixtureContent(t)
		s.Rand = fixedRand(0.8)
		l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
			gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
				for {
					m, err := h.Recv(0)
					if err != nil {
						return err
					}
					sg, ok := m.(gmshost.StartGame)
					if !ok {
						continue
					}
					for _, r := range sg.Roster {
						h.Send(gmshost.AddPlayer{User: r.User, Index: r.Index})
						if !r.Spectator { // the host sends no playerFinish for a spectator
							h.Send(gmshost.PlayerFinish{User: r.User, Stats: map[string]int64{progress.StatKills: 3}})
						}
					}
					if err := h.Send(gmshost.FinishGame{WinnerTeam: 1, DurationMs: 600000, Reason: "guardian"}); err != nil {
						return err
					}
					for {
						m, err := h.Recv(time.Second)
						if err != nil {
							return nil // nothing more comes: the host ends
						}
						got <- m
					}
				}
			})
		}}
		hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+5, GMSPort+6),
			ExitTimeout: time.Second})
		if err := hosts.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		s.Hosts = hosts
	})
	t.Cleanup(hosts.Close)
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B spectates", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
	b.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	for deadline := time.Now().Add(5 * time.Second); hosts.Running() > 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("match host still running")
		}
	}
	var rewarded []int64
	for len(got) > 0 {
		if r, ok := (<-got).(gmshost.PlayerRewards); ok {
			rewarded = append(rewarded, r.User)
		}
	}
	if len(rewarded) != 1 || rewarded[0] != a.id {
		t.Errorf("playerRewards for %v, want only the player %d", rewarded, a.id)
	}
	pa, _ := ts.s.Store.Account(store.FirstUserID)
	pb, _ := ts.s.Store.Account(store.FirstUserID + 1)
	if pa.Stats[progress.PlayerStats]["PLAYED"] != "1" {
		t.Errorf("the player's stats %v", pa.Stats[progress.PlayerStats])
	}
	if played := pb.Stats[progress.PlayerStats]["PLAYED"]; played != "" {
		t.Errorf("the spectator's account has PLAYED %q", played)
	}
	// a custom game counts for the Leaderboard, not for its spectator
	if pa.History.Ranked != (store.GamesWon{Games: 1, Wins: 1}) || pb.History.Ranked != (store.GamesWon{}) {
		t.Errorf("Leaderboard counts: the player's %+v, the spectator's %+v", pa.History.Ranked, pb.History.Ranked)
	}
	if log := ts.log.String(); !strings.Contains(log, "account 1000000001: game 1 leaderboard: counted (custom game); 1 games, 1 wins") {
		t.Errorf("log lacks the player's leaderboard line")
	}
	if log := ts.log.String(); !strings.Contains(log, "account 1000000002: game 1 not stored (a spectator)") {
		t.Errorf("log lacks the spectator's line")
	}
}

// dgserver --custom-bots: a custom game's host fills the open seats with bots (createGame's fillTeams = the team size).
func TestCustomGameBots(t *testing.T) {
	ts, host := newCustomHostServer(t, func(s *Server) { s.CustomBots = true })
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	if cg := <-host.created; cg.FillTeams != 5 || cg.Mode != gmshost.ModeCustom {
		t.Errorf("createGame fillTeams %d, mode %s", cg.FillTeams, cg.Mode)
	}
}

// dgserver --custom-autostart: a custom game whose only human moved to the spectators starts by itself (the lobby offers
// no Start Game then): the host's startGame with the spectator, NotifyGameStateChange IN_GAME to the client, and a later
// advanceGameState is refused as a second start. Moving back to a team before then keeps the game in its lobby.
func TestCustomGameAutoStart(t *testing.T) {
	ts, host := newCustomHostServer(t, func(s *Server) {
		s.CustomBots = true
		s.CustomAutoStart = 50 * time.Millisecond
	})
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	toTeam := func(team int64) []byte {
		return tdf.NewWriter().Int("GID", 1).List("LGAM", tdf.TypeStruct, []*tdf.Writer{
			tdf.NewWriter().Int("PID", a.id).Int("TIDX", team)}).Bytes()
	}
	// to the spectators and back to team 1 at once: no start
	wantReply(t, "to the spectators", a.gm(CmdGameManagerSwapPlayersTeam, toTeam(SpectatorTeam)))
	wantReply(t, "back to team 1", a.gm(CmdGameManagerSwapPlayersTeam, toTeam(0)))
	time.Sleep(150 * time.Millisecond)
	if log := ts.log.String(); !strings.Contains(log, "game 1: no start by itself (a human is on a team, or it started)") {
		t.Errorf("log lacks the cancelled start")
	}
	// to the spectators again: it starts by itself
	wantReply(t, "to the spectators again", a.gm(CmdGameManagerSwapPlayersTeam, toTeam(SpectatorTeam)))
	sg := hostNext[gmshost.StartGame](t, host)
	if len(sg.Roster) != 1 || !sg.Roster[0].Spectator {
		t.Fatalf("startGame roster %s", describeRoster(sg.Roster))
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if strings.Contains(ts.log.String(), "game 1 starts (--custom-autostart: every human spectates)") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("log lacks the start by itself")
		}
	}
	wantError(t, "a second start", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))),
		ErrInvalidGameStateAction)
}

// dgserver --halloween: every hosted game's createGame carries levelFlags 1 (the Halloween decoration's bit), which the
// host loads its level with and sends in GamePrepareForStart; without it the field is left out (TestCustomGameHosted).
func TestHalloweenLevelFlags(t *testing.T) {
	ts, host := newCustomHostServer(t, func(s *Server) { s.Halloween = true })
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	cg := <-host.created
	if cg.LevelFlags != gmshost.LevelFlagHalloween {
		t.Errorf("createGame levelFlags %d, want %d", cg.LevelFlags, gmshost.LevelFlagHalloween)
	}
	wantLine(t, cg, `{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"custom","seed":7919,"roster":[{"user":2000000001,`+
		`"name":"alpha","index":0,"team":1,"noun":"","bot":false}],"descriptors":2,"levelFlags":1}`) // docs/data/host_control_examples.jsonl
}

// createRequest is the custom create request with its GameFlags attribute and, when name isn't "", its GNAM replaced.
func createRequest(t *testing.T, g *golden.Data, flags, name string) []byte {
	t.Helper()
	st, err := tdf.Decode(golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request")))
	if err != nil {
		t.Fatal(err)
	}
	w := tdf.NewWriter()
	for _, f := range st.Fields {
		if m, ok := f.Value.(*tdf.Map); ok && f.Label == "ATTR" {
			for i, e := range m.Entries {
				if e.Key == "GameFlags" {
					m.Entries[i].Value = flags
				}
			}
		}
		if f.Label == "GNAM" && name != "" {
			f.Value = name
		}
		w.Value(f.Label, f.Type, f.Value)
	}
	return w.Bytes()
}

// practiceGroupRequest is the custom create request as a practice group sends it: GameFlags 0x412 (1042).
func practiceGroupRequest(t *testing.T, g *golden.Data) []byte {
	t.Helper()
	return createRequest(t, g, "1042", "")
}

// A practice group's players are all on team 0, the group's own, whatever TIDX their join asks for (VPSLIVE: a client
// answering a stale invite asked for TIDX 1 and played against its group, unseen in the group's lobby); the admin's
// swap can't move one off it; a sixth player finds the team full; the host's start has the five on team 1.
func TestPracticeGroupTeams(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, practiceGroupRequest(t, g)))
	if cg := <-host.created; cg.Descriptors != 0x412 || cg.Roster[0].Team != 1 {
		t.Fatalf("createGame %+v", cg)
	}
	a.note(ComponentGameManager, NotifyGameSetupID)

	var others []*tcpClient
	for i, tidx := range []int64{1, 0, 0xFFFF, 1} { // the stale invite's TIDX 1 first
		c := ts.client(fmt.Sprintf("p%d@example.com", i))
		others = append(others, c)
		wantReply(t, fmt.Sprintf("join %d (TIDX %d)", i, tidx),
			c.gm(CmdGameManagerJoinGame, tdf.NewWriter().Int("GID", 1).Int("TIDX", tidx).Bytes()))
		pj := decode(t, a.note(ComponentGameManager, NotifyPlayerJoiningID))
		pdat, _ := pj.Sub("PDAT")
		if team, _ := pdat.Int("TIDX"); team != 0 {
			t.Errorf("player %d (asked TIDX %d): team %d", i, tidx, team)
		}
	}
	f := ts.client("full@example.com")
	wantError(t, "a sixth joins", f.gm(CmdGameManagerJoinGame, tdf.NewWriter().Int("GID", 1).Int("TIDX", 1).Bytes()), ErrTeamFull)

	swap := tdf.NewWriter().Int("GID", 1).List("LGAM", tdf.TypeStruct, []*tdf.Writer{
		tdf.NewWriter().Int("PID", others[0].id).Int("TIDX", 1)})
	wantReply(t, "swap to team 1", a.gm(CmdGameManagerSwapPlayersTeam, swap.Bytes()))
	a.none(ComponentGameManager, NotifyGamePlayerTeamChangeID, 50*time.Millisecond)

	wantReply(t, "start", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	sg := hostNext[gmshost.StartGame](t, host)
	if len(sg.Roster) != 5 {
		t.Fatalf("startGame roster %s", describeRoster(sg.Roster))
	}
	for _, e := range sg.Roster {
		if e.Team != 1 || e.Spectator {
			t.Errorf("startGame roster %s", describeRoster(sg.Roster))
			break
		}
	}
	log := ts.log.String()
	for _, want := range []string{`a practice group game (GameFlags 0x412`,
		"joinGame: game 1: player 2000000002 asked for team 1; a practice group's players join team 0",
		"joinGame: game 1: team 0 full; error reply",
		"game 1: 1 moves off team 0 ignored (a practice group)"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

// The create dialog's bot checkbox (BOTBOX) ends the name in BotFillMarker: the server strips it, names the game
// "[Bots] <name>" (NotifyGameSetup, the browser) and the host fills the open seats (createGame's fillTeams = the team
// size). Without the marker nothing changes; a practice group loses the marker and keeps its own bot rule.
func TestCustomGameBotFillMarker(t *testing.T) {
	g := golden.Load(t)
	for _, tc := range []struct {
		what, flags, name, want string
		fill                    int
	}{
		{"ticked", "2", "Friday Night" + BotFillMarker, "[Bots] Friday Night", 5},
		{"unticked", "2", "Friday Night", "Friday Night", 0},
		{"only the marker", "2", BotFillMarker, "[Bots] alpha's Game", 5},
		{"a practice group", "1042", "Friday Night" + BotFillMarker, "Friday Night", 0},
	} {
		t.Run(tc.what, func(t *testing.T) {
			ts, host := newCustomHostServer(t, nil)
			a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
			wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, createRequest(t, g, tc.flags, tc.name)))
			if cg := <-host.created; cg.FillTeams != tc.fill {
				t.Errorf("createGame fillTeams %d, want %d", cg.FillTeams, tc.fill)
			}
			game, _ := decode(t, a.note(ComponentGameManager, NotifyGameSetupID)).Sub("GAME")
			if name, _ := game.Str("GNAM"); name != tc.want {
				t.Errorf("NotifyGameSetup GNAM %q, want %q", name, tc.want)
			}
			if tc.flags != "2" {
				return // a practice group isn't in the browser
			}
			wantReply(t, "game list", b.gm(CmdGameManagerGetGameListSubscription,
				golden.Bytes(t, decodeCase(t, g, "getgamelistsubscription_request"))))
			up := decode(t, b.note(ComponentGameManager, NotifyGameListUpdateID))
			u, _ := up.Get("UPDT")
			items := u.Value.(*tdf.List).Items
			if len(items) != 1 {
				t.Fatalf("browser: %d games", len(items))
			}
			gam, _ := items[0].(*tdf.Struct).Sub("GAM")
			if name, _ := gam.Str("GNAM"); name != tc.want {
				t.Errorf("browser GNAM %q, want %q", name, tc.want)
			}
		})
	}
}
