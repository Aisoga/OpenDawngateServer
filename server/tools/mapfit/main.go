// Command mapfit compares an imported map package with the shipped level it came from (developers): where the two
// walk differently, where their ground lies at another height, and how near the walkable ground comes to the
// level's props on each. The level's side is MM_Map_v3 as the setup builds it (its line-of-sight map, its Player
// graph of 2 x 2-cell squares, its ground); the package's side is the compiled package.
//
// Usage (from server\):
//
//	go run ./tools/mapfit --copy <copy> [--px 4] [--region name:x,y,r]... <package> <out>
//
// It writes <out>/<name>.png for every region (the default regions: the whole map and a few of MM_Map_v3's places),
// and prints the counts. The pictures, north up:
//   - light grey: both Player graphs; orange: only the level's; blue: only the package's;
//   - dark: blocked in the level's line-of-sight map; mid grey: open there but on neither graph;
//   - green outlines: the props' boxes where they stand (the package's placements, which are the level's markers);
//   - red dots: both teams' objects (Locus, Guardian, shop zone, spawn points, wells).
//
// <name>_height.png: the ground's height, the package's less the level's (red: higher, blue: lower, by 1 m steps
// to 5 m), with the package's Player graph's outline.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/mapmm/los"
	"dawngate/server/internal/setup/mapmm/navgraph"
)

type region struct {
	name    string
	x, y, r float64
}

type regions []region

func (rs *regions) String() string { return fmt.Sprint(*rs) }

func (rs *regions) Set(s string) error {
	name, rest, ok := strings.Cut(s, ":")
	if !ok {
		return fmt.Errorf("a region is name:x,y,r")
	}
	parts := strings.Split(rest, ",")
	if len(parts) != 3 {
		return fmt.Errorf("a region is name:x,y,r")
	}
	var v [3]float64
	for k, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return err
		}
		v[k] = f
	}
	*rs = append(*rs, region{name, v[0], v[1], v[2]})
	return nil
}

// the default regions: the whole map, the centre, team 1's base round the shop and its mine
var (
	probes [][2]float64
	track  [][]float64
)

var defaults = regions{{"map", 0, 0, 292}, {"centre", 0, 0, 45}, {"base", -200, -200, 50}, {"mine", -255, -255, 45}}

// grids are the two sides' cells, both 512 x 512 over the same size (rows from the south)
type side struct {
	los   []byte // 0 open, else blocked
	graph []bool // the cell lies under a Player-graph square
	field *ground.Field
	spec  ground.Spec
}

func cellsUnder(blk []bool, bw, cell, w, h int) []bool {
	out := make([]bool, w*h)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			bx, by := i/cell, j/cell
			if bx < bw && by < h/cell && blk[by*bw+bx] {
				out[j*w+i] = true
			}
		}
	}
	return out
}

func main() {
	copyDir := flag.String("copy", "", "the game copy (the folder with game\\)")
	px := flag.Float64("px", 4, "pixels a metre in the regions (the whole map at 1.5)")
	var rs regions
	flag.Var(&rs, "region", "name:x,y,r (repeatable; default the map, centre, base and mine)")
	probe := flag.String("probe", "", "x,y;x,y...: print both sides' cells at these points")
	trackFile := flag.String("track", "", "a file of lines \"x y [gx gy]\": a unit's positions (cyan) and goals (magenta)")
	flag.Parse()
	if *trackFile != "" {
		b, err := os.ReadFile(*trackFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mapfit:", err)
			os.Exit(2)
		}
		for _, ln := range strings.Split(string(b), "\n") {
			var v []float64
			for _, f := range strings.Fields(ln) {
				x, err := strconv.ParseFloat(f, 64)
				if err == nil {
					v = append(v, x)
				}
			}
			if len(v) >= 2 {
				track = append(track, v)
			}
		}
	}
	for _, s := range strings.Split(*probe, ";") {
		if a, b, ok := strings.Cut(s, ","); ok {
			x, _ := strconv.ParseFloat(a, 64)
			y, _ := strconv.ParseFloat(b, 64)
			probes = append(probes, [2]float64{x, y})
		}
	}
	if *copyDir == "" || flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: mapfit --copy <copy> [--px 4] [--region name:x,y,r]... <package> <out>")
		os.Exit(2)
	}
	if len(rs) == 0 {
		rs = defaults
	}
	if err := run(*copyDir, flag.Arg(0), flag.Arg(1), *px, rs); err != nil {
		fmt.Fprintln(os.Stderr, "mapfit:", err)
		os.Exit(1)
	}
}

