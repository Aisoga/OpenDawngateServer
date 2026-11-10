package blaze

// SPECSTATS: the match a custom game's spectator watched last. A spectator has no seat in the match history (matchLocked
// counts teams 1 and 2 only) and gets no result, rating or profile stats (persist), so its own newest match is the last
// one it played; but its After Action Review's MATCH STATS tab (history.html?latest=1) must show the match it just
// watched. The server notes, per persona, the stored match it last watched; games.json hands it to the asking player
// (dgserver's history.go, "watched"), and the page opens whichever of the two is newer. Memory only: a restart forgets
// it (the review it is for is shown right after the match), and a match the store refused is noted by nobody.

import "sync"

// watchedMatches is the last stored match each persona watched as a spectator (Server.watched).
type watchedMatches struct {
	mu sync.Mutex
	by map[int64]int64 // persona id -> match id (store.Match.ID)
}

// noteWatched records match as the last one each of the personas watched (storeMatch, after the store gave the id).
func (s *Server) noteWatched(personas []int64, match int64) {
	if match <= 0 || len(personas) == 0 {
		return
	}
	s.watched.mu.Lock()
	defer s.watched.mu.Unlock()
	if s.watched.by == nil {
		s.watched.by = map[int64]int64{}
	}
	for _, p := range personas {
		if match > s.watched.by[p] { // ids only grow; never step back to an older match
			s.watched.by[p] = match
		}
	}
}

// WatchedMatch is the match history id of the last match the persona watched as a spectator since the server started,
// 0 for none.
func (s *Server) WatchedMatch(persona int64) int64 {
	s.watched.mu.Lock()
	defer s.watched.mu.Unlock()
	return s.watched.by[persona]
}

// spectatorsLocked are the personas who watched the match to its end (hg.mu held): a custom game's spectators whose
// host reported them (addPlayer) and who were still in the game at the finish. A spectator who left before the end
// sees no review of it.
func (hg *hostedGame) spectatorsLocked() []int64 {
	if hg.custom == nil {
		return nil
	}
	var out []int64
	for _, hp := range hg.players {
		if hp.p.Team == SpectatorTeam && hp.joined && !hp.removed {
			out = append(out, hp.p.ID)
		}
	}
	return out
}
