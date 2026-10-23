package moviepatch

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/snap"
)

// TestServerMaps: the copy's maps that the server offers; a map the copy lacks is never listed.
func TestServerMaps(t *testing.T) {
	mm := ExtraMaps[0]
	for _, c := range []struct {
		have    []Map
		offered []string
		want    int
	}{
		{ExtraMaps, []string{"mm"}, 1},
		{ExtraMaps, nil, 0},
		{ExtraMaps, []string{"xx"}, 0},
		{nil, []string{"mm"}, 0}, // the copy lacks MM's files (MAPGUARD): never listed
	} {
		got := ServerMaps(c.have, c.offered)
		if len(got) != c.want || (c.want == 1 && got[0] != mm) {
			t.Errorf("ServerMaps(%v, %v) = %v", c.have, c.offered, got)
		}
	}
}

// TestWriteMapList: the front's in-place write into a made-up copy's archive (the same layout, only the entry's
// bytes), the setup's view of it (MapListOnly), and an archive of the layout before (refused, unchanged).
// test_picker.py pins the same hashes for patch_login_movie.py's write_map_list.
func TestWriteMapList(t *testing.T) {
	sp := MatchOptions
	sp.Tags = []byte("OURTAGS")
	src := snap.Pack([]snap.File{{Name: MatchOptions.MoviePath, Data: synth()}})
	withMM, err := sp.BuildMaps(src, ExtraMaps)
	if err != nil {
		t.Fatal(err)
	}
	none, _ := sp.BuildMaps(src, nil)
	copy := t.TempDir()
	path := MapListArchive(copy)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, withMM, 0o644); err != nil {
		t.Fatal(err)
	}
	// a server without MM: the list without it, in place (byte for byte the setup's archive without maps)
	if changed, err := WriteMapList(copy, nil); err != nil || !changed {
		t.Fatalf("WriteMapList(none): %v, %v", changed, err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, none) || sum(got) != "e4994e8df80b86886df2a24a92f90ab698bd91a572be6e0a66cff9ab3a881471" {
		t.Errorf("the archive after the write: %s, want the setup's without maps", sum(got))
	}
	if !MapListOnly(got, withMM) || MapListOnly(withMM, withMM) {
		t.Error("MapListOnly: the front's list isn't told from the setup's")
	}
	if changed, err := WriteMapList(copy, nil); err != nil || changed {
		t.Errorf("the same list again: %v, %v (want unchanged)", changed, err)
	}
	if changed, err := WriteMapList(copy, ExtraMaps); err != nil || !changed {
		t.Errorf("WriteMapList(MM): %v, %v", changed, err)
	}
	if got, _ = os.ReadFile(path); !bytes.Equal(got, withMM) {
		t.Errorf("back to MM: %s", sum(got))
	}
	// another movie in the archive isn't the front's list
	other := append([]byte(nil), withMM...)
	other[snap.HeaderSize+3] ^= 1
	if MapListOnly(other, withMM) {
		t.Error("MapListOnly: a changed movie counts as a map list")
	}
	// the layout before MAPDROP-server (a compressed list): not written, the file as it was
	old := snap.Pack([]snap.File{{Name: MatchOptions.MoviePath, Data: synth()},
		{Name: MapListPath, Data: []byte("dawngate-maps\t1\nmap\tmm\tMM (one lane)\nend\n")}})
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMapList(copy, nil); !errors.Is(err, ErrMapListLayout) {
		t.Errorf("an older layout: %v", err)
	}
	if got, _ = os.ReadFile(path); !bytes.Equal(got, old) {
		t.Error("an older layout was changed")
	}
	if MapListOnly(old, withMM) {
		t.Error("MapListOnly: an older layout counts")
	}
	if _, err := WriteMapList(t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "zz_MatchOptions.snap") {
		t.Errorf("no archive: %v", err)
	}
}
