package blaze

import (
	"fmt"
	"strings"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/discordhook"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/progress"
)

// ResultPosts is where a finished match's result goes on Discord (dgserver --discord-webhook-result, else
// --discord-webhook; internal/discordhook): one embed per match whose mode Settings.PostsResults lists. A nil Hook
// posts nothing. Set once at start with SetResultPosts.
type ResultPosts struct {
	Hook     *discordhook.Hook
	Settings discordhook.Settings // Names (off: "a player"), ResultModes
}

// SetResultPosts sets where match results are posted (safe while the server is serving).
func (s *Server) SetResultPosts(r ResultPosts) { s.results.Store(&r) }

// matchResult is one finished match as the result post shows it: taken from the game's roster and the host's
// playerFinish lines (hostedGame.finishes), so it needs no account store.
type matchResult struct {
	GameID     int64
	Kind       string // resultKind's: "PvP match", "Custom game", "Practice group", "Practice match"
	Mode       string // gmshost.ModePvP, ModeCustom, ModePractice
	Level      string // the level it was played on (gameLevel)
	WinnerTeam int    // 1 or 2; anything else = no winner
	Reason     string // finishGame's: "guardian", "surrender", ...
	Duration   time.Duration
	At         time.Time
	Players    []resultPlayer
	Bots       [2]int // per team (the host's fill rule: botSeats)
}

// resultPlayer is one human of a finished match.
type resultPlayer struct {
	Name    string
	Team    int  // 1 or 2 (Blaze TIDX + 1)
	Result  bool // the host sent its playerFinish
	Noun    uint32
	K, D, A int64
}

// Result kinds; resultMode maps them to the discord_result_modes names.
const (
	kindPvP           = "PvP match"
	kindCustom        = "Custom game"
	kindPracticeGroup = "Practice group"
	kindPractice      = "Practice match"
)

// resultKind names a game's kind for the post and gives the discord_result_modes name that switches its posts. A
// practice group's game (a custom game with BotsFillTeams in its GameFlags: the group against bots) counts as
// practice there [the server's rule: it is a practice match played together, and the Leaderboard doesn't count it
// either; hostedGame.ranked].
func resultKind(mode string, descriptors uint32) (kind, postMode string) {
	switch mode {
	case gmshost.ModePvP:
		return kindPvP, discordhook.ModePvP
	case gmshost.ModeCustom:
		if descriptors&GameFlagBotsFillTeams != 0 {
			return kindPracticeGroup, discordhook.ModePractice
		}
		return kindCustom, discordhook.ModeCustom
	}
	return kindPractice, discordhook.ModePractice
}

// botSeats is how many bots the match host put on each team: the host's rule (gms/core/bots/teamfill.h BotTeamSize
// with [host] bots at its default, auto: createGame's fillTeams, else five seats a team when the GameFlags have
// BotsFillTeams, else none) minus the team's humans, at most ten players in all. The host reports no result for a
// bot (its playerFinish lines for bots stay in its log), so the post can only count them [assumed: a host whose
// [host] bots is set, or a party that got a bot too many, differs].
func botSeats(fillTeams int, descriptors uint32, humans [2]int) [2]int {
	size := fillTeams
	if size <= 0 {
		size = 0
		if descriptors&GameFlagBotsFillTeams != 0 {
			size = 5
		}
	}
	var bots [2]int
	free := 10 - humans[0] - humans[1]
	for t := range bots {
		n := min(max(size-humans[t], 0), max(free, 0))
		bots[t] = n
		free -= n
	}
	return bots
}

// postResult keeps a finished game's result for the home page (Finish, after persist and storeMatch; HOME1, recent.go)
// with its match history id (0: not stored) and posts it when a result webhook is set and the game's mode posts. Never
// waits: Hook.Post only queues.
func (hg *hostedGame) postResult(f gmshost.FinishGame, matchID int64) {
	hg.mu.Lock()
	r := hg.resultLocked(f, hg.s.Now())
	hg.mu.Unlock()
	x := recentResultOf(r, hg.s.Content)
	x.MatchID = matchID
	hg.s.recent.add(x)
	rp := hg.s.results.Load()
	if rp == nil || rp.Hook == nil {
		return
	}
	_, postMode := resultKind(hg.mode, hg.game.descriptors())
	if !rp.Settings.PostsResults(postMode) {
		hg.s.Log.Logf("blaze", "   game %d: result not posted to Discord (%s results are off)", hg.game.ID, postMode)
		return
	}
	rp.Hook.Post(resultMessage(r, rp.Settings.Names, hg.s.Content))
	hg.s.Log.Logf("blaze", "   game %d: result posted to Discord (%d players, %d+%d bots)", hg.game.ID, len(r.Players),
		r.Bots[0], r.Bots[1])
}

