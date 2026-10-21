package blaze

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/discordhook"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/tdf"
)

var resultAt = time.Date(2026, 10, 21, 18, 30, 0, 0, time.UTC)

// resultGame is a finished hosted game for the result tests: players (name, TIDX, joined) and their playerFinish
// stats (nil = the host sent none).
func resultGame(mode string, flags string, fill int, custom bool, players []resultSeat) *hostedGame {
	hg := &hostedGame{game: Game{ID: 7, Attrs: []tdf.Pair{tdf.KV("GameFlags", flags)}}, mode: mode, fillTeams: fill,
		finishes: map[int64]gmshost.PlayerFinish{}}
	if custom {
		hg.custom = &customLobby{}
	}
	for i, p := range players {
		id := int64(2000000001 + i)
		hg.players = append(hg.players, &hostedPlayer{p: Player{ID: id, Name: p.name, Team: p.team}, joined: !p.away})
		if p.stats != nil {
			hg.finishes[id] = gmshost.PlayerFinish{User: id, Stats: p.stats}
		}
	}
	return hg
}

type resultSeat struct {
	name  string
	team  int64
	away  bool // never joined the GMS
	stats map[string]int64
}

func kda(noun string, k, d, a int64) map[string]int64 {
	s := map[string]int64{progress.StatKills: k, progress.StatDeaths: d, progress.StatAssists: a}
	if noun != "" {
		s[progress.StatNoun] = int64(content.AssetID(noun))
	}
	return s
}

