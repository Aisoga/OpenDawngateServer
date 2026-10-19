package blaze

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"dawngate/server/internal/tdf"
)

// Join codes: "Join" from a chat app's profile (the launcher's side of it). A player's launcher asks the server for a
// code that stands for the player's party, practice group or custom lobby and shows it to the player's friends; a
// friend's launcher hands the code back to its own server connection, and the server sends that friend the game's own
// invite in the name of the party's leader or the lobby's owner. The friend's client shows its usual invite pop-up,
// and its YES joins the usual way. The client can't be put into a party or lobby any other way: its
// NotifyJoinPlaygroup handler (0x00C8ECD0) takes only a join it asked for.
//
// The client's invites (GameEntryInviteManager.cpp, read statically): OnUserMessage (0x0067FB30) takes a user message
// of TYPE 0x176A0D2B (a PvP party, kind 2: ATTR 0xB0003 the playgroup id), 0x8FBFA37C (a custom game, kind 4) or
// 0x237748AA (a practice group's game, kind 8; both ATTR 0xB0004 the game id); each needs a non-zero id, a non-zero
// invite number 0xB0005 and a non-empty sender name 0xB0000, and keeps 0xB0006 (parsed as a number) beside them. A
// client whose kinds mask (vtable +0x4C) lacks the kind answers "I" at once; otherwise the invite replaces older ones
// from the same sender (SRCE). Accepting it (ChatUI_RespondToInvite 0x004A02C0): kind 2 -> PVPPlaygroup_Join (game
// entry manager slot 0x74) unless it is the current party; kinds 4 and 8 -> unless it is the current game,
// Game_JoinPublic (slot 0x2C, 0x00692F80) with team 0xFFFF for kind 4 and 0 for kind 8 when 0xB0006 is 0, else
// Game_JoinPrivate (slot 0x30, 0x006930A0) with 0xB0006 as the private game's key (the PrivateAuthToken player
// attribute). No invite leads to a spectator join (team 2): a started custom game has no Join. The client then answers
// the sender (0xA501C11E, 0x55722FA1, 0xEBBABA1B with 0xB0002 "T" and the invite's number).
//
// The launcher's channel is a Blaze component the client doesn't have (ComponentJoinCodes; the client's 19 components
// are in fire.Components): the launcher's front puts these two requests on the player's own Blaze connection, so the
// server knows the persona from the connection, and takes their replies off it before the game sees them.

// The launcher's join-code component and its commands.
const (
	ComponentJoinCodes = 0x7D00 // no component of the client's (fire.Components); never announced in preAuth
	CmdJoinCodesIssue  = 0x01   // issueJoinCode {KIND, ID} -> JoinCodeInfo
	CmdJoinCodesRedeem = 0x02   // redeemJoinCode {CODE} -> JoinCodeRedeemed
)

// The targets an issueJoinCode request names (its KIND).
const (
	JoinTargetParty = 1 // ID = the party's playgroup id (PGID)
	JoinTargetGame  = 2 // ID = the game id (GID) of a practice group or custom game in its lobby
)

// What a code stands for (the replies' KIND).
const (
	JoinKindParty         = 1 // a PvP party (an unkeyed playgroup)
	JoinKindPracticeGroup = 2 // a practice group's lobby game (GameFlags 0x412)
	JoinKindCustomGame    = 3 // a custom game's lobby (GameFlags 2)
)

// The replies' RSN: why a code can't be issued or used (now). JoinOK is success. Temporary reasons (full, closed)
// still come with a code; the others with none.
const (
	JoinOK          = 0
	JoinNotLoggedIn = 1  // the connection has no persona (before loginPersona)
	JoinGone        = 2  // no such code, party or lobby, it ended, or every member that issued the code left it
	JoinNotMember   = 3  // issue: the caller isn't in the party or lobby
	JoinFull        = 4  // a full party (MLIM), a custom game with both teams full, a full practice group
	JoinClosed      = 5  // the party is closed (its leader is matchmaking: setPlaygroupJoinControls CLOSED)
	JoinPrivate     = 6  // a private custom game (PrivateMatch "1"): never joinable
	JoinStarted     = 7  // the lobby's game started: no invite leads to a spectator seat
	JoinAlreadyIn   = 8  // redeem: the caller is in it already
	JoinBlocked     = 9  // redeem: the caller blocked the leader or owner (its client would drop the invite)
	JoinUnsupported = 10 // issue: a request KIND we don't know, a game chat playgroup, a game without a lobby
	JoinRateLimited = 11 // redeem: over the caller's Messaging rate limit (MessagingLimits)
)

