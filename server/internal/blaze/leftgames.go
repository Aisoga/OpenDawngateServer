package blaze

import (
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
)

// PROFILE3, the user's rules (2026-11-12, refined 2026-11-13): a game a player leaves before its end counts as a loss for
// that player, but only when the match ended by a surrender or the Guardian's death (the game the others finished), or
// had run at least 15 minutes of match time (LeftLossAfter) when it ended or was abandoned; a game every player left
// within about a minute of each other (2 or more humans: a restart after a crash) counts for nobody; a left game brings
// no rewards and no XP. The past stays as it is (no import of older games).
//
// Two cases:
//   - The game still finishes (others played on): persist counts a player who left before finishGame (not kicked) as a
//     loss, whatever its team's result, when leaverLoses(finishGame) (gamehost.go); its match-history seat says Won
//     false and Left (matchLocked). Otherwise its team's result, as before PROFILE3. The skill rating keeps RATING1's
//     rule: it scores the team's result (rating.Rate by Winner), so a rated game rates the leaver as before.
//   - The game never finishes because its players left: the last human's leave (or the last team player's, leaving
//     only spectators: SPECLEAVE) makes the server send abortGame (leave marks the game abandoned), and the host's end
//     calls countLeftGame. If the game had run LeftLossAfter since it went IN_GAME and its players didn't all leave
//     within massLeaveWindow, every human who joined the GMS, was in the game when it went IN_GAME and left by itself
//     gets a loss: the match is stored with their seats (Winner 0, every seat Left and not Won, no stats: the host
//     reports none at an abort), and each account gets applyLeft's loss. No rating (no winner: rating.Judge), no Discord
//     result, no home-page ring entry.
//
// Not counted (nobody left a running game by choice): a game that never went IN_GAME (a lobby, a match still loading or
// in shaper select), a host that gave up or never became ready, a game the admin page ended (EndHostedMatch), a custom
// lobby its admin left, a kicked player. A spectator never has a result.

// LeftGameReason is the stored match's reason for a game its players left before its end.
const LeftGameReason = "left"

// DefaultLeftLossAfter is the least match time after which a left game is a loss without a surrender or the Guardian's
// death (the user's 15 minutes; Server.LeftLossAfter, dgserver --left-loss-after).
const DefaultLeftLossAfter = 15 * time.Minute

// massLeaveWindow: a game whose 2 or more players all left within this of each other was a restart, not a loss
// (the user's "within about a minute").
const massLeaveWindow = time.Minute

// leftLossAfter is LeftLossAfter, or DefaultLeftLossAfter when unset.
func (s *Server) leftLossAfter() time.Duration {
	if s.LeftLossAfter > 0 {
		return s.LeftLossAfter
	}
	return DefaultLeftLossAfter
}

// leaverLoses reports whether a finished game f makes its leavers' result a loss: it ended by a surrender or the
// Guardian's death (the host's two finishGame reasons), or its match ran leftLossAfter.
func (s *Server) leaverLoses(f gmshost.FinishGame) bool {
	return f.Reason == "guardian" || f.Reason == "surrender" ||
		time.Duration(f.DurationMs)*time.Millisecond >= s.leftLossAfter()
}

// leftByChoiceLocked reports whether hp left the game by itself (removed on the Blaze side, not kicked by the custom
// game's admin). hg.mu held.
func (hg *hostedGame) leftByChoiceLocked(hp *hostedPlayer) bool {
	return hp.removed && !(hg.custom != nil && hg.custom.kicked[hp.p.ID])
}

