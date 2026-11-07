// Package web holds the HTTP side of the server: the HTTP logger and file server that replaces EA's
// CDN (telemetry_rules.xml, web content), the crash-report and the debug-telemetry URLs
// (re/scripts/patch_config.py points them at us), and the QoS server (qos.go, roadmap item B2).
//
// The HTTP server is a small hand-written HTTP/1.1 reader rather than net/http, so the log shows each
// request exactly as sent (request line, lower-cased headers in order, body) and replies carry only
// Content-Type, Content-Length and Connection: close, as the Python stub's did.
package web

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/tdf"
)

// Builtin is an answer served when --www has no file for the path.
type Builtin struct {
	Suffix      string // matched against the end of the request path
	ContentType string
	Body        []byte
	Path        string // the path the client requests (with DRC "1", en-us); dgserver --write-www writes it
}

// webContentRoot is the web content's path for BGWeb DRC "1" (blaze.WebContentVersion) and the live
// environment (docs/WEB_CONTENT.md): <loc> is webContentRoot+"/en-us", <com> webContentRoot+"/common".
const webContentRoot = "battlegrounds_test/live/www/client/1"

// Builtins are matched in order. The client fetches NewTelemetryServerConfigURL
// (".../builds/manifests/telemetry_rules.xml") at startup and retries with backoff on 404; after login
// the web content of webcontent.go (B6).
var Builtins = []Builtin{
	{Suffix: "/telemetry_rules.xml", ContentType: "text/xml", Body: TelemetryRules(),
		Path: "battlegrounds_test/live/builds/manifests/telemetry_rules.xml"},
	{Suffix: "/dynamic_strings/dynamic_strings.xml", ContentType: "text/xml", Body: DynamicStrings(),
		Path: webContentRoot + "/en-us/dynamic_strings/dynamic_strings.xml"},
	{Suffix: "/dynamic_lore/dynamic_lore.snap", ContentType: "application/octet-stream", Body: DynamicLoreSnap(),
		Path: webContentRoot + "/en-us/dynamic_lore/dynamic_lore.snap"},
	{Suffix: "/common/home.html", ContentType: "text/html", Body: HomePage(),
		Path: webContentRoot + "/common/home.html"},
	{Suffix: "/common/history.html", ContentType: "text/html", Body: HistoryPage(),
		Path: webContentRoot + "/common/history.html"},
}

// WriteBuiltins writes every built-in answer under dir at its Path (dgserver --write-www), so the files
// can be checked (re/scripts/snap.py verify) or edited and served with --www. It returns the files
// written.
func WriteBuiltins(dir string) ([]string, error) {
	var written []string
	for _, b := range Builtins {
		file := filepath.Join(dir, filepath.FromSlash(b.Path))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(file, b.Body, 0o644); err != nil {
			return written, err
		}
		written = append(written, file)
	}
	return written, nil
}

// HTTP serves one request per connection and logs it.
type HTTP struct {
	Log *logging.Log
	WWW string // absolute web root, or "" (then only built-in answers, else 404)
	// Extra answers are served at exactly their Path (before --www and the built-ins), e.g. the CA modulus
	// (dgserver --serve-ca, CAModulusPath).
	Extra []Builtin
	// Redirects answer exactly their path (escaped, without the query) with 302 Found to the URL (since the
	// client's login-screen links /dawngate/<page> go to the website's pages when dgserver runs one). They come
	// before Extra.
	Redirects map[string]string
	// Download is the server's client package (dgserver --client-package; nil: none): its file streamed at
	// Download.Path, its page at DownloadPagePath.
	Download *Download
	// Home is the home page's live data (HOME1, dgserver's home.json; nil: none, 404), served at every path that ends
	// with HomeJSONSuffix before --www and the built-ins (ServeHome). It must be cheap: every client on the home page
	// polls it.
	Home func() []byte
	// History is the match history's JSON (MHIST2, dgserver's history.go; nil: none, 404), served at every path that
	// ends with GamesJSONSuffix (kind HistoryGames) or GameJSONSuffix (HistoryGame) before --www and the built-ins
	// (history.go): the HTTP status code and the JSON body (HistoryError's for an error) for the request's query.
	History func(kind string, query url.Values) (int, []byte)

	// Maps are the map packages the server hands out (map delivery, mapdist.go; nil: none, 404 at its paths).
	Maps    MapPackages
	mapDist mapDist

	homeLog, historyLog homeLog
}

