package gamecontent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/testgame"
)

// contentSHA is the SHA-256 of the content.json the Python pipeline makes from build 854 (snap.py extract
// + assetdata.py dump --locale + dgserver --make-content), in content.Format
// dawngate-content-2. A change of content.Build changes it on purpose: then make the file with the pipeline
// again (or check TestBuildMatchesDump against a fresh dump) and update the hash. Since the withheld items
// (content's withheldItems: 16 of the 276 items under "withheld") it is the pipeline's earlier file
// (be325ff9…) loaded and written again by content.Load and Marshal, which gives the same bytes as Build. Since
// content format 3 (the item details) it is Build's own: the pipeline's dump gives the same file but for the
// passives' titles, texts and labels, the stat lines and the shop filters' titles, which only the game folder's
// scripts and Locale resolve (TestBuildMatchesDump).
const contentSHA = "aac14ad4e78325a0e6b3ec8d98e3e1e230369bb9755556714db136d41a8f8e13"

func TestBuildMatchesPipeline(t *testing.T) {
	dir := testgame.Dir(t)
	got, err := JSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(got)
	if h := hex.EncodeToString(sum[:]); h != contentSHA {
		t.Errorf("content.json SHA-256 %s, the pipeline's %s (content.Format %s)", h, contentSHA, content.Format)
	}
	// every form of the folder works
	for _, d := range []string{filepath.Join(dir, "game"), filepath.Join(dir, "game", "Data")} {
		if again, err := JSON(d); err != nil || !bytes.Equal(again, got) {
			t.Errorf("JSON(%s): %v", d, err)
		}
	}
	file := filepath.Join(t.TempDir(), "data", "content.json")
	c, err := Write(dir, file)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(file); !bytes.Equal(raw, got) {
		t.Error("Write wrote other bytes than JSON")
	}
	if len(c.Items) != 260 || len(c.Withheld) != 16 || len(c.Shapers) != 42 || len(c.Perks) != 108 || len(c.MatchItems) != 106 || len(c.LootBags) != 5 {
		t.Errorf("%d items, %d shapers, %d perks, %d match items, %d loot bags", len(c.Items), len(c.Shapers), len(c.Perks),
			len(c.MatchItems), len(c.LootBags))
	}
}

// With an assetdata.py dump of the same files (DAWNGATE_ASSETDATA_DUMP), the game path and the dump path of
// the current content.Build give the same bytes.
func TestBuildMatchesDump(t *testing.T) {
	dump := os.Getenv("DAWNGATE_ASSETDATA_DUMP")
	if dump == "" {
		t.Skip("set DAWNGATE_ASSETDATA_DUMP to an assetdata.py dump of the same game files")
	}
	g, err := Build(testgame.Dir(t))
	if err != nil {
		t.Fatal(err)
	}
	// the titles only the game folder resolves (the passives' scripts, the shop's "^ItemShop" Locale keys)
	for i := range g.MatchItems {
		for j := range g.MatchItems[i].Passives {
			g.MatchItems[i].Passives[j].Title, g.MatchItems[i].Passives[j].Text, g.MatchItems[i].Passives[j].Parts = "", "", nil
			g.MatchItems[i].Passives[j].Kind = ""
		}
		for j := range g.MatchItems[i].Stats { // the stat lines from the Locale (ITEMSHOP)
			g.MatchItems[i].Stats[j].Parts = nil
		}
	}
	for i := range g.ItemFilters {
		g.ItemFilters[i].Title = ""
		for j := range g.ItemFilters[i].Tags {
			g.ItemFilters[i].Tags[j].Title = ""
		}
	}
	got, err := g.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	c, err := content.Build(dump)
	if err != nil {
		t.Fatal(err)
	}
	want, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("the game folder's content differs from the dump's")
	}
}
