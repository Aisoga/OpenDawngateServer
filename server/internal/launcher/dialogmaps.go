package launcher

import (
	"encoding/hex"
	"path/filepath"
	"strings"

	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/tunnel"
)

// serverInfo is what a server tells of its maps for the CREATE GAME dialog: through the tunnel the info hello's facts
// (tunnel.FactMaps, tunnel.FactMapVersion), without it dgserver's pages (web.MapsPath, web.MapVersionsPath).
type serverInfo struct {
	tags     []string            // the maps it offers besides The Dawngate
	told     bool                // false: a release before it says which (the copy's maps are listed)
	versions map[string][32]byte // its version of an offered map, by tag (nil: none named, a release before them)
}

// dialogMaps decides the CREATE GAME dialog's maps for a server, the same at a login through the tunnel (the front's
// writeMapList) and without it (WriteServerMapList): the maps besides The Dawngate whose archive the player copy
// (the folder with game\) has (moviepatch.CopyMaps of ExtraMaps and folder), that the server offers, and of those
// that the server names a version of, only the ones whose archive in the copy has that SHA-256 (MAPED-GUARD-dialog:
// the server's map check refuses another version, and the game would play The Dawngate). why says how, for the log.
func dialogMaps(copy string, folder []moviepatch.Map, si serverInfo) (maps []moviepatch.Map, why string) {
	data := filepath.Join(copy, "game", "Data")
	have := moviepatch.CopyMaps(data, folder...)
	maps, why = have, "the server doesn't tell its maps (a release before it): the copy's"
	if si.told {
		maps = moviepatch.ServerMaps(have, si.tags)
		offered := "none besides The Dawngate"
		if len(si.tags) > 0 {
			offered = strings.Join(si.tags, ", ")
		}
		why = "the server offers " + offered + "; the copy has " + mapTags(have)
	}
	var kept []moviepatch.Map
	var other []string
	for _, m := range maps {
		want, ok := si.versions[strings.ToLower(m.Tag)]
		if !ok {
			kept = append(kept, m)
			continue
		}
		got := tunnel.ReadInstalledMaps(data, m.Archive)
		if len(got) == 1 && got[0].SHA256 == want {
			kept = append(kept, m)
			continue
		}
		yours := "unreadable"
		if len(got) == 1 {
			yours = got[0].Short()
		}
		other = append(other, m.Tag+" (the copy's "+yours+", the server's "+hex.EncodeToString(want[:4])+")")
	}
	if len(other) > 0 {
		why += "; left out, another version than the server's: " + strings.Join(other, ", ")
	}
	return kept, why
}

// dialogNames are the dialog's entries for maps, for the log ("The Dawngate, Anshavar Pass").
func dialogNames(maps []moviepatch.Map) string {
	names := "The Dawngate"
	for _, m := range maps {
		names += ", " + m.Label
	}
	return names
}
