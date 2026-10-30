package mapc

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// checkMines: each team's well in the built followers markerset is nounFmt's (%s the team's colour) with n worker
// spots (its children, all in the same set), each lo to hi metres from it.
func checkMines(t *testing.T, as *MapAssets, name, nounFmt string, n int, lo, hi float64) {
	t.Helper()
	fol := markers(as.Values[name+"_"+setFollowers+".Markerset"])
	byID := map[uint64]int{}
	for k, mk := range fol {
		byID[uval(mk, "markerId")] = k
	}
	for _, colour := range teamColour {
		noun := fmt.Sprintf(nounFmt, colour)
		found := 0
		for _, mk := range fol {
			if !strings.EqualFold(text(mk, "nounDef"), noun) {
				continue
			}
			found++
			w := vec(mk, "pos")
			kids := vec(mk, "childMarkerIds")
			if len(kids) != n {
				t.Errorf("%s: %d worker spots, %d expected", noun, len(kids), n)
			}
			for _, c := range kids {
				k, ok := byID[uint64(c)]
				if !ok {
					t.Errorf("%s: spot %d not in the followers set", noun, uint64(c))
					continue
				}
				s := vec(fol[k], "pos")
				if d := dist(Point{s[0], s[1]}, Point{w[0], w[1]}); d < lo || d > hi {
					t.Errorf("%s: spot %d %.1f m from it, %g to %g expected", noun, uint64(c), d, lo, hi)
				}
			}
		}
		if found != 1 {
			t.Errorf("%d %s markers, 1 expected", found, noun)
		}
	}
}

// TestMineMadeUp: CM_Ring with its well of kind mine places MineLevel's mine with its spots (team 1's first spot's
// look-at too) instead of SM's well; kind "well" builds the same bytes as no kind; CM_Ring itself still places SM's.
func TestMineMadeUp(t *testing.T) {
	ad := madeUpAssetData(t)
	build := func(kind string) (*Map, *MapAssets) {
		m := ring(t)
		m.Wells[0].Kind = kind
		r, cm := Check(m, nil)
		if !r.OK {
			t.Fatalf("kind %q: %+v", kind, r.Findings)
		}
		as, err := cm.Assets(ad, madeUpBounds)
		if err != nil {
			t.Fatal(err)
		}
		checkAssets(t, m, as)
		return m, as
	}
	m, mine := build(WellMine)
	checkMines(t, mine, m.Name, "gold_%s_side_MM.Noun", 3, 15.9, 17.1)
	fol := markers(mine.Values[m.Name+"_"+setFollowers+".Markerset"])
	// team 1: the mine, its first spot and that one's look-at, 2 spots; team 2: the mine and 3 spots
	if len(fol) != 9 || uval(fol[1], "targetMarkerId") != uval(fol[2], "markerId") {
		t.Errorf("%d followers, the first spot's target %d", len(fol), uval(fol[1], "targetMarkerId"))
	}
	w, s := vec(fol[0], "pos"), vec(fol[1], "pos")
	if dx, dy := s[0]-w[0], s[1]-w[1]; dx != 16 || dy != 0 {
		t.Errorf("the first spot stands (%v, %v) off the mine, (16, 0) expected", dx, dy)
	}
	_, none := build("")
	_, well := build(WellSpirit)
	if n := text(markers(none.Values[m.Name+"_"+setFollowers+".Markerset"])[0], "nounDef"); n != "gold_blue_side.Noun" {
		t.Errorf("CM_Ring's well is %s", n) // its spots are TestAssetsMadeUp's
	}
	for _, n := range none.Names {
		if !bytes.Equal(none.Files[n], well.Files[n]) {
			t.Errorf("%s: kind %q builds other bytes than none", n, WellSpirit)
		}
	}
}

// TestWellKind: the kind is checked (wells.kind), left out of the text when none, and kept through the text.
func TestWellKind(t *testing.T) {
	m := ring(t)
	m.Wells[0].Kind = "pit"
	if r, _ := Check(m, nil); !has(r, "wells.kind", Error) {
		t.Errorf("kind pit: %+v", r.Findings)
	}
	m.Wells[0].Kind = ""
	text, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(text, []byte(`"kind": ""`)) || bytes.Contains(text, []byte(`{"kind"`)) {
		t.Errorf("a well without a kind names one:\n%s", text)
	}
	m.Wells[0].Kind = WellMine
	text, err = FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Wells[0].IsMine() || !bytes.Contains(text, []byte(`{"kind": "mine", "pos": [`)) {
		t.Errorf("the kind lost: %+v", back.Wells)
	}
}
