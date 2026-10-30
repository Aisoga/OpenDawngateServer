package mapc

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"dawngate/server/internal/setup/mapmm/physmesh"
	"dawngate/server/internal/setup/testgame"
)

// TestSharedFloor: the floor made here is the cooking input the shipped mesh was cooked from (the hex file's header),
// every triangle faces up at the floor's height, and the squares cover the span once.
func TestSharedFloor(t *testing.T) {
	m := SharedFloor()
	if len(m.Verts) != (SharedTiles+1)*(SharedTiles+1) || len(m.Tris) != 2*SharedTiles*SharedTiles {
		t.Fatalf("%d vertices, %d triangles", len(m.Verts), len(m.Tris))
	}
	area := 0.0
	used := make([]bool, len(m.Verts))
	for _, tr := range m.Tris {
		a, b, c := m.Verts[tr[0]], m.Verts[tr[1]], m.Verts[tr[2]]
		if a[2] != physmesh.FloorZ || b[2] != physmesh.FloorZ || c[2] != physmesh.FloorZ {
			t.Fatalf("a triangle off the floor's height: %v", tr)
		}
		cross := (b[0]-a[0])*(c[1]-a[1]) - (b[1]-a[1])*(c[0]-a[0])
		if cross <= 0 {
			t.Fatalf("a triangle not facing up: %v", tr)
		}
		area += cross / 2
		for _, i := range tr {
			used[i] = true
		}
	}
	if want := (2 * SharedSpan) * (2 * SharedSpan); area != want {
		t.Errorf("the floor covers %g m2, not %g", area, want)
	}
	for i, u := range used {
		if !u {
			t.Errorf("vertex %d unused", i)
		}
	}
	if got := digest(SharedMeshInput()); got != SharedDGMISHA256 {
		t.Errorf("the floor's cooking input %s, the shipped mesh's %s", got, SharedDGMISHA256)
	}
	if SharedSpan < MaxSize/2 {
		t.Errorf("the floor (+-%g m) doesn't cover the largest map (%g m)", SharedSpan, MaxSize)
	}
}

// TestSharedCooked: the shipped mesh reads, matches its pins and is a PhysX 2.8 cooked mesh; a caller's change to its
// copy doesn't reach the next one.
func TestSharedCooked(t *testing.T) {
	b, err := SharedCooked()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 12236 || !bytes.HasPrefix(b, []byte(physmesh.CookedMagic)) {
		t.Errorf("%d bytes, % x", len(b), b[:8])
	}
	b[0] = 0
	if c, err := (SharedMesh{}).CookedMesh("CM_Ring"); err != nil || c[0] != 'N' {
		t.Errorf("the second copy: %v", err)
	}
	// a checkout may give the text CRLF line ends (core.autocrlf); ReadMeshHex reads either
	if !bytes.Equal(WriteMeshHex(SharedMeshInput(), mustShared(t)), bytes.ReplaceAll(sharedHex, []byte("\r"), nil)) {
		t.Errorf("shared_bin.hex isn't WriteMeshHex's text of its own bytes")
	}
}

func mustShared(t *testing.T) []byte {
	b, err := SharedCooked()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestCookShared (developers): physcook.exe over a copy of the game's PhysXCooking.dll cooks the floor into exactly
// the shipped mesh, so shared_bin.hex can be made again (gms/physcook; skipped without it or without the game).
func TestCookShared(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("physcook.exe is a Windows program")
	}
	exe := os.Getenv(physCookEnv)
	if exe == "" {
		_, file, _, _ := runtime.Caller(0) // server/internal/setup/mapc
		exe = filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "gms", "build", "Release", "physcook.exe")
	}
	if _, err := os.Stat(exe); err != nil {
		t.Skipf("no physcook.exe (%s; build gms or set %s)", exe, physCookEnv)
	}
	dll := filepath.Join(testgame.Dir(t), "game", "PhysXCooking.dll")
	if _, err := os.Stat(dll); err != nil {
		t.Skip(err)
	}
	cooked, text, err := CookShared(exe, dll)
	if err != nil {
		t.Fatal(err)
	}
	if got := digest(cooked); got != SharedCookedSHA256 {
		t.Errorf("cooked %s, the shipped mesh's %s", got, SharedCookedSHA256)
	}
	if !bytes.Equal(text, bytes.ReplaceAll(sharedHex, []byte("\r"), nil)) {
		t.Errorf("the hex text differs from shared_bin.hex")
	}
}

// physCookEnv names the variable that points at physcook.exe (else gms\build\Release\physcook.exe in this checkout).
const physCookEnv = "DAWNGATE_PHYSCOOK"
