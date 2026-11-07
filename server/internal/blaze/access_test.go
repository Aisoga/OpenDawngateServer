package blaze

import (
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/limits"
	"dawngate/server/internal/tdf"
)

// A connection without a login gets an answer to everything, but only logging in does anything. What it may ask is a short
// list, and each entry on it touches only the connection or gives nothing away.
func TestRequestsNeedALogin(t *testing.T) {
	ts := newTCPServer(t, nil)
	v := ts.client("victim@example.com")
	a := ts.dial("nobody")
	for _, c := range []struct {
		comp, cmd uint16
		what      string
		body      []byte
	}{
		{ComponentUserSessions, CmdUserSessionsLookupUser, "lookupUser", tdf.NewWriter().String("NAME", v.name).Bytes()},
		{ComponentBattlegroundsAccounts, CmdBGAGetBasicData, "getBasicData", tdf.NewWriter().List("BZID", tdf.TypeInt, []int64{v.id}).Bytes()},
		{ComponentBattlegroundsAccounts, CmdBGAGetPerkPageData, "getPerkPageData", tdf.NewWriter().Int("USER", v.id).Bytes()},
		{ComponentStats, CmdStatsGetLeaderboard, "getLeaderboard", tdf.NewWriter().String("NAME", "Skill").Int("COUN", 200).Bytes()},
		{ComponentGameManager, CmdGameManagerGetGameDataFromID, "getGameDataFromId", tdf.NewWriter().List("GLST", tdf.TypeInt, []int64{1}).Bytes()},
		{ComponentGameManager, CmdGameManagerCancelMatchmaking, "cancelMatchmaking", tdf.NewWriter().Int("MSID", 1).Bytes()},
		{ComponentGameManager, CmdGameManagerRemovePlayer, "removePlayer", tdf.NewWriter().Int("GID", 1).Int("PID", v.id).Bytes()},
		{ComponentUtil, CmdUtilFilterForUText, "filterForProfanity", nil},
	} {
		f := a.reply(c.comp, c.cmd, a.send(c.comp, c.cmd, c.body))
		if f.Type != fire.ErrorReply || f.Error != ErrAuthenticationRequired {
			t.Errorf("%s before a login: %s %#x, want ERR_AUTHENTICATION_REQUIRED", c.what, f.Type, f.Error)
		}
	}
	for _, c := range []struct {
		comp, cmd uint16
		what      string
	}{
		{ComponentUtil, CmdUtilPing, "ping"},
		{ComponentBGEcommerce, CmdBGEGetBalance, "getBalance"},
		{ComponentRooms, CmdRoomsSelectViewUpdate, "selectViewUpdates"},
	} {
		if f := a.reply(c.comp, c.cmd, a.send(c.comp, c.cmd, nil)); f.Type != fire.Reply {
			t.Errorf("%s before a login, which a real client sends: %s %#x", c.what, f.Type, f.Error)
		}
	}
}

// A matchmaking session is cancelled by its own players only: MSIDs count from 1.
func TestCancelOnlyYourOwnQueue(t *testing.T) {
	ts := mmServer(t, 5, 10, time.Hour) // nothing forms
	victim := ts.client("victim@example.com")
	f := victim.gm(CmdGameManagerStartMatchmaking, startMatchmakingRequest(600000))
	wantReply(t, "queue", f)
	msid, _ := decode(t, f).Int("MSID")
	other := ts.client("other@example.com")
	wantReply(t, "someone else's cancel", other.gm(CmdGameManagerCancelMatchmaking, tdf.NewWriter().Int("MSID", msid).Bytes()))
	if ts.s.MM.Len() != 1 {
		t.Fatalf("the queue after someone else's cancel: %d", ts.s.MM.Len())
	}
	wantReply(t, "its own cancel", victim.gm(CmdGameManagerCancelMatchmaking, tdf.NewWriter().Int("MSID", msid).Bytes()))
	victim.note(ComponentGameManager, NotifyMatchmakingFailedID)
	if ts.s.MM.Len() != 0 {
		t.Fatalf("the queue after its own cancel: %d", ts.s.MM.Len())
	}
}

