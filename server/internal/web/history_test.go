package web

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The match history's JSON: games.json and game.json at their path suffix (any DRC, absolute URL too) go to the hook
// with their kind and query; JSON with no-store and the hook's status; JSONP always 200 with the error inside; a bad
// callback or query 400 without the hook; HEAD without the body; the log quiet after an address's first request,
// except for errors; without the hook the paths are unknown files.
func TestHistoryJSONServed(t *testing.T) {
	type call struct {
		kind string
		q    url.Values
	}
	var calls []call
	h := &HTTP{History: func(kind string, q url.Values) (int, []byte) {
		calls = append(calls, call{kind, q})
		if q.Get("id") == "404" {
			return 404, HistoryError("notFound", "no such match")
		}
		return 200, []byte(`{"version":1}`)
	}}
	head := func(status, ctype string, n int) string {
		return "HTTP/1.1 " + status + "\r\nContent-Type: " + ctype + "\r\nContent-Length: " + strconv.Itoa(n) +
			"\r\nCache-Control: no-store\r\nPragma: no-cache\r\nConnection: close\r\n\r\n"
	}
	const jsonType = "application/json; charset=utf-8"
	games := "/" + webContentRoot + GamesJSONSuffix
	resp, log := roundTrip(t, h, "GET "+games+"?player=all&before=12&n=5 HTTP/1.1\r\nHost: x\r\n\r\n")
	if want := head("200 OK", jsonType, 13) + `{"version":1}`; resp != want {
		t.Errorf("games.json:\n%q\nwant\n%q", resp, want)
	}
	if len(calls) != 1 || calls[0].kind != HistoryGames || calls[0].q.Get("player") != "all" || calls[0].q.Get("before") != "12" {
		t.Errorf("calls %+v", calls)
	}
	if want := "[http] PEER GET " + games + "?player=all&before=12&n=5 -> 200 OK, 13 bytes (match history; later requests from this address within 10m0s not logged)\n"; !strings.Contains(log, want) {
		t.Errorf("log lacks %q:\n%s", want, log)
	}
	// game.json, absolute URL, another DRC: the hook's kind; quiet now
	resp, log = roundTrip(t, h, "GET http://127.0.0.1:18080/a/b/2/common/game.json?id=7&me=Alpha HTTP/1.1\r\n\r\n")
	if !strings.HasSuffix(resp, `{"version":1}`) || log != "" || calls[1].kind != HistoryGame || calls[1].q.Get("me") != "Alpha" {
		t.Errorf("game.json: %q, log %q, calls %+v", resp, log, calls)
	}
	// the hook's error: its status, always logged
	resp, log = roundTrip(t, h, "GET "+"/"+webContentRoot+GameJSONSuffix+"?id=404 HTTP/1.1\r\n\r\n")
	body := `{"version":1,"error":"notFound","message":"no such match"}`
	if want := head("404 Not Found", jsonType, len(body)) + body; resp != want || !strings.Contains(log, "-> 404 Not Found, ") {
		t.Errorf("an error:\n%q\nwant\n%q\nlog %q", resp, want, log)
	}
	// JSONP: 200 with the error object inside, the log names the answer's status
	resp, log = roundTrip(t, h, "GET "+"/"+webContentRoot+GameJSONSuffix+"?id=404&callback=dg_game HTTP/1.1\r\n\r\n")
	js := "/**/dg_game(" + body + ");"
	if want := head("200 OK", "application/javascript; charset=utf-8", len(js)) + js; resp != want ||
		!strings.Contains(log, "-> 200 OK (JSONP; the answer's status 404), ") {
		t.Errorf("JSONP error:\n%q\nwant\n%q\nlog %q", resp, want, log)
	}
	resp, _ = roundTrip(t, h, "HEAD "+games+" HTTP/1.1\r\n\r\n")
	if resp != head("200 OK", jsonType, 13) {
		t.Errorf("HEAD: %q", resp)
	}
	n := len(calls)
	for _, q := range []string{"?callback=alert(1)", "?player=%zz"} {
		resp, log = roundTrip(t, h, "GET "+games+q+" HTTP/1.1\r\n\r\n")
		if !strings.HasPrefix(resp, "HTTP/1.1 400 Bad Request\r\n") || strings.Contains(resp, "alert") || !strings.Contains(log, "-> 400 Bad Request") {
			t.Errorf("%s: %q, log %q", q, resp, log)
		}
	}
	if !strings.HasSuffix(resp, `{"version":1,"error":"badRequest","message":"the query doesn't parse"}`) || len(calls) != n {
		t.Errorf("a query that doesn't parse: %q, the hook called %d times", resp, len(calls)-n)
	}
	// without the hook: unknown files; other names aren't the history's
	for _, p := range []string{games, "/" + webContentRoot + GameJSONSuffix} {
		if resp, _ = roundTrip(t, &HTTP{}, "GET "+p+" HTTP/1.1\r\n\r\n"); !strings.HasPrefix(resp, "HTTP/1.1 404 Not Found\r\n") {
			t.Errorf("no hook, %s: %q", p, resp)
		}
	}
	for _, p := range []string{"/x/common/mygames.json", "/x/common/games.json.bak", "/x/en-us/game.jsonx"} {
		if historyKind(p) != "" {
			t.Errorf("%s is a history path", p)
		}
	}
}
