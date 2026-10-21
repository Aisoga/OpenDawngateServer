package clientpkg

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"dawngate/server/internal/update"
	"dawngate/server/internal/update/updatetest"
)

func fakeRelease(tag string) updatetest.FakeRelease {
	return updatetest.FakeRelease{Tag: tag, Files: map[string]string{
		"Start-Client.exe": "client " + tag + strings.Repeat(" signed", 200),
		"Start-Server.exe": "server " + tag,
		"README.txt":       "readme " + tag,
		"host/dgsim.dll":   "dll " + tag,
	}}
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// serve puts a release on the fake GitHub.
func serve(f *updatetest.FakeGitHub, tag string, zipBytes []byte) {
	f.Tag, f.ZipBytes, f.Digest = tag, zipBytes, "sha256:"+sum(zipBytes)
}

func requests(f *updatetest.FakeGitHub, part string) int {
	n := 0
	for _, r := range append([]string(nil), f.Requests...) {
		if strings.Contains(r, part) {
			n++
		}
	}
	return n
}

func newPkg(t *testing.T, f *updatetest.FakeGitHub, version, dir string) *Package {
	t.Helper()
	src, err := update.Loopback(f.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{Version: version, Source: src, Dir: dir, Name: "play.example.org", Log: t.Logf})
}

func names(t *testing.T, zipFile string) []string {
	t.Helper()
	zr, err := zip.OpenReader(zipFile)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var ns []string
	for _, e := range zr.File {
		ns = append(ns, e.Name)
	}
	sort.Strings(ns)
	return ns
}

// The package of the server's own version: the release of that tag (never the latest), every release entry copied
// raw plus the ini next to Start-Client.exe, kept in the data folder and served from there again (a restart too),
// built once for concurrent first requests, rebuilt without GitHub for a changed ini, and replaced by the next
// version's.
func TestPackage(t *testing.T) {
	f := updatetest.NewFakeGitHub()
	defer f.Close()
	rel := fakeRelease("v0.7")
	relZip := rel.Zip(nil)
	serve(f, "v0.7", relZip)
	dir := t.TempDir()
	ini := []byte("server = 203.0.113.5\r\ngateway_pin = " + strings.Repeat("ab", 32) + "\r\n")
	p := newPkg(t, f, "v0.7", dir)
	if p.FileName() != "OpenDawngate-play.example.org.zip" || p.Path() != "/dawngate/download/OpenDawngate-play.example.org.zip" {
		t.Fatalf("name %q, path %q", p.FileName(), p.Path())
	}
	p.Start(ini, "abababababababab")
	file, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if file != filepath.Join(dir, "v0.7", "OpenDawngate-play.example.org.zip") {
		t.Errorf("file %s", file)
	}
	// the right tag: the release of v0.7, not the latest
	if requests(f, "/releases/tags/v0.7") != 1 || requests(f, "/releases/latest") != 0 || requests(f, "/assets/v0.7.zip") != 1 {
		t.Errorf("requests %q", f.Requests)
	}
	// the release's entries, each copied raw, and the ini beside Start-Client.exe
	want := names(t, writeTemp(t, relZip))
	want = append(want, "v0.7/"+IniName)
	sort.Strings(want)
	if got := names(t, file); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("entries %q, want %q", got, want)
	}
	pr, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	rr, _ := zip.NewReader(bytes.NewReader(relZip), int64(len(relZip)))
	orig := map[string]*zip.File{}
	for _, e := range rr.File {
		orig[e.Name] = e
	}
	for _, e := range pr.File {
		if e.Name == "v0.7/"+IniName {
			r, _ := e.Open()
			b, _ := io.ReadAll(r)
			r.Close()
			if !bytes.Equal(b, ini) {
				t.Errorf("the ini: %q", b)
			}
			continue
		}
		o := orig[e.Name]
		if o == nil || o.CRC32 != e.CRC32 || o.CompressedSize64 != e.CompressedSize64 || o.Method != e.Method || !o.Modified.Equal(e.Modified) {
			t.Errorf("%s not copied as it was", e.Name)
			continue
		}
		a, _ := o.OpenRaw()
		b, _ := e.OpenRaw()
		ab, _ := io.ReadAll(a)
		bb, _ := io.ReadAll(b)
		if !bytes.Equal(ab, bb) {
			t.Errorf("%s: the compressed bytes differ", e.Name)
		}
	}
	pr.Close()
	// a second request: from the package made before; a restart (a new Package on the same folder) too
	n := f.Count()
	if again, err := p.Get(context.Background()); err != nil || again != file {
		t.Errorf("again: %s %v", again, err)
	}
	p2 := newPkg(t, f, "v0.7", dir)
	p2.Start(ini, "")
	if again, err := p2.Get(context.Background()); err != nil || again != file || f.Count() != n {
		t.Errorf("after a restart: %s %v, %d requests more", again, err, f.Count()-n)
	}
	// another ini (a new key): rebuilt from the kept release, without GitHub
	ini2 := []byte("server = 203.0.113.5\r\ngateway_pin = " + strings.Repeat("cd", 32) + "\r\n")
	p3 := newPkg(t, f, "v0.7", dir)
	p3.Start(ini2, "")
	if _, err := p3.Get(context.Background()); err != nil || f.Count() != n {
		t.Fatalf("a new ini: %v, %d requests more", err, f.Count()-n)
	}
	if b, _ := readIni(file); !bytes.Equal(b, ini2) {
		t.Errorf("the rebuilt ini: %q", b)
	}
	// the server updated to v0.8: its own package; v0.7's removed
	serve(f, "v0.8", fakeRelease("v0.8").Zip(nil))
	p4 := newPkg(t, f, "v0.8", dir)
	p4.Start(ini, "")
	if f8, err := p4.Get(context.Background()); err != nil || filepath.Base(filepath.Dir(f8)) != "v0.8" {
		t.Fatalf("v0.8: %s %v", f8, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "v0.7")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("v0.7's package kept: %v", err)
	}
}

