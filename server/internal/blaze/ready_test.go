package blaze

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/uichannel"
)

// attachUI gives a test server the UI channel on a port of its own, and returns where to connect.
func attachUI(t *testing.T, ts *tcpServer) string {
	t.Helper()
	ui := &uichannel.Server{Log: ts.s.Log}
	ts.s.AttachUI(ui)
	ts.s.lobbyGap = 0 // every change its own broadcast; TestLobbyPublishGap has the gap
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go ui.Serve(ln)
	t.Cleanup(func() {
		ln.Close()
		ui.Close()
	})
	return ln.Addr().String()
}

// uiClient is a test's class in a lobby: tc's UI channel.
type uiClient struct {
	t  *testing.T
	nc net.Conn
	r  *bufio.Reader
}

// uiConnect opens tc's channel and says hello.
func uiConnect(t *testing.T, addr string, tc *tcpClient) *uiClient {
	t.Helper()
	nc, err := net.Dial("tcp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	u := &uiClient{t: t, nc: nc, r: bufio.NewReader(nc)}
	u.call(uichannel.CallHello, `{"protocol":1,"name":"`+tc.name+`"}`)
	u.want(`{"event":"hello","data":{"protocol":1}}`)
	return u
}

func (u *uiClient) call(name, args string) {
	u.t.Helper()
	line := `{"call":"` + name + `"`
	if args != "" {
		line += `,"args":` + args
	}
	if _, err := u.nc.Write([]byte(line + "}\n")); err != nil {
		u.t.Fatal(err)
	}
}

func (u *uiClient) read() (string, error) {
	s, err := u.r.ReadString('\n')
	return strings.TrimSuffix(s, "\n"), err
}

func (u *uiClient) want(line string) {
	u.t.Helper()
	u.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := u.read()
	if err != nil {
		u.t.Fatalf("want %s: %v", line, err)
	}
	if got != line {
		u.t.Errorf("got %s\nwant %s", got, line)
	}
}

// quiet expects no event for a moment.
func (u *uiClient) quiet() {
	u.t.Helper()
	u.nc.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
	got, err := u.read()
	var ne net.Error
	if err == nil || !errors.As(err, &ne) || !ne.Timeout() {
		u.t.Errorf("unexpected event %s (%v)", got, err)
	}
}

// lobbyStateLine is the lobby.state line for these players, each `name`, `name 0`, `name 1` or with a rating
// `name - 1612` / `name 1 1612`.
func lobbyStateLine(players ...string) string {
	var out []string
	for _, p := range players {
		f := strings.Fields(p)
		s := `{"name":"` + f[0] + `"`
		if len(f) > 1 && f[1] != "-" {
			s += `,"ready":` + map[string]string{"0": "false", "1": "true"}[f[1]]
		}
		if len(f) > 2 {
			s += `,"rating":` + f[2]
		}
		out = append(out, s+"}")
	}
	return `{"event":"lobby.state","data":{"players":[` + strings.Join(out, ",") + `]}}`
}

// The state object itself.
func TestReadyCheckObject(t *testing.T) {
	var c readyCheck
	if c.Has(1) || c.Ready(1) || c.Set(1, true) {
		t.Error("an empty check has a member")
	}
	c.Leave(1) // nothing to leave
	c.Join(1)
	if !c.Has(1) || c.Ready(1) {
		t.Error("a new member is ready or missing")
	}
	if !c.Set(1, true) || !c.Ready(1) {
		t.Error("Set didn't take")
	}
	c.Join(1)
	if c.Ready(1) {
		t.Error("joining again keeps the state")
	}
	c.Set(1, true)
	c.Leave(1)
	if c.Has(1) || c.Ready(1) || c.Set(1, true) {
		t.Error("a member who left is still in")
	}
}

// The custom lobby's ready check over the UI channel: only a player whose class entered counts, the admin and the
// spectators never do, a client without the class counts as ready, a team change and a rejoin clear a state, the whole
// lobby goes to everyone who entered at every change, and the admin's start is refused while a counted player isn't
// ready (RosterLockedKey "0" to all, lobby.startRefused to the admin). Nothing of it touches the chat.
func TestReadyCheck(t *testing.T) {
	ts := newTCPServer(t, nil)
	addr := attachUI(t, ts)
	g := golden.Load(t)
	a, b, c, d := ts.client("alpha@example.com"), ts.client("bravo@example.com"), ts.client("charlie@example.com"),
		ts.client("delta@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	join := golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, join))
	wantReply(t, "C joins", c.gm(CmdGameManagerJoinGame, join))
	wantReply(t, "D spectates", d.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_spectate_request"))))
	start := golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))
	state := func(bState string) string {
		return lobbyStateLine(a.name, strings.TrimSpace(b.name+" "+bState), c.name, d.name)
	}
	ua, ub, ud := uiConnect(t, addr, a), uiConnect(t, addr, b), uiConnect(t, addr, d) // C has no class

	// the admin enters: it hears the lobby; it doesn't count, nor does anyone who hasn't entered
	ua.call(CallLobbyEnter, "")
	ua.want(state(""))
	// B enters: it counts, not ready; everyone who entered hears it
	ub.call(CallLobbyEnter, "")
	ua.want(state("0"))
	ub.want(state("0"))
	// the spectator enters: it hears the lobby and doesn't count
	ud.call(CallLobbyEnter, "")
	ua.want(state("0"))
	ub.want(state("0"))
	ud.want(state("0"))

	// the start while B isn't ready: refused, the roster unlocked for all, the admin told why and sent its own
	// extended data (READY2b: its lobby's roster redraw)
	for _, x := range []*tcpClient{a, b, c, d} {
		x.takeAll(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 50*time.Millisecond)
	}
	wantError(t, "A starts, B not ready", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	for _, x := range []*tcpClient{a, b, c, d} {
		f := x.note(ComponentGameManager, NotifyGameAttribChangeID)
		if !strings.Contains(string(f.Payload), AttrRosterLocked) {
			t.Errorf("%s: NotifyGameAttribChange without %s", x.name, AttrRosterLocked)
		}
	}
	ua.want(`{"event":"lobby.startRefused"}`)
	if usid, _ := decode(t, a.note(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate)).Int("USID"); usid != a.id {
		t.Errorf("the admin's update after the refusal names %d, not %d", usid, a.id)
	}
	for _, x := range []*tcpClient{b, c, d} {
		x.none(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 0)
	}

	// B ready: everyone who entered hears it
	ub.call(CallLobbySetReady, `{"ready":true}`)
	for _, u := range []*uiClient{ua, ub, ud} {
		u.want(state("1"))
	}
	// the admin and a spectator don't count: their calls change nothing
	ud.call(CallLobbySetReady, `{"ready":true}`)
	ua.call(CallLobbySetReady, `{"ready":true}`)
	ub.call(CallLobbySetReady, `{"nonsense":1}`)
	ua.quiet()
	ub.quiet()
	ud.quiet()

	// a team change clears B's state
	hg := ts.s.hosted.get(1)
	hg.mu.Lock()
	other := int64(1) - hg.playerLocked(b.id).p.Team
	hg.mu.Unlock()
	swap := tdf.NewWriter().Int("GID", 1).List("LGAM", tdf.TypeStruct, []*tdf.Writer{tdf.NewWriter().Int("PID", b.id).Int("TIDX", other)}).Bytes()
	wantReply(t, "swap B", a.gm(CmdGameManagerSwapPlayersTeam, swap))
	for _, u := range []*uiClient{ua, ub, ud} {
		u.want(state("0"))
	}

	// B's rejoin (a new lobby entry): out of the check until its class enters again, in the state of the others too
	ub.call(CallLobbySetReady, `{"ready":true}`)
	for _, u := range []*uiClient{ua, ub, ud} {
		u.want(state("1"))
	}
	wantReply(t, "B rejoins", b.gm(CmdGameManagerJoinGame, join))
	for _, u := range []*uiClient{ua, ud} {
		u.want(state(""))
	}
	ub.quiet()
	ub.call(CallLobbyEnter, "")
	for _, u := range []*uiClient{ua, ub, ud} {
		u.want(state("0"))
	}
	ub.call(CallLobbySetReady, `{"ready":true}`)
	for _, u := range []*uiClient{ua, ub, ud} {
		u.want(state("1"))
	}

	// the chat is only the chat: such a line goes to everyone as what it is, and changes nothing
	gg := joinGG(t, a, b, c, d)
	line := msg(MessageTypeChat, MessageFlagEcho|MessageFlagFilter, ObjTypePlaygroup, chatLine("~#rdy 0~"), gg)
	wantOK(t, "chat", c.sendMsg(line))
	for _, x := range []*tcpClient{a, b, c, d} {
		x.msgNote(c)
	}
	ua.quiet()

	// C (no class) counts as ready, so the start goes
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, start))
	// after the start nothing changes any more, and nobody hears of it
	ub.call(CallLobbySetReady, `{"ready":false}`)
	ua.quiet()
	ub.quiet()

	log := ts.log.String()
	for _, want := range []string{
		"advanceGameState IN_GAME refused: not ready [2000000002]",
		"ready check: 2000000002 is ready true; waiting for []",
		"lobby.setReady: not counted (the admin or a spectator); ignored",
		`lobby.setReady: no ready in the arguments`,
		"lobby.setReady: not in a lobby",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log without %q", want)
		}
	}
}

