package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
)

// H2: dgserver as a main server that takes agents, and dgserver in agent mode, in one process on
// ephemeral loopback ports. The agent's hosts are real processes: this test binary, started by the
// agent's ExecLauncher as its "Dawngate.exe" (--gms-dir names the binary itself) and turned into a
// fake host by TestMain.

const (
	hostEnv = "DGSERVER_TEST_HOST" // the fake host's scenario: finish, crash or hang
	// H2b (hostjob_windows_test.go): dgserver in its own process. mainEnv runs main() with the
	// process's command line (agent mode), localEnv ("<certs>|<log dir>") a main server with one
	// local host; childHostEnv is the scenario of the hosts that process starts.
	mainEnv      = "DGSERVER_TEST_MAIN"
	localEnv     = "DGSERVER_TEST_LOCAL"
	childHostEnv = "DGSERVER_TEST_CHILD_HOST"
)

// TestMain lets the test binary play a match host: with DGSERVER_TEST_HOST set it runs that scenario
// against DGGMS_* and exits instead of running the tests (before flag parsing, so the host's command
// line, -multipleInstances, doesn't matter). With DGSERVER_TEST_MAIN or DGSERVER_TEST_LOCAL it plays
// dgserver itself (serverProcess).
func TestMain(m *testing.M) {
	if mode := os.Getenv(hostEnv); mode != "" {
		os.Exit(fakeHost(mode))
	}
	if os.Getenv(mainEnv) != "" || os.Getenv(localEnv) != "" {
		os.Exit(serverProcess())
	}
	os.Exit(m.Run())
}

// serverProcess is dgserver in a process of its own, killed by the test (H2b). DGSERVER_TEST_MAIN:
// main() with this process's arguments (dgserver --agent ...). DGSERVER_TEST_LOCAL: a main server on
// ephemeral loopback ports (start, as main does, minus the redirector's fixed port) that starts one
// local host, prints "host <pid>" once it is ready and waits. The hosts are this binary again.
func serverProcess() int {
	local := os.Getenv(localEnv)
	os.Unsetenv(mainEnv)
	os.Unsetenv(localEnv)
	os.Setenv(hostEnv, os.Getenv(childHostEnv)) // for the hosts only: TestMain has already looked
	prepareHosts = func() error { return nil }  // no launcher event: the "Dawngate.exe" is this binary
	if local == "" {
		main()
		return 0
	}
	certs, logDir, _ := strings.Cut(local, "|")
	exe, err := os.Executable()
	if err != nil {
		fmt.Println("error", err)
		return 30
	}
	in, err := start(config{bind: "127.0.0.1", certs: certs, logDir: logDir, gmsDir: exe, gmsPorts: "20048-20048",
		gmsArgs: "-multipleInstances"})
	if err != nil {
		fmt.Println("error", err)
		return 31
	}
	ready := make(chan bool, 1)
	mt, err := in.hosts.Start(gmshost.CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: gmshost.ModePractice, Seed: 7919,
		Roster: []gmshost.RosterEntry{{User: 2000000001, Name: "player", Team: 1}}},
		gmshost.Events{Ready: func(*gmshost.Match, int) { ready <- true }})
	if err != nil {
		fmt.Println("error", err)
		return 32
	}
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		fmt.Println("error: the host was not ready in 20 s")
		return 33
	}
	fmt.Printf("host %d\n", mt.Pid())
	time.Sleep(2 * time.Minute) // until the test kills us
	return 34
}

func fakeHost(mode string) int {
	env, ok, err := gmshost.EnvFromLookup(os.LookupEnv)
	if !ok || err != nil {
		return 10
	}
	h, err := gmshost.DialHost(env, os.Getpid(), "dgserver test host "+mode)
	if err != nil {
		return 11
	}
	defer h.Close()
	if msg, err := h.Recv(10 * time.Second); err != nil || msg.Type() != gmshost.TCreateGame {
		return 12
	}
	_, port, _ := net.SplitHostPort(env.Bind)
	var n int
	fmt.Sscan(port, &n)
	h.Send(gmshost.GameReady{Port: n})
	switch mode {
	case "crash":
		return 3
	case "hang": // H2b: a running match, until the job ends it (or the test's cleanup)
		time.Sleep(2 * time.Minute)
		return 13
	}
	h.Send(gmshost.AddPlayer{User: 2000000001})
	h.Send(gmshost.FinishGame{WinnerTeam: 1, DurationMs: 1000, Reason: "agent test"})
	h.Send(gmshost.LogoutGMS{})
	return 0
}

