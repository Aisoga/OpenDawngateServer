package gamecontent

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/testgame"
)

// square is a w x w RGBA picture of one colour.
func square(w int, r, g, b, a byte) []byte {
	px := make([]byte, 0, w*w*4)
	for i := 0; i < w*w; i++ {
		px = append(px, r, g, b, a)
	}
	return px
}

func imageOf(w int) *image.NRGBA {
	return &image.NRGBA{Pix: square(w, 1, 2, 3, 255), Stride: 4 * w, Rect: image.Rect(0, 0, w, w)}
}

func ddsOf(t *testing.T, w int, format string, px []byte) []byte {
	t.Helper()
	b, err := dds.Encode(w, w, px, format, 1)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pngSize reads a PNG's size.
func pngSize(t *testing.T, file string) (int, int) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

// The art from a made-up copy: the icons (a 256-px uncompressed texture halved twice, a 32-px one kept, found by a
// path in other case and slashes), a DXT5 portrait in the shaper's own archive, a shaper without a portrait texture
// left out, the two fonts copied; a second run writes nothing; an older setup's 256-px icon and a changed font are
// written again; an item without its texture is an error.
func TestWriteArt(t *testing.T) {
	game := t.TempDir()
	data := filepath.Join(game, "game", "Data")
	fonts := filepath.Join(game, "launcher", "data", "fonts")
	os.MkdirAll(data, 0o755)
	os.MkdirAll(fonts, 0o755)
	os.WriteFile(filepath.Join(data, AssetDataSnap), testgame.Snap(nil), 0o644)
	os.WriteFile(filepath.Join(data, UIIconsSnap), testgame.Snap([]testgame.SnapFile{
		{Name: "ui/icons/shopicons/textures/basic_time.dds", Data: ddsOf(t, 256, "argb", square(256, 200, 100, 50, 255))},
		{Name: "UI/Icons/ShopIcons/textures/Consumable_DebugGold.dds", Data: ddsOf(t, 32, "dxt1", square(32, 0, 255, 0, 255))},
	}), 0o644)
	os.WriteFile(filepath.Join(data, "Hero_Alpha.snap"), testgame.Snap([]testgame.SnapFile{
		{Name: "heroes/Alpha/default/ui/textures/Alpha_Portrait.dds", Data: ddsOf(t, 256, "dxt5", square(256, 10, 20, 200, 128))},
	}), 0o644)
	os.WriteFile(filepath.Join(fonts, "FrizQuadrata.TTF"), []byte("friz"), 0o644)
	os.WriteFile(filepath.Join(fonts, "MasonSanAlt.ttf"), []byte("mason"), 0o644)
	c := &content.Content{
		MatchItems: []content.MatchItem{
			{Name: "Basic_Time", Icon: `UI\Icons\ShopIcons\textures\Basic_Time.dds`},
			{Name: "Consumable_DebugGold", Icon: "UI/Icons/ShopIcons/textures/Consumable_DebugGold.dds"},
			{Name: "NoIcon"},
		},
		Shapers: []content.Shaper{
			{Name: "PC_Alpha", Portrait: "Heroes/Alpha/default/ui/Textures/alpha_portrait.dds"},
			{Name: "PC_Sloth", Portrait: "Heroes/Sloth/default/ui/Textures/sloth_portrait.dds"},
			{Name: "PC_Weaver"},
		},
	}
	file := filepath.Join(t.TempDir(), "data", "content.json")
	a, err := ArtCheck(game, file, c)
	if err != nil || a.Icons != 2 || a.Portraits != 1 || a.Fonts != 2 || len(a.Stale) != 5 {
		t.Fatalf("ArtCheck before: %+v %v", a, err)
	}
	if !strings.HasPrefix(a.Why(), "5 of 5 art files missing or out of date (") || a.Summary() != "2 item icons, 1 portraits, 2 fonts" {
		t.Errorf("%q, %q", a.Why(), a.Summary())
	}
	a, err = WriteArt(game, file, c)
	if err != nil || a.Written != 5 {
		t.Fatalf("WriteArt: %+v %v", a, err)
	}
	icon := filepath.Join(content.IconDir(file), "Basic_Time.png")
	if w, h := pngSize(t, icon); w != 64 || h != 64 {
		t.Errorf("Basic_Time.png %d x %d", w, h)
	}
	if w, h := pngSize(t, filepath.Join(content.IconDir(file), "Consumable_DebugGold.png")); w != 32 || h != 32 {
		t.Errorf("Consumable_DebugGold.png %d x %d", w, h)
	}
	portrait := filepath.Join(content.PortraitDir(file), "PC_Alpha.png")
	if w, h := pngSize(t, portrait); w != 64 || h != 64 {
		t.Errorf("PC_Alpha.png %d x %d", w, h)
	}
	raw, _ := os.ReadFile(portrait)
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if p := color.NRGBAModel.Convert(img.At(10, 10)).(color.NRGBA); p.B < 190 || p.A < 120 || p.A > 136 || p.R > 30 || p.G > 40 {
		t.Errorf("portrait pixel %+v", p)
	}
	if _, err := os.Stat(filepath.Join(content.PortraitDir(file), "PC_Sloth.png")); err == nil {
		t.Error("a portrait without its texture was written")
	}
	if b, _ := os.ReadFile(filepath.Join(content.FontDir(file), "MasonSanAlt.ttf")); string(b) != "mason" {
		t.Errorf("font %q", b)
	}
	if a, err = WriteArt(game, file, c); err != nil || a.Written != 0 {
		t.Errorf("second WriteArt: %+v %v", a, err)
	}
	if a, err = ArtCheck(game, file, c); err != nil || a.Why() != "" {
		t.Errorf("ArtCheck after: %+v %v", a, err)
	}
	// an older setup's 256-px icon, a changed font
	var big bytes.Buffer
	png.Encode(&big, imageOf(256))
	os.WriteFile(icon, big.Bytes(), 0o644)
	os.WriteFile(filepath.Join(fonts, "FrizQuadrata.TTF"), []byte("friz 2"), 0o644)
	a, _ = ArtCheck(game, file, c)
	if len(a.Stale) != 2 || a.Stale[0] != filepath.Join("itemicons", "Basic_Time.png") || a.Stale[1] != filepath.Join("fonts", "FrizQuadrata.TTF") {
		t.Errorf("stale %v", a.Stale)
	}
	if a, err = WriteArt(game, file, c); err != nil || a.Written != 2 {
		t.Errorf("WriteArt again: %+v %v", a, err)
	}
	if w, _ := pngSize(t, icon); w != 64 {
		t.Errorf("the 256-px icon stayed %d", w)
	}
	c.MatchItems = append(c.MatchItems, content.MatchItem{Name: "Gone", Icon: "UI/Icons/ShopIcons/textures/Gone.dds"})
	if _, err := WriteArt(game, file, c); err == nil || !strings.Contains(err.Error(), "Gone") {
		t.Errorf("a missing icon: %v", err)
	}
}

// Build 854's art: 106 item icons (104 of 64 x 64, the two debug items' 32 x 32), a portrait for every released
// shaper (35, some DXT5), the two fonts; skipped without the game files.
func TestWriteArtGame(t *testing.T) {
	dir := testgame.Dir(t)
	c, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "content.json")
	t0 := time.Now()
	a, err := WriteArt(dir, file, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("WriteArt: %s, %d written in %v", a.Summary(), a.Written, time.Since(t0))
	if a.Icons != 106 || a.Fonts != 2 || a.Written != a.Icons+a.Portraits+a.Fonts {
		t.Errorf("%+v", a)
	}
	sizes := map[int]int{}
	for _, it := range c.MatchItems {
		w, h := pngSize(t, filepath.Join(content.IconDir(file), content.IconFile(it.Name)))
		if w != h {
			t.Errorf("%s: %d x %d", it.Name, w, h)
		}
		sizes[w]++
	}
	if sizes[64] != 104 || sizes[32] != 2 {
		t.Errorf("icon sizes %v", sizes)
	}
	released := 0
	for _, s := range c.Shapers {
		if s.Title == "" || s.TestingOnly {
			continue
		}
		released++
		if w, h := pngSize(t, filepath.Join(content.PortraitDir(file), content.PortraitFile(s.Name))); w != 64 || h != 64 {
			t.Errorf("%s portrait %d x %d", s.Name, w, h)
		}
	}
	t.Logf("%d released shapers, %d portraits", released, a.Portraits)
	if released != 35 || a.Portraits < released {
		t.Errorf("%d released shapers, %d portraits", released, a.Portraits)
	}
	t0 = time.Now()
	if a, err := ArtCheck(dir, file, c); err != nil || a.Why() != "" {
		t.Errorf("ArtCheck: %+v %v", a, err)
	}
	t.Logf("ArtCheck: %v", time.Since(t0))
}