// joinGG makes the clients members of game 1's chat playgroup GG-1, as their lobbies join it (B11), and returns its id.
func joinGG(t *testing.T, cs ...*tcpClient) int64 {
	t.Helper()
	gg := tdf.NewWriter().Int("JOIN", 1).Struct("PGRP", func(w *tdf.Writer) {
		w.Map("ATTR", tdf.TypeString, tdf.TypeString, tdf.KV(PlaygroupKeyAttr, "GG-1")).Int("MLIM", 15).String("NAME", "GG-1").String("UKEY", "GG-1")
	}).Bytes()
	var id int64
	for i, x := range cs {
		info, _ := decode(t, x.pgCall(CmdPlaygroupsCreatePlaygroup, gg)).Sub("INFO")
		id, _ = info.Int("PGID")
		x.note(ComponentPlaygroups, NotifyJoinPlaygroupID)
		for _, y := range cs[:i] {
			y.note(ComponentPlaygroups, NotifyMemberJoinedPlaygroupID)
		}
	}
	return id
}

// A player whose channel ends (its client gone, a lost connection) stops counting, so the lobby doesn't wait for it; so
// does one who logged out, whose channel ends with its login.
func TestReadyChannelEnds(t *testing.T) {
	ts := newTCPServer(t, nil)
	addr := attachUI(t, ts)
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	ua, ub := uiConnect(t, addr, a), uiConnect(t, addr, b)
	ua.call(CallLobbyEnter, "")
	ua.want(lobbyStateLine(a.name, b.name))
	ub.call(CallLobbyEnter, "")
	ua.want(lobbyStateLine(a.name, b.name+" 0"))
	ub.want(lobbyStateLine(a.name, b.name+" 0"))
	ub.nc.Close()
	ua.want(lobbyStateLine(a.name, b.name))
	wantReply(t, "A starts", a.gm(CmdGameManagerAdvanceGameState, golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))))
}

