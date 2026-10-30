package mapc

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/mapc/bake"
	"dawngate/server/internal/setup/mapmm/fogmap"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/mapmm/navgraph"
	"dawngate/server/internal/setup/mapmm/physmesh"
	"dawngate/server/internal/setup/mapmm/tagmap"
	"dawngate/server/internal/setup/snap"
)

// Sources are the copy's archives Build reads (paths in a copy): SM_Map_v5's graphs (the templates of the graphs'
// header values) and its level tag names, the grass texture, the terrain model (the template of the ground's
// materials) and the asset data (the template Level, markers and terrain noun; the environment and tuning levels).
var Sources = []string{"game/Data/SM_Map_v5.snap", "game/Data/anshavar3.snap", "game/Data/anshavar4.snap",
	AssetDataSource}

// Inputs are the copy's archives, in Sources' order, and Archive, which reads another of the copy's archives
// (game/Data/<name>) for the shipped textures a baked ground names (nil: none other).
type Inputs struct {
	SMMap, Anshavar3, Anshavar4, AssetData []byte
	Archive                                func(name string) ([]byte, error)
}

// ReadInputs reads Sources from a copy (the folder holding game/Data).
func ReadInputs(copyDir string) (Inputs, error) {
	var bs [4][]byte
	for i, s := range Sources {
		b, err := os.ReadFile(filepath.Join(copyDir, filepath.FromSlash(s)))
		if err != nil {
			return Inputs{}, err
		}
		bs[i] = b
	}
	return Inputs{SMMap: bs[0], Anshavar3: bs[1], Anshavar4: bs[2], AssetData: bs[3],
		Archive: ArchiveFromCopy(copyDir)}, nil
}

const (
	smPlayer  = "levels/SM_Map_v5/SM_Map_v5_Player.bfx"
	smNPC     = "levels/SM_Map_v5/SM_Map_v5_NPC.bfx"
	smTags    = "levels/SM_Map_v5/SM_Map_v5.tags"
	minimapMs = 7
)

// Entries are the archive's file names for a map, as the Level names them (the depot matches paths in any case).
type Entries struct {
	Player, NPC, Mesh, Outlines, Minimap, LoS, Grass, Tags, TagImage, Model, Dirt, GrassTex string
}

// EntriesFor names a map's files: its level folder levels/<name>/ and the image folders as MM_Map_v3's, its ground
// under environments/community/<name>/.
func EntriesFor(name string) Entries {
	lv := "levels/" + name + "/" + name
	env := "environments/community/" + strings.ToLower(name) + "/"
	return Entries{
		Player: lv + "_Player.bfx", NPC: lv + "_NPC.bfx", Mesh: lv + ".bin", Outlines: lv + ".out",
		Minimap: "textures/minimap/" + name + "_minimap.dds", LoS: "images/8bit/" + name + "_lineOfSight.tga",
		Grass: "images/32bit/" + name + "_GrassMap.tga", Tags: lv + ".tags", TagImage: "images/32bit/" + name + "_Tags.tga",
		Model: env + strings.ToLower(name) + "_terrain.bmdl", Dirt: env + "textures/" + strings.ToLower(name) + "_dirt_D.dds",
		GrassTex: env + "textures/" + strings.ToLower(name) + "_grass_D.dds",
	}
}

// File is the archive's file name for a map.
func File(name string) string { return "zz_" + name + ".snap" }

// MeshSource gives a level's cooked PhysX mesh (levels/<name>/<name>.bin). Only PhysX's own cooker can cook, so the
// mesh comes from outside the compiler; this is the one place that decides where. Every map uses SharedMesh, the one
// floor the developers cooked (shared.go).
type MeshSource interface {
	CookedMesh(name string) ([]byte, error)
}

// ReadMeshHex reads a cooked mesh as hex text (the first line "# dgmi-sha256 <hex>", the cooking input it was cooked
// from, then the cooked bytes as hex): the input's SHA-256 and the cooked bytes.
func ReadMeshHex(b []byte) (dgmiSHA string, cooked []byte, err error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	var sb strings.Builder
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			f := strings.Fields(line)
			if len(f) != 3 || f[0] != "#" || f[1] != "dgmi-sha256" || len(f[2]) != 64 {
				return "", nil, fmt.Errorf("the first line isn't \"# dgmi-sha256 <hex>\"")
			}
			dgmiSHA = f[2]
			continue
		}
		sb.WriteString(line)
	}
	if err := sc.Err(); err != nil {
		return "", nil, err
	}
	cooked, err = hex.DecodeString(sb.String())
	if err != nil {
		return "", nil, err
	}
	if !bytes.HasPrefix(cooked, []byte(physmesh.CookedMagic)) {
		return "", nil, fmt.Errorf("not a PhysX 2.8 cooked mesh")
	}
	return dgmiSHA, cooked, nil
}

