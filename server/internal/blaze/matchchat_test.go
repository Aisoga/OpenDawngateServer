package blaze

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// seat makes personas players of game on team in the server's record (a game of a server without match hosts), so a
// test that isn't about the game itself may use its match chats.
func seat(s *Server, game, team int64, personas ...int64) {
	for _, p := range personas {
		s.unhosted.add(game, p, team)
	}
}

// seatFirst is a pipe session's setup that seats the first n accounts' personas in game 1 on team 0.
func seatFirst(n int) func(s *Server) {
	return func(s *Server) {
		for i := range int64(n) {
			seat(s, 1, 0, store.FirstPersonaID+i)
		}
	}
}

// chatCreate is createPlaygroup for key as the client sends it (JOIN 1, B11), with the member limit and name it asks
// for (the client asks MLIM 15, NAME = the key).
func chatCreate(key string, limit int64, name string) []byte {
	return tdf.NewWriter().Int("JOIN", 1).Struct("PGRP", func(w *tdf.Writer) {
		w.Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV(PlaygroupKeyAttr, key)).Int("MLIM", limit).String("NAME", name).String("UKEY", key)
	}).Bytes()
}

func TestParseMatchChatKey(t *testing.T) {
	for _, c := range []struct {
		key  string
		want matchChat
		ok   bool
	}{
		{"GG-1", matchChat{game: 1}, true},
		{"GG-123", matchChat{game: 123}, true},
		{"TG-7-0", matchChat{game: 7, team: 0, tg: true}, true},
		{"TG-7-2", matchChat{game: 7, team: 2, tg: true}, true},
		{"TG-7-3", matchChat{}, false},
		{"GG-0", matchChat{}, false},
		{"GG-01", matchChat{}, false},
		{"GG-+1", matchChat{}, false},
		{"GG--1", matchChat{}, false},
		{"GG-", matchChat{}, false},
		{"GG-1-0", matchChat{}, false},
		{"TG-7", matchChat{}, false},
		{"TG-7-0-1", matchChat{}, false},
		{"TG-07-0", matchChat{}, false},
		{"party", matchChat{}, false},
	} {
		got, ok := parseMatchChatKey(c.key)
		if ok != c.ok || ok && got != c.want {
			t.Errorf("%q: %+v %v, want %+v %v", c.key, got, ok, c.want, c.ok)
		}
	}
	if !isMatchChatKey("GG-x") || !isMatchChatKey("TG-") || isMatchChatKey("gg-1") || isMatchChatKey("") {
		t.Error("isMatchChatKey")
	}
}

