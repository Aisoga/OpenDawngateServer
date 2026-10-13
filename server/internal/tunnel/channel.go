package tunnel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// The UDP channel's datagram (both directions):
//
//	+0   uint8   type: 1 = data
//	+1   uint64  session id (big-endian; the gateway finds the session by it)
//	+9   uint64  counter (big-endian; the nonce, per direction from 0)
//	+17  AES-256-GCM ciphertext with the header (+0..+17) as additional data, of:
//	       +0  uint8   kind: 0 data, 1 keepalive (echoed by the gateway)
//	       +1  uint16  service port (the port the game sent to; the port an answer comes from)
//	       +3  [4]byte the game socket's IPv4 address on the player's PC
//	       +7  uint16  the game socket's port
//	       +9  payload
//	     then the 16-byte tag
//
// The nonce is 4 zero bytes and the counter: each direction has its own key, so a nonce never repeats under a key.
const (
	HeaderLen = 17
	InnerLen  = 9
	TagLen    = 16
	// Overhead is what the channel adds to a datagram's payload (42 bytes; plus 28 for IPv4 and UDP).
	Overhead = HeaderLen + InnerLen + TagLen
	// MaxPayload is the largest game datagram the tunnel carries: 1400 + 42 + 28 = 1470 bytes on the wire, below a
	// 1,500-byte MTU. RakNet's first MTU probe (1492 - 28 = 1464 bytes) is dropped, so RakNet falls back to its
	// next size, 1200; QoS's bandwidth probes are 1,200 bytes.
	MaxPayload = 1400
	// WindowSize is the replay window: a datagram up to this many counters behind the newest one still arrives
	// (reordering); an older one, or one seen before, is dropped.
	WindowSize = 1024

	typeData byte = 1

	// KindData carries a game datagram; KindKeepalive keeps a NAT mapping open and is echoed by the gateway, which
	// tells the client that UDP reaches the gateway.
	KindData      byte = 0
	KindKeepalive byte = 1

	exporterLabel = "EXPORTER-dawngate-tunnel-udp-v1"
)

// Errors of Opener.Open.
var (
	ErrShort   = errors.New("tunnel: datagram too short")
	ErrType    = errors.New("tunnel: not a tunnel datagram")
	ErrSession = errors.New("tunnel: datagram of another session")
	ErrAuth    = errors.New("tunnel: datagram failed authentication (wrong key or changed on the way)")
	ErrReplay  = errors.New("tunnel: datagram replayed or too old")
	ErrTooBig  = errors.New("tunnel: payload above MaxPayload")
)

// Keys are a session's two keys: Up encrypts client -> gateway, Down gateway -> client.
type Keys struct{ Up, Down [32]byte }

// DeriveKeys exports a session's keys from its control connection's TLS 1.3 state (RFC 8446 section 7.5's exporter:
// both sides get the same 64 bytes, nobody else can), with the session id as the context.
func DeriveKeys(cs tls.ConnectionState, session uint64) (Keys, error) {
	var k Keys
	if cs.Version != tls.VersionTLS13 {
		return k, fmt.Errorf("tunnel: the control connection is not TLS 1.3")
	}
	b, err := cs.ExportKeyingMaterial(exporterLabel, binary.BigEndian.AppendUint64(nil, session), 64)
	if err != nil {
		return k, err
	}
	copy(k.Up[:], b[:32])
	copy(k.Down[:], b[32:])
	return k, nil
}

// Inner is the encrypted part of a datagram before its payload.
type Inner struct {
	Kind       byte
	Port       uint16  // the service port
	IP         [4]byte // the game socket's address on the player's PC
	ClientPort uint16
}

// Client returns the game socket's address.
func (in Inner) Client() *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4(in.IP[0], in.IP[1], in.IP[2], in.IP[3]).To4(), Port: int(in.ClientPort)}
}

// InnerFor makes the inner header of a game datagram from addr (an IPv4 address) to port.
func InnerFor(kind byte, port int, addr *net.UDPAddr) Inner {
	in := Inner{Kind: kind, Port: uint16(port)}
	if addr != nil {
		copy(in.IP[:], addr.IP.To4())
		in.ClientPort = uint16(addr.Port)
	}
	return in
}

func newAEAD(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Sealer encrypts one direction of a session. It is safe for concurrent use.
type Sealer struct {
	aead    cipher.AEAD
	session uint64
	next    atomic.Uint64
}

// NewSealer returns a sealer for session with key; its counter starts at 0.
func NewSealer(key [32]byte, session uint64) (*Sealer, error) {
	a, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: a, session: session}, nil
}

