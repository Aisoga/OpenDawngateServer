package tdfcheck

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"dawngate/server/internal/tdf"
)

func TestDumpParses(t *testing.T) {
	ts := Must(t)
	if len(ts.ByAddr) < 400 {
		t.Errorf("only %d tables", len(ts.ByAddr))
	}
	for _, class := range []string{"Blaze::Util::PingResponse", "PingResponse", "012FCAC4", "0x012FCAC4"} {
		tab, err := ts.Lookup(class)
		if err != nil {
			t.Fatal(err)
		}
		if tab.Addr != 0x012FCAC4 || tab.Name != "Blaze::Util::PingResponse" {
			t.Errorf("Lookup(%q) = %08X %q", class, tab.Addr, tab.Name)
		}
		m, ok := tab.Member("STIM")
		if !ok || m.Type != "uint32" || m.Code != 0x15 || m.Wire != tdf.TypeInt || m.Offset != 8 {
			t.Errorf("STIM = %+v", m)
		}
	}
	if _, err := ts.Lookup("NoSuchClass"); err == nil {
		t.Error("no error for an unknown class")
	}
}

// The nested links behind preAuth's reply, as BLAZE.md describes them.
func TestDumpLinksPreAuthResponse(t *testing.T) {
	ts := Must(t)
	pre, err := ts.Lookup("Blaze::Util::PreAuthResponse")
	if err != nil {
		t.Fatal(err)
	}
	member := func(tab *Table, label string) *Member {
		t.Helper()
		m, ok := tab.Member(label)
		if !ok {
			t.Fatalf("%s has no %s", tab, label)
		}
		return m
	}
	if m := member(pre, "CIDS"); m.Wire != tdf.TypeList || len(m.Elems) != 1 || m.Elems[0] != tdf.TypeInt {
		t.Errorf("CIDS = %+v", m)
	}
	conf := member(pre, "CONF")
	if conf.Link == nil || conf.Link.Name != "Blaze::Util::FetchConfigResponse" {
		t.Fatalf("CONF links to %v", conf.Link)
	}
	if m := member(conf.Link, "CONF"); !m.Ptr || m.Wire != tdf.TypeMap || len(m.Elems) != 2 ||
		m.Elems[0] != tdf.TypeString || m.Elems[1] != tdf.TypeString {
		t.Errorf("FetchConfigResponse.CONF = %+v", m)
	}
	qoss := member(pre, "QOSS")
	if qoss.Link == nil || qoss.Link.Name != "Blaze::QosConfigInfo" {
		t.Fatalf("QOSS links to %v", qoss.Link)
	}
	ltps := member(qoss.Link, "LTPS")
	if ltps.Link == nil || ltps.Link.Name != "Blaze::QosPingSiteInfo" || len(ltps.Elems) != 2 || ltps.Elems[1] != tdf.TypeStruct {
		t.Errorf("LTPS = %+v", ltps)
	}
	if m := member(pre, "EEFA"); m.Default != "0x1" || m.Type != "bool" {
		t.Errorf("EEFA = %+v", m)
	}
}

func pingSite(host string) *tdf.Writer {
	return tdf.NewWriter().String("PSA", host).Int("PSP", 17502).String("SNA", "local")
}

// A preAuth reply in the client's layout, with knobs to break it.
func preAuth(edit func(w *tdf.Writer, at string)) []byte {
	if edit == nil {
		edit = func(*tdf.Writer, string) {}
	}
	w := tdf.NewWriter()
	w.String("ASRC", "303107")
	w.List("CIDS", tdf.TypeInt, []int{1, 4, 9})
	w.Struct("CONF", func(w *tdf.Writer) {
		w.Map("CONF", tdf.TypeString, tdf.TypeString, tdf.KV("pingPeriod", "20s"))
		edit(w, "CONF.")
	})
	w.String("INST", "battlegrounds-1-pc")
	w.Struct("QOSS", func(w *tdf.Writer) {
		w.Struct("BWPS", func(w *tdf.Writer) {
			w.String("PSA", "127.0.0.1").Int("PSP", 17502).String("SNA", "local")
			edit(w, "QOSS.BWPS.")
		})
		w.Int("LNP", 10)
		w.Map("LTPS", tdf.TypeString, tdf.TypeStruct, tdf.KV("local", pingSite("127.0.0.1")))
		w.Int("SVID", 0x45410805)
	})
	edit(w, "")
	w.String("SVER", "Blaze 3.19.11.0")
	return w.Bytes()
}

