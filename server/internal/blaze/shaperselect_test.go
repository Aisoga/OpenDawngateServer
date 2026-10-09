package blaze

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/store"
)

// D3-live step 4: the roster's shaper select content from an account (shaperselect.go).
func TestShaperContent(t *testing.T) {
	c := loadFixtureContent(t)
	all, _, err := c.Grant(content.OwnAll)
	if err != nil {
		t.Fatal(err)
	}
	var shapers, skins []content.Item
	wards, announcers := 0, 0
	for _, it := range c.Items {
		if it.TestingOnly || it.Asset == "" {
			continue
		}
		switch it.ItemType {
		case content.ItemShaper:
			shapers = append(shapers, it)
		case content.ItemShaperSkin:
			skins = append(skins, it)
		case content.ItemWardSkin:
			wards++
		case content.ItemAnnouncerPack:
			announcers++
		}
	}
	if len(shapers) < 2 {
		t.Fatalf("the fixture has %d shapers", len(shapers))
	}
	first, second := shapers[0], shapers[1]

	// everything owned, the second shaper in the free rotation (by its title), a saved skin
	var skin content.Item
	for _, sk := range skins {
		if sk.Shaper == first.Shaper {
			skin = sk
			break
		}
	}
	a := store.Account{Items: all, Settings: map[string]string{}}
	if skin.CatalogID != 0 {
		a.Settings[fmt.Sprintf("SKIN_0x%X", first.CatalogID)] = fmt.Sprint(skin.CatalogID)
	}
	a.Settings["LOADOUT_0x1"] = "0" // not a skin
	a.PerkPages = []store.PerkPage{{ID: 1, Name: "Page 1", Version: 3, Status: 1,
		Shapes: []store.PerkShape{{ID: 0x10000006, Gems: []store.PerkGem{{ID: 0x10000007, Socket: 0}}}}},
		{ID: 4, Name: "Page 2", Version: 1, Status: 1}}
	var e gmshost.RosterEntry
	ShaperContent(c, a, []string{second.Title}, &e)
	// D3-perks: the pages in the account's order, with their stats (the Spark is incomplete: one gem of two)
	if want := []gmshost.PerkPage{{ID: 1, Version: 3, Attributes: []gmshost.PerkAttribute{{Attribute: 0, Op: 1, Milli: 400}}},
		{ID: 4, Version: 1}}; !reflect.DeepEqual(e.PerkPages, want) {
		t.Errorf("perk pages %+v, want %+v", e.PerkPages, want)
	}
	if len(e.Shapers) != len(shapers) {
		t.Fatalf("%d shapers, want %d: %+v", len(e.Shapers), len(shapers), e.Shapers)
	}
	for i, o := range e.Shapers {
		want := gmshost.ShaperOffer{Noun: shapers[i].Asset, Flags: gmshost.ShaperOwned}
		if i == 1 {
			want.Flags |= gmshost.ShaperFreeRotation
		}
		if o != want {
			t.Errorf("entry %d: %+v, want %+v", i, o, want)
		}
	}
	n := 0
	for def, list := range e.Skins {
		for _, s := range list {
			if s == def {
				t.Errorf("the default skin %s is listed as its own skin", def)
			}
		}
		n += len(list)
	}
	if n != len(skins) || len(e.Wards) != wards || len(e.Announcers) != announcers {
		t.Errorf("skins %d of %d, wards %d of %d, announcers %d of %d", n, len(skins), len(e.Wards), wards,
			len(e.Announcers), announcers)
	}
	if skin.CatalogID != 0 && !reflect.DeepEqual(e.SavedSkins, map[string]string{first.Asset: skin.Asset}) {
		t.Errorf("saved skins %v, want %s -> %s", e.SavedSkins, first.Asset, skin.Asset)
	}
	t.Logf("%d shapers, %d skins of %d shapers, %d wards, %d announcers, saved %v", len(e.Shapers), n, len(e.Skins),
		len(e.Wards), len(e.Announcers), e.SavedSkins)

	// nothing owned: every shaper is 0 (visible under "Any" only) but the free rotation's (by item name and by
	// PlayerClass), no skins, only the always-owned vanity items, and no saved skin (not owned)
	var bare gmshost.RosterEntry
	ShaperContent(c, store.Account{Settings: a.Settings}, []string{" " + strings.ToUpper(first.Name), second.Shaper}, &bare)
	for i, o := range bare.Shapers {
		want := 0
		if i < 2 {
			want = gmshost.ShaperFreeRotation
		}
		if o.Flags != want {
			t.Errorf("unowned entry %d: flags %#x, want %#x", i, o.Flags, want)
		}
	}
	if len(bare.Skins) != 0 || len(bare.SavedSkins) != 0 {
		t.Errorf("unowned: skins %v, saved %v", bare.Skins, bare.SavedSkins)
	}
	for _, w := range append(append([]string{}, bare.Wards...), bare.Announcers...) {
		found := false
		for _, it := range c.Items {
			if it.Asset == w && it.Status == "alwaysOwned" {
				found = true
			}
		}
		if !found {
			t.Errorf("unowned account lists %s", w)
		}
	}

	// no content: nothing
	var none gmshost.RosterEntry
	ShaperContent(nil, a, nil, &none)
	if !reflect.DeepEqual(none, gmshost.RosterEntry{}) {
		t.Errorf("without content: %+v", none)
	}
}

