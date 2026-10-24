package blaze

import (
	"encoding/hex"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/tdf/tdfcheck"
	"dawngate/server/internal/tunnel"
)

// trustedIP and untrustedIP are the map check tests' two client addresses: the first trusted by --trust-maps, the
// second not (a copy that may lack MM_Map_v3's files).
const (
	trustedIP   = "127.0.0.1"
	untrustedIP = "127.0.0.2"
)

func trustFirst() MapTrust {
	return MapTrust{Prefixes: []netip.Prefix{netip.MustParsePrefix(trustedIP + "/32")}}
}

func TestParseMapTrust(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", "none"},
		{"none", "none"},
		{" All ", "all"},
		{"127.0.0.3", "127.0.0.3"},
		{"127.0.0.3, 192.168.1.7/24,::ffff:10.0.0.1", "127.0.0.3, 192.168.1.0/24, 10.0.0.1"},
	} {
		m, err := ParseMapTrust(c.in)
		if err != nil || m.String() != c.want {
			t.Errorf("ParseMapTrust(%q) = %q, %v; want %q", c.in, m, err, c.want)
		}
	}
	for _, bad := range []string{"yes", "127.0.0.300", "10.0.0.0/40"} {
		if _, err := ParseMapTrust(bad); err == nil || !strings.Contains(err.Error(), "--trust-maps") {
			t.Errorf("ParseMapTrust(%q): %v", bad, err)
		}
	}
	m, _ := ParseMapTrust("127.0.0.3,192.168.1.0/24")
	for a, want := range map[string]bool{"127.0.0.3": true, "127.0.0.4": false, "192.168.1.200": true, "::ffff:127.0.0.3": true} {
		if got := m.trusts(netip.MustParseAddr(a)); got != want {
			t.Errorf("trusts(%s) = %v", a, got)
		}
	}
	if (MapTrust{}).trusts(netip.MustParseAddr("127.0.0.1")) || !(MapTrust{All: true}).trusts(netip.Addr{}) {
		t.Error("none trusts or all doesn't")
	}
}

// The map check's message (a broadcast of our own: id 101, a chat line and a notification) matches tdf_golden.py's
// writer; the join's error reply is the frame joingame_mapcheck_reply (fire's frame tests).
func TestMapCheckMessageMatchesGolden(t *testing.T) {
	g := golden.Load(t)
	s := &Server{Release: "rel-A"}
	mc := s.canLoad(&Conn{}, "MM_Map_v3")
	if mc.ok {
		t.Fatal("a client without the tunnel and no --trust-maps may play MM_Map_v3")
	}
	got := SystemMessageNotification(1, MapNoteMessage(s.mapRefusedText("MM_Map_v3", `"[Pass] Custom 1"`, mc)), testTime)
	tdfcheck.Require(t, "Blaze::Messaging::ServerMessage", got)
	if h, want := hex.EncodeToString(got), g.WriterCase(t, "notifymessage_mapcheck_notification").Hex; h != want {
		t.Errorf("got %s\nwant %s", h, want)
	}
	gf := g.Frame(t, "joingame_mapcheck_reply")
	b, err := fire.Encode(fire.Frame{Component: ComponentGameManager, Command: CmdGameManagerJoinGame, Error: ErrMapFilesMissing,
		Type: fire.ErrorReply, ID: gf.ID})
	if err != nil || hex.EncodeToString(b) != gf.Hex {
		t.Errorf("the error reply %x (%v)\nwant %s", b, err, gf.Hex)
	}
}

// canLoad: SM_Map_v5 always; MM_Map_v3 for a client without the tunnel only when --trust-maps names it.
func TestMapCheckDirect(t *testing.T) {
	s := &Server{}
	for _, lv := range []string{"", HostedLevel, "sm_map_v5"} {
		if mc := s.canLoad(&Conn{}, lv); !mc.ok || mc.why != "" {
			t.Errorf("level %q: %+v", lv, mc)
		}
	}
	if mc := s.canLoad(&Conn{}, "MM_Map_v3"); mc.ok || !strings.Contains(mc.why, "a client without the tunnel, which --trust-maps (none) doesn't name") ||
		!strings.Contains(mc.tell, "without the tunnel") {
		t.Errorf("untrusted: %+v", mc)
	}
	s.MapTrust = MapTrust{All: true}
	if mc := s.canLoad(&Conn{}, "MM_Map_v3"); !mc.ok || !strings.Contains(mc.why, "trusted by --trust-maps") {
		t.Errorf("--trust-maps all: %+v", mc)
	}
}

