package blaze

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/rules"
)

// The pick rules (RULES3): All Random and the shaper pools are the humans' shaper lists, which the match host's shaper
// select offers as dgserver sends them (a player can choose only an entry of its own list with flag bit 0, 1 or 5).
//
// Where the lists come from: a custom game's host starts at the create, before the lobby's rules are final, but its
// shaper select reads the humans' lists from the roster it has when the first human's HelloReq is answered, which in a
// custom game is after startGame (the host holds every HelloReq until then, B15), and startGame's roster replaces the
// create's (each human's content comes again from lobbyContent). So the rules shape startGame's roster, here, at the
// start, after the rules' last check:
//   - a pool keeps in each human's list only the pool's shapers, each marked available (ShaperAvailable, bit 5), so a
//     player who doesn't own a pool shaper can still play it; startGame.poolClasses names the pool for the bots' draw;
//   - All Random rolls each human a shaper of the pool (no shaper twice within a team; the teams roll on their own, as
//     the client allows a shaper on both teams) with a seed of the server's (crypto/rand; logged), and the human's list
//     is that one entry; the host (startGame.rules.pick random) locks it at once.
// The host doesn't roll itself: it has no catalog of which shapers are offered (content.Listed) or in a pool, and the
// rolls stay the server's (never a client's).

// shaperSelectRuns reports whether the server gives the humans shaper lists (lobbyContent's condition).
func (s *Server) shaperSelectRuns() bool { return s.ShaperSelect.On && s.Store != nil }

// rulesPools is the pools' catalog on this server: the shapers its content offers (SHAPERPOOL: a listed shaper item
// that isn't testing-only); without content every shaper of the table.
func (s *Server) rulesPools() rules.Pools {
	if s.Content.Empty() {
		return rules.Pools{}
	}
	listed := s.Content.Listed()
	offered := map[string]bool{}
	for _, it := range s.Content.Items {
		if it.ItemType == content.ItemShaper && !it.TestingOnly && it.Asset != "" && listed(it) {
			offered[it.Shaper] = true
		}
	}
	return rules.Pools{Offered: offered}
}

// rollSeed is the seed of a start's rolls.
func (s *Server) rollSeed() uint64 {
	if s.RollSeed != nil {
		return s.RollSeed()
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 1
	}
	return binary.LittleEndian.Uint64(b[:])
}

