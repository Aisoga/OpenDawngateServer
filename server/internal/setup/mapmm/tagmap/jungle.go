package tagmap

// A compiled map's tags outside the bases (Spec.Lanes, Spec.Discs), measured on the first map's own tag map: its Lane
// band lies about 25 units round its lanes' splines (a band of 24 to 26 splits its Lane and Jungle pixels best), its
// Top and Bottom are exactly its two lanes' bands, Jungle is the rest outside the bases, and its camp tags are discs
// on the Jungle round the camps (GreenBuff the Fish buff camps, BlueBuff the Fungus ones, OrangeBuff the Ugger ones,
// MoneyPigs the Boar camps, Parasite the parasite's), none on a lane or a base.

// laneTags are the tags of a point outside the bases on a map with lane bands: Lane and the lanes whose band holds
// it, else Jungle and the discs that hold it.
func laneTags(x, y float64, s Spec) []string {
	top, bottom := false, false
	for _, l := range s.Lanes {
		if inBand(x, y, l) {
			if l.ID == 1 {
				top = true
			} else {
				bottom = true
			}
		}
	}
	if top || bottom {
		if !s.TwoLanes { // one lane: both of the first map's lanes, as MM_Map_v3's
			top, bottom = true, true
		}
		out := []string{"Lane"}
		if top {
			out = append(out, "Top")
		}
		if bottom {
			out = append(out, "Bottom")
		}
		return out
	}
	out := []string{"Jungle"}
	for _, d := range s.Discs {
		if !has(out, d.Tag) && inDisc(x, y, []Point{d.Centre}, d.R) {
			out = append(out, d.Tag)
		}
	}
	return out
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// inBand is whether (x, y) lies within l.Half of the lane's polyline (each product rounded on its own, so every
// platform paints the same pixels).
func inBand(x, y float64, l LaneBand) bool {
	h2 := float64(l.Half * l.Half)
	for i := 0; i+1 < len(l.Knots); i++ {
		a, b := l.Knots[i], l.Knots[i+1]
		dx, dy := b.X-a.X, b.Y-a.Y
		ll := float64(dx*dx) + float64(dy*dy)
		t := 0.0
		if ll > 0 {
			t = (float64((x-a.X)*dx) + float64((y-a.Y)*dy)) / ll
			t = max(0, min(1, t))
		}
		ex := x - a.X - float64(t*dx)
		ey := y - a.Y - float64(t*dy)
		if float64(ex*ex)+float64(ey*ey) <= h2 {
			return true
		}
	}
	return false
}