// Seal returns the datagram for in and payload (a payload above MaxPayload is refused).
func (s *Sealer) Seal(in Inner, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, ErrTooBig
	}
	n := s.next.Add(1) - 1
	out := make([]byte, HeaderLen, HeaderLen+InnerLen+len(payload)+TagLen)
	out[0] = typeData
	binary.BigEndian.PutUint64(out[1:], s.session)
	binary.BigEndian.PutUint64(out[9:], n)
	plain := make([]byte, InnerLen, InnerLen+len(payload))
	plain[0] = in.Kind
	binary.BigEndian.PutUint16(plain[1:], in.Port)
	copy(plain[3:7], in.IP[:])
	binary.BigEndian.PutUint16(plain[7:], in.ClientPort)
	plain = append(plain, payload...)
	return s.aead.Seal(out, nonce(n), plain, out[:HeaderLen]), nil
}

func nonce(n uint64) []byte {
	var b [12]byte
	binary.BigEndian.PutUint64(b[4:], n)
	return b[:]
}

// SessionOf returns the session id of a datagram (0, false if it is no tunnel datagram).
func SessionOf(pkt []byte) (uint64, bool) {
	if len(pkt) < HeaderLen+InnerLen+TagLen || pkt[0] != typeData {
		return 0, false
	}
	return binary.BigEndian.Uint64(pkt[1:]), true
}

// Opener decrypts one direction of a session and keeps its replay window. It is safe for concurrent use.
type Opener struct {
	aead    cipher.AEAD
	session uint64
	mu      sync.Mutex
	win     window
}

// NewOpener returns an opener for session with key.
func NewOpener(key [32]byte, session uint64) (*Opener, error) {
	a, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &Opener{aead: a, session: session}, nil
}

// Open checks and decrypts a datagram: ErrShort, ErrType, ErrSession, ErrAuth or ErrReplay when it is dropped.
func (o *Opener) Open(pkt []byte) (Inner, []byte, error) {
	var in Inner
	if len(pkt) < HeaderLen+InnerLen+TagLen {
		return in, nil, ErrShort
	}
	if pkt[0] != typeData {
		return in, nil, ErrType
	}
	if binary.BigEndian.Uint64(pkt[1:]) != o.session {
		return in, nil, ErrSession
	}
	n := binary.BigEndian.Uint64(pkt[9:])
	o.mu.Lock()
	ok := o.win.check(n)
	o.mu.Unlock()
	if !ok { // before the decryption: a replay costs no AES work
		return in, nil, ErrReplay
	}
	plain, err := o.aead.Open(nil, nonce(n), pkt[HeaderLen:], pkt[:HeaderLen])
	if err != nil {
		return in, nil, ErrAuth
	}
	o.mu.Lock()
	ok = o.win.update(n) // again under the lock: two copies decrypted at once, only one passes
	o.mu.Unlock()
	if !ok {
		return in, nil, ErrReplay
	}
	in.Kind = plain[0]
	in.Port = binary.BigEndian.Uint16(plain[1:])
	copy(in.IP[:], plain[3:7])
	in.ClientPort = binary.BigEndian.Uint16(plain[7:])
	return in, plain[InnerLen:], nil
}

// window is the replay window (RFC 6479's idea): a ring of WindowSize bits, bit n % WindowSize for counter n, valid
// for the WindowSize counters up to top.
type window struct {
	seen bool
	top  uint64
	bits [WindowSize / 64]uint64
}

func (w *window) check(n uint64) bool {
	switch {
	case !w.seen || n > w.top:
		return true
	case w.top-n >= WindowSize:
		return false
	}
	return w.bits[n%WindowSize/64]&(1<<(n%64)) == 0
}

func (w *window) update(n uint64) bool {
	if !w.check(n) {
		return false
	}
	if !w.seen || n > w.top {
		from := w.top + 1
		if !w.seen || n-w.top >= WindowSize {
			w.bits = [WindowSize / 64]uint64{}
		} else {
			for i := from; i <= n; i++ { // clear the bits the window moves over
				w.bits[i%WindowSize/64] &^= 1 << (i % 64)
			}
		}
		w.seen, w.top = true, n
	}
	w.bits[n%WindowSize/64] |= 1 << (n % 64)
	return true
}
