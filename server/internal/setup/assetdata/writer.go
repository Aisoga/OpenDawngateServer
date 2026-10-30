package assetdata

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The writer: Parse's inverse, for new assets (a map's Level, Markersets and tuning) and for changed
// copies of shipped ones.

// writer builds one .bin file: the fixed part first, then the out-of-line data appended in the order the
// reader (and the client's walk, 0x008D4020) consumes it.
type writer struct {
	ts   *Types
	d    []byte
	ptrs []pointerWord
	like []byte // WriteOptions.Like
}

// pointerWord is a pointer member that is present: where its word lies and where its data starts.
type pointerWord struct {
	at, target int
	array      bool // an array's: the client goes by the count alone, so Like's word is taken even if zero
}

// WriteOptions are the choices a file's value doesn't make.
type WriteOptions struct {
	// PointerBase is added to the file offset of a present member's data to give its pointer word, as the
	// build tool's memory image held the address of the data. The client only tests the words for zero,
	// so any base works; 0 means DefaultPointerBase.
	PointerBase uint32
	// Like is the file being rewritten, if any. The bytes no value carries come from it, each where Like
	// has the same layout: a present member's pointer word takes Like's non-zero word at the same place
	// (an array's takes Like's word even if zero: the client goes by the count alone), an empty array's
	// pointer word Like's word where Like's array is empty too,
	// and a char[N] the bytes after its NUL where Like holds the same text. So a file written back from its
	// own ParseExact value comes out byte for byte.
	Like []byte
}

// DefaultPointerBase is the pointer base of new files: the range the build tool's own pointers lie in.
const DefaultPointerBase = 0x02000000

// Write is Parse's inverse: the bytes of a .bin file of type t holding obj. obj has Parse's (or
// ParseExact's) form: members by name, in any order, a missing member reads as zero or empty; floats as
// float32 (exact) or float64 (rounded to the nearest float32); enums and bitfields by name or number; Keys
// and StringHashes as text (their hashes recomputed) or as the objects Parse makes for text-less or
// mismatched ones; "_unk" restores the bytes no member covers; "_badref" is ignored. A present member's
// pointer word gets a non-zero value (PointerBase plus its data's offset, or Like's word): the client only
// tests it for zero (0x008D4020), while the shipped files hold the build machine's heap addresses, which
// no value carries. A file of a class with a post-load hook is written with the hook's bytes as "_unk"
// gives them (zero in a new object): the client recomputes them at every load.
func (ts *Types) Write(t *Type, obj *Object, opt WriteOptions) ([]byte, error) {
	w := &writer{ts: ts, d: make([]byte, t.Size), like: opt.Like}
	if err := w.structAt(t, 0, obj, ""); err != nil {
		return nil, err
	}
	base := opt.PointerBase
	if base == 0 {
		base = DefaultPointerBase
	}
	for _, p := range w.ptrs {
		v := base + uint32(p.target)
		if p.at+4 <= len(opt.Like) {
			if old := binary.LittleEndian.Uint32(opt.Like[p.at:]); old != 0 || p.array {
				v = old
			}
		}
		binary.LittleEndian.PutUint32(w.d[p.at:], v)
	}
	return w.d, nil
}

// PointerWords are the places of the pointer words Write sets for obj (the present char*, asset, Key and
// StringHash texts, arrays with elements and nullables), in the order their data follows.
func (ts *Types) PointerWords(t *Type, obj *Object) ([]int, error) {
	w := &writer{ts: ts, d: make([]byte, t.Size)}
	if err := w.structAt(t, 0, obj, ""); err != nil {
		return nil, err
	}
	out := make([]int, len(w.ptrs))
	for i, p := range w.ptrs {
		out[i] = p.at
	}
	return out, nil
}

// alloc appends n zero bytes of out-of-line data and returns where they start.
func (w *writer) alloc(n int) int {
	at := len(w.d)
	w.d = append(w.d, make([]byte, n)...)
	return at
}

// pointTo marks the pointer word at off as present, pointing at target.
func (w *writer) pointTo(off, target int) {
	w.ptrs = append(w.ptrs, pointerWord{at: off, target: target})
}

