package blaze

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

// M27, M28: games in their players' Preferred Regions (regions.go, preferred.go). The servers here are Europe's (home
// eu, "Europe") with a North American machine na1 ("NA (Virginia)"; gmshost.Manager.AddTestMachine: a stand-in for a
// registered host machine).

func TestRegionID(t *testing.T) {
	for alias, want := range map[string]string{"eu": "1819748219", "EU": "1819748219", "na": "1668749442"} {
		if got := RegionID(alias); got != want {
			t.Errorf("RegionID(%q) = %s, want %s", alias, got, want)
		}
	}
}

// The vote: one vote per human, the most votes win, any tie for the most is home; an empty or unknown preference
// counts as home.
func TestVoteRegion(t *testing.T) {
	ids := []string{"eu", "na"}
	rep := func(r string, n int) []string { return strings.Split(strings.Repeat(r+" ", n-1)+r, " ") }
	for _, c := range []struct {
		what  string
		votes []string
		want  string
	}{
		{"5 NA + 1 EU", append(rep("na", 5), "eu"), "na"},
		{"3 NA + 3 EU: a tie", append(rep("na", 3), rep("eu", 3)...), "eu"},
		{"1 NA alone", []string{"na"}, "na"},
		{"no votes", nil, "eu"},
		{"2 NA + an unknown + an empty: a tie with home", []string{"na", "na", "ap", ""}, "eu"},
		{"2 NA + 1 EU + 1 unknown: a tie", []string{"na", "na", "eu", "ap"}, "eu"},
		{"3 NA + 2 EU", append(rep("na", 3), rep("eu", 2)...), "na"},
	} {
		if got := voteRegion(c.votes, "eu", ids); got != c.want {
			t.Errorf("%s: %q, want %q", c.what, got, c.want)
		}
	}
	if got := describeVotes(append(rep("na", 5), "eu"), "eu", ids); got != "na 5, eu 1" {
		t.Errorf("describeVotes %q", got)
	}
}

// lobby.state's region: always the id and name where the game runs, the notice's fields only for a fallback.
func TestRegionInfo(t *testing.T) {
	m := gmshost.NewManager(gmshost.Config{})
	m.SetRegions("eu", map[string]string{"na1": "na"})
	m.SetRegionNames(map[string]string{"eu": "Europe", "na": "NA (Virginia)"})
	s := &Server{Hosts: m}
	for _, c := range []struct {
		region, wanted, fellBack, want string
	}{
		{"na", "na", "", `{"id":"na","name":"NA (Virginia)"}`},
		{"eu", "", "", `{"id":"eu","name":"Europe"}`},
		{"eu", "eu", "", `{"id":"eu","name":"Europe"}`},
		{"eu", "na", "full", `{"id":"eu","name":"Europe","want":"na","wantName":"NA (Virginia)","got":"eu","why":"full"}`},
		{"", "", "", `{"id":"eu","name":"Europe"}`},
	} {
		b, _ := json.Marshal(s.regionInfo(c.region, c.wanted, c.fellBack))
		if string(b) != c.want {
			t.Errorf("%s/%s/%s: %s\nwant %s", c.region, c.wanted, c.fellBack, b, c.want)
		}
	}
	b, _ := json.Marshal((&Server{}).regionInfo("", "", ""))
	if string(b) != localRegionJSON {
		t.Errorf("no regions: %s", b)
	}
}

// getUserSettings: an account that never chose a region in EA's picker gets the home region's id (tdf_golden.py's
// region3_getusersettings_default_reply); a stored choice and regions off stay as they are; our PreferredRegion
// isn't sent to the client.
func TestUserSettingsDefaultRegion(t *testing.T) {
	g := golden.Load(t)
	m := gmshost.NewManager(gmshost.Config{})
	s := &Server{Hosts: m}
	if got := s.userSettings(nil); got != nil {
		t.Errorf("regions off: %v", got)
	}
	m.SetRegions("eu", map[string]string{"na1": "na"})
	stored := map[string]string{"Audio.Master": "0.5"}
	got := s.userSettings(stored)
	if h, want := hex.EncodeToString(GetUserSettingsReply(got)), g.WriterCase(t, "region3_getusersettings_default_reply").Hex; h != want {
		t.Errorf("reply\n got %s\nwant %s", h, want)
	}
	if len(stored) != 1 {
		t.Error("the stored settings changed")
	}
	chose := map[string]string{SettingRegionID: RegionID("na")}
	if got := s.userSettings(chose); got[SettingRegionID] != RegionID("na") {
		t.Errorf("a stored choice: %v", got)
	}
	if got := (&Server{}).userSettings(stored); len(got) != 1 {
		t.Errorf("no match hosts: %v", got)
	}
	ours := map[string]string{"Audio.Master": "0.5", SettingPreferredRegion: "na", SettingRegionID: RegionID("eu")}
	if got := s.userSettings(ours); len(got) != 2 || got[SettingPreferredRegion] != "" || len(ours) != 3 {
		t.Errorf("PreferredRegion sent or dropped from the store: %v (stored %v)", got, ours)
	}
}

