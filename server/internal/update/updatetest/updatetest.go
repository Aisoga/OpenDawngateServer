// Package updatetest fakes a release zip and GitHub's release API for the update tests (Start-Client never links it).
package updatetest

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"

	"dawngate/server/internal/update"
)

// FakeRelease is a release zip as make_release.ps1 writes it, for tests: one top folder <Tag>\ with Files and a
// BUILD.txt (CRLF, backslash paths) listing them.
type FakeRelease struct {
	Tag   string
	Files map[string]string // slash path → content
}

// BuildText is the release's BUILD.txt.
func (r FakeRelease) BuildText() string {
	lines := []string{"release  " + r.Tag, "version  " + r.Tag + " (Start-Server.exe and Start-Client.exe; a player's Start-Client and the server must have the same)",
		"commit   0123456789", "", "SHA-256 of the files:"}
	paths := make([]string, 0, len(r.Files))
	for p := range r.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		h := sha256.Sum256([]byte(r.Files[p]))
		lines = append(lines, hex.EncodeToString(h[:])+"  "+strings.ReplaceAll(p, "/", `\`))
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// Zip is the release's zip; extra entries (slash path → content) are added unlisted, e.g. to test the refusals.
func (r FakeRelease) Zip(extra map[string]string) []byte {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	add := func(name, content string) {
		w, err := z.Create(name)
		if err != nil {
			panic(err)
		}
		w.Write([]byte(content))
	}
	z.Create(r.Tag + "/")
	for p, c := range r.Files {
		add(r.Tag+"/"+p, c)
	}
	add(r.Tag+"/"+update.BuildFile, r.BuildText())
	for p, c := range extra {
		add(p, c)
	}
	if err := z.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// FakeGitHub serves GitHub's latest-release answer (also as the answer for its tag, releases/tags/<Tag>; any other
// tag is 404) and a release's zip (through a redirect, as GitHub's downloads
// are) on a loopback address, and counts the requests. Tests only.
type FakeGitHub struct {
	*httptest.Server
	mu       sync.Mutex
	Tag      string // the latest release's tag
	ZipName  string // its asset's name (default <Tag>.zip)
	ZipBytes []byte
	Digest   string // the asset's digest field ("": none)
	Size     int64  // the asset's size field (0: the zip's length)
	Draft    bool
	Requests []string // "GET <path>"
}

// NewFakeGitHub starts the fake; Close it.
func NewFakeGitHub() *FakeGitHub {
	f := &FakeGitHub{}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// Count is how many requests the fake got.
func (f *FakeGitHub) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Requests)
}

func (f *FakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.Requests = append(f.Requests, r.Method+" "+r.URL.Path)
	tag, name, data, digest, size, draft := f.Tag, f.ZipName, f.ZipBytes, f.Digest, f.Size, f.Draft
	f.mu.Unlock()
	if name == "" {
		name = tag + ".zip"
	}
	if size == 0 {
		size = int64(len(data))
	}
	dl := "/" + update.Repo + "/releases/download/" + tag + "/" + name
	switch r.URL.Path {
	case "/repos/" + update.Repo + "/releases/latest", "/repos/" + update.Repo + "/releases/tags/" + tag:
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no User-Agent", http.StatusForbidden)
			return
		}
		asset := map[string]any{"name": name, "browser_download_url": f.URL + dl, "size": size}
		if digest != "" {
			asset["digest"] = digest
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "draft": draft, "prerelease": false,
			"assets": []any{map[string]any{"name": tag + "-linux.tar.gz", "browser_download_url": f.URL + "/x.tgz", "size": 1}, asset}})
	case dl:
		http.Redirect(w, r, "/assets/"+name, http.StatusFound)
	case "/assets/" + name:
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Write(data)
	default:
		http.NotFound(w, r)
	}
}
