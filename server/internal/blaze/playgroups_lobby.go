package blaze

import (
	"slices"

	"dawngate/server/internal/tdf"
)

// The PvP lobby's Playgroups commands (B10a; docs/BLAZE.md *PvP matchmaking (B10a)*, read statically).
// The PvP lobby is a party on a Blaze playgroup that cGameEntryManager manages; a solo player queues
// without one. B11 answered create, finalize and leave for the game's chat; a party adds:
//
//   - create: the play screen's group tile (MatchTypeUI action 2 -> cMainMenuUI 0x004DE690) ->
//     PVPPlaygroup_Create (0x0068CD40) -> createPlaygroup {JOIN 0, PGRP {NAME <33-character uuid>, MLIM 5,
//     UKEY "", ATTR {}, ENBV 0}}: always a new playgroup (createOrJoin with an empty key);
//   - join: an invite (a Messaging message of type 0x176A0D2B, ATTR 0xB0003 = the playgroup id; not
//     relayed yet, B10b) -> PVPPlaygroup_Join (0x0068D260) -> joinPlaygroup {PGID, PNET};
//   - the leader's state for its guests: setPlaygroupAttributes "MatchmakingStatus" = "%X|%X|%X" (kGEMS
//     None 0 / Matchmaking 1 / Success 2, a counter, elapsed ms; 0x00684BF0) with setPlaygroupJoinControls
//     (closed while matchmaking, 0x00684AF0), and "GsrAttr" (the game server region, 0x00671A50);
//     members set their own "memberGEM" and "GsrAttr" with setMemberAttributes;
//   - kick (leader only, while its member has permission 1 = PERM bit 0x04) and leave (the leader too:
//     no caller sends destroyPlaygroup in the PvP flow; answered anyway).
//
// The senders' jobs have no response class (an empty reply), and the title's callback runs on the reply,
// but the client's copies of the attributes and join controls change only through the notifications
// (0x00C91140, 0x00C911C0, 0x00C91290), so every change goes to every member, the sender included
// [assumed for the sender: harmless, and it keeps the client's skip-if-equal check right]. Payloads from
// the client's member tables (docs/data/tdf_member_tables.txt); the server-side rules are ours where
// marked.
const (
	CmdPlaygroupsDestroyPlaygroup         = 0x02
	CmdPlaygroupsJoinPlaygroup            = 0x03
	CmdPlaygroupsSetPlaygroupAttributes   = 0x05
	CmdPlaygroupsSetMemberAttributes      = 0x06
	CmdPlaygroupsKickPlaygroupMember      = 0x07
	CmdPlaygroupsSetPlaygroupJoinControls = 0x08
	NotifyDestroyPlaygroupID              = 0x32
	NotifyPlaygroupAttributesSetID        = 0x36
	NotifyMemberAttributesSetID           = 0x4B
	NotifyLeaderChangeID                  = 0x4F
	NotifyMemberPermissionsChangeID       = 0x50
	NotifyJoinControlsChangeID            = 0x55
)

// Playgroups enum values and errors (name tables .rdata 0x012ED128 member remove reasons, 0x012ED148
// destroy reasons, 0x012ED160 join controls; errors: docs/data/blaze_components.txt, code << 16 | 6).
const (
	PlaygroupMemberRemoveKicked = 2 // PLAYGROUP_MEMBER_REMOVE_REASON_KICKED
	PlaygroupDestroyDefault     = 0 // PLAYGROUP_DESTROY_REASON_DEFAULT
	PlaygroupOpen               = 0 // PLAYGROUP_OPEN
	PlaygroupClosed             = 1 // PLAYGROUP_CLOSED
	ErrPlaygroupNotAuthorized   = 0x0002
	ErrPlaygroupNotFound        = 0x0005
	ErrPlaygroupClosed          = 0x0006
	ErrPlaygroupNotInGroup      = 0x0001
)

// PlaygroupAttributesSetNotification is Playgroups::NotifyPlaygroupAttributesSet (0x36; class table
// 0x012ED358: ATTR map<string,string>, PGID uint64).
func PlaygroupAttributesSetNotification(pgID int64, attrs []tdf.Pair) []byte {
	return tdf.NewWriter().Map("ATTR", tdf.TypeString, tdf.TypeString, attrs...).Int("PGID", pgID).Bytes()
}

