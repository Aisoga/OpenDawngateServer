package blaze

import (
	"fmt"

	"dawngate/server/internal/content"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// Perk pages (roadmap B7c; the client's side was read in R11, docs/ACCOUNT_CONTENT.md §7).
//
// A page is a 4 x 4 board of perk shapes (catalogIDs of PerkShape items, each at a cell GIDX = row * 4 +
// column, turned ROT quarter turns) with gems in their sockets (Gem catalogIDs, SIDX = the index into
// PerkShape.socket). The client keeps its own copy: getCompleteData's PERK replaces it at login,
// getPerkPageData (sent after a loadout page was granted) appends the pages it doesn't have yet, and
// updatePerkPage sends a whole page after an edit and ignores the reply. validatePerkPage and
// resetPerkPage have no sender in the client.
const (
	CmdBGAUpdatePerkPage   = 0x81
	CmdBGAValidatePerkPage = 0x82
	CmdBGAGetPerkPageData  = 0x87
	// PERKPAGEUPDATE_ENUM (.rdata 0x012E34A0): UNKNOWN 0, SUCCESS 1, FAILED 2.
	PerkPageUpdateSuccess = 1
	PerkPageUpdateFailed  = 2
	// The board: kNumGridSegments 16 (LoadoutPage.numGridSegments), kShapeRotLimit 3 (LoadoutPerk_Init
	// 0x00889810 asserts "mRotation <= 3" and "idx < kNumGridSegments").
	PerkBoardCells   = 16
	PerkMaxRotation  = 3
	DefaultPerkPages = 10 // the page limit (the loadout page's GameItem.STCK) [assumed: the original is unknown]
)

func registerPerkPages(s *Server) {
	// getPerkPageData (BGAGetPerkPageDataRequest, table 0x012E3368: SMRY bool default 1, USER the local
	// persona id; sender BGAComponent_SendGetPerkPageData 0x00ACE8F0, job 0x012B823C) ->
	// BGAGetPerkPageDataResponse (0x012E3480: PPGS list of BGACompletePerkPage, USER). The reply handler
	// (MetagameManager_OnGetPerkPageData 0x00AD1CF0) asserts USER = the local persona id ("<dev>: Mismatch
	// of accounts") and appends the pages whose PID it doesn't have yet, so USER is echoed and PPGS
	// carries every page of that persona's account. SMRY is ignored [assumed: whole pages either way; a
	// new page is empty anyway].
	s.Register(ComponentBattlegroundsAccounts, CmdBGAGetPerkPageData, "getPerkPageData", func(r *Request) []byte {
		user, _ := r.Body.Int("USER")
		acct, _, ok := r.Server.Store.PersonaByID(user)
		if !ok {
			r.Server.Log.Logf("blaze", "   (getPerkPageData: no persona %d; no pages)", user)
		}
		return PerkPageDataReply(user, acct.PerkPages)
	})
	// updatePerkPage (BGAUpdatePerkPageRequest, table 0x012E3388: PAGE BGACompletePerkPage; sender
	// BGAComponent_SendUpdatePerkPage 0x00ACEA00 from MetagameManager_UpdatePerkPage 0x00AD16C0, which
	// writes the whole page with PerkPage_WriteTdf 0x00ACEE20) -> BGAUpdatePerkPageResponse (0x01460FF4:
	// ENUM). The client's callback (0x004E5B70) is empty, so the reply only matters to us: SUCCESS when
	// the page is stored, FAILED when it is refused (checkPerkPage; logged).
	s.Register(ComponentBattlegroundsAccounts, CmdBGAUpdatePerkPage, "updatePerkPage", func(r *Request) []byte {
		page, ok := pageOf(r)
		if !ok {
			return UpdatePerkPageReply(PerkPageUpdateFailed)
		}
		var why string
		r.update("updatePerkPage", func(a *store.Account) {
			if why = checkPerkPage(page, *a, r.Server.Content); why == "" {
				*a.Page(page.ID) = page
			}
		})
		if r.Conn.Session == nil {
			return UpdatePerkPageReply(PerkPageUpdateFailed)
		}
		if why != "" {
			r.Server.Log.Logf("blaze", "   (updatePerkPage: page %d refused: %s)", page.ID, why)
			return UpdatePerkPageReply(PerkPageUpdateFailed)
		}
		r.Server.Log.Logf("blaze", "   account %d: perk page %d %q stored (version %d, %d shapes)",
			r.Conn.Session.Account.UserID, page.ID, page.Name, page.Version, len(page.Shapes))
		return UpdatePerkPageReply(PerkPageUpdateSuccess)
	})
	// validatePerkPage (0x82) has no sender, request or response class in the client (R11). We read it
	// as updatePerkPage's request without the store [assumed] and answer with the same ENUM.
	s.Register(ComponentBattlegroundsAccounts, CmdBGAValidatePerkPage, "validatePerkPage", func(r *Request) []byte {
		page, ok := pageOf(r)
		acct, logged := r.account()
		if !ok || !logged {
			return UpdatePerkPageReply(PerkPageUpdateFailed)
		}
		if why := checkPerkPage(page, acct, r.Server.Content); why != "" {
			r.Server.Log.Logf("blaze", "   (validatePerkPage: page %d invalid: %s)", page.ID, why)
			return UpdatePerkPageReply(PerkPageUpdateFailed)
		}
		return UpdatePerkPageReply(PerkPageUpdateSuccess)
	})
}

// pageOf decodes the request's PAGE (BGACompletePerkPage); false (logged) if it is missing.
func pageOf(r *Request) (store.PerkPage, bool) {
	sub, ok := r.Body.Sub("PAGE")
	if !ok {
		r.Server.Log.Log("blaze", "   (no PAGE in the request)")
		return store.PerkPage{}, false
	}
	return DecodePerkPage(sub), true
}

// DecodePerkPage reads a Blaze::BattlegroundsAccounts::BGACompletePerkPage (0x012E322C: PERK list of
// BGAPerkPageShape {GEMS list of BGAPerkPageGem {GID, SIDX}, GIDX, PID, ROT}, SUMM BGASummaryPerkPage
// {NAME, PID, STAT, VERS}). Missing members are 0.
func DecodePerkPage(s *tdf.Struct) store.PerkPage {
	var p store.PerkPage
	u32 := func(s *tdf.Struct, label string) uint32 { v, _ := s.Int(label); return uint32(v) }
	u8 := func(s *tdf.Struct, label string) uint8 { v, _ := s.Int(label); return uint8(v) }
	if summ, ok := s.Sub("SUMM"); ok {
		p.Name, _ = summ.Str("NAME")
		p.ID, p.Version, p.Status = u32(summ, "PID"), u32(summ, "VERS"), u8(summ, "STAT")
	}
	for _, sh := range structs(s, "PERK") {
		shape := store.PerkShape{ID: u32(sh, "PID"), Cell: u8(sh, "GIDX"), Rotation: u8(sh, "ROT")}
		for _, g := range structs(sh, "GEMS") {
			shape.Gems = append(shape.Gems, store.PerkGem{ID: u32(g, "GID"), Socket: u8(g, "SIDX")})
		}
		p.Shapes = append(p.Shapes, shape)
	}
	return p
}

// structs returns the struct items of a struct-list member (none if missing).
func structs(s *tdf.Struct, label string) []*tdf.Struct {
	f, ok := s.Get(label)
	if !ok {
		return nil
	}
	l, ok := f.Value.(*tdf.List)
	if !ok {
		return nil
	}
	var out []*tdf.Struct
	for _, v := range l.Items {
		if st, ok := v.(*tdf.Struct); ok {
			out = append(out, st)
		}
	}
	return out
}

// checkPerkPage says why a page from the client can't be stored ("" = it can). Our rules [assumed:
// the original server's checks aren't in the client], from what the client itself asserts or needs
// (R11):
//   - the page id is one of the account's pages, and its version is at least 1 (LoadoutManager_LoadPage
//     0x0088DAD0 asserts mSaveVersion > 0);
//   - every shape is on the board (GIDX < 16) with ROT <= 3, every gem's socket exists on its shape
//     (once the content knows sockets) and holds one gem;
//   - with content: every shape is a PerkShape item and every gem a Gem item, and a page holds no more
//     of each than the account owns (an alwaysOwned item counts as owned its maximum).
//
// Board overlaps aren't checked (the UI prevents them while dragging), nor gem colours: a gem whose
// colour doesn't fit its socket only leaves the shape incomplete (LoadoutManager_IsPerkComplete
// 0x00888AE0), which the client allows.
func checkPerkPage(p store.PerkPage, a store.Account, c *content.Content) string {
	if a.Page(p.ID) == nil {
		return fmt.Sprintf("the account has no page %d", p.ID)
	}
	if p.Version < 1 {
		return "version 0"
	}
	if len(p.Shapes) > PerkBoardCells {
		return fmt.Sprintf("%d shapes", len(p.Shapes))
	}
	used := map[uint32]uint32{}
	var order []uint32 // the items in page order, for a stable message
	count := func(id uint32) {
		if used[id] == 0 {
			order = append(order, id)
		}
		used[id]++
	}
	for i, s := range p.Shapes {
		if s.Cell >= PerkBoardCells || s.Rotation > PerkMaxRotation {
			return fmt.Sprintf("shape %d: cell %d, rotation %d", i, s.Cell, s.Rotation)
		}
		count(s.ID)
		perk, known := c.Perk(s.ID)
		if !c.Empty() && (!known || perk.Kind != content.KindShape) {
			return fmt.Sprintf("shape %d: %#x is not a perk shape", i, s.ID)
		}
		sockets := map[uint8]bool{}
		for _, g := range s.Gems {
			if sockets[g.Socket] {
				return fmt.Sprintf("shape %d: socket %d twice", i, g.Socket)
			}
			sockets[g.Socket] = true
			if known && !c.Old() && int(g.Socket) >= len(perk.Sockets) {
				return fmt.Sprintf("shape %d (%s): no socket %d", i, perk.Name, g.Socket)
			}
			count(g.ID)
			if gem, ok := c.Perk(g.ID); !c.Empty() && (!ok || gem.Kind != content.KindGem) {
				return fmt.Sprintf("shape %d: %#x is not a gem", i, g.ID)
			}
		}
	}
	if c.Empty() {
		return ""
	}
	for _, id := range order {
		if have := ownedCount(a, c, id); used[id] > have {
			return fmt.Sprintf("%#x placed %d times, owned %d", id, used[id], have)
		}
	}
	return ""
}

// ownedCount is how many of an item the account owns for a perk page: the inventory count, or the
// maximum for an alwaysOwned item (Collection_IsOwned 0x00878840).
func ownedCount(a store.Account, c *content.Content, id uint32) uint32 {
	if it, ok := c.Item(id); ok && it.Status == content.StatusAlwaysOwned {
		return max(it.MaxOwnable, 1)
	}
	return a.Items[id]
}

// perkPage writes a Blaze::BattlegroundsAccounts::BGACompletePerkPage: PERK (the shapes, each with GEMS,
// GIDX, PID, ROT; empty lists written, as R11's goldens have them) and SUMM {NAME, PID, STAT, VERS}.
func perkPage(w *tdf.Writer, p store.PerkPage) {
	shapes := make([]*tdf.Writer, len(p.Shapes))
	for i, s := range p.Shapes {
		gems := make([]*tdf.Writer, len(s.Gems))
		for j, g := range s.Gems {
			gems[j] = tdf.NewWriter().Int("GID", int64(g.ID)).Int("SIDX", int64(g.Socket))
		}
		shapes[i] = tdf.NewWriter().List("GEMS", tdf.TypeStruct, gems).Int("GIDX", int64(s.Cell)).
			Int("PID", int64(s.ID)).Int("ROT", int64(s.Rotation))
	}
	w.List("PERK", tdf.TypeStruct, shapes)
	w.Struct("SUMM", func(w *tdf.Writer) {
		w.String("NAME", p.Name).Int("PID", int64(p.ID)).Int("STAT", int64(p.Status)).Int("VERS", int64(p.Version))
	})
}

func perkPages(pages []store.PerkPage) []*tdf.Writer {
	list := make([]*tdf.Writer, len(pages))
	for i, p := range pages {
		list[i] = tdf.NewWriter()
		perkPage(list[i], p)
	}
	return list
}

// PerkPageDataReply is getPerkPageData's BGAGetPerkPageDataResponse: PPGS the pages, USER echoed.
func PerkPageDataReply(user int64, pages []store.PerkPage) []byte {
	return tdf.NewWriter().List("PPGS", tdf.TypeStruct, perkPages(pages)).Int("USER", user).Bytes()
}

// UpdatePerkPageReply is updatePerkPage's (and validatePerkPage's) BGAUpdatePerkPageResponse: ENUM.
func UpdatePerkPageReply(result int64) []byte {
	return tdf.NewWriter().Int("ENUM", result).Bytes()
}

// pageLimit is the most perk pages an account can have (Policy.PerkPages, else DefaultPerkPages).
func (s *Server) pageLimit() uint32 {
	if s.Policy.PerkPages > 0 {
		return s.Policy.PerkPages
	}
	return DefaultPerkPages
}

// loadoutPages counts the account's loadout-page items: one perk page each.
func loadoutPages(a store.Account, c *content.Content) uint32 {
	var n uint32
	for id, count := range a.Items {
		if it, ok := c.Item(id); ok && it.ItemType == content.ItemLoadoutPage {
			n += count
		}
	}
	return n
}

// ensurePages gives the account a perk page per loadout-page item it owns (a new account owns one: its
// first page, R11: "a new account needs a page"); accounts made before B7c get theirs at their next
// login. Without content nothing happens.
func (s *Server) ensurePages(acct store.Account) store.Account {
	if s.Content.Empty() || uint32(len(acct.PerkPages)) >= loadoutPages(acct, s.Content) {
		return acct
	}
	var added []uint32
	got, err := s.Store.Update(acct.UserID, func(a *store.Account) {
		for uint32(len(a.PerkPages)) < loadoutPages(*a, s.Content) {
			added = append(added, a.AddPage().ID)
		}
	})
	if err != nil {
		s.Log.Logf("blaze", "   (perk pages: %v)", err)
		if err == store.ErrNoAccount {
			return acct
		}
	}
	s.Log.Logf("blaze", "   account %d: perk pages %v created", got.UserID, added)
	return got
}