// cstring appends s and its NUL as out-of-line data and points the word at off to it.
func (w *writer) cstring(off int, s string) error {
	if strings.IndexByte(s, 0) >= 0 {
		return fmt.Errorf("assetdata: string %q holds a NUL", s)
	}
	w.pointTo(off, len(w.d))
	w.d = append(w.d, s...)
	w.d = append(w.d, 0)
	return nil
}

func (w *writer) put32(off int, v uint32) { binary.LittleEndian.PutUint32(w.d[off:], v) }

// structAt writes the struct obj at base: its members in table order, then "_unk".
func (w *writer) structAt(t *Type, base int, obj *Object, path string) error {
	if obj == nil {
		obj = NewObject()
	}
	known := map[string]bool{"_unk": true, "_badref": true}
	for i := range t.Members {
		m := &t.Members[i]
		known[m.Name] = true
		p := m.Name
		if path != "" {
			p = path + "." + m.Name
		}
		v, _ := obj.Get(m.Name)
		if err := w.value(m.Type, base+m.Offset, m, p, m.Count, v); err != nil {
			return err
		}
	}
	for _, k := range obj.Keys() {
		if !known[k] {
			return fmt.Errorf("assetdata: %s: %s has no member %q", path, t.Name, k)
		}
	}
	if u, ok := obj.Get("_unk"); ok && u != nil {
		return w.unknown(t, base, u, path)
	}
	return nil
}

// unknown writes back "_unk": the bytes of each listed 4-byte chunk that no member covers.
func (w *writer) unknown(t *Type, base int, u any, path string) error {
	uo, ok := u.(*Object)
	if !ok {
		return fmt.Errorf("assetdata: %s._unk: %T, want an object", path, u)
	}
	covered := make([]bool, t.Size)
	for _, m := range t.Members {
		lo, hi := w.ts.memberSpan(m)
		for k := max(0, lo); k < min(t.Size, hi); k++ {
			covered[k] = true
		}
	}
	for _, k := range uo.Keys() {
		v, _ := uo.Get(k)
		hs, ok := v.(string)
		off, err := strconv.ParseInt(strings.TrimPrefix(k, "+0x"), 16, 32)
		if !ok || err != nil || !strings.HasPrefix(k, "+0x") {
			return fmt.Errorf("assetdata: %s._unk: bad entry %q: %v", path, k, v)
		}
		b, err := hex.DecodeString(hs)
		if err != nil || off < 0 || int(off)+len(b) > t.Size {
			return fmt.Errorf("assetdata: %s._unk[%s]: bad bytes %q", path, k, hs)
		}
		for i, c := range b {
			if !covered[int(off)+i] {
				w.d[base+int(off)+i] = c
			}
		}
	}
	return nil
}

