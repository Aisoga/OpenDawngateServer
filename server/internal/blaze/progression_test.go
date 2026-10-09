package blaze

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// fixedRand is a progress.Rand that always returns v.
type fixedRand float64

func (r fixedRand) Float64() float64 { return float64(r) }

var (
	b7cPage1       = store.PerkPage{ID: 1, Name: "Page 1", Version: 1, Status: 1}
	b7cPage1Edited = store.PerkPage{ID: 1, Name: "Page 1", Version: 2, Status: 1, Shapes: []store.PerkShape{
		{ID: 0x10000006, Cell: 5, Rotation: 1, Gems: []store.PerkGem{{ID: 0x10000007, Socket: 0}}}}}
	b7cPage2 = store.PerkPage{ID: 2, Name: "Page 2", Version: 1, Status: 1}
	// the game the hosted tests play: kills 3, deaths 1, assists 2, best streak 3, one double kill
	b7cGameStats = map[string]int64{progress.StatKills: 3, progress.StatDeaths: 1, progress.StatAssists: 2,
		progress.StatBestStreak: 3, progress.StatDoubleKills: 1}
)

// b7cStatValues is PlayerStats after one won game with b7cGameStats, in the group's stat order.
func b7cStatValues() []string {
	stats := progress.ApplyPlayerStats(nil, true, b7cGameStats)
	values := make([]string, len(StatGroups["PlayerStats"]))
	for i, name := range StatGroups["PlayerStats"] {
		values[i] = stats[name]
		if values[i] == "" {
			values[i] = "0"
		}
	}
	return values
}

// b7cPayloads are the B7c replies with their golden case and member-table class (tdf_golden.py writes
// the same independently; replycheck_test.go checks the classes).
func b7cPayloads(t testing.TB) []struct {
	name, class string
	got         []byte
} {
	f := newB7aFixture(t)
	pid := int64(store.FirstPersonaID)
	return []struct {
		name, class string
		got         []byte
	}{
		{"b7c_getcompletedata_reply", "Blaze::BattlegroundsAccounts::BGAMetagameGetCompleteAccountDataResponse",
			CompleteAccountDataReply(f.server.basicDataOf(f.acct, pid), InventoryOf(f.acct, f.content), []store.PerkPage{b7cPage1})},
		{"b7c_getgameitems_reply", "Blaze::BGEcommerce::GetGameItemsResponse", GetGameItemsReply(GameItemsOf(f.content, DefaultPerkPages))},
		{"b7c_getperkpagedata_reply", "Blaze::BattlegroundsAccounts::BGAGetPerkPageDataResponse",
			PerkPageDataReply(pid, []store.PerkPage{b7cPage1})},
		{"b7c_getperkpagedata_two_reply", "Blaze::BattlegroundsAccounts::BGAGetPerkPageDataResponse",
			PerkPageDataReply(pid, []store.PerkPage{b7cPage1Edited, b7cPage2})},
		{"b7c_updateperkpage_reply", "Blaze::BattlegroundsAccounts::BGAUpdatePerkPageResponse", UpdatePerkPageReply(PerkPageUpdateSuccess)},
		{"b7c_updateperkpage_failed_reply", "Blaze::BattlegroundsAccounts::BGAUpdatePerkPageResponse", UpdatePerkPageReply(PerkPageUpdateFailed)},
		{"b7c_purchasewithec_reply", "Blaze::BGEcommerce::PurchaseWithECResponse",
			PurchaseWithECReply(Purchase{BlazeID: pid, Count: 1, Balance: 9500, Items: []uint32{0x10000006}, Quantity: []uint32{1}})},
		{"b7c_purchasewithpc_reply", "Blaze::BGEcommerce::PurchaseWithPCResponse",
			PurchaseWithPCReply(Purchase{BlazeID: pid, Count: 1, Balance: 700, Items: []uint32{0x1000000B}, Quantity: []uint32{1}})},
		{"b7c_getcommerceprofile_reply", "Blaze::BGEcommerce::GetCommerceProfileResponse",
			GetCommerceProfileReply(store.Commerce{SpentEC: 650, SpentPC: 300, CountEC: 2, CountPC: 1})},
		{"b7c_extendeddataupdate_notification", "Blaze::UserSessionExtendedDataUpdate", ExtendedDataUpdateNotification(pid, 0, 1)},
		{"b7c_getbasicdata_reply", "Blaze::BattlegroundsAccounts::BGAMetagameGetBasicAccountDataResponse",
			BasicAccountDataReply([]BasicData{{BlazeID: pid, Level: 2, XP: 124, Avatar: 0x1000000A, Flags: 1}})},
		{"b7c_getstatsasync_notification", "Blaze::Stats::KeyScopedStatValues", StatsAsyncNotification("PlayerStats", 1, pid, b7cStatValues())},
	}
}

