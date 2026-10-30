package blaze

import (
	"encoding/hex"
	"strings"
	"testing"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/setup/localepatch"
	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

func TestLevelAssetID(t *testing.T) {
	// SM_Map_v5's is GamePrepareForStart's level of every live session and MatchOptionsUI's constant (0x013E2B7C)
	for name, want := range map[string]uint32{"SM_Map_v5": 0x946D41FE, "MM_Map_v3": 0x420DE52E, "mm_map_v3": 0x420DE52E} {
		if got := LevelAssetID(name); got != want {
			t.Errorf("LevelAssetID(%q) = %#08x, want %#08x", name, got, want)
		}
	}
}

// Only PlayableLevels pass, by name (any case, with or without ".Level") or tag; any other Level asset (TA_Map_v1
// lacks its graphs, mesh and ground) and anything that isn't a level name is refused.
func TestParseLevel(t *testing.T) {
	for in, want := range map[string]string{"MM_Map_v3": "MM_Map_v3", " MM_Map_v3.Level ": "MM_Map_v3",
		"SM_Map_v5.level": "SM_Map_v5", "mm_map_v3": "MM_Map_v3", "mm": "MM_Map_v3", "SM": "SM_Map_v5"} {
		if got, err := ParseLevel(in); err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", ".Level", "MM Map", "levels/MM_Map_v3", "MM_Map_v3.bin", string(make([]byte, 81)),
		"TA_Map_v1", "SM_Map_v4", "bots"} {
		if got, err := ParseLevel(in); err == nil {
			t.Errorf("ParseLevel(%q) = %q, want an error", in, got)
		}
	}
	if got, err := ParseLevels(" MM_Map_v3, mm ,SM_Map_v5,"); err != nil || strings.Join(got, ",") != "MM_Map_v3,SM_Map_v5" {
		t.Errorf("ParseLevels = %v, %v", got, err)
	}
	for _, in := range []string{"", "none", " None "} {
		if got, err := ParseLevels(in); err != nil || got != nil {
			t.Errorf("ParseLevels(%q) = %v, %v; want none", in, got, err)
		}
	}
	if _, err := ParseLevels("MM_Map_v3,TA_Map_v1"); err == nil {
		t.Error("ParseLevels with TA_Map_v1: no error")
	}
}

// A custom game's tags come off the end of its name in either order; the first map tag in the name wins (a typed one
// before the dialog's list's), every map tag is stripped, a "~#" that is no tag stays.
func TestCutNameTags(t *testing.T) {
	for _, tc := range []struct {
		in, rest   string
		bots       bool
		tag        string // "" = none
		wantTagged bool
	}{
		{"Friday Night", "Friday Night", false, "", false},
		{"Friday Night~#mm", "Friday Night", false, "MM_Map_v3", true},
		{"Friday Night ~#MM", "Friday Night", false, "MM_Map_v3", true},
		{"Friday Night~#mm~#bots", "Friday Night", true, "MM_Map_v3", true},
		{"Friday Night~#bots~#mm", "Friday Night", true, "MM_Map_v3", true},
		{"Friday Night~#mm~#sm", "Friday Night", false, "MM_Map_v3", true}, // typed ~#mm, the list's default ~#sm
		{"Friday Night~#sm~#mm", "Friday Night", false, "SM_Map_v5", true}, // typed ~#sm, the list's MM
		{"Friday Night~#mm~#sm~#bots", "Friday Night", true, "MM_Map_v3", true},
		{"Friday Night~#mm~#bots~#sm", "Friday Night", true, "MM_Map_v3", true},
		{"Friday Night~#MM~#mm~#sm", "Friday Night", false, "MM_Map_v3", true},
		{"~#mm", "", false, "MM_Map_v3", true},
		{"Friday~#ta", "Friday~#ta", false, "", false},
		{"Friday~#mmm", "Friday~#mmm", false, "", false},
		{"Friday Night" + BotFillMarker, "Friday Night", true, "", false},
	} {
		rest, bots, tag, tagged := cutNameTags(tc.in)
		if rest != tc.rest || bots != tc.bots || tagged != tc.wantTagged || tag.Name != tc.tag {
			t.Errorf("cutNameTags(%q) = %q, %v, %q, %v; want %q, %v, %q, %v", tc.in, rest, bots, tag.Name, tagged,
				tc.rest, tc.bots, tc.tag, tc.wantTagged)
		}
	}
}

// A custom game on MM_Map_v3 has the level's LevelId (tdf_golden.py notifygamesetup_custom_mm_notification, and the
// browser's row notifygamelistupdate_mm_notification with the map's label in its name); SM_Map_v5's leaves the
// dialog's id alone.
func TestLevelAttrsMatchGolden(t *testing.T) {
	g := golden.Load(t)
	if a := levelAttrs(""); a != nil {
		t.Errorf("no level: %v", a)
	}
	if a := levelAttrs(HostedLevel); a != nil {
		t.Errorf("SM_Map_v5: %v", a)
	}
	game := testCustomGame()
	game.Attrs = mergeAttrs(game.Attrs, levelAttrs("MM_Map_v3"))
	creator := testLobbyPlayers()[0]
	creator.State = PlayerStateActiveConnecting
	got := LobbyGameSetupNotification(game, []int64{creator.ID}, []LobbyPlayer{creator}, SetupContextCreateGame)
	tdfcheck.Require(t, "Blaze::GameManager::NotifyGameSetup", got)
	if h, want := hex.EncodeToString(got), g.WriterCase(t, "notifygamesetup_custom_mm_notification").Hex; h != want {
		t.Errorf("got %s\nwant %s", h, want)
	}
	mm, _ := FindLevel("MM_Map_v3")
	game.Name = mm.Label + game.Name
	ps := testLobbyPlayers()
	got = GameListUpdateNotification(1, []ListedGame{{Game: game, State: GameStatePreGame, Admins: []int64{ps[0].ID},
		Region: "na", Players: ps}}, nil)
	tdfcheck.Require(t, "Blaze::GameManager::NotifyGameListUpdate", got)
	if h, want := hex.EncodeToString(got), g.WriterCase(t, "notifygamelistupdate_mm_notification").Hex; h != want {
		t.Errorf("browser row: got %s\nwant %s", h, want)
	}
}

// With --map the host's createGame names the level, and the custom game's NotifyGameSetup (and so the game
// browser's row, the same attributes) carries its LevelId and its name the map's label.
func TestCustomGameHostedLevel(t *testing.T) {
	ts, host := newCustomHostServer(t, func(s *Server) { s.Level, s.MapTrust = "MM_Map_v3", MapTrust{All: true} })
	g := golden.Load(t)
	a := ts.client("alpha@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	cg := <-host.created
	if cg.Level != "MM_Map_v3" {
		t.Fatalf("createGame %+v", cg)
	}
	wantLine(t, cg, `{"t":"createGame","gameId":1,"level":"MM_Map_v3","mode":"custom","seed":7919,"roster":[{"user":2000000001,`+
		`"name":"alpha","index":0,"team":1,"noun":"","bot":false}],"descriptors":2}`)
	setup := decode(t, a.note(ComponentGameManager, NotifyGameSetupID))
	game, _ := setup.Sub("GAME")
	if v := attrValue(stringMap(game, "ATTR"), AttrLevelID); v != "1108206894" {
		t.Errorf("LevelId %q, want 1108206894 (MM_Map_v3.Level)", v)
	}
	if name, _ := game.Str("GNAM"); !strings.HasPrefix(name, "[Pass] ") {
		t.Errorf("GNAM %q, want the map's label in front", name)
	}
	if log := ts.log.String(); !strings.Contains(log, "game 1: map MM_Map_v3 (LevelId 1108206894; the server's map)") {
		t.Errorf("log lacks the map line:\n%s", log)
	}
}

// A custom game's name tag picks its map, if the server offers it (SM_Map_v5 always, the server's own map,
// --custom-maps): createGame's level, the LevelId and the label in NotifyGameSetup and the browser's row. Without
// the offer the tag goes and the server's map stays. The bot marker works beside it.
func TestCustomGameMapTag(t *testing.T) {
	g := golden.Load(t)
	for _, tc := range []struct {
		what         string
		server       string   // Server.Level
		custom       []string // Server.CustomLevels
		name         string
		level, gnam  string
		levelID, why string
		fill         int
	}{
		{"offered", "", []string{"MM_Map_v3"}, "Friday Night~#mm", "MM_Map_v3", "[Pass] Friday Night", "1108206894",
			"the name's tag ~#mm", 0},
		{"offered, with bots", "", []string{"MM_Map_v3"}, "Friday Night~#mm" + BotFillMarker, "MM_Map_v3",
			"[Pass] [Bots] Friday Night", "1108206894", "the name's tag ~#mm", 5},
		{"not offered", "", nil, "Friday Night~#mm", HostedLevel, "Friday Night", "2490188286",
			"asks for MM_Map_v3, which this server doesn't offer (--custom-maps), so the server's map", 0},
		{"SM_Map_v5 on an MM server", "MM_Map_v3", nil, "Friday Night~#sm", HostedLevel, "Friday Night", "2490188286",
			"the name's tag ~#sm", 0},
		{"no tag on an MM server", "MM_Map_v3", nil, "Friday Night", "MM_Map_v3", "[Pass] Friday Night", "1108206894",
			"the server's map", 0},
		{"no tag", "", []string{"MM_Map_v3"}, "Friday Night", HostedLevel, "Friday Night", "2490188286", "", 0},
		// a tag typed before the one the create dialog's map list appends wins (MAPPICK-label, mappick_label1c)
		{"typed ~#mm, the list's ~#sm", "", []string{"MM_Map_v3"}, "ML1~#mm~#sm", "MM_Map_v3", "[Pass] ML1",
			"1108206894", "the name's tag ~#mm", 0},
		{"typed ~#sm, the list's ~#mm", "", []string{"MM_Map_v3"}, "ML1~#sm~#mm", HostedLevel, "ML1", "2490188286",
			"the name's tag ~#sm", 0},
		{"typed ~#mm, the list's ~#sm, bots", "", []string{"MM_Map_v3"}, "ML1~#mm~#sm" + BotFillMarker, "MM_Map_v3",
			"[Pass] [Bots] ML1", "1108206894", "the name's tag ~#mm", 5},
	} {
		t.Run(tc.what, func(t *testing.T) {
			ts, host := newCustomHostServer(t, func(s *Server) { s.Level, s.CustomLevels, s.MapTrust = tc.server, tc.custom, MapTrust{All: true} })
			a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
			wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, createRequest(t, g, "2", tc.name)))
			if cg := <-host.created; cg.Level != tc.level || cg.FillTeams != tc.fill {
				t.Errorf("createGame level %q fillTeams %d, want %q %d", cg.Level, cg.FillTeams, tc.level, tc.fill)
			}
			game, _ := decode(t, a.note(ComponentGameManager, NotifyGameSetupID)).Sub("GAME")
			if name, _ := game.Str("GNAM"); name != tc.gnam {
				t.Errorf("NotifyGameSetup GNAM %q, want %q", name, tc.gnam)
			}
			if v := attrValue(stringMap(game, "ATTR"), AttrLevelID); v != tc.levelID {
				t.Errorf("NotifyGameSetup LevelId %q, want %q", v, tc.levelID)
			}
			wantReply(t, "game list", b.gm(CmdGameManagerGetGameListSubscription,
				golden.Bytes(t, decodeCase(t, g, "getgamelistsubscription_request"))))
			up := decode(t, b.note(ComponentGameManager, NotifyGameListUpdateID))
			u, _ := up.Get("UPDT")
			items := u.Value.(*tdf.List).Items
			if len(items) != 1 {
				t.Fatalf("browser: %d games", len(items))
			}
			gam, _ := items[0].(*tdf.Struct).Sub("GAM")
			if name, _ := gam.Str("GNAM"); name != tc.gnam {
				t.Errorf("browser GNAM %q, want %q", name, tc.gnam)
			}
			if v := attrValue(stringMap(gam, "ATTR"), AttrLevelID); v != tc.levelID {
				t.Errorf("browser LevelId %q, want %q", v, tc.levelID)
			}
			// HOME1: the home page's lobby, its title without the server's prefixes; a lobby, not a match
			title := strings.TrimPrefix(strings.TrimPrefix(tc.gnam, "[Pass] "), BotFillPrefix)
			if ls := ts.s.CustomLobbies(); len(ls) != 1 || ls[0].Title != title || ls[0].Level != tc.level ||
				ls[0].Bots != (tc.fill > 0) || ls[0].Started || ls[0].Private || ls[0].Players != 1 || ls[0].Spectators != 0 ||
				ls[0].Seats != 10 {
				t.Errorf("CustomLobbies %+v, want %q on %s", ls, title, tc.level)
			}
			if ms := ts.s.HostedMatches(); len(ms) != 1 || !ms[0].Lobby || ms[0].Level != tc.level || ms[0].Kind != kindCustom {
				t.Errorf("HostedMatches %+v", ms)
			}
			log := ts.log.String()
			if tc.why == "" {
				if strings.Contains(log, "game 1: map ") {
					t.Errorf("a map line for the default:\n%s", log)
				}
			} else if !strings.Contains(log, "game 1: map "+tc.level) || !strings.Contains(log, tc.why) {
				t.Errorf("log lacks the map line (%s, %s):\n%s", tc.level, tc.why, log)
			}
		})
	}
}

