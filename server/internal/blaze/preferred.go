package blaze

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/store"
	"dawngate/server/internal/uichannel"
)

// Preferred Region (PREF1, M28). Each player has a Preferred Region, an account setting (SettingPreferredRegion) that
// the play screen's dropdown sets over the UI channel (region.set) and reads there (region.state). It decides where a
// game's match host runs (gmshost.Manager.StartIn, with REGION3's fallback):
//   - a custom game (CREATE GAME): its creator's preference at the create;
//   - solo practice (PRACTICE DAWNGATE): its player's;
//   - a practice group's game and a PvP match: the vote of their humans (voteRegion): one vote each, bots none, the
//     region with the most votes, any tie for the most the home region. A practice group's game is made at its
//     creator's create, before anyone joins, so its voters are the creator and the members of the creator's party
//     (a PvP playgroup the creator is in), if any.
//
// A region the player's client can't reach (RegionRoute set and a client whose Start-Client takes no away message: a
// direct client or an earlier release) makes the game home, whoever else voted: the host machine is reached only
// through its match gateway. EA's own region choice (updateNetworkInfo's NLMP, the GameServerRegionId setting, the PvP
// request's specificRegion) no longer decides anything: the server still stores what the client saves.
//
// The ids are the server's region aliases ("eu", "na"; gmshost.LocalRegion on a server without regions); the names
// the operator's (--region-name, Start-Server.ini's region_names). The channel's side: region.state after every hello
// (uichannel.Server.Greet) and after each change, region.set {id}.

// SettingPreferredRegion is the account setting holding the player's Preferred Region (a region id; empty or no
// region of the server's = the home region). Only region.set writes it: saveUserSettings drops it.
const SettingPreferredRegion = "PreferredRegion"

// The region area's call and event on the UI channel.
const (
	CallRegionSet    = "region.set"
	EventRegionState = "region.state"
)

// RegionEntry is one region of EventRegionState.
type RegionEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RegionState is EventRegionState's data: the server's regions (the home first), its home region and the player's
// choice (its stored preference when that is one of Regions, else Home).
type RegionState struct {
	Regions  []RegionEntry `json:"regions"`
	Home     string        `json:"home"`
	Selected string        `json:"selected"`
}

// regionTable is the home region and the regions a player can prefer: the match hosts' regions when regions are on,
// else the one region gmshost.LocalRegion.
func (s *Server) regionTable() (home string, ids []string) {
	if s.Hosts != nil {
		if h := s.Hosts.HomeRegion(); h != "" {
			return h, s.Hosts.Regions()
		}
	}
	return gmshost.LocalRegion, []string{gmshost.LocalRegion}
}

// regionName is a region's display name.
func (s *Server) regionName(id string) string {
	if s.Hosts != nil {
		return s.Hosts.RegionName(id)
	}
	return gmshost.DefaultRegionName(id)
}

// preferredIn is the preference settings hold among ids: the stored region if it is one of them, else home.
func preferredIn(settings map[string]string, home string, ids []string) string {
	if p := settings[SettingPreferredRegion]; p != "" && slices.Contains(ids, p) {
		return p
	}
	return home
}

// regionStateOf is user's (a persona id) region.state.
func (s *Server) regionStateOf(user int64) RegionState {
	home, ids := s.regionTable()
	st := RegionState{Home: home, Selected: home}
	for _, id := range ids {
		st.Regions = append(st.Regions, RegionEntry{ID: id, Name: s.regionName(id)})
	}
	if s.Store != nil {
		if a, _, ok := s.Store.PersonaByID(user); ok {
			st.Selected = preferredIn(a.Settings, home, ids)
		}
	}
	return st
}

// regionGreeting is the UI channel's Greet: every new connection gets the player's region.state.
func (s *Server) regionGreeting(user int64) []uichannel.Greeting {
	return []uichannel.Greeting{{Event: EventRegionState, Data: s.regionStateOf(user)}}
}

