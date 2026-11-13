package blaze

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/rating"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// pfStateLine is profile.state's line.
func pfStateLine(mode string) string {
	return `{"event":"profile.state","data":{"mode":"` + mode + `","modes":["practice","standard","draft","custom","all"]}}`
}

// profileGame is one stored match of the PROFILE2 tests: its kind, rules and Unrated, the persona's seat and when it
// ended.
type profileGame struct {
	kind, rules string
	unrated     bool
	noun        string // the shaper's Noun ("ember_frost" counts for ember)
	role        int
	won         bool
	kills       int64
	at          int64 // Unix time of its end
}

// profileGames: two practice games (one a practice group's), PvP and a custom game without rules (standard), a draft
// game, a draft game its admin made Unrated, and a custom game with other rules.
var profileGames = []profileGame{
	{matchKindPractice, "", false, "ember_frost", 1, true, 3, testTime},
	{matchKindPracticeGroup, "", false, "stone", 3, false, 1, b7dNow},
	{matchKindPvP, "", false, "ember_frost", 1, true, 5, b7dNow},
	{matchKindCustom, "", false, "stone", 2, true, 2, b7dNow},
	{matchKindCustom, `{"pick":"draft"}`, false, "ember_frost", 4, false, 4, b7dNow},
	{matchKindCustom, `{"pick":"draft"}`, true, "stone", 4, true, 9, b7dNow},
	{matchKindCustom, `{"pick":"random","shop":"onDeath"}`, false, "stone", 1, true, 7, b7dNow},
}

// insertProfileGames stores profileGames with persona at seat 0 and another persona at seat 5 (whose games must not
// count for persona).
func insertProfileGames(t *testing.T, st *store.Store, persona, user, other int64) {
	t.Helper()
	for i, g := range profileGames {
		mode := map[string]string{matchKindPractice: "practice", matchKindPracticeGroup: "practice", matchKindPvP: "pvp",
			matchKindCustom: "custom"}[g.kind]
		end := time.Unix(g.at, 0)
		won := int64(0)
		if g.won {
			won = 1
		}
		m := store.Match{GameID: int64(i + 1), Kind: g.kind, Mode: mode, Level: "SM_Map_v5", Winner: 1, Reason: "guardian",
			Started: end.Add(-10 * time.Minute), Ended: end, DurationMs: 600000, Rules: g.rules, Unrated: g.unrated,
			Seats: []store.MatchSeat{
				{Seat: 0, PersonaID: persona, UserID: user, Team: 1, Noun: content.AssetID(g.noun + ".Noun"), Role: g.role,
					Won: g.won, Kills: g.kills, Deaths: 1, Assists: 2, CS: 30, Gold: 4000,
					Stats: map[string]int64{"kills": g.kills, "deaths": 1, "assists": 2, "creeps": 30, "gold": 4000,
						"won": won, "durationMs": 600000, "bestStreak": g.kills, "doubleKills": 1}},
				{Seat: 5, PersonaID: other, Team: 2, Noun: content.AssetID("stone.Noun"), Role: 2, Won: !g.won, Kills: 50,
					Stats: map[string]int64{"kills": 50}},
			}}
		if _, err := st.InsertMatch(m); err != nil {
			t.Fatal(err)
		}
	}
}

