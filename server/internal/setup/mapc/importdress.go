package mapc

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The import's dressing (mapc import --dress): a level's markersets that hold dressing nouns (PropTypes) as props
// groups, except the level's environment (its terrain noun is the level's own ground; the compiled map has its own).
// Each becomes one group that stands once ("mirror": false) with the polygon region of the whole level and up to
// dressMargin past its edges (a level's backdrop, such as SM_Map_v5's waterfall behind its base, stands there), its
// anchor at and pos the centroid of the members it takes (so the group stands where it was, unturned, unscaled): both
// halves wear the level's own dressing, marker for marker. A mirrored group of team 1's half would put team 1's
// dressing turned on team 2's half, and a level's hand-placed dressing is only roughly point-symmetric (the report
// says how far the level's own team 2 dressing lies from team 1's mirror). A set that is mostly gameplay markers is
// taken too (a tower set's Binding platforms and range rings): the members that aren't dressing are left out by the
// compiler (props.group warning) and listed in the report. The map's gameplay objects stay mirrored as ever.

// ImportDress is the dressing part of the import's report.
type ImportDress struct {
	// Region is the groups' polygon (the whole level, past its edges).
	Region []Point `json:"region"`
	// Taken are the dressing sets written as groups, Skipped the other sets that hold dressing markers.
	Taken   []DressSet `json:"taken"`
	Skipped []DressSet `json:"skipped"`
	// Members counts the taken sets' members in the region (the markers the groups place, once each).
	Members int `json:"members"`
}

// DressSet is one markerset of the level as the dressing import saw it.
type DressSet struct {
	Set      string `json:"set"`      // the suffix (<level>_<set>)
	Markers  int    `json:"markers"`  // all its markers
	Dressing int    `json:"dressing"` // of them dressing nouns
	// Taken: the dressing markers in the region (a taken set's group members, both halves); Outside: the dressing
	// markers outside it (not taken); LeftOut: the region's markers that aren't dressing ("noun xN"), which the group
	// leaves out.
	Taken   int      `json:"taken"`
	Outside int      `json:"outside,omitempty"`
	LeftOut []string `json:"leftOut,omitempty"`
	// Team2: the taken markers on team 2's side (x + y > importCut); Unmatched: those without a team 1 marker of the
	// same noun mirrored within dressMatch m (where a mirrored team 1 half would differ from the level).
	Team2     int `json:"team2"`
	Unmatched int `json:"unmatched"`
	// Reason says why a set wasn't taken (the environment, or no dressing in the region).
	Reason string `json:"reason,omitempty"`
}

// dressMatch is how far (m) a team 2 dressing marker may lie from team 1's mirrored one to count as its twin.
const dressMatch = 1.0

// dressMargin is how far past the level's edges (metres) the dressing region reaches: SM_Map_v5's backdrop (its
// cliffs, canopies and the waterfall behind its base) stands up to 74 m past them.
const dressMargin = 100.0

// dressRegion is the whole of a level of size s (metres) and dressMargin past its edges: a square, rounded to 0.1 m.
func dressRegion(s float64) []Point {
	h := r1(s/2 + dressMargin)
	return []Point{{-h, -h}, {h, -h}, {h, h}, {-h, h}}
}

// dress writes the level's dressing sets as groups (m.Props) and reports them (rep.Dress).
func (im *importer) dress(src *assetSource, level string, sets []string, size float64) error {
	ps := newPropSource(src)
	d := &ImportDress{Region: dressRegion(size)}
	region := &Group{Polygon: d.Region}
	lim := im.m.Size/2 - edgeMargin
	for _, s := range sets {
		suffix := strings.TrimPrefix(s, level+"_")
		if suffix == s {
			continue // not the level's own set
		}
		obj, err := src.object(s + ".Markerset")
		if err != nil {
			return err
		}
		ds := DressSet{Set: suffix}
		left := map[string]int{}
		type mk struct {
			noun string
			p    Point
		}
		var t1, t2 []mk
		var sum Point
		for _, m := range markers(obj) {
			ds.Markers++
			info := ps.noun(text(m, "nounDef"))
			dressing := info != nil && isPropType(info.typ)
			noun := strings.ToLower(text(m, "nounDef"))
			if info != nil {
				noun = info.name
			}
			if dressing {
				ds.Dressing++
			}
			v := vec(m, "pos")
			if len(v) < 2 {
				continue
			}
			p := Point{v[0], v[1]}
			switch {
			case !region.takes(p):
				if dressing {
					ds.Outside++
				}
			case dressing:
				ds.Taken++
				if p[0]+p[1] > importCut {
					t2 = append(t2, mk{noun, p})
				} else {
					t1 = append(t1, mk{noun, p})
				}
				sum[0] += p[0]
				sum[1] += p[1]
			default:
				left[strings.TrimSuffix(noun, ".Noun")]++
			}
		}
		if ds.Dressing == 0 {
			continue
		}
		switch {
		case strings.EqualFold(suffix, setEnvironment):
			ds.Reason = "the level's environment (its own ground); the map has its own"
		case ds.Taken == 0:
			ds.Reason = "no dressing in the level"
		}
		if ds.Reason != "" {
			ds.Taken, ds.Outside, left = 0, 0, nil
			d.Skipped = append(d.Skipped, ds)
			continue
		}
		for n, c := range left {
			ds.LeftOut = append(ds.LeftOut, fmt.Sprintf("%s x%d", n, c))
		}
		sort.Strings(ds.LeftOut)
		ds.Team2 = len(t2)
		for _, q := range t2 {
			ok := false
			for _, p := range t1 {
				if p.noun == q.noun && dist(Mirror(p.p), q.p) <= dressMatch {
					ok = true
					break
				}
			}
			if !ok {
				ds.Unmatched++
			}
		}
		// the anchor: the members' centroid, in the map's bounds and off the centre (where a mirrored group would
		// stand once; kept so for an author who turns mirroring on)
		at := r1p(Point{sum[0] / float64(ds.Taken), sum[1] / float64(ds.Taken)})
		at = Point{math.Max(-lim, math.Min(lim, at[0])), math.Max(-lim, math.Min(lim, at[1]))}
		if math.Hypot(at[0], at[1]) <= 2*CentreTolerance {
			at = Point{-1, -1}
		}
		once := false
		im.m.Props = append(im.m.Props, Prop{Group: &Group{Level: level, Sets: []string{suffix}, At: at,
			Polygon: d.Region}, Pos: [3]float64{at[0], at[1], 0}, Scale: 1, Mirror: &once})
		d.Members += ds.Taken
		d.Taken = append(d.Taken, ds)
	}
	im.rep.Dress = d
	if len(d.Taken) > 0 {
		im.note("the level's dressing: %d sets as groups placed once as the level has them (%d markers, both "+
			"halves; a mirror of team 1's half would differ from the level's team 2 dressing in %d markers)",
			len(d.Taken), d.Members, d.unmatched())
	}
	return nil
}

func (d *ImportDress) unmatched() int {
	n := 0
	for _, s := range d.Taken {
		n += s.Unmatched
	}
	return n
}
