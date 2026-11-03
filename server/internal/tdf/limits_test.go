package tdf

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

// The decoder's bounds (decode.go): counts must fit the bytes left, containers nest at most MaxDepth deep, and a
// payload decodes into at most DecodeBudget(len) bytes. Each lying payload is an error, never a panic or a big
// allocation.

// hdr is a member's header: its label and type.
func hdr(label string, t Type) []byte {
	l := EncodeLabel(label)
	return append(l[:], byte(t))
}

// count is a varint as the decoder reads it.
func count(n int64) []byte { return appendInt(nil, n) }

// allocBytes is the heap a call of f allocates (an average over a few runs).
func allocBytes(f func()) uint64 {
	const runs = 5
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	for i := 0; i < runs; i++ {
		f()
	}
	runtime.ReadMemStats(&b)
	return (b.TotalAlloc - a.TotalAlloc) / runs
}

func wantDecodeError(t *testing.T, name string, payload []byte, want string) {
	t.Helper()
	s, err := Decode(payload)
	if err == nil {
		t.Fatalf("%s: decoded %v, want an error", name, s)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: error %q, want it to contain %q", name, err, want)
	}
}

func TestDecodeListLyingCount(t *testing.T) {
	// a list of ints claiming 1<<20 items in 9 bytes: the old decoder reserved 16 MiB before reading one
	payload := append(hdr("LIST", TypeList), byte(TypeInt))
	payload = append(payload, count(1<<20)...)
	wantDecodeError(t, "list", payload, "too large")
	if n := allocBytes(func() { _, _ = Decode(payload) }); n > 4096 {
		t.Fatalf("decoding the lying list allocated %d bytes", n)
	}
	// a count that would fit in bytes but not in items of the element's size (a list of lists takes 2 a piece)
	payload = append(hdr("LIST", TypeList), byte(TypeList))
	payload = append(payload, count(3)...)
	payload = append(payload, byte(TypeInt), 0, byte(TypeInt))
	wantDecodeError(t, "list of lists", payload, "too large")
	// struct items at the end of the data took no bytes at all (no terminator): 1000 claimed, none there
	payload = append(hdr("LIST", TypeList), byte(TypeStruct))
	payload = append(payload, count(1000)...)
	wantDecodeError(t, "list of structs", payload, "too large")
	// the largest count that fits still decodes
	payload = append(hdr("LIST", TypeList), byte(TypeInt))
	payload = append(payload, count(3)...)
	payload = append(payload, 1, 2, 3)
	s, err := Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	if l := s.Fields[0].Value.(*List); len(l.Items) != 3 || cap(l.Items) != 3 {
		t.Fatalf("list %v (cap %d)", l.Items, cap(l.Items))
	}
}

func TestDecodeMapAndIntListLyingCounts(t *testing.T) {
	payload := append(hdr("MAP", TypeMap), byte(TypeString), byte(TypeStruct))
	payload = append(payload, count(1<<20)...)
	wantDecodeError(t, "map", payload, "too large")
	payload = append(hdr("MAP", TypeMap), byte(TypeInt), byte(TypeInt))
	payload = append(payload, count(2)...)
	payload = append(payload, 1, 2, 3) // two entries need 4 bytes
	wantDecodeError(t, "short map", payload, "too large")

	payload = append(hdr("ILST", TypeIntList), count(1<<20)...)
	wantDecodeError(t, "intlist", payload, "too large")
	if n := allocBytes(func() { _, _ = Decode(payload) }); n > 4096 {
		t.Fatalf("decoding the lying intlist allocated %d bytes", n)
	}
	// counts beyond the old maxCount and beyond int32 are refused the same way
	payload = append(hdr("ILST", TypeIntList), count(1<<40)...)
	wantDecodeError(t, "huge intlist", payload, "too large")
}

func TestDecodeStringAndBlobLyingLengths(t *testing.T) {
	payload := append(hdr("STR", TypeString), count(1<<30)...)
	wantDecodeError(t, "string", payload, "truncated")
	payload = append(hdr("BLOB", TypeBlob), count(1<<30)...)
	wantDecodeError(t, "blob", payload, "truncated")
	payload = append(hdr("BLOB", TypeBlob), count(-5)...)
	wantDecodeError(t, "negative blob", payload, "truncated")
}

// nestedLists is a list of one list of one list ... (depth lists), the innermost one empty.
func nestedLists(depth int) []byte {
	p := hdr("LIST", TypeList)
	for i := 1; i < depth; i++ {
		p = append(p, byte(TypeList), 1)
	}
	return append(p, byte(TypeInt), 0)
}

