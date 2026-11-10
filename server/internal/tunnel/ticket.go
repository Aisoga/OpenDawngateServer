package tunnel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

// A ticket lets one player's Start-Client into one match on a host machine's match gateway (Gateway.Admit). The main
// server mints it when it puts the player into a game there and hands it over inside the player's own session
// (Addr.RouteAway, the away message); the host machine's agent checks it. Its bytes (all integers big-endian):
//
//	version uint8 (TicketVersion), machine length uint8 + the host machine's name (1-64 bytes), match int64 (the main
//	server's match id), user int64 (the persona id the ticket is for), issued int64 (the main server's Unix time in ms),
//	nonce [16] (random), MAC [32] = HMAC-SHA256(the link's ticket key, ticketLabel || the bytes before the MAC)
//
// The ticket key is derived anew at every registration of the agent's link by both ends and never sent, so a ticket of
// an earlier link fails the MAC. A ticket is never logged.
const (
	TicketVersion = 1
	// TicketLife is how long after its issue a ticket is taken; TicketSkew is how far the corrected clocks may differ
	// (the link's latency between the main server's clock reading and its arrival)
	TicketLife = 120 * time.Second
	TicketSkew = 5 * time.Second
	// MaxMachineName bounds a host machine's name in a ticket
	MaxMachineName = 64
	// MaxTicketLen bounds a ticket on the wire (a session hello, a ticket hello, an away message); a ticket of
	// MaxMachineName is 138 bytes
	MaxTicketLen = 255

	ticketLabel = "dawngate ticket v1"
	ticketFixed = 1 + 1 + 8 + 8 + 8 + 16 // the bytes besides the machine name and the MAC
	ticketMAC   = 32
)

// Ticket is what a ticket says.
type Ticket struct {
	Machine string
	Match   int64
	User    int64
	// Issued is the main server's clock when it minted the ticket (kept in ms)
	Issued time.Time
	Nonce  [16]byte
}

// The reasons CheckTicket and TicketGuard.Check refuse a ticket.
var (
	ErrTicketMalformed = errors.New("ticket: malformed")
	ErrTicketMAC       = errors.New("ticket: wrong MAC (another key: an earlier link, or forged)")
	ErrTicketMachine   = errors.New("ticket: for another host machine")
	ErrTicketExpired   = errors.New("ticket: expired or not yet valid")
	ErrTicketUsed      = errors.New("ticket: already used")
	ErrTicketMatch     = errors.New("ticket: its match doesn't run here")
	ErrTicketNoKey     = errors.New("ticket: no ticket key (the agent's link isn't registered)")
)

// MintTicket writes t as a ticket signed with key. A zero Nonce is made random.
func MintTicket(key []byte, t Ticket) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrTicketNoKey
	}
	if len(t.Machine) == 0 || len(t.Machine) > MaxMachineName {
		return nil, fmt.Errorf("ticket: a machine name of %d bytes (1 to %d)", len(t.Machine), MaxMachineName)
	}
	if t.Nonce == ([16]byte{}) {
		if _, err := rand.Read(t.Nonce[:]); err != nil {
			return nil, err
		}
	}
	b := append(make([]byte, 0, ticketFixed+len(t.Machine)+ticketMAC), TicketVersion, byte(len(t.Machine)))
	b = append(b, t.Machine...)
	b = binary.BigEndian.AppendUint64(b, uint64(t.Match))
	b = binary.BigEndian.AppendUint64(b, uint64(t.User))
	b = binary.BigEndian.AppendUint64(b, uint64(t.Issued.UnixMilli()))
	b = append(b, t.Nonce[:]...)
	return append(b, ticketSum(key, b)...), nil
}

func ticketSum(key, body []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(ticketLabel))
	m.Write(body)
	return m.Sum(nil)
}

