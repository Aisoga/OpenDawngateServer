package site

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"dawngate/server/internal/accounts"
)

// The forms. Each POST passes guard (the CSRF check and the address's form rate) before it reads a field; the forms
// that check a password also ask the limiter (Limits) first and report the result to it.

// guard checks a POST's CSRF token and the address's form rate; false = it answered already (403 or 429).
func (s *Site) guard(w http.ResponseWriter, r *http.Request, page string, v *view) bool {
	if err := r.ParseForm(); err != nil {
		noteFor(w, "form not read: %v", err)
		s.render(w, r, http.StatusRequestEntityTooLarge, "message", &view{Title: "Form refused",
			Text: "The form could not be read. Go back, reload the page and try again."})
		return false
	}
	if ok, why := s.csrfOK(r); !ok {
		noteFor(w, "refused: %s", why)
		v.CSRF = s.csrfToken(w, r)
		v.Error = "The form had expired (or came from another site). It is fresh now: please send it again."
		s.render(w, r, http.StatusForbidden, page, v)
		return false
	}
	if wait := s.forms.take(clientAddr(r), s.cfg.Now()); wait > 0 {
		s.tooMany(w, r, page, v, wait, "the address's form rate")
		return false
	}
	return true
}

// tooMany answers 429 with the form again.
func (s *Site) tooMany(w http.ResponseWriter, r *http.Request, page string, v *view, wait time.Duration, why string) {
	secs := int(math.Ceil(wait.Seconds()))
	noteFor(w, "refused: %s (retry in %d s)", why, secs)
	w.Header().Set("Retry-After", fmt.Sprint(secs))
	v.CSRF = s.csrfToken(w, r)
	v.Error = fmt.Sprintf("Too many attempts from your address. Please wait %s and try again.", wait.Round(time.Second))
	s.render(w, r, http.StatusTooManyRequests, page, v)
}

// limitLogin asks the limiter before a password check (account "" = a registration: the address's rate only).
func (s *Site) limitLogin(w http.ResponseWriter, r *http.Request, page string, v *view, account string) bool {
	if s.cfg.Limits == nil {
		return true
	}
	if wait, why := s.cfg.Limits.Login(clientAddr(r), account); wait > 0 {
		s.tooMany(w, r, page, v, wait, "limits: "+why)
		return false
	}
	return true
}

// loginDone tells the limiter how a password check went (only a wrong name or password counts as a failure).
func (s *Site) loginDone(r *http.Request, account string, res accounts.Result) {
	if s.cfg.Limits == nil {
		return
	}
	bad := res.Code == accounts.CodeInvalidUser || res.Code == accounts.CodeInvalidPassword
	s.cfg.Limits.LoginDone(clientAddr(r), account, !bad)
}

// accountKey is the limiter's key of a login name (limits.AccountKey: trimmed, lower case).
func accountKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// message is the player's text for a refusal.
func message(res accounts.Result) (field, text string) {
	field = res.Field
	if res.Code == accounts.CodeInvalidPassword && (field == accounts.FieldNew || field == accounts.FieldPassword) {
		// an empty new password (registration, change: a rule's refusal names its field; a wrong password names none)
		return field, fmt.Sprintf("A password has %d to %d characters.", accounts.MinPassword, accounts.MaxPassword)
	}
	switch res.Code {
	case accounts.CodeInvalidUser, accounts.CodeInvalidPassword:
		return accounts.FieldPassword, "Wrong login name or password."
	case accounts.CodeExists:
		if field == accounts.FieldDisplay {
			return field, "This display name is taken: choose another."
		}
		return accounts.FieldName, "This login name is taken: choose another."
	case accounts.CodeInvalidName, accounts.CodeTooShort, accounts.CodeTooLong:
		switch field {
		case accounts.FieldName:
			return field, fmt.Sprintf("A login name has %d to %d characters: letters, digits and . _ - @.", accounts.MinName, accounts.MaxName)
		case accounts.FieldDisplay:
			return field, fmt.Sprintf("A display name has %d to %d characters: letters, digits, _ and -.", accounts.MinDisplay, accounts.MaxDisplay)
		case accounts.FieldPassword, accounts.FieldNew:
			return field, fmt.Sprintf("A password has %d to %d characters.", accounts.MinPassword, accounts.MaxPassword)
		}
		return field, "A login name or password is too long."
	case accounts.CodeInvalidCode:
		return accounts.FieldCode, "This invite code is unknown, expired or revoked."
	case accounts.CodeCodeUsed:
		return accounts.FieldCode, "This invite code has been used up."
	case accounts.CodeBusy:
		return "", "Your address has made its accounts for today. Please try again tomorrow."
	case accounts.CodeUserInactive:
		return "", "This account is disabled. Ask the server's operator."
	case accounts.CodeBanned:
		return "", "This account is banned."
	case accounts.CodeDeactivated:
		return "", "This account is deleted."
	}
	return "", "The server could not do this just now. Please try again later."
}

