package launcher

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/logging"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/telemetry"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/version"
	"dawngate/server/internal/web"
)

// Start-Client's front: the server picker's side on the player's PC. The game's copy is patched for the tunnel
// address (127.0.0.1) as with the tunnel, but the game talks to the front first, which answers the redirector,
// Util::preAuth, Util::ping, QoS and the telemetry rules itself, so the login screen appears with no server
// connected. The login name carries the picked server as <name>#<tag> ('#' is no login-name character). At
// Authentication::login the front starts a tunnel session to that server's gateway (its key pinned; a new one confirmed once),
// replays the client's own preAuth and ping requests to the server's Blaze (the same message ids, so the ids stay
// aligned), drops their replies, sends the login without the tag and from then on passes the connection through.
// The tunnel's other ports (HTTP, telemetry, QoS, the match hosts' UDP) go to the current session; before the first
// login the front answers them itself. A logout makes the client go through the redirector again, which is the
// front's: the player picks again. A lost connection's UserSessions::resumeSession goes to the current session.

// FrontPorts are the ports the front serves for the game on the tunnel address. The redirector's port is compiled
// into the client, Blaze's is what the front's redirector names and QoS's what its preAuth names, HTTP's and
// telemetry's are the config packages' (the player copy's patch): the game's own ports, whatever the servers use.
type FrontPorts struct{ Redirector, Blaze, QoS, HTTP, Telemetry int }

// DefaultFrontPorts are the game's ports in a player copy.
var DefaultFrontPorts = FrontPorts{Redirector: RedirectorPort, Blaze: 10041, QoS: 17502, HTTP: HTTPPort, Telemetry: 9988}

// The front's login errors: the Fire error codes of its error reply to Authentication::login. The client shows
// the BlazeErrors line of (code << 16) | 1 in its LOGIN ERROR panel (the texts: setup/localepatch's ErrorLines,
// added to every player copy). The codes are Authentication codes the client names no error for (its AUTH_ERR_ names
// end at 0x00D4, the next is 0x012C).
const (
	// FrontErrUnknownServer: the tag names no server in the list, or the list is empty.
	FrontErrUnknownServer uint16 = 0x00E0
	// FrontErrUnreachable: the server's tunnel didn't answer within the connect timeout (10 s).
	FrontErrUnreachable uint16 = 0x00E1
	// FrontErrKeyChanged: the server's tunnel key is another than the one trusted before.
	FrontErrKeyChanged uint16 = 0x00E2
	// FrontErrNoGateway: something answers at the address, but no Dawngate tunnel (a server without tunnel = on).
	FrontErrNoGateway uint16 = 0x00E3
	// FrontErrServer: the tunnel works, but the server's redirector or Blaze didn't answer the replayed requests.
	FrontErrServer uint16 = 0x00E4
	// FrontErrRemoved: a login as <name>#-<tag> removed that server from the list (the picker's Remove).
	FrontErrRemoved uint16 = 0x00E5
	// FrontErrBadAddress: a login as <name>#+<address> named no valid server address (the picker's Add).
	FrontErrBadAddress uint16 = 0x00E6
	// FrontErrNewKey: the server's key isn't trusted yet (confirm once): the list shows its fingerprint, and the next
	// login to the same server and key in this run trusts it.
	FrontErrNewKey uint16 = 0x00E7
	// FrontErrFixed: a login as <name>#-pc: This PC is always in the list.
	FrontErrFixed uint16 = 0x00E8
	// FrontErrThisPCStarting: the server on this PC is still being set up or starting (longer than the login waits):
	// the list shows its progress, and a later login goes through.
	FrontErrThisPCStarting uint16 = 0x00E9
	// FrontErrThisPCFailed: the server on this PC couldn't be set up or started (Start-Client's console says why).
	FrontErrThisPCFailed uint16 = 0x00EA
	// FrontErrVersion: the server runs another version than this Start-Client, or names none (a release before the
	// version check, or no answer to the question): the list shows both versions. This PC is never refused.
	FrontErrVersion uint16 = 0x00EB
	// FrontErrAdded: a login as <name>#*<address> (the picker's ADD) added that server to the list (or found it there
	// already); no login is made.
	FrontErrAdded uint16 = 0x00EC
)

// frontError is a refused login: its code and the log's text.
type frontError struct {
	code uint16
	msg  string
}

func (e *frontError) Error() string { return e.msg }

