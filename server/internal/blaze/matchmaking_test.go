package blaze

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// startMatchmakingRequest is a minimal StartMatchmakingRequest for the flow tests (the client's whole
// request is the golden decode case startmatchmaking_request).
func startMatchmakingRequest(durMs int64) []byte {
	return startMatchmakingRequestAttrs(durMs, tdf.KV("GameType", "1"))
}

// startMatchmakingRequestAttrs is startMatchmakingRequest with other game attributes (D8c: GameFlags "1", the
// matchmade descriptor whose post-game has the karma vote).
func startMatchmakingRequestAttrs(durMs int64, attrs ...tdf.Pair) []byte {
	w := tdf.NewWriter()
	w.Map("ATTR", tdf.TypeString, tdf.TypeString, attrs...)
	w.Int("DUR", durMs)
	w.Int("GSET", 0x506)
	w.String("GVER", "1.0.903.854-RL-Prod-Release")
	w.Union("PNET", NetworkAddressIpPair, func(w *tdf.Writer) {
		w.Struct("VALU", func(w *tdf.Writer) { ipPair(w, Addr{IP: 0x7F000001, Port: 3659}, Addr{IP: 0x0A000002, Port: 3659}) })
	})
	return w.Bytes()
}

func mmServer(t *testing.T, team, min int, fill time.Duration) *tcpServer {
	return newTCPServer(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: team, MinPlayers: min, FillAfter: fill})
	})
}

// queue sends startMatchmaking and returns the session id from the reply.
func (tc *tcpClient) queue(durMs int64) int64 {
	tc.t.Helper()
	id := tc.send(ComponentGameManager, CmdGameManagerStartMatchmaking, startMatchmakingRequest(durMs))
	f := tc.reply(ComponentGameManager, CmdGameManagerStartMatchmaking, id)
	if f.Type != fire.Reply {
		tc.t.Fatalf("%s: startMatchmaking: %s error %#x", tc.name, f.Type, f.Error)
	}
	tdfcheck.Require(tc.t, "Blaze::GameManager::StartMatchmakingResponse", f.Payload)
	msid, _ := decode(tc.t, f).Int("MSID")
	return msid
}

// gameSetup waits for a matchmade NotifyGameSetup and returns its game id, the recipient's team index,
// the number of players and the setup context.
func (tc *tcpClient) gameSetup() (gid, tidx int64, n int, ctx *tdf.Struct) {
	tc.t.Helper()
	f := tc.note(ComponentGameManager, NotifyGameSetupID)
	tdfcheck.Require(tc.t, "Blaze::GameManager::NotifyGameSetup", f.Payload)
	st := decode(tc.t, f)
	game, _ := st.Sub("GAME")
	gid, _ = game.Int("GID")
	pf, _ := st.Get("PROS")
	pros := pf.Value.(*tdf.List).Items
	tidx = -1
	for _, it := range pros {
		p := it.(*tdf.Struct)
		if pid, _ := p.Int("PID"); pid == tc.id {
			tidx, _ = p.Int("TIDX")
		}
	}
	rf, _ := st.Get("REAS")
	u := rf.Value.(*tdf.Union)
	if u.Member != setupReasonMatchmaking {
		tc.t.Fatalf("%s: setup reason member %d", tc.name, u.Member)
	}
	return gid, tidx, len(pros), u.Field.Value.(*tdf.Struct)
}

// A lone player in a 5v5 queue with MinPlayers 1 and no wait gets a game at once, after the reply.
func TestMatchmakingSolo(t *testing.T) {
	ts := mmServer(t, 5, 1, 0)
	a := ts.client("alpha@example.com")
	msid := a.queue(10800000)
	gid, tidx, n, ctx := a.gameSetup()
	usid, _ := ctx.Int("USID")
	sid, _ := ctx.Int("MSID")
	rslt, _ := ctx.Int("RSLT")
	if gid != 1 || tidx != 0 || n != 1 || usid != a.id || sid != msid || rslt != MatchmakingCreatedGame {
		t.Errorf("game %d team %d players %d ctx %v", gid, tidx, n, ctx)
	}
	if log := ts.log.String(); !strings.Contains(log, "matchmaking: game 1 \"alpha's Game\", 1 v 0 (full false") {
		t.Errorf("log:\n%s", log)
	}
}

