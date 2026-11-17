package gamecontent

// The art beside the content file (content.IconDir, PortraitDir, FontDir), made from the operator's own game copy at
// setup time and never committed or shipped: each match item's icon (its Icon texture, found in any mounted archive,
// UI_Icons.snap for build 854) and each shaper's portrait (Shaper.Portrait, the default skin's largeIcon, in the
// shaper's own archive) as content.ArtSize PNGs, and the launcher's two fonts (content.ArtFonts) copied as they are.
// The history page shows the icons (dgserver serves them); the Discord result picture uses the portraits, the icons
// and the fonts. setup/depot reads only the archives' indexes, then the entries asked for.

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/dds"
	"dawngate/server/internal/setup/depot"
)

// Art says what the art beside a content file holds and what ArtCheck found out of date.
type Art struct {
	Icons, Portraits, Fonts int      // the files of each kind the copy gives
	Stale                   []string // the files missing or out of date (the folder and file name), in plan order
	Written                 int      // the files WriteArt wrote
}

// Summary is the counts as a phrase: "106 item icons, 35 portraits, 2 fonts".
func (a Art) Summary() string {
	return fmt.Sprintf("%d item icons, %d portraits, %d fonts", a.Icons, a.Portraits, a.Fonts)
}

// Why is the reason the art isn't current ("" when it is): "106 of 143 art files missing or out of date (itemicons\Basic_Time.png, ...)".
func (a Art) Why() string {
	if len(a.Stale) == 0 {
		return ""
	}
	show := a.Stale
	if len(show) > 3 {
		show = append(append([]string{}, show[:3]...), "...")
	}
	return fmt.Sprintf("%d of %d art files missing or out of date (%s)", len(a.Stale), a.Icons+a.Portraits+a.Fonts,
		strings.Join(show, ", "))
}

type artKind int

const (
	artIcon artKind = iota
	artPortrait
	artFont
)

type artJob struct {
	kind artKind
	src  string // the archive path (an icon or portrait) or the font file
	dst  string
}

// fontDir is the launcher's font folder beside the copy's game folder (<copy>\launcher\data\fonts).
func fontDir(data string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(data)), "launcher", "data", "fonts")
}

// planArt lists every art file the copy gives for the content, with the depot to read the textures from. An item
// whose icon texture no archive holds is an error; a shaper whose portrait none holds (an unfinished shaper) and a
// font the copy lacks are left out.
func planArt(gameDir, contentFile string, c *content.Content) ([]artJob, *depot.Depot, Art, error) {
	var a Art
	data, err := DataDir(gameDir)
	if err != nil {
		return nil, nil, a, err
	}
	d, err := depot.OpenData(data)
	if err != nil {
		return nil, nil, a, fmt.Errorf("gamecontent: %w", err)
	}
	var jobs []artJob
	icons := content.IconDir(contentFile)
	for _, it := range c.MatchItems {
		if it.Icon == "" {
			continue
		}
		if !d.Has(it.Icon) {
			d.Close()
			return nil, nil, a, fmt.Errorf("gamecontent: %s: its icon %s isn't in the game's archives", it.Name, it.Icon)
		}
		jobs = append(jobs, artJob{artIcon, it.Icon, filepath.Join(icons, content.IconFile(it.Name))})
		a.Icons++
	}
	portraits := content.PortraitDir(contentFile)
	for _, s := range c.Shapers {
		if s.Portrait != "" && d.Has(s.Portrait) {
			jobs = append(jobs, artJob{artPortrait, s.Portrait, filepath.Join(portraits, content.PortraitFile(s.Name))})
			a.Portraits++
		}
	}
	fonts := fontDir(data)
	for _, f := range content.ArtFonts {
		if st, err := os.Stat(filepath.Join(fonts, f)); err == nil && st.Mode().IsRegular() {
			jobs = append(jobs, artJob{artFont, filepath.Join(fonts, f), filepath.Join(content.FontDir(contentFile), f)})
			a.Fonts++
		}
	}
	return jobs, d, a, nil
}

// current says whether a job's file is there as it should be: a picture a PNG of at most ArtSize on each side (a
// 256-px icon of an older setup isn't), a font the copy's bytes.
func (j artJob) current() bool {
	have, err := os.ReadFile(j.dst)
	if err != nil {
		return false
	}
	if j.kind == artFont {
		want, err := os.ReadFile(j.src)
		return err == nil && bytes.Equal(have, want)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(have))
	return err == nil && cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= content.ArtSize && cfg.Height <= content.ArtSize
}

func (j artJob) show() string {
	return filepath.Join(filepath.Base(filepath.Dir(j.dst)), filepath.Base(j.dst))
}

// ArtCheck reports what the art beside contentFile holds and which of its files are missing or out of date.
func ArtCheck(gameDir, contentFile string, c *content.Content) (Art, error) {
	jobs, d, a, err := planArt(gameDir, contentFile, c)
	if err != nil {
		return a, err
	}
	defer d.Close()
	for _, j := range jobs {
		if !j.current() {
			a.Stale = append(a.Stale, j.show())
		}
	}
	return a, nil
}

// WriteArt writes the art files that are missing or out of date beside contentFile (the folders made as needed)
// and reports what it holds then.
func WriteArt(gameDir, contentFile string, c *content.Content) (Art, error) {
	jobs, d, a, err := planArt(gameDir, contentFile, c)
	if err != nil {
		return a, err
	}
	defer d.Close()
	for _, j := range jobs {
		if j.current() {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(j.dst), 0o755); err != nil {
			return a, err
		}
		if j.kind == artFont {
			b, err := os.ReadFile(j.src)
			if err != nil {
				return a, err
			}
			if err := writeFile(j.dst, b); err != nil {
				return a, err
			}
		} else {
			raw, err := d.Read(j.src)
			if err != nil {
				return a, fmt.Errorf("gamecontent: %s: %w", j.src, err)
			}
			w, h, rgba, err := dds.Decode(raw)
			if err != nil {
				return a, fmt.Errorf("gamecontent: %s: %w", j.src, err)
			}
			for w > content.ArtSize || h > content.ArtSize {
				w, h, rgba = dds.Downsample(w, h, rgba)
			}
			if err := writePNG(j.dst, w, h, rgba); err != nil {
				return a, err
			}
		}
		a.Written++
	}
	return a, nil
}

// writePNG writes RGBA pixels (straight alpha, rows top to bottom) as a PNG.
func writePNG(file string, w, h int, rgba []byte) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, &image.NRGBA{Pix: rgba, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}); err != nil {
		return err
	}
	return writeFile(file, buf.Bytes())
}

// writeFile writes a file through a temporary one beside it, so a reader never sees half of it.
func writeFile(file string, b []byte) error {
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
