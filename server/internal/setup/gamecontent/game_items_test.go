package gamecontent

import (
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

// Build 854's item details: every passive gets its name (the ranked ones by their rank), the shop's filter titles
// resolve, 35 recommended builds each name a shaper, and every item's icon is a texture of UI_Icons.snap.
func TestItemDetails(t *testing.T) {
	dir := testgame.Dir(t)
	c, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	passives, cheat := 0, 0
	for _, it := range c.MatchItems {
		for _, p := range it.Passives {
			passives++
			if p.Title == "" {
				t.Errorf("%s: %s has no name", it.Name, p.Modifier)
			}
			if p.Text == "" || strings.ContainsAny(p.Text, "~<>\x00") || len(p.Parts) == 0 {
				t.Errorf("%s: %s's text %q", it.Name, p.Modifier, p.Text)
			}
		}
		if it.Cheat {
			cheat++
		}
	}
	if passives != 98 || cheat != 2 {
		t.Errorf("%d passives, %d cheat items", passives, cheat)
	}
	for name, want := range map[string]string{"Advanced_Integrity": "Toughness II", "Legendary_Order": "Toughness III",
		"Legendary_Fluidity": "Spirit Walk", "Advanced_Energy": "Shock"} {
		if it, _ := c.MatchItem(name); len(it.Passives) == 0 || it.Passives[0].Title != want {
			t.Errorf("%s: %+v, want %q", name, it.Passives, want)
		}
	}
	// ITEMSHOP: the shop's item set in its order (the in-match shop's Basic column: Life, Resilience, Will, Power,
	// Time, Hunger)
	var basics []string
	for _, n := range c.ItemShop {
		if strings.HasPrefix(n, "Basic_") {
			basics = append(basics, strings.TrimPrefix(n, "Basic_"))
		}
	}
	if len(c.ItemShop) != 106 || strings.Join(basics, ",") != "Life,Resilience,Will,Power,Time,Hunger" {
		t.Errorf("the shop's set: %d items, basics %v", len(c.ItemShop), basics)
	}
	// ITEMSHOP: the stat lines as the shop writes them (the pictures of the in-match shop: "+20 Haste", "+5% Movement
	// Speed", "+200 Health"), every stat of every item has one, and the passives' labels (Decay's two are Unique)
	lines := func(it content.MatchItem) []string {
		var out []string
		for _, s := range it.Stats {
			line := ""
			for _, p := range s.Parts {
				line += p.Text + "|" + p.Color + ";"
			}
			out = append(out, line)
		}
		return out
	}
	for name, want := range map[string][]string{
		"Advanced_Motion": {"+20|#5555FF; Haste|;", "+5%|#55D9FF; Movement Speed|;"},
		"Legendary_Decay": {"+200|#FF2929; Health|;", "+30|#55FF55; Power|;", "+10|#47CC82; Defense Penetration|;"},
	} {
		it, _ := c.MatchItem(name)
		if got := lines(it); strings.Join(got, "/") != strings.Join(want, "/") {
			t.Errorf("%s's stat lines %q, want %q", name, got, want)
		}
	}
	for _, it := range c.MatchItems {
		for _, s := range it.Stats {
			if len(s.Parts) == 0 {
				t.Errorf("%s: no line for %s", it.Name, s.ID)
			}
		}
	}
	// ITEMSHOP round 2: every spell's and consumable's active has its text (the spells' level values as
	// "330 (+30 per level)", the scripts' own tokens filled), the spells their cooldown
	for _, it := range c.MatchItems {
		if (it.Category == "Ability" || it.Category == "Consumable") && !it.Cheat &&
			(it.ActiveInfo == nil || it.ActiveInfo.Text == "" || strings.ContainsAny(it.ActiveInfo.Text, "~<>\x00")) {
			t.Errorf("%s: active %+v", it.Name, it.ActiveInfo)
		}
	}
	for name, want := range map[string]string{"Spell_Vanquish": "Instantly deals 330 (+30 per level) true damage",
		"Spell_Bastion": "absorbs 80 (+20 per level) damage and lasts 2 seconds", "Consumable_Potion_HealingPotion": "Heal for 150 Health over 15 seconds"} {
		if it, _ := c.MatchItem(name); it.ActiveInfo == nil || !strings.Contains(it.ActiveInfo.Text, want) {
			t.Errorf("%s: %+v, want %q", name, it.ActiveInfo, want)
		}
	}
	if it, _ := c.MatchItem("Spell_Vanquish"); it.ActiveInfo == nil || it.ActiveInfo.Cooldown != 75 {
		t.Errorf("Vanquish's cooldown: %+v", it.ActiveInfo)
	}
	if it, _ := c.MatchItem("Basic_Life"); len(it.Passives) != 1 || it.Passives[0].Title != "Consume I" || it.Passives[0].Kind != "stackable" {
		t.Errorf("Life's passive %+v, want Consume I, stackable (the shop's picture)", it.Passives)
	}
	if it, _ := c.MatchItem("Legendary_Decay"); len(it.Passives) != 2 || it.Passives[0].Kind != "unique" || it.Passives[1].Kind != "unique" {
		t.Errorf("Decay's passives %+v, want two unique ones", it.Passives)
	}
	// the numbers of a few texts (the scripts' constants through their TranslateToken; checked against the scripts):
	// Order's (1 - 0.8) * 100 in float32 at precision 1, Chaos's ability damage, ratio and cooldown (a field its
	// handler leaves nil), Strife's 0.25 at precision 2 and 0.995 → 0.5, and its "Current" lines left out
	for name, want := range map[string][]string{"Legendary_Order": {"20%"}, "Legendary_Chaos": {"75 (+10%", "4 nearby", "6 second"},
		"Legendary_Strife": {"3 Health and 0.25 Power", "by 0.5%"}} {
		it, _ := c.MatchItem(name)
		for _, w := range want {
			if len(it.Passives) == 0 || !strings.Contains(it.Passives[0].Text, w) || strings.Contains(it.Passives[0].Text, "Current") {
				t.Errorf("%s: %q lacks %q", name, it.Passives, w)
			}
		}
	}
	if motion, _ := c.MatchItem("Advanced_Motion"); len(motion.From) != 1 || motion.From[0] != (content.ItemStep{Item: "Basic_Time", Price: 480}) ||
		motion.Into[0] != (content.ItemStep{Item: "Legendary_Fluidity", Price: 1200}) {
		t.Errorf("Motion's steps %+v %+v", motion.From, motion.Into)
	}
	if len(c.ItemFilters) != 3 || c.ItemFilters[0].Title != "Offense" || c.ItemFilters[0].Tags[0].Title != "Power" {
		t.Errorf("filters %+v", c.ItemFilters)
	}
	if len(c.ItemBuilds) != 35 {
		t.Errorf("%d builds", len(c.ItemBuilds))
	}
	for _, b := range c.ItemBuilds {
		if b.Shaper == "" || len(b.Categories) == 0 {
			t.Errorf("build %+v", b)
		}
	}
	// every item's icon is a texture of UI_Icons.snap, which the front end has mounted: the Collection's cards show
	// it by its path (moviepatch.ItemList)
	data, err := DataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	arch, err := snap.Open(filepath.Join(data, UIIconsSnap))
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, e := range arch.Entries {
		have[iconKey(e.Name)] = true
	}
	n := 0
	for _, it := range c.MatchItems {
		if it.Icon == "" || !have[iconKey(it.Icon)] {
			t.Errorf("%s: icon %q isn't in %s", it.Name, it.Icon, UIIconsSnap)
		}
		n++
	}
	if n != 106 {
		t.Errorf("%d match items", n)
	}
}
