package blaze

import (
	"slices"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/tdf"
)

func TestParseLobbyLine(t *testing.T) {
	for _, c := range []struct {
		text string
		kind string
		args []string
		ok   bool
	}{
		{"~#rdy hi~", "rdy", []string{"hi"}, true},
		{"~#rdy 1~", "rdy", []string{"1"}, true},
		{"~#rdy  0 ~", "rdy", []string{"0"}, true},
		{"~#rdy~", "rdy", []string{}, true},
		{"~#rdy 1", "", nil, false},                    // cut: no closing ~
		{"hello ~#rdy 1~", "", nil, false},             // not the whole line
		{"~#bots~", "", nil, false},                    // not a lobby line kind (the bot box's name marker)
		{"~#rat 1612~", "rat", []string{"1612"}, true}, // the server's own kind: swallowed from a client too (ignored)
		{"", "", nil, false},
	} {
		kind, args, ok := parseLobbyLine(c.text)
		if ok != c.ok || kind != c.kind || ok && !slices.Equal(args, c.args) {
			t.Errorf("%q: %q %q %v, want %q %q %v", c.text, kind, args, ok, c.kind, c.args, c.ok)
		}
	}
	if got := LobbyLine(LobbyLineReady, "wait"); got != "~#rdy wait~" {
		t.Errorf("LobbyLine %q", got)
	}
}

// joinGG makes the clients members of game 1's chat playgroup GG-1, as their lobbies join it (B11), and returns its id.
func joinGG(t *testing.T, cs ...*tcpClient) int64 {
	t.Helper()
	gg := tdf.NewWriter().Int("JOIN", 1).Struct("PGRP", func(w *tdf.Writer) {
		w.Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV(PlaygroupKeyAttr, "GG-1")).Int("MLIM", 15).String("NAME", "GG-1").String("UKEY", "GG-1")
	}).Bytes()
	var id int64
	for i, x := range cs {
		info, _ := decode(t, x.pgCall(CmdPlaygroupsCreatePlaygroup, gg)).Sub("INFO")
		id, _ = info.Int("PGID")
		x.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
		for _, y := range cs[:i] {
			y.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID)
		}
	}
	return id
}

// readyFrom expects a ready line on tc in the name of from with text want, in the lobby chat's form.
func (tc *tcpClient) readyFrom(from *tcpClient, want string) {
	tc.t.Helper()
	p := tc.msgNote(from)
	if got := attrOf(tc.t, p, MsgAttrText); got != want {
		tc.t.Errorf("%s: a line from %s %q, want %q", tc.name, from.name, got, want)
	}
	if got := attrOf(tc.t, p, MsgAttrTextUnfiltered); got != want {
		tc.t.Errorf("%s: the unfiltered text %q, want %q", tc.name, got, want)
	}
	if typ, _ := p.Int("TYPE"); typ != MessageTypeChat {
		tc.t.Errorf("%s: TYPE %#x", tc.name, typ)
	}
}

