package blaze

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// Ending a logged-in player's session at once: an
// account that the admin page disables, bans or deletes, or the player deletes on the website, or the operator's
// --account-status changes in another process (seen by WatchAccountStatus), loses its sessions now instead of at
// its next login. The order: the hosted game first (the host gets removePlayer PLAYER_KICKED, the player and
// whoever is still in the game NotifyPlayerRemoved PLAYER_KICKED, which takes the client back to the main menu),
// then, KickGameDelay later, the UserSessions notification of SessionEndNotice, then the connection is closed (at
// once for SessionEndClose, else when the client hasn't closed it itself within KickGrace). The connection's
// session-end hooks (the playgroups, a matchmaking session, the Messaging relay, presence) then run as for any
// lost connection; the client opens a new connection and shows the login screen, where the account's next login is
// refused (0x0065 disabled, 0x002B banned, 0x0029 deleted).
//
// Client evidence (Ghidra): the UserSessions dispatcher (DispatchUserSessionsNotification 0x00C7A8B0) hands
// notification 4 (Blaze::UserSessionDisconnectReason, SDR; the enum has only DUPLICATE_LOGIN 0, table 0x012F1C68)
// to the user manager's handler 0x00C8A280 (registered by InitializeBlazeUserManager 0x00C8D020 through the
// component's setter 0x00CA7320): for SDR 0 it calls the connection manager's disconnect (slot +0x18) with
// ERR_DUPLICATE_LOGIN (0x40070000), the slot BlazeSDK's own logout path uses; the platform's onBlazeStateChange
// (0x00AD4CB0, state 3 with an error) then reports LABS_LOST_SERVER_BLAZE and drops its Blaze objects. Notification
// 9 (Blaze::UserSessionLogoutInfo, BID + LOTP: LOGOUT_TYPE_ACCOUNT 0, LOGOUT_TYPE_PERSONA 1, table 0x012F1F30) goes
// to 0x00C8A810: for the local user with that BlazeId the user manager's state becomes 9 (account) or 10 (persona),
// as at the client's own logout (B12), but the connection stays.

// SessionEndNotice is what the client is told before its connection closes (dgserver --session-end).
type SessionEndNotice int

const (
	// SessionEndDisconnected sends UserSessions::UserSessionDisconnected {SDR DUPLICATE_LOGIN}: the client
	// disconnects itself (ERR_DUPLICATE_LOGIN) and goes to the login screen.
	SessionEndDisconnected SessionEndNotice = iota
	// SessionEndUnauthenticated sends UserSessions::UserUnauthenticated {BID, LOTP LOGOUT_TYPE_ACCOUNT}.
	SessionEndUnauthenticated
	// SessionEndClose closes the connection without a word (the client sees a lost connection).
	SessionEndClose
)

var sessionEndNames = map[SessionEndNotice]string{SessionEndDisconnected: "disconnected",
	SessionEndUnauthenticated: "unauthenticated", SessionEndClose: "close"}

func (n SessionEndNotice) String() string {
	if s, ok := sessionEndNames[n]; ok {
		return s
	}
	return fmt.Sprintf("notice%d", int(n))
}

// ParseSessionEndNotice reads dgserver --session-end's value.
func ParseSessionEndNotice(s string) (SessionEndNotice, error) {
	for n, name := range sessionEndNames {
		if strings.EqualFold(strings.TrimSpace(s), name) {
			return n, nil
		}
	}
	return 0, fmt.Errorf("--session-end %q: want disconnected, unauthenticated or close", s)
}

// UserSessions notification ids of the session's end (GetUserSessionsNotificationName 0x00C79E50).
const (
	NotifyUserSessionDisconnected = 0x04
	NotifyUserUnauthenticated     = 0x09
	// DisconnectDuplicateLogin is UserSessionDisconnectReason DUPLICATE_LOGIN, the enum's only value.
	DisconnectDuplicateLogin = 0
	// LogoutTypeAccount is UserSessionLogoutType LOGOUT_TYPE_ACCOUNT (LOGOUT_TYPE_PERSONA is 1).
	LogoutTypeAccount = 0
)

// UserSessionDisconnectedNotification is UserSessions::UserSessionDisconnected -> Blaze::UserSessionDisconnectReason
// (member table 0x01462658: SDR enum).
func UserSessionDisconnectedNotification(reason int64) []byte {
	return tdf.NewWriter().Int("SDR", reason).Bytes()
}

// UserUnauthenticatedNotification is UserSessions::UserUnauthenticated -> Blaze::UserSessionLogoutInfo (member table
// 0x014625F0: BID the BlazeId (the persona id, as UserAuthenticated's BUID), LOTP the logout type).
func UserUnauthenticatedNotification(blazeID, logoutType int64) []byte {
	return tdf.NewWriter().Int("BID", blazeID).Int("LOTP", logoutType).Bytes()
}

// KickGameDelay is how long the kick waits after taking the player out of its hosted game before it ends the Blaze
// session, so the client's own way back to the main menu (onPlayerRemoved) is over first; KickGrace how long it
// waits for the client to close the connection after the notice. Variables for the tests.
var (
	KickGameDelay = 1500 * time.Millisecond
	KickGrace     = 3 * time.Second
)

// liveSession is one logged-in connection in the registry.
type liveSession struct {
	conn    *Conn
	user    int64
	persona int64 // 0 until loginPersona
}

// liveSessions are the logged-in connections by account.
type liveSessions struct {
	mu     sync.Mutex
	byUser map[int64][]*liveSession
}

