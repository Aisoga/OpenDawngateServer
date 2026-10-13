package launcher

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dawngate/server/internal/setup"
	"dawngate/server/internal/store"
)

// Start-Server.ini's login keys become dgserver flags (before server_flags, which may override them), with safe
// defaults for a home or LAN server when the keys are missing (an ini of an earlier release).
func TestLoginFlags(t *testing.T) {
	for _, c := range []struct {
		cfg  Config
		want string
	}{
		{Config{}, "--signup open --signups-per-address 3 --log-days 30"},
		{Config{"signup": "Invite", "signups_per_address": "0", "log_days": "0"}, "--signup invite --signups-per-address 0 --log-days 0"},
		{Config{"signup": "closed", "log_days": "90"}, "--signup closed --signups-per-address 3 --log-days 90"},
	} {
		got, err := LoginFlags(c.cfg)
		if err != nil || strings.Join(got, " ") != c.want {
			t.Errorf("%v: %v %v, want %s", c.cfg, got, err, c.want)
		}
	}
	for _, bad := range []Config{{"signup": "public"}, {"signups_per_address": "-1"}, {"log_days": "a week"}} {
		if _, err := LoginFlags(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if !strings.Contains(loginHint("invite"), "--make-invite") || !strings.Contains(loginHint(""), "first login") ||
		!strings.Contains(loginHint("closed"), "--create-account") {
		t.Error("loginHint")
	}
}

func TestSplitAccountArgs(t *testing.T) {
	for _, c := range []struct{ in, own, acct []string }{
		{[]string{"--check"}, []string{"--check"}, nil},
		{[]string{"--make-invite", "--invite-uses", "5", "--config", "x.ini"}, []string{"--config", "x.ini"},
			[]string{"--make-invite", "--invite-uses", "5"}},
		{[]string{"-reset-password", "Alice", "--non-interactive"}, []string{"--non-interactive"}, []string{"-reset-password", "Alice"}},
		{[]string{"--account-status=bob=banned"}, nil, []string{"--account-status=bob=banned"}},
		{[]string{"--invite-uses", "5"}, []string{"--invite-uses", "5"}, nil}, // an option alone is no command
		// the admin role
		{[]string{"--make-admin", "boss", "--check"}, []string{"--check"}, []string{"--make-admin", "boss"}},
		{[]string{"--remove-admin=boss"}, nil, []string{"--remove-admin=boss"}},
	} {
		own, acct := SplitAccountArgs(c.in)
		if !reflect.DeepEqual(own, c.own) || !reflect.DeepEqual(acct, c.acct) {
			t.Errorf("%v: own %v acct %v", c.in, own, acct)
		}
	}
}

// Start-Server.exe --create-account / --reset-password work on the setup folder's database without a setup or a
// running server.
func TestStartServerAccountCommands(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	o := IO{In: strings.NewReader(""), Out: &out, ExeDir: dir}
	if code := RunServer([]string{"--create-account", "Member1"}, o); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	data := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}.Data()
	if code := RunServer([]string{"--list-invites"}, o); code != 0 {
		t.Fatalf("list: exit %d", code)
	}
	if code := RunServer([]string{"--reset-password", "nobody"}, o); code != 1 {
		t.Errorf("reset of an unknown account: exit %d", code)
	}
	st, err := store.Open(filepath.Join(data, store.FileName)) // dgserver printed to the console; the database says it
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if c, found, _ := st.Credentials("member1"); !found || c.Hash == "" {
		t.Errorf("no account made in %s: %+v", data, c)
	}
	if !strings.HasPrefix(data, dir) {
		t.Errorf("data folder %s", data)
	}
}
