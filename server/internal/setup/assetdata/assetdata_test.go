package assetdata

import (
	"bytes"
	"encoding/binary"
	"flag"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dawngate/server/internal/setup/dbpf"
	"dawngate/server/internal/setup/pytext"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

var update = flag.Bool("update", false, "copy docs/data/assetdata_types.txt over the embedded assetdata_types.txt")

// The embedded type tables are docs/data/assetdata_types.txt (line ends aside: git may check either out
// with CRLF).
func TestTypesInSync(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	docs := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "docs", "data", "assetdata_types.txt")
	want, err := os.ReadFile(docs)
	if err != nil {
		t.Skipf("no %s: %v", docs, err)
	}
	if *update {
		if err := os.WriteFile(filepath.Join(filepath.Dir(file), "assetdata_types.txt"), want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	norm := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
	if !bytes.Equal(norm(want), norm(EmbeddedText())) {
		t.Fatal("server/internal/setup/assetdata/assetdata_types.txt differs from docs/data/assetdata_types.txt: " +
			"go test ./internal/setup/assetdata -run TestTypesInSync -update")
	}
	ts, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ts.Names()); n != 190 {
		t.Errorf("%d types, want 190", n)
	}
	ci := ts.Type("collectionitem")
	if ci == nil || ci.Size != 0x78 || len(ci.Members) != 11 || ci.CRC != 0x181fb6c7 || ci.Hooks["postload"] != 0x009a3f00 {
		t.Errorf("CollectionItem: %+v", ci)
	}
	if m := ci.Members[4]; m.Name != "longName" || m.Type != "Key" || m.Offset != 0x1c || m.Delta != -12 {
		t.Errorf("CollectionItem.longName: %+v", m)
	}
	if m := ci.Members[10]; m.Type != "array" || m.Elem != "asset" || m.ESize != 4 || m.Delta != 4 {
		t.Errorf("CollectionItem.tag: %+v", m)
	}
	if e, ok := ts.Enum(0x01412d00); !ok || e.Kind != "enum" || e.Values[0] != (EnumValue{-1, "unknown"}) || e.Values[2].Name != "shaper skin" {
		t.Errorf("enum 0x01412d00: %+v", e)
	}
}

// NeedsFixup caches its answers like assetdata.py, so its answer could depend on the order of the first
// questions if types referred to each other in a cycle. For the embedded tables every order gives the
// same answers (forward, backward, each type asked first), so reading only the classes content needs
// gives what reading every class does.
func TestNeedsFixupOrderFree(t *testing.T) {
	ask := func(order []string) map[string]bool {
		ts, _ := Embedded()
		out := map[string]bool{}
		for _, n := range order {
			v, err := ts.NeedsFixup(ts.Type(n))
			if err != nil {
				t.Fatal(err)
			}
			out[n] = v
		}
		return out
	}
	ts, _ := Embedded()
	names := ts.Names()
	fwd := ask(names)
	rev := make([]string, len(names))
	for i, n := range names {
		rev[len(names)-1-i] = n
	}
	back := ask(rev)
	yes := 0
	for _, n := range names {
		alone := ask([]string{n})[n]
		if fwd[n] != back[n] || fwd[n] != alone {
			t.Errorf("%s: %v forward, %v backward, %v first", n, fwd[n], back[n], alone)
		}
		if fwd[n] {
			yes++
		}
	}
	if !fwd["LootBagDrop"] || fwd["cSPBoundingBox"] {
		t.Errorf("LootBagDrop %v", fwd["LootBagDrop"])
	}
	t.Logf("%d of %d types need the fixup walk", yes, len(names))
}

const synthTypes = `# a synthetic table in assetdata_types.txt's form
== Elem size=0x8 members=2 table=0x1 registered=0x2 crc=0x00000001
   0 +0x0 n uint32_t
   1 +0x4 s char*

== Inner size=0x8 members=2 table=0x1 registered=0x2 crc=0x00000002
   0 +0x0 x uint16_t
   1 +0x4 s asset

== Thing size=0xa8 members=26 table=0x1 registered=0x2 crc=0x00000003 postload=0x10
   0 +0x0 f float
   1 +0x4 b bool
   2 +0x5 b2 bool
   3 +0x6 u8 uint8_t
   4 +0x8 i int
   5 +0xc e enum enum=0x100
   6 +0x10 e2 enum enum=0x100
   7 +0x14 bits bitfield enum=0x200
   8 +0x18 id tObjID
   9 +0x1c name char x8
   10 +0x24 v cSPVector3
   11 +0x30 str char*
   12 +0x34 none asset
   13 +0x44 loc Key delta=-12
   14 +0x48 plain Key delta=4
   15 +0x58 bad Key delta=4
   16 +0x68 sh StringHash delta=4
   17 +0x74 shbad StringHash delta=-4
   18 +0x78 arr array elem=Elem esize=0x8 delta=4
   19 +0x80 nums array elem=uint32_t esize=0x4 delta=4
   20 +0x88 strs array elem=char* esize=0x4 delta=4
   21 +0x90 opt nullable elem=Elem esize=0x8
   22 +0x94 inner Inner
   23 +0x9c empty array elem=Elem esize=0x8 delta=4
   24 +0xa4 nul nullable elem=Elem esize=0x8

== NoFix size=0xc members=2 table=0x1 registered=0x2 crc=0x00000004
   0 +0x0 a array elem=uint32_t esize=0x4 delta=4
   1 +0x8 s char*

== Holder size=0x8 members=1 table=0x1 registered=0x2 crc=0x00000005
   0 +0x0 h array elem=NoFix esize=0xc delta=4

enum 0x100 enum 3: 0=zero | 1=one | 2=two
enum 0x200 bitfield 4: 0=none | 1=a | 2=b | 8=c
`

// thing writes a Thing file: the fixed part, then the out-of-line data in member order.
func thing() []byte {
	d := make([]byte, 0xa8)
	le := binary.LittleEndian
	le.PutUint32(d[0:], math.Float32bits(1.5))
	d[4], d[5], d[6], d[7] = 1, 2, 3, 0xAA // 0x07 is padding
	le.PutUint32(d[8:], uint32(0xFFFFFFFB))
	le.PutUint32(d[0xc:], 2)
	le.PutUint32(d[0x10:], 9)
	le.PutUint32(d[0x14:], 0x1B)
	le.PutUint32(d[0x18:], 0xDEADBEEF)
	copy(d[0x1c:], "abc\x00junk")
	le.PutUint32(d[0x24:], math.Float32bits(1.5))
	le.PutUint32(d[0x28:], math.Float32bits(0.1))
	le.PutUint32(d[0x2c:], math.Float32bits(-2))
	le.PutUint32(d[0x30:], 0x02000000)                       // str
	le.PutUint32(d[0x38:], 0x2a)                             // loc's hash part: instance,
	le.PutUint32(d[0x40:], pytext.FNV1([]byte("tbl")))       // type 0, group
	le.PutUint32(d[0x44:], 0x02000001)                       // loc
	le.PutUint32(d[0x48:], 0x02000002)                       // plain
	le.PutUint32(d[0x4c:], pytext.FNV1([]byte("ability_x"))) // its instance
	le.PutUint32(d[0x58:], 0x02000003)                       // bad
	le.PutUint32(d[0x5c:], 1)
	le.PutUint32(d[0x60:], 2)
	le.PutUint32(d[0x64:], 3)
	le.PutUint32(d[0x68:], 0x02000004) // sh
	le.PutUint32(d[0x6c:], pytext.FNV1([]byte("hi")))
	le.PutUint32(d[0x70:], 0x1234)     // shbad's hash
	le.PutUint32(d[0x74:], 0x02000005) // shbad
	le.PutUint32(d[0x7c:], 2)          // arr: count only (the pointer is ignored)
	le.PutUint32(d[0x80:], 0x02000006)
	le.PutUint32(d[0x84:], 3) // nums
	le.PutUint32(d[0x88:], 0x02000007)
	le.PutUint32(d[0x8c:], 2)          // strs
	le.PutUint32(d[0x90:], 0x02000008) // opt
	le.PutUint16(d[0x94:], 513)
	le.PutUint32(d[0x98:], 0x02000009) // inner.s
	le.PutUint32(d[0x9c:], 0x1234)     // empty: a pointer, count 0
	ool := []byte("hello\x00Tbl!0x0000002a\x00ability_x\x00Grp!Name.ext\x00Hi\x00Yo\x00")
	ool = le.AppendUint32(ool, 1)
	ool = le.AppendUint32(ool, 0x0200000a)
	ool = le.AppendUint32(ool, 2)
	ool = le.AppendUint32(ool, 0)
	ool = append(ool, "e0\x00"...)
	for _, v := range []uint32{7, 8, 9, 0x0200000b, 0x0200000c} {
		ool = le.AppendUint32(ool, v)
	}
	ool = append(ool, "s0\x00s1\x00"...)
	ool = le.AppendUint32(ool, 5)
	ool = le.AppendUint32(ool, 0x0200000d)
	ool = append(ool, "o\x00Inner.Asset\x00"...)
	return append(d, ool...)
}

const thingJSON = `{"f":1.5,"b":true,"b2":2,"u8":3,"i":-5,"e":"two","e2":9,"bits":["a","b","c","0x10"],"id":"0xdeadbeef",
"name":"abc","v":[1.5,0.1,-2.0],"str":"hello","none":null,"loc":{"ref":"Tbl!0x0000002a","text":"Forty-two"},
"plain":"ability_x","bad":{"ref":"Grp!Name.ext","instance":"0x00000001","type":"0x00000002","group":"0x00000003"},
"sh":"Hi","shbad":{"text":"Yo","hash":"0x00001234"},"arr":[{"n":1,"s":"e0"},{"n":2,"s":null}],"nums":[7,8,9],
"strs":["s0","s1"],"opt":{"n":5,"s":"o"},"inner":{"x":513,"s":"Inner.Asset"},"empty":[],"nul":null,"_unk":{"+0x4":"010203aa"}}`

func synthLocale(t *testing.T) *Locale {
	t.Helper()
	text := "0x0000002a Forty-two\r\n0x0000002b\tTabbed\n0xZZ bad\n0x0000002cno space\n0x0000002d\u3000Ideographic\u2028next"
	pkg, err := dbpf.Parse(testgame.Package(0, []testgame.PackageEntry{
		{Type: 0x02FAC0B6, Instance: uint64(pytext.FNV1([]byte("tbl"))), Data: testgame.RefPackLiterals([]byte(text)),
			Comp: dbpf.CompRefPack, USize: uint32(len(text))},
	}))
	if err != nil {
		t.Fatal(err)
	}
	loc, err := LoadLocale(pkg)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestLocale(t *testing.T) {
	loc := synthLocale(t)
	tbl := pytext.FNV1([]byte("tbl"))
	for id, want := range map[uint32]string{0x2a: "Forty-two", 0x2b: "Tabbed", 0x2d: "Ideographic"} {
		if got, ok := loc.Text(tbl, id); !ok || got != want {
			t.Errorf("%#x: %q %v", id, got, ok)
		}
	}
	if _, ok := loc.Text(tbl, 0x2c); ok {
		t.Error("a line without whitespace after the id")
	}
	if loc.Tables() != 1 {
		t.Error("tables")
	}
}

func TestParseSynthetic(t *testing.T) {
	ts, err := ParseTypes([]byte(strings.ReplaceAll(synthTypes, "\n", "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	obj, refs, err := ts.Parse(ts.Type("Thing"), thing(), synthLocale(t))
	if err != nil {
		t.Fatal(err)
	}
	if diffs := compareJSON([]byte(thingJSON), AppendJSON(nil, obj), 10); len(diffs) > 0 {
		t.Errorf("Thing:\n  %s\n%s", strings.Join(diffs, "\n  "), AppendJSON(nil, obj))
	}
	// assetdata.py's _badref for the same file
	if want := []BadRef{{"bad", "Grp!Name.ext", "hashes 00000001/00000002/00000003, expected 2f8b3bf4/35931eb6/338d0b1e"},
		{"shbad", "Yo", "hash 0x00001234 != FNV-1 0x587707fd"}}; len(refs) != 2 || refs[0] != want[0] || refs[1] != want[1] {
		t.Errorf("refs %q", refs)
	}
	// without a Locale the Locale key stays text
	obj, _, _ = ts.Parse(ts.Type("Thing"), thing(), nil)
	if v, _ := obj.Get("loc"); v != "Tbl!0x0000002a" {
		t.Errorf("loc without a Locale: %v", v)
	}

	// what doesn't parse: trailing bytes, a cut file, an unterminated string, elements the client skips
	for name, c := range map[string]struct {
		typ  string
		data []byte
		msg  string
	}{
		"trailing":     {"Thing", append(thing(), 0), "1 unread byte(s) after the out-of-line data"},
		"cut":          {"Thing", thing()[:0xa0], "read of 168 bytes at 0x0 past the end (0xa0)"},
		"unterminated": {"Thing", thing()[:0xa8+3], "unterminated string at 0xa8"},
		"skipped":      {"Holder", holder(), "h: NoFix elements carry out-of-line data the client skips"},
	} {
		_, _, err := ts.Parse(ts.Type(c.typ), c.data, nil)
		if err == nil || errFatal(err) || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: %v, want %q", name, err, c.msg)
		}
	}
	if fix, _ := ts.NeedsFixup(ts.Type("NoFix")); fix {
		t.Error("NoFix: the first array member decides (uint32_t: no)")
	}
}

// With DAWNGATE_P3A2_SYNTH_OUT set, the synthetic inputs go there for assetdata.py (thingJSON is its
// `--types synth_types.txt show <dir> b.Thing --locale synth_locale.package`).
func TestWriteSyntheticAssetData(t *testing.T) {
	dir := os.Getenv("DAWNGATE_P3A2_SYNTH_OUT")
	if dir == "" {
		t.Skip("set DAWNGATE_P3A2_SYNTH_OUT to write the synthetic inputs")
	}
	text := "0x0000002a Forty-two\r\n0x0000002b\tTabbed\n0xZZ bad\n0x0000002cno space\n0x0000002d　Ideographic next"
	pkg := testgame.Package(0, []testgame.PackageEntry{
		{Type: 0x02FAC0B6, Instance: uint64(pytext.FNV1([]byte("tbl"))), Data: testgame.RefPackLiterals([]byte(text)),
			Comp: dbpf.CompRefPack, USize: uint32(len(text))},
	})
	for name, b := range map[string][]byte{"synth_types.txt": []byte(synthTypes), "synth_locale.package": pkg,
		"bin/b.Thing.bin": thing(), "bin/h.Holder.bin": holder()} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func holder() []byte {
	le := binary.LittleEndian
	d := le.AppendUint32(nil, 0)
	d = le.AppendUint32(d, 1)
	d = le.AppendUint32(d, 0)
	d = le.AppendUint32(d, 0)
	d = le.AppendUint32(d, 0x02000000)
	return append(d, "x\x00"...)
}

func TestParseTypesErrors(t *testing.T) {
	for _, s := range []string{
		"== Bad\n",
		"== T size=0xzz members=0\n",
		"enum 0x1 enum 1\n",
		"enum 0x1 enum: 0=a\n",
		"enum 0x1 enum 1: x=a\n",
		"== T size=0x4 crc=0xZZ \n",
	} {
		if _, err := ParseTypes([]byte(s)); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
	ts, err := ParseTypes([]byte("# only a comment\n\n   0 +0x0 orphan int\n"))
	if err != nil || len(ts.Names()) != 0 {
		t.Errorf("comment-only table: %v", err)
	}
}

func TestFromSnap(t *testing.T) {
	ts, _ := ParseTypes([]byte(synthTypes))
	arch, err := snap.Parse(testgame.Snap([]testgame.SnapFile{
		{Name: "script/preload.txt", Data: []byte("x")},
		{Name: "AssetData/Binary/b.Thing.bin", Data: thing()},
		{Name: "AssetData/Binary/a.Thing.bin", Data: thing()[:10]},
		{Name: "AssetData/Binary/sub/c.Thing.bin", Data: thing()},
		{Name: "AssetData/Binary/catalog_153.bin", Data: []byte{0}},
		{Name: "assetdata/binary/Odd.Elem.Extra.bin", Data: []byte{0}},
		{Name: "AssetData/Binary/readme.txt", Data: []byte{0}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	set, err := FromSnap(arch)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(set.Classes(), ","); got != "catalog,elem.extra,thing" {
		t.Errorf("classes %s", got)
	}
	if fs := set.Files("THING"); len(fs) != 2 || fs[0].Name != "a" || fs[1].FileName != "b.Thing.bin" {
		t.Errorf("files %+v", fs)
	}
	inst, errs, typ, err := set.Instances(ts, "Thing", nil)
	if err != nil || typ.Name != "Thing" || inst.Len() != 1 || errs.Len() != 1 {
		t.Fatalf("%v %v %v", inst, errs, err)
	}
	b, _ := inst.Get("b")
	bad, _ := b.(*Object).Get("_badref")
	if len(bad.([]any)) != 2 {
		t.Errorf("_badref %v", bad)
	}
	if msg, _ := errs.Get("a"); !strings.Contains(msg.(string), "past the end") {
		t.Errorf("a: %v", msg)
	}
	for _, c := range []string{"Elem.Extra", "Nothing", "Inner"} {
		if _, _, _, err := set.Instances(ts, c, nil); !IsNoClass(err) {
			t.Errorf("%s: %v", c, err)
		}
	}
	doc, err := set.Dump(ts, "thing", nil)
	if err != nil || !bytes.HasPrefix(doc, []byte(`{"class":"Thing","count":1,"errors":{"a":`)) {
		t.Errorf("dump %s %v", doc, err)
	}

	for _, names := range [][]string{
		{"AssetData/Binary/x.Thing.bin", "AssetData/Binary/X.thing.bin"},
		{"AssetData/../x.bin"},
		{"C:/x.bin"},
		{"AssetData/Binary/a?.Thing.bin"},
		{"/"},
	} {
		var files []testgame.SnapFile
		for _, n := range names {
			files = append(files, testgame.SnapFile{Name: n, Data: []byte{0}})
		}
		a, _ := snap.Parse(testgame.Snap(files))
		if _, err := FromSnap(a); err == nil {
			t.Errorf("%q accepted", names)
		}
	}
}

func TestAppendJSON(t *testing.T) {
	o := NewObject()
	o.Set("b", 1.0)
	o.Set("a", []any{math.NaN(), math.Inf(1), math.Inf(-1), 1e16, 1e-5, math.Copysign(0, -1), 100000.0, uint64(7), int64(-7), int(3)})
	o.Set("s", "q\"\\\n\r\t\x01\x7fé")
	o.Set("b", true) // keeps its place
	o.Set("x", []string{"y"})
	got := string(AppendJSON(nil, o))
	want := `{"b":true,"a":[NaN,Infinity,-Infinity,1e+16,1e-05,-0.0,100000.0,7,-7,3],"s":"q\"\\\n\r\t\u0001\u007fé","x":["y"]}`
	if got != want {
		t.Errorf("%s\nwant %s", got, want)
	}
	if m, err := o.MarshalJSON(); err != nil || string(m) != want {
		t.Error("MarshalJSON")
	}
}
