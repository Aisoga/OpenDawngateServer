package tunnel

import "testing"

func TestTrimMTUProbe(t *testing.T) {
	probe := func(n int) []byte {
		p := make([]byte, n)
		p[0] = 0x05
		copy(p[1:], rakNetOfflineMagic)
		p[17] = 6
		return p
	}
	if got, ok := TrimMTUProbe(probe(1464)); !ok || len(got) != 1172 {
		t.Errorf("1492 probe: %d bytes, %v", len(got), ok)
	}
	for _, n := range []int{1172, 548, MaxPayload} { // fits already
		if got, ok := TrimMTUProbe(probe(n)); ok || len(got) != n {
			t.Errorf("%d-byte probe changed", n)
		}
	}
	p := probe(1464)
	p[1300] = 1 // not padding
	if _, ok := TrimMTUProbe(p); ok {
		t.Error("a request with data past 1172 was cut")
	}
	q := probe(1464)
	q[0] = 0x07
	if _, ok := TrimMTUProbe(q); ok {
		t.Error("another message was cut")
	}
}