// Two clients, 1v1: the first waits, the second completes the match; both get the same game on
// opposite teams.
func TestMatchmaking1v1(t *testing.T) {
	ts := mmServer(t, 1, 2, time.Hour)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	a.queue(10800000)
	a.none(ComponentGameManager, NotifyGameSetupID, 50*time.Millisecond)
	b.queue(10800000)
	ga, ta, na, ca := a.gameSetup()
	gb, tb, nb, cb := b.gameSetup()
	ra, _ := ca.Int("RSLT")
	rb, _ := cb.Int("RSLT")
	if ga != gb || ta == tb || na != 2 || nb != 2 || ra != MatchmakingCreatedGame || rb != MatchmakingJoinedNewGame {
		t.Errorf("a: game %d team %d n %d rslt %d; b: game %d team %d n %d rslt %d", ga, ta, na, ra, gb, tb, nb, rb)
	}
}

// Ten clients queue at once for 5v5: one game, five per team.
func TestMatchmaking5v5(t *testing.T) {
	ts := mmServer(t, 5, 10, time.Hour)
	var clients []*tcpClient
	for i := 0; i < 10; i++ {
		clients = append(clients, ts.client(fmt.Sprintf("p%d@example.com", i)))
	}
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.send(ComponentGameManager, CmdGameManagerStartMatchmaking, startMatchmakingRequest(0))
		}()
	}
	wg.Wait()
	var teams [2]int
	for _, c := range clients {
		gid, tidx, n, _ := c.gameSetup()
		if gid != 1 || n != 10 || tidx < 0 || tidx > 1 {
			t.Fatalf("%s: game %d team %d n %d", c.name, gid, tidx, n)
		}
		teams[tidx]++
	}
	if teams != [2]int{5, 5} {
		t.Errorf("teams %v", teams)
	}
}

// With match hosts (--gms-dir): a formed match starts one host (mode pvp, both players in the roster,
// team = TIDX + 1); NotifyGameSetup follows gameReady with the host's port; the players' finish is a
// pvp result (no FTUE flag); when the host has gone the players are removed with GAME_ENDED.
func TestMatchmakingHosted(t *testing.T) {
	got := make(chan gmshost.CreateGame, 1)
	var hosts *gmshost.Manager
	ts := newTCPServer(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 1, MinPlayers: 2})
		s.MMBots = true // B10d: a full match has no open seat, so no fillTeams
		l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
			gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
				got <- cg
				for _, r := range cg.Roster {
					if err := h.Send(gmshost.AddPlayer{User: r.User, Index: r.Index}); err != nil {
						return err
					}
				}
				return h.Send(gmshost.FinishGame{WinnerTeam: 2, DurationMs: 1000, Reason: "guardian"})
			})
		}}
		hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+3, GMSPort+3),
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
	cg := <-got
	if cg.Mode != gmshost.ModePvP || cg.Level != HostedLevel || len(cg.Roster) != 2 || cg.FillTeams != 0 ||
		!reflect.DeepEqual(cg.Roster[0], gmshost.RosterEntry{User: a.id, Name: "alpha", Index: 0, Team: 1}) ||
		!reflect.DeepEqual(cg.Roster[1], gmshost.RosterEntry{User: b.id, Name: "bravo", Index: 1, Team: 2}) {
		t.Errorf("createGame %+v", cg)
	}
	for _, c := range []*tcpClient{a, b} {
		f := c.note(ComponentGameManager, NotifyGameSetupID)
		game, _ := decode(t, f).Sub("GAME")
		hf, _ := game.Get("HNET")
		addr := hf.Value.(*tdf.List).Items[0].(*tdf.ListUnion).Value
		exip, _ := addr.Sub("EXIP")
		if port, _ := exip.Int("PORT"); port != GMSPort+3 {
			t.Errorf("%s: GMS port %d", c.name, port)
		}
		c.note(ComponentGameManager, NotifyGameStateChangeID) // POST_GAME at finishGame
		f = c.note(ComponentGameManager, NotifyPlayerRemovedID)
		if r, _ := decode(t, f).Int("REAS"); r != RemovedGameEnded {
			t.Errorf("%s: removed with %d", c.name, r)
		}
	}
	for deadline := time.Now().Add(5 * time.Second); hosts.Running() > 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("match host still running")
		}
	}
	acct, _ := ts.s.Store.Account(store.FirstUserID)
	if acct.Flags&store.FlagFTUEDone != 0 {
		t.Error("a PvP game set the FTUE flag")
	}
	if log := ts.log.String(); !strings.Contains(log, "ready 0.0 s after the match formed") ||
		!strings.Contains(log, "match host 1 starting (pid ") {
		t.Errorf("log:\n%s", log)
	}
}

