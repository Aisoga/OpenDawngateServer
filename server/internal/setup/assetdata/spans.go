package assetdata

import "fmt"

// ElemSpan is where one array element lies in a file: its fixed bytes at Off and its out-of-line data from Start to
// End.
type ElemSpan struct{ Off, Start, End int }

// ArraySpans is where a top-level array member's data lies in a file: its elements (ESize fixed bytes each, back to
// back) and End, the end of the whole array's out-of-line data (what follows belongs to the members after it).
type ArraySpans struct {
	ESize int
	Elems []ElemSpan
	End   int
}

// Spans walks a file of type t the way Parse does (the client's cursor over the out-of-line data) and returns where
// the elements of its top-level array member named member lie. The file must parse completely. The elements must be
// structs.
func (ts *Types) Spans(t *Type, data []byte, member string) (*ArraySpans, error) {
	if _, _, err := ts.Parse(t, data, nil); err != nil {
		return nil, err
	}
	r := &reader{d: data, ts: ts}
	if _, err := r.take(t.Size); err != nil {
		return nil, err
	}
	for i := range t.Members {
		m := &t.Members[i]
		if m.Name != member {
			if _, err := r.value(m.Type, m.Offset, m, m.Name, m.Count); err != nil {
				return nil, err
			}
			continue
		}
		et := ts.Type(m.Elem)
		if m.Type != "array" || !m.HasElem || et == nil {
			return nil, fmt.Errorf("assetdata: %s.%s is no array of structs", t.Name, member)
		}
		delta := m.Delta
		if delta == 0 {
			delta = 4
		}
		n, err := r.u32(m.Offset + delta)
		if err != nil {
			return nil, err
		}
		out := &ArraySpans{ESize: m.ESize}
		if n == 0 {
			out.End = r.pos
			return out, nil
		}
		base, err := r.take(int(n) * m.ESize)
		if err != nil {
			return nil, err
		}
		for k := 0; k < int(n); k++ {
			start := r.pos
			if _, err := r.structAt(et, base+k*m.ESize, fmt.Sprintf("%s[%d]", member, k)); err != nil {
				return nil, err
			}
			out.Elems = append(out.Elems, ElemSpan{Off: base + k*m.ESize, Start: start, End: r.pos})
		}
		out.End = r.pos
		return out, nil
	}
	return nil, fmt.Errorf("assetdata: %s has no member %s", t.Name, member)
}