func TestCheckAcceptsTheClientLayout(t *testing.T) {
	ts := Must(t)
	if err := ts.Check("Blaze::Util::PreAuthResponse", preAuth(nil)); err != nil {
		t.Error(err)
	}
	if err := ts.Check("Blaze::Util::PingResponse", tdf.NewWriter().Int("STIM", 1791405059).Bytes()); err != nil {
		t.Error(err)
	}
	if err := ts.Check("Blaze::Util::PingResponse", nil); err != nil {
		t.Errorf("empty payload (all defaults): %v", err)
	}
}

// Deliberately wrong replies: each must fail with the expected problem.
func TestCheckFindsProblems(t *testing.T) {
	ts := Must(t)
	nul := tdf.EncodeLabel("STIM")
	cases := []struct {
		name, class string
		payload     []byte
		path, msg   string
	}{
		{"unknown tag", "PingResponse", tdf.NewWriter().Int("STIM", 1).Int("XTRA", 2).Bytes(),
			"XTRA", "isn't a member of Blaze::Util::PingResponse"},
		{"wrong wire type", "PingResponse", tdf.NewWriter().String("STIM", "1").Bytes(),
			"STIM", "wire type string, but the client decodes STIM (uint32) as int"},
		{"out of order", "PreAuthResponse", tdf.NewWriter().String("SVER", "x").String("ASRC", "y").Bytes(),
			"ASRC", "out of tag order (after SVER)"},
		{"twice", "PingResponse", tdf.NewWriter().Int("STIM", 1).Int("STIM", 2).Bytes(),
			"STIM", "appears twice"},
		{"nested wire type", "PreAuthResponse", preAuth(func(w *tdf.Writer, at string) {
			if at == "QOSS.BWPS." {
				w.String("ZZZ", "x") // after SNA, but not a member
			}
		}), "QOSS.BWPS.ZZZ", "isn't a member of Blaze::QosPingSiteInfo"},
		{"nested in pointer member's class", "PreAuthResponse", preAuth(func(w *tdf.Writer, at string) {
			if at == "CONF." {
				w.Int("CONG", 1)
			}
		}), "CONF.CONG", "isn't a member of Blaze::Util::FetchConfigResponse"},
		{"list element type", "PreAuthResponse", tdf.NewWriter().List("CIDS", tdf.TypeString, []string{"1"}).Bytes(),
			"CIDS", "list of string, but the client's list holds int"},
		{"map value type", "QosConfigInfo", tdf.NewWriter().Map("LTPS", tdf.TypeString, tdf.TypeString, tdf.KV("a", "b")).Bytes(),
			"LTPS", "map<string,string>, but the client's map is map<string,struct>"},
		{"struct in a map", "QosConfigInfo", tdf.NewWriter().Map("LTPS", tdf.TypeString, tdf.TypeStruct,
			tdf.KV("local", tdf.NewWriter().Int("PSA", 1))).Bytes(),
			"LTPS[local].PSA", "wire type int, but the client decodes PSA (string) as string"},
		{"struct in a list", "Blaze::Authentication::LoginResponse", tdf.NewWriter().List("PLST", tdf.TypeStruct,
			[]*tdf.Writer{tdf.NewWriter().String("DSNM", "x"), tdf.NewWriter().String("NAME", "y")}).Bytes(),
			"PLST[1].NAME", "isn't a member of Blaze::Authentication::PersonaDetails"},
		{"no terminator", "PreAuthResponse", tdf.NewWriter().Struct("CONF", nil).Bytes()[:4],
			"CONF", "struct has no 0x00 terminator"},
		{"unknown wire type", "PingResponse", []byte{nul[0], nul[1], nul[2], 12, 0},
			"STIM", "wire type 12"},
		{"top-level zero", "PingResponse", []byte{0}, "(payload)", "0x00 at the top level"},
		{"truncated", "PingResponse", []byte{nul[0], nul[1], nul[2], 0, 0x80}, "STIM", "payload ends inside the value"},
	}
	for _, c := range cases {
		err := ts.Check(c.class, c.payload)
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%s: no problems found (err %v)", c.name, err)
			continue
		}
		found := false
		for _, p := range e.Problems {
			if p.Path == c.path && strings.Contains(p.Msg, c.msg) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %s: ...%s..., got\n%v", c.name, c.path, c.msg, err)
		}
	}
}

