package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A client decides how many frames are logged and how long they are, so a public server's log is bounded: a message is cut, a
// flood is dropped and counted, and the files on the disk stay under their total however long it goes on.
func TestLogLimits(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.SetConsole(nil)
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return clock }
	l.SetLimits(Limits{MaxMessage: 100, BytesPerS: 1000, Burst: 2000, FileBytes: 500, TotalBytes: 2000})

	l.Log("x", strings.Repeat("a", 1000))
	if b, _ := os.ReadFile(l.Path); !strings.Contains(string(b), "bytes cut") || len(b) > 300 {
		t.Errorf("a long message: %d bytes", len(b))
	}

	// at most the burst now: the rest is dropped, and one line says so later
	for range 1000 {
		l.Log("x", "flood")
	}
	if l.dropped == 0 {
		t.Error("nothing dropped by the rate")
	}
	clock = clock.Add(time.Minute)
	l.Log("x", "after")
	var all strings.Builder
	files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		all.Write(b)
	}
	if !strings.Contains(all.String(), "lines dropped") || !strings.Contains(all.String(), "after") {
		t.Errorf("no note of the dropped lines:\n%s", all.String())
	}

	// a whole day's flood: new files, and the oldest go
	for i := range 2000 {
		clock = clock.Add(time.Second)
		l.Log("x", strings.Repeat("b", 90)+string(rune('0'+i%10)))
	}
	var total int64
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		st, _ := e.Info()
		total += st.Size()
	}
	if total > 2600 { // the total, with the file being written
		t.Errorf("%d bytes on the disk in %d files", total, len(entries))
	}
	if len(entries) < 2 {
		t.Errorf("no new file: %d", len(entries))
	}
	if !strings.HasPrefix(filepath.Dir(l.Path), dir) {
		t.Errorf("the log moved to %s", l.Path)
	}
}

// A log without limits, as every lane's, writes everything.
func TestNoLimitsByDefault(t *testing.T) {
	var b strings.Builder
	l := New(&b)
	l.Log("x", strings.Repeat("a", 1<<20))
	if b.Len() < 1<<20 {
		t.Errorf("%d bytes", b.Len())
	}
}
