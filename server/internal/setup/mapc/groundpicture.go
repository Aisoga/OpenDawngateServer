package mapc

import (
	"fmt"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/mapc/bake"
)

// The ground's picture (ground.picture, a baked ground's optional member): a painted ground under the layers, one PNG
// in the package per tile of the bake. Each picture is exactly the tile's texture as `mapc bake --full` writes it
// (tile_<i>_<j>.png, i from the west, j from the south): Texels x Texels pixels, north up, its outer
// bake.MarginFor(Texels) pixels on every side the neighbours' ground (the margin that keeps the tiles' edges
// seamless). Where a margin lies over a neighbour tile's own square it is taken from that tile's picture
// (bake.JoinMargins), so a painter paints each place in the tile that owns it; the margins along the ground's outer
// border are the picture's own. Alpha is not read. So a bake exported and imported unchanged bakes the same tiles.
//
// The picture is the ground under the layers: every layer, the first too, is drawn over it through its mask (a
// layer without one would cover it), then the stamps; the cliff material and the decals are as without it. Nothing
// of it is mirrored: team 2's half is painted as the picture has it.

// MaxPictureTileBytes is the most bytes one picture tile's file may have (a 4096 x 4096 RGB PNG of a painted ground
// is about 20 to 50 MB; uncompressed RGBA 64 MB).
const MaxPictureTileBytes = 160 << 20

// pictureTileName is a tile's file name as `mapc bake --full` writes it (and with a suffix: tile_0_1_<hash>.png).
var pictureTileName = regexp.MustCompile(`(?i)(?:^|_)tile_([0-9]+)_([0-9]+)(?:_[A-Za-z0-9-]+)?\.png$`)

// PictureTileFile is the file `mapc bake --full` writes for tile (ti, tj).
func PictureTileFile(ti, tj int) string { return fmt.Sprintf("tile_%d_%d.png", ti, tj) }

// groundPicture runs the picture's rules (structure only: no files).
func (c *checker) groundPicture() {
	m, g := c.m, &c.m.Ground
	if g.Picture == nil || !m.Baked() {
		return // without the baked style ground.baked has said it
	}
	n := m.BakeTiles()
	if len(g.Picture) != n*n {
		c.errorf("ground.picture", "ground.picture", nil,
			"%d pictures for %d x %d tiles: one per tile, west to east, then south to north", len(g.Picture), n, n)
	}
	seen := map[string]int{}
	for k, f := range g.Picture {
		p := fmt.Sprintf("ground.picture[%d]", k)
		if err := PackagePath(f, ".png"); err != nil {
			c.errorf("ground.file", p, nil, "%v", err)
			continue
		}
		if q, ok := seen[strings.ToLower(f)]; ok {
			c.errorf("ground.picture", p, nil, "%s is listed twice (also as picture %d)", f, q)
		}
		seen[strings.ToLower(f)] = k
		if mm := pictureTileName.FindStringSubmatch(path.Base(f)); mm != nil && len(g.Picture) == n*n {
			i, _ := strconv.Atoi(mm[1])
			j, _ := strconv.Atoi(mm[2])
			if i != k%n || j != k/n {
				c.errorf("ground.picture", p, nil, "%s is named for tile (%d, %d) but listed for tile (%d, %d): list "+
					"the tiles west to east, then south to north", f, i, j, k%n, k/n)
			}
		}
	}
}

