package grassmap

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/mapmm/fogmap"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/testgame"
)

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// madeUp is the Python reference's made-up input (its selftest builds the same): three patches on a 64 x 48 map of a
// level 100 units square, the second with its corners swapped, the third beside the first (their rings meet).
var madeUp = []Patch{
	{"path1_Made_Up", [2]float64{0.20, 0.30}, [2]float64{0.35, 0.52}},
	{"PATH2_made_up", [2]float64{0.72, 0.62}, [2]float64{0.60, 0.55}},
	{"path3_Made_Up", [2]float64{0.36, 0.33}, [2]float64{0.45, 0.45}},
}

// The Python reference's results on madeUp (the same pins on both sides): the grass map and a one-square terrain
// outline file with the three grass outlines appended.
const (
	madeUpImageSHA256 = "b59d5e9af2225ed8d9e9805a2b07feeac53cefa74b72f0dcc5d541eeba359f49"
	madeUpOutSHA256   = "21100205b5febb9a6b929dac3e23854fa503073880ab6d464060583cc18e92ef"
)

func TestAngles(t *testing.T) {
	for k := 0; k < 24; k++ {
		a := float64(k) * math.Pi / 12
		if math.Abs(cos24[k]-math.Cos(a)) > 1e-15 || math.Abs(sin24(k)-math.Sin(a)) > 1e-15 {
			t.Errorf("angle %d: cos %v sin %v", k, cos24[k], sin24(k))
		}
	}
}

// TestID: the first map's patches' ids (its .out's grass outline ids and the match host's grass ids) are FNV-1 of the
// outline names in lower case.
func TestID(t *testing.T) {
	for name, want := range map[string]uint32{"path9043_SM_Map_v5": 0x16982688, "path8971_SM_Map_v5": 0x486A198D,
		"path6376_MM_Map_v3": 0x87CE2122} {
		if got, err := ID(name); err != nil || got != want {
			t.Errorf("%s: %08X %v, %08X expected", name, got, err, want)
		}
	}
	if _, err := ID("pfad\xc3\xa4"); err == nil {
		t.Error("a name that isn't ASCII was taken")
	}
}

func TestMadeUp(t *testing.T) {
	img, err := Image(madeUp, 100, 64, 48)
	if err != nil {
		t.Fatal(err)
	}
	if d := digest(img); d != madeUpImageSHA256 {
		t.Errorf("image SHA-256 %s, the reference's %s", d, madeUpImageSHA256)
	}
	vals, err := cellValues(madeUp, 100, 64, 48)
	if err != nil {
		t.Fatal(err)
	}
	solid := 0
	for _, v := range vals {
		if v>>24 != 0 {
			solid++
		}
	}
	if solid != 170 || len(vals)-solid != 94 {
		t.Errorf("%d solid and %d ring cells, 170 and 94 expected", solid, len(vals)-solid)
	}
	square, err := fogmap.Encode([]fogmap.Outline{{Points: []fogmap.Point{{X: -10, Y: -10}, {X: 10, Y: -10}, {X: 10, Y: 10},
		{X: -10, Y: 10}}}})
	if err != nil {
		t.Fatal(err)
	}
	outs, err := Outlines(madeUp, 100)
	if err != nil {
		t.Fatal(err)
	}
	file, err := fogmap.AppendGrass(square, outs)
	if err != nil {
		t.Fatal(err)
	}
	if d := digest(file); d != madeUpOutSHA256 {
		t.Errorf(".out SHA-256 %s, the reference's %s", d, madeUpOutSHA256)
	}
	if binary.LittleEndian.Uint32(file[4:]) != 1 || binary.LittleEndian.Uint16(file[10:]) != 3 {
		t.Errorf("header %x", file[:16])
	}
	if _, err := Image([]Patch{{"x", [2]float64{0.5, 0.5}, [2]float64{0.5, 0.6}}}, 100, 64, 48); err == nil {
		t.Error("an empty rectangle was taken")
	}
}

// The second map's grass map from build 854's grass markers (the Python reference's hash).
const mmImageSHA256 = "b7191ccd6dc7245b250649080c3b0730f22ca7455a9a92ea0884ae7477ca3c08"

// TestGame: the second map's four patches from the game's markers, their ids, and the grass map.
func TestGame(t *testing.T) {
	set, err := los.FromArchive(testgame.Read(t, "AssetData.snap"), MarkersetEntry)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := assetdata.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	ps, err := Patches(ts, set, Entry)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]uint32{"path6376_MM_Map_v3": 0x87CE2122, "path6371_MM_Map_v3": 0x6642AEF5,
		"path6607_MM_Map_v3": 0x549964B7, "path6609_MM_Map_v3": 0x6B1A9B15}
	if len(ps) != len(want) {
		t.Fatalf("%d patches, %d expected", len(ps), len(want))
	}
	for _, p := range ps {
		id, _ := ID(p.Name)
		if id != want[p.Name] {
			t.Errorf("%s: id %08X", p.Name, id)
		}
	}
	x0, y0, x1, y1 := ps[0].Rect(los.Size)
	if math.Abs(x1-x0-15.30468) > 1e-2 || math.Abs(y1-y0-21.71892) > 1e-2 || math.Abs((x0+x1)/2+90.942) > 1e-2 ||
		math.Abs((y0+y1)/2+62.402) > 1e-2 {
		t.Errorf("%s: (%.3f, %.3f) .. (%.3f, %.3f), the marker's size and position expected", ps[0].Name, x0, y0, x1, y1)
	}
	img, err := Image(ps, los.Size, 512, 512)
	if err != nil {
		t.Fatal(err)
	}
	if d := digest(img); d != mmImageSHA256 {
		t.Errorf("image SHA-256 %s, the reference's %s", d, mmImageSHA256)
	}
}
