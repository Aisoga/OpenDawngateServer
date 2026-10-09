package blaze

import (
	"fmt"

	"dawngate/server/internal/content"
	"dawngate/server/internal/store"
)

// Policy says what a new account gets and which account flags the server forces (B7a; dgserver flags
// --owned, --start-ec, --start-pc, --ftue-done).
type Policy struct {
	// Owned is the inventory policy for new accounts: content.OwnAll or content.OwnStarter.
	Owned string
	// StartEC and StartPC are a new account's earned ("Destiny") and paid ("Waypoints") currency.
	StartEC, StartPC uint32
	// FTUEDone gives every account store.FlagFTUEDone in BGABasicData.FLAG, so PLAY opens the normal
	// play screen instead of the Practice-only first-time-user screen (needed for PvP matchmaking,
	// roadmap B10). Without it only accounts whose stored Flags have the bit pass the gate.
	FTUEDone bool
	// PerkPages is the most perk pages an account can have (B7c, dgserver --perk-pages; the loadout
	// page's GameItem.STCK); 0 = DefaultPerkPages.
	PerkPages uint32
	// SpentElsewhere is a test aid (B7c-live, dgserver --test-spent-elsewhere): right before a purchase
	// is checked, the account's balance in the purchase's currency drops to 0, as if spent in another
	// session after the client last fetched it. The client never sends an unaffordable purchase by
	// itself (its prompt disables UNLOCK below the price and offers only the counts the balance covers,
	// docs/ACCOUNT_CONTENT.md §13), so this is how a session sees the server's
	// BGECOMMERCE_ERR_CHECKOUT_FAILED_INSUFFICIENT_FUNDS and the client's purchase-failed popup.
	SpentElsewhere bool
}

// Defaults of Policy (and dgserver's flags).
const (
	DefaultStartEC = 10000
	DefaultStartPC = 1000
)

// DefaultPolicy is everything owned, DefaultStartEC/DefaultStartPC, the first-time-user gate as the
// original game had it (closed for new accounts), DefaultPerkPages.
func DefaultPolicy() Policy {
	return Policy{Owned: content.OwnAll, StartEC: DefaultStartEC, StartPC: DefaultStartPC, PerkPages: DefaultPerkPages}
}

// grant gives an account the new-account content once (inventory, avatar, currencies) and logs it.
// Without content it does nothing, so an account made before the content file existed gets it at a
// later login.
func (s *Server) grant(acct store.Account) store.Account {
	if acct.Granted || s.Content.Empty() {
		return acct
	}
	items, avatar, err := s.Content.Grant(s.Policy.Owned)
	if err != nil {
		s.Log.Logf("blaze", "   (no content granted: %v)", err)
		return acct
	}
	got, err := s.Store.Update(acct.UserID, func(a *store.Account) {
		for id, n := range items {
			a.Items[id] = max(a.Items[id], n)
		}
		if a.Avatar == 0 {
			a.Avatar = avatar
		}
		a.EC += s.Policy.StartEC
		a.PC += s.Policy.StartPC
		a.Granted = true
	})
	if err != nil && err != store.ErrNoAccount {
		s.Log.Logf("blaze", "   (%v)", err)
	}
	if err == store.ErrNoAccount {
		return acct
	}
	s.Log.Logf("blaze", "   account %d: granted %d items (%s), avatar %#x, EC %d, PC %d", got.UserID, len(items),
		s.Policy.Owned, got.Avatar, got.EC, got.PC)
	return got
}

// account returns the logged-in account of the request's connection, fresh from the store.
func (r *Request) account() (store.Account, bool) {
	if r.Conn.Session == nil {
		return store.Account{}, false
	}
	return r.Server.Store.Account(r.Conn.Session.Account.UserID)
}

// update changes the logged-in account; without a login it only logs that. what names the request.
func (r *Request) update(what string, fn func(a *store.Account)) {
	if r.Conn.Session == nil {
		r.Server.Log.Logf("blaze", "   (%s before login; nothing stored)", what)
		return
	}
	acct, err := r.Server.Store.Update(r.Conn.Session.Account.UserID, fn)
	if err != nil {
		r.Server.Log.Log("blaze", fmt.Sprintf("   (%s: %v)", what, err))
		return
	}
	r.Conn.Session.Account = acct
}