// applyPickRules puts the start's pick rules into startGame (sg.Rules from the rules' last check; nil: nothing to do):
// the pool's lists and poolClasses, All Random's rolled lists, and the draft's (drafted: each human's drafted
// PlayerClass, RULES5b, and its declared role, DRAFT1, which nothing uses before the role preset in shaper select).
// Logged per game.
func (s *Server) applyPickRules(gid int64, sg *gmshost.StartGame, drafted map[int64]draftedSeat) {
	s.applyTestRoles(gid, sg) // DRAFT3: a test aid's declared roles (roles.go)
	if sg.Rules == nil {
		return
	}
	c := rules.Combination(*sg.Rules).Normal()
	if c.Pick == "blind" && c.ShaperPool == "all" {
		return
	}
	classOf := map[string]string{} // a shaper's default skin (lower case) -> its PlayerClass
	if !s.Content.Empty() {
		for _, it := range s.Content.Items {
			if it.ItemType == content.ItemShaper && it.Asset != "" {
				classOf[strings.ToLower(it.Asset)] = it.Shaper
			}
		}
	}
	class := func(o gmshost.ShaperOffer) string { return classOf[strings.ToLower(o.Noun)] }
	var humans []*gmshost.RosterEntry
	for i := range sg.Roster {
		if e := &sg.Roster[i]; !e.Bot && !e.Spectator {
			humans = append(humans, e)
		}
	}
	if c.ShaperPool != "all" {
		members := s.rulesPools().Members(c.ShaperPool)
		sg.PoolClasses = members
		in := map[string]bool{}
		for _, m := range members {
			in[m] = true
		}
		for _, e := range humans {
			if len(e.Shapers) == 0 {
				continue
			}
			var kept []gmshost.ShaperOffer
			for _, o := range e.Shapers {
				if in[class(o)] {
					o.Flags |= gmshost.ShaperAvailable
					kept = append(kept, o)
				}
			}
			if len(kept) == 0 { // never with the check's pool sizes; the client asserts on an empty list
				s.Log.Logf("blaze", "   game %d: shaper pool %s: user %d's list holds none of it; it keeps its list", gid,
					c.ShaperPool, e.User)
				continue
			}
			e.Shapers = kept
		}
		s.Log.Logf("blaze", "   game %d: shaper pool %s: %d shapers (%s); each human's list holds only them, all "+
			"available; the bots draw from them", gid, c.ShaperPool, len(members), strings.Join(members, ", "))
	}
	if c.Pick == "draft" || rules.IsVotePick(c.Pick) { // ARMY1: a vote's drawn shapers go the draft's way
		s.applyDraft(gid, humans, class, drafted)
		return
	}
	if c.Pick != "random" {
		return
	}
	// the classes every human's list offers (the lists differ in flags only; the pool's filter ran above)
	var candidates []string
	seats := map[int]int{}
	byTeam := map[int][]*gmshost.RosterEntry{}
	first := true
	common := map[string]bool{}
	for _, e := range humans {
		if len(e.Shapers) == 0 {
			continue
		}
		mine := map[string]bool{}
		for _, o := range e.Shapers {
			if cl := class(o); cl != "" && o.Flags&gmshost.ShaperHidden == 0 {
				mine[cl] = true
			}
		}
		if first {
			common, first = mine, false
		} else {
			for cl := range common {
				if !mine[cl] {
					delete(common, cl)
				}
			}
		}
		seats[e.Team]++
		byTeam[e.Team] = append(byTeam[e.Team], e)
	}
	for cl := range common {
		candidates = append(candidates, cl)
	}
	sort.Strings(candidates)
	seed := s.rollSeed()
	rolls := rules.Roll(candidates, seats, seed)
	var desc []string
	for team, es := range byTeam {
		sort.Slice(es, func(i, j int) bool { return es[i].Index < es[j].Index })
		for k, e := range es {
			cl := rolls[team][k]
			var got *gmshost.ShaperOffer
			for i := range e.Shapers {
				if class(e.Shapers[i]) == cl {
					got = &e.Shapers[i]
					break
				}
			}
			if cl == "" || got == nil { // never with the check's pool sizes
				desc = append(desc, fmt.Sprintf("user %d (team %d): no shaper left, keeps its list", e.User, team))
				continue
			}
			o := *got
			o.Flags |= gmshost.ShaperAvailable
			e.Shapers = []gmshost.ShaperOffer{o}
			desc = append(desc, fmt.Sprintf("user %d (index %d, team %d) -> %s", e.User, e.Index, team, o.Noun))
		}
	}
	sort.Strings(desc)
	s.Log.Logf("blaze", "   game %d: All Random (seed %d, %d shapers to roll from): %s; each list holds only the rolled "+
		"shaper and the host locks it", gid, seed, len(candidates), strings.Join(desc, "; "))
}

// applyDraft makes each human's list its drafted shaper alone (RULES5b), marked available; the host (startGame.rules.pick
// "draft") locks it at once, as All Random's. A human without a drafted shaper, or whose list lacks it, keeps its list
// (never after a draft of the checks' pool size: every seat picks, a timeout picks at random).
func (s *Server) applyDraft(gid int64, humans []*gmshost.RosterEntry, class func(gmshost.ShaperOffer) string,
	drafted map[int64]draftedSeat) {
	var desc, roles []string
	for _, e := range humans {
		// DRAFT3: the declared role goes into the roster, and the host presets it in the human's shaper select
		// record (its role page opens with it selected, still changeable)
		if r := drafted[e.User].Role; gmshost.RoleIndex(r) >= 0 {
			e.Role = r
			roles = append(roles, fmt.Sprintf("user %d (index %d) %s", e.User, e.Index, r))
		}
		cl := drafted[e.User].Shaper
		var got *gmshost.ShaperOffer
		for i := range e.Shapers {
			if cl != "" && class(e.Shapers[i]) == cl {
				got = &e.Shapers[i]
				break
			}
		}
		if got == nil {
			desc = append(desc, fmt.Sprintf("user %d (team %d): no drafted shaper in its list (%q), keeps its list",
				e.User, e.Team, cl))
			continue
		}
		o := *got
		o.Flags |= gmshost.ShaperAvailable
		e.Shapers = []gmshost.ShaperOffer{o}
		desc = append(desc, fmt.Sprintf("user %d (index %d, team %d) -> %s", e.User, e.Index, e.Team, o.Noun))
	}
	sort.Strings(desc)
	s.Log.Logf("blaze", "   game %d: the draft's shapers: %s; each list holds only the drafted shaper and the host locks it",
		gid, strings.Join(desc, "; "))
	if len(roles) > 0 {
		sort.Strings(roles)
		s.Log.Logf("blaze", "   game %d: the draft's roles: %s; the host presets each in shaper select", gid,
			strings.Join(roles, "; "))
	}
}
