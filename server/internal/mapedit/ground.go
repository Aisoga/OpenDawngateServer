package mapedit

// The ground panel's side (painting a baked ground): the texture palette (`mapc textures` and `mapc texture`), the
// author's own pictures (a texture PNG added to the package, a painted mask), the drafts' package files and the bake
// preview (`mapc bake` of the latest draft).
//
// Pictures the page adds go into a scratch store per package (<work>\<pkg>\assets\ground\...), not into the package:
// each check copies the package's files and the store into the draft, so mapc sees them; Save copies the pictures
// the saved map.json names into the package and removes the painted masks (ground/paint_*.png) it no longer names.
// A painted mask's name carries a hash of its pixels, so every stroke gives a new file and undo is a change of the
// name in map.json.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png" // DecodeConfig of the pictures added
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// groundState is the ground panel's cache.
type groundState struct {
	mu       sync.Mutex
	textures []byte         // mapc textures --json of the copy
	bakes    map[string]int // the bake previews per package
}

func (s *Server) routesGround() {
	s.mux.HandleFunc("GET /api/textures", s.textures)
	s.mux.HandleFunc("POST /api/texture-pictures", s.texturePictures)
	s.mux.HandleFunc("GET /api/assets/{pkg}", s.assetList)
	s.mux.HandleFunc("POST /api/asset/{pkg}", s.assetAdd)
	s.mux.HandleFunc("GET /api/asset/{pkg}/{file...}", s.assetGet)
	s.mux.HandleFunc("POST /api/bake/{pkg}", s.bake)
	s.mux.HandleFunc("GET /bake/{pkg}/{n}/{file}", s.bakeFile)
	s.routesGroundIO() // groundio.go: the picture out and in
}

// assetName is a picture the page may add: ground/<name>.png.
var assetName = regexp.MustCompile(`^ground/[A-Za-z0-9_-]{1,64}\.png$`)

// paintName is a painted mask's file (the editor's own, removed by Save when no longer named).
var paintName = regexp.MustCompile(`^ground/paint_[A-Za-z0-9_-]+\.png$`)

const maxPicture = 4096 // a package picture's longest side (mapc's MaxPicture)
const maxPictureBytes = 64 << 20

func (s *Server) assets(pkg string) string { return filepath.Join(s.c.Work, pkg, "assets") }

