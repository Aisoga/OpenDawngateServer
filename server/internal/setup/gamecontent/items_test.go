package gamecontent

import (
	"os"
	"path/filepath"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/dbpf"
)

func TestResolveItems(t *testing.T) {
	dir := t.TempDir()
	src := "modifier_Item_Test_Shock = nAbility.create()\n" +
		"modifier_Item_Test_Shock.localizedGroup = nUtil.SPID(\"ItemModifiers\")\n" +
		"modifier_Item_Test_Shock.localizedName = nUtil.ToGUID(\"0x00000042\")\n" +
		"nAbility.RegisterAbility(\"modifier_Item_Test_Shock\")\n"
	src2 := "modifier_Item_Test_Aura = nAbility.create()\n" +
		"modifier_Item_Test_Aura.localizedDescription = nUtil.ToGUID(\"0x00000043\")\n" +
		"nAbility.RegisterAbility(\"modifier_Item_Test_Aura\")\n"
	os.WriteFile(filepath.Join(dir, ServerDataPackage), dbpf.WritePackage([]dbpf.NewEntry{
		{Type: luaType, Group: fnv("Modifiers"), Instance: uint64(fnv("modifier_Item_Test_Shock ")), Data: []byte(src)},
		{Type: luaType, Group: fnv("Modifiers"), Instance: uint64(fnv("modifier_Item_Test_Aura")), Data: []byte(src2)},
	}), 0o644)
	pkg, err := dbpf.Parse(dbpf.WritePackage([]dbpf.NewEntry{
		{Type: 1, Group: 1, Instance: uint64(fnv("ItemModifiers")), Data: []byte("0x00000042 Shock\n")},
		{Type: 1, Group: 1, Instance: uint64(fnv("Other")), Data: []byte("0x00000043 <font color=\"#FFFF99\">Glow</font>: shines.\n")},
		{Type: 1, Group: 1, Instance: uint64(fnv("ItemShop")), Data: []byte("0x00000030  Offense\n0x00000031 Power\n")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	loc, err := assetdata.LoadLocale(pkg)
	if err != nil {
		t.Fatal(err)
	}
	c := fixture(t)
	if err := resolveItems(c, dir, loc); err != nil {
		t.Fatal(err)
	}
	if s, _ := c.MatchItem("Legendary_Storm"); s.Passives[0].Title != "Shock" {
		t.Errorf("Storm's passive %+v", s.Passives)
	}
	if g, _ := c.MatchItem("Advanced_Guard"); g.Passives[0].Title != "Glow" {
		t.Errorf("Guard's passive %+v", g.Passives)
	}
	if f := c.ItemFilters[0]; f.Title != "Offense" || f.Tags[0].Title != "Power" || f.Tags[1].Title != "Pierce" {
		t.Errorf("filter %+v", f)
	}
	// without ServerData.package the passives keep no title; the filters still get theirs
	c = fixture(t)
	if err := resolveItems(c, t.TempDir(), loc); err != nil {
		t.Fatal(err)
	}
	if s, _ := c.MatchItem("Legendary_Storm"); s.Passives[0].Title != "" || c.ItemFilters[0].Title != "Offense" {
		t.Errorf("without scripts: %+v %+v", s.Passives, c.ItemFilters[0])
	}
}

func fixture(t *testing.T) *content.Content {
	t.Helper()
	c, err := content.Build(filepath.Join(golden.Dir(), "assetdata"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
