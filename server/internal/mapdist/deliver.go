package mapdist

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dawngate/server/internal/setup/mapc"
)

// Paths on the server's HTTP port (dgserver's web.MapPackagesPath and web.MapPackagePrefix; repeated here so that the
// client side needs no web import).
const (
	ManifestPath  = "dawngate/mappkgs.txt"
	PackagePrefix = "dawngate/mappkg/"
)

// MarkerFile marks a package that map delivery put into the maps folder: only such a package is replaced by another
// version. Nothing else reads it.
const MarkerFile = "delivered.txt"

// IncomingDir is the maps folder's sub-folder where a package is unpacked and checked before it is installed (not a
// package: the maps folder's readers look only at its direct sub-folders with a map.json).
const IncomingDir = ".incoming"

// Getter asks the server for a path on its HTTP port: the body, its Content-Length (-1: not given) and the status
// code. The caller closes the body.
type Getter func(path string) (body io.ReadCloser, size int64, status int, err error)

// Verifier runs every rule on a received package and builds its archive from the player copy's own files as the
// setup will, returning the archive's SHA-256 (BuildVerifier).
type Verifier func(m *mapc.Map, others []mapc.Other) ([32]byte, error)

// Want is a map the client needs from the server: its tag and the server's version (the host copy archive's SHA-256).
type Want struct {
	Tag     string
	Version [32]byte
}

// Status is how a map's delivery ended.
type Status int

const (
	Installed Status = iota // downloaded, checked and put into the maps folder: the next start compiles it
	Present                 // the maps folder holds that package already: the next start compiles it
	NotServed               // the server doesn't hand it out (or no map at all: a release before delivery, or off)
	Refused                 // the package or the folder refused it (its content, the rules, the version, the player's own package)
	Failed                  // the transfer failed (tried again at the next login)
)

// Result is one map's delivery.
type Result struct {
	Tag, Name, Display string
	Status             Status
	Package            [32]byte // the bundle's SHA-256 (zero when unknown)
	Err                error
}

// Delivery fetches a server's maps into a maps folder.
type Delivery struct {
	MapsDir string // the client's maps folder (written only inside it)
	Server  string // the server's name for delivered.txt and the lines
	Limits  Limits
	Get     Getter
	Verify  Verifier
	// Skip says whether a package (by its bundle SHA-256) was refused before in this run: it isn't fetched again.
	Skip func(sum [32]byte) bool
	// Logf gets the progress lines (a download's start, every Every while it runs; nil: none).
	Logf  func(format string, a ...any)
	Every time.Duration // 0: 10 s
	Now   func() time.Time
}

// Run delivers each wanted map, in order. A manifest that can't be read fails every map the same way.
func (d *Delivery) Run(wants []Want) []Result {
	if len(wants) == 0 {
		return nil
	}
	man, err := d.manifest()
	var out []Result
	for _, w := range wants {
		r := Result{Tag: w.Tag}
		switch {
		case errors.Is(err, errNoDelivery):
			r.Status, r.Err = NotServed, err
		case err != nil:
			r.Status, r.Err = Failed, err
		default:
			e, ok := man[strings.ToLower(w.Tag)]
			if !ok {
				r.Status, r.Err = NotServed, fmt.Errorf("the server doesn't hand out ~#%s", w.Tag)
				break
			}
			r = d.one(e, w)
		}
		out = append(out, r)
	}
	return out
}

var errNoDelivery = errors.New("the server doesn't hand out its maps (turned off there, or a release before map delivery)")

