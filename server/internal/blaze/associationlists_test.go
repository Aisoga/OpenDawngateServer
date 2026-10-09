package blaze

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// clientLists are the lists the client asks for after enterService (a5b_run6d: types 2, 4 and 1 with FLGS 1, 0 and
// 0x15), as the server describes them.
func clientLists() []ListInfo {
	var out []ListInfo
	for _, l := range []ListInfo{{Type: ListTypeRecentPlayers, Flags: 1}, {Type: ListTypeBlock},
		{Type: ListTypeFriends, Flags: 0x15}} {
		out = append(out, DescribeList(l))
	}
	return out
}

// emptyLists is getLists' reply content for an account without members (B13's reply).
func emptyLists() []ListContent {
	var out []ListContent
	for _, l := range clientLists() {
		out = append(out, ListContent{Info: l})
	}
	return out
}

// B13: the live request (rebuilt from a5b_run6d's log, 137 bytes as logged) decodes to the three lists, and the
// reply (an empty list of each) matches tdf_golden.py's independent writer and the client's member table.
func TestGetListsReplyMatchesGolden(t *testing.T) {
	g := golden.Load(t)
	var raw []byte
	for _, c := range g.Decode {
		if c.Name == "getlists_request" {
			raw = golden.Bytes(t, c.Hex)
		}
	}
	if len(raw) != 137 {
		t.Fatalf("getlists_request: %d bytes, the client sent 137", len(raw))
	}
	tdfcheck.Require(t, "Blaze::Association::GetListsRequest", raw)
	body, err := tdf.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []ListInfo{{Type: 2, Flags: 1}, {Type: 4}, {Type: 1, Flags: 0x15}}
	if got := RequestedLists(body); !reflect.DeepEqual(got, want) {
		t.Errorf("requested lists\n got %+v\nwant %+v", got, want)
	}

	got := GetListsReply(emptyLists(), 0)
	tdfcheck.Require(t, "Blaze::Association::Lists", got)
	if h, want := hex.EncodeToString(got), g.WriterCase(t, "getlists_reply").Hex; h != want {
		t.Errorf("getlists_reply\n got %s\nwant %s", h, want)
	}
}

// The client keys its lists by type, object id and name (AssociationListAPI_CreateOrUpdateList 0x00C9A010), so the
// server gives each list its own name and object id unless the request names them; the rest stays as asked.
func TestDescribeList(t *testing.T) {
	seenName, seenID := map[string]bool{}, map[tdf.ObjID]bool{}
	for _, l := range clientLists() {
		if l.Name == "" || seenName[l.Name] || seenID[l.ObjectID] {
			t.Errorf("list %d: name %q, object id %v not unique", l.Type, l.Name, l.ObjectID)
		}
		seenName[l.Name], seenID[l.ObjectID] = true, true
		if l.ObjectID != (tdf.ObjID{Component: 0x19, Type: 1, ID: l.Type}) {
			t.Errorf("list %d: object id %v", l.Type, l.ObjectID)
		}
	}
	asked := ListInfo{ObjectID: tdf.ObjID{Component: 0x19, Type: 1, ID: 77}, Flags: 4, Name: "mine", Type: 1,
		MaxSize: 200, PairID: 3}
	if got := DescribeList(asked); got != asked {
		t.Errorf("a named list changed: %+v", got)
	}
	if got := DescribeList(ListInfo{Type: 9}); got.Name != "list9" || got.ObjectID.ID != 9 {
		t.Errorf("unknown type: %+v", got)
	}
}

// B13b fixture: the accounts player (2000000001), second (2000000002) and third (2000000003); player's friend and
// recent player is second (who has player as a friend), its blocked user third, all added at testTime.
const thirdPID = store.FirstPersonaID + 2

func b13bAccounts(t *testing.T, st *store.Store, lists bool) {
	t.Helper()
	at := time.Unix(testTime, 0)
	var ids []int64
	for _, mail := range []string{"player@example.com", "second@example.com", "third@example.com"} {
		a, _, err := st.Login(mail, at)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.UserID)
	}
	if !lists {
		return
	}
	second := store.ListMember{ID: store.FirstPersonaID + 1, Name: "second", Added: at}
	if _, _, err := st.UpdatePair(ids[0], ids[1], func(a, b *store.Account) {
		a.Lists = store.Lists{Friends: []store.ListMember{second}, Recent: []store.ListMember{second},
			Blocked: []store.ListMember{{ID: thirdPID, Name: "third", Added: at}}}
		b.Lists.Friends = []store.ListMember{{ID: store.FirstPersonaID, Name: "player", Added: at}}
	}); err != nil {
		t.Fatal(err)
	}
}

