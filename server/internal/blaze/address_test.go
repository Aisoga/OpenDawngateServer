package blaze

import (
	"encoding/hex"
	"strings"
	"testing"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// Roadmap A5: every address the Blaze server hands out is Server.Host (dgserver --public-host), and the
// client's own addresses echoed in NotifyGameSetup's PNET are forced to loopback only while that host is
// loopback. The golden *_lan payloads are tdf_golden.py's A5 cases (public host 192.168.1.20).

const lanHost = "192.168.1.20" // tdf_golden.py A5_PUBLIC

var (
	lanGMS    = Addr{IP: 0xC0A80114, Port: GMSPort} // 192.168.1.20:10042
	clientLAN = Addr{IP: 0x0A000002, Port: 3659}    // creategame_request's INIP (dummy LAN address)
	loop3659  = Addr{IP: 0x7F000001, Port: 3659}
)

func TestA5RepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	acct, _ := testAccount()
	game, player := testGame(), testPlayer()
	game.GMS, player.Int = lanGMS, clientLAN
	for _, c := range []struct {
		name, class string
		got         []byte
	}{
		{"preauth_reply_lan", "Blaze::Util::PreAuthResponse", PreAuthReply(lanHost, 17502, Service)},
		{"postauth_reply_lan", "Blaze::Util::PostAuthResponse", PostAuthReply(lanHost, 9988, acct.UserID)},
		{"gettelemetryserver_reply_lan", "Blaze::Util::GetTelemetryServerResponse", TelemetryServerReply(lanHost, 9988)},
		{"notifygamesetup_notification_lan", "Blaze::GameManager::NotifyGameSetup", GameSetupNotification(game, player)},
	} {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
		tdfcheck.Require(t, c.class, c.got)
	}
}

func TestClientAddr(t *testing.T) {
	lan := Addr{IP: 0xC0A8011E, Port: 3659} // 192.168.1.30
	for _, c := range []struct {
		host string
		in   Addr
		want Addr
	}{
		{"127.0.0.1", lan, loop3659},                                                // the default: forced, as since B8
		{"127.0.0.1", clientLAN, loop3659},                                          // ...
		{"127.0.0.2", lan, Addr{IP: 0x7F000002, Port: 3659}},                        // A6: lane b's own loopback address
		{"127.0.0.2", loop3659, loop3659},                                           // a loopback address stays as it is
		{"127.0.0.1", Addr{IP: 0x7F000005, Port: 1}, Addr{IP: 0x7F000005, Port: 1}}, // loopback stays as it is
		{lanHost, lan, lan},                                                         // a public host: echoed as sent
		{lanHost, loop3659, loop3659},                                               // (also a loopback EXIP the client reported)
		{"10.1.2.3", clientLAN, clientLAN},                                          // any non-loopback host
		{"", lan, loop3659},                                                         // no usable host: the safe side
		{"dgserver.lan", lan, loop3659},                                             // (main resolves names to an address first)
	} {
		s := &Server{Host: c.host}
		if got := s.ClientAddr(c.in); got != c.want {
			t.Errorf("host %q: ClientAddr(%s) = %s, want %s", c.host, c.in, got, c.want)
		}
	}
}

// The B5/B8 flow with a public host: preAuth's ping site, postAuth's and getTelemetryServer's telemetry
// server and NotifyGameSetup's GMS address name it, and PNET echoes the client's own addresses (its LAN
// address 10.0.0.2 is no longer replaced by 127.0.0.1).
func TestPracticeGamePublicHost(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) { s.Host = lanHost })
	ps.send(9, 7, 0, nil)
	ps.expect(9, 7, fire.Reply, 0, "preauth_reply_lan")
	ps.send(1, 0x28, 1, ps.request("login_request"))
	ps.expect(1, 0x28, fire.Reply, 1, "login_reply")
	ps.send(1, 0x6E, 2, ps.request("loginpersona_request"))
	ps.expect(1, 0x6E, fire.Reply, 2, "loginpersona_reply")
	ps.next("UserAuthenticated")
	ps.next("UserSessionExtendedDataUpdate")
	ps.send(9, 8, 3, nil)
	ps.expect(9, 8, fire.Reply, 3, "postauth_reply_lan")
	ps.send(9, 5, 4, tdf.NewWriter().String("CMAC", "$000000000000").String("SNAM", "").Bytes())
	ps.expect(9, 5, fire.Reply, 4, "gettelemetryserver_reply_lan")
	ps.send(4, 0x19, 5, ps.request("creategame_request"))
	ps.expect(4, 0x19, fire.Reply, 5, "resetdedicatedserver_reply")
	ps.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_notification_lan")
	log := ps.close()
	if want := "[blaze]    game 1 \"player's Game\": GMS 192.168.1.20:10042 (HNET), player 2000000001\n"; !strings.Contains(log, want) {
		t.Errorf("log lacks %q:\n%s", want, log)
	}
	// the payloads above are pinned byte for byte; no logged reply names the loopback host
	if i := strings.Index(log, "-> preAuth reply"); i < 0 || strings.Contains(log[i:], "127.0.0.1") {
		t.Errorf("a reply names 127.0.0.1:\n%s", log)
	}
}

// A match host (C1) with a public host: the host binds DGGMS_BIND (the manager's BindHost, dgserver
// --bind) and NotifyGameSetup names the public host with the host's port. The two are set apart here
// to show which one goes where.
func TestHostedGamePublicHost(t *testing.T) {
	hs := newHostedSessionAt(t, lanHost, "10.1.2.3", nil, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		return h.Send(gmshost.FinishGame{WinnerTeam: 1, DurationMs: 1000, Reason: "test"})
	})
	hs.login()
	hs.send(4, 0x19, 2, hs.request("creategame_request"))
	hs.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	hs.hostGot()
	hs.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification_lan")
	hs.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(1, GameStatePostGame))
	hs.expectNote(NotifyPlayerRemovedID, PlayerRemovedNotification(1, store.FirstPersonaID, RemovedGameEnded))
	hs.waitIdle()
	hs.close()
	envs := hs.l.Envs()
	if len(envs) != 1 || envs[0].Bind != "10.1.2.3:10042" || !strings.HasPrefix(envs[0].Control, "127.0.0.1:") {
		t.Errorf("host environment %+v (want DGGMS_BIND 10.1.2.3:10042, the control channel on loopback)", envs)
	}
	log := hs.log.String()
	for _, want := range []string{
		"GMS 10.1.2.3:10042\n", // [host] match 1: started pid ... GMS <DGGMS_BIND>
		"[blaze]    game 1: GMS 192.168.1.20:10042 (HNET) ready ",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}
