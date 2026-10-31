// Package textjson edits a JSON text while keeping its layout: Merge takes the file as it is on disk and the
// document as the editor holds it, and writes the file anew with every unchanged value's bytes kept as they were
// (members' order, spacing, line breaks, number spellings, members the editor doesn't know). Only values that
// differ are written fresh, in the file's style: inline where they fit, one per line otherwise.
//
// So an unchanged document gives back the same bytes, and a moved point changes only its two numbers.
package textjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Node is one parsed value and where it is in the text.
type Node struct {
	Kind       byte // 'o' object, 'a' array, 's' string, 'n' number, 't' true, 'f' false, 'z' null
	Start, End int  // the value's bytes: text[Start:End]
	Keys       []string
	KeyStart   []int   // where each member's key (its opening quote) starts
	Vals       []*Node // the members' values, in the text's order
	Elems      []*Node
	Str        string
	Num        float64
}

// Member returns the value of the object's last member named key, or nil.
func (n *Node) Member(key string) *Node {
	for i := len(n.Keys) - 1; i >= 0; i-- {
		if n.Keys[i] == key {
			return n.Vals[i]
		}
	}
	return nil
}

type parser struct {
	s   []byte
	pos int
}

// Parse parses a JSON text strictly (one value, surrounded by whitespace only).
func Parse(text []byte) (*Node, error) {
	p := &parser{s: text}
	p.ws()
	n, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.pos != len(p.s) {
		return nil, p.errf("text after the value")
	}
	return n, nil
}

func (p *parser) errf(format string, a ...any) error {
	line := 1 + bytes.Count(p.s[:p.pos], []byte{'\n'})
	return fmt.Errorf("JSON line %d (byte %d): %s", line, p.pos, fmt.Sprintf(format, a...))
}

func (p *parser) ws() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) value(depth int) (*Node, error) {
	if depth > 200 {
		return nil, p.errf("nested too deep")
	}
	if p.pos >= len(p.s) {
		return nil, p.errf("unexpected end")
	}
	n := &Node{Start: p.pos}
	switch c := p.s[p.pos]; {
	case c == '{':
		n.Kind = 'o'
		p.pos++
		p.ws()
		if p.pos < len(p.s) && p.s[p.pos] == '}' {
			p.pos++
			break
		}
		for {
			p.ws()
			if p.pos >= len(p.s) || p.s[p.pos] != '"' {
				return nil, p.errf("a member's name expected")
			}
			ks := p.pos
			k, err := p.str()
			if err != nil {
				return nil, err
			}
			p.ws()
			if p.pos >= len(p.s) || p.s[p.pos] != ':' {
				return nil, p.errf("':' expected")
			}
			p.pos++
			p.ws()
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.Keys, n.KeyStart, n.Vals = append(n.Keys, k), append(n.KeyStart, ks), append(n.Vals, v)
			p.ws()
			if p.pos < len(p.s) && p.s[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.pos < len(p.s) && p.s[p.pos] == '}' {
				p.pos++
				break
			}
			return nil, p.errf("',' or '}' expected")
		}
	case c == '[':
		n.Kind = 'a'
		p.pos++
		p.ws()
		if p.pos < len(p.s) && p.s[p.pos] == ']' {
			p.pos++
			break
		}
		for {
			p.ws()
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.Elems = append(n.Elems, v)
			p.ws()
			if p.pos < len(p.s) && p.s[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.pos < len(p.s) && p.s[p.pos] == ']' {
				p.pos++
				break
			}
			return nil, p.errf("',' or ']' expected")
		}
	case c == '"':
		n.Kind = 's'
		s, err := p.str()
		if err != nil {
			return nil, err
		}
		n.Str = s
	case c == 't' || c == 'f' || c == 'n':
		for _, lit := range []string{"true", "false", "null"} {
			if bytes.HasPrefix(p.s[p.pos:], []byte(lit)) {
				n.Kind = map[string]byte{"true": 't', "false": 'f', "null": 'z'}[lit]
				p.pos += len(lit)
				break
			}
		}
		if n.Kind == 0 {
			return nil, p.errf("unknown literal")
		}
	case c == '-' || (c >= '0' && c <= '9'):
		n.Kind = 'n'
		e := p.pos
		for e < len(p.s) && strings.IndexByte("+-0123456789.eE", p.s[e]) >= 0 {
			e++
		}
		lit := string(p.s[p.pos:e])
		if !json.Valid([]byte(lit)) {
			return nil, p.errf("bad number %q", lit)
		}
		f, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			return nil, p.errf("bad number %q", lit)
		}
		n.Num = f
		p.pos = e
	default:
		return nil, p.errf("unexpected %q", c)
	}
	n.End = p.pos
	return n, nil
}

// str reads a string literal at pos (its opening quote) and decodes it.
func (p *parser) str() (string, error) {
	s := p.pos
	p.pos++
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case '\\':
			p.pos += 2
			continue
		case '"':
			p.pos++
			var out string
			if err := json.Unmarshal(p.s[s:p.pos], &out); err != nil {
				p.pos = s
				return "", p.errf("bad string: %v", err)
			}
			return out, nil
		}
		p.pos++
	}
	return "", p.errf("unterminated string")
}

