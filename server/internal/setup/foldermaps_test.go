package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/botprofiles"
	"dawngate/server/internal/setup/localepatch"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

// repoMaps is the test maps' fixture folder (server/testdata/maps; CM_Ring among them).
var repoMaps = testmaps.Dir()

// ring854 is CM_Ring's archive from build 854's files (mapc's TestGame pin).
const ring854 = "0e25f79534e7aa94b4a33d55d432dfc78b944fcfd4b3fba74403c508389a241d"

// TestFolderMapsNone: without a maps folder (or with an empty one) nothing is reported, the catalog, the dialog and the
// lobby line stay as without maps.
func TestFolderMapsNone(t *testing.T) {
	for _, dir := range []string{"", t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		s := &Setup{Out: &bytes.Buffer{}, Maps: dir}
		s.stepFolderMaps("client", t.TempDir())
		if len(s.Lines()) != 0 || s.mapCatalogs() != nil || s.FolderMaps() != nil || s.lobbyMap() != "" {
			t.Errorf("%q: %v", dir, s.Lines())
		}
	}
}

// TestFolderMapsLeftOut: a package that fails a rule is left out with a SKIPPED line (the setup stays complete), but
// counts for the lobby line (a folder with a package, as the Python setups decide).
func TestFolderMapsLeftOut(t *testing.T) {
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join(repoMaps, "CM_Ring", mapc.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "CM_Bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(b), `"size": 440`, `"size": 200`, 1) // the bases off the map
	if err := os.WriteFile(filepath.Join(dir, "CM_Bad", mapc.FileName), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Setup{Out: &bytes.Buffer{}, Maps: dir}
	s.stepFolderMaps("host", t.TempDir())
	if l := s.Lines(); len(l) != 1 || l[0].Status != Skipped || !strings.Contains(l[0].Detail, "left out") ||
		s.Failed() {
		t.Errorf("%v", l)
	}
	if s.lobbyMap() != localepatch.LobbyMapAny || len(s.FolderMaps()) != 0 {
		t.Errorf("lobby %q, %d maps", s.lobbyMap(), len(s.FolderMaps()))
	}
}

// TestFolderMapsStep compiles CM_Ring into a copy holding the game's own input files: missing (--check), done, the
// pinned archive, current, one build for two copies; the bot profiles carry its catalog entries, the dialog lists it
// once the archive is there, and the lobby line names none.
func TestFolderMapsStep(t *testing.T) {
	copyDir := t.TempDir()
	var files []archive.File
	// the inputs, and the archive of the shipped cliff texture CM_Ring's baked ground names
	for _, rel := range append(append([]string{}, mapc.Sources...), "game/Data/anshavar2.snap") {
		b, err := os.ReadFile(filepath.Join(testgame.Dir(t), filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(copyDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, archive.File{Path: archive.Top + "/" + rel, Size: int64(len(b)), SHA256: digest(b)})
	}
	m := archive.NewManifest(files)
	added := filepath.Join(copyDir, "game", "Data", "zz_CM_Ring.snap")
	// a maps folder with the repository's CM_Ring alone (the repository's holds more maps)
	maps := t.TempDir()
	ring, err := os.ReadFile(filepath.Join(repoMaps, "CM_Ring", mapc.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(maps, "CM_Ring"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(maps, "CM_Ring", mapc.FileName), ring, 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(check bool) *Setup {
		s := &Setup{Out: &bytes.Buffer{}, Manifest: m, Check: check, Maps: maps}
		s.stepFolderMaps("client", copyDir)
		return s
	}
	if s := run(true); len(s.Lines()) != 1 || s.Lines()[0].Status != Missing || s.Lines()[0].Step != "client CM_Ring" {
		t.Fatalf("--check on a new copy: %v", s.Lines())
	}
	if s := run(false); s.Lines()[0].Status != Done {
		t.Fatalf("%v", s.Lines())
	}
	b, err := os.ReadFile(added)
	if err != nil || digest(b) != ring854 {
		t.Fatalf("the archive: %v, SHA-256 %s, pinned %s", err, digest(b), ring854)
	}
	s := run(true)
	if s.Lines()[0].Status != OK {
		t.Errorf("current: %v", s.Lines())
	}
	s.stepFolderMaps("host", copyDir) // the second copy reuses the bytes
	if l := s.Lines(); len(l) != 2 || l[1].Status != OK {
		t.Errorf("%v", l)
	}
	// the catalog, the dialog, the lobby line
	cats := s.mapCatalogs()
	if len(cats) != 1 || len(cats[0].Entries) != len(mapc.AssetNames("CM_Ring")) {
		t.Errorf("catalogs %+v", cats)
	}
	ad, err := os.ReadFile(filepath.Join(copyDir, filepath.FromSlash(mapc.AssetDataSource)))
	if err != nil {
		t.Fatal(err)
	}
	with, err1 := botprofiles.Build(ad, cats...)
	without, err2 := botprofiles.Build(ad)
	if err1 != nil || err2 != nil || bytes.Equal(with, without) {
		t.Errorf("the bot profiles' archive with the map's entries: %v %v, equal %v", err1, err2, bytes.Equal(with, without))
	}
	dialog := moviepatch.CopyMaps(filepath.Join(copyDir, "game", "Data"), DialogMaps(s.FolderMaps())...)
	if len(dialog) != 1 || dialog[0] != (moviepatch.Map{Tag: "ring", Archive: "zz_CM_Ring.snap", Label: "Test Ring"}) {
		t.Errorf("the dialog's maps %+v", dialog)
	}
	if s.lobbyMap() != localepatch.LobbyMapAny {
		t.Errorf("lobby %q", s.lobbyMap())
	}
}

// TestFolderMapsSkipFailed: a package whose build fails is FAILED (the setup incomplete), but SKIPPED with
// SkipFailedMaps (the map editor), and MapLine finds its line in either case; a package a rule check left out is found
// by MapLine too.
func TestFolderMapsSkipFailed(t *testing.T) {
	copyDir := t.TempDir()
	var files []archive.File
	for _, rel := range mapc.Sources { // the copy's inputs aren't the build's: every build fails
		p := filepath.Join(copyDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("not the game's "+rel), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, archive.File{Path: archive.Top + "/" + rel, Size: 1, SHA256: digest([]byte(rel))})
	}
	m := archive.NewManifest(files)
	maps := t.TempDir()
	ring, err := os.ReadFile(filepath.Join(repoMaps, "CM_Ring", mapc.FileName))
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.NewReplacer(`"size": 440`, `"size": 200`, `"CM_Ring"`, `"CM_Bad"`, `"tag": "ring"`, `"tag": "bad"`).Replace(string(ring))
	for name, text := range map[string]string{"CM_Ring": string(ring), "CM_Bad": bad} {
		if err := os.MkdirAll(filepath.Join(maps, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(maps, name, mapc.FileName), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, skip := range []bool{false, true} {
		s := &Setup{Out: &bytes.Buffer{}, Manifest: m, Maps: maps, SkipFailedMaps: skip}
		s.stepFolderMaps("client", copyDir)
		l, ok := s.MapLine("client", "CM_Ring")
		want := Failed
		if skip {
			want = Skipped
		}
		if !ok || l.Status != want || !strings.Contains(l.Detail, "not the untouched file") || s.Failed() == skip {
			t.Errorf("skip %v: CM_Ring %v %v; failed %v; %v", skip, l, ok, s.Failed(), s.Lines())
		}
		if skip && (!strings.HasPrefix(l.Detail, "left out: its build fails: ") || s.result() != nil) {
			t.Errorf("skipped: %v, result %v", l, s.result())
		}
		if l, ok := s.MapLine("client", "cm_bad"); !ok || l.Status != Skipped || !strings.Contains(l.Detail, "rule(s) fail") {
			t.Errorf("CM_Bad: %v %v; %v", l, ok, s.Lines())
		}
		if _, ok := s.MapLine("host", "CM_Ring"); ok {
			t.Error("a line for the host copy, which wasn't set up")
		}
		if _, ok := s.MapLine("client", "CM_None"); ok {
			t.Error("a line for a map that isn't there")
		}
	}
}