// The custom lobby's map line for a level the client can't name (localepatch.LobbyLines, the player copy's
// FE_CustomGameLobby 0x0f2b57c5) is the create dialog's label for that level (moviepatch.ExtraMaps): it names one map,
// so exactly one hosted level besides HostedLevel (the only one in the client's own map table) may exist. A third map
// needs another text (or a patch of the client's map table). The players' messages name it the same (Display), and
// the browser's prefix names it too: Anshavar Pass, "[Pass] ".
func TestLobbyMapLabel(t *testing.T) {
	var other []string
	var display []string
	for _, l := range PlayableLevels {
		if l.Name != HostedLevel {
			other = append(other, l.Tag)
			display = append(display, l.Display+" "+l.Label)
		}
	}
	line := localepatch.LobbyLines[localepatch.LobbyMapLine]
	if len(other) != 1 || len(moviepatch.ExtraMaps) != 1 || moviepatch.ExtraMaps[0].Tag != other[0] ||
		moviepatch.ExtraMaps[0].Label != line || len(localepatch.LobbyLines) != 1 ||
		display[0] != "Anshavar Pass [Pass] " || line != "Anshavar Pass" || LevelDisplay(HostedLevel) != "The Dawngate" {
		t.Errorf("levels besides %s %v (%q), the dialog's maps %+v, the lobby's line %q", HostedLevel, other, display,
			moviepatch.ExtraMaps, line)
	}
	// MAPTABLE: the patched client names each map by the line of its LevelId, which the setup writes for the built-in
	// maps (localepatch.BuiltInMaps) and the maps folder's: the same ids and names as here
	for _, m := range localepatch.BuiltInMaps {
		if l, ok := FindLevel(m.Name); !ok || l.Display != m.Display || localepatch.LevelID(m.Name) != LevelAssetID(l.Name) {
			t.Errorf("built-in map %+v: level %+v", m, l)
		}
	}
	if len(localepatch.BuiltInMaps) != len(PlayableLevels)-1 {
		t.Errorf("localepatch.BuiltInMaps %v, PlayableLevels %v", localepatch.BuiltInMaps, PlayableLevels)
	}
}