// nestedStructs is a struct holding a struct ... (depth structs), each with its terminator.
func nestedStructs(depth int) []byte {
	var p []byte
	for i := 0; i < depth; i++ {
		p = append(p, hdr("S", TypeStruct)...)
	}
	return append(p, bytes.Repeat([]byte{0}, depth)...)
}

func TestDecodeNestingLimit(t *testing.T) {
	cases := []struct {
		name string
		make func(int) []byte
	}{
		{"lists", nestedLists},
		{"structs", nestedStructs},
		{"unions", func(depth int) []byte {
			var p []byte
			for i := 0; i < depth; i++ {
				p = append(p, hdr("U", TypeUnion)...)
				p = append(p, 0)
			}
			return append(append(p, hdr("V", TypeInt)...), 1) // the innermost union's member: an int
		}},
		{"maps", func(depth int) []byte {
			p := hdr("MAP", TypeMap)
			for i := 1; i < depth; i++ {
				p = append(p, byte(TypeInt), byte(TypeMap), 1, 1) // one entry: key 1, a map
			}
			return append(p, byte(TypeInt), byte(TypeInt), 0)
		}},
		{"list unions", func(depth int) []byte { // a struct list's union element holds a struct list ...
			p := hdr("LIST", TypeList)
			for i := 1; i+1 < depth; i += 2 { // two levels each: the union element and its struct's list
				p = append(p, byte(TypeStruct), 1, 1) // a list of one union element, member 1
				p = append(p, hdr("L", TypeList)...)  // its struct holds a list
			}
			return append(p, byte(TypeInt), 0)
		}},
	}
	for _, c := range cases {
		if _, err := Decode(c.make(MaxDepth)); err != nil {
			t.Errorf("%s nested %d deep: %v", c.name, MaxDepth, err)
		}
		wantDecodeError(t, c.name, c.make(MaxDepth+1), "nested deeper")
	}
	// far deeper (a stack-exhaustion attempt) stops at the limit
	wantDecodeError(t, "deep lists", nestedLists(1_000_000), "nested deeper")
	wantDecodeError(t, "deep structs", nestedStructs(1_000_000), "nested deeper")
}

func TestDecodeBudget(t *testing.T) {
	// 60000 empty structs in a list: each takes 1 byte on the wire and about 40 in memory, over the budget
	n := 60000
	payload := append(hdr("LIST", TypeList), byte(TypeStruct))
	payload = append(payload, count(int64(n))...)
	payload = append(payload, make([]byte, n)...)
	wantDecodeError(t, "empty structs", payload, "over the budget")
	// as many small ints decode (16 + 8 bytes each), as do the same structs in a smaller list
	payload = append(hdr("LIST", TypeList), byte(TypeInt))
	payload = append(payload, count(int64(n))...)
	payload = append(payload, make([]byte, n)...)
	if _, err := Decode(payload); err != nil {
		t.Fatal(err)
	}
	payload = append(hdr("LIST", TypeList), byte(TypeStruct))
	payload = append(payload, count(50)...)
	payload = append(payload, make([]byte, 50)...)
	if _, err := Decode(payload); err != nil {
		t.Fatal(err)
	}
	if got := DecodeBudget(100); got != budgetFactor*100+budgetBase {
		t.Fatalf("DecodeBudget(100) = %d", got)
	}
}

func TestDecodeUTF8Unchanged(t *testing.T) {
	cases := map[string]string{
		"plain":      "hello",
		"valid":      "äöü € \U0001F600",
		"bad lead":   "a\xffb",
		"truncated":  "a\xe2\x82",
		"surrogate":  "\xed\xa0\x80",
		"overlong":   "\xc0\xaf",
		"bad second": "\xe0\x80\x80x",
		"too big":    "\xf4\x90\x80\x80",
		"mixed":      "Zo\xc3\xab \xe2\x82\xac\xff\xf0\x9f\x98",
	}
	want := map[string]string{
		"plain":      "hello",
		"valid":      "äöü € \U0001F600",
		"bad lead":   "a�b",
		"truncated":  "a�",
		"surrogate":  "���",
		"overlong":   "��",
		"bad second": "���x",
		"too big":    "����",
		"mixed":      "Zoë €��",
	}
	for name, in := range cases {
		if got := decodeUTF8([]byte(in)); got != want[name] {
			t.Errorf("%s: %+q, want %+q", name, got, want[name])
		}
	}
}
