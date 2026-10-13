package blaze

import (
	"net"

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

// ForClient returns g as this connection's client must see it: for a tunnelled client the GMS address on its tunnel
// address with the same port, after telling the gateway where that port's datagrams go (the real GMS address).
func (c *Conn) ForClient(g Game) Game {
	t := c.TunnelAddr()
	if t == nil || g.GMS.IP == 0 {
		return g
	}
	ip := net.IPv4(byte(g.GMS.IP>>24), byte(g.GMS.IP>>16), byte(g.GMS.IP>>8), byte(g.GMS.IP)).To4()
	t.Route(int(g.GMS.Port), &net.UDPAddr{IP: ip, Port: int(g.GMS.Port)})
	if a, ok := ParseAddr(t.IP.String(), int(g.GMS.Port)); ok {
		g.GMS = a
	}
	return g
}

// forClientListed is ForClient for a game browser list.
func (c *Conn) forClientListed(games []ListedGame) []ListedGame {
	if c.TunnelAddr() == nil {
		return games
	}
	out := make([]ListedGame, len(games))
	for i, lg := range games {
		lg.Game = c.ForClient(lg.Game)
		out[i] = lg
	}
	return out
}
