package blaze

import (
	"slices"
	"sync"

	"dawngate/server/internal/store"
)

// Friends' presence (B13b; docs/BLAZE.md *Friends, recent players and blocked users (B13b)*). The client knows
// other users only through UserSessions notifications: UserAdded makes a user known (with its extended data, whose
// CMAP[PresenceAttribute] is the presence the chat shows), UserUpdated sets its status (online or offline), and
// UserSessionExtendedDataUpdate replaces its extended data. The server keeps each online persona's presence (what
// its client last set with updateExtendedDataAttribute) and tells the persona's online friends:
//   - getLists and an add announce every member a client doesn't know yet (UserAdded, UserUpdated);
//   - a presence change goes to the friends as the persona's extended data (PresenceNotification);
//   - a login sends the friends UserUpdated online, the session's end the presence 0 (offline, which the chat's
//     presence listener 0x00AA9C40 sees) and then UserUpdated offline.
// That the original server pushed these the same way is our reading of the client, not seen.

// presenceState is each online persona's presence value.
type presenceState struct {
	mu    sync.Mutex
	value map[int64]int64
}

// presenceOf returns persona id's presence: what its client set while online, 0 when offline or unset.
func (s *Server) presenceOf(id int64) int64 {
	if !s.personaOnline(id) {
		return 0
	}
	s.presence.mu.Lock()
	defer s.presence.mu.Unlock()
	return s.presence.value[id]
}

func (s *Server) setPresence(id, v int64) {
	s.presence.mu.Lock()
	defer s.presence.mu.Unlock()
	if s.presence.value == nil {
		s.presence.value = map[int64]int64{}
	}
	if v == 0 {
		delete(s.presence.value, id)
	} else {
		s.presence.value[id] = v
	}
}

// connsOf returns persona id's logged-in connections (the messaging relay's, B10c).
func (s *Server) connsOf(id int64) []*Conn {
	m := &s.msgs
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.online[id])
}

// hasMessage reports whether persona owner's inbox holds a message of type typ from persona from (B13b: a pending
// friend request).
func (m *messaging) hasMessage(owner, from, typ int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.ContainsFunc(m.inbox[owner], func(sm ServerMessage) bool {
		return sm.Source == from && sm.Payload.Type == typ
	})
}

// userNotes returns UserAdded and UserUpdated for persona id as things stand: its presence, online or not.
func (s *Server) userNotes(id int64) (added, status []byte, ok bool) {
	acct, p, ok := s.Store.PersonaByID(id)
	if !ok {
		return nil, nil, false
	}
	return UserAddedNotification(acct, p, s.presenceOf(id)), UserUpdatedNotification(id, s.personaOnline(id)), true
}

// introduce queues UserAdded and UserUpdated for persona id before the reply, unless the client knows it already.
func (r *Request) introduce(id int64) {
	if !r.Conn.firstSight(id) {
		return
	}
	if added, status, ok := r.Server.userNotes(id); ok {
		r.NotifyFirst(ComponentUserSessions, NotifyUserAdded, "UserAdded", added)
		r.NotifyFirst(ComponentUserSessions, NotifyUserUpdated, "UserUpdated", status)
	}
}

// pushUserIfNew sends UserAdded and UserUpdated for persona id to c now, unless c's client knows it already. It
// reports whether it did.
func (s *Server) pushUserIfNew(c *Conn, id int64) bool {
	if !c.firstSight(id) {
		return false
	}
	added, status, ok := s.userNotes(id)
	if ok {
		c.Push(s, ComponentUserSessions, NotifyUserAdded, "UserAdded", added)
		c.Push(s, ComponentUserSessions, NotifyUserUpdated, "UserUpdated", status)
	}
	return true
}

// userNote is one UserSessions notification about a user.
type userNote struct {
	id      uint16
	name    string
	payload []byte
}

// Other users' wins (B14; docs/BLAZE.md *System messages and other users' wins (B14)*). The party lobby's
// GetMemberWins (PVPLobbyUI 0x004F7370) reads a member's DMAP[0x00070034] through BlazePlatform_GetUserExtendedInfo
// (0x00AD4F90): the user from the client's UserManager (0x00C8AFD0) and the value from its extended data. A member the
// client knows only from the playgroup (PlaygroupMemberInfo.USER, acquired by 0x00C8CA30 -> 0x00C8C5E0) has none, so
// it showed 0. The server therefore tells a client about every other member of a playgroup it joins, before
// NotifyJoinPlaygroup, and every member about the one who joins, before NotifyMemberJoinedPlaygroup (answerJoin; the
// PvP party and the game's chat GG-<id>, which every client in a game joins):
//   - a user the connection's client wasn't told about gets UserAdded (with the extended data) and UserUpdated.
//     HandleUserAddedNotification (0x00C8CF90) acquires the user, creating it if needed (0x00C8C5E0), and copies DATA
//     unless an earlier UserAdded already did (bit 0 of the user's +0x2A; otherwise it only drops the extra reference,
//     0x00C8BFD0), so it fills a user the client made from a playgroup or a game roster too.
//   - a user it was told about gets UserSessionExtendedDataUpdate, whose handler (0x00C8C400) replaces a known user's
//     data: so the wins are current (UserAdded's reference keeps the user known for the session).
// After a game every client that was told about a player gets the player's new data (refreshKnownUser).

