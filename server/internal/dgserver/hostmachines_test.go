package dgserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/version"
)

// REGION1: dgserver as a main server that takes a host machine through its tunnel gateway (--host-machine), and
// dgserver in agent mode over the TLS link (--agent-tls, --agent-pin), in one process. The agent's hosts are this
// test binary (TestMain's fake host), which dial the agent's relay; the match's lines travel in the link.
func TestAgentTLSMode(t *testing.T) {
	prepareHosts = func() error { return nil } // no launcher event: the "Dawngate.exe" is this binary
	t.Cleanup(func() { prepareHosts = gmshost.LauncherEvent })
	key := filepath.Join(t.TempDir(), "na1.key")
	if err := writeAgentKey(key); err != nil {
		t.Fatal(err)
	}
	const ip = "127.0.9.41"
	mainLog, agentLog := t.TempDir(), t.TempDir()
	in, err := start(config{bind: ip, certs: testCerts(t), logDir: mainLog, redirectorPort: redirector.Port, tunnel: TunnelOn,
		gmsPorts: "20044-20047", regions: regionFlags{region: "eu", machines: stringList{"na1=na:" + key}}})
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
	gw := in.gateway.Addr()
	ag, err := startAgent(config{agentKey: key, agentName: "na1", gmsDir: exe, gmsArgs: "-multipleInstances",
		gmsPorts: "20044-20045", bind: "127.0.0.1", publicHost: "127.0.0.1", logDir: agentLog,
		regions: regionFlags{agentTLS: gw, agentPin: in.gateway.Identity.Pin.String()}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if ag != nil {
			ag.stop()
		}
	}()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, ok := in.hosts.Machine("na1"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host machine did not register")
		}
	}
	t.Setenv(hostEnv, "finish") // the agent's ExecLauncher passes this process's environment on
	end := make(chan gmshost.Outcome, 1)
	mt, err := in.hosts.StartIn(gmshost.CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: gmshost.ModePractice, Seed: 7919,
		Roster: []gmshost.RosterEntry{{User: 2000000001, Name: "player", Team: 1}}},
		gmshost.Events{End: func(mt *gmshost.Match, o gmshost.Outcome) { end <- o }}, gmshost.Want{Region: "na"}) // REGION5: regions on
	if err != nil {
		t.Fatal(err)
	}
	if mt.Site != "na1" || mt.Bind != "127.0.0.1:20044" {
		t.Errorf("match %+v", mt)
	}
	select {
	case o := <-end:
		if !o.Ready || !o.Finished || o.Killed || o.ExitCode != 0 {
			t.Errorf("outcome %+v", o)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the match did not end")
	}
	pid := mt.Pid()
	ag.stop()
	ag = nil
	in.stop()
	in = nil

	main, agent := readLog(t, mainLog), readLog(t, agentLog)
	for _, want := range []string{
		"host machines na1 (na) through the gateway",
		`[host] host machine "na1" (region na) from ('127.0.0.1', `,
		`) registered over the TLS link (` + version.String() + `): GMS host 127.0.0.1, bind 127.0.0.1, ports 20044-20045, match gateway none`,
		fmt.Sprintf("[host] match 1: hello from pid %d (dgserver test host finish)", pid),
		": finished (winner team 1): agent test; exit code 0, killed false; port 20044 free",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main server log lacks %q:\n%s", want, main)
		}
	}
	for _, want := range []string{
		`[agent] the hosts' control port 127.0.0.1:`,
		fmt.Sprintf(`[agent] registered with %s over the TLS link as "na1" (seen from 127.0.0.1; main server started `, gw),
		fmt.Sprintf("[agent] match 1: the host's control connection relayed (pid %d)", pid),
		fmt.Sprintf("[agent] match 1: pid %d exited with code 0", pid),
		"[agent] stopped",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("agent log lacks %q:\n%s", want, agent)
		}
	}
}

