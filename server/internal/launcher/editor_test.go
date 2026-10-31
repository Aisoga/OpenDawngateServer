package launcher

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/mapedit"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/moviepatch"
)

// fakeProcess is a started game for the editor's tests: it exits when closed (unless it ignores WM_CLOSE, as the game
// in a match does) or killed.
type fakeProcess struct {
	pid        int
	ignore     bool // Close does nothing (only Kill ends it)
	mu         sync.Mutex
	closes     int
	kills      int
	exit       chan uint32
	once       sync.Once
	parked     bool
	released   bool
	parkStoped chan struct{}
}

func newFakeProcess(pid int) *fakeProcess {
	return &fakeProcess{pid: pid, exit: make(chan uint32, 1), parkStoped: make(chan struct{})}
}

func (g *fakeProcess) end(code uint32) { g.once.Do(func() { g.exit <- code; close(g.exit) }) }
func (g *fakeProcess) ID() int         { return g.pid }
func (g *fakeProcess) Wait() (uint32, error) {
	return <-g.exit, nil
}
func (g *fakeProcess) Exited(d time.Duration) bool {
	time.Sleep(10 * time.Millisecond)
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.ignore
}
func (g *fakeProcess) Close() {
	g.mu.Lock()
	g.closes++
	ignore := g.ignore
	g.mu.Unlock()
	if !ignore {
		g.end(0)
	}
}
func (g *fakeProcess) Kill() error {
	g.mu.Lock()
	g.kills++
	g.mu.Unlock()
	g.end(1)
	return nil
}
func (g *fakeProcess) Release() {
	g.mu.Lock()
	g.released = true
	g.mu.Unlock()
}
func (g *fakeProcess) Park(stop <-chan struct{}, prev uintptr, ev ParkEvents) {
	g.mu.Lock()
	g.parked = true
	g.mu.Unlock()
	<-stop
	close(g.parkStoped)
}
func (g *fakeProcess) counts() (int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closes, g.kills
}

// fakeEditorPC is This PC for the editor's tests.
type fakeEditorPC struct {
	mu       sync.Mutex
	maps     []string
	restarts int
	held     bool
}

func (p *fakeEditorPC) SetMap(name string) { p.mu.Lock(); p.maps = append(p.maps, name); p.mu.Unlock() }
func (p *fakeEditorPC) Restart()           { p.mu.Lock(); p.restarts++; p.mu.Unlock() }
func (p *fakeEditorPC) folderServer() (setup.RunLock, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return setup.RunLock{Program: "Start-Server", Pid: 4242}, p.held
}

// fakeEditorList is the front for the editor's tests.
type fakeEditorList struct {
	mu      sync.Mutex
	paused  bool
	pauses  int
	resumes int
	maps    []moviepatch.Map
	inSetup bool     // the setup ran while the list was paused
	ended   []string // EndSession's tags
}

func (l *fakeEditorList) PauseList() func() error {
	l.mu.Lock()
	l.paused = true
	l.pauses++
	l.mu.Unlock()
	return func() error {
		l.mu.Lock()
		l.paused = false
		l.resumes++
		l.mu.Unlock()
		return nil
	}
}
func (l *fakeEditorList) SetMaps(m []moviepatch.Map) { l.mu.Lock(); l.maps = m; l.mu.Unlock() }
func (l *fakeEditorList) EndSession(tag string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ended = append(l.ended, tag)
	return true
}

// editorRig is an editorGame with fakes: setupLine is the tested map's line the fake setup reports.
type editorRig struct {
	t         *testing.T
	e         *editorGame
	pc        *fakeEditorPC
	list      *fakeEditorList
	console   syncBuffer
	mu        sync.Mutex
	games     []*fakeProcess
	setupLine setup.Status
	setupErr  bool
	gate      chan struct{} // the fake setup waits for it (nil: no wait)
}