// A login's end ends its channel.
func TestUIEndsWithTheLogin(t *testing.T) {
	ts := newTCPServer(t, nil)
	addr := attachUI(t, ts)
	a := ts.client("alpha@example.com")
	ua := uiConnect(t, addr, a)
	if !ts.s.UI.Connected(a.id) {
		t.Fatal("not connected")
	}
	a.c.Close()
	ua.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := ua.read(); err == nil {
		t.Error("the channel outlived the login")
	} else {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Error("the channel outlived the login")
		}
	}
}

// tunnelConn is a stream as the gateway hands it to a service.
type tunnelConn struct {
	net.Conn
	local *tunnel.Addr
}

func (c tunnelConn) LocalAddr() net.Addr { return c.local }

// Who a connection is. Through the tunnel the session says it, whatever name the client gives; directly, the name must
// be logged in from the same address, every loopback address counting as one.
func TestIdentifyUI(t *testing.T) {
	ts := newTCPServer(t, nil)
	a := ts.client("alpha@example.com")
	ts.client("bravo@example.com")
	ip := net.IPv4(127, 0, 0, 1)
	dial := func(local string) net.Conn {
		t.Helper()
		ln, err := net.Listen("tcp4", "127.0.0.9:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		accepted := make(chan net.Conn, 1)
		go func() {
			c, _ := ln.Accept()
			accepted <- c
		}()
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(local)}}
		out, err := d.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { out.Close() })
		srv := <-accepted
		t.Cleanup(func() { srv.Close() })
		return srv
	}

	// direct: a logged-in name from a loopback address (another one than its Blaze connection's: the movie's socket
	// isn't bound), and nobody else
	if id, ok := ts.s.identifyUI(dial("127.0.0.1"), a.name); !ok || id != a.id {
		t.Errorf("direct: %d %v, want %d", id, ok, a.id)
	}
	if id, ok := ts.s.identifyUI(dial("127.0.0.5"), a.name); !ok || id != a.id {
		t.Errorf("direct from another loopback address: %d %v, want %d", id, ok, a.id)
	}
	if id, ok := ts.s.identifyUI(dial("127.0.0.1"), "nobody"); ok {
		t.Errorf("an unknown name was identified as %d", id)
	}
	if id, ok := ts.s.identifyUI(dial("127.0.0.1"), store.PersonaName("charlie@example.com")); ok {
		t.Errorf("a name that isn't logged in was identified as %d", id)
	}

	// through the tunnel: the session of a's Blaze connection, whatever it calls itself; another session is nobody
	conns := ts.s.connsOf(a.id)
	if len(conns) != 1 {
		t.Fatalf("%d connections", len(conns))
	}
	sess := tunnel.FakeSessionAddr(ip, 10041, "v", true)
	sess.Session = 77
	conns[0].tun = sess
	session := func(id uint64) net.Conn {
		other := tunnel.FakeSessionAddr(ip, uichannel.Port, "v", true)
		other.Session = id
		return tunnelConn{Conn: dial("127.0.0.1"), local: other}
	}
	if id, ok := ts.s.identifyUI(session(77), "somebody else"); !ok || id != a.id {
		t.Errorf("tunnel: %d %v, want %d", id, ok, a.id)
	}
	if id, ok := ts.s.identifyUI(session(78), a.name); ok {
		t.Errorf("another tunnel session was identified as %d", id)
	}

	// two logins in one session (the test lanes' two clients behind one tunnel): the name picks among them, and nobody
	// else can be named
	b, c := ts.client("bravo2@example.com"), ts.client("charlie2@example.com")
	ts.s.connsOf(b.id)[0].tun = sess
	if id, ok := ts.s.identifyUI(session(77), b.name); !ok || id != b.id {
		t.Errorf("two logins, by name: %d %v, want %d", id, ok, b.id)
	}
	if id, ok := ts.s.identifyUI(session(77), a.name); !ok || id != a.id {
		t.Errorf("two logins, by the other name: %d %v, want %d", id, ok, a.id)
	}
	if id, ok := ts.s.identifyUI(session(77), c.name); ok {
		t.Errorf("a login of another session was identified as %d", id)
	}
	if id, ok := ts.s.identifyUI(session(77), ""); ok {
		t.Errorf("two logins, no name: identified as %d", id)
	}

	// a direct connection never passes for a tunnelled login, even from that player's own address: a's connection is
	// a tunnelled one now, and its address (this test's loopback) is where the attacker comes from
	if id, ok := ts.s.identifyUI(dial("127.0.0.1"), a.name); ok {
		t.Errorf("a direct connection was identified as the tunnelled login %d", id)
	}
	if id, ok := ts.s.identifyUI(dial("127.0.0.1"), c.name); !ok || id != c.id {
		t.Errorf("a direct login: %d %v, want %d", id, ok, c.id)
	}
}

