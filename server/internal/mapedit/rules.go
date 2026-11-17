package mapedit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The map's rules (the page's Map > Rules... and Advanced > Gameplay Constants...): `mapc tuning --json` reads a
// tuning level's values (SM_Map_v5's or MM_Map_v3's death timer, waves, gold, wells, surrender) and, given a map with
// --map -, applies the map's rules section to them and describes it as players read it (the summary, the short text,
// whether the game stays rated, the rules' findings, every member of the compiled tuning with its default and this
// map's value). The editor only relays that reply: what the rules mean stays in mapc.
//
//	GET  /api/tuning?level=<level>  -> {ok: true, <mapc's reply>} (the defaults the page shows as placeholders); cached
//	POST /api/rules/{pkg}            the document -> {ok: true, <mapc's reply>} for the draft (merged as the check merges it)
//
// Both pass the editor's game copy (--copy) when it has one: mapc then reads the level's values from the copy's tuning
// assets and lists their members ("constants", Gameplay Constants; "source": "copy"). Without a copy, or when the
// copy's tuning can't be read, they ask without it: the same numbers from mapc's own table ("source": "shipped"), no
// constants.
//
// When mapc can't answer (a mapc without the tuning command, or one that fails) the reply is {ok: false, error} with
// status 200: the page then uses its own copy of the shipped values and of the summary's wording.

func (s *Server) routesRules() {
	s.mux.HandleFunc("GET /api/tuning", s.tuning)
	s.mux.HandleFunc("POST /api/rules/{pkg}", s.rules)
}

// tuningLevelName is a level name mapc may be asked about (it checks the name itself).
var tuningLevelName = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// tuningCache keeps each server's successful `mapc tuning --level` replies: a level's values never change while the
// editor runs. Key: tuningKey.
var tuningCache sync.Map

type tuningKey struct {
	s     *Server
	level string
}

func (s *Server) tuning(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	if level == "" {
		level = "SM_Map_v5"
	}
	if !tuningLevelName.MatchString(level) {
		fail(w, http.StatusBadRequest, "bad level name %q", level)
		return
	}
	k := tuningKey{s, level}
	if v, ok := tuningCache.Load(k); ok {
		writeJSON(w, v.([]byte))
		return
	}
	b, err := s.tuningCopy(nil, "--level", level)
	if err != nil {
		reply(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	tuningCache.Store(k, b)
	writeJSON(w, b)
}

// rules answers for the draft: the request's document merged into the package's map.json (as the check does) and
// given to mapc on its standard input.
func (s *Server) rules(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	file, err := os.ReadFile(filepath.Join(dir, "map.json"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	_, text, err := readDoc(r, file)
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	b, err := s.tuningCopy(text, "--map", "-")
	if err != nil {
		reply(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, b)
}

// tuningCopy runs `mapc tuning --json` with args, with --copy first when the editor has a game copy and, when that run
// fails (the copy's tuning unreadable), again without it.
func (s *Server) tuningCopy(stdin []byte, args ...string) ([]byte, error) {
	plain := append([]string{"tuning", "--json"}, args...)
	if s.c.Copy == "" {
		return s.tuningReply(stdin, plain...)
	}
	b, err := s.tuningReply(stdin, append([]string{"tuning", "--json", "--copy", s.c.Copy}, args...)...)
	if err == nil {
		return b, nil
	}
	if b, err2 := s.tuningReply(stdin, plain...); err2 == nil {
		return b, nil
	}
	return nil, err
}

// tuningReply runs mapc with stdin and returns its JSON object with "ok": true added, or why it has none.
func (s *Server) tuningReply(stdin []byte, args ...string) ([]byte, error) {
	out, errOut, code, err := s.mapcStdin(30*time.Second, stdin, args...)
	if err != nil {
		return nil, err
	}
	var v map[string]json.RawMessage
	if jerr := json.Unmarshal(out, &v); jerr != nil || code != 0 {
		why := strings.TrimSpace(string(errOut))
		if why == "" {
			why = strings.TrimSpace(string(out))
		}
		if i := strings.IndexByte(why, '\n'); i > 0 {
			why = why[:i]
		}
		if len(why) > 300 {
			why = why[:300]
		}
		return nil, fmt.Errorf("mapc %s: exit %d: %s", args[0], code, why)
	}
	if e, ok := v["error"]; ok && v["ok"] == nil {
		var msg string
		json.Unmarshal(e, &msg)
		return nil, fmt.Errorf("mapc %s: %s", args[0], msg)
	}
	v["ok"] = json.RawMessage("true")
	return json.Marshal(v)
}

// mapcStdin runs mapc as Server.mapc does, with stdin (nil: none) and its standard error kept.
func (s *Server) mapcStdin(timeout time.Duration, stdin []byte, args ...string) (stdout, stderr []byte, code int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.c.Mapc, append(append([]string(nil), s.c.MapcArgs...), args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.Bytes(), errb.Bytes(), ee.ExitCode(), nil
	}
	if err != nil {
		return nil, nil, -1, fmt.Errorf("mapc: %v %s", err, errb.String())
	}
	return out.Bytes(), errb.Bytes(), 0, nil
}

func writeJSON(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
