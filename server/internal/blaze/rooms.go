package blaze

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// Rooms component (0x15), standard BlazeSDK: the Social panel's Rooms tab and the bottom bar's room windows.
//
// The client sends only selectViewUpdates, selectCategoryUpdates, joinRoom, leaveRoom, toggleJoinedRoomNotifications
// and selectPseudoRoomUpdates (a scan of the Rooms senders 0x00C91A00-0x00C93000); everything the tab shows is pushed:
// the view (RoomViewAdded) after selectViewUpdates, the categories and rooms (RoomCategoryAdded, RoomAdded) after each
// selectCategoryUpdates of the view. The rooms manager (cBlazeRoomsManager 0x00AAD240) selects only a view whose NAME
// starts with "allRooms" (0x00AAAD60). The server serves a fixed list of public rooms from its configuration
// (dgserver --chat-rooms), in one view and two categories (the rooms the client opens by itself in the second,
// ROOMS3), in memory only; nobody creates rooms, and nobody moderates.
const (
	ComponentRooms                               = 0x15
	CmdRoomsSelectViewUpdate                     = 0x0A
	CmdRoomsSelectCategoryUpdates                = 0x0B
	CmdRoomsJoinRoom                             = 0x14
	CmdRoomsLeaveRoom                            = 0x15
	CmdRoomsToggleJoinedRoomNotifications        = 0x96
	NotifyRoomViewAdded                   uint16 = 0x0B // RoomViewData
	NotifyRoomCategoryAdded               uint16 = 0x15 // RoomCategoryData
	NotifyRoomUpdated                     uint16 = 0x1E // RoomData
	NotifyRoomAdded                       uint16 = 0x1F // RoomData
	NotifyRoomMemberJoined                uint16 = 0x32 // RoomMemberData
	NotifyRoomMemberLeft                  uint16 = 0x33 // RoomMemberRemoved
)

// The Rooms errors we answer with (the upper half of ROOMS_ERR_*, from the client's error table). The client shows
// none of them: an error reply to joinRoom drops the pending join and posts an event nothing handles (0x00AAB3A0).
const (
	ErrRoomsNotFound      = 0x000B // ROOMS_ERR_NOT_FOUND
	ErrRoomsRoomFull      = 0x000C // ROOMS_ERR_ROOM_FULL
	ErrRoomsUnknownView   = 0x000D // ROOMS_ERR_UNKNOWN_VIEW
	ErrRoomsAlreadyMember = 0x000E // ROOMS_ERR_ALREADY_MEMBER
)

// ObjTypeRoom is the room's ObjectType (component 0x15, type 1: .data 0x01461144, copied to 0x01BEE294 by
// 0x01191F00): the target type of a room's chat lines (sendMessage TTYP, BlazeRoomsManager_SendRoomChat 0x00AB0200).
var ObjTypeRoom = tdf.ObjType{Component: ComponentRooms, Type: 1}

// The one view and the two categories. The view's NAME must start with "allRooms" (the rooms manager's view name,
// 0x012B3A1C); the tab shows neither the view nor the categories (one flat list under "Public Chatrooms"). A room in
// a category whose CTID is AutoOpenCategoryMin or more is one our chat-movie mod (client_ui/chatrooms/AutoRooms.as,
// its AUTO_CATEGORY_MIN, installed as game\Data\zz_Chat.snap) joins by itself once per login (ROOMS3): the open rooms
// (--chat-rooms Name:open) are in AutoOpenCategoryID, the others in RoomCategoryID. A client without the mod lists
// both alike.
const (
	RoomViewID          = 1
	RoomViewName        = "allRooms"
	RoomCategoryID      = 1
	AutoOpenCategoryMin = 1000 // AutoRooms.as AUTO_CATEGORY_MIN: keep both the same
	AutoOpenCategoryID  = AutoOpenCategoryMin
	RoomCapacity        = 50 // a room's default capacity (--chat-rooms Name:capacity sets another)
	MaxChatRooms        = 99
	MaxRoomNameRune     = 32
)

// ChatRoom is one configured room.
type ChatRoom struct {
	ID       int64 // RMID: assigned in the configuration's order, all of one width (the tab sorts by the id string)
	Name     string
	Capacity int
	Queue    bool // the server posts the PvP queue's news into it (roomQueueNews)
	Open     bool // the client (with zz_Chat.snap) joins it by itself at the login: its category is AutoOpenCategoryID
}