// refused shows the form again with the refusal.
func (s *Site) refused(w http.ResponseWriter, r *http.Request, status int, page string, v *view, res accounts.Result) {
	noteFor(w, "refused %#04x: %s", res.Code, res.Note)
	field, text := message(res)
	v.Error = text
	if field != "" {
		v.Fields = map[string]string{field: text}
	}
	v.CSRF = s.csrfToken(w, r)
	s.render(w, r, status, page, v)
}

// ---- registration ----

func (s *Site) signupView() *view { return &view{Title: "Make an account"} }

func (s *Site) signupForm(w http.ResponseWriter, r *http.Request) {
	v := s.signupView()
	v.CSRF = s.csrfToken(w, r)
	s.render(w, r, http.StatusOK, "signup", v)
}

func (s *Site) signup(w http.ResponseWriter, r *http.Request) {
	v := s.signupView()
	if !s.guard(w, r, "signup", v) {
		return
	}
	v.Name, v.Display = strings.TrimSpace(r.PostFormValue("name")), strings.TrimSpace(r.PostFormValue("display"))
	if s.cfg.Policy.CurrentMode() == accounts.Closed {
		noteFor(w, "refused: sign-up is closed")
		s.render(w, r, http.StatusForbidden, "signup", v)
		return
	}
	pw := r.PostFormValue("password")
	if pw != r.PostFormValue("password2") {
		noteFor(w, "refused: the two passwords differ")
		v.Error, v.Fields, v.CSRF = "The two passwords differ.", map[string]string{"password2": "The two passwords differ."}, s.csrfToken(w, r)
		s.render(w, r, http.StatusBadRequest, "signup", v)
		return
	}
	if !s.limitLogin(w, r, "signup", v, "") {
		return
	}
	res := s.cfg.Policy.Register(s.cfg.Store, v.Name, v.Display, pw, r.PostFormValue("code"), sourceAddr(r), s.cfg.Now())
	if res.Code != 0 {
		status := http.StatusBadRequest
		if res.Code == accounts.CodeBusy {
			status = http.StatusTooManyRequests
		} else if res.Code == accounts.CodeSystem {
			status = http.StatusInternalServerError
		}
		s.refused(w, r, status, "signup", v, res)
		return
	}
	noteFor(w, "%s", res.Note)
	display := ""
	if len(res.Account.Personas) > 0 {
		display = res.Account.Personas[0].Name
	}
	s.render(w, r, http.StatusOK, "message", &view{Title: "Account made", Done: fmt.Sprintf(
		"Your account %s is ready; other players see you as %s. Start the game and log in with this login name and "+
			"your password.", res.Account.Email, display)})
}

// ---- change the password ----

func (s *Site) changeForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "change", &view{Title: "Change your password", CSRF: s.csrfToken(w, r)})
}