func TestB7cRepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range b7cPayloads(t) {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// The client's requests as R11 read its senders (tdf_golden.py's decode cases) come out as meant.
func TestB7cClientRequests(t *testing.T) {
	ps := newPipeSession(t)
	defer ps.close()
	body, err := tdf.Decode(ps.request("updateperkpage_request"))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := body.Sub("PAGE")
	if got := DecodePerkPage(page); !reflect.DeepEqual(got, b7cPage1Edited) {
		t.Errorf("updatePerkPage page %+v", got)
	}
	body, _ = tdf.Decode(ps.request("getperkpagedata_request"))
	if u, _ := body.Int("USER"); u != store.FirstPersonaID {
		t.Errorf("getPerkPageData USER %d", u)
	}
	for name, want := range map[string]string{"purchasewithec_request": "268435462", "purchasewithpc_request": "268435467"} {
		body, _ = tdf.Decode(ps.request(name))
		proi, _ := body.Str("PROI")
		vers, _ := body.Str("VERS")
		if cnt, _ := body.Int("CNT"); proi != want || vers != CatalogVersion || cnt != 1 {
			t.Errorf("%s: PROI %q VERS %q CNT %d", name, proi, vers, cnt)
		}
	}
}

// expectError checks the next frame is an error reply with code.
func (ps *pipeSession) expectError(comp, cmd uint16, id uint32, code uint16) {
	ps.t.Helper()
	f := ps.next("error reply")
	if f.Component != comp || f.Command != cmd || f.Type != fire.ErrorReply || f.ID != id || f.Error != code || len(f.Payload) != 0 {
		ps.t.Errorf("got %#x/%#x %s id %d error %#x, want error %#x", f.Component, f.Command, f.Type, f.ID, f.Error, code)
	}
}

func purchaseRequest(proi string, item uint32, ppri int64, vers string, cnt int64) []byte {
	return tdf.NewWriter().Int("CNT", cnt).List("ITMS", tdf.TypeInt, []uint32{item}).Int("PPRI", ppri).String("PROI", proi).
		List("QNTS", tdf.TypeInt, []int64{1}).String("VERS", vers).Bytes()
}

func updatePageRequest(p store.PerkPage) []byte {
	w := tdf.NewWriter()
	w.Struct("PAGE", func(w *tdf.Writer) { perkPage(w, p) })
	return w.Bytes()
}

// Perk pages and purchases with a persistent store and the starter policy: a new account gets its first
// page; buying the fixture's perk shape and gem deducts EC and grants them; the page with both saves;
// buying the loadout page with PC deducts PC and creates page 2, which getPerkPageData then returns; the
// commerce profile counts it all; refusals get the client's error codes; and after a restart (a new
// server on the same file) everything is still there.
func TestPerkPagesAndPurchases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	c := loadFixtureContent(t)
	setup := func(s *Server) {
		st, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s.Store, s.Content = st, c
		s.Policy = Policy{Owned: content.OwnStarter, StartEC: DefaultStartEC, StartPC: DefaultStartPC}
	}
	ps := newPipeSessionWith(t, setup)
	ps.login(0, "extendeddataupdate_notification")
	ps.send(0x81F, CmdBGAGetPerkPageData, 2, ps.request("getperkpagedata_request"))
	ps.expect(0x81F, CmdBGAGetPerkPageData, fire.Reply, 2, "b7c_getperkpagedata_reply")
	// a page with shapes the account doesn't own yet is refused (FAILED)
	ps.send(0x81F, CmdBGAUpdatePerkPage, 3, ps.request("updateperkpage_request"))
	ps.expect(0x81F, CmdBGAUpdatePerkPage, fire.Reply, 3, "b7c_updateperkpage_failed_reply")
	// buy Spark with EC (500), then RedGem (150)
	ps.send(0x820, CmdBGEPurchaseWithEC, 4, ps.request("purchasewithec_request"))
	ps.expect(0x820, CmdBGEPurchaseWithEC, fire.Reply, 4, "b7c_purchasewithec_reply")
	ps.send(0x820, CmdBGEPurchaseWithEC, 5, purchaseRequest("268435463", 0x10000007, 0, "1", 1))
	want := PurchaseWithECReply(Purchase{BlazeID: store.FirstPersonaID, Count: 1, Balance: 9350, Items: []uint32{0x10000007}, Quantity: []uint32{1}})
	if f := ps.next("gem purchase"); hex.EncodeToString(f.Payload) != hex.EncodeToString(want) {
		t.Errorf("gem purchase: % x", f.Payload)
	}
	// now the page saves
	ps.send(0x81F, CmdBGAUpdatePerkPage, 6, ps.request("updateperkpage_request"))
	ps.expect(0x81F, CmdBGAUpdatePerkPage, fire.Reply, 6, "b7c_updateperkpage_reply")
	ps.send(0x81F, CmdBGAValidatePerkPage, 7, ps.request("updateperkpage_request"))
	ps.expect(0x81F, CmdBGAValidatePerkPage, fire.Reply, 7, "b7c_updateperkpage_reply")
	// buy a loadout page with PC (300): page 2
	ps.send(0x820, CmdBGEPurchaseWithPC, 8, ps.request("purchasewithpc_request"))
	ps.expect(0x820, CmdBGEPurchaseWithPC, fire.Reply, 8, "b7c_purchasewithpc_reply")
	ps.send(0x81F, CmdBGAGetPerkPageData, 9, ps.request("getperkpagedata_request"))
	ps.expect(0x81F, CmdBGAGetPerkPageData, fire.Reply, 9, "b7c_getperkpagedata_two_reply")
	ps.send(0x820, CmdBGEGetCommerceProfile, 10, nil)
	ps.expect(0x820, CmdBGEGetCommerceProfile, fire.Reply, 10, "b7c_getcommerceprofile_reply")
	ps.send(0x820, CmdBGEGetBalance, 11, nil)
	if f := ps.next("getBalance"); hex.EncodeToString(f.Payload) != hex.EncodeToString(GetBalanceReply(0, 9350, 700)) {
		t.Errorf("getBalance: % x", f.Payload)
	}
	// refusals
	ps.send(0x820, CmdBGEPurchaseWithPC, 12, purchaseRequest("268435457", 0x10000001, 975, "1", 1)) // a shaper already owned
	ps.expectError(0x820, CmdBGEPurchaseWithPC, 12, ErrBGEInvalidItem)
	ps.send(0x820, CmdBGEPurchaseWithPC, 13, purchaseRequest("268435462", 0x10000006, 0, "1", 1)) // Spark has no PC price
	ps.expectError(0x820, CmdBGEPurchaseWithPC, 13, ErrBGEPaymentMethodUnavailable)
	ps.send(0x820, CmdBGEPurchaseWithEC, 14, purchaseRequest("268435462", 0x10000006, 0, "0", 1)) // old catalog
	ps.expectError(0x820, CmdBGEPurchaseWithEC, 14, ErrBGEBadVersion)
	ps.send(0x820, CmdBGEPurchaseWithEC, 15, purchaseRequest("42", 42, 0, "1", 1)) // no such offer
	ps.expectError(0x820, CmdBGEPurchaseWithEC, 15, ErrBGEProductNotFound)
	ps.send(0x820, CmdBGEPurchaseWithEC, 16, purchaseRequest("268435462", 0x10000006, 0, "1", 2)) // Spark: 1 + 2 > 2
	ps.expectError(0x820, CmdBGEPurchaseWithEC, 16, ErrBGEInvalidItem)
	ps.send(0x820, CmdBGEPurchaseWithPC, 17, purchaseRequest("268435460", 0x10000004, 750, "1", 1)) // a skin: 750 PC > 700
	ps.expectError(0x820, CmdBGEPurchaseWithPC, 17, ErrBGEInsufficientFunds)
	ps.send(0x820, CmdBGEPurchaseWithEC, 18, purchaseRequest("268435467", 0x1000000B, 300, "1", 5)) // 5 x 2000 EC > 9350
	ps.expectError(0x820, CmdBGEPurchaseWithEC, 18, ErrBGEInsufficientFunds)
	// bad pages: an unknown page, a cell off the board, a socket the shape lacks, two gems in one socket
	for i, p := range []store.PerkPage{
		{ID: 9, Name: "x", Version: 1},
		{ID: 1, Name: "x", Version: 1, Shapes: []store.PerkShape{{ID: 0x10000006, Cell: 16}}},
		{ID: 1, Name: "x", Version: 1, Shapes: []store.PerkShape{{ID: 0x10000006, Gems: []store.PerkGem{{ID: 0x10000007, Socket: 2}}}}},
		{ID: 1, Name: "x", Version: 1, Shapes: []store.PerkShape{{ID: 0x10000006, Gems: []store.PerkGem{{ID: 0x10000007}, {ID: 0x10000007}}}}},
		{ID: 1, Name: "x", Version: 0},
		{ID: 1, Name: "x", Version: 1, Shapes: []store.PerkShape{{ID: 0x10000007}}}, // a gem as a shape
	} {
		id := uint32(20 + i)
		ps.send(0x81F, CmdBGAUpdatePerkPage, id, updatePageRequest(p))
		ps.expect(0x81F, CmdBGAUpdatePerkPage, fire.Reply, id, "b7c_updateperkpage_failed_reply")
	}
	log := ps.close()
	for _, want := range []string{
		"[blaze]    account 1000000001: perk pages [1] created\n",
		"(updatePerkPage: page 1 refused: 0x10000006 placed 1 times, owned 0)",
		"[blaze]    account 1000000001: bought Spark (0x10000006) x1 for 500 EC; EC 9500 left\n",
		"[blaze]    account 1000000001: perk page 1 \"Page 1\" stored (version 2, 1 shapes)\n",
		"[blaze]    account 1000000001: bought Page (0x1000000b) x1 for 300 PC; PC 700 left\n",
		"[blaze]    account 1000000001: perk pages [2] created\n",
		"(purchaseWithEC refused: 10000 EC needed, 9350 left)",
		"(purchaseWithPC refused: ember: 1 owned, 1 more would pass the limit 1)",
		"(updatePerkPage: page 1 refused: shape 0 (Spark): no socket 2)",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}

	// restart: the same file, a new server; the pages, items, balances and the profile are kept
	ps = newPipeSessionWith(t, setup)
	ps.login(0, "extendeddataupdate_notification")
	ps.send(0x81F, CmdBGAGetPerkPageData, 2, ps.request("getperkpagedata_request"))
	ps.expect(0x81F, CmdBGAGetPerkPageData, fire.Reply, 2, "b7c_getperkpagedata_two_reply")
	ps.send(0x820, CmdBGEGetCommerceProfile, 3, nil)
	ps.expect(0x820, CmdBGEGetCommerceProfile, fire.Reply, 3, "b7c_getcommerceprofile_reply")
	ps.send(0x81F, CmdBGAGetCompleteData, 4, nil)
	body, err := tdf.Decode(ps.next("getCompleteData").Payload)
	if err != nil {
		t.Fatal(err)
	}
	lf, _ := body.Get("LIST")
	acct := lf.Value.(*tdf.List).Items[0].(*tdf.Struct)
	if pages := structs(acct, "PERK"); len(pages) != 2 || !reflect.DeepEqual(DecodePerkPage(pages[0]), b7cPage1Edited) {
		t.Errorf("PERK: %d pages", len(pages))
	}
	items := map[int64]int64{}
	for _, it := range structs(acct, "ITEM") {
		id, _ := it.Int("ITID")
		n, _ := it.Int("CONT")
		items[id] = n
	}
	if items[0x10000006] != 1 || items[0x10000007] != 1 || items[0x1000000B] != 2 {
		t.Errorf("ITEM: %v", items)
	}
	if log := ps.close(); strings.Contains(log, "created") {
		t.Errorf("pages created again:\n%s", log)
	}
}

