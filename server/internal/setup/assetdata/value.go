package assetdata

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Object is a JSON object that keeps its keys in insertion order, as a Python dict does (setting a key
// again keeps its place). The values the reader makes are nil, bool, int64, uint64, float64, string,
// []any and *Object.
type Object struct {
	keys []string
	vals map[string]any
}

// NewObject returns an empty Object.
func NewObject() *Object { return &Object{vals: map[string]any{}} }

// Set sets a key's value.
func (o *Object) Set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// Get returns a key's value.
func (o *Object) Get(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// Keys are the keys in order.
func (o *Object) Keys() []string { return o.keys }

// Len is the number of keys.
func (o *Object) Len() int { return len(o.keys) }

// MarshalJSON writes the object with AppendJSON. A NaN or infinite float makes the output invalid
// JSON, so encoding/json refuses it: use AppendJSON directly to get Python's form.
func (o *Object) MarshalJSON() ([]byte, error) { return AppendJSON(nil, o), nil }

// AppendJSON appends v as JSON, keys in order, so that it decodes as Python's json.dump output of the same
// values does (spacing and float digits aside; a float32 is its own shortest form: a float is Go's shortest form, always with a point or an
// exponent, as Python's repr). NaN and the infinities become NaN, Infinity and -Infinity as with
// Python's default allow_nan, which no JSON reader takes (so a dump that holds one fails to load in both
// pipelines).
func AppendJSON(buf []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(buf, "null"...)
	case bool:
		return strconv.AppendBool(buf, x)
	case int64:
		return strconv.AppendInt(buf, x, 10)
	case uint64:
		return strconv.AppendUint(buf, x, 10)
	case int:
		return strconv.AppendInt(buf, int64(x), 10)
	case float32: // ParseExact's floats: the shortest text that reads back as the same float32
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return AppendJSON(buf, float64(x))
		}
		start := len(buf)
		buf = strconv.AppendFloat(buf, float64(x), 'g', -1, 32)
		if !strings.ContainsAny(string(buf[start:]), ".e") {
			buf = append(buf, '.', '0')
		}
		return buf
	case float64:
		switch {
		case math.IsNaN(x):
			return append(buf, "NaN"...)
		case math.IsInf(x, 1):
			return append(buf, "Infinity"...)
		case math.IsInf(x, -1):
			return append(buf, "-Infinity"...)
		}
		// Python's repr always marks a float ("1.0", "1e+16"), and encoding/json won't put "1.0" into an
		// int field: keep a ".0" where Go's shortest form has neither point nor exponent.
		start := len(buf)
		buf = strconv.AppendFloat(buf, x, 'g', -1, 64)
		if !strings.ContainsAny(string(buf[start:]), ".e") {
			buf = append(buf, '.', '0')
		}
		return buf
	case string:
		return appendString(buf, x)
	case []any:
		buf = append(buf, '[')
		for i, e := range x {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = AppendJSON(buf, e)
		}
		return append(buf, ']')
	case []string:
		buf = append(buf, '[')
		for i, e := range x {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendString(buf, e)
		}
		return append(buf, ']')
	case *Object:
		buf = append(buf, '{')
		for i, k := range x.keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendString(buf, k)
			buf = append(buf, ':')
			buf = AppendJSON(buf, x.vals[k])
		}
		return append(buf, '}')
	}
	panic("assetdata: AppendJSON: unexpected value type")
}

// appendString writes a JSON string; invalid UTF-8 (which the reader never makes) becomes U+FFFD.
func appendString(buf []byte, s string) []byte {
	const hexd = "0123456789abcdef"
	buf = append(buf, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\':
				buf = append(buf, '\\', c)
			case c == '\n':
				buf = append(buf, '\\', 'n')
			case c == '\r':
				buf = append(buf, '\\', 'r')
			case c == '\t':
				buf = append(buf, '\\', 't')
			case c < 0x20 || c == 0x7F:
				buf = append(buf, '\\', 'u', '0', '0', hexd[c>>4], hexd[c&0xF])
			default:
				buf = append(buf, c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		buf = utf8.AppendRune(buf, r)
		i += size
	}
	return append(buf, '"')
}
