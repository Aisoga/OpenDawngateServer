package blaze

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// b7dNow is when the B7d tests look at the profile: two days after the test time.
const b7dNow = testTime + 2*86400

// b7dGames are the two hosted games of the B7d tests (tdf_golden.py writes their stats independently): game 1
// at the test time as Frost Ember (a skin of ember 0x10000001) in the Gladiator role, won after 12 minutes;
// game 2 two days later (b7dNow) as Stone (0x10000002) in the Hunter role, lost after 30 minutes.
var b7dGames = []struct {
	at      int64 // Unix time of the game's end
	winner  int   // finishGame.winnerTeam (the player is on team 1)
	ms      int64 // finishGame.durationMs
	stats   map[string]int64
	shaper  uint32
	role    int64
	history string // the log line's text
}{
	{testTime, 1, 720000, map[string]int64{progress.StatNoun: int64(content.AssetID("ember_frost.Noun")),
		progress.StatRole: progress.RoleGladiator, progress.StatKills: 3, progress.StatDeaths: 1, progress.StatAssists: 2,
		progress.StatCreeps: 40, progress.StatGold: 5000}, 0x10000001, progress.RoleGladiator,
		"shaper ember (0x10000001), role 1 (gladiator)"},
	{b7dNow, 2, 1800000, map[string]int64{progress.StatNoun: int64(content.AssetID("stone.Noun")),
		progress.StatRole: progress.RoleHunter, progress.StatKills: 1, progress.StatDeaths: 4, progress.StatAssists: 6,
		progress.StatCreeps: 90, progress.StatGold: 9000}, 0x10000002, progress.RoleHunter,
		"shaper stone (0x10000002), role 3 (hunter)"},
}

// b7dHistory is the fixture account's history after the two games, as progress builds it.
func b7dHistory(t testing.TB) store.History {
	c := loadFixtureContent(t)
	var h store.History
	for _, g := range b7dGames {
		shaper, ok := c.ShaperOf(uint32(g.stats[progress.StatNoun]))
		if !ok || shaper != g.shaper {
			t.Fatalf("ShaperOf(%#x) = %#x, %v", g.stats[progress.StatNoun], shaper, ok)
		}
		h = progress.ApplyHistory(h, time.Unix(g.at, 0), shaper, g.stats[progress.StatRole], g.winner == 1, g.stats,
			float64(g.ms)/1000)
	}
	return h
}

// b7dPayloads are the B7d replies with their golden case and member-table class.
func b7dPayloads(t testing.TB) []struct {
	name, class string
	got         []byte
} {
	pid := int64(store.FirstPersonaID)
	h, now := b7dHistory(t), time.Unix(b7dNow, 0)
	shapers := KeyedStatsNotifications(progress.ShaperStats, 2, pid, progress.ShaperStatsOf(h))
	history := KeyedStatsNotifications(progress.ShaperHistoryStats, 3, pid, progress.ShaperHistoryOf(h, now, 0, 7))
	roles := KeyedStatsNotifications(progress.RoleHistoryStats, 4, pid, progress.RoleHistoryOf(h, now, 0, 7))
	if len(shapers) != 2 || len(history) != 2 || len(roles) != 2 {
		t.Fatalf("notifications: %d shapers, %d history, %d roles", len(shapers), len(history), len(roles))
	}
	const group, values = "Blaze::Stats::StatGroupResponse", "Blaze::Stats::KeyScopedStatValues"
	return []struct {
		name, class string
		got         []byte
	}{
		{"b7d_getstatgroup_shaperstats_reply", group, StatGroupReply(progress.ShaperStats, StatGroups[progress.ShaperStats])},
		{"b7d_getstatgroup_shaperhistorystats_reply", group,
			StatGroupReply(progress.ShaperHistoryStats, StatGroups[progress.ShaperHistoryStats])},
		{"b7d_getstatgroup_rolehistorystats_reply", group, StatGroupReply(progress.RoleHistoryStats, StatGroups[progress.RoleHistoryStats])},
		{"b7d_shaperstats_empty_notification", values, KeyedStatsNotifications(progress.ShaperStats, 2, pid, nil)[0]},
		{"b7d_shaperstats_ember_notification", values, shapers[0]},
		{"b7d_shaperstats_stone_notification", values, shapers[1]},
		{"b7d_shaperhistorystats_ember_notification", values, history[0]},
		{"b7d_shaperhistorystats_stone_notification", values, history[1]},
		{"b7d_rolehistorystats_gladiator_notification", values, roles[0]},
		{"b7d_rolehistorystats_hunter_notification", values, roles[1]},
	}
}

