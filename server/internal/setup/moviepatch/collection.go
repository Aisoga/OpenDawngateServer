package moviepatch

import (
	_ "embed"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"dawngate/server/internal/content"
)

// CollectionTags are the Collection's Items class's four tags: DefineSprite 2000, DoABC (the class), SymbolClass 2000
// -> CollectionItems, PlaceObject3 of 2000 at depth 2000 without an instance name.
var CollectionTags = mustHex(collectionTagsHex)

//go:embed collection_tags.hex
var collectionTagsHex string

// Collection is the Collection's Items category (client_ui/collection): the match items as the Collection's own cards,
// in the front end's movie UIRuntime.gfx from game\Data\code.snap, in game\Data\zz_CollectionItems.snap with the item
// list (ItemsPath). Every player copy gets it; the setup sets ItemList (ItemList of the copy's own game data). A click
// on one of its cards opens the item's details (its stats and texts, the route from and into it as cards); the
// Collection's search field, Sort dropdown and an "Item Filters" category of the class's own act on its cards.
var Collection = Spec{What: "the Collection's items", Source: "game/Data/code.snap",
	Rel: "game/Data/zz_CollectionItems.snap", MoviePath: "UI/Scaleform/CDN/swf/code/UIRuntime.gfx",
	Class: "CollectionItems", CharID: 2000, Depth: 2000, Tags: CollectionTags, Items: true}

// ItemsPath is the Collection's item list in zz_CollectionItems.snap, which client_ui/collection/CollectionItems.as
// reads as Collection/dgitems.json. The file (UTF-8, LF, tab-separated):
//
//	dawngate-collitems	2
//	filter	<group>	<tag>	<title>             one per tag of the shop's filter groups, in their order
//	item	<name>	<title>	<kind>	<cost>	<group>	<icon>     one per match item a player can buy, then its details:
//	stat	<runs>                              one per stat: the shop's line ("+20 Haste")
//	passive	<kind>	<title>	<runs>              one per passive (kind unique, stackable or empty)
//	active	<kind>	<title>	<cooldown>	<runs>  its active (a spell's ability, a consumable's effect; cooldown in s)
//	from	<name>                              one per item it builds from (in the list)
//	into	<name>                              one per item it builds into (in the list)
//	tags	<tag> <tag> ...                     its stats' attributes and its tags (the shop filters' keys)
//	end
//
// kind: basic, advanced or legendary (the item's tier), consumable or spell (its category); group: a spell's own
// (AbilityItemCategory: Offense, Utility or Defense), any other item's the shop's filter groups (their titles) that
// hold one of its tags, in the shop's order, joined by " / " ("Offense / Utility"), empty without such a tag
// (COLLITEMS10: the game gives the other items no category of their own; itemGroup); icon: its ShopIcons texture in
// UI_Icons.snap, which the front end has mounted (forward slashes). <runs> is text in colours: pairs
// <colour><TAB><text>, the colour "#RRGGBB", empty (plain) or "n" (a line break, empty text). In the details a tab or
// CR is a space. re/scripts/patch_login_movie.py's item_list writes the same bytes from content.json.
const ItemsPath = "UI/Scaleform/Collection/dgitems.json"

// itemKindOrder is the list's order of kinds; an unknown kind goes last.
var itemKindOrder = map[string]int{"basic": 0, "advanced": 1, "legendary": 2, "consumable": 3, "spell": 4}

// itemKind is a match item's kind in the list: spell (category Ability), consumable (category Consumable), else its
// tier in lower case.
func itemKind(it content.MatchItem) string {
	switch it.Category {
	case "Ability":
		return "spell"
	case "Consumable":
		return "consumable"
	}
	return strings.ToLower(it.Tier)
}

