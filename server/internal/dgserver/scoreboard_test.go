package dgserver

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/goregular"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/content"
	"dawngate/server/internal/discordhook"
	"dawngate/server/internal/scoreboard"
)

// The image style's renderer: none with the text style; none and a note when the art is missing (the posts fall back
// to text); with the art (Go's font standing in for the game's) a PNG of the rows, which name the icons by file.
func TestResultImage(t *testing.T) {
	s := discordhook.DefaultSettings()
	if draw, note := resultImage(s, "x.json"); draw != nil || note != "" {
		t.Errorf("text style: %v %q", draw != nil, note)
	}
	s.ResultStyle = discordhook.StyleImage
	file := filepath.Join(t.TempDir(), "content.json")
	draw, note := resultImage(s, file)
	if draw != nil || !strings.HasPrefix(note, "discord result picture: none, results are posted as text: no art beside") {
		t.Errorf("no art: %v %q", draw != nil, note)
	}
	for _, dir := range []string{content.IconDir(file), content.FontDir(file)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(content.FontDir(file), scoreboard.TextFont), goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	draw, note = resultImage(s, file)
	if draw == nil || !strings.HasPrefix(note, "discord result picture: drawn from the art beside ") {
		t.Fatalf("art: %v %q", draw != nil, note)
	}
	row := blaze.ResultRow{Team: 1, Name: "Ann", Result: true, Portrait: "PC_Alpha", Kills: 3}
	row.Items[0] = blaze.ResultItem{ID: 7, Name: "Edge", File: "Advanced_Edge"}
	row.Items[6] = blaze.ResultItem{ID: 8, Name: "Battle spell"}
	img := blaze.ResultImage{Winner: 1, Rows: []blaze.ResultRow{row, {Team: 2, Bot: true}}}
	b := scoreboardOf(img)
	if len(b.Rows) != 2 || b.Winner != 1 || b.Rows[0].Items[0] != "Advanced_Edge" || b.Rows[0].Items[6] != "" ||
		b.Rows[0].Portrait != "PC_Alpha" || b.Rows[0].Name != "Ann" || !b.Rows[1].Bot {
		t.Errorf("board %+v", b)
	}
	pic, err := draw(img)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(pic))
	if err != nil || cfg.Width != scoreboard.Width || cfg.Height != scoreboard.Height(1, 1) {
		t.Errorf("picture %+v %v", cfg, err)
	}
}
