package launcher

// Start-Client's map editor (Start-Client.exe --editor): Start-Client starts as for a game (the player copy, the
// front with the server list and This PC), but instead of the game it serves the map editor's page
// (internal/mapedit) on a loopback address and waits until its console is closed. The game starts when the page's
// Test is pressed: the saved package is checked, the editor's game (if one runs) is closed, This PC's own server is
// stopped and given the map (--map, also for practice), the player copy is set up again (the changed package compiled
// into it, the create dialog's list rewritten; a package of the folder that doesn't build is skipped, the tested one
// fails the Test), and the game starts with This PC preselected. The login to This PC sets up the host copy with the
// same package and starts the server with the map. Every Test restarts the game: it mounts its archives only at its
// start. Stop closes the game; quitting closes the game, This PC's server and the page's server.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/mapedit"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/moviepatch"
)

// DefaultEditorAddr is where the map editor's page is served (--editor-addr): this PC only.
const DefaultEditorAddr = "127.0.0.1:17890"

// EditorFolder is the map editor's page beside Start-Client.exe: the folder the editor's zip unpacks (--editor-pages).
const EditorFolder = "editor"

// editorOptions are Start-Client's editor flags.
type editorOptions struct {
	on    bool
	addr  string // --editor-addr
	pages string // --editor-pages, resolved against the program's folder (default: its editor folder)
}

// editorPrecheck checks the editor's flags before anything is set up: a loopback address (the page writes into the
// maps folder: never for the network) and the page's folder (the editor's zip unpacked beside Start-Client.exe).
func editorPrecheck(exeDir string, e *editorOptions) error {
	host, _, err := net.SplitHostPort(e.addr)
	if err != nil {
		return fmt.Errorf("--editor-addr %s: %v (an address with a port, such as %s)", e.addr, err, DefaultEditorAddr)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("--editor-addr %s: only an address of this PC (127.x.x.x) is allowed: the map editor writes "+
			"into your maps folder, so nothing on the network may reach it", e.addr)
	}
	if e.pages == "" {
		e.pages = filepath.Join(exeDir, EditorFolder)
	} else {
		e.pages = Resolve(exeDir, e.pages)
	}
	if st, err := os.Stat(e.pages); err != nil || !st.IsDir() {
		return fmt.Errorf("The map editor isn't here: the folder %s is missing.\nUnpack the map editor's zip "+
			"(Dawngate-Map-Editor-<version>.zip) here, into %s: it holds the folder %s and Map Editor.cmd. Then start "+
			"Map Editor.cmd again.", e.pages, exeDir, EditorFolder)
	}
	return nil
}

// errNoPicker is the editor's refusal without the server picker: Test plays on This PC, which only the picker has.
var errNoPicker = errors.New("The map editor plays your map on This PC, the server on this PC, which needs the server " +
	"picker: start the map editor without a server address, and with picker = auto and tunnel = auto in " +
	ClientConfigName + ".")

// errFolderServer is the editor's refusal while this folder's Start-Server serves This PC: Test can't give that one
// the map.
func errFolderServer(held setup.RunLock) error {
	return fmt.Errorf("This folder's %s (pid %d) is running, so This PC is that server, and the map editor can't "+
		"make it host your map. Stop that %s and start the map editor again; or, to play your map there, name it "+
		"in its %s (map = CM_<name>) and start it again.", held.Program, held.Pid, held.Program, ServerConfigName)
}

// editorServer is what the editor's Test needs of This PC (*thisPC; the tests' fakes).
type editorServer interface {
	SetMap(name string)
	Restart()
	folderServer() (setup.RunLock, bool)
}

// editorList is what the editor's Test needs of the front (*Front; the tests' fakes).
type editorList interface {
	EndSession(tag string) bool
	PauseList() func() error
	SetMaps(maps []moviepatch.Map)
}

