package blaze

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// Messaging component (0x0F), BlazeSDK's standard one: user-to-user and group messages (B10c; docs/BLAZE.md
// *Messaging relay (B10c)*, read statically from Dawngate.exe 854). The client's MessagingAPI (constructor
// 0x00CAAC00) sends a Blaze::Messaging::ClientMessage (table 0x012EE228) with sendMessage (0x00CAB370 ->
// 0x00CAA5B0, job 0x012EE8AC: SendMessageResponse) and gets every message as NotifyMessage
// (Blaze::Messaging::ServerMessage 0x012EE2D0; dispatcher: the component's vtable slot 5 0x00CAA150 ->
// 0x00CAA840). 0x00CAA840 hands the message to the callbacks registered for PYLD.TTYP (UserMessageCb for
// users, PlaygroupMessageCb for playgroups, GameMessageCb for games; the playgroup dispatcher 0x00AAC440
// looks the object up by PYLD.TIDS and passes null when the client doesn't know it, the game one
// presumably alike) and then to every GlobalMessageCb. The game uses it for:
//
//   - invites (GameEntryInviteManager.cpp): PvP party, custom and practice match invites and their
//     answers, user to user (OnUserMessage 0x0067FB30);
//   - chat (snblaze_rooms.cpp cBlazeRoomsManager, snlobby_messaging.cpp cLobbyMessaging): TYPE 2 to a
//     playgroup (the game's GG-/TG- chats, the PvP party) with FLAG echo|filter, whispers (TYPE 3) and
//     instant messages to users.
//
// The server relays: users by BlazeId (persona id) to their connections, playgroups to their members,
// games to the members of the game's chat playgroup "GG-<id>" (our choice: every client in a game joins
// it, B11); persistent messages go into the target users' inboxes for fetchMessages. Ids and constants
// below come from docs/data/blaze_components.txt and the client's code; where a rule is ours it says so.
const (
	ComponentMessaging    = 0x0F
	CmdMsgSendMessage     = 0x01
	CmdMsgFetchMessages   = 0x02
	CmdMsgPurgeMessages   = 0x03
	NotifyMessageID       = 0x01
	CmdUtilFilterForUText = 0x14 // Util::filterForProfanity
)

// ClientMessage FLAG bits. MessageFlagFilter is certain: sendMessage (0x00CAB370) sends a message with bit
// 0x4 through Util::filterForProfanity first (0x00CAB1E0 -> 0x00CC59A0; the callback 0x00CAB0D0 puts the
// filtered texts back and sends). The other two are our reading [inferred]: 0x1 is set on whispers (FLAG 7)
// and on the persistent user messages TYPE 8-11 (FLAG 5) that the client later purges by MGID, and the
// login's fetchMessages/purgeMessages pair only makes sense for stored messages; 0x2 is set on every chat
// message, whose senders never show their own line (0x00AB09F0), while cLobbyMessaging (0x00ACC4B0)
// records a message whose SRCE is the local user and whose TIDS don't name it: the server's echo.
const (
	MessageFlagPersistent = 0x1
	MessageFlagEcho       = 0x2
	MessageFlagFilter     = 0x4
)

// FetchMessageRequest/PurgeMessageRequest FLAG bits: which fields must match. From the client's purge
// senders: 0x00AAAE60 sets 0x4 with TYPE 3 (the login's "purge whispers"), 0x00AAAF50 and 0x00ABB840 set
// 0x1 (by MGID), 0x00ABB940 sets 0x2 (by SRCE). FLAG 0 matches everything (the login's fetch).
const (
	MessageMatchID     = 0x1
	MessageMatchSource = 0x2
	MessageMatchType   = 0x4
)

// Messaging errors (docs/data/blaze_components.txt, code << 16 | 0x0F).
const (
	ErrMessagingUnknown           = 0x0001 // MESSAGING_ERR_UNKNOWN (our rate limit)
	ErrMessagingMaxAttrExceeded   = 0x0002 // MESSAGING_ERR_MAX_ATTR_EXCEEDED
	ErrMessagingTargetNotFound    = 0x0004 // MESSAGING_ERR_TARGET_NOT_FOUND
	ErrMessagingTargetTypeInvalid = 0x0005 // MESSAGING_ERR_TARGET_TYPE_INVALID
	ErrMessagingInvalidParam      = 0x0009 // MESSAGING_ERR_INVALID_PARAM
)

