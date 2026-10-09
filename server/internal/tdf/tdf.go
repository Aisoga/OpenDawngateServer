// Package tdf implements Blaze's TDF (tagged data format) as the Dawngate client (BlazeSDK 3.19.11)
// speaks it. It is a port of the research codec re/scripts/tdf.py and is pinned by golden bytes and
// golden log text from that codec (server/testdata/tdf_golden.json, written by
// re/scripts/tdf_golden.py).
//
// Wire format of a field: a 3-byte label (four 6-bit characters, MSB first, char = value + 0x20), a
// 1-byte type, then the value. Struct members follow one another until a 0x00 byte; a packet payload
// is a top-level struct without that terminator. Integers are varints: the first byte holds a continue
// bit 0x80, a sign bit 0x40 and 6 value bits, each following byte a continue bit and 7 value bits.
// Strings are length-prefixed and include their NUL. Unions are a member byte (0x7F = unset) followed
// by one field.
//
// Reply layouts are never guessed: they come from the client's member tables
// (docs/data/tdf_member_tables.txt, members sorted by tag = write order), see docs/BLAZE.md.
package tdf

import "strconv"

// Type is a TDF wire type.
type Type byte

// The wire types (BlazeSDK 3.x).
const (
	TypeInt Type = iota
	TypeString
	TypeBlob
	TypeStruct
	TypeList
	TypeMap
	TypeUnion
	TypeIntList
	TypeObjType
	TypeObjID
	TypeFloat
	TypeTimeValue
)

// UnionUnset is the member byte of a union that holds no value.
const UnionUnset = 0x7F

var typeNames = [...]string{"int", "string", "blob", "struct", "list", "map", "union", "intlist",
	"objtype", "objid", "float", "timevalue"}

func (t Type) String() string {
	if int(t) < len(typeNames) {
		return typeNames[t]
	}
	return "type" + strconv.Itoa(int(t))
}

// EncodeLabel packs a tag such as "SECU" into its 3 wire bytes. Labels are at most 4 characters
// (longer ones are cut, shorter ones padded); each character keeps the low 6 bits of (char - 0x20).
func EncodeLabel(label string) [3]byte {
	r := []rune(label)
	var v uint32
	for i := 0; i < 4; i++ {
		var c uint32
		if i < len(r) && r[i] != 0 {
			c = uint32(r[i]-0x20) & 0x3F
		}
		v |= c << (18 - 6*i)
	}
	return [3]byte{byte(v >> 16), byte(v >> 8), byte(v)}
}

// DecodeLabel unpacks 3 wire bytes into a tag; zero characters are skipped.
func DecodeLabel(b []byte) string {
	v := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	out := make([]byte, 0, 4)
	for i := 0; i < 4; i++ {
		if c := (v >> (18 - 6*i)) & 0x3F; c != 0 {
			out = append(out, byte(c+0x20))
		}
	}
	return string(out)
}

// appendVarint writes a sign and a magnitude in TDF's varint form.
func appendVarint(b []byte, neg bool, mag uint64) []byte {
	first := byte(mag & 0x3F)
	if neg {
		first |= 0x40
	}
	mag >>= 6
	if mag != 0 {
		first |= 0x80
	}
	b = append(b, first)
	for mag != 0 {
		c := byte(mag & 0x7F)
		mag >>= 7
		if mag != 0 {
			c |= 0x80
		}
		b = append(b, c)
	}
	return b
}

func appendInt(b []byte, v int64) []byte {
	if v < 0 {
		return appendVarint(b, true, uint64(-v)) // also right for math.MinInt64
	}
	return appendVarint(b, false, uint64(v))
}
