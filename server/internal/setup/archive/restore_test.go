package archive

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestoreFiles(t *testing.T) {
	entries := sample()
	zp := makeZip(t, entries)
	m := manifestOf(entries)
	dest := filepath.Join(t.TempDir(), "copy")
	if _, err := Extract(zp, dest, m, nil); err != nil {
		t.Fatal(err)
	}
	game := filepath.Join(dest, "Dawngate")
	exe := filepath.Join(game, "game", "Dawngate.exe")
	cfg := filepath.Join(game, "game", "Data", "Config.package")
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if bad, _ := CheckCopy(game, m, nil); len(bad) != 2 {
		t.Fatalf("CheckCopy before: %v", bad)
	}
	if err := RestoreFiles(zp, game, m, []string{"game/Dawngate.exe", "game/Data/Config.package"}); err != nil {
		t.Fatal(err)
	}
	if bad, _ := CheckCopy(game, m, nil); len(bad) != 0 {
		t.Fatalf("CheckCopy after: %v", bad)
	}
	if problems, err := VerifyTree(dest, m, true, nil); err != nil || len(problems) != 0 {
		t.Fatalf("VerifyTree after: %v %v", problems, err)
	}
	st, _ := os.Stat(cfg)
	at := when.Add(2 * time.Second)
	want := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), at.Second(), 0, time.Local)
	if !st.ModTime().Equal(want) {
		t.Errorf("restored time %v, want %v", st.ModTime(), want)
	}
	if err := RestoreFiles(zp, game, m, []string{"game/unknown.dll"}); err == nil {
		t.Error("a file outside the manifest was restored")
	}
	// an archive whose file doesn't match the manifest writes nothing
	files := m.Files()
	files[0].SHA256 = sum("something else")
	target := filepath.Join(game, filepath.FromSlash(files[0].CopyPath()))
	before, _ := os.ReadFile(target)
	if err := RestoreFiles(zp, game, NewManifest(files), []string{files[0].CopyPath()}); err == nil {
		t.Error("a mismatching file was restored")
	}
	if _, err := os.Stat(target + ".partial"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a .partial was left: %v", err)
	}
	if after, _ := os.ReadFile(target); string(after) != string(before) {
		t.Error("the file was replaced by a mismatching one")
	}
}
