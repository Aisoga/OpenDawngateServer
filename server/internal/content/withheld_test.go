package content

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Made-up catalogIDs for withheld items added to the fixture.
const (
	idTestVanguard = 0x2000_0001
	idTestColossus = 0x2000_0002
)

// withWithheld is the fixture plus two perk shapes the list withholds (one named in another case), both in the
// FTUE loot, run through index as Build and Load do.
func withWithheld(t *testing.T) *Content {
	t.Helper()
	c := Fixture(t)
	c.Items = append(c.Items,
		Item{CatalogID: idTestVanguard, Name: "vanguard", Type: "perk", ItemType: ItemPerkShape, Status: StatusNone,
			Asset: "vanguard.PerkShape", Title: "Spiritstone of the Vanguard", MaxOwnable: 1},
		Item{CatalogID: idTestColossus, Name: "Colossus", Type: "perk", ItemType: ItemPerkShape, Status: StatusAlwaysOwned,
			Asset: "Colossus.PerkShape", Title: "Spiritstone of the Colossus", MaxOwnable: 1})
	c.Perks = append(c.Perks,
		Perk{Name: "vanguard", Kind: KindShape, CatalogID: idTestVanguard, MaxOwnable: 1, Sockets: []string{"red"},
			Modifiers: []string{"modifier_PerkHandler_ProtectDistantAllies"}},
		Perk{Name: "Colossus", Kind: KindShape, CatalogID: idTestColossus, MaxOwnable: 1})
	c.FTUELoot = append(c.FTUELoot, idTestVanguard, idTestColossus)
	if err := c.index(); err != nil {
		t.Fatal(err)
	}
	return c
}

func byCatalogID(a, b Item) int { return cmp.Compare(a.CatalogID, b.CatalogID) }

func checkWithheld(t *testing.T, name string, c *Content) {
	t.Helper()
	for _, id := range []uint32{idTestVanguard, idTestColossus} {
		if !c.IsWithheld(id) {
			t.Errorf("%s: %#x not withheld", name, id)
		}
		if _, ok := c.Item(id); ok {
			t.Errorf("%s: Item(%#x) found", name, id)
		}
		if _, ok := c.Perk(id); ok {
			t.Errorf("%s: Perk(%#x) found", name, id)
		}
		for _, it := range c.Items {
			if it.CatalogID == id {
				t.Errorf("%s: %#x in Items", name, id)
			}
		}
		if slices.Contains(c.FTUELoot, id) {
			t.Errorf("%s: %#x in the FTUE loot", name, id)
		}
		for _, policy := range []string{OwnAll, OwnStarter} {
			items, _, err := c.Grant(policy)
			if err != nil {
				t.Fatal(err)
			}
			if items[id] != 0 {
				t.Errorf("%s: %s grants %#x", name, policy, id)
			}
		}
	}
	if len(c.Withheld) != 2 || c.Withheld[0].CatalogID != idTestVanguard || c.Withheld[1].CatalogID != idTestColossus {
		t.Errorf("%s: Withheld %+v", name, c.Withheld)
	}
	if !slices.IsSortedFunc(c.Items, byCatalogID) {
		t.Errorf("%s: Items out of order", name)
	}
}