// regionServer is a server of region eu whose machine na1 (if na is not nil) runs North America's hosts, with the
// regions' names, and its UI channel's address.
func regionServer(t *testing.T, na gmshost.Launcher, naPool *gmshost.Pool, set func(s *Server)) (*tcpServer, *customHost, string) {
	ts, host := newCustomHostServer(t, func(s *Server) {
		s.Hosts.SetRegions("eu", map[string]string{"na1": "na"})
		s.Hosts.SetRegionNames(map[string]string{"eu": "Europe", "na": "NA (Virginia)"})
		if na != nil {
			s.Hosts.AddTestMachine("na1", na, naPool, "")
		}
		if set != nil {
			set(s)
		}
	})
	return ts, host, attachUI(t, ts)
}

// regionStateLine is region.state on regionServer's servers with selected chosen.
func regionStateLine(selected string) string {
	return `{"event":"region.state","data":{"regions":[{"id":"eu","name":"Europe"},{"id":"na","name":"NA (Virginia)"}],` +
		`"home":"eu","selected":"` + selected + `"}}`
}

// regionJSON is lobby.state's region for a game running in Europe or North America.
func regionJSON(id string) string {
	return map[string]string{"eu": `{"id":"eu","name":"Europe"}`, "na": `{"id":"na","name":"NA (Virginia)"}`}[id]
}

// prefer stores tc's Preferred Region directly (the channel's own path: TestRegionSet).
func prefer(t *testing.T, ts *tcpServer, tc *tcpClient, region string) {
	t.Helper()
	acct, _, ok := ts.s.Store.PersonaByID(tc.id)
	if !ok {
		t.Fatalf("no account for %s", tc.name)
	}
	if _, err := ts.s.Store.Update(acct.UserID, func(a *store.Account) { a.Settings[SettingPreferredRegion] = region }); err != nil {
		t.Fatal(err)
	}
}

// pickRegion sends EA's play screen region click: updateNetworkInfo with the region's ping sites (since PREF1 it
// decides nothing).
func pickRegion(t *testing.T, tc *tcpClient, sites ...tdf.Pair) {
	t.Helper()
	tc.reply(ComponentUserSessions, CmdUserSessionsUpdateNetworkInfo, tc.send(ComponentUserSessions,
		CmdUserSessionsUpdateNetworkInfo, tdf.NewWriter().Map("NLMP", tdf.TypeString, tdf.TypeInt, sites...).Bytes()))
}

func naSites() []tdf.Pair {
	return []tdf.Pair{tdf.KV("eu", int64(150)), tdf.KV("eu-any", int64(200)), tdf.KV("na", int64(50)), tdf.KV("na-any", int64(100))}
}

// createInRegion creates a custom game from a client whose Preferred Region is North America (set over the channel)
// and enters its lobby's ready check.
func createInRegion(t *testing.T, ts *tcpServer, ui string) (*tcpClient, *uiClient) {
	t.Helper()
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	u := uiConnect(t, ui, a)
	u.call(CallRegionSet, `{"id":"na"}`)
	u.want(regionStateLine("na"))
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	u.call(CallLobbyEnter, "")
	return a, u
}

func (ts *tcpServer) lobbyRegions(t *testing.T) (psas, want string, notice *LobbyRegion) {
	t.Helper()
	hg := ts.s.hosted.get(1)
	if hg == nil {
		t.Fatal("no game 1")
	}
	hg.mu.Lock()
	defer hg.mu.Unlock()
	return hg.custom.region, hg.custom.want, hg.custom.notice
}

// withRegion is a lobby.state line (lobbyStateLine's) with region instead of the local one.
func withRegion(line, region string) string {
	return strings.Replace(line, `"region":`+localRegionJSON, `"region":`+region, 1)
}