// Download is the client package on the HTTP port.
type Download struct {
	Path, File  string                 // the file's URL path (/dawngate/download/<File>) and name
	Disposition string                 // its Content-Disposition
	Open        func() (string, error) // the package file, built first if need be (an fs.ErrNotExist error: none, 404)
	// State is the tunnel key's fingerprint and why the server has no package ("" when it has one), for the page
	State func() (fingerprint, unavailable string)
}

// DownloadPagePath is the client package's page on the HTTP port (beside the login screen's pages).
const DownloadPagePath = "dawngate/download"

// CAModulusPath is where dgserver --serve-ca serves the redirector CA's public modulus (128 bytes, ca_modulus.bin):
// Start-Client.exe fetches it from a server on another machine and has the player compare its Fingerprint with the
// one the operator's Start-Server.exe shows.
const CAModulusPath = "dawngate/ca_modulus.bin"

// VersionPath is where dgserver answers its build's version (version.String, one line of text). Start-Client asks it
// of a server it plays on without the tunnel; through the tunnel the gateway's info hello answers it.
const VersionPath = "dawngate/version.txt"

// VersionPage is the answer at VersionPath.
func VersionPage(v string) Builtin {
	return Builtin{Path: VersionPath, ContentType: "text/plain", Body: []byte(v + "\n")}
}

// MapsPath is where dgserver answers the maps a custom game may pick there besides The Dawngate (--map,
// --custom-maps): their tags joined by "+", the tunnel's FactMaps value, one line (empty: none). A checkout's
// Start-Client.cmd, which plays without the tunnel, asks it for the create dialog's list (mapsetup --server-list).
const MapsPath = "dawngate/maps.txt"

// MapsPage is the answer at MapsPath for FactMaps's value.
func MapsPage(fact string) Builtin {
	return Builtin{Path: MapsPath, ContentType: "text/plain", Body: []byte(fact + "\n")}
}

// MapVersionsPath is where dgserver answers its versions of the maps of MapsPath (MAPED-GUARD-dialog): a line
// "<tag> <SHA-256 of the host copy's archive>" per map that has one (tunnel.MapVersionsText; empty: none), so that
// a checkout's Start-Client.cmd leaves out of the create dialog a map its copy has in another version. A server of a
// release before it answers 404, and the dialog lists the map as before. Its own page and not more lines in MapsPath,
// which a Start-Client of an earlier release reads whole as one value.
const MapVersionsPath = "dawngate/mapversions.txt"

// MapVersionsPage is the answer at MapVersionsPath for tunnel.MapVersionsText's body.
func MapVersionsPage(text string) Builtin {
	return Builtin{Path: MapVersionsPath, ContentType: "text/plain", Body: []byte(text)}
}

// Fingerprint is a CA modulus's fingerprint: the first 16 hex digits of its SHA-256 (patch_client.py --show's
// ca_sha256, certs.Fingerprint).
func Fingerprint(modulus []byte) string {
	h := sha256.Sum256(modulus)
	return hex.EncodeToString(h[:])[:16]
}

// maxHead bounds the request head, like asyncio's StreamReader limit.
const maxHead = 64 * 1024

// The request body's limits: a body longer than its path's limit (Content-Length, or the chunks' running total) is
// answered 413 and the connection closed, before any of it is read. Nothing here uses a body beyond logging its
// first logging.MaxDump bytes, so the body is read in pieces and only those bytes are kept, whatever its length.
// The game's only bodies are CrashRpt's reports: an error report was 62 KB, a crash's report (a minidump, the logs and
// a picture of every screen) is untested, so the crash paths get room for several big pictures; every other path
// (the web content, the home page, the history, the maps, the QoS requests) is GETs only.
const (
	maxBody      = 1 << 20  // 1 MiB: every path but the crash reports'
	maxCrashBody = 32 << 20 // 32 MiB: CrashRpt's reports under /crash/
	maxChunkLine = 4096     // a chunk's size line (with its extensions), bufio.Reader's default size
)

// bodyLimit is the body limit for a request target.
func bodyLimit(target string) int64 {
	if strings.HasPrefix(urlPath(target), "/crash/") {
		return maxCrashBody
	}
	return maxBody
}

// errTooLarge is a body over its path's limit (413).
type errTooLarge struct {
	limit int64
	what  string // the claimed length or the chunk that went over
}

func (e errTooLarge) Error() string {
	return fmt.Sprintf("body over the limit of %d bytes (%s)", e.limit, e.what)
}

// oneLine makes a client's text one line of the log: a head is split at CR LF only, so a bare LF could forge a log line.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return '?'
		}
		return r
	}, s)
}

