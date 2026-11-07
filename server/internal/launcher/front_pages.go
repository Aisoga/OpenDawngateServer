package launcher

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/setup/localepatch"
	"dawngate/server/internal/sitelinks"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/web"
)

// The login screen's links on the front. A player copy for the tunnel opens its Sign up now!, Forgot Password? and
// legal links at http://<tunnel address>:18080/dawngate/<page> (sitelinks), which is the front's HTTP port, and the
// server picker opens each server's own Sign up now! and Forgot Password? at /dawngate/pick/<slot>/<page> (the URL
// table's line of the server's slot, through the game's Global.OpenLocalizedURL; localepatch.LinkSlots). The front
// answers both itself, before any login and for any server of the list:
//
//   - /dawngate/pick/<slot>/<page>: that server's page. The current session's server is asked through the session; any
//     other through a short tunnel session of its own, opened for the request and closed after it (no game login).
//     Its key must be trusted: a new key is shown with its fingerprint and trusted only when the player confirms it
//     on the page (a form with this run's token, from this page only), as the login's confirm once; a changed key is
//     refused. This PC without passwords gets the front's own page: no sign-up needed.
//   - /dawngate/signup and /dawngate/password: a page with the player's servers (their sign-up modes, This PC's
//     no-password note) whose links lead to the pick pages: the plain links can't tell which server the player means.
//   - the start page and the legal pages: the current session's server; with none, the same list.
//
// A request passes to a server with "Connection: close" and the path /dawngate/<page>, as a copy without the picker
// asks it; the server answers with its fallback page, or with a redirect to its website, which the browser follows.

// isPagePath reports whether an HTTP request line asks for one of the login screen's pages.
func isPagePath(line []byte) bool {
	f := strings.Fields(string(line))
	if len(f) < 2 {
		return false
	}
	rest, ok := strings.CutPrefix(pathOf(f[1]), "/"+sitelinks.FallbackPrefix)
	if !ok {
		return false
	}
	return strings.HasPrefix(rest, "pick/") || slices.Contains(sitelinks.Pages, rest)
}

// pathOf is a request target's path (without its query or fragment).
func pathOf(target string) string {
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		return target[:i]
	}
	return target
}

