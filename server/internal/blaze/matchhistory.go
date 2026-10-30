package blaze

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
)

// This file stores every finished match for the match history (M18): at the
// host's finishGame, after persist and postResult, one store.Match with a seat per human of the roster and per bot the
// host reported. The host's seatStats and matchTimeline (sent before the playerFinish lines) are kept on the game until
// then; a host without them (an older build, [host] match_stats=0) still gets its match stored from the roster and the
// playerFinish lines, its humans only (it reports nothing about its bots). An aborted match (no finishGame) stores
// nothing.

// statLevel is playerFinish's level key (the champion level at the end).
const statLevel = "level"

// Match kinds as the history stores them (store.Match.Kind).
const (
	matchKindPvP           = "pvp"
	matchKindCustom        = "custom"
	matchKindPracticeGroup = "practiceGroup"
	matchKindPractice      = "practice"
)

// matchKind is the history's name of a game's kind: resultKind's four kinds (a practice group is a custom game with
// BotsFillTeams in its GameFlags).
func matchKind(mode string, descriptors uint32) string {
	switch kind, _ := resultKind(mode, descriptors); kind {
	case kindPvP:
		return matchKindPvP
	case kindCustom:
		return matchKindCustom
	case kindPracticeGroup:
		return matchKindPracticeGroup
	}
	return matchKindPractice
}

// keepSeatStats keeps the host's seatStats line for the seat until finishGame (a second one for the same seat
// replaces the first).
func (hg *hostedGame) keepSeatStats(ss gmshost.SeatStats) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	if hg.seatStats == nil {
		hg.seatStats = map[int]gmshost.SeatStats{}
	}
	hg.seatStats[ss.Index] = ss
}

// keepTimeline keeps the host's matchTimeline until finishGame.
func (hg *hostedGame) keepTimeline(tl gmshost.MatchTimeline) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.timeline = &tl
}

// storeMatch stores the finished match in the history (Finish, after persist, before postResult, which gives the home
// page's ring the id) and returns its id: 0 when nothing was stored (no store, no seat, the store's error).
func (hg *hostedGame) storeMatch(f gmshost.FinishGame) int64 {
	s := hg.s
	if s.Store == nil {
		return 0
	}
	hg.mu.Lock()
	m, skipped := hg.matchLocked(f, s.Now())
	stats, timeline := len(hg.seatStats), hg.timeline != nil
	hg.mu.Unlock()
	if len(m.Seats) == 0 {
		s.Log.Logf("blaze", "   game %d: not stored in the match history (no seat)", hg.game.ID)
		return 0
	}
	id, err := s.Store.InsertMatch(m)
	if err != nil {
		s.Log.Logf("blaze", "   (game %d: not stored in the match history: %v)", hg.game.ID, err)
		return 0
	}
	bots := 0
	for _, st := range m.Seats {
		if st.Bot {
			bots++
		}
	}
	note := ""
	if skipped > 0 {
		note = fmt.Sprintf(", seatStats ignored for %d seat(s)", skipped)
	}
	s.Log.Logf("blaze", "   game %d: stored as match %d (%d players, %d bots; seatStats from the host %d, timeline %v%s)",
		hg.game.ID, id, len(m.Seats)-bots, bots, stats, timeline, note)
	return id
}

