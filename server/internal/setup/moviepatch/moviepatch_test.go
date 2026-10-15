package moviepatch

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"

	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func tag(code int, data []byte) []byte {
	if len(data) < 0x3F {
		return append(binary.LittleEndian.AppendUint16(nil, uint16(code<<6|len(data))), data...)
	}
	out := binary.LittleEndian.AppendUint16(nil, uint16(code<<6|0x3F))
	return append(binary.LittleEndian.AppendUint32(out, uint32(len(data))), data...)
}

func u16s(v ...int) []byte {
	var out []byte
	for _, x := range v {
		out = binary.LittleEndian.AppendUint16(out, uint16(x))
	}
	return out
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// synth is a small made-up movie (re/scripts/test_picker.py builds the same bytes): an import, a sprite with a
// nested long tag, a symbol, a placed object at depth 3, two frames.
func synth() []byte {
	body := cat([]byte{0}, u16s(24<<8, 1),
		tag(69, []byte{8, 0, 0, 0}),
		tag(9, []byte{0x10, 0x20, 0x30}),
		tag(71, cat([]byte("lib.swf\x00\x01\x00"), u16s(1, 9), []byte("Imp\x00"))),
		tag(39, cat(u16s(5, 1), tag(2, cat(u16s(7), make([]byte, 70))), tag(1, nil), tag(0, nil))),
		tag(76, cat(u16s(1, 5), []byte("Foo\x00"))),
		tag(26, cat([]byte{2}, u16s(3, 5))),
		tag(1, nil), tag(1, nil), tag(0, nil))
	head := binary.LittleEndian.AppendUint32([]byte("CFX\x0c"), uint32(len(body)+8-100))
	return append(head, ZlibStored(body)...)
}

func inflate(t *testing.T, z []byte) []byte {
	t.Helper()
	r, err := zlib.NewReader(bytes.NewReader(z))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The SHA-256s patch_login_movie.py gives for the same inputs.
func TestPatchMovieMatchesPython(t *testing.T) {
	m := synth()
	if got, want := sum(m), "28afbd4438f7fc9e187c6c81e86cecbaf714502fab19f24893a385d0386aab68"; got != want || len(m) != 170 {
		t.Fatalf("the made-up movie: %d bytes, %s; test_picker.py's: 170 bytes, %s", len(m), got, want)
	}
	out, err := PatchMovie(m, []byte("OURTAGS"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sum(out), "2de38d5f994b8b116ec59c105917d030e7e882e68e81afd747f876b30a689b4b"; got != want || len(out) != 177 {
		t.Errorf("patched: %d bytes, %s; patch_login_movie.py's: 177 bytes, %s", len(out), got, want)
	}
	// the tags sit before the root's first ShowFrame, after the PlaceObject2; the length grew by their size
	body, newBody := inflate(t, m[8:]), inflate(t, out[8:])
	i := bytes.Index(newBody, []byte("OURTAGS"))
	if i < 0 || !bytes.Equal(newBody[:i], body[:i]) || !bytes.Equal(newBody[i+7:], body[i:]) || !bytes.Equal(body[i:i+2], []byte{0x40, 0}) {
		t.Errorf("the tags aren't spliced in before the first ShowFrame (at %d)", i)
	}
	if binary.LittleEndian.Uint32(out[4:]) != binary.LittleEndian.Uint32(m[4:])+7 {
		t.Errorf("the header's length didn't grow by the tags' size")
	}
	if got := ZlibStored(nil); hex.EncodeToString(got) != "7801010000ffff00000001" {
		t.Errorf("ZlibStored(empty) = %x", got)
	}
}

func TestPatchMovieRefuses(t *testing.T) {
	m := synth()
	once, err := PatchMovie(m, PickerTags)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		in   []byte
		want string
	}{
		{"twice", once, "has the server picker already"},
		{"not a movie", []byte("FWS\x0c\x00\x00\x00\x00"), "not a compressed"},
		{"cut", m[:40], "zlib"},
	} {
		if _, err := PatchMovie(c.in, PickerTags); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	// a movie with the picker's id or depth taken
	body := inflate(t, m[8:])
	for _, c := range []struct{ old, new []byte }{
		{cat(u16s(1, 5), []byte("Foo")), cat(u16s(1, CharID), []byte("Foo"))}, // SymbolClass CharID -> Foo
		{cat([]byte{2}, u16s(3, 5)), cat([]byte{2}, u16s(Depth, 5))},          // PlaceObject2 at Depth
	} {
		changed := append(append([]byte(nil), m[:8]...), ZlibStored(bytes.Replace(body, c.old, c.new, 1))...)
		if _, err := PatchMovie(changed, PickerTags); err == nil || !strings.Contains(err.Error(), "uses") {
			t.Errorf("an id or depth taken: %v", err)
		}
	}
}

func TestPickerTags(t *testing.T) {
	// DefineSprite 2000, DoABC "serverpicker", SymbolClass 2000 -> ServerPicker, PlaceObject3 at depth 2000
	tags, err := tagsOf(PickerTags, 0, len(PickerTags))
	if err != nil || len(tags) != 4 {
		t.Fatalf("picker_tags.hex: %d tags, %v", len(tags), err)
	}
	for i, code := range []int{39, 82, 76, 70} {
		if tags[i].code != code {
			t.Errorf("tag %d: code %d, want %d", i, tags[i].code, code)
		}
	}
	if tags[3].end != len(PickerTags) {
		t.Errorf("bytes after the PlaceObject3")
	}
	d := PickerTags[tags[3].start:tags[3].end]
	if d[0] != 0x06 || binary.LittleEndian.Uint16(d[2:]) != Depth || binary.LittleEndian.Uint16(d[4:]) != CharID {
		t.Errorf("PlaceObject3 %x: not character %d at depth %d without a name", d, CharID, Depth)
	}
}

// The player's own LoginScreen.snap of build 854 (skipped without the game files).
func TestBuildGame854(t *testing.T) {
	raw, err := os.ReadFile(testgame.Data(t, "LoginScreen.snap"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := snap.Parse(out)
	if err != nil || len(a.Entries) != 1 || a.Entries[0].Name != MoviePath {
		t.Fatalf("the archive: %v, %+v", err, a)
	}
	movie, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	s, err := scan(inflate(t, movie[8:]))
	if err != nil || !s.names[ClassName] || !s.ids[CharID] || !s.depths[Depth] {
		t.Errorf("the patched movie: %v; picker class %v, id %v, depth %v", err, s.names[ClassName], s.ids[CharID], s.depths[Depth])
	}
	if s.names["maxis.LoginScreen"] != true {
		t.Errorf("the movie's own document class is gone")
	}
	again, _ := Build(raw)
	if !bytes.Equal(again, out) {
		t.Errorf("Build isn't deterministic")
	}
	if p := os.Getenv("DAWNGATE_PICKER_OUT"); p != "" { // for a byte-for-byte comparison with patch_login_movie.py
		if err := os.WriteFile(p, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