// The result embeds as the sender sends them (discordhook.Encode: allowed_mentions parse [] on every post): a 5v5 PvP
// win, a custom game (a surrender, a player without a result, a spectator and a lobby member who never joined left
// out, over an hour), a lone player's match against bots, the same with names off, a name with markdown and
// @everyone (escaped: no formatting, no mention), and a match without a winner.
func TestResultMessageGolden(t *testing.T) {
	c := loadFixtureContent(t)
	five := []resultSeat{
		{"Ann", 0, false, kda("ember.Noun", 5, 1, 7)}, {"Bob", 0, false, kda("stone.Noun", 2, 3, 9)},
		{"Cid", 0, false, kda("ghost.Noun", 4, 2, 3)}, {"Dee", 0, false, kda("ember_frost.Noun", 0, 4, 11)},
		{"Eve", 0, false, kda("ghost_mist.Noun", 8, 0, 4)},
		{"Fay", 1, false, kda("stone.Noun", 1, 5, 2)}, {"Gus", 1, false, kda("ember.Noun", 3, 4, 1)},
		{"Hal", 1, false, kda("ghost.Noun", 2, 6, 3)}, {"Ivy", 1, false, kda("", 0, 3, 4)},
		{"Jon", 1, false, kda("stone.Noun", 1, 2, 0)},
	}
	for _, tc := range []struct {
		name  string
		hg    *hostedGame
		f     gmshost.FinishGame
		names bool
		want  string
	}{
		{"pvp 5v5", resultGame(gmshost.ModePvP, "1", 0, false, five),
			gmshost.FinishGame{WinnerTeam: 1, DurationMs: 1865400, Reason: "guardian"}, true,
			`{"content":"","embeds":[{"title":"PvP match: Team 1 wins","description":"Team 2's Guardian fell after 31:05",` +
				`"color":3447003,"fields":[{"name":"Team 1 (won)","value":"**Ann** · Ember · 5/1/7\n**Bob** · stone · 2/3/9\n` +
				`**Cid** · ghost · 4/2/3\n**Dee** · Ember · 0/4/11\n**Eve** · ghost · 8/0/4","inline":true},` +
				`{"name":"Team 2","value":"**Fay** · stone · 1/5/2\n**Gus** · Ember · 3/4/1\n**Hal** · ghost · 2/6/3\n` +
				`**Ivy** · ? · 0/3/4\n**Jon** · stone · 1/2/0","inline":true}],"footer":{"text":"Game 7"},` +
				`"timestamp":"2026-10-21T18:30:00Z"}],"allowed_mentions":{"parse":[]}}`},
		{"custom", resultGame(gmshost.ModeCustom, "0", 0, true, []resultSeat{
			{"Ann", 0, false, kda("ember.Noun", 1, 9, 2)}, {"Bob", 0, false, nil},
			{"Kim", 1, false, kda("stone.Noun", 9, 1, 0)}, {"Lou", 1, true, nil}, {"Spec", SpectatorTeam, false, nil}}),
			gmshost.FinishGame{WinnerTeam: 2, DurationMs: 3723000, Reason: "surrender"}, true,
			`{"content":"","embeds":[{"title":"Custom game: Team 2 wins","description":"Team 1 surrendered after 1:02:03",` +
				`"color":15158332,"fields":[{"name":"Team 1","value":"**Ann** · Ember · 1/9/2\n**Bob** · no result","inline":true},` +
				`{"name":"Team 2 (won)","value":"**Kim** · stone · 9/1/0","inline":true}],"footer":{"text":"Game 7"},` +
				`"timestamp":"2026-10-21T18:30:00Z"}],"allowed_mentions":{"parse":[]}}`},
		{"bots", resultGame(gmshost.ModePvP, "1", 5, false, []resultSeat{{"Ann", 0, false, kda("ember.Noun", 3, 1, 2)}}),
			gmshost.FinishGame{WinnerTeam: 1, DurationMs: 754000, Reason: "guardian"}, true,
			`{"content":"","embeds":[{"title":"PvP match: Team 1 wins","description":"Team 2's Guardian fell after 12:34",` +
				`"color":3447003,"fields":[{"name":"Team 1 (won)","value":"**Ann** · Ember · 3/1/2\n+ 4 bots","inline":true},` +
				`{"name":"Team 2","value":"+ 5 bots","inline":true}],"footer":{"text":"Game 7"},` +
				`"timestamp":"2026-10-21T18:30:00Z"}],"allowed_mentions":{"parse":[]}}`},
		{"names off", resultGame(gmshost.ModePvP, "1", 2, false, []resultSeat{{"Ann", 0, false, kda("ember.Noun", 3, 1, 2)},
			{"Bob", 1, false, kda("stone.Noun", 0, 2, 1)}}),
			gmshost.FinishGame{WinnerTeam: 2, DurationMs: 59000, Reason: "guardian"}, false,
			`{"content":"","embeds":[{"title":"PvP match: Team 2 wins","description":"Team 1's Guardian fell after 0:59",` +
				`"color":15158332,"fields":[{"name":"Team 1","value":"a player · Ember · 3/1/2\n+ 1 bot","inline":true},` +
				`{"name":"Team 2 (won)","value":"a player · stone · 0/2/1\n+ 1 bot","inline":true}],"footer":{"text":"Game 7"},` +
				`"timestamp":"2026-10-21T18:30:00Z"}],"allowed_mentions":{"parse":[]}}`},
		{"markdown name", resultGame(gmshost.ModeCustom, "0", 0, true, []resultSeat{
			{"@everyone **x**", 0, false, kda("ember.Noun", 1, 0, 0)}, {"<@&123> [a](b)", 1, false, kda("stone.Noun", 0, 1, 0)}}),
			gmshost.FinishGame{WinnerTeam: 1, DurationMs: 600000, Reason: "guardian"}, true,
			`{"content":"","embeds":[{"title":"Custom game: Team 1 wins","description":"Team 2's Guardian fell after 10:00",` +
				`"color":3447003,"fields":[{"name":"Team 1 (won)","value":"**\\@` + "\u200b" + `everyone \\*\\*x\\*\\*** · Ember · 1/0/0",` +
				`"inline":true},{"name":"Team 2","value":"**\\\u003c\\@` + "\u200b" + `\u0026123\\\u003e \\[a\\]\\(b\\)** · stone · 0/1/0",` +
				`"inline":true}],"footer":{"text":"Game 7"},"timestamp":"2026-10-21T18:30:00Z"}],"allowed_mentions":{"parse":[]}}`},
		{"no winner", resultGame(gmshost.ModePvP, "1", 0, false, []resultSeat{{"Ann", 0, false, kda("ember.Noun", 0, 0, 0)}}),
			gmshost.FinishGame{WinnerTeam: 255, DurationMs: 30000, Reason: "guardian"}, true,
			`{"content":"","embeds":[{"title":"PvP match: no winner","description":"No winner after 0:30","color":9807270,` +
				`"fields":[{"name":"Team 1","value":"**Ann** · Ember · 0/0/0","inline":true},{"name":"Team 2","value":"nobody",` +
				`"inline":true}],"footer":{"text":"Game 7"},"timestamp":"2026-10-21T18:30:00Z"}],"allowed_mentions":{"parse":[]}}`},
	} {
		m := resultMessage(tc.hg.resultLocked(tc.f, resultAt), tc.names, c)
		b, err := discordhook.Encode(m)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, b, tc.want)
		}
		if strings.Contains(string(b), `"@everyone`) || strings.Contains(string(b), `"roles"`) {
			t.Errorf("%s: a mention could ping: %s", tc.name, b)
		}
	}
}

