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
	// the Hunter AI's BotClass and the shapers without an AI (botprofiles' borrowed-AI profiles): the donor gets a
	// skill order and one spell (fixed words +0x0C/+0x10 and +0x14/+0x18, their data after the two names)
	donor, err := Bin(ts, "BotClass", map[string]any{"nounDef": "spark.Noun", "aiDefinition": "BOT_spark_Hunter.AIDefinition",
		"role": uint32(2)})
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	le.PutUint32(donor[0x0C:], 0x02000100)
	le.PutUint32(donor[0x10:], 18)
	le.PutUint32(donor[0x14:], 0x02000200)
	le.PutUint32(donor[0x18:], 1)
	for _, s := range []uint32{3, 4, 5, 3, 3, 6, 3, 4, 3, 6, 4, 4, 4, 5, 6, 5, 5, 5} {
		donor = le.AppendUint32(donor, s)
	}
	donor = append(le.AppendUint32(donor, 0x02000210), "Spell_Spark.Item\x00"...)
	files = append(files, testgame.SnapFile{Name: "AssetData/Binary/PC_Artificer.BotClass.bin", Data: donor})
	// the Tactician AI's BotClass (PC_Guardian) with a skill order and three spells, for a profile that names its own
	guardian, err := Bin(ts, "BotClass", map[string]any{"nounDef": "rock.Noun", "aiDefinition": "BOT_rock_Tactician.AIDefinition",
		"role": uint32(1)})
	if err != nil {
		return nil, err
	}
	le.PutUint32(guardian[0x0C:], 0x02000300)
	le.PutUint32(guardian[0x10:], 18)
	le.PutUint32(guardian[0x14:], 0x02000400)
	le.PutUint32(guardian[0x18:], 3)
	for _, s := range []uint32{5, 3, 4, 5, 5, 6, 5, 3, 5, 6, 3, 3, 3, 4, 6, 4, 4, 4} {
		guardian = le.AppendUint32(guardian, s)
	}
	for _, w := range []uint32{0x02000410, 0x02000420, 0x02000430} {
		guardian = le.AppendUint32(guardian, w)
	}
	guardian = append(guardian, "Spell_A.Item\x00Spell_B.Item\x00Spell_C.Item\x00"...)
	files = append(files, testgame.SnapFile{Name: "AssetData/Binary/PC_Guardian.BotClass.bin", Data: guardian})
	// the Gladiator AI's BotClass (PC_HighHuntress) with a skill order and three spells (MIKELLABOT)
	huntress, err := Bin(ts, "BotClass", map[string]any{"nounDef": "blade.Noun",
		"aiDefinition": "BOT_blade_Gladiator.AIDefinition", "role": uint32(0)})
	if err != nil {
		return nil, err
	}
	le.PutUint32(huntress[0x0C:], 0x02000500)
	le.PutUint32(huntress[0x10:], 18)
	le.PutUint32(huntress[0x14:], 0x02000600)
	le.PutUint32(huntress[0x18:], 3)
	for _, s := range []uint32{3, 5, 4, 3, 3, 6, 3, 5, 3, 6, 5, 5, 5, 4, 6, 4, 4, 4} {
		huntress = le.AppendUint32(huntress, s)
	}
	for _, w := range []uint32{0x02000610, 0x02000620, 0x02000630} {
		huntress = le.AppendUint32(huntress, w)
	}
	huntress = append(huntress, "Spell_A.Item\x00Spell_D.Item\x00Spell_E.Item\x00"...)
	files = append(files, testgame.SnapFile{Name: "AssetData/Binary/PC_HighHuntress.BotClass.bin", Data: huntress})
	for _, n := range []string{"Golem", "Survivor", "Desecrator", "MerchantPrincess", "Otter"} {
		b, err := Bin(ts, "BotClass", map[string]any{"nounDef": strings.ToLower(n) + ".Noun", "role": uint32(1)})
		if err != nil {
			return nil, err
		}
		files = append(files, testgame.SnapFile{Name: "AssetData/Binary/PC_" + n + ".BotClass.bin", Data: b})
	}
	// the map's level and its unlisted holiday markerset (the Halloween archive, halloween): a Level of 0x620 bytes
	// listing one markerset, a Markerset of 0x28 bytes with one 8-byte marker and the group "none", no condition
	level := make([]byte, 0x620)
	le.PutUint32(level[0x00:], 0x02000700)
	le.PutUint32(level[0x04:], 1)
	level = append(le.AppendUint32(level, 0x02000710), "SM_Map_v5_default.Markerset\x00"...)
	ms := make([]byte, 0x28)
	le.PutUint32(ms[0x00:], 0x02000800)
	le.PutUint32(ms[0x04:], 1)
	le.PutUint32(ms[0x08:], 0x2CA33BDB)
	le.PutUint32(ms[0x14:], 0x02000810)
	le.PutUint32(ms[0x18:], 0x3F800000)
	ms = append(ms, "MARKER01none\x00"...)
	files = append(files, testgame.SnapFile{Name: "AssetData/Binary/SM_Map_v5.Level.bin", Data: level},
		testgame.SnapFile{Name: "AssetData/Binary/SM_Map_v5_holiday_treatment.Markerset.bin", Data: ms})
	// a file that doesn't parse (left out, as assetdata.py dump leaves it out) and the catalog
	files = append(files, testgame.SnapFile{Name: "AssetData/Binary/broken.CollectionItem.bin", Data: []byte{1, 2, 3}},
		testgame.SnapFile{Name: "AssetData/Binary/catalog_153.bin", Data: catalog()})
	return files, nil
}

// catalog is a Catalog listing the nine BotClasses (CatalogEntries of 0x30 bytes: name, compile time, typeCrc
// 0xDF9D7C39 = the BotClass table's, source file, location and status 4; then each one's two strings).
func catalog() []byte {
	le := binary.LittleEndian
	names := []string{"PC_Warden", "PC_Artificer", "PC_Golem", "PC_Survivor", "PC_Guardian", "PC_Desecrator",
		"PC_HighHuntress", "PC_MerchantPrincess", "PC_Otter"}
	d := le.AppendUint32(le.AppendUint32(nil, 0x02000000), uint32(len(names)))
	var strs []byte
	for i, n := range names {
		e := make([]byte, 0x30)
		le.PutUint32(e[0x00:], 0x02000010+uint32(0x20*i))
		le.PutUint64(e[0x08:], 1415127097)
		le.PutUint32(e[0x14:], 0xDF9D7C39)
		le.PutUint32(e[0x1C:], 0x02000020+uint32(0x20*i))
		e[0x20], e[0x21] = 4, 4
		d = append(d, e...)
		strs = append(strs, n+".BotClass\x00Heroes/ember/"+n+".Noun\x00"...)
	}
	return append(d, strs...)
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
	if err := os.WriteFile(filepath.Join(data, "Battlegrounds.snap"), Battlegrounds(), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(data, "Global.snap"), Global(), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(data, "Locale", "en-us", "Locale.package"), Locale(), 0o644)
}
