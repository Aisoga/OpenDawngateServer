package mapc

import (
	"math"
	"sort"
	"strconv"
)

// SM_Map_v5's numbers, which "auto" places by (measured on its markers: the Mortal Guardian's titan node at
// (-182, -182), its nubs, its Bindings against its two blue lanes' knots).
var (
	// smCoreOffsets are the Cores nub_mortal_v5_A to _E from the Guardian.
	smCoreOffsets = [5]Point{{32.4622, 32.4622}, {-16.9454, 43.0663}, {43.0663, -16.9454}, {-47.0581, 4.0271},
		{4.0271, -47.0581}}
	// smBindingFractions are the Outer, Middle and Inner Bindings' places along their lane from the own base (the
	// mean of the two lanes: Outer 0.4464 and 0.4478, Middle 0.3120 and 0.3139, Inner 0.1743 and 0.1760).
	smBindingFractions = [3]float64{0.447, 0.313, 0.175}
	// smBindingLateral are their distances to the left of the lane (metres, the direction of travel), lane 1's; lane
	// 2's are the opposite (its lane is the first's mirror across the diagonal).
	smBindingLateral = [3]float64{9.3, -7.1, 6.4}
)

const (
	// SpawnerBack is how far a lane's minion spawner stands behind its first knot (SM_Map_v5's: 20.6 and 21.0 m
	// along the first leg).
	SpawnerBack = 21.0
	// HoleRadius is the Guardian's platform cut out of both graphs; IslandRadius its own NPC island (MAPNAV2b: the
	// shipped graphs' 10.0-10.9 m and 3.8 m).
	HoleRadius   = 10.0
	IslandRadius = 3.7
)

// Tier names, in the order of a lane's three Bindings.
var Tiers = [3]string{"Outer", "Middle", "Inner"}

// TierNames are the tiers of a lane's n Bindings from the own base's far side: SM_Map_v5's three (Outer, Middle,
// Inner) or MM_Map_v3's two (Middle, Inner: MM's towers markerset has no Outer Binding; MAPED5).
func TierNames(n int) []string {
	if n == 2 {
		return []string{"Middle", "Inner"}
	}
	return Tiers[:]
}

// CoreCounts are the Cores a base may have: SM_Map_v5's five (A to E) or MM_Map_v3's three (A to C; MAPED5).
var CoreCounts = [2]int{5, 3}

// Team is one team's resolved objects.
type Team struct {
	Locus, Guardian, Shop Point
	Spawns                []Point
	Cores                 []Point
	Bindings              map[int][]Point // lane id: Outer, Middle, Inner (or Middle, Inner: TierNames)
	Spawners              map[int]Point   // lane id: the minion spawner
	Lanes                 map[int][]Point // lane id: the knots in this team's direction
	Wells                 []Point
	Camps                 []Camp
}

// Layout is the whole map: both teams' objects. Teams[0] is the authored team 1.
type Layout struct {
	Map     *Map
	LaneIDs []int // sorted
	Teams   [2]Team
}

// Mirror is the point mirror through the centre.
func Mirror(p Point) Point { return Point{-p[0], -p[1]} }

func mirrorAll(ps []Point) []Point {
	if ps == nil {
		return nil
	}
	out := make([]Point, len(ps))
	for i, p := range ps {
		out[i] = Mirror(p)
	}
	return out
}

func reversed(ps []Point) []Point {
	out := make([]Point, len(ps))
	for i, p := range ps {
		out[len(ps)-1-i] = p
	}
	return out
}

// polyline helpers

func dist(a, b Point) float64 { return math.Hypot(a[0]-b[0], a[1]-b[1]) }

// along is the point at arc length s of the polyline, its unit direction there.
func along(ps []Point, s float64) (Point, Point) {
	for i := 0; i+1 < len(ps); i++ {
		l := dist(ps[i], ps[i+1])
		if l == 0 {
			continue
		}
		if s <= l || i+2 == len(ps) {
			t := s / l
			d := Point{(ps[i+1][0] - ps[i][0]) / l, (ps[i+1][1] - ps[i][1]) / l}
			return Point{ps[i][0] + t*(ps[i+1][0]-ps[i][0]), ps[i][1] + t*(ps[i+1][1]-ps[i][1])}, d
		}
		s -= l
	}
	return ps[0], Point{1, 0}
}

func length(ps []Point) float64 {
	var l float64
	for i := 0; i+1 < len(ps); i++ {
		l += dist(ps[i], ps[i+1])
	}
	return l
}

// project is the arc length of the polyline's point nearest p and the distance to it.
func project(ps []Point, p Point) (s, d float64) {
	d = math.Inf(1)
	var acc float64
	for i := 0; i+1 < len(ps); i++ {
		a, b := ps[i], ps[i+1]
		dx, dy := b[0]-a[0], b[1]-a[1]
		ll := dx*dx + dy*dy
		t := 0.0
		if ll > 0 {
			t = math.Max(0, math.Min(1, ((p[0]-a[0])*dx+(p[1]-a[1])*dy)/ll))
		}
		q := Point{a[0] + t*dx, a[1] + t*dy}
		if e := dist(p, q); e < d {
			d, s = e, acc+t*math.Sqrt(ll)
		}
		acc += math.Sqrt(ll)
	}
	return s, d
}

