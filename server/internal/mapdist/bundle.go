// Package mapdist is map delivery: a server hands the map packages it offers to the players who need them, and a
// player's Start-Client checks a received package, puts it into its maps folder and leaves the compile to its setup.
//
// A package travels as a bundle (format "DGMP" 1): "DGMP", a version byte, a file count (u16, big-endian), then per
// file a name length (u16), the name (a relative path with "/") and a size (u32) followed by the bytes; map.json first,
// then the PNGs its map names (mapc.PackageFiles) in name order. The bundle is deterministic, so its SHA-256 says
// whether two packages are the same. Nothing else travels: no game bytes (a package names shipped assets by name and
// the client compiles them from its own copy) and nothing that runs.
//
// The receiver treats every byte as untrusted: Unpack bounds the file count, each file, the whole, the pictures' sides
// and the names (no path leaves the folder, no Windows device name, no two names alike) before it writes anything.
package mapdist

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"dawngate/server/internal/setup/mapc"
)

// Magic and Version open a bundle.
const (
	Magic   = "DGMP"
	Version = 1
)

// Limits bound a bundle. The receiver's are what protects it; the sender checks the same before serving.
type Limits struct {
	Files int   // files in a package, map.json included
	JSON  int64 // map.json's bytes
	File  int64 // one PNG's bytes
	Total int64 // the whole bundle's bytes
	Side  int   // a PNG's largest width or height
}

// DefaultLimits are Start-Client's and dgserver's defaults: 64 files, map.json up to 4 MB, a PNG up to
// mapc.MaxPictureTileBytes and mapc.MaxPicture pixels a side, 512 MB a package (a 2 x 2 x 4096 painted ground is
// about 70 MB).
func DefaultLimits() Limits {
	return Limits{Files: 64, JSON: 4 << 20, File: mapc.MaxPictureTileBytes, Total: 512 << 20, Side: mapc.MaxPicture}
}

// MaxNameLen and MaxNameParts bound a file's name in a bundle.
const (
	MaxNameLen   = 160
	MaxNameParts = 4
)

var namePart = regexp.MustCompile(`^[A-Za-z0-9_\-][A-Za-z0-9_.\-]*$`)

// windowsDevices are the names Windows maps to devices whatever their extension.
var windowsDevices = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true, "conin$": true, "conout$": true,
	"clock$": true}

// CheckName says whether name may be a file of a bundle: mapc.FileName, or a clean relative .png path inside the
// package (mapc.PackagePath) of at most MaxNameParts parts and MaxNameLen characters, each part of A-Z a-z 0-9 _ - .
// not starting with a dot or ending with one, and no Windows device name.
func CheckName(name string) error {
	if name == mapc.FileName {
		return nil
	}
	if len(name) > MaxNameLen {
		return fmt.Errorf("file name %.40q…: longer than %d characters", name, MaxNameLen)
	}
	if err := mapc.PackagePath(name, ".png"); err != nil {
		return fmt.Errorf("file name %q: %v", name, err)
	}
	parts := strings.Split(name, "/")
	if len(parts) > MaxNameParts {
		return fmt.Errorf("file name %q: more than %d folder levels", name, MaxNameParts)
	}
	for _, p := range parts {
		if !namePart.MatchString(p) || strings.HasSuffix(p, ".") {
			return fmt.Errorf("file name %q: each part only A-Z a-z 0-9 _ - . and not starting or ending with a dot", name)
		}
		base := strings.ToLower(p)
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		if windowsDevices[base] || (len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) &&
			base[3] >= '0' && base[3] <= '9') {
			return fmt.Errorf("file name %q: a Windows device name", name)
		}
	}
	return nil
}

// Package is a map package on disk, ready to be bundled: its folder, its map and its files (map.json first) with their
// sizes when it was read.
type Package struct {
	Dir   string
	Map   *mapc.Map
	Files []string
	Sizes []int64
}