// naLauncher is na1's host: it reports each game it got.
func naLauncher() (*gmshost.FakeLauncher, chan gmshost.CreateGame) {
	naHost := make(chan gmshost.CreateGame, 4)
	return &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
		gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
			naHost <- cg
			<-p.Done()
			return nil
		})
	}}, naHost
}

// A custom game whose creator prefers North America runs on na1: the browser's Region is na, the lobby names it.
func TestCustomGameInRegion(t *testing.T) {
	na, naHost := naLauncher()
	ts, _, ui := regionServer(t, na, gmshost.NewPool(GMSPort+20, GMSPort+20), nil)
	a, u := createInRegion(t, ts, ui)
	select {
	case <-naHost:
	case <-time.After(5 * time.Second):
		t.Fatal("no host on na1")
	}
	u.wantRegionState(withRegion(lobbyStateLine(a.name), regionJSON("na")))
	if psas, want, notice := ts.lobbyRegions(t); psas != "na" || want != "na" || notice == nil || notice.Why != "" {
		t.Errorf("PSAS %q, want %q, notice %+v", psas, want, notice)
	}
	lg, ok := ts.s.hosted.get(1).listed()
	if !ok || lg.Region != "NA (Virginia)" { // PREF3: the name, which the client shows as it is
		t.Errorf("browser row %+v (%v)", lg.Region, ok)
	}
	if log := ts.log.String(); !strings.Contains(log, "game 1: region na (the player's Preferred Region)") {
		t.Errorf("log lacks the region:\n%s", log)
	}
}

// Each reason North America can't take the game: it runs in Europe, the browser says eu, and lobby.state carries the
// notice (the server's log too).
func TestCustomGameRegionFallback(t *testing.T) {
	full := gmshost.NewPool(GMSPort+20, GMSPort+20)
	full.Quarantine(GMSPort+20, time.Hour, time.Hour)
	ok := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) { gmshost.PlayHost(env, p, nil) }}
	for _, c := range []struct {
		why  string
		na   gmshost.Launcher
		pool *gmshost.Pool
		set  func(s *Server)
	}{
		{gmshost.FellBackDown, nil, nil, nil},
		{gmshost.FellBackVersion, nil, nil, func(s *Server) { s.Hosts.MachineRefused("na1", gmshost.FellBackVersion) }},
		{gmshost.FellBackMap, ok, gmshost.NewPool(GMSPort+20, GMSPort+20),
			func(s *Server) { s.Hosts.SetMachineMaps("na1", []string{"MM_Map_v3"}) }},
		{gmshost.FellBackFull, ok, full, nil},
		{gmshost.FellBackFailed, &gmshost.FakeLauncher{Err: errors.New("no host copy")}, gmshost.NewPool(GMSPort+20, GMSPort+20), nil},
	} {
		t.Run(c.why, func(t *testing.T) {
			ts, _, ui := regionServer(t, c.na, c.pool, c.set)
			a, u := createInRegion(t, ts, ui)
			region := `{"id":"eu","name":"Europe","want":"na","wantName":"NA (Virginia)","got":"eu","why":"` + c.why + `"}`
			u.wantRegionState(withRegion(lobbyStateLine(a.name), region))
			if psas, w, _ := ts.lobbyRegions(t); psas != "eu" || w != "na" {
				t.Errorf("PSAS %q, want %q", psas, w)
			}
			if log := ts.log.String(); !strings.Contains(log, "game 1: wanted na, runs on local (eu): "+c.why) {
				t.Errorf("log lacks the fallback:\n%s", log)
			}
		})
	}
}

