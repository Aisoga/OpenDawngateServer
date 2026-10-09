package blaze

import (
	"sync"

	"dawngate/server/internal/tdf"
)

// Playgroups component (6), BlazeSDK's standard one (B11; docs/BLAZE.md *Game chat playgroup (B11)*).
// Command and notification ids from docs/data/blaze_components.txt. The notification dispatcher is the
// component's vtable slot 5 (0x00CA8F80, jump table 0x00CA9468 by id - 0x32); PlaygroupAPI (constructor
// 0x00C917F0) registers its handlers in 0x00C915C0: NotifyJoinPlaygroup -> 0x00C8ECD0,
// NotifyMemberJoinedPlaygroup -> 0x00C8EFE0, NotifyMemberRemoveFromPlaygroup -> 0x00C8F0C0,
// NotifyDestroyPlaygroup -> 0x00C914D0.
//
// What the game uses it for during a match: its chat (cBlazeClientPlaygroup, snblaze_rooms.cpp) joins
// the playgroup "GG-<game id>" with createPlaygroup (JOIN 1: create or join by UKEY), and leaves it with
// leavePlaygroup when the key is cleared (0x00AAE2B0 -> Leave 0x00AAE170). The client's side of a create:
//
//	createPlaygroup -> JoinPlaygroupResponse: PlaygroupAPI's callback (0x00C90970) makes a Playgroup
//	  (0x00C905C0) from INFO, keeps the title's callback in it and lists it as pending (+0x1A0);
//	NotifyJoinPlaygroup: 0x00C8ECD0 finds that playgroup by INFO.PGID (asserts "false" otherwise), takes
//	  INFO and MLST (0x00C90880; the member whose USER.ID equals USER is the local one), then, with ENBV 0
//	  (no network mesh), sends finalizePlaygroupCreation {PGID} and calls the title's callback with
//	  ERR_OK (0x00C8DFB0);
//	the chat's callback (0x00AACAD0) then wants INFO.ATTR PlaygroupKey equal to the key it asked for.
//
// Without the reply the RPC times out and the chat asserts (snblaze_rooms.cpp 557 and 604, "Failed to
// create or join chat playgroup (GG-1) with error -2147221504 (SDK_ERR_RPC_TIMEOUT)", every session
// since B9); without the notification the callback never runs.
const (
	ComponentPlaygroups                    = 0x0006
	CmdPlaygroupsCreatePlaygroup           = 0x01
	CmdPlaygroupsLeavePlaygroup            = 0x04
	CmdPlaygroupsFinalizePlaygroupCreation = 0x09
	NotifyJoinPlaygroupID                  = 0x33
	NotifyMemberJoinedPlaygroupID          = 0x34
	NotifyMemberRemovedFromPlaygroupID     = 0x35
)

// Playgroups enum values and error codes, from the client's name tables (.rdata 0x012ED128: member
// remove reasons; 0x012ED160: join controls; errors: docs/data/blaze_components.txt, code << 16 | 6).
const (
	PlaygroupMemberRemoveDefault      = 0      // PLAYGROUP_MEMBER_REMOVE_REASON_DEFAULT
	PlaygroupMemberRemoveDisconnected = 1      // PLAYGROUP_MEMBER_REMOVE_REASON_DISCONNECTED
	ErrPlaygroupFull                  = 0x0003 // PLAYGROUPS_ERR_GROUP_FULL
	ErrPlaygroupAlreadyExists         = 0x0008 // PLAYGROUPS_ERR_GROUP_ALREADY_EXISTS
)

// PlaygroupKeyAttr is the attribute the game's chat checks after the join: its value must be the key it
// asked for (0x00AACAD0, asserts "finder != map->end()" and "mKey.compare(finder->second) == 0").
const PlaygroupKeyAttr = "PlaygroupKey"

// PlaygroupInfo is a playgroup as Blaze::Playgroups::PlaygroupInfo (table 0x01462028) carries it. The
// enums keep the request's values: JOIN (join controls, PLAYGROUP_OPEN 0), NTOP (GameNetworkTopology),
// PRES (PresenceMode), VOIP (VoipTopology, VOIP_DISABLED 0). ENBV (bool +0xCC) is copied into the client's
// Playgroup (+0x166) and decides whether it builds a network mesh (PlaygroupAPI +0x238) for the group; the
// chat asks for 0. HNET (the host's NetworkAddress), XNNC and XSES (Xbox session blobs) keep their
// defaults (the chat's request has HNET unset).
type PlaygroupInfo struct {
	ID          int64      // PGID, from 1
	Name        string     // NAME
	Key         string     // UKEY, the unique key the game joins by ("GG-<game id>")
	Attrs       []tdf.Pair // ATTR (string -> string)
	MemberLimit int64      // MLIM
	Owner       int64      // OWNR: the creator's BlazeId; the client's leader lookup (Playgroup slot 0x10) reads it
	JoinControl int64      // JOIN
	Topology    int64      // NTOP
	Presence    int64      // PRES
	Voip        int64      // VOIP
	EnableNet   int64      // ENBV
	UserPres    int64      // UPRS
	UUID        string     // UUID
}

