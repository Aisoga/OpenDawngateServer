package dgserver

import (
	"fmt"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/tunnel"
)

// FolderLevel is a map package's playable level: its name, tag, the browser's prefix, its display name and its
// archive, as the package's map.json names them (mapc.Label).
func FolderLevel(l mapc.Label) blaze.PlayableLevel {
	return blaze.PlayableLevel{Name: l.Name, Tag: l.Tag, Label: l.Prefix, Archive: l.Archive, Display: l.Display}
}

// mmArchive is MM_Map_v3's archive (a player copy's installed maps name it too).
func mmArchive() string {
	l, _ := blaze.FindLevel("MM_Map_v3")
	return l.Archive
}

// describeClientMaps words --client-maps for the start-up line: "127.0.0.4: zz_CM_Ring.snap 85d808c2; 127.0.4.2: none".
func describeClientMaps(m map[netip.Addr][]tunnel.InstalledMap) string {
	addrs := make([]netip.Addr, 0, len(m))
	for a := range m {
		addrs = append(addrs, a)
	}
	slices.SortFunc(addrs, func(a, b netip.Addr) int { return a.Compare(b) })
	var out []string
	for _, a := range addrs {
		out = append(out, a.String()+": "+tunnel.MapsWords(m[a]))
	}
	return strings.Join(out, "; ")
}

// folderArchives are the archives of the maps folder dir's packages (none without a folder).
func folderArchives(dir string) []string {
	if dir == "" {
		return nil
	}
	pkgs, _ := mapc.ReadFolder(dir)
	var out []string
	for _, p := range pkgs {
		out = append(out, p.Label.Archive)
	}
	return out
}

// mapVersions are the server's own versions of the maps folder's maps (blaze.Server.MapVersions, MAPED-GUARD): each
// package's archive in the host copy gmsDir (--gms-dir), hashed. The setup compiles every package into that copy
// and the players' copies alike, and the compiler is deterministic, so a player's copy has the server's version when
// its archive has the same hash. A map whose archive the host copy lacks (or no host copy: the match hosts run
// elsewhere) has no version, and the map check keeps MAPGUARD's release rule for it. notes are the start-up lines.
func mapVersions(mapsDir, gmsDir string) (map[string][32]byte, []string) {
	archives := folderArchives(mapsDir)
	if len(archives) == 0 {
		return nil, nil
	}
	if gmsDir == "" {
		return nil, []string{fmt.Sprintf("map check: the maps folder's maps by Start-Client's release only, not by "+
			"version: no host copy (--gms-dir) to take their versions from (%s)", strings.Join(archives, ", "))}
	}
	exe, err := gmshost.ResolveExe(gmsDir)
	if err != nil {
		return nil, []string{fmt.Sprintf("map check: the maps folder's maps by Start-Client's release only: %v", err)}
	}
	data := filepath.Join(filepath.Dir(exe), "Data")
	versions := map[string][32]byte{}
	var known, unknown []string
	for _, m := range tunnel.ReadInstalledMaps(data, archives...) {
		versions[strings.ToLower(m.Archive)] = m.SHA256
		known = append(known, m.Archive+" "+m.Short())
	}
	for _, a := range archives {
		if _, ok := versions[strings.ToLower(a)]; !ok {
			unknown = append(unknown, a)
		}
	}
	var notes []string
	if len(known) > 0 {
		notes = append(notes, fmt.Sprintf("map check by version: %s (the host copy's; a player's copy needs the same "+
			"archive, named by its Start-Client through the tunnel or by --client-maps without it)", strings.Join(known, ", ")))
	}
	if len(unknown) > 0 {
		notes = append(notes, fmt.Sprintf("map check: no version of %s (the host copy %s lacks it), so by Start-Client's "+
			"release only", strings.Join(unknown, ", "), data))
	}
	return versions, notes
}

// offeredVersions are the server's versions of the maps it offers besides The Dawngate (blaze.OfferedLevels of --map
// and --custom-maps), by tag: those whose archive has a version (mapVersions). The info hello names them
// (tunnel.MapVersionFacts) and the HTTP port answers them (web.MapVersionsPath), so Start-Client's create dialog
// leaves out a map the player's copy has in another version (MAPED-GUARD-dialog).
func offeredVersions(level string, custom []string, versions map[string][32]byte) map[string][32]byte {
	out := map[string][32]byte{}
	for _, l := range blaze.OfferedLevels(level, custom) {
		if sum, ok := versions[strings.ToLower(l.Archive)]; ok && l.Archive != "" {
			out[strings.ToLower(l.Tag)] = sum
		}
	}
	return out
}

// addFolderMaps makes the packages of the maps folder dir (--maps) playable levels (blaze.AddLevels) and returns the
// start-up lines: the maps it offers and the packages it left out (a broken map.json, a name or tag taken; they are
// left out, not fatal, so one bad package doesn't stop the server). A level already added the same way (a second run
// in one process) is kept. An error only for a level whose name or tag a built-in map has.
func addFolderMaps(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	pkgs, errs := mapc.ReadFolder(dir)
	var notes []string
	for _, err := range errs {
		notes = append(notes, fmt.Sprintf("WARNING: maps folder %s: %v (left out)", dir, err))
	}
	var add []blaze.PlayableLevel
	var names []string
	for _, p := range pkgs {
		l := FolderLevel(p.Label)
		names = append(names, fmt.Sprintf("%s (%s%s, %q)", l.Name, blaze.MapTagPrefix, l.Tag, l.Display))
		if have, ok := blaze.FindLevel(l.Name); ok && have == l {
			continue
		}
		add = append(add, l)
	}
	if err := blaze.AddLevels(add...); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return append(notes, fmt.Sprintf("maps folder %s: no map packages", dir)), nil
	}
	return append(notes, fmt.Sprintf("maps folder %s: %s; --map and --custom-maps may name them (every player's copy "+
		"and the host copy need their archives)", dir, strings.Join(names, ", "))), nil
}