// withLevels adds levels for one test and takes them away after it.
func withLevels(t *testing.T, ls ...PlayableLevel) {
	t.Helper()
	orig := PlayableLevels
	t.Cleanup(func() { PlayableLevels = orig })
	if err := AddLevels(ls...); err != nil {
		t.Fatal(err)
	}
}

var ringLevel = PlayableLevel{Name: "CM_Ring", Tag: "ring", Label: "[Ring] ", Archive: "zz_CM_Ring.snap",
	Display: "Test Ring"}

// TestAddLevels: a maps folder's map is parsed by name and tag, cut from a game's name, offered by --custom-maps and
// named by its display name; a taken name or tag, a bad name or a missing field adds nothing.
func TestAddLevels(t *testing.T) {
	withLevels(t, ringLevel)
	for _, in := range []string{"CM_Ring", "cm_ring.Level", "ring"} {
		if got, err := ParseLevel(in); err != nil || got != "CM_Ring" {
			t.Errorf("ParseLevel(%q) = %q, %v", in, got, err)
		}
	}
	if rest, _, tag, ok := cutNameTags("Friday~#ring"); !ok || tag.Name != "CM_Ring" || rest != "Friday" {
		t.Errorf("cutNameTags: %q %+v %v", rest, tag, ok)
	}
	if got := OfferedMapTags("", []string{"MM_Map_v3", "CM_Ring"}); strings.Join(got, ",") != "mm,ring" {
		t.Errorf("OfferedMapTags %v", got)
	}
	if got := OfferedMapTags("CM_Ring", nil); strings.Join(got, ",") != "ring" {
		t.Errorf("OfferedMapTags for the server's own map %v", got)
	}
	if LevelDisplay("CM_Ring") != "Test Ring" {
		t.Errorf("LevelDisplay %q", LevelDisplay("CM_Ring"))
	}
	n := len(PlayableLevels)
	for _, bad := range [][]PlayableLevel{
		{{Name: "CM_Two", Tag: "two", Archive: "zz_CM_Two.snap", Display: "Two"},
			{Name: "CM_Ring", Tag: "r2", Archive: "a", Display: "d"}},
		{{Name: "CM_X", Tag: "mm", Archive: "a", Display: "d"}},
		{{Name: "CM_X", Tag: "ring", Archive: "a", Display: "d"}},
		{{Name: "CM X", Tag: "x", Archive: "a", Display: "d"}},
		{{Name: "CM_X", Tag: "x", Display: "d"}},
		{{Name: "CM_X", Tag: "sm_map_v5", Archive: "a", Display: "d"}},
	} {
		if err := AddLevels(bad...); err == nil {
			t.Errorf("AddLevels(%+v) took it", bad)
		}
		if len(PlayableLevels) != n {
			t.Fatalf("a refused AddLevels left %d levels, %d before", len(PlayableLevels), n)
		}
	}
}
