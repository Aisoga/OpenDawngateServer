package blaze

import (
	"bytes"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// C1's notifications, written independently by tdf_golden.py from the member tables, and checked
// against the client's tables.
func TestC1NotificationsMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range []struct {
		name, class string
		got         []byte
	}{
		{"notifygamestatechange_notification", "Blaze::GameManager::NotifyGameStateChange", GameStateChangeNotification(1, GameStatePostGame)},
		{"notifyplayerremoved_notification", "Blaze::GameManager::NotifyPlayerRemoved", PlayerRemovedNotification(1, store.FirstPersonaID, RemovedGameEnded)},
		{"notifyplayerremoved_playerleft_notification", "Blaze::GameManager::NotifyPlayerRemoved", PlayerRemovedNotification(1, store.FirstPersonaID, 6)},
	} {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
		tdfcheck.Require(t, c.class, c.got)
	}
	tdfcheck.Require(t, "Blaze::GameManager::NotifyGameStateChange", GameStateChangeNotification(1, GameStateInGame))
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// hostedSession is a pipe session whose server starts fake match hosts (gmshost.FakeLauncher) through
// a real gmshost.Manager on an ephemeral control port. The pool is the single port 10042 (a number
// only: nothing binds it), so NotifyGameSetup is B8's golden payload.
type hostedSession struct {
	*pipeSession
	s    *Server
	m    *gmshost.Manager
	l    *gmshost.FakeLauncher
	log  *lockedBuf
	msgs chan gmshost.Message // what the fake host received after createGame
}

func newHostedSession(t *testing.T, launchErr error, script func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error) *hostedSession {
	return newHostedSessionAt(t, "", "", launchErr, script)
}

// newHostedSessionAt is newHostedSession with the server's public host and the hosts' bind address (A5;
// "" = the defaults, 127.0.0.1).
func newHostedSessionAt(t *testing.T, public, bind string, launchErr error, script func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error) *hostedSession {
	return newHostedSessionFull(t, public, bind, launchErr, nil, script)
}

// newHostedSessionFull is newHostedSessionAt with an extra server setup (B7c: content, a fixed random
// source).
func newHostedSessionFull(t *testing.T, public, bind string, launchErr error, extra func(s *Server),
	script func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error) *hostedSession {
	hs := &hostedSession{log: &lockedBuf{}, msgs: make(chan gmshost.Message, 16)}
	hs.l = &gmshost.FakeLauncher{Err: launchErr, Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
		gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
			hs.msgs <- cg
			return script(hs, h, p)
		})
	}}
	hs.pipeSession = newPipeSessionWith(t, func(s *Server) {
		s.Log = logging.New(hs.log)
		if public != "" {
			s.Host = public
		}
		hs.m = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: hs.l, Pool: gmshost.NewPool(GMSPort, GMSPort),
			BindHost: bind, HeartbeatTimeout: 10 * time.Second, ExitTimeout: time.Second})
		if err := hs.m.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		s.Hosts = hs.m
		hs.s = s
		if extra != nil {
			extra(s)
		}
	})
	t.Cleanup(hs.m.Close)
	return hs
}

// login runs login and loginPersona (ids 0, 1).
func (hs *hostedSession) login() {
	hs.send(1, 0x28, 0, hs.request("login_request"))
	hs.expect(1, 0x28, fire.Reply, 0, "login_reply")
	hs.send(1, 0x6E, 1, hs.request("loginpersona_request"))
	hs.expect(1, 0x6E, fire.Reply, 1, "loginpersona_reply")
	hs.next("UserAuthenticated")
	hs.next("UserSessionExtendedDataUpdate")
}

// expectNote checks the next frame is GameManager notification id with payload want.
func (hs *hostedSession) expectNote(id uint16, want []byte) {
	hs.t.Helper()
	f := hs.next("notification")
	if f.Component != ComponentGameManager || f.Command != id || f.Type != fire.Notification {
		hs.t.Fatalf("got %#x/%#x %s, want GameManager notification %#x", f.Component, f.Command, f.Type, id)
	}
	if !bytes.Equal(f.Payload, want) {
		hs.t.Errorf("notification %#x\n got %x\nwant %x", id, f.Payload, want)
	}
}

