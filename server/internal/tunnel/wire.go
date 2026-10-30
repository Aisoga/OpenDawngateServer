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
//	client -> gateway: "DGTN", version uint8 (3 for a session, 1 for the others), kind uint8, then by kind:
//	  1 session: tunnel address [4] (the loopback address the player copy is patched for), resume id uint64,
//	             resume token [16] (zeros for a new session; a session that lost its control connection is taken
//	             up again within Grace, with new UDP keys), then (version 2) the release length uint8 + the
//	             Start-Client's release name (version.String, at most version.MaxLen bytes), then (version 3) the
//	             player copy's installed map archives: a count uint8 (at most MaxHelloMaps) and per archive its
//	             name's length uint8 + its file name in game\Data ("zz_CM_Ring.snap") + its SHA-256 [32] (the map
//	             check compares it with the server's own, blaze.Server.MapVersions). The gateway refuses a version 1
//	             session hello (a Start-Client from before the release check) with StatusVersion and one naming
//	             another release than its own with StatusRelease, unless Gateway.AllowAnyClient; it takes versions 2
//	             and 3 (a version 2 hello names no maps). A gateway of a release before version 3 answers it
//	             StatusVersion, and the client says hello again with version 2 (Client.connect)
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
//	           and its bytes (e.g. "signup": open, invite, closed or local; "maps"; "version.<tag>", FactMapVersion),
//	           in key order; then the gateway closes the connection.
//	           A reply that ends after the version (a release before the facts) has none.
//
// After a session's reply both sides derive the UDP keys (DeriveKeys) and the control connection carries only
// heartbeats (one byte 0 every HeartbeatEvery, both ways); it ends the session's life, after Grace.
const (
	magic   = "DGTN"
	version = 1 // the stream and info hellos'
	// releaseVersion is the session hello that adds the Start-Client's release name after version 1's 28 bytes (a
	// gateway of an earlier release answers it StatusVersion).
	releaseVersion = 2
	// sessionVersion is the session hello this build writes: 3 adds the installed maps after the release name.
	sessionVersion = 3

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
	StatusRelease     byte = 6 // the session hello names another release than the server's (or an empty name)
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
	case StatusRelease:
		return "another release than the server's"
	}
	return fmt.Sprintf("status %d", s)
}

type sessionHello struct {
	// ver is the hello's version (0 = sessionVersion when written)
	ver         byte
	tunnelIP    [4]byte
	resumeID    uint64
	resumeToken [16]byte
	// release is the Start-Client's release name (version 2; "" in a version 1 hello)
	release string
	// maps are the player copy's installed map archives (version 3; nil in an earlier hello)
	maps []InstalledMap
}

// version is the hello's version as written.
func (h sessionHello) version() byte {
	if h.ver == 0 {
		return sessionVersion
	}
	return h.ver
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

// encode writes a session hello of h's version (version 2: the release name cut to version.MaxLen bytes; version 3
// also the maps, at most MaxHelloMaps, each name cut to 255 bytes).
func (h sessionHello) encode() []byte {
	ver := h.version()
	b := append([]byte(magic), ver, kindSession)
	b = append(b, h.tunnelIP[:]...)
	b = binary.BigEndian.AppendUint64(b, h.resumeID)
	b = append(b, h.resumeToken[:]...)
	if ver < releaseVersion {
		return b
	}
	r := h.release
	if len(r) > build.MaxLen {
		r = r[:build.MaxLen]
	}
	b = append(append(b, byte(len(r))), r...)
	if ver < sessionVersion {
		return b
	}
	maps := h.maps
	if len(maps) > MaxHelloMaps {
		maps = maps[:MaxHelloMaps]
	}
	b = append(b, byte(len(maps)))
	for _, m := range maps {
		name := m.Archive
		if len(name) > 255 {
			name = name[:255]
		}
		b = append(append(b, byte(len(name))), name...)
		b = append(b, m.SHA256[:]...)
	}
	return b
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

// maxFacts bounds the facts of an info reply: the server's own ones and a version per offered map (FactMapVersion,
// at most MaxHelloMaps). It was 16 before MAPED-GUARD-dialog: a reader of that release reads the first 16 in key
// order, which FactMaps and FactSignup lead.
const maxFacts = 16 + MaxHelloMaps

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

// readHello reads a hello's head; it returns its version (version or sessionVersion) and kind, or errVersion for any
// other version (answered StatusVersion). Which version a kind needs is the gateway's check.
func readHello(r io.Reader) (ver, kind byte, err error) {
	var head [6]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, 0, err
	}
	if string(head[:4]) != magic {
		return 0, 0, errMagic
	}
	if head[4] < version || head[4] > sessionVersion {
		return head[4], head[5], errVersion
	}
	return head[4], head[5], nil
}

var errVersion = errors.New("unsupported tunnel version")

// readSessionHello reads a session hello after its head: version 1's 28 bytes, from releaseVersion on the release
// name too (cleaned: version.Clean), and for sessionVersion the maps (names cleaned: CleanArchive; at most
// MaxHelloMaps).
func readSessionHello(r io.Reader, ver byte) (sessionHello, error) {
	var b [4 + 8 + 16 + 1]byte
	h := sessionHello{ver: ver}
	n := len(b) - 1
	if ver >= releaseVersion {
		n = len(b)
	}
	if _, err := io.ReadFull(r, b[:n]); err != nil {
		return h, err
	}
	copy(h.tunnelIP[:], b[:4])
	h.resumeID = binary.BigEndian.Uint64(b[4:])
	copy(h.resumeToken[:], b[12:28])
	if ver < releaseVersion {
		return h, nil
	}
	name := make([]byte, b[28])
	if _, err := io.ReadFull(r, name); err != nil {
		return h, err
	}
	h.release = build.Clean(string(name))
	if ver < sessionVersion {
		return h, nil
	}
	var count [1]byte
	if _, err := io.ReadFull(r, count[:]); err != nil {
		return h, err
	}
	if int(count[0]) > MaxHelloMaps {
		return h, fmt.Errorf("the session hello names %d maps (at most %d)", count[0], MaxHelloMaps)
	}
	h.maps = []InstalledMap{}
	for i := 0; i < int(count[0]); i++ {
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return h, err
		}
		name := make([]byte, int(l[0])+32)
		if _, err := io.ReadFull(r, name); err != nil {
			return h, err
		}
		m := InstalledMap{Archive: CleanArchive(string(name[:l[0]]))}
		copy(m.SHA256[:], name[l[0]:])
		h.maps = append(h.maps, m)
	}
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
