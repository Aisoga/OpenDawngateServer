package testgame

import (
	"encoding/binary"
	"strings"
)

// Writers of synthetic archives for the tests that need no game file (made-up names and bytes only).

// SnapFile is one entry of a synthetic .snap archive.
type SnapFile struct {
	Name   string
	Data   []byte
	Stored bool // flag 1: the payload as is; else a Snappy block (SnappyLiterals)
}

// Snap builds a .snap archive in the game's layout: a 0x20-byte header, the payloads in
// index order, the index, the name table.
func Snap(files []SnapFile) []byte {
	le := binary.LittleEndian
	var names strings.Builder
	out := make([]byte, 0x20)
	copy(out, "pans")
	le.PutUint16(out[4:], 0x200)
	le.PutUint16(out[6:], 1)
	type at struct{ off, stored, size, nameOff, flags uint32 }
	var idx []at
	for _, f := range files {
		payload := f.Data
		flags := uint32(0)
		if f.Stored {
			flags = 1
		} else {
			payload = SnappyLiterals(f.Data)
		}
		idx = append(idx, at{uint32(len(out)), uint32(len(payload)), uint32(len(f.Data)), uint32(names.Len()), flags})
		out = append(out, payload...)
		names.WriteString(f.Name)
		names.WriteByte(0)
	}
	for _, e := range idx {
		for _, v := range []uint32{e.nameOff, e.size, e.stored, 0, 0, 0, e.off, e.flags} {
			out = le.AppendUint32(out, v)
		}
	}
	le.PutUint32(out[8:], uint32(len(files)))
	le.PutUint32(out[12:], uint32(names.Len()))
	return append(out, names.String()...)
}

// SnappyLiterals encodes b as a valid raw Snappy block of literal tags only (no compression).
func SnappyLiterals(b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(len(b)))
	for len(b) > 0 {
		n := min(len(b), 1<<16)
		switch {
		case n <= 60:
			out = append(out, byte(n-1)<<2)
		case n <= 256:
			out = append(out, 60<<2, byte(n-1))
		default:
			out = append(out, 61<<2, byte(n-1), byte((n-1)>>8))
		}
		out = append(out, b[:n]...)
		b = b[n:]
	}
	return out
}

// RefPackLiterals encodes b (under 16 MB) as a valid RefPack (QFS) stream of literal blocks only: header
// 10 FB + the size in 3 bytes, blocks E0-FB of 4 to 112 bytes, a stop code FC-FF with the last 0-3 bytes.
func RefPackLiterals(b []byte) []byte {
	out := []byte{0x10, 0xFB, byte(len(b) >> 16), byte(len(b) >> 8), byte(len(b))}
	for len(b) > 3 {
		n := min(len(b)&^3, 112)
		out = append(out, byte(0xE0|(n-4)>>2))
		out = append(out, b[:n]...)
		b = b[n:]
	}
	out = append(out, byte(0xFC|len(b)))
	return append(out, b...)
}

// PackageEntry is one entry of a synthetic DBPF package; Data is stored as given, with Comp and USize
// (USize 0: len(Data)).
type PackageEntry struct {
	Type, Group uint32
	Instance    uint64
	Data        []byte
	Comp        uint16
	USize       uint32
	Committed   uint16
}

// Package builds a DBPF 3.0 package in the game's layout: a 0x60-byte header ("DBPF", 3.0, index version 3
// at +0x3C), the entry data, the index at the end. flags & 1/2/4 store the first entry's type, group and
// instance high word once as index constants.
func Package(flags uint32, entries []PackageEntry) []byte {
	le := binary.LittleEndian
	out := make([]byte, 0x60)
	copy(out, "DBPF")
	le.PutUint32(out[4:], 3)
	le.PutUint32(out[0x3C:], 3)
	offs := make([]uint32, len(entries))
	for i, e := range entries {
		offs[i] = uint32(len(out))
		out = append(out, e.Data...)
	}
	index := le.AppendUint32(nil, flags)
	if len(entries) > 0 {
		first := entries[0]
		for k, bit := range []uint32{1, 2, 4} {
			if flags&bit != 0 {
				index = le.AppendUint32(index, [3]uint32{first.Type, first.Group, uint32(first.Instance >> 32)}[k])
			}
		}
	}
	for i, e := range entries {
		for k, bit := range []uint32{1, 2, 4} {
			if flags&bit == 0 {
				index = le.AppendUint32(index, [3]uint32{e.Type, e.Group, uint32(e.Instance >> 32)}[k])
			}
		}
		usize := e.USize
		if usize == 0 {
			usize = uint32(len(e.Data))
		}
		index = le.AppendUint32(index, uint32(e.Instance))
		index = le.AppendUint32(index, offs[i])
		index = le.AppendUint32(index, uint32(len(e.Data))|0x80000000)
		index = le.AppendUint32(index, usize)
		index = le.AppendUint16(index, e.Comp)
		index = le.AppendUint16(index, e.Committed)
	}
	le.PutUint32(out[0x24:], uint32(len(entries)))
	le.PutUint32(out[0x2C:], uint32(len(index)))
	le.PutUint64(out[0x40:], uint64(len(out)))
	return append(out, index...)
}
