package blaze

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"dawngate/server/internal/accounts"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

// a tunnelled client is told its tunnel address with the server's ports; a direct one the server's address.
func TestTunnelledClientAddresses(t *testing.T) {
	gms, _ := ParseAddr("203.0.113.5", 10044)
	g := Game{ID: 3, GMS: gms}
	direct := &Conn{}
	tunnelled := &Conn{tun: &tunnel.Addr{IP: net.ParseIP("127.0.0.1").To4(), Port: 10041, Session: 9}}
	var none *Conn

	if got := direct.Host("203.0.113.5"); got != "203.0.113.5" {
		t.Errorf("direct Host %q", got)
	}
	if got := none.Host("203.0.113.5"); got != "203.0.113.5" {
		t.Errorf("nil conn Host %q", got)
	}
	if got := tunnelled.Host("203.0.113.5"); got != "127.0.0.1" {
		t.Errorf("tunnelled Host %q", got)
	}
	if got := direct.ForClient(g).GMS; got != gms {
		t.Errorf("direct GMS %v", got)
	}
	want, _ := ParseAddr("127.0.0.1", 10044)
	if got := tunnelled.ForClient(g).GMS; got != want {
		t.Errorf("tunnelled GMS %v, want %v", got, want)
	}
	if g.GMS != gms {
		t.Error("ForClient changed the caller's game")
	}
	listed := tunnelled.forClientListed([]ListedGame{{Game: g}})
	if listed[0].Game.GMS != want {
		t.Errorf("listed GMS %v", listed[0].Game.GMS)
	}
	// the preAuth reply's ping site and the postAuth reply's telemetry server carry the conn's host
	st, err := tdf.Decode(PreAuthReply(tunnelled.Host("203.0.113.5"), 17502, Service))
	if err != nil {
		t.Fatal(err)
	}
	qoss, _ := st.Sub("QOSS")
	bwps, _ := qoss.Sub("BWPS")
	if psa, _ := bwps.Str("PSA"); psa != "127.0.0.1" {
		t.Errorf("ping site %q", psa)
	}
}

// a tunnelled client's source address is the player's real address from the gateway (tunnel.Addr.Peer),
// not the loopback address its game socket has on the player's PC, so the sign-up cap per address counts it.
func TestTunnelledClientSourceAddress(t *testing.T) {
	peer := netip.MustParseAddr("203.0.113.7")
	tunnelled := &Conn{tun: &tunnel.Addr{IP: net.ParseIP("127.0.0.1").To4(), Port: 10041, Session: 9, Peer: peer}}
	if got := tunnelled.Addr(); got != peer {
		t.Fatalf("tunnelled Addr %v, want %v", got, peer)
	}
	if got := (&Conn{}).Addr(); got.IsValid() {
		t.Errorf("a conn without a socket has the address %v", got)
	}
	s := &Server{Store: store.New(), Accounts: accounts.Policy{Mode: accounts.Open, SignupsPerAddress: 1}}
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	if r := s.authenticate("first@example.com", "dawngate1", tunnelled.Addr(), now); r.Code != 0 {
		t.Fatalf("first sign-up through the tunnel: %#x %s", r.Code, r.Note)
	}
	if r := s.authenticate("second@example.com", "dawngate1", tunnelled.Addr(), now); r.Code != accounts.CodeBusy {
		t.Errorf("second sign-up from the same public address through the tunnel: %#x %s, want CodeBusy", r.Code, r.Note)
	}
	other := &Conn{tun: &tunnel.Addr{IP: net.ParseIP("127.0.0.1").To4(), Port: 10041, Session: 10,
		Peer: netip.MustParseAddr("198.51.100.4")}}
	if r := s.authenticate("third@example.com", "dawngate1", other.Addr(), now); r.Code != 0 {
		t.Errorf("a sign-up from another address: %#x %s", r.Code, r.Note)
	}
}
