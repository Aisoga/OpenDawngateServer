package blaze

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"dawngate/server/internal/content"
	"dawngate/server/internal/discordhook"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
)

var updateResults = flag.Bool("update-results", false, "rewrite testdata/discord/result_*.json from the code")

// item is a fixture match item's asset id (as seatStats' inventory has it).
func item(name string) int64 { return int64(content.AssetID(name + ".Item")) }

// rowsResult is the result post's input for a finished game whose host sent seatStats: postResult's steps (the match
// record's seats, the roster's names, the rows) without a webhook.
func rowsResult(t *testing.T, hg *hostedGame, f gmshost.FinishGame, names bool, c *content.Content,
	items map[int64]ResultItem) matchResult {
	t.Helper()
	if hg.s == nil {
		hg.s = &Server{Content: c}
	}
	m, _ := hg.matchLocked(f, resultAt)
	r := hg.resultLocked(f, resultAt)
	byID := map[int64]string{}
	for _, hp := range hg.players {
		byID[hp.p.ID] = hp.p.Name
	}
	r.Rows = resultRows(m.Seats, byID, names, c, items)
	return r
}

// checkResultGolden compares the post as the sender sends it with testdata/discord/<name>.json (indented there).
func checkResultGolden(t *testing.T, name string, msg discordhook.Message) []byte {
	t.Helper()
	b, err := discordhook.Encode(msg)
	if err != nil {
		t.Fatal(err)
	}
	var ind bytes.Buffer
	if err := json.Indent(&ind, b, "", "  "); err != nil {
		t.Fatal(err)
	}
	ind.WriteByte('\n')
	path := filepath.Join(golden.Dir(), "discord", name+".json")
	if *updateResults {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, ind.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (go test -run TestResultRows -update-results writes it)", err)
	}
	if got := ind.String(); got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("%s:\n got %s\nwant %s", name, got, want)
	}
	if strings.Contains(string(b), `"@everyone`) || strings.Contains(string(b), `"roles"`) {
		t.Errorf("%s: a mention could ping: %s", name, b)
	}
	return b
}

