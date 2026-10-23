package dgserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
)

// --map (and its older name --level) and --custom-maps are checked before anything starts: only the playable maps
// pass. A started server logs the map its hosted games load and the maps custom games may pick.
func TestLevelFlag(t *testing.T) {
	for _, args := range [][]string{{"--level", ""}, {"--level", "MM Map"}, {"--level", "levels/MM_Map_v3"},
		{"--map", "TA_Map_v1"}, {"--custom-maps", "MM_Map_v3,TA_Map_v1"}, {"--custom-maps", "MM Map"},
		{"--trust-maps", "yes"}, {"--trust-maps", "10.0.0.0/40"}} {
		if code := Run(append(args, "--print-ports"), Hooks{}); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	for _, args := range [][]string{{"--level", "MM_Map_v3.Level"}, {"--map", "mm"}, {"--map=SM_Map_v5"},
		{"--custom-maps", "MM_Map_v3"}, {"--custom-maps="}, {"--custom-maps", "none"},
		{"--trust-maps", "all"}, {"--trust-maps", "127.0.0.3,192.168.1.0/24"}, {"--trust-maps=none"}} {
		if code := Run(append(args, "--print-ports"), Hooks{}); code != 0 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	logDir := t.TempDir()
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: logDir, level: "MM_Map_v3",
		customLevels: []string{"MM_Map_v3"}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	in.stop()
	files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
	if len(files) == 0 {
		t.Fatal("no log")
	}
	raw, _ := os.ReadFile(files[0])
	for _, want := range []string{`[stub] hosted level: MM_Map_v3 (LevelId 1108206894 = 0x420de52e; --map)`,
		`[stub] custom games may pick the maps MM_Map_v3 (~#mm) by a name tag (--custom-maps)`,
		`[stub] map check: MM_Map_v3 only for Start-Client of this server's release `,
		`through the tunnel, and without the tunnel for nobody (--trust-maps)`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("log lacks %q:\n%s", want, raw)
		}
	}
}

// The map check's start-up line names who may play the maps beside SM_Map_v5, and only when the server offers one.
func TestMapCheckNote(t *testing.T) {
	if n := mapCheckNote(config{}); n != "" {
		t.Errorf("SM_Map_v5 only: %q", n)
	}
	mt, _ := blaze.ParseMapTrust("127.0.0.3")
	n := mapCheckNote(config{customLevels: []string{"MM_Map_v3"}, mapTrust: mt})
	if !strings.Contains(n, "map check: MM_Map_v3 only for Start-Client") || !strings.Contains(n, "without the tunnel for 127.0.0.3 (--trust-maps)") ||
		!strings.Contains(n, "a game it creates plays on SM_Map_v5") {
		t.Errorf("the note %q", n)
	}
	if n := mapCheckNote(config{level: "MM_Map_v3", mapTrust: blaze.MapTrust{All: true}}); !strings.Contains(n, "for every client (--trust-maps)") {
		t.Errorf("all: %q", n)
	}
}

// A map whose archive the local host copy lacks gets a warning; SM_Map_v5 needs none, a copy with the archive none.
func TestLevelArchiveNotes(t *testing.T) {
	host := t.TempDir()
	game := filepath.Join(host, "Dawngate", "game")
	if err := os.MkdirAll(filepath.Join(game, "Data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(game, "Dawngate.exe"), []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := levelArchiveNotes(config{gmsDir: host}); len(n) != 0 {
		t.Errorf("SM_Map_v5: %v", n)
	}
	if n := levelArchiveNotes(config{level: "MM_Map_v3"}); len(n) != 0 {
		t.Errorf("no --gms-dir: %v", n)
	}
	n := levelArchiveNotes(config{gmsDir: host, level: "MM_Map_v3", customLevels: []string{"MM_Map_v3"}}) // one note
	if len(n) != 1 || !strings.Contains(n[0], "WARNING: map MM_Map_v3: the host copy has no zz_MM_Map_v3.snap") {
		t.Errorf("without the archive: %v", n)
	}
	if err := os.WriteFile(filepath.Join(game, "Data", "zz_MM_Map_v3.snap"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := levelArchiveNotes(config{gmsDir: host, level: "MM_Map_v3", customLevels: []string{"MM_Map_v3"}}); len(n) != 0 {
		t.Errorf("with the archive: %v", n)
	}
}
