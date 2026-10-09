// Package pytext holds the few rules of Python 3's text handling that the ported setup scripts
// (dbpf.py, patch_config.py, snap.py, assetdata.py) depend on, so the Go
// ports read odd input exactly as the scripts do: bytes.decode("utf-8", "replace"), the re module's \s
// and \w on str patterns, str.splitlines and str.lower. The character sets were taken from CPython
// 3.12 by testing every code point (\s and str.splitlines) or every Latin-1 character (\w); Python's \d
// is Go's unicode.IsDigit (checked the same way: 680 code points, no difference).
package pytext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// DecodeUTF8 decodes b as Python's bytes.decode("utf-8", "replace") does: each maximal invalid
// subsequence (Unicode's "maximal subpart" rule, which CPython follows) becomes one U+FFFD. Go's own
// conversion replaces byte by byte instead, which differs for a sequence cut short ("\xe2\x82").
func DecodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) + 8)
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		var need int
		lo, hi := byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c == 0xE0:
			need, lo = 2, 0xA0
		case c == 0xED:
			need, hi = 2, 0x9F
		case c >= 0xE1 && c <= 0xEF:
			need = 2
		case c == 0xF0:
			need, lo = 3, 0x90
		case c >= 0xF1 && c <= 0xF3:
			need = 3
		case c == 0xF4:
			need, hi = 3, 0x8F
		default:
			sb.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j, ok := i+1, true
		for k := 0; k < need; k++ {
			l, h := byte(0x80), byte(0xBF)
			if k == 0 {
				l, h = lo, hi
			}
			if j >= len(b) || b[j] < l || b[j] > h {
				ok = false
				break
			}
			j++
		}
		if ok {
			sb.Write(b[i:j])
		} else {
			sb.WriteRune(utf8.RuneError)
		}
		i = j
	}
	return sb.String()
}

// IsSpace reports whether the re module's \s matches r in a str pattern: Go's unicode.IsSpace (the
// White_Space property) plus U+001C-U+001F, which Python counts as whitespace too.
func IsSpace(r rune) bool {
	return unicode.IsSpace(r) || r >= 0x1C && r <= 0x1F
}

// IsWordLatin1 reports whether the re module's \w matches the Latin-1 character r (0-255) in a str
// pattern: ASCII letters, digits and '_', and the Latin-1 letters and number signs (ª ² ³ µ ¹ º ¼ ½ ¾,
// À-Ö, Ø-ö, ø-ÿ). Only defined for r < 256.
func IsWordLatin1(r rune) bool {
	switch {
	case r >= '0' && r <= '9', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		return true
	case r == 0xAA, r == 0xB2, r == 0xB3, r == 0xB5, r == 0xB9, r == 0xBA, r >= 0xBC && r <= 0xBE:
		return true
	case r >= 0xC0 && r <= 0xFF && r != 0xD7 && r != 0xF7:
		return true
	}
	return false
}

// SplitLines splits s as Python's str.splitlines() does: at \n, \r, \r\n, \v, \f, \x1c, \x1d, \x1e,
// U+0085, U+2028 and U+2029, without the line ends, and without an empty last line after a final break.
func SplitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '\r':
			lines = append(lines, s[start:i])
			i += size
			if i < len(s) && s[i] == '\n' {
				i++
			}
			start = i
			continue
		case '\n', '\v', '\f', 0x1C, 0x1D, 0x1E, 0x85, 0x2028, 0x2029:
			lines = append(lines, s[start:i])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// Lower lower-cases s as Python's str.lower() does, as far as the result's Latin-1 characters go: Go's
// simple case mapping, except that U+0130 (İ) becomes "i" plus U+0307, as in Python's full mapping. Other
// differences (Greek final sigma) only touch characters outside Latin-1.
func Lower(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if r == 0x130 {
			sb.WriteString("i̇")
			continue
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	return sb.String()
}

// Latin1Replace encodes s as Python's str.encode("latin-1", "replace") does: one byte per character,
// '?' for each character above U+00FF.
func Latin1Replace(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			r = '?'
		}
		out = append(out, byte(r))
	}
	return out
}

// Latin1Decode decodes b as Latin-1 (each byte is the character of the same number).
func Latin1Decode(b []byte) string {
	rs := make([]rune, len(b))
	for i, c := range b {
		rs[i] = rune(c)
	}
	return string(rs)
}

// Latin1Encode encodes s as Latin-1; ok is false when s has a character above U+00FF (Python's strict
// encode would raise).
func Latin1Encode(s string) (out []byte, ok bool) {
	out = make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			return nil, false
		}
		out = append(out, byte(r))
	}
	return out, true
}

// FNV1 is the 32-bit FNV-1 hash (offset basis 0x811C9DC5, prime 0x01000193, multiply then XOR) of b,
// the hash of Spore's and Dawngate's names (asset names, property ids, Locale tables).
func FNV1(b []byte) uint32 {
	h := uint32(0x811C9DC5)
	for _, c := range b {
		h *= 0x01000193
		h ^= uint32(c)
	}
	return h
}
