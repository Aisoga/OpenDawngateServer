package tdf

import (
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
)

// Writer builds a payload, member by member, in the order the client's member table lists them
// (ascending tag):
//
//	w := tdf.NewWriter()
//	w.String("ASRC", "303107")
//	w.List("CIDS", tdf.TypeInt, []uint16{1, 4, 5})
//	w.Struct("CONF", func(w *tdf.Writer) {
//		w.Map("CONF", tdf.TypeString, tdf.TypeString, tdf.KV("pingPeriod", "20s"))
//	})
//	payload := w.Bytes()
//
// Invalid arguments (a list item that doesn't match its element type, ...) are programming errors and
// panic; the golden tests catch them.
type Writer struct {
	buf []byte
}

// NewWriter returns an empty Writer.
func NewWriter() *Writer { return &Writer{} }

// Bytes returns the payload written so far.
func (w *Writer) Bytes() []byte { return w.buf }

// Len returns the number of bytes written so far.
func (w *Writer) Len() int { return len(w.buf) }

func (w *Writer) header(label string, t Type) {
	l := EncodeLabel(label)
	w.buf = append(w.buf, l[0], l[1], l[2], byte(t))
}

// Int writes an integer member (also for the client's bool, enum and intN/uintN types).
func (w *Writer) Int(label string, v int64) *Writer {
	w.header(label, TypeInt)
	w.buf = appendInt(w.buf, v)
	return w
}

// Uint writes an unsigned integer member that may exceed math.MaxInt64.
func (w *Writer) Uint(label string, v uint64) *Writer {
	w.header(label, TypeInt)
	w.buf = appendVarint(w.buf, false, v)
	return w
}

// String writes a string member (UTF-8, NUL-terminated on the wire).
func (w *Writer) String(label, s string) *Writer {
	w.header(label, TypeString)
	w.buf = appendString(w.buf, s)
	return w
}

// Blob writes a blob member.
func (w *Writer) Blob(label string, b []byte) *Writer {
	w.header(label, TypeBlob)
	w.buf = appendInt(w.buf, int64(len(b)))
	w.buf = append(w.buf, b...)
	return w
}

// Struct writes a struct member: body writes its members into w, then the terminator follows.
func (w *Writer) Struct(label string, body func(w *Writer)) *Writer {
	w.header(label, TypeStruct)
	if body != nil {
		body(w)
	}
	w.buf = append(w.buf, 0)
	return w
}

// Union writes a union member. body writes exactly one field (conventionally labelled VALU); for
// UnionUnset pass a nil body.
func (w *Writer) Union(label string, member byte, body func(w *Writer)) *Writer {
	w.header(label, TypeUnion)
	w.buf = append(w.buf, member)
	if body != nil {
		body(w)
	}
	return w
}

// UnionItem is a list element of a union class for Writer.List(label, TypeStruct, []UnionItem{...}):
// the member byte, then (unless Member is UnionUnset) Body's members and 0x00, without a VALU header
// (see ListUnion). Body holds the struct member's members.
type UnionItem struct {
	Member byte
	Body   *Writer
}

// List writes a list member. items is a slice whose elements fit elem: integers for TypeInt,
// strings for TypeString, []byte for TypeBlob, *Writer (holding the members) or UnionItem for
// TypeStruct.
func (w *Writer) List(label string, elem Type, items any) *Writer {
	rv := reflect.ValueOf(items)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		panic(fmt.Sprintf("tdf: List %s: items must be a slice, not %T", label, items))
	}
	w.header(label, TypeList)
	w.buf = append(w.buf, byte(elem))
	w.buf = appendInt(w.buf, int64(rv.Len()))
	for i := 0; i < rv.Len(); i++ {
		w.raw(elem, rv.Index(i).Interface())
	}
	return w
}

// Pair is one map entry for Writer.Map; build it with KV.
type Pair struct {
	Key, Value any
}

// KV returns a map entry for Writer.Map.
func KV(key, value any) Pair { return Pair{Key: key, Value: value} }

// Map writes a map member; keys and values follow the same rules as List items.
func (w *Writer) Map(label string, key, value Type, pairs ...Pair) *Writer {
	w.header(label, TypeMap)
	w.buf = append(w.buf, byte(key), byte(value))
	w.buf = appendInt(w.buf, int64(len(pairs)))
	for _, p := range pairs {
		w.raw(key, p.Key)
		w.raw(value, p.Value)
	}
	return w
}

// Value writes a decoded value under a label, so decoded payloads can be re-encoded.
func (w *Writer) Value(label string, t Type, v Value) *Writer {
	w.header(label, t)
	w.buf = appendValue(w.buf, t, v)
	return w
}

// Encode re-encodes a decoded payload (the inverse of Decode).
func Encode(s *Struct) []byte {
	w := NewWriter()
	for _, f := range s.Fields {
		w.Value(f.Label, f.Type, f.Value)
	}
	return w.Bytes()
}

