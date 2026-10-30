package snap

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"

	"dawngate/server/internal/setup/testgame"
)

func unhex(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		panic(err)
	}
	return b
}

// Handcrafted Snappy blocks for every tag form (format_description.txt).
func TestDecodeForms(t *testing.T) {
	long := strings.Repeat("x", 100)
	for _, c := range []struct{ name, block, want string }{
		{"literal", "05 1068656c6c6f", "hello"},
		{"literal, 1-byte length", "64 f063" + hex.EncodeToString([]byte(long)), long},
		{"copy 1-byte offset, overlapping", "0a 0061 1501", "aaaaaaaaaa"},
		{"copy 2-byte offset", "0c 0c61626364 1e0400", "abcdabcdabcd"},
		{"copy 4-byte offset", "08 0c61626364 0f04000000", "abcdabcd"},
		{"empty", "00", ""},
	} {
		got, err := Decode(unhex(c.block), -1)
		if err != nil || string(got) != c.want {
			t.Errorf("%s: %q %v, want %q", c.name, got, err, c.want)
		}
	}
	// SnappyLiterals' 2-byte lengths
	big := bytes.Repeat([]byte("0123456789"), 7000)
	got, err := Decode(testgame.SnappyLiterals(big), int64(len(big)))
	if err != nil || !bytes.Equal(got, big) {
		t.Errorf("literals: %v", err)
	}
}

// DecodePrefix stops once the bytes asked for are out, and takes a block cut anywhere after them.
func TestDecodePrefix(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789"), 7000)
	block := Encode(big)
	for _, cut := range []int{len(block), 300, 160} {
		got, err := DecodePrefix(block[:cut], 128)
		if err != nil || len(got) < 128 || !bytes.Equal(got, big[:len(got)]) {
			t.Errorf("cut at %d: %d bytes, %v", cut, len(got), err)
		}
	}
	lit := testgame.SnappyLiterals(big) // one long literal, cut inside it
	if got, err := DecodePrefix(lit[:200], 128); err != nil || len(got) < 128 || !bytes.Equal(got, big[:len(got)]) {
		t.Errorf("literal cut: %d bytes, %v", len(got), err)
	}
	if got, err := DecodePrefix(unhex("05 1068656c6c6f"), 128); err != nil || string(got) != "hello" {
		t.Errorf("a short block: %q %v", got, err)
	}
}

func TestDecodeErrors(t *testing.T) {
	for name, block := range map[string]string{
		"no varint":                "",
		"varint cut short":         "80",
		"varint too long":          "8080808080 00",
		"literal past the end":     "05 1068656c6c",
		"literal length cut short": "05 f0",
		"copy before the start":    "04 0105",
		"copy offset 0":            "05 0061 0100",
		"copy cut short":           "05 0061 02",
		"4-byte copy cut short":    "05 0061 030100",
		"less than its size":       "06 1068656c6c6f",
		"more than its size":       "04 1068656c6c6f",
		"copy beyond its size":     "04 0061 1e0100",
	} {
		if got, err := Decode(unhex(block), -1); err == nil {
			t.Errorf("%s: accepted (%q)", name, got)
		}
	}
	if _, err := Decode(unhex("05 1068656c6c6f"), 6); err == nil {
		t.Error("a block whose size differs from the index's")
	}
}

func TestArchive(t *testing.T) {
	files := []testgame.SnapFile{
		{Name: "script/preload.txt", Data: []byte("assetdata/binary\n")},
		{Name: "Audio/1.ogg", Data: []byte("RIFF...."), Stored: true},
		{Name: "AssetData/Binary/a.Noun.bin", Data: bytes.Repeat([]byte{7}, 300)},
		{Name: "empty.bin", Data: nil},
	}
	raw := testgame.Snap(files)
	a, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Entries) != 4 || a.Version != 0x200 || a.Unknown != 1 {
		t.Fatalf("%+v", a)
	}
	for i, f := range files {
		e := a.Entries[i]
		if e.Name != f.Name || e.Stored() != f.Stored || e.Index != i {
			t.Errorf("entry %d: %+v", i, e)
		}
		got, err := a.Read(e)
		if err != nil || !bytes.Equal(got, f.Data) {
			t.Errorf("%s: %q %v", f.Name, got, err)
		}
	}
	if notes := a.LayoutNotes(); len(notes) != 0 {
		t.Errorf("notes: %v", notes)
	}
	if a, err := Parse(testgame.Snap(nil)); err != nil || len(a.Entries) != 0 || len(a.LayoutNotes()) != 0 {
		t.Errorf("an empty archive (Shared.snap): %v", err)
	}

	// a stored entry whose sizes differ, an index entry outside the data, a name outside the table
	bad := append([]byte(nil), raw...)
	idx := a.IndexOffset + 1*EntrySize
	binary.LittleEndian.PutUint32(bad[idx+4:], 3)
	b, _ := Parse(bad)
	if _, err := b.Read(b.Entries[1]); err == nil {
		t.Error("stored entry with two sizes read")
	}
	bad = append([]byte(nil), raw...)
	binary.LittleEndian.PutUint32(bad[idx+24:], uint32(a.IndexOffset))
	if _, err := Parse(bad); err == nil {
		t.Error("data in the index accepted")
	}
	bad = append([]byte(nil), raw...)
	binary.LittleEndian.PutUint32(bad[idx:], 1<<20)
	if _, err := Parse(bad); err == nil {
		t.Error("name outside the table accepted")
	}
	bad = append([]byte(nil), raw...)
	binary.LittleEndian.PutUint32(bad[8:], 1<<20)
	if _, err := Parse(bad); err == nil {
		t.Error("an index larger than the file accepted")
	}
	if _, err := Parse([]byte("snap")); err == nil {
		t.Error("not an archive")
	}
	// layout notes
	odd := append([]byte(nil), raw...)
	odd[0x10] = 1
	binary.LittleEndian.PutUint32(odd[idx+12:], 5)
	binary.LittleEndian.PutUint32(odd[idx+28:], 3)
	o, _ := Parse(odd)
	if notes := o.LayoutNotes(); len(notes) != 3 {
		t.Errorf("notes: %q", notes)
	}
}
