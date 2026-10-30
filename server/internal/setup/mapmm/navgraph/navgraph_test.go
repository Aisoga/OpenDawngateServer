package navgraph

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"testing"
	"time"

	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/testgame"
)

// The made-up inputs' results, as the Python reference makes them from the same inputs.
const (
	wantTemplatePlayer = "bfd38b6e9ff9d0742822f4dcbf33adbb82eaa475ab3df2ba5a7bc31d6fe7c34a"
	wantTemplateNPC    = "f44f9760c2eb08b0be42f423f9bfdb935493fdee9cd17ee5c9d2ef7c7b96d48d"
	wantSynthPlayer    = "a56f182b3046274e2315a39fb935c70f96b44301b886247d3884494cf0b2ddd5"
	wantSynthNPC       = "5474841e44cd59aaf4702c2055800270d064082528c597e93097033b1135f5ef"
)

// MM_Map_v3's graphs as the game's line-of-sight map and SM_Map_v5's graphs make them.
const (
	wantMMPlayer = "58497c72afdb978936e54e74c98fac3849c3b7dbb04f85079d5569b255130ac9"
	wantMMNPC    = "a7b50a5bfaa2724a51bf179d3ea6d50ba03caa95ae5609564c1867590024bbd5"
)

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// synthLOS is the made-up line-of-sight map (128 x 112 cells): a wall with a corridor through it, a ring around
// a pocket that only an NPC keep point keeps, a ring around a pocket no one keeps, a blocked corner, a diagonal
// wall and scattered pillars.
func synthLOS() los.Image {
	const w, h = 128, 112
	pix := make([]byte, w*h)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			var v byte
			switch {
			case i >= 60 && i <= 67 && !(j >= 50 && j <= 61):
				v = 128
			case max(iabs(i-96), iabs(j-80)) >= 9 && max(iabs(i-96), iabs(j-80)) <= 11:
				v = 128
			case max(iabs(i-24), iabs(j-24)) >= 6 && max(iabs(i-24), iabs(j-24)) <= 7:
				v = 128
			case i < 18 && j >= 90:
				v = 255
			case i >= 75 && i <= 115 && i-j-40 >= 0 && i-j-40 <= 2:
				v = 64
			case (i*i+3*j*j+i*j)%89 == 0:
				v = 128
			}
			pix[j*w+i] = v
		}
	}
	return los.Image{W: w, H: h, Bpp: 8, Pix: pix}
}

var synthSpec = Spec{
	SizeX: 150.4, SizeY: 131.6, Cell: 2, Erode: 2, Z: 0.7,
	Keep: []Point{{-40.0, 5.0}},
	// The pillars split the map into 21 parts: the kept one has 1,200 squares, the right side's biggest 642.
	// NPCKeep: the kept part; both Guardians (inside their holes, so dropped; the second would keep the 642);
	// the pocket's centre; a point on the pocket's ring (kept by the nearest-block search); a point whose
	// nearest blocks lie in the kept part and in the 642 (the first in discovery order wins, the first in row
	// order would be the 642's); a point in the blocked corner (too far from any block).
	NPCKeep: []Point{{-40.0, 5.0}, {-30.0, -30.0}, {45.0, -35.0}, {37.6, 28.2}, {37.6, 16.45}, {19.39, -15.86},
		{-69.3, 57.6}},
	Guardians:  []Point{{-30.0, -30.0}, {45.0, -35.0}},
	HoleRadius: 10.0, IslandRadius: 3.7, IslandZ: 1.1, IslandSides: 8,
}

// synthTemplate is a made-up template graph: two triangles, made-up header values and constant bytes.
func synthTemplate(layer uint32, radius float64, navsetVersion uint32) []byte {
	img := FromPolygons([]Polygon{
		{{0, 0, 0.5}, {4, 0, 0.5}, {0, 3, 0.5}},
		{{4, 0, 0.5}, {4, 3, 0.5}, {0, 3, 0.5}},
	}, nil)
	g := img.Graphs[0]
	g.Layer = layer
	g.BuildScale, g.VoxSize, g.Radius, g.Step, g.Height = 2.0, 0.35, float32(radius), 0.45, 4.5
	for i := range g.Reserved {
		g.Reserved[i] = byte(i*7 + 3)
	}
	for i := range g.Constant {
		g.Constant[i] = byte(i*29 + 11 + int(layer))
	}
	img.NavsetVersion = navsetVersion
	return Write(img)
}

