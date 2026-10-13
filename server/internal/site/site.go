// Package site is the server's website: registration in the server's
// sign-up mode (the rules and invite codes), the player's self-service (change the password, delete the account,
// download the own data as JSON), the public status page (players online, matches running, the server's version; no
// names), the forgotten-password help (the operator resets passwords: the server never sends mail) and the
// operator's privacy and terms pages. dgserver serves it over HTTPS (--web-port; a Let's Encrypt certificate for
// --web-domain by TLS-ALPN-01 (acme.go), else a self-signed one (selfsigned.go)).
//
// Server-rendered html/template pages, no script. Every form carries a CSRF token bound to a cookie (csrf.go), every
// POST passes a per-address rate (always on) and, when dgserver's limits are on (internal/limits), the same
// per-address and per-account login backoff as the game's login for the forms that check a password. The site keeps
// no login session: each self-service form names the account and is confirmed by its password. The request log has
// one line per request (address, method, path, status, what happened) and never a request body: the plain HTTP
// logger (internal/web) hexdumps bodies, so the forms never go through it.
//
// The page names are the client's login-screen links' (internal/sitelinks): / (start), /signup, /password
// (forgotten password), /terms, /privacy; dgserver's plain HTTP port redirects /dawngate/<page> to them.
package site

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/sitelinks"
	"dawngate/server/internal/store"
)

// Limiter is the hook for dgserver's limits (*limits.Limiter has these methods; a nil one allows everything).
// Login is asked before a form checks a password (account = the login name lower-cased, "" for a registration, which
// counts only against the address's rate) and LoginDone told the result, so the website can't be used to guess
// passwords past the game's login backoff.
type Limiter interface {
	Login(addr, account string) (wait time.Duration, why string)
	LoginDone(addr, account string, ok bool)
}

// Status is what the public status page shows: no names.
type Status struct {
	PlayersOnline  int
	MatchesRunning int
}

// Config is a site's setup.
type Config struct {
	Store  *store.Store
	Policy accounts.Policy
	// Limits is dgserver's limiter (nil = limits off). FormRate is the site's own rate of POSTs per address,
	// always on (zero = DefaultFormRate).
	Limits   Limiter
	FormRate Rate
	// Status returns the live numbers (nil = none shown); Version is the server's version (Version()).
	Status  func() Status
	Version string
	// Pages is the operator's folder of page texts (privacy.html, terms.html, about.html: HTML fragments put into
	// the page as they are; "" or a missing file = the default text).
	Pages string
	// HSTS sends Strict-Transport-Security (a real certificate: --web-domain).
	HSTS bool
	// Admin is the admin page under /admin (admin.go); nil = none. Policy.Live is the sign-up mode it changes.
	Admin *AdminConfig
	// EndSessions ends the account's running game sessions now (blaze.Server.EndAccountSessions) and returns how
	// many there were; why goes to the server's log. The admin page calls it when it disables, bans or deletes an
	// account, the self-service when a player deletes its own. nil = none (the next login is refused all the same).
	EndSessions func(userID int64, why string) int
	Log         func(format string, a ...any) // one line per request (nil = none)
	Now         func() time.Time
}

// Rate is a token bucket: PerMinute tokens a minute, at most Burst saved up.
type Rate struct {
	PerMinute float64
	Burst     float64
}

// DefaultFormRate is the POSTs one address may send: 10 at once, then one every 6 s. A player needs a handful; each
// POST may run an argon2id check (19 MiB, about 30 ms).
var DefaultFormRate = Rate{PerMinute: 10, Burst: 10}

// Site is the website's handler.
type Site struct {
	cfg     Config
	mux     *http.ServeMux
	key     []byte // the CSRF tokens' HMAC key (new at every start: an open form is then refused once, then works)
	pages   map[string]*template.Template
	forms   *buckets
	started time.Time
	admin   *adminState // nil without Config.Admin
}

//go:embed templates/*.html static/site.css
var files embed.FS

// The page names are the client's login-screen links' (the internal/sitelinks, the one list of them: the setups
// write the links, dgserver's HTTP port serves its fallback pages and redirects there).
const (
	PageIndex    = sitelinks.Index
	PageSignup   = sitelinks.Signup
	PagePassword = sitelinks.Password
	PageTerms    = sitelinks.Terms
	PagePrivacy  = sitelinks.Privacy
)

// Pages are the client's link targets, in sitelinks' order.
var Pages = sitelinks.Pages