// Message types (ClientMessage TYPE) the client sends and reads. Chat types from cLobbyMessaging's names
// (0x00ACC4B0: 0 "InstantMessage", 2 "Chat", 3 "Whisper") and the assert "msgType ==
// kMessageTypeChatMessage" (0x00AAD000); the invite types from GameEntryInviteManager (0x0067F100 sends,
// 0x0067EE10 answers, 0x0067EAC0 asks, 0x0067FB30 receives).
const (
	MessageTypeInstant              = 0
	MessageTypeChat                 = 2
	MessageTypeWhisper              = 3
	MessageTypePvPInvite            = 0x176A0D2B // invite kind 2: join my PvP party (ATTR 0xB0003)
	MessageTypeCustomMatchInvite    = 0x8FBFA37C // kind 4 (ATTR 0xB0004 = the game)
	MessageTypePracticeMatchInvite  = 0x237748AA // kind 8 (ATTR 0xB0004)
	MessageTypePvPInviteAnswer      = 0xA501C11E
	MessageTypeCustomInviteAnswer   = 0x55722FA1
	MessageTypePracticeInviteAnswer = 0xEBBABA1B
	MessageTypeInviteQuery          = 0x0ED4789F // "which invites do you take?", no ATTR
	MessageTypeInviteQueryAnswer    = 0xE4407198 // ATTR 0xB0001 = the kinds taken ("%u", bits 2/4/8)
)

// The friend messages (B13b; snlobby_friends.cpp's cBlazeFriends, read statically): user messages with FLAG 5
// (persistent | filter) and ATTR {0xFF02 "", 0xB0003 "6", 0xB0009 ""} (the text is the chat UI's empty
// string .data 0x011BA850; "6" is "%d" of 6). The receiver's callback (0x00AC07B0: only when TIDS names the
// local user and SRCE isn't on its block list) handles them by TYPE and purges each by MGID afterwards
// (0x00ABB840). Friendship itself is the association list (associationlists.go): accepting sends
// addUsersToList for the friends list before the accept message, unfriending removeUsersFromList before
// MessageTypeFriendRemoved.
const (
	// MessageTypeFriendRequest is "add me as a friend" (MaxisChat.AddFriendByName/AddFriendByID -> 0x00ABF790 ->
	// 0x00ABE0F0). The receiver (0x00ABFE30) drops it from a friend or a user it already has an invite from, refuses
	// it at once (MessageTypeFriendDeclined) while its setting at cBlazeFriends +0x9D is off (the constructor
	// 0x00ABCF60 gets 1 from 0x00AD6B2D), else keeps the invite (MGID, BlazeId, name) and shows it (event
	// 0xDBCF3BF5).
	MessageTypeFriendRequest = 8
	// MessageTypeFriendAccepted answers it (MaxisChat.AcceptFriendInvite -> 0x00ABFBC0 -> 0x00ABE7C0, after
	// addUsersToList for the friends list with the inviter); the inviter shows it (0x00AC0030, event 0x7142279F).
	MessageTypeFriendAccepted = 9
	// MessageTypeFriendDeclined refuses it (MaxisChat.RefuseFriendInvite -> 0x00ABFA70 -> 0x00ABE430).
	MessageTypeFriendDeclined = 10
	// MessageTypeFriendRemoved tells the former friend (MaxisChat.UnfriendByID/ByName -> 0x00ABEB80, after
	// removeUsersFromList for the friends list); the receiver only notes it (0x00AC0530, event 0x9159C6E3).
	MessageTypeFriendRemoved = 11
)

