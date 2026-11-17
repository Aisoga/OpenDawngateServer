package dgserver

// The match items' icons for the history page: the PNGs beside the content file (content.IconDir), which
// --make-content and the setup's content step write from the game copy (gamecontent.WriteArt). web.ItemIcons serves
// the listed names only: a match item's file that is there.

import (
	"fmt"
	"os"
	"path/filepath"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/gamecontent"
	"dawngate/server/internal/web"
)

// itemIconsWeb is what the HTTP server answers under itemicons/: nil without content or a content file (the paths
// are then 404, and the page draws letter boxes).
func itemIconsWeb(c *content.Content, contentFile string) *web.ItemIcons {
	if c == nil || len(c.MatchItems) == 0 || contentFile == "" {
		return nil
	}
	dir := content.IconDir(contentFile)
	names := map[string]bool{}
	for _, it := range c.MatchItems {
		name := content.IconFile(it.Name)
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && st.Mode().IsRegular() {
			names[name] = true
		}
	}
	return &web.ItemIcons{Dir: dir, Names: names}
}

// itemIconsNote is the start-up line about the icons.
func itemIconsNote(ic *web.ItemIcons, c *content.Content) string {
	switch {
	case ic == nil:
		return "item icons: no content file with match items, itemicons/ answers 404"
	case len(ic.Names) == 0:
		return fmt.Sprintf("item icons: none in %s (the history page draws letters; run the setup, or --make-content <game folder>)", ic.Dir)
	}
	return fmt.Sprintf("item icons: %d of %d match items' icons from %s", len(ic.Names), len(c.MatchItems), ic.Dir)
}

// makeArt writes the art beside the content file (--make-content from a game folder; from an AssetData dump there
// are no textures to read, so none).
func makeArt(dir, file string, c *content.Content) error {
	if !gamecontent.IsGameDir(dir) {
		fmt.Println("no item icons, portraits or fonts: --make-content from a dump folder (give the game folder for them)")
		return nil
	}
	a, err := gamecontent.WriteArt(dir, file, c)
	if err != nil {
		return err
	}
	fmt.Printf("art beside %s: %s (%d written)\n", file, a.Summary(), a.Written)
	return nil
}
