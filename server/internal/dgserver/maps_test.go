package dgserver

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/testmaps"
)

// TestAddFolderMaps: the test maps' fixture folder (server/testdata/maps) offers CM_Ring by name and tag (twice in one process is fine); a
// broken package is left out with a warning; no folder, no maps.
func TestAddFolderMaps(t *testing.T) {
	orig := blaze.PlayableLevels
	t.Cleanup(func() { blaze.PlayableLevels = orig })
	dir := testmaps.Dir()
	for range 2 {
		notes, err := addFolderMaps(dir)
		if err != nil || len(notes) != 1 || !strings.Contains(notes[0], `CM_Ring (~#ring, "Test Ring")`) {
			t.Fatalf("%v %q", err, notes)
		}
	}
	if got, err := blaze.ParseLevel("ring"); err != nil || got != "CM_Ring" {
		t.Errorf("ParseLevel(ring) = %q, %v", got, err)
	}
	if l, _ := blaze.FindLevel("CM_Ring"); l.Archive != "zz_CM_Ring.snap" || l.Label != "[Ring] " {
		t.Errorf("%+v", l)
	}
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, "CM_Bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "CM_Bad", "map.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes, err := addFolderMaps(bad)
	if err != nil || len(notes) != 2 || !strings.HasPrefix(notes[0], "WARNING: maps folder") ||
		!strings.Contains(notes[1], "no map packages") {
		t.Errorf("%v %q", err, notes)
	}
	if notes, err := addFolderMaps(""); notes != nil || err != nil {
		t.Errorf("%v %v", notes, err)
	}
	// the folder read again after its package changed (This PC started again by the map editor's Test): replaced
	edited := t.TempDir()
	ring, err := os.ReadFile(filepath.Join(dir, "CM_Ring", "map.json"))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(edited, "CM_Ring"), 0o755)
	os.WriteFile(filepath.Join(edited, "CM_Ring", "map.json"), []byte(strings.Replace(string(ring), `"Test Ring"`, `"Test Ring v2"`, 1)), 0o644)
	if notes, err := addFolderMaps(edited); err != nil || len(notes) != 1 || !strings.Contains(notes[0], `CM_Ring (~#ring, "Test Ring v2")`) {
		t.Fatalf("the edited folder: %v %q", err, notes)
	}
	if blaze.LevelDisplay("CM_Ring") != "Test Ring v2" {
		t.Errorf("after the edit: %q", blaze.LevelDisplay("CM_Ring"))
	}
}

// mapVersions: the maps folder's archives in the host copy, hashed (a missing one named in a note); no host copy, no
// versions; the start-up line names the versioned maps; --client-maps and --tunnel-maps are taken.
func TestMapVersions(t *testing.T) {
	orig := blaze.PlayableLevels
	t.Cleanup(func() { blaze.PlayableLevels = orig })
	dir := testmaps.Dir()
	if _, err := addFolderMaps(dir); err != nil {
		t.Fatal(err)
	}
	host := t.TempDir()
	data := filepath.Join(host, "Dawngate", "game", "Data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(host, "Dawngate", "game", "Dawngate.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "zz_CM_Ring.snap"), []byte("ring"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, notes := mapVersions(dir, host)
	want := sha256.Sum256([]byte("ring"))
	if len(v) != 1 || v["zz_cm_ring.snap"] != want || len(notes) != 2 ||
		!strings.Contains(notes[0], "map check by version: zz_CM_Ring.snap "+hex.EncodeToString(want[:4])) ||
		!strings.Contains(notes[1], "map check: no version of zz_CM_") {
		t.Errorf("mapVersions = %v, %q", v, notes)
	}
	if v, notes := mapVersions(dir, ""); v != nil || len(notes) != 1 || !strings.Contains(notes[0], "no host copy (--gms-dir)") {
		t.Errorf("no host copy: %v, %q", v, notes)
	}
	if v, notes := mapVersions("", host); v != nil || notes != nil {
		t.Errorf("no maps folder: %v, %q", v, notes)
	}
	n := mapCheckNote(config{customLevels: []string{"CM_Ring", "MM_Map_v3"}, mapVersions: v})
	if !strings.HasPrefix(n, "map check: CM_Ring only for a player whose copy has the host copy's version of its archive") ||
		!strings.Contains(n, "as follows. CM_Ring, MM_Map_v3 only for Start-Client of this server's release") {
		t.Errorf("the note %q", n)
	}
	h := hex.EncodeToString(want[:])
	for _, args := range [][]string{{"--client-maps", "127.0.0.4=zz_CM_Ring.snap:" + h + ",127.0.4.2="},
		{"--tunnel-maps", host}} {
		if code := Run(append(args, "--print-ports"), Hooks{}); code != 0 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if code := Run([]string{"--client-maps", "127.0.0.4=zz_CM_Ring.snap:abc", "--print-ports"}, Hooks{}); code != 2 {
		t.Errorf("a bad --client-maps: exit %d", code)
	}
	cm, _ := blaze.ParseClientMaps("127.0.4.2=,127.0.0.4=zz_CM_Ring.snap:" + h)
	if d := describeClientMaps(cm); d != "127.0.0.4: zz_CM_Ring.snap "+h[:8]+"; 127.0.4.2: none" {
		t.Errorf("describeClientMaps = %q", d)
	}
}
