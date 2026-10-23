package tagmap

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/testgame"
)

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// madeUp is the Python reference's made-up input (its selftest builds the same): another size, other markers, the
// names in reverse order in a made-up .tags file.
func madeUp() (Spec, []string, []byte) {
	s := Spec{SizeX: 500.0, SizeY: 470.0,
		Loci:     []Point{{-200.0, -190.0}, {205.0, 180.5}},
		Bindings: []Point{{-30.25, -50.5}, {-90.0, -100.75}, {35.5, 40.0}, {80.0, 110.25}},
		Inner:    [2]Point{{-90.0, -100.75}, {80.0, 110.25}},
		Wells:    []Point{{-240.5, -230.0}, {230.0, 220.75}}}
	var names []string
	for i := len(Names) - 1; i >= 0; i-- {
		names = append(names, Names[i])
	}
	b := make([]byte, 16+64*numTags)
	copy(b, "sgat")
	binary.LittleEndian.PutUint32(b[4:], 0x10001000)
	binary.LittleEndian.PutUint32(b[8:], numTags)
	for i, n := range names {
		copy(b[16+64*i:], n)
		binary.LittleEndian.PutUint32(b[16+64*i+60:], uint32(0x1000+i))
	}
	return s, names, b
}

// madeUpImageSHA256 is the Python reference's image of madeUp (the same pin on both sides).
const madeUpImageSHA256 = "b246cda50b10294e7a8ee2187bd9f1c89ce02746fa4690c9f82330fb047f0fb5"

func TestTagNames(t *testing.T) {
	_, names, b := madeUp()
	got, err := TagNames(b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join(names, ",") {
		t.Errorf("got %v", got)
	}
	bad := [][]byte{b[:len(b)-1], append([]byte("xgat"), b[4:]...)}
	c := append([]byte(nil), b...)
	binary.LittleEndian.PutUint32(c[8:], 15)
	bad = append(bad, c)
	c = append([]byte(nil), b...)
	copy(c[16:76], make([]byte, 60))
	copy(c[16:], "Nowhere")
	bad = append(bad, c)
	c = append([]byte(nil), b...)
	copy(c[80:144], b[16:80]) // a name twice
	bad = append(bad, c)
	for i, x := range bad {
		if _, err := TagNames(x); err == nil {
			t.Errorf("bad tags file %d was taken", i)
		}
	}
}

func TestImageMadeUp(t *testing.T) {
	s, names, _ := madeUp()
	img, err := Image(names, s)
	if err != nil {
		t.Fatal(err)
	}
	if d := digest(img); d != madeUpImageSHA256 {
		t.Errorf("SHA-256 %s, the reference's %s", d, madeUpImageSHA256)
	}
	for _, c := range []struct {
		x, y          float64
		want, without string
	}{
		{-200, -190, "Base,Locus,MortalSide", "Lane,SpiritSide"},
		{0, 0, "Bottom,Lane,MortalSide,Top", "Base,Locus"},
		{35.5, 40, "BindingRange,Lane,SpiritSide", "Base"},
		{230, 220.75, "Base,SpiritSide,SpiritWell,SpiritWellCenter", "Lane"},
		{230, 200, "Base,SpiritWell", "SpiritWellCenter"},
	} {
		got, err := At(names, img, s, c.x, c.y)
		if err != nil {
			t.Fatal(err)
		}
		has := map[string]bool{}
		for _, n := range got {
			has[n] = true
		}
		for _, n := range strings.Split(c.want, ",") {
			if !has[n] {
				t.Errorf("(%.1f, %.1f): %v lacks %s", c.x, c.y, got, n)
			}
		}
		for _, n := range strings.Split(c.without, ",") {
			if has[n] {
				t.Errorf("(%.1f, %.1f): %v has %s", c.x, c.y, got, n)
			}
		}
	}
	if _, err := Image(names[:15], s); err == nil {
		t.Error("an image for a tags file without SpiritSide")
	}
}

// The game's own .tags file and MM_Map_v3's image (the Python reference's hashes).
const (
	smTagsSHA256  = "f4f8ec2190e3e4fe1d791cc9df0f2c4e045e38f3d8eb8b936e6a54c5cc5497a0"
	mmImageSHA256 = "d0806d411dc1615f6f29d54c0be73403dfdb94beb386700d8130428c0b087038"
	smTagsEntry   = "levels/SM_Map_v5/SM_Map_v5.tags"
	smMapArchive  = "SM_Map_v5.snap"
)

// TestGame: the first map's .tags file from the game and MM_Map_v3's image, with the tags at its markers.
func TestGame(t *testing.T) {
	tags, err := los.FromArchive(testgame.Read(t, smMapArchive), smTagsEntry)
	if err != nil {
		t.Fatal(err)
	}
	if d := digest(tags); d != smTagsSHA256 {
		t.Errorf(".tags SHA-256 %s, %s expected", d, smTagsSHA256)
	}
	names, err := TagNames(tags)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Image(names, MM)
	if err != nil {
		t.Fatal(err)
	}
	if d := digest(img); d != mmImageSHA256 {
		t.Errorf("image SHA-256 %s, the reference's %s", d, mmImageSHA256)
	}
	for _, c := range []struct {
		x, y float64
		want string
	}{
		{-221, -221, "Base,Locus,MortalSide"},                           // the Mortal Locus
		{205, 205, "Base,Locus,SpiritSide"},                             // the Spirit players' start
		{-166.3, -166.3, "Base,MortalSide"},                             // the Mortal Guardian
		{-40.1, -46.2, "BindingRange,Bottom,Lane,MortalSide,Top"},       // Binding_Mortal_Middle
		{0, 0, "Bottom,Lane,MortalSide,Top"},                            // the lane's middle
		{-260.3, -260.3, "Base,MortalSide,SpiritWell,SpiritWellCenter"}, // the blue gold mine
	} {
		got, err := At(names, img, MM, c.x, c.y)
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(got)
		if strings.Join(got, ",") != c.want {
			t.Errorf("(%.1f, %.1f): %v, %s expected", c.x, c.y, got, c.want)
		}
	}
}
