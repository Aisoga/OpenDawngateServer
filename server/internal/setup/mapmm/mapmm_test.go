package mapmm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/mapmm/fogmap/synthlos"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func gameInputs(t *testing.T) Inputs {
	t.Helper()
	read := func(rel string) []byte { return testgame.Read(t, filepath.Base(rel)) }
	return Inputs{Images: read(Sources[0]), SMMap: read(Sources[1]), Anshavar3: read(Sources[2]),
		Anshavar4: read(Sources[3]), AssetData: read(Sources[4])}
}

// TestGame builds the whole archive from build 854's files (the shipped PhysX mesh in it) and compares it with the pin
// (the Python reference's archive).
func TestGame(t *testing.T) {
	in := gameInputs(t)
	t0 := time.Now()
	b, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d bytes in %v", len(b), time.Since(t0))
	if got := digest(b); got != Game854SHA256 {
		t.Errorf("SHA-256 %s, the reference's %s", got, Game854SHA256)
	}
	a, err := snap.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range parts {
		names = append(names, p.names...)
	}
	if len(a.Entries) != len(names) {
		t.Fatalf("%d entries, %d expected", len(a.Entries), len(names))
	}
	for i, e := range a.Entries {
		if e.Name != names[i] {
			t.Errorf("entry %d: %s, %s expected", i, e.Name, names[i])
		}
	}
}

// TestOtherMap: a line-of-sight map with other walls than the game's gets no mesh (the shipped one isn't its), so no
// archive.
func TestOtherMap(t *testing.T) {
	st := &state{los: synthlos.Map(77, 45)}
	if _, err := makeMesh(st); err == nil {
		t.Error("the shipped mesh for another map")
	}
}

// swapMadeUpSHA256 is the Python reference's result on TestSwapBaseZones' input (the same pin on both sides).
const swapMadeUpSHA256 = "f094c4676d2fdec5e2637e9267ee6d1ae31d169bebbb6df92e84a42dc24606b6"

// TestSwapBaseZones on a made-up markerset: each name twice, the strings longer, the rest shifted unchanged; a
// markerset naming a zone once is refused.
func TestSwapBaseZones(t *testing.T) {
	in := []byte("\x01\x00\x00\x00Base_MM_Regen_Defenders.Noun\x00Base_MM_Regen_Defenders.Noun\x00\x00\x00\x00\x00" +
		"Base_MM_Regen_Attackers.Noun\x00Base_MM_Regen_Attackers.Noun\x00tail\x00")
	got, err := SwapBaseZones(in)
	if err != nil {
		t.Fatal(err)
	}
	want := "\x01\x00\x00\x00Base_Health_Regen_Defenders.Noun\x00Base_Health_Regen_Defenders.Noun\x00\x00\x00\x00\x00" +
		"Base_Health_Regen_Attackers.Noun\x00Base_Health_Regen_Attackers.Noun\x00tail\x00"
	if string(got) != want {
		t.Errorf("got %q", got)
	}
	if d := digest(got); d != swapMadeUpSHA256 {
		t.Errorf("SHA-256 %s, the reference's %s", d, swapMadeUpSHA256)
	}
	if _, err := SwapBaseZones(in[:40]); err == nil {
		t.Error("a markerset naming the zones once was taken")
	}
}

// editLevelMadeUpSHA256 is the Python reference's result on TestEditLevel's input (the same pin on both sides).
const editLevelMadeUpSHA256 = "776569ed2b70a6e65459029ba7e188e102e8060f6567b93fb4b3c65b9c3fe386"