// A connection belongs to the login it came by: when that login ends the connection ends, also while the persona has
// another login elsewhere, whose connection stays.
func TestUIEndsWithItsOwnLogin(t *testing.T) {
	ts := newTCPServer(t, nil)
	attachUI(t, ts)
	a := ts.client("alpha@example.com")
	second := ts.dial("alpha@example.com") // the same persona's second login
	second.reply(1, 0x28, second.send(1, 0x28, tdf.NewWriter().String("MAIL", "alpha@example.com").String("PASS", testPassword).
		String("TOKN", "").Int("TYPE", 0).Bytes()))
	second.reply(1, 0x6E, second.send(1, 0x6E, tdf.NewWriter().String("PNAM", second.name).Bytes()))
	conns := ts.s.connsOf(a.id)
	if len(conns) != 2 {
		t.Fatalf("%d logins", len(conns))
	}
	sess := func(id uint64) *tunnel.Addr {
		x := tunnel.FakeSessionAddr(net.IPv4(127, 0, 0, 1), 10041, "v", true)
		x.Session = id
		return x
	}
	conns[0].tun, conns[1].tun = sess(1), sess(2)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	stream := func(id uint64) *uiClient {
		t.Helper()
		accepted := make(chan net.Conn, 1)
		go func() {
			c, _ := ln.Accept()
			accepted <- c
		}()
		out, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { out.Close() })
		go ts.s.UI.ServeConn(tunnelConn{Conn: <-accepted, local: sess(id)})
		u := &uiClient{t: t, nc: out, r: bufio.NewReader(out)}
		u.call(uichannel.CallHello, `{"protocol":1,"name":""}`)
		u.want(`{"event":"hello","data":{"protocol":1}}`)
		return u
	}
	first := stream(1)
	other := stream(2)

	// session 1's login ends; the persona is still online through session 2
	a.c.Close()
	first.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := first.read(); err == nil {
		t.Error("an event instead of the end")
	} else {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Error("the connection outlived its own login")
		}
	}
	if !ts.s.UI.Connected(a.id) {
		t.Fatal("the other login's connection ended too")
	}
	if !ts.s.UI.Send(a.id, "e", nil) {
		t.Fatal("Send: false")
	}
	other.want(`{"event":"e"}`)
}

