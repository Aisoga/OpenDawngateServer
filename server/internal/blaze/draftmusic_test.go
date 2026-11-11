package blaze

import (
	"encoding/json"
	"strings"
	"testing"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/tdf"
)

// The draft's music switch: draft.music stores the player's choice in its account and echoes it in that player's own
// draft.state only (also a spectator's), every later board carries it, a call with bad arguments gets no answer, a
// player in no lobby may set it too, and EA's settings calls never read or write the key.
func TestDraftMusic(t *testing.T) {
	ts, _, a, _, ua, ub, start, addr := draftLobby(t, nil)
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	if sa, sb := ua.draftState(), ub.draftState(); sa.MusicOff || sb.MusicOff || sa.Phase != "role" {
		t.Fatalf("the begin: A %+v, B %+v", sa, sb)
	}
	setting := func(id int64) string {
		t.Helper()
		acct, _ := ts.s.Store.Account(accountOf(t, ts.s, id))
		return acct.Settings[SettingDraftMusic]
	}

	ua.call(CallDraftMusic, `{"on":false}`)
	if s := ua.draftState(); !s.MusicOff || s.Phase != "role" {
		t.Fatalf("A's echo %+v", s)
	}
	ub.quietDraft()
	if got := setting(a.id); got != "off" {
		t.Errorf("A's setting %q", got)
	}
	endStep(t, ts) // the role phase's end: a public change
	if sa, sb := ua.draftState(), ub.draftState(); !sa.MusicOff || sb.MusicOff {
		t.Errorf("the next board: A musicOff %v, B musicOff %v", sa.MusicOff, sb.MusicOff)
	}
	for _, bad := range []string{`{}`, `{"on":"no"}`, `{"on":true,"x":1}`} { // the rest: TestParseDraftMusic (10 calls a second)
		ua.call(CallDraftMusic, bad)
	}
	ua.quietDraft()
	if got := setting(a.id); got != "off" {
		t.Errorf("A's setting after bad calls %q", got)
	}
	ua.call(CallDraftMusic, `{"on":true}`)
	if s := ua.draftState(); s.MusicOff {
		t.Errorf("A's echo after on %+v", s)
	}
	if got := setting(a.id); got != "on" {
		t.Errorf("A's setting after on %q", got)
	}

	// a spectator: its own sound too
	g := golden.Load(t)
	c := ts.client("charlie@example.com")
	wantReply(t, "C spectates", c.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
	c.settle()
	uc := uiConnect(t, addr, c)
	uc.call(CallLobbyEnter, "")
	uc.draftState()
	uc.call(CallDraftMusic, `{"on":false}`)
	if s := uc.draftState(); !s.MusicOff || s.You != nil {
		t.Errorf("the spectator's echo %+v", s)
	}
	ua.quietDraft()
	ub.quietDraft()

	// a player in no lobby: stored, no board
	d := ts.client("delta@example.com")
	ud := uiConnect(t, addr, d)
	ud.call(CallDraftMusic, `{"on":false}`)
	ud.quietDraft()
	if got := setting(d.id); got != "off" {
		t.Errorf("D's setting %q", got)
	}

	// EA's settings calls: saveUserSettings doesn't write the key, getUserSettings doesn't show it
	payload := tdf.NewWriter().Map("USET", tdf.TypeString, tdf.TypeString, tdf.KV(SettingDraftMusic, "on"),
		tdf.KV("Audio.Master", "0.5")).Bytes()
	d.reply(ComponentBattlegroundsAccounts, CmdBGASaveUserSettings, d.send(ComponentBattlegroundsAccounts, CmdBGASaveUserSettings, payload))
	acct, _ := ts.s.Store.Account(accountOf(t, ts.s, d.id))
	if acct.Settings[SettingDraftMusic] != "off" || acct.Settings["Audio.Master"] != "0.5" {
		t.Errorf("after saveUserSettings: %v", acct.Settings)
	}
	if got := ts.s.userSettings(acct.Settings); got[SettingDraftMusic] != "" || got["Audio.Master"] != "0.5" {
		t.Errorf("getUserSettings shows %v", got)
	}
	log := ts.log.String()
	for _, want := range []string{"draft: player " + itoa(a.id) + " turns the draft's music off",
		"draft: player " + itoa(a.id) + " turns the draft's music on", "draft: player " + itoa(d.id) + " turns the draft's music off"} {
		if !strings.Contains(log, want) {
			t.Errorf("no log line %q", want)
		}
	}
}

func TestParseDraftMusic(t *testing.T) {
	for in, want := range map[string]bool{`{"on":true}`: true, `{"on":false}`: false} {
		if got, err := parseDraftMusic(json.RawMessage(in)); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{`{}`, `{"on":null}`, `{"on":"true"}`, `{"on":0}`, `{"on":true,"on2":true}`, `{"on":true}{}`, `[]`, ``,
		`{"on":` + strings.Repeat(" ", 300) + `true}`} {
		if _, err := parseDraftMusic(json.RawMessage(bad)); err == nil {
			t.Errorf("%s taken", bad)
		}
	}
}
