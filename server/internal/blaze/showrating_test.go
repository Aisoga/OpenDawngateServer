package blaze

import (
	"encoding/hex"
	"io"
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// The rating players see (RATINGSHOW1): with ShowRating on and from the Leaderboard's games (rated ones) on, rounded
// and at least 1; never for an account without a rated game, nor with ShowRating off.
func TestShownRating(t *testing.T) {
	s := NewServer(logging.New(io.Discard), "127.0.0.1", 17502)
	if !s.ShowRating || !s.RatingLeaderboard {
		t.Fatal("both switches are on by default")
	}
	rated := func(v float64, games uint32) store.History {
		return store.History{Rating: store.Rating{Value: v, RD: 80, Vol: 0.06, Games: games}}
	}
	for _, c := range []struct {
		name     string
		h        store.History
		minGames int
		show     bool
		want     int64
		ok       bool
	}{
		{"never rated", store.History{}, 10, true, 0, false},
		{"below the Leaderboard's games", rated(1612.4, 9), 10, true, 0, false},
		{"at the Leaderboard's games", rated(1612.4, 10), 10, true, 1612, true},
		{"rounded", rated(1532.5, 44), 10, true, 1533, true},
		{"off", rated(1612.4, 44), 10, false, 0, false},
		{"a minimum of 0 still needs a rated game", store.History{}, 0, true, 0, false},
		{"one rated game with a minimum of 0", rated(1662.3, 1), 0, true, 1662, true},
		{"never below 1", rated(-40, 30), 10, true, 1, true},
	} {
		s.ShowRating, s.LeaderboardMinGames = c.show, c.minGames
		if got, ok := s.ShownRating(c.h); got != c.want || ok != c.ok {
			t.Errorf("%s: %d %v, want %d %v", c.name, got, ok, c.want, c.ok)
		}
	}
	var none *Server
	if _, ok := none.ShownRating(rated(1500, 50)); ok {
		t.Error("a nil server shows a rating")
	}
}

// shownOf returns the shown rating in an extended data struct (DATA's DMAP[0x00070037]; 0 without one).
func shownOf(t *testing.T, s *tdf.Struct) int64 {
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
		if e.Key == int64(ExtendedDataShownRating) {
			return e.Value.(int64)
		}
	}
	return 0
}

// RATINGSHOW1: the extended data with the shown rating matches tdf_golden.py's RATINGSHOW1 cases and the member tables.
func TestRatingShow1RepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	acct, p := testAccount()
	acct.History.Rating = store.Rating{Value: 1612.4, RD: 120, Vol: 0.06, Games: 12}
	s := NewServer(logging.New(io.Discard), "127.0.0.1", 17502)
	for _, c := range []struct {
		name, class string
		got         []byte
	}{
		{"ratingshow1_extendeddataupdate_notification", "Blaze::UserSessionExtendedDataUpdate",
			ExtendedDataUpdateNotification(p.ID, 0, 3, RatingValue(acct), s.ShownValue(acct))},
		{"ratingshow1_useradded_notification", "Blaze::NotifyUserAdded", UserAddedNotification(acct, p, 0, s.ShownValue(acct))},
	} {
		tdfcheck.Require(t, c.class, c.got)
		if h, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; h != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, h, want)
		}
	}
}

// RATINGSHOW1: the login's UserSessionExtendedDataUpdate carries DMAP[0x00070037] only with show_rating on and the
// Leaderboard's games rated (RATING2's 0x00070035 in every case), and another player learns it with the user
// (UserAdded), so a lobby can show it.
func TestShownRatingAtLogin(t *testing.T) {
	for _, c := range []struct {
		show        bool
		games       uint32
		want, other int64
	}{{true, 12, 2105, 2105}, {false, 12, 0, 0}, {true, 9, 0, 0}} {
		ps := newPipeSessionWith(t, func(s *Server) {
			seatFirst(2)(s) // both play game 1, whose chat GG-1 they join (SEC6)
			s.ShowRating = c.show
			for _, email := range []string{"player@example.com", "second@example.com"} {
				if _, _, err := s.Store.Login(email, time.Unix(testTime, 0)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Store.Update(store.FirstUserID, func(a *store.Account) {
				a.History.Rating = store.Rating{Value: 2105.2, RD: 90, Vol: 0.06, Games: c.games}
			}); err != nil {
				t.Fatal(err)
			}
		})
		ps.send(1, 0x28, 0, tdf.NewWriter().String("MAIL", "player@example.com").String("PASS", testPassword).
			String("TOKN", "").Int("TYPE", 0).Bytes())
		ps.next("login reply")
		ps.send(1, 0x6E, 1, tdf.NewWriter().String("PNAM", store.PersonaName("player@example.com")).Bytes())
		ps.next("loginPersona reply")
		ps.next("UserAuthenticated")
		data := payloadOf(t, "Blaze::UserSessionExtendedDataUpdate", ps.next("UserSessionExtendedDataUpdate"))
		if got, r := shownOf(t, data), ratingOf(t, data); got != c.want || r != 2105 {
			t.Errorf("show %v, %d rated games: shown %d (rating %d), want %d (2105)", c.show, c.games, got, r, c.want)
		}
		ps.send(6, 1, 3, ps.request("createplaygroup_request"))
		ps.next("createPlaygroup reply")
		ps.next("NotifyJoinPlaygroup (GG-1, alone)")
		two := ps.another()
		two.loginAs("second@example.com", 0)
		two.send(6, 1, 3, ps.request("createplaygroup_request"))
		two.next("createPlaygroup reply")
		f := two.next("the admin's UserAdded")
		if got := shownOf(t, payloadOf(t, "Blaze::NotifyUserAdded", f)); f.Command != NotifyUserAdded || got != c.other {
			t.Errorf("show %v, %d rated games: %#x with shown %d, want UserAdded with %d", c.show, c.games, f.Command, got, c.other)
		}
	}
}