func newEditorRig(t *testing.T) *editorRig {
	r := &editorRig{t: t, pc: &fakeEditorPC{}, list: &fakeEditorList{}, setupLine: setup.Done}
	r.e = &editorGame{out: &r.console, pc: r.pc, list: r.list, park: true,
		start: func() (gameProcess, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			g := newFakeProcess(100 + len(r.games))
			r.games = append(r.games, g)
			return g, nil
		},
		setup: func(out io.Writer) (*setup.Setup, error) {
			r.mu.Lock()
			gate, st, failErr := r.gate, r.setupLine, r.setupErr
			r.mu.Unlock()
			if gate != nil {
				<-gate
			}
			r.list.mu.Lock()
			r.list.inSetup = r.list.paused
			r.list.mu.Unlock()
			s := &setup.Setup{Out: out}
			s.Report(setup.OK, "client exe", "current")
			s.Report(setup.Skipped, "client CM_Other", "left out: its build fails: a test")
			if failErr {
				s.Report(setup.Failed, "client config", "a test failure")
				return s, setup.ErrIncomplete
			}
			s.Report(st, "client CM_Test", "the map CM_Test")
			return s, nil
		},
		dialogMaps: func() []moviepatch.Map { return []moviepatch.Map{{Tag: "test", Archive: "zz_CM_Test.snap"}} }}
	return r
}

func (r *editorRig) request(checkErr error) mapedit.TestRequest {
	return mapedit.TestRequest{Pkg: "CM_Test", Dir: "maps/CM_Test", Map: "CM_Test",
		Check: func() ([]byte, error) { return []byte(`{}`), checkErr }}
}

// wait waits until the status has the phase (and running state), failing after 5 s.
func (r *editorRig) wait(phase string) *mapedit.TestStatus {
	r.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; {
		st := r.e.Status()
		if st != nil && st.Phase == phase {
			return st
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("no phase %q: %+v\n%s", phase, st, r.console.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *editorRig) game(i int) *fakeProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.games) {
		r.t.Fatalf("no game %d (%d started)", i, len(r.games))
	}
	return r.games[i]
}

// TestEditorGameSequence: a Test's steps in order (check, This PC's map and restart, the setup with the list paused,
// the dialog's maps, the game parked), a second Test closing the first game and starting another, Stop, the status
// copies and the lines.
func TestEditorGameSequence(t *testing.T) {
	r := newEditorRig(t)
	if r.e.Status() != nil {
		t.Fatal("a status before the first Test")
	}
	if err := r.e.Stop(false); !errors.Is(err, mapedit.ErrNoTest) {
		t.Errorf("Stop before a Test: %v", err)
	}
	if err := r.e.Test(r.request(nil)); err != nil {
		t.Fatal(err)
	}
	st := r.wait(testPhasePlaying)
	if !st.Running || st.Name != "CM_Test-1" || st.Map != "CM_Test" || st.Pkg != "CM_Test" || st.Error != "" {
		t.Errorf("playing: %+v", st)
	}
	r.pc.mu.Lock()
	maps, restarts := append([]string(nil), r.pc.maps...), r.pc.restarts
	r.pc.mu.Unlock()
	if strings.Join(maps, ",") != "CM_Test" || restarts != 1 {
		t.Errorf("This PC: maps %v, %d restarts", maps, restarts)
	}
	r.list.mu.Lock()
	if r.list.pauses != 1 || r.list.resumes != 1 || !r.list.inSetup || len(r.list.maps) != 1 || strings.Join(r.list.ended, ",") != ThisPCTag {
		t.Errorf("the list: %+v", r.list)
	}
	r.list.mu.Unlock()
	g1 := r.game(0)
	for deadline := time.Now().Add(time.Second); ; time.Sleep(5 * time.Millisecond) {
		g1.mu.Lock()
		parked := g1.parked
		g1.mu.Unlock()
		if parked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the game wasn't parked")
		}
	}
	text := strings.Join(st.Lines, "\n")
	for _, want := range []string{"mapc check of the saved package", "the next login starts it again with --map CM_Test",
		"[skipped]  client CM_Other left out: its build fails", "[done]     client CM_Test", "the game runs (pid 100)"} {
		if !strings.Contains(text, want) {
			t.Errorf("the lines lack %q:\n%s", want, text)
		}
	}
	if !strings.Contains(r.console.String(), "  editor   the game runs (pid 100)") {
		t.Errorf("the console:\n%s", r.console.String())
	}
	// the status is a copy
	st.Lines[0], st.Phase = "changed", "changed"
	if s := r.e.Status(); s.Lines[0] == "changed" || s.Phase != testPhasePlaying {
		t.Error("Status isn't a copy")
	}

	// the second Test: the first game closed (WM_CLOSE), This PC restarted again, a new game
	if err := r.e.Test(r.request(nil)); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if s := r.e.Status(); s.Name == "CM_Test-2" && s.Phase == testPhasePlaying {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the second Test: %+v\n%s", r.e.Status(), r.console.String())
		}
	}
	if c, k := g1.counts(); c != 1 || k != 0 {
		t.Errorf("the first game: %d closes, %d kills", c, k)
	}
	select {
	case <-g1.parkStoped:
	case <-time.After(time.Second):
		t.Error("the first game's park went on")
	}
	r.pc.mu.Lock()
	if r.pc.restarts != 2 {
		t.Errorf("%d restarts", r.pc.restarts)
	}
	r.pc.mu.Unlock()
	if s := r.e.Status(); !strings.Contains(strings.Join(s.Lines, "\n"), "closing the game (pid 100)") {
		t.Errorf("the second Test's lines: %v", s.Lines)
	}

	// Stop: the game closed, the Test done and stopped
	g2 := r.game(1)
	g2.mu.Lock()
	g2.ignore = true // in a match: WM_CLOSE is ignored, the kill after 4 s (the fake's at once) ends it
	g2.mu.Unlock()
	if err := r.e.Stop(false); err != nil {
		t.Fatal(err)
	}
	st = r.wait(testPhaseDone)
	if st.Running || !st.Stopped || st.Error != "" {
		t.Errorf("stopped: %+v", st)
	}
	if c, k := g2.counts(); c != 1 || k != 1 {
		t.Errorf("the second game: %d closes, %d kills", c, k)
	}
	if err := r.e.Stop(false); !errors.Is(err, mapedit.ErrNoTest) {
		t.Errorf("Stop after the end: %v", err)
	}
	// the player closes the game: the Test is done
	if err := r.e.Test(r.request(nil)); err != nil {
		t.Fatal(err)
	}
	r.wait(testPhasePlaying)
	r.game(2).end(0)
	if st = r.wait(testPhaseDone); st.Running || st.Stopped || !strings.Contains(strings.Join(st.Lines, "\n"), "the game exited (code 0)") {
		t.Errorf("the game ended: %+v", st)
	}
}

