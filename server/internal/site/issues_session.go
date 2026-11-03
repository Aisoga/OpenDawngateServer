package site

import (
	"crypto/rand"
	"net/http"
	"sync"
	"time"

	"dawngate/server/internal/store"
)

// The issue tracker's player session: a player logs in on /issues/login with the game account's login name and
// password (accounts.Authenticate, the argon2id check of a game login) and gets a random 32-byte token in the cookie
// PlayerCookie (__Host-, Secure, HttpOnly, SameSite=Lax, so a link to an issue from another site opens it logged in;
// every POST still needs the site's CSRF token, whose cookie is SameSite=Strict). The sessions are kept in memory
// (at most maxPlayerSessions, the least recently used goes first): one ends after PlayerIdle without a request, after
// PlayerMax, at a logout or a restart, and at the next request once the account isn't active any more (the store is
// read at every request: a ban, a disable or a deletion ends it at once).

// PlayerCookie is the issue tracker's session cookie.
const PlayerCookie = "__Host-dg_player"

// The player session's limits.
const (
	PlayerIdle        = 7 * 24 * time.Hour
	PlayerMax         = 30 * 24 * time.Hour
	maxPlayerSessions = 4096
)

type playerSession struct {
	userID        int64
	name          string // the login name (the store's, re-read at every request)
	persona       string // the display name other players see
	created, seen time.Time
}

type playerState struct {
	idle, max time.Duration
	cap       int
	mu        sync.Mutex
	sessions  map[string]*playerSession
}

func newPlayerState() *playerState {
	return &playerState{idle: PlayerIdle, max: PlayerMax, cap: maxPlayerSessions, sessions: map[string]*playerSession{}}
}

// playerSession returns the request's player session, checked against the store (an active account still); nil =
// none. A session whose account is gone, banned, disabled or deleted is dropped.
func (s *Site) playerSession(r *http.Request) *playerSession {
	c, err := r.Cookie(PlayerCookie)
	if err != nil || len(c.Value) < 32 || len(c.Value) > 64 {
		return nil
	}
	p := s.players
	now := s.cfg.Now()
	p.mu.Lock()
	sess := p.sessions[c.Value]
	if sess != nil && (now.Sub(sess.seen) > p.idle || now.Sub(sess.created) > p.max) {
		delete(p.sessions, c.Value)
		sess = nil
	}
	var cp playerSession
	if sess != nil {
		sess.seen = now
		cp = *sess
	}
	p.mu.Unlock()
	if sess == nil {
		return nil
	}
	cred, found, err := s.cfg.Store.CredentialsByID(cp.userID)
	if err != nil || !found || cred.Status != store.StatusActive {
		p.mu.Lock()
		delete(p.sessions, c.Value)
		p.mu.Unlock()
		return nil
	}
	cp.name = cred.Name
	return &cp
}

// newPlayerSession starts a session for the account and sets its cookie (Max-Age PlayerMax: the server ends it
// sooner when it idles).
func (s *Site) newPlayerSession(w http.ResponseWriter, cred store.Credentials) *playerSession {
	raw := make([]byte, 32)
	rand.Read(raw)
	token := b64.EncodeToString(raw)
	persona := ""
	if a, ok := s.cfg.Store.Account(cred.UserID); ok && len(a.Personas) > 0 {
		persona = a.Personas[0].Name
	}
	p := s.players
	now := s.cfg.Now()
	sess := &playerSession{userID: cred.UserID, name: cred.Name, persona: persona, created: now, seen: now}
	p.mu.Lock()
	for k, v := range p.sessions { // expired ones go; then the least recently used beyond the cap
		if now.Sub(v.seen) > p.idle || now.Sub(v.created) > p.max {
			delete(p.sessions, k)
		}
	}
	for len(p.sessions) >= p.cap {
		oldest := ""
		for k, v := range p.sessions {
			if oldest == "" || v.seen.Before(p.sessions[oldest].seen) {
				oldest = k
			}
		}
		delete(p.sessions, oldest)
	}
	p.sessions[token] = sess
	p.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: PlayerCookie, Value: token, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(p.max / time.Second)})
	cp := *sess
	return &cp
}

// endPlayerSession ends the request's session and clears its cookie.
func (s *Site) endPlayerSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(PlayerCookie); err == nil {
		s.players.mu.Lock()
		delete(s.players.sessions, c.Value)
		s.players.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: PlayerCookie, Value: "", Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