func frontErr(code uint16, format string, a ...any) *frontError {
	return &frontError{code: code, msg: fmt.Sprintf(format, a...)}
}

const frontWho = "front"

// Front is Start-Client's front (Start, Close).
type Front struct {
	Log       *logging.Log // nil: no log
	Layout    setup.Layout // the servers' trusted tunnel keys (TrustedGateway)
	Bookmarks *Bookmarks
	// TunnelIP is the loopback address the player copy is patched for.
	TunnelIP net.IP
	// Ports are the game's ports (zero: DefaultFrontPorts).
	Ports FrontPorts
	// RedirectorTLS ends the game's TLS 1.1 to the redirector (this PC's own set for TunnelIP, tunnelCertSet).
	RedirectorTLS *tls.Config
	// Default is the server (tag, address or name) a login without a tag goes to before the first login; after it
	// such a login goes to the current server, without any session to the last one used.
	Default string
	// ConnectTimeout bounds a server's tunnel connect (the probe, the session; 0: 10 s), ReplyTimeout the server's
	// answers to the replayed requests (0: 20 s; the client gives a request 80 s).
	ConnectTimeout, ReplyTimeout time.Duration
	// ProbeTimeout bounds the server list's probes (0: 2 s).
	ProbeTimeout time.Duration
	// ListRefresh is how often the server list is probed and written again while the login screen shows (0: 15 s;
	// negative: never, only at the start and after an add, a remove or a failed connect).
	ListRefresh time.Duration
	Now         func() time.Time
	// ThisPC is the list's fixed entry This PC (thispc.go; nil: none, as in the lanes' front): a login to it starts
	// the server on this PC when needed, waiting up to ThisPCWait (0: the const ThisPCWait).
	ThisPC     *thisPC
	ThisPCWait time.Duration
	// Version is this Start-Client's version ("": version.String()): a login to a server of another version, or of
	// none it can tell, is refused with FrontErrVersion (This PC never).
	Version string
	// Presence follows the relayed Blaze frames for the player's Discord status (presence.go; nil: none). It only
	// reads them.
	Presence *presenceTracker
	// DiscordJoin turns Join from Discord on (front_join.go; needs Presence): the player's join code in the activity,
	// a friend's Join taken (DiscordJoinSecret). JoinLoopback lets a loopback server address into a join secret (the
	// test hook: two clients on one PC). JoinRefresh and JoinSettle are joinLoop's timings (0: the defaults).
	DiscordJoin, JoinLoopback bool
	// JoinAfterLists and JoinAnswerWait: the first invite's delay after the game's friends lists, the wait for the
	// game's answer before inviting again (0: the consts).
	JoinRefresh, JoinSettle, JoinAfterLists, JoinAnswerWait time.Duration

	join   frontJoin
	defMu  sync.Mutex // Default (setDefault, preselected)
	authed *blazeConn // the game's connection logged in on the current server (f.mu)

	qos       *web.QoS
	httpLocal *web.HTTP
	sink      *telemetry.Sink

	mu     sync.Mutex
	tcpLns map[int]net.Listener
	udpLns map[int]*net.UDPConn
	cur    *frontSession
	conns  map[net.Conn]struct{}
	closed bool
	done   chan struct{}
	wg     sync.WaitGroup

	switchMu sync.Mutex // one session switch at a time

	probeMu sync.Mutex
	probes  map[string]probeResult // the server list's recent probes, by gateway

	keyMu sync.Mutex
	shown map[string]tunnel.Pin // confirm once: a new key a login was refused for, by server address

	pageMu    sync.Mutex
	pageToken string         // the confirm form's token (front_pages.go): random per run
	slots     map[string]int // the servers' link slots by tag (slotOf), kept for the run
	slotTags  []string       // the tags by slot
	lastSts   []serverStatus // the statuses of the list written last (the pages' server list)

	// Maps are the create dialog's entries of the maps folder beside the programs (folderDialogMaps): the dialog's list
	// per server (writeMapList) offers them too when the copy has their archives. Set before Start; SetMaps later
	// (the map editor's Test, after it set the copy up again).
	Maps []moviepatch.Map
	// MapDownload is map delivery (front_mapdist.go; nil: off): at a login, the server's maps the copy lacks.
	MapDownload *MapDownload
	mapDL       frontMapDL

	listMu     sync.Mutex
	listCopy   string // the player copy the server list is written into (WriteServerList)
	atLogin    bool   // the login screen shows: from the start and each redirector request to a login that went through
	writeMu    sync.Mutex
	lastList   []byte        // the list text written last
	lastStates string        // its servers' states, for the log
	refresh    chan struct{} // asks refreshLoop for a new list now
}