func readLog(t *testing.T, dir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	if len(files) != 1 {
		t.Fatalf("log files in %s: %v", dir, files)
	}
	raw, _ := os.ReadFile(files[0])
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

func TestAgentMode(t *testing.T) {
	prepareHosts = func() error { return nil } // no launcher event: the "Dawngate.exe" is this binary
	t.Cleanup(func() { prepareHosts = gmshost.LauncherEvent })
	key := filepath.Join(t.TempDir(), "agent.key")
	if err := writeAgentKey(key); err != nil {
		t.Fatal(err)
	}
	mainLog, agentLog := t.TempDir(), t.TempDir()
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: mainLog, agentKey: key})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if in != nil {
			in.stop()
		}
	}()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctl := in.hosts.Addr()
	ag, err := startAgent(config{agent: ctl, agentKey: key, agentName: "hostA", gmsDir: exe,
		gmsArgs: "-multipleInstances", gmsPorts: "20042-20043", bind: "127.0.0.1", publicHost: "127.0.0.1", logDir: agentLog})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if ag != nil {
			ag.stop()
		}
	}()
	for deadline := time.Now().Add(10 * time.Second); len(in.hosts.Sites()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the agent did not register")
		}
	}
	game := gmshost.CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: gmshost.ModePractice, Seed: 7919,
		Roster: []gmshost.RosterEntry{{User: 2000000001, Name: "player", Team: 1}}}
	var pids []int
	for _, c := range []struct {
		mode     string
		finished bool
		code     int
	}{{"finish", true, 0}, {"crash", false, 3}} {
		t.Setenv(hostEnv, c.mode) // the agent's ExecLauncher passes this process's environment on
		end := make(chan gmshost.Outcome, 1)
		mt, err := in.hosts.Start(game, gmshost.Events{End: func(mt *gmshost.Match, o gmshost.Outcome) { end <- o }})
		if err != nil {
			t.Fatal(err)
		}
		if mt.Site != "hostA" || mt.Bind != "127.0.0.1:20042" || mt.GMSHost != "127.0.0.1" {
			t.Errorf("match %+v", mt)
		}
		select {
		case o := <-end:
			if !o.Ready || o.Finished != c.finished || o.Killed || o.ExitCode != c.code {
				t.Errorf("%s: outcome %+v", c.mode, o)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("%s: the match did not end", c.mode)
		}
		pids = append(pids, mt.Pid())
	}
	ag.stop()
	ag = nil
	for deadline := time.Now().Add(5 * time.Second); len(in.hosts.Sites()) > 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the agent is still registered")
		}
	}
	in.stop()
	in = nil

	main, agent := readLog(t, mainLog), readLog(t, agentLog)
	for _, want := range []string{
		"GMS (agents with the key in " + key + ", control 127.0.0.1:",
		`[host] agent "hostA" from ('127.0.0.1', `, `) registered (dgserver): GMS host 127.0.0.1, bind 127.0.0.1, ports 20042-20043`,
		fmt.Sprintf("[host] match 1: started pid %d on agent hostA for game 1 (practice, SM_Map_v5, 1 players), GMS 127.0.0.1:20042", pids[0]),
		fmt.Sprintf("[host] match 1: hello from pid %d (dgserver test host finish)", pids[0]),
		": finished (winner team 1): agent test; exit code 0, killed false; port 20042 free",
		"[host] match 2: ended after ", ": aborted: exited with code 3 without finishGame; exit code 3, killed false; port 20042 free",
		`[host] agent "hostA" (127.0.0.1) gone: connection closed by the agent; 0 running hosts end with it`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main server log lacks %q:\n%s", want, main)
		}
	}
	for _, want := range []string{
		fmt.Sprintf(`[stub] agent "hostA" for %s: hosts %s -multipleInstances, ports 20042-20043 bound on 127.0.0.1, GMS host 127.0.0.1; log `, ctl, exe),
		fmt.Sprintf(`[agent] registered with %s as "hostA" (seen from 127.0.0.1): GMS host 127.0.0.1, bind 127.0.0.1, ports 20042-20043`, ctl),
		fmt.Sprintf("[agent] match 1: pid %d exited with code 0", pids[0]),
		fmt.Sprintf("[agent] match 2: pid %d exited with code 3", pids[1]),
		"[agent] stopped", "[stub] stopped",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("agent log lacks %q:\n%s", want, agent)
		}
	}
}

