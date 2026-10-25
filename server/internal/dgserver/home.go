package dgserver

// HOME1: the home page's live data, home.json. The client's home page (web/www/home.html) polls it on the plain HTTP
// port and the tunnel's (the one *web.HTTP serves both; web/home.go answers it). It holds no player names, only a
// custom game's title as its creator typed it (the website's rule), and costs no store query: everything comes from
// the Blaze server's memory, and the JSON is made at most once per homeCacheFor.

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/matchmaker"
)

// homeVersion is home.json's format version: a page checks it and a change that breaks a page raises it.
const homeVersion = 1

// homeCacheFor is how long one home.json stays the answer.
const homeCacheFor = 2 * time.Second

// homeMaxCustom bounds homeData.CustomGames.
const homeMaxCustom = 20

// homeData is home.json. Every time is in whole seconds relative to ServerTime, so the page counts down without its
// own clock agreeing with the server's.
type homeData struct {
	Version     int          `json:"version"`
	ServerTime  int64        `json:"serverTime"` // Unix seconds when it was made
	Online      homeOnline   `json:"online"`
	Queue       *homeQueue   `json:"queue"` // null: this server has no matchmaking
	Matches     []homeMatch  `json:"matches"`
	CustomGames []homeCustom `json:"customGames"` // open lobbies, not started
	Results     []homeResult `json:"results"`     // newest first
	Server      homeServer   `json:"server"`
}

type homeOnline struct {
	Players   int `json:"players"`   // logged in (the website's number)
	InMatches int `json:"inMatches"` // in a running match (PlayersInMatches)
	Matches   int `json:"matches"`   // len(Matches)
}

// homeQueue is the PvP queue (the PLAY menu's matchmaking).
type homeQueue struct {
	Players    int `json:"players"`  // humans queued
	Capacity   int `json:"capacity"` // seats of one match (2 × TeamSize)
	TeamSize   int `json:"teamSize"`
	MinPlayers int `json:"minPlayers"` // fewest humans a match starts with after the fill wait
	// StartsInSec is when the next match starts by the rules (0: now), null while no start is known (empty queue, or
	// fewer than MinPlayers).
	StartsInSec  *int `json:"startsInSec"`
	BotsFill     bool `json:"botsFill"`     // bots take the open seats of a match that starts short
	FillAfterSec int  `json:"fillAfterSec"` // how long the oldest ticket waits for full teams
	// FillExtendSec is the least time a match that starts short still waits after its last join (0: none)
	FillExtendSec int `json:"fillExtendSec"`
}

// homeMatch is a running match (a custom game once started; not a lobby).
type homeMatch struct {
	Mode       string      `json:"mode"` // "pvp", "custom", "practice"
	Kind       string      `json:"kind"` // "PvP match", "Custom game", "Practice group", "Practice match"
	Map        string      `json:"map"`  // display name
	State      string      `json:"state"`
	ElapsedSec int         `json:"elapsedSec"` // in game so far (0 while starting)
	Minutes    int         `json:"minutes"`    // ElapsedSec / 60
	Teams      [2]homeSide `json:"teams"`
}

// homeMatch.State values.
const (
	homeStarting = "starting" // loading, shaper select
	homePlaying  = "playing"
	homeEnding   = "ending" // post-game
)

type homeSide struct {
	Humans int `json:"humans"`
	Bots   int `json:"bots"`
}

// homeCustom is an open custom lobby.
type homeCustom struct {
	Title      string `json:"title"` // as its creator typed it, without the server's prefixes
	Map        string `json:"map"`
	Players    int    `json:"players"`
	Seats      int    `json:"seats"`
	Spectators int    `json:"spectators"`
	Bots       bool   `json:"bots"`    // bots fill the open seats at the start
	Private    bool   `json:"private"` // needs the password
}

// homeResult is a finished match.
type homeResult struct {
	Mode        string            `json:"mode"`
	Kind        string            `json:"kind"`
	Map         string            `json:"map"`
	Winner      int               `json:"winner"` // 1 or 2 (the team), 0 no winner
	Reason      string            `json:"reason"` // "guardian", "surrender", ...
	DurationSec int               `json:"durationSec"`
	EndedAgoSec int               `json:"endedAgoSec"`
	Teams       [2]homeResultSide `json:"teams"`
}

type homeResultSide struct {
	Shapers []string `json:"shapers"` // the humans' shapers by name ("" = unknown)
	Bots    int      `json:"bots"`
}

type homeServer struct {
	Release    string   `json:"release"`
	Map        string   `json:"map"` // the map of matchmade and practice games
	Halloween  bool     `json:"halloween"`
	CustomMaps []string `json:"customMaps"` // the maps a custom game may pick besides The Dawngate
}

// homeSource is what home.json is made of (homeSourceOf: the Blaze server's; the tests' fakes).
type homeSource struct {
	now               func() time.Time
	online, inMatches func() int
	// queue is the PvP queue and the matchmaker's rules; ok false without a matchmaker
	queue    func() (st matchmaker.QueueState, cfg matchmaker.Config, ok bool)
	botsFill func() bool // the matchmaker's bots take open seats (match hosts and --mm-bots)
	matches  func() []blaze.HostedMatch
	customs  func() []blaze.CustomLobby
	results  func() []blaze.RecentResult
	// the server's settings
	release, level string
	customLevels   []string
	halloween      bool
}