func (d *Delivery) manifest() (map[string]Entry, error) {
	body, _, status, err := d.Get(ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("the server's map list: %v", err)
	}
	defer body.Close()
	switch status {
	case 200:
	case 404:
		return nil, errNoDelivery
	default:
		return nil, fmt.Errorf("the server's map list: HTTP status %d", status)
	}
	b, err := io.ReadAll(io.LimitReader(body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("the server's map list: %v", err)
	}
	return ParseManifest(string(b))
}

// one delivers one map of the manifest.
func (d *Delivery) one(e Entry, w Want) (r Result) {
	r = Result{Tag: e.Tag, Name: e.Name, Package: e.SHA256}
	fail := func(st Status, format string, a ...any) Result {
		r.Status, r.Err = st, fmt.Errorf(format, a...)
		return r
	}
	if d.Skip != nil && d.Skip(e.SHA256) {
		return fail(Refused, "package %s was refused before (start Start-Client again to try it once more)", e.Short())
	}
	if e.Size > d.Limits.Total {
		return fail(Refused, "%s, more than this Start-Client takes (%s; map_download_max_mb in Start-Client.ini)",
			Bytes(e.Size), Bytes(d.Limits.Total))
	}
	pkgs, _ := mapc.ReadFolder(d.MapsDir)
	var replace []string // delivered packages of the same name or tag, replaced by this one
	others := []mapc.Other{}
	for _, p := range pkgs {
		sameName, sameTag := strings.EqualFold(p.Map.Name, e.Name), strings.EqualFold(p.Map.Tag, e.Tag)
		if !sameName && !sameTag {
			others = append(others, mapc.Other{Name: p.Map.Name, Tag: p.Map.Tag})
			continue
		}
		if sameName && sameTag {
			if lp, err := ReadPackage(p.Dir, d.Limits); err == nil {
				if sum, _, err := lp.Hash(); err == nil && sum == e.SHA256 {
					r.Display, r.Status = p.Label.Display, Present
					return r
				}
			}
		}
		if !Delivered(p.Dir) {
			what := "your own " + p.Label.Display
			if !sameName {
				what = fmt.Sprintf("your own map %s with the same tag ~#%s", p.Map.Name, e.Tag)
			}
			return fail(Refused, "the maps folder has %s (folder %s), another version than this server's: move that "+
				"folder out of the maps folder to get this server's (Start-Client replaces only maps it downloaded)", what, p.Dir)
		}
		replace = append(replace, p.Dir)
	}
	target := filepath.Join(d.MapsDir, e.Name)
	if _, err := os.Stat(target); err == nil && !containsFold(replace, target) {
		return fail(Refused, "the folder %s is in the way (another map or no map package): move it out of the maps folder to get this map", target)
	}
	in := filepath.Join(d.MapsDir, IncomingDir)
	if err := os.MkdirAll(in, 0o755); err != nil {
		return fail(Failed, "%v", err)
	}
	defer os.Remove(in) // only when empty
	tmp := filepath.Join(in, e.Name+"-"+randomHex())
	defer os.RemoveAll(tmp)
	got, err := d.download(e, tmp)
	if err != nil {
		st := Failed
		if errors.Is(err, ErrBundle) {
			st = Refused
		}
		r.Status, r.Err = st, err
		return r
	}
	m, err := mapc.Load(tmp)
	if err != nil {
		return fail(Refused, "%w: %v", ErrBundle, err)
	}
	r.Display = mapc.LabelOf(m).Display
	if m.Name != e.Name || m.Tag != e.Tag {
		return fail(Refused, "%w: the package is %s (~#%s), the server's list says %s (~#%s)", ErrBundle, m.Name, m.Tag,
			e.Name, e.Tag)
	}
	if want := append([]string{mapc.FileName}, mapc.PackageFiles(m)...); !sameSet(want, got.Files) {
		return fail(Refused, "%w: its files (%s) aren't the ones its map.json names (%s)", ErrBundle,
			strings.Join(got.Files, ", "), strings.Join(want, ", "))
	}
	sum, err := d.Verify(m, others)
	if err != nil {
		return fail(Refused, "%w: it doesn't compile: %v", ErrBundle, err)
	}
	if sum != w.Version {
		return fail(Refused, "%w: it compiles to version %s, not the server's %s (a server of another build?)", ErrBundle,
			hex.EncodeToString(sum[:4]), hex.EncodeToString(w.Version[:4]))
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	marker := fmt.Sprintf("This map package was downloaded by Start-Client from the server %s on %s.\n"+
		"Start-Client replaces it when a server you log in to offers another version of it; delete this file to keep "+
		"it as your own.\nmap %s\ntag %s\npackage %s\nversion %s\n", d.Server, now().UTC().Format("2006-01-02 15:04 MST"),
		e.Name, e.Tag, hex.EncodeToString(e.SHA256[:]), hex.EncodeToString(w.Version[:]))
	if err := os.WriteFile(filepath.Join(tmp, MarkerFile), []byte(marker), 0o644); err != nil {
		return fail(Failed, "%v", err)
	}
	// the old delivered versions aside, the new one in, the old ones removed (back if the move fails)
	var moved [][2]string
	for _, dir := range replace {
		old := filepath.Join(in, filepath.Base(dir)+"-old-"+randomHex())
		if err := os.Rename(dir, old); err != nil {
			undo(moved)
			return fail(Failed, "the old version in %s can't be moved: %v (is the editor using it?)", dir, err)
		}
		moved = append(moved, [2]string{old, dir})
	}
	if err := os.Rename(tmp, target); err != nil {
		undo(moved)
		return fail(Failed, "%v", err)
	}
	for _, mv := range moved {
		os.RemoveAll(mv[0])
	}
	r.Status = Installed
	return r
}

// download fetches e's bundle into dir (new), checking its size and SHA-256 against the manifest's.
func (d *Delivery) download(e Entry, dir string) (*Unpacked, error) {
	body, size, status, err := d.Get(PackagePrefix + e.Tag)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	switch {
	case status == 503:
		return nil, errors.New("the server is busy handing out maps (it is tried again at your next login)")
	case status == 404:
		return nil, fmt.Errorf("%w: the server no longer hands out ~#%s", ErrBundle, e.Tag)
	case status != 200:
		return nil, fmt.Errorf("HTTP status %d", status)
	case size >= 0 && size != e.Size:
		return nil, fmt.Errorf("%w: %d bytes sent, the server's list says %d", ErrBundle, size, e.Size)
	}
	if d.Logf != nil {
		d.Logf("downloading %s (%s) …", e.Name, Bytes(e.Size))
	}
	pr := &progressReader{r: io.LimitReader(body, e.Size+1), d: d, e: e, last: time.Now()}
	got, err := Unpack(pr, dir, d.Limits)
	if err != nil {
		return nil, err
	}
	if got.Size != e.Size || got.SHA256 != e.SHA256 {
		return nil, fmt.Errorf("%w: it isn't the package the server announced (%d bytes, SHA-256 %s; announced %d bytes, %s)",
			ErrBundle, got.Size, hex.EncodeToString(got.SHA256[:4]), e.Size, e.Short())
	}
	return got, nil
}

type progressReader struct {
	r    io.Reader
	d    *Delivery
	e    Entry
	n    int64
	last time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	k, err := p.r.Read(b)
	p.n += int64(k)
	every := p.d.Every
	if every == 0 {
		every = 10 * time.Second
	}
	if p.d.Logf != nil && time.Since(p.last) >= every && p.e.Size > 0 {
		p.last = time.Now()
		p.d.Logf("downloading %s: %s of %s (%d %%)", p.e.Name, Bytes(p.n), Bytes(p.e.Size), p.n*100/p.e.Size)
	}
	return k, err
}

// Delivered says whether the package in dir came by map delivery (its MarkerFile).
func Delivered(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, MarkerFile))
	return err == nil && st.Mode().IsRegular()
}

