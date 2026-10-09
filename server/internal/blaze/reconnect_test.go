package blaze

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// testRunningGame is matchmade 1 v 1 game 1 while it runs (tdf_golden.py's G3 cases): its PGID, IN_GAME.
func testRunningGame() Game {
	g := testMatchedGame()
	g.Capacity = []int64{2, 0}
	g.TeamCap = 1
	g.PersistedID = PersistedGameID(1)
	g.State = GameStateInGame
	return g
}

func testRunningPlayers() []Player {
	ps := testMatchedPlayers()
	ps[1].Team = 1
	return ps
}

// G3's replies and notifications, written independently by tdf_golden.py from the member tables, and against the
// client's tables.
func TestG3RepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	hosted := testGame()
	hosted.PersistedID = PersistedGameID(1)
	ps := testRunningPlayers()
	browser := BrowserGame{Game: testRunningGame(), State: GameStateInGame, Admin: ps[0].ID,
		Teams: [][]int64{{ps[0].ID}, {ps[1].ID}}, Players: 2}
	for _, c := range []struct {
		name, class string
		got         []byte
	}{
		{"notifygamesetup_hosted_notification", "Blaze::GameManager::NotifyGameSetup",
			GameSetupNotification(hosted, testPlayer())},
		{"getgameid_saved_reply", "Blaze::BGOps::GetPlayerGameIDResponse", GetGameIDReply(PersistedGameID(1))},
		{"getgamedatafromid_reply", "Blaze::GameManager::GameBrowserDataList", GameBrowserData([]BrowserGame{browser})},
		{"notifygamesetup_rejoin_notification", "Blaze::GameManager::NotifyGameSetup",
			RejoinGameSetupNotification(testRunningGame(), ps)},
		{"notifyplayerjoining_notification", "Blaze::GameManager::NotifyPlayerJoining",
			PlayerJoiningNotification(testRunningGame(), ps[1], 1)},
	} {
		tdfcheck.Require(t, c.class, c.got)
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// The PlayerIdList attributes decode the way the client's 0x00AB44A0 does: base64, then u64 little-endian per id.
func TestPlayerIDListAttrs(t *testing.T) {
	attrs := PlayerIDListAttrs([][]int64{{2000000001, 2000000003}, {2000000002}})
	if len(attrs) != 3 || attrs[0].Key != AttrPlayerIDTeamCount || attrs[0].Value != "2" {
		t.Fatalf("attrs %v", attrs)
	}
	raw, err := base64.StdEncoding.DecodeString(attrs[1].Value.(string))
	if err != nil || len(raw) != 16 || binary.LittleEndian.Uint64(raw[8:]) != 2000000003 {
		t.Errorf("team 0: %v %x", err, raw)
	}
	if id, ok := gameIDOfPersisted(PersistedGameID(42)); !ok || id != 42 {
		t.Errorf("persisted id round trip: %d %v", id, ok)
	}
	if _, ok := gameIDOfPersisted("00000000-0000-0000-0000-000000000001"); ok {
		t.Error("a foreign persisted id was taken")
	}
	got := sortedAttrs([]tdf.Pair{tdf.KV("ServerBuildVersion", "v"), tdf.KV("GameType", "1")}, attrs)
	var keys []string
	for _, p := range got {
		keys = append(keys, p.Key.(string))
	}
	if strings.Join(keys, ",") != "GameType,PlayerIdList_Team_0,PlayerIdList_Team_1,PlayerIdList_TeamCount,ServerBuildVersion" {
		t.Errorf("order %v", keys)
	}
}

// The reconnect as the client runs it: a 1 v 1 hosted game in progress; B saves the game's PGID (setGameID), its
// session ends (the host hears removePlayer, A gets NotifyPlayerRemoved); B logs in again: getGameID returns the PGID,
// getGameDataFromId the running game (IN_GAME, PCNT, B's team in PlayerIdList_Team_1), joinGame lets B back in
// (JoinGameResponse, NotifyGameSetup with the join context, A gets NotifyPlayerJoining); B's new session's end
// removes it again. A game the server doesn't run answers an empty list, a join of it GAMEMANAGER_ERR_INVALID_GAME_ID.
func TestHostedGameReconnect(t *testing.T) {
	removed := make(chan string, 4)
	var hosts *gmshost.Manager
	ts := newTCPServer(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 1, MinPlayers: 2})
		l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
			gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
				for _, r := range cg.Roster {
					if err := h.Send(gmshost.AddPlayer{User: r.User, Index: r.Index}); err != nil {
						return err
					}
				}
				if err := h.Send(gmshost.GMSUpdate{Phase: gmshost.PhaseInGame}); err != nil {
					return err
				}
				for {
					m, err := h.Recv(0)
					if err != nil {
						return err
					}
					if rp, ok := m.(gmshost.RemovePlayer); ok {
						removed <- rp.Reason
					}
				}
			})
		}}
		hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+5, GMSPort+5),
			ExitTimeout: time.Second})
		if err := hosts.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		s.Hosts = hosts
	})
	t.Cleanup(hosts.Close)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	a.queue(0)
	b.queue(0)
	var pgid string
	for _, c := range []*tcpClient{a, b} {
		f := c.note(ComponentGameManager, NotifyGameSetupID)
		game, _ := decode(t, f).Sub("GAME")
		pgid, _ = game.Str("PGID")
		c.note(ComponentGameManager, NotifyGameStateChangeID) // IN_GAME
	}
	if pgid != PersistedGameID(1) {
		t.Fatalf("PGID %q", pgid)
	}
	// B's client saves it, then its connection drops
	id := b.send(ComponentBGOps, CmdBGOpsSetGameID, tdf.NewWriter().Int("ACID", 0).String("GMID", pgid).Bytes())
	b.reply(ComponentBGOps, CmdBGOpsSetGameID, id)
	b.c.Close()
	if r := <-removed; r != "BLAZESERVER_CONN_LOST" {
		t.Errorf("host got removePlayer %s", r)
	}
	a.note(ComponentGameManager, NotifyPlayerRemovedID)

	b2 := ts.client("bravo@example.com")
	if b2.id != b.id {
		t.Fatalf("persona %d, was %d", b2.id, b.id)
	}
	id = b2.send(ComponentBGOps, CmdBGOpsGetGameID, tdf.NewWriter().Int("ACID", 0).Bytes())
	if gmid, _ := decode(t, b2.reply(ComponentBGOps, CmdBGOpsGetGameID, id)).Str("GMID"); gmid != pgid {
		t.Fatalf("getGameID %q", gmid)
	}
	ask := tdf.NewWriter().String("DNAM", "default").List("PIDL", tdf.TypeString, []string{pgid}).Bytes()
	id = b2.send(ComponentGameManager, CmdGameManagerGetGameDataFromID, ask)
	f := b2.reply(ComponentGameManager, CmdGameManagerGetGameDataFromID, id)
	tdfcheck.Require(t, "Blaze::GameManager::GameBrowserDataList", f.Payload)
	gd, _ := decode(t, f).Get("GDAT")
	items := gd.Value.(*tdf.List).Items
	if len(items) != 1 {
		t.Fatalf("GDAT %d games", len(items))
	}
	gb := items[0].(*tdf.Struct)
	if st, _ := gb.Int("GSTA"); st != GameStateInGame {
		t.Errorf("GSTA %#x", st)
	}
	af, _ := gb.Get("ATTR")
	var team1 string
	for _, e := range af.Value.(*tdf.Map).Entries {
		if e.Key == AttrPlayerIDTeam+"1" {
			team1 = e.Value.(string)
		}
	}
	if raw, _ := base64.StdEncoding.DecodeString(team1); len(raw) != 8 || int64(binary.LittleEndian.Uint64(raw)) != b.id {
		t.Errorf("PlayerIdList_Team_1 %q", team1)
	}

	join := tdf.NewWriter().Int("GID", 1).Int("TIDX", 1).Bytes()
	id = b2.send(ComponentGameManager, CmdGameManagerJoinGame, join)
	f = b2.reply(ComponentGameManager, CmdGameManagerJoinGame, id)
	if f.Type != fire.Reply {
		t.Fatalf("joinGame: %s error %#x", f.Type, f.Error)
	}
	tdfcheck.Require(t, "Blaze::GameManager::JoinGameResponse", f.Payload)
	f = b2.note(ComponentGameManager, NotifyGameSetupID)
	tdfcheck.Require(t, "Blaze::GameManager::NotifyGameSetup", f.Payload)
	setup := decode(t, f)
	rf, _ := setup.Get("REAS")
	if dctx, _ := rf.Value.(*tdf.Union).Field.Value.(*tdf.Struct).Int("DCTX"); dctx != SetupContextJoinGame {
		t.Errorf("setup context %d", dctx)
	}
	f = a.note(ComponentGameManager, NotifyPlayerJoiningID)
	tdfcheck.Require(t, "Blaze::GameManager::NotifyPlayerJoining", f.Payload)

	// a game that isn't running
	ask = tdf.NewWriter().List("PIDL", tdf.TypeString, []string{PersistedGameID(9)}).Bytes()
	id = b2.send(ComponentGameManager, CmdGameManagerGetGameDataFromID, ask)
	gd, _ = decode(t, b2.reply(ComponentGameManager, CmdGameManagerGetGameDataFromID, id)).Get("GDAT")
	if n := len(gd.Value.(*tdf.List).Items); n != 0 {
		t.Errorf("game 9: %d games", n)
	}
	id = b2.send(ComponentGameManager, CmdGameManagerJoinGame, tdf.NewWriter().Int("GID", 9).Bytes())
	if f := b2.reply(ComponentGameManager, CmdGameManagerJoinGame, id); f.Type != fire.ErrorReply || f.Error != ErrInvalidGameID {
		t.Errorf("join of game 9: %s %#x", f.Type, f.Error)
	}
	// the rejoined session's end takes B out again
	b2.c.Close()
	if r := <-removed; r != "BLAZESERVER_CONN_LOST" {
		t.Errorf("host got removePlayer %s", r)
	}
	if log := ts.log.String(); !strings.Contains(log, "game 1: player 2000000002 rejoins (team index 1 asked, 1 on the roster)") {
		t.Errorf("log:\n%s", log)
	}
	_ = store.FirstPersonaID
}
