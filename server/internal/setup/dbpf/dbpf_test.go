package dbpf

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"

	"dawngate/server/internal/setup/testgame"
)

func unhex(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		panic(err)
	}
	return b
}

// Handcrafted RefPack streams, one per opcode form; the outputs are dbpf.py's refpack_decompress's.
func TestRefPackForms(t *testing.T) {
	for _, c := range []struct{ name, stream, want string }{
		{"literal block, 2-byte copy, stop with 3", "10fb00000f e061626364 1403 ff58595a", "abcdabcdabcdXYZ"},
		{"3-byte copy with 2 literals", "10fb00000c e07778797a 828005 3132 fc", "wxyz12wxyz12"},
		{"4-byte copy, overlapping", "10fb00000c e061626364 c1000402 65 fc", "abcdeabcdeab"},
		{"run of one byte", "10fb000008 1100 61 fc", "aaaaaaaa"},
		{"4-byte sizes and a compressed size", "81fb00000011 0000000c e061626364 c1000402 65 fc", "abcdeabcdeab"},
		{"a literal block longer than the size", "10fb000005 e161626364656667 fc", ""},
	} {
		got, err := RefPackDecompress(unhex(c.stream))
		if c.want == "" {
			if err == nil {
				t.Errorf("%s: accepted (%q)", c.name, got)
			}
			continue
		}
		if err != nil || string(got) != c.want {
			t.Errorf("%s: %q %v, want %q", c.name, got, err, c.want)
		}
	}
	lit := bytes.Repeat([]byte("0123456789abcdef"), 40)
	for n := 0; n <= len(lit); n += 37 {
		got, err := RefPackDecompress(testgame.RefPackLiterals(lit[:n]))
		if err != nil || !bytes.Equal(got, lit[:n]) {
			t.Errorf("literals %d: %v", n, err)
		}
	}
}

func TestRefPackErrors(t *testing.T) {
	for _, s := range []string{
		"",
		"10fa000001 fc",                 // magic
		"10fb00",                        // header cut short
		"10fb000008 e43031323334353637", // literal block past the end (dbpf.py: size mismatch)
		"10fb000008 1103 61 fc",         // copy from before the start
		"10fb000004 14",                 // 2-byte op cut short
		"10fb000004 8280",               // 3-byte op cut short
		"10fb000004 c10004",             // 4-byte op cut short
		"10fb000002 e061626364 fc",      // more output than its size
		"10fb000009 e061626364 fc",      // less output than its size
		"10fb000004 fd61",               // stop code's literal cut short
	} {
		if got, err := RefPackDecompress(unhex(s)); err == nil {
			t.Errorf("%s: accepted (%q)", s, got)
		}
	}
}