// fiveVFive is a PvP match of two humans and eight bots whose host sent seatStats for every seat: Ann (team 1, seat
// 0) with items, a battle spell, an unknown battle spell and a consumable (not shown); Kim (team 2, seat 5), who
// left, without an inventory; the bots, some with items.
func fiveVFive() *hostedGame {
	hg := resultGame(gmshost.ModePvP, "1", 5, false, []resultSeat{
		{"Ann", 0, false, kda("ember.Noun", 5, 1, 7)}, {"Kim", 1, false, kda("stone.Noun", 1, 6, 2)}})
	hg.players[0].index, hg.players[0].indexed = 0, true
	hg.players[1].index, hg.players[1].indexed, hg.players[1].removed = 5, true, true
	hg.seatStats = map[int]gmshost.SeatStats{}
	keep := func(ss gmshost.SeatStats) { hg.seatStats[ss.Index] = ss }
	keep(gmshost.SeatStats{Index: 0, User: 2000000001, Team: 1, Stats: map[string]int64{"creeps": 152, "gold": 11840, "level": 14},
		Inventory: []int64{item("Advanced_Edge"), item("Advanced_Guard"), 0, item("Legendary_Storm"), 0, 0,
			item("Spell_Hop"), 1234567, 0, item("Consumable_Tonic"), 0}})
	keep(gmshost.SeatStats{Index: 5, User: 2000000002, Team: 2, Stats: map[string]int64{"creeps": 61, "gold": 5120, "level": 9}})
	bots := []struct {
		index, team int
		noun        string
		k, d, a, cs int64
		inv         []int64
	}{
		{1, 1, "stone.Noun", 2, 3, 9, 98, []int64{item("Basic_Blade"), 0, 0, 0, 0, 0, item("Spell_Hop"), 0, 0, 0, 0}},
		{2, 1, "ghost.Noun", 4, 2, 3, 120, nil},
		{3, 1, "ember_frost.Noun", 0, 4, 11, 40, []int64{item("Advanced_Guard"), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
		{4, 1, "ghost_mist.Noun", 8, 0, 4, 177, []int64{item("Legendary_Storm"), item("Advanced_Edge"), 0, 0, 0, 0, 0, 0, 0, 0, 0}},
		{6, 2, "ember.Noun", 3, 4, 1, 87, []int64{item("Basic_Blade"), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
		{7, 2, "ghost.Noun", 2, 6, 3, 75, nil},
		{8, 2, "stone.Noun", 0, 3, 4, 33, []int64{0, 0, 0, 0, 0, 0, item("Spell_Hop"), 0, 0, 0, 0}},
		{9, 2, "nobody.Noun", 1, 2, 0, 12, nil},
	}
	for _, b := range bots {
		keep(gmshost.SeatStats{Index: b.index, Bot: true, Team: b.team, Noun: int64(content.AssetID(b.noun)),
			Stats:     map[string]int64{"kills": b.k, "deaths": b.d, "assists": b.a, "creeps": b.cs, "gold": 1000 * b.cs / 10, "level": 10},
			Inventory: b.inv})
	}
	return hg
}

// The text style's rows: a 5v5 with eight bots (every seat a row in team and seat order, the items and battle spells
// by name, a consumable and an empty slot left out, an unknown battle spell named so, the player who left marked, a
// seat without an inventory without an item line, non-inline fields), the same with names off, an old host (no
// seatStats: today's lines and the bots counted), and a worst case of long, escaped names over two fields.
func TestResultRows(t *testing.T) {
	c := loadFixtureContent(t)
	items := itemTitleMap(c)
	f := gmshost.FinishGame{WinnerTeam: 1, DurationMs: 1865400, Reason: "guardian"}

	r := rowsResult(t, fiveVFive(), f, true, c, items)
	if len(r.Rows) != 10 || r.Rows[0].Name != "Ann" || r.Rows[0].Shaper != "Ember" || !r.Rows[0].HasItems ||
		r.Rows[0].Items[6].Name != "Hop" || r.Rows[0].Items[7].Name != "Battle spell" || r.Rows[5].Name != "Kim" ||
		!r.Rows[5].Left || r.Rows[5].HasItems || !r.Rows[6].Bot || r.Rows[6].Seat != 6 || r.Rows[0].Level != 14 {
		t.Errorf("rows %+v", r.Rows)
	}
	// the image style's file names: the portrait by PlayerClass, the icons by asset name (none for an unknown id)
	if r.Rows[0].Portrait != "PC_Ember" || r.Rows[0].Items[0].File != "Advanced_Edge" || r.Rows[0].Items[6].File != "Spell_Hop" ||
		r.Rows[0].Items[7].File != "" || r.Rows[9].Portrait != "" {
		t.Errorf("file names: %q %+v; last %q", r.Rows[0].Portrait, r.Rows[0].Items, r.Rows[9].Portrait)
	}
	b := checkResultGolden(t, "result_5v5_bots", resultMessage(r, true, c))
	if strings.Contains(string(b), `"inline":true`) || strings.Contains(string(b), "Tonic") {
		t.Errorf("an inline field or a consumable: %s", b)
	}

	off := rowsResult(t, fiveVFive(), f, false, c, items)
	b = checkResultGolden(t, "result_5v5_names_off", resultMessage(off, false, c))
	if strings.Contains(string(b), "Ann") || strings.Contains(string(b), "Kim") {
		t.Errorf("names off shows a name: %s", b)
	}

	// an old host: no seatStats, no rows; the post is DISCORD2c's
	old := resultGame(gmshost.ModePvP, "1", 5, false, []resultSeat{{"Ann", 0, false, kda("ember.Noun", 3, 1, 2)}})
	old.s = &Server{Content: c}
	b, err := discordhook.Encode(resultMessage(old.resultLocked(f, resultAt), true, c))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `{"name":"Team 1 (won)","value":"**Ann** · Ember · 3/1/2\n+ 4 bots","inline":true}`) {
		t.Errorf("old host: %s", b)
	}

	// the worst case: ten humans with 32-character names of markdown characters (each escaped to 64), nine items
	// each with as long a name: team 1 continues in a second field, every field at most 1024, the embed under 6000
	long := make([]resultSeat, 10)
	for i := range long {
		long[i] = resultSeat{strings.Repeat("*", 31) + string(rune('a'+i)), int64(i / 5), false, kda("ember.Noun", 10, 10, 10)}
	}
	hg := resultGame(gmshost.ModeCustom, "0", 0, true, long)
	hg.seatStats = map[int]gmshost.SeatStats{}
	longItems := map[int64]ResultItem{}
	inv := make([]int64, 11)
	for s := range ResultSlots {
		inv[s] = int64(9000 + s)
		longItems[inv[s]] = ResultItem{Name: strings.Repeat("_", 31) + string(rune('A'+s))}
	}
	for i := range long {
		hg.seatStats[i] = gmshost.SeatStats{Index: i, User: int64(2000000001 + i), Team: i/5 + 1,
			Stats: map[string]int64{"creeps": 999, "gold": 99999}, Inventory: inv}
	}
	lr := rowsResult(t, hg, gmshost.FinishGame{WinnerTeam: 2, DurationMs: 3723000, Reason: "surrender"}, true, c, longItems)
	msg := resultMessage(lr, true, c)
	b = checkResultGolden(t, "result_long_names", msg)
	e := msg.Embeds[0]
	total := utf8.RuneCountInString(e.Title) + utf8.RuneCountInString(e.Description) + utf8.RuneCountInString(e.Footer.Text)
	var names []string
	for _, fl := range e.Fields {
		if n := utf8.RuneCountInString(fl.Value); n > 1024 {
			t.Errorf("field %q: %d characters", fl.Name, n)
		}
		total += utf8.RuneCountInString(fl.Name) + utf8.RuneCountInString(fl.Value)
		names = append(names, fl.Name)
	}
	if total >= 6000 {
		t.Errorf("the embed has %d characters", total)
	}
	// each row with its item line takes about 700 characters: a field each, the last rows without their items
	if names[0] != "Team 1" || names[1] != "Team 1 (continued)" || !slices.Contains(names, "Team 2 (won)") ||
		names[len(names)-1] != "Team 2 (continued)" {
		t.Errorf("fields %v", names)
	}
	if !strings.Contains(e.Fields[0].Value, "\n"+itemLinePrefix+`\_`) || !strings.HasSuffix(e.Fields[len(e.Fields)-1].Value, " vim") {
		t.Errorf("the item lines (kept first, dropped last): %s", b)
	}
}

// Packing: a field never passes 1024 characters; item lines go from the last rows first when the embed would pass
// 6000; a team without a seat says "nobody".
func TestResultRowFields(t *testing.T) {
	row := func(team int, name string) ResultRow {
		r := ResultRow{Team: team, Name: name, Result: true, HasItems: true}
		for s := range ResultSlots {
			r.Items[s] = ResultItem{ID: int64(s + 1), Name: strings.Repeat("x", 32)}
		}
		return r
	}
	var rows []ResultRow
	for i := range 10 {
		rows = append(rows, row(1, strings.Repeat("y", 32)+string(rune('a'+i))))
	}
	const head = 3000
	fields := rowFields(rows, 1, head)
	if len(fields) < 3 || fields[0].Name != "Team 1 (won)" || fields[len(fields)-1].Name != "Team 2" ||
		fields[len(fields)-1].Value != "nobody" {
		t.Fatalf("fields %+v", fields)
	}
	if n := fieldsLen(fields); head+n >= embedMax {
		t.Errorf("the embed: %d", head+n)
	}
	if !strings.Contains(fields[0].Value, "\n"+itemLinePrefix+"xxx") || !strings.HasSuffix(fields[len(fields)-2].Value, " vim") {
		t.Errorf("the first rows keep their items, the last lose them first: %+v", fields)
	}
	for _, fl := range fields {
		if utf8.RuneCountInString(fl.Value) > fieldValueMax {
			t.Errorf("%q: %d", fl.Name, utf8.RuneCountInString(fl.Value))
		}
	}
}
