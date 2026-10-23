package moviepatch

// The create dialog's map list per server: the setup writes the copy's maps (CopyMaps) into zz_MatchOptions.snap,
// and Start-Client's front writes, at each login's switch to a server, the maps of those that the server offers
// (ServerMaps of the tunnel's FactMaps) into the same entry in place (WriteMapList). The game reads every archive's
// index once, when it mounts them at its start, and an entry's bytes from the file whenever a movie loads it; the
// dialog's movie is made anew for every CREATE GAME and its class reads the list at its construction, so the list
// the last login wrote is the one the dialog shows. An archive written again with another layout while the game runs
// would fail the load (FileDepot.cpp 138), hence the fixed size and the literals-only block (MapList).
//
// re/scripts/patch_login_movie.py has the same functions (server_maps, write_map_list, map_list_only).

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"dawngate/server/internal/setup/snap"
)

// MapListSize is the map list's fixed size in its archive (MapList pads the text with line ends).
const MapListSize = 1 << 10

// ErrMapListLayout says the copy's zz_MatchOptions.snap has no map list of the fixed layout (a setup of a release
// before it; the next setup writes it).
var ErrMapListLayout = errors.New("no map list of the fixed layout (an older setup; the next start's setup writes it)")

// ServerMaps are the maps of have (the copy's, CopyMaps) that a server offers: offered holds its tags
// (tunnel.FactMapTags). A map the copy lacks is never listed (the server keeps such a player out of its games).
func ServerMaps(have []Map, offered []string) []Map {
	var out []Map
	for _, m := range have {
		for _, t := range offered {
			if t == m.Tag {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// MapListArchive is the path of the copy's archive that holds the map list (the copy holds game\Data).
func MapListArchive(copy string) string {
	return filepath.Join(copy, filepath.FromSlash(MatchOptions.Rel))
}

// mapListEntry finds the map list's entry in an archive and checks its layout: a literals-only block of MapListSize.
func mapListEntry(a *snap.Archive) (snap.Entry, error) {
	want := len(snap.EncodeLiterals(make([]byte, MapListSize)))
	for _, e := range a.Entries {
		if e.Name == MapListPath {
			if e.Stored() || e.Size != MapListSize || int(e.StoredSize) != want ||
				int(e.Offset)+want > len(a.Data) {
				return e, ErrMapListLayout
			}
			return e, nil
		}
	}
	return snap.Entry{}, ErrMapListLayout
}

// WriteMapList writes the list of maps into the copy's zz_MatchOptions.snap in place (the game may have it mounted):
// only the entry's bytes, the archive's layout unchanged. It reports whether the bytes changed; ErrMapListLayout (or a
// file error) leaves the file as it was.
func WriteMapList(copy string, maps []Map) (bool, error) {
	path := MapListArchive(copy)
	a, err := snap.Open(path)
	if err != nil {
		return false, err
	}
	e, err := mapListEntry(a)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	payload := snap.EncodeLiterals(MapList(maps))
	if bytes.Equal(a.Raw(e), payload) {
		return false, nil
	}
	w, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false, err
	}
	_, err = w.WriteAt(payload, int64(e.Offset))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err == nil, err
}

// MapListOnly reports whether the archive have differs from want only in the map list's text (both of the fixed
// layout, have's list whole): a list Start-Client's front wrote for the server a login went to, which the setup
// counts as current.
func MapListOnly(have, want []byte) bool {
	if len(have) != len(want) || bytes.Equal(have, want) {
		return false
	}
	aw, err := snap.Parse(want)
	if err != nil {
		return false
	}
	ew, err := mapListEntry(aw)
	if err != nil {
		return false
	}
	end := int(ew.Offset) + int(ew.StoredSize)
	if !bytes.Equal(have[:ew.Offset], want[:ew.Offset]) || !bytes.Equal(have[end:], want[end:]) {
		return false
	}
	ah, err := snap.Parse(have)
	if err != nil {
		return false
	}
	eh, err := mapListEntry(ah)
	if err != nil || eh.Offset != ew.Offset {
		return false
	}
	text, err := ah.Read(eh)
	return err == nil && bytes.HasPrefix(text, []byte("dawngate-maps\t1\n")) && bytes.Contains(text, []byte("\nend\n"))
}