// parsePick reads pick/<slot>/<page>.
func parsePick(rest string) (slot int, page string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(rest, "pick/"), "/")
	if len(parts) != 2 {
		return 0, "", false
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 0 || n >= localepatch.LinkSlots || !slices.Contains(sitelinks.Pages, parts[1]) {
		return 0, "", false
	}
	return n, parts[1], true
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// slotKey keys a server's slot: This PC by its tag (its address follows who serves it), the others by address.
func slotKey(b Bookmark) string {
	if b.Tag == ThisPCTag {
		return ThisPCTag
	}
	return strings.ToLower(b.Address)
}

// slotOf is the link slot of a server of the list (-1: none left): assigned at the server's first list line and kept
// for the run, so a link the picker read earlier never leads to another server.
func (f *Front) slotOf(tag string) int {
	b, ok := f.Bookmarks.Find(tag)
	if !ok {
		return -1
	}
	key := slotKey(b)
	f.pageMu.Lock()
	defer f.pageMu.Unlock()
	if f.slots == nil {
		f.slots = map[string]int{}
	}
	if s, ok := f.slots[key]; ok {
		return s
	}
	if len(f.slotTags) >= localepatch.LinkSlots {
		return -1
	}
	f.slots[key] = len(f.slotTags)
	f.slotTags = append(f.slotTags, key)
	return len(f.slotTags) - 1
}

// slotServer is the server of a link slot (false: none, or it left the list).
func (f *Front) slotServer(slot int) (Bookmark, bool) {
	f.pageMu.Lock()
	key := ""
	if slot >= 0 && slot < len(f.slotTags) {
		key = f.slotTags[slot]
	}
	f.pageMu.Unlock()
	if key == "" {
		return Bookmark{}, false
	}
	if key == ThisPCTag {
		return f.Bookmarks.ThisPC()
	}
	for _, b := range f.Bookmarks.List() {
		if strings.ToLower(b.Address) == key && b.Tag != ThisPCTag {
			return b, true
		}
	}
	return Bookmark{}, false
}

// keepStatuses keeps the statuses of the list written last, for the pages' list of servers.
func (f *Front) keepStatuses(sts []serverStatus) {
	f.pageMu.Lock()
	f.lastSts = sts
	f.pageMu.Unlock()
}

func (f *Front) pageStatuses() []serverStatus {
	f.pageMu.Lock()
	sts := f.lastSts
	f.pageMu.Unlock()
	if sts != nil {
		return sts
	}
	var out []serverStatus
	for _, b := range f.Bookmarks.List() {
		out = append(out, serverStatus{Bookmark: b, Key: "unknown"})
	}
	return out
}

// pageHost is the Host a page request must name: the front's HTTP address (a page of another name, e.g. by DNS
// rebinding, is refused).
func (f *Front) pageHost() string {
	return net.JoinHostPort(f.TunnelIP.String(), strconv.Itoa(f.Ports.HTTP))
}

// samePage reports whether a request comes from the front's own pages or the game's link: its Host, and its Origin and
// Sec-Fetch-Site where the browser sends them. A link from the game opens with Sec-Fetch-Site none. Another site's request
// is refused for a GET too: a GET of a server's page starts a probe and a tunnel session, which count against the player's
// limits at that server.
func (f *Front) samePage(r *http.Request, post bool) bool {
	if !strings.EqualFold(r.Host, f.pageHost()) {
		return false
	}
	site := r.Header.Get("Sec-Fetch-Site")
	switch site {
	case "", "same-origin", "none":
	default:
		return false
	}
	if r.Header.Get("Service-Worker") != "" { // a worker would outlive the page and stand between the browser and every page here
		return false
	}
	if !post {
		return true
	}
	// The pages send Referrer-Policy same-origin, so a browser names the page's origin; under no-referrer Chrome and
	// Edge sent "Origin: null" with the form (seen live), which counts only beside Sec-Fetch-Site same-origin.
	if o := r.Header.Get("Origin"); o != "" && !strings.EqualFold(o, "http://"+f.pageHost()) && !(o == "null" && site == "same-origin") {
		return false
	}
	return true
}

// serveBrowser answers a program other than the game on the HTTP port, outside the login screen's pages. The one thing it
// gets is a person's click on a server page's Download link (the same site, GET); everything else is refused here, before
// any stream to the server.
func (f *Front) serveBrowser(c net.Conn, br *bufio.Reader, who string) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	c.SetReadDeadline(time.Time{})
	path := pathOf(req.RequestURI)
	if s := f.current(); s != nil && s.c.Up() && isDownloadPath(path) && (req.Method == http.MethodGet || req.Method == http.MethodHead) &&
		strings.EqualFold(req.Host, f.pageHost()) && req.Header.Get("Sec-Fetch-Site") == "same-origin" {
		c.SetWriteDeadline(time.Now().Add(downloadLimit))
		f.forward(c, req, s.b, path, downloadLimit, f.sessionStream(s, c))
		return
	}
	f.Guard.logRefused(f.Ports.HTTP, who)
	writePage(c, http.StatusForbidden, "Refused", "<p>This port of Start-Client on this PC serves the game. Its pages open from the game's login screen.</p>")
}

// downloadLimit bounds a client package's download through the front.
const downloadLimit = 30 * time.Minute

// isDownloadPath: the server's client package and its page (web.Download), and nothing that could walk away from them.
func isDownloadPath(path string) bool {
	root := "/" + web.DownloadPagePath
	return (path == root || strings.HasPrefix(path, root+"/")) && !strings.Contains(path, "..") && !strings.Contains(path, "%")
}