// ClientMessage ATTR keys (map<int,string>). 0xFF02 is the chat text, the only key at or below 0xFFFF,
// so the only one the profanity filter sees (0x00CAB1E0 collects keys <= 0xFFFF); 0xB0009 is the same
// text, unfiltered. The invite keys: 0xB0000 the sender's persona name (must not be empty), 0xB0002 the
// answer ("T" accepted, "F" declined, "I" not taken; .data 0x013F3A3C/40/44), 0xB0003 the PvP party's
// playgroup id ("%llu"), 0xB0004 the game id, 0xB0005 the invite's number ("%u", the answer repeats it),
// 0xB0006 an extra value ("%u").
const (
	MsgAttrText           = 0xFF02
	MsgAttrTextUnfiltered = 0xB0009
	MsgAttrSenderName     = 0xB0000
	MsgAttrInviteAnswer   = 0xB0002
	MsgAttrPlaygroupID    = 0xB0003
	MsgAttrGameID         = 0xB0004
	MsgAttrInviteID       = 0xB0005
	MsgAttrInviteExtra    = 0xB0006
)

// Target object types (TTYP): the client's ClientMessage target setters write them (0x00CAAE00: the user
// type, .data 0x0146221C = 0x7802/1; 0x00CAAD70: the object's own id, e.g. a Playgroup's (6, 1, PGID),
// 0x00C8FF10).
var (
	ObjTypeUser      = tdf.ObjType{Component: ComponentUserSessions, Type: 1}
	ObjTypeGame      = tdf.ObjType{Component: ComponentGameManager, Type: 1}
	ObjTypePlaygroup = tdf.ObjType{Component: ComponentPlaygroups, Type: 1}
)

// FilterResult (FilteredUserText.DIRT; name table .rdata 0x012FCD70): FILTER_RESULT_PASSED 0, OFFENSIVE 1,
// UNPROCESSED 2, STRING_TOO_LONG 3, OTHER 4.
const FilterResultPassed = 0

// MsgAttr is one ClientMessage attribute.
type MsgAttr struct {
	Key   int64
	Value string
}

// ClientMessage is Blaze::Messaging::ClientMessage (table 0x012EE228): ATTR map<int,string>, FLAG bitfield,
// STAT uint32, TAG uint32, TIDS list<int> (the targets' ids), TTYP objtype (the targets' type), TYPE uint32.
// It is sendMessage's request and NotifyMessage's PYLD, which the server relays unchanged (attributes in
// key order, as the client's map holds them): the receivers read TYPE, TTYP/TIDS (the dispatch, and
// whether a message names the local user) and ATTR.
type ClientMessage struct {
	Attrs      []MsgAttr
	Flags      int64
	Status     int64
	Tag        int64
	Targets    []int64
	TargetType tdf.ObjType
	Type       int64
}

// Attr returns attribute key's value.
func (m ClientMessage) Attr(key int64) (string, bool) {
	for _, a := range m.Attrs {
		if a.Key == key {
			return a.Value, true
		}
	}
	return "", false
}

func writeClientMessage(w *tdf.Writer, m ClientMessage) {
	attrs := make([]tdf.Pair, len(m.Attrs))
	for i, a := range m.Attrs {
		attrs[i] = tdf.KV(a.Key, a.Value)
	}
	targets := m.Targets
	if targets == nil {
		targets = []int64{}
	}
	w.Map("ATTR", tdf.TypeInt, tdf.TypeString, attrs...)
	w.Int("FLAG", m.Flags)
	w.Int("STAT", m.Status)
	w.Int("TAG", m.Tag)
	w.List("TIDS", tdf.TypeInt, targets)
	w.Value("TTYP", tdf.TypeObjType, m.TargetType)
	w.Int("TYPE", m.Type)
}

// ClientMessageFromRequest reads a sendMessage request. Attributes without an integer key or a string
// value are dropped; ok is false without a TTYP.
func ClientMessageFromRequest(s *tdf.Struct) (m ClientMessage, ok bool) {
	if f, found := s.Get("ATTR"); found {
		if mp, isMap := f.Value.(*tdf.Map); isMap {
			for _, e := range mp.Entries {
				k, kok := e.Key.(int64)
				v, vok := e.Value.(string)
				if kok && vok {
					m.Attrs = append(m.Attrs, MsgAttr{k, v})
				}
			}
		}
	}
	slices.SortStableFunc(m.Attrs, func(a, b MsgAttr) int { return cmp.Compare(a.Key, b.Key) })
	m.Flags, _ = s.Int("FLAG")
	m.Status, _ = s.Int("STAT")
	m.Tag, _ = s.Int("TAG")
	m.Targets = intList(s, "TIDS")
	m.Type, _ = s.Int("TYPE")
	f, found := s.Get("TTYP")
	if !found {
		return m, false
	}
	m.TargetType, ok = f.Value.(tdf.ObjType)
	return m, ok
}

