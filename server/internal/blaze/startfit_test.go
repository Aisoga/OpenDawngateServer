package blaze

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/rules"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// lineFits checks that msg's control line is at most limit bytes, logs its size and returns it (CTLLINE: the lines a
// custom game's host must get).
func lineFits(t *testing.T, what string, msg gmshost.Message, limit int) int {
	t.Helper()
	line, err := gmshost.Encode(msg)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	n := len(line) - 1
	t.Logf("%s line: %d bytes (limit %d)", what, n, limit)
	if n > limit {
		t.Errorf("%s line: %d bytes, over %d", what, n, limit)
	}
	return n
}

// ownEverything gives a everything in c: every item, a saved skin for every shaper that has another skin, and
// DefaultPerkPages perk pages, each with every perk shape and a gem of the socket's colour in every socket.
func ownEverything(t *testing.T, c *content.Content, a *store.Account) {
	t.Helper()
	items, avatar, err := c.Grant(content.OwnAll)
	if err != nil {
		t.Fatal(err)
	}
	a.Items, a.Avatar, a.Granted = items, avatar, true
	a.Settings = map[string]string{}
	byClass := map[string]content.Item{}
	for _, it := range c.Items {
		if it.ItemType == content.ItemShaper && it.Asset != "" {
			byClass[it.Shaper] = it
		}
	}
	for _, it := range c.Items {
		if sh, ok := byClass[it.Shaper]; ok && it.ItemType == content.ItemShaperSkin && it.Asset != sh.Asset {
			a.Settings[fmt.Sprintf("SKIN_0x%x", sh.CatalogID)] = strconv.FormatUint(uint64(it.CatalogID), 10)
		}
	}
	gems := map[string]uint32{} // colour -> a gem of it
	var shapes []content.Perk
	var anyGem uint32
	for _, p := range c.Perks {
		switch p.Kind {
		case content.KindGem:
			if gems[p.Category] == 0 {
				gems[p.Category] = p.CatalogID
			}
			if anyGem == 0 {
				anyGem = p.CatalogID
			}
		case content.KindShape:
			shapes = append(shapes, p)
		}
	}
	a.PerkPages = nil
	for i := range int(DefaultPerkPages) {
		page := store.PerkPage{ID: uint32(i + 1), Name: fmt.Sprintf("Page %d", i+1), Version: 9, Status: 1}
		for k, sh := range shapes {
			ps := store.PerkShape{ID: sh.CatalogID, Cell: uint8(k % 16), Rotation: uint8(k % 4)}
			for si, sock := range sh.Sockets {
				g := gems[sock]
				if g == 0 {
					g = anyGem
				}
				ps.Gems = append(ps.Gems, store.PerkGem{ID: g, Socket: uint8(si)})
			}
			page.Shapes = append(page.Shapes, ps)
		}
		a.PerkPages = append(a.PerkPages, page)
	}
}

// CTLLINE: a full lobby's lines through the real path (lobbyRoster, lobbyContent/ShaperContent, applyPickRules): ten
// players who own everything in the largest content the tests load (every perk page and saved skin filled) and five
// spectators. createGame (a warm host's carries the whole lobby), updateRoster and startGame stay within a quarter of
// MaxLine. (On 2026-11-17 such a startGame came to about 66 KB with the real content, over the 64 KiB of the host then.)
func TestFullLobbyLinesFit(t *testing.T) {
	c := loadFixtureContent(t)
	timers, _ := ParseShaperTimers("30000,30000,15000,5000")
	s := &Server{Content: c, Store: store.New(), Log: newTestLog(t), Policy: DefaultPolicy(),
		ShaperSelect: ShaperSelect{On: true, Timers: timers}}
	var players []*hostedPlayer
	for i := range 15 {
		acct, err := s.Store.Create(store.NewAccount{Name: fmt.Sprintf("full%02d@example.com", i)}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Store.Update(acct.UserID, func(a *store.Account) { ownEverything(t, c, a) }); err != nil {
			t.Fatal(err)
		}
		team := int64(i % 2)
		if i >= 10 {
			team = SpectatorTeam
		}
		players = append(players, &hostedPlayer{p: Player{ID: 3000000001 + int64(i), Name: fmt.Sprintf("Longest_Name_%03d", i),
			Team: team}, userID: acct.UserID, slot: int64(i)})
	}
	roster := lobbyRoster(players, nil)
	s.lobbyContent(roster, players)
	if len(roster) != 15 {
		t.Fatalf("roster %s", describeRoster(roster))
	}
	for _, e := range roster {
		full := len(e.Shapers) > 0 && len(e.PerkPages) == int(DefaultPerkPages) && len(e.SavedSkins) > 0 &&
			len(e.Wards) > 0 && len(e.Announcers) > 0
		if full == e.Spectator {
			t.Fatalf("%d (spectator %v): %d shapers, %d pages, %d saved skins", e.User, e.Spectator, len(e.Shapers),
				len(e.PerkPages), len(e.SavedSkins))
		}
	}
	fill := 5
	cg := gmshost.CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: gmshost.ModeCustom, Seed: 7919, Roster: roster,
		Timers: timers, Descriptors: 2, ShaperSelect: true}
	sg := gmshost.StartGame{Roster: roster, Rules: &gmshost.Rules{Pick: "blind", ShaperPool: "all", Shop: "normal",
		ItemPool: "all"}, FillTeams: &fill}
	s.applyPickRules(1, &sg, nil)
	limit := gmshost.MaxLine / 4
	lineFits(t, "createGame", cg, limit)
	lineFits(t, "updateRoster", gmshost.UpdateRoster{Roster: roster}, limit)
	n := lineFits(t, "startGame", sg, limit)
	per, _ := json.Marshal(roster[0])
	t.Logf("one player's entry: %d bytes; the startGame is %.1f%% of MaxLine", len(per), 100*float64(n)/gmshost.MaxLine)
}

