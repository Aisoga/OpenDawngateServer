package blaze

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/store"
)

// The starter page's items as build 854's game data has them (CollectionItem, PerkShape, Gem, Socket; the
// content file dgserver --make-content writes), plus a loadout page, a shaper and an avatar for the grants.
const (
	idVigor   = 279208843 // Starter_HealthPower_B2, Blue Spiritstone of Vigor
	idBravery = 279208844 // Starter_ArmorHealth_G2, Green Spiritstone of Bravery
	idFervor  = 279208845 // Starter_HastePower_R1, Red Spiritstone of Fervor
	idHealth  = 279208832 // FlatHealth, Red Spark of Health
	idPower   = 279208835 // FlatPower, Green Spark of Power
	idHaste   = 279208838 // FlatHaste, Blue Spark of Haste
	idPage    = 279210428 // StandardPage, the loadout page
)

func starterContent(t *testing.T) *content.Content {
	t.Helper()
	add := func(id string, value float32) []content.PerkAttribute {
		return []content.PerkAttribute{{ID: id, Op: "Add", Value: value}}
	}
	c := &content.Content{Format: content.Format, Items: []content.Item{
		{CatalogID: idHealth, Name: "FlatHealth", Type: "gem", ItemType: content.ItemPerkGem, Status: content.StatusNone, MaxOwnable: 16},
		{CatalogID: idPower, Name: "FlatPower", Type: "gem", ItemType: content.ItemPerkGem, Status: content.StatusNone, MaxOwnable: 16},
		{CatalogID: idHaste, Name: "FlatHaste", Type: "gem", ItemType: content.ItemPerkGem, Status: content.StatusNone, MaxOwnable: 16},
		{CatalogID: idVigor, Name: "Starter_HealthPower_B2", Type: "perk", ItemType: content.ItemPerkShape, Status: content.StatusNone, MaxOwnable: 1},
		{CatalogID: idBravery, Name: "Starter_ArmorHealth_G2", Type: "perk", ItemType: content.ItemPerkShape, Status: content.StatusNone, MaxOwnable: 1},
		{CatalogID: idFervor, Name: "Starter_HastePower_R1", Type: "perk", ItemType: content.ItemPerkShape, Status: content.StatusNone, MaxOwnable: 2},
		{CatalogID: idPage, Name: "StandardPage", Type: "loadout", ItemType: content.ItemLoadoutPage, Status: content.StatusHide, MaxOwnable: 1},
	}, Perks: []content.Perk{
		{Name: "FlatHaste", Kind: content.KindGem, CatalogID: idHaste, MaxOwnable: 16, Category: "blue", Attributes: add("Haste", 0.4)},
		{Name: "FlatHealth", Kind: content.KindGem, CatalogID: idHealth, MaxOwnable: 16, Category: "red", Attributes: add("MaxHealth", 4.7)},
		{Name: "FlatPower", Kind: content.KindGem, CatalogID: idPower, MaxOwnable: 16, Category: "green", Attributes: add("Power", 0.4)},
		{Name: "Starter_ArmorHealth_G2", Kind: content.KindShape, CatalogID: idBravery, MaxOwnable: 1, Category: "offense",
			Sockets: []string{"green", "green"}, Attributes: append(add("MaxHealth", 27.7), add("PhysicalDamageReduction", 4)...)},
		{Name: "Starter_HastePower_R1", Kind: content.KindShape, CatalogID: idFervor, MaxOwnable: 2, Category: "defense",
			Sockets: []string{"red"}, Attributes: append(add("Haste", 1.2), add("Power", 1.2)...)},
		{Name: "Starter_HealthPower_B2", Kind: content.KindShape, CatalogID: idVigor, MaxOwnable: 1, Category: "defense",
			Sockets: []string{"blue", "blue", "blue", "blue"}, Attributes: append(add("MaxHealth", 70.9), add("Power", 5.8)...)},
	}}
	return writeLoad(t, c)
}

