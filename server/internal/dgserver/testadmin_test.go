package dgserver

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/blaze"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/site"
	"dawngate/server/internal/store"
)

// SYSCHAT's test aid: --test-admin makes the account (with the given password, default dawngate1) and gives it the
// admin role at the start, with an audit entry; with the limits on (a public address) it is refused.
func TestTestAdminAtStart(t *testing.T) {
	data := t.TempDir()
	log := runBriefly(t, data, "--test-admin", "sysadmin1@x.io")
	if !strings.Contains(log, `TEST: account 1000000001 "sysadmin1@x.io" is an admin (was player) (--test-admin, a test aid)`) {
		t.Fatalf("start:\n%s", log)
	}
	st, err := store.Open(filepath.Join(data, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if c, r := accounts.Authenticate(st, "sysadmin1@x.io", "dawngate1"); r.Code != 0 || c.Role != store.RoleAdmin {
		t.Errorf("the admin's login: %+v, %+v", c, r)
	}
	if a, err := st.AuditLog(10, 0); err != nil || len(a) == 0 || a[0].Action != "account role" || a[0].Source != "test aid" ||
		!strings.Contains(a[0].Detail, "(--test-admin)") {
		t.Errorf("audit %+v, %v", a, err)
	}
	st.Close()
	if log := runBriefly(t, data, "--test-admin", "sysadmin2@x.io|", "--limits", "off"); !strings.Contains(log,
		`TEST: --test-admin: "sysadmin2@x.io|": want <login name>[|<password>]`) {
		t.Errorf("an empty password:\n%s", log)
	}
}

func TestTestAdminRefusedWithLimits(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := applyTestAdmin(st, "sysadmin3@x.io|secret123", true, time.Unix(1_800_000_000, 0)); err == nil ||
		!strings.Contains(err.Error(), "the limits are on") {
		t.Errorf("with the limits on: %v", err)
	}
	if _, found, _ := st.CredentialsByID(1000000001); found {
		t.Error("an account was made with the limits on")
	}
	if note, err := applyTestAdmin(st, "sysadmin3@x.io|secret123", false, time.Unix(1_800_000_000, 0)); err != nil ||
		!strings.Contains(note, "is an admin") {
		t.Errorf("with the limits off: %q, %v", note, err)
	}
	if c, r := accounts.Authenticate(st, "sysadmin3@x.io", "secret123"); r.Code != 0 || c.Role != store.RoleAdmin {
		t.Errorf("the given password: %+v, %+v", c, r)
	}
}

// The admin page's notice goes to Blaze; a second one within the gap comes back as the site's ErrNoticeTooSoon (429),
// a refused text as Blaze's words.
func TestWebsiteNotice(t *testing.T) {
	bz := blaze.NewServer(logging.New(&strings.Builder{}), "127.0.0.1", 17502)
	now := time.Unix(1_800_000_000, 0)
	bz.Now = func() time.Time { return now }
	send := websiteAdmin(bz).Notice
	if n, text, err := send(" Server update\r\nat 20:00 UTC ", "admin boss (account 1000000001)"); err != nil || n != 0 ||
		text != "Server update at 20:00 UTC" {
		t.Errorf("first: %d %q %v", n, text, err)
	}
	if _, _, err := send("Again", "admin boss (account 1000000001)"); !errors.Is(err, site.ErrNoticeTooSoon) ||
		!strings.Contains(err.Error(), "send this one in 10 s") {
		t.Errorf("too soon: %v", err)
	}
	now = now.Add(blaze.NoticeGap)
	if _, _, err := send("a < b", "admin boss (account 1000000001)"); err == nil || errors.Is(err, site.ErrNoticeTooSoon) ||
		!strings.Contains(err.Error(), "markup") {
		t.Errorf("markup: %v", err)
	}
	if websiteAdmin(bz).NoticeMax != blaze.NoticeMaxRunes {
		t.Error("the form's maxlength isn't the server's limit")
	}
}
