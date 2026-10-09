// Package dbpf reads and writes EA/Maxis DBPF 3.0 ".package" files, the container of the client's
// Config.package, Config_Ship.package, Config_Pack.package and Locale.package.
// It is re/scripts/dbpf.py's port: Read decompresses an entry as
// dbpf.py's Package.read does (RefPack/QFS and zlib), and Rebuild writes a package with some entries
// replaced, byte for byte as dbpf.py's rebuild does: the replaced entries stored uncompressed, every
// other entry's raw bytes, flags and index order kept.
//
// Differences from dbpf.py, on purpose: an entry whose data lies outside the file is an error here
// (dbpf.py cut it short silently), and RefPack input that refers before the start of the output or
// runs past its end is an error (dbpf.py read garbage or raised IndexError). Valid packages read the
// same.
package dbpf

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Compression codes of an index entry (dbpf.py's COMP_*).
const (
	CompNone          = 0x0000
	CompZlib          = 0x5A42
	CompRefPack       = 0xFFFF
	CompRefPackStream = 0xFFFE
	CompDeleted       = 0xFFE0
)

// HeaderSize is the size of the DBPF 3.0 header; Rebuild puts the entry data right after it.
const HeaderSize = 0x60

// Entry is one index entry.
type Entry struct {
	Type, Group uint32
	Instance    uint64
	Offset      uint32 // of the stored data, from the file start
	CSize       uint32 // stored size (the index's high bit masked off)
	USize       uint32 // size once decompressed
	Comp        uint16 // compression (Comp*)
	Committed   uint16
}

// Key names the entry as dbpf.py does: "TTTTTTTT-GGGGGGGG-IIIIIIIIIIIIIIII" (hex, upper case).
func (e Entry) Key() string {
	return fmt.Sprintf("%08X-%08X-%016X", e.Type, e.Group, e.Instance)
}

// Package is a parsed DBPF file. Data is the whole file; Raw and Read return slices of it, which the
// caller must not change.
type Package struct {
	Data         []byte
	Major, Minor uint32
	IndexSize    uint32 // header +0x2C
	IndexVersion uint32 // header +0x3C
	IndexFlags   uint32 // the index's first word: 1 type, 2 group, 4 instance high word are constants
	Entries      []Entry
}

// Open reads and parses a package file.
func Open(path string) (*Package, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Parse parses a package held in memory (dbpf.py's Package).
func Parse(data []byte) (*Package, error) {
	if len(data) < 4 || string(data[:4]) != "DBPF" {
		return nil, errors.New("dbpf: not a DBPF file")
	}
	if len(data) < 0x48 {
		return nil, errors.New("dbpf: header cut short")
	}
	le := binary.LittleEndian
	p := &Package{
		Data:         data,
		Major:        le.Uint32(data[4:]),
		Minor:        le.Uint32(data[8:]),
		IndexSize:    le.Uint32(data[0x2C:]),
		IndexVersion: le.Uint32(data[0x3C:]),
	}
	count := le.Uint32(data[0x24:])
	pos := le.Uint64(data[0x40:])
	u32 := func() (uint32, error) {
		if pos > uint64(len(data)) || uint64(len(data))-pos < 4 {
			return 0, fmt.Errorf("dbpf: index runs past the end of the file (at %#x)", pos)
		}
		v := le.Uint32(data[pos:])
		pos += 4
		return v, nil
	}
	flags, err := u32()
	if err != nil {
		return nil, err
	}
	p.IndexFlags = flags
	var consts [3]uint32 // type, group, instance high word
	for i, bit := range []uint32{1, 2, 4} {
		if flags&bit != 0 {
			if consts[i], err = u32(); err != nil {
				return nil, err
			}
		}
	}
	perEntry := uint64(20)
	for _, bit := range []uint32{1, 2, 4} {
		if flags&bit == 0 {
			perEntry += 4
		}
	}
	if uint64(count)*perEntry > uint64(len(data)) {
		return nil, fmt.Errorf("dbpf: %d index entries don't fit in the file", count)
	}
	p.Entries = make([]Entry, 0, count)
	for i := uint32(0); i < count; i++ {
		var vals [3]uint32
		for k, bit := range []uint32{1, 2, 4} {
			if flags&bit != 0 {
				vals[k] = consts[k]
			} else if vals[k], err = u32(); err != nil {
				return nil, err
			}
		}
		var f [4]uint32
		for k := range f {
			if f[k], err = u32(); err != nil {
				return nil, err
			}
		}
		last, err := u32()
		if err != nil {
			return nil, err
		}
		p.Entries = append(p.Entries, Entry{
			Type: vals[0], Group: vals[1], Instance: uint64(vals[2])<<32 | uint64(f[0]),
			Offset: f[1], CSize: f[2] & 0x7FFFFFFF, USize: f[3],
			Comp: uint16(last), Committed: uint16(last >> 16),
		})
	}
	return p, nil
}

// Raw returns an entry's stored bytes.
func (p *Package) Raw(e Entry) ([]byte, error) {
	end := uint64(e.Offset) + uint64(e.CSize)
	if end > uint64(len(p.Data)) {
		return nil, fmt.Errorf("dbpf: %s: data at %#x+%#x lies outside the file (%#x bytes)", e.Key(), e.Offset, e.CSize, len(p.Data))
	}
	return p.Data[e.Offset:end:end], nil
}

// Read returns an entry's contents, decompressed (dbpf.py's Package.read, including its rule that an
// entry whose stored and unpacked sizes are equal is stored raw unless it is zlib).
func (p *Package) Read(e Entry) ([]byte, error) {
	raw, err := p.Raw(e)
	if err != nil {
		return nil, err
	}
	if e.Comp == CompNone || (e.CSize == e.USize && e.Comp != CompZlib) {
		return raw, nil
	}
	switch e.Comp {
	case CompRefPack, CompRefPackStream:
		out, err := RefPackDecompress(raw)
		if err != nil {
			return nil, fmt.Errorf("dbpf: %s: %w", e.Key(), err)
		}
		return out, nil
	case CompZlib:
		r, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("dbpf: %s: %w", e.Key(), err)
		}
		defer r.Close()
		out, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("dbpf: %s: %w", e.Key(), err)
		}
		return out, nil
	case CompDeleted:
		return []byte{}, nil
	}
	return nil, fmt.Errorf("dbpf: %s: unknown compression %#x", e.Key(), e.Comp)
}

// RefPackDecompress decompresses EA RefPack (QFS) data (dbpf.py's refpack_decompress).
func RefPackDecompress(src []byte) ([]byte, error) {
	n := len(src)
	if n < 2 {
		return nil, errors.New("not RefPack (too short)")
	}
	flags, magic := src[0], src[1]
	if magic != 0xFB {
		return nil, fmt.Errorf("not RefPack (magic %#x)", magic)
	}
	width := 3
	if flags&0x80 != 0 {
		width = 4
	}
	pos := 2
	if flags&0x01 != 0 {
		pos += width // the compressed size, unused
	}
	if pos+width > n {
		return nil, errors.New("RefPack header cut short")
	}
	usize := 0
	for _, b := range src[pos : pos+width] {
		usize = usize<<8 | int(b)
	}
	pos += width
	out := make([]byte, 0, usize)
	need := func(k int) error {
		if pos+k > n {
			return fmt.Errorf("RefPack data cut short at %#x", pos)
		}
		return nil
	}
	for pos < n {
		b0 := int(src[pos])
		var plain, copyLen, off int
		switch {
		case b0 <= 0x7F:
			if err := need(2); err != nil {
				return nil, err
			}
			b1 := int(src[pos+1])
			pos += 2
			plain, copyLen, off = b0&3, (b0&0x1C)>>2+3, (b0&0x60)<<3+b1+1
		case b0 <= 0xBF:
			if err := need(3); err != nil {
				return nil, err
			}
			b1, b2 := int(src[pos+1]), int(src[pos+2])
			pos += 3
			plain, copyLen, off = b1>>6&3, b0&0x3F+4, (b1&0x3F)<<8+b2+1
		case b0 <= 0xDF:
			if err := need(4); err != nil {
				return nil, err
			}
			b1, b2, b3 := int(src[pos+1]), int(src[pos+2]), int(src[pos+3])
			pos += 4
			plain, copyLen, off = b0&3, (b0&0x0C)<<6+b3+5, (b0&0x10)<<12+b1<<8+b2+1
		case b0 <= 0xFB:
			pos++
			plain = (b0&0x1F)<<2 + 4
		default: // the stop code: up to 3 last literal bytes
			pos++
			plain = b0 & 3
			if err := need(plain); err != nil {
				return nil, err
			}
			out = append(out, src[pos:pos+plain]...)
			pos = n
			continue
		}
		if err := need(plain); err != nil {
			return nil, err
		}
		out = append(out, src[pos:pos+plain]...)
		pos += plain
		start := len(out) - off
		if start < 0 {
			return nil, fmt.Errorf("RefPack copy from %d bytes back with %d bytes of output", off, len(out))
		}
		if len(out)+copyLen > usize {
			return nil, fmt.Errorf("RefPack output exceeds its size %d", usize)
		}
		for i := 0; i < copyLen; i++ { // byte by byte: copies may overlap
			out = append(out, out[start+i])
		}
		if len(out) > usize {
			return nil, fmt.Errorf("RefPack output exceeds its size %d", usize)
		}
	}
	if len(out) != usize {
		return nil, fmt.Errorf("RefPack size mismatch %d != %d", len(out), usize)
	}
	return out, nil
}

// Rebuild returns the package's bytes with the entries in replacements (by Key) replaced: dbpf.py's
// rebuild, byte for byte. Replaced entries are stored uncompressed (CompNone); the others keep their
// raw bytes, compression and committed flag. The layout is the originals': the first 0x60 header bytes
// with the entry count (+0x24), index size (+0x2C) and index offset (+0x40) updated, the entry data in
// the order of their old offsets, then the index in its old order. Every entry whose key is in
// replacements is replaced; a key no entry has is an error.
func (p *Package) Rebuild(replacements map[string][]byte) ([]byte, error) {
	have := make(map[string]bool, len(p.Entries))
	for _, e := range p.Entries {
		have[e.Key()] = true
	}
	var missing []string
	for k := range replacements {
		if !have[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("dbpf: not in the package: %s", strings.Join(missing, ", "))
	}
	if len(p.Data) < HeaderSize {
		return nil, errors.New("dbpf: header cut short")
	}
	type placed struct {
		off, size, usize uint32
		comp             uint16
	}
	order := make([]int, len(p.Entries))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return p.Entries[order[a]].Offset < p.Entries[order[b]].Offset })
	var body bytes.Buffer
	where := make([]placed, len(p.Entries))
	for _, i := range order {
		e := p.Entries[i]
		at := uint64(HeaderSize + body.Len())
		var raw []byte
		pl := placed{usize: e.USize, comp: e.Comp}
		if repl, ok := replacements[e.Key()]; ok {
			raw = repl
			pl.usize, pl.comp = uint32(len(repl)), CompNone
			if uint64(len(repl)) > 0xFFFFFFFF {
				return nil, fmt.Errorf("dbpf: %s: entry too large", e.Key())
			}
		} else {
			var err error
			if raw, err = p.Raw(e); err != nil {
				return nil, err
			}
		}
		if at > 0xFFFFFFFF {
			return nil, errors.New("dbpf: package larger than 4 GB")
		}
		pl.off, pl.size = uint32(at), uint32(len(raw))
		where[i] = pl
		body.Write(raw)
	}

	flags := uint32(0)
	var consts [3]uint32
	if len(p.Entries) > 0 {
		flags = p.IndexFlags
		first := p.Entries[0]
		consts = [3]uint32{first.Type, first.Group, uint32(first.Instance >> 32)}
	}
	le := binary.LittleEndian
	var index []byte
	index = le.AppendUint32(index, flags)
	for k, bit := range []uint32{1, 2, 4} {
		if flags&bit != 0 {
			index = le.AppendUint32(index, consts[k])
		}
	}
	for i, e := range p.Entries {
		pl := where[i]
		for k, bit := range []uint32{1, 2, 4} {
			val := [3]uint32{e.Type, e.Group, uint32(e.Instance >> 32)}[k]
			if flags&bit != 0 {
				if val != consts[k] {
					return nil, fmt.Errorf("dbpf: %s: %s differs from the index constant", e.Key(), [3]string{"type", "group", "insthi"}[k])
				}
			} else {
				index = le.AppendUint32(index, val)
			}
		}
		index = le.AppendUint32(index, uint32(e.Instance))
		index = le.AppendUint32(index, pl.off)
		index = le.AppendUint32(index, pl.size|0x80000000)
		index = le.AppendUint32(index, pl.usize)
		index = le.AppendUint16(index, pl.comp)
		index = le.AppendUint16(index, e.Committed)
	}

	out := make([]byte, 0, HeaderSize+body.Len()+len(index))
	out = append(out, p.Data[:HeaderSize]...)
	le.PutUint32(out[0x24:], uint32(len(p.Entries)))
	le.PutUint32(out[0x2C:], uint32(len(index)))
	le.PutUint64(out[0x40:], uint64(HeaderSize+body.Len()))
	out = append(out, body.Bytes()...)
	return append(out, index...), nil
}
