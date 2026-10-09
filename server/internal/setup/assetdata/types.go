// Package assetdata reads AssetData.snap's binary asset definitions (AssetData/Binary/<Name>.<Class>.bin)
// with the client's own reflection tables: re/scripts/assetdata.py's port.
// An object comes out as assetdata.py dump
// writes it (Object: members in table order, enums by name, floats rounded to 7 significant digits,
// Keys as text or, with a Locale, {"ref", "text"}, "_unk" for bytes no member covers, "_badref" for
// references whose hashes don't match their text), so JSON of it reads the same as the dump's.
//
// The type tables are docs/data/assetdata_types.txt (written by re/scripts/assetdata_types.py from
// Dawngate.exe), embedded through the synced copy assetdata_types.txt in this folder: go:embed can't
// reach docs/. TestTypesInSync fails when the two differ; `go test ./internal/setup/assetdata -run
// TestTypesInSync -update` copies docs/data/assetdata_types.txt over the embedded one.
package assetdata

import (
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed assetdata_types.txt
var embeddedTypes []byte

// Member is one member of a type's table (a member line of assetdata_types.txt).
type Member struct {
	Index   int
	Offset  int
	Name    string
	Type    string // float, enum, Key, array, a struct type's name, ...
	Count   int    // N of char[N] and fixed arrays (1 without " xN")
	Elem    string // an array's or nullable's element type
	HasElem bool
	ESize   int // its element size
	Delta   int // an array's count position, a Key's or StringHash's hash part relative to its char*
	Enum    uint32
	HasEnum bool // Enum names an enum or bitfield table
}

// Type is one registered type (a "==" line of assetdata_types.txt and its members).
type Type struct {
	Name    string
	Size    int
	Members []Member
	CRC     uint32 // the table's typeCrc (CatalogEntry.typeCrc must equal it)
	HasCRC  bool
	Hooks   map[string]int64 // version, upgrade, postload, unload, hook4: the ones the line names
}

// EnumValue is one {value, name} pair of an enum or bitfield table (a bitfield's value is its mask).
type EnumValue struct {
	Value int64
	Name  string
}

// Enum is an enum or bitfield table.
type Enum struct {
	Kind   string // "enum" or "bitfield"
	Values []EnumValue
}

// Types are the parsed reflection tables. A Types keeps the client's "needs a fixup walk" answer per
// type once computed (assetdata.py caches it on its Type objects), so use one per run, from one
// goroutine.
type Types struct {
	byName map[string]*Type // lower-case name
	enums  map[uint32]Enum
	fixup  map[*Type]bool
}

// Embedded parses the embedded copy of docs/data/assetdata_types.txt.
func Embedded() (*Types, error) { return ParseTypes(embeddedTypes) }

// EmbeddedText is the embedded copy of docs/data/assetdata_types.txt.
func EmbeddedText() []byte { return embeddedTypes }

// Type returns the type of this name (any case), or nil.
func (ts *Types) Type(name string) *Type { return ts.byName[strings.ToLower(name)] }

// Names are the types' names, sorted.
func (ts *Types) Names() []string {
	var out []string
	for _, t := range ts.byName {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// Enum returns an enum or bitfield table by its address.
func (ts *Types) Enum(va uint32) (Enum, bool) {
	e, ok := ts.enums[va]
	return e, ok
}

var (
	typeLine   = regexp.MustCompile(`^== (\S+) size=0x([0-9a-f]+) `)
	typeKV     = regexp.MustCompile(` (\w+)=(\S+)`)
	memberLine = regexp.MustCompile(`^\s+(\d+) \+0x([0-9a-f]+) (\S+) (\S+)(.*)$`)
	memberKV   = regexp.MustCompile(`(\w+)=(\S+)`)
	memberCnt  = regexp.MustCompile(` x(\d+)`)
)

// ParseTypes parses assetdata_types.txt (assetdata.py's load_types). Lines it doesn't know (comments,
// blank lines) are skipped; a "==" or "enum" line it can't read is an error, as it stopped assetdata.py.
func ParseTypes(text []byte) (*Types, error) {
	s := strings.ReplaceAll(string(text), "\r\n", "\n") // Python's universal newlines
	s = strings.ReplaceAll(s, "\r", "\n")
	ts := &Types{byName: map[string]*Type{}, enums: map[uint32]Enum{}, fixup: map[*Type]bool{}}
	var cur *Type
	for n, line := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(line, "== "):
			m := typeLine.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("assetdata types line %d: no type head: %q", n+1, line)
			}
			size, err := strconv.ParseInt(m[2], 16, 64)
			if err != nil {
				return nil, fmt.Errorf("assetdata types line %d: %v", n+1, err)
			}
			cur = &Type{Name: m[1], Size: int(size), Hooks: map[string]int64{}}
			kv := map[string]string{}
			for _, p := range typeKV.FindAllStringSubmatch(line, -1) {
				kv[p[1]] = p[2]
			}
			if v, ok := kv["crc"]; ok {
				crc, err := parseHex(v)
				if err != nil {
					return nil, fmt.Errorf("assetdata types line %d: crc: %v", n+1, err)
				}
				cur.CRC, cur.HasCRC = uint32(crc), true
			}
			for _, k := range []string{"version", "upgrade", "postload", "unload", "hook4"} {
				if v, ok := kv[k]; ok {
					h, err := strconv.ParseInt(v, 0, 64)
					if err != nil {
						return nil, fmt.Errorf("assetdata types line %d: %s: %v", n+1, k, err)
					}
					cur.Hooks[k] = h
				}
			}
			ts.byName[strings.ToLower(cur.Name)] = cur
		case strings.HasPrefix(line, "enum "):
			head, vals, ok := strings.Cut(line, ": ")
			f := strings.Fields(head)
			if !ok || len(f) != 4 {
				return nil, fmt.Errorf("assetdata types line %d: bad enum line %q", n+1, line)
			}
			va, err := parseHex(f[1])
			if err != nil {
				return nil, fmt.Errorf("assetdata types line %d: %v", n+1, err)
			}
			e := Enum{Kind: f[2]}
			for _, item := range strings.Split(vals, " | ") {
				v, name, _ := strings.Cut(item, "=")
				iv, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
				if err != nil {
					return nil, fmt.Errorf("assetdata types line %d: %v", n+1, err)
				}
				e.Values = append(e.Values, EnumValue{iv, name})
			}
			ts.enums[uint32(va)] = e
		default:
			m := memberLine.FindStringSubmatch(line)
			if m == nil || cur == nil {
				continue
			}
			idx, err1 := strconv.Atoi(m[1])
			off, err2 := strconv.ParseInt(m[2], 16, 64)
			err := errors.Join(err1, err2)
			if err != nil {
				return nil, fmt.Errorf("assetdata types line %d: %v", n+1, err)
			}
			mb := Member{Index: idx, Offset: int(off), Name: m[3], Type: m[4], Count: 1}
			rest := m[5]
			kv := map[string]string{}
			for _, p := range memberKV.FindAllStringSubmatch(rest, -1) {
				kv[p[1]] = p[2]
			}
			if c := memberCnt.FindStringSubmatch(rest); c != nil {
				if mb.Count, err = strconv.Atoi(c[1]); err != nil {
					return nil, fmt.Errorf("assetdata types line %d: %v", n+1, err)
				}
			}
			mb.Elem, mb.HasElem = kv["elem"]
			if v, ok := kv["esize"]; ok {
				es, err := parseHex(v)
				if err != nil {
					return nil, fmt.Errorf("assetdata types line %d: esize: %v", n+1, err)
				}
				mb.ESize = int(es)
			}
			if v, ok := kv["delta"]; ok {
				if mb.Delta, err = strconv.Atoi(v); err != nil {
					return nil, fmt.Errorf("assetdata types line %d: delta: %v", n+1, err)
				}
			}
			if v, ok := kv["enum"]; ok {
				ev, err := parseHex(v)
				if err != nil {
					return nil, fmt.Errorf("assetdata types line %d: enum: %v", n+1, err)
				}
				mb.Enum, mb.HasEnum = uint32(ev), true
			}
			cur.Members = append(cur.Members, mb)
		}
	}
	return ts, nil
}