// TestEditorGameFailures: mapc check failing, the setup failing, the tested map left out, a Test while one starts,
// the folder's Start-Server, another client, Stop while the steps run, quitting.
func TestEditorGameFailures(t *testing.T) {
	r := newEditorRig(t)
	if err := r.e.Test(r.request(errors.New("the saved CM_Test fails mapc check (exit 1): rule: why"))); err != nil {
		t.Fatal(err)
	}
	st := r.wait(testPhaseDone)
	if st.Running || !strings.Contains(st.Error, "fails mapc check") || len(r.games) != 0 || r.pc.restarts != 0 {
		t.Errorf("check failed: %+v, %d games, %d restarts", st, len(r.games), r.pc.restarts)
	}
	r.setupErr = true
	r.e.Test(r.request(nil))
	if st = r.wait(testPhaseDone); !strings.Contains(st.Error, "the game copy is not ready: ") || !strings.Contains(st.Error, "a test failure") {
		t.Errorf("setup failed: %+v", st)
	}
	r.setupErr = false
	r.setupLine = setup.Skipped
	r.e.Test(r.request(nil))
	if st = r.wait(testPhaseDone); !strings.Contains(st.Error, "CM_Test isn't in the game copy: [skipped]  client CM_Test") || len(r.games) != 0 {
		t.Errorf("the map left out: %+v", st)
	}
	r.list.mu.Lock()
	if r.list.resumes != r.list.pauses {
		t.Errorf("the list: %d pauses, %d resumes", r.list.pauses, r.list.resumes)
	}
	r.list.mu.Unlock()

	// a Test while one starts is refused; Stop then starts no game
	r.setupLine = setup.OK
	r.gate = make(chan struct{})
	if err := r.e.Test(r.request(nil)); err != nil {
		t.Fatal(err)
	}
	r.wait(testPhaseSetup)
	if err := r.e.Test(r.request(nil)); err == nil || !strings.Contains(err.Error(), "still starting (setup)") {
		t.Errorf("a Test while one starts: %v", err)
	}
	if err := r.e.Stop(false); err != nil {
		t.Fatal(err)
	}
	close(r.gate)
	if st = r.wait(testPhaseDone); !st.Stopped || st.Error != "" || len(r.games) != 0 {
		t.Errorf("stopped in the setup: %+v, %d games", st, len(r.games))
	}
	r.gate = nil

	// the folder's Start-Server is This PC; another client runs
	r.pc.held = true
	if err := r.e.Test(r.request(nil)); err == nil || !strings.Contains(err.Error(), "Start-Server (pid 4242) is running") ||
		!strings.Contains(err.Error(), "map = CM_<name>") {
		t.Errorf("the folder's Start-Server: %v", err)
	}
	r.pc.held = false
	r.e.otherGame = func() bool { return true }
	if err := r.e.Test(r.request(nil)); err == nil || !strings.Contains(err.Error(), "another Dawngate client") {
		t.Errorf("another client: %v", err)
	}
	r.e.otherGame = nil

	// quitting closes the game, and no Test starts after it
	r.e.Test(r.request(nil))
	r.wait(testPhasePlaying)
	r.e.Close()
	if c, _ := r.game(0).counts(); c != 1 {
		t.Errorf("quitting: %d closes", c)
	}
	r.wait(testPhaseDone)
	if err := r.e.Test(r.request(nil)); err == nil {
		t.Error("a Test after quitting")
	}
}