// Serve accepts connections until the listener is closed.
func (h *HTTP) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go h.ServeConn(c)
	}
}

type header struct{ name, value string }

type request struct {
	line    string
	headers []header
	body    []byte // the body's first logging.MaxDump bytes
	size    int64  // the body's length
}

// Write keeps a body's first logging.MaxDump bytes and counts the rest (readRequest's sink).
func (r *request) Write(p []byte) (int, error) {
	if room := logging.MaxDump - len(r.body); room > 0 {
		r.body = append(r.body, p[:min(room, len(p))]...)
	}
	r.size += int64(len(p))
	return len(p), nil
}

func (r *request) get(name string) string {
	for _, h := range r.headers {
		if h.name == name {
			return h.value
		}
	}
	return ""
}

// ServeConn reads one request (30 s limit), logs it, answers and closes the connection. A request it can't read
// is answered 400 (malformed) or 413 (a body over its limit, bodyLimit); a panic in its handling is logged and
// ends only this connection.
func (h *HTTP) ServeConn(c net.Conn) {
	defer c.Close()
	const who = "http"
	peer := logging.Peer(c.RemoteAddr())
	defer h.Log.Recover(who, peer, c)
	c.SetReadDeadline(time.Now().Add(30 * time.Second))
	req, err := readRequest(bufio.NewReaderSize(c, maxChunkLine))
	if err != nil {
		refuse(h.Log, who, c, peer, req, err)
		return
	}
	if req == nil {
		return // closed without a request
	}
	if h.Home != nil {
		if f := strings.Fields(req.line); len(f) >= 2 && strings.HasSuffix(urlPath(f[1]), HomeJSONSuffix) {
			h.serveHome(c, peer, f[0], f[1])
			return
		}
	}
	if h.History != nil {
		if f := strings.Fields(req.line); len(f) >= 2 {
			if kind := historyKind(urlPath(f[1])); kind != "" {
				h.serveHistory(c, peer, f[0], f[1], kind)
				return
			}
		}
	}
	if h.Maps != nil {
		if f := strings.Fields(req.line); len(f) >= 2 && isMapDist(urlPath(f[1])) {
			h.serveMapDist(c, peer, f[0], f[1])
			return
		}
	}
	var hdrs []string
	for _, hd := range req.headers {
		hdrs = append(hdrs, "    "+oneLine(hd.name)+": "+oneLine(hd.value))
	}
	msg := peer + " " + oneLine(req.line) + "\n" + strings.Join(hdrs, "\n")
	if req.size > 0 {
		more := ""
		if req.size > int64(len(req.body)) {
			more = fmt.Sprintf(" (first %d shown)", len(req.body))
		}
		msg += fmt.Sprintf("\n    body %d bytes%s:\n%s", req.size, more, logging.Hexdump(req.body))
	}
	h.Log.Log(who, msg)

	parts := strings.Fields(req.line)
	target := "/"
	if len(parts) >= 2 {
		target = parts[1]
	}
	if to, ok := h.Redirects[urlPath(target)]; ok {
		body := []byte("See " + to + "\n")
		head := fmt.Sprintf("HTTP/1.1 302 Found\r\nLocation: %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n"+
			"Connection: close\r\n\r\n", to, len(body))
		out := []byte(head)
		if len(parts) == 0 || parts[0] != "HEAD" {
			out = append(out, body...)
		}
		if _, err := c.Write(out); err != nil {
			h.Log.Logf(who, "%s %s: %v", peer, logging.ErrName(err), err)
			return
		}
		h.Log.Log(who, "-> 302 Found, to "+to)
		return
	}
	if d := h.Download; d != nil && urlPath(target) == d.Path {
		h.serveDownload(c, peer, len(parts) > 0 && parts[0] == "HEAD")
		return
	}
	var data []byte
	ctype, status, source := "text/plain", "404 Not Found", ""
	if d := h.Download; d != nil && urlPath(target) == "/"+DownloadPagePath {
		fp, why := d.State()
		data, ctype, status, source = DownloadPage(d.Path, d.File, fp, why), "text/html; charset=utf-8", "200 OK", "(served)"
	}
	for _, b := range h.Extra {
		if urlPath(target) == "/"+b.Path && source == "" {
			data, ctype, status, source = b.Body, b.ContentType, "200 OK", "(served)"
			break
		}
	}
	if file := h.lookup(target); file != "" && source == "" {
		if b, err := os.ReadFile(file); err == nil {
			data, status, source = b, "200 OK", "from "+file
			ctype = "application/octet-stream"
			if t := mime.TypeByExtension(filepath.Ext(file)); t != "" {
				ctype = strings.TrimSpace(strings.SplitN(t, ";", 2)[0])
			}
		}
	}
	if source == "" {
		for _, b := range Builtins {
			if strings.HasSuffix(urlPath(target), b.Suffix) {
				data, ctype, status, source = b.Body, b.ContentType, "200 OK", "(built-in)"
				break
			}
		}
	}
	head := fmt.Sprintf("HTTP/1.1 %s\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", status, ctype, len(data))
	out := []byte(head)
	if len(parts) == 0 || parts[0] != "HEAD" {
		out = append(out, data...)
	}
	if _, err := c.Write(out); err != nil {
		h.Log.Logf(who, "%s %s: %v", peer, logging.ErrName(err), err)
		return
	}
	note := ""
	if len(data) > 0 {
		note = fmt.Sprintf(", %d bytes %s", len(data), source)
	}
	h.Log.Log(who, "-> "+status+note)
}

