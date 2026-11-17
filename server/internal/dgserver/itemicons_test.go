package dgserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/content"
)

// The icons served are the match items' files beside the content file; another file there isn't; without content
// or a file nothing is served, and the start-up line says why.
func TestItemIconsWeb(t *testing.T) {
	file := filepath.Join(t.TempDir(), "content.json")
	c := &content.Content{MatchItems: []content.MatchItem{{Name: "Basic_Time"}, {Name: "Basic_Spark"}}}
	if ic := itemIconsWeb(nil, file); ic != nil || !strings.Contains(itemIconsNote(ic, nil), "answers 404") {
		t.Errorf("no content: %+v", ic)
	}
	if ic := itemIconsWeb(c, ""); ic != nil {
		t.Errorf("no content file: %+v", ic)
	}
	ic := itemIconsWeb(c, file)
	if ic == nil || len(ic.Names) != 0 || !strings.HasPrefix(itemIconsNote(ic, c), "item icons: none in ") {
		t.Errorf("no icons: %+v %q", ic, itemIconsNote(ic, c))
	}
	dir := content.IconDir(file)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "Basic_Time.png"), []byte("png"), 0o644)
	os.WriteFile(filepath.Join(dir, "Other.png"), []byte("png"), 0o644)
	ic = itemIconsWeb(c, file)
	if len(ic.Names) != 1 || !ic.Names["Basic_Time.png"] || ic.Dir != dir {
		t.Errorf("%+v", ic)
	}
	if note := itemIconsNote(ic, c); note != "item icons: 1 of 2 match items' icons from "+dir {
		t.Errorf("%q", note)
	}
}
