package mapctool

// The package scene from the compiled Level: `mapc scene` of a package lists the markers `mapc build` writes into the
// map's archive (its Level's markersets, compiled from the copy's AssetData.snap), so a viewer shows what the game
// places, not a template level's dressing.

import (
	"os"
	"path/filepath"

	"dawngate/server/internal/setup/depot"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/mapc/bake"
	"dawngate/server/internal/setup/scene"
)

// compiledScene compiles the package's assets against the copy (as `mapc build` does) and lists its Level's markers.
func compiledScene(as *scene.Assets, cm *mapc.Compiled, d *depot.Depot, copyDir string) (*scene.Scene, error) {
	model, _, err := scene.GroundFiles(cm, d)
	if err != nil {
		return nil, err
	}
	info, err := bake.CheckModel(model)
	if err != nil {
		return nil, err
	}
	ad, err := os.ReadFile(filepath.Join(copyDir, filepath.FromSlash(mapc.AssetDataSource)))
	if err != nil {
		return nil, err
	}
	ma, err := cm.Assets(ad, info.Bounds)
	if err != nil {
		return nil, err
	}
	return as.CompiledScene(cm, ma)
}
