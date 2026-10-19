package launcher

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/discord"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

const (
	ownID   = 2000000001
	guestID = 2000000002
	thirdID = 2000000003
)

// trackerRig is a tracker with a clock of its own and the activities it sent.
type trackerRig struct {
	t     *testing.T
	tr    *presenceTracker
	clock time.Time
	sent  []*discord.Activity
}

func newTrackerRig(t *testing.T) *trackerRig {
	r := &trackerRig{t: t, clock: time.Unix(1760000000, 0)}
	r.tr = newPresenceTracker(func() time.Time { return r.clock })
	r.tr.setOutput(func(a *discord.Activity) { r.sent = append(r.sent, a) })
	return r
}

func (r *trackerRig) server(comp, cmd uint16, typ fire.MsgType, payload []byte) {
	r.tr.fromServer(fire.Frame{Component: comp, Command: cmd, Type: typ, Payload: payload})
}

func (r *trackerRig) notify(comp, cmd uint16, payload []byte) {
	r.server(comp, cmd, fire.Notification, payload)
}

func (r *trackerRig) game(comp, cmd uint16, id uint32, payload []byte) {
	r.tr.fromGame(fire.Frame{Component: comp, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
}

// expect checks the last activity sent and that one was sent since the last check (changed) or none (!changed).
func (r *trackerRig) expect(step, details, state string, size int, start time.Time) {
	r.t.Helper()
	if len(r.sent) == 0 {
		r.t.Fatalf("%s: nothing sent", step)
	}
	a := r.sent[len(r.sent)-1]
	if a.Details != details || a.State != state || a.PartySize != size || !a.Start.Equal(start) {
		r.t.Errorf("%s: got %s, want %q / %q (%d) since %v", step, a, details, state, size, start)
	}
	if size > 0 && a.PartyMax != 5 {
		r.t.Errorf("%s: party max %d", step, a.PartyMax)
	}
	if a.LargeImage != "logo" || a.LargeText != "Dawngate" {
		r.t.Errorf("%s: assets %q %q", step, a.LargeImage, a.LargeText)
	}
}

// unchanged checks that nothing was sent since n activities.
func (r *trackerRig) unchanged(step string, n int) {
	r.t.Helper()
	if len(r.sent) != n {
		r.t.Errorf("%s: %d activities sent, want none (last %s)", step, len(r.sent)-n, r.sent[len(r.sent)-1])
	}
}

func party(id int64, key string) blaze.PlaygroupInfo {
	return blaze.PlaygroupInfo{ID: id, Name: "p", Key: key, MemberLimit: 5}
}

func member(id int64) blaze.PlaygroupMember {
	return blaze.PlaygroupMember{AccountID: id, BlazeID: id, Name: "m"}
}

func matchmakingRequest(flags string) []byte {
	w := tdf.NewWriter()
	w.Struct("GCDA", func(w *tdf.Writer) {
		w.Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV("GameFlags", flags), tdf.KV("GameType", "1"))
	})
	return w.Int("MODE", 1).Bytes()
}

func gameSetup(id int64, flags string, state int64) []byte {
	g := blaze.Game{ID: id, Name: "g", Attrs: []tdf.Pair{tdf.KV("GameFlags", flags)}, Capacity: []int64{10, 0}, State: state}
	return blaze.GameSetupNotification(g, blaze.Player{ID: ownID, Name: "me"})
}

// TestTrackerFlow: the frames of a whole evening, built by the server's own builders: the login screen, the menu, a
// party, the queue (failed once), shaper select, the match with its timer, the post-game, the removal, the party's
// end and the logout.
func TestTrackerFlow(t *testing.T) {
	r := newTrackerRig(t)
	r.expect("start", "At the login screen", "", 0, time.Time{})
	n := len(r.sent)
	r.tr.atLogin(true) // the redirector's request
	r.unchanged("redirector", n)

	acct := store.Account{UserID: ownID, Email: "me", Personas: []store.Persona{{ID: ownID, Name: "me"}}}
	r.server(blaze.ComponentAuthentication, blaze.CmdAuthLogin, fire.Reply, blaze.LoginReply(acct, "key", 1))
	r.tr.atLogin(false) // the front's own note of the reply
	r.expect("login", "In the menu", "", 0, time.Time{})
	r.server(blaze.ComponentAuthentication, blaze.CmdAuthLoginPersona, fire.Reply,
		blaze.SessionInfoReply(acct, acct.Personas[0], "key", false, 1))
	if r.tr.own != ownID {
		t.Fatalf("own persona %d", r.tr.own)
	}

	r.notify(blaze.ComponentPlaygroups, blaze.NotifyJoinPlaygroupID,
		blaze.JoinPlaygroupNotification(party(7, ""), []blaze.PlaygroupMember{member(ownID), member(guestID)}, ownID))
	r.expect("party", "In the menu", "In a party", 2, time.Time{})
	n = len(r.sent)
	r.notify(blaze.ComponentPlaygroups, blaze.NotifyJoinPlaygroupID, // the game's chat: not a party
		blaze.JoinPlaygroupNotification(party(8, "GG-1"), []blaze.PlaygroupMember{member(ownID)}, ownID))
	r.unchanged("chat playgroup", n)
	r.notify(blaze.ComponentPlaygroups, blaze.NotifyMemberJoinedPlaygroupID, blaze.MemberJoinedPlaygroupNotification(7, member(thirdID)))
	r.expect("third member", "In the menu", "In a party", 3, time.Time{})

	queued := r.clock
	r.game(blaze.ComponentGameManager, blaze.CmdGameManagerStartMatchmaking, 21, matchmakingRequest("1"))
	r.expect("queue", "In queue · PvP", "In a party", 3, queued)
	r.clock = r.clock.Add(30 * time.Second)
	r.notify(blaze.ComponentGameManager, blaze.NotifyMatchmakingFailedID,
		blaze.MatchmakingFailedNotification(blaze.MatchmakingContext{Session: 1, User: ownID, Result: 4}))
	r.expect("queue failed", "In the menu", "In a party", 3, time.Time{})
	queued = r.clock
	r.game(blaze.ComponentGameManager, blaze.CmdGameManagerStartMatchmaking, 22, matchmakingRequest("1"))
	r.expect("queue again", "In queue · PvP", "In a party", 3, queued)

	r.clock = r.clock.Add(time.Minute)
	r.notify(blaze.ComponentGameManager, blaze.NotifyGameSetupID, gameSetup(5, "1", 0))
	r.expect("shaper select", "Shaper select · PvP", "In a party", 3, time.Time{})

	r.clock = r.clock.Add(90 * time.Second)
	started := r.clock
	r.notify(blaze.ComponentGameManager, blaze.NotifyGameStateChangeID, blaze.GameStateChangeNotification(5, blaze.GameStateInGame))
	r.expect("in game", "In match · PvP", "In a party", 3, started)
	n = len(r.sent)
	r.clock = r.clock.Add(time.Minute)
	r.notify(blaze.ComponentGameManager, blaze.NotifyGameStateChangeID, blaze.GameStateChangeNotification(5, blaze.GameStateInGame))
	r.notify(blaze.ComponentGameManager, blaze.NotifyPlayerRemovedID, blaze.PlayerRemovedNotification(5, guestID, 1))
	r.notify(blaze.ComponentGameManager, blaze.NotifyGameStateChangeID, blaze.GameStateChangeNotification(6, blaze.GameStatePostGame))
	r.unchanged("the same state again, another's removal, another game's end", n)

	r.notify(blaze.ComponentGameManager, blaze.NotifyGameStateChangeID, blaze.GameStateChangeNotification(5, blaze.GameStatePostGame))
	r.expect("post-game", "Post-game · PvP", "In a party", 3, time.Time{})
	r.notify(blaze.ComponentGameManager, blaze.NotifyPlayerRemovedID, blaze.PlayerRemovedNotification(5, ownID, blaze.RemovedGameEnded))
	r.expect("removed", "In the menu", "In a party", 3, time.Time{})

	r.notify(blaze.ComponentPlaygroups, blaze.NotifyMemberRemovedFromPlaygroupID, blaze.MemberRemovedFromPlaygroupNotification(7, thirdID, 0))
	r.expect("a member left", "In the menu", "In a party", 2, time.Time{})
	r.game(blaze.ComponentPlaygroups, blaze.CmdPlaygroupsLeavePlaygroup, 30, tdf.NewWriter().Int("PGID", 7).Bytes())
	r.expect("left the party", "In the menu", "", 0, time.Time{})

	r.game(blaze.ComponentAuthentication, blaze.CmdAuthLogout, 31, nil)
	r.expect("logout", "At the login screen", "", 0, time.Time{})
}

// TestTrackerModes: the mode from GameFlags; a practice match; a lobby game; a rejoin of a running match.
func TestTrackerModes(t *testing.T) {
	for _, c := range []struct {
		flags string
		want  string
	}{{"1292", "Shaper select · Practice vs bots"}, {"1036", "Shaper select · Practice vs bots"},
		{"1042", "In the lobby · Practice group"}, {"2", "In the lobby · Custom game"}, {"", "Shaper select"}} {
		r := newTrackerRig(t)
		r.tr.atLogin(false)
		r.notify(blaze.ComponentGameManager, blaze.NotifyGameSetupID, gameSetup(9, c.flags, 0))
		r.expect("flags "+c.flags, c.want, "", 0, time.Time{})
	}
	r := newTrackerRig(t)
	r.tr.atLogin(false)
	r.notify(blaze.ComponentGameManager, blaze.NotifyGameSetupID, gameSetup(9, "1292", blaze.GameStateInGame))
	r.expect("rejoin", "In match · Practice vs bots", "", 0, r.clock)
	// a practice match through to the menu
	r = newTrackerRig(t)
	r.server(blaze.ComponentAuthentication, blaze.CmdAuthLoginPersona, fire.Reply, blaze.SessionInfoReply(
		store.Account{UserID: ownID}, store.Persona{ID: ownID, Name: "me"}, "k", false, 1))
	r.tr.atLogin(false)
	r.notify(blaze.ComponentGameManager, blaze.NotifyGameSetupID, gameSetup(9, "1292", 0))
	r.notify(blaze.ComponentGameManager, blaze.NotifyGameStateChangeID, blaze.GameStateChangeNotification(9, blaze.GameStateInGame))
	r.expect("practice match", "In match · Practice vs bots", "", 0, r.clock)
	r.notify(blaze.ComponentGameManager, blaze.NotifyPlayerRemovedID, blaze.PlayerRemovedNotification(9, ownID, blaze.RemovedGameEnded))
	r.expect("practice removed", "In the menu", "", 0, time.Time{})
}

// TestTrackerQueueEnds: a cancel, an error reply to the request, a guest following the leader's status.
func TestTrackerQueueEnds(t *testing.T) {
	r := newTrackerRig(t)
	r.tr.atLogin(false)
	r.game(blaze.ComponentGameManager, blaze.CmdGameManagerStartMatchmaking, 3, matchmakingRequest("1"))
	r.game(blaze.ComponentGameManager, blaze.CmdGameManagerCancelMatchmaking, 4, nil)
	r.expect("cancel", "In the menu", "", 0, time.Time{})
	r.game(blaze.ComponentGameManager, blaze.CmdGameManagerStartMatchmaking, 5, matchmakingRequest("1"))
	r.server(blaze.ComponentGameManager, blaze.CmdGameManagerStartMatchmaking, fire.ErrorReply, nil) // id 0: not ours
	r.expect("another id's error", "In queue · PvP", "", 0, r.clock)
	r.tr.fromServer(fire.Frame{Component: blaze.ComponentGameManager, Command: blaze.CmdGameManagerStartMatchmaking, Type: fire.ErrorReply, ID: 5})
	r.expect("refused", "In the menu", "", 0, time.Time{})

	// a guest: the leader's MatchmakingStatus (kGEMS 1 matchmaking, 0 none)
	r.notify(blaze.ComponentPlaygroups, blaze.NotifyJoinPlaygroupID,
		blaze.JoinPlaygroupNotification(party(4, ""), []blaze.PlaygroupMember{member(guestID), member(ownID)}, ownID))
	r.notify(blaze.ComponentPlaygroups, blaze.NotifyPlaygroupAttributesSetID,
		blaze.PlaygroupAttributesSetNotification(4, []tdf.Pair{tdf.KV("MatchmakingStatus", "1|3|0")}))
	r.expect("guest queued", "In queue · PvP", "In a party", 2, r.clock)
	r.notify(blaze.ComponentPlaygroups, blaze.NotifyPlaygroupAttributesSetID,
		blaze.PlaygroupAttributesSetNotification(4, []tdf.Pair{tdf.KV("MatchmakingStatus", "0|4|1F4")}))
	r.expect("guest out", "In the menu", "In a party", 2, time.Time{})
	r.notify(blaze.ComponentPlaygroups, blaze.NotifyDestroyPlaygroupID, blaze.DestroyPlaygroupNotification(4, 0))
	r.expect("destroyed", "In the menu", "", 0, time.Time{})
}

// TestTrackerMalformed: payloads that don't decode, or lack the members read, change nothing (and never panic).
func TestTrackerMalformed(t *testing.T) {
	r := newTrackerRig(t)
	r.tr.atLogin(false)
	r.notify(blaze.ComponentPlaygroups, blaze.NotifyJoinPlaygroupID,
		blaze.JoinPlaygroupNotification(party(7, ""), []blaze.PlaygroupMember{member(ownID)}, ownID))
	n := len(r.sent)
	bad := [][]byte{{0xFF, 0xFF, 0xFF}, {0x01}, tdf.NewWriter().Int("XXXX", 1).Bytes(), tdf.NewWriter().String("GAME", "x").Bytes(), nil}
	for _, p := range bad {
		r.notify(blaze.ComponentGameManager, blaze.NotifyGameSetupID, p)
		r.notify(blaze.ComponentGameManager, blaze.NotifyGameStateChangeID, p)
		r.notify(blaze.ComponentGameManager, blaze.NotifyPlayerRemovedID, p)
		r.notify(blaze.ComponentPlaygroups, blaze.NotifyJoinPlaygroupID, p)
		r.notify(blaze.ComponentPlaygroups, blaze.NotifyMemberJoinedPlaygroupID, p)
		r.notify(blaze.ComponentPlaygroups, blaze.NotifyMemberRemovedFromPlaygroupID, p)
		r.notify(blaze.ComponentPlaygroups, blaze.NotifyDestroyPlaygroupID, p)
		r.notify(blaze.ComponentPlaygroups, blaze.NotifyPlaygroupAttributesSetID, p)
		r.server(blaze.ComponentAuthentication, blaze.CmdAuthLogin, fire.Reply, p)
		r.server(blaze.ComponentAuthentication, blaze.CmdAuthLoginPersona, fire.Reply, p)
		r.game(blaze.ComponentPlaygroups, blaze.CmdPlaygroupsLeavePlaygroup, 1, p)
	}
	r.unchanged("malformed", n)
	if a := r.tr.Activity(); a.Details != "In the menu" || a.PartySize != 1 {
		t.Errorf("after the malformed frames: %s", a)
	}
	// a nil tracker (the status off) takes every hook
	var nt *presenceTracker
	nt.fromGame(fire.Frame{})
	nt.fromServer(fire.Frame{Type: fire.Notification})
	nt.atLogin(true)
	nt.setOutput(nil)
}

func TestDiscordSetting(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == discord.TestPipeEnv {
				return v
			}
			return ""
		}
	}
	none := env("")
	ini := Config{DiscordAppIDKey: "123456789012345678"}
	cases := []struct {
		name   string
		flag   string
		ini    Config
		test   bool
		env    func(string) string
		on     bool
		pipes  int
		why    string
		failed bool
	}{
		{"no built-in id, none configured: off", "", nil, false, none, false, 0, "no Discord application id", false},
		{"an id configured", "", ini, false, none, true, 10, "your Discord status", false},
		{"ini off", "", Config{DiscordKey: "off", DiscordAppIDKey: "1"}, false, none, false, 0, "discord = off", false},
		{"flag off over ini on", "off", Config{DiscordKey: "on", DiscordAppIDKey: "1"}, false, none, false, 0, "discord = off", false},
		{"flag on over ini off", "on", Config{DiscordKey: "off", DiscordAppIDKey: "1"}, false, none, true, 10, "", false},
		{"a test run", "", ini, true, none, false, 0, "a test run", false},
		{"a test run with the hook", "", nil, true, env("dawngate-test-discord-b"), true, 1, "test hook", false},
		{"the hook outside a test run", "", ini, false, env("dawngate-test-discord-b"), true, 1, "test hook", false},
		{"the hook never names the app's pipe", "", ini, true, env("discord-ipc-0"), false, 0, "", true},
		{"a bad value", "maybe", nil, false, none, false, 0, "", true},
		{"a bad id", "", Config{DiscordAppIDKey: "abc"}, false, none, false, 0, "", true},
	}
	saved := defaultDiscordAppID
	defer func() { defaultDiscordAppID = saved }()
	defaultDiscordAppID = "" // a build without a built-in id: no id, no status
	for _, c := range cases {
		dc, err := discordSetting(c.flag, c.ini, c.test, c.env)
		if (err != nil) != c.failed {
			t.Errorf("%s: err %v", c.name, err)
			continue
		}
		if c.failed {
			continue
		}
		if dc.on != c.on || len(dc.pipes) != c.pipes || !strings.Contains(dc.why, c.why) {
			t.Errorf("%s: %+v", c.name, dc)
		}
		if dc.on && dc.appID == "" {
			t.Errorf("%s: no application id", c.name)
		}
		for _, p := range dc.pipes {
			if c.test && strings.Contains(p, "discord-ipc") {
				t.Errorf("%s: a test run would use the app's pipe %s", c.name, p)
			}
		}
	}
	// the table above ran as a build without a built-in id; now with the release's
	defaultDiscordAppID = discord.DefaultAppID
	if discord.DefaultAppID == "" || strings.Trim(discord.DefaultAppID, "0123456789") != "" {
		t.Fatalf("the built-in application id %q isn't a number", discord.DefaultAppID)
	}
	for _, c := range []struct {
		name string
		ini  Config
		test bool
		env  func(string) string
		on   bool
		id   string
	}{
		{"defaults: the built-in id", nil, false, none, true, discord.DefaultAppID},
		{"discord_app_id over the built-in id", ini, false, none, true, ini[DiscordAppIDKey]},
		{"a test run stays off with the built-in id", nil, true, none, false, ""},
		{"the hook with the built-in id", nil, true, env("dawngate-test-discord-b"), true, discord.DefaultAppID},
	} {
		dc, err := discordSetting("", c.ini, c.test, c.env)
		if err != nil || dc.on != c.on || dc.appID != c.id {
			t.Errorf("%s: %+v %v", c.name, dc, err)
		}
		for _, p := range dc.pipes {
			if c.test && strings.Contains(p, "discord-ipc") {
				t.Errorf("%s: a test run would use the app's pipe %s", c.name, p)
			}
		}
	}
	// without any id the hook gets a placeholder
	defaultDiscordAppID = ""
	if dc, _ := discordSetting("", nil, true, env("dawngate-test-discord-b")); !dc.on || dc.appID != discordTestAppID {
		t.Errorf("the hook without an id: %+v", dc)
	}
}

