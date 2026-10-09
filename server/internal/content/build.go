package content

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Classes are the AssetData classes Build reads; CollectionItem and PlayerClass must exist.
var Classes = []string{"CollectionItem", "PlayerClass", "Noun", "PerkShape", "Shape", "Socket", "Gem", "Item",
	"LootBag", "LootBagRarity", "LootBagTuning"}

// A Source gives Build a class's instances: it decodes them into into (a *map[string]T) from JSON as
// re/scripts/assetdata.py dump writes it ({name: object}; with --locale, Locale keys are {"ref", "text"}
// objects). An error wrapping fs.ErrNotExist says the source has no such class. DumpDir reads a dump
// folder; server/internal/setup/gamecontent reads the game's AssetData.snap itself.
type Source func(class string, into any) error

// Build makes the content from an AssetData dump: the folder re/scripts/assetdata.py dump writes
// (<Class>.json per class, {"class", "count", "errors", "instances": {name: object}}).
func Build(dumpDir string) (*Content, error) { return BuildFrom(DumpDir(dumpDir)) }

// DumpDir is the Source of a dump folder: <dir>/<Class>.json, whose "class" must be the class.
func DumpDir(dir string) Source {
	return func(class string, into any) error {
		path := filepath.Join(dir, class+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("content: %w (run re/scripts/assetdata.py dump first, see docs/ASSETDATA.md)", err)
		}
		var doc struct {
			Class     string          `json:"class"`
			Instances json.RawMessage `json:"instances"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("content: %s: %v", path, err)
		}
		if doc.Class != class {
			return fmt.Errorf("content: %s holds class %q, want %q", path, doc.Class, class)
		}
		if err := json.Unmarshal(doc.Instances, into); err != nil {
			return fmt.Errorf("content: %s: %v", path, err)
		}
		return nil
	}
}

// BuildFrom makes the content from a Source's classes (Classes).
//
// Links followed (ASSETDATA.md *Meta classes*): a shaper's or skin's CollectionItem.parentAsset is a
// Noun whose playerClassData names its PlayerClass; a perk's parentAsset is a PerkShape whose shape
// names a Shape (maxOwnable); a gem's parentAsset is a Gem (maxOwnable).
func BuildFrom(src Source) (*Content, error) {
	var (
		items      map[string]dumpCollectionItem
		classes    map[string]dumpPlayerClass
		nouns      map[string]dumpNoun
		perkShapes map[string]dumpPerkShape
		shapes     map[string]dumpShape
		gems       map[string]dumpGem
		sockets    map[string]dumpSocket
		matchItems map[string]dumpMatchItem
		lootBags   map[string]dumpLootBag
		rarities   map[string]dumpLootBagRarity
		tuning     map[string]dumpLootBagTuning
	)
	for _, f := range []struct {
		class    string
		into     any
		required bool
	}{
		{"CollectionItem", &items, true}, {"PlayerClass", &classes, true}, {"Noun", &nouns, false},
		{"PerkShape", &perkShapes, false}, {"Shape", &shapes, false}, {"Socket", &sockets, false},
		{"Gem", &gems, false}, {"Item", &matchItems, false}, {"LootBag", &lootBags, false},
		{"LootBagRarity", &rarities, false}, {"LootBagTuning", &tuning, false},
	} {
		if err := src(f.class, f.into); err != nil && (f.required || !errors.Is(err, fs.ErrNotExist)) {
			return nil, err
		}
	}

	c := &Content{Format: Format, Generator: "dgserver --make-content (server/internal/content)"}

	shaperOfNoun := func(ref string) string {
		if n, ok := nouns[assetName(ref)]; ok && n.PlayerClassData != "" {
			return assetName(n.PlayerClassData)
		}
		return ""
	}
	shaperItem := map[string]uint32{}
	for _, name := range sortedNames(items) {
		ci := items[name]
		it := Item{CatalogID: ci.CatalogID, Name: name, Type: ci.Type, ItemType: itemTypes[ci.Type],
			Status: ci.SpecialStatus, Asset: ci.ParentAsset, Title: ci.LongName.Text, MaxOwnable: 1}
		switch ci.Type {
		case "shaper", "shaper skin":
			it.Shaper = shaperOfNoun(ci.ParentAsset)
			if pc, ok := classes[it.Shaper]; ok && pc.TestingOnly {
				it.TestingOnly = true
			}
			if ci.Type == "shaper" && it.Shaper != "" {
				shaperItem[it.Shaper] = it.CatalogID
			}
		case "perk":
			if ps, ok := perkShapes[assetName(ci.ParentAsset)]; ok {
				if sh, ok := shapes[assetName(ps.Shape)]; ok && sh.MaxOwnable > 0 {
					it.MaxOwnable = sh.MaxOwnable
				}
			}
		case "gem":
			if g, ok := gems[assetName(ci.ParentAsset)]; ok && g.MaxOwnable > 0 {
				it.MaxOwnable = g.MaxOwnable
			}
		}
		c.Items = append(c.Items, it)
	}
	sort.Slice(c.Items, func(i, j int) bool { return c.Items[i].CatalogID < c.Items[j].CatalogID })
	if err := c.index(); err != nil {
		return nil, err
	}

	for _, name := range sortedNames(classes) {
		pc := classes[name]
		s := Shaper{Name: name, CatalogID: shaperItem[name], Title: pc.NameLocaleKey.Text, Archetype: pc.CharacterArchetype,
			SubArchetype: pc.CharacterSubArchetype, TestingOnly: pc.TestingOnly}
		for _, a := range []string{pc.BasicAbility, pc.SpecialAbility1, pc.SpecialAbility2, pc.SpecialAbility3,
			pc.SpecialAbility4, pc.PassiveAbility} {
			if a != "" {
				s.Abilities = append(s.Abilities, a)
			}
		}
		c.Shapers = append(c.Shapers, s)
	}

	itemOf := func(ref string) uint32 {
		if ci, ok := items[assetName(ref)]; ok {
			return ci.CatalogID
		}
		return 0
	}
	for _, name := range sortedNames(perkShapes) {
		ps := perkShapes[name]
		p := Perk{Name: name, Kind: KindShape, CatalogID: itemOf(ps.CollectionItem), MaxOwnable: 1, Category: ps.SocketCategory}
		if sh, ok := shapes[assetName(ps.Shape)]; ok && sh.MaxOwnable > 0 {
			p.MaxOwnable = sh.MaxOwnable
		}
		for _, so := range ps.Socket { // the socket's colour; a socket asset the dump lacks counts as "any"
			typ := SocketAny
			if d, ok := sockets[assetName(so.Asset)]; ok && d.Type != "" {
				typ = d.Type
			}
			p.Sockets = append(p.Sockets, typ)
		}
		p.Attributes, p.Modifiers = perkStats(ps.Attribute, ps.Modifier)
		c.Perks = append(c.Perks, p)
	}
	for _, name := range sortedNames(gems) {
		g := gems[name]
		p := Perk{Name: name, Kind: KindGem, CatalogID: itemOf(g.CollectionItem), MaxOwnable: max(g.MaxOwnable, 1),
			Category: g.Type}
		p.Attributes, p.Modifiers = perkStats(g.Attribute, g.Modifier)
		c.Perks = append(c.Perks, p)
	}
	sort.SliceStable(c.Perks, func(i, j int) bool { return c.Perks[i].Name < c.Perks[j].Name })

	for _, name := range sortedNames(matchItems) {
		mi := matchItems[name]
		c.MatchItems = append(c.MatchItems, MatchItem{Name: name, Title: mi.Name.Text, Category: mi.Category, Tier: mi.Tier,
			Cost: mi.ItemCost})
	}
	for _, name := range sortedNames(lootBags) {
		lb := lootBags[name]
		c.LootBags = append(c.LootBags, LootBag{Name: name, Title: lb.TierName.Text, RequiredBagScore: lb.RequiredBagScore,
			ID: AssetID(name + ".LootBag"), Drop: lb.Drop.drop()})
	}
	sort.SliceStable(c.LootBags, func(i, j int) bool { return c.LootBags[i].RequiredBagScore < c.LootBags[j].RequiredBagScore })
	for _, name := range sortedNames(rarities) {
		r := Rarity{Name: name, Score: rarities[name].RarityScore, Pools: []Pool{}}
		for _, d := range rarities[name].DropType {
			r.Pools = append(r.Pools, Pool{Name: d.Name, Chance: d.Chance})
		}
		c.Rarities = append(c.Rarities, r)
	}
	for _, name := range sortedNames(tuning) {
		t := tuning[name]
		c.FTUELoot = append(c.FTUELoot, t.FTUELoot...)
		c.LootTuning = LootTuning{FTUEBag: assetName(t.FTUEBagLoot), DefaultBag: assetName(t.FTUEBagDefault),
			FirstWinPoints: t.PointsFirstWinOfTheDay, WonGamePoints: t.PointsWonGame}
	}
	if c.FTUELoot == nil {
		c.FTUELoot = []uint32{}
	}
	return c, nil
}

// locKey is a Key member as the dump writes it: a plain string (the key's text, e.g. a Locale
// reference "alpha!0x0d0712d6" without --locale) or {"ref": ..., "text": ...} with --locale.
type locKey struct {
	Ref, Text string
}

func (k *locKey) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		k.Ref = s
		return nil
	}
	var o struct {
		Ref  string  `json:"ref"`
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return nil // null or an unexpected shape: no title
	}
	k.Ref = o.Ref
	if o.Text != nil {
		k.Text = *o.Text
	}
	return nil
}

// The dump's members (names from docs/data/assetdata_types.txt); only what Build uses.
type dumpCollectionItem struct {
	Type          string `json:"type"`
	SpecialStatus string `json:"specialStatus"`
	CatalogID     uint32 `json:"catalogID"`
	ParentAsset   string `json:"parentAsset"`
	LongName      locKey `json:"longName"`
}

type dumpPlayerClass struct {
	TestingOnly           bool   `json:"testingOnly"`
	NameLocaleKey         locKey `json:"nameLocaleKey"`
	BasicAbility          string `json:"basicAbility"`
	SpecialAbility1       string `json:"specialAbility1"`
	SpecialAbility2       string `json:"specialAbility2"`
	SpecialAbility3       string `json:"specialAbility3"`
	SpecialAbility4       string `json:"specialAbility4"`
	PassiveAbility        string `json:"passiveAbility"`
	CharacterArchetype    string `json:"CharacterArchetype"`
	CharacterSubArchetype string `json:"CharacterSubArchetype"`
}

type dumpNoun struct {
	PlayerClassData string `json:"playerClassData"`
}

type dumpPerkShape struct {
	CollectionItem string `json:"collectionItem"`
	Shape          string `json:"shape"`
	SocketCategory string `json:"socketCategory"`
	Socket         []struct {
		Asset string `json:"asset"`
	} `json:"socket"`
	Attribute []PerkAttribute `json:"attribute"`
	Modifier  []dumpPerkMod   `json:"modifier"`
}

// dumpPerkMod is a cPerkModifierData: its id (a StringHash) as the dump writes it, the ability's name.
type dumpPerkMod struct {
	ID string `json:"id"`
}

// perkStats copies a perk's attributes and modifier names (D3-perks); entries without a name are left out.
func perkStats(attrs []PerkAttribute, mods []dumpPerkMod) ([]PerkAttribute, []string) {
	var a []PerkAttribute
	for _, at := range attrs {
		if at.ID != "" {
			a = append(a, at)
		}
	}
	var m []string
	for _, md := range mods {
		if md.ID != "" {
			m = append(m, md.ID)
		}
	}
	return a, m
}

type dumpSocket struct {
	Type string `json:"type"`
}

type dumpShape struct {
	MaxOwnable uint32 `json:"maxOwnable"`
}

type dumpGem struct {
	Type           string          `json:"type"`
	CollectionItem string          `json:"collectionItem"`
	MaxOwnable     uint32          `json:"maxOwnable"`
	Attribute      []PerkAttribute `json:"attribute"`
	Modifier       []dumpPerkMod   `json:"modifier"`
}

type dumpMatchItem struct {
	Category string `json:"Category"`
	Tier     string `json:"Tier"`
	ItemCost int    `json:"ItemCost"`
	Name     locKey `json:"Name"`
}

type dumpLootBag struct {
	RequiredBagScore int           `json:"RequiredBagScore"`
	TierName         locKey        `json:"TierName"`
	Drop             *dumpLootDrop `json:"Drop"`
}

// dumpLootDrop is a LootBagDrop: DropType, its children (Drop), a rarity reference and a chance.
type dumpLootDrop struct {
	DropType string         `json:"DropType"`
	Drop     []dumpLootDrop `json:"Drop"`
	Rarity   string         `json:"Rarity"`
	Chance   float64        `json:"Chance"`
}

func (d *dumpLootDrop) drop() *Drop {
	if d == nil {
		return nil
	}
	out := &Drop{Type: d.DropType, Chance: d.Chance, Rarity: assetName(d.Rarity)}
	for i := range d.Drop {
		out.Drops = append(out.Drops, *d.Drop[i].drop())
	}
	return out
}

type dumpLootBagRarity struct {
	DropType []struct {
		Name   string  `json:"Name"`
		Chance float64 `json:"Chance"`
	} `json:"DropType"`
	RarityScore float64 `json:"RarityScore"`
}

type dumpLootBagTuning struct {
	FTUELoot               []uint32 `json:"FTUELoot"`
	FTUEBagLoot            string   `json:"FTUEBag_Loot"`
	FTUEBagDefault         string   `json:"FTUEBag_Default"`
	PointsWonGame          int      `json:"Points_WonGame"`
	PointsFirstWinOfTheDay int      `json:"Points_FirstWinOfTheDay"`
}
