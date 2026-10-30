package mapc

import (
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/mapc/bake"
	"dawngate/server/internal/setup/mapmm/fogmap"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/snap"
)

// The baked ground (ground style "baked"): the ground model's texture painted from above out of layers and stamps,
// as the first map's ground is painted, instead of the tiled dirt and grass. A layer is a tileable texture drawn
// through a mask (the automatic rules, shapes as the layout's, and a painted picture); a stamp is a picture laid onto
// the ground. The bake is cut into tiles, each one texture the model maps from above; a cliff material can take the
// steep faces from the side. The minimap is the bake, shrunk, over the layout's colours. Decals (both styles) are
// the engine's own projected pictures, placed as markers.

// GroundBake is the ground section's members besides the style. Every one is optional; the defaults are the
// constants below and, without layers, DefaultLayers.
type GroundBake struct {
	Texels  int           `json:"texels,omitempty"`  // a tile's texture side: 1024, 2048 (default) or 4096
	Tiles   int           `json:"tiles,omitempty"`   // tiles per side: 1, 2 (default) or 4
	Layers  []GroundLayer `json:"layers,omitempty"`  // bottom first; the first is the ground under all others
	Stamps  []GroundStamp `json:"stamps,omitempty"`  // laid over the layers in order
	Cliff   *GroundCliff  `json:"cliff,omitempty"`   // the steep faces' own material (none: they keep the bake)
	Minimap string        `json:"minimap,omitempty"` // "bake" (default for the baked style) or "flat"
	Decals  []GroundDecal `json:"decals,omitempty"`  // the engine's decals (both styles)

	// Picture is a painted ground under the layers (groundpicture.go): one PNG in the package per tile, each exactly
	// the tile's texture (Texels x Texels, as `mapc bake --full` writes it), listed west to east, then south to north.
	Picture []string `json:"picture,omitempty"`
}

// TextureRef names a picture: exactly one of a shipped texture (Game, a .dds path in the copy's archive Archive,
// read from the player's copy at build time), a PNG in the package (File, a path below the package's folder), a
// procedural texture ("dirt": the tiled style's earth) or one colour (RGB 0..255).
type TextureRef struct {
	Game       string      `json:"game,omitempty"`
	Archive    string      `json:"archive,omitempty"`
	File       string      `json:"file,omitempty"`
	Procedural string      `json:"procedural,omitempty"`
	RGB        *[3]float64 `json:"rgb,omitempty"`
}

// GroundLayer is one layer: its texture repeated every Scale metres (turned Rot degrees), times Tint, its brightness
// varied by Vary; drawn at Opacity where its mask is. The mask is the automatic rule Auto, then the picture Image
// (grey 0..255 over the level's square, north up), then the shapes in order (an Erase shape takes away); Soft metres
// soften every edge of the rule and the shapes, Noise metres break it up. Mirror (default true) draws every mask
// part with its point mirror too.
type GroundLayer struct {
	Name    string      `json:"name,omitempty"`
	Texture TextureRef  `json:"texture"`
	Scale   float64     `json:"scale,omitempty"`
	Rot     float64     `json:"rot,omitempty"`
	Tint    *[3]float64 `json:"tint,omitempty"`
	Vary    float64     `json:"vary,omitempty"`
	Break   float64     `json:"break,omitempty"` // 0..1: a second, larger, turned copy breaks up the repeats
	Opacity *float64    `json:"opacity,omitempty"`
	Auto    string      `json:"auto,omitempty"`
	Slope   float64     `json:"slope,omitempty"` // Auto "slope": the steepness (degrees) where the layer is half drawn
	Image   string      `json:"image,omitempty"`
	Shapes  []MaskShape `json:"shapes,omitempty"`
	Soft    *float64    `json:"soft,omitempty"`
	Noise   float64     `json:"noise,omitempty"`
	Mirror  *bool       `json:"mirror,omitempty"`
}

// MaskShape is a mask's shape: one geometry as the layout's shapes have (a lane id's corridor, at Width when given,
// else the lane's own; a circle; a polygon; a path with a Width), drawn or, with Erase, taken away; Soft (metres,
// when given) softens this shape's edge instead of the layer's Soft (a brush stroke's own softness).
type MaskShape struct {
	Lane    int      `json:"lane,omitempty"`
	Circle  *Point   `json:"circle,omitempty"`
	R       float64  `json:"r,omitempty"`
	Polygon []Point  `json:"polygon,omitempty"`
	Path    []Point  `json:"path,omitempty"`
	Width   float64  `json:"width,omitempty"`
	Soft    *float64 `json:"soft,omitempty"`
	Erase   bool     `json:"erase,omitempty"`
}

// GroundStamp is a picture laid onto the ground: Image (Rect, a pixel rectangle x, y, w, h of it), centred at Pos,
// Size metres (along its own x and y), turned Rot degrees counter-clockwise (its top towards north at 0), its alpha
// times Opacity, Soft metres of its border faded (Round: an ellipse); Mirror (default true) lays the point mirror too.
type GroundStamp struct {
	Image   TextureRef  `json:"image"`
	Rect    *[4]int     `json:"rect,omitempty"`
	Pos     Point       `json:"pos"`
	Size    [2]float64  `json:"size"`
	Rot     float64     `json:"rot,omitempty"`
	Opacity *float64    `json:"opacity,omitempty"`
	Tint    *[3]float64 `json:"tint,omitempty"`
	Soft    float64     `json:"soft,omitempty"`
	Round   bool        `json:"round,omitempty"`
	Mirror  *bool       `json:"mirror,omitempty"`
}