// textures lists the copy's ground-worthy textures (once per editor run).
func (s *Server) textures(w http.ResponseWriter, r *http.Request) {
	if s.c.Copy == "" {
		fail(w, http.StatusConflict, "the texture palette reads the game's files: the editor has no game copy to read")
		return
	}
	s.g.mu.Lock()
	cached := s.g.textures
	s.g.mu.Unlock()
	if cached == nil {
		stdout, code, err := s.mapc(time.Minute, "textures", "--json", s.c.Copy)
		if err != nil || code != 0 {
			fail(w, http.StatusInternalServerError, "mapc textures: exit %d, %v: %s", code, err, stdout)
			return
		}
		cached = stdout
		s.g.mu.Lock()
		s.g.textures = cached
		s.g.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(cached)
}

// texturePictures exports textures as PNG into <cache>\tex<max> (served at /3d/tex<max>/...).
func (s *Server) texturePictures(w http.ResponseWriter, r *http.Request) {
	if why := s.view3DReason(); why != "" {
		fail(w, http.StatusConflict, "%s", why)
		return
	}
	var req struct {
		Paths []string `json:"paths"`
		Max   int      `json:"max"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || len(req.Paths) == 0 ||
		len(req.Paths) > 200 {
		fail(w, http.StatusBadRequest, "want {paths: [1 to 200 .dds paths], max}")
		return
	}
	if req.Max != 128 && req.Max != 512 && req.Max != 1024 && req.Max != 2048 {
		req.Max = 128
	}
	for _, p := range req.Paths {
		if !strings.HasSuffix(strings.ToLower(p), ".dds") || strings.HasPrefix(p, "-") || strings.Contains(p, "..") {
			fail(w, http.StatusBadRequest, "bad texture path %q", p)
			return
		}
	}
	sub := "tex" + strconv.Itoa(req.Max)
	args := append([]string{"texture", "--json", "--max-texture", strconv.Itoa(req.Max), s.c.Copy}, req.Paths...)
	stdout, _, err := s.mapc(5*time.Minute, append(args, filepath.Join(s.c.Cache, sub))...)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	var res struct {
		Textures []struct {
			Path  string `json:"path"`
			File  string `json:"file"`
			Error string `json:"error"`
		} `json:"textures"`
	}
	if err := json.Unmarshal(stdout, &res); err != nil {
		fail(w, http.StatusInternalServerError, "mapc's reply: %v: %s", err, stdout)
		return
	}
	out := []map[string]string{}
	for _, t := range res.Textures {
		e := map[string]string{"path": t.Path}
		if t.File != "" {
			e["url"] = "/3d/" + sub + "/" + t.File
		} else {
			e["error"] = t.Error
		}
		out = append(out, e)
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "textures": out})
}

// assetList lists the package's pictures and the ones added since (ground/*.png and any other *.png of the
// package).
func (s *Server) assetList(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	seen := map[string]bool{}
	list := []map[string]any{}
	for _, root := range []string{s.assets(pkg), dir} {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".png") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if seen[rel] || anyTile.MatchString(rel) { // the ground picture's tiles aren't textures (and are big)
				return nil
			}
			seen[rel] = true
			e := map[string]any{"file": rel, "saved": root == dir, "url": "/api/asset/" + pkg + "/" + rel}
			if f, err := os.Open(p); err == nil {
				if c, _, err := image.DecodeConfig(f); err == nil {
					e["w"], e["h"] = c.Width, c.Height
				}
				f.Close()
			}
			list = append(list, e)
			return nil
		})
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "assets": list})
}

// checkPicture reads a picture's header: a PNG of at most maxPicture pixels a side.
func checkPicture(b []byte) (int, int, error) {
	c, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || format != "png" {
		return 0, 0, fmt.Errorf("not a PNG picture")
	}
	if c.Width < 1 || c.Height < 1 || c.Width > maxPicture || c.Height > maxPicture {
		return 0, 0, fmt.Errorf("%d x %d pixels: at most %d a side", c.Width, c.Height, maxPicture)
	}
	return c.Width, c.Height, nil
}

// assetAdd stores a picture under ?name=ground/<name>.png in the package's scratch store. A different picture of
// that name (in the store or the package) gets the first free name <name>_2, _3 ...; the reply names the file.
func (s *Server) assetAdd(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	name := r.URL.Query().Get("name")
	if !assetName.MatchString(name) {
		fail(w, http.StatusBadRequest, "name %q: ground/<letters, digits, _ or ->.png", name)
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxPictureBytes+1))
	if err != nil || len(b) > maxPictureBytes {
		fail(w, http.StatusBadRequest, "a picture of at most %d bytes", maxPictureBytes)
		return
	}
	pw, ph, err := checkPicture(b)
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	defer s.lock(pkg)()
	stem := strings.TrimSuffix(name, ".png")
	for k := 1; ; k++ {
		if k > 1 {
			name = fmt.Sprintf("%s_%d.png", stem, k)
		}
		same, taken := false, false
		for _, root := range []string{s.assets(pkg), dir} {
			if old, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name))); err == nil {
				taken = true
				same = same || bytes.Equal(old, b)
			}
		}
		if same {
			break
		}
		if !taken {
			full := filepath.Join(s.assets(pkg), filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				fail(w, http.StatusInternalServerError, "%v", err)
				return
			}
			if err := os.WriteFile(full, b, 0o644); err != nil {
				fail(w, http.StatusInternalServerError, "%v", err)
				return
			}
			break
		}
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "file": name, "w": pw, "h": ph,
		"url": "/api/asset/" + pkg + "/" + name})
}

// assetGet serves a picture of the store or the package.
func (s *Server) assetGet(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	file := r.PathValue("file")
	if strings.Contains(file, "..") || strings.Contains(file, `\`) || !strings.EqualFold(filepath.Ext(file), ".png") {
		http.NotFound(w, r)
		return
	}
	for _, root := range []string{s.assets(pkg), dir} {
		full := filepath.Join(root, filepath.FromSlash(file))
		if st, err := os.Stat(full); err == nil && !st.IsDir() {
			http.ServeFile(w, r, full)
			return
		}
	}
	http.NotFound(w, r)
}

// syncDraft gives the draft the package's files and the store's pictures (the store wins), and removes what neither
// has any more; map.json is the check's own.
func (s *Server) syncDraft(pkg, dir, draft string) error {
	want := map[string]string{} // draft-relative path -> source
	for _, root := range []string{dir, s.assets(pkg)} {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			if strings.EqualFold(rel, "map.json") || strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(rel, ".tmp") {
				return nil
			}
			if info, err := d.Info(); err == nil && info.Size() <= maxTileBytes {
				want[rel] = p
			}
			return nil
		})
	}
	for rel, src := range want {
		dst := filepath.Join(draft, rel)
		ss, err := os.Stat(src)
		if err != nil {
			continue
		}
		if ds, err := os.Stat(dst); err == nil && ds.Size() == ss.Size() && ds.ModTime().Equal(ss.ModTime()) {
			continue
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
		os.Chtimes(dst, ss.ModTime(), ss.ModTime())
	}
	return filepath.WalkDir(draft, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(draft, p)
		if _, ok := want[rel]; !ok && !strings.EqualFold(rel, "map.json") {
			os.Remove(p)
		}
		return nil
	})
}

// namedPictures lists the package pictures a map.json's ground names.
func namedPictures(text []byte) map[string]bool {
	var doc struct {
		Ground struct {
			Layers []struct {
				Texture struct {
					File string `json:"file"`
				} `json:"texture"`
				Image string `json:"image"`
			} `json:"layers"`
			Stamps []struct {
				Image struct {
					File string `json:"file"`
				} `json:"image"`
			} `json:"stamps"`
			Cliff *struct {
				Texture struct {
					File string `json:"file"`
				} `json:"texture"`
			} `json:"cliff"`
			Picture []string `json:"picture"`
		} `json:"ground"`
	}
	out := map[string]bool{}
	if json.Unmarshal(text, &doc) != nil {
		return out
	}
	add := func(f string) {
		if f != "" {
			out[f] = true
		}
	}
	for _, l := range doc.Ground.Layers {
		add(l.Texture.File)
		add(l.Image)
	}
	for _, st := range doc.Ground.Stamps {
		add(st.Image.File)
	}
	if doc.Ground.Cliff != nil {
		add(doc.Ground.Cliff.Texture.File)
	}
	for _, f := range doc.Ground.Picture {
		add(f)
	}
	return out
}

// commitPictures copies the store's pictures the saved map.json names into the package and removes the painted
// masks it doesn't name; it returns the files written and removed.
func (s *Server) commitPictures(pkg, dir string, text []byte) (written, removed []string, err error) {
	named := namedPictures(text)
	for f := range named {
		if !assetName.MatchString(f) && !tileName.MatchString(f) {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(s.assets(pkg), filepath.FromSlash(f)))
		if rerr != nil {
			continue // not added here: the package's own
		}
		dst := filepath.Join(dir, filepath.FromSlash(f))
		if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, b) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return written, removed, err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return written, removed, err
		}
		written = append(written, f)
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "ground"))
	for _, e := range ents {
		f := "ground/" + e.Name()
		if !e.IsDir() && (paintName.MatchString(f) || tileName.MatchString(f)) && !named[f] {
			if os.Remove(filepath.Join(dir, "ground", e.Name())) == nil {
				removed = append(removed, f)
			}
		}
	}
	return written, removed, nil
}

// bake runs `mapc bake` on the latest draft into <work>\<pkg>\bake<n> and keeps the last two such folders.
func (s *Server) bake(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	if s.c.Copy == "" {
		fail(w, http.StatusConflict, "the bake reads the game's textures: the editor has no game copy to read")
		return
	}
	defer s.lock(pkg)()
	s.g.mu.Lock()
	if s.g.bakes == nil {
		s.g.bakes = map[string]int{}
	}
	s.g.bakes[pkg]++
	n := s.g.bakes[pkg]
	s.g.mu.Unlock()
	out := filepath.Join(s.c.Work, pkg, "bake"+strconv.Itoa(n))
	stdout, code, err := s.mapc(3*time.Minute, "bake", "--json", "--maps", s.c.Maps, s.draftOrPackage(pkg, dir),
		s.c.Copy, out)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	var res map[string]any
	if json.Unmarshal(stdout, &res) != nil || res["ok"] != true || code != 0 {
		reply(w, http.StatusOK, map[string]any{"ok": false, "exit": code, "result": json.RawMessage(orNull(stdout))})
		return
	}
	ents, _ := os.ReadDir(filepath.Join(s.c.Work, pkg))
	for _, e := range ents {
		if k, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "bake")); err == nil && strings.HasPrefix(e.Name(), "bake") &&
			k < n-1 {
			os.RemoveAll(filepath.Join(s.c.Work, pkg, e.Name()))
		}
	}
	res["n"] = n
	res["url"] = fmt.Sprintf("/bake/%s/%d/", pkg, n)
	reply(w, http.StatusOK, res)
}

var bakeFile = regexp.MustCompile(`^(ground|minimap|mask_[0-9]+)\.png$`)

func (s *Server) bakeFile(w http.ResponseWriter, r *http.Request) {
	pkg, n, file := r.PathValue("pkg"), r.PathValue("n"), r.PathValue("file")
	if _, err := strconv.Atoi(n); err != nil || !pkgName.MatchString(pkg) || !bakeFile.MatchString(file) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.c.Work, pkg, "bake"+n, file))
}
