package web

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/limits"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/tunnel"
)

// QoS is the server side of DirtySDK's QosApi, the client's QoS check (roadmap item B2), on the
// ping site the preAuth reply announces (QOSS: BWPS and LTPS "local", host PSA, port PSP). QosApi's
// HTTP requests arrive over TCP; its UDP probes go to the qosport our replies name, which is the
// same port number over UDP. docs/BLAZE.md "QoS (B2)" has the whole exchange. Everything here is
// read from the client (Dawngate.exe 854, names given in Ghidra in B2):
//
//   - QosApiUpdate (0x00CE2EC0) runs each request: state 0 sends GET /qos/qos?vers=1&qtyp=1&prpt=…,
//     1 sends the latency probes; a request without flag bit 0 then goes on to 2 (qtyp=2), 3 (the
//     bandwidth probes), 4 (waiting), 5 (GET /qos/firewall?vers=1&nint=2) and 6/7 (GET
//     /qos/firetype?vers=1&rqid=…&rqsc=…&inip=…&inpt=…, sending firewall packets while it waits).
//   - QosApiParseHttpReply (0x00CE21F0) reads the XML replies with DirtySDK's XmlFindElement and
//     XmlContentGetInteger (decimal; children found by name, so their order doesn't matter).
//   - QosApiSendLatencyProbe (0x00CE2580), QosApiSendBandwidthProbe (0x00CE26F0),
//     QosApiSendFirewallProbes (0x00CE2810) write the probes; QosApiRecvPackets (0x00CE2DA0) and
//     QosApiProcessResponse (0x00CE29E0) read the answers.
//
// BlazeSDK starts one latency-only request (flags 1) per LTPS ping site, then one full request
// (flags 0) to the best site, whose bandwidth and NAT type become NetworkQosData (UBPS, DBPS, NATT).
type QoS struct {
	Log  *logging.Log
	Host string       // the IPv4 address we hand out (dgserver --public-host); the firewall reply announces it
	UDP  *net.UDPConn // the probe socket; its port is the qosport of every reply

	Hold          time.Duration // how long a latency probe waits before its echo goes out
	BandwidthWait time.Duration // after the first bandwidth probe, how long to wait for the rest
	FiretypeWait  time.Duration // how long a firetype request waits for the firewall packets
	Secret        func() uint32 // request secrets (tests fix them)
	// Limits (dgserver --limits; nil = off): every UDP datagram passes AllowPacket by its source address first.
	Limits *limits.Limiter

	mu    sync.Mutex
	next  uint32
	tests map[uint32]*qosTest
}

const (
	// LatencyHold delays every latency echo. The client times probes with timeGetTime (1 ms steps;
	// the game calls timeBeginPeriod(1)), so a loopback round trip measures 0 ms, and BlazeSDK keeps a
	// ping site at MAX_QOS_LATENCY when the median latency is 0 (StoreQosPingSiteLatency 0x00CCC260).
	// Holding each echo for 1 ms makes the measured latency at least 1 ms.
	LatencyHold = time.Millisecond
	// BandwidthProbes and ProbeSize go into the qtyp=2 reply: the client sends that many probes of
	// that size, and we send as many back. The client needs numprobes >= 2 and probesize > 0, and its
	// probe buffers hold 0x4F0 bytes; 1,200 bytes also stays below a 1,500-byte MTU.
	BandwidthProbes = 10
	ProbeSize       = 1200
	// LatencyRequestID is the requestid of every qtyp=1 reply. The client treats an answer whose
	// requestid is above 1 as a bandwidth answer (QosApiProcessResponse), and latency probes carry the
	// requestid of the last reply, so it has to be 1 (0 fails the reply check).
	LatencyRequestID = 1
)

// Firetype values as the client reads them; BlazeSDK maps them to Blaze::Util::NatType
// (HandleQosNatType 0x00CCA490): 1 OPEN, 2 MODERATE, 3 STRICT_SEQUENTIAL, 4 STRICT, 5 pending (no
// change), anything else UNKNOWN.
const (
	FiretypeUnknown  = 0
	FiretypeOpen     = 1
	FiretypeModerate = 2
)

// qosTest is a bandwidth or firewall test handed out by an HTTP reply, keyed by its requestid.
type qosTest struct {
	firewall bool
	peer     string // the HTTP client that asked (peerKey); UDP from other addresses is ignored
	secret   uint32
	created  time.Time

	// bandwidth
	probes, size int
	got          int
	first, last  time.Time
	client       *udpPeer
	clientReq    uint32 // the client's request id (word 0 of its probes)
	timer        *time.Timer
	done         bool

	// firewall
	from    *udpPeer      // source of the first firewall packet
	arrived chan struct{} // closed when it arrives
}

