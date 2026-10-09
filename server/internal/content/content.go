// Package content is the server's view of the game's catalog (roadmap B7a): which collection items
// exist (shapers, skins, perk shapes, gems, wards, announcer packs, avatars, loadout pages) with
// their catalogIDs, the shapers (PlayerClass), the perks, the in-match shop items and the loot bags.
//
// The data comes from the user's own copy of the game: re/scripts/assetdata.py dump writes the
// client's AssetData as JSON (docs/ASSETDATA.md), and Build turns that dump into one content file
// (dgserver --make-content <dump dir>, default server\data\content.json). The content file is
// copyrighted game data: it is generated at setup, lives in the gitignored server data folder and is
// never committed. Tests use the synthetic dump in server/testdata/assetdata.
//
// Items are identified by their CollectionItem catalogID (uint32, AssetData CollectionItem +0x08),
// the id the client's metagame and ecommerce code keys items by (ASSETDATA.md, "How the client
// identifies an item on the wire").
package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Item types of the BGEcommerce component, from the client's enum name table at .rdata 0x012F2FE0
// ({name, value} pairs next to the BGEcommerce class names; B7a): INVALID 0, SHAPER 1, SHAPER_SKIN 2,
// PERK_GEM 3, PERK_SHAPE 4, WARD_SKIN 5, ANNOUNCER_PACK 6, PLAYER_AVATAR 7, KILL_BANNER 8,
// LOADOUT_PAGE 9. GameItem.TYPE is this enum (the client files getGameItems by it: 5 = the wards of
// shaper select, 9 = the perk-page limit). We send it in BGAInventoryItem.TYPE too, which the client
// never reads (R11, docs/ACCOUNT_CONTENT.md; the loadout manager's m_componentType is its own 1 perk /
// 2 gem, taken from AssetData).
const (
	ItemInvalid       = 0
	ItemShaper        = 1
	ItemShaperSkin    = 2
	ItemPerkGem       = 3
	ItemPerkShape     = 4
	ItemWardSkin      = 5
	ItemAnnouncerPack = 6
	ItemPlayerAvatar  = 7
	ItemKillBanner    = 8
	ItemLoadoutPage   = 9
)

// itemTypes maps AssetData CollectionItem.type (the CollectionType enum: shaper 0, shaper skin 1,
// perk 2, gem 3, loadout 4, ward 5, announcer pack 6, avatar 7, demo loot 8, living lore vote 9) to
// the BGEcommerce item type. Demo loot and living lore votes are not inventory items (type 0).
var itemTypes = map[string]int{
	"shaper": ItemShaper, "shaper skin": ItemShaperSkin, "perk": ItemPerkShape, "gem": ItemPerkGem,
	"loadout": ItemLoadoutPage, "ward": ItemWardSkin, "announcer pack": ItemAnnouncerPack,
	"avatar": ItemPlayerAvatar,
}

// Special statuses of a CollectionItem (AssetData enum specialStatus).
const (
	StatusNone          = "none"
	StatusAlwaysOwned   = "alwaysOwned"
	StatusHide          = "hide"
	StatusHideIfUnowned = "hideIfUnowned"
)

// Format names the content file's layout. Load accepts Format and FormatB7a; Build writes Format.
//
//   - FormatB7a (B7a): items, shapers, perks, match items, loot bags, the FTUE loot.
//   - Format (B7c): adds the perk shapes' sockets, the loot bags' asset ids and drop tables, the loot
//     rarities and the loot tuning. A B7a file still works: without sockets the server can't check
//     a gem's socket index, and without drop tables bags drop nothing (Old reports it).
const (
	Format    = "dawngate-content-2"
	FormatB7a = "dawngate-content-1"
)

// Content is the content file.
type Content struct {
	Format    string `json:"format"`
	Generator string `json:"generator"`
	// Items holds every CollectionItem, ordered by catalogID.
	Items []Item `json:"items"`
	// Shapers holds every PlayerClass, ordered by name.
	Shapers []Shaper `json:"shapers"`
	// Perks holds the PerkShapes and Gems, ordered by name (with or without a CollectionItem).
	Perks []Perk `json:"perks"`
	// MatchItems holds the in-match shop items (AssetData Item), ordered by name.
	MatchItems []MatchItem `json:"matchItems"`
	// LootBags holds the loot bag tiers (AssetData LootBag), ordered by required bag score.
	LootBags []LootBag `json:"lootBags"`
	// FTUELoot is LootBagTuning.FTUELoot: the catalogIDs of the first-time-user loot.
	FTUELoot []uint32 `json:"ftueLoot"`
	// Rarities holds the loot rarities (AssetData LootBagRarity), ordered by name (B7c).
	Rarities []Rarity `json:"rarities,omitempty"`
	// LootTuning is the rest of LootBagTuning (B7c).
	LootTuning LootTuning `json:"lootTuning"`

	byID map[uint32]int
}