func TestDiscordConfigText(t *testing.T) {
	cfg, err := ParseConfig(strings.NewReader(ClientConfigText), ClientConfigName)
	if err != nil {
		t.Fatal(err)
	}
	if cfg[DiscordKey] != "on" {
		t.Errorf("discord = %q", cfg[DiscordKey])
	}
	if _, ok := cfg[DiscordAppIDKey]; !ok {
		t.Error("no discord_app_id line")
	}
	if u := cfg.Unknown(clientKeys...); len(u) > 0 {
		t.Errorf("unknown keys %q", u)
	}
	// a bad value stops Start-Client before anything else (here: --check)
	dir := t.TempDir()
	var out bytes.Buffer
	if code := RunClient([]string{"--check", "--discord", "maybe"}, IO{In: strings.NewReader(""), Out: &out, ExeDir: dir}); code == 0 ||
		!strings.Contains(out.String(), "discord = maybe") {
		t.Errorf("exit %d: %s", code, out.String())
	}
	os.WriteFile(filepath.Join(dir, ClientConfigName), []byte("discord = sometimes\n"), 0o644)
	out.Reset()
	if code := RunClient([]string{"--check"}, IO{In: strings.NewReader(""), Out: &out, ExeDir: dir}); code == 0 ||
		!strings.Contains(out.String(), "discord = sometimes") || strings.Contains(out.String(), "unknown option") {
		t.Errorf("exit %d: %s", code, out.String())
	}
}