// TestEditLevel on a made-up Level: the fields written in place (nothing shifts), a second edit changes nothing, a
// field holding other text or a short file is refused.
func TestEditLevel(t *testing.T) {
	in := make([]byte, levelStructSize+37)
	for i := range in {
		in[i] = byte((i*7 + 3) % 256)
	}
	for _, f := range levelFields {
		copy(in[f.off:f.off+levelFieldSize], make([]byte, levelFieldSize))
	}
	got, err := EditLevel(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(in) || string(got[0x3C8:0x3C8+len(tagImageEntry)+1]) != tagImageEntry+"\x00" {
		t.Errorf("the tagMap field holds %q", got[0x3C8:0x3C8+40])
	}
	if d := digest(got); d != editLevelMadeUpSHA256 {
		t.Errorf("SHA-256 %s, the reference's %s", d, editLevelMadeUpSHA256)
	}
	again, err := EditLevel(got)
	if err != nil || string(again) != string(got) {
		t.Errorf("a second edit: %v", err)
	}
	other := append([]byte(nil), in...)
	copy(other[0x3C8:], "images/32bit/Other.tga")
	if _, err := EditLevel(other); err == nil {
		t.Error("a tagMap holding other text was overwritten")
	}
	if _, err := EditLevel(in[:levelStructSize-1]); err == nil {
		t.Error("a short Level was taken")
	}
}

// mmLevelMadeUpSHA256 is the Python reference's Level with both edits on TestEndMadeUp's made-up pair.
const mmLevelMadeUpSHA256 = "e5e2b986a316d7b6ea5fc7d43c5f5384f5268d2b689eecbbfdcd2f57d23771fe"

// TestMMLevelMadeUp: the archive's one Level edit is the end cinematics, then levelFields.
func TestMMLevelMadeUp(t *testing.T) {
	mm := append(make([]byte, 0x620), "tail"...)
	sm := make([]byte, 0x620)
	for k, s := range []string{"custom", "matchmade", "practice"} {
		copy(sm[endScriptsLo+128*k:], "cinematic_game_end_"+s)
	}
	sm = append(sm, "other"...)
	got, err := MMLevel(mm, sm)
	if err != nil {
		t.Fatal(err)
	}
	end, err := EndScriptsLevel(mm, sm)
	if err != nil {
		t.Fatal(err)
	}
	want, err := EditLevel(end)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("MMLevel is not EditLevel(EndScriptsLevel)")
	}
	if d := digest(got); d != mmLevelMadeUpSHA256 {
		t.Errorf("SHA-256 %s, the reference's %s", d, mmLevelMadeUpSHA256)
	}
}

// The Python reference's results on TestEndMadeUp's inputs (the same pins on both sides).
const (
	endDesignMadeUpSHA256 = "f21515f81798a93d133c234a99c3448226984c550bb28a2b887d4e76d804af39"
	endLevelMadeUpSHA256  = "d723c3cb38692ea6ad5945bea041ff01ee4aa30b29126504fdf353102f628ef2"
)

type madeUpMarker struct {
	name     string
	id       uint32
	noun     string
	pos      [3]float32
	children []uint32
}

// madeUpMarkerset lays out a Markerset as the asset data does: the markers' fixed bytes (every other member zero, so
// no other out-of-line data), their names, nouns and child ids, then the condition array.
func madeUpMarkerset(markers []madeUpMarker, condition []uint32) []byte {
	le := binary.LittleEndian
	head := make([]byte, markersetSize)
	le.PutUint32(head[0:], 1)
	le.PutUint32(head[4:], uint32(len(markers)))
	le.PutUint32(head[0x18:], math.Float32bits(1))
	if len(condition) > 0 {
		le.PutUint32(head[0x20:], 1)
	}
	le.PutUint32(head[0x24:], uint32(len(condition)))
	var fixed, ool []byte
	for _, m := range markers {
		el := make([]byte, markerSize)
		le.PutUint32(el[0:], 1)
		le.PutUint32(el[4:], m.id)
		le.PutUint32(el[8:], 1)
		for k, v := range m.pos {
			le.PutUint32(el[0x14+4*k:], math.Float32bits(v))
		}
		le.PutUint32(el[0x2C:], math.Float32bits(1))
		if len(m.children) > 0 {
			le.PutUint32(el[0x58:], 1)
		}
		le.PutUint32(el[0x5C:], uint32(len(m.children)))
		fixed = append(fixed, el...)
		ool = append(ool, m.name+"\x00"+m.noun+"\x00"...)
		for _, c := range m.children {
			ool = le.AppendUint32(ool, c)
		}
	}
	out := append(append(head, fixed...), ool...)
	for _, c := range condition {
		out = le.AppendUint32(out, c)
	}
	return out
}

