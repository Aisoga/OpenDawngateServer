package gmshost

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"dawngate/server/internal/logging"
)

// DefaultControlPort is the control listener's port (dgserver --gms-control-port).
const DefaultControlPort = 10040

// Default timeouts (ADR-2 decision 7).
const (
	DefaultHelloTimeout     = 30 * time.Second  // spawn -> hello
	DefaultReadyTimeout     = 120 * time.Second // createGame -> gameReady
	DefaultHeartbeatTimeout = 30 * time.Second  // between two messages once ready (heartbeat every 5 s)
	DefaultExitTimeout      = 10 * time.Second  // logoutGMS or abortGame -> exit
	reapTimeout             = 10 * time.Second  // kill -> exit
	exitDrain               = 2 * time.Second   // exit -> the rest of the control connection
	writeTimeout            = 5 * time.Second
	// H2 (remote.go)
	DefaultAgentTimeout  = 30 * time.Second // between two lines of a registered agent (heartbeat every 5 s)
	DefaultLaunchTimeout = 10 * time.Second // launchHost -> hostStarted or hostFailed
)

const who = "host"

// LocalSite is Match.Site for a host on this machine (dgserver --gms-dir); a host an agent started
// has the agent's name there (H2).
const LocalSite = "local"

// Config configures a Manager.
type Config struct {
	Log *logging.Log
	// The hosts on this machine (dgserver --gms-dir): Launcher starts them, Pool hands out their GMS
	// ports. A nil Launcher means no hosts here: every match goes to an agent (H2).
	Launcher Launcher
	Pool     *Pool
	// BindHost is the host part of DGGMS_BIND: the address the GMS port binds (dgserver --bind). What
	// the server announces in HNET is its public host (--public-host, blaze.Server.Host; A5).
	BindHost string
	// AgentKey lets agents register (remote host machines, H2: dgserver --agent with the same key; see
	// remote.go). Empty: agents are refused.
	AgentKey []byte

	HelloTimeout, ReadyTimeout, HeartbeatTimeout, ExitTimeout time.Duration
	// AgentTimeout drops a registered agent that sent nothing for this long; LaunchTimeout is how
	// long an agent may take to answer launchHost (0 = the defaults).
	AgentTimeout, LaunchTimeout time.Duration
	// Warm (H1-live, dgserver --gms-warm) keeps this many idle hosts ready on this machine: started
	// ahead (FillWarm), their hello seen, waiting for a game without a timeout (DGGMS_WARM). Start
	// gives a game to the oldest idle one, so no process start and no engine start-up lies between
	// resetDedicatedServer and gameReady, and starts its replacement in the background. Each holds a
	// port of the pool. 0 = a host per game, started by Start (C1).
	Warm int
}

// The warm pool's states of a Match (Match.warm, under Manager.mu).
const (
	warmNone     = iota // a host with a game (or one that never was warm)
	warmStarting        // a warm host started, its hello not seen yet
	warmIdle            // a warm host ready for a game (in Manager.idle)
)

// warmMaxFails warm hosts in a row that end before their hello stop the refilling (a start-up
// problem would otherwise restart hosts forever); the next one that gets ready resets the count.
const warmMaxFails = 3

// site is a machine that runs hosts: this one (dgserver --gms-dir) or an agent's (H2).
type site struct {
	name     string // LocalSite or the agent's name
	launcher Launcher
	pool     *Pool
	bindHost string     // DGGMS_BIND's host
	gmsHost  string     // the host announced in HNET; "" = the server's own public host
	agent    *agentConn // nil for this machine
}

// ErrNoSite is returned by Start when no machine can run a host: no local hosts (--gms-dir) and no
// agent registered.
var ErrNoSite = errors.New("gmshost: no host machine (no local hosts and no agent registered)")

// Events are the caller's callbacks. They run on the match's goroutine, one at a time, in the order
// the host sent the messages; any may be nil. A callback must not wait for the match to end.
type Events struct {
	Ready        func(mt *Match, port int) // gameReady: announce the GMS address (NotifyGameSetup)
	PlayerAdded  func(mt *Match, p AddPlayer)
	PlayerGone   func(mt *Match, p DisconnectPlayer)
	Update       func(mt *Match, u GMSUpdate)
	PlayerFinish func(mt *Match, p PlayerFinish)
	Finish       func(mt *Match, f FinishGame)
	PlayerKarma  func(mt *Match, k PlayerKarma) // D8c: a matchmade game's karma votes, after finishGame
	End          func(mt *Match, o Outcome)     // the host is gone (always called last, once)
}