// The same state again tells nobody anything, and a member's new connection gets the state without the others hearing it.
func TestLobbyNoChangeNoBroadcast(t *testing.T) {
	ts := newTCPServer(t, nil)
	addr := attachUI(t, ts)
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	ua, ub := uiConnect(t, addr, a), uiConnect(t, addr, b)
	ua.call(CallLobbyEnter, "")
	ua.want(lobbyStateLine(a.name, b.name))
	ub.call(CallLobbyEnter, "")
	notReady := lobbyStateLine(a.name, b.name+" 0")
	ua.want(notReady)
	ub.want(notReady)

	ub.call(CallLobbySetReady, `{"ready":false}`)
	ua.quiet()
	ub.quiet()
	ub.call(CallLobbyEnter, "") // only the caller hears
	ub.want(notReady)
	ua.quiet()
}

// However often players change their state, a lobby's players hear it at most once per gap, and the last state always
// reaches them.
func TestLobbyPublishGap(t *testing.T) {
	ts := newTCPServer(t, nil)
	addr := attachUI(t, ts)
	ts.s.lobbyGap = 100 * time.Millisecond
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	ua, ub := uiConnect(t, addr, a), uiConnect(t, addr, b)
	ua.call(CallLobbyEnter, "")
	ua.want(lobbyStateLine(a.name, b.name))
	ub.call(CallLobbyEnter, "")
	ua.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := ua.read(); err != nil {
		t.Fatal(err)
	}

	// a burst within the call budget, ending ready
	start := time.Now()
	for i := 0; i < 8; i++ {
		ub.call(CallLobbySetReady, `{"ready":`+map[bool]string{true: "true", false: "false"}[i%2 == 0]+`}`)
	}
	ub.call(CallLobbySetReady, `{"ready":true}`)
	final := lobbyStateLine(a.name, b.name+" 1")
	heard := 0
	for {
		ua.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := ua.read()
		if err != nil {
			t.Fatalf("after %d lines: %v", heard, err)
		}
		heard++
		if line == final && time.Since(start) >= 100*time.Millisecond {
			break
		}
	}
	if max := int(time.Since(start)/(100*time.Millisecond)) + 2; heard > max {
		t.Errorf("%d broadcasts in %v (at most %d)", heard, time.Since(start), max)
	}
}