// New makes the site.
func New(cfg Config) (*Site, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("site: no account store")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.FormRate.PerMinute <= 0 {
		cfg.FormRate = DefaultFormRate
	}
	if cfg.Version == "" {
		cfg.Version = Version()
	}
	s := &Site{cfg: cfg, key: make([]byte, 32), pages: map[string]*template.Template{}, forms: newBuckets(cfg.FormRate),
		started: cfg.Now()}
	if _, err := rand.Read(s.key); err != nil {
		return nil, err
	}
	base, err := template.New("layout.html").Funcs(template.FuncMap{"upper": strings.ToUpper}).
		ParseFS(files, "templates/layout.html")
	if err != nil {
		return nil, err
	}
	names, err := fs.Glob(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		page := strings.TrimSuffix(filepath.Base(n), ".html")
		if page == "layout" {
			continue
		}
		t, err := template.Must(base.Clone()).ParseFS(files, n)
		if err != nil {
			return nil, err
		}
		s.pages[page] = t
	}
	css, err := files.ReadFile("static/site.css")
	if err != nil {
		return nil, err
	}
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", s.index)
	m.HandleFunc("GET /"+PageSignup, s.signupForm)
	m.HandleFunc("POST /"+PageSignup, s.signup)
	m.HandleFunc("GET /"+PagePassword, s.passwordHelp)
	m.HandleFunc("GET /account/password", s.changeForm)
	m.HandleFunc("POST /account/password", s.change)
	m.HandleFunc("GET /account/delete", s.deleteForm)
	m.HandleFunc("POST /account/delete", s.delete)
	m.HandleFunc("GET /account/data", s.dataForm)
	m.HandleFunc("POST /account/data", s.data)
	m.HandleFunc("GET /status", s.status)
	m.HandleFunc("GET /status.json", s.statusJSON)
	m.HandleFunc("GET /"+PagePrivacy, s.operatorPage(PagePrivacy, "Privacy"))
	m.HandleFunc("GET /"+PageTerms, s.operatorPage(PageTerms, "Terms of use"))
	if cfg.Admin != nil {
		s.admin = newAdminState(*cfg.Admin, cfg.Now)
		s.adminRoutes(m)
	}
	m.HandleFunc("GET /site.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(css)
	})
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.render(w, r, http.StatusNotFound, "message", &view{Title: "Not found", Text: "There is no page here."})
	})
	s.mux = m
	return s, nil
}

// maxBody bounds a form's body (the fields are short; a longer body is refused before it is read).
const maxBody = 8 << 10

// recorder keeps the status and the note for the request's log line.
type recorder struct {
	http.ResponseWriter
	status int
	note   string
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// ServeHTTP answers one request: the security headers, the body limit, the page and the log line.
func (s *Site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; "+
		"frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	if s.cfg.HSTS {
		h.Set("Strict-Transport-Security", "max-age=31536000")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	rec := &recorder{ResponseWriter: w}
	s.mux.ServeHTTP(rec, r)
	if s.cfg.Log != nil {
		note := ""
		if rec.note != "" {
			note = ": " + rec.note
		}
		s.cfg.Log("%s %s %s -> %d%s", clientAddr(r), r.Method, r.URL.Path, max(rec.status, 200), note)
	}
}

// noteFor puts a note into the request's log line.
// endSessions ends the account's game sessions (Config.EndSessions) and says so for a page.
func (s *Site) endSessions(userID int64, why string) string {
	if s.cfg.EndSessions == nil {
		return ""
	}
	switch n := s.cfg.EndSessions(userID, why); n {
	case 0:
		return " It wasn't logged into the game."
	case 1:
		return " Its game session ends now (the player goes back to the login screen; a match it was in loses it)."
	default:
		return fmt.Sprintf(" Its %d game sessions end now (the player goes back to the login screen; a match it was in "+
			"loses it).", n)
	}
}

func noteFor(w http.ResponseWriter, format string, a ...any) {
	if rec, ok := w.(*recorder); ok {
		rec.note = fmt.Sprintf(format, a...)
	}
}

// clientAddr is the request's source address (its IP; "" if unknown).
func clientAddr(r *http.Request) string {
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return ap.Addr().Unmap().String()
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

func sourceAddr(r *http.Request) netip.Addr {
	a, _ := netip.ParseAddr(clientAddr(r))
	return a
}

// view is every page's data.
type view struct {
	Title   string
	Text    string // a paragraph (plain text)
	Error   string // the form's error line
	Done    string // the success line
	CSRF    string
	Mode    string // the sign-up mode
	Name    string // the form's login name (kept after an error; never a password)
	Display string
	Fields  map[string]string // field -> its error
	Rules   rules
	Status  *statusView
	HTML    template.HTML // the operator's page text
	Version string
	Pages   string     // where the operator's texts go
	Admin   *adminView // the admin pages' data (nil on the player's pages)
}

type rules struct{ MinName, MaxName, MinDisplay, MaxDisplay, MinPassword, MaxPassword int }

var theRules = rules{accounts.MinName, accounts.MaxName, accounts.MinDisplay, accounts.MaxDisplay, accounts.MinPassword,
	accounts.MaxPassword}

func (s *Site) render(w http.ResponseWriter, r *http.Request, status int, page string, v *view) {
	t := s.pages[page]
	if t == nil {
		http.Error(w, "no page "+page, http.StatusInternalServerError)
		return
	}
	v.Version, v.Mode, v.Rules = s.cfg.Version, s.cfg.Policy.CurrentMode().String(), theRules
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "layout.html", v); err != nil && s.cfg.Log != nil {
		s.cfg.Log("%s %s: template: %v", clientAddr(r), r.URL.Path, err)
	}
}

func (s *Site) index(w http.ResponseWriter, r *http.Request) {
	v := &view{Title: "Dawngate community server", HTML: s.operatorText("about")}
	if st := s.statusNow(); st != nil {
		v.Status = st
	}
	s.render(w, r, http.StatusOK, "index", v)
}

func (s *Site) passwordHelp(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "password", &view{Title: "Forgotten password"})
}

