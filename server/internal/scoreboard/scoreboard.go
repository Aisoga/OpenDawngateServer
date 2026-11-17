// Package scoreboard draws a finished match's scoreboard as a PNG: the picture of the Discord result post when
// dgserver's --discord-result-style is image. It draws from plain data (Board, a Row per seat) and the art the setup
// writes beside the content file from the operator's own game copy (content.IconDir, PortraitDir, FontDir: the item
// icons, the shapers' portraits and the launcher's two fonts). The art is never in git or a release: a server
// without it posts the text style instead.
//
// The look follows the game's own end-of-match scoreboard: a dark panel, per team a header bar with the team's name
// (the winner marked) over short column labels, then a row per seat: the portrait with the level on it, the name
// and the shaper, K, D, A, CS and vim, six item boxes, a gap and three battle spell boxes. A player who left is
// dimmed; a missing icon or portrait is an empty box.
package scoreboard

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"dawngate/server/internal/content"
)

// Slots are the inventory slots a row shows: ItemSlots items, then the battle spells.
const (
	Slots     = 9
	ItemSlots = 6
)

// Row is one seat of the board.
type Row struct {
	Team int // 1 or 2
	// Name is the player's name; "" draws "Player" (names off), a bot always "Bot".
	Name   string
	Bot    bool
	Left   bool // the player left the match: the row is dimmed
	Result bool // the host reported the seat's stats (else its numbers are dashes)
	// Shaper is the shaper's name ("Ember"; "" unknown); Portrait its portrait's file name without ".png" in the
	// portrait folder (the PlayerClass, "PC_Alpha"; "" none: an empty box).
	Shaper, Portrait              string
	Level, Kills, Deaths, Assists int64
	CS, Vim                       int64
	Items                         [Slots]string // each slot's icon file name without ".png" ("Advanced_Abolition"); "" empty
}

// Board is a finished match: its rows (team 1's, then team 2's, each in seat order) and the winning team (1 or 2;
// anything else: no winner).
type Board struct {
	Winner int
	Rows   []Row
}

// Fonts the picture uses from the art folder (content.ArtFonts): the text face and the headers' face.
const (
	TextFont = "FrizQuadrata.TTF"
	HeadFont = "MasonSanAlt.ttf"
)

// Renderer draws boards. Its fonts are parsed once (New, Load); the icons and portraits are read from their folders
// when a board first needs them and kept, scaled, for the next. Safe for concurrent use (one board at a time).
type Renderer struct {
	textF, headF     *sfnt.Font // the primary faces
	fbText, fbHead   *sfnt.Font // Go's fonts, for a character the primary face lacks
	iconDir, portDir string

	mu    sync.Mutex
	cache map[string]image.Image // "i:<name>" / "p:<name>" at their drawn size; nil = missing
	buf   sfnt.Buffer
}

// New is a renderer with the given fonts (head nil: text for both) and art folders (an empty or missing folder draws
// empty boxes).
func New(text, head *sfnt.Font, iconDir, portraitDir string) *Renderer {
	if head == nil {
		head = text
	}
	fbText, _ := opentype.Parse(goregular.TTF)
	fbHead, _ := opentype.Parse(gobold.TTF)
	return &Renderer{textF: text, headF: head, fbText: fbText, fbHead: fbHead, iconDir: iconDir, portDir: portraitDir,
		cache: map[string]image.Image{}}
}

// GoFonts is a renderer with Go's own fonts (golang.org/x/image/font/gofont) and the given art folders: for tests,
// which can't use the game's fonts.
func GoFonts(iconDir, portraitDir string) *Renderer {
	text, _ := opentype.Parse(goregular.TTF)
	head, _ := opentype.Parse(gobold.TTF)
	return New(text, head, iconDir, portraitDir)
}

// Load is a renderer from the art beside a content file: the fonts in content.FontDir (TextFont is required,
// HeadFont optional), the icons in content.IconDir and the portraits in content.PortraitDir. An error says what is
// missing (no art folder, no font, a font that doesn't parse).
func Load(contentFile string) (*Renderer, error) {
	if contentFile == "" {
		return nil, errors.New("no content file, so no art beside it")
	}
	icons := content.IconDir(contentFile)
	if st, err := os.Stat(icons); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("no art beside the content file (%s is missing; --make-content writes it)", icons)
	}
	fonts := content.FontDir(contentFile)
	text, err := loadFont(filepath.Join(fonts, TextFont))
	if err != nil {
		return nil, err
	}
	head, err := loadFont(filepath.Join(fonts, HeadFont))
	if err != nil {
		head = text
	}
	return New(text, head, icons, content.PortraitDir(contentFile)), nil
}

