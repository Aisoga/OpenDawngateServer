// Package gms is the match server (GMS) side of the emulator. So far (roadmap item B8) it only listens
// on the GMS address that GameManager::NotifyGameSetup hands the client and logs what arrives, without
// answering: the client's first RakNet datagrams (docs/GMS_PROTOCOL.md, "Transport").
package gms

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"sync"

	"dawngate/server/internal/logging"
)

const who = "gms"

// OfflineMagic is RakNet's OFFLINE_MESSAGE_DATA_ID, the 16 bytes every unconnected message carries.
var OfflineMagic = []byte{0x00, 0xFF, 0xFF, 0x00, 0xFE, 0xFE, 0xFE, 0xFE, 0xFD, 0xFD, 0xFD, 0xFD, 0x12, 0x34, 0x56, 0x78}

// offlineIDs are RakNet 4's message ids of the unconnected handshake (MessageIdentifiers.h, which the
// client's TransportRakNet_HandleReceives 0x00ADE1B0 matches for the connected ones).
var offlineIDs = map[byte]string{
	0x01: "ID_UNCONNECTED_PING",
	0x02: "ID_UNCONNECTED_PING_OPEN_CONNECTIONS",
	0x05: "ID_OPEN_CONNECTION_REQUEST_1",
	0x06: "ID_OPEN_CONNECTION_REPLY_1",
	0x07: "ID_OPEN_CONNECTION_REQUEST_2",
	0x08: "ID_OPEN_CONNECTION_REPLY_2",
}

// Logger logs the UDP datagrams that reach the GMS port and never answers.
type Logger struct {
	Log  *logging.Log
	Conn *net.UDPConn

	mu   sync.Mutex
	seen map[string]bool // peer + first byte + length already dumped
}

// Serve reads datagrams until the socket is closed.
func (l *Logger) Serve() error {
	buf := make([]byte, 2048)
	for {
		n, from, err := l.Conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			l.Log.Logf(who, "udp read: %v", err) // e.g. WSAECONNRESET after an ICMP; the socket stays usable
			continue
		}
		l.Packet(buf[:n], from)
	}
}

// Packet logs one datagram: a line with its RakNet reading for each, and a hex dump of the first
// datagram of each peer, kind and length.
func (l *Logger) Packet(p []byte, from net.Addr) {
	peer := logging.Peer(from)
	l.Log.Logf(who, "udp %s %d bytes: %s", peer, len(p), Describe(p))
	key := fmt.Sprintf("%s/%d/%d", peer, firstByte(p), len(p))
	l.mu.Lock()
	if l.seen == nil {
		l.seen = map[string]bool{}
	}
	first := !l.seen[key]
	l.seen[key] = true
	l.mu.Unlock()
	if first {
		l.Log.Log(who, "   first of its kind:\n"+dumpTrimmed(p))
	}
}

func firstByte(p []byte) int {
	if len(p) == 0 {
		return -1
	}
	return int(p[0])
}

// Describe reads a datagram the way a RakNet 4 server would: an unconnected handshake message (first
// byte, the offline magic, and for ID_OPEN_CONNECTION_REQUEST_1 the protocol version byte; the rest is
// zero padding up to the MTU being probed), or a connected datagram (bit 0x80 set).
func Describe(p []byte) string {
	if len(p) == 0 {
		return "empty"
	}
	id := p[0]
	if name, ok := offlineIDs[id]; ok {
		s := fmt.Sprintf("0x%02X %s", id, name)
		switch id {
		case 0x05, 0x07:
			if len(p) >= 17 && bytes.Equal(p[1:17], OfflineMagic) {
				s += ", offline magic"
				if id == 0x05 && len(p) >= 18 {
					s += fmt.Sprintf(", protocol version %d, padded to %d bytes (MTU probe)", p[17], len(p))
				}
			} else {
				s += ", no offline magic"
			}
		case 0x01, 0x02:
			if len(p) >= 25 && bytes.Equal(p[9:25], OfflineMagic) {
				s += ", offline magic"
			}
		}
		return s
	}
	if id&0x80 != 0 {
		return fmt.Sprintf("0x%02X connected datagram (RakNet bit 0x80)", id)
	}
	return fmt.Sprintf("0x%02X unknown", id)
}

// dumpTrimmed hex-dumps p without its trailing zero padding (noted instead).
func dumpTrimmed(p []byte) string {
	end := len(p)
	for end > 0 && p[end-1] == 0 {
		end--
	}
	if end+16 >= len(p) { // not worth trimming
		return logging.Hexdump(p)
	}
	return logging.Hexdump(p[:end]) + fmt.Sprintf("\n    (then %d zero bytes)", len(p)-end)
}
