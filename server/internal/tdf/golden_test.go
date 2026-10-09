package tdf

import (
	"bytes"
	"encoding/hex"
	"testing"

	"dawngate/server/internal/golden"
)

func unhex(t *testing.T, s string) []byte { return golden.Bytes(t, s) }

func pingSite(host string, port int64) *Writer {
	return NewWriter().String("PSA", host).Int("PSP", port).String("SNA", "local")
}

// writerCases are the Go twins of tdf_golden.py's WRITER_CASES (codec cases only; the replies are
// checked where they are built, in the redirector and blaze packages).
var writerCases = map[string]func() *Writer{
	"ints": func() *Writer {
		w := NewWriter()
		w.Int("A", 0).Int("B", 1).Int("C", 63).Int("D", 64).Int("E", -1).Int("F", -63).Int("G", -64)
		w.Int("H", 8191).Int("I", 8192).Int("J", 0x7FFFFFFF).Int("K", -0x80000000).Int("L", 0xFFFFFFFF)
		w.Int("M", 1<<63-1).Int("N", -1<<63).Uint("O", 1<<64-1).Int("STIM", 1791405059).Int("LOC", 0x656E5553)
		return w
	},
	"labels": func() *Writer {
		w := NewWriter()
		for _, l := range []string{"X", "AB", "ABC", "ABCD", "IP", "PS1", "A_Z9", "ab", "TOOLONG"} {
			w.Int(l, 1)
		}
		return w
	},
	"strings": func() *Writer {
		return NewWriter().String("EMPT", "").String("HELO", "hello").String("UTF8", "\u00e4\u00f6\u00fc \u20ac \U0001F600").
			String("SQ", "it's").String("BOTH", "it's \"x\"").String("DQ", "say \"hi\"").
			String("CTRL", "a\tb\nc\rd\x01\x7f").String("BSL", `C:\path`).String("NBSP", "a\u00a0b\u200bc")
	},
	"blobs": func() *Writer {
		return NewWriter().Blob("EMPT", []byte{}).Blob("BIN", append([]byte{0, 1, 0x7F, 0x80, 0xFF}, "'\"\\\t\n\rAz"...))
	},
	"structs": func() *Writer {
		w := NewWriter()
		w.Struct("OUTR", func(w *Writer) {
			w.Int("A", 1)
			w.Struct("INNR", func(w *Writer) {
				w.String("S", "x")
				w.Struct("EMPT", nil)
			})
			w.Int("B", 2)
		})
		w.Struct("EMPT", nil)
		return w
	},
	"lists": func() *Writer {
		return NewWriter().
			List("INTS", TypeInt, []int{1, 4, 5, 30722, -7}).
			List("STRS", TypeString, []string{"a", "", "b c"}).
			List("STCT", TypeStruct, []*Writer{NewWriter().Int("ID", 1).String("NAME", "one"), NewWriter()}).
			List("EMPT", TypeInt, []int{})
	},
	"maps": func() *Writer {
		return NewWriter().
			Map("SS", TypeString, TypeString, KV("connIdleTimeout", "90s"), KV("pingPeriod", "20s")).
			Map("ST", TypeString, TypeStruct, KV("local", pingSite("127.0.0.1", 17502))).
			Map("II", TypeInt, TypeInt, KV(1, 10), KV(-2, 20)).
			Map("IS", TypeInt, TypeString, KV(7, "seven")).
			Map("EMPT", TypeString, TypeInt)
	},
	"unions": func() *Writer {
		w := NewWriter()
		w.Union("ADDR", 0, func(w *Writer) {
			w.Struct("VALU", func(w *Writer) { w.String("HOST", "127.0.0.1") })
		})
		w.Union("UNST", UnionUnset, nil)
		w.Union("INT", 2, func(w *Writer) { w.Int("VALU", 5) })
		return w
	},
	"list_unions": func() *Writer {
		addr := func(w *Writer, label string, ip, port int64) {
			w.Struct(label, func(w *Writer) { w.Int("IP", ip).Int("PORT", port) })
		}
		pair := NewWriter()
		addr(pair, "EXIP", 0x7F000001, 10042)
		addr(pair, "INIP", 0x7F000001, 3659)
		return NewWriter().
			List("HNET", TypeStruct, []UnionItem{{Member: 2, Body: pair},
				{Member: 3, Body: NewWriter().Int("IP", 1).Int("PORT", 2)}, {Member: UnionUnset}}).
			List("EMPT", TypeStruct, []UnionItem{})
	},
}

