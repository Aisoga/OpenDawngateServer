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

// UserIdentificationRequest is a lookupUser request (Blaze::UserIdentification 0x012F1C70) with every member
// written, as the client's encoder does: by id, or by name with id 0.
func UserIdentificationRequest(id int64, name string) []byte {
	return tdf.NewWriter().Int("AID", 0).Int("ALOC", 0).Blob("EXBB", nil).Int("EXID", 0).Int("ID", id).
		String("NAME", name).Int("ORIG", 0).Bytes()
}

// A5b: UserSessions::lookupUser's reply (Blaze::UserData) against tdf_golden.py's independent writer and the
// client's member table, online and offline.
func TestLookupUserRepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	acct := store.Account{UserID: store.FirstUserID + 1}
	p := store.Persona{ID: store.FirstPersonaID + 1, Name: "second"}
	for _, c := range []struct {
		name   string
		online bool
	}{{"lookupuser_reply", true}, {"lookupuser_offline_reply", false}} {
		got := UserDataReply(acct, p, c.online)
		tdfcheck.Require(t, "Blaze::UserData", got)
		if h, want := hex.EncodeToString(got), g.WriterCase(t, c.name).Hex; h != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, h, want)
		}
	}
}

// The PvP lobby's manual invite as the client sends it (MatchInviteUI 0x004E8A20 -> 0x00C8CDB0, static reading):
// "player" looks "second" up by name and gets its user (online); an unknown name gets USER_ERR_USER_NOT_FOUND;
// the lookup by id finds the same user.
func TestLookupUserByNameAndID(t *testing.T) {
	ps := newPipeSession(t)
	ps.loginAs("player@example.com", 0)
	two := ps.another()
	two.loginAs("second@example.com", 0)
	tdfcheck.Require(t, "Blaze::UserIdentification", ps.request("lookupuser_request"))

	ps.send(ComponentUserSessions, CmdUserSessionsLookupUser, 2, ps.request("lookupuser_request"))
	ps.expect(ComponentUserSessions, CmdUserSessionsLookupUser, fire.Reply, 2, "lookupuser_reply")

	unknown := strings.Replace(hex.EncodeToString(ps.request("lookupuser_request")), hex.EncodeToString([]byte("second")),
		hex.EncodeToString([]byte("nobody")), 1)
	raw, _ := hex.DecodeString(unknown)
	ps.send(ComponentUserSessions, CmdUserSessionsLookupUser, 3, raw)
	ps.expectError(ComponentUserSessions, CmdUserSessionsLookupUser, 3, ErrUserNotFound)

	byID := UserIdentificationRequest(store.FirstPersonaID+1, "")
	tdfcheck.Require(t, "Blaze::UserIdentification", byID)
	ps.send(ComponentUserSessions, CmdUserSessionsLookupUser, 4, byID)
	ps.expect(ComponentUserSessions, CmdUserSessionsLookupUser, fire.Reply, 4, "lookupuser_reply")

	two.client.Close()
	<-two.done
	log := ps.close()
	for _, want := range []string{
		"lookupUser id 0 name \"second\": persona 2000000002 \"second\" (online)",
		"lookupUser id 0 name \"nobody\": not found",
		"lookupUser id 2000000002 name \"\": persona 2000000002 \"second\" (online)",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
}