// Every kind of game a player makes runs in the Preferred Region (was TestRegionsOnlyForCustomGames: before PREF1 only
// a custom game looked at regions): a practice group's game (its creator's party's vote: the creator alone here) and
// practice on na1; EA's own region pick (NLMP) decides nothing, so a creator who clicked North America there but
// prefers Europe plays at home without a notice.
func TestRegionsForEveryGame(t *testing.T) {
	g := golden.Load(t)
	for _, c := range []struct {
		what   string
		req    func() []byte
		prefer string
		sites  []tdf.Pair
	}{
		{"practice group", func() []byte { return practiceGroupRequest(t, g) }, "na", nil},
		{"practice", func() []byte { return createRequest(t, g, "1292", "") }, "na", nil},
		{"custom game in Europe, NLMP North America", func() []byte {
			return golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))
		}, "eu", naSites()},
		{"practice, no preference, NLMP North America", func() []byte { return createRequest(t, g, "1292", "") }, "", naSites()},
	} {
		t.Run(c.what, func(t *testing.T) {
			na, naHost := naLauncher()
			ts, host, _ := regionServer(t, na, gmshost.NewPool(GMSPort+20, GMSPort+20), nil)
			a := ts.client("alpha@example.com")
			if c.prefer != "" {
				prefer(t, ts, a, c.prefer)
			}
			if c.sites != nil {
				pickRegion(t, a, c.sites...)
			}
			wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, c.req()))
			wantNA := c.prefer == "na"
			select {
			case <-naHost:
				if !wantNA {
					t.Error("a host on na1")
				}
			case <-host.created:
				if wantNA {
					t.Error("a host at home")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no host")
			}
			a.note(ComponentGameManager, NotifyGameSetupID)
			if hg := ts.s.hosted.get(1); hg.custom != nil {
				want := "eu"
				if wantNA {
					want = "na"
				}
				if psas, _, notice := ts.lobbyRegions(t); notice == nil || notice.ID != want || notice.Why != "" || psas != want {
					t.Errorf("PSAS %q, notice %+v", psas, notice)
				}
			}
			if log := ts.log.String(); wantNA != strings.Contains(log, ": region na (") {
				t.Errorf("the region's log line (want na %v):\n%s", wantNA, log)
			}
		})
	}
}

// A practice group's game is made at its creator's create: the creator and the members of the creator's PvP party
// vote (2 NA + the creator's EU: NA).
func TestPracticeGroupPartyVote(t *testing.T) {
	g := golden.Load(t)
	na, naHost := naLauncher()
	ts, host, _ := regionServer(t, na, gmshost.NewPool(GMSPort+20, GMSPort+20), nil)
	a, b, c := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com")
	prefer(t, ts, a, "eu")
	prefer(t, ts, b, "na")
	prefer(t, ts, c, "na")
	pg := ts.s.groups.createOrJoin(PlaygroupInfo{Name: "party", MemberLimit: 5}, false, PlaygroupMember{BlazeID: a.id,
		AccountID: accountOf(t, ts.s, a.id)}, nil)
	for _, m := range []*tcpClient{b, c} {
		ts.s.groups.invite(pg.info.ID, a.id, []int64{m.id})
		if res := ts.s.groups.join(pg.info.ID, "", PlaygroupMember{BlazeID: m.id, AccountID: accountOf(t, ts.s, m.id)}, nil); res.err != 0 {
			t.Fatalf("join: %#x", res.err)
		}
	}
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, practiceGroupRequest(t, g)))
	select {
	case <-naHost:
	case <-host.created:
		t.Error("a host at home")
	case <-time.After(5 * time.Second):
		t.Fatal("no host")
	}
	if log := ts.log.String(); !strings.Contains(log, "game 1 (a practice group): region na (the players' vote: na 2, eu 1)") {
		t.Errorf("log lacks the vote:\n%s", log)
	}
}

// region.set: the id must be one of the server's regions; a new one is stored, survives a new connection (the greeting
// says it) and reaches every connection; a refused call sends the state back; the same id sends nothing;
// saveUserSettings can't write it.
func TestRegionSet(t *testing.T) {
	ts, _, ui := regionServer(t, nil, nil, nil)
	a := ts.client("alpha@example.com")
	u1 := uiConnect(t, ui, a)
	if u1.greeting != regionStateLine("eu") {
		t.Errorf("greeting %s", u1.greeting)
	}
	u2 := uiConnect(t, ui, a)
	for _, bad := range []string{`{}`, `{"id":7}`, `{"id":"ap"}`, `{"id":"EU"}`, `{"id":"local"}`, `[]`} {
		u1.call(CallRegionSet, bad)
		u1.want(regionStateLine("eu"))
		u2.want(regionStateLine("eu")) // every connection of the player hears the state
	}
	u1.call(CallRegionSet, `{"id":"na"}`)
	u1.want(regionStateLine("na"))
	u2.want(regionStateLine("na"))
	u1.call(CallRegionSet, `{"id":"na"}`)
	u1.quiet()
	if acct, _ := ts.s.Store.Account(accountOf(t, ts.s, a.id)); acct.Settings[SettingPreferredRegion] != "na" {
		t.Errorf("stored %v", acct.Settings)
	}
	if u3 := uiConnect(t, ui, a); u3.greeting != regionStateLine("na") {
		t.Errorf("a new connection's greeting %s", u3.greeting)
	}
	// EA's saveUserSettings stores what the client saves, but not our key
	payload := tdf.NewWriter().Map("USET", tdf.TypeString, tdf.TypeString, tdf.KV(SettingPreferredRegion, "eu"),
		tdf.KV(SettingRegionID, RegionID("eu"))).Bytes()
	a.reply(ComponentBattlegroundsAccounts, CmdBGASaveUserSettings, a.send(ComponentBattlegroundsAccounts, CmdBGASaveUserSettings, payload))
	if acct, _ := ts.s.Store.Account(accountOf(t, ts.s, a.id)); acct.Settings[SettingPreferredRegion] != "na" || acct.Settings[SettingRegionID] != RegionID("eu") {
		t.Errorf("after saveUserSettings: %v", acct.Settings)
	}
	if log := ts.log.String(); !strings.Contains(log, "region: player "+itoa(a.id)+" prefers na (was none)") ||
		!strings.Contains(log, "region.set: an id that is no region of this server") {
		t.Errorf("log:\n%s", log)
	}
	// a stored region the server no longer has counts as home
	prefer(t, ts, a, "ap")
	if u4 := uiConnect(t, ui, a); u4.greeting != regionStateLine("eu") {
		t.Errorf("an unknown stored region: %s", u4.greeting)
	}
}