// The profile's numbers per mode are made from the stored matches of that type (GameType) without the Unrated ones,
// the player's own seat only: practice counts the practice group's game, standard PvP and the custom game without
// rules, draft only the rated draft game, custom the game with other rules. Each is what applyGame would have stored.
func TestProfileOf(t *testing.T) {
	c := loadFixtureContent(t)
	st := store.New()
	insertProfileGames(t, st, 2000000001, 1000000001, 2000000009)
	type want struct {
		played, wins, kills string
		shapers             map[uint32]store.GamesWon
	}
	ember, stone := uint32(0x10000001), uint32(0x10000002)
	for mode, w := range map[string]want{
		ProfilePractice: {"2", "1", "4", map[uint32]store.GamesWon{ember: {Games: 1, Wins: 1}, stone: {Games: 1}}},
		rating.Standard: {"2", "2", "7", map[uint32]store.GamesWon{ember: {Games: 1, Wins: 1}, stone: {Games: 1, Wins: 1}}},
		rating.Draft:    {"1", "0", "4", map[uint32]store.GamesWon{ember: {Games: 1}}},
		rating.Custom:   {"1", "1", "7", map[uint32]store.GamesWon{stone: {Games: 1, Wins: 1}}},
	} {
		ms, err := st.PlayedMatches(2000000001, func(m store.Match) bool { return !m.Unrated && GameType(m) == mode })
		if err != nil {
			t.Fatal(err)
		}
		ps, h := ProfileOf(c, ms)
		if ps["PLAYED"] != w.played || ps["WINS"] != w.wins || ps["KILLS"] != w.kills {
			t.Errorf("%s: PlayerStats %v", mode, ps)
		}
		if !reflect.DeepEqual(h.Shapers, w.shapers) {
			t.Errorf("%s: shapers %v, want %v", mode, h.Shapers, w.shapers)
		}
		// the same as applyGame's functions over the same games
		var wantPS map[string]string
		var wantH store.History
		for _, m := range ms {
			s := m.Seats[0]
			shaper, _ := c.ShaperOf(s.Noun)
			wantPS = progress.ApplyPlayerStats(wantPS, s.Won, s.Stats)
			wantH = progress.ApplyHistory(wantH, m.Ended, shaper, int64(s.Role), s.Won, s.Stats, 600)
		}
		if !reflect.DeepEqual(ps, wantPS) || !reflect.DeepEqual(h, wantH) {
			t.Errorf("%s: not applyGame's numbers\n got %v %+v\nwant %v %+v", mode, ps, h, wantPS, wantH)
		}
	}
	// the Unrated draft game counts nowhere; the other persona's seats never
	all, _ := st.PlayedMatches(2000000001, nil)
	if len(all) != len(profileGames) {
		t.Fatalf("%d matches", len(all))
	}
	ps, _ := ProfileOf(c, all)
	if ps["KILLS"] != "31" {
		t.Errorf("every seat of the persona: KILLS %s", ps["KILLS"])
	}
	// a seat without stats (a host without match stats) counts with its list columns
	ps, _ = ProfileOf(c, []store.Match{{Ended: time.Unix(testTime, 0), Seats: []store.MatchSeat{{Won: true, Kills: 6, Assists: 3}}}})
	if ps["PLAYED"] != "1" || ps["WINS"] != "1" || ps["KILLS"] != "6" || ps["ASSISTS"] != "3" {
		t.Errorf("columns only: %v", ps)
	}
}