// TestEndMadeUp: the design markerset (the base zones swapped, two cameras of two markers each and the two archons
// among three markers added, moved) and the level (the three end cinematic names copied) on made-up inputs, pinned by
// the Python reference's hashes; levels that already name a cinematic, name none or are short are refused.
func TestEndMadeUp(t *testing.T) {
	design := madeUpMarkerset([]madeUpMarker{
		{"Base_MM_Regen_Defenders.Noun", 1, "Base_MM_Regen_Defenders.Noun", [3]float32{-206, -206, 0.088}, nil},
		{"Base_MM_Regen_Attackers.Noun", 2, "Base_MM_Regen_Attackers.Noun", [3]float32{206, 206, 0.088}, []uint32{1}},
		{"SpawnPoint_Team1.Noun-1", 3, "SpawnPoint_Team1.Noun", [3]float32{-203.171, -203.198, 0.088}, nil},
	}, []uint32{1})
	smDesign := madeUpMarkerset([]madeUpMarker{
		{"itemshoppe_archon_Mortal.Noun-1", 10, "itemshoppe_archon_Mortal.Noun", [3]float32{-252.686, -207.827, 5.066}, nil},
		{"rock.Noun-7", 11, "rock.Noun", [3]float32{1.5, 2.5, 0}, []uint32{10, 12}},
		{"itemshoppe_archon_Spirit.Noun-1", 12, "itemshoppe_archon_Spirit.Noun", [3]float32{252.726, 208.682, 5.057}, nil},
	}, nil)
	cams := [][]byte{
		madeUpMarkerset([]madeUpMarker{
			{"SplineCamera.Noun", 20, "SplineCamera.Noun", [3]float32{-157.8354, -200.2609, 56.35353}, nil},
			{"SplineCameraNode.Noun", 21, "SplineCameraNode.Noun", [3]float32{-147.8081, -151.0914, 36.64431}, nil},
		}, nil),
		madeUpMarkerset([]madeUpMarker{
			{"SplineCamera.Noun-1", 22, "SplineCamera.Noun", [3]float32{177.291, 141.552, 57.61}, nil},
			{"SplineCameraNode.Noun-1", 23, "SplineCameraNode.Noun", [3]float32{154.124, 148.608, 33.383}, nil},
		}, nil),
	}
	got, err := DesignMarkerset(design, smDesign, cams)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := assetdata.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	sp, err := markerSpans(ts, got)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uint32
	for _, e := range sp.Elems {
		ids = append(ids, binary.LittleEndian.Uint32(got[e.Off+4:]))
	}
	if want := []uint32{1, 2, 3, 20, 21, 22, 23, 10, 12}; !slices.Equal(ids, want) {
		t.Errorf("marker ids %v, %v expected", ids, want)
	}
	if !bytes.Equal(got[sp.End:], design[len(design)-4:]) {
		t.Errorf("the condition array moved: %x", got[sp.End:])
	}
	if x := math.Float32frombits(binary.LittleEndian.Uint32(got[sp.Elems[7].Off+0x14:])); x != float32(float64(float32(-252.686))+24.2) {
		t.Errorf("the Mortal archon's x %v", x)
	}
	if d := digest(got); d != endDesignMadeUpSHA256 {
		t.Errorf("design SHA-256 %s, the reference's %s", d, endDesignMadeUpSHA256)
	}

	mm := append(make([]byte, 0x620), "tail"...)
	sm := make([]byte, 0x620)
	for k, s := range []string{"custom", "matchmade", "practice"} {
		copy(sm[endScriptsLo+128*k:], "cinematic_game_end_"+s)
	}
	sm = append(sm, "other"...)
	lvl, err := EndScriptsLevel(mm, sm)
	if err != nil {
		t.Fatal(err)
	}
	if string(lvl[0x4C8:0x4C8+28]) != "cinematic_game_end_matchmade" || len(lvl) != len(mm) {
		t.Errorf("level %q", lvl[0x4C8:0x4C8+28])
	}
	if d := digest(lvl); d != endLevelMadeUpSHA256 {
		t.Errorf("level SHA-256 %s, the reference's %s", d, endLevelMadeUpSHA256)
	}
	for _, bad := range [][2][]byte{{sm, sm}, {mm, mm}, {mm[:0x500], sm}} {
		if _, err := EndScriptsLevel(bad[0], bad[1]); err == nil {
			t.Error("a bad level pair was taken")
		}
	}
}
