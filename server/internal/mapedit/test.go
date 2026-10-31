package mapedit

// Test: a practice match on the saved map, played by the game of the program that serves the editor (Config.Game:
// Start-Client's editor mode, MAPED-REL3). The backend checks the request (a saved package with a map name, the
// options a JSON object), hands it to the Game with a Check that runs `mapc check` on the saved package, and passes
// Stop and the Game's Status on to the page. Without a Game, Test is off and /api/state says why.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Game is Test's game: Start-Client's (MAPED-REL3), or a test's fake.
type Game interface {
	// Test starts a Test of the saved package and returns at once; an error when it can't start (one running,
	// options it refuses). The Game runs req.Check in its own phase.
	Test(req TestRequest) error
	// Stop ends the running Test (force: its processes at once); an error when none runs.
	Stop(force bool) error
	// Status is the last Test's state, nil before the first.
	Status() *TestStatus
}

// TestRequest is one Test: the saved package and the page's options.
type TestRequest struct {
	Pkg     string          // the package's folder name
	Dir     string          // its folder
	Map     string          // its map.json's name (the level CM_<name>)
	Options json.RawMessage // the page's options (a JSON object), nil without: the Game's to read
	// Check runs `mapc check --json` on the saved package (with --copy when the editor has a copy): nil when it
	// passes, else why not (the first error finding); the report is mapc's JSON.
	Check func() (report []byte, err error)
}

// TestStatus is a Test's state as the page polls it (GET /api/test).
type TestStatus struct {
	Name    string    `json:"name"` // the Game's name for this Test (its log's)
	Pkg     string    `json:"pkg"`
	Map     string    `json:"map"`
	Phase   string    `json:"phase"` // the Game's phases, then "done"
	Running bool      `json:"running"`
	Started time.Time `json:"started"`
	Stopped bool      `json:"stopped,omitempty"`
	Error   string    `json:"error,omitempty"`
	Lines   []string  `json:"-"` // the Test's last log lines (the reply's "tail")
}

// noGame is why Test is off without a Game.
const noGame = "Test plays the map in the game: start the map editor with Start-Client.exe --editor"

func (s *Server) routesTest() {
	s.mux.HandleFunc("POST /api/test/{pkg}", s.test)
	s.mux.HandleFunc("GET /api/test", s.testStatus)
	s.mux.HandleFunc("POST /api/test-stop", s.testStop)
}

// testReason says why Test can't run here, or "".
func (s *Server) testReason() string {
	if s.c.Game != nil {
		return ""
	}
	if s.c.TestOff != "" {
		return s.c.TestOff
	}
	return noGame
}

// test starts a Test of the saved package.
func (s *Server) test(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	if why := s.testReason(); why != "" {
		fail(w, http.StatusConflict, "%s", why)
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	var opts json.RawMessage
	if b = bytes.TrimSpace(b); len(b) > 0 {
		var o map[string]json.RawMessage
		if err := json.Unmarshal(b, &o); err != nil {
			fail(w, http.StatusBadRequest, "options: %v", err)
			return
		}
		opts = b
	}
	var m struct {
		Name string `json:"name"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "map.json")); err != nil || json.Unmarshal(b, &m) != nil || !pkgName.MatchString(m.Name) {
		fail(w, http.StatusConflict, "%s/map.json has no map name", pkg)
		return
	}
	req := TestRequest{Pkg: pkg, Dir: dir, Map: m.Name, Options: opts, Check: func() ([]byte, error) { return s.checkSaved(pkg, dir) }}
	if err := s.c.Game.Test(req); err != nil {
		fail(w, http.StatusConflict, "%v", err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "test": s.c.Game.Status()})
}

// checkSaved runs `mapc check --json` on the saved package, with the copy's catalog when the editor has a copy.
func (s *Server) checkSaved(pkg, dir string) ([]byte, error) {
	defer s.lock(pkg)()
	args := []string{"check", "--json"}
	if s.c.Copy != "" {
		args = append(args, "--copy", s.c.Copy)
	}
	out, code, err := s.mapc(3*time.Minute, append(args, "--maps", s.c.Maps, dir)...)
	if err != nil {
		return out, err
	}
	if code == 0 {
		return out, nil
	}
	first := ""
	var rep struct {
		Findings []struct{ Rule, Severity, Message string }
	}
	if json.Unmarshal(out, &rep) == nil {
		for _, f := range rep.Findings {
			if f.Severity == "error" {
				first = ": " + f.Rule + ": " + f.Message
				break
			}
		}
	}
	return out, fmt.Errorf("the saved %s fails mapc check (exit %d)%s: fix its findings and save", pkg, code, first)
}

func (s *Server) testStatus(w http.ResponseWriter, r *http.Request) {
	var st *TestStatus
	if s.c.Game != nil {
		st = s.c.Game.Status()
	}
	if st == nil {
		reply(w, http.StatusOK, map[string]any{"test": nil})
		return
	}
	reply(w, http.StatusOK, map[string]any{"test": st, "tail": st.Lines})
}

// testStop ends the running Test (?force=1: its processes at once).
func (s *Server) testStop(w http.ResponseWriter, r *http.Request) {
	if s.c.Game == nil {
		fail(w, http.StatusConflict, "no Test is running")
		return
	}
	if err := s.c.Game.Stop(r.URL.Query().Get("force") == "1"); err != nil {
		fail(w, http.StatusConflict, "%v", err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true})
}

// ErrNoTest is what a Game's Stop returns when no Test runs.
var ErrNoTest = errors.New("no Test is running")
