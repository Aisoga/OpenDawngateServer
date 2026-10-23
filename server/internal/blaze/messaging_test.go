package blaze

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// The B10c scenario of tdf_golden.py: "player" invites "second" to PvP party 1 (message 1), "second" accepts
// (message 2), "player" chats in the party (message 3, echo and filter) and whispers to "second" (message 4,
// persistent).
var (
	b10cInviteAttrs = []MsgAttr{{MsgAttrSenderName, "player"}, {MsgAttrPlaygroupID, "1"}, {MsgAttrInviteID, "1"},
		{MsgAttrInviteExtra, "0"}}
	b10cChatAttrs    = []MsgAttr{{MsgAttrText, "gl hf"}, {MsgAttrPlaygroupID, "2"}, {MsgAttrTextUnfiltered, "gl hf"}}
	b10cWhisperAttrs = []MsgAttr{{MsgAttrText, "gl hf"}, {MsgAttrPlaygroupID, "0"}, {MsgAttrGameID, "false"},
		{MsgAttrTextUnfiltered, "gl hf"}}
)

func b10cNote(id int64, name string, src int64, m ClientMessage) []byte {
	return MessageNotification(ServerMessage{ID: id, Name: name, Payload: m, Source: src, Time: testTime})
}

// b10cPayloads are B10c's replies and notifications with the golden case and the class they must match.
func b10cPayloads() []struct {
	name, class string
	got         []byte
} {
	second := store.FirstPersonaID + 1
	return []struct {
		name, class string
		got         []byte
	}{
		{"sendmessage_reply", "Blaze::Messaging::SendMessageResponse", SendMessageReply(1, nil)},
		{"sendmessage_stored_reply", "Blaze::Messaging::SendMessageResponse", SendMessageReply(4, []int64{4})},
		{"notifymessage_pvpinvite_notification", "Blaze::Messaging::ServerMessage", b10cNote(1, "player", store.FirstPersonaID,
			ClientMessage{Attrs: b10cInviteAttrs, Targets: []int64{second}, TargetType: ObjTypeUser, Type: MessageTypePvPInvite})},
		{"notifymessage_inviteanswer_notification", "Blaze::Messaging::ServerMessage", b10cNote(2, "second", second,
			ClientMessage{Attrs: []MsgAttr{{MsgAttrInviteAnswer, "T"}, {MsgAttrInviteID, "1"}}, Targets: []int64{store.FirstPersonaID},
				TargetType: ObjTypeUser, Type: MessageTypePvPInviteAnswer})},
		{"notifymessage_chat_notification", "Blaze::Messaging::ServerMessage", b10cNote(3, "player", store.FirstPersonaID,
			ClientMessage{Attrs: b10cChatAttrs, Flags: MessageFlagEcho | MessageFlagFilter, Targets: []int64{1},
				TargetType: ObjTypePlaygroup, Type: MessageTypeChat})},
		{"notifymessage_whisper_notification", "Blaze::Messaging::ServerMessage", b10cNote(4, "player", store.FirstPersonaID,
			ClientMessage{Attrs: b10cWhisperAttrs, Flags: MessageFlagPersistent | MessageFlagEcho | MessageFlagFilter,
				Targets: []int64{second}, TargetType: ObjTypeUser, Type: MessageTypeWhisper})},
		{"filterforprofanity_reply", "Blaze::Util::FilterUserTextResponse", FilterUserTextReply([]string{"gl hf"})},
	}
}

