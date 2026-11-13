package rating

import (
	"fmt"

	"dawngate/server/internal/rules"
	"dawngate/server/internal/store"
)

// The match kinds a rated game can be (store.Match.Kind: blaze's matchKind). A practice game and a practice group's
// (a custom game with the bots-fill bit, 0x412) never are.
const (
	kindPvP    = "pvp"
	kindCustom = "custom"
)

// The game types a rating is kept for (M30, MMR1): each account has a rating per type, and a rated game changes only
// its type's (ModeOf). The ids are the store's (store.RatingModes; the Leaderboard's tabs, RatingLeaderboard's mode).
const (
	Standard = store.ModeStandard // PvP matchmaking, or a custom game with the default rules
	Draft    = store.ModeDraft    // a custom game with exactly the rules preset "draft"
	Custom   = store.ModeCustom   // a custom game with any other rules
)

// Modes are the game types in the Leaderboard's order.
func Modes() []string { return append([]string(nil), store.RatingModes...) }

// draftPreset is the rules preset whose games are the Draft type.
const draftPreset = "draft"

// ModeOf is the match's game type (M30: the type comes from the rules, not from the lobby's tab):
// Standard for a PvP game (matchmaking has no rules) and a custom game without rules (store.Match.Rules "", or a
// combination that is every category's default); Draft for a custom game whose rules are exactly the preset "draft"
// (pick draft, every other category at its default); Custom for any other combination, and for rules this build can't
// read. A practice game is never rated; its type is Standard.
func ModeOf(m store.Match) string {
	if m.Kind != kindCustom || m.Rules == "" {
		return Standard
	}
	c, err := rules.ParseJSON([]byte(m.Rules))
	if err != nil {
		return Custom
	}
	return ModeOfRules(c)
}

// ModeOfRules is the game type of a custom game with the rules c (ModeOf's rule; MMR2: the custom lobby's tiles show
// the rating of the lobby's current type): Standard for every category at its default, Draft for exactly the preset
// "draft", Custom for any other combination.
func ModeOfRules(c rules.Combination) string {
	if c.IsDefault() {
		return Standard
	}
	if p, ok := rules.PresetOf(c); ok && p.ID == draftPreset {
		return Draft
	}
	return Custom
}

// Verdict is the rule's answer for one finished match.
type Verdict struct {
	Rated  bool
	Mode   string // the match's game type (ModeOf): the rating a rated match changes
	Humans [2]int // the humans of team 1 and team 2 at the start
	// Why is the log's text: "2 v 2 humans" (rated, or not rated for another reason when the teams are even) or the
	// reason it isn't rated ("2 v 3 humans", "a practice game", "no winner").
	Why string
}

// Judge is the user's rule for a rated game: a PvP or custom game (never practice, never a practice group's) that
// finished with a winner (team 1 or 2), that its admin didn't make unrated (store.Match.Unrated, M30), whose teams had
// the same number of humans at its start, at least one each. Since M30 (MMR1) a game with match rules is rated too, in
// its own type (Verdict.Mode, ModeOf); until then such a game never was (RULES1).
// Bots and spectators aren't humans (a spectator has no seat). The humans at the start are the match's human seats
// (blaze's matchLocked: the roster's players on teams 1 and 2, a custom game's who were in it when it started); a
// human who left during the match keeps a seat and its team's result.
func Judge(m store.Match) Verdict {
	v := Verdict{Mode: ModeOf(m)}
	for _, st := range humans(m) {
		v.Humans[st.Team-1]++
	}
	count := fmt.Sprintf("%d v %d humans", v.Humans[0], v.Humans[1])
	switch {
	case m.Kind == "practiceGroup":
		v.Why = "a practice group's game"
	case m.Kind != kindPvP && m.Kind != kindCustom:
		v.Why = "a practice game"
	case m.Unrated: // M30: the custom game's admin chose Unrated in the lobby
		v.Why = "unrated by the admin"
	case m.Winner != 1 && m.Winner != 2:
		v.Why = "no winner"
	default:
		v.Why = count
		v.Rated = v.Humans[0] == v.Humans[1] && v.Humans[0] >= 1
	}
	return v
}

// humans are the match's human seats on teams 1 and 2, each account once (the first seat).
func humans(m store.Match) []store.MatchSeat {
	var out []store.MatchSeat
	seen := map[int64]bool{}
	for _, st := range m.Seats {
		if st.Bot || st.UserID == 0 || st.PersonaID == 0 || st.Team < 1 || st.Team > 2 || seen[st.UserID] {
			continue
		}
		seen[st.UserID] = true
		out = append(out, st)
	}
	return out
}

// Change is one human's rating change in a rated game.
type Change struct {
	UserID, PersonaID int64
	Team              int
	Won               bool         // the team's result (finishGame's winner), also for a player who left
	Before, After     store.Rating // After's games and wins are Before's (progress.ApplyRated counts the game)
}

// Rate is the rating update of one match: Judge's verdict, and for a rated match a change per human, each rated
// against the other team's humans as one composite opponent (Composite) with its team's result, in the match's game
// type (Verdict.Mode). current gives an account's rating in a type before the match (the zero Rating or
// store.StartRating for one that has none). Bots are left out of both teams: the
// rule makes the human sides even, so the composite compares the humans; the bots' strength is the same on both sides
// [assumed].
func Rate(m store.Match, current func(userID int64, mode string) store.Rating) (Verdict, []Change) {
	v := Judge(m)
	if !v.Rated {
		return v, nil
	}
	hs := humans(m)
	var teams [2][]store.Rating
	before := make([]store.Rating, len(hs))
	for i, st := range hs {
		before[i] = current(st.UserID, v.Mode).OrStart()
		teams[st.Team-1] = append(teams[st.Team-1], before[i])
	}
	opp := [2]Result{Composite(teams[1]), Composite(teams[0])} // the opponent of team 1's players, of team 2's
	changes := make([]Change, len(hs))
	for i, st := range hs {
		won := st.Team == m.Winner
		o := opp[st.Team-1]
		o.Score = 0
		if won {
			o.Score = 1
		}
		changes[i] = Change{UserID: st.UserID, PersonaID: st.PersonaID, Team: st.Team, Won: won, Before: before[i],
			After: Update(before[i], []Result{o})}
	}
	return v, changes
}

// Describe is a change's log text: "1500 -> 1516 (rated: 2 v 2 humans)" (the game type's rating; the log line names a
// type other than the standard: blaze's "draft rating: ...").
func (c Change) Describe(v Verdict) string {
	return fmt.Sprintf("%.0f -> %.0f (rated: %s)", c.Before.Value, c.After.Value, v.Why)
}

// DescribeNotRated is a human's log text for a match that isn't rated: "not rated (2 v 3 humans)".
func (v Verdict) DescribeNotRated() string { return "not rated (" + v.Why + ")" }