// inflateEntry makes tc's roster entry about 40 KB: 1,600 perk pages (more than the page limit allows, but the entry is
// what counts), so two such players are over a 64 KiB line and one is not.
func inflateEntry(t *testing.T, ts *tcpServer, tc *tcpClient) {
	t.Helper()
	acct, _, ok := ts.s.Store.PersonaByID(tc.id)
	if !ok {
		t.Fatalf("%s: no account", tc.name)
	}
	if _, err := ts.s.Store.Update(acct.UserID, func(a *store.Account) {
		a.PerkPages = nil
		for i := range 1600 {
			a.PerkPages = append(a.PerkPages, store.PerkPage{ID: uint32(i + 1), Name: "p", Version: 1, Status: 1})
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// wantLargeFinding reads u's lobby.startRefused and checks its one finding: CTLLINE's host finding for a 64 KiB host.
func wantLargeFinding(t *testing.T, u *uiClient) {
	t.Helper()
	line := u.event(EventLobbyStartRefused)
	var got struct {
		Data struct {
			Findings []rules.Finding `json:"findings"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil || len(got.Data.Findings) != 1 {
		t.Fatalf("lobby.startRefused %s (%v)", line, err)
	}
	f := got.Data.Findings[0]
	if f.Category != rules.FindingHost || !strings.HasPrefix(f.Message, "The game can't start: its player data is too large "+
		"for the match host (") || !strings.HasSuffix(f.Message, " of 65536 bytes). Please report this.") {
		t.Errorf("finding %+v", f)
	}
}

// CTLLINE: a host without longlines (an older build) and a lobby whose roster is over its 64 KiB: the admin's START
// fails with ErrInvalidGameStateAction and lobby.startRefused carries the host finding; no startGame (and no
// updateRoster that long) reaches the host, the game stays in its lobby; a second START after a player left starts it.
func TestCustomGameStartTooLarge(t *testing.T) {
	ts, host, addr := rulesHostServer(t, []string{gmshost.FeatureRules}, func(s *Server) {
		s.Content = loadFixtureContent(t)
	})
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	inflateEntry(t, ts, a)
	inflateEntry(t, ts, b)
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	if cg := <-host.created; len(cg.Roster) != 1 || len(cg.Roster[0].PerkPages) != 1600 {
		t.Fatalf("createGame: %d entries", len(cg.Roster))
	}
	a.note(ComponentGameManager, NotifyGameSetupID)
	ua := uiConnect(t, addr, a)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	start := golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))
	wantError(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	wantLargeFinding(t, ua)
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	started, state := hg.custom.started, hg.state
	hg.mu.Unlock()
	if started || state != GameStatePreGame {
		t.Errorf("after the refusal: started %v, state %#x", started, state)
	}
	// nothing that long reached the host: no updateRoster with B, no startGame
	for done := false; !done; {
		select {
		case m := <-host.msgs:
			switch v := m.(type) {
			case gmshost.StartGame:
				t.Fatal("a startGame reached the host")
			case gmshost.UpdateRoster:
				t.Errorf("an updateRoster of %d entries reached the host", len(v.Roster))
			}
		case <-time.After(200 * time.Millisecond):
			done = true
		}
	}
	log := ts.log.String()
	for _, want := range []string{"(updateRoster not sent: ", " bytes, over the host's line limit of 65536 bytes)",
		"advanceGameState IN_GAME refused: its startGame is "} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
	// B leaves: the roster fits again, and START starts the game
	leave := tdf.NewWriter().Int("CNTX", 0).Int("GID", 1).Int("PID", b.id).Int("REAS", 6).Bytes()
	wantReply(t, "B leaves", b.gm(CmdGameManagerRemovePlayer, leave))
	if ur := hostNext[gmshost.UpdateRoster](t, host); len(ur.Roster) != 1 {
		t.Errorf("updateRoster after the leave: %s", describeRoster(ur.Roster))
	}
	wantReply(t, "A starts again", a.gm(CmdGameManagerAdvanceGameState, start))
	if sg := hostNext[gmshost.StartGame](t, host); len(sg.Roster) != 1 || len(sg.Roster[0].PerkPages) != 1600 {
		t.Errorf("startGame: %s", describeRoster(sg.Roster))
	}
}
