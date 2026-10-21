// Package clientpkg is the server's client package (dgserver --client-package): the Windows release of the server's
// own version with a Start-Client.ini added next to Start-Client.exe that names this server and trusts its tunnel key,
// so a player unzips it, starts Start-Client.exe and logs in, with nothing to type and no fingerprint to compare.
//
// The release comes from GitHub once per version (update.Source.ByTag: the release of the server's own tag, never the
// latest, since a server plays only with Start-Client of its own release), downloaded and checked as Start-Client's
// self-update checks a release (the size and digest GitHub names, every file against the release's BUILD.txt, both
// programs present). The package copies the release zip's entries as they are (zip.Writer.Copy: no recompression, the
// programs byte for byte, so their signatures stay valid) and adds the ini. Both live in the data folder,
// client-package\<version>\, built at the first need and kept; a server of another version builds its own and removes
// the old ones. A developer build has no release and so no package (ErrDevBuild).
package clientpkg

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/update"
)

// URLPrefix is where the package is served (on the HTTP port and the website): URLPrefix + FileName(name).
const URLPrefix = "/dawngate/download/"

// DirName is the package's folder in the data folder.
const DirName = "client-package"

// IniName is the file the package adds next to Start-Client.exe.
const IniName = "Start-Client.ini"

// ClientExe is the program the ini goes next to.
const ClientExe = "Start-Client.exe"

// failedFor is how long a failed build is answered from memory before the next request tries again (GitHub isn't
// asked at every request while it is unreachable).
const failedFor = 5 * time.Minute

// buildTime bounds one build (the download of about 30 MB, the check, the copy).
const buildTime = 15 * time.Minute

type devBuildError struct{}

func (devBuildError) Error() string {
	return "this server is a developer build, not a numbered release (v<major>.<minor>): there is no release of its " +
		"version to hand out, so it has no client package"
}

// Is makes a developer build's missing package a missing file (a 404 on the HTTP port).
func (devBuildError) Is(target error) bool { return target == fs.ErrNotExist }

// ErrDevBuild is the package of a server that isn't a numbered release: there is none.
var ErrDevBuild error = devBuildError{}

// Config is a package's setup.
type Config struct {
	Version string        // the server's version (version.String()): the release to package
	Source  update.Source // GitHub (update.GitHub), or a fake on a loopback address in tests (update.Loopback)
	Dir     string        // the package's folder: <data>\client-package
	Name    string        // the server's name or address, for the file name (FileName)
	Log     func(format string, a ...any)
}

// Package is one server's client package.
type Package struct {
	cfg   Config
	file  string
	ready chan struct{} // closed by Start, which set ini
	ini   []byte

	mu      sync.Mutex
	running chan struct{} // closed when the running build ends (nil: none runs)
	built   string        // the package file once built
	err     error         // the last build's error
	failed  time.Time
	// the fingerprint of the tunnel key the ini trusts
	fingerprint string

	bg       sync.WaitGroup     // Start's background build
	bgCancel context.CancelFunc // ends it at Close (nil before Start)
}

// New makes the package; Start gives it its ini and starts the first build.
func New(cfg Config) *Package {
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	return &Package{cfg: cfg, file: FileName(cfg.Name), ready: make(chan struct{})}
}

// FileName is the package's file name for a server's name or address: OpenDawngate-<name>.zip, the name cut to
// letters, digits, dots, dashes and underscores (anything else a dash; at most 64 characters; "server" for none).
func FileName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if !ok || r == '-' {
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = true
			continue
		}
		b.WriteRune(r)
		dash = false
	}
	s := b.String()
	if len(s) > 64 {
		s = s[:64]
	}
	s = strings.Trim(s, "-.")
	if s == "" {
		s = "server"
	}
	return "OpenDawngate-" + s + ".zip"
}

// FileName is this package's file name.
func (p *Package) FileName() string { return p.file }

// Path is the package's URL path.
func (p *Package) Path() string { return URLPrefix + p.file }

// Unavailable says why the server has no package ("" when it has one, or will once built).
func (p *Package) Unavailable() string {
	if !update.IsRelease(p.cfg.Version) {
		return ErrDevBuild.Error()
	}
	return ""
}

// State is the tunnel key's fingerprint (once Start gave it) and Unavailable, for the download pages.
func (p *Package) State() (fingerprint, unavailable string) {
	p.mu.Lock()
	fp := p.fingerprint
	p.mu.Unlock()
	return fp, p.Unavailable()
}

