package mapc

import (
	"bytes"
	"math"
	"slices"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapmm/ground"
)

func hp(v float64) *float64 { return &v }

// The test heights on CM_Ring: a 12-m mountain inside the walls west of the centre (no walkable ground near it), a
// 12-m ridge south of lane 2 (the lane's ground lies north of it, so the game camera looks over it at the lane), and
// the centre's chasm (shape 6) 15 m deep.
var (
	mountain = Shape{Type: "wall", Circle: &Point{-110, -40}, R: 30}
	ridge    = Shape{Type: "wall", Polygon: pts(-90, -216, -10, -216, -10, -188, -90, -188)}
)

func heightRing(t *testing.T, mountainH, ridgeH, chasmH *float64) *Map {
	t.Helper()
	m := ring(t)
	a, b := mountain, ridge
	a.Height, b.Height = mountainH, ridgeH
	m.add(a)
	m.add(b)
	m.Terrain.Shapes[6].Height = chasmH
	return m
}

// TestHeightFormat: "height" is written and read back; an older reading (an unknown member) refuses it; a map
// without heights writes none.
func TestHeightFormat(t *testing.T) {
	plain, err := FormatMap(ring(t))
	if err != nil || strings.Contains(string(plain), `"height"`) {
		t.Fatalf("a map without heights: %v", err)
	}
	m := heightRing(t, hp(12), nil, hp(-15))
	b, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if h := back.Terrain.Shapes[11].Height; h == nil || *h != 12 || back.Terrain.Shapes[12].Height != nil {
		t.Errorf("read back %+v", back.Terrain.Shapes[11:])
	}
	if h := back.Terrain.Shapes[6].Height; h == nil || *h != -15 {
		t.Errorf("the chasm read back %+v", back.Terrain.Shapes[6])
	}
	if _, err := Parse([]byte(strings.Replace(string(b), `"height"`, `"elevation"`, 1))); err == nil {
		t.Error("an unknown member read")
	}
	if HasHeights(ring(t)) || !HasHeights(m) {
		t.Error("HasHeights")
	}
}

// TestHeightRules: a height only on walls (1 to 40 m) and chasms (-40 to -1 m); walkable ground has none.
func TestHeightRules(t *testing.T) {
	for _, c := range []struct {
		typ string
		h   float64
		ok  bool
	}{{"wall", 12, true}, {"wall", 1, true}, {"wall", 40, true}, {"wall", 0.5, false}, {"wall", 41, false},
		{"wall", -5, false}, {"chasm", -15, true}, {"chasm", -1, true}, {"chasm", 5, false}, {"chasm", -41, false},
		{"open", 3, false}, {"island", 2, false}, {"wall", math.NaN(), false}} {
		m := ring(t)
		s := Shape{Type: c.typ, Circle: &Point{-110, -40}, R: 30, Height: hp(c.h)}
		m.add(s)
		r, _ := Check(m, nil)
		if has(r, "terrain.height", Error) == c.ok {
			t.Errorf("%s %g: %+v", c.typ, c.h, r.Findings)
		}
	}
	m := ring(t)
	m.add(Shape{Type: "bush", Polygon: pts(-186, -60, -178, -60, -178, -40), Height: hp(2)})
	if r, _ := Check(m, nil); !has(r, "terrain.height", Error) {
		t.Errorf("a bush's height: %+v", r.Findings)
	}
}

