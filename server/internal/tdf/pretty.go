package tdf

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Pretty renders a decoded payload as the server logs it. The output is byte for byte what tdf.py's
// pretty(decode(payload), indent) prints, including Python's repr() of strings, bytes and floats, so
// logs of the Go server and of the old Python stub compare line by line (golden: tdf_golden.json).
func Pretty(s *Struct, indent int) string { return pretty(s, indent) }

// In tdf.py's terms a *Struct or *Map is a dict, a *List or IntList a list, and a *Union, *ListUnion,
// ObjType or ObjID a tuple.
func isDict(v Value) bool { _, s := v.(*Struct); _, m := v.(*Map); return s || m }

func isList(v Value) bool { _, l := v.(*List); _, il := v.(IntList); return l || il }

func isTuple(v Value) bool {
	switch v.(type) {
	case *Union, *ListUnion, ObjType, ObjID:
		return true
	}
	return false
}

func isEmpty(v Value) bool {
	switch x := v.(type) {
	case *Struct:
		return len(x.Fields) == 0
	case *Map:
		return len(x.Entries) == 0
	case *List:
		return len(x.Items) == 0
	case IntList:
		return len(x) == 0
	}
	return false
}

func pretty(v Value, indent int) string {
	pad := strings.Repeat("  ", indent)
	// a dict member: nested containers on the next lines, everything else as repr() after the colon
	member := func(key string, x Value) string {
		if (isDict(x) || isList(x) || isTuple(x)) && !isEmpty(x) {
			return pad + key + ": \n" + pretty(x, indent+1)
		}
		return pad + key + ": " + Repr(x)
	}
	switch x := v.(type) {
	case *Struct:
		lines := make([]string, len(x.Fields))
		for i, f := range x.Fields {
			lines[i] = member(f.Label, f.Value)
		}
		return strings.Join(lines, "\n")
	case *Map:
		lines := make([]string, len(x.Entries))
		for i, e := range x.Entries {
			lines[i] = member(pyStr(e.Key), e.Value)
		}
		return strings.Join(lines, "\n")
	case *Union:
		s := pad + fmt.Sprintf("<union member %d>", x.Member)
		if x.Field != nil {
			s += "\n" + pretty(&Struct{Fields: []Field{*x.Field}}, indent+1)
		}
		return s
	case *ListUnion:
		s := pad + fmt.Sprintf("<union member %d>", x.Member)
		if x.Value != nil && len(x.Value.Fields) > 0 {
			s += "\n" + pretty(x.Value, indent+1)
		}
		return s
	case *List, IntList:
		items := listItems(x)
		lines := make([]string, len(items))
		for i, item := range items {
			if isDict(item) || isList(item) {
				lines[i] = pad + "- \n" + pretty(item, indent+1)
			} else {
				lines[i] = pad + "- " + Repr(item)
			}
		}
		return strings.Join(lines, "\n")
	}
	return pad + Repr(v)
}

func listItems(v Value) []Value {
	switch x := v.(type) {
	case *List:
		return x.Items
	case IntList:
		items := make([]Value, len(x))
		for i, n := range x {
			items[i] = n
		}
		return items
	}
	return nil
}

// Repr is Python's repr() of the value tdf.py decodes to.
func Repr(v Value) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case string:
		return pyStrRepr(x)
	case []byte:
		return pyBytesRepr(x)
	case float32:
		return pyFloatRepr(float64(x))
	case *Struct:
		parts := make([]string, len(x.Fields))
		for i, f := range x.Fields {
			parts[i] = pyStrRepr(f.Label) + ": " + Repr(f.Value)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *Map:
		parts := make([]string, len(x.Entries))
		for i, e := range x.Entries {
			parts[i] = keyRepr(e.Key) + ": " + Repr(e.Value)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *List, IntList:
		items := listItems(x)
		parts := make([]string, len(items))
		for i, item := range items {
			parts[i] = Repr(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Union:
		if x.Field == nil {
			return fmt.Sprintf("('union', %d, None)", x.Member)
		}
		return fmt.Sprintf("('union', %d, {%s: %s})", x.Member, pyStrRepr(x.Field.Label), Repr(x.Field.Value))
	case *ListUnion: // tdf.py: ("union", member, dict of the struct member or None)
		if x.Value == nil {
			return fmt.Sprintf("('union', %d, None)", x.Member)
		}
		return fmt.Sprintf("('union', %d, %s)", x.Member, Repr(x.Value))
	case ObjType:
		return fmt.Sprintf("(%d, %d)", x.Component, x.Type)
	case ObjID:
		return fmt.Sprintf("(%d, %d, %d)", x.Component, x.Type, x.ID)
	}
	return fmt.Sprintf("%v", v)
}

// keyRepr is repr() of a map key: tdf.py turns list keys into tuples.
func keyRepr(k Value) string {
	if isList(k) {
		items := listItems(k)
		parts := make([]string, len(items))
		for i, item := range items {
			parts[i] = Repr(item)
		}
		if len(parts) == 1 {
			return "(" + parts[0] + ",)"
		}
		return "(" + strings.Join(parts, ", ") + ")"
	}
	return Repr(k)
}

// pyStr is Python's str() of a map key, as an f-string prints it.
func pyStr(k Value) string {
	if s, ok := k.(string); ok {
		return s
	}
	return keyRepr(k)
}

func pyStrRepr(s string) string {
	quote := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range s {
		switch {
		case r == quote || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7F:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7F || unicode.IsPrint(r):
			b.WriteRune(r)
		case r <= 0xFF:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xFFFF:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteRune(quote)
	return b.String()
}

func pyBytesRepr(p []byte) string {
	quote := byte('\'')
	if strings.IndexByte(string(p), '\'') >= 0 && strings.IndexByte(string(p), '"') < 0 {
		quote = '"'
	}
	var b strings.Builder
	b.WriteString("b")
	b.WriteByte(quote)
	for _, c := range p {
		switch {
		case c == quote || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 0x20 || c >= 0x7F:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// pyFloatRepr is Python's repr(float): the shortest round-trip digits, positional for exponents
// -4..15, else scientific with at least two exponent digits.
func pyFloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	sci := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	exp, _ := strconv.Atoi(sci[strings.IndexByte(sci, 'e')+1:])
	if exp < -4 || exp >= 16 {
		return sci
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return s
}
