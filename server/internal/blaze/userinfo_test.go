package blaze

import (
	"path/filepath"
	"testing"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// uattOf returns DATA.UATT of a UserSessionExtendedDataUpdate frame (checked against the member table).
func uattOf(t *testing.T, f fire.Frame) uint64 {
	t.Helper()
	body := payloadOf(t, "Blaze::UserSessionExtendedDataUpdate", f)
	data, ok := body.Sub("DATA")
	if !ok {
		t.Fatal("no DATA")
	}
	v, _ := data.Int("UATT")
	return uint64(v)
}

// B6: dismissing the news overlay (setUserInfoAttribute ATTV 1<<62) is stored, comes back to the
// client at once as UserSessionExtendedDataUpdate, survives a restart of the store, and comes back at
// the next login, so the client's check (bit 62 of UATT, 0x00AD9200) keeps the overlay away.
func TestNewsOverlaySeen(t *testing.T) {
	path := filepath.Join(t.TempDir(), store.FileName)
	setup := func(s *Server) {
		st, err := openTestStore(t, path)
		if err != nil {
			t.Fatal(err)
		}
		s.Store = st
	}
	seen := tdf.NewWriter().Uint("ATTV", UserInfoNewsSeen).Uint("MASK", 0).Bytes()

	ps := newPipeSessionWith(t, setup)
	// before login nothing is stored and nothing is pushed: the next frame is the ping's reply
	ps.send(ComponentUserSessions, CmdUserSessionsSetUserInfoAttribute, 1, seen)
	ps.expect(ComponentUserSessions, CmdUserSessionsSetUserInfoAttribute, fire.Reply, 1, "")
	ps.send(9, 2, 2, nil)
	if f := ps.next("ping reply"); f.Component != 9 || f.Type != fire.Reply {
		t.Errorf("after setUserInfoAttribute without login: %#x/%#x %s", f.Component, f.Command, f.Type)
	}
	ps.loginAs("player@example.com", 10)
	ps.send(ComponentUserSessions, CmdUserSessionsSetUserInfoAttribute, 12, seen)
	ps.expect(ComponentUserSessions, CmdUserSessionsSetUserInfoAttribute, fire.Reply, 12, "")
	f := ps.next("UserSessionExtendedDataUpdate")
	if f.Component != ComponentUserSessions || f.Command != NotifyUserSessionExtendedDataUpdate || f.Type != fire.Notification {
		t.Fatalf("got %#x/%#x %s", f.Component, f.Command, f.Type)
	}
	if got := uattOf(t, f); got != UserInfoNewsSeen {
		t.Errorf("pushed UATT %#x", got)
	}
	ps.close()

	if a, ok := mustOpen(t, path).Account(store.FirstUserID); !ok || a.UserInfo != UserInfoNewsSeen {
		t.Fatalf("stored account %+v", a)
	}
	// a restarted server (same file): the login's extended data carries the bit
	ps = newPipeSessionWith(t, setup)
	ps.send(1, 0x28, 1, tdf.NewWriter().String("MAIL", "player@example.com").String("PASS", testPassword).String("TOKN", "").Int("TYPE", 0).Bytes())
	ps.next("login reply")
	ps.send(1, 0x6E, 2, tdf.NewWriter().String("PNAM", store.PersonaName("player@example.com")).Bytes())
	ps.next("loginPersona reply")
	ps.next("UserAuthenticated")
	if got := uattOf(t, ps.next("UserSessionExtendedDataUpdate")); got&UserInfoNewsSeen == 0 {
		t.Errorf("login UATT %#x lacks bit 62", got)
	}
	ps.close()
}

func mustOpen(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := openTestStore(t, path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
