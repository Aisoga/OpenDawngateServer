package blaze

import (
	"crypto/sha256"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/tunnel"
)

// The map check per version (MAPED-GUARD): the tests' versions of CM_Ring, the server's and another.
var (
	ringServer = tunnel.InstalledMap{Archive: "zz_CM_Ring.snap", SHA256: sha256.Sum256([]byte("the server's ring"))}
	ringOther  = tunnel.InstalledMap{Archive: "zz_CM_Ring.snap", SHA256: sha256.Sum256([]byte("another ring"))}
	mmInstall  = tunnel.InstalledMap{Archive: "zz_MM_Map_v3.snap", SHA256: sha256.Sum256([]byte("mm"))}
)

func ringVersions() map[string][32]byte {
	return map[string][32]byte{"zz_cm_ring.snap": ringServer.SHA256}
}

// thirdIP is a client address of the version tests besides trustedIP and untrustedIP.
const thirdIP = "127.0.0.3"

func TestParseClientMaps(t *testing.T) {
	h := ringServer.Hex()
	m, err := ParseClientMaps(" 127.0.0.4=zz_CM_Ring.snap:" + h + "+zz_MM_Map_v3.snap:" + mmInstall.Hex() + ", ::ffff:127.0.4.2= ,")
	if err != nil {
		t.Fatal(err)
	}
	if got := m[netip.MustParseAddr("127.0.0.4")]; len(got) != 2 || got[0] != ringServer || got[1] != mmInstall {
		t.Errorf("127.0.0.4: %+v", got)
	}
	if got, ok := m[netip.MustParseAddr("127.0.4.2")]; !ok || got == nil || len(got) != 0 {
		t.Errorf("127.0.4.2 (none): %#v, %v", got, ok)
	}
	if m, err := ParseClientMaps(""); err != nil || len(m) != 0 {
		t.Errorf("empty: %v, %v", m, err)
	}
	for _, bad := range []string{"127.0.0.4", "x=zz_A.snap:" + h, "127.0.0.4=zz_A.snap", "127.0.0.4=zz_A.snap:abc",
		"127.0.0.4=zz A.snap:" + h, "127.0.0.4=:" + h} {
		if _, err := ParseClientMaps(bad); err == nil || !strings.Contains(err.Error(), "--client-maps") {
			t.Errorf("ParseClientMaps(%q): %v", bad, err)
		}
	}
}

