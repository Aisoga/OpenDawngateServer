package mapc

// Authored heights: a wall shape's "height" is its plateau's height (metres, positive), a chasm shape's its depth
// (negative). Without one a wall stands ground.Plateau (5 m) high and a chasm drops as far, as before heights. Where
// shapes overlap the later shape's height wins, as its kind does (a low wall drawn over a mountain cuts a pass into
// it; drawing order is what the editor shows); the ground model then blends the heights of one kind over
// ground.HeightBlend metres (mapmm/ground relief.go) so neighbouring plateaus ramp instead of stepping. Walkable ground
// has no height: it stays the flat floor at z 0 the characters stand on (the game's floor is flat, What the engine
// fixes), so a height on an open, island or bush shape is an error.
//
// The rules: terrain.height (an error: a height on another kind, out of range or of the wrong sign),
// terrain.height-hide (a warning: a wall tall enough to hide a character standing north of it from the game camera)
// terrain.height-void (a warning: a chasm deeper than the void's backdrop, which then covers its bottom) and
// terrain.height-step (a warning: a chasm cut straight into a wall, where the ground drops from the plateau to the
// chasm's floor in one step). The geometric ones only run on a map with heights: a map without one keeps its report,
// which its archive carries, byte for byte.

import (
	"fmt"
	"math"
	"slices"

	"dawngate/server/internal/setup/mapmm/ground"
)

// The game camera the hide rule measures against: the client's "Main Gameplay Camera" (Other.package, instance
// 0xC484BACF: cameraPitch 60, cameraFOV 47, cameraDistance 63; yaw 0, so it looks north). The shallowest ray it shows
// is at the top of its view, CameraPitch - CameraFOV/2 = 36.5 degrees below the horizontal; HideCharacter is a
// character's height [assumed: about a shaper's; not measured in game].
const (
	CameraPitch   = 60.0 // degrees below the horizontal
	CameraFOV     = 47.0 // degrees, vertical
	HideCharacter = 2.0  // metres
)

// HideAngle is the shallowest view's angle (degrees): the top edge of the default camera's view.
const HideAngle = CameraPitch - CameraFOV/2

// HasHeights reports whether a shape of the map sets a height (the map then has a Relief).
func HasHeights(m *Map) bool {
	for _, s := range m.Terrain.Shapes {
		if s.Height != nil {
			return true
		}
	}
	return false
}

// kindTop is a cell's authored top for its kind: a wall's plateau height (h or ground.Plateau), a chasm's depth
// (negative), 0 for any other kind.
func kindTop(kind byte, h *float64) float64 {
	switch kind {
	case Wall:
		if h != nil {
			return *h
		}
		return ground.Plateau
	case Chasm:
		if h != nil {
			return *h
		}
		return -ground.Plateau
	}
	return 0
}

// heightRules are the structural rules: a height only on a wall (MinHeight to MaxHeight) or a chasm (-MaxHeight to
// -MinHeight).
func (c *checker) heightRules() {
	for k, s := range c.m.Terrain.Shapes {
		if s.Height == nil {
			continue
		}
		h, path := *s.Height, fmt.Sprintf("terrain.shapes[%d].height", k)
		switch s.Type {
		case "wall":
			if !(h >= ground.MinHeight && h <= ground.MaxHeight) {
				c.errorf("terrain.height", path, nil, "height %g: a wall's plateau is %g to %g metres up",
					h, ground.MinHeight, ground.MaxHeight)
			}
		case "chasm":
			if !(h >= -ground.MaxHeight && h <= -ground.MinHeight) {
				c.errorf("terrain.height", path, nil, "height %g: a chasm's depth is %g to %g metres (negative)",
					h, -ground.MaxHeight, -ground.MinHeight)
			}
		default:
			c.errorf("terrain.height", path, nil, "a %s shape has no height: walkable ground stays the flat floor "+
				"at z 0 (heights are for walls and chasms)", s.Type)
		}
	}
}

