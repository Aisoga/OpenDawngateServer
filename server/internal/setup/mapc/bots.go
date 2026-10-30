package mapc

import (
	"math"
	"strings"
)

// The bots' derived points: where the match host's bots will go on the compiled map, computed from the compiled map
// by the host's own rules (the match host's gms/core/bots/rally.h ParasiteRallyPoint and route.h DeriveRoute, fed as
// gms/engine/bots.cpp and hunterroute.cpp feed them from the built Level), so that a map's author sees where a layout
// edit puts them before playing it. preview.json carries them as "bots".
//
//   - The parasite's rally point of each team: 62 units from the parasite camp's marker toward the team's Guardian; a
//     point on a wall of the line-of-sight map, with a wall within 5 units round it, or with a wall on the line to the
//     parasite turns in 10-degree steps (+, -, ++, ...; up to 90) and comes nearer in 3-unit steps down to 47; then the
//     same without the line test; else the plain first point, unchecked. A map without a parasite camp keeps the
//     first map's fixed points (no rally here).
//   - Each team's Hunter route: the camps nearer the team's Guardian than the enemy's; from each buff camp (any camp
//     when there is none) a walk to the nearest camp not taken yet, every time; the shortest walk wins; the first two
//     buff camps and every small camp from level 1, a later buff camp and the big camps from level 3; at most 32 steps.
//
// The arithmetic is the host's single precision, so the points agree with the host's log lines (to their 0.1).

// BotRally is one team's rally point for the parasite.
type BotRally struct {
	Team     int     `json:"team"`
	Pos      Point   `json:"pos"`
	From     Point   `json:"from"`     // the parasite camp's marker
	Toward   Point   `json:"toward"`   // the team's Guardian
	Straight Point   `json:"straight"` // the first point tried: 62 units straight toward the Guardian
	Dist     float64 `json:"dist"`     // from the parasite camp
	Turn     float64 `json:"turn"`     // degrees off the straight line (counter-clockwise positive)
	Quality  string  `json:"quality"`  // open-line, open (a wall between it and the parasite), unchecked
}

// BotRouteStep is one step of a Hunter route.
type BotRouteStep struct {
	Pos      Point  `json:"pos"`
	Role     string `json:"role"`     // buff, big, small
	MinLevel int    `json:"minLevel"` // taken from this level on
	Label    string `json:"label"`    // the host's log label: "<role> <noun>"
	Path     string `json:"path"`     // the camp in map.json (team 2's: the mirror of team 1's)
}

// BotRoute is one team's Hunter route.
type BotRoute struct {
	Team  int            `json:"team"`
	Steps []BotRouteStep `json:"steps"`
}

// BotPoints are the bots' derived points of both teams.
type BotPoints struct {
	Rally  []BotRally `json:"rally"` // empty without a parasite camp
	Routes []BotRoute `json:"routes"`
}

// The rally's constants (rally.h RallyConfig).
const (
	rallyDist      float32 = 62
	rallyMinDist   float32 = 47
	rallyStepDist  float32 = 3
	rallyTurnDeg   float32 = 10
	rallyTurns             = 9
	rallyClearance float32 = 5
	rallyLineStep  float32 = 2
	rallyLineSkip  float32 = 10
)

// The rally's qualities (rally.h RallyQuality).
const (
	RallyUnchecked = "unchecked"
	RallyOpen      = "open"
	RallyOpenLine  = "open-line"
)

// CampRole is a camp noun's role for the host: buff (a creature marker larger than the camp's others), big (no such
// marker, a respawn of 240 s or more) or small. The host reads it from the first map's spawner marker the camp is
// copied from; on that map's camp nouns that is the name rule: the Buff_ camps are buff, the Boar (Gold Pigs) camp is
// big, the rest small (6 / 2 / 14 on the first map, the host's MAPED4 split).
func CampRole(noun string) string {
	n := strings.ToLower(noun)
	switch {
	case strings.Contains(n, "buff"):
		return "buff"
	case strings.Contains(n, "boar"):
		return "big"
	}
	return "small"
}

