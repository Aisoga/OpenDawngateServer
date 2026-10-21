package archive

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// PhaseCheckZip is the phase CheckZipFiles reports.
const PhaseCheckZip = "checking the archive"

// CheckZipFiles checks an archive against the build without unpacking it: CheckZip, then every file of m must be an
// entry of the archive with the manifest's size and SHA-256 (extra entries are allowed, as VerifyTree allows extra
// files). It is the check a downloaded Dawngate.zip passes before it is used; Extract checks the unpacked files the
// same way again. The error names the first file that doesn't match (and how many more don't).
func CheckZipFiles(zipPath string, m *Manifest, progress Progress) error {
	if m == nil {
		m = Build854()
	}
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("not a zip archive (%v)", err)
	}
	defer z.Close()
	if err := CheckZip(z.File); err != nil {
		return err
	}
	entries := map[string]*zip.File{}
	for _, f := range z.File {
		entries[f.Name] = f
	}
	total := m.TotalSize()
	var bad []string
	var done int64
	buf := make([]byte, 1<<20)
	for _, want := range m.files {
		e, ok := entries[want.Path]
		switch {
		case !ok:
			bad = append(bad, want.Path+" is missing")
			continue
		case int64(e.UncompressedSize64) != want.Size:
			bad = append(bad, fmt.Sprintf("%s has %d bytes, want %d", want.Path, e.UncompressedSize64, want.Size))
			continue
		}
		r, err := e.Open() // checks the size and CRC-32 at the end
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s can't be read (%v)", want.Path, err))
			continue
		}
		h := sha256.New()
		start := done
		var n int64
		for {
			k, rerr := r.Read(buf)
			if k > 0 {
				h.Write(buf[:k])
				n += int64(k)
				if n%progressStep < int64(k) {
					report(progress, PhaseCheckZip, start+n, total)
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				err = rerr
				break
			}
		}
		r.Close()
		done += want.Size
		report(progress, PhaseCheckZip, done, total)
		switch {
		case err != nil:
			bad = append(bad, fmt.Sprintf("%s can't be read (%v)", want.Path, err))
		case hex.EncodeToString(h.Sum(nil)) != want.SHA256:
			bad = append(bad, want.Path+" differs (SHA-256)")
		}
	}
	if len(bad) > 0 {
		more := ""
		if len(bad) > 1 {
			more = fmt.Sprintf(" (+%d more)", len(bad)-1)
		}
		return fmt.Errorf("it doesn't match build 1.0.903.854: %s%s", bad[0], more)
	}
	return nil
}
