package site

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/limits"
	"dawngate/server/internal/store"
)

// The admin page: under /admin, for accounts with the admin role
// (dgserver --make-admin). An admin logs in with its login name and password (the argon2id check of a game login)
// under a limiter of its own that is stricter than the game's and always on (limits.Admin), plus the game login's
// limiter when dgserver's limits are on; the session is a random cookie (AdminCookie: __Host-, Secure, HttpOnly,
// SameSite=Strict) that ends after AdminIdle without a request, after AdminMax, at a logout, at a restart, or as soon
// as the account isn't an active admin any more (checked at every request). Every form carries the site's CSRF token.
//
// Pages: accounts (search; disable, enable, ban; a new password, shown once; delete as the player's own deletion
// does), invite codes (make one, shown once; list; revoke), the sign-up mode (in force at once and kept across
// restarts: accounts.SetMode), the running matches (players, duration; end one: the host's abortGame), and the audit
// log, which every admin action, every admin login and every one of the operator's account commands writes.

// AdminConfig is the admin page's setup (Config.Admin; nil = no admin page).
type AdminConfig struct {
	// Limits is the admin login's limiter; nil = one with limits.Admin() (always on).
	Limits Limiter
	// Matches lists the running matches, EndMatch ends one (the host's abortGame with the reason); nil = none shown,
	// none can be ended.
	Matches  func() []Match
	EndMatch func(gameID int64, reason string) error
	// Idle and Max end a session (zero = AdminIdle, AdminMax).
	Idle, Max time.Duration
}

// Match is one running match on the admin page.
type Match struct {
	GameID  int64
	Name    string
	Mode    string
	State   string
	Started time.Time
	Host    int64
	Pid     int
	Players []MatchPlayer
}

// MatchPlayer is one human player of a match.
type MatchPlayer struct {
	Name         string
	UserID       int64 // the account
	PersonaID    int64
	Team         int64 // Blaze TIDX (the page shows team TIDX + 1)
	Joined, Left bool  // in the match on the host; left on the Blaze side
}

// AdminCookie is the admin session's cookie.
const AdminCookie = "__Host-dg_admin"

// The session's limits.
const (
	AdminIdle = 30 * time.Minute
	AdminMax  = 12 * time.Hour
	// maxSessions bounds the sessions kept (the oldest goes first).
	maxSessions = 64
)

type adminSession struct {
	userID        int64
	name          string
	created, seen time.Time
}

type adminState struct {
	cfg      AdminConfig
	limits   Limiter
	mu       sync.Mutex
	sessions map[string]*adminSession
}

// adminView is the admin pages' data.
type adminView struct {
	Name     string // the admin logged in ("" = the login form)
	UserID   int64
	Query    string
	Accounts []accountRow
	Invites  []inviteRow
	Matches  []matchRow
	Audit    []store.AuditEntry
	Older    int64 // the audit log's next page (entries before this id; 0 = none)
	// Secret is a new password or invite code, shown once; SecretWhat says what it is.
	Secret, SecretWhat string
	Modes              []string
	ModeSince          string
	CanSetMode         bool
	Counts             adminCounts
	LoginLimits        string
	CanEnd             bool
}

type adminCounts struct {
	Accounts, Admins, Online, Matches int
	Live                              bool
}

type accountRow struct {
	store.AccountSummary
	Displays  string
	Self      bool
	Deleted   bool
	Created   string
	LastLogin string
}

type inviteRow struct {
	store.Invite
	State   string
	Active  bool
	Created string
	Expiry  string
}

type matchRow struct {
	Match
	StartedAt string
	Duration  string
	Players   []string
}

func newAdminState(cfg AdminConfig, now func() time.Time) *adminState {
	if cfg.Idle <= 0 {
		cfg.Idle = AdminIdle
	}
	if cfg.Max <= 0 {
		cfg.Max = AdminMax
	}
	a := &adminState{cfg: cfg, limits: cfg.Limits, sessions: map[string]*adminSession{}}
	if a.limits == nil {
		a.limits = limits.New(limits.Admin(), now)
	}
	return a
}

