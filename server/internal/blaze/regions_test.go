package blaze

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/tdf"
)

// M27: custom games in their creator's region (regions.go). The servers here are Europe's (home eu) with a North
// American machine na1 (gmshost.Manager.AddTestMachine: a stand-in for a registered host machine).

func TestRegionID(t *testing.T) {
	for alias, want := range map[string]string{"eu": "1819748219", "EU": "1819748219", "na": "1668749442"} {
		if got := RegionID(alias); got != want {
			t.Errorf("RegionID(%q) = %s, want %s", alias, got, want)
		}
	}
}

// The rule's three sources: NLMP's choice ("-any" cut), else the stored setting, else home; a region the server lacks
// counts as none.
func TestWantedRegion(t *testing.T) {
	regions := []string{"eu", "na"}
	naID := map[string]string{SettingRegionID: RegionID("na")}
	for _, c := range []struct {
		what     string
		sites    map[string]int64
		settings map[string]string
		want     string
	}{
		{"NLMP na", map[string]int64{"na": 50, "na-any": 100, "eu": 150, "eu-any": 200}, nil, "na"},
		{"NLMP Europe, any region", map[string]int64{"eu-any": 50, "na": 150}, naID, "eu"},
		{"the login's NLMP (na lowest)", map[string]int64{"eu": 150, "eu-any": 200, "na": 50, "na-any": 100}, nil, "na"},
		{"QoS's NLMP only: the setting", map[string]int64{"local": 2}, naID, "na"},
		{"no NLMP: the setting", nil, naID, "na"},
		{"neither: home", nil, map[string]string{"Audio.Master": "0.5"}, "eu"},
		{"a region the server lacks", map[string]int64{"ap": 50}, nil, "eu"},
		{"an unknown setting", nil, map[string]string{SettingRegionID: "12345"}, "eu"},
	} {
		if got := WantedRegion(c.sites, c.settings, "eu", regions); got != c.want {
			t.Errorf("%s: %q, want %q", c.what, got, c.want)
		}
	}
}

func TestRegionNotice(t *testing.T) {
	if n := regionNotice("na", "na", ""); n != nil {
		t.Errorf("a game where it was wanted: %+v", n)
	}
	if n := regionNotice("eu", "", ""); n != nil {
		t.Errorf("a game without a wish: %+v", n)
	}
	b, _ := json.Marshal(LobbyState{Players: []LobbyStatePlayer{{Name: "a"}}, Region: regionNotice("eu", "na", "full")})
	if want := `{"players":[{"name":"a"}],"region":{"want":"na","got":"eu","why":"full"}}`; string(b) != want {
		t.Errorf("lobby.state %s\nwant %s", b, want)
	}
}

// getUserSettings: an account that never chose a region gets the home region's id (tdf_golden.py's
// region3_getusersettings_default_reply); a stored choice and regions off stay as they are.
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
}

// regionServer is a custom-game server of region eu whose machine na1 (if na is not nil) runs North America's hosts.
func regionServer(t *testing.T, na gmshost.Launcher, naPool *gmshost.Pool, set func(m *gmshost.Manager)) (*tcpServer, *customHost, string) {
	ts, host := newCustomHostServer(t, func(s *Server) {
		s.Hosts.SetRegions("eu", map[string]string{"na1": "na"})
		if na != nil {
			s.Hosts.AddTestMachine("na1", na, naPool, "")
		}
		if set != nil {
			set(s.Hosts)
		}
	})
	return ts, host, attachUI(t, ts)
}

// pickRegion sends the play screen's region click: updateNetworkInfo with the region's ping sites.
func pickRegion(t *testing.T, tc *tcpClient, sites ...tdf.Pair) {
	t.Helper()
	tc.reply(ComponentUserSessions, CmdUserSessionsUpdateNetworkInfo, tc.send(ComponentUserSessions,
		CmdUserSessionsUpdateNetworkInfo, tdf.NewWriter().Map("NLMP", tdf.TypeString, tdf.TypeInt, sites...).Bytes()))
}

func naSites() []tdf.Pair {
	return []tdf.Pair{tdf.KV("eu", int64(150)), tdf.KV("eu-any", int64(200)), tdf.KV("na", int64(50)), tdf.KV("na-any", int64(100))}
}