// raw writes a list item or map key/value from a builder argument.
func (w *Writer) raw(t Type, v any) {
	switch t {
	case TypeInt, TypeTimeValue:
		w.buf = appendAnyInt(w.buf, v)
	case TypeString:
		s, ok := v.(string)
		if !ok {
			panic(fmt.Sprintf("tdf: %T is not a string", v))
		}
		w.buf = appendString(w.buf, s)
	case TypeBlob:
		b, ok := v.([]byte)
		if !ok {
			panic(fmt.Sprintf("tdf: %T is not a blob", v))
		}
		w.buf = appendInt(w.buf, int64(len(b)))
		w.buf = append(w.buf, b...)
	case TypeStruct:
		if u, ok := v.(UnionItem); ok {
			w.buf = append(w.buf, u.Member)
			if u.Member != UnionUnset {
				if u.Body != nil {
					w.buf = append(w.buf, u.Body.buf...)
				}
				w.buf = append(w.buf, 0)
			}
			return
		}
		sub, ok := v.(*Writer)
		if !ok {
			panic(fmt.Sprintf("tdf: struct items must be *tdf.Writer or tdf.UnionItem, not %T", v))
		}
		w.buf = append(w.buf, sub.buf...)
		w.buf = append(w.buf, 0)
	default:
		panic(fmt.Sprintf("tdf: unsupported element type %d", t))
	}
}

func appendString(b []byte, s string) []byte {
	b = appendInt(b, int64(len(s)+1))
	b = append(b, s...)
	return append(b, 0)
}

func appendAnyInt(b []byte, v any) []byte {
	switch x := v.(type) {
	case int:
		return appendInt(b, int64(x))
	case int8:
		return appendInt(b, int64(x))
	case int16:
		return appendInt(b, int64(x))
	case int32:
		return appendInt(b, int64(x))
	case int64:
		return appendInt(b, x)
	case uint:
		return appendVarint(b, false, uint64(x))
	case uint8:
		return appendVarint(b, false, uint64(x))
	case uint16:
		return appendVarint(b, false, uint64(x))
	case uint32:
		return appendVarint(b, false, uint64(x))
	case uint64:
		return appendVarint(b, false, x)
	}
	panic(fmt.Sprintf("tdf: %T is not an integer", v))
}

// appendValue writes a decoded Value (see the Value type for the mapping).
func appendValue(b []byte, t Type, v Value) []byte {
	switch t {
	case TypeInt, TypeTimeValue:
		return appendAnyInt(b, v)
	case TypeString:
		return appendString(b, v.(string))
	case TypeBlob:
		x := v.([]byte)
		b = appendInt(b, int64(len(x)))
		return append(b, x...)
	case TypeStruct:
		for _, f := range v.(*Struct).Fields {
			l := EncodeLabel(f.Label)
			b = append(b, l[0], l[1], l[2], byte(f.Type))
			b = appendValue(b, f.Type, f.Value)
		}
		return append(b, 0)
	case TypeList:
		l := v.(*List)
		b = append(b, byte(l.Elem))
		b = appendInt(b, int64(len(l.Items)))
		for _, item := range l.Items {
			if u, ok := item.(*ListUnion); ok {
				b = append(b, u.Member)
				if u.Value != nil {
					b = appendValue(b, TypeStruct, u.Value)
				}
				continue
			}
			b = appendValue(b, l.Elem, item)
		}
		return b
	case TypeMap:
		m := v.(*Map)
		b = append(b, byte(m.KeyType), byte(m.ValueType))
		b = appendInt(b, int64(len(m.Entries)))
		for _, e := range m.Entries {
			b = appendValue(b, m.KeyType, e.Key)
			b = appendValue(b, m.ValueType, e.Value)
		}
		return b
	case TypeUnion:
		u := v.(*Union)
		b = append(b, u.Member)
		if u.Field != nil {
			l := EncodeLabel(u.Field.Label)
			b = append(b, l[0], l[1], l[2], byte(u.Field.Type))
			b = appendValue(b, u.Field.Type, u.Field.Value)
		}
		return b
	case TypeIntList:
		l := v.(IntList)
		b = appendInt(b, int64(len(l)))
		for _, x := range l {
			b = appendInt(b, x)
		}
		return b
	case TypeObjType:
		o := v.(ObjType)
		return appendInt(appendInt(b, o.Component), o.Type)
	case TypeObjID:
		o := v.(ObjID)
		return appendInt(appendInt(appendInt(b, o.Component), o.Type), o.ID)
	case TypeFloat:
		return binary.BigEndian.AppendUint32(b, math.Float32bits(v.(float32)))
	}
	panic(fmt.Sprintf("tdf: unknown type %d", t))
}