// ServerMessage is Blaze::Messaging::ServerMessage (table 0x012EE2D0), NotifyMessage's payload.
type ServerMessage struct {
	Flags   int64         // FLAG: 0 [assumed: no reader found]
	ID      int64         // MGID uint64: the message's id (purgeMessages by MGID removes it)
	Name    string        // NAME: the sender's persona name; the chats show it (0x00AAD000, 0x00ACC4B0)
	Payload ClientMessage // PYLD
	Source  int64         // SRCE objid (0x7802, 1, BlazeId): the sender; receivers answer it and check
	// their block list with it (hub vtable +0x138). ObjectId keeps the id at +0 (decoder 0x00C6FC90), so
	// the receivers' ServerMessage +0x10 is this id.
	Time int64 // TIME uint32, seconds
}

// MessageNotification is Messaging::NotifyMessage (notification 1; the dispatcher 0x00CAA150 decodes it
// with the ServerMessage constructor 0x00CAA020).
func MessageNotification(m ServerMessage) []byte {
	w := tdf.NewWriter()
	w.Int("FLAG", m.Flags)
	w.Int("MGID", m.ID)
	w.String("NAME", m.Name)
	w.Struct("PYLD", func(w *tdf.Writer) { writeClientMessage(w, m.Payload) })
	w.Value("SRCE", tdf.TypeObjID, tdf.ObjID{Component: ObjTypeUser.Component, Type: ObjTypeUser.Type, ID: m.Source})
	w.Int("TIME", m.Time)
	return w.Bytes()
}

// SendMessageReply is Messaging::sendMessage's reply, Blaze::Messaging::SendMessageResponse (table
// 0x012EE3E8: MGID uint64, MIDS list<int>; job 0x012EE8AC). No caller reads it (the invite senders pass
// no callback, the chats' callbacks 0x00AAA5F0/0x00AC9C40 and GameEntryManager's 0x00687B20/0x006817B0
// look at the error only). MIDS = the ids of the copies kept for fetchMessages [assumed meaning].
func SendMessageReply(id int64, stored []int64) []byte {
	if stored == nil {
		stored = []int64{}
	}
	return tdf.NewWriter().Int("MGID", id).List("MIDS", tdf.TypeInt, stored).Bytes()
}

// FilterUserTextReply is Util::filterForProfanity's reply, Blaze::Util::FilterUserTextResponse (table
// 0x012FCD98: TLST list of FilteredUserText 0x014632F8 {DIRT enum FilterResult, UTXT string}), one entry
// per request text in order. The client (0x00CAB0D0) replaces a message attribute with UTXT where DIRT
// isn't PASSED and then sends the message. We filter nothing: every text PASSED, unchanged.
func FilterUserTextReply(texts []string) []byte {
	items := make([]*tdf.Writer, len(texts))
	for i, t := range texts {
		items[i] = tdf.NewWriter().Int("DIRT", FilterResultPassed).String("UTXT", t)
	}
	return tdf.NewWriter().List("TLST", tdf.TypeStruct, items).Bytes()
}

// MessageCountReply is the reply of Messaging::fetchMessages -> Blaze::Messaging::FetchMessageResponse
// (member table 0x012EE408; RPC job 0x012EE86C at 0x00CAA472) and of purgeMessages ->
// PurgeMessageResponse (0x012EE420; job 0x012EE88C at 0x00CAA542). Both are one uint32 MCNT, the number
// of messages; fetched messages arrive as NotifyMessage notifications (after the reply; the client's
// fetch callback, cBlazeRoomsManager 0x00AAD520, is empty).
func MessageCountReply(n int) []byte {
	return tdf.NewWriter().Int("MCNT", int64(n)).Bytes()
}

