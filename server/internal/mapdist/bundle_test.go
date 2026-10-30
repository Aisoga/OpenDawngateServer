package mapdist

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/testmaps"
)

// copyPkg copies a fixture package into dir/<name> and returns that folder.
func copyPkg(t *testing.T, name, dir string) string {
	t.Helper()
	src, dst := testmaps.Pkg(name), filepath.Join(dir, name)
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// bundleOf is a package's bundle bytes.
func bundleOf(t *testing.T, dir string) []byte {
	t.Helper()
	p, err := ReadPackage(dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if _, err := p.WriteTo(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestPackageFiles: CM_Marsh names one package PNG (its moss), CM_Ring none.
func TestPackageFiles(t *testing.T) {
	for _, c := range []struct {
		name string
		want []string
	}{{"CM_Marsh", []string{"ground/marsh_moss.png"}}, {"CM_Ring", nil}} {
		m, err := mapc.Load(testmaps.Pkg(c.name))
		if err != nil {
			t.Fatal(err)
		}
		if got := mapc.PackageFiles(m); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestBundleRoundTrip: CM_Marsh as a bundle (the head's bytes pinned), unpacked into a new folder: the same files,
// the same hash and size; the map reads and gives the same bundle again.
func TestBundleRoundTrip(t *testing.T) {
	src := testmaps.Pkg("CM_Marsh")
	b := bundleOf(t, src)
	js, _ := os.ReadFile(filepath.Join(src, "map.json"))
	png, _ := os.ReadFile(filepath.Join(src, "ground", "marsh_moss.png"))
	want := []byte("DGMP\x01\x00\x02\x00\x08map.json")
	want = binary.BigEndian.AppendUint32(want, uint32(len(js)))
	want = append(want, js...)
	want = append(want, "\x00\x15ground/marsh_moss.png"...)
	want = binary.BigEndian.AppendUint32(want, uint32(len(png)))
	want = append(want, png...)
	if !bytes.Equal(b, want) {
		t.Fatalf("the bundle's bytes (%d) aren't the format's (%d)", len(b), len(want))
	}
	p, _ := ReadPackage(src, DefaultLimits())
	sum, n, err := p.Hash()
	if err != nil || sum != sha256.Sum256(b) || n != int64(len(b)) || p.Size() != n {
		t.Fatalf("Hash %x %d %v, Size %d; the bytes' %x %d", sum[:4], n, err, p.Size(), sha256.Sum256(b), len(b))
	}
	dir := filepath.Join(t.TempDir(), "out")
	u, err := Unpack(bytes.NewReader(b), dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(u.Files, ",") != "map.json,ground/marsh_moss.png" || u.SHA256 != sum || u.Size != n {
		t.Errorf("unpacked %+v", u)
	}
	if again := bundleOf(t, dir); !bytes.Equal(again, b) {
		t.Error("the unpacked package bundles to other bytes")
	}
}

// fakeBundle writes a bundle of the given files as the format says, without any check (the attacker's side).
func fakeBundle(magic string, version byte, files ...[2]string) []byte {
	b := []byte(magic)
	b = append(b, version)
	b = binary.BigEndian.AppendUint16(b, uint16(len(files)))
	for _, f := range files {
		b = binary.BigEndian.AppendUint16(b, uint16(len(f[0])))
		b = append(b, f[0]...)
		b = binary.BigEndian.AppendUint32(b, uint32(len(f[1])))
		b = append(b, f[1]...)
	}
	return b
}

// pngHeadOf is a PNG's first 24 bytes for a w x h picture (enough for Unpack's check) plus some bytes.
func pngHeadOf(w, h uint32) string {
	b := append([]byte(nil), pngSignature...)
	b = append(b, 0, 0, 0, 13)
	b = append(b, "IHDR"...)
	b = binary.BigEndian.AppendUint32(b, w)
	b = binary.BigEndian.AppendUint32(b, h)
	return string(append(b, "rest of the picture"...))
}

// TestUnpackRefuses: every bound and name rule refuses the package with ErrBundle, writes nothing outside the folder,
// and a short or cut package is an error too.
func TestUnpackRefuses(t *testing.T) {
	js := [2]string{"map.json", `{"format": 1}`}
	okPNG := pngHeadOf(16, 16)
	lim := DefaultLimits()
	small := lim
	small.JSON, small.File, small.Total, small.Files = 64, 100, 300, 3
	many := []([2]string){js}
	for i := 0; i < 65; i++ {
		many = append(many, [2]string{"g/" + hex.EncodeToString([]byte{byte(i)}) + ".png", okPNG})
	}
	cases := []struct {
		name string
		b    []byte
		lim  Limits
		why  string
	}{
		{"magic", fakeBundle("PK\x03\x04", 1, js), lim, "not a map package"},
		{"version", fakeBundle(Magic, 2, js), lim, "format 2"},
		{"no files", fakeBundle(Magic, 1), lim, "0 files"},
		{"too many files", fakeBundle(Magic, 1, many...), lim, "66 files"},
		{"parent", fakeBundle(Magic, 1, js, [2]string{"../evil.png", okPNG}), lim, "clean path"},
		{"deep parent", fakeBundle(Magic, 1, js, [2]string{"ground/../../evil.png", okPNG}), lim, "clean path"},
		{"backslash", fakeBundle(Magic, 1, js, [2]string{`ground\..\..\evil.png`, okPNG}), lim, "relative path"},
		{"absolute", fakeBundle(Magic, 1, js, [2]string{"/evil.png", okPNG}), lim, "relative path"},
		{"drive", fakeBundle(Magic, 1, js, [2]string{"C:/evil.png", okPNG}), lim, "relative path"},
		{"device", fakeBundle(Magic, 1, js, [2]string{"ground/CON.png", okPNG}), lim, "device"},
		{"device com", fakeBundle(Magic, 1, js, [2]string{"com1.png", okPNG}), lim, "device"},
		{"dot file", fakeBundle(Magic, 1, js, [2]string{".hidden.png", okPNG}), lim, "dot"},
		{"trailing dot", fakeBundle(Magic, 1, js, [2]string{"ground./a.png", okPNG}), lim, "dot"},
		{"too deep", fakeBundle(Magic, 1, js, [2]string{"a/b/c/d/e.png", okPNG}), lim, "folder levels"},
		{"not a png name", fakeBundle(Magic, 1, js, [2]string{"run.exe", okPNG}), lim, ".png file"},
		{"a second map.json", fakeBundle(Magic, 1, js, js), lim, "first, once"},
		{"map.json not first", fakeBundle(Magic, 1, [2]string{"a.png", okPNG}, js), lim, "first, once"},
		{"twice ignoring case", fakeBundle(Magic, 1, js, [2]string{"a.png", okPNG}, [2]string{"A.png", okPNG}), lim, "twice"},
		{"map.json too big", fakeBundle(Magic, 1, [2]string{"map.json", strings.Repeat(" ", 65)}), small, "map.json: 65 bytes"},
		{"png too big", fakeBundle(Magic, 1, js, [2]string{"a.png", okPNG + strings.Repeat("x", 100)}), small, "at most 100"},
		{"total", fakeBundle(Magic, 1, js, [2]string{"a.png", okPNG}, [2]string{"b.png", okPNG + strings.Repeat("x", 50)},
			[2]string{"c.png", okPNG}), Limits{Files: 4, JSON: 64, File: 100, Total: 200, Side: 4096}, "more than 200 bytes"},
		{"not a png", fakeBundle(Magic, 1, js, [2]string{"a.png", "MZ" + strings.Repeat("\x00", 40)}), lim, "not a PNG"},
		{"png too short", fakeBundle(Magic, 1, js, [2]string{"a.png", "\x89PNG"}), lim, "not a PNG"},
		{"png too wide", fakeBundle(Magic, 1, js, [2]string{"a.png", pngHeadOf(4097, 16)}), lim, "4097 x 16"},
		{"png empty", fakeBundle(Magic, 1, js, [2]string{"a.png", pngHeadOf(0, 16)}), lim, "0 x 16"},
		{"bytes after", append(fakeBundle(Magic, 1, js), "more"...), lim, "bytes after the last file"},
	}
	for _, c := range cases {
		root := t.TempDir()
		dir := filepath.Join(root, "pkg")
		_, err := Unpack(bytes.NewReader(c.b), dir, c.lim)
		if !errors.Is(err, ErrBundle) || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v, want ErrBundle with %q", c.name, err, c.why)
		}
		if ents, _ := os.ReadDir(root); len(ents) > 1 || (len(ents) == 1 && ents[0].Name() != "pkg") {
			t.Errorf("%s: wrote outside the folder: %v", c.name, ents)
		}
	}
	for _, cut := range []int{3, 9, 20} { // a package cut short: an error, not a refusal of its content
		b := fakeBundle(Magic, 1, js, [2]string{"a.png", okPNG})
		if _, err := Unpack(bytes.NewReader(b[:len(b)-cut]), filepath.Join(t.TempDir(), "pkg"), lim); err == nil ||
			errors.Is(err, ErrBundle) || !strings.Contains(err.Error(), "ends early") {
			t.Errorf("cut %d: %v", cut, err)
		}
	}
	if _, err := Unpack(bytes.NewReader(fakeBundle(Magic, 1, js)), t.TempDir(), lim); err == nil { // the folder exists
		t.Error("unpacked into an existing folder")
	}
}

// TestCheckName: names a package may have.
func TestCheckName(t *testing.T) {
	for _, n := range []string{"map.json", "ground/tile_0_0.png", "ground/tile_1_1_6a7fc0439a.png", "a-b_c.d.PNG",
		"x/y/z/w.png", "console.png", "com10.png"} {
		if err := CheckName(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	for _, n := range []string{"Map.json", "layout.json", "a b.png", "a/./b.png", "", strings.Repeat("a", 157) + ".png",
		"nul.tar.png", "aux.png", "lpt9.png", "a:b.png"} {
		if CheckName(n) == nil {
			t.Errorf("%q passed", n)
		}
	}
}

// TestReadPackageBounds: the sender's side refuses what the receiver would.
func TestReadPackageBounds(t *testing.T) {
	dir := copyPkg(t, "CM_Marsh", t.TempDir())
	lim := DefaultLimits()
	lim.File = 100
	if _, err := ReadPackage(dir, lim); err == nil || !strings.Contains(err.Error(), "marsh_moss.png") {
		t.Errorf("a big PNG: %v", err)
	}
	os.Remove(filepath.Join(dir, "ground", "marsh_moss.png"))
	if _, err := ReadPackage(dir, DefaultLimits()); err == nil {
		t.Error("a missing PNG passed")
	}
}

// TestManifest: the text both ways; malformed lines refused.
func TestManifest(t *testing.T) {
	es := []Entry{{Tag: "ring", Name: "CM_Ring", Size: 1234, SHA256: sha256.Sum256([]byte("r"))},
		{Tag: "marsh", Name: "CM_Marsh", Size: 99, SHA256: sha256.Sum256([]byte("m"))}}
	text := FormatManifest(es)
	if !strings.HasPrefix(text, "marsh CM_Marsh 99 ") || strings.Count(text, "\n") != 2 {
		t.Errorf("the text %q", text)
	}
	got, err := ParseManifest(text + "\n")
	if err != nil || len(got) != 2 || got["ring"] != es[0] || got["marsh"] != es[1] {
		t.Errorf("parsed %v %v", got, err)
	}
	h := hex.EncodeToString(es[0].SHA256[:])
	for _, bad := range []string{"ring CM_Ring 12 " + h[:60], "Ring CM_Ring 12 " + h, "ring ../x 12 " + h,
		"ring CM_Ring -1 " + h, "ring CM_Ring 12", "ring CM_Ring 12 " + h + "\nring CM_Ring 12 " + h} {
		if _, err := ParseManifest(bad); err == nil {
			t.Errorf("%q passed", bad)
		}
	}
}
