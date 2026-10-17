package dgserver

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/limits"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/site"
	"dawngate/server/internal/sitelinks"
	"dawngate/server/internal/store"
)

// The website: HTTPS on --web-port (0 = none; Start-Server.ini's web_port, 443) with
// internal/site's pages, a Let's Encrypt certificate for --web-domain (TLS-ALPN-01 on that port: the router forwards
// TCP 443 to it) or else a self-signed one for the server's addresses, both kept in --web-dir (default <data>\site,
// which also holds the operator's privacy.html, terms.html and about.html). The plain HTTP port then redirects the
// client's login-screen links /dawngate/<page> there.
//
// Behind a reverse proxy (--web-proxy): plain HTTP on a loopback address (--web-bind, default 127.0.0.1) and
// --web-port, for a proxy such as nginx that holds the certificate of --web-domain and forwards
// https://<domain>/ there; no TLS, no ACME, no self-signed certificate. The site takes the players' addresses from
// the proxy's X-Forwarded-For/X-Real-IP (from a loopback peer only), and the limits apply per request to them.

// websiteConfig is the website's part of config.
type websiteConfig struct {
	port          int
	domain, email string
	dir, acmeURL  string
	proxy         bool   // --web-proxy
	bind          string // --web-bind: the proxy mode's loopback address ("" = DefaultWebProxyBind)
}

// DefaultWebProxyBind is where the website listens behind a reverse proxy unless --web-bind names another loopback
// address.
const DefaultWebProxyBind = "127.0.0.1"

// checkWebProxy checks the proxy mode's flags: --web-domain is needed (the links and redirects name it, the proxy
// serves it), --web-bind must be a loopback IPv4 address (the plain-HTTP site is the proxy's only: never on a network
// address), and the ACME flags have no use (the proxy holds the certificate). Without --web-proxy, --web-bind is
// refused. It fills in the default bind address.
func checkWebProxy(wc *websiteConfig) error {
	if !wc.proxy {
		if wc.bind != "" {
			return errors.New("--web-bind needs --web-proxy (the website otherwise listens on --bind)")
		}
		return nil
	}
	if wc.port == 0 {
		return nil // no website
	}
	if wc.domain == "" {
		return errors.New("--web-proxy needs --web-domain: the domain your reverse proxy serves (the links and " +
			"redirects point at https://<domain>/)")
	}
	if wc.email != "" || wc.acmeURL != "" {
		return errors.New("--web-email and --web-acme-url have no use with --web-proxy: your reverse proxy holds the " +
			"certificate")
	}
	if wc.bind == "" {
		wc.bind = DefaultWebProxyBind
	}
	if ip := net.ParseIP(wc.bind); ip == nil || ip.To4() == nil || !ip.IsLoopback() || strings.Contains(wc.bind, ":") {
		return fmt.Errorf("--web-bind %s: behind a reverse proxy the website listens only on a loopback IPv4 address "+
			"such as %s, never on a network address", wc.bind, DefaultWebProxyBind)
	}
	return nil
}

// checkWebDomain checks --web-domain: "" or a DNS name such as play.example.org (lower-cased): sitelinks.WebsiteDomain,
// the rule the setups' links and Start-Server.ini's web_domain follow too.
func checkWebDomain(d string) (string, error) {
	d, err := sitelinks.WebsiteDomain(d)
	if err != nil {
		return "", fmt.Errorf("--web-domain: %w", err)
	}
	return d, nil
}

// website is the running website.
type website struct {
	srv         *http.Server
	acme        *site.ACME
	url         string // https://<host>[:port]/
	fingerprint string // the self-signed certificate's (""with a domain)
	proxyAddr   string // behind a reverse proxy: where the site listens (<loopback address>:<port>), else ""
	note        string // the start-up line
}

// webURL is the website's address as players reach it.
func webURL(host string, port int) string {
	if port == 443 {
		return "https://" + host + "/"
	}
	return "https://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"
}

// logWriter is the http.Server's error log (TLS handshake errors and the like) into dgserver's log as "web" lines.
type logWriter struct{ l *logging.Log }

func (w logWriter) Write(p []byte) (int, error) {
	w.l.Log("web", strings.TrimSpace(string(p)))
	return len(p), nil
}