// The redirector's ADDR union, as the dump recovers it from the client's union code.
func TestDumpUnion(t *testing.T) {
	ts := Must(t)
	info, err := ts.Lookup("Blaze::Redirector::ServerInstanceInfo")
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := info.Member("ADDR")
	if addr == nil || addr.Wire != tdf.TypeUnion || addr.Union == nil || len(addr.Union.Members) != 2 {
		t.Fatalf("ADDR = %+v", addr)
	}
	m0 := addr.Union.Members[0]
	if m0.Label != "VALU" || m0.Wire != tdf.TypeStruct || m0.Link == nil || m0.Link.Name != "Blaze::Redirector::IpAddress" {
		t.Errorf("ADDR member 0 = %+v", m0)
	}
}

func serverInstance(member byte, value func(w *tdf.Writer)) []byte {
	return tdf.NewWriter().Union("ADDR", member, value).Int("SECU", 0).Bytes()
}

func TestCheckUnions(t *testing.T) {
	ts := Must(t)
	const class = "Blaze::Redirector::ServerInstanceInfo"
	ip := func(w *tdf.Writer) {
		w.Struct("VALU", func(w *tdf.Writer) { w.String("HOST", "127.0.0.1").Int("IP", 0x7F000001).Int("PORT", 10041) })
	}
	if err := ts.Check(class, serverInstance(0, ip)); err != nil {
		t.Error(err)
	}
	if err := ts.Check(class, serverInstance(tdf.UnionUnset, nil)); err != nil {
		t.Error(err)
	}
	for _, c := range []struct {
		name      string
		payload   []byte
		path, msg string
	}{
		{"index past the end", serverInstance(5, ip), "ADDR", "union member 5, but union 012FDF28 has 2 members"},
		{"other tag", serverInstance(0, func(w *tdf.Writer) { w.Struct("ADDR", nil) }), "ADDR.ADDR",
			"union member 0's field is VALU, not ADDR"},
		{"other wire type", serverInstance(0, func(w *tdf.Writer) { w.Int("VALU", 1) }), "ADDR.VALU",
			"wire type int, but union 012FDF28 member 0 is struct"},
		{"inside the member", serverInstance(0, func(w *tdf.Writer) {
			w.Struct("VALU", func(w *tdf.Writer) { w.Int("HOST", 1) })
		}), "ADDR.VALU.HOST", "the client decodes HOST (string) as string"},
	} {
		err := ts.Check(class, c.payload)
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%s: no problems found (err %v)", c.name, err)
			continue
		}
		found := false
		for _, p := range e.Problems {
			found = found || p.Path == c.path && strings.Contains(p.Msg, c.msg)
		}
		if !found {
			t.Errorf("%s: want %s: ...%s..., got\n%v", c.name, c.path, c.msg, err)
		}
	}
}

// A list of unions (ReplicatedGameData.HNET, NetworkAddress) as the client writes it (b8_run4): member
// byte, then the member's struct without a VALU header.
func TestCheckListUnions(t *testing.T) {
	ts := Must(t)
	const class = "Blaze::GameManager::ReplicatedGameData"
	ip := func(port int64) *tdf.Writer { return tdf.NewWriter().Int("IP", 0x7F000001).Int("PORT", port) }
	pair := tdf.NewWriter().
		Struct("EXIP", func(w *tdf.Writer) { w.Int("IP", 0x7F000001).Int("PORT", 10042) }).
		Struct("INIP", func(w *tdf.Writer) { w.Int("IP", 0x7F000001).Int("PORT", 10042) })
	hnet := func(items ...tdf.UnionItem) []byte {
		return tdf.NewWriter().Int("GID", 1).List("HNET", tdf.TypeStruct, items).Int("NTOP", 1).Bytes()
	}
	for name, p := range map[string][]byte{
		"IpPairAddress": hnet(tdf.UnionItem{Member: 2, Body: pair}),
		"IpAddress":     hnet(tdf.UnionItem{Member: 3, Body: ip(10042)}),
		"unset":         hnet(tdf.UnionItem{Member: tdf.UnionUnset}),
		"two":           hnet(tdf.UnionItem{Member: 3, Body: ip(1)}, tdf.UnionItem{Member: 2, Body: pair}),
	} {
		if err := ts.Check(class, p); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, c := range []struct {
		name      string
		payload   []byte
		path, msg string
	}{
		{"index past the end", hnet(tdf.UnionItem{Member: 5, Body: pair}), "HNET[0]", "union member 5, but union 012F22E8 has 5 members"},
		{"other member's layout", hnet(tdf.UnionItem{Member: 3, Body: pair}), "HNET[0]<3>.EXIP", "EXIP isn't a member of Blaze::IpAddress"},
		{"inside the member", hnet(tdf.UnionItem{Member: 2, Body: tdf.NewWriter().Int("EXIP", 1)}), "HNET[0]<2>.EXIP",
			"the client decodes EXIP (struct) as struct"},
	} {
		err := ts.Check(class, c.payload)
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%s: no problems found (err %v)", c.name, err)
			continue
		}
		found := false
		for _, p := range e.Problems {
			found = found || p.Path == c.path && strings.Contains(p.Msg, c.msg)
		}
		if !found {
			t.Errorf("%s: want %s: ...%s..., got\n%v", c.name, c.path, c.msg, err)
		}
	}
}

