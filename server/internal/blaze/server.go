// Package blaze is the Blaze core: it reads Fire frames on the plain-TCP connection that the
// redirector hands out, logs every frame, and answers the requests it has handlers for. Unhandled
// requests get no answer, so the client's next unknown step stays visible in the log.
//
// Each component lives in its own file (util.go, authentication.go, ...) and registers its commands
// in NewServer. A handler's comment names its client evidence (member table, function address;
// docs/BLAZE.md). A handler may queue notifications (Request.Notify); they go out right after its
// reply, in order. Those that the reply depends on (Request.NotifyFirst, B13b: the users a list names)
// go out right before it.
package blaze

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/content"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/limits"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/progress"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

// Server holds the handler table and what handlers need to know about this server.
type Server struct {
	Log     *logging.Log
	Host    string // the address we hand out (dgserver --public-host, A5): ping site, telemetry, GMS; see ClientAddr
	QosPort int    // the QoS ping site's port
	// TelemetryPort is our telemetry sink's port, announced in the postAuth reply (TELE).
	TelemetryPort int
	Service       string // the Blaze service name, "battlegrounds-1-pc"
	Now           func() time.Time
	Store         *store.Store  // accounts, personas and player content
	NewKey        func() string // makes session keys (random; fixed in tests)
	// GMSPort is the port of the match server (GMS) address on Host that NotifyGameSetup announces
	// (B8). Nothing answers there yet; dgserver only logs what arrives.
	GMSPort int
	// Content is the game's catalog (B7a, dgserver --content); nil or empty = none: new accounts get
	// no items and the store is empty, as before B7a.
	Content *content.Content
	// Policy is what new accounts get and which account flags the server forces (B7a).
	Policy Policy
	// Hosts starts a match host per game (C1, dgserver --gms-dir; since H2 also on the host machines of
	// registered agents, --agent-key; gamehost.go). nil = B8's behaviour: NotifyGameSetup names GMSPort
	// at once and something else serves it.
	Hosts *gmshost.Manager
	// MM is the PvP matchmaker (B10a, GameManager::startMatchmaking; matchmaking.go); its rules come from
	// dgserver --mm-team-size, --mm-min-players, --mm-fill-after and --mm-max-wait.
	MM *matchmaker.Matchmaker
	// MMRefuse is a test aid (B10b, dgserver --mm-refuse): every startMatchmaking gets the error reply
	// GAMEMANAGER_ERR_INVALID_MATCHMAKING_CRITERIA, as a party larger than a team does, so one client can
	// see the client's error dialog.
	MMRefuse bool
	// TestLoginErrors is a test aid (dgserver --test-login-errors): the server's n-th Authentication::login
	// (counted from 0 over all connections) gets the error reply TestLoginErrors[n] (the 16-bit error code of the
	// Fire header; the client adds the component); 0 or past the list's end = the login is answered as usual. So
	// one session sees the client's popup for each code, then logs in. Not for real servers.
	TestLoginErrors []uint16
	testLoginTries  atomic.Int64
	// Accounts is the login check's sign-up policy (dgserver --signup, --signups-per-address; internal/accounts):
	// the zero value is open sign-up without a cap. Every login's password is checked against the account's hash.
	Accounts accounts.Policy
	// MMBots fills the open seats of a matchmade game with bots (B10d, dgserver --mm-bots, on by default there):
	// a match that formed with open seats (matchmaker.Match.Full false, after FillAfter) asks its host for bots
	// up to the team size (createGame's fillTeams). Off = the open seats stay empty (B10b's 1 v 0).
	MMBots bool
	// Rand is the random source of the post-game rewards (B7c, progression.go); nil = math/rand/v2's.
	Rand progress.Rand
	// ShaperSelect asks the match hosts for shaper select with each player's content (D3-live step 4,
	// dgserver --shaper-select, --free-rotation, --shaper-timers; shaperselect.go). Off = the host decides.
	ShaperSelect ShaperSelect
	// KillSwitches are the client features the server switches off (B14, dgserver --kill-switches; sysmsg.go): every
	// login gets them as the Messaging system message TYPE 300 TAG 0. nil = none sent (all allowed, as before B14).
	KillSwitches []int64
	// CustomBots fills a custom game's open seats with bots when it starts (B15, dgserver --custom-bots): createGame's
	// fillTeams = the team size. Off (the default) = only the lobby's humans play, as the original's custom games did
	// [assumed: their create dialog has no bot option]. A practice group's game (GameFlags 0x412: BotsFillTeams) gets
	// its bots from the host's auto rule either way.
	CustomBots bool
	// Limits are the rate and count limits (dgserver --limits; limits.go): nil = off.
	Limits *limits.Limiter
	// SessionEnd is what a client hears before EndAccountSessions closes its connection (dgserver --session-end;
	// kick.go): the zero value, SessionEndDisconnected, is UserSessions::UserSessionDisconnected.
	SessionEnd SessionEndNotice

	gameIDs atomic.Int64 // game ids, from 1
	hosted  hostedGames  // games with a match host (C1)
	groups  playgroups   // playgroups, e.g. a game's chat (B11)
	mm      mmState      // matchmaking sessions and their timer (B10a)
	msgs    messaging    // Messaging relay: online users, inboxes, rate limits (B10c)
	// G3: each persona's saved reconnect game id (BGOps::setGameID; reconnect.go)
	reconnect reconnectIDs
	// B13b: each online persona's presence (associationlists.go)
	presence presenceState
	// B14: the operator's broadcast messages (sysmsg.go)
	sys systemMessages
	// B15: the game browser's lists (customgames.go)
	browser gameBrowser
	// the logged-in connections by account (kick.go)
	live liveSessions

	handlers map[key]Handler
}