// B10c payloads, written independently by tdf_golden.py from the member tables (and checked against the
// client's tables in TestRepliesMatchClientMemberTables).
func TestB10cRepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range b10cPayloads() {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// The client's B10c requests (rebuilt from the static reading in tdf_golden.py) fit their classes, the
// server reads them, and the PYLD it relays is the request byte for byte.
func TestB10cClientRequests(t *testing.T) {
	ps := newPipeSession(t)
	defer ps.close()
	tdfcheck.Require(t, "Blaze::Util::UserStringList", ps.request("filterforprofanity_request"))
	for _, c := range []struct {
		name  string
		typ   int64
		flags int64
		ttyp  tdf.ObjType
	}{
		{"sendmessage_pvpinvite_request", MessageTypePvPInvite, 0, ObjTypeUser},
		{"sendmessage_inviteanswer_request", MessageTypePvPInviteAnswer, 0, ObjTypeUser},
		{"sendmessage_partychat_request", MessageTypeChat, MessageFlagEcho | MessageFlagFilter, ObjTypePlaygroup},
		{"sendmessage_whisper_request", MessageTypeWhisper, MessageFlagPersistent | MessageFlagEcho | MessageFlagFilter, ObjTypeUser},
	} {
		raw := ps.request(c.name)
		tdfcheck.Require(t, "Blaze::Messaging::ClientMessage", raw)
		st, err := tdf.Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		m, ok := ClientMessageFromRequest(st)
		if !ok || m.Type != c.typ || m.Flags != c.flags || m.TargetType != c.ttyp || len(m.Targets) != 1 {
			t.Errorf("%s: read %+v", c.name, m)
		}
		w := tdf.NewWriter()
		writeClientMessage(w, m)
		if !bytes.Equal(w.Bytes(), raw) {
			t.Errorf("%s: relayed PYLD differs\n got %x\nwant %x", c.name, w.Bytes(), raw)
		}
	}
}

// The invite, its answer, a party chat line and a whisper as the client sends them (static reading), over
// two pipe sessions against the goldens: the invitee gets the invite, joins the party by the invite's
// playgroup id and answers; the chat line goes through the profanity filter and reaches both members (the
// sender by echo); the whisper is kept for the invitee's fetchMessages and purged as the login does.
func TestInviteAndChatGolden(t *testing.T) {
	ps := newPipeSession(t)
	ps.loginAs("player@example.com", 0)
	ps.send(6, 1, 2, ps.request("createplaygroup_party_request"))
	ps.expect(6, 1, fire.Reply, 2, "createplaygroup_party_reply")
	ps.expect(6, 0x33, fire.Notification, 0, "notifyjoinplaygroup_party_notification")
	two := ps.another()
	two.loginAs("second@example.com", 0)

	ps.send(ComponentMessaging, CmdMsgSendMessage, 3, ps.request("sendmessage_pvpinvite_request"))
	ps.expect(ComponentMessaging, CmdMsgSendMessage, fire.Reply, 3, "sendmessage_reply")
	two.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_pvpinvite_notification")

	// the invitee's accept: joinPlaygroup with the invite's 0xB0003, then the answer "T" to the inviter
	two.send(6, 3, 2, ps.request("joinplaygroup_request"))
	two.expect(6, 3, fire.Reply, 2, "createplaygroup_party_reply")
	two.expectUserAdded(store.FirstPersonaID, true) // B14: each side learns the other as a user first
	payloadOf(t, "Blaze::Playgroups::NotifyJoinPlaygroup", two.next("NotifyJoinPlaygroup"))
	ps.expectUserAdded(store.FirstPersonaID+1, true)
	payloadOf(t, "Blaze::Playgroups::NotifyMemberJoinedPlaygroup", ps.next("NotifyMemberJoinedPlaygroup"))
	two.send(ComponentMessaging, CmdMsgSendMessage, 3, ps.request("sendmessage_inviteanswer_request"))
	if f := two.next("sendMessage reply"); f.Type != fire.Reply || !bytes.Equal(f.Payload, SendMessageReply(2, nil)) {
		t.Errorf("answer's reply: %s %x", f.Type, f.Payload)
	}
	ps.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_inviteanswer_notification")

	// party chat: the filter first (flag 0x4), then the message; the echo comes after the reply
	ps.send(ComponentUtil, CmdUtilFilterForUText, 4, ps.request("filterforprofanity_request"))
	ps.expect(ComponentUtil, CmdUtilFilterForUText, fire.Reply, 4, "filterforprofanity_reply")
	ps.send(ComponentMessaging, CmdMsgSendMessage, 5, ps.request("sendmessage_partychat_request"))
	if f := ps.next("sendMessage reply"); f.Type != fire.Reply || !bytes.Equal(f.Payload, SendMessageReply(3, nil)) {
		t.Errorf("chat's reply: %s %x", f.Type, f.Payload)
	}
	ps.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_chat_notification")
	two.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_chat_notification")

	// whisper (persistent): delivered, echoed and kept; the next login's fetch and purge
	ps.send(ComponentMessaging, CmdMsgSendMessage, 6, ps.request("sendmessage_whisper_request"))
	ps.expect(ComponentMessaging, CmdMsgSendMessage, fire.Reply, 6, "sendmessage_stored_reply")
	ps.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_whisper_notification")
	two.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_whisper_notification")
	fetch := tdf.NewWriter().Int("FLAG", 0).Int("MGID", 0).Int("PIDX", 0).Int("PSIZ", 0).Int("SMSK", 0).Int("SORT", 0).
		Value("SRCE", tdf.TypeObjID, tdf.ObjID{}).Int("STAT", 0).Value("TARG", tdf.TypeObjID, tdf.ObjID{}).Int("TYPE", 0).Bytes()
	tdfcheck.Require(t, "Blaze::Messaging::FetchMessageRequest", fetch)
	two.send(ComponentMessaging, CmdMsgFetchMessages, 4, fetch)
	if f := two.next("fetchMessages reply"); !bytes.Equal(f.Payload, MessageCountReply(1)) {
		t.Errorf("fetch: %x", f.Payload)
	}
	two.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_whisper_notification")
	purge := tdf.NewWriter().Int("FLAG", MessageMatchType).Int("MGID", 0).Int("SMSK", 0).
		Value("SRCE", tdf.TypeObjID, tdf.ObjID{}).Int("STAT", 0).Int("TYPE", MessageTypeWhisper).Bytes()
	tdfcheck.Require(t, "Blaze::Messaging::PurgeMessageRequest", purge)
	two.send(ComponentMessaging, CmdMsgPurgeMessages, 5, purge)
	if f := two.next("purgeMessages reply"); !bytes.Equal(f.Payload, MessageCountReply(1)) {
		t.Errorf("purge: %x", f.Payload)
	}
	// the guest quits: its close hook tells the leader (a synchronous pipe, so read before waiting)
	two.client.Close()
	if f := ps.next("NotifyMemberRemovedFromPlaygroup"); f.Command != NotifyMemberRemovedFromPlaygroupID {
		t.Errorf("after the guest's close: %#x", f.Command)
	}
	<-two.done
	log := ps.close()
	for _, want := range []string{
		"[blaze]    message 1 from 2000000001 \"player\" (PvP invite 0x176a0d2b, flags 0x0, to user [2000000002]): delivered to [2000000002], stored for []\n",
		"[blaze]    message 3 from 2000000001 \"player\" (chat 0x2, flags 0x6, to playgroup [1]): delivered to [2000000001 2000000002], stored for []\n",
		"[blaze]    message 4 from 2000000001 \"player\" (whisper 0x3, flags 0x7, to user [2000000002]): delivered to [2000000002 2000000001], stored for [2000000002]\n",
		"[blaze]    profanity filter: 1 texts (\"gl hf\"), all passed\n",
		"[blaze]    messages for 2000000002: 1 fetched\n",
		"[blaze]    messages for 2000000002: 1 purged\n",
		"[blaze] -> Messaging::NotifyMessage notification, ",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// msg builds a sendMessage request as the client does (every member written).
func msg(typ, flags int64, ttyp tdf.ObjType, attrs []MsgAttr, targets ...int64) []byte {
	w := tdf.NewWriter()
	writeClientMessage(w, ClientMessage{Attrs: attrs, Flags: flags, Targets: targets, TargetType: ttyp, Type: typ})
	return w.Bytes()
}

func chatLine(text string) []MsgAttr {
	return []MsgAttr{{MsgAttrText, text}, {MsgAttrPlaygroupID, "3"}, {MsgAttrTextUnfiltered, text}}
}

func (tc *tcpClient) sendMsg(payload []byte) fire.Frame {
	tc.t.Helper()
	return tc.reply(ComponentMessaging, CmdMsgSendMessage, tc.send(ComponentMessaging, CmdMsgSendMessage, payload))
}

// msgNote waits for a NotifyMessage, checks it against ServerMessage and returns its PYLD's text (0xFF02),
// checking the sender.
func (tc *tcpClient) msgNote(from *tcpClient) *tdf.Struct {
	tc.t.Helper()
	st := payloadOf(tc.t, "Blaze::Messaging::ServerMessage", tc.note(ComponentMessaging, NotifyMessageID))
	if f, _ := st.Get("SRCE"); f.Value != (tdf.ObjID{Component: ComponentUserSessions, Type: 1, ID: from.id}) {
		tc.t.Errorf("%s: SRCE %v, want %d", tc.name, f.Value, from.id)
	}
	if name, _ := st.Str("NAME"); name != from.name {
		tc.t.Errorf("%s: NAME %q, want %q", tc.name, name, from.name)
	}
	pyld, _ := st.Sub("PYLD")
	return pyld
}

func attrOf(t *testing.T, pyld *tdf.Struct, key int64) string {
	t.Helper()
	f, _ := pyld.Get("ATTR")
	for _, e := range f.Value.(*tdf.Map).Entries {
		if e.Key == key {
			return e.Value.(string)
		}
	}
	t.Errorf("no attribute %#x in %v", key, f.Value)
	return ""
}

// A PvP invite between fake clients on a loopback port: the leader creates the party and invites; the
// invitee reads the playgroup id from the invite, joins the party (the leader hears of it) and answers;
// a bystander gets nothing; an invite to nobody fails.
func TestMessagingInviteJoinsParty(t *testing.T) {
	ts := newTCPServer(t, nil)
	a, b, c := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com")
	f := a.pgCall(CmdPlaygroupsCreatePlaygroup, pvpCreate())
	info, _ := decode(t, f).Sub("INFO")
	pgid, _ := info.Int("PGID")
	a.note(ComponentPlaygroups, NotifyJoinPlaygroupID)

	invite := msg(MessageTypePvPInvite, 0, ObjTypeUser, []MsgAttr{{MsgAttrSenderName, a.name},
		{MsgAttrPlaygroupID, strconv.FormatInt(pgid, 10)}, {MsgAttrInviteID, "7"}, {MsgAttrInviteExtra, "0"}}, b.id)
	r := a.sendMsg(invite)
	wantOK(t, "invite", r)
	tdfcheck.Require(t, "Blaze::Messaging::SendMessageResponse", r.Payload)
	pyld := b.msgNote(a)
	if typ, _ := pyld.Int("TYPE"); typ != MessageTypePvPInvite {
		t.Errorf("TYPE %#x", typ)
	}
	got, err := strconv.ParseInt(attrOf(t, pyld, MsgAttrPlaygroupID), 10, 64)
	if err != nil || got != pgid {
		t.Fatalf("invite's playgroup %d (%v), want %d", got, err, pgid)
	}
	a.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond) // no echo without the flag

	wantOK(t, "join from the invite", b.pgCall(CmdPlaygroupsJoinPlaygroup, tdf.NewWriter().Int("PGID", got).Bytes()))
	b.pgNote(NotifyJoinPlaygroupID, "Blaze::Playgroups::NotifyJoinPlaygroup")
	a.pgNote(NotifyMemberJoinedPlaygroupID, "Blaze::Playgroups::NotifyMemberJoinedPlaygroup")
	answer := msg(MessageTypePvPInviteAnswer, 0, ObjTypeUser, []MsgAttr{{MsgAttrInviteAnswer, "T"},
		{MsgAttrInviteID, attrOf(t, pyld, MsgAttrInviteID)}}, a.id)
	wantOK(t, "answer", b.sendMsg(answer))
	if p := a.msgNote(b); attrOf(t, p, MsgAttrInviteAnswer) != "T" || attrOf(t, p, MsgAttrInviteID) != "7" {
		t.Errorf("answer %v", p)
	}
	c.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)

	wantErr(t, "invite to nobody", a.sendMsg(msg(MessageTypePvPInvite, 0, ObjTypeUser, nil, 4242)), ErrMessagingTargetNotFound)
}

// Chat: the party's chat (playgroup target, echo) reaches every member and only members may send; the
// game's chat playgroup "GG-7" and the game itself (target (4, 1, 7)) reach every player in the game;
// without the echo flag the sender gets nothing; the profanity filter passes the text back unchanged.
func TestMessagingChat(t *testing.T) {
	ts := newTCPServer(t, nil)
	a, b, c, d := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com"),
		ts.client("delta@example.com")
	f := a.pgCall(CmdPlaygroupsCreatePlaygroup, pvpCreate())
	info, _ := decode(t, f).Sub("INFO")
	party, _ := info.Int("PGID")
	a.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	wantOK(t, "join", b.pgCall(CmdPlaygroupsJoinPlaygroup, tdf.NewWriter().Int("PGID", party).Bytes()))
	b.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	a.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID)

	filter := tdf.NewWriter().List("UTXT", tdf.TypeStruct, []*tdf.Writer{tdf.NewWriter().String("UTXT", "hi all")}).Bytes()
	r := a.reply(ComponentUtil, CmdUtilFilterForUText, a.send(ComponentUtil, CmdUtilFilterForUText, filter))
	tdfcheck.Require(t, "Blaze::Util::FilterUserTextResponse", r.Payload)
	if !bytes.Equal(r.Payload, FilterUserTextReply([]string{"hi all"})) {
		t.Errorf("filter: %x", r.Payload)
	}

	wantOK(t, "party chat", a.sendMsg(msg(MessageTypeChat, MessageFlagEcho|MessageFlagFilter, ObjTypePlaygroup, chatLine("hi all"), party)))
	for _, x := range []*tcpClient{a, b} {
		if p := x.msgNote(a); attrOf(t, p, MsgAttrText) != "hi all" {
			t.Errorf("%s: text", x.name)
		}
	}
	wantErr(t, "chat into a party one isn't in", c.sendMsg(msg(MessageTypeChat, MessageFlagEcho, ObjTypePlaygroup, chatLine("x"), party)),
		ErrMessagingTargetNotFound)
	wantOK(t, "no echo", b.sendMsg(msg(MessageTypeChat, 0, ObjTypePlaygroup, chatLine("quiet"), party)))
	a.msgNote(b)
	b.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)

	// the game's chat: GG-7 as the client joins it (createPlaygroup JOIN 1, B11)
	gg := tdf.NewWriter().Int("JOIN", 1).Struct("PGRP", func(w *tdf.Writer) {
		w.Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV(PlaygroupKeyAttr, "GG-7")).Int("MLIM", 15).String("NAME", "GG-7").String("UKEY", "GG-7")
	}).Bytes()
	var ggID int64
	for i, x := range []*tcpClient{a, b, c} {
		info, _ := decode(t, x.pgCall(CmdPlaygroupsCreatePlaygroup, gg)).Sub("INFO")
		ggID, _ = info.Int("PGID")
		x.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
		for _, y := range []*tcpClient{a, b, c}[:i] {
			y.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID)
		}
	}
	wantOK(t, "game chat", c.sendMsg(msg(MessageTypeChat, MessageFlagEcho|MessageFlagFilter, ObjTypePlaygroup, chatLine("gg"), ggID)))
	for _, x := range []*tcpClient{a, b, c} {
		if p := x.msgNote(c); attrOf(t, p, MsgAttrText) != "gg" {
			t.Errorf("%s: game chat text", x.name)
		}
	}
	wantOK(t, "game target", b.sendMsg(msg(MessageTypeChat, MessageFlagEcho, ObjTypeGame, chatLine("to the game"), 7)))
	for _, x := range []*tcpClient{a, b, c} {
		p := x.msgNote(b)
		if f, _ := p.Get("TTYP"); f.Value != ObjTypeGame {
			t.Errorf("%s: TTYP %v", x.name, f.Value)
		}
	}
	wantErr(t, "game one isn't in", d.sendMsg(msg(MessageTypeChat, 0, ObjTypeGame, chatLine("x"), 7)), ErrMessagingTargetNotFound)
	wantErr(t, "unknown game", a.sendMsg(msg(MessageTypeChat, 0, ObjTypeGame, chatLine("x"), 8)), ErrMessagingTargetNotFound)
	d.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
}

