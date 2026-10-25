package content

import (
	"cmp"
	"slices"
	"strings"
)

// withheld is one entry of the withheld list: a CollectionItem name (Item.Name, any case; any item type) and why.
type withheld struct{ Name, Why string }

// withheldItems are the collection items the server never hands out: never granted (either policy), sold, given
// as game items, listed in shaper select, dropped as loot or part of a perk page, and taken out of older accounts
// at login (blaze's withhold). All are the user's decisions of 2026-10-25 (WIPSTONES).
//
// The six Spiritstones of the lowercase "PerkHandler" batch were work in progress in the original and never live:
// the user named Vanguard, Savant and Piledriver from knowledge of the original and confirmed Colossus, Spellweaver
// and Marauder from a website about it. Build 854's data still has them complete, with titles, shapes, sockets and
// effects, so before this list every account under the "all" policy owned them, the store sold them and the perk
// screen offered them. The eleven older Spiritstones (Adventurer, Berserker, Bravado, Brawler, Bulwark,
// Forcebubble, Harrier, Lurker, Reaper, Resonance, Scavenger) stay.
//
// The shaper skins: six have no title (no string table in build 854, so the client's collection never lists them),
// and two are copies of a released skin under a second catalogID. A withheld skin leaves only the server's side
// (grants, offers, shaper select's skin lists, inventories, a saved skin choice); the client's collection still
// shows every titled skin as owned (it counts every skin as owned), but it can't be picked.
//
// The two ward skins (the user: two wards are "wrong"; the Southern Brilliance and the Blazing Waystone the user sees
// in the collection stay): wardskin_magma has no title (no string table, so the collection can't list it), and
// wardSkin_southbox is a second "Southern Brilliance" (wardskin_south's title) under a later catalogID. A withheld
// ward leaves the host's ward list (the host refuses a pick of it) and a saved WARD choice; the client's own ward
// carousel in shaper select may still show it.
var withheldItems = []withheld{
	{"vanguard", "Spiritstone of the Vanguard (modifier_PerkHandler_ProtectDistantAllies): never live, the user's knowledge of the original"},
	{"savant", "Spiritstone of the Savant (_TemporaryCooldownReduction): never live, the user's knowledge of the original"},
	{"piledriver", "Spiritstone of the Piledriver (_PiercingAttacks): never live, the user's knowledge of the original"},
	{"colossus", "Spiritstone of the Colossus (_AttackDamageUpSpeedDown): never live, confirmed by the user from a website"},
	{"spellweaver", "Spiritstone of the Spellweaver (_ShieldFromBurstDamage): never live, confirmed by the user from a website"},
	{"marauder", "Spiritstone of the Marauder (_AttackSpeedAgainstSameTarget): never live, confirmed by the user from a website"},
	{"dreamer_badger", "a Dibs skin without a title"},
	{"survivor_ice", "a Freia skin without a title"},
	{"survivor_dark", "a Freia skin without a title"},
	{"highhuntress_trials", "a Nissa skin without a title"},
	{"artificer_obsidian", "a Petrus skin without a title"},
	{"gunman_silvermerc", "a Varion skin without a title"},
	// The user: only one of the two "House Anzerani Varion" entries shows in the collection. gunman_silver_hounds
	// is a copy of gunman_anzerani (its title, text, thumbnail and model are gunman_anzerani's; Silver Hounds is
	// gunman_hounds, which stays), so the copy goes.
	{"gunman_silver_hounds", "a copy of House Anzerani Varion (gunman_anzerani) under a second catalogID"},
	// The user: one of the two "Paragon Faris" is "a weird green version of the original". golem_forest is the
	// default model tinted green (tintColor 0,1,0) under golem_anzerani's title; golem_anzerani has its own model.
	{"golem_forest", "the default Faris tinted green, under Paragon Faris's title (golem_anzerani stays)"},
	{"wardskin_magma", "a ward skin without a title (Blazing Waystone, wardskin_wsmagma, stays)"},
	{"wardSkin_southbox", "a second Southern Brilliance ward under a later catalogID (wardskin_south stays)"},
}

// WithheldNames returns the names of the withheld items, in the list's order.
func WithheldNames() []string {
	out := make([]string, len(withheldItems))
	for i, w := range withheldItems {
		out[i] = w.Name
	}
	return out
}

// WithheldReason returns why the item with this name is withheld ("" if it isn't).
func WithheldReason(name string) string {
	for _, w := range withheldItems {
		if strings.EqualFold(w.Name, name) {
			return w.Why
		}
	}
	return ""
}

// IsWithheld reports whether id is the catalogID of a withheld item.
func (c *Content) IsWithheld(id uint32) bool { return c != nil && c.withheld[id] }

// applyWithheld moves the withheld items out of Items into Withheld, both when content is built and when a
// content file is loaded (a file made before the list, or before a name joined it, still has the item in
// items), and moves an item whose name has left the list back into Items, so a change of the list needs no
// new content file. A withheld item's catalogID also leaves FTUELoot. The perks (Perks) keep their
// entries, but Perk doesn't find a withheld one. Content without a withheld item stays as it is.
func (c *Content) applyWithheld() {
	names := map[string]bool{}
	for _, w := range withheldItems {
		names[strings.ToLower(w.Name)] = true
	}
	moves := len(c.Withheld) > 0
	for _, it := range c.Items {
		moves = moves || names[strings.ToLower(it.Name)]
	}
	c.withheld = map[uint32]bool{}
	if !moves {
		return
	}
	all := append(slices.Clone(c.Items), c.Withheld...)
	keep, held := make([]Item, 0, len(all)), []Item(nil)
	for _, it := range all {
		if names[strings.ToLower(it.Name)] {
			held = append(held, it)
			c.withheld[it.CatalogID] = true
		} else {
			keep = append(keep, it)
		}
	}
	byID := func(a, b Item) int { return cmp.Compare(a.CatalogID, b.CatalogID) }
	slices.SortStableFunc(keep, byID)
	slices.SortStableFunc(held, byID)
	c.Items, c.Withheld = keep, held
	if c.FTUELoot != nil {
		c.FTUELoot = slices.DeleteFunc(c.FTUELoot, func(id uint32) bool { return c.withheld[id] })
	}
}
