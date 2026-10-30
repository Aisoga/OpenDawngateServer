package blaze

// HOME1: what the client's home page shows of the server (dgserver's home.json, internal/dgserver/home.go): the last
// finished matches and the open custom lobbies, both without player names (the website's rule). The results live in
// memory only: a restart forgets them.

import (
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/content"
)

// RecentResultsKept is how many finished matches RecentResults remembers.
const RecentResultsKept = 10

// RecentResult is one finished match without names: matchResult's mode, map, winner and length, and per team the
// humans' shapers and the bots.
type RecentResult struct {
	GameID int64
	// MatchID is the match's id in the match history (store.Match.ID, MHIST1); 0 when it wasn't stored (the store
	// refused it, or it had no seat).
	MatchID    int64
	Mode       string // gmshost.ModePvP, ModeCustom, ModePractice
	Kind       string // resultKind's: "PvP match", "Custom game", "Practice group", "Practice match"
	Level      string
	WinnerTeam int    // 1 or 2; anything else = no winner
	Reason     string // finishGame's: "guardian", "surrender", ...
	Duration   time.Duration
	At         time.Time
	Teams      [2]RecentTeam
}

// RecentTeam is one team of a RecentResult.
type RecentTeam struct {
	// Shapers are its humans' shapers by the content's title (shaperTitle: "Fenmore"), in roster order; "" for a
	// human the host reported no result for.
	Shapers []string
	Bots    int
}

// recentResults is a ring of the last RecentResultsKept results.
type recentResults struct {
	mu   sync.Mutex
	list []RecentResult // oldest first
}

func (r *recentResults) add(x RecentResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.list) >= RecentResultsKept {
		r.list = append(r.list[:0], r.list[len(r.list)-RecentResultsKept+1:]...)
	}
	r.list = append(r.list, x)
}

// recentResultOf drops the names of r and resolves its shapers.
func recentResultOf(r matchResult, c *content.Content) RecentResult {
	x := RecentResult{GameID: r.GameID, Mode: r.Mode, Kind: r.Kind, Level: r.Level, WinnerTeam: r.WinnerTeam,
		Reason: r.Reason, Duration: r.Duration, At: r.At}
	for t := range x.Teams {
		x.Teams[t].Bots = r.Bots[t]
		x.Teams[t].Shapers = []string{}
	}
	for _, p := range r.Players {
		if p.Team != 1 && p.Team != 2 {
			continue
		}
		name := ""
		if p.Result {
			name = shaperTitle(p.Noun, c)
		}
		x.Teams[p.Team-1].Shapers = append(x.Teams[p.Team-1].Shapers, name)
	}
	return x
}

// RecentResults are the last finished matches with a host (at most RecentResultsKept, every mode), newest first.
func (s *Server) RecentResults() []RecentResult {
	s.recent.mu.Lock()
	defer s.recent.mu.Unlock()
	out := make([]RecentResult, len(s.recent.list))
	for i, x := range s.recent.list {
		for t := range x.Teams {
			x.Teams[t].Shapers = append([]string{}, x.Teams[t].Shapers...)
		}
		out[len(out)-1-i] = x
	}
	return out
}

// CustomLobby is one custom game the game browser lists, without its players' names.
type CustomLobby struct {
	GameID int64
	// Title is the game's name as its creator typed it, without the server's prefixes ("[Pass] ", "[Bots] ") and tags.
	Title      string
	Level      string
	Bots       bool // bots fill the open seats when it starts (the creator's checkbox, or dgserver --custom-bots)
	Private    bool // the browser's padlock: joining needs the password
	Started    bool // its admin started it (the browser still lists it)
	Players    int  // the humans on its two teams
	Spectators int  // the others in it (Blaze team 2, SpectatorTeam)
	Seats      int  // the two teams' seats (TCAP × 2; the spectator team's don't count)
}

// CustomLobbies are the custom games the game browser lists (listedGames), by game id.
func (s *Server) CustomLobbies() []CustomLobby {
	var out []CustomLobby
	for _, lg := range s.listedGames() {
		g := lg.Game
		c := CustomLobby{GameID: g.ID, Level: s.gameLevel(g), Private: attrValue(g.Attrs, AttrPrivateMatch) == "1",
			Started: lg.State != GameStatePreGame, Seats: int(g.TeamCap) * min(len(g.TeamIDs), SpectatorTeam)}
		title := g.Name
		if l, ok := FindLevel(c.Level); ok && l.Label != "" {
			title = strings.TrimPrefix(title, l.Label)
		}
		if t, ok := strings.CutPrefix(title, BotFillPrefix); ok {
			title, c.Bots = t, true
		}
		c.Title = title
		if hg := s.hosted.get(g.ID); hg != nil {
			hg.mu.Lock()
			c.Bots = c.Bots || hg.fillTeams > 0
			c.Started = c.Started || hg.custom.started
			hg.mu.Unlock()
		}
		for _, p := range lg.Players {
			if p.Team >= 0 && p.Team < SpectatorTeam {
				c.Players++
			} else {
				c.Spectators++
			}
		}
		out = append(out, c)
	}
	return out
}
