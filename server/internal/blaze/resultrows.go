package blaze

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"dawngate/server/internal/content"
	"dawngate/server/internal/discordhook"
	"dawngate/server/internal/store"
)

// This file builds the result post's rows (M32): every seat of the finished match, bots included, with its shaper,
// K/D/A, CS, vim and final items, from the match record Finish builds for the history (historyMatch's merged seats;
// no store needed). The text style (resultMessage) writes them into the embed's fields; the image style
// (ResultPosts.Image, resultimage.go) draws the same rows. A host that sent no seatStats gets no rows: the post keeps
// its humans' lines and the bots counted.

// ResultSlots is how many of a seat's inventory slots the post shows: the items (slots 0-5) and the battle spells
// (6-8); the consumables (9-10) are left out.
const ResultSlots = 9

// resultSpellSlot is the first battle spell's slot.
const resultSpellSlot = 6

// ResultRow is one seat of a finished match as the result post shows it.
type ResultRow struct {
	Team int // 1 or 2
	Seat int // the gameplay index
	// Name is the player's name unescaped; "" for a bot and with discord_names off (the post says "Bot" or "a player").
	Name   string
	Bot    bool
	Left   bool // a human who left the match before its end (or never reached it)
	Result bool // the host reported stats for the seat (else the post says "no result")
	// Shaper is the shaper's name (its content title, "Ember"); "" when unknown. Portrait is its PlayerClass
	// ("PC_Alpha"), the portrait's file name in content.PortraitDir without ".png" (the image style); "" when unknown.
	Shaper, Portrait       string
	Level                  int64
	Kills, Deaths, Assists int64
	CS, Vim                int64                   // creeps, gold
	Items                  [ResultSlots]ResultItem // by slot; ID 0 = an empty slot
	HasItems               bool                    // the seat has an inventory (a host that reports items)
}

// ResultItem is an item in a seat's final inventory: its asset id and its name (the content's title; "Item", or
// "Battle spell" in slots 6-8, when the content doesn't know it). File is the match item's asset name
// ("Advanced_Abolition"), its icon's file name in content.IconDir without ".png" (the image style); "" when the
// content doesn't know it.
type ResultItem struct {
	ID   int64
	Name string
	File string
}

// resultRows are a finished match's rows: its seats on teams 1 and 2 in team and seat order (seats as matchLocked
// sorts them), humans and bots. names maps a persona to its name (the roster's); with showNames false no row has a
// name. items names the item ids (itemTitles).
func resultRows(seats []store.MatchSeat, names map[int64]string, showNames bool, c *content.Content,
	items map[int64]ResultItem) []ResultRow {
	rows := make([]ResultRow, 0, len(seats))
	for team := 1; team <= 2; team++ {
		for _, st := range seats {
			if st.Team != team {
				continue
			}
			r := ResultRow{Team: team, Seat: st.Seat, Bot: st.Bot, Left: st.Left && !st.Bot, Result: len(st.Stats) > 0,
				Shaper: shaperTitle(st.Noun, c), Portrait: shaperClass(st.Noun, c), Level: st.Level, Kills: st.Kills, Deaths: st.Deaths, Assists: st.Assists,
				CS: st.CS, Vim: st.Gold}
			if showNames && !st.Bot {
				r.Name = names[st.PersonaID]
			}
			r.Items, r.HasItems = resultItems(st.Inventory, items)
			rows = append(rows, r)
		}
	}
	return rows
}

// resultItems are a stored inventory's first ResultSlots slots with their names (false: no inventory, or one that
// doesn't decode).
func resultItems(inv json.RawMessage, items map[int64]ResultItem) (out [ResultSlots]ResultItem, ok bool) {
	if len(inv) == 0 {
		return out, false
	}
	var ids []int64
	if json.Unmarshal(inv, &ids) != nil {
		return out, false
	}
	for slot, id := range ids[:min(len(ids), ResultSlots)] {
		if id == 0 {
			continue
		}
		it := items[id]
		if it.Name == "" {
			it.Name = "Item"
			if slot >= resultSpellSlot {
				it.Name = "Battle spell"
			}
		}
		it.ID = id
		out[slot] = it
	}
	return out, true
}

// itemTitles maps every match item's asset id (content.AssetID(name+".Item"), as seatStats' inventory has it) to its
// title (its asset name when it has none) and its asset name (File). The content doesn't change while the server
// runs: built once.
func (s *Server) itemTitles() map[int64]ResultItem {
	s.itemNamesOnce.Do(func() { s.itemNames = itemTitleMap(s.Content) })
	return s.itemNames
}

// itemTitleMap is itemTitles' map for a content (empty for none).
func itemTitleMap(c *content.Content) map[int64]ResultItem {
	m := map[int64]ResultItem{}
	if c == nil {
		return m
	}
	for _, it := range c.MatchItems {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			title = it.Name
		}
		id := int64(content.AssetID(it.Name + ".Item"))
		m[id] = ResultItem{ID: id, Name: title, File: it.Name}
	}
	return m
}

