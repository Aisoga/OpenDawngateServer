// Package mapmm makes zz_MM_Map_v3.snap, the added archive that lets the game load its cut second map MM_Map_v3.
//
// The game ships MM_Map_v3's level asset, its line-of-sight map and its scenery, but none of the files its level
// names: the navigation graphs, the PhysX mesh, the fog outlines, the minimap and the ground model. Every one of them
// is made here from the player's own game files, mostly from the line-of-sight map (512 x 512 cells, 0 open, 128
// blocked), so the release ships none of the game's bytes. The one file that ships ready-made is the PhysX mesh in its
// cooked form: only PhysX's own cooker can make that, so it is our data (our vertices and triangles, the walls traced
// from the line-of-sight map, plus the cooker's search tree), used only when the player's map has those walls:
//
//	levels/MM_Map_v3/MM_Map_v3_Player.bfx, _NPC.bfx  the NavPower graphs (navgraph: cells of two, the Guardians'
//	                                                  platforms cut out; SM_Map_v5's graphs give the header values)
//	levels/MM_Map_v3/MM_Map_v3.bin                    the PhysX triangle mesh: a floor and the walls (physmesh),
//	                                                  shipped cooked; the copy's map must be the one it was made from
//	levels/MM_Map_v3/MM_Map_v3.out                    the fog of war's wall outlines (fogmap) and its bushes'
//	                                                  (grassmap)
//	textures/minimap/MM_Map_v3_minimap.dds            the minimap (fogmap's picture, DXT5 with 7 mips)
//	Environments/prototype/mm_v3_terrain.bmdl        the ground model and its two textures (ground: SM_Map_v5's
//	environments/prototype/textures/mm_v3_dirt_D.dds  terrain materials, our own dirt, the game's grass pixels)
//	environments/prototype/textures/mm_v3_grass_D.dds
//	AssetData/Binary/MM_Map_v3_design.Markerset.bin  the map's own design markerset with its two base zones on the
//	                                                  first map's nouns (SwapBaseZones), so the map has an item shop,
//	                                                  and the first map's Guardian death cameras and shop archons
//	                                                  moved onto it (EndMarkers, AppendMarkers)
//	AssetData/Binary/MM_Map_v3.Level.bin             the map's own level (MMLevel): the first map's three end
//	                                                  cinematics (EndScriptsLevel), so a match ends with the victory
//	                                                  view, the tag map's path (EditLevel), so the game loads the
//	                                                  next two, and an energy-line tuning (EnergyLinesLevel), without
//	                                                  which the game shows no Binding's death
//	levels/MM_Map_v3/MM_Map_v3.tags                   the level tag names (the first map's own file) and the tag map
//	images/32bit/MM_Map_v3_Tags.tga                   from the map's markers (tagmap)
//	images/32bit/MM_Map_v3_GrassMap.tga              the bushes' grass map from the map's grass markers (grassmap),
//	                                                  so they hide what stands in them
//
// The bytes equal the developers' Python reference's, pinned by hashes on made-up inputs in each part's tests and,
// on build 854's files, by Game854SHA256.
package mapmm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/mapmm/fogmap"
	"dawngate/server/internal/setup/mapmm/grassmap"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/mapmm/navgraph"
	"dawngate/server/internal/setup/mapmm/physmesh"
	"dawngate/server/internal/setup/mapmm/tagmap"
	"dawngate/server/internal/setup/snap"
)

const (
	File = "zz_MM_Map_v3.snap"
	Rel  = "game/Data/" + File // in a copy
	// Game854SHA256 is the archive Build makes from build 854's files.
	Game854SHA256 = "eed1e14c861eb5a68e315410aedc994d99a11079bfdf16ace1c23e0702b98bbf"
	minimapMips   = 7
)

// Sources are the copy's archives Build reads, in Inputs' order: the line-of-sight map, SM_Map_v5's graphs (the
// template, its level tag names), the grass texture, the terrain model (the template of the ground's materials) and the
// asset data (the design markerset, the Level, the grass markers).
var Sources = []string{"game/Data/" + los.Archive, "game/Data/SM_Map_v5.snap", "game/Data/anshavar3.snap",
	"game/Data/anshavar4.snap", "game/Data/AssetData.snap"}