// CategoryID is the room's category: AutoOpenCategoryID for an open room, else RoomCategoryID.
func (r ChatRoom) CategoryID() int64 {
	if r.Open {
		return AutoOpenCategoryID
	}
	return RoomCategoryID
}

// DefaultChatRooms is dgserver --chat-rooms' default: three public rooms, the last with the PvP queue's news and
// opened by the client itself at the login. No original room names survive (the client has none; its rooms were
// server data) [assumed names; that only Matchmaking opens is our choice too].
const DefaultChatRooms = "General, Looking for group, Matchmaking:queue:open"

// ParseChatRooms reads dgserver --chat-rooms: rooms separated by commas, in the tab's order; each a name, optionally
// followed by ":<capacity>" (2-1000, default RoomCapacity), ":queue" (the room that gets the PvP queue's news; at
// most one) and ":open" (the client joins it by itself at every login, ROOMS3), in any order. "none" or "" = no rooms
// (the Rooms tab stays empty). The rooms get ids 1, 2, … in this order (10, 11, … from ten rooms on, so that all ids
// have one width).
func ParseChatRooms(s string) ([]ChatRoom, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "none") {
		return nil, nil
	}
	var rooms []ChatRoom
	queue := false
	for _, item := range strings.Split(s, ",") {
		parts := strings.Split(item, ":")
		r := ChatRoom{Name: strings.TrimSpace(parts[0]), Capacity: RoomCapacity}
		if r.Name == "" {
			return nil, fmt.Errorf("a room without a name in %q", s)
		}
		if n := len([]rune(r.Name)); n > MaxRoomNameRune {
			return nil, fmt.Errorf("room name %q: %d characters, at most %d", r.Name, n, MaxRoomNameRune)
		}
		for _, p := range parts[1:] {
			p = strings.TrimSpace(p)
			if strings.EqualFold(p, "queue") {
				if queue {
					return nil, fmt.Errorf("room %q: only one room can have the queue's news", r.Name)
				}
				r.Queue, queue = true, true
				continue
			}
			if strings.EqualFold(p, "open") {
				r.Open = true
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil || n < 2 || n > 1000 {
				return nil, fmt.Errorf("room %q: %q is neither a capacity (2-1000) nor \"queue\" nor \"open\"", r.Name, p)
			}
			r.Capacity = n
		}
		if slices.ContainsFunc(rooms, func(o ChatRoom) bool { return strings.EqualFold(o.Name, r.Name) }) {
			return nil, fmt.Errorf("room %q appears twice", r.Name)
		}
		rooms = append(rooms, r)
	}
	if len(rooms) > MaxChatRooms {
		return nil, fmt.Errorf("%d rooms, at most %d", len(rooms), MaxChatRooms)
	}
	first := int64(1)
	if len(rooms) > 9 {
		first = 10
	}
	for i := range rooms {
		rooms[i].ID = first + int64(i)
	}
	return rooms, nil
}

// DescribeChatRooms is the rooms for the log: `1 "General" (50), 3 "Matchmaking" (50, queue news, opens)`.
func DescribeChatRooms(rooms []ChatRoom) string {
	if len(rooms) == 0 {
		return "none"
	}
	parts := make([]string, len(rooms))
	for i, r := range rooms {
		parts[i] = fmt.Sprintf("%d %q (%d", r.ID, r.Name, r.Capacity)
		if r.Queue {
			parts[i] += ", queue news"
		}
		if r.Open {
			parts[i] += ", opens"
		}
		parts[i] += ")"
	}
	return strings.Join(parts, ", ")
}

// chatRooms is the rooms' state: the rooms with their members, the connections subscribed to the view (their tab
// lists the rooms: they get RoomUpdated when a room's population changes) and what the queue room was told last.
type chatRooms struct {
	mu    sync.Mutex
	rooms []*chatRoom
	subs  []*Conn
	// the queue room's news (roomQueueNews): whether a live queue line was posted, its key (the state it shows) and text
	queueOpen bool
	queueKey  string
	queueLine string
}

type chatRoom struct {
	ChatRoom
	members []roomMember // in the order they joined
}

type roomMember struct {
	id   int64 // the persona (BlazeId)
	conn *Conn
}

