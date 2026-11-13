package blaze

import (
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"dawngate/server/internal/rating"
	"dawngate/server/internal/store"
	"dawngate/server/internal/uichannel"
)

// The Leaderboard's tabs (M30, MMR2): STANDARD, DRAFT and CUSTOM, each ranking by its own rating (MMR1's types,
// rating.Modes). The client always asks for 'Skill' and 'SkillDaily' (Stats::getLeaderboard), so which ranking it gets
// is the player's choice on the server: the front end's class (client_ui/leaderboard/LeaderboardRating.as) calls
// leaderboard.select {board} at a tab click, and once leaderboard.state names the new board it presses the screen's
// own Refresh (its nativeBridge.refreshButtonClicked), whose requests leaderboard() answers from the choice. The tabs
// exist only with the rating Leaderboard (RatingLeaderboard): without it the state names no boards, the class draws
// no tabs and every request gets the wins ranking as before.

// The Leaderboard's UI channel call and event.
const (
	CallLeaderboardSelect = "leaderboard.select"
	EventLeaderboardState = "leaderboard.state"
)

// leaderboardSelectGap is the least time between two changes of one player's choice (a faster one is refused and the
// state sent back): the choice itself costs a map entry, but each change makes the class ask for two rankings.
const leaderboardSelectGap = 250 * time.Millisecond

// LeaderboardState is EventLeaderboardState's data: the board the player's Leaderboard requests are answered from,
// and the boards there are, in the tabs' order (none: the wins ranking, no tabs).
type LeaderboardState struct {
	Board  string   `json:"board"`
	Boards []string `json:"boards,omitempty"`
}

// leaderboardChoices are the players' boards (persona id -> a board other than the standard), kept while the player
// is logged in: the last login's end forgets it (uiLoginEnded), so a new login starts on STANDARD.
type leaderboardChoices struct {
	mu    sync.Mutex
	board map[int64]string
	at    map[int64]time.Time // the last change
}

// leaderboardBoard is the board persona's Leaderboard requests are answered from: standard without the rating
// Leaderboard, without a choice or before a login (0).
func (s *Server) leaderboardBoard(persona int64) string {
	if !s.RatingLeaderboard || persona == 0 {
		return store.ModeStandard
	}
	c := &s.lbChoice
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.board[persona]; ok {
		return b
	}
	return store.ModeStandard
}

// leaderboardStateOf is user's leaderboard.state.
func (s *Server) leaderboardStateOf(user int64) LeaderboardState {
	st := LeaderboardState{Board: s.leaderboardBoard(user)}
	if s.RatingLeaderboard {
		st.Boards = rating.Modes()
	}
	return st
}

// greetUI is the UI channel's Greet: every new connection hears the player's Preferred Region (PREF1), Leaderboard
// board (MMR2) and Profile mode (PROFILE2).
func (s *Server) greetUI(user int64) []uichannel.Greeting {
	return append(s.regionGreeting(user),
		uichannel.Greeting{Event: EventLeaderboardState, Data: s.leaderboardStateOf(user)},
		uichannel.Greeting{Event: EventProfileState, Data: s.profileStateOf(user)})
}

// leaderboardSelect is leaderboard.select {board}: board must be one of the state's boards (rating.Modes, with the
// rating Leaderboard on). A new board is kept for the player and every connection of the player hears the new state;
// the same board again, or a refused call (an unknown board, the tabs off, a change within leaderboardSelectGap of
// the last), sends the player its state again (every connection of it), so its tabs show what its requests get.
func (s *Server) leaderboardSelect(user int64, args json.RawMessage) error {
	var a struct {
		Board *string `json:"board"`
	}
	refuse := func(why string) error {
		s.UI.Send(user, EventLeaderboardState, s.leaderboardStateOf(user))
		return errors.New(why)
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Board == nil {
		return refuse("no board in the arguments")
	}
	if !s.RatingLeaderboard {
		return refuse("the Leaderboard ranks by wins here (--rating-leaderboard off): no boards")
	}
	board := *a.Board
	if !slices.Contains(rating.Modes(), board) {
		return refuse("an unknown board") // the value as it came stays out of the log
	}
	c := &s.lbChoice
	c.mu.Lock()
	old, ok := c.board[user]
	if !ok {
		old = store.ModeStandard
	}
	if board == old {
		c.mu.Unlock()
		s.UI.Send(user, EventLeaderboardState, s.leaderboardStateOf(user))
		return nil
	}
	now := time.Now()
	if last, ok := c.at[user]; ok && s.lbGap > 0 && now.Sub(last) < s.lbGap {
		c.mu.Unlock()
		return refuse("a change within " + s.lbGap.String() + " of the last")
	}
	if c.board == nil {
		c.board, c.at = map[int64]string{}, map[int64]time.Time{}
	}
	if board == store.ModeStandard {
		delete(c.board, user)
	} else {
		c.board[user] = board
	}
	c.at[user] = now
	c.mu.Unlock()
	s.Log.Logf("blaze", "   leaderboard: persona %d's board %s (was %s)", user, board, old)
	s.UI.Send(user, EventLeaderboardState, s.leaderboardStateOf(user))
	return nil
}

// forgetLeaderboardBoard drops persona's board once it has no login left.
func (s *Server) forgetLeaderboardBoard(persona int64) {
	m := &s.msgs
	m.mu.Lock()
	online := len(m.online[persona]) > 0
	m.mu.Unlock()
	if online {
		return
	}
	c := &s.lbChoice
	c.mu.Lock()
	delete(c.board, persona)
	delete(c.at, persona)
	c.mu.Unlock()
}

// ShownRatingIn is ShownRating for the account's rating in game type mode (rating.Modes; MMR2: the custom lobby's
// tiles show the lobby's type): the same rule, with that type's rated games.
func (s *Server) ShownRatingIn(h store.History, mode string) (int64, bool) {
	r := h.RatingIn(mode).OrStart()
	return s.shownRating(r.Value, r.Games)
}

// tilesModeLocked is the game type whose rating the lobby's tiles show (MMR2; hg.mu held): the type of the lobby's
// rules now (rating.ModeOfRules; standard for a game without rules). When it changed since the tiles' ratings were
// read (the admin's lobby.setRules), they are read again.
func (hg *hostedGame) tilesModeLocked() string {
	mode := store.ModeStandard
	if hg.rulesOnLocked() {
		mode = rating.ModeOfRules(hg.custom.rules)
	}
	if mode != hg.tilesMode {
		hg.tilesMode = mode
		for _, hp := range hg.players {
			hp.ratingRead = false
		}
	}
	return mode
}
