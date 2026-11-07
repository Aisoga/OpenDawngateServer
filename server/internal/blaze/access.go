package blaze

import (
	"slices"
	"strconv"
	"time"

	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// Who may ask what. The code is public, so nothing here assumes a client behaves as the 2014 one does: a request is
// looked at for what the connection has earned, not for what the game's own UI would have sent.

// ErrAuthenticationRequired is ERR_AUTHENTICATION_REQUIRED (Blaze_GetFrameworkErrorName 0x00C7A160), the Fire header's
// error field for a request that needs a login.
const ErrAuthenticationRequired uint16 = 0x4004

// noLoginNeeded are the requests a connection without a login may make. The first three lines are what logging in takes.
// The rest is what a real client was seen to send while it has no session, around a logout or a kick (every session log
// up to 2026-11-06): they touch only the connection itself or give nothing away. Everything else is refused with
// ErrAuthenticationRequired, before it costs any store work: an answer of any kind ends the client's request (see
// sendUnhandled), so a real client isn't left waiting.
var noLoginNeeded = map[key]bool{
	{ComponentUtil, CmdUtilPreAuth}: true, {ComponentUtil, CmdUtilPing}: true, {ComponentUtil, CmdUtilPostAuth}: true,
	{ComponentUtil, CmdUtilGetTelemetryServer}: true,
	{ComponentAuthentication, CmdAuthLogin}:    true, {ComponentAuthentication, CmdAuthLoginPersona}: true, {ComponentAuthentication, CmdAuthLogout}: true,

	{ComponentBGEcommerce, CmdBGEGetCommerceProfile}: true, {ComponentBGEcommerce, CmdBGEGetBalance}: true,
	{ComponentRooms, CmdRoomsSelectViewUpdate}: true, {ComponentRooms, CmdRoomsSelectCategoryUpdates}: true,
	{ComponentUserSessions, CmdUserSessionsUpdateNetworkInfo}: true, {ComponentUserSessions, CmdUserSessionsUpdateExtendedDataAttribute}: true,
	{ComponentUserSessions, CmdUserSessionsSetUserInfoAttribute}: true,
	{ComponentPlaygroups, CmdPlaygroupsLeavePlaygroup}:           true,
	{ComponentGameManager, CmdGameManagerUpdateMeshConnection}:   true,

	// the front asks these itself; they answer "not logged in" in their own terms (Start-Client's Join waits on it)
	{ComponentJoinCodes, CmdJoinCodesIssue}: true, {ComponentJoinCodes, CmdJoinCodesRedeem}: true,
}

// The bounds on what a request may leave behind. The game's own values are far below them: its settings are a few keys per
// shaper, and it holds one party and a match's chats.
const (
	maxSettingKeys      = 512
	maxSettingKeyBytes  = 128
	maxSettingValue     = 4 << 10
	maxSettingsBytes    = 48 << 10 // getUserSettings must fit one frame (64 KiB), or the account's own client hears nothing
	maxBasicDataIDs     = 512
	maxPlaygroupsPerUse = 16
	maxGameListsPerConn = 8
	maxAttrEntries      = 128
	maxAttrValueBytes   = 4 << 10
)

// settingsFit says whether an account's settings with these changes stay within the bounds, and if not why.
func settingsFit(have, set map[string]string) (bool, string) {
	total, keys := 0, len(have)
	for k, v := range have {
		if _, replaced := set[k]; !replaced {
			total += len(k) + len(v)
		} else {
			keys--
		}
	}
	for k, v := range set {
		if len(k) > maxSettingKeyBytes || len(v) > maxSettingValue {
			return false, "a key or value over its limit"
		}
		total += len(k) + len(v)
		keys++
	}
	switch {
	case keys > maxSettingKeys:
		return false, strconv.Itoa(keys) + " keys"
	case total > maxSettingsBytes:
		return false, strconv.Itoa(total) + " bytes"
	}
	return true, ""
}

// The invite messages this server relays carry what a later join needs. The Blaze side never saw an invite as more than a
// message before, so anyone could join a party or a practice group by its id, and a private game's password was only the
// owner's client's business: a modified client just didn't kick.

// msgAttr is a message's attribute (false: it has none).
func msgAttr(msg ClientMessage, key int64) (string, bool) {
	for _, a := range msg.Attrs {
		if int64(a.Key) == key {
			return a.Value, true
		}
	}
	return "", false
}

// MessageTypePrivateAuthAnswer is the owner's answer to a private game's password (user message 0x6EBDC8E6: 0xB0000 the game,
// 0xB0001 "1" for the right password, 0xB0002 the auth token the joiner joins with; b15l2_pw1d).
const MessageTypePrivateAuthAnswer = 0x6EBDC8E6

// noteInvite records what a relayed message allows.
func (s *Server) noteInvite(sender store.Persona, msg ClientMessage) {
	switch msg.Type {
	case MessageTypePvPInvite:
		if v, ok := msgAttr(msg, MsgAttrPlaygroupID); ok {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				s.groups.invite(id, sender.ID, msg.Targets)
			}
		}
	case MessageTypePracticeMatchInvite, MessageTypeCustomMatchInvite:
		v, ok := msgAttr(msg, MsgAttrGameID)
		if !ok {
			return
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return
		}
		key, _ := msgAttr(msg, MsgAttrInviteExtra)
		s.noteGameInvite(id, sender.ID, msg.Targets, key)
	case MessageTypePrivateAuthAnswer:
		game, ok1 := msgAttr(msg, 0xB0000)
		right, ok2 := msgAttr(msg, 0xB0001)
		token, ok3 := msgAttr(msg, 0xB0002)
		if !ok1 || !ok2 || !ok3 || right != "1" || token == "" {
			return
		}
		if id, err := strconv.ParseInt(game, 10, 64); err == nil {
			s.noteGameInvite(id, sender.ID, nil, token)
		}
	}
}