// SetChatRooms replaces the rooms (dgserver --chat-rooms; before the server serves: members are dropped).
func (s *Server) SetChatRooms(rooms []ChatRoom) {
	s.rooms.mu.Lock()
	defer s.rooms.mu.Unlock()
	s.rooms.rooms = nil
	for _, r := range rooms {
		s.rooms.rooms = append(s.rooms.rooms, &chatRoom{ChatRoom: r})
	}
}

// ChatRooms returns the configured rooms.
func (s *Server) ChatRooms() []ChatRoom {
	s.rooms.mu.Lock()
	defer s.rooms.mu.Unlock()
	out := make([]ChatRoom, len(s.rooms.rooms))
	for i, r := range s.rooms.rooms {
		out[i] = r.ChatRoom
	}
	return out
}

func (cr *chatRooms) byIDLocked(id int64) *chatRoom {
	for _, r := range cr.rooms {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// memberIDs returns room id's members (the messaging relay: a room line reaches them all).
func (cr *chatRooms) memberIDs(id int64) ([]int64, bool) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	r := cr.byIDLocked(id)
	if r == nil {
		return nil, false
	}
	ids := make([]int64, len(r.members))
	for i, m := range r.members {
		ids[i] = m.id
	}
	return ids, true
}

// watchersLocked are the connections that get RoomUpdated when r's count changes: every subscriber, the members
// included. A member's tab entry also changes only with ChatRoomUpdated (RoomUpdated, RoomAdded): RoomMemberJoined
// moves the count in the room's window only (seen live: the tab said 1/50 with two members in).
func (cr *chatRooms) watchersLocked() []*Conn {
	return slices.Clone(cr.subs)
}

// The builders. Member tables: RoomViewData 0x012FF778, RoomCategoryData 0x012FF7D8, RoomData 0x012FF8C8,
// RoomMemberData 0x012FF9D0, JoinRoomResponse 0x012FFB70, LeaveRoomResponse 0x012FFBD0, RoomMemberRemoved 0x01300038
// (golden cases ROOMS1_CASES in tdf_golden.py). Members left out stay at their defaults.

// roomView writes RoomViewData: DISP, NAME (prefix "allRooms", read by 0x00AAAD60), VWID (non-zero; 0x00C953A0 adds a
// view it doesn't know by it).
func roomView(w *tdf.Writer) {
	w.String("DISP", "Chat rooms").String("NAME", RoomViewName).Int("VWID", RoomViewID)
}

// roomCategory writes RoomCategoryData for category ctid (RoomCategoryID or AutoOpenCategoryID): CAPA (a room's
// capacity when its CAP is -1), CTID, DESC/DISP/NAME (not shown), FLAG 0 (0x4 would make it a pseudo-room category,
// 0x8 makes the logout leave its rooms: 0x00C97660), VWID (must name a known view, else 0x00C967C0 drops it).
func roomCategory(w *tdf.Writer, ctid int64) {
	desc, disp, name := "Public chat rooms", "Public", "public"
	if ctid == AutoOpenCategoryID {
		desc, disp, name = "Public chat rooms opened at the login", "Open", "open"
	}
	w.Int("CAPA", RoomCapacity).Int("CTID", ctid).String("DESC", desc).String("DISP", disp)
	w.Int("FLAG", 0).String("NAME", name).Int("VWID", RoomViewID)
}

// roomCategories are the categories that hold a room, in CTID order (RoomCategoryID, then AutoOpenCategoryID).
func roomCategories(rooms []roomPop) []int64 {
	var out []int64
	for _, id := range []int64{RoomCategoryID, AutoOpenCategoryID} {
		if slices.ContainsFunc(rooms, func(r roomPop) bool { return r.CategoryID() == id }) {
			out = append(out, id)
		}
	}
	return out
}

// roomData writes RoomData: CAP, CTID (the room's category; must name a known one, else 0x00C954D0 drops it), NAME,
// POPU (the
// members; the tab shows it while the player isn't in the room), PSWD "" (no lock: the UI can't enter a password),
// RMID (a decimal string in the UI, strtoull back).
func roomData(w *tdf.Writer, r ChatRoom, popu int) {
	w.Int("CAP", int64(r.Capacity)).Int("CTID", r.CategoryID()).String("NAME", r.Name).Int("POPU", int64(popu))
	w.String("PSWD", "").Int("RMID", r.ID)
}

// RoomViewAddedNotification is Rooms::RoomViewAdded (0x0B, RoomViewData): the view, after selectViewUpdates.
func RoomViewAddedNotification() []byte {
	w := tdf.NewWriter()
	roomView(w)
	return w.Bytes()
}

// RoomCategoryAddedNotification is Rooms::RoomCategoryAdded (0x15, RoomCategoryData): category ctid, after the
// view's selectCategoryUpdates reply (one per category that holds a room, all before the rooms).
func RoomCategoryAddedNotification(ctid int64) []byte {
	w := tdf.NewWriter()
	roomCategory(w, ctid)
	return w.Bytes()
}

// RoomDataNotification is Rooms::RoomAdded (0x1F) and RoomUpdated (0x1E), both RoomData: a room with its
// population. RoomAdded follows the category; RoomUpdated tells the tab a new count (RoomPopulationUpdated would
// change POPU too, but no UI listens to its event).
func RoomDataNotification(r ChatRoom, popu int) []byte {
	w := tdf.NewWriter()
	roomData(w, r, popu)
	return w.Bytes()
}

// RoomMemberNotification is Rooms::RoomMemberJoined (0x32, RoomMemberData {BZID, RMID}).
func RoomMemberNotification(bzid, rmid int64) []byte {
	return tdf.NewWriter().Int("BZID", bzid).Int("RMID", rmid).Bytes()
}

// RoomMemberLeftNotification is Rooms::RoomMemberLeft (0x33, RoomMemberRemoved {MBID, RMID}); never sent for the
// receiver's own id (the client then only posts an event nothing handles, and its window stays, dead).
func RoomMemberLeftNotification(mbid, rmid int64) []byte {
	return tdf.NewWriter().Int("MBID", mbid).Int("RMID", rmid).Bytes()
}

// JoinRoomReply is Rooms::joinRoom -> JoinRoomResponse {CDAT, MDAT, RDAT, VDAT, VERS} (its callback 0x00C96A50):
// MDAT the joiner as a member, RDAT the room with POPU counting the members after the join, the joiner included (the
// join completes when the client's member count reaches it, after one RoomMemberJoined per other member).
func JoinRoomReply(joiner int64, r ChatRoom, popu int) []byte {
	w := tdf.NewWriter()
	w.Struct("CDAT", func(w *tdf.Writer) { roomCategory(w, r.CategoryID()) })
	w.Struct("MDAT", func(w *tdf.Writer) { w.Int("BZID", joiner).Int("RMID", r.ID) })
	w.Struct("RDAT", func(w *tdf.Writer) { roomData(w, r, popu) })
	w.Struct("VDAT", roomView)
	return w.Int("VERS", 0).Bytes()
}

// LeaveRoomReply is Rooms::leaveRoom -> LeaveRoomResponse {CTID, RMID}: the room's category and id (its callback
// 0x00C965D0 doesn't read them).
func LeaveRoomReply(ctid, rmid int64) []byte {
	return tdf.NewWriter().Int("CTID", ctid).Int("RMID", rmid).Bytes()
}

// SelectCategoryUpdatesReply is Rooms::selectCategoryUpdates -> Blaze::Rooms::SelectCategoryUpdatesResponse (member
// table 0x012FFAC0: VWID, uint64): the request's view id back. The client's callback for the view (0x00C93130) looks
// the reply's VWID up among its views and selects it; the one for VWID 0 (0x00C93BF0) clears the rooms not joined.
// The logout's (0x00C928C0, called by the rooms manager's teardown 0x00C97660 with VWID 0) has no callback; on the
// public server it was the last request of each logout, unanswered (LBFIX).
func SelectCategoryUpdatesReply(viewID uint64) []byte {
	return tdf.NewWriter().Int("VWID", int64(viewID)).Bytes()
}

// roomChatAttrs are a room line's attributes as the client sends them (BlazeRoomsManager_SendRoomChat 0x00AB0200):
// the text, the channel kind "0" (room) and the unfiltered text.
func roomChatAttrs(text string) []MsgAttr {
	return []MsgAttr{{MsgAttrText, text}, {MsgAttrPlaygroupID, "0"}, {MsgAttrTextUnfiltered, text}}
}

func registerRooms(s *Server) {
	rooms, err := ParseChatRooms(DefaultChatRooms)
	if err != nil {
		panic(err) // the default is a constant (TestChatRoomsConfig)
	}
	s.SetChatRooms(rooms)

	// selectViewUpdates (SelectViewUpdatesRequest UPDT 1, right after enterService; at the logout too):
	// the RPC job (0x011F7C00 at 0x00C92072) has no response class, so the reply is empty. With UPDT 1 the view follows
	// (RoomViewAdded), which the rooms manager selects by its name.
	s.Register(ComponentRooms, CmdRoomsSelectViewUpdate, "selectViewUpdates", func(r *Request) []byte {
		updt, _ := r.Body.Int("UPDT")
		if updt != 0 && len(r.Server.ChatRooms()) > 0 {
			r.Notify(ComponentRooms, NotifyRoomViewAdded, "RoomViewAdded", RoomViewAddedNotification())
		}
		return nil
	})
	// selectCategoryUpdates: VWID 0 first (and at the logout, without a callback), then the view's (RoomsAPI
	// 0x00C96430, and every refresh of the panel, 0x00AAADF0). The reply is the request's VWID; after the view's come
	// its category and rooms, and the connection is subscribed (RoomUpdated) until VWID 0 or its session's end.
	s.Register(ComponentRooms, CmdRoomsSelectCategoryUpdates, "selectCategoryUpdates", func(r *Request) []byte {
		id, _ := r.Body.Int("VWID")
		if id == 0 {
			r.Server.roomsUnsubscribe(r.Conn)
			return SelectCategoryUpdatesReply(0)
		}
		if id != RoomViewID || len(r.Server.ChatRooms()) == 0 {
			r.Server.Log.Logf("blaze", "   rooms: view %d unknown", id)
			r.Fail(ErrRoomsUnknownView)
			return nil
		}
		rooms := r.Server.roomsSubscribe(r.Conn)
		for _, ctid := range roomCategories(rooms) {
			r.Notify(ComponentRooms, NotifyRoomCategoryAdded, "RoomCategoryAdded", RoomCategoryAddedNotification(ctid))
		}
		var desc []string
		for _, rm := range rooms {
			r.Notify(ComponentRooms, NotifyRoomAdded, "RoomAdded", RoomDataNotification(rm.ChatRoom, rm.popu))
			d := fmt.Sprintf("%d %q %d/%d", rm.ID, rm.Name, rm.popu, rm.Capacity)
			if rm.Open {
				d += fmt.Sprintf(" (category %d)", rm.CategoryID())
			}
			desc = append(desc, d)
		}
		r.Server.Log.Logf("blaze", "   rooms: view %d, %d rooms: %s", id, len(rooms), strings.Join(desc, ", "))
		return SelectCategoryUpdatesReply(uint64(id))
	})
	s.Register(ComponentRooms, CmdRoomsJoinRoom, "joinRoom", func(r *Request) []byte {
		return r.Server.joinRoom(r)
	})
	s.Register(ComponentRooms, CmdRoomsLeaveRoom, "leaveRoom", func(r *Request) []byte {
		sess := r.Conn.Session
		if sess == nil || sess.Persona.ID == 0 {
			r.Drop("leaveRoom before login")
			return nil
		}
		id, _ := r.Body.Int("RMID")
		r.Server.leaveRoom(id, sess.Persona, r.Conn, r)
		return LeaveRoomReply(r.Server.roomCategoryOf(id), id)
	})
	// toggleJoinedRoomNotifications {ENBL} (RoomsAPI slot 8 0x00C92990, and at the logout with ENBL 0): no response
	// class, an empty reply. We always notify.
	s.Register(ComponentRooms, CmdRoomsToggleJoinedRoomNotifications, "toggleJoinedRoomNotifications",
		func(r *Request) []byte { return nil })
}

// roomPop is a room with its population for the tab.
type roomPop struct {
	ChatRoom
	popu int
}

// roomsSubscribe subscribes c to the view (once; its session's end unsubscribes it) and returns the rooms as they are.
func (s *Server) roomsSubscribe(c *Conn) []roomPop {
	cr := &s.rooms
	cr.mu.Lock()
	added := !slices.Contains(cr.subs, c)
	if added {
		cr.subs = append(cr.subs, c)
	}
	out := make([]roomPop, len(cr.rooms))
	for i, rm := range cr.rooms {
		out[i] = roomPop{rm.ChatRoom, len(rm.members)}
	}
	cr.mu.Unlock()
	if added {
		c.OnSessionEnd(func() { s.roomsUnsubscribe(c) })
	}
	return out
}

// roomCategoryOf is room id's category (RoomCategoryID for an unknown room).
func (s *Server) roomCategoryOf(id int64) int64 {
	cr := &s.rooms
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if rm := cr.byIDLocked(id); rm != nil {
		return rm.CategoryID()
	}
	return RoomCategoryID
}

func (s *Server) roomsUnsubscribe(c *Conn) {
	cr := &s.rooms
	cr.mu.Lock()
	cr.subs = slices.DeleteFunc(cr.subs, func(o *Conn) bool { return o == c })
	cr.mu.Unlock()
}

// joinRoom answers Rooms::joinRoom {CTID, INID, INVT, PASS, PVAL, RMID} (RoomsAPI_JoinRoom 0x00C97800, from the tab's
// double-click or "Join Chatroom": MaxisChat.JoinRoom 0x004960D0). Unknown room: ROOMS_ERR_NOT_FOUND; a member already:
// ROOMS_ERR_ALREADY_MEMBER; full: ROOMS_ERR_ROOM_FULL (all silent in the client). Else, in this order: UserAdded and
// UserUpdated to the joiner for each member its client doesn't know (the members' names come from their user objects:
// the member constructor 0x00C93620 acquires the user, and OnChatMemberJoined 0x0049A0B0 skips a member without one),
// the reply, one RoomMemberJoined per other member; then to each other member the joiner as a user (if new) and its
// RoomMemberJoined, and to every subscriber (the members too) RoomUpdated with the new count. The queue room's
// joiner also learns the announcer as a user and gets the open queue's line. The room is found by its RMID; the
// request's CTID is the category the client was told (an open room's AutoOpenCategoryID, from the mod's join as
// from a double-click), and a different one is only logged (a list from before a restart with another config).
func (s *Server) joinRoom(r *Request) []byte {
	sess := r.Conn.Session
	if sess == nil || sess.Persona.ID == 0 {
		r.Drop("joinRoom before login")
		return nil
	}
	me := sess.Persona
	id, _ := r.Body.Int("RMID")
	cr := &s.rooms
	cr.mu.Lock()
	rm := cr.byIDLocked(id)
	var code uint16
	var why string
	switch {
	case rm == nil:
		code, why = ErrRoomsNotFound, "no such room"
	case slices.ContainsFunc(rm.members, func(m roomMember) bool { return m.id == me.ID }):
		code, why = ErrRoomsAlreadyMember, "a member already"
	case len(rm.members) >= rm.Capacity:
		code, why = ErrRoomsRoomFull, fmt.Sprintf("full (%d)", rm.Capacity)
	}
	if code != 0 {
		cr.mu.Unlock()
		s.Log.Logf("blaze", "   room %d: join by %d: error %#04x (%s)", id, me.ID, code, why)
		r.Fail(code)
		return nil
	}
	others := slices.Clone(rm.members)
	rm.members = append(rm.members, roomMember{id: me.ID, conn: r.Conn})
	room, popu := rm.ChatRoom, len(rm.members)
	watchers := cr.watchersLocked()
	queueLine := ""
	if room.Queue && cr.queueOpen {
		queueLine = cr.queueLine
	}
	cr.mu.Unlock()

	s.Log.Logf("blaze", "   room %d %q: %d %q joined (%d/%d)", room.ID, room.Name, me.ID, me.Name, popu, room.Capacity)
	if ctid, ok := r.Body.Int("CTID"); ok && ctid != room.CategoryID() {
		s.Log.Logf("blaze", "   room %d: the join named category %d, the room's is %d", room.ID, ctid, room.CategoryID())
	}
	conn := r.Conn
	conn.OnSessionEnd(func() { s.leaveRoom(room.ID, me, conn, nil) })
	for _, m := range others {
		r.introduce(m.id)
	}
	if room.Queue {
		r.introduceAnnouncer()
	}
	for _, m := range others {
		r.Notify(ComponentRooms, NotifyRoomMemberJoined, "RoomMemberJoined", RoomMemberNotification(m.id, room.ID))
	}
	if queueLine != "" {
		r.Notify(ComponentMessaging, NotifyMessageID, "NotifyMessage", s.announcerLine(room.ID, queueLine))
	}
	joined := RoomMemberNotification(me.ID, room.ID)
	updated := RoomDataNotification(room, popu)
	r.After(func() {
		for _, m := range others {
			s.pushUserIfNew(m.conn, me.ID)
			m.conn.Push(s, ComponentRooms, NotifyRoomMemberJoined, "RoomMemberJoined", joined)
		}
		for _, c := range watchers {
			c.Push(s, ComponentRooms, NotifyRoomUpdated, "RoomUpdated", updated)
		}
	})
	return JoinRoomReply(me.ID, room, popu)
}

// leaveRoom takes persona p (on conn) out of room id: leaveRoom (r set: its reply goes out first, and the leaver's tab
// gets the new count too), or the session's end (r nil). The other members get RoomMemberLeft, the subscribers (the remaining
// members too) RoomUpdated. Nothing when p isn't a member (the reply is still OK: the client then closes its window).
func (s *Server) leaveRoom(id int64, p store.Persona, conn *Conn, r *Request) {
	cr := &s.rooms
	cr.mu.Lock()
	rm := cr.byIDLocked(id)
	i := -1
	if rm != nil {
		i = slices.IndexFunc(rm.members, func(m roomMember) bool { return m.id == p.ID && m.conn == conn })
	}
	if i < 0 {
		cr.mu.Unlock()
		if r != nil {
			s.Log.Logf("blaze", "   room %d: leave by %d, not a member", id, p.ID)
		}
		return
	}
	rm.members = slices.Delete(rm.members, i, i+1)
	others := slices.Clone(rm.members)
	room, popu := rm.ChatRoom, len(rm.members)
	watchers := cr.watchersLocked()
	cr.mu.Unlock()

	how := "left"
	if r == nil {
		how = "dropped (session ended)"
		watchers = slices.DeleteFunc(watchers, func(c *Conn) bool { return c == conn })
	}
	s.Log.Logf("blaze", "   room %d %q: %d %q %s (%d/%d)", room.ID, room.Name, p.ID, p.Name, how, popu, room.Capacity)
	left := RoomMemberLeftNotification(p.ID, room.ID)
	updated := RoomDataNotification(room, popu)
	send := func() {
		for _, m := range others {
			m.conn.Push(s, ComponentRooms, NotifyRoomMemberLeft, "RoomMemberLeft", left)
		}
		for _, c := range watchers {
			c.Push(s, ComponentRooms, NotifyRoomUpdated, "RoomUpdated", updated)
		}
	}
	if r != nil {
		r.After(send)
	} else {
		send()
	}
}

// The queue room's announcer (our own feature: the original had no such room). The server posts the PvP queue's news
// (the Discord queue message's texts, plain) into the room as chat lines from a persona that no account has: the
// joiner's client learns it as a user (UserAdded), so the line shows its name (the bottom bar looks a sender that
// isn't a window member up in the user cache, MaxisChat.GetUserData 0x004A1DD0 [inferred, untested]). It isn't a
// member: the room's count stays the players'.
const (
	QueueAnnouncerID   = store.FirstPersonaID - 1 // below every persona id the store hands out
	queueAnnouncerUser = store.FirstUserID - 1
	QueueAnnouncerName = "PvP queue"
)

// introduceAnnouncer queues UserAdded and UserUpdated (online) for the announcer before the reply, once per login.
func (r *Request) introduceAnnouncer() {
	if !r.Conn.firstSight(QueueAnnouncerID) {
		return
	}
	acct := store.Account{UserID: queueAnnouncerUser}
	r.NotifyFirst(ComponentUserSessions, NotifyUserAdded, "UserAdded",
		UserAddedNotification(acct, store.Persona{ID: QueueAnnouncerID, Name: QueueAnnouncerName}, 1))
	r.NotifyFirst(ComponentUserSessions, NotifyUserUpdated, "UserUpdated", UserUpdatedNotification(QueueAnnouncerID, true))
}

// announcerLine is a NotifyMessage with the announcer's line text in room rmid (a chat line as a member sends it:
// TYPE 2, FLAG 6, the room target).
func (s *Server) announcerLine(rmid int64, text string) []byte {
	return MessageNotification(ServerMessage{ID: s.msgs.newID(), Name: QueueAnnouncerName, Source: QueueAnnouncerID,
		Time: s.Now().Unix(), Payload: ClientMessage{Attrs: roomChatAttrs(text), Flags: MessageFlagEcho | MessageFlagFilter,
			Targets: []int64{rmid}, TargetType: ObjTypeRoom, Type: MessageTypeChat}})
}

// roomQueueNews posts the PvP queue's news into the queue room (called with the Discord queue message's updates,
// discordQueueSync, so in the same order): each started match ("PvP match started: …"), then the queue's line when
// what it shows changed (who, the count, the start time), or "PvP queue closed" when the queue emptied. Only the
// room's members see it (the client joins no room by itself).
func (s *Server) roomQueueNews(v QueueView, started []MatchStarted) {
	cr := &s.rooms
	cr.mu.Lock()
	var room *chatRoom
	for _, rm := range cr.rooms {
		if rm.Queue {
			room = rm
		}
	}
	if room == nil {
		cr.mu.Unlock()
		return
	}
	var lines []string
	for _, m := range started {
		lines = append(lines, strings.ReplaceAll(matchStartedText(m), "**", ""))
		cr.queueOpen, cr.queueKey, cr.queueLine = false, "", ""
	}
	if v.State.Players == 0 {
		if cr.queueOpen {
			lines = append(lines, strings.ReplaceAll(queueClosedText, "**", ""))
		}
		cr.queueOpen, cr.queueKey, cr.queueLine = false, "", ""
	} else {
		now := s.Now()
		key := fmt.Sprintf("%d/%d %v %d", v.State.Players, v.State.Capacity, v.Bots, v.State.StartsAt.Unix())
		for _, t := range v.State.Tickets {
			key += fmt.Sprintf(" %d:%d", t.ID, len(t.Players))
		}
		if key != cr.queueKey {
			line := roomQueueText(v, now)
			lines = append(lines, line)
			cr.queueOpen, cr.queueKey, cr.queueLine = true, key, line
		}
	}
	var conns []*Conn
	for _, m := range room.members {
		if !slices.Contains(conns, m.conn) {
			conns = append(conns, m.conn)
		}
	}
	rmid := room.ID
	cr.mu.Unlock()
	for _, line := range lines {
		s.Log.Logf("blaze", "   room %d: queue news to %d members: %s", rmid, len(conns), line)
		note := s.announcerLine(rmid, line)
		for _, c := range conns {
			c.Push(s, ComponentMessaging, NotifyMessageID, "NotifyMessage", note)
		}
	}
}

// roomQueueText is the queue's line in the queue room: queueText's in plain text (the chat window shows no Markdown
// and no Discord timestamp), with the names and the time left: "Alice + 1 and Bob are queuing for PvP: 3/10, starts
// in 1:30, bots fill the open seats".
func roomQueueText(v QueueView, now time.Time) string {
	st := v.State
	parts := make([]string, len(st.Tickets))
	for i, t := range st.Tickets {
		parts[i] = t.Players[0].Name
		if n := len(t.Players) - 1; n > 0 {
			parts[i] += fmt.Sprintf(" + %d", n)
		}
	}
	who := joinAnd(parts)
	switch {
	case len(parts) == 0 && st.Players == 1:
		who = "A player is"
	case len(parts) == 0:
		who = fmt.Sprintf("%d players are", st.Players)
	case len(parts) == 1:
		who += " is"
	default:
		who += " are"
	}
	s := fmt.Sprintf("%s queuing for PvP: %d/%d, ", who, st.Players, st.Capacity)
	switch left := st.StartsAt.Sub(now); {
	case st.StartsAt.IsZero():
		return s + "waiting for more players"
	case left <= 0:
		s += "starting now"
	default:
		secs := int((left + time.Second - 1) / time.Second)
		if secs < 60 {
			s += fmt.Sprintf("starts in %d s", secs)
		} else {
			s += fmt.Sprintf("starts in %d:%02d", secs/60, secs%60)
		}
	}
	if st.Players >= st.Capacity {
		return s
	}
	if v.Bots {
		return s + ", bots fill the open seats"
	}
	return s + ", open seats stay empty"
}