func run(copyDir, pkg, out string, px float64, rs regions) error {
	ad, err := os.ReadFile(filepath.Join(copyDir, filepath.FromSlash(mapc.AssetDataSource)))
	if err != nil {
		return err
	}
	images, err := os.ReadFile(filepath.Join(copyDir, "game", "Data", los.Archive))
	if err != nil {
		return err
	}
	raw, err := los.FromArchive(images, los.Entry)
	if err != nil {
		return err
	}
	img, err := los.Read(raw)
	if err != nil {
		return err
	}
	m, err := mapc.Load(pkg)
	if err != nil {
		return err
	}
	if err := mapc.ResolveProps(m, ad); err != nil {
		return err
	}
	cm, err := mapc.Compile(m)
	if err != nil {
		return err
	}
	const w = mapc.Grid
	if img.W != w || img.H != w || math.Abs(m.Size-los.Size) > 1e-9 {
		return fmt.Errorf("the package's grid (%d, %.3f m) isn't MM_Map_v3's (%d, %.3f m)", w, m.Size, img.W, los.Size)
	}

	// the level's side: MM_Map_v3's graph as the setup builds it (navgraph.MM), and the same with the package's cell
	spec := navgraph.MM
	holes := []navgraph.Hole{}
	for _, g := range spec.Guardians {
		holes = append(holes, navgraph.Hole{X: g.X, Y: g.Y, R: spec.HoleRadius})
	}
	mask := navgraph.WalkableMask(img.Pix, w, w, spec.Erode)
	blk, bw, _ := navgraph.KeptBlocks(mask, w, w, spec.SizeX, spec.SizeY, spec.Cell, spec.Keep, holes)
	lvl := side{los: img.Pix, graph: cellsUnder(blk, bw, spec.Cell, w, w), spec: ground.MM}
	blk3, bw3, _ := navgraph.KeptBlocks(mask, w, w, spec.SizeX, spec.SizeY, cm.Cell, spec.Keep, holes)
	lvlCell := cellsUnder(blk3, bw3, cm.Cell, w, w)
	if lvl.field, err = ground.NewField(w, w, img.Pix, m.Size); err != nil {
		return err
	}
	// the package's side
	pk := side{los: cm.Layers.LoS().Pix, graph: cellsUnder(cm.Player.On, cm.Player.W, cm.Cell, w, w),
		spec: cm.GroundSpec()}
	gw := cm.Layers.GroundWalk()
	if pk.field, err = ground.NewField(w, w, gw.Pix, m.Size); err != nil {
		return err
	}
	// the package's graph cut from the level's own line of sight (the package's cell): what the tracing costs
	lvlMaskCM := navgraph.WalkableMask(img.Pix, w, w, mapc.Erode)
	_ = lvlMaskCM

	cellM := m.Size / w
	fmt.Printf("cell %.4f m; the level's graph: squares of %d cells, %d areas; the package's: %d cells, %d areas\n",
		cellM, spec.Cell, count(blk), cm.Cell, cm.Player.Areas)
	both, onlyL, onlyP := 0, 0, 0
	onlyLcell, onlyLtrace := 0, 0
	for k := range lvl.graph {
		switch {
		case lvl.graph[k] && pk.graph[k]:
			both++
		case lvl.graph[k]:
			onlyL++
			if !lvlCell[k] {
				onlyLcell++ // the level's own map at the package's square size loses it too
			} else {
				onlyLtrace++
			}
		case pk.graph[k]:
			onlyP++
		}
	}
	fmt.Printf("walkable cells: both %d, only the level's %d (%.1f %% of its %d: %d lost by the square size alone, "+
		"%d by the tracing and the mirror), only the package's %d\n", both, onlyL,
		100*float64(onlyL)/float64(both+onlyL), both+onlyL, onlyLcell, onlyLtrace, onlyP)
	fmt.Printf("the level's map at %d-cell squares: %d areas, %d cells\n", cm.Cell, count(blk3), count(lvlCell))

	placed, _, err := cm.PlacedProps(ad)
	if err != nil {
		return err
	}
	nouns, err := mapc.PropNouns(ad)
	if err != nil {
		return err
	}
	boxes := map[string]mapc.PropNoun{}
	for _, n := range nouns {
		boxes[strings.ToLower(strings.TrimSuffix(n.Name, ".Noun"))] = n
	}
	gaps(placed, boxes, lvl, pk, cellM, m.Size)

	for _, p := range probes {
		i, j := cellAt(p[0], p[1], cellM)
		k := j*w + i
		fmt.Printf("probe (%.1f, %.1f) cell (%d, %d): the level los %d graph %v; the package kind %d los %d mask %v "+
			"graph %v; heights %.2f / %.2f\n", p[0], p[1], i, j, lvl.los[k], lvl.graph[k], cm.Layers.Kind[k],
			pk.los[k], cm.Mask[k], pk.graph[k], ground.Height(lvl.field.At(p[0], p[1]), p[0], p[1], &lvl.spec),
			ground.Height(pk.field.At(p[0], p[1]), p[0], p[1], &pk.spec))
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, r := range rs {
		scale := px
		if r.name == "map" {
			scale = 1.5
		}
		pic := draw(r, scale, lvl, pk, cm, placed, boxes, cellM, m.Size)
		if err := writePNG(filepath.Join(out, r.name+".png"), pic); err != nil {
			return err
		}
		hp := heights(r, scale, lvl, pk, cellM, m.Size)
		if err := writePNG(filepath.Join(out, r.name+"_height.png"), hp); err != nil {
			return err
		}
		regionCounts(r, lvl, pk, lvlCell, cellM, m.Size)
	}
	return nil
}

func count(b []bool) int {
	n := 0
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}

func cellAt(x, y, cellM float64) (int, int) {
	return int(math.Floor(x/cellM + mapc.Grid/2 + 0.5)), int(math.Floor(y/cellM + mapc.Grid/2 + 0.5))
}

func inGrid(i, j int) bool { return i >= 0 && j >= 0 && i < mapc.Grid && j < mapc.Grid }

// propCorners is a prop's box (x, y) turned and scaled to where it stands
func propCorners(p mapc.Placed, n mapc.PropNoun) [4][2]float64 {
	s := p.Scale
	if s == 0 {
		s = 1
	}
	g := n.GraphicsScale
	if g == 0 {
		g = 1
	}
	s *= g
	a := p.Rot[2] * math.Pi / 180
	c, sn := math.Cos(a), math.Sin(a)
	var out [4][2]float64
	for k, q := range [4][2]float64{{n.Box.Min[0], n.Box.Min[1]}, {n.Box.Max[0], n.Box.Min[1]},
		{n.Box.Max[0], n.Box.Max[1]}, {n.Box.Min[0], n.Box.Max[1]}} {
		x, y := q[0]*s, q[1]*s
		out[k] = [2]float64{p.Pos[0] + x*c - y*sn, p.Pos[1] + x*sn + y*c}
	}
	return out
}

// gaps: for every obstacle prop standing within 8 m of either side's walkable ground, how far the nearest graph cell
// lies from its centre on each side
func gaps(placed []mapc.Placed, boxes map[string]mapc.PropNoun, lvl, pk side, cellM, size float64) {
	near := func(s side, x, y float64) float64 {
		best := 1e9
		ci, cj := cellAt(x, y, cellM)
		rc := int(12/cellM) + 1
		for dj := -rc; dj <= rc; dj++ {
			for di := -rc; di <= rc; di++ {
				i, j := ci+di, cj+dj
				if !inGrid(i, j) || !s.graph[j*mapc.Grid+i] {
					continue
				}
				cx, cy := float64(i-mapc.Grid/2)*cellM, float64(j-mapc.Grid/2)*cellM
				best = math.Min(best, math.Hypot(cx-x, cy-y))
			}
		}
		return best
	}
	var dl, dp []float64
	more := 0
	for _, p := range placed {
		n, ok := boxes[strings.ToLower(strings.TrimSuffix(p.Noun, ".Noun"))]
		if !ok || !strings.EqualFold(n.Type, "obstacle") {
			continue
		}
		a, b := near(lvl, p.Pos[0], p.Pos[1]), near(pk, p.Pos[0], p.Pos[1])
		if a > 8 && b > 8 {
			continue
		}
		dl, dp = append(dl, math.Min(a, 12)), append(dp, math.Min(b, 12))
		if b > a+0.5 {
			more++
		}
	}
	med := func(v []float64) float64 {
		s := append([]float64{}, v...)
		sort.Float64s(s)
		if len(s) == 0 {
			return 0
		}
		return s[len(s)/2]
	}
	mean := func(v []float64) float64 {
		t := 0.0
		for _, x := range v {
			t += x
		}
		return t / math.Max(1, float64(len(v)))
	}
	fmt.Printf("obstacle props within 8 m of walkable ground: %d; the nearest graph cell from a prop's centre: "+
		"the level median %.2f m (mean %.2f), the package median %.2f m (mean %.2f); farther by > 0.5 m on the "+
		"package: %d\n", len(dl), med(dl), mean(dl), med(dp), mean(dp), more)
}

func regionCounts(r region, lvl, pk side, lvlCell []bool, cellM, size float64) {
	both, onlyL, onlyP, onlyLcell := 0, 0, 0, 0
	losL, losP := 0, 0
	hi, lo := 0, 0
	for j := 0; j < mapc.Grid; j++ {
		for i := 0; i < mapc.Grid; i++ {
			x, y := float64(i-mapc.Grid/2)*cellM, float64(j-mapc.Grid/2)*cellM
			if math.Hypot(x-r.x, y-r.y) > r.r {
				continue
			}
			k := j*mapc.Grid + i
			switch {
			case lvl.graph[k] && pk.graph[k]:
				both++
			case lvl.graph[k]:
				onlyL++
				if !lvlCell[k] {
					onlyLcell++
				}
			case pk.graph[k]:
				onlyP++
			}
			if lvl.los[k] == 0 && pk.los[k] != 0 {
				losL++
			}
			if lvl.los[k] != 0 && pk.los[k] == 0 {
				losP++
			}
			d := ground.Height(pk.field.At(x, y), x, y, &pk.spec) - ground.Height(lvl.field.At(x, y), x, y, &lvl.spec)
			if d > 0.5 {
				hi++
			} else if d < -0.5 {
				lo++
			}
		}
	}
	fmt.Printf("%s (%.0f, %.0f) r %.0f: walkable both %d, only the level %d (%d by the square size), only the "+
		"package %d; line of sight: open only on the level %d, only on the package %d; ground > 0.5 m higher on the "+
		"package %d cells, lower %d\n", r.name, r.x, r.y, r.r, both, onlyL, onlyLcell, onlyP, losL, losP, hi, lo)
}

type canvas struct {
	img      *image.RGBA
	r        region
	scale    float64
	n        int
	cellM    float64
	gridSize float64
}

func newCanvas(r region, scale, cellM, size float64) *canvas {
	n := int(2 * r.r * scale)
	return &canvas{img: image.NewRGBA(image.Rect(0, 0, n, n)), r: r, scale: scale, n: n, cellM: cellM, gridSize: size}
}

// world is pixel (px, py)'s world point (north up)
func (c *canvas) world(px, py int) (float64, float64) {
	return c.r.x - c.r.r + (float64(px)+0.5)/c.scale, c.r.y + c.r.r - (float64(py)+0.5)/c.scale
}

func (c *canvas) pixel(x, y float64) (int, int) {
	return int(math.Floor((x - c.r.x + c.r.r) * c.scale)), int(math.Floor((c.r.y + c.r.r - y) * c.scale))
}

func (c *canvas) set(px, py int, col color.RGBA) {
	if px >= 0 && py >= 0 && px < c.n && py < c.n {
		c.img.SetRGBA(px, py, col)
	}
}

func (c *canvas) line(x0, y0, x1, y1 float64, col color.RGBA) {
	steps := int(math.Hypot(x1-x0, y1-y0)*c.scale*2) + 1
	for s := 0; s <= steps; s++ {
		t := float64(s) / float64(steps)
		px, py := c.pixel(x0+(x1-x0)*t, y0+(y1-y0)*t)
		c.set(px, py, col)
	}
}

func (c *canvas) dot(x, y, rm float64, col color.RGBA) {
	cx, cy := c.pixel(x, y)
	rp := int(math.Max(2, rm*c.scale))
	for dy := -rp; dy <= rp; dy++ {
		for dx := -rp; dx <= rp; dx++ {
			if dx*dx+dy*dy <= rp*rp {
				c.set(cx+dx, cy+dy, col)
			}
		}
	}
}

func draw(r region, scale float64, lvl, pk side, cm *mapc.Compiled, placed []mapc.Placed,
	boxes map[string]mapc.PropNoun, cellM, size float64) *image.RGBA {
	c := newCanvas(r, scale, cellM, size)
	for py := 0; py < c.n; py++ {
		for px := 0; px < c.n; px++ {
			x, y := c.world(px, py)
			i, j := cellAt(x, y, cellM)
			col := color.RGBA{0, 0, 0, 255}
			if inGrid(i, j) {
				k := j*mapc.Grid + i
				switch {
				case lvl.graph[k] && pk.graph[k]:
					col = color.RGBA{205, 205, 200, 255}
				case lvl.graph[k]:
					col = color.RGBA{240, 130, 20, 255}
				case pk.graph[k]:
					col = color.RGBA{40, 110, 235, 255}
				case lvl.los[k] == 0:
					col = color.RGBA{95, 95, 95, 255}
				default:
					col = color.RGBA{35, 35, 35, 255}
				}
				if pk.los[k] == 0 && lvl.los[k] != 0 && !pk.graph[k] { // open only in the package's LoS
					col = color.RGBA{70, 70, 120, 255}
				}
			}
			c.set(px, py, col)
		}
	}
	for _, p := range placed {
		n, ok := boxes[strings.ToLower(strings.TrimSuffix(p.Noun, ".Noun"))]
		if !ok || n.Box.Max[0]-n.Box.Min[0] <= 0 {
			continue
		}
		if math.Hypot(p.Pos[0]-r.x, p.Pos[1]-r.y) > r.r+40 {
			continue
		}
		col := color.RGBA{60, 200, 60, 255}
		if !strings.EqualFold(n.Type, "obstacle") {
			col = color.RGBA{150, 220, 150, 255}
		}
		q := propCorners(p, n)
		for k := 0; k < 4; k++ {
			a, b := q[k], q[(k+1)%4]
			c.line(a[0], a[1], b[0], b[1], col)
		}
	}
	for t := range 2 {
		tm := cm.Layout.Teams[t]
		pts := append([]mapc.Point{tm.Locus, tm.Guardian, tm.Shop}, tm.Spawns...)
		pts = append(pts, tm.Wells...)
		for _, p := range pts {
			c.dot(p[0], p[1], 0.8, color.RGBA{230, 30, 30, 255})
		}
	}
	for _, v := range track {
		if len(v) >= 4 && (v[2] != 0 || v[3] != 0) {
			c.dot(v[2], v[3], 0.25, color.RGBA{230, 40, 230, 255})
		}
		c.dot(v[0], v[1], 0.25, color.RGBA{0, 230, 230, 255})
	}
	return c.img
}

// heights: the package's ground less the level's, red higher, blue lower, the package's graph outlined in black
func heights(r region, scale float64, lvl, pk side, cellM, size float64) *image.RGBA {
	c := newCanvas(r, scale, cellM, size)
	for py := 0; py < c.n; py++ {
		for px := 0; px < c.n; px++ {
			x, y := c.world(px, py)
			zl := ground.Height(lvl.field.At(x, y), x, y, &lvl.spec)
			zp := ground.Height(pk.field.At(x, y), x, y, &pk.spec)
			d := math.Max(-5, math.Min(5, zp-zl))
			t := math.Abs(d) / 5
			base := 230 - 40*math.Min(1, math.Max(0, zl/5)) // the level's own height as a grey
			var col color.RGBA
			if d >= 0 {
				col = color.RGBA{uint8(base), uint8(base * (1 - t)), uint8(base * (1 - t)), 255}
			} else {
				col = color.RGBA{uint8(base * (1 - t)), uint8(base * (1 - t)), uint8(base), 255}
			}
			i, j := cellAt(x, y, cellM)
			if inGrid(i, j) {
				k := j*mapc.Grid + i
				edge := false
				for _, nb := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					ii, jj := i+nb[0], j+nb[1]
					if inGrid(ii, jj) && pk.graph[jj*mapc.Grid+ii] != pk.graph[k] {
						edge = true
					}
				}
				if edge && pk.graph[k] {
					col = color.RGBA{0, 0, 0, 255}
				}
			}
			c.set(px, py, col)
		}
	}
	return c.img
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