// Item is one CollectionItem: something a player can own.
type Item struct {
	CatalogID uint32 `json:"catalogID"`
	Name      string `json:"name"`     // the CollectionItem's asset name ("alpha")
	Type      string `json:"type"`     // CollectionItem.type ("shaper", "shaper skin", "perk", ...)
	ItemType  int    `json:"itemType"` // BGEcommerce item type (ItemShaper, ...); 0 = not an inventory item
	Status    string `json:"status"`   // CollectionItem.specialStatus
	Asset     string `json:"asset"`    // parentAsset ("alpha.Noun", "Adventurer.PerkShape", ...)
	Title     string `json:"title,omitempty"`
	// MaxOwnable is how many the player can own: Shape.maxOwnable of a perk shape, Gem.maxOwnable of
	// a gem, 1 otherwise.
	MaxOwnable uint32 `json:"maxOwnable"`
	// Shaper is the PlayerClass of a shaper or shaper skin ("PC_Alpha").
	Shaper string `json:"shaper,omitempty"`
	// TestingOnly marks a shaper whose PlayerClass is testingOnly, and its skins.
	TestingOnly bool `json:"testingOnly,omitempty"`
}

// Shaper is one PlayerClass.
type Shaper struct {
	Name         string   `json:"name"`                // "PC_Alpha"
	CatalogID    uint32   `json:"catalogID,omitempty"` // its CollectionItem (via the Noun), 0 if none
	Title        string   `json:"title,omitempty"`
	Archetype    string   `json:"archetype,omitempty"`
	SubArchetype string   `json:"subArchetype,omitempty"`
	TestingOnly  bool     `json:"testingOnly,omitempty"`
	Abilities    []string `json:"abilities,omitempty"` // basic, special 1-4, passive (Lua names)
}

// Perk is one PerkShape or Gem.
type Perk struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"` // "shape" or "gem"
	CatalogID  uint32 `json:"catalogID,omitempty"`
	MaxOwnable uint32 `json:"maxOwnable"`
	// Category is a shape's socketCategory or a gem's colour (Gem.type: "red", "green", "blue").
	Category string `json:"category,omitempty"`
	// Sockets are a shape's sockets in PerkShape.socket order, each its Socket.type ("any", "red",
	// "green", "blue"). A perk page's gem names its socket by this index (BGAPerkPageGem.SIDX, R11).
	Sockets []string `json:"sockets,omitempty"`
	// Attributes and Modifiers are the perk's stats (PerkShape/Gem.attribute, cPerkAttributeData) and
	// passive modifiers (PerkShape/Gem.modifier, cPerkModifierData: an ability's Lua name), which the
	// client's LoadoutManager_ComputePageStats 0x0088C890 adds up into a perk page's stats (D3-perks). A
	// content file from before D3-perks has neither (HasPerkStats); the format name stayed, so older
	// servers still read a newer file.
	Attributes []PerkAttribute `json:"attributes,omitempty"`
	Modifiers  []string        `json:"modifiers,omitempty"`
}

// PerkAttribute is one stat of a perk shape or gem: AssetData cPerkAttributeData {id, op, value}, with
// the attribute (enum 0x012464B8) and the op (enum 0x01419C70) by name as the dump writes them.
type PerkAttribute struct {
	ID    string  `json:"id"` // "Power", "MaxHealth", ... (AttributeIndex)
	Op    string  `json:"op"` // "Add", "PercentChange", ... (OpIndex)
	Value float32 `json:"value"`
}