// ReadPackage reads the package in dir for a bundle: map.json and the PNGs its map names, each there, each name a
// bundle's (CheckName) and every size within lim.
func ReadPackage(dir string, lim Limits) (*Package, error) {
	m, err := mapc.Load(dir)
	if err != nil {
		return nil, err
	}
	p := &Package{Dir: dir, Map: m, Files: append([]string{mapc.FileName}, mapc.PackageFiles(m)...)}
	if len(p.Files) > lim.Files {
		return nil, fmt.Errorf("%d files, at most %d", len(p.Files), lim.Files)
	}
	for _, f := range p.Files {
		if err := CheckName(f); err != nil {
			return nil, err
		}
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return nil, fmt.Errorf("%s: %v", f, err)
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: not a file", f)
		}
		max := lim.File
		if f == mapc.FileName {
			max = lim.JSON
		}
		if st.Size() > max {
			return nil, fmt.Errorf("%s: %d bytes, at most %d", f, st.Size(), max)
		}
		p.Sizes = append(p.Sizes, st.Size())
	}
	if n := p.Size(); n > lim.Total {
		return nil, fmt.Errorf("%d bytes as a bundle, at most %d", n, lim.Total)
	}
	return p, nil
}

// Size is the package's bundle size.
func (p *Package) Size() int64 {
	n := int64(len(Magic) + 1 + 2)
	for i, f := range p.Files {
		n += 2 + int64(len(f)) + 4 + p.Sizes[i]
	}
	return n
}

// WriteTo writes the package's bundle to w, the files read again; a file whose size changed since ReadPackage is an
// error (the bundle would not be the one announced).
func (p *Package) WriteTo(w io.Writer) (int64, error) {
	bw := bufio.NewWriterSize(w, 256<<10)
	var n int64
	put := func(b []byte) error {
		k, err := bw.Write(b)
		n += int64(k)
		return err
	}
	head := []byte(Magic)
	head = append(head, Version)
	head = binary.BigEndian.AppendUint16(head, uint16(len(p.Files)))
	if err := put(head); err != nil {
		return n, err
	}
	for i, f := range p.Files {
		h := binary.BigEndian.AppendUint16(nil, uint16(len(f)))
		h = append(h, f...)
		h = binary.BigEndian.AppendUint32(h, uint32(p.Sizes[i]))
		if err := put(h); err != nil {
			return n, err
		}
		fh, err := os.Open(filepath.Join(p.Dir, filepath.FromSlash(f)))
		if err != nil {
			return n, err
		}
		k, err := io.Copy(bw, io.LimitReader(fh, p.Sizes[i]+1))
		fh.Close()
		n += k
		if err != nil {
			return n, err
		}
		if k != p.Sizes[i] {
			return n, fmt.Errorf("%s changed while it was sent (%d bytes, was %d)", f, k, p.Sizes[i])
		}
	}
	return n, bw.Flush()
}

// Hash is the package's bundle SHA-256 and size.
func (p *Package) Hash() ([32]byte, int64, error) {
	h := sha256.New()
	n, err := p.WriteTo(h)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, n, err
}

// ErrBundle marks a bundle refused for its content (a bound, a name, the format), as against a failed read.
var ErrBundle = errors.New("the map package is refused")

func refused(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBundle, fmt.Sprintf(format, a...))
}

// pngSignature and the IHDR's place: the first chunk of a PNG must be IHDR, width and height at bytes 16 and 20.
var pngSignature = []byte("\x89PNG\r\n\x1a\n")

const pngHead = 24

// Unpacked is what Unpack read: the files' names (map.json first, as sent), the bundle's SHA-256 and its size.
type Unpacked struct {
	Files  []string
	SHA256 [32]byte
	Size   int64
}

