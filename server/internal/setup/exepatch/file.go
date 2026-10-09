package exepatch

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileOptions are PatchFile's and State's options besides the patch's own.
type FileOptions struct {
	Options
	// BaseSHA256 is the SHA-256 the untouched exe (<exe>.orig, or the exe before its first patch) must have;
	// empty means Build854SHA256 (setup_all.py's base_of). Tests set it for their synthetic exes.
	BaseSHA256 string
}

func (o FileOptions) base() string {
	if o.BaseSHA256 == "" {
		return Build854SHA256
	}
	return o.BaseSHA256
}

// FileResult says what PatchFile did.
type FileResult struct {
	Summary   string // patch_client.py's summary line
	SavedOrig bool   // the untouched exe was saved as <exe>.orig now
	Changed   bool   // the exe was written (false: it held these bytes already)
}

// OrigSuffix names the untouched exe's backup next to it.
const OrigSuffix = ".orig"

// RefusePristine refuses a path with a folder named ia_* (the extracted builds kept as extracted, client/ia_*), as
// patch_client.py does (setup_server_copy.py: \client\ia_; this is the wider of the two, case-insensitive).
func RefusePristine(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(filepath.ToSlash(abs)), "/ia_") {
		return errorf("refusing to touch a pristine extraction (client/ia_*): %s; use a working copy", path)
	}
	return nil
}

// PatchFile patches a working copy's Dawngate.exe as patch_client.py's main() does, with setup_all.py's checks: the
// options are checked before anything is written; the untouched exe (<exe>.orig if it exists, else the exe itself)
// must have o's base SHA-256; the first run saves it as <exe>.orig (with its file times); every run patches from that
// backup, so re-running is safe and switching profiles is another run. The exe is written only when its bytes
// differ. modulus is ca_modulus.bin's 128 bytes.
func PatchFile(exe string, modulus []byte, o FileOptions) (FileResult, error) {
	var res FileResult
	if err := RefusePristine(exe); err != nil {
		return res, err
	}
	if _, _, err := ResolveProfile(o.Options); err != nil {
		return res, err
	}
	if err := CheckDataSuffix(o.DataSuffix); err != nil {
		return res, err
	}
	if len(modulus) != 128 {
		return res, errorf("expected a 128-byte (1024-bit) CA modulus, got %d", len(modulus))
	}
	orig := exe + OrigSuffix
	haveOrig := isFile(orig)
	base := exe
	if haveOrig {
		base = orig
	}
	untouched, err := os.ReadFile(base)
	if err != nil {
		return res, err
	}
	if got := sha256Hex(untouched); got != o.base() {
		return res, errorf("%s is not the untouched build-854 exe (SHA-256 %s, want %s); the patcher starts from it: "+
			"make the copy again", base, got[:16], o.base()[:16])
	}
	want, summary, err := Patch(untouched, modulus, o.Options)
	if err != nil {
		return res, err
	}
	res.Summary = summary
	if !haveOrig {
		if err := copyFileWithTimes(exe, orig); err != nil {
			return res, fmt.Errorf("saving the original as %s: %w", orig, err)
		}
		res.SavedOrig = true
	}
	if cur, err := os.ReadFile(exe); err == nil && bytes.Equal(cur, want) {
		return res, nil
	}
	if err := os.WriteFile(exe, want, 0o644); err != nil {
		return res, fmt.Errorf("writing %s (is the game running from this copy?): %w", exe, err)
	}
	res.Changed = true
	return res, nil
}

// State says whether exe is what PatchFile would make of it: "" when it is, else why not ("not patched (no .orig)",
// "not patched", the differing Description keys, or "an older patch"). keys nil compares the bytes (setup_all.py's
// exact check); HostKeys compares only what a match host's copy must carry. The error is for a missing or foreign
// untouched exe, unreadable files or refused options.
func State(exe string, modulus []byte, o FileOptions, keys []string) (string, error) {
	orig := exe + OrigSuffix
	base := exe
	if isFile(orig) {
		base = orig
	}
	untouched, err := os.ReadFile(base)
	if err != nil {
		return "", err
	}
	if got := sha256Hex(untouched); got != o.base() {
		return "", errorf("%s is not the untouched build-854 exe (SHA-256 %s); the patcher starts from it", base, got[:16])
	}
	want, _, err := Patch(untouched, modulus, o.Options)
	if err != nil {
		return "", err
	}
	if !isFile(exe) || !isFile(orig) {
		return "not patched (no .orig)", nil
	}
	cur, err := os.ReadFile(exe)
	if err != nil {
		return "", err
	}
	if bytes.Equal(cur, want) {
		return "", nil
	}
	if bytes.Equal(cur, untouched) {
		return "not patched", nil
	}
	got, err := Describe(cur)
	if err != nil {
		return "", err
	}
	wantD, err := Describe(want)
	if err != nil {
		return "", err
	}
	diff := Differences(got, wantD, keys)
	if keys != nil && len(diff) == 0 {
		return "", nil
	}
	if len(diff) == 0 {
		return "an older patch (patch_client.py makes other bytes now)", nil
	}
	return strings.Join(diff, "; "), nil
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// copyFileWithTimes copies src to a new dst and gives it src's modification time (shutil.copy2's part that matters).
func copyFileWithTimes(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return errors.Join(err, os.Remove(dst))
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, st.ModTime(), st.ModTime())
}
