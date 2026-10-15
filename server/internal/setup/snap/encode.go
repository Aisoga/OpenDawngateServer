package snap

import (
	"encoding/binary"
)

// Encode compresses src into one raw Snappy block, byte for byte as snap.py's snappy_compress does: greedy LZ77 per
// 64 KB block with a table of the last position of every 4-byte sequence (no hashing), literals up to 64 KB and
// copies of up to 64 bytes (a 1-byte-offset copy for 4-11 bytes within 2 KB, else a 2-byte-offset copy; a longer
// match leaves at least 4 bytes for its last copy). The game's reader (and Decode) take any valid block; the exact
// bytes matter only so that the Go setup writes what the Python tools write.
func Encode(src []byte) []byte {
	n := len(src)
	out := binary.AppendUvarint(make([]byte, 0, n+n/6+16), uint64(n))
	literal := func(a, b int) {
		for a < b {
			ln := min(b-a, 1<<16)
			k := ln - 1
			switch {
			case k < 60:
				out = append(out, byte(k<<2))
			case k < 256:
				out = append(out, 60<<2, byte(k))
			default:
				out = append(out, 61<<2, byte(k), byte(k>>8))
			}
			out = append(out, src[a:a+ln]...)
			a += ln
		}
	}
	copyTag := func(off, ln int) {
		for ln > 0 {
			k := min(ln, 64)
			if ln-k < 4 && ln > 64 { // leave at least 4 for the last copy
				k = ln - 4
			}
			if k >= 4 && k <= 11 && off < 2048 {
				out = append(out, byte(1|(k-4)<<2|(off>>8)<<5), byte(off))
			} else {
				out = append(out, byte(2|(k-1)<<2), byte(off), byte(off>>8))
			}
			ln -= k
		}
	}
	for block := 0; block < n; block += 1 << 16 {
		end := min(n, block+1<<16)
		table := make(map[uint32]int, 1<<12)
		i, lit := block, block
		for i+4 <= end {
			key := binary.LittleEndian.Uint32(src[i:])
			cand, ok := table[key]
			table[key] = i
			if ok && i-cand < 65536 {
				ln := 4
				for i+ln < end && src[cand+ln] == src[i+ln] {
					ln++
				}
				literal(lit, i)
				copyTag(i-cand, ln)
				i += ln
				lit = i
				continue
			}
			i++
		}
		literal(lit, end)
	}
	return out
}

// EncodeLiterals is a Snappy block of literals only: its size depends on len(src) alone.
func EncodeLiterals(src []byte) []byte {
	out := binary.AppendUvarint(make([]byte, 0, len(src)+len(src)>>16*3+8), uint64(len(src)))
	for a := 0; a < len(src); {
		ln := min(len(src)-a, 1<<16)
		k := ln - 1
		switch {
		case k < 60:
			out = append(out, byte(k<<2))
		case k < 256:
			out = append(out, 60<<2, byte(k))
		default:
			out = append(out, 61<<2, byte(k), byte(k>>8))
		}
		out = append(out, src[a:a+ln]...)
		a += ln
	}
	return out
}

// File is one entry of an archive to write: Snappy-compressed by Encode, or with Literals by EncodeLiterals (the
// archive's layout then depends only on the entries' sizes).
type File struct {
	Name     string
	Data     []byte
	Literals bool
}

// Pack writes an archive (snap.py's pack): every entry Snappy-compressed, in the given order, the layout of the
// package comment.
func Pack(files []File) []byte {
	var data, index, names []byte
	le := binary.LittleEndian
	for _, f := range files {
		payload := Encode(f.Data)
		if f.Literals {
			payload = EncodeLiterals(f.Data)
		}
		index = le.AppendUint32(index, uint32(len(names)))
		index = le.AppendUint32(index, uint32(len(f.Data)))
		index = le.AppendUint32(index, uint32(len(payload)))
		index = append(index, make([]byte, 12)...)
		index = le.AppendUint32(index, uint32(HeaderSize+len(data)))
		index = le.AppendUint32(index, 0)
		names = append(append(names, f.Name...), 0)
		data = append(data, payload...)
	}
	out := make([]byte, 0, HeaderSize+len(data)+len(index)+len(names))
	out = append(out, Magic...)
	out = le.AppendUint16(out, 0x200)
	out = le.AppendUint16(out, 1)
	out = le.AppendUint32(out, uint32(len(files)))
	out = le.AppendUint32(out, uint32(len(names)))
	out = append(out, make([]byte, 16)...)
	out = append(out, data...)
	out = append(out, index...)
	return append(out, names...)
}
