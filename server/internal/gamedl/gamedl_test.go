package gamedl

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// zipBytes is a small zip archive with one file of n bytes of the given seed (stored, so its size is about n).
func zipBytes(t *testing.T, seed string, n int) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.CreateHeader(&zip.FileHeader{Name: "Dawngate/game/data.bin", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte(strings.Repeat(seed, n/len(seed)+1)[:n]))
	w.Close()
	return b.Bytes()
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// server serves body at /Dawngate.zip as a mirror would; the mode changes how.
type server struct {
	*httptest.Server
	mu     sync.Mutex
	body   []byte
	cutAt  int  // >0: the first request sends this many bytes (announcing all), then drops the connection
	noRang bool // ignore Range (always 200 and the whole file)
	stall  bool // send the header and a few bytes, then nothing
	ranges []string
}

func newServer(t *testing.T, body []byte) *server {
	s := &server{body: body}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.ranges = append(s.ranges, r.Header.Get("Range"))
		cut := s.cutAt
		s.cutAt = 0
		s.mu.Unlock()
		if r.URL.Path != "/Dawngate.zip" {
			http.NotFound(w, r)
			return
		}
		switch {
		case cut > 0:
			w.Header().Set("Content-Length", strconv.Itoa(len(s.body)))
			w.WriteHeader(http.StatusOK)
			w.Write(s.body[:cut])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler) // the connection is cut
		case s.stall:
			w.Header().Set("Content-Length", strconv.Itoa(len(s.body)))
			w.WriteHeader(http.StatusOK)
			w.Write(s.body[:10])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case s.noRang:
			w.Header().Set("Content-Length", strconv.Itoa(len(s.body)))
			w.Write(s.body)
		default:
			http.ServeContent(w, r, "Dawngate.zip", time.Time{}, bytes.NewReader(s.body))
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) url() string { return s.URL + "/Dawngate.zip" }

func (s *server) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ranges...)
}

// downloader into a temp folder; the check accepts only want (by SHA-256, as Start-Client's known download).
func downloader(t *testing.T, want []byte, sources ...string) (Downloader, *bytes.Buffer) {
	var log bytes.Buffer
	d := Downloader{
		Sources: sources,
		Dest:    filepath.Join(t.TempDir(), "Dawngate.zip"),
		Check: func(path string, r Result) error {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if sum(b) != r.SHA256 || int64(len(b)) != r.Size {
				t.Errorf("the result %+v doesn't describe the file (%d bytes, %s)", r, len(b), sum(b))
			}
			if r.SHA256 != sum(want) {
				return errors.New("its files differ")
			}
			return nil
		},
		Say: func(f string, a ...any) { fmt.Fprintf(&log, f+"\n", a...) },
	}
	d.Idle = 2 * time.Second
	return d, &log
}

func checkDest(t *testing.T, d Downloader, want []byte) {
	t.Helper()
	b, err := os.ReadFile(d.Dest)
	if err != nil || !bytes.Equal(b, want) {
		t.Errorf("%s: %d bytes, %v; want the %d of the source", d.Dest, len(b), err, len(want))
	}
	if _, err := os.Stat(d.Dest + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the .part file is still there (%v)", err)
	}
}

func TestFullDownload(t *testing.T) {
	body := zipBytes(t, "dawngate", 300_000)
	s := newServer(t, body)
	d, log := downloader(t, body, s.url())
	var last Progress
	calls := 0
	d.Progress = func(p Progress) { last = p; calls++ }
	src, err := d.Run(context.Background())
	if err != nil || src != s.url() {
		t.Fatalf("Run: %q, %v\n%s", src, err, log)
	}
	checkDest(t, d, body)
	if last.Done != int64(len(body)) || last.Total != int64(len(body)) || last.Resumed != 0 || calls < 2 {
		t.Errorf("progress %+v after %d calls", last, calls)
	}
	if r := s.requests(); len(r) != 1 || r[0] != "" {
		t.Errorf("requests %q", r)
	}
}