// noteGameInvite records a member's invite to a game of its, and from an admin the private game's token.
func (s *Server) noteGameInvite(gid, sender int64, targets []int64, token string) {
	hg := s.hosted.get(gid)
	if hg == nil {
		return
	}
	hg.mu.Lock()
	defer hg.mu.Unlock()
	if hg.custom == nil {
		return
	}
	hp := hg.playerLocked(sender)
	if hp == nil || hp.removed {
		return
	}
	if len(targets) > 0 {
		hg.custom.invite(targets...)
	}
	if token != "" && token != "0" && hg.isAdminLocked(sender) {
		hg.custom.token = token
	}
}

// invite records players a game's member invited.
func (l *customLobby) invite(ids ...int64) {
	if l.invited == nil {
		l.invited = map[int64]bool{}
	}
	for _, id := range ids {
		l.invited[id] = true
	}
}

// joinRefusal says why a player may not take a new seat in a custom game now ("": it may), with the error to answer. tokens
// are the joiner's player attributes.
func (l *customLobby) joinRefusal(me int64, private bool, token string) (uint16, string) {
	switch {
	case l.kicked[me]:
		return ErrPermissionDenied, "its admin removed this player"
	case !l.browsable && !l.invited[me]:
		return ErrPermissionDenied, "a practice group takes only the players its members invite"
	case private && !l.invited[me] && (l.token == "" || token != l.token):
		return ErrPermissionDenied, "a private game: no valid auth token from its owner"
	}
	return 0, ""
}

// invite records players a party's member invited: joining a party takes an invite (a member's relayed message, or a join
// code's), since parties are found by counting ids.
func (p *playgroups) invite(id, sender int64, targets []int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg := p.byID[id]
	if pg == nil || pg.indexOf(sender) < 0 {
		return
	}
	if pg.invited == nil {
		pg.invited = map[int64]bool{}
	}
	for _, t := range targets {
		pg.invited[t] = true
	}
}

// inviteByServer is a join code's invite: its holder was checked by the code.
func (p *playgroups) inviteByServer(id, target int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pg := p.byID[id]; pg != nil {
		if pg.invited == nil {
			pg.invited = map[int64]bool{}
		}
		pg.invited[target] = true
	}
}

// groupsOfLocked counts the playgroups a persona is in.
func (p *playgroups) groupsOfLocked(blazeID int64) int {
	n := 0
	for _, pg := range p.byID {
		if pg.indexOf(blazeID) >= 0 {
			n++
		}
	}
	return n
}

// LobbyMaxAge is how long a custom game may wait for its start: it holds a match host and a port, of which a server has few,
// so a lobby nobody starts ends. A browser's page of waiting players has long gone by then.
const LobbyMaxAge = time.Hour

// expireLobby ends a custom game that hasn't started by LobbyMaxAge.
func (hg *hostedGame) expireLobby() {
	hg.mu.Lock()
	over := hg.ended || hg.custom == nil || hg.custom.started
	hg.mu.Unlock()
	if !over {
		hg.endLobby("it waited " + LobbyMaxAge.String() + " for its start")
	}
}

// practiceFlags are a game's GameFlags as the server takes them. The flags decide the rewards (progress.ECAward), and a
// client chooses them in its create request: a practice game must not pass for a matchmade or ranked one. Only the
// matchmaker makes those, on its own path.
func practiceFlags(g *Game) {
	f := g.descriptors()
	want := f &^ uint32(progress.DescMatchmade|progress.DescRanked)
	if want&progress.DescCustom == 0 && want&(progress.DescSoloPractice|progress.DescGroupPractice) == 0 {
		want |= progress.DescSoloPractice
	}
	if want == f {
		return
	}
	for i, a := range g.Attrs {
		if k, _ := a.Key.(string); k == "GameFlags" {
			g.Attrs[i].Value = strconv.FormatUint(uint64(want), 10)
			return
		}
	}
}

// capAttrs keeps a set of attributes within its bounds: values over the limit are refused one by one, and so are new keys
// once the set is full. The game's own sets have a few dozen short entries.
func capAttrs(attrs []tdf.Pair) []tdf.Pair {
	if len(attrs) <= maxAttrEntries && !slices.ContainsFunc(attrs, func(p tdf.Pair) bool {
		v, _ := p.Value.(string)
		return len(v) > maxAttrValueBytes
	}) {
		return attrs
	}
	out := make([]tdf.Pair, 0, min(len(attrs), maxAttrEntries))
	for _, p := range attrs {
		if v, _ := p.Value.(string); len(v) > maxAttrValueBytes {
			continue
		}
		if len(out) == maxAttrEntries {
			break
		}
		out = append(out, p)
	}
	return out
}