// The REGION1 settings that stop a start.
func TestHostMachineConfig(t *testing.T) {
	for _, c := range []struct{ in, name, region, file, err string }{
		{in: "na1=na:keys/na1.key", name: "na1", region: "na", file: "keys/na1.key"},
		{in: `na1=na:C:\Dawngate-Data\agents\na1.key`, name: "na1", region: "na", file: `C:\Dawngate-Data\agents\na1.key`},
		{in: "na1=na", err: "want NAME=REGION:KEYFILE"},
		{in: "na1:na.key", err: "want NAME=REGION:KEYFILE"},
		{in: "na1=NA:k", err: "2-8 lower-case letters"},
	} {
		name, region, file, err := parseHostMachine(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: %v, want %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil || name != c.name || region != c.region || file != c.file {
			t.Errorf("%s: %q %q %q %v", c.in, name, region, file, err)
		}
	}
	key := filepath.Join(t.TempDir(), "na1.key")
	if err := writeAgentKey(key); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		cfg  config
		want string
	}{
		{config{tunnel: TunnelOff, regions: regionFlags{machines: stringList{"na1=na:" + key}}}, "--host-machine needs the tunnel gateway (--tunnel on)"},
		{config{tunnel: TunnelOn, regions: regionFlags{machines: stringList{"na1=na:" + key, "na1=na:" + key}}}, "--host-machine na1 twice"},
		{config{tunnel: TunnelOn, regions: regionFlags{machines: stringList{"na1=na:" + key + ".missing"}}}, "agent key"},
		{config{regions: regionFlags{region: "Europe"}}, `--region "Europe"`},
	} {
		if _, _, err := c.cfg.linkConfig(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.cfg.regions, err, c.want)
		}
	}
	lc, note, err := config{tunnel: TunnelOn, gmsPorts: "10042-10051", gmsOptions: "net_limits=1",
		regions: regionFlags{region: "eu", machines: stringList{"na1=na:" + key}}}.linkConfig()
	if err != nil || note != "host machines na1 (na) through the gateway" || lc.Machines["na1"].Region != "na" ||
		len(lc.Machines["na1"].Key) != 64 || lc.Ports != "10042-10051" || lc.Options != "net_limits=1" || lc.Version != version.String() {
		t.Errorf("link config %+v %q %v", lc, note, err)
	}
	ag := &gmshost.Agent{}
	for _, c := range []struct {
		r    regionFlags
		agt  string
		want string
	}{
		{regionFlags{agentPin: strings.Repeat("ab", 32)}, "", "--agent-pin needs --agent-tls"},
		{regionFlags{agentTLS: "203.0.113.1:42128"}, "", "--agent-tls needs --agent-pin"},
		{regionFlags{agentTLS: "203.0.113.1", agentPin: strings.Repeat("ab", 32)}, "", "want the main server's gateway as host:port"},
		{regionFlags{agentTLS: "203.0.113.1:42128", agentPin: strings.Repeat("ab", 32)}, "127.0.0.1:10040", "--agent and --agent-tls exclude each other"},
	} {
		if err := (config{agent: c.agt, regions: c.r}).agentLink(ag); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.r, err, c.want)
		}
	}
	if err := (config{gmsControlPort: 10040, bind: "127.0.0.1", regions: regionFlags{agentTLS: "dawngate.example:42128",
		agentPin: strings.Repeat("ab", 32)}}).agentLink(ag); err != nil {
		t.Fatal(err)
	}
	if ag.Server != "dawngate.example:42128" || ag.Build != version.String() || ag.Link.Relay != "127.0.0.1:10040" ||
		ag.Link.SharedIPs != "127.0.0.1" || ag.Link.Pin.String() != strings.Repeat("ab", 32) {
		t.Errorf("agent %+v", ag)
	}
	if got := (config{gmsControlPort: 10040, regions: regionFlags{agentTLS: "x:1"}}).printPorts(); fmt.Sprint(got) != "[10040]" {
		t.Errorf("--print-ports in TLS agent mode: %v", got)
	}
}
