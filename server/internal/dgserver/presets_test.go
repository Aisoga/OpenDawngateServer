package dgserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/setup/mapc"
)

// RULES6: FolderLevel carries the map's suggested lobby setup; the start-up line names it.
func TestFolderLevelLobbyPreset(t *testing.T) {
	dir := t.TempDir()
	tunedPackage(t, dir, "", 0)
	path := filepath.Join(dir, "CM_Tuned", "map.json")
	src, _ := os.ReadFile(path)
	text := strings.Replace(string(src), `"rules": {`, `"rules": {"lobbyPreset": "pick=random,victory=bind1", `, 1)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs, errs := mapc.ReadFolder(dir)
	if len(pkgs) != 1 || len(errs) != 0 {
		t.Fatalf("%d packages, %v", len(pkgs), errs)
	}
	if l := FolderLevel(pkgs[0]); l.LobbyPreset != "pick=random,victory=bind1" || l.MapRules.Victory() {
		t.Errorf("FolderLevel %+v", l)
	}
	orig := blaze.PlayableLevels
	t.Cleanup(func() { blaze.PlayableLevels = orig })
	notes, err := addFolderMaps(dir)
	if err != nil || !strings.Contains(strings.Join(notes, "\n"), `suggests the lobby setup "pick=random,victory=bind1"`) {
		t.Errorf("notes %q (%v)", notes, err)
	}
	if n := suggestionNotes(nil); len(n) != 0 {
		t.Errorf("a usable suggestion: %q", n)
	}
	blaze.PlayableLevels[len(blaze.PlayableLevels)-1].LobbyPreset = "brawl"
	if n := suggestionNotes(nil); len(n) != 1 || !strings.Contains(n[0], `map CM_Tuned suggests the lobby setup "brawl", which this server can't use (unknown preset "brawl")`) {
		t.Errorf("an unknown preset: %q", n)
	}
}

// The presets file: a relative name in the data folder, its bad lines and a missing file as start-up lines, the
// default checked against the built-in and the operator's ids (case-insensitive, the preset's own id kept).
func TestLoadRulePresets(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "presets.txt"),
		[]byte("brawl | Bind Brawl | pick=random,victory=bind1\naram | Mine | pick=random\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, def, notes := loadRulePresets("presets.txt", data, "BRAWL")
	all := strings.Join(notes, "\n")
	if len(lib.Own()) != 1 || def != "brawl" || !strings.Contains(all, `line 2: aram: the id is a built-in preset's (left out)`) ||
		!strings.Contains(all, `: brawl "Bind Brawl" (All Random, First Binding)`) ||
		!strings.Contains(all, `custom games start their lobby with the preset brawl "Bind Brawl" (All Random, First Binding)`) {
		t.Errorf("presets %+v, default %q, notes\n%s", lib.Own(), def, all)
	}
	if _, def, notes := loadRulePresets("", data, "draft"); def != "draft" || len(notes) != 1 {
		t.Errorf("a built-in default: %q %q", def, notes)
	}
	lib, def, notes = loadRulePresets("missing.txt", data, "brawl")
	if lib == nil || def != "" || len(notes) != 2 || !strings.Contains(notes[0], "only the built-in presets") ||
		!strings.Contains(notes[1], `--default-rules "brawl": no preset of that id`) {
		t.Errorf("a missing file: %v %q %q", lib, def, notes)
	}
	if got := presetsPath(`C:\x\p.txt`, data); filepath.IsAbs(`C:\x\p.txt`) && got != `C:\x\p.txt` {
		t.Errorf("an absolute path: %q", got)
	}
	if code := Run([]string{"--rule-presets=presets.txt", "--default-rules=brawl", "--print-ports"}, Hooks{}); code != 0 {
		t.Errorf("the flags: exit %d", code)
	}
}
