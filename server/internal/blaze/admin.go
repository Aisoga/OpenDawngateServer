package blaze

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// The admin page's view of the running games: the games with a match
// host, their players and how long they run, and the end of one through the control channel's abortGame. The host
// then exits without finishGame, so its end (hostedGame.events End) tells every player still in the game
// NotifyPlayerRemoved GAME_DESTROYED, which takes the client back to the main menu (C1b's -HostKillAt path; H5).

// HostedMatch is one running game with a match host.
type HostedMatch struct {
	GameID  int64
	Name    string // GNAM
	Mode    string // gmshost.ModePractice, ModePvP, ModeCustom
	Started time.Time
	State   string // the Blaze game state the clients know: pre-game, in game, post-game
	Host    int64  // the match host's id (gmshost.Match.ID; 0 before it started)
	Pid     int
	Players []HostedMatchPlayer
	// HOME1, the home page's view (no names there):
	Level    string    // the level it loads (gameLevel; LevelDisplay names it)
	Kind     string    // resultKind's: "PvP match", "Custom game", "Practice group", "Practice match"
	Lobby    bool      // a custom game whose admin hasn't started it yet (its lobby isn't a match)
	InGameAt time.Time // when the clients went IN_GAME (zero while loading and in shaper select)
	// Bots are the bots on each team by the host's fill rule (botSeats, as the result post counts them) [assumed: the
	// host reports no bots; a host whose [host] bots is set differs].
	Bots [2]int
}

// HostedMatchPlayer is one human player of a hosted game (bots aren't on the Blaze side).
type HostedMatchPlayer struct {
	PersonaID int64
	UserID    int64 // the account
	Name      string
	Team      int64 // Blaze TIDX
	Joined    bool  // the host reported its GMS connection (addPlayer)
	Left      bool  // left on the Blaze side
}

func stateName(s int64) string {
	switch s {
	case GameStatePreGame:
		return "pre-game"
	case GameStateInGame:
		return "in game"
	case GameStatePostGame:
		return "post-game"
	}
	return fmt.Sprintf("state %#x", s)
}

// HostedMatches lists the running games with a match host, oldest first.
func (s *Server) HostedMatches() []HostedMatch {
	s.hosted.mu.Lock()
	games := make([]*hostedGame, 0, len(s.hosted.byID))
	for _, hg := range s.hosted.byID {
		games = append(games, hg)
	}
	s.hosted.mu.Unlock()
	out := make([]HostedMatch, 0, len(games))
	for _, hg := range games {
		hg.mu.Lock()
		if hg.ended {
			hg.mu.Unlock()
			continue
		}
		m := HostedMatch{GameID: hg.game.ID, Name: hg.game.Name, Mode: hg.mode, Started: hg.started, State: stateName(hg.state),
			Level: hg.s.gameLevel(hg.game), Lobby: hg.custom != nil && !hg.custom.started, InGameAt: hg.inGameAt}
		m.Kind, _ = resultKind(hg.mode, hg.game.descriptors())
		if hg.match != nil {
			m.Host, m.Pid = hg.match.ID, hg.match.Pid()
		}
		var humans [2]int // counted as resultLocked counts them for the bots
		for _, hp := range hg.players {
			m.Players = append(m.Players, HostedMatchPlayer{PersonaID: hp.p.ID, UserID: hp.userID, Name: hp.p.Name,
				Team: hp.p.Team, Joined: hp.joined, Left: hp.removed})
			if hp.p.Team >= 0 && hp.p.Team <= 1 && (hg.custom == nil || hp.joined) {
				humans[hp.p.Team]++
			}
		}
		m.Bots = botSeats(hg.fillTeams, hg.game.descriptors(), humans)
		hg.mu.Unlock()
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GameID < out[j].GameID })
	return out
}

// ErrNoHostedGame is EndHostedMatch's answer for a game that has no running match host (unknown, or ended).
var ErrNoHostedGame = errors.New("no running game with this id")

// EndHostedMatch ends a running game from the admin page: its host gets abortGame with the reason and exits; the
// players are told GAME_DESTROYED by the host's end, as for a crashed host. Nothing is stored for the game.
func (s *Server) EndHostedMatch(gameID int64, reason string) error {
	hg := s.hosted.get(gameID)
	if hg == nil {
		return ErrNoHostedGame
	}
	hg.mu.Lock()
	mt, ended := hg.match, hg.ended
	hg.mu.Unlock()
	if ended || mt == nil {
		return ErrNoHostedGame
	}
	s.Log.Logf("blaze", "   game %d: ending it (%s): abortGame to match host %d", gameID, reason, mt.ID)
	mt.Abort(reason)
	return nil
}
