package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The item icons: a listed file at the path's suffix (any DRC or locale), cacheable with an ETag (304 for a match);
// a name not listed, a path out of the folder, a sub-folder or a missing file is 404 and logged; one log line per
// client address otherwise; without ItemIcons the paths fall through to the files and built-ins.
func TestItemIconsServed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Basic_Spark.png"), []byte("\x89PNG fake"), 0o644)
	os.WriteFile(filepath.Join(dir, "Secret.png"), []byte("not listed"), 0o644)
	ic := &ItemIcons{Dir: dir, Names: map[string]bool{"Basic_Spark.png": true, "Gone.png": true}}
	h := &HTTP{ItemIcons: ic}
	resp, log := roundTrip(t, h, "GET http://127.0.0.1:18080/x/y/1/common/itemicons/Basic_Spark.png?r=1 HTTP/1.1\r\n\r\n")
	const etag = `"` // the first 8 bytes of the file's SHA-256 in hex, quoted
	if !strings.HasPrefix(resp, "HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nContent-Length: 9\r\n") ||
		!strings.Contains(resp, "Cache-Control: public, max-age=86400\r\nETag: "+etag) || !strings.HasSuffix(resp, "\r\n\r\n\x89PNG fake") {
		t.Errorf("icon: %q", resp)
	}
	if !strings.Contains(log, "-> 200 OK, 9 bytes (item icon;") {
		t.Errorf("log %q", log)
	}
	tag := resp[strings.Index(resp, "ETag: ")+6:]
	tag = tag[:strings.Index(tag, "\r\n")]
	if len(tag) != 18 {
		t.Errorf("ETag %q", tag)
	}
	resp, log = roundTrip(t, h, "GET /"+webContentRoot+ItemIconsPart+"Basic_Spark.png HTTP/1.1\r\nIf-None-Match: "+tag+"\r\n\r\n")
	if !strings.HasPrefix(resp, "HTTP/1.1 304 Not Modified\r\n") || !strings.HasSuffix(resp, "Connection: close\r\n\r\n") || log != "" {
		t.Errorf("revalidation: %q, log %q", resp, log)
	}
	for _, p := range []string{"Secret.png", "Gone.png", "..%2Fcontent.json", "sub/Basic_Spark.png", `..\content.json`, ""} {
		resp, log := roundTrip(t, h, "GET /"+webContentRoot+ItemIconsPart+p+" HTTP/1.1\r\n\r\n")
		if !strings.HasPrefix(resp, "HTTP/1.1 404 Not Found\r\n") || strings.Contains(resp, "not listed") {
			t.Errorf("%q: %q", p, resp)
		}
		if !strings.Contains(log, "404 Not Found") {
			t.Errorf("%q: a refusal is logged: %q", p, log)
		}
	}
	resp, _ = roundTrip(t, h, "HEAD /"+webContentRoot+ItemIconsPart+"Basic_Spark.png HTTP/1.1\r\n\r\n")
	if !strings.HasSuffix(resp, "Connection: close\r\n\r\n") || !strings.Contains(resp, "Content-Length: 9\r\n") {
		t.Errorf("HEAD: %q", resp)
	}
	resp, _ = roundTrip(t, &HTTP{}, "GET /"+webContentRoot+ItemIconsPart+"Basic_Spark.png HTTP/1.1\r\n\r\n")
	if !strings.HasPrefix(resp, "HTTP/1.1 404 Not Found\r\n") {
		t.Errorf("no ItemIcons: %q", resp)
	}
}
