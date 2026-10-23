package blaze

import (
	"bytes"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// ROOMS2: the default rooms General (1) and Looking for group (2) as the ROOMS1 golden cases have them.
func rooms2Default(t *testing.T) []ChatRoom {
	t.Helper()
	rooms, err := ParseChatRooms(DefaultChatRooms)
	if err != nil {
		t.Fatal(err)
	}
	return rooms
}

// The builders make ROOMS1's golden bytes (tdf_golden.py ROOMS1_CASES, written from the client's member tables).
func TestROOMS2BuildersMatchGolden(t *testing.T) {
	g := golden.Load(t)
	rooms := rooms2Default(t)
	general, lfg, mm := rooms[0], rooms[1], rooms[2]
	second := store.FirstPersonaID + 1
	for _, c := range []struct {
		name string
		got  []byte
	}{
		{"roomviewadded_notification", RoomViewAddedNotification()},
		{"roomcategoryadded_notification", RoomCategoryAddedNotification(RoomCategoryID)},
		{"roomadded_notification", RoomDataNotification(general, 0)},
		{"roomadded_second_notification", RoomDataNotification(lfg, 0)},
		{"roomupdated_notification", RoomDataNotification(general, 2)},
		{"selectcategoryupdates_view_reply", SelectCategoryUpdatesReply(RoomViewID)},
		{"joinroom_reply", JoinRoomReply(store.FirstPersonaID, general, 1)},
		{"joinroom_second_reply", JoinRoomReply(second, general, 2)},
		{"roommemberjoined_notification", RoomMemberNotification(store.FirstPersonaID, general.ID)},
		{"roommemberjoined_second_notification", RoomMemberNotification(second, general.ID)},
		{"roommemberleft_notification", RoomMemberLeftNotification(second, general.ID)},
		{"leaveroom_reply", LeaveRoomReply(RoomCategoryID, general.ID)},
		// ROOMS3: the default's open room (Matchmaking, category 1000)
		{"roomcategoryadded_open_notification", RoomCategoryAddedNotification(AutoOpenCategoryID)},
		{"roomadded_open_notification", RoomDataNotification(mm, 0)},
		{"joinroom_open_reply", JoinRoomReply(store.FirstPersonaID, mm, 1)},
		{"leaveroom_open_reply", LeaveRoomReply(mm.CategoryID(), mm.ID)},
		{"notifymessage_room_notification", MessageNotification(ServerMessage{ID: 9, Name: "player", Source: store.FirstPersonaID,
			Time: testTime, Payload: ClientMessage{Attrs: roomChatAttrs("hello room"), Flags: MessageFlagEcho | MessageFlagFilter,
				Targets: []int64{general.ID}, TargetType: ObjTypeRoom, Type: MessageTypeChat}})},
	} {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// --chat-rooms: the default, capacities, the queue room, the open rooms, ids of one width from ten rooms on, the
// refusals.
func TestChatRoomsConfig(t *testing.T) {
	rooms := rooms2Default(t)
	want := []ChatRoom{{1, "General", 50, false, false}, {2, "Looking for group", 50, false, false},
		{3, "Matchmaking", 50, true, true}}
	if len(rooms) != len(want) {
		t.Fatalf("default: %+v", rooms)
	}
	for i := range want {
		if rooms[i] != want[i] {
			t.Errorf("default room %d: %+v, want %+v", i, rooms[i], want[i])
		}
	}
	if got := DescribeChatRooms(rooms); got != `1 "General" (50), 2 "Looking for group" (50), 3 "Matchmaking" (50, queue news, opens)` {
		t.Errorf("describe: %s", got)
	}
	if rooms[0].CategoryID() != RoomCategoryID || rooms[2].CategoryID() != AutoOpenCategoryID || AutoOpenCategoryID < AutoOpenCategoryMin {
		t.Errorf("categories %d %d", rooms[0].CategoryID(), rooms[2].CategoryID())
	}
	got, err := ParseChatRooms(" Lobby:10 , Queue: queue :200, Hello: OPEN ,Both:open:queue:5")
	if err == nil {
		t.Errorf("two queue rooms: %+v", got)
	}
	got, err = ParseChatRooms(" Lobby:10 , Queue: queue :200, Hello: OPEN ,Both:open:5:open")
	if err != nil || len(got) != 4 || got[0] != (ChatRoom{1, "Lobby", 10, false, false}) ||
		got[1] != (ChatRoom{2, "Queue", 200, true, false}) || got[2] != (ChatRoom{3, "Hello", 50, false, true}) ||
		got[3] != (ChatRoom{4, "Both", 5, false, true}) {
		t.Errorf("custom: %+v %v", got, err)
	}
	for _, s := range []string{"", "none", " None "} {
		if got, err := ParseChatRooms(s); got != nil || err != nil {
			t.Errorf("%q: %+v %v", s, got, err)
		}
	}
	ten, err := ParseChatRooms("a,b,c,d,e,f,g,h,i,j")
	if err != nil || ten[0].ID != 10 || ten[9].ID != 19 {
		t.Errorf("ten rooms: %+v %v", ten, err)
	}
	for _, bad := range []string{"a,,b", "a:1", "a:1001", "a:big", "a:opened", "a:queue,b:queue", "a,A", strings.Repeat("x", 33),
		strings.Repeat("r,", 99) + "last"} {
		if _, err := ParseChatRooms(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// The ROOMS1 call sequence over two pipe sessions, against the goldens: the view after selectViewUpdates, the
// category and rooms after the view's selectCategoryUpdates, two joins (the second joiner learns the first as a user
// and gets its RoomMemberJoined after the reply; the first learns the second the same way), a room line echoed to
// the sender and relayed to the other, a leave (RoomMemberLeft to the other, the new count to the leaver's tab), and
// the first's disconnect (the second's tab gets the count).
func TestRoomsGoldenSession(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) { s.msgs.nextID = 8 })
	rooms := ps.srv.ChatRooms()
	general := rooms[0]
	second := store.FirstPersonaID + 1
	enter := func(x *pipeSession, id uint32) {
		x.send(ComponentRooms, CmdRoomsSelectViewUpdate, id, x.request("selectviewupdates_request"))
		x.expect(ComponentRooms, CmdRoomsSelectViewUpdate, fire.Reply, id, "")
		x.expect(ComponentRooms, NotifyRoomViewAdded, fire.Notification, 0, "roomviewadded_notification")
		x.send(ComponentRooms, CmdRoomsSelectCategoryUpdates, id+1, x.request("selectcategoryupdates_request"))
		x.expect(ComponentRooms, CmdRoomsSelectCategoryUpdates, fire.Reply, id+1, "selectcategoryupdates_reply")
		x.send(ComponentRooms, CmdRoomsSelectCategoryUpdates, id+2, x.request("selectcategoryupdates_view_request"))
		x.expect(ComponentRooms, CmdRoomsSelectCategoryUpdates, fire.Reply, id+2, "selectcategoryupdates_view_reply")
		x.expect(ComponentRooms, NotifyRoomCategoryAdded, fire.Notification, 0, "roomcategoryadded_notification")
		x.expect(ComponentRooms, NotifyRoomCategoryAdded, fire.Notification, 0, "roomcategoryadded_open_notification")
		popu := 0
		if x != ps {
			popu = 1
		}
		if f := x.next("RoomAdded General"); f.Command != NotifyRoomAdded || !bytes.Equal(f.Payload, RoomDataNotification(general, popu)) {
			t.Errorf("RoomAdded General: %#x %x", f.Command, f.Payload)
		}
		x.expect(ComponentRooms, NotifyRoomAdded, fire.Notification, 0, "roomadded_second_notification")
		x.expect(ComponentRooms, NotifyRoomAdded, fire.Notification, 0, "roomadded_open_notification")
	}
	ps.loginAs("player@example.com", 0)
	enter(ps, 2)
	tdfcheck.Require(t, "Blaze::Rooms::JoinRoomRequest", ps.request("joinroom_request"))
	ps.send(ComponentRooms, CmdRoomsJoinRoom, 5, ps.request("joinroom_request"))
	ps.expect(ComponentRooms, CmdRoomsJoinRoom, fire.Reply, 5, "joinroom_reply")
	updated := func(x *pipeSession, popu int) { // every subscriber's tab, a member's too, gets the count
		t.Helper()
		if f := x.next("RoomUpdated"); f.Command != NotifyRoomUpdated || !bytes.Equal(f.Payload, RoomDataNotification(general, popu)) {
			t.Errorf("RoomUpdated %d: %#x %x", popu, f.Command, f.Payload)
		}
	}
	updated(ps, 1)

	two := ps.another()
	two.loginAs("second@example.com", 0)
	enter(two, 2)
	two.send(ComponentRooms, CmdRoomsJoinRoom, 5, ps.request("joinroom_request"))
	two.expectUserAdded(store.FirstPersonaID, true)
	two.expect(ComponentRooms, CmdRoomsJoinRoom, fire.Reply, 5, "joinroom_second_reply")
	two.expect(ComponentRooms, NotifyRoomMemberJoined, fire.Notification, 0, "roommemberjoined_notification")
	ps.expectUserAdded(second, true)
	ps.expect(ComponentRooms, NotifyRoomMemberJoined, fire.Notification, 0, "roommemberjoined_second_notification")
	updated(ps, 2)
	updated(two, 2)

	// a line: the sender's copy after the reply, the other's pushed after it
	ps.send(ComponentMessaging, CmdMsgSendMessage, 6, ps.request("sendmessage_room_request"))
	if f := ps.next("sendMessage reply"); f.Type != fire.Reply || !bytes.Equal(f.Payload, SendMessageReply(9, nil)) {
		t.Errorf("room line's reply: %s %x", f.Type, f.Payload)
	}
	ps.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_room_notification")
	two.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_room_notification")

	// the second leaves: its reply, the first's RoomMemberLeft, then both tabs get the count
	two.send(ComponentRooms, CmdRoomsLeaveRoom, 6, ps.request("leaveroom_request"))
	two.expect(ComponentRooms, CmdRoomsLeaveRoom, fire.Reply, 6, "leaveroom_reply")
	ps.expect(ComponentRooms, NotifyRoomMemberLeft, fire.Notification, 0, "roommemberleft_notification")
	updated(ps, 1)
	updated(two, 1)
	two.send(ComponentRooms, CmdRoomsToggleJoinedRoomNotifications, 7, ps.request("togglejoinedroomnotifications_request"))
	two.expect(ComponentRooms, CmdRoomsToggleJoinedRoomNotifications, fire.Reply, 7, "")

	// the first's connection ends: the second's tab gets the empty room
	ps.client.Close()
	if f := two.next("RoomUpdated"); f.Command != NotifyRoomUpdated || !bytes.Equal(f.Payload, RoomDataNotification(general, 0)) {
		t.Errorf("after the close: %#x %x", f.Command, f.Payload)
	}
	<-ps.done
	two.close()
	log := ps.log.String()
	for _, want := range []string{
		`[blaze]    rooms: view 1, 3 rooms: 1 "General" 0/50, 2 "Looking for group" 0/50, 3 "Matchmaking" 0/50 (category 1000)` + "\n",
		`[blaze]    room 1 "General": 2000000001 "player" joined (1/50)` + "\n",
		`[blaze]    room 1 "General": 2000000002 "second" joined (2/50)` + "\n",
		"[blaze]    message 9 from 2000000001 \"player\" (chat 0x2, flags 0x6, to room [1]): delivered to [2000000001 2000000002], stored for []\n",
		`[blaze]    room 1 "General": 2000000002 "second" left (1/50)` + "\n",
		`[blaze]    room 1 "General": 2000000001 "player" dropped (session ended) (0/50)` + "\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func (tc *tcpClient) roomsCall(cmd uint16, payload []byte) fire.Frame {
	tc.t.Helper()
	return tc.reply(ComponentRooms, cmd, tc.send(ComponentRooms, cmd, payload))
}

func (tc *tcpClient) joinRoom(id int64) fire.Frame {
	tc.t.Helper()
	return tc.joinRoomIn(RoomCategoryID, id)
}

// joinRoomIn sends joinRoom with the category ctid (the one the client was told for the room).
func (tc *tcpClient) joinRoomIn(ctid, id int64) fire.Frame {
	tc.t.Helper()
	return tc.roomsCall(CmdRoomsJoinRoom, tdf.NewWriter().Int("CTID", ctid).Int("INID", 0).Int("INVT", 0).
		String("PASS", "").String("PVAL", "").Int("RMID", id).Bytes())
}

// roomNote waits for a Rooms notification and returns it decoded (checked against class).
func (tc *tcpClient) roomNote(id uint16, class string) *tdf.Struct {
	tc.t.Helper()
	return payloadOf(tc.t, class, tc.note(ComponentRooms, id))
}

func (tc *tcpClient) subscribe() {
	tc.t.Helper()
	wantOK(tc.t, "view", tc.roomsCall(CmdRoomsSelectCategoryUpdates, tdf.NewWriter().Int("VWID", RoomViewID).Bytes()))
	tc.roomNote(NotifyRoomCategoryAdded, "Blaze::Rooms::RoomCategoryData")
}

func wantPopu(t *testing.T, what string, st *tdf.Struct, rmid int64, popu int64) {
	t.Helper()
	id, _ := st.Int("RMID")
	n, _ := st.Int("POPU")
	if id != rmid || n != popu {
		t.Errorf("%s: room %d POPU %d, want room %d POPU %d", what, id, n, rmid, popu)
	}
}

// Three fake clients: c only watches the list. Joins and leaves move c's count (RoomUpdated) and tell the members
// (RoomMemberJoined, RoomMemberLeft); a line reaches the members only, and only members may send one; the refusals
// (unknown room, a member again, full, unknown view); a disconnect leaves; VWID 0 ends the watching.
func TestRoomsMembersAndCounts(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) {
		s.SetChatRooms([]ChatRoom{{ID: 1, Name: "General", Capacity: 2}, {ID: 2, Name: "Other", Capacity: 50}})
	})
	a, b, c := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com")
	wantOK(t, "selectViewUpdates", c.roomsCall(CmdRoomsSelectViewUpdate, tdf.NewWriter().Int("UPDT", 1).Bytes()))
	c.roomNote(NotifyRoomViewAdded, "Blaze::Rooms::RoomViewData")
	c.subscribe()
	wantPopu(t, "c's list", c.roomNote(NotifyRoomAdded, "Blaze::Rooms::RoomData"), 1, 0)
	wantPopu(t, "c's list", c.roomNote(NotifyRoomAdded, "Blaze::Rooms::RoomData"), 2, 0)
	wantErr(t, "unknown view", c.roomsCall(CmdRoomsSelectCategoryUpdates, tdf.NewWriter().Int("VWID", 2).Bytes()), ErrRoomsUnknownView)

	r := a.joinRoom(1)
	wantOK(t, "a joins", r)
	rdat, _ := payloadOf(t, "Blaze::Rooms::JoinRoomResponse", r).Sub("RDAT")
	wantPopu(t, "a's reply", rdat, 1, 1)
	wantPopu(t, "c after a", c.roomNote(NotifyRoomUpdated, "Blaze::Rooms::RoomData"), 1, 1)
	wantErr(t, "a again", a.joinRoom(1), ErrRoomsAlreadyMember)
	wantErr(t, "no room 9", a.joinRoom(9), ErrRoomsNotFound)

	wantOK(t, "b joins", b.joinRoom(1))
	if m := b.roomNote(NotifyRoomMemberJoined, "Blaze::Rooms::RoomMemberData"); m != nil {
		if id, _ := m.Int("BZID"); id != a.id {
			t.Errorf("b's member %d, want a %d", id, a.id)
		}
	}
	if m := a.roomNote(NotifyRoomMemberJoined, "Blaze::Rooms::RoomMemberData"); m != nil {
		if id, _ := m.Int("BZID"); id != b.id {
			t.Errorf("a's new member %d, want b %d", id, b.id)
		}
	}
	wantPopu(t, "c after b", c.roomNote(NotifyRoomUpdated, "Blaze::Rooms::RoomData"), 1, 2)
	wantErr(t, "c into the full room", c.joinRoom(1), ErrRoomsRoomFull)

	line := msg(MessageTypeChat, MessageFlagEcho|MessageFlagFilter, ObjTypeRoom, roomChatAttrs("hi room"), 1)
	wantOK(t, "b's line", b.sendMsg(line))
	for _, x := range []*tcpClient{a, b} {
		if p := x.msgNote(b); attrOf(t, p, MsgAttrText) != "hi room" {
			t.Errorf("%s: text", x.name)
		}
	}
	c.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	wantErr(t, "c isn't a member", c.sendMsg(line), ErrMessagingTargetNotFound)
	wantErr(t, "no room 9", a.sendMsg(msg(MessageTypeChat, MessageFlagEcho, ObjTypeRoom, roomChatAttrs("x"), 9)),
		ErrMessagingTargetNotFound)

	// a leaves: b hears it, c's count drops; a's own tab isn't subscribed (it never selected the view): nothing
	wantOK(t, "a leaves", a.roomsCall(CmdRoomsLeaveRoom, tdf.NewWriter().Int("RMID", 1).Bytes()))
	if m := b.roomNote(NotifyRoomMemberLeft, "Blaze::Rooms::RoomMemberRemoved"); m != nil {
		if id, _ := m.Int("MBID"); id != a.id {
			t.Errorf("left %d, want a %d", id, a.id)
		}
	}
	wantPopu(t, "c after a left", c.roomNote(NotifyRoomUpdated, "Blaze::Rooms::RoomData"), 1, 1)
	wantOK(t, "a leaves again (not a member: still OK)", a.roomsCall(CmdRoomsLeaveRoom, tdf.NewWriter().Int("RMID", 1).Bytes()))
	a.none(ComponentRooms, NotifyRoomMemberLeft, 20*time.Millisecond)

	// c joins now, then b's connection ends: c hears it as a member
	wantOK(t, "c joins", c.joinRoom(1))
	c.roomNote(NotifyRoomMemberJoined, "Blaze::Rooms::RoomMemberData")
	wantPopu(t, "c's own join", c.roomNote(NotifyRoomUpdated, "Blaze::Rooms::RoomData"), 1, 2) // a member's tab too
	b.roomNote(NotifyRoomMemberJoined, "Blaze::Rooms::RoomMemberData")
	b.c.Close()
	if m := c.roomNote(NotifyRoomMemberLeft, "Blaze::Rooms::RoomMemberRemoved"); m != nil {
		if id, _ := m.Int("MBID"); id != b.id {
			t.Errorf("dropped %d, want b %d", id, b.id)
		}
	}
	wantPopu(t, "c after b's close", c.roomNote(NotifyRoomUpdated, "Blaze::Rooms::RoomData"), 1, 1)
	// c stops watching (VWID 0, as at the logout): a's join of Other doesn't reach it
	wantOK(t, "VWID 0", c.roomsCall(CmdRoomsSelectCategoryUpdates, tdf.NewWriter().Int("VWID", 0).Bytes()))
	wantOK(t, "a joins Other", a.joinRoom(2))
	c.none(ComponentRooms, NotifyRoomUpdated, 50*time.Millisecond)
	if ids, _ := ts.s.rooms.memberIDs(1); len(ids) != 1 || ids[0] != c.id {
		t.Errorf("General's members %v, want [c]", ids)
	}
}

// No rooms configured: selectViewUpdates sends no view, a view's selectCategoryUpdates is refused, VWID 0 is answered.
func TestRoomsNone(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) { s.SetChatRooms(nil) })
	a := ts.client("alpha@example.com")
	wantOK(t, "selectViewUpdates", a.roomsCall(CmdRoomsSelectViewUpdate, tdf.NewWriter().Int("UPDT", 1).Bytes()))
	a.none(ComponentRooms, NotifyRoomViewAdded, 50*time.Millisecond)
	wantErr(t, "view 1", a.roomsCall(CmdRoomsSelectCategoryUpdates, tdf.NewWriter().Int("VWID", 1).Bytes()), ErrRoomsUnknownView)
	wantOK(t, "VWID 0", a.roomsCall(CmdRoomsSelectCategoryUpdates, tdf.NewWriter().Int("VWID", 0).Bytes()))
}

// The queue room: its joiner learns the announcer as a user and gets the open queue's line; the news follow the
// queue (a line per change of what it shows, not per tick; a match start; the queue closed), and only members get
// them.
func TestRoomsQueueNews(t *testing.T) {
	ts := newTCPServer(t, nil)
	s := ts.s
	now := time.Unix(testTime, 0)
	s.Now = func() time.Time { return now }
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	queue := s.ChatRooms()[2]
	if !queue.Queue {
		t.Fatalf("room 3: %+v", queue)
	}
	announcer := &tcpClient{name: QueueAnnouncerName, id: QueueAnnouncerID}
	view := func(players int, startsIn time.Duration, names ...string) QueueView {
		st := matchmaker.QueueState{Players: players, Capacity: 10}
		if startsIn > 0 {
			st.StartsAt = now.Add(startsIn)
		}
		for i, n := range names {
			st.Tickets = append(st.Tickets, matchmaker.TicketState{ID: int64(i + 1), Players: []matchmaker.Player{{ID: int64(i), Name: n}}})
		}
		return QueueView{State: st, Bots: true}
	}
	text := func(x *tcpClient) string {
		t.Helper()
		return attrOf(t, x.msgNote(announcer), MsgAttrText)
	}

	s.roomQueueNews(view(1, 0, "Alice"), nil) // nobody in the room yet: kept as the open line
	wantOK(t, "a joins the queue room", a.joinRoom(queue.ID))
	added := payloadOf(t, "Blaze::NotifyUserAdded", a.note(ComponentUserSessions, NotifyUserAdded))
	if user, _ := added.Sub("USER"); user == nil {
		t.Fatal("announcer's UserAdded without USER")
	} else if id, _ := user.Int("ID"); id != QueueAnnouncerID {
		t.Errorf("UserAdded for %d", id)
	} else if name, _ := user.Str("NAME"); name != QueueAnnouncerName {
		t.Errorf("announcer's name %q", name)
	}
	if got := text(a); got != "Alice is queuing for PvP: 1/10, waiting for more players" {
		t.Errorf("joiner's line %q", got)
	}

	s.roomQueueNews(view(2, 90*time.Second, "Alice", "Bob"), nil)
	if got := text(a); got != "Alice and Bob are queuing for PvP: 2/10, starts in 1:30, bots fill the open seats" {
		t.Errorf("line %q", got)
	}
	now = now.Add(10 * time.Second) // a tick that changed nothing: no line
	s.roomQueueNews(view(2, 80*time.Second, "Alice", "Bob"), nil)
	a.none(ComponentMessaging, NotifyMessageID, 30*time.Millisecond)
	s.roomQueueNews(view(0, 0), []MatchStarted{{Players: 2, Bots: 8}})
	if got := text(a); got != "PvP match started: 2 players + 8 bots" {
		t.Errorf("start %q", got)
	}
	a.none(ComponentMessaging, NotifyMessageID, 30*time.Millisecond) // no "closed" after a start
	s.roomQueueNews(view(1, 45*time.Second, "Carol"), nil)
	if got := text(a); got != "Carol is queuing for PvP: 1/10, starts in 45 s, bots fill the open seats" {
		t.Errorf("line %q", got)
	}
	s.roomQueueNews(view(0, 0), nil)
	if got := text(a); got != "PvP queue closed" {
		t.Errorf("closed %q", got)
	}
	b.none(ComponentMessaging, NotifyMessageID, 30*time.Millisecond)

	// lines in the queue room from members still work, and a second join knows the announcer already
	wantOK(t, "b joins", b.joinRoom(queue.ID))
	b.note(ComponentUserSessions, NotifyUserAdded) // a
	b.note(ComponentUserSessions, NotifyUserAdded) // the announcer
	b.none(ComponentMessaging, NotifyMessageID, 30*time.Millisecond)
}

// ROOMS3: the open room's join as the mod sends it (the golden request, CTID 1000), against the goldens: the announcer
// as a user before the reply (Matchmaking has the queue news), the reply with CDAT and RDAT in category 1000, the
// joiner's own tab's RoomUpdated in 1000, then the window's close: the leave's reply with CTID 1000.
func TestROOMS3OpenRoomGoldenSession(t *testing.T) {
	ps := newPipeSessionWith(t, nil)
	mm := ps.srv.ChatRooms()[2]
	ps.loginAs("player@example.com", 0)
	ps.send(ComponentRooms, CmdRoomsSelectCategoryUpdates, 2, ps.request("selectcategoryupdates_view_request"))
	ps.expect(ComponentRooms, CmdRoomsSelectCategoryUpdates, fire.Reply, 2, "selectcategoryupdates_view_reply")
	ps.expect(ComponentRooms, NotifyRoomCategoryAdded, fire.Notification, 0, "roomcategoryadded_notification")
	ps.expect(ComponentRooms, NotifyRoomCategoryAdded, fire.Notification, 0, "roomcategoryadded_open_notification")
	ps.expect(ComponentRooms, NotifyRoomAdded, fire.Notification, 0, "roomadded_notification")
	ps.expect(ComponentRooms, NotifyRoomAdded, fire.Notification, 0, "roomadded_second_notification")
	ps.expect(ComponentRooms, NotifyRoomAdded, fire.Notification, 0, "roomadded_open_notification")

	tdfcheck.Require(t, "Blaze::Rooms::JoinRoomRequest", ps.request("joinroom_open_request"))
	ps.send(ComponentRooms, CmdRoomsJoinRoom, 3, ps.request("joinroom_open_request"))
	ps.expectUserAdded(QueueAnnouncerID, true)
	ps.expect(ComponentRooms, CmdRoomsJoinRoom, fire.Reply, 3, "joinroom_open_reply")
	if f := ps.next("RoomUpdated"); f.Command != NotifyRoomUpdated || !bytes.Equal(f.Payload, RoomDataNotification(mm, 1)) {
		t.Errorf("RoomUpdated: %#x %x", f.Command, f.Payload)
	}
	ps.send(ComponentRooms, CmdRoomsLeaveRoom, 4, tdf.NewWriter().Int("RMID", mm.ID).Bytes())
	ps.expect(ComponentRooms, CmdRoomsLeaveRoom, fire.Reply, 4, "leaveroom_open_reply")
	if f := ps.next("RoomUpdated"); f.Command != NotifyRoomUpdated || !bytes.Equal(f.Payload, RoomDataNotification(mm, 0)) {
		t.Errorf("RoomUpdated after the leave: %#x %x", f.Command, f.Payload)
	}
	log := ps.close()
	for _, want := range []string{
		`[blaze]    room 3 "Matchmaking": 2000000001 "player" joined (1/50)` + "\n",
		`[blaze]    room 3 "Matchmaking": 2000000001 "player" left (0/50)` + "\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "the join named category") {
		t.Errorf("the golden join's category was taken for another:\n%s", log)
	}
}

// ROOMS3's categories: only those that hold a room are sent, in CTID order and before the rooms; a join naming
// another category than the room's still joins (logged), and every RoomData of an open room says 1000.
func TestRoomsAutoOpenCategories(t *testing.T) {
	enter := func(rooms string) (*pipeSession, []fire.Frame) {
		t.Helper()
		ps := newPipeSessionWith(t, func(s *Server) {
			rs, err := ParseChatRooms(rooms)
			if err != nil {
				t.Fatal(err)
			}
			s.SetChatRooms(rs)
		})
		ps.loginAs("player@example.com", 0)
		ps.send(ComponentRooms, CmdRoomsSelectCategoryUpdates, 2, tdf.NewWriter().Int("VWID", RoomViewID).Bytes())
		if f := ps.next("view reply"); f.Type != fire.Reply {
			t.Fatalf("%s: view reply %s", rooms, f.Type)
		}
		var frames []fire.Frame
		for range len(ps.srv.ChatRooms()) + len(roomCategoriesOf(ps.srv.ChatRooms())) {
			frames = append(frames, ps.next("category or room"))
		}
		return ps, frames
	}
	for rooms, want := range map[string][]int64{
		"a, oopen:open, last": {RoomCategoryID, AutoOpenCategoryID},
		"a, b, last":          {RoomCategoryID},
		"oa:open, olast:open": {AutoOpenCategoryID},
		"oa:open:queue, last": {RoomCategoryID, AutoOpenCategoryID},
	} {
		ps, frames := enter(rooms)
		var cats []int64
		roomsSeen := 0
		for _, f := range frames {
			switch f.Command {
			case NotifyRoomCategoryAdded:
				if roomsSeen > 0 {
					t.Errorf("%s: a category after a room", rooms)
				}
				ctid, _ := payloadOf(t, "Blaze::Rooms::RoomCategoryData", f).Int("CTID")
				cats = append(cats, ctid)
			case NotifyRoomAdded:
				roomsSeen++
				st := payloadOf(t, "Blaze::Rooms::RoomData", f)
				ctid, _ := st.Int("CTID")
				name, _ := st.Str("NAME")
				want := int64(RoomCategoryID)
				if strings.HasPrefix(name, "o") {
					want = AutoOpenCategoryID
				}
				if ctid != want {
					t.Errorf("%s: room %q in %d, want %d", rooms, name, ctid, want)
				}
			default:
				t.Errorf("%s: unexpected %#x", rooms, f.Command)
			}
		}
		if !slices.Equal(cats, want) {
			t.Errorf("%s: categories %v, want %v", rooms, cats, want)
		}
		ps.close()
	}

	// the open room joined with the category it was told, left, then joined with the other one (a stale list): both
	// join, every RoomData says 1000, the leave's reply too; General's leave says 1
	ps, _ := enter("a, oopen:open, last")
	ctidOf := func(st *tdf.Struct, what string, want int64) {
		t.Helper()
		if c, _ := st.Int("CTID"); c != want {
			t.Errorf("%s: CTID %d, want %d", what, c, want)
		}
	}
	reply := func(what string) *tdf.Struct {
		t.Helper()
		f := ps.next(what)
		if f.Type != fire.Reply {
			t.Fatalf("%s: %s", what, f.Type)
		}
		class := "Blaze::Rooms::JoinRoomResponse"
		if f.Command == CmdRoomsLeaveRoom {
			class = "Blaze::Rooms::LeaveRoomResponse"
		}
		return payloadOf(t, class, f)
	}
	updated := func(what string) {
		t.Helper()
		f := ps.next(what)
		if f.Command != NotifyRoomUpdated {
			t.Fatalf("%s: %#x", what, f.Command)
		}
		ctidOf(payloadOf(t, "Blaze::Rooms::RoomData", f), what, AutoOpenCategoryID)
	}
	join := func(id uint32, ctid int64) {
		ps.send(ComponentRooms, CmdRoomsJoinRoom, id, tdf.NewWriter().Int("CTID", ctid).Int("INID", 0).Int("INVT", 0).
			String("PASS", "").String("PVAL", "").Int("RMID", 2).Bytes())
	}
	join(3, AutoOpenCategoryID)
	r := reply("join")
	cdat, _ := r.Sub("CDAT")
	rdat, _ := r.Sub("RDAT")
	ctidOf(cdat, "CDAT", AutoOpenCategoryID)
	ctidOf(rdat, "RDAT", AutoOpenCategoryID)
	updated("RoomUpdated after the join")
	ps.send(ComponentRooms, CmdRoomsLeaveRoom, 4, tdf.NewWriter().Int("RMID", 2).Bytes())
	ctidOf(reply("leave"), "LeaveRoomResponse", AutoOpenCategoryID)
	updated("RoomUpdated after the leave")
	join(5, RoomCategoryID)
	rdat, _ = reply("join with category 1").Sub("RDAT")
	ctidOf(rdat, "RDAT of the join with category 1", AutoOpenCategoryID)
	updated("RoomUpdated after the second join")
	ps.send(ComponentRooms, CmdRoomsLeaveRoom, 6, tdf.NewWriter().Int("RMID", 1).Bytes())
	ctidOf(reply("General's leave"), "General's LeaveRoomResponse", RoomCategoryID)
	if log := ps.close(); !strings.Contains(log, "room 2: the join named category 1, the room's is 1000") ||
		strings.Count(log, "the join named category") != 1 {
		t.Errorf("the category mismatch line:\n%s", log)
	}
}

// roomCategoriesOf is roomCategories for configured rooms.
func roomCategoriesOf(rooms []ChatRoom) []int64 {
	pops := make([]roomPop, len(rooms))
	for i, r := range rooms {
		pops[i] = roomPop{ChatRoom: r}
	}
	return roomCategories(pops)
}