type key struct{ component, command uint16 }

// Handler answers one command.
type Handler struct {
	Name string
	Fn   func(*Request) []byte // returns the reply payload
}

// Request is what a handler gets: the frame, its decoded payload and the connection.
type Request struct {
	Server *Server
	Frame  fire.Frame
	Body   *tdf.Struct
	Conn   *Conn

	dropped string
	errCode uint16   // Fail: answer with an error reply
	after   []func() // After: run once the reply is out
}

// Drop tells ServeConn not to answer this request (nor send its queued notifications); reason goes to
// the log. Use it where the client's next move is unknown, so the case stays visible.
func (r *Request) Drop(reason string) { r.dropped = reason }

// Fail makes ServeConn answer with an error reply (message type 3, empty payload) whose header error
// field is code, the upper half of a Blaze error (code << 16 | component; docs/BLAZE.md *Components*).
// Queued notifications are not sent.
func (r *Request) Fail(code uint16) { r.errCode = code }

// After queues fn to run after the reply and its notifications were written (or failed), on the
// connection's goroutine (C1: a notification pushed later by another goroutine must not overtake
// the reply).
func (r *Request) After(fn func()) { r.after = append(r.after, fn) }

// Conn is one client connection's state.
type Conn struct {
	Session *Session // nil until Authentication::login succeeds
	notes   []note
	pre     []note // NotifyFirst's
	// Net is the client's last UserSessions::updateNetworkInfo (B10a: the matchmaker's region and a
	// party member's address); only the connection's own goroutine touches it.
	Net NetworkInfo

	// Since C1 other goroutines can write to the connection (Push), so every write takes wmu.
	nc         net.Conn
	wmu        sync.Mutex
	closed     bool
	sessionEnd []func() // OnSessionEnd's hooks

	// B13b: the users this connection's client was told about (UserSessions UserAdded) since its login, and the
	// persona whose friends hear when this connection's session ends (friendsLogin).
	kmu      sync.Mutex
	known    map[int64]bool
	statusOf int64

	// the tunnel address of a client that came through the tunnel (tunnel.go), nil for a direct one
	tun *tunnel.Addr

	// ending is set once EndAccountSessions ends this connection's session (so it is ended once); gone
	// is closed when ServeConn returns. Requests are answered as usual until the connection closes: the client's own
	// leave of a match it was kicked from (updateMeshConnection, leavePlaygroup) goes through (seen in a test run).
	ending atomic.Bool
	gone   chan struct{}
}

// closeNow closes the connection from another goroutine (an ended session); ServeConn then returns and runs
// the session-end hooks.
func (c *Conn) closeNow() {
	if c.nc != nil {
		c.nc.Close()
	}
}

