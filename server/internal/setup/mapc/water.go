package mapc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/mapmm/ground"
)

// Water in the chasms: a chasm shape with "water" gets a water plane (a copy of a shipped chasm water marker) at a
// height below the floor, so it shows only where the ground drops below it, in the chasm's dip (the ground model's
// chasms fall to -ground.Plateau m past the walkable edge). The plane is the smallest rectangle round the shape's
// cells whose ground lies below the water (any turn), widened by WaterMargin, so its edges always lie under higher
// ground: the shore is where the ground model meets the water, whatever the chasm's outline. A shape whose plane
// would overlap its mirror's (a chasm across the centre) gets one plane for both. The planes go into the map's
// environment markerset, after the decals; a map without water builds as before.
//
// In map.json the member is a checkbox: "water": true (the defaults), false (none), or {"z": m, "mask": name}.

// The water's limits and defaults (metres).
const (
	DefaultWaterZ = -2.0  // the water's height: the shore lies about 2.5 m past the chasm's walkable edge
	MinWaterZ     = -4.5  // the chasm's floor is at -ground.Plateau
	MaxWaterZ     = -0.25 // below the floor (z 0), which must hide the plane
	WaterMargin   = 3.0   // the plane reaches this far past the outermost wet cell's edge, under the rising ground
	wetBelow      = 0.05  // a cell is wet when its ground lies this far below the water
)

// WaterTemplateSet is the shipped markerset whose water markers the planes copy (their colours, fresnel, reflection,
// depth fog and soft edges; the size, the mask and the outline are the map's): per look the first marker whose mask
// names WaterLooks' pattern.
const WaterTemplateSet = "SM_Map_v5_D_Water.Markerset"

// WaterLooks are the looks a chasm's water may take, the first the default, and the mask each look's template marker
// has: "river" is SM_Map_v5's river (Fish_wm_1: reflective, refracting, depth fog 0.08 a metre, ripples tiled every
// riverTileM), meant for shallow water; "chasm" is SM's chasm water (Chasm_wm_2: no reflection, depth fog 0.01), made
// for its chasms tens of metres deep, so over a dip of a few metres it is nearly clear.
var WaterLooks = [][2]string{{"river", "fish_wm"}, {"chasm", "chasm_wm"}}

// riverTileM is the river look's ripple size: SM's river markers tile their normal map about every 20 m (80 m: 4).
const riverTileM = 20.0

