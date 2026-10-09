package progress

import "strconv"

// Stats of a finished game, as the match host reports them in playerFinish.stats (docs/HOST_CONTROL.md;
// MATCH_FLOW.md *Match end and post-game (D8-static)* §4 names the keys). Missing keys count as 0.
const (
	StatWon         = "won"
	StatKills       = "kills"
	StatDeaths      = "deaths"
	StatAssists     = "assists"
	StatBestStreak  = "bestStreak"
	StatDoubleKills = "doubleKills"
	StatTripleKills = "tripleKills"
	StatTetraKills  = "tetraKills"
	StatOmniKills   = "omniKills"
	StatDurationMs  = "durationMs"
)

// PlayerStats is the Stats group the profile shows (B7a's 13 names, the AS3 PlayerStatsData fields).
const PlayerStats = "PlayerStats"

// ApplyPlayerStats adds one finished game to an account's PlayerStats values (name -> decimal string,
// missing = 0) and returns the new values [assumed meanings, D8-static §3: practice games count]:
//
//	PLAYED +1, WINS +1 for a win, KILLS/ASSISTS += the game's, KILLSINGAME/ASSISTSINGAME/STREAKINGAME =
//	the best single game, DOUBLEKILLS/TRIPLEKILLS/TETRAKILLS/OMNIKILLS += the game's multi-kills.
//
// MPS and MPSG (meaning unknown) and the deaths (no PlayerStats name) are left alone.
func ApplyPlayerStats(cur map[string]string, won bool, game map[string]int64) map[string]string {
	out := map[string]string{}
	for k, v := range cur {
		out[k] = v
	}
	get := func(name string) int64 {
		v, _ := strconv.ParseInt(out[name], 10, 64)
		return v
	}
	set := func(name string, v int64) { out[name] = strconv.FormatInt(v, 10) }
	add := func(name string, d int64) { set(name, get(name)+max(d, 0)) }
	best := func(name string, v int64) { set(name, max(get(name), v)) }
	add("PLAYED", 1)
	if won {
		add("WINS", 1)
	} else {
		add("WINS", 0)
	}
	add("KILLS", game[StatKills])
	add("ASSISTS", game[StatAssists])
	best("KILLSINGAME", game[StatKills])
	best("ASSISTSINGAME", game[StatAssists])
	best("STREAKINGAME", game[StatBestStreak])
	add("DOUBLEKILLS", game[StatDoubleKills])
	add("TRIPLEKILLS", game[StatTripleKills])
	add("TETRAKILLS", game[StatTetraKills])
	add("OMNIKILLS", game[StatOmniKills])
	return out
}

// Wins returns the WINS value of PlayerStats values (0 if missing): the main menu's wins
// (UserSessionExtendedData.DMAP[0x00070034], R11).
func Wins(stats map[string]string) int64 {
	v, _ := strconv.ParseInt(stats["WINS"], 10, 64)
	return max(v, 0)
}

// Account XP and level [assumed: build 854 reads ALEV/ALXP into its account data (+0xCFC/+0xD00,
// SetClientBasicInfo 0x00ACE210) but no reader of either was found, and no AssetData class holds an
// account XP curve, so both are ours].
const (
	XPPerGame   = 50 // every finished game
	XPPerWin    = 50 // extra for a win
	XPPerMinute = 2  // per match minute, up to 55 minutes (the loot formulas' cap)
	MaxLevel    = 30
)

// GameXP is the account XP of one finished game: 50, +50 for a win, +2 per match minute (at most 55).
func GameXP(won bool, seconds float64) uint32 {
	xp := uint32(XPPerGame)
	if won {
		xp += XPPerWin
	}
	return xp + XPPerMinute*uint32(min(max(seconds, 0)/60, 55))
}

// LevelFor is the account level of a total XP: level L needs 50 * L * (L - 1) XP in all (level 2 at 100,
// 3 at 300, 4 at 600, ...), at most MaxLevel. ALXP carries the total.
func LevelFor(xp uint32) uint32 {
	level := uint32(1)
	for level < MaxLevel && uint64(xp) >= 50*uint64(level+1)*uint64(level) {
		level++
	}
	return level
}