// Addr is the client's address (the login check's source address, sign-ups per address). The zero Addr when the
// connection has none (a test's pipe). For a client that came through the tunnel it is the player's real
// address as the gateway saw it, not the loopback address the game's socket has on the player's PC.
func (c *Conn) Addr() netip.Addr {
	if t := c.TunnelAddr(); t != nil {
		return t.Peer
	}
	if c.nc == nil {
		return netip.Addr{}
	}
	ap, err := netip.ParseAddrPort(c.nc.RemoteAddr().String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

// firstSight reports whether the connection's client hasn't been told about user id yet, and marks it told.
func (c *Conn) firstSight(id int64) bool {
	c.kmu.Lock()
	defer c.kmu.Unlock()
	if c.known[id] {
		return false
	}
	if c.known == nil {
		c.known = map[int64]bool{}
	}
	c.known[id] = true
	return true
}

// forgetUsers starts a new login's set of told users.
func (c *Conn) forgetUsers() {
	c.kmu.Lock()
	c.known = nil
	c.kmu.Unlock()
}

// Push sends a notification now, from any goroutine (C1: the match host's events). It returns false
// if the connection is gone; name is only for the log.
func (c *Conn) Push(s *Server, component, id uint16, name string, payload []byte) bool {
	c.wmu.Lock()
	closed := c.closed
	c.wmu.Unlock()
	if closed || c.nc == nil {
		s.Log.Logf("blaze", "   (%s::%s notification not sent: connection closed)", fire.ComponentName(component), name)
		return false
	}
	return s.send(c, fire.Frame{Component: component, Command: id, Type: fire.Notification, Payload: payload},
		fmt.Sprintf("%s::%s notification", fire.ComponentName(component), name))
}

// OnSessionEnd registers fn to run once when the connection's session ends: when its user logs out
// (Authentication::logout, B12) or when the connection closes, whichever comes first, on the connection's
// goroutine. Everything a login leaves behind (the hosted game's seat, playgroups, a matchmaking session,
// the Messaging relay's online entry) ends this way, as Blaze ends it with the user session. Registered
// after the connection has closed, fn runs at once.
func (c *Conn) OnSessionEnd(fn func()) {
	c.wmu.Lock()
	if !c.closed {
		c.sessionEnd = append(c.sessionEnd, fn)
		c.wmu.Unlock()
		return
	}
	c.wmu.Unlock()
	fn()
}

// endSession runs the session-end hooks registered so far and forgets them. closed marks the connection
// closed first (later registrations then run at once); after a logout the connection stays open, and a new
// login registers new hooks.
func (c *Conn) endSession(closed bool) {
	c.wmu.Lock()
	if closed {
		c.closed = true
	}
	hooks := c.sessionEnd
	c.sessionEnd = nil
	c.wmu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}

// Session is the logged-in user of a connection.
type Session struct {
	Account store.Account
	Persona store.Persona // zero until Authentication::loginPersona
	Key     string        // session key (LoginResponse SKEY, SessionInfo KEY)
	First   bool          // the account was created by this login
}

type note struct {
	name  string
	frame fire.Frame
}

// Notify queues a notification (component, notification id, payload); ServeConn sends it after the
// reply. name is only for the log.
func (r *Request) Notify(component, id uint16, name string, payload []byte) {
	r.Conn.notes = append(r.Conn.notes, note{name: name, frame: fire.Frame{
		Component: component, Command: id, Type: fire.Notification, Payload: payload}})
}

// NotifyFirst queues a notification that ServeConn sends right before the reply (B13b: the client handles
// frames in order, and a list reply's members must be known users by then). Like Notify's, they are not
// sent with an error reply or a dropped request.
func (r *Request) NotifyFirst(component, id uint16, name string, payload []byte) {
	r.Conn.pre = append(r.Conn.pre, note{name: name, frame: fire.Frame{
		Component: component, Command: id, Type: fire.Notification, Payload: payload}})
}

// Service is the Blaze service name the client asks for (redirector NAME, preAuth SVCN).
const Service = "battlegrounds-1-pc"

// NewServer returns a server with every component's handlers registered.
func NewServer(log *logging.Log, host string, qosPort int) *Server {
	s := &Server{Log: log, Host: host, QosPort: qosPort, Service: Service, Now: time.Now, Store: store.New(),
		NewKey: randomKey, GMSPort: GMSPort, Policy: DefaultPolicy(), MM: matchmaker.New(DefaultMatchmaking()),
		handlers: map[key]Handler{}}
	registerUtil(s)
	registerAuthentication(s)
	registerUserSessions(s)
	registerEntry(s)
	registerBattlegroundsAccounts(s)
	registerBGEcommerce(s)
	registerLivingLore(s)
	registerBGWeb(s)
	registerStats(s)
	registerAssociationLists(s)
	registerRooms(s)
	registerMessaging(s)
	registerBGOps(s)
	registerGameManager(s)
	registerReconnect(s) // G3: getGameDataFromId, joinGame
	registerPlaygroups(s)
	applyLimits(s) // login, game starts and queues (limits.go)
	return s
}

// randomKey returns 16 random bytes as hex, the session key format we hand out.
func randomKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	return hex.EncodeToString(b[:])
}

// Register adds a handler for component/command.
func (s *Server) Register(component, command uint16, name string, fn func(*Request) []byte) {
	s.handlers[key{component, command}] = Handler{Name: name, Fn: fn}
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.ServeConn(c)
	}
}