// A client's team index can't make the server allocate: the roster and the game's data are sized by it.
func TestHugeTeamIndexIsNotAllocated(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	a := ts.client("alpha@example.com")
	req := tdf.NewWriter().String("GNAM", "x").Int("TIDX", 1<<40).
		Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV("GameFlags", "1292")).Bytes()
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, req))
	cg := <-host.created
	if cg.Roster[0].Team != 1 { // team index 0, the host's team 1
		t.Errorf("createGame's team %d", cg.Roster[0].Team)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	wantReply(t, "game data", a.gm(CmdGameManagerGetGameDataFromID, tdf.NewWriter().List("GLST", tdf.TypeInt, []int64{cg.GameID}).Bytes()))
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
		t.Errorf("one request allocated %d MiB", grew>>20)
	}
}

// A practice game can't claim to be a matchmade or ranked one: the flags pick the rewards.
func TestPracticeFlagsAreTheServers(t *testing.T) {
	for in, want := range map[string]string{
		"1292": "1292", // a real solo practice: SoloPractice is set, nothing changes
		"1":    "8",    // "matchmade"
		"32":   "8",    // "ranked"
		"33":   "8",    //
		"2":    "2",    // a custom game
		"1042": "1042", // a practice group
		"3":    "2",    // a custom game claiming to be matchmade
	} {
		g := Game{Attrs: []tdf.Pair{tdf.KV("GameFlags", in)}}
		practiceFlags(&g)
		if got := attrValue(g.Attrs, "GameFlags"); got != want {
			t.Errorf("GameFlags %s: %s, want %s", in, got, want)
		}
	}
}

// A client that stops reading loses its connection and nobody else waits for it (the audit's case: one chat message to it
// used to hang the sender's connection).
func TestSlowReaderStallsNobody(t *testing.T) {
	ts := newTCPServer(t, nil)
	victim := ts.client("victim@example.com")
	raw := rawLogin(t, ts, "stalled@example.com")
	item := tdf.NewWriter().String("UTXT", strings.Repeat("x", 60000))
	payload := tdf.NewWriter().List("UTXT", tdf.TypeStruct, []*tdf.Writer{item}).Bytes()
	raw.SetWriteDeadline(time.Now().Add(3 * time.Second))
	for i := range 2000 { // fills the socket's buffers and the queue: nothing is read
		b, _ := fire.Encode(fire.Frame{Component: ComponentUtil, Command: CmdUtilFilterForUText, Type: fire.Request, ID: uint32(i + 10), Payload: payload})
		if _, err := raw.Write(b); err != nil {
			break
		}
	}
	msg := tdf.NewWriter().Map("ATTR", tdf.TypeInt, tdf.TypeString, tdf.KV(int64(MsgAttrText), "hi")).Int("FLAG", 0).Int("STAT", 0).Int("TAG", 0).
		List("TIDS", tdf.TypeInt, []int64{rawPersona}).Value("TTYP", tdf.TypeObjType, ObjTypeUser).Int("TYPE", MessageTypeChat).Bytes()
	start := time.Now()
	victim.reply(ComponentMessaging, CmdMsgSendMessage, victim.send(ComponentMessaging, CmdMsgSendMessage, msg))
	ping := victim.send(ComponentUtil, CmdUtilPing, nil)
	if f := victim.reply(ComponentUtil, CmdUtilPing, ping); f.Type != fire.Reply {
		t.Fatalf("the sender's ping: %s %#x", f.Type, f.Error)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the sender waited %s for a client that doesn't read", d)
	}
}

var rawPersona int64