// matchLocked builds the history's record of the finished match (hg.mu held). The humans are resultLocked's (the
// roster's on teams 1 and 2; in a custom game only those who joined), each at the gameplay index the host's seatStats
// or addPlayer gave it (else its roster position), with its playerFinish stats and its seatStats' merged (seatStats'
// win on a key both have). Won is playerFinish's "won" when the host sent it, else the team is finishGame's winner.
// Then a seat per bot the host sent a seatStats for (user 0 or bot true). A seatStats line that matches no human or
// whose seat is taken is left out (skipped counts them).
func (hg *hostedGame) matchLocked(f gmshost.FinishGame, now time.Time) (m store.Match, skipped int) {
	m = store.Match{GameID: hg.game.ID, Kind: matchKind(hg.mode, hg.game.descriptors()), Mode: hg.mode,
		Level: hg.s.gameLevel(hg.game), Winner: f.WinnerTeam, Reason: f.Reason, Started: hg.started, Ended: now,
		DurationMs: f.DurationMs}
	if !hg.inGameAt.IsZero() {
		m.Started = hg.inGameAt
	}
	if hg.timeline != nil {
		if b, err := json.Marshal(hg.timeline); err == nil {
			m.Timeline = b
		}
	}
	byUser := map[int64]gmshost.SeatStats{}
	for _, ss := range hg.seatStats {
		if !ss.Bot && ss.User != 0 {
			byUser[ss.User] = ss
		}
	}
	used := map[int]bool{}
	for i, hp := range hg.players {
		if hp.p.Team < 0 || hp.p.Team > 1 || (hg.custom != nil && !hp.joined) {
			continue
		}
		seat := i
		if hp.indexed {
			seat = hp.index
		}
		ss, hasStats := byUser[hp.p.ID]
		if hasStats {
			seat = ss.Index
			delete(byUser, hp.p.ID)
		}
		if used[seat] {
			continue // two humans at one index: a roster the host didn't keep (never seen)
		}
		used[seat] = true
		pf := hg.finishes[hp.p.ID]
		st := store.MatchSeat{Seat: seat, PersonaID: hp.p.ID, UserID: hp.userID, Team: int(hp.p.Team) + 1,
			Left: hp.removed || !hp.joined}
		fillSeat(&st, f, mergeStats(pf.Stats, ss.Stats), ss, hasStats)
		m.Seats = append(m.Seats, st)
	}
	skipped = len(byUser)
	for _, idx := range slices.Sorted(maps.Keys(hg.seatStats)) {
		ss := hg.seatStats[idx]
		if !ss.Bot && ss.User != 0 {
			continue // a human's: taken above, or counted in skipped
		}
		if used[ss.Index] {
			skipped++
			continue
		}
		used[ss.Index] = true
		st := store.MatchSeat{Seat: ss.Index, Team: ss.Team, Bot: true}
		fillSeat(&st, f, mergeStats(nil, ss.Stats), ss, true)
		m.Seats = append(m.Seats, st)
	}
	slices.SortFunc(m.Seats, func(a, b store.MatchSeat) int { return a.Seat - b.Seat })
	return m, skipped
}

// mergeStats is a seat's stats: playerFinish's, then seatStats' over them (nil when both are empty).
func mergeStats(finish, seat map[string]int64) map[string]int64 {
	if len(finish)+len(seat) == 0 {
		return nil
	}
	out := maps.Clone(finish)
	if out == nil {
		out = map[string]int64{}
	}
	maps.Copy(out, seat)
	return out
}

// fillSeat sets the seat's result, shaper, role and list columns from its stats, and its sources from the seatStats.
func fillSeat(st *store.MatchSeat, f gmshost.FinishGame, stats map[string]int64, ss gmshost.SeatStats, hasStats bool) {
	st.Stats = stats
	st.Won = f.WinnerTeam != 0 && st.Team == f.WinnerTeam
	if v, ok := stats[progress.StatWon]; ok {
		st.Won = v != 0
	}
	st.Noun, st.Role = uint32(stats[progress.StatNoun]), int(stats[progress.StatRole])
	if hasStats && ss.Noun != 0 {
		st.Noun = uint32(ss.Noun)
	}
	if hasStats && ss.Role != 0 {
		st.Role = ss.Role
	}
	st.Kills, st.Deaths, st.Assists = stats[progress.StatKills], stats[progress.StatDeaths], stats[progress.StatAssists]
	st.CS, st.Gold, st.Level = stats[progress.StatCreeps], stats[progress.StatGold], stats[statLevel]
	if hasStats && len(ss.Sources) > 0 {
		if b, err := json.Marshal(ss.Sources); err == nil {
			st.Sources = b
		}
	}
}