// ServeConn handles one client connection until it closes.
func (s *Server) ServeConn(c net.Conn) {
	defer c.Close()
	const who = "blaze"
	s.Log.Log(who, "connection from "+logging.Peer(c.RemoteAddr()))
	conn := &Conn{nc: c, tun: tunnel.Local(c), gone: make(chan struct{})}
	defer close(conn.gone)
	defer conn.endSession(true)
	r := fire.NewReader(c)
	gate := s.requestGate(conn) // requests per connection
	for {
		f, err := r.Next()
		if err != nil {
			fire.LogEnd(s.Log, who, r, err)
			break
		}
		if !gate() {
			break
		}
		fire.Describe(s.Log, who, f)
		h, ok := s.handlers[key{f.Component, f.Command}]
		if !ok || f.Type != fire.Request {
			s.Log.Log(who, "   (no handler; not answered)")
			continue
		}
		body, err := tdf.Decode(f.Payload)
		if err != nil {
			s.Log.Log(who, "   (request doesn't decode; not answered)")
			continue
		}
		conn.notes, conn.pre = conn.notes[:0], conn.pre[:0]
		req := &Request{Server: s, Frame: f, Body: body, Conn: conn}
		reply := h.Fn(req)
		if req.dropped != "" {
			s.Log.Logf(who, "   (%s; not answered)", req.dropped)
			continue
		}
		if req.errCode != 0 {
			ok = s.send(conn, fire.Frame{Component: f.Component, Command: f.Command, Error: req.errCode,
				Type: fire.ErrorReply, ID: f.ID}, fmt.Sprintf("%s error reply (error %#04x)", h.Name, req.errCode))
		} else {
			ok = true
			for _, n := range conn.pre {
				if ok = s.send(conn, n.frame, fmt.Sprintf("%s::%s notification", fire.ComponentName(n.frame.Component), n.name)); !ok {
					break
				}
			}
			ok = ok && s.send(conn, fire.Frame{Component: f.Component, Command: f.Command, Type: fire.Reply, ID: f.ID, Payload: reply},
				h.Name+" reply")
			for _, n := range conn.notes {
				if !ok {
					break
				}
				ok = s.send(conn, n.frame, fmt.Sprintf("%s::%s notification", fire.ComponentName(n.frame.Component), n.name))
			}
		}
		for _, fn := range req.after {
			fn()
		}
		if !ok {
			break
		}
	}
	s.Log.Log(who, "connection closed")
}

// send writes one frame and logs it as "-> <what>, N bytes:" plus its payload. It returns false if
// the connection failed; a frame that doesn't encode is logged and skipped.
func (s *Server) send(c *Conn, f fire.Frame, what string) bool {
	const who = "blaze"
	out, err := fire.Encode(f)
	if err != nil {
		s.Log.Logf(who, "   (%s not sent: %v)", what, err)
		return true
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.nc.Write(out); err != nil {
		s.Log.Logf(who, "write error: %s: %v", logging.ErrName(err), err)
		return false
	}
	s.Log.Log(who, fmt.Sprintf("-> %s, %d bytes:\n", what, len(f.Payload))+prettyPayload(f.Payload))
	return true
}

func prettyPayload(b []byte) string {
	st, err := tdf.Decode(b)
	if err != nil {
		return fmt.Sprintf("   (TDF decode failed: %v)\n%s", err, logging.Hexdump(b))
	}
	return tdf.Pretty(st, 2)
}