// B10d: a lone player in a 5v5 queue gets a game with open seats (FillAfter 0); with MMBots its host is asked for
// bots up to the team size (createGame's fillTeams 5: the line docs/data/host_control_examples.jsonl pins), without
// it (dgserver --mm-bots=false) the seats stay empty (no fillTeams, B10b's 1 v 0). The descriptors stay the game's
// GameFlags (1) either way.
func TestMatchmakingHostedBots(t *testing.T) {
	const want = `{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"pvp","seed":7919,` +
		`"roster":[{"user":2000000001,"name":"alpha","index":0,"team":1,"noun":"","bot":false}],"descriptors":1,"fillTeams":5}`
	for _, c := range []struct {
		bots    bool
		fill    int
		logs    []string
		wantRaw string
	}{
		{true, 5, []string{`1 v 0 (full false, region ""): sessions [1] v []; open seats: bots up to 5 a team`,
			"player 2000000001, bots fill each team to 5; NotifyGameSetup follows gameReady"}, want},
		{false, 0, []string{`1 v 0 (full false, region ""): sessions [1] v []; open seats stay empty (--mm-bots=false)`},
			strings.Replace(want, `,"fillTeams":5`, "", 1)},
	} {
		got := make(chan gmshost.CreateGame, 1)
		var hosts *gmshost.Manager
		ts := newTCPServer(t, func(s *Server) {
			s.MM = matchmaker.New(matchmaker.Config{TeamSize: 5, MinPlayers: 1})
			s.MMBots = c.bots
			l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
				gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
					got <- cg
					return h.Send(gmshost.FinishGame{WinnerTeam: 1, DurationMs: 1000, Reason: "guardian"})
				})
			}}
			hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+5, GMSPort+5),
				ExitTimeout: time.Second})
			if err := hosts.Listen("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			s.Hosts = hosts
		})
		a := ts.client("alpha@example.com")
		id := a.send(ComponentGameManager, CmdGameManagerStartMatchmaking,
			startMatchmakingRequestAttrs(0, tdf.KV("GameFlags", "1"), tdf.KV("GameType", "1")))
		if f := a.reply(ComponentGameManager, CmdGameManagerStartMatchmaking, id); f.Type != fire.Reply {
			t.Fatalf("startMatchmaking %s", f.Type)
		}
		cg := <-got
		line, err := gmshost.Encode(cg)
		if err != nil {
			t.Fatal(err)
		}
		if cg.FillTeams != c.fill || cg.Descriptors != 1 || string(line) != c.wantRaw+"\n" {
			t.Errorf("bots %v: createGame\n got %s\nwant %s", c.bots, line, c.wantRaw)
		}
		a.note(ComponentGameManager, NotifyGameSetupID)
		for deadline := time.Now().Add(5 * time.Second); hosts.Running() > 0; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("match host still running")
			}
		}
		hosts.Close()
		log := ts.log.String()
		for _, w := range c.logs {
			if !strings.Contains(log, w) {
				t.Errorf("bots %v: log lacks %q:\n%s", c.bots, w, log)
			}
		}
	}
}

