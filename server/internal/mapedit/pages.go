package mapedit

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"

	"dawngate/server/internal/version"
)

// API is the version of the interface between the page and this backend (the routes, their arguments and replies).
// The page's editor.json names the one it was made for; a page with another is refused (VersionError). It goes up
// when a route or a reply changes incompatibly (removed, renamed, another meaning); a new route or a new member old
// pages ignore keeps it.
const API = 1

// ManifestFile is the page's manifest: {"version": "<the page's release>", "api": <API it was made for>}.
const ManifestFile = "editor.json"

// Manifest is the page's editor.json.
type Manifest struct {
	Version string `json:"version"`
	API     int    `json:"api"`
}

// Pages is where the page, its templates and its manifest are.
type Pages struct {
	Web       string // the folder served at / (index.html, the scripts)
	Templates string // the template packages New from template offers beside the maps folder's
	Manifest  Manifest
}

// VersionError is a page made for another API than this backend's.
type VersionError struct {
	Page    Manifest
	Backend int    // API
	Release string // this program's release (version.String())
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("the map editor's page (version %s) is made for editor interface %d, but this program "+
		"(release %s) has interface %d: use the map editor made for this release", orUnknown(e.Page.Version),
		e.Page.API, e.Release, e.Backend)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// LoadPages finds the page in dir. The page is dir itself (a release's editor\: index.html, templates\ and
// editor.json inside it), or dir\web when dir has web\index.html (a checkout of the page's repository). The
// templates\ and editor.json are in the page's folder or, when it has none, beside it (a checkout's web\ given
// directly: the page repository's DEVELOPING.md). It checks the manifest: a missing or unreadable editor.json is an
// error, a page made for another API a *VersionError.
func LoadPages(dir string) (*Pages, error) {
	if dir == "" {
		return nil, errors.New("no folder of the map editor's page given")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	p := &Pages{Web: abs}
	if isFile(filepath.Join(abs, "web", "index.html")) {
		p.Web = filepath.Join(abs, "web")
	} else if !isFile(filepath.Join(abs, "index.html")) {
		return nil, fmt.Errorf("%s: no map editor here (no index.html)", abs)
	}
	// inside the page's folder, else beside it
	near := func(name string, ok func(string) bool) string {
		if c := filepath.Join(p.Web, name); ok(c) {
			return c
		}
		if c := filepath.Join(filepath.Dir(p.Web), name); ok(c) {
			return c
		}
		return filepath.Join(p.Web, name)
	}
	p.Templates = near("templates", isDir)
	manifest := near(ManifestFile, isFile)
	b, err := os.ReadFile(manifest)
	if err != nil {
		return nil, fmt.Errorf("%s: the map editor's %s: %v", abs, ManifestFile, err)
	}
	if err := json.Unmarshal(b, &p.Manifest); err != nil {
		return nil, fmt.Errorf("%s: %v", manifest, err)
	}
	if p.Manifest.API != API {
		return p, &VersionError{Page: p.Manifest, Backend: API, Release: version.String()}
	}
	return p, nil
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// PageError is why the page isn't served (no page folder, no manifest, another API), or nil. The page's address
// then shows it as a short error page; the API routes work regardless.
func (s *Server) PageError() error { return s.pageErr }

const errorPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>Map editor: not started</title>
<style>body{font:15px system-ui,sans-serif;margin:3em;max-width:48em;color:#222}code{background:#eee}</style></head>
<body><h1>The map editor can't start</h1><p>%s</p></body></html>
`

// page serves the page's folder, or the error page when it can't be served.
func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if s.pageErr != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, errorPage, html.EscapeString(s.pageErr.Error()))
		return
	}
	s.files.ServeHTTP(w, r)
}