// loadFont reads and parses a font file.
func loadFont(file string) (*sfnt.Font, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("no font %s (--make-content copies it from the game's launcher)", file)
	}
	f, err := opentype.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("the font %s doesn't parse: %v", file, err)
	}
	return f, nil
}

// PNG draws the board and encodes it as a PNG.
func (r *Renderer) PNG(b Board) ([]byte, error) {
	img, err := r.Draw(b)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// The layout (pixels).
const (
	Width      = 900 // the picture's width
	pad        = 12  // around the panel's content
	headH      = 30  // a team's header bar
	labelH     = 18  // the column labels under it
	rowH       = 46  // a seat's row
	teamGap    = 12  // between the teams
	portSize   = 40  // the portrait
	iconSize   = 36  // an item or spell box
	iconStep   = 39  // from one box to the next
	nameX      = pad + portSize + 10
	nameW      = 226
	colK       = 306 // the numbers' centres
	colD       = 340
	colA       = 374
	colCS      = 418
	colVim     = 474
	itemsX     = 518 // the first item box
	spellsX    = itemsX + ItemSlots*iconStep + 10
	maxRows    = 10 // a team's rows drawn at most
	maxArtSide = 1024
)

// Colours.
var (
	colBack      = color.RGBA{0x0e, 0x11, 0x17, 0xff}
	colRowA      = color.RGBA{0x16, 0x1a, 0x22, 0xff}
	colRowB      = color.RGBA{0x1b, 0x20, 0x29, 0xff}
	colText      = color.RGBA{0xec, 0xe6, 0xd6, 0xff}
	colMuted     = color.RGBA{0x8c, 0x93, 0xa0, 0xff}
	colGold      = color.RGBA{0xe8, 0xb8, 0x5c, 0xff}
	colLeft      = color.RGBA{0xd9, 0x6c, 0x5f, 0xff}
	colBox       = color.RGBA{0x0a, 0x0c, 0x10, 0xff}
	colBoxEdge   = color.RGBA{0x2b, 0x31, 0x40, 0xff}
	colBadge     = color.RGBA{0x08, 0x0a, 0x0e, 0xe0}
	colDim       = color.RGBA{0x0e, 0x11, 0x17, 0xa8} // over a leaver's row
	teamColors   = [2]color.RGBA{{0x34, 0x98, 0xdb, 0xff}, {0xe7, 0x4c, 0x3c, 0xff}}
	colLabelLine = color.RGBA{0x12, 0x15, 0x1c, 0xff}
)

// Height is a board's picture height for the given rows per team.
func Height(team1, team2 int) int {
	h := pad
	for _, n := range [2]int{team1, team2} {
		h += headH + labelH + min(max(n, 1), maxRows)*rowH
	}
	return h + teamGap + pad
}

// Draw draws the board: both teams, each with at least one row's room ("nobody" when it has none).
func (r *Renderer) Draw(b Board) (*image.RGBA, error) {
	var teams [2][]Row
	for _, row := range b.Rows {
		if row.Team == 1 || row.Team == 2 {
			if len(teams[row.Team-1]) < maxRows {
				teams[row.Team-1] = append(teams[row.Team-1], row)
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.faces()
	if err != nil {
		return nil, err
	}
	defer f.close()
	img := image.NewRGBA(image.Rect(0, 0, Width, Height(len(teams[0]), len(teams[1]))))
	fill(img, img.Rect, colBack)
	y := pad
	for t, rows := range teams {
		if t == 1 {
			y += teamGap
		}
		y = r.drawTeam(img, f, y, t+1, b.Winner == t+1, rows)
	}
	return img, nil
}

// faces are the board's font faces at their sizes, each with a fallback face for characters its font lacks.
type faces struct {
	head, headNum, name, small, num, label, badge face
}

type face struct {
	main, fb font.Face
	font     *sfnt.Font
}

func (f *faces) close() {
	for _, x := range []face{f.head, f.headNum, f.name, f.small, f.num, f.label, f.badge} {
		x.main.Close()
		x.fb.Close()
	}
}

func (r *Renderer) faces() (*faces, error) {
	mk := func(main, fb *sfnt.Font, size float64) (face, error) {
		o := &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull}
		m, err := opentype.NewFace(main, o)
		if err != nil {
			return face{}, err
		}
		g, err := opentype.NewFace(fb, o)
		if err != nil {
			return face{}, err
		}
		return face{main: m, fb: g, font: main}, nil
	}
	var f faces
	var err error
	specs := []struct {
		dst      *face
		main, fb *sfnt.Font
		size     float64
	}{
		{&f.head, r.headF, r.fbHead, 21}, {&f.headNum, r.textF, r.fbHead, 18},
		{&f.name, r.textF, r.fbText, 16}, {&f.small, r.textF, r.fbText, 12},
		{&f.num, r.textF, r.fbText, 16}, {&f.label, r.textF, r.fbText, 11}, {&f.badge, r.textF, r.fbText, 11},
	}
	for _, s := range specs {
		if *s.dst, err = mk(s.main, s.fb, s.size); err != nil {
			return nil, err
		}
	}
	return &f, nil
}

// drawTeam draws a team's header, labels and rows from y; it returns the y below them.
func (r *Renderer) drawTeam(img *image.RGBA, f *faces, y, team int, won bool, rows []Row) int {
	tc := teamColors[team-1]
	// the header bar: the team's colour fading into the panel, a gold line on top for the winner
	for i := 0; i < headH; i++ {
		k := 0.55 - 0.30*float64(i)/float64(headH-1)
		fill(img, image.Rect(pad, y+i, Width-pad, y+i+1), mix(tc, colBack, k))
	}
	fill(img, image.Rect(pad, y, Width-pad, y+1), mix(tc, colText, 0.4))
	if won {
		fill(img, image.Rect(pad, y, Width-pad, y+2), colGold)
	}
	// "Team 1" (the number in the text face: the heading face's digits read as letters), the winner "· Victory"
	title, num := "Team ", strconv.Itoa(team)
	var suffix string
	if won {
		suffix = "  ·  Victory"
	}
	tw := r.measure(f.head, title) + r.measure(f.headNum, num) + r.measure(f.head, suffix)
	x := (Width - tw) / 2
	base := baseline(f.head, y, headH)
	x = r.text(img, f.head, x, base, title, colText)
	x = r.text(img, f.headNum, x, base, num, colText)
	if suffix != "" {
		r.text(img, f.head, x, base, suffix, colGold)
	}
	y += headH
	// the column labels
	fill(img, image.Rect(pad, y, Width-pad, y+labelH), colLabelLine)
	lb := baseline(f.label, y, labelH)
	r.text(img, f.label, nameX, lb, "PLAYER", colMuted)
	for _, c := range []struct {
		x int
		s string
	}{{colK, "K"}, {colD, "D"}, {colA, "A"}, {colCS, "CS"}, {colVim, "VIM"},
		{itemsX + (ItemSlots*iconStep-3)/2, "ITEMS"}, {spellsX + (3*iconStep-3)/2, "SPELLS"}} {
		r.centred(img, f.label, c.x, lb, c.s, colMuted)
	}
	y += labelH
	if len(rows) == 0 {
		fill(img, image.Rect(pad, y, Width-pad, y+rowH), colRowA)
		r.text(img, f.name, nameX, baseline(f.name, y, rowH), "nobody", colMuted)
		return y + rowH
	}
	for i, row := range rows {
		bg := colRowA
		if i%2 == 1 {
			bg = colRowB
		}
		r.drawRow(img, f, y, row, bg)
		y += rowH
	}
	return y
}

// drawRow draws one seat's row from y.
func (r *Renderer) drawRow(img *image.RGBA, f *faces, y int, row Row, bg color.RGBA) {
	rect := image.Rect(pad, y, Width-pad, y+rowH)
	fill(img, rect, bg)
	mid := y + rowH/2
	// the portrait, the level on it
	px, py := pad+3, mid-portSize/2
	r.box(img, px, py, portSize, r.art("p", r.portDir, row.Portrait, portSize))
	if row.Result && row.Level > 0 {
		lv := strconv.FormatInt(row.Level, 10)
		w := max(r.measure(f.badge, lv)+6, 15)
		bx, by := px+portSize-w, py+portSize-13
		fillOver(img, image.Rect(bx, by, px+portSize, py+portSize), colBadge)
		r.centred(img, f.badge, bx+w/2, by+11, lv, colGold)
	}
	// the name and the shaper
	name, nameCol := row.Name, colText
	switch {
	case row.Bot:
		name, nameCol = "Bot", colMuted
	case name == "":
		name = "Player"
	}
	r.text(img, f.name, nameX, y+21, r.fit(f.name, name, nameW), nameCol)
	sub := row.Shaper
	if sub == "" {
		sub = "?"
	}
	sx := r.text(img, f.small, nameX, y+37, r.fit(f.small, sub, nameW-60), colMuted)
	// the numbers
	nb := baseline(f.num, y, rowH)
	if row.Result {
		for _, c := range []struct {
			x   int
			v   int64
			col color.RGBA
		}{{colK, row.Kills, colText}, {colD, row.Deaths, colText}, {colA, row.Assists, colText},
			{colCS, row.CS, colText}, {colVim, row.Vim, colGold}} {
			r.centred(img, f.num, c.x, nb, strconv.FormatInt(c.v, 10), c.col)
		}
	} else {
		for _, x := range []int{colK, colD, colA, colCS, colVim} {
			r.centred(img, f.num, x, nb, "–", colMuted)
		}
	}
	// the items, a gap, the battle spells
	iy := mid - iconSize/2
	for s := 0; s < Slots; s++ {
		x := itemsX + s*iconStep
		if s >= ItemSlots {
			x = spellsX + (s-ItemSlots)*iconStep
		}
		r.box(img, x, iy, iconSize, r.art("i", r.iconDir, row.Items[s], iconSize))
	}
	if row.Left { // dimmed, the mark on top
		fillOver(img, rect, colDim)
		r.text(img, f.small, sx, y+37, "  ·  left", colLeft)
	}
}

// box draws a picture (nil: an empty box) at x, y in a frame.
func (r *Renderer) box(img *image.RGBA, x, y, size int, pic image.Image) {
	outer := image.Rect(x-1, y-1, x+size+1, y+size+1)
	fill(img, outer, colBoxEdge)
	in := image.Rect(x, y, x+size, y+size)
	fill(img, in, colBox)
	if pic != nil {
		stddraw.Draw(img, in, pic, pic.Bounds().Min, stddraw.Over)
	}
}

// art is a picture from dir (name.png) scaled to size, read once and kept; nil when it is missing or unreadable (an
// empty box). A name with a path in it is refused.
func (r *Renderer) art(kind, dir, name string, size int) image.Image {
	if name == "" || dir == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\:`) || name == ".." {
		return nil
	}
	key := kind + ":" + name
	if pic, ok := r.cache[key]; ok {
		return pic
	}
	pic := readArt(filepath.Join(dir, name+".png"), size)
	r.cache[key] = pic
	return pic
}

// readArt reads a PNG and scales it to size x size (nil on any failure).
func readArt(file string, size int) image.Image {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxArtSide || cfg.Height > maxArtSide {
		return nil
	}
	src, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Rect, src, src.Bounds(), draw.Src, nil)
	return dst
}

// text draws s with its baseline at y from x (a character the face's font lacks in the fallback face); it returns
// the x after it.
func (r *Renderer) text(img *image.RGBA, f face, x, y int, s string, c color.Color) int {
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Dot: fixed.P(x, y)}
	for _, run := range r.runs(f, s) {
		d.Face = run.face
		d.DrawString(run.s)
	}
	return d.Dot.X.Round()
}

// centred draws s centred on x.
func (r *Renderer) centred(img *image.RGBA, f face, x, y int, s string, c color.Color) {
	r.text(img, f, x-r.measure(f, s)/2, y, s, c)
}

// measure is s's width in pixels.
func (r *Renderer) measure(f face, s string) int {
	var w fixed.Int26_6
	for _, run := range r.runs(f, s) {
		w += font.MeasureString(run.face, run.s)
	}
	return w.Round()
}

// fit cuts s with "…" to at most w pixels.
func (r *Renderer) fit(f face, s string, w int) string {
	if r.measure(f, s) <= w {
		return s
	}
	rs := []rune(s)
	for n := len(rs) - 1; n > 0; n-- {
		t := strings.TrimRight(string(rs[:n]), " ") + "…"
		if r.measure(f, t) <= w {
			return t
		}
	}
	return "…"
}

type run struct {
	face font.Face
	s    string
}

// runs splits s into runs drawn by the face's font or, for characters it lacks, its fallback.
func (r *Renderer) runs(f face, s string) []run {
	var out []run
	var cur strings.Builder
	var curFace font.Face
	for _, c := range s {
		fc := f.main
		if gi, err := f.font.GlyphIndex(&r.buf, c); err != nil || gi == 0 {
			fc = f.fb
		}
		if fc != curFace && cur.Len() > 0 {
			out = append(out, run{curFace, cur.String()})
			cur.Reset()
		}
		curFace = fc
		cur.WriteRune(c)
	}
	if cur.Len() > 0 {
		out = append(out, run{curFace, cur.String()})
	}
	return out
}

// baseline is the baseline that centres a face's capitals in a band of height h from top.
func baseline(f face, top, h int) int {
	m := f.main.Metrics()
	capH := m.CapHeight.Round()
	if capH <= 0 {
		capH = m.Ascent.Round() * 7 / 10
	}
	return top + (h+capH)/2
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	stddraw.Draw(img, r, image.NewUniform(c), image.Point{}, stddraw.Src)
}

func fillOver(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	// image.Uniform with a non-premultiplied colour: convert
	a := uint32(c.A)
	pc := color.RGBA{uint8(uint32(c.R) * a / 255), uint8(uint32(c.G) * a / 255), uint8(uint32(c.B) * a / 255), c.A}
	stddraw.Draw(img, r, image.NewUniform(pc), image.Point{}, stddraw.Over)
}

// mix is a*k + b*(1-k).
func mix(a, b color.RGBA, k float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x)*k + float64(y)*(1-k) + 0.5) }
	return color.RGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), 0xff}
}
