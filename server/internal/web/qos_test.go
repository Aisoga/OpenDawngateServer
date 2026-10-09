package web

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// lockedBuffer collects log text written from several goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// qosRig is a QoS server on loopback: HTTP on an ephemeral TCP port, probes on an ephemeral UDP port.
type qosRig struct {
	q    *QoS
	log  *lockedBuffer
	http string // TCP address
	udp  int    // UDP port (the qosport of the replies)
}

const testSecret = 0x12345678

func newQoSRig(t *testing.T) *qosRig {
	t.Helper()
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &qosRig{log: &lockedBuffer{}, http: ln.Addr().String(), udp: udp.LocalAddr().(*net.UDPAddr).Port}
	r.q = NewQoS(logging.New(r.log), "127.0.0.1", udp)
	r.q.Secret = func() uint32 { return testSecret }
	go r.q.Serve(ln)
	go r.q.ServeUDP()
	t.Cleanup(func() { ln.Close(); udp.Close() })
	return r
}

// get sends a request the way the client's ProtoHttp does (headers from session b5_run9) and returns
// the whole response.
func (r *qosRig) get(t *testing.T, target string) string {
	t.Helper()
	c, err := net.Dial("tcp4", r.http)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: 127.0.0.1:17502\r\nContent-Length: 0\r\n"+
		"User-Agent: ProtoHttp 1.3/DS 8.18.11.0 (Windows)\r\nAccept: */*\r\n\r\n", target)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, _ := io.ReadAll(c)
	return string(resp)
}

func xmlResponse(body string) string {
	return fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/xml\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
}

// udpClient is the client's QoS socket.
func (r *qosRig) udpClient(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: r.udp})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func words(ws ...uint32) []byte {
	b := make([]byte, 4*len(ws))
	for i, w := range ws {
		binary.BigEndian.PutUint32(b[4*i:], w)
	}
	return b
}

func readUDP(t *testing.T, c *net.UDPConn) []byte {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 0x4F0) // the client's receive buffer
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf[:n]
}

// clientField reads an element of an XML reply the way the client's XmlFind and
// XmlContentGetInteger do: by name (a dotted path for nested elements, as in ".ips.ips"), decimal,
// wrapping to 32 bits; ok is false when it is missing.
func clientField(body, path string) (v uint32, ok bool) {
	s := body
	for _, name := range strings.Split(path, ".") {
		i := strings.Index(s, "<"+name+">")
		if i < 0 {
			return 0, false
		}
		s = s[i+len(name)+2:]
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		v = v*10 + uint32(s[0]-'0')
		s = s[1:]
	}
	if neg {
		v = -v
	}
	return v, true
}

// clientAccepts applies QosApiParseHttpReply's checks (0x00CE21F0) to a /qos/qos reply for the given
// qtyp: qosport and requestid not 0; for qtyp 2 probesize not 0 and numprobes at least 2. On top, the
// requestid has to put the answers on the right path of QosApiProcessResponse: at most 1 for latency
// probes, above 1 for bandwidth probes.
func clientAccepts(body string, qtyp int) error {
	port, _ := clientField(body, "qosport")
	id, _ := clientField(body, "requestid")
	probes, _ := clientField(body, "numprobes")
	size, _ := clientField(body, "probesize")
	switch {
	case !strings.HasPrefix(body, "<qos>"):
		return fmt.Errorf("no <qos> element")
	case port == 0 || id == 0:
		return fmt.Errorf("qosport %d, requestid %d", port, id)
	case qtyp == 2 && (size == 0 || probes < 2):
		return fmt.Errorf("probesize %d, numprobes %d", size, probes)
	case qtyp == 2 && size > 0x4F0:
		return fmt.Errorf("probesize %d exceeds the client's 0x4F0-byte buffers", size)
	case qtyp == 1 && id > 1, qtyp == 2 && id <= 1:
		return fmt.Errorf("requestid %d for qtyp %d", id, qtyp)
	}
	return nil
}

