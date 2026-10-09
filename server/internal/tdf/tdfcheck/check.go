package tdfcheck

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"dawngate/server/internal/tdf"
)

// Problem is one way a payload breaks its class's member table.
type Problem struct {
	Path   string // member path: "QOSS.BWPS.PSP", "PLST[0].PID", "LTPS[local].PSA"
	Offset int    // payload offset of the field's header (or of the value, for element problems)
	Msg    string
}

func (p Problem) String() string { return fmt.Sprintf("%s (byte %d): %s", p.Path, p.Offset, p.Msg) }

// Error is every problem Check found in one payload.
type Error struct {
	Class    string
	Problems []Problem
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "payload doesn't match %s's member table:", e.Class)
	for _, p := range e.Problems {
		b.WriteString("\n  " + p.String())
	}
	return b.String()
}

// Check walks payload, a top-level struct as a Fire frame carries it (no terminator), against the
// member table of class (see Lookup). It reports:
//   - a tag that isn't a member of the class (the client's decoder skips it);
//   - a tag that doesn't follow the previous one in ascending tag order, or repeats it (the client
//     reads members in tag order and puts back a greater tag, so a member out of order is lost);
//   - a wire type other than the one the client's decoder asks for the member's type code (a decode
//     error in the client);
//   - list element and map key/value wire types other than the dump's (the client skips the list);
//   - a union member index past the union's members, or a union field other than the member's tag
//     (VALU) and wire type; in a list of unions (HNET) each element is a member byte and the member's
//     value without a header (tdf.ListUnion), checked against the member's class;
//   - strings without their NUL, wire types the client doesn't know (>= 12), truncated data;
//
// and does the same inside nested structs (struct members, struct elements of lists, struct values of
// maps, struct members of unions) wherever the dump links their class. Values without a link are only
// walked. Missing members are fine: the client keeps their defaults. Returns nil or an *Error.
func (ts *Tables) Check(class string, payload []byte) error {
	t, err := ts.Lookup(class)
	if err != nil {
		return err
	}
	w := &walker{d: payload}
	if err := w.fields(t, "", true, 0); err != nil && !errors.Is(err, errStop) {
		w.add("", w.p, "%v", err)
	}
	if len(w.problems) == 0 {
		return nil
	}
	return &Error{Class: t.String(), Problems: w.problems}
}

// Require checks payload against class with the dump at DumpPath and reports every problem as a
// test error.
func Require(t testing.TB, class string, payload []byte) {
	t.Helper()
	err := Must(t).Check(class, payload)
	var e *Error
	if errors.As(err, &e) {
		for _, p := range e.Problems {
			t.Errorf("%s: %s", e.Class, p)
		}
	} else if err != nil {
		t.Error(err)
	}
}

// errStop ends the walk after a problem that leaves the rest unreadable (already reported).
var errStop = errors.New("stop")

const maxDepth = 64

type walker struct {
	d        []byte
	p        int
	problems []Problem
}

func (w *walker) add(path string, off int, format string, args ...any) {
	if path == "" {
		path = "(payload)"
	}
	w.problems = append(w.problems, Problem{Path: path, Offset: off, Msg: fmt.Sprintf(format, args...)})
}

// stop reports a problem that makes the rest of the payload unreadable.
func (w *walker) stop(path string, off int, format string, args ...any) error {
	w.add(path, off, format, args...)
	return errStop
}

func join(path, label string) string {
	if path == "" {
		return label
	}
	return path + "." + label
}

// fields checks the members of a struct body against t (nil: walk only). top: the payload itself,
// which ends with the data instead of a 0x00.
func (w *walker) fields(t *Table, path string, top bool, depth int) error {
	if depth > maxDepth {
		return w.stop(path, w.p, "nested deeper than %d levels", maxDepth)
	}
	var prevTag uint32
	prevLabel := ""
	for {
		if w.p >= len(w.d) {
			if top {
				return nil
			}
			return w.stop(path, w.p, "struct has no 0x00 terminator")
		}
		if w.d[w.p] == 0 {
			if top {
				return w.stop(path, w.p, "0x00 at the top level: the client stops reading the payload here")
			}
			w.p++
			return nil
		}
		start := w.p
		if len(w.d)-w.p < 4 {
			return w.stop(path, w.p, "truncated field header")
		}
		lb := w.d[w.p : w.p+3]
		wt := tdf.Type(w.d[w.p+3])
		w.p += 4
		label := tdf.DecodeLabel(lb)
		tag := uint32(lb[0])<<16 | uint32(lb[1])<<8 | uint32(lb[2])
		fp := join(path, label)
		if prevLabel != "" {
			switch {
			case tag == prevTag:
				w.add(fp, start, "%s appears twice", label)
			case tag < prevTag:
				w.add(fp, start, "out of tag order (after %s): the client's decoder has passed %s and loses it", prevLabel, label)
			}
		}
		prevTag, prevLabel = tag, label
		if wt >= 12 {
			return w.stop(fp, start, "wire type %d: the client's decoder knows 0..11 only", wt)
		}
		var m *Member
		if t != nil {
			var ok bool
			if m, ok = t.Member(label); !ok {
				w.add(fp, start, "%s isn't a member of %s: the client skips it", label, t)
			} else if wt != m.Wire {
				w.add(fp, start, "wire type %s, but the client decodes %s (%s) as %s", wt, label, m.Type, m.Wire)
				m = nil // walk the value as sent
			}
		}
		if err := w.value(m, wt, fp, depth); err != nil {
			return err
		}
	}
}

