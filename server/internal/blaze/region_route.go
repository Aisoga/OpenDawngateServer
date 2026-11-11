package blaze

import (
	"slices"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/tunnel"
)

// REGION5: a game on a host machine in another region is played through that machine's match gateway.
// When a tunnelled player is put into such a game (ForClient), the server mints a ticket for the game's match and the
// player (RegionRoute, set by dgserver) and sends the player's Start-Client away there (tunnel.Addr.RouteAway): it opens
// a second session to that gateway for the game's GMS port. The host there asks verifyHello as everywhere; the machine's
// relay annotates the question with its match gateway's session (gmshost.FlowAnnotator), and the claim must be one of
// that session's tickets' users for this match (awayHello).

// routeAway is ForClient's part for a game on a host machine with a match gateway: true when ForClient sets no Route,
// either because the player's Start-Client was sent there, or because it couldn't be (an earlier release, its control
// connection down): a machine with a match gateway is reached only through it, never by a Route to the address it
// offered (its hosts listen where nothing else reaches, and that address is the machine's word alone). False for
// every other game, whose datagrams go through this gateway as before.
func (s *Server) routeAway(c *Conn, t *tunnel.Addr, g Game) bool {
	if g.Site == "" || g.Site == gmshost.LocalSite || g.Match == 0 {
		return false
	}
	gated := s.gatedSite(g.Site)
	if s.RegionRoute == nil && !gated {
		return false
	}
	var user int64
	if sess := c.Session; sess != nil {
		user = sess.Persona.ID
	}
	port := int(g.GMS.Port)
	if user != 0 && s.RegionRoute != nil {
		if away, ok := s.RegionRoute(g, user); ok {
			if t.RouteAway(port, away) {
				s.Log.Logf("blaze", "   game %d: player %d sent to host machine %s's match gateway %s for GMS port %d (match %d)",
					g.ID, user, g.Site, away.Gateway, port, g.Match)
				return true
			}
			if gated {
				s.Log.Logf("blaze", "   game %d: player %d's Start-Client took no away message for host machine %s (an earlier "+
					"release, or its control connection is down); no route for GMS port %d (the machine is reached only "+
					"through its match gateway)", g.ID, user, g.Site, port)
				return true
			}
			s.Log.Logf("blaze", "   game %d: player %d's Start-Client took no away message for host machine %s (an earlier release, "+
				"or its control connection is down); GMS port %d goes through this gateway", g.ID, user, g.Site, port)
			return false
		}
	}
	if gated {
		s.Log.Logf("blaze", "   game %d: player %d gets no ticket for host machine %s; no route for GMS port %d (the machine is "+
			"reached only through its match gateway)", g.ID, user, g.Site, port)
		return true
	}
	return false
}

// gatedSite reports whether site is a TLS-linked host machine that offered a match gateway.
func (s *Server) gatedSite(site string) bool {
	if s.Hosts == nil {
		return false
	}
	info, linked := s.Hosts.Machine(site)
	return linked && info.Gateway != ""
}

// awayHello answers verifyHello for a match on a host machine with a match gateway (away true; false for every other
// match): the relay's annotation must name this match and the claim among its users. Its players reach the match only
// through that gateway (the hosts there bind an address nothing else reaches), so there is no other way in.
func (s *Server) awayHello(mt *gmshost.Match, v gmshost.VerifyHello) (ok, away bool) {
	if s.Hosts == nil {
		return false, false
	}
	site, _, _, _ := mt.Placement()
	if site == gmshost.LocalSite || site == "" {
		return false, false
	}
	info, linked := s.Hosts.Machine(site)
	if !linked || info.Gateway == "" {
		return false, false
	}
	switch f := v.Flow; {
	case f == nil:
		s.Log.Logf("blaze", "   match %d: verifyHello: %s claims player %d, no flow of %s's match gateway; refused", mt.ID, v.Addr, v.User, site)
	case f.Match != mt.ID:
		s.Log.Logf("blaze", "   match %d: verifyHello: %s claims player %d with a ticket for match %d; refused", mt.ID, v.Addr, v.User, f.Match)
	case !slices.Contains(f.Users, v.User):
		s.Log.Logf("blaze", "   match %d: verifyHello: %s claims player %d, not among its tickets' users %v at %s; refused", mt.ID, v.Addr,
			v.User, f.Users, site)
	default:
		return true, true
	}
	return false, true
}
