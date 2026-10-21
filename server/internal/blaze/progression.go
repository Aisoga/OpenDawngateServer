package blaze

import (
	"fmt"
	"math/rand/v2"
	"strconv"

	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
)

// Progression after a hosted game (roadmap B7c): when the match host reports finishGame, every roster
// player who joined the GMS gets the game counted (progress: PlayerStats, account XP and level, the
// post-game rewards with the server Lua's loot-bag formulas, the main menu's wins; since B7d the shaper and
// role history of the profile), the host gets each player's rewards back (playerRewards, for its C1 04),
// and a player still connected gets a fresh UserSessionExtendedDataUpdate so the main menu's wins follow
// at once.
//
// D8c: in a game with a karma vote (a matchmade game, progress.Game.KarmaVote) the rewards wait for the vote:
// finishGame stores everything else, the host's playerKarma (after the vote has closed) brings the karma votes,
// and only then is the bag decided with the karma points in its score, stored and sent (playerRewards with
// karma). A player the host reports no karma for gets the rewards without karma when the host has gone.

// globalRand is math/rand/v2's global source (safe for concurrent use).
type globalRand struct{}

func (globalRand) Float64() float64 { return rand.Float64() }

// random is the rewards' random source: Server.Rand (tests) or the global one.
func (s *Server) random() progress.Rand {
	if s.Rand != nil {
		return s.Rand
	}
	return globalRand{}
}

// descriptors is the game's GameFlags attribute (nGameDescriptors; B8's practice game sends "1292" =
// 0x50C, B10a's matchmade game "1"); 0 if missing.
func (g Game) descriptors() uint32 {
	for _, a := range g.Attrs {
		if k, _ := a.Key.(string); k == "GameFlags" {
			v, _ := a.Value.(string)
			n, _ := strconv.ParseUint(v, 10, 32)
			return uint32(n)
		}
	}
	return 0
}

// toWire and fromWire convert rewards between progress and the control channel (gmshost).
func toWire(r progress.Rewards) gmshost.Rewards {
	w := gmshost.Rewards{LootBag: r.LootBag, Destiny: r.Destiny, CC: r.CC, FTUE: r.FTUE, BaseRoll: r.BaseRoll,
		WinPoints: r.WinPoints, WinAltPoints: r.WinAltPoints, FirstWinPoints: r.FirstWinPoints,
		Items: []gmshost.RewardItem{}}
	for _, it := range r.Items {
		w.Items = append(w.Items, gmshost.RewardItem{CatalogID: it.CatalogID, Duplicate: it.Duplicate, Alternate: it.Alternate})
	}
	if r.Karma != nil { // D8c: the karma dgserver counted (a matchmade game)
		w.Karma = &gmshost.Karma{Points: r.KarmaPoints, Party: r.Karma.Party, Team: r.Karma.Team, Enemy: r.Karma.Enemy}
	}
	return w
}

// GameResult is what one player's finished game changed, for the log and the tests.
type GameResult struct {
	Won     bool
	XP      uint32 // gained
	Level   uint32 // after
	Wins    int64  // PlayerStats WINS after
	Rewards progress.Rewards
	// RewardsHeld (D8c): the game has a karma vote, so its rewards wait for the host's playerKarma (applyRewards);
	// Rewards is empty.
	RewardsHeld bool
	// B7d: the shaper and role history. Noun is playerFinish.stats.noun, Shaper its shaper's catalogID (0:
	// none, not counted by shaper), Role the role key (outside 1-4: not counted by role).
	Noun, Shaper uint32
	Role         int64
	// Ranked: the game counts for the Leaderboard (hostedGame.ranked); RankedTotal the account's Leaderboard games
	// and wins after it.
	Ranked      bool
	RankedTotal store.GamesWon
}

// describeRanked is the log text of what a game of this mode added to the Leaderboard's counts.
func (r GameResult) describeRanked(mode string) string {
	if !r.Ranked {
		kind := "a practice game"
		if mode != gmshost.ModePractice {
			kind = "a practice group's game"
		}
		return fmt.Sprintf("not counted (%s); %d games, %d wins", kind, r.RankedTotal.Games, r.RankedTotal.Wins)
	}
	return fmt.Sprintf("counted (%s game); %d games, %d wins", mode, r.RankedTotal.Games, r.RankedTotal.Wins)
}

// describeHistory is the log text of what a game added to the shaper and role history.
func (r GameResult) describeHistory(c *content.Content) string {
	shaper := "no shaper (no noun in playerFinish)"
	switch {
	case r.Shaper != 0:
		name := ""
		if it, ok := c.Item(r.Shaper); ok {
			name = it.Name + " "
		}
		shaper = fmt.Sprintf("shaper %s(%#x)", name, r.Shaper)
	case r.Noun != 0:
		shaper = fmt.Sprintf("no shaper (noun %#x is none in the content)", r.Noun)
	}
	role := "no role"
	if r.Role >= progress.RoleGladiator && r.Role <= progress.RolePredator {
		role = fmt.Sprintf("role %d (%s)", r.Role, progress.RoleNames[r.Role])
	} else if r.Role != 0 {
		role = fmt.Sprintf("no role (role %d is none of 1-4)", r.Role)
	}
	return shaper + ", " + role
}