// Equal reports whether two values mean the same: numbers by value, objects regardless of their members' order.
func Equal(a, b *Node) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case 'n':
		return a.Num == b.Num
	case 's':
		return a.Str == b.Str
	case 'a':
		if len(a.Elems) != len(b.Elems) {
			return false
		}
		for i := range a.Elems {
			if !Equal(a.Elems[i], b.Elems[i]) {
				return false
			}
		}
		return true
	case 'o':
		ka, kb := distinct(a), distinct(b)
		if len(ka) != len(kb) {
			return false
		}
		for k := range ka {
			if _, ok := kb[k]; !ok || !Equal(a.Member(k), b.Member(k)) {
				return false
			}
		}
		return true
	}
	return true
}

func distinct(n *Node) map[string]struct{} {
	m := make(map[string]struct{}, len(n.Keys))
	for _, k := range n.Keys {
		m[k] = struct{}{}
	}
	return m
}

// Width is the line length up to which a new value is written inline.
const Width = 118

type merger struct {
	old, new []byte
	nl       string
}

// Merge returns newText's document written in oldText's layout (see the package comment). Both must parse.
func Merge(oldText, newText []byte) ([]byte, error) {
	o, err := Parse(oldText)
	if err != nil {
		return nil, fmt.Errorf("the file: %w", err)
	}
	n, err := Parse(newText)
	if err != nil {
		return nil, fmt.Errorf("the document: %w", err)
	}
	m := &merger{old: oldText, new: newText, nl: "\n"}
	if bytes.Contains(oldText, []byte("\r\n")) {
		m.nl = "\r\n"
	}
	var out bytes.Buffer
	out.Write(oldText[:o.Start])
	out.WriteString(m.merge(o, n))
	out.Write(oldText[o.End:])
	return out.Bytes(), nil
}

func (m *merger) span(n *Node) string { return string(m.old[n.Start:n.End]) }

// indentAt is the leading whitespace of the line that holds byte pos, and pos's column.
func (m *merger) indentAt(pos int) (string, int) {
	ls := bytes.LastIndexByte(m.old[:pos], '\n') + 1
	e := ls
	for e < pos && (m.old[e] == ' ' || m.old[e] == '\t') {
		e++
	}
	return string(m.old[ls:e]), pos - ls
}

func (m *merger) merge(o, n *Node) string {
	if Equal(o, n) {
		return m.span(o)
	}
	switch {
	case o.Kind == 'o' && n.Kind == 'o':
		return m.object(o, n)
	case o.Kind == 'a' && n.Kind == 'a':
		return m.array(o, n)
	}
	ind, col := m.indentAt(o.Start)
	return m.format(n, ind, col)
}

type item struct {
	orig int // the original member's or element's index, or -1 for a new one
	text string
}

// join puts items between open and close; seps[i] is the text that followed original item i.
func join(items []item, open, close string, seps []string, def string) string {
	var b strings.Builder
	b.WriteString(open)
	for k, it := range items {
		if k > 0 {
			prev, next := items[k-1].orig, it.orig
			switch {
			case prev >= 0 && next > prev:
				b.WriteString(seps[next-1]) // after a deleted one, the separator before the next is kept
			case prev >= 0 && next < 0 && prev < len(seps):
				b.WriteString(seps[prev])
			default:
				b.WriteString(def)
			}
		}
		b.WriteString(it.text)
	}
	b.WriteString(close)
	return b.String()
}

// layout returns the open, close and separator texts of an original container with children spanning
// [starts[i], ends[i]], and the separator for a new neighbour.
func (m *merger) layout(o *Node, starts, ends []int) (open, close string, seps []string, def string) {
	last := len(starts) - 1
	open = string(m.old[o.Start:starts[0]])
	close = string(m.old[ends[last]:o.End])
	for i := 0; i < last; i++ {
		seps = append(seps, string(m.old[ends[i]:starts[i+1]]))
	}
	switch {
	case len(seps) > 0: // the most common one
		count := map[string]int{}
		for _, sep := range seps {
			count[sep]++
			if count[sep] >= count[def] {
				def = sep
			}
		}
	case strings.Contains(open, "\n"):
		def = "," + open[strings.LastIndex(open, "\n"):]
	default:
		def = ", "
	}
	return open, close, seps, def
}