// Unpack reads a bundle from r into dir, a new folder (it must not exist), checking every bound of lim before the
// bytes it governs are written: the head, the file count, each name (CheckName, map.json exactly once and first, no
// two names equal ignoring case), each size (map.json lim.JSON, a PNG lim.File, the whole lim.Total), a PNG's
// signature, first chunk and sides (lim.Side) from its first bytes, and nothing after the last file. On an error dir
// may hold part of the package: the caller removes it.
func Unpack(r io.Reader, dir string, lim Limits) (*Unpacked, error) {
	h := sha256.New()
	cr := &countReader{r: io.TeeReader(r, h)}
	br := bufio.NewReaderSize(cr, 64<<10)
	head := make([]byte, len(Magic)+3)
	if _, err := io.ReadFull(br, head); err != nil {
		return nil, shortRead("the head", err)
	}
	if string(head[:4]) != Magic {
		return nil, refused("not a map package (no %s head)", Magic)
	}
	if head[4] != Version {
		return nil, refused("a map package of format %d; this Start-Client reads format %d", head[4], Version)
	}
	count := int(binary.BigEndian.Uint16(head[5:]))
	if count < 1 || count > lim.Files {
		return nil, refused("%d files, at most %d", count, lim.Files)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, err
	}
	out := &Unpacked{}
	seen := map[string]bool{}
	var total int64 = int64(len(head))
	for i := 0; i < count; i++ {
		var nl [2]byte
		if _, err := io.ReadFull(br, nl[:]); err != nil {
			return nil, shortRead("a file's name", err)
		}
		n := int(binary.BigEndian.Uint16(nl[:]))
		if n == 0 || n > MaxNameLen {
			return nil, refused("a file name of %d characters, at most %d", n, MaxNameLen)
		}
		nb := make([]byte, n+4)
		if _, err := io.ReadFull(br, nb); err != nil {
			return nil, shortRead("a file's name", err)
		}
		name := string(nb[:n])
		size := int64(binary.BigEndian.Uint32(nb[n:]))
		if err := CheckName(name); err != nil {
			return nil, refused("%v", err)
		}
		if (i == 0) != (name == mapc.FileName) {
			return nil, refused("%s must come first, once (file %d is %q)", mapc.FileName, i+1, name)
		}
		if low := strings.ToLower(name); seen[low] {
			return nil, refused("%q twice", name)
		} else {
			seen[low] = true
		}
		max := lim.File
		if name == mapc.FileName {
			max = lim.JSON
		}
		if size > max {
			return nil, refused("%s: %d bytes, at most %d", name, size, max)
		}
		total += 2 + int64(n) + 4 + size
		if total > lim.Total {
			return nil, refused("more than %d bytes", lim.Total)
		}
		if err := unpackFile(br, dir, name, size, lim); err != nil {
			return nil, err
		}
		out.Files = append(out.Files, name)
	}
	if _, err := br.ReadByte(); err != io.EOF {
		if err == nil {
			return nil, refused("bytes after the last file")
		}
		return nil, err
	}
	copy(out.SHA256[:], h.Sum(nil))
	out.Size = cr.n
	return out, nil
}

// unpackFile writes one file of size bytes from br to dir/name, a new file; a PNG's head is checked first.
func unpackFile(br *bufio.Reader, dir, name string, size int64, lim Limits) error {
	var first []byte
	if path.Ext(name) != ".json" {
		if size < pngHead {
			return refused("%s: %d bytes, not a PNG", name, size)
		}
		first = make([]byte, pngHead)
		if _, err := io.ReadFull(br, first); err != nil {
			return shortRead(name, err)
		}
		if !bytes.Equal(first[:8], pngSignature) || string(first[12:16]) != "IHDR" {
			return refused("%s: not a PNG", name)
		}
		w, hgt := binary.BigEndian.Uint32(first[16:20]), binary.BigEndian.Uint32(first[20:24])
		if w == 0 || hgt == 0 || w > uint32(lim.Side) || hgt > uint32(lim.Side) {
			return refused("%s: %d x %d pixels, each side 1 to %d", name, w, hgt, lim.Side)
		}
	}
	full := filepath.Join(dir, filepath.FromSlash(name))
	if rel, err := filepath.Rel(dir, full); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return refused("%q leaves the package's folder", name) // CheckName already refuses it
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err = f.Write(first); err == nil {
		var k int64
		k, err = io.CopyN(f, br, size-int64(len(first)))
		if err != nil && k < size-int64(len(first)) {
			err = shortRead(name, err)
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func shortRead(what string, err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("the package ends early (in %s)", what)
	}
	return err
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(b []byte) (int, error) {
	k, err := c.r.Read(b)
	c.n += int64(k)
	return k, err
}
