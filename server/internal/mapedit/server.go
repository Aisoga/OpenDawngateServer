// Package mapedit is the map editor's backend: a local web app that edits map packages (map.json, format 1). It
// serves the page (a folder: a release's editor\ or a checkout of the page's repository, LoadPages) on a loopback
// address only and reaches the map pipeline through the mapc command line alone, as a child process (Config.Mapc
// with Config.MapcArgs: Start-Client.exe --mapc in a release): every edit is written to a draft package and checked
// with `mapc preview --json` (the rule checks plus the derived layers as pictures), Save writes the package's
// map.json in its own layout (unchanged values keep their bytes), Build runs `mapc build` into the output folder.
// Test hands the saved package to the game (Config.Game, test.go); without one it is off.
//
// The program that serves it fills a Config (ReleaseConfig for Start-Client's editor mode; tools/mapedit for the
// developers), calls NewServer and serves the Server, an http.Handler, on a loopback listener.
package mapedit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/mapdist"
	"dawngate/server/internal/mapedit/textjson"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/version"
)

// Config is where the editor finds its inputs and puts its outputs.
type Config struct {
	Maps     string   // the folder of map packages (the release's maps\ beside Start-Client)
	Mapc     string   // the mapc program (Start-Client.exe itself in a release)
	MapcArgs []string // the arguments before mapc's own (["--mapc"] for Start-Client); none for a plain mapc
	Copy     string   // a game copy to read (the folder with game\Data); empty: no build, no catalog checks
	Out      string   // Build's output folder
	Cache    string   // the 3-D view's exports (models, textures, scene lists, grounds)
	Work     string   // scratch: drafts and their previews; empty: a temporary folder Close removes
	Pages    string   // the page (LoadPages): a release's editor\ folder or a checkout of the page's repository
	Game     Game     // Test's game (test.go); nil: Test is off
	TestOff  string   // why Test is off without a Game (default: noGame)
}

// ReleaseConfig is the editor's Config in a release: the maps folder beside Start-Client (launcher.MapsDir), the
// player copy to read (mapc reads only its shipped catalog, AssetData.snap, so the installed zz_ maps don't matter),
// Start-Client itself as mapc (exe --mapc), and the outputs, which are converted game art, beside the copy in the
// data folder's editor\ (out\, cache\), never beside the page. Pages and Game are the caller's.
func ReleaseConfig(exe, mapsDir string, l setup.Layout, pages string) Config {
	return Config{Maps: mapsDir, Mapc: exe, MapcArgs: []string{"--mapc"}, Copy: l.ClientCopy(),
		Out: filepath.Join(l.Root, "editor", "out"), Cache: filepath.Join(l.Root, "editor", "cache"), Pages: pages}
}

// Server is the editor's HTTP side.
type Server struct {
	c       Config
	mux     *http.ServeMux
	pages   *Pages                 // nil when pageErr says why
	pageErr error                  // why the page isn't served (pages.go)
	files   http.Handler           // the page's folder
	ownWork bool                   // Work is a temporary folder of NewServer's own
	mu      sync.Mutex             // guards locks and revs
	locks   map[string]*sync.Mutex // one per package: drafts, saves and builds don't overlap
	revs    map[string]int
	grounds map[string]int // the 3-D view's ground exports per package
	props   propsState
	g       groundState // ground.go
}

var pkgName = regexp.MustCompile(`^[A-Za-z0-9_]{1,80}$`)

