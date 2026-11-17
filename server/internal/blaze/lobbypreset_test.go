package blaze

import (
	"strings"
	"testing"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/rules"
)

// RULES6: a map's suggested lobby setup, the operator's presets and default_rules.

// suggestLevel is a folder map without its own victory that suggests All Random with First Binding.
var suggestLevel = PlayableLevel{Name: "CM_Suggest", Tag: "suggest", Label: "[Suggest] ", Archive: "zz_CM_Suggest.snap",
	Display: "Suggest", MapRules: MapRules{Tuned: true, Summary: "Respawn 8 s.", Short: "Tuned"},
	LobbyPreset: "pick=random,victory=bind1"}

// operatorPresets is an operator's file with one preset.
func operatorPresets(t *testing.T) *rules.Library {
	t.Helper()
	l, problems := rules.ParsePresets([]byte("brawl | Bind Brawl | pick=random,victory=bind1\nmelee | Melee | shaperPool=melee\n"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	return l
}

// createdGame creates a custom game as alpha and returns it once its host is ready.
func createdGame(t *testing.T, ts *tcpServer, host *customHost) *hostedGame {
	t.Helper()
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	<-host.created
	a.note(ComponentGameManager, NotifyGameSetupID)
	return ts.s.hosted.get(1)
}

// gameRules is a game's rules, where they came from and its browser name.
func gameRules(hg *hostedGame) (rules.Combination, string, string) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	return hg.custom.rules, hg.custom.rulesFrom, hg.game.Name
}

func wantLog(t *testing.T, ts *tcpServer, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(ts.log.String(), line) {
			t.Errorf("log lacks %q:\n%s", line, ts.log.String())
		}
	}
}

// A map's suggestion opens the lobby with it (logged, the browser's prefix), and the admin may change it; with
// --test-rules the test aid wins.
func TestLobbySuggestion(t *testing.T) {
	withLevels(t, suggestLevel)
	ts, host, _ := rulesHostServer(t, allHostFeatures, func(s *Server) {
		s.Level, s.MapTrust = suggestLevel.Name, MapTrust{All: true}
	})
	hg := createdGame(t, ts, host)
	want, _ := rules.Parse(suggestLevel.LobbyPreset)
	c, from, name := gameRules(hg)
	if c != want.Normal() || from != `the map's suggested lobby setup "pick=random,victory=bind1"` ||
		!strings.HasPrefix(name, "[Suggest] [All Random] [Bind 1] ") {
		t.Errorf("rules %+v from %q, name %q", c, from, name)
	}
	wantLog(t, ts, `game 1: the lobby starts with the map's suggested lobby setup "pick=random,victory=bind1": All Random, First Binding`)
	// the admin changes it as any lobby's rules
	hg.mu.Lock()
	fs := hg.rulesFindingsLocked()
	hg.mu.Unlock()
	if len(fs) != 0 {
		t.Errorf("findings %+v", fs)
	}
	if err := ts.s.lobbySetRules(hg.players[0].p.ID, []byte(`{"preset":"standard"}`)); err != nil {
		t.Fatal(err)
	}
	if c, _, name := gameRules(hg); c != rules.Standard() || name != "[Suggest] Custom 1" {
		t.Errorf("after the admin's change: %+v, %q", c, name)
	}

	// --test-rules wins
	aram, _ := rules.Parse("aram")
	ts2, host2, _ := rulesHostServer(t, allHostFeatures, func(s *Server) {
		s.Level, s.MapTrust, s.TestRules = suggestLevel.Name, MapTrust{All: true}, &aram
	})
	if c, from, _ := gameRules(createdGame(t, ts2, host2)); c != aram || from != "" {
		t.Errorf("--test-rules: %+v from %q", c, from)
	}
}