// hostGot waits for the next message the fake host received.
func (hs *hostedSession) hostGot() gmshost.Message {
	hs.t.Helper()
	select {
	case m := <-hs.msgs:
		return m
	case <-time.After(5 * time.Second):
		hs.t.Fatal("the fake host received nothing")
		return nil
	}
}

func (hs *hostedSession) waitIdle() {
	hs.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); hs.m.Running() > 0; {
		if time.Now().After(deadline) {
			hs.t.Fatal("match host still running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// receive forwards what the host gets until abortGame, then returns (the host exits 0).
func receive(hs *hostedSession, h *gmshost.HostConn) error {
	for {
		m, err := h.Recv(5 * time.Second)
		if err != nil {
			return err
		}
		hs.msgs <- m
		if _, ok := m.(gmshost.AbortGame); ok {
			return nil
		}
	}
}

// PRACTICE DAWNGATE with a match host: the reply comes at once, NotifyGameSetup (B8's payload) once
// the host is ready; the host's phases become game state changes; finishGame sets the FTUE flag of
// the player who joined; when the host has exited the player is removed with GAME_ENDED.
func TestHostedPracticeGame(t *testing.T) {
	hs := newHostedSession(t, nil, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		for _, m := range []gmshost.Message{gmshost.AddPlayer{User: store.FirstPersonaID},
			gmshost.GMSUpdate{Phase: gmshost.PhaseInGame}, gmshost.PlayerFinish{User: store.FirstPersonaID},
			gmshost.FinishGame{WinnerTeam: 1, DurationMs: 61500, Reason: "guardian"}} {
			if err := h.Send(m); err != nil {
				return err
			}
		}
		return nil
	})
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	cg := hs.hostGot().(gmshost.CreateGame)
	want := gmshost.CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: "practice", Seed: 7919,
		Roster: []gmshost.RosterEntry{{User: store.FirstPersonaID, Name: "player", Index: 0, Team: 1}}}
	if cg.GameID != want.GameID || cg.Level != want.Level || cg.Mode != want.Mode || cg.Seed != want.Seed ||
		len(cg.Roster) != 1 || !reflect.DeepEqual(cg.Roster[0], want.Roster[0]) || cg.ShaperSelect {
		t.Errorf("createGame %+v", cg)
	}
	if cg.Descriptors != 0x50C { // D8-live: the game's GameFlags ("1292" in B8's createGame request)
		t.Errorf("createGame descriptors %#x, want 0x50C", cg.Descriptors)
	}
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
	hs.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(1, GameStateInGame))
	// B7c: the game counted (a win: the main menu's wins), then POST_GAME
	hs.expect(0x7802, NotifyUserSessionExtendedDataUpdate, fire.Notification, 0, "b7c_extendeddataupdate_notification")
	hs.expect(4, NotifyGameStateChangeID, fire.Notification, 0, "notifygamestatechange_notification")
	hs.expect(4, NotifyPlayerRemovedID, fire.Notification, 0, "notifyplayerremoved_notification")
	hs.waitIdle()
	// the client leaves on its own afterwards: the game has no host any more, so only the empty reply
	hs.send(4, 0x0B, 3, tdf.NewWriter().Int("GID", 1).Int("PID", store.FirstPersonaID).Int("REAS", 6).Bytes())
	hs.expect(4, 0x0B, fire.Reply, 3, "")
	acct, _ := hs.s.Store.Account(store.FirstUserID)
	if acct.Flags&store.FlagFTUEDone == 0 {
		t.Error("FTUE flag not set after the practice game")
	}
	hs.close()
	log := hs.log.String()
	for _, want := range []string{
		"[blaze]    game 1 \"player's Game\": match host 1 starting (pid ",
		"GMS port 10042), player 2000000001; NotifyGameSetup follows gameReady",
		"[host] match 1: started pid ",
		"[blaze]    game 1: GMS 127.0.0.1:10042 (HNET) ready 0.0 s after resetDedicatedServer",
		"[blaze] -> GameManager::NotifyGameSetup notification, ",
		"[blaze]    game 1: player 2000000001 joined the GMS (index 0)",
		"[blaze]    game 1 finished: winner team 1, 61.5 s, guardian",
		"[blaze]    account 1000000001: FTUE done (practice game 1 finished)",
		"[blaze] -> GameManager::NotifyPlayerRemoved notification, ",
		"[host] match 1: ended after ",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// The client leaves (removePlayer PLAYER_LEFT): it gets NotifyPlayerRemoved PLAYER_LEFT after the reply
// (C1b), the host hears removePlayer, then abortGame, and the game ends without a result and without a
// further notification to the player who left.
func TestHostedGamePlayerLeaves(t *testing.T) {
	hs := newHostedSession(t, nil, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		h.Send(gmshost.AddPlayer{User: store.FirstPersonaID})
		return receive(hs, h)
	})
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	hs.hostGot()
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
	hs.send(4, 0x0B, 3, tdf.NewWriter().Int("GID", 1).Int("PID", store.FirstPersonaID).Int("REAS", 6).Bytes())
	hs.expect(4, 0x0B, fire.Reply, 3, "")
	hs.expect(4, NotifyPlayerRemovedID, fire.Notification, 0, "notifyplayerremoved_playerleft_notification")
	if m := hs.hostGot(); m != (gmshost.RemovePlayer{User: store.FirstPersonaID, Reason: "PLAYER_LEFT"}) {
		t.Errorf("host got %#v", m)
	}
	if m := hs.hostGot(); m != (gmshost.AbortGame{Reason: "no players left"}) {
		t.Errorf("host got %#v", m)
	}
	hs.waitIdle()
	acct, _ := hs.s.Store.Account(store.FirstUserID)
	if acct.Flags&store.FlagFTUEDone != 0 {
		t.Error("FTUE flag set by an aborted game")
	}
	hs.close()
	if log := hs.log.String(); !strings.Contains(log, "[blaze]    game 1: player 2000000001 left (PLAYER_LEFT); 0 still in the game") ||
		!strings.Contains(log, "aborted: abortGame: no players left") {
		t.Errorf("log:\n%s", log)
	}
}

// Quitting the client closes its Blaze connection: that ends the host too.
func TestHostedGameConnectionClosed(t *testing.T) {
	hs := newHostedSession(t, nil, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		return receive(hs, h)
	})
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	hs.hostGot()
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
	hs.close()
	if m := hs.hostGot(); m != (gmshost.RemovePlayer{User: store.FirstPersonaID, Reason: "BLAZESERVER_CONN_LOST"}) {
		t.Errorf("host got %#v", m)
	}
	if m := hs.hostGot(); m != (gmshost.AbortGame{Reason: "no players left"}) {
		t.Errorf("host got %#v", m)
	}
	hs.waitIdle()
}

