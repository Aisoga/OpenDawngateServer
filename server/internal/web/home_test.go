package web

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

const homePath = "/" + webContentRoot + HomeJSONSuffix

// The home page's data: JSON with no-store at the path's suffix (any DRC or locale, absolute URL too), HEAD without
// the body, JSONP with a plain callback name, 400 for any other; one log line per client address.
func TestHomeJSONServed(t *testing.T) {
	calls := 0
	h := &HTTP{Home: func() []byte { calls++; return []byte(`{"version":1}`) }}
	head := func(status, ctype string, n int) string {
		return "HTTP/1.1 " + status + "\r\nContent-Type: " + ctype + "\r\nContent-Length: " + strconv.Itoa(n) +
			"\r\nCache-Control: no-store\r\nPragma: no-cache\r\nConnection: close\r\n\r\n"
	}
	resp, log := roundTrip(t, h, "GET "+homePath+" HTTP/1.1\r\nHost: 127.0.0.1:18080\r\n\r\n")
	if want := head("200 OK", "application/json; charset=utf-8", 13) + `{"version":1}`; resp != want {
		t.Errorf("response:\n%q\nwant\n%q", resp, want)
	}
	if want := "[http] PEER GET " + homePath + " -> 200 OK, 13 bytes (live; later requests from this address within 10m0s not logged)\n"; !strings.Contains(log, want) || strings.Contains(log, "host:") {
		t.Errorf("log lacks %q (or has the headers):\n%s", want, log)
	}
	// the same address again: answered, not logged
	resp, log = roundTrip(t, h, "GET http://127.0.0.1:18080/a/b/2/common/home.json?t=5 HTTP/1.1\r\n\r\n")
	if !strings.HasSuffix(resp, `{"version":1}`) || log != "" {
		t.Errorf("second request: %q, log %q", resp, log)
	}
	resp, _ = roundTrip(t, h, "HEAD "+homePath+" HTTP/1.1\r\n\r\n")
	if resp != head("200 OK", "application/json; charset=utf-8", 13) {
		t.Errorf("HEAD: %q", resp)
	}
	resp, _ = roundTrip(t, h, "GET "+homePath+"?callback=dg_home1&t=9 HTTP/1.1\r\n\r\n")
	if want := head("200 OK", "application/javascript; charset=utf-8", 28) + `/**/dg_home1({"version":1});`; resp != want {
		t.Errorf("JSONP:\n%q\nwant\n%q", resp, want)
	}
	resp, log = roundTrip(t, h, "GET "+homePath+"?callback=alert(1) HTTP/1.1\r\n\r\n")
	if !strings.HasPrefix(resp, "HTTP/1.1 400 Bad Request\r\n") || strings.Contains(resp, "alert") ||
		!strings.Contains(log, "-> 400 Bad Request") {
		t.Errorf("a bad callback: %q, log %q", resp, log)
	}
	if calls != 5 {
		t.Errorf("Home called %d times, want once per request (5)", calls)
	}
	// without a Home hook the path is an unknown file
	resp, _ = roundTrip(t, &HTTP{}, "GET "+homePath+" HTTP/1.1\r\n\r\n")
	if !strings.HasPrefix(resp, "HTTP/1.1 404 Not Found\r\n") {
		t.Errorf("no hook: %q", resp)
	}
}

func TestHomeCallback(t *testing.T) {
	for _, c := range []struct {
		target, name string
		ok           bool
	}{
		{"/x/common/home.json", "", true},
		{"/x/common/home.json?t=1", "", true},
		{"/x/common/home.json?callback=f", "f", true},
		{"/x/common/home.json?cb=On_Home_2", "On_Home_2", true},
		{"/x/common/home.json?callback=a&cb=b", "a", true},
		{"/x/common/home.json?callback=f#frag", "f", true},
		{"/x/common/home.json?callback=", "", false},
		{"/x/common/home.json?callback=a.b", "", false},
		{"/x/common/home.json?callback=%3Cscript%3E", "", false},
		{"/x/common/home.json?callback=" + strings.Repeat("a", 65), "", false},
		{"/x/common/home.json?callback=%zz", "", false},
	} {
		if name, ok := homeCallback(c.target); name != c.name || ok != c.ok {
			t.Errorf("%s: %q %v, want %q %v", c.target, name, ok, c.name, c.ok)
		}
	}
}

func TestHomeLogForgets(t *testing.T) {
	var l homeLog
	t0 := time.Unix(1000, 0)
	if !l.first("a", t0) || l.first("a", t0.Add(time.Minute)) || !l.first("b", t0) {
		t.Fatal("first per address")
	}
	if !l.first("a", t0.Add(homeLogEvery)) {
		t.Error("not logged again after homeLogEvery")
	}
	for i := range 3000 {
		l.first(strconv.Itoa(i), t0)
		if len(l.last) > homeLogAddresses {
			t.Fatalf("%d addresses remembered", len(l.last))
		}
	}
}