// rawLogin logs in on a raw TCP connection that nobody reads after the login.
func rawLogin(t *testing.T, ts *tcpServer, email string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp4", ts.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetReadBuffer(4096)
	}
	r := fire.NewReader(c)
	send := func(cmd uint16, id uint32, payload []byte) {
		b, _ := fire.Encode(fire.Frame{Component: 1, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
		c.Write(b)
	}
	send(0x28, 1, tdf.NewWriter().String("MAIL", email).String("PASS", testPassword).String("TOKN", "").Int("TYPE", 0).Bytes())
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := r.Next(); err != nil {
		t.Fatal(err)
	}
	send(0x6E, 2, tdf.NewWriter().String("PNAM", "stalled").Bytes())
	for {
		f, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if f.Command == 0x6E && f.Type == fire.Reply {
			st, err := tdf.Decode(f.Payload)
			if err != nil {
				t.Fatal(err)
			}
			rawPersona, _ = st.Int("BUID")
			break
		}
	}
	c.SetReadDeadline(time.Time{})
	return c
}

// A private game takes those with its owner's token, and a removed player doesn't come back.
func TestPrivateGameAndKicks(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	a, b, c := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com")
	req := tdf.NewWriter().String("GNAM", "x").Int("TCAP", 5).List("TIDS", tdf.TypeInt, []int64{1, 2, 3}).
		Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV("GameFlags", "2"), tdf.KV(AttrPrivateMatch, "1")).Bytes()
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, req))
	cg := <-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	join := func(token string, team int64) *tdf.Writer {
		w := tdf.NewWriter().Int("GID", cg.GameID).Int("TIDX", team)
		if token != "" {
			w.Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV(AttrPrivateAuthToken, token))
		}
		return w
	}
	denied := func(what string, f fire.Frame) {
		t.Helper()
		if f.Type != fire.ErrorReply || f.Error != ErrPermissionDenied {
			t.Errorf("%s: %s %#x", what, f.Type, f.Error)
		}
	}
	denied("no token", b.gm(CmdGameManagerJoinGame, join("", 0).Bytes()))
	denied("no token, a spectator", b.gm(CmdGameManagerJoinGame, join("", SpectatorTeam).Bytes()))
	denied("a made-up token before the owner gave one", b.gm(CmdGameManagerJoinGame, join("77AA59D2", 0).Bytes()))
	// the owner answers bravo's right password: the relayed message, 0x6EBDC8E6
	answer := tdf.NewWriter().Map("ATTR", tdf.TypeInt, tdf.TypeString, tdf.KV(int64(0xB0000), fmt.Sprint(cg.GameID)), tdf.KV(int64(0xB0001), "1"), tdf.KV(int64(0xB0002), "77AA59D2")).
		Int("FLAG", 0).Int("STAT", 0).Int("TAG", 0).List("TIDS", tdf.TypeInt, []int64{b.id}).Value("TTYP", tdf.TypeObjType, ObjTypeUser).Int("TYPE", MessageTypePrivateAuthAnswer).Bytes()
	wantReply(t, "the owner's answer", a.reply(ComponentMessaging, CmdMsgSendMessage, a.send(ComponentMessaging, CmdMsgSendMessage, answer)))
	denied("the wrong token", c.gm(CmdGameManagerJoinGame, join("00000000", 0).Bytes()))
	wantReply(t, "the owner's token", b.gm(CmdGameManagerJoinGame, join("77AA59D2", 0).Bytes()))
	// someone who isn't its owner can't name a token
	forged := tdf.NewWriter().Map("ATTR", tdf.TypeInt, tdf.TypeString, tdf.KV(int64(0xB0000), fmt.Sprint(cg.GameID)), tdf.KV(int64(0xB0001), "1"), tdf.KV(int64(0xB0002), "FFFFFFFF")).
		Int("FLAG", 0).Int("STAT", 0).Int("TAG", 0).List("TIDS", tdf.TypeInt, []int64{c.id}).Value("TTYP", tdf.TypeObjType, ObjTypeUser).Int("TYPE", MessageTypePrivateAuthAnswer).Bytes()
	b.reply(ComponentMessaging, CmdMsgSendMessage, b.send(ComponentMessaging, CmdMsgSendMessage, forged))
	denied("a token a player made up", c.gm(CmdGameManagerJoinGame, join("FFFFFFFF", 0).Bytes()))
	// a kick stays
	wantReply(t, "the kick", a.gm(CmdGameManagerRemovePlayer, tdf.NewWriter().Int("GID", cg.GameID).Int("PID", b.id).Int("REAS", RemovedPlayerKicked).Bytes()))
	denied("the removed player, back", b.gm(CmdGameManagerJoinGame, join("77AA59D2", 0).Bytes()))
}

