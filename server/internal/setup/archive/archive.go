// Package archive checks and unpacks Dawngate.zip (archive.org, build 1.0.903.854) for the player setup. It is
// the Go port of re/scripts/setup_all.py's archive step (check_game_zip, the extraction
// of extract_client.py, verify_tree, load_manifest), which stays the reference (ADR-3):
//
//   - CheckZip refuses an archive that isn't one Dawngate/ install folder (entries outside it, absolute or "..",
//     drive letters or backslashes, more than MaxEntries entries or MaxBytes uncompressed).
//   - Extract unpacks it with the archive's file times into <dest>.partial, checks every file against the manifest
//     (size and SHA-256), then renames the folder to <dest>. A failed check leaves <dest>.partial for a look.
//   - VerifyTree checks an existing extraction: the sizes of all files and the SHA-256 of KeyFiles (all with full).
//
// The manifest of build 854 is compiled in (Build854, generated from docs/file_manifest.csv by gen_manifest.go).
// Nothing here writes into an existing extraction; the zip is only read. The slow steps report progress.
package archive

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The archive.org download (https://archive.org/details/Dawngate) as fetched on 2026-10-07 (setup_all.py's GAME_ZIP_*).
const (
	GameZipName   = "Dawngate.zip"
	GameZipSize   = 1310588859
	GameZipSHA256 = "56ae93939904605ac3145cb55ace854482f40bf128991a0a21ba56f14d4bdae1"
)

// Top is the one folder Dawngate.zip holds.
const Top = "Dawngate"

// Limits of CheckZip (setup_all.py's check_game_zip defaults). Dawngate.zip has 185 entries and 1.53 GB.
const (
	MaxEntries = 2000
	MaxBytes   = 4 << 30
)

// FreeMargin is the free space Extract wants beyond the archive's uncompressed size (setup_all.py's MARGIN).
const FreeMargin = 1 << 30

// KeyFiles are the files (copy paths) whose SHA-256 every VerifyTree checks: the ones the patchers and the host
// copy start from (setup_all.py's KEY_FILES).
var KeyFiles = []string{
	"game/Dawngate.exe",
	"game/Data/Config.package",
	"game/Data/Config_Ship.package",
	"game/Data/Config_Pack.package",
	"game/PhysXLoader.dll",
}

// The phases Progress reports.
const (
	PhaseHashZip = "hashing the archive"
	PhaseExtract = "extracting"
	PhaseVerify  = "verifying"
)

// Progress is told how far a slow step is: done of total bytes in a phase. It may be nil.
type Progress func(phase string, done, total int64)

// progressStep is how many bytes pass between two Progress calls (besides each file's end).
const progressStep = 8 << 20

// CheckZip refuses an archive that isn't one Dawngate/ install folder (setup_all.py's check_game_zip).
func CheckZip(files []*zip.File) error {
	return checkZip(files, Top, MaxBytes, MaxEntries)
}

func checkZip(files []*zip.File, top string, maxBytes uint64, maxEntries int) error {
	var total uint64
	for _, f := range files {
		name := f.Name
		parts := strings.Split(name, "/")
		bad := parts[0] != top || strings.HasPrefix(name, "/") || strings.ContainsAny(name, ":\\")
		for _, p := range parts {
			if p == ".." {
				bad = true
			}
		}
		if bad {
			return fmt.Errorf("unexpected entry %q (everything must be under %s/)", name, top)
		}
		total += f.UncompressedSize64
	}
	if len(files) == 0 || len(files) > maxEntries {
		return fmt.Errorf("%d entries (Dawngate.zip has 185)", len(files))
	}
	if total > maxBytes {
		return fmt.Errorf("%d bytes uncompressed", total)
	}
	return nil
}

// Result describes a finished Extract.
type Result struct {
	Dest          string        // the extraction (holds Dawngate/)
	Files         int           // files written
	Bytes         int64         // their bytes
	ZipSHA256     string        // the archive's SHA-256
	KnownDownload bool          // the archive is the known download (GameZipSize, GameZipSHA256); its files are checked either way
	Took          time.Duration // the whole step
}

// VerifyError is a check that found files that don't match the manifest.
type VerifyError struct {
	Dir      string   // where the files are (an Extract's <dest>.partial, left for a look)
	Problems []string // one line per file, as setup_all.py's verify_tree words them
}

func (e *VerifyError) Error() string {
	more := ""
	if len(e.Problems) > 1 {
		more = fmt.Sprintf(" (+%d more)", len(e.Problems)-1)
	}
	return fmt.Sprintf("%s doesn't match build 1.0.903.854: %s%s", e.Dir, e.Problems[0], more)
}

// Extract unpacks the archive zipPath into dest (which then holds Dawngate/) as setup_all.py's archive step does:
// the archive's SHA-256 (a note: KnownDownload), CheckZip, the free space (the uncompressed size plus FreeMargin, where
// the system tells it), every entry into dest+".partial" with the archive's file times, then VerifyTree with every
// file's SHA-256 against m, then the rename to dest. dest must not exist; a <dest>.partial left by an interrupted run
// is removed first. On a failed check the files stay in <dest>.partial and the error is a *VerifyError.
func Extract(zipPath, dest string, m *Manifest, progress Progress) (Result, error) {
	t0 := time.Now()
	res := Result{Dest: dest}
	if _, err := os.Lstat(dest); err == nil {
		return res, fmt.Errorf("%s exists already (it is never written again; check it with VerifyTree)", dest)
	}
	st, err := os.Stat(zipPath)
	if err != nil {
		return res, err
	}
	digest, err := HashFile(zipPath, func(done, total int64) { report(progress, PhaseHashZip, done, total) })
	if err != nil {
		return res, err
	}
	res.ZipSHA256 = digest
	res.KnownDownload = st.Size() == GameZipSize && digest == GameZipSHA256

	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return res, fmt.Errorf("%s is not Dawngate's archive: %w", zipPath, err)
	}
	defer z.Close()
	if err := CheckZip(z.File); err != nil {
		return res, fmt.Errorf("%s is not Dawngate's archive: %w", zipPath, err)
	}
	var total int64
	for _, f := range z.File {
		total += int64(f.UncompressedSize64)
	}
	if free, err := FreeBytes(dest); err == nil && free < uint64(total)+FreeMargin {
		return res, fmt.Errorf("not enough disk space for %s: %.1f GB free, %.1f GB needed", dest,
			float64(free)/(1<<30), float64(uint64(total)+FreeMargin)/(1<<30))
	}

	tmp := dest + ".partial"
	if err := os.RemoveAll(tmp); err != nil { // left by an interrupted run (only that folder: it is named after dest)
		return res, err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return res, err
	}
	var done int64
	buf := make([]byte, 1<<20)
	for _, f := range z.File {
		n, err := extractOne(f, tmp, buf, func(n int64) { report(progress, PhaseExtract, done+n, total) })
		if err != nil {
			return res, fmt.Errorf("extracting %s: %w", f.Name, err)
		}
		done += n
		if !f.FileInfo().IsDir() {
			res.Files++
			res.Bytes += n
		}
	}
	report(progress, PhaseExtract, total, total)
	problems, err := VerifyTree(tmp, m, true, progress)
	if err != nil {
		return res, err
	}
	if len(problems) > 0 {
		return res, &VerifyError{Dir: tmp, Problems: problems}
	}
	if err := os.Rename(tmp, dest); err != nil {
		return res, err
	}
	res.Took = time.Since(t0)
	return res, nil
}

