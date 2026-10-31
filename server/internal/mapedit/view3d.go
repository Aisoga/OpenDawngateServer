package mapedit

// The 3-D view's side: mapc's exports (scene lists, models, the generated ground as glTF and PNG) made into a cache
// folder and served from it. The models and textures are the player's game files turned into another format, so
// the cache stays outside git (the default is under the output folder, which is gitignored).

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// View3DReason says why the 3-D view is off, or "" when it can run.
func (s *Server) view3DReason() string {
	if s.c.Copy == "" {
		return "the 3-D view draws the game's own models: the editor has no game copy to read"
	}
	if s.c.Cache == "" {
		return "no cache folder for the exports"
	}
	return ""
}

func (s *Server) routes3D() {
	s.mux.HandleFunc("GET /api/scene/{pkg}", s.scenePkg)
	s.mux.HandleFunc("GET /api/level-scene/{level}", s.sceneLevel)
	s.mux.HandleFunc("POST /api/ground/{pkg}", s.ground)
	s.mux.HandleFunc("GET /3d/", s.files3D)
}

// draftOrPackage is the package's latest draft (what the last check saw) or, before any check, the package itself.
func (s *Server) draftOrPackage(pkg, dir string) string {
	draft := filepath.Join(s.c.Work, pkg, "draft", pkg)
	if _, err := os.Stat(filepath.Join(draft, "map.json")); err == nil {
		return draft
	}
	return dir
}

// runScene runs `mapc scene --json` into the cache and replies with the scene list.
func (s *Server) runScene(w http.ResponseWriter, target string, models bool) {
	if why := s.view3DReason(); why != "" {
		fail(w, http.StatusConflict, "%s", why)
		return
	}
	args := []string{"scene", "--json", "--maps", s.c.Maps}
	if !models {
		args = append(args, "--no-models")
	}
	stdout, code, err := s.mapc(10*time.Minute, append(args, s.c.Copy, target, s.c.Cache)...)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	var res struct {
		OK    bool   `json:"ok"`
		Scene string `json:"scene"`
		Error string `json:"error"`
	}
	if json.Unmarshal(stdout, &res) != nil || !res.OK || code != 0 {
		reply(w, http.StatusOK, map[string]any{"ok": false, "exit": code, "result": json.RawMessage(orNull(stdout))})
		return
	}
	b, err := os.ReadFile(res.Scene)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "base": "/3d/", "scene": json.RawMessage(b),
		"result": json.RawMessage(stdout)})
}

func (s *Server) scenePkg(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	defer s.lock(pkg)()
	s.runScene(w, s.draftOrPackage(pkg, dir), true)
}

var levelName = regexp.MustCompile(`^[A-Za-z0-9_]{1,80}$`)

func (s *Server) sceneLevel(w http.ResponseWriter, r *http.Request) {
	level := r.PathValue("level")
	if !levelName.MatchString(level) {
		fail(w, http.StatusBadRequest, "bad level name %q", level)
		return
	}
	// ?models=0: the marker list alone (the props' paste dialog), without exporting the level's models
	s.runScene(w, level, r.URL.Query().Get("models") != "0")
}

// ground runs `mapc ground` on the latest draft into cache\ground\<pkg>\<n> and keeps the last two such folders.
func (s *Server) ground(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	if why := s.view3DReason(); why != "" {
		fail(w, http.StatusConflict, "%s", why)
		return
	}
	defer s.lock(pkg)()
	s.mu.Lock()
	s.grounds[pkg]++
	n := s.grounds[pkg]
	s.mu.Unlock()
	base := filepath.Join(s.c.Cache, "ground", pkg)
	out := filepath.Join(base, strconv.Itoa(n))
	stdout, code, err := s.mapc(2*time.Minute, "ground", "--json", "--maps", s.c.Maps, s.draftOrPackage(pkg, dir),
		s.c.Copy, out)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	var res struct {
		OK     bool `json:"ok"`
		Ground struct {
			File      string `json:"file"`
			Triangles int    `json:"triangles"`
		} `json:"ground"`
	}
	if json.Unmarshal(stdout, &res) != nil || !res.OK || code != 0 {
		reply(w, http.StatusOK, map[string]any{"ok": false, "exit": code, "result": json.RawMessage(orNull(stdout))})
		return
	}
	ents, _ := os.ReadDir(base)
	for _, e := range ents {
		if k, err := strconv.Atoi(e.Name()); err == nil && k < n-1 {
			os.RemoveAll(filepath.Join(base, e.Name()))
		}
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "n": n, "triangles": res.Ground.Triangles,
		"url": "/3d/ground/" + pkg + "/" + strconv.Itoa(n) + "/" + res.Ground.File})
}

// files3D serves the cache read-only (http.Dir keeps requests inside it); clients revalidate.
func (s *Server) files3D(w http.ResponseWriter, r *http.Request) {
	if s.c.Cache == "" || strings.Contains(r.URL.Path, "..") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.StripPrefix("/3d/", http.FileServer(http.Dir(s.c.Cache))).ServeHTTP(w, r)
}
