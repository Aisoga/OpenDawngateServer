package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/mapmm"
	"dawngate/server/internal/setup/testgame"
)

// TestMapMMSkipped: a build without MM_Map_v3's line-of-sight map (the synthetic one) skips the step.
func TestMapMMSkipped(t *testing.T) {
	s := &Setup{Out: &bytes.Buffer{}, Manifest: archive.NewManifest(nil)}
	if err := s.MapMM("MM_Map_v3", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if l := s.Lines(); len(l) != 1 || l[0].Status != Skipped {
		t.Errorf("lines %v", l)
	}
}

// TestMapMMStep runs the step on a copy holding the game's own input files, with a manifest of those files (so
// "current" means byte for byte what mapmm.Build makes): missing, done, current, stale, --check, one build for two
// copies, a changed input.
func TestMapMMStep(t *testing.T) {
	copyDir := t.TempDir()
	var files []archive.File
	for _, rel := range mapmm.Sources {
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
	run := func(check bool) (*Setup, error) {
		s := &Setup{Out: &bytes.Buffer{}, Manifest: m, Check: check}
		return s, s.MapMM("MM_Map_v3", copyDir)
	}
	status := func(s *Setup) Status { return s.Lines()[0].Status }
	added := filepath.Join(copyDir, filepath.FromSlash(mapmm.Rel))

	if s, err := run(true); err == nil || status(s) != Missing {
		t.Fatalf("--check on a new copy: %v %v", err, s.Lines())
	}
	if _, err := os.Stat(added); err == nil {
		t.Fatal("--check wrote the archive")
	}
	if s, err := run(false); err != nil || status(s) != Done {
		t.Fatalf("first run: %v %v", err, s.Lines())
	}
	// the game's own inputs make the reference archive (the shipped mesh in it)
	if b, err := os.ReadFile(added); err != nil || digest(b) != mapmm.Game854SHA256 {
		t.Fatalf("the archive is not the reference %s: %v", mapmm.Game854SHA256[:16], err)
	}
	if s, err := run(false); err != nil || status(s) != OK {
		t.Fatalf("second run: %v %v", err, s.Lines())
	}
	if err := os.WriteFile(added, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := run(true); err == nil || status(s) != Stale {
		t.Fatalf("--check on an old archive: %v %v", err, s.Lines())
	}
	if s, err := run(false); err != nil || status(s) != Done || !strings.Contains(s.Lines()[0].Detail, "an older one") {
		t.Fatalf("rerun on an old archive: %v %v", err, s.Lines())
	}
	// a second copy in the same setup reuses the archive (one build)
	s := &Setup{Out: &bytes.Buffer{}, Manifest: m}
	s.stepMapMM("a", copyDir)
	first := s.mapMM
	s.stepMapMM("b", copyDir)
	if len(first) == 0 || &s.mapMM[0] != &first[0] {
		t.Error("the second copy of one setup built the archive again")
	}
	// an input that isn't the build's
	img := filepath.Join(copyDir, filepath.FromSlash(mapmm.Sources[0]))
	orig, _ := os.ReadFile(img)
	if err := os.WriteFile(img, append(orig, 0), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := run(false); err == nil || status(s) != Failed || !strings.Contains(s.Lines()[0].Detail, "not the untouched") {
		t.Errorf("a changed images.snap: %v %v", err, s.Lines())
	}
	if err := os.WriteFile(img, orig, 0o644); err != nil {
		t.Fatal(err)
	}
}