// servePage answers one request for the login screen's pages.
func (f *Front) servePage(c net.Conn, br *bufio.Reader) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	c.SetReadDeadline(time.Time{})
	c.SetWriteDeadline(time.Now().Add(2 * time.Minute))
	rest := strings.TrimPrefix(pathOf(req.RequestURI), "/"+sitelinks.FallbackPrefix)
	if !f.samePage(req, req.Method == http.MethodPost) {
		// a web page can ask hundreds of times a second, so the log holds only the first ones
		if n := f.pagesRefused.Add(1); n <= maxGuardLines {
			f.logf("page %s %q from %s refused: Host %q, Origin %q, Sec-Fetch-Site %q", req.Method, req.RequestURI, c.RemoteAddr(), req.Host,
				req.Header.Get("Origin"), req.Header.Get("Sec-Fetch-Site"))
		}
		writePage(c, http.StatusForbidden, "Refused", "<p>This page belongs to Start-Client on this PC and opens only from the game's login screen.</p>")
		return
	}
	// one line per request, so the log tells how often a link opened a page (a browser sends Sec-Fetch-Site; the
	// User-Agent's last word tells browsers apart)
	agent := strings.Fields(req.UserAgent())
	last := ""
	if len(agent) > 0 {
		last = agent[len(agent)-1]
	}
	f.logf("page request %s %s from %s (Sec-Fetch-Site %q, agent %q)", req.Method, req.RequestURI, c.RemoteAddr(),
		req.Header.Get("Sec-Fetch-Site"), last)
	if strings.HasPrefix(rest, "pick/") {
		slot, page, ok := parsePick(rest)
		if !ok {
			writePage(c, http.StatusNotFound, "Not found", "<p>No such page.</p>")
			return
		}
		b, ok := f.slotServer(slot)
		if !ok {
			f.logf("page %s: slot %d names no server of the list now", req.RequestURI, slot)
			f.writeChooser(c, page, "That server is no longer in your list.")
			return
		}
		f.pickPage(c, req, b, slot, page)
		return
	}
	page := rest
	s := f.current()
	if page == sitelinks.Signup || page == sitelinks.Password || s == nil || !s.c.Up() {
		f.writeChooser(c, page, "")
		return
	}
	f.forwardPage(c, req, s.b, page, f.sessionStream(s, c))
}

// pickPage answers a server's own page: through the current session, or a short session of its own once its key is
// trusted (a new one confirmed on the page).
func (f *Front) pickPage(c net.Conn, req *http.Request, b Bookmark, slot int, page string) {
	name := html.EscapeString(displayName(b))
	if b.Tag == ThisPCTag && f.ThisPC != nil {
		st, _ := f.ThisPC.status()
		mode := f.ThisPC.signupMode()
		if mode == signupLocal {
			f.logf("page %s: This PC without passwords (the front's own page)", req.RequestURI)
			writePage(c, http.StatusOK, "This PC: no sign-up needed", thisPCLocalPage(page))
			return
		}
		if st != "" {
			writePage(c, http.StatusOK, "This PC", thisPCWaitingPage(st, mode))
			return
		}
		if tb, ok := f.Bookmarks.ThisPC(); ok {
			b = tb
		}
	}
	if req.Method == http.MethodPost {
		f.confirmKey(c, req, b, slot, page)
		return
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		writePage(c, http.StatusMethodNotAllowed, "Not allowed", "<p>Only links open these pages.</p>")
		return
	}
	if s := f.current(); s != nil && s.b.Tag == b.Tag && s.b.Gateway() == b.Gateway() && s.c.Up() {
		f.forwardPage(c, req, b, page, f.sessionStream(s, c))
		return
	}
	info, err := tunnel.ProbeInfo(b.Gateway(), f.ConnectTimeout, f.dialerFor(b.Address))
	if err != nil {
		p := f.classifyDial(b, err, f.ConnectTimeout)
		f.logf("page %s for %s: %v", req.RequestURI, b.Tag, p)
		if p.code == FrontErrNoGateway {
			writePage(c, http.StatusBadGateway, "No Dawngate server there", fmt.Sprintf("<p>Something answers at the address of %s (%s), but no Dawngate server with the tunnel on. Ask its operator.</p>", name, html.EscapeString(b.Address)))
		} else {
			writePage(c, http.StatusBadGateway, "No answer", fmt.Sprintf("<p>No answer from %s (%s). It may be offline, or its address may be wrong. Try again later.</p>", name, html.EscapeString(b.Address)))
		}
		return
	}
	switch state, known := f.pinState(b, info.Pin); state {
	case "changed":
		f.logf("page %s for %s: the key changed (%s, trusted %s): refused", req.RequestURI, b.Tag, info.Pin.Fingerprint(), known.Fingerprint())
		writePage(c, http.StatusForbidden, "KEY CHANGED", fmt.Sprintf("<p><b>%s now shows another key</b> than the one you trusted: it shows "+
			"<code>%s</code>, you trusted <code>%s</code>. Don't continue until its operator confirms the new key: see the troubleshooting "+
			"guide, <i>The server's key has changed</i>.</p>", name, info.Pin.Fingerprint(), known.Fingerprint()))
		return
	case "new":
		f.logf("page %s for %s: its key %s isn't trusted yet: shown for confirmation", req.RequestURI, b.Tag, info.Pin.Fingerprint())
		writePage(c, http.StatusOK, "New server key", fmt.Sprintf(`<p>%s (%s) shows the key <code>%s</code>.</p>
<p>Compare it with the fingerprint its operator gives you. If they are the same, trust it: your Start-Client then trusts
this server for your logins too. If they differ, close this page.</p>
<form method="post" action="%s"><input type="hidden" name="token" value="%s"><input type="hidden" name="key" value="%s">
<button type="submit">Trust %s and open the page</button></form>`, name, html.EscapeString(b.Address), info.Pin.Fingerprint(),
			html.EscapeString(pickPath(slot, page)), f.pageToken, info.Pin.String(), info.Pin.Fingerprint()))
		return
	}
	f.forwardPage(c, req, b, page, func() (net.Conn, func(), error) {
		tc := &tunnel.Client{Log: f.Log, Gateway: b.Gateway(), Pin: info.Pin, TunnelIP: f.TunnelIP, External: true, DialTimeout: f.ConnectTimeout,
			Release: f.releaseFor(b, info.Version), Maps: f.installedMaps()}
		if err := tc.Start(); err != nil {
			return nil, nil, err
		}
		up, err := tc.OpenStream(f.Ports.HTTP, tcpAddr(c.RemoteAddr()))
		if err != nil {
			tc.Close()
			return nil, nil, err
		}
		return up, tc.Close, nil
	})
}