// frontSession is a tunnel session to one server, with the server's own ports where they differ from the game's.
type frontSession struct {
	b Bookmark
	c *tunnel.Client
	// facts are the gateway's facts from the connect's probe (tunnel.FactMaps: the create dialog's maps; nil: none)
	facts map[string]string

	mu         sync.Mutex
	blaze, qos int // the server's Blaze port (from its redirector) and QoS port (from its preAuth); 0: not known
}

func (s *frontSession) ports() (blaze, qos int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blaze, s.qos
}

// serverPort is the server's port for one of the game's ports.
func (s *frontSession) serverPort(f *Front, local int) int {
	blaze, qos := s.ports()
	switch {
	case local == f.Ports.Blaze && blaze != 0:
		return blaze
	case local == f.Ports.QoS && qos != 0:
		return qos
	}
	return local
}

// localPort is the game's port for one of the server's ports.
func (s *frontSession) localPort(f *Front, server int) int {
	blaze, qos := s.ports()
	switch {
	case server == qos && qos != 0:
		return f.Ports.QoS
	case server == blaze && blaze != 0:
		return f.Ports.Blaze
	}
	return server
}

func (f *Front) logf(format string, a ...any) {
	if f.Log != nil {
		f.Log.Logf(frontWho, format, a...)
	}
}

// Start listens on the tunnel address at the game's ports and serves until Close.
func (f *Front) Start() error {
	if f.TunnelIP == nil {
		f.TunnelIP = net.ParseIP(DefaultTunnelAddress)
	}
	f.TunnelIP = f.TunnelIP.To4()
	if f.TunnelIP == nil || !f.TunnelIP.IsLoopback() {
		return errors.New("the front's address must be an IPv4 loopback address")
	}
	if f.Ports == (FrontPorts{}) {
		f.Ports = DefaultFrontPorts
	}
	if f.RedirectorTLS == nil {
		return errors.New("the front has no redirector certificate set")
	}
	if f.Bookmarks == nil {
		f.Bookmarks = &Bookmarks{path: BookmarksPath(f.Layout)}
	}
	if f.ConnectTimeout == 0 {
		f.ConnectTimeout = 10 * time.Second
	}
	if f.ReplyTimeout == 0 {
		f.ReplyTimeout = 20 * time.Second
	}
	if f.ProbeTimeout == 0 {
		f.ProbeTimeout = 2 * time.Second
	}
	if f.Now == nil {
		f.Now = time.Now
	}
	if f.Log == nil {
		f.Log = logging.New(io.Discard)
	}
	if f.ListRefresh == 0 {
		f.ListRefresh = 15 * time.Second
	}
	if f.ThisPCWait == 0 {
		f.ThisPCWait = ThisPCWait
	}
	if f.Version == "" {
		f.Version = version.String()
	}
	if f.ThisPC != nil {
		f.ThisPC.logf, f.ThisPC.changed = f.logf, f.wantList
	}
	f.done = make(chan struct{})
	f.tcpLns, f.udpLns, f.conns, f.probes = map[int]net.Listener{}, map[int]*net.UDPConn{}, map[net.Conn]struct{}{}, map[string]probeResult{}
	f.refresh = make(chan struct{}, 1)
	f.pageToken = randomToken()
	f.listMu.Lock()
	f.atLogin = true
	f.listMu.Unlock()
	// the local answers first: the listeners serve at once
	qosUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: f.TunnelIP, Port: f.Ports.QoS})
	if err != nil {
		return fmt.Errorf("UDP port %d on %s is in use (a Dawngate server or another Start-Client on this PC?): %w", f.Ports.QoS, f.TunnelIP, err)
	}
	f.qos = web.NewQoS(f.Log, f.TunnelIP.String(), qosUDP)
	f.httpLocal = &web.HTTP{Log: f.Log}
	f.sink = &telemetry.Sink{Log: f.Log}
	f.mu.Lock()
	f.udpLns[f.Ports.QoS] = qosUDP
	f.wg.Add(1)
	go f.udpLoop(f.Ports.QoS, qosUDP)
	f.mu.Unlock()
	for _, p := range []int{f.Ports.Redirector, f.Ports.Blaze, f.Ports.QoS, f.Ports.HTTP, f.Ports.Telemetry} {
		if err := f.listenTCP(p); err != nil {
			f.Close()
			return err
		}
	}
	f.wg.Add(1)
	go f.refreshLoop()
	if f.DiscordJoin && f.Presence != nil {
		f.join.wake = make(chan struct{}, 1)
		f.Presence.mu.Lock()
		f.Presence.changed = f.wakeJoin
		f.Presence.mu.Unlock()
		f.wg.Add(1)
		go f.joinLoop()
	}
	f.logf("the server picker's front on %s: redirector %d, Blaze %d, QoS %d (TCP and UDP), HTTP %d, telemetry %d; %d server(s) in %s",
		f.TunnelIP, f.Ports.Redirector, f.Ports.Blaze, f.Ports.QoS, f.Ports.HTTP, f.Ports.Telemetry, len(f.Bookmarks.List()), f.Bookmarks.path)
	return nil
}