// TestLineWriter: the setup's output line by line, the progress's carriage returns too, the rest at Flush.
func TestLineWriter(t *testing.T) {
	var got []string
	w := &lineWriter{line: func(l string) { got = append(got, l) }}
	io.WriteString(w, "  [ok]  a\r\n  [do")
	io.WriteString(w, "ne] b\n\n   \nc 10 %\rc 20 %")
	w.Flush()
	if strings.Join(got, "|") != "  [ok]  a|  [done] b|c 10 %|c 20 %" {
		t.Errorf("%q", got)
	}
}

// TestEditorPrecheck: the address must be this PC's, the page folder must be there (default editor\ beside the
// program, a relative --editor-pages from there).
func TestEditorPrecheck(t *testing.T) {
	dir := t.TempDir()
	e := editorOptions{on: true, addr: "0.0.0.0:17890"}
	if err := editorPrecheck(dir, &e); err == nil || !strings.Contains(err.Error(), "only an address of this PC") {
		t.Errorf("a wildcard address: %v", err)
	}
	e = editorOptions{on: true, addr: "127.0.0.1"}
	if err := editorPrecheck(dir, &e); err == nil || !strings.Contains(err.Error(), "--editor-addr 127.0.0.1") {
		t.Errorf("no port: %v", err)
	}
	e = editorOptions{on: true, addr: DefaultEditorAddr}
	if err := editorPrecheck(dir, &e); err == nil || !strings.Contains(err.Error(), "Unpack the map editor's zip") ||
		!strings.Contains(err.Error(), filepath.Join(dir, EditorFolder)) {
		t.Errorf("no editor folder: %v", err)
	}
	os.MkdirAll(filepath.Join(dir, EditorFolder), 0o755)
	if err := editorPrecheck(dir, &e); err != nil || e.pages != filepath.Join(dir, EditorFolder) {
		t.Errorf("the editor folder: %v %q", err, e.pages)
	}
	os.MkdirAll(filepath.Join(dir, "page", "web"), 0o755)
	e = editorOptions{on: true, addr: "127.0.0.4:17893", pages: filepath.Join("page", "web")}
	if err := editorPrecheck(dir, &e); err != nil || e.pages != filepath.Join(dir, "page", "web") {
		t.Errorf("--editor-pages: %v %q", err, e.pages)
	}
}

