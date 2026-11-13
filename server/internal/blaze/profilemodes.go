package blaze

import (
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/rating"
	"dawngate/server/internal/store"
)

// The Profile screen by game type (PROFILE2): a tab row Practice | Standard | Draft | Custom | All Modes on the
// main menu's Profile, which switches every number the screen shows (Career, Recent History, Statistics) to the
// player's games of that type. The client always asks for the same four stat groups (StatsManager_RequestPlayerStats:
// PlayerStats, ShaperStats, ShaperHistoryStats, RoleHistoryStats), so which games they count is the viewing player's
// choice on the server, as the Leaderboard's board is (leaderboardtabs.go): the front end's class
// (client_ui/profile/ProfileModes.as) calls profile.select {mode} at a tab click, and once profile.state names the new
// mode it makes the screen load its player again (its own search native for the shown name, which reloads the same
// player since the exe patch profilereload), whose requests getStatsByGroupAsync answers for the choice.
//
// What each mode counts: "all" is the account's stored profile (every finished game since the account began, kept as
// the games end: progress.ApplyPlayerStats and ApplyHistory in applyGame), which leaves out the custom games their
// admin made Unrated (store.Match.Unrated; since PROFILE2 applyGame doesn't add them). The other modes are made from
// the stored matches (store.PlayedMatches: every finished match since the match history began), the player's games
// of that type (GameType) that weren't made Unrated, with the same functions in the order they were played. So on a
// server that ran before the match history, All Modes can count more games than its four types together.

// The Profile's UI channel call and event.
const (
	CallProfileSelect = "profile.select"
	EventProfileState = "profile.state"
)

// The Profile's modes: the game types of the match history (GameType) and "all" (the default).
const (
	ProfileAll      = "all"
	ProfilePractice = "practice"
)

// ProfileModes are profile.state's modes in the tabs' order (the user's: Practice, Standard, Draft, Custom, All
// Modes).
func ProfileModes() []string {
	return []string{ProfilePractice, rating.Standard, rating.Draft, rating.Custom, ProfileAll}
}

// profileSelectGap is the least time between two changes of one player's mode (a faster one is refused and the state
// sent back): each change makes the class load the screen again, four stat requests that read the player's matches.
const profileSelectGap = 250 * time.Millisecond

// ProfileState is EventProfileState's data: the mode the player's profile requests are answered in, and the modes there
// are, in the tabs' order.
type ProfileState struct {
	Mode  string   `json:"mode"`
	Modes []string `json:"modes"`
}

// profileChoices are the players' modes (persona id -> a mode other than "all"), kept while the player is logged in:
// the last login's end forgets it (uiLoginEnded), so a new login starts on All Modes.
type profileChoices struct {
	mu   sync.Mutex
	mode map[int64]string
	at   map[int64]time.Time // the last change
}

// GameType is a stored match's game type, the match history's and the Profile's (PROFILE1, PROFILE2): "practice" for a
// practice game and a practice group's, else its rating type (rating.ModeOf: standard for PvP and a custom game
// without rules, draft for the draft preset, custom for any other rules).
func GameType(m store.Match) string {
	if m.Kind == matchKindPractice || m.Kind == matchKindPracticeGroup {
		return ProfilePractice
	}
	return rating.ModeOf(m)
}

// profileMode is the mode persona's profile requests are answered in: "all" without a choice or before a login (0).
func (s *Server) profileMode(persona int64) string {
	if persona == 0 {
		return ProfileAll
	}
	c := &s.pfChoice
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.mode[persona]; ok {
		return m
	}
	return ProfileAll
}

// profileStateOf is user's profile.state.
func (s *Server) profileStateOf(user int64) ProfileState {
	return ProfileState{Mode: s.profileMode(user), Modes: ProfileModes()}
}