// JoinReasonName names a RSN for logs.
func JoinReasonName(rsn int64) string {
	names := []string{"ok", "not logged in", "gone", "not a member", "full", "closed", "private", "started",
		"already in it", "blocked", "not joinable", "rate limited"}
	if rsn >= 0 && rsn < int64(len(names)) {
		return names[rsn]
	}
	return "reason " + strconv.FormatInt(rsn, 10)
}

// JoinInviteFirst is the first invite number the server uses for its invites (0xB0005, counting up). The client's
// own invites count from 1 (GameEntryInviteManager +0x2C), so an answer with a number this high is to the server's.
const JoinInviteFirst = 1_000_000_000

// joinInviteTTL is how long the server waits for the answer to one of its invites (to drop it).
const joinInviteTTL = 15 * time.Minute

// JoinCodeInfo is issueJoinCode's reply: CODE the code ("" when none is issued), JOIN 1 when a friend can join now,
// KIND what it is, MAX its seats (0 = no limit), RSN JoinOK or why not, SIZE its members now. For a custom game SIZE
// and MAX count the two teams' seats, not the spectators.
type JoinCodeInfo struct {
	Code     string
	Joinable bool
	Kind     int64
	Max      int64
	Reason   int64
	Size     int64
}

// JoinCodeIssueRequest is issueJoinCode's request: KIND (JoinTarget*), ID (the PGID or GID).
func JoinCodeIssueRequest(target, id int64) []byte {
	return tdf.NewWriter().Int("ID", id).Int("KIND", target).Bytes()
}

// JoinCodeRedeemRequest is redeemJoinCode's request: CODE.
func JoinCodeRedeemRequest(code string) []byte {
	return tdf.NewWriter().String("CODE", code).Bytes()
}

// JoinCodeIssueReply writes issueJoinCode's reply.
func JoinCodeIssueReply(in JoinCodeInfo) []byte {
	join := int64(0)
	if in.Joinable {
		join = 1
	}
	return tdf.NewWriter().String("CODE", in.Code).Int("JOIN", join).Int("KIND", in.Kind).Int("MAX", in.Max).
		Int("RSN", in.Reason).Int("SIZE", in.Size).Bytes()
}

// ParseJoinCodeIssueReply reads issueJoinCode's reply.
func ParseJoinCodeIssueReply(payload []byte) (JoinCodeInfo, error) {
	st, err := tdf.Decode(payload)
	if err != nil {
		return JoinCodeInfo{}, err
	}
	var in JoinCodeInfo
	in.Code, _ = st.Str("CODE")
	join, _ := st.Int("JOIN")
	in.Joinable = join != 0
	in.Kind, _ = st.Int("KIND")
	in.Max, _ = st.Int("MAX")
	in.Size, _ = st.Int("SIZE")
	var ok bool
	if in.Reason, ok = st.Int("RSN"); !ok {
		return in, fmt.Errorf("issueJoinCode reply without RSN")
	}
	return in, nil
}

// JoinCodeRedeemed is redeemJoinCode's reply: ID the party's or game's id, KIND what it is, RSN JoinOK (the invite went
// to the caller's connections) or why not.
type JoinCodeRedeemed struct {
	ID     int64
	Kind   int64
	Reason int64
}

// JoinCodeRedeemReply writes redeemJoinCode's reply.
func JoinCodeRedeemReply(r JoinCodeRedeemed) []byte {
	return tdf.NewWriter().Int("ID", r.ID).Int("KIND", r.Kind).Int("RSN", r.Reason).Bytes()
}