// groundPictureFiles checks the picture's files (Check, when the map was loaded from its folder): there, a PNG, the
// tile's size. Only the headers are read.
func (c *checker) groundPictureFiles() {
	m := c.m
	t := m.BakeTexels()
	for k, f := range m.Ground.Picture {
		if PackagePath(f, ".png") != nil {
			continue
		}
		p := fmt.Sprintf("ground.picture[%d]", k)
		full := filepath.Join(m.Dir, filepath.FromSlash(f))
		st, err := os.Stat(full)
		if err != nil {
			c.errorf("ground.picture", p, nil, "%s is missing from the package: a ground picture's tiles are big files, "+
				"kept apart from the map's text; copy them into the package (mapc bake --full writes them)", f)
			continue
		}
		if st.Size() > MaxPictureTileBytes {
			c.errorf("ground.picture", p, nil, "%s: %d bytes, at most %d", f, st.Size(), MaxPictureTileBytes)
			continue
		}
		fh, err := os.Open(full)
		if err != nil {
			c.errorf("ground.picture", p, nil, "%s: %v", f, err)
			continue
		}
		cfg, err := png.DecodeConfig(fh)
		fh.Close()
		switch {
		case err != nil:
			c.errorf("ground.picture", p, nil, "%s: not a PNG (%v)", f, err)
		case cfg.Width != t || cfg.Height != t:
			c.errorf("ground.picture", p, nil, "%s: %d x %d pixels, the tile is %d x %d (margins included, as mapc "+
				"bake --full writes it)", f, cfg.Width, cfg.Height, t, t)
		}
	}
}

// groundPicture reads the map's picture as the bake's base (nil without one): the tiles' pixels, opaque, their
// margins joined.
func (cm *Compiled) groundPicture(s *bake.Spec) ([]*bake.Image, error) {
	m := cm.Map
	if len(m.Ground.Picture) == 0 {
		return nil, nil
	}
	if len(m.Ground.Picture) != s.Tiles*s.Tiles {
		return nil, fmt.Errorf("ground.picture: %d pictures for %d x %d tiles", len(m.Ground.Picture), s.Tiles, s.Tiles)
	}
	if m.Dir == "" {
		return nil, fmt.Errorf("ground.picture: the map's package folder is not known")
	}
	px := make([][]byte, len(m.Ground.Picture))
	for k, f := range m.Ground.Picture {
		full := filepath.Join(m.Dir, filepath.FromSlash(f))
		st, err := os.Stat(full)
		if err != nil {
			return nil, fmt.Errorf("ground.picture[%d]: %s is missing from the package", k, f)
		}
		if st.Size() > MaxPictureTileBytes {
			return nil, fmt.Errorf("ground.picture[%d]: %s: %d bytes, at most %d", k, f, st.Size(), MaxPictureTileBytes)
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("ground.picture[%d]: %v", k, err)
		}
		w, h, rgba, err := bake.DecodePNGPixels(b, MaxPicture)
		if err != nil {
			return nil, fmt.Errorf("ground.picture[%d]: %s: %v", k, f, err)
		}
		if w != s.Texels || h != s.Texels {
			return nil, fmt.Errorf("ground.picture[%d]: %s: %d x %d pixels, the tile is %d x %d", k, f, w, h, s.Texels, s.Texels)
		}
		for o := 3; o < len(rgba); o += 4 {
			rgba[o] = 255
		}
		px[k] = rgba
	}
	if err := bake.JoinMargins(s.Tiles, s.Texels, s.Margin, px); err != nil {
		return nil, err
	}
	out := make([]*bake.Image, len(px))
	for k := range px {
		im, err := bake.NewImage(s.Texels, s.Texels, px[k])
		if err != nil {
			return nil, err
		}
		out[k] = im
	}
	return out, nil
}

// PictureTiles are the bake's tiles as RGBA (k = tj*Tiles + ti), as the archive's textures hold them: what `mapc
// bake --full` writes and the picture reads back.
func PictureTiles(s *bake.Spec) ([][]byte, error) {
	out := make([][]byte, 0, s.Tiles*s.Tiles)
	for tj := 0; tj < s.Tiles; tj++ {
		for ti := 0; ti < s.Tiles; ti++ {
			px, err := s.Tile(ti, tj)
			if err != nil {
				return nil, err
			}
			out = append(out, px)
		}
	}
	return out, nil
}
