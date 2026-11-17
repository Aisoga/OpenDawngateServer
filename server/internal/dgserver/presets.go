package dgserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/rules"
)

// The operator's lobby presets and the default lobby start (RULES6; blaze/lobbypreset.go uses them).
//
// --rule-presets names a file of the operator's presets (Start-Server.ini's rule_presets), a bare or relative name
// taken in the data folder; --default-rules a preset's id (built-in or the operator's) every custom game's lobby starts
// with on a map that suggests none. Neither stops the server: a missing or unreadable file, a bad line (rules.
// ParsePresets: also ids of the built-in presets and repeated combinations) and an unknown default are start-up lines,
// and the server goes on without them.

// presetsPath is the presets file's path: a relative name is the data folder's ("" stays "").
func presetsPath(name, dataDir string) string {
	if name == "" || filepath.IsAbs(name) || dataDir == "" {
		return name
	}
	return filepath.Join(dataDir, name)
}

// loadRulePresets reads the operator's presets and checks the default: the Library (never nil), the default id to use
// ("" = none, the standard game) and the start-up lines.
func loadRulePresets(file, dataDir, defaultID string) (*rules.Library, string, []string) {
	var notes []string
	lib := &rules.Library{}
	if path := presetsPath(file, dataDir); path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err != nil:
			notes = append(notes, fmt.Sprintf("WARNING: lobby presets (--rule-presets): %v; only the built-in presets", err))
		default:
			var problems []string
			lib, problems = rules.ParsePresets(data)
			for _, p := range problems {
				notes = append(notes, fmt.Sprintf("WARNING: lobby presets %s: %s (left out)", path, p))
			}
			var names []string
			for _, p := range lib.Own() {
				names = append(names, fmt.Sprintf("%s %q (%s)", p.ID, p.Name, rules.Describe(p.Options)))
			}
			if len(names) == 0 {
				names = []string{"none"}
			}
			notes = append(notes, fmt.Sprintf("lobby presets %s: %s", path, strings.Join(names, ", ")))
		}
	}
	if defaultID != "" {
		p, ok := lib.Find(defaultID)
		switch {
		case !ok:
			notes = append(notes, fmt.Sprintf("WARNING: --default-rules %q: no preset of that id (the built-in ones, "+
				"those of --rule-presets); custom games start on Standard", defaultID))
			defaultID = ""
		default:
			notes = append(notes, fmt.Sprintf("custom games start their lobby with the preset %s %q (%s) on a map that "+
				"suggests no setup (--default-rules); the admin may change it", p.ID, p.Name, rules.Describe(p.Options)))
			defaultID = p.ID
		}
	}
	return lib, defaultID, notes
}

// suggestionNotes are the start-up lines about the maps' suggested lobby setups: a map whose suggestion this server
// can't use (no preset of the id, rules that don't parse) is named.
func suggestionNotes(lib *rules.Library) []string {
	var notes []string
	for _, l := range blaze.PlayableLevels {
		if l.LobbyPreset == "" {
			continue
		}
		if _, err := lib.Resolve(l.LobbyPreset); err != nil {
			notes = append(notes, fmt.Sprintf("WARNING: map %s suggests the lobby setup %q, which this server can't "+
				"use (%v): its custom games start as the others", l.Name, l.LobbyPreset,
				strings.TrimPrefix(err.Error(), "rules: ")))
		}
	}
	return notes
}
