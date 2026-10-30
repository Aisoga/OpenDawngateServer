package mapc

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"

	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/mapmm/tagmap"
)

// The preview: pictures of the derived layers (PNG, 512 x 512 unless noted, north up: pixel (px, py) shows world
// ((px + 0.5) / 512 - 0.5) * size, (0.5 - (py + 0.5) / 512) * size) and preview.json, which lists them with every
// derived object, the lanes, the bushes' outlines, the check's report and the numbers, so that a web page can draw
// and label them.

// PreviewObject is one placed object of the compiled map.
type PreviewObject struct {
	Kind string `json:"kind"` // locus, guardian, shop, spawn, core, binding, spawner, well, camp
	Team int    `json:"team"` // 1 or 2
	Name string `json:"name"` // e.g. "Core A", "lane 1 Outer"
	Lane int    `json:"lane,omitempty"`
	Pos  Point  `json:"pos"`
	Path string `json:"path,omitempty"` // where team 1's object comes from in map.json
}

// PreviewLane is one team's lane path.
type PreviewLane struct {
	Team  int     `json:"team"`
	ID    int     `json:"id"`
	Width float64 `json:"width"`
	Knots []Point `json:"knots"`
}

// PreviewBush is one bush patch.
type PreviewBush struct {
	Name    string  `json:"name"`
	ID      string  `json:"id"` // the grass id, hex
	Shape   int     `json:"shape"`
	Team    int     `json:"team"` // 1 drawn, 2 mirrored
	Outline []Point `json:"outline"`
}

