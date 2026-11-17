package blaze

import (
	"strings"

	"dawngate/server/internal/rules"
)

// A custom game's starting rules (RULES6). A custom game with a match host starts its lobby with, in this order:
//
//   - dgserver --test-rules (a test aid), when set;
//   - its map's suggested lobby setup (mapc's rules.lobbyPreset: PlayableLevel.LobbyPreset), a preset's id or rules;
//   - the server's default lobby start (Start-Server.ini's default_rules: Server.DefaultRules, a preset's id);
//   - the standard game.
//
// A suggestion or default the server can't use is skipped with a log line: an id no preset of this server has (the
// operator's presets: Server.RulePresets), or a combination the lobby's own checks refuse in this game (a pool too small
// for its bots, a win condition on a map with its own, an option this server can't play). The checks run twice: at the
// create, with what is known then (the host's features aren't: every option is assumed playable), and when the match
// host's hello is in (gameReady), where a refusal starts the lobby on the standard game, before anyone but its
// creator can see it. Either way the admin may change the rules afterwards as always.

// rulesStart is one candidate of a lobby's starting rules: where it comes from (the log's words) and its spec.
type rulesStart struct{ from, spec string }

// startingRules is a new custom game's starting rules and their source's words ("" = the standard game), judged in ctx
// (the create's view: rulesCreateContext). Each skipped candidate is logged.
func (s *Server) startingRules(gid int64, level string, ctx rules.Context) (rules.Combination, string) {
	var cands []rulesStart
	if l, ok := FindLevel(level); ok && l.LobbyPreset != "" {
		cands = append(cands, rulesStart{"the map's suggested lobby setup", l.LobbyPreset})
	}
	if s.DefaultRules != "" {
		cands = append(cands, rulesStart{"the server's default_rules", s.DefaultRules})
	}
	for _, c := range cands {
		comb, err := s.RulePresets.Resolve(c.spec)
		if err != nil {
			why := strings.TrimPrefix(err.Error(), "rules: ")
			if strings.Contains(why, "unknown preset") {
				why = "this server has no preset of that id (Start-Server.ini's rule_presets)"
			}
			s.Log.Logf("blaze", "   game %d: %s %q isn't used: %s", gid, c.from, c.spec, why)
			continue
		}
		comb = comb.Normal()
		if fs := rules.Check(comb, ctx); len(fs) > 0 {
			s.Log.Logf("blaze", "   game %d: %s %q (%s) isn't used: the lobby's checks refuse it (%s)", gid, c.from,
				c.spec, rules.Describe(comb), rules.DescribeFindings(fs))
			continue
		}
		return comb, c.from + " " + quoteSpec(c.spec)
	}
	return rules.Standard(), ""
}

// quoteSpec is a start's spec for the log: "%q" of it.
func quoteSpec(spec string) string { return `"` + spec + `"` }

// rulesCreateContext is the checks' view of a custom game at its create, before its host's hello: lobby's bots on its
// team capacity, the server's pools and shaper select, the map's victory, a host assumed to play every option.
func (s *Server) rulesCreateContext(lobby *customLobby, teamCap int64, level string) rules.Context {
	l, _ := FindLevel(level)
	ctx := rules.Context{Bots: lobby.bots, TeamSize: int(teamCap), HostRules: true,
		HostFeatures: append([]string(nil), rules.AllHostFeatures...), Catalog: s.rulesPools(),
		NoShaperSelect: !s.shaperSelectRuns(), MapVictory: l.MapRules.Victory()}
	if ctx.Bots {
		ctx.BotSeats = int(teamCap) // the creator's team leaves one fewer; TeamSize counts anyway
	}
	return ctx
}

// rulesStartReadyLocked judges a suggested or default start again once the match host's hello is in (gameReady): if
// the lobby's checks refuse it now (the host is older, ...), the lobby starts on the standard game instead, logged; true
// when the rules changed (the caller renames the game; hg.mu held).
func (hg *hostedGame) rulesStartReadyLocked() bool {
	lobby := hg.custom
	if lobby == nil || lobby.rulesFrom == "" || !lobby.rulesSetAt.IsZero() || !hg.rulesOnLocked() {
		return false
	}
	fs := rules.Check(lobby.rules, hg.rulesContextLocked()) // the combination's own checks, not the map's host part
	if len(fs) == 0 {
		return false
	}
	hg.s.Log.Logf("blaze", "   game %d: %s (%s) isn't used: the lobby's checks refuse it with this match host (%s); the "+
		"lobby starts on Standard", hg.game.ID, lobby.rulesFrom, rules.Describe(lobby.rules), rules.DescribeFindings(fs))
	lobby.rules, lobby.rulesFrom = rules.Standard(), ""
	hg.renameLocked()
	return true
}