// Outcome is how a match ended.
type Outcome struct {
	Ready    bool       // gameReady arrived (the game was announced)
	Finished bool       // finishGame arrived (Result holds it); otherwise the match was aborted
	Result   FinishGame // the finishGame
	Reason   string     // the abort reason, or Result.Reason
	Killed   bool       // the server killed the process
	ExitCode int        // -1 if unknown
	Duration time.Duration
	// H5: the host's first fatal reportGMSError (it gave up: ErrTickStalled from its watchdog,
	// ErrStepFailing, ...); without finishGame before it, Reason is "host error <code> (<name>): <text>".
	Error  *GMSError
	Errors int // reportGMSError lines that weren't fatal (H5: a step's exceptions, 1st, 10th, 100th, ...)
}

// Manager runs the control listener and the matches.
type Manager struct {
	cfg Config
	log *logging.Log

	mu      sync.Mutex
	ln      net.Listener
	matches map[int64]*Match
	sites   []*site // this machine first (if it runs hosts), then the agents in registration order
	nextID  int64
	closed  bool
	wg      sync.WaitGroup
	// the warm pool (H1-live, Config.Warm)
	idle         []*Match // warm hosts ready for a game, oldest first
	warmStarting int      // warm hosts started whose hello hasn't come yet
	warmFails    int      // warm hosts in a row that ended before their hello
	warmPaused   bool     // warmMaxFails reached: the pool isn't refilled
}

// NewManager returns a manager; zero timeouts get the defaults.
func NewManager(cfg Config) *Manager {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&cfg.HelloTimeout, DefaultHelloTimeout)
	def(&cfg.ReadyTimeout, DefaultReadyTimeout)
	def(&cfg.HeartbeatTimeout, DefaultHeartbeatTimeout)
	def(&cfg.ExitTimeout, DefaultExitTimeout)
	def(&cfg.AgentTimeout, DefaultAgentTimeout)
	def(&cfg.LaunchTimeout, DefaultLaunchTimeout)
	if cfg.BindHost == "" {
		cfg.BindHost = "127.0.0.1"
	}
	if cfg.Log == nil {
		cfg.Log = logging.New(nopWriter{})
	}
	m := &Manager{cfg: cfg, log: cfg.Log, matches: map[int64]*Match{}}
	if cfg.Launcher != nil {
		if cfg.Pool == nil {
			first, last, _ := ParsePorts(DefaultPorts)
			m.cfg.Pool = NewPool(first, last)
		}
		m.sites = []*site{{name: LocalSite, launcher: cfg.Launcher, pool: m.cfg.Pool, bindHost: cfg.BindHost}}
	}
	return m
}

type nopWriter struct{}

func (nopWriter) Write(b []byte) (int, error) { return len(b), nil }

// Listen binds the control listener (TCP, "127.0.0.1:10040") and serves it in the background.
func (m *Manager) Listen(addr string) error {
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return err
	}
	m.Serve(ln)
	return nil
}

// Serve serves the control protocol on ln in the background.
func (m *Manager) Serve(ln net.Listener) {
	m.mu.Lock()
	m.ln = ln
	m.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go m.handshake(c)
		}
	}()
}

// Addr is the control listener's address ("" before Listen).
func (m *Manager) Addr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ln == nil {
		return ""
	}
	return m.ln.Addr().String()
}

// Running returns the number of running matches.
func (m *Manager) Running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.matches)
}

// SiteInfo describes a machine that runs hosts (Sites).
type SiteInfo struct {
	Name     string // LocalSite or the agent's name
	Addr     string // the agent's address as seen here ("" for this machine)
	BindHost string // DGGMS_BIND's host
	GMSHost  string // announced in HNET ("" = the server's own public host)
	Ports    string // the GMS port range
	Free     int    // free GMS ports
	Running  int    // its running matches
}

// Sites lists the machines that run hosts: this one first (if it does), then the registered agents.
func (m *Manager) Sites() []SiteInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SiteInfo
	for _, s := range m.sites {
		si := SiteInfo{Name: s.name, BindHost: s.bindHost, GMSHost: s.gmsHost, Ports: s.pool.String(), Free: s.pool.Free()}
		if s.agent != nil {
			si.Addr = s.agent.ip.String()
		}
		for _, mt := range m.matches {
			if mt.site == s {
				si.Running++
			}
		}
		out = append(out, si)
	}
	return out
}

// ErrClosed is returned by Start after Close.
var ErrClosed = errors.New("gmshost: manager closed")

