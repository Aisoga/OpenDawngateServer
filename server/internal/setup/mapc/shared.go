package mapc

import (
	"bytes"
	_ "embed"
	"fmt"

	"dawngate/server/internal/setup/mapmm/physmesh"
)

// The shared PhysX mesh. Every map gets the same levels/<name>/<name>.bin: a flat floor without walls. A match never
// asks the physics mesh about a wall: the terrain-height queries cast down from above the asked point and the ground
// plane answers where a mesh has no floor, so a map compiled with only a floor plays as one with its walls (tested
// live on MM_Map_v3 with this mesh in place of its walled one: heights, skillshots across walls, structures, minions
// and bots). So the compiler needs no PhysX cooker: the mesh was cooked once by the developers' physcook tool over
// PhysX 2.8.4.4's cooker and ships as data (shared_bin.hex): our vertices and triangles plus the cooker's search tree,
// no byte of a game file.
//
// The floor: SharedTiles x SharedTiles squares of SharedTile metres over -SharedSpan..SharedSpan in x and y, its top
// at physmesh.FloorZ, every triangle facing up (SharedFloor). It covers every map the rules allow (MaxSize).
const (
	SharedSpan  = 400.0
	SharedTile  = 50.0
	SharedTiles = 16
	// SharedDGMISHA256 is the cooking input's SHA-256 (DGMI of SharedFloor), the hex file's header.
	SharedDGMISHA256 = "b0fa0b0e15a39298e8de14ff84d56613f9751073e3219d22c17ef2e9e85add87"
	// SharedCookedSHA256 is the cooked mesh's (12,236 bytes).
	SharedCookedSHA256 = "967040d32f60c57839036b0bf4f8321be7cdf736563d8e61a2f0d203974031b4"
)

// shared_bin.hex is the cooked mesh as hex text (the first line "# dgmi-sha256 <hex>", then 32 bytes a line), so the
// repository holds no binary file; `mapc cook-shared` makes it again.
//
//go:embed shared_bin.hex
var sharedHex []byte

// SharedFloor is the shared mesh's triangles: the vertices row by row from the south-west corner (x fastest), each
// square two triangles counter-clockwise seen from above.
func SharedFloor() physmesh.Mesh {
	const n = SharedTiles
	var m physmesh.Mesh
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			m.Verts = append(m.Verts, [3]float64{-SharedSpan + float64(i)*SharedTile,
				-SharedSpan + float64(j)*SharedTile, physmesh.FloorZ})
		}
	}
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			a, b := uint32(j*(n+1)+i), uint32(j*(n+1)+i+1)
			c, d := uint32((j+1)*(n+1)+i+1), uint32((j+1)*(n+1)+i)
			m.Tris = append(m.Tris, [3]uint32{a, b, c}, [3]uint32{a, c, d})
		}
	}
	return m
}

// SharedMeshInput is the shared mesh's cooking input (physcook's DGMI form).
func SharedMeshInput() []byte { return SharedFloor().DGMI() }

// SharedCooked is the shipped cooked mesh, checked against its pins and its cooking input.
func SharedCooked() ([]byte, error) {
	dgmiSHA, cooked, err := ReadMeshHex(sharedHex)
	if err != nil {
		return nil, fmt.Errorf("the shared mesh: %v", err)
	}
	if dgmiSHA != SharedDGMISHA256 {
		return nil, fmt.Errorf("the shared mesh was cooked from input %s, not %s", dgmiSHA[:16], SharedDGMISHA256[:16])
	}
	if got := digest(cooked); got != SharedCookedSHA256 {
		return nil, fmt.Errorf("the shared mesh's SHA-256 is %s, not %s", got[:16], SharedCookedSHA256[:16])
	}
	return bytes.Clone(cooked), nil
}

// SharedMesh is the MeshSource every map uses: the shared floor, whatever the map's layout.
type SharedMesh struct{}

// CookedMesh implements MeshSource.
func (SharedMesh) CookedMesh(string) ([]byte, error) { return SharedCooked() }