// PreviewFile is one picture: its file name, what it shows, its size.
type PreviewFile struct {
	File   string `json:"file"`
	Shows  string `json:"shows"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// PreviewIndex is preview.json.
type PreviewIndex struct {
	Map     string          `json:"map"`
	Size    float64         `json:"size"`
	Grid    int             `json:"grid"`
	Pixel   string          `json:"pixel"`
	Files   []PreviewFile   `json:"files"`
	Objects []PreviewObject `json:"objects"`
	Lanes   []PreviewLane   `json:"lanes"`
	Bushes  []PreviewBush   `json:"bushes"`
	Islands []Point         `json:"islands"` // the NPC graph's Guardian islands
	Report  *Report         `json:"report"`

	Props []Placed     `json:"props,omitempty"` // the props placed without the copy (Placements, props.go)
	Water []WaterPlane `json:"water,omitempty"` // the chasms' water planes (water.go)

	// the bots' derived points: the parasite rally and the Hunter routes, as the match host derives them (bots.go)
	Bots *BotPoints `json:"bots,omitempty"`
}

// Objects are both teams' objects.
func (cm *Compiled) Objects() []PreviewObject {
	var out []PreviewObject
	for t := range cm.Layout.Teams {
		team := &cm.Layout.Teams[t]
		add := func(kind, name string, lane int, p Point, path string) {
			if t == 1 {
				path = ""
			}
			out = append(out, PreviewObject{Kind: kind, Team: t + 1, Name: name, Lane: lane, Pos: p, Path: path})
		}
		add("locus", "Locus", 0, team.Locus, "base.locus")
		add("guardian", "Guardian", 0, team.Guardian, "base.guardian")
		add("shop", "shop", 0, team.Shop, "base.shop")
		for k, p := range team.Spawns {
			add("spawn", "spawn "+string(rune('1'+k)), 0, p, "base.spawns["+string(rune('0'+k))+"]")
		}
		for k, p := range team.Cores {
			add("core", "Core "+string(rune('A'+k)), 0, p, "base.cores")
		}
		for _, id := range cm.Layout.LaneIDs {
			for k, p := range team.Bindings[id] {
				add("binding", "lane "+string(rune('0'+id))+" "+TierNames(len(team.Bindings[id]))[k], id, p,
					"base.bindings")
			}
			add("spawner", "lane "+string(rune('0'+id))+" minion spawner", id, team.Spawners[id], cm.lanePath(id))
		}
		for k, p := range team.Wells {
			add("well", "well", 0, p, "wells["+itoa(k)+"].pos")
		}
		for k, c := range team.Camps {
			add("camp", c.Kind+" "+c.Noun, 0, c.Pos, "camps["+itoa(k)+"].pos")
		}
	}
	return append(out, cm.wardObjects()...) // wards.go
}

func itoa(n int) string { return string(appendInt(nil, n)) }

func appendInt(b []byte, n int) []byte {
	if n >= 10 {
		b = appendInt(b, n/10)
	}
	return append(b, byte('0'+n%10))
}

// Preview makes the preview's files: name -> bytes (the PNGs and preview.json).
func (cm *Compiled) Preview(r *Report) (map[string][]byte, error) {
	files := map[string][]byte{}
	var idx PreviewIndex
	idx.Map, idx.Size, idx.Grid, idx.Report = cm.Map.Name, cm.Map.Size, Grid, r
	idx.Pixel = "px = (x / size + 0.5) * grid - 0.5, py = (0.5 - y / size) * grid - 0.5 (north up)"
	put := func(name, shows string, img image.Image) error {
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return err
		}
		files[name] = b.Bytes()
		idx.Files = append(idx.Files, PreviewFile{File: name, Shows: shows, Width: img.Bounds().Dx(),
			Height: img.Bounds().Dy()})
		return nil
	}
	ly := cm.Layers
	// the layout
	layout := cm.cellImage(func(k int) color.RGBA {
		switch ly.Kind[k] {
		case Open:
			return color.RGBA{196, 186, 160, 255}
		case Solid:
			return color.RGBA{120, 84, 52, 255}
		case Chasm:
			return color.RGBA{40, 60, 110, 255}
		case Island:
			return color.RGBA{183, 177, 194, 255} // MM_Map_v3's minimap colour for its NPC-only islands
		}
		return color.RGBA{70, 62, 58, 255}
	})
	for _, b := range ly.Bushes {
		for _, k := range b.Cells {
			px, py := k%Grid, Grid-1-k/Grid
			c := layout.RGBAAt(px, py)
			layout.SetRGBA(px, py, color.RGBA{c.R / 3, c.G/3 + 110, c.B / 3, 255})
		}
	}
	if err := put("layout.png", "the ground kinds (open, wall, chasm) and the bushes", layout); err != nil {
		return nil, err
	}
	overview := image.NewRGBA(layout.Bounds())
	copy(overview.Pix, layout.Pix)
	cm.drawObjects(overview)
	cm.drawWater(overview) // water.go
	if err := put("objects.png", "the layout with the lanes and every object of both teams", overview); err != nil {
		return nil, err
	}
	losImg := ly.LoS()
	if err := put("los.png", "the line-of-sight map (black blocks sight)", cm.cellImage(func(k int) color.RGBA {
		if losImg.Pix[k] != 0 {
			return color.RGBA{0, 0, 0, 255}
		}
		return color.RGBA{255, 255, 255, 255}
	})); err != nil {
		return nil, err
	}
	grass := ly.GrassCells()
	if err := put("grass.png", "the grass map (each bush's id; its alpha-0 ring darker)", cm.cellImage(func(k int) color.RGBA {
		v, ok := grass[k]
		switch {
		case !ok:
			return color.RGBA{0, 0, 0, 255}
		case v>>24 == 0:
			return color.RGBA{byte(v>>16) / 3, byte(v>>8) / 3, byte(v) / 3, 255}
		}
		return color.RGBA{byte(v >> 16), byte(v >> 8), byte(v), 255}
	})); err != nil {
		return nil, err
	}
	for _, g := range []struct {
		name, shows string
		b           *Blocks
		islands     bool
	}{{"graph_player.png", "the Player graph's squares over the walkable cells", &cm.Player, false},
		{"graph_npc.png", "the NPC graph's squares and the Guardians' islands", &cm.NPC, true}} {
		img := cm.graphImage(g.b, g.islands)
		if err := put(g.name, g.shows, img); err != nil {
			return nil, err
		}
	}
	tagImg, err := cm.tagImage()
	if err != nil {
		return nil, err
	}
	if err := put("tags.png", "the tag map: sides (red/blue tint), Base (dark), Top (yellow) and Bottom (cyan) lane, "+
		"Jungle (green), Locus, BindingRange, SpiritWell, the buffs (green, blue, orange), MoneyPigs (pink), Parasite "+
		"(violet)", tagImg); err != nil {
		return nil, err
	}
	rgba, err := cm.MinimapRGBA()
	if err != nil {
		return nil, err
	}
	mm := image.NewRGBA(image.Rect(0, 0, Grid, Grid))
	copy(mm.Pix, rgba)
	if err := put("minimap.png", "the minimap (before its DXT5 compression)", mm); err != nil {
		return nil, err
	}
	gimg, err := cm.groundImage()
	if err != nil {
		return nil, err
	}
	if err := put("ground.png", "the ground model's heights, shaded and in its vertex colours (321 x 321 vertices)",
		gimg); err != nil {
		return nil, err
	}
	idx.Objects = cm.Objects()
	for t := range cm.Layout.Teams {
		for _, id := range cm.Layout.LaneIDs {
			idx.Lanes = append(idx.Lanes, PreviewLane{Team: t + 1, ID: id, Width: cm.laneWidth(id),
				Knots: cm.Layout.Teams[t].Lanes[id]})
		}
	}
	for _, b := range ly.Bushes {
		team := 1
		if b.Mirrored {
			team = 2
		}
		idx.Bushes = append(idx.Bushes, PreviewBush{Name: b.Name, ID: hexID(b.ID), Shape: b.Shape, Team: team,
			Outline: b.Outline})
	}
	idx.Props = cm.Placements()                           // props.go
	if idx.Water, _, err = cm.WaterPlanes(); err != nil { // water.go
		return nil, err
	}
	for _, g := range cm.Guardians() {
		idx.Islands = append(idx.Islands, Point{g.X, g.Y})
	}
	idx.Bots = cm.Bots() // bots.go
	j, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, err
	}
	files["preview.json"] = append(j, '\n')
	return files, nil
}

func hexID(v uint32) string {
	const digits = "0123456789ABCDEF"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = digits[v&15]
		v >>= 4
	}
	return string(b)
}

// cellImage paints each cell (k = j*Grid+i, j from the bottom) north up.
func (cm *Compiled) cellImage(col func(k int) color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, Grid, Grid))
	for j := 0; j < Grid; j++ {
		for i := 0; i < Grid; i++ {
			img.SetRGBA(i, Grid-1-j, col(j*Grid+i))
		}
	}
	return img
}

func (cm *Compiled) pixel(p Point) (int, int) {
	i, j := cm.Layers.CellOf(p)
	return i, Grid - 1 - j
}

func (cm *Compiled) graphImage(b *Blocks, islands bool) *image.RGBA {
	mask := cm.Mask
	if islands { // the NPC graph: over the ground NPCs walk
		mask = cm.NPCMask
	}
	img := cm.cellImage(func(k int) color.RGBA {
		if mask[k] {
			return color.RGBA{90, 90, 90, 255}
		}
		return color.RGBA{20, 20, 20, 255}
	})
	cell := cm.Cell
	for by := 0; by < b.H; by++ {
		for bx := 0; bx < b.W; bx++ {
			if !b.On[by*b.W+bx] {
				continue
			}
			for dj := 0; dj < cell; dj++ {
				for di := 0; di < cell; di++ {
					c := color.RGBA{120, 200, 120, 255}
					if di == 0 || dj == 0 { // the squares' edges
						c = color.RGBA{60, 140, 60, 255}
					}
					img.SetRGBA(bx*cell+di, Grid-1-(by*cell+dj), c)
				}
			}
		}
	}
	if islands {
		for _, g := range cm.Guardians() {
			disc(img, cm, Point{g.X, g.Y}, IslandRadius, color.RGBA{230, 200, 60, 255})
		}
	}
	return img
}

func disc(img *image.RGBA, cm *Compiled, c Point, r float64, col color.RGBA) {
	cs := cm.Map.Size / Grid
	px, py := cm.pixel(c)
	n := int(math.Ceil(r / cs))
	for dy := -n; dy <= n; dy++ {
		for dx := -n; dx <= n; dx++ {
			if float64(dx*dx+dy*dy)*cs*cs <= r*r {
				x, y := px+dx, py+dy
				if x >= 0 && y >= 0 && x < Grid && y < Grid {
					img.SetRGBA(x, y, col)
				}
			}
		}
	}
}

func line(img *image.RGBA, cm *Compiled, a, b Point, col color.RGBA) {
	cs := cm.Map.Size / Grid
	n := int(math.Ceil(dist(a, b)/cs)) + 1
	for s := 0; s <= n; s++ {
		t := float64(s) / float64(n)
		x, y := cm.pixel(Point{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t})
		img.SetRGBA(x, y, col)
	}
}

var objectColours = map[string]color.RGBA{
	"locus": {255, 255, 255, 255}, "guardian": {255, 0, 255, 255}, "shop": {255, 160, 0, 255},
	"spawn": {0, 255, 0, 255}, "core": {255, 255, 0, 255}, "binding": {255, 0, 0, 255},
	"spawner": {0, 255, 255, 255}, "well": {255, 215, 0, 255}, "camp": {160, 80, 255, 255},
	"ward": {0, 200, 120, 255},
}

func (cm *Compiled) drawObjects(img *image.RGBA) {
	for t := range cm.Layout.Teams {
		col := color.RGBA{80, 120, 255, 255}
		if t == 1 {
			col = color.RGBA{255, 90, 70, 255}
		}
		for _, id := range cm.Layout.LaneIDs {
			ks := cm.Layout.Teams[t].Lanes[id]
			for k := 0; k+1 < len(ks); k++ {
				line(img, cm, ks[k], ks[k+1], col)
			}
		}
	}
	for _, g := range cm.Guardians() {
		disc(img, cm, Point{g.X, g.Y}, HoleRadius, color.RGBA{120, 0, 120, 255})
	}
	for _, o := range cm.Objects() {
		r := 2.0
		switch o.Kind {
		case "locus", "guardian", "well":
			r = 4
		case "binding", "core":
			r = 3
		}
		disc(img, cm, o.Pos, r, objectColours[o.Kind])
	}
}

func (cm *Compiled) tagImage() (*image.RGBA, error) {
	names := tagmap.Names
	s := cm.TagSpec()
	raw, err := tagmap.Image(names, s)
	if err != nil {
		return nil, err
	}
	bit := func(o int, name string) bool {
		for i, n := range names {
			if n == name {
				if i < 8 {
					return raw[18+o+2]&(1<<uint(i)) != 0
				}
				return raw[18+o+1]&(1<<uint(i-8)) != 0
			}
		}
		return false
	}
	img := image.NewRGBA(image.Rect(0, 0, tagmap.Size, tagmap.Size))
	for my := 0; my < tagmap.Size; my++ {
		for mx := 0; mx < tagmap.Size; mx++ {
			o := 4 * (my*tagmap.Size + mx)
			c := [3]float64{60, 60, 60}
			switch {
			case bit(o, "Top"):
				c = [3]float64{200, 190, 60}
			case bit(o, "Bottom"):
				c = [3]float64{60, 190, 200}
			case bit(o, "Jungle"): // MAPED4b
				c = [3]float64{50, 120, 50}
			}
			if bit(o, "Base") {
				c = [3]float64{c[0] * 0.5, c[1] * 0.5, c[2] * 0.5}
			}
			if bit(o, "MortalSide") {
				c[2] = math.Min(255, c[2]+50)
			} else if bit(o, "SpiritSide") {
				c[0] = math.Min(255, c[0]+50)
			}
			for _, x := range []struct {
				tag string
				col [3]float64
			}{{"BindingRange", [3]float64{255, 80, 80}}, {"SpiritWell", [3]float64{255, 215, 0}},
				{"Locus", [3]float64{255, 255, 255}}, {"GreenBuff", [3]float64{0, 255, 0}},
				{"BlueBuff", [3]float64{40, 80, 255}}, {"OrangeBuff", [3]float64{255, 140, 0}},
				{"MoneyPigs", [3]float64{255, 105, 180}}, {"Parasite", [3]float64{170, 40, 240}}} {
				if bit(o, x.tag) {
					c = [3]float64{(c[0] + x.col[0]) / 2, (c[1] + x.col[1]) / 2, (c[2] + x.col[2]) / 2}
				}
			}
			img.SetRGBA(mx, tagmap.Size-1-my, color.RGBA{byte(c[0]), byte(c[1]), byte(c[2]), 255})
		}
	}
	return img, nil
}

// groundImage shades the ground model's vertex grid: its vertex colours lit from the north-west, north up.
func (cm *Compiled) groundImage() (*image.RGBA, error) {
	walk := cm.Layers.NPCWalk() // the islands' ground is flat, as the archive's (GroundWalk)
	f, err := ground.NewField(walk.W, walk.H, walk.Pix, cm.Map.Size)
	if err != nil {
		return nil, err
	}
	g := ground.NewGroundSpec(f, ground.Grid, 0, cm.GroundSpec())
	img := image.NewRGBA(image.Rect(0, 0, g.N, g.N))
	l := [3]float64{-0.5, 0.5, 0.707}
	for j := 0; j < g.N; j++ {
		for i := 0; i < g.N; i++ {
			k := j*g.N + i
			n := g.Normal[k]
			sh := math.Max(0.25, n[0]*l[0]+n[1]*l[1]+n[2]*l[2])
			z := g.Z[k]
			tone := [3]float64{150, 120, 90} // the dirt floor
			if z > 2.5 {
				tone = [3]float64{90, 130, 70} // the grass plateau
			} else if z < -0.5 {
				tone = [3]float64{50, 50, 70} // a chasm
			}
			var c [3]byte
			for q := range c {
				c[q] = byte(math.Min(255, tone[q]*g.RGB[k][q]*sh*1.3))
			}
			img.SetRGBA(i, g.N-1-j, color.RGBA{c[0], c[1], c[2], 255})
		}
	}
	return img, nil
}