// ParseJoinCodeRedeemReply reads redeemJoinCode's reply.
func ParseJoinCodeRedeemReply(payload []byte) (JoinCodeRedeemed, error) {
	st, err := tdf.Decode(payload)
	if err != nil {
		return JoinCodeRedeemed{}, err
	}
	var r JoinCodeRedeemed
	r.ID, _ = st.Int("ID")
	r.Kind, _ = st.Int("KIND")
	var ok bool
	if r.Reason, ok = st.Int("RSN"); !ok {
		return r, fmt.Errorf("redeemJoinCode reply without RSN")
	}
	return r, nil
}

// joinTarget is what a code stands for: a party (JoinTargetParty, PGID) or a lobby game (JoinTargetGame, GID).
type joinTarget struct{ kind, id int64 }

// joinCode is one live code: its target and the members who issued it (it lives while one of them is in it).
type joinCode struct {
	code    string
	target  joinTarget
	holders []int64
}

// serverInvite is an invite the server sent in an inviter's name; the joiner's answer to it is dropped.
type serverInvite struct {
	joiner, inviter, number, answerType int64
	until                               time.Time
}

// joinCodes is the server's join-code state, in memory (a restart forgets every code).
type joinCodes struct {
	mu       sync.Mutex
	byCode   map[string]*joinCode
	byTarget map[joinTarget]*joinCode
	invites  []serverInvite
	next     int64 // the last invite number used (from JoinInviteFirst)
	newCode  func() string
}

func (j *joinCodes) initLocked() {
	if j.byCode == nil {
		j.byCode, j.byTarget = map[string]*joinCode{}, map[joinTarget]*joinCode{}
	}
	if j.newCode == nil {
		j.newCode = randomJoinCode
	}
}

// randomJoinCode returns 128 random bits, base64url without padding (22 characters).
func randomJoinCode() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// joinState is a target as a newcomer sees it now.
type joinState struct {
	kind        int64   // JoinKind*
	members     []int64 // who is in it (a lobby's players, spectators included)
	inviter     int64   // the party's leader, the lobby's owner (admin)
	inviterName string
	size, max   int64
	why         int64 // JoinOK, or why a newcomer can't join now
	final       bool  // why can't change while the target lives: no code
}

// joinStateOf looks the target up; ok is false when it doesn't exist (or ended).
func (s *Server) joinStateOf(t joinTarget) (st joinState, ok bool) {
	switch t.kind {
	case JoinTargetParty:
		s.groups.mu.Lock()
		defer s.groups.mu.Unlock()
		pg := s.groups.byID[t.id]
		if pg == nil || len(pg.members) == 0 {
			return st, false
		}
		st.kind, st.inviter, st.max, st.size = JoinKindParty, pg.info.Owner, pg.info.MemberLimit, int64(len(pg.members))
		for _, m := range pg.members {
			st.members = append(st.members, m.BlazeID)
			if m.BlazeID == pg.info.Owner {
				st.inviterName = m.Name
			}
		}
		switch {
		case pg.info.Key != "": // a game's or team's chat (GG-, TG-), not a party
			st.why, st.final = JoinUnsupported, true
		case pg.info.JoinControl == PlaygroupClosed:
			st.why = JoinClosed
		case st.max > 0 && st.size >= st.max:
			st.why = JoinFull
		}
		return st, true
	case JoinTargetGame:
		hg := s.hosted.get(t.id)
		if hg == nil {
			return st, false
		}
		hg.mu.Lock()
		defer hg.mu.Unlock()
		if hg.ended || hg.custom != nil && !hg.custom.ready {
			return st, false
		}
		var counts [3]int64
		for _, hp := range hg.players {
			if hp.removed {
				continue
			}
			st.members = append(st.members, hp.p.ID)
			if hp.p.Team >= 0 && hp.p.Team <= SpectatorTeam {
				counts[hp.p.Team]++
			}
		}
		if hg.custom == nil {
			st.why, st.final = JoinUnsupported, true // a practice or matchmade game: no lobby to join
			return st, true
		}
		if len(hg.custom.admins) > 0 {
			st.inviter = hg.custom.admins[0]
			if hp := hg.playerLocked(st.inviter); hp != nil {
				st.inviterName = hp.p.Name
			}
		}
		teamCap := hg.game.TeamCap
		if hg.custom.browsable {
			st.kind, st.size, st.max = JoinKindCustomGame, counts[0]+counts[1], 2*teamCap
		} else {
			st.kind, st.size, st.max = JoinKindPracticeGroup, counts[0], teamCap
		}
		switch {
		case attrValue(hg.game.Attrs, AttrPrivateMatch) == "1":
			st.why, st.final = JoinPrivate, true
		case hg.custom.started:
			st.why, st.final = JoinStarted, true
		case hg.custom.browsable && counts[0] >= teamCap && counts[1] >= teamCap, !hg.custom.browsable && counts[0] >= teamCap:
			st.why = JoinFull
		}
		return st, true
	}
	return st, false
}

