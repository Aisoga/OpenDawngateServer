package blaze

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf/tdfcheck"
)

// the notifications that end a session, against tdf_golden.py's cases and the client's member tables.
func TestSessionEndNotificationsMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range []struct {
		name, class string
		payload     []byte
	}{
		{"usersessiondisconnected_notification", "Blaze::UserSessionDisconnectReason",
			UserSessionDisconnectedNotification(DisconnectDuplicateLogin)},
		{"userunauthenticated_notification", "Blaze::UserSessionLogoutInfo",
			UserUnauthenticatedNotification(store.FirstPersonaID, LogoutTypeAccount)},
		{"notifyplayerremoved_kicked_notification", "Blaze::GameManager::NotifyPlayerRemoved",
			PlayerRemovedNotification(1, store.FirstPersonaID, RemovedPlayerKicked)},
	} {
		if got, want := hex.EncodeToString(c.payload), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
		tdfcheck.Require(t, c.class, c.payload)
	}
	for _, s := range []string{"disconnected", "Unauthenticated", " close "} {
		if _, err := ParseSessionEndNotice(s); err != nil {
			t.Error(err)
		}
	}
	if _, err := ParseSessionEndNotice("kick"); err == nil {
		t.Error("--session-end kick accepted")
	}
}

// shortKick makes the kick's waits short for a test.
func shortKick(t *testing.T) {
	gd, gr := KickGameDelay, KickGrace
	KickGameDelay, KickGrace = 50*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { KickGameDelay, KickGrace = gd, gr })
}

// ended waits until the fake client's connection has ended.
func (tc *tcpClient) ended(within time.Duration) bool {
	tc.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		tc.mu.Lock()
		err := tc.err
		tc.mu.Unlock()
		if err != nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// logHas waits until the log holds want.
func logHas(t *testing.T, log *lockedBuf, want string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if strings.Contains(log.String(), want) {
			return
		}
	}
	t.Errorf("log lacks %q:\n%s", want, log.String())
}

// A player at the main menu whose account is disabled: UserSessionDisconnected, the client closes its connection (as
// the live client did 57 ms later in a test run), the session's hooks run; another player stays.
func TestEndAccountSessionsAtTheMainMenu(t *testing.T) {
	shortKick(t)
	ts := newTCPServer(t, nil)
	a, b := ts.client("victim@x.io"), ts.client("other@x.io")
	acctA, _, _ := ts.s.Store.PersonaByID(a.id)
	acctB, _, _ := ts.s.Store.PersonaByID(b.id)
	if got := ts.s.LoggedInAccounts(); len(got) != 2 {
		t.Fatalf("logged in: %v", got)
	}
	if n := ts.s.EndAccountSessions(acctA.UserID, "test: disabled"); n != 1 {
		t.Fatalf("ended %d sessions", n)
	}
	if n := ts.s.EndAccountSessions(acctA.UserID, "again"); n != 0 {
		t.Errorf("a second end while the first runs: %d", n)
	}
	f := a.note(ComponentUserSessions, NotifyUserSessionDisconnected)
	if sdr, _ := payloadOf(t, "Blaze::UserSessionDisconnectReason", f).Int("SDR"); sdr != DisconnectDuplicateLogin {
		t.Errorf("SDR %d", sdr)
	}
	a.c.Close() // the client disconnects itself (ERR_DUPLICATE_LOGIN)
	logHas(t, ts.log, "account 1000000001: the client closed its connection after the notice")
	for deadline := time.Now().Add(5 * time.Second); len(ts.s.LoggedInAccounts()) != 1; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("logged in after the end: %v", ts.s.LoggedInAccounts())
		}
	}
	if got := ts.s.LoggedInAccounts(); got[0] != acctB.UserID {
		t.Errorf("logged in: %v", got)
	}
	b.none(ComponentUserSessions, NotifyUserSessionDisconnected, 100*time.Millisecond)
	if n := ts.s.EndAccountSessions(acctA.UserID, "gone"); n != 0 {
		t.Errorf("a session ended twice: %d", n)
	}
	logHas(t, ts.log, "[blaze] account 1000000001 (persona 2000000001): ending its session now (test: disabled); notice disconnected")
}