func pickPath(slot int, page string) string {
	return "/" + localepatch.SlotPath(slot, page)
}

// confirmKey trusts a server's new key that the player confirmed on the page (the form's token and the key shown),
// then opens the page.
func (f *Front) confirmKey(c net.Conn, req *http.Request, b Bookmark, slot int, page string) {
	req.Body = http.MaxBytesReader(nil, req.Body, 4096)
	if err := req.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(req.PostForm.Get("token")), []byte(f.pageToken)) != 1 {
		f.logf("page %s: a confirmation without this run's token refused", req.RequestURI)
		writePage(c, http.StatusForbidden, "Refused", "<p>That confirmation didn't come from this page. Open the server's link in the game again.</p>")
		return
	}
	pin, err := tunnel.ParsePin(req.PostForm.Get("key"))
	if err != nil {
		writePage(c, http.StatusBadRequest, "Refused", "<p>No key to trust.</p>")
		return
	}
	info, err := tunnel.ProbeInfo(b.Gateway(), f.ConnectTimeout, f.dialerFor(b.Address))
	if err != nil || !tunnel.SamePin(info.Pin, pin) {
		f.logf("page %s: %s's key isn't the one confirmed any more (%v)", req.RequestURI, b.Tag, err)
		writePage(c, http.StatusConflict, "The key changed", "<p>The server doesn't show the key you confirmed any more. Open its link in the game again.</p>")
		return
	}
	if state, _ := f.pinState(b, pin); state == "changed" {
		writePage(c, http.StatusForbidden, "KEY CHANGED", "<p>You trusted another key for this server before: it can't be replaced here. See the troubleshooting guide, <i>The server's key has changed</i>.</p>")
		return
	} else if state == "new" {
		if _, err := TrustGateway(b.Address, pin, TrustOptions{Store: TrustedGateway(f.Layout, b.Address), Accept: true}); err != nil {
			writePage(c, http.StatusInternalServerError, "Not stored", "<p>The key couldn't be stored: "+html.EscapeString(err.Error())+"</p>")
			return
		}
		f.keyMu.Lock()
		delete(f.shown, strings.ToLower(b.Address))
		f.keyMu.Unlock()
		f.logf("trusting %s's tunnel key %s from now on (confirmed on its sign-up page; %s)", b.Address, pin.Fingerprint(),
			TrustedGateway(f.Layout, b.Address))
		f.wantList()
	}
	fmt.Fprintf(c, "HTTP/1.1 303 See Other\r\nLocation: %s\r\nContent-Length: 0\r\nCache-Control: no-store\r\nConnection: close\r\n\r\n", pickPath(slot, page))
}