// pickSite chooses where the next host runs: the machine with the most free GMS ports, ties going
// to the earlier one (this machine, then the agents in registration order).
func (m *Manager) pickSite() (*site, error) {
	var best *site
	bestFree := 0
	for _, s := range m.sites {
		if f := s.pool.Free(); f > bestFree {
			best, bestFree = s, f
		}
	}
	switch {
	case best != nil:
		return best, nil
	case len(m.sites) == 0:
		return nil, ErrNoSite
	}
	return nil, ErrNoPort
}

// Start spawns a host for game: it picks a machine (pickSite) and a port there, starts the process
// with the DGGMS_* environment and supervises it. With a warm pool (Config.Warm, H1-live) it gives
// the game to an idle warm host instead, if there is one, and starts that one's replacement in the
// background. The caller hears back through ev. Errors: ErrNoPort (every pool exhausted), ErrNoSite,
// the launcher's error, ErrClosed.
func (m *Manager) Start(game CreateGame, ev Events) (*Match, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	if m.ln == nil {
		m.mu.Unlock()
		return nil, errors.New("gmshost: control listener not started")
	}
	if len(m.idle) > 0 { // H1-live: the oldest warm host takes the game
		mt := m.idle[0]
		m.idle = m.idle[1:]
		mt.warm = warmNone
		// Written under m.mu and before the assign command, so the match's goroutine (which reads
		// them only after the command, or after leaveWarm under m.mu) sees them.
		mt.Game, mt.ev = game, ev
		m.mu.Unlock()
		mt.command(assignGame{})
		m.log.Logf(who, "match %d: started pid %d (warm host, ready for %.1f s) for game %d (%s, %s, %d players), GMS %s",
			mt.ID, mt.proc.Pid(), time.Since(mt.started).Seconds(), game.GameID, game.Mode, game.Level, len(game.Roster), mt.Bind)
		go m.FillWarm()
		return mt, nil
	}
	m.nextID++
	id := m.nextID
	st, err := m.pickSite()
	var port int
	if err == nil {
		port, err = st.pool.Acquire() // only Start and FillWarm acquire, under m.mu, so pickSite's free port is still free
	}
	if err != nil {
		m.mu.Unlock()
		m.log.Logf(who, "match %d (game %d): not started: %v", id, game.GameID, err)
		return nil, err
	}
	mt, env := m.newMatch(id, st, port, game, ev, false)
	m.mu.Unlock()

	if err := m.launch(mt, env); err != nil {
		m.log.Logf(who, "match %d (game %d): launch failed: %v", id, game.GameID, err)
		return nil, err
	}
	on := ""
	if st.agent != nil {
		on = " on agent " + st.name
	}
	m.log.Logf(who, "match %d: started pid %d%s for game %d (%s, %s, %d players), GMS %s", id, mt.proc.Pid(), on,
		game.GameID, game.Mode, game.Level, len(game.Roster), env.Bind)
	go mt.run()
	return mt, nil
}

// newMatch registers a match on st with port (m.mu held). It is registered before the launch, so a
// host that dials in at once finds its match (its hello waits for run).
func (m *Manager) newMatch(id int64, st *site, port int, game CreateGame, ev Events, warm bool) (*Match, Env) {
	mt := &Match{ID: id, Port: port, Site: st.name, Bind: net.JoinHostPort(st.bindHost, strconv.Itoa(port)),
		GMSHost: st.gmsHost, Game: game, m: m, site: st, ev: ev, token: newToken(), started: time.Now(),
		attach: make(chan *ctlConn), cmds: make(chan Message, 16), done: make(chan struct{})}
	if warm {
		mt.warm = warmStarting
		m.warmStarting++
	}
	m.matches[id] = mt
	m.wg.Add(1)
	return mt, Env{Control: m.ln.Addr().String(), Match: id, Token: mt.token, Bind: mt.Bind, Warm: warm}
}

// launch starts mt's process. The lock isn't held across the launch: an agent answers it over the
// network. On failure the match is unregistered and its port freed.
func (m *Manager) launch(mt *Match, env Env) error {
	proc, err := mt.site.launcher.Launch(env)
	if err != nil {
		m.mu.Lock()
		delete(m.matches, mt.ID)
		if mt.warm == warmStarting {
			m.warmStarting--
		}
		mt.warm = warmNone
		m.mu.Unlock()
		mt.site.pool.Release(mt.Port)
		close(mt.done) // a hello that came meanwhile is turned away
		m.wg.Done()
		return err
	}
	mt.proc = proc
	return nil
}