// value writes a member's value at off (the reader's value); m is nil for array elements and nullable
// targets.
func (w *writer) value(tname string, off int, m *Member, path string, count int, v any) error {
	if count != 1 && tname != "char" {
		size := w.ts.typeSize(tname)
		var items []any
		if v != nil {
			var ok bool
			if items, ok = v.([]any); !ok || len(items) != count {
				return fmt.Errorf("assetdata: %s: want a list of %d, got %v", path, count, v)
			}
		}
		for i := 0; i < count; i++ {
			var x any
			if items != nil {
				x = items[i]
			}
			if err := w.value(tname, off+i*size, m, fmt.Sprintf("%s[%d]", path, i), 1, x); err != nil {
				return err
			}
		}
		return nil
	}
	if tname == "char" {
		n := 1
		if m != nil {
			n = m.Count
		}
		s, err := optText(v, path)
		if err != nil {
			return err
		}
		if len(s) > n || strings.IndexByte(s, 0) >= 0 {
			return fmt.Errorf("assetdata: %s: %q doesn't fit char[%d]", path, s, n)
		}
		copy(w.d[off:off+n], s)
		if k := len(s); k < n && off+n <= len(w.like) && string(w.like[off:off+k]) == s && w.like[off+k] == 0 {
			copy(w.d[off+k+1:off+n], w.like[off+k+1:off+n])
		}
		return nil
	}
	if _, ok := primSizes[tname]; ok {
		return w.prim(tname, off, m, path, v)
	}
	if n, ok := vectorSizes[tname]; ok {
		if v == nil {
			return nil
		}
		items, ok := v.([]any)
		if !ok || len(items) != n {
			return fmt.Errorf("assetdata: %s: want %d floats, got %v", path, n, v)
		}
		for i, x := range items {
			f, err := toFloat32(x, path)
			if err != nil {
				return err
			}
			w.put32(off+4*i, math.Float32bits(f))
		}
		return nil
	}
	switch tname {
	case "char*", "asset":
		if v == nil {
			return nil
		}
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("assetdata: %s: %T, want a string", path, v)
		}
		return w.cstring(off, s)
	case "Key":
		delta := -12
		if m != nil {
			delta = m.Delta
		}
		return w.key(off, delta, path, v)
	case "StringHash":
		delta := 4
		if m != nil {
			delta = m.Delta
		}
		return w.stringHash(off, delta, path, v)
	case "cLocalizedAssetString":
		delta := 8
		if m != nil {
			delta = m.Delta
		}
		return w.localizedAssetString(off, delta, path, v)
	case "array":
		if m == nil {
			return fmt.Errorf("assetdata: %s: an array inside an array or nullable", path)
		}
		if !m.HasElem {
			return fmt.Errorf("assetdata: %s: array without an element type", path)
		}
		delta := m.Delta
		if delta == 0 {
			delta = 4
		}
		items, ok := v.([]any)
		if !ok && v != nil {
			return fmt.Errorf("assetdata: %s: %T, want a list", path, v)
		}
		if len(items) == 0 {
			if off+8 <= len(w.like) && binary.LittleEndian.Uint32(w.like[off+delta:]) == 0 {
				copy(w.d[off:off+4], w.like[off:off+4])
			}
			return nil
		}
		w.put32(off+delta, uint32(len(items)))
		base := w.alloc(len(items) * m.ESize)
		w.pointTo(off, base)
		w.ptrs[len(w.ptrs)-1].array = true
		return w.arrayElements(m.Elem, base, m.ESize, path, items)
	case "nullable":
		if m == nil {
			return fmt.Errorf("assetdata: %s: a nullable inside an array or nullable", path)
		}
		if !m.HasElem {
			return fmt.Errorf("assetdata: %s: nullable without an element type", path)
		}
		if v == nil {
			return nil
		}
		base := w.alloc(m.ESize)
		w.pointTo(off, base)
		if t := w.ts.Type(m.Elem); t != nil {
			o, ok := v.(*Object)
			if !ok {
				return fmt.Errorf("assetdata: %s: %T, want an object", path, v)
			}
			return w.structAt(t, base, o, path)
		}
		return w.value(m.Elem, base, nil, path, 1, v)
	}
	if t := w.ts.Type(tname); t != nil {
		var o *Object
		if v != nil {
			var ok bool
			if o, ok = v.(*Object); !ok {
				return fmt.Errorf("assetdata: %s: %T, want an object", path, v)
			}
		}
		return w.structAt(t, off, o, path)
	}
	return fmt.Errorf("assetdata: %s: unknown type %s", path, tname)
}