// homeSourceOf reads bz. Its settings (Level, CustomLevels, Halloween, Release) are taken now: they don't change
// while the server runs.
func homeSourceOf(bz *blaze.Server) homeSource {
	return homeSource{now: bz.Now, online: bz.OnlineUsers, inMatches: bz.PlayersInMatches,
		queue: func() (matchmaker.QueueState, matchmaker.Config, bool) {
			if bz.MM == nil {
				return matchmaker.QueueState{}, matchmaker.Config{}, false
			}
			return bz.MM.Snapshot(gmshost.ModePvP), bz.MM.Config(), true
		},
		botsFill: func() bool { return bz.Hosts != nil && bz.MMBots },
		matches:  bz.HostedMatches, customs: bz.CustomLobbies, results: bz.RecentResults,
		release: bz.Release, level: bz.Level, customLevels: bz.CustomLevels, halloween: bz.Halloween}
}

// homeStatus makes home.json's content from src as it is now.
func homeStatus(src homeSource) func() homeData {
	return func() homeData {
		now := src.now()
		d := homeData{Version: homeVersion, ServerTime: now.Unix(), Matches: []homeMatch{}, CustomGames: []homeCustom{},
			Results: []homeResult{}}
		d.Online.Players, d.Online.InMatches = src.online(), src.inMatches()
		if st, cfg, ok := src.queue(); ok {
			q := &homeQueue{Players: st.Players, Capacity: st.Capacity, TeamSize: cfg.TeamSize, MinPlayers: cfg.MinPlayers,
				BotsFill: src.botsFill(), FillAfterSec: secs(cfg.FillAfter), FillExtendSec: secs(cfg.FillExtend)}
			if !st.StartsAt.IsZero() {
				in := max(secsCeil(st.StartsAt.Sub(now)), 0)
				q.StartsInSec = &in
			}
			d.Queue = q
		}
		for _, m := range src.matches() {
			if m.Lobby {
				continue
			}
			hm := homeMatch{Mode: m.Mode, Kind: m.Kind, Map: blaze.LevelDisplay(m.Level), State: homeStarting}
			switch m.State {
			case "in game":
				hm.State = homePlaying
			case "post-game":
				hm.State = homeEnding
			}
			if !m.InGameAt.IsZero() {
				hm.ElapsedSec = max(secs(now.Sub(m.InGameAt)), 0)
				hm.Minutes = hm.ElapsedSec / 60
			}
			for _, p := range m.Players {
				if !p.Left && p.Team >= 0 && p.Team <= 1 {
					hm.Teams[p.Team].Humans++
				}
			}
			for t := range hm.Teams {
				hm.Teams[t].Bots = m.Bots[t]
			}
			d.Matches = append(d.Matches, hm)
		}
		d.Online.Matches = len(d.Matches)
		for _, c := range src.customs() {
			if c.Started || len(d.CustomGames) >= homeMaxCustom {
				continue
			}
			d.CustomGames = append(d.CustomGames, homeCustom{Title: c.Title, Map: blaze.LevelDisplay(c.Level),
				Players: c.Players, Seats: c.Seats, Spectators: c.Spectators, Bots: c.Bots, Private: c.Private})
		}
		for _, r := range src.results() {
			hr := homeResult{Mode: r.Mode, Kind: r.Kind, Map: blaze.LevelDisplay(r.Level), Reason: r.Reason,
				DurationSec: secs(r.Duration), EndedAgoSec: max(secs(now.Sub(r.At)), 0)}
			if r.WinnerTeam == 1 || r.WinnerTeam == 2 {
				hr.Winner = r.WinnerTeam
			}
			for t := range hr.Teams {
				hr.Teams[t] = homeResultSide{Shapers: append([]string{}, r.Teams[t].Shapers...), Bots: r.Teams[t].Bots}
			}
			d.Results = append(d.Results, hr)
		}
		level := src.level
		if level == "" {
			level = blaze.HostedLevel
		}
		d.Server = homeServer{Release: src.release, Map: blaze.LevelDisplay(level), Halloween: src.halloween,
			CustomMaps: []string{}}
		for _, l := range blaze.OfferedLevels(src.level, src.customLevels) {
			d.Server.CustomMaps = append(d.Server.CustomMaps, blaze.LevelDisplay(l.Name))
		}
		return d
	}
}

func secs(d time.Duration) int { return int(d / time.Second) }

// secsCeil rounds up, so a countdown never shows 0 before the start.
func secsCeil(d time.Duration) int { return int((d + time.Second - 1) / time.Second) }

// homeCache makes home.json at most once per homeCacheFor; safe for concurrent use.
type homeCache struct {
	status func() homeData
	now    func() time.Time

	mu   sync.Mutex
	at   time.Time
	body []byte
}

func newHomeCache(status func() homeData, now func() time.Time) *homeCache {
	return &homeCache{status: status, now: now}
}

// JSON is the current home.json (web.HTTP.Home).
func (c *homeCache) JSON() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.body != nil && now.Sub(c.at) < homeCacheFor && !now.Before(c.at) {
		return c.body
	}
	b, err := json.Marshal(c.status())
	if err != nil { // only plain types: can't happen
		b = []byte(`{"version":1}`)
	}
	c.body, c.at = b, now
	return b
}

// testHomeFile is the test aid --test-home-json: home.json answers with the file's bytes, read anew at every request
// (so a test can change the page's state while it runs), and with the live data while the file can't be read.
func testHomeFile(path string, live func() []byte) func() []byte {
	return func() []byte {
		if b, err := os.ReadFile(path); err == nil {
			return b
		}
		return live()
	}
}