// udpPeer is where a datagram came from and how to answer it: a socket address (the QoS port's own socket), or
// a game socket behind the tunnel (tunnel.Addr, answered through the gateway).
type udpPeer struct {
	addr net.Addr // for the log
	ip   net.IP
	port int
	key  string // equal to the HTTP peer's key of the same client (peerKey)
	send func([]byte) error
}

// peerKey names a client for the tests' address check: its IP, or for a tunnelled client its tunnel session,
// since every tunnelled player's game has the address 127.0.0.1 (and one session is one player's PC).
func peerKey(a net.Addr) string {
	if t, ok := a.(*tunnel.Addr); ok {
		return fmt.Sprintf("tunnel %d", t.Session)
	}
	return hostOf(a)
}

// announceHost is the address a reply names for the server: the tunnel address for a tunnelled client, else Host.
func (q *QoS) announceHost(c net.Conn) string {
	if a := tunnel.Local(c); a != nil {
		return a.IP.String()
	}
	return q.Host
}

// NewQoS returns a QoS server with the default timings.
func NewQoS(log *logging.Log, host string, udp *net.UDPConn) *QoS {
	return &QoS{Log: log, Host: host, UDP: udp, Hold: LatencyHold,
		BandwidthWait: 500 * time.Millisecond, FiretypeWait: time.Second}
}

const qosWho = "qos"

// Serve answers QosApi's HTTP requests until the listener is closed.
func (q *QoS) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go q.ServeConn(c)
	}
}

// ServeConn reads one HTTP request (30 s limit), logs it, answers and closes the connection.
func (q *QoS) ServeConn(c net.Conn) {
	defer c.Close()
	peer := logging.Peer(c.RemoteAddr())
	c.SetReadDeadline(time.Now().Add(30 * time.Second))
	req, err := readRequest(bufio.NewReader(c))
	if err != nil {
		q.Log.Logf(qosWho, "%s %s", peer, describeErr(err))
		return
	}
	if req == nil {
		return // closed without a request
	}
	q.Log.Log(qosWho, peer+" "+req.line)
	status, body := q.AnswerFor(req.line, peerKey(c.RemoteAddr()), q.announceHost(c))
	ctype := "text/xml"
	if body == "" {
		ctype = "text/plain"
	}
	out := fmt.Sprintf("HTTP/1.1 %s\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", status, ctype, len(body), body)
	if _, err := c.Write([]byte(out)); err != nil {
		q.Log.Logf(qosWho, "%s %s: %v", peer, logging.ErrName(err), err)
		return
	}
	note := ""
	if body != "" {
		note = ", " + body
	}
	q.Log.Log(qosWho, "-> "+status+note)
}

func hostOf(a net.Addr) string {
	switch t := a.(type) {
	case *net.TCPAddr:
		return t.IP.String()
	case *net.UDPAddr:
		return t.IP.String()
	}
	h, _, _ := net.SplitHostPort(a.String())
	return h
}

// Answer returns the status and XML body for a request line from peer (an IP address). Bodies are
// one line without an XML declaration; QosApiParseHttpReply needs neither.
func (q *QoS) Answer(line, peer string) (status, body string) { return q.AnswerFor(line, peer, q.Host) }

// AnswerFor is Answer for a client that is told host as the server's address (a tunnelled client: its tunnel
// address); peer is the client's peerKey.
func (q *QoS) AnswerFor(line, peer, host string) (status, body string) {
	parts := strings.Fields(line)
	if len(parts) < 2 || parts[0] != "GET" {
		return "404 Not Found", ""
	}
	u, err := url.Parse(parts[1])
	if err != nil {
		return "404 Not Found", ""
	}
	args := u.Query()
	switch u.Path {
	case "/qos/qos":
		switch args.Get("qtyp") {
		case "1":
			return "200 OK", q.LatencyReply()
		case "2":
			return "200 OK", q.BandwidthReply(peer)
		}
	case "/qos/firewall":
		return "200 OK", q.firewallReply(peer, host)
	case "/qos/firetype":
		return "200 OK", q.FiretypeReply(args)
	}
	return "404 Not Found", ""
}

// qosXML is QosApi's reply to /qos/qos: QosApiParseHttpReply reads numprobes (http state +0x109C),
// probesize (+0x1094), qosport (request +0x11C, the UDP port the probes go to), requestid (+0x10A8)
// and reqsecret (+0x10AC). It fails the request when qosport or requestid is 0, or, for qtyp=2, when
// probesize is 0 or numprobes is below 2.
func qosXML(numprobes, probesize, port int, id, secret uint32) string {
	return fmt.Sprintf("<qos><numprobes>%d</numprobes><probesize>%d</probesize><qosport>%d</qosport>"+
		"<requestid>%d</requestid><reqsecret>%d</reqsecret></qos>", numprobes, probesize, port, id, secret)
}