// editorGame is the map editor's mapedit.Game: one Test at a time, each a fresh start of the game.
type editorGame struct {
	out        io.Writer                                 // Start-Client's console
	start      func() (gameProcess, error)               // StartGame on the player copy
	park       bool                                      // --test-park: each game kept parked
	foreground func() uintptr                            // ForegroundWindow (nil: none), given back by Park
	pc         editorServer                              // This PC
	list       editorList                                // the front (nil: none)
	setup      func(out io.Writer) (*setup.Setup, error) // the player copy's setup again, under the folder's lock
	dialogMaps func() []moviepatch.Map                   // the maps folder's create-dialog entries now
	otherGame  func() bool                               // another client holds Global\Dawngate (nil: not checked)

	mu      sync.Mutex
	n       int
	st      *mapedit.TestStatus
	busy    bool // a Test's steps run (until its game started or it ended)
	stopReq bool // Stop or quitting: the running steps start no game
	closed  bool
	g       gameProcess   // the editor's game while it runs
	gdone   chan struct{} // closed when g has exited
}

// maxTestLines are the lines a Test keeps for the page.
const maxTestLines = 120

// The Test's phases (TestStatus.Phase), in order; "done" at the end.
const (
	testPhaseCheck   = "check"   // mapc check of the saved package
	testPhaseClosing = "closing" // the previous game closes
	testPhaseServer  = "server"  // This PC's server stops (it starts with the map at the login)
	testPhaseSetup   = "setup"   // the player copy set up again
	testPhaseStart   = "starting"
	testPhasePlaying = "playing" // the game runs: log in, PLAY, PRACTICE DAWNGATE
	testPhaseDone    = "done"
)

// Test starts a Test (mapedit.Game): refused while another one's steps run, while this folder's Start-Server is This
// PC, or while another client's game runs; a running game of the editor's is closed by the Test's steps.
func (e *editorGame) Test(req mapedit.TestRequest) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("the map editor is quitting")
	}
	if e.busy {
		return fmt.Errorf("a Test is still starting (%s): wait until the game runs, or press Stop", e.st.Phase)
	}
	if held, ok := e.pc.folderServer(); ok {
		return errFolderServer(held)
	}
	if e.g == nil && e.otherGame != nil && e.otherGame() {
		return errors.New("another Dawngate client runs on this PC (only one can run at a time): close it, then press Test again")
	}
	e.n++
	st := &mapedit.TestStatus{Name: fmt.Sprintf("%s-%d", req.Map, e.n), Pkg: req.Pkg, Map: req.Map, Phase: testPhaseCheck,
		Running: true, Started: time.Now()}
	e.st, e.busy, e.stopReq = st, true, false
	go e.run(req, st)
	return nil
}

// Stop ends the running Test (mapedit.Game): its steps start no game, a running game is closed (force: at once).
func (e *editorGame) Stop(force bool) error {
	e.mu.Lock()
	g, st := e.g, e.st
	if g == nil && !e.busy {
		e.mu.Unlock()
		return mapedit.ErrNoTest
	}
	e.stopReq = true
	st.Stopped = true
	e.mu.Unlock()
	if g != nil {
		e.say(st, "Stop: closing the game (pid %d)", g.ID())
		go closeGame(g, force)
	} else {
		e.say(st, "Stop: the game won't start")
	}
	return nil
}

// Status is the last Test's state (mapedit.Game), a copy.
func (e *editorGame) Status() *mapedit.TestStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.st == nil {
		return nil
	}
	c := *e.st
	c.Lines = append([]string(nil), e.st.Lines...)
	return &c
}

