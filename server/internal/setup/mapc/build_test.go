package mapc

import (
	"bytes"
	"encoding/binary"
	"testing"

	"dawngate/server/internal/setup/mapc/bake"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/mapmm/navgraph"
	"dawngate/server/internal/setup/mapmm/tagmap"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// The pins. RingMadeUp* are CM_Ring's files that need no game file, the graphs and the assets from made-up templates
// (so the public tests, on Windows and Linux, pin everything the compiler computes); Ring854SHA256 is the whole
// archive from build 854's files with the shared mesh (the level files and the assets), RingTiled854SHA256 the same
// with the tiled ground and no decals (the archive before the baked ground).
const (
	RingMadeUpSHA256   = "b2e8323144ca77b4f5e7619edf5b797c0e2d0156bc2c9fafbcd0336235f8120d"
	Ring854SHA256      = "0e25f79534e7aa94b4a33d55d432dfc78b944fcfd4b3fba74403c508389a241d"
	RingTiled854SHA256 = "f0fd13daab5ba836fb465065494dfdb0b2292f48476e382637e1f2637092da37"
)

// madeUpTemplate is a made-up graph template: two triangles and made-up header values (navgraph's tests' kind).
func madeUpTemplate(layer uint32) []byte {
	img := navgraph.FromPolygons([]navgraph.Polygon{
		{{0, 0, 0.5}, {4, 0, 0.5}, {0, 3, 0.5}},
		{{4, 0, 0.5}, {4, 3, 0.5}, {0, 3, 0.5}},
	}, nil)
	g := img.Graphs[0]
	g.Layer = layer
	g.BuildScale, g.VoxSize, g.Radius, g.Step, g.Height = 2.0, 0.35, 1.25, 0.45, 4.5
	for i := range g.Constant {
		g.Constant[i] = byte(i*29 + 11 + int(layer))
	}
	return navgraph.Write(img)
}

// madeUpTags is a .tags file with the game's 16 names in reverse order (no hashes).
func madeUpTags() []byte {
	b := make([]byte, 16+64*16)
	copy(b, "sgat")
	binary.LittleEndian.PutUint32(b[8:], 16)
	for i := range 16 {
		copy(b[16+64*i:], tagmap.Names[15-i])
	}
	return b
}

// ringMadeUp are CM_Ring's game-free files, named.
func ringMadeUp(t *testing.T) []snap.File {
	t.Helper()
	r, cm := Check(ring(t), nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	player, npc, err := navgraph.Build(cm.Layers.Walk(), cm.NavSpec(), madeUpTemplate(1), madeUpTemplate(2))
	if err != nil {
		t.Fatal(err)
	}
	outl, mm, err := cm.FogFiles()
	if err != nil {
		t.Fatal(err)
	}
	timg, err := cm.TagImage(madeUpTags())
	if err != nil {
		t.Fatal(err)
	}
	prev, err := cm.Preview(r)
	if err != nil {
		t.Fatal(err)
	}
	files := []snap.File{{Name: "player", Data: player}, {Name: "npc", Data: npc}, {Name: "outlines", Data: outl},
		{Name: "minimap", Data: mm}, {Name: "los", Data: LoSTGA(cm.Layers.LoS())}, {Name: "grass", Data: cm.Layers.GrassTGA()},
		{Name: "tags", Data: timg}}
	for _, n := range []string{"preview.json", "layout.png", "objects.png", "graph_npc.png", "tags.png", "ground.png"} {
		files = append(files, snap.File{Name: n, Data: prev[n]})
	}
	as, err := cm.Assets(madeUpAssetData(t), madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range as.Names {
		files = append(files, snap.File{Name: n, Data: as.Files[n]})
	}
	return files
}

// TestRingMadeUp pins CM_Ring's game-free files (each one's hash is logged for a diff).
func TestRingMadeUp(t *testing.T) {
	files := ringMadeUp(t)
	var all bytes.Buffer
	for _, f := range files {
		t.Logf("%-34s %8d %s", f.Name, len(f.Data), digest(f.Data)[:16])
		all.WriteString(digest(f.Data))
	}
	if got := digest(all.Bytes()); got != RingMadeUpSHA256 {
		t.Errorf("SHA-256 %s, pinned %s", got, RingMadeUpSHA256)
	}
	again := ringMadeUp(t)
	for i := range files {
		if !bytes.Equal(files[i].Data, again[i].Data) {
			t.Errorf("%s differs between two runs", files[i].Name)
		}
	}
	// the graphs: the NPC graph holds the Player graph's squares and the two Guardian islands
	_, cm := Check(ring(t), nil)
	for _, g := range []struct {
		b    []byte
		want int
	}{{files[0].Data, cm.Player.Areas}, {files[1].Data, cm.NPC.Areas}} {
		img, err := navgraph.Parse(g.b)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(img.Graphs[0].Areas); n != g.want {
			t.Errorf("%d areas, %d expected", n, g.want)
		}
	}
}

// TestGame builds CM_Ring from build 854's files with the shared mesh and compares the archive with the pin.
func TestGame(t *testing.T) {
	dir := testgame.Dir(t)
	in, err := ReadInputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, r, err := Build(ring(t), in, SharedMesh{}, nil)
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if got := digest(b); got != Ring854SHA256 {
		t.Errorf("SHA-256 %s, pinned %s", got, Ring854SHA256)
	}
	a, err := snap.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	e := EntriesFor("CM_Ring")
	want := []string{e.Player, e.NPC, e.Mesh, e.Outlines, e.Minimap, e.LoS, e.Grass, e.Tags, e.TagImage, e.Model}
	for _, tj := range []int{0, 1} { // the baked ground's 2 x 2 tiles and its cliff
		for _, ti := range []int{0, 1} {
			want = append(want, BakeTileEntry("CM_Ring", ti, tj))
		}
	}
	want = append(want, CliffEntry("CM_Ring"))
	for _, n := range AssetNames("CM_Ring") {
		want = append(want, AssetEntry(n))
	}
	if len(a.Entries) != len(want) {
		t.Fatalf("%d entries, %d expected", len(a.Entries), len(want))
	}
	read := map[string][]byte{}
	for i, en := range a.Entries {
		if en.Name != want[i] {
			t.Errorf("entry %d: %s, %s expected", i, en.Name, want[i])
		}
		if read[en.Name], err = a.Read(en); err != nil {
			t.Fatal(err)
		}
	}
	// the line-of-sight map is laid out as the shipped ones (the header but for the size)
	images := testgame.Read(t, "images.snap")
	sm, err := los.FromArchive(images, "images/8bit/SM_Map_v5_LineOfSight.tga")
	if err != nil {
		t.Fatal(err)
	}
	ours := read[e.LoS]
	if !bytes.Equal(ours[:12], sm[:12]) || !bytes.Equal(ours[16:18], sm[16:18]) || len(ours) != len(sm) {
		t.Errorf("our line-of-sight header % x, the shipped % x (%d and %d bytes)", ours[:18], sm[:18], len(ours),
			len(sm))
	}
	if info, err := bake.CheckModel(read[e.Model]); err != nil || info.Materials != 5 {
		t.Errorf("the ground model: %+v %v", info, err)
	} else {
		t.Logf("ground model %+v", info)
	}
	// the tiled style is as before the bake came: CM_Ring's archive with its old ground
	tiled := ring(t)
	tiled.Ground = Ground{Style: "tiled"}
	b, r, err = Build(tiled, in, SharedMesh{}, nil)
	if err != nil {
		t.Fatalf("%v %+v", err, r)
	}
	if got := digest(b); got != RingTiled854SHA256 {
		t.Errorf("tiled: SHA-256 %s, pinned %s", got, RingTiled854SHA256)
	}
	if info, err := ground.CheckModel(read[e.Model]); err == nil {
		t.Errorf("the baked model passes the tiled check: %+v", info)
	}
}
