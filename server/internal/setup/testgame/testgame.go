// Package testgame finds the game's files for the tests that compare the setup ports with the Python
// pipeline. The folder is $DAWNGATE_GAME (the folder that holds game\Data,
// e.g. client\ia_Dawngate\Dawngate), else client\ia_Dawngate\Dawngate in this checkout or in a folder
// above it, so a git worktree finds the main checkout's. Tests that need it skip
// without it. The files are only read: they are the pristine extraction (never written).
package testgame

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Env names the variable that points at the game folder.
const Env = "DAWNGATE_GAME"

// Find returns the game folder (the one with game\Data), or "" when there is none.
func Find() string {
	if d := os.Getenv(Env); d != "" {
		if isGame(d) {
			return d
		}
		return ""
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	for dir := filepath.Dir(file); ; {
		cand := filepath.Join(dir, "client", "ia_Dawngate", "Dawngate")
		if isGame(cand) {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isGame(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "game", "Data", "AssetData.snap"))
	return err == nil && !st.IsDir()
}

// Dir returns the game folder, or skips the test.
func Dir(t testing.TB) string {
	t.Helper()
	d := Find()
	if d == "" {
		t.Skipf("no game files (set %s to the folder with game\\Data, e.g. client\\ia_Dawngate\\Dawngate)", Env)
	}
	return d
}

// Data returns a path under the game's game\Data folder, or skips the test.
func Data(t testing.TB, rel ...string) string {
	t.Helper()
	return filepath.Join(append([]string{Dir(t), "game", "Data"}, rel...)...)
}

// Read reads a file under game\Data, or skips the test.
func Read(t testing.TB, rel ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(Data(t, rel...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