// applyGame stores one player's finished game in the account and returns what changed. The rewards
// (grantRewards) are part of it unless holdRewards (D8c: a game with a karma vote, whose rewards wait for the
// host's playerKarma and applyRewards). A ranked game (hostedGame.ranked) also adds to the Leaderboard's counts.
func (s *Server) applyGame(userID int64, g progress.Game, stats map[string]int64, hostRewards *gmshost.Rewards,
	practice, ranked, holdRewards bool) (store.Account, GameResult, error) {
	today := s.Now().UTC().Format("2006-01-02")
	var res GameResult
	acct, err := s.Store.Update(userID, func(a *store.Account) {
		if holdRewards {
			res.RewardsHeld = true
		} else {
			res.Rewards = s.grantRewards(a, g, hostRewards, today)
		}
		if practice {
			a.Flags |= store.FlagFTUEDone
		}
		a.Stats[progress.PlayerStats] = progress.ApplyPlayerStats(a.Stats[progress.PlayerStats], g.Won, stats)
		res.Noun, res.Role = uint32(stats[progress.StatNoun]), stats[progress.StatRole]
		res.Shaper, _ = s.Content.ShaperOf(res.Noun)
		a.History = progress.ApplyHistory(a.History, s.Now(), res.Shaper, res.Role, g.Won, stats, g.Seconds)
		if ranked {
			a.History = progress.ApplyRanked(a.History, s.Now(), g.Won)
		}
		res.Ranked, res.RankedTotal = ranked, a.History.Ranked
		res.XP = progress.GameXP(g.Won, g.Seconds)
		a.XP += res.XP
		a.Level = max(a.Level, progress.LevelFor(a.XP))
		res.Won, res.Level = g.Won, a.Level
		res.Wins = progress.Wins(a.Stats[progress.PlayerStats])
	})
	return acct, res, err
}

// applyRewards decides and stores the rewards of a game whose rewards were held for its karma vote (D8c): g
// carries the karma votes the host reported (playerKarma), or none if the host ended without a report.
func (s *Server) applyRewards(userID int64, g progress.Game, hostRewards *gmshost.Rewards) (progress.Rewards, error) {
	today := s.Now().UTC().Format("2006-01-02")
	var r progress.Rewards
	_, err := s.Store.Update(userID, func(a *store.Account) { r = s.grantRewards(a, g, hostRewards, today) })
	return r, err
}

// grantRewards decides one player's rewards for a game and adds them to the account: Destiny (EC), the crafting
// currency of duplicates (CC), the items that aren't duplicates, the day of the first win, and FLAG bit 0 for an
// FTUE reward. The host's own rewards (playerFinish.rewards) are kept if it sent some, with their duplicates
// decided again from the inventory (since D8c with the karma votes added, but no new bag); otherwise the server
// computes them (progress.Compute; the karma counts in the bag score).
func (s *Server) grantRewards(a *store.Account, g progress.Game, hostRewards *gmshost.Rewards,
	today string) progress.Rewards {
	g.FirstWin = g.Won && a.FirstWinDay != today
	owned := func(id uint32) uint32 { return a.Items[id] }
	var r progress.Rewards
	if hostRewards != nil {
		r = progress.Rewards{LootBag: hostRewards.LootBag, Destiny: hostRewards.Destiny,
			BaseRoll: hostRewards.BaseRoll, WinPoints: hostRewards.WinPoints, WinAltPoints: hostRewards.WinAltPoints,
			FirstWinPoints: hostRewards.FirstWinPoints, FTUE: hostRewards.FTUE}
		if s.Content != nil {
			for _, b := range s.Content.LootBags {
				if b.ID == hostRewards.LootBag {
					r.Bag = b.Name
				}
			}
		}
		var ids []uint32
		for _, it := range hostRewards.Items {
			ids = append(ids, it.CatalogID)
		}
		r.Items, r.CC = progress.Resolve(s.Content, owned, ids)
		r.AddKarma(g)
	} else {
		r = progress.Compute(s.Content, owned, g, s.random())
	}
	if r.FirstWinPoints > 0 {
		a.FirstWinDay = today
	}
	a.EC += r.Destiny
	a.CC += r.CC
	for _, it := range r.Items {
		if !it.Duplicate {
			a.Items[it.CatalogID]++
		}
	}
	if r.FTUE {
		a.Flags |= store.FlagFTUEDone
	}
	return r
}

// describe is the log text of a game's result.
func (r GameResult) describe() string {
	outcome := "lost"
	if r.Won {
		outcome = "won"
	}
	rewards := "rewards wait for the karma vote (playerKarma)"
	if !r.RewardsHeld {
		rewards = describeRewards(r.Rewards)
	}
	return fmt.Sprintf("%s, +%d XP (level %d), %d wins; %s", outcome, r.XP, r.Level, r.Wins, rewards)
}

// describeRewards is the log text of a game's rewards; the karma only where it was counted (D8c).
func describeRewards(r progress.Rewards) string {
	items := ""
	for i, it := range r.Items {
		if i > 0 {
			items += " "
		}
		items += fmt.Sprintf("%#x", it.CatalogID)
		if it.Duplicate {
			items += fmt.Sprintf("(duplicate, %d CC)", it.Alternate)
		}
	}
	s := fmt.Sprintf("bag %q, %d Destiny, items [%s], %d CC, FTUE %v", r.Bag, r.Destiny, items, r.CC, r.FTUE)
	if r.Karma != nil {
		s += fmt.Sprintf(", score %d with karma %d (votes: party %d, team %d, enemy %d)", r.Score(), r.KarmaPoints,
			r.Karma.Party, r.Karma.Team, r.Karma.Enemy)
	}
	return s
}