// canLoad of a folder map whose version the server knows: the copy's hash decides, through the tunnel (the hello's
// maps) and without it (--client-maps), whatever --trust-maps and the release say; a client that named no maps keeps
// the release rule and --trust-maps; MM_Map_v3 keeps MAPGUARD's rule; a map without a known version too.
func TestMapCheckVersion(t *testing.T) {
	withLevels(t, ringLevel)
	s := &Server{MapTrust: MapTrust{All: true}, MapVersions: ringVersions(), ClientMaps: map[netip.Addr][]tunnel.InstalledMap{
		netip.MustParseAddr(untrustedIP): {ringOther}, netip.MustParseAddr(thirdIP): {}}}
	lo := net.ParseIP("127.0.0.1").To4()
	tun := func(maps []tunnel.InstalledMap) *Conn {
		return &Conn{tun: tunnel.FakeSessionMapsAddr(lo, 10041, "rel-A", true, maps)}
	}
	short, other := ringServer.Short(), ringOther.Short()
	for _, c := range []struct {
		what      string
		conn      *Conn
		level     string
		ok        bool
		why, tell string
	}{
		{"the server's version", tun([]tunnel.InstalledMap{mmInstall, {Archive: "ZZ_cm_ring.snap", SHA256: ringServer.SHA256}}),
			"CM_Ring", true, "its copy's zz_CM_Ring.snap is the server's version " + short + " (its Start-Client's tunnel hello)", ""},
		{"another version", tun([]tunnel.InstalledMap{ringOther}), "CM_Ring", false,
			"its copy's zz_CM_Ring.snap is " + other + ", not the server's version " + short + " (its Start-Client's tunnel hello)",
			"has another version of Test Ring (yours " + other + ", this server's " + short + ")"},
		{"without it", tun([]tunnel.InstalledMap{mmInstall}), "CM_Ring", false,
			"its copy has no zz_CM_Ring.snap (its Start-Client's tunnel hello names zz_MM_Map_v3.snap " + mmInstall.Short() + ")",
			"doesn't have Test Ring"},
		{"no list named, the same release", &Conn{tun: tunnel.FakeSessionAddr(lo, 10041, "rel-A", true)}, "CM_Ring", true,
			"its Start-Client is this server's release rel-A", ""},
		{"no list named, another release", &Conn{tun: tunnel.FakeSessionAddr(lo, 10041, "rel-B", false)}, "CM_Ring", false,
			"its Start-Client is release rel-B, not this server's (let in by --allow-any-client)", "your Start-Client is release rel-B, not this server's"},
		{"MM_Map_v3 by the release", tun([]tunnel.InstalledMap{}), "MM_Map_v3", true, "its Start-Client is this server's release rel-A", ""},
	} {
		mc := s.canLoad(c.conn, c.level)
		if mc.ok != c.ok || mc.why != c.why || mc.tell != c.tell {
			t.Errorf("%s: %+v\nwant ok %v why %q tell %q", c.what, mc, c.ok, c.why, c.tell)
		}
	}
	// without the tunnel: --client-maps decides over --trust-maps all; an address it doesn't name is trusted
	direct := func(ip string) *Conn {
		return &Conn{nc: addrConn{a: &net.TCPAddr{IP: net.ParseIP(ip), Port: 50000}}}
	}
	if mc := s.canLoad(direct(untrustedIP), "CM_Ring"); mc.ok || !mc.version ||
		mc.why != "its copy's zz_CM_Ring.snap is "+other+", not the server's version "+short+" (--client-maps for 127.0.0.2)" {
		t.Errorf("--client-maps, another version: %+v", mc)
	}
	if mc := s.canLoad(direct(thirdIP), "CM_Ring"); mc.ok || mc.why != "its copy has no zz_CM_Ring.snap (--client-maps for 127.0.0.3 names none)" {
		t.Errorf("--client-maps, none: %+v", mc)
	}
	if mc := s.canLoad(direct(trustedIP), "CM_Ring"); !mc.ok || !strings.Contains(mc.why, "trusted by --trust-maps") {
		t.Errorf("not named by --client-maps: %+v", mc)
	}
	// a server that doesn't know its version (no host copy): the release rule
	s.MapVersions = nil
	if mc := s.canLoad(tun([]tunnel.InstalledMap{ringOther}), "CM_Ring"); !mc.ok || mc.why != "its Start-Client is this server's release rel-A" {
		t.Errorf("no version known: %+v", mc)
	}
}

// A custom game on CM_Ring: the creator with the server's version plays it, a player with another version is refused
// (JOIN) and told how to fix it, one without it too; one without it who creates a game on it plays SM_Map_v5.
func TestMapCheckVersionJoin(t *testing.T) {
	withLevels(t, ringLevel)
	g := golden.Load(t)
	ts, host := newCustomHostServer(t, func(s *Server) {
		s.CustomLevels, s.MapTrust, s.Release = []string{"CM_Ring"}, MapTrust{All: true}, "rel-A"
		s.MapVersions = ringVersions()
		s.ClientMaps = map[netip.Addr][]tunnel.InstalledMap{netip.MustParseAddr(trustedIP): {ringServer},
			netip.MustParseAddr(untrustedIP): {ringOther}, netip.MustParseAddr(thirdIP): {mmInstall}}
	})
	a := ts.clientFrom("alpha@example.com", trustedIP)
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, createRequest(t, g, "2", "MP1~#ring")))
	if cg := <-host.created; cg.Level != "CM_Ring" {
		t.Fatalf("createGame level %q", cg.Level)
	}
	a.note(ComponentGameManager, NotifyGameSetupID)
	b := ts.clientFrom("bravo@example.com", untrustedIP)
	wantError(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))), ErrMapFilesMissing)
	if text, want := b.mapNote(), `You can't join "[Ring] MP1": it plays on the map Test Ring, and your copy of Dawngate has `+
		"another version of Test Ring (yours "+ringOther.Short()+", this server's "+ringServer.Short()+"). Copy this server's "+
		"version of its map package into the maps folder beside Start-Client.exe, then close the game and start it again "+
		"with Start-Client.exe, which installs it."; text != want {
		t.Errorf("the message\n got %s\nwant %s", text, want)
	}
	c := ts.clientFrom("charlie@example.com", thirdIP)
	wantError(t, "C joins", c.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))), ErrMapFilesMissing)
	if text := c.mapNote(); !strings.Contains(text, "and your copy of Dawngate doesn't have Test Ring. Copy this server's version") {
		t.Errorf("C's message %q", text)
	}
	b.none(ComponentGameManager, NotifyGameSetupID, 50*time.Millisecond)
	// C's own game on the ring plays SM_Map_v5
	wantReply(t, "C creates", c.gm(CmdGameManagerResetDedicatedServer, createRequest(t, g, "2", "Mine~#ring")))
	if cg := <-host.created; cg.Level != HostedLevel {
		t.Errorf("C's game: createGame level %q", cg.Level)
	}
	if text := c.mapNote(); !strings.HasPrefix(text, "Your game plays on The Dawngate instead of Test Ring: your copy of "+
		"Dawngate doesn't have Test Ring. Copy this server's version of the map package into the maps folder") {
		t.Errorf("C's fallback message %q", text)
	}
	log := ts.log.String()
	for _, want := range []string{
		"game 1 plays on CM_Ring: its creator 2000000001 may play it (map check: its copy's zz_CM_Ring.snap is the server's version " +
			ringServer.Short() + " (--client-maps for 127.0.0.1))",
		"joinGame: game 1: player 2000000002 refused: the game plays on CM_Ring and its copy's zz_CM_Ring.snap is " +
			ringOther.Short() + ", not the server's version " + ringServer.Short() + " (--client-maps for 127.0.0.2) (map check); error reply 0x0131",
		"joinGame: game 1: player 2000000003 refused: the game plays on CM_Ring and its copy has no zz_CM_Ring.snap",
		"game 2: SM_Map_v5 instead of CM_Ring: its creator 2000000003 may lack the map's files, its copy has no zz_CM_Ring.snap",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
}

