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
	"mime"
	"net"
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
}

// CAModulusPath is where dgserver --serve-ca serves the redirector CA's public modulus (128 bytes, ca_modulus.bin):
// Start-Client.exe fetches it from a server on another machine and has the player compare its Fingerprint with the
// one the operator's Start-Server.exe shows.
const CAModulusPath = "dawngate/ca_modulus.bin"

// Fingerprint is a CA modulus's fingerprint: the first 16 hex digits of its SHA-256 (patch_client.py --show's
// ca_sha256, certs.Fingerprint).
func Fingerprint(modulus []byte) string {
	h := sha256.Sum256(modulus)
	return hex.EncodeToString(h[:])[:16]
}

// maxHead bounds the request head, like asyncio's StreamReader limit.
const maxHead = 64 * 1024

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
	body    []byte
}

func (r *request) get(name string) string {
	for _, h := range r.headers {
		if h.name == name {
			return h.value
		}
	}
	return ""
}

// ServeConn reads one request (30 s limit), logs it, answers and closes the connection.
func (h *HTTP) ServeConn(c net.Conn) {
	defer c.Close()
	const who = "http"
	peer := logging.Peer(c.RemoteAddr())
	c.SetReadDeadline(time.Now().Add(30 * time.Second))
	req, err := readRequest(bufio.NewReader(c))
	if err != nil {
		h.Log.Logf(who, "%s %s", peer, describeErr(err))
		return
	}
	if req == nil {
		return // closed without a request
	}
	var hdrs []string
	for _, hd := range req.headers {
		hdrs = append(hdrs, "    "+hd.name+": "+hd.value)
	}
	msg := peer + " " + req.line + "\n" + strings.Join(hdrs, "\n")
	if len(req.body) > 0 {
		more := ""
		if len(req.body) > logging.MaxDump {
			more = fmt.Sprintf(" (first %d shown)", logging.MaxDump)
		}
		msg += fmt.Sprintf("\n    body %d bytes%s:\n%s", len(req.body), more, logging.Hexdump(req.body[:min(len(req.body), logging.MaxDump)]))
	}
	h.Log.Log(who, msg)

	parts := strings.Fields(req.line)
	target := "/"
	if len(parts) >= 2 {
		target = parts[1]
	}
	var data []byte
	ctype, status, source := "text/plain", "404 Not Found", ""
	for _, b := range h.Extra {
		if urlPath(target) == "/"+b.Path {
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
	switch {
	case strings.EqualFold(req.get("transfer-encoding"), "chunked"):
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return nil, err
			}
			sizeText := strings.TrimSpace(strings.SplitN(line, ";", 2)[0])
			size, perr := strconv.ParseInt(sizeText, 16, 64)
			if perr != nil || size < 0 {
				return nil, errValue{fmt.Sprintf("invalid chunk size %q", line)}
			}
			chunk := make([]byte, size+2)
			if _, err := io.ReadFull(r, chunk); err != nil {
				return nil, err
			}
			if size == 0 {
				break
			}
			req.body = append(req.body, chunk[:size]...)
		}
	case req.get("content-length") != "":
		n, err := strconv.Atoi(req.get("content-length"))
		if err != nil || n < 0 {
			return nil, errValue{fmt.Sprintf("invalid content-length %q", req.get("content-length"))}
		}
		req.body = make([]byte, n)
		if _, err := io.ReadFull(r, req.body); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func latin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}
