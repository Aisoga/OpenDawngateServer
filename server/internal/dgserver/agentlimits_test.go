package dgserver

import (
	"os"
	"path/filepath"
	"testing"

	"dawngate/server/internal/limits"
	"dawngate/server/internal/logging"
)

// TestAgentLogLimits: a host machine's log gets a public server's limits whenever its limits are on (its match gateway
// and relay port are public, and every refused connection is a line), and none with them off.
func TestAgentLogLimits(t *testing.T) {
	key := filepath.Join(t.TempDir(), "agent.key")
	if err := writeAgentKey(key); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dawngate.exe"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		mode limits.Mode
		want logging.Limits
	}{
		{limits.On, logging.PublicLimits()},
		{limits.Off, logging.Limits{}},
	} {
		// a main server that isn't there: the agent keeps trying in the background until stop
		ag, err := startAgent(config{agent: "127.0.9.153:9", agentKey: key, gmsDir: dir, gmsPorts: "10042-10051",
			bind: "127.0.0.1", publicHost: "127.0.0.1", agentName: "hostA", logDir: t.TempDir(), limitsMode: c.mode})
		if err != nil {
			t.Fatal(err)
		}
		got := ag.log.Limits()
		ag.stop()
		if got != c.want {
			t.Errorf("--limits %v: the agent's log limits %+v, want %+v", c.mode, got, c.want)
		}
	}
}
