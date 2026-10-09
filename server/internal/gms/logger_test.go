package gms

import (
	"bytes"
	"net"
	"strings"
	"testing"

	"dawngate/server/internal/logging"
)

// ocr1 is what the client sent to the GMS address in b8_run5: ID_OPEN_CONNECTION_REQUEST_1, the
// offline magic, protocol version 5, zero padding up to the MTU probe size.
func ocr1(size int) []byte {
	p := append(append([]byte{0x05}, OfflineMagic...), 5)
	return append(p, make([]byte, size-len(p))...)
}

func TestDescribe(t *testing.T) {
	for _, c := range []struct {
		p    []byte
		want string
	}{
		{ocr1(1464), "0x05 ID_OPEN_CONNECTION_REQUEST_1, offline magic, protocol version 5, padded to 1464 bytes (MTU probe)"},
		{[]byte{0x05, 1, 2}, "0x05 ID_OPEN_CONNECTION_REQUEST_1, no offline magic"},
		{append(append([]byte{0x07}, OfflineMagic...), 1, 2), "0x07 ID_OPEN_CONNECTION_REQUEST_2, offline magic"},
		{append(append(make([]byte, 9), OfflineMagic...), 0), "0x00 unknown"},
		{append(append([]byte{0x01, 0, 0, 0, 0, 0, 0, 0, 1}, OfflineMagic...), 0), "0x01 ID_UNCONNECTED_PING, offline magic"},
		{[]byte{0x84, 0, 0, 0}, "0x84 connected datagram (RakNet bit 0x80)"},
		{nil, "empty"},
	} {
		if got := Describe(c.p); got != c.want {
			t.Errorf("Describe(% x...) = %q, want %q", c.p[:min(len(c.p), 4)], got, c.want)
		}
	}
}

// Every datagram gets a line; the first of each peer, kind and length also a dump without its padding.
func TestPacketLog(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{Log: logging.New(&buf)}
	from := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 58238}
	l.Packet(ocr1(1464), from)
	l.Packet(ocr1(1464), from)
	l.Packet(ocr1(548), from)
	text := buf.String()
	if n := strings.Count(text, "[gms] udp ('127.0.0.1', 58238) "); n != 3 {
		t.Errorf("%d datagram lines:\n%s", n, text)
	}
	if n := strings.Count(text, "first of its kind"); n != 2 {
		t.Errorf("%d dumps:\n%s", n, text)
	}
	for _, want := range []string{"    0010  78 05 ", "(then 1446 zero bytes)", "(then 530 zero bytes)"} {
		if !strings.Contains(text, want) {
			t.Errorf("log lacks %q:\n%s", want, text)
		}
	}
}