// value checks (m set and matching) or walks one value of wire type wt.
func (w *walker) value(m *Member, wt tdf.Type, path string, depth int) error {
	var link *Table
	var elems []tdf.Type
	var union *Union
	if m != nil {
		link, elems, union = m.Link, m.Elems, m.Union
	}
	switch wt {
	case tdf.TypeStruct:
		return w.fields(link, path, false, depth+1)
	case tdf.TypeList:
		at := w.p
		et, err := w.byte(path)
		if err != nil {
			return err
		}
		n, err := w.count(path)
		if err != nil {
			return err
		}
		check := elems != nil
		if check && tdf.Type(et) != elems[0] {
			w.add(path, at, "list of %s, but the client's list holds %s (it skips the whole list)", tdf.Type(et), elems[0])
			check = false
		}
		for i := 0; i < n; i++ {
			ep := fmt.Sprintf("%s[%d]", path, i)
			var err error
			switch {
			case check && union != nil:
				err = w.listUnion(union, ep, depth+1)
			case check:
				err = w.value(&Member{Link: link}, tdf.Type(et), ep, depth+1)
			case tdf.Type(et) == tdf.TypeStruct && w.p < len(w.d) && w.d[w.p] > 0 && w.d[w.p] <= tdf.UnionUnset:
				err = w.listUnion(nil, ep, depth+1) // walk a union element of an unknown list (tdf.ListUnion)
			default:
				err = w.value(nil, tdf.Type(et), ep, depth+1)
			}
			if err != nil {
				return err
			}
		}
		return nil
	case tdf.TypeMap:
		at := w.p
		kt, err := w.byte(path)
		if err != nil {
			return err
		}
		vt, err := w.byte(path)
		if err != nil {
			return err
		}
		n, err := w.count(path)
		if err != nil {
			return err
		}
		check := elems != nil
		if check && (tdf.Type(kt) != elems[0] || tdf.Type(vt) != elems[1]) {
			w.add(path, at, "map<%s,%s>, but the client's map is map<%s,%s> (it skips the whole map)",
				tdf.Type(kt), tdf.Type(vt), elems[0], elems[1])
			check = false
		}
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("#%d", i)
			if tdf.Type(kt) == tdf.TypeString {
				s, err := w.str(path)
				if err != nil {
					return err
				}
				key = s
			} else if err := w.value(nil, tdf.Type(kt), path, depth+1); err != nil {
				return err
			}
			var sub *Member
			if check {
				sub = &Member{Link: link}
			}
			if err := w.value(sub, tdf.Type(vt), fmt.Sprintf("%s[%s]", path, key), depth+1); err != nil {
				return err
			}
		}
		return nil
	case tdf.TypeUnion:
		// The client's decoder (0x00C6FB50) reads the member byte, sets the union's active member
		// (an index past the end resets it to unset) and lets the union read that member's field.
		at := w.p
		member, err := w.byte(path)
		if err != nil {
			return err
		}
		if member == tdf.UnionUnset {
			return nil
		}
		var um *UnionMember
		if union != nil {
			if int(member) < len(union.Members) {
				um = &union.Members[member]
			} else {
				w.add(path, at, "union member %d, but %s has %d members (the client skips the value)",
					member, union, len(union.Members))
			}
		}
		if len(w.d)-w.p < 4 {
			return w.stop(path, w.p, "truncated union field header")
		}
		start := w.p
		label := tdf.DecodeLabel(w.d[w.p : w.p+3])
		ft := tdf.Type(w.d[w.p+3])
		w.p += 4
		fp := join(path, label)
		if ft >= 12 {
			return w.stop(fp, start, "wire type %d: the client's decoder knows 0..11 only", ft)
		}
		var sub *Member
		switch {
		case um == nil:
		case label != um.Label:
			w.add(fp, start, "union member %d's field is %s, not %s (the client skips it)", member, um.Label, label)
		case ft != um.Wire:
			w.add(fp, start, "wire type %s, but %s member %d is %s", ft, union, member, um.Wire)
		default:
			sub = &Member{Link: um.Link}
		}
		return w.value(sub, ft, fp, depth+1)
	case tdf.TypeString:
		_, err := w.str(path)
		return err
	case tdf.TypeBlob:
		n, err := w.length(path)
		if err != nil {
			return err
		}
		w.p += n
		return nil
	case tdf.TypeIntList:
		// Wire type 7 as the client decodes it (code 0x06, decoder 0x00C6F210): a present byte, then
		// a TDF id varint and a struct body. Untested against real data; our codec reads it as an
		// int list, so no reply should use it yet.
		present, err := w.byte(path)
		if err != nil || present == 0 {
			return err
		}
		if _, _, err := w.varint(path); err != nil {
			return err
		}
		return w.fields(nil, path, false, depth+1)
	case tdf.TypeObjType:
		return w.varints(path, 2)
	case tdf.TypeObjID:
		return w.varints(path, 3)
	case tdf.TypeFloat:
		if len(w.d)-w.p < 4 {
			return w.stop(path, w.p, "truncated float")
		}
		w.p += 4
		return nil
	default: // int, timevalue
		_, _, err := w.varint(path)
		return err
	}
}