// MemberAttributesSetNotification is Playgroups::NotifyMemberAttributesSet (0x4B; class table 0x012ED3A8:
// ATTR map<string,string>, EID int64 = the member's BlazeId, PGID uint64).
func MemberAttributesSetNotification(pgID, blazeID int64, attrs []tdf.Pair) []byte {
	return tdf.NewWriter().Map("ATTR", tdf.TypeString, tdf.TypeString, attrs...).Int("EID", blazeID).Int("PGID", pgID).Bytes()
}

// LeaderChangeNotification is Playgroups::NotifyLeaderChange (0x4F; class table 0x012ED2E0: HSID uint8
// the new leader's slot, LID int64 the new leader's BlazeId, PGID uint64).
func LeaderChangeNotification(pgID, leader, slot int64) []byte {
	return tdf.NewWriter().Int("HSID", slot).Int("LID", leader).Int("PGID", pgID).Bytes()
}

// MemberPermissionsChangeNotification is Playgroups::NotifyMemberPermissionsChange (0x50; class table
// 0x012ED310: LID int64 the member's BlazeId, PERM bitfield, PGID uint64). The client sets that member's
// permissions (0x00C8F410 -> 0x00C8DED0).
func MemberPermissionsChangeNotification(pgID, blazeID, perm int64) []byte {
	return tdf.NewWriter().Int("LID", blazeID).Int("PERM", perm).Int("PGID", pgID).Bytes()
}

// JoinControlsChangeNotification is Playgroups::NotifyJoinControlsChange (0x55; class table 0x01461FB8:
// OPEN enum PLAYGROUP_OPEN 0 / PLAYGROUP_CLOSED 1, PGID uint64).
func JoinControlsChangeNotification(pgID, open int64) []byte {
	return tdf.NewWriter().Int("OPEN", open).Int("PGID", pgID).Bytes()
}

// DestroyPlaygroupNotification is Playgroups::NotifyDestroyPlaygroup (0x32; class table 0x01462110: PGID
// uint64, REAS enum PlaygroupDestroyReason).
func DestroyPlaygroupNotification(pgID, reason int64) []byte {
	return tdf.NewWriter().Int("PGID", pgID).Int("REAS", reason).Bytes()
}

