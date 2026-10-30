package mapc

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"dawngate/server/internal/setup/assetdata"
	"dawngate/server/internal/setup/mapmm/ground"
	"dawngate/server/internal/setup/snap"
)

// fordAssetData is propsAssetData with a water noun and a made-up level's water markerset: a ford by the centre
// (z -0.22, 30 x 60 m turned 45 degrees, no mask, as MM_Map_v3's), a deep plane, a shallow masked one and a shallow
// one away from the centre.
func fordAssetData(t *testing.T) []byte {
	t.Helper()
	ts, err := assetdata.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	a, err := snap.Parse(propsAssetData(t))
	if err != nil {
		t.Fatal(err)
	}
	var files []snap.File
	for _, e := range a.Entries {
		b, err := a.Read(e)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, snap.File{Name: e.Name, Data: b})
	}
	write := func(asset string, o *assetdata.Object) {
		b, err := ts.Write(ts.Type(classOfAsset(asset)), o, assetdata.WriteOptions{})
		if err != nil {
			t.Fatalf("%s: %v", asset, err)
		}
		files = append(files, snap.File{Name: AssetEntry(asset), Data: b})
	}
	wd := func(w, l float32, mask string) *assetdata.Object {
		return obj("mWaterType", "Water", "size", []any{w, l}, "mask", mask, "enable", true)
	}
	turn := func(deg float32) []any { return []any{float32(0), float32(0), deg} }
	write("Water.Noun", obj("nounType", "Water", "graphicsScale", float32(1)))
	write("Made_Level_Water.Markerset", obj("markers", []any{
		madeUpMarker("Water.Noun", 1401, -0.9, 0.9, -0.22, "rotDegrees", turn(45), "waterData", wd(30, 60, "")),
		madeUpMarker("Water.Noun", 1402, -60, -60, -2.9, "rotDegrees", turn(0), "waterData", wd(20, 20, "")),
		madeUpMarker("Water.Noun", 1403, -80, -40, -0.3, "rotDegrees", turn(0), "waterData",
			wd(10, 10, `Environments\anshavar\textures\Circle_wm.dds`)),
		madeUpMarker("Water.Noun", 1404, -40, -80, -0.3, "rotDegrees", turn(30), "waterData", wd(10, 20, "")),
	}, "group", "none", "weight", float32(1)))
	return snap.Pack(files)
}

// TestFordMadeUp: a group's water planes from made-up assets: the ford whose plane overlaps its mirror's stands once
// (team 0, where it is), the others on both sides; the shallow unmasked planes are fords (the ford's and both sides'
// of the other), the deep and the masked plane aren't; the ground model's walkable floor dips under a ford by its
// depth (FordWater under the water) and nowhere else; a map without water props has no dip and builds as before.
func TestFordMadeUp(t *testing.T) {
	ad := fordAssetData(t)
	plain := ring(t)
	if err := ResolveProps(plain, ad); err != nil {
		t.Fatal(err)
	}
	if _, cm := Check(plain, nil); cm == nil || len(cm.Fords()) != 0 || cm.GroundSpec().Dips != nil {
		t.Errorf("a map without props has fords")
	}
	m := ring(t)
	m.Props = []Prop{{Group: &Group{Level: "Made_Level", Sets: []string{"Water"}, At: Point{-50, -50}, R: 80},
		Pos: [3]float64{-50, -50, 0}}}
	if err := ResolveProps(m, ad); err != nil {
		t.Fatal(err)
	}
	r, cm := Check(m, nil)
	if !r.OK || len(r.Findings) != 0 {
		t.Fatalf("%+v", r.Findings)
	}
	pls, fs, err := cm.PlacedProps(ad)
	if err != nil || len(fs) != 0 {
		t.Fatalf("%v %+v", err, fs)
	}
	var got []string
	for _, p := range pls {
		got = append(got, fmt.Sprintf("%s (%.1f, %.1f) %d", p.Noun, p.Pos[0], p.Pos[1], p.Team))
	}
	want := []string{"Water.Noun (-0.9, 0.9) 0", "Water.Noun (-60.0, -60.0) 1", "Water.Noun (60.0, 60.0) 2",
		"Water.Noun (-80.0, -40.0) 1", "Water.Noun (80.0, 40.0) 2", "Water.Noun (-40.0, -80.0) 1",
		"Water.Noun (40.0, 80.0) 2"}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("placed\n%s\nwant\n%s", strings.Join(got, "; "), strings.Join(want, "; "))
	}
	fords := cm.Fords()
	if len(fords) != 3 {
		t.Fatalf("fords %+v", fords)
	}
	for i, w := range [][3]float64{{-0.9, 0.9, -0.55}, {-40, -80, -0.63}, {40, 80, -0.63}} {
		f := fords[i]
		if math.Abs(f.X-w[0]) > 1e-3 || math.Abs(f.Y-w[1]) > 1e-3 || f.Depth != w[2] {
			t.Errorf("ford %d %+v, want %v", i, f, w)
		}
	}
	if f := fords[0]; f.HalfW != 15 || f.HalfL != 30 || math.Abs(f.Cos-math.Sqrt(0.5)) > 1e-9 {
		t.Errorf("the ford's rectangle %+v", f)
	}
	// the floor (d < 0: walkable, 5 m from its edge) under the ford, past its rectangle, by the deep plane
	spec := cm.GroundSpec()
	flat := spec
	flat.Dips = nil
	dip := func(x, y float64) float64 { return ground.Height(-5, x, y, &spec) - ground.Height(-5, x, y, &flat) }
	if d := dip(-0.9, 0.9); math.Abs(d+0.55) > 1e-9 {
		t.Errorf("the floor under the ford dips %g", d)
	}
	if d := dip(40, 80); math.Abs(d+0.63) > 1e-9 {
		t.Errorf("the floor under the mirrored ford dips %g", d)
	}
	for _, p := range []Point{{-0.9 + 25, 0.9 + 25}, {-60, -60}, {60, 60}, {-80, -40}, {100, -100}} {
		if d := dip(p[0], p[1]); d != 0 {
			t.Errorf("the floor at %v dips %g", p, d)
		}
	}
	if d := ground.Height(3, -0.9, 0.9, &spec) - ground.Height(3, -0.9, 0.9, &flat); d != 0 {
		t.Errorf("a wall's foot dips %g", d)
	}
	// the props markerset holds the seven planes; the ground model differs from the plain map's by the dips only
	as, err := cm.Assets(ad, madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, mk := range markers(as.Values[m.Name+"_props.Markerset"]) {
		if text(mk, "nounDef") == "Water.Noun" {
			n++
		}
	}
	if n != 7 {
		t.Errorf("%d water markers in the props markerset", n)
	}
}