// NewServer checks the configuration and sets up the routes. The page's problems (no folder, no editor.json, a
// page made for another API) don't fail it: PageError says why, and the page's address shows it.
func NewServer(c Config) (*Server, error) {
	if st, err := os.Stat(c.Maps); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("the maps folder %q: not a folder", c.Maps)
	}
	if _, err := os.Stat(c.Mapc); err != nil {
		return nil, fmt.Errorf("mapc: %v", err)
	}
	s := &Server{c: c, mux: http.NewServeMux(), locks: map[string]*sync.Mutex{}, revs: map[string]int{},
		grounds: map[string]int{}}
	if s.c.Work == "" {
		work, err := os.MkdirTemp("", "mapedit-")
		if err != nil {
			return nil, err
		}
		s.c.Work, s.ownWork = work, true
	}
	s.pages, s.pageErr = LoadPages(c.Pages)
	if s.pageErr == nil {
		s.files = http.FileServer(http.Dir(s.pages.Web))
	}
	s.mux.HandleFunc("GET /", s.page)
	s.mux.HandleFunc("GET /api/state", s.state)
	s.mux.HandleFunc("GET /api/map/{pkg}", s.getMap)
	s.mux.HandleFunc("POST /api/check/{pkg}", s.check)
	s.mux.HandleFunc("POST /api/save/{pkg}", s.save)
	s.mux.HandleFunc("POST /api/new", s.newMap)
	s.mux.HandleFunc("POST /api/build/{pkg}", s.build)
	s.mux.HandleFunc("GET /preview/{pkg}/{rev}/{file}", s.previewFile)
	s.routes3D()
	s.routesTest()
	s.routesProps()
	s.routesGround()
	return s, nil
}

// Close removes the scratch folder NewServer made (a Config without Work). A Test's game is the caller's to end.
func (s *Server) Close() error {
	if s.ownWork {
		return os.RemoveAll(s.c.Work)
	}
	return nil
}

// ServeHTTP lets only this machine's pages in: the Host must be a loopback name (no DNS rebinding), and a POST's
// Origin, when sent, this server.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		http.Error(w, "loopback only", http.StatusForbidden)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && r.Method != http.MethodGet && o != "http://"+r.Host {
		http.Error(w, "foreign origin", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) lock(pkg string) func() {
	s.mu.Lock()
	l := s.locks[pkg]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[pkg] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	b, _ := json.Marshal(v)
	w.Write(b)
}

func fail(w http.ResponseWriter, code int, format string, a ...any) {
	reply(w, code, map[string]any{"ok": false, "error": fmt.Sprintf(format, a...)})
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// pkgDir returns a package's folder from the request's {pkg}, or "" (and the reply) when it's no package.
func (s *Server) pkgDir(w http.ResponseWriter, r *http.Request) (string, string) {
	pkg := r.PathValue("pkg")
	if !pkgName.MatchString(pkg) {
		fail(w, http.StatusBadRequest, "bad package name %q", pkg)
		return "", ""
	}
	dir := filepath.Join(s.c.Maps, pkg)
	if _, err := os.Stat(filepath.Join(dir, "map.json")); err != nil {
		fail(w, http.StatusNotFound, "no package %s", pkg)
		return "", ""
	}
	return pkg, dir
}

type mapInfo struct {
	Pkg       string `json:"pkg"`
	Name      string `json:"name"`
	Display   string `json:"display"`
	Tag       string `json:"tag"`
	Mesh      bool   `json:"mesh"`
	Delivered bool   `json:"delivered,omitempty"` // map delivery's package (read-only here)
}

// list reads the packages of a folder (the maps folder or the templates).
func list(dir string) []mapInfo {
	ents, _ := os.ReadDir(dir)
	var out []mapInfo
	for _, e := range ents {
		if !e.IsDir() || !pkgName.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name(), "map.json"))
		if err != nil {
			continue
		}
		mi := mapInfo{Pkg: e.Name()}
		json.Unmarshal(b, &mi)
		mi.Pkg = e.Name()
		mi.Mesh = true // every map gets the compiler's shared PhysX floor; nothing to cook
		mi.Delivered = mapdist.Delivered(filepath.Join(dir, e.Name()))
		out = append(out, mi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pkg < out[j].Pkg })
	return out
}

