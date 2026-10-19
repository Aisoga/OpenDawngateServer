package blaze

import (
	"encoding/hex"
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

// The join-code replies and the server's invites against tdf_golden.py's DISCORD3A_CASES; the invites also against
// the client's ServerMessage class.
func TestJoinCodesGolden(t *testing.T) {
	g := golden.Load(t)
	second := store.FirstPersonaID + 1
	invite := func(id, kind, target int64) []byte {
		return MessageNotification(ServerMessage{ID: id, Name: "player", Source: store.FirstPersonaID, Time: testTime,
			Payload: JoinInviteMessage(kind, target, "player", JoinInviteFirst, second)})
	}
	cases := []struct {
		name string
		got  []byte
	}{
		{"issuejoincode_reply", JoinCodeIssueReply(JoinCodeInfo{Code: "q83vEjRWeJCrze8SNFZ4kA", Joinable: true,
			Kind: JoinKindParty, Max: 5, Size: 2})},
		{"issuejoincode_private_reply", JoinCodeIssueReply(JoinCodeInfo{Kind: JoinKindCustomGame, Max: 10,
			Reason: JoinPrivate, Size: 1})},
		{"redeemjoincode_reply", JoinCodeRedeemReply(JoinCodeRedeemed{ID: 1, Kind: JoinKindParty})},
		{"notifymessage_joincode_party_notification", invite(5, JoinKindParty, 1)},
		{"notifymessage_joincode_practice_notification", invite(6, JoinKindPracticeGroup, 7)},
		{"notifymessage_joincode_custom_notification", invite(7, JoinKindCustomGame, 7)},
	}
	for _, c := range cases {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
		if strings.HasPrefix(c.name, "notifymessage_") {
			tdfcheck.Require(t, "Blaze::Messaging::ServerMessage", c.got)
		}
	}
	in, err := ParseJoinCodeIssueReply(cases[0].got)
	if err != nil || in != (JoinCodeInfo{Code: "q83vEjRWeJCrze8SNFZ4kA", Joinable: true, Kind: 1, Max: 5, Size: 2}) {
		t.Errorf("ParseJoinCodeIssueReply %+v %v", in, err)
	}
	out, err := ParseJoinCodeRedeemReply(cases[2].got)
	if err != nil || out != (JoinCodeRedeemed{ID: 1, Kind: 1}) {
		t.Errorf("ParseJoinCodeRedeemReply %+v %v", out, err)
	}
	if _, err := ParseJoinCodeIssueReply(tdf.NewWriter().String("CODE", "x").Bytes()); err == nil {
		t.Error("a reply without RSN parsed")
	}
	// the requests as the launcher writes them
	st := decode(t, fire.Frame{Payload: JoinCodeIssueRequest(JoinTargetGame, 7)})
	if k, _ := st.Int("KIND"); k != JoinTargetGame {
		t.Errorf("issue request %v", st)
	}
	if id, _ := st.Int("ID"); id != 7 {
		t.Errorf("issue request %v", st)
	}
	st = decode(t, fire.Frame{Payload: JoinCodeRedeemRequest("abc")})
	if c, _ := st.Str("CODE"); c != "abc" {
		t.Errorf("redeem request %v", st)
	}
	if randomJoinCode() == randomJoinCode() || len(randomJoinCode()) != 22 {
		t.Error("random codes")
	}
}

func (tc *tcpClient) issueCode(target, id int64) JoinCodeInfo {
	tc.t.Helper()
	f := tc.reply(ComponentJoinCodes, CmdJoinCodesIssue, tc.send(ComponentJoinCodes, CmdJoinCodesIssue, JoinCodeIssueRequest(target, id)))
	if f.Type != fire.Reply {
		tc.t.Fatalf("%s: issueJoinCode: %s", tc.name, f.Type)
	}
	in, err := ParseJoinCodeIssueReply(f.Payload)
	if err != nil {
		tc.t.Fatal(err)
	}
	return in
}

func (tc *tcpClient) redeemCode(code string) JoinCodeRedeemed {
	tc.t.Helper()
	f := tc.reply(ComponentJoinCodes, CmdJoinCodesRedeem, tc.send(ComponentJoinCodes, CmdJoinCodesRedeem, JoinCodeRedeemRequest(code)))
	if f.Type != fire.Reply {
		tc.t.Fatalf("%s: redeemJoinCode: %s", tc.name, f.Type)
	}
	out, err := ParseJoinCodeRedeemReply(f.Payload)
	if err != nil {
		tc.t.Fatal(err)
	}
	return out
}

func wantRSN(t *testing.T, what string, got, want int64) {
	t.Helper()
	if got != want {
		t.Errorf("%s: RSN %d (%s), want %d (%s)", what, got, JoinReasonName(got), want, JoinReasonName(want))
	}
}

// inviteFrom waits for the server's invite in from's name and checks it: SRCE and NAME the inviter, TYPE, the target's
// id under idKey, an invite number from JoinInviteFirst on, 0xB0006 "0". It returns the invite number.
func (tc *tcpClient) inviteFrom(from *tcpClient, typ, idKey, id int64) string {
	tc.t.Helper()
	pyld := tc.msgNote(from)
	if got, _ := pyld.Int("TYPE"); got != typ {
		tc.t.Errorf("%s: invite TYPE %#x, want %#x", tc.name, got, typ)
	}
	if got := attrOf(tc.t, pyld, idKey); got != strconv.FormatInt(id, 10) {
		tc.t.Errorf("%s: invite id %q, want %d", tc.name, got, id)
	}
	if got := attrOf(tc.t, pyld, MsgAttrSenderName); got != from.name {
		tc.t.Errorf("%s: invite sender name %q", tc.name, got)
	}
	n := attrOf(tc.t, pyld, MsgAttrInviteID)
	if v, err := strconv.ParseInt(n, 10, 64); err != nil || v < JoinInviteFirst {
		tc.t.Errorf("%s: invite number %q", tc.name, n)
	}
	if got := attrOf(tc.t, pyld, MsgAttrInviteExtra); got != "0" {
		tc.t.Errorf("%s: invite extra %q", tc.name, got)
	}
	return n
}

func answer(typ int64, number, value string, to int64) []byte {
	return msg(typ, 0, ObjTypeUser, []MsgAttr{{MsgAttrInviteAnswer, value}, {MsgAttrInviteID, number}}, to)
}

// A party's code: issued to any member (one code), redeemed by a friend (the leader's invite, the answer dropped),
// refused while closed, full, for a member, for a user who blocked the leader, over the rate limit, after every
// holder left and once the party is gone; not logged in, an unknown code or kind, a non-member, a chat playgroup.
func TestJoinCodeParty(t *testing.T) {
	ts := newTCPServer(t, nil)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(ts.log.String())
		}
	})
	a, b, c := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com")

	anon := ts.dial("anon@example.com")
	wantRSN(t, "issue before login", anon.issueCode(JoinTargetParty, 1).Reason, JoinNotLoggedIn)
	wantRSN(t, "redeem before login", anon.redeemCode("x").Reason, JoinNotLoggedIn)

	f := a.pgCall(CmdPlaygroupsCreatePlaygroup, pvpCreate())
	info, _ := decode(t, f).Sub("INFO")
	pgid, _ := info.Int("PGID")
	a.note(ComponentPlaygroups, NotifyJoinPlaygroupID)

	in := a.issueCode(JoinTargetParty, pgid)
	if in.Code == "" || !in.Joinable || in.Kind != JoinKindParty || in.Size != 1 || in.Max != 5 || in.Reason != JoinOK {
		t.Fatalf("issue: %+v", in)
	}
	if again := a.issueCode(JoinTargetParty, pgid); again.Code != in.Code {
		t.Errorf("issued again: %q, want %q", again.Code, in.Code)
	}
	wantRSN(t, "a non-member issues", c.issueCode(JoinTargetParty, pgid).Reason, JoinNotMember)
	wantRSN(t, "an unknown party", a.issueCode(JoinTargetParty, 999).Reason, JoinGone)
	wantRSN(t, "an unknown kind", a.issueCode(9, pgid).Reason, JoinUnsupported)
	wantRSN(t, "an unknown code", b.redeemCode("nothing").Reason, JoinGone)

	// bravo redeems: alpha's invite on bravo's connection, alpha hears nothing
	out := b.redeemCode(in.Code)
	if out != (JoinCodeRedeemed{ID: pgid, Kind: JoinKindParty}) {
		t.Fatalf("redeem: %+v", out)
	}
	number := b.inviteFrom(a, MessageTypePvPInvite, MsgAttrPlaygroupID, pgid)
	a.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	// bravo's client accepts: joinPlaygroup, then the answer "T" to alpha, which the server drops
	wantOK(t, "join from the invite", b.pgCall(CmdPlaygroupsJoinPlaygroup, tdf.NewWriter().Int("PGID", pgid).Bytes()))
	b.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	a.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID)
	wantOK(t, "the answer", b.sendMsg(answer(MessageTypePvPInviteAnswer, number, "T", a.id)))
	a.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	// the same answer again, or one with a number the server never sent, is relayed as any message
	wantOK(t, "the answer again", b.sendMsg(answer(MessageTypePvPInviteAnswer, number, "T", a.id)))
	a.msgNote(b)

	// bravo, a member now, gets the same code; redeeming it is "already in"
	if bi := b.issueCode(JoinTargetParty, pgid); bi.Code != in.Code || bi.Size != 2 {
		t.Errorf("bravo's issue: %+v", bi)
	}
	wantRSN(t, "a member redeems", b.redeemCode(in.Code).Reason, JoinAlreadyIn)

	// closed while the leader queues: a code, not joinable; redeem refused
	closeTo := func(open int64) {
		t.Helper()
		wantOK(t, "join controls", a.pgCall(CmdPlaygroupsSetPlaygroupJoinControls, tdf.NewWriter().Int("OPEN", open).Int("PGID", pgid).Bytes()))
		a.note(ComponentPlaygroups, NotifyJoinControlsChangeID)
		b.note(ComponentPlaygroups, NotifyJoinControlsChangeID)
	}
	closeTo(PlaygroupClosed)
	if ci := a.issueCode(JoinTargetParty, pgid); ci.Code != in.Code || ci.Joinable || ci.Reason != JoinClosed {
		t.Errorf("closed: %+v", ci)
	}
	wantRSN(t, "closed", c.redeemCode(in.Code).Reason, JoinClosed)
	closeTo(PlaygroupOpen)

	// charlie blocked alpha: its client would drop the invite
	if _, err := ts.s.Store.Update(accountOf(t, ts.s, c.id), func(acct *store.Account) {
		acct.Lists.Blocked = append(acct.Lists.Blocked, store.ListMember{ID: a.id, Name: a.name})
	}); err != nil {
		t.Fatal(err)
	}
	wantRSN(t, "blocked the leader", c.redeemCode(in.Code).Reason, JoinBlocked)
	c.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)

	// alpha leaves: bravo leads, and its code (bravo issued it too) still works, now in bravo's name
	wantOK(t, "alpha leaves", a.pgCall(CmdPlaygroupsLeavePlaygroup, tdf.NewWriter().Int("PGID", pgid).Bytes()))
	d := ts.client("delta@example.com")
	wantRSN(t, "after the leader left", d.redeemCode(in.Code).Reason, JoinOK)
	d.inviteFrom(b, MessageTypePvPInvite, MsgAttrPlaygroupID, pgid)
	// bravo leaves too: the party is gone, and so is the code
	wantOK(t, "bravo leaves", b.pgCall(CmdPlaygroupsLeavePlaygroup, tdf.NewWriter().Int("PGID", pgid).Bytes()))
	deadline := time.Now().Add(5 * time.Second)
	for ts.s.groups.count() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	wantRSN(t, "the party is gone", d.redeemCode(in.Code).Reason, JoinGone)
	if _, _, ok := ts.s.joins.lookup(in.Code); ok {
		t.Error("the code outlived its party")
	}

	// a party of 2 whose only holder leaves: the code is gone though the party lives
	f = c.pgCall(CmdPlaygroupsCreatePlaygroup, tdf.NewWriter().Int("JOIN", 0).Struct("PGRP", func(w *tdf.Writer) {
		w.Int("MLIM", 2).String("NAME", "pair")
	}).Bytes())
	info, _ = decode(t, f).Sub("INFO")
	pair, _ := info.Int("PGID")
	c.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
	pc := c.issueCode(JoinTargetParty, pair)
	wantOK(t, "delta joins", d.pgCall(CmdPlaygroupsJoinPlaygroup, tdf.NewWriter().Int("PGID", pair).Bytes()))
	if full := c.issueCode(JoinTargetParty, pair); full.Code != pc.Code || full.Joinable || full.Reason != JoinFull || full.Size != 2 {
		t.Errorf("full: %+v", full)
	}
	e := ts.client("echo@example.com")
	wantRSN(t, "full", e.redeemCode(pc.Code).Reason, JoinFull)
	wantOK(t, "charlie leaves", c.pgCall(CmdPlaygroupsLeavePlaygroup, tdf.NewWriter().Int("PGID", pair).Bytes()))
	wantRSN(t, "every holder left", e.redeemCode(pc.Code).Reason, JoinGone)
	if nc := d.issueCode(JoinTargetParty, pair); nc.Code == "" || nc.Code == pc.Code || !nc.Joinable {
		t.Errorf("a new code after the holders left: %+v (old %q)", nc, pc.Code)
	}

	// a game's chat playgroup isn't a party
	f = e.pgCall(CmdPlaygroupsCreatePlaygroup, tdf.NewWriter().Int("JOIN", 1).Struct("PGRP", func(w *tdf.Writer) {
		w.Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV(PlaygroupKeyAttr, "GG-9")).Int("MLIM", 15).String("NAME", "GG-9").String("UKEY", "GG-9")
	}).Bytes())
	info, _ = decode(t, f).Sub("INFO")
	chat, _ := info.Int("PGID")
	if ci := e.issueCode(JoinTargetParty, chat); ci.Code != "" || ci.Reason != JoinUnsupported {
		t.Errorf("a chat playgroup: %+v", ci)
	}

	log := ts.log.String()
	for _, want := range []string{"issueJoinCode by 2000000001 for party 1: code true, 1 of 5, ok",
		"the server's PvP invite 0x176a0d2b to 2000000002 in the name of 2000000001 \"alpha\" (invite 1000000000, party 1)",
		"answer \"T\" to the server's invite 1000000000 in the name of 2000000001; dropped",
		"redeemJoinCode by 2000000003: party 1: blocked"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

// accountOf returns persona id's account (store) id.
func accountOf(t *testing.T, s *Server, id int64) int64 {
	t.Helper()
	acct, _, ok := s.Store.PersonaByID(id)
	if !ok {
		t.Fatalf("no persona %d", id)
	}
	return acct.UserID
}

// Redeeming is subject to the redeemer's Messaging rate limit.
func TestJoinCodeRateLimit(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) {
		s.msgs.limits = MessagingLimits{MaxTargets: 32, MaxAttrs: 16, MaxAttrBytes: 1024, InboxSize: 100, Burst: 1, Every: time.Hour}
	})
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	f := a.pgCall(CmdPlaygroupsCreatePlaygroup, pvpCreate())
	info, _ := decode(t, f).Sub("INFO")
	pgid, _ := info.Int("PGID")
	code := a.issueCode(JoinTargetParty, pgid).Code
	wantRSN(t, "first", b.redeemCode(code).Reason, JoinOK)
	wantRSN(t, "second", b.redeemCode(code).Reason, JoinRateLimited)
}

