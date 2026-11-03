package blaze

import (
	"bytes"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/tdf"
)

// takeAll removes every queued notification comp/id of tc and returns them (waiting within first).
func (tc *tcpClient) takeAll(comp, id uint16, within time.Duration) []fire.Frame {
	time.Sleep(within)
	tc.mu.Lock()
	defer tc.mu.Unlock()
	var got, keep []fire.Frame
	for _, f := range tc.frames {
		if f.Component == comp && f.Command == id && f.Type == fire.Notification {
			got = append(got, f)
		} else {
			keep = append(keep, f)
		}
	}
	tc.frames = keep
	return got
}

// RATING2b: an admin's setGameAttributes with TeamRostersKey (BALANCE, Join Team, a join) is followed by the admin's
// own UserSessionExtendedDataUpdate, the login's bytes, so its lobby redraws (the UserManager listener's slot 1);
// the other players get none, and attributes without TeamRostersKey send none.
func TestCustomLobbyRosterRedraw(t *testing.T) {
	ts, host := newCustomHostServer(t, nil)
	g := golden.Load(t)
	a := ts.dial("alpha@example.com") // tc.login, keeping the login's own extended data
	a.reply(1, 0x28, a.send(1, 0x28, tdf.NewWriter().String("MAIL", "alpha@example.com").String("PASS", testPassword).
		String("TOKN", "").Int("TYPE", 0).Bytes()))
	st := decode(t, a.reply(1, 0x6E, a.send(1, 0x6E, tdf.NewWriter().String("PNAM", a.name).Bytes())))
	a.id, _ = st.Int("BUID")
	login := a.note(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate)
	b := ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID) // the game is ready: the join may come
	wantReply(t, "join", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	b.note(ComponentGameManager, NotifyGameSetupID)
	a.takeAll(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 100*time.Millisecond)
	b.takeAll(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 0)

	wantReply(t, "attributes", a.gm(CmdGameManagerSetGameAttributes, golden.Bytes(t, decodeCase(t, g, "setgameattributes_request"))))
	a.note(ComponentGameManager, NotifyGameAttribChangeID)
	own := a.note(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate)
	if usid, _ := decode(t, own).Int("USID"); usid != a.id {
		t.Errorf("the update names %d, not the admin %d", usid, a.id)
	}
	if !bytes.Equal(own.Payload, login.Payload) {
		t.Errorf("the update differs from the login's:\n%x\n%x", own.Payload, login.Payload)
	}
	b.note(ComponentGameManager, NotifyGameAttribChangeID)
	b.none(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 50*time.Millisecond)

	// only the lock: no update
	lock := tdf.NewWriter().Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV("RosterLockedKey", "1")).Int("GID", 1)
	wantReply(t, "lock", a.gm(CmdGameManagerSetGameAttributes, lock.Bytes()))
	a.note(ComponentGameManager, NotifyGameAttribChangeID)
	a.none(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 50*time.Millisecond)
}