// PlaygroupMember is one member as Blaze::Playgroups::PlaygroupMemberInfo (table 0x012ED170) carries it.
// The client makes its member object from it (0x00C902B0) and looks its user up by USER
// (Blaze::UserIdentification 0x012F1C70) through the user manager (0x00C8CA30); later notifications find
// the member by that user's BlazeId. PNET (no mesh) keeps its default; ATTR and PERM are B10a's (a PvP
// party's member attributes, and its leader's permissions).
type PlaygroupMember struct {
	AccountID int64      // USER.AID
	BlazeID   int64      // USER.ID: the persona id, as everywhere else (BUID)
	Name      string     // USER.NAME
	Slot      int64      // SID
	JoinTime  int64      // JTIM, seconds
	Attrs     []tdf.Pair // ATTR (string -> string, by key; setMemberAttributes); written only when set
	Perm      int64      // PERM bitfield (no name table in the client); see PartyLeaderPermissions
}

// PartyLeaderPermissions is the PERM of a PvP party's leader (B10a). The client kicks only while its own
// member has permission 1 (Playgroup_LocalMemberHasPermission 0x00C8D540; permission index -> bit: 0 ->
// 0x1, 1 -> 0x4, 2 -> 0x2, 3 -> 0x8, 4 -> 0x10), so the leader gets every bit [assumed: the other bits'
// meanings are unknown]. Other members, and every member of a keyed playgroup (the game's chat, B11,
// whose bytes the live client accepted), keep 0.
const PartyLeaderPermissions = 0x1F

func writePlaygroupInfo(w *tdf.Writer, pg PlaygroupInfo) {
	w.Map("ATTR", tdf.TypeString, tdf.TypeString, pg.Attrs...)
	w.Int("ENBV", pg.EnableNet)
	w.Int("HSID", 0)
	w.Int("JOIN", pg.JoinControl)
	w.Int("MLIM", pg.MemberLimit)
	w.String("NAME", pg.Name)
	w.Int("NTOP", pg.Topology)
	w.Int("OWNR", pg.Owner)
	w.Int("PGID", pg.ID)
	w.Int("PRES", pg.Presence)
	w.String("UKEY", pg.Key)
	w.Int("UPRS", pg.UserPres)
	w.String("UUID", pg.UUID)
	w.Int("VOIP", pg.Voip)
}

func writePlaygroupMember(w *tdf.Writer, m PlaygroupMember) {
	if len(m.Attrs) > 0 {
		w.Map("ATTR", tdf.TypeString, tdf.TypeString, m.Attrs...)
	}
	w.Int("JTIM", m.JoinTime)
	w.Int("PERM", m.Perm)
	w.Int("SID", m.Slot)
	w.Struct("USER", func(w *tdf.Writer) {
		w.Int("AID", m.AccountID).Int("ALOC", LocaleEnUS).Int("ID", m.BlazeID).String("NAME", m.Name)
	})
}

// JoinPlaygroupReply is Playgroups::createPlaygroup's (and joinPlaygroup's) reply,
// Blaze::Playgroups::JoinPlaygroupResponse (table 0x012ED270: INFO struct PlaygroupInfo). Response class
// from the RPC job 0x012E5E9C (constructed at 0x00C8D909), which both senders use: createPlaygroup
// 0x00C8E890 (cmd 1) and joinPlaygroup 0x00C8E960 (cmd 3).
func JoinPlaygroupReply(pg PlaygroupInfo) []byte {
	return tdf.NewWriter().Struct("INFO", func(w *tdf.Writer) { writePlaygroupInfo(w, pg) }).Bytes()
}

