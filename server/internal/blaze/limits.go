package blaze

import (
	"time"

	"dawngate/server/internal/limits"
)

// The limits (docs/reference.md *Limits*): Server.Limits (nil = off) wraps three handlers here, so the handlers
// themselves stay as they are: Authentication::login (attempts per address, the backoff per address and per
// account), GameManager::resetDedicatedServer (games a user starts, and is in at once) and startMatchmaking (queues
// a user joins). ServeConn applies the per-connection request rate (requestGate).

// errLimitServerBusy is ERR_SERVER_BUSY (0x4072), a refused login's error: the client's LOGIN ERROR panel shows
// "*** [40720000]" (no text of its own; the added locale package gives it one) and keeps the connection, the name
// and an empty password field (seen in a test run).
const errLimitServerBusy = 0x4072

// applyLimits wraps the limited handlers (NewServer, after every component registered).
func applyLimits(s *Server) {
	s.wrap(ComponentAuthentication, CmdAuthLogin, limitLogin)
	s.wrap(ComponentGameManager, CmdGameManagerResetDedicatedServer, limitGameStart)
	s.wrap(ComponentGameManager, CmdGameManagerStartMatchmaking, limitQueue)
}

// wrap replaces a registered handler's function with mw(it).
func (s *Server) wrap(component, command uint16, mw func(func(*Request) []byte) func(*Request) []byte) {
	k := key{component, command}
	if h, ok := s.handlers[k]; ok {
		h.Fn = mw(h.Fn)
		s.handlers[k] = h
	}
}

// remoteIP is the key of the connection's client address in the limits: Addr, so a tunnelled client counts with the
// player's real address as the gateway saw it (tunnel.Addr.Peer), not the loopback address every tunnelled
// game socket has; "" without a network connection, as in tests.
func (c *Conn) remoteIP() string {
	if c == nil {
		return ""
	}
	if a := c.Addr(); a.IsValid() {
		return a.String()
	}
	return ""
}

// limitLogin: a login over its address's rate, or in an address's or account's backoff, is answered with
// ERR_SERVER_BUSY before the login handler sees it; the others go through, and their result (an error reply = a
// failure, e.g. the wrong password; a reply = a success) feeds the backoff.
func limitLogin(next func(*Request) []byte) func(*Request) []byte {
	return func(r *Request) []byte {
		lim := r.Server.Limits
		if lim == nil {
			return next(r)
		}
		mail, _ := r.Body.Str("MAIL")
		addr, account := r.Conn.remoteIP(), limits.AccountKey(mail)
		if wait, why := lim.Login(addr, account); wait > 0 {
			if lim.ShouldLog("login", addr) { // the first a minute per address; the rest in the minute's limits line
				r.Server.Log.Logf("blaze", "   limits: login refused (%s; try again in %s): ERR_SERVER_BUSY (further "+
					"refusals for this address this minute: the limits line)", why, wait.Round(time.Second))
			}
			r.Fail(errLimitServerBusy)
			return nil
		}
		reply := next(r)
		lim.LoginDone(addr, account, r.errCode == 0 && r.dropped == "")
		return reply
	}
}

// limitGameStart: a user's games (practice, a custom lobby) over its rate or while it is in GamesAtOnce running
// hosted games get resetDedicatedServer's error reply for a host that can't start (ErrNoHostCapacity, the client's
// own path back to the menu, C1).
func limitGameStart(next func(*Request) []byte) func(*Request) []byte {
	return func(r *Request) []byte {
		lim, sess := r.Server.Limits, r.Conn.Session
		if lim == nil || sess == nil {
			return next(r)
		}
		user := sess.Account.UserID
		if err := lim.StartGame(user, r.Server.hosted.runningFor(user)); err != nil {
			r.Server.Log.Logf("blaze", "   limits: game start of account %d refused: %v", user, err)
			r.Fail(ErrNoHostCapacity)
			return nil
		}
		return next(r)
	}
}

// limitQueue: matchmaking starts over the user's rate get the error the matchmaker gives a second queue
// (GAMEMANAGER_ERR_MATCHMAKING_EXCEEDED_MAX_REQUESTS); one queue per user at a time is the matchmaker's own rule
// (matchmaker.ErrAlreadyQueued).
func limitQueue(next func(*Request) []byte) func(*Request) []byte {
	return func(r *Request) []byte {
		lim, sess := r.Server.Limits, r.Conn.Session
		if lim == nil || sess == nil {
			return next(r)
		}
		if err := lim.JoinQueue(sess.Account.UserID); err != nil {
			r.Server.Log.Logf("blaze", "   limits: matchmaking of account %d refused: %v", sess.Account.UserID, err)
			r.Fail(ErrMatchmakingExceededLimit)
			return nil
		}
		return next(r)
	}
}

// runningFor counts the hosted games the account is in (not ended, not removed).
func (h *hostedGames) runningFor(user int64) int {
	h.mu.Lock()
	games := make([]*hostedGame, 0, len(h.byID))
	for _, g := range h.byID {
		games = append(games, g)
	}
	h.mu.Unlock()
	n := 0
	for _, g := range games {
		g.mu.Lock()
		if !g.ended {
			for _, p := range g.players {
				if p.userID == user && !p.removed {
					n++
					break
				}
			}
		}
		g.mu.Unlock()
	}
	return n
}

// requestGate is ServeConn's per-connection request rate: before each frame it waits as the gate says, or reports
// false when the connection's requests have been held back for RequestHeld (it is then closed).
func (s *Server) requestGate(c *Conn) func() bool {
	if s.Limits == nil {
		return func() bool { return true }
	}
	g := s.Limits.RequestGate()
	return func() bool {
		wait, ok := g.Next()
		if !ok {
			s.Log.Logf("blaze", "limits: connection from %s closed: requests held back to %s for more than %s",
				c.remoteIP(), s.Limits.Config().Requests, s.Limits.Config().RequestHeld)
			return false
		}
		if wait > 0 {
			time.Sleep(wait)
		}
		return true
	}
}