// fill writes every member of t with a zero value of its wire type, one element into lists and maps
// of linked structs, member 0 into known unions, recursing into linked classes.
func fill(w *tdf.Writer, t *Table, depth int) {
	for _, m := range t.Members {
		writeValue(w, m.Label, m.Wire, &m, depth)
	}
}

func writeValue(w *tdf.Writer, label string, wire tdf.Type, m *Member, depth int) {
	sub := func(link *Table) *tdf.Writer {
		e := tdf.NewWriter()
		if link != nil && depth < 6 {
			fill(e, link, depth+1)
		}
		return e
	}
	switch wire {
	case tdf.TypeStruct:
		w.Struct(label, func(w *tdf.Writer) {
			if m != nil && m.Link != nil && depth < 6 {
				fill(w, m.Link, depth+1)
			}
		})
	case tdf.TypeList:
		elem := tdf.TypeInt
		if m != nil && m.Elems != nil {
			elem = m.Elems[0]
		}
		if elem == tdf.TypeStruct && m.Link != nil {
			w.List(label, elem, []*tdf.Writer{sub(m.Link)})
		} else if elem == tdf.TypeStruct && m.Union != nil && len(m.Union.Members) > 0 {
			// a list of unions: one element holding the last member (a struct one for NetworkAddress)
			last := len(m.Union.Members) - 1
			um := m.Union.Members[last]
			if um.Wire == tdf.TypeStruct {
				w.List(label, elem, []tdf.UnionItem{{Member: byte(last), Body: sub(um.Link)}})
			} else {
				w.List(label, elem, []tdf.UnionItem{{Member: tdf.UnionUnset}})
			}
		} else {
			w.List(label, elem, []int{})
		}
	case tdf.TypeMap:
		k, v := tdf.TypeString, tdf.TypeString
		if m != nil && m.Elems != nil {
			k, v = m.Elems[0], m.Elems[1]
		}
		if k == tdf.TypeString && v == tdf.TypeStruct && m.Link != nil {
			w.Map(label, k, v, tdf.KV("key", sub(m.Link)))
		} else {
			w.Map(label, k, v)
		}
	case tdf.TypeUnion:
		if m == nil || m.Union == nil || len(m.Union.Members) == 0 {
			w.Union(label, tdf.UnionUnset, nil)
			return
		}
		um := m.Union.Members[0]
		w.Union(label, 0, func(w *tdf.Writer) { writeValue(w, um.Label, um.Wire, &Member{Link: um.Link}, depth+1) })
	case tdf.TypeString:
		w.String(label, "")
	case tdf.TypeBlob:
		w.Blob(label, nil)
	case tdf.TypeIntList: // the client's variable TDF: present byte 0
		w.Value(label, tdf.TypeIntList, tdf.IntList{})
	case tdf.TypeObjType:
		w.Value(label, wire, tdf.ObjType{})
	case tdf.TypeObjID:
		w.Value(label, wire, tdf.ObjID{})
	case tdf.TypeFloat:
		w.Value(label, wire, float32(0))
	default:
		w.Int(label, 0)
	}
}

