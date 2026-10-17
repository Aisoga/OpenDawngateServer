package blaze

import (
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

// LeaderboardMinGames is the fewest games a ranked account has by default: the screen's own text reads "Solo Top 200.
// Rank is based upon results of solo games only. Must have at least 50 games played." (dgserver
// --leaderboard-min-games changes it; every game of the profile's PLAYED counts, not only solo queue games
// [assumed]).
const LeaderboardMinGames = 50

// leaderboard answers getLeaderboard (LeaderboardStatsRequest, member table 0x012F0518: NAME, STRT the first rank
// wanted, COUN how many, POFF the period offset; the client sends NAME 'Skill' with POFF 0, then 'SkillDaily' with
// POFF 1, both STRT 1, COUN 200, LBID 0, USET none). Skill is the ranking of the stored accounts with at least
// Server.LeaderboardMinGames games (store.Leaderboard); SkillDaily with POFF n >= 1 the ranking at the end of the UTC
// day n days ago (POFF 1: without today's games), with POFF 0 the ranking now; any other name gets no rows. Ranks
// count from 1; STRT below 1 counts as 1.
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
	rows, err := r.Server.Store.Leaderboard(day, r.Server.LeaderboardMinGames, int(start-1+count))
	if err != nil {
		r.Server.Log.Logf("blaze", "   (leaderboard %q: the store failed: %v; no rows)", name, err)
		return LeaderboardReply(nil, 1)
	}
	if int64(len(rows)) < start {
		rows = nil
	} else {
		rows = rows[start-1:]
	}
	r.Server.Log.Logf("blaze", "   leaderboard %q: %d row(s) from rank %d", name, len(rows), start)
	return LeaderboardReply(rows, int(start))
}

// LeaderboardReply is Stats::getLeaderboard -> Blaze::Stats::LeaderboardStatValues (member table 0x012F050C: LDLS
// list of Blaze::Stats::LeaderboardStatValuesRow 0x012F0590). Each row: ENAM the display name, ENID the persona id,
// RANK (from firstRank on), STAT four decimal strings; RSTA, RWFG, RWOT, RWST and UATT keep their defaults. Without
// rows LDLS is an empty list, which the client takes as an empty leaderboard.
//
// How the client reads it (snlobby_profile_manager.cpp; the 'Skill' callback 0x00AA4FA0, read in LBFIX): per row a
// LeaderboardManager entry (0x40 bytes) with RANK, ENID, ENAM and the STAT values by position, which it asserts
// to be at least four ("otherStats.size() >= 4"): [0] kept at entry +0x28 and never shown [assumed: the skill
// value; we send the wins], [1] games played, [2] wins ("entry.gamesPlayed >= entry.wins"), [3] a shaper's catalogID,
// whose default skin's icon LeaderboardUI's cell renderer (0x0046E9E0) draws beside the name (0: the random-select
// icon). The renderer shows the rank with an arrow, the name, "played (wins/losses)" and the win ratio "%.1f%".
// Then the callback asks for 'SkillDaily', and its callback (0x00AA34B0) sets each entry's previous rank from the
// row with the same ENID (no row: "new"); the arrow is up, down or level from the two ranks.
func LeaderboardReply(rows []store.LeaderRow, firstRank int) []byte {
	list := make([]*tdf.Writer, len(rows))
	for i, row := range rows {
		stat := []string{u32s(row.Wins), u32s(row.Games), u32s(row.Wins), u32s(row.Shaper)}
		list[i] = tdf.NewWriter().String("ENAM", row.Name).Int("ENID", row.PersonaID).Int("RANK", int64(firstRank+i)).
			List("STAT", tdf.TypeString, stat)
	}
	return tdf.NewWriter().List("LDLS", tdf.TypeStruct, list).Bytes()
}

func u32s(v uint32) string { return strconv.FormatUint(uint64(v), 10) }