// losOpen is the host's open-ground test on the line-of-sight map (bots.cpp OpenGround): inside the level and on a
// cell that doesn't block sight; the cell as LosMap::CellOf finds it (round to nearest even of origin + x * scale).
func (cm *Compiled) losOpen() func(x, y float32) bool {
	pix := cm.Layers.LoS().Pix
	size := float32(cm.Map.Size)
	scale := float32(Grid) / size
	origin := float32(Grid) * 0.5
	half := float32(Grid) / scale / 2
	cell := func(v float32) int {
		i := int(math.RoundToEven(float64(origin + v*scale)))
		return min(max(i, 0), Grid-1)
	}
	return func(x, y float32) bool {
		if x < -half || x > half || y < -half || y > half {
			return false
		}
		return pix[cell(y)*Grid+cell(x)]&0x80 == 0
	}
}

func cos32(a float32) float32 { return float32(math.Cos(float64(a))) }
func sin32(a float32) float32 { return float32(math.Sin(float64(a))) }

// rallyPoint is ParasiteRallyPoint: the point, its quality and the turn (in steps) it was found at.
func rallyPoint(px, py, gx, gy float32, open func(x, y float32) bool) (x, y float32, quality string, turn int) {
	dx, dy := gx-px, gy-py
	l := float32(math.Sqrt(float64(dx*dx + dy*dy)))
	if l > 1e-3 {
		dx /= l
		dy /= l
	} else {
		dx, dy = -0.70710678, -0.70710678
	}
	x, y = px+dx*rallyDist, py+dy*rallyDist
	groundOK := func(x, y float32) bool {
		c := rallyClearance
		return open(x, y) && open(x+c, y) && open(x-c, y) && open(x, y+c) && open(x, y-c)
	}
	lineOK := func(x, y, d float32) bool {
		for s := rallyLineStep; s < d-rallyLineSkip; s += rallyLineStep {
			t := s / d
			if !open(x+(px-x)*t, y+(py-y)*t) {
				return false
			}
		}
		return true
	}
	const rad float32 = 3.14159265 / 180
	for pass := range 2 {
		for k := 0; k <= 2*rallyTurns; k++ {
			tn := (k + 1) / 2
			if k%2 == 0 {
				tn = -tn
			}
			a := float32(tn) * rallyTurnDeg * rad
			ux, uy := dx*cos32(a)-dy*sin32(a), dx*sin32(a)+dy*cos32(a)
			for d := rallyDist; d >= rallyMinDist-1e-3; d -= rallyStepDist {
				qx, qy := px+ux*d, py+uy*d
				if !groundOK(qx, qy) || (pass == 0 && !lineOK(qx, qy, d)) {
					continue
				}
				if pass == 0 {
					return qx, qy, RallyOpenLine, tn
				}
				return qx, qy, RallyOpen, tn
			}
		}
	}
	return x, y, RallyUnchecked, 0
}

// parasite is the parasite camp's place (the first one), or false.
func (cm *Compiled) parasite() (Point, bool) {
	for _, c := range cm.Layout.Teams[0].Camps {
		if c.Kind == CampParasite {
			return c.Pos, true
		}
	}
	return Point{}, false
}

// Rally is each team's rally point for the parasite (none without a parasite camp).
func (cm *Compiled) Rally() []BotRally {
	pp, ok := cm.parasite()
	if !ok {
		return nil
	}
	open := cm.losOpen()
	var out []BotRally
	for t := range 2 {
		g := cm.Layout.Teams[t].Guardian
		px, py := float32(pp[0]), float32(pp[1])
		x, y, q, turn := rallyPoint(px, py, float32(g[0]), float32(g[1]), open)
		sx, sy, _, _ := rallyPoint(px, py, float32(g[0]), float32(g[1]), func(float32, float32) bool { return true })
		out = append(out, BotRally{Team: t + 1, Pos: Point{float64(x), float64(y)}, From: pp, Toward: g,
			Straight: Point{float64(sx), float64(sy)},
			Dist:     float64(float32(math.Sqrt(float64((x-px)*(x-px) + (y-py)*(y-py))))),
			Turn:     float64(turn) * float64(rallyTurnDeg), Quality: q})
	}
	return out
}

