package mapc

import (
	"fmt"
	"math"
	"sort"
	"testing"
)

// TestPropTiltHoles: a single prop's tilt (its rules, its marker's rotDegrees x and y on both teams, read back as
// written) and a group's holes (their rules; the members inside a hole are left out). Taking a member out of a group
// as a single prop (a hole round it, a prop at its place) places what the group placed: noun, place, turn and scale
// on both teams.
func TestPropTiltHoles(t *testing.T) {
	group := &Group{Level: "SM_Map_v5", Sets: []string{"D_Rocks"}, At: Point{0, 0}, R: 10}
	for _, tc := range []struct {
		rule string
		p    Prop
	}{
		{"props.tilt", Prop{Noun: "rock", Pos: [3]float64{-150, -150}, Tilt: &[2]float64{200, 0}}},
		{"props.tilt", Prop{Group: group, Pos: [3]float64{-150, -150}, Tilt: &[2]float64{5, 0}}},
		{"props.group", Prop{Group: &Group{Level: "SM_Map_v5", Sets: []string{"D_Rocks"}, R: 10,
			Holes: [][]Point{pts(0, 0, 10, 0)}}, Pos: [3]float64{-150, -150}}},
		{"props.group", Prop{Group: &Group{Level: "SM_Map_v5", Sets: []string{"D_Rocks"}, R: 10,
			Holes: [][]Point{pts(0, 0, 900, 0, 0, 10)}}, Pos: [3]float64{-150, -150}}},
	} {
		m := ring(t)
		m.Props = []Prop{tc.p}
		if r, _ := Check(m, nil); !has(r, tc.rule, Error) || r.OK {
			t.Errorf("%s: not found in %+v", tc.rule, r.Findings)
		}
	}

	ad := propsAssetData(t)
	place := func(m *Map) []Placed {
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
		return pls
	}
	sig := func(pls []Placed) []string {
		var out []string
		for _, p := range pls {
			out = append(out, fmt.Sprintf("%s %d (%.3f %.3f %.3f) (%.3f %.3f %.3f) %.3f", p.Noun, p.Team, p.Pos[0],
				p.Pos[1], p.Pos[2], p.Rot[0], p.Rot[1], normDeg(p.Rot[2]), p.Scale))
		}
		sort.Strings(out)
		return out
	}
	// the group of the made-up rocks at (10, 10) and (14, 10), moved, turned 90 and scaled 1.5
	whole := func() *Map {
		m := ring(t)
		m.Props = []Prop{{Group: &Group{Level: TemplateLevel, Sets: []string{"D_Rocks"}, At: Point{12, 10}, R: 5},
			Pos: [3]float64{-120, -160, 1}, Rot: 90, Scale: 1.5}}
		return m
	}
	before := place(whole())
	if len(before) != 4 {
		t.Fatalf("the group placed %d markers", len(before))
	}
	// the rock at (14, 10) taken out: a hole round it (the level's coordinates), a single prop where it stood
	var taken Placed
	for _, p := range before {
		if p.Team == 1 && math.Abs(p.Pos[1]-(-157)) < 1e-3 {
			taken = p
		}
	}
	if taken.Noun == "" {
		t.Fatalf("no member at (-120, -157): %+v", before)
	}
	split := whole()
	split.Props[0].Group.Holes = [][]Point{pts(13, 9, 15, 9, 15, 11, 13, 11)}
	split.Props = append(split.Props, Prop{Noun: taken.Noun, Pos: taken.Pos, Rot: taken.Rot[2], Scale: taken.Scale})
	after := place(split)
	if a, b := sig(after), sig(before); fmt.Sprint(a) != fmt.Sprint(b) {
		t.Errorf("split:\n%v\nthe group:\n%v", a, b)
	}
	// a tilted single prop: rotDegrees (5, -3, rot) and its mirror (5, -3, rot + 180); written and read back
	m := ring(t)
	m.Props = []Prop{{Noun: "made_rock", Pos: [3]float64{-130, -120, 0.5}, Rot: 20, Tilt: &[2]float64{5, -3}}}
	js, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(js)
	if err != nil || back.Props[0].Tilt == nil || *back.Props[0].Tilt != [2]float64{5, -3} {
		t.Fatalf("%v %s", err, js)
	}
	pls := place(back)
	if len(pls) != 2 || pls[0].Rot != [3]float64{5, -3, 20} || pls[1].Rot != [3]float64{5, -3, -160} ||
		pls[1].Pos != [3]float64{130, 120, 0.5} {
		t.Errorf("%+v", pls)
	}
}