func TestQoSLatencyReply(t *testing.T) {
	r := newQoSRig(t)
	resp := r.get(t, "/qos/qos?vers=1&qtyp=1&prpt=3659")
	body := fmt.Sprintf("<qos><numprobes>0</numprobes><probesize>0</probesize><qosport>%d</qosport>"+
		"<requestid>1</requestid><reqsecret>0</reqsecret></qos>", r.udp)
	if resp != xmlResponse(body) {
		t.Errorf("response:\n%q", resp)
	}
	if err := clientAccepts(body, 1); err != nil {
		t.Error(err)
	}
	log := r.log.String()
	for _, want := range []string{
		") GET /qos/qos?vers=1&qtyp=1&prpt=3659 HTTP/1.1\n",
		"[qos] -> 200 OK, " + body + "\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// A latency probe (QosApiSendLatencyProbe 0x00CE2580: request id, requestid, reqsecret, flags, tick)
// comes back after the hold with the client's address, port and an empty extra-data field.
func TestQoSLatencyEcho(t *testing.T) {
	r := newQoSRig(t)
	r.q.Hold = 20 * time.Millisecond
	c := r.udpClient(t)
	probe := words(2, 1, 0, 1, 0x00ABCDEF)
	start := time.Now()
	c.Write(probe)
	got := readUDP(t, c)
	held := time.Since(start)
	local := c.LocalAddr().(*net.UDPAddr)
	want := append(append([]byte{}, probe...), 127, 0, 0, 1, byte(local.Port>>8), byte(local.Port), 0, 0, 0, 0)
	if !bytes.Equal(got, want) {
		t.Errorf("echo\n% x\nwant\n% x", got, want)
	}
	if held < r.q.Hold {
		t.Errorf("echo after %v, hold is %v", held, r.q.Hold)
	}
	if len(got) == 20 {
		t.Error("a 20-byte answer would be read as a probe")
	}
	if want := fmt.Sprintf("[qos] udp ('127.0.0.1', %d) latency probe: request 2, requestid 1, flags 1 -> echo\n", local.Port); !strings.Contains(r.log.String(), want) {
		t.Errorf("log lacks %q:\n%s", want, r.log.String())
	}

	// request id 0 gets no answer (the client ignores such answers anyway)
	c.Write(words(0, 0, 0, 1, 5))
	c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if n, err := c.Read(make([]byte, 64)); err == nil {
		t.Errorf("answer to request id 0: %d bytes", n)
	}
}

func TestQoSBandwidth(t *testing.T) {
	r := newQoSRig(t)
	resp := r.get(t, "/qos/qos?vers=1&qtyp=2&prpt=3659")
	body := fmt.Sprintf("<qos><numprobes>10</numprobes><probesize>1200</probesize><qosport>%d</qosport>"+
		"<requestid>2</requestid><reqsecret>305419896</reqsecret></qos>", r.udp)
	if resp != xmlResponse(body) {
		t.Fatalf("response:\n%q", resp)
	}
	if err := clientAccepts(body, 2); err != nil {
		t.Error(err)
	}

	// QosApiSendBandwidthProbe (0x00CE26F0): probesize bytes; request id, requestid, reqsecret, index, numprobes
	c := r.udpClient(t)
	for i := uint32(0); i < 10; i++ {
		p := make([]byte, 1200)
		copy(p, words(7, 2, testSecret, i, 10))
		c.Write(p)
	}
	for seq := uint32(1); seq <= 10; seq++ {
		got := readUDP(t, c)
		if len(got) != 1200 {
			t.Fatalf("answer %d: %d bytes", seq, len(got))
		}
		up := binary.BigEndian.Uint32(got[20:])
		if !bytes.Equal(got[:24], words(7, 2, testSecret, seq, 10, up)) || up == 0 {
			t.Errorf("answer %d: % x", seq, got[:24])
		}
		if !bytes.Equal(got[24:], make([]byte, 1200-24)) {
			t.Errorf("answer %d: padding not zero", seq)
		}
	}
	if !strings.Contains(r.log.String(), " bandwidth test 2: 10 of 10 probes of 1200 bytes in ") {
		t.Errorf("log:\n%s", r.log.String())
	}

	// the test is over; a late probe is ignored
	p := make([]byte, 1200)
	copy(p, words(7, 2, testSecret, 3, 10))
	c.Write(p)
	c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if n, err := c.Read(make([]byte, 2048)); err == nil {
		t.Errorf("answer to a late probe: %d bytes", n)
	}
}

// With probes lost, the test answers BandwidthWait after the first one.
func TestQoSBandwidthTimeout(t *testing.T) {
	r := newQoSRig(t)
	r.q.BandwidthWait = 50 * time.Millisecond
	r.get(t, "/qos/qos?vers=1&qtyp=2&prpt=3659")
	c := r.udpClient(t)
	p := make([]byte, 1200)
	copy(p, words(7, 2, testSecret, 0, 10))
	c.Write(p)
	for seq := uint32(1); seq <= 10; seq++ {
		got := readUDP(t, c)
		if binary.BigEndian.Uint32(got[12:]) != seq {
			t.Fatalf("answer %d: % x", seq, got[:24])
		}
	}
	if !strings.Contains(r.log.String(), " bandwidth test 2: 1 of 10 probes of 1200 bytes in 0 ms, upstream 9600000 bit/s; sending 10 back\n") {
		t.Errorf("log:\n%s", r.log.String())
	}
}

func TestQoSBandwidthWrongSecret(t *testing.T) {
	r := newQoSRig(t)
	r.get(t, "/qos/qos?vers=1&qtyp=2&prpt=3659")
	c := r.udpClient(t)
	p := make([]byte, 1200)
	copy(p, words(7, 2, testSecret+1, 9, 10))
	c.Write(p)
	c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if n, err := c.Read(make([]byte, 2048)); err == nil {
		t.Errorf("answer to a probe with the wrong secret: %d bytes", n)
	}
	if !strings.Contains(r.log.String(), " 1200 bytes for requestid 2 ignored (no open bandwidth test)\n") {
		t.Errorf("log:\n%s", r.log.String())
	}
}

// The firewall reply names our UDP address; the client sends its firewall packets there while its
// firetype request waits, and the firetype reply says OPEN when they come from inip:inpt.
func TestQoSFirewall(t *testing.T) {
	r := newQoSRig(t)
	body := fmt.Sprintf("<firewall><numinterfaces>1</numinterfaces><ips><ips>2130706433</ips></ips>"+
		"<ports><ports>%d</ports></ports><requestid>2</requestid><reqsecret>305419896</reqsecret></firewall>", r.udp)
	if resp := r.get(t, "/qos/firewall?vers=1&nint=2"); resp != xmlResponse(body) {
		t.Fatalf("response:\n%q", resp)
	}
	if ip, _ := clientField(body, "ips.ips"); ip != 0x7F000001 {
		t.Errorf("ips %#x", ip)
	}
	if port, _ := clientField(body, "ports.ports"); int(port) != r.udp {
		t.Errorf("ports %d", port)
	}
	if id, _ := clientField(body, "requestid"); id == 0 {
		t.Error("requestid 0 fails the client's check")
	}

	c := r.udpClient(t)
	inpt := c.LocalAddr().(*net.UDPAddr).Port
	done := make(chan string)
	go func() {
		// rqsc as the client prints it (%d); inip a LAN address, which prints negative
		done <- r.get(t, fmt.Sprintf("/qos/firetype?vers=1&rqid=2&rqsc=305419896&inip=-1062731519&inpt=%d", inpt))
	}()
	time.Sleep(50 * time.Millisecond) // the client sends the packets while the request is outstanding
	c.Write(words(2, testSecret))
	c.Write(words(2, testSecret))
	if resp := <-done; resp != xmlResponse("<firetype><firetype>1</firetype></firetype>") {
		t.Errorf("firetype response:\n%q", resp)
	}
	if strings.Count(r.log.String(), "firewall packet for requestid 2\n") != 1 {
		t.Errorf("log:\n%s", r.log.String())
	}
}

// A5: with dgserver --public-host the firewall reply names that address (where the client sends its
// firewall packets), not the one the server binds.
func TestQoSFirewallPublicHost(t *testing.T) {
	r := newQoSRig(t)
	r.q.Host = "192.168.1.20"
	resp := r.get(t, "/qos/firewall?vers=1&nint=2")
	i := strings.Index(resp, "<firewall>")
	if i < 0 {
		t.Fatalf("response:\n%q", resp)
	}
	if ip, _ := clientField(resp[i:], "ips.ips"); ip != 0xC0A80114 {
		t.Errorf("ips %#x, want 192.168.1.20", ip)
	}
	if port, _ := clientField(resp[i:], "ports.ports"); int(port) != r.udp {
		t.Errorf("ports %d", port)
	}
}

func TestQoSFiretypeMappedAndMissing(t *testing.T) {
	r := newQoSRig(t)
	r.q.FiretypeWait = 100 * time.Millisecond
	c := r.udpClient(t)
	r.get(t, "/qos/firewall?vers=1&nint=2")
	c.Write(words(2, testSecret))
	time.Sleep(50 * time.Millisecond)
	other := c.LocalAddr().(*net.UDPAddr).Port + 1 // the client says it sent from another port
	if resp := r.get(t, "/qos/firetype?vers=1&rqid=2&rqsc=305419896&inip=2130706433&inpt="+strconv.Itoa(other)); !strings.HasSuffix(resp, "<firetype><firetype>2</firetype></firetype>") {
		t.Errorf("mapped port:\n%q", resp)
	}

	r.get(t, "/qos/firewall?vers=1&nint=2") // requestid 3, no packet
	if resp := r.get(t, "/qos/firetype?vers=1&rqid=3&rqsc=305419896&inip=2130706433&inpt=1"); !strings.HasSuffix(resp, "<firetype><firetype>0</firetype></firetype>") {
		t.Errorf("no packet:\n%q", resp)
	}
	if resp := r.get(t, "/qos/firetype?vers=1&rqid=9&rqsc=1&inip=0&inpt=0"); !strings.HasSuffix(resp, "<firetype><firetype>0</firetype></firetype>") {
		t.Errorf("unknown test:\n%q", resp)
	}
	for _, want := range []string{"firetype: no firewall packet for requestid 3 within 100ms\n", "firetype: no firewall test 9 with that secret\n"} {
		if !strings.Contains(r.log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, r.log.String())
		}
	}
}

func TestQoSNotFound(t *testing.T) {
	r := newQoSRig(t)
	for _, target := range []string{"/qos/qos?vers=1&qtyp=3", "/qos/other", "/"} {
		if resp := r.get(t, target); resp != "HTTP/1.1 404 Not Found\r\nContent-Type: text/plain\r\nContent-Length: 0\r\nConnection: close\r\n\r\n" {
			t.Errorf("%s: %q", target, resp)
		}
	}
	if status, _ := r.q.Answer("POST /qos/qos?qtyp=1 HTTP/1.1", "127.0.0.1"); status != "404 Not Found" {
		t.Errorf("POST: %s", status)
	}
}

func TestQoSRequestIDs(t *testing.T) {
	q := &QoS{}
	q.next = 0xFFFFFFFE
	if id := q.add(&qosTest{}); id != 0xFFFFFFFF {
		t.Errorf("id %#x", id)
	}
	if id := q.add(&qosTest{}); id != 2 { // wraps past 0 and 1
		t.Errorf("id after wrap %d", id)
	}
}

func TestRate(t *testing.T) {
	for _, c := range []struct {
		count, size int
		ms          int64
		want        uint32
	}{
		{10, 1200, 0, 96000000},
		{10, 1200, 4, 24000000},
		{1, 1200, 0, 9600000},
		{64, 1264, 0, 647168000},
		{1000, 1264, 0, 0xFFFFFFFF},
	} {
		if got := Rate(c.count, c.size, c.ms); got != c.want {
			t.Errorf("Rate(%d, %d, %d) = %d, want %d", c.count, c.size, c.ms, got, c.want)
		}
	}
}