// templates is the folder of the page's template packages, or "".
func (s *Server) templates() string {
	if s.pages == nil {
		return ""
	}
	return s.pages.Templates
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	st := map[string]any{
		"api": API, "release": version.String(),
		"maps": s.list(), "mapsDir": s.c.Maps, "copy": s.c.Copy, "out": s.c.Out,
		"templates": []mapInfo{},
		"test":      map[string]any{"enabled": s.testReason() == "", "reason": s.testReason()},
		"view3d":    map[string]any{"enabled": s.view3DReason() == "", "reason": s.view3DReason()},
	}
	if t := s.templates(); t != "" {
		if l := list(t); l != nil {
			st["templates"] = l
		}
	}
	if s.pages != nil {
		st["page"] = s.pages.Manifest
	}
	if s.pageErr != nil {
		st["pageError"] = s.pageErr.Error()
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) list() []mapInfo { return list(s.c.Maps) }

func (s *Server) getMap(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, "map.json"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	res := map[string]any{"pkg": pkg, "text": string(b), "sha256": sum(b)}
	if from, ok := mapdist.DeliveredFrom(dir); ok {
		res["delivered"] = map[string]any{"server": from, "why": deliveredWhy(pkg, from)}
	}
	reply(w, http.StatusOK, res)
}

// deliveredWhy is why a delivered package isn't saved: the next login to its server would replace the edits
// (MAPED-DIST's Replacing a version).
func deliveredWhy(pkg, server string) string {
	from := "a server"
	if server != "" {
		from = "the server " + server
	}
	return fmt.Sprintf("%s came from %s by map delivery: Start-Client replaces it when that server offers another "+
		"version, so it isn't saved here. Save as my own copy keeps your edits in a package of your own", pkg, from)
}

// mapc runs the compiler and returns its stdout (one JSON object with --json) and exit code.
func (s *Server) mapc(timeout time.Duration, args ...string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.c.Mapc, append(append([]string(nil), s.c.MapcArgs...), args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.Bytes(), ee.ExitCode(), nil
	}
	if err != nil {
		return nil, -1, fmt.Errorf("mapc: %v %s", err, errb.String())
	}
	return out.Bytes(), 0, nil
}

// readDoc reads the request's document (the page's JSON.stringify of map.json) and merges it into the package's
// file on disk: the text to check or save.
func readDoc(r *http.Request, file []byte) ([]byte, []byte, error) {
	doc, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return nil, nil, err
	}
	text, err := textjson.Merge(file, doc)
	return doc, text, err
}

