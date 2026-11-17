package scoreboard

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/image/font/gofont/goregular"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/gamecontent"
	"dawngate/server/internal/setup/testgame"
)

var sample = flag.String("sample", "", "TestDrawGame writes its picture to this PNG file")

// writePNG writes a size x size picture of one colour (a synthetic icon or portrait).
func writePNG(t *testing.T, file string, size int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func at(img image.Image, x, y int) color.RGBA {
	return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
}

func near(a, b color.RGBA, d int) bool {
	abs := func(x int) int {
		if x < 0 {
			return -x
		}
		return x
	}
	return abs(int(a.R)-int(b.R)) <= d && abs(int(a.G)-int(b.G)) <= d && abs(int(a.B)-int(b.B)) <= d
}

// The picture from synthetic art with Go's fonts: its size (two teams of 2 and 1 rows), an icon and a portrait drawn
// in their boxes, a missing icon and an empty slot as empty boxes, the leaver's row dimmed, a path in a name refused.
func TestDraw(t *testing.T) {
	dir := t.TempDir()
	icons, ports := filepath.Join(dir, "itemicons"), filepath.Join(dir, "portraits")
	red, green := color.RGBA{0xff, 0, 0, 0xff}, color.RGBA{0, 0xff, 0, 0xff}
	writePNG(t, filepath.Join(icons, "Advanced_Edge.png"), 64, red)
	writePNG(t, filepath.Join(icons, "Spell_Hop.png"), 32, red)
	writePNG(t, filepath.Join(ports, "PC_Alpha.png"), 64, green)
	r := GoFonts(icons, ports)
	full := Row{Team: 1, Name: "Ann", Result: true, Shaper: "Ember", Portrait: "PC_Alpha", Level: 14, Kills: 5,
		Deaths: 1, Assists: 7, CS: 152, Vim: 11840}
	full.Items[0], full.Items[1], full.Items[6] = "Advanced_Edge", "Missing_Item", "Spell_Hop"
	left := full
	left.Name, left.Left = "Kim", true
	bot := Row{Team: 2, Bot: true, Result: true, Shaper: "Stone", Portrait: "../PC_Alpha"}
	bot.Items[0] = `..\itemicons\Advanced_Edge`
	img, err := r.Draw(Board{Winner: 1, Rows: []Row{full, left, bot}})
	if err != nil {
		t.Fatal(err)
	}
	if w, h := img.Rect.Dx(), img.Rect.Dy(); w != Width || h != Height(2, 1) || h != pad+2*(headH+labelH)+3*rowH+teamGap+pad {
		t.Fatalf("size %d x %d", w, h)
	}
	row0 := pad + headH + labelH // the first row's top
	mid := row0 + rowH/2
	if c := at(img, itemsX+iconSize/2, mid); !near(c, red, 8) {
		t.Errorf("item 0: %v", c)
	}
	if c := at(img, itemsX+iconStep+iconSize/2, mid); c != colBox {
		t.Errorf("missing icon: %v", c)
	}
	if c := at(img, itemsX+2*iconStep+iconSize/2, mid); c != colBox {
		t.Errorf("empty slot: %v", c)
	}
	if c := at(img, spellsX+iconSize/2, mid); !near(c, red, 8) {
		t.Errorf("spell 0 (a 32-px icon scaled up): %v", c)
	}
	if c := at(img, pad+3+5, mid-portSize/2+5); !near(c, green, 8) {
		t.Errorf("portrait: %v", c)
	}
	// the leaver's row: the same icon, dimmed
	if c := at(img, itemsX+iconSize/2, mid+rowH); c.R >= 0x80 || c.R < 0x20 || c.G > 0x30 {
		t.Errorf("leaver's item not dimmed: %v", c)
	}
	// the bot (team 2): a portrait and an icon named with a path are empty boxes
	row2 := row0 + 2*rowH + teamGap + headH + labelH
	if c := at(img, pad+3+5, row2+rowH/2-portSize/2+5); c != colBox {
		t.Errorf("a portrait with a path: %v", c)
	}
	if c := at(img, itemsX+iconSize/2, row2+rowH/2); c != colBox {
		t.Errorf("an icon with a path: %v", c)
	}
	// the winner's header has its gold line, the other's not
	if c := at(img, Width/2, pad); c != colGold {
		t.Errorf("winner's header: %v", c)
	}
	if c := at(img, Width/2, row2-headH-labelH); c == colGold {
		t.Errorf("loser's header is gold")
	}
	b, err := r.PNG(Board{Rows: []Row{full}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width != Width || cfg.Height != Height(1, 0) {
		t.Errorf("PNG %+v %v", cfg, err)
	}
}

// Load: no content file, no art folder, no font, a font that doesn't parse; the heading font is optional.
func TestLoad(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Error("no content file loaded")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "content.json")
	if _, err := Load(file); err == nil {
		t.Error("no art folder loaded")
	}
	if err := os.MkdirAll(content.IconDir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil {
		t.Error("no font loaded")
	}
	if err := os.MkdirAll(content.FontDir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content.FontDir(file), TextFont), []byte("not a font"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil {
		t.Error("a broken font loaded")
	}
	if err := os.WriteFile(filepath.Join(content.FontDir(file), TextFont), goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.PNG(Board{}); err != nil {
		t.Error(err)
	}
}

// gameBoard is a ten-row sample: long and short names, a leaver, bots, a player without a result, empty slots, an
// unknown shaper.
func gameBoard() Board {
	row := func(team int, name string, bot bool, shaper, port string, lv, k, d, a, cs, vim int64, items ...string) Row {
		r := Row{Team: team, Name: name, Bot: bot, Result: true, Shaper: shaper, Portrait: port, Level: lv, Kills: k,
			Deaths: d, Assists: a, CS: cs, Vim: vim}
		copy(r.Items[:], items)
		return r
	}
	rows := []Row{
		row(1, "Ann", false, "Fenmore", "PC_Alpha", 18, 12, 3, 9, 211, 15420, "Legendary_Conquest", "Advanced_Abolition",
			"Legendary_Valor", "Advanced_Force", "Basic_Power", "Legendary_Glory", "Spell_Blink", "Spell_Vanquish", "Spell_Bastion"),
		row(1, "TheVeryLongPlayerNameOfThirty2", false, "Kel", "PC_HighHuntress", 16, 4, 6, 15, 87, 10110,
			"Legendary_Harmony", "Advanced_Clarity", "", "Basic_Life", "", "", "Spell_Tailwind", "Spell_Dispel"),
		row(1, "", true, "Voluc", "PC_DeepOne", 15, 2, 5, 11, 140, 9800, "Legendary_Adamance", "Advanced_Integrity",
			"Basic_Resilience", "", "", "", "Spell_Stasis"),
		row(1, "Zoë_ÅÆ_Привет", false, "Varion", "PC_Gunman", 17, 8, 4, 6, 190, 12890, "Legendary_Ruin",
			"Advanced_Aggression", "Advanced_Motion", "Basic_Hunger", "", "", "Spell_Blink", "Spell_Drain", "Spell_Exceed"),
		row(1, "Bo", false, "", "", 14, 1, 2, 3, 40, 6200),
		row(2, "Kim", false, "Nissa", "PC_Dancer", 12, 1, 6, 2, 61, 5120, "Advanced_Desire", "Basic_Will", "", "", "",
			"", "Spell_Wither"),
		row(2, "", true, "Sohn", "PC_Golem", 15, 3, 4, 1, 87, 8700, "Legendary_Defiance", "Advanced_Vigor", "Basic_Time"),
		row(2, "Mira the Brave", false, "Tavi", "PC_Otter", 16, 6, 7, 5, 120, 11050, "Legendary_Passion", "Legendary_Fate",
			"Advanced_Growth", "Basic_Power", "Advanced_Might", "", "Spell_Blitz", "Spell_Stagger"),
		row(2, "", true, "Aeri", "PC_SkyCaptain", 14, 2, 6, 3, 75, 7400, "Legendary_Hope", "", "", "", "", "",
			"Spell_Deflect"),
		{Team: 2, Name: "Quit", Shaper: "Zalgus", Portrait: "PC_Savage"},
	}
	rows[5].Left = true
	return Board{Winner: 1, Rows: rows}
}

// The picture from the real copy's art (gamecontent.WriteArt into a temporary folder, or -sample's folder is the
// output only): ten rows, the game's fonts. Skips without the game files. -sample <file> writes the picture.
func TestDrawGame(t *testing.T) {
	dir := testgame.Dir(t)
	c, err := gamecontent.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "content.json")
	if _, err := gamecontent.WriteArt(dir, file, c); err != nil {
		t.Fatal(err)
	}
	r, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	b, err := r.PNG(gameBoard())
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Now()
	if _, err := r.PNG(gameBoard()); err != nil { // the art cached
		t.Fatal(err)
	}
	t.Logf("PNG %d bytes, first %v, again %v", len(b), t1.Sub(t0), time.Since(t1))
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != Width || img.Bounds().Dy() != Height(5, 5) {
		t.Errorf("size %v", img.Bounds())
	}
	mid := pad + headH + labelH + rowH/2
	if c := at(img, itemsX+iconSize/2, mid); c == colBox {
		t.Error("the first item's icon isn't drawn")
	}
	if c := at(img, pad+3+portSize/2, mid); c == colBox {
		t.Error("the first portrait isn't drawn")
	}
	if *sample != "" {
		if err := os.MkdirAll(filepath.Dir(*sample), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(*sample, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
