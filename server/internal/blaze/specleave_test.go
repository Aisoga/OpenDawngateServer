package blaze

import (
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/tdf"
)

// SPECLEAVE: spectators never keep a started custom game open. These tests play a custom game with a fake match host
// (newCustomHostServer): A creates it (team 1, the admin), B spectates or joins a team as each case needs.

// specGame creates A's custom game and returns the clients; with spectate B joins as a spectator, else onto a team.
func specGame(t *testing.T, ts *tcpServer, host *customHost, spectate bool) (a, b *tcpClient) {
	t.Helper()
	g := golden.Load(t)
	a, b = ts.client("alpha@example.com"), ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	join := "joingame_browser_request"
	if spectate {
		join = "joingame_spectate_request"
	}
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, join))))
	b.note(ComponentGameManager, NotifyGameSetupID)
	return a, b
}

func specStart(t *testing.T, host *customHost, admin *tcpClient) {
	t.Helper()
	g := golden.Load(t)
	wantReply(t, "start", admin.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	hostNext[gmshost.StartGame](t, host)
}

func specLeave(c *tcpClient, gid, pid int64) []byte {
	return tdf.NewWriter().Int("CNTX", 0).Int("GID", gid).Int("PID", pid).Int("REAS", 6).Bytes()
}

// specSwap moves the admin's player pid to team.
func specSwap(t *testing.T, admin *tcpClient, pid, team int64) {
	t.Helper()
	wantReply(t, "swap", admin.gm(CmdGameManagerSwapPlayersTeam, tdf.NewWriter().Int("GID", 1).List("LGAM", tdf.TypeStruct,
		[]*tdf.Writer{tdf.NewWriter().Int("PID", pid).Int("TIDX", team)}).Bytes()))
}

// wantDestroyed waits for c's NotifyPlayerRemoved GAME_DESTROYED (skipping the other removals before it).
func wantDestroyed(t *testing.T, c *tcpClient) {
	t.Helper()
	for range 4 {
		if reas, _ := decode(t, c.note(ComponentGameManager, NotifyPlayerRemovedID)).Int("REAS"); reas == RemovedGameDestroyed {
			return
		}
	}
	t.Fatalf("%s: no NotifyPlayerRemoved GAME_DESTROYED", c.name)
}

func wantGone(t *testing.T, ts *tcpServer) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ts.s.hosted.get(1) != nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("game 1 still hosted")
		}
	}
}

// noAbort checks that the host gets no abortGame within d.
func noAbort(t *testing.T, host *customHost, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-host.msgs:
			if ab, ok := m.(gmshost.AbortGame); ok {
				t.Fatalf("host got abortGame %q", ab.Reason)
			}
		case <-deadline:
			return
		}
	}
}

// The last team player leaves a started game with a spectator in it: the host gets removePlayer and abortGame
// (SpectatorsOnlyReason), the spectator NotifyPlayerRemoved GAME_DESTROYED, and the game is gone.
func TestSpecLeaveLastTeamPlayerLeaves(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	a, b := specGame(t, ts, host, true)
	specStart(t, host, a)
	wantReply(t, "A leaves", a.gm(CmdGameManagerRemovePlayer, specLeave(a, 1, a.id)))
	if rp := hostNext[gmshost.RemovePlayer](t, host); rp.User != a.id {
		t.Errorf("removePlayer %d", rp.User)
	}
	if ab := hostNext[gmshost.AbortGame](t, host); ab.Reason != SpectatorsOnlyReason {
		t.Errorf("abortGame %q", ab.Reason)
	}
	wantDestroyed(t, b)
	wantGone(t, ts)
	if log := ts.log.String(); !strings.Contains(log, "game 1: its last team player left, 1 spectator(s) remain: the game ends") {
		t.Errorf("log lacks the spectators-only line")
	}
}