// privateCreateRequest is the custom create request with the attribute PrivateMatch "1" (a password).
func privateCreateRequest(t *testing.T, g *golden.Data) []byte {
	t.Helper()
	st, err := tdf.Decode(createRequest(t, g, "2", ""))
	if err != nil {
		t.Fatal(err)
	}
	w := tdf.NewWriter()
	for _, f := range st.Fields {
		if m, ok := f.Value.(*tdf.Map); ok && f.Label == "ATTR" {
			m.Entries = append(m.Entries, tdf.MapEntry{Key: AttrPrivateMatch, Value: "1"})
		}
		w.Value(f.Label, f.Type, f.Value)
	}
	return w.Bytes()
}

// A custom game's and a practice group's codes (no match host): the owner's invite of the right kind, the join it
// leads to, the answer dropped; a full game; a started game (no code, the old code gone); a private game; a practice
// or matchmade game without a lobby.
func TestJoinCodeGames(t *testing.T) {
	ts := newTCPServer(t, nil)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(ts.log.String())
		}
	})
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")

	// a custom game, 1 of 10
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, createRequest(t, g, "2", "")))
	a.note(ComponentGameManager, NotifyGameSetupID)
	in := a.issueCode(JoinTargetGame, 1)
	if in.Code == "" || !in.Joinable || in.Kind != JoinKindCustomGame || in.Size != 1 || in.Max != 10 {
		t.Fatalf("custom: %+v", in)
	}
	wantRSN(t, "a non-player", b.issueCode(JoinTargetGame, 1).Reason, JoinNotMember)
	if out := b.redeemCode(in.Code); out != (JoinCodeRedeemed{ID: 1, Kind: JoinKindCustomGame}) {
		t.Fatalf("redeem: %+v", out)
	}
	number := b.inviteFrom(a, MessageTypeCustomMatchInvite, MsgAttrGameID, 1)
	// the client's Game_JoinPublic with team 0xFFFF, then its answer
	wantReply(t, "join from the invite", b.gm(CmdGameManagerJoinGame, tdf.NewWriter().Int("GID", 1).Int("TIDX", 0xFFFF).Bytes()))
	b.note(ComponentGameManager, NotifyGameSetupID)
	wantOK(t, "the answer", b.sendMsg(answer(MessageTypeCustomInviteAnswer, number, "T", a.id)))
	a.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	if bi := b.issueCode(JoinTargetGame, 1); bi.Code != in.Code || bi.Size != 2 {
		t.Errorf("a player's issue: %+v", bi)
	}
	// eight more fill both teams; a spectator doesn't count
	for i := 0; i < 8; i++ {
		c := ts.client(string(rune('c'+i)) + "@example.com")
		wantReply(t, "join", c.gm(CmdGameManagerJoinGame, tdf.NewWriter().Int("GID", 1).Int("TIDX", 0xFFFF).Bytes()))
	}
	spec := ts.client("spectator@example.com")
	wantReply(t, "spectate", spec.gm(CmdGameManagerJoinGame, tdf.NewWriter().Int("GID", 1).Int("TIDX", SpectatorTeam).Bytes()))
	if full := a.issueCode(JoinTargetGame, 1); full.Code != in.Code || full.Joinable || full.Reason != JoinFull || full.Size != 10 {
		t.Errorf("full: %+v", full)
	}
	late := ts.client("late@example.com")
	wantRSN(t, "full", late.redeemCode(in.Code).Reason, JoinFull)
	// started: no code; the old one is gone
	wantReply(t, "start", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
	if st := a.issueCode(JoinTargetGame, 1); st.Code != "" || st.Joinable || st.Reason != JoinStarted {
		t.Errorf("started: %+v", st)
	}
	wantRSN(t, "started", late.redeemCode(in.Code).Reason, JoinGone)

	// a private custom game: never a code
	p := ts.client("private@example.com")
	wantReply(t, "create private", p.gm(CmdGameManagerResetDedicatedServer, privateCreateRequest(t, g)))
	p.note(ComponentGameManager, NotifyGameSetupID)
	if pi := p.issueCode(JoinTargetGame, 2); pi.Code != "" || pi.Reason != JoinPrivate || pi.Kind != JoinKindCustomGame {
		t.Errorf("private: %+v", pi)
	}

	// a practice group: the practice invite, 1 of 5
	q := ts.client("quebec@example.com")
	wantReply(t, "create practice group", q.gm(CmdGameManagerResetDedicatedServer, practiceGroupRequest(t, g)))
	q.note(ComponentGameManager, NotifyGameSetupID)
	qi := q.issueCode(JoinTargetGame, 3)
	if qi.Code == "" || !qi.Joinable || qi.Kind != JoinKindPracticeGroup || qi.Size != 1 || qi.Max != 5 {
		t.Fatalf("practice group: %+v", qi)
	}
	r := ts.client("romeo@example.com")
	if out := r.redeemCode(qi.Code); out != (JoinCodeRedeemed{ID: 3, Kind: JoinKindPracticeGroup}) {
		t.Fatalf("redeem practice: %+v", out)
	}
	number = r.inviteFrom(q, MessageTypePracticeMatchInvite, MsgAttrGameID, 3)
	wantOK(t, "the answer", r.sendMsg(answer(MessageTypePracticeInviteAnswer, number, "T", q.id)))
	q.none(ComponentMessaging, NotifyMessageID, 50*time.Millisecond)
	// an answer of the wrong kind to that number isn't the server's: relayed
	wantOK(t, "a stray answer", r.sendMsg(answer(MessageTypePvPInviteAnswer, number, "T", q.id)))
	q.msgNote(r)

	// the owner leaves the lobby before the start: the game ends, the code goes
	wantReply(t, "the owner leaves", q.gm(CmdGameManagerRemovePlayer, tdf.NewWriter().Int("GID", 3).Int("PID", q.id).Int("REAS", 6).Bytes()))
	deadline := time.Now().Add(5 * time.Second)
	for ts.s.hosted.get(3) != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	wantRSN(t, "the lobby ended", r.redeemCode(qi.Code).Reason, JoinGone)
}