// canLoad through the tunnel: a Start-Client of the server's release may play MM_Map_v3, whatever --trust-maps says;
// one let in by --allow-any-client (another release, or none named) may not.
func TestMapCheckTunnel(t *testing.T) {
	s := &Server{MapTrust: MapTrust{All: true}}
	lo := net.ParseIP("127.0.0.1").To4()
	same := &Conn{tun: tunnel.FakeSessionAddr(lo, 10041, "rel-A", true)}
	if mc := s.canLoad(same, "MM_Map_v3"); !mc.ok || mc.why != "its Start-Client is this server's release rel-A" {
		t.Errorf("the same release: %+v", mc)
	}
	other := &Conn{tun: tunnel.FakeSessionAddr(lo, 10041, "rel-B", false)}
	if mc := s.canLoad(other, "MM_Map_v3"); mc.ok || !strings.Contains(mc.why, "release rel-B, not this server's (let in by --allow-any-client)") ||
		mc.tell != "your Start-Client is release rel-B, not this server's" {
		t.Errorf("another release: %+v", mc)
	}
	old := &Conn{tun: tunnel.FakeSessionAddr(lo, 10041, "", false)}
	if mc := s.canLoad(old, "MM_Map_v3"); mc.ok || !strings.Contains(mc.why, "names no release") {
		t.Errorf("no release named: %+v", mc)
	}
	if mc := s.canLoad(other, HostedLevel); !mc.ok {
		t.Errorf("SM_Map_v5 refused: %+v", mc)
	}
}

// mapNote waits for the map check's message (skipping the login's other system messages) and returns its text.
func (tc *tcpClient) mapNote() string {
	tc.t.Helper()
	for {
		tag, attrs := tc.systemNote()
		if tag == SystemTagBroadcast && attrs[BroadcastAttrID] == "101" {
			if attrs[BroadcastAttrFlags] != "3" || attrs[BroadcastAttrSeconds] != "600" {
				tc.t.Errorf("%s: the map note %v", tc.name, attrs)
			}
			return attrs[BroadcastAttrText]
		}
	}
}

// MAPGUARD: a custom game on MM_Map_v3 (a name tag): a trusted client joins; one the server can't tell has the files
// is refused (JOIN and SPECTATE) with ErrMapFilesMissing and told why in a system message, numbered when repeated;
// the log says why. The game's own players are unaffected.
func TestMapCheckCustomJoin(t *testing.T) {
	g := golden.Load(t)
	ts, host := newCustomHostServer(t, func(s *Server) {
		s.CustomLevels, s.MapTrust, s.Release = []string{"MM_Map_v3"}, trustFirst(), "rel-A"
	})
	a := ts.clientFrom("alpha@example.com", trustedIP)
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, createRequest(t, g, "2", "MP1~#mm")))
	if cg := <-host.created; cg.Level != "MM_Map_v3" {
		t.Fatalf("createGame level %q", cg.Level)
	}
	a.note(ComponentGameManager, NotifyGameSetupID)
	b := ts.clientFrom("bravo@example.com", untrustedIP)
	wantError(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))), ErrMapFilesMissing)
	text := b.mapNote()
	if want := `You can't join "[Pass] MP1": it plays on the map Anshavar Pass, and your copy of Dawngate may not have that ` +
		"map's files (you are connected without the tunnel, so the server can't check your copy). Close the game and " +
		"start it again with Start-Client.exe of this server's release (rel-A), which adds them."; text != want {
		t.Errorf("the message\n got %s\nwant %s", text, want)
	}
	wantError(t, "B spectates", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))), ErrMapFilesMissing)
	if text := b.mapNote(); !strings.HasSuffix(text, "which adds them. (2)") {
		t.Errorf("the repeated message %q", text)
	}
	b.none(ComponentGameManager, NotifyGameSetupID, 50*time.Millisecond)
	a.none(ComponentGameManager, NotifyPlayerJoiningID, 0)
	c := ts.clientFrom("charlie@example.com", trustedIP)
	wantReply(t, "C joins", c.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	c.note(ComponentGameManager, NotifyGameSetupID)
	log := ts.log.String()
	for _, want := range []string{
		"game 1 plays on MM_Map_v3: its creator 2000000001 may play it (map check: a client without the tunnel from 127.0.0.1, trusted by --trust-maps)",
		"joinGame: game 1: player 2000000002 refused: the game plays on MM_Map_v3 and a client without the tunnel from 127.0.0.2, " +
			"which --trust-maps (127.0.0.1) doesn't name, so the server can't tell whether its copy has zz_MM_Map_v3.snap (map check); error reply 0x0131",
		"joinGame: game 1 plays on MM_Map_v3: player 2000000003 may join (map check: a client without the tunnel from 127.0.0.1, trusted by --trust-maps)",
		"game 1: player 2000000003 joins",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
}

// A game the player creates on a map its copy may lack plays on SM_Map_v5 instead (a failed create shows the client
// nothing): practice on an MM server, and a custom game (the server's map or a tag); the player is told.
func TestMapCheckCreateFallsBack(t *testing.T) {
	g := golden.Load(t)
	for _, tc := range []struct {
		what, flags, name, mode, gnam string
	}{
		{"practice", "1292", "", gmshost.ModePractice, ""},
		{"custom game", "2", "Friday", gmshost.ModeCustom, "Friday"},
		{"custom game with the tag", "2", "Friday~#mm", gmshost.ModeCustom, "Friday"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			ts, host := newCustomHostServer(t, func(s *Server) {
				s.Level, s.CustomLevels, s.MapTrust = "MM_Map_v3", []string{"MM_Map_v3"}, trustFirst()
			})
			b := ts.clientFrom("bravo@example.com", untrustedIP)
			wantReply(t, "create", b.gm(CmdGameManagerResetDedicatedServer, createRequest(t, g, tc.flags, tc.name)))
			cg := <-host.created
			if cg.Level != HostedLevel || cg.Mode != tc.mode {
				t.Errorf("createGame level %q mode %q, want %s %s", cg.Level, cg.Mode, HostedLevel, tc.mode)
			}
			if text := b.mapNote(); !strings.HasPrefix(text, "Your game plays on The Dawngate instead of Anshavar Pass: your copy of Dawngate may not have Anshavar Pass's files") {
				t.Errorf("the message %q", text)
			}
			if tc.gnam != "" {
				game, _ := decode(t, b.note(ComponentGameManager, NotifyGameSetupID)).Sub("GAME")
				if name, _ := game.Str("GNAM"); name != tc.gnam {
					t.Errorf("GNAM %q, want %q (no [Pass] label)", name, tc.gnam)
				}
				if v := attrValue(stringMap(game, "ATTR"), AttrLevelID); v != "2490188286" {
					t.Errorf("LevelId %q", v)
				}
			}
			if log := ts.log.String(); !strings.Contains(log, "game 1: SM_Map_v5 instead of MM_Map_v3: its creator 2000000001 may lack the map's files, a client without the tunnel from 127.0.0.2") {
				t.Errorf("the log lacks the fallback:\n%s", log)
			}
		})
	}
}

