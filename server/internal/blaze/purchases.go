package blaze

import (
	"fmt"
	"strconv"

	"dawngate/server/internal/content"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// Purchases (roadmap B7c; the client's side was read in R11, docs/ACCOUNT_CONTENT.md §5).
//
// PurchaseFlowUI_PurchaseCurrentOffer (0x004F23E0) buys the current offer with count 1 through
// EcommerceManager_PurchaseOfferWithEarnedCurrency (0x00ABA200 -> purchaseWithEC, sender 0x00AB7A70) or
// ...WithPaidCurrency (0x00AB9F80 -> purchaseWithPC, sender 0x00AB7B80). The request
// (PurchaseWithECRequest 0x012F3360 / PurchaseWithPCRequest 0x012F3310) carries PROI = the offer's PUII,
// PPRI = the offer's paid (PC) price even for EC, VERS = the catalog's VERS, CNT, and ITMS/QNTS = the
// offer's ITMI/ITMC. The reply handlers (0x00AB9A40 / 0x00AB9CC0) take ECLB/PCBL as the new balance,
// add the drop to the spent amount and one to the purchase count of that currency, and grant each ITMS
// entry CNT x QNTS times (asserting both lists have the same length; a loadout page also makes the client
// ask getPerkPageData). An error reply goes to the purchase-failed popup (event 0x208F4419).
const (
	CmdBGEPurchaseWithPC = 0x03
	CmdBGEPurchaseWithEC = 0x04
)

// BGEcommerce error codes (the code half of BGECOMMERCE_ERR_*, docs/data/blaze_components.txt).
const (
	ErrBGEProductNotFound          = 0x0005 // BGECOMMERCE_ERR_PRODUCT_NOT_FOUND: no offer with that PROI
	ErrBGENoCatalog                = 0x0006 // BGECOMMERCE_ERR_NO_CATALOG: the server has no content
	ErrBGEInsufficientFunds        = 0x0009 // BGECOMMERCE_ERR_CHECKOUT_FAILED_INSUFFICIENT_FUNDS
	ErrBGENotAuthorized            = 0x0015 // BGECOMMERCE_ERR_NOT_AUTHORIZED: no login
	ErrBGEBadVersion               = 0x0016 // BGECOMMERCE_ERR_BAD_VERSION: VERS isn't the catalog's
	ErrBGEPaymentMethodUnavailable = 0x0018 // BGECOMMERCE_ERR_PAYMENT_METHOD_UNAVAILABLE: no price in that currency
	ErrBGEInvalidItem              = 0x001A // BGECOMMERCE_ERROR_INVALID_ITEM: owned up to the limit, or CNT 0
)

func registerPurchases(s *Server) {
	s.Register(ComponentBGEcommerce, CmdBGEPurchaseWithEC, "purchaseWithEC", func(r *Request) []byte {
		return r.Server.purchase(r, false)
	})
	s.Register(ComponentBGEcommerce, CmdBGEPurchaseWithPC, "purchaseWithPC", func(r *Request) []byte {
		return r.Server.purchase(r, true)
	})
}

// Purchase is what a purchase reply carries.
type Purchase struct {
	BlazeID  int64
	Count    uint32   // CNT, echoed
	Balance  uint32   // the new balance of the currency used (ECLB or PCBL)
	Items    []uint32 // ITMS: the offer's catalogIDs
	Quantity []uint32 // QNTS: per-unit quantities (the client grants CNT x QNTS)
}

// PurchaseWithECReply is purchaseWithEC -> Blaze::BGEcommerce::PurchaseWithECResponse (0x012F34B0:
// BZID, CNT, ECLB, ITMS, QNTS).
func PurchaseWithECReply(p Purchase) []byte {
	return tdf.NewWriter().Int("BZID", p.BlazeID).Int("CNT", int64(p.Count)).Int("ECLB", int64(p.Balance)).
		List("ITMS", tdf.TypeInt, p.Items).List("QNTS", tdf.TypeInt, p.Quantity).Bytes()
}

// PurchaseWithPCReply is purchaseWithPC -> Blaze::BGEcommerce::PurchaseWithPCResponse (0x012F3470:
// BZID, CNT, ITMS, PCBL, QNTS).
func PurchaseWithPCReply(p Purchase) []byte {
	return tdf.NewWriter().Int("BZID", p.BlazeID).Int("CNT", int64(p.Count)).List("ITMS", tdf.TypeInt, p.Items).
		Int("PCBL", int64(p.Balance)).List("QNTS", tdf.TypeInt, p.Quantity).Bytes()
}

// purchase runs purchaseWithEC (paid false) or purchaseWithPC (paid true). Our rules [assumed: the
// original commerce backend isn't in the client]: the offer is found by PROI (our PUII = the catalogID in
// decimal) and priced by the server's own catalog in the command's currency (PPRI, the paid price, is
// only logged when it differs from ours); VERS must be the catalog's; the balance must cover price x
// CNT; the account may own at most an item's maximum (the perk-page limit for the loadout page) after
// the purchase. On success the balance drops, the items are granted, the commerce profile adds the
// amount and one purchase, and every loadout page bought becomes a new empty perk page (which the
// client then fetches with getPerkPageData).
func (s *Server) purchase(r *Request, paid bool) []byte {
	what, cur := "purchaseWithEC", "EC"
	if paid {
		what, cur = "purchaseWithPC", "PC"
	}
	fail := func(code uint16, why string, args ...any) []byte {
		s.Log.Logf("blaze", "   (%s refused: %s)", what, fmt.Sprintf(why, args...))
		r.Fail(code)
		return nil
	}
	if r.Conn.Session == nil {
		return fail(ErrBGENotAuthorized, "no login")
	}
	if s.Content.Empty() {
		return fail(ErrBGENoCatalog, "no content")
	}
	proi, _ := r.Body.Str("PROI")
	vers, _ := r.Body.Str("VERS")
	ppri, _ := r.Body.Int("PPRI")
	cnt, ok := r.Body.Int("CNT")
	if !ok {
		cnt = 1 // CNT's default
	}
	if vers != CatalogVersion {
		return fail(ErrBGEBadVersion, "VERS %q, catalog %q", vers, CatalogVersion)
	}
	var offer Offer
	found := false
	for _, o := range OffersOf(s.Content) {
		if strconv.FormatUint(uint64(o.CatalogID), 10) == proi {
			offer, found = o, true
			break
		}
	}
	if !found {
		return fail(ErrBGEProductNotFound, "no offer %q", proi)
	}
	price := offer.EC
	if paid {
		price = offer.PC
	}
	if price == 0 {
		return fail(ErrBGEPaymentMethodUnavailable, "offer %q has no %s price", proi, cur)
	}
	if cnt < 1 || cnt > 255 {
		return fail(ErrBGEInvalidItem, "CNT %d", cnt)
	}
	if int64(offer.PC) != ppri {
		s.Log.Logf("blaze", "   (%s: PPRI %d, our paid price %d; ours counts)", what, ppri, offer.PC)
	}
	item, _ := s.Content.Item(offer.CatalogID)
	n := uint32(cnt)
	total := price * n
	limit := max(item.MaxOwnable, 1)
	if item.ItemType == content.ItemLoadoutPage {
		limit = s.pageLimit()
	}
	var code uint16
	var why string
	var pages []uint32
	var drained uint32
	acct, err := s.Store.Update(r.Conn.Session.Account.UserID, func(a *store.Account) {
		bal := &a.EC
		if paid {
			bal = &a.PC
		}
		if s.Policy.SpentElsewhere { // test aid: the balance went elsewhere since the client fetched it
			drained, *bal = *bal, 0
		}
		switch {
		case a.Items[offer.CatalogID]+n > limit:
			code, why = ErrBGEInvalidItem, fmt.Sprintf("%s: %d owned, %d more would pass the limit %d",
				item.Name, a.Items[offer.CatalogID], n, limit)
		case *bal < total:
			code, why = ErrBGEInsufficientFunds, fmt.Sprintf("%d %s needed, %d left", total, cur, *bal)
		default:
			*bal -= total
			a.Items[offer.CatalogID] += n
			if paid {
				a.Commerce.SpentPC += total
				a.Commerce.CountPC++
			} else {
				a.Commerce.SpentEC += total
				a.Commerce.CountEC++
			}
			if item.ItemType == content.ItemLoadoutPage {
				for range n {
					pages = append(pages, a.AddPage().ID)
				}
			}
		}
	})
	if err != nil && err != store.ErrNoAccount {
		s.Log.Logf("blaze", "   (%s: %v)", what, err)
	}
	if err == store.ErrNoAccount {
		return fail(ErrBGENotAuthorized, "no account")
	}
	if s.Policy.SpentElsewhere {
		s.Log.Logf("blaze", "   (test --test-spent-elsewhere: %d %s spent elsewhere before the check)", drained, cur)
	}
	if code != 0 {
		return fail(code, "%s", why)
	}
	r.Conn.Session.Account = acct
	bal := acct.EC
	if paid {
		bal = acct.PC
	}
	s.Log.Logf("blaze", "   account %d: bought %s (%#x) x%d for %d %s; %s %d left", acct.UserID, item.Name,
		offer.CatalogID, n, total, cur, cur, bal)
	if len(pages) > 0 {
		s.Log.Logf("blaze", "   account %d: perk pages %v created", acct.UserID, pages)
	}
	p := Purchase{BlazeID: r.Conn.Session.Persona.ID, Count: n, Balance: bal, Items: []uint32{offer.CatalogID},
		Quantity: []uint32{1}}
	if paid {
		return PurchaseWithPCReply(p)
	}
	return PurchaseWithECReply(p)
}