// The PvP queue on a CM_Ring server: a party with a member of another version is refused, the leader told which
// copies lack the server's version; the leader alone is queued.
func TestMapCheckVersionQueue(t *testing.T) {
	withLevels(t, ringLevel)
	g := golden.Load(t)
	ts := newTCPServer(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 5, MinPlayers: 10, FillAfter: time.Hour})
		s.Level, s.MapTrust, s.MapVersions = "CM_Ring", MapTrust{All: true}, ringVersions()
		s.ClientMaps = map[netip.Addr][]tunnel.InstalledMap{netip.MustParseAddr(trustedIP): {ringServer},
			netip.MustParseAddr(untrustedIP): {ringOther}}
	})
	a := ts.clientFrom("alpha@example.com", trustedIP)
	b := ts.clientFrom("bravo@example.com", untrustedIP)
	if f := a.reply(6, 1, a.send(6, 1, golden.Bytes(t, decodeCase(t, g, "createplaygroup_party_request")))); f.Error != 0 {
		t.Fatalf("party: %#x", f.Error)
	}
	if f := b.reply(6, 3, b.send(6, 3, golden.Bytes(t, decodeCase(t, g, "joinplaygroup_request")))); f.Error != 0 {
		t.Fatalf("join party: %#x", f.Error)
	}
	f := a.reply(ComponentGameManager, CmdGameManagerStartMatchmaking,
		a.send(ComponentGameManager, CmdGameManagerStartMatchmaking, golden.Bytes(t, decodeCase(t, g, "startmatchmaking_party_request"))))
	wantError(t, "the party's queue", f, ErrMapFilesMissing)
	if text := b.mapNote(); !strings.HasPrefix(text, "alpha's party can't queue with you: the PvP queue plays on the map "+
		"Test Ring, and your copy of Dawngate has another version of Test Ring") {
		t.Errorf("the member's message %q", text)
	}
	if text := a.mapNote(); text != "The PvP queue plays on the map Test Ring, which the copies of bravo don't have in "+
		"this server's version; they copy its map package into the maps folder beside Start-Client.exe and start the game again." {
		t.Errorf("the leader's message %q", text)
	}
	if ts.s.MM.Len() != 0 {
		t.Errorf("%d sessions queued", ts.s.MM.Len())
	}
	a.queue(60000)
	if log := ts.log.String(); !strings.Contains(log, "matchmaking for 2000000001 refused: the queue plays on CM_Ring, and bravo may lack its files (map check)") {
		t.Errorf("the log:\n%s", log)
	}
}

// addrConn is a connection that only has a remote address (canLoad's direct path).
type addrConn struct {
	net.Conn
	a net.Addr
}

func (c addrConn) RemoteAddr() net.Addr { return c.a }