// ItemList returns the Collection's item list for the match items of c: ordered by kind, cost, then title (stable:
// the items' own order after that), without the cheat items (the flag, or a name starting consumable_debug or holding
// cheat, as the server's) and without an item whose fields hold a tab or a line end; each item with its details, the
// shop's filter tags first. debug adds the line "debug<TAB>1" (the class then logs its steps; research only).
func ItemList(c *content.Content, debug bool) []byte {
	type row struct {
		order, cost int
		title       string
		fields      []string
		item        content.MatchItem
	}
	var rows []row
	groups := filterGroups(c)
	for _, it := range c.MatchItems {
		low := strings.ToLower(it.Name)
		if it.Cheat || strings.HasPrefix(low, "consumable_debug") || strings.Contains(low, "cheat") {
			continue
		}
		title := it.Title
		if title == "" {
			title = it.Name
		}
		kind := itemKind(it)
		fields := []string{it.Name, title, kind, strconv.Itoa(it.Cost), itemGroup(it, groups), strings.ReplaceAll(it.Icon, `\`, "/")}
		bad := false
		for _, f := range fields {
			bad = bad || strings.ContainsAny(f, "\t\n\r")
		}
		if bad {
			continue
		}
		order, ok := itemKindOrder[kind]
		if !ok {
			order = len(itemKindOrder)
		}
		rows = append(rows, row{order, it.Cost, title, fields, it})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.order != b.order {
			return a.order < b.order
		}
		if a.cost != b.cost {
			return a.cost < b.cost
		}
		return a.title < b.title
	})
	listed := map[string]bool{}
	for _, r := range rows {
		listed[r.fields[0]] = true
	}
	var b strings.Builder
	b.WriteString("dawngate-collitems\t2\n")
	if debug {
		b.WriteString("debug\t1\n")
	}
	for _, f := range c.ItemFilters {
		group := f.Title
		if group == "" {
			group = f.Name
		}
		for _, t := range f.Tags {
			if !tagOK(t.Tag) {
				continue
			}
			title := t.Title
			if title == "" {
				title = t.Tag
			}
			b.WriteString(strings.Join([]string{"filter", flat(group), t.Tag, flat(title)}, "\t") + "\n")
		}
	}
	for _, r := range rows {
		b.WriteString("item\t" + strings.Join(r.fields, "\t") + "\n")
		for _, l := range itemDetails(r.item, listed) {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("end\n")
	return []byte(b.String())
}

// tagOK: a tag the list carries, not empty, without a space, tab or line end.
func tagOK(t string) bool {
	return t != "" && !strings.ContainsAny(t, " \t\r\n")
}

// filterGroup is one of the shop's filter groups: its title in the list and the tags its filter lines carry.
type filterGroup struct {
	title string
	tags  []string
}

// filterGroups are the shop's filter groups of c in the shop's order.
func filterGroups(c *content.Content) []filterGroup {
	var out []filterGroup
	for _, f := range c.ItemFilters {
		title := f.Title
		if title == "" {
			title = f.Name
		}
		g := filterGroup{title: flat(title)}
		for _, t := range f.Tags {
			if tagOK(t.Tag) {
				g.tags = append(g.tags, t.Tag)
			}
		}
		out = append(out, g)
	}
	return out
}

// itemGroup is a match item's group in the list: a spell's own, else the titles of the filter groups that hold one of
// its tags (itemTags), in their order, each once, joined by " / " (empty: none).
func itemGroup(it content.MatchItem, groups []filterGroup) string {
	if itemKind(it) == "spell" {
		return it.Group
	}
	has := map[string]bool{}
	for _, t := range itemTags(it) {
		has[t] = true
	}
	var out []string
	for _, g := range groups {
		if g.title == "" || slices.Contains(out, g.title) {
			continue
		}
		if slices.ContainsFunc(g.tags, func(t string) bool { return has[t] }) {
			out = append(out, g.title)
		}
	}
	return strings.Join(out, " / ")
}

// itemTags are a match item's tags in the list: its stats' attributes, then its tags, each once (tagOK).
func itemTags(it content.MatchItem) []string {
	var tags []string
	for _, t := range append(statIDs(it), it.Tags...) {
		if tagOK(t) && !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	return tags
}

// statIDs are the attributes of a match item's stats.
func statIDs(it content.MatchItem) []string {
	ids := make([]string, 0, len(it.Stats))
	for _, s := range it.Stats {
		ids = append(ids, s.ID)
	}
	return ids
}

// flat is a detail field: tabs, CRs and line ends as spaces.
func flat(s string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(s)
}

// textRuns are text runs as list fields (colour, text, ...): parts, else text plain; a line end is the pair ("n", "").
func textRuns(parts []content.TextPart, text string) []string {
	if len(parts) == 0 && text != "" {
		parts = []content.TextPart{{Text: text}}
	}
	var out []string
	for _, p := range parts {
		c := p.Color
		if !isColour(c) {
			c = ""
		}
		for k, piece := range strings.Split(p.Text, "\n") {
			if k > 0 {
				out = append(out, "n", "")
			}
			piece = strings.NewReplacer("\t", " ", "\r", " ").Replace(piece)
			if piece != "" {
				out = append(out, c, piece)
			}
		}
	}
	return out
}

// isColour: "#RRGGBB".
func isColour(c string) bool {
	if len(c) != 7 || c[0] != '#' {
		return false
	}
	for _, x := range c[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", x) {
			return false
		}
	}
	return true
}

// itemDetails are the detail lines (without line ends) of a match item; listed the names of the items in the list.
func itemDetails(it content.MatchItem, listed map[string]bool) []string {
	var lines []string
	for _, s := range it.Stats {
		if runs := textRuns(s.Parts, s.ID); len(runs) > 0 {
			lines = append(lines, "stat\t"+strings.Join(runs, "\t"))
		}
	}
	for _, p := range it.Passives {
		lines = append(lines, strings.Join(append([]string{"passive", flat(p.Kind), flat(p.Title)},
			textRuns(p.Parts, p.Text)...), "\t"))
	}
	if a := it.ActiveInfo; a != nil {
		if runs := textRuns(a.Parts, a.Text); len(runs) > 0 {
			cd := 0
			if a.Cooldown > 0 {
				cd = int(math.Floor(a.Cooldown + 0.5))
			}
			lines = append(lines, strings.Join(append([]string{"active", flat(a.Kind), flat(a.Title), strconv.Itoa(cd)},
				runs...), "\t"))
		}
	}
	for _, st := range it.From {
		if listed[st.Item] {
			lines = append(lines, "from\t"+st.Item)
		}
	}
	for _, st := range it.Into {
		if listed[st.Item] {
			lines = append(lines, "into\t"+st.Item)
		}
	}
	if tags := itemTags(it); len(tags) > 0 {
		lines = append(lines, "tags\t"+strings.Join(tags, " "))
	}
	return lines
}
