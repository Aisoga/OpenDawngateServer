package blaze

import (
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf/tdfcheck"
)

// B12: Authentication::logout (0x46) as the client sent it after its AFK kick (c1b_run11: request id 71, no
// payload) gets an empty reply (golden frames from tdf_golden.py; the RPC job has no response class, so there
// is no member table to check), and the players still in a game get NotifyPlayerRemoved BLAZESERVER_CONN_LOST
// for one whose session ended (golden, member table).
func TestLogoutMatchesGolden(t *testing.T) {
	g := golden.Load(t)
	note := PlayerRemovedNotification(1, store.FirstPersonaID, RemovedBlazeServerConnLost)
	if got, want := hex.EncodeToString(note), g.WriterCase(t, "notifyplayerremoved_connlost_notification").Hex; got != want {
		t.Errorf("NotifyPlayerRemoved BLAZESERVER_CONN_LOST\n got %s\nwant %s", got, want)
	}
	tdfcheck.Require(t, "Blaze::GameManager::NotifyPlayerRemoved", note)

	ps := newPipeSession(t)
	ps.loginAs("player@example.com", 0)
	ps.client.Write(golden.Bytes(t, ps.g.Frame(t, "logout_request").Hex))
	ps.expectFrame("logout_reply")
	// a second logout, and one on a connection that never logged in: still an empty reply
	ps.send(ComponentAuthentication, CmdAuthLogout, 72, nil)
	ps.expect(ComponentAuthentication, CmdAuthLogout, fire.Reply, 72, "")
	log := ps.close()
	for _, want := range []string{
		"[blaze] <- Authentication(0x0001) cmd 0x0046 request id 71 err 0x0, 0 bytes",
		"[blaze]    account 1000000001 (persona 2000000001) logged out; its session ends\n",
		"[blaze] -> logout reply, 0 bytes:\n",
		"[blaze]    (logout without a login on this connection)\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// The AFK kick's path (c1b_run11), with the answer the client lacked: a player in a hosted practice game
// logs out; after the reply the host hears removePlayer BLAZESERVER_CONN_LOST and abortGame, the game ends,
// and the connection, still open, takes a fresh login and a new game; closing it later ends only that game.
func TestHostedGameLogout(t *testing.T) {
	hs := newHostedSession(t, nil, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		if err := h.Send(gmshost.AddPlayer{User: store.FirstPersonaID}); err != nil {
			return err
		}
		return receive(hs, h)
	})
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	if cg := hs.hostGot().(gmshost.CreateGame); cg.GameID != 1 {
		t.Fatalf("createGame %+v", cg)
	}
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
	// the client's AFK kick: it leaves the chat playgroup (none here) and logs out
	hs.send(ComponentAuthentication, CmdAuthLogout, 71, nil)
	hs.expect(ComponentAuthentication, CmdAuthLogout, fire.Reply, 71, "")
	if m := hs.hostGot(); m != (gmshost.RemovePlayer{User: store.FirstPersonaID, Reason: "BLAZESERVER_CONN_LOST"}) {
		t.Errorf("host got %#v", m)
	}
	if m := hs.hostGot(); m != (gmshost.AbortGame{Reason: "no players left"}) {
		t.Errorf("host got %#v", m)
	}
	hs.waitIdle()
	if hs.s.hosted.get(1) != nil {
		t.Error("game 1 still hosted after the logout")
	}
	// a request that needs a login is refused now (left unanswered, logged)
	hs.send(0x81F, 0x76, 72, nil)

	// a fresh login on the same connection, and a new practice game
	hs.send(1, 0x28, 73, hs.request("login_request"))
	hs.expect(1, 0x28, fire.Reply, 73, "login_reply")
	hs.send(1, 0x6E, 74, hs.request("loginpersona_request"))
	f := hs.next("loginPersona reply")
	if buid, _ := decode(t, f).Int("BUID"); f.Type != fire.Reply || f.ID != 74 || buid != store.FirstPersonaID {
		t.Errorf("loginPersona after the logout: %s id %d BUID %d", f.Type, f.ID, buid)
	}
	hs.next("UserAuthenticated")
	hs.next("UserSessionExtendedDataUpdate")
	hs.send(4, 0x19, 75, hs.request("creategame_request"))
	f = hs.next("resetDedicatedServer reply")
	if gid, _ := decode(t, f).Int("GID"); f.Type != fire.Reply || f.ID != 75 || gid != 2 {
		t.Errorf("second game: %s id %d GID %d", f.Type, f.ID, gid)
	}
	if cg := hs.hostGot().(gmshost.CreateGame); cg.GameID != 2 || len(cg.Roster) != 1 || cg.Roster[0].User != store.FirstPersonaID {
		t.Errorf("second createGame %+v", cg)
	}
	hs.next("second NotifyGameSetup")
	// quitting the client now ends game 2 only (game 1's hooks ran at the logout)
	hs.close()
	if m := hs.hostGot(); m != (gmshost.RemovePlayer{User: store.FirstPersonaID, Reason: "BLAZESERVER_CONN_LOST"}) {
		t.Errorf("host got %#v", m)
	}
	if m := hs.hostGot(); m != (gmshost.AbortGame{Reason: "no players left"}) {
		t.Errorf("host got %#v", m)
	}
	hs.waitIdle()
	log := hs.log.String()
	for _, want := range []string{
		"[blaze]    account 1000000001 (persona 2000000001) logged out; its session ends\n",
		"[blaze]    game 1: player 2000000001 left (BLAZESERVER_CONN_LOST); 0 still in the game\n",
		"[host] match 1: ended after ",
		"(getCompleteData before login; not answered)",
		"[blaze]    game 2: player 2000000001 left (BLAZESERVER_CONN_LOST); 0 still in the game\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if n := strings.Count(log, "game 1: player 2000000001 left"); n != 1 {
		t.Errorf("game 1 left %d times", n)
	}
}

// A 1v1 hosted PvP game whose players share the game's chat playgroup: one logs out in the game. The other
// gets NotifyPlayerRemoved BLAZESERVER_CONN_LOST for it and NotifyMemberRemovedFromPlaygroup; the host hears
// removePlayer for it but goes on (a player is left). The one who logged out logs in again on the same
// connection; when the other quits, the host gets its removePlayer and abortGame.
func TestHostedPvPLogout(t *testing.T) {
	g := golden.Load(t)
	got := make(chan gmshost.Message, 16)
	var hosts *gmshost.Manager
	ts := newTCPServer(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 1, MinPlayers: 2})
		l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
			gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
				got <- cg
				for _, r := range cg.Roster {
					if err := h.Send(gmshost.AddPlayer{User: r.User, Index: r.Index}); err != nil {
						return err
					}
				}
				for {
					m, err := h.Recv(5 * time.Second)
					if err != nil {
						return err
					}
					got <- m
					if _, ok := m.(gmshost.AbortGame); ok {
						return nil
					}
				}
			})
		}}
		hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+4, GMSPort+4),
			ExitTimeout: time.Second})
		if err := hosts.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		s.Hosts = hosts
	})
	t.Cleanup(hosts.Close)
	hostGot := func() gmshost.Message {
		t.Helper()
		select {
		case m := <-got:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("the fake host received nothing")
			return nil
		}
	}
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	a.queue(0)
	b.queue(0)
	if cg := hostGot().(gmshost.CreateGame); len(cg.Roster) != 2 {
		t.Fatalf("createGame %+v", cg)
	}
	var gid int64
	for _, c := range []*tcpClient{a, b} {
		gid, _, _, _ = c.gameSetup()
		// the game's chat playgroup GG-<id>, as the client asks for it (B11's request; key and name GG-1)
		wantOK(t, "chat playgroup", c.pgCall(CmdPlaygroupsCreatePlaygroup, golden.Bytes(t, decodeCase(t, g, "createplaygroup_request"))))
		c.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	}
	a.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID)

	id := a.send(ComponentAuthentication, CmdAuthLogout, nil)
	if f := a.reply(ComponentAuthentication, CmdAuthLogout, id); f.Type != fire.Reply || len(f.Payload) != 0 {
		t.Errorf("logout reply %+v", f)
	}
	f := b.note(ComponentGameManager, NotifyPlayerRemovedID)
	if hex.EncodeToString(f.Payload) != hex.EncodeToString(PlayerRemovedNotification(gid, a.id, RemovedBlazeServerConnLost)) {
		t.Errorf("b got NotifyPlayerRemoved %x", f.Payload)
	}
	f = b.note(ComponentPlaygroups, NotifyMemberRemovedFromPlaygroupID)
	if hex.EncodeToString(f.Payload) != hex.EncodeToString(MemberRemovedFromPlaygroupNotification(1, a.id, PlaygroupMemberRemoveDisconnected)) {
		t.Errorf("b got NotifyMemberRemovedFromPlaygroup %x", f.Payload)
	}
	if m := hostGot(); m != (gmshost.RemovePlayer{User: a.id, Reason: "BLAZESERVER_CONN_LOST"}) {
		t.Errorf("host got %#v", m)
	}
	a.none(ComponentGameManager, NotifyPlayerRemovedID, 100*time.Millisecond) // the leaver itself gets none
	if hosts.Running() != 1 {
		t.Errorf("%d hosts running, want 1 (bravo is still in the game)", hosts.Running())
	}

	first := a.id
	a.login("alpha@example.com")
	if a.id != first {
		t.Errorf("login after the logout: persona %d, want %d", a.id, first)
	}
	b.c.Close()
	if m := hostGot(); m != (gmshost.RemovePlayer{User: b.id, Reason: "BLAZESERVER_CONN_LOST"}) {
		t.Errorf("host got %#v", m)
	}
	if m := hostGot(); m != (gmshost.AbortGame{Reason: "no players left"}) {
		t.Errorf("host got %#v", m)
	}
	for deadline := time.Now().Add(5 * time.Second); hosts.Running() > 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("match host still running")
		}
	}
	a.none(ComponentGameManager, NotifyPlayerRemovedID, 50*time.Millisecond)
	if log := ts.log.String(); !strings.Contains(log, "player "+strconv.FormatInt(first, 10)+" left (BLAZESERVER_CONN_LOST); 1 still in the game") {
		t.Errorf("log:\n%s", log)
	}
}

// decodeCase returns a golden decode case's hex.
func decodeCase(t *testing.T, g *golden.Data, name string) string {
	t.Helper()
	for _, c := range g.Decode {
		if c.Name == name {
			return c.Hex
		}
	}
	t.Fatalf("no decode case %q", name)
	return ""
}
