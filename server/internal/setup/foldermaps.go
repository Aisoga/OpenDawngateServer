package setup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"dawngate/server/internal/setup/botprofiles"
	"dawngate/server/internal/setup/localepatch"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/moviepatch"
)

// folderMaps is what the setup read from its maps folder (Setup.Maps), once: the packages that pass every rule check,
// the problems of those left out, and the archives built so far (a second copy reuses the first one's bytes; the
// inputs are the build's either way).
type folderMaps struct {
	pkgs   []mapc.Package
	errs   []error
	any    bool // the folder holds a package (a sub-folder with map.json), whether it passes or not
	built  map[string][]byte
	loaded bool
}

// mapsRead reads Setup.Maps: the packages (mapc.ReadFolder), each checked with every rule (mapc.Check) against the
// others, so a package that can't be built is left out of the archives, the catalog, the dialog and the lobby line.
func (s *Setup) mapsRead() *folderMaps {
	if s.maps.loaded {
		return &s.maps
	}
	s.maps.loaded = true
	s.maps.built = map[string][]byte{}
	pkgs, errs := mapc.ReadFolder(s.Maps)
	s.maps.any = len(pkgs)+len(errs) > 0
	s.maps.errs = errs
	for i, p := range pkgs {
		if r, _ := mapc.Check(p.Map, mapc.Others(pkgs, i)); !r.OK {
			first := ""
			for _, f := range r.Findings {
				if f.Severity == mapc.Error {
					first = f.Rule + ": " + f.Message
					break
				}
			}
			s.maps.errs = append(s.maps.errs, fmt.Errorf("map %s (%s) fails a rule check: %s (mapc check)", p.Map.Name,
				p.Dir, first))
			continue
		}
		s.maps.pkgs = append(s.maps.pkgs, p)
	}
	return &s.maps
}

// FolderMaps are the maps folder's packages the setup installs (Setup.Maps; none without one).
func (s *Setup) FolderMaps() []mapc.Package { return s.mapsRead().pkgs }

// mapCatalogs are the catalog entries the maps folder's maps add to the copy's one catalog (zz_BotProfiles.snap).
func (s *Setup) mapCatalogs() []botprofiles.CatalogSource {
	var out []botprofiles.CatalogSource
	for _, p := range s.mapsRead().pkgs {
		out = append(out, mapc.Catalog(p.Map))
	}
	return out
}

// DialogMaps are the create dialog's entries for the maps folder's maps (listed when their archive is in the copy,
// moviepatch.CopyMaps).
func DialogMaps(pkgs []mapc.Package) []moviepatch.Map {
	var out []moviepatch.Map
	for _, p := range pkgs {
		out = append(out, moviepatch.Map{Tag: p.Label.Tag, Archive: p.Label.Archive, Label: p.Label.Display})
	}
	return out
}

// lobbyMap is the custom lobby's map line for a copy set up with the maps folder: with a package in it the copy has
// more maps than MM_Map_v3, and the line names none (localepatch.LobbyMapAny); the developers' Python setup decides
// the same way (a sub-folder with map.json, valid or not).
func (s *Setup) lobbyMap() string {
	if s.mapsRead().any {
		return localepatch.LobbyMapAny
	}
	return ""
}

// lobbyMaps are the maps folder's installed maps for the copy's locale package: each gets its line in
// FE_CustomGameLobby, by which the patched client names it in the custom lobby and the game browser (exepatch's
// maptable). The developers' Python setup takes every package whose map.json reads (patch_locale.py's folder_maps), so
// only a package that fails a rule differs (a line nobody asks for).
func (s *Setup) lobbyMaps() []localepatch.Map {
	var out []localepatch.Map
	for _, p := range s.mapsRead().pkgs {
		out = append(out, localepatch.Map{Name: p.Map.Name, Display: p.Map.Display})
	}
	return out
}

// stepFolderMaps installs the archive game\Data\zz_<name>.snap of every map in the maps folder (Setup.Maps): the map's
// level files and assets compiled from the package and the copy's own game files (mapc.Build), which must be the
// build's. Every player copy and the match-host copy get them, as MM_Map_v3's: a server whose --maps names the same
// folder may host them. Current means byte for byte what mapc builds now (a build takes about a second). A package
// left out (it doesn't read or fails a rule) gets a SKIPPED line saying why.
func (s *Setup) stepFolderMaps(where, copy string) {
	fm := s.mapsRead()
	for _, err := range fm.errs {
		s.report(Skipped, where+" maps", "left out: "+err.Error())
	}
	for i, p := range fm.pkgs {
		s.stepFolderMap(fmt.Sprintf("%s %s", where, p.Map.Name), copy, i)
	}
}