// registerPlaygroupLobby adds the PvP lobby's commands (called by registerPlaygroups). Every change is
// told to every member, the sender included, after the empty reply (see above). Playgroup attributes,
// join controls, kick and destroy are the leader's (others: PLAYGROUPS_ERR_NOT_AUTHORIZED [assumed]; the
// client only sends them as leader); member attributes are each member's own.
func registerPlaygroupLobby(s *Server) {
	// joinPlaygroup (JoinPlaygroupRequest 0x012ED238: PGID, PNET, UKEY, USER; PVPPlaygroup_Join 0x0068D260
	// -> PlaygroupAPI_JoinPlaygroup 0x00C90D80 -> sender 0x00C8E960; job 0x012E5E9C like createPlaygroup,
	// so the reply is JoinPlaygroupResponse, then NotifyJoinPlaygroup as for a create): join a party by
	// id; unknown: PLAYGROUPS_ERR_GROUP_NOT_FOUND, closed: _GROUP_CLOSED, full (MLIM 5): _GROUP_FULL.
	s.Register(ComponentPlaygroups, CmdPlaygroupsJoinPlaygroup, "joinPlaygroup", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			r.Drop("joinPlaygroup before login")
			return nil
		}
		id, _ := r.Body.Int("PGID")
		key, _ := r.Body.Str("UKEY")
		m := PlaygroupMember{AccountID: sess.Account.UserID, BlazeID: sess.Persona.ID, Name: sess.Persona.Name,
			JoinTime: r.Server.Now().Unix()}
		res := r.Server.groups.join(id, key, m, r.Conn)
		if res.err != 0 {
			r.Server.Log.Logf("blaze", "   playgroup %d %q: join by %d: error %#04x", id, key, m.BlazeID, res.err)
			r.Fail(res.err)
			return nil
		}
		return r.Server.answerJoin(r, res, m.BlazeID, "joined")
	})
	// setPlaygroupAttributes (SetPlaygroupAttributesRequest 0x012ED338: ATTR, PGID; Playgroup_SetAttributeValue
	// 0x00C90020 -> sender 0x00C8E6D0, and GameClient's direct send 0x0066D040 for GsrAttr): merge into the
	// playgroup's ATTR; NotifyPlaygroupAttributesSet (0x00C91140 merges, then the guests parse
	// MatchmakingStatus).
	s.Register(ComponentPlaygroups, CmdPlaygroupsSetPlaygroupAttributes, "setPlaygroupAttributes", func(r *Request) []byte {
		id, _ := r.Body.Int("PGID")
		attrs := stringMap(r.Body, "ATTR")
		return r.Server.lobbyUpdate(r, id, true, func(pg *playgroup) uint16 {
			pg.info.Attrs = mergeAttrs(pg.info.Attrs, attrs)
			return 0
		}, NotifyPlaygroupAttributesSetID, "NotifyPlaygroupAttributesSet", PlaygroupAttributesSetNotification(id, attrs),
			"attributes %v", attrs)
	})
	// setMemberAttributes (SetMemberAttributesRequest 0x012ED378: ATTR, EID = the member's BlazeId, PGID;
	// PlaygroupMember_SetAttributeValue 0x00C90390 -> sender 0x00C8EA30; "memberGEM", "GsrAttr",
	// "MatchmakingRestartSyncId"): merge into the member's ATTR; NotifyMemberAttributesSet (0x00C911C0
	// finds the member by EID).
	s.Register(ComponentPlaygroups, CmdPlaygroupsSetMemberAttributes, "setMemberAttributes", func(r *Request) []byte {
		id, _ := r.Body.Int("PGID")
		eid, _ := r.Body.Int("EID")
		attrs := stringMap(r.Body, "ATTR")
		self := r.Conn.Session != nil && r.Conn.Session.Persona.ID == eid
		return r.Server.lobbyUpdate(r, id, !self, func(pg *playgroup) uint16 { // others' attributes: the leader only
			i := pg.indexOf(eid)
			if i < 0 {
				return ErrPlaygroupNotInGroup
			}
			pg.members[i].Attrs = mergeAttrs(pg.members[i].Attrs, attrs)
			return 0
		}, NotifyMemberAttributesSetID, "NotifyMemberAttributesSet", MemberAttributesSetNotification(id, eid, attrs),
			"member %d attributes %v", eid, attrs)
	})
	// setPlaygroupJoinControls (SetJoinControlsRequest 0x01461FD8: OPEN, PGID; Playgroup_SetJoinControls
	// 0x00C8F700 -> sender 0x00C8E7B0; CLOSED while the party matchmakes): NotifyJoinControlsChange
	// (0x00C91290 sets INFO.JOIN); a closed party takes no new members.
	s.Register(ComponentPlaygroups, CmdPlaygroupsSetPlaygroupJoinControls, "setPlaygroupJoinControls", func(r *Request) []byte {
		id, _ := r.Body.Int("PGID")
		open, _ := r.Body.Int("OPEN")
		return r.Server.lobbyUpdate(r, id, true, func(pg *playgroup) uint16 {
			pg.info.JoinControl = open
			return 0
		}, NotifyJoinControlsChangeID, "NotifyJoinControlsChange", JoinControlsChangeNotification(id, open),
			"join controls %d", open)
	})
	// kickPlaygroupMember (KickPlaygroupMemberRequest 0x01462130: EID, PGID, REAS 2 KICKED; PVPLobby KickMember
	// 0x004F5D00 / context menu -> Playgroup_KickMember 0x00C8F8A0 -> sender 0x00C8E5F0): the leader removes
	// a member; everyone, the kicked member included, gets NotifyMemberRemovedFromPlaygroup (0x00C8F0C0: the
	// kicked client drops the party, SetPVPPlaygroup(0)).
	s.Register(ComponentPlaygroups, CmdPlaygroupsKickPlaygroupMember, "kickPlaygroupMember", func(r *Request) []byte {
		id, _ := r.Body.Int("PGID")
		eid, _ := r.Body.Int("EID")
		reason, ok := r.Body.Int("REAS")
		if !ok || reason == PlaygroupMemberRemoveDefault {
			reason = PlaygroupMemberRemoveKicked
		}
		return r.Server.lobbyUpdate(r, id, true, func(pg *playgroup) uint16 {
			i := pg.indexOf(eid)
			if i < 0 || pg.info.Owner == eid {
				return ErrPlaygroupNotInGroup
			}
			pg.members = append(pg.members[:i], pg.members[i+1:]...)
			return 0
		}, NotifyMemberRemovedFromPlaygroupID, "NotifyMemberRemovedFromPlaygroup",
			MemberRemovedFromPlaygroupNotification(id, eid, reason), "kicked %d (reason %d)", eid, reason)
	})
	// destroyPlaygroup (DestroyPlaygroupRequest 0x012ED228: PGID; sender 0x00C8E430, Playgroup_Destroy
	// 0x00C914F0, which no PvP code calls: a leader leaves instead): the leader ends the party; every member
	// gets NotifyDestroyPlaygroup.
	s.Register(ComponentPlaygroups, CmdPlaygroupsDestroyPlaygroup, "destroyPlaygroup", func(r *Request) []byte {
		id, _ := r.Body.Int("PGID")
		return r.Server.lobbyUpdate(r, id, true, func(pg *playgroup) uint16 {
			pg.members = nil
			return 0
		}, NotifyDestroyPlaygroupID, "NotifyDestroyPlaygroup", DestroyPlaygroupNotification(id, PlaygroupDestroyDefault),
			"destroyed")
	})
}

