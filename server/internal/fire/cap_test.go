package fire

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"dawngate/server/internal/logging"
)

// A header over MaxPayload is refused as soon as it is in: the reader doesn't wait for (or buffer) the payload it
// claims, and a frame at the cap still passes.
func TestReaderPayloadCap(t *testing.T) {
	at, _ := Encode(Frame{Component: 9, Command: 2, Payload: make([]byte, 100)})
	over, _ := Encode(Frame{Component: 9, Command: 2, ID: 1, Payload: make([]byte, 101)})
	// the stream ends after the second header: a reader that waited for its payload would see EOF instead
	r := NewReader(iotest.OneByteReader(bytes.NewReader(append(at, over[:HeaderLen]...))))
	r.MaxPayload = 100
	if f, err := r.Next(); err != nil || len(f.Payload) != 100 {
		t.Fatalf("frame at the cap: %v, %d bytes", err, len(f.Payload))
	}
	_, err := r.Next()
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("frame over the cap: %v", err)
	}
	if want := "fire: frame over the payload cap: 101 bytes claimed, 100 allowed (component 0x0009, command 0x0002)"; err.Error() != want {
		t.Fatalf("error %q, want %q", err, want)
	}
	var b strings.Builder
	LogEnd(logging.New(&b), "blaze", r, err)
	if got := b.String(); !strings.Contains(got, "connection closed: fire: frame over the payload cap") ||
		strings.Contains(got, "unparsed") {
		t.Fatalf("LogEnd: %s", got)
	}
	// without a cap the wire's own limit holds: the largest frame passes
	big, _ := Encode(Frame{Payload: make([]byte, MaxPayload)})
	if f, err := NewReader(bytes.NewReader(big)).Next(); err != nil || len(f.Payload) != MaxPayload {
		t.Fatalf("largest frame: %v, %d bytes", err, len(f.Payload))
	}
}
