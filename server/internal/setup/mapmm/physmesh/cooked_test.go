package physmesh

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/setup/mapmm/fogmap/synthlos"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/testgame"
)

// PhysCookEnv names the variable that points at physcook.exe (else gms\build\Release\physcook.exe in this
// checkout).
const PhysCookEnv = "DAWNGATE_PHYSCOOK"

// TestCooked checks the shipped mesh: the pins, its size, its header ("NXS", 1, "MESH", version 1, serial flags 0x13:
// materials, remap, u16 indices) and that a caller's change to its copy doesn't reach the next one.
func TestCooked(t *testing.T) {
	if CookedSHA256 != gameBin || SourceDGMISHA256 != gameDGMI {
		t.Fatalf("the exported pins differ from the reference's")
	}
	b, err := Cooked()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 149872 || sum(b) != gameBin {
		t.Errorf("%d bytes, sha256 %s", len(b), sum(b))
	}
	le := binary.LittleEndian
	if string(b[:8]) != CookedMagic || le.Uint32(b[8:]) != 1 || le.Uint32(b[12:]) != 0x13 {
		t.Errorf("header % x", b[:16])
	}
	b[0] = 'x'
	if again, err := Cooked(); err != nil || again[0] != 'N' {
		t.Errorf("the shipped bytes changed through a copy: %v", err)
	}
}

// TestForMapGame: the game's own map gives the shipped mesh.
func TestForMapGame(t *testing.T) {
	b, err := ForMap(gameMap(t), los.Size, los.Size)
	if err != nil {
		t.Fatal(err)
	}
	if got := sum(b); got != gameBin {
		t.Errorf("sha256 %s, the reference %s", got, gameBin)
	}
}

// TestForMapOther: the shipped mesh isn't given for another map.
func TestForMapOther(t *testing.T) {
	if _, err := ForMap(synthlos.Map(77, 45), 81.3, 47.9); err == nil || !strings.Contains(err.Error(), "not the ones") {
		t.Errorf("another map: %v", err)
	}
}

func physcook() string {
	if p := os.Getenv(PhysCookEnv); p != "" {
		return p
	}
	_, file, _, _ := runtime.Caller(0) // server/internal/setup/mapmm/physmesh
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..", "gms", "build", "Release", "physcook.exe")
}

// TestCookGame (developers): physcook.exe over a copy of the game's PhysXCooking.dll cooks the game map's cooking
// input into exactly the shipped mesh, so the shipped file can be made again (gms/physcook; skipped without it).
func TestCookGame(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("physcook.exe is a Windows program")
	}
	exe := physcook()
	if _, err := os.Stat(exe); err != nil {
		t.Skipf("no physcook.exe (%s; build gms or set %s)", exe, PhysCookEnv)
	}
	dll, err := os.ReadFile(filepath.Join(testgame.Dir(t), "game", "PhysXCooking.dll"))
	if err != nil {
		t.Skip(err)
	}
	m, err := Build(gameMap(t), los.Size, los.Size)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dllCopy := filepath.Join(dir, "PhysXCooking.dll")
	in, out := filepath.Join(dir, "mesh.dgmi"), filepath.Join(dir, "mesh.bin")
	if err := os.WriteFile(dllCopy, dll, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, m.DGMI(), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if o, err := exec.Command(exe, dllCopy, in, out).CombinedOutput(); err != nil {
		t.Fatalf("physcook: %v: %s", err, o)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cooked %d bytes in %v", len(b), time.Since(start))
	if got := sum(b); got != gameBin {
		t.Errorf("sha256 %s, the shipped mesh's %s", got, gameBin)
	}
}