// Start gives the package its Start-Client.ini and the fingerprint of the key it trusts (known once the tunnel's
// gateway runs) and builds it in the background, so the first player doesn't wait and the log says early whether it
// works. Requests before Start wait for it.
func (p *Package) Start(ini []byte, fingerprint string) {
	p.mu.Lock()
	p.fingerprint = fingerprint
	p.mu.Unlock()
	p.ini = ini
	close(p.ready)
	if !update.IsRelease(p.cfg.Version) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), buildTime)
	p.mu.Lock()
	p.bgCancel = cancel
	p.mu.Unlock()
	p.bg.Add(1)
	go func() {
		defer p.bg.Done()
		defer cancel()
		p.Get(ctx)
	}()
}

// Close ends Start's background build: one not begun yet doesn't begin, one running is waited for.
func (p *Package) Close() {
	p.mu.Lock()
	cancel := p.bgCancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	p.bg.Wait()
}

// Get returns the package file, built first if need be: concurrent callers wait for one build. A failed build is
// answered from memory for failedFor, then tried again; a developer build never builds (ErrDevBuild).
func (p *Package) Get(ctx context.Context) (string, error) {
	select {
	case <-p.ready:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		p.mu.Lock()
		if p.built != "" {
			if _, err := os.Stat(p.built); err == nil {
				f := p.built
				p.mu.Unlock()
				return f, nil
			}
			p.built = "" // deleted meanwhile: built again
		}
		if p.err != nil && (errors.Is(p.err, ErrDevBuild) || time.Since(p.failed) < failedFor) {
			err := p.err
			p.mu.Unlock()
			return "", err
		}
		if ch := p.running; ch != nil {
			p.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		ch := make(chan struct{})
		p.running = ch
		p.mu.Unlock()

		// the build doesn't end with the request that started it: others may wait for it
		bctx, cancel := context.WithTimeout(context.Background(), buildTime)
		start := time.Now()
		f, err := p.build(bctx)
		cancel()
		p.mu.Lock()
		p.running, p.built, p.err = nil, f, err
		if err != nil {
			p.failed = time.Now()
		}
		p.mu.Unlock()
		close(ch)
		if err != nil {
			p.cfg.Log("client package: %v", err)
		} else {
			p.cfg.Log("client package: %s ready (%s, %.1fs)", p.Path(), f, time.Since(start).Seconds())
		}
		return f, err
	}
}

// build makes the package of the server's version, or finds it made with the same ini.
func (p *Package) build(ctx context.Context) (string, error) {
	tag := p.cfg.Version
	if !update.IsRelease(tag) {
		return "", ErrDevBuild
	}
	dir := filepath.Join(p.cfg.Dir, tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dir, p.file)
	if have, err := readIni(out); err == nil && bytes.Equal(have, p.ini) {
		p.removeOld(tag)
		return out, nil
	}
	rel := filepath.Join(dir, tag+".zip")
	if _, err := os.Stat(rel); err != nil {
		if err := p.fetch(ctx, tag, rel); err != nil {
			return "", err
		}
	}
	if err := Write(rel, out, p.ini); err != nil {
		return "", fmt.Errorf("the package from %s: %v", rel, err)
	}
	p.removeOld(tag)
	return out, nil
}

// fetch downloads the release of tag into rel and checks it as Start-Client's update checks a release (the size and
// digest GitHub names, BUILD.txt, both programs); a refused download is deleted.
func (p *Package) fetch(ctx context.Context, tag, rel string) error {
	r, err := p.cfg.Source.ByTag(ctx, tag)
	if err != nil {
		return fmt.Errorf("the release %s on GitHub: %w", tag, err)
	}
	p.cfg.Log("client package: downloading %s (%d bytes) from %s", r.Zip.Name, r.Zip.Size, r.Zip.URL)
	tmp := rel + ".unchecked"
	if err := p.cfg.Source.Download(ctx, r.Zip, tmp, nil); err != nil {
		return fmt.Errorf("the download of %s: %w", r.Zip.Name, err)
	}
	if err := Check(tmp, tag); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%s is refused: %w", r.Zip.Name, err)
	}
	return os.Rename(tmp, rel)
}