// profileSelect is profile.select {mode}: mode must be one of ProfileModes (case-sensitive). A new mode is kept for the
// player and every connection of the player hears the new state; the same mode again, or a refused call (no mode, an
// unknown one, a change within profileSelectGap of the last), sends the player its state again (every connection of
// it), so its tabs show what its requests get.
func (s *Server) profileSelect(user int64, args json.RawMessage) error {
	var a struct {
		Mode *string `json:"mode"`
	}
	refuse := func(why string) error {
		s.UI.Send(user, EventProfileState, s.profileStateOf(user))
		return errors.New(why)
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Mode == nil {
		return refuse("no mode in the arguments")
	}
	mode := *a.Mode
	if !slices.Contains(ProfileModes(), mode) {
		return refuse("an unknown mode") // the value as it came stays out of the log
	}
	c := &s.pfChoice
	c.mu.Lock()
	old, ok := c.mode[user]
	if !ok {
		old = ProfileAll
	}
	if mode == old {
		c.mu.Unlock()
		s.UI.Send(user, EventProfileState, s.profileStateOf(user))
		return nil
	}
	now := time.Now()
	if last, ok := c.at[user]; ok && s.pfGap > 0 && now.Sub(last) < s.pfGap {
		c.mu.Unlock()
		return refuse("a change within " + s.pfGap.String() + " of the last")
	}
	if c.mode == nil {
		c.mode, c.at = map[int64]string{}, map[int64]time.Time{}
	}
	if mode == ProfileAll {
		delete(c.mode, user)
	} else {
		c.mode[user] = mode
	}
	c.at[user] = now
	c.mu.Unlock()
	s.Log.Logf("blaze", "   profile: persona %d's mode %s (was %s)", user, mode, old)
	s.UI.Send(user, EventProfileState, s.profileStateOf(user))
	return nil
}

// forgetProfileMode drops persona's mode once it has no login left.
func (s *Server) forgetProfileMode(persona int64) {
	m := &s.msgs
	m.mu.Lock()
	online := len(m.online[persona]) > 0
	m.mu.Unlock()
	if online {
		return
	}
	c := &s.pfChoice
	c.mu.Lock()
	delete(c.mode, persona)
	delete(c.at, persona)
	c.mu.Unlock()
}

// profileIn is the profile of persona's account in mode: for "all" acct's stored PlayerStats and history as they are;
// for a game type, both made from persona's stored matches of that type that weren't made Unrated (ProfileOf), in
// place of acct's (the rest of acct is left as it is). games is the number of games counted (-1 for "all").
func (s *Server) profileIn(persona int64, acct store.Account, mode string) (store.Account, int, error) {
	if mode == ProfileAll || s.Store == nil {
		return acct, -1, nil
	}
	ms, err := s.Store.PlayedMatches(persona, func(m store.Match) bool { return !m.Unrated && GameType(m) == mode })
	if err != nil {
		return acct, 0, err
	}
	ps, h := ProfileOf(s.Content, ms)
	stats := map[string]map[string]string{}
	for g, v := range acct.Stats {
		stats[g] = v
	}
	stats[progress.PlayerStats] = ps
	acct.Stats = stats
	acct.History.Shapers, acct.History.Days = h.Shapers, h.Days
	return acct, len(ms), nil
}

// ProfileOf is the profile (PlayerStats and the shaper and role history) of the games ms (store.PlayedMatches: one
// seat each, oldest first), each added as applyGame adds a finished game: ApplyPlayerStats with the seat's result and
// stats, ApplyHistory on the day the match ended with its shaper (the seat's noun through Content.ShaperOf), role and
// length. A seat without stats (a host without match stats) counts with its list columns.
func ProfileOf(c *content.Content, ms []store.Match) (map[string]string, store.History) {
	ps := map[string]string{}
	var h store.History
	shapers := map[uint32]uint32{}
	for _, m := range ms {
		if len(m.Seats) == 0 {
			continue
		}
		st := m.Seats[0]
		game := map[string]int64{}
		for k, v := range st.Stats {
			game[k] = v
		}
		for k, v := range map[string]int64{progress.StatKills: st.Kills, progress.StatDeaths: st.Deaths,
			progress.StatAssists: st.Assists, progress.StatCreeps: st.CS, progress.StatGold: st.Gold} {
			if _, ok := game[k]; !ok {
				game[k] = v
			}
		}
		ps = progress.ApplyPlayerStats(ps, st.Won, game)
		shaper, ok := shapers[st.Noun]
		if !ok {
			shaper, _ = c.ShaperOf(st.Noun)
			shapers[st.Noun] = shaper
		}
		h = progress.ApplyHistory(h, m.Ended, shaper, int64(st.Role), st.Won, game, float64(m.DurationMs)/1000)
	}
	return ps, h
}
