package web

// HOME1: the home page's live data. The page (common/home.html) fetches the relative "home.json" every few seconds;
// HTTP.Home makes the JSON (dgserver's home.go) and this file answers it: as JSON, or wrapped in a JSONP call when the
// request has a callback parameter (for a web view whose XHR doesn't work: <script src="home.json?callback=f">).

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/logging"
)

// HomeJSONSuffix is the end of the home page's data path (webContentRoot + "/common/home.json" for DRC "1").
const HomeJSONSuffix = "/common/home.json"

// maxCallback bounds a JSONP callback name.
const maxCallback = 64

// homeCallback is the request's JSONP callback (the query's "callback", else "cb"): "" without one, ok false when
// it isn't a plain name of 1 to maxCallback characters of [A-Za-z0-9_] (so it can't inject script).
func homeCallback(target string) (name string, ok bool) {
	i := strings.IndexByte(target, '?')
	if i < 0 {
		return "", true
	}
	q := target[i+1:]
	if j := strings.IndexByte(q, '#'); j >= 0 {
		q = q[:j]
	}
	v, err := url.ParseQuery(q)
	if err != nil {
		return "", false
	}
	name = v.Get("callback")
	if !v.Has("callback") {
		if !v.Has("cb") {
			return "", true
		}
		name = v.Get("cb")
	}
	if name == "" || len(name) > maxCallback {
		return "", false
	}
	for _, r := range name {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return "", false
		}
	}
	return name, true
}

// HomeReply is the answer to a home.json request target: status, content type and body. JSON as
// "application/json; charset=utf-8"; with a callback "/**/name(json);" as "application/javascript; charset=utf-8"; a
// bad callback 400.
func HomeReply(target string, data []byte) (status, ctype string, body []byte) {
	cb, ok := homeCallback(target)
	switch {
	case !ok:
		return "400 Bad Request", "text/plain; charset=utf-8",
			[]byte("callback must be 1 to 64 letters, digits or underscores\n")
	case cb != "":
		body = make([]byte, 0, len(data)+len(cb)+8)
		body = append(append(append(append(body, "/**/"...), cb...), '('), data...)
		return "200 OK", "application/javascript; charset=utf-8", append(body, ");"...)
	}
	return "200 OK", "application/json; charset=utf-8", data
}

// serveHome answers a home.json request: Cache-Control no-store, so the web view never shows a stale copy. It logs one
// line, and only the first request of a client address in homeLogEvery: the page polls it all the time.
func (h *HTTP) serveHome(c net.Conn, peer, method, target string) {
	const who = "http"
	status, ctype, body := HomeReply(target, h.Home())
	head := fmt.Sprintf("HTTP/1.1 %s\r\nContent-Type: %s\r\nContent-Length: %d\r\nCache-Control: no-store\r\n"+
		"Pragma: no-cache\r\nConnection: close\r\n\r\n", status, ctype, len(body))
	out := []byte(head)
	if method != "HEAD" {
		out = append(out, body...)
	}
	if _, err := c.Write(out); err != nil {
		h.Log.Logf(who, "%s %s: %v", peer, logging.ErrName(err), err)
		return
	}
	if h.homeLog.first(addrKey(c.RemoteAddr()), time.Now()) || status != "200 OK" {
		h.Log.Logf(who, "%s %s %s -> %s, %d bytes (live; later requests from this address within %s not logged)",
			peer, method, target, status, len(body), homeLogEvery)
	}
}

// homeLogEvery is how long serveHome stays quiet about one address after logging it.
const homeLogEvery = 10 * time.Minute

// homeLogAddresses bounds the addresses homeLog remembers.
const homeLogAddresses = 1024

// homeLog remembers when serveHome last logged each client address.
type homeLog struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// first reports whether key's request at now is to be logged, and if so remembers it.
func (l *homeLog) first(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.last[key]; ok && now.Sub(t) < homeLogEvery {
		return false
	}
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	if len(l.last) >= homeLogAddresses { // forget the stale ones, or all if none is
		for k, t := range l.last {
			if now.Sub(t) >= homeLogEvery {
				delete(l.last, k)
			}
		}
		if len(l.last) >= homeLogAddresses {
			clear(l.last)
		}
	}
	l.last[key] = now
	return true
}

// addrKey is a client address without its port (a tunnel connection's real address).
func addrKey(a net.Addr) string {
	switch t := a.(type) {
	case *net.TCPAddr:
		return t.IP.String()
	case interface{ IPPort() (net.IP, int) }:
		ip, _ := t.IPPort()
		return ip.String()
	}
	if a == nil {
		return ""
	}
	return a.String()
}
