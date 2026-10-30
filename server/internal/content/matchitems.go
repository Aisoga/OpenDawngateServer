package content

// The match items' details for the item browser (content format 3): what an item builds from and into with each
// step's price, its stats, passives, shop tags and icon, the shop's filters and the recommended builds. The data is
// AssetData Item, ItemFilterSet and ItemBuild; the rules are the client's
// (Item_GetCost, Item_GetSellValue), named where they are used.

import (
	"encoding/json"
	"strings"
)

// ItemStep is one step of an item's tree: the other item and the gold the step costs. In MatchItem.From the price
// is what buying this item costs while holding that component; in MatchItem.Into what buying that upgrade costs
// while holding this item. The client's Item_GetCost charges an item's ItemCost minus the ItemCost of the owned
// item it upgrades (one reachable through UpgradesInto, directly or further down), so a step costs the difference
// of the two items' costs (never below 0).
type ItemStep struct {
	Item  string `json:"item"`
	Price int    `json:"price"`
}

// ItemStat is one of an item's stats (Item.Attribute: the attribute by name, as perks' are, and its value).
type ItemStat struct {
	ID    string  `json:"id"`
	Value float32 `json:"value"`
	// Parts is the stat's line as the shop shows it ("+20 Haste", the value in the attribute's colour), from the
	// game's Locale by the game-folder build; empty from a dump or for an attribute the build has no line for.
	Parts []TextPart `json:"parts,omitempty"`
}

// ItemPassive is one of an item's passives (Item.Passive): the modifier's script name and its rank. Title is the
// passive's English name from its script's localizedName (filled by the game-folder build, which reads the scripts;
// "" from an AssetData dump).
type ItemPassive struct {
	Modifier string `json:"modifier"`
	Rank     int    `json:"rank,omitempty"`
	Title    string `json:"title,omitempty"`
	// Kind is the shop's label for it by its script's activation type: "unique" (Unique Passive), "stackable"
	// (Stackable Passive) or "" (Passive); filled by the game-folder build.
	Kind string `json:"kind,omitempty"`

	// Text is the passive's description at its rank with its numbers filled in, as plain text (lines split by
	// "\n"), and Parts the same text in runs with the colour the game gives them ("#55FF55"; "" for plain). Both
	// come from the game-folder build (its scripts and the Locale); the name the game's text starts with is left
	// out (Title holds it), and so are the lines that only make sense in a match ("Current Bonus Health: …").
	Text  string     `json:"text,omitempty"`
	Parts []TextPart `json:"parts,omitempty"`
}

// TextPart is a run of a passive's text: its characters and their colour ("#rrggbb", or "" for the plain colour).
type TextPart struct {
	Text  string `json:"t"`
	Color string `json:"c,omitempty"`
}

// ItemBuild is one recommended build (AssetData ItemBuild "RecommendedItemBuild_<class>", the in-game shop's
// Recommended panel): the shaper's PlayerClass (by the name's suffix; "" when none matches) and the build's
// categories in order.
type ItemBuild struct {
	Name       string              `json:"name"`
	Shaper     string              `json:"shaper,omitempty"`
	Categories []ItemBuildCategory `json:"categories"`
}

// ItemBuildCategory is one category of a recommended build ("Starting Items", "Core Items", ...): its title and its
// items by name, in order.
type ItemBuildCategory struct {
	Title string   `json:"title,omitempty"`
	Items []string `json:"items"`
}

// ItemFilter is one group of the shop's filters (ItemFilterSet ItemShopFilters: Offense, Utility, Defense) and its
// tags in order. Key is the title's Locale reference: the shop's are "^ItemShop!0x…", which the dump doesn't
// resolve, so the game-folder build fills Title from it.
type ItemFilter struct {
	Name  string          `json:"name"`
	Title string          `json:"title,omitempty"`
	Key   string          `json:"key,omitempty"`
	Tags  []ItemFilterTag `json:"tags"`
}