// sessionStream opens a stream to the current session's HTTP port.
func (f *Front) sessionStream(s *frontSession, c net.Conn) func() (net.Conn, func(), error) {
	return func() (net.Conn, func(), error) {
		up, err := s.c.OpenStream(s.serverPort(f, f.Ports.HTTP), tcpAddr(c.RemoteAddr()))
		return up, func() {}, err
	}
}

// A server's pages open in the player's browser under the front's address, which they share with the front's own pages
// and, for cookies, with every other program on 127.0.0.1. So a page may look however its server likes, but it runs no
// script and sets nothing in the browser, and a request carries only what describes the page wanted. Without this a
// hostile server's page could read the confirm form's token and the list of the player's servers from this same origin,
// and every cookie the browser holds for 127.0.0.1 would reach that server.
var (
	// pageRequestHeaders are the only headers a browser's request keeps on its way to a server.
	pageRequestHeaders = []string{"Accept", "Accept-Language", "Range", "User-Agent"}
	// pageReplyHeaders are the only headers of a server's answer the browser gets.
	pageReplyHeaders = []string{"Accept-Ranges", "Cache-Control", "Content-Disposition", "Content-Language", "Content-Range", "Content-Type",
		"Date", "ETag", "Expires", "Last-Modified", "Location"}
)

const pagePolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; form-action 'none'; frame-ancestors 'none'; base-uri 'none'"

// keepHeaders is h with only the names given.
func keepHeaders(h http.Header, names []string) http.Header {
	out := http.Header{}
	for _, n := range names {
		if v := h.Values(n); len(v) > 0 {
			out[n] = v
		}
	}
	return out
}

// countWriter counts what passes through it.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// forwardPage passes one page request to a server (as /dawngate/<page>, Connection: close) and its answer back.
func (f *Front) forwardPage(c net.Conn, req *http.Request, b Bookmark, page string, open func() (net.Conn, func(), error)) {
	f.forward(c, req, b, "/"+sitelinks.FallbackPrefix+page, time.Minute, open)
}

// forward passes a browser's request to a server's path and its answer back, within limit.
func (f *Front) forward(c net.Conn, req *http.Request, b Bookmark, path string, limit time.Duration, open func() (net.Conn, func(), error)) {
	start := time.Now()
	up, done, err := open()
	if err != nil {
		f.logf("page %s for %s: %v", req.RequestURI, b.Tag, err)
		writePage(c, http.StatusBadGateway, "No answer", fmt.Sprintf("<p>%s didn't answer the page: try again in a moment.</p>", html.EscapeString(displayName(b))))
		return
	}
	defer done()
	defer up.Close()
	out := req.Clone(req.Context())
	out.URL.Path, out.URL.RawPath, out.URL.RawQuery = path, "", ""
	out.RequestURI = ""
	out.Close = true
	out.Header = keepHeaders(req.Header, pageRequestHeaders)
	up.SetDeadline(time.Now().Add(limit))
	if err := out.Write(up); err != nil {
		f.logf("page %s for %s: %v", req.RequestURI, b.Tag, err)
		return
	}
	head := &headLimit{r: up, left: maxResponseHead}
	resp, err := http.ReadResponse(bufio.NewReader(head), out)
	head.off = true
	if err != nil {
		f.logf("page %s for %s: %v", req.RequestURI, b.Tag, err)
		writePage(c, http.StatusBadGateway, "No answer", fmt.Sprintf("<p>%s didn't answer the page: try again in a moment.</p>", html.EscapeString(displayName(b))))
		return
	}
	defer resp.Body.Close()
	resp.Header = keepHeaders(resp.Header, pageReplyHeaders)
	resp.Header.Set("Content-Security-Policy", pagePolicy)
	resp.Header.Set("X-Content-Type-Options", "nosniff")
	resp.Header.Set("X-Frame-Options", "DENY")
	resp.Header.Set("Referrer-Policy", "same-origin")
	resp.Close = true
	w := &countWriter{w: c}
	if err := resp.Write(w); err != nil {
		f.logf("page %s for %s: %v", req.RequestURI, b.Tag, err)
	}
	f.logf("page %s: %s's %s (%d bytes, %s)", req.RequestURI, b.Tag, path, w.n, time.Since(start).Round(time.Millisecond))
}