func TestB7dRepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range b7dPayloads(t) {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// expectPayload checks the next frame's header and payload.
func (ps *pipeSession) expectPayload(comp, cmd uint16, typ fire.MsgType, id uint32, want []byte) {
	ps.t.Helper()
	f := ps.next("frame")
	if f.Component != comp || f.Command != cmd || f.Type != typ || f.ID != id || f.Error != 0 {
		ps.t.Errorf("got header %#x/%#x %s id %d err %#x, want %#x/%#x %s id %d", f.Component, f.Command, f.Type, f.ID,
			f.Error, comp, cmd, typ, id)
	}
	if !bytes.Equal(f.Payload, want) {
		ps.t.Errorf("%#x/%#x payload\n got %x\nwant %x", comp, cmd, f.Payload, want)
	}
}

// Two hosted games (fake match hosts over the control channel on an ephemeral port), two days apart: the
// profile's keyed groups answer nothing before them and both games after them, by shaper (the skin's
// game counts for its shaper) and by role, each history row on its day; the getStatGroup replies carry our
// stat names; the account keeps the history; the log names what each game added.
func TestHostedGamesShaperAndRoleHistory(t *testing.T) {
	var clock, played atomic.Int64
	clock.Store(testTime) // the login's goldens
	hs := newHostedSessionFull(t, "", "", nil, func(s *Server) {
		s.Content = loadFixtureContent(t)
		s.Rand = fixedRand(0.5)
		s.Now = func() time.Time { return time.Unix(clock.Load(), 0) }
	}, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		g := b7dGames[played.Add(1)-1]
		for _, m := range []gmshost.Message{gmshost.AddPlayer{User: store.FirstPersonaID},
			gmshost.GMSUpdate{Phase: gmshost.PhaseInGame},
			gmshost.PlayerFinish{User: store.FirstPersonaID, Stats: g.stats},
			gmshost.FinishGame{WinnerTeam: g.winner, DurationMs: g.ms, Reason: "guardian"}} {
			if err := h.Send(m); err != nil {
				return err
			}
		}
		return nil
	})
	hs.login()
	// a new account: no keys, the view still completes
	hs.send(7, CmdStatsGetStatsByGroupAsync, 2, hs.request("getstatsbygroupasync_shaperstats_request"))
	hs.expect(7, CmdStatsGetStatsByGroupAsync, fire.Reply, 2, "")
	hs.expect(7, NotifyStatsGetStatsAsync, fire.Notification, 0, "b7d_shaperstats_empty_notification")
	for i, g := range b7dGames {
		gid, id := int64(i+1), uint32(10+i)
		clock.Store(g.at)
		hs.send(4, 0x19, id, hs.request("creategame_request"))
		hs.expectPayload(4, 0x19, fire.Reply, id, JoinGameReply(gid))
		if cg := hs.hostGot().(gmshost.CreateGame); cg.GameID != gid {
			t.Errorf("createGame for game %d", cg.GameID)
		}
		if f := hs.next("NotifyGameSetup"); f.Component != ComponentGameManager || f.Command != NotifyGameSetupID {
			t.Errorf("got %#x/%#x, want NotifyGameSetup", f.Component, f.Command)
		}
		hs.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(gid, GameStateInGame))
		hs.expect(0x7802, NotifyUserSessionExtendedDataUpdate, fire.Notification, 0, "b7c_extendeddataupdate_notification")
		hs.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(gid, GameStatePostGame))
		hs.expectNote(NotifyPlayerRemovedID, PlayerRemovedNotification(gid, store.FirstPersonaID, RemovedGameEnded))
		hs.waitIdle()
	}
	// the profile at game 2's time (b7dNow), as the client asks for it (the requests of session g2_run2)
	for i, group := range []string{progress.ShaperStats, progress.ShaperHistoryStats, progress.RoleHistoryStats} {
		id := uint32(20 + i)
		hs.send(7, CmdStatsGetStatGroup, id, tdf.NewWriter().String("NAME", group).Bytes())
		hs.expect(7, CmdStatsGetStatGroup, fire.Reply, id, "b7d_getstatgroup_"+strings.ToLower(group)+"_reply")
	}
	for i, c := range []struct{ request, first, second string }{
		{"shaperstats", "ember", "stone"}, {"shaperhistorystats", "ember", "stone"}, {"rolehistorystats", "gladiator", "hunter"},
	} {
		id := uint32(30 + i)
		hs.send(7, CmdStatsGetStatsByGroupAsync, id, hs.request("getstatsbygroupasync_"+c.request+"_request"))
		hs.expect(7, CmdStatsGetStatsByGroupAsync, fire.Reply, id, "")
		hs.expect(7, NotifyStatsGetStatsAsync, fire.Notification, 0, "b7d_"+c.request+"_"+c.first+"_notification")
		hs.expect(7, NotifyStatsGetStatsAsync, fire.Notification, 0, "b7d_"+c.request+"_"+c.second+"_notification")
	}
	acct, _ := hs.s.Store.Account(store.FirstUserID)
	if want := b7dHistory(t); !reflect.DeepEqual(acct.History, want) {
		t.Errorf("history\n got %+v\nwant %+v", acct.History, want)
	}
	if ps := acct.Stats[progress.PlayerStats]; ps["PLAYED"] != "2" || ps["WINS"] != "1" || ps["KILLS"] != "4" {
		t.Errorf("PlayerStats %v", ps)
	}
	hs.close()
	log := hs.log.String()
	for i, g := range b7dGames {
		want := "[blaze]    account 1000000001: game " + string(rune('1'+i)) + " history: " + g.history + "\n"
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// A game without a noun and role (today's hosts send none) counts in neither history, and the log says why;
// a noun that is no shaper of the content is named.
func TestHistoryWithoutShaperOrRole(t *testing.T) {
	r := GameResult{}
	if got := r.describeHistory(nil); got != "no shaper (no noun in playerFinish), no role" {
		t.Errorf("describeHistory: %q", got)
	}
	r = GameResult{Noun: 0x1234, Role: 9}
	if got := r.describeHistory(loadFixtureContent(t)); got != "no shaper (noun 0x1234 is none in the content), no role (role 9 is none of 1-4)" {
		t.Errorf("describeHistory: %q", got)
	}
	if h := progress.ApplyHistory(store.History{}, time.Unix(testTime, 0), 0, 9, true, nil, 60); !reflect.DeepEqual(h, store.History{}) {
		t.Errorf("history %+v", h)
	}
}

// Another player's profile: getStatsByGroupAsync answers for the persona in EID, with its Blaze id in the
// rows; an unknown persona gets no keys.
func TestStatsForAnotherProfile(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) { s.Now = func() time.Time { return time.Unix(b7dNow, 0) } })
	ps.login(0, "extendeddataupdate_notification")
	acct, _, err := ps.srv.Store.Login("other@example.com", time.Unix(b7dNow, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.srv.Store.Update(acct.UserID, func(a *store.Account) { a.History = b7dHistory(t) }); err != nil {
		t.Fatal(err)
	}
	me, other := int64(store.FirstPersonaID), acct.Personas[0].ID
	request := func(eid int64) []byte {
		return tdf.NewWriter().List("EID", tdf.TypeInt, []int64{eid}).String("NAME", progress.RoleHistoryStats).
			Int("PCTR", 7).Int("PTYP", 3).Int("VID", 4).Bytes()
	}
	ps.send(7, CmdStatsGetStatsByGroupAsync, 2, request(other))
	ps.expect(7, CmdStatsGetStatsByGroupAsync, fire.Reply, 2, "")
	want := KeyedStatsNotifications(progress.RoleHistoryStats, 4, other,
		progress.RoleHistoryOf(b7dHistory(t), time.Unix(b7dNow, 0), 0, 7))
	for _, w := range want {
		ps.expectPayload(7, NotifyStatsGetStatsAsync, fire.Notification, 0, w)
	}
	ps.send(7, CmdStatsGetStatsByGroupAsync, 3, request(42))
	ps.expect(7, CmdStatsGetStatsByGroupAsync, fire.Reply, 3, "")
	ps.expectPayload(7, NotifyStatsGetStatsAsync, fire.Notification, 0, KeyedStatsNotifications(progress.RoleHistoryStats, 4, 42, nil)[0])
	// the player's own profile (its own EID): nothing played yet
	ps.send(7, CmdStatsGetStatsByGroupAsync, 4, request(me))
	ps.expect(7, CmdStatsGetStatsByGroupAsync, fire.Reply, 4, "")
	ps.expectPayload(7, NotifyStatsGetStatsAsync, fire.Notification, 0, KeyedStatsNotifications(progress.RoleHistoryStats, 4, me, nil)[0])
	ps.close()
}

// One key with rows on several days: one EntityStats per row, each with its POFF; ShaperStats rows carry none.
func TestKeyedStatsNotificationRows(t *testing.T) {
	keys := []progress.Keyed{{ID: 7, Rows: []progress.Row{{Offset: 0, Values: []uint32{2, 1}}, {Offset: 6, Values: []uint32{1, 0}}}}}
	for _, c := range []struct {
		group, key string
		poff       bool
	}{{progress.RoleHistoryStats, "role=7", true}, {progress.ShaperStats, "shaper=7", false}} {
		n := KeyedStatsNotifications(c.group, 9, 5, keys)
		if len(n) != 1 {
			t.Fatalf("%d notifications", len(n))
		}
		st, err := tdf.Decode(n[0])
		if err != nil {
			t.Fatal(err)
		}
		key, _ := st.Str("KEY")
		last, _ := st.Int("LAST")
		sts, _ := st.Sub("STS")
		rows := structs(sts, "STAT")
		if key != c.key || last != 1 || len(rows) != 2 {
			t.Fatalf("%s: KEY %q LAST %d, %d rows", c.group, key, last, len(rows))
		}
		for i, r := range rows {
			eid, _ := r.Int("EID")
			poff, has := r.Int("POFF")
			if eid != 5 || has != c.poff || (has && poff != int64(keys[0].Rows[i].Offset)) {
				t.Errorf("%s row %d: EID %d POFF %d (%v)", c.group, i, eid, poff, has)
			}
		}
	}
}