// The channel's lines are JSON the movie's decoder reads; the state's shape is pinned here.
func TestLobbyStateJSON(t *testing.T) {
	yes, no := true, false
	got, err := json.Marshal(LobbyState{Players: []LobbyStatePlayer{
		{Name: "admin", Rating: 1612}, {Name: "ready", Ready: &yes}, {Name: "waiting", Ready: &no, Rating: 1388}, {Name: "spectator"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"players":[{"name":"admin","rating":1612},{"name":"ready","ready":true},{"name":"waiting","ready":false,"rating":1388},{"name":"spectator"}]}`
	if string(got) != want {
		t.Errorf("%s\nwant %s", got, want)
	}
}

// The test aid Server.TestRefuseStart (dgserver --test-refuse-start, READY2b): a custom game's first start is refused
// as the ready check's backstop refuses one (RosterLockedKey "0" to all, the admin's own extended data), the second
// goes.
func TestRefuseStartTestAid(t *testing.T) {
	ts := newTCPServer(t, func(s *Server) { s.TestRefuseStart = true })
	g := golden.Load(t)
	a, b := ts.client("alpha@example.com"), ts.client("bravo@example.com")
	wantReply(t, "create", a.gm(CmdGameManagerResetDedicatedServer, golden.Bytes(t, decodeCase(t, g, "resetdedicatedserver_custom_request"))))
	a.note(ComponentGameManager, NotifyGameSetupID)
	wantReply(t, "B joins", b.gm(CmdGameManagerJoinGame, golden.Bytes(t, decodeCase(t, g, "joingame_browser_request"))))
	a.takeAll(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate, 50*time.Millisecond)
	start := golden.Bytes(t, decodeCase(t, g, "advancegamestate_request"))
	wantError(t, "the first start", a.gm(CmdGameManagerAdvanceGameState, start), ErrInvalidGameStateAction)
	for _, x := range []*tcpClient{a, b} {
		x.note(ComponentGameManager, NotifyGameAttribChangeID)
	}
	if usid, _ := decode(t, a.note(ComponentUserSessions, NotifyUserSessionExtendedDataUpdate)).Int("USID"); usid != a.id {
		t.Errorf("the admin's update names %d, not %d", usid, a.id)
	}
	wantReply(t, "the second start", a.gm(CmdGameManagerAdvanceGameState, start))
	if log := ts.log.String(); !strings.Contains(log, "refused: not ready [] (the ready check) (--test-refuse-start") {
		t.Errorf("log without the test aid's refusal")
	}
}