// arrayElements writes an array's elements into their block at base, and their out-of-line data after
// it (struct, char* and asset elements; the reader refuses other element types with out-of-line data).
func (w *writer) arrayElements(elem string, base, esize int, path string, items []any) error {
	if t := w.ts.Type(elem); t != nil {
		for i, x := range items {
			var o *Object
			if x != nil {
				var ok bool
				if o, ok = x.(*Object); !ok {
					return fmt.Errorf("assetdata: %s[%d]: %T, want an object", path, i, x)
				}
			}
			if err := w.structAt(t, base+i*esize, o, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if pointerTypes[elem] && elem != "char*" && elem != "asset" {
		return fmt.Errorf("assetdata: %s: the client reads no out-of-line data for an array of %s", path, elem)
	}
	for i, x := range items {
		if err := w.value(elem, base+i*esize, nil, fmt.Sprintf("%s[%d]", path, i), 1, x); err != nil {
			return err
		}
	}
	return nil
}

// prim writes a built-in value (the reader's prim).
func (w *writer) prim(tname string, off int, m *Member, path string, v any) error {
	if v == nil {
		return nil
	}
	size := primSizes[tname].size
	if tname == "float" {
		f, err := toFloat32(v, path)
		if err != nil {
			return err
		}
		w.put32(off, math.Float32bits(f))
		return nil
	}
	var u uint64
	switch tname {
	case "bool":
		if b, ok := v.(bool); ok {
			if b {
				u = 1
			}
			break
		}
		n, err := toInt(v, path)
		if err != nil {
			return err
		}
		u = n
	case "tObjID", "guid":
		n, err := hexOrInt(v, path)
		if err != nil {
			return err
		}
		u = n
	case "enum":
		if s, ok := v.(string); ok {
			n, err := w.enumValue(m, s, path)
			if err != nil {
				return err
			}
			u = uint64(n)
			break
		}
		n, err := toInt(v, path)
		if err != nil {
			return err
		}
		u = n
	case "bitfield":
		if items, ok := v.([]any); ok {
			for _, x := range items {
				s, ok := x.(string)
				if !ok {
					return fmt.Errorf("assetdata: %s: bitfield item %v, want a name", path, x)
				}
				if strings.HasPrefix(s, "0x") {
					n, err := strconv.ParseUint(s[2:], 16, 64)
					if err != nil {
						return fmt.Errorf("assetdata: %s: %v", path, err)
					}
					u |= n
					continue
				}
				n, err := w.enumValue(m, s, path)
				if err != nil {
					return err
				}
				u |= uint64(n)
			}
			break
		}
		n, err := toInt(v, path)
		if err != nil {
			return err
		}
		u = n
	default:
		n, err := toInt(v, path)
		if err != nil {
			return err
		}
		u = n
	}
	if size < 8 && u>>(8*size) != 0 && int64(u)>>(8*size-1) != -1 {
		return fmt.Errorf("assetdata: %s: %v doesn't fit %s", path, v, tname)
	}
	switch size {
	case 1:
		w.d[off] = byte(u)
	case 2:
		binary.LittleEndian.PutUint16(w.d[off:], uint16(u))
	case 4:
		w.put32(off, uint32(u))
	case 8:
		binary.LittleEndian.PutUint64(w.d[off:], u)
	}
	return nil
}

// enumValue is the value of an enum or bitfield name in m's table.
func (w *writer) enumValue(m *Member, name, path string) (int64, error) {
	if m != nil && m.HasEnum {
		if e, ok := w.ts.enums[m.Enum]; ok {
			for _, ev := range e.Values {
				if ev.Name == name {
					return ev.Value, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("assetdata: %s: no enum value %q", path, name)
}

// key writes a Key: its char* at off and the hash part at delta from it.
func (w *writer) key(off, delta int, path string, v any) error {
	hoff := off + 4
	if delta < 0 {
		hoff = off + delta
	}
	var text any
	var h [3]uint32
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		text, h = x, keyHashes(x)
	case *Object:
		ref, _ := x.Get("ref")
		text = ref
		if _, ok := x.Get("instance"); ok {
			var err error
			if h, err = hashTriple(x, path); err != nil {
				return err
			}
		} else if s, ok := ref.(string); ok {
			h = keyHashes(s)
		} else {
			return fmt.Errorf("assetdata: %s: a Key object without ref or hashes", path)
		}
	default:
		return fmt.Errorf("assetdata: %s: %T, want a Key text", path, v)
	}
	for i, x := range h {
		w.put32(hoff+4*i, x)
	}
	switch s := text.(type) {
	case nil:
		return nil
	case string:
		return w.cstring(off, s)
	}
	return fmt.Errorf("assetdata: %s: Key ref %T, want a string", path, text)
}

// hashTriple reads {instance, type, group} as Parse writes them.
func hashTriple(o *Object, path string) ([3]uint32, error) {
	var h [3]uint32
	for i, k := range []string{"instance", "type", "group"} {
		x, _ := o.Get(k)
		n, err := hexOrInt(x, path+"."+k)
		if err != nil {
			return h, err
		}
		h[i] = uint32(n)
	}
	return h, nil
}

// stringHash writes a StringHash: its char* at off and the FNV-1 at delta from it.
func (w *writer) stringHash(off, delta int, path string, v any) error {
	var text any
	var h uint32
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		text, h = x, fnv1(x)
	case *Object:
		text, _ = x.Get("text")
		hv, ok := x.Get("hash")
		if ok {
			n, err := hexOrInt(hv, path+".hash")
			if err != nil {
				return err
			}
			h = uint32(n)
		} else if s, ok := text.(string); ok {
			h = fnv1(s)
		}
	default:
		return fmt.Errorf("assetdata: %s: %T, want a StringHash text", path, v)
	}
	w.put32(off+delta, h)
	switch s := text.(type) {
	case nil:
		return nil
	case string:
		return w.cstring(off, s)
	}
	return fmt.Errorf("assetdata: %s: StringHash text %T, want a string", path, text)
}

// localizedAssetString writes a cLocalizedAssetString: two char* (+0 text, +4 ref) and the ref's Key
// hash part at delta.
func (w *writer) localizedAssetString(off, delta int, path string, v any) error {
	if v == nil {
		return nil
	}
	o, ok := v.(*Object)
	if !ok {
		return fmt.Errorf("assetdata: %s: %T, want an object", path, v)
	}
	a, _ := o.Get("text")
	b, _ := o.Get("ref")
	var h [3]uint32
	if _, ok := o.Get("instance"); ok {
		var err error
		if h, err = hashTriple(o, path); err != nil {
			return err
		}
	} else if s, ok := b.(string); ok {
		h = keyHashes(s)
	}
	for i, x := range h {
		w.put32(off+delta+4*i, x)
	}
	for i, s := range []any{a, b} {
		if s == nil {
			continue
		}
		str, ok := s.(string)
		if !ok {
			return fmt.Errorf("assetdata: %s: %T, want a string", path, s)
		}
		if err := w.cstring(off+4*i, str); err != nil {
			return err
		}
	}
	return nil
}

// optText is a char[N] value: a string, or "" for nil.
func optText(v any, path string) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	}
	return "", fmt.Errorf("assetdata: %s: %T, want a string", path, v)
}

// toFloat32 takes a float32 as it is and rounds anything else numeric to the nearest float32.
func toFloat32(v any, path string) (float32, error) {
	switch x := v.(type) {
	case float32:
		return x, nil
	case float64:
		return float32(x), nil
	case int64:
		return float32(x), nil
	case uint64:
		return float32(x), nil
	case int:
		return float32(x), nil
	}
	return 0, fmt.Errorf("assetdata: %s: %T, want a number", path, v)
}

// toInt is an integer value's two's-complement bits.
func toInt(v any, path string) (uint64, error) {
	switch x := v.(type) {
	case int64:
		return uint64(x), nil
	case uint64:
		return x, nil
	case int:
		return uint64(int64(x)), nil
	case uint32:
		return uint64(x), nil
	case int32:
		return uint64(int64(x)), nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case float64:
		if x == math.Trunc(x) && x >= math.MinInt64 && x < math.MaxUint64 {
			if x < 0 {
				return uint64(int64(x)), nil
			}
			return uint64(x), nil
		}
	}
	return 0, fmt.Errorf("assetdata: %s: %v (%T), want an integer", path, v, v)
}

// hexOrInt is a "0x..." text (tObjID, guid, hashes) or an integer.
func hexOrInt(v any, path string) (uint64, error) {
	if s, ok := v.(string); ok {
		n, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
		if err != nil || !strings.HasPrefix(s, "0x") {
			return 0, fmt.Errorf("assetdata: %s: %q, want 0x<hex>", path, s)
		}
		return n, nil
	}
	return toInt(v, path)
}