// Every class in the dump accepts a payload with all its members in the dump's layout: the parser,
// the links and the checker agree.
func TestEveryClassAcceptsItsOwnLayout(t *testing.T) {
	ts := Must(t)
	for _, tab := range ts.ByAddr {
		w := tdf.NewWriter()
		fill(w, tab, 0)
		if err := ts.Check(fmt.Sprintf("%08X", tab.Addr), w.Bytes()); err != nil {
			t.Error(err)
		}
	}
}

// A string without its NUL is a problem of its own.
func TestCheckStringNUL(t *testing.T) {
	ts := Must(t)
	payload := tdf.NewWriter().String("ASRC", "x").Bytes()
	payload[len(payload)-1] = 'y' // "xy" without NUL
	err := ts.Check("PreAuthResponse", payload)
	if err == nil || !strings.Contains(err.Error(), "ASRC (byte 4): string without its NUL") {
		t.Errorf("got %v", err)
	}
}

const miniDump = `# code 0x0a struct wire 3 struct
# code 0x15 uint32 wire 0 int
# code 0x04 string wire 1 string
# code 0x01 list wire 4 list
# code 0x09 union wire 6 union

== table @ 00000010: A B  (Test::Outer)
   00000010  A     uint32       +0x0008
   0000001C  B     struct       +0x000c -> empty (Test::Nothing)

== table @ 00000030: C U  (Test::Other)
   00000030  C     list         +0x0008 list<string> -> 00000010 (Test::Outer)
   0000003C  U     union        +0x0010 -> union 00000100

== union @ 00000100: 2 members
   member 0  VALU  string
   member 1  VALU  struct -> 00000010 (Test::Outer)
`

func TestParseMiniDump(t *testing.T) {
	ts, err := Parse(strings.NewReader(strings.ReplaceAll(miniDump, "\n", "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	outer, err := ts.Lookup("Outer")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := outer.Member("B")
	if b.Link == nil || b.Link.Name != "Test::Nothing" || len(b.Link.Members) != 0 {
		t.Errorf("B links to %+v", b.Link)
	}
	// a member of the empty class is unknown
	err = ts.Check("Outer", tdf.NewWriter().Int("A", 1).Struct("B", func(w *tdf.Writer) { w.Int("X", 1) }).Bytes())
	if err == nil || !strings.Contains(err.Error(), "B.X (byte 9): X isn't a member of Test::Nothing") {
		t.Errorf("got %v", err)
	}
	// a union member that is a struct is checked against its class
	err = ts.Check("Other", tdf.NewWriter().Union("U", 1, func(w *tdf.Writer) {
		w.Struct("VALU", func(w *tdf.Writer) { w.String("A", "x") })
	}).Bytes())
	if err == nil || !strings.Contains(err.Error(), "U.VALU.A (byte 9): wire type string, but the client decodes A (uint32) as int") {
		t.Errorf("got %v", err)
	}
	if err := ts.Check("Other", tdf.NewWriter().Union("U", 0, func(w *tdf.Writer) { w.String("VALU", "x") }).Bytes()); err != nil {
		t.Error(err)
	}
}

func TestParseErrors(t *testing.T) {
	for name, dump := range map[string]string{
		"empty":         "",
		"dangling link": strings.Replace(miniDump, "-> 00000010", "-> 00000099", 1),
		"bad type":      strings.Replace(miniDump, "uint32       +0x0008", "uint99       +0x0008", 1),
		"bad element":   strings.Replace(miniDump, "list<string>", "list<strung>", 1),
		"order":         strings.Replace(miniDump, "  B     struct", "  0     struct", 1),
		"twice":         miniDump + "\n== table @ 00000010: A\n",
		"stray member":  "# code 0x15 uint32 wire 0 int\n   00000010  A     uint32       +0x0008\n",
		"junk token":    strings.Replace(miniDump, "+0x0008\n", "+0x0008 what\n", 1),
		"no union":      strings.Replace(miniDump, "-> union 00000100", "-> union 00000200", 1),
		"union index":   strings.Replace(miniDump, "member 1  VALU", "member 2  VALU", 1),
		"union wire":    strings.Replace(miniDump, "VALU  string", "VALU  strung", 1),
	} {
		if _, err := Parse(strings.NewReader(dump)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