func TestResumeAfterCut(t *testing.T) {
	body := zipBytes(t, "resume", 500_000)
	s := newServer(t, body)
	s.cutAt = 200_000
	d, log := downloader(t, body, s.url())
	if _, err := d.Run(context.Background()); err == nil {
		t.Fatal("a cut download succeeded")
	}
	st, err := os.Stat(d.Dest + ".part")
	if err != nil || st.Size() != 200_000 {
		t.Fatalf("the .part file after the cut: %v %v", st, err)
	}
	if _, err := os.Stat(d.Dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a cut download became Dawngate.zip")
	}
	var first Progress
	d.Progress = func(p Progress) {
		if first.Done == 0 {
			first = p
		}
	}
	if _, err := d.Run(context.Background()); err != nil {
		t.Fatalf("the resumed Run: %v\n%s", err, log)
	}
	checkDest(t, d, body)
	if r := s.requests(); len(r) != 2 || r[1] != "bytes=200000-" {
		t.Errorf("requests %q", r)
	}
	if first.Resumed != 200_000 || first.Total != int64(len(body)) {
		t.Errorf("the resumed try's first progress %+v", first)
	}
}

func TestRangelessServerStartsOver(t *testing.T) {
	body := zipBytes(t, "norange", 300_000)
	s := newServer(t, body)
	s.noRang = true
	d, log := downloader(t, body, s.url())
	os.WriteFile(d.Dest+".part", body[:100_000], 0o644)
	if _, err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v\n%s", err, log)
	}
	checkDest(t, d, body)
	if !strings.Contains(log.String(), "it can't continue a download: starting over") {
		t.Errorf("no note about the restart:\n%s", log)
	}
	if r := s.requests(); len(r) != 1 || r[0] != "bytes=100000-" {
		t.Errorf("requests %q", r)
	}
}

func TestWrongFileSkippedNextSourceUsed(t *testing.T) {
	body := zipBytes(t, "right", 200_000)
	other := newServer(t, zipBytes(t, "wrong", 200_000)) // a zip, but another one: fails the check
	page := newServer(t, []byte("<!DOCTYPE html><html>The item is not available</html>"))
	missing := newServer(t, body)
	good := newServer(t, body)
	d, log := downloader(t, body, other.url(), page.url(), missing.URL+"/gone.zip", good.url())
	src, err := d.Run(context.Background())
	if err != nil || src != good.url() {
		t.Fatalf("Run: %q, %v\n%s", src, err, log)
	}
	checkDest(t, d, body)
	out := log.String()
	for _, want := range []string{
		other.url() + " gave a file that isn't build 854's Dawngate.zip (its files differ); skipping it",
		page.url() + ` gave a file that isn't build 854's Dawngate.zip (it isn't a zip archive (it begins "<!DOCTYPE html><html>The"))`,
		missing.URL + "/gone.zip: 404 Not Found",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no line %q in:\n%s", want, out)
		}
	}
	// every source wrong: the error names them all, nothing is left behind
	d, log = downloader(t, body, other.url(), page.url())
	if _, err := d.Run(context.Background()); err == nil || !strings.Contains(err.Error(), other.url()) ||
		!strings.Contains(err.Error(), page.url()) {
		t.Errorf("Run: %v\n%s", err, log)
	}
	for _, p := range []string{d.Dest, d.Dest + ".part"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left behind", p)
		}
	}
}

// A part from a source that later turned out wrong: the next source continues it, the check fails, and the file is
// fetched once more from the start.
func TestResumedPartFailingTheCheckStartsOver(t *testing.T) {
	body := zipBytes(t, "good", 300_000)
	good := newServer(t, body)
	d, log := downloader(t, body, good.url())
	bad := zipBytes(t, "bad!", 300_000)
	os.WriteFile(d.Dest+".part", bad[:150_000], 0o644)
	if _, err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v\n%s", err, log)
	}
	checkDest(t, d, body)
	if r := good.requests(); len(r) != 2 || r[0] != "bytes=150000-" || r[1] != "" {
		t.Errorf("requests %q", r)
	}
	if !strings.Contains(log.String(), "the continued download fails the check") {
		t.Errorf("log:\n%s", log)
	}
}

