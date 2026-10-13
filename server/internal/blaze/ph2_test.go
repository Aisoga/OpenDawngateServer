package blaze

import (
	"strings"
	"testing"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// Authentication::login checks the password. Under open sign-up a new name is created with its password; the
// next login needs that password (a wrong or empty one gets AUTH_ERR_INVALID_PASSWORD's error reply on the same
// connection, which stays usable); a disabled account is refused after its password; the log never holds a password.
func TestLoginPasswordCheck(t *testing.T) {
	ts := newTCPServer(t, nil)
	try := func(tc *tcpClient, name, pw string) fire.Frame {
		t.Helper()
		id := tc.send(1, 0x28, tdf.NewWriter().String("MAIL", name).String("PASS", pw).String("TOKN", "").Int("TYPE", 0).Bytes())
		return tc.reply(1, 0x28, id)
	}
	refused := func(f fire.Frame, code uint16) {
		t.Helper()
		if f.Type != fire.ErrorReply || f.Error != code || len(f.Payload) != 0 {
			t.Errorf("got %s error %#x (%d bytes), want the error reply %#x", f.Type, f.Error, len(f.Payload), code)
		}
	}
	a := ts.dial("ph2user")
	if f := try(a, "ph2user", "Correct-Horse-1"); f.Type != fire.Reply {
		t.Fatalf("first login: %s %#x", f.Type, f.Error)
	}
	b := ts.dial("ph2user")
	refused(try(b, "PH2USER", "wrong-password"), accounts.CodeInvalidPassword)
	refused(try(b, "ph2user", ""), accounts.CodeInvalidPassword) // the client's retry without retyping
	refused(try(b, "ph2 user", "Correct-Horse-1"), accounts.CodeInvalidName)
	if f := try(b, "ph2user", "Correct-Horse-1"); f.Type != fire.Reply {
		t.Fatalf("right password after refusals: %s %#x", f.Type, f.Error)
	}
	if _, err := accounts.SetStatus(ts.s.Store, "ph2user", store.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	c := ts.dial("ph2user")
	refused(try(c, "ph2user", "wrong-password"), accounts.CodeInvalidPassword)
	refused(try(c, "ph2user", "Correct-Horse-1"), accounts.CodeUserInactive)

	log := ts.log.String()
	if strings.Contains(log, "Correct-Horse-1") || strings.Contains(log, "wrong-password") {
		t.Errorf("the log holds a password:\n%s", log)
	}
	for _, want := range []string{"PASS: '***'", "PASS: ''", "login refused (error 0x000c): account 1000000001: wrong password",
		`account 1000000001 created for "ph2user" (open sign-up)`, "login refused (error 0x0065): account 1000000001 is disabled"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

// Under invite and closed sign-up the game's login makes no account: an unknown name gets AUTH_ERR_INVALID_USER (the
// client's text is the same as a wrong password's); an account made by the operator logs in.
func TestLoginInviteMode(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) { s.Accounts = accounts.Policy{Mode: accounts.Invite} })
	a := ts.dial("newname")
	id := a.send(1, 0x28, tdf.NewWriter().String("MAIL", "newname").String("PASS", testPassword).String("TOKN", "").Int("TYPE", 0).Bytes())
	if f := a.reply(1, 0x28, id); f.Type != fire.ErrorReply || f.Error != accounts.CodeInvalidUser {
		t.Fatalf("new name under invite: %s %#x", f.Type, f.Error)
	}
	if n, _ := ts.s.Store.Count(); n != 0 {
		t.Errorf("%d accounts", n)
	}
	_, pw, err := accounts.CreateAccount(ts.s.Store, "member", ts.s.Now())
	if err != nil {
		t.Fatal(err)
	}
	b := ts.dial("member")
	id = b.send(1, 0x28, tdf.NewWriter().String("MAIL", "member").String("PASS", pw).String("TOKN", "").Int("TYPE", 0).Bytes())
	if f := b.reply(1, 0x28, id); f.Type != fire.Reply {
		t.Fatalf("operator's account: %s %#x", f.Type, f.Error)
	}
	if strings.Contains(ts.log.String(), pw) {
		t.Error("the log holds the operator's password")
	}
}