// regionSet is region.set {id}: id must be one of the server's regions; a new one is stored and every connection of
// the player gets the new region.state. A refused call sends the caller's state back (its dropdown goes back), the same
// id as stored does nothing.
func (s *Server) regionSet(user int64, args json.RawMessage) error {
	var a struct {
		ID *string `json:"id"`
	}
	refuse := func(why string) error {
		s.UI.Send(user, EventRegionState, s.regionStateOf(user))
		return errors.New(why)
	}
	if err := json.Unmarshal(args, &a); err != nil || a.ID == nil {
		return refuse("no id in the arguments")
	}
	_, ids := s.regionTable()
	id := *a.ID
	if !slices.Contains(ids, id) {
		return refuse("an id that is no region of this server")
	}
	if s.Store == nil {
		return refuse("no account store")
	}
	acct, _, ok := s.Store.PersonaByID(user)
	if !ok {
		return refuse("no account")
	}
	was := acct.Settings[SettingPreferredRegion]
	if was == id {
		return nil
	}
	set := map[string]string{SettingPreferredRegion: id}
	if fit, why := settingsFit(acct.Settings, set); !fit { // access.go: the account's settings are at their limit
		return refuse("the account's settings are full (" + why + ")")
	}
	if _, err := s.Store.Update(acct.UserID, func(a *store.Account) {
		if a.Settings == nil {
			a.Settings = map[string]string{}
		}
		a.Settings[SettingPreferredRegion] = id
	}); err != nil {
		return refuse("not stored: " + err.Error())
	}
	if was == "" {
		was = "none"
	}
	s.Log.Logf("blaze", "   region: player %d prefers %s (was %s)", user, id, was)
	s.UI.Send(user, EventRegionState, s.regionStateOf(user))
	return nil
}

// regionVoter is one human whose Preferred Region counts for a game.
type regionVoter struct {
	persona int64
	user    int64 // the account's user id (store.Account.UserID)
	conn    *Conn // its Blaze connection (nil: unknown, counts as one that can't be sent away)
}

// voteRegion is the region most of votes name (each a region id; "" or one not in ids counts as home); any tie for the
// most is home, and no votes are home.
func voteRegion(votes []string, home string, ids []string) string {
	count := map[string]int{}
	for _, v := range votes {
		if v == "" || !slices.Contains(ids, v) {
			v = home
		}
		count[v]++
	}
	best, most, tie := home, 0, false
	for r, n := range count {
		switch {
		case n > most:
			best, most, tie = r, n, false
		case n == most:
			tie = true
		}
	}
	if tie {
		return home
	}
	return best
}

// describeVotes is a vote's count for the log: "na 5, eu 1".
func describeVotes(votes []string, home string, ids []string) string {
	count := map[string]int{}
	for _, v := range votes {
		if !slices.Contains(ids, v) {
			v = home
		}
		count[v]++
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return count[keys[i]] > count[keys[j]] || count[keys[i]] == count[keys[j]] && keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, count[k])
	}
	return strings.Join(parts, ", ")
}

// wantedBy is the region a game of what (for the log: "game 7") should run in by its voters' Preferred Regions: the
// vote's result, or home when one of them can't be sent to another region's match gateway. "" with regions off.
func (s *Server) wantedBy(what string, voters []regionVoter) string {
	home, ids := s.regionsOn()
	if home == "" {
		return ""
	}
	votes := make([]string, len(voters))
	for i, v := range voters {
		votes[i] = home
		if s.Store != nil && v.user != 0 {
			if a, ok := s.Store.Account(v.user); ok {
				votes[i] = preferredIn(a.Settings, home, ids)
			}
		}
	}
	want := voteRegion(votes, home, ids)
	how := "the player's Preferred Region"
	if len(voters) > 1 {
		how = "the players' vote: " + describeVotes(votes, home, ids)
	}
	if want != home && s.RegionRoute != nil {
		for _, v := range voters {
			if v.conn == nil || !v.conn.TunnelAddr().TakesAway() {
				s.Log.Logf("blaze", "   %s: region %s (%s) ignored: player %d's client has no tunnel session that takes the "+
					"match gateway (direct, or an earlier Start-Client); the game stays in %s", what, want, how, v.persona, home)
				return home
			}
		}
	}
	s.Log.Logf("blaze", "   %s: region %s (%s)", what, want, how)
	return want
}

// hostedVoters are the voters of a game's players.
func hostedVoters(players []*hostedPlayer) []regionVoter {
	out := make([]regionVoter, 0, len(players))
	for _, hp := range players {
		out = append(out, regionVoter{persona: hp.p.ID, user: hp.userID, conn: hp.conn})
	}
	return out
}

// partyVoters are the voters of a practice group's creator: the creator, and the members of each PvP party
// (a playgroup without a unique key) the creator is in.
func (s *Server) partyVoters(creator regionVoter) []regionVoter {
	out := []regionVoter{creator}
	seen := map[int64]bool{creator.persona: true}
	s.groups.mu.Lock()
	defer s.groups.mu.Unlock()
	for _, pg := range s.groups.byID {
		if pg.info.Key != "" || pg.indexOf(creator.persona) < 0 {
			continue
		}
		for _, m := range pg.members {
			if !seen[m.BlazeID] {
				seen[m.BlazeID] = true
				out = append(out, regionVoter{persona: m.BlazeID, user: m.AccountID, conn: m.conn})
			}
		}
	}
	return out
}