// ItemFilterTag is one tag of a filter group: an attribute ("Power", "MaxHealth", ...) or a custom tag ("Aura",
// "DebuffingEffect", ...). An item shows under it when its stats or its tags hold it [inferred from the client's
// per-item attribute bitset, Item +0xB8, which joins Attribute and attributeTag].
type ItemFilterTag struct {
	Tag   string `json:"tag"`
	Title string `json:"title,omitempty"`
	Key   string `json:"key,omitempty"`
}

// customTagNames are the custom tag enum's values 72-82 (they continue the attribute enum),
// as Item.customTag and the filters' custom tags use them.
var customTagNames = [...]string{"Consumable", "Aura", "OffensiveEffect", "DefensiveEffect", "DebuffingEffect",
	"DisableReduction", "Carry", "Caster", "Bruiser", "Tank", "Support"}

const firstCustomTag = 72

// tagName is a tag as the dump writes it (the enum's name, or its value when the dump's enum has no name for it).
func tagName(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n int
	if json.Unmarshal(raw, &n) != nil {
		return ""
	}
	if n >= firstCustomTag && n < firstCustomTag+len(customTagNames) {
		return customTagNames[n-firstCustomTag]
	}
	if name := AttributeName(n); name != "" {
		return name
	}
	return ""
}

// HasItemDetails reports whether the content holds the match items' details (a file of format 3 or later made
// from AssetData with the Item details): an item with a group, a sell value or a step.
func (c *Content) HasItemDetails() bool {
	if c == nil {
		return false
	}
	for _, it := range c.MatchItems {
		if it.Group != "" || it.SellValue != 0 || len(it.From)+len(it.Into) > 0 {
			return true
		}
	}
	return false
}

// MatchItem returns the match item of this name (case-insensitive).
func (c *Content) MatchItem(name string) (MatchItem, bool) {
	if c != nil {
		for _, it := range c.MatchItems {
			if strings.EqualFold(it.Name, name) {
				return it, true
			}
		}
	}
	return MatchItem{}, false
}

// The dump's members of the item classes.
type dumpItemRef struct {
	Asset string `json:"asset"`
}

type dumpItemBuild struct {
	Category []struct {
		Item []dumpItemRef `json:"item"`
		Name locKey        `json:"name"`
	} `json:"category"`
}

type dumpFilterTag struct {
	Key locKey `json:"key"`
	Tag string `json:"tag"`
}

type dumpItemFilterSet struct {
	Group []struct {
		Name         string          `json:"name"`
		LocaleKey    locKey          `json:"localeKey"`
		AttributeTag []dumpFilterTag `json:"attributeTag"`
		CustomTag    []dumpFilterTag `json:"customTag"`
	} `json:"group"`
}

// matchItemDetails fills each match item's details (format 3) from its dump entry; costs are the items' own.
func matchItemDetails(items []MatchItem, dump map[string]dumpMatchItem) {
	cost := map[string]int{}
	for _, it := range items {
		cost[it.Name] = it.Cost
	}
	step := func(ref string, price func(other int) int) ItemStep {
		name := assetName(ref)
		return ItemStep{Item: name, Price: max(price(cost[name]), 0)}
	}
	for i := range items {
		it, d := &items[i], dump[items[i].Name]
		it.Group = d.AbilityItemCategory
		it.SellValue = int(float64(it.Cost) * d.SellPercent) // Item_GetSellValue 0x008FCBF0: int(ItemCost × SellPercent)
		it.StackCount = d.StackCount
		it.Consumable = d.Consumable
		it.ConsumeOnFull = d.ConsumeOnFullInventory
		it.Cheat = d.CheatItem
		it.Icon = d.IconLarge
		if it.Icon == "" {
			it.Icon = d.IconSmall
		}
		it.Active = d.ActiveAbility
		if it.Active == "" {
			it.Active = d.ActiveModifier
		}
		for _, f := range d.UpgradesFrom {
			it.From = append(it.From, step(f.Asset, func(other int) int { return it.Cost - other }))
		}
		for _, f := range d.UpgradesInto {
			it.Into = append(it.Into, step(f.Asset, func(other int) int { return other - it.Cost }))
		}
		for _, a := range d.Attribute {
			if a.ID != "" {
				it.Stats = append(it.Stats, ItemStat{ID: a.ID, Value: a.Value})
			}
		}
		for _, p := range d.Passive {
			if p.PassiveModifier != "" {
				it.Passives = append(it.Passives, ItemPassive{Modifier: p.PassiveModifier, Rank: p.PassiveModifierRank})
			}
		}
		for _, raw := range append(append([]json.RawMessage{}, d.AttributeTag...), d.CustomTag...) {
			if t := tagName(raw); t != "" {
				it.Tags = append(it.Tags, t)
			}
		}
	}
}

