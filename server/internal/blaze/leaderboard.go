package blaze

import (
	"fmt"
	"strconv"

	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// The main menu's Leaderboard (Stats::getLeaderboard). Unanswered until LBFIX, the request kept the client from
// sending its keep-alive pings, and once the client sent nothing else for 90 s (connIdleTimeout, PreAuthReply) it
// closed its Blaze connection and went back to the login screen.

// The leaderboards the client asks for (strings at 0x012B30xx, the sender 0x00AA2D90): the ranking itself, then,
// from its reply's callback, the ranking a day ago for the rank arrows.
const (
	LeaderboardSkill      = "Skill"
	LeaderboardSkillDaily = "SkillDaily"
)

// leaderboardMax is the most rows a reply carries (the client asks for COUN 200).
const leaderboardMax = 200

// LeaderboardMinGames is the fewest games a ranked account has by default (dgserver --leaderboard-min-games changes
// it): rated games (the balanced PvP and custom games, internal/rating) in the rating ranking (Server.RatingLeaderboard, the
// default), else PvP and custom games of any balance (store.History.Ranked, hostedGame.ranked); practice games never
// count. The client's own text said "Solo Top 200. Rank is based upon results of solo games only. Must have at least
// 50 games played."; the player copy's setup replaces it with "Top 200" and "Rank is based upon skill rating from
// balanced PvP and custom games. Must have at least 10 rated games." (localepatch.LeaderboardLines, whose number is
// this one: a server that changes the flag, or turns the rating off, makes the screen's text wrong).
const LeaderboardMinGames = 10

// leaderboard answers getLeaderboard (LeaderboardStatsRequest, member table 0x012F0518: NAME, STRT the first rank
// wanted, COUN how many, POFF the period offset; the client sends NAME 'Skill' with POFF 0, then 'SkillDaily' with
// POFF 1, both STRT 1, COUN 200, LBID 0, USET none). Skill is the ranking of the stored accounts with at least
// Server.LeaderboardMinGames games: by skill rating with Server.RatingLeaderboard (store.RatingLeaderboard, rated games), else
// by wins (store.Leaderboard); SkillDaily with POFF n >= 1 the ranking at the end of the UTC
// day n days ago (POFF 1: without today's games), with POFF 0 the ranking now; any other name gets no rows. Ranks
// count from 1; STRT below 1 counts as 1. With the rating ranking both rank by the rating of the board the player
// picked on the screen's tabs (MMR2, leaderboardtabs.go: standard, draft or custom; standard before a login).
func (r *Request) leaderboard() []byte {
	name, _ := r.Body.Str("NAME")
	start, _ := r.Body.Int("STRT")
	count, _ := r.Body.Int("COUN")
	poff, _ := r.Body.Int("POFF")
	start = min(max(start, 1), 1<<20)
	count = min(max(count, 0), leaderboardMax)
	var day string // the first day whose games are taken out ("" = none)
	switch name {
	case LeaderboardSkill:
	case LeaderboardSkillDaily:
		if poff >= 1 {
			day = r.Server.Now().UTC().AddDate(0, 0, -int(min(poff, 1000)-1)).Format("2006-01-02")
		}
	default:
		r.Server.Log.Logf("blaze", "   (leaderboard %q unknown: no rows)", name)
		return LeaderboardReply(nil, 1)
	}
	// rows from rank start to start + count - 1
	var persona int64
	if sess := r.Conn.Session; sess != nil {
		persona = sess.Persona.ID
	}
	board := r.Server.leaderboardBoard(persona) // MMR2: the tab the player picked
	ranking := r.Server.Store.Leaderboard
	if r.Server.RatingLeaderboard {
		ranking = func(day string, minGames, limit int) ([]store.LeaderRow, error) {
			return r.Server.Store.RatingLeaderboard(board, day, minGames, limit)
		}
	}
	rows, err := ranking(day, r.Server.LeaderboardMinGames, int(start-1+count))
	if err != nil {
		r.Server.Log.Logf("blaze", "   (leaderboard %q: the store failed: %v; no rows)", name, err)
		return LeaderboardReply(nil, 1)
	}
	if int64(len(rows)) < start {
		rows = nil
	} else {
		rows = rows[start-1:]
	}
	by := "" // the wins ranking's line as before
	if r.Server.RatingLeaderboard {
		by = " (by rating)"
		if board != store.ModeStandard {
			by = " (by " + board + " rating)"
		}
	}
	shown := 0
	if name == LeaderboardSkill {
		rows, shown = r.Server.markRatings(rows)
	}
	if shown > 0 {
		by += fmt.Sprintf(", %d with the rating shown", shown)
	}
	r.Server.Log.Logf("blaze", "   leaderboard %q: %d row(s) from rank %d%s", name, len(rows), start, by)
	return LeaderboardReply(rows, int(start))
}

// The Leaderboard's Rating column (RATINGSHOW2). The client keeps a row's STAT[0] (entry +0x28) but no native of
// LeaderboardUI hands it to the screen: the cell provider's row objects (leaderboardCellItem, built by 0x0046E9E0)
// carry only rank, name, gamesPlayed and winRatio, each {text, flags} with the text in HTML. The name's text is
// "<font size='16'><p align='left'>&nbsp;<img .../> " + ENAM + "</p></font>", with ENAM put in unescaped, and the
// context menu and the search go by ENID and the typed text, so ENAM can carry the number: the setup's class in the
// front end's movie (client_ui/leaderboard/LeaderboardRating.as) finds RatingMark in the name, takes it out and shows
// the number in a column of its own; a row without it shows none, and a reply without any gets no column (the
// switch). Scaleform's htmlText drops the unknown tag, so a client without the class shows the plain name.

// RatingMark is the tag the 'Skill' reply appends to the name of a row whose rating players see (ShownRating's rule:
// show_rating on and at least the Leaderboard's rated games).
func RatingMark(rating int64) string { return "<dgr r='" + strconv.FormatInt(rating, 10) + "'/>" }

// markRatings returns the rows with RatingMark appended to each name whose rating players see, and how many.
func (s *Server) markRatings(rows []store.LeaderRow) ([]store.LeaderRow, int) {
	var out []store.LeaderRow
	n := 0
	for i, row := range rows {
		v, ok := s.shownRating(float64(row.Rating), row.RatedGames)
		if !ok {
			continue
		}
		if out == nil {
			out = append([]store.LeaderRow(nil), rows...)
		}
		out[i].Name = row.Name + RatingMark(v)
		n++
	}
	if out == nil {
		return rows, 0
	}
	return out, n
}

// LeaderboardReply is Stats::getLeaderboard -> Blaze::Stats::LeaderboardStatValues (member table 0x012F050C: LDLS
// list of Blaze::Stats::LeaderboardStatValuesRow 0x012F0590). Each row: ENAM the display name, ENID the persona id,
// RANK (from firstRank on), STAT four decimal strings; RSTA, RWFG, RWOT, RWST and UATT keep their defaults. Without
// rows LDLS is an empty list, which the client takes as an empty leaderboard.
//
// How the client reads it (snlobby_profile_manager.cpp; the 'Skill' callback 0x00AA4FA0, read in LBFIX): per row a
// LeaderboardManager entry (0x40 bytes) with RANK, ENID, ENAM and the STAT values by position, which it asserts
// to be at least four ("otherStats.size() >= 4"): [0] kept at entry +0x28 and never shown [assumed: the skill
// value; we send row.Skill: the rating rounded, or the wins in the wins ranking], [1] games played, [2] wins
// ("entry.gamesPlayed >= entry.wins"), [3] a shaper's catalogID,
// whose default skin's icon LeaderboardUI's cell renderer (0x0046E9E0) draws beside the name (0: the random-select
// icon). The renderer shows the rank with an arrow, the name, "played (wins/losses)" and the win ratio "%.1f%".
// Then the callback asks for 'SkillDaily', and its callback (0x00AA34B0) sets each entry's previous rank from the
// row with the same ENID (no row: "new"); the arrow is up, down or level from the two ranks.
func LeaderboardReply(rows []store.LeaderRow, firstRank int) []byte {
	list := make([]*tdf.Writer, len(rows))
	for i, row := range rows {
		stat := []string{u32s(row.Skill), u32s(row.Games), u32s(row.Wins), u32s(row.Shaper)}
		list[i] = tdf.NewWriter().String("ENAM", row.Name).Int("ENID", row.PersonaID).Int("RANK", int64(firstRank+i)).
			List("STAT", tdf.TypeString, stat)
	}
	return tdf.NewWriter().List("LDLS", tdf.TypeStruct, list).Bytes()
}

func u32s(v uint32) string { return strconv.FormatUint(uint64(v), 10) }