// TestEditorRefusals: Start-Client --editor refuses without the page's folder, with --check, without the picker and
// while the folder's Start-Server is This PC; nothing is set up then.
func TestEditorRefusals(t *testing.T) {
	dir := release(t)
	if code, out := run(t, RunClient, dir, "--editor", "--non-interactive", "--test-lane", "Q"); code != 1 ||
		!strings.Contains(out, "Unpack the map editor's zip") {
		t.Errorf("no editor folder: exit %d\n%s", code, out)
	}
	os.MkdirAll(filepath.Join(dir, EditorFolder), 0o755)
	if code, out := run(t, RunClient, dir, "--editor", "--check"); code != 1 || !strings.Contains(out, "not with --check") {
		t.Errorf("--check: exit %d\n%s", code, out)
	}
	if code, out := run(t, RunClient, dir, "--editor", "--non-interactive", "--test-lane", "Q", "--picker", "off", "127.0.0.1"); code != 1 ||
		!strings.Contains(out, "needs the server picker") {
		t.Errorf("--picker off: exit %d\n%s", code, out)
	}
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	os.MkdirAll(l.Root, 0o755)
	holdRunLock(t, l, setup.RunLock{Program: "Start-Server", Address: "127.0.0.1", GatewayPort: 42128, State: setup.RunRunning})
	if code, out := run(t, RunClient, dir, "--editor", "--non-interactive", "--test-lane", "Q", "--tunnel-address", thisPCFront); code != 1 ||
		!strings.Contains(out, "This folder's Start-Server") || !strings.Contains(out, "map = CM_<name>") {
		t.Errorf("the folder's Start-Server: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(l.ClientCopy()); err == nil {
		t.Error("a refusal made the player copy")
	}
}

// TestThisPCRestart: Restart stops This PC's own server (its run lock freed) without closing This PC; the next
// Ensure sets the host copy up again and starts the server with SetMap's map (--map, --custom-maps with the folder's
// custom maps); the host copy's setup skips a package whose build fails.
func TestThisPCRestart(t *testing.T) {
	dir := release(t)
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	var console syncBuffer
	pc := newThisPC(IO{Out: &console}, l, thisPCFront, filepath.Join(dir, "host"), Config{"custom_maps": "MM_Map_v3"}, bm, false, true)
	pc.findZip = func() (string, error) { return filepath.Join(dir, "Dawngate.zip"), nil }
	pc.skipFailedMaps = true
	var mu sync.Mutex
	var runs [][]string
	stops := 0
	pc.run = func(flags []string, h dgserver.Hooks) int {
		mu.Lock()
		runs = append(runs, flags)
		mu.Unlock()
		h.Started(dgserver.Info{})
		<-h.Stop
		mu.Lock()
		stops++
		mu.Unlock()
		return 0
	}
	defer pc.Close()
	if _, err := pc.Ensure(60 * time.Second); err != nil {
		t.Fatalf("the first start: %v\n%s", err, console.String())
	}
	if f := strings.Join(runs[0], " "); strings.Contains(f, "--map=") {
		t.Errorf("a map before SetMap: %s", f)
	}
	pc.SetMap("CM_Test")
	pc.Restart()
	mu.Lock()
	if stops != 1 {
		t.Errorf("%d stops after Restart", stops)
	}
	mu.Unlock()
	if st, _ := pc.status(); st != thisPCLocal {
		t.Errorf("after Restart: %q", st)
	}
	if _, err := os.Stat(filepath.Join(l.Root, setup.RunLockFile)); !os.IsNotExist(err) {
		t.Errorf("the run lock stayed: %v", err)
	}
	if _, held := pc.folderServer(); held {
		t.Error("This PC's own lock read as the folder's Start-Server")
	}
	if _, err := pc.Ensure(60 * time.Second); err != nil {
		t.Fatalf("the start after Restart: %v\n%s", err, console.String())
	}
	mu.Lock()
	f := strings.Join(runs[len(runs)-1], " ")
	n := len(runs)
	mu.Unlock()
	if n != 2 || !strings.Contains(f, " --map=CM_Test --custom-maps=CM_Test,MM_Map_v3 --discord-webhook=") {
		t.Errorf("%d runs; the flags after Restart: %s", n, f)
	}
	if !strings.Contains(console.String(), "stopping the server for the map editor's Test") {
		t.Errorf("the console:\n%s", console.String())
	}
	pc.Close()
	mu.Lock()
	if stops != 2 {
		t.Errorf("%d stops after Close", stops)
	}
	mu.Unlock()
	pc.Restart() // after Close: nothing runs, nothing starts
	if _, err := pc.Ensure(time.Second); !errors.Is(err, errClosedFront) {
		t.Errorf("Ensure after Close: %v", err)
	}
}

// TestRunEditor: editor mode's page server: the address in the console, Test on in /api/state, POST /api/test
// reaching the Game, quitting on the signal (the game closed, the server stopped).
func TestRunEditor(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	maps, pages := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(maps, "CM_Test"), 0o755)
	os.WriteFile(filepath.Join(maps, "CM_Test", "map.json"), []byte(`{"format": 1, "name": "CM_Test"}`), 0o644)
	r := newEditorRig(t)
	c := mapedit.Config{Maps: maps, Mapc: exe, MapcArgs: []string{"--no-such-flag"}, Pages: pages, Out: t.TempDir(),
		Cache: t.TempDir(), Game: r.e}
	var console syncBuffer
	quit := make(chan os.Signal, 1)
	done := make(chan int, 1)
	go func() {
		done <- runEditor(IO{Out: &console}, editorOptions{on: true, addr: "127.0.0.1:0", pages: pages}, c, r.e, false, quit)
	}()
	var url string
	for deadline := time.Now().Add(10 * time.Second); url == ""; time.Sleep(10 * time.Millisecond) {
		if _, rest, ok := strings.Cut(console.String(), "Map editor: "); ok {
			url, _, _ = strings.Cut(rest, "\n")
		}
		if time.Now().After(deadline) {
			t.Fatalf("no address:\n%s", console.String())
		}
	}
	resp, err := http.Get(url + "api/state")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"test":{"enabled":true,"reason":""}`) {
		t.Errorf("/api/state: %s", body)
	}
	if !strings.Contains(console.String(), "  editor   WARNING: ") {
		t.Errorf("no warning for a folder without the page:\n%s", console.String())
	}
	quit <- os.Interrupt
	select {
	case code := <-done:
		if code != 0 || !strings.Contains(console.String(), "Quitting the map editor") {
			t.Errorf("exit %d\n%s", code, console.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runEditor didn't quit")
	}
	if !r.e.closed {
		t.Error("the game wasn't closed")
	}
	if _, err := http.Get(url + "api/state"); err == nil {
		t.Error("the page server still answers")
	}
}

// TestFrontPauseList: while paused, the list isn't written (refreshLoop's and a login's writes find no copy); the
// resume writes it into the same copy at once; SetMaps replaces the dialog's maps.
func TestFrontPauseList(t *testing.T) {
	l := setup.Layout{Root: t.TempDir()}
	f := &Front{Layout: l, Bookmarks: &Bookmarks{path: BookmarksPath(l)}}
	copyDir := t.TempDir()
	os.MkdirAll(filepath.Join(copyDir, "game", "Data"), 0o755)
	if err := f.WriteServerList(copyDir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(copyDir, filepath.FromSlash(ListArchive))
	resume := f.PauseList()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	f.listChanged() // a login's write: none while paused
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("written while paused: %v", err)
	}
	if err := resume(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("not written at the resume: %v", err)
	}
	f.SetMaps([]moviepatch.Map{{Tag: "t"}})
	if m := f.folderMaps(); len(m) != 1 || m[0].Tag != "t" {
		t.Errorf("SetMaps: %v", m)
	}
}