// HideTan is the tangent of HideAngle.
var HideTan = math.Tan(HideAngle * math.Pi / 180)

// hideExcess is how far (metres) a wall of height h, its cliff starting at the walkable edge, rises above the ray
// that leaves the edge's floor towards the camera at HideAngle: the most of z(s) - s * HideTan over the distance s
// past the edge (no hills; sampled every 5 cm).
func hideExcess(h float64) float64 {
	hi := ground.CliffEnd(h)
	best := 0.0
	for s := 0.0; s <= hi+1; s += 0.05 {
		t := math.Min(math.Max((s-ground.CliffLo)/(hi-ground.CliffLo), 0), 1)
		best = math.Max(best, h*t*t*(3-2*t)-s*HideTan)
	}
	return best
}

// HideSafeHeight is the tallest wall that hides no HideCharacter-tall character even right at its foot (about 5.4 m):
// the default 5-m plateau never warns.
func HideSafeHeight() float64 {
	lo, hi := 1.0, ground.MaxHeight
	for range 40 {
		mid := (lo + hi) / 2
		if hideExcess(mid) > HideCharacter {
			hi = mid
		} else {
			lo = mid
		}
	}
	return lo
}

// HideReach is how far north of a wall of height h, its cliff at the walkable edge, a character can be hidden at the
// top of the camera's view (0: nowhere).
func HideReach(h float64) float64 {
	return math.Max(0, (hideExcess(h)-HideCharacter)/HideTan)
}

// heightGeometry runs the geometric height rules on the compiled map.
func (c *checker) heightGeometry(cm *Compiled) {
	m, ly := cm.Map, cm.Layers
	if ly.Relief == nil {
		return // a map without heights builds as before, its report too (the archive carries it: preview.json)
	}
	for _, st := range cm.ChasmSteps() {
		path := "terrain.default"
		if st.Shape >= 0 {
			path = fmt.Sprintf("terrain.shapes[%d]", st.Shape)
		}
		c.warnf("terrain.height-step", path, pt(st.At), "this chasm meets a wall on %d cells (%.0f m²) away from "+
			"walkable ground: the ground drops %.1f m there in one step, a vertical face along the cells that the game "+
			"draws as a dark pit with a stepped rim; give the chasm a rim of walkable ground (an open shape under it, "+
			"a few metres wider) where it meets the wall, so its cliff starts at that rim", st.Cells,
			float64(st.Cells)*cm.CellArea(), st.Drop)
	}
	if m.Void != nil && m.Void.Backdrop != nil {
		for k, s := range m.Terrain.Shapes {
			if s.Type == "chasm" && s.Height != nil && *s.Height < m.Void.Backdrop.Z {
				c.warnf("terrain.height-void", fmt.Sprintf("terrain.shapes[%d].height", k), nil, "the chasm drops to "+
					"%g m, below the void's backdrop at %g m: the backdrop covers its bottom", *s.Height,
					m.Void.Backdrop.Z)
			}
		}
	}
	for _, h := range cm.HiddenGround() {
		if h.Shape < 0 {
			c.warnf("terrain.height-hide", "terrain.default", pt(h.At), "the default walls here rise to %.1f m, "+
				"blended with a taller wall shape beside them, and can hide a character (%g m) on %d cells of walkable "+
				"ground north of them from the game camera (its view looks down at %g° at its top): lower that shape "+
				"or keep it further from walkable ground", h.Top, HideCharacter, h.Cells, HideAngle)
			continue
		}
		path := fmt.Sprintf("terrain.shapes[%d]", h.Shape)
		c.warnf("terrain.height-hide", path, pt(h.At), "this %g-m wall can hide a character (%g m) on %d cells "+
			"(%.0f m²) of walkable ground north of it from the game camera, whose view looks down at %g° at its top "+
			"(pitch %g less half the FOV %g): lower it to %.1f m where it meets walkable ground, or move its tall part "+
			"back (a character up to about %.0f m north of its cliff's foot is hidden)", h.Height, HideCharacter, h.Cells, float64(h.Cells)*cm.CellArea(), HideAngle,
			CameraPitch, CameraFOV, HideSafeHeight(), HideReach(h.Height))
	}
}

