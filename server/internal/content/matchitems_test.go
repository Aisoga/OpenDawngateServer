package content

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The fixture's tree: Blade (300) -> Edge (900) -> Storm (2,400), Blade -> Guard (1,000). Each step costs the
// difference (Item_GetCost); the sell value is half the cost; tags come by name from both enums.
func TestMatchItemDetails(t *testing.T) {
	c := Fixture(t)
	blade, _ := c.MatchItem("basic_blade")
	want := MatchItem{Name: "Basic_Blade", Title: "Blade", Category: "Stat", Tier: "Basic", Cost: 300, Group: "Offense",
		SellValue: 150, StackCount: 1, Into: []ItemStep{{"Advanced_Edge", 600}, {"Advanced_Guard", 700}},
		Stats: []ItemStat{{ID: "Power", Value: 10}}, Icon: `UI\Icons\ShopIcons\textures\Basic_Blade.dds`}
	if !reflect.DeepEqual(blade, want) {
		t.Errorf("Basic_Blade:\n got %+v\nwant %+v", blade, want)
	}
	edge, _ := c.MatchItem("Advanced_Edge")
	if !reflect.DeepEqual(edge.From, []ItemStep{{"Basic_Blade", 600}}) || !reflect.DeepEqual(edge.Into, []ItemStep{{"Legendary_Storm", 1500}}) {
		t.Errorf("Edge steps: from %+v into %+v", edge.From, edge.Into)
	}
	if !reflect.DeepEqual(edge.Tags, []string{"DefensePenetration", "OffensiveEffect"}) {
		t.Errorf("Edge tags %v", edge.Tags)
	}
	storm, _ := c.MatchItem("Legendary_Storm")
	if !reflect.DeepEqual(storm.Passives, []ItemPassive{{Modifier: "modifier_Item_Test_Shock", Rank: 2}}) || storm.SellValue != 1200 {
		t.Errorf("Storm %+v", storm)
	}
	tonic, _ := c.MatchItem("Consumable_Tonic")
	if !tonic.Consumable || tonic.StackCount != 5 || tonic.Active != "modifier_Item_Tonic" || tonic.Cheat ||
		!reflect.DeepEqual(tonic.Tags, []string{"Consumable"}) {
		t.Errorf("Tonic %+v", tonic)
	}
	hop, _ := c.MatchItem("Spell_Hop")
	if hop.Active != "ability_Item_Hop" || hop.Cost != 0 || hop.Group != "Utility" {
		t.Errorf("Hop %+v", hop)
	}
	if gold, _ := c.MatchItem("Consumable_Debug_Gold"); !gold.Cheat || !gold.ConsumeOnFull {
		t.Errorf("debug gold %+v", gold)
	}
	if _, ok := c.MatchItem("nothing"); ok {
		t.Error("MatchItem found an unknown item")
	}
}

func TestItemFiltersAndBuilds(t *testing.T) {
	c := Fixture(t)
	filters := []ItemFilter{
		{Name: "Offense", Key: "^ItemShop!0x00000030", Tags: []ItemFilterTag{{Tag: "Power", Key: "^ItemShop!0x00000031"},
			{Tag: "DefensePenetration", Title: "Pierce", Key: "^ItemShop!0x00000032"}}},
		{Name: "Utility", Key: "^ItemShop!0x00000033", Tags: []ItemFilterTag{{Tag: "MovementSpeedPercentIncrease", Key: "^ItemShop!0x00000034"},
			{Tag: "Aura", Key: "^ItemShop!0x00000035"}}},
	}
	if !reflect.DeepEqual(c.ItemFilters, filters) {
		t.Errorf("filters:\n got %+v\nwant %+v", c.ItemFilters, filters)
	}
	builds := []ItemBuild{
		{Name: "RecommendedItemBuild_Ember", Shaper: "PC_Ember", Categories: []ItemBuildCategory{
			{Title: "Starting Items", Items: []string{"Basic_Blade", "Consumable_Tonic"}},
			{Title: "Core Items", Items: []string{"Legendary_Storm"}},
			{Items: []string{"Advanced_Guard", "Missing_Item"}}}},
		{Name: "RecommendedItemBuild_stone", Shaper: "PC_Stone", Categories: []ItemBuildCategory{
			{Title: "Core Items", Items: []string{"Advanced_Guard"}}}},
	}
	if !reflect.DeepEqual(c.ItemBuilds, builds) {
		t.Errorf("builds:\n got %+v\nwant %+v", c.ItemBuilds, builds)
	}

	c.ResolveTitles(func(ref string) string { return "T(" + ref + ")" }, func(p ItemPassive) string {
		if p.Modifier == "modifier_Item_Test_Shock" && p.Rank == 2 {
			return "Shock"
		}
		return ""
	})
	if f := c.ItemFilters[0]; f.Title != "T(^ItemShop!0x00000030)" || f.Tags[0].Title != "T(^ItemShop!0x00000031)" || f.Tags[1].Title != "Pierce" {
		t.Errorf("resolved filter %+v", f)
	}
	if s, _ := c.MatchItem("Legendary_Storm"); s.Passives[0].Title != "Shock" {
		t.Errorf("resolved passive %+v", s.Passives)
	}
	if g, _ := c.MatchItem("Advanced_Guard"); g.Passives[0].Title != "" {
		t.Errorf("unknown passive got a title %+v", g.Passives)
	}
	var none *Content
	none.ResolveTitles(nil, nil)
	if none.HasItemDetails() {
		t.Error("HasItemDetails without content")
	}
}

// A file of the previous format still loads, without the details.
func TestLoadB7cFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "content.json")
	os.WriteFile(path, []byte(`{"format": "dawngate-content-2", "items": [{"catalogID": 1, "name": "a"}],
		"matchItems": [{"name": "Basic_Time", "cost": 420}]}`), 0o644)
	c, err := Load(path)
	if err != nil || c.Old() || c.HasItemDetails() || len(c.MatchItems) != 1 {
		t.Errorf("B7c format: %v, %+v", err, c)
	}
}
