package blaze

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

func src(kind string, slot int, id, damage int64) gmshost.StatSource {
	s := gmshost.StatSource{Kind: kind, ID: id, Damage: damage}
	if slot >= 0 {
		s.Slot = &slot
	}
	return s
}

// The history's record of a finished match: with the host's seatStats and timeline (a human at the index its seatStats
// gives, its playerFinish and seatStats stats merged, the bots' seats, a seatStats of nobody in the roster left out),
// and from a host without them (the roster and playerFinish only: the humans at their addPlayer index or roster
// position, a custom game's spectator and a member who never joined left out, "won" from playerFinish over the team).
func TestMatchRecord(t *testing.T) {
	end := resultAt
	noun := int64(274088913)
	t.Run("seatStats", func(t *testing.T) {
		hg := resultGame(gmshost.ModePvP, "1", 5, false, []resultSeat{{name: "alpha", team: 0,
			stats: map[string]int64{"kills": 3, "deaths": 1, "assists": 2, "creeps": 40, "gold": 1500, "level": 6, "noun": noun,
				"role": 2, "won": 1, "damageDealt": 1}}})
		hg.s, hg.inGameAt = &Server{}, end.Add(-10*time.Minute)
		hg.keepSeatStats(gmshost.SeatStats{Index: 0, User: 2000000001, Team: 1, Noun: noun, Role: 2,
			Stats:   map[string]int64{"damageDealt": 18234, "timeDeadMs": 41000},
			Sources: []gmshost.StatSource{src("ability", 1, 0, 5210), src("item", -1, 279208847, 812)}})
		hg.keepSeatStats(gmshost.SeatStats{Index: 6, Bot: true, Team: 2, Noun: 284823512, Role: 1,
			Stats: map[string]int64{"kills": 2, "deaths": 3, "assists": 4, "creeps": 88, "gold": 7420, "level": 11}})
		hg.keepSeatStats(gmshost.SeatStats{Index: 5, Team: 2, Noun: 284823512})  // a bot without stats
		hg.keepSeatStats(gmshost.SeatStats{Index: 4, User: 2000000099, Team: 1}) // nobody in the roster
		hg.keepTimeline(gmshost.MatchTimeline{StepSec: 60, Sec: []int64{60}, Events: []gmshost.TimelineEvent{{Ms: 5, Kind: "kill", A: -1, B: 0}}})
		m, skipped := hg.matchLocked(gmshost.FinishGame{WinnerTeam: 2, DurationMs: 600000, Reason: "guardian"}, end)
		want := store.Match{GameID: 7, Kind: "pvp", Mode: "pvp", Level: "SM_Map_v5", Winner: 2, Reason: "guardian",
			Started: end.Add(-10 * time.Minute), Ended: end, DurationMs: 600000,
			Timeline: json.RawMessage(`{"stepSec":60,"sec":[60],"events":[{"ms":5,"kind":"kill","a":-1,"b":0}]}`),
			Seats: []store.MatchSeat{
				{Seat: 0, PersonaID: 2000000001, Team: 1, Noun: uint32(noun), Role: 2, Won: true, Kills: 3, Deaths: 1, Assists: 2,
					CS: 40, Gold: 1500, Level: 6, Stats: map[string]int64{"kills": 3, "deaths": 1, "assists": 2, "creeps": 40,
						"gold": 1500, "level": 6, "noun": noun, "role": 2, "won": 1, "damageDealt": 18234, "timeDeadMs": 41000},
					Sources: json.RawMessage(`[{"kind":"ability","slot":1,"id":0,"damage":5210,"heal":0},` +
						`{"kind":"item","id":279208847,"damage":812,"heal":0}]`)},
				{Seat: 5, Team: 2, Bot: true, Noun: 284823512, Won: true},
				{Seat: 6, Team: 2, Bot: true, Noun: 284823512, Role: 1, Won: true, Kills: 2, Deaths: 3, Assists: 4, CS: 88, Gold: 7420,
					Level: 11, Stats: map[string]int64{"kills": 2, "deaths": 3, "assists": 4, "creeps": 88, "gold": 7420, "level": 11}},
			}}
		if !reflect.DeepEqual(m, want) || skipped != 1 {
			t.Errorf("record (skipped %d)\n got %+v\nwant %+v", skipped, m, want)
		}
	})
	t.Run("basics", func(t *testing.T) {
		hg := resultGame(gmshost.ModeCustom, "2", 0, true, []resultSeat{
			{name: "alpha", team: 0, stats: map[string]int64{"kills": 1, "won": 1}},
			{name: "bravo", team: 1},
			{name: "spectator", team: SpectatorTeam},
			{name: "away", team: 1, away: true},
		})
		hg.s = &Server{}
		hg.started = end.Add(-time.Hour)
		hg.players[1].index, hg.players[1].indexed = 5, true
		hg.players[1].removed = true
		m, skipped := hg.matchLocked(gmshost.FinishGame{WinnerTeam: 2, DurationMs: 1000, Reason: "surrender"}, end)
		want := store.Match{GameID: 7, Kind: "custom", Mode: "custom", Level: "SM_Map_v5", Winner: 2, Reason: "surrender",
			Started: end.Add(-time.Hour), Ended: end, DurationMs: 1000,
			Seats: []store.MatchSeat{
				{Seat: 0, PersonaID: 2000000001, Team: 1, Won: true, Kills: 1, Stats: map[string]int64{"kills": 1, "won": 1}},
				{Seat: 5, PersonaID: 2000000002, Team: 2, Won: true, Left: true},
			}}
		if !reflect.DeepEqual(m, want) || skipped != 0 {
			t.Errorf("record (skipped %d)\n got %+v\nwant %+v", skipped, m, want)
		}
	})
	for _, c := range []struct {
		mode, flags, want string
	}{
		{gmshost.ModePvP, "1", "pvp"}, {gmshost.ModeCustom, "2", "custom"}, {gmshost.ModeCustom, "1026", "practiceGroup"},
		{gmshost.ModePractice, "0", "practice"},
	} {
		g := Game{Attrs: []tdf.Pair{tdf.KV("GameFlags", c.flags)}}
		if got := matchKind(c.mode, g.descriptors()); got != c.want {
			t.Errorf("matchKind(%s, %s) = %s, want %s", c.mode, c.flags, got, c.want)
		}
	}
}