// check writes the document as a draft package beside nothing else and runs `mapc preview --json` on it: the rule
// checks' report and, when the structural rules pass, the derived layers.
func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	defer s.lock(pkg)()
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
	s.mu.Lock()
	s.revs[pkg]++
	rev := s.revs[pkg]
	s.mu.Unlock()
	draft := filepath.Join(s.c.Work, pkg, "draft", pkg)
	out := filepath.Join(s.c.Work, pkg, "rev"+strconv.Itoa(rev))
	if err := os.MkdirAll(draft, 0o755); err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(draft, "map.json"), text, 0o644); err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if err := s.syncDraft(pkg, dir, draft); err != nil { // the package's pictures and the added ones (ground.go)
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	// with a game copy, a second run adds the rules that need its catalog (the report is a superset of preview's)
	var full []byte
	var fullErr error
	var wg sync.WaitGroup
	// and, when the map has props, what they place (both teams, with their map.json entries) for the page's layers
	var placed json.RawMessage
	var placing []map[string]any
	if s.c.Copy != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			full, _, fullErr = s.mapc(time.Minute, "check", "--json", "--copy", s.c.Copy, "--maps", s.c.Maps, draft)
		}()
		if hasProps(text) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				placed, placing = s.placed(draft)
			}()
		}
	}
	stdout, code, err := s.mapc(time.Minute, "preview", "--json", "--maps", s.c.Maps, draft, out)
	wg.Wait()
	if err == nil {
		err = fullErr
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	res := map[string]any{"rev": rev, "exit": code, "text": string(text), "images": fmt.Sprintf("/preview/%s/%d/", pkg, rev)}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(stdout, &v); err != nil {
		fail(w, http.StatusInternalServerError, "mapc's reply: %v: %s", err, stdout)
		return
	}
	switch {
	case v["findings"] != nil: // the file didn't load: the report itself
		res["report"] = json.RawMessage(stdout)
	case v["report"] != nil:
		res["report"] = v["report"]
	}
	if v["error"] != nil {
		res["error"] = v["error"]
	}
	var fr map[string]json.RawMessage
	if full != nil && json.Unmarshal(full, &fr) == nil && fr["findings"] != nil {
		res["report"] = mergeFindings(json.RawMessage(full), placing)
		res["copyChecked"] = true
	}
	switch {
	case placed != nil:
		res["placed"] = placed
	case !hasProps(text):
		res["placed"] = json.RawMessage("[]")
	}
	if code == 0 {
		if idx, err := os.ReadFile(filepath.Join(out, "preview.json")); err == nil {
			res["preview"] = json.RawMessage(idx)
		}
	}
	// keep the last two revisions' pictures (the page may still be loading the previous one)
	ents, _ := os.ReadDir(filepath.Join(s.c.Work, pkg))
	for _, e := range ents {
		if n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "rev")); err == nil && strings.HasPrefix(e.Name(), "rev") &&
			n < rev-1 {
			os.RemoveAll(filepath.Join(s.c.Work, pkg, e.Name()))
		}
	}
	reply(w, http.StatusOK, res)
}

var previewFile = regexp.MustCompile(`^[a-z0-9_]+\.png$`)

func (s *Server) previewFile(w http.ResponseWriter, r *http.Request) {
	pkg, rev, file := r.PathValue("pkg"), r.PathValue("rev"), r.PathValue("file")
	if _, err := strconv.Atoi(rev); err != nil || !pkgName.MatchString(pkg) || !previewFile.MatchString(file) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.c.Work, pkg, "rev"+rev, file))
}

// save writes the document into the package's map.json in the file's layout. It refuses when the file changed on
// disk since the page read it (base), unless forced; an unchanged document leaves the file alone. A delivered
// package (mapdist's marker) is never written: Save as my own copy (newMap) is the way.
func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	if from, ok := mapdist.DeliveredFrom(dir); ok {
		reply(w, http.StatusForbidden, map[string]any{"ok": false, "delivered": true, "error": deliveredWhy(pkg, from)})
		return
	}
	defer s.lock(pkg)()
	path := filepath.Join(dir, "map.json")
	file, err := os.ReadFile(path)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if base := r.URL.Query().Get("base"); base != sum(file) && r.URL.Query().Get("force") != "1" {
		reply(w, http.StatusConflict, map[string]any{"ok": false, "sha256": sum(file),
			"error": "map.json changed on disk since it was opened"})
		return
	}
	_, text, err := readDoc(r, file)
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	pics, gone, err := s.commitPictures(pkg, dir, text) // the added pictures map.json names (ground.go)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if bytes.Equal(text, file) {
		reply(w, http.StatusOK, map[string]any{"ok": true, "written": false, "sha256": sum(file), "text": string(file),
			"pictures": pics, "removed": gone})
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, text, 0o644); err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "written": true, "sha256": sum(text), "text": string(text),
		"pictures": pics, "removed": gone})
}