func displayName(b Bookmark) string {
	if b.Tag == ThisPCTag {
		return ThisPCName
	}
	if b.Name != "" {
		return b.Name
	}
	return b.Address
}

// signupWords is a sign-up mode in a sentence for the pages.
func signupWords(mode string) string {
	switch mode {
	case signupOpen:
		return "open: your first login with a new name makes the account"
	case signupInvite:
		return "invite only: sign up first, with an invite code from its operator"
	case signupClosed:
		return "closed: only its operator makes accounts"
	case signupLocal:
		return "no password: any name logs in"
	}
	return "not known yet"
}

var pageTitles = map[string]string{sitelinks.Signup: "Sign up", sitelinks.Password: "Forgotten password",
	sitelinks.Terms: "Terms", sitelinks.Privacy: "Privacy", sitelinks.Index: "Dawngate servers"}

// writeChooser writes the page of the player's servers whose links lead to each one's page.
func (f *Front) writeChooser(c net.Conn, page, note string) {
	var rows []string
	for _, s := range f.pageStatuses() {
		mode := s.Signup
		if s.Tag == ThisPCTag && f.ThisPC != nil && mode == "" {
			mode = f.ThisPC.signupMode()
		}
		link := "(no link: too many servers in the list)"
		if slot := f.slotOf(s.Tag); slot >= 0 {
			link = fmt.Sprintf(`<a href="%s">open its page</a>`, html.EscapeString(pickPath(slot, page)))
		}
		if s.Tag == ThisPCTag && mode == signupLocal {
			link = "no sign-up needed: any name and password log in"
		}
		rows = append(rows, fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>", html.EscapeString(displayName(s.Bookmark)),
			html.EscapeString(s.Address), html.EscapeString(signupWords(mode)), link))
	}
	body := ""
	if note != "" {
		body = "<p><b>" + html.EscapeString(note) + "</b></p>\n"
	}
	body += `<p>The servers of your Start-Client's list. Pick the one whose page you want (in the game, Sign up now! below the
login opens the page of the server that SERVER shows).</p>
<table><tr><th>Server</th><th>Address</th><th>Sign-up</th><th></th></tr>
` + strings.Join(rows, "\n") + "\n</table>"
	title := pageTitles[page]
	if title == "" {
		title = "Dawngate servers"
	}
	writePage(c, http.StatusOK, title+": pick the server", body)
}

func thisPCLocalPage(page string) string {
	if page == sitelinks.Password {
		return `<p>This PC checks no password: log in with your login name and any password.</p>
<p>The server on this PC runs without passwords (Start-Client.ini <code>local_trust = on</code>, the default); only this
PC reaches it. With <code>local_trust = off</code> it checks passwords like any server.</p>`
	}
	return `<p>No sign-up needed on This PC: type any login name and any password in the game's login screen and click
LOGIN. The name is your profile on this PC; no password is checked or stored.</p>
<p>The server on this PC runs without passwords (Start-Client.ini <code>local_trust = on</code>, the default); only this
PC reaches it. With <code>local_trust = off</code> it checks passwords like any server.</p>`
}

func thisPCWaitingPage(state, mode string) string {
	s := "<p>The server on this PC starts at your first login to This PC"
	if state == thisPCStarting || state == thisPCSetup {
		s = "<p>The server on this PC is starting"
	}
	s += ". Open this page again once the list says online.</p>"
	if mode != "" {
		s += "<p>Sign-up there: " + html.EscapeString(signupWords(mode)) + ".</p>"
	}
	return s
}

// writePage writes a whole answer of the front's own (plain HTML, no script) and leaves the connection to be closed.
func writePage(c net.Conn, status int, title, body string) {
	doc := "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>" + html.EscapeString(title) +
		"</title>\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<style>body{font-family:sans-serif;" +
		"max-width:44em;margin:2em auto;padding:0 1em;line-height:1.5}td,th{padding:.2em .6em;text-align:left}" +
		"code{font-size:1.1em}</style></head>\n<body><h1>" + html.EscapeString(title) + "</h1>\n" + body +
		"\n<p><small>OpenDawngateServer: Start-Client on this PC.</small></p></body></html>\n"
	fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\nCache-Control: no-store\r\n"+
		"Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'\r\n"+
		"X-Frame-Options: DENY\r\nX-Content-Type-Options: nosniff\r\nReferrer-Policy: same-origin\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), len(doc), doc)
}