// DeliveredFrom says whether the package in dir came by map delivery and, when its MarkerFile's first line names
// it, the server it came from ("" when the line isn't the one this package writes).
func DeliveredFrom(dir string) (server string, ok bool) {
	if !Delivered(dir) {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(dir, MarkerFile))
	if err != nil {
		return "", true
	}
	line, _, _ := strings.Cut(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	const pre = "This map package was downloaded by Start-Client from the server "
	if rest, found := strings.CutPrefix(line, pre); found {
		if i := strings.LastIndex(rest, " on "); i > 0 {
			return rest[:i], true
		}
	}
	return "", true
}

// BuildVerifier checks a received package against the player copy (the folder with game\): every rule (mapc.Check,
// inside mapc.Build), the copy's catalog (mapc.CheckShipped: the nouns it names exist), then the archive built in
// memory from the copy's own files exactly as the setup builds it (mapc.Build with the shared mesh); its SHA-256.
func BuildVerifier(copy string) Verifier {
	return func(m *mapc.Map, others []mapc.Other) ([32]byte, error) {
		in, err := mapc.ReadInputs(copy)
		if err != nil {
			return [32]byte{}, fmt.Errorf("the game copy's files: %v", err)
		}
		shipped, err := mapc.ReadShipped(copy)
		if err != nil {
			return [32]byte{}, fmt.Errorf("the game copy's catalog: %v", err)
		}
		r := &mapc.Report{Map: m.Name}
		mapc.CheckShipped(m, shipped, r)
		if f := firstError(r); f != "" {
			return [32]byte{}, errors.New(f)
		}
		b, r, err := mapc.Build(m, in, mapc.SharedMesh{}, others)
		if err != nil {
			if f := firstError(r); f != "" {
				return [32]byte{}, fmt.Errorf("%v, first: %s", err, f)
			}
			return [32]byte{}, err
		}
		return sha256.Sum256(b), nil
	}
}

func firstError(r *mapc.Report) string {
	if r == nil {
		return ""
	}
	for _, f := range r.Findings {
		if f.Severity == mapc.Error {
			return fmt.Sprintf("%s (%s)", f.Message, f.Rule)
		}
	}
	return ""
}

func undo(moved [][2]string) {
	for _, mv := range moved {
		os.Rename(mv[0], mv[1])
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(filepath.Clean(v), filepath.Clean(s)) {
			return true
		}
	}
	return false
}

func randomHex() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Leftovers removes what an interrupted delivery left in the maps folder's IncomingDir (a Start-Client closed during a
// download); nothing else.
func Leftovers(mapsDir string) error {
	in := filepath.Join(mapsDir, IncomingDir)
	if _, err := os.Stat(in); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return os.RemoveAll(in)
}
