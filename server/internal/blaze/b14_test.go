package blaze

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// b14Broadcast is the broadcast of tdf_golden.py's B14 cases.
var b14Broadcast = Broadcast{ID: 1, Flags: BroadcastMarquee, Text: "Server restart at 20:00 UTC"}

// b14Player is the first persona's account with 3 wins (PlayerStats WINS), as tdf_golden.py's B14 cases have it.
func b14Player() (store.Account, store.Persona) {
	acct, p := testAccount()
	acct.Stats = map[string]map[string]string{progress.PlayerStats: {"WINS": "3"}}
	return acct, p
}

// b14Payloads are B14's notifications with their classes and tdf_golden.py's case names: the system messages
// (MGID 1-3 in the order a login gets them) and the party member's user data with its wins.
func b14Payloads() []struct {
	name, class string
	got         []byte
} {
	acct, p := b14Player()
	return []struct {
		name, class string
		got         []byte
	}{
		{"notifymessage_killswitches_notification", "Blaze::Messaging::ServerMessage",
			SystemMessageNotification(1, KillSwitchMessage([]int64{2}), testTime)},
		{"notifymessage_killswitches_two_notification", "Blaze::Messaging::ServerMessage",
			SystemMessageNotification(1, KillSwitchMessage([]int64{2, 8}), testTime)},
		{"notifymessage_hidebroadcasts_notification", "Blaze::Messaging::ServerMessage",
			SystemMessageNotification(2, HideBroadcastsMessage(), testTime)},
		{"notifymessage_broadcast_notification", "Blaze::Messaging::ServerMessage",
			SystemMessageNotification(3, BroadcastMessage(b14Broadcast), testTime)},
		{"useradded_wins_notification", "Blaze::NotifyUserAdded", UserAddedNotification(acct, p, 0)},
		{"memberdata_notification", "Blaze::UserSessionExtendedDataUpdate", PresenceNotification(acct, p, 0)},
	}
}

// B14: every new notification matches tdf_golden.py's independent writer and the client's member table.
func TestB14RepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range b14Payloads() {
		tdfcheck.Require(t, c.class, c.got)
		if h, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; h != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, h, want)
		}
	}
}