func (s *Site) adminRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /admin", s.adminHome)
	m.HandleFunc("POST /admin/login", s.adminLogin)
	m.HandleFunc("POST /admin/logout", s.adminLogout)
	m.HandleFunc("GET /admin/accounts", s.adminPage(s.adminAccounts))
	m.HandleFunc("POST /admin/accounts/status", s.adminAction(s.adminSetStatus))
	m.HandleFunc("POST /admin/accounts/password", s.adminAction(s.adminResetPassword))
	m.HandleFunc("POST /admin/accounts/delete", s.adminAction(s.adminDelete))
	m.HandleFunc("GET /admin/invites", s.adminPage(s.adminInvites))
	m.HandleFunc("POST /admin/invites", s.adminAction(s.adminMakeInvite))
	m.HandleFunc("POST /admin/invites/revoke", s.adminAction(s.adminRevokeInvite))
	m.HandleFunc("GET /admin/signup", s.adminPage(s.adminSignup))
	m.HandleFunc("POST /admin/signup", s.adminAction(s.adminSetSignup))
	m.HandleFunc("GET /admin/matches", s.adminPage(s.adminMatches))
	m.HandleFunc("POST /admin/matches/end", s.adminAction(s.adminEndMatch))
	m.HandleFunc("GET /admin/audit", s.adminPage(s.adminAudit))
}

// ---- sessions ----

// session returns the request's admin session, checked against the store (an active admin still); nil = none.
func (s *Site) session(r *http.Request) *adminSession {
	c, err := r.Cookie(AdminCookie)
	if err != nil || len(c.Value) < 32 || len(c.Value) > 64 {
		return nil
	}
	a := s.admin
	now := s.cfg.Now()
	a.mu.Lock()
	sess := a.sessions[c.Value]
	if sess != nil && (now.Sub(sess.seen) > a.cfg.Idle || now.Sub(sess.created) > a.cfg.Max) {
		delete(a.sessions, c.Value)
		sess = nil
	}
	if sess != nil {
		sess.seen = now
	}
	var cp adminSession
	if sess != nil {
		cp = *sess
	}
	a.mu.Unlock()
	if sess == nil {
		return nil
	}
	cred, found, err := s.cfg.Store.CredentialsByID(cp.userID)
	if err != nil || !found || cred.Role != store.RoleAdmin || cred.Status != store.StatusActive {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
		return nil
	}
	cp.name = cred.Name
	return &cp
}

func (s *Site) newSession(w http.ResponseWriter, cred store.Credentials) {
	raw := make([]byte, 32)
	rand.Read(raw)
	token := b64.EncodeToString(raw)
	a := s.admin
	now := s.cfg.Now()
	a.mu.Lock()
	for k, v := range a.sessions { // expired ones go; then the oldest beyond the cap
		if now.Sub(v.seen) > a.cfg.Idle || now.Sub(v.created) > a.cfg.Max {
			delete(a.sessions, k)
		}
	}
	for len(a.sessions) >= maxSessions {
		oldest := ""
		for k, v := range a.sessions {
			if oldest == "" || v.created.Before(a.sessions[oldest].created) {
				oldest = k
			}
		}
		delete(a.sessions, oldest)
	}
	a.sessions[token] = &adminSession{userID: cred.UserID, name: cred.Name, created: now, seen: now}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: AdminCookie, Value: token, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode})
}