// answerJoin answers a successful create or join: the reply, NotifyJoinPlaygroup to the joiner and
// NotifyMemberJoinedPlaygroup to the members already in it (after the reply). Since B14 each of them first learns
// the other side as a user with its extended data (userDataNotes: UserAdded and UserUpdated, or
// UserSessionExtendedDataUpdate for a user it knows), so the party lobby shows every member's wins.
func (s *Server) answerJoin(r *Request, res joinResult, blazeID int64, how string) []byte {
	if res.again {
		how += " again"
	}
	s.Log.Logf("blaze", "   playgroup %d %q %s by %d (%d members)", res.info.ID, res.info.Key, how, blazeID, len(res.members))
	pgID, conn := res.info.ID, r.Conn
	r.Conn.OnSessionEnd(func() { s.playgroupLeft(pgID, blazeID, conn, PlaygroupMemberRemoveDisconnected) })
	for _, m := range res.members {
		if m.BlazeID != blazeID {
			for _, n := range s.userDataNotes(r.Conn, m.BlazeID) {
				r.Notify(ComponentUserSessions, n.id, n.name, n.payload)
			}
		}
	}
	r.Notify(ComponentPlaygroups, NotifyJoinPlaygroupID, "NotifyJoinPlaygroup", JoinPlaygroupNotification(res.info, res.members, blazeID))
	if len(res.others) > 0 {
		note := MemberJoinedPlaygroupNotification(res.info.ID, res.member)
		r.After(func() {
			for _, c := range res.others {
				for _, n := range s.userDataNotes(c, blazeID) {
					c.Push(s, ComponentUserSessions, n.id, n.name, n.payload)
				}
				c.Push(s, ComponentPlaygroups, NotifyMemberJoinedPlaygroupID, "NotifyMemberJoinedPlaygroup", note)
			}
		})
	}
	return JoinPlaygroupReply(res.info)
}

// lobbyUpdate runs a lobby change for the request's user and, after the empty reply, sends note to the
// members (and to removed ones). Errors become error replies.
func (s *Server) lobbyUpdate(r *Request, id int64, leaderOnly bool, fn func(pg *playgroup) uint16, noteID uint16,
	noteName string, note []byte, format string, args ...any) []byte {
	sess := r.Conn.Session
	if sess == nil || sess.Persona.ID == 0 {
		r.Drop("playgroup change before login")
		return nil
	}
	ch := s.groups.update(id, sess.Persona.ID, leaderOnly, fn)
	if ch.err != 0 {
		s.Log.Logf("blaze", "   playgroup %d: %s by %d: error %#04x", id, noteName, sess.Persona.ID, ch.err)
		r.Fail(ch.err)
		return nil
	}
	s.Log.Logf("blaze", "   playgroup %d: "+format+" (by %d, %d members)", append(append([]any{id}, args...), sess.Persona.ID, len(ch.conns))...)
	r.After(func() {
		for _, c := range append(ch.conns, ch.gone...) {
			c.Push(s, ComponentPlaygroups, noteID, noteName, note)
		}
	})
	return nil
}

