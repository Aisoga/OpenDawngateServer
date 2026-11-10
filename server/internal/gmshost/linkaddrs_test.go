package gmshost

import (
	"net"
	"strings"
	"testing"
)

// TestCheckLinkAddrs: a TLS-linked machine's GMS host and match gateway may be loopback addresses only over a link
// from this machine (the lanes' host machines on 127.0.2.x), private ones only over a link from a private network or
// this machine, and never link-local, 0.0.0.0/8 or reserved; addresses on the internet always.
func TestCheckLinkAddrs(t *testing.T) {
	lo, lan, wan := net.ParseIP("127.0.2.20"), net.ParseIP("192.168.1.40"), net.ParseIP("203.0.113.9")
	for _, c := range []struct {
		src             net.IP
		public, gateway string
		want            string // "" = taken
	}{
		{lo, "127.0.2.20", "127.0.2.20:42128", ""},
		{lo, "192.168.1.31", "", ""},
		{lan, "192.168.1.31", "192.168.1.31:42128", ""},
		{lan, "203.0.113.9", "203.0.113.9:42128", ""},
		{wan, "198.51.100.7", "198.51.100.7:42128", ""},
		{wan, "127.0.1.10", "198.51.100.7:42128", "public host 127.0.1.10 is a loopback address"},
		{wan, "198.51.100.7", "127.0.0.1:42128", "match gateway 127.0.0.1 is a loopback address"},
		{wan, "10.0.0.5", "", "public host 10.0.0.5 is in a private network"},
		{wan, "198.51.100.7", "100.64.3.4:42128", "match gateway 100.64.3.4 is in a private network"},
		{lan, "127.0.0.1", "", "public host 127.0.0.1 is a loopback address"},
		{lo, "169.254.1.1", "", "public host 169.254.1.1 is no address players can reach"},
		{wan, "0.1.2.3", "", "public host 0.1.2.3 is no address players can reach"},
		{wan, "198.51.100.7", "240.0.0.1:42128", "match gateway 240.0.0.1 is no address players can reach"},
	} {
		err := checkLinkAddrs(AgentAuth{Public: c.public, Gateway: c.gateway}, c.src)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("from %v, public %s, gateway %s: %v, want %q", c.src, c.public, c.gateway, err, c.want)
		}
	}

	// on the link: a machine offering a link-local GMS host is rejected
	lm := newLinkMain(t, nil, nil)
	o := linkOffer
	o.Public = "169.254.1.1"
	rl := dialLink(t, lm)
	rl.auth(keyNA2, "na2", rl.hello(keyNA2, "na2", linkVersion), o, rl.exp)
	rl.closed()
	waitLog(t, lm.log, `rejected: public host 169.254.1.1 is no address players can reach`)
}
