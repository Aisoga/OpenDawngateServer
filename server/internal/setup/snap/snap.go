// Package snap reads Dawngate's ".snap" archives (magic "pans", 91 files in game/Data),
// re/scripts/snap.py's port. It reads an archive's index and names and
// decompresses its entries in memory: raw Snappy blocks, decoded by Decode (stdlib only, written here).
//
// Layout (little-endian): a 0x20-byte header ("pans", u16 version, u16 1, u32 entry count, u32 name-table
// size, 16 zero bytes), the payloads, the index (count × 32 bytes: u32 name offset, u32 size, u32 stored
// size, 3 × u32 0, u32 data offset, u32 flags, bit 0 = stored raw) directly before the name table
// (NUL-terminated paths), which ends the file.
//
// Differences from snap.py, on purpose: a Snappy tag cut short by the end of the block and output beyond
// the block's own size are errors at once (snap.py failed on them later or read past the end). Names are
// decoded from UTF-8 as snap.py does (pytext.DecodeUTF8: invalid bytes become U+FFFD). Valid archives
// read the same.
package snap

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"dawngate/server/internal/setup/pytext"
)

// Header and index sizes, and the flag of an entry stored raw.
const (
	Magic      = "pans"
	HeaderSize = 0x20
	EntrySize  = 32
	FlagStored = 0x1
)

// Entry is one index entry.
type Entry struct {
	Index      int
	Name       string // the entry's path, e.g. "AssetData/Binary/alpha.CollectionItem.bin"
	NameOffset uint32
	Size       uint32 // unpacked
	StoredSize uint32
	Reserved   [3]uint32
	Offset     uint32 // of the payload, from the file start
	Flags      uint32
}

// Stored reports whether the payload is stored raw (flag 1) rather than as a Snappy block.
func (e Entry) Stored() bool { return e.Flags&FlagStored != 0 }

// Archive is a parsed .snap file. Data is the whole file.
type Archive struct {
	Data        []byte
	Version     uint16 // 0x0200 in every archive
	Unknown     uint16 // 1 in every archive
	HeaderTail  []byte // header bytes 0x10-0x1F, zero in every archive
	IndexOffset int
	NamesOffset int
	Entries     []Entry
}