func TestParseKillSwitches(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []int64
	}{
		{"", nil},
		{"none", nil},
		{DefaultKillSwitches, []int64{2}},
		{"customgames, WalletTopUp,2", []int64{2, 8}},
		{"1,9", []int64{1, 9}},
	} {
		got, err := ParseKillSwitches(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseKillSwitches(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"Wallet", "0", "10", "WalletTopUp,x"} {
		if _, err := ParseKillSwitches(bad); err == nil {
			t.Errorf("ParseKillSwitches(%q): no error", bad)
		}
	}
	if got := DescribeKillSwitches([]int64{2, 8}); got != "2 WalletTopUp, 8 CustomGames" {
		t.Errorf("DescribeKillSwitches: %q", got)
	}
}

func TestParseBroadcasts(t *testing.T) {
	long := strings.Repeat("x", MaxBroadcastBytes-1) + "\u00e4" // the cut must not split the last character
	text := "\uFEFF# a comment\r\n\r\nWelcome!\r\n  @6 Restart in 5 minutes  \n@9 bad flags\n@2\n" + long + "\n"
	got, notes := ParseBroadcasts(text, DefaultBroadcastFlags)
	want := []Broadcast{{Flags: 4, Text: "Welcome!"}, {Flags: 6, Text: "Restart in 5 minutes"}, {Flags: 4, Text: "bad flags"},
		{Flags: 4, Text: strings.Repeat("x", MaxBroadcastBytes-1)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseBroadcasts\n got %+v\nwant %+v", got, want)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], `flags "9"`) || !strings.Contains(notes[1], "cut to 1023 bytes") {
		t.Errorf("notes %q", notes)
	}
	many := strings.Repeat("m\n", MaxBroadcasts+3)
	if got, notes := ParseBroadcasts(many, 1); len(got) != MaxBroadcasts || len(notes) != 1 {
		t.Errorf("%d messages, notes %q", len(got), notes)
	}
}

// expectUserAdded reads UserAdded and UserUpdated for persona id (B13b/B14: a user the connection's client wasn't
// told about), checks them against their classes and returns UserAdded's payload.
func (ps *pipeSession) expectUserAdded(id int64, online bool) *tdf.Struct {
	ps.t.Helper()
	f := ps.next("UserAdded")
	added := payloadOf(ps.t, "Blaze::NotifyUserAdded", f)
	if user, _ := added.Sub("USER"); f.Command != NotifyUserAdded || user == nil {
		ps.t.Fatalf("UserAdded for %d: got %#x/%#x %+v", id, f.Component, f.Command, added)
	} else if uid, _ := user.Int("ID"); uid != id {
		ps.t.Errorf("UserAdded for %d, want %d", uid, id)
	}
	f = ps.next("UserUpdated")
	st := payloadOf(ps.t, "Blaze::UserStatus", f)
	flags, _ := st.Int("FLGS")
	if uid, _ := st.Int("ID"); f.Command != NotifyUserUpdated || uid != id || flags != UserStatusKnown|b2i(online)*UserStatusOnline {
		ps.t.Errorf("UserUpdated: %#x id %d flags %#x", f.Command, uid, flags)
	}
	return added
}

// winsOf returns the wins in an extended data struct (DATA's DMAP[0x00070034]; 0 without one).
func winsOf(t *testing.T, s *tdf.Struct) int64 {
	t.Helper()
	data, ok := s.Sub("DATA")
	if !ok {
		t.Fatalf("no DATA: %+v", s)
	}
	f, ok := data.Get("DMAP")
	if !ok {
		return 0
	}
	for _, e := range f.Value.(*tdf.Map).Entries {
		if e.Key == int64(ExtendedDataWins) {
			return e.Value.(int64)
		}
	}
	return 0
}

// B14 (a): with kill switches and broadcasts configured, a login gets them right after loginPersona's reply and its
// two notifications: the switches (TAG 0), "hide all" (TAG 1), each broadcast (TAG 2). The server without either
// sends nothing (TestServeConn).
func TestLoginSystemMessages(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) {
		s.KillSwitches = []int64{2}
		s.SetBroadcasts([]Broadcast{{Flags: BroadcastMarquee, Text: b14Broadcast.Text}})
	})
	ps.send(1, 0x28, 0, ps.request("login_request"))
	ps.expect(1, 0x28, fire.Reply, 0, "login_reply")
	ps.send(1, 0x6E, 1, ps.request("loginpersona_request"))
	ps.expect(1, 0x6E, fire.Reply, 1, "loginpersona_reply")
	ps.expect(0x7802, 8, fire.Notification, 0, "userauthenticated_notification")
	ps.expect(0x7802, 1, fire.Notification, 0, "extendeddataupdate_notification")
	ps.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_killswitches_notification")
	ps.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_hidebroadcasts_notification")
	ps.expect(ComponentMessaging, NotifyMessageID, fire.Notification, 0, "notifymessage_broadcast_notification")
	ps.send(9, 8, 2, nil) // postAuth: the session goes on
	ps.expect(9, 8, fire.Reply, 2, "postauth_reply")
	log := ps.close()
	for _, want := range []string{
		`broadcasts: 1 "Server restart at 20:00 UTC" (flags 4); sent to 0 logged-in connections`,
		`system messages for 2000000001 "player": kill switches 2 WalletTopUp; broadcasts 1`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// systemNote waits for a system message and returns its PYLD's TAG and attributes.
func (tc *tcpClient) systemNote() (int64, map[int64]string) {
	tc.t.Helper()
	f := tc.note(ComponentMessaging, NotifyMessageID)
	tdfcheck.Require(tc.t, "Blaze::Messaging::ServerMessage", f.Payload)
	pyld, _ := decode(tc.t, f).Sub("PYLD")
	m, _ := ClientMessageFromRequest(pyld)
	if m.Type != MessageTypeSystem || m.TargetType != (tdf.ObjType{}) || len(m.Targets) != 0 {
		tc.t.Errorf("%s: not a system message: %+v", tc.name, m)
	}
	attrs := map[int64]string{}
	for _, a := range m.Attrs {
		attrs[a.Key] = a.Value
	}
	return m.Tag, attrs
}

// B14 (a): a change of the broadcasts reaches every logged-in client at once (hide all, then the messages); the same
// list again sends nothing; a client that logs in later gets the current list.
func TestBroadcastsPushedToOnlineClients(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) { s.SetBroadcasts(nil) })
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	for _, c := range []*tcpClient{a, b} { // at login: broadcasts on, none yet: hide all
		if tag, attrs := c.systemNote(); tag != SystemTagHideBroadcasts || len(attrs) != 0 {
			t.Errorf("%s: login: tag %d %v", c.name, tag, attrs)
		}
	}
	list := []Broadcast{{Flags: BroadcastMarquee | BroadcastChatLine, Text: "Restart in 5 minutes"}, {Flags: 4, Text: "Welcome"}}
	if !ts.s.SetBroadcasts(list) {
		t.Fatal("SetBroadcasts: no change")
	}
	for _, c := range []*tcpClient{a, b} {
		if tag, _ := c.systemNote(); tag != SystemTagHideBroadcasts {
			t.Errorf("%s: tag %d, want hide all first", c.name, tag)
		}
		for i, want := range list {
			tag, attrs := c.systemNote()
			wantAttrs := map[int64]string{BroadcastAttrID: string(rune('1' + i)), BroadcastAttrFlags: string(rune('0' + want.Flags)),
				BroadcastAttrSeconds: "604800", BroadcastAttrText: want.Text}
			if tag != SystemTagBroadcast || !reflect.DeepEqual(attrs, wantAttrs) {
				t.Errorf("%s: broadcast %d: tag %d %v", c.name, i, tag, attrs)
			}
		}
	}
	if ts.s.SetBroadcasts(list) {
		t.Error("the same list counted as a change")
	}
	a.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	c := ts.client("charlie@example.com")
	if tag, _ := c.systemNote(); tag != SystemTagHideBroadcasts {
		t.Errorf("late login: tag %d", tag)
	}
	for range list {
		if tag, _ := c.systemNote(); tag != SystemTagBroadcast {
			t.Errorf("late login: tag %d", tag)
		}
	}
	ts.s.SetBroadcasts(nil) // taken down: hide all, nothing else
	for _, x := range []*tcpClient{a, b, c} {
		if tag, _ := x.systemNote(); tag != SystemTagHideBroadcasts {
			t.Errorf("%s: tag %d", x.name, tag)
		}
	}
	b.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	if got := ts.s.Broadcasts(); len(got) != 0 {
		t.Errorf("broadcasts left: %+v", got)
	}
}

