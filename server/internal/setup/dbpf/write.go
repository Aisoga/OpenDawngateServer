package dbpf

import (
	"encoding/binary"
	"fmt"
)

// NewEntry is one entry for WritePackage.
type NewEntry struct {
	Type, Group uint32
	Instance    uint64
	Data        []byte
}

// WritePackage returns a new DBPF 3.0 package holding entries in that order, stored uncompressed (comp 0,
// committed 1): dbpf.py's write_package (ART3c-B; ported for ART3e), laid out as the shipped packages are
// (Locale/en-us/Locale.package): a 0x60-byte header with only the magic, version 3.0, the entry count (+0x24),
// the index size (+0x2C), the index version 3 (+0x3C) and the index offset (+0x40) set, then the entry data, then
// the index; type, group and the instance's high half go into the index header (flags 1, 2, 4) when every entry
// shares them.
func WritePackage(entries []NewEntry) []byte {
	le := binary.LittleEndian
	type placed struct {
		e   NewEntry
		off uint32
	}
	var body []byte
	ps := make([]placed, len(entries))
	for i, e := range entries {
		ps[i] = placed{e, uint32(HeaderSize + len(body))}
		body = append(body, e.Data...)
	}
	field := func(e NewEntry, bit uint32) uint32 {
		switch bit {
		case 1:
			return e.Type
		case 2:
			return e.Group
		}
		return uint32(e.Instance >> 32)
	}
	var flags uint32
	if len(ps) > 0 {
		for _, bit := range []uint32{1, 2, 4} {
			same := true
			for _, p := range ps[1:] {
				same = same && field(p.e, bit) == field(ps[0].e, bit)
			}
			if same {
				flags |= bit
			}
		}
	}
	index := le.AppendUint32(nil, flags)
	for _, bit := range []uint32{1, 2, 4} {
		if flags&bit != 0 {
			index = le.AppendUint32(index, field(ps[0].e, bit))
		}
	}
	for _, p := range ps {
		for _, bit := range []uint32{1, 2, 4} {
			if flags&bit == 0 {
				index = le.AppendUint32(index, field(p.e, bit))
			}
		}
		size := uint32(len(p.e.Data))
		index = le.AppendUint32(index, uint32(p.e.Instance))
		index = le.AppendUint32(index, p.off)
		index = le.AppendUint32(index, size|0x80000000)
		index = le.AppendUint32(index, size)
		index = le.AppendUint16(index, CompNone)
		index = le.AppendUint16(index, 1)
	}
	header := make([]byte, HeaderSize)
	copy(header, "DBPF")
	le.PutUint32(header[4:], 3)
	le.PutUint32(header[0x24:], uint32(len(ps)))
	le.PutUint32(header[0x2C:], uint32(len(index)))
	le.PutUint32(header[0x3C:], 3)
	le.PutUint64(header[0x40:], uint64(HeaderSize+len(body)))
	out := append(header, body...)
	return append(out, index...)
}

// CheckRoundTrip parses a package made by WritePackage and checks it holds exactly entries.
func CheckRoundTrip(data []byte, entries []NewEntry) error {
	p, err := Parse(data)
	if err != nil {
		return err
	}
	if len(p.Entries) != len(entries) {
		return fmt.Errorf("dbpf: %d entries read back, %d written", len(p.Entries), len(entries))
	}
	for i, e := range p.Entries {
		b, err := p.Read(e)
		if err != nil {
			return err
		}
		w := entries[i]
		if e.Type != w.Type || e.Group != w.Group || e.Instance != w.Instance || string(b) != string(w.Data) {
			return fmt.Errorf("dbpf: entry %d does not read back", i)
		}
	}
	return nil
}
