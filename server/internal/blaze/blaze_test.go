package blaze

import (
	"bytes"
	"encoding/hex"
	"net"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// The golden replies are the Python stub's (blaze_stub.py), which the client accepted in b1_run2.
func TestPreAuthReplyMatchesPythonStub(t *testing.T) {
	g := golden.Load(t)
	want := g.WriterCase(t, "preauth_reply")
	if got := hex.EncodeToString(PreAuthReply("127.0.0.1", 17502, Service)); got != want.Hex {
		t.Errorf("preAuth reply\n got %s\nwant %s", got, want.Hex)
	}
}

func TestPingReplyMatchesPythonStub(t *testing.T) {
	g := golden.Load(t)
	want := g.WriterCase(t, "ping_reply")
	if got := hex.EncodeToString(PingReply(1791405059)); got != want.Hex {
		t.Errorf("ping reply\n got %s\nwant %s", got, want.Hex)
	}
}

// B5 replies: written from the client's member tables by re/scripts/tdf_golden.py with fixed ids, key
// and time; the client accepted them in session b5_run3.
const (
	testKey  = "0123456789abcdef0123456789abcdef"
	testTime = 1791405059
	// testPassword is the test clients' password (a new account's password must keep the rules; the golden
	// login_request carries the same, dawngate1)
	testPassword = "dawngate1"
)

func testAccount() (store.Account, store.Persona) {
	p := store.Persona{ID: store.FirstPersonaID, Name: "player"}
	return store.Account{UserID: store.FirstUserID, Email: "player@example.com", Personas: []store.Persona{p}}, p
}

func TestB5RepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	acct, p := testAccount()
	for _, c := range []struct {
		name string
		got  []byte
	}{
		{"login_reply", LoginReply(acct, testKey, testTime)},
		{"loginpersona_reply", SessionInfoReply(acct, p, testKey, true, testTime)},
		{"userauthenticated_notification", UserAuthenticatedNotification(acct, p, testKey, true, testTime)},
		{"extendeddataupdate_notification", ExtendedDataUpdateNotification(p.ID, 0, 0)},
		{"postauth_reply", PostAuthReply("127.0.0.1", 9988, acct.UserID)},
		{"gettelemetryserver_reply", TelemetryServerReply("127.0.0.1", 9988)},
		{"enterservice_reply", EnterServiceReply()},
		// part 2: the meta data up to the main menu (b5_run8)
		{"getcompletedata_reply", CompleteAccountDataReply(BasicData{BlazeID: p.ID, Level: 1}, nil, nil)},
		{"getbasicdata_reply", BasicAccountDataReply([]BasicData{{BlazeID: p.ID, Level: 1}})},
		{"getcatalog_reply", GetCatalogReply("", nil)},
		{"getcommerceprofile_reply", GetCommerceProfileReply(store.Commerce{})},
		{"getbalance_reply", GetBalanceReply(0, 0, 0)},
		{"bgweb_getcurrentversion_reply", BGWebCurrentVersionReply()},
		{"getstatgroup_reply", StatGroupReply("PlayerStats", nil)},
		{"getstatsasync_notification", StatsAsyncNotification("PlayerStats", 1, 0, nil)},
		{"messagecount_reply", MessageCountReply(0)},
		{"getgameid_reply", GetGameIDReply("")},
		{"bgops_getcurrentversion_reply", BGOpsCurrentVersionReply("prod-info")},
		{"ismmlockedout_reply", MMLockoutReply(0)},
	} {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// pipeSession runs ServeConn on one end of a pipe with a fixed clock and session key.
type pipeSession struct {
	t      *testing.T
	g      *golden.Data
	log    bytes.Buffer
	client net.Conn
	r      *fire.Reader
	done   chan struct{}
	srv    *Server
}

func newPipeSession(t *testing.T) *pipeSession { return newPipeSessionWith(t, nil) }

// newPipeSessionWith lets setup change the server (store, content, policy) before the session starts.
func newPipeSessionWith(t *testing.T, setup func(s *Server)) *pipeSession {
	ps := &pipeSession{t: t, g: golden.Load(t), done: make(chan struct{})}
	s := NewServer(logging.New(&ps.log), "127.0.0.1", 17502)
	s.TelemetryPort = 9988
	s.Now = func() time.Time { return time.Unix(testTime, 0) }
	s.NewKey = func() string { return testKey }
	if setup != nil {
		setup(s)
	}
	ps.srv = s
	ps.serve()
	return ps
}

// serve connects the session's client end to its server.
func (ps *pipeSession) serve() {
	client, server := net.Pipe()
	ps.client, ps.r = client, fire.NewReader(client)
	go func() { ps.srv.ServeConn(server); close(ps.done) }()
}

// another opens a second client connection to the same server (its log goes to ps's log).
func (ps *pipeSession) another() *pipeSession {
	o := &pipeSession{t: ps.t, g: ps.g, done: make(chan struct{}), srv: ps.srv}
	o.serve()
	return o
}

func (ps *pipeSession) send(comp, cmd uint16, id uint32, payload []byte) {
	b, err := fire.Encode(fire.Frame{Component: comp, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
	if err != nil {
		ps.t.Fatal(err)
	}
	if _, err := ps.client.Write(b); err != nil {
		ps.t.Fatal(err)
	}
}

func (ps *pipeSession) next(what string) fire.Frame {
	ps.client.SetReadDeadline(time.Now().Add(5 * time.Second))
	f, err := ps.r.Next()
	if err != nil {
		ps.t.Fatalf("%s: %v", what, err)
	}
	return f
}

// expectFrame compares the next frame with a golden frame.
func (ps *pipeSession) expectFrame(name string) {
	want := ps.g.Frame(ps.t, name)
	b, _ := fire.Encode(ps.next(name))
	if got := hex.EncodeToString(b); got != want.Hex {
		ps.t.Errorf("%s frame\n got %s\nwant %s", name, got, want.Hex)
	}
}

// expect checks the next frame's header, and its payload against a writer case ("" = empty payload).
func (ps *pipeSession) expect(comp, cmd uint16, typ fire.MsgType, id uint32, golden string) {
	f := ps.next(golden)
	if f.Component != comp || f.Command != cmd || f.Type != typ || f.ID != id || f.Error != 0 {
		ps.t.Errorf("%q: got header %#x/%#x %s id %d err %#x", golden, f.Component, f.Command, f.Type, f.ID, f.Error)
	}
	want := ""
	if golden != "" {
		want = ps.g.WriterCase(ps.t, golden).Hex
	}
	if got := hex.EncodeToString(f.Payload); got != want {
		ps.t.Errorf("%q payload\n got %s\nwant %s", golden, got, want)
	}
}

// request returns a golden client request (a decode case).
func (ps *pipeSession) request(name string) []byte {
	for _, c := range ps.g.Decode {
		if c.Name == name {
			return golden.Bytes(ps.t, c.Hex)
		}
	}
	ps.t.Fatalf("no decode case %q", name)
	return nil
}

func (ps *pipeSession) close() string {
	ps.client.Close()
	<-ps.done
	return ps.log.String()
}

// A whole session over a pipe, as the client ran it in b5_run3: preAuth, ping, login, loginPersona
// (reply, then the UserAuthenticated and UserSessionExtendedDataUpdate notifications), postAuth,
// updateNetworkInfo, getTelemetryServer, enterService, then a request without a handler.
func TestServeConn(t *testing.T) {
	ps := newPipeSession(t)
	ps.send(9, 7, 0, nil)
	ps.expectFrame("preauth_reply")
	ps.send(9, 2, 1, nil)
	ps.expectFrame("ping_reply")
	ps.send(1, 0x28, 2, ps.request("login_request"))
	ps.expect(1, 0x28, fire.Reply, 2, "login_reply")
	ps.send(1, 0x6E, 3, ps.request("loginpersona_request"))
	ps.expect(1, 0x6E, fire.Reply, 3, "loginpersona_reply")
	ps.expect(0x7802, 8, fire.Notification, 0, "userauthenticated_notification")
	ps.expect(0x7802, 1, fire.Notification, 0, "extendeddataupdate_notification")
	ps.send(9, 8, 4, nil)
	ps.expect(9, 8, fire.Reply, 4, "postauth_reply")
	ps.send(0x7802, 0x14, 5, nil)
	ps.expect(0x7802, 0x14, fire.Reply, 5, "")
	ps.send(9, 5, 6, tdf.NewWriter().String("CMAC", "$000000000000").String("SNAM", "").Bytes())
	ps.expect(9, 5, fire.Reply, 6, "gettelemetryserver_reply")
	ps.send(0x825, 0x78, 7, nil)
	ps.expect(0x825, 0x78, fire.Reply, 7, "enterservice_reply")
	ps.send(0x7FFF, 1, 8, nil) // no such component: an error reply (LBFIX)
	ps.expectError(0x7FFF, 1, 8, ErrComponentNotFound)
	ps.send(9, 0x7F, 9, nil) // a component we serve, a command we don't
	ps.expectError(9, 0x7F, 9, ErrCommandNotFound)
	ps.send(9, 2, 10, []byte{0x86}) // a payload that doesn't decode
	ps.expectError(9, 2, 10, ErrSystem)
	ps.send(9, 2, 11, nil) // and the connection goes on
	ps.next("ping reply")
	log := ps.close()

	for _, want := range []string{
		"[blaze] <- Util(0x0009) cmd 0x0007 request id 0 err 0x0, 0 bytes",
		"[blaze] -> preAuth reply, 355 bytes:\n    ASRC: '303107'\n    CIDS: \n      - 1\n",
		"[blaze] -> ping reply, 9 bytes:\n    STIM: 1791405059\n",
		"[blaze] <- Authentication(0x0001) cmd 0x0028 request id 2 err 0x0, ",
		"    MAIL: 'player@example.com'\n",
		"[blaze] -> login reply, 115 bytes:\n    ANON: 0\n",
		"[blaze] -> UserSessions::UserAuthenticated notification, 143 bytes:\n    ALOC: 1701729619\n",
		"[blaze] -> UserSessions::UserSessionExtendedDataUpdate notification, ",
		"[blaze] -> updateNetworkInfo reply, 0 bytes:\n",
		"[blaze] <- component32767(0x7fff) cmd 0x0001 request id 8 err 0x0, 0 bytes",
		"[blaze] -> error reply (no handler; error 0x4002), 0 bytes:\n",
		"[blaze] -> error reply (no handler; error 0x4003), 0 bytes:\n",
		"[blaze] -> error reply (request doesn't decode; error 0x4001), 0 bytes:\n",
		"[blaze] connection closed\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	// the logged reply is exactly what the Python stub logged for it
	if pre := ps.g.WriterCase(t, "preauth_reply").Pretty; !strings.Contains(log, "-> preAuth reply, 355 bytes:\n"+pre+"\n") {
		t.Errorf("preAuth reply log differs from the Python stub's:\n%s", log)
	}
}

// The meta requests that need the session or the request: getCompleteData for the logged-in persona,
// getBasicData only for known ids, getStatsByGroupAsync's empty reply plus its notification, and
// getCurrentVersion echoing MENV (b5_run8).
func TestMetaRequests(t *testing.T) {
	// the original's closed first-time-user gate (open by default)
	ps := newPipeSessionWith(t, func(s *Server) { s.Policy.FTUEDone = false })
	ps.send(1, 0x28, 0, ps.request("login_request"))
	ps.expect(1, 0x28, fire.Reply, 0, "login_reply")
	ps.send(1, 0x6E, 1, ps.request("loginpersona_request"))
	ps.expect(1, 0x6E, fire.Reply, 1, "loginpersona_reply")
	ps.next("UserAuthenticated")
	ps.next("UserSessionExtendedDataUpdate")
	ps.send(0x81F, 0x76, 2, nil)
	ps.expect(0x81F, 0x76, fire.Reply, 2, "getcompletedata_reply")
	ps.send(0x81F, 0x73, 3, tdf.NewWriter().List("BZID", tdf.TypeInt, []int64{store.FirstPersonaID, 42}).Bytes())
	ps.expect(0x81F, 0x73, fire.Reply, 3, "getbasicdata_reply")
	ps.send(7, 0x10, 4, tdf.NewWriter().String("NAME", "PlayerStats").Int("VID", 1).Bytes())
	ps.expect(7, 0x10, fire.Reply, 4, "")
	ps.expect(7, 0x32, fire.Notification, 0, "b7a_getstatsasync_notification") // PlayerStats has values since B7a
	ps.send(0x823, 0x13, 5, tdf.NewWriter().String("MENV", "prod-info").Bytes())
	ps.expect(0x823, 0x13, fire.Reply, 5, "bgops_getcurrentversion_reply")
	if log := ps.close(); !strings.Contains(log, "[blaze] -> Stats::GetStatsAsyncNotification notification, ") {
		t.Errorf("log:\n%s", log)
	}
}

// B8 payloads, written independently by tdf_golden.py from the member tables (session b8_run5).
func TestB8RepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range []struct {
		name string
		got  []byte
	}{
		{"resetdedicatedserver_reply", JoinGameReply(1)},
		{"notifygamesetup_notification", GameSetupNotification(testGame(), testPlayer())},
	} {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// PRACTICE DAWNGATE as in b8_run5: resetDedicatedServer with the client's CreateGameRequest gets
// JoinGameResponse and then NotifyGameSetup with our GMS address in HNET and the player's own address
// (its LAN address replaced by loopback) as PNET; removePlayer gets an empty reply.
func TestPracticeGame(t *testing.T) {
	ps := newPipeSession(t)
	ps.send(9, 0x19, 0, nil) // wrong component: no handler, an error reply
	ps.expectError(9, 0x19, 0, ErrCommandNotFound)
	ps.send(4, 0x19, 1, ps.request("creategame_request"))
	ps.send(1, 0x28, 2, ps.request("login_request"))
	ps.expect(1, 0x28, fire.Reply, 2, "login_reply")
	ps.send(1, 0x6E, 3, ps.request("loginpersona_request"))
	ps.expect(1, 0x6E, fire.Reply, 3, "loginpersona_reply")
	ps.next("UserAuthenticated")
	ps.next("UserSessionExtendedDataUpdate")
	ps.send(4, 0x19, 4, ps.request("creategame_request"))
	ps.expect(4, 0x19, fire.Reply, 4, "resetdedicatedserver_reply")
	ps.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_notification")
	// B9: connected to the GMS, the client reports its mesh connection to the dedicated server (b9_run1)
	mesh := tdf.NewWriter().Int("GID", 1).List("TARG", tdf.TypeStruct, []*tdf.Writer{
		tdf.NewWriter().Int("FLGS", 0).Int("PID", DedicatedServerID).Int("STAT", 2)}).Bytes()
	tdfcheck.Require(t, "Blaze::GameManager::UpdateMeshConnectionRequest", mesh)
	ps.send(4, 0x1D, 5, mesh)
	ps.expect(4, 0x1D, fire.Reply, 5, "")
	ps.send(4, 0x0B, 6, tdf.NewWriter().Int("GID", 1).Int("PID", store.FirstPersonaID).Int("REAS", 6).Bytes())
	ps.expect(4, 0x0B, fire.Reply, 6, "")
	log := ps.close()
	for _, want := range []string{
		"(resetDedicatedServer before login; not answered)",
		"[blaze]    game 1 \"player's Game\": GMS 127.0.0.1:10042 (HNET), player 2000000001\n",
		"      - ('union', 2, {'EXIP': {'IP': 2130706433, 'PORT': 3659}, 'INIP': {'IP': 167772162, 'PORT': 3659}})\n",
		"[blaze] -> GameManager::NotifyGameSetup notification, ",
		"[blaze]    game 1: mesh connection to 3000000001 CONNECTED\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// getCompleteData without a login stays unanswered (logged).
func TestCompleteDataNeedsLogin(t *testing.T) {
	ps := newPipeSession(t)
	ps.send(0x81F, 0x76, 0, nil)
	if log := ps.close(); !strings.Contains(log, "(getCompleteData before login; not answered)") {
		t.Errorf("log:\n%s", log)
	}
}

// loginPersona without a login on the connection stays unanswered (logged).
func TestLoginPersonaNeedsLogin(t *testing.T) {
	ps := newPipeSession(t)
	ps.send(1, 0x6E, 0, ps.request("loginpersona_request"))
	if log := ps.close(); !strings.Contains(log, "(loginPersona before login; not answered)") {
		t.Errorf("log:\n%s", log)
	}
}