// Close stops the front: its listeners, the game's connections and the current session.
func (f *Front) Close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	if f.done != nil {
		close(f.done)
	}
	for _, ln := range f.tcpLns {
		ln.Close()
	}
	for _, ln := range f.udpLns {
		ln.Close()
	}
	for c := range f.conns {
		c.Close()
	}
	s := f.cur
	f.cur = nil
	f.mu.Unlock()
	if s != nil {
		s.c.Close()
	}
	f.wg.Wait()
}

// current is the current session (nil before the first login, or when it failed).
func (f *Front) current() *frontSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}

// Current is the server of the current session (false: none).
func (f *Front) Current() (Bookmark, bool) {
	if s := f.current(); s != nil {
		return s.b, true
	}
	return Bookmark{}, false
}

func (f *Front) listenTCP(port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errClosedFront
	}
	if f.tcpLns[port] != nil {
		return nil
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort(f.TunnelIP.String(), strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("TCP port %d on %s is in use (a Dawngate server or another Start-Client on this PC?): %w", port, f.TunnelIP, err)
	}
	f.tcpLns[port] = ln
	f.wg.Add(1)
	go f.acceptLoop(port, ln)
	return nil
}

func (f *Front) listenUDP(port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errClosedFront
	}
	if f.udpLns[port] != nil {
		return nil
	}
	ln, err := net.ListenUDP("udp4", &net.UDPAddr{IP: f.TunnelIP, Port: port})
	if err != nil {
		return fmt.Errorf("UDP port %d on %s is in use (a Dawngate server or another Start-Client on this PC?): %w", port, f.TunnelIP, err)
	}
	f.udpLns[port] = ln
	f.wg.Add(1)
	go f.udpLoop(port, ln)
	return nil
}

var errClosedFront = errors.New("the front is closed")

// track adds a game connection to the ones Close closes (false: the front is closed), or removes it.
func (f *Front) track(c net.Conn, add bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !add {
		delete(f.conns, c)
		return true
	}
	if f.closed {
		return false
	}
	f.conns[c] = struct{}{}
	return true
}

func (f *Front) acceptLoop(port int, ln net.Listener) {
	defer f.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-f.done:
				return
			default:
			}
			continue
		}
		if !f.track(c, true) {
			c.Close()
			return
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			defer f.track(c, false)
			defer c.Close()
			f.serveTCP(port, c)
		}()
	}
}

// serveTCP serves one of the game's connections by its port.
func (f *Front) serveTCP(port int, c net.Conn) {
	switch port {
	case f.Ports.Redirector:
		// always the front's: the client goes through the redirector at its start and after every logout
		f.setAtLogin(true)
		(&redirector.Server{Log: f.Log, Host: f.TunnelIP.String(), BlazePort: f.Ports.Blaze}).ServeConn(tls.Server(c, f.RedirectorTLS))
	case f.Ports.Blaze:
		f.serveBlaze(c)
	case f.Ports.QoS:
		f.servePiped(port, c, f.qos.ServeConn)
	case f.Ports.HTTP:
		f.serveHTTP(c)
	case f.Ports.Telemetry:
		f.serveTelemetry(c)
	default:
		f.servePiped(port, c, nil) // a port only a session named
	}
}

