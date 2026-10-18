package update

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// BuildFile is the release's list of its files (make_release.ps1): its version line and every file's SHA-256.
const BuildFile = "BUILD.txt"

// The unpacked zip's bounds (a release holds about 130 files, 40 MB).
const (
	maxEntries  = 4096
	maxUnpacked = 1 << 30
)

// Build is a BUILD.txt: the release's version and its files (slash paths relative to the release folder) with their
// SHA-256 in lower-case hex. BUILD.txt itself is not in Files.
type Build struct {
	Version string
	Files   map[string]string
}

var hashLine = regexp.MustCompile(`^([0-9a-fA-F]{64})  (\S.*)$`)

// ReadBuild reads a BUILD.txt. Every path must be a plain relative one that a release may hold (Protected names none).
func ReadBuild(file string) (Build, error) {
	f, err := os.Open(file)
	if err != nil {
		return Build{}, err
	}
	defer f.Close()
	b := Build{Files: map[string]string{}}
	sc := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "version" && b.Version == "" {
			b.Version = fields[1]
			continue
		}
		m := hashLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rel, err := cleanRel(m[2])
		if err != nil {
			return Build{}, fmt.Errorf("%s: %v", file, err)
		}
		if Protected(rel) || strings.EqualFold(rel, BuildFile) {
			return Build{}, fmt.Errorf("%s lists %s, which a release never holds", file, rel)
		}
		b.Files[rel] = strings.ToLower(m[1])
	}
	if err := sc.Err(); err != nil {
		return Build{}, fmt.Errorf("%s: %v", file, err)
	}
	if b.Version == "" || len(b.Files) == 0 {
		return Build{}, fmt.Errorf("%s has no version line or no files", file)
	}
	return b, nil
}

// Paths is the build's files, sorted.
func (b Build) Paths() []string {
	ps := make([]string, 0, len(b.Files))
	for p := range b.Files {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return ps
}

// cleanRel turns a listed or zipped path (either slash) into a clean slash path inside the folder, or an error for
// an absolute one, one that leaves the folder, a drive or a stream name.
func cleanRel(p string) (string, error) {
	q := strings.ReplaceAll(p, `\`, "/")
	c := path.Clean(q)
	if q == "" || strings.HasPrefix(q, "/") || strings.Contains(q, ":") || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("%q is not a path inside the release", p)
	}
	for _, part := range strings.Split(c, "/") {
		if strings.TrimRight(part, ". ") != part { // Windows drops trailing dots and blanks: "a." would be "a"
			return "", fmt.Errorf("%q is not a path inside the release", p)
		}
	}
	return c, nil
}

// Protected reports whether a path in the player's folder belongs to the player, never to a release: the config
// files (Start-*.ini), Dawngate.zip, the setup folder Dawngate-Data, and the old programs (*.old.exe).
func Protected(rel string) bool {
	r := strings.ToLower(strings.ReplaceAll(rel, `\`, "/"))
	first, _, nested := strings.Cut(r, "/")
	if first == "dawngate-data" {
		return true
	}
	if nested {
		return false
	}
	if m, _ := path.Match("start-*.ini", r); m {
		return true
	}
	return r == "dawngate.zip" || strings.HasSuffix(r, ".old.exe")
}

// Unpack extracts a release zip into dir (made anew) and returns the release folder in it: dir itself, or the zip's
// single top folder (make_release.ps1 zips the folder <name>\). Entries that leave dir, too many and too large ones
// are refused.
func Unpack(zipFile, dir string) (string, error) {
	z, err := zip.OpenReader(zipFile)
	if err != nil {
		return "", fmt.Errorf("%s: %v", zipFile, err)
	}
	defer z.Close()
	if len(z.File) > maxEntries {
		return "", fmt.Errorf("%s: %d entries, more than a release has", zipFile, len(z.File))
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var total int64
	for _, e := range z.File {
		if strings.HasSuffix(strings.ReplaceAll(e.Name, `\`, "/"), "/") {
			continue // a folder
		}
		rel, err := cleanRel(e.Name)
		if err != nil {
			return "", fmt.Errorf("%s: %v", zipFile, err)
		}
		if !e.Mode().IsRegular() {
			return "", fmt.Errorf("%s: %s is not a plain file", zipFile, e.Name)
		}
		n, err := extract(e, filepath.Join(dir, filepath.FromSlash(rel)), maxUnpacked-total)
		if err != nil {
			return "", fmt.Errorf("%s: %s: %v", zipFile, e.Name, err)
		}
		total += n
	}
	if _, err := os.Stat(filepath.Join(dir, BuildFile)); err == nil {
		return dir, nil
	}
	ents, err := os.ReadDir(dir)
	if err == nil && len(ents) == 1 && ents[0].IsDir() {
		root := filepath.Join(dir, ents[0].Name())
		if _, err := os.Stat(filepath.Join(root, BuildFile)); err == nil {
			return root, nil
		}
	}
	return "", fmt.Errorf("%s holds no %s (is it a release of this project?)", zipFile, BuildFile)
}

func extract(e *zip.File, dest string, room int64) (int64, error) {
	if room <= 0 {
		return 0, errors.New("the release unpacks to more than 1 GB")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	r, err := e.Open()
	if err != nil {
		return 0, err
	}
	defer r.Close()
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err // also a second entry of the same name
	}
	n, err := io.Copy(f, io.LimitReader(r, room+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > room {
		err = errors.New("the release unpacks to more than 1 GB")
	}
	return n, err
}

// Check checks an unpacked release folder against its BUILD.txt: the version line is tag, every listed file is there
// with its SHA-256, no other file is there, and the release's programs are among them. It returns the build.
func Check(root, tag string) (Build, error) {
	b, err := ReadBuild(filepath.Join(root, BuildFile))
	if err != nil {
		return Build{}, err
	}
	if b.Version != tag {
		return Build{}, fmt.Errorf("%s names the version %s, not %s", BuildFile, b.Version, tag)
	}
	for _, need := range []string{"Start-Client.exe", "Start-Server.exe"} {
		if _, ok := b.Files[need]; !ok {
			return Build{}, fmt.Errorf("%s doesn't list %s (not a Windows release)", BuildFile, need)
		}
	}
	seen := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel == BuildFile {
			return nil
		}
		want, ok := b.Files[rel]
		if !ok {
			return fmt.Errorf("%s is in the zip but not in its %s", rel, BuildFile)
		}
		got, err := fileSHA256(p)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%s: SHA-256 %s, %s says %s (a damaged download)", rel, got, BuildFile, want)
		}
		seen++
		return nil
	})
	if err != nil {
		return Build{}, err
	}
	if seen != len(b.Files) {
		for _, p := range b.Paths() {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
				return Build{}, fmt.Errorf("%s lists %s, which isn't in the zip", BuildFile, p)
			}
		}
		return Build{}, fmt.Errorf("%d of the %d files of %s are in the zip", seen, len(b.Files), BuildFile)
	}
	return b, nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
