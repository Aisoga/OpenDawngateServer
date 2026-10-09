package assetdata

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"dawngate/server/internal/setup/pytext"
)

// ParseError is a file that doesn't parse (assetdata.py's ParseError): dump leaves the file out and
// records the message. Any other error from Parse means the reader itself can't go on (assetdata.py
// stopped with an exception) and ends the whole run.
type ParseError struct{ Msg string }

func (e *ParseError) Error() string { return e.Msg }

func parseErr(format string, a ...any) error { return &ParseError{fmt.Sprintf(format, a...)} }

// BadRef is a reference whose hashes don't match its text: the member path, the text, why.
type BadRef [3]string

// reader parses one file; pos is the out-of-line cursor (assetdata.py's Reader).
type reader struct {
	d      []byte
	ts     *Types
	loc    *Locale // nil: Keys stay text
	pos    int
	refBad []BadRef
}

// Parse parses one .bin file of type t (assetdata.py's parse_file): the object and the references whose
// hashes don't match their text. The file must be consumed exactly. With a Locale, Locale keys become
// {"ref", "text"}.
func (ts *Types) Parse(t *Type, data []byte, loc *Locale) (*Object, []BadRef, error) {
	r := &reader{d: data, ts: ts, loc: loc}
	if _, err := r.take(t.Size); err != nil {
		return nil, nil, err
	}
	obj, err := r.structAt(t, 0, "")
	if err != nil {
		return nil, nil, err
	}
	if r.pos != len(data) {
		return nil, nil, parseErr("%d unread byte(s) after the out-of-line data (at %#x)", len(data)-r.pos, r.pos)
	}
	return obj, r.refBad, nil
}

func (r *reader) need(off, n int) error {
	if off < 0 || off+n > len(r.d) {
		return parseErr("read of %d bytes at %#x past the end (%#x)", n, off, len(r.d))
	}
	return nil
}

func (r *reader) u32(off int) (uint32, error) {
	if err := r.need(off, 4); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(r.d[off:]), nil
}

func (r *reader) take(n int) (int, error) {
	start := r.pos
	if err := r.need(start, n); err != nil {
		return 0, err
	}
	r.pos += n
	return start, nil
}

func (r *reader) cstring() (string, error) {
	if err := r.need(r.pos, 1); err != nil {
		return "", err
	}
	e := bytes.IndexByte(r.d[r.pos:], 0)
	if e < 0 {
		return "", parseErr("unterminated string at %#x", r.pos)
	}
	s := pytext.DecodeUTF8(r.d[r.pos : r.pos+e])
	r.pos += e + 1
	return s, nil
}

// optString reads a char* / asset value: the next string if the pointer at off is non-zero, else nil.
func (r *reader) optString(off int) (any, error) {
	p, err := r.u32(off)
	if err != nil || p == 0 {
		return nil, err
	}
	s, err := r.cstring()
	if err != nil {
		return nil, err
	}
	return s, nil
}

// round7 is Python's float(f"{v:.7g}"): the value rounded to 7 significant digits.
func round7(v float64) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 7, 64), 64)
	return f
}

func hex8(v uint32) string { return fmt.Sprintf("0x%08x", v) }

func (r *reader) float32At(off int) float64 {
	return round7(float64(math.Float32frombits(binary.LittleEndian.Uint32(r.d[off:]))))
}

// prim reads a built-in value (assetdata.py's prim).
func (r *reader) prim(tname string, off int, m *Member) (any, error) {
	p := primSizes[tname]
	if err := r.need(off, p.size); err != nil {
		return nil, err
	}
	if tname == "float" {
		return r.float32At(off), nil
	}
	var u uint64
	switch p.size {
	case 1:
		u = uint64(r.d[off])
	case 2:
		u = uint64(binary.LittleEndian.Uint16(r.d[off:]))
	case 4:
		u = uint64(binary.LittleEndian.Uint32(r.d[off:]))
	case 8:
		u = binary.LittleEndian.Uint64(r.d[off:])
	}
	var s int64 // the signed reading
	switch p.size {
	case 4:
		s = int64(int32(uint32(u)))
	case 8:
		s = int64(u)
	}
	switch tname {
	case "bool":
		if u <= 1 {
			return u == 1, nil
		}
		return u, nil
	case "tObjID", "guid":
		return hex8(uint32(u)), nil
	case "enum":
		if m != nil && m.HasEnum {
			if e, ok := r.ts.enums[m.Enum]; ok {
				for _, ev := range e.Values {
					if ev.Value == s {
						return ev.Name, nil
					}
				}
			}
		}
		return s, nil
	case "bitfield":
		if m != nil && m.HasEnum {
			if e, ok := r.ts.enums[m.Enum]; ok {
				v := int64(u)
				names := []any{}
				rest := v
				for _, ev := range e.Values {
					if ev.Value != 0 && v&ev.Value == ev.Value {
						names = append(names, ev.Name)
						rest &^= ev.Value
					}
				}
				if rest != 0 {
					names = append(names, fmt.Sprintf("0x%x", rest))
				}
				return names, nil
			}
		}
		return u, nil
	}
	if p.signed {
		return s, nil
	}
	return u, nil
}

