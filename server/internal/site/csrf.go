package site

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
)

// CSRF protection (the double-submit pattern with a signed token): the first form page a browser gets sets a random
// cookie (CSRFCookie: __Host- prefix, so Secure, Path=/ and no Domain are enforced by the browser; HttpOnly,
// SameSite=Strict), and every form carries HMAC-SHA256(the site's key, that cookie) in its hidden field. A POST is
// taken only with both, matching, and when the browser's Sec-Fetch-Site and Origin headers (where sent) name this
// site. A page of another site can neither read the cookie nor forge the field; the key is new at every start, so a
// form opened before a restart is refused once ("reload and try again").

// CSRFCookie is the cookie's name; CSRFField the form field's.
const (
	CSRFCookie = "__Host-dg_csrf"
	CSRFField  = "csrf"
)

var b64 = base64.RawURLEncoding

func (s *Site) tokenFor(cookie string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("dawngate-site-form\x00"))
	m.Write([]byte(cookie))
	return b64.EncodeToString(m.Sum(nil))
}

// csrfToken returns the token for the request's form, setting the cookie when the browser has none.
func (s *Site) csrfToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(CSRFCookie); err == nil && len(c.Value) >= 32 && len(c.Value) <= 64 {
		return s.tokenFor(c.Value)
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	v := b64.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{Name: CSRFCookie, Value: v, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode})
	return s.tokenFor(v)
}

// csrfOK checks a POST: the cookie, the field and the browser's origin headers. why is the log's reason.
func (s *Site) csrfOK(r *http.Request) (ok bool, why string) {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false, "Sec-Fetch-Site " + r.Header.Get("Sec-Fetch-Site")
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false, "Origin " + o
		}
	}
	c, err := r.Cookie(CSRFCookie)
	if err != nil {
		return false, "no CSRF cookie"
	}
	want := s.tokenFor(c.Value)
	got := r.PostFormValue(CSRFField)
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return false, "CSRF token mismatch"
	}
	return true, ""
}