// StepMax is the most the ground may drop from a wall cell to a neighbouring chasm cell before terrain.height-step
// warns (metres). The relief measures both from the walkable edge, so where a chasm is cut straight into a wall, away
// from walkable ground, the ground falls from the wall's plateau to the chasm's floor between two vertices: a
// vertical face along the cell raster, which the game draws as a dark pit with a stepped rim (EDUX9b, seen live). A
// cliff of the relief's own drops at most about 2 m a cell; 4 m leaves room for the blend.
const StepMax = 4.0

// Step is one chasm shape's ground that drops in one step from a wall (ChasmSteps).
type Step struct {
	Shape int     // the chasm shape (-1: terrain.default)
	Drop  float64 // the largest drop between neighbouring cells (m)
	Cells int     // the chasm cells with such a drop (both teams' halves)
	At    Point   // one of them, on team 1's half where there is one
}

// ChasmSteps finds the chasm cells whose ground lies more than StepMax below a neighbouring wall cell's (the four
// neighbours, the ground at the cell centres), per chasm shape (the last one that covers the cell), in shape order.
// With the void's cut, cells more than the cut past the walkable edge are left out (their triangles aren't drawn).
func (cm *Compiled) ChasmSteps() []Step {
	ly := cm.Layers
	walk := ly.GroundWalk()
	field, err := ground.NewField(walk.W, walk.H, walk.Pix, cm.Map.Size)
	if err != nil {
		return nil
	}
	spec := cm.GroundSpec()
	z := func(k int) float64 {
		p := ly.CellCentre(k%Grid, k/Grid)
		return ground.Height(field.D[k], p[0], p[1], &spec)
	}
	var owner []int // lazily: the last chasm shape over each cell
	shapeOf := func(k int) int {
		if owner == nil {
			owner = make([]int, len(ly.Kind))
			for c := range owner {
				owner[c] = -1
			}
			for si, s := range cm.Map.Terrain.Shapes {
				if s.Type != "chasm" {
					continue
				}
				g, err := geom(cm.Map, s)
				if err != nil {
					continue
				}
				for _, gg := range []shapeGeom{g, g.mirrored()} {
					for _, c := range ly.cellsIn(gg) {
						owner[c] = si
					}
				}
			}
		}
		return owner[k]
	}
	byShape := map[int]*Step{}
	var order []int
	for j := 0; j < Grid; j++ {
		for i := 0; i < Grid; i++ {
			k := j*Grid + i
			if ly.Kind[k] != Chasm || (spec.Cut > 0 && field.D[k] > spec.Cut) {
				continue
			}
			drop, zk := 0.0, math.NaN()
			for _, n := range [4][2]int{{i - 1, j}, {i + 1, j}, {i, j - 1}, {i, j + 1}} {
				if n[0] < 0 || n[0] >= Grid || n[1] < 0 || n[1] >= Grid || ly.Kind[n[1]*Grid+n[0]] != Wall {
					continue
				}
				if math.IsNaN(zk) {
					zk = z(k)
				}
				drop = math.Max(drop, z(n[1]*Grid+n[0])-zk)
			}
			if drop <= StepMax {
				continue
			}
			sh := shapeOf(k)
			st := byShape[sh]
			p := ly.CellCentre(i, j)
			if st == nil {
				st = &Step{Shape: sh, At: p}
				byShape[sh] = st
				order = append(order, sh)
			}
			st.Cells++
			st.Drop = math.Max(st.Drop, drop)
			if st.At[0]+st.At[1] > 0 && p[0]+p[1] <= 0 {
				st.At = p
			}
		}
	}
	slices.Sort(order)
	out := make([]Step, 0, len(order))
	for _, sh := range order {
		out = append(out, *byShape[sh])
	}
	return out
}