// A custom lobby without a match host (game 1): the admin A on team 0, B on team 1, S a spectator, X logged in but in
// no game. Only the game's players get into GG-1, only a team's players and the spectators into its TG-; an outsider
// can't make a match chat first (nor choose its owner or limit), nor join one by its id; the server's info stands
// whatever the request asks; the lobby commands don't work on a match chat; a team change and a kick take the player
// out of the chats it may no longer be in, and it gets no more of their lines.
func TestMatchChatMembership(t *testing.T) {
	ts := newTCPServer(t, nil)
	g := golden.Load(t)
	a, b, sp, x := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("spec@example.com"),
		ts.client("xray@example.com")

	// X first: no game 1 yet, then a game it isn't in
	wantErr(t, "X: GG-1 before the game", x.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate("GG-1", 15, "GG-1")), ErrPlaygroupNotAuthorized)
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	b.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "S spectates", sp.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
	sp.note(ComponentGameManager, NotifyGameSetupID)
	for _, k := range []string{"GG-1", "TG-1-0", "TG-1-1", "TG-1-2"} {
		wantErr(t, "X: pre-create "+k, x.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate(k, 1, "mine")), ErrPlaygroupNotAuthorized)
	}
	if n := ts.s.groups.count(); n != 0 {
		t.Fatalf("%d playgroups after the outsider's attempts", n)
	}

	// A makes GG-1 asking for one member and another name: the server's info (MLIM 15, NAME the key)
	f := a.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate("GG-1", 1, "evil"))
	wantOK(t, "A: GG-1", f)
	info, _ := decode(t, f).Sub("INFO")
	gg, _ := info.Int("PGID")
	if lim, _ := info.Int("MLIM"); lim != MatchChatMemberLimit {
		t.Errorf("MLIM %d", lim)
	}
	if name, _ := info.Str("NAME"); name != "GG-1" {
		t.Errorf("NAME %q", name)
	}
	a.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	for _, c := range []*tcpClient{b, sp} { // the limit A asked for keeps nobody out
		wantOK(t, c.name+": GG-1", c.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate("GG-1", 15, "GG-1")))
		c.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	}
	wantErr(t, "X: GG-1", x.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate("GG-1", 15, "GG-1")), ErrPlaygroupNotAuthorized)
	wantErr(t, "X: GG-1 by id", x.pgCall(CmdPlaygroupsJoinPlaygroup, tdf.NewWriter().Int("PGID", gg).Bytes()), ErrPlaygroupNotAuthorized)
	wantErr(t, "X: GG-1 by key", x.pgCall(CmdPlaygroupsJoinPlaygroup, tdf.NewWriter().String("UKEY", "GG-1").Bytes()), ErrPlaygroupNotAuthorized)
	wantErr(t, "A: GG-01", a.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate("GG-01", 15, "GG-01")), ErrPlaygroupNotAuthorized)

	// the team chats: own team yes, the enemy's no, the spectators every one
	join := func(c *tcpClient, key string) int64 {
		t.Helper()
		f := c.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate(key, 15, key))
		wantOK(t, c.name+": "+key, f)
		c.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
		info, _ := decode(t, f).Sub("INFO")
		id, _ := info.Int("PGID")
		return id
	}
	tg0 := join(a, "TG-1-0")
	wantErr(t, "B: the enemy's TG-1-0", b.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate("TG-1-0", 15, "TG-1-0")), ErrPlaygroupNotAuthorized)
	wantErr(t, "B: TG-1-0 by id", b.pgCall(CmdPlaygroupsJoinPlaygroup, tdf.NewWriter().Int("PGID", tg0).Bytes()), ErrPlaygroupNotAuthorized)
	wantErr(t, "A: the spectators' TG-1-2", a.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate("TG-1-2", 15, "TG-1-2")), ErrPlaygroupNotAuthorized)
	tg1 := join(b, "TG-1-1")
	for _, k := range []string{"TG-1-0", "TG-1-1", "TG-1-2"} {
		join(sp, k)
	}
	a.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID) // S in TG-1-0
	b.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID) // S in TG-1-1
	for _, c := range []*tcpClient{a, b} {                     // B and S in GG-1 (A); S in GG-1 (B)
		c.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID)
	}
	a.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID)
	wantErr(t, "X: TG-1-1", x.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate("TG-1-1", 15, "TG-1-1")), ErrPlaygroupNotAuthorized)

	// a team line reaches its team and the spectators, not the enemy
	wantOK(t, "A's team line", a.sendMsg(msg(MessageTypeChat, MessageFlagEcho, ObjTypePlaygroup, chatLine("push mid"), tg0)))
	a.msgNote(a)
	sp.msgNote(a)
	b.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	wantErr(t, "B into TG-1-0", b.sendMsg(msg(MessageTypeChat, 0, ObjTypePlaygroup, chatLine("x"), tg0)), ErrMessagingTargetNotFound)

	// the lobby commands: not on a match chat, not even for its owner
	wantErr(t, "A kicks B from GG-1", a.pgCall(CmdPlaygroupsKickPlaygroupMember, tdf.NewWriter().Int("EID", b.id).Int("PGID", gg).Bytes()),
		ErrPlaygroupNotAuthorized)
	wantErr(t, "A closes GG-1", a.pgCall(CmdPlaygroupsSetPlaygroupJoinControls, tdf.NewWriter().Int("OPEN", 1).Int("PGID", gg).Bytes()),
		ErrPlaygroupNotAuthorized)
	wantErr(t, "A destroys GG-1", a.pgCall(CmdPlaygroupsDestroyPlaygroup, tdf.NewWriter().Int("PGID", gg).Bytes()), ErrPlaygroupNotAuthorized)

	// the admin moves the spectator to team 1 (index 0): S leaves TG-1-1 and TG-1-2, stays in TG-1-0
	swap := func(pid, team int64) []byte {
		return tdf.NewWriter().Int("GID", 1).List("LGAM", tdf.TypeStruct, []*tdf.Writer{
			tdf.NewWriter().Int("PID", pid).Int("TIDX", team)}).Bytes()
	}
	wantReply(t, "S to team index 0", a.gm(CmdGameManagerSwapPlayersTeam, swap(sp.id, 0)))
	rm := decode(t, b.note(ComponentPlaygroups, NotifyMemberRemovedFromPlaygroupID))
	if pg, _ := rm.Int("PGID"); pg != tg1 {
		t.Errorf("B heard of a removal from %d, want TG-1-1 %d", pg, tg1)
	}
	if m, _ := rm.Int("MLST"); m != sp.id {
		t.Errorf("removed %d, want S", m)
	}
	a.none(ComponentPlaygroups, NotifyMemberRemovedFromPlaygroupID, 50*time.Millisecond)
	wantOK(t, "B's team line", b.sendMsg(msg(MessageTypeChat, MessageFlagEcho, ObjTypePlaygroup, chatLine("care"), tg1)))
	b.msgNote(b)
	sp.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	// the moved client leaves the old groups itself: nothing left to remove, an empty reply
	wantOK(t, "S leaves TG-1-1", sp.pgCall(CmdPlaygroupsLeavePlaygroup, tdf.NewWriter().Int("PGID", tg1).Bytes()))

	// B moves to team index 0: out of TG-1-1 (empty, gone), into TG-1-0
	wantReply(t, "B to team index 0", a.gm(CmdGameManagerSwapPlayersTeam, swap(b.id, 0)))
	join(b, "TG-1-0")

	// the admin kicks B: out of GG-1 and TG-1-0, and none of their lines reach it
	kick := tdf.NewWriter().Int("CNTX", 0).Int("GID", 1).Int("PID", b.id).Int("REAS", RemovedPlayerKicked).Bytes()
	wantReply(t, "A kicks B", a.gm(CmdGameManagerRemovePlayer, kick))
	b.note(ComponentGameManager, NotifyPlayerRemovedID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		m1, _ := ts.s.groups.memberIDs(gg, "")
		m2, _ := ts.s.groups.memberIDs(tg0, "")
		if !containsID(m1, b.id) && !containsID(m2, b.id) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("B still in GG-1 %v or TG-1-0 %v", m1, m2)
		}
		time.Sleep(10 * time.Millisecond)
	}
	wantOK(t, "A's line after the kick", a.sendMsg(msg(MessageTypeChat, MessageFlagEcho, ObjTypeGame, chatLine("bye"), 1)))
	a.msgNote(a)
	b.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	wantErr(t, "B: GG-1 after the kick", b.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate("GG-1", 15, "GG-1")), ErrPlaygroupNotAuthorized)

	log := ts.log.String()
	for _, want := range []string{
		`playgroup "GG-1": refused for 2000000004 (not a player of game 1); error 0x0002`,
		`playgroup "TG-1-0": refused for 2000000002 (on team 1 of game 1, not team 0); error 0x0002`,
		`playgroup "GG-01": refused for 2000000001 (not a match chat key of the client's form); error 0x0002`,
		`join refused for 2000000004 (not a player of game 1)`,
		`"TG-1-1": 2000000003 taken out (on team 0 of game 1, not team 1)`,
		`"GG-1": 2000000002 taken out (not a player of game 1)`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// A server without match hosts: a practice game's player gets its chats, another player doesn't; after its
// removePlayer the player is out of them; a party (no key) works for anyone.
func TestMatchChatUnhosted(t *testing.T) {
	ts := newTCPServer(t, nil)
	g := golden.Load(t)
	a, x := ts.client("alpha@example.com"), ts.client("xray@example.com")
	wantReply(t, "practice", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "creategame_request"))))
	setup := decode(t, a.note(ComponentGameManager, NotifyGameSetupID))
	game, _ := setup.Sub("GAME")
	gid, _ := game.Int("GID")
	key := "GG-" + itoa(gid)
	wantOK(t, "A: "+key, a.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate(key, 15, key)))
	a.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	wantOK(t, "A: TG-"+itoa(gid)+"-0", a.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate("TG-"+itoa(gid)+"-0", 15, "x")))
	wantErr(t, "X: "+key, x.pgCall(CmdPlaygroupsCreatePlaygroup, chatCreate(key, 15, key)), ErrPlaygroupNotAuthorized)
	// a party: any player, any limit
	wantOK(t, "X: a party", x.pgCall(CmdPlaygroupsCreatePlaygroup, pvpCreate()))
	leave := tdf.NewWriter().Int("CNTX", 0).Int("GID", gid).Int("PID", a.id).Int("REAS", 6).Bytes()
	wantReply(t, "A leaves", a.gm(CmdGameManagerRemovePlayer, leave))
	if _, in := ts.s.matchTeamOf(gid, a.id); in {
		t.Error("A still in the game after its removePlayer")
	}
	if ids, ok := ts.s.groups.memberIDs(0, key); ok && containsID(ids, a.id) {
		t.Errorf("A still in %s: %v", key, ids)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