// Check unpacks a release zip into a temporary folder beside it and checks it against its BUILD.txt for tag
// (update.Check: the version line, every file's SHA-256, no other file, both programs).
func Check(zipFile, tag string) error {
	stage, err := os.MkdirTemp(filepath.Dir(zipFile), "check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	root, err := update.Unpack(zipFile, stage)
	if err != nil {
		return err
	}
	_, err = update.Check(root, tag)
	return err
}

// removeOld deletes the packages of other versions and this version's packages of other names.
func (p *Package) removeOld(tag string) {
	ents, err := os.ReadDir(p.cfg.Dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() && e.Name() != tag && update.IsRelease(e.Name()) {
			if os.RemoveAll(filepath.Join(p.cfg.Dir, e.Name())) == nil {
				p.cfg.Log("client package: the package of %s removed", e.Name())
			}
		}
	}
	own, _ := os.ReadDir(filepath.Join(p.cfg.Dir, tag))
	for _, e := range own {
		if n := e.Name(); !e.IsDir() && n != p.file && strings.HasPrefix(n, "OpenDawngate-") && strings.HasSuffix(n, ".zip") {
			os.Remove(filepath.Join(p.cfg.Dir, tag, n))
		}
	}
}

// clientFolder finds Start-Client.exe in a release zip (at its top or in its single top folder): the entry and the
// prefix its folder's entries have ("" or "<folder>/", with the zip's own separator).
func clientFolder(files []*zip.File) (*zip.File, string, error) {
	for _, f := range files {
		n := strings.ReplaceAll(f.Name, `\`, "/")
		if path.Base(n) == ClientExe && strings.Count(n, "/") <= 1 && !strings.HasPrefix(n, "/") {
			return f, f.Name[:len(f.Name)-len(ClientExe)], nil
		}
	}
	return nil, "", fmt.Errorf("the zip has no %s at its top or in its top folder", ClientExe)
}

// Write makes the package out from the release zip rel: every entry of rel copied raw (zip.Writer.Copy: the same
// compressed bytes, names, times and CRCs; an ini in the release would be left out, a release has none), then ini as
// Start-Client.ini in Start-Client.exe's folder. It writes out.part and renames it.
func Write(rel, out string, ini []byte) error {
	zr, err := zip.OpenReader(rel)
	if err != nil {
		return err
	}
	defer zr.Close()
	exe, prefix, err := clientFolder(zr.File)
	if err != nil {
		return err
	}
	iniName := prefix + IniName
	tmp := out + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	err = func() error {
		for _, e := range zr.File {
			if strings.EqualFold(strings.ReplaceAll(e.Name, `\`, "/"), strings.ReplaceAll(iniName, `\`, "/")) {
				continue
			}
			if err := zw.Copy(e); err != nil {
				return err
			}
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: iniName, Method: zip.Deflate, Modified: exe.Modified})
		if err != nil {
			return err
		}
		if _, err := w.Write(ini); err != nil {
			return err
		}
		return zw.Close()
	}()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(out)
	return os.Rename(tmp, out)
}

// readIni is the Start-Client.ini a package holds.
func readIni(pkg string) ([]byte, error) {
	zr, err := zip.OpenReader(pkg)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	_, prefix, err := clientFolder(zr.File)
	if err != nil {
		return nil, err
	}
	want := strings.ReplaceAll(prefix+IniName, `\`, "/")
	for _, e := range zr.File {
		if strings.ReplaceAll(e.Name, `\`, "/") == want {
			r, err := e.Open()
			if err != nil {
				return nil, err
			}
			defer r.Close()
			return io.ReadAll(io.LimitReader(r, 1<<20))
		}
	}
	return nil, fs.ErrNotExist
}

// MinimalIni is a Start-Client.ini with only the package's keys (CRLF lines): server, gateway_port when it isn't
// defaultPort, gateway_pin. dgserver uses it when no fuller one is given (Start-Client's template, dgserver.ClientIni).
func MinimalIni(server string, gatewayPort, defaultPort int, pin string) []byte {
	lines := []string{"; Start-Client.ini: the options of Start-Client.exe, made by the server " + server + " for its players.",
		"; Start-Client.exe preselects this server and trusts its tunnel key at the first login.", "", "server = " + server}
	if gatewayPort != 0 && gatewayPort != defaultPort {
		lines = append(lines, fmt.Sprintf("gateway_port = %d", gatewayPort))
	}
	lines = append(lines, "gateway_pin = "+pin, "")
	return []byte(strings.Join(lines, "\r\n"))
}

// Disposition is the Content-Disposition header of the package's download.
func (p *Package) Disposition() string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": p.file})
}

// ServeHTTP serves the package on the website (GET and HEAD, ranges too): built first if need be (the request waits),
// 404 for a developer build, 503 while it can't be made (GitHub unreachable: the reason is in the server's log).
func (p *Package) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Only GET here.", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), buildTime)
	defer cancel()
	file, err := p.Get(ctx)
	switch {
	case errors.Is(err, ErrDevBuild):
		http.Error(w, "No client package: "+err.Error()+".", http.StatusNotFound)
		return
	case err != nil:
		w.Header().Set("Retry-After", "300")
		http.Error(w, "The client package can't be made right now; please try again later.", http.StatusServiceUnavailable)
		return
	}
	f, err := os.Open(file)
	if err != nil {
		http.Error(w, "The client package can't be read right now; please try again later.", http.StatusServiceUnavailable)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "The client package can't be read right now; please try again later.", http.StatusServiceUnavailable)
		return
	}
	// a slow line needs longer than a page: the download's own deadline
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", p.Disposition())
	http.ServeContent(w, r, p.file, st.ModTime(), f)
}
