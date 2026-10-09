package fire

import (
	"bytes"
	"encoding/hex"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"dawngate/server/internal/golden"
	"dawngate/server/internal/logging"
)

func loadFrames(t *testing.T) []golden.Frame {
	t.Helper()
	frames := golden.Load(t).Frames
	if len(frames) == 0 {
		t.Fatal("no frames in tdf_golden.json")
	}
	return frames
}

func TestEncodeMatchesPython(t *testing.T) {
	for _, g := range loadFrames(t) {
		payload, _ := hex.DecodeString(g.Payload)
		b, err := Encode(Frame{Component: g.Component, Command: g.Command, Error: g.Error, Type: MsgType(g.Type), ID: g.ID, Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(b); got != g.Hex {
			t.Errorf("%s:\n got %s\nwant %s", g.Name, got, g.Hex)
		}
	}
}

func TestReaderSplitsStream(t *testing.T) {
	frames := loadFrames(t)
	var stream []byte
	for _, g := range frames {
		b, _ := hex.DecodeString(g.Hex)
		stream = append(stream, b...)
	}
	stream = append(stream, 0x00, 0x05, 0x00) // a partial header at the end
	// one byte per Read: every frame boundary falls between reads
	r := NewReader(iotest.OneByteReader(bytes.NewReader(stream)))
	for _, g := range frames {
		f, err := r.Next()
		if err != nil {
			t.Fatalf("%s: %v", g.Name, err)
		}
		payload, _ := hex.DecodeString(g.Payload)
		if f.Component != g.Component || f.Command != g.Command || f.Error != g.Error ||
			f.Type != MsgType(g.Type) || f.ID != g.ID&0xFFFFF || !bytes.Equal(f.Payload, payload) {
			t.Errorf("%s: got %+v", g.Name, f)
		}
	}
	if _, err := r.Next(); err != io.EOF {
		t.Errorf("end: %v, want EOF", err)
	}
	if p := r.Pending(); !bytes.Equal(p, []byte{0x00, 0x05, 0x00}) {
		t.Errorf("pending % x", p)
	}
}

func TestEncodeRejectsOversizedPayload(t *testing.T) {
	if _, err := Encode(Frame{Payload: make([]byte, 0x10000)}); err == nil {
		t.Error("no error for a 65536-byte payload")
	}
}

// The component ids are the client's own (B4, docs/data/blaze_components.txt): the id each component's
// constructor stores at +8, named by its getComponentName.
func TestComponentsMatchClient(t *testing.T) {
	want := map[uint16]string{
		1: "Authentication", 4: "GameManager", 5: "Redirector", 6: "Playgroups", 7: "Stats",
		9: "Util", 10: "CensusData", 15: "Messaging", 21: "Rooms", 25: "AssociationLists",
		27: "GpsContentController", 28: "GameReporting", 0x7802: "UserSessions",
		0x081F: "BattlegroundsAccounts", 0x0820: "BGEcommerce", 0x0823: "BGOps", 0x0825: "Entry",
		0x0826: "LivingLore", 0x0827: "BGWeb",
	}
	if len(Components) != len(want) {
		t.Errorf("%d components, want %d", len(Components), len(want))
	}
	for id, name := range want {
		if got := ComponentName(id); got != name {
			t.Errorf("ComponentName(%#x) = %q, want %q", id, got, name)
		}
	}
	ids := ComponentIDs()
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("ComponentIDs not ascending: %v", ids)
		}
	}
}

// The describe lines are the ones run_session.ps1 parses ("<- Name(0x....) cmd 0x.... type").
func TestDescribe(t *testing.T) {
	var out bytes.Buffer
	log := logging.New(&out)
	Describe(log, "blaze", Frame{Component: 9, Command: 2, Type: Request, ID: 1})
	Describe(log, "blaze", Frame{Component: 0x99, Command: 1, Type: 5, ID: 3, Error: 0x4004, Payload: []byte{0x86}})
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	want := []string{
		"[blaze] <- Util(0x0009) cmd 0x0002 request id 1 err 0x0, 0 bytes",
		"[blaze]    TDF:",
		"",
		"[blaze] <- component153(0x0099) cmd 0x0001 5 id 3 err 0x4004, 1 bytes",
		"[blaze]    (TDF decode failed: truncated TDF)",
		"    0000  86                                               .",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines:\n%s", len(lines), out.String())
	}
	for i, w := range want {
		got := lines[i]
		if strings.HasPrefix(w, "[") {
			got = got[strings.IndexByte(got, ' ')+1:] // drop the timestamp
		}
		if got != w {
			t.Errorf("line %d:\n got %q\nwant %q", i, got, w)
		}
	}
}
