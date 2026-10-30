package mapc

import (
	"bytes"
	"encoding/json"
	"strings"
)

// FormatMap writes a map as the hand-written packages are laid out (CM_Ring, server/testdata/maps): one member a line, each lane, shape,
// well and camp on a line of its own, points inline, long lines wrapped between points. It writes the members the
// import fills (format to ground; props and scatter only when there are some).
func FormatMap(m *Map) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("{\n")
	first := true
	member := func(indent, key, val string) {
		if !first {
			b.WriteString(",\n")
		}
		first = false
		b.WriteString(indent + `"` + key + `": ` + val)
	}
	str := func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return spaced(raw), nil
	}
	list := func(indent, key string, items []any) error {
		if len(items) == 0 {
			member(indent, key, "[]")
			return nil
		}
		var lines []string
		for _, it := range items {
			s, err := str(it)
			if err != nil {
				return err
			}
			lines = append(lines, wrapJSON(indent+"  ", s))
		}
		member(indent, key, "[\n"+strings.Join(lines, ",\n")+"\n"+indent+"]")
		return nil
	}
	for _, kv := range []struct {
		k string
		v any
	}{{"format", m.Format}, {"name", m.Name}, {"display", m.Display}, {"tag", m.Tag}, {"prefix", m.Prefix},
		{"size", m.Size}, {"symmetry", m.Symmetry}, {"environment", m.Environment}, {"tuning", m.Tuning}} {
		s, err := str(kv.v)
		if err != nil {
			return nil, err
		}
		member("  ", kv.k, s)
	}
	var lanes []any
	for _, l := range m.Lanes {
		lanes = append(lanes, l)
	}
	if err := list("  ", "lanes", lanes); err != nil {
		return nil, err
	}
	// base
	var base []string
	for _, kv := range []struct {
		k string
		v any
	}{{"locus", m.Base.Locus}, {"guardian", m.Base.Guardian}, {"shop", m.Base.Shop}, {"spawns", m.Base.Spawns},
		{"cores", m.Base.Cores}, {"bindings", m.Base.Bindings}} {
		s, err := str(kv.v)
		if err != nil {
			return nil, err
		}
		base = append(base, wrapJSON("    ", `"`+kv.k+`": `+s))
	}
	member("  ", "base", "{\n"+strings.Join(base, ",\n")+"\n  }")
	var wells, camps []any
	for _, w := range m.Wells {
		wells = append(wells, w)
	}
	for _, c := range m.Camps {
		camps = append(camps, c)
	}
	if err := list("  ", "wells", wells); err != nil {
		return nil, err
	}
	if err := list("  ", "camps", camps); err != nil {
		return nil, err
	}
	// terrain
	def, err := str(m.Terrain.Default)
	if err != nil {
		return nil, err
	}
	var shapes []string
	for _, s := range m.Terrain.Shapes {
		t, err := str(s)
		if err != nil {
			return nil, err
		}
		shapes = append(shapes, wrapJSON("      ", t))
	}
	terr := "{\n    \"default\": " + def + ",\n    \"shapes\": [\n" + strings.Join(shapes, ",\n") + "\n    ]\n  }"
	if len(shapes) == 0 {
		terr = "{\n    \"default\": " + def + ",\n    \"shapes\": []\n  }"
	}
	member("  ", "terrain", terr)
	if m.Graph != nil { // graph.go
		s, err := str(m.Graph)
		if err != nil {
			return nil, err
		}
		member("  ", "graph", s)
	}
	g, err := str(m.Ground)
	if err != nil {
		return nil, err
	}
	member("  ", "ground", g)
	if v := m.Void; v != nil { // void.go: a member a line
		var parts []string
		for _, kv := range []struct {
			k   string
			v   any
			set bool
		}{{"cut", v.Cut, v.Cut != 0}, {"backdrop", v.Backdrop, v.Backdrop != nil}, {"mist", v.Mist, v.Mist != nil},
			{"fog", v.Fog, v.Fog != nil}} {
			if !kv.set {
				continue
			}
			s, err := str(kv.v)
			if err != nil {
				return nil, err
			}
			parts = append(parts, wrapJSON("    ", `"`+kv.k+`": `+s))
		}
		if len(parts) == 0 {
			member("  ", "void", "{}")
		} else {
			member("  ", "void", "{\n"+strings.Join(parts, ",\n")+"\n  }")
		}
	}
	if m.Props != nil {
		var props []any
		for _, p := range m.Props {
			props = append(props, p)
		}
		if err := list("  ", "props", props); err != nil {
			return nil, err
		}
	}
	if len(m.Scatter) > 0 {
		var sc []any
		for _, s := range m.Scatter {
			sc = append(sc, s)
		}
		if err := list("  ", "scatter", sc); err != nil {
			return nil, err
		}
	}
	if len(m.Wards) > 0 { // wards.go
		var ws []any
		for _, w := range m.Wards {
			ws = append(ws, w)
		}
		if err := list("  ", "wards", ws); err != nil {
			return nil, err
		}
	}
	b.WriteString("\n}\n")
	return b.Bytes(), nil
}

// spaced is compact JSON with a space after every colon and comma outside strings.
func spaced(raw []byte) string {
	var b strings.Builder
	in, esc := false, false
	for _, c := range string(raw) {
		b.WriteRune(c)
		switch {
		case esc:
			esc = false
		case in && c == '\\':
			esc = true
		case c == '"':
			in = !in
		case !in && (c == ':' || c == ','):
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// wrapLimit is the longest line wrapJSON writes where it can break.
const wrapLimit = 118

// wrapJSON writes s at indent, broken after "], " (between points) where a line would pass wrapLimit; the next
// lines are indented two more.
func wrapJSON(indent, s string) string {
	var lines []string
	cur := indent
	for len(s) > 0 {
		k := strings.Index(s, "], ")
		var piece string
		if k < 0 {
			piece, s = s, ""
		} else {
			piece, s = s[:k+3], s[k+3:]
		}
		if len(cur)+len(strings.TrimRight(piece, " ")) > wrapLimit && strings.TrimSpace(cur) != "" {
			lines = append(lines, strings.TrimRight(cur, " "))
			cur = indent + "  "
		}
		cur += piece
	}
	lines = append(lines, strings.TrimRight(cur, " "))
	return strings.Join(lines, "\n")
}
