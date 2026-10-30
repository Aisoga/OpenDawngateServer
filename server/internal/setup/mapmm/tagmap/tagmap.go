// Package tagmap paints MM_Map_v3's level tag map: the regions the game's scripts and the bots' target check ask
// about (which side, the bases, the Locus, a Binding's range, the lane, the gold mines), from the map's markers.
//
// The game loads a level's tag map, when its Level asset names one, from two files: levels/<level>/<level>.tags (the
// header "sgat", the count 16 at offset 8, then 16 entries of a 60-byte name and a 32-bit hash) and
// images/32bit/<level>_Tags.tga (512 x 512, 32 bits a pixel, rows bottom-up). A position (x, y) reads the pixel at
// ((x / sizeX + 0.5) * 511, (y / sizeY + 0.5) * 511); the .tags file's entry i is the pixel's red byte, bit i, for
// i < 8, and its green byte, bit i - 8, otherwise. The second map ships neither file, so no tag ever matches there.
// The rules are measured on the first map's own tag map:
//
//	MortalSide, SpiritSide   the pixel anti-diagonal: mx + my <= 510 is the Mortal side
//	Base                     behind the line x + y through each side's Inner Binding
//	Lane, Top, Bottom        everything else (the map's one lane is both of the first map's lanes)
//	Locus                    40 units around each Locus
//	BindingRange             31 units around each Binding
//	SpiritWell, ...Center    33 and 13 units around each gold mine
//
// and none of the jungle, camp and parasite tags (the map has none). A compiled map's spec adds its lanes' bands and
// its camps (jungle.go): Lane, Top and Bottom only in the bands, Jungle and the camp tags off them. The .tags file is the first map's own, copied
// from the player's game files; the image's bits follow its entry order. The bytes equal the developers' Python
// reference's, pinned by hashes on made-up inputs in the tests.
package tagmap

import (
	"encoding/binary"
	"fmt"
)

const (
	// Size is the image's width and height (the game asserts 512 x 512 x 32).
	Size    = 512
	numTags = 16
)

// Names are the game's level tags (nLevelTags), the names a .tags file may hold.
var Names = []string{"MortalSide", "SpiritSide", "Base", "Locus", "Lane", "Jungle", "SpiritWell", "SpiritWellCenter",
	"GreenBuff", "BlueBuff", "OrangeBuff", "MoneyPigs", "BindingRange", "Bottom", "Top", "Parasite"}

// Point is a marker's position in world units.
type Point struct{ X, Y float64 }

// Spec is a map's size and the markers the rules use.
type Spec struct {
	SizeX, SizeY float64
	Loci         []Point
	Bindings     []Point
	Inner        [2]Point // the Mortal side's Inner Binding, the Spirit side's
	Wells        []Point
	// TwoLanes splits the ground outside the bases into the Top lane (y - x >= 0, the host's LaneByDiagonal) and the
	// Bottom lane; without it a unit there is in both (a map with one lane).
	TwoLanes bool

	// A compiled map's lanes and camps (none: the rules above, MM_Map_v3's). With Lanes, Lane is only the ground within
	// a lane's band, Top lane 1's band and Bottom lane 2's (with one lane both), and Jungle the rest outside the bases;
	// each Disc's tag goes on the Jungle within its radius (the first map's camp tags never touch a lane or a base).
	Lanes []LaneBand
	Discs []Disc
}

// LaneBand is a lane's band: the ground within Half of its knots' polyline.
type LaneBand struct {
	ID    int // 1 Top, 2 Bottom
	Half  float64
	Knots []Point
}

// Disc is a camp's tag (GreenBuff, BlueBuff, OrangeBuff, MoneyPigs, Parasite) round its place.
type Disc struct {
	Tag    string
	Centre Point
	R      float64
}

// MM is MM_Map_v3: its levelSize and the markers of its towers and followers markersets.
var MM = Spec{
	SizeX: 580.478, SizeY: 580.478,
	Loci: []Point{{-221.0, -221.0}, {221.0, 221.0}},
	Bindings: []Point{{-40.06894, -46.16639}, {-86.54458, -108.8412}, {40.06894, 46.16639},
		{86.54458, 108.8412}},
	Inner: [2]Point{{-86.54458, -108.8412}, {86.54458, 108.8412}},
	Wells: []Point{{-260.2915, -260.2576}, {260.5175, 260.441}},
}

// The disc radii (world units), the best fits to the first map's tag map.
const (
	locusRadius      = 40.0
	bindingRadius    = 31.0
	wellRadius       = 33.0
	wellCenterRadius = 13.0
)

// header is the image's TGA header: uncompressed true colour, 512 x 512, 32 bits, 8 alpha bits, bottom-up.
var header = []byte{0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 2, 32, 8}

