package assetdata

import (
	"fmt"
	"unicode/utf8"

	"dawngate/server/internal/setup/dbpf"
	"dawngate/server/internal/setup/pytext"
)

// Locale holds a Locale.package's string tables (assetdata.py's load_locale): per table (the entry
// instance's low 32 bits, the FNV-1 of the table name) the strings by id. The English one is
// game\Data\Locale\en-us\Locale.package.
type Locale struct {
	tables map[uint32]map[uint32]string
}

// LoadLocale reads a Locale package: each entry is text (UTF-8, invalid bytes as U+FFFD) with lines
// "0x<8 hex digits><one whitespace character><string>"; other lines are skipped. A later entry with the
// same table replaces an earlier one, a later line with the same id an earlier line.
func LoadLocale(pkg *dbpf.Package) (*Locale, error) {
	l := &Locale{tables: map[uint32]map[uint32]string{}}
	for _, e := range pkg.Entries {
		data, err := pkg.Read(e)
		if err != nil {
			return nil, fmt.Errorf("assetdata: Locale: %w", err)
		}
		table := map[uint32]string{}
		for _, ln := range pytext.SplitLines(pytext.DecodeUTF8(data)) {
			if id, text, ok := localeLine(ln); ok {
				table[id] = text
			}
		}
		l.tables[uint32(e.Instance)] = table
	}
	return l, nil
}

// localeLine is re.match(r"0x([0-9a-fA-F]{8})\s(.*)$", line) on a line from str.splitlines (no line
// breaks left, so .* takes the rest).
func localeLine(ln string) (uint32, string, bool) {
	if len(ln) < 11 || ln[0] != '0' || ln[1] != 'x' {
		return 0, "", false
	}
	var id uint32
	for i := 2; i < 10; i++ {
		c := ln[i]
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, "", false
		}
		id = id<<4 | uint32(d)
	}
	r, size := utf8.DecodeRuneInString(ln[10:])
	if !pytext.IsSpace(r) {
		return 0, "", false
	}
	return id, ln[10+size:], true
}

// Text returns a string of a table.
func (l *Locale) Text(table, id uint32) (string, bool) {
	s, ok := l.tables[table][id]
	return s, ok
}

// Tables is the number of tables.
func (l *Locale) Tables() int { return len(l.tables) }

// lookup is locale.get(group, {}).get(instance): the string, or nil.
func (l *Locale) lookup(table, id uint32) any {
	if s, ok := l.Text(table, id); ok {
		return s
	}
	return nil
}