// shaperClass is the PlayerClass of the shaper a noun names ("PC_Alpha": its portrait's file name), "" when the
// content doesn't know it.
func shaperClass(noun uint32, c *content.Content) string {
	id, ok := c.ShaperOf(noun)
	if !ok {
		return ""
	}
	it, ok := c.Item(id)
	if !ok {
		return ""
	}
	return it.Shaper
}

// itemNamesCache is the Server's itemTitles cache (a field of Server).
type itemNamesCache struct {
	itemNamesOnce sync.Once
	itemNames     map[int64]ResultItem
}

// itemLinePrefix starts a row's item line (it sets the line apart from the next seat's).
const itemLinePrefix = "↳ "

// The text style's limits: Discord's for a field's value and for a whole embed (title, description, footer, every
// field's name and value).
const (
	fieldValueMax = 1024
	embedMax      = 6000
)

// rowFields are the text style's fields: per team its rows (rowText) packed into non-inline fields of at most
// fieldValueMax characters, "Team 1 (won)" and, when a team needs a second field, "Team 1 (continued)"; a team
// without a seat says "nobody". The item lines are dropped from the last rows first when the embed (head: its
// title, description and footer) would pass embedMax.
func rowFields(rows []ResultRow, winner int, head int) []discordhook.Field {
	withItems := make([]bool, len(rows))
	for i, r := range rows {
		withItems[i] = r.HasItems
	}
	for i := len(rows); ; i-- {
		fields := packRows(rows, withItems, winner)
		if i == 0 || head+fieldsLen(fields) < embedMax {
			return fields
		}
		withItems[i-1] = false
	}
}

// packRows packs the rows into the teams' fields (withItems: which rows show their item line).
func packRows(rows []ResultRow, withItems []bool, winner int) []discordhook.Field {
	var fields []discordhook.Field
	for team := 1; team <= 2; team++ {
		name := fmt.Sprintf("Team %d", team)
		if team == winner {
			name += " (won)"
		}
		var value string
		first := true
		flush := func() {
			n := name
			if !first {
				n = fmt.Sprintf("Team %d (continued)", team)
			}
			fields = append(fields, discordhook.Field{Name: n, Value: value})
			first, value = false, ""
		}
		for i, r := range rows {
			if r.Team != team {
				continue
			}
			text := rowText(r, withItems[i])
			if value != "" && utf8.RuneCountInString(value)+1+utf8.RuneCountInString(text) > fieldValueMax {
				flush()
			}
			if value != "" {
				value += "\n"
			}
			value += text
		}
		if value == "" && first {
			value = "nobody"
		}
		if value != "" {
			flush()
		}
	}
	return fields
}

// fieldsLen is the fields' length as Discord counts it towards embedMax (characters of names and values).
func fieldsLen(fields []discordhook.Field) int {
	n := 0
	for _, f := range fields {
		n += utf8.RuneCountInString(f.Name) + utf8.RuneCountInString(f.Value)
	}
	return n
}

// rowText is a row as the text style writes it: "**name** · shaper · K/D/A · N CS · N vim" ("Bot" for a bot, "a
// player" with names off, " (left)" after a player who left; "no result" when the host reported no stats), and with
// items a second line: "↳ ", the item names of slots 0-5, then " · " and the battle spells. Every name goes through
// Escape.
func rowText(r ResultRow, items bool) string {
	who := "a player"
	switch {
	case r.Bot:
		who = "Bot"
	case r.Name != "":
		who = "**" + discordhook.Escape(r.Name) + "**"
	}
	if r.Left {
		who += " (left)"
	}
	var line string
	if !r.Result {
		line = who + " · no result"
	} else {
		shaper := "?"
		if r.Shaper != "" {
			shaper = discordhook.Escape(r.Shaper)
		}
		line = fmt.Sprintf("%s · %s · %d/%d/%d · %d CS · %d vim", who, shaper, r.Kills, r.Deaths, r.Assists, r.CS, r.Vim)
	}
	if !items {
		return line
	}
	var gear, spells []string
	for slot, it := range r.Items {
		if it.ID == 0 {
			continue
		}
		if slot >= resultSpellSlot {
			spells = append(spells, discordhook.Escape(it.Name))
		} else {
			gear = append(gear, discordhook.Escape(it.Name))
		}
	}
	parts := make([]string, 0, 2)
	if len(gear) > 0 {
		parts = append(parts, strings.Join(gear, ", "))
	}
	if len(spells) > 0 {
		parts = append(parts, strings.Join(spells, ", "))
	}
	if len(parts) == 0 {
		return line
	}
	return line + "\n" + itemLinePrefix + strings.Join(parts, " · ")
}