// B14 (a): the operator's broadcast file is read at once and again every interval; a missing file means no messages
// from it, the static ones (dgserver --broadcast) come first.
func TestWatchBroadcasts(t *testing.T) {
	s := NewServer(newTestLog(t), "127.0.0.1", 17502)
	path := filepath.Join(t.TempDir(), "broadcast.txt")
	static := []Broadcast{{Flags: 4, Text: "Welcome"}}
	stop := s.WatchBroadcasts(path, static, 2, 10*time.Millisecond)
	if got := s.Broadcasts(); len(got) != 1 || got[0].Text != "Welcome" { // read at once, before it returns
		t.Errorf("at the start: %+v", got)
	}
	waitFor := func(what string, want []Broadcast) {
		t.Helper()
		for i := range want {
			want[i].ID = int64(i + 1)
		}
		deadline := time.Now().Add(5 * time.Second)
		for !reflect.DeepEqual(s.Broadcasts(), want) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: broadcasts %+v, want %+v", what, s.Broadcasts(), want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor("no file", []Broadcast{{Flags: 4, Text: "Welcome"}})
	if err := os.WriteFile(path, []byte("Restart at 20:00 UTC\n@4 Thanks for playing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor("file written", []Broadcast{{Flags: 4, Text: "Welcome"}, {Flags: 2, Text: "Restart at 20:00 UTC"},
		{Flags: 4, Text: "Thanks for playing"}})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitFor("file removed", []Broadcast{{Flags: 4, Text: "Welcome"}})
	stop()
	stop() // twice is fine
}

// B14 (b): a PvP party's guest gets the leader's extended data with its wins (UserAdded before NotifyJoinPlaygroup),
// the leader the guest's; a later playgroup (the game's chat) refreshes them as UserSessionExtendedDataUpdate, since
// both clients know each other; when the leader's wins change after a game, the guest gets them at once.
func TestPartyMemberWins(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) {
		if _, _, err := s.Store.Login("player@example.com", time.Unix(testTime, 0)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Store.Update(store.FirstUserID, func(a *store.Account) {
			a.Stats = map[string]map[string]string{progress.PlayerStats: {"WINS": "3"}}
		}); err != nil {
			t.Fatal(err)
		}
	})
	ps.loginAs("player@example.com", 0)
	ps.send(6, 1, 2, ps.request("createplaygroup_party_request"))
	ps.expect(6, 1, fire.Reply, 2, "createplaygroup_party_reply")
	ps.next("NotifyJoinPlaygroup (the leader alone)")
	two := ps.another()
	two.loginAs("second@example.com", 0)
	two.send(6, 3, 2, ps.request("joinplaygroup_request"))
	two.expect(6, 3, fire.Reply, 2, "createplaygroup_party_reply")
	two.expect(ComponentUserSessions, NotifyUserAdded, fire.Notification, 0, "useradded_wins_notification")
	f := two.next("UserUpdated")
	if st := payloadOf(t, "Blaze::UserStatus", f); f.Command != NotifyUserUpdated {
		t.Errorf("UserUpdated: %#x", f.Command)
	} else if flags, _ := st.Int("FLGS"); flags != UserStatusKnown|UserStatusOnline {
		t.Errorf("leader's status %#x, want online", flags)
	}
	if f := two.next("NotifyJoinPlaygroup"); f.Command != NotifyJoinPlaygroupID {
		t.Errorf("party: %#x, want NotifyJoinPlaygroup", f.Command)
	}
	ps.expect(ComponentUserSessions, NotifyUserAdded, fire.Notification, 0, "useradded_notification")
	ps.expect(ComponentUserSessions, NotifyUserUpdated, fire.Notification, 0, "userupdated_online_notification")
	ps.next("NotifyMemberJoinedPlaygroup")

	// the game's chat: both know each other now, so the joiner and the member get the other's data as an update
	ps.send(6, 1, 3, ps.request("createplaygroup_request"))
	ps.next("createPlaygroup reply")
	ps.next("NotifyJoinPlaygroup (GG-1, the leader alone)")
	two.send(6, 1, 3, ps.request("createplaygroup_request"))
	two.next("createPlaygroup reply")
	two.expect(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, fire.Notification, 0, "memberdata_notification")
	if f := two.next("NotifyJoinPlaygroup"); f.Command != NotifyJoinPlaygroupID {
		t.Errorf("GG-1: %#x, want NotifyJoinPlaygroup", f.Command)
	}
	f = ps.next("the guest's data")
	if upd := payloadOf(t, "Blaze::UserSessionExtendedDataUpdate", f); f.Command != NotifyUserSessionExtendedDataUpdate {
		t.Errorf("the guest's data: %#x", f.Command)
	} else if usid, _ := upd.Int("USID"); usid != store.FirstPersonaID+1 {
		t.Errorf("USID %d", usid)
	}
	ps.next("NotifyMemberJoinedPlaygroup (GG-1)")

	// a game changed the leader's wins: every client that knows it (the guest) hears; the leader itself gets its own
	// update from the game's end (gamehost.go), not this one
	if _, err := ps.srv.Store.Update(store.FirstUserID, func(a *store.Account) {
		a.Stats[progress.PlayerStats]["WINS"] = "4"
	}); err != nil {
		t.Fatal(err)
	}
	refreshed := make(chan struct{})
	go func() { ps.srv.refreshKnownUser(store.FirstPersonaID); close(refreshed) }()
	f = two.next("the leader's new wins")
	if upd := payloadOf(t, "Blaze::UserSessionExtendedDataUpdate", f); f.Command != NotifyUserSessionExtendedDataUpdate || winsOf(t, upd) != 4 {
		t.Errorf("after the game: %#x wins %d", f.Command, winsOf(t, upd))
	}
	<-refreshed

	// the guest leaves (its connection closes): the leader hears for both playgroups (a synchronous pipe: read first)
	two.client.Close()
	for _, pg := range []string{"party", "GG-1"} {
		if f := ps.next(pg); f.Command != NotifyMemberRemovedFromPlaygroupID {
			t.Errorf("%s: %#x", pg, f.Command)
		}
	}
	<-two.done
	ps.close()
}

// newTestLog is a logging.Log into the test's output.
func newTestLog(t *testing.T) *logging.Log { return logging.New(testWriter{t}) }

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(b), "\n"))
	return len(b), nil
}