// The bots are counted by the host's fill rule: fillTeams, else five a team with BotsFillTeams, else none; minus
// each team's humans; ten players at most.
func TestResultBotSeats(t *testing.T) {
	for _, c := range []struct {
		fill   int
		flags  uint32
		humans [2]int
		want   [2]int
	}{
		{5, 1, [2]int{1, 0}, [2]int{4, 5}},
		{0, 0x412, [2]int{2, 0}, [2]int{3, 5}},
		{0, 1, [2]int{1, 0}, [2]int{0, 0}},
		{3, 1, [2]int{2, 1}, [2]int{1, 2}},
		{5, 1, [2]int{6, 1}, [2]int{0, 3}},
		{0, 0x50C, [2]int{1, 0}, [2]int{4, 5}},
	} {
		if got := botSeats(c.fill, c.flags, c.humans); got != c.want {
			t.Errorf("botSeats(%d, %#x, %v) = %v, want %v", c.fill, c.flags, c.humans, got, c.want)
		}
	}
}

// Which modes post: discord_result_modes names pvp, custom, practice; a practice group's game (a custom game with
// BotsFillTeams) is practice.
func TestResultKind(t *testing.T) {
	for _, c := range []struct {
		mode       string
		flags      uint32
		kind, post string
	}{
		{gmshost.ModePvP, 1, kindPvP, discordhook.ModePvP},
		{gmshost.ModeCustom, 0, kindCustom, discordhook.ModeCustom},
		{gmshost.ModeCustom, 0x412, kindPracticeGroup, discordhook.ModePractice},
		{gmshost.ModePractice, 0x50C, kindPractice, discordhook.ModePractice},
	} {
		if k, p := resultKind(c.mode, c.flags); k != c.kind || p != c.post {
			t.Errorf("resultKind(%s, %#x) = %q, %q", c.mode, c.flags, k, p)
		}
	}
}

// fakeWebhook is an httptest Discord webhook: it answers posts like Discord (?wait=true: the message with an id)
// and keeps their bodies.
type fakeWebhook struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newFakeWebhook(t *testing.T) *fakeWebhook {
	w := &fakeWebhook{}
	w.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.mu.Lock()
		w.bodies = append(w.bodies, string(b))
		n := len(w.bodies)
		w.mu.Unlock()
		rw.Header().Set("Content-Type", "application/json")
		io.WriteString(rw, `{"id":"`+strconv.Itoa(n)+`"}`)
	}))
	t.Cleanup(w.Close)
	return w
}