// Persistent messages: a whisper to a user who is offline is kept (and echoed to the sender), a
// non-persistent one isn't; at the next login fetchMessages delivers the kept ones and purgeMessages
// deletes them by type, id or sender; a user logged in twice gets messages on both connections.
func TestMessagingOfflineAndFetch(t *testing.T) {
	ts := newTCPServer(t, nil)
	a := ts.client("alpha@example.com")
	c := ts.client("charlie@example.com")
	cid := c.id
	c.c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for ts.s.msgsOnline(cid) {
		if time.Now().After(deadline) {
			t.Fatal("charlie still online")
		}
		time.Sleep(5 * time.Millisecond)
	}
	whisper := msg(MessageTypeWhisper, MessageFlagPersistent|MessageFlagEcho|MessageFlagFilter, ObjTypeUser, chatLine("psst"), cid)
	r := a.sendMsg(whisper)
	wantOK(t, "whisper", r)
	st := decode(t, r)
	if f, _ := st.Get("MIDS"); len(f.Value.(*tdf.List).Items) != 1 {
		t.Errorf("MIDS %v", f.Value)
	}
	if p := a.msgNote(a); attrOf(t, p, MsgAttrText) != "psst" { // the echo
		t.Errorf("echo text")
	}
	wantOK(t, "instant message to someone offline", a.sendMsg(msg(MessageTypeInstant, MessageFlagEcho, ObjTypeUser, chatLine("lost"), cid)))
	a.msgNote(a)
	wantOK(t, "invite-like", a.sendMsg(msg(MessageTypeInviteQuery, MessageFlagPersistent, ObjTypeUser, nil, cid)))

	c2 := ts.client("charlie@example.com")
	c3 := ts.client("charlie@example.com") // the same user twice
	fetchAll := tdf.NewWriter().Int("FLAG", 0).Bytes()
	f := c2.reply(ComponentMessaging, CmdMsgFetchMessages, c2.send(ComponentMessaging, CmdMsgFetchMessages, fetchAll))
	if !bytes.Equal(f.Payload, MessageCountReply(2)) {
		t.Fatalf("fetch: %x", f.Payload)
	}
	first, second := c2.msgNote(a), c2.msgNote(a)
	if typ, _ := first.Int("TYPE"); typ != MessageTypeWhisper {
		t.Errorf("first fetched TYPE %#x", typ)
	}
	if typ, _ := second.Int("TYPE"); typ != MessageTypeInviteQuery {
		t.Errorf("second fetched TYPE %#x", typ)
	}
	c3.none(ComponentMessaging, NotifyMessageID, 30*time.Millisecond) // a fetch is per connection

	purge := func(tc *tcpClient, w *tdf.Writer) int64 {
		f := tc.reply(ComponentMessaging, CmdMsgPurgeMessages, tc.send(ComponentMessaging, CmdMsgPurgeMessages, w.Bytes()))
		n, _ := decode(t, f).Int("MCNT")
		return n
	}
	if n := purge(c2, tdf.NewWriter().Int("FLAG", MessageMatchType).Int("TYPE", MessageTypeWhisper)); n != 1 {
		t.Errorf("purge whispers: %d", n)
	}
	if n := purge(c2, tdf.NewWriter().Int("FLAG", MessageMatchID).Int("MGID", 999)); n != 0 {
		t.Errorf("purge by an unknown id: %d", n)
	}
	src := tdf.ObjID{Component: ComponentUserSessions, Type: 1, ID: a.id}
	if n := purge(c2, tdf.NewWriter().Int("FLAG", MessageMatchSource).Value("SRCE", tdf.TypeObjID, src)); n != 1 {
		t.Errorf("purge by sender: %d", n)
	}
	f = c2.reply(ComponentMessaging, CmdMsgFetchMessages, c2.send(ComponentMessaging, CmdMsgFetchMessages, fetchAll))
	if !bytes.Equal(f.Payload, MessageCountReply(0)) {
		t.Errorf("fetch after purge: %x", f.Payload)
	}

	// online now on two connections: both get a live message, and a persistent one is kept for later
	wantOK(t, "whisper online", a.sendMsg(whisper))
	a.msgNote(a)
	c2.msgNote(a)
	c3.msgNote(a)
	if n := purge(c3, tdf.NewWriter().Int("FLAG", MessageMatchID).Int("MGID", 4)); n != 1 {
		t.Errorf("purge by id: %d", n)
	}
}