// servePiped passes a connection to the current session's server, or serves it locally (local; nil: closes it)
// when there is none.
func (f *Front) servePiped(port int, c net.Conn, local func(net.Conn)) {
	if s := f.current(); s != nil {
		up, err := s.c.OpenStream(s.serverPort(f, port), tcpAddr(c.RemoteAddr()))
		if err == nil {
			tunnel.Pipe(c, up)
			return
		}
		f.logf("port %d: no stream to %s (%v)", port, s.b.Tag, err)
	}
	if local != nil {
		local(c)
	}
}

func tcpAddr(a net.Addr) *net.TCPAddr {
	t, _ := a.(*net.TCPAddr)
	return t
}

// replayConn is a connection whose first bytes were read already: Read gives them again, then the rest.
type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// CloseWrite passes a half close on (tunnel.Pipe).
func (c *replayConn) CloseWrite() error {
	if t, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return t.CloseWrite()
	}
	return c.Conn.Close()
}

// serveHTTP serves the game's HTTP port: the login screen's pages (/dawngate/<page>, the picker's
// /dawngate/pick/<slot>/<page>) are the front's (front_pages.go); the rest goes through the current session, and
// before a login to dgserver's built-in answers (the telemetry rules), else 404.
func (f *Front) serveHTTP(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReaderSize(c, 8192)
	line, err := br.ReadSlice('\n')
	if err != nil && len(line) == 0 {
		return
	}
	first := append([]byte(nil), line...)
	rest := io.MultiReader(bytes.NewReader(first), br)
	if isPagePath(first) {
		f.servePage(c, bufio.NewReader(rest))
		return
	}
	// the game's crash and error reports (CrashRpt: a memory dump, the game's log and preferences, pictures of every
	// screen) stay on this PC: the front answers them itself and never passes them to the server
	if isCrashReportPath(first) {
		f.httpLocal.ServeConn(&replayConn{Conn: c, r: rest})
		return
	}
	c.SetReadDeadline(time.Time{})
	f.servePiped(f.Ports.HTTP, &replayConn{Conn: c, r: rest}, f.httpLocal.ServeConn)
}

// isCrashReportPath: the request line's target is under the config packages' crash-report URL (/crash/).
func isCrashReportPath(line []byte) bool {
	f := strings.Fields(string(line))
	return len(f) >= 2 && strings.HasPrefix(pathOf(f[1]), "/crash/")
}

// serveTelemetry carries the game's telemetry connection (it stays open across logins) packet by packet to the
// current session's server: a new session gets a stream of its own, which first gets the connection's first
// packet again (the anonymous login token). Without a session the packets are logged here and dropped.
func (f *Front) serveTelemetry(c net.Conn) {
	peer := logging.Peer(c.RemoteAddr())
	src := tcpAddr(c.RemoteAddr())
	f.logf("telemetry connection from %s", peer)
	var (
		pending, first []byte
		up             net.Conn
		upSess         *frontSession
		raw            bool
		sent, dropped  int
	)
	defer func() {
		if up != nil {
			up.Close()
		}
		f.logf("telemetry %s closed: %d packet(s) passed on, %d logged here and dropped", peer, sent, dropped)
	}()
	forward := func(pkt []byte, p *telemetry.Packet, isFirst bool) {
		s := f.current()
		if s == nil {
			if up != nil {
				up.Close()
				up, upSess = nil, nil
			}
			if p != nil {
				f.sink.LogPacket(peer, *p)
			}
			dropped++
			return
		}
		if up == nil || upSess != s {
			if up != nil {
				up.Close()
			}
			var err error
			if up, err = s.c.OpenStream(s.serverPort(f, f.Ports.Telemetry), src); err != nil {
				up, upSess = nil, nil
				dropped++
				return
			}
			upSess = s
			go io.Copy(io.Discard, up) // the sink never answers
			if first != nil && !isFirst {
				if _, err := up.Write(first); err != nil {
					up.Close()
					up, upSess = nil, nil
					dropped++
					return
				}
			}
		}
		if _, err := up.Write(pkt); err != nil {
			up.Close()
			up, upSess = nil, nil
			dropped++
			return
		}
		sent++
	}
	buf := make([]byte, 65536)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			for !raw {
				p, rest, ok, perr := telemetry.CutPacket(pending)
				if perr != nil {
					raw = true // no packet framing: the rest passes on as it comes
					break
				}
				if !ok {
					break
				}
				pkt := append([]byte(nil), pending[:p.Size()]...)
				pending = rest
				isFirst := first == nil
				if isFirst {
					first = pkt
				}
				forward(pkt, &p, isFirst)
			}
			if raw && len(pending) > 0 {
				forward(pending, nil, false)
				pending = nil
			}
		}
		if err != nil {
			return
		}
	}
}

