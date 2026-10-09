package web

import "encoding/binary"

// SnapEntry is one file of a .snap archive: its path inside the archive and its unpacked bytes.
type SnapEntry struct {
	Name string
	Data []byte
}

// BuildSnap writes a .snap archive (docs/SNAP.md, R3) the way the client's package reader expects it
// (Package_MountSnap 0x00AE2A00, Package_ReadSnapEntry 0x00AE2DD0; docs/WEB_CONTENT.md):
//
//	0x00  "pans", u16 version 0x0200, u16 1, u32 entry count, u32 name-table size, 16 zero bytes
//	0x20  the payloads, back to back in index order
//	      count x 32-byte index entries: u32 name offset, u32 size, u32 stored size, 3 x u32 zero,
//	      u32 data offset (from the file start), u32 flags (0)
//	      the name table: NUL-terminated paths in index order
//
// The mount reads the header and the tail (index plus name table, found from the end of the file), so
// nothing else needs an offset. Every payload is a Snappy block: the reader always decompresses
// (snappy_uncompress 0x00AE5E50) and checks the result against the index size; it never looks at the
// "stored raw" flag that audio.snap's .ogg entries carry. snappyLiterals makes valid blocks without
// compressing.
func BuildSnap(entries []SnapEntry) []byte {
	var names []byte
	nameOffsets := make([]uint32, len(entries))
	for i, e := range entries {
		nameOffsets[i] = uint32(len(names))
		names = append(append(names, e.Name...), 0)
	}
	out := make([]byte, 0x20)
	copy(out, "pans")
	binary.LittleEndian.PutUint16(out[4:], 0x0200)
	binary.LittleEndian.PutUint16(out[6:], 1)
	binary.LittleEndian.PutUint32(out[8:], uint32(len(entries)))
	binary.LittleEndian.PutUint32(out[12:], uint32(len(names)))
	index := make([]byte, 32*len(entries))
	for i, e := range entries {
		block := snappyLiterals(e.Data)
		ix := index[32*i:]
		binary.LittleEndian.PutUint32(ix[0:], nameOffsets[i])
		binary.LittleEndian.PutUint32(ix[4:], uint32(len(e.Data)))
		binary.LittleEndian.PutUint32(ix[8:], uint32(len(block)))
		binary.LittleEndian.PutUint32(ix[0x18:], uint32(len(out)))
		out = append(out, block...)
	}
	return append(append(out, index...), names...)
}

// snappyLiterals encodes b as one raw Snappy block (Google's format_description.txt) made of literal
// elements only: a varint with the unpacked length, then literals of at most 64 KiB, each a tag byte
// (length-1 in the upper six bits, or 60/61 followed by the length-1 in one or two bytes). Any Snappy
// decoder accepts it; it is just not smaller than the input.
func snappyLiterals(b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(len(b)))
	for len(b) > 0 {
		n := min(len(b), 1<<16)
		switch m := n - 1; {
		case m < 60:
			out = append(out, byte(m<<2))
		case m < 1<<8:
			out = append(out, 60<<2, byte(m))
		default:
			out = append(out, 61<<2, byte(m), byte(m>>8))
		}
		out = append(out, b[:n]...)
		b = b[n:]
	}
	return out
}
