package botprofiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"

	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// MadeUp is a made-up AssetData.snap (re/scripts/test_bot_profiles.py builds the same bytes): a catalog of two
// entries, PC_Warden.BotClass (BotClass's typeCrc, a source file, no tags) and lantern.Noun (two tags), and a
// PC_Warden.BotClass.bin whose role is tactician. Nothing in it comes from the game.
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
	cat := le.AppendUint32(le.AppendUint32(nil, 0x02000000), 2)
	cat = append(cat, ent(0x02000010, BotClassTypeCRC, 0x02000020, 0)...)
	cat = append(cat, ent(0x02000040, 0x12345678, 0x02000050, 2)...)
	cat = append(cat, "PC_Warden.BotClass\x00Heroes/warden/PC_Warden.Noun\x00lantern.Noun\x00Props/lantern.Noun\x00"...)
	cat = le.AppendUint32(le.AppendUint32(cat, 0x02000060), 0x02000070)
	cat = append(cat, "placeable\x00npc\x00"...)
	bc := make([]byte, botClassSize)
	le.PutUint32(bc[0x00:], 0x02000080)
	le.PutUint32(bc[0x04:], 0x02000090)
	le.PutUint32(bc[0x08:], RoleTactician)
	bc = append(bc, "ember.Noun\x00BOT_ember_Tactician.AIDefinition\x00"...)
	return snap.Pack([]snap.File{{Name: Catalog, Data: cat}, {Name: "AssetData/Binary/PC_Warden.BotClass.bin", Data: bc}})
}

// The SHA-256s re/scripts/bot_profiles.py gives (test_bot_profiles.py pins the same values).
const (
	madeUpSHA     = "7baaa80e0bb296ba2d54c5dfb8c9277059d0cce7e94e340bd94417cb80432fea"
	madeUpArchive = "41066970b54e20a03c6cb57e2feacf44fdf399f5ed4cbadcba2af26e842b754b"
	game854       = "b0eff5ba01d1ace3df4839050c71b243f0ff711bd3b98d6bed9737b752a7d515" // from build 854's AssetData.snap
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
