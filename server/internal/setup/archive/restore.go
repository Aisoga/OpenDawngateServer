package archive

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RestoreFiles extracts the named files of the build (copy paths, "game/x") from the archive zipPath into the working
// copy dawngateDir (a Dawngate folder), each through <file>.partial with the archive's time, checked against m's size
// and SHA-256 before it replaces the file. It is setup_all.py's restore_file for a copy that lost or truncated files;
// the script copies them from client\ia_Dawngate, the Go setup keeps no pristine extraction and takes them from the
// archive. A path that isn't in m, or whose entry the archive lacks, is an error;
// nothing is written for it.
func RestoreFiles(zipPath, dawngateDir string, m *Manifest, paths []string) error {
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("%s is not Dawngate's archive: %w", zipPath, err)
	}
	defer z.Close()
	if err := CheckZip(z.File); err != nil {
		return fmt.Errorf("%s is not Dawngate's archive: %w", zipPath, err)
	}
	byName := map[string]*zip.File{}
	for _, f := range z.File {
		byName[f.Name] = f
	}
	buf := make([]byte, 1<<20)
	for _, rel := range paths {
		want, ok := m.Lookup(rel)
		if !ok {
			return fmt.Errorf("%s is not a file of build 1.0.903.854", rel)
		}
		f := byName[want.Path]
		if f == nil {
			return fmt.Errorf("%s has no %s", zipPath, want.Path)
		}
		out := filepath.Join(dawngateDir, filepath.FromSlash(want.CopyPath()))
		if err := restoreOne(f, out, want, buf); err != nil {
			return fmt.Errorf("restoring %s: %w", want.CopyPath(), err)
		}
	}
	return nil
}

// restoreOne writes one entry to out+".partial", checks it against want, sets the archive's time and renames it over
// out.
func restoreOne(f *zip.File, out string, want File, buf []byte) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := out + ".partial"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(dst, h), src, buf)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err == nil && (n != want.Size || hex.EncodeToString(h.Sum(nil)) != want.SHA256) {
		err = fmt.Errorf("the archive's %s doesn't match the manifest (%d bytes)", want.Path, n)
	}
	if err == nil {
		t := dosTime(f)
		err = os.Chtimes(tmp, t, t)
	}
	if err == nil {
		err = os.Rename(tmp, out)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