const (
	smPlayer      = "levels/SM_Map_v5/SM_Map_v5_Player.bfx"
	smNPC         = "levels/SM_Map_v5/SM_Map_v5_NPC.bfx"
	terrainEntry  = "environments/anshavar/sm_v5_terrain.bmdl"
	grassEntry    = "environments/anshavar/textures/grass_seamless_01_D.dds"
	outlinesEntry = "levels/MM_Map_v3/MM_Map_v3.out"
	minimapEntry  = "textures/minimap/MM_Map_v3_minimap.dds"
	// DesignEntry is MM_Map_v3's design markerset, in AssetData.snap and in the archive.
	DesignEntry = "AssetData/Binary/MM_Map_v3_design.Markerset.bin"
	// LevelEntry is MM_Map_v3's level, in AssetData.snap and in the archive.
	LevelEntry    = "AssetData/Binary/MM_Map_v3.Level.bin"
	smLevelEntry  = "AssetData/Binary/SM_Map_v5.Level.bin"
	smDesignEntry = "AssetData/Binary/SM_Map_v5_design.Markerset.bin"
	smTagsEntry   = "levels/SM_Map_v5/SM_Map_v5.tags"
	tagsEntry     = "levels/MM_Map_v3/MM_Map_v3.tags"
	tagImageEntry = "images/32bit/MM_Map_v3_Tags.tga"
)

// smCameraEntries are the first map's two Guardian death cameras (the end cinematics' cameraMortal, cameraSpirit).
var smCameraEntries = []string{"AssetData/Binary/SM_Map_v5_cameraMortal.Markerset.bin",
	"AssetData/Binary/SM_Map_v5_cameraSpirit.Markerset.bin"}

// Inputs are the copy's archives (their whole bytes).
type Inputs struct {
	Images, SMMap, Anshavar3, Anshavar4, AssetData []byte
}

// state is what the generators share: the decoded inputs.
type state struct {
	in    Inputs
	los   los.Image
	grass []grassmap.Patch // the grass markers' patches (grassPatches)
}

// parts are the archive's entries in order, one generator each: a new file is one more line.
var parts = []struct {
	names []string
	make  func(*state) ([][]byte, error)
}{
	{[]string{"levels/MM_Map_v3/MM_Map_v3_Player.bfx", "levels/MM_Map_v3/MM_Map_v3_NPC.bfx"}, makeGraphs},
	{[]string{"levels/MM_Map_v3/MM_Map_v3.bin"}, makeMesh},
	{[]string{outlinesEntry, minimapEntry}, makeFog},
	{[]string{"Environments/prototype/mm_v3_terrain.bmdl", "environments/prototype/textures/mm_v3_dirt_D.dds",
		"environments/prototype/textures/mm_v3_grass_D.dds"}, makeGround},
	{[]string{DesignEntry}, makeBases},
	{[]string{LevelEntry}, makeLevel},
	{[]string{tagsEntry, tagImageEntry}, makeTags},
	{[]string{grassmap.Entry}, makeGrass},
}

// Files makes the archive's entries in order.
func Files(in Inputs) ([]snap.File, error) {
	raw, err := los.FromArchive(in.Images, los.Entry)
	if err != nil {
		return nil, fmt.Errorf("mapmm: %v", err)
	}
	img, err := los.Read(raw)
	if err != nil {
		return nil, fmt.Errorf("mapmm: %v", err)
	}
	st := &state{in: in, los: img}
	var out []snap.File
	for _, p := range parts {
		blobs, err := p.make(st)
		if err != nil {
			return nil, fmt.Errorf("mapmm: %s: %v", p.names[0], err)
		}
		if len(blobs) != len(p.names) {
			return nil, fmt.Errorf("mapmm: %s: %d files made, %d expected", p.names[0], len(blobs), len(p.names))
		}
		for i, b := range blobs {
			out = append(out, snap.File{Name: p.names[i], Data: b})
		}
	}
	return out, nil
}

// Build makes the archive.
func Build(in Inputs) ([]byte, error) {
	files, err := Files(in)
	if err != nil {
		return nil, err
	}
	return snap.Pack(files), nil
}

func makeGraphs(st *state) ([][]byte, error) {
	player, err := los.FromArchive(st.in.SMMap, smPlayer)
	if err != nil {
		return nil, err
	}
	npc, err := los.FromArchive(st.in.SMMap, smNPC)
	if err != nil {
		return nil, err
	}
	p, n, err := navgraph.BuildMM(st.los, player, npc)
	if err != nil {
		return nil, err
	}
	return [][]byte{p, n}, nil
}