// A party takes those its members invited: parties are found by counting ids.
func TestPartyTakesTheInvited(t *testing.T) {
	ts := newTCPServer(t, nil)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	f := a.pgCall(CmdPlaygroupsCreatePlaygroup, pvpCreate())
	info, _ := decode(t, f).Sub("INFO")
	party, _ := info.Int("PGID")
	a.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	join := tdf.NewWriter().Int("PGID", party).Bytes()
	if f := b.pgCall(CmdPlaygroupsJoinPlaygroup, join); f.Error != ErrPlaygroupNotAuthorized {
		t.Fatalf("a stranger's join: %s %#x", f.Type, f.Error)
	}
	// someone outside the party can't invite anyone into it
	invite := func(c *tcpClient, to int64) {
		msg := tdf.NewWriter().Map("ATTR", tdf.TypeInt, tdf.TypeString, tdf.KV(int64(MsgAttrSenderName), c.name), tdf.KV(int64(MsgAttrPlaygroupID), fmt.Sprint(party))).
			Int("FLAG", 0).Int("STAT", 0).Int("TAG", 0).List("TIDS", tdf.TypeInt, []int64{to}).Value("TTYP", tdf.TypeObjType, ObjTypeUser).Int("TYPE", MessageTypePvPInvite).Bytes()
		c.reply(ComponentMessaging, CmdMsgSendMessage, c.send(ComponentMessaging, CmdMsgSendMessage, msg))
	}
	invite(b, b.id)
	if f := b.pgCall(CmdPlaygroupsJoinPlaygroup, join); f.Error != ErrPlaygroupNotAuthorized {
		t.Fatalf("after its own invite: %s %#x", f.Type, f.Error)
	}
	invite(a, b.id)
	wantOK(t, "the invited player's join", b.pgCall(CmdPlaygroupsJoinPlaygroup, join))
}

// An account's settings stay within what fits one reply.
func TestSettingsBounded(t *testing.T) {
	ts := newTCPServer(t, nil)
	a := ts.client("alpha@example.com")
	save := func(k, v string) fire.Frame {
		req := tdf.NewWriter().Map("USET", tdf.TypeString, tdf.TypeString, tdf.KV(k, v)).Bytes()
		return a.reply(ComponentBattlegroundsAccounts, CmdBGASaveUserSettings, a.send(ComponentBattlegroundsAccounts, CmdBGASaveUserSettings, req))
	}
	wantReply(t, "a real setting", save("LOADOUT_0x12345678", "1,2,3"))
	if f := save("big", strings.Repeat("v", maxSettingValue+1)); f.Type != fire.ErrorReply {
		t.Errorf("a value over the limit: %s", f.Type)
	}
	for i := 0; ; i++ {
		f := save(fmt.Sprintf("k%d", i), strings.Repeat("v", maxSettingValue))
		if f.Type == fire.ErrorReply {
			break
		}
		if i > 64 {
			t.Fatal("no limit on the settings' bytes")
		}
	}
	got := a.reply(ComponentBattlegroundsAccounts, CmdBGAGetUserSettings, a.send(ComponentBattlegroundsAccounts, CmdBGAGetUserSettings, nil))
	wantReply(t, "the settings still fit a frame", got)
}

// Wrong passwords from one address don't lock the account's owner out from another.
func TestAccountLockIsPerAddress(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) { s.Limits = limits.New(limits.Default(), nil) })
	ts.client("victim@example.com").c.Close()
	time.Sleep(50 * time.Millisecond)
	attacker := ts.dialFrom("attacker", "127.0.0.2")
	var last fire.Frame
	for range 7 {
		id := attacker.send(1, 0x28, tdf.NewWriter().String("MAIL", "victim@example.com").String("PASS", "wrong-password").Bytes())
		last = attacker.reply(1, 0x28, id)
	}
	if last.Error != errLimitServerBusy {
		t.Errorf("the guesser isn't locked: %s %#x", last.Type, last.Error)
	}
	v := ts.dialFrom("victim", "127.0.0.3")
	id := v.send(1, 0x28, tdf.NewWriter().String("MAIL", "victim@example.com").String("PASS", testPassword).Bytes())
	if f := v.reply(1, 0x28, id); f.Type != fire.Reply {
		t.Errorf("the owner from another address: %s %#x", f.Type, f.Error)
	}
}
