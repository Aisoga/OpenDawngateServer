package tunnel

import "bytes"

// RakNet's MTU discovery through the tunnel: the game's RakNet opens a connection with
// ID_OPEN_CONNECTION_REQUEST_1 (0x05, the offline magic, the protocol version, zero padding) padded to MTU - 28 bytes,
// first for 1492, then 1200, then 576 (RakPeer.cpp mtuSizes, several attempts each, one a second). The server takes
// the session's MTU from the request's length (RakNet 4.081 RakPeer.cpp: ID_OPEN_CONNECTION_REPLY_1 carries
// length + UDP_HEADER_SIZE, capped at MAXIMUM_MTU_SIZE) and the client uses the MTU of that reply. A 1464-byte
// request doesn't fit the channel (MaxPayload), and dropping it cost the first live connect 6 s (6
// requests dropped, connected at 1200); so the client side of the tunnel cuts the padding to the largest RakNet MTU
// that fits, and the server answers that MTU at once. Only the zero padding goes; the request's content is the same.

// rakNetOfflineMagic is RakNet's OFFLINE_MESSAGE_DATA_ID.
var rakNetOfflineMagic = []byte{0x00, 0xFF, 0xFF, 0x00, 0xFE, 0xFE, 0xFE, 0xFE, 0xFD, 0xFD, 0xFD, 0xFD, 0x12, 0x34, 0x56, 0x78}

const (
	idOpenConnectionRequest1 = 0x05
	// TrimmedProbe is the size an oversized ID_OPEN_CONNECTION_REQUEST_1 is cut to: RakNet's next MTU (1200) less the
	// 28 bytes of IPv4 and UDP headers.
	TrimmedProbe = 1200 - 28
)

// TrimMTUProbe returns p cut to TrimmedProbe bytes when it is a RakNet ID_OPEN_CONNECTION_REQUEST_1 larger than
// MaxPayload whose cut part is zero padding; otherwise p and false.
func TrimMTUProbe(p []byte) ([]byte, bool) {
	if len(p) <= MaxPayload || p[0] != idOpenConnectionRequest1 || !bytes.Equal(p[1:17], rakNetOfflineMagic) {
		return p, false
	}
	for _, b := range p[TrimmedProbe:] {
		if b != 0 {
			return p, false
		}
	}
	return p[:TrimmedProbe], true
}