func (s *Site) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(AdminCookie); err == nil {
		s.admin.mu.Lock()
		delete(s.admin.sessions, c.Value)
		s.admin.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: AdminCookie, Value: "", Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func actor(sess *adminSession, r *http.Request) accounts.Actor {
	return accounts.Actor{Name: sess.name, UserID: sess.userID, Source: "web " + clientAddr(r)}
}

// audit writes an audit log entry (a failure is logged; the action stays done).
func (s *Site) audit(w http.ResponseWriter, r *http.Request, who accounts.Actor, action, target, detail string) {
	if err := accounts.Audit(s.cfg.Store, who, action, target, detail, s.cfg.Now()); err != nil && s.cfg.Log != nil {
		s.cfg.Log("%s %s: the audit log: %v", clientAddr(r), r.URL.Path, err)
	}
	if target != "" {
		noteFor(w, "%s: %s %s %s", who, action, target, detail)
	} else {
		noteFor(w, "%s: %s %s", who, action, detail)
	}
}

// ---- the login ----

func (s *Site) adminRender(w http.ResponseWriter, r *http.Request, status int, page string, v *view) {
	if v.Admin == nil {
		v.Admin = &adminView{}
	}
	v.CSRF = s.csrfToken(w, r)
	s.render(w, r, status, page, v)
}

func (s *Site) loginView() *view {
	return &view{Title: "Admin login", Admin: &adminView{LoginLimits: limits.Admin().DescribeLogins()}}
}

func (s *Site) adminHome(w http.ResponseWriter, r *http.Request) {
	sess := s.session(r)
	if sess == nil {
		s.adminRender(w, r, http.StatusOK, "admin_login", s.loginView())
		return
	}
	s.adminRender(w, r, http.StatusOK, "admin_home", s.homeView(sess, ""))
}

func (s *Site) homeView(sess *adminSession, done string) *view {
	v := &view{Title: "Admin", Done: done, Admin: &adminView{Name: sess.name, UserID: sess.userID}}
	c := &v.Admin.Counts
	c.Accounts, _ = s.cfg.Store.Count()
	if admins, err := s.cfg.Store.Admins(); err == nil {
		c.Admins = len(admins)
	}
	if s.cfg.Status != nil {
		st := s.cfg.Status()
		c.Online, c.Matches, c.Live = st.PlayersOnline, st.MatchesRunning, true
	}
	return v
}

// adminLogin checks an admin login: the CSRF token and the form rate (guard), the admin limiter and (with dgserver's
// limits on) the game login's, then the password and the role. Every refusal has the same text, so the page doesn't
// tell whether a name exists or is an admin; each is a failure for both limiters and an audit log entry.
func (s *Site) adminLogin(w http.ResponseWriter, r *http.Request) {
	v := s.loginView()
	if !s.guard(w, r, "admin_login", v) {
		return
	}
	v.Name = strings.TrimSpace(r.PostFormValue("name"))
	key, addr := accountKey(v.Name), clientAddr(r)
	if wait, why := s.admin.limits.Login(addr, key); wait > 0 {
		s.tooMany(w, r, "admin_login", v, wait, "admin login limits: "+why)
		return
	}
	if !s.limitLogin(w, r, "admin_login", v, key) {
		return
	}
	cred, res := accounts.Authenticate(s.cfg.Store, v.Name, r.PostFormValue("password"))
	ok := res.Code == 0 && cred.Role == store.RoleAdmin
	s.admin.limits.LoginDone(addr, key, ok)
	if s.cfg.Limits != nil {
		s.cfg.Limits.LoginDone(addr, key, ok)
	}
	who := accounts.Actor{Name: cred.Name, UserID: cred.UserID, Source: "web " + addr}
	if !ok {
		why := res.Note
		if res.Code == 0 {
			why = fmt.Sprintf("account %d is no admin", cred.UserID)
		}
		accounts.Audit(s.cfg.Store, accounts.Actor{Source: "web " + addr}, "admin login refused", fmt.Sprintf("%q", v.Name),
			why, s.cfg.Now())
		noteFor(w, "admin login refused: %s", why)
		v.Error = "Wrong login name or password, or this account is no admin."
		s.adminRender(w, r, http.StatusUnauthorized, "admin_login", v)
		return
	}
	s.newSession(w, cred)
	s.audit(w, r, who, "admin login", "", "")
	sess := &adminSession{userID: cred.UserID, name: cred.Name}
	s.adminRender(w, r, http.StatusOK, "admin_home", s.homeView(sess, "Logged in."))
}

func (s *Site) adminLogout(w http.ResponseWriter, r *http.Request) {
	v := s.loginView()
	if !s.guard(w, r, "admin_login", v) {
		return
	}
	if sess := s.session(r); sess != nil {
		s.audit(w, r, actor(sess, r), "admin logout", "", "")
	}
	s.endSession(w, r)
	v.Done = "Logged out."
	s.adminRender(w, r, http.StatusOK, "admin_login", v)
}

// adminPage wraps a GET page: without a session the login form (303 to /admin).
func (s *Site) adminPage(page func(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := s.session(r)
		if sess == nil {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
		v := &view{Admin: &adminView{Name: sess.name, UserID: sess.userID}}
		page(w, r, sess, v)
	}
}

// adminAction wraps a POST action: the session (else 403 with the login form), then guard (CSRF, form rate).
func (s *Site) adminAction(act func(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := s.session(r)
		if sess == nil {
			v := s.loginView()
			v.Error = "Your admin session has ended: log in again."
			noteFor(w, "refused: no admin session")
			s.adminRender(w, r, http.StatusForbidden, "admin_login", v)
			return
		}
		v := &view{Admin: &adminView{Name: sess.name, UserID: sess.userID}}
		if !s.guard(w, r, "admin_home", s.homeView(sess, "")) {
			return
		}
		act(w, r, sess, v)
	}
}

// ---- accounts ----

func (s *Site) adminAccounts(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	s.showAccounts(w, r, sess, v, http.StatusOK, r.URL.Query().Get("q"))
}

func (s *Site) showAccounts(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view, status int, q string) {
	v.Title = "Accounts"
	q = strings.TrimSpace(q)
	if len(q) > 64 {
		q = q[:64]
	}
	v.Admin.Query = q
	list, err := s.cfg.Store.FindAccounts(q, 50)
	if err != nil {
		v.Error = "The account store failed: " + err.Error()
		status = http.StatusInternalServerError
	}
	for _, a := range list {
		row := accountRow{AccountSummary: a, Displays: strings.Join(a.Personas, ", "), Self: a.UserID == sess.userID,
			Deleted: a.Status == store.StatusDeleted, Created: day(a.Created), LastLogin: day(a.LastLogin)}
		v.Admin.Accounts = append(v.Admin.Accounts, row)
	}
	s.adminRender(w, r, status, "admin_accounts", v)
}

func day(t time.Time) string {
	if t.IsZero() || t.Year() < 2000 {
		return "never"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// formID reads a positive id from the form.
func formID(r *http.Request, field string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue(field)), 10, 64)
	return id, err == nil && id > 0
}

func (s *Site) refuseAction(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view, show func(int), status int, text string) {
	noteFor(w, "refused: %s", text)
	v.Error = text
	show(status)
}

func (s *Site) adminSetStatus(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	q := r.PostFormValue("q")
	show := func(status int) { s.showAccounts(w, r, sess, v, status, q) }
	id, ok := formID(r, "id")
	st, err := store.ParseStatus(r.PostFormValue("status"))
	if !ok || err != nil || st == store.StatusDeleted {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Choose an account and a status (active, disabled, banned).")
		return
	}
	who := actor(sess, r)
	before, err := accounts.SetStatusByID(s.cfg.Store, who, id, st)
	if err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Not changed: "+err.Error()+".")
		return
	}
	s.audit(w, r, who, "account status", accounts.AccountTarget(id, before.Name), fmt.Sprintf("%s -> %s", before.Status, st))
	v.Done = fmt.Sprintf("Account %d %s is %s now (was %s); its next login is refused.", id, before.Name, st, before.Status)
	if st != store.StatusActive { // and it stops playing now
		v.Done += s.endSessions(id, fmt.Sprintf("%s set it %s on the admin page", who, st))
	}
	if st == store.StatusActive {
		v.Done = fmt.Sprintf("Account %d %s is active now (was %s).", id, before.Name, before.Status)
	}
	show(http.StatusOK)
}

func (s *Site) adminResetPassword(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	q := r.PostFormValue("q")
	show := func(status int) { s.showAccounts(w, r, sess, v, status, q) }
	id, ok := formID(r, "id")
	if !ok {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Choose an account.")
		return
	}
	cred, pw, err := accounts.ResetPasswordByID(s.cfg.Store, id)
	if err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Not changed: "+err.Error()+".")
		return
	}
	s.audit(w, r, actor(sess, r), "reset password", accounts.AccountTarget(id, cred.Name), "a new random password")
	v.Admin.Secret, v.Admin.SecretWhat = pw, fmt.Sprintf("The new password of account %d %s", id, cred.Name)
	v.Done = "The old password no longer works. Give the player the new one; it is shown only now."
	show(http.StatusOK)
}