// serveDownload streams the client package (built first if need be: the request waits), with its
// Content-Disposition; 404 when the server has none, 503 while it can't be made (the reason is in the log).
func (h *HTTP) serveDownload(c net.Conn, peer string, head bool) {
	const who = "http"
	d := h.Download
	reply := func(status, text string) {
		body := []byte(text + "\n")
		out := fmt.Sprintf("HTTP/1.1 %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", status, len(body))
		if !head {
			out += string(body)
		}
		c.Write([]byte(out))
		h.Log.Log(who, "-> "+status)
	}
	file, err := d.Open()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			reply("404 Not Found", "No client package: "+err.Error()+".")
		} else {
			reply("503 Service Unavailable", "The client package can't be made right now; please try again later.")
		}
		return
	}
	f, err := os.Open(file)
	if err != nil {
		reply("503 Service Unavailable", "The client package can't be read right now; please try again later.")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		reply("503 Service Unavailable", "The client package can't be read right now; please try again later.")
		return
	}
	c.SetWriteDeadline(time.Now().Add(30 * time.Minute))
	hdr := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/zip\r\nContent-Length: %d\r\nContent-Disposition: %s\r\n"+
		"Connection: close\r\n\r\n", st.Size(), d.Disposition)
	if _, err := io.WriteString(c, hdr); err != nil {
		h.Log.Logf(who, "%s %s: %v", peer, logging.ErrName(err), err)
		return
	}
	if !head {
		if n, err := io.Copy(c, f); err != nil {
			h.Log.Logf(who, "%s the client package cut short after %d of %d bytes: %s: %v", peer, n, st.Size(), logging.ErrName(err), err)
			return
		}
	}
	h.Log.Logf(who, "-> 200 OK, the client package %s (%d bytes)", d.File, st.Size())
}

// lookup maps a request target to a file under the web root, or "".
func (h *HTTP) lookup(target string) string {
	if h.WWW == "" {
		return ""
	}
	p := strings.TrimLeft(unquote(urlPath(target)), "/")
	full := filepath.Join(h.WWW, filepath.FromSlash(p))
	if full != h.WWW && !strings.HasPrefix(full, h.WWW+string(filepath.Separator)) {
		return "" // outside the web root
	}
	if st, err := os.Stat(full); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	return full
}

// urlPath is the path part of a request target (absolute URL or origin form), still escaped.
func urlPath(target string) string {
	if i := strings.Index(target, "://"); i > 0 && !strings.ContainsAny(target[:i], "/?#") {
		rest := target[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			target = rest[j:]
		} else {
			target = ""
		}
	}
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		target = target[:i]
	}
	return target
}