// fatal marks an error that stopped assetdata.py with an exception other than ParseError.
func fatal(format string, a ...any) error { return fmt.Errorf("assetdata: "+format, a...) }

// value reads a member's value (assetdata.py's Reader.value); m is nil for array elements and
// nullable targets.
func (r *reader) value(tname string, off int, m *Member, path string, count int) (any, error) {
	if count != 1 && tname != "char" {
		size := r.ts.typeSize(tname)
		out := []any{}
		for i := 0; i < count; i++ {
			v, err := r.value(tname, off+i*size, m, fmt.Sprintf("%s[%d]", path, i), 1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	if tname == "char" {
		n := 1
		if m != nil {
			n = m.Count
		}
		if err := r.need(off, n); err != nil {
			return nil, err
		}
		b := r.d[off : off+n]
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		return pytext.DecodeUTF8(b), nil
	}
	if _, ok := primSizes[tname]; ok {
		return r.prim(tname, off, m)
	}
	if n, ok := vectorSizes[tname]; ok {
		if err := r.need(off, 4*n); err != nil {
			return nil, err
		}
		out := make([]any, n)
		for i := range out {
			out[i] = r.float32At(off + 4*i)
		}
		return out, nil
	}
	switch tname {
	case "char*", "asset":
		return r.optString(off)
	case "Key":
		delta := -12
		if m != nil {
			delta = m.Delta
		}
		return r.key(off, delta, path)
	case "StringHash":
		delta := 4
		if m != nil {
			delta = m.Delta
		}
		return r.stringHash(off, delta, path)
	case "cLocalizedAssetString":
		delta := 8
		if m != nil {
			delta = m.Delta
		}
		return r.localizedAssetString(off, delta, path)
	case "array":
		if m == nil {
			return nil, fatal("%s: an array inside an array or nullable", path)
		}
		delta := m.Delta
		if delta == 0 {
			delta = 4
		}
		n, err := r.u32(off + delta) // the count decides; the pointer is ignored (0x008D4224)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return []any{}, nil
		}
		total := uint64(n) * uint64(m.ESize)
		if total > uint64(len(r.d)) {
			return nil, parseErr("read of %d bytes at %#x past the end (%#x)", total, r.pos, len(r.d))
		}
		base, err := r.take(int(total))
		if err != nil {
			return nil, err
		}
		if !m.HasElem {
			return nil, fatal("%s: array without an element type", path)
		}
		return r.arrayElements(m.Elem, base, int(n), m.ESize, path)
	case "nullable":
		if m == nil {
			return nil, fatal("%s: a nullable inside an array or nullable", path)
		}
		p, err := r.u32(off)
		if err != nil || p == 0 {
			return nil, err
		}
		base, err := r.take(m.ESize)
		if err != nil {
			return nil, err
		}
		if !m.HasElem {
			return nil, fatal("%s: nullable without an element type", path)
		}
		return r.element(m.Elem, base, path)
	}
	if t := r.ts.Type(tname); t != nil {
		return r.structAt(t, off, path)
	}
	return nil, parseErr("%s: unknown type %s", path, tname)
}

// element reads a nullable's target: a struct, or a plain value.
func (r *reader) element(tname string, off int, path string) (any, error) {
	if t := r.ts.Type(tname); t != nil {
		return r.structAt(t, off, path)
	}
	return r.value(tname, off, nil, path, 1)
}

// arrayElements reads an array's elements the way the client's walker does (0x008D4266-0x008D4365):
// struct elements are walked if NeedsFixup says the type needs it, char* and asset elements are strings,
// any other element type has no out-of-line data.
func (r *reader) arrayElements(elem string, base, n, esize int, path string) (any, error) {
	out := make([]any, 0, n)
	if t := r.ts.Type(elem); t != nil {
		start := r.pos
		for i := 0; i < n; i++ {
			v, err := r.structAt(t, base+i*esize, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		if r.pos != start {
			fix, err := r.ts.NeedsFixup(t)
			if err != nil {
				return nil, err
			}
			if !fix {
				return nil, parseErr("%s: %s elements carry out-of-line data the client skips", path, t.Name)
			}
		}
		return out, nil
	}
	if pointerTypes[elem] && elem != "char*" && elem != "asset" {
		return nil, parseErr("%s: the client reads no out-of-line data for an array of %s", path, elem)
	}
	for i := 0; i < n; i++ {
		v, err := r.value(elem, base+i*esize, nil, fmt.Sprintf("%s[%d]", path, i), 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// localeRef is the Locale reference form, "Table!0x<8 hex digits>" (re.match with a final $, which
// also matches before a last "\n").
var localeRef = regexp.MustCompile(`\A[^!]+!0x[0-9a-fA-F]{8}\n?\z`)

// key reads a Key registered at its char* (off); delta is where the hash part lies relative to it.
func (r *reader) key(off, delta int, path string) (any, error) {
	hoff := off + 4
	if delta < 0 {
		hoff = off + delta
	}
	if err := r.need(min(off, hoff), 16); err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	inst, typ, grp := le.Uint32(r.d[hoff:]), le.Uint32(r.d[hoff+4:]), le.Uint32(r.d[hoff+8:])
	text, err := r.optString(off)
	if err != nil {
		return nil, err
	}
	if text == nil {
		if inst != 0 || typ != 0 || grp != 0 {
			o := NewObject()
			o.Set("instance", hex8(inst))
			o.Set("type", hex8(typ))
			o.Set("group", hex8(grp))
			return o, nil
		}
		return nil, nil
	}
	s := text.(string)
	if why := keyMismatch(s, inst, typ, grp); why != "" {
		r.refBad = append(r.refBad, BadRef{path, s, why})
		o := NewObject()
		o.Set("ref", s)
		o.Set("instance", hex8(inst))
		o.Set("type", hex8(typ))
		o.Set("group", hex8(grp))
		return o, nil
	}
	if r.loc != nil && localeRef.MatchString(s) {
		o := NewObject()
		o.Set("ref", s)
		o.Set("text", r.loc.lookup(grp, inst))
		return o, nil
	}
	return s, nil
}

// stringHash reads a StringHash: its string and the FNV-1 at delta from its char*.
func (r *reader) stringHash(off, delta int, path string) (any, error) {
	h, err := r.u32(off + delta)
	if err != nil {
		return nil, err
	}
	text, err := r.optString(off)
	if err != nil {
		return nil, err
	}
	if text == nil {
		if h == 0 {
			return nil, nil
		}
		o := NewObject()
		o.Set("hash", hex8(h))
		return o, nil
	}
	s := text.(string)
	if f := fnv1(s); f != h {
		r.refBad = append(r.refBad, BadRef{path, s, fmt.Sprintf("hash %s != FNV-1 %s", hex8(h), hex8(f))})
		o := NewObject()
		o.Set("text", s)
		o.Set("hash", hex8(h))
		return o, nil
	}
	return s, nil
}

// localizedAssetString reads a cLocalizedAssetString (20 bytes, only in cLongDescription, which no file
// holds): two char* (+0, +4), then the Key hash part at +delta (8; inferred).
func (r *reader) localizedAssetString(off, delta int, path string) (any, error) {
	p1, err := r.u32(off)
	if err != nil {
		return nil, err
	}
	p2, err := r.u32(off + 4)
	if err != nil {
		return nil, err
	}
	h := off + delta
	if h < 0 || h+12 > len(r.d) {
		return nil, fatal("%s: cLocalizedAssetString hash part outside the file", path)
	}
	le := binary.LittleEndian
	inst, typ, grp := le.Uint32(r.d[h:]), le.Uint32(r.d[h+4:]), le.Uint32(r.d[h+8:])
	var a, b any
	if p1 != 0 {
		if a, err = r.cstring(); err != nil {
			return nil, err
		}
	}
	if p2 != 0 {
		if b, err = r.cstring(); err != nil {
			return nil, err
		}
	}
	o := NewObject()
	o.Set("text", a)
	o.Set("ref", b)
	if b != nil {
		bs := b.(string)
		if why := keyMismatch(bs, inst, typ, grp); why != "" {
			r.refBad = append(r.refBad, BadRef{path, bs, why})
			o.Set("instance", hex8(inst))
			o.Set("type", hex8(typ))
			o.Set("group", hex8(grp))
		} else if r.loc != nil && strings.Contains(bs, "!0x") {
			o.Set("en", r.loc.lookup(grp, inst))
		}
	}
	return o, nil
}

// structAt reads a struct at base: its members in table order, then "_unk" with every 4-byte chunk
// holding a non-zero byte no member covers.
func (r *reader) structAt(t *Type, base int, path string) (*Object, error) {
	if err := r.need(base, t.Size); err != nil {
		return nil, err
	}
	out := NewObject()
	covered := make([]bool, t.Size)
	for i := range t.Members {
		m := &t.Members[i]
		p := m.Name
		if path != "" {
			p = path + "." + m.Name
		}
		v, err := r.value(m.Type, base+m.Offset, m, p, m.Count)
		if err != nil {
			return nil, err
		}
		out.Set(m.Name, v)
		lo, hi := r.ts.memberSpan(*m)
		for k := max(0, lo); k < min(t.Size, hi); k++ {
			covered[k] = true
		}
	}
	unk := NewObject()
	for i := 0; i < t.Size; i += 4 {
		chunk := r.d[base+i : base+min(i+4, t.Size)]
		for k, c := range chunk {
			if c != 0 && !covered[i+k] {
				unk.Set(fmt.Sprintf("+0x%x", i), hex.EncodeToString(chunk))
				break
			}
		}
	}
	if unk.Len() > 0 {
		out.Set("_unk", unk)
	}
	return out, nil
}

// fnv1 is assetdata.py's fnv1: FNV-1 of the text lower-cased (Python's str.lower) and encoded as
// Latin-1 with '?' for what Latin-1 can't hold.
func fnv1(text string) uint32 {
	return pytext.FNV1(pytext.Latin1Replace(pytext.Lower(text)))
}

var hexLiteral = regexp.MustCompile(`\A0x[0-9a-fA-F]{1,8}\z`)

// typeIDs are Spore's fixed type ids by extension; other extensions hash with FNV-1.
var typeIDs = map[string]uint32{"prop": 0x00B1B104}

// partID is a Key part's id: a hex literal ("0x0d0712d6") stands for itself, anything else is hashed.
func partID(s string, ext bool) uint32 {
	if hexLiteral.MatchString(s) {
		v, _ := strconv.ParseUint(s[2:], 16, 32)
		return uint32(v)
	}
	if ext {
		if id, ok := typeIDs[pytext.Lower(s)]; ok {
			return id
		}
	}
	return fnv1(s)
}

// keyMismatch is "" if a Key's instance/type/group are the ones its text names, else the reason. Text
// forms: "Table!0x<id>" (Locale string), "Group!Name.ext", "Group!0x<inst>.0x<type>", "path\\name.ext".
func keyMismatch(text string, inst, typ, grp uint32) string {
	g, rest, bang := strings.Cut(text, "!")
	if !bang {
		g, rest = "", text
	}
	var name, ext string
	if hexLiteral.MatchString(rest) {
		name = rest
	} else if i := strings.LastIndexByte(rest, '.'); i >= 0 {
		name, ext = rest[:i], rest[i+1:]
	} else {
		name = rest
	}
	want := [3]uint32{partID(name, false), 0, 0}
	if ext != "" {
		want[1] = partID(ext, true)
	}
	if g != "" {
		want[2] = fnv1(g)
	}
	if want == [3]uint32{inst, typ, grp} {
		return ""
	}
	return fmt.Sprintf("hashes %08x/%08x/%08x, expected %08x/%08x/%08x", inst, typ, grp, want[0], want[1], want[2])
}

// errFatal reports whether err ends a whole run (anything but a ParseError).
func errFatal(err error) bool {
	var pe *ParseError
	return err != nil && !errors.As(err, &pe)
}