func makeMesh(st *state) ([][]byte, error) {
	b, err := physmesh.ForMap(st.los, los.Size, los.Size)
	if err != nil {
		return nil, err
	}
	return [][]byte{b}, nil
}

func makeFog(st *state) ([][]byte, error) {
	f, err := fogmap.NewField(st.los)
	if err != nil {
		return nil, err
	}
	out, err := fogmap.OutlinesFrom(st.los, f, los.Size, los.Size)
	if err != nil {
		return nil, err
	}
	ps, err := grassPatches(st)
	if err != nil {
		return nil, err
	}
	bushes, err := grassmap.Outlines(ps, los.Size)
	if err != nil {
		return nil, err
	}
	if out, err = fogmap.AppendGrass(out, bushes); err != nil {
		return nil, err
	}
	rgba := fogmap.MinimapRGBAFrom(st.los, f, los.Size, los.Size)
	mm, err := dds.Encode(st.los.W, st.los.H, rgba, "dxt5", minimapMips)
	if err != nil {
		return nil, err
	}
	return [][]byte{out, mm}, nil
}

func makeGround(st *state) ([][]byte, error) {
	tmpl, err := los.FromArchive(st.in.Anshavar4, terrainEntry)
	if err != nil {
		return nil, err
	}
	grass, err := los.FromArchive(st.in.Anshavar3, grassEntry)
	if err != nil {
		return nil, err
	}
	model, dirt, g, err := ground.Build(st.los, tmpl, grass)
	if err != nil {
		return nil, err
	}
	return [][]byte{model, dirt, g}, nil
}

// baseSwaps are the base zones' noun names in MM_Map_v3's design markerset and the first map's. The game makes a
// marker its team's base object (the point the item shop's reach is measured from, on the client and the match host)
// only when its noun is a spawn point with a team and carries the catalog tag "baseMarker"; the second map's zones are
// server-only ornaments without it (no shop), the first map's zones are both.
var baseSwaps = [][2]string{
	{"Base_MM_Regen_Defenders.Noun\x00", "Base_Health_Regen_Defenders.Noun\x00"},
	{"Base_MM_Regen_Attackers.Noun\x00", "Base_Health_Regen_Attackers.Noun\x00"},
}

// SwapBaseZones is the design markerset with its two base zones' names and nouns swapped for the first map's: each
// name must occur twice (the marker's name and its noun). The asset data's out-of-line strings are unaligned and read
// in order, so the longer names shift the rest and nothing else changes.
func SwapBaseZones(b []byte) ([]byte, error) {
	for _, s := range baseSwaps {
		if n := bytes.Count(b, []byte(s[0])); n != 2 {
			return nil, fmt.Errorf("the design markerset names %s %d times, 2 expected", s[0][:len(s[0])-1], n)
		}
		b = bytes.ReplaceAll(b, []byte(s[0]), []byte(s[1]))
	}
	return b, nil
}

func makeBases(st *state) ([][]byte, error) {
	design, err := los.FromArchive(st.in.AssetData, DesignEntry)
	if err != nil {
		return nil, err
	}
	smDesign, err := los.FromArchive(st.in.AssetData, smDesignEntry)
	if err != nil {
		return nil, err
	}
	var cams [][]byte
	for _, e := range smCameraEntries {
		c, err := los.FromArchive(st.in.AssetData, e)
		if err != nil {
			return nil, err
		}
		cams = append(cams, c)
	}
	b, err := DesignMarkerset(design, smDesign, cams)
	if err != nil {
		return nil, err
	}
	return [][]byte{b}, nil
}

// The Level asset's file starts with the in-memory struct (0x620 bytes; its paths are 128-byte inline strings, the
// out-of-line data follows), so a path field is written in place and nothing shifts.
const (
	levelStructSize = 0x620
	levelFieldSize  = 128
)

// levelFields are the Level fields the archive sets, one line each: (offset, name, value). Every change of the second
// map's Level asset adds its line here (and in the Python reference's table).
var levelFields = []struct {
	off         int
	name, value string
}{
	{0x3C8, "tagMap", tagImageEntry}, // any non-empty text makes the game load the level's tag map
}

