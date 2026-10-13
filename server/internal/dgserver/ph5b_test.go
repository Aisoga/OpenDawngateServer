package dgserver

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/tdf"
)

// --account-status run as another process against the running server's database ends the logged-in account's
// session within --status-check (UserSessionDisconnected, then the connection's end); the flags are checked.
func TestStatusCheckEndsSession(t *testing.T) {
	for _, bad := range [][]string{{"--status-check", "-1s"}, {"--session-end", "kick"}} {
		if code := Main(append(bad, "--print-ports")); code != 2 {
			t.Errorf("%v: exit %d", bad, code)
		}
	}
	dir, logDir := t.TempDir(), t.TempDir()
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: logDir, dataDir: dir,
		statusCheck: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	bc, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", in.ports[1]))
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Close()
	r := fire.NewReader(bc)
	send := func(cmd uint16, id uint32, payload []byte) {
		b, _ := fire.Encode(fire.Frame{Component: 1, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
		if _, err := bc.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	send(0x28, 1, tdf.NewWriter().String("MAIL", "player@example.com").String("PASS", testPassword).String("TOKN", "").Int("TYPE", 0).Bytes())
	send(0x6E, 2, tdf.NewWriter().String("PNAM", "player").Bytes())
	next := func() fire.Frame {
		t.Helper()
		bc.SetReadDeadline(time.Now().Add(5 * time.Second))
		f, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	for f := next(); f.Command != 0x6E || f.Type != fire.Reply; f = next() {
	}
	// the operator's command, as another process would run it (its own database handle)
	var out, errOut bytes.Buffer
	if code := (accountCommands{accountStatus: "player@example.com=banned"}).run(dir, &out, &errOut, time.Now()); code != 0 {
		t.Fatal(errOut.String())
	}
	for {
		f := next()
		if f.Component == blaze.ComponentUserSessions && f.Command == blaze.NotifyUserSessionDisconnected && f.Type == fire.Notification {
			break
		}
	}
	bc.Close()
	time.Sleep(100 * time.Millisecond)
	in.stop()
	log := readLog(t, logDir)
	for _, want := range []string{"within 50ms after --account-status (status check)",
		"ending its session now (its account is banned (status check)); notice disconnected"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}