func (q *QoS) port() int { return q.UDP.LocalAddr().(*net.UDPAddr).Port }

// LatencyReply answers qtyp=1. The client then sends LNP probes (preAuth's QOSS.LNP, 10) to our UDP
// port and takes the median round trip; numprobes and probesize only matter for qtyp=2.
func (q *QoS) LatencyReply() string {
	return qosXML(0, 0, q.port(), LatencyRequestID, 0)
}

// BandwidthReply answers qtyp=2 with a new bandwidth test.
func (q *QoS) BandwidthReply(peer string) string {
	t := &qosTest{peer: peer, probes: BandwidthProbes, size: ProbeSize}
	id := q.add(t)
	return qosXML(t.probes, t.size, q.port(), id, t.secret)
}

// FirewallReply answers /qos/firewall with a new firewall test. QosApiParseHttpReply reads
// numinterfaces (+0x10B0; at most 2 fit), the first .ips.ips and .ports.ports and their next
// siblings (+0x10B4/+0x10BC: the addresses the firewall packets go to, IPv4 as a decimal integer),
// requestid (+0x10C4, must not be 0) and reqsecret (+0x10C8). The client asks for nint=2; we have one
// address, so we name one.
func (q *QoS) FirewallReply(peer string) string { return q.firewallReply(peer, q.Host) }

// firewallReply names host as the address the firewall packets go to: Host, or a tunnelled client's tunnel address,
// where the client side of the tunnel takes them.
func (q *QoS) firewallReply(peer, host string) string {
	t := &qosTest{firewall: true, peer: peer, arrived: make(chan struct{})}
	id := q.add(t)
	ip := net.ParseIP(host).To4()
	return fmt.Sprintf("<firewall><numinterfaces>1</numinterfaces><ips><ips>%d</ips></ips><ports><ports>%d</ports></ports>"+
		"<requestid>%d</requestid><reqsecret>%d</reqsecret></firewall>", binary.BigEndian.Uint32(ip), q.port(), id, t.secret)
}

// FiretypeReply answers /qos/firetype?rqid=&rqsc=&inip=&inpt=. The client sends this request and
// then, while the reply is outstanding, its firewall packets (requestid, reqsecret) to the addresses
// of the firewall reply, from its QoS socket; inip/inpt are its own interface address and QoS port.
// We wait for the first packet: if it comes from inpt on inip (or over loopback, which no NAT sits
// on), the client is OPEN; if the address or port changed on the way, a NAT mapped it, which with
// one address we can't tell from STRICT, so we answer MODERATE; no packet is UNKNOWN. The client
// stores the value (module +0x178) and hands it to BlazeSDK unless it is 5.
func (q *QoS) FiretypeReply(args url.Values) string {
	reply := func(v int) string { return fmt.Sprintf("<firetype><firetype>%d</firetype></firetype>", v) }
	// ProtoHttpUrlEncodeIntParm (0x00CDB390) prints the values with %d, so they may be negative.
	rqid, _ := strconv.ParseInt(args.Get("rqid"), 10, 64)
	secret, _ := strconv.ParseInt(args.Get("rqsc"), 10, 64)
	id := uint32(rqid)
	q.mu.Lock()
	t := q.tests[id]
	if t == nil || !t.firewall || t.secret != uint32(secret) {
		q.mu.Unlock()
		q.Log.Logf(qosWho, "firetype: no firewall test %d with that secret", id)
		return reply(FiretypeUnknown)
	}
	q.mu.Unlock()
	select {
	case <-t.arrived:
	case <-time.After(q.FiretypeWait):
	}
	q.mu.Lock()
	from := t.from
	delete(q.tests, id)
	q.mu.Unlock()
	if from == nil {
		q.Log.Logf(qosWho, "firetype: no firewall packet for requestid %d within %v", id, q.FiretypeWait)
		return reply(FiretypeUnknown)
	}
	inip, _ := strconv.ParseInt(args.Get("inip"), 10, 64) // NetConnStatus('addr')
	inpt, _ := strconv.Atoi(args.Get("inpt"))
	v := uint32(inip)
	in := net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	// a tunnelled client shows its own loopback address and port: OPEN, as a local client
	if from.port == inpt && (from.ip.Equal(in) || from.ip.IsLoopback()) {
		return reply(FiretypeOpen)
	}
	return reply(FiretypeModerate)
}