// MessagingLimits are the relay's limits (our choice; the zero value means DefaultMessagingLimits).
type MessagingLimits struct {
	MaxTargets   int           // TIDS per message (more: MESSAGING_ERR_INVALID_PARAM)
	MaxAttrs     int           // ATTR entries (more: MESSAGING_ERR_MAX_ATTR_EXCEEDED)
	MaxAttrBytes int           // bytes per attribute value (more: MESSAGING_ERR_INVALID_PARAM)
	InboxSize    int           // stored messages per user; the oldest goes when full
	Burst        int           // messages a user may send at once ...
	Every        time.Duration // ... and one more per this (over the limit: MESSAGING_ERR_UNKNOWN)
}

// DefaultMessagingLimits: a 5-player party's invites and a quick chat exchange fit; a chat line is far
// below 1 KiB.
func DefaultMessagingLimits() MessagingLimits {
	return MessagingLimits{MaxTargets: 32, MaxAttrs: 16, MaxAttrBytes: 1024, InboxSize: 100, Burst: 10,
		Every: 500 * time.Millisecond}
}

// messaging is the relay's state: who is online (BlazeId -> connections, from loginPersona), the users'
// inboxes of persistent messages (memory only: they don't survive a restart), the senders' rate buckets.
type messaging struct {
	mu      sync.Mutex
	limits  MessagingLimits
	nextID  int64
	online  map[int64][]*Conn
	inbox   map[int64][]ServerMessage
	buckets map[int64]*msgBucket
}

type msgBucket struct {
	tokens float64
	last   time.Time
}

func (m *messaging) initLocked() {
	if m.online == nil {
		m.online, m.inbox, m.buckets = map[int64][]*Conn{}, map[int64][]ServerMessage{}, map[int64]*msgBucket{}
	}
	if m.limits == (MessagingLimits{}) {
		m.limits = DefaultMessagingLimits()
	}
}

// messagingOnline makes conn reachable as persona p (called by loginPersona) until its session ends (a
// logout or the connection's close).
func (s *Server) messagingOnline(conn *Conn, p store.Persona) {
	m := &s.msgs
	m.mu.Lock()
	m.initLocked()
	if slices.Contains(m.online[p.ID], conn) {
		m.mu.Unlock()
		return
	}
	m.online[p.ID] = append(m.online[p.ID], conn)
	m.mu.Unlock()
	conn.OnSessionEnd(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		cs := slices.DeleteFunc(m.online[p.ID], func(c *Conn) bool { return c == conn })
		if len(cs) == 0 {
			delete(m.online, p.ID)
		} else {
			m.online[p.ID] = cs
		}
	})
}