// childIndent is the indentation a new member or element gets inside o.
func (m *merger) childIndent(o *Node, def string) (string, int) {
	if i := strings.LastIndex(def, "\n"); i >= 0 {
		ind := def[i+1:]
		return ind, len(ind)
	}
	ind, col := m.indentAt(o.Start)
	return ind, col + 2
}

func (m *merger) object(o, n *Node) string {
	if len(o.Keys) == 0 {
		ind, col := m.indentAt(o.Start)
		return m.format(n, ind, col)
	}
	used := map[string]bool{}
	var items []item
	for i, k := range o.Keys {
		nv := n.Member(k)
		if nv == nil || used[k] {
			continue
		}
		used[k] = true
		items = append(items, item{i, string(m.old[o.KeyStart[i]:o.Vals[i].Start]) + m.merge(o.Vals[i], nv)})
	}
	starts, ends := o.KeyStart, make([]int, len(o.Vals))
	for i, v := range o.Vals {
		ends[i] = v.End
	}
	open, close, seps, def := m.layout(o, starts, ends)
	colon := string(m.old[o.KeyStart[0]:o.Vals[0].Start])
	colon = colon[strings.LastIndexByte(colon, '"')+1:] // ": " as the file writes it
	ind, col := m.childIndent(o, def)
	for i, k := range n.Keys {
		if used[k] || n.Member(k) != n.Vals[i] {
			continue
		}
		used[k] = true
		key := quote(k)
		items = append(items, item{-1, key + colon + m.format(n.Vals[i], ind, col+len(key)+len(colon))})
	}
	if len(items) == 0 {
		return "{}"
	}
	return m.fit(o, n, join(items, open, close, seps, def))
}

// fit returns a merged container, or the container written fresh when the merge made a line too long that wasn't,
// or turned a container written on one line into several (MAPED-EDIT5: the template's `"ground": {"style":
// "tiled"}` merged with painted layers kept its inline start and wrapped only inside "layers").
func (m *merger) fit(o, n *Node, merged string) string {
	ind, col := m.indentAt(o.Start)
	if longest(merged, col) > Width && longest(m.span(o), col) <= Width {
		return m.format(n, ind, col)
	}
	if !strings.Contains(m.span(o), "\n") && strings.Contains(merged, "\n") {
		return m.format(n, ind, col)
	}
	return merged
}

func longest(s string, col int) int {
	max := 0
	for i, line := range strings.Split(s, "\n") {
		l := len(strings.TrimRight(line, "\r"))
		if i == 0 {
			l += col
		}
		if l > max {
			max = l
		}
	}
	return max
}

func (m *merger) array(o, n *Node) string {
	if len(o.Elems) == 0 {
		ind, col := m.indentAt(o.Start)
		return m.format(n, ind, col)
	}
	var items []item
	if len(o.Elems) == len(n.Elems) {
		for i := range n.Elems {
			items = append(items, item{i, m.merge(o.Elems[i], n.Elems[i])})
		}
	} else {
		match := lcs(o.Elems, n.Elems) // match[j] = the original element new element j equals, or -1
		// between two matches, pair the unmatched new elements with the unmatched old ones in order
		var pairs = make([]int, len(n.Elems))
		lastOld := -1
		for j := 0; j < len(n.Elems); {
			if match[j] >= 0 {
				pairs[j] = match[j]
				lastOld = match[j]
				j++
				continue
			}
			e := j
			for e < len(n.Elems) && match[e] < 0 {
				e++
			}
			nextOld := len(o.Elems)
			if e < len(n.Elems) {
				nextOld = match[e]
			}
			free := lastOld + 1
			for k := j; k < e; k++ {
				if free < nextOld {
					pairs[k] = free
					free++
				} else {
					pairs[k] = -1
				}
			}
			j = e
		}
		for j, nv := range n.Elems {
			if i := pairs[j]; i >= 0 {
				items = append(items, item{i, m.merge(o.Elems[i], nv)})
			} else {
				items = append(items, item{-1, ""}) // written below, once the layout is known
			}
		}
	}
	starts, ends := make([]int, len(o.Elems)), make([]int, len(o.Elems))
	for i, e := range o.Elems {
		starts[i], ends[i] = e.Start, e.End
	}
	open, close, seps, def := m.layout(o, starts, ends)
	ind, col := m.childIndent(o, def)
	for j := range items {
		if items[j].orig < 0 {
			items[j].text = m.format(n.Elems[j], ind, col)
		}
	}
	if len(items) == 0 {
		return "[]"
	}
	return m.fit(o, n, join(items, open, close, seps, def))
}

