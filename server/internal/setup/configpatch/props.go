package configpatch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"

	"dawngate/server/internal/setup/pytext"
)

// Spore/Darkspore binary property lists (DBPF type 00B1B104, e.g. Config_Ship.package), re/scripts/props.py's
// port. Big-endian: u32 count, then per property u32 id (FNV-1 of the lower-case name), u16 type, u16
// flags, the value. flags & 0x30: an array (u32 count, u32 item size, the items). Strings (0x12 string8,
// 0x13 string16 UTF-16BE) are u32 character count + characters, also inside arrays.

const (
	typeString8  = 0x12
	typeString16 = 0x13
	typeInt32    = 0x09
	typeUint32   = 0x0A
)

// fixedSizes are the value sizes of the other types (props.py's FIXED, checked by parsing every 00B1B104
// entry of Config_Ship.package and Config_Pack.package to the last byte).
var fixedSizes = map[uint16]uint64{0x01: 1, 0x02: 1, 0x03: 1, 0x05: 2, 0x06: 2, 0x09: 4, 0x0A: 4, 0x0B: 8, 0x0C: 8,
	0x0D: 4, 0x20: 12, 0x30: 8, 0x31: 16, 0x32: 16, 0x33: 16, 0x34: 16, 0x39: 24}

// prop is one property; raw is the value after the 8-byte head.
type prop struct {
	id    uint32
	typ   uint16
	flags uint16
	raw   []byte
}

func (p *prop) isArray() bool  { return p.flags&0x30 != 0 }
func (p *prop) isString() bool { return p.typ == typeString8 || p.typ == typeString16 }

// propID is a property's id: FNV-1 of the lower-case name (props.fnv; the names here are ASCII).
func propID(name string) uint32 {
	b := []byte(name)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return pytext.FNV1(b)
}

// parseProps parses a binary property list; it must consume every byte (props.parse).
func parseProps(d []byte) ([]*prop, error) {
	be := binary.BigEndian
	n := uint64(len(d))
	if n < 4 {
		return nil, errors.New("property list cut short")
	}
	count := be.Uint32(d)
	pos := uint64(4)
	strSize := func(typ uint16) (uint64, error) {
		if pos+4 > n {
			return 0, fmt.Errorf("property list cut short at %d", pos)
		}
		k := uint64(be.Uint32(d[pos:]))
		if typ == typeString16 {
			k *= 2
		}
		return 4 + k, nil
	}
	var out []*prop
	for i := uint32(0); i < count; i++ {
		if pos+8 > n {
			return nil, fmt.Errorf("property %d: cut short", i)
		}
		p := &prop{id: be.Uint32(d[pos:]), typ: be.Uint16(d[pos+4:]), flags: be.Uint16(d[pos+6:])}
		pos += 8
		start := pos
		switch {
		case p.isArray():
			if pos+8 > n {
				return nil, fmt.Errorf("property %d (%#010x): cut short", i, p.id)
			}
			items, size := uint64(be.Uint32(d[pos:])), uint64(be.Uint32(d[pos+4:]))
			pos += 8
			if p.isString() {
				for k := uint64(0); k < items && pos <= n; k++ {
					s, err := strSize(p.typ)
					if err != nil {
						return nil, err
					}
					pos += s
				}
			} else {
				pos += items * size
			}
		case p.isString():
			s, err := strSize(p.typ)
			if err != nil {
				return nil, err
			}
			pos += s
		default:
			size, ok := fixedSizes[p.typ]
			if !ok {
				return nil, fmt.Errorf("property %d (%#010x): unknown type %#x flags %#x", i, p.id, p.typ, p.flags)
			}
			pos += size
		}
		if pos > n {
			return nil, fmt.Errorf("property %d (%#010x): value runs past the end", i, p.id)
		}
		p.raw = d[start:pos:pos]
		out = append(out, p)
	}
	if pos != n {
		return nil, fmt.Errorf("parsed %d of %d bytes", pos, n)
	}
	return out, nil
}

// serializeProps writes a property list (props.serialize).
func serializeProps(ps []*prop) []byte {
	be := binary.BigEndian
	out := be.AppendUint32(nil, uint32(len(ps)))
	for _, p := range ps {
		out = be.AppendUint32(out, p.id)
		out = be.AppendUint16(out, p.typ)
		out = be.AppendUint16(out, p.flags)
		out = append(out, p.raw...)
	}
	return out
}