// EditLevel is MM_Map_v3.Level.bin with levelFields set: each field must be empty (all zeros) or hold the value
// already.
func EditLevel(b []byte) ([]byte, error) {
	if len(b) < levelStructSize {
		return nil, fmt.Errorf("the Level asset has %d bytes, at least %#x expected", len(b), levelStructSize)
	}
	out := append([]byte(nil), b...)
	empty := make([]byte, levelFieldSize)
	for _, f := range levelFields {
		if len(f.value) >= levelFieldSize {
			return nil, fmt.Errorf("the Level's %s value is too long", f.name)
		}
		want := make([]byte, levelFieldSize)
		copy(want, f.value)
		have := out[f.off : f.off+levelFieldSize]
		if !bytes.Equal(have, want) && !bytes.Equal(have, empty) {
			return nil, fmt.Errorf("the Level's %s already holds %q", f.name, bytes.TrimRight(have, "\x00"))
		}
		copy(have, want)
	}
	return out, nil
}

// MMLevel is the second map's Level as the archive ships it, every change in one place: the first map's three end
// cinematic names (EndScriptsLevel), then levelFields (EditLevel: the tag map), then the energy-line tuning
// (EnergyLinesLevel).
func MMLevel(mm, sm []byte) ([]byte, error) {
	b, err := EndScriptsLevel(mm, sm)
	if err != nil {
		return nil, err
	}
	if b, err = EditLevel(b); err != nil {
		return nil, err
	}
	return EnergyLinesLevel(b, sm)
}

// The Bindings' death. The game shows a Binding's death (its destroyed state, the explosion and its sound, the
// announcement and the "Binding destroyed" message) from its tower graphics, which start only when the level names an
// energy-line tuning (the Level's energyLineTuning, an asset reference at +0x618); the second map's is null, so its
// Bindings died unseen. The first map's old tuning, SM_Map_v4_EnergyLines, names no line model and no lines, so with it
// the graphics start and no energy line is drawn. The reference is a pointer word (any non-zero value means "present")
// and the asset's name as out-of-line data; it is the Level's last member with out-of-line data, so the name goes at
// the file's end.
const (
	energyLinesOff = 0x618
	// EnergyLines is the energy-line tuning the second map's Level names.
	EnergyLines = "SM_Map_v4_EnergyLines.EnergyLineTuning"
)

// EnergyLinesLevel is the second map's Level naming EnergyLines: the pointer word copied from the first map's Level
// (which names its own tuning there), the name appended.
func EnergyLinesLevel(mm, sm []byte) ([]byte, error) {
	o := energyLinesOff
	if len(mm) < levelStructSize || len(sm) < levelStructSize {
		return nil, fmt.Errorf("a Level asset shorter than its fixed part")
	}
	if binary.LittleEndian.Uint32(mm[o:]) != 0 {
		return nil, fmt.Errorf("the second map's level already names an energy-line tuning")
	}
	if binary.LittleEndian.Uint32(sm[o:]) == 0 {
		return nil, fmt.Errorf("the first map's level names no energy-line tuning")
	}
	out := append([]byte{}, mm[:o]...)
	out = append(out, sm[o:o+4]...)
	out = append(out, mm[o+4:]...)
	return append(append(out, EnergyLines...), 0), nil
}

func makeLevel(st *state) ([][]byte, error) {
	mm, err := los.FromArchive(st.in.AssetData, LevelEntry)
	if err != nil {
		return nil, err
	}
	sm, err := los.FromArchive(st.in.AssetData, smLevelEntry)
	if err != nil {
		return nil, err
	}
	b, err := MMLevel(mm, sm)
	if err != nil {
		return nil, err
	}
	return [][]byte{b}, nil
}

// grassPatches are the second map's bushes, read once from its grass markers.
func grassPatches(st *state) ([]grassmap.Patch, error) {
	if st.grass != nil {
		return st.grass, nil
	}
	set, err := los.FromArchive(st.in.AssetData, grassmap.MarkersetEntry)
	if err != nil {
		return nil, err
	}
	ts, err := assetdata.Embedded()
	if err != nil {
		return nil, err
	}
	ps, err := grassmap.Patches(ts, set, grassmap.Entry)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, fmt.Errorf("%s: no grass markers naming %s", grassmap.MarkersetEntry, grassmap.Entry)
	}
	st.grass = ps
	return ps, nil
}