// A server without regions offers one, its home local (the name from --region-name local=..., else "Local"); a
// server with regions names each, a region without a name by its alias in capitals.
func TestRegionStateNames(t *testing.T) {
	ts, _ := newCustomHostServer(t, nil)
	ui := attachUI(t, ts)
	a := ts.client("alpha@example.com")
	if u := uiConnect(t, ui, a); u.greeting != `{"event":"region.state","data":{"regions":[{"id":"local","name":"Local"}],"home":"local","selected":"local"}}` {
		t.Errorf("no regions: %s", u.greeting)
	}
	ts.s.Hosts.SetRegionNames(map[string]string{"local": "Our LAN"})
	if u := uiConnect(t, ui, a); !strings.Contains(u.greeting, `[{"id":"local","name":"Our LAN"}]`) {
		t.Errorf("a named local region: %s", u.greeting)
	}
	ts.s.Hosts.SetRegions("eu", map[string]string{"na1": "na", "ap1": "ap"})
	ts.s.Hosts.SetRegionNames(map[string]string{"na": "NA (Virginia) ✓"})
	want := `{"event":"region.state","data":{"regions":[{"id":"eu","name":"EU"},{"id":"ap","name":"AP"},` +
		`{"id":"na","name":"NA (Virginia) \u2713"}],"home":"eu","selected":"eu"}}`
	if u := uiConnect(t, ui, a); u.greeting != want {
		t.Errorf("names\n got %s\nwant %s", u.greeting, want)
	}
}

// mmRegionServer is regionServer with a matchmaker of teamSize a team that starts at once with minPlayers.
func mmRegionServer(t *testing.T, teamSize, minPlayers int, set func(s *Server)) (*tcpServer, *customHost, chan gmshost.CreateGame) {
	na, naHost := naLauncher()
	ts, host, _ := regionServer(t, na, gmshost.NewPool(GMSPort+20, GMSPort+21), func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: teamSize, MinPlayers: minPlayers})
		s.MMBots = true
		if set != nil {
			set(s)
		}
	})
	return ts, host, naHost
}

func queue(t *testing.T, tc *tcpClient) {
	t.Helper()
	id := tc.send(ComponentGameManager, CmdGameManagerStartMatchmaking,
		startMatchmakingRequestAttrs(0, tdf.KV("GameFlags", "1"), tdf.KV("GameType", "1")))
	if f := tc.reply(ComponentGameManager, CmdGameManagerStartMatchmaking, id); f.Type != fire.Reply {
		t.Fatalf("startMatchmaking %s", f.Type)
	}
}

// wherePlayed waits for the match's host: true on na1, false at home.
func wherePlayed(t *testing.T, host *customHost, naHost chan gmshost.CreateGame) bool {
	t.Helper()
	select {
	case <-naHost:
		return true
	case <-host.created:
		return false
	case <-time.After(5 * time.Second):
		t.Fatal("no host")
		return false
	}
}

