package snap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// lcg is a made-up input: n bytes of a linear congruential generator, (97 + (x >> 16) % mod) & 0xFF each.
func lcg(n, mod int) []byte {
	x := uint32(1)
	out := make([]byte, n)
	for i := range out {
		x = (x*1103515245 + 12345) & 0x7FFFFFFF
		out[i] = byte(97 + int(x>>16)%mod)
	}
	return out
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// The SHA-256s snap.py's snappy_compress (and patch_login_movie.py's pack_bytes) give for the same inputs.
func TestEncodeMatchesPython(t *testing.T) {
	hello := []byte("hello hello hello hello, said the snappy encoder to the hello")
	for _, c := range []struct {
		name string
		in   []byte
		want string
		size int
	}{
		{"empty", nil, "6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d", 1},
		{"hello", hello, "b10daa390db7799ad7dcfedf8a0e34adb13a332768625e6456ccaeea5b7cce3c", 44},
		{"zeros", make([]byte, 70000), "ce16974b6d393c0283bfc283b34a1764d29bb6a9ccfe6f96669eb5e55a597a86", 3289},
		{"lcg7", lcg(200000, 7), "eb802cfe0142a0359ee2c587dd9b17eabce0d9badd896ae64106ad29182e0d2f", 139120},
		{"lcg200", lcg(150000, 200), "e7247e971523ac302e41fd71d6b4523c6b60958ab5f791c8811fbf0a66286d84", 150021},
	} {
		got := Encode(c.in)
		if sum(got) != c.want || len(got) != c.size {
			t.Errorf("%s: %d bytes, SHA-256 %s; snap.py: %d bytes, %s", c.name, len(got), sum(got), c.size, c.want)
		}
		back, err := Decode(got, int64(len(c.in)))
		if err != nil || !bytes.Equal(back, c.in) {
			t.Errorf("%s: doesn't decode back (%v)", c.name, err)
		}
	}
	arc := Pack([]File{{Name: "a/one.txt", Data: hello}, {Name: "b/two.bin", Data: lcg(200000, 7)}})
	if got, want := sum(arc), "355e68850a27f0b05de2945b07d36d4739556355559093206c05194af1f4bd16"; got != want || len(arc) != 139280 {
		t.Errorf("Pack: %d bytes, SHA-256 %s; patch_login_movie.py's pack_bytes: 139280 bytes, %s", len(arc), got, want)
	}
	a, err := Parse(arc)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Entries) != 2 || a.Entries[1].Name != "b/two.bin" || len(a.LayoutNotes()) != 0 {
		t.Fatalf("Pack's archive: %+v, notes %v", a.Entries, a.LayoutNotes())
	}
	if d, err := a.Read(a.Entries[0]); err != nil || !bytes.Equal(d, hello) {
		t.Errorf("Pack's first entry: %q, %v", d, err)
	}
}

// Literals only: the block's size depends on the data's size alone, and it decodes.
func TestEncodeLiterals(t *testing.T) {
	for _, n := range []int{0, 1, 60, 61, 300, 1 << 16, 1<<16 + 5, 200000} {
		a, b := make([]byte, n), lcg(n, 3)
		ea, eb := EncodeLiterals(a), EncodeLiterals(b)
		if len(ea) != len(eb) {
			t.Errorf("%d bytes: %d and %d encoded", n, len(ea), len(eb))
		}
		for _, c := range []struct{ in, enc []byte }{{a, ea}, {b, eb}} {
			if back, err := Decode(c.enc, int64(n)); err != nil || !bytes.Equal(back, c.in) {
				t.Errorf("%d bytes: doesn't decode back (%v)", n, err)
			}
		}
	}
	x := Pack([]File{{Name: "x.json", Data: []byte("one\n\n\n\n\n"), Literals: true}})
	y := Pack([]File{{Name: "x.json", Data: []byte("two two\n"), Literals: true}})
	if len(x) != len(y) || !bytes.Equal(x[:HeaderSize], y[:HeaderSize]) || !bytes.Equal(x[len(x)-39:], y[len(y)-39:]) {
		t.Error("two archives of the same sizes have different layouts")
	}
}