// issue gives member a code for target: the target's live code (member added to its holders), or a new one when it
// has none or none of its holders is still in it. st is the target's state, already checked.
func (j *joinCodes) issue(target joinTarget, member int64, st joinState) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.initLocked()
	jc := j.byTarget[target]
	if jc != nil {
		jc.holders = slices.DeleteFunc(jc.holders, func(id int64) bool { return !slices.Contains(st.members, id) })
		if len(jc.holders) == 0 {
			delete(j.byCode, jc.code)
			jc = nil
		}
	}
	if jc == nil {
		jc = &joinCode{code: j.newCode(), target: target}
		for j.byCode[jc.code] != nil {
			jc.code = j.newCode()
		}
		j.byCode[jc.code], j.byTarget[target] = jc, jc
	}
	if !slices.Contains(jc.holders, member) {
		jc.holders = append(jc.holders, member)
	}
	return jc.code
}

// revoke forgets target's code (it ended, or can't be joined any more).
func (j *joinCodes) revoke(target joinTarget) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if jc := j.byTarget[target]; jc != nil {
		delete(j.byCode, jc.code)
		delete(j.byTarget, target)
	}
}

// lookup returns code's target and holders.
func (j *joinCodes) lookup(code string) (joinTarget, []int64, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	jc := j.byCode[code]
	if jc == nil {
		return joinTarget{}, nil, false
	}
	return jc.target, slices.Clone(jc.holders), true
}

// targets returns every target with a live code (for the sweep).
func (j *joinCodes) targets() []joinTarget {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]joinTarget, 0, len(j.byTarget))
	for t := range j.byTarget {
		out = append(out, t)
	}
	return out
}

// sweep forgets the codes whose target ended or whose holders all left it (a code is checked again at every use;
// the sweep only keeps the table small). It runs at every issue.
func (s *Server) sweepJoinCodes() {
	for _, t := range s.joins.targets() {
		st, ok := s.joinStateOf(t)
		_, holders, live := s.joins.lookupTarget(t)
		if !live {
			continue
		}
		if !ok || st.final || !slices.ContainsFunc(holders, func(id int64) bool { return slices.Contains(st.members, id) }) {
			s.joins.revoke(t)
		}
	}
}

// lookupTarget returns target's code and holders.
func (j *joinCodes) lookupTarget(t joinTarget) (string, []int64, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	jc := j.byTarget[t]
	if jc == nil {
		return "", nil, false
	}
	return jc.code, slices.Clone(jc.holders), true
}

// invite records an invite the server sends now and returns its number.
func (j *joinCodes) invite(joiner, inviter, answerType int64, now time.Time) int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.next < JoinInviteFirst {
		j.next = JoinInviteFirst - 1
	}
	j.next++
	j.invites = slices.DeleteFunc(j.invites, func(si serverInvite) bool { return now.After(si.until) })
	j.invites = append(j.invites, serverInvite{joiner: joiner, inviter: inviter, number: j.next, answerType: answerType,
		until: now.Add(joinInviteTTL)})
	return j.next
}