// newMap makes a package from a template package: its map.json with a new name, display name, tag and prefix (the
// rest as it is, unknown members too), and its own ground pictures (ground/), which its map.json may name. The
// cooked mesh isn't copied: it belongs to the template's layout. The template is a package of the maps folder
// (source "" or "maps") or of the page's templates (source "templates"). keepAdded (Save as my own copy) also takes
// the pictures added to the template in this run and not saved into it yet (its store), so the page can save its
// document into the copy; map delivery's marker is never copied, so the copy is the player's own.
func (s *Server) newMap(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Template, Source, Pkg, Name, Display, Tag, Prefix string
		KeepAdded                                         bool
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&q); err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	if q.Pkg == "" {
		q.Pkg = q.Name
	}
	if !pkgName.MatchString(q.Template) || !pkgName.MatchString(q.Pkg) || !pkgName.MatchString(q.Name) {
		fail(w, http.StatusBadRequest, "names are letters, digits and _ only")
		return
	}
	src := filepath.Join(s.c.Maps, q.Template)
	switch q.Source {
	case "", "maps":
	case "templates":
		if s.templates() == "" {
			fail(w, http.StatusNotFound, "no templates (%v)", s.pageErr)
			return
		}
		src = filepath.Join(s.templates(), q.Template)
		q.KeepAdded = false // the store is the maps folder's packages'
	default:
		fail(w, http.StatusBadRequest, "source %q: maps or templates", q.Source)
		return
	}
	file, err := os.ReadFile(filepath.Join(src, "map.json"))
	if err != nil {
		fail(w, http.StatusNotFound, "no template %s", q.Template)
		return
	}
	dst := filepath.Join(s.c.Maps, q.Pkg)
	if _, err := os.Stat(dst); err == nil {
		fail(w, http.StatusConflict, "%s exists already", q.Pkg)
		return
	}
	set := map[string]string{}
	order := []string{"name", "display", "tag", "prefix"}
	for k, v := range map[string]string{"name": q.Name, "display": q.Display, "tag": q.Tag, "prefix": q.Prefix} {
		if v != "" {
			b, _ := json.Marshal(v)
			set[k] = string(b)
		}
	}
	text, err := textjson.SetMembers(file, set, order)
	if err != nil {
		fail(w, http.StatusBadRequest, "the template: %v", err)
		return
	}
	defer s.lock(q.Pkg)()
	// MAPED-EDIT6: pictures added for an earlier package of this name (deleted since, in the same run) would still be
	// in its store, and a new picture of the same name would get _2
	if err := os.RemoveAll(s.assets(q.Pkg)); err != nil {
		fail(w, http.StatusInternalServerError, "the old store: %v", err)
		return
	}
	if q.KeepAdded && q.Template != q.Pkg {
		if err := copyGround(s.assets(q.Template), s.assets(q.Pkg)); err != nil {
			fail(w, http.StatusInternalServerError, "the added pictures: %v", err)
			return
		}
	}
	if err := os.Mkdir(dst, 0o755); err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(dst, "map.json"), text, 0o644); err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	// MAPED-EDIT5: without them a template whose ground names its own PNGs gave a copy that fails ground.file
	if err := copyGround(src, dst); err != nil {
		fail(w, http.StatusInternalServerError, "the template's pictures: %v", err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "pkg": q.Pkg})
}

// copyGround copies the regular files of a package's ground/ folder (and its subfolders) into another package.
func copyGround(src, dst string) error {
	root := filepath.Join(src, "ground")
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
}

// build runs `mapc build --json` on the saved package into the output folder. Nothing writes into a game copy:
// Start-Client's setup installs the maps folder's packages.
func (s *Server) build(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	if s.c.Copy == "" {
		fail(w, http.StatusConflict, "no game copy to build from")
		return
	}
	out := s.c.Out
	if err := os.MkdirAll(out, 0o755); err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	defer s.lock(pkg)()
	args := []string{"build", "--json", "--maps", s.c.Maps}
	if r.URL.Query().Get("mesh") == "0" {
		args = append(args, "--no-mesh")
	}
	stdout, code, err := s.mapc(5*time.Minute, append(args, dir, s.c.Copy, out)...)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"exit": code, "result": json.RawMessage(orNull(stdout))})
}

func orNull(b []byte) []byte {
	if !json.Valid(b) {
		return []byte("null")
	}
	return b
}