// roundTrip checks that a written graph parses and writes back byte for byte, and that its kd tree names every
// area once.
func roundTrip(t *testing.T, name string, b []byte) *Graph {
	t.Helper()
	img, err := Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !bytes.Equal(Write(img), b) {
		t.Errorf("%s: no byte-for-byte round trip", name)
	}
	g := img.Graphs[0]
	seen := map[uint32]int{}
	var walk func(n *KDNode)
	walk = func(n *KDNode) {
		if n.Leaf {
			seen[n.Area]++
			return
		}
		walk(n.Left)
		walk(n.Right)
	}
	walk(g.KD)
	for _, a := range g.Areas {
		if seen[a.Offset] != 1 {
			t.Errorf("%s: area 0x%x is in %d kd leaves", name, a.Offset, seen[a.Offset])
			break
		}
	}
	if len(seen) != len(g.Areas) {
		t.Errorf("%s: %d kd leaves for %d areas", name, len(seen), len(g.Areas))
	}
	return g
}

func stats(g *Graph) (linked, cluster, octagons int) {
	for _, a := range g.Areas {
		if len(a.Edges) == 8 {
			octagons++
		}
		for _, e := range a.Edges {
			if e.Adj != 0 {
				linked++
			}
			if e.Flags1&ClusterEdge != 0 {
				cluster++
			}
		}
	}
	return
}

// TestSynthetic builds the made-up map's graphs from made-up templates and compares them with the Python
// reference's hashes.
func TestSynthetic(t *testing.T) {
	tp, tn := synthTemplate(1, 1.25, 0x26), synthTemplate(2, 0.9, 0x25)
	for _, c := range []struct{ name, got, want string }{
		{"Player template", sha(tp), wantTemplatePlayer}, {"NPC template", sha(tn), wantTemplateNPC},
	} {
		if c.got != c.want {
			t.Errorf("%s: sha256 %s, want %s", c.name, c.got, c.want)
		}
	}
	player, npc, err := Build(synthLOS(), synthSpec, tp, tn)
	if err != nil {
		t.Fatal(err)
	}
	pg, ng := roundTrip(t, "Player", player), roundTrip(t, "NPC", npc)
	pl, pc, po := stats(pg)
	nl, nc, no := stats(ng)
	t.Logf("Player: %d areas, %d linked edges, %d cluster edges, %d octagons", len(pg.Areas), pl, pc, po)
	t.Logf("NPC:    %d areas, %d linked edges, %d cluster edges, %d octagons", len(ng.Areas), nl, nc, no)
	// the NPC graph: the Player graph's 1,200 squares, the pocket's 41 and the two Guardians' octagons
	if len(pg.Areas) != 1200 || len(ng.Areas) != 1243 || po != 0 || no != 2 || pc == 0 {
		t.Errorf("Player %d areas, NPC %d areas (%d octagons): 1,200 and 1,243 (2) expected", len(pg.Areas),
			len(ng.Areas), no)
	}
	if got := sha(player); got != wantSynthPlayer {
		t.Errorf("Player: sha256 %s, want %s", got, wantSynthPlayer)
	}
	if got := sha(npc); got != wantSynthNPC {
		t.Errorf("NPC: sha256 %s, want %s", got, wantSynthNPC)
	}
}