// userDataNotes returns what tells c's client about persona id with its current extended data (presence, wins):
// UserAdded and UserUpdated when c wasn't told about it yet (marking it told), else UserSessionExtendedDataUpdate.
func (s *Server) userDataNotes(c *Conn, id int64) []userNote {
	acct, p, ok := s.Store.PersonaByID(id)
	if !ok {
		return nil
	}
	presence := s.presenceOf(id)
	if c.firstSight(id) {
		return []userNote{{NotifyUserAdded, "UserAdded", UserAddedNotification(acct, p, presence)},
			statusNote(id, s.personaOnline(id))}
	}
	return []userNote{presenceNote(acct, p, presence)}
}

// knows reports whether c's client was told about user id (UserAdded) since its login.
func (c *Conn) knows(id int64) bool {
	c.kmu.Lock()
	defer c.kmu.Unlock()
	return c.known[id]
}

// refreshKnownUser sends persona id's current extended data (UserSessionExtendedDataUpdate with its presence and
// wins) to every logged-in connection whose client was told about it (B14: after a game changed its wins, so a party
// member's lobby card shows them).
func (s *Server) refreshKnownUser(id int64) {
	var note []byte
	for _, c := range s.onlineConns() {
		if !c.knows(id) {
			continue
		}
		if note == nil {
			acct, p, ok := s.Store.PersonaByID(id)
			if !ok {
				return
			}
			note = PresenceNotification(acct, p, s.presenceOf(id))
		}
		c.Push(s, ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, "UserSessionExtendedDataUpdate", note)
	}
}

func presenceNote(acct store.Account, p store.Persona, presence int64) userNote {
	return userNote{NotifyUserSessionExtendedDataUpdate, "UserSessionExtendedDataUpdate", PresenceNotification(acct, p, presence)}
}

func statusNote(id int64, online bool) userNote {
	return userNote{NotifyUserUpdated, "UserUpdated", UserUpdatedNotification(id, online)}
}

// tellFriends sends every online friend of persona p what changed about it (notes, built once from p's account),
// unless the friend's client doesn't know p yet, which then gets UserAdded and UserUpdated (with the new state)
// instead.
func (s *Server) tellFriends(p store.Persona, notes func(acct store.Account) []userNote) {
	acct, _, ok := s.Store.PersonaByID(p.ID)
	if !ok {
		return
	}
	var ns []userNote
	for _, f := range acct.Lists.Friends {
		for _, c := range s.connsOf(f.ID) {
			if s.pushUserIfNew(c, p.ID) {
				continue
			}
			if ns == nil {
				ns = notes(acct)
			}
			for _, n := range ns {
				c.Push(s, ComponentUserSessions, n.id, n.name, n.payload)
			}
		}
	}
}

// presenceChanged keeps the presence the requester's client set (updateExtendedDataAttribute PresenceAttribute)
// and passes it on to its online friends after the reply.
func (s *Server) presenceChanged(r *Request, v int64) {
	p := r.Conn.Session.Persona
	s.setPresence(p.ID, v)
	s.Log.Logf("blaze", "   presence of %d %q: %#x", p.ID, p.Name, v)
	r.After(func() {
		s.tellFriends(p, func(acct store.Account) []userNote { return []userNote{presenceNote(acct, p, s.presenceOf(p.ID))} })
	})
}

// friendsLogin runs at loginPersona: the connection's client knows no other user yet; after the reply the
// persona's online friends hear it is online, and when its last session ends that it is offline (presence 0
// first, which the chat's presence listener sees as offline, then the status).
func (s *Server) friendsLogin(r *Request, p store.Persona) {
	c := r.Conn
	c.forgetUsers()
	c.kmu.Lock()
	again := c.statusOf == p.ID // loginPersona twice in one session: the hook is there already
	c.statusOf = p.ID
	c.kmu.Unlock()
	if again {
		return
	}
	r.After(func() {
		s.tellFriends(p, func(store.Account) []userNote { return []userNote{statusNote(p.ID, true)} })
	})
	c.OnSessionEnd(func() {
		c.kmu.Lock()
		c.statusOf = 0
		c.kmu.Unlock()
		if s.personaOnline(p.ID) {
			return // still logged in on another connection
		}
		s.setPresence(p.ID, 0)
		s.tellFriends(p, func(acct store.Account) []userNote {
			return []userNote{presenceNote(acct, p, 0), statusNote(p.ID, false)}
		})
	})
}
