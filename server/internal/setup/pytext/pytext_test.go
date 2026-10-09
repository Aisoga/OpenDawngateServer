package pytext

import (
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// Python 3.12's b.decode("utf-8", "replace") of each input, as code points.
func TestDecodeUTF8LikePython(t *testing.T) {
	for _, c := range []struct {
		hex  string
		want []rune
	}{
		{"e282", []rune{0xFFFD}},
		{"e282ac", []rune{0x20AC}},
		{"61ff62", []rune{'a', 0xFFFD, 'b'}},
		{"eda080", []rune{0xFFFD, 0xFFFD, 0xFFFD}},
		{"f09f98", []rune{0xFFFD}},
		{"f4908080", []rune{0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD}},
		{"c0af", []rune{0xFFFD, 0xFFFD}},
		{"e080af", []rune{0xFFFD, 0xFFFD, 0xFFFD}},
		{"f08080af", []rune{0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD}},
		{"c3", []rune{0xFFFD}},
		{"8080", []rune{0xFFFD, 0xFFFD}},
		{"e180e18080", []rune{0xFFFD, 0x1000}},
		{"f18080e180c2", []rune{0xFFFD, 0xFFFD, 0xFFFD}},
		{"efbfbd", []rune{0xFFFD}},
		{"", nil},
	} {
		b, _ := hex.DecodeString(c.hex)
		got := []rune(DecodeUTF8(b))
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %U, want %U", c.hex, got, c.want)
		}
		if !utf8.ValidString(DecodeUTF8(b)) {
			t.Errorf("%s: invalid UTF-8 out", c.hex)
		}
	}
}

// Python's re \s, by testing every code point: 0009-000D 001C-0020 0085 00A0 1680 2000-200A 2028-2029 202F
// 205F 3000.
func TestIsSpaceLikePython(t *testing.T) {
	want := map[rune]bool{0x85: true, 0xA0: true, 0x1680: true, 0x2028: true, 0x2029: true, 0x202F: true, 0x205F: true, 0x3000: true}
	for r := rune(0x09); r <= 0x0D; r++ {
		want[r] = true
	}
	for r := rune(0x1C); r <= 0x20; r++ {
		want[r] = true
	}
	for r := rune(0x2000); r <= 0x200A; r++ {
		want[r] = true
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if IsSpace(r) != want[r] {
			t.Errorf("IsSpace(%U) = %v", r, IsSpace(r))
		}
	}
}

// Python's re \w on Latin-1: 0030-0039 0041-005A 005F 0061-007A 00AA 00B2-00B3 00B5 00B9-00BA 00BC-00BE
// 00C0-00D6 00D8-00F6 00F8-00FF.
func TestIsWordLatin1LikePython(t *testing.T) {
	ranges := [][2]rune{{0x30, 0x39}, {0x41, 0x5A}, {0x5F, 0x5F}, {0x61, 0x7A}, {0xAA, 0xAA}, {0xB2, 0xB3}, {0xB5, 0xB5},
		{0xB9, 0xBA}, {0xBC, 0xBE}, {0xC0, 0xD6}, {0xD8, 0xF6}, {0xF8, 0xFF}}
	for r := rune(0); r < 256; r++ {
		want := false
		for _, rg := range ranges {
			want = want || r >= rg[0] && r <= rg[1]
		}
		if IsWordLatin1(r) != want {
			t.Errorf("IsWordLatin1(%U) = %v", r, !want)
		}
	}
}

func TestSplitLinesLikePython(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"\n", []string{""}},
		{"a\r\nb\rc\nd", []string{"a", "b", "c", "d"}},
		{"a\n\nb", []string{"a", "", "b"}},
		{"a\r\r\nb", []string{"a", "", "b"}},
		{"a\vb\fc\x1cd\x1de\x1ef\u0085g h i", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}},
		{"a\x1fb\tc", []string{"a\x1fb\tc"}},
	} {
		if got := SplitLines(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitLines(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLowerAndLatin1(t *testing.T) {
	if got := Lower("PC_AlphaİKÀ"); got != "pc_alphai̇kà" {
		t.Errorf("Lower: %q", got)
	}
	if got := string(Latin1Replace("aḃé€")); got != "ab?\xe9?" {
		t.Errorf("Latin1Replace: %q", got)
	}
	if got := Latin1Decode([]byte{'a', 0xE9, 0xFF}); got != "aéÿ" {
		t.Errorf("Latin1Decode: %q", got)
	}
	if _, ok := Latin1Encode("a€"); ok {
		t.Error("Latin1Encode took €")
	}
	if b, ok := Latin1Encode("aé"); !ok || string(b) != "a\xe9" {
		t.Errorf("Latin1Encode: %q %v", b, ok)
	}
}

// FNV-1 of the documented property ids (patch_config.py: WebkitAllowedDomains 0xC27EA5F8, AFKThreshhold
// 0x5EEB4D02, AFKDisconnectThreshhold 0xFF234C60) on the lower-case names.
func TestFNV1(t *testing.T) {
	for name, want := range map[string]uint32{"WebkitAllowedDomains": 0xC27EA5F8, "AFKThreshhold": 0x5EEB4D02,
		"AFKDisconnectThreshhold": 0xFF234C60} {
		if got := FNV1([]byte(strings.ToLower(name))); got != want {
			t.Errorf("FNV1(%s) = %#x, want %#x", name, got, want)
		}
	}
	if FNV1(nil) != 0x811C9DC5 {
		t.Error("FNV1 offset basis")
	}
}