// attributeNames is the AssetData attribute enum 0x012464B8 by value (docs/data/assetdata_types.txt): the
// cPerkAttributeData id, the attribute byte of the pre-game loadout (C0 02) and the index of the
// creature's attribute (docs/INDEXES.md, index_attributes.txt; 0-71 are the stored attributes).
var attributeNames = [...]string{
	"Power", "Mastery", "MaxHealth", "MaxResourcePoints", "HealthRegen", "ResourcePointRegen",
	"ResourcePointPercentRegen", "DisableResourcePointRegen", "NonCombatSpeed", "MovementSpeedFlatDecrease",
	"MovementSpeedCap", "MovementSpeedPercentIncrease", "MovementSpeedPercentDecrease", "SlowEffectivenessReduction",
	"AttackSpeedDecrease", "AttackSpeedIncrease", "Haste", "SpellCooldownReduction", "AbilityCooldownReduction",
	"BasicRangeScale", "AbilityRangeScale", "DamageReduction", "BasicReduction", "AbilityReduction",
	"PhysicalDamageReduction", "MagicalDamageReduction", "ArmorReductionFlatDecrease", "ArmorReductionPercentDecrease",
	"MagicResistReductionFlatDecrease", "MagicResistReductionPercentDecrease", "DefensePenetration",
	"PercentDefensePenetration", "PercentBonusDefensePenetration", "PercentDamageDebuff", "PercentCriticalStrikeDamage",
	"FlatCriticalStrikeDamage", "PercentSpellOverloadDamage", "FlatSpellOverloadDamage", "MaxHealthIncrease",
	"HealingReduction", "HealIncrease", "Silence", "AutoAttackSilence", "Immobilized", "ImmobilizedNoRotate",
	"Unit Pass Through", "Immune", "StealthDetection", "Untargetable", "Unattackable", "Suppressed",
	"HighMobilityDisabled", "AutoAttackDisabled", "CrowdControlDurationDecrease", "ImmuneToDebuffs",
	"ImmuneToKnockback", "ImmuneToStunned", "ImmuneToFear", "ImmuneToSilence", "ImmuneToRooted", "ImmuneToSlow",
	"ImmuneToSuppression", "BehindDirectDamageIncrease", "AoeResistance", "LifeDrain", "BuffDuration", "BodyScale",
	"StructureDamageDeltIncrease", "StructureDamageTakenReduction", "VimPer5", "ExperiencePercentIncrease",
	"HideHealthBar",
}

// opNames is the AssetData perk attribute op enum 0x01419C70 by value.
var opNames = [...]string{"NoOp", "Add", "PercentChange", "Clamp", "AddBest", "PercentChangeBest", "MultiplyAdd",
	"DiminishingReturns"}

// AttributeIndex returns the attribute enum value of an attribute name (case-insensitive).
func AttributeIndex(name string) (int, bool) { return enumIndex(attributeNames[:], name) }

// OpIndex returns the op enum value of an op name (case-insensitive).
func OpIndex(name string) (int, bool) { return enumIndex(opNames[:], name) }

// AttributeName and OpName are the names of the enum values ("" for an unknown one).
func AttributeName(i int) string { return enumName(attributeNames[:], i) }
func OpName(i int) string        { return enumName(opNames[:], i) }

func enumIndex(names []string, name string) (int, bool) {
	for i, n := range names {
		if strings.EqualFold(n, name) {
			return i, true
		}
	}
	return 0, false
}

func enumName(names []string, i int) string {
	if i < 0 || i >= len(names) {
		return ""
	}
	return names[i]
}

// HasPerkStats reports whether the content knows the perks' stats (a file made since D3-perks): a perk with
// attributes or modifiers.
func (c *Content) HasPerkStats() bool {
	if c == nil {
		return false
	}
	for _, p := range c.Perks {
		if len(p.Attributes) > 0 || len(p.Modifiers) > 0 {
			return true
		}
	}
	return false
}

// Kinds of Perk, and the socket type that takes a gem of any colour (Socket.type "any" = 4; the client's
// LoadoutManager_IsPerkComplete 0x00888AE0, R11).
const (
	KindShape = "shape"
	KindGem   = "gem"
	SocketAny = "any"
)