// mergeAttrs returns old with set's entries added or replaced, ordered by key as the client looks them up
// (case-insensitively, StricmpCompare: its attribute maps keep the wire order; G3). Entries without a string key
// are dropped.
func mergeAttrs(old, set []tdf.Pair) []tdf.Pair {
	byKey := map[string]tdf.Pair{}
	for _, list := range [][]tdf.Pair{old, set} {
		for _, p := range list {
			if k, ok := p.Key.(string); ok {
				byKey[k] = p
			}
		}
	}
	out := make([]tdf.Pair, 0, len(byKey))
	for _, p := range byKey {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b tdf.Pair) int { return StricmpCompare(a.Key.(string), b.Key.(string)) })
	return out
}

// stringMap reads a map<string,string> member of a request.
func stringMap(s *tdf.Struct, label string) []tdf.Pair {
	var out []tdf.Pair
	if f, ok := s.Get(label); ok {
		if m, ok := f.Value.(*tdf.Map); ok {
			for _, e := range m.Entries {
				k, kok := e.Key.(string)
				v, vok := e.Value.(string)
				if kok && vok {
					out = append(out, tdf.KV(k, v))
				}
			}
		}
	}
	return out
}

// lobbyChange is what a lobby operation on a playgroup did: who to tell, or an error.
type lobbyChange struct {
	pg    PlaygroupInfo
	conns []*Conn // every member's connection after the change
	gone  []*Conn // removed members' connections (kicked, or everyone on destroy)
	err   uint16
}

// update runs fn on playgroup id for blazeID, who must be a member (leaderOnly: the owner), and returns
// whom to tell. fn may remove members; a playgroup left without members is deleted.
func (p *playgroups) update(id, blazeID int64, leaderOnly bool, fn func(pg *playgroup) uint16) lobbyChange {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg := p.byID[id]
	switch {
	case pg == nil:
		return lobbyChange{err: ErrPlaygroupNotFound}
	case pg.indexOf(blazeID) < 0:
		return lobbyChange{err: ErrPlaygroupNotInGroup}
	case leaderOnly && pg.info.Owner != blazeID:
		return lobbyChange{err: ErrPlaygroupNotAuthorized}
	}
	before := append([]pgMember{}, pg.members...)
	if err := fn(pg); err != 0 {
		return lobbyChange{err: err}
	}
	ch := lobbyChange{pg: pg.info}
	for _, m := range before {
		switch {
		case m.conn == nil:
		case pg.indexOf(m.BlazeID) < 0:
			ch.gone = append(ch.gone, m.conn)
		default:
			ch.conns = append(ch.conns, m.conn)
		}
	}
	if len(pg.members) == 0 {
		p.dropLocked(pg)
	}
	return ch
}

// snapshot returns playgroup id's members (with their connections) and owner.
func (p *playgroups) snapshot(id int64) (members []pgMember, owner int64, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg := p.byID[id]
	if pg == nil {
		return nil, 0, false
	}
	return append([]pgMember{}, pg.members...), pg.info.Owner, true
}

// join adds m to playgroup id (joinPlaygroup; by its unique key when id is 0). A closed playgroup
// (setPlaygroupJoinControls PLAYGROUP_CLOSED) takes nobody new: PLAYGROUPS_ERR_GROUP_CLOSED.
func (p *playgroups) join(id int64, key string, m PlaygroupMember, conn *Conn) joinResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg := p.byID[id]
	if pg == nil && id == 0 && key != "" {
		pg = p.byKey[key]
	}
	switch {
	case pg == nil:
		return joinResult{err: ErrPlaygroupNotFound}
	case pg.info.JoinControl == PlaygroupClosed && pg.indexOf(m.BlazeID) < 0:
		return joinResult{err: ErrPlaygroupClosed}
	}
	return pg.addLocked(m, conn)
}
