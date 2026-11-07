package web

import "testing"

// A request head is split at CR LF, so a bare LF stayed inside a line and made a line of its own in the log: anyone could write
// a log line that looks like the server's.
func TestOneLine(t *testing.T) {
	if got := oneLine("GET / HTTP/1.1\n12:00:00 [stub] the server stopped\r"); got != "GET / HTTP/1.1?12:00:00 [stub] the server stopped?" {
		t.Errorf("%q", got)
	}
}
