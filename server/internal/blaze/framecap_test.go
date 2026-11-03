package blaze

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/tdf"
)

// blobPayload is a payload of exactly n bytes: one blob member.
func blobPayload(n int) []byte {
	for size := n - 7; ; size++ { // the header and length take 6 or 7 bytes
		p := tdf.NewWriter().Blob("DATA", bytes.Repeat([]byte{'x'}, size)).Bytes()
		if len(p) == n {
			return p
		}
		if len(p) > n {
			panic("no blob payload of that size")
		}
	}
}

// writeRaw writes a frame without failing the test when the server has already closed the pipe.
func (ps *pipeSession) writeRaw(comp, cmd uint16, id uint32, payload []byte) {
	b, err := fire.Encode(fire.Frame{Component: comp, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
	if err != nil {
		ps.t.Fatal(err)
	}
	ps.client.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = ps.client.Write(b)
}

// Before login a frame over fire.PreLoginMaxPayload closes the connection unanswered (its payload isn't read);
// one at the cap is served.
func TestPreLoginFrameCap(t *testing.T) {
	ps := newPipeSession(t)
	ps.send(ComponentUtil, CmdUtilPing, 0, blobPayload(fire.PreLoginMaxPayload))
	if f := ps.next("ping at the cap"); f.Type != fire.Reply || f.ID != 0 {
		t.Fatalf("ping at the cap: %s id %d err %#x", f.Type, f.ID, f.Error)
	}
	ps.writeRaw(ComponentUtil, CmdUtilPing, 1, blobPayload(fire.PreLoginMaxPayload+1))
	ps.client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if f, err := ps.r.Next(); err == nil {
		t.Fatalf("a frame over the cap was answered: %s id %d", f.Type, f.ID)
	}
	log := ps.close()
	if !strings.Contains(log, "connection closed: fire: frame over the payload cap: 4097 bytes claimed, 4096 allowed") {
		t.Fatalf("log:\n%s", log)
	}
	if strings.Contains(log, "unparsed bytes") {
		t.Fatalf("the refused frame was dumped:\n%s", log)
	}
}

// After login the cap is the wire's own: a frame over the pre-login cap is served.
func TestPostLoginFrameCap(t *testing.T) {
	ps := newPipeSession(t)
	ps.loginAs("player@example.com", 0)
	ps.send(ComponentUtil, CmdUtilPing, 5, blobPayload(16000))
	if f := ps.next("big ping"); f.Type != fire.Reply || f.ID != 5 {
		t.Fatalf("big ping after login: %s id %d err %#x", f.Type, f.ID, f.Error)
	}
	ps.close()
}

// A request whose TDF claims more than it holds (a list of 1<<20 items in a few bytes, a nest past tdf.MaxDepth)
// gets ERR_SYSTEM, as any request that doesn't decode, and the connection goes on.
func TestLyingRequestPayload(t *testing.T) {
	ps := newPipeSession(t)
	label := tdf.EncodeLabel("LIST")
	lying := append(label[:], byte(tdf.TypeList), byte(tdf.TypeInt), 0x80, 0x80, 0x80, 0x01) // count 1<<20
	deep := append([]byte(nil), label[:]...)
	deep = append(deep, byte(tdf.TypeList))
	for range tdf.MaxDepth + 10 {
		deep = append(deep, byte(tdf.TypeList), 1)
	}
	deep = append(deep, byte(tdf.TypeInt), 0)
	for i, p := range [][]byte{lying, deep} {
		ps.send(ComponentUtil, CmdUtilPing, uint32(i), p)
		if f := ps.next("lying request"); f.Type != fire.ErrorReply || f.Error != ErrSystem || f.ID != uint32(i) {
			t.Fatalf("request %d: %s id %d err %#x", i, f.Type, f.ID, f.Error)
		}
	}
	ps.send(ComponentUtil, CmdUtilPing, 2, nil)
	if f := ps.next("ping"); f.Type != fire.Reply {
		t.Fatalf("ping after the lying requests: %s err %#x", f.Type, f.Error)
	}
	log := ps.close()
	for _, want := range []string{"(TDF decode failed: count 1048576 too large", "(TDF decode failed: nested deeper than 32"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
}
