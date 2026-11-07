package blaze

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"time"

	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/uichannel"
)

// The custom lobby's ready check. The client's lobby has none of its own (CustomGameLobbyView, its tiles and the
// admin's roster manager hold no ready state), so it is ours on both sides: readyCheck below holds the state, and a
// class in the front end's movie (client_ui/lobby/LobbyReady.as) shows it, over the UI channel (package uichannel):
//
//	lobby.enter    {}                       the class's lobby is showing: its player takes part in the check
//	lobby.setReady {"ready":true}           the READY button: the player's state
//	lobby.state    {"players":[{"name":"x","ready":false,"rating":1612}, ...]}
//	                                        the whole lobby, to everyone who entered, after a change (lobbyPublishGap)
//	lobby.startRefused                      to an admin: a start was refused because someone isn't ready
//
// lobby.state's players are the game's, by the name the lobby's natives know them by. ready is there only for a player
// whose state counts; rating (RATINGSHOW3) only for one whose rating players see (Server.ShownRating).
//
// Who counts: the players on teams 0 and 1 who entered, except the admin (who is ready by starting); spectators never;
// a player who never entered counts as ready (a client without the class doesn't block). A state goes back to not ready
// at a team change (swapPlayersTeam) and a rejoin; a player whose channel ends leaves the check. The admin's
// advanceGameState IN_GAME while a counted player isn't ready is refused (GAMEMANAGER_ERR_INVALID_GAME_STATE_ACTION) as
// a backstop only: the class keeps the admin's START disabled, which sends nothing. Client evidence: a refused
// advanceGameState leaves the client in its lobby (cBlazeGameManager::ChangeGameStateResult 0x00AC1C70).

// The UI channel's calls and events of the custom lobby.
const (
	CallLobbyEnter         = "lobby.enter"
	CallLobbySetReady      = "lobby.setReady"
	EventLobbyState        = "lobby.state"
	EventLobbyStartRefused = "lobby.startRefused"
)

// lobbyPublishGap is the least time between two lobby.state broadcasts of a lobby. Every player of it hears each one and
// redraws, and each player can change its state many times a second, so a burst ends in one broadcast with the last
// state. The first of a burst goes at once, so a click on READY still shows without a wait.
const lobbyPublishGap = 200 * time.Millisecond

// readyCheck is a lobby's ready state: who takes part, and whether each of them is ready (guarded by the hostedGame's
// mutex; the zero value is an empty check).
type readyCheck struct {
	members map[int64]bool
}

// Join makes id a member, not ready.
func (c *readyCheck) Join(id int64) {
	if c.members == nil {
		c.members = map[int64]bool{}
	}
	c.members[id] = false
}

// Leave takes id out.
func (c *readyCheck) Leave(id int64) { delete(c.members, id) }

// Has reports whether id is a member.
func (c *readyCheck) Has(id int64) bool {
	_, ok := c.members[id]
	return ok
}

// Ready reports whether id is a member and ready.
func (c *readyCheck) Ready(id int64) bool { return c.members[id] }

// Set sets a member's state; false for someone who isn't one.
func (c *readyCheck) Set(id int64, ready bool) bool {
	if !c.Has(id) {
		return false
	}
	c.members[id] = ready
	return true
}

// LobbyStatePlayer is one player of EventLobbyState.
type LobbyStatePlayer struct {
	Name   string `json:"name"`
	Ready  *bool  `json:"ready,omitempty"`
	Rating int64  `json:"rating,omitempty"`
}

// LobbyState is EventLobbyState's data.
type LobbyState struct {
	Players []LobbyStatePlayer `json:"players"`
}

