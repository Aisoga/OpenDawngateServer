package exepatch

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

// The draggrab piece's golden bytes, the same as the Python patcher's tests pin: assembled from patch_client.py's
// drag_grab_code (its docstring is the source).
const dragGrabCodeHex = "" +
	"803ddd7f970100753968128095148b0d1841ba01e8d7f7dffea2cc00c00184c07429c6442418016a01e8823ca1fe6a00" +
	"68c000c001e85653a1fe83c40ce96340a1fe803dcc00c001007511803ddd7f970100c644241801e9583ea1fec6442418" +
	"01e89ab8defe8b108bc8ff526c8b108bc8ff521c68dc00c00168d000c0018bc8e8db75defef30f1015e400c001f30f5c" +
	"15d800c0010f57db0f2ed30f8af70000000f84f1000000f30f1005c800c001f30f5c05d800c001f30f5ec2f30f100ddc" +
	"00c001f30f5c0dd000c001f30f59c8f30f580dd000c001f30f1015c000c001f30f5cd1f30f100de000c001f30f5c0dd4" +
	"00c001f30f59c8f30f580dd400c001f30f101dc400c001f30f5cd90f28c2f30f59c20f28cbf30f59cbf30f58c10f2f05" +
	"d041c8017672837b3006755d8b432c83f803745583f8047450f30f100524573f01f30f58c2f30f110524573f01f30f10" +
	"0528573f01f30f58c3f30f110528573f01f30f100530573f01f30f58c2f30f110530573f01f30f100534573f01f30f58" +
	"c3f30f110534573f0133c0894424088944244ce9633fa1fee9083fa1fe000000803dcc00c001007505e9528e9efe33c0" +
	"c3000000000000000000000000000000bd378635"

func TestDragGrabCode(t *testing.T) {
	code := dragGrabCode()
	if hex.EncodeToString(code) != dragGrabCodeHex {
		t.Errorf("draggrab piece\n%x\nwant\n%s", code, dragGrabCodeHex)
	}
	if _, room, _ := dgxPieceAt("draggrab"); len(code) > int(room) {
		t.Errorf("%d bytes for a room of %d", len(code), room)
	}
	want := []struct{ va, orig, new string }{
		{"00697eb8", "803ddd7f970100c644241801", "e953c15e0190909090909090"}, // jmp 0x01C84010 + 7 nops
		{"00697d3c", "e8cf52fdff", "e86fc45e01"},                             // call 0x01C841B0 (the spectator helper)
		{"006981a0", "e86b4efdff", "e80bc05e01"},
	}
	sites := dragGrabSites()
	if len(sites) != len(want) {
		t.Fatalf("%d sites", len(sites))
	}
	for i, s := range sites {
		if got := []string{hex.EncodeToString(le32be(s.va)), hex.EncodeToString(s.orig), hex.EncodeToString(s.new)}; got[0] != want[i].va || got[1] != want[i].orig || got[2] != want[i].new {
			t.Errorf("site %v, want %v", got, want[i])
		}
	}
	if binary.LittleEndian.Uint32(code[dragEps2Offset:]) != 0x358637BD { // 1e-6 as a float
		t.Error("eps2")
	}
}