// add registers a test under a new requestid (2 and up; 0 fails the client's check and 1 marks
// latency answers) and drops tests older than a minute.
func (q *QoS) add(t *qosTest) uint32 {
	t.created = time.Now()
	if q.Secret != nil {
		t.secret = q.Secret()
	} else {
		t.secret = rand.Uint32()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.tests == nil {
		q.tests = map[uint32]*qosTest{}
	}
	for id, old := range q.tests {
		if time.Since(old.created) > time.Minute {
			delete(q.tests, id)
		}
	}
	for {
		q.next++
		if q.next < 2 {
			q.next = 2
		}
		if q.tests[q.next] == nil {
			q.tests[q.next] = t
			return q.next
		}
	}
}

// ServeUDP answers the probes until the socket is closed.
func (q *QoS) ServeUDP() error {
	buf := make([]byte, 2048)
	for {
		n, from, err := q.UDP.ReadFromUDP(buf)
		if err != nil {
			if isClosed(err) {
				return err
			}
			// Windows reports an ICMP port-unreachable for an earlier send as an error here
			// (WSAECONNRESET), e.g. after the game exited; the socket stays usable.
			q.Log.Logf(qosWho, "udp read: %v", err)
			continue
		}
		if !q.Limits.AllowPacket(from.IP.String(), n) { // over the address's datagram or byte rate
			if q.Limits.ShouldLog("udp qos", from.IP.String()) {
				q.Log.Logf(qosWho, "limits: udp from %s over its rate; dropped (further drops counted in the minute's limits line)",
					from.IP)
			}
			continue
		}
		q.Packet(append([]byte(nil), buf[:n]...), from)
	}
}

func isClosed(err error) bool { return errors.Is(err, net.ErrClosed) }

// Packet handles one UDP datagram from the QoS socket: 20 bytes is a latency probe, 8 a firewall packet,
// anything longer a bandwidth probe.
func (q *QoS) Packet(p []byte, from *net.UDPAddr) {
	q.packet(p, &udpPeer{addr: from, ip: from.IP, port: from.Port, key: from.IP.String(),
		send: func(b []byte) error { _, err := q.UDP.WriteToUDP(b, from); return err }})
}

// TunnelPacket handles a datagram that came through the tunnel (the gateway's tunnel.PacketHandler for the
// QoS port): from is the game socket's address on the player's PC, reply answers it through the tunnel.
func (q *QoS) TunnelPacket(p []byte, from *tunnel.Addr, reply func([]byte) error) {
	q.packet(p, &udpPeer{addr: from, ip: from.IP, port: from.Port, key: peerKey(from), send: reply})
}

func (q *QoS) packet(p []byte, from *udpPeer) {
	peer := logging.Peer(from.addr)
	switch {
	case len(p) == 20:
		q.latency(p, from, peer)
	case len(p) == 8:
		q.firewallPacket(p, from, peer)
	case len(p) > 20:
		q.bandwidth(p, from, peer)
	default:
		q.Log.Logf(qosWho, "udp %s %d bytes ignored:\n%s", peer, len(p), logging.Hexdump(p))
	}
}

// latency echoes a latency probe. The probe (QosApiSendLatencyProbe) is five big-endian words: the
// client's request id, requestid and reqsecret of our qtyp=1 reply (0 for a request without HTTP),
// the request's flags and the send tick. The answer keeps them and adds what QosApiProcessResponse
// reads after them, in the layout of the client's own probe responder (QosApiRespondToProbe
// 0x00CE2910):
//
//	+0x14 uint32 the client's address as we see it   (BlazeSDK's external address, 'extn')
//	+0x18 uint16 the client's port as we see it
//	+0x1A uint32 extra data length (0 here; 1..256 bytes would follow at +0x1E)
//
// The client takes the round trip from the echoed tick, ignores answers to request id 0, and reads a
// 20-byte datagram as a probe to answer, so the answer must not be 20 bytes long.
func (q *QoS) latency(p []byte, from *udpPeer, peer string) {
	req := binary.BigEndian.Uint32(p)
	if req == 0 {
		q.Log.Logf(qosWho, "udp %s latency probe for request 0 ignored", peer)
		return
	}
	out := make([]byte, 30)
	copy(out, p)
	if ip := from.ip.To4(); ip != nil {
		copy(out[20:24], ip)
	}
	binary.BigEndian.PutUint16(out[24:26], uint16(from.port))
	q.Log.Logf(qosWho, "udp %s latency probe: request %d, requestid %d, flags %d -> echo",
		peer, req, binary.BigEndian.Uint32(p[4:]), binary.BigEndian.Uint32(p[12:]))
	time.AfterFunc(q.Hold, func() { from.send(out) })
}

// bandwidth counts a bandwidth probe (QosApiSendBandwidthProbe: probesize bytes, words client request
// id, requestid, reqsecret, probe index from 0, numprobes). When all have arrived, or BandwidthWait
// after the first, the test answers with numprobes datagrams of probesize bytes back to back
// (sendBandwidth).
func (q *QoS) bandwidth(p []byte, from *udpPeer, peer string) {
	id, secret, index := binary.BigEndian.Uint32(p[4:]), binary.BigEndian.Uint32(p[8:]), binary.BigEndian.Uint32(p[12:])
	q.mu.Lock()
	t := q.tests[id]
	if t == nil || t.firewall || t.secret != secret || t.peer != from.key || t.done {
		q.mu.Unlock()
		q.Log.Logf(qosWho, "udp %s %d bytes for requestid %d ignored (no open bandwidth test)", peer, len(p), id)
		return
	}
	now := time.Now()
	t.got++
	t.last = now
	if t.got == 1 {
		t.first, t.client, t.clientReq = now, from, binary.BigEndian.Uint32(p)
		t.timer = time.AfterFunc(q.BandwidthWait, func() { q.sendBandwidth(id) })
	}
	last := int(index) == t.probes-1 || t.got >= t.probes
	q.mu.Unlock()
	if last {
		q.sendBandwidth(id)
	}
}

// sendBandwidth ends a bandwidth test. QosApiProcessResponse reads an answer whose requestid (word 1)
// is above 1 as a bandwidth answer: requestid and reqsecret must match, the first answer's word at
// +0x14 is the upstream rate (bit/s, NetworkQosData.UBPS), the client computes the downstream rate
// from the answers' arrival (count * probesize * 8000 / ms since the first; DBPS), and the answer
// whose word 3 equals numprobes ends the test. Our answers carry the sequence 1..numprobes in word 3.
// The upstream rate uses the client's formula on our arrival times.
func (q *QoS) sendBandwidth(id uint32) {
	q.mu.Lock()
	t := q.tests[id]
	if t == nil || t.done {
		q.mu.Unlock()
		return
	}
	t.done = true
	t.timer.Stop()
	delete(q.tests, id)
	got, ms := t.got, t.last.Sub(t.first).Milliseconds()
	q.mu.Unlock()
	up := Rate(got, t.size, ms)
	q.Log.Logf(qosWho, "udp %s bandwidth test %d: %d of %d probes of %d bytes in %d ms, upstream %d bit/s; sending %d back",
		logging.Peer(t.client.addr), id, got, t.probes, t.size, ms, up, t.probes)
	for seq := 1; seq <= t.probes; seq++ {
		out := make([]byte, t.size)
		binary.BigEndian.PutUint32(out[0:], t.clientReq)
		binary.BigEndian.PutUint32(out[4:], id)
		binary.BigEndian.PutUint32(out[8:], t.secret)
		binary.BigEndian.PutUint32(out[12:], uint32(seq))
		binary.BigEndian.PutUint32(out[16:], uint32(t.probes))
		binary.BigEndian.PutUint32(out[20:], up)
		t.client.send(out)
	}
}

// Rate is the client's bandwidth formula (QosApiProcessResponse): count * size * 8000 bits over the
// milliseconds between the first and the last datagram, undivided when they arrive in the same
// millisecond (loopback), capped at the uint32 the client keeps.
func Rate(count, size int, ms int64) uint32 {
	bits := uint64(count) * uint64(size) * 8000
	if ms > 0 {
		bits /= uint64(ms)
	}
	return uint32(min(bits, math.MaxUint32))
}

// firewallPacket records the first firewall packet of a test (QosApiSendFirewallProbes: requestid and
// reqsecret of our firewall reply, big-endian, from the client's QoS socket; five rounds while the
// firetype request is outstanding).
func (q *QoS) firewallPacket(p []byte, from *udpPeer, peer string) {
	id, secret := binary.BigEndian.Uint32(p), binary.BigEndian.Uint32(p[4:])
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.tests[id]
	if t == nil || !t.firewall || t.secret != secret || t.peer != from.key {
		q.Log.Logf(qosWho, "udp %s firewall packet for requestid %d ignored (no open firewall test)", peer, id)
		return
	}
	if t.from == nil {
		t.from = from
		close(t.arrived)
		q.Log.Logf(qosWho, "udp %s firewall packet for requestid %d", peer, id)
	}
}
