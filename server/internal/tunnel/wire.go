package tunnel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"

	build "dawngate/server/internal/version"
)

// The hellos at the start of every TLS connection to the gateway (all integers big-endian):
//
//	client -> gateway: "DGTN", version uint8 (1), kind uint8, then by kind:
//	  1 session: tunnel address [4] (the loopback address the player copy is patched for), resume id uint64,
//	             resume token [16] (zeros for a new session; a session that lost its control connection is taken
//	             up again within Grace, with new UDP keys)
//	  2 stream:  session id uint64, token [16], port uint16 (the server port it opens), the game socket's
//	             address [4] and port uint16
//	  3 info:    nothing more (no session: Start-Client's list and login ask the server's version this way; a gateway
//	             of a release before the info hello closes the connection at the unknown kind)
//	gateway -> client:
//	  session: status uint8, session id uint64, token [16], resumed uint8, max payload uint16,
//	           TCP port count uint8 + ports uint16 each, UDP port count uint8 + ports uint16 each
//	  stream:  status uint8 (0: the bytes that follow are the port's)
//	  info:    status uint8, version length uint8 + the build's version (version.String, at most version.MaxLen
//	           bytes), then the server's facts: a count uint8 and per fact a key and a value, each a length uint8
//	           and its bytes (e.g. "signup": open, invite, closed or local); then the gateway closes the connection.
//	           A reply that ends after the version (a release before the facts) has none.
//
// After a session's reply both sides derive the UDP keys (DeriveKeys) and the control connection carries only
// heartbeats (one byte 0 every HeartbeatEvery, both ways); it ends the session's life, after Grace.
const (
	magic   = "DGTN"
	version = 1

	kindSession byte = 1
	kindStream  byte = 2
	kindInfo    byte = 3
)

// Status codes of the gateway's replies.
const (
	StatusOK          byte = 0
	StatusVersion     byte = 1 // another protocol version
	StatusNoSession   byte = 2 // the session id or token is unknown
	StatusPort        byte = 3 // the port isn't one the gateway serves
	StatusAddress     byte = 4 // the tunnel address isn't a loopback address
	StatusUnavailable byte = 5 // the gateway is closing or full
)

// StatusText names a status for log lines and errors.
func StatusText(s byte) string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusVersion:
		return "unsupported tunnel version"
	case StatusNoSession:
		return "unknown session"
	case StatusPort:
		return "port not served"
	case StatusAddress:
		return "tunnel address not a loopback address"
	case StatusUnavailable:
		return "gateway unavailable"
	}
	return fmt.Sprintf("status %d", s)
}

type sessionHello struct {
	tunnelIP    [4]byte
	resumeID    uint64
	resumeToken [16]byte
}

type streamHello struct {
	session    uint64
	token      [16]byte
	port       uint16
	clientIP   [4]byte
	clientPort uint16
}

type sessionReply struct {
	status     byte
	session    uint64
	token      [16]byte
	resumed    bool
	maxPayload uint16
	tcp, udp   []uint16
}

func (h sessionHello) encode() []byte {
	b := append([]byte(magic), version, kindSession)
	b = append(b, h.tunnelIP[:]...)
	b = binary.BigEndian.AppendUint64(b, h.resumeID)
	return append(b, h.resumeToken[:]...)
}

func (h streamHello) encode() []byte {
	b := append([]byte(magic), version, kindStream)
	b = binary.BigEndian.AppendUint64(b, h.session)
	b = append(b, h.token[:]...)
	b = binary.BigEndian.AppendUint16(b, h.port)
	b = append(b, h.clientIP[:]...)
	return binary.BigEndian.AppendUint16(b, h.clientPort)
}

func (r sessionReply) encode() []byte {
	b := []byte{r.status}
	b = binary.BigEndian.AppendUint64(b, r.session)
	b = append(b, r.token[:]...)
	resumed := byte(0)
	if r.resumed {
		resumed = 1
	}
	b = append(b, resumed)
	b = binary.BigEndian.AppendUint16(b, r.maxPayload)
	for _, ports := range [][]uint16{r.tcp, r.udp} {
		b = append(b, byte(len(ports)))
		for _, p := range ports {
			b = binary.BigEndian.AppendUint16(b, p)
		}
	}
	return b
}

func infoHello() []byte { return append([]byte(magic), version, kindInfo) }