// D8b: a party's match on a match host: both members' roster entries carry the party's playgroup id (createGame's
// party, the line docs/data/host_control_examples.jsonl pins), so the host's karma tally counts the teammate's vote
// as a party vote. The party queues as in TestPartyMatchmakingGolden (B10a's goldens); a 2v2 queue with the bots in
// the open seats.
func TestMatchmakingHostedParty(t *testing.T) {
	const want = `{"t":"createGame","gameId":1,"level":"SM_Map_v5","mode":"pvp","seed":7919,"roster":[` +
		`{"user":2000000001,"name":"player","index":0,"team":1,"noun":"","bot":false,"party":1},` +
		`{"user":2000000002,"name":"second","index":1,"team":1,"noun":"","bot":false,"party":1}],"descriptors":1,"fillTeams":2}`
	got := make(chan gmshost.CreateGame, 1)
	var hosts *gmshost.Manager
	ps := newPipeSessionWith(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 2, MinPlayers: 1})
		s.MMBots = true
		l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
			gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
				got <- cg
				return h.Send(gmshost.AbortGame{Reason: "test"})
			})
		}}
		hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+6, GMSPort+6),
			ExitTimeout: time.Second})
		if err := hosts.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		s.Hosts = hosts
	})
	defer hosts.Close()
	ps.loginAs("player@example.com", 0)
	ps.send(6, 1, 2, ps.request("createplaygroup_party_request"))
	ps.expect(6, 1, fire.Reply, 2, "createplaygroup_party_reply")
	ps.expect(6, 0x33, fire.Notification, 0, "notifyjoinplaygroup_party_notification")
	two := ps.another()
	two.loginAs("second@example.com", 0)
	two.send(0x7802, 0x14, 2, ps.request("updatenetworkinfo_request"))
	two.expect(0x7802, 0x14, fire.Reply, 2, "")
	two.send(6, 3, 3, ps.request("joinplaygroup_request"))
	two.expect(6, 3, fire.Reply, 3, "createplaygroup_party_reply")
	two.expectUserAdded(store.FirstPersonaID, true) // B14: the leader as a user first
	two.next("NotifyJoinPlaygroup")
	ps.expectUserAdded(store.FirstPersonaID+1, true)
	ps.next("NotifyMemberJoinedPlaygroup")
	ps.send(4, 0x0D, 3, ps.request("startmatchmaking_party_request"))
	ps.expect(4, 0x0D, fire.Reply, 3, "startmatchmaking_reply")
	select {
	case cg := <-got:
		line, err := gmshost.Encode(cg)
		if err != nil {
			t.Fatal(err)
		}
		if string(line) != want+"\n" {
			t.Errorf("createGame\n got %s\nwant %s", line, want)
		}
		if len(cg.Roster) != 2 || cg.Roster[0].Party != 1 || cg.Roster[1].Party != 1 {
			t.Errorf("roster %+v", cg.Roster)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no createGame")
	}
	ps.client.Close() // the game's notifications to the two clients aren't read
	two.client.Close()
	<-ps.done
	<-two.done
}

