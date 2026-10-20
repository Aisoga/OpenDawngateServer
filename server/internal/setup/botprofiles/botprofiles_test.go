package botprofiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// MadeUp is a made-up AssetData.snap (re/scripts/test_bot_profiles.py builds the same bytes): a catalog of ten
// entries, PC_Warden.BotClass (BotClass's typeCrc, a source file, no tags), lantern.Noun (two tags), PC_Artificer,
// PC_Golem, PC_Survivor, PC_Guardian, PC_Desecrator, PC_HighHuntress, PC_MerchantPrincess and PC_Otter (BotClasses),
// a PC_Warden.BotClass.bin whose role is tactician, a PC_Artificer, a PC_Guardian and a PC_HighHuntress one with an
// AI, a skill and a spell order, and five with a noun only. Nothing in it comes from the game.
func MadeUp() []byte {
	le := binary.LittleEndian
	ent := func(name, typeCrc, source, tags uint32) []byte {
		e := make([]byte, entrySize)
		le.PutUint32(e[0x00:], name)
		le.PutUint64(e[0x08:], 1415127097)
		le.PutUint32(e[0x14:], typeCrc)
		le.PutUint32(e[0x1C:], source)
		e[0x20], e[0x21] = 4, 4
		if tags > 0 {
			le.PutUint32(e[0x24:], 0x02000030)
			le.PutUint32(e[0x28:], tags)
		}
		return e
	}
	cat := le.AppendUint32(le.AppendUint32(nil, 0x02000000), 10)
	cat = append(cat, ent(0x02000010, BotClassTypeCRC, 0x02000020, 0)...)
	cat = append(cat, ent(0x02000040, 0x12345678, 0x02000050, 2)...)
	for i := uint32(0); i < 8; i++ {
		cat = append(cat, ent(0x020000A0+0x20*i, BotClassTypeCRC, 0x020000B0+0x20*i, 0)...)
	}
	cat = append(cat, "PC_Warden.BotClass\x00Heroes/warden/PC_Warden.Noun\x00lantern.Noun\x00Props/lantern.Noun\x00"...)
	cat = le.AppendUint32(le.AppendUint32(cat, 0x02000060), 0x02000070)
	cat = append(cat, "placeable\x00npc\x00"...)
	cat = append(cat, "PC_Artificer.BotClass\x00Heroes/spark/PC_Spark.Noun\x00PC_Golem.BotClass\x00Heroes/stone/PC_Stone.Noun\x00"+
		"PC_Survivor.BotClass\x00Heroes/thorn/PC_Thorn.Noun\x00PC_Guardian.BotClass\x00Heroes/rock/PC_Rock.Noun\x00"+
		"PC_Desecrator.BotClass\x00Heroes/bone/PC_Bone.Noun\x00"+
		"PC_HighHuntress.BotClass\x00Heroes/blade/PC_Blade.Noun\x00"+
		"PC_MerchantPrincess.BotClass\x00Heroes/coin/PC_Coin.Noun\x00"+
		"PC_Otter.BotClass\x00Heroes/reed/PC_Reed.Noun\x00"...)
	bc := make([]byte, botClassSize)
	le.PutUint32(bc[0x00:], 0x02000080)
	le.PutUint32(bc[0x04:], 0x02000090)
	le.PutUint32(bc[0x08:], RoleTactician)
	bc = append(bc, "ember.Noun\x00BOT_ember_Tactician.AIDefinition\x00"...)
	words := func(ws ...uint32) []byte {
		var out []byte
		for _, w := range ws {
			out = le.AppendUint32(out, w)
		}
		return out
	}
	hunter := words(0x02000100, 0x02000110, RoleHunter, 0x02000120, 18, 0x02000130, 3)
	hunter = append(hunter, "spark.Noun\x00BOT_spark_Hunter.AIDefinition\x00"...)
	hunter = append(hunter, words(5, 4, 3, 5, 5, 6, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4)...)
	hunter = append(hunter, words(0x02000140, 0x02000150, 0x02000160)...)
	hunter = append(hunter, "Spell_A.Item\x00Spell_B.Item\x00Spell_C.Item\x00"...)
	stone := append(words(0x02000170, 0, RoleTactician, 0, 0, 0, 0), "stone.Noun\x00"...)
	thorn := append(words(0x02000180, 0, RoleTactician, 0, 0, 0, 0), "thorn.Noun\x00"...)
	rock := words(0x02000190, 0x020001A0, RoleTactician, 0x020001B0, 18, 0x020001C0, 3)
	rock = append(rock, "rock.Noun\x00BOT_rock_Tactician.AIDefinition\x00"...)
	rock = append(rock, words(5, 3, 4, 5, 5, 6, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3)...)
	rock = append(rock, words(0x020001D0, 0x020001E0, 0x020001F0)...)
	rock = append(rock, "Spell_A.Item\x00Spell_D.Item\x00Spell_E.Item\x00"...)
	bone := append(words(0x02000200, 0, RoleTactician, 0, 0, 0, 0), "bone.Noun\x00"...)
	blade := words(0x02000210, 0x02000220, RoleGladiator, 0x02000230, 18, 0x02000240, 3)
	blade = append(blade, "blade.Noun\x00BOT_blade_Gladiator.AIDefinition\x00"...)
	blade = append(blade, words(3, 5, 4, 3, 3, 6, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5)...)
	blade = append(blade, words(0x02000250, 0x02000260, 0x02000270)...)
	blade = append(blade, "Spell_A.Item\x00Spell_F.Item\x00Spell_G.Item\x00"...)
	coin := append(words(0x02000280, 0, RoleTactician, 0, 0, 0, 0), "coin.Noun\x00"...)
	reed := append(words(0x02000290, 0, RoleTactician, 0, 0, 0, 0), "reed.Noun\x00"...)
	return snap.Pack([]snap.File{{Name: Catalog, Data: cat}, {Name: "AssetData/Binary/PC_Warden.BotClass.bin", Data: bc},
		{Name: "AssetData/Binary/PC_Artificer.BotClass.bin", Data: hunter},
		{Name: "AssetData/Binary/PC_Golem.BotClass.bin", Data: stone},
		{Name: "AssetData/Binary/PC_Survivor.BotClass.bin", Data: thorn},
		{Name: "AssetData/Binary/PC_Guardian.BotClass.bin", Data: rock},
		{Name: "AssetData/Binary/PC_Desecrator.BotClass.bin", Data: bone},
		{Name: "AssetData/Binary/PC_HighHuntress.BotClass.bin", Data: blade},
		{Name: "AssetData/Binary/PC_MerchantPrincess.BotClass.bin", Data: coin},
		{Name: "AssetData/Binary/PC_Otter.BotClass.bin", Data: reed}})
}