// A client that doesn't close after the notice is closed by the server after KickGrace; the other notices.
func TestEndAccountSessionsNotices(t *testing.T) {
	shortKick(t)
	for _, notice := range []SessionEndNotice{SessionEndDisconnected, SessionEndUnauthenticated, SessionEndClose} {
		t.Run(notice.String(), func(t *testing.T) {
			ts := newTCPServer(t, func(s *Server) { s.SessionEnd = notice })
			a := ts.client("victim@x.io")
			acct, _, _ := ts.s.Store.PersonaByID(a.id)
			ts.s.EndAccountSessions(acct.UserID, "test")
			switch notice {
			case SessionEndDisconnected:
				a.note(ComponentUserSessions, NotifyUserSessionDisconnected)
			case SessionEndUnauthenticated:
				st := payloadOf(t, "Blaze::UserSessionLogoutInfo", a.note(ComponentUserSessions, NotifyUserUnauthenticated))
				if bid, _ := st.Int("BID"); bid != a.id {
					t.Errorf("BID %d, want the persona %d", bid, a.id)
				}
			}
			if !a.ended(5 * time.Second) {
				t.Fatal("the server didn't close the connection")
			}
			logHas(t, ts.log, "account 1000000001: closing its connection (session ended: test)")
			if q := a.queued(); notice == SessionEndClose && q != "" {
				t.Errorf("close sent something: %s", q)
			}
		})
	}
}

// A player in a hosted practice game whose account is banned: the host gets removePlayer PLAYER_KICKED and abortGame
// (nobody left), the player NotifyPlayerRemoved PLAYER_KICKED (the client's way back to the main menu, seen in a test run),
// then UserSessionDisconnected; requests in between are answered.
func TestEndAccountSessionsInAHostedGame(t *testing.T) {
	shortKick(t)
	hs := newHostedSession(t, nil, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		h.Send(gmshost.AddPlayer{User: store.FirstPersonaID})
		h.Send(gmshost.GMSUpdate{Phase: gmshost.PhaseInGame})
		return receive(hs, h)
	})
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	hs.hostGot()
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
	hs.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(1, GameStateInGame))
	if n := hs.s.EndAccountSessions(store.FirstUserID, "test: banned"); n != 1 {
		t.Fatalf("ended %d", n)
	}
	hs.expect(ComponentGameManager, NotifyPlayerRemovedID, fire.Notification, 0, "notifyplayerremoved_kicked_notification")
	if m := hs.hostGot(); m != (gmshost.RemovePlayer{User: store.FirstPersonaID, Reason: "PLAYER_KICKED"}) {
		t.Errorf("host got %#v", m)
	}
	if m := hs.hostGot(); m != (gmshost.AbortGame{Reason: "no players left"}) {
		t.Errorf("host got %#v", m)
	}
	hs.send(ComponentUtil, CmdUtilPing, 9, nil) // the client's own requests still get answers
	if f := hs.next("ping reply"); f.Type != fire.Reply || f.ID != 9 {
		t.Errorf("ping during the kick: %s id %d", f.Type, f.ID)
	}
	hs.expect(ComponentUserSessions, NotifyUserSessionDisconnected, fire.Notification, 0, "usersessiondisconnected_notification")
	hs.waitIdle()
	hs.close()
	log := hs.log.String()
	for _, want := range []string{"game 1: player 2000000001 left (PLAYER_KICKED); 0 still in the game",
		"account 1000000001 (persona 2000000001): ending its session now (test: banned)"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "BLAZESERVER_CONN_LOST") {
		t.Error("the closed connection removed the kicked player again")
	}
}

// The status check: an account set to banned by another process (here: the store directly) loses its session within
// the interval; an active one keeps it.
func TestWatchAccountStatus(t *testing.T) {
	shortKick(t)
	ts := newTCPServer(t, nil)
	a, b := ts.client("victim@x.io"), ts.client("other@x.io")
	acctA, _, _ := ts.s.Store.PersonaByID(a.id)
	stop := ts.s.WatchAccountStatus(20 * time.Millisecond)
	defer stop()
	b.none(ComponentUserSessions, NotifyUserSessionDisconnected, 60*time.Millisecond)
	if err := ts.s.Store.SetStatus(acctA.UserID, store.StatusBanned); err != nil {
		t.Fatal(err)
	}
	a.note(ComponentUserSessions, NotifyUserSessionDisconnected)
	if !a.ended(5 * time.Second) {
		t.Fatal("not closed")
	}
	logHas(t, ts.log, "ending its session now (its account is banned (status check))")
	b.none(ComponentUserSessions, NotifyUserSessionDisconnected, 60*time.Millisecond)
	if n := strings.Count(ts.log.String(), "ending its session now"); n != 1 {
		t.Errorf("%d ends", n)
	}
}