func le32be(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// On the synthetic exe: the piece, the sites, .tls mapped, describe on / as-shipped / unknown, and the drag grab
// left out with NoDragGrab or NoDgx.
func TestDragGrabSynthetic(t *testing.T) {
	orig := SyntheticExe()
	p, summary, err := Patch(orig, modB, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "; the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400)") {
		t.Errorf("summary %q", summary)
	}
	img, err := parsePE(p)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := img.bytesAt(0x01C84010, len(dragGrabCodeHex)/2); err != nil || hex.EncodeToString(got) != dragGrabCodeHex {
		t.Errorf("the piece in the exe (%v)", err)
	}
	if got, _ := img.bytesAt(0x01C84010+uint32(len(dragGrabCodeHex)/2), 0x3F0-len(dragGrabCodeHex)/2); !bytes.Equal(got, make([]byte, len(got))) {
		t.Error("bytes after the piece")
	}
	if state, err := img.bytesAt(dragState, dragStateRoom); err != nil || !bytes.Equal(state, make([]byte, dragStateRoom)) {
		t.Errorf("the state in .tls (%v)", err)
	}
	for _, s := range img.sections {
		if s.name == ".tls" && s.vsize != tlsRaw {
			t.Errorf(".tls VirtualSize %#x", s.vsize)
		}
	}
	noGrab, noSummary, err := Patch(orig, modB, Options{NoDragGrab: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(noSummary, "OptionDragGrab") {
		t.Errorf("summary without the drag grab %q", noSummary)
	}
	noDgx, _, err := Patch(orig, modB, Options{NoDgx: true})
	if err != nil {
		t.Fatal(err)
	}
	// only the two pieces and the nine sites differ from the section without the drag grab
	allowed := [][2]int{{0x110A810, len(dragGrabCodeHex) / 2}, {0x110AC00, optNameOffset + len(optName)}}
	for _, s := range append(dragGrabSites(), dragOptionsSites()...) {
		allowed = append(allowed, [2]int{synthOffset(s.va), len(s.new)})
	}
	for _, r := range diffRanges(noGrab, p) {
		ok := false
		for _, a := range allowed {
			ok = ok || a[0] <= r[0] && r[1] <= a[0]+a[1]
		}
		if !ok {
			t.Errorf("unexpected change at %#x..%#x", r[0], r[1])
		}
	}
	broken := bytes.Clone(p)
	broken[0x110A810+5] ^= 1 // a byte of the cave
	halfway := bytes.Clone(noGrab)
	copy(halfway[synthOffset(dragHook):], dragGrabSites()[0].new) // the hook without its piece
	noTable := bytes.Clone(p)
	copy(noTable[synthOffset(0x004D414E):], dragOptionsSites()[2].orig) // the table's bound as shipped
	for _, c := range []struct {
		name string
		b    []byte
		want string
	}{{"untouched", orig, "as-shipped"}, {"default", p, "on"}, {"NoDragGrab", noGrab, "as-shipped"},
		{"NoDgx", noDgx, "as-shipped"}, {"a changed cave", broken, "unknown"}, {"the hook alone", halfway, "unknown"},
		{"a bound as shipped", noTable, "unknown"}} {
		if d, err := Describe(c.b); err != nil || d["draggrab"] != c.want {
			t.Errorf("%s: describe draggrab %q, want %q (%v)", c.name, d["draggrab"], c.want, err)
		}
	}
	// a site that isn't the shipped instruction: refused
	bad := bytes.Clone(orig)
	bad[synthOffset(0x006981A0)] = 0x90
	if _, _, err := Patch(bad, modB, Options{NoDgx: true}); err == nil || !strings.Contains(err.Error(), "the drag scroll's hook site at 0x006981a0") {
		t.Errorf("a foreign site: %v", err)
	}
	// the state's room already used: refused
	used := bytes.Clone(orig)
	used[synthOffset(dragState+0x20)] = 1
	if _, _, err := Patch(used, modB, Options{}); err == nil || !strings.Contains(err.Error(), "unused slack after the rating text") {
		t.Errorf("used state: %v", err)
	}
}

// dragOptionsTailHex is the options piece after EA's 12 entries: the 13th entry {0x14958012, 0x01C84470}, zeros and
// "OptionDragGrab" at +0x70 (the same golden bytes as the Python patcher's tests).
const dragOptionsTailHex = "128095147044c801" + "0000000000000000" + "4f7074696f6e44726167477261620" + "0"

// DRAGGRAB2b: the options piece and the table's six readers, on the synthetic exe (which holds the first hash only).
func TestDragOptionsTable(t *testing.T) {
	table := make([]byte, optTableSize)
	for i := range table {
		table[i] = byte(i)
	}
	piece, err := dragOptionsPiece(table)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(piece[:optTableSize], table) || hex.EncodeToString(piece[optTableSize:]) != dragOptionsTailHex {
		t.Errorf("piece %x", piece)
	}
	if _, err := dragOptionsPiece(table[1:]); err == nil {
		t.Error("a short table: accepted")
	}
	p, _, err := Patch(SyntheticExe(), modB, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := dragOptionsPiece(append(bytes.Clone(optTableFirst), make([]byte, optTableSize-4)...))
	if !bytes.Equal(p[0x110AC00:0x110AC00+len(want)], want) {
		t.Errorf("the piece in the exe: %x", p[0x110AC00:0x110AC00+len(want)])
	}
	for _, s := range dragOptionsSites() {
		if got := p[synthOffset(s.va) : synthOffset(s.va)+len(s.new)]; !bytes.Equal(got, s.new) {
			t.Errorf("site %#x: %x", s.va, got)
		}
	}
	// the readers point at the copy: GetGameSettings 0x004D4070 (hash) / 0x004D40AC (name), SaveSettings 0x004D66A6
	// (hash) / 0x004D66A0 (name)
	for va, disp := range map[uint32]uint32{0x004D4070: 0x01C84400, 0x004D40AC: 0x01C84404, 0x004D66A6: 0x01C84400, 0x004D66A0: 0x01C84404} {
		if got := binary.LittleEndian.Uint32(p[synthOffset(va)+2:]); got != disp {
			t.Errorf("%#x reads %#x, want %#x", va, got, disp)
		}
	}
	// a table that doesn't start with OptionFilterProfanity's hash: refused (wrong build)
	bad := SyntheticExe()
	bad[synthOffset(optTable)] ^= 1
	if _, _, err := Patch(bad, modB, Options{NoDragGrab: true}); err == nil || !strings.Contains(err.Error(), "the gameplay options table") {
		t.Errorf("a foreign table: %v", err)
	}
}