// Close is the editor's quit: no game starts any more, and the running one is closed (waited for up to 5 s).
func (e *editorGame) Close() {
	e.mu.Lock()
	e.closed, e.stopReq = true, true
	g, done := e.g, e.gdone
	e.mu.Unlock()
	if g != nil {
		fmt.Fprintf(e.out, "  editor   closing the game (pid %d)\n", g.ID())
		closeGame(g, false)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func (e *editorGame) say(st *mapedit.TestStatus, format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	e.mu.Lock()
	st.Lines = append(st.Lines, line)
	if n := len(st.Lines); n > maxTestLines {
		st.Lines = append([]string(nil), st.Lines[n-maxTestLines:]...)
	}
	e.mu.Unlock()
	if e.out != nil {
		fmt.Fprintf(e.out, "  editor   %s\n", line)
	}
}

func (e *editorGame) phase(st *mapedit.TestStatus, phase, format string, a ...any) {
	e.mu.Lock()
	st.Phase = phase
	e.mu.Unlock()
	e.say(st, format, a...)
}

func (e *editorGame) stopping() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopReq
}

// end ends a Test's steps: done, with the error (none when "").
func (e *editorGame) end(st *mapedit.TestStatus, failure string) {
	e.mu.Lock()
	st.Phase, st.Running, st.Error = testPhaseDone, false, failure
	if e.st == st {
		e.busy = false
	}
	e.mu.Unlock()
	if failure != "" {
		e.say(st, "Test FAILED: %s", failure)
	}
}

// run is a Test's steps (in its own goroutine).
func (e *editorGame) run(req mapedit.TestRequest, st *mapedit.TestStatus) {
	fail := func(format string, a ...any) { e.end(st, fmt.Sprintf(format, a...)) }
	stopped := func() bool {
		if !e.stopping() {
			return false
		}
		e.say(st, "stopped before the game started")
		e.end(st, "")
		return true
	}
	e.say(st, "Test %s of %s (%s)", st.Name, req.Map, req.Dir)
	e.phase(st, testPhaseCheck, "mapc check of the saved package")
	if _, err := req.Check(); err != nil {
		fail("%v", err)
		return
	}
	if stopped() {
		return
	}
	e.mu.Lock()
	g, gdone := e.g, e.gdone
	e.mu.Unlock()
	if g != nil {
		e.phase(st, testPhaseClosing, "closing the game (pid %d): it loads the new version only at its start", g.ID())
		closeGame(g, false)
		<-gdone
	}
	e.phase(st, testPhaseServer, "This PC's server stops; the next login starts it again with --map %s", req.Map)
	// the front's session to the stopping server goes first: it would keep connecting again and carry the next game's
	// QoS probes, which the front answers itself without a session
	if e.list != nil && e.list.EndSession(ThisPCTag) {
		e.say(st, "the front's session to This PC closed")
	}
	e.pc.SetMap(req.Map)
	e.pc.Restart()
	e.phase(st, testPhaseSetup, "setting up the game copy again (%s compiled into it)", req.Map)
	var resume func() error
	if e.list != nil {
		resume = e.list.PauseList()
	}
	w := &lineWriter{line: func(l string) { e.say(st, "%s", l) }}
	s, err := e.setup(w)
	w.Flush()
	if resume != nil {
		if rerr := resume(); rerr != nil {
			e.say(st, "note: %v", rerr)
		}
	}
	if e.list != nil && e.dialogMaps != nil {
		e.list.SetMaps(e.dialogMaps())
	}
	if err != nil {
		why := err.Error()
		if s != nil && len(s.Bad()) > 0 {
			why = strings.ReplaceAll(lines(s.Bad()), "\n", "; ")
		}
		fail("the game copy is not ready: %s", why)
		return
	}
	if l, ok := s.MapLine("client", req.Map); !ok {
		fail("the setup didn't compile %s into the game copy (is its package in the maps folder?)", req.Map)
		return
	} else if l.Status != setup.OK && l.Status != setup.Done {
		fail("%s isn't in the game copy: %s", req.Map, strings.TrimSpace(l.String()))
		return
	}
	if stopped() {
		return
	}
	e.phase(st, testPhaseStart, "starting the game")
	var prev uintptr
	if e.foreground != nil {
		prev = e.foreground()
	}
	g, err = e.start()
	if err != nil {
		fail("the game didn't start: %v", err)
		return
	}
	done := make(chan struct{})
	e.mu.Lock()
	e.g, e.gdone = g, done
	stopNow := e.stopReq
	st.Phase = testPhasePlaying
	if e.st == st {
		e.busy = false
	}
	e.mu.Unlock()
	e.say(st, "the game runs (pid %d): log in on This PC with any name, then PLAY and PRACTICE DAWNGATE play %s", g.ID(), req.Map)
	parkStop := make(chan struct{})
	if e.park {
		go g.Park(parkStop, prev, parkEvents(e.out))
	}
	go func() {
		code, _ := g.Wait()
		close(parkStop)
		g.Release()
		e.mu.Lock()
		if e.g == g {
			e.g, e.gdone = nil, nil
		}
		st.Phase, st.Running = testPhaseDone, false
		e.mu.Unlock()
		e.say(st, "the game exited (code %d)", int32(code))
		close(done)
	}()
	if stopNow {
		closeGame(g, false)
	}
}

// lineWriter passes what is written to it on line by line (the setup's step lines to a Test's log).
type lineWriter struct {
	line func(string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexAny(string(w.buf), "\r\n")
		if i < 0 {
			return len(p), nil
		}
		if l := strings.TrimRight(string(w.buf[:i]), " "); strings.TrimSpace(l) != "" {
			w.line(l)
		}
		w.buf = w.buf[i+1:]
	}
}

// Flush passes on a last line without its line end.
func (w *lineWriter) Flush() {
	if l := strings.TrimSpace(string(w.buf)); l != "" {
		w.line(l)
	}
	w.buf = nil
}

// runEditor is editor mode's end of RunClient, in place of play: the page's server on e.addr until quit has a value
// (Ctrl+C, the console closed), then the game closed and the page's server stopped (This PC's server and the front
// are the caller's, deferred). open opens the page in the default browser.
func runEditor(o IO, e editorOptions, c mapedit.Config, game *editorGame, open bool, quit <-chan os.Signal) int {
	srv, err := mapedit.NewServer(c)
	if err != nil {
		return o.fail("The map editor didn't start: %v", err)
	}
	defer srv.Close()
	if err := srv.PageError(); err != nil {
		fmt.Fprintf(o.Out, "  editor   WARNING: %v\n", err) // the page's address shows it too
	}
	ln, err := net.Listen("tcp", e.addr)
	if err != nil {
		return o.fail("The map editor can't serve %s: %v\nIs the map editor already running? Close it first (or start "+
			"Start-Client.exe --editor --editor-addr 127.0.0.1:<another port>).", e.addr, err)
	}
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- hs.Serve(ln) }()
	url := "http://" + ln.Addr().String() + "/"
	fmt.Fprintf(o.Out, "\nMap editor: %s\n", url)
	fmt.Fprintf(o.Out, "  maps     %s; the page %s\n", c.Maps, e.pages)
	fmt.Fprintln(o.Out, "  Make or open a map, Save, then Test: the game starts with This PC preselected. Log in with any name, then PLAY and PRACTICE DAWNGATE.")
	fmt.Fprintln(o.Out, "  Each Test starts the game again on the saved version. Closing this window (or Ctrl+C) quits the editor and the game.")
	if open {
		openBrowser(o.Out, url)
	}
	code := 0
	select {
	case <-quit:
		fmt.Fprintln(o.Out, "Quitting the map editor ...")
	case err := <-served:
		fmt.Fprintf(o.Out, "The map editor's page server ended: %v\n", err)
		code = 1
	}
	game.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	hs.Shutdown(ctx)
	cancel()
	return code
}

// openBrowser opens a page in the default browser.
func openBrowser(out io.Writer, url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(out, "  editor   the browser didn't open (%v): open %s yourself\n", err, url)
		return
	}
	go cmd.Wait()
}
