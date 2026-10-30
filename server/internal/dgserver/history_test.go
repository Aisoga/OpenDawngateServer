package dgserver

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/store"
	"dawngate/server/internal/web"
)

// updateHistory rewrites the golden files of TestHistoryGolden (go test ./internal/dgserver -run TestHistoryGolden
// -update-history); review the diff before committing it.
var updateHistory = flag.Bool("update-history", false, "rewrite testdata/history/*.json from the code")

var historyNow = time.Date(2026, 10, 27, 18, 0, 0, 0, time.UTC)

// Content ids of the history fixture.
const (
	histEmber    = 279208800 // the shaper Ember's CollectionItem
	histFenmore  = 279208801
	histStone    = 279208880 // Spiritstone of the Adventurer
	histStoneMod = "modifier_MediumPerk_BushSpeed"
	histItem     = "Advanced_Abolition"
)

// historyContent is a small content file: two shapers, a spirit stone with its modifier, a match item and a battle
// spell (round-tripped through its file, as dgserver loads it).
func historyContent(t *testing.T) *content.Content {
	t.Helper()
	c := &content.Content{Format: content.Format, Items: []content.Item{
		{CatalogID: histEmber, Name: "ember", Type: "shaper", ItemType: content.ItemShaper, Status: content.StatusNone,
			Asset: "ember.Noun", Title: "Ember", MaxOwnable: 1, Shaper: "PC_Ember"},
		{CatalogID: histFenmore, Name: "fenmore", Type: "shaper", ItemType: content.ItemShaper, Status: content.StatusNone,
			Asset: "fenmore.Noun", Title: "Fenmore", MaxOwnable: 1, Shaper: "PC_Fenmore"},
		{CatalogID: histStone, Name: "Adventurer", Type: "perk", ItemType: content.ItemPerkShape, Status: content.StatusNone,
			Asset: "Adventurer.PerkShape", Title: "Spiritstone of the Adventurer", MaxOwnable: 1},
	}, Shapers: []content.Shaper{{Name: "PC_Ember", CatalogID: histEmber, Title: "Ember"},
		{Name: "PC_Fenmore", CatalogID: histFenmore, Title: "Fenmore"}},
		Perks: []content.Perk{{Name: "Adventurer", Kind: content.KindShape, CatalogID: histStone, MaxOwnable: 1,
			Category: "offense", Modifiers: []string{histStoneMod}}},
		MatchItems: []content.MatchItem{{Name: histItem, Title: "Abolition", Category: "Stat", Tier: "Advanced", Cost: 1500},
			{Name: "Spell_Blink", Title: "Blink", Category: "Ability", Tier: "Basic"}}}
	path := filepath.Join(t.TempDir(), "content.json")
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := content.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// historyFixture is a store with three players (Alpha, Bravo, and Charlie, whose account is deleted) and three
// matches: 1 a practice match (Alpha and a bot), 2 a PvP match (Alpha against Bravo and a bot, with every statistic,
// sources and a timeline), 3 a custom game (Bravo, Charlie and a bot). It returns the store and the persona ids.
func historyFixture(t *testing.T) (*store.Store, []int64) {
	t.Helper()
	s := store.New()
	var ps []int64
	for i, name := range []string{"Alpha", "Bravo", "Charlie"} {
		a, _, err := s.Login(strings.ToLower(name)+"@example.com", historyNow.Add(-48*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Update(a.UserID, func(a *store.Account) { a.Personas[0].Name = name }); err != nil {
			t.Fatal(err)
		}
		ps = append(ps, a.Personas[0].ID)
		if i == 2 {
			defer func() {
				if err := s.DeleteAccount(a.UserID); err != nil {
					t.Fatal(err)
				}
			}()
		}
	}
	ember, fenmore := content.AssetID("ember.Noun"), content.AssetID("fenmore.Noun")
	at := func(h int) time.Time { return historyNow.Add(time.Duration(h) * time.Hour) }
	slot := func(n int) string { return `"slot":` + string(rune('0'+n)) + `,` }
	matches := []store.Match{
		{GameID: 1, Kind: "practice", Mode: "practice", Level: "SM_Map_v5", Winner: 1, Reason: "guardian",
			Started: at(-5), Ended: at(-5).Add(20 * time.Minute), DurationMs: 1200500, Seats: []store.MatchSeat{
				{Seat: 0, PersonaID: ps[0], UserID: 1, Team: 1, Noun: ember, Role: 1, Won: true, Kills: 4, Deaths: 1,
					Assists: 2, CS: 80, Gold: 6100, Level: 11, Stats: map[string]int64{"kills": 4}},
				{Seat: 5, Team: 2, Bot: true, Noun: fenmore, Role: 3, Kills: 1, Deaths: 4, CS: 60, Gold: 4200, Level: 9}}},
		{GameID: 2, Kind: "pvp", Mode: "pvp", Level: "MM_Map_v3", Winner: 2, Reason: "surrender",
			Started: at(-2), Ended: at(-2).Add(25 * time.Minute), DurationMs: 1501999,
			Timeline: json.RawMessage(`{"stepSec":60,"sec":[60,120],"seats":[{"index":0,"gold":[740,1310]}],` +
				`"events":[{"ms":95210,"kind":"kill","a":5,"b":0}]}`),
			Seats: []store.MatchSeat{
				{Seat: 0, PersonaID: ps[0], UserID: 1, Team: 1, Noun: ember, Role: 2, Left: true, Kills: 2, Deaths: 6,
					Assists: 3, CS: 41, Gold: 5300, Level: 10,
					Stats: map[string]int64{"damageDealt": 18234, "damageTaken": 9120, "timeDeadMs": 41000, "kills": 2},
					Sources: json.RawMessage(`[{"kind":"ability",` + slot(2) + `"id":0,"damage":5210,"heal":0},` +
						`{"kind":"basic","id":0,"damage":4000,"heal":0},` +
						`{"kind":"item","id":` + itoa(content.AssetID(histItem+".Item")) + `,"damage":812,"heal":0},` +
						`{"kind":"spell","id":` + itoa(content.AssetID("Spell_Blink.Item")) + `,"damage":0,"heal":0},` +
						`{"kind":"perk","id":` + itoa(content.AssetID(histStoneMod)) + `,"damage":300,"heal":0},` +
						`{"kind":"role","id":0,"damage":0,"heal":120},` +
						`{"kind":"item","id":12345,"damage":10,"heal":0},` +
						`{"kind":"ability",` + slot(4) + `"id":0,"damage":7,"heal":0},` +
						`{"kind":"other","id":0,"damage":3,"heal":0}]`)},
				{Seat: 5, PersonaID: ps[1], UserID: 2, Team: 2, Noun: fenmore, Role: 4, Won: true, Kills: 6, Deaths: 2,
					Assists: 1, CS: 77, Gold: 8800, Level: 13},
				{Seat: 6, Team: 2, Bot: true, Noun: 0, Role: 0, Won: true, Kills: 0, Deaths: 0, CS: 12, Gold: 2100, Level: 6,
					Stats: map[string]int64{"damageDealt": 1}}}},
		{GameID: 3, Kind: "custom", Mode: "custom", Level: "SM_Map_v5", Winner: 0, Reason: "",
			Started: at(-1), Ended: at(-1).Add(5 * time.Minute), DurationMs: 300000, Seats: []store.MatchSeat{
				{Seat: 0, PersonaID: ps[1], UserID: 2, Team: 1, Noun: fenmore, Kills: 0},
				{Seat: 1, PersonaID: ps[2], UserID: 3, Team: 1, Noun: ember},
				{Seat: 5, Team: 2, Bot: true, Noun: ember}}},
	}
	for _, m := range matches {
		if _, err := s.InsertMatch(m); err != nil {
			t.Fatal(err)
		}
	}
	return s, ps
}

func itoa(n uint32) string { b, _ := json.Marshal(n); return string(b) }

func newTestHistory(t *testing.T, names bool) (*history, *bytes.Buffer) {
	t.Helper()
	s, _ := historyFixture(t)
	var log bytes.Buffer
	h := newHistory(s, historyContent(t), names, func() time.Time { return historyNow },
		func(who, f string, a ...any) { log.WriteString(who + ": " + f + "\n") })
	return h, &log
}

func ask(h *history, kind, query string) (int, string) {
	q, err := url.ParseQuery(query)
	if err != nil {
		panic(err)
	}
	code, b := h.Serve(kind, q)
	return code, string(b)
}

// The golden answers (testdata/history/*.json, indented for reading; compared compacted): a list page of everyone's
// games with its next page, a player's own list, the last page, one match with everything, and the names-off forms.
func TestHistoryGolden(t *testing.T) {
	on, _ := newTestHistory(t, true)
	off, _ := newTestHistory(t, false)
	for _, c := range []struct {
		file, kind, query string
		h                 *history
	}{
		{"games_all", web.HistoryGames, "player=all&n=2&me=Alpha", on},
		{"games_all_last", web.HistoryGames, "before=2&n=2", on},
		{"games_alpha", web.HistoryGames, "player=alpha&me=ALPHA", on},
		{"game_2", web.HistoryGame, "id=2&me=Alpha", on},
		{"games_all_names_off", web.HistoryGames, "n=5&me=Alpha", off},
		{"game_3_names_off", web.HistoryGame, "id=3&me=Bravo", off},
	} {
		code, got := ask(c.h, c.kind, c.query)
		if code != 200 {
			t.Errorf("%s: %d %s", c.file, code, got)
			continue
		}
		path := filepath.Join("..", "..", "testdata", "history", c.file+".json")
		if *updateHistory {
			var b bytes.Buffer
			json.Indent(&b, []byte(got), "", "  ")
			b.WriteString("\n")
			if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var want bytes.Buffer
		if err := json.Compact(&want, raw); err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if got != want.String() {
			t.Errorf("%s:\n got %s\nwant %s", c.file, got, want.String())
		}
	}
}

// The rules beyond the golden files: the page size, the names rule's refusals, the errors.
func TestHistoryRules(t *testing.T) {
	on, log := newTestHistory(t, true)
	off, _ := newTestHistory(t, false)
	for _, c := range []struct {
		h           *history
		kind, query string
		code        int
		has         string
	}{
		{on, web.HistoryGames, "", 200, `"more":false,"nextBefore":0}`},
		{on, web.HistoryGames, "n=1", 200, `"more":true,"nextBefore":3}`},
		{on, web.HistoryGames, "n=500", 200, `"more":false`}, // clamped to 50
		{on, web.HistoryGames, "n=0", 400, `"error":"badRequest"`},
		{on, web.HistoryGames, "n=x", 400, `"error":"badRequest"`},
		{on, web.HistoryGames, "before=-1", 400, `"error":"badRequest"`},
		{on, web.HistoryGames, "player=Nobody", 404, `"error":"unknownPlayer"`},
		{on, web.HistoryGames, "player=Charlie", 404, `"error":"unknownPlayer"`}, // a deleted account isn't looked up
		{on, web.HistoryGames, "player=Bravo", 200, `"player":{"name":"Bravo"}`},
		{off, web.HistoryGames, "player=Bravo", 403, `"error":"namesOff"`},
		{off, web.HistoryGames, "player=Bravo&me=Alpha", 403, `"error":"namesOff"`},
		{off, web.HistoryGames, "player=Bravo&me=bravo", 200, `"name":"Bravo","bot":false,"you":true`},
		{on, web.HistoryGame, "", 400, `"error":"badRequest"`},
		{on, web.HistoryGame, "id=0", 400, `"error":"badRequest"`},
		{on, web.HistoryGame, "id=99", 404, `"error":"notFound"`},
		{on, "other", "", 404, `"error":"notFound"`},
	} {
		code, got := ask(c.h, c.kind, c.query)
		if code != c.code || !strings.Contains(got, c.has) || !strings.HasPrefix(got, `{"version":1,`) {
			t.Errorf("%s?%s: %d %s, want %d with %s", c.kind, c.query, code, got, c.code, c.has)
		}
	}
	// names off: no human's name but the asker's, in a list and in a match
	_, got := ask(off, web.HistoryGames, "n=50")
	if strings.Contains(got, "Alpha") || strings.Contains(got, "Bravo") || strings.Contains(got, `"you":true`) ||
		!strings.Contains(got, `"name":"Bot"`) || !strings.Contains(got, `"names":false`) {
		t.Errorf("names off, everyone's games: %s", got)
	}
	_, got = ask(off, web.HistoryGame, "id=2&me=Bravo")
	if strings.Contains(got, "Alpha") || !strings.Contains(got, `"name":"Bravo"`) {
		t.Errorf("names off, a match: %s", got)
	}
	if log.Len() != 0 {
		t.Errorf("log: %s", log)
	}
}

// A seat's sources that don't decode: the seat without them, a log line.
func TestHistoryBadSources(t *testing.T) {
	s := store.New()
	if _, err := s.InsertMatch(store.Match{Kind: "practice", Mode: "practice", Started: historyNow, Ended: historyNow,
		Seats: []store.MatchSeat{{Seat: 5, Team: 2, Bot: true, Sources: json.RawMessage(`{"kind":1}`)}}}); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	h := newHistory(s, nil, true, func() time.Time { return historyNow },
		func(who, f string, a ...any) { log.WriteString(who + ": " + f + "\n") })
	code, got := ask(h, web.HistoryGame, "id=1")
	if code != 200 || !strings.Contains(got, `"name":"Bot","bot":true`) || !strings.Contains(got, `"stats":{},"sources":[]}`) ||
		!strings.Contains(got, `"timeline":null`) || !strings.Contains(got, `"shaper":""`) ||
		!strings.Contains(log.String(), "http: match history: match %d seat %d: sources: %v") {
		t.Errorf("%d %s, log %s", code, got, log.String())
	}
}