// Purchases need a login and content.
func TestPurchaseNeedsLoginAndContent(t *testing.T) {
	ps := newPipeSession(t)
	ps.send(0x820, CmdBGEPurchaseWithEC, 0, purchaseRequest("268435462", 0x10000006, 0, "1", 1))
	ps.expectError(0x820, CmdBGEPurchaseWithEC, 0, ErrBGENotAuthorized)
	ps.login(1, "extendeddataupdate_notification")
	ps.send(0x820, CmdBGEPurchaseWithPC, 3, purchaseRequest("268435462", 0x10000006, 0, "1", 1))
	ps.expectError(0x820, CmdBGEPurchaseWithPC, 3, ErrBGENoCatalog)
	ps.send(0x81F, CmdBGAUpdatePerkPage, 4, updatePageRequest(b7cPage1))
	ps.expect(0x81F, CmdBGAUpdatePerkPage, fire.Reply, 4, "b7c_updateperkpage_failed_reply") // no pages without content
	ps.close()
}

// The test aid --test-spent-elsewhere (B7c-live): a purchase the starting balance covers finds the
// balance spent elsewhere and is refused for insufficient funds; the balance stays 0 afterwards, the
// other currency is untouched, nothing is granted.
func TestPurchaseSpentElsewhere(t *testing.T) {
	c := loadFixtureContent(t)
	ps := newPipeSessionWith(t, func(s *Server) {
		s.Content = c
		s.Policy = Policy{Owned: content.OwnStarter, StartEC: DefaultStartEC, StartPC: DefaultStartPC, SpentElsewhere: true}
	})
	ps.login(0, "extendeddataupdate_notification")
	ps.send(0x820, CmdBGEPurchaseWithEC, 2, ps.request("purchasewithec_request")) // Spark, 500 of 10000 EC
	ps.expectError(0x820, CmdBGEPurchaseWithEC, 2, ErrBGEInsufficientFunds)
	ps.send(0x820, CmdBGEGetBalance, 3, nil)
	if f := ps.next("getBalance"); hex.EncodeToString(f.Payload) != hex.EncodeToString(GetBalanceReply(0, 0, DefaultStartPC)) {
		t.Errorf("getBalance: % x", f.Payload)
	}
	log := ps.close()
	for _, want := range []string{
		"(test --test-spent-elsewhere: 10000 EC spent elsewhere before the check)",
		"(purchaseWithEC refused: 500 EC needed, 0 left)",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "bought") {
		t.Errorf("a purchase went through:\n%s", log)
	}
}

