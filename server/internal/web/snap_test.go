package web

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// readSnap is a reader built to docs/SNAP.md's spec (the format re/scripts/snap.py reads), with the
// checks the client's mount makes (Package_MountSnap 0x00AE2A00: "pans", version 0x0200, the index
// and name table at the end) and those snap.py verifies for every shipped archive (zero header tail,
// data contiguous from 0x20 up to the index, reserved fields zero, flags 0/1, names in index order).
// Like the client's entry reader (0x00AE2DD0) it always Snappy-decodes and compares the size.
func readSnap(b []byte) ([]SnapEntry, error) {
	if len(b) < 0x20 || string(b[:4]) != "pans" {
		return nil, errors.New("no pans header")
	}
	le := binary.LittleEndian
	if v := le.Uint16(b[4:]); v != 0x0200 {
		return nil, fmt.Errorf("version %#x", v)
	}
	if v := le.Uint16(b[6:]); v != 1 {
		return nil, fmt.Errorf("header u16 at 6 is %d", v)
	}
	if !bytes.Equal(b[0x10:0x20], make([]byte, 16)) {
		return nil, errors.New("header tail not zero")
	}
	count, nameSize := int(le.Uint32(b[8:])), int(le.Uint32(b[12:]))
	indexAt := len(b) - nameSize - 32*count
	if indexAt < 0x20 {
		return nil, errors.New("index and name table larger than the file")
	}
	names := b[len(b)-nameSize:]
	var out []SnapEntry
	next, nameNext := 0x20, 0
	for i := range count {
		ix := b[indexAt+32*i : indexAt+32*i+32]
		nameOff, size, stored := int(le.Uint32(ix[0:])), int(le.Uint32(ix[4:])), int(le.Uint32(ix[8:]))
		off, flags := int(le.Uint32(ix[0x18:])), le.Uint32(ix[0x1C:])
		if !bytes.Equal(ix[0xC:0x18], make([]byte, 12)) {
			return nil, fmt.Errorf("entry %d: reserved fields not zero", i)
		}
		if flags != 0 {
			return nil, fmt.Errorf("entry %d: flags %#x (the client's reader always decompresses)", i, flags)
		}
		if off != next || off+stored > indexAt {
			return nil, fmt.Errorf("entry %d: data at %#x+%d, expected at %#x before %#x", i, off, stored, next, indexAt)
		}
		next = off + stored
		if nameOff != nameNext {
			return nil, fmt.Errorf("entry %d: name at %d, expected %d", i, nameOff, nameNext)
		}
		end := bytes.IndexByte(names[nameOff:], 0)
		if end < 0 {
			return nil, fmt.Errorf("entry %d: name not terminated", i)
		}
		nameNext = nameOff + end + 1
		data, err := snappyDecode(b[off : off+stored])
		if err != nil {
			return nil, fmt.Errorf("entry %d: %v", i, err)
		}
		if len(data) != size {
			return nil, fmt.Errorf("entry %d: unpacked %d bytes, index says %d", i, len(data), size)
		}
		out = append(out, SnapEntry{Name: string(names[nameOff : nameOff+end]), Data: data})
	}
	if next != indexAt {
		return nil, fmt.Errorf("data ends at %#x, index at %#x", next, indexAt)
	}
	if nameNext != nameSize {
		return nil, fmt.Errorf("name table has %d bytes, names use %d", nameSize, nameNext)
	}
	return out, nil
}