// B13b: every reply and notification matches tdf_golden.py's independent writer and the client's member table.
func TestB13bRepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	at := time.Unix(testTime, 0)
	secondAcct := store.Account{UserID: store.FirstUserID + 1}
	second := store.Persona{ID: store.FirstPersonaID + 1, Name: "second"}
	secondMember := store.ListMember{ID: second.ID, Name: second.Name, Added: at}
	player := store.ListMember{ID: store.FirstPersonaID, Name: "player", Added: at}
	members := map[int64][]store.ListMember{ListTypeRecentPlayers: {secondMember},
		ListTypeBlock: {{ID: thirdPID, Name: "third", Added: at}}, ListTypeFriends: {secondMember}}
	var lists []ListContent
	for _, l := range clientLists() {
		lists = append(lists, ListContent{Info: l, Members: members[l.Type], Total: len(members[l.Type])})
	}
	for _, c := range []struct {
		name, class string
		got         []byte
	}{
		{"getlists_members_reply", "Blaze::Association::Lists", GetListsReply(lists, 0)},
		{"useradded_notification", "Blaze::NotifyUserAdded", UserAddedNotification(secondAcct, second, 0)},
		{"userupdated_online_notification", "Blaze::UserStatus", UserUpdatedNotification(second.ID, true)},
		{"userupdated_offline_notification", "Blaze::UserStatus", UserUpdatedNotification(second.ID, false)},
		{"presence_notification", "Blaze::UserSessionExtendedDataUpdate", PresenceNotification(secondAcct, second, 0x10001)},
		{"adduserstolist_friend_reply", "Blaze::Association::UpdateListMembersResponse",
			UpdateListMembersReply([]store.ListMember{secondMember}, nil)},
		{"removeusersfromlist_friend_reply", "Blaze::Association::UpdateListMembersResponse",
			UpdateListMembersReply(nil, []store.ListMember{{ID: second.ID, Name: second.Name}})},
		{"notifyupdatelistmembership_add_notification", "Blaze::Association::UpdateListWithMembersRequest",
			ListMembershipNotification(ListTypeFriends, []store.ListMember{player}, ListUpdateAdd)},
		{"notifyupdatelistmembership_remove_notification", "Blaze::Association::UpdateListWithMembersRequest",
			ListMembershipNotification(ListTypeFriends, []store.ListMember{{ID: player.ID, Name: player.Name}}, ListUpdateRemove)},
	} {
		tdfcheck.Require(t, c.class, c.got)
		if h, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; h != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, h, want)
		}
	}
	// the client requests rebuilt from the static reading fit their classes
	for name, class := range map[string]string{
		"adduserstolist_friend_request":                "Blaze::Association::UpdateListMembersRequest",
		"removeusersfromlist_friend_request":           "Blaze::Association::UpdateListMembersRequest",
		"adduserstolist_blockbyname_request":           "Blaze::Association::UpdateListMembersRequest",
		"removeusersfromlist_unblockbyname_request":    "Blaze::Association::UpdateListMembersRequest",
		"sendmessage_friendrequest_request":            "Blaze::Messaging::ClientMessage",
		"sendmessage_friendaccepted_request":           "Blaze::Messaging::ClientMessage",
		"updateextendeddataattribute_presence_request": "Blaze::UpdateUserSessionAttributeRequest",
	} {
		tdfcheck.Require(t, class, goldenRequest(t, g, name))
	}
	if refs := requestedMembers(decodeBytes(t, goldenRequest(t, g, "adduserstolist_blockbyname_request"))); !reflect.DeepEqual(refs,
		[]memberRef{{Name: "third"}}) {
		t.Errorf("block by name: %+v", refs)
	}
}

func goldenRequest(t *testing.T, g *golden.Data, name string) []byte {
	t.Helper()
	for _, c := range g.Decode {
		if c.Name == name {
			return golden.Bytes(t, c.Hex)
		}
	}
	t.Fatalf("no decode case %q", name)
	return nil
}