// createInRegion creates a custom game from a client that picked North America and enters its lobby's ready check.
func createInRegion(t *testing.T, ts *tcpServer, ui string) (*tcpClient, *uiClient) {
	t.Helper()
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	pickRegion(t, a, naSites()...)
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	u := uiConnect(t, ui, a)
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

// A custom game whose creator picked North America runs on na1: the browser's Region is na, the lobby has no notice.
func TestCustomGameInRegion(t *testing.T) {
	naHost := make(chan gmshost.CreateGame, 1)
	na := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
		gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
			naHost <- cg
			<-p.Done()
			return nil
		})
	}}
	ts, _, ui := regionServer(t, na, gmshost.NewPool(GMSPort+20, GMSPort+20), nil)
	a, u := createInRegion(t, ts, ui)
	select {
	case <-naHost:
	case <-time.After(5 * time.Second):
		t.Fatal("no host on na1")
	}
	u.wantRegionState(lobbyStateLine(a.name))
	if psas, want, notice := ts.lobbyRegions(t); psas != "na" || want != "na" || notice != nil {
		t.Errorf("PSAS %q, want %q, notice %+v", psas, want, notice)
	}
	lg, ok := ts.s.hosted.get(1).listed()
	if !ok || lg.Region != "na" {
		t.Errorf("browser row %+v (%v)", lg.Region, ok)
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
		set  func(m *gmshost.Manager)
	}{
		{gmshost.FellBackDown, nil, nil, nil},
		{gmshost.FellBackVersion, nil, nil, func(m *gmshost.Manager) { m.MachineRefused("na1", gmshost.FellBackVersion) }},
		{gmshost.FellBackMap, ok, gmshost.NewPool(GMSPort+20, GMSPort+20),
			func(m *gmshost.Manager) { m.SetMachineMaps("na1", []string{"MM_Map_v3"}) }},
		{gmshost.FellBackFull, ok, full, nil},
		{gmshost.FellBackFailed, &gmshost.FakeLauncher{Err: errors.New("no host copy")}, gmshost.NewPool(GMSPort+20, GMSPort+20), nil},
	} {
		t.Run(c.why, func(t *testing.T) {
			ts, _, ui := regionServer(t, c.na, c.pool, c.set)
			a, u := createInRegion(t, ts, ui)
			want := strings.TrimSuffix(lobbyStateLine(a.name), "}}") + `,"region":{"want":"na","got":"eu","why":"` + c.why + `"}}}`
			u.wantRegionState(want)
			if psas, w, _ := ts.lobbyRegions(t); psas != "eu" || w != "na" {
				t.Errorf("PSAS %q, want %q", psas, w)
			}
			if log := ts.log.String(); !strings.Contains(log, "game 1: wanted na, runs on local (eu): "+c.why) {
				t.Errorf("log lacks the fallback:\n%s", log)
			}
		})
	}
}

// A practice group's game and a practice game don't look at regions, though their creator picked North America; a
// European custom game has no notice.
func TestRegionsOnlyForCustomGames(t *testing.T) {
	na := &gmshost.FakeLauncher{Err: errors.New("never used")}
	g := golden.Load(t)
	for _, c := range []struct {
		what  string
		req   func() []byte
		sites []tdf.Pair
	}{
		{"practice group", func() []byte { return practiceGroupRequest(t, g) }, naSites()},
		{"practice", func() []byte { return createRequest(t, g, "1292", "") }, naSites()},
		{"custom game in Europe", func() []byte {
			return golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))
		}, []tdf.Pair{tdf.KV("eu", int64(50)), tdf.KV("eu-any", int64(100)), tdf.KV("na", int64(150))}},
	} {
		t.Run(c.what, func(t *testing.T) {
			ts, host, _ := regionServer(t, na, gmshost.NewPool(GMSPort+20, GMSPort+20), nil)
			a := ts.client("alpha@example.com")
			pickRegion(t, a, c.sites...)
			wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, c.req()))
			<-host.created // on this machine
			a.note(ComponentGameManager, NotifyGameSetupID)
			if hg := ts.s.hosted.get(1); hg.custom != nil {
				if psas, _, notice := ts.lobbyRegions(t); notice != nil || hg.custom.browsable && psas != "eu" {
					t.Errorf("PSAS %q, notice %+v", psas, notice)
				}
			}
			if log := ts.log.String(); strings.Contains(log, "wanted na") || strings.Contains(log, "region na") {
				t.Errorf("a region was asked for:\n%s", log)
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