// itemBuilds turns the ItemBuild assets into recommended builds, ordered by name; a build's shaper is the
// PlayerClass "PC_<suffix>" (case-insensitive) of its name "RecommendedItemBuild_<suffix>".
func itemBuilds(builds map[string]dumpItemBuild, classes map[string]dumpPlayerClass) []ItemBuild {
	var out []ItemBuild
	for _, name := range sortedNames(builds) {
		b := ItemBuild{Name: name, Categories: []ItemBuildCategory{}}
		if _, suffix, ok := strings.Cut(name, "_"); ok {
			for pc := range classes {
				if strings.EqualFold(pc, "PC_"+suffix) {
					b.Shaper = pc
				}
			}
		}
		for _, cat := range builds[name].Category {
			bc := ItemBuildCategory{Title: cat.Name.Text, Items: []string{}}
			for _, r := range cat.Item {
				if r.Asset != "" {
					bc.Items = append(bc.Items, assetName(r.Asset))
				}
			}
			b.Categories = append(b.Categories, bc)
		}
		out = append(out, b)
	}
	return out
}

type dumpItemSet struct {
	Item []dumpItemRef `json:"item"`
}

// itemShop is the shop's item set (ActiveItems_SM_Map_V5, else the first ItemSet) as item names in its order.
func itemShop(sets map[string]dumpItemSet) []string {
	names := sortedNames(sets)
	if len(names) == 0 {
		return nil
	}
	set := sets[names[0]]
	if s, ok := sets["ActiveItems_SM_Map_V5"]; ok {
		set = s
	}
	var out []string
	for _, r := range set.Item {
		if r.Asset != "" {
			out = append(out, assetName(r.Asset))
		}
	}
	return out
}

// itemFilters turns ItemShopFilters (or the first ItemFilterSet) into the filter groups, in the asset's order.
func itemFilters(sets map[string]dumpItemFilterSet) []ItemFilter {
	names := sortedNames(sets)
	if len(names) == 0 {
		return nil
	}
	set := sets[names[0]]
	if s, ok := sets["ItemShopFilters"]; ok {
		set = s
	}
	var out []ItemFilter
	for _, g := range set.Group {
		f := ItemFilter{Name: g.Name, Title: g.LocaleKey.Text, Key: g.LocaleKey.Ref, Tags: []ItemFilterTag{}}
		for _, t := range append(append([]dumpFilterTag{}, g.AttributeTag...), g.CustomTag...) {
			if t.Tag != "" {
				f.Tags = append(f.Tags, ItemFilterTag{Tag: t.Tag, Title: t.Key.Text, Key: t.Key.Ref})
			}
		}
		out = append(out, f)
	}
	return out
}

// ResolveTitles fills the empty titles of the filters and passives: text(ref) gives a Locale reference's English
// text (the filters' "^ItemShop!0x…" keys), passive(p) a passive's name at its rank ("" when unknown). Either may be
// nil.
func (c *Content) ResolveTitles(text func(ref string) string, passive func(p ItemPassive) string) {
	if c == nil {
		return
	}
	if text != nil {
		for i := range c.ItemFilters {
			f := &c.ItemFilters[i]
			if f.Title == "" && f.Key != "" {
				f.Title = text(f.Key)
			}
			for j := range f.Tags {
				if t := &f.Tags[j]; t.Title == "" && t.Key != "" {
					t.Title = text(t.Key)
				}
			}
		}
	}
	if passive != nil {
		for i := range c.MatchItems {
			for j := range c.MatchItems[i].Passives {
				if p := &c.MatchItems[i].Passives[j]; p.Title == "" {
					p.Title = passive(*p)
				}
			}
		}
	}
}