// JoinPlaygroupNotification is Playgroups::NotifyJoinPlaygroup (0x33; dispatcher case 0x00CA90B1, class
// Blaze::Playgroups::NotifyJoinPlaygroup table 0x012ED288: INFO struct, MLST list of PlaygroupMemberInfo,
// USER int64 = the joining user's BlazeId). It must follow the reply (the client finds the pending
// playgroup by INFO.PGID) and carry the PlaygroupKey attribute again, since INFO replaces the reply's.
func JoinPlaygroupNotification(pg PlaygroupInfo, members []PlaygroupMember, user int64) []byte {
	ms := make([]*tdf.Writer, len(members))
	for i, m := range members {
		ms[i] = tdf.NewWriter()
		writePlaygroupMember(ms[i], m)
	}
	w := tdf.NewWriter()
	w.Struct("INFO", func(w *tdf.Writer) { writePlaygroupInfo(w, pg) })
	w.List("MLST", tdf.TypeStruct, ms)
	w.Int("USER", user)
	return w.Bytes()
}

// MemberJoinedPlaygroupNotification is Playgroups::NotifyMemberJoinedPlaygroup (0x34; class table
// 0x012ED2B0: MEMB struct PlaygroupMemberInfo, PGID uint64), sent to the members already in the group.
// The client (0x00C8EFE0) finds the playgroup by PGID and adds the member (Playgroup slot 0x74).
func MemberJoinedPlaygroupNotification(pgID int64, m PlaygroupMember) []byte {
	w := tdf.NewWriter()
	w.Struct("MEMB", func(w *tdf.Writer) { writePlaygroupMember(w, m) })
	w.Int("PGID", pgID)
	return w.Bytes()
}

// MemberRemovedFromPlaygroupNotification is Playgroups::NotifyMemberRemovedFromPlaygroup (0x35; class
// Blaze::Playgroups::NotifyMemberRemoveFromPlaygroup, table 0x01461FF8: MLST int64 = the member's
// BlazeId, PGID uint64, REAS enum PlaygroupMemberRemoveReason), sent to the members that stay. The client
// (0x00C8F0C0) finds the playgroup by PGID and the member by its user's BlazeId and removes it (Playgroup
// slot 0x78). The leaver itself needs none: its leavePlaygroup callback (0x00C8DD60) removes it locally.
func MemberRemovedFromPlaygroupNotification(pgID, blazeID, reason int64) []byte {
	return tdf.NewWriter().Int("MLST", blazeID).Int("PGID", pgID).Int("REAS", reason).Bytes()
}

// playgroups are the server's playgroups (B11), in memory: by id and by unique key.
type playgroups struct {
	mu     sync.Mutex
	nextID int64
	byID   map[int64]*playgroup
	byKey  map[string]*playgroup
}

type playgroup struct {
	info    PlaygroupInfo
	members []pgMember
	nextSID int64
}

type pgMember struct {
	PlaygroupMember
	conn *Conn
}

func (pg *playgroup) memberList() []PlaygroupMember {
	out := make([]PlaygroupMember, len(pg.members))
	for i, m := range pg.members {
		out[i] = m.PlaygroupMember
	}
	return out
}

func (pg *playgroup) indexOf(blazeID int64) int {
	for i, m := range pg.members {
		if m.BlazeID == blazeID {
			return i
		}
	}
	return -1
}

// others returns the connections of every member except blazeID.
func (pg *playgroup) others(blazeID int64) []*Conn {
	var out []*Conn
	for _, m := range pg.members {
		if m.BlazeID != blazeID && m.conn != nil {
			out = append(out, m.conn)
		}
	}
	return out
}

// joinResult is what createOrJoin did: the playgroup as the joiner sees it, and who else to tell.
type joinResult struct {
	info    PlaygroupInfo
	members []PlaygroupMember
	member  PlaygroupMember
	others  []*Conn // members that get NotifyMemberJoinedPlaygroup
	created bool
	again   bool   // the user was a member already (nothing new for the others)
	err     uint16 // a Playgroups error code instead
}

// createOrJoin makes the playgroup req describes, or joins the one with its UKEY if req.join is set (the
// game's chat asks "create or join", JOIN 1). Our reading of BlazeSDK's server side, which isn't in the
// client: an existing key without JOIN is PLAYGROUPS_ERR_GROUP_ALREADY_EXISTS, a full group (MLIM)
// PLAYGROUPS_ERR_GROUP_FULL; a member who asks again gets the group as it is.
func (p *playgroups) createOrJoin(req PlaygroupInfo, join bool, m PlaygroupMember, conn *Conn) joinResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byID == nil {
		p.byID, p.byKey = map[int64]*playgroup{}, map[string]*playgroup{}
	}
	var res joinResult
	pg := p.byKey[req.Key]
	switch {
	case pg == nil || req.Key == "":
		p.nextID++
		req.ID, req.Owner = p.nextID, m.BlazeID
		pg = &playgroup{info: req}
		p.byID[req.ID] = pg
		if req.Key != "" {
			p.byKey[req.Key] = pg
		}
		res.created = true
	case !join:
		return joinResult{err: ErrPlaygroupAlreadyExists}
	}
	res2 := pg.addLocked(m, conn)
	res2.created = res.created
	return res2
}

