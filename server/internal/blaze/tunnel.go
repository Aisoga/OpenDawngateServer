package blaze

import (
	"net"
	"net/netip"
	"slices"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/tunnel"
)

// a client that comes through the tunnel (dgserver --tunnel) has its player copy patched for a
// loopback address, the tunnel address, where Start-Client's side of the tunnel listens on every port the server
// uses. Every address Blaze hands that client must be the tunnel address, as for a local client (the local profile):
// the QoS ping site (preAuth), the telemetry server (postAuth, getTelemetryServer) and the GMS in HNET; the port stays
// the server's, since the tunnel listens on the same port numbers. The gateway routes the session's datagrams for a
// GMS port to the game's real GMS address (Conn.ForClient), so a match on an agent machine (H2) is reached too.

// TunnelAddr is the connection's tunnel address (its LocalAddr through the gateway), nil for a direct client.
func (c *Conn) TunnelAddr() *tunnel.Addr {
	if c == nil {
		return nil
	}
	return c.tun
}

// Host is the server address this connection's client is told: its tunnel address when it came through the tunnel,
// else def (Server.Host).
func (c *Conn) Host(def string) string {
	if t := c.TunnelAddr(); t != nil {
		return t.IP.String()
	}
	return def
}

// ForClient returns g as this connection's client must see it when it joins: for a tunnelled client the GMS address on
// its tunnel address with the same port, after telling the gateway where that port's datagrams go (the real GMS
// address). Without such a route a session reaches no match host: only the paths that really put a player into a game
// call this. Whoever only looks at a game gets addressedForClient.
func (c *Conn) ForClient(g Game) Game {
	t := c.TunnelAddr()
	if t == nil || g.GMS.IP == 0 {
		return g
	}
	ip := net.IPv4(byte(g.GMS.IP>>24), byte(g.GMS.IP>>16), byte(g.GMS.IP>>8), byte(g.GMS.IP)).To4()
	t.Route(int(g.GMS.Port), &net.UDPAddr{IP: ip, Port: int(g.GMS.Port)})
	return c.addressedForClient(g)
}

// addressedForClient is ForClient's address without its route: a game in a list or a lookup.
func (c *Conn) addressedForClient(g Game) Game {
	t := c.TunnelAddr()
	if t == nil || g.GMS.IP == 0 {
		return g
	}
	if a, ok := ParseAddr(t.IP.String(), int(g.GMS.Port)); ok {
		g.GMS = a
	}
	return g
}

// verifyHello answers a match host's verifyHello: may the connection from addr claim user? RakNet has no password and a
// HelloReq only claims an id, so the answer comes from where the datagrams came from. A flow of the tunnel gateway names
// its session, and the claim must be one of that session's logins (several: the test lanes' two clients behind one
// tunnel). Anything else is a direct connection, whose claim must be a direct login from the same IP, all loopback
// addresses counting as one: the weaker path, which a neighbour behind the same NAT address passes, and the reason a
// public server has no such logins (tunnel = on, --plain off).
func (s *Server) verifyHello(mt *gmshost.Match, v gmshost.VerifyHello) bool {
	if s.NoHelloCheck {
		return true
	}
	ap, err := netip.ParseAddrPort(v.Addr)
	if err != nil {
		s.Log.Logf("blaze", "   match %d: verifyHello: bad address %q; refused", mt.ID, v.Addr)
		return false
	}
	var users []int64
	how := "direct logins"
	id, flow := uint64(0), false
	if s.TunnelFlow != nil {
		id, flow = s.TunnelFlow(ap)
	}
	if flow {
		users, how = s.msgs.usersOfTunnelSession(id), "tunnel session's logins"
	} else {
		users = s.msgs.usersOfDirectAddr(ap.Addr().Unmap())
	}
	if !slices.Contains(users, v.User) {
		s.Log.Logf("blaze", "   match %d: verifyHello: %s claims player %d, not among its %s %v; refused", mt.ID, v.Addr, v.User, how, users)
		return false
	}
	return true
}

// gmsHost is the host of the GMS address a new game gets: GMSHost (the match hosts' own address, dgserver
// --gms-bind) or Host.
func (s *Server) gmsHost() string {
	if s.GMSHost != "" {
		return s.GMSHost
	}
	return s.Host
}

// forClientListed is the address ForClient gives, for a game browser list.
func (c *Conn) forClientListed(games []ListedGame) []ListedGame {
	if c.TunnelAddr() == nil {
		return games
	}
	out := make([]ListedGame, len(games))
	for i, lg := range games {
		lg.Game = c.addressedForClient(lg.Game) // no route: a list joins nothing
		out[i] = lg
	}
	return out
}