// Limits and refusals: target types other than user, playgroup and game, no targets, too many attributes,
// a too long text, the sender's rate.
func TestMessagingLimits(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) {
		s.msgs.limits = MessagingLimits{MaxTargets: 4, MaxAttrs: 3, MaxAttrBytes: 16, InboxSize: 2, Burst: 7, Every: time.Hour}
	})
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	other := tdf.ObjType{Component: ComponentRooms, Type: 2}
	wantErr(t, "another type", a.sendMsg(msg(MessageTypeChat, 0, other, chatLine("x"), 1)), ErrMessagingTargetTypeInvalid)
	wantErr(t, "no targets", a.sendMsg(msg(MessageTypeChat, 0, ObjTypeUser, chatLine("x"))), ErrMessagingInvalidParam)
	wantErr(t, "five targets", a.sendMsg(msg(MessageTypeChat, 0, ObjTypeUser, chatLine("x"), b.id, b.id, b.id, b.id, b.id)), ErrMessagingInvalidParam)
	wantErr(t, "four attributes", a.sendMsg(msg(MessageTypeChat, 0, ObjTypeUser, append(chatLine("x"), MsgAttr{1, "y"}), b.id)),
		ErrMessagingMaxAttrExceeded)
	wantErr(t, "long text", a.sendMsg(msg(MessageTypeChat, 0, ObjTypeUser, chatLine(strings.Repeat("x", 17)), b.id)), ErrMessagingInvalidParam)
	if f := a.reply(ComponentMessaging, CmdMsgSendMessage, a.send(ComponentMessaging, CmdMsgSendMessage, nil)); f.Type != fire.ErrorReply {
		t.Errorf("no TTYP: %s", f.Type)
	}
	wantOK(t, "kept 1", a.sendMsg(msg(MessageTypeWhisper, MessageFlagPersistent, ObjTypeUser, chatLine("1"), b.id)))
	b.msgNote(a)
	wantOK(t, "kept 2", a.sendMsg(msg(MessageTypeWhisper, MessageFlagPersistent, ObjTypeUser, chatLine("2"), b.id)))
	b.msgNote(a)
	// seven counted (the TTYP-less one never reached the relay): the eighth is over the burst of 7
	wantErr(t, "rate", a.sendMsg(msg(MessageTypeChat, 0, ObjTypeUser, chatLine("3"), b.id)), ErrMessagingUnknown)
	ts.s.msgs.mu.Lock()
	box := ts.s.msgs.inbox[b.id]
	ts.s.msgs.mu.Unlock()
	if len(box) != 2 {
		t.Errorf("inbox %d", len(box))
	}
	wantOK(t, "b may still send", b.sendMsg(msg(MessageTypeChat, 0, ObjTypeUser, chatLine("4"), a.id)))
	a.msgNote(b)
}