// addLocked makes m a member of pg (the playgroups' lock held): a member asking again gets the group as
// it is; a full group (MLIM) is PLAYGROUPS_ERR_GROUP_FULL.
func (pg *playgroup) addLocked(m PlaygroupMember, conn *Conn) joinResult {
	var res joinResult
	if i := pg.indexOf(m.BlazeID); i >= 0 {
		pg.members[i].conn = conn
		res.again = true
		res.member = pg.members[i].PlaygroupMember
	} else {
		if lim := pg.info.MemberLimit; lim > 0 && int64(len(pg.members)) >= lim {
			return joinResult{err: ErrPlaygroupFull}
		}
		m.Slot = pg.nextSID
		pg.nextSID++
		if pg.info.Key == "" && pg.info.Owner == m.BlazeID { // a PvP party's creator (B10a)
			m.Perm = PartyLeaderPermissions
		}
		pg.members = append(pg.members, pgMember{PlaygroupMember: m, conn: conn})
		res.member = m
		res.others = pg.others(m.BlazeID)
	}
	res.info, res.members = pg.info, pg.memberList()
	return res
}

// leave removes blazeID from playgroup id and returns the connections of the members that stay; a
// playgroup without members is gone. With conn set, only a member that still belongs to conn leaves (a
// closed connection whose user has joined again from another one doesn't take the member along). ok is
// false if nothing was removed. If the owner left and others stay, the longest member becomes the owner
// (leader is then that member; B10a, our reading: BlazeSDK's server isn't in the client).
func (p *playgroups) leave(id, blazeID int64, conn *Conn) (others []*Conn, leader *PlaygroupMember, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg := p.byID[id]
	if pg == nil {
		return nil, nil, false
	}
	i := pg.indexOf(blazeID)
	if i < 0 || conn != nil && pg.members[i].conn != conn {
		return nil, nil, false
	}
	pg.members = append(pg.members[:i], pg.members[i+1:]...)
	if len(pg.members) == 0 {
		p.dropLocked(pg)
		return nil, nil, true
	}
	if pg.info.Owner == blazeID {
		pg.info.Owner = pg.members[0].BlazeID
		if pg.info.Key == "" {
			pg.members[0].Perm = PartyLeaderPermissions
		}
		m := pg.members[0].PlaygroupMember
		leader = &m
	}
	return pg.others(blazeID), leader, true
}

// dropLocked deletes pg (p.mu held).
func (p *playgroups) dropLocked(pg *playgroup) {
	delete(p.byID, pg.info.ID)
	if p.byKey[pg.info.Key] == pg {
		delete(p.byKey, pg.info.Key)
	}
}

// count returns how many playgroups exist (for tests and logs).
func (p *playgroups) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byID)
}