// A hosted practice game with content: finishGame stores PlayerStats, XP and level, the rewards (FTUE
// bag and loot: both items already owned, so crafting currency) and the FTUE flag; the host gets the
// rewards back; the player's main menu gets its wins at once; and the profile, the basic data and the
// balance show it.
func TestHostedGameProgression(t *testing.T) {
	rewards := make(chan gmshost.PlayerRewards, 1)
	hs := newHostedSessionFull(t, "", "", nil, func(s *Server) {
		s.Content = loadFixtureContent(t)
		s.Rand = fixedRand(0.5)
	}, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		for _, m := range []gmshost.Message{gmshost.AddPlayer{User: store.FirstPersonaID},
			gmshost.GMSUpdate{Phase: gmshost.PhaseInGame},
			gmshost.PlayerFinish{User: store.FirstPersonaID, Stats: b7cGameStats},
			gmshost.FinishGame{WinnerTeam: 1, DurationMs: 720000, Reason: "guardian"}} {
			if err := h.Send(m); err != nil {
				return err
			}
		}
		m, err := h.Recv(5 * time.Second)
		if err != nil {
			return err
		}
		rewards <- m.(gmshost.PlayerRewards)
		return nil
	})
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	hs.hostGot()
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification")
	hs.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(1, GameStateInGame))
	hs.expect(0x7802, NotifyUserSessionExtendedDataUpdate, fire.Notification, 0, "b7c_extendeddataupdate_notification")
	hs.expect(4, NotifyGameStateChangeID, fire.Notification, 0, "notifygamestatechange_notification")
	hs.expect(4, NotifyPlayerRemovedID, fire.Notification, 0, "notifyplayerremoved_notification")
	hs.waitIdle()
	select {
	case r := <-rewards:
		want := gmshost.PlayerRewards{User: store.FirstPersonaID, Rewards: gmshost.Rewards{LootBag: content.AssetID("Silverish.LootBag"),
			Destiny: 12, CC: 65, FTUE: true, BaseRoll: 218, WinPoints: 200, WinAltPoints: 10, FirstWinPoints: 100,
			Items: []gmshost.RewardItem{{CatalogID: 0x10000006, Duplicate: true, Alternate: 50},
				{CatalogID: 0x10000007, Duplicate: true, Alternate: 15}}}}
		if !reflect.DeepEqual(r, want) {
			t.Errorf("playerRewards\n got %+v\nwant %+v", r, want)
		}
	default:
		t.Error("the host got no playerRewards")
	}
	hs.send(0x81F, 0x73, 3, tdf.NewWriter().List("BZID", tdf.TypeInt, []int64{store.FirstPersonaID}).Bytes())
	hs.expect(0x81F, 0x73, fire.Reply, 3, "b7c_getbasicdata_reply")
	hs.send(7, 0x10, 4, tdf.NewWriter().String("NAME", "PlayerStats").Int("VID", 1).Bytes())
	hs.expect(7, 0x10, fire.Reply, 4, "")
	hs.expect(7, 0x32, fire.Notification, 0, "b7c_getstatsasync_notification")
	hs.send(0x820, CmdBGEGetBalance, 5, nil)
	if f := hs.next("getBalance"); hex.EncodeToString(f.Payload) != hex.EncodeToString(GetBalanceReply(65, DefaultStartEC+12, DefaultStartPC)) {
		t.Errorf("getBalance: % x", f.Payload)
	}
	acct, _ := hs.s.Store.Account(store.FirstUserID)
	if acct.FirstWinDay != time.Unix(testTime, 0).UTC().Format("2006-01-02") || acct.Flags&store.FlagFTUEDone == 0 {
		t.Errorf("account: first win %q, flags %#x", acct.FirstWinDay, acct.Flags)
	}
	hs.close()
	log := hs.log.String()
	for _, want := range []string{
		"[blaze]    game 1: player 2000000001 result map[assists:2 bestStreak:3 deaths:1 doubleKills:1 kills:3] (stored at finishGame)\n",
		"[blaze]    account 1000000001: game 1 won, +124 XP (level 2), 1 wins; bag \"Silverish\", 12 Destiny, items [0x10000006(duplicate, 50 CC) 0x10000007(duplicate, 15 CC)], 65 CC, FTUE true\n",
		"[blaze]    account 1000000001: FTUE done (practice game 1 finished)\n",
		"[host] match 1 -> {\"t\":\"playerRewards\",\"user\":2000000001,",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// A hosted PvP game with two players: both get the game counted (the winner a win, the loser not), the
// host gets both rewards, and the winner's main menu its first win.
func TestHostedPvPProgression(t *testing.T) {
	got := make(chan gmshost.PlayerRewards, 2)
	var hosts *gmshost.Manager
	ts := newTCPServer(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 1, MinPlayers: 2})
		s.Content = loadFixtureContent(t)
		s.Rand = fixedRand(0.5)
		l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
			gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
				for _, r := range cg.Roster {
					h.Send(gmshost.AddPlayer{User: r.User, Index: r.Index})
					h.Send(gmshost.PlayerFinish{User: r.User, Stats: map[string]int64{progress.StatKills: int64(r.Index + 1)}})
				}
				if err := h.Send(gmshost.FinishGame{WinnerTeam: 2, DurationMs: 1800000, Reason: "guardian"}); err != nil {
					return err
				}
				for range cg.Roster {
					m, err := h.Recv(5 * time.Second)
					if err != nil {
						return err
					}
					got <- m.(gmshost.PlayerRewards)
				}
				return nil
			})
		}}
		hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+4, GMSPort+4),
			ExitTimeout: time.Second})
		if err := hosts.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		s.Hosts = hosts
	})
	t.Cleanup(hosts.Close)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	a.queue(0)
	b.queue(0)
	for _, c := range []*tcpClient{a, b} {
		c.note(ComponentGameManager, NotifyGameSetupID)
		ext := decode(t, c.note(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate))
		data, _ := ext.Sub("DATA")
		_, hasWins := data.Get("DMAP")
		if hasWins != (c == b) {
			t.Errorf("%s: DMAP present %v", c.name, hasWins)
		}
		c.note(ComponentGameManager, NotifyPlayerRemovedID)
	}
	for deadline := time.Now().Add(5 * time.Second); hosts.Running() > 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("match host still running")
		}
	}
	users := map[int64]gmshost.Rewards{}
	for range 2 {
		select {
		case r := <-got:
			users[r.User] = r.Rewards
		case <-time.After(5 * time.Second):
			t.Fatal("rewards missing")
		}
	}
	// 30 minutes: alpha lost (218 + 27 = 245: Copper), bravo won (+200 +100 first win = 545: Silverish, two drops)
	if r := users[a.id]; r.LootBag != content.AssetID("Copper.LootBag") || r.FirstWinPoints != 0 || len(r.Items) != 0 || r.FTUE {
		t.Errorf("alpha's rewards %+v", r)
	}
	if r := users[b.id]; r.LootBag != content.AssetID("Silverish.LootBag") || r.FirstWinPoints != 100 || len(r.Items) != 2 {
		t.Errorf("bravo's rewards %+v", r)
	}
	for _, c := range []struct {
		uid          int64
		wins, played string
		kills        string
		xp           uint32
	}{{store.FirstUserID, "0", "1", "1", 50 + 60}, {store.FirstUserID + 1, "1", "1", "2", 100 + 60}} {
		acct, _ := ts.s.Store.Account(c.uid)
		ps := acct.Stats[progress.PlayerStats]
		if ps["WINS"] != c.wins || ps["PLAYED"] != c.played || ps["KILLS"] != c.kills || acct.XP != c.xp ||
			acct.Flags&store.FlagFTUEDone != 0 {
			t.Errorf("account %d: stats %v, XP %d, flags %#x", c.uid, ps, acct.XP, acct.Flags)
		}
	}
}