// makeGrass paints the bushes' grass map (the line-of-sight map's size, as the game asserts).
func makeGrass(st *state) ([][]byte, error) {
	ps, err := grassPatches(st)
	if err != nil {
		return nil, err
	}
	img, err := grassmap.Image(ps, los.Size, st.los.W, st.los.H)
	if err != nil {
		return nil, err
	}
	return [][]byte{img}, nil
}

// makeTags copies the first map's .tags file (the game's 16 tag names and their hashes) and paints the tag map in its
// entry order.
func makeTags(st *state) ([][]byte, error) {
	tags, err := los.FromArchive(st.in.SMMap, smTagsEntry)
	if err != nil {
		return nil, err
	}
	names, err := tagmap.TagNames(tags)
	if err != nil {
		return nil, err
	}
	img, err := tagmap.Image(names, tagmap.MM)
	if err != nil {
		return nil, err
	}
	return [][]byte{tags, img}, nil
}

// The end screen. The game starts the end cinematic its level names (gameEndScriptNameCustom, ...Matchmade,
// ...Practice: three char[128] at +0x448 of the Level asset); the second map's three are empty, so its game goes from
// the end of a match straight back to the lobby, without the victory view or the loot bag. The cinematics' first shot
// is a spline camera of the game level by name (cameraMortal, cameraSpirit), which the first map keeps in two
// markersets of its own and the second map lacks.
const (
	endScriptsLo  = 0x448
	endScriptsHi  = 0x5C8
	markerSize    = 0xC8 // cLabsMarker: markerName char* +0, markerId +4, nounDef +8, pos +0x14
	markersetSize = 0x28 // Markerset: markers {pointer, count} at +0
)

// cameraShifts move the first map's cameras by its Guardians' offset to the second map's: (-166.31, -166.17) minus
// (-182, -182) for cameraMortal, (165.59, 165.63) minus (182, 182) for cameraSpirit. archonShifts move the shop archons
// by the spawn points' offset: the first map's two archons of a team stand on the line across the diagonal through its
// spawn points' centre (their midpoint (-230.6, -230.7), (230.4, 230.7)), so they move to the second map's spawn
// centre, (-206.4, -206.4) and (206.2, 206.1). The base zones' offset (44 each way) put them 20 units toward the
// Guardians (the user's check of 2026-10-23).
var (
	cameraShifts = [][2]float64{{15.69, 15.83}, {-16.41, -16.37}}
	archonPrefix = []byte("itemshoppe_archon_")
	archonShifts = map[string][2]float64{"Mortal": {24.2, 24.3}, "Spirit": {-24.2, -24.6}}
)

// EndScriptsLevel is the second map's level with the first map's three end cinematic names (their 384 bytes copied).
func EndScriptsLevel(mm, sm []byte) ([]byte, error) {
	if len(mm) < endScriptsHi || len(sm) < endScriptsHi {
		return nil, fmt.Errorf("a Level asset shorter than its fixed part")
	}
	for k := endScriptsLo; k < endScriptsHi; k += 128 {
		if mm[k] != 0 {
			return nil, fmt.Errorf("the second map's level already names an end cinematic at +%#x", k)
		}
		if !bytes.HasPrefix(sm[k:k+128], []byte("cinematic_game_end_")) {
			return nil, fmt.Errorf("the first map's level names no end cinematic at +%#x", k)
		}
	}
	out := append([]byte{}, mm[:endScriptsLo]...)
	out = append(out, sm[endScriptsLo:endScriptsHi]...)
	return append(out, mm[endScriptsHi:]...), nil
}

// MarkerAdd is one marker to add: marker Index of the markerset Src, its position moved by Shift (x, y).
type MarkerAdd struct {
	Src   []byte
	Index int
	Shift [2]float64
}

func markerSpans(ts *assetdata.Types, set []byte) (*assetdata.ArraySpans, error) {
	t := ts.Type("Markerset")
	if t == nil || t.Size != markersetSize || len(t.Members) == 0 || t.Members[0].Name != "markers" ||
		t.Members[0].Offset != 0 || t.Members[0].ESize != markerSize {
		return nil, fmt.Errorf("the Markerset type is not laid out as expected")
	}
	return ts.Spans(t, set, "markers")
}

