package dgserver

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/store"
)

// --make-admin and --remove-admin, and the audit log of every account command (never a password or a code).
func TestAdminCommandsAndAudit(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	run := func(c accountCommands) (string, int) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := c.run(dir, &out, &errOut, now)
		return out.String() + errOut.String(), code
	}
	out, _ := run(accountCommands{createAccount: "boss"})
	pw := regexp.MustCompile(`\n    (\S{16})\n`).FindStringSubmatch(out)[1]
	out, code := run(accountCommands{makeAdmin: "BOSS"})
	if code != 0 || !strings.Contains(out, `account 1000000001 "boss": admin (was player)`) || !strings.Contains(out, "/admin") {
		t.Fatalf("make admin: %d %s", code, out)
	}
	if out, code := run(accountCommands{makeAdmin: "nobody"}); code != 1 || !strings.Contains(out, `no account "nobody"`) {
		t.Errorf("an unknown name: %d %s", code, out)
	}
	run(accountCommands{accountStatus: "boss=disabled"})
	run(accountCommands{accountStatus: "boss=active"})
	out, _ = run(accountCommands{makeInvite: true, inviteUses: 1, inviteDays: 0, inviteNote: "ann"})
	invite := regexp.MustCompile(`[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}`).FindString(out)
	run(accountCommands{revokeInvite: 1})
	out, _ = run(accountCommands{resetPassword: "boss"})
	pw2 := regexp.MustCompile(`\n    (\S{16})\n`).FindStringSubmatch(out)[1]
	run(accountCommands{listInvites: true}) // changes nothing: no entry
	if out, code := run(accountCommands{removeAdmin: "boss"}); code != 0 || !strings.Contains(out, "player (was admin)") {
		t.Errorf("remove admin: %d %s", code, out)
	}

	st, err := store.Open(filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := st.AuditLog(20, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := len(log) - 1; i >= 0; i-- {
		e := log[i]
		if e.Actor != "operator" || e.Source != "command line" || !e.Time.Equal(now) {
			t.Errorf("entry %+v", e)
		}
		lines = append(lines, fmt.Sprintf("%s|%s|%s", e.Action, e.Target, e.Detail))
		for _, secret := range []string{pw, pw2, invite} {
			if strings.Contains(e.Target+e.Detail, secret) {
				t.Errorf("a secret in the audit log: %+v", e)
			}
		}
	}
	want := []string{
		`create account|account 1000000001 "boss"|a random password`,
		`account role|account 1000000001 "boss"|player -> admin`,
		`account status|account 1000000001 "boss"|active -> disabled`,
		`account status|account 1000000001 "boss"|disabled -> active`,
		`invite create|invite 1|1 use(s), never expires, note "ann"`,
		`invite revoke|invite 1|`,
		`reset password|account 1000000001 "boss"|a new random password`,
		`account role|account 1000000001 "boss"|admin -> player`,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("audit log:\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// The admin page end to end: dgserver with the website: the start-up line names the admin page, an admin logs in at /admin, sets
// the sign-up mode, which the server keeps across a restart with the same --signup and gives up to a new --signup.
func TestAdminPageOnServer(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	now := time.Now()
	if code := (accountCommands{createAccount: "boss"}).run(dir, &out, &errOut, now); code != 0 {
		t.Fatal(errOut.String())
	}
	pw := regexp.MustCompile(`\n    (\S{16})\n`).FindStringSubmatch(out.String())[1]
	if code := (accountCommands{makeAdmin: "boss"}).run(dir, &out, &errOut, now); code != 0 {
		t.Fatal(errOut.String())
	}

	certs := testCerts(t)
	// run starts dgserver with the website and the sign-up mode signup; stop ends it and returns its log
	run := func(signup accounts.Mode) (base string, stop func() string) {
		t.Helper()
		cfg := config{bind: "127.0.0.1", certs: certs, logDir: t.TempDir(), dataDir: dir, accounts: accounts.Policy{Mode: signup}}
		in := startWeb(t, &cfg)
		return fmt.Sprintf("https://127.0.0.1:%d", cfg.web.port), func() string { in.stop(); return readLog(t, cfg.logDir) }
	}
	base, stop := run(accounts.Open)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	tokenRE := regexp.MustCompile(`name="csrf" value="([^"]+)"`)
	post := func(path string, v url.Values) (int, string) {
		t.Helper()
		res, err := c.Get(base + "/admin")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		v.Set("csrf", tokenRE.FindStringSubmatch(string(b))[1])
		res, err = c.PostForm(base+path, v)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ = io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, body := post("/admin/login", url.Values{"name": {"boss"}, "password": {pw}}); code != 200 || !strings.Contains(body, "Logged in.") {
		t.Fatalf("admin login: %d\n%s", code, body)
	}
	if code, body := post("/admin/signup", url.Values{"mode": {"invite"}}); code != 200 || !strings.Contains(body, "Sign-up is invite now") {
		t.Fatalf("set the mode: %d\n%s", code, body)
	}
	logText := stop()
	if !strings.Contains(logText, "admin page (PH5) at https://127.0.0.1:") || !strings.Contains(logText, "1 admin(s): boss") ||
		!strings.Contains(logText, "POST /admin/signup -> 200: admin boss (account 1000000001): sign-up mode open -> invite") {
		t.Errorf("log:\n%s", logText)
	}
	if strings.Contains(logText, pw) {
		t.Error("the admin's password in the log")
	}

	// a restart with the same --signup keeps the page's choice; a new --signup wins
	_, stop = run(accounts.Open)
	if l := stop(); !strings.Contains(l, "sign-up invite: new accounts only with an invite code") ||
		!strings.Contains(l, "(set on the admin page ") {
		t.Errorf("restart with the same --signup:\n%s", l)
	}
	_, stop = run(accounts.Closed)
	if l := stop(); !strings.Contains(l, "sign-up closed") || strings.Contains(l, "set on the admin page") {
		t.Errorf("restart with a new --signup:\n%s", l)
	}
}