// Concurrent first requests wait for one build: one release request, one download.
func TestPackageConcurrent(t *testing.T) {
	f := updatetest.NewFakeGitHub()
	defer f.Close()
	serve(f, "v0.7", fakeRelease("v0.7").Zip(nil))
	p := newPkg(t, f, "v0.7", t.TempDir())
	var wg sync.WaitGroup
	files := make([]string, 8)
	errs := make([]error, 8)
	for i := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			files[i], errs[i] = p.Get(context.Background())
		}()
	}
	p.Start([]byte("server = 203.0.113.5\r\n"), "")
	wg.Wait()
	for i := range files {
		if errs[i] != nil || files[i] != files[0] {
			t.Errorf("request %d: %s %v", i, files[i], errs[i])
		}
	}
	if requests(f, "/releases/tags/") != 1 || requests(f, "/assets/") != 1 {
		t.Errorf("requests %q", f.Requests)
	}
}

// A bad asset is refused and nothing of it kept: a wrong digest, a zip with a file its BUILD.txt doesn't list, a
// release without Start-Client.exe (not the Windows release), no release of that tag. A failure is answered from
// memory for a while (GitHub isn't asked at every request).
func TestPackageRefused(t *testing.T) {
	cases := []struct {
		name, want string
		setup      func(f *updatetest.FakeGitHub)
	}{
		{"wrong digest", "SHA-256", func(f *updatetest.FakeGitHub) {
			serve(f, "v0.7", fakeRelease("v0.7").Zip(nil))
			f.Digest = "sha256:" + strings.Repeat("0", 64)
		}},
		{"unlisted file", "not in its BUILD.txt", func(f *updatetest.FakeGitHub) {
			serve(f, "v0.7", fakeRelease("v0.7").Zip(map[string]string{"v0.7/evil.exe": "x"}))
		}},
		{"not the Windows release", "not a Windows release", func(f *updatetest.FakeGitHub) {
			r := fakeRelease("v0.7")
			delete(r.Files, "Start-Client.exe")
			serve(f, "v0.7", r.Zip(nil))
		}},
		{"no such release", "404", func(f *updatetest.FakeGitHub) {
			serve(f, "v0.6", fakeRelease("v0.6").Zip(nil))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := updatetest.NewFakeGitHub()
			defer f.Close()
			c.setup(f)
			dir := t.TempDir()
			p := newPkg(t, f, "v0.7", dir)
			p.Start([]byte("server = 203.0.113.5\r\n"), "")
			_, err := p.Get(context.Background())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want %q", err, c.want)
			}
			left, _ := filepath.Glob(filepath.Join(dir, "v0.7", "*"))
			if len(left) != 0 {
				t.Errorf("left behind: %q", left)
			}
			n := f.Count()
			if _, err2 := p.Get(context.Background()); err2 == nil || f.Count() != n {
				t.Errorf("again: %v, %d requests more", err2, f.Count()-n)
			}
		})
	}
}