// D8c: a matchmade game (GameFlags "1", whose post-game has the karma vote) holds the rewards for the vote:
// finishGame stores everything else and pushes the wins, but sends no playerRewards; the host's playerKarma
// brings the loser's votes, and its bag is decided with the karma points in the score (roll 0.8: 379, a lost
// 20-minute game 18: 397, Copper without karma; one enemy vote, 10 points: 407, Silverish) and sent with the
// karma. The winner, for whom the host reports no karma, gets its rewards without karma once the host has
// ended (379 + 200 + 18 + 100 first win = 697, Silverish), and no playerRewards (the host is gone).
func TestHostedMatchmadeKarma(t *testing.T) {
	got := make(chan gmshost.Message, 8)
	var hosts *gmshost.Manager
	ts := newTCPServer(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 1, MinPlayers: 2})
		s.Content = loadFixtureContent(t)
		s.Rand = fixedRand(0.8)
		l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
			gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
				got <- cg
				var loser int64
				for _, r := range cg.Roster {
					h.Send(gmshost.AddPlayer{User: r.User, Index: r.Index})
					h.Send(gmshost.PlayerFinish{User: r.User, Stats: map[string]int64{progress.StatKills: int64(r.Index + 1)}})
					if r.Team == 1 {
						loser = r.User
					}
				}
				if err := h.Send(gmshost.FinishGame{WinnerTeam: 2, DurationMs: 1200000, Reason: "guardian"}); err != nil {
					return err
				}
				// after the post-game's vote: the loser got one vote from the enemy; nothing for the winner
				if err := h.Send(gmshost.PlayerKarma{User: loser, Enemy: 1}); err != nil {
					return err
				}
				for {
					m, err := h.Recv(time.Second)
					if err != nil {
						return nil // nothing more comes: the host ends
					}
					got <- m
				}
			})
		}}
		hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+4, GMSPort+4),
			ExitTimeout: time.Second})
		if err := hosts.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		s.Hosts = hosts
	})
	t.Cleanup(hosts.Close)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	for _, c := range []*tcpClient{a, b} {
		id := c.send(ComponentGameManager, CmdGameManagerStartMatchmaking,
			startMatchmakingRequestAttrs(0, tdf.KV("GameFlags", "1"), tdf.KV("GameType", "1")))
		if f := c.reply(ComponentGameManager, CmdGameManagerStartMatchmaking, id); f.Type != fire.Reply {
			t.Fatalf("%s: startMatchmaking %s", c.name, f.Type)
		}
	}
	for _, c := range []*tcpClient{a, b} {
		c.note(ComponentGameManager, NotifyGameSetupID)
		c.note(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate) // the wins: at finishGame, as before
		c.note(ComponentGameManager, NotifyPlayerRemovedID)
	}
	for deadline := time.Now().Add(5 * time.Second); hosts.Running() > 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("match host still running")
		}
	}
	cg := (<-got).(gmshost.CreateGame)
	if cg.Mode != gmshost.ModePvP || cg.Descriptors != 1 {
		t.Errorf("createGame %+v", cg)
	}
	var rewards []gmshost.PlayerRewards
	for len(got) > 0 {
		if r, ok := (<-got).(gmshost.PlayerRewards); ok {
			rewards = append(rewards, r)
		}
	}
	dup := gmshost.RewardItem{CatalogID: 0x10000007, Duplicate: true, Alternate: 15}
	want := gmshost.PlayerRewards{User: a.id, Rewards: gmshost.Rewards{LootBag: content.AssetID("Silverish.LootBag"),
		CC: 30, BaseRoll: 379, WinAltPoints: 18, Items: []gmshost.RewardItem{dup, dup},
		Karma: &gmshost.Karma{Points: 10, Enemy: 1}}}
	if len(rewards) != 1 || !reflect.DeepEqual(rewards[0], want) {
		t.Errorf("playerRewards %+v\nwant one: %+v", rewards, want)
	}
	for _, c := range []struct {
		uid    int64
		wins   string
		cc     uint32
		winDay bool
	}{{store.FirstUserID, "0", 30, false}, {store.FirstUserID + 1, "1", 30, true}} {
		acct, _ := ts.s.Store.Account(c.uid)
		ps := acct.Stats[progress.PlayerStats]
		if ps["WINS"] != c.wins || ps["PLAYED"] != "1" || acct.CC != c.cc || (acct.FirstWinDay != "") != c.winDay {
			t.Errorf("account %d: stats %v, CC %d, first win %q", c.uid, ps, acct.CC, acct.FirstWinDay)
		}
	}
	// the line the host got is the example in docs/data/host_control_examples.jsonl that gms/core/control's tests
	// decode and take (dg_control_tests: the matchmade round trip)
	wire := `{"t":"playerRewards","user":2000000001,"rewards":{"lootBag":2079898500,"destiny":0,"cc":30,"items":[` +
		`{"catalogId":268435463,"duplicate":true,"alternate":15},{"catalogId":268435463,"duplicate":true,"alternate":15}],` +
		`"ftue":false,"baseRoll":379,"winPoints":0,"winAltPoints":18,"firstWinPoints":0,` +
		`"karma":{"points":10,"party":0,"team":0,"enemy":1}}}`
	examples, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "data", "host_control_examples.jsonl"))
	if lines := strings.ReplaceAll(string(examples), "\r\n", "\n"); err != nil || !strings.Contains(lines, "\n"+wire+"\n") {
		t.Errorf("host_control_examples.jsonl lacks the line (%v): %s", err, wire)
	}
	log := ts.log.String()
	for _, want := range []string{
		"[host] match 1 -> " + wire + "\n",
		"account 1000000001: game 1 lost, +90 XP (level 1), 0 wins; rewards wait for the karma vote (playerKarma)\n",
		"account 1000000002: game 1 won, +140 XP (level 2), 1 wins; rewards wait for the karma vote (playerKarma)\n",
		"game 1: player 2000000001 karma votes: party 0, team 0, enemy 1\n",
		"account 1000000001: game 1 rewards after the karma vote: bag \"Silverish\", 0 Destiny, items [0x10000007(duplicate, 15 CC) " +
			"0x10000007(duplicate, 15 CC)], 30 CC, FTUE false, score 407 with karma 10 (votes: party 0, team 0, enemy 1)\n",
		"account 1000000002: game 1 rewards after the host's end without playerKarma (no karma): bag \"Silverish\", 0 Destiny, " +
			"items [0x10000007(duplicate, 15 CC) 0x10000007(duplicate, 15 CC)], 30 CC, FTUE false\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}