// The SHA-256s re/scripts/bot_profiles.py gives (test_bot_profiles.py pins the same values).
const (
	madeUpSHA     = "077b45f323ca19dc1bb074c1c98b4854c1e91a88a8a8c96247046b2e10a7c100"
	madeUpArchive = "7bd9e97f1097388a9501a76bcd8d7045d1c17c6a7c9ba76d057dd29c0b002fb7"
	game854       = "933a9d5732368d53d6597dec2e54ca777f2252056533feb9a0d5856b42fe02fe" // from build 854's AssetData.snap
)

func TestBuildMatchesPython(t *testing.T) {
	in := MadeUp()
	if got := digest(in); got != madeUpSHA {
		t.Errorf("the made-up AssetData.snap: SHA-256 %s, test_bot_profiles.py's %s", got, madeUpSHA)
	}
	out, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := digest(out); got != madeUpArchive {
		t.Errorf("the archive: SHA-256 %s, bot_profiles.py's %s", got, madeUpArchive)
	}
	checkArchive(t, in, out)
}

// checkArchive reads the archive back: the catalog lists every profile after the copy's own entries, unchanged, and
// each profile's file is its base's with the role changed.
func checkArchive(t *testing.T, in, out []byte) {
	t.Helper()
	src, err := snap.Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	a, err := snap.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Entries) != 1+len(Profiles) || a.Entries[0].Name != Catalog {
		t.Fatalf("entries %v", a.Entries)
	}
	find := func(ar *snap.Archive, name string) []byte {
		for _, e := range ar.Entries {
			if strings.EqualFold(e.Name, name) {
				b, err := ar.Read(e)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
		}
		t.Fatalf("no %s", name)
		return nil
	}
	old, err := parseCatalog(find(src, Catalog))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := parseCatalog(find(a, Catalog))
	if err != nil {
		t.Fatal(err)
	}
	if len(cat) != len(old)+len(Profiles) {
		t.Fatalf("catalog: %d entries, want %d", len(cat), len(old)+len(Profiles))
	}
	for i, e := range old {
		if !bytes.Equal(cat[i].fixed, e.fixed) || cat[i].name != e.name || cat[i].source != e.source {
			t.Errorf("catalog entry %d (%s) changed", i, e.name)
		}
	}
	for i, p := range Profiles {
		e := cat[len(old)+i]
		if e.name != p.Name+".BotClass" || binary.LittleEndian.Uint32(e.fixed[0x14:]) != BotClassTypeCRC {
			t.Errorf("profile %s: entry %q", p.Name, e.name)
		}
		base := find(src, "AssetData/Binary/"+p.Base+".BotClass.bin")
		b := find(a, "AssetData/Binary/"+p.Name+".BotClass.bin")
		if p.AI != "" {
			// a borrowed AI: the base's noun on the donor's AI, the role, the table's skill and spell order (else the donor's)
			fb, err1 := parseBotClass(base)
			fd, err2 := parseBotClass(find(src, "AssetData/Binary/"+p.AI+".BotClass.bin"))
			f, err3 := parseBotClass(b)
			if err1 != nil || err2 != nil || err3 != nil {
				t.Fatalf("profile %s: %v %v %v", p.Name, err1, err2, err3)
			}
			skills, spells := p.Skills, p.Spells
			if skills == nil {
				skills = fd.skills
			}
			if spells == nil {
				spells = fd.spells
			}
			if f.noun != fb.noun || f.ai != fd.ai || f.words[2] != p.Role || fmt.Sprint(f.skills) != fmt.Sprint(skills) ||
				fmt.Sprint(f.spells) != fmt.Sprint(spells) {
				t.Errorf("profile %s: %s on %s, role %d, skills %v, spells %v", p.Name, f.noun, f.ai, f.words[2], f.skills, f.spells)
			}
			continue
		}
		if len(b) != len(base) || binary.LittleEndian.Uint32(b[botClassRole:]) != p.Role ||
			!bytes.Equal(b[:botClassRole], base[:botClassRole]) || !bytes.Equal(b[botClassRole+4:], base[botClassRole+4:]) {
			t.Errorf("profile %s: the file is not its base's with role %d", p.Name, p.Role)
		}
	}
}

