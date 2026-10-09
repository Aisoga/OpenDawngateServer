package blaze

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/store"
)

// H2: games on a remote host machine. The server (public host 192.168.1.10) has no hosts of its own;
// an agent (an in-process gmshost.Agent whose launcher starts fake hosts) registers as hostA with
// public host 192.168.1.20, and NotifyGameSetup's HNET names that machine: 192.168.1.20:10042, the
// address of tdf_golden.py's A5 case notifygamesetup_notification_lan (whose PNET echoes the client's
// own addresses, as with any non-loopback server host).

const (
	mainHost  = "192.168.1.10" // the main server's public host (only loopback-ness matters for PNET)
	agentHost = "192.168.1.20" // the agent's public host: tdf_golden.py A5_GMS
)

var agentKey = []byte("an agent key for the blaze tests")

type agentSession struct {
	*hostedSession
	agent  *gmshost.Agent
	alog   *lockedBuf
	cancel context.CancelFunc
	done   chan struct{}
}

// newAgentSession is a hosted session whose games run on an agent; script plays each host after
// gameReady (as in newHostedSession).
func newAgentSession(t *testing.T, script func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error) *agentSession {
	return newAgentSessionRun(t, script, nil)
}

// newAgentSessionRun is newAgentSession with the whole fake host given (run, if not nil).
func newAgentSessionRun(t *testing.T, script func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error,
	run func(env gmshost.Env, p *gmshost.FakeProcess)) *agentSession {
	as := &agentSession{hostedSession: &hostedSession{log: &lockedBuf{}, msgs: make(chan gmshost.Message, 16)},
		alog: &lockedBuf{}, done: make(chan struct{})}
	hs := as.hostedSession
	if run == nil {
		run = func(env gmshost.Env, p *gmshost.FakeProcess) {
			gmshost.PlayHost(env, p, func(h *gmshost.HostConn, cg gmshost.CreateGame) error {
				hs.msgs <- cg
				if script == nil {
					return nil
				}
				return script(hs, h, p)
			})
		}
	}
	hs.l = &gmshost.FakeLauncher{Run: run}
	hs.pipeSession = newPipeSessionWith(t, func(s *Server) {
		s.Log = logging.New(hs.log)
		s.Host = mainHost
		hs.m = gmshost.NewManager(gmshost.Config{Log: s.Log, AgentKey: agentKey, HeartbeatTimeout: 10 * time.Second,
			ExitTimeout: time.Second})
		if err := hs.m.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		s.Hosts = hs.m
		hs.s = s
	})
	t.Cleanup(hs.m.Close)
	as.agent = &gmshost.Agent{Log: logging.New(as.alog), Server: hs.m.Addr(), Key: agentKey, Name: "hostA",
		BindHost: "10.1.2.3", PublicHost: agentHost, Ports: "10042-10042", Launcher: hs.l, Retry: time.Hour,
		Heartbeat: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	as.cancel = cancel
	go func() { as.agent.Run(ctx); close(as.done) }()
	t.Cleanup(as.stop)
	for deadline := time.Now().Add(5 * time.Second); len(hs.m.Sites()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the agent did not register:\n%s\n%s", hs.log.String(), as.alog.String())
		}
	}
	return as
}

func (as *agentSession) stop() {
	as.cancel()
	<-as.done
}

// PRACTICE DAWNGATE with the host on another machine: the agent starts it there, and NotifyGameSetup
// names that machine's address (HNET) with the host's port; the rest is as with a local host.
func TestHostedGameOnAgent(t *testing.T) {
	as := newAgentSession(t, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
		return h.Send(gmshost.FinishGame{WinnerTeam: 1, DurationMs: 1000, Reason: "test"})
	})
	as.login()
	as.send(4, 0x19, 2, as.request("creategame_request"))
	as.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
	as.hostGot()
	as.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification_lan")
	as.expectNote(NotifyGameStateChangeID, GameStateChangeNotification(1, GameStatePostGame))
	as.expectNote(NotifyPlayerRemovedID, PlayerRemovedNotification(1, store.FirstPersonaID, RemovedGameEnded))
	as.waitIdle()
	as.close()
	envs := as.l.Envs()
	if len(envs) != 1 || envs[0].Bind != "10.1.2.3:10042" || envs[0].Control != as.m.Addr() {
		t.Errorf("host environment %+v (want DGGMS_BIND on the agent's bind address, DGGMS_CONTROL the server's control port)", envs)
	}
	log := as.log.String()
	for _, want := range []string{
		`[host] agent "hostA" from ('127.0.0.1', `,
		"[blaze]    game 1 \"player's Game\": match host 1 starting on agent hostA (GMS host 192.168.1.20) (pid ",
		"GMS port 10042), player 2000000001; NotifyGameSetup follows gameReady",
		" on agent hostA for game 1 (practice, SM_Map_v5, 1 players), GMS 10.1.2.3:10042\n",
		"[blaze]    game 1: GMS 192.168.1.20:10042 (HNET) ready ",
		"[host] match 1: ended after ",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// The agent lost during the game: the game ends without a result, GAME_DESTROYED. A host that dies on
// the agent before gameReady: NotifyGameSetup names its dead address on that machine (B8's path).
func TestHostedGameAgentFailures(t *testing.T) {
	t.Run("agent lost", func(t *testing.T) {
		as := newAgentSession(t, func(hs *hostedSession, h *gmshost.HostConn, p *gmshost.FakeProcess) error {
			<-p.Killed()
			return errors.New("killed")
		})
		as.login()
		as.send(4, 0x19, 2, as.request("creategame_request"))
		as.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
		as.hostGot()
		as.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification_lan")
		as.stop()
		as.expectNote(NotifyPlayerRemovedID, PlayerRemovedNotification(1, store.FirstPersonaID, RemovedGameDestroyed))
		as.waitIdle()
		as.close()
		if log := as.log.String(); !strings.Contains(log, "[blaze]    game 1: match host aborted (agent hostA lost)") {
			t.Errorf("log:\n%s", log)
		}
	})
	t.Run("host gone before ready", func(t *testing.T) {
		as := newAgentSessionRun(t, nil, func(env gmshost.Env, p *gmshost.FakeProcess) { p.Exit(7) }) // never dials in
		as.login()
		as.send(4, 0x19, 2, as.request("creategame_request"))
		as.expect(4, 0x19, fire.Reply, 2, "resetdedicatedserver_reply")
		as.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_hosted_notification_lan")
		as.waitIdle()
		as.close()
		if log := as.log.String(); !strings.Contains(log, "NotifyGameSetup names the dead GMS 192.168.1.20:10042") {
			t.Errorf("log:\n%s", log)
		}
	})
	t.Run("no host machine", func(t *testing.T) {
		as := newAgentSession(t, nil)
		as.stop()
		for deadline := time.Now().Add(5 * time.Second); len(as.m.Sites()) > 0; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the agent is still registered")
			}
		}
		as.login()
		as.send(4, 0x19, 2, as.request("creategame_request"))
		f := as.next("error reply")
		if f.Type != fire.ErrorReply || f.Error != ErrNoHostCapacity {
			t.Errorf("got %+v", f)
		}
		as.close()
		if log := as.log.String(); !strings.Contains(log, "no match host (gmshost: no host machine (no local hosts and no agent registered)); error reply") {
			t.Errorf("log:\n%s", log)
		}
	})
}