// A developer build has no package: it says so, asks nobody, and its error is a missing file (404).
func TestPackageDevBuild(t *testing.T) {
	f := updatetest.NewFakeGitHub()
	defer f.Close()
	p := newPkg(t, f, "dev-0123456789ab", t.TempDir())
	if p.Unavailable() == "" {
		t.Error("a developer build says it has a package")
	}
	p.Start([]byte("server = 127.0.0.1\r\n"), "ff")
	_, err := p.Get(context.Background())
	if !errors.Is(err, ErrDevBuild) || !errors.Is(err, fs.ErrNotExist) || f.Count() != 0 {
		t.Fatalf("Get: %v, %d requests", err, f.Count())
	}
	if fp, why := p.State(); fp != "ff" || !strings.Contains(why, "developer build") {
		t.Errorf("State %q %q", fp, why)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p.Path(), nil))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "developer build") {
		t.Errorf("served: %d %s", rec.Code, rec.Body.String())
	}
}

// The website's handler: the file with its Content-Disposition, HEAD and ranges by http.ServeContent.
func TestPackageServeHTTP(t *testing.T) {
	f := updatetest.NewFakeGitHub()
	defer f.Close()
	serve(f, "v0.7", fakeRelease("v0.7").Zip(nil))
	p := newPkg(t, f, "v0.7", t.TempDir())
	p.Start([]byte("server = 203.0.113.5\r\n"), "")
	file, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(file)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p.Path(), nil))
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), b) || rec.Header().Get("Content-Type") != "application/zip" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename=OpenDawngate-play.example.org.zip` {
		t.Errorf("GET: %d %v (%d bytes of %d)", rec.Code, rec.Header(), rec.Body.Len(), len(b))
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, p.Path(), nil)
	req.Header.Set("Range", "bytes=0-9")
	p.ServeHTTP(rec, req)
	if rec.Code != 206 || !bytes.Equal(rec.Body.Bytes(), b[:10]) {
		t.Errorf("a range: %d %d bytes", rec.Code, rec.Body.Len())
	}
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p.Path(), nil))
	if rec.Code != 405 {
		t.Errorf("POST: %d", rec.Code)
	}
}

func TestFileName(t *testing.T) {
	for in, want := range map[string]string{
		"play.example.org":         "OpenDawngate-play.example.org.zip",
		"203.0.113.5":              "OpenDawngate-203.0.113.5.zip",
		"Alex's  Server!":          "OpenDawngate-Alex-s-Server.zip",
		"..":                       "OpenDawngate-server.zip",
		"":                         "OpenDawngate-server.zip",
		`a/b\c:d`:                  "OpenDawngate-a-b-c-d.zip",
		strings.Repeat("x", 100):   "OpenDawngate-" + strings.Repeat("x", 64) + ".zip",
		"--Dawngate Server (EU)--": "OpenDawngate-Dawngate-Server-EU.zip",
	} {
		if got := FileName(in); got != want {
			t.Errorf("FileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMinimalIni(t *testing.T) {
	pin := strings.Repeat("ab", 32)
	if got := string(MinimalIni("203.0.113.5", 42128, 42128, pin)); !strings.Contains(got, "\r\nserver = 203.0.113.5\r\ngateway_pin = "+pin+"\r\n") ||
		strings.Contains(got, "gateway_port") {
		t.Errorf("default port: %q", got)
	}
	if got := string(MinimalIni("203.0.113.5", 42000, 42128, pin)); !strings.Contains(got, "\r\ngateway_port = 42000\r\n") {
		t.Errorf("another port: %q", got)
	}
}

func writeTemp(t *testing.T, b []byte) string {
	p := filepath.Join(t.TempDir(), "r.zip")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
