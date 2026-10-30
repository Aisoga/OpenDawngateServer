package gamecontent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/testgame/synthgame"
)

// synthContentSHA is the SHA-256 of the content.json the Python pipeline (snap.py extract, assetdata.py
// dump --locale, dgserver --make-content) makes from synthgame's folder; since content format 3 (the item
// details: synthgame has no Item, so only the format name changed) the file Build makes.
const synthContentSHA = "45417119e9cb4dd10f6f4657c6a0c4666f703a3d0cf4dcd559c0923adff912ea"

func TestSyntheticGame(t *testing.T) {
	dir := t.TempDir()
	if err := synthgame.Write(dir); err != nil {
		t.Fatal(err)
	}
	c, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	wantItems := []content.Item{
		{CatalogID: 0x10000001, Name: "ember", Type: "shaper", ItemType: content.ItemShaper, Status: "none", Asset: "ember.Noun",
			Title: "Ember", MaxOwnable: 1, Shaper: "PC_Ember"},
		{CatalogID: 0x10000002, Name: "ember_frost", Type: "shaper skin", ItemType: content.ItemShaperSkin, Status: "none",
			Asset: "ember_frost.Noun", Title: "Frost Ember", MaxOwnable: 1, Shaper: "PC_Ember"},
		{CatalogID: 0x10000003, Name: "lantern", Type: "ward", ItemType: content.ItemWardSkin, Status: "alwaysOwned",
			Asset: "lantern.Noun", MaxOwnable: 1},
	}
	if !reflect.DeepEqual(c.Items, wantItems) {
		t.Errorf("items %+v", c.Items)
	}
	wantShapers := []content.Shaper{
		{Name: "PC_Ember", CatalogID: 0x10000001, Title: "Ember the Bright", Archetype: "mage", SubArchetype: "control mage",
			Abilities: []string{"ability_Ember_Basic", "ability_Ember_Passive"}},
		{Name: "PC_Test", Archetype: "carry", SubArchetype: "ranged carry", TestingOnly: true},
	}
	if !reflect.DeepEqual(c.Shapers, wantShapers) {
		t.Errorf("shapers %+v", c.Shapers)
	}
	raw, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if h := hex.EncodeToString(sum[:]); h != synthContentSHA {
		t.Errorf("content.json SHA-256 %s, the Python pipeline's %s:\n%s", h, synthContentSHA, raw)
	}
}

func TestWriteSyntheticGame(t *testing.T) {
	dir := os.Getenv("DAWNGATE_P3A2_SYNTH_OUT")
	if dir == "" {
		t.Skip("set DAWNGATE_P3A2_SYNTH_OUT to write synthgame's folder there")
	}
	if err := synthgame.Write(filepath.Join(dir, "Dawngate")); err != nil {
		t.Fatal(err)
	}
}

func TestDataDirAndErrors(t *testing.T) {
	dir := t.TempDir()
	if IsGameDir(dir) {
		t.Error("an empty folder is a game folder")
	}
	if _, err := Build(dir); err == nil {
		t.Error("built from an empty folder")
	}
	synthgame.Write(dir)
	for _, d := range []string{dir, filepath.Join(dir, "game"), filepath.Join(dir, "game", "Data")} {
		got, err := DataDir(d)
		if err != nil || got != filepath.Join(dir, "game", "Data") {
			t.Errorf("DataDir(%s) = %s, %v", d, got, err)
		}
	}
	// without the Locale package, with a broken archive
	loc := filepath.Join(dir, "game", "Data", LocalePackage)
	os.Rename(loc, loc+".x")
	if _, err := Build(dir); err == nil {
		t.Error("built without Locale.package")
	}
	os.Rename(loc+".x", loc)
	snapPath := filepath.Join(dir, "game", "Data", AssetDataSnap)
	os.WriteFile(snapPath, []byte("pans but broken"), 0o644)
	if _, err := Build(dir); err == nil {
		t.Error("built from a broken archive")
	}
}

// A Source without CollectionItem fails; one without an optional class doesn't.
func TestSourceMissingClass(t *testing.T) {
	src := func(class string, into any) error {
		if class == "PlayerClass" || class == "CollectionItem" {
			return nil // empty
		}
		return fs.ErrNotExist
	}
	if _, err := content.BuildFrom(src); err != nil {
		t.Errorf("optional classes missing: %v", err)
	}
	src2 := func(class string, into any) error { return errors.Join(errors.New("gone"), fs.ErrNotExist) }
	if _, err := content.BuildFrom(src2); err == nil {
		t.Error("built without CollectionItem")
	}
}