// GroundCliff is the steep faces' material: a tileable texture mapped from the side, every Scale metres, on the
// faces steeper than Slope degrees.
type GroundCliff struct {
	Texture TextureRef  `json:"texture"`
	Scale   float64     `json:"scale,omitempty"`
	Slope   float64     `json:"slope,omitempty"`
	Tint    *[3]float64 `json:"tint,omitempty"`
}

// GroundDecal is one of the engine's decals: a shipped texture (Diffuse, a path as the game's files name it)
// projected into a box Size (width, length, depth in metres) centred at Pos and height Z, turned Rot degrees; Mirror
// (default true) places the point mirror too.
type GroundDecal struct {
	Diffuse string      `json:"diffuse"`
	Pos     Point       `json:"pos"`
	Z       float64     `json:"z,omitempty"`
	Size    [3]float64  `json:"size"`
	Rot     float64     `json:"rot,omitempty"`
	Opacity *float64    `json:"opacity,omitempty"`
	Tint    *[3]float64 `json:"tint,omitempty"`
	Mirror  *bool       `json:"mirror,omitempty"`
}

// The defaults and the limits.
const (
	DefaultTexels   = 2048
	DefaultTiles    = 2
	DefaultScale    = 16.0 // metres per repeat
	DefaultSoft     = 2.0  // metres
	DefaultSlope    = 35.0 // degrees (Auto "slope")
	DefaultCliffDeg = 50.0
	DefaultCliffM   = 10.0
	MaxLayers       = 12
	MaxStamps       = 64
	MaxDecals       = 32
	MaxPicture      = 4096 // a package picture's largest side
	maxFileBytes    = 64 << 20
)

// The shipped textures DefaultLayers name.
const (
	EarthArchive = "anshavar3.snap"
	GrassPainted = "environments/anshavar/textures/OutsouceGroundMap02_D.dds"
)

// DefaultLayers are the baked style's layers when a map names none: the tiled style's earth everywhere and a painted
// grass on the high ground, with a soft, broken edge.
func DefaultLayers() []GroundLayer {
	soft := 3.0
	return []GroundLayer{
		{Name: "earth", Texture: TextureRef{Procedural: "dirt"}, Scale: 18},
		{Name: "grass", Texture: TextureRef{Game: GrassPainted, Archive: EarthArchive}, Scale: 24, Auto: "high",
			Soft: &soft, Noise: 2},
	}
}

var (
	archiveRe = regexp.MustCompile(`^[A-Za-z0-9_]+\.snap$`)
	autoRules = []string{"all", "floor", "high", "slope", "chasm", "bushes"}
)

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

func floatOr(f *float64, def float64) float64 {
	if f == nil {
		return def
	}
	return *f
}

func tintOr(t *[3]float64) [3]float64 {
	if t == nil {
		return [3]float64{1, 1, 1}
	}
	return *t
}

// Baked reports whether the map's ground is baked.
func (m *Map) Baked() bool { return m.Ground.Style == "baked" }

// BakeTexels and BakeTiles are the bake's tile size and count with the defaults.
func (m *Map) BakeTexels() int {
	if m.Ground.Texels == 0 {
		return DefaultTexels
	}
	return m.Ground.Texels
}

// BakeTiles is the bake's tiles per side.
func (m *Map) BakeTiles() int {
	if m.Ground.Tiles == 0 {
		return DefaultTiles
	}
	return m.Ground.Tiles
}

// BakeLayers are the map's layers or, without layers and without a ground picture, DefaultLayers.
func (m *Map) BakeLayers() []GroundLayer {
	if len(m.Ground.Layers) == 0 && len(m.Ground.Picture) == 0 {
		return DefaultLayers()
	}
	return m.Ground.Layers
}