// assignGame is the internal command that hands an idle warm host its game (Start): the match's
// goroutine sends createGame (H1-live).
type assignGame struct{}

func (assignGame) Type() string { return "assignGame" }

// FillWarm starts warm hosts on this machine until Config.Warm of them are idle or starting
// (H1-live). dgserver calls it once after Listen; Start calls it after it gave a warm host a game, and
// the pool after a warm host ended without one. Without local hosts or with Warm 0 it does nothing.
func (m *Manager) FillWarm() {
	for {
		m.mu.Lock()
		if m.closed || m.ln == nil || m.cfg.Warm <= 0 || len(m.sites) == 0 || m.sites[0].name != LocalSite ||
			m.warmPaused || len(m.idle)+m.warmStarting >= m.cfg.Warm {
			m.mu.Unlock()
			return
		}
		st := m.sites[0]
		port, err := st.pool.Acquire()
		if err != nil {
			m.mu.Unlock()
			m.log.Logf(who, "warm pool: no port for another warm host (%v)", err)
			return
		}
		m.nextID++
		mt, env := m.newMatch(m.nextID, st, port, CreateGame{}, Events{}, true)
		m.mu.Unlock()
		if err := m.launch(mt, env); err != nil {
			m.log.Logf(who, "match %d: warm host not started: %v", mt.ID, err)
			m.warmEnded(false)
			return
		}
		m.log.Logf(who, "match %d: warm host started, pid %d, GMS %s (it waits for a game)", mt.ID, mt.proc.Pid(), mt.Bind)
		go mt.run()
	}
}

// Warm returns the warm pool: hosts ready for a game and hosts still starting.
func (m *Manager) Warm() (idle, starting int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.idle), m.warmStarting
}

// warmReady moves a warm host whose hello came into the idle list; false if mt isn't a starting
// warm host (a host with a game).
func (m *Manager) warmReady(mt *Match) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mt.warm != warmStarting {
		return false
	}
	mt.warm = warmIdle
	m.warmStarting--
	m.idle = append(m.idle, mt)
	m.warmFails = 0
	m.warmPaused = false
	return true
}

// leaveWarm takes a warm host out of the pool when it ends or loses its connection; true if it had
// no game (then Start can't give it one any more). False means it has a game: Start set mt.Game and
// mt.ev under m.mu, so the caller may read them now.
func (m *Manager) leaveWarm(mt *Match) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch mt.warm {
	case warmIdle:
		for i, x := range m.idle {
			if x == mt {
				m.idle = append(m.idle[:i:i], m.idle[i+1:]...)
				break
			}
		}
	case warmStarting:
		m.warmStarting--
	default:
		return false
	}
	mt.warm = warmNone
	return true
}

// warmEnded counts a warm host that ended without a game (ready: after its hello) and refills the
// pool, unless warmMaxFails hosts in a row ended before their hello.
func (m *Manager) warmEnded(ready bool) {
	m.mu.Lock()
	if !ready {
		m.warmFails++
	}
	if m.warmFails >= warmMaxFails && !m.warmPaused {
		m.warmPaused = true
		m.log.Logf(who, "warm pool: %d warm hosts in a row ended before their hello; the pool is not refilled", m.warmFails)
	}
	paused, closed := m.warmPaused, m.closed
	m.mu.Unlock()
	if !paused && !closed {
		go m.FillWarm()
	}
}

// Close kills every host, stops the listener, waits (up to the reap timeout) for the matches to end
// and closes the agents' connections.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	if m.ln != nil {
		m.ln.Close()
	}
	var all []*Match
	for _, mt := range m.matches {
		all = append(all, mt)
	}
	m.mu.Unlock()
	for _, mt := range all {
		mt.command(shutdown{})
	}
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(reapTimeout + exitDrain):
		m.log.Log(who, "(some hosts did not end in time)")
	}
	m.mu.Lock()
	var agents []*agentConn
	for _, s := range m.sites {
		if s.agent != nil {
			agents = append(agents, s.agent)
		}
	}
	m.mu.Unlock()
	for _, a := range agents {
		a.close("server shutting down")
	}
}

func newToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	return hex.EncodeToString(b[:])
}

