package dgserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

func TestStatusTexts(t *testing.T) {
	at := time.Date(2026, 10, 21, 18, 30, 0, 0, time.UTC)
	for got, want := range map[string]string{
		onlineText("v1.2_3"):      "**Server online** (version v1\\.2\\_3)",
		onlineText("dev"):         "**Server online** (version dev)",
		backOnlineText("dev", at): "**Server back online** (version dev): it stopped unexpectedly around <t:1792607400:f>",
		goingDownText(0):          "**Server going down** (no match running)",
		goingDownText(1):          "**Server going down** (1 match cut short)",
		goingDownText(3):          "**Server going down** (3 matches cut short)",
	} {
		if got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	}
}

func testLog(t *testing.T) *logging.Log {
	t.Helper()
	l, err := logging.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// The marker: written at the start (pid, start, version, heartbeat), refreshed by the heartbeat, removed at a clean
// stop; one left behind makes the next start's post "back online" with its last heartbeat.
func TestStatusMarker(t *testing.T) {
	dir := t.TempDir()
	log := testLog(t)
	t0 := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	now := t0
	st := startStatus(dir, nil, log, "dev", func() time.Time { return now })
	if st.prev != nil {
		t.Fatalf("a first start found a marker: %+v", st.prev)
	}
	m := readMarker(dir)
	if m == nil || m.Pid != os.Getpid() || m.Version != "dev" || !m.Start.Equal(t0) || !m.Heartbeat.Equal(t0) {
		t.Fatalf("marker %+v", m)
	}
	now = t0.Add(3 * time.Minute)
	st.beat()
	if m = readMarker(dir); m == nil || !m.Heartbeat.Equal(now) || !m.Start.Equal(t0) {
		t.Fatalf("after a heartbeat: %+v", m)
	}

	// a crash: the marker stays; the next start finds it
	st2 := startStatus(dir, nil, log, "dev", func() time.Time { return t0.Add(time.Hour) })
	if st2.prev == nil || !st2.prev.lastSeen().Equal(now) {
		t.Fatalf("the next start: prev %+v", st2.prev)
	}
	st.down(nil, 0, time.Second) // stops the first one's heartbeat (its marker is already the second's)
	st2.down(nil, 0, time.Second)
	if readMarker(dir) != nil {
		t.Error("the marker is still there after a clean stop")
	}
	st3 := startStatus(dir, nil, log, "dev", time.Now)
	if st3.prev != nil {
		t.Errorf("after a clean stop: prev %+v", st3.prev)
	}
	st3.down(nil, 0, time.Second)

	// an unreadable marker counts as found, its file time as the last sign of life
	os.WriteFile(filepath.Join(dir, MarkerName), []byte("{half"), 0o644)
	if m := readMarker(dir); m == nil || m.lastSeen().IsZero() {
		t.Errorf("unreadable marker: %+v", m)
	}
	if readMarker("") != nil {
		t.Error("a marker without a data folder")
	}
	// the heartbeat goroutine refreshes by itself
	os.Remove(filepath.Join(dir, MarkerName))
	st4 := &serverStatus{dir: dir, log: log, now: time.Now, stop: make(chan struct{}), done: make(chan struct{})}
	go st4.heartbeat(10 * time.Millisecond)
	deadline := time.Now().Add(5 * time.Second)
	for readMarker(dir) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	st4.down(nil, 0, 0)
	if readMarker(dir) != nil {
		t.Error("the heartbeat didn't write, or down didn't remove the marker")
	}
}

// fakeDiscord records the posts per webhook path.
type fakeDiscord struct {
	srv   *httptest.Server
	mu    sync.Mutex
	posts map[string][]string
	got   chan struct{}
}

func newFakeDiscord(t *testing.T) *fakeDiscord {
	f := &fakeDiscord{posts: map[string][]string{}, got: make(chan struct{}, 16)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m struct {
			Content string `json:"content"`
		}
		json.Unmarshal(b, &m)
		f.mu.Lock()
		f.posts[r.URL.Path] = append(f.posts[r.URL.Path], r.Method+" "+m.Content)
		f.mu.Unlock()
		io.WriteString(w, `{"id":"1"}`)
		f.got <- struct{}{}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDiscord) list(path string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts[path]...)
}

// A whole run: a marker left by a crash, the start posts "back online" to the server's webhook only (never the
// default one), the clean stop "going down" and removes the marker.
func TestStatusRun(t *testing.T) {
	fd := newFakeDiscord(t)
	srvPath, defPath := "/api/webhooks/111/"+hookToken, "/api/webhooks/222/"+hookToken
	data := t.TempDir()
	crashed := time.Date(2026, 10, 21, 17, 59, 0, 0, time.UTC)
	if err := writeMarker(data, runMarker{Pid: 4242, Start: crashed.Add(-time.Hour), Version: "dev", Heartbeat: crashed}); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	stop, started, done := make(chan struct{}), make(chan Info, 1), make(chan int, 1)
	go func() {
		done <- Run([]string{"--bind", "127.0.9.71", "--certs", testCerts(t), "--log", logDir, "--data", data, "--broadcast-file", "",
			"--plain", "off", "--tunnel", "on", "--gateway-port", "0", "--test-discord-local",
			"--discord-webhook-server", fd.srv.URL + srvPath, "--discord-webhook", fd.srv.URL + defPath},
			Hooks{Stop: stop, Quiet: true, Started: func(i Info) { started <- i }, StartFailed: func(error) { started <- Info{} }})
	}()
	info := <-started
	if info.Log == "" {
		t.Fatal("dgserver didn't start")
	}
	select {
	case <-fd.got:
	case <-time.After(10 * time.Second):
		t.Fatal("no post")
	}
	if m := readMarker(data); m == nil || m.Pid != os.Getpid() {
		t.Errorf("this run's marker: %+v", m)
	}
	close(stop)
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("dgserver didn't stop")
	}
	want := []string{"POST " + backOnlineText(info.Version, crashed), "POST " + goingDownText(0)}
	if got := fd.list(srvPath); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the server's webhook got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := fd.list(defPath); len(got) != 0 {
		t.Errorf("the default webhook got the status: %v", got)
	}
	if readMarker(data) != nil {
		t.Error("the marker is still there after a clean stop")
	}
	b, _ := os.ReadFile(info.Log)
	if log := string(b); strings.Contains(log, hookToken) || !strings.Contains(log, "the last run didn't stop cleanly") ||
		!strings.Contains(log, "server on (local test receiver") {
		t.Errorf("log:\n%s", log)
	}
}