func (s *Site) change(w http.ResponseWriter, r *http.Request) {
	v := &view{Title: "Change your password"}
	if !s.guard(w, r, "change", v) {
		return
	}
	v.Name = strings.TrimSpace(r.PostFormValue("name"))
	pw := r.PostFormValue("new")
	if pw != r.PostFormValue("new2") {
		noteFor(w, "refused: the two new passwords differ")
		v.Error, v.Fields, v.CSRF = "The two new passwords differ.", map[string]string{"new2": "The two new passwords differ."}, s.csrfToken(w, r)
		s.render(w, r, http.StatusBadRequest, "change", v)
		return
	}
	key := accountKey(v.Name)
	if !s.limitLogin(w, r, "change", v, key) {
		return
	}
	res := accounts.ChangePassword(s.cfg.Store, v.Name, r.PostFormValue("password"), pw)
	s.loginDone(r, key, res)
	if res.Code != 0 {
		s.refused(w, r, statusOf(res), "change", v, res)
		return
	}
	noteFor(w, "%s", res.Note)
	s.render(w, r, http.StatusOK, "message", &view{Title: "Password changed",
		Done: "Your password is changed. Log in with the new one from now on."})
}

// statusOf is a self-service refusal's HTTP status.
func statusOf(res accounts.Result) int {
	switch res.Code {
	case accounts.CodeInvalidUser, accounts.CodeInvalidPassword:
		return http.StatusUnauthorized
	case accounts.CodeUserInactive, accounts.CodeBanned, accounts.CodeDeactivated:
		return http.StatusForbidden
	case accounts.CodeSystem:
		return http.StatusInternalServerError
	}
	return http.StatusBadRequest
}

// ---- delete the account ----

func (s *Site) deleteForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "delete", &view{Title: "Delete your account", CSRF: s.csrfToken(w, r)})
}

func (s *Site) delete(w http.ResponseWriter, r *http.Request) {
	v := &view{Title: "Delete your account"}
	if !s.guard(w, r, "delete", v) {
		return
	}
	v.Name = strings.TrimSpace(r.PostFormValue("name"))
	if r.PostFormValue("confirm") != "yes" {
		noteFor(w, "refused: not confirmed")
		v.Error, v.Fields, v.CSRF = "Tick the box to confirm.", map[string]string{"confirm": "Tick the box to confirm."}, s.csrfToken(w, r)
		s.render(w, r, http.StatusBadRequest, "delete", v)
		return
	}
	key := accountKey(v.Name)
	if !s.limitLogin(w, r, "delete", v, key) {
		return
	}
	res := accounts.DeleteAccount(s.cfg.Store, v.Name, r.PostFormValue("password"))
	s.loginDone(r, key, res)
	if res.Code != 0 {
		s.refused(w, r, statusOf(res), "delete", v, res)
		return
	}
	noteFor(w, "%s", res.Note)
	if s.cfg.EndSessions != nil && res.Account.UserID != 0 { // a game it is logged into ends now
		if n := s.cfg.EndSessions(res.Account.UserID, "its player deleted it on the website"); n > 0 {
			noteFor(w, "%s; its %d game session(s) end now", res.Note, n)
		}
	}
	s.render(w, r, http.StatusOK, "message", &view{Title: "Account deleted",
		Done: "Your account is deleted. Your login name, display name, password, settings, lists and items are gone; " +
			"your match statistics stay on the server without your name. If the game is logged in with it, it goes " +
			"back to the login screen now."})
}

// ---- download the own data ----

func (s *Site) dataForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "data", &view{Title: "Download your data", CSRF: s.csrfToken(w, r)})
}

func (s *Site) data(w http.ResponseWriter, r *http.Request) {
	v := &view{Title: "Download your data"}
	if !s.guard(w, r, "data", v) {
		return
	}
	v.Name = strings.TrimSpace(r.PostFormValue("name"))
	key := accountKey(v.Name)
	if !s.limitLogin(w, r, "data", v, key) {
		return
	}
	exp, res := accounts.ExportAccount(s.cfg.Store, v.Name, r.PostFormValue("password"), s.cfg.Now())
	s.loginDone(r, key, res)
	if res.Code != 0 {
		s.refused(w, r, statusOf(res), "data", v, res)
		return
	}
	exp.Server = "Dawngate restoration server " + s.cfg.Version
	body, err := json.MarshalIndent(exp, "", "  ")
	if err != nil {
		s.refused(w, r, http.StatusInternalServerError, "data", v, accounts.Result{Code: accounts.CodeSystem, Note: err.Error()})
		return
	}
	noteFor(w, "%s (%d bytes)", res.Note, len(body))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="dawngate-account-%d.json"`, exp.Account.UserID))
	w.Write(append(body, '\n'))
}
