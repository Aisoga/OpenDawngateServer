package blaze

import (
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
	"dawngate/server/internal/tunnel"
)

// REGION5: with host machines that have match gateways (RegionRoute set), a creator whose client can't be sent to one
// (a direct client here, no tunnel session) plays at home without a notice, and nothing asks RegionRoute.
func TestRegionNeedsTheTunnel(t *testing.T) {
	asked := make(chan bool, 4)
	naHost := make(chan bool, 1)
	na := &gmshost.FakeLauncher{Run: func(env gmshost.Env, p *gmshost.FakeProcess) {
		naHost <- true
		gmshost.PlayHost(env, p, nil)
	}}
	ts, _ := newCustomHostServer(t, func(s *Server) {
		s.Hosts.SetRegions("eu", map[string]string{"na1": "na"})
		s.Hosts.AddTestMachine("na1", na, gmshost.NewPool(GMSPort+20, GMSPort+20), "")
		s.RegionRoute = func(Game, int64) (tunnel.Away, bool) { asked <- true; return tunnel.Away{}, false }
	})
	ui := attachUI(t, ts)
	_, u := createInRegion(t, ts, ui)
	u.wantRegionState(lobbyStateLine("alpha"))
	if psas, want, notice := ts.lobbyRegions(t); psas != "eu" || want != "eu" || notice != nil {
		t.Errorf("PSAS %q, want %q, notice %+v", psas, want, notice)
	}
	select {
	case <-naHost:
		t.Error("a host started on na1")
	case <-asked:
		t.Error("RegionRoute asked for a direct client")
	case <-time.After(200 * time.Millisecond):
	}
	if log := ts.log.String(); !strings.Contains(log, "region na ignored: player's client has no tunnel session that takes the match gateway") {
		t.Errorf("log lacks the ignored region:\n%s", log)
	}
}
