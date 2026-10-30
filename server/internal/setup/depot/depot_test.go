package depot

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"dawngate/server/internal/setup/snap"
)

func TestMountOrder(t *testing.T) {
	copyDir := t.TempDir()
	data := filepath.Join(copyDir, "game", "Data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, files ...snap.File) {
		if err := os.WriteFile(filepath.Join(data, name), snap.Pack(files), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	long := make([]byte, 5000) // compressible, so the entry is a Snappy block
	for i := range long {
		long[i] = byte(i % 7)
	}
	write("Alpha.snap", snap.File{Name: "Models/A.bmdl", Data: []byte("alpha")},
		snap.File{Name: "shared/x.dds", Data: []byte("from Alpha")})
	write("aa_added.snap", snap.File{Name: "shared/X.dds", Data: []byte("from aa_added")})
	write("zz_last.snap", snap.File{Name: "Shared\\x.dds", Data: []byte("from zz_last")},
		snap.File{Name: "big/long.bin", Data: long})
	if err := os.WriteFile(filepath.Join(data, "notes.txt"), []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Open(copyDir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// aa_added < Alpha < zz_last without regard to case: the last archive holding a path wins
	if b, err := d.Read("SHARED/x.DDS"); err != nil || string(b) != "from zz_last" || d.Archive("shared/x.dds") != "zz_last.snap" {
		t.Errorf("shared/x.dds: %q %v from %s", b, err, d.Archive("shared/x.dds"))
	}
	if b, err := d.Read(`models\a.bmdl`); err != nil || string(b) != "alpha" {
		t.Errorf("models/a.bmdl: %q %v", b, err)
	}
	if b, err := d.Read("big/long.bin"); err != nil || len(b) != len(long) || b[4999] != long[4999] {
		t.Errorf("big/long.bin: %d bytes, %v", len(b), err)
	}
	if _, err := d.Read("nope.dds"); !errors.Is(err, fs.ErrNotExist) || d.Has("nope.dds") || !d.Has("Models/A.bmdl") {
		t.Errorf("a missing path: %v", err)
	}
	if p := d.Paths("shared/"); len(p) != 1 || p[0] != "shared/x.dds" {
		t.Errorf("Paths: %v", p)
	}
	// Files lists the hidden entries too, each read from its own archive; Head reads a start
	var seen []string
	for _, f := range d.Files() {
		seen = append(seen, f.Archive+":"+f.Name)
		if f.Name == "shared/x.dds" {
			if b, err := d.ReadFile(f); err != nil || string(b) != "from Alpha" || f.Size != 10 {
				t.Errorf("Alpha's hidden x.dds: %q %v", b, err)
			}
		}
		if f.Name == "big/long.bin" {
			if b, err := d.Head(f, 100); err != nil || len(b) < 100 || len(b) >= len(long) || b[99] != long[99] {
				t.Errorf("Head: %d bytes, %v", len(b), err)
			}
		}
	}
	if len(seen) != 5 || seen[0] != "aa_added.snap:shared/X.dds" || seen[4] != "zz_last.snap:big/long.bin" {
		t.Errorf("Files: %v", seen)
	}
	// a broken archive is named
	if err := os.WriteFile(filepath.Join(data, "zzz_broken.snap"), []byte("pans but cut"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(copyDir); err == nil {
		t.Error("a broken archive was mounted")
	}
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("a folder without game/Data was accepted")
	}
}