// resultLocked takes the result from the roster and the host's playerFinish lines (hg.mu held). The players are
// the roster's humans on teams 1 and 2 in roster order; in a custom game only those who joined the match (a lobby
// member who never reached the GMS didn't play), in a matchmade or practice game every roster player (the host
// keeps their seats). A custom game's spectators are left out.
func (hg *hostedGame) resultLocked(f gmshost.FinishGame, at time.Time) matchResult {
	kind, _ := resultKind(hg.mode, hg.game.descriptors())
	r := matchResult{GameID: hg.game.ID, Kind: kind, Mode: hg.mode, Level: hg.s.gameLevel(hg.game),
		WinnerTeam: f.WinnerTeam, Reason: f.Reason, Duration: time.Duration(f.DurationMs) * time.Millisecond, At: at}
	var humans [2]int
	for _, hp := range hg.players {
		if hp.p.Team < 0 || hp.p.Team > 1 || (hg.custom != nil && !hp.joined) {
			continue
		}
		p := resultPlayer{Name: hp.p.Name, Team: int(hp.p.Team) + 1}
		if pf, ok := hg.finishes[hp.p.ID]; ok {
			p.Result = true
			p.Noun = uint32(pf.Stats[progress.StatNoun])
			p.K, p.D, p.A = pf.Stats[progress.StatKills], pf.Stats[progress.StatDeaths], pf.Stats[progress.StatAssists]
		}
		humans[hp.p.Team]++
		r.Players = append(r.Players, p)
	}
	r.Bots = botSeats(hg.fillTeams, hg.game.descriptors(), humans)
	return r
}

// Embed colours: the winning team's (team 1 blue, team 2 red), grey without a winner.
const (
	colorTeam1    = 0x3498DB
	colorTeam2    = 0xE74C3C
	colorNoWinner = 0x95A5A6
)

// resultMessage is the result post: one embed, title "<kind>: Team N wins" (or "no winner"), the description how it
// ended and how long it took, a field per team with each human ("name" or "a player" with names off, then the
// shaper and K/D/A) and the bots counted, the footer the game id, the timestamp the end. Every name goes through
// Escape; the sender's allowed_mentions pings nobody.
func resultMessage(r matchResult, names bool, c *content.Content) discordhook.Message {
	e := discordhook.Embed{Color: colorNoWinner, Footer: &discordhook.Footer{Text: fmt.Sprintf("Game %d", r.GameID)}}
	won := r.WinnerTeam == 1 || r.WinnerTeam == 2
	how := "no winner"
	if won {
		e.Title = fmt.Sprintf("%s: Team %d wins", r.Kind, r.WinnerTeam)
		e.Color = colorTeam1
		if r.WinnerTeam == 2 {
			e.Color = colorTeam2
		}
		switch r.Reason {
		case "guardian":
			how = fmt.Sprintf("Team %d's Guardian fell", 3-r.WinnerTeam)
		case "surrender":
			how = fmt.Sprintf("Team %d surrendered", 3-r.WinnerTeam)
		default:
			how = "the match ended"
		}
	} else {
		e.Title = r.Kind + ": no winner"
	}
	e.Description = fmt.Sprintf("%s after %s", capitalize(how), clock(r.Duration))
	for team := 1; team <= 2; team++ {
		var lines []string
		for _, p := range r.Players {
			if p.Team == team {
				lines = append(lines, playerLine(p, names, c))
			}
		}
		if n := r.Bots[team-1]; n > 0 {
			lines = append(lines, "+ "+plural(n, "bot"))
		}
		if len(lines) == 0 {
			lines = append(lines, "nobody")
		}
		name := fmt.Sprintf("Team %d", team)
		if won && team == r.WinnerTeam {
			name += " (won)"
		}
		e.Fields = append(e.Fields, discordhook.Field{Name: name, Value: strings.Join(lines, "\n"), Inline: true})
	}
	if !r.At.IsZero() {
		e.At(r.At)
	}
	return discordhook.Message{Embeds: []discordhook.Embed{e}}
}

// playerLine is one human's line: "**name** · shaper · 5/1/7" (no result from the host: "no result").
func playerLine(p resultPlayer, names bool, c *content.Content) string {
	who := "a player"
	if names {
		who = "**" + discordhook.Escape(p.Name) + "**"
	}
	if !p.Result {
		return who + " · no result"
	}
	return fmt.Sprintf("%s · %s · %d/%d/%d", who, shaperName(p.Noun, c), p.K, p.D, p.A)
}

// shaperName is the shaper's name from the content (playerFinish.stats noun → ShaperOf → the shaper item's Title,
// the name the game shows, "Fenmore"; else its asset name, "alpha"), "?" when the host reported none or the content
// doesn't know it.
func shaperName(noun uint32, c *content.Content) string {
	name := shaperTitle(noun, c)
	if name == "" {
		return "?"
	}
	return discordhook.Escape(name)
}

// ShaperTitle is the shaper a noun names by its content title ("Ember"; the match history's, MHIST2), "" when the
// content doesn't know it.
func ShaperTitle(noun uint32, c *content.Content) string { return shaperTitle(noun, c) }

// shaperTitle is shaperName's name unescaped, "" when the host reported none or the content doesn't know it.
func shaperTitle(noun uint32, c *content.Content) string {
	id, ok := c.ShaperOf(noun)
	if !ok {
		return ""
	}
	it, ok := c.Item(id)
	if !ok {
		return ""
	}
	if name := strings.TrimSpace(it.Title); name != "" {
		return name
	}
	return strings.TrimSpace(it.Name)
}

// clock is a match length as m:ss (h:mm:ss from an hour on).
func clock(d time.Duration) string {
	s := int64(d / time.Second)
	if s < 0 {
		s = 0
	}
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
