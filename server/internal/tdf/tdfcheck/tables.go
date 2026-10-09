// Package tdfcheck checks encoded TDF payloads against the client's own member tables, so a reply
// can't silently carry a member the client skips, a wrong wire type or a wrong order. It is a test
// helper (ADR-1: tests may read docs/data/); the server doesn't use it.
//
// The tables come from docs/data/tdf_member_tables.txt, which re/scripts/tdf_tables.py dumps from
// Dawngate.exe: every TDF class the client can build, each member's tag, type and the wire type the
// client's Heat2 decoder expects for it, the element wire types of lists and maps, the nested class
// of struct members and of struct elements, and the members of unions. See docs/BLAZE.md, "Reading
// reply layouts from the client".
//
// Use it in a test:
//
//	tdfcheck.Require(t, "Blaze::Util::PingResponse", PingReply(1791405059))
package tdfcheck

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"dawngate/server/internal/tdf"
)

// Member is one member entry of a class.
type Member struct {
	Addr   uint32 // the entry's address in Dawngate.exe
	Label  string
	Tag    uint32 // packed label (3 bytes), the order key
	Type   string // the client's type name ("uint32", "struct", ...)
	Code   int    // the client's type code
	Wire   tdf.Type
	Offset int
	Ptr    bool // the object holds a pointer to the member

	// Elems are the wire types of a list's elements ([elem]) or of a map's keys and values
	// ([key, value]); nil when the dump doesn't know them.
	Elems []tdf.Type
	// Link is the nested class: of a struct member, or of the struct elements of a list or map
	// (the map's values). Nil when the dump doesn't link one.
	Link *Table
	// Union is the union class of a union member (or of a list's elements); nil if unknown.
	Union *Union

	Default string // the default as the dump prints it ("0x1", "\"default\""), "" if none

	ref ref // the link as the dump writes it; Parse resolves it into Link or Union
}

// Table is one class's member table, members in tag order.
type Table struct {
	Addr    uint32 // address of the first entry; 0 for a linked class without members
	Name    string // qualified class name ("Blaze::Util::PingResponse"), "" if unknown
	Members []Member
	byLabel map[string]int
}

// Member returns the member with this label.
func (t *Table) Member(label string) (*Member, bool) {
	i, ok := t.byLabel[label]
	if !ok {
		return nil, false
	}
	return &t.Members[i], true
}

// String names the table: its class name if known, else its address.
func (t *Table) String() string {
	switch {
	case t.Name != "":
		return t.Name
	case t.Addr == 0:
		return "(class without members)"
	}
	return fmt.Sprintf("table %08X", t.Addr)
}

// Union is a union class: the member byte on the wire selects one of Members, which follows as one
// field (tag VALU).
type Union struct {
	Addr    uint32 // the union class's vtable in Dawngate.exe
	Members []UnionMember
}

func (u *Union) String() string { return fmt.Sprintf("union %08X", u.Addr) }

// UnionMember is one member of a union.
type UnionMember struct {
	Label string // the field's tag, VALU
	Wire  tdf.Type
	Link  *Table // a struct member's class, nil if unknown
	ref   ref
}

// ref is a link as the dump writes it: "-> 012F1BA0 (Class)", "-> empty (Class)", "-> union 012F22E8".
type ref struct {
	kind string // "", "table", "empty", "union"
	addr uint32
	name string
}

// Tables is a parsed dump.
type Tables struct {
	ByAddr  map[uint32]*Table
	Unions  map[uint32]*Union
	byName  map[string][]*Table // qualified and short names
	codes   map[string]code     // type name -> code and wire type
	wireNum map[string]tdf.Type // wire type name -> wire type
	empty   map[string]*Table   // classes without members, by name
}

type code struct {
	code int
	wire tdf.Type
}

// DumpPath is docs/data/tdf_member_tables.txt, found relative to this source file.
func DumpPath() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "docs", "data", "tdf_member_tables.txt")
}