// PvP: one queue (players of different preferences meet), the region by the humans' vote: NA + NA on na1, NA + EU a
// tie (home); a lone NA player with bots filling the match plays on na1 (bots don't vote).
func TestPvPRegionVote(t *testing.T) {
	for _, c := range []struct {
		what  string
		prefs []string
		size  int
		na    bool
		log   string
	}{
		{"NA + NA", []string{"na", "na"}, 1, true, "game 1: region na (the players' vote: na 2)"},
		{"NA + EU: a tie, one queue", []string{"na", "eu"}, 1, false, "game 1: region eu (the players' vote: eu 1, na 1)"},
		{"NA alone, bots fill", []string{"na"}, 5, true, "game 1: region na (the player's Preferred Region)"},
	} {
		t.Run(c.what, func(t *testing.T) {
			ts, host, naHost := mmRegionServer(t, c.size, len(c.prefs), nil)
			for i, p := range c.prefs {
				tc := ts.client([]string{"alpha", "bravo"}[i] + "@example.com")
				prefer(t, ts, tc, p)
				queue(t, tc)
			}
			if got := wherePlayed(t, host, naHost); got != c.na {
				t.Errorf("on na1 %v, want %v", got, c.na)
			}
			if log := ts.log.String(); !strings.Contains(log, c.log) {
				t.Errorf("log lacks %q:\n%s", c.log, log)
			}
		})
	}
}

// With host machines behind match gateways (RegionRoute set), a player whose client can't be sent there (a direct
// client here) makes the vote's region home, for everyone.
func TestPvPRegionUnreachable(t *testing.T) {
	ts, host, naHost := mmRegionServer(t, 1, 2, func(s *Server) {
		s.RegionRoute = func(Game, int64) (tunnel.Away, bool) { return tunnel.Away{}, false }
	})
	for _, n := range []string{"alpha", "bravo"} {
		tc := ts.client(n + "@example.com")
		prefer(t, ts, tc, "na")
		queue(t, tc)
	}
	if wherePlayed(t, host, naHost) {
		t.Error("the match ran on na1")
	}
	if log := ts.log.String(); !strings.Contains(log, "game 1: region na (the players' vote: na 2) ignored: player ") ||
		!strings.Contains(log, "the game stays in eu") {
		t.Errorf("log:\n%s", log)
	}
}

// PREF4: solo practice and a practice group's game of a player whose client can't take the away route (here a direct
// client; live, a tunnel session without away, whose hello is version 3: tunnel TestHelloSaysAway) stay home, as a
// custom game (TestRegionNeedsTheTunnel) and PvP (TestPvPRegionUnreachable) do.
func TestPracticeRegionUnreachable(t *testing.T) {
	g := golden.Load(t)
	for _, c := range []struct {
		what string
		req  func() []byte
	}{
		{"practice", func() []byte { return createRequest(t, g, "1292", "") }},
		{"practice group", func() []byte { return practiceGroupRequest(t, g) }},
	} {
		t.Run(c.what, func(t *testing.T) {
			na, naHost := naLauncher()
			ts, host, _ := regionServer(t, na, gmshost.NewPool(GMSPort+20, GMSPort+20), func(s *Server) {
				s.RegionRoute = func(Game, int64) (tunnel.Away, bool) { return tunnel.Away{}, false }
			})
			a := ts.client("alpha@example.com")
			prefer(t, ts, a, "na")
			wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, c.req()))
			if wherePlayed(t, host, naHost) {
				t.Error("the game ran on na1")
			}
			if log := ts.log.String(); !strings.Contains(log, "region na (the player's Preferred Region) ignored: player ") ||
				!strings.Contains(log, "the game stays in eu") {
				t.Errorf("log:\n%s", log)
			}
		})
	}
}

// wantRegionState reads u's events up to the next lobby.state and checks it against line without its rules part: since
// M26 a lobby with a match host sends lobby.rulesCatalog first and the state carries the rules (M27-MERGE26), which the
// region tests don't look at, but it must be there.
func (u *uiClient) wantRegionState(line string) {
	u.t.Helper()
	got := u.event(EventLobbyState)
	var g, w map[string]any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		u.t.Fatalf("lobby.state %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(line), &w); err != nil {
		u.t.Fatalf("want %s: %v", line, err)
	}
	data, _ := g["data"].(map[string]any)
	if _, ok := data["rules"]; !ok {
		u.t.Errorf("lobby.state without rules: %s", got)
	}
	delete(data, "rules")
	if !reflect.DeepEqual(g, w) {
		u.t.Errorf("got  %s\nwant %s", got, line)
	}
}
