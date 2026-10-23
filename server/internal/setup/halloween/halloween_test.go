package halloween

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"reflect"
	"strings"
	"testing"

	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// madeUpLevel is a Level (re/scripts/test_halloween.py builds the same bytes): the fixed part zero but the markersets
// array {0x02000000, 2} and a byte at +0x61C, two pointer words, two names, then a made-up string.
func madeUpLevel() []byte {
	le := binary.LittleEndian
	b := make([]byte, levelSize)
	le.PutUint32(b[0:], 0x02000000)
	le.PutUint32(b[4:], 2)
	b[0x61C] = 1
	b = le.AppendUint32(le.AppendUint32(b, 0x02000010), 0x02000030)
	return append(b, "Map_one.Markerset\x00Map_two.Markerset\x00music_of_the_map\x00"...)
}

// madeUpMarkerset is a Markerset: markers {0x02000100, 1} with one made-up 8-byte marker, the group Key "none" (text
// pointer at +0x14), weight 1.0, no condition; then the marker and the group's text.
func madeUpMarkerset() []byte {
	le := binary.LittleEndian
	b := make([]byte, markersetSize)
	le.PutUint32(b[0x00:], 0x02000100)
	le.PutUint32(b[0x04:], 1)
	le.PutUint32(b[0x08:], 0x2CA33BDB)
	le.PutUint32(b[0x14:], 0x02000200)
	le.PutUint32(b[0x18:], math.Float32bits(1))
	return append(b, "MARKER01none\x00"...)
}

// MadeUp is the made-up AssetData.snap: the level, another file and the markerset. Nothing in it comes from the game.
func MadeUp() []byte {
	return snap.Pack([]snap.File{{Name: binPath(Level), Data: madeUpLevel()},
		{Name: "AssetData/Binary/other.Noun.bin", Data: []byte("not a level\x00")},
		{Name: binPath(Markerset), Data: madeUpMarkerset()}})
}

const (
	madeUpSHA     = "97143ec0d0b2a032d2281868543b7be8b9f4c075c0384af1d90a445677a3acda"
	madeUpArchive = "154b03df45c699539c6e31ba66a4643829615209436c7eb20962464be43ccb6c"
	game854       = "c9cc70a55cda03144e49c2d2c84206bb9ad6ee81d2532f409d61fa2e5eda65fa" // from build 854's AssetData.snap
)

func TestBuildMatchesPython(t *testing.T) {
	in := MadeUp()
	if got := digest(in); got != madeUpSHA {
		t.Errorf("the made-up AssetData.snap: SHA-256 %s, test_halloween.py's %s", got, madeUpSHA)
	}
	out, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := digest(out); got != madeUpArchive {
		t.Errorf("the archive: SHA-256 %s, halloween.py's %s", got, madeUpArchive)
	}
	checkArchive(t, madeUpLevel(), madeUpMarkerset(), out)
}

// checkArchive reads the archive back: the level lists the old markersets plus the holiday one with every other byte
// in place, and the markerset has the condition [0] and is otherwise unchanged.
func checkArchive(t *testing.T, levelIn, msIn, out []byte) {
	t.Helper()
	a, err := snap.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Entries) != 2 || a.Entries[0].Name != binPath(Level) || a.Entries[1].Name != binPath(Markerset) {
		t.Fatalf("the archive's entries: %+v", a.Entries)
	}
	level, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	ms, err := a.Read(a.Entries[1])
	if err != nil {
		t.Fatal(err)
	}
	old, err := MarkersetNames(levelIn)
	if err != nil {
		t.Fatal(err)
	}
	got, err := MarkersetNames(level)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, append(append([]string(nil), old...), Markerset)) {
		t.Errorf("the level's markersets: %v", got)
	}
	if len(level) != len(levelIn)+4+len(Markerset)+1 || !bytes.Equal(level[8:levelSize], levelIn[8:levelSize]) {
		t.Errorf("the level: %d bytes from %d, or its fixed part changed", len(level), len(levelIn))
	}
	namesEnd := levelSize + 4*len(old)
	for _, n := range old {
		namesEnd += len(n) + 1
	}
	if !bytes.HasSuffix(level, levelIn[namesEnd:]) {
		t.Error("the level's data after the names changed")
	}
	le := binary.LittleEndian
	if !bytes.Equal(ms[:0x20], msIn[:0x20]) || !bytes.Equal(ms[markersetSize:len(msIn)], msIn[markersetSize:]) ||
		le.Uint32(ms[0x20:]) != le.Uint32(msIn[0:]) || le.Uint32(ms[0x24:]) != 1 || le.Uint32(ms[len(msIn):]) != Condition ||
		len(ms) != len(msIn)+4 {
		t.Error("the markerset isn't the old one with the condition [0]")
	}
}

func TestBuildRefuses(t *testing.T) {
	twice, err := AddMarkerset(madeUpLevel(), Markerset)
	if err != nil {
		t.Fatal(err)
	}
	cond, err := SetCondition(madeUpMarkerset(), []uint32{0})
	if err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"listed twice":   second(AddMarkerset(twice, Markerset)),
		"a cut level":    second(AddMarkerset(madeUpLevel()[:0x100], Markerset)),
		"cut names":      second(AddMarkerset(madeUpLevel()[:levelSize+8+5], Markerset)),
		"a condition":    second(SetCondition(cond, []uint32{0})),
		"a cut set":      second(SetCondition(madeUpMarkerset()[:0x20], []uint32{0})),
		"no markerset":   second(Build(snap.Pack([]snap.File{{Name: binPath(Level), Data: madeUpLevel()}}))),
		"not an archive": second(Build([]byte("no snap"))),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func second(_ []byte, err error) error { return err }

// Build 854's AssetData.snap (skipped without the game files).
func TestBuildGame(t *testing.T) {
	in := testgame.Read(t, "AssetData.snap")
	out, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := digest(out); got != game854 {
		t.Errorf("SHA-256 %s, halloween.py's %s", got, game854)
	}
	a, err := snap.Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	var level, ms []byte
	for _, e := range a.Entries {
		switch {
		case strings.EqualFold(e.Name, binPath(Level)):
			level, _ = a.Read(e)
		case strings.EqualFold(e.Name, binPath(Markerset)):
			ms, _ = a.Read(e)
		}
	}
	checkArchive(t, level, ms, out)
}
