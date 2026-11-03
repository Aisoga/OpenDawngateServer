package blaze

import (
	"encoding/hex"
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// ratingOf returns the skill rating in an extended data struct (DATA's DMAP[0x00070035]; 0 without one).
func ratingOf(t *testing.T, s *tdf.Struct) int64 {
	t.Helper()
	data, ok := s.Sub("DATA")
	if !ok {
		t.Fatalf("no DATA: %+v", s)
	}
	f, ok := data.Get("DMAP")
	if !ok {
		return 0
	}
	for _, e := range f.Value.(*tdf.Map).Entries {
		if e.Key == int64(ExtendedDataRating) {
			return e.Value.(int64)
		}
	}
	return 0
}

// rated is acct with a stored rating of value (one rated game).
func rated(acct store.Account, value float64) store.Account {
	acct.History.Rating = store.Rating{Value: value, RD: 290, Vol: 0.06, Games: 1}
	return acct
}

// RATING2: the extended data with the rating key matches tdf_golden.py's RATING2 cases and the member tables.
func TestRating2RepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	acct, p := testAccount()
	won := rated(acct, 1661.51)
	won.Stats = map[string]map[string]string{progress.PlayerStats: {"WINS": "3"}}
	lost := rated(acct, 1338.4)
	lost.Stats = won.Stats
	for _, c := range []struct {
		name, class string
		got         []byte
	}{
		{"rating2_extendeddataupdate_notification", "Blaze::UserSessionExtendedDataUpdate",
			ExtendedDataUpdateNotification(p.ID, 0, progress.Wins(won.Stats[progress.PlayerStats]), RatingValue(won), 0)},
		{"rating2_useradded_notification", "Blaze::NotifyUserAdded", UserAddedNotification(rated(acct, 2105), p, 0, 0)},
		{"rating2_memberdata_notification", "Blaze::UserSessionExtendedDataUpdate", PresenceNotification(lost, p, 0x20001, 0)},
	} {
		tdfcheck.Require(t, c.class, c.got)
		if h, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; h != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, h, want)
		}
	}
}

func TestRatingValue(t *testing.T) {
	acct, _ := testAccount()
	for _, c := range []struct {
		r    store.Rating
		want int64
	}{
		{store.Rating{}, 1500}, // no rated game yet: the start value
		{store.StartRating, 1500},
		{store.Rating{Value: 1661.5, RD: 290, Vol: 0.06}, 1662},
		{store.Rating{Value: 1338.49, RD: 290, Vol: 0.06}, 1338},
		{store.Rating{Value: -12, RD: 40, Vol: 0.06}, 0}, // never negative (the client reads an unsigned value)
	} {
		acct.History.Rating = c.r
		if got := RatingValue(acct); got != c.want {
			t.Errorf("RatingValue(%+v) = %d, want %d", c.r, got, c.want)
		}
	}
}

// RATING2: the login's UserSessionExtendedDataUpdate carries the stored rating in both show_rating modes, and the
// game chat's members learn each other's rating (UserAdded), then a changed one (refreshKnownUser), as the custom
// lobby's admin needs them for BALANCE.
func TestRatingInExtendedData(t *testing.T) {
	for _, show := range []bool{true, false} {
		ps := newPipeSessionWith(t, func(s *Server) {
			seatFirst(2)(s) // both play game 1, whose chat GG-1 they join (SEC6)
			s.ShowRating = show
			for _, email := range []string{"player@example.com", "second@example.com"} {
				if _, _, err := s.Store.Login(email, time.Unix(testTime, 0)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Store.Update(store.FirstUserID, func(a *store.Account) { *a = rated(*a, 2105.2) }); err != nil {
				t.Fatal(err)
			}
		})
		ps.send(1, 0x28, 0, tdf.NewWriter().String("MAIL", "player@example.com").String("PASS", testPassword).
			String("TOKN", "").Int("TYPE", 0).Bytes())
		ps.next("login reply")
		ps.send(1, 0x6E, 1, tdf.NewWriter().String("PNAM", store.PersonaName("player@example.com")).Bytes())
		ps.next("loginPersona reply")
		ps.next("UserAuthenticated")
		f := ps.next("UserSessionExtendedDataUpdate")
		if r := ratingOf(t, payloadOf(t, "Blaze::UserSessionExtendedDataUpdate", f)); r != 2105 {
			t.Errorf("show_rating %v: the login's rating %d, want 2105", show, r)
		}

		// the game's chat GG-1 (every player of a custom lobby joins it): the joiner learns the admin's rating
		ps.send(6, 1, 3, ps.request("createplaygroup_request"))
		ps.next("createPlaygroup reply")
		ps.next("NotifyJoinPlaygroup (GG-1, alone)")
		two := ps.another()
		two.loginAs("second@example.com", 0)
		two.send(6, 1, 3, ps.request("createplaygroup_request"))
		two.next("createPlaygroup reply")
		f = two.next("the admin's UserAdded")
		if r := ratingOf(t, payloadOf(t, "Blaze::NotifyUserAdded", f)); f.Command != NotifyUserAdded || r != 2105 {
			t.Errorf("show_rating %v: %#x with rating %d, want UserAdded with 2105", show, f.Command, r)
		}
		two.next("UserUpdated")
		two.next("NotifyJoinPlaygroup")
		f = ps.next("the joiner's UserAdded")
		if r := ratingOf(t, payloadOf(t, "Blaze::NotifyUserAdded", f)); f.Command != NotifyUserAdded || r != 1500 {
			t.Errorf("show_rating %v: %#x with rating %d, want UserAdded with the start rating", show, f.Command, r)
		}
		ps.next("UserUpdated")
		ps.next("NotifyMemberJoinedPlaygroup")

		// a rated game changed the admin's rating: the joiner hears it
		if _, err := ps.srv.Store.Update(store.FirstUserID, func(a *store.Account) { *a = rated(*a, 1987.6) }); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { ps.srv.refreshKnownUser(store.FirstPersonaID); close(done) }()
		f = two.next("the admin's new rating")
		if r := ratingOf(t, payloadOf(t, "Blaze::UserSessionExtendedDataUpdate", f)); r != 1988 {
			t.Errorf("show_rating %v: the refreshed rating %d, want 1988", show, r)
		}
		<-done
	}
}
