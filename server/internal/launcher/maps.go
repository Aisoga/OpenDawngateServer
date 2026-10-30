package launcher

import (
	"os"
	"path/filepath"

	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/tunnel"
)

// MapsDir is the maps folder beside the programs (exeDir\maps): a player or an operator copies a map package (a
// folder with map.json) into it. The setup compiles every package there into the player copy and the match-host copy
// (setup.Setup.Maps), and the server may host them (dgserver --maps; Start-Server.ini map and custom_maps name them).
func MapsDir(exeDir string) string { return filepath.Join(exeDir, mapc.FolderName) }

// MapsFlags are dgserver's --maps for the maps folder beside the programs, when there is one.
func MapsFlags(exeDir string) []string {
	dir := MapsDir(exeDir)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil
	}
	return []string{"--maps=" + dir}
}

// folderDialogMaps are the create dialog's entries for the maps folder's packages (listed when the copy has their
// archives, moviepatch.CopyMaps).
func folderDialogMaps(exeDir string) []moviepatch.Map {
	pkgs, _ := mapc.ReadFolder(MapsDir(exeDir))
	return setup.DialogMaps(pkgs)
}

// InstalledMaps are the map archives installed in the player copy (the folder with game\Data) with their SHA-256:
// MM_Map_v3's and those of the maps folder's packages (folder: their create-dialog entries) that it has. The tunnel's
// session hello names them, and a server lets the player into a game on a map of its maps folder only with its own
// version (MAPED-GUARD). nil without a copy (the hello then names no list, and the server goes by the release).
func InstalledMaps(copy string, folder []moviepatch.Map) []tunnel.InstalledMap {
	if copy == "" {
		return nil
	}
	var archives []string
	for _, m := range append(append([]moviepatch.Map(nil), moviepatch.ExtraMaps...), folder...) {
		archives = append(archives, m.Archive)
	}
	return tunnel.ReadInstalledMaps(filepath.Join(copy, "game", "Data"), archives...)
}