// extractOne writes one entry below dir (a folder entry makes the folder) and returns its bytes. Files get the
// archive's time as Python's zipfile and extract_client.py set it: the MS-DOS date and time read as local time
// (time.mktime), for both the access and the modification time.
func extractOne(f *zip.File, dir string, buf []byte, step func(int64)) (int64, error) {
	rel := strings.TrimSuffix(f.Name, "/")
	out := filepath.Join(dir, filepath.FromSlash(rel))
	if r, err := filepath.Rel(dir, out); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return 0, fmt.Errorf("entry %q leaves the target folder", f.Name)
	}
	if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
		return 0, os.MkdirAll(out, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return 0, err
	}
	src, err := f.Open() // checks the size and CRC-32 at the end
	if err != nil {
		return 0, err
	}
	defer src.Close()
	dst, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	var n, last int64
	for {
		k, rerr := src.Read(buf)
		if k > 0 {
			if _, werr := dst.Write(buf[:k]); werr != nil {
				dst.Close()
				return n, werr
			}
			n += int64(k)
			if n-last >= progressStep {
				step(n)
				last = n
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			dst.Close()
			return n, rerr
		}
	}
	if err := dst.Close(); err != nil {
		return n, err
	}
	step(n)
	t := dosTime(f)
	return n, os.Chtimes(out, t, t)
}