// D3-perks: a page's stats as LoadoutManager_ComputePageStats adds them up (the fixture's Spark: sockets red and
// any, MaxHealth Add 50, Power PercentChange 0.05, modifier_MediumPerk_BushSpeed; RedGem: red, Power Add 0.4).
func TestPageStats(t *testing.T) {
	c := loadFixtureContent(t)
	const spark, red = 0x10000006, 0x10000007
	page := func(gems ...store.PerkGem) store.PerkPage {
		return store.PerkPage{ID: 2, Version: 5, Shapes: []store.PerkShape{{ID: spark, Cell: 5, Rotation: 1, Gems: gems}}}
	}
	complete := gmshost.PerkPage{ID: 2, Version: 5, Attributes: []gmshost.PerkAttribute{{Attribute: 0, Op: 1, Milli: 800},
		{Attribute: 0, Op: 2, Milli: 50}, {Attribute: 2, Op: 1, Milli: 50000}}, Modifiers: []string{"modifier_MediumPerk_BushSpeed"}}
	gemsOnly := func(milli int64) gmshost.PerkPage {
		return gmshost.PerkPage{ID: 2, Version: 5, Attributes: []gmshost.PerkAttribute{{Attribute: 0, Op: 1, Milli: milli}}}
	}
	for _, tc := range []struct {
		name string
		p    store.PerkPage
		want gmshost.PerkPage
	}{
		{"complete (red in red, red in any)", page(store.PerkGem{ID: red, Socket: 1}, store.PerkGem{ID: red, Socket: 0}), complete},
		{"one socket empty", page(store.PerkGem{ID: red, Socket: 1}), gemsOnly(400)},
		{"one socket twice", page(store.PerkGem{ID: red, Socket: 0}, store.PerkGem{ID: red, Socket: 0}), gemsOnly(800)},
		{"an unknown gem", page(store.PerkGem{ID: red, Socket: 0}, store.PerkGem{ID: 0x99, Socket: 1}), gemsOnly(400)},
		{"a socket the shape lacks", page(store.PerkGem{ID: red, Socket: 0}, store.PerkGem{ID: red, Socket: 2}), gemsOnly(800)},
		{"empty", store.PerkPage{ID: 2, Version: 5}, gmshost.PerkPage{ID: 2, Version: 5}},
	} {
		if got := PageStats(c, tc.p); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}

	// a gem of the wrong colour leaves the shape incomplete; the sums are rounded to 0.001 after each addition, as
	// the client's floor((sum + value) x 1000 + 0.5); unknown attribute or op names give nothing
	hand := &content.Content{Perks: []content.Perk{
		{Name: "Shape", Kind: content.KindShape, CatalogID: 1, Sockets: []string{"blue"},
			Attributes: []content.PerkAttribute{{ID: "Haste", Op: "Add", Value: 2.4}, {ID: "Nonsense", Op: "Add", Value: 1},
				{ID: "Haste", Op: "Sometimes", Value: 1}}},
		{Name: "Armor", Kind: content.KindGem, CatalogID: 2, Category: "red",
			Attributes: []content.PerkAttribute{{ID: "PhysicalDamageReduction", Op: "PercentChange", Value: 0.007}}},
		{Name: "Cooldown", Kind: content.KindGem, CatalogID: 3, Category: "blue",
			Attributes: []content.PerkAttribute{{ID: "AbilityCooldownReduction", Op: "Add", Value: 0.0046}}},
	}}
	wrong := store.PerkPage{ID: 1, Version: 1, Shapes: []store.PerkShape{{ID: 1, Gems: []store.PerkGem{{ID: 2}}},
		{ID: 1, Gems: []store.PerkGem{{ID: 2}}}, {ID: 1, Gems: []store.PerkGem{{ID: 3}}}, {ID: 1, Gems: []store.PerkGem{{ID: 3}}}}}
	want := gmshost.PerkPage{ID: 1, Version: 1, Attributes: []gmshost.PerkAttribute{{Attribute: 16, Op: 1, Milli: 4800},
		{Attribute: 18, Op: 1, Milli: 10}, {Attribute: 24, Op: 2, Milli: 14}}}
	if got := PageStats(hand, wrong); !reflect.DeepEqual(got, want) {
		t.Errorf("colours and rounding: %+v, want %+v", got, want)
	}
}

func TestParseShaperTimers(t *testing.T) {
	got, err := ParseShaperTimers("8000, 8000,4000,16")
	want := map[string]int64{gmshost.TimerShaperSelection: 8000, gmshost.TimerRolePerkSelect: 8000,
		gmshost.TimerSkinSelect: 4000, gmshost.TimerReveal: 16}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %v (%v)", got, err)
	}
	for _, bad := range []string{"", "1,2,3", "1,2,3,x", "1,2,3,-4"} {
		if _, err := ParseShaperTimers(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// PRACTICE DAWNGATE with --shaper-select: createGame asks for shaper select, carries the timers and the
// player's content from its account (everything owned: B7a's default policy).
func TestHostedGameShaperSelect(t *testing.T) {
	timers, _ := ParseShaperTimers("8000,8000,4000,16")
	hs := newHostedSessionFull(t, "", "", nil, func(s *Server) {
		s.Content = loadFixtureContent(t)
		s.ShaperSelect = ShaperSelect{On: true, Timers: timers}
	}, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error { return nil })
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	cg := hs.hostGot().(gmshost.CreateGame)
	if !cg.ShaperSelect || !reflect.DeepEqual(cg.Timers, timers) || len(cg.Roster) != 1 {
		t.Fatalf("createGame %+v", cg)
	}
	r := cg.Roster[0]
	if len(r.Shapers) == 0 || len(r.Wards) == 0 || len(r.Announcers) == 0 || r.Shapers[0].Flags != gmshost.ShaperOwned {
		t.Errorf("roster entry %+v", r)
	}
	line, err := gmshost.Encode(cg)
	if err != nil || len(line) > gmshost.MaxLine {
		t.Errorf("createGame line: %d bytes (%v)", len(line), err)
	}
	t.Logf("createGame line with one player's content: %d bytes", len(line))
	hs.close()
}
