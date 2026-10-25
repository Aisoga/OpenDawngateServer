package blaze

import (
	"strconv"
	"strings"

	"dawngate/server/internal/content"
	"dawngate/server/internal/store"
)

// Withheld items in older accounts (WIPSTONES; the list is content's withheldItems).
//
// Content withholds some items (never granted, sold, listed or dropped), but an account made before an item was
// withheld may own it and have it on a perk page. Such a page would fail on the client: a shape that isn't a
// loadout component (no offer: MetagameClientData_BuildLoadoutComponents 0x00ACF990 takes only items with an offer) sets the page's state to failed (LoadoutManager_LoadPage
// 0x0088DAD0). So at login, before the replies, the account loses them:
//   - the withheld items leave the inventory;
//   - a withheld shape leaves every perk page with the gems in its sockets (they are owned and free again), and a
//     withheld gem leaves its socket; such a page's version goes up by one. The rest of the page stays where it is,
//     so the page stays valid (an incomplete or partly empty board is one the client allows; checkPerkPage passes);
//   - a saved choice naming a withheld item (a shaper's skin, the last ward or announcer pack) is dropped, so shaper
//     select falls back to the default;
//   - an avatar that is withheld becomes the default avatar.
//
// It is logged with the counts and changes nothing at a second login.

// withheldSetting reports whether a user setting names a withheld item: a saved skin (SKIN_0x<default skin> =
// <skin catalogID>, ShaperContent), the last ward or announcer pack (WARD, ANNOUNCER: catalogIDs the client writes at
// EnterGame and preselects in the next shaper select).
func withheldSetting(c *content.Content, key, value string) bool {
	k := strings.ToUpper(key)
	if !strings.HasPrefix(k, "SKIN_0X") && k != "WARD" && k != "ANNOUNCER" {
		return false
	}
	id, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
	return err == nil && c.IsWithheld(uint32(id))
}

// withheldIn reports whether the account holds a withheld item.
func withheldIn(a store.Account, c *content.Content) bool {
	if c.IsWithheld(a.Avatar) {
		return true
	}
	for k, v := range a.Settings {
		if withheldSetting(c, k, v) {
			return true
		}
	}
	for id := range a.Items {
		if c.IsWithheld(id) {
			return true
		}
	}
	for _, p := range a.PerkPages {
		for _, s := range p.Shapes {
			if c.IsWithheld(s.ID) {
				return true
			}
			for _, g := range s.Gems {
				if c.IsWithheld(g.ID) {
					return true
				}
			}
		}
	}
	return false
}

// withheldRemoved counts what withholdFrom took out of an account.
type withheldRemoved struct {
	Items    int      // inventory entries
	Shapes   int      // shapes taken off perk pages
	Gems     int      // gems taken off perk pages (also those in a withheld shape)
	Pages    []uint32 // the pages changed
	Settings int      // saved choices dropped (a skin, the ward, the announcer pack)
	Avatar   bool     // the avatar was reset
}

// withholdFrom takes the withheld items out of a (the inventory, the perk pages, the avatar).
func withholdFrom(a *store.Account, c *content.Content) withheldRemoved {
	var r withheldRemoved
	for id := range a.Items {
		if c.IsWithheld(id) {
			delete(a.Items, id)
			r.Items++
		}
	}
	for i := range a.PerkPages {
		p := &a.PerkPages[i]
		changed := false
		shapes := p.Shapes[:0:0]
		for _, s := range p.Shapes {
			if c.IsWithheld(s.ID) {
				r.Shapes++
				r.Gems += len(s.Gems)
				changed = true
				continue
			}
			gems := s.Gems[:0:0]
			for _, g := range s.Gems {
				if c.IsWithheld(g.ID) {
					r.Gems++
					changed = true
					continue
				}
				gems = append(gems, g)
			}
			if len(gems) == 0 {
				gems = nil
			}
			s.Gems = gems
			shapes = append(shapes, s)
		}
		if changed {
			if len(shapes) == 0 {
				shapes = nil
			}
			p.Shapes = shapes
			p.Version++
			r.Pages = append(r.Pages, p.ID)
		}
	}
	for k, v := range a.Settings {
		if withheldSetting(c, k, v) {
			delete(a.Settings, k)
			r.Settings++
		}
	}
	if c.IsWithheld(a.Avatar) {
		a.Avatar = c.DefaultAvatar()
		r.Avatar = true
	}
	return r
}

// withhold takes the withheld items out of the account at login (withholdFrom), logged; an account without one is
// returned as it is.
func (s *Server) withhold(acct store.Account) store.Account {
	if s.Content.Empty() || !withheldIn(acct, s.Content) {
		return acct
	}
	var r withheldRemoved
	got, err := s.Store.Update(acct.UserID, func(a *store.Account) { r = withholdFrom(a, s.Content) })
	if err != nil {
		s.Log.Logf("blaze", "   (withheld items: %v)", err)
		return acct
	}
	s.Log.Logf("blaze", "   account %d: withheld items removed: %d from the inventory, %d shapes and %d gems from perk pages %v, "+
		"%d saved choices, avatar reset %v", got.UserID, r.Items, r.Shapes, r.Gems, r.Pages, r.Settings, r.Avatar)
	return got
}