// cancelMatchmaking: an empty reply, then NotifyMatchmakingFailed SESSION_CANCELED; a closed connection
// takes its session out of the queue; a session with a short DUR times out.
func TestMatchmakingCancelDisconnectTimeout(t *testing.T) {
	ts := mmServer(t, 5, 10, time.Hour)
	a := ts.client("alpha@example.com")
	msid := a.queue(0)
	id := a.send(ComponentGameManager, CmdGameManagerCancelMatchmaking, tdf.NewWriter().Int("MSID", msid).Bytes())
	tdfcheck.Require(t, "Blaze::GameManager::CancelMatchmakingRequest", tdf.NewWriter().Int("MSID", msid).Bytes())
	if f := a.reply(ComponentGameManager, CmdGameManagerCancelMatchmaking, id); f.Type != fire.Reply || len(f.Payload) != 0 {
		t.Errorf("cancel reply %+v", f)
	}
	f := a.note(ComponentGameManager, NotifyMatchmakingFailedID)
	tdfcheck.Require(t, "Blaze::GameManager::NotifyMatchmakingFailed", f.Payload)
	if r, _ := decode(t, f).Int("RSLT"); r != MatchmakingCanceled {
		t.Errorf("RSLT %d", r)
	}
	if ts.s.MM.Len() != 0 {
		t.Errorf("%d queued after cancel", ts.s.MM.Len())
	}

	b := ts.client("bravo@example.com")
	b.queue(0)
	b.c.Close()
	for deadline := time.Now().Add(5 * time.Second); ts.s.MM.Len() != 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("disconnected session still queued")
		}
	}

	c := ts.client("charlie@example.com")
	c.queue(100) // DUR 100 ms
	f = c.note(ComponentGameManager, NotifyMatchmakingFailedID)
	if r, _ := decode(t, f).Int("RSLT"); r != MatchmakingTimedOut {
		t.Errorf("RSLT %d", r)
	}
	// queueing twice is refused
	c.queue(0)
	id = c.send(ComponentGameManager, CmdGameManagerStartMatchmaking, startMatchmakingRequest(0))
	if f := c.reply(ComponentGameManager, CmdGameManagerStartMatchmaking, id); f.Type != fire.ErrorReply || f.Error != ErrMatchmakingExceededLimit {
		t.Errorf("second session: %s error %#x", f.Type, f.Error)
	}
}

// B10b: --mm-max-wait ends a session that asked for 3 h (the client's DUR) after the cap, and the test aid
// --mm-refuse answers every startMatchmaking with GAMEMANAGER_ERR_INVALID_MATCHMAKING_CRITERIA.
func TestMatchmakingMaxWaitAndRefuse(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 5, MinPlayers: 10, FillAfter: time.Hour, MaxWait: 100 * time.Millisecond})
	})
	a := ts.client("alpha@example.com")
	msid := a.queue(10800000)
	f := a.note(ComponentGameManager, NotifyMatchmakingFailedID)
	st := decode(t, f)
	if r, _ := st.Int("RSLT"); r != MatchmakingTimedOut {
		t.Errorf("RSLT %d", r)
	}
	if id, _ := st.Int("MSID"); id != msid {
		t.Errorf("MSID %d, want %d", id, msid)
	}
	if log := ts.log.String(); !strings.Contains(log, "timeout 100ms") || !strings.Contains(log, "wait at most 100ms") {
		t.Errorf("log:\n%s", log)
	}

	tr := newTCPServer(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 1, MinPlayers: 1})
		s.MMRefuse = true
	})
	b := tr.client("bravo@example.com")
	id := b.send(ComponentGameManager, CmdGameManagerStartMatchmaking, startMatchmakingRequest(10800000))
	if f := b.reply(ComponentGameManager, CmdGameManagerStartMatchmaking, id); f.Type != fire.ErrorReply || f.Error != ErrInvalidMatchmaking {
		t.Errorf("refused: %s error %#x", f.Type, f.Error)
	}
	b.none(ComponentGameManager, NotifyGameSetupID, 50*time.Millisecond)
	if tr.s.MM.Len() != 0 {
		t.Errorf("%d queued after a refusal", tr.s.MM.Len())
	}
}