// AppendMarkers is the markerset with more markers at the end of its array: each added marker's fixed bytes with its
// position moved (float32 x + dx, y + dy) and its out-of-line data as it is. The out-of-line data is unaligned and
// walked by a cursor, so the new fixed bytes go after the old ones, their out-of-line data after the old markers', and
// the count at +4 grows; nothing else changes.
func AppendMarkers(ts *assetdata.Types, set []byte, adds []MarkerAdd) ([]byte, error) {
	sp, err := markerSpans(ts, set)
	if err != nil {
		return nil, err
	}
	var fixed, ool []byte
	for _, a := range adds {
		src, err := markerSpans(ts, a.Src)
		if err != nil {
			return nil, err
		}
		if a.Index < 0 || a.Index >= len(src.Elems) {
			return nil, fmt.Errorf("no marker %d in a markerset of %d", a.Index, len(src.Elems))
		}
		e := src.Elems[a.Index]
		el := append([]byte{}, a.Src[e.Off:e.Off+markerSize]...)
		for k := 0; k < 2; k++ {
			v := math.Float32frombits(binary.LittleEndian.Uint32(el[0x14+4*k:]))
			binary.LittleEndian.PutUint32(el[0x14+4*k:], math.Float32bits(float32(float64(v)+a.Shift[k])))
		}
		fixed = append(fixed, el...)
		ool = append(ool, a.Src[e.Start:e.End]...)
	}
	n := len(sp.Elems)
	fixedEnd := markersetSize + n*markerSize
	out := append([]byte{}, set[:markersetSize]...)
	binary.LittleEndian.PutUint32(out[4:], uint32(n+len(adds)))
	out = append(out, set[markersetSize:fixedEnd]...)
	out = append(out, fixed...)
	out = append(out, set[fixedEnd:sp.End]...)
	out = append(out, ool...)
	return append(out, set[sp.End:]...), nil
}

// EndMarkers are the markers the second map's design markerset gets: the first map's two cameras (each its camera and
// its node, in file order) moved to the second map's Guardians, then the first map's shop archons in its design
// markerset's order, moved to the second map's base zones (the purchase effect's source).
func EndMarkers(ts *assetdata.Types, smDesign []byte, cameras [][]byte) ([]MarkerAdd, error) {
	if len(cameras) != len(cameraShifts) {
		return nil, fmt.Errorf("%d camera markersets, %d expected", len(cameras), len(cameraShifts))
	}
	var adds []MarkerAdd
	for k, c := range cameras {
		sp, err := markerSpans(ts, c)
		if err != nil {
			return nil, err
		}
		for i := range sp.Elems {
			adds = append(adds, MarkerAdd{c, i, cameraShifts[k]})
		}
	}
	sp, err := markerSpans(ts, smDesign)
	if err != nil {
		return nil, err
	}
	for i, e := range sp.Elems {
		if binary.LittleEndian.Uint32(smDesign[e.Off:]) == 0 {
			return nil, fmt.Errorf("the first map's design marker %d has no name", i)
		}
		end := bytes.IndexByte(smDesign[e.Start:e.End], 0)
		if end < 0 {
			return nil, fmt.Errorf("the first map's design marker %d: unterminated name", i)
		}
		name := smDesign[e.Start : e.Start+end] // markerName, the marker's first out-of-line string
		if !bytes.HasPrefix(name, archonPrefix) {
			continue
		}
		team, _, _ := bytes.Cut(name[len(archonPrefix):], []byte("."))
		shift, ok := archonShifts[string(team)]
		if !ok {
			return nil, fmt.Errorf("%s: an archon of no known team", name)
		}
		adds = append(adds, MarkerAdd{smDesign, i, shift})
	}
	return adds, nil
}

// DesignMarkerset is the second map's design markerset as the archive ships it: the base zones swapped, then the
// first map's cameras and archons added.
func DesignMarkerset(design, smDesign []byte, cameras [][]byte) ([]byte, error) {
	ts, err := assetdata.Embedded()
	if err != nil {
		return nil, err
	}
	b, err := SwapBaseZones(design)
	if err != nil {
		return nil, err
	}
	adds, err := EndMarkers(ts, smDesign, cameras)
	if err != nil {
		return nil, err
	}
	return AppendMarkers(ts, b, adds)
}
