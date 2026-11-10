package dgserver

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

// REGION5: everything in one process: a main dgserver of region eu with the tunnel and local hosts, a
// host machine na1 (dgserver --agent-tls with its match gateway, --tunnel on), a player's tunnel session to the main
// gateway (session A, as Start-Client's front opens it) and the away session to the match gateway the server sends it
// to (session B: the front's away session, its routing is launcher's TestFrontAway). The match hosts on both machines
// are this test binary (TestMain's fake host "seat": every "claim <id>" datagram becomes a verifyHello, the verdict
// goes back as "ok <id>" or "no <id>").

// seatHost is the "seat" fake host: as fakeHost, but it binds its GMS port and asks verifyHello for every claim until
// the server ends the match.
func seatHost() int {
	env, ok, err := gmshost.EnvFromLookup(os.LookupEnv)
	if !ok || err != nil {
		return 10
	}
	h, err := gmshost.DialHost(env, os.Getpid(), "dgserver test host seat")
	if err != nil {
		return 11
	}
	defer h.Close()
	if msg, err := h.Recv(10 * time.Second); err != nil || msg.Type() != gmshost.TCreateGame {
		return 12
	}
	addr, err := net.ResolveUDPAddr("udp4", env.Bind)
	if err != nil {
		return 13
	}
	sock, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return 14
	}
	defer sock.Close()
	h.Send(gmshost.GameReady{Port: addr.Port})
	var mu sync.Mutex
	from := map[string]*net.UDPAddr{}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, a, err := sock.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var id int64
			if _, err := fmt.Sscanf(string(buf[:n]), "claim %d", &id); err != nil {
				continue
			}
			mu.Lock()
			from[a.String()] = a
			mu.Unlock()
			h.Send(gmshost.VerifyHello{Addr: a.String(), User: id})
		}
	}()
	for {
		m, err := h.Recv(0)
		if err != nil {
			return 0
		}
		switch v := m.(type) {
		case gmshost.AbortGame:
			return 0
		case gmshost.HelloVerdict:
			mu.Lock()
			a := from[v.Addr]
			mu.Unlock()
			word := "no"
			if v.OK {
				word = "ok"
			}
			if a != nil {
				sock.WriteToUDP([]byte(fmt.Sprintf("%s %d", word, v.User)), a)
			}
		}
	}
}

// r5Client is a Blaze client through session A: requests answered in order, notifications kept.
type r5Client struct {
	t      *testing.T
	c      net.Conn
	r      *fire.Reader
	id     uint32
	buid   int64
	notes  []fire.Frame
	expect string
}