// The PvP queue on an MM server: a party with a member the server can't tell has the files is refused (the client's
// pop-up), the member and the leader are told; a trusted player alone is queued.
func TestMapCheckQueue(t *testing.T) {
	g := golden.Load(t)
	ts := newTCPServer(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 5, MinPlayers: 10, FillAfter: time.Hour})
		s.Level, s.MapTrust = "MM_Map_v3", trustFirst()
	})
	a := ts.clientFrom("alpha@example.com", trustedIP)
	b := ts.clientFrom("bravo@example.com", untrustedIP)
	if f := a.reply(6, 1, a.send(6, 1, golden.Bytes(t, decodeCase(t, g, "createplaygroup_party_request")))); f.Type != fire.Reply {
		t.Fatalf("party: %s %#x", f.Type, f.Error)
	}
	if f := b.reply(6, 3, b.send(6, 3, golden.Bytes(t, decodeCase(t, g, "joinplaygroup_request")))); f.Type != fire.Reply {
		t.Fatalf("join party: %s %#x", f.Type, f.Error)
	}
	f := a.reply(ComponentGameManager, CmdGameManagerStartMatchmaking,
		a.send(ComponentGameManager, CmdGameManagerStartMatchmaking, golden.Bytes(t, decodeCase(t, g, "startmatchmaking_party_request"))))
	wantError(t, "the party's queue", f, ErrMapFilesMissing)
	if text := b.mapNote(); !strings.HasPrefix(text, "alpha's party can't queue with you: the PvP queue plays on the map Anshavar Pass") {
		t.Errorf("the member's message %q", text)
	}
	if text := a.mapNote(); !strings.HasPrefix(text, "The PvP queue plays on the map Anshavar Pass, whose files the copies of bravo may not have") {
		t.Errorf("the leader's message %q", text)
	}
	if ts.s.MM.Len() != 0 {
		t.Errorf("%d sessions queued", ts.s.MM.Len())
	}
	// alone, the trusted leader's own queue is taken; bravo's alone is refused
	a.queue(60000)
	wantError(t, "bravo alone", b.reply(ComponentGameManager, CmdGameManagerStartMatchmaking,
		b.send(ComponentGameManager, CmdGameManagerStartMatchmaking, startMatchmakingRequest(60000))), ErrMapFilesMissing)
	if text := b.mapNote(); !strings.HasPrefix(text, "You can't queue: the PvP queue plays on the map Anshavar Pass") {
		t.Errorf("bravo's message %q", text)
	}
	log := ts.log.String()
	for _, want := range []string{
		"matchmaking: player 2000000001 may play MM_Map_v3 (map check: a client without the tunnel from 127.0.0.1, trusted by --trust-maps)",
		"matchmaking: player 2000000002 can't play MM_Map_v3: a client without the tunnel from 127.0.0.2",
		"matchmaking for 2000000001 refused: the queue plays on MM_Map_v3, and bravo may lack its files (map check); error reply 0x0131",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
}