// listUnion checks one union element of a list (HNET, a list of NetworkAddress; u nil: walk only). In a
// list the client's union decoder (0x00C6FB50) runs with the header flag off: it reads the member byte
// (0x7F = unset), sets the active member and lets the union read that member's value without a VALU
// header; a struct member is its members and 0x00 (struct decoder 0x00C6F2F0). See tdf.ListUnion.
func (w *walker) listUnion(u *Union, path string, depth int) error {
	at := w.p
	member, err := w.byte(path)
	if err != nil || member == tdf.UnionUnset {
		return err
	}
	ep := fmt.Sprintf("%s<%d>", path, member)
	if u == nil {
		return w.fields(nil, ep, false, depth+1)
	}
	if int(member) >= len(u.Members) {
		// the client resets the union to unset and then skips one field header and value
		return w.stop(path, at, "union member %d, but %s has %d members (the client misreads the rest)",
			member, u, len(u.Members))
	}
	um := u.Members[member]
	return w.value(&Member{Link: um.Link}, um.Wire, ep, depth+1)
}

func (w *walker) byte(path string) (byte, error) {
	if w.p >= len(w.d) {
		return 0, w.stop(path, w.p, "payload ends inside the value")
	}
	b := w.d[w.p]
	w.p++
	return b, nil
}

func (w *walker) varint(path string) (neg bool, mag uint64, err error) {
	b, err := w.byte(path)
	if err != nil {
		return false, 0, err
	}
	neg, mag = b&0x40 != 0, uint64(b&0x3F)
	for shift := 6; b&0x80 != 0; shift += 7 {
		if b, err = w.byte(path); err != nil {
			return false, 0, err
		}
		if shift < 64 {
			mag |= uint64(b&0x7F) << shift
		}
	}
	return neg, mag, nil
}

func (w *walker) varints(path string, n int) error {
	for i := 0; i < n; i++ {
		if _, _, err := w.varint(path); err != nil {
			return err
		}
	}
	return nil
}

// count reads a list or map length: not negative, and not more items than bytes left.
func (w *walker) count(path string) (int, error) {
	at := w.p
	neg, n, err := w.varint(path)
	if err != nil {
		return 0, err
	}
	if neg || n > uint64(len(w.d)-w.p) {
		return 0, w.stop(path, at, "bad element count")
	}
	return int(n), nil
}

// length reads a string or blob length that must fit in the rest of the payload.
func (w *walker) length(path string) (int, error) {
	at := w.p
	neg, n, err := w.varint(path)
	if err != nil {
		return 0, err
	}
	if neg || n > uint64(len(w.d)-w.p) {
		return 0, w.stop(path, at, "length %d runs past the end of the payload", n)
	}
	return int(n), nil
}

// str reads a string: length (including the NUL), bytes, NUL.
func (w *walker) str(path string) (string, error) {
	at := w.p
	n, err := w.length(path)
	if err != nil {
		return "", err
	}
	s := w.d[w.p : w.p+n]
	w.p += n
	if n == 0 || s[n-1] != 0 {
		w.add(path, at, "string without its NUL terminator (the client drops the last byte)")
		return string(s), nil
	}
	return string(s[:n-1]), nil
}