// operatorText reads the operator's page text <Pages>\<name>.html ("" when there is none).
func (s *Site) operatorText(name string) template.HTML {
	if s.cfg.Pages == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(s.cfg.Pages, name+".html"))
	if err != nil || len(b) > 256<<10 {
		return ""
	}
	return template.HTML(b) // the operator's own text, trusted as the operator's
}

func (s *Site) operatorPage(name, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := &view{Title: title, HTML: s.operatorText(name), Pages: s.cfg.Pages}
		s.render(w, r, http.StatusOK, name, v)
	}
}

// ---- the status page ----

type statusView struct {
	Online, Matches int
	Live            bool
	Since           string
	Mode            string
}

func (s *Site) statusNow() *statusView {
	v := &statusView{Since: s.started.UTC().Format("2006-01-02 15:04 UTC"), Mode: s.cfg.Policy.CurrentMode().String()}
	if s.cfg.Status != nil {
		st := s.cfg.Status()
		v.Online, v.Matches, v.Live = st.PlayersOnline, st.MatchesRunning, true
	}
	return v
}

func (s *Site) status(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "status", &view{Title: "Server status", Status: s.statusNow()})
}

// StatusJSON is /status.json's answer.
type StatusJSON struct {
	PlayersOnline  int    `json:"playersOnline"`
	MatchesRunning int    `json:"matchesRunning"`
	Version        string `json:"version"`
	Signup         string `json:"signup"`
	Since          string `json:"since"`
}

func (s *Site) statusJSON(w http.ResponseWriter, r *http.Request) {
	st := s.statusNow()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(StatusJSON{PlayersOnline: st.Online, MatchesRunning: st.Matches, Version: s.cfg.Version,
		Signup: st.Mode, Since: s.started.UTC().Format(time.RFC3339)})
}

// ---- the per-address form rate ----

type bucket struct {
	tokens float64
	last   time.Time
}

type buckets struct {
	rate Rate
	mu   sync.Mutex
	m    map[string]*bucket
	tick time.Time
}

func newBuckets(r Rate) *buckets { return &buckets{rate: r, m: map[string]*bucket{}} }

// take takes a token for addr at now: 0 = allowed, else how long until the next one.
func (b *buckets) take(addr string, now time.Time) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	per := b.rate.PerMinute / 60
	if now.Sub(b.tick) > time.Minute || len(b.m) > 10000 { // forget full buckets
		b.tick = now
		for k, v := range b.m {
			if v.tokens+now.Sub(v.last).Seconds()*per >= b.rate.Burst {
				delete(b.m, k)
			}
		}
	}
	v := b.m[addr]
	if v == nil {
		v = &bucket{tokens: b.rate.Burst, last: now}
		b.m[addr] = v
	}
	v.tokens = min(b.rate.Burst, v.tokens+now.Sub(v.last).Seconds()*per)
	v.last = now
	if v.tokens >= 1 {
		v.tokens--
		return 0
	}
	return time.Duration((1 - v.tokens) / per * float64(time.Second))
}