func r5Login(t *testing.T, ip string, port int, email string) *r5Client {
	t.Helper()
	d := net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(ip)}}
	c, err := d.Dial("tcp4", net.JoinHostPort(ip, fmt.Sprint(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	b := &r5Client{t: t, c: c, r: fire.NewReader(c)}
	b.call(1, 0x28, tdf.NewWriter().String("MAIL", email).String("PASS", testPassword).String("TOKN", "").Int("TYPE", 0).Bytes())
	f := b.call(1, 0x6E, tdf.NewWriter().String("PNAM", strings.Split(email, "@")[0]).Bytes())
	st, err := tdf.Decode(f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	b.buid, _ = st.Int("BUID")
	if b.buid == 0 {
		t.Fatalf("%s: no BUID", email)
	}
	return b
}

func (b *r5Client) call(comp, cmd uint16, payload []byte) fire.Frame {
	b.t.Helper()
	b.id++
	req, _ := fire.Encode(fire.Frame{Component: comp, Command: cmd, Type: fire.Request, ID: b.id, Payload: payload})
	if _, err := b.c.Write(req); err != nil {
		b.t.Fatal(err)
	}
	for {
		b.c.SetReadDeadline(time.Now().Add(10 * time.Second))
		f, err := b.r.Next()
		if err != nil {
			b.t.Fatalf("request %#x/%#x: %v", comp, cmd, err)
		}
		switch {
		case f.Type == fire.Notification:
			b.notes = append(b.notes, f)
		case f.ID == b.id && f.Type == fire.Reply:
			return f
		case f.ID == b.id:
			b.t.Fatalf("request %#x/%#x: error reply %+v", comp, cmd, f)
		}
	}
}

// note waits for a notification (those that came with replies first).
func (b *r5Client) note(comp, cmd uint16, within time.Duration) fire.Frame {
	b.t.Helper()
	for i, f := range b.notes {
		if f.Component == comp && f.Command == cmd {
			b.notes = append(b.notes[:i:i], b.notes[i+1:]...)
			return f
		}
	}
	deadline := time.Now().Add(within)
	for {
		b.c.SetReadDeadline(deadline)
		f, err := b.r.Next()
		if err != nil {
			b.t.Fatalf("no notification %#x/%#x: %v", comp, cmd, err)
		}
		if f.Type == fire.Notification && f.Component == comp && f.Command == cmd {
			return f
		}
		if f.Type == fire.Notification {
			b.notes = append(b.notes, f)
		}
	}
}

// createCustom picks the region (updateNetworkInfo's NLMP, as the play screen's click) and creates a custom game; it
// returns once NotifyGameSetup came (the host is ready).
func (b *r5Client) createCustom(region string) {
	b.t.Helper()
	other := map[string]string{"na": "eu", "eu": "na"}[region]
	b.call(blaze.ComponentUserSessions, blaze.CmdUserSessionsUpdateNetworkInfo, tdf.NewWriter().Map("NLMP", tdf.TypeString, tdf.TypeInt,
		tdf.KV(other, int64(150)), tdf.KV(other+"-any", int64(200)), tdf.KV(region, int64(50)), tdf.KV(region+"-any", int64(100))).Bytes())
	g := golden.Load(b.t)
	hexReq := ""
	for _, c := range g.Decode {
		if c.Name == "resetdedicatedserver_custom_request" {
			hexReq = c.Hex
		}
	}
	b.call(blaze.ComponentGameManager, blaze.CmdGameManagerResetDedicatedServer, golden.Bytes(b.t, hexReq))
	b.note(blaze.ComponentGameManager, blaze.NotifyGameSetupID, 20*time.Second)
}

// claimVia sends "claim <id>" with send (a session's datagram to the GMS port) and waits for the host's answer on got.
func claimVia(send func([]byte) bool, got <-chan string, id int64) string {
	for try := 0; try < 5; try++ {
		send([]byte(fmt.Sprintf("claim %d", id)))
		select {
		case s := <-got:
			return s
		case <-time.After(500 * time.Millisecond):
		}
	}
	return ""
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
	}
}

func TestRegionsJoined(t *testing.T) {
	prepareHosts = func() error { return nil } // no launcher event: the "Dawngate.exe" is this binary
	t.Cleanup(func() { prepareHosts = gmshost.LauncherEvent })
	t.Setenv(hostEnv, "seat") // the hosts both machines start are this binary's seat host
	const mainIP, agentIP, agentHostsIP, tunIP = "127.0.9.110", "127.0.9.111", "127.0.9.112", "127.0.9.113"
	key := filepath.Join(t.TempDir(), "na1.key")
	if err := writeAgentKey(key); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mainLog, agentLog := t.TempDir(), t.TempDir()
	in, err := start(config{bind: mainIP, certs: testCerts(t), logDir: mainLog, redirectorPort: redirector.Port, tunnel: TunnelOn,
		gmsDir: exe, gmsArgs: "-multipleInstances", gmsPorts: "20070-20071", gmsControlBind: mainIP,
		regions: regionFlags{region: "eu", machines: stringList{"na1=na:" + key}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if in != nil {
			in.stop()
		}
	}()
	agentCfg := config{agentKey: key, agentName: "na1", gmsDir: exe, gmsArgs: "-multipleInstances", gmsPorts: "20070-20071",
		bind: agentIP, gmsBind: agentHostsIP, publicHost: agentIP, logDir: agentLog, certs: t.TempDir(), tunnel: TunnelOn, logDays: 30,
		regions: regionFlags{agentTLS: in.gateway.Addr(), agentPin: in.gateway.Identity.Pin.String()}}
	ag, err := startAgent(agentCfg)
	if err != nil {
		t.Fatal(err)
	}
	// Start-Server's host-machine role gets the match gateway from Hooks.Started
	if inf := ag.info(agentCfg); inf.GatewayPort == 0 || inf.GatewayPin != ag.gateway.Identity.Pin.String() || inf.PublicHost != agentIP ||
		fmt.Sprint(inf.TCPPorts) != fmt.Sprintf("[0 %d]", inf.GatewayPort) {
		t.Errorf("agent info %+v", inf)
	}
	defer func() {
		if ag != nil {
			ag.stop()
		}
	}()
	waitFor(t, "the host machine did not register with its match gateway", func() bool {
		m, ok := in.hosts.Machine("na1")
		return ok && strings.HasPrefix(m.Gateway, agentIP+":")
	})

	// session A, as the front opens it: its away messages come to awayCh
	awayCh, homeCh := make(chan tunnel.Away, 4), make(chan int, 4)
	slog := logging.New(&strings.Builder{})
	sa := &tunnel.Client{Log: slog, Gateway: in.gateway.Addr(), Pin: in.gateway.Identity.Pin, TunnelIP: net.ParseIP(tunIP),
		DialIP: net.ParseIP(tunIP), OnAway: func(_ int, a tunnel.Away) { awayCh <- a }, OnHome: func(p int) { homeCh <- p }}
	if err := sa.Start(); err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	if !sa.WaitUDP(3 * time.Second) {
		t.Fatal("no UDP through session A")
	}
	blazePort := in.ports[1]

	// 1. North America: the game runs on na1, the creator's Start-Client is sent to its match gateway
	alpha := r5Login(t, tunIP, blazePort, "alpha@example.com")
	alpha.createCustom("na")
	var away tunnel.Away
	select {
	case away = <-awayCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no away message on session A")
	}
	if away.Gateway.Addr().String() != agentIP || away.Pin != ag.gateway.Identity.Pin {
		t.Fatalf("away %v (pin %s)", away.Gateway, away.Pin.Fingerprint())
	}
	tk, err := tunnel.ParseTicket(away.Ticket)
	if err != nil || tk.Machine != "na1" || tk.User != alpha.buid || tk.Match != 1 {
		t.Fatalf("ticket %+v %v", tk, err)
	}
	const naPort = 20070
	answers := make(chan string, 8)
	sb := &tunnel.Client{Log: slog, Gateway: away.Gateway.String(), Pin: away.Pin, TunnelIP: net.ParseIP(tunIP), External: true,
		Ticket: away.Ticket, Deliver: func(port int, _ *net.UDPAddr, p []byte) {
			if port == naPort {
				answers <- string(p)
			}
		}}
	if err := sb.Start(); err != nil {
		t.Fatalf("session B: %v", err)
	}
	defer sb.Close()
	src := &net.UDPAddr{IP: net.ParseIP(tunIP), Port: 50001}
	sendB := func(p []byte) bool { return sb.SendDatagram(naPort, src, p) }
	if got := claimVia(sendB, answers, alpha.buid); got != fmt.Sprintf("ok %d", alpha.buid) {
		t.Errorf("the ticket's user through session B: %q", got)
	}
	if got := claimVia(sendB, answers, alpha.buid+1); got != fmt.Sprintf("no %d", alpha.buid+1) {
		t.Errorf("another user through session B: %q", got)
	}
	// session A no longer reaches the match: the main gateway has no route for the port, and na1's hosts aren't its
	direct := make(chan string, 4)
	sockA, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(tunIP)})
	if err != nil {
		t.Fatal(err)
	}
	defer sockA.Close()
	go func() {
		buf := make([]byte, 256)
		for {
			n, _, err := sockA.ReadFromUDP(buf)
			if err != nil {
				return
			}
			direct <- string(buf[:n])
		}
	}()
	sendA := func(p []byte) bool {
		_, err := sockA.WriteToUDP(p, &net.UDPAddr{IP: net.ParseIP(tunIP), Port: naPort})
		return err == nil
	}
	if got := claimVia(sendA, direct, alpha.buid); got != "" {
		t.Errorf("session A reached the NA match: %q", got)
	}

	// 2. Europe: a second login behind the same session plays at home, through session A (the port is the same number
	// on this machine: the session's route comes home)
	bravo := r5Login(t, tunIP, blazePort, "bravo@example.com")
	bravo.createCustom("eu")
	select {
	case p := <-homeCh:
		if p != naPort {
			t.Errorf("home for port %d", p)
		}
	case <-time.After(5 * time.Second):
		t.Error("no home message for the port the Europe game took")
	}
	if got := claimVia(sendA, direct, bravo.buid); got != fmt.Sprintf("ok %d", bravo.buid) {
		t.Errorf("the Europe game through session A: %q", got)
	}

	// 3. na1 stops: the next North American game plays in Europe, with the reason
	ag.stop()
	ag = nil
	waitFor(t, "na1 still registered", func() bool { _, ok := in.hosts.Machine("na1"); return !ok })
	charlie := r5Login(t, tunIP, blazePort, "charlie@example.com")
	charlie.createCustom("na")

	in.stop()
	in = nil
	main, agent := readLog(t, mainLog), readLog(t, agentLog)
	for _, want := range []string{
		"regions: home eu, custom games may run in eu, na",
		`registered over the TLS link (`,
		"match gateway " + agentIP + ":",
		fmt.Sprintf("game 1: player %d sent to host machine na1's match gateway %s for GMS port %d (match 1)", alpha.buid, away.Gateway, naPort),
		fmt.Sprintf("not among its tickets' users [%d] at na1; refused", alpha.buid),
		"wanted na, runs on local (eu): down",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main server log lacks %q:\n%s", want, main)
		}
	}
	if strings.Contains(main, fmt.Sprintf("game 2: player %d sent to host machine", bravo.buid)) {
		t.Error("the Europe game was sent away")
	}
	for _, want := range []string{
		`[stub] agent "na1": match gateway on ` + agentIP + ":",
		"ports 20070-20071 to the hosts on " + agentHostsIP,
		"[agent] match 1: the host's control connection relayed",
		`[stub] agent "na1": logs older than 30 days deleted (--log-days`,
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("agent log lacks %q:\n%s", want, agent)
		}
	}
}

// A machine hosts SM_Map_v5, a level whose archive this server has no version of, and a versioned map only in this
// server's version.
func TestMachineLevels(t *testing.T) {
	v := [32]byte{1, 2, 3}
	levels := machineLevels(map[string][32]byte{"zz_mm_map_v3.snap": v})
	has := func(got []string, name string) bool {
		for _, l := range got {
			if l == name {
				return true
			}
		}
		return false
	}
	same := levels(map[string]string{"zz_mm_map_v3.snap": fmt.Sprintf("%x", v)})
	other := levels(map[string]string{"zz_mm_map_v3.snap": strings.Repeat("ab", 32)})
	none := levels(nil)
	if !has(same, "SM_Map_v5") || !has(same, "MM_Map_v3") || !has(other, "SM_Map_v5") || has(other, "MM_Map_v3") || has(none, "MM_Map_v3") {
		t.Errorf("same %v, other %v, none %v", same, other, none)
	}
	if got := machineLevels(nil)(nil); !has(got, "MM_Map_v3") {
		t.Errorf("no versions known: %v", got)
	}
}