// The operator's default_rules start every lobby on a map without a suggestion; the browser shows its name.
func TestLobbyDefaultRules(t *testing.T) {
	ts, host, _ := rulesHostServer(t, allHostFeatures, func(s *Server) {
		s.RulePresets, s.DefaultRules = operatorPresets(t), "brawl"
	})
	c, from, name := gameRules(createdGame(t, ts, host))
	want, _ := rules.Parse("pick=random,victory=bind1")
	if c != want || from != `the server's default_rules "brawl"` || !strings.HasPrefix(name, "[Bind Brawl] [Bind 1] ") {
		t.Errorf("rules %+v from %q, name %q", c, from, name)
	}
	// a lobby equal to an operator's preset without a win condition: its name alone
	if got := lobbyName(operatorPresets(t), "", false, false, rules.Combination{ShaperPool: "melee"}, "G"); got != "[Melee] G" {
		t.Errorf("name %q", got)
	}
}

// A suggestion the server can't use is skipped (logged): an id it has no preset of falls to default_rules; one the
// lobby's checks refuse (a win condition on a map with its own, a pool too small for the bots) to Standard.
func TestLobbySuggestionRefused(t *testing.T) {
	unknown := suggestLevel
	unknown.Name, unknown.Tag, unknown.LobbyPreset = "CM_Unknown", "unknown", "chaos"
	withLevels(t, unknown)
	ts, host, _ := rulesHostServer(t, allHostFeatures, func(s *Server) {
		s.Level, s.MapTrust, s.RulePresets, s.DefaultRules = unknown.Name, MapTrust{All: true}, operatorPresets(t), "brawl"
	})
	if _, from, _ := gameRules(createdGame(t, ts, host)); from != `the server's default_rules "brawl"` {
		t.Errorf("from %q", from)
	}
	wantLog(t, ts, `game 1: the map's suggested lobby setup "chaos" isn't used: this server has no preset of that id (Start-Server.ini's rule_presets)`)

	victory := tunedLevel
	victory.Name, victory.Tag, victory.LobbyPreset = "CM_Victory", "victory", "victory=bind1"
	withLevels(t, victory)
	ts2, host2, _ := rulesHostServer(t, allHostFeatures, func(s *Server) {
		s.Level, s.MapTrust = victory.Name, MapTrust{All: true}
	})
	if c, from, _ := gameRules(createdGame(t, ts2, host2)); c != rules.Standard() || from != "" {
		t.Errorf("a victory map: %+v from %q", c, from)
	}
	wantLog(t, ts2, `game 1: the map's suggested lobby setup "victory=bind1" (First Binding) isn't used: the lobby's checks refuse it (victory: This map has its own win condition. Choose Guardian only.)`)

	tanks := suggestLevel
	tanks.Name, tanks.Tag, tanks.LobbyPreset = "CM_Tanks", "tanks", "shaperPool=tank"
	withLevels(t, tanks)
	ts3, host3, _ := rulesHostServer(t, allHostFeatures, func(s *Server) {
		s.Level, s.MapTrust, s.CustomBots = tanks.Name, MapTrust{All: true}, true
	})
	if c, _, _ := gameRules(createdGame(t, ts3, host3)); c != rules.Standard() {
		t.Errorf("a pool too small for the bots: %+v", c)
	}
	wantLog(t, ts3, `"shaperPool=tank" (Tanks) isn't used: the lobby's checks refuse it (bots: The bots play`)
}

// A host without maprules2 can't play the suggested win condition: at gameReady the lobby goes back to Standard.
func TestLobbySuggestionOldHost(t *testing.T) {
	withLevels(t, suggestLevel)
	ts, host, _ := rulesHostServer(t, []string{gmshost.FeatureRules, gmshost.FeatureRules2, gmshost.FeatureMapRules},
		func(s *Server) { s.Level, s.MapTrust = suggestLevel.Name, MapTrust{All: true} })
	c, from, name := gameRules(createdGame(t, ts, host))
	if c != rules.Standard() || from != "" || strings.Contains(name, "[Bind 1]") {
		t.Errorf("rules %+v from %q, name %q", c, from, name)
	}
	wantLog(t, ts, `game 1: the map's suggested lobby setup "pick=random,victory=bind1" (All Random, First Binding) isn't used: the lobby's checks refuse it with this match host (victory: First Binding needs a newer match host than this game's: choose another option.); the lobby starts on Standard`)
}