func (s *Site) adminDelete(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	q := r.PostFormValue("q")
	show := func(status int) { s.showAccounts(w, r, sess, v, status, q) }
	id, ok := formID(r, "id")
	if !ok {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Choose an account.")
		return
	}
	if r.PostFormValue("confirm") != "yes" {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Tick the box beside Delete to confirm.")
		return
	}
	cred, err := accounts.DeleteByID(s.cfg.Store, actor(sess, r), id)
	if err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Not deleted: "+err.Error()+".")
		return
	}
	s.audit(w, r, actor(sess, r), "delete account", accounts.AccountTarget(id, cred.Name),
		"personal data removed, match history kept without the name")
	v.Done = fmt.Sprintf("Account %d %s is deleted: its login name, display names, password, settings, lists and items "+
		"are gone; its match history stays without the name.", id, cred.Name) +
		s.endSessions(id, fmt.Sprintf("%s deleted it on the admin page", actor(sess, r)))
	show(http.StatusOK)
}

// ---- invite codes ----

func (s *Site) adminInvites(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	s.showInvites(w, r, v, http.StatusOK)
}

func (s *Site) showInvites(w http.ResponseWriter, r *http.Request, v *view, status int) {
	v.Title = "Invite codes"
	all, err := s.cfg.Store.Invites()
	if err != nil {
		v.Error = "The account store failed: " + err.Error()
		status = http.StatusInternalServerError
	}
	now := s.cfg.Now()
	for i := len(all) - 1; i >= 0; i-- { // newest first
		inv := all[i]
		row := inviteRow{Invite: inv, Created: day(inv.Created), Expiry: "never"}
		if !inv.Expires.IsZero() {
			row.Expiry = day(inv.Expires)
		}
		switch left := inv.Left(now); {
		case inv.Revoked:
			row.State = "revoked"
		case !inv.Expires.IsZero() && !now.Before(inv.Expires):
			row.State = "expired"
		case left == 0:
			row.State = "used up"
		default:
			row.State, row.Active = fmt.Sprintf("%d left", left), true
		}
		v.Admin.Invites = append(v.Admin.Invites, row)
	}
	s.adminRender(w, r, status, "admin_invites", v)
}