// segDist is the distance from p to the segment ab.
func segDist(p, a, b Point) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	ll := dx*dx + dy*dy
	t := 0.0
	if ll > 0 {
		t = math.Max(0, math.Min(1, ((p[0]-a[0])*dx+(p[1]-a[1])*dy)/ll))
	}
	return math.Hypot(p[0]-a[0]-t*dx, p[1]-a[1]-t*dy)
}

// otherLane is the lane id a lane's objects take in the mirror: with two lanes the point mirror swaps them (team 1's
// Top lane mirrored is team 2's view of the Bottom corridor), with one lane it is the same.
func (l *Layout) otherLane(id int) int {
	if len(l.LaneIDs) == 2 {
		if id == l.LaneIDs[0] {
			return l.LaneIDs[1]
		}
		return l.LaneIDs[0]
	}
	return id
}

// AutoBindings are a lane's three Bindings at SM_Map_v5's fractions and offsets.
func AutoBindings(id int, knots []Point) []Point {
	total := length(knots)
	out := make([]Point, 3)
	for k := range out {
		p, d := along(knots, smBindingFractions[k]*total)
		lat := smBindingLateral[k]
		if id != 1 {
			lat = -lat
		}
		out[k] = Point{p[0] - d[1]*lat, p[1] + d[0]*lat}
	}
	return out
}

// Spawner is a lane's minion spawner: SpawnerBack metres behind its first knot.
func Spawner(knots []Point) Point {
	if len(knots) < 2 {
		return knots[0]
	}
	l := dist(knots[0], knots[1])
	if l == 0 {
		return knots[0]
	}
	return Point{knots[0][0] - (knots[1][0]-knots[0][0])/l*SpawnerBack,
		knots[0][1] - (knots[1][1]-knots[0][1])/l*SpawnerBack}
}

// Resolve derives both teams' objects. It expects a map that passed the structural checks (Check's first stage):
// every lane with at least two knots, "auto" or complete Cores and Bindings.
func Resolve(m *Map) *Layout {
	l := &Layout{Map: m}
	for _, ln := range m.Lanes {
		l.LaneIDs = append(l.LaneIDs, ln.ID)
	}
	sort.Ints(l.LaneIDs)
	b := m.Base
	t1 := Team{Locus: b.Locus, Guardian: b.Guardian, Shop: b.Shop, Spawns: append([]Point(nil), b.Spawns...),
		Bindings: map[int][]Point{}, Spawners: map[int]Point{}, Lanes: map[int][]Point{}}
	if b.Cores.Auto {
		for _, o := range smCoreOffsets {
			t1.Cores = append(t1.Cores, Point{b.Guardian[0] + o[0], b.Guardian[1] + o[1]})
		}
	} else {
		t1.Cores = append(t1.Cores, b.Cores.Points...)
	}
	for _, ln := range m.Lanes {
		t1.Lanes[ln.ID] = append([]Point(nil), ln.Knots...)
		t1.Spawners[ln.ID] = Spawner(ln.Knots)
		if b.Bindings.Auto {
			t1.Bindings[ln.ID] = AutoBindings(ln.ID, ln.Knots)
		} else {
			t1.Bindings[ln.ID] = append([]Point(nil), b.Bindings.Lanes[strconv.Itoa(ln.ID)]...)
		}
	}
	for _, w := range m.Wells {
		t1.Wells = append(t1.Wells, w.Pos)
	}
	t1.Camps = append(t1.Camps, m.Camps...)

	t2 := Team{Locus: Mirror(t1.Locus), Guardian: Mirror(t1.Guardian), Shop: Mirror(t1.Shop),
		Spawns: mirrorAll(t1.Spawns), Cores: mirrorAll(t1.Cores), Wells: mirrorAll(t1.Wells),
		Bindings: map[int][]Point{}, Spawners: map[int]Point{}, Lanes: map[int][]Point{}}
	for id, ks := range t1.Lanes {
		t2.Lanes[id] = reversed(ks)
		t2.Spawners[id] = Spawner(t2.Lanes[id])
		t2.Bindings[l.otherLane(id)] = mirrorAll(t1.Bindings[id])
	}
	for _, c := range t1.Camps {
		if c.Kind == CampParasite { // the map's one parasite, not mirrored (MAPED3)
			continue
		}
		t2.Camps = append(t2.Camps, Camp{Kind: c.Kind, Noun: c.Noun, Pos: Mirror(c.Pos)})
	}
	l.Teams = [2]Team{t1, t2}
	return l
}

// SpawnCentre is a team's spawn points' mean (its base end: the minimap's and the ground's base colour).
func (t *Team) SpawnCentre() Point {
	var c Point
	for _, p := range t.Spawns {
		c[0] += p[0] / float64(len(t.Spawns))
		c[1] += p[1] / float64(len(t.Spawns))
	}
	return c
}
