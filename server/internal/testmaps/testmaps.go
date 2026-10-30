// Package testmaps finds the map packages the tests load: server/testdata/maps, a fixed copy of the test maps
// (CM_Ring, CM_Grove, CM_Dress, CM_Water, CM_Stand, CM_Pass, CM_Marsh, CM_PushA, CM_PushB and CM_PushC, each a
// sub-folder with map.json). The repository's maps/ folder holds no packages (it is where an operator or a player
// copies them), so no test reads it; these fixtures are in git and in CI, but neither installed nor shipped.
package testmaps

import (
	"path/filepath"
	"runtime"
)

// Dir returns the fixture folder (server/testdata/maps), found from this source file, so it is the same from every
// package's test.
func Dir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("testmaps: no caller information")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "maps")
}

// Pkg returns the folder of the fixture package name (e.g. "CM_Ring").
func Pkg(name string) string {
	return filepath.Join(Dir(), name)
}