// infoReply is the gateway's answer to an info hello: its version and facts (keys and values cleaned with
// version.Clean's rule, in key order; at most maxFacts).
func infoReply(v string, facts map[string]string) []byte {
	if len(v) > 255 {
		v = v[:255]
	}
	b := append([]byte{StatusOK, byte(len(v))}, v...)
	keys := make([]string, 0, len(facts))
	for k := range facts {
		if build.Clean(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > maxFacts {
		keys = keys[:maxFacts]
	}
	b = append(b, byte(len(keys)))
	for _, k := range keys {
		ck, cv := build.Clean(k), build.Clean(facts[k])
		b = append(append(b, byte(len(ck))), ck...)
		b = append(append(b, byte(len(cv))), cv...)
	}
	return b
}

// maxFacts bounds the facts of an info reply.
const maxFacts = 16

// readInfoReply reads an info reply's version and facts (cleaned: version.Clean). A reply that ends after the version
// (a gateway of a release before the facts) gives no facts and no error.
func readInfoReply(r io.Reader) (string, map[string]string, error) {
	var head [2]byte
	if _, err := io.ReadFull(r, head[:1]); err != nil {
		return "", nil, err
	}
	if head[0] != StatusOK {
		return "", nil, fmt.Errorf("the gateway answered the info hello with %s", StatusText(head[0]))
	}
	if _, err := io.ReadFull(r, head[1:]); err != nil {
		return "", nil, err
	}
	b := make([]byte, head[1])
	if _, err := io.ReadFull(r, b); err != nil {
		return "", nil, err
	}
	v := build.Clean(string(b))
	var n [1]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return v, nil, nil // no facts: a gateway of an earlier release
	}
	facts := map[string]string{}
	for i := 0; i < int(n[0]) && i < maxFacts; i++ {
		var kv [2]string
		for j := range kv {
			var l [1]byte
			if _, err := io.ReadFull(r, l[:]); err != nil {
				return v, facts, nil
			}
			s := make([]byte, l[0])
			if _, err := io.ReadFull(r, s); err != nil {
				return v, facts, nil
			}
			kv[j] = build.Clean(string(s))
		}
		if kv[0] != "" {
			facts[kv[0]] = kv[1]
		}
	}
	return v, facts, nil
}

var errMagic = errors.New("not a Dawngate tunnel hello")

// readHello reads a hello's head; it returns the kind, or StatusVersion as the error's status.
func readHello(r io.Reader) (kind byte, err error) {
	var head [6]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, err
	}
	if string(head[:4]) != magic {
		return 0, errMagic
	}
	if head[4] != version {
		return head[5], errVersion
	}
	return head[5], nil
}

var errVersion = errors.New("unsupported tunnel version")

func readSessionHello(r io.Reader) (sessionHello, error) {
	var b [4 + 8 + 16]byte
	var h sessionHello
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return h, err
	}
	copy(h.tunnelIP[:], b[:4])
	h.resumeID = binary.BigEndian.Uint64(b[4:])
	copy(h.resumeToken[:], b[12:])
	return h, nil
}

func readStreamHello(r io.Reader) (streamHello, error) {
	var b [8 + 16 + 2 + 4 + 2]byte
	var h streamHello
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return h, err
	}
	h.session = binary.BigEndian.Uint64(b[:])
	copy(h.token[:], b[8:24])
	h.port = binary.BigEndian.Uint16(b[24:])
	copy(h.clientIP[:], b[26:30])
	h.clientPort = binary.BigEndian.Uint16(b[30:])
	return h, nil
}

func readSessionReply(r io.Reader) (sessionReply, error) {
	var rep sessionReply
	var b [1 + 8 + 16 + 1 + 2]byte
	if _, err := io.ReadFull(r, b[:1]); err != nil {
		return rep, err
	}
	rep.status = b[0]
	if rep.status != StatusOK {
		return rep, nil
	}
	if _, err := io.ReadFull(r, b[1:]); err != nil {
		return rep, err
	}
	rep.session = binary.BigEndian.Uint64(b[1:])
	copy(rep.token[:], b[9:25])
	rep.resumed = b[25] == 1
	rep.maxPayload = binary.BigEndian.Uint16(b[26:])
	for _, ports := range []*[]uint16{&rep.tcp, &rep.udp} {
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return rep, err
		}
		raw := make([]byte, 2*int(n[0]))
		if _, err := io.ReadFull(r, raw); err != nil {
			return rep, err
		}
		for i := 0; i < len(raw); i += 2 {
			*ports = append(*ports, binary.BigEndian.Uint16(raw[i:]))
		}
	}
	return rep, nil
}

func ip4(ip net.IP) (b [4]byte) {
	copy(b[:], ip.To4())
	return b
}

func ipOf(b [4]byte) net.IP { return net.IPv4(b[0], b[1], b[2], b[3]).To4() }
