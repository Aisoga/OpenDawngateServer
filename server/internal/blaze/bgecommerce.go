package blaze

import (
	"strconv"

	"dawngate/server/internal/content"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// BGEcommerce component (0x0820), Dawngate's own (B4): store, currencies and owned game items.
const (
	ComponentBGEcommerce     = 0x0820
	CmdBGEGetCatalog         = 0x02
	CmdBGEGetCommerceProfile = 0x06
	CmdBGEGetBalance         = 0x08
	CmdBGEGetGameItems       = 0x0F
)

func registerBGEcommerce(s *Server) {
	s.Register(ComponentBGEcommerce, CmdBGEGetCatalog, "getCatalog", func(r *Request) []byte {
		if r.Server.Content.Empty() {
			return GetCatalogReply("", nil)
		}
		return GetCatalogReply(CatalogVersion, OffersOf(r.Server.Content))
	})
	s.Register(ComponentBGEcommerce, CmdBGEGetCommerceProfile, "getCommerceProfile", func(r *Request) []byte {
		acct, _ := r.account()
		return GetCommerceProfileReply(acct.Commerce)
	})
	s.Register(ComponentBGEcommerce, CmdBGEGetBalance, "getBalance", func(r *Request) []byte {
		acct, _ := r.account()
		return GetBalanceReply(acct.CC, acct.EC, acct.PC)
	})
	s.Register(ComponentBGEcommerce, CmdBGEGetGameItems, "getGameItems", func(r *Request) []byte {
		return GetGameItemsReply(GameItemsOf(r.Server.Content, r.Server.pageLimit()))
	})
	registerPurchases(s)
}

// CatalogVersion is the store catalog's version (GetCatalogResponse VERS; purchase requests send a
// VERS back). Ours is fixed.
const CatalogVersion = "1"

// Offer is one Blaze::BGEcommerce::PurchasableUnitTdf: one item for earned and/or paid currency.
type Offer struct {
	CatalogID uint32
	EC, PC    uint32 // prices; 0 = not for this currency
}

// offerPrices are our own store prices by BGEcommerce item type (made up for the emulator: the
// original prices came from EA's commerce backend and aren't in the client's data).
var offerPrices = map[int]struct{ EC, PC uint32 }{
	content.ItemShaper:        {4800, 975},
	content.ItemShaperSkin:    {0, 750},
	content.ItemPerkShape:     {500, 0},
	content.ItemPerkGem:       {150, 0},
	content.ItemWardSkin:      {0, 400},
	content.ItemAnnouncerPack: {0, 500},
	content.ItemLoadoutPage:   {2000, 300},
}

// OffersOf is the store: one offer per item whose specialStatus is none (always-owned and hidden
// items aren't sold) and that has a price for its type, testing-only shapers and skins left out; by
// catalogID. The loadout page is sold although its specialStatus is hide (R11): "hide" keeps it out of
// the collection (Collection_IsVisible 0x00878970), but buying a perk page needs an offer for it
// (PerksUI OnSelectLoadout 0x00484440 asserts "pBestOffer" for the LoadoutPage item's catalogID).
func OffersOf(c *content.Content) []Offer {
	var offers []Offer
	if c == nil {
		return nil
	}
	for _, it := range c.Items {
		p, ok := offerPrices[it.ItemType]
		sold := it.Status == content.StatusNone || it.ItemType == content.ItemLoadoutPage
		if !ok || !sold || it.TestingOnly {
			continue
		}
		offers = append(offers, Offer{CatalogID: it.CatalogID, EC: p.EC, PC: p.PC})
	}
	return offers
}

// GetCatalogReply is BGEcommerce::getCatalog -> Blaze::BGEcommerce::GetCatalogResponse (member table
// 0x012F3444: CATA list of PurchasableUnitTdf +0x08, VERS string +0x38; RPC job 0x012B5648 at
// 0x00AB77CF). Without content: VERS ” and no CATA (B5).
//
// Each offer is a PurchasableUnitTdf (0x012F2F08) with only these members (the rest keep their
// defaults): ERNC and PAIC the earned- and paid-currency PurchaseOption (0x012F2EC8: AVIL available,
// PAIC price), FLGS the offer flags (enum table .rdata 0x012F2EE8: BASE_FLAG 1, NEW_FLAG 2,
// SALE_FLAG 4), ITMI/ITMC the catalogIDs and counts in the offer, PUID and PUII both the catalogID in
// decimal.
//
// The client's reader (R11, docs/ACCOUNT_CONTENT.md *Store offers*): the catalog callback 0x00ABAE50
// hands each offer to 0x00ABAAD0, which keys the offer by FNV-1(lower-cased PUII) and indexes it under
// every ITMI catalogID. The purchase flow looks the offer up by that key and sends PUII back as the
// request's PROI, so PUII must be unique per offer: with no PUII every offer had the key FNV-1("") and
// any purchase picked the last offer. PUID is only copied (no reader found).
func GetCatalogReply(version string, offers []Offer) []byte {
	w := tdf.NewWriter()
	if len(offers) > 0 {
		list := make([]*tdf.Writer, len(offers))
		for i, o := range offers {
			ow := tdf.NewWriter()
			ow.Struct("ERNC", purchaseOption(o.EC))
			ow.Int("FLGS", OfferBaseFlag)
			ow.List("ITMC", tdf.TypeInt, []int64{1})
			ow.List("ITMI", tdf.TypeInt, []int64{int64(o.CatalogID)})
			ow.Struct("PAIC", purchaseOption(o.PC))
			id := strconv.FormatUint(uint64(o.CatalogID), 10)
			ow.String("PUID", id)
			ow.String("PUII", id) // the client's offer key (FNV-1 of it) and the purchase's PROI
			list[i] = ow
		}
		w.List("CATA", tdf.TypeStruct, list)
	}
	return w.String("VERS", version).Bytes()
}

// OfferBaseFlag is BASE_FLAG of the offer flags (.rdata 0x012F2EE8).
const OfferBaseFlag = 1

// purchaseOption writes a Blaze::BGEcommerce::PurchaseOption (0x012F2EC8): AVIL whether the offer can
// be bought with this currency, PAIC the price. PAIS (a string) is left out.
func purchaseOption(price uint32) func(w *tdf.Writer) {
	return func(w *tdf.Writer) { w.Int("AVIL", b2i(price > 0)).Int("PAIC", int64(price)) }
}

// GetCommerceProfileReply is BGEcommerce::getCommerceProfile -> GetCommerceProfileResponse (0x012F34FC:
// GCPR struct; RPC job 0x012B5668 at 0x00AB78DF). GCPR is a Blaze::BGEcommerce::CommerceProfile
// (0x012F2DC0: ASEC ASPC BLCC BLEC CTEC CTPC, all uint32), all zero for a new player. The client's
// reader (0x00AB8010, R11) keeps ASPC/ASEC (amount spent in PC/EC) and CTPC/CTEC (number of PC/EC
// purchases) in the ecommerce manager (+0x88/+0x8C/+0x90/+0x94, the fields a purchase reply adds to);
// BLEC and BLCC are not read. Since B7c the four come from the account's stored purchases (purchases.go);
// BLEC and BLCC stay 0.
func GetCommerceProfileReply(c store.Commerce) []byte {
	return tdf.NewWriter().Struct("GCPR", func(w *tdf.Writer) {
		w.Int("ASEC", int64(c.SpentEC)).Int("ASPC", int64(c.SpentPC)).Int("BLCC", 0).Int("BLEC", 0).
			Int("CTEC", int64(c.CountEC)).Int("CTPC", int64(c.CountPC))
	}).Bytes()
}

// GetBalanceReply is BGEcommerce::getBalance -> GetBalanceResponse (0x012F3514: CCBL +0x10, ECBL +0x0C,
// PCBL +0x08, uint32; RPC job 0x012B5628 at 0x00AB76BF): the account's currency balances.
//
// Confirmed statically (B7a): the ecommerce manager's reply handler (0x00AB8080) copies PCBL to +0x98
// and ECBL to +0x9C, which 0x00AB7EA0 commits to +0x80/+0x84; the main menu's
// ext.GetPaidCurrencyBalance (0x004DC360) shows +0x80 and ext.GetEarnedCurrencyBalance (0x004DC250)
// +0x84. So ECBL is the earned currency ("Destiny") and PCBL the paid one ("Waypoints"). CCBL (the
// crafting currency, D8) is not read by the client (R11).
func GetBalanceReply(cc, ec, pc uint32) []byte {
	return tdf.NewWriter().Int("CCBL", int64(cc)).Int("ECBL", int64(ec)).Int("PCBL", int64(pc)).Bytes()
}

// GameItem is one Blaze::BGEcommerce::GameItem.
type GameItem struct {
	CatalogID uint32
	Type      int    // BGEcommerce item type
	Stack     uint32 // STCK: how many one can own
	Enabled   bool
}

// GameItemsOf lists every item of the content that has an item type, by catalogID; testing-only
// shapers and their skins are disabled. The loadout page's STCK is pageLimit, the most perk pages an
// account can have (R11: PerksUI_OnSelectLoadout 0x00484440 offers STCK - pages more; B7c); 0 keeps
// B7a's value, the item's maximum (1), which allows no second page.
func GameItemsOf(c *content.Content, pageLimit uint32) []GameItem {
	var list []GameItem
	if c == nil {
		return nil
	}
	for _, it := range c.Items {
		if it.ItemType != content.ItemInvalid {
			stack := max(it.MaxOwnable, 1)
			if it.ItemType == content.ItemLoadoutPage && pageLimit > 0 {
				stack = pageLimit
			}
			list = append(list, GameItem{CatalogID: it.CatalogID, Type: it.ItemType, Stack: stack,
				Enabled: !it.TestingOnly})
		}
	}
	return list
}

// GetGameItemsReply is BGEcommerce::getGameItems -> GetGameItemsResponse (0x012F3554: ITMS list of
// Blaze::BGEcommerce::GameItem 0x014626F0; RPC job 0x012B827C at 0x00ACEB9F). GameItem: ENBL enabled,
// ITID catalogID, RARE rarity (0 here), STCK the most one can own (Shape/Gem maxOwnable, else 1), TYPE
// the item type enum (SHAPER 1 ... LOADOUT_PAGE 9, .rdata 0x012F2FE0). The list describes the items,
// not what the player owns (that is getCompleteData's ITEM). The client's reader (0x00AD0F90, R11)
// skips TYPE 0 and files each item under its TYPE: shaper select lists the TYPE 5 items as its wards
// (0x0051B0A0), and the perk screen takes the TYPE 9 item's STCK as the most perk pages an account can
// have (0x00484440: pages left to buy = STCK - pages). ENBL and RARE have no reader found. No content:
// ITMS left out.
func GetGameItemsReply(items []GameItem) []byte {
	if len(items) == 0 {
		return nil
	}
	list := make([]*tdf.Writer, len(items))
	for i, it := range items {
		list[i] = tdf.NewWriter().Int("ENBL", b2i(it.Enabled)).Int("ITID", int64(it.CatalogID)).Int("RARE", 0).
			Int("STCK", int64(it.Stack)).Int("TYPE", int64(it.Type))
	}
	return tdf.NewWriter().List("ITMS", tdf.TypeStruct, list).Bytes()
}
