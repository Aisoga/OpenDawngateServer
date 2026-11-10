package tunnel

import (
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

var testTicketKey = func() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}()

func testTicket() Ticket {
	t := Ticket{Machine: "na1", Match: 0x0102030405060708, User: 2000000001, Issued: time.UnixMilli(1762700000000)}
	for i := range t.Nonce {
		t.Nonce[i] = 0xA0 + byte(i)
	}
	return t
}

// TestTicketWire pins a ticket's bytes (the MAC computed independently with Python's hmac over the same fields) and
// reads them back.
func TestTicketWire(t *testing.T) {
	const want = "01036e6131010203040506070800000000773594010000019a691b7b00a0a1a2a3a4a5a6a7a8a9aaabacadaeaf" +
		"61b4dfb1d4cdf1be4989539a7f38bf8a004966374d3cb41b039fa11e0848bb95"
	b, err := MintTicket(testTicketKey, testTicket())
	if err != nil || hex.EncodeToString(b) != want {
		t.Fatalf("MintTicket = %x, %v\nwant          %s", b, err, want)
	}
	back, err := ParseTicket(b)
	if err != nil || back != testTicket() {
		t.Errorf("ParseTicket = %+v, %v", back, err)
	}
	if _, err := MintTicket(testTicketKey, Ticket{Machine: string(make([]byte, MaxMachineName+1))}); err == nil {
		t.Error("a machine name over the limit was minted")
	}
	if _, err := MintTicket(nil, testTicket()); !errors.Is(err, ErrTicketNoKey) {
		t.Errorf("no key: %v", err)
	}
	r, _ := MintTicket(testTicketKey, Ticket{Machine: "na1"})
	if p, _ := ParseTicket(r); p.Nonce == ([16]byte{}) {
		t.Error("a zero nonce wasn't made random")
	}
	for _, bad := range [][]byte{nil, {1}, {2, 3}, b[:len(b)-1], append(append([]byte{}, b...), 0)} {
		if _, err := ParseTicket(bad); !errors.Is(err, ErrTicketMalformed) {
			t.Errorf("ParseTicket(%x) = %v", bad, err)
		}
	}
}

// TestCheckTicket: good, expired (both ways, with the skew), another machine, another key, a changed byte.
func TestCheckTicket(t *testing.T) {
	tk := testTicket()
	b, _ := MintTicket(testTicketKey, tk)
	for _, c := range []struct {
		name string
		key  []byte
		mach string
		at   time.Duration // after Issued
		b    []byte
		want error
	}{
		{"good", testTicketKey, "na1", time.Second, b, nil},
		{"at its life's end", testTicketKey, "na1", TicketLife + TicketSkew, b, nil},
		{"expired", testTicketKey, "na1", TicketLife + TicketSkew + time.Millisecond, b, ErrTicketExpired},
		{"a little ahead", testTicketKey, "na1", -TicketSkew, b, nil},
		{"from the future", testTicketKey, "na1", -TicketSkew - time.Millisecond, b, ErrTicketExpired},
		{"other machine", testTicketKey, "eu1", time.Second, b, ErrTicketMachine},
		{"other key (an earlier link)", make([]byte, 32), "na1", time.Second, b, ErrTicketMAC},
		{"no key", nil, "na1", time.Second, b, ErrTicketNoKey},
		{"a changed byte", testTicketKey, "na1", time.Second, func() []byte { x := append([]byte{}, b...); x[10] ^= 1; return x }(), ErrTicketMAC},
	} {
		got, err := CheckTicket(c.key, c.mach, c.b, tk.Issued.Add(c.at))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if c.want == nil && got != tk {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// TestTicketGuard: the clock offset from agentReady.now, one use, a new link's key forgets the old one's tickets.
func TestTicketGuard(t *testing.T) {
	local := time.Unix(1000000, 0) // the host machine's clock: far from the main server's
	g := &TicketGuard{Machine: "na1", Now: func() time.Time { return local }}
	if _, err := g.Check([]byte{1}); err == nil {
		t.Error("a check without a key passed")
	}
	main := time.UnixMilli(1762700000000)
	g.SetKey(testTicketKey, main) // agentReady.now, read at its arrival
	tk := testTicket()
	tk.Issued = main.Add(2 * time.Second)
	b, _ := MintTicket(testTicketKey, tk)
	local = local.Add(3 * time.Second) // a second after the issue, on the main server's clock
	if got, err := g.Check(b); err != nil || got.Match != tk.Match || got.User != tk.User {
		t.Fatalf("a good ticket: %+v, %v", got, err)
	}
	if _, err := g.Check(b); !errors.Is(err, ErrTicketUsed) {
		t.Errorf("reused: %v", err)
	}
	tk.Nonce[0]++
	late, _ := MintTicket(testTicketKey, tk)
	local = local.Add(TicketLife + TicketSkew)
	if _, err := g.Check(late); !errors.Is(err, ErrTicketExpired) {
		t.Errorf("expired: %v", err)
	}
	tk.Nonce[0]++
	tk.Issued = main.Add(TicketLife + 4*time.Second)
	other, _ := MintTicket(testTicketKey, tk)
	g.SetKey(make([]byte, 32), main.Add(TicketLife+4*time.Second)) // the link registered again
	if _, err := g.Check(other); !errors.Is(err, ErrTicketMAC) {
		t.Errorf("a ticket of the last link: %v", err)
	}
}

// TestTicketAdmit: a match gateway's Admit takes a good ticket for a running match, refuses another match, and a
// ticket only once.
func TestTicketAdmit(t *testing.T) {
	now := time.UnixMilli(1762700000000)
	g := &TicketGuard{Machine: "na1", Now: func() time.Time { return now }}
	g.SetKey(testTicketKey, now)
	host := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 10042}
	admit := TicketAdmit(g, func(m int64) (*net.UDPAddr, bool) { return host, m == 7 })
	tk := testTicket()
	tk.Issued, tk.Match = now, 7
	b, _ := MintTicket(testTicketKey, tk)
	a, err := admit(b, netip.Addr{})
	if err != nil || a.Match != 7 || a.User != tk.User || a.Port != 10042 || a.Target != host {
		t.Fatalf("admit = %+v, %v", a, err)
	}
	if _, err := admit(b, netip.Addr{}); !errors.Is(err, ErrTicketUsed) {
		t.Errorf("reused: %v", err)
	}
	tk.Match, tk.Nonce[0] = 8, 1
	b, _ = MintTicket(testTicketKey, tk)
	if _, err := admit(b, netip.Addr{}); !errors.Is(err, ErrTicketMatch) {
		t.Errorf("a match not running here: %v", err)
	}
}