// A host that dies after gameReady: the player is removed with GAME_DESTROYED. One that dies before:
// NotifyGameSetup names its (dead) port, B8's failed-connection path.
func TestHostedGameHostDies(t *testing.T) {
	t.Run("after ready", func(t *testing.T) {
		hs := newHostedSession(t, nil, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
			h.Close()
			p.Exit(5)
			return errors.New("crash")
		})
		hs.login()
		hs.send(4, 0x19, 2, hs.request("creategame_request"))
		hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
		hs.hostGot()
		hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
		hs.expectNote(NotifyPlayerRemovedID, PlayerRemovedNotification(1, store.FirstPersonaID, RemovedGameDestroyed))
		hs.waitIdle()
		hs.close()
		if log := hs.log.String(); !strings.Contains(log, "[blaze]    game 1: match host aborted (exited with code 5 without finishGame)") {
			t.Errorf("log:\n%s", log)
		}
	})
	t.Run("before ready", func(t *testing.T) {
		hs := newHostedSession(t, nil, nil)
		hs.l.Run = func(env gmshost.Env, p *gmshost.FakeProcess) { p.Exit(7) } // never dials in
		hs.login()
		hs.send(4, 0x19, 2, hs.request("creategame_request"))
		hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
		hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
		hs.waitIdle()
		hs.close()
		if log := hs.log.String(); !strings.Contains(log, "match host gone before gameReady (exited with code 7 without finishGame); NotifyGameSetup names the dead GMS 127.0.0.1:10042") {
			t.Errorf("log:\n%s", log)
		}
	})
}

