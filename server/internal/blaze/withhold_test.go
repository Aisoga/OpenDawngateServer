package blaze

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/store"
)

// Two of the withheld Spiritstones as build 854's content file has them.
const (
	idVanguard = 292526211 // vanguard, Spiritstone of the Vanguard
	idColossus = 287856852 // colossus, Spiritstone of the Colossus
)

// withheldContent is the starter page's content plus Vanguard and Colossus, loaded as dgserver loads a content file
// made before they were withheld (both still in its items).
func withheldContent(t *testing.T) *content.Content {
	t.Helper()
	c := starterContent(t)
	c.Items = append(c.Items,
		content.Item{CatalogID: idVanguard, Name: "vanguard", Type: "perk", ItemType: content.ItemPerkShape,
			Status: content.StatusNone, Asset: "vanguard.PerkShape", Title: "Spiritstone of the Vanguard", MaxOwnable: 1},
		content.Item{CatalogID: idColossus, Name: "colossus", Type: "perk", ItemType: content.ItemPerkShape,
			Status: content.StatusNone, Asset: "colossus.PerkShape", Title: "Spiritstone of the Colossus", MaxOwnable: 1})
	c.Perks = append(c.Perks,
		content.Perk{Name: "vanguard", Kind: content.KindShape, CatalogID: idVanguard, MaxOwnable: 1, Category: "defense",
			Sockets: []string{"red"}, Modifiers: []string{"modifier_PerkHandler_ProtectDistantAllies"}},
		content.Perk{Name: "colossus", Kind: content.KindShape, CatalogID: idColossus, MaxOwnable: 1, Category: "defense",
			Sockets: []string{"any"}, Modifiers: []string{"modifier_PerkHandler_ShieldFromBurstDamage"}})
	c.Items = append(c.Items,
		content.Item{CatalogID: idGolem, Name: "golem", Type: "shaper", ItemType: content.ItemShaper, Status: content.StatusNone,
			Asset: "golem.Noun", Title: "Faris", MaxOwnable: 1, Shaper: "PC_Golem"},
		content.Item{CatalogID: idParagon, Name: "golem_anzerani", Type: "shaper skin", ItemType: content.ItemShaperSkin,
			Status: content.StatusNone, Asset: "golem_anzerani.Noun", Title: "Paragon Faris", MaxOwnable: 1, Shaper: "PC_Golem"},
		content.Item{CatalogID: idGreen, Name: "golem_forest", Type: "shaper skin", ItemType: content.ItemShaperSkin,
			Status: content.StatusNone, Asset: "golem_forest.Noun", Title: "Paragon Faris", MaxOwnable: 1, Shaper: "PC_Golem"})
	c.Items, c.Withheld = append(c.Items, c.Withheld...), nil // as an old file has them
	return writeLoad(t, c)
}

// Faris and two of his skins as build 854's content has them (golem_forest is withheld).
const (
	idGolem   = 284486713 // golem, the shaper item (its default skin golem.Noun)
	idParagon = 290512491 // golem_anzerani, Paragon Faris
	idGreen   = 308615081 // golem_forest, the green copy
)

// A withheld item is never granted (either policy), sold or listed as a game item, and gives a page no stats.
func TestWithheldNotGrantedOrSold(t *testing.T) {
	c := withheldContent(t)
	if len(c.Withheld) != 3 {
		t.Fatalf("withheld %+v", c.Withheld)
	}
	// shaper select: the owner of both Paragon skins gets only the kept one, and a saved green skin isn't sent
	var e gmshost.RosterEntry
	owner := store.Account{Items: map[uint32]uint32{idGolem: 1, idParagon: 1, idGreen: 1},
		Settings: map[string]string{fmt.Sprintf("SKIN_0x%08X", idGolem): fmt.Sprint(idGreen)}}
	ShaperContent(c, owner, nil, &e)
	if got := e.Skins["golem.Noun"]; !slices.Equal(got, []string{"golem_anzerani.Noun"}) || len(e.SavedSkins) != 0 {
		t.Errorf("skins %v, saved %v", e.Skins, e.SavedSkins)
	}
	for _, policy := range []string{content.OwnAll, content.OwnStarter} {
		items, _, _ := c.Grant(policy)
		if items[idVanguard] != 0 || items[idColossus] != 0 {
			t.Errorf("%s grants a withheld stone: %v", policy, items)
		}
		if items[idVigor] == 0 {
			t.Errorf("%s: the starter parts are gone: %v", policy, items)
		}
	}
	for _, o := range OffersOf(c) {
		if c.IsWithheld(o.CatalogID) {
			t.Errorf("offer for %d", o.CatalogID)
		}
	}
	for _, g := range GameItemsOf(c, 10) {
		if c.IsWithheld(g.CatalogID) {
			t.Errorf("game item %d", g.CatalogID)
		}
	}
	page := store.PerkPage{ID: 1, Version: 2, Shapes: []store.PerkShape{
		{ID: idVanguard, Cell: 0, Gems: []store.PerkGem{{ID: idHealth}}},
		{ID: idColossus, Cell: 2, Gems: []store.PerkGem{{ID: idHealth}}},
	}}
	if got := PageStats(c, page); len(got.Modifiers) != 0 {
		t.Errorf("a withheld shape's modifiers reach the host: %v", got.Modifiers)
	}
	a := store.Account{Items: map[uint32]uint32{idVanguard: 1, idColossus: 1, idHealth: 2}, PerkPages: []store.PerkPage{page}}
	if why := checkPerkPage(page, a, c); !strings.Contains(why, "is not a perk shape") {
		t.Errorf("a page with a withheld shape: %q", why)
	}
}

