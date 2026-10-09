package content

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"dawngate/server/internal/golden"
)

// Fixture is the content of the synthetic AssetData dump in server/testdata/assetdata: made-up names
// and ids in the shape re/scripts/assetdata.py dump writes (no game data). The blaze and dgserver tests
// build it the same way.
func Fixture(t testing.TB) *Content {
	t.Helper()
	c, err := Build(filepath.Join(golden.Dir(), "assetdata"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// BuildFrom asks its Source for exactly Classes, in that order; the dump folder's source gives the
// fixture's content.
func TestBuildFromSource(t *testing.T) {
	var asked []string
	dump := DumpDir(filepath.Join(golden.Dir(), "assetdata"))
	c, err := BuildFrom(func(class string, into any) error {
		asked = append(asked, class)
		return dump(class, into)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(asked, Classes) {
		t.Errorf("asked %v, Classes %v", asked, Classes)
	}
	if !reflect.DeepEqual(c, Fixture(t)) {
		t.Error("BuildFrom(DumpDir) differs from Build")
	}
	raw, err := c.Marshal()
	if err != nil || raw[len(raw)-1] != '\n' {
		t.Errorf("Marshal: %v", err)
	}
}

// B7d: a shaper's or skin's Noun, a PlayerClass's shaper Noun or a shaper/skin catalogID names the shaper's
// catalogID; anything else (a ward's Noun, a perk, unknown ids, no content) names none.
func TestShaperOf(t *testing.T) {
	c := Fixture(t)
	for _, tc := range []struct {
		id   uint32
		want uint32
	}{
		{AssetID("ember.Noun"), 0x10000001}, {AssetID("ember_frost.Noun"), 0x10000001}, {AssetID("PC_Ember.Noun"), 0x10000001},
		{AssetID("stone.Noun"), 0x10000002}, {AssetID("ghost_mist.Noun"), 0x10000003}, {0x10000004, 0x10000001},
		{0x10000002, 0x10000002}, {AssetID("lantern.Noun"), 0}, {0x10000006, 0}, {AssetID("nobody.Noun"), 0}, {0, 0},
	} {
		got, ok := c.ShaperOf(tc.id)
		if got != tc.want || ok != (tc.want != 0) {
			t.Errorf("ShaperOf(%#x) = %#x, %v; want %#x", tc.id, got, ok, tc.want)
		}
	}
	var none *Content
	if _, ok := none.ShaperOf(AssetID("ember.Noun")); ok {
		t.Error("ShaperOf without content")
	}
}

func TestBuildFromDump(t *testing.T) {
	c := Fixture(t)
	want := []Item{
		{CatalogID: 0x10000001, Name: "ember", Type: "shaper", ItemType: ItemShaper, Status: "none", Asset: "ember.Noun", Title: "Ember", MaxOwnable: 1, Shaper: "PC_Ember"},
		{CatalogID: 0x10000002, Name: "stone", Type: "shaper", ItemType: ItemShaper, Status: "none", Asset: "stone.Noun", MaxOwnable: 1, Shaper: "PC_Stone"},
		{CatalogID: 0x10000003, Name: "ghost", Type: "shaper", ItemType: ItemShaper, Status: "none", Asset: "ghost.Noun", MaxOwnable: 1, Shaper: "PC_Ghost", TestingOnly: true},
		{CatalogID: 0x10000004, Name: "ember_frost", Type: "shaper skin", ItemType: ItemShaperSkin, Status: "none", Asset: "ember_frost.Noun", Title: "Frost Ember", MaxOwnable: 1, Shaper: "PC_Ember"},
		{CatalogID: 0x10000005, Name: "ghost_mist", Type: "shaper skin", ItemType: ItemShaperSkin, Status: "none", Asset: "ghost_mist.Noun", MaxOwnable: 1, Shaper: "PC_Ghost", TestingOnly: true},
		{CatalogID: 0x10000006, Name: "Spark", Type: "perk", ItemType: ItemPerkShape, Status: "none", Asset: "Spark.PerkShape", Title: "Spark", MaxOwnable: 2},
		{CatalogID: 0x10000007, Name: "RedGem", Type: "gem", ItemType: ItemPerkGem, Status: "none", Asset: "RedGem.Gem", MaxOwnable: 3},
		{CatalogID: 0x10000008, Name: "lantern", Type: "ward", ItemType: ItemWardSkin, Status: "alwaysOwned", Asset: "lantern.Noun", MaxOwnable: 1},
		{CatalogID: 0x10000009, Name: "Herald", Type: "announcer pack", ItemType: ItemAnnouncerPack, Status: "alwaysOwned", Asset: "Herald.AnnouncerPack", MaxOwnable: 1},
		{CatalogID: 0x1000000A, Name: "Founder", Type: "avatar", ItemType: ItemPlayerAvatar, Status: "hide", Asset: "Founder.AvatarIcon", MaxOwnable: 1},
		{CatalogID: 0x1000000B, Name: "Page", Type: "loadout", ItemType: ItemLoadoutPage, Status: "hide", Asset: "Page.LoadoutPage", MaxOwnable: 1},
		{CatalogID: 0x1000000C, Name: "Trinket", Type: "demo loot", Status: "none", Asset: "Trinket.DemoModeLoot", MaxOwnable: 1},
		{CatalogID: 0x1000000D, Name: "VoteA", Type: "living lore vote", Status: "none", Asset: "VoteA.LivingLoreVote", MaxOwnable: 1},
	}
	if !reflect.DeepEqual(c.Items, want) {
		t.Errorf("items:\n got %+v\nwant %+v", c.Items, want)
	}
	shapers := []Shaper{
		{Name: "PC_Ember", CatalogID: 0x10000001, Title: "Ember", Archetype: "mage", SubArchetype: "burst mage",
			Abilities: []string{"ability_Ember_Basic", "ability_Ember_Q", "ability_Ember_W", "ability_Ember_E", "ability_Ember_R", "modifier_Ember_Passive"}},
		{Name: "PC_Ghost", CatalogID: 0x10000003, Archetype: "mage", SubArchetype: "burst mage", TestingOnly: true, Abilities: []string{"ability_Ghost_Basic"}},
		{Name: "PC_Stone", CatalogID: 0x10000002, Archetype: "tank", SubArchetype: "initiator", Abilities: []string{"ability_Stone_Basic", "ability_Stone_Q"}},
	}
	if !reflect.DeepEqual(c.Shapers, shapers) {
		t.Errorf("shapers:\n got %+v\nwant %+v", c.Shapers, shapers)
	}
	perks := []Perk{
		{Name: "BlueGem", Kind: "gem", MaxOwnable: 16, Category: "blue",
			Attributes: []PerkAttribute{{ID: "Haste", Op: "PercentChange", Value: 0.007}}},
		{Name: "Lonely", Kind: "shape", MaxOwnable: 2, Category: "utility"},
		{Name: "RedGem", Kind: "gem", CatalogID: 0x10000007, MaxOwnable: 3, Category: "red",
			Attributes: []PerkAttribute{{ID: "Power", Op: "Add", Value: 0.4}}},
		{Name: "Spark", Kind: "shape", CatalogID: 0x10000006, MaxOwnable: 2, Category: "offense", Sockets: []string{"red", "any"},
			Attributes: []PerkAttribute{{ID: "MaxHealth", Op: "Add", Value: 50}, {ID: "Power", Op: "PercentChange", Value: 0.05}},
			Modifiers:  []string{"modifier_MediumPerk_BushSpeed"}},
	}
	if !reflect.DeepEqual(c.Perks, perks) {
		t.Errorf("perks:\n got %+v\nwant %+v", c.Perks, perks)
	}
	if !c.HasPerkStats() {
		t.Error("HasPerkStats false")
	}
	if want := []MatchItem{{Name: "Basic_Blade", Title: "Blade", Category: "Stat", Tier: "Basic", Cost: 300}}; !reflect.DeepEqual(c.MatchItems, want) {
		t.Errorf("match items: %+v", c.MatchItems)
	}
	shiny := Drop{Type: "IndependentEvents", Chance: 1, Rarity: "Shiny"}
	bags := []LootBag{{Name: "Copper", Title: "Common", RequiredBagScore: 25, ID: AssetID("Copper.LootBag")},
		{Name: "Silverish", RequiredBagScore: 400, ID: AssetID("Silverish.LootBag"),
			Drop: &Drop{Type: "IndependentEvents", Chance: 1, Drops: []Drop{shiny, shiny}}}}
	if !reflect.DeepEqual(c.LootBags, bags) {
		t.Errorf("loot bags:\n got %+v\nwant %+v", c.LootBags, bags)
	}
	rarities := []Rarity{{Name: "Dull", Score: 50, Pools: []Pool{{"16_MAX_SHAPES", 1}}},
		{Name: "Shiny", Score: 100, Pools: []Pool{{"2_MAX_SHAPES", 0.75}, {"GEMS", 0.25}}}}
	if !reflect.DeepEqual(c.Rarities, rarities) {
		t.Errorf("rarities: %+v", c.Rarities)
	}
	if want := (LootTuning{FTUEBag: "Silverish", DefaultBag: "Copper", FirstWinPoints: 100, WonGamePoints: 100}); c.LootTuning != want {
		t.Errorf("loot tuning: %+v", c.LootTuning)
	}
	if !reflect.DeepEqual(c.FTUELoot, []uint32{0x10000006, 0x10000007}) {
		t.Errorf("FTUE loot: %v", c.FTUELoot)
	}
	if c.Format != Format || c.Old() {
		t.Errorf("format %q", c.Format)
	}
	if p, ok := c.Perk(0x10000007); !ok || p.Name != "RedGem" || p.Kind != KindGem {
		t.Errorf("Perk lookup: %+v %v", p, ok)
	}
	if b, ok := c.Bag("silverish"); !ok || b.RequiredBagScore != 400 {
		t.Errorf("Bag lookup: %+v %v", b, ok)
	}
	if it, ok := c.Item(0x10000004); !ok || it.Name != "ember_frost" {
		t.Errorf("Item lookup: %+v %v", it, ok)
	}
	if _, ok := c.Item(42); ok {
		t.Errorf("Item found an unknown id")
	}
}

func TestWriteLoadRoundTrip(t *testing.T) {
	c := Fixture(t)
	path := filepath.Join(t.TempDir(), "sub", "content.json")
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d.byID, c.byID = nil, nil
	if !reflect.DeepEqual(c, d) {
		t.Errorf("round trip differs:\n got %+v\nwant %+v", d, c)
	}
}

// The loot bags' asset ids are FNV-1 of the lower-cased "name.LootBag", as the client's AssetCatalog
// computes them; build 854's ids from MATCH_FLOW.md (postgame.py's table).
func TestAssetID(t *testing.T) {
	for name, want := range map[string]uint32{"Bronze.LootBag": 0x4F079E93, "Silver.LootBag": 0x6401D184,
		"Gold.LootBag": 0xC308B989, "Platinum.LootBag": 0x103EBDF7, "Diamond.LootBag": 0xC0FEE8B9} {
		if got := AssetID(name); got != want {
			t.Errorf("AssetID(%q) = %#x, want %#x", name, got, want)
		}
	}
}

// A content file of B7a's format still loads; Old reports it (no sockets, drops or loot tuning).
func TestLoadB7aFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "content.json")
	os.WriteFile(path, []byte(`{"format": "dawngate-content-1", "items": [{"catalogID": 1, "name": "a"}]}`), 0o644)
	c, err := Load(path)
	if err != nil || !c.Old() {
		t.Errorf("B7a format: %v, old %v", err, c != nil && c.Old())
	}
}

func TestLoadRejects(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"format":    `{"format": "something-else", "items": []}`,
		"zero":      `{"format": "dawngate-content-1", "items": [{"catalogID": 0, "name": "a"}]}`,
		"duplicate": `{"format": "dawngate-content-1", "items": [{"catalogID": 1, "name": "a"}, {"catalogID": 1, "name": "b"}]}`,
		"json":      `{"format": `,
	} {
		path := filepath.Join(dir, name+".json")
		os.WriteFile(path, []byte(body), 0o644)
		if _, err := Load(path); err == nil {
			t.Errorf("%s: Load accepted it", name)
		}
	}
	if _, err := Load(filepath.Join(dir, "missing.json")); !os.IsNotExist(err) {
		t.Errorf("missing file: %v", err)
	}
	if _, err := Build(dir); err == nil {
		t.Errorf("Build accepted a folder without CollectionItem.json")
	}
}

func TestGrant(t *testing.T) {
	c := Fixture(t)
	all, avatar, err := c.Grant(OwnAll)
	if err != nil {
		t.Fatal(err)
	}
	wantAll := map[uint32]uint32{0x10000001: 1, 0x10000002: 1, 0x10000004: 1, 0x10000006: 2, 0x10000007: 3,
		0x10000008: 1, 0x10000009: 1, 0x1000000A: 1, 0x1000000B: 1}
	if !reflect.DeepEqual(all, wantAll) || avatar != 0x1000000A {
		t.Errorf("all: %v avatar %#x", all, avatar)
	}
	starter, avatar, err := c.Grant(OwnStarter)
	if err != nil {
		t.Fatal(err)
	}
	// always owned (lantern, Herald), the loadout page, the avatar, one shaper per archetype (ember for
	// mage: the testing-only ghost doesn't count; stone for tank)
	wantStarter := map[uint32]uint32{0x10000001: 1, 0x10000002: 1, 0x10000008: 1, 0x10000009: 1, 0x1000000A: 1, 0x1000000B: 1}
	if !reflect.DeepEqual(starter, wantStarter) || avatar != 0x1000000A {
		t.Errorf("starter: %v avatar %#x", starter, avatar)
	}
	if _, _, err := c.Grant("everything"); err == nil {
		t.Errorf("unknown policy accepted")
	}
	var none *Content
	if items, avatar, err := none.Grant(OwnAll); err != nil || len(items) != 0 || avatar != 0 || !none.Empty() {
		t.Errorf("no content: %v %d %v", items, avatar, err)
	}
}

// D3-perks: the AssetData enums of a perk attribute (docs/data/assetdata_types.txt: 0x012464B8, 72 values; 0x01419C70).
func TestPerkAttributeEnums(t *testing.T) {
	for _, c := range []struct {
		name string
		want int
	}{{"Power", 0}, {"MaxHealth", 2}, {"PhysicalDamageReduction", 24}, {"Unit Pass Through", 45}, {"VimPer5", 69},
		{"HideHealthBar", 71}, {"maxhealth", 2}} {
		if got, ok := AttributeIndex(c.name); !ok || got != c.want {
			t.Errorf("AttributeIndex(%q) = %d, %v; want %d", c.name, got, ok, c.want)
		}
	}
	if _, ok := AttributeIndex("NumAttributes"); ok || len(attributeNames) != 72 {
		t.Errorf("NumAttributes known, or %d names", len(attributeNames))
	}
	if i, ok := OpIndex("PercentChange"); !ok || i != 2 || OpName(1) != "Add" || OpName(8) != "" || AttributeName(2) != "MaxHealth" {
		t.Errorf("ops: %d %v %q", i, ok, OpName(1))
	}
	var none *Content
	if none.HasPerkStats() || (&Content{Perks: []Perk{{Name: "x"}}}).HasPerkStats() {
		t.Error("HasPerkStats without stats")
	}
}
