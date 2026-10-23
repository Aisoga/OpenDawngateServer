package fogmap

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
	"time"

	"dawngate/server/internal/setup/mapmm/fogmap/synthlos"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/testgame"
)

// The Python reference's hashes of the made-up maps (synthlos) and of MM_Map_v3's own map.
const (
	synth200Outlines = "6b65807eaef833c01960cc88faf583ead6aa6790df085a1ff01ba68a4ddd6097"
	synth512Outlines = "3d62b3a00b38aea034769989e5d59a0f122a76b7d00fd4a12faebbcfbc3b4e00"
	synth512Minimap  = "c689f940e70e1693c9ae322b557ec2ac37190449c544ac27c4f369011056fe18"
	gameOutlines     = "fbedf8c2bebef55c80840403e2d469313cc4d42940a32b01b168ee3af9a581d7" // MM_Map_v3.out, 30,304 bytes
	gameMinimap      = "bd288edaee374eeec95076f57db61c6b27872c4b2896e3cbca4d38687e83ce2c" // the RGBA its .dds compresses
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestSynthOutlines(t *testing.T) {
	img := synthlos.Map(200, 168)
	start := time.Now()
	out, err := Outlines(img, 211.375, 183.9)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("200 x 168: %d bytes in %v", len(out), time.Since(start))
	if got := sum(out); got != synth200Outlines {
		t.Errorf("200 x 168 outlines: sha256 %s, the Python reference's %s", got, synth200Outlines)
	}
	checkFile(t, out)
}

func TestSynthMinimapAndOutlines512(t *testing.T) {
	img := synthlos.Map(512, 512)
	start := time.Now()
	f, err := NewField(img)
	if err != nil {
		t.Fatal(err)
	}
	out, err := OutlinesFrom(img, f, los.Size, los.Size)
	if err != nil {
		t.Fatal(err)
	}
	rgba := MinimapRGBAFrom(img, f, los.Size, los.Size)
	t.Logf("512 x 512: outlines %d bytes, minimap %d bytes in %v", len(out), len(rgba), time.Since(start))
	if got := sum(out); got != synth512Outlines {
		t.Errorf("512 x 512 outlines: sha256 %s, the Python reference's %s", got, synth512Outlines)
	}
	if got := sum(rgba); got != synth512Minimap {
		t.Errorf("512 x 512 minimap: sha256 %s, the Python reference's %s", got, synth512Minimap)
	}
	checkFile(t, out)
}

// checkFile reads an outline file back: the header, every outline's strip and its bounding box.
func checkFile(t *testing.T, b []byte) {
	t.Helper()
	le := binary.LittleEndian
	if string(b[:4]) != "ltuo" || le.Uint16(b[8:]) != 0x0300 || le.Uint16(b[10:]) != 0 {
		t.Fatalf("header % x", b[:16])
	}
	n, off := int(le.Uint32(b[4:])), 16
	f32 := func(o int) float32 { return math.Float32frombits(le.Uint32(b[o:])) }
	for k := 0; k < n; k++ {
		vb := int(le.Uint32(b[off+32:]))
		if vb%4 != 0 || vb < 12 {
			t.Fatalf("outline %d: %d vertices", k, vb)
		}
		v := off + 36
		// p0 twice at z 0, then p0 at z 0 and z 1; the strip ends with p0 twice at z 0
		zs := [5]float32{f32(v + 8), f32(v + 20), f32(v + 32), f32(v + 44), f32(v + 12*vb - 4)}
		if zs != [5]float32{0, 0, 0, 1, 0} {
			t.Fatalf("outline %d: the strip's z %v", k, zs)
		}
		off = v + 12*vb
	}
	if off != len(b) {
		t.Errorf("%d bytes left over", len(b)-off)
	}
}

// TestHypot: the Python reference's math.hypot on inputs where math.Hypot is an ulp off (x, y, result as bits).
func TestHypot(t *testing.T) {
	for _, c := range [][3]uint64{
		{0xc026fc4da9bb73c0, 0xc0680709086f5c5c, 0x40681205174d0bab},
		{0x403c4f9e14b19750, 0xc0678240ad7ee5cc, 0x4067c60f1b64125f},
		{0x404cfc9ffc031290, 0x40644c959eb86a9e, 0x40658dcc48ed6961},
		{0x4048df3d2d050768, 0x405c8b7fc7233304, 0x405f22e20f882be6},
		{0x405edb3efbaea810, 0x4041b62592346b60, 0x40600d11052f00ac},
		{0xc032d795d8d29690, 0x405f0910160dfa14, 0x405f640e8cb44aa9},
	} {
		x, y := math.Float64frombits(c[0]), math.Float64frombits(c[1])
		if got := math.Float64bits(hypot(x, y)); got != c[2] {
			t.Errorf("hypot(%v, %v) = %#x, the reference %#x", x, y, got, c[2])
		}
	}
	if hypot(3, 4) != 5 || hypot(0, -2) != 2 || hypot(0, 0) != 0 || !math.IsInf(hypot(math.Inf(-1), math.NaN()), 1) {
		t.Error("special cases")
	}
}

func TestEncode(t *testing.T) {
	b, err := Encode([]Outline{
		{Points: []Point{{0, 0}, {2, 0}, {2, 1}}},
		{Grass: 0x12345678, Points: []Point{{0, 0}, {4, 0}, {4, 3}, {0, 3}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	if le.Uint32(b[4:]) != 1 || le.Uint16(b[10:]) != 1 {
		t.Fatalf("header % x", b[:16])
	}
	// 3 points: 2 * 4 + 4 vertices; 4 points: 2 * 6 + 4
	if got := le.Uint32(b[16+32:]); got != 12 {
		t.Errorf("3 points: %d vertices", got)
	}
	g := 16 + 36 + 12*12
	if le.Uint32(b[g:]) != 0x12345678 || le.Uint32(b[g+4+32:]) != 16 {
		t.Errorf("grass outline % x", b[g:g+40])
	}
	if r := math.Float32frombits(le.Uint32(b[g+4+12:])); r != 2.5 {
		t.Errorf("radius %v, 2.5 expected", r)
	}
	if _, err := Encode([]Outline{{Grass: 1, Points: []Point{{0, 0}, {1, 0}, {1, 1}}}, {Points: []Point{{0, 0}, {1, 0}, {1, 1}}}}); err == nil {
		t.Error("a terrain outline after a grass outline was accepted")
	}
	if _, err := Encode([]Outline{{Points: []Point{{0, 0}, {1, 0}, {1, 0}}}}); err == nil {
		t.Error("a repeated point was accepted")
	}
}

// TestGame makes MM_Map_v3's outlines and minimap picture from the game's own line-of-sight map.
func TestGame(t *testing.T) {
	raw, err := los.FromArchive(testgame.Read(t, los.Archive), los.Entry)
	if err != nil {
		t.Fatal(err)
	}
	img, err := los.Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	f, err := NewField(img)
	if err != nil {
		t.Fatal(err)
	}
	out, err := OutlinesFrom(img, f, los.Size, los.Size)
	if err != nil {
		t.Fatal(err)
	}
	rgba := MinimapRGBAFrom(img, f, los.Size, los.Size)
	t.Logf("outlines %d bytes, minimap %d bytes in %v", len(out), len(rgba), time.Since(start))
	if got := sum(out); got != gameOutlines {
		t.Errorf("outlines: sha256 %s, the reference %s", got, gameOutlines)
	}
	if got := sum(rgba); got != gameMinimap {
		t.Errorf("minimap: sha256 %s, the reference %s", got, gameMinimap)
	}
}