// Open reads and parses an archive file.
func Open(path string) (*Archive, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	a, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

// Parse parses an archive held in memory (snap.py's Snap).
func Parse(d []byte) (*Archive, error) {
	if len(d) < HeaderSize || string(d[:4]) != Magic {
		return nil, errors.New("snap: not a .snap archive")
	}
	le := binary.LittleEndian
	a := &Archive{Data: d, Version: le.Uint16(d[4:]), Unknown: le.Uint16(d[6:]), HeaderTail: d[0x10:HeaderSize:HeaderSize]}
	count, namesSize := int64(le.Uint32(d[8:])), int64(le.Uint32(d[12:]))
	namesOff := int64(len(d)) - namesSize
	indexOff := namesOff - count*EntrySize
	if indexOff < HeaderSize {
		return nil, fmt.Errorf("snap: index (%d entries) and names (%d B) exceed the file", count, namesSize)
	}
	a.IndexOffset, a.NamesOffset = int(indexOff), int(namesOff)
	names := d[namesOff:]
	a.Entries = make([]Entry, count)
	for i := range a.Entries {
		at := a.IndexOffset + i*EntrySize
		e := Entry{
			Index:      i,
			NameOffset: le.Uint32(d[at:]),
			Size:       le.Uint32(d[at+4:]),
			StoredSize: le.Uint32(d[at+8:]),
			Reserved:   [3]uint32{le.Uint32(d[at+12:]), le.Uint32(d[at+16:]), le.Uint32(d[at+20:])},
			Offset:     le.Uint32(d[at+24:]),
			Flags:      le.Uint32(d[at+28:]),
		}
		if int64(e.NameOffset) >= int64(len(names)) {
			return nil, fmt.Errorf("snap: entry %d name offset %#x outside the name table", i, e.NameOffset)
		}
		end := bytes.IndexByte(names[e.NameOffset:], 0)
		if end < 0 {
			return nil, fmt.Errorf("snap: entry %d name offset %#x outside the name table", i, e.NameOffset)
		}
		e.Name = pytext.DecodeUTF8(names[e.NameOffset : int(e.NameOffset)+end])
		if e.Offset < HeaderSize || int64(e.Offset)+int64(e.StoredSize) > indexOff {
			return nil, fmt.Errorf("snap: entry %d (%s) data outside the data area", i, e.Name)
		}
		a.Entries[i] = e
	}
	return a, nil
}

// Raw returns an entry's stored bytes (a slice of Data, not to be changed).
func (a *Archive) Raw(e Entry) []byte {
	end := int(e.Offset) + int(e.StoredSize)
	return a.Data[e.Offset:end:end]
}

// Read returns an entry's contents: the stored bytes of a raw entry, else its Snappy block decoded.
func (a *Archive) Read(e Entry) ([]byte, error) {
	raw := a.Raw(e)
	if e.Stored() {
		if e.StoredSize != e.Size {
			return nil, fmt.Errorf("snap: %s: stored entry with size %d != stored size %d", e.Name, e.Size, e.StoredSize)
		}
		return raw, nil
	}
	out, err := Decode(raw, int64(e.Size))
	if err != nil {
		return nil, fmt.Errorf("snap: %s: %w", e.Name, err)
	}
	return out, nil
}

// LayoutNotes lists what deviates from the layout described in the package comment (snap.py's
// layout_notes); empty for every archive of builds 754 and 854.
func (a *Archive) LayoutNotes() []string {
	var notes []string
	if a.Version != 0x200 || a.Unknown != 1 {
		notes = append(notes, fmt.Sprintf("header version %#x/%d", a.Version, a.Unknown))
	}
	for _, b := range a.HeaderTail {
		if b != 0 {
			notes = append(notes, fmt.Sprintf("header bytes 0x10-0x1F not zero: %x", a.HeaderTail))
			break
		}
	}
	pos := uint32(HeaderSize)
	contiguous := true
	for _, e := range a.Entries {
		if e.Offset != pos {
			notes = append(notes, fmt.Sprintf("entry %d (%s) at %#x, expected %#x", e.Index, e.Name, e.Offset, pos))
			contiguous = false
			break
		}
		pos = e.Offset + e.StoredSize
	}
	if contiguous && len(a.Entries) > 0 && int(pos) != a.IndexOffset {
		notes = append(notes, fmt.Sprintf("%d bytes between data and index", a.IndexOffset-int(pos)))
	}
	for _, e := range a.Entries {
		if e.Reserved != [3]uint32{} {
			notes = append(notes, "non-zero reserved index fields")
			break
		}
	}
	flagSet := map[uint32]bool{}
	unknown := false
	for _, e := range a.Entries {
		flagSet[e.Flags] = true
		unknown = unknown || e.Flags&^FlagStored != 0
	}
	if unknown {
		var fl []string
		for f := range flagSet {
			fl = append(fl, fmt.Sprint(f))
		}
		sort.Slice(fl, func(i, j int) bool { return len(fl[i]) < len(fl[j]) || len(fl[i]) == len(fl[j]) && fl[i] < fl[j] })
		notes = append(notes, fmt.Sprintf("unknown flags [%s]", strings.Join(fl, ", ")))
	}
	expected := uint32(0)
	inOrder := true
	for _, e := range a.Entries {
		if e.NameOffset != expected {
			notes = append(notes, fmt.Sprintf("name table not in index order at entry %d", e.Index))
			inOrder = false
			break
		}
		expected += uint32(len(e.Name)) + 1
	}
	if inOrder && len(a.Entries) > 0 && int(expected) != len(a.Data)-a.NamesOffset {
		notes = append(notes, "name table has unused bytes")
	}
	count := map[string]int{}
	var order []string
	for _, e := range a.Entries {
		k := strings.ToLower(e.Name)
		if count[k] == 0 {
			order = append(order, k)
		}
		count[k]++
	}
	var dups []string
	for _, k := range order {
		if count[k] > 1 {
			dups = append(dups, k)
		}
	}
	if len(dups) > 0 {
		notes = append(notes, fmt.Sprintf("%d duplicate names (case-insensitive), e.g. %s", len(dups), dups[0]))
	}
	return notes
}

// ErrSnappy is wrapped by every error Decode returns for malformed input.
var ErrSnappy = errors.New("bad Snappy block")

// Decode decompresses one raw Snappy block (no framing; google/snappy format_description.txt), as
// snap.py's snappy_decompress does. expected is the size the index gives (-1: don't check it); the
// block's own size varint must equal it.
func Decode(src []byte, expected int64) ([]byte, error) {
	return decode(src, expected, -1)
}

// DecodePrefix decompresses the start of a raw Snappy block: at least its first want bytes (fewer only when the
// block is shorter), without reading on once they are out; for a file's header without its body. src may be
// the block's first bytes only.
func DecodePrefix(src []byte, want int) ([]byte, error) {
	return decode(src, -1, int64(want))
}

// decode is Decode, stopping once limit bytes are out (limit < 0: the whole block).
func decode(src []byte, expected, limit int64) ([]byte, error) {
	n := len(src)
	pos := 0
	var usize uint64
	for shift := uint(0); ; shift += 7 {
		if pos >= n || shift > 28 {
			return nil, fmt.Errorf("%w: bad length varint", ErrSnappy)
		}
		b := src[pos]
		pos++
		usize |= uint64(b&0x7F) << shift
		if b < 0x80 {
			break
		}
	}
	if expected >= 0 && usize != uint64(expected) {
		return nil, fmt.Errorf("%w: length %d != index size %d", ErrSnappy, usize, expected)
	}
	if usize > 1<<32 {
		return nil, fmt.Errorf("%w: length %d too large", ErrSnappy, usize)
	}
	capacity := usize
	if limit >= 0 && uint64(limit)+64 < capacity {
		capacity = uint64(limit) + 64
	}
	out := make([]byte, 0, capacity)
	for pos < n {
		if limit >= 0 && int64(len(out)) >= limit {
			return out, nil
		}
		tag := src[pos]
		pos++
		var ln, off uint64 // 64-bit, so a 32-bit build can't overflow on hostile input
		switch tag & 3 {
		case 0: // literal
			ln = uint64(tag >> 2)
			if ln >= 60 {
				nb := int(ln - 59)
				if nb > n-pos {
					return nil, fmt.Errorf("%w: literal length runs past the end", ErrSnappy)
				}
				ln = 0
				for k := nb - 1; k >= 0; k-- {
					ln = ln<<8 | uint64(src[pos+k])
				}
				pos += nb
			}
			ln++
			if limit >= 0 && ln > uint64(n-pos) { // a prefix of the block: the literal's bytes that are there
				return append(out, src[pos:]...), nil
			}
			if ln > uint64(n-pos) {
				return nil, fmt.Errorf("%w: literal runs past the end", ErrSnappy)
			}
			if uint64(len(out))+ln > usize {
				return nil, fmt.Errorf("%w: output exceeds %d bytes", ErrSnappy, usize)
			}
			out = append(out, src[pos:pos+int(ln)]...)
			pos += int(ln)
			continue
		case 1: // copy, 1-byte offset
			if pos+1 > n {
				return nil, fmt.Errorf("%w: copy tag cut short", ErrSnappy)
			}
			ln = uint64(tag>>2&7) + 4
			off = uint64(tag>>5)<<8 | uint64(src[pos])
			pos++
		case 2: // copy, 2-byte offset
			if pos+2 > n {
				return nil, fmt.Errorf("%w: copy tag cut short", ErrSnappy)
			}
			ln = uint64(tag>>2) + 1
			off = uint64(binary.LittleEndian.Uint16(src[pos:]))
			pos += 2
		default: // copy, 4-byte offset
			if pos+4 > n {
				return nil, fmt.Errorf("%w: copy tag cut short", ErrSnappy)
			}
			ln = uint64(tag>>2) + 1
			off = uint64(binary.LittleEndian.Uint32(src[pos:]))
			pos += 4
		}
		have := uint64(len(out))
		if off == 0 || off > have {
			return nil, fmt.Errorf("%w: copy offset %d outside %d bytes of output", ErrSnappy, off, have)
		}
		if have+ln > usize {
			return nil, fmt.Errorf("%w: output exceeds %d bytes", ErrSnappy, usize)
		}
		start := int(have - off)
		for i := 0; i < int(ln); i++ { // an overlapping copy repeats the last off bytes
			out = append(out, out[start+i])
		}
	}
	if uint64(len(out)) != usize {
		return nil, fmt.Errorf("%w: decompressed %d bytes, header says %d", ErrSnappy, len(out), usize)
	}
	return out, nil
}
