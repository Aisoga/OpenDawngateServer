package mapedit

// The ground's picture out and in (painting a baked ground in another program): Export writes the bake's tiles at
// their own size (`mapc bake --full` of the latest draft) into <out>\ground\<pkg>\, tile_<i>_<j>.png; Import stores
// painted tiles in the package's scratch store as ground/tile_<i>_<j>_<hash>.png, which the page lists as the ground's
// "picture". A tile's name carries a hash of its bytes, so a new import is a new name and undo a change of the list;
// Save copies the named tiles into the package and removes the editor's tiles it no longer names.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// maxTileBytes is the most bytes a picture tile may have (mapc's MaxPictureTileBytes): a 4096 x 4096 PNG.
const maxTileBytes = 160 << 20

// tileName is a picture tile the editor stored (Save removes such a file once map.json no longer names it).
var tileName = regexp.MustCompile(`^ground/tile_[0-9]+_[0-9]+_[0-9a-f]{10}\.png$`)

// anyTile is any picture tile of a package (the editor's or put there by hand).
var anyTile = regexp.MustCompile(`(?i)^ground/tile_[0-9]+_[0-9]+[^/]*\.png$`)

// exportFile is a file of the export folder the page may fetch.
var exportFile = regexp.MustCompile(`^(tile_[0-9]+_[0-9]+|ground|minimap)\.png$`)

func (s *Server) routesGroundIO() {
	s.mux.HandleFunc("POST /api/ground-export/{pkg}", s.groundExport)
	s.mux.HandleFunc("GET /ground-export/{pkg}/{file}", s.groundExportFile)
	s.mux.HandleFunc("POST /api/ground-tile/{pkg}", s.groundTile)
}

// exportDir is the package's export folder.
func (s *Server) exportDir(pkg string) string { return filepath.Join(s.c.Out, "ground", pkg) }

// groundExport writes the latest draft's bake with every tile at its own size into the export folder.
func (s *Server) groundExport(w http.ResponseWriter, r *http.Request) {
	pkg, dir := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	if s.c.Copy == "" {
		fail(w, http.StatusConflict, "the bake reads the game's textures: the editor has no game copy to read")
		return
	}
	defer s.lock(pkg)()
	out := s.exportDir(pkg)
	if old, err := filepath.Glob(filepath.Join(out, "tile_*.png")); err == nil { // another tile count leaves none behind
		for _, f := range old {
			os.Remove(f)
		}
	}
	stdout, code, err := s.mapc(10*time.Minute, "bake", "--json", "--full", "--maps", s.c.Maps, s.draftOrPackage(pkg, dir),
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
	res["dir"] = out
	res["url"] = "/ground-export/" + pkg + "/"
	reply(w, http.StatusOK, res)
}

// groundExportFile serves a file of the export folder.
func (s *Server) groundExportFile(w http.ResponseWriter, r *http.Request) {
	pkg, file := r.PathValue("pkg"), r.PathValue("file")
	if !pkgName.MatchString(pkg) || !exportFile.MatchString(file) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.exportDir(pkg), file))
}

// groundTile stores a painted tile (?i=&j=, the body a PNG) in the scratch store as ground/tile_<i>_<j>_<hash>.png.
func (s *Server) groundTile(w http.ResponseWriter, r *http.Request) {
	pkg, _ := s.pkgDir(w, r)
	if pkg == "" {
		return
	}
	i, erri := strconv.Atoi(r.URL.Query().Get("i"))
	j, errj := strconv.Atoi(r.URL.Query().Get("j"))
	if erri != nil || errj != nil || i < 0 || j < 0 || i > 3 || j > 3 {
		fail(w, http.StatusBadRequest, "want ?i=<0..3>&j=<0..3>: the tile from the west and from the south")
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxTileBytes+1))
	if err != nil || len(b) > maxTileBytes {
		fail(w, http.StatusBadRequest, "a tile of at most %d bytes", maxTileBytes)
		return
	}
	pw, ph, err := checkPicture(b)
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	sum := sha256.Sum256(b)
	name := fmt.Sprintf("ground/tile_%d_%d_%s.png", i, j, hex.EncodeToString(sum[:])[:10])
	defer s.lock(pkg)()
	full := filepath.Join(s.assets(pkg), filepath.FromSlash(name))
	if _, err := os.Stat(full); err != nil {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			fail(w, http.StatusInternalServerError, "%v", err)
			return
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			fail(w, http.StatusInternalServerError, "%v", err)
			return
		}
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "file": name, "w": pw, "h": ph,
		"url": "/api/asset/" + pkg + "/" + name})
}