// waterMaskDir is where the shipped water masks are (as the shipped markers spell it).
const waterMaskDir = `Environments\anshavar\textures\`

// WaterMasks are the masks a chasm's water may name: "none" (the default: the rectangle, cut by the ground) and the
// shipped water markers' masks (each with its outline .out but Spirit_Lake_gobo). "circle" is Circle_wm.
var WaterMasks = []string{"none", "circle", "Chasm_wm_1", "Chasm_wm_2", "Chasm_wm_3", "Chasm_wm_4", "Chasm_wm_5",
	"Chasm_wm_6", "Chasm_wm_7", "Chasm_wm_8", "Fish_wm_1", "Fish_wm_2", "Fish_wm_3", "Fish_wm_4", "Fish_wm_5",
	"Fish_wm_6", "Spirit_Lake_gobo"}

// Water is a chasm shape's water: true (Water{}), false (Off) or its height and mask.
type Water struct {
	Z    *float64 `json:"z,omitempty"`    // the water's height, default DefaultWaterZ
	Mask string   `json:"mask,omitempty"` // one of WaterMasks, default "none"
	Look string   `json:"look,omitempty"` // one of WaterLooks, default "river"
	Off  bool     `json:"-"`              // written as false: no water
}

// UnmarshalJSON reads true, false or the object.
func (w *Water) UnmarshalJSON(b []byte) error {
	switch string(bytes.TrimSpace(b)) {
	case "true":
		*w = Water{}
		return nil
	case "false":
		*w = Water{Off: true}
		return nil
	}
	type plain Water
	var p plain
	if err := strictUnmarshal(b, &p); err != nil {
		return fmt.Errorf(`water: true, false or {"z": m, "mask": name, "look": name}: %v`, err)
	}
	*w = Water(p)
	return nil
}

// MarshalJSON writes false, true (the defaults) or the object.
func (w Water) MarshalJSON() ([]byte, error) {
	if w.Off {
		return []byte("false"), nil
	}
	if w.Z == nil && w.Mask == "" && w.Look == "" {
		return []byte("true"), nil
	}
	type plain Water
	return json.Marshal(plain(w))
}

// watered reports whether the shape holds water.
func (s Shape) watered() bool { return s.Water != nil && !s.Water.Off }

// Level is the water's height.
func (w *Water) Level() float64 {
	if w == nil || w.Z == nil {
		return DefaultWaterZ
	}
	return *w.Z
}

// look is the water's look (one of WaterLooks' names) and its template's mask pattern; ok false for an unknown look.
func (w *Water) look() (name, pattern string, ok bool) {
	if w == nil || w.Look == "" {
		return WaterLooks[0][0], WaterLooks[0][1], true
	}
	for _, l := range WaterLooks {
		if strings.EqualFold(l[0], w.Look) {
			return l[0], l[1], true
		}
	}
	return "", "", false
}

// MaskFiles are the mask's texture and outline paths as a water marker names them ("" for none).
func (w *Water) MaskFiles() (mask, outline string, ok bool) {
	name := "none"
	if w != nil && w.Mask != "" {
		name = w.Mask
	}
	for _, n := range WaterMasks {
		if !strings.EqualFold(n, name) {
			continue
		}
		switch n {
		case "none":
			return "", "", true
		case "circle":
			n = "Circle_wm"
		case "Spirit_Lake_gobo":
			return waterMaskDir + n + ".dds", "", true
		}
		return waterMaskDir + n + ".dds", waterMaskDir + n + ".out", true
	}
	return "", "", false
}

// WaterPlane is one water marker: its place, turn and size (along the turned x and y), from which shape.
type WaterPlane struct {
	Shape   int        `json:"shape"` // index into terrain.shapes
	Team    int        `json:"team"`  // 1 the shape's, 2 its mirror, 0 one plane for both (across the centre)
	Pos     [3]float64 `json:"pos"`
	Rot     float64    `json:"rot"`  // degrees about z, counter-clockwise, in [0, 90)
	Size    [2]float64 `json:"size"` // metres along the turned x and y
	Mask    string     `json:"mask,omitempty"`
	Outline string     `json:"outline,omitempty"`
	Look    string     `json:"look"`  // the template: river or chasm
	Cells   int        `json:"cells"` // the wet cells it covers (ground below the water)
	Path    string     `json:"path"`  // terrain.shapes[k].water
}

// HasWater reports whether a shape of the map holds water.
func HasWater(m *Map) bool {
	for _, s := range m.Terrain.Shapes {
		if s.watered() {
			return true
		}
	}
	return false
}

// waterRules are the structural rules: water only on a chasm, its height in range, a known mask.
func (c *checker) waterRules() {
	for k, s := range c.m.Terrain.Shapes {
		if !s.watered() {
			continue
		}
		path := fmt.Sprintf("terrain.shapes[%d].water", k)
		if s.Type != "chasm" {
			c.errorf("terrain.water", path, nil, "water fills a chasm; this shape is %q", s.Type)
		}
		if z := s.Water.Level(); !(z >= MinWaterZ && z <= MaxWaterZ) {
			c.errorf("terrain.water", path+".z", nil, "z %g: from %g to %g (below the floor, above the chasm's "+
				"bottom at %g)", z, MinWaterZ, MaxWaterZ, -ground.Plateau)
		}
		if _, _, ok := s.Water.look(); !ok {
			c.errorf("terrain.water", path+".look", nil, "look %q: river or chasm", s.Water.Look)
		}
		if _, _, ok := s.Water.MaskFiles(); !ok {
			c.errorf("terrain.water", path+".mask", nil, "mask %q: one of %s", s.Water.Mask,
				strings.Join(WaterMasks, ", "))
		}
	}
}

// waterGeometry warns of a watered chasm with no ground below its water and of planes that overlap (the overlap
// draws the water twice).
func (c *checker) waterGeometry(cm *Compiled) {
	if !HasWater(cm.Map) {
		return
	}
	ps, fs, err := cm.WaterPlanes()
	if err != nil {
		c.errorf("terrain.water", "terrain.shapes", nil, "%v", err)
		return
	}
	c.fs = append(c.fs, fs...)
	for i := range ps {
		for j := range i {
			if ps[i].Shape != ps[j].Shape && rectsOverlap(ps[i], ps[j]) {
				c.warnf("terrain.water-overlap", ps[i].Path, pt(Point{ps[i].Pos[0], ps[i].Pos[1]}), "this water "+
					"overlaps %s's (team %d): the overlap is drawn twice; keep watered chasms apart or make one shape",
					ps[j].Path, ps[j].Team)
			}
		}
	}
}

// WaterPlanes are the map's water planes, by shape (team 1's, then its mirror's, or one for both); the findings
// warn of a watered chasm whose ground never drops below its water (no plane).
func (cm *Compiled) WaterPlanes() ([]WaterPlane, []Finding, error) {
	m := cm.Map
	if !HasWater(m) {
		return nil, nil, nil
	}
	ly := cm.Layers
	walk := ly.GroundWalk()
	field, err := ground.NewField(walk.W, walk.H, walk.Pix, m.Size)
	if err != nil {
		return nil, nil, err
	}
	spec := cm.GroundSpec()
	cellM := m.Size / Grid
	c := &checker{m: m}
	var out []WaterPlane
	for k, s := range m.Terrain.Shapes {
		if !s.watered() || s.Type != "chasm" {
			continue
		}
		g, err := geom(m, s)
		if err != nil {
			return nil, nil, fmt.Errorf("terrain.shapes[%d]: %v", k, err)
		}
		z := s.Water.Level()
		mask, outline, _ := s.Water.MaskFiles()
		look, _, _ := s.Water.look()
		path := fmt.Sprintf("terrain.shapes[%d].water", k)
		wet := func(gg shapeGeom) []Point {
			var pts []Point
			for _, cell := range ly.cellsIn(gg) {
				if ly.Kind[cell] != Chasm {
					continue
				}
				p := ly.CellCentre(cell%Grid, cell/Grid)
				if ground.Height(field.D[cell], p[0], p[1], &spec) < z-wetBelow {
					pts = append(pts, p)
				}
			}
			return pts
		}
		own := wet(g)
		if len(own) == 0 {
			c.warnf("terrain.water", path, nil, "the chasm's ground never drops below the water at z %g (too narrow, "+
				"or drawn over): no water", z)
			continue
		}
		plane := func(pts []Point, team int) WaterPlane {
			centre, rot, half := fitRect(pts)
			pl := WaterPlane{Shape: k, Team: team, Pos: [3]float64{round3(centre[0]) + 0, round3(centre[1]) + 0, z},
				Rot: rot, Size: [2]float64{round3(2 * (half[0] + cellM/2 + WaterMargin)),
					round3(2 * (half[1] + cellM/2 + WaterMargin))}, Mask: mask, Outline: outline, Look: look, Cells: len(pts),
				Path: path}
			return pl
		}
		p1 := plane(own, 1)
		p2 := p1
		p2.Team, p2.Pos[0], p2.Pos[1] = 2, -p1.Pos[0]+0, -p1.Pos[1]+0
		if atCentre(Point{p1.Pos[0], p1.Pos[1]}) || rectsOverlap(p1, p2) {
			both := own
			seen := map[Point]bool{}
			for _, p := range own {
				seen[p] = true
			}
			for _, p := range wet(g.mirrored()) {
				if !seen[p] {
					both = append(both, p)
				}
			}
			one := plane(both, 0)
			one.Pos[0], one.Pos[1] = 0, 0 // the wet cells are the point mirror of themselves
			out = append(out, one)
			continue
		}
		out = append(out, p1, p2)
	}
	return out, c.fs, nil
}

// fitRect is the smallest rectangle round the points (the convex hull's edges tried in turn, the first smallest
// kept): its centre, its turn in [0, 90) degrees (rounded to 0.001) and its half sides along the turned x and y.
func fitRect(pts []Point) (Point, float64, [2]float64) {
	h := hull(pts)
	best := math.Inf(1)
	var centre Point
	var ux, uy float64
	var half [2]float64
	for i := range h {
		a, b := h[i], h[(i+1)%len(h)]
		dx, dy := b[0]-a[0], b[1]-a[1]
		l := math.Hypot(dx, dy)
		ex, ey := 1.0, 0.0
		if l > 0 {
			ex, ey = dx/l, dy/l
		}
		lo, hi := [2]float64{math.Inf(1), math.Inf(1)}, [2]float64{math.Inf(-1), math.Inf(-1)}
		for _, p := range h {
			s := float64(p[0]*ex) + float64(p[1]*ey)
			t := float64(-p[0]*ey) + float64(p[1]*ex)
			lo[0], hi[0] = math.Min(lo[0], s), math.Max(hi[0], s)
			lo[1], hi[1] = math.Min(lo[1], t), math.Max(hi[1], t)
		}
		area := float64((hi[0] - lo[0]) * (hi[1] - lo[1]))
		if area < best-1e-9 {
			best, ux, uy = area, ex, ey
			ms, mt := (lo[0]+hi[0])/2, (lo[1]+hi[1])/2
			centre = Point{float64(ms*ex) - float64(mt*ey), float64(ms*ey) + float64(mt*ex)}
			half = [2]float64{(hi[0] - lo[0]) / 2, (hi[1] - lo[1]) / 2}
		}
	}
	rot := math.Mod(math.Atan2(uy, ux)*180/math.Pi+360, 180)
	if rot >= 90 {
		rot -= 90
		half[0], half[1] = half[1], half[0]
	}
	rot = round3(rot)
	if rot >= 90 {
		rot = 0
		half[0], half[1] = half[1], half[0]
	}
	return centre, rot + 0, half
}

// hull is the points' convex hull, counter-clockwise (Andrew's monotone chain); fewer than three points when they lie
// on a line.
func hull(pts []Point) []Point {
	ps := append([]Point{}, pts...)
	sort.Slice(ps, func(i, j int) bool {
		if ps[i][0] != ps[j][0] {
			return ps[i][0] < ps[j][0]
		}
		return ps[i][1] < ps[j][1]
	})
	if len(ps) < 3 {
		return ps
	}
	cross := func(o, a, b Point) float64 {
		return float64((a[0]-o[0])*(b[1]-o[1])) - float64((a[1]-o[1])*(b[0]-o[0]))
	}
	var h []Point
	for _, p := range ps {
		for len(h) >= 2 && cross(h[len(h)-2], h[len(h)-1], p) <= 0 {
			h = h[:len(h)-1]
		}
		h = append(h, p)
	}
	lower := len(h) + 1
	for i := len(ps) - 2; i >= 0; i-- {
		p := ps[i]
		for len(h) >= lower && cross(h[len(h)-2], h[len(h)-1], p) <= 0 {
			h = h[:len(h)-1]
		}
		h = append(h, p)
	}
	return h[:len(h)-1]
}

// corners are a plane's rectangle's corners.
func (p WaterPlane) corners() [4]Point {
	r := p.Rot * math.Pi / 180
	ux, uy := math.Cos(r), math.Sin(r)
	a, b := p.Size[0]/2, p.Size[1]/2
	var out [4]Point
	for i, s := range [4][2]float64{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}} {
		x, y := s[0]*a, s[1]*b
		out[i] = Point{p.Pos[0] + x*ux - y*uy, p.Pos[1] + x*uy + y*ux}
	}
	return out
}

// rectsOverlap reports whether two planes' rectangles share area (the separating axis test; touching isn't
// overlapping).
func rectsOverlap(a, b WaterPlane) bool {
	ca, cb := a.corners(), b.corners()
	for _, cs := range [2][4]Point{ca, cb} {
		for i := range 2 {
			ax := Point{cs[i+1][0] - cs[i][0], cs[i+1][1] - cs[i][1]}
			lo1, hi1 := projectRect(ca, ax)
			lo2, hi2 := projectRect(cb, ax)
			if hi1 <= lo2+1e-6 || hi2 <= lo1+1e-6 {
				return false
			}
		}
	}
	return true
}

func projectRect(cs [4]Point, ax Point) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, c := range cs {
		v := c[0]*ax[0] + c[1]*ax[1]
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	return lo, hi
}

// water writes the planes into the environment markerset: per plane a copy of WaterTemplateSet's first chasm water
// marker at its place and turn, scale 1, with its size, mask and outline.
func (b *assetBuild) water() error {
	if !HasWater(b.cm.Map) {
		return nil
	}
	ps, _, err := b.cm.WaterPlanes()
	if err != nil {
		return err
	}
	set, err := b.src.object(WaterTemplateSet)
	if err != nil {
		return err
	}
	tmpls := map[string]*assetdata.Object{}
	for _, l := range WaterLooks {
		for _, m := range markers(set) {
			if wd := sub(m, "waterData"); wd != nil && strings.Contains(strings.ToLower(text(wd, "mask")), l[1]) {
				tmpls[l[0]] = m
				break
			}
		}
	}
	for _, p := range ps {
		tmpl := tmpls[p.Look]
		if tmpl == nil {
			return fmt.Errorf("%s has no water marker for the %s look", WaterTemplateSet, p.Look)
		}
		key := fmt.Sprintf("water/%d/%d", p.Shape, p.Team)
		m := b.place(setEnvironment, tmpl, key, Point{p.Pos[0], p.Pos[1]})
		m.Set("pos", f32s(p.Pos[0], p.Pos[1], p.Pos[2]))
		m.Set("rotDegrees", f32s(0, 0, p.Rot+float64(180*boolInt(p.Team == 2))))
		m.Set("scale", float32(1))
		m.Set("visible", true)
		wd := cloneObj(sub(tmpl, "waterData"))
		wd.Set("size", f32s(p.Size[0], p.Size[1]))
		wd.Set("mask", p.Mask)
		wd.Set("outline", p.Outline)
		if p.Look == "river" { // the ripples as large as on SM's river, whatever the plane's size
			wd.Set("tile", f32s(round3(p.Size[0]/riverTileM), round3(p.Size[1]/riverTileM)))
		}
		m.Set("waterData", wd)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// drawWater outlines the water planes (the preview's objects.png; nothing for a map without water).
func (cm *Compiled) drawWater(img *image.RGBA) {
	ps, _, err := cm.WaterPlanes()
	if err != nil {
		return
	}
	col := color.RGBA{120, 220, 255, 255}
	for _, p := range ps {
		cs := p.corners()
		for i := range cs {
			line(img, cm, cs[i], cs[(i+1)%4], col)
		}
	}
}