// udpLoop carries the game's datagrams to one port: to the current session's server, or before a login to the
// front's own QoS ping site.
func (f *Front) udpLoop(port int, ln *net.UDPConn) {
	defer f.wg.Done()
	buf := make([]byte, 65536)
	for {
		n, src, err := ln.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-f.done:
				return
			default:
			}
			continue
		}
		if s := f.current(); s != nil {
			s.c.SendDatagram(s.serverPort(f, port), src, buf[:n])
			continue
		}
		if port == f.Ports.QoS {
			f.qos.Packet(append([]byte(nil), buf[:n]...), src)
		}
	}
}

// deliverFor sends a session's datagrams from the gateway to the game, from the game's port they belong to.
func (f *Front) deliverFor(s *frontSession) func(port int, dst *net.UDPAddr, payload []byte) {
	return func(port int, dst *net.UDPAddr, payload []byte) {
		f.mu.Lock()
		ln, cur := f.udpLns[s.localPort(f, port)], f.cur
		f.mu.Unlock()
		if ln != nil && cur == s {
			ln.WriteToUDP(payload, dst)
		}
	}
}

// ensureListeners listens on every port a session's server names that the front doesn't serve yet (the match
// hosts' UDP ports; a server's own port numbers where they differ from the game's).
func (f *Front) ensureListeners(s *frontSession) {
	tcp, udp := s.c.Ports()
	for _, p := range tcp {
		if lp := s.localPort(f, p); lp != f.Ports.Redirector {
			if err := f.listenTCP(lp); err != nil {
				f.logf("%v", err)
			}
		}
	}
	for _, p := range udp {
		if err := f.listenUDP(s.localPort(f, p)); err != nil {
			f.logf("%v", err)
		}
	}
}

// session returns a session to a server: the current one when it is that server's and up, else a new one, which
// becomes the current one (the last is closed).
func (f *Front) session(b Bookmark) (*frontSession, error) {
	f.switchMu.Lock()
	defer f.switchMu.Unlock()
	if cur := f.current(); cur != nil && cur.b.Tag == b.Tag && cur.b.Gateway() == b.Gateway() && cur.c.Up() {
		return cur, nil
	}
	s, err := f.connect(b)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		s.c.Close()
		return nil, errClosedFront
	}
	old := f.cur
	f.cur = s
	f.mu.Unlock()
	if old != nil {
		f.logf("session to %s (%s) closed: the player picked %s", old.b.Tag, old.b.Gateway(), b.Tag)
		old.c.Close()
	}
	f.ensureListeners(s)
	return s, nil
}

// dialerFor is the dialer of a server's probes: a gateway on this PC (tests) is dialled from the tunnel address,
// as tunnel.Client does.
func (f *Front) dialerFor(address string) *net.Dialer {
	if ip := net.ParseIP(address); ip != nil && ip.IsLoopback() {
		return &net.Dialer{LocalAddr: &net.TCPAddr{IP: f.TunnelIP}}
	}
	return nil
}

// connect probes a server's gateway (its key and its version), checks the version (checkVersion: the same as this
// Start-Client's) and the key (trustKey: a new one confirmed once) and starts a session.
func (f *Front) connect(b Bookmark) (*frontSession, error) {
	gw := b.Gateway()
	start := time.Now()
	info, err := tunnel.ProbeInfo(gw, f.ConnectTimeout, f.dialerFor(b.Address))
	served := info.Pin
	r := probeResult{at: time.Now(), reachable: err == nil, ping: time.Since(start), pin: served, version: info.Version, versionErr: info.VersionErr,
		facts: info.Facts}
	if err != nil {
		r.problem = f.classifyDial(b, err, f.ConnectTimeout)
	}
	f.recordProbe(b, r) // the list shows what the login found
	if err != nil {
		f.wantList()
		return nil, r.problem
	}
	if err := f.checkVersion(b, r); err != nil {
		f.wantList()
		return nil, err
	}
	if err := f.trustKey(b, served); err != nil {
		f.wantList()
		return nil, err
	}
	s := &frontSession{b: b, facts: r.facts}
	s.c = &tunnel.Client{Log: f.Log, Gateway: gw, Pin: served, TunnelIP: f.TunnelIP, External: true, DialTimeout: f.ConnectTimeout,
		Release: f.releaseFor(b, r.version), Maps: f.installedMaps()}
	s.c.Deliver = f.deliverFor(s)
	if err := s.c.Start(); err != nil {
		if errors.Is(err, tunnel.ErrPinMismatch) {
			return nil, frontErr(FrontErrKeyChanged, "%s's tunnel key changed during the connect: %v", gw, err)
		}
		if tunnel.ReleaseRefused(err) {
			return nil, frontErr(FrontErrVersion, "%s (%s) refused this Start-Client's release %s: %v (its version: %q)", b.Tag, gw,
				s.c.Release, err, r.version)
		}
		return nil, frontErr(FrontErrUnreachable, "the tunnel to %s didn't start: %v", gw, err)
	}
	f.logf("session to %s (%s, key %s) in %s", b.Tag, gw, served.Fingerprint(), time.Since(start).Round(time.Millisecond))
	go func() {
		if s.c.WaitUDP(3 * time.Second) {
			f.logf("session to %s: UDP works (%s)", b.Tag, s.c.UDPEchoAfter().Round(time.Millisecond))
		} else {
			f.logf("session to %s: WARNING: no UDP answer within 3 s; does its router forward UDP %d too?", b.Tag, b.GatewayPort)
		}
	}()
	return s, nil
}