func zlibBytes(b []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func synthPackage(flags uint32) []byte {
	text := []byte("property A 0x1 string8 http://ea.com/x\n")
	return testgame.Package(flags, []testgame.PackageEntry{
		{Type: 0x024A0E52, Group: 0, Instance: 0x8C47952D, Data: testgame.RefPackLiterals(text), Comp: CompRefPack, USize: uint32(len(text)), Committed: 1},
		{Type: 0x024A0E52, Group: 0, Instance: 0x0000000200000001, Data: []byte("plain")},
		{Type: 0x024A0E52, Group: 0, Instance: 0x0000000200000002, Data: zlibBytes([]byte("zipped zipped zipped")), Comp: CompZlib, USize: 20},
		{Type: 0x024A0E52, Group: 0, Instance: 0x0000000200000003, Data: nil, Comp: CompDeleted, USize: 7},
		{Type: 0x024A0E52, Group: 0, Instance: 0x0000000200000004, Data: []byte("same"), Comp: CompRefPack, USize: 4},
	})
}

func TestReadAndRebuild(t *testing.T) {
	for _, flags := range []uint32{0, 1, 3} {
		raw := synthPackage(flags)
		p, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if p.Major != 3 || p.IndexVersion != 3 || p.IndexFlags != flags || len(p.Entries) != 5 {
			t.Fatalf("flags %d: %+v", flags, p)
		}
		if k := p.Entries[1].Key(); k != "024A0E52-00000000-0000000200000001" {
			t.Errorf("key %s", k)
		}
		for i, want := range []string{"property A 0x1 string8 http://ea.com/x\n", "plain", "zipped zipped zipped", "", "same"} {
			got, err := p.Read(p.Entries[i])
			if err != nil || string(got) != want {
				t.Errorf("flags %d entry %d: %q %v", flags, i, got, err)
			}
		}
		same, err := p.Rebuild(nil)
		if err != nil || !bytes.Equal(same, raw) {
			t.Errorf("flags %d: Rebuild(nil) changed the package: %v", flags, err)
		}
		out, err := p.Rebuild(map[string][]byte{p.Entries[0].Key(): []byte("new text"), p.Entries[2].Key(): []byte("z")})
		if err != nil {
			t.Fatal(err)
		}
		q, err := Parse(out)
		if err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint64(out[0x40:]) != uint64(len(out)-int(q.IndexSize)) || !bytes.Equal(out[:0x24], raw[:0x24]) {
			t.Error("header")
		}
		for i, want := range []string{"new text", "plain", "z", "", "same"} {
			got, err := q.Read(q.Entries[i])
			if err != nil || string(got) != want {
				t.Errorf("rebuilt entry %d: %q %v", i, got, err)
			}
		}
		if e := q.Entries[0]; e.Comp != CompNone || e.USize != 8 || e.CSize != 8 || e.Committed != 1 || e.Offset != HeaderSize {
			t.Errorf("replaced entry %+v", e)
		}
		if e := q.Entries[2]; e.Comp != CompNone || e.USize != 1 {
			t.Errorf("replaced zlib entry %+v", e)
		}
		if _, err := p.Rebuild(map[string][]byte{"00000000-00000000-0000000000000000": nil}); err == nil {
			t.Error("Rebuild took a key no entry has")
		}
	}
}

// Rebuild writes the data in the order of the old offsets and the index in its old order (dbpf.py).
func TestRebuildOrder(t *testing.T) {
	raw := testgame.Package(0, []testgame.PackageEntry{
		{Type: 1, Instance: 1, Data: []byte("first")},
		{Type: 1, Instance: 2, Data: []byte("second")},
	})
	p, _ := Parse(raw)
	// swap the index order: entry 0 now points at the second payload
	p.Entries[0], p.Entries[1] = p.Entries[1], p.Entries[0]
	out, err := p.Rebuild(map[string][]byte{p.Entries[1].Key(): []byte("FIRST")})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("FIRSTsecond")) {
		t.Errorf("data order: %q", out[HeaderSize:HeaderSize+11])
	}
	q, _ := Parse(out)
	if q.Entries[0].Instance != 2 || q.Entries[1].Instance != 1 || q.Entries[0].Offset != HeaderSize+5 {
		t.Errorf("index order: %+v", q.Entries)
	}
}

func TestParseErrors(t *testing.T) {
	good := synthPackage(0)
	cut := func(n int) []byte { return good[:n] }
	badCount := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(badCount[0x24:], 1<<30)
	badOffset := append([]byte(nil), good...)
	binary.LittleEndian.PutUint64(badOffset[0x40:], 1<<40)
	for name, b := range map[string][]byte{"empty": nil, "magic": []byte("DBPX" + string(good[4:])), "header": cut(0x40),
		"index": cut(len(good) - 3), "count": badCount, "offset": badOffset} {
		if _, err := Parse(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	p, _ := Parse(good)
	e := p.Entries[1]
	e.CSize = 1 << 20
	if _, err := p.Read(e); err == nil {
		t.Error("an entry outside the file read")
	}
	e = p.Entries[1]
	e.Comp = 0x1234
	e.USize = 99
	if _, err := p.Read(e); err == nil {
		t.Error("an unknown compression read")
	}
}