// getStatsByGroupAsync answers in the viewing player's mode: "all" from the account's stored profile, a game type from
// the stored matches of that type (also for another player's profile, in the viewer's mode); the log names it.
func TestProfileStatsByMode(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) {
		s.Content = loadFixtureContent(t)
		s.Now = func() time.Time { return time.Unix(b7dNow, 0) }
	})
	ps.login(0, "extendeddataupdate_notification")
	me := int64(store.FirstPersonaID)
	acct, _, err := ps.srv.Store.Login("other@example.com", time.Unix(b7dNow, 0))
	if err != nil {
		t.Fatal(err)
	}
	other := acct.Personas[0].ID
	insertProfileGames(t, ps.srv.Store, me, store.FirstUserID, other)
	stored := map[string]string{"PLAYED": "99", "WINS": "50"}
	if _, err := ps.srv.Store.Update(store.FirstUserID, func(a *store.Account) {
		a.Stats[progress.PlayerStats] = stored
		a.History = b7dHistory(t)
	}); err != nil {
		t.Fatal(err)
	}
	values := func(m map[string]string) []string {
		var out []string
		for _, n := range StatGroups[progress.PlayerStats] {
			v := m[n]
			if v == "" {
				v = "0"
			}
			out = append(out, v)
		}
		return out
	}
	request := func(eid int64, group string) []byte {
		w := tdf.NewWriter().List("EID", tdf.TypeInt, []int64{eid}).String("NAME", group)
		if group == progress.PlayerStats || group == progress.ShaperStats {
			return w.Int("PCTR", 1).Int("PTYP", 0).Int("VID", 1).Bytes()
		}
		return w.Int("PCTR", 7).Int("PTYP", 3).Int("VID", 4).Bytes()
	}
	id := uint32(10)
	ask := func(eid int64, group string) {
		t.Helper()
		id++
		ps.send(7, CmdStatsGetStatsByGroupAsync, id, request(eid, group))
		ps.expect(7, CmdStatsGetStatsByGroupAsync, fire.Reply, id, "")
	}
	setMode := func(mode string) {
		c := &ps.srv.pfChoice
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.mode == nil {
			c.mode = map[int64]string{}
		}
		c.mode[me] = mode
	}

	// All Modes: the stored profile
	ask(me, progress.PlayerStats)
	ps.expectPayload(7, NotifyStatsGetStatsAsync, fire.Notification, 0,
		StatsAsyncNotification(progress.PlayerStats, 1, me, values(stored)))
	ask(me, progress.RoleHistoryStats)
	for _, w := range KeyedStatsNotifications(progress.RoleHistoryStats, 4, me,
		progress.RoleHistoryOf(b7dHistory(t), time.Unix(b7dNow, 0), 0, 7)) {
		ps.expectPayload(7, NotifyStatsGetStatsAsync, fire.Notification, 0, w)
	}

	// each type: its games' numbers
	for _, mode := range []string{ProfilePractice, rating.Standard, rating.Draft, rating.Custom} {
		setMode(mode)
		ms, _ := ps.srv.Store.PlayedMatches(me, func(m store.Match) bool { return !m.Unrated && GameType(m) == mode })
		wantPS, wantH := ProfileOf(ps.srv.Content, ms)
		ask(me, progress.PlayerStats)
		ps.expectPayload(7, NotifyStatsGetStatsAsync, fire.Notification, 0,
			StatsAsyncNotification(progress.PlayerStats, 1, me, values(wantPS)))
		ask(me, progress.ShaperStats)
		for _, w := range KeyedStatsNotifications(progress.ShaperStats, 1, me, progress.ShaperStatsOf(wantH)) {
			ps.expectPayload(7, NotifyStatsGetStatsAsync, fire.Notification, 0, w)
		}
		ask(me, progress.ShaperHistoryStats)
		for _, w := range KeyedStatsNotifications(progress.ShaperHistoryStats, 4, me,
			progress.ShaperHistoryOf(wantH, time.Unix(b7dNow, 0), 0, 7)) {
			ps.expectPayload(7, NotifyStatsGetStatsAsync, fire.Notification, 0, w)
		}
	}

	// another player's profile in the viewer's mode (custom): the other persona's custom game, its seat only
	ask(other, progress.PlayerStats)
	ps.expectPayload(7, NotifyStatsGetStatsAsync, fire.Notification, 0, StatsAsyncNotification(progress.PlayerStats, 1,
		other, values(map[string]string{"PLAYED": "1", "WINS": "0", "KILLS": "50", "KILLSINGAME": "50", "ASSISTS": "0",
			"ASSISTSINGAME": "0", "STREAKINGAME": "0", "DOUBLEKILLS": "0", "TRIPLEKILLS": "0", "TETRAKILLS": "0",
			"OMNIKILLS": "0"})))
	log := ps.close()
	for _, want := range []string{
		"profile: persona " + itoa(me) + "'s stats for persona " + itoa(me) + " in mode practice (2 games)",
		"profile: persona " + itoa(me) + "'s stats for persona " + itoa(me) + " in mode draft (1 games)",
		"profile: persona " + itoa(other) + "'s stats for persona " + itoa(me) + " in mode custom (1 games)",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

// profile.select picks the mode a player's profile requests are answered in: every connection's greeting names it
// (all at first) and the modes in the tabs' order; a new mode is kept per persona and every connection of the player
// hears it, the other players keep theirs; an unknown mode, a malformed call and a change within the gap are refused
// with the caller's state; the same mode again answers the state; the end of the last login forgets the mode.
func TestProfileSelect(t *testing.T) {
	ts := newTCPServer(t, nil)
	addr := attachUI(t, ts)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	ua, ua2, ub := uiConnect(t, addr, a), uiConnect(t, addr, a), uiConnect(t, addr, b)
	for _, u := range []*uiClient{ua, ua2, ub} {
		if u.pfGreeting != pfStateLine("all") {
			t.Errorf("greeting %s", u.pfGreeting)
		}
	}
	ua.call(CallProfileSelect, `{"mode":"practice"}`)
	ua.want(pfStateLine("practice"))
	ua2.want(pfStateLine("practice"))
	ub.quiet()
	if ts.s.profileMode(a.id) != ProfilePractice || ts.s.profileMode(b.id) != ProfileAll {
		t.Errorf("modes %q %q", ts.s.profileMode(a.id), ts.s.profileMode(b.id))
	}
	if log := ts.log.String(); !strings.Contains(log, "profile: persona "+itoa(a.id)+"'s mode practice (was all)") {
		t.Errorf("log:\n%s", log)
	}
	ub.call(CallProfileSelect, `{"mode":"draft"}`)
	ub.want(pfStateLine("draft"))

	// hostile and early calls: refused with the player's state (to each of its connections), the mode kept
	ts.s.pfGap = time.Hour
	for _, bad := range []string{`{"mode":"ranked"}`, `{"mode":"Practice"}`, `{"mode":7}`, `{}`, `"all"`, `{"mode":"custom"}`} {
		ua.call(CallProfileSelect, bad)
		ua.want(pfStateLine("practice"))
		ua2.want(pfStateLine("practice"))
	}
	// the same mode again: the state, no change
	ua.call(CallProfileSelect, `{"mode":"practice"}`)
	ua.want(pfStateLine("practice"))
	ua2.want(pfStateLine("practice"))
	ub.quiet()
	ts.s.pfGap = 0
	ua.call(CallProfileSelect, `{"mode":"all"}`)
	ua.want(pfStateLine("all"))
	ua2.want(pfStateLine("all"))
	if ts.s.profileMode(a.id) != ProfileAll || ts.s.profileMode(b.id) != rating.Draft {
		t.Errorf("modes %q %q", ts.s.profileMode(a.id), ts.s.profileMode(b.id))
	}

	// bravo's login ends: the mode is forgotten; the next login starts on All Modes
	b.c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for ts.s.profileMode(b.id) != ProfileAll && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := ts.s.profileMode(b.id); got != ProfileAll {
		t.Errorf("after the logout bravo's mode %q", got)
	}
	b2 := ts.client("bravo@example.com")
	if u := uiConnect(t, addr, b2); u.pfGreeting != pfStateLine("all") {
		t.Errorf("the new login's greeting %s", u.pfGreeting)
	}
}

// An admin-Unrated custom game leaves the profile's numbers alone (PlayerStats, the history), still gives XP; the log
// says so. A game counted as before.
func TestUnratedGameNotInProfile(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) { s.Content = loadFixtureContent(t); s.Rand = fixedRand(0.5) })
	ps.login(0, "extendeddataupdate_notification")
	g := progress.Game{Won: true, Seconds: 600}
	stats := map[string]int64{progress.StatNoun: int64(content.AssetID("stone.Noun")), progress.StatRole: 1,
		progress.StatKills: 4}
	acct, res, err := ps.srv.applyGame(store.FirstUserID, g, stats, nil, false, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Stats[progress.PlayerStats]["PLAYED"] != "" || len(acct.History.Shapers) != 0 || len(acct.History.Days) != 0 ||
		res.XP == 0 || !res.NotInProfile {
		t.Errorf("Unrated: stats %v history %+v xp %d", acct.Stats[progress.PlayerStats], acct.History, res.XP)
	}
	if got := res.describeHistory(ps.srv.Content); !strings.HasPrefix(got, "not in the profile (made Unrated by its admin; shaper stone") {
		t.Errorf("describeHistory %q", got)
	}
	acct, _, err = ps.srv.applyGame(store.FirstUserID, g, stats, nil, false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Stats[progress.PlayerStats]["PLAYED"] != "1" || acct.History.Shapers[0x10000002].Games != 1 {
		t.Errorf("counted: stats %v history %+v", acct.Stats[progress.PlayerStats], acct.History)
	}
	ps.close()
}
