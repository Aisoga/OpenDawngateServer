// Package gamecontent makes the server's content file (content.json, server/internal/content; B7a) from
// the game folder alone: game\Data\AssetData.snap and game\Data\Locale\en-us\Locale.package, read in
// memory with the Go ports of snap.py, dbpf.py and assetdata.py. It
// replaces the pipeline snap.py extract + assetdata.py dump --locale + dgserver --make-content <dump>,
// byte for byte, with no Python and no dump on disk: content.BuildFrom with a Source that decodes the
// classes content.Classes names straight from the archive.
//
// Differences from the pipeline, on purpose: only those classes' files are decoded and parsed (the
// pipeline extracted and parsed all 6,970 files and stopped if any failed; all of build 854's parse).
package gamecontent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/dbpf"
	"dawngate/server/internal/setup/snap"
)

// The inputs, relative to the Data folder.
var (
	AssetDataSnap  = "AssetData.snap"
	LocalePackage  = filepath.Join("Locale", "en-us", "Locale.package")
	errNoGameFiles = errors.New("gamecontent: no game\\Data\\AssetData.snap")
)

// DataDir finds the game's Data folder from dir: dir\game\Data (the Dawngate folder, e.g.
// client\ia_Dawngate\Dawngate or a player copy's Dawngate folder), dir\Data (its game folder) or dir
// itself, whichever holds AssetData.snap first.
func DataDir(dir string) (string, error) {
	for _, d := range []string{filepath.Join(dir, "game", "Data"), filepath.Join(dir, "Data"), dir} {
		if st, err := os.Stat(filepath.Join(d, AssetDataSnap)); err == nil && !st.IsDir() {
			return d, nil
		}
	}
	return "", fmt.Errorf("%w in %s", errNoGameFiles, dir)
}

// IsGameDir reports whether DataDir finds a Data folder in dir.
func IsGameDir(dir string) bool {
	_, err := DataDir(dir)
	return err == nil
}

// Build makes the content from a game folder (any form DataDir takes).
func Build(gameDir string) (*content.Content, error) {
	data, err := DataDir(gameDir)
	if err != nil {
		return nil, err
	}
	arch, err := snap.Open(filepath.Join(data, AssetDataSnap))
	if err != nil {
		return nil, fmt.Errorf("gamecontent: %w", err)
	}
	set, err := assetdata.FromSnap(arch)
	if err != nil {
		return nil, fmt.Errorf("gamecontent: %w", err)
	}
	pkg, err := dbpf.Open(filepath.Join(data, LocalePackage))
	if err != nil {
		return nil, fmt.Errorf("gamecontent: %w", err)
	}
	loc, err := assetdata.LoadLocale(pkg)
	if err != nil {
		return nil, fmt.Errorf("gamecontent: %w", err)
	}
	ts, err := assetdata.Embedded()
	if err != nil {
		return nil, fmt.Errorf("gamecontent: %w", err)
	}
	c, err := content.BuildFrom(Source(set, ts, loc))
	if err != nil {
		return nil, err
	}
	if err := resolveItems(c, data, loc); err != nil { // the passives' names, the filters' titles (items.go)
		return nil, err
	}
	return c, nil
}

// JSON is the content file's bytes for a game folder: what dgserver --make-content writes.
func JSON(gameDir string) ([]byte, error) {
	c, err := Build(gameDir)
	if err != nil {
		return nil, err
	}
	return c.Marshal()
}

// Write builds the content from a game folder and writes it to file (creating its folder).
func Write(gameDir, file string) (*content.Content, error) {
	c, err := Build(gameDir)
	if err != nil {
		return nil, err
	}
	return c, c.Write(file)
}

// Source is the content.Source of AssetData read in memory: a class's instances as assetdata.py dump
// writes them (with the Locale's English titles), decoded into content's structs from the same JSON. A
// class without files or type table is absent (fs.ErrNotExist), as the dump then writes no file for it.
func Source(set *assetdata.Set, ts *assetdata.Types, loc *assetdata.Locale) content.Source {
	return func(class string, into any) error {
		inst, _, t, err := set.Instances(ts, class, loc)
		if assetdata.IsNoClass(err) {
			return fmt.Errorf("content: AssetData.snap has no %s files: %w", class, fs.ErrNotExist)
		}
		if err != nil {
			return fmt.Errorf("content: %w", err)
		}
		if t.Name != class {
			return fmt.Errorf("content: AssetData holds class %q, want %q", t.Name, class)
		}
		if err := json.Unmarshal(assetdata.AppendJSON(nil, inst), into); err != nil {
			return fmt.Errorf("content: AssetData %s: %v", class, err)
		}
		return nil
	}
}
