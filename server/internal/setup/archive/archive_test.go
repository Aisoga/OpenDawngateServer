package archive

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/setup/setuptest"
)

// The generated table must be docs/file_manifest.csv's build-854 side (go generate after the CSV changed).
func TestManifestMatchesDocs(t *testing.T) {
	repo := setuptest.Repo()
	if repo == "" {
		t.Skip("no checkout")
	}
	f, err := os.Open(filepath.Join(repo, "docs", "file_manifest.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	docs, err := ParseManifestCSV(f)
	if err != nil {
		t.Fatal(err)
	}
	got, want := Build854().Files(), docs.Files()
	if len(got) != len(want) {
		t.Fatalf("manifest_gen.go has %d files, docs/file_manifest.csv %d: run go generate", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("manifest_gen.go %v, docs/file_manifest.csv %v: run go generate", got[i], want[i])
		}
	}
}

func TestBuild854(t *testing.T) {
	m := Build854()
	if m.Len() != 167 {
		t.Fatalf("%d files, want 167", m.Len())
	}
	for _, k := range KeyFiles {
		if _, ok := m.Lookup(k); !ok {
			t.Errorf("key file %s not in the manifest", k)
		}
	}
	exe, _ := m.Lookup("Dawngate/game/Dawngate.exe")
	if exe.Size != 17868560 || exe.SHA256 != "6f7c8e8243bd7a820699535cd84b64017d5a843db12a51be7c9a2e759dde0e2c" {
		t.Errorf("Dawngate.exe: %+v", exe)
	}
	if exe.CopyPath() != "game/Dawngate.exe" {
		t.Errorf("CopyPath %q", exe.CopyPath())
	}
}

type entry struct {
	name string
	data string
	t    time.Time
}

// makeZip writes a zip of entries (a name ending in "/" is a folder) and returns its path.
func makeZip(t *testing.T, entries []entry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if strings.HasSuffix(e.name, "/") {
			h.Method = zip.Store
		}
		h.Modified = e.t
		ew, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ew.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func manifestOf(entries []entry) *Manifest {
	var files []File
	for _, e := range entries {
		if !strings.HasSuffix(e.name, "/") {
			files = append(files, File{Path: e.name, Size: int64(len(e.data)), SHA256: sum(e.data)})
		}
	}
	return NewManifest(files)
}

var when = time.Date(2014, 12, 2, 18, 13, 4, 0, time.UTC)

func sample() []entry {
	return []entry{
		{"Dawngate/", "", when},
		{"Dawngate/game/", "", when},
		{"Dawngate/game/Dawngate.exe", "MZ fake exe", when},
		{"Dawngate/game/Data/Config.package", strings.Repeat("config ", 1000), when.Add(2 * time.Second)},
		{"Dawngate/game/readme.txt", "", when},
	}
}

func TestCheckZipRefusals(t *testing.T) {
	ok := []entry{{"Dawngate/", "", when}, {"Dawngate/a.txt", "x", when}}
	zr := func(entries []entry) []*zip.File {
		r, err := zip.OpenReader(makeZip(t, entries))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close() })
		return r.File
	}
	if err := CheckZip(zr(ok)); err != nil {
		t.Fatalf("a good archive refused: %v", err)
	}
	for _, name := range []string{"Other/a.txt", "/Dawngate/a.txt", "Dawngate/../a.txt", "Dawngate/c:/a.txt",
		"Dawngate/x\\..\\a.txt", "a.txt", "dawngate/a.txt"} {
		if err := CheckZip(zr(append(ok, entry{name, "x", when}))); err == nil {
			t.Errorf("%q not refused", name)
		}
	}
	// limits (small ones for the test; the real ones are MaxEntries, MaxBytes)
	files := zr(ok)
	if err := checkZip(files, Top, 1<<20, 1); err == nil {
		t.Error("too many entries not refused")
	}
	if err := checkZip(files, Top, 0, 10); err == nil {
		t.Error("too many bytes not refused")
	}
	if err := checkZip(nil, Top, 1<<20, 10); err == nil {
		t.Error("an empty archive not refused")
	}
}

func TestExtract(t *testing.T) {
	entries := sample()
	zp := makeZip(t, entries)
	dest := filepath.Join(t.TempDir(), "ia_Dawngate")
	// a .partial left by an interrupted run is removed
	if err := os.MkdirAll(filepath.Join(dest+".partial", "junk"), 0o755); err != nil {
		t.Fatal(err)
	}
	var phases []string
	res, err := Extract(zp, dest, manifestOf(entries), func(phase string, done, total int64) {
		if done > total {
			t.Errorf("%s: %d of %d", phase, done, total)
		}
		if len(phases) == 0 || phases[len(phases)-1] != phase {
			phases = append(phases, phase)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 3 || res.Bytes != int64(len("MZ fake exe")+7000) || res.KnownDownload || res.ZipSHA256 == "" {
		t.Errorf("result %+v", res)
	}
	if strings.Join(phases, ",") != PhaseHashZip+","+PhaseExtract+","+PhaseVerify {
		t.Errorf("phases %v", phases)
	}
	if _, err := os.Stat(dest + ".partial"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".partial still there: %v", err)
	}
	for _, e := range entries[2:] {
		p := filepath.Join(dest, filepath.FromSlash(e.name))
		b, err := os.ReadFile(p)
		if err != nil || string(b) != e.data {
			t.Errorf("%s: %v", e.name, err)
		}
		// the MS-DOS time read as local time, as Python's zipfile + time.mktime set it
		st, _ := os.Stat(p)
		want := time.Date(e.t.Year(), e.t.Month(), e.t.Day(), e.t.Hour(), e.t.Minute(), e.t.Second(), 0, time.Local)
		if !st.ModTime().Equal(want) {
			t.Errorf("%s: time %v, want %v", e.name, st.ModTime(), want)
		}
	}
	if _, err := Extract(zp, dest, manifestOf(entries), nil); err == nil {
		t.Error("an existing extraction was written again")
	}
	problems, err := VerifyTree(dest, manifestOf(entries), true, nil)
	if err != nil || len(problems) != 0 {
		t.Errorf("VerifyTree %v %v", problems, err)
	}
}

func TestExtractMismatchLeavesPartial(t *testing.T) {
	entries := sample()
	m := manifestOf(entries)
	files := m.Files()
	files[0].SHA256 = sum("something else")
	dest := filepath.Join(t.TempDir(), "ia_Dawngate")
	_, err := Extract(makeZip(t, entries), dest, NewManifest(files), nil)
	var ve *VerifyError
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || !strings.Contains(ve.Problems[0], "differs (SHA-256)") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest+".partial", "Dawngate", "game", "Dawngate.exe")); err != nil {
		t.Errorf("the .partial folder wasn't left for a look: %v", err)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dest made despite the mismatch")
	}
}

func TestVerifyTree(t *testing.T) {
	entries := sample()
	dest := filepath.Join(t.TempDir(), "x")
	m := manifestOf(entries)
	if _, err := Extract(makeZip(t, entries), dest, m, nil); err != nil {
		t.Fatal(err)
	}
	// extra files are allowed
	if err := os.WriteFile(filepath.Join(dest, "Dawngate", "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// same size, other bytes: found only with full, or for a key file
	cfg := filepath.Join(dest, "Dawngate", "game", "Data", "Config.package")
	if err := os.WriteFile(cfg, bytes.Repeat([]byte("CONFIG "), 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, _ := VerifyTree(dest, m, false, nil)
	if len(problems) != 1 || problems[0] != "Dawngate/game/Data/Config.package differs (SHA-256)" {
		t.Errorf("key file: %v", problems)
	}
	txt := filepath.Join(dest, "Dawngate", "game", "readme.txt")
	if err := os.WriteFile(txt, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dest, "Dawngate", "game", "Dawngate.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(txt); err != nil {
		t.Fatal(err)
	}
	problems, _ = VerifyTree(dest, m, false, nil)
	want := []string{
		"Dawngate/game/Data/Config.package differs (SHA-256)",
		"Dawngate/game/Dawngate.exe has 2 bytes, want 11",
		"Dawngate/game/readme.txt is missing",
	}
	if strings.Join(problems, "|") != strings.Join(want, "|") {
		t.Errorf("got %q\nwant %q", problems, want)
	}
}

func TestCheckCopy(t *testing.T) {
	entries := sample()
	m := manifestOf(entries)
	dest := filepath.Join(t.TempDir(), "x")
	if _, err := Extract(makeZip(t, entries), dest, m, nil); err != nil {
		t.Fatal(err)
	}
	copyDir := filepath.Join(dest, "Dawngate")
	if bad, err := CheckCopy(copyDir, m, nil); err != nil || len(bad) != 0 {
		t.Errorf("complete copy: %v %v", bad, err)
	}
	// a patched file of another size is exempt; a missing file is not
	if err := os.WriteFile(filepath.Join(copyDir, "game", "Data", "Config.package"), []byte("patched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(copyDir, "game", "readme.txt")); err != nil {
		t.Fatal(err)
	}
	bad, err := CheckCopy(copyDir, m, PatchedFiles)
	if err != nil || strings.Join(bad, ",") != "game/readme.txt" {
		t.Errorf("got %v %v", bad, err)
	}
	if bad, _ := CheckCopy(copyDir, m, nil); len(bad) != 2 {
		t.Errorf("without exemptions: %v", bad)
	}
}

// The main checkout's client/ia_Dawngate (extracted by extract_client.py; read only): sizes and key files.
func TestVerifyExtractedBuild(t *testing.T) {
	exe := setuptest.GameFile(t, "DG_IA_DAWNGATE_EXE", "game/Dawngate.exe")
	base := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // .../ia_Dawngate
	problems, err := VerifyTree(base, Build854(), os.Getenv("DG_VERIFY_FULL") != "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Errorf("%s: %v", base, problems)
	}
}

// The real archive, only on request (1.3 GB in, 1.5 GB out): DG_ARCHIVE_EXTRACT_TO=<new folder> extracts Dawngate.zip
// (DG_GAME_ZIP or the main checkout's) there and compares every file's time with the main checkout's
// client/ia_Dawngate (extract_client.py's extraction).
func TestExtractRealArchive(t *testing.T) {
	dest := os.Getenv("DG_ARCHIVE_EXTRACT_TO")
	if dest == "" {
		t.Skip("set DG_ARCHIVE_EXTRACT_TO=<a new folder> to extract the real Dawngate.zip")
	}
	zp := setuptest.Find(os.Getenv("DG_GAME_ZIP"), GameZipName)
	if zp == "" {
		t.Skip("no Dawngate.zip")
	}
	t0 := time.Now()
	last := ""
	res, err := Extract(zp, dest, Build854(), func(phase string, done, total int64) {
		if phase != last {
			t.Logf("%6.1fs %s (%d bytes)", time.Since(t0).Seconds(), phase, total)
			last = phase
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", res)
	if !res.KnownDownload || res.Files != 167 {
		t.Errorf("result %+v", res)
	}
	ref := setuptest.Find("client/ia_Dawngate/Dawngate/game/Dawngate.exe")
	if ref == "" {
		return
	}
	refBase := filepath.Dir(filepath.Dir(filepath.Dir(ref)))
	for _, f := range Build854().Files() {
		a, err1 := os.Stat(filepath.Join(dest, filepath.FromSlash(f.Path)))
		b, err2 := os.Stat(filepath.Join(refBase, filepath.FromSlash(f.Path)))
		if err1 != nil || err2 != nil {
			t.Errorf("%s: %v %v", f.Path, err1, err2)
			continue
		}
		if !a.ModTime().Equal(b.ModTime()) {
			t.Errorf("%s: time %v, extract_client.py's %v", f.Path, a.ModTime(), b.ModTime())
		}
	}
}