// allowLocked takes a token from blazeID's bucket (m.mu held).
func (m *messaging) allowLocked(blazeID int64, now time.Time) bool {
	b := m.buckets[blazeID]
	if b == nil {
		b = &msgBucket{tokens: float64(m.limits.Burst), last: now}
		m.buckets[blazeID] = b
	}
	if m.limits.Every > 0 && now.After(b.last) {
		b.tokens = min(float64(m.limits.Burst), b.tokens+float64(now.Sub(b.last))/float64(m.limits.Every))
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// relayResult is what sendMessage did.
type relayResult struct {
	note      ServerMessage
	conns     []*Conn // connections that get NotifyMessage (each once)
	delivered []int64 // users whose connections get it
	stored    []int64 // users whose inboxes keep it
	blocked   []int64 // users who don't get it because they blocked the sender (B13b)
	err       uint16
	why       string
}

// membersOf returns the members of playgroup id, or of the playgroup with key when id is 0.
func (p *playgroups) membersOf(id int64, key string) ([]pgMember, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg := p.byID[id]
	if id == 0 {
		pg = p.byKey[key]
	}
	if pg == nil {
		return nil, false
	}
	return append([]pgMember{}, pg.members...), true
}

// relay checks msg from sender and works out who gets it. Our rules (BlazeSDK's server isn't in the
// client): users get it on every connection they're logged in on, and a persistent one also in their
// inbox, online or not (the client purges what it handled: whispers at login, the others by MGID); an
// unknown user is skipped (none known: MESSAGING_ERR_TARGET_NOT_FOUND); a playgroup or game reaches its
// members, and only a member may send to it; the sender gets its own copy only with MessageFlagEcho (or as
// a target). Rooms and other target types: MESSAGING_ERR_TARGET_TYPE_INVALID.
func (s *Server) relay(sender store.Persona, msg ClientMessage) relayResult {
	m := &s.msgs
	now := s.Now()
	m.mu.Lock()
	m.initLocked()
	lim := m.limits
	allowed := m.allowLocked(sender.ID, now)
	m.mu.Unlock()
	switch {
	case !allowed:
		return relayResult{err: ErrMessagingUnknown, why: "rate limit"}
	case msg.TargetType != ObjTypeUser && msg.TargetType != ObjTypePlaygroup && msg.TargetType != ObjTypeGame:
		return relayResult{err: ErrMessagingTargetTypeInvalid, why: fmt.Sprintf("target type %v", msg.TargetType)}
	case len(msg.Targets) == 0 || len(msg.Targets) > lim.MaxTargets:
		return relayResult{err: ErrMessagingInvalidParam, why: fmt.Sprintf("%d targets", len(msg.Targets))}
	case len(msg.Attrs) > lim.MaxAttrs:
		return relayResult{err: ErrMessagingMaxAttrExceeded, why: fmt.Sprintf("%d attributes", len(msg.Attrs))}
	}
	for _, a := range msg.Attrs {
		if len(a.Value) > lim.MaxAttrBytes {
			return relayResult{err: ErrMessagingInvalidParam, why: fmt.Sprintf("attribute %#x: %d bytes", a.Key, len(a.Value))}
		}
	}

	var users []int64 // the recipients' BlazeIds
	switch msg.TargetType {
	case ObjTypeUser:
		for _, id := range msg.Targets {
			if _, _, ok := s.Store.PersonaByID(id); ok && !slices.Contains(users, id) {
				users = append(users, id)
			}
		}
		if len(users) == 0 {
			return relayResult{err: ErrMessagingTargetNotFound, why: fmt.Sprintf("no user %v", msg.Targets)}
		}
	default:
		for _, id := range msg.Targets {
			members, ok := s.groups.membersOf(id, "")
			if msg.TargetType == ObjTypeGame { // the game's chat playgroup (B11) stands for its players
				members, ok = s.groups.membersOf(0, fmt.Sprintf("GG-%d", id))
			}
			if !ok || !slices.ContainsFunc(members, func(pm pgMember) bool { return pm.BlazeID == sender.ID }) {
				return relayResult{err: ErrMessagingTargetNotFound, why: fmt.Sprintf("%v %d: not a member", msg.TargetType, id)}
			}
			for _, pm := range members {
				if (pm.BlazeID != sender.ID || msg.Flags&MessageFlagEcho != 0) && !slices.Contains(users, pm.BlazeID) {
					users = append(users, pm.BlazeID)
				}
			}
		}
	}
	// B13b: a user who blocked the sender gets none of its messages, nor a stored copy. The client drops them too
	// (the receivers' block checks, e.g. the friend messages' 0x00AC07B0 and SporeOnlinePlatform_IsUserBlocked
	// 0x00AD4BF0), but not before its block list has arrived: until then the check asserts "Race condition :
	// Checking block list before its loaded!" (snlobby_friends.cpp 205) and answers "not blocked". The sender
	// isn't told (its reply is the same).
	var blocked []int64
	users = slices.DeleteFunc(users, func(id int64) bool {
		if id != sender.ID && s.blockedBy(id, sender.ID) {
			blocked = append(blocked, id)
			return true
		}
		return false
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	res := relayResult{note: ServerMessage{ID: m.nextID, Name: sender.Name, Payload: msg, Source: sender.ID, Time: now.Unix()},
		blocked: blocked}
	recipients := users
	if msg.TargetType == ObjTypeUser && msg.Flags&MessageFlagEcho != 0 && !slices.Contains(users, sender.ID) {
		recipients = append(slices.Clone(users), sender.ID)
	}
	for _, id := range recipients {
		cs := m.online[id]
		if len(cs) > 0 {
			res.delivered = append(res.delivered, id)
		}
		for _, c := range cs {
			if !slices.Contains(res.conns, c) {
				res.conns = append(res.conns, c)
			}
		}
	}
	if msg.TargetType == ObjTypeUser && msg.Flags&MessageFlagPersistent != 0 {
		for _, id := range users {
			box := append(m.inbox[id], res.note)
			if over := len(box) - lim.InboxSize; over > 0 {
				box = slices.Delete(box, 0, over)
			}
			m.inbox[id] = box
			res.stored = append(res.stored, id)
		}
	}
	return res
}

// inboxMatch reports whether a stored message fits a fetch or purge request (FLAG = which fields must
// match; STAT/SMSK, TARG, TYPL, SORT and the paging fields are ignored [not seen used]).
func inboxMatch(req *tdf.Struct, sm ServerMessage) bool {
	flags, _ := req.Int("FLAG")
	if flags&MessageMatchID != 0 {
		if id, _ := req.Int("MGID"); id != sm.ID {
			return false
		}
	}
	if flags&MessageMatchSource != 0 {
		if f, ok := req.Get("SRCE"); !ok || f.Value != (tdf.ObjID{Component: ObjTypeUser.Component, Type: ObjTypeUser.Type, ID: sm.Source}) {
			return false
		}
	}
	if flags&MessageMatchType != 0 {
		if t, _ := req.Int("TYPE"); t != sm.Payload.Type {
			return false
		}
	}
	return true
}

// inboxTake returns blazeID's stored messages that match req, removing them when purge is set.
func (m *messaging) inboxTake(blazeID int64, req *tdf.Struct, purge bool) []ServerMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()
	var out, keep []ServerMessage
	for _, sm := range m.inbox[blazeID] {
		if inboxMatch(req, sm) {
			out = append(out, sm)
		} else {
			keep = append(keep, sm)
		}
	}
	if purge {
		if len(keep) == 0 {
			delete(m.inbox, blazeID)
		} else {
			m.inbox[blazeID] = keep
		}
	}
	return out
}

// messageTypeName names a TYPE for the log.
func messageTypeName(t int64) string {
	switch t {
	case MessageTypeInstant:
		return "instant message"
	case MessageTypeChat:
		return "chat"
	case MessageTypeWhisper:
		return "whisper"
	case MessageTypePvPInvite:
		return "PvP invite"
	case MessageTypeCustomMatchInvite:
		return "custom match invite"
	case MessageTypePracticeMatchInvite:
		return "practice match invite"
	case MessageTypePvPInviteAnswer, MessageTypeCustomInviteAnswer, MessageTypePracticeInviteAnswer:
		return "invite answer"
	case MessageTypeInviteQuery:
		return "invite query"
	case MessageTypeInviteQueryAnswer:
		return "invite query answer"
	case MessageTypeFriendRequest:
		return "friend request"
	case MessageTypeFriendAccepted:
		return "friend request accepted"
	case MessageTypeFriendDeclined:
		return "friend request declined"
	case MessageTypeFriendRemoved:
		return "friend removed"
	}
	return "type"
}

func targetTypeName(t tdf.ObjType) string {
	switch t {
	case ObjTypeUser:
		return "user"
	case ObjTypePlaygroup:
		return "playgroup"
	case ObjTypeGame:
		return "game"
	}
	return fmt.Sprintf("(%d, %d)", t.Component, t.Type)
}

func registerMessaging(s *Server) {
	// sendMessage (ClientMessage 0x012EE228 as the request): relay, reply, then NotifyMessage to the
	// recipients: on the sender's own connection queued after the reply (the echo), elsewhere pushed after
	// the reply is out.
	s.Register(ComponentMessaging, CmdMsgSendMessage, "sendMessage", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			r.Drop("sendMessage before login")
			return nil
		}
		msg, ok := ClientMessageFromRequest(r.Body)
		if !ok {
			r.Server.Log.Logf("blaze", "   message from %d: no TTYP", sess.Persona.ID)
			r.Fail(ErrMessagingInvalidParam)
			return nil
		}
		if r.Server.joins.takeAnswer(sess.Persona.ID, msg, r.Server.Now()) {
			// the answer to an invite the server sent in another user's name (joincodes.go): that user's client never
			// sent it, so it doesn't get the answer
			answer, _ := msg.Attr(MsgAttrInviteAnswer)
			inviteNo, _ := msg.Attr(MsgAttrInviteID)
			id := r.Server.msgs.newID()
			r.Server.Log.Logf("blaze", "   message %d from %d: answer %q to the server's invite %s in the name of %d; dropped",
				id, sess.Persona.ID, answer, inviteNo, msg.Targets[0])
			return SendMessageReply(id, nil)
		}
		res := r.Server.relay(sess.Persona, msg)
		desc := fmt.Sprintf("%s %#x, flags %#x, to %s %v", messageTypeName(msg.Type), msg.Type, msg.Flags,
			targetTypeName(msg.TargetType), msg.Targets)
		if res.err != 0 {
			r.Server.Log.Logf("blaze", "   message from %d (%s): error %#04x (%s)", sess.Persona.ID, desc, res.err, res.why)
			r.Fail(res.err)
			return nil
		}
		dropped := ""
		if len(res.blocked) > 0 {
			dropped = fmt.Sprintf(", dropped for %v (blocked the sender)", res.blocked)
		}
		r.Server.Log.Logf("blaze", "   message %d from %d %q (%s): delivered to %v, stored for %v%s", res.note.ID,
			sess.Persona.ID, sess.Persona.Name, desc, res.delivered, res.stored, dropped)
		note := MessageNotification(res.note)
		var others []*Conn
		for _, c := range res.conns {
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
		var ids []int64
		if len(res.stored) > 0 {
			ids = []int64{res.note.ID}
		}
		return SendMessageReply(res.note.ID, ids)
	})
	// fetchMessages (FetchMessageRequest 0x01462190) follows getLists at every login (b5_run4 onwards: FLAG 0,
	// everything else 0, i.e. all messages; sender cBlazeRoomsManager 0x00AAD520 -> 0x00CAA680): the user's
	// stored messages as NotifyMessage after the count (MCNT). They stay stored until purged.
	s.Register(ComponentMessaging, CmdMsgFetchMessages, "fetchMessages", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			return MessageCountReply(0)
		}
		msgs := r.Server.msgs.inboxTake(sess.Persona.ID, r.Body, false)
		if len(msgs) > 0 {
			r.Server.Log.Logf("blaze", "   messages for %d: %d fetched", sess.Persona.ID, len(msgs))
		}
		for _, sm := range msgs {
			r.Notify(ComponentMessaging, NotifyMessageID, "NotifyMessage", MessageNotification(sm))
		}
		return MessageCountReply(len(msgs))
	})
	// purgeMessages (PurgeMessageRequest 0x012EE318) right after it (FLAG 4, TYPE 3: the whispers), and by
	// MGID or SRCE from the chat code (0x00AAAF50, 0x00ABB840, 0x00ABB940): delete what matches, reply the
	// count.
	s.Register(ComponentMessaging, CmdMsgPurgeMessages, "purgeMessages", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			return MessageCountReply(0)
		}
		msgs := r.Server.msgs.inboxTake(sess.Persona.ID, r.Body, true)
		if len(msgs) > 0 {
			r.Server.Log.Logf("blaze", "   messages for %d: %d purged", sess.Persona.ID, len(msgs))
		}
		return MessageCountReply(len(msgs))
	})
	// Util::filterForProfanity (cmd 0x14; Blaze::Util::UserStringList 0x012FCA38 {UTXT list of UserText
	// {UTXT}}; sender 0x00CC5120, job 0x012FC650 -> FilterUserTextResponse) comes before every message with
	// MessageFlagFilter, i.e. every chat line: without a reply no chat message is ever sent.
	s.Register(ComponentUtil, CmdUtilFilterForUText, "filterForProfanity", func(r *Request) []byte {
		var texts []string
		if f, ok := r.Body.Get("UTXT"); ok {
			if l, ok := f.Value.(*tdf.List); ok {
				for _, it := range l.Items {
					st, _ := it.(*tdf.Struct)
					t, _ := st.Str("UTXT")
					texts = append(texts, t)
				}
			}
		}
		r.Server.Log.Logf("blaze", "   profanity filter: %d texts (%s), all passed", len(texts), strings.Join(quoteAll(texts), ", "))
		return FilterUserTextReply(texts)
	})
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