// parseHex is Python's int(v, 16), which takes an optional 0x.
func parseHex(v string) (uint64, error) {
	v = strings.TrimPrefix(strings.TrimPrefix(v, "0x"), "0X")
	return strconv.ParseUint(v, 16, 64)
}

// pointerTypes are the member types with out-of-line data (assetdata.py's POINTER_TYPES).
var pointerTypes = map[string]bool{"char*": true, "asset": true, "Key": true, "StringHash": true, "nullable": true,
	"cLocalizedAssetString": true}

// NeedsFixup is the client's test whether an object of type t needs the fixup walk (0x008CCDF0;
// assetdata.py's needs_fixup): a postLoad or +0x10 hook, or a member with out-of-line data, where the
// first array member decides alone (the client returns its element's answer without looking at later
// members). A type that refers to itself counts as "no" while its own answer is computed.
func (ts *Types) NeedsFixup(t *Type) (bool, error) {
	if v, ok := ts.fixup[t]; ok {
		return v, nil
	}
	ts.fixup[t] = false
	if t.Hooks["postload"] != 0 || t.Hooks["hook4"] != 0 {
		ts.fixup[t] = true
		return true, nil
	}
	for _, m := range t.Members {
		if m.Type == "array" {
			if !m.HasElem {
				return false, fmt.Errorf("assetdata: %s.%s: array without an element type", t.Name, m.Name)
			}
			v, err := ts.typeHasPointers(m.Elem)
			if err != nil {
				return false, err
			}
			ts.fixup[t] = v
			return v, nil
		}
		v, err := ts.typeHasPointers(m.Type)
		if err != nil {
			return false, err
		}
		if v {
			ts.fixup[t] = true
			return true, nil
		}
	}
	return false, nil
}