// unquote decodes %XX escapes (invalid ones stay as they are).
func unquote(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b = append(b, byte(v))
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// errValue is a malformed request (the Python stub's ValueError).
type errValue struct{ msg string }

func (e errValue) Error() string { return e.msg }

func describeErr(err error) string {
	var ve errValue
	if errors.As(err, &ve) {
		return "ValueError: " + ve.msg
	}
	var tl errTooLarge
	if errors.As(err, &tl) {
		return "TooLarge: " + tl.Error()
	}
	return logging.ErrName(err) + ": " + err.Error()
}

// readRequest returns the request, or nil if the connection closed before one started.
func readRequest(r *bufio.Reader) (*request, error) {
	var head []byte
	for !bytes.HasSuffix(head, []byte("\r\n\r\n")) {
		b, err := r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(bytes.TrimSpace(head)) > 0 {
					return nil, errValue{"truncated request head: " + tdf.Repr(head[:min(len(head), 200)])}
				}
				return nil, nil
			}
			return nil, err
		}
		head = append(head, b)
		if len(head) > maxHead {
			return nil, errValue{"request head longer than 64 KiB"}
		}
	}
	lines := strings.Split(latin1(head), "\r\n")
	req := &request{line: lines[0]}
	for _, l := range lines[1:] {
		if k, v, ok := strings.Cut(l, ":"); ok {
			name, value := strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
			replaced := false
			for i := range req.headers { // a repeated header keeps its place and its last value
				if req.headers[i].name == name {
					req.headers[i].value, replaced = value, true
				}
			}
			if !replaced {
				req.headers = append(req.headers, header{name, value})
			}
		}
	}
	// The body: every length the client claims is checked against what is left of the path's limit before a byte of
	// it is read, and it goes to req's sink in bufio-sized pieces (nothing is ever sized by a claimed length).
	target := "/"
	if f := strings.Fields(req.line); len(f) >= 2 {
		target = f[1]
	}
	limit := bodyLimit(target)
	switch {
	case strings.EqualFold(req.get("transfer-encoding"), "chunked"):
		for {
			line, err := r.ReadSlice('\n') // at most the reader's buffer (maxChunkLine)
			if errors.Is(err, bufio.ErrBufferFull) {
				return req, errValue{fmt.Sprintf("chunk size line longer than %d bytes", maxChunkLine)}
			}
			if err != nil {
				return req, unexpectedEOF(err)
			}
			sizeText := strings.TrimSpace(strings.SplitN(string(line), ";", 2)[0])
			size, perr := strconv.ParseUint(sizeText, 16, 64)
			if perr != nil {
				if errors.Is(perr, strconv.ErrRange) {
					return req, errTooLarge{limit, "a chunk of 0x" + sizeText + " bytes"}
				}
				return req, errValue{fmt.Sprintf("invalid chunk size %q", line)}
			}
			if size > uint64(limit-req.size) { // req.size <= limit throughout, so no overflow
				return req, errTooLarge{limit, fmt.Sprintf("a chunk of %d bytes after %d", size, req.size)}
			}
			if _, err := io.CopyN(req, r, int64(size)); err != nil {
				return req, unexpectedEOF(err)
			}
			if _, err := r.Discard(2); err != nil { // the chunk's CRLF (after the last chunk: the body's end)
				return req, unexpectedEOF(err)
			}
			if size == 0 {
				break
			}
		}
	case req.get("content-length") != "":
		n, err := strconv.ParseInt(req.get("content-length"), 10, 64)
		if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(req.get("content-length"), "-") {
			return req, errTooLarge{limit, "Content-Length " + req.get("content-length")}
		}
		if err != nil || n < 0 {
			return req, errValue{fmt.Sprintf("invalid content-length %q", req.get("content-length"))}
		}
		if n > limit {
			return req, errTooLarge{limit, fmt.Sprintf("Content-Length %d", n)}
		}
		if _, err := io.CopyN(req, r, n); err != nil {
			return req, unexpectedEOF(err)
		}
	}
	return req, nil
}

// unexpectedEOF is a body read's error, io.EOF (the connection closed before the body's end) made
// io.ErrUnexpectedEOF.
func unexpectedEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// refuse logs a request readRequest couldn't read (req: its head, nil if that wasn't read) and answers it: 413 for a
// body over its limit, 400 for a malformed request; a connection that failed, closed or timed out gets no answer.
func refuse(log *logging.Log, who string, c net.Conn, peer string, req *request, err error) {
	what := describeErr(err)
	if req != nil {
		what = req.line + ": " + what
	}
	log.Logf(who, "%s %s", peer, what)
	var tooLarge errTooLarge
	var bad errValue
	status, text := "", ""
	switch {
	case errors.As(err, &tooLarge):
		status, text = "413 Content Too Large", fmt.Sprintf("The request's body is over this server's limit of %d bytes.", tooLarge.limit)
	case errors.As(err, &bad):
		status, text = "400 Bad Request", "The request can't be read."
	default:
		return
	}
	body := text + "\n"
	c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(c, "HTTP/1.1 %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, len(body), body); err != nil {
		log.Logf(who, "%s %s: %v", peer, logging.ErrName(err), err)
		return
	}
	log.Log(who, "-> "+status)
	// The client may still be sending the body: closing with its bytes unread would reset the connection, and the
	// reset can take the answer with it. So end our side, then read (and drop) a bounded amount before the close.
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	io.CopyN(io.Discard, c, maxBody)
}

func latin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}
