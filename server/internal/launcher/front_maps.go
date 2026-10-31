package launcher

import (
	"strings"

	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/tunnel"
)

// writeMapList writes the CREATE GAME dialog's map list for the server of a login's session into the player copy
// (the one the server list goes to, WriteServerList): dialogMaps of what its gateway's facts at the connect say (the
// offered maps, tunnel.FactMaps, and their versions, tunnel.FactMapVersion); a server of a release before the fact
// gets the copy's maps, which it may not offer (it then plays its own map). The game reads the list when the dialog
// opens, so it is written at the switch, before the login reaches the server; in place, since the game has the
// archive mounted (moviepatch.WriteMapList).
func (f *Front) writeMapList(s *frontSession) {
	f.listMu.Lock()
	copy := f.listCopy
	f.listMu.Unlock()
	if copy == "" {
		return
	}
	tags, told := tunnel.FactMapTags(s.facts)
	maps, why := dialogMaps(copy, f.folderMaps(), serverInfo{tags: tags, told: told, versions: tunnel.FactMapVersions(s.facts)})
	names := dialogNames(maps)
	changed, err := moviepatch.WriteMapList(copy, maps)
	switch {
	case err != nil: // e.g. moviepatch.ErrMapListLayout: a copy set up by an older release
		f.logf("the create dialog's maps for %s: not written: %v", s.b.Tag, err)
	case changed:
		f.logf("the create dialog's maps for %s: %s (%s), written into %s", s.b.Tag, names, why, moviepatch.MapListArchive(copy))
	default:
		f.logf("the create dialog's maps for %s: %s (%s), as written before", s.b.Tag, names, why)
	}
}

// mapTags is a list of maps' tags for the log ("none" when empty).
func mapTags(maps []moviepatch.Map) string {
	if len(maps) == 0 {
		return "none"
	}
	var tags []string
	for _, m := range maps {
		tags = append(tags, m.Tag)
	}
	return strings.Join(tags, ", ")
}

// installedMaps are the player copy's installed maps for a session hello (InstalledMaps of the copy the server list
// goes to; nil without one).
func (f *Front) installedMaps() []tunnel.InstalledMap {
	f.listMu.Lock()
	copy := f.listCopy
	f.listMu.Unlock()
	return InstalledMaps(copy, f.folderMaps())
}