// MatchItem is one in-match shop item (AssetData Item).
type MatchItem struct {
	Name     string `json:"name"`
	Title    string `json:"title,omitempty"`
	Category string `json:"category,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Cost     int    `json:"cost"`
}

// LootBag is one loot bag tier.
type LootBag struct {
	Name             string `json:"name"`
	Title            string `json:"title,omitempty"`
	RequiredBagScore int    `json:"requiredBagScore"`
	// ID is the bag's AssetCatalog id, AssetID(Name + ".LootBag"): what the post-game's C1 04 names the
	// bag by (LootBag_GetNounAndEffect 0x00893240; MATCH_FLOW.md *Match end and post-game (D8-static)*).
	ID uint32 `json:"id,omitempty"`
	// Drop is LootBag.Drop, the bag's drop table (nil: the bag drops nothing).
	Drop *Drop `json:"drop,omitempty"`
}

// Drop is one node of a loot bag's drop table (AssetData LootBag.Drop, LootBagDrop): Type
// "IndependentEvents" (each child happens with its own Chance) or "WeightedRandom" (one child, picked
// by Chance as weight); a node with a Rarity drops one item of that rarity.
type Drop struct {
	Type   string  `json:"type"`
	Chance float64 `json:"chance"`
	Rarity string  `json:"rarity,omitempty"` // a Rarity's name
	Drops  []Drop  `json:"drops,omitempty"`
}

// Rarity is one LootBagRarity: Pools are its DropType entries, each a pool name and its weight. The
// pool names are "GEMS" and "<n>_MAX_SHAPES"; the n are exactly the maxOwnable values of build 854's
// perk shapes (1, 2, 4, 5, 8, 10, 16), so a pool is read as "the perk shapes one can own n of"
// [inferred, B7c].
type Rarity struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"` // RarityScore
	Pools []Pool  `json:"pools"`
}

// Pool is one weighted pool of a Rarity.
type Pool struct {
	Name   string  `json:"name"`
	Chance float64 `json:"chance"`
}

// LootTuning is the rest of LootBagTuning: the first-time-user bags (by bag name) and the bag points of
// the first win of the day.
type LootTuning struct {
	FTUEBag        string `json:"ftueBag,omitempty"`    // FTUEBag_Loot ("Platinum")
	DefaultBag     string `json:"defaultBag,omitempty"` // FTUEBag_Default ("Bronze")
	FirstWinPoints int    `json:"firstWinPoints,omitempty"`
	WonGamePoints  int    `json:"wonGamePoints,omitempty"` // Points_WonGame (100; the Lua formula says 200)
}

// AssetID is the AssetCatalog id of an asset name such as "Platinum.LootBag": FNV-1 (32 bit) of the
// lower-cased "name.class" (AssetCatalog_FindByName 0x008CF0F0; re/scripts/postgame.py fnv1).
func AssetID(name string) uint32 {
	h := uint32(0x811C9DC5)
	for _, c := range []byte(strings.ToLower(name)) {
		h *= 0x01000193
		h ^= uint32(c)
	}
	return h
}

// Bag returns the loot bag with this name.
func (c *Content) Bag(name string) (LootBag, bool) {
	if c != nil {
		for _, b := range c.LootBags {
			if strings.EqualFold(b.Name, name) {
				return b, true
			}
		}
	}
	return LootBag{}, false
}

// Rarity returns the rarity with this name.
func (c *Content) Rarity(name string) (Rarity, bool) {
	if c != nil {
		for _, r := range c.Rarities {
			if r.Name == name {
				return r, true
			}
		}
	}
	return Rarity{}, false
}

// Perk returns the perk shape or gem whose CollectionItem has this catalogID.
func (c *Content) Perk(id uint32) (Perk, bool) {
	if c != nil && id != 0 {
		for _, p := range c.Perks {
			if p.CatalogID == id {
				return p, true
			}
		}
	}
	return Perk{}, false
}

// ShaperOf returns the catalogID of the shaper (its "shaper" CollectionItem) that id names (B7d: the
// match host reports a player's shaper as playerFinish.stats.noun, the asset id of the player's Noun):
//   - the asset id of a shaper's or skin's Noun (CollectionItem.parentAsset, e.g. "alpha.Noun" or
//     "alpha_seawolf.Noun": AssetID of the name),
//   - the asset id of the shaper Noun of a PlayerClass ("PC_Alpha.Noun", the default-skin Noun's parentNoun,
//     which the client's catalog map also maps to the shaper's catalogID: ACCOUNT_CONTENT.md §1),
//   - or a shaper's or skin's catalogID itself.
//
// That catalogID is what the profile's shaper stats are keyed by (ACCOUNT_CONTENT.md §11). false when id
// names no shaper or the shaper has no CollectionItem.
func (c *Content) ShaperOf(id uint32) (uint32, bool) {
	if c == nil || id == 0 {
		return 0, false
	}
	class := ""
	for _, it := range c.Items {
		if (it.ItemType == ItemShaper || it.ItemType == ItemShaperSkin) && (it.CatalogID == id || AssetID(it.Asset) == id) {
			class = it.Shaper
			break
		}
	}
	for _, s := range c.Shapers {
		if (class != "" && s.Name == class) || (class == "" && AssetID(s.Name+".Noun") == id) {
			return s.CatalogID, s.CatalogID != 0
		}
	}
	return 0, false
}