// sessionStarted registers a login of account user on conn until the session ends (logout or a closed connection).
func (s *Server) sessionStarted(conn *Conn, user int64) {
	ls := &liveSession{conn: conn, user: user}
	r := &s.live
	r.mu.Lock()
	if r.byUser == nil {
		r.byUser = map[int64][]*liveSession{}
	}
	r.byUser[user] = append(r.byUser[user], ls)
	r.mu.Unlock()
	conn.OnSessionEnd(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		list := slices.DeleteFunc(r.byUser[user], func(x *liveSession) bool { return x == ls })
		if len(list) == 0 {
			delete(r.byUser, user)
		} else {
			r.byUser[user] = list
		}
	})
}

// sessionPersona records the persona that conn's session logged in (loginPersona).
func (s *Server) sessionPersona(conn *Conn, user, persona int64) {
	r := &s.live
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ls := range r.byUser[user] {
		if ls.conn == conn {
			ls.persona = persona
		}
	}
}

// LoggedInAccounts returns the accounts with a session now, sorted.
func (s *Server) LoggedInAccounts() []int64 {
	r := &s.live
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int64, 0, len(r.byUser))
	for id := range r.byUser {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// EndAccountSessions ends every session of the account now (see the top of this file); why goes to the log. It
// returns how many sessions it started to end (0: not logged in, or its end already runs). It doesn't wait.
func (s *Server) EndAccountSessions(user int64, why string) int {
	r := &s.live
	r.mu.Lock()
	var todo []*liveSession
	for _, ls := range r.byUser[user] {
		if ls.conn.ending.CompareAndSwap(false, true) {
			todo = append(todo, ls)
		}
	}
	r.mu.Unlock()
	delay, grace := KickGameDelay, KickGrace
	for _, ls := range todo {
		cp := *ls
		go s.kick(&cp, why, delay, grace)
	}
	return len(todo)
}

// kick ends one session: the hosted games, the notice, the connection.
func (s *Server) kick(ls *liveSession, why string, delay, grace time.Duration) {
	conn := ls.conn
	notice := s.SessionEnd
	s.Log.Logf("blaze", "account %d (persona %d): ending its session now (%s); notice %s", ls.user, ls.persona, why, notice)
	if s.kickFromGames(conn) {
		time.Sleep(delay)
	}
	switch notice {
	case SessionEndDisconnected:
		conn.Push(s, ComponentUserSessions, NotifyUserSessionDisconnected, "UserSessionDisconnected",
			UserSessionDisconnectedNotification(DisconnectDuplicateLogin))
	case SessionEndUnauthenticated:
		conn.Push(s, ComponentUserSessions, NotifyUserUnauthenticated, "UserUnauthenticated",
			UserUnauthenticatedNotification(ls.persona, LogoutTypeAccount))
	}
	if notice != SessionEndClose {
		select {
		case <-conn.gone:
			s.Log.Logf("blaze", "account %d: the client closed its connection after the notice", ls.user)
			return
		case <-time.After(grace):
		}
	}
	s.Log.Logf("blaze", "account %d: closing its connection (session ended: %s)", ls.user, why)
	conn.closeNow()
}

// kickFromGames takes conn's players out of every hosted game they are in, as a kick (PLAYER_KICKED): the host gets
// removePlayer (and abortGame once nobody is left), the player and the others NotifyPlayerRemoved. It reports
// whether conn was in a game.
func (s *Server) kickFromGames(conn *Conn) bool {
	s.hosted.mu.Lock()
	games := make([]*hostedGame, 0, len(s.hosted.byID))
	for _, hg := range s.hosted.byID {
		games = append(games, hg)
	}
	s.hosted.mu.Unlock()
	found := false
	for _, hg := range games {
		hg.mu.Lock()
		var ids []int64
		for _, hp := range hg.players {
			if hp.conn == conn && !hp.removed {
				ids = append(ids, hp.p.ID)
			}
		}
		hg.mu.Unlock()
		for _, pid := range ids {
			if !hg.leave(conn, pid, RemovedPlayerKicked) {
				continue
			}
			found = true
			payload := PlayerRemovedNotification(hg.game.ID, pid, RemovedPlayerKicked)
			conn.Push(s, ComponentGameManager, NotifyPlayerRemovedID, "NotifyPlayerRemoved", payload)
			hg.push(NotifyPlayerRemovedID, "NotifyPlayerRemoved", func(*hostedPlayer) []byte { return payload })
			hg.lobbyLeft(pid)
		}
	}
	return found
}

// WatchAccountStatus reads the status of every logged-in account every interval and ends the sessions of those that
// aren't active any more (disabled, banned, deleted, or gone): the operator's --account-status runs in another
// process, so the running server learns it only from the database. The admin page ends them at once
// (EndAccountSessions); this is the rest. It returns the stop function.
func (s *Server) WatchAccountStatus(every time.Duration) (stop func()) {
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		lastErr := ""
		for {
			select {
			case <-quit:
				return
			case <-t.C:
			}
			ids := s.LoggedInAccounts()
			if len(ids) == 0 {
				continue
			}
			st, err := s.Store.Statuses(ids)
			if err != nil {
				if msg := err.Error(); msg != lastErr {
					s.Log.Logf("blaze", "status check: the account store failed: %v", err)
					lastErr = msg
				}
				continue
			}
			lastErr = ""
			for _, id := range ids {
				status, ok := st[id]
				switch {
				case !ok:
					s.EndAccountSessions(id, "its account is gone (status check)")
				case status != store.StatusActive:
					s.EndAccountSessions(id, fmt.Sprintf("its account is %s (status check)", status))
				}
			}
		}
	}()
	return func() { close(quit); <-done }
}
