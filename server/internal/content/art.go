package content

// The art the setup writes beside the content file from the operator's game copy (setup/gamecontent WriteArt): the
// match items' icons and the shapers' portraits as small PNGs, and two of the launcher's fonts for the Discord result
// picture. Like the content file it is never committed or shipped: every server makes its own.

import "path/filepath"

// ArtSize is the icons' and portraits' size in pixels (the game's 256 x 256 textures halved twice; a smaller
// texture stays as it is).
const ArtSize = 64

// ArtFonts are the launcher's fonts copied into FontDir (from the copy's launcher\data\fonts): the game's text face
// and its heading face. Commercial fonts: only copied into the data folder, never served over HTTP, never in git
// or a release.
var ArtFonts = []string{"FrizQuadrata.TTF", "MasonSanAlt.ttf"}

// IconDir is the folder of the match items' icons beside a content file (<item>.png, IconFile).
func IconDir(contentFile string) string { return filepath.Join(filepath.Dir(contentFile), "itemicons") }

// IconFile is an item's icon file name in IconDir.
func IconFile(item string) string { return item + ".png" }

// PortraitDir is the folder of the shapers' portraits beside a content file (<shaper>.png by the shaper's
// PlayerClass name, PortraitFile).
func PortraitDir(contentFile string) string {
	return filepath.Join(filepath.Dir(contentFile), "portraits")
}

// PortraitFile is a shaper's portrait file name in PortraitDir ("PC_Alpha.png").
func PortraitFile(shaper string) string { return shaper + ".png" }

// FontDir is the folder of the copied fonts beside a content file.
func FontDir(contentFile string) string { return filepath.Join(filepath.Dir(contentFile), "fonts") }
