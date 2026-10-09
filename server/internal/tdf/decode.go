package tdf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Value is one decoded TDF value. Its dynamic type depends on the wire type:
//
//	TypeInt, TypeTimeValue  int64 (uint64 only for magnitudes above math.MaxInt64)
//	TypeString              string (invalid UTF-8 replaced by U+FFFD)
//	TypeBlob                []byte
//	TypeStruct              *Struct
//	TypeList                *List (a union element of a struct list is a *ListUnion)
//	TypeMap                 *Map
//	TypeUnion               *Union
//	TypeIntList             IntList
//	TypeObjType             ObjType
//	TypeObjID               ObjID
//	TypeFloat               float32
type Value = any

// Field is one tagged member of a struct.
type Field struct {
	Label string
	Type  Type
	Value Value
}

// Struct holds a struct's members in wire order. A label that occurs twice keeps its first position
// and its last value (as tdf.py's dict does).
type Struct struct {
	Fields []Field
}

// List is a TDF list; every item has the element type.
type List struct {
	Elem  Type
	Items []Value
}

// Map is a TDF map in wire order. A key that occurs twice keeps its first position and its last value.
type Map struct {
	KeyType, ValueType Type
	Entries            []MapEntry
}

// MapEntry is one key/value pair of a Map.
type MapEntry struct {
	Key, Value Value
}

// Union is a TDF union; Field is nil when the member is UnionUnset.
type Union struct {
	Member byte
	Field  *Field
}

// ListUnion is a union element of a list (HNET, a list of NetworkAddress). Heat2 writes it without a
// header: the list's element type is struct, and each element is the member byte (UnionUnset = no
// value), then the member's value without its VALU tag; for a struct member, its members and 0x00. The
// client's union decoder (0x00C6FB50) reads it that way when the header flag is off, as it is for list
// elements; its encoder wrote HNET so in session b8_run4 (docs/BLAZE.md, "Lists of unions").
//
// The decoder takes a struct element whose first byte is 0x01..0x7F for such a union: a member tag
// starts at 0x84 or above (its first character is a letter). Member 0 can't be told from an empty
// struct and decodes as one. Every list-of-union member the client has (NetworkAddress) has struct
// members, so Value is the member's struct (nil when the member is UnionUnset).
type ListUnion struct {
	Member byte
	Value  *Struct
}

// IntList is a TDF intlist.
type IntList []int64

// ObjType is a Blaze object type (component, type).
type ObjType struct{ Component, Type int64 }

// ObjID is a Blaze object id (component, type, id).
type ObjID struct{ Component, Type, ID int64 }

// Get returns the field with this label.
func (s *Struct) Get(label string) (Field, bool) {
	if s != nil {
		for _, f := range s.Fields {
			if f.Label == label {
				return f, true
			}
		}
	}
	return Field{}, false
}

// Int returns an integer member (TypeInt or TypeTimeValue).
func (s *Struct) Int(label string) (int64, bool) {
	f, ok := s.Get(label)
	if !ok {
		return 0, false
	}
	switch v := f.Value.(type) {
	case int64:
		return v, true
	case uint64:
		return int64(v), true
	}
	return 0, false
}

// Str returns a string member.
func (s *Struct) Str(label string) (string, bool) {
	f, ok := s.Get(label)
	if !ok {
		return "", false
	}
	v, ok := f.Value.(string)
	return v, ok
}

// Sub returns a struct member.
func (s *Struct) Sub(label string) (*Struct, bool) {
	f, ok := s.Get(label)
	if !ok {
		return nil, false
	}
	v, ok := f.Value.(*Struct)
	return v, ok
}

func (s *Struct) set(f Field) {
	for i := range s.Fields {
		if s.Fields[i].Label == f.Label {
			s.Fields[i] = f
			return
		}
	}
	s.Fields = append(s.Fields, f)
}

// The decoder's error messages are tdf.py's, so the server logs the same "(TDF decode failed: ...)".
var (
	errIndex     = errors.New("index out of range")
	errTruncated = errors.New("truncated TDF")
)

// maxCount bounds list, map and intlist lengths so a corrupt count can't allocate gigabytes.
const maxCount = 1 << 20