func TestBuildRefuses(t *testing.T) {
	le := binary.LittleEndian
	in := MadeUp()
	a, _ := snap.Parse(in)
	cat, _ := a.Read(a.Entries[0])
	if _, err := BuildCatalog(cat, [][2]string{{"x.BotClass", "lantern.Noun"}}); err == nil {
		t.Error("a base of another class was taken")
	}
	if _, err := BuildCatalog(cat, [][2]string{{"PC_Warden.BotClass", "PC_Warden.BotClass"}}); err == nil {
		t.Error("a name already listed was taken")
	}
	if _, err := BuildCatalog(cat, [][2]string{{"x.BotClass", "missing.BotClass"}}); err == nil {
		t.Error("a missing base was taken")
	}
	if _, err := BuildCatalog(cat[:len(cat)-1], nil); err == nil {
		t.Error("a cut catalog was taken")
	}
	if _, err := BuildCatalog(append(append([]byte(nil), cat...), 0), nil); err == nil {
		t.Error("a catalog with a byte too many was taken")
	}
	bad := append([]byte(nil), cat...)
	le.PutUint32(bad[4:], 0)
	if _, err := BuildCatalog(bad, nil); err == nil {
		t.Error("an empty catalog was taken")
	}
	if _, err := BuildBotClass(make([]byte, 8), RolePredator); err == nil {
		t.Error("a short BotClass was taken")
	}
	bc := make([]byte, botClassSize)
	le.PutUint32(bc[botClassRole:], 9)
	if _, err := BuildBotClass(bc, RolePredator); err == nil {
		t.Error("a BotClass with role 9 was taken")
	}
	if _, err := Build([]byte("not a snap")); err == nil {
		t.Error("a file that is no archive was taken")
	}
}

// Build 854's AssetData.snap (skipped without the game files).
func TestBuildGame(t *testing.T) {
	in := testgame.Read(t, "AssetData.snap")
	out, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := digest(out); got != game854 {
		t.Errorf("SHA-256 %s, bot_profiles.py's %s", got, game854)
	}
	checkArchive(t, in, out)
}