func decodeBytes(t *testing.T, b []byte) *tdf.Struct {
	t.Helper()
	st, err := tdf.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// The login's getLists and a game's loading step as the client runs them, for an account without lists:
// getLists gets an empty list of each requested type, and addUsersToList for the recent players list (a practice
// game's empty one, then one with a user the server doesn't know) changes nothing and gets the empty
// UpdateListMembersResponse.
func TestAssociationListsSession(t *testing.T) {
	ps := newPipeSession(t)
	ps.loginAs("player@example.com", 0)
	ps.send(ComponentAssociationLists, CmdALGetLists, 2, ps.request("getlists_request"))
	ps.expect(ComponentAssociationLists, CmdALGetLists, fire.Reply, 2, "getlists_reply")
	for i, name := range []string{"adduserstolist_empty_request", "adduserstolist_request"} {
		tdfcheck.Require(t, "Blaze::Association::UpdateListMembersRequest", ps.request(name))
		ps.send(ComponentAssociationLists, CmdALAddUsersToList, uint32(3+i), ps.request(name))
		ps.expectPayload(ComponentAssociationLists, CmdALAddUsersToList, fire.Reply, uint32(3+i), UpdateListMembersReply(nil, nil))
	}
	log := ps.close()
	for _, want := range []string{
		`getLists: 3 lists (2 "recentPlayerList", 4 "blockList", 1 "friendList"), all empty`,
		"-> getLists reply, 221 bytes:\n    LMAP: \n",
		`addUsersToList list 2 "recentPlayerList" users []: nothing to do`,
		`addUsersToList list 2 "recentPlayerList" users [2000000002]: skipped [2000000002 (unknown user)]`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "not answered") {
		t.Errorf("a request went unanswered:\n%s", log)
	}
}

// B13b: an account's lists come back at its next login, and every member is a known user before the reply: UserAdded
// and UserUpdated (offline here) once per user, in the lists' order (second for the recent players and the friends,
// third for the block list).
func TestGetListsWithMembers(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) { b13bAccounts(t, s.Store, true) })
	ps.loginAs("player@example.com", 0)
	ps.send(ComponentAssociationLists, CmdALGetLists, 2, ps.request("getlists_request"))
	ps.expect(ComponentUserSessions, NotifyUserAdded, fire.Notification, 0, "useradded_notification")
	ps.expect(ComponentUserSessions, NotifyUserUpdated, fire.Notification, 0, "userupdated_offline_notification")
	f := ps.next("third's UserAdded")
	third := payloadOf(t, "Blaze::NotifyUserAdded", f)
	if user, _ := third.Sub("USER"); f.Command != NotifyUserAdded || user == nil {
		t.Fatalf("third's UserAdded: %#x %+v", f.Command, third)
	} else if id, _ := user.Int("ID"); id != thirdPID {
		t.Errorf("UserAdded for %d, want third", id)
	}
	f = ps.next("third's UserUpdated")
	if st := payloadOf(t, "Blaze::UserStatus", f); f.Command != NotifyUserUpdated {
		t.Errorf("third's UserUpdated: %#x", f.Command)
	} else if flags, _ := st.Int("FLGS"); flags != UserStatusKnown {
		t.Errorf("third's status %#x, want offline", flags)
	}
	ps.expect(ComponentAssociationLists, CmdALGetLists, fire.Reply, 2, "getlists_members_reply")
	log := ps.close()
	if want := `getLists: 3 lists (2 "recentPlayerList": 1, 4 "blockList": 1, 1 "friendList": 1 members)`; !strings.Contains(log, want) {
		t.Errorf("log lacks %q:\n%s", want, log)
	}
}