// CellArea is a layer cell's area (m²).
func (cm *Compiled) CellArea() float64 {
	c := cm.Map.Size / Grid
	return c * c
}

// Hidden is the walkable ground one wall shape can hide from the game camera (HiddenGround).
type Hidden struct {
	Shape  int     // the shape whose cells hide it (-1: terrain.default)
	Height float64 // the shape's authored height (ground.Plateau without one)
	Top    float64 // the highest ground of the shape that hides a cell (m; blended, with the hills)
	Cells  int     // the walkable cells hidden (both teams' halves)
	At     Point   // one of them, on team 1's half where there is one
}

// HiddenGround finds the walkable cells (open and island) where a HideCharacter-tall character is hidden from the
// game camera at its shallowest view: a cell is hidden when the ground south of it (towards the camera, which looks
// north) rises above the ray from the character's head at HideAngle. Looking north over the whole map also covers a
// camera that looks south for team 2: the map is point-symmetric, so team 2's southward view is team 1's northward
// one mirrored. Per occluding shape, in shape order; nil without authored heights.
func (cm *Compiled) HiddenGround() []Hidden {
	ly := cm.Layers
	if ly.Relief == nil {
		return nil
	}
	walk := ly.GroundWalk()
	field, err := ground.NewField(walk.W, walk.H, walk.Pix, cm.Map.Size)
	if err != nil {
		return nil
	}
	spec := cm.GroundSpec()
	z := make([]float64, Grid*Grid)
	zmax := 0.0
	for k, d := range field.D {
		if d > ground.CliffLo {
			p := ly.CellCentre(k%Grid, k/Grid)
			z[k] = ground.Height(d, p[0], p[1], &spec)
			zmax = math.Max(zmax, z[k])
		}
	}
	if zmax <= HideCharacter {
		return nil
	}
	cell := cm.Map.Size / Grid
	rad := int(math.Round(ground.HeightBlend / cell))
	// blame is the cell within the blend of o whose authored top is the highest: the shape that raised o (o itself
	// when it is as high)
	blame := func(o int) int {
		oi, oj := o%Grid, o/Grid
		best := o
		for j := max(0, oj-rad); j <= min(Grid-1, oj+rad); j++ {
			for i := max(0, oi-rad); i <= min(Grid-1, oi+rad); i++ {
				if k := j*Grid + i; ly.Top[k] > ly.Top[best] {
					best = k
				}
			}
		}
		return best
	}
	steps := int(math.Ceil((zmax - HideCharacter) / (cell * HideTan)))
	byShape := map[int]*Hidden{}
	var order []int
	for j := 0; j < Grid; j++ {
		for i := 0; i < Grid; i++ {
			if !Walkable(ly.Kind[j*Grid+i]) {
				continue
			}
			best, o := 0.0, -1 // the cell that rises furthest above the ray hides it
			for s := 1; s <= steps && j-s >= 0; s++ {
				if e := z[(j-s)*Grid+i] - (HideCharacter + float64(s)*cell*HideTan); e > best {
					best, o = e, (j-s)*Grid+i
				}
			}
			if o < 0 {
				continue
			}
			sh := ly.TopShape[blame(o)]
			h := byShape[sh]
			p := ly.CellCentre(i, j)
			if h == nil {
				h = &Hidden{Shape: sh, At: p}
				byShape[sh] = h
				order = append(order, sh)
			}
			h.Cells++
			h.Top = math.Max(h.Top, z[o])
			if h.At[0]+h.At[1] > 0 && p[0]+p[1] <= 0 {
				h.At = p
			}
		}
	}
	slices.Sort(order)
	out := make([]Hidden, 0, len(order))
	for _, sh := range order {
		h := byShape[sh]
		h.Height = ground.Plateau
		if sh >= 0 && cm.Map.Terrain.Shapes[sh].Height != nil {
			h.Height = *cm.Map.Terrain.Shapes[sh].Height
		}
		out = append(out, *h)
	}
	return out
}
