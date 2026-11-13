package exepatch

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The same constants as the Python patcher's tests: the synthetic exe, its default patch with bytes(range(128)) (since
// DRAGGRAB1 with the drag grab's piece in .dgx) and the same without the section, so both patchers write the same
// bytes; and .dgx's layout.
const (
	synthSHA256      = "20f7bf51f3d71cf03c4804820cff69a6e150f364588c350751f722cce3c4cb97"
	synthDgxSHA256   = "beb8c94260cfb7d2f72a067e8644832703c35e7649004deef02d6ff43b90452f"
	synthNoDgxSHA256 = "3d33ab4c634b99f087d6ed98d41c9d0a029bdef2ff461c8e7827394df24a4b59"
	synthOpt         = 0x80 + 4 + 20
	dgxHeaderAt      = synthOpt + 0xE0 + 5*40
)

func TestDgxSynthetic(t *testing.T) {
	orig := SyntheticExe()
	if sha256Hex(orig) != synthSHA256 {
		t.Errorf("synthetic exe %s, want %s", sha256Hex(orig), synthSHA256)
	}
	full, _, err := Patch(orig, modB, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// the section's own checks without the drag grab's piece and sites (draggrab_test.go checks those)
	p, summary, err := Patch(orig, modB, Options{NoDragGrab: true, NoProfileReload: true})
	if err != nil {
		t.Fatal(err)
	}
	nodgx, noSummary, err := Patch(orig, modB, Options{NoDgx: true, NoProfileReload: true})
	if err != nil {
		t.Fatal(err)
	}
	if sha256Hex(full) != synthDgxSHA256 || sha256Hex(nodgx) != synthNoDgxSHA256 {
		t.Errorf("patched %s, without the section %s", sha256Hex(full), sha256Hex(nodgx))
	}
	if summary != noSummary+"; an executable section .dgx at 0x01c84000 (4096 bytes) for later patches" {
		t.Errorf("summary %q", summary)
	}
	u32 := func(b []byte, o int) uint32 { return binary.LittleEndian.Uint32(b[o:]) }
	if len(orig) != 0x110A710 || len(p) != 0x110A800+0x1000 {
		t.Errorf("lengths %#x %#x", len(orig), len(p))
	}
	want := append(append(append(append(append([]byte(".dgx\x00\x00\x00\x00"), le32(0x1000)...), le32(0x1884000)...),
		le32(0x1000)...), le32(0x110A800)...), append(make([]byte, 12), le32(0x60000020)...)...)
	if !bytes.Equal(p[dgxHeaderAt:dgxHeaderAt+40], want) {
		t.Errorf("section header % x", p[dgxHeaderAt:dgxHeaderAt+40])
	}
	if binary.LittleEndian.Uint16(p[0x80+6:]) != 6 || u32(p, synthOpt+56) != 0x1885000 || u32(p, synthOpt+4) != 0xDAB200 ||
		!bytes.Equal(p[synthOpt+128:synthOpt+136], make([]byte, 8)) || u32(p, synthOpt+64) != u32(orig, synthOpt+64) {
		t.Error("the headers' fields")
	}
	if !bytes.Equal(p[synthCertAt:len(orig)], orig[synthCertAt:]) || !bytes.Equal(p[len(orig):0x110A800], make([]byte, 0x110A800-len(orig))) ||
		!bytes.Equal(p[0x110A800:0x110A808], dgxMagic) || !bytes.Equal(p[0x110A808:], make([]byte, 0x1000-8)) {
		t.Error("the certificate, the padding or the section's bytes")
	}
	fields := [][2]int{{0x80 + 6, 2}, {synthOpt + 4, 4}, {synthOpt + 56, 4}, {synthOpt + 128, 8}, {dgxHeaderAt, 40}}
	for _, r := range diffRanges(nodgx, p[:len(nodgx)]) {
		ok := false
		for _, f := range fields {
			ok = ok || f[0] <= r[0] && r[1] <= f[0]+f[1]
		}
		if !ok {
			t.Errorf("unexpected change at %#x..%#x", r[0], r[1])
		}
	}
	img, err := parsePE(p)
	if err != nil {
		t.Fatal(err)
	}
	if off, err := img.offset(dgxVA+0x400, 1); err != nil || off != 0x110AC00 {
		t.Errorf("offset of 0x01C84400: %#x %v", off, err)
	}
	// describe
	for _, c := range []struct {
		b    []byte
		want string
	}{{orig, "as-shipped"}, {nodgx, "as-shipped"}, {p, "on"}} {
		if d, err := Describe(c.b); err != nil || d["dgx"] != c.want {
			t.Errorf("describe dgx %q, want %q (%v)", d["dgx"], c.want, err)
		}
	}
	writable := bytes.Clone(p)
	writable[dgxHeaderAt+39] |= 0x80
	nomagic := bytes.Clone(p)
	nomagic[0x110A800] = 0
	for _, b := range [][]byte{writable, nomagic} {
		if d, _ := Describe(b); d["dgx"] != "unknown" {
			t.Errorf("describe dgx %q, want unknown", d["dgx"])
		}
	}
	if n := len(DescriptionKeys); DescriptionKeys[n-3] != "dgx" || DescriptionKeys[n-2] != "draggrab" ||
		DescriptionKeys[n-1] != "profilereload" {
		t.Error("dgx, draggrab and profilereload are not the last keys")
	}
}

func TestDgxLayout(t *testing.T) {
	want := []dgxPiece{{"header", 0x000, 0x010}, {"draggrab", 0x010, 0x3F0}, {"options", 0x400, 0x100}} // DGX_LAYOUT
	if len(dgxLayout) != len(want) {
		t.Fatalf("layout %v", dgxLayout)
	}
	end := uint32(0)
	for i, p := range dgxLayout {
		if p != want[i] || p.offset%16 != 0 || p.offset < end {
			t.Errorf("piece %v", p)
		}
		end = p.offset + p.room
	}
	if end > dgxSize {
		t.Errorf("the layout ends at %#x", end)
	}
	if va, room, err := dgxPieceAt("options"); va != 0x01C84400 || room != 0x100 || err != nil {
		t.Errorf("options: %#x %#x %v", va, room, err)
	}
	if !bytes.Equal(dgxMagic, []byte("DGX\x00\x01\x00\x00\x00")) {
		t.Error("magic")
	}
}

func TestDgxPlaceAndRefusals(t *testing.T) {
	orig := SyntheticExe()
	p, _, _ := Patch(orig, modB, Options{NoDragGrab: true})
	nodgx, _, _ := Patch(orig, modB, Options{NoDgx: true})
	data := bytes.Clone(p)
	if va, err := dgxPlace(data, "draggrab", []byte{0xC3}); va != 0x01C84010 || err != nil || data[0x110A810] != 0xC3 {
		t.Errorf("place: %#x %v", va, err)
	}
	for _, c := range []struct {
		data  []byte
		piece string
		b     []byte
		want  string
	}{
		{data, "draggrab", []byte{0x90}, "written already"},
		{data, "options", make([]byte, 257), "room for 256 bytes, not 257"},
		{data, "nothing", []byte{0x90}, "no piece 'nothing'"},
		{bytes.Clone(nodgx), "options", []byte{1}, "needs the section .dgx"},
	} {
		if _, err := dgxPlace(c.data, c.piece, c.b); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.piece, err, c.want)
		}
	}
	if _, err := addDgxSection(p); err == nil || !strings.Contains(err.Error(), "unexpected PE header for .dgx (6 sections") {
		t.Errorf("twice: %v", err)
	}
	if _, err := addDgxSection(append(bytes.Clone(orig), 0)); err == nil || !strings.Contains(err.Error(), "doesn't end the file") {
		t.Errorf("a byte after the certificate: %v", err)
	}
	used := bytes.Clone(orig)
	used[dgxHeaderAt] = 1
	if _, err := addDgxSection(used); err == nil || !strings.Contains(err.Error(), "no free section header") {
		t.Errorf("a used header slot: %v", err)
	}
	image := bytes.Clone(orig)
	binary.LittleEndian.PutUint32(image[synthOpt+56:], 0x1885000)
	if _, err := addDgxSection(image); err == nil || !strings.Contains(err.Error(), "SizeOfImage 0x1885000") {
		t.Errorf("another SizeOfImage: %v", err)
	}
	// without a certificate the section still goes after the file's end
	unsigned := bytes.Clone(orig[:synthCertAt])
	copy(unsigned[synthOpt+128:], make([]byte, 8))
	if out, err := addDgxSection(unsigned); err != nil || binary.LittleEndian.Uint32(out[dgxHeaderAt+20:]) != synthCertAt {
		t.Errorf("unsigned: %v", err)
	}
}