// B13b: a friend request (the Messaging message TYPE 8), its acceptance (addUsersToList for the friends list with
// MUTA 1, then the accept message), the friend's presence, its logout and login, and the unfriend (removeUsersFromList
// with MUTA 1), between two clients on one server. The other side learns of each friendship change through UserAdded
// and NotifyUpdateListMembership, and of each presence change through its friend's extended data.
func TestFriendRequestAcceptPresenceAndUnfriend(t *testing.T) {
	g := golden.Load(t)
	ts := newTCPServer(t, func(s *Server) { s.Now = func() time.Time { return time.Unix(testTime, 0) } })
	player := ts.client("player@example.com")
	second := ts.client("second@example.com")
	payloadIs := func(f fire.Frame, name string) {
		t.Helper()
		if got, want := hex.EncodeToString(f.Payload), g.WriterCase(t, name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", name, got, want)
		}
	}
	for _, c := range []*tcpClient{player, second} {
		id := c.send(ComponentAssociationLists, CmdALGetLists, goldenRequest(t, g, "getlists_request"))
		payloadIs(c.reply(ComponentAssociationLists, CmdALGetLists, id), "getlists_reply")
	}

	// second asks player; player gets the request (a stored message)
	id := second.send(ComponentMessaging, CmdMsgSendMessage, goldenRequest(t, g, "sendmessage_friendrequest_request"))
	second.reply(ComponentMessaging, CmdMsgSendMessage, id)
	if pyld, _ := decode(t, player.note(ComponentMessaging, NotifyMessageID)).Sub("PYLD"); pyld == nil {
		t.Fatal("no friend request")
	} else if typ, _ := pyld.Int("TYPE"); typ != MessageTypeFriendRequest {
		t.Fatalf("message type %d", typ)
	}

	// a third user without a request can't make itself second's friend
	third := ts.client("third@example.com")
	id = third.send(ComponentAssociationLists, CmdALAddUsersToList, goldenRequest(t, g, "adduserstolist_friend_request"))
	if f := third.reply(ComponentAssociationLists, CmdALAddUsersToList, id); f.Type != fire.ErrorReply || f.Error != ErrALInvalidUser {
		t.Errorf("add without a request: %s error %#x", f.Type, f.Error)
	}

	// player accepts: second is announced, then the reply; second hears of player, then of the list change
	id = player.send(ComponentAssociationLists, CmdALAddUsersToList, goldenRequest(t, g, "adduserstolist_friend_request"))
	payloadIs(player.reply(ComponentAssociationLists, CmdALAddUsersToList, id), "adduserstolist_friend_reply")
	added := payloadOf(t, "Blaze::NotifyUserAdded", player.note(ComponentUserSessions, NotifyUserAdded))
	if user, _ := added.Sub("USER"); user == nil {
		t.Fatal("UserAdded without USER")
	} else if name, _ := user.Str("NAME"); name != "second" {
		t.Errorf("UserAdded for %q", name)
	}
	payloadIs(player.note(ComponentUserSessions, NotifyUserUpdated), "userupdated_online_notification")
	payloadOf(t, "Blaze::NotifyUserAdded", second.note(ComponentUserSessions, NotifyUserAdded))
	second.note(ComponentUserSessions, NotifyUserUpdated)
	payloadIs(second.note(ComponentAssociationLists, NotifyUpdateListMembership), "notifyupdatelistmembership_add_notification")
	id = player.send(ComponentMessaging, CmdMsgSendMessage, goldenRequest(t, g, "sendmessage_friendaccepted_request"))
	player.reply(ComponentMessaging, CmdMsgSendMessage, id)
	second.note(ComponentMessaging, NotifyMessageID)
	friends := func(c *tcpClient) []store.ListMember {
		a, _, _ := ts.s.Store.PersonaByID(c.id)
		return a.Lists.Friends
	}
	if p, s := friends(player), friends(second); len(p) != 1 || p[0].Name != "second" ||
		len(s) != 1 || s[0].Name != "player" {
		t.Fatalf("friends: player %+v, second %+v", p, s)
	}
	// accepting again: already a friend
	id = player.send(ComponentAssociationLists, CmdALAddUsersToList, goldenRequest(t, g, "adduserstolist_friend_request"))
	if f := player.reply(ComponentAssociationLists, CmdALAddUsersToList, id); f.Type != fire.ErrorReply || f.Error != ErrALMemberAlreadyInList {
		t.Errorf("second accept: %s error %#x", f.Type, f.Error)
	}

	// second's presence reaches player; its logout shows it offline (presence 0, then the status)
	id = second.send(ComponentUserSessions, CmdUserSessionsUpdateExtendedDataAttribute,
		goldenRequest(t, g, "updateextendeddataattribute_presence_request"))
	second.reply(ComponentUserSessions, CmdUserSessionsUpdateExtendedDataAttribute, id)
	payloadIs(player.note(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate), "presence_notification")
	second.c.Close()
	offline := decode(t, player.note(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate))
	if data, _ := offline.Sub("DATA"); data == nil {
		t.Error("offline update without DATA")
	} else if f, ok := data.Get("CMAP"); !ok || len(f.Value.(*tdf.Map).Entries) != 1 || f.Value.(*tdf.Map).Entries[0].Value != int64(0) {
		t.Errorf("offline presence %+v", f.Value)
	}
	payloadIs(player.note(ComponentUserSessions, NotifyUserUpdated), "userupdated_offline_notification")

	// second logs in again: player hears it is online; second's lists still have player, announced first
	second = ts.client("second@example.com")
	payloadIs(player.note(ComponentUserSessions, NotifyUserUpdated), "userupdated_online_notification")
	id = second.send(ComponentAssociationLists, CmdALGetLists, goldenRequest(t, g, "getlists_request"))
	lists := payloadOf(t, "Blaze::Association::Lists", second.reply(ComponentAssociationLists, CmdALGetLists, id))
	payloadOf(t, "Blaze::NotifyUserAdded", second.note(ComponentUserSessions, NotifyUserAdded))
	if l, _ := lists.Get("LMAP"); len(l.Value.(*tdf.List).Items) != 3 {
		t.Fatalf("LMAP %+v", l.Value)
	} else if fl := l.Value.(*tdf.List).Items[2].(*tdf.Struct); fl != nil {
		if n, _ := fl.Int("TOCT"); n != 1 {
			t.Errorf("second's friends after a new login: %d", n)
		}
	}

	// player unfriends second: both lists lose the friendship, second hears of it
	id = player.send(ComponentAssociationLists, CmdALRemoveUsersFromList, goldenRequest(t, g, "removeusersfromlist_friend_request"))
	payloadIs(player.reply(ComponentAssociationLists, CmdALRemoveUsersFromList, id), "removeusersfromlist_friend_reply")
	payloadIs(second.note(ComponentAssociationLists, NotifyUpdateListMembership), "notifyupdatelistmembership_remove_notification")
	if p, s := friends(player), friends(second); len(p) != 0 || len(s) != 0 {
		t.Errorf("friends after the unfriend: player %+v, second %+v", p, s)
	}
	id = player.send(ComponentAssociationLists, CmdALRemoveUsersFromList, goldenRequest(t, g, "removeusersfromlist_friend_request"))
	if f := player.reply(ComponentAssociationLists, CmdALRemoveUsersFromList, id); f.Type != fire.ErrorReply || f.Error != ErrALMemberNotFound {
		t.Errorf("second unfriend: %s error %#x", f.Type, f.Error)
	}
	log := ts.log.String()
	for _, want := range []string{
		`message 1 from 2000000002 "second" (friend request 0x8, flags 0x5, to user [2000000001]): delivered to [2000000001], stored for [2000000001]`,
		`addUsersToList list 1 "friendList" users [2000000002]: skipped [2000000002 (no friend request from it)]`,
		`addUsersToList list 1 "friendList" users [2000000002]: added [2000000002 "second"]`,
		`presence of 2000000002 "second": 0x10001`,
		`removeUsersFromList list 1 "friendList" users [2000000002]: removed [2000000002 "second"]`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// B13b: a blocked user's messages are dropped for the user who blocked it (and not stored), whatever the client
// does with them; blocking and unblocking by name, as the chat UI sends them, and their errors.
func TestBlockedUserMessagesDropped(t *testing.T) {
	g := golden.Load(t)
	ts := newTCPServer(t, func(s *Server) { s.Now = func() time.Time { return time.Unix(testTime, 0) } })
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(ts.log.String())
		}
	})
	player := ts.client("player@example.com")
	ts.client("second@example.com")
	third := ts.client("third@example.com")
	request := func(c *tcpClient, cmd uint16, name string) fire.Frame {
		t.Helper()
		return c.reply(ComponentAssociationLists, cmd, c.send(ComponentAssociationLists, cmd, goldenRequest(t, g, name)))
	}
	whisper := func() {
		t.Helper()
		msg := tdf.NewWriter()
		writeClientMessage(msg, ClientMessage{Attrs: []MsgAttr{{MsgAttrText, "hi"}}, Flags: MessageFlagPersistent,
			Targets: []int64{player.id}, TargetType: ObjTypeUser, Type: MessageTypeWhisper})
		if f := third.reply(ComponentMessaging, CmdMsgSendMessage, third.send(ComponentMessaging, CmdMsgSendMessage, msg.Bytes())); f.Type != fire.Reply {
			t.Fatalf("third's message: %s", f.Type)
		}
	}

	f := request(player, CmdALAddUsersToList, "adduserstolist_blockbyname_request")
	reply := payloadOf(t, "Blaze::Association::UpdateListMembersResponse", f)
	if l, _ := reply.Get("LMID"); len(l.Value.(*tdf.List).Items) != 1 {
		t.Fatalf("block reply %+v", reply)
	}
	payloadOf(t, "Blaze::NotifyUserAdded", player.note(ComponentUserSessions, NotifyUserAdded))
	if f := request(player, CmdALAddUsersToList, "adduserstolist_blockbyname_request"); f.Type != fire.ErrorReply || f.Error != ErrALMemberAlreadyInList {
		t.Errorf("blocking twice: %s error %#x", f.Type, f.Error)
	}
	whisper()
	player.none(ComponentMessaging, NotifyMessageID, 200*time.Millisecond)
	if ts.s.msgs.hasMessage(player.id, third.id, MessageTypeWhisper) {
		t.Error("a blocked user's message was stored")
	}

	f = request(player, CmdALRemoveUsersFromList, "removeusersfromlist_unblockbyname_request")
	reply = payloadOf(t, "Blaze::Association::UpdateListMembersResponse", f)
	if l, _ := reply.Get("REM"); len(l.Value.(*tdf.List).Items) != 1 {
		t.Fatalf("unblock reply %+v", reply)
	}
	if f := request(player, CmdALRemoveUsersFromList, "removeusersfromlist_unblockbyname_request"); f.Type != fire.ErrorReply || f.Error != ErrALMemberNotFound {
		t.Errorf("unblocking twice: %s error %#x", f.Type, f.Error)
	}
	whisper()
	player.note(ComponentMessaging, NotifyMessageID)
	log := ts.log.String()
	for _, want := range []string{
		`addUsersToList list 4 "blockList" users ["third"]: added [2000000003 "third"]`,
		`delivered to [], stored for [], dropped for [2000000001] (blocked the sender)`,
		`removeUsersFromList list 4 "blockList" users ["third"]: removed [2000000003 "third"]`,
		`delivered to [2000000001], stored for [2000000001]`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// updateListRequest builds an UpdateListMembersRequest as the client does (every member written).
func updateListRequest(typ int64, muta int64, members ...memberRef) []byte {
	items := make([]*tdf.Writer, 0, len(members))
	for _, m := range members {
		items = append(items, tdf.NewWriter().Int("BLID", m.ID).Blob("EXBB", nil).Int("PLAT", 0).String("PNAM", m.Name).Int("XREF", 0))
	}
	return tdf.NewWriter().Int("BID", 0).List("BIDL", tdf.TypeStruct, items).
		Struct("LID", func(w *tdf.Writer) { w.String("LNM", listName(typ)).Int("TYPE", typ) }).
		Int("MUTA", muta).Int("VALA", 1).Int("VALD", 1).Bytes()
}

// B13b: the recent players list keeps the other humans of the user's last games: a known member is refreshed (moved
// to the end, not added again), the oldest member goes beyond MaxRecentPlayers (the add reply's REM), the user itself
// and unknown users are left out; the errors of the other lists.
func TestRecentPlayersAndListErrors(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) {
		for i := 0; i <= MaxRecentPlayers+1; i++ {
			if _, _, err := s.Store.Login(fmt.Sprintf("p%02d@example.com", i), time.Unix(testTime, 0)); err != nil {
				t.Fatal(err)
			}
		}
	})
	ps.loginAs("player@example.com", 0) // the next persona id after the fixture's
	me := store.FirstPersonaID + MaxRecentPlayers + 2
	id := uint32(2)
	call := func(cmd uint16, payload []byte) fire.Frame {
		t.Helper()
		ps.send(ComponentAssociationLists, cmd, id, payload)
		id++
		for {
			f := ps.next("reply")
			if f.Type != fire.Notification {
				return f
			}
		}
	}
	reply := func(f fire.Frame) (added, removed []int64) {
		t.Helper()
		st := payloadOf(t, "Blaze::Association::UpdateListMembersResponse", f)
		for _, label := range []string{"LMID", "REM"} {
			l, _ := st.Get(label)
			for _, it := range l.Value.(*tdf.List).Items {
				m := it.(*tdf.Struct)
				if label == "LMID" {
					m, _ = m.Sub("LMID")
				}
				blid, _ := m.Int("BLID")
				if label == "LMID" {
					added = append(added, blid)
				} else {
					removed = append(removed, blid)
				}
			}
		}
		return added, removed
	}
	var first []memberRef
	for i := int64(0); i <= MaxRecentPlayers; i++ { // one more than fits
		first = append(first, memberRef{ID: store.FirstPersonaID + i})
	}
	added, removed := reply(call(CmdALAddUsersToList, updateListRequest(ListTypeRecentPlayers, 0,
		append(first, memberRef{ID: me}, memberRef{ID: 42})...)))
	if len(added) != MaxRecentPlayers || added[0] != store.FirstPersonaID+1 || len(removed) != 0 {
		t.Fatalf("first game: added %v, removed %v", added, removed)
	}
	// the oldest member (persona +1) is refreshed, so the next new one pushes out persona +2
	if added, removed := reply(call(CmdALAddUsersToList, updateListRequest(ListTypeRecentPlayers, 0, memberRef{ID: store.FirstPersonaID + 1}))); len(added) != 0 || len(removed) != 0 {
		t.Errorf("refresh: added %v, removed %v", added, removed)
	}
	newest := store.FirstPersonaID + MaxRecentPlayers + 1
	if added, removed := reply(call(CmdALAddUsersToList, updateListRequest(ListTypeRecentPlayers, 0, memberRef{ID: newest}))); !reflect.DeepEqual(added, []int64{newest}) ||
		!reflect.DeepEqual(removed, []int64{store.FirstPersonaID + 2}) {
		t.Errorf("past the limit: added %v, removed %v", added, removed)
	}
	acct, _, _ := ps.srv.Store.PersonaByID(me)
	if n := len(acct.Lists.Recent); n != MaxRecentPlayers || acct.Lists.Recent[n-2].ID != store.FirstPersonaID+1 ||
		acct.Lists.Recent[n-1].ID != newest {
		t.Errorf("recent players %+v", acct.Lists.Recent)
	}
	for _, c := range []struct {
		cmd     uint16
		payload []byte
		err     uint16
	}{
		{CmdALAddUsersToList, updateListRequest(3, 0, memberRef{ID: store.FirstPersonaID}), ErrALListNotFound},
		{CmdALRemoveUsersFromList, updateListRequest(3, 0, memberRef{ID: store.FirstPersonaID}), ErrALListNotFound},
		{CmdALAddUsersToList, updateListRequest(ListTypeBlock, 0, memberRef{ID: me}), ErrALCannotIncludeSelf},
		{CmdALAddUsersToList, updateListRequest(ListTypeBlock, 0, memberRef{Name: "nobody"}), ErrALUserNotFound},
		{CmdALAddUsersToList, updateListRequest(ListTypeFriends, 1, memberRef{ID: 42}), ErrALUserNotFound},
		{CmdALRemoveUsersFromList, updateListRequest(ListTypeBlock, 0, memberRef{ID: store.FirstPersonaID}), ErrALMemberNotFound},
	} {
		if f := call(c.cmd, c.payload); f.Type != fire.ErrorReply || f.Error != c.err {
			t.Errorf("cmd %d: %s error %#x, want %#x", c.cmd, f.Type, f.Error, c.err)
		}
	}
	// removing a recent player by id
	if _, removed := reply(call(CmdALRemoveUsersFromList, updateListRequest(ListTypeRecentPlayers, 0, memberRef{ID: newest}))); !reflect.DeepEqual(removed, []int64{newest}) {
		t.Errorf("remove: %v", removed)
	}
	ps.close()
}

// B13b: a friendship survives a server restart (the accounts file), and the next login's getLists has it.
func TestFriendsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b13bAccounts(t, st, true)

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ps := newPipeSessionWith(t, func(s *Server) { s.Store = reopened })
	ps.loginAs("player@example.com", 0)
	ps.send(ComponentAssociationLists, CmdALGetLists, 2, ps.request("getlists_request"))
	ps.expect(ComponentUserSessions, NotifyUserAdded, fire.Notification, 0, "useradded_notification")
	ps.expect(ComponentUserSessions, NotifyUserUpdated, fire.Notification, 0, "userupdated_offline_notification")
	ps.next("third's UserAdded")
	ps.next("third's UserUpdated")
	ps.expect(ComponentAssociationLists, CmdALGetLists, fire.Reply, 2, "getlists_members_reply")
	ps.close()
}