// WriteMeshHex is a cooked mesh's hex text: the header, then 32 bytes a line.
func WriteMeshHex(dgmi, cooked []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# dgmi-sha256 %s\n", digest(dgmi))
	for i := 0; i < len(cooked); i += 32 {
		b.WriteString(hex.EncodeToString(cooked[i:min(i+32, len(cooked))]))
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// SHA256 is b's SHA-256 in hex (an archive's pin).
func SHA256(b []byte) string { return digest(b) }

// TagSpec is the tag map's spec: the Loci, the Bindings, the Inner Bindings' lines (the Mortal side's highest
// x + y, the Spirit side's lowest), the wells, and the lanes' bands and the camps (tags.go: Lane, Top and Bottom in
// the bands, Jungle and the camp tags off them).
func (cm *Compiled) TagSpec() tagmap.Spec {
	t1, t2 := &cm.Layout.Teams[0], &cm.Layout.Teams[1]
	tp := func(p Point) tagmap.Point { return tagmap.Point{X: p[0], Y: p[1]} }
	s := tagmap.Spec{SizeX: cm.Map.Size, SizeY: cm.Map.Size, Loci: []tagmap.Point{tp(t1.Locus), tp(t2.Locus)},
		TwoLanes: len(cm.Layout.LaneIDs) == 2}
	var set [2]bool // a team's Inner line found
	for _, id := range cm.Layout.LaneIDs {
		for t, team := range []*Team{t1, t2} {
			for _, p := range team.Bindings[id] {
				s.Bindings = append(s.Bindings, tp(p))
			}
			if len(team.Bindings[id]) == 0 { // a lane of a one-path map without its own (fixed.bindings)
				continue
			}
			in := team.Bindings[id][len(team.Bindings[id])-1] // the Inner Binding
			u, cur := in[0]+in[1], s.Inner[t].X+s.Inner[t].Y
			if !set[t] || (t == 0 && u > cur) || (t == 1 && u < cur) {
				s.Inner[t] = tp(in)
				set[t] = true
			}
		}
	}
	for _, team := range []*Team{t1, t2} {
		for _, w := range team.Wells {
			s.Wells = append(s.Wells, tp(w))
		}
	}
	cm.jungleTags(&s) // tags.go
	return s
}

// GroundSpec is the ground model's spec: the base ends at the spawn points' centres, chasms dropping, the walkable
// floor dipping under the props' fords (known once ResolveProps has read the copy).
func (cm *Compiled) GroundSpec() ground.Spec {
	a, b := cm.Layout.Teams[0].SpawnCentre(), cm.Layout.Teams[1].SpawnCentre()
	ly := cm.Layers
	return ground.Spec{Size: cm.Map.Size, Bases: [2][2]float64{a, b},
		Down: func(x, y float64) bool { return ly.KindAt(Point{x, y}) == Chasm }, Dips: cm.Map.fords,
		Cut: cm.Map.voidCut()}
}

// MinimapSpec is the minimap's: the base ends, the wells on islands (MM_Map_v3's island colour round them), the
// chasms.
func (cm *Compiled) MinimapSpec() fogmap.MinimapSpec {
	chasm := make([]bool, len(cm.Layers.Kind))
	for k, v := range cm.Layers.Kind {
		chasm[k] = v == Chasm
	}
	var islands [][2]float64
	for t := range 2 {
		for _, w := range cm.IslandWells(t) {
			islands = append(islands, cm.Layout.Teams[t].Wells[w])
		}
	}
	return fogmap.MinimapSpec{Bases: [2][2]float64{cm.Layout.Teams[0].SpawnCentre(), cm.Layout.Teams[1].SpawnCentre()},
		Islands: islands, Chasm: chasm}
}

// MinimapRGBA is the minimap's picture (W x H RGBA, rows top to bottom, +y up).
func (cm *Compiled) MinimapRGBA() ([]byte, error) {
	img := cm.Layers.LoS()
	f, err := fogmap.NewField(img)
	if err != nil {
		return nil, err
	}
	return fogmap.MinimapRGBASpec(img, f, cm.Map.Size, cm.Map.Size, cm.MinimapSpec()), nil
}

// LevelFiles makes the level's files from the copy's inputs, in the archive's order: the level files, then the
// assets (AssetNames' order) under AssetData/Binary/. mesh nil leaves the PhysX mesh out (a preview's or a test's
// build). The findings are the warnings placing the markers raised.
func (cm *Compiled) LevelFiles(in Inputs, mesh MeshSource) ([]snap.File, []Finding, error) {
	files, err := cm.levelFiles(in, mesh)
	if err != nil {
		return nil, nil, err
	}
	e := EntriesFor(cm.Map.Name)
	var model []byte
	for _, f := range files {
		if f.Name == e.Model {
			model = f.Data
		}
	}
	info, err := bake.CheckModel(model)
	if err != nil {
		return nil, nil, err
	}
	as, err := cm.Assets(in.AssetData, info.Bounds)
	if err != nil {
		return nil, nil, err
	}
	for _, n := range as.Names {
		files = append(files, snap.File{Name: AssetEntry(n), Data: as.Files[n]})
	}
	return files, as.Findings, nil
}

func (cm *Compiled) levelFiles(in Inputs, mesh MeshSource) ([]snap.File, error) {
	e := EntriesFor(cm.Map.Name)
	var out []snap.File
	add := func(name string, b []byte) { out = append(out, snap.File{Name: name, Data: b}) }
	pt, err := los.FromArchive(in.SMMap, smPlayer)
	if err != nil {
		return nil, err
	}
	nt, err := los.FromArchive(in.SMMap, smNPC)
	if err != nil {
		return nil, err
	}
	player, npc, err := navgraph.BuildTwo(cm.Layers.Walk(), cm.Layers.NPCWalk(), cm.NavSpec(), pt, nt)
	if err != nil {
		return nil, err
	}
	add(e.Player, player)
	add(e.NPC, npc)

	if mesh != nil {
		b, err := mesh.CookedMesh(cm.Map.Name)
		if err != nil {
			return nil, err
		}
		add(e.Mesh, b)
	}

	outl, mm, err := cm.FogFiles()
	if err != nil {
		return nil, err
	}
	add(e.Outlines, outl)
	add(e.Minimap, mm)
	add(e.LoS, LoSTGA(cm.Layers.LoS()))
	add(e.Grass, cm.Layers.GrassTGA())

	tags, err := los.FromArchive(in.SMMap, smTags)
	if err != nil {
		return nil, err
	}
	timg, err := cm.TagImage(tags)
	if err != nil {
		return nil, err
	}
	add(e.Tags, tags)
	add(e.TagImage, timg)

	// the ground (groundtex.go): the tiled one, or the baked one with its tiles and the minimap from the bake
	gf, baked, err := cm.GroundFiles(in.Shipped())
	if err != nil {
		return nil, err
	}
	if baked != nil {
		for k := range out {
			if out[k].Name == e.Minimap {
				out[k].Data = baked
			}
		}
	}
	return append(out, gf...), nil
}

// FogFiles are the fog outlines (the walls' and the bushes') and the minimap (DXT5, 7 mips).
func (cm *Compiled) FogFiles() (outlines, minimap []byte, err error) {
	losImg := cm.Layers.LoS()
	size := cm.Map.Size
	f, err := fogmap.NewField(losImg)
	if err != nil {
		return nil, nil, err
	}
	outl, err := fogmap.OutlinesFrom(losImg, f, size, size)
	if err != nil {
		return nil, nil, err
	}
	if outl, err = fogmap.AppendGrass(outl, cm.Layers.GrassOutlines()); err != nil {
		return nil, nil, err
	}
	rgba := fogmap.MinimapRGBASpec(losImg, f, size, size, cm.MinimapSpec())
	mm, err := dds.Encode(Grid, Grid, rgba, "dxt5", minimapMs)
	if err != nil {
		return nil, nil, err
	}
	return outl, mm, nil
}

// TagImage is the tag map for a .tags file (its bits in the file's entry order).
func (cm *Compiled) TagImage(tags []byte) ([]byte, error) {
	names, err := tagmap.TagNames(tags)
	if err != nil {
		return nil, err
	}
	return tagmap.Image(names, cm.TagSpec())
}

// Build checks the map and makes its archive. A map with an error finding isn't built (the report says why). The
// props' boxes are read from the copy first (ResolveProps), so the blocking props' footprints are in the layers.
func Build(m *Map, in Inputs, mesh MeshSource, others []Other) ([]byte, *Report, error) {
	if err := ResolveProps(m, in.AssetData); err != nil {
		return nil, nil, fmt.Errorf("%s: %v", m.Name, err)
	}
	r, cm := Check(m, others)
	if !r.OK {
		return nil, r, fmt.Errorf("%s: the map fails %d rule check(s)", m.Name, countErrors(r))
	}
	files, fs, err := cm.LevelFiles(in, mesh)
	if err != nil {
		return nil, r, fmt.Errorf("%s: %v", m.Name, err)
	}
	r.Findings = append(r.Findings, fs...)
	if n := countErrors(r); n > 0 { // placing the props found a noun that isn't dressing
		r.OK = false
		return nil, r, fmt.Errorf("%s: the map fails %d rule check(s)", m.Name, n)
	}
	return snap.Pack(files), r, nil
}

func countErrors(r *Report) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == Error {
			n++
		}
	}
	return n
}