// TestNumerics checks the helpers that reproduce the reference's library functions.
func TestNumerics(t *testing.T) {
	if got := norm(3, 4); got != 5 {
		t.Errorf("norm(3, 4) = %v", got)
	}
	if got := norm(-2.5, 0); got != 2.5 {
		t.Errorf("norm(-2.5, 0) = %v", got)
	}
	for _, c := range []struct{ x, want float64 }{
		{0.0625, 0.062}, {0.0635, 0.064}, {-0.0004, 0}, {1.2345, 1.234}, {2.0005, 2.001}, {-166.30049, -166.3},
	} {
		if got := round3(c.x); got != c.want || (got == 0 && math.Signbit(got)) {
			t.Errorf("round3(%v) = %v, want %v", c.x, got, c.want)
		}
	}
	for _, c := range []struct{ a, b, want float64 }{{7, 2, 3}, {-7, 2, -4}, {-0.5, 2, -1}, {0.5, 2, 0}} {
		if got := pyFloorDiv(c.a, c.b); got != c.want {
			t.Errorf("%v // %v = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if got := pySum([]float64{1e16, 1, -1e16}); got != 1 {
		t.Errorf("compensated sum = %v, want 1", got)
	}
}

// TestGame builds MM_Map_v3's graphs from the game's own files and compares them with the shipped reference.
func TestGame(t *testing.T) {
	raw, err := los.FromArchive(testgame.Read(t, los.Archive), los.Entry)
	if err != nil {
		t.Fatal(err)
	}
	img, err := los.Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	snapSM := testgame.Read(t, "SM_Map_v5.snap")
	tp, err := los.FromArchive(snapSM, "levels/SM_Map_v5/SM_Map_v5_Player.bfx")
	if err != nil {
		t.Fatal(err)
	}
	tn, err := los.FromArchive(snapSM, "levels/SM_Map_v5/SM_Map_v5_NPC.bfx")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		b    []byte
	}{{"SM_Map_v5_Player.bfx", tp}, {"SM_Map_v5_NPC.bfx", tn}} {
		roundTrip(t, c.name, c.b)
	}
	start := time.Now()
	player, npc, err := BuildMM(img, tp, tn)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("built in %v: Player %d bytes, NPC %d bytes", time.Since(start), len(player), len(npc))
	roundTrip(t, "MM_Map_v3_Player.bfx", player)
	roundTrip(t, "MM_Map_v3_NPC.bfx", npc)
	if got := sha(player); got != wantMMPlayer {
		t.Errorf("MM_Map_v3_Player.bfx: sha256 %s, want %s", got, wantMMPlayer)
	}
	if got := sha(npc); got != wantMMNPC {
		t.Errorf("MM_Map_v3_NPC.bfx: sha256 %s, want %s", got, wantMMNPC)
	}
}

// TestCells: a made-up graph's two areas (a square and a triangle) cover the cells whose centres lie in them, an
// edge's own centres included; SM_Map_v5's shipped Player graph covers 98,554 cells of its 512 x 512 grid, none of
// them blocked in its line-of-sight map.
func TestCells(t *testing.T) {
	area := func(pts ...[2]float32) Area {
		a := Area{}
		for _, p := range pts {
			a.Edges = append(a.Edges, Edge{Pos: [3]float32{p[0], p[1], 0.7}})
		}
		return a
	}
	g := &Graph{Areas: []Area{area([2]float32{-2, -2}, [2]float32{2, -2}, [2]float32{2, 2}, [2]float32{-2, 2}),
		area([2]float32{3, 0}, [2]float32{5, 0}, [2]float32{3, 2})}}
	on := Cells(g, 16, 16) // cells of 1 m, cell (i, j) centred on (i - 8, j - 8)
	want := map[[2]int]bool{}
	for j := 6; j <= 10; j++ {
		for i := 6; i <= 10; i++ {
			want[[2]int{i, j}] = true
		}
	}
	for _, c := range [][2]int{{11, 8}, {12, 8}, {13, 8}, {11, 9}, {12, 9}, {11, 10}} {
		want[c] = true
	}
	for k, v := range on {
		if c := [2]int{k % 16, k / 16}; v != want[c] {
			t.Errorf("cell %v: %v", c, v)
		}
	}
	snapSM := testgame.Read(t, "SM_Map_v5.snap")
	raw, err := los.FromArchive(snapSM, "levels/SM_Map_v5/SM_Map_v5_Player.bfx")
	if err != nil {
		t.Fatal(err)
	}
	img, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	lraw, err := los.FromArchive(testgame.Read(t, los.Archive), "images/8bit/SM_Map_v5_LineOfSight.tga")
	if err != nil {
		t.Fatal(err)
	}
	lim, err := los.Read(lraw)
	if err != nil {
		t.Fatal(err)
	}
	n, blocked := 0, 0
	for k, v := range Cells(img.Graphs[0], 525.434, 512) {
		if v {
			n++
			if lim.Pix[k] != 0 {
				blocked++
			}
		}
	}
	if n != 98554 || blocked != 0 {
		t.Errorf("SM_Map_v5_Player: %d cells, %d of them blocked", n, blocked)
	}
}