// dosTime is an entry's MS-DOS date and time as local time (Python's time.mktime(ZipInfo.date_time)). Go's
// FileHeader.Modified prefers the NTFS extra field Dawngate.zip carries, so the legacy fields are read directly.
func dosTime(f *zip.File) time.Time {
	d, t := f.ModifiedDate, f.ModifiedTime //nolint:staticcheck // the legacy fields are what extract_client.py uses
	return time.Date(int(d>>9)+1980, time.Month(d>>5&0xF), int(d&0x1F), int(t>>11), int(t>>5&0x3F), int(t&0x1F)*2, 0,
		time.Local)
}

// VerifyTree lists the problems of an extraction under base (which holds Dawngate/): missing files, wrong sizes, and
// a wrong SHA-256 for KeyFiles (every file with full). Extra files are allowed (setup_all.py's verify_tree). The
// error is for a failure to read, not for a mismatch.
func VerifyTree(base string, m *Manifest, full bool, progress Progress) ([]string, error) {
	key := map[string]bool{}
	for _, k := range KeyFiles {
		key[k] = true
	}
	var total int64
	for _, f := range m.files {
		if full || key[f.CopyPath()] {
			total += f.Size
		}
	}
	var bad []string
	var done int64
	for _, f := range m.files {
		p := filepath.Join(base, filepath.FromSlash(f.Path))
		st, err := os.Stat(p)
		switch {
		case err != nil && errors.Is(err, os.ErrNotExist), err == nil && !st.Mode().IsRegular():
			bad = append(bad, f.Path+" is missing")
			continue
		case err != nil:
			return bad, err
		case st.Size() != f.Size:
			bad = append(bad, fmt.Sprintf("%s has %d bytes, want %d", f.Path, st.Size(), f.Size))
			continue
		}
		if !full && !key[f.CopyPath()] {
			continue
		}
		start := done
		digest, err := HashFile(p, func(n, _ int64) { report(progress, PhaseVerify, start+n, total) })
		if err != nil {
			return bad, err
		}
		done += f.Size
		if digest != f.SHA256 {
			bad = append(bad, f.Path+" differs (SHA-256)")
		}
	}
	return bad, nil
}

// PatchedFiles are the copy paths a working copy's patchers change (setup_all.py's PATCHED); a match host's copy also
// changes game/PhysXLoader.dll (hostcopy).
var PatchedFiles = []string{
	"game/Dawngate.exe",
	"game/Data/Config.package",
	"game/Data/Config_Ship.package",
	"game/Data/Config_Pack.package",
}

// CheckCopy lists the files of the build missing from a working copy (its Dawngate folder) or of another size,
// except the copy paths in exempt (setup_all.py's ensure_copy check; sizes only, since a copy's patched files
// differ). Pass PatchedFiles for a client copy, PatchedFiles plus "game/PhysXLoader.dll" for a host copy.
func CheckCopy(dawngateDir string, m *Manifest, exempt []string) ([]string, error) {
	skip := map[string]bool{}
	for _, e := range exempt {
		skip[e] = true
	}
	var bad []string
	for _, f := range m.files {
		rel := f.CopyPath()
		if skip[rel] {
			continue
		}
		st, err := os.Stat(filepath.Join(dawngateDir, filepath.FromSlash(rel)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return bad, err
		}
		if err != nil || !st.Mode().IsRegular() || st.Size() != f.Size {
			bad = append(bad, rel)
		}
	}
	return bad, nil
}

// HashFile is the SHA-256 (hex) of a file; progress (may be nil) gets the bytes read so far and the file's size.
func HashFile(path string, progress func(done, total int64)) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var n, last int64
	for {
		k, rerr := f.Read(buf)
		if k > 0 {
			h.Write(buf[:k])
			n += int64(k)
			if progress != nil && n-last >= progressStep {
				progress(n, st.Size())
				last = n
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	if progress != nil {
		progress(n, st.Size())
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func report(p Progress, phase string, done, total int64) {
	if p != nil {
		p(phase, done, total)
	}
}

// FreeBytes is the free space for the current user on the drive that holds path (its nearest existing parent).
// errors.ErrUnsupported where the system doesn't tell.
func FreeBytes(path string) (uint64, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	for {
		if _, err := os.Stat(p); err == nil {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return freeBytes(p)
}