// The inbox keeps the newest InboxSize messages.
func TestMessagingInboxSize(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) {
		s.msgs.limits = MessagingLimits{MaxTargets: 4, MaxAttrs: 4, MaxAttrBytes: 64, InboxSize: 2, Burst: 100, Every: time.Second}
	})
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	for i := range 3 {
		wantOK(t, "whisper", a.sendMsg(msg(MessageTypeWhisper, MessageFlagPersistent, ObjTypeUser, chatLine(fmt.Sprint(i)), b.id)))
		b.msgNote(a)
	}
	f := b.reply(ComponentMessaging, CmdMsgFetchMessages, b.send(ComponentMessaging, CmdMsgFetchMessages, nil))
	if !bytes.Equal(f.Payload, MessageCountReply(2)) {
		t.Fatalf("fetch %x", f.Payload)
	}
	for _, want := range []string{"1", "2"} {
		if p := b.msgNote(a); attrOf(t, p, MsgAttrText) != want {
			t.Errorf("kept %q", attrOf(t, p, MsgAttrText))
		}
	}
}

// msgsOnline reports whether blazeID has a connection (tests).
func (s *Server) msgsOnline(blazeID int64) bool {
	s.msgs.mu.Lock()
	defer s.msgs.mu.Unlock()
	return len(s.msgs.online[blazeID]) > 0
}