// H5: a host whose watchdog saw its tick stall reports it (the fatal reportGMSError, after a step exception that
// was only counted), logs out and exits with code 6: the player is removed with GAME_DESTROYED (the client goes
// back to the main menu, as after a host crash), the port is free, and PRACTICE again starts the next game on a new
// host. The two reports are the example lines in docs/data/host_control_examples.jsonl.
func TestHostedGameStalls(t *testing.T) {
	stalled := gmshost.GMSError{Code: gmshost.ErrTickStalled, Fatal: true, Text: "no server tick for 10.0 s (tick 1834, " +
		"match 61.0 s); the engine thread was in step test.stall for 10.0 s, eip 77A42F6C (ntdll.dll+0x72F6C); minidump watchdog.dmp"}
	stepped := gmshost.GMSError{Code: gmshost.ErrStepException, Text: "step tick.lua threw C0000005 at 0085C1A3 " +
		"(exception #1 of this step); that run was skipped, the tick goes on"}
	var hosts atomic.Int32
	hs := newHostedSession(t, nil, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		if hosts.Add(1) > 1 { // the next game's host runs until the client quits
			return receive(hs, h)
		}
		for _, m := range []gmshost.Message{gmshost.AddPlayer{User: store.FirstPersonaID},
			gmshost.GMSUpdate{Phase: gmshost.PhaseInGame}, stepped, stalled, gmshost.LogoutGMS{}} {
			if err := h.Send(m); err != nil {
				return err
			}
		}
		h.Close()
		p.Exit(6) // the watchdog's exit code
		return errors.New("stalled")
	})
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	hs.hostGot()
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
	hs.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(1, GameStateInGame))
	hs.expectNote(NotifyPlayerRemovedID, PlayerRemovedNotification(1, store.FirstPersonaID, RemovedGameDestroyed))
	hs.waitIdle()
	if hs.s.hosted.get(1) != nil {
		t.Error("game 1 still hosted after its host gave up")
	}
	acct, _ := hs.s.Store.Account(store.FirstUserID)
	if acct.Flags&store.FlagFTUEDone != 0 {
		t.Error("FTUE flag set by a stalled game")
	}
	// the slot is free: PRACTICE again starts game 2 on a new host, on the same GMS port
	hs.send(4, 0x19, 3, hs.request("creategame_request"))
	f := hs.next("resetDedicatedServer reply")
	if gid, _ := decode(t, f).Int("GID"); f.Type != fire.Reply || f.ID != 3 || gid != 2 {
		t.Errorf("next game: %s id %d GID %d", f.Type, f.ID, gid)
	}
	if cg := hs.hostGot().(gmshost.CreateGame); cg.GameID != 2 || len(cg.Roster) != 1 || cg.Roster[0].User != store.FirstPersonaID {
		t.Errorf("next createGame %+v", cg)
	}
	if f := hs.next("next NotifyGameSetup"); f.Component != ComponentGameManager || f.Command != 0x14 {
		t.Errorf("next game: %#x/%#x, want NotifyGameSetup", f.Component, f.Command)
	}
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
		"[host] match 1: host error 4 (step exception, fatal false): step tick.lua threw C0000005",
		"[host] match 1: host error 2 (tick stalled, fatal true): no server tick for 10.0 s",
		"[blaze]    game 1: the match host gave up: host error 2 (tick stalled): no server tick for 10.0 s (tick 1834",
		"[blaze]    game 1: the match host sent 1 non-fatal error report(s) (step exceptions)",
		"[blaze]    game 1: match host aborted (host error 2 (tick stalled): no server tick for 10.0 s",
		"exit code 6, killed false; port 10042 free",
		"[host] match 2: started pid ",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// No host can be started (launch error, or the pool is exhausted): an error reply,
// GAMEMANAGER_ERR_DYNAMIC_GAME_CREATION_FAILED_NO_CAPACITY (C1b: the error the client shows a message for).
func TestHostedGameNoHost(t *testing.T) {
	hs := newHostedSession(t, errors.New("no Dawngate.exe"), nil)
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	f := hs.next("error reply")
	if f.Component != 4 || f.Command != 0x19 || f.Type != fire.ErrorReply || f.ID != 2 || f.Error != ErrNoHostCapacity || len(f.Payload) != 0 {
		t.Errorf("got %+v", f)
	}
	hs.close()
	if log := hs.log.String(); !strings.Contains(log, "no match host (no Dawngate.exe); error reply") ||
		!strings.Contains(log, "-> resetDedicatedServer error reply (error 0x0132), 0 bytes") {
		t.Errorf("log:\n%s", log)
	}
}