// classifyDial words a failed probe: no answer at all (unreachable), or an answer that is no Dawngate tunnel.
func (f *Front) classifyDial(b Bookmark, err error, timeout time.Duration) *frontError {
	gw := b.Gateway()
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return frontErr(FrontErrUnreachable, "no answer from %s within %s: %v", gw, timeout, err)
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		// nothing at the gateway's port: a server without the tunnel still answers at the redirector's
		if Answers(b.Address, RedirectorPort, 2*time.Second) == nil {
			return frontErr(FrontErrNoGateway, "%s answers at the redirector's port %d but runs no tunnel at %d (%v)", b.Address, RedirectorPort, b.GatewayPort, err)
		}
		return frontErr(FrontErrUnreachable, "nothing answers at %s: %v", gw, err)
	}
	return frontErr(FrontErrNoGateway, "%s answers, but with no Dawngate tunnel: %v", gw, err)
}

// pinState compares a served key with the one trusted for a server: "ok", "new" (none trusted yet) or "changed".
func (f *Front) pinState(b Bookmark, served tunnel.Pin) (string, tunnel.Pin) {
	raw, err := os.ReadFile(TrustedGateway(f.Layout, b.Address))
	if err != nil {
		return "new", tunnel.Pin{}
	}
	known, err := tunnel.ParsePin(strings.TrimSpace(string(raw)))
	if err != nil || !tunnel.SamePin(known, served) {
		return "changed", known
	}
	return "ok", known
}

// trustedPin is the key trusted for a server (false: none, or a file that doesn't parse).
func (f *Front) trustedPin(b Bookmark) (tunnel.Pin, bool) {
	raw, err := os.ReadFile(TrustedGateway(f.Layout, b.Address))
	if err != nil {
		return tunnel.Pin{}, false
	}
	known, err := tunnel.ParsePin(strings.TrimSpace(string(raw)))
	return known, err == nil
}

// Version states of a server in the list (versionState): the same version as this Start-Client's, another one, or
// none it could tell (a release before the version check). "" for a server not reached, and for This PC.
const (
	versionSame    = "same"
	versionOther   = "other"
	versionUnknown = "unknown"
)

// versionState compares a probe's version with this Start-Client's.
func (f *Front) versionState(b Bookmark, r probeResult) string {
	switch {
	case !r.reachable || (b.Tag == ThisPCTag && f.ThisPC != nil):
		return ""
	case r.version == "":
		return versionUnknown
	case version.Same(f.Version, r.version):
		return versionSame
	}
	return versionOther
}

// checkVersion refuses a login to a server whose version isn't this Start-Client's (FrontErrVersion): any difference,
// and a server that names none. This PC is never refused (the server on this PC is this folder's own program); a
// difference there is only logged.
func (f *Front) checkVersion(b Bookmark, r probeResult) error {
	if b.Tag == ThisPCTag && f.ThisPC != nil {
		if r.version != f.Version {
			f.logf("This PC (%s) runs version %q, this Start-Client %s: not refused (This PC never is)", b.Gateway(), r.version, f.Version)
		}
		return nil
	}
	switch f.versionState(b, r) {
	case versionSame:
		return nil
	case versionUnknown:
		return frontErr(FrontErrVersion, "%s (%s) doesn't tell its version (%v), so the login is refused: this Start-Client "+
			"is %s and plays only on servers of the same version", b.Tag, b.Gateway(), r.versionErr, f.Version)
	}
	return frontErr(FrontErrVersion, "%s (%s) runs version %s, this Start-Client is %s: both need the same version, so the "+
		"login is refused", b.Tag, b.Gateway(), r.version, f.Version)
}