func TestWriterMatchesPythonCodec(t *testing.T) {
	g := golden.Load(t)
	seen := 0
	for _, c := range g.Writer {
		build, ok := writerCases[c.Name]
		if !ok {
			continue // a reply; tested in its own package
		}
		seen++
		if got := hex.EncodeToString(build().Bytes()); got != c.Hex {
			t.Errorf("%s: Go writer\n got %s\nwant %s", c.Name, got, c.Hex)
		}
	}
	if seen != len(writerCases) {
		t.Errorf("only %d of %d writer cases found in tdf_golden.json", seen, len(writerCases))
	}
}

func TestDecodePrettyMatchesPythonCodec(t *testing.T) {
	g := golden.Load(t)
	for _, c := range append(g.Writer, g.Decode...) {
		s, err := Decode(unhex(t, c.Hex))
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if got := Pretty(s, 2); got != c.Pretty {
			t.Errorf("%s: pretty\n got %q\nwant %q", c.Name, got, c.Pretty)
		}
	}
}

// Round trip: decoding and re-encoding gives the same bytes for every canonical payload.
func TestRoundTrip(t *testing.T) {
	g := golden.Load(t)
	nonCanonical := map[string]bool{ // decode normalizes these on purpose
		"map_duplicate_keys": true, "duplicate_labels": true, "string_no_nul": true,
		"string_bad_utf8": true, "struct_unterminated": true,
	}
	for _, c := range append(g.Writer, g.Decode...) {
		if nonCanonical[c.Name] {
			continue
		}
		in := unhex(t, c.Hex)
		s, err := Decode(in)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if out := Encode(s); !bytes.Equal(out, in) {
			t.Errorf("%s: round trip\n got %x\nwant %x", c.Name, out, in)
		}
	}
}

func TestDecodeErrorsMatchPythonCodec(t *testing.T) {
	g := golden.Load(t)
	if len(g.Errors) == 0 {
		t.Fatal("no error cases")
	}
	for _, c := range g.Errors {
		_, err := Decode(unhex(t, c.Hex))
		if err == nil || err.Error() != c.Error {
			t.Errorf("%s: error %v, want %q", c.Name, err, c.Error)
		}
	}
}

func TestStructAccessors(t *testing.T) {
	s, err := Decode(NewWriter().String("MAIL", "player@example.com").Int("TYPE", 0).
		Struct("CDAT", func(w *Writer) { w.Int("IITO", 7) }).Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := s.Str("MAIL"); !ok || v != "player@example.com" {
		t.Errorf("Str(MAIL) = %q, %v", v, ok)
	}
	if v, ok := s.Int("TYPE"); !ok || v != 0 {
		t.Errorf("Int(TYPE) = %d, %v", v, ok)
	}
	if sub, ok := s.Sub("CDAT"); !ok {
		t.Error("Sub(CDAT) missing")
	} else if v, _ := sub.Int("IITO"); v != 7 {
		t.Errorf("CDAT.IITO = %d", v)
	}
	if _, ok := s.Str("NONE"); ok {
		t.Error("Str(NONE) found")
	}
}

func TestLabels(t *testing.T) {
	for _, l := range []string{"SECU", "VALU", "PSA", "IP", "A"} {
		b := EncodeLabel(l)
		if got := DecodeLabel(b[:]); got != l {
			t.Errorf("label %q -> % x -> %q", l, b, got)
		}
	}
	if b := EncodeLabel("STIM"); b != [3]byte{0xCF, 0x4A, 0x6D} {
		t.Errorf("STIM = % x", b)
	}
}