// routeCamp is a camp as the route sees it: team 1's camps but the parasite, then team 2's (the order the host reads
// the Level's markers in).
type routeCamp struct {
	x, y       float32
	role, noun string
	path       string
}

func (cm *Compiled) routeCamps() []routeCamp {
	var src []int // map.json's index of each camp but the parasite's (team 2's camps are their mirrors, in order)
	for i, c := range cm.Layout.Teams[0].Camps {
		if c.Kind != CampParasite {
			src = append(src, i)
		}
	}
	var out []routeCamp
	for t := range 2 {
		j := 0
		for _, c := range cm.Layout.Teams[t].Camps {
			if c.Kind == CampParasite {
				continue
			}
			out = append(out, routeCamp{x: float32(c.Pos[0]), y: float32(c.Pos[1]), role: CampRole(c.Noun),
				noun: strings.TrimSuffix(c.Noun, ".Noun"), path: "camps[" + itoa(src[j]) + "].pos"})
			j++
		}
	}
	return out
}

// Routes are both teams' Hunter routes (route.h DeriveRoute).
func (cm *Compiled) Routes() []BotRoute {
	camps := cm.routeCamps()
	var out []BotRoute
	for t := range 2 {
		own, enemy := cm.Layout.Teams[t].Guardian, cm.Layout.Teams[1-t].Guardian
		r := BotRoute{Team: t + 1, Steps: append([]BotRouteStep{}, deriveRoute(camps, own, enemy)...)}
		out = append(out, r)
	}
	return out
}

func deriveRoute(camps []routeCamp, ownG, enemyG Point) []BotRouteStep {
	ox, oy, ex, ey := float32(ownG[0]), float32(ownG[1]), float32(enemyG[0]), float32(enemyG[1])
	var own []int
	for i, c := range camps {
		ax, ay, bx, by := c.x-ox, c.y-oy, c.x-ex, c.y-ey
		if ax*ax+ay*ay < bx*bx+by*by {
			own = append(own, i)
		}
	}
	if len(own) == 0 {
		return nil
	}
	dist := func(a, b int) float32 {
		dx, dy := camps[a].x-camps[b].x, camps[a].y-camps[b].y
		return float32(math.Sqrt(float64(dx*dx + dy*dy)))
	}
	walk := func(start int) ([]int, float32) {
		order := []int{start}
		taken := make([]bool, len(camps))
		taken[start] = true
		var length float32
		for len(order) < len(own) {
			best, bd := -1, float32(0)
			for _, k := range own {
				if taken[k] {
					continue
				}
				if d := dist(order[len(order)-1], k); best < 0 || d < bd {
					best, bd = k, d
				}
			}
			taken[best] = true
			order = append(order, best)
			length += bd
		}
		return order, length
	}
	anyBuff := false
	for _, k := range own {
		anyBuff = anyBuff || camps[k].role == "buff"
	}
	var bestOrder []int
	var bestLen float32
	for _, k := range own {
		if anyBuff && camps[k].role != "buff" {
			continue
		}
		if order, l := walk(k); bestOrder == nil || l < bestLen {
			bestOrder, bestLen = order, l
		}
	}
	var out []BotRouteStep
	buffs := 0
	for _, k := range bestOrder {
		if len(out) >= 32 {
			break
		}
		c := camps[k]
		if c.role == "buff" {
			buffs++
		}
		late := c.role == "big" || (c.role == "buff" && buffs > 2)
		lvl := 1
		if late && len(out) > 0 {
			lvl = 3
		}
		noun := c.noun
		if noun == "" {
			noun = "camp"
		}
		out = append(out, BotRouteStep{Pos: Point{float64(c.x), float64(c.y)}, Role: c.role, MinLevel: lvl,
			Label: c.role + " " + noun, Path: c.path})
	}
	return out
}

// Bots are the bots' derived points of the compiled map; nil when there are none (no parasite camp and no camps).
func (cm *Compiled) Bots() *BotPoints {
	b := &BotPoints{Rally: cm.Rally(), Routes: cm.Routes()}
	for _, r := range b.Routes {
		if len(r.Steps) > 0 {
			return b
		}
	}
	if len(b.Rally) > 0 {
		return b
	}
	return nil
}