func TestCompletePartAndRefusedRange(t *testing.T) {
	body := zipBytes(t, "whole", 100_000)
	s := newServer(t, body)
	d, log := downloader(t, body, s.url())
	os.WriteFile(d.Dest+".part", body, 0o644) // complete already: the server answers 416
	if _, err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v\n%s", err, log)
	}
	checkDest(t, d, body)
	// a part longer than the file: 416 without a fit, so it starts over
	os.Remove(d.Dest)
	os.WriteFile(d.Dest+".part", append(append([]byte(nil), body...), "more"...), 0o644)
	if _, err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v\n%s", err, log)
	}
	checkDest(t, d, body)
}

func TestNoSpace(t *testing.T) {
	body := zipBytes(t, "space", 10_000)
	s := newServer(t, body)
	d, _ := downloader(t, body, s.url())
	d.Size, d.Need = 1310588859, 3<<30
	var asked string
	d.Free = func(dir string) (uint64, error) { asked = dir; return 2 << 30, nil }
	_, err := d.Run(context.Background())
	if !errors.Is(err, ErrNoSpace) || !strings.Contains(err.Error(), "2.0 GB free, 4.2 GB needed") {
		t.Errorf("Run: %v", err)
	}
	if asked != filepath.Dir(d.Dest) || len(s.requests()) != 0 {
		t.Errorf("asked %q, requests %q", asked, s.requests())
	}
	// a part already there counts
	os.WriteFile(d.Dest+".part", body[:5000], 0o644)
	d.Size, d.Need = 5000+(1<<30), 1<<30
	if _, err := d.Run(context.Background()); err != nil {
		t.Errorf("2 GB free for 1 GB to go and 1 GB of copies: %v", err)
	}
}

func TestStalledSource(t *testing.T) {
	body := zipBytes(t, "stall", 100_000)
	s := newServer(t, body)
	s.stall = true
	d, _ := downloader(t, body, s.url())
	d.Idle = 300 * time.Millisecond
	t0 := time.Now()
	_, err := d.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nothing arrived for 300ms") || time.Since(t0) > 10*time.Second {
		t.Errorf("Run: %v after %v", err, time.Since(t0))
	}
	if st, err := os.Stat(d.Dest + ".part"); err != nil || st.Size() != 10 {
		t.Errorf("the part of a stalled source isn't kept for the next try: %v %v", st, err)
	}
}

func TestHelpers(t *testing.T) {
	for _, c := range []struct {
		v           string
		start, size int64
		ok          bool
	}{{"bytes 100-199/200", 100, 200, true}, {"bytes */300", -1, 300, true}, {"bytes 1-2", 0, 0, false}, {"items 1-2/3", 0, 0, false}} {
		if s, n, ok := contentRange(c.v); s != c.start || n != c.size || ok != c.ok {
			t.Errorf("contentRange(%q) = %d %d %v", c.v, s, n, ok)
		}
	}
	for _, u := range []string{"http://127.0.0.1:8000/Dawngate.zip", DefaultSource} {
		if err := CheckURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"ftp://x/Dawngate.zip", "C:\\Dawngate.zip", "https:///x", ""} {
		if CheckURL(u) == nil {
			t.Errorf("%q accepted", u)
		}
	}
	if n := Name("https://user:pw@mirror.example/d/Dawngate.zip?token=1"); n != "https://mirror.example/d/Dawngate.zip" {
		t.Errorf("Name %q", n)
	}
	if !isZipStart([]byte("PK")) || !isZipStart([]byte("PK\x03\x04rest")) || isZipStart([]byte("<html")) {
		t.Error("isZipStart")
	}
}