// decodeChars decodes a string body: Latin-1 (string8) or strict UTF-16BE (string16; Python's
// decode("utf-16-be") raises on a lone surrogate).
func decodeChars(typ uint16, body []byte) (string, error) {
	if typ != typeString16 {
		return pytext.Latin1Decode(body), nil
	}
	if len(body)%2 != 0 {
		return "", errors.New("UTF-16 string with an odd number of bytes")
	}
	units := make([]uint16, len(body)/2)
	for i := range units {
		units[i] = binary.BigEndian.Uint16(body[2*i:])
	}
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u >= 0xD800 && u < 0xDC00:
			if i+1 >= len(units) || units[i+1] < 0xDC00 || units[i+1] >= 0xE000 {
				return "", errors.New("UTF-16 string with a lone surrogate")
			}
			i++
		case u >= 0xDC00 && u < 0xE000:
			return "", errors.New("UTF-16 string with a lone surrogate")
		}
	}
	return string(utf16.Decode(units)), nil
}

// encodeChars encodes a string as props.py does: u32 count of characters (Python's len), then Latin-1
// (string8) or UTF-16BE (string16) bytes.
func encodeChars(typ uint16, s string) ([]byte, error) {
	be := binary.BigEndian
	runes := []rune(s)
	out := be.AppendUint32(nil, uint32(len(runes)))
	if typ != typeString16 {
		b, ok := pytext.Latin1Encode(s)
		if !ok {
			return nil, fmt.Errorf("%q has a character Latin-1 can't hold", s)
		}
		return append(out, b...), nil
	}
	for _, u := range utf16.Encode(runes) {
		out = be.AppendUint16(out, u)
	}
	return out, nil
}

// str is a single string property's value (Prop.string).
func (p *prop) str() (string, error) {
	if !p.isString() || p.isArray() {
		return "", fmt.Errorf("%#010x is not a single string", p.id)
	}
	if len(p.raw) < 4 {
		return "", fmt.Errorf("%#010x: string cut short", p.id)
	}
	return decodeChars(p.typ, p.raw[4:])
}

func (p *prop) setStr(s string) error {
	if !p.isString() || p.isArray() {
		return fmt.Errorf("%#010x is not a single string", p.id)
	}
	raw, err := encodeChars(p.typ, s)
	if err != nil {
		return err
	}
	p.raw = raw
	return nil
}

// strs are a string array property's items (Prop.strings).
func (p *prop) strs() ([]string, error) {
	if !p.isString() || !p.isArray() {
		return nil, fmt.Errorf("%#010x is not a string array", p.id)
	}
	be := binary.BigEndian
	n := be.Uint32(p.raw)
	pos := uint64(8)
	width := uint64(1)
	if p.typ == typeString16 {
		width = 2
	}
	var out []string
	for i := uint32(0); i < n; i++ {
		k := uint64(be.Uint32(p.raw[pos:]))
		body := p.raw[pos+4 : pos+4+k*width]
		s, err := decodeChars(p.typ, body)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
		pos += 4 + uint64(len(body))
	}
	return out, nil
}

// setStrs replaces a string array's items; the item-size word (+4) stays as it was (Prop.set_strings).
func (p *prop) setStrs(items []string) error {
	if _, err := p.strs(); err != nil {
		return err
	}
	raw := binary.BigEndian.AppendUint32(nil, uint32(len(items)))
	raw = append(raw, p.raw[4:8]...)
	for _, s := range items {
		b, err := encodeChars(p.typ, s)
		if err != nil {
			return err
		}
		raw = append(raw, b...)
	}
	p.raw = raw
	return nil
}

// intValue is a single int32/uint32 property's value (Prop.int).
func (p *prop) intValue() (int64, error) {
	if (p.typ != typeInt32 && p.typ != typeUint32) || p.isArray() || len(p.raw) != 4 {
		return 0, fmt.Errorf("%#010x is not a single int32/uint32", p.id)
	}
	v := binary.BigEndian.Uint32(p.raw)
	if p.typ == typeInt32 {
		return int64(int32(v)), nil
	}
	return int64(v), nil
}

func (p *prop) setInt(v int64) error {
	if _, err := p.intValue(); err != nil {
		return err
	}
	if p.typ == typeInt32 && (v < -1<<31 || v > 1<<31-1) || p.typ == typeUint32 && (v < 0 || v > 1<<32-1) {
		return fmt.Errorf("%#010x: %d out of range", p.id, v)
	}
	p.raw = binary.BigEndian.AppendUint32(nil, uint32(v))
	return nil
}