// A lone player's matchmade game against bots, through Blaze and a fake match host: with the host's seatStats and
// timeline the history holds the player's seat (its sources, its stats merged) and the bots'; from a host without them
// the player's seat alone; an aborted match stores nothing.
func TestHostedGameStoresMatch(t *testing.T) {
	for _, c := range []struct {
		name          string
		stats, finish bool
		seats         int // seats stored; -1 = no match
	}{
		{"with seatStats", true, true, 3},
		{"basics", false, true, 1},
		{"aborted", true, false, -1},
	} {
		var hosts *gmshost.Manager
		ts := newTCPServer(t, func(s *Server) {
			s.MM = matchmaker.New(matchmaker.Config{TeamSize: 5, MinPlayers: 1})
			s.MMBots = true
			s.Content = loadFixtureContent(t)
			l := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
				gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
					u := cg.Roster[0].User
					h.Send(gmshost.AddPlayer{User: u, Index: 0})
					if c.stats {
						h.Send(gmshost.SeatStats{Index: 0, User: u, Team: 1, Stats: map[string]int64{"damageDealt": 900},
							Sources: []gmshost.StatSource{src("basic", -1, 0, 900)}})
						h.Send(gmshost.SeatStats{Index: 5, Bot: true, Team: 2, Noun: 284823512,
							Stats: map[string]int64{"kills": 1}})
						h.Send(gmshost.SeatStats{Index: 1, Bot: true, Team: 1})
						h.Send(gmshost.MatchTimeline{StepSec: 60, Sec: []int64{60}})
					}
					if !c.finish {
						return nil
					}
					h.Send(gmshost.PlayerFinish{User: u, Stats: kda("ember.Noun", 3, 1, 2)})
					return h.Send(gmshost.FinishGame{WinnerTeam: 1, DurationMs: 754000, Reason: "guardian"})
				})
			}}
			hosts = gmshost.NewManager(gmshost.Config{Log: s.Log, Launcher: l, Pool: gmshost.NewPool(GMSPort+7, GMSPort+7),
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
		ms, err := ts.s.Store.RecentMatches(0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if c.seats < 0 {
			if len(ms) != 0 {
				t.Errorf("%s: stored %+v", c.name, ms)
			}
			continue
		}
		if len(ms) != 1 || len(ms[0].Seats) != c.seats {
			t.Fatalf("%s: stored %+v", c.name, ms)
		}
		m, _, _ := ts.s.Store.Match(ms[0].ID)
		// the home page's ring carries the stored id (home.json's matchId)
		if rr := ts.s.RecentResults(); len(rr) != 1 || rr[0].MatchID != ms[0].ID || ms[0].ID == 0 {
			t.Errorf("%s: the ring %+v, stored id %d", c.name, rr, ms[0].ID)
		}
		p := m.Seats[0]
		if m.GameID != 1 || m.Kind != "pvp" || m.Winner != 1 || m.DurationMs != 754000 || p.Seat != 0 || p.Name != "alpha" ||
			p.PersonaID != a.id || p.Bot || !p.Won || p.Kills != 3 || p.Deaths != 1 || p.Assists != 2 || p.Noun == 0 {
			t.Errorf("%s: match %+v", c.name, m)
		}
		log := ts.log.String()
		if c.stats {
			if string(p.Sources) != `[{"kind":"basic","id":0,"damage":900,"heal":0}]` || p.Stats["damageDealt"] != 900 ||
				string(m.Timeline) != `{"stepSec":60,"sec":[60]}` || m.Seats[1].Seat != 1 || !m.Seats[1].Won ||
				m.Seats[2].Seat != 5 || m.Seats[2].Kills != 1 || m.Seats[2].Won || m.Seats[2].PersonaID != 0 {
				t.Errorf("%s: match %+v", c.name, m)
			}
			if !strings.Contains(log, "game 1: stored as match 1 (1 players, 2 bots; seatStats from the host 3, timeline true)") {
				t.Errorf("%s: log:\n%s", c.name, log)
			}
		} else if p.Sources != nil || m.Timeline != nil ||
			!strings.Contains(log, "game 1: stored as match 1 (1 players, 0 bots; seatStats from the host 0, timeline false)") {
			t.Errorf("%s: match %+v, log:\n%s", c.name, m, log)
		}
	}
}