// handshake reads a new connection's first line: a host's hello (checked here, then the connection
// goes to its match) or an agent's agentHello (remote.go). Anything else closes it.
func (m *Manager) handshake(c net.Conn) {
	peer := logging.Peer(c.RemoteAddr())
	c.SetReadDeadline(time.Now().Add(m.cfg.HelloTimeout))
	r := NewLineReader(c)
	line, err := r.Next()
	if err != nil {
		m.log.Logf(who, "control connection from %s closed before hello: %v", peer, err)
		c.Close()
		return
	}
	msg, _ := Decode(line)
	switch h := msg.(type) {
	case Hello:
		m.hostHello(c, r, h, peer)
	case AgentHello:
		m.agentHello(c, r, h, peer)
	default:
		m.log.Logf(who, "control connection from %s rejected: first line is not hello: %.200s", peer, line)
		c.Close()
	}
}

// hostHello checks a host's hello and hands the connection to its match. The token must be the
// match's, unused (a token is good for one hello: a replay is refused even while the match runs), and
// the connection must come from the machine the host was started on (helloFrom).
func (m *Manager) hostHello(c net.Conn, r *LineReader, h Hello, peer string) {
	ip := remoteIP(c)
	m.mu.Lock()
	mt := m.matches[h.Match]
	var why string
	switch {
	case mt == nil || subtle.ConstantTimeCompare([]byte(h.Token), []byte(mt.token)) != 1:
		why = fmt.Sprintf("unknown match %d or wrong token", h.Match)
	case mt.helloSeen:
		why = fmt.Sprintf("match %d's token was used already (a replay, or a second host)", h.Match)
	case !m.helloFrom(mt.site, ip):
		why = fmt.Sprintf("hello for match %d from %s, but its host runs on %s (%s)", h.Match, ip, mt.site.name, m.siteAddr(mt.site))
	default:
		mt.helloSeen = true
	}
	m.mu.Unlock()
	if why != "" {
		m.log.Logf(who, "control connection from %s rejected: %s", peer, why)
		c.Close()
		return
	}
	c.SetReadDeadline(time.Time{})
	cc := &ctlConn{c: c, r: r, hello: h, msgs: make(chan Message, 64), gone: make(chan struct{})}
	select {
	case mt.attach <- cc:
		go cc.readLoop(mt)
	case <-mt.done:
		c.Close()
	}
}

// helloFrom: may a hello for a host on s come from ip? An agent's hosts dial the same address the
// agent dialed, so they come from the agent's address; hosts on this machine come from loopback or
// from the control listener's own address (--gms-control-bind). Called with m.mu held.
func (m *Manager) helloFrom(s *site, ip net.IP) bool {
	if ip == nil {
		return false
	}
	if s.agent != nil {
		return ip.Equal(s.agent.ip)
	}
	if ip.IsLoopback() {
		return true
	}
	a, ok := m.ln.Addr().(*net.TCPAddr)
	return ok && ip.Equal(a.IP)
}

// siteAddr describes where a site's hosts must come from (for the log).
func (m *Manager) siteAddr(s *site) string {
	if s.agent != nil {
		return s.agent.ip.String()
	}
	return "loopback or " + m.ln.Addr().String()
}

// remoteIP is the IPv4 (or IPv6) address a TCP connection comes from; nil for other connections.
func remoteIP(c net.Conn) net.IP {
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		return a.IP
	}
	return nil
}

// ctlConn is a host's control connection after its hello.
type ctlConn struct {
	c     net.Conn
	r     *LineReader
	hello Hello
	msgs  chan Message
	gone  chan struct{} // closed when the connection ends; every message read is in msgs before
	err   error
}

func (cc *ctlConn) readLoop(mt *Match) {
	defer close(cc.gone)
	for {
		line, err := cc.r.Next()
		if err != nil {
			cc.err = err
			return
		}
		mt.m.log.Logf(who, "match %d <- %s", mt.ID, line)
		msg, err := Decode(line)
		if err != nil {
			mt.m.log.Logf(who, "match %d: (line ignored: %v)", mt.ID, err)
			continue
		}
		select {
		case cc.msgs <- msg:
		case <-mt.done:
			return
		}
	}
}

// Match is one host process and its match.
type Match struct {
	ID   int64
	Port int    // the pool port in DGGMS_BIND
	Site string // where the host runs: LocalSite, or the agent's name (H2)
	Bind string // DGGMS_BIND, "<the machine's bind address>:<Port>"
	// GMSHost is the host part of the GMS address clients are told (NotifyGameSetup's HNET): the
	// agent's public address for a host on another machine (H2); "" for one on this machine, which
	// the server announces on its own public host (--public-host, A5).
	GMSHost string
	Game    CreateGame

	m         *Manager
	site      *site
	ev        Events
	proc      Process
	token     string
	helloSeen bool // the token was used (under m.mu)
	warm      int  // the warm pool's state: warmNone, warmStarting, warmIdle (under m.mu; H1-live)
	started   time.Time
	attach    chan *ctlConn
	cmds      chan Message
	done      chan struct{}
	outcome   Outcome
}