// takeAnswer reports whether msg from sender answers one of the server's invites (and forgets that invite): a user
// message of the invite's answer type to the inviter whose 0xB0005 is the invite's number.
func (j *joinCodes) takeAnswer(sender int64, msg ClientMessage, now time.Time) bool {
	if msg.TargetType != ObjTypeUser || len(msg.Targets) != 1 {
		return false
	}
	switch msg.Type {
	case MessageTypePvPInviteAnswer, MessageTypeCustomInviteAnswer, MessageTypePracticeInviteAnswer:
	default:
		return false
	}
	v, _ := msg.Attr(MsgAttrInviteID)
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < JoinInviteFirst {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for i, si := range j.invites {
		if si.joiner == sender && si.inviter == msg.Targets[0] && si.number == n && si.answerType == msg.Type &&
			!now.After(si.until) {
			j.invites = slices.Delete(j.invites, i, i+1)
			return true
		}
	}
	return false
}

// JoinInviteMessage is the invite the server sends a code's redeemer in the inviter's name, as the inviter's client
// would send it (GameEntryInviteManager_SendInvite 0x0067F100: FLAG 0, the user target, ATTR 0xB0000 the inviter's
// name, 0xB0003 the PGID (a party) or 0xB0004 the GID, 0xB0005 the invite's number, 0xB0006 "0": no private key).
func JoinInviteMessage(kind, id int64, inviterName string, number, joiner int64) ClientMessage {
	typ, idKey := int64(MessageTypePvPInvite), int64(MsgAttrPlaygroupID)
	switch kind {
	case JoinKindPracticeGroup:
		typ, idKey = MessageTypePracticeMatchInvite, MsgAttrGameID
	case JoinKindCustomGame:
		typ, idKey = MessageTypeCustomMatchInvite, MsgAttrGameID
	}
	return ClientMessage{Attrs: []MsgAttr{{MsgAttrSenderName, inviterName}, {idKey, strconv.FormatInt(id, 10)},
		{MsgAttrInviteID, strconv.FormatInt(number, 10)}, {MsgAttrInviteExtra, "0"}},
		Targets: []int64{joiner}, TargetType: ObjTypeUser, Type: typ}
}

// inviteAnswerType is the answer TYPE the client sends to an invite of kind (JoinKind*).
func inviteAnswerType(kind int64) int64 {
	switch kind {
	case JoinKindPracticeGroup:
		return MessageTypePracticeInviteAnswer
	case JoinKindCustomGame:
		return MessageTypeCustomInviteAnswer
	}
	return MessageTypePvPInviteAnswer
}

func joinKindName(kind int64) string {
	switch kind {
	case JoinKindParty:
		return "party"
	case JoinKindPracticeGroup:
		return "practice group"
	case JoinKindCustomGame:
		return "custom game"
	}
	return "target"
}

func registerJoinCodes(s *Server) {
	// issueJoinCode {ID, KIND}: a member of a party or lobby asks for its code (the launcher, for the player's chat-app
	// profile). Refused (RSN, no code) for a target that doesn't exist, a non-member, a private or started lobby.
	s.Register(ComponentJoinCodes, CmdJoinCodesIssue, "issueJoinCode", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			r.Server.Log.Log("blaze", "   issueJoinCode: not logged in")
			return JoinCodeIssueReply(JoinCodeInfo{Reason: JoinNotLoggedIn})
		}
		me := sess.Persona.ID
		kind, _ := r.Body.Int("KIND")
		id, _ := r.Body.Int("ID")
		t := joinTarget{kind, id}
		r.Server.sweepJoinCodes()
		if kind != JoinTargetParty && kind != JoinTargetGame {
			r.Server.Log.Logf("blaze", "   issueJoinCode by %d: kind %d unknown", me, kind)
			return JoinCodeIssueReply(JoinCodeInfo{Reason: JoinUnsupported})
		}
		st, ok := r.Server.joinStateOf(t)
		info := JoinCodeInfo{Kind: st.kind, Size: st.size, Max: st.max, Reason: st.why}
		switch {
		case !ok:
			info = JoinCodeInfo{Reason: JoinGone}
		case !slices.Contains(st.members, me):
			info = JoinCodeInfo{Kind: st.kind, Reason: JoinNotMember}
		case st.final:
			r.Server.joins.revoke(t)
		default:
			info.Code = r.Server.joins.issue(t, me, st)
			info.Joinable = st.why == JoinOK
		}
		r.Server.Log.Logf("blaze", "   issueJoinCode by %d for %s %d: code %v, %d of %d, %s", me, joinKindName(st.kind), id,
			info.Code != "", info.Size, info.Max, JoinReasonName(info.Reason))
		return JoinCodeIssueReply(info)
	})
	// redeemJoinCode {CODE}: a friend's launcher hands a code to its own connection; the server sends that connection's
	// user the invite in the leader's or owner's name (NotifyMessage, after the reply), and drops the answer later.
	s.Register(ComponentJoinCodes, CmdJoinCodesRedeem, "redeemJoinCode", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			r.Server.Log.Log("blaze", "   redeemJoinCode: not logged in")
			return JoinCodeRedeemReply(JoinCodeRedeemed{Reason: JoinNotLoggedIn})
		}
		me := sess.Persona.ID
		code, _ := r.Body.Str("CODE")
		out, note := r.Server.redeemJoinCode(me, code)
		r.Server.Log.Logf("blaze", "   redeemJoinCode by %d: %s %d: %s", me, joinKindName(out.Kind), out.ID, JoinReasonName(out.Reason))
		if note != nil {
			conns := r.Server.connsOf(me)
			var others []*Conn
			for _, c := range conns {
				if c == r.Conn {
					r.Notify(ComponentMessaging, NotifyMessageID, "NotifyMessage", note)
				} else {
					others = append(others, c)
				}
			}
			if len(others) > 0 {
				r.After(func() {
					for _, c := range others {
						c.Push(r.Server, ComponentMessaging, NotifyMessageID, "NotifyMessage", note)
					}
				})
			}
		}
		return JoinCodeRedeemReply(out)
	})
}