// PackagePath checks a path a map names in its package: relative, with forward slashes, inside the folder, clean.
func PackagePath(p, ext string) error {
	switch {
	case p == "" || strings.Contains(p, `\`) || path.IsAbs(p) || filepath.IsAbs(p) || strings.Contains(p, ":"):
		return fmt.Errorf("%q: a relative path with / inside the package", p)
	case path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../"):
		return fmt.Errorf("%q: a clean path inside the package", p)
	case !strings.EqualFold(path.Ext(p), ext):
		return fmt.Errorf("%q: a %s file", p, ext)
	}
	return nil
}

// ---------------------------------------------------------------- the rules

func (c *checker) tint(p string, t *[3]float64) {
	if t == nil {
		return
	}
	for _, v := range t {
		if !(v >= 0 && v <= 4) {
			c.errorf("ground.tint", p, nil, "tint %v: each 0 to 4", *t)
			return
		}
	}
}

func (c *checker) unit(rule, p string, v *float64) {
	if v != nil && !(*v >= 0 && *v <= 1) {
		c.errorf(rule, p, nil, "%g: 0 to 1", *v)
	}
}

func (c *checker) textureRef(p string, t TextureRef, flat bool) {
	n := 0
	for _, set := range []bool{t.Game != "", t.File != "", t.Procedural != "", t.RGB != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		c.errorf("ground.texture", p, nil, "a texture needs exactly one of game, file, procedural or rgb (it has %d)", n)
		return
	}
	switch {
	case t.Game != "":
		if !archiveRe.MatchString(t.Archive) || strings.HasPrefix(strings.ToLower(t.Archive), "zz_") {
			c.errorf("ground.texture", p+".archive", nil, "archive %q: a shipped archive's file name (<name>.snap)", t.Archive)
		}
		if !strings.EqualFold(path.Ext(t.Game), ".dds") || strings.Contains(t.Game, `\`) {
			c.errorf("ground.texture", p+".game", nil, "game %q: a .dds path with /", t.Game)
		}
	case t.Archive != "":
		c.errorf("ground.texture", p+".archive", nil, "archive goes with game")
	case t.File != "":
		if err := PackagePath(t.File, ".png"); err != nil {
			c.errorf("ground.file", p+".file", nil, "%v", err)
		}
	case t.Procedural != "":
		if !flat || t.Procedural != "dirt" {
			c.errorf("ground.texture", p+".procedural", nil, "procedural %q: \"dirt\" (layers and the cliff only)", t.Procedural)
		}
	default:
		if !flat {
			c.errorf("ground.texture", p+".rgb", nil, "rgb: layers and the cliff only")
		}
		for _, v := range t.RGB {
			if !(v >= 0 && v <= 255) {
				c.errorf("ground.texture", p+".rgb", nil, "rgb %v: each 0 to 255", *t.RGB)
				break
			}
		}
	}
}

// ground runs the ground section's rules (structure: no rasterising, no files).
func (c *checker) ground() {
	m, g := c.m, &c.m.Ground
	switch g.Style {
	case "tiled", "baked":
	default:
		c.errorf("ground.style", "ground.style", nil, "ground style %q: \"tiled\" or \"baked\"", g.Style)
		return
	}
	if !m.Baked() {
		if g.Texels != 0 || g.Tiles != 0 || g.Layers != nil || g.Stamps != nil || g.Cliff != nil || g.Minimap != "" ||
			g.Picture != nil {
			c.errorf("ground.baked", "ground", nil, "texels, tiles, layers, stamps, cliff, minimap and picture go with the style \"baked\"")
		}
	}
	c.groundPicture() // groundpicture.go
	if g.Texels != 0 && g.Texels != 1024 && g.Texels != 2048 && g.Texels != 4096 {
		c.errorf("ground.texels", "ground.texels", nil, "texels %d: 1024, 2048 or 4096", g.Texels)
	}
	if g.Tiles != 0 && g.Tiles != 1 && g.Tiles != 2 && g.Tiles != 4 {
		c.errorf("ground.tiles", "ground.tiles", nil, "tiles %d: 1, 2 or 4", g.Tiles)
	}
	if g.Minimap != "" && g.Minimap != "bake" && g.Minimap != "flat" {
		c.errorf("ground.minimap", "ground.minimap", nil, "minimap %q: \"bake\" or \"flat\"", g.Minimap)
	}
	if len(g.Layers) > MaxLayers {
		c.errorf("ground.layers", "ground.layers", nil, "%d layers, at most %d", len(g.Layers), MaxLayers)
	}
	for k, l := range g.Layers {
		p := fmt.Sprintf("ground.layers[%d]", k)
		c.textureRef(p+".texture", l.Texture, true)
		if l.Scale != 0 && !(l.Scale >= 0.5 && l.Scale <= 500) {
			c.errorf("ground.scale", p+".scale", nil, "scale %g: 0.5 to 500 metres", l.Scale)
		}
		c.tint(p+".tint", l.Tint)
		c.unit("ground.opacity", p+".opacity", l.Opacity)
		if !(l.Vary >= 0 && l.Vary <= 1) {
			c.errorf("ground.vary", p+".vary", nil, "vary %g: 0 to 1", l.Vary)
		}
		if !(l.Break >= 0 && l.Break <= 1) {
			c.errorf("ground.vary", p+".break", nil, "break %g: 0 to 1", l.Break)
		}
		if l.Auto != "" && !inList(l.Auto, autoRules, false) {
			c.errorf("ground.auto", p+".auto", nil, "auto %q: one of %s", l.Auto, strings.Join(autoRules, ", "))
		}
		if l.Slope != 0 && !(l.Slope >= 5 && l.Slope <= 85) {
			c.errorf("ground.slope", p+".slope", nil, "slope %g: 5 to 85 degrees", l.Slope)
		}
		if l.Image != "" {
			if err := PackagePath(l.Image, ".png"); err != nil {
				c.errorf("ground.file", p+".image", nil, "%v", err)
			}
		}
		if l.Soft != nil && !(*l.Soft >= 0 && *l.Soft <= 50) {
			c.errorf("ground.soft", p+".soft", nil, "soft %g: 0 to 50 metres", *l.Soft)
		}
		if !(l.Noise >= 0 && l.Noise <= 20) {
			c.errorf("ground.noise", p+".noise", nil, "noise %g: 0 to 20 metres", l.Noise)
		}
		for q, s := range l.Shapes {
			if _, err := maskGeom(m, s); err != nil {
				c.errorf("ground.shape", fmt.Sprintf("%s.shapes[%d]", p, q), nil, "%v", err)
			}
			if s.Soft != nil && !(*s.Soft >= 0 && *s.Soft <= 50) {
				c.errorf("ground.soft", fmt.Sprintf("%s.shapes[%d].soft", p, q), nil, "soft %g: 0 to 50 metres", *s.Soft)
			}
		}
		masked := l.Auto != "" || l.Image != "" || len(l.Shapes) > 0
		switch {
		case len(g.Picture) > 0 && !masked: // over a ground picture every layer is drawn through its mask
			c.errorf("ground.mask", p, nil, "a layer over the ground's picture needs a mask (auto, image or shapes)")
		case len(g.Picture) > 0:
		case k == 0 && masked:
			c.warnf("ground.base-mask", p, nil, "the first layer lies under all others; its mask is not read")
		case k > 0 && !masked:
			c.errorf("ground.mask", p, nil, "a layer above the first needs a mask (auto, image or shapes)")
		}
	}
	if len(g.Stamps) > MaxStamps {
		c.errorf("ground.stamps", "ground.stamps", nil, "%d stamps, at most %d", len(g.Stamps), MaxStamps)
	}
	for k, s := range g.Stamps {
		p := fmt.Sprintf("ground.stamps[%d]", k)
		c.textureRef(p+".image", s.Image, false)
		if s.Rect != nil && (s.Rect[0] < 0 || s.Rect[1] < 0 || s.Rect[2] <= 0 || s.Rect[3] <= 0) {
			c.errorf("ground.rect", p+".rect", nil, "rect %v: x, y >= 0, w, h > 0", *s.Rect)
		}
		if !(s.Size[0] >= 0.5 && s.Size[0] <= MaxSize && s.Size[1] >= 0.5 && s.Size[1] <= MaxSize) {
			c.errorf("ground.size", p+".size", pt(s.Pos), "size %v: each 0.5 to %g metres", s.Size, MaxSize)
		}
		c.unit("ground.opacity", p+".opacity", s.Opacity)
		c.tint(p+".tint", s.Tint)
		if !(s.Soft >= 0 && s.Soft <= 100) {
			c.errorf("ground.soft", p+".soft", pt(s.Pos), "soft %g: 0 to 100 metres", s.Soft)
		}
	}
	if cl := g.Cliff; cl != nil {
		c.textureRef("ground.cliff.texture", cl.Texture, true)
		if cl.Scale != 0 && !(cl.Scale >= 6 && cl.Scale <= 100) {
			c.errorf("ground.cliff", "ground.cliff.scale", nil, "scale %g: 6 to 100 metres (a face must span less than a repeat)", cl.Scale)
		}
		if cl.Slope != 0 && !(cl.Slope >= 20 && cl.Slope <= 80) {
			c.errorf("ground.cliff", "ground.cliff.slope", nil, "slope %g: 20 to 80 degrees", cl.Slope)
		}
		c.tint("ground.cliff.tint", cl.Tint)
	}
	if len(g.Decals) > MaxDecals {
		c.errorf("ground.decals", "ground.decals", nil, "%d decals, at most %d", len(g.Decals), MaxDecals)
	}
	for k, d := range g.Decals {
		p := fmt.Sprintf("ground.decals[%d]", k)
		if !strings.EqualFold(path.Ext(strings.ReplaceAll(d.Diffuse, `\`, "/")), ".dds") {
			c.errorf("ground.decal", p+".diffuse", pt(d.Pos), "diffuse %q: a shipped .dds path", d.Diffuse)
		}
		for _, v := range d.Size {
			if !(v >= 0.25 && v <= 100) {
				c.errorf("ground.decal", p+".size", pt(d.Pos), "size %v: each 0.25 to 100 metres", d.Size)
				break
			}
		}
		if !(d.Z >= -50 && d.Z <= 50) {
			c.errorf("ground.decal", p+".z", pt(d.Pos), "z %g: -50 to 50 metres", d.Z)
		}
		c.unit("ground.opacity", p+".opacity", d.Opacity)
		c.tint(p+".tint", d.Tint)
	}
}

// groundFiles checks that the package's pictures the ground names are there and read (Check, when the map was
// loaded from its folder).
func (c *checker) groundFiles() {
	m := c.m
	if m.Dir == "" || !m.Baked() {
		return
	}
	try := func(p, file string) {
		if PackagePath(file, ".png") != nil {
			return
		}
		if _, err := readPicture(m, file); err != nil {
			c.errorf("ground.file", p, nil, "%v", err)
		}
	}
	for k, l := range m.Ground.Layers {
		if l.Texture.File != "" {
			try(fmt.Sprintf("ground.layers[%d].texture.file", k), l.Texture.File)
		}
		if l.Image != "" {
			try(fmt.Sprintf("ground.layers[%d].image", k), l.Image)
		}
	}
	for k, s := range m.Ground.Stamps {
		if s.Image.File != "" {
			try(fmt.Sprintf("ground.stamps[%d].image.file", k), s.Image.File)
		}
	}
	if cl := m.Ground.Cliff; cl != nil && cl.Texture.File != "" {
		try("ground.cliff.texture.file", cl.Texture.File)
	}
	c.groundPictureFiles() // groundpicture.go
}

// readPicture reads a package's PNG.
func readPicture(m *Map, file string) (*bake.Image, error) {
	if m.Dir == "" {
		return nil, fmt.Errorf("%s: the map's package folder is not known", file)
	}
	full := filepath.Join(m.Dir, filepath.FromSlash(file))
	st, err := os.Stat(full)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", file, err)
	}
	if st.Size() > maxFileBytes {
		return nil, fmt.Errorf("%s: %d bytes, at most %d", file, st.Size(), maxFileBytes)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	img, err := bake.DecodePNG(b, MaxPicture)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", file, err)
	}
	return img, nil
}

// ---------------------------------------------------------------- the masks' geometry

func maskGeom(m *Map, s MaskShape) (shapeGeom, error) {
	g, err := geom(m, Shape{Type: "open", Lane: s.Lane, Circle: s.Circle, R: s.R, Polygon: s.Polygon, Path: s.Path,
		Width: s.Width})
	if err != nil {
		return g, err
	}
	if s.Lane != 0 && s.Width != 0 {
		if !(s.Width > 0) {
			return g, fmt.Errorf("a width > 0")
		}
		g.half = s.Width / 2
	}
	return g, nil
}

// box is the geometry's bounding box (x0, y0, x1, y1) grown by reach metres (and a path's half width): farther out
// the signed distance exceeds reach.
func (g shapeGeom) box(reach float64) [4]float64 {
	b := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	add := func(p Point, r float64) {
		b[0], b[1] = math.Min(b[0], p[0]-r), math.Min(b[1], p[1]-r)
		b[2], b[3] = math.Max(b[2], p[0]+r), math.Max(b[3], p[1]+r)
	}
	switch {
	case g.circle != nil:
		add(*g.circle, g.r+reach)
	case g.polygon != nil:
		for _, p := range g.polygon {
			add(p, reach)
		}
	default:
		for _, p := range g.path {
			add(p, g.half+reach)
		}
	}
	return b
}

// segDistExact is segDist with every product rounded (no fused arithmetic).
func segDistExact(p, a, b Point) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	ll := float64(dx*dx) + float64(dy*dy)
	px, py := p[0]-a[0], p[1]-a[1]
	t := 0.0
	if ll > 0 {
		t = math.Max(0, math.Min(1, (float64(px*dx)+float64(py*dy))/ll))
	}
	ex, ey := px-float64(t*dx), py-float64(t*dy)
	return math.Sqrt(float64(ex*ex) + float64(ey*ey))
}

// signed is p's signed distance to the geometry's edge (negative inside).
func (g shapeGeom) signed(p Point) float64 {
	switch {
	case g.circle != nil:
		dx, dy := p[0]-g.circle[0], p[1]-g.circle[1]
		return math.Sqrt(float64(dx*dx)+float64(dy*dy)) - g.r
	case g.polygon != nil:
		d := math.Inf(1)
		for i, j := 0, len(g.polygon)-1; i < len(g.polygon); j, i = i, i+1 {
			d = math.Min(d, segDistExact(p, g.polygon[j], g.polygon[i]))
		}
		if inPolygon(p, g.polygon) {
			return -d
		}
		return d
	default:
		d := math.Inf(1)
		for i := 0; i+1 < len(g.path); i++ {
			d = math.Min(d, segDistExact(p, g.path[i], g.path[i+1]))
		}
		return d - g.half
	}
}

// cover is a soft edge: 1 well inside (sd < -soft/2), 0 well outside.
func cover(sd, soft float64) float64 {
	if soft <= 0 {
		if sd <= 0 {
			return 1
		}
		return 0
	}
	return bake.Smooth(0.5 - sd/soft)
}

// edgeNoise moves an edge by up to amp metres (two octaves of value noise).
func edgeNoise(x, y, amp float64, seed int) float64 {
	if amp <= 0 {
		return 0
	}
	period := math.Max(3, 4*amp)
	n := float64(0.65*bake.Noise(x, y, period, seed)) + float64(0.35*bake.Noise(x, y, period/2.7, seed+1))
	return float64(amp * (float64(2*n) - 1))
}

// ---------------------------------------------------------------- reading the textures

// ShippedFiles reads a shipped file: the entry path of the copy's archive game/Data/<archive>.
type ShippedFiles func(archive, path string) ([]byte, error)

// Shipped reads from the inputs' archives, and the others through Archive (nil: none other).
func (in Inputs) Shipped() ShippedFiles {
	cache := map[string]*snap.Archive{}
	return func(archive, name string) ([]byte, error) {
		key := strings.ToLower(archive)
		a, ok := cache[key]
		if !ok {
			var raw []byte
			switch key {
			case strings.ToLower(path.Base(Sources[0])):
				raw = in.SMMap
			case strings.ToLower(path.Base(Sources[1])):
				raw = in.Anshavar3
			case strings.ToLower(path.Base(Sources[2])):
				raw = in.Anshavar4
			case strings.ToLower(path.Base(Sources[3])):
				raw = in.AssetData
			default:
				if in.Archive == nil {
					return nil, fmt.Errorf("the archive %s is not read here", archive)
				}
				var err error
				if raw, err = in.Archive(archive); err != nil {
					return nil, err
				}
			}
			var err error
			if a, err = snap.Parse(raw); err != nil {
				return nil, fmt.Errorf("%s: %v", archive, err)
			}
			cache[key] = a
		}
		for _, e := range a.Entries {
			if strings.EqualFold(e.Name, name) {
				return a.Read(e)
			}
		}
		return nil, fmt.Errorf("%s has no %s", archive, name)
	}
}

// ArchiveFromCopy reads game/Data/<name> from a copy (the folder holding game/Data).
func ArchiveFromCopy(copyDir string) func(name string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		if !archiveRe.MatchString(name) {
			return nil, fmt.Errorf("%q is no archive name", name)
		}
		return os.ReadFile(filepath.Join(copyDir, "game", "Data", name))
	}
}

type textures struct {
	m     *Map
	read  ShippedFiles
	cache map[string]*bake.Image
}

func (t *textures) get(ref TextureRef) (*bake.Image, error) {
	switch {
	case ref.RGB != nil:
		return bake.Flat(*ref.RGB), nil
	case ref.Procedural == "dirt":
		key := "procedural:dirt"
		if im, ok := t.cache[key]; ok {
			return im, nil
		}
		im, err := bake.NewImage(ground.DirtSize, ground.DirtSize, ground.DirtPixels(ground.DirtSize))
		if err != nil {
			return nil, err
		}
		t.cache[key] = im
		return im, nil
	case ref.Game != "":
		key := "game:" + strings.ToLower(ref.Archive) + ":" + strings.ToLower(ref.Game)
		if im, ok := t.cache[key]; ok {
			return im, nil
		}
		b, err := t.read(ref.Archive, ref.Game)
		if err != nil {
			return nil, err
		}
		im, err := bake.DecodeDDS(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", ref.Game, err)
		}
		t.cache[key] = im
		return im, nil
	case ref.File != "":
		key := "file:" + ref.File
		if im, ok := t.cache[key]; ok {
			return im, nil
		}
		im, err := readPicture(t.m, ref.File)
		if err != nil {
			return nil, err
		}
		t.cache[key] = im
		return im, nil
	}
	return nil, fmt.Errorf("an empty texture")
}

// ---------------------------------------------------------------- the bake

// Baked is a baked ground's parts: the bake, the heightfield it lies on, the field of distances to the walkable
// edge.
type Baked struct {
	Spec   *bake.Spec
	Ground *ground.Ground
	Field  *ground.Field
}

// normalZ is the ground's normal's z at (x, y), bilinear between vertices.
func normalZ(g *ground.Ground, x, y float64) float64 {
	n := g.N
	fx := math.Min(math.Max(x/g.Cell+float64(g.Grid/2), 0), float64(g.Grid))
	fy := math.Min(math.Max(y/g.Cell+float64(g.Grid/2), 0), float64(g.Grid))
	i0, j0 := min(int(fx), g.Grid-1), min(int(fy), g.Grid-1)
	tx, ty := fx-float64(i0), fy-float64(j0)
	v := func(i, j int) float64 { return g.Normal[j*n+i][2] }
	top := v(i0, j0) + float64(float64(v(i0+1, j0)-v(i0, j0))*tx)
	bot := v(i0, j0+1) + float64(float64(v(i0+1, j0+1)-v(i0, j0+1))*tx)
	return top + float64(float64(bot-top)*ty)
}

// layerMask is a layer's mask as a function of the place.
func (cm *Compiled) layerMask(k int, l GroundLayer, b *Baked, pic *bake.Image) (func(x, y float64) float64, error) {
	soft := floatOr(l.Soft, DefaultSoft)
	mirror := boolOr(l.Mirror, true)
	var geoms []shapeGeom
	var erase []bool
	var softs []float64
	var boxes [][4]float64 // each shape's reach: outside it the shape leaves the mask as it is
	for _, s := range l.Shapes {
		g, err := maskGeom(cm.Map, s)
		if err != nil {
			return nil, err
		}
		sf := floatOr(s.Soft, soft)
		reach := sf/2 + l.Noise + 1
		geoms, erase, softs = append(geoms, g), append(erase, s.Erase), append(softs, sf)
		boxes = append(boxes, g.box(reach))
		if mirror {
			gm := g.mirrored()
			geoms, erase, softs = append(geoms, gm), append(erase, s.Erase), append(softs, sf)
			boxes = append(boxes, gm.box(reach))
		}
	}
	var bushes []shapeGeom
	if l.Auto == "bushes" {
		for _, bu := range cm.Layers.Bushes {
			bushes = append(bushes, shapeGeom{polygon: bu.Outline})
		}
	}
	slope := l.Slope
	if slope == 0 {
		slope = DefaultSlope
	}
	cosSlope := math.Cos(slope * math.Pi / 180)
	size := cm.Map.Size
	seed := 101 + 7*k
	ly := cm.Layers
	picAt := func(x, y float64) float64 {
		if x < -size/2 || x > size/2 || y < -size/2 || y > size/2 {
			return 0
		}
		p := pic.Sample(x/size+0.5, 0.5-y/size, 1, false)
		return float64(float64(p[0]+p[1]+p[2])/3) / 255 * (p[3] / 255)
	}
	return func(x, y float64) float64 {
		nz := edgeNoise(x, y, l.Noise, seed)
		v := 0.0
		if l.Auto != "" {
			d := b.Field.At(x, y)
			chasm := d > 0 && ly.KindAt(Point{x, y}) == Chasm
			switch l.Auto {
			case "all":
				v = 1
			case "floor":
				v = cover(d-1.0+nz, soft)
			case "high":
				if !chasm {
					v = cover(3.0-d+nz, soft)
				}
			case "chasm":
				if chasm {
					v = cover(0.5-d+nz, soft)
				}
			case "slope":
				v = bake.Smooth((cosSlope + 0.05 - normalZ(b.Ground, x, y) + float64(nz*0.02)) / 0.1)
			case "bushes":
				sd := math.Inf(1)
				for _, g := range bushes {
					sd = math.Min(sd, g.signed(Point{x, y}))
				}
				v = cover(sd+nz, soft)
			}
		}
		if pic != nil {
			p := picAt(x, y)
			if mirror {
				p = math.Max(p, picAt(-x, -y))
			}
			v = math.Max(v, p)
		}
		for q, g := range geoms {
			if bx := boxes[q]; x < bx[0] || y < bx[1] || x > bx[2] || y > bx[3] {
				continue // the cover is 0 out here (v stays in 0..1), so skipping changes no value
			}
			c := cover(g.signed(Point{x, y})+nz, softs[q])
			if erase[q] {
				v = math.Min(v, 1-c)
			} else {
				v = math.Max(v, c)
			}
		}
		return v
	}, nil
}

// MaskCells is a mask grid's side for a bake (about four texels a cell).
func MaskCells(texels, tiles int) int { return min(2048, max(256, texels*tiles/4)) }

// Bake resolves the map's baked ground: the heightfield, the layers with their masks, the stamps. read gives the
// shipped textures.
func (cm *Compiled) Bake(read ShippedFiles) (*Baked, error) {
	m := cm.Map
	walk := cm.Layers.GroundWalk()
	field, err := ground.NewField(walk.W, walk.H, walk.Pix, m.Size)
	if err != nil {
		return nil, err
	}
	g := ground.NewGroundSpec(field, ground.Grid, 0, cm.GroundSpec())
	half := float64(ground.Grid/2) * g.Cell
	s := &bake.Spec{Half: half, Tiles: m.BakeTiles(), Texels: m.BakeTexels(), Margin: bake.MarginFor(m.BakeTexels())}
	b := &Baked{Spec: s, Ground: g, Field: field}
	if s.Base, err = cm.groundPicture(s); err != nil { // groundpicture.go: nil without a picture
		return nil, err
	}
	tx := &textures{m: m, read: read, cache: map[string]*bake.Image{}}
	cells := MaskCells(s.Texels, s.Tiles)
	for k, l := range m.BakeLayers() {
		im, err := tx.get(l.Texture)
		if err != nil {
			return nil, fmt.Errorf("ground.layers[%d]: %v", k, err)
		}
		scale := l.Scale
		if scale == 0 {
			scale = DefaultScale
		}
		bl := bake.Layer{Tex: im, Scale: scale, Rot: l.Rot * math.Pi / 180, Tint: tintOr(l.Tint), Vary: l.Vary,
			Opacity: floatOr(l.Opacity, 1), Seed: k, Break: l.Break}
		if k > 0 || s.Base != nil { // the first layer is the ground unless a picture is
			var pic *bake.Image
			if l.Image != "" {
				if pic, err = readPicture(m, l.Image); err != nil {
					return nil, fmt.Errorf("ground.layers[%d].image: %v", k, err)
				}
			}
			f, err := cm.layerMask(k, l, b, pic)
			if err != nil {
				return nil, fmt.Errorf("ground.layers[%d]: %v", k, err)
			}
			bl.Mask = bake.NewGrid(cells, half, f)
		}
		s.Layers = append(s.Layers, bl)
	}
	for k, st := range m.Ground.Stamps {
		im, err := tx.get(st.Image)
		if err != nil {
			return nil, fmt.Errorf("ground.stamps[%d]: %v", k, err)
		}
		if r := st.Rect; r != nil {
			if im, err = im.Cut(r[0], r[1], r[2], r[3]); err != nil {
				return nil, fmt.Errorf("ground.stamps[%d].rect: %v", k, err)
			}
		}
		bs := bake.Stamp{Img: im, Centre: st.Pos, Size: st.Size, Rot: st.Rot * math.Pi / 180,
			Opacity: floatOr(st.Opacity, 1), Tint: tintOr(st.Tint), Soft: st.Soft, Round: st.Round}
		s.Stamps = append(s.Stamps, bs)
		if boolOr(st.Mirror, true) {
			mb := bs
			mb.Centre = Mirror(st.Pos)
			mb.Rot = (st.Rot + 180) * math.Pi / 180
			s.Stamps = append(s.Stamps, mb)
		}
	}
	return b, nil
}

// BakeTileEntry names tile (ti, tj)'s texture in the archive; CliffEntry the cliff's.
func BakeTileEntry(name string, ti, tj int) string {
	n := strings.ToLower(name)
	return fmt.Sprintf("environments/community/%s/textures/%s_ground_%d_%d_D.dds", n, n, ti, tj)
}

// CliffEntry names the cliff material's texture in the archive.
func CliffEntry(name string) string {
	n := strings.ToLower(name)
	return fmt.Sprintf("environments/community/%s/textures/%s_cliff_D.dds", n, n)
}

// cliffSize is the most pixels a side of the cliff's source keeps (its texture holds 2 x 2 of them).
const cliffSize = 512

// CliffPixels is the cliff's texture: the source halved to at most cliffSize, laid out 2 x 2 (so a face's UVs can
// shift by whole repeats and stay inside 0..1), times the tint.
func CliffPixels(src *bake.Image, tint [3]float64) (w, h int, rgba []byte) {
	w, h, rgba = src.Repeat(2, 2, cliffSize)
	for k := 0; k < w*h; k++ {
		for c := 0; c < 3; c++ {
			v := float64(float64(rgba[4*k+c]) * tint[c])
			rgba[4*k+c] = byte(min(255, max(0, int(math.RoundToEven(v)))))
		}
		rgba[4*k+3] = 255
	}
	return w, h, rgba
}

// minimapBlend is how much of the bake the minimap shows over the layout's colours.
const minimapBlend = 0.6

// BakedMinimap is the minimap's picture from the bake (Grid x Grid RGBA, rows top to bottom): each pixel the layout's
// colour (fogmap's) mixed with the bake at the cell's centre, the dark rim along the walls kept.
func (cm *Compiled) BakedMinimap(b *Baked) ([]byte, error) {
	img := cm.Layers.LoS()
	f, err := fogmap.NewField(img)
	if err != nil {
		return nil, err
	}
	flat := fogmap.MinimapRGBASpec(img, f, cm.Map.Size, cm.Map.Size, cm.MinimapSpec())
	c := cm.Map.Size / Grid
	pic := b.Spec.Picture(Grid, -cm.Map.Size/2-c/2, -cm.Map.Size/2-c/2, cm.Map.Size)
	out := make([]byte, len(flat))
	for r := 0; r < Grid; r++ {
		j := Grid - 1 - r
		for i := 0; i < Grid; i++ {
			k := j*Grid + i
			o := 4 * (r*Grid + i)
			rim := img.Pix[k] != 0 && f[k]+0.5 <= 1.5
			for q := 0; q < 3; q++ {
				if rim {
					out[o+q] = flat[o+q]
				} else {
					v := lerpF(float64(flat[o+q]), float64(pic[o+q]), minimapBlend)
					out[o+q] = byte(min(255, max(0, int(math.RoundToEven(v)))))
				}
			}
			out[o+3] = 255
		}
	}
	return out, nil
}

func lerpF(a, b, t float64) float64 { return a + float64(float64(b-a)*t) }

// GroundFiles are the ground's archive entries (the model and its textures) and, for a baked ground whose minimap is
// the bake's, the minimap (else nil: FogFiles' stands).
func (cm *Compiled) GroundFiles(read ShippedFiles) (files []snap.File, minimap []byte, err error) {
	m := cm.Map
	e := EntriesFor(m.Name)
	tmpl, err := read(ground.TemplateArchive, ground.TemplateEntry)
	if err != nil {
		return nil, nil, err
	}
	back, mist, voidFiles, err := cm.voidPlanes(read) // void.go: none without a void
	if err != nil {
		return nil, nil, err
	}
	if !m.Baked() {
		grass, err := read(ground.GrassArchive, ground.GrassSource)
		if err != nil {
			return nil, nil, err
		}
		// the ground rises past the walkable ground's edge, and drops where that edge is a chasm's
		spec := cm.GroundSpec()
		spec.Backdrop, spec.Mist = back, mist
		model, dirt, g, err := ground.BuildSpec(cm.Layers.GroundWalk(), spec, tmpl, grass, [2]string{e.Dirt, e.GrassTex})
		if err != nil {
			return nil, nil, err
		}
		return append([]snap.File{{Name: e.Model, Data: model}, {Name: e.Dirt, Data: dirt}, {Name: e.GrassTex, Data: g}},
			voidFiles...), nil, nil
	}
	b, err := cm.Bake(read)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range []*ground.Backdrop{back, mist} {
		if p != nil {
			b.Ground.Backs = append(b.Ground.Backs, p)
		}
	}
	s := b.Spec
	var paths []string
	var texs []snap.File
	for tj := 0; tj < s.Tiles; tj++ {
		for ti := 0; ti < s.Tiles; ti++ {
			px, err := s.Tile(ti, tj)
			if err != nil {
				return nil, nil, err
			}
			d, err := dds.Encode(s.Texels, s.Texels, px, "dxt1", 0)
			if err != nil {
				return nil, nil, err
			}
			p := BakeTileEntry(m.Name, ti, tj)
			paths = append(paths, p)
			texs = append(texs, snap.File{Name: p, Data: d})
		}
	}
	var cliff *bake.Cliff
	if cl := m.Ground.Cliff; cl != nil {
		tx := &textures{m: m, read: read, cache: map[string]*bake.Image{}}
		src, err := tx.get(cl.Texture)
		if err != nil {
			return nil, nil, fmt.Errorf("ground.cliff: %v", err)
		}
		w, h, px := CliffPixels(src, tintOr(cl.Tint))
		d, err := dds.Encode(w, h, px, "dxt1", 0)
		if err != nil {
			return nil, nil, err
		}
		scale, slope := cl.Scale, cl.Slope
		if scale == 0 {
			scale = DefaultCliffM
		}
		if slope == 0 {
			slope = DefaultCliffDeg
		}
		cliff = &bake.Cliff{Path: CliffEntry(m.Name), Scale: scale, CosSlope: math.Cos(slope * math.Pi / 180)}
		texs = append(texs, snap.File{Name: cliff.Path, Data: d})
	}
	model, err := bake.BuildModel(tmpl, b.Ground, s, strings.ToLower(m.Name), paths, cliff)
	if err != nil {
		return nil, nil, err
	}
	if _, err := bake.CheckModel(model); err != nil {
		return nil, nil, err
	}
	files = append(append([]snap.File{{Name: e.Model, Data: model}}, texs...), voidFiles...)
	if m.Ground.Minimap == "flat" {
		return files, nil, nil
	}
	rgba, err := cm.BakedMinimap(b)
	if err != nil {
		return nil, nil, err
	}
	if minimap, err = dds.Encode(Grid, Grid, rgba, "dxt5", minimapMs); err != nil {
		return nil, nil, err
	}
	return files, minimap, nil
}