// shutdown is Close's internal command: kill at once.
type shutdown struct{}

func (shutdown) Type() string { return "shutdown" }

// RemovePlayer tells the host that user left on the Blaze side (reason: a PlayerRemovedReason name).
func (mt *Match) RemovePlayer(user int64, reason string) {
	mt.command(RemovePlayer{User: user, Reason: reason})
}

// Abort ends the match: the host gets abortGame and must exit within the exit timeout; before its
// hello it is killed at once.
func (mt *Match) Abort(reason string) { mt.command(AbortGame{Reason: reason}) }

// Rewards tells the host what a player got for the game (playerRewards, B7c). It may be called from an
// event handler (Events.Finish): the command queue holds 16, more than a match has players.
func (mt *Match) Rewards(user int64, r Rewards) {
	if r.Items == nil {
		r.Items = []RewardItem{} // [] as the C++ side writes it
	}
	mt.command(PlayerRewards{User: user, Rewards: r})
}

// UpdateRoster tells a custom game's host its lobby roster after a change (B15: who may connect; the host holds
// their HelloReq until StartGame). Before the host's hello it only replaces the roster createGame will carry.
func (mt *Match) UpdateRoster(roster []RosterEntry) {
	mt.command(UpdateRoster{Roster: append([]RosterEntry(nil), roster...)})
}

// StartGame starts a custom game on its host with the final roster (B15; the admin's advanceGameState IN_GAME).
func (mt *Match) StartGame(roster []RosterEntry) {
	mt.command(StartGame{Roster: append([]RosterEntry(nil), roster...)})
}

func (mt *Match) command(msg Message) {
	select {
	case mt.cmds <- msg:
	case <-mt.done:
	}
}

// Done is closed when the match has ended (after Events.End).
func (mt *Match) Done() <-chan struct{} { return mt.done }

// Outcome returns how the match ended; valid after Done.
func (mt *Match) Outcome() Outcome { return mt.outcome }

// Pid returns the host's process id.
func (mt *Match) Pid() int { return mt.proc.Pid() }

type exitInfo struct {
	code int
	err  error
}

