package blaze

import (
	"io"
	"net"
	"strings"
	"testing"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tunnel"
)

// TestRegionNoFallbackRoute: a game on a host machine with a match gateway is never routed to the address that machine
// offered (a compromised one could name the main server's own match hosts): when no ticket comes, or the player's
// Start-Client takes no away message, the session gets no route for the port. A game on a machine without one is
// routed as before.
func TestRegionNoFallbackRoute(t *testing.T) {
	m := gmshost.NewManager(gmshost.Config{Log: logging.New(io.Discard)})
	m.AddTestLinkedMachine("na1", "na", "198.51.100.7:42128", strings.Repeat("ab", 32), nil, gmshost.NewPool(GMSPort, GMSPort))
	m.AddTestMachine("eu2", nil, gmshost.NewPool(GMSPort+1, GMSPort+1), "127.0.1.10")
	buf := &lockedBuf{}
	s := &Server{Log: logging.New(buf), Hosts: m}
	gms, _ := ParseAddr("127.0.1.10", GMSPort) // what a compromised machine offered as its GMS host
	for _, c := range []struct {
		name  string
		route func(Game, int64) (tunnel.Away, bool)
		log   string
	}{
		{"no ticket", func(Game, int64) (tunnel.Away, bool) { return tunnel.Away{}, false }, "gets no ticket for host machine na1; no route"},
		{"no away message", func(Game, int64) (tunnel.Away, bool) { return tunnel.Away{Ticket: []byte{1}}, true },
			"took no away message for host machine na1 (an earlier release, or its control connection is down); no route"},
		{"no RegionRoute", nil, "gets no ticket for host machine na1; no route"},
	} {
		s.RegionRoute = c.route
		ta := tunnel.FakeSessionAddr(net.IPv4(127, 0, 0, 1), 1, "", true) // a session whose Start-Client takes no away message
		conn := &Conn{tun: ta, away: s.routeAway, Session: &Session{Persona: store.Persona{ID: 2000000001}}}
		conn.ForClient(Game{ID: 1, Site: "na1", Match: 5, GMS: gms})
		if r := ta.RouteOf(GMSPort); r != nil {
			t.Errorf("%s: GMS port routed to %v", c.name, r)
		}
		if !strings.Contains(buf.String(), c.log) {
			t.Errorf("%s: the log lacks %q:\n%s", c.name, c.log, buf.String())
		}
	}

	// a machine without a match gateway (H2's agents): routed as before
	s.RegionRoute = func(Game, int64) (tunnel.Away, bool) { return tunnel.Away{}, false }
	ta := tunnel.FakeSessionAddr(net.IPv4(127, 0, 0, 1), 1, "", true)
	conn := &Conn{tun: ta, away: s.routeAway, Session: &Session{Persona: store.Persona{ID: 2000000001}}}
	conn.ForClient(Game{ID: 2, Site: "eu2", Match: 6, GMS: gms})
	if r := ta.RouteOf(GMSPort); r == nil || r.String() != "127.0.1.10:10042" {
		t.Errorf("a machine without a match gateway: route %v", r)
	}
}