// An account made before the stones were withheld (it owns them; one page uses Vanguard and Colossus, another only
// Vanguard) loses them at login: the inventory, the shapes with their gems; the pages stay valid, the rest in place;
// a second login changes nothing.
func TestWithholdOlderAccount(t *testing.T) {
	c := withheldContent(t)
	var log strings.Builder
	s := &Server{Content: c, Store: store.New(), Log: logging.New(&log), Policy: DefaultPolicy()}
	a, _, err := s.Store.Login("old@example.com", time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	a = s.ensurePages(s.grant(a))
	vigor := store.PerkShape{ID: idVigor, Cell: 0, Gems: []store.PerkGem{{ID: idHaste, Socket: 0}, {ID: idHaste, Socket: 1},
		{ID: idHaste, Socket: 2}, {ID: idHaste, Socket: 3}}}
	a, err = s.Store.Update(a.UserID, func(a *store.Account) {
		a.Items[idVanguard], a.Items[idColossus], a.Items[idPage], a.Items[idGreen] = 1, 1, 2, 1
		a.Settings[fmt.Sprintf("SKIN_0x%08X", idGolem)] = fmt.Sprint(idGreen)
		a.Settings["WARD"] = "285354426" // a kept ward: stays
		a.PerkPages[0] = store.PerkPage{ID: a.PerkPages[0].ID, Name: "Tank", Version: 3, Status: 1, Shapes: []store.PerkShape{
			vigor,
			{ID: idVanguard, Cell: 2, Gems: []store.PerkGem{{ID: idHealth, Socket: 0}}},
			{ID: idColossus, Cell: 10, Rotation: 1, Gems: []store.PerkGem{{ID: idHealth, Socket: 0}}},
		}}
		p := a.AddPage()
		a.Page(p.ID).Version = 2
		a.Page(p.ID).Shapes = []store.PerkShape{{ID: idVanguard, Cell: 5}}
	})
	if err != nil {
		t.Fatal(err)
	}

	got := s.withhold(a)
	if got.Items[idVanguard] != 0 || got.Items[idColossus] != 0 {
		t.Errorf("items %v", got.Items)
	}
	if _, has := got.Items[idVanguard]; has {
		t.Error("vanguard still an inventory entry")
	}
	if got.Items[idVigor] == 0 || got.Items[idHealth] == 0 {
		t.Errorf("other items lost: %v", got.Items)
	}
	p1, p2 := got.PerkPages[0], got.PerkPages[1]
	if p1.Name != "Tank" || p1.Version != 4 || len(p1.Shapes) != 1 || !slices.Equal(p1.Shapes[0].Gems, vigor.Gems) {
		t.Errorf("page 1 %+v", p1)
	}
	if p2.Version != 3 || len(p2.Shapes) != 0 {
		t.Errorf("page 2 %+v", p2)
	}
	for _, p := range got.PerkPages {
		if why := checkPerkPage(p, got, c); why != "" {
			t.Errorf("page %d refused after the migration: %s", p.ID, why)
		}
	}
	if stored, _ := s.Store.Account(got.UserID); len(stored.PerkPages[0].Shapes) != 1 || stored.Items[idColossus] != 0 {
		t.Errorf("not stored: %+v", stored.PerkPages)
	}
	if len(got.Settings) != 1 || got.Settings["WARD"] != "285354426" {
		t.Errorf("settings %v", got.Settings)
	}
	want := "withheld items removed: 3 from the inventory, 3 shapes and 2 gems from perk pages [1 2], 1 saved choices, avatar reset false"
	if !strings.Contains(log.String(), want) {
		t.Errorf("log:\n%s", log.String())
	}

	// a second login: nothing to do, nothing logged; ensurePages leaves the saved pages alone
	before := log.Len()
	again := s.ensurePages(s.withhold(got))
	if log.Len() != before {
		t.Errorf("second login logged:\n%s", log.String()[before:])
	}
	if again.PerkPages[0].Version != 4 || again.PerkPages[0].Name != "Tank" || len(again.PerkPages) != 2 {
		t.Errorf("second login: %+v", again.PerkPages)
	}

	// a new account under either policy has nothing to withhold
	for _, policy := range []string{content.OwnAll, content.OwnStarter} {
		s.Policy.Owned = policy
		n, _, _ := s.Store.Login(policy+"@example.com", time.Unix(1_800_000_000, 0))
		n = s.ensurePages(s.grant(n))
		if withheldIn(n, c) {
			t.Errorf("%s: a new account holds a withheld item", policy)
		}
	}
}