// ParseTicket reads a ticket's fields without checking its MAC (Start-Client reads the match to tell its away sessions
// apart; it has no key).
func ParseTicket(b []byte) (Ticket, error) {
	var t Ticket
	if len(b) < 2 || b[0] != TicketVersion {
		return t, ErrTicketMalformed
	}
	n := int(b[1])
	if n == 0 || n > MaxMachineName || len(b) != ticketFixed+n+ticketMAC {
		return t, ErrTicketMalformed
	}
	t.Machine = string(b[2 : 2+n])
	r := b[2+n:]
	t.Match = int64(binary.BigEndian.Uint64(r))
	t.User = int64(binary.BigEndian.Uint64(r[8:]))
	t.Issued = time.UnixMilli(int64(binary.BigEndian.Uint64(r[16:])))
	copy(t.Nonce[:], r[24:40])
	return t, nil
}

// CheckTicket checks a ticket's MAC (in constant time), its machine and its time against now (the checking machine's
// clock corrected to the main server's): issued at most TicketLife ago and at most TicketSkew ahead (the skew is
// allowed both ways). It doesn't know which nonces were used: TicketGuard does.
func CheckTicket(key []byte, machine string, b []byte, now time.Time) (Ticket, error) {
	if len(key) == 0 {
		return Ticket{}, ErrTicketNoKey
	}
	t, err := ParseTicket(b)
	if err != nil {
		return t, err
	}
	body := b[:len(b)-ticketMAC]
	if !hmac.Equal(ticketSum(key, body), b[len(body):]) {
		return t, ErrTicketMAC
	}
	if t.Machine != machine {
		return t, ErrTicketMachine
	}
	if age := now.Sub(t.Issued); age < -TicketSkew || age > TicketLife+TicketSkew {
		return t, ErrTicketExpired
	}
	return t, nil
}

// TicketGuard checks a host machine's tickets: CheckTicket with the current link's key and clock offset, then one use
// per nonce (kept until the ticket expires).
type TicketGuard struct {
	// Machine is this host machine's name (as the main server knows it)
	Machine string
	// Now is the local clock (nil: time.Now)
	Now func() time.Time

	mu     sync.Mutex
	key    []byte
	offset time.Duration
	used   map[[16]byte]time.Time // nonce -> when it may be forgotten (main server's clock)
}

// maxUsedNonces bounds the nonces kept: past it the oldest expired ones go first, and a guard that holds this many
// live ones refuses new tickets (a main server minting thousands a minute is broken or hostile)
const maxUsedNonces = 65536

func (g *TicketGuard) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// SetKey takes a new link's ticket key and the main server's clock as the link's agentReady named it (mainNow, read
// at its arrival): later checks correct the local clock by the difference. The nonces of the last link are forgotten
// (its tickets fail the new key's MAC anyway). A nil key turns the checks off until the next link (ErrTicketNoKey).
func (g *TicketGuard) SetKey(key []byte, mainNow time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.key = append([]byte(nil), key...)
	if key == nil {
		g.key = nil
	}
	g.offset = mainNow.Sub(g.now())
	g.used = map[[16]byte]time.Time{}
}

// Check checks a ticket (CheckTicket) and uses it up: a second check of the same ticket is ErrTicketUsed.
func (g *TicketGuard) Check(b []byte) (Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now().Add(g.offset)
	t, err := CheckTicket(g.key, g.Machine, b, now)
	if err != nil {
		return t, err
	}
	if _, ok := g.used[t.Nonce]; ok {
		return t, ErrTicketUsed
	}
	if len(g.used) >= maxUsedNonces {
		for n, until := range g.used {
			if now.After(until) {
				delete(g.used, n)
			}
		}
		if len(g.used) >= maxUsedNonces {
			return t, ErrTicketUsed
		}
	}
	if g.used == nil {
		g.used = map[[16]byte]time.Time{}
	}
	g.used[t.Nonce] = t.Issued.Add(TicketLife + TicketSkew)
	return t, nil
}