// cleanText keeps a short free text (a note, a reason): printable characters only, at most n runes.
func cleanText(s string, n int) string {
	var b strings.Builder
	count := 0
	for _, r := range strings.TrimSpace(s) {
		if count == n {
			break
		}
		if unicode.IsPrint(r) {
			b.WriteRune(r)
			count++
		}
	}
	return b.String()
}

func (s *Site) adminMakeInvite(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	show := func(status int) { s.showInvites(w, r, v, status) }
	uses, err1 := strconv.Atoi(strings.TrimSpace(r.PostFormValue("uses")))
	days, err2 := strconv.ParseFloat(strings.TrimSpace(r.PostFormValue("days")), 64)
	if err1 != nil || err2 != nil || uses < 1 || uses > 1000 || days < 0 || days > 3650 {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Uses: 1 to 1000; days: 0 (never expires) to 3650.")
		return
	}
	note := cleanText(r.PostFormValue("note"), 100)
	code, inv, err := accounts.NewInvite(s.cfg.Store, uses, time.Duration(days*float64(24*time.Hour)), note, s.cfg.Now())
	if err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusInternalServerError, "Not made: "+err.Error()+".")
		return
	}
	exp := "never expires"
	if !inv.Expires.IsZero() {
		exp = "valid until " + day(inv.Expires) + " UTC"
	}
	s.audit(w, r, actor(sess, r), "invite create", fmt.Sprintf("invite %d", inv.ID), fmt.Sprintf("%d use(s), %s, note %q",
		uses, exp, note))
	v.Admin.Secret, v.Admin.SecretWhat = code, fmt.Sprintf("Invite code %d (%d use(s), %s)", inv.ID, uses, exp)
	v.Done = "Hand the code to the player; it is shown only now (the server keeps only its hash)."
	show(http.StatusOK)
}

func (s *Site) adminRevokeInvite(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	show := func(status int) { s.showInvites(w, r, v, status) }
	id, ok := formID(r, "id")
	if !ok {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Choose an invite code.")
		return
	}
	if err := s.cfg.Store.RevokeInvite(id); err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Not revoked: "+err.Error()+".")
		return
	}
	s.audit(w, r, actor(sess, r), "invite revoke", fmt.Sprintf("invite %d", id), "")
	v.Done = fmt.Sprintf("Invite code %d is revoked.", id)
	show(http.StatusOK)
}

// ---- the sign-up mode ----

func (s *Site) adminSignup(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	s.showSignup(w, r, v, http.StatusOK)
}

