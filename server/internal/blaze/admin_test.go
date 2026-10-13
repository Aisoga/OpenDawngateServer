package blaze

import (
	"errors"
	"strings"
	"testing"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/store"
)

// the admin page's list of running games and the end of one: the host gets abortGame with the admin's reason,
// exits, and the player is removed with GAME_DESTROYED (the client's way back to the main menu).
func TestAdminEndsHostedMatch(t *testing.T) {
	hs := newHostedSession(t, nil, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		h.Send(gmshost.AddPlayer{User: store.FirstPersonaID})
		h.Send(gmshost.GMSUpdate{Phase: gmshost.PhaseInGame})
		return receive(hs, h)
	})
	if got := hs.s.HostedMatches(); len(got) != 0 {
		t.Fatalf("matches before a game: %+v", got)
	}
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	hs.hostGot()
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
	hs.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(1, GameStateInGame))
	got := hs.s.HostedMatches()
	if len(got) != 1 {
		t.Fatalf("matches: %+v", got)
	}
	m := got[0]
	if m.GameID != 1 || m.Mode != gmshost.ModePractice || m.State != "in game" || m.Host != 1 || m.Pid == 0 ||
		len(m.Players) != 1 || m.Players[0] != (HostedMatchPlayer{PersonaID: store.FirstPersonaID, UserID: store.FirstUserID,
		Name: "player", Team: 0, Joined: true}) || m.Started.IsZero() {
		t.Errorf("match %+v", m)
	}
	if err := hs.s.EndHostedMatch(99, "x"); !errors.Is(err, ErrNoHostedGame) {
		t.Errorf("an unknown game: %v", err)
	}
	if err := hs.s.EndHostedMatch(1, "ended by admin boss on the admin page"); err != nil {
		t.Fatal(err)
	}
	if msg := hs.hostGot(); msg != (gmshost.AbortGame{Reason: "ended by admin boss on the admin page"}) {
		t.Errorf("host got %#v", msg)
	}
	hs.expectNote(NotifyPlayerRemovedID, PlayerRemovedNotification(1, store.FirstPersonaID, RemovedGameDestroyed))
	hs.waitIdle()
	if got := hs.s.HostedMatches(); len(got) != 0 {
		t.Errorf("matches after the end: %+v", got)
	}
	if err := hs.s.EndHostedMatch(1, "again"); !errors.Is(err, ErrNoHostedGame) {
		t.Errorf("an ended game: %v", err)
	}
	hs.close()
	log := hs.log.String()
	for _, want := range []string{"[blaze]    game 1: ending it (ended by admin boss on the admin page): abortGame to match host 1",
		"[blaze]    game 1: match host aborted (abortGame: ended by admin boss on the admin page)"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	acct, _ := hs.s.Store.Account(store.FirstUserID)
	if acct.Flags&store.FlagFTUEDone != 0 {
		t.Error("an ended game counted as finished")
	}
}