// releaseFor is the release a session to a server names in its hello: this Start-Client's version for any server,
// and for This PC the version its gateway told (serverVersion): This PC is never refused, so a folder's running
// Start-Server of another release still lets its own Start-Client in. (A Start-Server of a release before the release
// check takes no version 2 hello at all: restarting it with the folder's release helps.)
func (f *Front) releaseFor(b Bookmark, serverVersion string) string {
	if b.Tag == ThisPCTag && f.ThisPC != nil && serverVersion != "" {
		return serverVersion
	}
	return f.Version
}

// trustKey checks a server's key against the trusted one and refuses a changed one (the player can't be asked during
// a login, so a changed key needs the old one removed by hand: its file). A key not trusted yet is confirmed once:
// the first login to it is refused with FrontErrNewKey while the login screen's list shows its fingerprint, and the
// next login to the same server presenting the same key (in this run) trusts it, as the console's question did.
func (f *Front) trustKey(b Bookmark, served tunnel.Pin) error {
	state, known := f.pinState(b, served)
	switch state {
	case "ok":
		return nil
	case "changed":
		return frontErr(FrontErrKeyChanged, "%s presents the tunnel key %s, not the one trusted before (%s; %s)", b.Gateway(),
			served.Fingerprint(), known.Fingerprint(), TrustedGateway(f.Layout, b.Address))
	}
	key := strings.ToLower(b.Address)
	f.keyMu.Lock()
	shown, seen := f.shown[key]
	if !seen || !tunnel.SamePin(shown, served) {
		if f.shown == nil {
			f.shown = map[string]tunnel.Pin{}
		}
		f.shown[key] = served
		f.keyMu.Unlock()
		return frontErr(FrontErrNewKey, "%s's tunnel key %s isn't trusted yet: the list shows its fingerprint, and the next "+
			"login to it trusts it (confirm once)", b.Gateway(), served.Fingerprint())
	}
	delete(f.shown, key)
	f.keyMu.Unlock()
	if _, err := TrustGateway(b.Address, served, TrustOptions{Store: TrustedGateway(f.Layout, b.Address), Accept: true}); err != nil {
		return frontErr(FrontErrServer, "storing %s's tunnel key: %v", b.Address, err)
	}
	f.logf("trusting %s's tunnel key %s from now on (confirmed: the second login to it; %s)", b.Address, served.Fingerprint(),
		TrustedGateway(f.Layout, b.Address))
	return nil
}

// resolve finds the server a login goes to: its tag (a tag, address or name of the list), else before the first
// login Default, else the current session's server, else the one used last.
func (f *Front) resolve(tag string) (Bookmark, error) {
	if tag != "" {
		if b, ok := f.Bookmarks.Find(tag); ok {
			return b, nil
		}
		return Bookmark{}, frontErr(FrontErrUnknownServer, "no server %q in the list (tags: %s)", tag, strings.Join(f.Bookmarks.sortedTags(), ", "))
	}
	if s := f.current(); s != nil {
		if b, ok := f.Bookmarks.Find(s.b.Tag); ok && strings.EqualFold(b.Address, s.b.Address) {
			return b, nil
		}
		// the current server was removed from the list: as before the first login
	}
	if def := f.preselected(); def != "" {
		if b, ok := f.Bookmarks.Find(def); ok {
			return b, nil
		}
	}
	if b, ok := f.Bookmarks.Last(); ok {
		return b, nil
	}
	return Bookmark{}, frontErr(FrontErrUnknownServer, "no server in the list")
}

// splitTag splits a login name typed as <name>#<pick> at its first '#' ('#' is no login-name character, so the name
// is everything before it; the pick, a tag or an add's display name, may hold more).
func splitTag(mail string) (name, tag string, tagged bool) {
	i := strings.IndexByte(mail, '#')
	if i < 0 {
		return mail, "", false
	}
	return mail[:i], strings.TrimSpace(mail[i+1:]), true
}
