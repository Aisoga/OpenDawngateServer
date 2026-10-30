package mapc

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
)

// TestPropOnce: a prop with "mirror": false stands once, where it is authored, on either half: a group's members
// once each (team 0) where the level has them, a noun once, a blocking noun's footprint stamped once; the rule that
// keeps authored props on team 1's half doesn't apply to it (it does to a mirrored one). Written and read back; a
// member split out of such a group as a single prop that stands once places what the group placed.
func TestPropOnce(t *testing.T) {
	ad := propsAssetData(t)
	no := false
	place := func(m *Map) ([]Placed, *Compiled) {
		t.Helper()
		if err := ResolveProps(m, ad); err != nil {
			t.Fatal(err)
		}
		r, cm := Check(m, nil)
		if !r.OK {
			t.Fatalf("%+v", r.Findings)
		}
		pls, _, err := cm.PlacedProps(ad)
		if err != nil {
			t.Fatal(err)
		}
		return pls, cm
	}
	sig := func(pls []Placed) string {
		var out []string
		for _, p := range pls {
			out = append(out, fmt.Sprintf("%s %d (%.3f %.3f %.3f) %.3f %.3f", p.Noun, p.Team, p.Pos[0], p.Pos[1],
				p.Pos[2], normDeg(p.Rot[2]), p.Scale))
		}
		sort.Strings(out)
		return fmt.Sprint(out)
	}
	// the made-up rocks at (10, 10) and (14, 10): team 2's half, where the level has them
	group := func() *Map {
		m := ring(t)
		m.Props = []Prop{{Group: &Group{Level: TemplateLevel, Sets: []string{"D_Rocks"}, At: Point{12, 10}, R: 5},
			Pos: [3]float64{12, 10}, Scale: 1, Mirror: &no}}
		return m
	}
	m := group()
	m.Props[0].Mirror = nil
	if r, _ := Check(m, nil); !has(r, "symmetry.half", Error) {
		t.Errorf("a mirrored group on team 2's half: %+v", r.Findings)
	}
	m = group()
	js, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(js)
	if err != nil || !bytes.Contains(js, []byte(`"mirror": false`)) || !back.Props[0].Once() {
		t.Fatalf("%v %s", err, js)
	}
	whole, _ := place(back)
	var taken Placed
	for _, p := range whole {
		if p.Team != 0 {
			t.Errorf("%+v doesn't stand once", p)
		}
		if p.Pos[0] == 14 && p.Pos[1] == 10 {
			taken = p
		}
	}
	if len(whole) != 2 || taken.Noun == "" {
		t.Fatalf("the group placed %s", sig(whole))
	}
	// split: a hole round the rock at (14, 10), a single prop that stands once where it stood
	split := group()
	split.Props[0].Group.Holes = [][]Point{pts(13, 9, 15, 9, 15, 11, 13, 11)}
	split.Props = append(split.Props, Prop{Noun: taken.Noun, Pos: taken.Pos, Rot: taken.Rot[2], Scale: taken.Scale,
		Mirror: &no})
	if after, _ := place(split); sig(after) != sig(whole) {
		t.Errorf("split %s, the group %s", sig(after), sig(whole))
	}
	// a blocking noun: stamped once on team 2's half; mirrored on team 1's half it stamps both
	k := ring(t).Lanes[0].Knots[1]
	solid := func(p Prop) int {
		m := ring(t)
		m.Props = []Prop{p}
		cm, err := Compile(m)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, v := range cm.Layers.Kind {
			if v == Solid {
				n++
			}
		}
		return n
	}
	fp := &[2]float64{6, 6}
	once := solid(Prop{Noun: "made_rock", Pos: [3]float64{-k[0], -k[1]}, Blocks: true, Footprint: fp, Mirror: &no})
	both := solid(Prop{Noun: "made_rock", Pos: [3]float64{k[0], k[1]}, Blocks: true, Footprint: fp})
	if once == 0 || both != 2*once {
		t.Errorf("solid cells: %d once, %d mirrored", once, both)
	}
}