// startWebsite binds the website's port on cfg.bind and serves it. public is the announced address (an IP),
// publicName the name given as --public-host ("" for none); both go into a self-signed certificate.
//
// With dgserver's limits on (lim not nil) the website's listener admits connections by the per-address rules of
// every listener, and its password forms take the login backoff per address and account (site.Limiter).
//
// admin is the admin page's setup (nil = no /admin): its login takes limits.Admin()'s limiter, always on.
// endSessions ends an account's game sessions when the page disables, bans or deletes it.
func startWebsite(cfg config, lg *logging.Log, st *store.Store, lim *limits.Limiter, status func() site.Status,
	admin *site.AdminConfig, endSessions func(userID int64, why string) int, public, publicName string) (*website, error) {
	wc := cfg.web
	dir := wc.dir
	if dir == "" && cfg.dataDir != "" {
		dir = filepath.Join(cfg.dataDir, "site")
	}
	if wc.proxy {
		return startProxiedWebsite(cfg, lg, st, lim, status, admin, endSessions, dir)
	}
	ws := &website{}
	var tlsCfg *tls.Config
	var certNote string
	if wc.domain != "" {
		if dir == "" {
			return nil, errors.New("--web-domain needs --web-dir or --data (the certificate is kept there)")
		}
		ws.acme = &site.ACME{Domain: wc.domain, Email: wc.email, Dir: dir, DirectoryURL: wc.acmeURL,
			Log: func(format string, a ...any) { lg.Logf("web", format, a...) }}
		tlsCfg = ws.acme.TLSConfig()
		// players reach a domain's website on 443 whatever --web-port is (Let's Encrypt checks it there, the router
		// forwards 443 to --web-port), and the setups' links point at https://<domain>/<page> (sitelinks.Base)
		ws.url = webURL(wc.domain, 443)
		ca := wc.acmeURL
		if ca == "" {
			ca = "Let's Encrypt"
		}
		certNote = fmt.Sprintf("certificate for %s from %s by TLS-ALPN-01 (the CA connects to %s:443; cached in %s)", wc.domain, ca, wc.domain, dir)
	} else {
		hosts := []string{public, cfg.bind, "localhost"}
		if publicName != "" {
			hosts = append([]string{publicName}, hosts...)
		}
		cert, fp, fresh, err := site.SelfSigned(dir, hosts, time.Now())
		if err != nil {
			return nil, fmt.Errorf("the website's self-signed certificate: %w", err)
		}
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}}
		ws.url, ws.fingerprint = webURL(hosts[0], wc.port), fp
		made := "kept"
		if fresh {
			made = "made now"
		}
		where := "in memory only"
		if dir != "" {
			where = filepath.Join(dir, site.SelfSignedFile)
		}
		certNote = fmt.Sprintf("self-signed certificate for %s (%s, %s; browsers warn once: its SHA-256 fingerprint is %s)",
			strings.Join(cert.Leaf.DNSNames, ", ")+ipList(cert.Leaf.IPAddresses), made, where, fp)
	}
	sc := site.Config{Store: st, Policy: cfg.accounts, Status: status, Pages: dir, HSTS: wc.domain != "", Admin: admin,
		EndSessions: endSessions, Log: func(format string, a ...any) { lg.Logf("web", format, a...) }}
	if lim != nil { // not a nil *Limiter in the interface: the site skips the calls without limits
		sc.Limits = lim
	}
	h, err := site.New(sc)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort(cfg.bind, strconv.Itoa(wc.port)))
	if err != nil {
		return nil, fmt.Errorf("the website's port: %w", err)
	}
	ln = lim.Listener(ln, "web", func(f string, a ...any) { lg.Logf("web", f, a...) }) // the limits (nil: ln itself)
	ws.srv = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		ErrorLog: log.New(logWriter{lg}, "", 0),
		// no HTTP/2 (the site needs none) and the ACME challenge's protocol ends after its handshake
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){}}
	go ws.srv.Serve(tls.NewListener(ln, tlsCfg))
	if ws.acme != nil {
		ws.acme.Start()
	}
	pages := "the operator's pages (privacy.html, terms.html, about.html) from " + dir
	if dir == "" {
		pages = "no operator pages (no --data)"
	}
	ws.note = fmt.Sprintf("website (PH4) on %s (bound %s:%d): %s; %s; the HTTP port's /dawngate/<page> redirect there",
		ws.url, cfg.bind, ln.Addr().(*net.TCPAddr).Port, certNote, pages)
	if admin != nil {
		ws.note += "; " + describeAdmin(st, ws.url)
	}
	return ws, nil
}