// The last team player's client crashes (its Blaze connection closes): the same end, no rejoin wait for the
// spectator's sake (without a spectator "no players left" ends it at once too).
func TestSpecLeaveLastTeamPlayerConnectionLost(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	a, b := specGame(t, ts, host, true)
	specStart(t, host, a)
	a.c.Close()
	if rp := hostNext[gmshost.RemovePlayer](t, host); rp.User != a.id || rp.Reason != "BLAZESERVER_CONN_LOST" {
		t.Errorf("removePlayer %+v", rp)
	}
	if ab := hostNext[gmshost.AbortGame](t, host); ab.Reason != SpectatorsOnlyReason {
		t.Errorf("abortGame %q", ab.Reason)
	}
	wantDestroyed(t, b)
	wantGone(t, ts)
}

// A spectator's leave changes nothing for the players: removePlayer only; the team player's later leave then ends
// the game by the old rule ("no players left").
func TestSpecLeaveSpectatorLeaves(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	a, b := specGame(t, ts, host, true)
	specStart(t, host, a)
	wantReply(t, "B leaves", b.gm(CmdGameManagerRemovePlayer, specLeave(b, 1, b.id)))
	if rp := hostNext[gmshost.RemovePlayer](t, host); rp.User != b.id {
		t.Errorf("removePlayer %d", rp.User)
	}
	noAbort(t, host, 300*time.Millisecond)
	if ts.s.hosted.get(1) == nil {
		t.Fatal("the game ended with its spectator's leave")
	}
	wantReply(t, "A leaves", a.gm(CmdGameManagerRemovePlayer, specLeave(a, 1, a.id)))
	if ab := hostNext[gmshost.AbortGame](t, host); ab.Reason != "no players left" {
		t.Errorf("abortGame %q", ab.Reason)
	}
	wantGone(t, ts)
}

// Before the start the lobby's rules stay: a team player leaving a lobby whose admin spectates keeps the lobby (it is
// what --custom-autostart starts); the admin's own leave still ends it.
func TestSpecLeaveLobbyUnchanged(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	a, b := specGame(t, ts, host, false)
	specSwap(t, a, a.id, SpectatorTeam)
	wantReply(t, "B leaves", b.gm(CmdGameManagerRemovePlayer, specLeave(b, 1, b.id)))
	noAbort(t, host, 300*time.Millisecond)
	if ts.s.hosted.get(1) == nil {
		t.Fatal("the lobby ended with its last team player's leave")
	}
	wantReply(t, "A leaves", a.gm(CmdGameManagerRemovePlayer, specLeave(a, 1, a.id)))
	if ab := hostNext[gmshost.AbortGame](t, host); ab.Reason != "no players left" {
		t.Errorf("abortGame %q", ab.Reason)
	}
	wantGone(t, ts)
}

// A spectating admin kicks the last team player of the started game: the same end as a leave.
func TestSpecLeaveKickLastTeamPlayer(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	a, b := specGame(t, ts, host, false)
	specSwap(t, a, a.id, SpectatorTeam)
	specStart(t, host, a)
	wantReply(t, "A kicks B", a.gm(CmdGameManagerRemovePlayer, tdf.NewWriter().Int("CNTX", 0).Int("GID", 1).Int("PID", b.id).
		Int("REAS", RemovedPlayerKicked).Bytes()))
	if ab := hostNext[gmshost.AbortGame](t, host); ab.Reason != SpectatorsOnlyReason {
		t.Errorf("abortGame %q", ab.Reason)
	}
	wantDestroyed(t, a)
	wantGone(t, ts)
}

// A game --custom-autostart started with spectators alone (bots on both teams) had no team player to lose: a second
// spectator's join and leave don't end it.
func TestSpecLeaveAutoStartedSpectatorGame(t *testing.T) {
	ts, host := newCustomHostServer(t, func(s *Server) {
		s.CustomBots = true
		s.CustomAutoStart = 50 * time.Millisecond
	})
	a, b := specGame(t, ts, host, true)
	specSwap(t, a, a.id, SpectatorTeam)
	hostNext[gmshost.StartGame](t, host)
	wantReply(t, "B leaves", b.gm(CmdGameManagerRemovePlayer, specLeave(b, 1, b.id)))
	noAbort(t, host, 300*time.Millisecond)
	if ts.s.hosted.get(1) == nil {
		t.Fatal("the spectators' game ended with a spectator's leave")
	}
}
