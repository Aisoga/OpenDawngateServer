package blaze

import (
	"strings"
	"testing"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/tdf"
)

// test aid (dgserver --test-login-errors): the n-th login gets the n-th error reply (no payload), 0 or past
// the list's end is answered as usual, and a refused login leaves no session (loginPersona stays unanswered).
func TestTestLoginErrors(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) { s.TestLoginErrors = []uint16{0x0C, 0, 0x4072} })
	login := func(tc *tcpClient, name string) fire.Frame {
		t.Helper()
		id := tc.send(1, 0x28, tdf.NewWriter().String("MAIL", name).String("PASS", testPassword).String("TOKN", "").Int("TYPE", 0).Bytes())
		return tc.reply(1, 0x28, id)
	}
	a := ts.dial("ph2aname")
	if f := login(a, "ph2aname"); f.Type != fire.ErrorReply || f.Error != 0x0C || len(f.Payload) != 0 {
		t.Fatalf("login 1: %s error %#x, %d bytes", f.Type, f.Error, len(f.Payload))
	}
	if f := login(a, "ph2aname"); f.Type != fire.Reply || f.Error != 0 {
		t.Fatalf("login 2: %s error %#x", f.Type, f.Error)
	}
	b := ts.dial("other")
	if f := login(b, "other"); f.Type != fire.ErrorReply || f.Error != 0x4072 {
		t.Fatalf("login 3: %s error %#x", f.Type, f.Error)
	}
	if f := login(b, "other"); f.Type != fire.Reply {
		t.Fatalf("login 4 (past the list): %s error %#x", f.Type, f.Error)
	}
	log := ts.log.String()
	for _, want := range []string{
		"(test --test-login-errors: login 1 refused with error 0x000c)",
		"(test --test-login-errors: login 3 refused with error 0x4072)",
		"-> login error reply (error 0x000c), 0 bytes",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}
