package physmesh

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"

	"dawngate/server/internal/setup/mapmm/los"
)

// CookedMagic starts a little-endian PhysX 2.8 cooked triangle mesh.
const CookedMagic = "NXS\x01MESH"

// The shipped mesh and what it was cooked from:
//   - CookedSHA256 is MM_Map_v3.bin as the level loader reads it (149,872 bytes), cooked once by the developers'
//     physcook tool (gms/physcook) over PhysX 2.8.4.4's cooker from the cooking input of the game's own
//     line-of-sight map, and shipped here as data (MM_Map_v3_bin.hex): our vertices and triangles plus the cooker's
//     search tree and edge flags, no byte of a game file.
//   - SourceDGMISHA256 is that cooking input (DGMI of Build on the game's map); the setup builds it again from the
//     player's map and uses the shipped mesh only when it matches, so the mesh always holds the map's walls.
const (
	CookedSHA256     = "7a220b50fb3a71dcdcfaf230537a82fc958f5a5f15dd533d36575a1a6ab46cde"
	SourceDGMISHA256 = "a0879fc6790744061e471ac9672aef698e7f191208c9ce491c16123adda6ef33"
)

// MM_Map_v3_bin.hex is the cooked mesh as hex text (32 bytes a line), so the repository holds no binary file; the
// developers' tools regenerate it.
//
//go:embed MM_Map_v3_bin.hex
var cookedHex string

var cooked = func() []byte {
	b, err := hex.DecodeString(strings.Join(strings.Fields(cookedHex), ""))
	if err != nil {
		panic("physmesh: MM_Map_v3_bin.hex: " + err.Error())
	}
	return b
}()

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Cooked is the shipped cooked mesh (a copy), checked against CookedSHA256.
func Cooked() ([]byte, error) {
	if got := digest(cooked); got != CookedSHA256 {
		return nil, fmt.Errorf("physmesh: the shipped mesh's SHA-256 is %s, not %s", got[:16], CookedSHA256[:16])
	}
	if !bytes.HasPrefix(cooked, []byte(CookedMagic)) {
		return nil, fmt.Errorf("physmesh: the shipped mesh is not a PhysX 2.8 cooked mesh")
	}
	return bytes.Clone(cooked), nil
}

// ForMap is the shipped cooked mesh for a line-of-sight map whose cells span sx x sy world units: the map's own floor
// and walls (Build) must be the cooking input the mesh was cooked from (SourceDGMISHA256), else the shipped mesh
// isn't this map's and nothing is returned.
func ForMap(img los.Image, sx, sy float64) ([]byte, error) {
	m, err := Build(img, sx, sy)
	if err != nil {
		return nil, err
	}
	if got := digest(m.DGMI()); got != SourceDGMISHA256 {
		return nil, fmt.Errorf("physmesh: this line-of-sight map's walls (cooking input %s) are not the ones the "+
			"shipped mesh was cooked from (%s)", got[:16], SourceDGMISHA256[:16])
	}
	return Cooked()
}