// Old reports a content file of the B7a format: no sockets, drop tables or loot tuning.
func (c *Content) Old() bool { return c != nil && c.Format == FormatB7a }

// Item returns the item with this catalogID.
func (c *Content) Item(id uint32) (Item, bool) {
	if c == nil {
		return Item{}, false
	}
	i, ok := c.byID[id]
	if !ok {
		return Item{}, false
	}
	return c.Items[i], true
}

// Empty reports whether there is no content (no file loaded).
func (c *Content) Empty() bool { return c == nil || len(c.Items) == 0 }

func (c *Content) index() error {
	c.byID = make(map[uint32]int, len(c.Items))
	for i, it := range c.Items {
		if it.CatalogID == 0 {
			return fmt.Errorf("content: item %q has catalogID 0", it.Name)
		}
		if j, dup := c.byID[it.CatalogID]; dup {
			return fmt.Errorf("content: catalogID %#x is used by %q and %q", it.CatalogID, c.Items[j].Name, it.Name)
		}
		c.byID[it.CatalogID] = i
	}
	return nil
}

// Load reads a content file.
func Load(path string) (*Content, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Content
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("content: %s: %v", path, err)
	}
	if c.Format != Format && c.Format != FormatB7a {
		return nil, fmt.Errorf("content: %s: format %q, want %q (regenerate it with dgserver --make-content)", path, c.Format, Format)
	}
	if err := c.index(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Write writes the content file (creating its folder).
func (c *Content) Write(path string) error {
	raw, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// Grant policies for a new account's inventory (dgserver --owned).
const (
	OwnAll     = "all"     // every inventory item except testing-only shapers and their skins
	OwnStarter = "starter" // always-owned items, loadout pages, the default avatar, one shaper per archetype
)

// Grant returns the inventory (catalogID -> count) a new account gets under the policy, and its
// avatar (DefaultAvatar).
//
//   - OwnAll: every item with an item type that isn't testing-only, MaxOwnable of each.
//   - OwnStarter: the items whose specialStatus is alwaysOwned, the loadout pages, the default avatar,
//     and for each shaper archetype the non-testing shaper with the lowest catalogID.
func (c *Content) Grant(policy string) (map[uint32]uint32, uint32, error) {
	items := map[uint32]uint32{}
	if c.Empty() {
		return items, 0, nil
	}
	avatar := c.DefaultAvatar()
	switch policy {
	case OwnAll:
		for _, it := range c.Items {
			if it.ItemType != ItemInvalid && !it.TestingOnly {
				items[it.CatalogID] = max(it.MaxOwnable, 1)
			}
		}
	case OwnStarter:
		for _, it := range c.Items {
			if it.ItemType == ItemInvalid || it.TestingOnly {
				continue
			}
			if it.Status == StatusAlwaysOwned || it.ItemType == ItemLoadoutPage {
				items[it.CatalogID] = max(it.MaxOwnable, 1)
			}
		}
		if avatar != 0 {
			items[avatar] = 1
		}
		archetypes := map[string]bool{}
		for _, it := range c.Items { // by catalogID
			if it.ItemType != ItemShaper || it.TestingOnly {
				continue
			}
			arch := c.shaperArchetype(it.Shaper)
			if arch == "" || archetypes[arch] {
				continue
			}
			archetypes[arch] = true
			items[it.CatalogID] = 1
		}
	default:
		return nil, 0, fmt.Errorf("content: unknown ownership policy %q (want %q or %q)", policy, OwnAll, OwnStarter)
	}
	return items, avatar, nil
}

func (c *Content) shaperArchetype(name string) string {
	for _, s := range c.Shapers {
		if s.Name == name {
			return s.Archetype
		}
	}
	return ""
}

// DefaultAvatar is the avatar item (PLAYER_AVATAR) with the lowest catalogID, or 0 if there is none.
// In build 854 that is YearZero ("Year Zero").
func (c *Content) DefaultAvatar() uint32 {
	if c == nil {
		return 0
	}
	for _, it := range c.Items {
		if it.ItemType == ItemPlayerAvatar {
			return it.CatalogID
		}
	}
	return 0
}

// sortedNames returns a map's keys in order.
func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// assetName strips the class from an asset reference: "alpha.Noun" -> "alpha".
func assetName(ref string) string {
	if i := strings.LastIndexByte(ref, '.'); i >= 0 {
		return ref[:i]
	}
	return ref
}