func (w *fakeWebhook) got() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.bodies...)
}

// A lone player's matchmade game against bots, through Blaze and a fake match host, posts one result embed to the
// result webhook (the player's shaper and K/D/A, the bots counted); with pvp not among the result modes nothing is
// posted, and an aborted match (the host exits without finishGame) posts nothing either.
func TestHostedGameResultPosted(t *testing.T) {
	for _, c := range []struct {
		name   string
		modes  []string
		finish bool
		want   string // "" = no post
	}{
		{"posted", []string{discordhook.ModePvP}, true,
			`{"content":"","embeds":[{"title":"PvP match: Team 1 wins","description":"Team 2's Guardian fell after 12:34",` +
				`"color":3447003,"fields":[{"name":"Team 1 (won)","value":"**alpha** · Ember · 3/1/2\n+ 4 bots","inline":true},` +
				`{"name":"Team 2","value":"+ 5 bots","inline":true}],"footer":{"text":"Game 1"},`},
		{"mode off", []string{discordhook.ModeCustom}, true, ""},
		{"aborted", []string{discordhook.ModePvP}, false, ""},
	} {
		wh := newFakeWebhook(t)
		hook, err := discordhook.New(wh.URL+"/api/webhooks/1/x", discordhook.Options{AllowLocal: true})
		if err != nil {
			t.Fatal(err)
		}
		var hosts *gmshost.Manager
		ts := newTCPServer(t, func(s *Server) {
			s.MM = matchmaker.New(matchmaker.Config{TeamSize: 5, MinPlayers: 1})
			s.MMBots = true
			s.Content = loadFixtureContent(t)
			st := discordhook.DefaultSettings()
			st.ResultModes = c.modes
			s.SetResultPosts(ResultPosts{Hook: hook, Settings: st})
			l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
				gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
					h.Send(gmshost.AddPlayer{User: cg.Roster[0].User})
					if !c.finish {
						return nil
					}
					h.Send(gmshost.PlayerFinish{User: cg.Roster[0].User, Stats: kda("ember.Noun", 3, 1, 2)})
					return h.Send(gmshost.FinishGame{WinnerTeam: 1, DurationMs: 754000, Reason: "guardian"})
				})
			}}
			hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+6, GMSPort+6),
				ExitTimeout: time.Second})
			if err := hosts.Listen("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			s.Hosts = hosts
		})
		a := ts.client("alpha@example.com")
		id := a.send(ComponentGameManager, CmdGameManagerStartMatchmaking,
			startMatchmakingRequestAttrs(0, tdf.KV("GameFlags", "1"), tdf.KV("GameType", "1")))
		if f := a.reply(ComponentGameManager, CmdGameManagerStartMatchmaking, id); f.Type != fire.Reply {
			t.Fatalf("%s: startMatchmaking %s", c.name, f.Type)
		}
		a.note(ComponentGameManager, NotifyGameSetupID)
		a.note(ComponentGameManager, NotifyPlayerRemovedID)
		hosts.Close()
		hook.Close(5 * time.Second) // sends what is queued
		got := wh.got()
		switch {
		case c.want == "" && len(got) != 0:
			t.Errorf("%s: posted %v", c.name, got)
		case c.want != "" && (len(got) != 1 || !strings.HasPrefix(got[0], c.want) ||
			!strings.HasSuffix(got[0], `"allowed_mentions":{"parse":[]}}`)):
			t.Errorf("%s: posts\n got %v\nwant one starting %s", c.name, got, c.want)
		}
		log := ts.log.String()
		for _, w := range map[string][]string{
			"posted":   {"game 1: result posted to Discord (1 players, 4+5 bots)"},
			"mode off": {"game 1: result not posted to Discord (pvp results are off)"},
		}[c.name] {
			if !strings.Contains(log, w) {
				t.Errorf("%s: log lacks %q:\n%s", c.name, w, log)
			}
		}
	}
}
