package blaze

import (
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// TEAMCHAT: a matchmade (not custom) hosted game completes each player's join at its GMS connection, as B15 did for
// custom games only: NotifyPlayerJoinCompleted {GID, PID} to every player, once per join, and again after a rejoin.
// The client's chat takes the local player's team from it (the rooms manager's onPlayerJoinComplete) and joins
// TG-<game>-<team>; without it every team line was dropped in the client.
func TestHostedJoinCompletedMatchmade(t *testing.T) {
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
		hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+6, GMSPort+6),
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
	for _, c := range []*tcpClient{a, b} {
		c.note(ComponentGameManager, NotifyGameSetupID)
		c.note(ComponentGameManager, NotifyGameStateChangeID) // IN_GAME
	}
	mesh := func(c *tcpClient) {
		t.Helper()
		w := tdf.NewWriter().Int("GID", 1).List("TARG", tdf.TypeStruct, []*tdf.Writer{
			tdf.NewWriter().Int("FLGS", 0).Int("PID", DedicatedServerID).Int("STAT", 2)})
		wantReply(t, "mesh", c.gm(CmdGameManagerUpdateMeshConnection, w.Bytes()))
	}
	completed := func(c *tcpClient) int64 {
		t.Helper()
		f := c.note(ComponentGameManager, NotifyPlayerJoinCompletedID)
		tdfcheck.Require(t, "Blaze::GameManager::NotifyPlayerJoinCompleted", f.Payload)
		st := decode(t, f)
		if gid, _ := st.Int("GID"); gid != 1 {
			t.Errorf("%s: join completed for game %d", c.name, gid)
		}
		pid, _ := st.Int("PID")
		return pid
	}
	mesh(a)
	mesh(b)
	mesh(a) // again: no second notification
	for _, c := range []*tcpClient{a, b} {
		done := map[int64]bool{completed(c): true, completed(c): true}
		if !done[a.id] || !done[b.id] {
			t.Errorf("%s: joins completed %v", c.name, done)
		}
		c.none(ComponentGameManager, NotifyPlayerJoinCompletedID, 50*time.Millisecond)
	}
	// a mesh connection to something else than the GMS, or not CONNECTED, completes nothing
	other := tdf.NewWriter().Int("GID", 1).List("TARG", tdf.TypeStruct, []*tdf.Writer{
		tdf.NewWriter().Int("FLGS", 0).Int("PID", a.id).Int("STAT", 2)})
	wantReply(t, "mesh to a player", b.gm(CmdGameManagerUpdateMeshConnection, other.Bytes()))

	// B drops and rejoins (G3): its new GMS connection completes its join again, for both
	b.c.Close()
	if r := <-removed; r != "BLAZESERVER_CONN_LOST" {
		t.Errorf("host got removePlayer %s", r)
	}
	a.note(ComponentGameManager, NotifyPlayerRemovedID)
	b2 := ts.client("bravo@example.com")
	id := b2.send(ComponentGameManager, CmdGameManagerJoinGame, tdf.NewWriter().Int("GID", 1).Int("TIDX", 1).Bytes())
	if f := b2.reply(ComponentGameManager, CmdGameManagerJoinGame, id); f.Type != fire.Reply {
		t.Fatalf("joinGame: %s error %#x", f.Type, f.Error)
	}
	b2.note(ComponentGameManager, NotifyGameSetupID)
	a.note(ComponentGameManager, NotifyPlayerJoiningID)
	mesh(b2)
	for _, c := range []*tcpClient{a, b2} {
		if pid := completed(c); pid != b.id {
			t.Errorf("%s: join completed for %d after the rejoin, want %d", c.name, pid, b.id)
		}
		c.none(ComponentGameManager, NotifyPlayerJoinCompletedID, 50*time.Millisecond)
	}
	if log := ts.log.String(); !strings.Contains(log, "game 1: player 2000000002's join completed (its GMS connection is up); NotifyPlayerJoinCompleted") {
		t.Errorf("log:\n%s", log)
	}
}
