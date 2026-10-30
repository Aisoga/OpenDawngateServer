package mapc

import (
	"bytes"
	"strings"
	"testing"
)

// TestGraphCell: the graph section sets the squares' size (CM_Ring's budget picks 3 cells); out of range or over
// MaxAreas it fails; without it, or with cell 0, the budget picks as before.
func TestGraphCell(t *testing.T) {
	m := ring(t)
	r, cm := Check(m, nil)
	if !r.OK || cm.Cell != 3 {
		t.Fatalf("CM_Ring: cell %d, %+v", cm.Cell, r.Findings)
	}
	auto := cm.Player.Areas
	m.Graph = &Graph{}
	if r, cm := Check(m, nil); !r.OK || len(r.Findings) != 0 || cm.Cell != 3 || cm.Player.Areas != auto {
		t.Errorf("cell 0: cell %d, %d areas, %+v", cm.Cell, cm.Player.Areas, r.Findings)
	}
	m.Graph = &Graph{Cell: 4}
	r, cm = Check(m, nil)
	if !r.OK || len(r.Findings) != 0 || cm.Cell != 4 || cm.Player.Areas >= auto {
		t.Errorf("cell 4: cell %d, %d areas (auto %d), %+v", cm.Cell, cm.Player.Areas, auto, r.Findings)
	}
	if s := cm.NavSpec(); s.Cell != 4 {
		t.Errorf("the archive's graphs: squares of %d cells", s.Cell)
	}
	for _, c := range []struct {
		cell int
		rule string
	}{{1, "graph.cell"}, {5, "graph.cell"}, {-2, "graph.cell"}, {2, "graph.areas"}} {
		m := ring(t)
		m.Graph = &Graph{Cell: c.cell}
		r, cm := Check(m, nil)
		if !has(r, c.rule, Error) || r.OK {
			t.Errorf("cell %d: %s not found in %+v", c.cell, c.rule, r.Findings)
		}
		if c.rule == "graph.areas" && (cm == nil || cm.Cell != 2 || cm.Player.Areas <= MaxAreas || has(r, "budget.areas", Error)) {
			t.Errorf("cell 2: %+v", r.Findings)
		}
	}
}

// TestGraphFormat: written after terrain on one line, read back the same; a map without one writes none.
func TestGraphFormat(t *testing.T) {
	m := ring(t)
	plain, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte(`"graph"`)) {
		t.Errorf("a map without a graph section writes one")
	}
	m.Graph = &Graph{Cell: 4}
	text, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "  },\n  \"graph\": {\"cell\": 4},\n  \"ground\": ") {
		t.Errorf("the graph written:\n%s", text)
	}
	back, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if back.Graph == nil || *back.Graph != *m.Graph {
		t.Errorf("read back %+v", back.Graph)
	}
	if _, err := Parse(bytes.Replace(text, []byte(`{"cell": 4}`), []byte(`{"cell": 4, "size": 1}`), 1)); err == nil {
		t.Errorf("an unknown member of graph was read")
	}
}