// TestTrackerJoinTarget: what a friend's Join would join (a party in the menu and the queue, a lobby of a practice
// group or custom game; nothing in shaper select, a match or at the login screen), the front woken at each change,
// the join secret shown only while its target is still the current one, a lobby's roster change waking the front.
func TestTrackerJoinTarget(t *testing.T) {
	r := newTrackerRig(t)
	wakes := 0
	r.tr.mu.Lock()
	r.tr.changed = func() { wakes++ }
	r.tr.mu.Unlock()
	if _, ok, _ := r.tr.JoinTarget(); ok {
		t.Fatal("a target at the login screen")
	}
	acct := store.Account{UserID: ownID, Email: "me", Personas: []store.Persona{{ID: ownID, Name: "me"}}}
	r.server(blaze.ComponentAuthentication, blaze.CmdAuthLogin, fire.Reply, blaze.LoginReply(acct, "key", 1))
	r.server(blaze.ComponentAuthentication, blaze.CmdAuthLoginPersona, fire.Reply, blaze.SessionInfoReply(acct, acct.Personas[0], "key", false, 1))
	r.notify(blaze.ComponentPlaygroups, blaze.NotifyJoinPlaygroupID,
		blaze.JoinPlaygroupNotification(party(7, ""), []blaze.PlaygroupMember{member(ownID)}, ownID))
	w, ok, _ := r.tr.JoinTarget()
	if !ok || w != (joinWant{blaze.JoinTargetParty, 7}) || wakes < 2 {
		t.Fatalf("party: %+v %v, %d wakes", w, ok, wakes)
	}
	r.tr.setJoin(&issuedJoin{want: w, info: blaze.JoinCodeInfo{Code: "c", Joinable: true, Size: 1, Max: 5}, secret: "dg1/s", partyID: "p"})
	if a := r.sent[len(r.sent)-1]; a.JoinSecret != "dg1/s" || a.PartyID != "p" || a.PartySize != 1 || a.PartyMax != 5 {
		t.Errorf("the secret not shown: %s %+v", a, a)
	}
	n := wakes
	r.notify(blaze.ComponentPlaygroups, blaze.NotifyMemberJoinedPlaygroupID, blaze.MemberJoinedPlaygroupNotification(7, member(guestID)))
	if wakes == n {
		t.Error("a new member didn't wake the front")
	}
	// shaper select: no target, the secret gone at once
	r.notify(blaze.ComponentGameManager, blaze.NotifyGameSetupID, gameSetup(5, "1", 0))
	if _, ok, _ := r.tr.JoinTarget(); ok {
		t.Error("a target in shaper select")
	}
	if a := r.sent[len(r.sent)-1]; a.JoinSecret != "" || a.PartyID != "" {
		t.Errorf("a secret in shaper select: %s", a)
	}
	// a custom lobby and a practice group: the game; practice vs bots' shaper select: none
	for _, c := range []struct {
		flags string
		ok    bool
	}{{"2", true}, {"1042", true}, {"1036", false}} {
		r.notify(blaze.ComponentGameManager, blaze.NotifyGameSetupID, gameSetup(9, c.flags, 0))
		w, ok, _ := r.tr.JoinTarget()
		if ok != c.ok || (ok && w != (joinWant{blaze.JoinTargetGame, 9})) {
			t.Errorf("flags %s: %+v %v", c.flags, w, ok)
		}
	}
	r.notify(blaze.ComponentGameManager, blaze.NotifyGameSetupID, gameSetup(9, "2", 0))
	n = wakes
	r.notify(blaze.ComponentGameManager, 0x15, tdf.NewWriter().Int("GID", 9).Bytes()) // a player joins the lobby
	r.notify(blaze.ComponentGameManager, 0x15, tdf.NewWriter().Int("GID", 4).Bytes()) // another game's
	if wakes != n+1 {
		t.Errorf("the lobby's roster: %d wakes, want 1", wakes-n)
	}
	r.tr.setJoin(&issuedJoin{want: joinWant{blaze.JoinTargetGame, 9}, info: blaze.JoinCodeInfo{Code: "c", Joinable: true, Size: 3, Max: 10},
		secret: "dg1/g", partyID: "q"})
	if a := r.sent[len(r.sent)-1]; a.JoinSecret != "dg1/g" || a.PartySize != 3 || a.PartyMax != 10 || a.State != "In a party" {
		t.Errorf("the lobby's activity: %s (%q)", a, a.State)
	}
}