func registerPlaygroups(s *Server) {
	registerPlaygroupLobby(s) // B10a: the PvP lobby's commands (playgroups_lobby.go)
	// createPlaygroup (Blaze::Playgroups::CreatePlaygroupRequest 0x012ED1B0: JOIN bool, PGRP PlaygroupInfo)
	// is how the game's chat joins the game's playgroup, right after the client's join of game N (B9,
	// every session since b9_run1): JOIN 1, PGRP {ATTR {PlaygroupKey 'GG-N'}, MLIM 15, NAME/UKEY 'GG-N',
	// HNET unset, the rest 0 or empty}. Reply, then NotifyJoinPlaygroup to the joiner and
	// NotifyMemberJoinedPlaygroup to the members already in it (after the reply).
	s.Register(ComponentPlaygroups, CmdPlaygroupsCreatePlaygroup, "createPlaygroup", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			r.Drop("createPlaygroup before login")
			return nil
		}
		join, _ := r.Body.Int("JOIN")
		req := PlaygroupInfo{}
		if pgrp, ok := r.Body.Sub("PGRP"); ok {
			req = playgroupFromRequest(pgrp)
		}
		m := PlaygroupMember{AccountID: sess.Account.UserID, BlazeID: sess.Persona.ID, Name: sess.Persona.Name,
			JoinTime: r.Server.Now().Unix()}
		res := r.Server.groups.createOrJoin(req, join != 0, m, r.Conn)
		if res.err != 0 {
			r.Server.Log.Logf("blaze", "   playgroup %q: error %#04x", req.Key, res.err)
			r.Fail(res.err)
			return nil
		}
		how := "joined"
		if res.created {
			how = "created"
		}
		// A closed connection takes its member along (a duplicate hook finds nothing to remove).
		return r.Server.answerJoin(r, res, m.BlazeID, how)
	})
	// leavePlaygroup (Blaze::Playgroups::LeavePlaygroupRequest 0x012ED2D0: PGID) is what the chat sends
	// when its key is cleared (game over; cBlazeClientPlaygroup::Leave 0x00AAE170 -> Playgroup 0x00C8F630).
	// Its job (0x012E5E5C, sender 0x00C8E510) has no response class, so the reply is empty; the client then
	// removes itself (0x00C8DD60). The members that stay get NotifyMemberRemovedFromPlaygroup.
	s.Register(ComponentPlaygroups, CmdPlaygroupsLeavePlaygroup, "leavePlaygroup", func(r *Request) []byte {
		pgID, _ := r.Body.Int("PGID")
		if sess := r.Conn.Session; sess != nil {
			blazeID := sess.Persona.ID
			r.After(func() { r.Server.playgroupLeft(pgID, blazeID, nil, PlaygroupMemberRemoveDefault) })
		}
		return nil
	})
	// finalizePlaygroupCreation (Blaze::Playgroups::UpdatePlaygroupSessionRequest 0x012ED1C8: PGID, XNNC,
	// XSES) follows NotifyJoinPlaygroup for a playgroup without a network mesh (ENBV 0, 0x00C8EDF5): PGID
	// and empty Xbox session blobs. Sender 0x00C8DBF0 with the generic job 0x011F7C00 (no response class)
	// and no callback, so the reply is empty.
	s.Register(ComponentPlaygroups, CmdPlaygroupsFinalizePlaygroupCreation, "finalizePlaygroupCreation",
		func(r *Request) []byte {
			pgID, _ := r.Body.Int("PGID")
			r.Server.Log.Logf("blaze", "   playgroup %d: creation finalized", pgID)
			return nil
		})
}

// playgroupLeft takes blazeID out of playgroup pgID (only if it still belongs to conn, unless conn is
// nil) and tells the members that stay.
func (s *Server) playgroupLeft(pgID, blazeID int64, conn *Conn, reason int64) {
	others, leader, ok := s.groups.leave(pgID, blazeID, conn)
	if !ok {
		return
	}
	s.Log.Logf("blaze", "   playgroup %d: %d left (reason %d, %d members stay)", pgID, blazeID, reason, len(others))
	note := MemberRemovedFromPlaygroupNotification(pgID, blazeID, reason)
	for _, c := range others {
		c.Push(s, ComponentPlaygroups, NotifyMemberRemovedFromPlaygroupID, "NotifyMemberRemovedFromPlaygroup", note)
	}
	if leader != nil { // B10a: the owner left; a party's new leader also gets the leader's permissions
		s.Log.Logf("blaze", "   playgroup %d: %d is the new leader", pgID, leader.BlazeID)
		lc := LeaderChangeNotification(pgID, leader.BlazeID, leader.Slot)
		var pc []byte
		if leader.Perm != 0 {
			pc = MemberPermissionsChangeNotification(pgID, leader.BlazeID, leader.Perm)
		}
		for _, c := range others {
			c.Push(s, ComponentPlaygroups, NotifyLeaderChangeID, "NotifyLeaderChange", lc)
			if pc != nil {
				c.Push(s, ComponentPlaygroups, NotifyMemberPermissionsChangeID, "NotifyMemberPermissionsChange", pc)
			}
		}
	}
}

// playgroupFromRequest reads a create request's PGRP: everything the server keeps as the client asked,
// except the id and the owner (assigned by createOrJoin).
func playgroupFromRequest(s *tdf.Struct) PlaygroupInfo {
	var pg PlaygroupInfo
	pg.Name, _ = s.Str("NAME")
	pg.Key, _ = s.Str("UKEY")
	pg.MemberLimit, _ = s.Int("MLIM")
	pg.JoinControl, _ = s.Int("JOIN")
	pg.Topology, _ = s.Int("NTOP")
	pg.Presence, _ = s.Int("PRES")
	pg.Voip, _ = s.Int("VOIP")
	pg.EnableNet, _ = s.Int("ENBV")
	pg.UserPres, _ = s.Int("UPRS")
	pg.UUID, _ = s.Str("UUID")
	pg.Attrs = stringMap(s, "ATTR")
	return pg
}
