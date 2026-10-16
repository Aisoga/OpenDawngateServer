package dgserver

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/store"
)

// the operator's account commands on the data folder's database: create an account and reset its password (each
// printed once, logging in with it works), set its status, make, list and revoke invite codes.
func TestAccountCommands(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	run := func(c accountCommands) (string, int) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := c.run(dir, &out, &errOut, now)
		return out.String() + errOut.String(), code
	}
	pwLine := regexp.MustCompile(`\n    (\S{16})\n`)
	out, code := run(accountCommands{createAccount: "Op.Made"})
	m := pwLine.FindStringSubmatch(out)
	if code != 0 || m == nil {
		t.Fatalf("create: %d %s", code, out)
	}
	login := func(pw string) uint16 {
		st, err := store.Open(filepath.Join(dir, store.FileName))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		return accounts.Policy{Mode: accounts.Closed}.Login(st, "op.made", pw, netip.Addr{}, now).Code
	}
	if c := login(m[1]); c != 0 {
		t.Errorf("login with the printed password: %#x", c)
	}
	if out, code := run(accountCommands{createAccount: "op.made"}); code != 1 || !strings.Contains(out, "exists already") {
		t.Errorf("create twice: %d %s", code, out)
	}
	out, code = run(accountCommands{resetPassword: "OP.MADE"})
	m2 := pwLine.FindStringSubmatch(out)
	if code != 0 || m2 == nil || m2[1] == m[1] {
		t.Fatalf("reset: %d %s", code, out)
	}
	if login(m[1]) != accounts.CodeInvalidUser || login(m2[1]) != 0 { // closed: a wrong password as an unknown name
		t.Error("after the reset the old password works or the new one doesn't")
	}
	if out, code := run(accountCommands{accountStatus: "op.made=disabled"}); code != 0 || !strings.Contains(out, "disabled (was active)") {
		t.Errorf("status: %d %s", code, out)
	}
	if login(m2[1]) != accounts.CodeUserInactive {
		t.Error("a disabled account logged in")
	}
	for _, bad := range []accountCommands{{accountStatus: "op.made"}, {accountStatus: "op.made=gone"}, {accountStatus: "x=banned"},
		{resetPassword: "nobody"}, {makeInvite: true, inviteUses: 0}, {revokeInvite: 77}} {
		if out, code := run(bad); code != 1 {
			t.Errorf("%+v: %d %s", bad, code, out)
		}
	}
	out, code = run(accountCommands{makeInvite: true, inviteUses: 2, inviteDays: 1.5, inviteNote: "club"})
	if code != 0 || !regexp.MustCompile(`invite code 1 for 2 account\(s\), valid until 2026-10-15 00:00 UTC:\n    [0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}\n`).MatchString(out) {
		t.Errorf("make invite: %d %s", code, out)
	}
	run(accountCommands{makeInvite: true, inviteUses: 1, inviteDays: 14})
	if out, code := run(accountCommands{revokeInvite: 2}); code != 0 {
		t.Errorf("revoke: %d %s", code, out)
	}
	out, _ = run(accountCommands{listInvites: true})
	for _, want := range []string{"invite 1: made 2026-10-13 12:00 UTC, valid until 2026-10-15 00:00 UTC, used 0, 2 of 2 left (club)",
		"invite 2: made 2026-10-13 12:00 UTC, valid until 2026-10-27 12:00 UTC, used 0, revoked"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	var none bytes.Buffer
	if code := (accountCommands{listInvites: true}).run("", &none, &none, now); code != 2 {
		t.Errorf("without a data folder: %d", code)
	}
}

// --log-days deletes dgserver's own log files and the hosts' folders older than the limit, nothing else.
func TestPruneLogs(t *testing.T) {
	logs := t.TempDir()
	hosts := filepath.Join(logs, "hosts")
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	write := func(p string, mod time.Time) {
		t.Helper()
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, mod, mod)
	}
	write(filepath.Join(logs, "20260801-101010.log"), old)                   // goes
	write(filepath.Join(logs, "20261013-101010.log"), now)                   // stays: new
	write(filepath.Join(logs, "notes.txt"), old)                             // stays: not a dgserver log
	write(filepath.Join(hosts, "1", "dgsim.log"), old)                       // goes
	write(filepath.Join(hosts, "2", "dgsim.log"), old)                       // stays: a newer file inside
	write(filepath.Join(hosts, "2", "AppData", "Application.log"), now)      //
	write(filepath.Join(hosts, "3", "dgsim.log"), now.Add(-29*24*time.Hour)) // stays: younger than 30 days
	for _, d := range []string{filepath.Join(hosts, "1"), filepath.Join(hosts, "2"), filepath.Join(hosts, "3")} {
		os.Chtimes(d, old, old)
	}
	if got := pruneLogs(logs, hosts, 0, now); got != nil {
		t.Errorf("days 0 removed %v", got)
	}
	got := pruneLogs(logs, hosts, 30, now)
	want := []string{filepath.Join(logs, "20260801-101010.log"), filepath.Join(hosts, "1")}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("removed %v, want %v", got, want)
	}
	for _, p := range []string{"20261013-101010.log", "notes.txt", filepath.Join("hosts", "2"), filepath.Join("hosts", "3")} {
		if _, err := os.Stat(filepath.Join(logs, p)); err != nil {
			t.Errorf("%s gone", p)
		}
	}
}

// the flags: --signup and --signups-per-address reach the Blaze server's policy; a bad mode stops dgserver.
func TestSignupFlags(t *testing.T) {
	if code := Main([]string{"--signup", "public", "--print-ports"}); code != 2 {
		t.Errorf("--signup public: exit %d", code)
	}
	if code := Main([]string{"--log-days", "-1", "--print-ports"}); code != 2 {
		t.Errorf("--log-days -1: exit %d", code)
	}
	if s := describeSignup(accounts.Policy{Mode: accounts.Open, SignupsPerAddress: 3}); !strings.Contains(s, "at most 3 a day per public address") {
		t.Errorf("%s", s)
	}
	if s := describeSignup(accounts.Policy{Mode: accounts.Invite}); !strings.Contains(s, "sign-up invite") {
		t.Errorf("%s", s)
	}
}
