package accounts

import (
	"errors"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/store"
)

// the admin role, the admin page's actions by account id and their refusals.
func TestAdminActions(t *testing.T) {
	st := store.New()
	defer st.Close()
	admin, _, err := CreateAccount(st, "boss", t0)
	if err != nil {
		t.Fatal(err)
	}
	player, _, _ := CreateAccount(st, "player1", t0)
	if _, err := SetAdmin(st, "BOSS", true); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := st.Credentials("boss"); c.Role != store.RoleAdmin {
		t.Fatalf("role %v", c.Role)
	}
	if _, err := SetAdmin(st, "nobody", true); err == nil {
		t.Error("an unknown name made admin")
	}
	who := Actor{Name: "boss", UserID: admin.UserID, Source: "web 127.0.0.2"}
	if who.String() != "admin boss (account 1000000001)" || CommandLine.String() != "operator" {
		t.Errorf("actors %q %q", who, CommandLine)
	}

	// disable, and the login refused with 0x0065 after the right password; enable again
	if _, err := SetStatusByID(st, who, player.UserID, store.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	_, pw, err := ResetPasswordByID(st, player.UserID)
	if err != nil || len(pw) != 16 {
		t.Fatalf("reset %q %v", pw, err)
	}
	if r := (Policy{}).Login(st, "player1", pw, loopback, t0); r.Code != CodeUserInactive {
		t.Errorf("a disabled account's login: %#x %s", r.Code, r.Note)
	}
	if _, err := SetStatusByID(st, who, player.UserID, store.StatusActive); err != nil {
		t.Fatal(err)
	}
	if r := (Policy{}).Login(st, "player1", pw, loopback, t0); r.Code != 0 {
		t.Errorf("enabled again: %#x %s", r.Code, r.Note)
	}
	// not on the own account; deleting only by the delete action
	if _, err := SetStatusByID(st, who, admin.UserID, store.StatusBanned); !errors.Is(err, ErrSelf) {
		t.Errorf("own ban: %v", err)
	}
	if _, err := DeleteByID(st, who, admin.UserID); !errors.Is(err, ErrSelf) {
		t.Errorf("own delete: %v", err)
	}
	if _, err := SetStatusByID(st, who, player.UserID, store.StatusDeleted); err == nil {
		t.Error("deleted by a status")
	}
	if _, err := SetStatusByID(st, who, 1, store.StatusDisabled); err == nil || !strings.Contains(err.Error(), "no account 1") {
		t.Errorf("unknown id: %v", err)
	}
	// delete: the name is free again, the account can't be changed any more
	if _, err := DeleteByID(st, who, player.UserID); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := st.Credentials("player1"); found {
		t.Error("the deleted name still found")
	}
	if _, err := SetStatusByID(st, who, player.UserID, store.StatusActive); !errors.Is(err, ErrDeleted) {
		t.Errorf("enable a deleted account: %v", err)
	}
	if _, _, err := ResetPasswordByID(st, player.UserID); !errors.Is(err, ErrDeleted) {
		t.Errorf("reset a deleted account: %v", err)
	}
	if _, err := DeleteByID(st, who, player.UserID); !errors.Is(err, ErrDeleted) {
		t.Errorf("delete twice: %v", err)
	}
	// a disabled account can't become an admin; --remove-admin takes the role
	other, _, _ := CreateAccount(st, "other", t0)
	SetStatusByID(st, who, other.UserID, store.StatusDisabled)
	if _, err := SetAdmin(st, "other", true); err == nil {
		t.Error("a disabled account made admin")
	}
	if _, err := SetAdmin(st, "boss", false); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := st.Credentials("boss"); c.Role != store.RolePlayer {
		t.Errorf("role after --remove-admin %v", c.Role)
	}
	if err := Audit(st, who, "account status", AccountTarget(2, "x"), "active -> disabled", t0); err != nil {
		t.Fatal(err)
	}
	log, _ := st.AuditLog(1, 0)
	if len(log) != 1 || log[0].Actor != "admin boss (account 1000000001)" || log[0].Source != "web 127.0.0.2" ||
		log[0].Target != `account 2 "x"` {
		t.Errorf("audit %+v", log)
	}
}

// the sign-up mode: live for logins and registrations, kept across restarts; the most recent change wins.
func TestSignupModeSetting(t *testing.T) {
	st := store.New()
	defer st.Close()
	live := NewModeSetting(Open)
	p := Policy{Mode: Open, Live: live}
	if r := p.Login(st, "newname", "dawngate1", loopback, t0); r.Code != 0 || !r.First {
		t.Fatalf("open: %#x", r.Code)
	}
	if _, err := SetMode(st, live, Invite, t0); err != nil {
		t.Fatal(err)
	}
	if r := p.Login(st, "another", "dawngate1", loopback, t0); r.Code != CodeInvalidUser {
		t.Errorf("invite (a copy of the policy sees the change): %#x", r.Code)
	}
	if r := p.Register(st, "another", "", "dawngate1", "", loopback, t0); r.Code != CodeInvalidCode {
		t.Errorf("register under invite without a code: %#x", r.Code)
	}

	// restarts: the first start stores the flag's mode
	st2 := store.New()
	defer st2.Close()
	day := 24 * time.Hour
	m, note, err := StartMode(st2, Open, t0)
	if m != Open || note != "" || err != nil {
		t.Fatalf("first start %v %q %v", m, note, err)
	}
	live2 := NewModeSetting(m)
	SetMode(st2, live2, Closed, t0.Add(day)) // the admin page
	m, note, _ = StartMode(st2, Open, t0.Add(2*day))
	if m != Closed || !strings.Contains(note, "set on the admin page") {
		t.Errorf("restart with the same flag: %v %q", m, note)
	}
	m, note, _ = StartMode(st2, Invite, t0.Add(3*day)) // the operator changed the ini: that is newer
	if m != Invite || note != "" {
		t.Errorf("restart with a new flag: %v %q", m, note)
	}
	log, _ := st2.AuditLog(5, 0)
	if len(log) != 1 || log[0].Action != "sign-up mode" || log[0].Detail != "closed -> invite (--signup / Start-Server.ini)" ||
		log[0].Source != "server start" {
		t.Errorf("audit %+v", log)
	}
	if m, _, _ = StartMode(st2, Invite, t0.Add(4*day)); m != Invite {
		t.Errorf("unchanged: %v", m)
	}
}