// writeLoad round-trips content through its file, as dgserver loads it.
func writeLoad(t *testing.T, c *content.Content) *content.Content {
	t.Helper()
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

// The shapes' outlines (Shape.convexShape, in cells: x the column, y the row) for the board check.
var starterOutlines = map[uint32][][2]float64{
	idVigor:   {{0, 0}, {2, 0}, {2, 4}, {0, 4}}, // rectangle16
	idBravery: {{0, 0}, {2, 0}, {2, 2}, {0, 2}}, // square8
	idFervor:  {{2, 0}, {2, 2}, {0, 2}},         // triangle4_starter
}

// boardPoint places a point of a shape's outline on the board as the perk screen draws a page: the clip sits at
// mGridPoints[ROT][GIDX] = ((column + dx) x cell, (row + dy) x cell), (dx, dy) = (0,0), (1,0), (1,1), (0,1), and
// turns ROT x 90 degrees clockwise (screen y points down) about that point.
func boardPoint(cell, rot uint8, p [2]float64) [2]float64 {
	d := [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}[rot]
	ax, ay := float64(cell%4)+d[0], float64(cell/4)+d[1]
	x, y := p[0], p[1]
	for i := uint8(0); i < rot; i++ {
		x, y = -y, x
	}
	return [2]float64{ax + x, ay + y}
}

// inside says whether q is inside the convex polygon (either winding).
func inside(poly [][2]float64, q [2]float64) bool {
	sign := 0.0
	for i := range poly {
		a, b := poly[i], poly[(i+1)%len(poly)]
		cross := (b[0]-a[0])*(q[1]-a[1]) - (b[1]-a[1])*(q[0]-a[0])
		if cross*sign < 0 {
			return false
		}
		if cross != 0 {
			sign = cross
		}
	}
	return true
}

// The starter page fills the board exactly: every half cell (both sides of each cell's top-right to bottom-left
// diagonal, the cut of the triangle) is covered by exactly one shape, and no shape leaves the board.
func TestStarterPageFillsTheBoard(t *testing.T) {
	page, ok := StarterPage(starterContent(t))
	if !ok {
		t.Fatal("no starter page")
	}
	var cover [4][4][2]int
	for i, s := range page.Shapes {
		outline := starterOutlines[s.ID]
		placed := make([][2]float64, len(outline))
		for j, p := range outline {
			placed[j] = boardPoint(s.Cell, s.Rotation, p)
			if placed[j][0] < 0 || placed[j][0] > 4 || placed[j][1] < 0 || placed[j][1] > 4 {
				t.Errorf("shape %d (%d at cell %d, rotation %d) leaves the board: %v", i, s.ID, s.Cell, s.Rotation, placed[j])
			}
		}
		for r := 0; r < 4; r++ {
			for col := 0; col < 4; col++ {
				for half, off := range [2]float64{0.3, 0.7} { // top-left half, bottom-right half
					if inside(placed, [2]float64{float64(col) + off, float64(r) + off}) {
						cover[r][col][half]++
					}
				}
			}
		}
	}
	for r := range cover {
		for col := range cover[r] {
			if cover[r][col] != [2]int{1, 1} {
				t.Errorf("cell row %d column %d: covered %v times", r, col, cover[r][col])
			}
		}
	}
	// the layout the user described: Vigor on the left half, Bravery top right, the Fervors in the bottom-right square
	want := []store.PerkShape{{ID: idVigor, Cell: 0}, {ID: idBravery, Cell: 2}, {ID: idFervor, Cell: 10}, {ID: idFervor, Cell: 15, Rotation: 2}}
	for i, w := range want {
		if s := page.Shapes[i]; s.ID != w.ID || s.Cell != w.Cell || s.Rotation != w.Rotation {
			t.Errorf("shape %d: %+v, want %+v", i, s, w)
		}
	}
}

// The page passes the server's own page check for a new account under both grant policies, every shape is complete
// (a Spark of its colour in every socket), and it gives the stats the user remembers: Health 108, Power 9, Haste 4,
// armor 4.
func TestStarterPageIsCompleteAndValid(t *testing.T) {
	c := starterContent(t)
	page, ok := StarterPage(c)
	if !ok {
		t.Fatal("no starter page")
	}
	if page.Name != "Starter" || page.ID != 1 || page.Version != 1 || page.Status != 1 {
		t.Errorf("summary %+v", page)
	}
	for _, policy := range []string{content.OwnAll, content.OwnStarter} {
		items, _, err := c.Grant(policy)
		if err != nil {
			t.Fatal(err)
		}
		acct, _ := testAccount()
		acct.Items, acct.PerkPages = items, []store.PerkPage{page}
		if why := checkPerkPage(page, acct, c); why != "" {
			t.Errorf("%s: refused: %s", policy, why)
		}
	}
	gems := 0
	for i, s := range page.Shapes {
		shape, _ := c.Perk(s.ID)
		if !perkComplete(c, shape, s.Gems) {
			t.Errorf("shape %d (%s) incomplete: %+v", i, shape.Name, s.Gems)
		}
		gems += len(s.Gems)
	}
	if len(page.Shapes) != 4 || gems != 8 {
		t.Errorf("%d shapes, %d gems; want 4 and 8", len(page.Shapes), gems)
	}
	want := map[string]int64{"Power": 9000, "MaxHealth": 108000, "Haste": 4000, "PhysicalDamageReduction": 4000}
	stats := PageStats(c, page)
	if len(stats.Attributes) != len(want) {
		t.Errorf("attributes %+v", stats.Attributes)
	}
	for _, a := range stats.Attributes {
		if name := content.AttributeName(a.Attribute); want[name] != a.Milli || content.OpName(a.Op) != "Add" {
			t.Errorf("%s %s: %d milli, want %d", name, content.OpName(a.Op), a.Milli, want[name])
		}
	}
	// one Spark too few: the page check refuses it
	items, _, _ := c.Grant(content.OwnStarter)
	items[idHaste] = 3
	acct, _ := testAccount()
	acct.Items, acct.PerkPages = items, []store.PerkPage{page}
	if why := checkPerkPage(page, acct, c); !strings.Contains(why, "placed 4 times, owned 3") {
		t.Errorf("3 Hastes: %q", why)
	}
}

// --owned starter grants exactly the page's parts (the shapes and Sparks as many as it uses), --owned all at least
// as many, and the store sells all six.
func TestStarterPageGrantsAndOffers(t *testing.T) {
	c := starterContent(t)
	parts := map[uint32]uint32{idFervor: 2, idBravery: 1, idVigor: 1, idHealth: 2, idPower: 2, idHaste: 4}
	starter, _, _ := c.Grant(content.OwnStarter)
	all, _, _ := c.Grant(content.OwnAll)
	for id, n := range parts {
		if starter[id] != n {
			t.Errorf("starter: %d x %d, want %d", starter[id], id, n)
		}
		if all[id] < n {
			t.Errorf("all: %d x %d, want at least %d", all[id], id, n)
		}
	}
	if got := c.StarterComponents(); len(got) != len(parts) {
		t.Errorf("components %v", got)
	}
	sold := map[uint32]bool{}
	for _, o := range OffersOf(c) {
		sold[o.CatalogID] = true
	}
	for id := range parts {
		if !sold[id] {
			t.Errorf("%d not sold", id)
		}
	}
	// without the starter shapes (the test fixture's content) there is no starter page and nothing extra
	if _, ok := StarterPage(loadFixtureContent(t)); ok {
		t.Error("starter page from the fixture content")
	}
	// a socket of the wrong colour: no page
	bad := starterContent(t)
	for i := range bad.Perks {
		if bad.Perks[i].CatalogID == idBravery {
			bad.Perks[i].Sockets = []string{"green", "red"}
		}
	}
	if _, ok := StarterPage(bad); ok || len(bad.StarterComponents()) != 0 {
		t.Error("starter page with a red socket in Bravery")
	}
}

// ensurePages at login: a new account's first page is the starter page; it is given once; an older account whose
// pages were never saved gets it (with the parts it lacks) and keeps its other pages; a page a player saved or
// renamed is never replaced.
func TestEnsurePagesStarter(t *testing.T) {
	c := starterContent(t)
	starter, _ := StarterPage(c)
	var log strings.Builder
	s := &Server{Content: c, Store: store.New(), Log: logging.New(&log), Policy: DefaultPolicy()}
	newAccount := func(mail, policy string) store.Account {
		t.Helper()
		a, _, err := s.Store.Login(mail, time.Unix(1_800_000_000, 0))
		if err != nil {
			t.Fatal(err)
		}
		s.Policy.Owned = policy
		return s.grant(a)
	}
	pageOK := func(name string, a store.Account) {
		t.Helper()
		if len(a.PerkPages) == 0 || a.PerkPages[0].Name != "Starter" || len(a.PerkPages[0].Shapes) != 4 {
			t.Fatalf("%s: pages %+v", name, a.PerkPages)
		}
		if why := checkPerkPage(a.PerkPages[0], a, c); why != "" {
			t.Errorf("%s: the starter page is refused: %s", name, why)
		}
	}

	// a new account, both policies
	for _, policy := range []string{content.OwnAll, content.OwnStarter} {
		a := s.ensurePages(newAccount(policy+"@example.com", policy))
		pageOK(policy, a)
		if len(a.PerkPages) != 1 || a.PerkPages[0].ID != 1 || a.PerkPages[0].Version != 1 {
			t.Errorf("%s: pages %+v", policy, a.PerkPages)
		}
		// the next login changes nothing
		before := log.Len()
		again := s.ensurePages(a)
		if log.Len() != before || len(again.PerkPages) != 1 {
			t.Errorf("%s: second login: %+v\n%s", policy, again.PerkPages, log.String()[before:])
		}
	}
	if !strings.Contains(log.String(), `perk page 1 is the starter page "Starter"`) {
		t.Errorf("log:\n%s", log.String())
	}

	// an older account (made before the starter page, without its parts) with two never-saved pages
	old := newAccount("old@example.com", content.OwnStarter)
	old, _ = s.Store.Update(old.UserID, func(a *store.Account) {
		for _, id := range []uint32{idFervor, idBravery, idVigor, idHealth, idPower, idHaste} {
			delete(a.Items, id)
		}
		a.Items[idPage] = 2
		a.Items[idHaste] = 1
		a.AddPage()
		a.AddPage()
	})
	old = s.ensurePages(old)
	pageOK("old account", old)
	if len(old.PerkPages) != 2 || old.PerkPages[1].Name != "Page 2" || len(old.PerkPages[1].Shapes) != 0 {
		t.Errorf("old account: pages %+v", old.PerkPages)
	}
	if old.Items[idHaste] != 4 || old.Items[idFervor] != 2 {
		t.Errorf("old account: items %v", old.Items)
	}

	// pages a player saved: an emptied page 1 (version 2), a renamed one, a second page with shapes
	for name, edit := range map[string]func(a *store.Account){
		"saved empty": func(a *store.Account) { a.AddPage(); a.PerkPages[0].Version = 2 },
		"renamed":     func(a *store.Account) { a.AddPage(); a.PerkPages[0].Name = "Mine" },
		"page 2 used": func(a *store.Account) {
			a.Items[idPage] = 2
			a.AddPage()
			a.AddPage()
			a.PerkPages[1].Shapes = starter.Shapes[:1]
		},
	} {
		a := newAccount(strings.ReplaceAll(name, " ", "")+"@example.com", content.OwnAll)
		a, _ = s.Store.Update(a.UserID, edit)
		want := store.ClonePages(a.PerkPages)
		got := s.ensurePages(a)
		if len(got.PerkPages) != len(want) || got.PerkPages[0].Name != want[0].Name || len(got.PerkPages[0].Shapes) != 0 {
			t.Errorf("%s: pages %+v, want %+v", name, got.PerkPages, want)
		}
	}
}

// With DG_CONTENT_FILE naming a content file made from the game (dgserver --make-content), the starter page builds
// from it and passes the same checks.
func TestStarterPageRealContent(t *testing.T) {
	path := os.Getenv("DG_CONTENT_FILE")
	if path == "" {
		t.Skip("DG_CONTENT_FILE not set")
	}
	c, err := content.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.StarterParts(); err != nil {
		t.Fatal(err)
	}
	page, _ := StarterPage(c)
	for _, policy := range []string{content.OwnAll, content.OwnStarter} {
		items, _, _ := c.Grant(policy)
		acct, _ := testAccount()
		acct.Items, acct.PerkPages = items, []store.PerkPage{page}
		if why := checkPerkPage(page, acct, c); why != "" {
			t.Errorf("%s: %s", policy, why)
		}
	}
	for i, s := range page.Shapes {
		if shape, _ := c.Perk(s.ID); !perkComplete(c, shape, s.Gems) {
			t.Errorf("shape %d incomplete", i)
		}
	}
	got := map[string]int64{}
	for _, a := range PageStats(c, page).Attributes {
		got[content.AttributeName(a.Attribute)] = a.Milli
	}
	want := map[string]int64{"Power": 9000, "MaxHealth": 108000, "Haste": 4000, "PhysicalDamageReduction": 4000}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %d milli, want %d (all: %v)", k, got[k], v, got)
		}
	}
}