// TestHeightRelief: the compiled map's ground: the mountain 12 m up, the chasm 15 m down, every walkable cell at
// 0 as without heights, the walls far from the shapes as without heights; no height, no relief.
func TestHeightRelief(t *testing.T) {
	r, cm := Check(heightRing(t, hp(12), nil, hp(-15)), nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	_, plainCM := Check(ring(t), nil)
	if plainCM.Layers.Relief != nil || plainCM.Layers.Top != nil || plainCM.GroundSpec().Relief != nil {
		t.Fatal("a map without heights has a relief")
	}
	ly := cm.Layers
	walk := ly.GroundWalk()
	field, err := ground.NewField(walk.W, walk.H, walk.Pix, cm.Map.Size)
	if err != nil {
		t.Fatal(err)
	}
	spec, plain := cm.GroundSpec(), plainCM.GroundSpec()
	if spec.Relief == nil {
		t.Fatal("no relief")
	}
	z := func(p Point, s *ground.Spec) float64 { return ground.Height(field.At(p[0], p[1]), p[0], p[1], s) }
	if v := z(Point{-110, -40}, &spec); math.Abs(v-12) > 0.6 {
		t.Errorf("the mountain's top %g", v)
	}
	if v := z(Point{110, 40}, &spec); math.Abs(v-12) > 0.6 { // its mirror
		t.Errorf("the mirrored mountain's top %g", v)
	}
	if v := z(Point{0, 0}, &spec); v != -15 {
		t.Errorf("the chasm's bottom %g", v)
	}
	if a, b := z(Point{-200, 100}, &spec), z(Point{-200, 100}, &plain); a != b {
		t.Errorf("a far wall %g, without heights %g", a, b)
	}
	n := 0
	for k, kind := range ly.Kind {
		if !Walkable(kind) && kind != Solid {
			continue
		}
		p := ly.CellCentre(k%Grid, k/Grid)
		if v := ground.Height(field.D[k], p[0], p[1], &spec); v != 0 {
			t.Fatalf("walkable cell %v at z %g", p, v)
		}
		n++
	}
	t.Logf("%d walkable cells at z 0; the mountain %.2f, the chasm %.2f", n, z(Point{-110, -40}, &spec),
		z(Point{0, 0}, &spec))
}

// TestHeightOverlap: where shapes overlap the later one's height wins, as its kind does; a later wall without a
// height sets the default plateau.
func TestHeightOverlap(t *testing.T) {
	big := Shape{Type: "wall", Circle: &Point{-110, -40}, R: 30, Height: hp(20)}
	small := Shape{Type: "wall", Circle: &Point{-110, -40}, R: 12, Height: hp(8)}
	plain := Shape{Type: "wall", Circle: &Point{-110, -40}, R: 12}
	for _, c := range []struct {
		shapes []Shape
		want   float64
	}{{[]Shape{big, small}, 8}, {[]Shape{small, big}, 20}, {[]Shape{big, plain}, ground.Plateau}} {
		m := ring(t)
		for _, s := range c.shapes {
			m.add(s)
		}
		r, cm := Check(m, nil)
		if !r.OK {
			t.Fatalf("%+v", r.Findings)
		}
		ly := cm.Layers
		i, j := ly.CellOf(Point{-110, -40})
		if got := ly.Top[j*Grid+i]; got != c.want || ly.TopShape[j*Grid+i] != len(m.Terrain.Shapes)-1 {
			t.Errorf("%v then %v: top %g (shape %d), want %g", c.shapes[0].Height, c.shapes[1].Height, got,
				ly.TopShape[j*Grid+i], c.want)
		}
		if got := ly.Relief.Top(-110, -40, false); got != c.want {
			t.Errorf("the blended top %g, want %g", got, c.want)
		}
		// the ring between the circles keeps the big one's
		if got := ly.Relief.Top(-110, -20, false); got != 20 {
			t.Errorf("between the circles %g", got)
		}
	}
}

// TestHeightHide: a 12-m ridge south of lane 2 warns (the camera looks north over it at the lane); at 5 m it
// doesn't, nor does the mountain with no walkable ground north of it; the limit comes from the camera.
func TestHeightHide(t *testing.T) {
	if s := HideSafeHeight(); s < 5.2 || s > 5.6 {
		t.Errorf("HideSafeHeight %g", s)
	}
	if HideReach(ground.Plateau) != 0 || HideReach(12) < 3 || HideReach(12) > 6 {
		t.Errorf("HideReach 5: %g, 12: %g", HideReach(ground.Plateau), HideReach(12))
	}
	if math.Abs(HideAngle-36.5) > 1e-9 {
		t.Errorf("HideAngle %g", HideAngle)
	}
	r, _ := Check(heightRing(t, hp(12), hp(12), hp(-15)), nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	var hide []Finding
	for _, f := range r.Findings {
		if f.Rule == "terrain.height-hide" {
			hide = append(hide, f)
		}
	}
	if len(hide) != 1 || hide[0].Path != "terrain.shapes[12]" || hide[0].Severity != Warning || hide[0].At == nil ||
		hide[0].At[0]+hide[0].At[1] > 0 {
		t.Fatalf("hide findings %+v", hide)
	}
	at := *hide[0].At
	if at[0] < -95 || at[0] > -5 || at[1] < -190 || at[1] > -170 {
		t.Errorf("hidden at %v: not on lane 2 north of the ridge", at)
	}
	t.Logf("%s", hide[0].Message)
	for _, h := range []*float64{hp(5), nil} {
		r, _ := Check(heightRing(t, hp(12), h, hp(-15)), nil)
		if has(r, "terrain.height-hide", Warning) {
			t.Errorf("ridge %v: %+v", h, r.Findings)
		}
	}
}

// TestHeightVoid: a chasm deeper than the void's backdrop warns.
func TestHeightVoid(t *testing.T) {
	m := heightRing(t, nil, nil, hp(-15))
	m.Void = &Void{Backdrop: &VoidBackdrop{Z: -10, Texture: TextureRef{RGB: &[3]float64{0, 0, 0}}}}
	r, _ := Check(m, nil)
	if !has(r, "terrain.height-void", Warning) {
		t.Errorf("%+v", r.Findings)
	}
	m.Void.Backdrop.Z = -20
	if r, _ := Check(m, nil); has(r, "terrain.height-void", Warning) {
		t.Errorf("%+v", r.Findings)
	}
}

// TestHeightBakePlateau: the made-up baked CM_Ring with heights that are all the default (a 5-m wall shape, the
// chasm at -5) bakes byte for byte as the pinned one without heights; at 12 and -15 it differs.
func TestHeightBakePlateau(t *testing.T) {
	run := func(wall, chasm float64) string {
		m, read := madeUpBaked(t)
		s := mountain
		s.Height = hp(wall)
		m.add(s)
		m.Terrain.Shapes[6].Height = hp(chasm)
		r, cm := Check(m, nil)
		if !r.OK {
			t.Fatalf("%+v", r.Findings)
		}
		if cm.Layers.Relief == nil {
			t.Fatal("no relief")
		}
		files, mm, err := cm.GroundFiles(read)
		if err != nil {
			t.Fatal(err)
		}
		var all bytes.Buffer
		for _, f := range files {
			all.WriteString(digest(f.Data))
		}
		all.WriteString(digest(mm))
		return digest(all.Bytes())
	}
	if got := run(ground.Plateau, -ground.Plateau); got != RingBakedMadeUpSHA256 {
		t.Errorf("all heights at the default: %s, pinned %s", got, RingBakedMadeUpSHA256)
	}
	if got := run(12, -15); got == RingBakedMadeUpSHA256 {
		t.Error("12 and -15 bake as the default")
	}
}

// TestHeightStep: CM_Ring's centre chasm is cut into the walls; at -15 the ground drops about 20 m from the plateau
// to its floor in one step there, and the check warns on the chasm (terrain.height-step); with an open ring under it
// the drop starts at the walkable edge and nothing warns. Without heights the default chasm still steps (about 10 m)
// but the check stays as before (the report is part of the archive).
func TestHeightStep(t *testing.T) {
	m := heightRing(t, nil, nil, hp(-15))
	r, cm := Check(m, nil)
	if !r.OK || !has(r, "terrain.height-step", Warning) {
		t.Fatalf("%+v", r.Findings)
	}
	steps := cm.ChasmSteps()
	if len(steps) != 1 || steps[0].Shape != 6 || steps[0].Drop < 19 || steps[0].Drop > 21 || steps[0].At[0]+steps[0].At[1] > 0 {
		t.Errorf("%+v", steps)
	}
	for _, f := range r.Findings {
		if f.Rule == "terrain.height-step" && f.Path != "terrain.shapes[6]" {
			t.Errorf("%+v", f)
		}
	}
	rim := heightRing(t, nil, nil, hp(-15))
	rim.Terrain.Shapes = slices.Insert(rim.Terrain.Shapes, 6, Shape{Type: "open", Circle: &Point{0, 0}, R: 48})
	r, cm = Check(rim, nil)
	if !r.OK || has(r, "terrain.height-step", Warning) || len(cm.ChasmSteps()) != 0 {
		t.Errorf("with a rim: %+v", r.Findings)
	}
	plain := ring(t)
	r, cm = Check(plain, nil)
	if has(r, "terrain.height-step", Warning) {
		t.Errorf("without heights: %+v", r.Findings)
	}
	if s := cm.ChasmSteps(); len(s) != 1 || s[0].Drop < 9.5 || s[0].Drop > 11 {
		t.Errorf("without heights: %+v", s)
	}
}