// snappyDecode decodes one raw Snappy block (literals and all three copy forms).
func snappyDecode(src []byte) ([]byte, error) {
	n, k := binary.Uvarint(src)
	if k <= 0 {
		return nil, errors.New("bad length varint")
	}
	src = src[k:]
	out := make([]byte, 0, n)
	for len(src) > 0 {
		tag := src[0]
		var length, offset int
		switch tag & 3 {
		case 0:
			length = int(tag>>2) + 1
			src = src[1:]
			if extra := int(tag>>2) - 59; extra > 0 {
				if len(src) < extra {
					return nil, errors.New("truncated literal length")
				}
				length = 1
				for i := range extra {
					length += int(src[i]) << (8 * i)
				}
				src = src[extra:]
			}
			if len(src) < length {
				return nil, errors.New("truncated literal")
			}
			out = append(out, src[:length]...)
			src = src[length:]
			continue
		case 1:
			if len(src) < 2 {
				return nil, errors.New("truncated copy")
			}
			length, offset = int(tag>>2&7)+4, int(tag>>5)<<8|int(src[1])
			src = src[2:]
		case 2:
			if len(src) < 3 {
				return nil, errors.New("truncated copy")
			}
			length, offset = int(tag>>2)+1, int(binary.LittleEndian.Uint16(src[1:]))
			src = src[3:]
		case 3:
			if len(src) < 5 {
				return nil, errors.New("truncated copy")
			}
			length, offset = int(tag>>2)+1, int(binary.LittleEndian.Uint32(src[1:]))
			src = src[5:]
		}
		if offset == 0 || offset > len(out) {
			return nil, fmt.Errorf("copy offset %d with %d bytes out", offset, len(out))
		}
		for range length { // byte by byte: copies may overlap
			out = append(out, out[len(out)-offset])
		}
	}
	if uint64(len(out)) != n {
		return nil, fmt.Errorf("decoded %d bytes, header says %d", len(out), n)
	}
	return out, nil
}

func TestSnappyDecoderOnCopies(t *testing.T) {
	// "abcabcabcd": literal "abc", 1-byte-offset copy of 6 from 3 back, literal "d"
	got, err := snappyDecode([]byte{10, 0x08, 'a', 'b', 'c', 0x09, 0x03, 0x00, 'd'})
	if err != nil || string(got) != "abcabcabcd" {
		t.Errorf("decode = %q, %v", got, err)
	}
	if _, err := snappyDecode([]byte{4, 0x09, 0x05}); err == nil {
		t.Error("copy before any output decoded")
	}
}

func TestSnappyLiterals(t *testing.T) {
	for _, n := range []int{0, 1, 59, 60, 61, 255, 256, 257, 65535, 65536, 65537, 200000} {
		in := make([]byte, n)
		for i := range in {
			in[i] = byte(i*7 + i>>8)
		}
		block := snappyLiterals(in)
		out, err := snappyDecode(block)
		if err != nil || !bytes.Equal(out, in) {
			t.Errorf("%d bytes: round trip failed (%v)", n, err)
		}
	}
	// the tag forms: 60 bytes fit the tag, 61 need a one-byte length, 257 a two-byte one
	if b := snappyLiterals(make([]byte, 60)); b[1] != 59<<2 {
		t.Errorf("60 bytes: tag %#x", b[1])
	}
	if b := snappyLiterals(make([]byte, 61)); b[1] != 60<<2 || b[2] != 60 {
		t.Errorf("61 bytes: tag % x", b[1:3])
	}
	if b := snappyLiterals(make([]byte, 257)); b[2] != 61<<2 || b[3] != 0 || b[4] != 1 {
		t.Errorf("257 bytes: tag % x", b[2:5])
	}
}

func TestBuildSnapRoundTrip(t *testing.T) {
	big := []byte(strings.Repeat("dynamic lore ", 10000)) // more than one 64 KiB literal
	in := []SnapEntry{{"dynamic_lore/dynamic_lore.xml", []byte("<DynamicLore/>")}, {"empty.txt", nil}, {"a/b/big.bin", big}}
	b := BuildSnap(in)
	got, err := readSnap(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(in) {
		t.Fatalf("%d entries", len(got))
	}
	for i := range in {
		if got[i].Name != in[i].Name || !bytes.Equal(got[i].Data, in[i].Data) {
			t.Errorf("entry %d: %q (%d bytes)", i, got[i].Name, len(got[i].Data))
		}
	}
	if b := BuildSnap(nil); len(b) != 0x20 || string(b[:4]) != "pans" {
		t.Errorf("empty archive: % x", b) // like Shared.snap: a 32-byte header with count 0
	}
}

// The archive the server sends: exactly the lore XML at the path Config.package names.
func TestDynamicLoreSnap(t *testing.T) {
	got, err := readSnap(DynamicLoreSnap())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "dynamic_lore/dynamic_lore.xml" || !bytes.Equal(got[0].Data, DynamicLoreXML()) {
		t.Errorf("entries %+v", got)
	}
}