// run supervises the match: one goroutine per match owns its state and calls the events.
func (mt *Match) run() {
	m, cfg := mt.m, mt.m.cfg
	defer m.wg.Done()
	exited := make(chan exitInfo, 1)
	go func() {
		code, err := mt.proc.Wait()
		exited <- exitInfo{code, err}
	}()

	const (
		waitHello = iota
		waitReady
		running
		idle // a warm host after its hello, waiting for a game (H1-live)
	)
	phase := waitHello
	since := mt.started     // the start, or for a warm host the moment it got its game
	unassignedWarm := false // a warm host left the pool without a game (leaveWarm)
	var (
		conn       *ctlConn
		msgs       <-chan Message
		gone       <-chan struct{}
		ending     bool // logoutGMS or abortGame: only the exit is awaited
		killed     bool
		o          = Outcome{ExitCode: -1}
		timerWhat  = fmt.Sprintf("no hello within %v", cfg.HelloTimeout)
		timer      = time.NewTimer(cfg.HelloTimeout)
		abortCause string
	)
	defer timer.Stop()
	setTimer := func(d time.Duration, what string) { timer.Reset(d); timerWhat = what }
	send := func(msg Message) {
		if conn == nil {
			return
		}
		line, err := Encode(msg)
		if err == nil {
			conn.c.SetWriteDeadline(time.Now().Add(writeTimeout))
			_, err = conn.c.Write(line)
		}
		if err != nil {
			m.log.Logf(who, "match %d: (%s not sent: %v)", mt.ID, msg.Type(), err)
			conn.c.Close() // the reader ends and closes gone
			return
		}
		m.log.Logf(who, "match %d -> %s", mt.ID, line[:len(line)-1])
	}
	kill := func(reason string) {
		if killed {
			return
		}
		killed, o.Killed = true, true
		if abortCause == "" {
			abortCause = reason
		}
		m.log.Logf(who, "match %d: killing pid %d: %s", mt.ID, mt.proc.Pid(), reason)
		if err := mt.proc.Kill(); err != nil {
			m.log.Logf(who, "match %d: (kill: %v)", mt.ID, err)
		}
		setTimer(reapTimeout, "not reaped after kill")
	}
	handle := func(msg Message) {
		if phase == running && !ending {
			setTimer(cfg.HeartbeatTimeout, fmt.Sprintf("no message within %v", cfg.HeartbeatTimeout))
		}
		switch v := msg.(type) {
		case GameReady:
			if phase != waitReady {
				m.log.Logf(who, "match %d: (gameReady again, ignored)", mt.ID)
				return
			}
			phase, o.Ready = running, true
			if !ending {
				setTimer(cfg.HeartbeatTimeout, fmt.Sprintf("no message within %v", cfg.HeartbeatTimeout))
			}
			if v.Port != mt.Port {
				m.log.Logf(who, "match %d: host reports GMS port %d, DGGMS_BIND said %d", mt.ID, v.Port, mt.Port)
			}
			m.log.Logf(who, "match %d: ready on port %d, %.1f s after the start", mt.ID, v.Port, time.Since(since).Seconds())
			if mt.ev.Ready != nil {
				mt.ev.Ready(mt, v.Port)
			}
		case AddPlayer:
			if mt.ev.PlayerAdded != nil {
				mt.ev.PlayerAdded(mt, v)
			}
		case DisconnectPlayer:
			if mt.ev.PlayerGone != nil {
				mt.ev.PlayerGone(mt, v)
			}
		case GMSUpdate:
			if mt.ev.Update != nil {
				mt.ev.Update(mt, v)
			}
		case PlayerFinish:
			if mt.ev.PlayerFinish != nil {
				mt.ev.PlayerFinish(mt, v)
			}
		case FinishGame:
			if o.Finished {
				m.log.Logf(who, "match %d: (finishGame again, ignored)", mt.ID)
				return
			}
			o.Finished, o.Result = true, v
			if mt.ev.Finish != nil {
				mt.ev.Finish(mt, v)
			}
		case PlayerKarma:
			if mt.ev.PlayerKarma != nil {
				mt.ev.PlayerKarma(mt, v)
			}
		case GMSError:
			m.log.Logf(who, "match %d: host error %d (%s, fatal %v): %s", mt.ID, v.Code, ErrorName(v.Code), v.Fatal, v.Text)
			if !v.Fatal {
				o.Errors++
				return
			}
			// H5: the host gives up (its watchdog saw the tick stall, or a step failed in every run): the
			// match ends as aborted with this reason, unless finishGame came first; the host exits by
			// itself (logoutGMS and its exit follow), else it is killed after the exit timeout
			if o.Error == nil {
				e := v
				o.Error = &e
			}
			if abortCause == "" && !o.Finished {
				abortCause = fmt.Sprintf("host error %d (%s): %s", v.Code, ErrorName(v.Code), v.Text)
			}
			if !ending {
				ending = true
				setTimer(cfg.ExitTimeout, fmt.Sprintf("no exit within %v of a fatal reportGMSError", cfg.ExitTimeout))
			}
		case LogoutGMS:
			if !ending {
				ending = true
				setTimer(cfg.ExitTimeout, fmt.Sprintf("no exit within %v of logoutGMS", cfg.ExitTimeout))
			}
		default:
			m.log.Logf(who, "match %d: (%s from the host, ignored)", mt.ID, msg.Type())
		}
	}
	drain := func() {
		for {
			select {
			case msg := <-msgs:
				handle(msg)
			default:
				return
			}
		}
	}
	dropConn := func() {
		if conn != nil {
			conn.c.Close()
		}
		conn, msgs, gone = nil, nil, nil
	}

	var exit exitInfo
loop:
	for {
		select {
		case cc := <-mt.attach:
			if conn != nil || phase != waitHello || killed {
				m.log.Logf(who, "match %d: second control connection rejected", mt.ID)
				cc.c.Close()
				continue
			}
			conn, msgs, gone = cc, cc.msgs, cc.gone
			m.log.Logf(who, "match %d: hello from pid %d (%s), %.1f s after the start", mt.ID, cc.hello.PID,
				cc.hello.Build, time.Since(mt.started).Seconds())
			if cc.hello.PID != 0 && cc.hello.PID != mt.proc.Pid() {
				m.log.Logf(who, "match %d: hello names pid %d, the process we started is %d", mt.ID, cc.hello.PID, mt.proc.Pid())
			}
			if m.warmReady(mt) { // H1-live: a warm host waits for its game (no timer: the connection is its liveness)
				phase = idle
				setTimer(time.Duration(1<<62), "")
				m.log.Logf(who, "match %d: warm host ready, waiting for a game", mt.ID)
				continue
			}
			phase = waitReady
			setTimer(cfg.ReadyTimeout, fmt.Sprintf("no gameReady within %v", cfg.ReadyTimeout))
			send(mt.Game)
		case msg := <-msgs:
			handle(msg)
		case <-gone:
			drain()
			if conn.err != nil && !errors.Is(conn.err, net.ErrClosed) {
				m.log.Logf(who, "match %d: control connection ended: %v", mt.ID, conn.err)
			} else {
				m.log.Logf(who, "match %d: control connection closed", mt.ID)
			}
			dropConn()
			if phase == idle && !unassignedWarm && m.leaveWarm(mt) {
				unassignedWarm = true // no game can come any more
			}
			if !ending { // a crashing host drops the connection just before it exits: give it a moment
				ending = true
				setTimer(exitDrain, "control connection lost")
			}
		case cmd := <-mt.cmds:
			switch v := cmd.(type) {
			case assignGame: // H1-live: Start gave this warm host a game (mt.Game, mt.ev)
				if phase != idle || ending || killed {
					m.log.Logf(who, "match %d: (a game for a warm host that is not waiting: it ends)", mt.ID)
					continue
				}
				phase = waitReady
				since = time.Now()
				setTimer(cfg.ReadyTimeout, fmt.Sprintf("no gameReady within %v", cfg.ReadyTimeout))
				send(mt.Game)
			case RemovePlayer, PlayerRewards:
				send(v)
			case UpdateRoster: // B15: createGame (not sent yet: before the hello, or a warm host's) carries it then
				mt.Game.Roster = v.Roster
				if phase != waitHello && phase != idle {
					send(v)
				}
			case StartGame:
				mt.Game.Roster = v.Roster
				send(v)
			case AbortGame:
				if abortCause == "" {
					abortCause = "abortGame: " + v.Reason
				}
				if ending || killed {
					continue
				}
				ending = true
				if conn == nil && phase == waitHello {
					kill(abortCause)
					continue
				}
				send(v)
				setTimer(cfg.ExitTimeout, fmt.Sprintf("no exit within %v of abortGame", cfg.ExitTimeout))
			case shutdown:
				kill("server shutting down")
			}
		case <-timer.C:
			if killed {
				m.log.Logf(who, "match %d: pid %d %s; giving up on it", mt.ID, mt.proc.Pid(), timerWhat)
				break loop
			}
			kill(timerWhat)
		case exit = <-exited:
			if conn != nil { // the rest of what the host wrote before it exited
				grace := time.NewTimer(exitDrain)
			wait:
				for {
					select {
					case msg := <-msgs:
						handle(msg)
					case <-gone:
						drain()
						break wait
					case <-grace.C:
						break wait
					}
				}
				grace.Stop()
				dropConn()
			}
			o.ExitCode = exit.code
			break loop
		}
	}
	dropConn()
	mt.site.pool.Release(mt.Port)
	m.mu.Lock()
	delete(m.matches, mt.ID)
	m.mu.Unlock()

	// H1-live: a warm host that ended without a game has no events; the pool gets a replacement
	if unassignedWarm || m.leaveWarm(mt) {
		ready := phase != waitHello
		when := "before its hello"
		if ready {
			when = "after its hello"
		}
		m.log.Logf(who, "match %d: warm host ended without a game after %.1f s (%s; exit code %d, killed %v); port %d free",
			mt.ID, time.Since(mt.started).Seconds(), when, exit.code, o.Killed, mt.Port)
		close(mt.done)
		m.warmEnded(ready)
		return
	}

	o.Duration = time.Since(since)
	switch {
	case o.Finished:
		o.Reason = o.Result.Reason
	case abortCause != "":
		o.Reason = abortCause
	case exit.err != nil: // the end wasn't seen: an agent lost (remote.go)
		o.Reason = exit.err.Error()
	default:
		o.Reason = fmt.Sprintf("exited with code %d without finishGame", o.ExitCode)
	}
	result := "aborted"
	if o.Finished {
		result = fmt.Sprintf("finished (winner team %d)", o.Result.WinnerTeam)
	}
	if exit.err != nil {
		m.log.Logf(who, "match %d: (wait: %v)", mt.ID, exit.err)
	}
	m.log.Logf(who, "match %d: ended after %.1f s: %s: %s; exit code %d, killed %v; port %d free", mt.ID,
		o.Duration.Seconds(), result, o.Reason, o.ExitCode, o.Killed, mt.Port)
	mt.outcome = o
	if mt.ev.End != nil {
		mt.ev.End(mt, o)
	}
	close(mt.done)
}
