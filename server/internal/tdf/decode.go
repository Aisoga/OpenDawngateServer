package tdf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
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

// The decoder's bounds: a payload comes from anyone who can connect (Blaze decodes requests before login), so
// nothing it claims may cost the server more than its own bytes justify.
//
//   - A list, map or intlist count must fit the bytes left (each item takes at least minSize bytes); a larger one
//     is an error at once, and no slice is reserved beyond what those bytes could hold.
//   - Containers (structs, lists, maps, unions) nest at most MaxDepth deep (a payload's top-level struct counts 0);
//     the deepest message the tests decode nests 8 (BattlegroundsAccounts::getCompleteData's reply with perk pages).
//   - A payload's decoded values may take at most DecodeBudget(len) bytes (estimated Go heap, charged by charge),
//     a bound on how far a payload can expand: the tests' messages of 500 bytes or more stay under 11 times their
//     size, the smaller ones under the base. A payload made mostly of empty structs (about 40 bytes for each byte)
//     or of map entries with one-byte keys and values (about 40 for 2) goes over it.
const (
	MaxDepth = 32

	budgetFactor = 32
	budgetBase   = 4096
)

// DecodeBudget is the estimated heap a payload of n bytes may decode into.
func DecodeBudget(n int) int { return budgetFactor*n + budgetBase }

type reader struct {
	d      []byte
	p      int
	depth  int // containers open around the value being read
	budget int // estimated heap bytes left (DecodeBudget)
}

// The estimated heap of decoded values (amd64; charged by charge): an interface slot, a Field, a struct, a
// list, a map entry with its index entry, and the smaller boxed values.
const (
	costSlot   = 16
	costField  = 48
	costStruct = 24
	costList   = 32
	costMap    = 56
	costEntry  = 64
	costUnion  = 24
	costBoxed  = 8
)

func (r *reader) charge(n int) error {
	if r.budget -= n; r.budget < 0 {
		return fmt.Errorf("decoded size over the budget of %d bytes at %d", DecodeBudget(len(r.d)), r.p)
	}
	return nil
}

// enter opens a container (struct, list, map, union); leave closes it.
func (r *reader) enter() error {
	if r.depth++; r.depth > MaxDepth {
		return fmt.Errorf("nested deeper than %d at %d", MaxDepth, r.p)
	}
	return nil
}

func (r *reader) leave() { r.depth-- }

// minSize is the fewest bytes a value of type t takes on the wire (1 for an unknown type, which fails at its
// first item anyway). A struct list element takes 1 (its terminator, or a list union's member byte).
func minSize(t Type) int {
	switch t {
	case TypeList, TypeObjType:
		return 2 // element type and count; component and type
	case TypeMap, TypeObjID:
		return 3
	case TypeFloat:
		return 4
	}
	return 1
}

// fits checks a claimed count of items of per bytes each against the bytes left.
func (r *reader) fits(n, per int) error {
	if n > (len(r.d)-r.p)/per {
		return fmt.Errorf("count %d too large for the %d bytes left at %d", n, len(r.d)-r.p, r.p)
	}
	return nil
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
	if n > int64(len(r.d)-r.p) { // no item takes less than a byte (fits checks the element's own size)
		return 0, fmt.Errorf("count %d too large for the %d bytes left at %d", n, len(r.d)-r.p, r.p)
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
		if err := r.charge(costBoxed); err != nil {
			return nil, err
		}
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
		if err := r.charge(costSlot + 3*int(n)); err != nil { // U+FFFD takes 3 bytes for 1
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
		if err := r.charge(costSlot + int(n)); err != nil {
			return nil, err
		}
		return append([]byte(nil), b...), nil
	case TypeStruct:
		if err := r.enter(); err != nil {
			return nil, err
		}
		defer r.leave()
		return r.structBody()
	case TypeList:
		if err := r.enter(); err != nil {
			return nil, err
		}
		defer r.leave()
		et, err := r.byte()
		if err != nil {
			return nil, err
		}
		n, err := r.count()
		if err == nil {
			err = r.fits(n, minSize(Type(et)))
		}
		if err == nil {
			err = r.charge(costList + costSlot*n)
		}
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
		if err := r.enter(); err != nil {
			return nil, err
		}
		defer r.leave()
		kt, err := r.byte()
		if err != nil {
			return nil, err
		}
		vt, err := r.byte()
		if err != nil {
			return nil, err
		}
		n, err := r.count()
		if err == nil {
			err = r.fits(n, minSize(Type(kt))+minSize(Type(vt)))
		}
		if err == nil {
			err = r.charge(costMap)
		}
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
			if err := r.charge(costEntry + len(id)); err != nil {
				return nil, err
			}
			if j, dup := index[id]; dup {
				m.Entries[j].Value = v
				continue
			}
			index[id] = len(m.Entries)
			m.Entries = append(m.Entries, MapEntry{Key: k, Value: v})
		}
		return m, nil
	case TypeUnion:
		if err := r.enter(); err != nil {
			return nil, err
		}
		defer r.leave()
		member, err := r.byte()
		if err == nil {
			err = r.charge(costUnion)
		}
		if err != nil {
			return nil, err
		}
		if member == UnionUnset {
			return &Union{Member: member}, nil
		}
		label, ft, err := r.header()
		if err == nil {
			err = r.charge(costField)
		}
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
		if err == nil {
			err = r.charge(costSlot + costBoxed*n) // count fits the bytes left: an int takes at least one
		}
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
		if err := r.charge(costSlot); err != nil {
			return nil, err
		}
		var o ObjType
		var err error
		if o.Component, err = r.int64(); err == nil {
			o.Type, err = r.int64()
		}
		return o, err
	case TypeObjID:
		if err := r.charge(costSlot + costBoxed); err != nil {
			return nil, err
		}
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
		if err := r.enter(); err != nil {
			return nil, err
		}
		defer r.leave()
		member := r.d[r.p]
		r.p++
		if err := r.charge(costSlot); err != nil {
			return nil, err
		}
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
	if err := r.charge(costStruct); err != nil {
		return nil, err
	}
	s := &Struct{}
	for r.p < len(r.d) {
		if r.d[r.p] == 0 {
			r.p++
			break
		}
		if err := r.member(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// member reads one tagged member into s.
func (r *reader) member(s *Struct) error {
	label, t, err := r.header()
	if err == nil {
		err = r.charge(costField)
	}
	if err != nil {
		return err
	}
	v, err := r.value(t)
	if err != nil {
		return err
	}
	s.set(Field{Label: label, Type: t, Value: v})
	return nil
}

// Decode reads a packet payload: a top-level struct without terminator. It refuses a payload that claims more
// items than its bytes can hold, nests deeper than MaxDepth or decodes into more than DecodeBudget(len(payload))
// bytes (the decoder's bounds, above).
func Decode(payload []byte) (*Struct, error) {
	r := &reader{d: payload, budget: DecodeBudget(len(payload))}
	s := &Struct{}
	for r.p < len(payload) {
		if err := r.member(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// decodeUTF8 is Python's bytes.decode("utf-8", "replace"): each maximal invalid subsequence becomes
// one U+FFFD. Valid text is copied as it is; otherwise the result grows as it is written (at most 3 bytes for
// each input byte).
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			out = append(out, c)
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
			out = utf8.AppendRune(out, 0xFFFD)
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
			out = utf8.AppendRune(out, 0xFFFD)
			i += j
			continue
		}
		out = append(out, b[i:i+need+1]...) // a valid sequence: its bytes as they are
		i += need + 1
	}
	return string(out)
}
