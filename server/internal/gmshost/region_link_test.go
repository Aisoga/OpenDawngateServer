package gmshost

import (
	"net/netip"
	"testing"
)

// REGION5: the relay's annotation of a verifyHello from the match gateway's sessions.
func TestFlowAnnotator(t *testing.T) {
	flow := netip.MustParseAddrPort("127.0.0.1:50001")
	ann := FlowAnnotator(func(a netip.AddrPort) (int64, []int64, bool) {
		if a == flow {
			return 3, []int64{7, 8}, true
		}
		return 0, nil, false
	})
	for _, c := range []struct{ in, want string }{
		{`{"t":"verifyHello","addr":"127.0.0.1:50001","user":7}`, `{"t":"verifyHello","addr":"127.0.0.1:50001","user":7,"flow":{"match":3,"users":[7,8]}}`},
		{`{"t":"verifyHello","addr":"127.0.0.1:50001","user":7}` + "\n", `{"t":"verifyHello","addr":"127.0.0.1:50001","user":7,"flow":{"match":3,"users":[7,8]}}`},
		{`{"t":"verifyHello","addr":"127.0.0.1:50002","user":7}`, `{"t":"verifyHello","addr":"127.0.0.1:50002","user":7}`}, // no flow
		{`{"t":"verifyHello","addr":"bad","user":7}`, `{"t":"verifyHello","addr":"bad","user":7}`},
		{`not json`, `not json`},
	} {
		got := string(ann(3, []byte(c.in)))
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	msg, err := Decode([]byte(`{"t":"verifyHello","addr":"127.0.0.1:50001","user":7,"flow":{"match":3,"users":[7,8]}}`))
	v, ok := msg.(VerifyHello)
	if err != nil || !ok || v.Flow == nil || v.Flow.Match != 3 || len(v.Flow.Users) != 2 {
		t.Errorf("decoded %+v %v", msg, err)
	}
	line, _ := Encode(VerifyHello{Addr: "127.0.0.1:1", User: 2})
	if string(line) != `{"t":"verifyHello","addr":"127.0.0.1:1","user":2}`+"\n" && string(line) != `{"t":"verifyHello","addr":"127.0.0.1:1","user":2}` {
		t.Errorf("a host's verifyHello without a flow: %s", line)
	}
}

// A registration clears an earlier refusal and records the levels the offer gives (LinkConfig.Levels).
func TestMachineRegistered(t *testing.T) {
	m := NewManager(Config{Link: LinkConfig{Levels: func(offer map[string]string) []string {
		if offer["zz_cm_ring.snap"] != "" {
			return []string{"SM_Map_v5", "CM_Ring"}
		}
		return nil
	}}})
	defer m.Close()
	m.SetRegions("eu", map[string]string{"na1": "na"})
	m.MachineRefused("na1", FellBackVersion)
	m.machineRegistered("na1", map[string]string{"zz_cm_ring.snap": "ab"})
	m.mu.Lock()
	refused, maps := m.reg.refused["na1"], m.reg.maps["na1"]
	m.mu.Unlock()
	if refused != "" || len(maps) != 2 || !offers(maps, "cm_ring") || offers(maps, "MM_Map_v3") {
		t.Errorf("refused %q, maps %v", refused, maps)
	}
	m.machineRegistered("na1", nil) // Levels gives none: the machine hosts no level
	m.mu.Lock()
	maps = m.reg.maps["na1"]
	m.mu.Unlock()
	if maps == nil || offers(maps, "SM_Map_v5") {
		t.Errorf("an empty offer: %v", maps)
	}
}