// lcs matches the new elements to equal old ones, keeping their order (a longest common subsequence).
func lcs(a, b []*Node) []int {
	la, lb := len(a), len(b)
	t := make([][]int, la+1)
	for i := range t {
		t[i] = make([]int, lb+1)
	}
	for i := la - 1; i >= 0; i-- {
		for j := lb - 1; j >= 0; j-- {
			switch {
			case Equal(a[i], b[j]):
				t[i][j] = t[i+1][j+1] + 1
			case t[i+1][j] >= t[i][j+1]:
				t[i][j] = t[i+1][j]
			default:
				t[i][j] = t[i][j+1]
			}
		}
	}
	match := make([]int, lb)
	for j := range match {
		match[j] = -1
	}
	for i, j := 0, 0; i < la && j < lb; {
		switch {
		case Equal(a[i], b[j]):
			match[j] = i
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			i++
		default:
			j++
		}
	}
	return match
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// inline writes a new value on one line, in the ", " and ": " style; scalars as the document spells them.
func (m *merger) inline(n *Node) string {
	switch n.Kind {
	case 'o':
		parts := make([]string, 0, len(n.Keys))
		for i, k := range n.Keys {
			parts = append(parts, quote(k)+": "+m.inline(n.Vals[i]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case 'a':
		parts := make([]string, 0, len(n.Elems))
		for _, e := range n.Elems {
			parts = append(parts, m.inline(e))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return string(m.new[n.Start:n.End])
}

// format writes a new value that starts at column col of a line indented by ind.
func (m *merger) format(n *Node, ind string, col int) string {
	in := m.inline(n)
	if col+len(in) <= Width || (n.Kind != 'o' && n.Kind != 'a') {
		return in
	}
	ind2 := ind + "  "
	if n.Kind == 'o' {
		parts := make([]string, 0, len(n.Keys))
		for i, k := range n.Keys {
			key := quote(k) + ": "
			parts = append(parts, key+m.format(n.Vals[i], ind2, len(ind2)+len(key)))
		}
		return "{" + m.nl + ind2 + strings.Join(parts, ","+m.nl+ind2) + m.nl + ind + "}"
	}
	small := true
	for _, e := range n.Elems {
		small = small && len(m.inline(e)) <= 32
	}
	if small { // packed: as many per line as fit, continued one step in
		var b strings.Builder
		b.WriteString("[")
		c := col + 1
		for i, e := range n.Elems {
			s := m.inline(e)
			if i > 0 {
				if c+2+len(s)+1 > Width {
					b.WriteString("," + m.nl + ind2)
					c = len(ind2)
				} else {
					b.WriteString(", ")
					c += 2
				}
			}
			b.WriteString(s)
			c += len(s)
		}
		return b.String() + "]"
	}
	parts := make([]string, 0, len(n.Elems))
	for _, e := range n.Elems {
		parts = append(parts, m.format(e, ind2, len(ind2)))
	}
	return "[" + m.nl + ind2 + strings.Join(parts, ","+m.nl+ind2) + m.nl + ind + "]"
}

// SetMembers sets top-level members of an object text to the given JSON values, keeping the rest of the text:
// an existing member's value is replaced in place, a missing one is added at the end.
func SetMembers(text []byte, set map[string]string, order []string) ([]byte, error) {
	root, err := Parse(text)
	if err != nil {
		return nil, err
	}
	if root.Kind != 'o' {
		return nil, fmt.Errorf("the document isn't an object")
	}
	// build the new document as text and merge it, so new members get the file's layout
	var b strings.Builder
	b.WriteString("{")
	done := map[string]bool{}
	first := true
	put := func(k, v string) {
		if !first {
			b.WriteString(", ")
		}
		first = false
		b.WriteString(quote(k) + ": " + v)
	}
	for _, k := range root.Keys {
		if done[k] {
			continue
		}
		done[k] = true
		if v, ok := set[k]; ok {
			put(k, v)
		} else {
			v := root.Member(k)
			put(k, string(text[v.Start:v.End]))
		}
	}
	for _, k := range order {
		if !done[k] {
			if v, ok := set[k]; ok {
				done[k] = true
				put(k, v)
			}
		}
	}
	b.WriteString("}")
	return Merge(text, []byte(b.String()))
}