func (s *Site) showSignup(w http.ResponseWriter, r *http.Request, v *view, status int) {
	v.Title = "Sign-up mode"
	v.Admin.Modes = []string{accounts.Open.String(), accounts.Invite.String(), accounts.Closed.String()}
	v.Admin.CanSetMode = s.cfg.Policy.Live != nil
	if _, changed, found, err := s.cfg.Store.Setting(accounts.SettingSignup); err == nil && found {
		v.Admin.ModeSince = day(changed) + " UTC"
	}
	s.adminRender(w, r, status, "admin_signup", v)
}

func (s *Site) adminSetSignup(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	show := func(status int) { s.showSignup(w, r, v, status) }
	if s.cfg.Policy.Live == nil {
		s.refuseAction(w, r, sess, v, show, http.StatusConflict, "This server's sign-up mode can't be changed here.")
		return
	}
	m, err := accounts.ParseMode(r.PostFormValue("mode"))
	if err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Choose open, invite or closed.")
		return
	}
	before, err := accounts.SetMode(s.cfg.Store, s.cfg.Policy.Live, m, s.cfg.Now())
	if err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusInternalServerError, "Not changed: "+err.Error()+".")
		return
	}
	s.audit(w, r, actor(sess, r), "sign-up mode", "", fmt.Sprintf("%s -> %s", before, m))
	v.Done = fmt.Sprintf("Sign-up is %s now (was %s), for the game's login and the website alike. It stays so after a "+
		"restart until Start-Server.ini's signup (--signup) is changed.", m, before)
	show(http.StatusOK)
}

// ---- matches ----

func (s *Site) adminMatches(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	s.showMatches(w, r, v, http.StatusOK)
}

// duration is a match's running time for the page.
func duration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	return fmt.Sprintf("%d min %02d s", int(d.Minutes()), int(d.Seconds())%60)
}

func (s *Site) showMatches(w http.ResponseWriter, r *http.Request, v *view, status int) {
	v.Title = "Running matches"
	v.Admin.CanEnd = s.admin.cfg.EndMatch != nil
	if s.admin.cfg.Matches != nil {
		now := s.cfg.Now()
		for _, m := range s.admin.cfg.Matches() {
			row := matchRow{Match: m, StartedAt: day(m.Started) + " UTC", Duration: duration(now.Sub(m.Started))}
			for _, p := range m.Players {
				state := "connecting"
				switch {
				case p.Left:
					state = "left"
				case p.Joined:
					state = "in the match"
				}
				row.Players = append(row.Players, fmt.Sprintf("%s (account %d, team %d, %s)", p.Name, p.UserID, p.Team+1, state))
			}
			v.Admin.Matches = append(v.Admin.Matches, row)
		}
	}
	s.adminRender(w, r, status, "admin_matches", v)
}

func (s *Site) adminEndMatch(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	show := func(status int) { s.showMatches(w, r, v, status) }
	id, ok := formID(r, "game")
	if !ok || s.admin.cfg.EndMatch == nil {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Choose a match.")
		return
	}
	if r.PostFormValue("confirm") != "yes" {
		s.refuseAction(w, r, sess, v, show, http.StatusBadRequest, "Tick the box beside End to confirm.")
		return
	}
	who := actor(sess, r)
	reason := fmt.Sprintf("ended by %s on the admin page", who)
	if why := cleanText(r.PostFormValue("reason"), 120); why != "" {
		reason += ": " + why
	}
	if err := s.admin.cfg.EndMatch(id, reason); err != nil {
		s.refuseAction(w, r, sess, v, show, http.StatusConflict, fmt.Sprintf("Match %d not ended: %v.", id, err))
		return
	}
	s.audit(w, r, who, "end match", fmt.Sprintf("game %d", id), reason)
	v.Done = fmt.Sprintf("Match %d is ending: its host got abortGame; its players go back to the main menu. Nothing is "+
		"stored for it.", id)
	show(http.StatusOK)
}

// ---- the audit log ----

func (s *Site) adminAudit(w http.ResponseWriter, r *http.Request, sess *adminSession, v *view) {
	v.Title = "Audit log"
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	const page = 100
	log, err := s.cfg.Store.AuditLog(page, before)
	status := http.StatusOK
	if err != nil {
		v.Error, status = "The account store failed: "+err.Error(), http.StatusInternalServerError
	}
	v.Admin.Audit = log
	if len(log) == page {
		v.Admin.Older = log[len(log)-1].ID
	}
	s.adminRender(w, r, status, "admin_audit", v)
}