// The custom lobby's ready check (ready.go): the lines are swallowed (no other client, no echo), only the clients
// that said hi get the server's lines (in the name of the player whose state it is), the admin and the spectators
// don't count, a client without the class counts as ready, a team change and a rejoin clear a state, and the admin's
// start is refused while a counted player isn't ready (RosterLockedKey "0" to all, ~#rdy wait~ to the admin).
func TestReadyCheck(t *testing.T) {
	ts := newTCPServer(t, nil)
	g := golden.Load(t)
	a, b, c, d := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com"),
		ts.client("delta@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	join := golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, join))
	wantReply(t, "C joins", c.gm(CmdGameManagerJoinGame, join))
	wantReply(t, "D spectates", d.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
	gg := joinGG(t, a, b, c, d)
	line := func(text string) []byte {
		return msg(MessageTypeChat, MessageFlagEcho|MessageFlagFilter, ObjTypePlaygroup, chatLine(text), gg)
	}
	quiet := func(what string, cs ...*tcpClient) {
		t.Helper()
		for _, x := range cs {
			x.none(ComponentMessaging, NotifyMessageID, 60*time.Millisecond)
		}
	}

	// the admin's hi: nobody counted has said hi yet, so it hears nothing (and its own line isn't echoed)
	wantOK(t, "A hi", a.sendMsg(line("~#rdy hi~")))
	quiet("A hi", a, b, c, d)
	// B's hi: B hears its own 0, the admin B's 0; C (no class) and D nothing yet
	wantOK(t, "B hi", b.sendMsg(line("~#rdy hi~")))
	b.readyFrom(b, "~#rdy 0~")
	a.readyFrom(b, "~#rdy 0~")
	quiet("B hi", a, b, c, d)
	// the spectator's hi: it hears B's state; nobody hears of it (spectators don't count)
	wantOK(t, "D hi", d.sendMsg(line("~#rdy hi~")))
	d.readyFrom(b, "~#rdy 0~")
	quiet("D hi", a, b, c)

	// the start while B isn't ready: refused, the roster unlocked for all, the admin told why and sent its own
	// extended data (READY2b: its lobby's roster redraw)
	for _, x := range []*tcpClient{a, b, c, d} {
		x.takeAll(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 50*time.Millisecond)
	}
	wantError(t, "A starts, B not ready", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))),
		ErrInvalidGameStateAction)
	for _, x := range []*tcpClient{a, b, c, d} {
		f := x.note(ComponentGameManager, NotifyGameAttribChangeID)
		if !strings.Contains(string(f.Payload), AttrRosterLocked) {
			t.Errorf("%s: NotifyGameAttribChange without %s", x.name, AttrRosterLocked)
		}
	}
	a.readyFrom(a, "~#rdy wait~")
	quiet("refused", a, b, c, d)
	if usid, _ := decode(t, a.note(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate)).Int("USID"); usid != a.id {
		t.Errorf("the admin's update after the refusal names %d, not %d", usid, a.id)
	}
	for _, x := range []*tcpClient{b, c, d} {
		x.none(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 0)
	}

	// B ready: every class holder hears it (B too), C nothing; a spectator's 1 is ignored
	wantOK(t, "B ready", b.sendMsg(line("~#rdy 1~")))
	for _, x := range []*tcpClient{a, b, d} {
		x.readyFrom(b, "~#rdy 1~")
	}
	wantOK(t, "D ready (a spectator)", d.sendMsg(line("~#rdy 1~")))
	quiet("B ready", a, b, c, d)

	// a team change clears B's state
	swap := tdf.NewWriter().Int("GID", 1).List("LGAM", tdf.TypeStruct, []*tdf.Writer{tdf.NewWriter().Int("PID", b.id).Int("TIDX", 1)}).Bytes()
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	team := hg.playerLocked(b.id).p.Team
	hg.mu.Unlock()
	if team == 1 {
		swap = tdf.NewWriter().Int("GID", 1).List("LGAM", tdf.TypeStruct, []*tdf.Writer{tdf.NewWriter().Int("PID", b.id).Int("TIDX", 0)}).Bytes()
	}
	wantReply(t, "swap B", a.gm(CmdGameManagerSwapPlayersTeam, swap))
	for _, x := range []*tcpClient{a, b, d} {
		x.readyFrom(b, "~#rdy 0~")
	}
	quiet("swap", c)

	// B's rejoin (a new lobby entry): its state goes back to 0 for the others; until its new hi it counts as ready
	wantOK(t, "B ready again", b.sendMsg(line("~#rdy 1~")))
	for _, x := range []*tcpClient{a, b, d} {
		x.readyFrom(b, "~#rdy 1~")
	}
	wantReply(t, "B rejoins", b.gm(CmdGameManagerJoinGame, join))
	for _, x := range []*tcpClient{a, d} {
		x.readyFrom(b, "~#rdy 0~")
	}
	quiet("rejoin", b, c)
	wantOK(t, "B hi again", b.sendMsg(line("~#rdy hi~")))
	b.readyFrom(b, "~#rdy 0~")
	a.readyFrom(b, "~#rdy 0~")
	d.readyFrom(b, "~#rdy 0~")
	wantOK(t, "B ready", b.sendMsg(line("~#rdy 1~")))
	for _, x := range []*tcpClient{a, b, d} {
		x.readyFrom(b, "~#rdy 1~")
	}
	// the game target works like its playgroup; C (no class) counts as ready, so the start goes
	wantOK(t, "B ready to the game", b.sendMsg(msg(MessageTypeChat, MessageFlagEcho, ObjTypeGame, chatLine("~#rdy 1~"), 1)))
	for _, x := range []*tcpClient{a, b, d} {
		x.readyFrom(b, "~#rdy 1~")
	}
	quiet("before the start", a, b, c, d)
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	// after the start lines are still swallowed and change nothing
	wantOK(t, "B after the start", b.sendMsg(line("~#rdy 0~")))
	quiet("after the start", a, b, c, d)
	// an ordinary chat line is relayed as before
	wantOK(t, "chat", c.sendMsg(line("gg ~#rdy 1~")))
	for _, x := range []*tcpClient{a, b, c, d} {
		x.msgNote(c)
	}

	log := ts.log.String()
	for _, want := range []string{
		`a lobby line "~#rdy hi~" for game 1; not relayed`,
		"advanceGameState IN_GAME refused: not ready [2000000002]",
		`ready check: "1" from 2000000004: not counted (the admin or a spectator); ignored`,
		`ready check: "0" from 2000000002: not in the lobby (or it started); ignored`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log without %q", want)
		}
	}
}

// The test aid Server.TestRefuseStart (dgserver --test-refuse-start, READY2b): a custom game's first start is refused
// as the ready check's backstop refuses one (RosterLockedKey "0" to all, the admin's own extended data), the second
// goes.
func TestRefuseStartTestAid(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) { s.TestRefuseStart = true })
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	a.takeAll(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 50*time.Millisecond)
	start := golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))
	wantError(t, "the first start", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	for _, x := range []*tcpClient{a, b} {
		x.note(ComponentGameManager, NotifyGameAttribChangeID)
	}
	if usid, _ := decode(t, a.note(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate)).Int("USID"); usid != a.id {
		t.Errorf("the admin's update names %d, not %d", usid, a.id)
	}
	wantReply(t, "the second start", a.gm(CmdGameManagerAdvanceGameState, start))
	if log := ts.log.String(); !strings.Contains(log, "refused: not ready [] (the ready check) (--test-refuse-start") {
		t.Errorf("log without the test aid's refusal")
	}
}
