package tagmap

import (
	"strings"
	"testing"
)

// madeUpJungle is madeUp with two lanes' bands and camp discs (a compiled map's spec).
func madeUpJungle() (Spec, []string) {
	s, names, _ := madeUp()
	s.TwoLanes = true
	s.Lanes = []LaneBand{
		{ID: 1, Half: 16, Knots: []Point{{-150, -120}, {-160, 100}, {120, 160}}},
		{ID: 2, Half: 16, Knots: []Point{{-120, -150}, {100, -160}, {160, 120}}},
	}
	s.Discs = []Disc{{"GreenBuff", Point{-40.5, 60.25}, 20}, {"BlueBuff", Point{60.25, -40.5}, 20},
		{"MoneyPigs", Point{-80, 20}, 24}, {"Parasite", Point{0, 0}, 38}, {"Parasite", Point{10, 10}, 38}}
	return s, names
}

// madeUpJungleSHA256 is madeUpJungle's image (Go only: the compiled maps have no Python twin).
const madeUpJungleSHA256 = "bcbc9367923bf477e69ff9db80ca7dffdfe0bc03aa76b072a5e65aac03bee179"

func TestJungleMadeUp(t *testing.T) {
	s, names := madeUpJungle()
	img, err := Image(names, s)
	if err != nil {
		t.Fatal(err)
	}
	if d := digest(img); d != madeUpJungleSHA256 {
		t.Errorf("SHA-256 %s, pinned %s", d, madeUpJungleSHA256)
	}
	for _, c := range []struct {
		x, y          float64
		want, without string
	}{
		{-155, 0, "Lane,MortalSide,Top", "Bottom,Jungle,Base"},      // on lane 1's second leg
		{0, -155, "Lane,MortalSide,Bottom", "Top,Jungle"},           // lane 2
		{0, 0, "Jungle,Parasite,MortalSide", "Lane,Top,Bottom"},     // off the bands: the parasite's disc
		{-40, 60, "Jungle,GreenBuff,SpiritSide", "BlueBuff,Lane"},   // the green buff
		{60, -40, "Jungle,BlueBuff", "GreenBuff,Lane"},              // the blue buff
		{-80, 20, "Jungle,MoneyPigs", "Parasite"},                   // the pigs
		{-120, 30, "Jungle", "MoneyPigs,Lane,GreenBuff,Parasite"},   // jungle beside the band, no camp
		{-150, -120, "Base", "Lane,Jungle,Top"},                     // the bases stay as they were
		{-160, 100, "Lane,Top", "Jungle"},                           // a knot
		{-136, 30, "Jungle", "Lane"},                                // just beyond the band (20.8 from lane 1)
		{230, 220.75, "Base,SpiritWell,SpiritWellCenter", "Jungle"}, // the wells' rules unchanged
	} {
		got, err := At(names, img, s, c.x, c.y)
		if err != nil {
			t.Fatal(err)
		}
		set := map[string]bool{}
		for _, n := range got {
			set[n] = true
		}
		for _, n := range strings.Split(c.want, ",") {
			if !set[n] {
				t.Errorf("(%.1f, %.1f): %v lacks %s", c.x, c.y, got, n)
			}
		}
		for _, n := range strings.Split(c.without, ",") {
			if set[n] {
				t.Errorf("(%.1f, %.1f): %v has %s", c.x, c.y, got, n)
			}
		}
	}
	// one lane: its band is both lanes
	s.TwoLanes = false
	s.Lanes = s.Lanes[:1]
	img, err = Image(names, s)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := At(names, img, s, -155, 0); strings.Join(got, ",") != "Top,Bottom,Lane,MortalSide" {
		t.Errorf("one lane: %v", got)
	}
	// without lanes the image is MM_Map_v3's rule's, byte for byte (the made-up pin)
	s, names, _ = madeUp()
	s.Discs = []Disc{{"GreenBuff", Point{0, 0}, 50}}
	img, err = Image(names, s)
	if err != nil {
		t.Fatal(err)
	}
	if d := digest(img); d != madeUpImageSHA256 {
		t.Errorf("discs without lanes changed the image: %s", d)
	}
}