// typeHasPointers is 0x008CCC50: a registered type asks NeedsFixup; char*, asset, Key, StringHash,
// nullable and cLocalizedAssetString say yes; every other built-in type says no.
func (ts *Types) typeHasPointers(name string) (bool, error) {
	if t := ts.Type(name); t != nil {
		return ts.NeedsFixup(t)
	}
	return pointerTypes[name], nil
}

// primSizes are the built-in value types: size and whether they read signed.
var primSizes = map[string]struct {
	size   int
	signed bool
}{
	"float": {4, false}, "bool": {1, false}, "int": {4, true}, "int32_t": {4, true}, "uint32_t": {4, false},
	"uint8_t": {1, false}, "uint16_t": {2, false}, "uint64_t": {8, false}, "int64_t": {8, true}, "enum": {4, true},
	"bitfield": {4, false}, "tObjID": {4, false}, "guid": {4, false},
}

// vectorSizes are the float vector types' float counts.
var vectorSizes = map[string]int{"cSPVector2": 2, "cSPVector3": 3, "cSPVector4": 4, "cSPQuaternion": 4}

// typeSize is a member type's size in its struct (assetdata.py's type_size).
func (ts *Types) typeSize(name string) int {
	if p, ok := primSizes[name]; ok {
		return p.size
	}
	if n, ok := vectorSizes[name]; ok {
		return 4 * n
	}
	switch name {
	case "char*", "asset", "Key", "StringHash", "nullable":
		return 4
	case "array":
		return 8
	case "char":
		return 1
	}
	if t := ts.Type(name); t != nil {
		return t.Size
	}
	return 4
}

// memberSpan is the bytes [lo, hi) of its struct a member occupies (assetdata.py's member_span).
func (ts *Types) memberSpan(m Member) (int, int) {
	o := m.Offset
	switch m.Type {
	case "Key":
		if m.Delta < 0 {
			return o + m.Delta, o + 4
		}
		return o, o + 16
	case "StringHash":
		if m.Delta < 0 {
			return o + m.Delta, o + 4
		}
		return o, o + 8
	case "cLocalizedAssetString":
		return o, o + 20
	case "char":
		return o, o + m.Count
	}
	return o, o + ts.typeSize(m.Type)*m.Count
}
