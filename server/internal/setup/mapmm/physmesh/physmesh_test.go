package physmesh

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"dawngate/server/internal/setup/mapmm/fogmap/synthlos"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/testgame"
)

// The Python reference's hashes: the cooking input of a made-up 77 x 45 map (81.3 x 47.9 m) and of MM_Map_v3's
// own map, and MM_Map_v3's cooked mesh.
const (
	synthDGMI = "2adf87f4cffeef1ef68076d7a202948121c6d916919d4f7667f45acbe04101a4"
	gameDGMI  = "a0879fc6790744061e471ac9672aef698e7f191208c9ce491c16123adda6ef33" // 126,856 bytes
	gameBin   = "7a220b50fb3a71dcdcfaf230537a82fc958f5a5f15dd533d36575a1a6ab46cde" // MM_Map_v3.bin, 149,872 bytes
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestSynth(t *testing.T) {
	m, err := Build(synthlos.Map(77, 45), 81.3, 47.9)
	if err != nil {
		t.Fatal(err)
	}
	if got := sum(m.DGMI()); got != synthDGMI {
		t.Errorf("77 x 45: sha256 %s, the Python reference's %s", got, synthDGMI)
	}
}

// TestSmall checks a 6 x 5 map by hand: a 2 x 2 wall block and a blocked cell at the edge.
func TestSmall(t *testing.T) {
	pix := make([]byte, 30)
	for _, c := range [][2]int{{2, 1}, {3, 1}, {2, 2}, {3, 2}, {0, 4}} {
		pix[c[1]*6+c[0]] = 128
	}
	blocked := make([]bool, 30)
	for k, v := range pix {
		blocked[k] = v != 0
	}
	if got := rectangles(blocked, 6, 5); len(got) != 2 || got[0] != (rect{2, 1, 4, 3}) || got[1] != (rect{0, 4, 1, 5}) {
		t.Errorf("wall tops %v", got)
	}
	segs := wallSegments(blocked, 6, 5)
	want := []segment{{false, 1, 4, 5, 1}, {false, 2, 1, 3, -1}, {false, 4, 1, 3, 1}, {true, 1, 2, 4, -1},
		{true, 3, 2, 4, 1}, {true, 4, 0, 1, -1}}
	if len(segs) != len(want) {
		t.Fatalf("side faces %v", segs)
	}
	for k := range want {
		if segs[k] != want[k] {
			t.Errorf("side face %d: %v, %v expected", k, segs[k], want[k])
		}
	}
	m, err := Build(los.Image{W: 6, H: 5, Bpp: 8, Pix: pix}, 6, 5)
	if err != nil {
		t.Fatal(err)
	}
	// one floor tile and two wall tops (2 triangles each), six side faces (2 each)
	if len(m.Tris) != 2+4+12 {
		t.Errorf("%d triangles", len(m.Tris))
	}
	if d := m.DGMI(); len(d) != 24+12*len(m.Verts)+14*len(m.Tris) || string(d[:4]) != "DGMI" {
		t.Errorf("DGMI of %d bytes", len(d))
	}
}

func gameMap(t *testing.T) los.Image {
	raw, err := los.FromArchive(testgame.Read(t, los.Archive), los.Entry)
	if err != nil {
		t.Fatal(err)
	}
	img, err := los.Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// TestGame builds MM_Map_v3's mesh from the game's own line-of-sight map.
func TestGame(t *testing.T) {
	img := gameMap(t)
	start := time.Now()
	m, err := Build(img, los.Size, los.Size)
	if err != nil {
		t.Fatal(err)
	}
	d := m.DGMI()
	t.Logf("%d vertices, %d triangles, %d bytes in %v", len(m.Verts), len(m.Tris), len(d), time.Since(start))
	if got := sum(d); got != gameDGMI {
		t.Errorf("sha256 %s, the reference %s", got, gameDGMI)
	}
}