func (s *Setup) stepFolderMap(step, copy string, i int) bool {
	p := s.maps.pkgs[i]
	added := filepath.Join(copy, "game", "Data", p.Label.Archive)
	for _, rel := range mapc.Sources {
		if s.untouched(rel) == "" {
			return s.report(Skipped, step, "this build has no "+rel)
		}
	}
	want, err := s.buildFolderMap(copy, i)
	if err != nil {
		if s.Check {
			return s.report(Missing, step, err.Error())
		}
		return s.report(Failed, step, err.Error())
	}
	line := fmt.Sprintf("%s: the map %s, %q (~#%s) from %s (%s)", s.show(added), p.Map.Name, p.Label.Display,
		p.Label.Tag, s.show(p.Dir), digest(want)[:16])
	have, haveErr := os.ReadFile(added)
	if haveErr == nil && bytes.Equal(have, want) {
		return s.report(OK, step, line)
	}
	if s.Check {
		return s.mapMMStale(step, added, haveErr)
	}
	tmp := added + ".partial"
	err = os.WriteFile(tmp, want, 0o644)
	if err == nil {
		err = os.Rename(tmp, added)
	}
	if err != nil {
		os.Remove(tmp)
		return s.report(Failed, step, fmt.Sprintf("%s: %v (is the game running from this copy?)", s.show(added), err))
	}
	why := "an older one"
	if haveErr != nil {
		why = "not there"
	}
	return s.report(Done, step, fmt.Sprintf("%s (was: %s)", line, why))
}

// buildFolderMap reads the copy's inputs (each checked against the manifest) and builds package i's archive.
func (s *Setup) buildFolderMap(copy string, i int) ([]byte, error) {
	p := s.maps.pkgs[i]
	if b, ok := s.maps.built[p.Map.Name]; ok {
		return b, nil
	}
	var bs [][]byte
	for _, rel := range mapc.Sources {
		path := filepath.Join(copy, filepath.FromSlash(rel))
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s (the copy is incomplete)", s.show(path))
		}
		if digest(b) != s.untouched(rel) {
			return nil, fmt.Errorf("%s is not the untouched file of the build (docs/file_manifest.csv); delete %s "+
				"and start again (it is extracted anew)", s.show(path), copy)
		}
		bs = append(bs, b)
	}
	in := mapc.Inputs{SMMap: bs[0], Anshavar3: bs[1], Anshavar4: bs[2], AssetData: bs[3]}
	// a baked ground may name a shipped texture of another archive: read it, checked like the inputs
	in.Archive = func(name string) ([]byte, error) {
		rel := "game/Data/" + name
		if s.untouched(rel) == "" {
			return nil, fmt.Errorf("this build has no %s", rel)
		}
		path := filepath.Join(copy, filepath.FromSlash(rel))
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s (the copy is incomplete)", s.show(path))
		}
		if digest(b) != s.untouched(rel) {
			return nil, fmt.Errorf("%s is not the untouched file of the build (docs/file_manifest.csv)", s.show(path))
		}
		return b, nil
	}
	b, _, err := mapc.Build(p.Map, in, mapc.SharedMesh{}, mapc.Others(s.maps.pkgs, i))
	if err != nil {
		return nil, err
	}
	s.maps.built[p.Map.Name] = b
	return b, nil
}

// MapSteps runs the steps of the maps on one copy (the developers' setups call it through server/tools/mapsetup): the
// MM_Map_v3 archive, the maps folder's archives, the bot profiles with the maps' catalog entries and, for a player
// copy (client), the create dialog's map list. nil when everything is current or was written, ErrIncomplete
// otherwise (Bad says why).
func (s *Setup) MapSteps(copy string, client bool) error {
	where := "host"
	if client {
		where = "client"
	}
	s.stepMapMM(where+" MM_Map_v3", copy)
	s.stepFolderMaps(where, copy)
	s.stepBotProfiles(where+" bots", copy)
	if client {
		s.stepMatchOptions("bot check box", copy)
	}
	return s.result()
}
