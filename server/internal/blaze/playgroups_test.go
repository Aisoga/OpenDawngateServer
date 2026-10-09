package blaze

import (
	"encoding/hex"
	"strings"
	"testing"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// testPlaygroup is game 1's chat playgroup as the server makes it from the client's createPlaygroup
// (session d2f_run2), owned by the first persona; tdf_golden.py writes the same.
func testPlaygroup() PlaygroupInfo {
	return PlaygroupInfo{ID: 1, Name: "GG-1", Key: "GG-1", Attrs: []tdf.Pair{tdf.KV(PlaygroupKeyAttr, "GG-1")},
		MemberLimit: 15, Owner: store.FirstPersonaID}
}

func testMember() PlaygroupMember {
	return PlaygroupMember{AccountID: store.FirstUserID, BlazeID: store.FirstPersonaID, Name: "player",
		JoinTime: testTime}
}

// secondMember is a second player who joins the same chat (G3's case; written by tdf_golden.py too).
func secondMember() PlaygroupMember {
	return PlaygroupMember{AccountID: store.FirstUserID + 1, BlazeID: store.FirstPersonaID + 1, Name: "second",
		Slot: 1, JoinTime: testTime}
}

// B11 payloads, written independently by tdf_golden.py from the member tables.
func TestB11RepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range []struct {
		name string
		got  []byte
	}{
		{"createplaygroup_reply", JoinPlaygroupReply(testPlaygroup())},
		{"notifyjoinplaygroup_notification", JoinPlaygroupNotification(testPlaygroup(),
			[]PlaygroupMember{testMember()}, store.FirstPersonaID)},
		{"notifymemberjoinedplaygroup_notification", MemberJoinedPlaygroupNotification(1, secondMember())},
		{"notifymemberremovedfromplaygroup_notification", MemberRemovedFromPlaygroupNotification(1,
			store.FirstPersonaID+1, PlaygroupMemberRemoveDisconnected)},
	} {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// loginAs logs a pipe session in with email (its persona is the email's name) and skips the two
// notifications that follow loginPersona.
func (ps *pipeSession) loginAs(email string, id uint32) {
	ps.send(1, 0x28, id, tdf.NewWriter().String("MAIL", email).String("PASS", "x").String("TOKN", "").Int("TYPE", 0).Bytes())
	ps.next("login reply")
	ps.send(1, 0x6E, id+1, tdf.NewWriter().String("PNAM", store.PersonaName(email)).Bytes())
	ps.next("loginPersona reply")
	ps.next("UserAuthenticated")
	ps.next("UserSessionExtendedDataUpdate")
}

// payloadOf decodes a frame's payload, checks it against the class's member table and returns it.
func payloadOf(t *testing.T, class string, f fire.Frame) *tdf.Struct {
	t.Helper()
	tdfcheck.Require(t, class, f.Payload)
	st, err := tdf.Decode(f.Payload)
	if err != nil {
		t.Fatalf("%s: %v", class, err)
	}
	return st
}

// The game's chat, as the client runs it after joining game 1 (b9_run1 onwards): createPlaygroup "GG-1"
// gets JoinPlaygroupResponse and then NotifyJoinPlaygroup; finalizePlaygroupCreation and leavePlaygroup
// get empty replies. A second player with the same key joins the same playgroup (the first member hears
// of it), and leaves it by disconnecting.
func TestChatPlaygroup(t *testing.T) {
	ps := newPipeSession(t)
	ps.send(6, 1, 0, ps.request("createplaygroup_request"))
	tdfcheck.Require(t, "Blaze::Playgroups::CreatePlaygroupRequest", ps.request("createplaygroup_request"))
	ps.loginAs("player@example.com", 1)
	ps.send(6, 1, 3, ps.request("createplaygroup_request"))
	ps.expect(6, 1, fire.Reply, 3, "createplaygroup_reply")
	ps.expect(6, 0x33, fire.Notification, 0, "notifyjoinplaygroup_notification")
	// the client's next step (0x00C8EDF5): finalizePlaygroupCreation with PGID and empty Xbox blobs
	fin := tdf.NewWriter().Int("PGID", 1).Blob("XNNC", nil).Blob("XSES", nil).Bytes()
	tdfcheck.Require(t, "Blaze::Playgroups::UpdatePlaygroupSessionRequest", fin)
	ps.send(6, 9, 4, fin)
	ps.expect(6, 9, fire.Reply, 4, "")

	// a second player asks for the same key: same playgroup, both members; the first one is told
	two := ps.another()
	two.loginAs("second@example.com", 1)
	two.send(6, 1, 3, ps.request("createplaygroup_request"))
	two.expect(6, 1, fire.Reply, 3, "createplaygroup_reply") // the playgroup as it is: owner = the first
	two.expectUserAdded(store.FirstPersonaID, true)          // B14: the other member as a user first
	join := payloadOf(t, "Blaze::Playgroups::NotifyJoinPlaygroup", two.next("NotifyJoinPlaygroup"))
	if user, _ := join.Int("USER"); user != store.FirstPersonaID+1 {
		t.Errorf("NotifyJoinPlaygroup USER %d", user)
	}
	if f, ok := join.Get("MLST"); !ok || len(f.Value.(*tdf.List).Items) != 2 {
		t.Errorf("NotifyJoinPlaygroup MLST: %v", f.Value)
	}
	ps.expectUserAdded(store.FirstPersonaID+1, true)
	ps.expect(6, 0x34, fire.Notification, 0, "notifymemberjoinedplaygroup_notification")
	// asking again changes nothing for the others; the asker gets the member's current data again (it knows the user)
	two.send(6, 1, 4, ps.request("createplaygroup_request"))
	two.expect(6, 1, fire.Reply, 4, "createplaygroup_reply")
	if f := two.next("the member's data"); f.Command != NotifyUserSessionExtendedDataUpdate {
		t.Errorf("again: %#x, want UserSessionExtendedDataUpdate", f.Command)
	}
	two.next("NotifyJoinPlaygroup again")
	// the second player's connection closes; its close hook writes to ps (a synchronous pipe), so read
	// that before waiting for the connection to end
	two.client.Close()
	ps.expect(6, 0x35, fire.Notification, 0, "notifymemberremovedfromplaygroup_notification")
	<-two.done

	// the game is over: the chat leaves (empty reply); the playgroup is gone
	ps.send(6, 4, 5, tdf.NewWriter().Int("PGID", 1).Bytes())
	ps.expect(6, 4, fire.Reply, 5, "")
	log := ps.close()
	if n := ps.srv.groups.count(); n != 0 {
		t.Errorf("%d playgroups left", n)
	}
	for _, want := range []string{
		"(createPlaygroup before login; not answered)",
		"[blaze]    playgroup 1 \"GG-1\" created by 2000000001 (1 members)\n",
		"[blaze] -> createPlaygroup reply, 112 bytes:\n",
		"[blaze] -> Playgroups::NotifyJoinPlaygroup notification, 191 bytes:\n",
		"[blaze]    playgroup 1: creation finalized\n",
		"[blaze]    playgroup 1 \"GG-1\" joined by 2000000002 (2 members)\n",
		"[blaze]    playgroup 1 \"GG-1\" joined again by 2000000002 (2 members)\n",
		"[blaze] -> Playgroups::NotifyMemberJoinedPlaygroup notification, 73 bytes:\n",
		"[blaze]    playgroup 1: 2000000002 left (reason 1, 1 members stay)\n",
		"[blaze]    playgroup 1: 2000000001 left (reason 0, 0 members stay)\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// createPlaygroup without JOIN for a key that exists, and for a full playgroup, gets an error reply.
func TestPlaygroupErrors(t *testing.T) {
	ps := newPipeSession(t)
	ps.loginAs("player@example.com", 0)
	small := tdf.NewWriter().Int("JOIN", 1).Struct("PGRP", func(w *tdf.Writer) {
		w.Int("MLIM", 1).String("NAME", "solo").String("UKEY", "solo")
	}).Bytes()
	ps.send(6, 1, 2, small)
	ps.next("createPlaygroup reply")
	ps.next("NotifyJoinPlaygroup")
	two := ps.another()
	two.loginAs("second@example.com", 0)
	two.send(6, 1, 2, small)
	if f := two.next("full"); f.Type != fire.ErrorReply || f.Error != ErrPlaygroupFull {
		t.Errorf("full playgroup: type %s error %#x", f.Type, f.Error)
	}
	noJoin := tdf.NewWriter().Int("JOIN", 0).Struct("PGRP", func(w *tdf.Writer) { w.String("UKEY", "solo") }).Bytes()
	two.send(6, 1, 3, noJoin)
	if f := two.next("exists"); f.Type != fire.ErrorReply || f.Error != ErrPlaygroupAlreadyExists {
		t.Errorf("existing key without JOIN: type %s error %#x", f.Type, f.Error)
	}
	two.close()
	ps.close()
	if n := ps.srv.groups.count(); n != 0 {
		t.Errorf("%d playgroups left after both connections closed", n)
	}
}