// The H2 settings that stop a start: before anything binds a non-loopback address.
func TestAgentModeConfig(t *testing.T) {
	key := filepath.Join(t.TempDir(), "agent.key")
	if err := writeAgentKey(key); err != nil {
		t.Fatal(err)
	}
	if err := writeAgentKey(key); err == nil {
		t.Error("--make-agent-key overwrote an existing file")
	}
	raw, _ := os.ReadFile(key)
	if !regexp.MustCompile(`^[0-9a-f]{64}\n$`).Match(raw) {
		t.Errorf("key file %q", raw)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dawngate.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		cfg  config
		want string
	}{
		{config{gmsDir: dir, gmsControlBind: "0.0.0.0"}, "--gms-control-bind must be one IPv4 address"},
		{config{gmsDir: dir, gmsControlBind: "192.168.1.10"}, "puts the control port on the network, which only agents need: add --agent-key"},
		{config{agentKey: filepath.Join(dir, "missing.key")}, "agent key"},
		{config{agentKey: key, gmsExternal: true, gmsPort: 20042}, "--gms-dir/--agent-key and --gms-external exclude each other"},
	} {
		c.cfg.bind, c.cfg.certs, c.cfg.logDir, c.cfg.gmsPorts = "127.0.0.1", testCerts(t), t.TempDir(), "20042-20043"
		if in, err := start(c.cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			if in != nil {
				in.stop()
			}
			t.Errorf("start %+v: %v, want %q", c.cfg, err, c.want)
		}
	}
	agent := func() config {
		return config{agent: "127.0.0.1:10040", agentKey: key, gmsDir: dir, gmsPorts: "10042-10051", bind: "127.0.0.1",
			publicHost: "127.0.0.1", agentName: "hostA", logDir: t.TempDir()}
	}
	for _, c := range []struct {
		change func(*config)
		want   string
	}{
		{func(c *config) { c.agentKey = "" }, "--agent needs --agent-key"},
		{func(c *config) { c.gmsDir = "" }, "--agent needs --gms-dir"},
		{func(c *config) { c.gmsDir = t.TempDir() }, "no Dawngate.exe"},
		{func(c *config) { c.agent = "127.0.0.1" }, "bad server control address"},
		{func(c *config) { c.agent = "192.168.1.10:10040" }, "the GMS host announced to clients is loopback 127.0.0.1 (set --public-host)"},
		{func(c *config) { c.bind = "0.0.0.0" }, "not the wildcard"},
		{func(c *config) { c.agentName = "local" }, `bad agent name "local"`},
		{func(c *config) { c.gmsPorts = "x" }, "bad port range"},
	} {
		cfg := agent()
		c.change(&cfg)
		if ag, err := startAgent(cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			if ag != nil {
				ag.stop()
			}
			t.Errorf("agent %+v: %v, want %q", cfg, err, c.want)
		}
	}
	c := config{redirectorPort: 1, blazePort: 2, qosPort: 3, httpPort: 4, telemetryPort: 5, gmsControlPort: 6, agentKey: key}
	if got := fmt.Sprint(c.printPorts()); got != "[1 2 3 4 5 6]" {
		t.Errorf("print ports with --agent-key: %s", got)
	}
	c.agent = "127.0.0.1:10040"
	if got := c.printPorts(); got != nil {
		t.Errorf("print ports in agent mode: %v", got)
	}
}