// AttachUI serves the lobby's calls on ui and identifies its users by their Blaze logins.
func (s *Server) AttachUI(ui *uichannel.Server) {
	s.UI = ui
	s.lobbyGap = lobbyPublishGap
	ui.Identify = s.identifyUI
	ui.OnDisconnect = s.uiDisconnected
	ui.Handle(CallLobbyEnter, func(user int64, _ json.RawMessage) error { return s.lobbyEnter(user) })
	ui.Handle(CallLobbySetReady, func(user int64, args json.RawMessage) error {
		var a struct {
			Ready *bool `json:"ready"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.Ready == nil {
			return errors.New("no ready in the arguments")
		}
		return s.lobbySetReady(user, *a.Ready)
	})
}

// identifyUI names the user behind a UI channel connection. Through the tunnel the session says it: one Start-Client,
// one tunnel session, one login, and what the client calls itself doesn't count. (A session with several logins, as the
// test lanes' two clients behind one tunnel: the name picks among that session's logins, and only those.) A direct
// connection (tunnel = off, the developers' path, whose Blaze is unencrypted too) has only its address: name must be
// logged in from the same one, all loopback addresses counting as one (the movie's socket isn't bound: it comes from
// 127.0.0.1 whatever the game's own bind address is), and only by a direct login: someone behind a tunnelled player's
// public address must not pass for that player on a server that also has plain listeners. The store is asked only when
// there is a choice to make, so a connection without any login costs it nothing.
func (s *Server) identifyUI(c net.Conn, name string) (int64, bool) {
	var users []int64
	if t := tunnel.Local(c); t != nil {
		users = s.msgs.usersOfTunnelSession(t.Session)
		if len(users) == 1 {
			return users[0], true
		}
	} else {
		ap, err := netip.ParseAddrPort(c.RemoteAddr().String())
		if err != nil {
			return 0, false
		}
		users = s.msgs.usersOfDirectAddr(ap.Addr().Unmap())
	}
	if len(users) == 0 || s.Store == nil {
		return 0, false
	}
	if _, p, named := s.Store.PersonaByName(name); named && slices.Contains(users, p.ID) {
		return p.ID, true
	}
	return 0, false
}

// usersOfDirectAddr are the personas logged in without the tunnel from address from, all loopback addresses counting
// as one.
func (m *messaging) usersOfDirectAddr(from netip.Addr) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var users []int64
	for user, conns := range m.online {
		for _, c := range conns {
			if c.TunnelAddr() != nil {
				continue
			}
			if a := c.Addr(); a == from || a.IsLoopback() && from.IsLoopback() {
				users = append(users, user)
				break
			}
		}
	}
	return users
}

// usersOfTunnelSession are the personas logged in through tunnel session id.
func (m *messaging) usersOfTunnelSession(id uint64) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var users []int64
	for user, conns := range m.online {
		for _, c := range conns {
			if t := c.TunnelAddr(); t != nil && t.Session == id {
				users = append(users, user)
				break
			}
		}
	}
	return users
}

// lobbyOf is the custom lobby user is in (nil: none, or its game started).
func (s *Server) lobbyOf(user int64) *hostedGame {
	s.hosted.mu.Lock()
	games := make([]*hostedGame, 0, len(s.hosted.byID))
	for _, g := range s.hosted.byID {
		games = append(games, g)
	}
	s.hosted.mu.Unlock()
	for _, g := range games {
		g.mu.Lock()
		hp := g.playerLocked(user)
		in := g.custom != nil && !g.custom.started && !g.ended && hp != nil && !hp.removed
		g.mu.Unlock()
		if in {
			return g
		}
	}
	return nil
}

// lobbyEnter: user's lobby is showing, so its client has the class and takes part in the check.
func (s *Server) lobbyEnter(user int64) error {
	hg := s.lobbyOf(user)
	if hg == nil {
		return fmt.Errorf("not in a lobby")
	}
	hg.mu.Lock()
	same := hg.custom.check.Has(user) && !hg.custom.check.Ready(user)
	hg.custom.check.Join(user)
	counted := hg.readyCountedLocked()
	hg.mu.Unlock()
	if same { // a new connection of a member, or a repeated call: nothing for the others to hear
		hg.sendLobbyState(user)
		return nil
	}
	s.Log.Logf("blaze", "   game %d: ready check: %d entered; %d counted now", hg.game.ID, user, len(counted))
	hg.publishLobby()
	return nil
}

// lobbySetReady is the READY button: user's state. It changes nothing for someone who doesn't count (the admin, a
// spectator, a player who hasn't entered), and the same state again tells nobody anything.
func (s *Server) lobbySetReady(user int64, ready bool) error {
	hg := s.lobbyOf(user)
	if hg == nil {
		return fmt.Errorf("not in a lobby")
	}
	hg.mu.Lock()
	counted := false
	if hp := hg.playerLocked(user); hp != nil {
		counted = hg.readyCountsLocked(hp)
	}
	if !counted {
		hg.mu.Unlock()
		return fmt.Errorf("not counted (the admin or a spectator); ignored")
	}
	if hg.custom.check.Ready(user) == ready {
		hg.mu.Unlock()
		return nil
	}
	hg.custom.check.Set(user, ready)
	waiting := hg.readyWaitingLocked()
	hg.mu.Unlock()
	s.Log.Logf("blaze", "   game %d: ready check: %d is ready %v; waiting for %v", hg.game.ID, user, ready, waiting)
	hg.publishLobby()
	return nil
}

// uiDisconnected: user's channel ended, so its client can't say it is ready any more: it leaves the check, as a client
// without the class.
func (s *Server) uiDisconnected(user int64) {
	hg := s.lobbyOf(user)
	if hg == nil {
		return
	}
	hg.mu.Lock()
	hg.custom.check.Leave(user)
	hg.mu.Unlock()
	s.Log.Logf("blaze", "   game %d: ready check: %d's UI channel ended; it doesn't count any more", hg.game.ID, user)
	hg.publishLobby()
}

// readyCountsLocked reports whether hp's state counts for the start (hg.mu held).
func (hg *hostedGame) readyCountsLocked(hp *hostedPlayer) bool {
	return !hp.removed && hg.custom.check.Has(hp.p.ID) && (hp.p.Team == 0 || hp.p.Team == 1) && !hg.isAdminLocked(hp.p.ID)
}

// readyCountedLocked are the players whose state counts (hg.mu held).
func (hg *hostedGame) readyCountedLocked() []int64 {
	var out []int64
	for _, hp := range hg.players {
		if hg.readyCountsLocked(hp) {
			out = append(out, hp.p.ID)
		}
	}
	return out
}

// readyWaitingLocked are the players the start waits for (hg.mu held).
func (hg *hostedGame) readyWaitingLocked() []int64 {
	var out []int64
	for _, id := range hg.readyCountedLocked() {
		if !hg.custom.check.Ready(id) {
			out = append(out, id)
		}
	}
	return out
}

// lobbyRosterChanged: someone joined, left or moved. The host's roster and the lobby's classes follow.
func (hg *hostedGame) lobbyRosterChanged() {
	hg.syncHostRoster()
	hg.publishLobby()
}

// publishLobby tells everyone who entered the lobby's whole state (outside hg.mu), at most once per Server.lobbyGap: a
// change inside the gap goes out at its end, with whatever came after it. The state is sent again rather than as
// differences, so there is no state on the client to get out of step.
func (hg *hostedGame) publishLobby() {
	s := hg.s
	if s.UI == nil {
		return
	}
	hg.mu.Lock()
	if hg.custom == nil || hg.custom.started || hg.ended {
		hg.mu.Unlock()
		return
	}
	now := time.Now()
	if wait := hg.custom.lastPublish.Add(s.lobbyGap).Sub(now); wait > 0 {
		if !hg.custom.publishDue {
			hg.custom.publishDue = true
			time.AfterFunc(wait, hg.publishWaiting)
		}
		hg.mu.Unlock()
		return
	}
	hg.custom.lastPublish = now
	hg.mu.Unlock()
	hg.sendLobbyState(0)
}

// publishWaiting is a publish whose turn has come.
func (hg *hostedGame) publishWaiting() {
	hg.mu.Lock()
	if hg.custom != nil {
		hg.custom.publishDue = false
	}
	hg.mu.Unlock()
	hg.publishLobby()
}

// sendLobbyState sends the lobby's state to only, or (0) to everyone who entered.
func (hg *hostedGame) sendLobbyState(only int64) {
	s := hg.s
	if s.UI == nil {
		return
	}
	hg.mu.Lock()
	if hg.custom == nil || hg.custom.started || hg.ended {
		hg.mu.Unlock()
		return
	}
	var to []int64
	var state LobbyState
	unread := map[int]*hostedPlayer{} // state.Players' index -> a player whose rating hasn't been read yet
	for _, hp := range hg.players {
		if hp.removed {
			continue
		}
		p := LobbyStatePlayer{Name: hp.p.Name, Rating: hp.rating}
		if hg.readyCountsLocked(hp) {
			ready := hg.custom.check.Ready(hp.p.ID)
			p.Ready = &ready
		}
		if !hp.ratingRead {
			unread[len(state.Players)] = hp
		}
		state.Players = append(state.Players, p)
		if hg.custom.check.Has(hp.p.ID) && (only == 0 || hp.p.ID == only) {
			to = append(to, hp.p.ID)
		}
	}
	hg.mu.Unlock()
	if len(to) == 0 {
		return
	}
	for i, hp := range unread {
		v := s.lobbyRating(hp.p.ID)
		state.Players[i].Rating = v
		hg.mu.Lock()
		hp.rating, hp.ratingRead = v, true
		hg.mu.Unlock()
	}
	for _, id := range to {
		s.UI.Send(id, EventLobbyState, state)
	}
}

// lobbyRating is the rating lobby.state shows for persona id; 0: none.
func (s *Server) lobbyRating(id int64) int64 {
	if s.Store == nil {
		return 0
	}
	if acct, _, ok := s.Store.PersonaByID(id); ok {
		if v, shown := s.ShownRating(acct.History); shown {
			return v
		}
	}
	return 0
}

// uiLoginEnded: one of user's logins ended, and the channel connections that came by it with it.
func (s *Server) uiLoginEnded(user int64) {
	if s.UI != nil {
		s.UI.Recheck(user)
	}
}

// refuseUnreadyStart refuses the admin's advanceGameState IN_GAME while counted players aren't ready (hg.mu held; it
// unlocks it). The class keeps START disabled, so this is a backstop for a modified or racing client. The client
// survives the error (cBlazeGameManager::ChangeGameStateResult 0x00AC1C70 -> HandleNetworkGameStartFailed
// 0x00684D70: no dialog, the lobby stays), but its START had locked the roster (RosterLockedKey "1"), which nothing of
// the client's unlocks again; so the server sets RosterLockedKey "0" for every player (the roster manager's
// onGameAttributeUpdated 0x006A2670 takes "0"/"1" into its lock, 0x0069FBE0), and tells an admin with the class why
// (EventLobbyStartRefused).
//
// READY2b: the unlock alone leaves the admin's join buttons and kick X hidden. 0x0069FBE0 tells the roster listeners
// (0x0069F4C0 -> 0x0069F1E0, slot 5), the lobby's slot 5 (0x00427E30 -> 0x00427800) sets the view's rosterLocked,
// whose setter only redraws the lock's parts (drawTeamChangeAvailable: dragging, the join buttons' enabled state); the
// join buttons' and kick buttons' visibility comes from drawRoster (showJoinTeamResource, showKickButton), which runs
// only at invalidateRoster. (The lobby's own 0x00425DA0 calls the view's "invalidateRosterIsLocked", a name the view
// doesn't have: its method is invalidateIsRosterLocked.) So the admin also gets its own extended data after the
// unlock, as RATING2b's redraw after BALANCE: the lobby's UserManager listener (0x004260E0) calls invalidateRoster.
// why is added to the log line.
func (s *Server) refuseUnreadyStart(r *Request, hg *hostedGame, waiting []int64, why string) {
	gid := hg.game.ID
	unlock := []tdf.Pair{tdf.KV(AttrRosterLocked, "0")}
	hg.game.Attrs = mergeAttrs(hg.game.Attrs, unlock)
	var admins []*hostedPlayer
	for _, a := range hg.custom.admins {
		hp := hg.playerLocked(a)
		if hp == nil || hp.removed || hp.conn == nil {
			continue
		}
		admins = append(admins, hp)
	}
	hg.mu.Unlock()
	s.Log.Logf("blaze", "   game %d: advanceGameState IN_GAME refused: not ready %v (the ready check)%s; RosterLockedKey \"0\" to every player, the admin its extended data (its lobby's redraw)",
		gid, waiting, why)
	r.Fail(ErrInvalidGameStateAction)
	note := GameAttribChangeNotification(gid, unlock)
	r.After(func() {
		hg.push(NotifyGameAttribChangeID, "NotifyGameAttribChange", func(*hostedPlayer) []byte { return note })
		for _, a := range admins {
			if own := s.OwnExtendedData(a.p.ID); own != nil {
				a.conn.Push(s, ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, "UserSessionExtendedDataUpdate", own)
			}
			if s.UI != nil {
				s.UI.Send(a.p.ID, EventLobbyStartRefused, nil)
			}
		}
	})
}
