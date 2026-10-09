package blaze

import (
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/content"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// b7aFixture is the account tdf_golden.py writes the b7a_* cases for: the synthetic content fixture
// (server/testdata/assetdata) granted under the default policy (everything owned, EC 10000, PC 1000,
// the lowest-catalogID avatar), with the FTUE gate passed (Policy.FTUEDone), two settings and the news
// overlay's user info attribute.
type b7aFixture struct {
	content *content.Content
	acct    store.Account
	server  *Server
}

func loadFixtureContent(t testing.TB) *content.Content {
	t.Helper()
	c, err := content.Build(filepath.Join(golden.Dir(), "assetdata"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newB7aFixture(t testing.TB) b7aFixture {
	c := loadFixtureContent(t)
	items, avatar, err := c.Grant(content.OwnAll)
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := testAccount()
	acct.Level, acct.Avatar, acct.EC, acct.PC, acct.Items, acct.Granted = 1, avatar, DefaultStartEC, DefaultStartPC, items, true
	acct.Settings = map[string]string{"Video.Width": "1280", "Audio.Master": "0.5"}
	acct.UserInfo = 1 << 62
	s := &Server{Content: c, Policy: DefaultPolicy()}
	s.Policy.FTUEDone = true
	return b7aFixture{content: c, acct: acct, server: s}
}

func (f b7aFixture) completeData() []byte {
	return CompleteAccountDataReply(f.server.basicDataOf(f.acct, store.FirstPersonaID), InventoryOf(f.acct, f.content), nil)
}

func (f b7aFixture) basicData() []byte {
	return BasicAccountDataReply([]BasicData{f.server.basicDataOf(f.acct, store.FirstPersonaID)})
}

func (f b7aFixture) statsNotification() []byte {
	values := make([]string, len(StatGroups["PlayerStats"]))
	for i := range values {
		values[i] = "0"
	}
	return StatsAsyncNotification("PlayerStats", 1, store.FirstPersonaID, values)
}

// The B7a replies against tdf_golden.py's independent encoding from the member tables.
func TestB7aRepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	f := newB7aFixture(t)
	for _, c := range []struct {
		name string
		got  []byte
	}{
		{"b7a_getcompletedata_reply", f.completeData()},
		{"b7a_getbasicdata_reply", f.basicData()},
		{"b7a_getbalance_reply", GetBalanceReply(f.acct.CC, f.acct.EC, f.acct.PC)},
		{"b7a_getcatalog_reply", GetCatalogReply(CatalogVersion, OffersOf(f.content))},
		{"b7a_getgameitems_reply", GetGameItemsReply(GameItemsOf(f.content, 1))},
		{"b7a_getusersettings_reply", GetUserSettingsReply(f.acct.Settings)},
		{"b7a_getstatgroup_reply", StatGroupReply("PlayerStats", StatGroups["PlayerStats"])},
		{"b7a_getstatsasync_notification", f.statsNotification()},
		{"b7a_extendeddataupdate_notification", ExtendedDataUpdateNotification(store.FirstPersonaID, f.acct.UserInfo, 0)},
	} {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// The client keys each offer by FNV-1(PUII) and sends PUII back as a purchase's PROI (R11,
// docs/ACCOUNT_CONTENT.md *Store offers*), so every offer needs its own non-empty PUII. The loadout page
// is sold although it is hidden, since buying a perk page needs an offer for it.
func TestCatalogOfferKeys(t *testing.T) {
	f := newB7aFixture(t)
	body, err := tdf.Decode(GetCatalogReply(CatalogVersion, OffersOf(f.content)))
	if err != nil {
		t.Fatal(err)
	}
	cata, ok := body.Get("CATA")
	if !ok {
		t.Fatal("no CATA")
	}
	keys := map[string]bool{}
	page := false
	for _, v := range cata.Value.(*tdf.List).Items {
		o := v.(*tdf.Struct)
		puii, _ := o.Str("PUII")
		puid, _ := o.Str("PUID")
		if puii == "" || keys[puii] || puii != puid {
			t.Errorf("offer PUII %q (PUID %q): empty, duplicate or not the PUID", puii, puid)
		}
		keys[puii] = true
		if items, ok := o.Get("ITMI"); ok {
			for _, id := range items.Value.(*tdf.List).Items {
				page = page || id.(int64) == 0x1000000B
			}
		}
	}
	if len(keys) != 6 || !page {
		t.Errorf("%d offers, loadout page sold: %v", len(keys), page)
	}
}

// login runs login and loginPersona on a pipe session and checks the extended data's golden case.
func (ps *pipeSession) login(firstID uint32, extended string) {
	ps.send(1, 0x28, firstID, ps.request("login_request"))
	ps.next("login reply")
	ps.send(1, 0x6E, firstID+1, ps.request("loginpersona_request"))
	ps.next("loginPersona reply")
	ps.next("UserAuthenticated")
	ps.expect(0x7802, 1, fire.Notification, 0, extended)
}

// A whole content session with a persistent store: the first login grants the content, the meta
// replies carry it, the client's changes (settings, user info attribute, avatar) are stored, and after
// a restart (a new server on the same file) the account comes back as it was, without a second grant.
func TestPlayerContentSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	c := loadFixtureContent(t)
	setup := func(s *Server) {
		st, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s.Store, s.Content = st, c
		s.Policy.FTUEDone = true
	}

	ps := newPipeSessionWith(t, setup)
	ps.login(0, "extendeddataupdate_notification") // UATT 0
	ps.send(0x81F, 0x76, 2, nil)
	ps.expect(0x81F, 0x76, fire.Reply, 2, "b7c_getcompletedata_reply")
	ps.send(0x81F, 0x73, 3, tdf.NewWriter().List("BZID", tdf.TypeInt, []int64{store.FirstPersonaID}).Bytes())
	ps.expect(0x81F, 0x73, fire.Reply, 3, "b7a_getbasicdata_reply")
	ps.send(0x820, 0x08, 4, nil)
	ps.expect(0x820, 0x08, fire.Reply, 4, "b7a_getbalance_reply")
	ps.send(0x820, 0x02, 5, nil)
	ps.expect(0x820, 0x02, fire.Reply, 5, "b7a_getcatalog_reply")
	ps.send(0x820, 0x0F, 6, nil)
	ps.expect(0x820, 0x0F, fire.Reply, 6, "b7c_getgameitems_reply")
	ps.send(0x81F, 0x8A, 7, nil)
	ps.expect(0x81F, 0x8A, fire.Reply, 7, "") // no settings yet
	ps.send(0x81F, 0x8B, 8, ps.request("saveusersettings_request"))
	ps.expect(0x81F, 0x8B, fire.Reply, 8, "")
	ps.send(0x81F, 0x8A, 9, nil)
	ps.expect(0x81F, 0x8A, fire.Reply, 9, "b7a_getusersettings_reply")
	ps.send(0x7802, 0x1A, 10, ps.request("setuserinfoattribute_request"))
	ps.expect(0x7802, 0x1A, fire.Reply, 10, "")
	ps.expect(0x7802, 1, fire.Notification, 0, "b7a_extendeddataupdate_notification") // B6: UATT 1<<62 at once
	ps.send(7, 0x04, 11, tdf.NewWriter().String("NAME", "PlayerStats").Bytes())
	ps.expect(7, 0x04, fire.Reply, 11, "b7a_getstatgroup_reply")
	ps.send(7, 0x04, 12, tdf.NewWriter().String("NAME", "ShaperStats").Bytes())
	ps.next("ShaperStats group")
	ps.send(7, 0x10, 13, tdf.NewWriter().String("NAME", "PlayerStats").Int("VID", 1).Bytes())
	ps.expect(7, 0x10, fire.Reply, 13, "")
	ps.expect(7, 0x32, fire.Notification, 0, "b7a_getstatsasync_notification")
	ps.send(7, 0x10, 14, tdf.NewWriter().String("NAME", "ShaperStats").Int("VID", 2).Bytes())
	ps.expect(7, 0x10, fire.Reply, 14, "")
	if body, err := tdf.Decode(ps.next("ShaperStats values").Payload); err != nil {
		t.Error(err)
	} else if _, has := body.Get("STS"); has {
		t.Errorf("ShaperStats has values")
	} else if g, _ := body.Str("GRNM"); g != "ShaperStats" {
		t.Errorf("GRNM %q", g)
	}
	log := ps.close()
	for _, want := range []string{
		"[blaze]    account 1000000001: granted 9 items (all), avatar 0x1000000a, EC 10000, PC 1000\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}

	// restart: the same file, a new server
	ps = newPipeSessionWith(t, setup)
	ps.login(0, "b7a_extendeddataupdate_notification") // UATT 1<<62 from setUserInfoAttribute
	ps.send(0x81F, 0x8A, 2, nil)
	ps.expect(0x81F, 0x8A, fire.Reply, 2, "b7a_getusersettings_reply")
	ps.send(0x820, 0x08, 3, nil)
	ps.expect(0x820, 0x08, fire.Reply, 3, "b7a_getbalance_reply") // no second grant
	ps.send(0x81F, 0x76, 4, nil)
	ps.expect(0x81F, 0x76, fire.Reply, 4, "b7c_getcompletedata_reply")
	// setAvatar: an id that isn't an avatar item is stored anyway (logged), and getBasicData shows it
	ps.send(0x81F, 0x7F, 5, tdf.NewWriter().Int("AVTR", 0x10000001).Bytes())
	ps.expect(0x81F, 0x7F, fire.Reply, 5, "")
	ps.send(0x81F, 0x73, 6, tdf.NewWriter().List("BZID", tdf.TypeInt, []int64{store.FirstPersonaID}).Bytes())
	want := BasicAccountDataReply([]BasicData{{BlazeID: store.FirstPersonaID, Level: 1, Avatar: 0x10000001, Flags: 1}})
	if f := ps.next("getBasicData"); hex.EncodeToString(f.Payload) != hex.EncodeToString(want) {
		t.Errorf("getBasicData after setAvatar: % x", f.Payload)
	}
	log = ps.close()
	if strings.Contains(log, "granted") || !strings.Contains(log, "(setAvatar: 0x10000001 is not a collection avatar, presumably a built-in one; stored)") {
		t.Errorf("log:\n%s", log)
	}
	st, _ := store.Open(path)
	if a, ok := st.Account(store.FirstUserID); !ok || a.Avatar != 0x10000001 || a.UserInfo != 1<<62 || a.EC != DefaultStartEC {
		t.Errorf("stored account: %+v", a)
	}
}

// An account made while the server had no content gets it at its first login with content; until
// then the replies are B5's.
func TestGrantAtLaterLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	open := func(s *Server) {
		st, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s.Store = st
	}
	ps := newPipeSessionWith(t, open)
	ps.login(0, "extendeddataupdate_notification")
	ps.send(0x81F, 0x76, 2, nil)
	ps.expect(0x81F, 0x76, fire.Reply, 2, "getcompletedata_reply")
	ps.send(0x820, 0x08, 3, nil)
	ps.expect(0x820, 0x08, fire.Reply, 3, "getbalance_reply")
	ps.send(0x820, 0x02, 4, nil)
	ps.expect(0x820, 0x02, fire.Reply, 4, "getcatalog_reply")
	ps.send(0x820, 0x0F, 5, nil)
	ps.expect(0x820, 0x0F, fire.Reply, 5, "")
	ps.close()

	c := loadFixtureContent(t)
	ps = newPipeSessionWith(t, func(s *Server) {
		open(s)
		s.Content = c
		s.Policy.FTUEDone = true
	})
	ps.login(0, "extendeddataupdate_notification")
	ps.send(0x81F, 0x76, 2, nil)
	ps.expect(0x81F, 0x76, fire.Reply, 2, "b7c_getcompletedata_reply")
	if log := ps.close(); !strings.Contains(log, "granted 9 items (all)") {
		t.Errorf("log:\n%s", log)
	}
}

// The starter policy and the closed FTUE gate (the default): FLAG stays 0 for a new account.
func TestStarterPolicyAndFTUEGate(t *testing.T) {
	c := loadFixtureContent(t)
	ps := newPipeSessionWith(t, func(s *Server) {
		s.Content = c
		s.Policy = Policy{Owned: content.OwnStarter, StartEC: 5, StartPC: 0}
	})
	ps.login(0, "extendeddataupdate_notification")
	ps.send(0x81F, 0x76, 2, nil)
	starter := []InventoryItem{{0x10000001, content.ItemShaper, 1}, {0x10000002, content.ItemShaper, 1},
		{0x10000008, content.ItemWardSkin, 1}, {0x10000009, content.ItemAnnouncerPack, 1},
		{0x1000000A, content.ItemPlayerAvatar, 1}, {0x1000000B, content.ItemLoadoutPage, 1}}
	want := CompleteAccountDataReply(BasicData{BlazeID: store.FirstPersonaID, Level: 1, Avatar: 0x1000000A}, starter, []store.PerkPage{b7cPage1})
	if f := ps.next("getCompleteData"); hex.EncodeToString(f.Payload) != hex.EncodeToString(want) {
		t.Errorf("getCompleteData: % x", f.Payload)
	}
	ps.send(0x820, 0x08, 3, nil)
	if f := ps.next("getBalance"); hex.EncodeToString(f.Payload) != hex.EncodeToString(GetBalanceReply(0, 5, 0)) {
		t.Errorf("getBalance: % x", f.Payload)
	}
	ps.close()
}

// Requests that change the account do nothing without a login (logged).
func TestContentChangesNeedLogin(t *testing.T) {
	ps := newPipeSession(t)
	ps.send(0x81F, 0x8B, 0, ps.request("saveusersettings_request"))
	ps.expect(0x81F, 0x8B, fire.Reply, 0, "")
	ps.send(0x81F, 0x7F, 1, tdf.NewWriter().Int("AVTR", 1).Bytes())
	ps.expect(0x81F, 0x7F, fire.Reply, 1, "")
	ps.send(0x820, 0x08, 2, nil)
	ps.expect(0x820, 0x08, fire.Reply, 2, "getbalance_reply")
	log := ps.close()
	for _, want := range []string{"(saveUserSettings before login; nothing stored)", "(setAvatar before login; nothing stored)"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestUserInfoAttribute(t *testing.T) {
	for _, c := range []struct{ old, attv, mask, want uint64 }{
		{0, 1 << 62, 0, 1 << 62},          // the client's request (b8_run3)
		{0xF0, 0x0F, 0, 0x0F},             // MASK 0 replaces
		{0xF0, 0x0F, 0x0C, 0xFC},          // only the MASK bits change
		{1 << 62, 0, 1 << 62, 0},          // clear one bit
		{1<<62 | 1, 1 << 1, 2, 1<<62 | 3}, // set one bit
	} {
		if got := UserInfoAttribute(c.old, c.attv, c.mask); got != c.want {
			t.Errorf("UserInfoAttribute(%#x, %#x, %#x) = %#x, want %#x", c.old, c.attv, c.mask, got, c.want)
		}
	}
}