// A withheld item leaves Items (and so every grant, offer and list) when content is built and when a file is loaded,
// also a file written before the list (the item still in items); one whose name leaves the list comes back.
func TestWithheldItems(t *testing.T) {
	fixture := Fixture(t)
	c := withWithheld(t)
	checkWithheld(t, "built", c)
	if len(c.Items) != len(fixture.Items) {
		t.Errorf("%d items, the fixture has %d", len(c.Items), len(fixture.Items))
	}
	if _, ok := c.Perk(fixture.Perks[0].CatalogID); fixture.Perks[0].CatalogID != 0 && !ok {
		t.Error("a fixture perk is no longer found")
	}

	// written and loaded again: still withheld, the file lists them under "withheld"
	dir := t.TempDir()
	path := filepath.Join(dir, "content.json")
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"withheld": [`) {
		t.Error("the file has no withheld list")
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	checkWithheld(t, "loaded", loaded)

	// a file from before the list: both still in items
	old := withWithheld(t)
	old.Items, old.Withheld = append(old.Items, old.Withheld...), nil
	old.FTUELoot = append(old.FTUELoot, idTestVanguard)
	if err := old.Write(path); err != nil {
		t.Fatal(err)
	}
	if loaded, err = Load(path); err != nil {
		t.Fatal(err)
	}
	checkWithheld(t, "old file", loaded)

	// a name that left the list: back in Items, in catalogID order
	saved := withheldItems
	t.Cleanup(func() { withheldItems = saved })
	withheldItems = []withheld{{Name: "vanguard"}}
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	if loaded, err = Load(path); err != nil {
		t.Fatal(err)
	}
	if it, ok := loaded.Item(idTestColossus); !ok || it.Name != "Colossus" || loaded.IsWithheld(idTestColossus) {
		t.Errorf("Colossus not back: %+v %v", it, ok)
	}
	if !loaded.IsWithheld(idTestVanguard) || len(loaded.Withheld) != 1 {
		t.Errorf("Withheld %+v", loaded.Withheld)
	}
	if !slices.IsSortedFunc(loaded.Items, byCatalogID) {
		t.Error("Items out of order")
	}

	// content without a withheld item is unchanged (the fixture: Build gives the same content as before)
	if fixture.Withheld != nil || len(fixture.withheld) != 0 {
		t.Errorf("fixture: withheld %+v", fixture.Withheld)
	}
}

// With DG_CONTENT_FILE naming a content file made from the game (dgserver --make-content; a file from before the list
// works too), exactly the list is withheld (six Spiritstones, eight skins, two wards), every entry has its reason, and the
// eleven older Spiritstones and the kept skins of the duplicate titles stay.
func TestWithheldRealContent(t *testing.T) {
	path := os.Getenv("DG_CONTENT_FILE")
	if path == "" {
		t.Skip("DG_CONTENT_FILE not set")
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var held []string
	stones, skins, wards := 0, 0, 0
	for _, it := range c.Withheld {
		held = append(held, it.Name)
		switch {
		case it.Type == "perk" && strings.HasPrefix(it.Title, "Spiritstone of the "):
			stones++
		case it.Type == "shaper skin":
			skins++
		case it.Type == "ward":
			wards++
		default:
			t.Errorf("withheld %+v", it)
		}
		if WithheldReason(it.Name) == "" {
			t.Errorf("%s: no reason", it.Name)
		}
	}
	want := WithheldNames()
	slices.Sort(held)
	slices.Sort(want)
	if !slices.Equal(held, want) || stones != 6 || skins != 8 || wards != 2 {
		t.Errorf("withheld %v (%d stones, %d skins, %d wards), want %v", held, stones, skins, wards, want)
	}
	stay := map[string]bool{"gunman_anzerani": true, "gunman_hounds": true, "golem_anzerani": true, "wardskin_south": true,
		"wardskin_wsmagma": true}
	for _, n := range []string{"Adventurer", "Berserker", "Bravado", "Brawler", "Bulwark", "Forcebubble", "Harrier", "Lurker",
		"Reaper", "Resonance", "Scavenger"} {
		stay[strings.ToLower(n)] = true
	}
	all, _, _ := c.Grant(OwnAll)
	for _, it := range c.Items {
		if stay[strings.ToLower(it.Name)] {
			delete(stay, strings.ToLower(it.Name))
			if all[it.CatalogID] == 0 {
				t.Errorf("%s not granted", it.Name)
			}
		}
	}
	if len(stay) != 0 {
		t.Errorf("missing kept items %v", stay)
	}
	t.Logf("%d items, withheld %v", len(c.Items), held)
}