// startProxiedWebsite is startWebsite behind a reverse proxy (--web-proxy; checkWebProxy has passed): plain HTTP on
// --web-bind:--web-port, https://<domain>/ the address everywhere. The listener isn't wrapped in the limits (every
// connection is the proxy's): the site asks the per-address connection rule per request with the player's address.
func startProxiedWebsite(cfg config, lg *logging.Log, st *store.Store, lim *limits.Limiter, status func() site.Status,
	admin *site.AdminConfig, endSessions func(userID int64, why string) int, dir string) (*website, error) {
	wc := cfg.web
	if err := checkWebProxy(&wc); err != nil { // again: never a plain-HTTP site on a network address
		return nil, err
	}
	ws := &website{url: webURL(wc.domain, 443)}
	sc := site.Config{Store: st, Policy: cfg.accounts, Status: status, Pages: dir, HSTS: true, Admin: admin,
		EndSessions: endSessions, Proxy: true, PublicURL: ws.url,
		Log: func(format string, a ...any) { lg.Logf("web", format, a...) }}
	if lim != nil { // not a nil *Limiter in the interfaces: the site skips the calls without limits
		sc.Limits, sc.Conns = lim, lim
	}
	h, err := site.New(sc)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort(wc.bind, strconv.Itoa(wc.port)))
	if err != nil {
		return nil, fmt.Errorf("the website's port: %w", err)
	}
	ws.proxyAddr = ln.Addr().String()
	ws.srv = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		ErrorLog: log.New(logWriter{lg}, "", 0)}
	go ws.srv.Serve(ln)
	pages := "the operator's pages (privacy.html, terms.html, about.html) from " + dir
	if dir == "" {
		pages = "no operator pages (no --data)"
	}
	limitsNote := "limits off"
	if lim != nil {
		limitsNote = "the limits per request by the player's address"
	}
	ws.note = fmt.Sprintf("website on %s behind a reverse proxy (--web-proxy): plain HTTP on %s, no certificate of its "+
		"own (the proxy holds %s's); the players' addresses from X-Forwarded-For/X-Real-IP of a loopback peer only, %s; "+
		"%s; the HTTP port's /dawngate/<page> redirect there", ws.url, ws.proxyAddr, wc.domain, limitsNote, pages)
	if admin != nil {
		ws.note += "; " + describeAdmin(st, ws.url)
	}
	return ws, nil
}

// describeAdmin is the admin page's part of the website's start-up line.
func describeAdmin(st *store.Store, url string) string {
	admins, err := st.Admins()
	who := ""
	switch {
	case err != nil:
		who = "admins unknown (" + err.Error() + ")"
	case len(admins) == 0:
		who = "no admin yet (--make-admin <login name>)"
	default:
		var names []string
		for _, a := range admins {
			names = append(names, a.Name)
		}
		who = fmt.Sprintf("%d admin(s): %s", len(admins), strings.Join(names, ", "))
	}
	return fmt.Sprintf("admin page (PH5) at %sadmin, %s; its login: %s", url, who, limits.Admin().DescribeLogins())
}

// websiteAdmin is the admin page's view of the running matches (the hosted games) and their end.
func websiteAdmin(bz *blaze.Server) *site.AdminConfig {
	return &site.AdminConfig{
		Matches: func() []site.Match {
			var out []site.Match
			for _, m := range bz.HostedMatches() {
				sm := site.Match{GameID: m.GameID, Name: m.Name, Mode: m.Mode, State: m.State, Started: m.Started, Host: m.Host,
					Pid: m.Pid}
				for _, p := range m.Players {
					sm.Players = append(sm.Players, site.MatchPlayer{Name: p.Name, UserID: p.UserID, PersonaID: p.PersonaID,
						Team: p.Team, Joined: p.Joined, Left: p.Left})
				}
				out = append(out, sm)
			}
			return out
		},
		EndMatch: bz.EndHostedMatch,
	}
}

// websiteStatus is the status page's live numbers: the users logged in and the matches with a game (warm hosts that
// wait for one don't count). No names.
func websiteStatus(bz *blaze.Server, hosts *gmshost.Manager) func() site.Status {
	return func() site.Status {
		s := site.Status{PlayersOnline: bz.OnlineUsers()}
		if hosts != nil {
			idle, starting := hosts.Warm()
			s.MatchesRunning = max(hosts.Running()-idle-starting, 0)
		}
		return s
	}
}

func ipList(ips []net.IP) string {
	var s []string
	for _, ip := range ips {
		s = append(s, ip.String())
	}
	if len(s) == 0 {
		return ""
	}
	return ", " + strings.Join(s, ", ")
}

// redirects are the plain HTTP port's redirects of the client's login-screen links to the website's pages.
func (ws *website) redirects() map[string]string {
	m := map[string]string{}
	for _, p := range sitelinks.Pages {
		m["/"+sitelinks.FallbackPrefix+p] = ws.url + p
	}
	return m
}

func (ws *website) close() {
	if ws.acme != nil {
		ws.acme.Close()
	}
	ws.srv.Close()
}