// A copy patched by an older release (no section) is patched again from its .orig, as Start-Client does; a second
// run writes nothing; an older release (NoDgx) makes the file short again.
func TestDgxPatchFileUpgrade(t *testing.T) {
	orig := SyntheticExe()
	older, _, _ := Patch(orig, modA, Options{NoDgx: true})
	want, _, _ := Patch(orig, modA, Options{})
	exe := filepath.Join(t.TempDir(), "Dawngate.exe")
	if os.WriteFile(exe, older, 0o644) != nil || os.WriteFile(exe+OrigSuffix, orig, 0o644) != nil {
		t.Fatal("write")
	}
	fo := FileOptions{BaseSHA256: sha256Hex(orig)}
	if why, err := State(exe, modA, fo, nil); err != nil || why != "dgx as-shipped (want on); draggrab as-shipped (want on)" {
		t.Errorf("state of an older patch: %q %v", why, err)
	}
	if why, err := State(exe, modA, fo, HostKeys); err != nil || why != "" {
		t.Errorf("host keys: %q %v", why, err)
	}
	if res, err := PatchFile(exe, modA, fo); err != nil || !res.Changed || res.SavedOrig {
		t.Errorf("upgrade: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, want) {
		t.Error("the upgraded exe is not Patch's output")
	}
	if res, err := PatchFile(exe, modA, fo); err != nil || res.Changed {
		t.Errorf("again: %+v %v", res, err)
	}
	if why, err := State(exe, modA, fo, nil); err != nil || why != "" {
		t.Errorf("state: %q %v", why, err)
	}
	back := FileOptions{Options: Options{NoDgx: true}, BaseSHA256: fo.BaseSHA256}
	if res, err := PatchFile(exe, modA, back); err != nil || !res.Changed {
		t.Errorf("back: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, older) {
		t.Error("the exe without the section is not Patch's output")
	}
}
