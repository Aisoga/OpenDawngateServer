package gmshost

import (
	"io"
	"net/netip"
	"testing"

	"dawngate/server/internal/logging"
)

// TestRelayFilter: the relay reads a host's line as the main server does (Decode), so a flow of the host's own is
// dropped however it is spelled (another case, escapes, a "T" for "t"), and a verifyHello reaches the annotation
// rewritten with its own fields only.
func TestRelayFilter(t *testing.T) {
	flow := netip.MustParseAddrPort("127.0.0.1:50001")
	r := &relay{a: &Agent{Log: logging.New(io.Discard), Link: AgentLink{Annotate: FlowAnnotator(func(a netip.AddrPort) (int64, []int64, bool) {
		if a == flow {
			return 3, []int64{7}, true
		}
		return 0, nil, false
	})}}}
	for _, line := range []string{
		`{"t":"verifyHello","addr":"127.0.0.1:50002","user":9,"flow":{"match":3,"users":[9]}}`,
		`{"t":"verifyHello","addr":"127.0.0.1:50002","user":9,"Flow":{"match":3,"users":[9]}}`,
		`{"t":"verifyHello","addr":"127.0.0.1:50002","user":9,"FLOW":{"match":3,"users":[9]}}`,
		`{"t":"verifyHello","addr":"127.0.0.1:50002","user":9,"\u0066low":{"match":3,"users":[9]}}`,
		`{"T":"verifyHello","addr":"127.0.0.1:50002","user":9,"flow":{"match":3,"users":[9]}}`,
		`{"t":"verifyHell\u006f","addr":"127.0.0.1:50002","user":9,"flow":{"match":3,"users":[9]}}`,
		`{"t":"gameReady","t":"verifyHello","addr":"127.0.0.1:50002","user":9,"Flow":{"match":3,"users":[9]}}`,
	} {
		out := r.filter(3, []byte(line))
		if out == nil {
			continue
		}
		// whatever passes must not give the main server a flow the relay didn't add
		if msg, err := Decode(out); err == nil {
			if v, ok := msg.(VerifyHello); ok && v.Flow != nil {
				t.Errorf("%s passed as %s", line, out)
			}
		}
	}
	for _, c := range []struct{ in, want string }{
		// a flow of the gateway's: annotated, the line rewritten from its fields
		{`{"t":"verifyHello","user":7,"addr":"127.0.0.1:50001","extra":1}`, `{"t":"verifyHello","addr":"127.0.0.1:50001","user":7,"flow":{"match":3,"users":[7]}}`},
		{`{"T":"verifyHello","Addr":"127.0.0.1:50001","User":7}`, `{"t":"verifyHello","addr":"127.0.0.1:50001","user":7,"flow":{"match":3,"users":[7]}}`},
		// no flow there: on without one
		{`{"t":"verifyHello","addr":"127.0.0.1:50002","user":9}`, `{"t":"verifyHello","addr":"127.0.0.1:50002","user":9}`},
		// other lines as they came
		{`{"t":"gameReady"}`, `{"t":"gameReady"}`},
		{`not json`, `not json`},
	} {
		if got := string(r.filter(3, []byte(c.in))); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}
