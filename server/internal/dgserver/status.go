package dgserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dawngate/server/internal/discordhook"
	"dawngate/server/internal/logging"
)

// The server's status in Discord (--discord-webhook-server): "online" after the start, "going down" at a clean stop
// and, when the last run didn't stop cleanly, "back online" with the time it was last seen alive. The clean-stop
// marker is <data>\running.json: written at the start, its heartbeat refreshed every minute, removed at a clean stop
// (Ctrl+C, the console closed, Start-Server's stop or update, --seconds). A marker found at the start means the last
// run ended otherwise (a crash, a kill, a power cut); its heartbeat is when, give or take a minute. The marker is kept
// with or without a webhook (the log says so too).

// MarkerName is the clean-stop marker's file in the data folder.
const MarkerName = "running.json"

// heartbeatEvery is how often the running server refreshes its marker.
const heartbeatEvery = time.Minute

// runMarker is the content of running.json.
type runMarker struct {
	Pid       int       `json:"pid"`
	Start     time.Time `json:"start"`
	Version   string    `json:"version"`
	Heartbeat time.Time `json:"heartbeat"`
}

// lastSeen is when the run was last known alive (its heartbeat, else its start).
func (m runMarker) lastSeen() time.Time {
	if m.Heartbeat.After(m.Start) {
		return m.Heartbeat
	}
	return m.Start
}

// readMarker reads dir's marker: nil without one (or without a data folder); an unreadable one counts as found
// (the run that wrote it didn't stop cleanly either), with its file time as the last sign of life.
func readMarker(dir string) *runMarker {
	if dir == "" {
		return nil
	}
	p := filepath.Join(dir, MarkerName)
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var m runMarker
	if err != nil || json.Unmarshal(b, &m) != nil || m.lastSeen().IsZero() {
		m = runMarker{}
		if fi, err := os.Stat(p); err == nil {
			m.Heartbeat = fi.ModTime()
		}
	}
	return &m
}

// writeMarker writes dir's marker (a new file renamed over the old one, so a crash never leaves half a file).
func writeMarker(dir string, m runMarker) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, MarkerName)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// The three status texts. The version is escaped (it is only ever ours, but a "_" in it would turn italic).
func onlineText(version string) string {
	return fmt.Sprintf("**Server online** (version %s)", discordhook.Escape(version))
}

func backOnlineText(version string, lastSeen time.Time) string {
	return fmt.Sprintf("**Server back online** (version %s): it stopped unexpectedly around %s", discordhook.Escape(version),
		discordhook.Timestamp(lastSeen, "f"))
}

func goingDownText(matches int) string {
	switch matches {
	case 0:
		return "**Server going down** (no match running)"
	case 1:
		return "**Server going down** (1 match cut short)"
	}
	return fmt.Sprintf("**Server going down** (%d matches cut short)", matches)
}

// serverStatus is the running server's marker and status posts.
type serverStatus struct {
	dir     string
	hook    *discordhook.Hook // nil: no --discord-webhook-server
	log     *logging.Log
	version string
	prev    *runMarker // the last run's marker: it didn't stop cleanly (nil: it did, or there was none)
	now     func() time.Time

	mu     sync.Mutex
	marker runMarker
	stop   chan struct{}
	done   chan struct{}
}

// startStatus reads the last run's marker, writes this run's and starts its heartbeat (all only with a data folder).
func startStatus(dir string, hook *discordhook.Hook, log *logging.Log, version string, now func() time.Time) *serverStatus {
	st := &serverStatus{dir: dir, hook: hook, log: log, version: version, now: now, prev: readMarker(dir)}
	if st.prev != nil {
		log.Logf("stub", "the last run didn't stop cleanly (%s found): last seen %s%s", MarkerName,
			st.prev.lastSeen().Format(time.RFC3339), describeMarker(*st.prev))
	}
	if dir == "" {
		return st
	}
	t := now()
	st.marker = runMarker{Pid: os.Getpid(), Start: t, Version: version, Heartbeat: t}
	if err := writeMarker(dir, st.marker); err != nil {
		log.Logf("stub", "the clean-stop marker %s can't be written: %v", MarkerName, err)
		return st
	}
	st.stop, st.done = make(chan struct{}), make(chan struct{})
	go st.heartbeat(heartbeatEvery)
	return st
}

func describeMarker(m runMarker) string {
	if m.Pid == 0 {
		return ""
	}
	return fmt.Sprintf(" (pid %d, version %s, started %s)", m.Pid, m.Version, m.Start.Format(time.RFC3339))
}

func (st *serverStatus) heartbeat(every time.Duration) {
	defer close(st.done)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-st.stop:
			return
		case <-t.C:
			st.beat()
		}
	}
}

// beat refreshes the marker's heartbeat.
func (st *serverStatus) beat() {
	st.mu.Lock()
	st.marker.Heartbeat = st.now()
	m := st.marker
	st.mu.Unlock()
	if err := writeMarker(st.dir, m); err != nil {
		st.log.Logf("stub", "the clean-stop marker's heartbeat: %v", err)
	}
}

// online posts "online", or "back online" after a run that didn't stop cleanly.
func (st *serverStatus) online() {
	text := onlineText(st.version)
	if st.prev != nil {
		text = backOnlineText(st.version, st.prev.lastSeen())
	}
	st.hook.Post(discordhook.Message{Content: text})
}

// down is the clean stop: it posts "going down" with the running matches and gives the posts still queued (on every
// webhook) flush to go out, then removes the marker.
func (st *serverStatus) down(hooks *discordhook.Hooks, matches int, flush time.Duration) {
	st.hook.Post(discordhook.Message{Content: goingDownText(matches)})
	hooks.Close(flush)
	if st.stop != nil {
		close(st.stop)
		<-st.done
		st.stop = nil
		if err := os.Remove(filepath.Join(st.dir, MarkerName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			st.log.Logf("stub", "the clean-stop marker %s can't be removed: %v", MarkerName, err)
		}
	}
}