type reader struct {
	d []byte
	p int
}

func (r *reader) byte() (byte, error) {
	if r.p >= len(r.d) {
		return 0, errIndex
	}
	b := r.d[r.p]
	r.p++
	return b, nil
}

func (r *reader) varint() (neg bool, mag uint64, err error) {
	b, err := r.byte()
	if err != nil {
		return false, 0, err
	}
	neg, mag = b&0x40 != 0, uint64(b&0x3F)
	for shift := 6; b&0x80 != 0; shift += 7 {
		if b, err = r.byte(); err != nil {
			return false, 0, err
		}
		if shift < 64 {
			mag |= uint64(b&0x7F) << shift
		}
	}
	return neg, mag, nil
}

func (r *reader) int() (Value, error) {
	neg, mag, err := r.varint()
	switch {
	case err != nil:
		return nil, err
	case neg:
		return -int64(mag), nil
	case mag > math.MaxInt64:
		return mag, nil
	}
	return int64(mag), nil
}

func (r *reader) int64() (int64, error) {
	neg, mag, err := r.varint()
	if neg {
		return -int64(mag), err
	}
	return int64(mag), err
}

// count reads a list or map length; like tdf.py's range(), a negative one means no items.
func (r *reader) count() (int, error) {
	n, err := r.int64()
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, nil
	}
	if n > maxCount {
		return 0, fmt.Errorf("count %d too large at %d", n, r.p)
	}
	return int(n), nil
}

func (r *reader) take(n int64) ([]byte, error) {
	if n < 0 || n > int64(len(r.d)-r.p) {
		return nil, errTruncated
	}
	v := r.d[r.p : r.p+int(n)]
	r.p += int(n)
	return v, nil
}

func (r *reader) header() (string, Type, error) {
	l, err := r.take(3)
	if err != nil {
		return "", 0, err
	}
	t, err := r.byte()
	return DecodeLabel(l), Type(t), err
}

func (r *reader) value(t Type) (Value, error) {
	switch t {
	case TypeInt, TypeTimeValue:
		return r.int()
	case TypeString:
		n, err := r.int64()
		if err != nil || n == 0 {
			return "", err
		}
		b, err := r.take(n)
		if err != nil {
			return nil, err
		}
		return decodeUTF8(b[:len(b)-1]), nil // drops the NUL (tdf.py drops the last byte, NUL or not)
	case TypeBlob:
		n, err := r.int64()
		if err != nil {
			return nil, err
		}
		b, err := r.take(n)
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), b...), nil
	case TypeStruct:
		return r.structBody()
	case TypeList:
		et, err := r.byte()
		if err != nil {
			return nil, err
		}
		n, err := r.count()
		if err != nil {
			return nil, err
		}
		l := &List{Elem: Type(et), Items: make([]Value, 0, n)}
		for i := 0; i < n; i++ {
			v, err := r.listItem(Type(et))
			if err != nil {
				return nil, err
			}
			l.Items = append(l.Items, v)
		}
		return l, nil
	case TypeMap:
		kt, err := r.byte()
		if err != nil {
			return nil, err
		}
		vt, err := r.byte()
		if err != nil {
			return nil, err
		}
		n, err := r.count()
		if err != nil {
			return nil, err
		}
		m := &Map{KeyType: Type(kt), ValueType: Type(vt)}
		index := map[string]int{}
		for i := 0; i < n; i++ {
			k, err := r.value(Type(kt))
			if err != nil {
				return nil, err
			}
			v, err := r.value(Type(vt))
			if err != nil {
				return nil, err
			}
			id := Repr(k)
			if j, dup := index[id]; dup {
				m.Entries[j].Value = v
				continue
			}
			index[id] = len(m.Entries)
			m.Entries = append(m.Entries, MapEntry{Key: k, Value: v})
		}
		return m, nil
	case TypeUnion:
		member, err := r.byte()
		if err != nil {
			return nil, err
		}
		if member == UnionUnset {
			return &Union{Member: member}, nil
		}
		label, ft, err := r.header()
		if err != nil {
			return nil, err
		}
		v, err := r.value(ft)
		if err != nil {
			return nil, err
		}
		return &Union{Member: member, Field: &Field{Label: label, Type: ft, Value: v}}, nil
	case TypeIntList:
		n, err := r.count()
		if err != nil {
			return nil, err
		}
		l := make(IntList, 0, n)
		for i := 0; i < n; i++ {
			v, err := r.int64()
			if err != nil {
				return nil, err
			}
			l = append(l, v)
		}
		return l, nil
	case TypeObjType:
		var o ObjType
		var err error
		if o.Component, err = r.int64(); err == nil {
			o.Type, err = r.int64()
		}
		return o, err
	case TypeObjID:
		var o ObjID
		var err error
		if o.Component, err = r.int64(); err == nil {
			if o.Type, err = r.int64(); err == nil {
				o.ID, err = r.int64()
			}
		}
		return o, err
	case TypeFloat:
		b, err := r.take(4)
		if err != nil {
			return nil, err
		}
		return math.Float32frombits(binary.BigEndian.Uint32(b)), nil
	}
	return nil, fmt.Errorf("unknown TDF type %d at %d", t, r.p-1)
}