var (
	loadOnce sync.Once
	loaded   *Tables
	loadErr  error
)

// Load parses the dump at DumpPath once; later calls return the same tables.
func Load() (*Tables, error) {
	loadOnce.Do(func() {
		f, err := os.Open(DumpPath())
		if err != nil {
			loadErr = err
			return
		}
		defer f.Close()
		loaded, loadErr = Parse(f)
		if loadErr != nil {
			loadErr = fmt.Errorf("%s: %w", DumpPath(), loadErr)
		}
	})
	return loaded, loadErr
}

// Must is Load for tests: it stops the test if the dump can't be read.
func Must(t testing.TB) *Tables {
	t.Helper()
	ts, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// Lookup finds a table by qualified class name ("Blaze::Util::PingResponse"), short name
// ("PingResponse", if only one class has it) or address ("012FCAC4").
func (ts *Tables) Lookup(class string) (*Table, error) {
	if c := ts.byName[class]; len(c) == 1 {
		return c[0], nil
	} else if len(c) > 1 {
		names := make([]string, len(c))
		for i, t := range c {
			names[i] = t.Name
		}
		return nil, fmt.Errorf("tdfcheck: %q is ambiguous: %s", class, strings.Join(names, ", "))
	}
	if a, err := strconv.ParseUint(strings.TrimPrefix(class, "0x"), 16, 32); err == nil {
		if t, ok := ts.ByAddr[uint32(a)]; ok {
			return t, nil
		}
	}
	return nil, fmt.Errorf("tdfcheck: no member table for %q in %s", class, filepath.Base(DumpPath()))
}

// Parse reads a dump written by re/scripts/tdf_tables.py.
func Parse(r io.Reader) (*Tables, error) {
	ts := &Tables{ByAddr: map[uint32]*Table{}, Unions: map[uint32]*Union{}, byName: map[string][]*Table{},
		codes: map[string]code{}, wireNum: map[string]tdf.Type{}, empty: map[string]*Table{}}
	var table *Table
	var union *Union
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		var err error
		switch {
		case strings.HasPrefix(line, "# code "):
			err = ts.parseCode(line)
		case strings.HasPrefix(line, "== table @ "):
			table, union = nil, nil
			table, err = ts.parseTableHeader(line)
		case strings.HasPrefix(line, "== union @ "):
			table, union = nil, nil
			union, err = ts.parseUnionHeader(line)
		case strings.HasPrefix(line, "   ") && strings.TrimSpace(line) != "":
			switch {
			case table != nil:
				err = ts.parseMember(table, line)
			case union != nil:
				err = ts.parseUnionMember(union, line)
			default:
				err = fmt.Errorf("member line outside a table")
			}
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(ts.codes) == 0 || len(ts.ByAddr) == 0 {
		return nil, fmt.Errorf("no type codes or no tables (not a tdf_tables.py dump?)")
	}
	for _, t := range ts.ByAddr {
		for i := range t.Members {
			m := &t.Members[i]
			var err error
			if m.Link, m.Union, err = ts.resolve(m.ref); err != nil {
				return nil, fmt.Errorf("%s.%s: %w", t, m.Label, err)
			}
		}
	}
	for _, u := range ts.Unions {
		for i := range u.Members {
			m := &u.Members[i]
			var err error
			var inner *Union
			if m.Link, inner, err = ts.resolve(m.ref); err != nil || inner != nil {
				return nil, fmt.Errorf("%s member %d: bad link (%v)", u, i, err)
			}
		}
	}
	return ts, nil
}

func (ts *Tables) resolve(r ref) (*Table, *Union, error) {
	switch r.kind {
	case "table":
		if t := ts.ByAddr[r.addr]; t != nil {
			return t, nil, nil
		}
		return nil, nil, fmt.Errorf("links to %08X, which isn't a table", r.addr)
	case "union":
		if u := ts.Unions[r.addr]; u != nil {
			return nil, u, nil
		}
		return nil, nil, fmt.Errorf("links to union %08X, which isn't in the dump", r.addr)
	case "empty": // one shared table per class without members
		if ts.empty[r.name] == nil {
			ts.empty[r.name] = &Table{Name: r.name, byLabel: map[string]int{}}
		}
		return ts.empty[r.name], nil, nil
	}
	return nil, nil, nil
}

// "# code 0x15 uint32 wire 0 int"
func (ts *Tables) parseCode(line string) error {
	f := strings.Fields(line)
	if len(f) != 7 || f[4] != "wire" {
		return fmt.Errorf("bad type code line %q", line)
	}
	c, err1 := strconv.ParseInt(f[2], 0, 32)
	w, err2 := strconv.Atoi(f[5])
	if err1 != nil || err2 != nil {
		return fmt.Errorf("bad type code line %q", line)
	}
	ts.codes[f[3]] = code{int(c), tdf.Type(w)}
	ts.wireNum[f[6]] = tdf.Type(w)
	return nil
}

// "== table @ 012FCAC4: STIM  (Blaze::Util::PingResponse)"
func (ts *Tables) parseTableHeader(line string) (*Table, error) {
	rest := strings.TrimPrefix(line, "== table @ ")
	addr, rest, ok := strings.Cut(rest, ":")
	a, err := strconv.ParseUint(addr, 16, 32)
	if !ok || err != nil {
		return nil, fmt.Errorf("bad table header %q", line)
	}
	t := &Table{Addr: uint32(a), byLabel: map[string]int{}}
	if i := strings.LastIndex(rest, "  ("); i >= 0 && strings.HasSuffix(rest, ")") {
		t.Name = rest[i+3 : len(rest)-1]
	}
	if ts.ByAddr[t.Addr] != nil {
		return nil, fmt.Errorf("table %08X twice", t.Addr)
	}
	ts.ByAddr[t.Addr] = t
	if t.Name != "" {
		ts.byName[t.Name] = append(ts.byName[t.Name], t)
		if i := strings.LastIndex(t.Name, "::"); i >= 0 {
			short := t.Name[i+2:]
			ts.byName[short] = append(ts.byName[short], t)
		}
	}
	return t, nil
}

// "== union @ 012F22E8: 5 members"
func (ts *Tables) parseUnionHeader(line string) (*Union, error) {
	addr, _, ok := strings.Cut(strings.TrimPrefix(line, "== union @ "), ":")
	a, err := strconv.ParseUint(addr, 16, 32)
	if !ok || err != nil || ts.Unions[uint32(a)] != nil {
		return nil, fmt.Errorf("bad or repeated union header %q", line)
	}
	u := &Union{Addr: uint32(a)}
	ts.Unions[u.Addr] = u
	return u, nil
}

// "   member 3  VALU  struct -> 012F1ADC (Blaze::IpAddress)"
func (ts *Tables) parseUnionMember(u *Union, line string) error {
	f := strings.Fields(line)
	if len(f) < 4 || f[0] != "member" || f[1] != strconv.Itoa(len(u.Members)) {
		return fmt.Errorf("bad union member line %q", line)
	}
	w, ok := ts.wireNum[f[3]]
	if !ok {
		return fmt.Errorf("unknown wire type %q in %q", f[3], line)
	}
	m := UnionMember{Label: f[2], Wire: w}
	if rest := f[4:]; len(rest) > 0 {
		var err error
		if m.ref, rest, err = parseRef(rest); err != nil || len(rest) > 0 {
			return fmt.Errorf("bad link in %q", line)
		}
	}
	u.Members = append(u.Members, m)
	return nil
}

// parseRef reads "-> 012F1BA0 (Class)", "-> empty (Class)" or "-> union 012F22E8" from f.
func parseRef(f []string) (ref, []string, error) {
	if len(f) < 2 || f[0] != "->" {
		return ref{}, f, fmt.Errorf("no link")
	}
	var r ref
	f = f[1:]
	switch f[0] {
	case "empty":
		r.kind, f = "empty", f[1:]
	case "union":
		if len(f) < 2 {
			return ref{}, f, fmt.Errorf("union without address")
		}
		a, err := strconv.ParseUint(f[1], 16, 32)
		if err != nil {
			return ref{}, f, err
		}
		r.kind, r.addr, f = "union", uint32(a), f[2:]
	default:
		a, err := strconv.ParseUint(f[0], 16, 32)
		if err != nil {
			return ref{}, f, err
		}
		r.kind, r.addr, f = "table", uint32(a), f[1:]
	}
	if len(f) > 0 && strings.HasPrefix(f[0], "(") && strings.HasSuffix(f[0], ")") {
		r.name, f = f[0][1:len(f[0])-1], f[1:]
	}
	return r, f, nil
}

// "   012FCC04  CONF  struct       +0x0048 -> 012FC9F8 (Blaze::Util::FetchConfigResponse)"
// "   012FC9F8  CONF  map          +0x002c ptr map<string,string>"
// "   012F1BD8  LNP   uint16       +0x0008 default=0xa"
func (ts *Tables) parseMember(t *Table, line string) error {
	body, dflt, _ := strings.Cut(line, " default=")
	f := strings.Fields(body)
	if len(f) < 4 || !strings.HasPrefix(f[3], "+") {
		return fmt.Errorf("bad member line %q", line)
	}
	a, err1 := strconv.ParseUint(f[0], 16, 32)
	off, err2 := strconv.ParseInt(f[3][1:], 0, 32)
	c, ok := ts.codes[f[2]]
	if err1 != nil || err2 != nil || !ok {
		return fmt.Errorf("bad member line %q (unknown type %q?)", line, f[2])
	}
	m := Member{Addr: uint32(a), Label: f[1], Tag: packTag(f[1]), Type: f[2], Code: c.code, Wire: c.wire,
		Offset: int(off), Default: dflt}
	rest := f[4:]
	if len(rest) > 0 && rest[0] == "ptr" {
		m.Ptr, rest = true, rest[1:]
	}
	if len(rest) > 0 && strings.HasSuffix(rest[0], ">") && (strings.HasPrefix(rest[0], "list<") || strings.HasPrefix(rest[0], "map<")) {
		tok := rest[0]
		for _, name := range strings.Split(tok[strings.Index(tok, "<")+1:len(tok)-1], ",") {
			w, ok := ts.wireNum[name]
			if !ok {
				return fmt.Errorf("unknown element type %q in %q", name, line)
			}
			m.Elems = append(m.Elems, w)
		}
		if want := map[tdf.Type]int{tdf.TypeList: 1, tdf.TypeMap: 2}[m.Wire]; len(m.Elems) != want {
			return fmt.Errorf("%s with %d element types in %q", m.Type, len(m.Elems), line)
		}
		rest = rest[1:]
	}
	if len(rest) > 0 {
		var err error
		if m.ref, rest, err = parseRef(rest); err != nil {
			return fmt.Errorf("unexpected %q in %q", strings.Join(rest, " "), line)
		}
	}
	if len(rest) > 0 {
		return fmt.Errorf("unexpected %q in %q", strings.Join(rest, " "), line)
	}
	if len(t.Members) > 0 && m.Tag <= t.Members[len(t.Members)-1].Tag {
		return fmt.Errorf("%s: %s doesn't follow %s in tag order", t, m.Label, t.Members[len(t.Members)-1].Label)
	}
	t.byLabel[m.Label] = len(t.Members)
	t.Members = append(t.Members, m)
	return nil
}

// packTag is a label's 3 wire bytes as a number: the order the client's tables and decoder use.
func packTag(label string) uint32 {
	b := tdf.EncodeLabel(label)
	return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
}
