package site

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"dawngate/server/internal/accounts"
)

// endCalls records Config.EndSessions's calls and answers with sessions.
type endCalls struct {
	mu       sync.Mutex
	calls    []string // "<user id> <why>"
	sessions int
}

func (e *endCalls) end(id int64, why string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, fmt.Sprintf("%d %s", id, why))
	return e.sessions
}

func (e *endCalls) take() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.calls
	e.calls = nil
	return c
}

// the admin page's disable, ban and delete end the account's game sessions at once (enable doesn't), and say
// so on the page.
func TestAdminEndsSessions(t *testing.T) {
	ec := &endCalls{sessions: 1}
	af := newAdminFixture(t, func(c *Config) { c.EndSessions = ec.end })
	af.login(af.c)
	id := fmt.Sprint(af.player.UserID)
	for _, c := range []struct {
		status, call, page string
	}{
		{"disabled", id + " admin boss (account 1000000001) set it disabled on the admin page", "Its game session ends now"},
		{"active", "", "is active now"},
		{"banned", id + " admin boss (account 1000000001) set it banned on the admin page", "Its game session ends now"},
	} {
		code, body, _ := af.adminPost(af.c, "/admin/accounts/status", map[string]string{"id": id, "status": c.status})
		if code != 200 || !strings.Contains(body, c.page) {
			t.Errorf("%s: %d\n%s", c.status, code, body)
		}
		got := ec.take()
		if (c.call == "" && len(got) != 0) || (c.call != "" && (len(got) != 1 || got[0] != c.call)) {
			t.Errorf("%s: EndSessions calls %q, want %q", c.status, got, c.call)
		}
	}
	ec.sessions = 0
	code, body, _ := af.adminPost(af.c, "/admin/accounts/status", map[string]string{"id": id, "status": "disabled"})
	if code != 200 || !strings.Contains(body, "It wasn&#39;t logged into the game.") {
		t.Errorf("not logged in: %d\n%s", code, body)
	}
	ec.take()
	ec.sessions = 2
	code, body, _ = af.adminPost(af.c, "/admin/accounts/delete", map[string]string{"id": id, "confirm": "yes"})
	if code != 200 || !strings.Contains(body, "Its 2 game sessions end now") {
		t.Errorf("delete: %d\n%s", code, body)
	}
	if got := ec.take(); len(got) != 1 || got[0] != id+" admin boss (account 1000000001) deleted it on the admin page" {
		t.Errorf("delete: EndSessions calls %q", got)
	}
}

// a player's own deletion on the website ends its game sessions too; a refused one doesn't.
func TestSelfDeleteEndsSessions(t *testing.T) {
	ec := &endCalls{sessions: 1}
	f := newFixture(t, accounts.Open, func(c *Config) { c.EndSessions = ec.end })
	me, pw, err := accounts.CreateAccount(f.st, "selfie", t0)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.submit(f.c, "/account/delete", map[string]string{"name": "selfie", "password": "wrong-one", "confirm": "yes"}); code != 401 {
		t.Errorf("wrong password: %d", code)
	}
	if got := ec.take(); len(got) != 0 {
		t.Errorf("a refused delete ended sessions: %q", got)
	}
	code, body, _ := f.submit(f.c, "/account/delete", map[string]string{"name": "selfie", "password": pw, "confirm": "yes"})
	if code != 200 || !strings.Contains(body, "goes back to the login screen now") {
		t.Fatalf("delete: %d\n%s", code, body)
	}
	if got := ec.take(); len(got) != 1 || got[0] != fmt.Sprintf("%d its player deleted it on the website", me.UserID) {
		t.Errorf("EndSessions calls %q", got)
	}
	if l := f.logText(); !strings.Contains(l, "its 1 game session(s) end now") {
		t.Errorf("log:\n%s", l)
	}
}