// listItem reads one list element; a struct element starting with 0x01..0x7F is a union (ListUnion).
func (r *reader) listItem(t Type) (Value, error) {
	if t == TypeStruct && r.p < len(r.d) && r.d[r.p] > 0 && r.d[r.p] <= UnionUnset {
		member := r.d[r.p]
		r.p++
		if member == UnionUnset {
			return &ListUnion{Member: member}, nil
		}
		s, err := r.structBody()
		if err != nil {
			return nil, err
		}
		return &ListUnion{Member: member, Value: s}, nil
	}
	return r.value(t)
}

// structBody reads members up to a 0x00 terminator or the end of the data.
func (r *reader) structBody() (*Struct, error) {
	s := &Struct{}
	for r.p < len(r.d) {
		if r.d[r.p] == 0 {
			r.p++
			break
		}
		label, t, err := r.header()
		if err != nil {
			return nil, err
		}
		v, err := r.value(t)
		if err != nil {
			return nil, err
		}
		s.set(Field{Label: label, Type: t, Value: v})
	}
	return s, nil
}

// Decode reads a packet payload: a top-level struct without terminator.
func Decode(payload []byte) (*Struct, error) {
	r := &reader{d: payload}
	s := &Struct{}
	for r.p < len(payload) {
		label, t, err := r.header()
		if err != nil {
			return nil, err
		}
		v, err := r.value(t)
		if err != nil {
			return nil, err
		}
		s.set(Field{Label: label, Type: t, Value: v})
	}
	return s, nil
}

// decodeUTF8 is Python's bytes.decode("utf-8", "replace"): each maximal invalid subsequence becomes
// one U+FFFD.
func decodeUTF8(b []byte) string {
	out := make([]rune, 0, len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			out = append(out, rune(c))
			i++
			continue
		}
		var need int
		lo, hi := byte(0x80), byte(0xBF) // allowed range of the first continuation byte
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c == 0xE0:
			need, lo = 2, 0xA0
		case c >= 0xE1 && c <= 0xEC, c == 0xEE, c == 0xEF:
			need = 2
		case c == 0xED:
			need, hi = 2, 0x9F
		case c == 0xF0:
			need, lo = 3, 0x90
		case c >= 0xF1 && c <= 0xF3:
			need = 3
		case c == 0xF4:
			need, hi = 3, 0x8F
		default:
			out = append(out, 0xFFFD)
			i++
			continue
		}
		j := 1
		for ; j <= need && i+j < len(b); j++ {
			cb := b[i+j]
			if j == 1 && (cb < lo || cb > hi) || j > 1 && (cb < 0x80 || cb > 0xBF) {
				break
			}
		}
		if j <= need { // incomplete or invalid: replace the valid prefix as one character
			out = append(out, 0xFFFD)
			i += j
			continue
		}
		r := rune(c) & (0x7F >> (need + 1))
		for k := 1; k <= need; k++ {
			r = r<<6 | rune(b[i+k]&0x3F)
		}
		out = append(out, r)
		i += need + 1
	}
	return string(out)
}
