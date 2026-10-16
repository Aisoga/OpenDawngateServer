// Package synthgame writes a small made-up game folder for the tests that need no game file:
// game\Data\AssetData.snap with a few CollectionItem, Noun and PlayerClass files laid out by the
// embedded reflection tables, and game\Data\Locale\en-us\Locale.package with their titles. Nothing in it
// comes from the game.
package synthgame

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/pytext"
	"dawngate/server/internal/setup/testgame"
)

func fnv(s string) uint32 { return pytext.FNV1([]byte(strings.ToLower(s))) }

// keyHash is the hash part of a Key text of the forms "Table!0x<id>" and "plain_name".
func keyHash(text string) [3]uint32 {
	if table, id, ok := strings.Cut(text, "!0x"); ok {
		v, _ := strconv.ParseUint(id, 16, 32)
		return [3]uint32{uint32(v), 0, fnv(table)}
	}
	return [3]uint32{fnv(text), 0, 0}
}

// Bin writes a file of class from the type tables: the fixed part zero but for the members in set, and the
// out-of-line data of the set pointer members in member order. Values: uint32 for ints and enums, bool,
// string for char*/asset (the string) and Key (its text; the hash part follows from it).
func Bin(ts *assetdata.Types, class string, set map[string]any) ([]byte, error) {
	t := ts.Type(class)
	if t == nil {
		return nil, fmt.Errorf("synthgame: no type %s", class)
	}
	le := binary.LittleEndian
	d := make([]byte, t.Size)
	var ool []byte
	done := 0
	for _, m := range t.Members {
		v, ok := set[m.Name]
		if !ok {
			continue
		}
		done++
		switch x := v.(type) {
		case uint32:
			le.PutUint32(d[m.Offset:], x)
		case bool:
			if x {
				d[m.Offset] = 1
			}
		case string:
			switch m.Type {
			case "char*", "asset":
			case "Key":
				h := keyHash(x)
				at := m.Offset + 4
				if m.Delta < 0 {
					at = m.Offset + m.Delta
				}
				for k, hv := range h {
					le.PutUint32(d[at+4*k:], hv)
				}
			default:
				return nil, fmt.Errorf("synthgame: %s.%s is a %s", class, m.Name, m.Type)
			}
			le.PutUint32(d[m.Offset:], 0x02000000+uint32(len(ool)))
			ool = append(append(ool, x...), 0)
		}
	}
	if done != len(set) {
		return nil, fmt.Errorf("synthgame: %s: unknown members in %v", class, set)
	}
	return append(d, ool...), nil
}

// Files are the synthetic AssetData files by path in the archive.
func Files(ts *assetdata.Types) ([]testgame.SnapFile, error) {
	type obj struct {
		name, class string
		set         map[string]any
	}
	objs := []obj{
		{"ember", "CollectionItem", map[string]any{"catalogID": uint32(0x10000001), "parentAsset": "ember.Noun", "longName": "Ember!0x00000001"}},
		{"ember_frost", "CollectionItem", map[string]any{"type": uint32(1), "catalogID": uint32(0x10000002),
			"parentAsset": "ember_frost.Noun", "longName": "Ember!0x00000003", "shortName": "Ember!0x00000099"}},
		{"lantern", "CollectionItem", map[string]any{"type": uint32(5), "specialStatus": uint32(1), "catalogID": uint32(0x10000003),
			"parentAsset": "lantern.Noun"}},
		{"ember", "Noun", map[string]any{"playerClassData": "PC_Ember.PlayerClass"}},
		{"ember_frost", "Noun", map[string]any{"playerClassData": "PC_Ember.PlayerClass"}},
		{"lantern", "Noun", map[string]any{}},
		{"PC_Ember", "PlayerClass", map[string]any{"nameLocaleKey": "Ember!0x00000002", "basicAbility": "ability_Ember_Basic",
			"passiveAbility": "ability_Ember_Passive", "CharacterArchetype": uint32(2), "CharacterSubArchetype": uint32(4)}},
		{"PC_Test", "PlayerClass", map[string]any{"testingOnly": true}},
	}
	files := []testgame.SnapFile{{Name: "script/preload.txt", Data: []byte("assetdata/binary\n")}}
	for _, o := range objs {
		b, err := Bin(ts, o.class, o.set)
		if err != nil {
			return nil, err
		}
		files = append(files, testgame.SnapFile{Name: "AssetData/Binary/" + o.name + "." + o.class + ".bin", Data: b})
	}
	// the BotClass the bot profiles copy (botprofiles: PC_Warden)
	bc, err := Bin(ts, "BotClass", map[string]any{"nounDef": "ember.Noun", "aiDefinition": "BOT_ember.AIDefinition",
		"role": uint32(1)})
	if err != nil {
		return nil, err
	}
	files = append(files, testgame.SnapFile{Name: "AssetData/Binary/PC_Warden.BotClass.bin", Data: bc})
	// a file that doesn't parse (left out, as assetdata.py dump leaves it out) and the catalog
	files = append(files, testgame.SnapFile{Name: "AssetData/Binary/broken.CollectionItem.bin", Data: []byte{1, 2, 3}},
		testgame.SnapFile{Name: "AssetData/Binary/catalog_153.bin", Data: catalog()})
	return files, nil
}

// catalog is a Catalog listing PC_Warden.BotClass alone (a CatalogEntry of 0x30 bytes: name, compile time, typeCrc
// 0xDF9D7C39 = the BotClass table's, source file, location and status 4; then its two strings).
func catalog() []byte {
	le := binary.LittleEndian
	e := make([]byte, 0x30)
	le.PutUint32(e[0x00:], 0x02000010)
	le.PutUint64(e[0x08:], 1415127097)
	le.PutUint32(e[0x14:], 0xDF9D7C39)
	le.PutUint32(e[0x1C:], 0x02000020)
	e[0x20], e[0x21] = 4, 4
	d := le.AppendUint32(le.AppendUint32(nil, 0x02000000), 1)
	return append(append(d, e...), "PC_Warden.BotClass\x00Heroes/ember/PC_Warden.Noun\x00"...)
}

// Locale is the synthetic English Locale.package.
func Locale() []byte {
	text := "// titles\r\n0x00000001 Ember\r\n0x00000002 Ember the Bright\r\n0x00000003 Frost Ember\r\n"
	return testgame.Package(0, []testgame.PackageEntry{{Type: 0x02FAC0B6, Instance: uint64(fnv("Ember")),
		Data: testgame.RefPackLiterals([]byte(text)), Comp: 0xFFFF, USize: uint32(len(text))}})
}

// Write makes the synthetic game folder dir (dir\game\Data\...).
func Write(dir string) error {
	ts, err := assetdata.Embedded()
	if err != nil {
		return err
	}
	files, err := Files(ts)
	if err != nil {
		return err
	}
	data := filepath.Join(dir, "game", "Data")
	if err := os.MkdirAll(filepath.Join(data, "Locale", "en-us"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(data, "AssetData.snap"), testgame.Snap(files), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(data, "LoginScreen.snap"), LoginScreen(), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(data, "Locale", "en-us", "Locale.package"), Locale(), 0o644)
}