// countLeftGame counts a game its players left as a loss for each of them (the host's end of an aborted game, o), by
// the rules above.
func (hg *hostedGame) countLeftGame(o gmshost.Outcome) {
	s := hg.s
	if s.Store == nil {
		return
	}
	now := s.Now()
	hg.mu.Lock()
	if !hg.abandoned || hg.endedByAdmin || !o.Ready || o.Error != nil || hg.inGameAt.IsZero() {
		hg.mu.Unlock()
		return
	}
	f := gmshost.FinishGame{Reason: LeftGameReason, DurationMs: max(now.Sub(hg.inGameAt).Milliseconds(), 0)}
	m, skipped := hg.matchLocked(f, now)
	rec := historyRecord{Match: m, skipped: skipped, stats: len(hg.seatStats), timeline: hg.timeline != nil}
	ranked, unrated, withRules := hg.ranked(), hg.adminUnratedLocked(), hg.unratedRulesLocked()
	var losers []*hostedPlayer
	var first, last time.Time
	for _, hp := range hg.players {
		if hp.joined && hp.atStart && (hp.p.Team == 0 || hp.p.Team == 1) && hg.leftByChoiceLocked(hp) {
			losers = append(losers, hp)
			if first.IsZero() || hp.leftAt.Before(first) {
				first = hp.leftAt
			}
			if hp.leftAt.After(last) {
				last = hp.leftAt
			}
		}
	}
	hg.mu.Unlock()
	if len(losers) == 0 {
		return
	}
	inGame := time.Duration(f.DurationMs) * time.Millisecond
	switch {
	case inGame < s.leftLossAfter():
		s.Log.Logf("blaze", "   game %d: its players left it after %.1f s in game, under %s: counted for nobody (PROFILE3)",
			hg.game.ID, inGame.Seconds(), s.leftLossAfter())
		return
	case len(losers) >= 2 && last.Sub(first) <= massLeaveWindow:
		s.Log.Logf("blaze", "   game %d: its %d players all left within %.1f s of each other (a restart): counted for nobody "+
			"(PROFILE3)", hg.game.ID, len(losers), last.Sub(first).Seconds())
		return
	}
	// only the humans who count have a seat: matchLocked's roster seats of a game that never finished include a PvP
	// player who never joined the GMS, which this game doesn't count
	counted := map[int64]bool{}
	for _, hp := range losers {
		counted[hp.p.ID] = true
	}
	seats := rec.Seats[:0]
	for _, st := range rec.Seats {
		if st.Bot || counted[st.PersonaID] {
			st.Won, st.Left = false, !st.Bot
			seats = append(seats, st)
		}
	}
	rec.Seats = seats
	s.Log.Logf("blaze", "   game %d: its players left it before its end (after %.1f s in game): a loss for %d player(s)",
		hg.game.ID, float64(f.DurationMs)/1000, len(losers))
	for _, hp := range losers {
		var stats map[string]int64
		for _, st := range rec.Seats {
			if st.PersonaID == hp.p.ID && st.Noun != 0 {
				stats = map[string]int64{progress.StatNoun: int64(st.Noun), progress.StatRole: int64(st.Role)}
			}
		}
		_, res, err := s.applyLeft(hp.userID, stats, float64(f.DurationMs)/1000, ranked, !unrated)
		res.Unrated = unrated
		res.Rules = withRules
		if err != nil {
			s.Log.Logf("blaze", "   (account %d: game %d not stored: %v)", hp.userID, hg.game.ID, err)
			continue
		}
		s.Log.Logf("blaze", "   account %d: game %d left before its end: lost, no rewards, no XP; %d wins", hp.userID,
			hg.game.ID, res.Wins)
		s.Log.Logf("blaze", "   account %d: game %d history: %s", hp.userID, hg.game.ID, res.describeHistory(s.Content))
		s.Log.Logf("blaze", "   account %d: game %d leaderboard: %s", hp.userID, hg.game.ID, res.describeRanked(hg.mode))
	}
	hg.storeMatch(rec)
}

// applyLeft stores a game the player left before its end, when the game never finished (countLeftGame): a loss in
// the profile (PlayerStats and, when the shaper or role is known, the shaper and role history; inProfile false, a
// custom game its admin made Unrated, leaves them alone as applyGame does) and, for a ranked game, in the
// Leaderboard's counts. No rewards, no account XP, no FTUE flag [our choice: leaving a game must not pay]. stats holds
// what is known of the player's game (its noun and role, or nothing); seconds is the time in game.
func (s *Server) applyLeft(userID int64, stats map[string]int64, seconds float64, ranked, inProfile bool) (store.Account,
	GameResult, error) {
	var res GameResult
	acct, err := s.Store.Update(userID, func(a *store.Account) {
		res.Noun, res.Role = uint32(stats[progress.StatNoun]), stats[progress.StatRole]
		res.Shaper, _ = s.Content.ShaperOf(res.Noun)
		res.NotInProfile = !inProfile
		if inProfile {
			if a.Stats == nil {
				a.Stats = map[string]map[string]string{}
			}
			a.Stats[progress.PlayerStats] = progress.ApplyPlayerStats(a.Stats[progress.PlayerStats], false, stats)
			a.History = progress.ApplyHistory(a.History, s.Now(), res.Shaper, res.Role, false, stats, seconds)
		}
		if ranked {
			a.History = progress.ApplyRanked(a.History, s.Now(), false)
		}
		res.Ranked, res.RankedTotal = ranked, a.History.Ranked
		res.Level = a.Level
		res.Wins = progress.Wins(a.Stats[progress.PlayerStats])
	})
	return acct, res, err
}
