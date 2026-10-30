package mapc

import (
	"fmt"
	"math"

	"dawngate/server/internal/setup/assetdata"
)

// The bots' ward spots (MAPED4b). The game's ward condition (condition_Bot_Ward) carries eleven fixed spots a side,
// SM_Map_v5's; a map names its own in map.json's "wards" (team 1's, each mirrored for team 2). The compiler writes each
// spot as a marker of the design markerset named BotWard_<Mortal|Spirit>_<nn> (a navigation marker that allows
// players and NPCs, as the base's own: nothing in the game reads it); the match host reads them at the level's start
// and hands the ward condition the map's tables: Mortal the authored spots, Spirit their mirrors (the condition takes
// the side's table by where the bot stands).

// Ward is one ward spot.
type Ward struct {
	Pos Point `json:"pos"`
}

const (
	// MaxWards is the spots a side (the game's table holds 11).
	MaxWards = 16
	// WardCentreRadius is the match host's: a bot other than a Hunter wards no spot this near the map's centre (the
	// parasite's ground; dgsim_bots_api.lua's wardCenterRadius).
	WardCentreRadius = 60.0
	// WardMarkerPrefix starts a ward spot marker's name.
	WardMarkerPrefix = "BotWard_"
)

// WardMarkerName is team t's (0 team 1, Mortal) spot k's marker name.
func WardMarkerName(t, k int) string {
	return fmt.Sprintf("%s%s_%02d", WardMarkerPrefix, teamName[t], k)
}

// WardSpots are team t's ward spots: team 1's as authored, team 2's their mirrors.
func (cm *Compiled) WardSpots(t int) []Point {
	var out []Point
	for _, w := range cm.Map.Wards {
		if t == 0 {
			out = append(out, w.Pos)
		} else {
			out = append(out, Mirror(w.Pos))
		}
	}
	return out
}

// wardObjects are the ward spots as preview objects.
func (cm *Compiled) wardObjects() []PreviewObject {
	var out []PreviewObject
	for t := range 2 {
		for k, p := range cm.WardSpots(t) {
			path := fmt.Sprintf("wards[%d].pos", k)
			if t == 1 {
				path = ""
			}
			out = append(out, PreviewObject{Kind: "ward", Team: t + 1, Name: "ward " + itoa(k+1), Pos: p, Path: path})
		}
	}
	return out
}

// wards checks the ward spots: the count (wards.budget), a coordinate of 0 (wards.axis: the condition takes a spot
// with x or y 0 for none), the level's bounds (size.bounds); on the compiled map each spot on the players' ground
// joined to the bases (wards.reach) and, a warning, one near the centre (wards.centre).
func (c *checker) wards(cm *Compiled) {
	m := c.m
	if len(m.Wards) > MaxWards {
		c.errorf("wards.budget", "wards", nil, "%d ward spots a side, at most %d", len(m.Wards), MaxWards)
	}
	lim := m.Size/2 - edgeMargin
	for k, w := range m.Wards {
		path := fmt.Sprintf("wards[%d].pos", k)
		p := w.Pos
		if p[0] == 0 || p[1] == 0 {
			c.errorf("wards.axis", path, pt(p), "ward spot (%g, %g) has a coordinate of 0: the bots' ward condition "+
				"takes it for no spot", p[0], p[1])
		}
		if math.Abs(p[0]) > lim || math.Abs(p[1]) > lim {
			c.errorf("size.bounds", path, pt(p), "(%g, %g) lies outside the level (+-%g m, %g m from its edge)",
				p[0], p[1], m.Size/2, edgeMargin)
		}
		if d := math.Hypot(p[0], p[1]); d < WardCentreRadius {
			c.warnf("wards.centre", path, pt(p), "ward spot %.0f m from the centre: only a Hunter wards it (the match "+
				"host keeps the others %g m away)", d, WardCentreRadius)
		}
	}
	if cm == nil || len(m.Wards) == 0 {
		return
	}
	comp := cm.Player.Components()
	at := func(p Point) int {
		bx, by := cm.BlockAt(p)
		if i := cm.Player.Near(bx, by, 1); i >= 0 {
			return comp[i]
		}
		return -1
	}
	home := at(cm.Layout.Teams[0].Spawns[0])
	for k, p := range cm.WardSpots(0) { // team 2's are the mirror on the mirrored ground
		if cm.Layers.KindAt(p) != Open || home < 0 || at(p) != home {
			c.errorf("wards.reach", fmt.Sprintf("wards[%d].pos", k), pt(p), "ward spot (%g, %g) isn't on open ground "+
				"the players reach from the bases", p[0], p[1])
		}
	}
}

// wardMarkers places the ward spots' markers in the design markerset: the template level's navigation marker that
// allows players (the base's), named WardMarkerName.
func (b *assetBuild) wardMarkers() error {
	if len(b.cm.Map.Wards) == 0 {
		return nil
	}
	both := b.navBoth()
	if both == nil {
		return fmt.Errorf("%s lacks its island markers", TemplateLevel)
	}
	for t := range 2 {
		for k, p := range b.cm.WardSpots(t) {
			m := b.place(setDesign, both, fmt.Sprintf("ward/%d/%d", t+1, k), p)
			m.Set("markerName", WardMarkerName(t, k))
		}
	}
	return nil
}

// navBoth is the template level's first navigation marker that allows players.
func (b *assetBuild) navBoth() *assetdata.Object {
	for _, m := range b.tm.byNoun["navmeshmarker_valid.noun"] {
		if d := sub(m, "navmeshMarkerData"); d != nil {
			if pl, _ := d.Get("allowPlayersHere"); pl == true {
				return m
			}
		}
	}
	return nil
}