// TagNames reads a .tags file's 16 names in entry order: the header, the count, the size and known, distinct names.
func TagNames(b []byte) ([]string, error) {
	if len(b) != 16+64*numTags || string(b[:4]) != "sgat" {
		return nil, fmt.Errorf("not a level tags file (%d bytes)", len(b))
	}
	if n := binary.LittleEndian.Uint32(b[8:]); n != numTags {
		return nil, fmt.Errorf("the tags file names %d tags, not %d", n, numTags)
	}
	known := map[string]bool{}
	for _, n := range Names {
		known[n] = true
	}
	var names []string
	for i := 0; i < numTags; i++ {
		e := b[16+64*i : 16+64*i+60]
		end := 0
		for end < len(e) && e[end] != 0 {
			end++
		}
		n := string(e[:end])
		if !known[n] {
			return nil, fmt.Errorf("the tags file's entry %d is %q", i, n)
		}
		known[n] = false
		names = append(names, n)
	}
	return names, nil
}

// centre is the world coordinate of pixel m's centre (the game maps x to int((x / size + 0.5) * 511)).
func centre(m int, size float64) float64 {
	t := (float64(m)+0.5)/511.0 - 0.5
	return float64(t * size)
}

func inDisc(x, y float64, pts []Point, r float64) bool {
	r2 := float64(r * r)
	for _, p := range pts {
		dx := x - p.X
		dy := y - p.Y
		if float64(dx*dx)+float64(dy*dy) <= r2 {
			return true
		}
	}
	return false
}

// pixelTags are the tags pixel (mx, my) carries.
func pixelTags(mx, my int, s Spec) []string {
	x := centre(mx, s.SizeX)
	y := centre(my, s.SizeY)
	var out []string
	if mx+my <= 510 {
		out = append(out, "MortalSide")
	} else {
		out = append(out, "SpiritSide")
	}
	u := x + y
	if u <= s.Inner[0].X+s.Inner[0].Y || u >= s.Inner[1].X+s.Inner[1].Y {
		out = append(out, "Base")
	} else if len(s.Lanes) > 0 {
		out = append(out, laneTags(x, y, s)...)
	} else {
		switch {
		case !s.TwoLanes:
			out = append(out, "Lane", "Top", "Bottom")
		case y-x >= 0:
			out = append(out, "Lane", "Top")
		default:
			out = append(out, "Lane", "Bottom")
		}
	}
	if inDisc(x, y, s.Loci, locusRadius) {
		out = append(out, "Locus")
	}
	if inDisc(x, y, s.Bindings, bindingRadius) {
		out = append(out, "BindingRange")
	}
	if inDisc(x, y, s.Wells, wellRadius) {
		out = append(out, "SpiritWell")
	}
	if inDisc(x, y, s.Wells, wellCenterRadius) {
		out = append(out, "SpiritWellCenter")
	}
	return out
}

// Image is the tag map's TGA for a .tags file's entry order (names, as TagNames reads them).
func Image(names []string, s Spec) ([]byte, error) {
	type slot struct {
		byteOff int
		bit     byte
	}
	where := map[string]slot{}
	for i, n := range names {
		if i < 8 {
			where[n] = slot{2, 1 << uint(i)}
		} else {
			where[n] = slot{1, 1 << uint(i-8)}
		}
	}
	out := make([]byte, len(header)+Size*Size*4)
	copy(out, header)
	px := out[len(header):]
	for my := 0; my < Size; my++ {
		for mx := 0; mx < Size; mx++ {
			o := 4 * (my*Size + mx)
			for _, n := range pixelTags(mx, my, s) {
				w, ok := where[n]
				if !ok {
					return nil, fmt.Errorf("the tags file has no %s", n)
				}
				px[o+w.byteOff] |= w.bit
			}
		}
	}
	return out, nil
}

// At is the tags the game finds at world (x, y) in an image made for names (its clamp and truncation).
func At(names []string, img []byte, s Spec, x, y float64) ([]string, error) {
	if len(img) != len(header)+Size*Size*4 || string(img[:len(header)]) != string(header) {
		return nil, fmt.Errorf("not a 512 x 512 x 32 tag image")
	}
	clamp := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	mx := int(clamp(x/s.SizeX+0.5) * 511)
	my := int(clamp(y/s.SizeY+0.5) * 511)
	o := len(header) + 4*(my*Size+mx)
	v := uint32(img[o+2]) | uint32(img[o+1])<<8
	var got []string
	for i, n := range names {
		if v>>uint(i)&1 != 0 {
			got = append(got, n)
		}
	}
	return got, nil
}
