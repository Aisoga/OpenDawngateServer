package mapc

import (
	"slices"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapmm/tagmap"
)

// TestWardsMadeUp: CM_Ring with two ward spots: a marker per spot and team in the design markerset, named
// BotWard_<side>_<nn>, at the spot (team 2's at the mirror), the template's navigation marker that allows players.
func TestWardsMadeUp(t *testing.T) {
	m := ring(t)
	m.Wards = []Ward{{Point{-172, 5}}, {Point{-30, 174}}}
	r, cm := Check(m, nil)
	if !r.OK || len(r.Findings) != 0 {
		t.Fatalf("%+v", r.Findings)
	}
	as, err := cm.Assets(madeUpAssetData(t), madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	if n := as.MarkerCount()[m.Name+"_design.Markerset"]; n != 18+4 {
		t.Errorf("design: %d markers, 22 expected", n)
	}
	found := map[string][]float64{}
	for _, mk := range markers(as.Values[m.Name+"_design.Markerset"]) {
		if n := text(mk, "markerName"); strings.HasPrefix(n, WardMarkerPrefix) {
			found[n] = vec(mk, "pos")
			if pl, _ := sub(mk, "navmeshMarkerData").Get("allowPlayersHere"); pl != true ||
				!strings.EqualFold(text(mk, "nounDef"), "navmeshmarker_valid.Noun") {
				t.Errorf("%s: noun %s, players %v", n, text(mk, "nounDef"), pl)
			}
		}
	}
	for name, want := range map[string][2]float64{"BotWard_Mortal_00": {-172, 5}, "BotWard_Mortal_01": {-30, 174},
		"BotWard_Spirit_00": {172, -5}, "BotWard_Spirit_01": {30, -174}} {
		p, ok := found[name]
		if !ok || p[0] != want[0] || p[1] != want[1] {
			t.Errorf("%s at %v, %v expected (%v)", name, p, want, found)
		}
	}
	if len(found) != 4 {
		t.Errorf("%d ward markers", len(found))
	}
	// the preview lists them; FormatMap writes them and reads them back
	n := 0
	for _, o := range cm.Objects() {
		if o.Kind == "ward" {
			n++
		}
	}
	if n != 4 {
		t.Errorf("%d ward objects in the preview", n)
	}
	text, err := FormatMap(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(text)
	if err != nil || !slices.Equal(back.Wards, m.Wards) {
		t.Errorf("FormatMap's wards read back as %v (%v)", back.Wards, err)
	}
	// none: the design markerset as before
	m.Wards = nil
	_, cm = Check(m, nil)
	as, err = cm.Assets(madeUpAssetData(t), madeUpBounds)
	if err != nil {
		t.Fatal(err)
	}
	if n := as.MarkerCount()[m.Name+"_design.Markerset"]; n != 18 {
		t.Errorf("design without wards: %d markers", n)
	}
}

// TestWardRules: a coordinate of 0, too many spots, a spot in a wall or out of the level, one near the centre.
func TestWardRules(t *testing.T) {
	for _, c := range []struct {
		name  string
		wards []Point
		rule  string
		sev   string
	}{
		{"axis", []Point{{-174, 0}}, "wards.axis", Error},
		{"wall", []Point{{-100, -100}}, "wards.reach", Error},
		{"outside", []Point{{-260, 10}}, "size.bounds", Error},
		{"centre", []Point{{-30, -20}}, "wards.centre", Warning},
		{"budget", make([]Point, MaxWards+1), "wards.budget", Error},
	} {
		m := ring(t)
		for _, p := range c.wards {
			m.Wards = append(m.Wards, Ward{p})
		}
		r, _ := Check(m, nil)
		if !has(r, c.rule, c.sev) {
			t.Errorf("%s: no %s %s in %+v", c.name, c.sev, c.rule, r.Findings)
		}
	}
}

// TestGroveTags: CM_Grove's tag map: Lane with Top or Bottom only in the lanes' bands, Jungle off them, the buff
// camps' colours (the Fungus blue, the Fish green) and the parasite's disc on the jungle, the bases as before.
func TestGroveTags(t *testing.T) {
	r, cm := Check(grove(t), nil)
	if !r.OK {
		t.Fatalf("%+v", r.Findings)
	}
	names, err := tagmap.TagNames(madeUpTags())
	if err != nil {
		t.Fatal(err)
	}
	img, err := cm.TagImage(madeUpTags())
	if err != nil {
		t.Fatal(err)
	}
	s := cm.TagSpec()
	for _, c := range []struct {
		x, y          float64
		want, without string
	}{
		{-174, 0, "Lane,Top,MortalSide", "Bottom,Jungle"},
		{0, -174, "Lane,Bottom", "Top,Jungle"},
		{174, 0, "Lane,Bottom,SpiritSide", "Top,Jungle"},
		{-100, -15, "Jungle,BlueBuff", "Lane,Top,GreenBuff"},
		{-15, -100, "Jungle,GreenBuff", "Lane,Bottom,BlueBuff"},
		{100, 15, "Jungle,BlueBuff,SpiritSide", "Lane"},
		{-105, 60, "Jungle", "BlueBuff,GreenBuff,OrangeBuff,MoneyPigs,Parasite,Lane"},
		{0, 0, "Jungle,Parasite", "Lane,Top,Bottom"},
		{-65, -65, "Jungle", "Lane,Base"},
		{-196, -196, "Base,Locus", "Lane,Jungle"},
	} {
		got, err := tagmap.At(names, img, s, c.x, c.y)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range strings.Split(c.want, ",") {
			if !slices.Contains(got, n) {
				t.Errorf("(%g, %g): %v lacks %s", c.x, c.y, got, n)
			}
		}
		for _, n := range strings.Split(c.without, ",") {
			if slices.Contains(got, n) {
				t.Errorf("(%g, %g): %v has %s", c.x, c.y, got, n)
			}
		}
	}
	for _, c := range []struct {
		camp Camp
		tag  string
	}{
		{Camp{"buff", "SpawnPoint_v5_Buff_Ugger.Noun", Point{}}, "OrangeBuff"},
		{Camp{"camp", "SpawnPoint_v5_CreepsBoar.Noun", Point{}}, "MoneyPigs"},
		{Camp{"camp", "SpawnPoint_v5_CreepsFish.Noun", Point{}}, ""},
		{Camp{"parasite", "SpawnPoint_Objective_Titan.Noun", Point{}}, "Parasite"},
	} {
		if tag, _ := CampTag(c.camp); tag != c.tag {
			t.Errorf("%s: %q, %q expected", c.camp.Noun, tag, c.tag)
		}
	}
}