// redeemJoinCode checks code for joiner and, when it can be used, builds the invite (NotifyMessage's payload).
func (s *Server) redeemJoinCode(joiner int64, code string) (JoinCodeRedeemed, []byte) {
	t, holders, ok := s.joins.lookup(code)
	if !ok {
		return JoinCodeRedeemed{Reason: JoinGone}, nil
	}
	out := JoinCodeRedeemed{ID: t.id}
	st, ok := s.joinStateOf(t)
	out.Kind = st.kind
	if !ok || !slices.ContainsFunc(holders, func(id int64) bool { return slices.Contains(st.members, id) }) {
		s.joins.revoke(t)
		out.Reason = JoinGone
		return out, nil
	}
	switch {
	case slices.Contains(st.members, joiner):
		out.Reason = JoinAlreadyIn
	case st.final:
		s.joins.revoke(t)
		out.Reason = st.why
	case st.why != JoinOK:
		out.Reason = st.why
	case st.inviter == 0 || st.inviterName == "":
		out.Reason = JoinGone
	case s.blockedBy(joiner, st.inviter):
		out.Reason = JoinBlocked
	}
	if out.Reason != JoinOK {
		return out, nil
	}
	now := s.Now()
	m := &s.msgs
	m.mu.Lock()
	m.initLocked()
	allowed := m.allowLocked(joiner, now)
	m.mu.Unlock()
	if !allowed {
		out.Reason = JoinRateLimited
		return out, nil
	}
	number := s.joins.invite(joiner, st.inviter, inviteAnswerType(st.kind), now)
	msg := JoinInviteMessage(st.kind, t.id, st.inviterName, number, joiner)
	id := m.newID()
	s.Log.Logf("blaze", "   message %d: the server's %s %#x to %d in the name of %d %q (invite %d, %s %d)", id,
		messageTypeName(msg.Type), msg.Type, joiner, st.inviter, st.inviterName, number, joinKindName(st.kind), t.id)
	return out, MessageNotification(ServerMessage{ID: id, Name: st.inviterName, Payload: msg, Source: st.inviter,
		Time: now.Unix()})
}
