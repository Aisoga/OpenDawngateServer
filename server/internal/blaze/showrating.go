package blaze

import (
	"math"

	"dawngate/server/internal/store"
)

// The shown skill rating (RATINGSHOW1). The main menu's profile box has EA's field for a rating (MainMenuUI.gfx's
// mcProfile.m_glickoText, filled by SetExtendedData's second argument), which build 854 always leaves empty; the
// setup's exe patch (exepatch's rating) has both callers (MainMenuUI_OnUserExtendedDataChanged 0x004DE0D0,
// MainMenuUI_BindExtFunctions 0x004DFFC0) read the local user's DMAP[ExtendedDataShownRating] through
// BlazeUser_GetExtendedDataValue (0x00C8B560) and show "Rating <n>" when it is above 0. So the server decides per
// login: it sends the key only for a rating players are to see. ExtendedDataRating (RATING2's 0x00070035, the custom
// lobby's BALANCE) is sent either way.

// ExtendedDataShownRating is the UserSessionExtendedData.DMAP key of the rating a player sees (component 7 << 16 |
// 0x37: a key 854 never reads; it reads 0x34-0x36 only, BlazePlatform_GetUserExtendedInfo 0x00AD4F90).
const ExtendedDataShownRating = 0x00070037

// ShownRating is the rating players see for an account's history, rounded, and whether they see one: with ShowRating
// on and once the account has as many rated games as the Leaderboard asks (LeaderboardMinGames, at least 1), so a
// number shown is one that ranks; below that (and for an account never rated) nothing. Every place that shows a
// rating (the profile box; RATINGSHOW2's Leaderboard column and RATINGSHOW3's lobby tiles) asks this.
func (s *Server) ShownRating(h store.History) (int64, bool) {
	r := h.Rating.OrStart()
	return s.shownRating(r.Value, r.Games)
}

// shownRating is ShownRating's rule for a rating and its rated games.
func (s *Server) shownRating(value float64, games uint32) (int64, bool) {
	if s == nil || !s.ShowRating {
		return 0, false
	}
	if int64(games) < max(int64(s.LeaderboardMinGames), 1) {
		return 0, false
	}
	return max(int64(math.Round(value)), 1), true // a positive value: the patch shows nothing for 0 or less
}

// ShownValue is an account's DMAP[ExtendedDataShownRating]: ShownRating's number, or 0 (the key left out) when players
// see none. Every extended data the server sends carries it (the player's own at the login, after userInfoChanged and
// after a game; other users' in UserAdded, lookupUser and the presence updates), so a copy of the local user's data
// from any of them keeps the profile box's number.
func (s *Server) ShownValue(acct store.Account) int64 {
	v, _ := s.ShownRating(acct.History)
	return v
}
