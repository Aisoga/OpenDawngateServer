package web

// MHIST2: the match history's JSON. The history page (common/history.html, beside home.html) reads the list of
// recent games ("games.json") and one match ("game.json") from its own origin; HTTP.History makes the JSON (dgserver's
// history.go) and this file answers it, as home.go answers home.json: no-store, JSON or a JSONP call, a quiet log.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dawngate/server/internal/logging"
)

// The match history's data paths (webContentRoot + the suffix for DRC "1"), matched at the end of the request path
// like HomeJSONSuffix.
const (
	GamesJSONSuffix = "/common/games.json" // the list: ?player=<name>|all&before=<id>&n=<1..50>&me=<name>
	GameJSONSuffix  = "/common/game.json"  // one match: ?id=<id>&me=<name>
)

// History kinds, HTTP.History's first argument.
const (
	HistoryGames = "games"
	HistoryGame  = "game"
)

// historyKind is the history request a path asks for, "" for none.
func historyKind(path string) string {
	switch {
	case strings.HasSuffix(path, GamesJSONSuffix):
		return HistoryGames
	case strings.HasSuffix(path, GameJSONSuffix):
		return HistoryGame
	}
	return ""
}

// historyQuery is the query of a request target (url.Values{} without one; ok false when it doesn't parse).
func historyQuery(target string) (url.Values, bool) {
	i := strings.IndexByte(target, '?')
	if i < 0 {
		return url.Values{}, true
	}
	q := target[i+1:]
	if j := strings.IndexByte(q, '#'); j >= 0 {
		q = q[:j]
	}
	v, err := url.ParseQuery(q)
	return v, err == nil
}

// HistoryError is the JSON body of a history answer that isn't 200: {"version":1,"error":code,"message":text}.
func HistoryError(code, message string) []byte {
	b, _ := json.Marshal(struct {
		Version int    `json:"version"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}{1, code, message})
	return b
}

// HistoryReply is the answer to a history request: status line, content type and body, and the hook's own status
// code (what the log reports). JSON keeps the status the hook gave; with a JSONP callback the answer is always 200 (a
// script element runs nothing else), the error object inside. A bad callback or a query that doesn't parse is 400
// without calling the hook.
func HistoryReply(kind, target string, hook func(kind string, q url.Values) (int, []byte)) (status, ctype string, body []byte, code int) {
	q, ok := historyQuery(target)
	if !ok {
		return "400 Bad Request", "application/json; charset=utf-8", HistoryError("badRequest", "the query doesn't parse"), 400
	}
	cb, ok := homeCallback(target)
	if !ok {
		return "400 Bad Request", "text/plain; charset=utf-8",
			[]byte("callback must be 1 to 64 letters, digits or underscores\n"), 400
	}
	code, data := hook(kind, q)
	if cb != "" {
		_, ctype, body = HomeReply(target, data)
		return "200 OK", ctype, body, code
	}
	return fmt.Sprintf("%d %s", code, http.StatusText(code)), "application/json; charset=utf-8", data, code
}

// serveHistory answers a games.json or game.json request: Cache-Control no-store (a new match must show), and one log
// line for the first request of a client address in homeLogEvery and for every answer that isn't 200.
func (h *HTTP) serveHistory(c net.Conn, peer, method, target, kind string) {
	const who = "http"
	status, ctype, body, code := HistoryReply(kind, target, h.History)
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
	if h.historyLog.first(addrKey(c.RemoteAddr()), time.Now()) || code != 200 {
		note := ""
		if code != 200 && status == "200 OK" {
			note = fmt.Sprintf(" (JSONP; the answer's status %d)", code)
		}
		h.Log.Logf(who, "%s %s %s -> %s%s, %d bytes (match history; later requests from this address within %s not logged)",
			peer, method, target, status, note, len(body), homeLogEvery)
	}
}
