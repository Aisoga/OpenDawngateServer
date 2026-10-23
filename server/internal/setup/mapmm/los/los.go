// Package los reads MM_Map_v3's line-of-sight map, the one game input every generator of zz_MM_Map_v3.snap starts
// from (the navigation graphs, the PhysX mesh, the fog outlines, the minimap and the ground).
//
// The map ships in the player's images.snap as images/8bit/MM_Map_v3_LineOfSight.tga: 512 x 512 cells, one byte
// each (0 open, 128 blocked), rows bottom to top. Its cells span the Level's levelSize, 580.478 m square.
package los

import (
	"encoding/binary"
	"fmt"
	"strings"

	"dawngate/server/internal/setup/snap"
)

const (
	// Level is the map's name; Size its Level asset's levelSize (both sides, metres).
	Level = "MM_Map_v3"
	Size  = 580.478
	// Archive and Entry: where the map ships (an archive of the copy's game/Data and the path inside it).
	Archive = "images.snap"
	Entry   = "images/8bit/MM_Map_v3_LineOfSight.tga"
)

// Image is a decoded uncompressed TGA (as the Python reference reads it): the header's size and depth and the pixel bytes
// as stored (rows bottom to top for the game's maps).
type Image struct {
	W, H, Bpp int
	Pix       []byte
}

// ReadTGA decodes an uncompressed true-colour or grey TGA (image types 2 and 3, no colour map), as the Python
// reference does: the pixels are the w * h * bpp/8 bytes after the header and the image id.
func ReadTGA(b []byte) (Image, error) {
	if len(b) < 18 {
		return Image{}, fmt.Errorf("los: a TGA has at least 18 bytes, this one %d", len(b))
	}
	idlen, cmtype, imtype := int(b[0]), b[1], b[2]
	if cmtype != 0 || (imtype != 2 && imtype != 3) {
		return Image{}, fmt.Errorf("los: unsupported TGA (colour map %d, type %d)", cmtype, imtype)
	}
	w, h := int(binary.LittleEndian.Uint16(b[12:])), int(binary.LittleEndian.Uint16(b[14:]))
	bpp := int(b[16])
	off, n := 18+idlen, w*h*(bpp/8)
	if off+n > len(b) {
		return Image{}, fmt.Errorf("los: the TGA's pixels are cut (%d of %d bytes)", len(b)-off, n)
	}
	return Image{W: w, H: h, Bpp: bpp, Pix: b[off : off+n]}, nil
}

// FromArchive reads the entry name (any case) out of a .snap archive's bytes.
func FromArchive(archive []byte, name string) ([]byte, error) {
	a, err := snap.Parse(archive)
	if err != nil {
		return nil, err
	}
	for _, e := range a.Entries {
		if strings.EqualFold(e.Name, name) {
			return a.Read(e)
		}
	}
	return nil, fmt.Errorf("los: the archive has no %s", name)
}

// Read decodes a line-of-sight map: an 8-bit TGA.
func Read(b []byte) (Image, error) {
	img, err := ReadTGA(b)
	if err != nil {
		return img, err
	}
	if img.Bpp != 8 {
		return img, fmt.Errorf("los: an 8-bit line-of-sight map is expected, this one has %d bits per pixel", img.Bpp)
	}
	return img, nil
}
